package file

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/zebaqui/notx-engine/core"
	"github.com/zebaqui/notx-engine/internal/repo"
	"github.com/zebaqui/notx-engine/internal/repo/index"
)

// tracer returns a fresh Tracer from the current global provider so that any
// provider swap (e.g. in tests) is visible to all callers.
func tracer() oteltrace.Tracer {
	return otel.Tracer("notx/repo/file")
}

// ─────────────────────────────────────────────────────────────────────────────
// Directory layout
//
//   <dataDir>/
//     notes/   — one .notx file per note, append-only event stream
//     index/   — Badger database (metadata cache + materialized content + FTS)
//
// There are no .meta.json sidecars and no .jsonl journals.
// Badger is the single fast-read cache. The .notx files are the canonical
// on-disk truth. On startup the provider reconciles them.
// ─────────────────────────────────────────────────────────────────────────────

const (
	notesSubdir = "notes"
	indexSubdir = "index"
)

// Provider is a file-system-backed implementation of repo.NoteRepository.
//
// Write path  — every AppendEvent call:
//  1. Appends the event block to the .notx file (single O_APPEND write).
//  2. Rewrites the # head_sequence header line in the .notx file.
//  3. Updates Badger with the new metadata + materialised content.
//
// Read path   — Get, List, Events:
//   - Get:    served entirely from Badger (header + content). No file I/O.
//   - List:   served entirely from Badger.
//   - Events: parses the .notx file (only called when history is requested).
//   - Search: served from Badger FTS index.
//
// Startup reconciliation — New():
//
//	Scans all .notx files, compares each file's head_sequence against what
//	Badger knows. Any file that is ahead of (or absent from) Badger is
//	re-parsed and re-indexed. This covers manual file edits, copies, and
//	any crash that left Badger stale.
//
// Provider is safe for concurrent use.
type Provider struct {
	dataDir  string
	notesDir string
	idx      *index.Index
	mu       sync.RWMutex
}

// New creates a new Provider rooted at dataDir, opens Badger, and reconciles
// the notes directory against the index before returning.
func New(dataDir string) (*Provider, error) {
	notesDir := filepath.Join(dataDir, notesSubdir)
	indexDir := filepath.Join(dataDir, indexSubdir)

	for _, dir := range []string{notesDir, indexDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("file provider: create directory %q: %w", dir, err)
		}
	}

	idx, err := index.Open(indexDir)
	if err != nil {
		return nil, fmt.Errorf("file provider: open index: %w", err)
	}

	p := &Provider{
		dataDir:  dataDir,
		notesDir: notesDir,
		idx:      idx,
	}

	if err := p.reconcile(); err != nil {
		_ = idx.Close()
		return nil, fmt.Errorf("file provider: reconcile: %w", err)
	}

	return p, nil
}

// Close releases all resources held by the provider.
func (p *Provider) Close() error {
	return p.idx.Close()
}

// ─────────────────────────────────────────────────────────────────────────────
// Startup reconciliation
// ─────────────────────────────────────────────────────────────────────────────

// reconcile scans every .notx file in notesDir and re-indexes any file whose
// head_sequence is ahead of what Badger currently knows. This keeps the cache
// consistent after crashes, manual file copies, or out-of-band edits.
func (p *Provider) reconcile() error {
	entries, err := os.ReadDir(p.notesDir)
	if err != nil {
		return fmt.Errorf("read notes dir: %w", err)
	}

	for _, de := range entries {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".notx") {
			continue
		}

		notePath := filepath.Join(p.notesDir, de.Name())

		// Peek at just the header to get the URN and head_sequence cheaply.
		urn, fileSeq, err := peekNotxHeader(notePath)
		if err != nil {
			// Corrupt or unrecognised file — skip, don't crash the server.
			continue
		}

		// Check what Badger knows.
		existing, err := p.idx.Get(urn)
		if err != nil {
			return fmt.Errorf("index get %q: %w", urn, err)
		}

		if existing != nil && existing.HeadSequence >= fileSeq {
			// Badger is current — nothing to do.
			continue
		}

		// File is ahead of the index (or unknown): parse and re-index.
		note, err := core.NewNoteFromFile(notePath)
		if err != nil {
			continue
		}

		if err := p.indexNote(note); err != nil {
			return fmt.Errorf("re-index %q: %w", urn, err)
		}
	}

	return nil
}

