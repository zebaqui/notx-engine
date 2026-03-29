package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zebaqui/notx-engine/core"
	"github.com/zebaqui/notx-engine/internal/repo"
	"github.com/zebaqui/notx-engine/internal/server/config"
)

// ─────────────────────────────────────────────────────────────────────────────
// Handler — the HTTP layer
// ─────────────────────────────────────────────────────────────────────────────

// Handler wires all HTTP routes and holds the dependencies needed to serve them.
// It implements http.Handler so it can be passed directly to http.Server.
type Handler struct {
	cfg    *config.Config
	repo   repo.NoteRepository
	log    *slog.Logger
	mux    *http.ServeMux
	server *http.Server
}

// New creates a new Handler, registers all routes, and returns it.
// The caller must call Serve to start accepting connections.
func New(cfg *config.Config, r repo.NoteRepository, log *slog.Logger) *Handler {
	h := &Handler{
		cfg:  cfg,
		repo: r,
		log:  log,
		mux:  http.NewServeMux(),
	}
	h.routes()
	return h
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// Serve starts the HTTP server on the configured address. It blocks until
// the server shuts down. Call Shutdown to trigger a graceful stop.
func (h *Handler) Serve() error {
	h.server = &http.Server{
		Addr:         h.cfg.HTTPAddr(),
		Handler:      h,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	ln, err := net.Listen("tcp", h.cfg.HTTPAddr())
	if err != nil {
		return fmt.Errorf("http: listen on %s: %w", h.cfg.HTTPAddr(), err)
	}

	h.log.Info("http server listening", "addr", h.cfg.HTTPAddr())

	if err := h.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http: serve: %w", err)
	}
	return nil
}

// Shutdown gracefully drains in-flight requests and stops the server.
func (h *Handler) Shutdown(ctx context.Context) error {
	if h.server == nil {
		return nil
	}
	return h.server.Shutdown(ctx)
}

// ─────────────────────────────────────────────────────────────────────────────
// Route registration
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) routes() {
	// Notes collection
	h.mux.HandleFunc("/v1/notes", h.withMiddleware(h.routeNotes))
	// Single note
	h.mux.HandleFunc("/v1/notes/", h.withMiddleware(h.routeNote))
	// Events sub-resource
	h.mux.HandleFunc("/v1/events", h.withMiddleware(h.routeAppendEvent))
	// Search
	h.mux.HandleFunc("/v1/search", h.withMiddleware(h.routeSearch))
	// Health / readiness probes
	h.mux.HandleFunc("/healthz", h.handleHealthz)
	h.mux.HandleFunc("/readyz", h.handleReadyz)
}

// withMiddleware wraps a handler with logging and panic recovery.
func (h *Handler) withMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Recover from panics.
		defer func() {
			if rec := recover(); rec != nil {
				h.log.Error("http: panic recovered", "panic", rec, "path", r.URL.Path)
				writeError(w, http.StatusInternalServerError, "internal server error")
			}
		}()

		// Inject a request-scoped logger.
		rlog := h.log.With(
			"method", r.Method,
			"path", r.URL.Path,
			"remote", r.RemoteAddr,
		)
		r = r.WithContext(context.WithValue(r.Context(), ctxKeyLogger{}, rlog))

		next(w, r)

		rlog.Info("request handled",
			"status", w.Header().Get("X-Status"),
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}
}

type ctxKeyLogger struct{}

func loggerFromCtx(ctx context.Context, fallback *slog.Logger) *slog.Logger {
	if l, ok := ctx.Value(ctxKeyLogger{}).(*slog.Logger); ok {
		return l
	}
	return fallback
}

// ─────────────────────────────────────────────────────────────────────────────
// Route handlers
// ─────────────────────────────────────────────────────────────────────────────

