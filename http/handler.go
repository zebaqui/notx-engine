package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdlog "log"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/zebaqui/notx-engine/config"
	grpcsvc "github.com/zebaqui/notx-engine/internal/server/grpc"
	pb "github.com/zebaqui/notx-engine/proto"
	"github.com/zebaqui/notx-engine/snip"
)

// ─────────────────────────────────────────────────────────────────────────────
// Handler — the HTTP layer
// ─────────────────────────────────────────────────────────────────────────────

// Handler wires all HTTP routes and holds the dependencies needed to serve them.
// It implements http.Handler so it can be passed directly to http.Server.
// All business logic is delegated to the gRPC service structs — the HTTP layer
// is a pure translation layer: decode JSON → call gRPC method → encode JSON.
type Handler struct {
	cfg        *config.Config
	noteSvc    pb.NoteServiceServer
	projSvc    pb.ProjectServiceServer
	folderSvc  pb.FolderServiceServer
	contextSvc *grpcsvc.ContextServer // optional
	linkSvc    *grpcsvc.LinkServer    // optional
	plugins    []snip.SnipPlugin

	log    *slog.Logger
	mux    *http.ServeMux
	server *http.Server // initialised in New(); never written after that
}

// New creates a new Handler, registers all routes, and returns it.
// The caller must call Serve to start accepting connections.
func New(
	cfg *config.Config,
	noteSvc pb.NoteServiceServer,
	projSvc pb.ProjectServiceServer,
	folderSvc pb.FolderServiceServer,
	contextSvc *grpcsvc.ContextServer,
	linkSvc *grpcsvc.LinkServer,
	log *slog.Logger,
	plugins []snip.SnipPlugin,
) *Handler {
	addr := fmt.Sprintf("127.0.0.1:%d", cfg.HTTPPort)
	h := &Handler{
		cfg:        cfg,
		noteSvc:    noteSvc,
		projSvc:    projSvc,
		folderSvc:  folderSvc,
		contextSvc: contextSvc,
		linkSvc:    linkSvc,
		plugins:    plugins,
		log:        log,
		mux:        http.NewServeMux(),
	}
	h.server = &http.Server{
		Addr:         addr,
		Handler:      h,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
		ErrorLog:     newDiscardLogger(),
	}
	h.routes()
	return h
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// Serve starts the HTTP server on 127.0.0.1:<port> (plaintext only).
// It blocks until the server shuts down. Call Shutdown to trigger a graceful stop.
// h.server is initialised in New() so Shutdown() can safely read it from any goroutine.
func (h *Handler) Serve() error {
	addr := h.server.Addr

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("http: listen on %s: %w", addr, err)
	}

	h.log.Info("http server listening", "addr", addr)

	if err := h.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http: serve: %w", err)
	}
	return nil
}

func newDiscardLogger() *stdlog.Logger {
	return stdlog.New(io.Discard, "", 0)
}