// peekNotxHeader reads just the comment header block of a .notx file and
// returns the note URN string and the head_sequence value. It does not parse
// the event stream.
func peekNotxHeader(path string) (urn string, headSeq int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "#") && strings.TrimSpace(line) != "" {
			break // past the header block
		}
		if strings.HasPrefix(line, "# note_urn:") {
			urn = strings.TrimSpace(strings.TrimPrefix(line, "# note_urn:"))
		}
		if strings.HasPrefix(line, "# head_sequence:") {
			raw := strings.TrimSpace(strings.TrimPrefix(line, "# head_sequence:"))
			_, _ = fmt.Sscanf(raw, "%d", &headSeq)
		}
	}
	if urn == "" {
		return "", 0, fmt.Errorf("no note_urn found in %q", path)
	}
	return urn, headSeq, scanner.Err()
}

// indexNote upserts a fully-parsed note (with all events applied) into Badger,
// storing both the metadata and the materialised content string.
func (p *Provider) indexNote(note *core.Note) error {
	urn := note.URN.String()

	entry := index.IndexEntry{
		URN:          urn,
		Name:         note.Name,
		NoteType:     note.NoteType.String(),
		Deleted:      note.Deleted,
		CreatedAt:    note.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:    note.UpdatedAt.UTC().Format(time.RFC3339),
		HeadSequence: note.HeadSequence(),
	}
	if note.ProjectURN != nil {
		entry.ProjectURN = note.ProjectURN.String()
	}
	if note.FolderURN != nil {
		entry.FolderURN = note.FolderURN.String()
	}

	var content string
	if note.NoteType == core.NoteTypeNormal {
		content = note.Content()
	}

	return p.idx.Upsert(entry, content)
}

// ─────────────────────────────────────────────────────────────────────────────
// Note lifecycle
// ─────────────────────────────────────────────────────────────────────────────

// Create persists a new note. Writes the .notx header stub and indexes it.
// Returns repo.ErrAlreadyExists if the URN is already in use.
func (p *Provider) Create(ctx context.Context, note *core.Note) error {
	ctx, span := tracer().Start(ctx, "file.Create")
	defer span.End()

	if err := ctx.Err(); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	urn := note.URN.String()
	span.SetAttributes(
		attribute.String("note.urn", urn),
		attribute.String("note.type", note.NoteType.String()),
	)

	p.mu.Lock()
	defer p.mu.Unlock()

	notePath := p.noteFilePath(urn)
	if _, err := os.Stat(notePath); err == nil {
		err2 := fmt.Errorf("%w: %s", repo.ErrAlreadyExists, urn)
		span.RecordError(err2)
		span.SetStatus(codes.Error, err2.Error())
		return err2
	}

	if err := p.writeNotxStub(note); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	_, idxSpan := tracer().Start(ctx, "file.Create/index.Upsert")
	if err := p.indexNote(note); err != nil {
		idxSpan.RecordError(err)
		idxSpan.SetStatus(codes.Error, err.Error())
		idxSpan.End()
		_ = os.Remove(notePath)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("file provider: index create: %w", err)
	}
	idxSpan.End()

	return nil
}

// Get returns the note header and materialised content from Badger.
// No .notx file I/O occurs on the hot read path.
func (p *Provider) Get(ctx context.Context, urn string) (*core.Note, error) {
	ctx, span := tracer().Start(ctx, "file.Get")
	defer span.End()
	span.SetAttributes(attribute.String("note.urn", urn))

	if err := ctx.Err(); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	_, idxSpan := tracer().Start(ctx, "file.Get/index.Get")
	entry, err := p.idx.Get(urn)
	idxSpan.End()
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, fmt.Errorf("file provider: index get: %w", err)
	}
	if entry == nil {
		// Not in index — fall back to file if it exists (e.g. after manual copy).
		notePath := p.noteFilePath(urn)
		if _, statErr := os.Stat(notePath); os.IsNotExist(statErr) {
			err2 := fmt.Errorf("%w: %s", repo.ErrNotFound, urn)
			span.RecordError(err2)
			span.SetStatus(codes.Error, err2.Error())
			return nil, err2
		}
		note, parseErr := core.NewNoteFromFile(notePath)
		if parseErr != nil {
			span.RecordError(parseErr)
			span.SetStatus(codes.Error, parseErr.Error())
			return nil, fmt.Errorf("file provider: fallback parse %q: %w", urn, parseErr)
		}
		// Re-index so next read is fast.
		_ = p.indexNote(note)
		span.SetAttributes(attribute.Bool("cache.miss", true))
		return note, nil
	}

	// Build the note from the index entry (no file I/O).
	_, buildSpan := tracer().Start(ctx, "file.Get/buildFromIndex")

	var note *core.Note
	if entry.NoteType == "normal" {
		_, contentSpan := tracer().Start(ctx, "file.Get/index.GetContent")
		content, contentErr := p.idx.GetContent(urn)
		contentSpan.End()
		if contentErr != nil {
			span.RecordError(contentErr)
			span.SetStatus(codes.Error, contentErr.Error())
			buildSpan.End()
			return nil, fmt.Errorf("file provider: get content: %w", contentErr)
		}
		noteURN, parseErr := core.ParseURN(urn)
		if parseErr != nil {
			span.RecordError(parseErr)
			span.SetStatus(codes.Error, parseErr.Error())
			buildSpan.End()
			return nil, fmt.Errorf("file provider: parse urn: %w", parseErr)
		}
		createdAt, _ := time.Parse(time.RFC3339, entry.CreatedAt)
		updatedAt, _ := time.Parse(time.RFC3339, entry.UpdatedAt)
		note = core.NewNoteAtSequence(noteURN, entry.Name, createdAt, updatedAt, entry.HeadSequence, content)
		note.NoteType, _ = core.ParseNoteType(entry.NoteType)
		note.Deleted = entry.Deleted
		if entry.ProjectURN != "" {
			if purn, e := core.ParseURN(entry.ProjectURN); e == nil {
				note.ProjectURN = &purn
			}
		}
		if entry.FolderURN != "" {
			if furn, e := core.ParseURN(entry.FolderURN); e == nil {
				note.FolderURN = &furn
			}
		}
	} else {
		var buildErr error
		note, buildErr = noteFromIndexEntry(*entry)
		if buildErr != nil {
			span.RecordError(buildErr)
			span.SetStatus(codes.Error, buildErr.Error())
			buildSpan.End()
			return nil, fmt.Errorf("file provider: build note from index: %w", buildErr)
		}
	}
	buildSpan.End()

	span.SetAttributes(
		attribute.Int("note.head_sequence", note.HeadSequence()),
		attribute.Bool("cache.miss", false),
	)
	return note, nil
}