// routeNotes dispatches GET /v1/notes and POST /v1/notes.
func (h *Handler) routeNotes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleListNotes(w, r)
	case http.MethodPost:
		h.handleCreateNote(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// routeNote dispatches requests to /v1/notes/<urn> and sub-paths.
func (h *Handler) routeNote(w http.ResponseWriter, r *http.Request) {
	// Path format: /v1/notes/<urn>[/events]
	path := strings.TrimPrefix(r.URL.Path, "/v1/notes/")
	if path == "" {
		writeError(w, http.StatusBadRequest, "note URN is required")
		return
	}

	// Check for /v1/notes/<urn>/events suffix (stream events).
	if strings.HasSuffix(path, "/events") {
		noteURN := strings.TrimSuffix(path, "/events")
		h.handleStreamEvents(w, r, noteURN)
		return
	}

	// Check for /v1/notes/<urn>/content suffix (replace full content).
	if strings.HasSuffix(path, "/content") {
		noteURN := strings.TrimSuffix(path, "/content")
		h.handleReplaceContent(w, r, noteURN)
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.handleGetNote(w, r, path)
	case http.MethodPatch:
		h.handleUpdateNote(w, r, path)
	case http.MethodDelete:
		h.handleDeleteNote(w, r, path)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// routeAppendEvent handles POST /v1/events.
func (h *Handler) routeAppendEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	h.handleAppendEvent(w, r)
}

// routeSearch handles GET /v1/search?q=...
func (h *Handler) routeSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	h.handleSearchNotes(w, r)
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /v1/notes
// ─────────────────────────────────────────────────────────────────────────────

type listNotesResponse struct {
	Notes         []*noteHeaderJSON `json:"notes"`
	NextPageToken string            `json:"next_page_token,omitempty"`
}

func (h *Handler) handleListNotes(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	opts := repo.ListOptions{
		ProjectURN:     q.Get("project_urn"),
		FolderURN:      q.Get("folder_urn"),
		PageToken:      q.Get("page_token"),
		IncludeDeleted: q.Get("include_deleted") == "true",
	}

	if ps := q.Get("page_size"); ps != "" {
		n, err := strconv.Atoi(ps)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "page_size must be a positive integer")
			return
		}
		if n > h.cfg.MaxPageSize {
			n = h.cfg.MaxPageSize
		}
		opts.PageSize = n
	} else {
		opts.PageSize = h.cfg.DefaultPageSize
	}

	if nt := q.Get("note_type"); nt != "" {
		parsed, err := core.ParseNoteType(nt)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid note_type: %v", err))
			return
		}
		opts.NoteTypeFilter = parsed
		opts.FilterByType = true
	}

	result, err := h.repo.List(r.Context(), opts)
	if err != nil {
		h.internalError(w, r, "list notes", err)
		return
	}

	resp := &listNotesResponse{
		Notes:         make([]*noteHeaderJSON, 0, len(result.Notes)),
		NextPageToken: result.NextPageToken,
	}
	for _, n := range result.Notes {
		resp.Notes = append(resp.Notes, noteToHeaderJSON(n))
	}

	writeJSON(w, http.StatusOK, resp)
}

// ─────────────────────────────────────────────────────────────────────────────
// POST /v1/notes
// ─────────────────────────────────────────────────────────────────────────────

type createNoteRequest struct {
	URN        string `json:"urn"`
	Name       string `json:"name"`
	NoteType   string `json:"note_type"`
	ProjectURN string `json:"project_urn,omitempty"`
	FolderURN  string `json:"folder_urn,omitempty"`
}

type createNoteResponse struct {
	Note *noteHeaderJSON `json:"note"`
}

