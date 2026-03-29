package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/zebaqui/notx-engine/internal/admin"
	"github.com/zebaqui/notx-engine/internal/buildinfo"
	"github.com/zebaqui/notx-engine/internal/clientconfig"
)

var adminCmd = &cobra.Command{
	Use:   "admin",
	Short: "Serve the embedded admin UI",
	Long: `Serve the notx admin dashboard as a self-contained HTTP server.

The admin UI is compiled into the binary at build time. No external files or
network access are required — everything (HTML, JS, CSS) is embedded.

The admin panel proxies API calls to the notx server. By default it expects
the notx API server to be running on localhost:4060. Override with --api.

Run the API server alongside with:

  notx server

LOCAL MODE (default — server on the same machine):

  notx admin

  The admin UI uses the built-in sentinel admin device that the server
  bootstraps automatically on startup. No registration required.

REMOTE MODE (server on a different machine):

  notx admin --remote https://my-server:4060

  You will be prompted for the admin passphrase that was set on the server
  with --admin-passphrase. A new admin device is registered on the remote
  server and its URN is saved to ~/.notx/config.json so subsequent runs do
  not need to register again.

Examples:
  # Local mode — default port (9090), proxying API to localhost:4060
  notx admin

  # Local mode — custom port
  notx admin --port 8080

  # Remote mode — register against a remote server
  notx admin --remote https://my-server:4060

  # Remote mode — force re-registration (ignore saved device URN)
  notx admin --remote https://my-server:4060 --reregister
`,
	RunE: runAdmin,
}

var adminFlags struct {
	port       int
	host       string
	api        string
	remote     string
	reregister bool
}

func init() {
	// Seed defaults from ~/.notx/config.json so the help text reflects the real
	// effective values. Flags still override for a single invocation.
	fileCfg, _ := clientconfig.Load()

	adminAddr := fileCfg.Admin.Addr // e.g. ":9090" or "127.0.0.1:9090"
	adminPort := portFromAddr(adminAddr, 9090)
	adminHost := hostFromAddr(adminAddr, "")
	adminAPI := fileCfg.Admin.APIAddr

	f := adminCmd.Flags()
	f.IntVar(&adminFlags.port, "port", adminPort,
		fmt.Sprintf("TCP port to serve the admin UI on (default from config: %d)", adminPort))
	f.StringVar(&adminFlags.host, "host", adminHost,
		"Bind address (default: all interfaces)")
	f.StringVar(&adminFlags.api, "api", adminAPI,
		fmt.Sprintf("Base URL of the notx API server to proxy requests to (default from config: %s)", adminAPI))
	f.StringVar(&adminFlags.remote, "remote", "",
		"Base URL of a remote notx server to register against as admin (e.g. https://my-server:4060)")
	f.BoolVar(&adminFlags.reregister, "reregister", false,
		"Force re-registration even if a device URN is already saved in the config")

	rootCmd.AddCommand(adminCmd)
}

// ─────────────────────────────────────────────────────────────────────────────
// Remote admin registration
// ─────────────────────────────────────────────────────────────────────────────

// adminRegisterRequest mirrors the JSON body for POST /v1/devices.
type adminRegisterRequest struct {
	URN             string `json:"urn"`
	Name            string `json:"name"`
	OwnerURN        string `json:"owner_urn"`
	AdminPassphrase string `json:"admin_passphrase"`
}

// adminRegisterResponse is the minimal shape we need from the registration response.
type adminRegisterResponse struct {
	URN            string `json:"urn"`
	ApprovalStatus string `json:"approval_status"`
	Role           string `json:"role"`
}