// List returns a paginated list of note headers from Badger.
func (p *Provider) List(ctx context.Context, opts repo.ListOptions) (*repo.ListResult, error) {
	ctx, span := tracer().Start(ctx, "file.List")
	defer span.End()
	span.SetAttributes(
		attribute.Int("opts.page_size", opts.PageSize),
		attribute.Bool("opts.include_deleted", opts.IncludeDeleted),
	)

	if err := ctx.Err(); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	idxOpts := index.ListOptions{
		IncludeDeleted: opts.IncludeDeleted,
		PageSize:       opts.PageSize,
		PageToken:      opts.PageToken,
		ProjectURN:     opts.ProjectURN,
		FolderURN:      opts.FolderURN,
	}
	if opts.FilterByType {
		idxOpts.NoteType = opts.NoteTypeFilter.String()
	}

	_, idxSpan := tracer().Start(ctx, "file.List/index.List")
	entries, nextToken, err := p.idx.List(idxOpts)
	idxSpan.SetAttributes(attribute.Int("index.results", len(entries)))
	idxSpan.End()
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, fmt.Errorf("file provider: list index: %w", err)
	}

	notes := make([]*core.Note, 0, len(entries))
	for _, e := range entries {
		n, err := noteFromIndexEntry(e)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return nil, fmt.Errorf("file provider: build note from index: %w", err)
		}
		notes = append(notes, n)
	}

	span.SetAttributes(attribute.Int("results.count", len(notes)))
	return &repo.ListResult{
		Notes:         notes,
		NextPageToken: nextToken,
	}, nil
}

// Update persists changes to a note's mutable header fields.
// NoteType is immutable; returns repo.ErrNoteTypeImmutable if altered.
func (p *Provider) Update(ctx context.Context, note *core.Note) error {
	ctx, span := tracer().Start(ctx, "file.Update")
	defer span.End()

	if err := ctx.Err(); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	urn := note.URN.String()
	span.SetAttributes(attribute.String("note.urn", urn))

	p.mu.Lock()
	defer p.mu.Unlock()

	existing, err := p.idx.Get(urn)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("file provider: index get for update: %w", err)
	}
	if existing == nil {
		err2 := fmt.Errorf("%w: %s", repo.ErrNotFound, urn)
		span.RecordError(err2)
		span.SetStatus(codes.Error, err2.Error())
		return err2
	}
	if existing.NoteType != note.NoteType.String() {
		err2 := fmt.Errorf("%w: cannot change note_type from %q to %q",
			repo.ErrNoteTypeImmutable, existing.NoteType, note.NoteType.String())
		span.RecordError(err2)
		span.SetStatus(codes.Error, err2.Error())
		return err2
	}

	// Preserve head_sequence and content — only header metadata changes.
	entry := index.IndexEntry{
		URN:          urn,
		Name:         note.Name,
		NoteType:     existing.NoteType,
		Deleted:      note.Deleted,
		CreatedAt:    existing.CreatedAt,
		UpdatedAt:    note.UpdatedAt.UTC().Format(time.RFC3339),
		HeadSequence: existing.HeadSequence,
	}
	if note.ProjectURN != nil {
		entry.ProjectURN = note.ProjectURN.String()
	}
	if note.FolderURN != nil {
		entry.FolderURN = note.FolderURN.String()
	}

	// Get existing content to preserve it through the Upsert.
	var content string
	if existing.NoteType == "normal" {
		content, err = p.idx.GetContent(urn)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return fmt.Errorf("file provider: get content for update: %w", err)
		}
	}

	_, idxSpan := tracer().Start(ctx, "file.Update/index.Upsert")
	if err := p.idx.Upsert(entry, content); err != nil {
		idxSpan.RecordError(err)
		idxSpan.SetStatus(codes.Error, err.Error())
		idxSpan.End()
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("file provider: index update: %w", err)
	}
	idxSpan.End()
	return nil
}

