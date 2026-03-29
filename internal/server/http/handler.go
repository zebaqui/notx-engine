package http

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdlog "log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

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
	proj   repo.ProjectRepository
	dev    repo.DeviceRepository
	users  repo.UserRepository
	log    *slog.Logger
	mux    *http.ServeMux
	server *http.Server
}

// New creates a new Handler, registers all routes, and returns it.
// The caller must call Serve to start accepting connections.
func New(cfg *config.Config, r repo.NoteRepository, proj repo.ProjectRepository, dev repo.DeviceRepository, users repo.UserRepository, log *slog.Logger) *Handler {
	h := &Handler{
		cfg:   cfg,
		repo:  r,
		proj:  proj,
		dev:   dev,
		users: users,
		log:   log,
		mux:   http.NewServeMux(),
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
//
// If TLS is configured (TLSCertFile + TLSKeyFile) the listener is wrapped with
// a tls.Config that enforces TLS 1.3 as the minimum version. When mTLS is also
// configured (TLSCAFile) the server additionally requires and verifies a client
// certificate signed by that CA.
func (h *Handler) Serve() error {
	h.server = &http.Server{
		Addr:         h.cfg.HTTPAddr(),
		Handler:      h,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
		// Suppress the default "http: TLS handshake error" log lines that
		// http.Server emits for every rejected client connection. Those are
		// expected during mTLS enforcement and would pollute test output.
		ErrorLog: newDiscardLogger(),
	}

	ln, err := net.Listen("tcp", h.cfg.HTTPAddr())
	if err != nil {
		return fmt.Errorf("http: listen on %s: %w", h.cfg.HTTPAddr(), err)
	}

	if h.cfg.TLSEnabled() {
		tlsCfg, err := buildTLSConfig(h.cfg)
		if err != nil {
			return fmt.Errorf("http: build TLS config: %w", err)
		}
		ln = tls.NewListener(ln, tlsCfg)
		h.log.Info("http server listening",
			"addr", h.cfg.HTTPAddr(),
			"tls", true,
			"mtls", h.cfg.MTLSEnabled(),
		)
	} else {
		h.log.Info("http server listening", "addr", h.cfg.HTTPAddr())
	}

	if err := h.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http: serve: %w", err)
	}
	return nil
}

// newDiscardLogger returns a *log.Logger that silently discards all output.
// It is used as http.Server.ErrorLog to suppress expected TLS handshake errors
// (e.g. rejected client connections during mTLS enforcement) from polluting
// logs and test output.
func newDiscardLogger() *stdlog.Logger {
	return stdlog.New(io.Discard, "", 0)
}

// buildTLSConfig constructs a *tls.Config from the server configuration.
// It mirrors the logic in internal/server/grpc.buildTransportCredentials.
func buildTLSConfig(cfg *config.Config) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(cfg.TLSCertFile, cfg.TLSKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load server cert/key: %w", err)
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
	}

	if cfg.MTLSEnabled() {
		caPEM, err := os.ReadFile(cfg.TLSCAFile)
		if err != nil {
			return nil, fmt.Errorf("read CA cert %q: %w", cfg.TLSCAFile, err)
		}
		caPool := x509.NewCertPool()
		if !caPool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("parse CA cert %q: no valid PEM blocks found", cfg.TLSCAFile)
		}
		tlsCfg.ClientCAs = caPool
		tlsCfg.ClientAuth = tls.RequireAndVerifyClientCert
	}

	return tlsCfg, nil
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
	// ── Data routes — require an identified, approved, non-revoked device ────
	// Notes collection
	h.mux.HandleFunc("/v1/notes", h.withDeviceAuthMiddleware(h.routeNotes))
	// Single note
	h.mux.HandleFunc("/v1/notes/", h.withDeviceAuthMiddleware(h.routeNote))
	// Events sub-resource
	h.mux.HandleFunc("/v1/events", h.withDeviceAuthMiddleware(h.routeAppendEvent))
	// Search
	h.mux.HandleFunc("/v1/search", h.withDeviceAuthMiddleware(h.routeSearch))
	// Projects
	h.mux.HandleFunc("/v1/projects", h.withDeviceAuthMiddleware(h.routeProjects))
	h.mux.HandleFunc("/v1/projects/", h.withDeviceAuthMiddleware(h.routeProject))
	// Folders
	h.mux.HandleFunc("/v1/folders", h.withDeviceAuthMiddleware(h.routeFolders))
	h.mux.HandleFunc("/v1/folders/", h.withDeviceAuthMiddleware(h.routeFolder))

	// ── Device & user management — open (no device auth required) ───────────
	// Devices (registration is open so new devices can onboard themselves)
	h.mux.HandleFunc("/v1/devices", h.withMiddleware(h.routeDevices))
	h.mux.HandleFunc("/v1/devices/", h.withMiddleware(h.routeDevice))
	// Users
	h.mux.HandleFunc("/v1/users", h.withMiddleware(h.routeUsers))
	h.mux.HandleFunc("/v1/users/", h.withMiddleware(h.routeUser))
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

