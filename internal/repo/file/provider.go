package file

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/zebaqui/notx-engine/core"
	"github.com/zebaqui/notx-engine/internal/repo"
	"github.com/zebaqui/notx-engine/internal/repo/index"
)

// ─────────────────────────────────────────────────────────────────────────────
// Directory layout
//
//   <dataDir>/
//     notes/           .notx files, one per note: <urn-uuid>.notx
//     events/          append-only event journals: <urn-uuid>.jsonl
//     index/           Badger database directory
// ─────────────────────────────────────────────────────────────────────────────

const (
	notesSubdir  = "notes"
	eventsSubdir = "events"
	indexSubdir  = "index"
)

// Provider is a file-system-backed implementation of repo.NoteRepository.
//
// Notes are stored as .notx files under <dataDir>/notes/.
// Each note's event stream is additionally journaled as newline-delimited JSON
// under <dataDir>/events/ for fast sequential reads without re-parsing the
// full .notx file.
// A Badger-backed index under <dataDir>/index/ provides fast list and search
// operations without reading every file on disk.
//
// Provider is safe for concurrent use.
type Provider struct {
	dataDir   string
	notesDir  string
	eventsDir string

	idx *index.Index

	mu sync.RWMutex // guards in-memory note cache and file writes
}

// New creates a new file-based Provider rooted at dataDir.
// It creates all required sub-directories if they do not exist and opens the
// Badger index. The caller must call Close when done.
func New(dataDir string) (*Provider, error) {
	notesDir := filepath.Join(dataDir, notesSubdir)
	eventsDir := filepath.Join(dataDir, eventsSubdir)
	indexDir := filepath.Join(dataDir, indexSubdir)

	for _, dir := range []string{notesDir, eventsDir, indexDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("file provider: create directory %q: %w", dir, err)
		}
	}

	idx, err := index.Open(indexDir)
	if err != nil {
		return nil, fmt.Errorf("file provider: open index: %w", err)
	}

	return &Provider{
		dataDir:   dataDir,
		notesDir:  notesDir,
		eventsDir: eventsDir,
		idx:       idx,
	}, nil
}

// Close releases all resources held by the provider (index database, etc.).
func (p *Provider) Close() error {
	return p.idx.Close()
}

// ─────────────────────────────────────────────────────────────────────────────
// Note lifecycle
// ─────────────────────────────────────────────────────────────────────────────

// Create persists a new note header. The note must have an empty event stream.
// Returns repo.ErrAlreadyExists if the URN is already in use.
func (p *Provider) Create(ctx context.Context, note *core.Note) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	urn := note.URN.String()
	notePath := p.noteFilePath(urn)

	p.mu.Lock()
	defer p.mu.Unlock()

	if _, err := os.Stat(notePath); err == nil {
		return fmt.Errorf("%w: %s", repo.ErrAlreadyExists, urn)
	}

	// Serialise header metadata to a compact JSON sidecar alongside the .notx file.
	meta := noteMetaFromNote(note)
	if err := p.writeMeta(urn, meta); err != nil {
		return err
	}

	// Write an empty .notx stub so the file exists on disk.
	if err := p.writeNotxStub(note); err != nil {
		_ = os.Remove(p.metaFilePath(urn))
		return err
	}

	// Update the index.
	entry := index.IndexEntry{
		URN:       urn,
		Name:      note.Name,
		NoteType:  note.NoteType.String(),
		Deleted:   note.Deleted,
		CreatedAt: note.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: note.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if note.ProjectURN != nil {
		entry.ProjectURN = note.ProjectURN.String()
	}
	if note.FolderURN != nil {
		entry.FolderURN = note.FolderURN.String()
	}

	if err := p.idx.Upsert(entry, ""); err != nil {
		_ = os.Remove(p.metaFilePath(urn))
		_ = os.Remove(notePath)
		return fmt.Errorf("file provider: index create: %w", err)
	}

	return nil
}

// Get retrieves a note by URN, replaying its full event stream.
// Returns repo.ErrNotFound if no note with that URN exists.
func (p *Provider) Get(ctx context.Context, urn string) (*core.Note, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	notePath := p.noteFilePath(urn)
	if _, err := os.Stat(notePath); os.IsNotExist(err) {
		return nil, fmt.Errorf("%w: %s", repo.ErrNotFound, urn)
	}

	note, err := core.NewNoteFromFile(notePath)
	if err != nil {
		return nil, fmt.Errorf("file provider: parse note %q: %w", urn, err)
	}

	// Replay events from the journal (they are the authoritative stream).
	events, err := p.readEventJournal(urn)
	if err != nil {
		return nil, err
	}
	for _, ev := range events {
		if ev.Sequence <= note.HeadSequence() {
			// Already applied via the .notx file; skip.
			continue
		}
		if err := note.ApplyEvent(ev); err != nil {
			return nil, fmt.Errorf("file provider: replay event seq %d for %q: %w", ev.Sequence, urn, err)
		}
	}

	return note, nil
}