// Shutdown gracefully stops the HTTP server, waiting up to 30 seconds for
// in-flight requests to complete.
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
	// Health probes
	h.mux.HandleFunc("/healthz", h.handleHealthz)
	h.mux.HandleFunc("/readyz", h.handleReadyz)

	// Snips — typed notes
	h.mux.HandleFunc("/v1/snips", h.withMiddleware(h.routeSnips))

	// Notes
	h.mux.HandleFunc("/v1/notes", h.withMiddleware(h.routeNotes))
	h.mux.HandleFunc("/v1/notes/", h.withMiddleware(h.routeNote))

	// Events
	h.mux.HandleFunc("/v1/events", h.withMiddleware(h.routeAppendEvent))

	// Search
	h.mux.HandleFunc("/v1/search", h.withMiddleware(h.routeSearch))

	// Projects & folders
	h.mux.HandleFunc("/v1/projects", h.withMiddleware(h.routeProjects))
	h.mux.HandleFunc("/v1/projects/", h.withMiddleware(h.routeProject))
	h.mux.HandleFunc("/v1/folders", h.withMiddleware(h.routeFolders))
	h.mux.HandleFunc("/v1/folders/", h.withMiddleware(h.routeFolder))

	// Ports — public, no auth needed; returns all active service ports
	h.mux.HandleFunc("/v1/ports", h.withMiddleware(h.handlePorts))

	// AI credential management
	h.mux.HandleFunc("/v1/ai/credentials", h.withMiddleware(h.routeAICredentials))
	h.mux.HandleFunc("/v1/ai/credentials/", h.withMiddleware(h.routeAICredential))

	// Context graph — bursts, candidates, promote/dismiss, config
	h.mux.HandleFunc("/v1/context/stats", h.withMiddleware(h.routeContextStats))
	h.mux.HandleFunc("/v1/context/candidates", h.withMiddleware(h.routeContextCandidates))
	h.mux.HandleFunc("/v1/context/candidates/", h.withMiddleware(h.routeContextCandidate))
	h.mux.HandleFunc("/v1/context/bursts", h.withMiddleware(h.routeContextBursts))
	h.mux.HandleFunc("/v1/context/bursts/search", h.withMiddleware(h.routeContextBurstSearch))
	h.mux.HandleFunc("/v1/context/bursts/", h.withMiddleware(h.routeContextBurst))
	h.mux.HandleFunc("/v1/context/config/", h.withMiddleware(h.routeContextConfig))
	h.mux.HandleFunc("/v1/context/inferences", h.withMiddleware(h.routeContextInferences))
	h.mux.HandleFunc("/v1/context/inferences/", h.withMiddleware(h.routeContextInference))

	// Register snip plugin HTTP routes
	for _, p := range h.plugins {
		p.RegisterHTTP(h.mux, h.withMiddleware)
	}

	// Links — anchors, backlinks, external links
	h.mux.HandleFunc("/v1/links/anchors", h.withMiddleware(h.routeLinkAnchors))
	h.mux.HandleFunc("/v1/links/anchors/", h.withMiddleware(h.routeLinkAnchor))
	h.mux.HandleFunc("/v1/links/backlinks/recent", h.withMiddleware(h.handleRecentBacklinks))
	h.mux.HandleFunc("/v1/links/backlinks", h.withMiddleware(h.routeLinkBacklinks))
	h.mux.HandleFunc("/v1/links/outbound", h.withMiddleware(h.routeLinkOutbound))
	h.mux.HandleFunc("/v1/links/referrers", h.withMiddleware(h.routeLinkReferrers))
	h.mux.HandleFunc("/v1/links/external", h.withMiddleware(h.routeLinkExternal))
}

// Mux returns the HTTP mux so plugins can register their routes.
func (h *Handler) Mux() *http.ServeMux {
	return h.mux
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

// portsResponse lists the TCP port currently bound by the HTTP server.
type portsResponse struct {
	HTTP *int `json:"http,omitempty"`
}

// handlePorts handles GET /v1/ports.
// It is intentionally public (no auth) so clients and operators can
// discover service addresses without any credentials.
func (h *Handler) handlePorts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	resp := portsResponse{}

	if h.cfg.EnableHTTP {
		p := h.cfg.HTTPPort
		resp.HTTP = &p
	}

	writeJSON(w, http.StatusOK, resp)
}

// ─────────────────────────────────────────────────────────────────────────────
// JSON helpers
// ─────────────────────────────────────────────────────────────────────────────

type errorResponse struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, statusCode int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Status", strconv.Itoa(statusCode))
	w.WriteHeader(statusCode)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, statusCode int, msg string) {
	writeJSON(w, statusCode, &errorResponse{Error: msg})
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

// grpcErrToHTTP maps a gRPC status error to the appropriate HTTP response.
func grpcErrToHTTP(w http.ResponseWriter, r *http.Request, h *Handler, err error, op string) {
	st, ok := status.FromError(err)
	if !ok {
		h.internalError(w, r, op, err)
		return
	}
	switch st.Code() {
	case codes.NotFound:
		writeError(w, http.StatusNotFound, st.Message())
	case codes.AlreadyExists:
		writeError(w, http.StatusConflict, st.Message())
	case codes.InvalidArgument:
		writeError(w, http.StatusBadRequest, st.Message())
	case codes.PermissionDenied:
		writeError(w, http.StatusForbidden, st.Message())
	case codes.Unauthenticated:
		writeError(w, http.StatusUnauthorized, st.Message())
	case codes.Aborted:
		writeError(w, http.StatusConflict, st.Message())
	case codes.FailedPrecondition:
		writeError(w, http.StatusConflict, st.Message())
	default:
		h.internalError(w, r, op, err)
	}
}

func (h *Handler) internalError(w http.ResponseWriter, r *http.Request, op string, err error) {
	loggerFromCtx(r.Context(), h.log).Error("internal error", "op", op, "err", err)
	writeError(w, http.StatusInternalServerError, "internal server error")
}
