package sqlite

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"

	"github.com/zebaqui/notx-engine/core"
	pairing "github.com/zebaqui/notx-engine/internal/pairing"
	"github.com/zebaqui/notx-engine/repo"
)

const (
	notesSubdir     = "notes"
	indexFile       = "index.db"
	defaultPageSize = 50
)

// normalizeBurstPair returns (aID, aNoteURN, bID, bNoteURN, pairKey) with
// aID <= bID lexicographically, so the same physical burst pair always maps
// to the same row regardless of which note triggered candidate detection.
// pairKey is used as the UNIQUE deduplication key in candidate_relations.
func normalizeBurstPair(
	burstAID, noteURN_A, burstBID, noteURN_B string,
) (normAID, normNoteA, normBID, normNoteB, pairKey string) {
	if burstAID <= burstBID {
		return burstAID, noteURN_A, burstBID, noteURN_B,
			burstAID + ":" + burstBID
	}
	return burstBID, noteURN_B, burstAID, noteURN_A,
		burstBID + ":" + burstAID
}

// walPragmas are applied immediately after opening the database connection.
const walPragmas = `
PRAGMA journal_mode = WAL;
PRAGMA synchronous  = NORMAL;
PRAGMA foreign_keys = ON;
`

// ─────────────────────────────────────────────────────────────────────────────
// Writer goroutine types
// ─────────────────────────────────────────────────────────────────────────────

type writeOp func(db *sql.DB) error
type writeResult struct{ err error }
type writeRequest struct {
	fn   writeOp
	done chan writeResult
}

// ─────────────────────────────────────────────────────────────────────────────
// Provider
// ─────────────────────────────────────────────────────────────────────────────

// SyncNotifier is implemented by the real-time sync bus. Calling Notify
// triggers an immediate push attempt for the given note URN.
type SyncNotifier interface {
	Notify(noteURN string)
}

// Provider is a SQLite-backed implementation of all repository interfaces.
type Provider struct {
	dataDir            string
	notesDir           string
	db                 *sql.DB
	writeCh            chan writeRequest
	wg                 sync.WaitGroup
	closeOnce          sync.Once
	closeErr           error
	scorerCh           chan<- string      // channel to send candidate IDs for BM25 enrichment
	scorerCtxCancel    context.CancelFunc // cancels the scorer goroutine
	inferenceCh        chan<- string      // channel to send note URNs for metadata inference
	inferenceCtxCancel context.CancelFunc // cancels the inference runner goroutine
	burstCfg           core.BurstConfig   // burst extraction config
	syncBus            SyncNotifier       // optional real-time sync bus; may be nil
}

// SetSyncBus registers the real-time sync bus. Must be called before the first
// AppendEvent call if immediate push-on-write is desired.
func (p *Provider) SetSyncBus(bus SyncNotifier) {
	p.syncBus = bus
}

// MarkSyncPending upserts a row into pending_sync for the given note URN.
func (p *Provider) MarkSyncPending(noteURN string) error {
	return p.write(func(db *sql.DB) error {
		_, err := db.Exec(
			`INSERT INTO pending_sync(note_urn, updated_at) VALUES(?, ?)
			 ON CONFLICT(note_urn) DO UPDATE SET updated_at=excluded.updated_at`,
			noteURN, toMs(time.Now()),
		)
		return err
	})
}

// ClearSyncPending removes the pending_sync row for the given note URN.
func (p *Provider) ClearSyncPending(noteURN string) error {
	return p.write(func(db *sql.DB) error {
		_, err := db.Exec(`DELETE FROM pending_sync WHERE note_urn=?`, noteURN)
		return err
	})
}