func (h *Handler) handleCreateNote(w http.ResponseWriter, r *http.Request) {
	var req createNoteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.URN == "" {
		writeError(w, http.StatusBadRequest, "urn is required")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	noteURN, err := core.ParseURN(req.URN)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid urn: %v", err))
		return
	}

	noteType, err := core.ParseNoteType(req.NoteType)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid note_type: %v", err))
		return
	}

	note := core.NewNote(noteURN, req.Name, time.Now().UTC())
	note.NoteType = noteType

	if req.ProjectURN != "" {
		projURN, err := core.ParseURN(req.ProjectURN)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid project_urn: %v", err))
			return
		}
		note.ProjectURN = &projURN
	}
	if req.FolderURN != "" {
		folderURN, err := core.ParseURN(req.FolderURN)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid folder_urn: %v", err))
			return
		}
		note.FolderURN = &folderURN
	}

	if err := h.repo.Create(r.Context(), note); err != nil {
		if errors.Is(err, repo.ErrAlreadyExists) {
			writeError(w, http.StatusConflict, "note with this URN already exists")
			return
		}
		h.internalError(w, r, "create note", err)
		return
	}

	writeJSON(w, http.StatusCreated, &createNoteResponse{Note: noteToHeaderJSON(note)})
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /v1/notes/:urn
// ─────────────────────────────────────────────────────────────────────────────

type getNoteResponse struct {
	Header  *noteHeaderJSON `json:"header"`
	Content string          `json:"content"`
}

func (h *Handler) handleGetNote(w http.ResponseWriter, r *http.Request, urn string) {
	note, err := h.repo.Get(r.Context(), urn)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(w, http.StatusNotFound, "note not found")
			return
		}
		h.internalError(w, r, "get note", err)
		return
	}

	resp := &getNoteResponse{
		Header:  noteToHeaderJSON(note),
		Content: note.Content(),
	}

	writeJSON(w, http.StatusOK, resp)
}

// ─────────────────────────────────────────────────────────────────────────────
// PATCH /v1/notes/:urn
// ─────────────────────────────────────────────────────────────────────────────

type updateNoteRequest struct {
	Name       *string `json:"name,omitempty"`
	ProjectURN *string `json:"project_urn,omitempty"`
	FolderURN  *string `json:"folder_urn,omitempty"`
	Deleted    *bool   `json:"deleted,omitempty"`
}

func (h *Handler) handleUpdateNote(w http.ResponseWriter, r *http.Request, urn string) {
	var req updateNoteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	note, err := h.repo.Get(r.Context(), urn)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(w, http.StatusNotFound, "note not found")
			return
		}
		h.internalError(w, r, "get note for update", err)
		return
	}

	if req.Name != nil {
		if *req.Name == "" {
			writeError(w, http.StatusBadRequest, "name must not be empty")
			return
		}
		note.Name = *req.Name
	}
	if req.ProjectURN != nil {
		if *req.ProjectURN == "" {
			note.ProjectURN = nil
		} else {
			projURN, err := core.ParseURN(*req.ProjectURN)
			if err != nil {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid project_urn: %v", err))
				return
			}
			note.ProjectURN = &projURN
		}
	}
	if req.FolderURN != nil {
		if *req.FolderURN == "" {
			note.FolderURN = nil
		} else {
			folderURN, err := core.ParseURN(*req.FolderURN)
			if err != nil {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid folder_urn: %v", err))
				return
			}
			note.FolderURN = &folderURN
		}
	}
	if req.Deleted != nil {
		note.Deleted = *req.Deleted
	}

	if err := h.repo.Update(r.Context(), note); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(w, http.StatusNotFound, "note not found")
			return
		}
		if errors.Is(err, repo.ErrNoteTypeImmutable) {
			writeError(w, http.StatusBadRequest, "note_type is immutable after creation")
			return
		}
		h.internalError(w, r, "update note", err)
		return
	}

	writeJSON(w, http.StatusOK, &createNoteResponse{Note: noteToHeaderJSON(note)})
}

// ─────────────────────────────────────────────────────────────────────────────
// DELETE /v1/notes/:urn
// ─────────────────────────────────────────────────────────────────────────────

type deleteNoteResponse struct {
	Deleted bool `json:"deleted"`
}

func (h *Handler) handleDeleteNote(w http.ResponseWriter, r *http.Request, urn string) {
	if err := h.repo.Delete(r.Context(), urn); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(w, http.StatusNotFound, "note not found")
			return
		}
		h.internalError(w, r, "delete note", err)
		return
	}
	writeJSON(w, http.StatusOK, &deleteNoteResponse{Deleted: true})
}