// Delete soft-deletes a note by setting its Deleted flag.
// Returns repo.ErrNotFound if the note does not exist.
func (p *Provider) Delete(ctx context.Context, urn string) error {
	ctx, span := tracer().Start(ctx, "file.Delete")
	defer span.End()
	span.SetAttributes(attribute.String("note.urn", urn))

	if err := ctx.Err(); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	existing, err := p.idx.Get(urn)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("file provider: index get for delete: %w", err)
	}
	if existing == nil {
		err2 := fmt.Errorf("%w: %s", repo.ErrNotFound, urn)
		span.RecordError(err2)
		span.SetStatus(codes.Error, err2.Error())
		return err2
	}

	existing.Deleted = true
	existing.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	var content string
	if existing.NoteType == "normal" {
		content, err = p.idx.GetContent(urn)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return fmt.Errorf("file provider: get content for delete: %w", err)
		}
	}

	_, idxSpan := tracer().Start(ctx, "file.Delete/index.Upsert")
	if err := p.idx.Upsert(*existing, content); err != nil {
		idxSpan.RecordError(err)
		idxSpan.SetStatus(codes.Error, err.Error())
		idxSpan.End()
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("file provider: index soft-delete: %w", err)
	}
	idxSpan.End()
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Event stream
// ─────────────────────────────────────────────────────────────────────────────

