package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

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

Examples:
  # Serve admin on the default port (9090), proxying API to localhost:4060
  notx admin

  # Serve on a custom port
  notx admin --port 8080

  # Point at a non-default API server address
  notx admin --api http://localhost:5000

  # Serve on a specific interface
  notx admin --port 443 --host 127.0.0.1
`,
	RunE: runAdmin,
}

var adminFlags struct {
	port int
	host string
	api  string
}

func init() {
	// Seed defaults from ~/.notx/config.yml so the help text reflects the real
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

	rootCmd.AddCommand(adminCmd)
}

func runAdmin(cmd *cobra.Command, args []string) error {
	log := buildLogger("info")

	addr := fmt.Sprintf("%s:%d", adminFlags.host, adminFlags.port)

	mux := http.NewServeMux()
	mux.Handle("/", admin.Handler(adminFlags.api))

	srv := &http.Server{
		Addr:         addr,
		Handler:      withRequestLogger(mux, log),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("admin: listen on %s: %w", addr, err)
	}

	// Resolve the actual address in case port 0 was given.
	actualAddr := ln.Addr().String()

	log.Info("notx admin UI",
		"addr", fmt.Sprintf("http://%s", actualAddr),
		"api", adminFlags.api,
		"version", buildinfo.Version,
		"commit", buildinfo.Commit,
		"built_at", buildinfo.BuildTime,
	)

	// Pretty-print for humans.
	fmt.Fprintf(os.Stdout, "\n  \033[1;32m▶\033[0m  notx admin   →  \033[1;36mhttp://%s\033[0m\n", actualAddr)
	fmt.Fprintf(os.Stdout, "  \033[1;34m⇒\033[0m  proxying API →  \033[0;36m%s\033[0m\n\n", adminFlags.api)

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