// ─────────────────────────────────────────────────────────────────────────────
// POST /v1/events
// ─────────────────────────────────────────────────────────────────────────────

type appendEventRequest struct {
	NoteURN        string          `json:"note_urn"`
	Sequence       int             `json:"sequence"`
	AuthorURN      string          `json:"author_urn"`
	CreatedAt      string          `json:"created_at,omitempty"`
	Entries        []lineEntryJSON `json:"entries,omitempty"`
	ExpectSequence int             `json:"expect_sequence,omitempty"`
}

type appendEventResponse struct {
	Sequence int `json:"sequence"`
}

func (h *Handler) handleAppendEvent(w http.ResponseWriter, r *http.Request) {
	var req appendEventRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.NoteURN == "" {
		writeError(w, http.StatusBadRequest, "note_urn is required")
		return
	}
	if req.Sequence < 1 {
		writeError(w, http.StatusBadRequest, "sequence must be >= 1")
		return
	}
	if req.AuthorURN == "" {
		writeError(w, http.StatusBadRequest, "author_urn is required")
		return
	}
	if len(req.Entries) == 0 {
		writeError(w, http.StatusBadRequest, "entries must not be empty")
		return
	}

	noteURN, err := core.ParseURN(req.NoteURN)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid note_urn: %v", err))
		return
	}
	authorURN, err := core.ParseURN(req.AuthorURN)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid author_urn: %v", err))
		return
	}

	createdAt := time.Now().UTC()
	if req.CreatedAt != "" {
		t, err := time.Parse(time.RFC3339, req.CreatedAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid created_at: %v", err))
			return
		}
		createdAt = t.UTC()
	}

	entries := make([]core.LineEntry, 0, len(req.Entries))
	for _, e := range req.Entries {
		entry, err := lineEntryFromJSON(e)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid line entry: %v", err))
			return
		}
		entries = append(entries, entry)
	}

	event := &core.Event{
		NoteURN:   noteURN,
		Sequence:  req.Sequence,
		AuthorURN: authorURN,
		CreatedAt: createdAt,
		Entries:   entries,
	}

	opts := repo.AppendEventOptions{
		ExpectSequence: req.ExpectSequence,
	}

	if err := h.repo.AppendEvent(r.Context(), event, opts); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(w, http.StatusNotFound, "note not found")
			return
		}
		if errors.Is(err, repo.ErrSequenceConflict) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		h.internalError(w, r, "append event", err)
		return
	}

	writeJSON(w, http.StatusCreated, &appendEventResponse{Sequence: req.Sequence})
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /v1/notes/:urn/events?from=<seq>
// ─────────────────────────────────────────────────────────────────────────────
// POST /v1/notes/:urn/content
// ─────────────────────────────────────────────────────────────────────────────

type replaceContentRequest struct {
	Content   string `json:"content"`
	AuthorURN string `json:"author_urn,omitempty"`
}

type replaceContentResponse struct {
	Sequence int    `json:"sequence"`
	Changed  bool   `json:"changed"`
	NoteURN  string `json:"note_urn"`
}