// AppendEvent appends a single event to the note's .notx file and updates
// the Badger cache atomically (from the caller's perspective).
//
// Write sequence:
//  1. Validate sequence via Badger (no file read).
//  2. Append the event block to the .notx file (O_APPEND).
//  3. Rewrite the # head_sequence header line in-place.
//  4. Upsert Badger with updated metadata + new materialised content.
func (p *Provider) AppendEvent(ctx context.Context, event *core.Event, opts repo.AppendEventOptions) error {
	ctx, span := tracer().Start(ctx, "file.AppendEvent")
	defer span.End()

	noteURN := event.NoteURN.String()
	span.SetAttributes(
		attribute.String("note.urn", noteURN),
		attribute.Int("event.sequence", event.Sequence),
		attribute.Int("event.entries", len(event.Entries)),
	)

	if err := ctx.Err(); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// ── 1. Validate via Badger ────────────────────────────────────────────────
	existing, err := p.idx.Get(noteURN)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("file provider: index get for append: %w", err)
	}
	if existing == nil {
		err2 := fmt.Errorf("%w: %s", repo.ErrNotFound, noteURN)
		span.RecordError(err2)
		span.SetStatus(codes.Error, err2.Error())
		return err2
	}

	if opts.ExpectSequence > 0 && existing.HeadSequence != opts.ExpectSequence-1 {
		err2 := fmt.Errorf("%w: expected head %d, got %d",
			repo.ErrSequenceConflict, opts.ExpectSequence-1, existing.HeadSequence)
		span.RecordError(err2)
		span.SetStatus(codes.Error, err2.Error())
		return err2
	}

	expectedSeq := existing.HeadSequence + 1
	if event.Sequence != expectedSeq {
		err2 := fmt.Errorf("%w: expected sequence %d, got %d",
			repo.ErrSequenceConflict, expectedSeq, event.Sequence)
		span.RecordError(err2)
		span.SetStatus(codes.Error, err2.Error())
		return err2
	}

	// ── 2. Append event to .notx file ─────────────────────────────────────────
	_, notxSpan := tracer().Start(ctx, "file.AppendEvent/writeEventToNotx")
	if err := p.writeEventToNotx(noteURN, event); err != nil {
		notxSpan.RecordError(err)
		notxSpan.SetStatus(codes.Error, err.Error())
		notxSpan.End()
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	notxSpan.End()

	// ── 3. Update head_sequence header in .notx file ──────────────────────────
	_, hdrSpan := tracer().Start(ctx, "file.AppendEvent/updateNotxHeader")
	if err := p.updateNotxHeader(noteURN, event.Sequence); err != nil {
		hdrSpan.RecordError(err)
		hdrSpan.SetStatus(codes.Error, err.Error())
		hdrSpan.End()
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	hdrSpan.End()

	// ── 4. Update Badger cache ─────────────────────────────────────────────────
	// Compute new materialised content by applying the event to the existing
	// cached content — no full file re-parse needed.
	_, idxSpan := tracer().Start(ctx, "file.AppendEvent/index.Upsert")

	newContent, err := p.applyEventToContent(existing, event)
	if err != nil {
		idxSpan.RecordError(err)
		idxSpan.SetStatus(codes.Error, err.Error())
		idxSpan.End()
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("file provider: apply event to content: %w", err)
	}

	updated := index.IndexEntry{
		URN:          existing.URN,
		Name:         existing.Name,
		NoteType:     existing.NoteType,
		ProjectURN:   existing.ProjectURN,
		FolderURN:    existing.FolderURN,
		Deleted:      existing.Deleted,
		CreatedAt:    existing.CreatedAt,
		UpdatedAt:    event.CreatedAt.UTC().Format(time.RFC3339),
		HeadSequence: event.Sequence,
	}

	if err := p.idx.Upsert(updated, newContent); err != nil {
		idxSpan.RecordError(err)
		idxSpan.SetStatus(codes.Error, err.Error())
		idxSpan.End()
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("file provider: index append event: %w", err)
	}
	idxSpan.End()
	return nil
}

// applyEventToContent fetches the current cached content from Badger and
// applies the new event's line entries directly on the line slice — no Note
// construction, no sequence-number gymnastics.  Returns "" for secure notes.
func (p *Provider) applyEventToContent(existing *index.IndexEntry, event *core.Event) (string, error) {
	if existing.NoteType != "normal" {
		return "", nil
	}

	cached, err := p.idx.GetContent(existing.URN)
	if err != nil {
		return "", fmt.Errorf("get cached content: %w", err)
	}

	oldLines := core.SplitLines(cached)
	newLines := applyEntriesToLines(oldLines, event.Entries)
	return strings.Join(newLines, "\n"), nil
}

// applyEntriesToLines applies a slice of LineEntry operations to a line slice
// and returns the updated slice.  It mirrors the semantics of core.applyEvent
// but operates directly on []string so it can be used outside the Note type.
func applyEntriesToLines(lines []string, entries []core.LineEntry) []string {
	var deletes []int

	for _, e := range entries {
		switch e.Op {
		case core.LineOpDelete:
			deletes = append(deletes, e.LineNumber)
		case core.LineOpSetEmpty:
			idx := e.LineNumber - 1
			switch {
			case idx < len(lines):
				lines[idx] = ""
			case idx == len(lines):
				lines = append(lines, "")
			default:
				for len(lines) < idx {
					lines = append(lines, "")
				}
				lines = append(lines, "")
			}
		default: // LineOpSet
			idx := e.LineNumber - 1
			switch {
			case idx < len(lines):
				lines[idx] = e.Content
			case idx == len(lines):
				lines = append(lines, e.Content)
			default:
				for len(lines) < idx {
					lines = append(lines, "")
				}
				lines = append(lines, e.Content)
			}
		}
	}

	// Apply deletes highest-first to avoid index drift.
	for i := 0; i < len(deletes)-1; i++ {
		for j := i + 1; j < len(deletes); j++ {
			if deletes[j] > deletes[i] {
				deletes[i], deletes[j] = deletes[j], deletes[i]
			}
		}
	}
	for _, lineNum := range deletes {
		idx := lineNum - 1
		if idx >= 0 && idx < len(lines) {
			lines = append(lines[:idx], lines[idx+1:]...)
		}
	}

	return lines
}

// Events returns the event stream for a note by parsing the .notx file.
// This is the only operation that reads from disk after startup.
func (p *Provider) Events(ctx context.Context, noteURN string, fromSequence int) ([]*core.Event, error) {
	ctx, span := tracer().Start(ctx, "file.Events")
	defer span.End()
	span.SetAttributes(
		attribute.String("note.urn", noteURN),
		attribute.Int("from_sequence", fromSequence),
	)

	if err := ctx.Err(); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	// Confirm the note exists.
	existing, err := p.idx.Get(noteURN)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, fmt.Errorf("file provider: index get for events: %w", err)
	}
	if existing == nil {
		err2 := fmt.Errorf("%w: %s", repo.ErrNotFound, noteURN)
		span.RecordError(err2)
		span.SetStatus(codes.Error, err2.Error())
		return nil, err2
	}

	// Parse events from the .notx file.
	_, parseSpan := tracer().Start(ctx, "file.Events/parseNotxFile")
	notePath := p.noteFilePath(noteURN)
	note, err := core.NewNoteFromFile(notePath)
	parseSpan.End()
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, fmt.Errorf("file provider: parse note for events %q: %w", noteURN, err)
	}

	all := note.Events()
	span.SetAttributes(attribute.Int("events.total", len(all)))

	if fromSequence <= 1 {
		span.SetAttributes(attribute.Int("events.returned", len(all)))
		return all, nil
	}

	var filtered []*core.Event
	for _, ev := range all {
		if ev.Sequence >= fromSequence {
			filtered = append(filtered, ev)
		}
	}
	span.SetAttributes(attribute.Int("events.returned", len(filtered)))
	return filtered, nil
}

