package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/zebaqui/notx-engine/core"
	"github.com/zebaqui/notx-engine/repo"
	"github.com/zebaqui/notx-engine/snip"
)

// ─────────────────────────────────────────────────────────────────────────────
// NoteService interface
// ─────────────────────────────────────────────────────────────────────────────

// NoteService defines the business-logic contract for note operations.
// All methods operate on core.* / repo.* types and return plain Go errors.
// Transport layers (gRPC, HTTP) are responsible for converting between their
// wire formats and these types.
type NoteService interface {
	// Get returns the note and its full event history starting at sequence 1.
	Get(ctx context.Context, urn string) (*core.Note, []*core.Event, error)

	// List returns a paginated list of note headers. Pagination defaults are
	// applied when opts.PageSize is zero.
	List(ctx context.Context, opts repo.ListOptions) (*repo.ListResult, error)

	// ListSnips returns a paginated list of snip headers.
	ListSnips(ctx context.Context, opts repo.ListSnipsOptions) (*repo.ListResult, error)

	// Create persists a new note. note.URN and note.Name must be set.
	Create(ctx context.Context, note *core.Note) error

	// Update applies partial mutations to an existing note and persists them.
	// Only the non-zero / non-nil fields of upd are applied.
	Update(ctx context.Context, urn string, upd NoteUpdate) (*core.Note, error)

	// Delete soft-deletes the note identified by urn.
	Delete(ctx context.Context, urn string) error

	// AppendEvent appends a new event to the note's history.
	AppendEvent(ctx context.Context, event *core.Event, opts repo.AppendEventOptions) error

	// Events returns all events for the note starting at fromSequence (1-based).
	Events(ctx context.Context, noteURN string, fromSequence int) ([]*core.Event, error)

	// Search performs a full-text search over notes.
	Search(ctx context.Context, opts repo.SearchOptions) (*repo.SearchResults, error)

	// ReplaceContent atomically replaces the entire text content of a normal
	// note by computing a line diff and appending a single event.
	ReplaceContent(ctx context.Context, in ReplaceContentInput) (ReplaceContentResult, error)

	// SetSnipRegistry wires a snip plugin registry so that plugin hooks are
	// dispatched after note create and event append writes.
	SetSnipRegistry(r *snip.Registry)
}

// ─────────────────────────────────────────────────────────────────────────────
// Input / output types
// ─────────────────────────────────────────────────────────────────────────────

// NoteUpdate carries the optional fields to change on an existing note.
// Zero values are treated as "no change".
type NoteUpdate struct {
	// Name, when non-empty, replaces the note's current name.
	Name string

	// ClearProjectURN removes the project association when true.
	// Takes precedence over SetProjectURN.
	ClearProjectURN bool

	// SetProjectURN, when non-empty, sets the note's project.
	// Ignored when ClearProjectURN is true.
	SetProjectURN string

	// ClearFolderURN removes the folder association when true.
	// Takes precedence over SetFolderURN.
	ClearFolderURN bool

	// SetFolderURN, when non-empty, sets the note's folder.
	// Ignored when ClearFolderURN is true.
	SetFolderURN string

	// Deleted, when non-nil, explicitly sets the note's soft-delete flag.
	Deleted *bool
}

// ReplaceContentInput is the input for ReplaceContent.
type ReplaceContentInput struct {
	// NoteURN is the note whose content will be replaced. Required.
	NoteURN string

	// Content is the desired full new content of the note.
	Content string

	// AuthorURN, when non-empty, is attributed to the generated event.
	// Defaults to the anonymous sentinel when empty.
	AuthorURN string
}

// ReplaceContentResult is the outcome of a successful ReplaceContent call.
type ReplaceContentResult struct {
	NoteURN  string
	Sequence int
	Changed  bool
}

// ─────────────────────────────────────────────────────────────────────────────
// noteService — concrete implementation
// ─────────────────────────────────────────────────────────────────────────────

type noteService struct {
	repo        repo.NoteRepository
	contextRepo repo.ContextRepository // optional; nil disables project backfill
	registry    *snip.Registry         // optional; nil disables plugin hooks
	defaultPage int
	maxPage     int
}

func newNoteService(r repo.NoteRepository, contextRepo repo.ContextRepository, defaultPage, maxPage int) *noteService {
	dp, mx := resolvePageDefaults(defaultPage, maxPage)
	return &noteService{
		repo:        r,
		contextRepo: contextRepo,
		defaultPage: dp,
		maxPage:     mx,
	}
}

func (s *noteService) SetSnipRegistry(r *snip.Registry) {
	s.registry = r
}