// ListSyncPending returns all note URNs that have pending sync rows.
func (p *Provider) ListSyncPending() ([]string, error) {
	rows, err := p.db.QueryContext(context.Background(),
		`SELECT note_urn FROM pending_sync ORDER BY updated_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list pending sync: %w", err)
	}
	defer rows.Close()
	var urns []string
	for rows.Next() {
		var urn string
		if err := rows.Scan(&urn); err != nil {
			return nil, fmt.Errorf("sqlite: scan pending sync: %w", err)
		}
		urns = append(urns, urn)
	}
	return urns, rows.Err()
}

// New opens (or creates) the SQLite index and starts the writer goroutine.
func New(dataDir string, rebuild func(ctx context.Context, notesDir string, db *sql.DB) error) (*Provider, error) {
	notesDir := filepath.Join(dataDir, notesSubdir)
	if err := os.MkdirAll(notesDir, 0o755); err != nil {
		return nil, fmt.Errorf("sqlite provider: create notes dir: %w", err)
	}
	if rebuild == nil {
		rebuild = func(_ context.Context, _ string, _ *sql.DB) error { return nil }
	}
	dbPath := filepath.Join(dataDir, indexFile)
	p := &Provider{
		dataDir:  dataDir,
		notesDir: notesDir,
		writeCh:  make(chan writeRequest, 64),
	}
	if err := p.ensureIndex(context.Background(), dbPath, rebuild); err != nil {
		return nil, fmt.Errorf("sqlite provider: ensure index: %w", err)
	}
	p.wg.Add(1)
	go p.writerLoop()

	// Start the background BM25 scorer goroutine.
	scorerCtx, scorerCancel := context.WithCancel(context.Background())
	p.scorerCtxCancel = scorerCancel
	p.scorerCh = StartScorer(scorerCtx, p.db, p.write, DefaultScorerConfig())
	p.burstCfg = core.DefaultBurstConfig()

	// Start the background inference runner goroutine.
	inferenceCtx, inferenceCancel := context.WithCancel(context.Background())
	p.inferenceCtxCancel = inferenceCancel
	p.inferenceCh = StartInferenceRunner(inferenceCtx, p.db, p.write, p.burstCfg, DefaultInferenceConfig())

	return p, nil
}

// Close shuts down the writer goroutine and closes the database connection.
func (p *Provider) Close() error {
	p.closeOnce.Do(func() {
		if p.scorerCtxCancel != nil {
			p.scorerCtxCancel()
		}
		if p.inferenceCtxCancel != nil {
			p.inferenceCtxCancel()
		}
		close(p.writeCh)
		p.wg.Wait()
		if p.db != nil {
			p.closeErr = p.db.Close()
		}
	})
	return p.closeErr
}

func (p *Provider) write(fn writeOp) error {
	done := make(chan writeResult, 1)
	p.writeCh <- writeRequest{fn: fn, done: done}
	res := <-done
	return res.err
}

func (p *Provider) writerLoop() {
	defer p.wg.Done()
	for req := range p.writeCh {
		err := req.fn(p.db)
		req.done <- writeResult{err: err}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// EnsureIndex startup sequence
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) ensureIndex(ctx context.Context, dbPath string, rebuild func(context.Context, string, *sql.DB) error) error {
	needRebuild := false
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		needRebuild = true
	}
	db, err := openDB(dbPath)
	if err != nil {
		return err
	}
	p.db = db
	if _, err := db.ExecContext(ctx, walPragmas); err != nil {
		return fmt.Errorf("sqlite provider: apply WAL pragmas: %w", err)
	}
	if err := applySchema(ctx, db); err != nil {
		return err
	}
	if !needRebuild {
		sv, err := schemaVersion(ctx, db)
		if err != nil {
			return err
		}
		if err := runMigrations(ctx, db, sv); err != nil {
			return err
		}
		pv, err := projectionVersion(ctx, db)
		if err != nil {
			return err
		}
		if pv < currentProjectionVersion {
			needRebuild = true
		}
		if !needRebuild && !integrityOK(ctx, db) {
			_ = db.Close()
			_ = os.Remove(dbPath)
			db2, err2 := openDB(dbPath)
			if err2 != nil {
				return err2
			}
			p.db = db2
			db = db2
			if _, err2 = db.ExecContext(ctx, walPragmas); err2 != nil {
				return fmt.Errorf("sqlite provider: apply WAL pragmas after rebuild: %w", err2)
			}
			if err2 = applySchema(ctx, db); err2 != nil {
				return err2
			}
			needRebuild = true
		}
	}
	if needRebuild {
		if err := rebuild(ctx, p.notesDir, db); err != nil {
			return fmt.Errorf("sqlite provider: rebuild: %w", err)
		}
		if err := setProjectionVersion(ctx, db, currentProjectionVersion); err != nil {
			return err
		}
		if sv, _ := schemaVersion(ctx, db); sv == 0 {
			if err := runMigrations(ctx, db, 0); err != nil {
				return err
			}
		}
	}
	return nil
}

func openDB(path string) (*sql.DB, error) {
	dsn := path + "?_busy_timeout=5000&_cache_size=-20000"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite provider: open db: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return db, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Cursor / pagination helpers
// ─────────────────────────────────────────────────────────────────────────────

func encodeCursor(updatedAtMs int64, urn string) string {
	raw := fmt.Sprintf("%d\x00%s", updatedAtMs, urn)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(token string) (updatedAtMs int64, urn string, err error) {
	if token == "" {
		return 0, "", nil
	}
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return 0, "", fmt.Errorf("sqlite: invalid page token: %w", err)
	}
	parts := strings.SplitN(string(b), "\x00", 2)
	if len(parts) != 2 {
		return 0, "", fmt.Errorf("sqlite: malformed page token")
	}
	var ms int64
	if _, scanErr := fmt.Sscanf(parts[0], "%d", &ms); scanErr != nil {
		return 0, "", fmt.Errorf("sqlite: page token timestamp: %w", scanErr)
	}
	return ms, parts[1], nil
}

func resolvePageSize(n int) int {
	if n > 0 {
		return n
	}
	return defaultPageSize
}

// ─────────────────────────────────────────────────────────────────────────────
// Time helpers
// ─────────────────────────────────────────────────────────────────────────────

func toMs(t time.Time) int64 { return t.UnixMilli() }

func fromMs(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

// ─────────────────────────────────────────────────────────────────────────────
// NoteRepository — Create
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) Create(ctx context.Context, note *core.Note) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	urn := note.URN.String()
	projectURN := ""
	if note.ProjectURN != nil {
		projectURN = note.ProjectURN.String()
	}
	folderURN := ""
	if note.FolderURN != nil {
		folderURN = note.FolderURN.String()
	}
	return p.write(func(db *sql.DB) error {
		_, err := db.Exec(
			`INSERT INTO notes(urn, project_urn, folder_urn, note_type, title, preview, head_seq, deleted, created_at, updated_at)
			 VALUES(?, ?, ?, ?, ?, '', 0, 0, ?, ?)`,
			urn, projectURN, folderURN, note.NoteType.String(), note.Name,
			toMs(note.CreatedAt), toMs(note.UpdatedAt),
		)
		if err != nil {
			if isSQLiteConstraintUnique(err) {
				return fmt.Errorf("%w: %s", repo.ErrAlreadyExists, urn)
			}
			return fmt.Errorf("sqlite: create note: %w", err)
		}
		if note.NoteType == core.NoteTypeNormal {
			_, _ = db.Exec(`INSERT OR IGNORE INTO note_content(urn, content) VALUES(?, '')`, urn)
		}
		return nil
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// NoteRepository — Get
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) Get(ctx context.Context, urn string) (*core.Note, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	row := p.db.QueryRowContext(ctx,
		`SELECT n.urn, n.project_urn, n.folder_urn, n.note_type, n.title,
		        COALESCE(nc.content, '') AS content, n.head_seq, n.deleted,
		        n.created_at, n.updated_at
		 FROM notes n LEFT JOIN note_content nc ON nc.urn = n.urn
		 WHERE n.urn = ?`, urn)
	return scanNote(row)
}

func scanNote(row *sql.Row) (*core.Note, error) {
	var urnStr, projectURNStr, folderURNStr, noteTypeStr, title, content string
	var headSeq int
	var deleted bool
	var createdAtMs, updatedAtMs int64
	if err := row.Scan(&urnStr, &projectURNStr, &folderURNStr, &noteTypeStr, &title,
		&content, &headSeq, &deleted, &createdAtMs, &updatedAtMs); err != nil {
		if err == sql.ErrNoRows {
			return nil, repo.ErrNotFound
		}
		return nil, fmt.Errorf("sqlite: scan note: %w", err)
	}
	return buildNote(urnStr, projectURNStr, folderURNStr, noteTypeStr, title, content,
		headSeq, deleted, createdAtMs, updatedAtMs)
}

func buildNote(urnStr, projectURNStr, folderURNStr, noteTypeStr, title, content string,
	headSeq int, deleted bool, createdAtMs, updatedAtMs int64) (*core.Note, error) {
	noteURN, err := core.ParseURN(urnStr)
	if err != nil {
		return nil, fmt.Errorf("sqlite: parse note URN %q: %w", urnStr, err)
	}
	createdAt := fromMs(createdAtMs)
	updatedAt := fromMs(updatedAtMs)
	noteType, _ := core.ParseNoteType(noteTypeStr)

	var note *core.Note
	if noteType == core.NoteTypeNormal {
		note = core.NewNoteAtSequence(noteURN, title, createdAt, updatedAt, headSeq, content)
	} else {
		note = core.NewSecureNote(noteURN, title, createdAt)
		note.UpdatedAt = updatedAt
	}
	note.Deleted = deleted

	if projectURNStr != "" {
		u, err := core.ParseURN(projectURNStr)
		if err == nil {
			note.ProjectURN = &u
		}
	}
	if folderURNStr != "" {
		u, err := core.ParseURN(folderURNStr)
		if err == nil {
			note.FolderURN = &u
		}
	}
	_ = headSeq
	return note, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// NoteRepository — List
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) List(ctx context.Context, opts repo.ListOptions) (*repo.ListResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	notes, nextToken, err := queryNotes(ctx, p.db, opts)
	if err != nil {
		return nil, err
	}
	return &repo.ListResult{Notes: notes, NextPageToken: nextToken}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// NoteRepository — Update
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) Update(ctx context.Context, note *core.Note) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	urn := note.URN.String()
	projectURN := ""
	if note.ProjectURN != nil {
		projectURN = note.ProjectURN.String()
	}
	folderURN := ""
	if note.FolderURN != nil {
		folderURN = note.FolderURN.String()
	}
	return p.write(func(db *sql.DB) error {
		res, err := db.Exec(
			`UPDATE notes SET project_urn=?, folder_urn=?, title=?, deleted=?, updated_at=?
			 WHERE urn=?`,
			projectURN, folderURN, note.Name, boolToInt(note.Deleted), toMs(note.UpdatedAt), urn,
		)
		if err != nil {
			return fmt.Errorf("sqlite: update note: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return fmt.Errorf("%w: %s", repo.ErrNotFound, urn)
		}
		return nil
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// NoteRepository — Delete
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) Delete(ctx context.Context, urn string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.write(func(db *sql.DB) error {
		res, err := db.Exec(
			`UPDATE notes SET deleted=1, updated_at=? WHERE urn=?`,
			toMs(time.Now().UTC()), urn,
		)
		if err != nil {
			return fmt.Errorf("sqlite: delete note: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return fmt.Errorf("%w: %s", repo.ErrNotFound, urn)
		}

		// ── Purge context bursts for the deleted note ─────────────────────
		if _, err := db.Exec(`DELETE FROM context_bursts WHERE note_urn=?`, urn); err != nil {
			return fmt.Errorf("sqlite: delete note bursts: %w", err)
		}
		// Defensive explicit FTS delete (content= table does not auto-cascade).
		_, _ = db.Exec(`DELETE FROM context_bursts_fts WHERE note_urn=?`, urn)

		// ── Purge pending candidates that reference this note ─────────────
		if _, err := db.Exec(
			`DELETE FROM candidate_relations
			 WHERE (note_urn_a=? OR note_urn_b=?) AND status='pending'`,
			urn, urn,
		); err != nil {
			return fmt.Errorf("sqlite: delete note pending candidates: %w", err)
		}

		// TODO: When note un-delete (restore) is implemented, re-run burst extraction
		// from the note's full event history and re-run candidate detection against the
		// project's burst pool for any note that lacks an existing link for the same
		// candidate pair (pair_key).

		return nil
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// NoteRepository — AppendEvent
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) AppendEvent(ctx context.Context, event *core.Event, opts repo.AppendEventOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	noteURN := event.NoteURN.String()
	err := p.write(func(db *sql.DB) error {
		var headSeq int
		var noteTypeStr string
		err := db.QueryRow(`SELECT head_seq, note_type FROM notes WHERE urn=?`, noteURN).
			Scan(&headSeq, &noteTypeStr)
		if err == sql.ErrNoRows {
			return fmt.Errorf("%w: %s", repo.ErrNotFound, noteURN)
		}
		if err != nil {
			return fmt.Errorf("sqlite: append event read head: %w", err)
		}
		if opts.ExpectSequence > 0 && headSeq+1 != opts.ExpectSequence {
			return fmt.Errorf("%w: expected %d got %d", repo.ErrSequenceConflict, opts.ExpectSequence, headSeq+1)
		}
		if event.Sequence != headSeq+1 {
			return fmt.Errorf("%w: expected %d got %d", repo.ErrSequenceConflict, headSeq+1, event.Sequence)
		}

		payloadJSON, _ := json.Marshal(event.Entries)
		authorURN := event.AuthorURN.String()
		eventURNStr := event.URN.String()
		if eventURNStr == "" || eventURNStr == (core.URN{}).String() {
			event.URN = core.NewURN(core.ObjectTypeEvent)
			eventURNStr = event.URN.String()
		}
		_, err = db.Exec(
			`INSERT INTO events(urn, note_urn, sequence, author_urn, label, payload, created_at)
			 VALUES(?, ?, ?, ?, ?, ?, ?)`,
			eventURNStr, noteURN, event.Sequence, authorURN, event.Label, string(payloadJSON),
			toMs(event.CreatedAt),
		)
		if err != nil {
			return fmt.Errorf("sqlite: insert event: %w", err)
		}

		newSeq := event.Sequence
		noteType, _ := core.ParseNoteType(noteTypeStr)
		if noteType == core.NoteTypeNormal {
			var currentContent string
			_ = db.QueryRow(`SELECT content FROM note_content WHERE urn=?`, noteURN).Scan(&currentContent)
			newContent := applyEntriesToContent(currentContent, event.Entries)
			_, err = db.Exec(
				`INSERT INTO note_content(urn, content) VALUES(?, ?)
				 ON CONFLICT(urn) DO UPDATE SET content=excluded.content`,
				noteURN, newContent,
			)
			if err != nil {
				return fmt.Errorf("sqlite: update note_content: %w", err)
			}
			_, _ = db.Exec(
				`INSERT INTO notes_fts(urn, title, body) VALUES(?, ?, ?)
				 ON CONFLICT(urn) DO UPDATE SET body=excluded.body`,
				noteURN, event.Label, newContent,
			)
			// ── Context graph: burst extraction and candidate detection ────────
			// This runs inside the write goroutine, after FTS update, before commit.
			// Errors are logged and suppressed — never propagate to the caller.
			p.extractBurstsForEvent(db, event, newContent)
		}

		_, err = db.Exec(
			`UPDATE notes SET head_seq=?, updated_at=? WHERE urn=?`,
			newSeq, toMs(event.CreatedAt), noteURN,
		)
		if err != nil {
			return fmt.Errorf("sqlite: update note head_seq: %w", err)
		}

		// Mark this note as needing a cloud sync. Errors are intentionally
		// suppressed — a missing pending_sync row just means the 30-second
		// backlog sweep will catch it on next reconnect.
		_, _ = db.Exec(
			`INSERT INTO pending_sync(note_urn, updated_at) VALUES(?, ?)
			 ON CONFLICT(note_urn) DO UPDATE SET updated_at=excluded.updated_at`,
			noteURN, toMs(event.CreatedAt),
		)
		return nil
	})
	if err != nil {
		return err
	}
	if p.syncBus != nil {
		p.syncBus.Notify(noteURN)
	}
	return nil
}

// extractBurstsForEvent runs context burst extraction for a single event.
// MUST be called from inside the writer goroutine — it uses db directly and
// never calls p.write(), avoiding write-channel re-entrancy deadlock.
// All errors are logged and suppressed — the write path must not be blocked.
// placeholders returns a comma-separated string of n "?" SQL placeholders,
// used to build variable-length IN (...) clauses for raw SQL statements.
func placeholders(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = "?"
	}
	return strings.Join(parts, ",")
}

func (p *Provider) extractBurstsForEvent(db *sql.DB, event *core.Event, content string) {
	noteURN := event.NoteURN.String()

	// ── 1. Read project / folder URN directly from the already-open db ───────
	var projURN, fldURN, noteTitle string
	_ = db.QueryRow(`SELECT project_urn, folder_urn, title FROM notes WHERE urn=?`, noteURN).
		Scan(&projURN, &fldURN, &noteTitle)

	cfg := p.burstCfg

	// ── 2. Build a minimal core.Note for ExtractBursts ───────────────────────
	noteURNParsed, err := core.ParseURN(noteURN)
	if err != nil {
		return
	}
	note := &core.Note{URN: noteURNParsed}
	if projURN != "" {
		if u, err2 := core.ParseURN(projURN); err2 == nil {
			note.ProjectURN = &u
		}
	}
	if fldURN != "" {
		if u, err2 := core.ParseURN(fldURN); err2 == nil {
			note.FolderURN = &u
		}
	}

	// ── 3A. Compute affected line windows from the event entries ──────────────
	noteLines := splitContentLines(content)
	totalLines := len(noteLines)
	affectedWindows := core.GroupAffectedLines(event.Entries, cfg.WindowLines, totalLines)

	// ── 3B. Find existing bursts for this note that overlap any affected window,
	//        then delete them and their pending candidates. ────────────────────
	if len(affectedWindows) > 0 {
		existingRows, err2 := db.Query(
			`SELECT id, line_start, line_end FROM context_bursts WHERE note_urn=?`,
			noteURN,
		)
		if err2 != nil {
			fmt.Printf("context: WARN: query existing bursts for overlap check: %v\n", err2)
		} else {
			type existingBurst struct {
				id        string
				lineStart int
				lineEnd   int
			}
			var overlapping []existingBurst
			for existingRows.Next() {
				var eb existingBurst
				if scanErr := existingRows.Scan(&eb.id, &eb.lineStart, &eb.lineEnd); scanErr == nil {
					for _, w := range affectedWindows {
						if eb.lineStart <= w.End && eb.lineEnd >= w.Start {
							overlapping = append(overlapping, eb)
							break
						}
					}
				}
			}
			existingRows.Close()

			// ── 3C. Delete overlapping bursts and their pending candidates ────
			if len(overlapping) > 0 {
				ids := make([]any, len(overlapping))
				for i, ob := range overlapping {
					ids[i] = ob.id
				}
				ph := placeholders(len(ids))

				if _, delErr := db.Exec(
					`DELETE FROM context_bursts WHERE id IN (`+ph+`)`, ids...,
				); delErr != nil {
					fmt.Printf("context: WARN: delete overlapping bursts: %v\n", delErr)
				}
				// Defensive explicit FTS delete.
				_, _ = db.Exec(
					`DELETE FROM context_bursts_fts WHERE id IN (`+ph+`)`, ids...,
				)
				if _, delErr := db.Exec(
					`DELETE FROM candidate_relations
					 WHERE (burst_a_id IN (`+ph+`) OR burst_b_id IN (`+ph+`)) AND status='pending'`,
					append(ids, ids...)...,
				); delErr != nil {
					fmt.Printf("context: WARN: delete pending candidates for overlapping bursts: %v\n", delErr)
				}
			}
		}
	}

	// ── 4. Extract bursts (pure in-memory) ────────────────────────────────────
	bursts := core.ExtractBursts(note, event, noteLines, cfg)
	if len(bursts) == 0 {
		return
	}

	// ── 5. Consecutive similarity skip check (raw SQL) ────────────────────────
	var recentTokensStr string
	var recentCreatedMs int64
	err = db.QueryRow(
		`SELECT tokens, created_at FROM context_bursts WHERE note_urn=? ORDER BY created_at DESC LIMIT 1`,
		noteURN,
	).Scan(&recentTokensStr, &recentCreatedMs)
	if err == nil && recentTokensStr != "" {
		recentTokens := strings.Fields(recentTokensStr)
		cutoff := event.CreatedAt.Add(-time.Duration(cfg.SkipWindowSeconds) * time.Second)
		recentTime := fromMs(recentCreatedMs)
		if recentTime.After(cutoff) && core.SimilaritySkip(recentTokens, bursts[0].Tokens, cfg.SkipThreshold) {
			return
		}
	}

	// ── 6. Store each burst and detect candidates — all raw SQL ───────────────
	var newCandidateIDs []string

	for _, b := range bursts {
		tokensStr := strings.Join(b.Tokens, " ")
		truncatedInt := 0
		if b.Truncated {
			truncatedInt = 1
		}
		createdMs := toMs(b.CreatedAt)

		// Insert burst row
		_, err := db.Exec(
			`INSERT INTO context_bursts
			   (id, note_urn, project_urn, folder_urn, author_urn,
			    sequence, line_start, line_end, text, tokens, truncated, created_at)
			 VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
			b.ID, b.NoteURN, b.ProjectURN, b.FolderURN, b.AuthorURN,
			b.Sequence, b.LineStart, b.LineEnd, b.Text, tokensStr,
			truncatedInt, createdMs,
		)
		if err != nil {
			fmt.Printf("context: WARN: insert burst: %v\n", err)
			continue
		}

		// Insert FTS5 row (best-effort — ignore errors)
		_, _ = db.Exec(
			`INSERT INTO context_bursts_fts(id, note_urn, project_urn, tokens) VALUES(?,?,?,?)`,
			b.ID, b.NoteURN, b.ProjectURN, tokensStr,
		)

		// Skip candidate detection for project-less notes
		if projURN == "" {
			continue
		}

		// Fetch recent bursts in project for candidate detection (raw SQL).
		// Exclude bursts from deleted notes.
		cutoffMs := toMs(time.Now().UTC().Add(-time.Duration(cfg.CandidateLookbackDays) * 24 * time.Hour))
		rows, err2 := db.Query(
			`SELECT id, note_urn, project_urn, tokens
			 FROM context_bursts
			 WHERE project_urn=? AND created_at>=? AND note_urn!=?
			   AND note_urn NOT IN (SELECT urn FROM notes WHERE deleted=1)
			 ORDER BY created_at DESC LIMIT ?`,
			projURN, cutoffMs, noteURN, cfg.CandidateLookbackN,
		)
		if err2 != nil {
			fmt.Printf("context: WARN: fetch recent bursts: %v\n", err2)
			continue
		}

		var recentCore []core.Burst
		for rows.Next() {
			var rID, rNoteURN, rProjectURN, rTokens string
			if scanErr := rows.Scan(&rID, &rNoteURN, &rProjectURN, &rTokens); scanErr != nil {
				continue
			}
			recentCore = append(recentCore, core.Burst{
				ID:         rID,
				NoteURN:    rNoteURN,
				ProjectURN: rProjectURN,
				Tokens:     strings.Fields(rTokens),
			})
		}
		rows.Close()

		// Detect candidates (in-memory Jaccard)
		pairs := core.DetectCandidates(b, recentCore, cfg.OverlapThreshold)
		for _, pair := range pairs {
			candID, err3 := uuid.NewV7()
			if err3 != nil {
				continue
			}
			idStr := candID.String()
			nAID, nNoteA, nBID, nNoteB, pairKey := normalizeBurstPair(
				pair.BurstA.ID, pair.BurstA.NoteURN,
				pair.BurstB.ID, pair.BurstB.NoteURN,
			)
			_, err4 := db.Exec(
				`INSERT OR IGNORE INTO candidate_relations
					   (id, burst_a_id, burst_b_id, note_urn_a, note_urn_b,
					    project_urn, overlap_score, bm25_score, status, created_at, pair_key)
					 VALUES(?,?,?,?,?,?,?,0,'pending',?,?)`,
				idStr,
				nAID, nBID,
				nNoteA, nNoteB,
				pair.ProjectURN,
				pair.OverlapScore,
				createdMs,
				pairKey,
			)
			if err4 != nil {
				fmt.Printf("context: WARN: insert candidate: %v\n", err4)
				continue
			}
			newCandidateIDs = append(newCandidateIDs, idStr)
		}
	}

	// ── 7. Send new candidate IDs to scorer (non-blocking) ───────────────────
	for _, id := range newCandidateIDs {
		select {
		case p.scorerCh <- id:
		default:
			// Channel full — drop silently
		}
	}

	// ── 8. Queue for inference if the note lacks a title or project ──────────
	// The inference runner is advisory: if the channel is full, we drop silently.
	if noteTitle == "" || projURN == "" {
		select {
		case p.inferenceCh <- noteURN:
		default:
			// Channel full — drop silently.
		}
	}
}