func (h *Handler) handleReplaceContent(w http.ResponseWriter, r *http.Request, noteURN string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req replaceContentRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	note, err := h.repo.Get(r.Context(), noteURN)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(w, http.StatusNotFound, "note not found")
			return
		}
		h.internalError(w, r, "get note for content replace", err)
		return
	}

	if note.NoteType == core.NoteTypeSecure {
		writeError(w, http.StatusBadRequest, "content replacement is not supported for secure notes")
		return
	}

	// Diff the incoming content against the current document state.
	oldLines := core.SplitLines(note.Content())
	newLines := core.SplitLines(req.Content)
	entries := core.DiffLines(oldLines, newLines)

	// Nothing changed — return early without writing an event.
	if len(entries) == 0 {
		writeJSON(w, http.StatusOK, &replaceContentResponse{
			Sequence: note.HeadSequence(),
			Changed:  false,
			NoteURN:  noteURN,
		})
		return
	}

	// Resolve author URN (default to anon).
	authorURNStr := fmt.Sprintf("%s:usr:anon", note.URN.Namespace)
	if req.AuthorURN != "" {
		authorURNStr = req.AuthorURN
	}
	authorURN, err := core.ParseURN(authorURNStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid author_urn: %v", err))
		return
	}

	nextSeq := note.HeadSequence() + 1
	event := &core.Event{
		NoteURN:   note.URN,
		Sequence:  nextSeq,
		AuthorURN: authorURN,
		CreatedAt: time.Now().UTC(),
		Entries:   entries,
	}

	opts := repo.AppendEventOptions{
		ExpectSequence: nextSeq,
	}

	if err := h.repo.AppendEvent(r.Context(), event, opts); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(w, http.StatusNotFound, "note not found")
			return
		}
		if errors.Is(err, repo.ErrSequenceConflict) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		h.internalError(w, r, "append content event", err)
		return
	}

	writeJSON(w, http.StatusCreated, &replaceContentResponse{
		Sequence: nextSeq,
		Changed:  true,
		NoteURN:  noteURN,
	})
}

// ─────────────────────────────────────────────────────────────────────────────

type streamEventsResponse struct {
	NoteURN string       `json:"note_urn"`
	Events  []*eventJSON `json:"events"`
	Count   int          `json:"count"`
}

func (h *Handler) handleStreamEvents(w http.ResponseWriter, r *http.Request, noteURN string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	fromSeq := 1
	if fs := r.URL.Query().Get("from"); fs != "" {
		n, err := strconv.Atoi(fs)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "from must be a positive integer")
			return
		}
		fromSeq = n
	}

	events, err := h.repo.Events(r.Context(), noteURN, fromSeq)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(w, http.StatusNotFound, "note not found")
			return
		}
		h.internalError(w, r, "list events", err)
		return
	}

	resp := &streamEventsResponse{
		NoteURN: noteURN,
		Events:  make([]*eventJSON, 0, len(events)),
		Count:   len(events),
	}
	for _, ev := range events {
		resp.Events = append(resp.Events, eventToJSON(ev))
	}

	writeJSON(w, http.StatusOK, resp)
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /v1/search?q=...
// ─────────────────────────────────────────────────────────────────────────────

type searchNotesResponse struct {
	Results       []*searchResultJSON `json:"results"`
	NextPageToken string              `json:"next_page_token,omitempty"`
}

type searchResultJSON struct {
	Note    *noteHeaderJSON `json:"note"`
	Excerpt string          `json:"excerpt,omitempty"`
}

func (h *Handler) handleSearchNotes(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	query := q.Get("q")
	if query == "" {
		writeError(w, http.StatusBadRequest, "q (query) parameter is required")
		return
	}

	pageSize := h.cfg.DefaultPageSize
	if ps := q.Get("page_size"); ps != "" {
		n, err := strconv.Atoi(ps)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "page_size must be a positive integer")
			return
		}
		if n > h.cfg.MaxPageSize {
			n = h.cfg.MaxPageSize
		}
		pageSize = n
	}

	opts := repo.SearchOptions{
		Query:     query,
		PageSize:  pageSize,
		PageToken: q.Get("page_token"),
	}

	results, err := h.repo.Search(r.Context(), opts)
	if err != nil {
		h.internalError(w, r, "search notes", err)
		return
	}

	resp := &searchNotesResponse{
		Results:       make([]*searchResultJSON, 0, len(results.Results)),
		NextPageToken: results.NextPageToken,
	}
	for _, sr := range results.Results {
		resp.Results = append(resp.Results, &searchResultJSON{
			Note:    noteToHeaderJSON(sr.Note),
			Excerpt: sr.Excerpt,
		})
	}

	writeJSON(w, http.StatusOK, resp)
}