// ── Get ───────────────────────────────────────────────────────────────────────

func (s *noteService) Get(ctx context.Context, urn string) (*core.Note, []*core.Event, error) {
	if urn == "" {
		return nil, nil, fmt.Errorf("%w: urn is required", ErrInvalidInput)
	}

	note, err := s.repo.Get(ctx, urn)
	if err != nil {
		return nil, nil, err
	}

	events, err := s.repo.Events(ctx, urn, 1)
	if err != nil {
		return nil, nil, err
	}

	return note, events, nil
}

// ── List ──────────────────────────────────────────────────────────────────────

func (s *noteService) List(ctx context.Context, opts repo.ListOptions) (*repo.ListResult, error) {
	opts.PageSize = clampPageSize(opts.PageSize, s.defaultPage, s.maxPage)
	return s.repo.List(ctx, opts)
}

// ── ListSnips ─────────────────────────────────────────────────────────────────

func (s *noteService) ListSnips(ctx context.Context, opts repo.ListSnipsOptions) (*repo.ListResult, error) {
	opts.PageSize = clampPageSize(opts.PageSize, s.defaultPage, s.maxPage)
	return s.repo.ListSnips(ctx, opts)
}

// ── Create ────────────────────────────────────────────────────────────────────

func (s *noteService) Create(ctx context.Context, note *core.Note) error {
	if note == nil {
		return fmt.Errorf("%w: note is required", ErrInvalidInput)
	}
	if note.URN == (core.URN{}) {
		return fmt.Errorf("%w: note.URN is required", ErrInvalidInput)
	}
	if note.Name == "" {
		return fmt.Errorf("%w: note.Name is required", ErrInvalidInput)
	}

	if err := s.repo.Create(ctx, note); err != nil {
		return err
	}

	s.dispatchSnipHook(ctx, note, nil)
	return nil
}

// ── Update ────────────────────────────────────────────────────────────────────

func (s *noteService) Update(ctx context.Context, urn string, upd NoteUpdate) (*core.Note, error) {
	if urn == "" {
		return nil, fmt.Errorf("%w: urn is required", ErrInvalidInput)
	}

	note, err := s.repo.Get(ctx, urn)
	if err != nil {
		return nil, err
	}

	// Capture the old project URN so we can detect project changes after save.
	oldProjectURN := ""
	if note.ProjectURN != nil {
		oldProjectURN = note.ProjectURN.String()
	}

	if upd.Name != "" {
		note.Name = upd.Name
	}

	switch {
	case upd.ClearProjectURN:
		note.ProjectURN = nil
	case upd.SetProjectURN != "":
		projURN, err := core.ParseURN(upd.SetProjectURN)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid project_urn: %v", ErrInvalidInput, err)
		}
		note.ProjectURN = &projURN
	}

	switch {
	case upd.ClearFolderURN:
		note.FolderURN = nil
	case upd.SetFolderURN != "":
		folderURN, err := core.ParseURN(upd.SetFolderURN)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid folder_urn: %v", ErrInvalidInput, err)
		}
		note.FolderURN = &folderURN
	}

	if upd.Deleted != nil {
		note.Deleted = *upd.Deleted
	}

	note.UpdatedAt = time.Now().UTC()

	if err := s.repo.Update(ctx, note); err != nil {
		return nil, err
	}

	// When the project changes, backfill context bursts asynchronously so the
	// caller is not blocked.
	newProjectURN := ""
	if note.ProjectURN != nil {
		newProjectURN = note.ProjectURN.String()
	}
	if s.contextRepo != nil && newProjectURN != "" && newProjectURN != oldProjectURN {
		go func(noteURN, projectURN string) {
			bgCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			_, _ = s.contextRepo.IndexNoteIntoProject(bgCtx, noteURN, projectURN)
		}(urn, newProjectURN)
	}

	return note, nil
}

// ── Delete ────────────────────────────────────────────────────────────────────

func (s *noteService) Delete(ctx context.Context, urn string) error {
	if urn == "" {
		return fmt.Errorf("%w: urn is required", ErrInvalidInput)
	}
	return s.repo.Delete(ctx, urn)
}

// ── AppendEvent ───────────────────────────────────────────────────────────────