// Search performs full-text search over normal note content via Badger.
func (p *Provider) Search(ctx context.Context, opts repo.SearchOptions) (*repo.SearchResults, error) {
	ctx, span := tracer().Start(ctx, "file.Search")
	defer span.End()
	span.SetAttributes(
		attribute.String("query", opts.Query),
		attribute.Int("opts.page_size", opts.PageSize),
	)

	if err := ctx.Err(); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	maxResults := opts.PageSize
	if maxResults == 0 {
		maxResults = 50
	}

	_, idxSpan := tracer().Start(ctx, "file.Search/index.Search")
	urns, err := p.idx.Search(opts.Query, maxResults)
	idxSpan.SetAttributes(attribute.Int("index.hits", len(urns)))
	idxSpan.End()
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, fmt.Errorf("file provider: search: %w", err)
	}

	results := make([]*repo.SearchResult, 0, len(urns))
	for _, urn := range urns {
		entry, err := p.idx.Get(urn)
		if err != nil || entry == nil {
			continue
		}
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

	span.SetAttributes(attribute.Int("results.count", len(results)))
	return &repo.SearchResults{
		Results:       results,
		NextPageToken: "",
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// ProjectRepository implementation
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) CreateProject(ctx context.Context, proj *core.Project) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	existing, err := p.idx.GetProject(proj.URN.String())
	if err != nil {
		return fmt.Errorf("file provider: create project: %w", err)
	}
	if existing != nil {
		return fmt.Errorf("%w: %s", repo.ErrAlreadyExists, proj.URN.String())
	}

	entry := index.ProjectEntry{
		URN:         proj.URN.String(),
		Name:        proj.Name,
		Description: proj.Description,
		Deleted:     proj.Deleted,
		CreatedAt:   proj.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:   proj.UpdatedAt.UTC().Format(time.RFC3339),
	}
	return p.idx.UpsertProject(entry)
}

func (p *Provider) GetProject(ctx context.Context, urn string) (*core.Project, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entry, err := p.idx.GetProject(urn)
	if err != nil {
		return nil, fmt.Errorf("file provider: get project: %w", err)
	}
	if entry == nil {
		return nil, fmt.Errorf("%w: %s", repo.ErrNotFound, urn)
	}
	return projectEntryToCore(entry)
}

func (p *Provider) ListProjects(ctx context.Context, opts repo.ProjectListOptions) (*repo.ProjectListResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}
	entries, nextToken, err := p.idx.ListProjects(opts.IncludeDeleted, pageSize, opts.PageToken)
	if err != nil {
		return nil, fmt.Errorf("file provider: list projects: %w", err)
	}
	projects := make([]*core.Project, 0, len(entries))
	for i := range entries {
		proj, err := projectEntryToCore(&entries[i])
		if err != nil {
			return nil, err
		}
		projects = append(projects, proj)
	}
	return &repo.ProjectListResult{Projects: projects, NextPageToken: nextToken}, nil
}

func (p *Provider) UpdateProject(ctx context.Context, proj *core.Project) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	existing, err := p.idx.GetProject(proj.URN.String())
	if err != nil {
		return fmt.Errorf("file provider: update project: %w", err)
	}
	if existing == nil {
		return fmt.Errorf("%w: %s", repo.ErrNotFound, proj.URN.String())
	}

	entry := index.ProjectEntry{
		URN:         proj.URN.String(),
		Name:        proj.Name,
		Description: proj.Description,
		Deleted:     proj.Deleted,
		CreatedAt:   existing.CreatedAt, // preserve original
		UpdatedAt:   proj.UpdatedAt.UTC().Format(time.RFC3339),
	}
	return p.idx.UpsertProject(entry)
}