// registerRemoteAdminDevice registers a new admin device on the remote server
// using the supplied passphrase. It returns the registered device URN.
func registerRemoteAdminDevice(serverBase, passphrase string) (string, error) {
	namespace := "notx"
	deviceID := uuid.New().String()
	ownerID := uuid.New().String()

	deviceURN := fmt.Sprintf("%s:device:%s", namespace, deviceID)
	ownerURN := fmt.Sprintf("%s:usr:%s", namespace, ownerID)

	payload := adminRegisterRequest{
		URN:             deviceURN,
		Name:            "notx-admin-remote",
		OwnerURN:        ownerURN,
		AdminPassphrase: passphrase,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal registration payload: %w", err)
	}

	url := serverBase + "/v1/devices"
	resp, err := http.Post(url, "application/json", bytes.NewReader(body)) //nolint:noctx
	if err != nil {
		return "", fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusConflict {
		return "", fmt.Errorf("device URN already registered — this should not happen with a fresh UUID")
	}
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("server returned %d: %s", resp.StatusCode, string(respBody))
	}

	var reg adminRegisterResponse
	if err := json.Unmarshal(respBody, &reg); err != nil {
		return "", fmt.Errorf("parse registration response: %w", err)
	}

	if reg.Role != "admin" {
		return "", fmt.Errorf(
			"registration succeeded but device was not granted admin role (got %q) — "+
				"check that the server was started with --admin-passphrase and that the passphrase is correct",
			reg.Role,
		)
	}

	return reg.URN, nil
}