func (s *noteService) AppendEvent(ctx context.Context, event *core.Event, opts repo.AppendEventOptions) error {
	if event == nil {
		return fmt.Errorf("%w: event is required", ErrInvalidInput)
	}
	if event.NoteURN == (core.URN{}) {
		return fmt.Errorf("%w: event.NoteURN is required", ErrInvalidInput)
	}
	if event.Sequence < 1 {
		return fmt.Errorf("%w: event.Sequence must be >= 1", ErrInvalidInput)
	}
	if event.AuthorURN == (core.URN{}) {
		return fmt.Errorf("%w: event.AuthorURN is required", ErrInvalidInput)
	}

	if err := s.repo.AppendEvent(ctx, event, opts); err != nil {
		return err
	}

	// Dispatch plugin hook after a successful write.
	if s.registry != nil {
		if note, err := s.repo.Get(ctx, event.NoteURN.String()); err == nil {
			s.dispatchSnipHook(ctx, note, event)
		}
	}

	return nil
}

// ── Events ────────────────────────────────────────────────────────────────────

func (s *noteService) Events(ctx context.Context, noteURN string, fromSequence int) ([]*core.Event, error) {
	if noteURN == "" {
		return nil, fmt.Errorf("%w: noteURN is required", ErrInvalidInput)
	}
	if fromSequence < 1 {
		fromSequence = 1
	}
	return s.repo.Events(ctx, noteURN, fromSequence)
}

// ── Search ────────────────────────────────────────────────────────────────────

func (s *noteService) Search(ctx context.Context, opts repo.SearchOptions) (*repo.SearchResults, error) {
	if strings.TrimSpace(opts.Query) == "" {
		return nil, fmt.Errorf("%w: query is required", ErrInvalidInput)
	}
	opts.PageSize = clampPageSize(opts.PageSize, s.defaultPage, s.maxPage)
	return s.repo.Search(ctx, opts)
}

// ── ReplaceContent ────────────────────────────────────────────────────────────

func (s *noteService) ReplaceContent(ctx context.Context, in ReplaceContentInput) (ReplaceContentResult, error) {
	if in.NoteURN == "" {
		return ReplaceContentResult{}, fmt.Errorf("%w: NoteURN is required", ErrInvalidInput)
	}

	note, err := s.repo.Get(ctx, in.NoteURN)
	if err != nil {
		return ReplaceContentResult{}, err
	}

	if note.NoteType == core.NoteTypeSecure {
		return ReplaceContentResult{}, fmt.Errorf(
			"%w: ReplaceContent is not permitted on secure notes", ErrInvalidInput)
	}

	oldLines := core.SplitLines(note.Content())
	newLines := core.SplitLines(in.Content)
	entries := core.DiffLines(oldLines, newLines)

	if len(entries) == 0 {
		return ReplaceContentResult{
			NoteURN:  in.NoteURN,
			Sequence: note.HeadSequence(),
			Changed:  false,
		}, nil
	}

	noteURN, err := core.ParseURN(in.NoteURN)
	if err != nil {
		return ReplaceContentResult{}, fmt.Errorf("%w: invalid NoteURN: %v", ErrInvalidInput, err)
	}

	var authorURN core.URN
	if in.AuthorURN != "" {
		authorURN, err = core.ParseURN(in.AuthorURN)
		if err != nil {
			return ReplaceContentResult{}, fmt.Errorf("%w: invalid AuthorURN: %v", ErrInvalidInput, err)
		}
	} else {
		authorURN = core.AnonURN()
	}

	nextSeq := note.HeadSequence() + 1
	ev := &core.Event{
		URN:       core.NewURN(core.ObjectTypeEvent),
		NoteURN:   noteURN,
		Sequence:  nextSeq,
		AuthorURN: authorURN,
		CreatedAt: time.Now().UTC(),
		Entries:   entries,
	}

	if err := s.repo.AppendEvent(ctx, ev, repo.AppendEventOptions{ExpectSequence: nextSeq}); err != nil {
		return ReplaceContentResult{}, err
	}

	return ReplaceContentResult{
		NoteURN:  in.NoteURN,
		Sequence: nextSeq,
		Changed:  true,
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Internal helpers
// ─────────────────────────────────────────────────────────────────────────────

// dispatchSnipHook calls the registered plugin hook after a note write.
// event is nil for Create hooks and non-nil for AppendEvent hooks.
// Hook errors are silently discarded — plugin indexing is best-effort and
// must never surface to callers.
func (s *noteService) dispatchSnipHook(ctx context.Context, note *core.Note, event *core.Event) {
	if s.registry == nil || note.SnipType == nil {
		return
	}
	plugin, ok := s.registry.Get(*note.SnipType)
	if !ok {
		return
	}
	var err error
	if event == nil {
		err = plugin.OnNoteCreated(ctx, note)
	} else {
		err = plugin.OnEventAppended(ctx, note, event)
	}
	_ = err // non-fatal
}