func (p *Provider) DeleteProject(ctx context.Context, urn string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	existing, err := p.idx.GetProject(urn)
	if err != nil {
		return fmt.Errorf("file provider: delete project: %w", err)
	}
	if existing == nil {
		return fmt.Errorf("%w: %s", repo.ErrNotFound, urn)
	}

	existing.Deleted = true
	existing.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	return p.idx.UpsertProject(*existing)
}

func (p *Provider) CreateFolder(ctx context.Context, f *core.Folder) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	existing, err := p.idx.GetFolder(f.URN.String())
	if err != nil {
		return fmt.Errorf("file provider: create folder: %w", err)
	}
	if existing != nil {
		return fmt.Errorf("%w: %s", repo.ErrAlreadyExists, f.URN.String())
	}

	entry := index.FolderEntry{
		URN:         f.URN.String(),
		ProjectURN:  f.ProjectURN.String(),
		Name:        f.Name,
		Description: f.Description,
		Deleted:     f.Deleted,
		CreatedAt:   f.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:   f.UpdatedAt.UTC().Format(time.RFC3339),
	}
	return p.idx.UpsertFolder(entry)
}

func (p *Provider) GetFolder(ctx context.Context, urn string) (*core.Folder, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entry, err := p.idx.GetFolder(urn)
	if err != nil {
		return nil, fmt.Errorf("file provider: get folder: %w", err)
	}
	if entry == nil {
		return nil, fmt.Errorf("%w: %s", repo.ErrNotFound, urn)
	}
	return folderEntryToCore(entry)
}

func (p *Provider) ListFolders(ctx context.Context, opts repo.FolderListOptions) (*repo.FolderListResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}
	entries, nextToken, err := p.idx.ListFolders(opts.ProjectURN, opts.IncludeDeleted, pageSize, opts.PageToken)
	if err != nil {
		return nil, fmt.Errorf("file provider: list folders: %w", err)
	}
	folders := make([]*core.Folder, 0, len(entries))
	for i := range entries {
		f, err := folderEntryToCore(&entries[i])
		if err != nil {
			return nil, err
		}
		folders = append(folders, f)
	}
	return &repo.FolderListResult{Folders: folders, NextPageToken: nextToken}, nil
}

func (p *Provider) UpdateFolder(ctx context.Context, f *core.Folder) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	existing, err := p.idx.GetFolder(f.URN.String())
	if err != nil {
		return fmt.Errorf("file provider: update folder: %w", err)
	}
	if existing == nil {
		return fmt.Errorf("%w: %s", repo.ErrNotFound, f.URN.String())
	}

	entry := index.FolderEntry{
		URN:         f.URN.String(),
		ProjectURN:  f.ProjectURN.String(),
		Name:        f.Name,
		Description: f.Description,
		Deleted:     f.Deleted,
		CreatedAt:   existing.CreatedAt, // preserve original
		UpdatedAt:   f.UpdatedAt.UTC().Format(time.RFC3339),
	}
	return p.idx.UpsertFolder(entry)
}

func (p *Provider) DeleteFolder(ctx context.Context, urn string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	existing, err := p.idx.GetFolder(urn)
	if err != nil {
		return fmt.Errorf("file provider: delete folder: %w", err)
	}
	if existing == nil {
		return fmt.Errorf("%w: %s", repo.ErrNotFound, urn)
	}

	existing.Deleted = true
	existing.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	return p.idx.UpsertFolder(*existing)
}

// ─────────────────────────────────────────────────────────────────────────────
// Project / folder conversion helpers
// ─────────────────────────────────────────────────────────────────────────────

func projectEntryToCore(e *index.ProjectEntry) (*core.Project, error) {
	urn, err := core.ParseURN(e.URN)
	if err != nil {
		return nil, fmt.Errorf("file provider: parse project URN: %w", err)
	}
	createdAt, _ := time.Parse(time.RFC3339, e.CreatedAt)
	updatedAt, _ := time.Parse(time.RFC3339, e.UpdatedAt)
	return &core.Project{
		URN:         urn,
		Name:        e.Name,
		Description: e.Description,
		Deleted:     e.Deleted,
		CreatedAt:   createdAt,
		UpdatedAt:   updatedAt,
	}, nil
}