// List returns a filtered, paginated list of notes using the Badger index.
// Only header metadata is returned (no event streams).
func (p *Provider) List(ctx context.Context, opts repo.ListOptions) (*repo.ListResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	idxOpts := index.ListOptions{
		IncludeDeleted: opts.IncludeDeleted,
		PageSize:       opts.PageSize,
		PageToken:      opts.PageToken,
	}
	if opts.ProjectURN != "" {
		idxOpts.ProjectURN = opts.ProjectURN
	}
	if opts.FolderURN != "" {
		idxOpts.FolderURN = opts.FolderURN
	}
	if opts.FilterByType {
		idxOpts.NoteType = opts.NoteTypeFilter.String()
	}

	entries, nextToken, err := p.idx.List(idxOpts)
	if err != nil {
		return nil, fmt.Errorf("file provider: list index: %w", err)
	}

	notes := make([]*core.Note, 0, len(entries))
	for _, e := range entries {
		n, err := noteFromIndexEntry(e)
		if err != nil {
			return nil, fmt.Errorf("file provider: reconstruct note from index: %w", err)
		}
		notes = append(notes, n)
	}

	return &repo.ListResult{
		Notes:         notes,
		NextPageToken: nextToken,
	}, nil
}

// Update persists mutable header field changes for an existing note.
// NoteType cannot be changed; returns repo.ErrNoteTypeImmutable if attempted.
func (p *Provider) Update(ctx context.Context, note *core.Note) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	urn := note.URN.String()

	p.mu.Lock()
	defer p.mu.Unlock()

	// Load existing meta to validate NoteType immutability.
	existing, err := p.readMeta(urn)
	if err != nil {
		return err
	}
	if existing == nil {
		return fmt.Errorf("%w: %s", repo.ErrNotFound, urn)
	}
	if existing.NoteType != note.NoteType.String() {
		return fmt.Errorf("%w: cannot change note_type from %q to %q",
			repo.ErrNoteTypeImmutable, existing.NoteType, note.NoteType.String())
	}

	// Persist updated meta.
	meta := noteMetaFromNote(note)
	if err := p.writeMeta(urn, meta); err != nil {
		return err
	}

	// Update the index.
	entry := index.IndexEntry{
		URN:       urn,
		Name:      note.Name,
		NoteType:  note.NoteType.String(),
		Deleted:   note.Deleted,
		CreatedAt: note.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: note.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if note.ProjectURN != nil {
		entry.ProjectURN = note.ProjectURN.String()
	}
	if note.FolderURN != nil {
		entry.FolderURN = note.FolderURN.String()
	}

	if err := p.idx.Upsert(entry, ""); err != nil {
		return fmt.Errorf("file provider: index update: %w", err)
	}
	return nil
}