func applyEntriesToContent(content string, entries []core.LineEntry) string {
	lines := splitContentLines(content)
	for _, e := range entries {
		ln := e.LineNumber - 1
		switch e.Op {
		case core.LineOpSet:
			for len(lines) <= ln {
				lines = append(lines, "")
			}
			lines[ln] = e.Content
		case core.LineOpSetEmpty:
			for len(lines) <= ln {
				lines = append(lines, "")
			}
			lines[ln] = ""
		case core.LineOpDelete:
			if ln < len(lines) {
				lines = append(lines[:ln], lines[ln+1:]...)
			}
		}
	}
	return strings.Join(lines, "\n")
}

func splitContentLines(content string) []string {
	if content == "" {
		return []string{}
	}
	return strings.Split(content, "\n")
}

// ─────────────────────────────────────────────────────────────────────────────
// NoteRepository — Events
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) Events(ctx context.Context, noteURN string, fromSequence int) ([]*core.Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := p.db.QueryContext(ctx,
		`SELECT urn, note_urn, sequence, author_urn, label, payload, created_at
		 FROM events WHERE note_urn=? AND sequence >= ? ORDER BY sequence ASC`,
		noteURN, fromSequence)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list events: %w", err)
	}
	defer rows.Close()
	var events []*core.Event
	for rows.Next() {
		var urnStr, noteURNStr, authorURNStr, label, payloadJSON string
		var sequence int
		var createdMs int64
		if err := rows.Scan(&urnStr, &noteURNStr, &sequence, &authorURNStr, &label, &payloadJSON, &createdMs); err != nil {
			return nil, fmt.Errorf("sqlite: scan event: %w", err)
		}
		evURN, _ := core.ParseURN(urnStr)
		if evURN == (core.URN{}) {
			evURN = core.NewURN(core.ObjectTypeEvent)
		}
		nURN, _ := core.ParseURN(noteURNStr)
		aURN, _ := core.ParseURN(authorURNStr)
		var entries []core.LineEntry
		_ = json.Unmarshal([]byte(payloadJSON), &entries)
		events = append(events, &core.Event{
			URN:       evURN,
			NoteURN:   nURN,
			Sequence:  sequence,
			AuthorURN: aURN,
			Label:     label,
			Entries:   entries,
			CreatedAt: fromMs(createdMs),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterate events: %w", err)
	}
	if events == nil {
		events = []*core.Event{}
	}
	return events, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// NoteRepository — UpdateEventWrappedKeys
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) UpdateEventWrappedKeys(ctx context.Context, noteURN string, wrappedKeys map[string][]byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	// Wrapped keys require a dedicated event_wrapped_keys table not yet in the
	// schema. Return 0 updated keys for now.
	return 0, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// NoteRepository — ReceiveSharedNote
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) ReceiveSharedNote(ctx context.Context, note *core.Note, events []*core.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	noteURN := note.URN.String()
	projectURN := ""
	if note.ProjectURN != nil {
		projectURN = note.ProjectURN.String()
	}
	folderURN := ""
	if note.FolderURN != nil {
		folderURN = note.FolderURN.String()
	}
	return p.write(func(db *sql.DB) error {
		_, err := db.Exec(
			`INSERT INTO notes(urn, project_urn, folder_urn, note_type, title, preview, head_seq, deleted, created_at, updated_at)
			 VALUES(?, ?, ?, ?, ?, '', 0, ?, ?, ?)
			 ON CONFLICT(urn) DO UPDATE SET
			 	project_urn=excluded.project_urn,
			 	folder_urn=excluded.folder_urn,
			 	title=excluded.title,
			 	deleted=excluded.deleted,
			 	updated_at=excluded.updated_at`,
			noteURN, projectURN, folderURN, note.NoteType.String(), note.Name,
			boolToInt(note.Deleted), toMs(note.CreatedAt), toMs(note.UpdatedAt),
		)
		if err != nil {
			return fmt.Errorf("sqlite: receive shared note upsert: %w", err)
		}
		if note.NoteType == core.NoteTypeNormal {
			_, _ = db.Exec(`INSERT OR IGNORE INTO note_content(urn, content) VALUES(?, '')`, noteURN)
		}

		var headSeq int
		var noteTypeStr string
		_ = db.QueryRow(`SELECT head_seq, note_type FROM notes WHERE urn=?`, noteURN).
			Scan(&headSeq, &noteTypeStr)
		noteType, _ := core.ParseNoteType(noteTypeStr)

		var content string
		if noteType == core.NoteTypeNormal {
			_ = db.QueryRow(`SELECT content FROM note_content WHERE urn=?`, noteURN).Scan(&content)
		}

		for _, ev := range events {
			if ev.Sequence <= headSeq {
				continue
			}
			payloadJSON, _ := json.Marshal(ev.Entries)
			authorURN := ev.AuthorURN.String()
			evURNStr := ev.URN.String()
			if evURNStr == "" || evURNStr == (core.URN{}).String() {
				ev.URN = core.NewURN(core.ObjectTypeEvent)
				evURNStr = ev.URN.String()
			}
			_, err = db.Exec(
				`INSERT OR IGNORE INTO events(urn, note_urn, sequence, author_urn, label, payload, created_at)
				 VALUES(?, ?, ?, ?, ?, ?, ?)`,
				evURNStr, noteURN, ev.Sequence, authorURN, ev.Label, string(payloadJSON), toMs(ev.CreatedAt),
			)
			if err != nil {
				return fmt.Errorf("sqlite: receive shared note insert event: %w", err)
			}
			if noteType == core.NoteTypeNormal {
				content = applyEntriesToContent(content, ev.Entries)
			}
			headSeq = ev.Sequence
		}

		if noteType == core.NoteTypeNormal {
			_, err = db.Exec(
				`INSERT INTO note_content(urn, content) VALUES(?, ?)
				 ON CONFLICT(urn) DO UPDATE SET content=excluded.content`,
				noteURN, content,
			)
			if err != nil {
				return fmt.Errorf("sqlite: receive shared note update content: %w", err)
			}
		}
		_, err = db.Exec(`UPDATE notes SET head_seq=? WHERE urn=?`, headSeq, noteURN)
		return err
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// NoteRepository — Search
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) Search(ctx context.Context, opts repo.SearchOptions) (*repo.SearchResults, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return searchNotes(ctx, p.db, opts)
}

// ─────────────────────────────────────────────────────────────────────────────
// ProjectRepository
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) CreateProject(ctx context.Context, proj *core.Project) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	urn := proj.URN.String()
	return p.write(func(db *sql.DB) error {
		_, err := db.Exec(
			`INSERT INTO projects(urn, name, deleted, created_at, updated_at) VALUES(?, ?, 0, ?, ?)`,
			urn, proj.Name, toMs(proj.CreatedAt), toMs(proj.UpdatedAt),
		)
		if err != nil {
			if isSQLiteConstraintUnique(err) {
				return fmt.Errorf("%w: %s", repo.ErrAlreadyExists, urn)
			}
			return fmt.Errorf("sqlite: create project: %w", err)
		}
		return nil
	})
}

func (p *Provider) GetProject(ctx context.Context, urn string) (*core.Project, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return queryProject(ctx, p.db, urn)
}

func (p *Provider) ListProjects(ctx context.Context, opts repo.ProjectListOptions) (*repo.ProjectListResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return queryProjects(ctx, p.db, opts)
}

func (p *Provider) UpdateProject(ctx context.Context, proj *core.Project) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	urn := proj.URN.String()
	return p.write(func(db *sql.DB) error {
		res, err := db.Exec(
			`UPDATE projects SET name=?, deleted=?, updated_at=? WHERE urn=?`,
			proj.Name, boolToInt(proj.Deleted), toMs(proj.UpdatedAt), urn,
		)
		if err != nil {
			return fmt.Errorf("sqlite: update project: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return fmt.Errorf("%w: %s", repo.ErrNotFound, urn)
		}
		return nil
	})
}

func (p *Provider) DeleteProject(ctx context.Context, urn string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.write(func(db *sql.DB) error {
		res, err := db.Exec(
			`UPDATE projects SET deleted=1, updated_at=? WHERE urn=?`,
			toMs(time.Now().UTC()), urn,
		)
		if err != nil {
			return fmt.Errorf("sqlite: delete project: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return fmt.Errorf("%w: %s", repo.ErrNotFound, urn)
		}
		return nil
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// FolderRepository
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) CreateFolder(ctx context.Context, f *core.Folder) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	urn := f.URN.String()
	projectURN := f.ProjectURN.String()
	return p.write(func(db *sql.DB) error {
		_, err := db.Exec(
			`INSERT INTO folders(urn, project_urn, name, deleted, created_at, updated_at) VALUES(?, ?, ?, 0, ?, ?)`,
			urn, projectURN, f.Name, toMs(f.CreatedAt), toMs(f.UpdatedAt),
		)
		if err != nil {
			if isSQLiteConstraintUnique(err) {
				return fmt.Errorf("%w: %s", repo.ErrAlreadyExists, urn)
			}
			return fmt.Errorf("sqlite: create folder: %w", err)
		}
		return nil
	})
}

func (p *Provider) GetFolder(ctx context.Context, urn string) (*core.Folder, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return queryFolder(ctx, p.db, urn)
}

func (p *Provider) ListFolders(ctx context.Context, opts repo.FolderListOptions) (*repo.FolderListResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return queryFolders(ctx, p.db, opts)
}

func (p *Provider) UpdateFolder(ctx context.Context, f *core.Folder) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	urn := f.URN.String()
	return p.write(func(db *sql.DB) error {
		res, err := db.Exec(
			`UPDATE folders SET name=?, deleted=?, updated_at=? WHERE urn=?`,
			f.Name, boolToInt(f.Deleted), toMs(f.UpdatedAt), urn,
		)
		if err != nil {
			return fmt.Errorf("sqlite: update folder: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return fmt.Errorf("%w: %s", repo.ErrNotFound, urn)
		}
		return nil
	})
}

func (p *Provider) DeleteFolder(ctx context.Context, urn string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.write(func(db *sql.DB) error {
		res, err := db.Exec(
			`UPDATE folders SET deleted=1, updated_at=? WHERE urn=?`,
			toMs(time.Now().UTC()), urn,
		)
		if err != nil {
			return fmt.Errorf("sqlite: delete folder: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return fmt.Errorf("%w: %s", repo.ErrNotFound, urn)
		}
		return nil
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// DeviceRepository
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) RegisterDevice(ctx context.Context, d *core.Device) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	urn := d.URN.String()
	ownerURN := d.OwnerURN.String()
	return p.write(func(db *sql.DB) error {
		_, err := db.Exec(
			`INSERT INTO devices(urn, name, owner_urn, public_key_b64, role, approval_status, revoked, registered_at, last_seen_at)
			 VALUES(?, ?, ?, ?, ?, ?, 0, ?, 0)`,
			urn, d.Name, ownerURN, d.PublicKeyB64, string(d.Role), string(d.ApprovalStatus),
			toMs(d.RegisteredAt),
		)
		if err != nil {
			if isSQLiteConstraintUnique(err) {
				return fmt.Errorf("%w: %s", repo.ErrAlreadyExists, urn)
			}
			return fmt.Errorf("sqlite: register device: %w", err)
		}
		return nil
	})
}

func (p *Provider) GetDevice(ctx context.Context, urn string) (*core.Device, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return queryDevice(ctx, p.db, urn)
}

func (p *Provider) ListDevices(ctx context.Context, opts repo.DeviceListOptions) (*repo.DeviceListResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return queryDevices(ctx, p.db, opts)
}

func (p *Provider) UpdateDevice(ctx context.Context, d *core.Device) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	urn := d.URN.String()
	return p.write(func(db *sql.DB) error {
		res, err := db.Exec(
			`UPDATE devices SET name=?, approval_status=?, revoked=?, last_seen_at=? WHERE urn=?`,
			d.Name, string(d.ApprovalStatus), boolToInt(d.Revoked), toMs(d.LastSeenAt), urn,
		)
		if err != nil {
			return fmt.Errorf("sqlite: update device: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return fmt.Errorf("%w: %s", repo.ErrNotFound, urn)
		}
		return nil
	})
}

func (p *Provider) RevokeDevice(ctx context.Context, urn string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.write(func(db *sql.DB) error {
		res, err := db.Exec(`UPDATE devices SET revoked=1 WHERE urn=?`, urn)
		if err != nil {
			return fmt.Errorf("sqlite: revoke device: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return fmt.Errorf("%w: %s", repo.ErrNotFound, urn)
		}
		return nil
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// UserRepository
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) CreateUser(ctx context.Context, u *core.User) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	urn := u.URN.String()
	return p.write(func(db *sql.DB) error {
		_, err := db.Exec(
			`INSERT INTO users(urn, display_name, email, deleted, created_at, updated_at) VALUES(?, ?, ?, 0, ?, ?)`,
			urn, u.DisplayName, u.Email, toMs(u.CreatedAt), toMs(u.UpdatedAt),
		)
		if err != nil {
			if isSQLiteConstraintUnique(err) {
				return fmt.Errorf("%w: %s", repo.ErrAlreadyExists, urn)
			}
			return fmt.Errorf("sqlite: create user: %w", err)
		}
		return nil
	})
}

func (p *Provider) GetUser(ctx context.Context, urn string) (*core.User, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return queryUser(ctx, p.db, urn)
}

func (p *Provider) ListUsers(ctx context.Context, opts repo.UserListOptions) (*repo.UserListResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return queryUsers(ctx, p.db, opts)
}

func (p *Provider) UpdateUser(ctx context.Context, u *core.User) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	urn := u.URN.String()
	return p.write(func(db *sql.DB) error {
		res, err := db.Exec(
			`UPDATE users SET display_name=?, email=?, deleted=?, updated_at=? WHERE urn=?`,
			u.DisplayName, u.Email, boolToInt(u.Deleted), toMs(u.UpdatedAt), urn,
		)
		if err != nil {
			return fmt.Errorf("sqlite: update user: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return fmt.Errorf("%w: %s", repo.ErrNotFound, urn)
		}
		return nil
	})
}

func (p *Provider) DeleteUser(ctx context.Context, urn string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.write(func(db *sql.DB) error {
		res, err := db.Exec(
			`UPDATE users SET deleted=1, updated_at=? WHERE urn=?`,
			toMs(time.Now().UTC()), urn,
		)
		if err != nil {
			return fmt.Errorf("sqlite: delete user: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return fmt.Errorf("%w: %s", repo.ErrNotFound, urn)
		}
		return nil
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// ServerRepository
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) RegisterServer(ctx context.Context, s *core.Server) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	urn := s.URN.String()
	return p.write(func(db *sql.DB) error {
		_, err := db.Exec(
			`INSERT INTO servers(urn, name, endpoint, cert_pem, cert_serial, revoked, registered_at, expires_at, last_seen_at)
			 VALUES(?, ?, ?, ?, ?, 0, ?, ?, 0)`,
			urn, s.Name, s.Endpoint, string(s.CertPEM), s.CertSerial,
			toMs(s.RegisteredAt), toMs(s.ExpiresAt),
		)
		if err != nil {
			if isSQLiteConstraintUnique(err) {
				return fmt.Errorf("%w: %s", repo.ErrAlreadyExists, urn)
			}
			return fmt.Errorf("sqlite: register server: %w", err)
		}
		return nil
	})
}

func (p *Provider) GetServer(ctx context.Context, urn string) (*core.Server, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return queryServer(ctx, p.db, urn)
}

func (p *Provider) ListServers(ctx context.Context, opts repo.ServerListOptions) (*repo.ServerListResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return queryServers(ctx, p.db, opts)
}

func (p *Provider) UpdateServer(ctx context.Context, s *core.Server) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	urn := s.URN.String()
	return p.write(func(db *sql.DB) error {
		res, err := db.Exec(
			`UPDATE servers SET name=?, endpoint=?, cert_pem=?, cert_serial=?, last_seen_at=? WHERE urn=?`,
			s.Name, s.Endpoint, string(s.CertPEM), s.CertSerial, toMs(s.LastSeenAt), urn,
		)
		if err != nil {
			return fmt.Errorf("sqlite: update server: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return fmt.Errorf("%w: %s", repo.ErrNotFound, urn)
		}
		return nil
	})
}

func (p *Provider) RevokeServer(ctx context.Context, urn string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.write(func(db *sql.DB) error {
		res, err := db.Exec(`UPDATE servers SET revoked=1 WHERE urn=?`, urn)
		if err != nil {
			return fmt.Errorf("sqlite: revoke server: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return fmt.Errorf("%w: %s", repo.ErrNotFound, urn)
		}
		return nil
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// PairingSecretStore
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) AddSecret(ctx context.Context, s *repo.PairingSecret) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.write(func(db *sql.DB) error {
		_, err := db.Exec(
			`INSERT INTO pairing_secrets(id, label, hash_bcrypt, expires_at) VALUES(?, ?, ?, ?)`,
			s.ID, s.LabelHint, s.HashBcrypt, toMs(s.ExpiresAt),
		)
		if err != nil {
			return fmt.Errorf("sqlite: add pairing secret: %w", err)
		}
		return nil
	})
}

func (p *Provider) ConsumeSecret(ctx context.Context, plaintext string) (*repo.PairingSecret, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// O(1) lookup: extract the ID from the token instead of scanning the table.
	id, ok := pairing.ExtractSecretID(plaintext)
	if !ok {
		return nil, fmt.Errorf("%w: malformed pairing secret token", repo.ErrNotFound)
	}

	now := time.Now().UTC()
	nowMs := toMs(now)

	// Single-row primary key read — no table scan.
	var label, hashBcrypt string
	var expiresMs int64
	err := p.db.QueryRowContext(ctx,
		`SELECT label, hash_bcrypt, expires_at FROM pairing_secrets
		 WHERE id = ? AND used_at IS NULL AND expires_at > ?`,
		id, nowMs,
	).Scan(&label, &hashBcrypt, &expiresMs)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("%w: pairing secret not found or expired", repo.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: query pairing secret: %w", err)
	}

	// One bcrypt call, not N.
	if err := bcrypt.CompareHashAndPassword([]byte(hashBcrypt), []byte(plaintext)); err != nil {
		return nil, fmt.Errorf("%w: pairing secret invalid", repo.ErrNotFound)
	}

	// Write the used_at timestamp via the serialised write channel.
	var rowsAffected int64
	if err := p.write(func(db *sql.DB) error {
		res, err := db.Exec(
			`UPDATE pairing_secrets SET used_at=? WHERE id=? AND used_at IS NULL`,
			nowMs, id,
		)
		if err != nil {
			return fmt.Errorf("sqlite: mark pairing secret used: %w", err)
		}
		rowsAffected, err = res.RowsAffected()
		return err
	}); err != nil {
		return nil, err
	}

	// If another concurrent request already consumed this secret, rowsAffected
	// will be 0 — treat it as not found.
	if rowsAffected == 0 {
		return nil, fmt.Errorf("%w: pairing secret already used", repo.ErrNotFound)
	}

	expiresAt := fromMs(expiresMs)
	return &repo.PairingSecret{
		ID:         id,
		LabelHint:  label,
		HashBcrypt: hashBcrypt,
		ExpiresAt:  expiresAt,
		UsedAt:     &now,
	}, nil
}

func (p *Provider) PruneExpired(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.write(func(db *sql.DB) error {
		_, err := db.Exec(
			`DELETE FROM pairing_secrets WHERE expires_at <= ?`,
			toMs(time.Now().UTC()),
		)
		if err != nil {
			return fmt.Errorf("sqlite: prune expired secrets: %w", err)
		}
		return nil
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Utility helpers
// ─────────────────────────────────────────────────────────────────────────────

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// isSQLiteConstraintUnique reports whether err is a SQLite UNIQUE constraint violation.
func isSQLiteConstraintUnique(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed") ||
		strings.Contains(err.Error(), "constraint failed")
}
