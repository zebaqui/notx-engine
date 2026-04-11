package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/zebaqui/notx-engine/core"
	"github.com/zebaqui/notx-engine/repo"
)

// ─────────────────────────────────────────────────────────────────────────────
// Burst scan helper
// ─────────────────────────────────────────────────────────────────────────────

const burstColumns = `id, note_urn, project_urn, folder_urn, author_urn,
	sequence, line_start, line_end, text, tokens, truncated, created_at`

func scanBurst(rows interface {
	Scan(dest ...any) error
}) (repo.BurstRecord, error) {
	var b repo.BurstRecord
	var truncatedInt int
	var createdAtMs int64
	if err := rows.Scan(
		&b.ID, &b.NoteURN, &b.ProjectURN, &b.FolderURN, &b.AuthorURN,
		&b.Sequence, &b.LineStart, &b.LineEnd, &b.Text, &b.Tokens,
		&truncatedInt, &createdAtMs,
	); err != nil {
		return repo.BurstRecord{}, err
	}
	b.Truncated = truncatedInt != 0
	b.CreatedAt = fromMs(createdAtMs)
	return b, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Candidate scan helper
// ─────────────────────────────────────────────────────────────────────────────

const candidateColumns = `id, burst_a_id, burst_b_id, note_urn_a, note_urn_b,
	project_urn, overlap_score, bm25_score, status, created_at,
	reviewed_at, reviewed_by, promoted_link`

func scanCandidate(rows interface {
	Scan(dest ...any) error
}) (repo.CandidateRecord, error) {
	var c repo.CandidateRecord
	var createdAtMs int64
	var reviewedAtMs *int64
	if err := rows.Scan(
		&c.ID, &c.BurstAID, &c.BurstBID, &c.NoteURN_A, &c.NoteURN_B,
		&c.ProjectURN, &c.OverlapScore, &c.BM25Score, &c.Status, &createdAtMs,
		&reviewedAtMs, &c.ReviewedBy, &c.PromotedLink,
	); err != nil {
		return repo.CandidateRecord{}, err
	}
	c.CreatedAt = fromMs(createdAtMs)
	if reviewedAtMs != nil {
		t := fromMs(*reviewedAtMs)
		c.ReviewedAt = &t
	}
	return c, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Pagination helpers for context methods
// ─────────────────────────────────────────────────────────────────────────────

func encodeOffsetToken(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

func decodeOffsetToken(token string) (int, error) {
	if token == "" {
		return 0, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return 0, fmt.Errorf("sqlite: invalid page token: %w", err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, fmt.Errorf("sqlite: invalid page token value: %w", err)
	}
	return n, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// BurstCountToday
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) BurstCountToday(ctx context.Context, noteURN, projectURN string) (noteCount, projectCount int, err error) {
	if err = ctx.Err(); err != nil {
		return
	}
	startOfDay := time.Now().UTC().Truncate(24 * time.Hour)
	startMs := toMs(startOfDay)

	row := p.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM context_bursts WHERE note_urn=? AND created_at>=?`,
		noteURN, startMs)
	if err = row.Scan(&noteCount); err != nil {
		err = fmt.Errorf("sqlite: burst count today (note): %w", err)
		return
	}

	row = p.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM context_bursts WHERE project_urn=? AND created_at>=?`,
		projectURN, startMs)
	if err = row.Scan(&projectCount); err != nil {
		err = fmt.Errorf("sqlite: burst count today (project): %w", err)
		return
	}
	return
}

// ─────────────────────────────────────────────────────────────────────────────
// MostRecentBurst
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) MostRecentBurst(ctx context.Context, noteURN string) (repo.BurstRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return repo.BurstRecord{}, false, err
	}
	row := p.db.QueryRowContext(ctx,
		`SELECT `+burstColumns+`
		 FROM context_bursts WHERE note_urn=? ORDER BY created_at DESC LIMIT 1`,
		noteURN)
	b, err := scanBurst(row)
	if err == sql.ErrNoRows {
		return repo.BurstRecord{}, false, nil
	}
	if err != nil {
		return repo.BurstRecord{}, false, fmt.Errorf("sqlite: most recent burst: %w", err)
	}
	return b, true, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// StoreBurst
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) StoreBurst(ctx context.Context, b repo.BurstRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	truncatedInt := 0
	if b.Truncated {
		truncatedInt = 1
	}
	createdMs := toMs(b.CreatedAt)

	return p.write(func(db *sql.DB) error {
		_, err := db.Exec(
			`INSERT INTO context_bursts(id, note_urn, project_urn, folder_urn, author_urn,
			  sequence, line_start, line_end, text, tokens, truncated, created_at)
			 VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
			b.ID, b.NoteURN, b.ProjectURN, b.FolderURN, b.AuthorURN,
			b.Sequence, b.LineStart, b.LineEnd, b.Text, b.Tokens,
			truncatedInt, createdMs,
		)
		if err != nil {
			return fmt.Errorf("sqlite: insert burst: %w", err)
		}
		_, err = db.Exec(
			`INSERT INTO context_bursts_fts(id, note_urn, project_urn, tokens)
			 VALUES(?,?,?,?)`,
			b.ID, b.NoteURN, b.ProjectURN, b.Tokens,
		)
		if err != nil {
			return fmt.Errorf("sqlite: insert burst fts: %w", err)
		}
		return nil
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// ListBursts
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) ListBursts(ctx context.Context, noteURN string, sinceSeq, pageSize int) ([]repo.BurstRecord, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	limit := resolvePageSize(pageSize)

	rows, err := p.db.QueryContext(ctx,
		`SELECT `+burstColumns+`
		 FROM context_bursts
		 WHERE note_urn=? AND sequence>=?
		 ORDER BY sequence ASC
		 LIMIT ?`,
		noteURN, sinceSeq, limit)
	if err != nil {
		return nil, "", fmt.Errorf("sqlite: list bursts: %w", err)
	}
	defer rows.Close()

	var bursts []repo.BurstRecord
	for rows.Next() {
		b, err := scanBurst(rows)
		if err != nil {
			return nil, "", fmt.Errorf("sqlite: scan burst: %w", err)
		}
		bursts = append(bursts, b)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("sqlite: iterate bursts: %w", err)
	}

	var nextToken string
	if len(bursts) == limit {
		lastSeq := bursts[len(bursts)-1].Sequence
		nextToken = fmt.Sprintf("%d", lastSeq+1)
	}
	return bursts, nextToken, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// GetBurst
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) GetBurst(ctx context.Context, id string) (repo.BurstRecord, error) {
	if err := ctx.Err(); err != nil {
		return repo.BurstRecord{}, err
	}
	row := p.db.QueryRowContext(ctx,
		`SELECT `+burstColumns+` FROM context_bursts WHERE id=?`, id)
	b, err := scanBurst(row)
	if err == sql.ErrNoRows {
		return repo.BurstRecord{}, fmt.Errorf("%w: burst %s", repo.ErrNotFound, id)
	}
	if err != nil {
		return repo.BurstRecord{}, fmt.Errorf("sqlite: get burst: %w", err)
	}
	return b, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// SweepBursts
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) SweepBursts(ctx context.Context, olderThan time.Time) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	cutoffMs := toMs(olderThan)
	var deleted int
	err := p.write(func(db *sql.DB) error {
		res, err := db.Exec(
			`DELETE FROM context_bursts WHERE created_at<?`, cutoffMs)
		if err != nil {
			return fmt.Errorf("sqlite: sweep bursts: %w", err)
		}
		n, _ := res.RowsAffected()
		deleted = int(n)
		return nil
	})
	return deleted, err
}

// ─────────────────────────────────────────────────────────────────────────────
// IndexNoteIntoProject
// ─────────────────────────────────────────────────────────────────────────────

// IndexNoteIntoProject backfills existing bursts for a note with the given
// projectURN and runs candidate detection against the project's burst pool.
// It runs through p.write() to avoid concurrent write issues, then enqueues
// new candidate IDs to the scorer channel.
func (p *Provider) IndexNoteIntoProject(ctx context.Context, noteURN, projectURN string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	var newCandidateIDs []string
	cfg := p.burstCfg

	err := p.write(func(db *sql.DB) error {
		// ── 1. Stamp all existing bursts for this note with the project URN ──
		_, err := db.Exec(
			`UPDATE context_bursts SET project_urn=? WHERE note_urn=? AND (project_urn='' OR project_urn IS NULL)`,
			projectURN, noteURN,
		)
		if err != nil {
			return fmt.Errorf("sqlite: index note into project — update bursts: %w", err)
		}

		// Also update FTS5 shadow table (best-effort).
		_, _ = db.Exec(
			`UPDATE context_bursts_fts SET project_urn=? WHERE note_urn=? AND (project_urn='' OR project_urn IS NULL)`,
			projectURN, noteURN,
		)

		// ── 2. Load all bursts for this note (now with project_urn set) ──────
		rows, err := db.Query(
			`SELECT id, tokens FROM context_bursts WHERE note_urn=? ORDER BY created_at ASC`,
			noteURN,
		)
		if err != nil {
			return fmt.Errorf("sqlite: index note into project — load note bursts: %w", err)
		}
		type noteBurst struct {
			id     string
			tokens []string
		}
		var noteBursts []noteBurst
		for rows.Next() {
			var id, tokStr string
			if err := rows.Scan(&id, &tokStr); err != nil {
				rows.Close()
				return fmt.Errorf("sqlite: index note into project — scan burst: %w", err)
			}
			noteBursts = append(noteBursts, noteBurst{id: id, tokens: strings.Fields(tokStr)})
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("sqlite: index note into project — rows err: %w", err)
		}

		if len(noteBursts) == 0 {
			return nil
		}

		// ── 3. Load recent bursts from OTHER notes in the project ─────────────
		cutoffMs := toMs(time.Now().UTC().Add(-time.Duration(cfg.CandidateLookbackDays) * 24 * time.Hour))
		peerRows, err := db.Query(
			`SELECT id, note_urn, project_urn, tokens
			 FROM context_bursts
			 WHERE project_urn=? AND note_urn!=? AND created_at>=?
			   AND note_urn NOT IN (SELECT urn FROM notes WHERE deleted=1)
			 ORDER BY created_at DESC LIMIT ?`,
			projectURN, noteURN, cutoffMs, cfg.CandidateLookbackN,
		)
		if err != nil {
			return fmt.Errorf("sqlite: index note into project — load peer bursts: %w", err)
		}
		type peerBurst struct {
			id, noteURN, projectURN string
			tokens                  []string
		}
		var peerBursts []peerBurst
		for peerRows.Next() {
			var id, nURN, pURN, tokStr string
			if err := peerRows.Scan(&id, &nURN, &pURN, &tokStr); err != nil {
				peerRows.Close()
				return fmt.Errorf("sqlite: index note into project — scan peer burst: %w", err)
			}
			peerBursts = append(peerBursts, peerBurst{id: id, noteURN: nURN, projectURN: pURN, tokens: strings.Fields(tokStr)})
		}
		peerRows.Close()
		if err := peerRows.Err(); err != nil {
			return fmt.Errorf("sqlite: index note into project — peer rows err: %w", err)
		}

		if len(peerBursts) == 0 {
			return nil // no peers yet, nothing to pair with
		}

		// ── 4. Load already-reviewed pair keys to avoid recreating dismissed or
		//       promoted candidates when a note is re-indexed into a project. ────
		existingPairKeys := make(map[string]struct{})
		existingRows, err := db.Query(
			`SELECT pair_key FROM candidate_relations
			 WHERE (note_urn_a=? OR note_urn_b=?) AND status IN ('promoted','dismissed')`,
			noteURN, noteURN,
		)
		if err == nil {
			for existingRows.Next() {
				var pk string
				if existingRows.Scan(&pk) == nil {
					existingPairKeys[pk] = struct{}{}
				}
			}
			_ = existingRows.Close()
		}

		// ── 5. Run Jaccard detection for each note burst vs each peer burst ───
		now := toMs(time.Now().UTC())
		stmt, err := db.Prepare(
			`INSERT OR IGNORE INTO candidate_relations
			   (id, burst_a_id, burst_b_id, note_urn_a, note_urn_b,
			    project_urn, overlap_score, bm25_score, status, created_at, pair_key)
			 VALUES(?,?,?,?,?,?,?,0,'pending',?,?)`,
		)
		if err != nil {
			return fmt.Errorf("sqlite: index note into project — prepare candidate: %w", err)
		}
		defer stmt.Close()

		for _, nb := range noteBursts {
			for _, pb := range peerBursts {
				score := core.JaccardScore(nb.tokens, pb.tokens)
				if score < cfg.OverlapThreshold {
					continue
				}
				candID, err := uuid.NewV7()
				if err != nil {
					continue
				}
				idStr := candID.String()
				nAID, nNoteA, nBID, nNoteB, pairKey := normalizeBurstPair(
					nb.id, noteURN,
					pb.id, pb.noteURN,
				)
				// Skip pairs that have already been reviewed (promoted or dismissed)
				// so we never surface the same candidate twice after a re-index.
				if _, reviewed := existingPairKeys[pairKey]; reviewed {
					continue
				}
				_, err = stmt.Exec(
					idStr,
					nAID, nBID,
					nNoteA, nNoteB,
					projectURN,
					score,
					now,
					pairKey,
				)
				if err != nil {
					continue // INSERT OR IGNORE — duplicate means it already exists
				}
				newCandidateIDs = append(newCandidateIDs, idStr)
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}

	// ── 5. Enqueue new candidate IDs to BM25 scorer ──────────────────────────
	for _, id := range newCandidateIDs {
		select {
		case p.scorerCh <- id:
		default:
			// channel full — drop silently
		}
	}

	return len(newCandidateIDs), nil
}

// ─────────────────────────────────────────────────────────────────────────────
// SuggestProjectForNote
// ─────────────────────────────────────────────────────────────────────────────

// SuggestProjectForNote loads the note's project-less bursts, then scores them
// against recent bursts from notes that do have a project assigned, delegating
// all scoring logic to core.SuggestProjectForNote.
func (p *Provider) SuggestProjectForNote(ctx context.Context, noteURN string) ([]core.ProjectSuggestion, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// ── 1. Load this note's bursts that have no project yet ─────────────────
	noteRows, err := p.db.QueryContext(ctx,
		`SELECT tokens FROM context_bursts WHERE note_urn=? AND (project_urn IS NULL OR project_urn='')`,
		noteURN,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite: suggest project — load note bursts: %w", err)
	}
	var noteTokenSets [][]string
	for noteRows.Next() {
		var tokStr string
		if err := noteRows.Scan(&tokStr); err != nil {
			noteRows.Close()
			return nil, fmt.Errorf("sqlite: suggest project — scan note burst: %w", err)
		}
		if toks := strings.Fields(tokStr); len(toks) >= 3 {
			noteTokenSets = append(noteTokenSets, toks)
		}
	}
	noteRows.Close()
	if err := noteRows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: suggest project — note rows err: %w", err)
	}

	if len(noteTokenSets) == 0 {
		return nil, nil
	}

	// ── 2. Load recent bursts from notes that DO have a project assigned ─────
	cfg := p.burstCfg
	cutoffMs := toMs(time.Now().UTC().Add(-time.Duration(cfg.CandidateLookbackDays) * 24 * time.Hour))
	projRows, err := p.db.QueryContext(ctx,
		`SELECT project_urn, tokens
		 FROM context_bursts
		 WHERE project_urn IS NOT NULL AND project_urn!='' AND note_urn!=? AND created_at>=?
		 ORDER BY created_at DESC LIMIT ?`,
		noteURN, cutoffMs, cfg.CandidateLookbackN,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite: suggest project — load project bursts: %w", err)
	}
	var hints []core.ProjectBurstHint
	for projRows.Next() {
		var projURN, tokStr string
		if err := projRows.Scan(&projURN, &tokStr); err != nil {
			projRows.Close()
			return nil, fmt.Errorf("sqlite: suggest project — scan project burst: %w", err)
		}
		if toks := strings.Fields(tokStr); len(toks) >= 3 {
			hints = append(hints, core.ProjectBurstHint{ProjectURN: projURN, Tokens: toks})
		}
	}
	projRows.Close()
	if err := projRows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: suggest project — project rows err: %w", err)
	}

	// ── 3. Delegate scoring to pure core function ────────────────────────────
	return core.SuggestProjectForNote(noteTokenSets, hints, cfg.OverlapThreshold), nil
}

// ─────────────────────────────────────────────────────────────────────────────
// RecentBurstsInProject
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) RecentBurstsInProject(ctx context.Context, projectURN string, days, limit int) ([]repo.BurstRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cutoff := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour)
	cutoffMs := toMs(cutoff)
	if limit <= 0 {
		limit = defaultPageSize
	}

	rows, err := p.db.QueryContext(ctx,
		`SELECT `+burstColumns+`
		 FROM context_bursts
		 WHERE project_urn=? AND created_at>=? AND note_urn!=''
		 ORDER BY created_at DESC
		 LIMIT ?`,
		projectURN, cutoffMs, limit)
	if err != nil {
		return nil, fmt.Errorf("sqlite: recent bursts in project: %w", err)
	}
	defer rows.Close()

	var bursts []repo.BurstRecord
	for rows.Next() {
		b, err := scanBurst(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan burst: %w", err)
		}
		bursts = append(bursts, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterate recent bursts: %w", err)
	}
	return bursts, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// StoreCandidates
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) StoreCandidates(ctx context.Context, candidates []repo.CandidateRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(candidates) == 0 {
		return nil
	}
	return p.write(func(db *sql.DB) error {
		stmt, err := db.Prepare(
			`INSERT OR IGNORE INTO candidate_relations
			  (id, burst_a_id, burst_b_id, note_urn_a, note_urn_b,
			   project_urn, overlap_score, bm25_score, status, created_at, pair_key)
			 VALUES(?,?,?,?,?,?,?,0,'pending',?,?)`)
		if err != nil {
			return fmt.Errorf("sqlite: prepare store candidates: %w", err)
		}
		defer stmt.Close()

		for _, c := range candidates {
			nAID, nNoteA, nBID, nNoteB, pairKey := normalizeBurstPair(
				c.BurstAID, c.NoteURN_A,
				c.BurstBID, c.NoteURN_B,
			)
			_, err := stmt.Exec(
				c.ID, nAID, nBID, nNoteA, nNoteB,
				c.ProjectURN, c.OverlapScore, toMs(c.CreatedAt),
				pairKey,
			)
			if err != nil {
				return fmt.Errorf("sqlite: insert candidate %s: %w", c.ID, err)
			}
		}
		return nil
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// UpdateCandidateBM25
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) UpdateCandidateBM25(ctx context.Context, id string, score float64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.write(func(db *sql.DB) error {
		res, err := db.Exec(
			`UPDATE candidate_relations SET bm25_score=? WHERE id=?`, score, id)
		if err != nil {
			return fmt.Errorf("sqlite: update candidate bm25: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return fmt.Errorf("%w: candidate %s", repo.ErrNotFound, id)
		}
		return nil
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// ListCandidates
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) ListCandidates(ctx context.Context, opts repo.CandidateListOptions) ([]repo.CandidateRecord, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}

	offset, err := decodeOffsetToken(opts.PageToken)
	if err != nil {
		return nil, "", err
	}

	limit := resolvePageSize(opts.PageSize)

	var (
		where []string
		args  []any
	)

	if opts.ProjectURN != "" {
		where = append(where, "project_urn=?")
		args = append(args, opts.ProjectURN)
	}
	if opts.NoteURN != "" {
		where = append(where, "(note_urn_a=? OR note_urn_b=?)")
		args = append(args, opts.NoteURN, opts.NoteURN)
	}
	if opts.Status != "" {
		where = append(where, "status=?")
		args = append(args, opts.Status)
	}
	if opts.MinScore > 0 {
		where = append(where, "overlap_score>=?")
		args = append(args, opts.MinScore)
	}

	clause := "1=1"
	if len(where) > 0 {
		clause = strings.Join(where, " AND ")
	}

	query := fmt.Sprintf(
		`SELECT `+candidateColumns+`
		 FROM candidate_relations
		 WHERE %s
		 ORDER BY bm25_score DESC, overlap_score DESC
		 LIMIT ? OFFSET ?`,
		clause,
	)
	args = append(args, limit, offset)

	rows, err := p.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("sqlite: list candidates: %w", err)
	}
	defer rows.Close()

	var records []repo.CandidateRecord
	for rows.Next() {
		c, err := scanCandidate(rows)
		if err != nil {
			return nil, "", fmt.Errorf("sqlite: scan candidate: %w", err)
		}
		records = append(records, c)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("sqlite: iterate candidates: %w", err)
	}

	var nextToken string
	if len(records) == limit {
		nextToken = encodeOffsetToken(offset + limit)
	}
	return records, nextToken, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// GetCandidate
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) GetCandidate(ctx context.Context, id string) (repo.CandidateRecord, error) {
	if err := ctx.Err(); err != nil {
		return repo.CandidateRecord{}, err
	}
	row := p.db.QueryRowContext(ctx,
		`SELECT `+candidateColumns+` FROM candidate_relations WHERE id=?`, id)
	c, err := scanCandidate(row)
	if err == sql.ErrNoRows {
		return repo.CandidateRecord{}, fmt.Errorf("%w: candidate %s", repo.ErrNotFound, id)
	}
	if err != nil {
		return repo.CandidateRecord{}, fmt.Errorf("sqlite: get candidate: %w", err)
	}
	return c, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// PromoteCandidate
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) PromoteCandidate(ctx context.Context, id string, opts repo.PromoteOptions) (repo.PromoteResult, error) {
	if err := ctx.Err(); err != nil {
		return repo.PromoteResult{}, err
	}

	var result repo.PromoteResult

	err := p.write(func(db *sql.DB) error {
		now := toMs(time.Now().UTC())
		nowStr := time.Now().UTC().Format(time.RFC3339)

		// ── 1. Load the candidate to get note URNs and burst text ─────────────
		var noteURN_A, noteURN_B, burstAID, burstBID string
		err := db.QueryRow(
			`SELECT note_urn_a, note_urn_b, burst_a_id, burst_b_id
			 FROM candidate_relations WHERE id=?`, id,
		).Scan(&noteURN_A, &noteURN_B, &burstAID, &burstBID)
		if err == sql.ErrNoRows {
			return fmt.Errorf("%w: candidate %s", repo.ErrNotFound, id)
		}
		if err != nil {
			return fmt.Errorf("sqlite: promote candidate load: %w", err)
		}

		// ── 2. Load burst text for slug generation ────────────────────────────
		var textA, textB string
		_ = db.QueryRow(`SELECT text FROM context_bursts WHERE id=?`, burstAID).Scan(&textA)
		_ = db.QueryRow(`SELECT text FROM context_bursts WHERE id=?`, burstBID).Scan(&textB)

		// ── 3. Generate anchor IDs from burst text (or label override) ────────
		//
		// Use the label as the slug base when provided, otherwise derive from
		// the burst text using SlugFromText.  Each anchor ID must be unique
		// within its note; we load existing IDs for collision avoidance.
		existingA := loadAnchorIDs(db, noteURN_A)
		existingB := loadAnchorIDs(db, noteURN_B)

		var anchorAID, anchorBID string
		if opts.Label != "" {
			anchorAID = uniqueSlug(opts.Label, existingA)
			existingA[anchorAID] = struct{}{}
			anchorBID = uniqueSlug(opts.Label, existingB)
		} else {
			anchorAID = core.SlugFromText(combineTexts(textA, textB), existingA)
			existingA[anchorAID] = struct{}{}
			anchorBID = core.SlugFromText(combineTexts(textB, textA), existingB)
		}

		result.AnchorAID = anchorAID
		result.AnchorBID = anchorBID

		// ── 4. Write anchors ──────────────────────────────────────────────────
		previewA := previewText(textA)
		previewB := previewText(textB)

		if _, err := db.Exec(
			`INSERT INTO anchors(note_urn, anchor_id, line, char_start, char_end, preview, status, updated_at)
			 VALUES(?,?,0,0,0,?,'ok',?)
			 ON CONFLICT(note_urn, anchor_id) DO UPDATE SET
			   preview=excluded.preview, status='ok', updated_at=excluded.updated_at`,
			noteURN_A, anchorAID, previewA, nowStr,
		); err != nil {
			return fmt.Errorf("sqlite: promote candidate upsert anchor A: %w", err)
		}

		if _, err := db.Exec(
			`INSERT INTO anchors(note_urn, anchor_id, line, char_start, char_end, preview, status, updated_at)
			 VALUES(?,?,0,0,0,?,'ok',?)
			 ON CONFLICT(note_urn, anchor_id) DO UPDATE SET
			   preview=excluded.preview, status='ok', updated_at=excluded.updated_at`,
			noteURN_B, anchorBID, previewB, nowStr,
		); err != nil {
			return fmt.Errorf("sqlite: promote candidate upsert anchor B: %w", err)
		}

		// ── 5. Build link tokens ──────────────────────────────────────────────
		//   notx:lnk:id:<target_note_urn>:<target_anchor_id>
		linkAToB := "notx:lnk:id:" + noteURN_B + ":" + anchorBID
		linkBToA := "notx:lnk:id:" + noteURN_A + ":" + anchorAID

		direction := opts.Direction
		if direction == "" {
			direction = "both"
		}

		switch direction {
		case "a_to_b":
			linkBToA = ""
		case "b_to_a":
			linkAToB = ""
		}

		result.LinkAToB = linkAToB
		result.LinkBToA = linkBToA

		// ── 6. Write backlinks ────────────────────────────────────────────────
		label := opts.Label
		if label == "" {
			label = anchorAID
		}

		if linkAToB != "" {
			// A links into B at anchor B
			if _, err := db.Exec(
				`INSERT INTO backlinks(source_urn, target_urn, target_anchor, label, created_at)
				 VALUES(?,?,?,?,?)
				 ON CONFLICT(source_urn, target_urn, target_anchor) DO UPDATE SET
				   label=excluded.label, created_at=excluded.created_at`,
				noteURN_A, noteURN_B, anchorBID, label, nowStr,
			); err != nil {
				return fmt.Errorf("sqlite: promote candidate upsert backlink A→B: %w", err)
			}
		}

		if linkBToA != "" {
			// B links into A at anchor A
			if _, err := db.Exec(
				`INSERT INTO backlinks(source_urn, target_urn, target_anchor, label, created_at)
				 VALUES(?,?,?,?,?)
				 ON CONFLICT(source_urn, target_urn, target_anchor) DO UPDATE SET
				   label=excluded.label, created_at=excluded.created_at`,
				noteURN_B, noteURN_A, anchorAID, label, nowStr,
			); err != nil {
				return fmt.Errorf("sqlite: promote candidate upsert backlink B→A: %w", err)
			}
		}

		// ── 7. Mark candidate promoted ────────────────────────────────────────
		// Store the A→B link token as the canonical promoted_link reference.
		promotedLink := linkAToB
		if promotedLink == "" {
			promotedLink = linkBToA
		}

		res, err := db.Exec(
			`UPDATE candidate_relations
			 SET status='promoted', reviewed_at=?, reviewed_by=?, promoted_link=?
			 WHERE id=?`,
			now, opts.ReviewerURN, promotedLink, id,
		)
		if err != nil {
			return fmt.Errorf("sqlite: promote candidate update: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return fmt.Errorf("%w: candidate %s", repo.ErrNotFound, id)
		}
		return nil
	})
	if err != nil {
		return repo.PromoteResult{}, err
	}
	return result, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// PromoteCandidate helpers
// ─────────────────────────────────────────────────────────────────────────────

// loadAnchorIDs returns the set of existing anchor IDs for a note, used for
// collision-avoidance when generating new anchor slugs.
func loadAnchorIDs(db *sql.DB, noteURN string) map[string]struct{} {
	ids := make(map[string]struct{})
	rows, err := db.Query(`SELECT anchor_id FROM anchors WHERE note_urn=?`, noteURN)
	if err != nil {
		return ids
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids[id] = struct{}{}
		}
	}
	return ids
}

// uniqueSlug ensures slug is not already in existing, appending -2, -3, …
// as needed.  Mirrors the collision logic in core.SlugFromText.
func uniqueSlug(base string, existing map[string]struct{}) string {
	candidate := base
	if _, ok := existing[candidate]; !ok {
		return candidate
	}
	for n := 2; ; n++ {
		candidate = fmt.Sprintf("%s-%d", base, n)
		if _, ok := existing[candidate]; !ok {
			return candidate
		}
	}
}

// combineTexts joins two burst texts with a newline so SlugFromText sees both
// token sets — giving better signal when one burst is very short.
func combineTexts(primary, secondary string) string {
	if secondary == "" {
		return primary
	}
	// Only use the first line of secondary to avoid biasing the slug too far.
	secondaryFirst := secondary
	if idx := strings.IndexByte(secondary, '\n'); idx >= 0 {
		secondaryFirst = secondary[:idx]
	}
	return primary + "\n" + secondaryFirst
}

// previewText returns the first 80 characters of text, collapsed to a single
// line, for use as the anchor preview field.
func previewText(text string) string {
	// Collapse to single line.
	line := strings.ReplaceAll(text, "\n", " ")
	line = strings.Join(strings.Fields(line), " ")
	if len(line) > 80 {
		// Use byte length for simplicity; anchors are ASCII-dominant.
		return line[:77] + "…"
	}
	return line
}

// anchorHashFallback returns a short deterministic suffix derived from text,
// used when SlugFromText produces an empty result.
// Kept here as a safety net — not called in the hot path.
func anchorHashFallback(text string) string {
	sum := sha256.Sum256([]byte(text))
	return fmt.Sprintf("%x", sum[:3])
}

// ─────────────────────────────────────────────────────────────────────────────
// DismissCandidate
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) DismissCandidate(ctx context.Context, id, reviewerURN string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.write(func(db *sql.DB) error {
		now := toMs(time.Now().UTC())
		res, err := db.Exec(
			`UPDATE candidate_relations
			 SET status='dismissed', reviewed_at=?, reviewed_by=?
			 WHERE id=?`,
			now, reviewerURN, id,
		)
		if err != nil {
			return fmt.Errorf("sqlite: dismiss candidate: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return fmt.Errorf("%w: candidate %s", repo.ErrNotFound, id)
		}
		return nil
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// GetProjectContextConfig
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) GetProjectContextConfig(ctx context.Context, projectURN string) (repo.ProjectContextConfig, error) {
	if err := ctx.Err(); err != nil {
		return repo.ProjectContextConfig{}, err
	}
	row := p.db.QueryRowContext(ctx,
		`SELECT project_urn, burst_max_per_note_per_day, burst_max_per_project_per_day, updated_at
		 FROM project_context_config WHERE project_urn=?`,
		projectURN)

	var cfg repo.ProjectContextConfig
	var updatedAtMs int64
	var maxNote, maxProject *int
	if err := row.Scan(&cfg.ProjectURN, &maxNote, &maxProject, &updatedAtMs); err != nil {
		if err == sql.ErrNoRows {
			return repo.ProjectContextConfig{}, fmt.Errorf("%w: project context config %s", repo.ErrNotFound, projectURN)
		}
		return repo.ProjectContextConfig{}, fmt.Errorf("sqlite: get project context config: %w", err)
	}
	cfg.BurstMaxPerNotePerDay = maxNote
	cfg.BurstMaxPerProjectPerDay = maxProject
	cfg.UpdatedAt = fromMs(updatedAtMs)
	return cfg, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// UpsertProjectContextConfig
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) UpsertProjectContextConfig(ctx context.Context, cfg repo.ProjectContextConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.write(func(db *sql.DB) error {
		_, err := db.Exec(
			`INSERT INTO project_context_config
			  (project_urn, burst_max_per_note_per_day, burst_max_per_project_per_day, updated_at)
			 VALUES(?,?,?,?)
			 ON CONFLICT(project_urn) DO UPDATE SET
			   burst_max_per_note_per_day=excluded.burst_max_per_note_per_day,
			   burst_max_per_project_per_day=excluded.burst_max_per_project_per_day,
			   updated_at=excluded.updated_at`,
			cfg.ProjectURN,
			cfg.BurstMaxPerNotePerDay,
			cfg.BurstMaxPerProjectPerDay,
			toMs(cfg.UpdatedAt),
		)
		if err != nil {
			return fmt.Errorf("sqlite: upsert project context config: %w", err)
		}
		return nil
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// GetContextStats
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) GetContextStats(ctx context.Context, projectURN string) (repo.ContextStats, error) {
	if err := ctx.Err(); err != nil {
		return repo.ContextStats{}, err
	}

	var stats repo.ContextStats
	scoped := projectURN != ""

	// Helper: run a COUNT(*) query, with optional AND project_urn=? appended.
	count := func(base, suffix string, baseArgs ...any) (int, error) {
		q := base
		args := baseArgs
		if scoped {
			q += suffix
			args = append(args, projectURN)
		}
		var n int
		if err := p.db.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
			return 0, err
		}
		return n, nil
	}

	startOfDay := time.Now().UTC().Truncate(24 * time.Hour)
	startMs := toMs(startOfDay)

	var err error

	stats.BurstsTotal, err = count(
		`SELECT COUNT(*) FROM context_bursts`,
		` WHERE project_urn=?`,
	)
	if err != nil {
		return repo.ContextStats{}, fmt.Errorf("sqlite: stats bursts total: %w", err)
	}

	stats.BurstsToday, err = count(
		`SELECT COUNT(*) FROM context_bursts WHERE created_at>=?`,
		` AND project_urn=?`,
		startMs,
	)
	if err != nil {
		return repo.ContextStats{}, fmt.Errorf("sqlite: stats bursts today: %w", err)
	}

	stats.CandidatesPending, err = count(
		`SELECT COUNT(*) FROM candidate_relations WHERE status='pending'`,
		` AND project_urn=?`,
	)
	if err != nil {
		return repo.ContextStats{}, fmt.Errorf("sqlite: stats candidates pending: %w", err)
	}

	stats.CandidatesPendingUnenriched, err = count(
		`SELECT COUNT(*) FROM candidate_relations WHERE status='pending' AND bm25_score=0`,
		` AND project_urn=?`,
	)
	if err != nil {
		return repo.ContextStats{}, fmt.Errorf("sqlite: stats candidates pending unenriched: %w", err)
	}

	stats.CandidatesPromoted, err = count(
		`SELECT COUNT(*) FROM candidate_relations WHERE status='promoted'`,
		` AND project_urn=?`,
	)
	if err != nil {
		return repo.ContextStats{}, fmt.Errorf("sqlite: stats candidates promoted: %w", err)
	}

	stats.CandidatesDismissed, err = count(
		`SELECT COUNT(*) FROM candidate_relations WHERE status='dismissed'`,
		` AND project_urn=?`,
	)
	if err != nil {
		return repo.ContextStats{}, fmt.Errorf("sqlite: stats candidates dismissed: %w", err)
	}

	// Oldest pending candidate age in days.
	minQ := `SELECT MIN(created_at) FROM candidate_relations WHERE status='pending'`
	minArgs := []any{}
	if scoped {
		minQ += ` AND project_urn=?`
		minArgs = append(minArgs, projectURN)
	}
	var minCreatedMs *int64
	if err := p.db.QueryRowContext(ctx, minQ, minArgs...).Scan(&minCreatedMs); err != nil {
		return repo.ContextStats{}, fmt.Errorf("sqlite: stats oldest pending: %w", err)
	}
	if minCreatedMs != nil {
		oldest := fromMs(*minCreatedMs)
		stats.OldestPendingAgeDays = time.Since(oldest).Hours() / 24
	}

	// Inference counts (not project-scoped — inference records are note-level).
	var infPending, infAccepted int
	_ = p.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM note_context_inferences WHERE status='pending'`,
	).Scan(&infPending)
	_ = p.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM note_context_inferences WHERE status='accepted'`,
	).Scan(&infAccepted)
	stats.InferencesPending = infPending
	stats.InferencesAccepted = infAccepted

	return stats, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// SearchBursts
// ─────────────────────────────────────────────────────────────────────────────

// SearchBursts searches burst tokens and text with two complementary strategies
// so that partial input, different cases, and full words all return results:
//
//  1. FTS5 prefix search on context_bursts_fts.tokens — "Cha*" matches
//     "challenges", "change", etc.  FTS5's unicode61 tokenizer folds to
//     lowercase before indexing, so the match is always case-insensitive.
//     The FTS5 rank value is returned as the BM25 score for ordering.
//
//  2. LIKE fallback on context_bursts.text and tokens — catches raw text
//     passages where the term appears but may not have been tokenised the same
//     way (e.g. hyphenated words, punctuation boundaries).  LOWER() on both
//     sides makes it case-insensitive.  Rows already found by the FTS5 leg are
//     skipped so there are no duplicates; these rows get a score of 0.
//
// FTS5 hits always sort above LIKE-only hits because the FTS5 score (-rank,
// which is positive) is non-zero while LIKE-only rows receive 0.0.
func (p *Provider) SearchBursts(ctx context.Context, q string, pageSize int) ([]repo.BurstSearchResult, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return []repo.BurstSearchResult{}, nil
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	qLower := strings.ToLower(q)
	words := strings.Fields(qLower)

	// ── Leg 1: FTS5 exact phrase-prefix match ────────────────────────────────
	// "phase 3*" — FTS5 treats this as a phrase where the last word is
	// prefix-matched. This gives the highest relevance for exact phrase hits.
	phraseQuery := `"` + qLower + `*"`

	// ── Leg 2: FTS5 AND of individual token prefixes ─────────────────────────
	// "phase* AND 3*" — all words must appear anywhere in the burst (not
	// necessarily adjacent). Only built for multi-word queries; for single
	// words it's identical to Leg 1 so we skip it.
	var andQuery string
	if len(words) > 1 {
		prefixed := make([]string, len(words))
		for i, w := range words {
			prefixed[i] = w + "*"
		}
		andQuery = strings.Join(prefixed, " AND ")
	}

	var results []repo.BurstSearchResult
	seenIDs := make(map[string]struct{})

	// ─────────────────────────────────────────────────────────────────────────
	// Helper: run one FTS5 leg and append new hits to results.
	// scoreMultiplier scales -rank so we can rank legs relative to each other.
	// ─────────────────────────────────────────────────────────────────────────
	runFTSLeg := func(ftsQ string, scoreMultiplier float64) error {
		const ftsSQL = `
			SELECT b.id, b.note_urn, COALESCE(b.project_urn,''), b.line_start, b.line_end,
			       COALESCE(b.text,''), COALESCE(b.tokens,''),
			       CAST(-fts.rank * ? AS REAL) AS bm25_score,
			       b.created_at
			FROM context_bursts_fts fts
			JOIN context_bursts b ON b.id = fts.id
			WHERE context_bursts_fts MATCH ?
			ORDER BY fts.rank
			LIMIT ?`
		rows, err := p.db.QueryContext(ctx, ftsSQL, scoreMultiplier, ftsQ, pageSize)
		if err != nil {
			// FTS5 may not be available on older DBs — ignore.
			return nil
		}
		defer rows.Close()
		for rows.Next() {
			var r repo.BurstSearchResult
			var createdAtMs int64
			if err := rows.Scan(&r.ID, &r.NoteURN, &r.ProjectURN, &r.LineStart, &r.LineEnd,
				&r.Text, &r.Tokens, &r.BM25Score, &createdAtMs); err != nil {
				return fmt.Errorf("sqlite: search bursts fts: scan: %w", err)
			}
			if _, already := seenIDs[r.ID]; already {
				continue
			}
			r.CreatedAt = fromMs(createdAtMs).UTC()
			r.MatchLocations = repo.FindMatchLocations(r.Text, q)
			results = append(results, r)
			seenIDs[r.ID] = struct{}{}
		}
		return rows.Err()
	}

	// Run Leg 1: exact phrase prefix (full score weight 1.0).
	if err := runFTSLeg(phraseQuery, 1.0); err != nil {
		return nil, fmt.Errorf("sqlite: search bursts phrase: %w", err)
	}

	// Run Leg 2: AND of individual prefixes (score weight 0.75 — below phrase).
	if andQuery != "" && len(results) < pageSize {
		if err := runFTSLeg(andQuery, 0.75); err != nil {
			return nil, fmt.Errorf("sqlite: search bursts and: %w", err)
		}
	}

	// ── Leg 3: LIKE fallback on raw text ─────────────────────────────────────
	if len(results) < pageSize {
		remaining := pageSize - len(results)

		const likeSQLQuery = `
			SELECT b.id, b.note_urn, COALESCE(b.project_urn,''), b.line_start, b.line_end,
			       COALESCE(b.text,''), COALESCE(b.tokens,''),
			       0.0 AS bm25_score,
			       b.created_at
			FROM context_bursts b
			WHERE (LOWER(b.tokens) LIKE '%' || LOWER(?) || '%'
			    OR LOWER(b.text)   LIKE '%' || LOWER(?) || '%')
			ORDER BY b.created_at DESC
			LIMIT ?`

		likeRows, err := p.db.QueryContext(ctx, likeSQLQuery, q, q, remaining+len(seenIDs))
		if err != nil {
			return nil, fmt.Errorf("sqlite: search bursts like: %w", err)
		}
		defer likeRows.Close()

		for likeRows.Next() {
			var r repo.BurstSearchResult
			var createdAtMs int64
			if err := likeRows.Scan(&r.ID, &r.NoteURN, &r.ProjectURN, &r.LineStart, &r.LineEnd,
				&r.Text, &r.Tokens, &r.BM25Score, &createdAtMs); err != nil {
				return nil, fmt.Errorf("sqlite: search bursts like: scan: %w", err)
			}
			if _, already := seenIDs[r.ID]; already {
				continue
			}
			r.CreatedAt = fromMs(createdAtMs).UTC()
			r.MatchLocations = repo.FindMatchLocations(r.Text, q)
			results = append(results, r)
			seenIDs[r.ID] = struct{}{}
			if len(results) >= pageSize {
				break
			}
		}
		if err := likeRows.Err(); err != nil {
			return nil, fmt.Errorf("sqlite: search bursts like: iterate: %w", err)
		}
	}

	if results == nil {
		results = []repo.BurstSearchResult{}
	}
	return results, nil
}