// Delete soft-deletes a note by setting its Deleted flag.
// Returns repo.ErrNotFound if the note does not exist.
func (p *Provider) Delete(ctx context.Context, urn string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	meta, err := p.readMeta(urn)
	if err != nil {
		return err
	}
	if meta == nil {
		return fmt.Errorf("%w: %s", repo.ErrNotFound, urn)
	}

	meta.Deleted = true
	meta.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := p.writeMeta(urn, meta); err != nil {
		return err
	}

	// Update index entry — keep it present so list-with-deleted works.
	entry := index.IndexEntry{
		URN:        meta.URN,
		Name:       meta.Name,
		NoteType:   meta.NoteType,
		ProjectURN: meta.ProjectURN,
		FolderURN:  meta.FolderURN,
		Deleted:    true,
		CreatedAt:  meta.CreatedAt,
		UpdatedAt:  meta.UpdatedAt,
	}
	if err := p.idx.Upsert(entry, ""); err != nil {
		return fmt.Errorf("file provider: index soft-delete: %w", err)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Event stream
// ─────────────────────────────────────────────────────────────────────────────

// journaledEvent is the JSON record written to the event journal file.
type journaledEvent struct {
	URN       string         `json:"urn"`
	NoteURN   string         `json:"note_urn"`
	Sequence  int            `json:"sequence"`
	AuthorURN string         `json:"author_urn"`
	CreatedAt string         `json:"created_at"` // RFC3339
	Entries   []journalEntry `json:"entries,omitempty"`
	Encrypted *encryptedBlob `json:"encrypted,omitempty"`
}

type journalEntry struct {
	LineNumber int    `json:"ln"`
	Op         int    `json:"op"`
	Content    string `json:"c,omitempty"`
}

// encryptedBlob holds the opaque secure-note payload. The server stores this
// verbatim and never inspects the nonce, payload, or per-device keys.
type encryptedBlob struct {
	Nonce         []byte            `json:"nonce"`
	Payload       []byte            `json:"payload"`
	PerDeviceKeys map[string][]byte `json:"per_device_keys"`
}

// AppendEvent appends a single event to an existing note's event stream.
// For normal notes the content is forwarded to the search index.
// For secure notes the encrypted blob is stored verbatim; the index is never
// updated with decrypted content.
func (p *Provider) AppendEvent(ctx context.Context, event *core.Event, opts repo.AppendEventOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	noteURN := event.NoteURN.String()

	p.mu.Lock()
	defer p.mu.Unlock()

	meta, err := p.readMeta(noteURN)
	if err != nil {
		return err
	}
	if meta == nil {
		return fmt.Errorf("%w: %s", repo.ErrNotFound, noteURN)
	}

	// Optimistic concurrency check.
	if opts.ExpectSequence > 0 && meta.HeadSequence != opts.ExpectSequence-1 {
		return fmt.Errorf("%w: expected head %d, got %d",
			repo.ErrSequenceConflict, opts.ExpectSequence-1, meta.HeadSequence)
	}

	expectedSeq := meta.HeadSequence + 1
	if event.Sequence != expectedSeq {
		return fmt.Errorf("%w: expected sequence %d, got %d",
			repo.ErrSequenceConflict, expectedSeq, event.Sequence)
	}

	// Serialise to journal.
	je := journaledEventFromCore(event)
	if err := p.appendToJournal(noteURN, je); err != nil {
		return err
	}

	// Update meta.
	meta.HeadSequence = event.Sequence
	meta.UpdatedAt = event.CreatedAt.UTC().Format(time.RFC3339)
	if err := p.writeMeta(noteURN, meta); err != nil {
		return err
	}

	// Update index — only index content for normal notes.
	idxEntry := index.IndexEntry{
		URN:        meta.URN,
		Name:       meta.Name,
		NoteType:   meta.NoteType,
		ProjectURN: meta.ProjectURN,
		FolderURN:  meta.FolderURN,
		Deleted:    meta.Deleted,
		CreatedAt:  meta.CreatedAt,
		UpdatedAt:  meta.UpdatedAt,
	}

	var content string
	if meta.NoteType == "normal" {
		// Build content string from the line entries for indexing.
		var sb strings.Builder
		for _, e := range event.Entries {
			if e.Op == core.LineOpSet && e.Content != "" {
				sb.WriteString(e.Content)
				sb.WriteRune(' ')
			}
		}
		content = sb.String()
	}
	// For secure notes content stays empty — index.Upsert enforces the rule.

	if err := p.idx.Upsert(idxEntry, content); err != nil {
		return fmt.Errorf("file provider: index append event: %w", err)
	}
	return nil
}

// Events returns all events for a note starting at fromSequence (inclusive).
func (p *Provider) Events(ctx context.Context, noteURN string, fromSequence int) ([]*core.Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	meta, err := p.readMeta(noteURN)
	if err != nil {
		return nil, err
	}
	if meta == nil {
		return nil, fmt.Errorf("%w: %s", repo.ErrNotFound, noteURN)
	}

	all, err := p.readEventJournal(noteURN)
	if err != nil {
		return nil, err
	}

	if fromSequence <= 1 {
		return all, nil
	}

	var filtered []*core.Event
	for _, ev := range all {
		if ev.Sequence >= fromSequence {
			filtered = append(filtered, ev)
		}
	}
	return filtered, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Search
// ─────────────────────────────────────────────────────────────────────────────

// Search performs a full-text search over normal note content only.
// Secure notes can never appear in results.
func (p *Provider) Search(ctx context.Context, opts repo.SearchOptions) (*repo.SearchResults, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	maxResults := opts.PageSize
	if maxResults == 0 {
		maxResults = 50
	}

	urns, err := p.idx.Search(opts.Query, maxResults)
	if err != nil {
		return nil, fmt.Errorf("file provider: search: %w", err)
	}

	results := make([]*repo.SearchResult, 0, len(urns))
	for _, urn := range urns {
		entry, err := p.idx.Get(urn)
		if err != nil || entry == nil {
			continue
		}
		// Double-check: never return secure notes from search.
		if entry.NoteType == "secure" {
			continue
		}
		n, err := noteFromIndexEntry(*entry)
		if err != nil {
			continue
		}
		results = append(results, &repo.SearchResult{
			Note:    n,
			Excerpt: fmt.Sprintf("matched: %q in %q", opts.Query, n.Name),
		})
	}

	return &repo.SearchResults{
		Results:       results,
		NextPageToken: "", // pagination over search results is a future enhancement
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Internal helpers — file paths
// ─────────────────────────────────────────────────────────────────────────────

// noteFilePath returns the .notx file path for a given URN string.
// URN colons are replaced with underscores to produce a safe filename.
func (p *Provider) noteFilePath(urn string) string {
	return filepath.Join(p.notesDir, sanitiseURN(urn)+".notx")
}

func (p *Provider) metaFilePath(urn string) string {
	return filepath.Join(p.notesDir, sanitiseURN(urn)+".meta.json")
}

func (p *Provider) journalFilePath(urn string) string {
	return filepath.Join(p.eventsDir, sanitiseURN(urn)+".jsonl")
}

func sanitiseURN(urn string) string {
	return strings.ReplaceAll(urn, ":", "_")
}

// ─────────────────────────────────────────────────────────────────────────────
// Internal helpers — meta sidecar
// ─────────────────────────────────────────────────────────────────────────────

// noteMeta is the JSON sidecar stored alongside each .notx file.
// It caches mutable header fields so we avoid re-parsing the full .notx file
// for list, update, and append operations.
type noteMeta struct {
	URN          string `json:"urn"`
	Name         string `json:"name"`
	NoteType     string `json:"note_type"`
	ProjectURN   string `json:"project_urn,omitempty"`
	FolderURN    string `json:"folder_urn,omitempty"`
	Deleted      bool   `json:"deleted"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	HeadSequence int    `json:"head_sequence"`
}

func noteMetaFromNote(n *core.Note) *noteMeta {
	m := &noteMeta{
		URN:          n.URN.String(),
		Name:         n.Name,
		NoteType:     n.NoteType.String(),
		Deleted:      n.Deleted,
		CreatedAt:    n.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:    n.UpdatedAt.UTC().Format(time.RFC3339),
		HeadSequence: n.HeadSequence(),
	}
	if n.ProjectURN != nil {
		m.ProjectURN = n.ProjectURN.String()
	}
	if n.FolderURN != nil {
		m.FolderURN = n.FolderURN.String()
	}
	return m
}

func (p *Provider) writeMeta(urn string, meta *noteMeta) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("file provider: marshal meta for %q: %w", urn, err)
	}
	path := p.metaFilePath(urn)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("file provider: write meta %q: %w", path, err)
	}
	return nil
}

func (p *Provider) readMeta(urn string) (*noteMeta, error) {
	path := p.metaFilePath(urn)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("file provider: read meta %q: %w", path, err)
	}
	var meta noteMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("file provider: unmarshal meta %q: %w", path, err)
	}
	return &meta, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Internal helpers — .notx stub
// ─────────────────────────────────────────────────────────────────────────────

// writeNotxStub writes a minimal valid .notx file header for a newly created
// note. Events are journaled separately; the stub is a human-readable anchor.
func (p *Provider) writeNotxStub(note *core.Note) error {
	urn := note.URN.String()
	noteType := note.NoteType.String()
	createdAt := note.CreatedAt.UTC().Format(time.RFC3339)

	var sb strings.Builder
	sb.WriteString("# notx/1.0\n")
	fmt.Fprintf(&sb, "# note_urn:      %s\n", urn)
	fmt.Fprintf(&sb, "# note_type:     %s\n", noteType)
	fmt.Fprintf(&sb, "# name:          %s\n", note.Name)
	fmt.Fprintf(&sb, "# created_at:    %s\n", createdAt)
	sb.WriteString("# head_sequence: 0\n")

	if note.ProjectURN != nil {
		fmt.Fprintf(&sb, "# project_urn:   %s\n", note.ProjectURN.String())
	}
	if note.FolderURN != nil {
		fmt.Fprintf(&sb, "# folder_urn:    %s\n", note.FolderURN.String())
	}
	sb.WriteString("\n")

	path := p.noteFilePath(urn)
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		return fmt.Errorf("file provider: write notx stub %q: %w", path, err)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Internal helpers — event journal
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) appendToJournal(noteURN string, je journaledEvent) error {
	data, err := json.Marshal(je)
	if err != nil {
		return fmt.Errorf("file provider: marshal event for %q: %w", noteURN, err)
	}
	data = append(data, '\n')

	f, err := os.OpenFile(p.journalFilePath(noteURN), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("file provider: open journal for %q: %w", noteURN, err)
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("file provider: write journal for %q: %w", noteURN, err)
	}
	return nil
}

func (p *Provider) readEventJournal(noteURN string) ([]*core.Event, error) {
	path := p.journalFilePath(noteURN)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("file provider: read journal for %q: %w", noteURN, err)
	}

	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	events := make([]*core.Event, 0, len(lines))

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var je journaledEvent
		if err := json.Unmarshal([]byte(line), &je); err != nil {
			return nil, fmt.Errorf("file provider: parse journal line for %q: %w", noteURN, err)
		}
		ev, err := coreEventFromJournaled(je)
		if err != nil {
			return nil, fmt.Errorf("file provider: decode event for %q: %w", noteURN, err)
		}
		events = append(events, ev)
	}
	return events, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Conversion helpers
// ─────────────────────────────────────────────────────────────────────────────

func journaledEventFromCore(ev *core.Event) journaledEvent {
	je := journaledEvent{
		URN:       ev.URN.String(),
		NoteURN:   ev.NoteURN.String(),
		Sequence:  ev.Sequence,
		AuthorURN: ev.AuthorURN.String(),
		CreatedAt: ev.CreatedAt.UTC().Format(time.RFC3339),
	}
	for _, e := range ev.Entries {
		je.Entries = append(je.Entries, journalEntry{
			LineNumber: e.LineNumber,
			Op:         int(e.Op),
			Content:    e.Content,
		})
	}
	return je
}

func coreEventFromJournaled(je journaledEvent) (*core.Event, error) {
	noteURN, err := core.ParseURN(je.NoteURN)
	if err != nil {
		return nil, fmt.Errorf("parse note_urn: %w", err)
	}
	authorURN, err := core.ParseURN(je.AuthorURN)
	if err != nil {
		return nil, fmt.Errorf("parse author_urn: %w", err)
	}
	createdAt, err := time.Parse(time.RFC3339, je.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("parse created_at: %w", err)
	}

	var urn core.URN
	if je.URN != "" && je.URN != "::" {
		urn, err = core.ParseURN(je.URN)
		if err != nil {
			// Non-fatal: events may not carry their own URN.
			urn = core.URN{}
		}
	}

	entries := make([]core.LineEntry, 0, len(je.Entries))
	for _, e := range je.Entries {
		entries = append(entries, core.LineEntry{
			LineNumber: e.LineNumber,
			Op:         core.LineOp(e.Op),
			Content:    e.Content,
		})
	}

	return &core.Event{
		URN:       urn,
		NoteURN:   noteURN,
		Sequence:  je.Sequence,
		AuthorURN: authorURN,
		CreatedAt: createdAt,
		Entries:   entries,
	}, nil
}

func noteFromIndexEntry(e index.IndexEntry) (*core.Note, error) {
	urn, err := core.ParseURN(e.URN)
	if err != nil {
		return nil, fmt.Errorf("parse urn %q: %w", e.URN, err)
	}
	createdAt, err := time.Parse(time.RFC3339, e.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("parse created_at for %q: %w", e.URN, err)
	}
	updatedAt, err := time.Parse(time.RFC3339, e.UpdatedAt)
	if err != nil {
		updatedAt = createdAt
	}

	noteType, err := core.ParseNoteType(e.NoteType)
	if err != nil {
		noteType = core.NoteTypeNormal
	}

	n := core.NewNote(urn, e.Name, createdAt)
	n.NoteType = noteType
	n.Deleted = e.Deleted
	n.UpdatedAt = updatedAt

	if e.ProjectURN != "" {
		projURN, err := core.ParseURN(e.ProjectURN)
		if err == nil {
			n.ProjectURN = &projURN
		}
	}
	if e.FolderURN != "" {
		folderURN, err := core.ParseURN(e.FolderURN)
		if err == nil {
			n.FolderURN = &folderURN
		}
	}

	return n, nil
}