// ─────────────────────────────────────────────────────────────────────────────
// Projects
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) routeProjects(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleListProjects(w, r)
	case http.MethodPost:
		h.handleCreateProject(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) routeProject(w http.ResponseWriter, r *http.Request) {
	urn := strings.TrimPrefix(r.URL.Path, "/v1/projects/")
	if urn == "" {
		writeError(w, http.StatusBadRequest, "project URN is required")
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.handleGetProject(w, r, urn)
	case http.MethodPatch:
		h.handleUpdateProject(w, r, urn)
	case http.MethodDelete:
		h.handleDeleteProject(w, r, urn)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) routeFolders(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleListFolders(w, r)
	case http.MethodPost:
		h.handleCreateFolder(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) routeFolder(w http.ResponseWriter, r *http.Request) {
	urn := strings.TrimPrefix(r.URL.Path, "/v1/folders/")
	if urn == "" {
		writeError(w, http.StatusBadRequest, "folder URN is required")
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.handleGetFolder(w, r, urn)
	case http.MethodPatch:
		h.handleUpdateFolder(w, r, urn)
	case http.MethodDelete:
		h.handleDeleteFolder(w, r, urn)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

type projectJSON struct {
	URN         string `json:"urn"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Deleted     bool   `json:"deleted,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type createProjectRequest struct {
	URN         string `json:"urn"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type updateProjectRequest struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Deleted     *bool   `json:"deleted,omitempty"`
}

type listProjectsResponse struct {
	Projects      []*projectJSON `json:"projects"`
	NextPageToken string         `json:"next_page_token,omitempty"`
}

func projectToJSON(p *core.Project) *projectJSON {
	return &projectJSON{
		URN:         p.URN.String(),
		Name:        p.Name,
		Description: p.Description,
		Deleted:     p.Deleted,
		CreatedAt:   p.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:   p.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// GET /v1/projects
func (h *Handler) handleListProjects(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	includeDeleted := q.Get("include_deleted") == "true"
	pageSize := 0
	if ps := q.Get("page_size"); ps != "" {
		fmt.Sscanf(ps, "%d", &pageSize)
	}
	pageToken := q.Get("page_token")

	result, err := h.proj.ListProjects(r.Context(), repo.ProjectListOptions{
		IncludeDeleted: includeDeleted,
		PageSize:       pageSize,
		PageToken:      pageToken,
	})
	if err != nil {
		h.internalError(w, r, "list projects", err)
		return
	}

	out := make([]*projectJSON, 0, len(result.Projects))
	for _, p := range result.Projects {
		out = append(out, projectToJSON(p))
	}
	writeJSON(w, http.StatusOK, &listProjectsResponse{
		Projects:      out,
		NextPageToken: result.NextPageToken,
	})
}

// POST /v1/projects
func (h *Handler) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var req createProjectRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.URN == "" {
		writeError(w, http.StatusBadRequest, "urn is required")
		return
	}

	urn, err := core.ParseURN(req.URN)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid urn: %v", err))
		return
	}

	now := time.Now().UTC()
	proj := &core.Project{
		URN:         urn,
		Name:        req.Name,
		Description: req.Description,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := h.proj.CreateProject(r.Context(), proj); err != nil {
		if errors.Is(err, repo.ErrAlreadyExists) {
			writeError(w, http.StatusConflict, "project already exists")
			return
		}
		h.internalError(w, r, "create project", err)
		return
	}

	writeJSON(w, http.StatusCreated, projectToJSON(proj))
}

// GET /v1/projects/<urn>
func (h *Handler) handleGetProject(w http.ResponseWriter, r *http.Request, urn string) {
	proj, err := h.proj.GetProject(r.Context(), urn)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		h.internalError(w, r, "get project", err)
		return
	}
	writeJSON(w, http.StatusOK, projectToJSON(proj))
}

// PATCH /v1/projects/<urn>
func (h *Handler) handleUpdateProject(w http.ResponseWriter, r *http.Request, urn string) {
	var req updateProjectRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	proj, err := h.proj.GetProject(r.Context(), urn)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		h.internalError(w, r, "get project for update", err)
		return
	}

	if req.Name != nil {
		proj.Name = *req.Name
	}
	if req.Description != nil {
		proj.Description = *req.Description
	}
	if req.Deleted != nil {
		proj.Deleted = *req.Deleted
	}
	proj.UpdatedAt = time.Now().UTC()

	if err := h.proj.UpdateProject(r.Context(), proj); err != nil {
		h.internalError(w, r, "update project", err)
		return
	}
	writeJSON(w, http.StatusOK, projectToJSON(proj))
}

// DELETE /v1/projects/<urn>
func (h *Handler) handleDeleteProject(w http.ResponseWriter, r *http.Request, urn string) {
	if err := h.proj.DeleteProject(r.Context(), urn); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		h.internalError(w, r, "delete project", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// ─────────────────────────────────────────────────────────────────────────────
// Folders
// ─────────────────────────────────────────────────────────────────────────────

type folderJSON struct {
	URN         string `json:"urn"`
	ProjectURN  string `json:"project_urn"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Deleted     bool   `json:"deleted,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type createFolderRequest struct {
	URN         string `json:"urn"`
	ProjectURN  string `json:"project_urn"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type updateFolderRequest struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Deleted     *bool   `json:"deleted,omitempty"`
}

type listFoldersResponse struct {
	Folders       []*folderJSON `json:"folders"`
	NextPageToken string        `json:"next_page_token,omitempty"`
}

func folderToJSON(f *core.Folder) *folderJSON {
	return &folderJSON{
		URN:         f.URN.String(),
		ProjectURN:  f.ProjectURN.String(),
		Name:        f.Name,
		Description: f.Description,
		Deleted:     f.Deleted,
		CreatedAt:   f.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:   f.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// GET /v1/folders
func (h *Handler) handleListFolders(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	projectURN := q.Get("project_urn")
	includeDeleted := q.Get("include_deleted") == "true"
	pageSize := 0
	if ps := q.Get("page_size"); ps != "" {
		fmt.Sscanf(ps, "%d", &pageSize)
	}
	pageToken := q.Get("page_token")

	result, err := h.proj.ListFolders(r.Context(), repo.FolderListOptions{
		ProjectURN:     projectURN,
		IncludeDeleted: includeDeleted,
		PageSize:       pageSize,
		PageToken:      pageToken,
	})
	if err != nil {
		h.internalError(w, r, "list folders", err)
		return
	}

	out := make([]*folderJSON, 0, len(result.Folders))
	for _, f := range result.Folders {
		out = append(out, folderToJSON(f))
	}
	writeJSON(w, http.StatusOK, &listFoldersResponse{
		Folders:       out,
		NextPageToken: result.NextPageToken,
	})
}

// POST /v1/folders
func (h *Handler) handleCreateFolder(w http.ResponseWriter, r *http.Request) {
	var req createFolderRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.URN == "" {
		writeError(w, http.StatusBadRequest, "urn is required")
		return
	}
	if req.ProjectURN == "" {
		writeError(w, http.StatusBadRequest, "project_urn is required")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	urn, err := core.ParseURN(req.URN)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid urn: %v", err))
		return
	}
	projURN, err := core.ParseURN(req.ProjectURN)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid project_urn: %v", err))
		return
	}

	now := time.Now().UTC()
	f := &core.Folder{
		URN:         urn,
		ProjectURN:  projURN,
		Name:        req.Name,
		Description: req.Description,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := h.proj.CreateFolder(r.Context(), f); err != nil {
		if errors.Is(err, repo.ErrAlreadyExists) {
			writeError(w, http.StatusConflict, "folder already exists")
			return
		}
		h.internalError(w, r, "create folder", err)
		return
	}

	writeJSON(w, http.StatusCreated, folderToJSON(f))
}

// GET /v1/folders/<urn>
func (h *Handler) handleGetFolder(w http.ResponseWriter, r *http.Request, urn string) {
	f, err := h.proj.GetFolder(r.Context(), urn)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(w, http.StatusNotFound, "folder not found")
			return
		}
		h.internalError(w, r, "get folder", err)
		return
	}
	writeJSON(w, http.StatusOK, folderToJSON(f))
}

// PATCH /v1/folders/<urn>
func (h *Handler) handleUpdateFolder(w http.ResponseWriter, r *http.Request, urn string) {
	var req updateFolderRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	f, err := h.proj.GetFolder(r.Context(), urn)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(w, http.StatusNotFound, "folder not found")
			return
		}
		h.internalError(w, r, "get folder for update", err)
		return
	}

	if req.Name != nil {
		f.Name = *req.Name
	}
	if req.Description != nil {
		f.Description = *req.Description
	}
	if req.Deleted != nil {
		f.Deleted = *req.Deleted
	}
	f.UpdatedAt = time.Now().UTC()

	if err := h.proj.UpdateFolder(r.Context(), f); err != nil {
		h.internalError(w, r, "update folder", err)
		return
	}
	writeJSON(w, http.StatusOK, folderToJSON(f))
}

// DELETE /v1/folders/<urn>
func (h *Handler) handleDeleteFolder(w http.ResponseWriter, r *http.Request, urn string) {
	if err := h.proj.DeleteFolder(r.Context(), urn); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(w, http.StatusNotFound, "folder not found")
			return
		}
		h.internalError(w, r, "delete folder", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) internalError(w http.ResponseWriter, r *http.Request, op string, err error) {
	loggerFromCtx(r.Context(), h.log).Error("internal error", "op", op, "err", err)
	writeError(w, http.StatusInternalServerError, "internal server error")
}

// ─────────────────────────────────────────────────────────────────────────────
// Devices
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) routeDevices(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleListDevices(w, r)
	case http.MethodPost:
		h.handleRegisterDevice(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) routeDevice(w http.ResponseWriter, r *http.Request) {
	// Strip the /v1/devices/ prefix to get the remaining path segment(s).
	// After stripping, the layout is one of:
	//   <urn>                     → single device operations
	//   <urn>/<action>            → e.g. approve, reject, status
	//   <urn>/<action>/<sub>      → e.g. status/stream
	//
	// We split on the FIRST slash so the URN (which never contains a slash)
	// is always the left segment and everything to the right is the sub-path.
	trimmed := strings.TrimPrefix(r.URL.Path, "/v1/devices/")
	if trimmed == "" {
		writeError(w, http.StatusBadRequest, "device URN is required")
		return
	}

	// Check for action sub-resources.
	if idx := strings.Index(trimmed, "/"); idx != -1 {
		urn := trimmed[:idx]
		subPath := trimmed[idx+1:] // everything after the first slash
		if urn == "" {
			writeError(w, http.StatusBadRequest, "device URN is required")
			return
		}
		switch subPath {
		case "status":
			if r.Method != http.MethodGet {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			h.handleGetDeviceStatus(w, r, urn)
		case "status/stream":
			if r.Method != http.MethodGet {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			h.handleStreamDeviceStatus(w, r, urn)
		case "approve":
			if r.Method != http.MethodPatch {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			h.handleApproveDevice(w, r, urn)
		case "reject":
			if r.Method != http.MethodPatch {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			h.handleRejectDevice(w, r, urn)
		default:
			writeError(w, http.StatusNotFound, "unknown device action: "+subPath)
		}
		return
	}

	urn := trimmed
	switch r.Method {
	case http.MethodGet:
		h.handleGetDevice(w, r, urn)
	case http.MethodPatch:
		h.handleUpdateDevice(w, r, urn)
	case http.MethodDelete:
		h.handleRevokeDevice(w, r, urn)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

type deviceJSON struct {
	URN            string `json:"urn"`
	Name           string `json:"name"`
	OwnerURN       string `json:"owner_urn"`
	PublicKeyB64   string `json:"public_key_b64"`
	Role           string `json:"role"`
	ApprovalStatus string `json:"approval_status"`
	Revoked        bool   `json:"revoked,omitempty"`
	RegisteredAt   string `json:"registered_at"`
	LastSeenAt     string `json:"last_seen_at,omitempty"`
}

type registerDeviceRequest struct {
	URN             string `json:"urn"`
	Name            string `json:"name"`
	OwnerURN        string `json:"owner_urn"`
	PublicKeyB64    string `json:"public_key_b64"`
	AdminPassphrase string `json:"admin_passphrase,omitempty"`
}

type updateDeviceRequest struct {
	Name       *string `json:"name,omitempty"`
	LastSeenAt *string `json:"last_seen_at,omitempty"`
}

type listDevicesResponse struct {
	Devices []*deviceJSON `json:"devices"`
}

func deviceToJSON(d *core.Device) *deviceJSON {
	role := string(d.Role)
	if role == "" {
		role = string(core.DeviceRoleClient)
	}
	j := &deviceJSON{
		URN:            d.URN.String(),
		Name:           d.Name,
		OwnerURN:       d.OwnerURN.String(),
		PublicKeyB64:   d.PublicKeyB64,
		Role:           role,
		ApprovalStatus: string(d.ApprovalStatus),
		Revoked:        d.Revoked,
		RegisteredAt:   d.RegisteredAt.UTC().Format(time.RFC3339),
	}
	if !d.LastSeenAt.IsZero() {
		j.LastSeenAt = d.LastSeenAt.UTC().Format(time.RFC3339)
	}
	return j
}

// GET /v1/devices
func (h *Handler) handleListDevices(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ownerURN := q.Get("owner_urn")
	includeRevoked := q.Get("include_revoked") == "true"

	result, err := h.dev.ListDevices(r.Context(), repo.DeviceListOptions{
		OwnerURN:       ownerURN,
		IncludeRevoked: includeRevoked,
	})
	if err != nil {
		h.internalError(w, r, "list devices", err)
		return
	}

	out := make([]*deviceJSON, len(result.Devices))
	for i, d := range result.Devices {
		out[i] = deviceToJSON(d)
	}
	writeJSON(w, http.StatusOK, &listDevicesResponse{Devices: out})
}

// POST /v1/devices
//
// admin_passphrase (optional): when the server has an AdminPassphraseHash
// configured and the supplied plaintext matches, the device is registered
// with role=admin and approval_status=approved immediately, bypassing the
// normal approval flow. An incorrect passphrase is silently ignored and the
// device is registered as a normal client — no information about passphrase
// validity is leaked in the response.
func (h *Handler) handleRegisterDevice(w http.ResponseWriter, r *http.Request) {
	var req registerDeviceRequest
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
	if req.OwnerURN == "" {
		writeError(w, http.StatusBadRequest, "owner_urn is required")
		return
	}

	deviceURN, err := core.ParseURN(req.URN)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid device URN: "+err.Error())
		return
	}
	ownerURN, err := core.ParseURN(req.OwnerURN)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid owner_urn: "+err.Error())
		return
	}

	// Determine role and initial approval status.
	//
	// Admin path: a passphrase hash is configured AND the request supplies a
	// passphrase that matches it → role=admin, immediately approved.
	//
	// Client path (default): no passphrase, wrong passphrase, or no hash
	// configured → role=client, approval follows the onboarding config.
	role := core.DeviceRoleClient
	approvalStatus := core.DeviceApprovalPending
	if h.cfg.DeviceOnboarding.AutoApprove {
		approvalStatus = core.DeviceApprovalApproved
	}

	if h.cfg.Admin.AdminPassphraseHash != "" && req.AdminPassphrase != "" {
		if err := bcrypt.CompareHashAndPassword(
			[]byte(h.cfg.Admin.AdminPassphraseHash),
			[]byte(req.AdminPassphrase),
		); err == nil {
			role = core.DeviceRoleAdmin
			approvalStatus = core.DeviceApprovalApproved
		}
		// Wrong passphrase: fall through silently as a regular client.
		// We do not reveal whether the passphrase was wrong.
	}

	now := time.Now().UTC()
	d := &core.Device{
		URN:            deviceURN,
		Name:           req.Name,
		OwnerURN:       ownerURN,
		PublicKeyB64:   req.PublicKeyB64,
		Role:           role,
		ApprovalStatus: approvalStatus,
		RegisteredAt:   now,
	}

	if err := h.dev.RegisterDevice(r.Context(), d); err != nil {
		if errors.Is(err, repo.ErrAlreadyExists) {
			writeError(w, http.StatusConflict, "device already registered")
			return
		}
		h.internalError(w, r, "register device", err)
		return
	}

	log := loggerFromCtx(r.Context(), h.log)
	log.Info("device registered",
		"device_urn", d.URN.String(),
		"device_name", d.Name,
		"owner_urn", d.OwnerURN.String(),
		"role", string(d.Role),
		"approval_status", string(d.ApprovalStatus),
		"auto_approve", h.cfg.DeviceOnboarding.AutoApprove,
	)

	writeJSON(w, http.StatusCreated, deviceToJSON(d))
}

// GET /v1/devices/:urn
func (h *Handler) handleGetDevice(w http.ResponseWriter, r *http.Request, urn string) {
	d, err := h.dev.GetDevice(r.Context(), urn)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(w, http.StatusNotFound, "device not found")
			return
		}
		h.internalError(w, r, "get device", err)
		return
	}
	writeJSON(w, http.StatusOK, deviceToJSON(d))
}

// PATCH /v1/devices/:urn
func (h *Handler) handleUpdateDevice(w http.ResponseWriter, r *http.Request, urn string) {
	var req updateDeviceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	d, err := h.dev.GetDevice(r.Context(), urn)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(w, http.StatusNotFound, "device not found")
			return
		}
		h.internalError(w, r, "get device for update", err)
		return
	}

	if req.Name != nil {
		d.Name = *req.Name
	}
	if req.LastSeenAt != nil {
		ts, err := time.Parse(time.RFC3339, *req.LastSeenAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid last_seen_at: "+err.Error())
			return
		}
		d.LastSeenAt = ts.UTC()
	}

	if err := h.dev.UpdateDevice(r.Context(), d); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(w, http.StatusNotFound, "device not found")
			return
		}
		h.internalError(w, r, "update device", err)
		return
	}
	writeJSON(w, http.StatusOK, deviceToJSON(d))
}

// DELETE /v1/devices/:urn
func (h *Handler) handleRevokeDevice(w http.ResponseWriter, r *http.Request, urn string) {
	if err := h.dev.RevokeDevice(r.Context(), urn); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(w, http.StatusNotFound, "device not found")
			return
		}
		h.internalError(w, r, "revoke device", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"revoked": true})
}

