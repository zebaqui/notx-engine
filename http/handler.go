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
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/zebaqui/notx-engine/config"
	grpcsvc "github.com/zebaqui/notx-engine/internal/server/grpc"
	"github.com/zebaqui/notx-engine/repo"
)

// ─────────────────────────────────────────────────────────────────────────────
// Handler — the HTTP layer
// ─────────────────────────────────────────────────────────────────────────────

// Handler wires all HTTP routes and holds the dependencies needed to serve them.
// It implements http.Handler so it can be passed directly to http.Server.
// All business logic is delegated to the gRPC service structs — the HTTP layer
// is a pure translation layer: decode JSON → call gRPC method → encode JSON.
type Handler struct {
	cfg            *config.Config
	noteSvc        *grpcsvc.NoteServer
	projSvc        *grpcsvc.ProjectServer
	folderSvc      *grpcsvc.FolderServer
	deviceSvc      *grpcsvc.DeviceServer
	deviceAdminSvc *grpcsvc.DeviceAdminServer
	userSvc        *grpcsvc.UserServer
	pairing        *grpcsvc.PairingServer
	secretStore    repo.PairingSecretStore
	relaySvc       *grpcsvc.RelayServer
	contextSvc     *grpcsvc.ContextServer
	linkSvc        *grpcsvc.LinkServer

	log    *slog.Logger
	mux    *http.ServeMux
	server *http.Server
}