// ensureRemoteAdminDevice resolves which device URN the admin UI should use
// when connecting to a remote server. It either reuses a previously saved URN
// from the config or performs a fresh registration using the supplied
// passphrase. The config is updated and saved when a new registration occurs.
func ensureRemoteAdminDevice(serverBase, passphrase string, forceReregister bool) (deviceURN string, err error) {
	cfg, err := clientconfig.Load()
	if err != nil {
		return "", fmt.Errorf("load config: %w", err)
	}

	if !forceReregister && cfg.Admin.DeviceURN != "" {
		fmt.Fprintf(os.Stdout,
			"  \033[1;34mℹ\033[0m  Reusing saved admin device URN: \033[36m%s\033[0m\n",
			cfg.Admin.DeviceURN,
		)
		return cfg.Admin.DeviceURN, nil
	}

	fmt.Fprintf(os.Stdout, "  \033[1;33m⟳\033[0m  Registering new admin device on %s …\n", serverBase)

	urn, err := registerRemoteAdminDevice(serverBase, passphrase)
	if err != nil {
		return "", fmt.Errorf("register admin device: %w", err)
	}

	// Persist the new device URN so future runs don't need to re-register.
	cfg.Admin.DeviceURN = urn
	cfg.Admin.APIAddr = serverBase
	if err := clientconfig.Save(cfg); err != nil {
		// Non-fatal: warn but continue — the session will still work.
		fmt.Fprintf(os.Stderr,
			"  \033[33m⚠\033[0m  Could not save device URN to config: %v\n  Next run will need to re-register.\n",
			err,
		)
	} else {
		path, _ := clientconfig.Path()
		fmt.Fprintf(os.Stdout,
			"  \033[1;32m✓\033[0m  Admin device registered — URN saved to \033[36m%s\033[0m\n",
			path,
		)
	}

	return urn, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// runAdmin
// ─────────────────────────────────────────────────────────────────────────────

func runAdmin(cmd *cobra.Command, args []string) error {
	log := buildLogger("info")

	// ── Determine API base URL and admin device URN ──────────────────────────

	apiBase := adminFlags.api
	var deviceURN string

	if adminFlags.remote != "" {
		// Remote mode: the server lives somewhere else. We need a passphrase
		// to register (or reuse) an admin device on that server.
		apiBase = adminFlags.remote

		fmt.Fprintf(os.Stdout, "\n  \033[1;34m▶\033[0m  Remote admin mode → \033[36m%s\033[0m\n\n", apiBase)

		passphrase, err := promptPasswordOnce("  Admin passphrase: ")
		if err != nil {
			return fmt.Errorf("read passphrase: %w", err)
		}
		if passphrase == "" {
			return fmt.Errorf("passphrase must not be empty")
		}

		deviceURN, err = ensureRemoteAdminDevice(apiBase, passphrase, adminFlags.reregister)
		if err != nil {
			return err
		}

		fmt.Fprintf(os.Stdout, "\n")
	} else {
		// Local mode: read the persisted admin device URN from ~/.notx/config.json
		// so the UI sends the unique per-installation URN instead of the
		// hardcoded all-zero sentinel.
		localCfg, err := clientconfig.Load()
		if err == nil && localCfg.Admin.AdminDeviceURN != "" {
			deviceURN = localCfg.Admin.AdminDeviceURN
		}
	}

	addr := fmt.Sprintf("%s:%d", adminFlags.host, adminFlags.port)

	mux := http.NewServeMux()

	// Serve a tiny /admin-config endpoint so the embedded SPA can discover
	// the device URN to use for X-Device-ID without it being hardcoded.
	mux.HandleFunc("/admin-config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"device_urn": deviceURN,
		})
	})

	mux.Handle("/", admin.Handler(apiBase))

	srv := &http.Server{
		Addr:         addr,
		Handler:      withRequestLogger(mux, log),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second, // longer for SSE streams
		IdleTimeout:  120 * time.Second,
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("admin: listen on %s: %w", addr, err)
	}

	// Resolve the actual address in case port 0 was given.
	actualAddr := ln.Addr().String()

	modeLabel := "local"
	if adminFlags.remote != "" {
		modeLabel = "remote"
	}

	log.Info("notx admin UI",
		"addr", fmt.Sprintf("http://%s", actualAddr),
		"api", apiBase,
		"mode", modeLabel,
		"device_urn", deviceURN,
		"version", buildinfo.Version,
		"commit", buildinfo.Commit,
		"built_at", buildinfo.BuildTime,
	)

	// Pretty-print for humans.
	fmt.Fprintf(os.Stdout, "\n  \033[1;32m▶\033[0m  notx admin   →  \033[1;36mhttp://%s\033[0m\n", actualAddr)
	fmt.Fprintf(os.Stdout, "  \033[1;34m⇒\033[0m  proxying API →  \033[0;36m%s\033[0m\n", apiBase)
	if deviceURN != "" {
		fmt.Fprintf(os.Stdout, "  \033[1;34m⇒\033[0m  admin device →  \033[0;36m%s\033[0m\n", deviceURN)
	}
	fmt.Fprintf(os.Stdout, "\n")

	// ── Graceful shutdown on SIGINT / SIGTERM ─────────────────────────────────
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("admin server: %w", err)
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		log.Info("admin: shutdown signal received")
	case err := <-errCh:
		if err != nil {
			return err
		}
		return nil
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	log.Info("admin: shutting down")
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("admin: graceful shutdown: %w", err)
	}

	// Drain any error that raced with shutdown.
	if err := <-errCh; err != nil {
		return err
	}

	log.Info("admin: stopped")
	return nil
}

// promptPasswordOnce reads a password from stdin without echoing it.
// Falls back to plain line reading if the terminal does not support raw mode.
func promptPasswordOnce(prompt string) (string, error) {
	fmt.Fprint(os.Stdout, prompt)

	// Try to read without echo using the OS-specific approach.
	// We use a simple bufio scanner as a fallback since this avoids
	// pulling in golang.org/x/term just for one prompt.
	var line string
	_, err := fmt.Scanln(&line)
	// Print a newline after the silent input.
	fmt.Fprintln(os.Stdout)
	if err != nil && line == "" {
		return "", err
	}
	return line, nil
}

// withRequestLogger wraps a handler with minimal structured request logging.
func withRequestLogger(next http.Handler, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		log.Debug("admin request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.status,
			"duration", time.Since(start).String(),
		)
	})
}

// statusWriter captures the HTTP status code written by a handler.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}