// GET /v1/devices/:urn/status
//
// Returns a lightweight approval status summary for the device identified by
// :urn. This endpoint is intentionally open — it does NOT require an
// X-Device-ID header — so a freshly registered device can poll its own
// approval state before it has been granted data access.
type deviceStatusResponse struct {
	URN            string `json:"urn"`
	ApprovalStatus string `json:"approval_status"`
	Revoked        bool   `json:"revoked,omitempty"`
	// Approved is a convenience boolean derived from ApprovalStatus and Revoked.
	// true only when approval_status == "approved" AND revoked == false.
	Approved bool `json:"approved"`
}

func (h *Handler) handleGetDeviceStatus(w http.ResponseWriter, r *http.Request, urn string) {
	d, err := h.dev.GetDevice(r.Context(), urn)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(w, http.StatusNotFound, "device not found")
			return
		}
		h.internalError(w, r, "get device status", err)
		return
	}

	approved := d.ApprovalStatus == core.DeviceApprovalApproved && !d.Revoked
	writeJSON(w, http.StatusOK, &deviceStatusResponse{
		URN:            d.URN.String(),
		ApprovalStatus: string(d.ApprovalStatus),
		Revoked:        d.Revoked,
		Approved:       approved,
	})
}

// PATCH /v1/devices/:urn/approve
func (h *Handler) handleApproveDevice(w http.ResponseWriter, r *http.Request, urn string) {
	d, err := h.dev.GetDevice(r.Context(), urn)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(w, http.StatusNotFound, "device not found")
			return
		}
		h.internalError(w, r, "get device for approval", err)
		return
	}

	if d.Revoked {
		writeError(w, http.StatusConflict, "cannot approve a revoked device")
		return
	}
	if d.ApprovalStatus == core.DeviceApprovalRejected {
		writeError(w, http.StatusConflict, "cannot approve a rejected device; re-register the device instead")
		return
	}

	d.ApprovalStatus = core.DeviceApprovalApproved
	if err := h.dev.UpdateDevice(r.Context(), d); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(w, http.StatusNotFound, "device not found")
			return
		}
		h.internalError(w, r, "approve device", err)
		return
	}

	log := loggerFromCtx(r.Context(), h.log)
	log.Info("device approved", "device_urn", urn)
	writeJSON(w, http.StatusOK, deviceToJSON(d))
}