// New creates a new Handler, registers all routes, and returns it.
// The caller must call Serve to start accepting connections.
// pairingSvc and secretStore may be nil when server pairing is disabled;
// the relevant endpoints will return 503 in that case.
// relaySvc may be nil when the relay engine is not available; the relay
// endpoints will return 503 in that case.
func New(
	cfg *config.Config,
	noteSvc *grpcsvc.NoteServer,
	projSvc *grpcsvc.ProjectServer,
	folderSvc *grpcsvc.FolderServer,
	deviceSvc *grpcsvc.DeviceServer,
	deviceAdminSvc *grpcsvc.DeviceAdminServer,
	userSvc *grpcsvc.UserServer,
	log *slog.Logger,
	pairingSvc *grpcsvc.PairingServer,
	secretStore repo.PairingSecretStore,
	relaySvc *grpcsvc.RelayServer,
	contextSvc *grpcsvc.ContextServer,
	linkSvc *grpcsvc.LinkServer,
) *Handler {
	h := &Handler{
		cfg:            cfg,
		noteSvc:        noteSvc,
		projSvc:        projSvc,
		folderSvc:      folderSvc,
		deviceSvc:      deviceSvc,
		deviceAdminSvc: deviceAdminSvc,
		userSvc:        userSvc,
		pairing:        pairingSvc,
		secretStore:    secretStore,
		relaySvc:       relaySvc,
		contextSvc:     contextSvc,
		linkSvc:        linkSvc,
		log:            log,
		mux:            http.NewServeMux(),
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

func newDiscardLogger() *stdlog.Logger {
	return stdlog.New(io.Discard, "", 0)
}

func buildTLSConfig(cfg *config.Config) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(cfg.TLSCertFile, cfg.TLSKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load cert/key: %w", err)
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
	}

	if cfg.MTLSEnabled() {
		caPEM, err := os.ReadFile(cfg.TLSCAFile)
		if err != nil {
			return nil, fmt.Errorf("read CA file %q: %w", cfg.TLSCAFile, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("parse CA PEM: no valid blocks found")
		}
		tlsCfg.ClientCAs = pool
		tlsCfg.ClientAuth = tls.RequireAndVerifyClientCert
	}

	return tlsCfg, nil
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

	// Notes
	h.mux.HandleFunc("/v1/notes", h.withDeviceAuthMiddleware(h.routeNotes))
	// /v1/notes/receive/ is a server-to-server push endpoint — no device auth.
	h.mux.HandleFunc("/v1/notes/receive/", h.withMiddleware(h.routeNoteReceiveOpen))
	h.mux.HandleFunc("/v1/notes/", h.withDeviceAuthMiddleware(h.routeNote))

	// Events
	h.mux.HandleFunc("/v1/events", h.withDeviceAuthMiddleware(h.routeAppendEvent))

	// Sync progress stream
	h.mux.HandleFunc("/v1/sync/stream", h.withDeviceAuthMiddleware(h.handleSyncStream))

	// Search
	h.mux.HandleFunc("/v1/search", h.withDeviceAuthMiddleware(h.routeSearch))

	// Projects & folders
	h.mux.HandleFunc("/v1/projects", h.withDeviceAuthMiddleware(h.routeProjects))
	h.mux.HandleFunc("/v1/projects/", h.withDeviceAuthMiddleware(h.routeProject))
	h.mux.HandleFunc("/v1/folders", h.withDeviceAuthMiddleware(h.routeFolders))
	h.mux.HandleFunc("/v1/folders/", h.withDeviceAuthMiddleware(h.routeFolder))

	// Devices — POST /v1/devices (registration) is intentionally open so a
	// new device can bootstrap itself without a pre-existing identity.
	// All other device routes (GET, PATCH, DELETE) require a valid device.
	h.mux.HandleFunc("/v1/devices", h.withMiddleware(h.routeDevicesOpen))
	// /v1/devices/ is a single catch-all. The dispatcher (routeDeviceDispatch)
	// applies the lighter existence-only auth for status/status/stream sub-paths
	// (so a pending device can poll its own approval state) and the full
	// approval-gated auth for every other device sub-path.
	h.mux.HandleFunc("/v1/devices/", h.withMiddleware(h.routeDeviceDispatch))

	// Users
	h.mux.HandleFunc("/v1/users", h.withDeviceAuthMiddleware(h.routeUsers))
	h.mux.HandleFunc("/v1/users/", h.withDeviceAuthMiddleware(h.routeUser))

	// Server pairing — /v1/servers/ca is intentionally public (returns CA cert only).
	// All write/admin routes require device auth.
	h.mux.HandleFunc("/v1/servers/ca", h.withMiddleware(h.routeServersCA))
	h.mux.HandleFunc("/v1/pairing-secrets", h.withDeviceAuthMiddleware(h.routePairingSecrets))
	h.mux.HandleFunc("/v1/servers/outbound-pair", h.withDeviceAuthMiddleware(h.routeOutboundPair))
	h.mux.HandleFunc("/v1/servers", h.withDeviceAuthMiddleware(h.routeServers))
	h.mux.HandleFunc("/v1/servers/", h.withDeviceAuthMiddleware(h.routeServer))

	// Server info
	h.mux.HandleFunc("/v1/info", h.withMiddleware(h.routeServerInfo))

	// Ports — public, no auth needed; returns all active service ports
	h.mux.HandleFunc("/v1/ports", h.withMiddleware(h.handlePorts))

	// Relay
	if h.relaySvc != nil {
		h.routeRelay(h.relaySvc)
	}

	// Context graph — bursts, candidates, promote/dismiss, config
	h.mux.HandleFunc("/v1/context/stats", h.withDeviceAuthMiddleware(h.routeContextStats))
	h.mux.HandleFunc("/v1/context/candidates", h.withDeviceAuthMiddleware(h.routeContextCandidates))
	h.mux.HandleFunc("/v1/context/candidates/", h.withDeviceAuthMiddleware(h.routeContextCandidate))
	h.mux.HandleFunc("/v1/context/bursts", h.withDeviceAuthMiddleware(h.routeContextBursts))
	h.mux.HandleFunc("/v1/context/bursts/search", h.withDeviceAuthMiddleware(h.routeContextBurstSearch))
	h.mux.HandleFunc("/v1/context/bursts/", h.withDeviceAuthMiddleware(h.routeContextBurst))
	h.mux.HandleFunc("/v1/context/config/", h.withDeviceAuthMiddleware(h.routeContextConfig))
	h.mux.HandleFunc("/v1/context/inferences", h.withDeviceAuthMiddleware(h.routeContextInferences))
	h.mux.HandleFunc("/v1/context/inferences/", h.withDeviceAuthMiddleware(h.routeContextInference))

	// Links — anchors, backlinks, external links
	h.mux.HandleFunc("/v1/links/anchors", h.withDeviceAuthMiddleware(h.routeLinkAnchors))
	h.mux.HandleFunc("/v1/links/anchors/", h.withDeviceAuthMiddleware(h.routeLinkAnchor))
	h.mux.HandleFunc("/v1/links/backlinks/recent", h.withDeviceAuthMiddleware(h.handleRecentBacklinks))
	h.mux.HandleFunc("/v1/links/backlinks", h.withDeviceAuthMiddleware(h.routeLinkBacklinks))
	h.mux.HandleFunc("/v1/links/outbound", h.withDeviceAuthMiddleware(h.routeLinkOutbound))
	h.mux.HandleFunc("/v1/links/referrers", h.withDeviceAuthMiddleware(h.routeLinkReferrers))
	h.mux.HandleFunc("/v1/links/external", h.withDeviceAuthMiddleware(h.routeLinkExternal))
}

func (h *Handler) withMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Inject a request-scoped logger.
		log := h.log.With(
			"method", r.Method,
			"path", r.URL.Path,
		)
		ctx := context.WithValue(r.Context(), ctxKeyLogger{}, log)
		next(w, r.WithContext(ctx))
	}
}

type ctxKeyLogger struct{}

func loggerFromCtx(ctx context.Context, fallback *slog.Logger) *slog.Logger {
	if l, ok := ctx.Value(ctxKeyLogger{}).(*slog.Logger); ok && l != nil {
		return l
	}
	return fallback
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

// portsResponse lists every TCP port currently bound by this server instance.
// Fields are omitted when the corresponding service is disabled.
type portsResponse struct {
	HTTP             *int `json:"http,omitempty"`
	GRPC             *int `json:"grpc,omitempty"`
	PairingBootstrap *int `json:"pairing_bootstrap,omitempty"`
}

// handlePorts handles GET /v1/ports.
// It is intentionally public (no device auth) so clients and operators can
// discover service addresses without needing a registered device.
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

	if h.cfg.EnableGRPC {
		p := h.cfg.GRPCPort
		resp.GRPC = &p
	}

	if h.cfg.Pairing.Enabled {
		p := h.cfg.Pairing.BootstrapPort
		resp.PairingBootstrap = &p
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