// ─────────────────────────────────────────────────────────────────────────────
// Health probes
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleReadyz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// ─────────────────────────────────────────────────────────────────────────────
// JSON wire types
// ─────────────────────────────────────────────────────────────────────────────

type noteHeaderJSON struct {
	URN        string `json:"urn"`
	Name       string `json:"name"`
	NoteType   string `json:"note_type"`
	ProjectURN string `json:"project_urn,omitempty"`
	FolderURN  string `json:"folder_urn,omitempty"`
	Deleted    bool   `json:"deleted"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

type eventJSON struct {
	URN       string          `json:"urn,omitempty"`
	NoteURN   string          `json:"note_urn"`
	Sequence  int             `json:"sequence"`
	AuthorURN string          `json:"author_urn"`
	CreatedAt string          `json:"created_at"`
	Entries   []lineEntryJSON `json:"entries,omitempty"`
}

type lineEntryJSON struct {
	// Op: "set" | "set_empty" | "delete"
	Op         string `json:"op"`
	LineNumber int    `json:"line_number"`
	Content    string `json:"content,omitempty"`
}

func noteToHeaderJSON(n *core.Note) *noteHeaderJSON {
	h := &noteHeaderJSON{
		URN:       n.URN.String(),
		Name:      n.Name,
		NoteType:  n.NoteType.String(),
		Deleted:   n.Deleted,
		CreatedAt: n.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: n.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if n.ProjectURN != nil {
		h.ProjectURN = n.ProjectURN.String()
	}
	if n.FolderURN != nil {
		h.FolderURN = n.FolderURN.String()
	}
	return h
}

func eventToJSON(ev *core.Event) *eventJSON {
	j := &eventJSON{
		NoteURN:   ev.NoteURN.String(),
		Sequence:  ev.Sequence,
		AuthorURN: ev.AuthorURN.String(),
		CreatedAt: ev.CreatedAt.UTC().Format(time.RFC3339),
		Entries:   make([]lineEntryJSON, 0, len(ev.Entries)),
	}
	if !ev.URN.Equal(core.URN{}) {
		j.URN = ev.URN.String()
	}
	for _, e := range ev.Entries {
		j.Entries = append(j.Entries, lineEntryToJSON(e))
	}
	return j
}

func lineEntryToJSON(e core.LineEntry) lineEntryJSON {
	j := lineEntryJSON{
		LineNumber: e.LineNumber,
		Content:    e.Content,
	}
	switch e.Op {
	case core.LineOpSetEmpty:
		j.Op = "set_empty"
	case core.LineOpDelete:
		j.Op = "delete"
	default:
		j.Op = "set"
	}
	return j
}

func lineEntryFromJSON(j lineEntryJSON) (core.LineEntry, error) {
	if j.LineNumber < 1 {
		return core.LineEntry{}, fmt.Errorf("line_number must be >= 1")
	}
	var op core.LineOp
	switch j.Op {
	case "set", "":
		op = core.LineOpSet
	case "set_empty":
		op = core.LineOpSetEmpty
	case "delete":
		op = core.LineOpDelete
	default:
		return core.LineEntry{}, fmt.Errorf("unknown op %q: must be set, set_empty, or delete", j.Op)
	}
	return core.LineEntry{
		LineNumber: j.LineNumber,
		Op:         op,
		Content:    j.Content,
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// JSON helpers
// ─────────────────────────────────────────────────────────────────────────────

type errorResponse struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Status", strconv.Itoa(status))
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, &errorResponse{Error: msg})
}

func decodeJSON(r *http.Request, v any) error {
	if r.Body == nil {
		return fmt.Errorf("request body is required")
	}
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	return nil
}

func (h *Handler) internalError(w http.ResponseWriter, r *http.Request, op string, err error) {
	loggerFromCtx(r.Context(), h.log).Error("internal error", "op", op, "err", err)
	writeError(w, http.StatusInternalServerError, "internal server error")
}