// PATCH /v1/devices/:urn/reject
func (h *Handler) handleRejectDevice(w http.ResponseWriter, r *http.Request, urn string) {
	d, err := h.dev.GetDevice(r.Context(), urn)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(w, http.StatusNotFound, "device not found")
			return
		}
		h.internalError(w, r, "get device for rejection", err)
		return
	}

	if d.Revoked {
		writeError(w, http.StatusConflict, "cannot reject a revoked device")
		return
	}

	d.ApprovalStatus = core.DeviceApprovalRejected
	if err := h.dev.UpdateDevice(r.Context(), d); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(w, http.StatusNotFound, "device not found")
			return
		}
		h.internalError(w, r, "reject device", err)
		return
	}

	log := loggerFromCtx(r.Context(), h.log)
	log.Info("device rejected", "device_urn", urn)
	writeJSON(w, http.StatusOK, deviceToJSON(d))
}

// ─────────────────────────────────────────────────────────────────────────────
// Users
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) routeUsers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleListUsers(w, r)
	case http.MethodPost:
		h.handleCreateUser(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) routeUser(w http.ResponseWriter, r *http.Request) {
	urn := strings.TrimPrefix(r.URL.Path, "/v1/users/")
	if urn == "" {
		writeError(w, http.StatusBadRequest, "user URN is required")
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.handleGetUser(w, r, urn)
	case http.MethodPatch:
		h.handleUpdateUser(w, r, urn)
	case http.MethodDelete:
		h.handleDeleteUser(w, r, urn)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

type userJSON struct {
	URN         string `json:"urn"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email,omitempty"`
	Deleted     bool   `json:"deleted,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type createUserRequest struct {
	URN         string `json:"urn"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email,omitempty"`
}

type updateUserRequest struct {
	DisplayName *string `json:"display_name,omitempty"`
	Email       *string `json:"email,omitempty"`
	Deleted     *bool   `json:"deleted,omitempty"`
}

type listUsersResponse struct {
	Users         []*userJSON `json:"users"`
	NextPageToken string      `json:"next_page_token,omitempty"`
}

func userToJSON(u *core.User) *userJSON {
	return &userJSON{
		URN:         u.URN.String(),
		DisplayName: u.DisplayName,
		Email:       u.Email,
		Deleted:     u.Deleted,
		CreatedAt:   u.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:   u.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// GET /v1/users
func (h *Handler) handleListUsers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	includeDeleted := q.Get("include_deleted") == "true"
	pageSize := 0
	if s := q.Get("page_size"); s != "" {
		fmt.Sscan(s, &pageSize)
	}
	pageToken := q.Get("page_token")

	result, err := h.users.ListUsers(r.Context(), repo.UserListOptions{
		IncludeDeleted: includeDeleted,
		PageSize:       pageSize,
		PageToken:      pageToken,
	})
	if err != nil {
		h.internalError(w, r, "list users", err)
		return
	}

	out := make([]*userJSON, len(result.Users))
	for i, u := range result.Users {
		out[i] = userToJSON(u)
	}
	writeJSON(w, http.StatusOK, &listUsersResponse{Users: out, NextPageToken: result.NextPageToken})
}

// POST /v1/users
func (h *Handler) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.URN == "" {
		writeError(w, http.StatusBadRequest, "urn is required")
		return
	}
	if req.DisplayName == "" {
		writeError(w, http.StatusBadRequest, "display_name is required")
		return
	}

	userURN, err := core.ParseURN(req.URN)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid URN: "+err.Error())
		return
	}
	if userURN.ObjectType != core.ObjectTypeUser {
		writeError(w, http.StatusBadRequest, "URN must be of type usr")
		return
	}

	now := time.Now().UTC()
	u := &core.User{
		URN:         userURN,
		DisplayName: req.DisplayName,
		Email:       req.Email,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := h.users.CreateUser(r.Context(), u); err != nil {
		if errors.Is(err, repo.ErrAlreadyExists) {
			writeError(w, http.StatusConflict, "user already exists")
			return
		}
		h.internalError(w, r, "create user", err)
		return
	}
	writeJSON(w, http.StatusCreated, userToJSON(u))
}

// GET /v1/users/:urn
func (h *Handler) handleGetUser(w http.ResponseWriter, r *http.Request, urn string) {
	u, err := h.users.GetUser(r.Context(), urn)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		h.internalError(w, r, "get user", err)
		return
	}
	writeJSON(w, http.StatusOK, userToJSON(u))
}

// PATCH /v1/users/:urn
func (h *Handler) handleUpdateUser(w http.ResponseWriter, r *http.Request, urn string) {
	var req updateUserRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	u, err := h.users.GetUser(r.Context(), urn)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		h.internalError(w, r, "get user for update", err)
		return
	}

	if req.DisplayName != nil {
		u.DisplayName = *req.DisplayName
	}
	if req.Email != nil {
		u.Email = *req.Email
	}
	if req.Deleted != nil {
		u.Deleted = *req.Deleted
	}
	u.UpdatedAt = time.Now().UTC()

	if err := h.users.UpdateUser(r.Context(), u); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		h.internalError(w, r, "update user", err)
		return
	}
	writeJSON(w, http.StatusOK, userToJSON(u))
}

// DELETE /v1/users/:urn
func (h *Handler) handleDeleteUser(w http.ResponseWriter, r *http.Request, urn string) {
	if err := h.users.DeleteUser(r.Context(), urn); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		h.internalError(w, r, "delete user", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}