func folderEntryToCore(e *index.FolderEntry) (*core.Folder, error) {
	urn, err := core.ParseURN(e.URN)
	if err != nil {
		return nil, fmt.Errorf("file provider: parse folder URN: %w", err)
	}
	projURN, err := core.ParseURN(e.ProjectURN)
	if err != nil {
		return nil, fmt.Errorf("file provider: parse folder project URN: %w", err)
	}
	createdAt, _ := time.Parse(time.RFC3339, e.CreatedAt)
	updatedAt, _ := time.Parse(time.RFC3339, e.UpdatedAt)
	return &core.Folder{
		URN:         urn,
		ProjectURN:  projURN,
		Name:        e.Name,
		Description: e.Description,
		Deleted:     e.Deleted,
		CreatedAt:   createdAt,
		UpdatedAt:   updatedAt,
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Internal helpers — file paths
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) noteFilePath(urn string) string {
	return filepath.Join(p.notesDir, sanitiseURN(urn)+".notx")
}

func sanitiseURN(urn string) string {
	return strings.ReplaceAll(urn, ":", "_")
}

// ─────────────────────────────────────────────────────────────────────────────
// Internal helpers — .notx file writes
// ─────────────────────────────────────────────────────────────────────────────

// writeNotxStub writes the initial .notx file header for a newly created note.
func (p *Provider) writeNotxStub(note *core.Note) error {
	urn := note.URN.String()

	var sb strings.Builder
	sb.WriteString("# notx/1.0\n")
	fmt.Fprintf(&sb, "# note_urn:      %s\n", urn)
	fmt.Fprintf(&sb, "# note_type:     %s\n", note.NoteType.String())
	fmt.Fprintf(&sb, "# name:          %s\n", note.Name)
	fmt.Fprintf(&sb, "# created_at:    %s\n", note.CreatedAt.UTC().Format(time.RFC3339))
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

// writeEventToNotx appends a single event to the .notx file in lane format.
func (p *Provider) writeEventToNotx(noteURN string, event *core.Event) error {
	var buf bytes.Buffer

	fmt.Fprintf(&buf, "%d:%s:%s\n",
		event.Sequence,
		event.CreatedAt.UTC().Format(time.RFC3339),
		event.AuthorURN.String(),
	)
	buf.WriteString("->\n")

	for _, e := range event.Entries {
		switch e.Op {
		case core.LineOpDelete:
			fmt.Fprintf(&buf, "%d |-\n", e.LineNumber)
		case core.LineOpSetEmpty:
			fmt.Fprintf(&buf, "%d |\n", e.LineNumber)
		default:
			fmt.Fprintf(&buf, "%d | %s\n", e.LineNumber, e.Content)
		}
	}
	buf.WriteString("\n")

	notePath := p.noteFilePath(noteURN)
	f, err := os.OpenFile(notePath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("file provider: open notx for append %q: %w", notePath, err)
	}
	defer f.Close()

	if _, err := f.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("file provider: write event to notx %q: %w", notePath, err)
	}
	return nil
}

// updateNotxHeader rewrites just the # head_sequence line in the .notx header.
func (p *Provider) updateNotxHeader(noteURN string, headSequence int) error {
	notePath := p.noteFilePath(noteURN)

	data, err := os.ReadFile(notePath)
	if err != nil {
		return fmt.Errorf("file provider: read notx for header update %q: %w", notePath, err)
	}

	var out bytes.Buffer
	scanner := bufio.NewScanner(bytes.NewReader(data))
	updated := false

	for scanner.Scan() {
		line := scanner.Text()
		if !updated && strings.HasPrefix(line, "# head_sequence:") {
			fmt.Fprintf(&out, "# head_sequence: %d\n", headSequence)
			updated = true
		} else {
			out.WriteString(line)
			out.WriteByte('\n')
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("file provider: scan notx for header update %q: %w", notePath, err)
	}

	if err := os.WriteFile(notePath, out.Bytes(), 0o644); err != nil {
		return fmt.Errorf("file provider: write notx after header update %q: %w", notePath, err)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Internal helpers — content injection
// ─────────────────────────────────────────────────────────────────────────────

// ─────────────────────────────────────────────────────────────────────────────
// Conversion helpers
// ─────────────────────────────────────────────────────────────────────────────

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

// ─────────────────────────────────────────────────────────────────────────────
// Cleanup helpers (used by tests / migrations)
// ─────────────────────────────────────────────────────────────────────────────

// RemoveLegacyFiles deletes any .meta.json and .jsonl files left over from the
// previous provider implementation. Safe to call on a live provider; the files
// are no longer read or written.
func RemoveLegacyFiles(dataDir string) error {
	for _, subdir := range []string{
		filepath.Join(dataDir, notesSubdir),
		filepath.Join(dataDir, "events"),
	} {
		_ = filepath.WalkDir(subdir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			name := d.Name()
			if strings.HasSuffix(name, ".meta.json") || strings.HasSuffix(name, ".jsonl") {
				_ = os.Remove(path)
			}
			return nil
		})
	}
	return nil
}
