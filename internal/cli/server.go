package cli

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"github.com/zebaqui/notx-engine/internal/clientconfig"
	"github.com/zebaqui/notx-engine/internal/repo/file"
	"github.com/zebaqui/notx-engine/internal/server"
	"github.com/zebaqui/notx-engine/internal/server/config"
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Start the notx server",
	Long: `Start the notx server with HTTP and/or gRPC listeners.

Default values are read from ~/.notx/config.yml. Flags override the config
for a single invocation. Run "notx config" to edit the config interactively.

Examples:
  # Start both servers using config defaults
  notx server

  # HTTP only on a custom port
  notx server --grpc=false --http-port 8080

  # gRPC only with TLS
  notx server --http=false --tls-cert server.crt --tls-key server.key

  # Both servers, mTLS, custom data directory
  notx server --data-dir /var/notx --tls-cert s.crt --tls-key s.key --tls-ca ca.crt
`,
	RunE: runServer,
}

// serverFlags holds the raw flag values populated by cobra.
// Defaults are seeded from ~/.notx/config.yml in init(); flags override them.
var serverFlags struct {
	httpEnabled bool
	grpcEnabled bool

	httpPort int
	grpcPort int
	host     string

	dataDir string

	tlsCert string
	tlsKey  string
	tlsCA   string

	logLevel string
}

func init() {
	// Seed defaults from config so flags show the real effective value in --help.
	fileCfg, _ := clientconfig.Load()

	f := serverCmd.Flags()

	// Protocol toggles
	f.BoolVar(&serverFlags.httpEnabled, "http", fileCfg.Server.EnableHTTP,
		"Enable the HTTP/JSON API server")
	f.BoolVar(&serverFlags.grpcEnabled, "grpc", fileCfg.Server.EnableGRPC,
		"Enable the gRPC server")

	// Network — parse host:port from config addrs, fall back to package defaults.
	httpPort := portFromAddr(fileCfg.Server.HTTPAddr, config.DefaultHTTPPort)
	grpcPort := portFromAddr(fileCfg.Server.GRPCAddr, config.DefaultGRPCPort)
	host := hostFromAddr(fileCfg.Server.HTTPAddr, "")

	f.IntVar(&serverFlags.httpPort, "http-port", httpPort,
		fmt.Sprintf("TCP port for the HTTP server (default from config: %d)", httpPort))
	f.IntVar(&serverFlags.grpcPort, "grpc-port", grpcPort,
		fmt.Sprintf("TCP port for the gRPC server (default from config: %d)", grpcPort))
	f.StringVar(&serverFlags.host, "host", host,
		"Bind address for both servers (default: all interfaces)")

	// Storage
	f.StringVar(&serverFlags.dataDir, "data-dir", fileCfg.Storage.DataDir,
		"Root directory for note files and the Badger index")

	// TLS / mTLS
	f.StringVar(&serverFlags.tlsCert, "tls-cert", fileCfg.TLS.CertFile,
		"Path to the PEM-encoded server TLS certificate (leave empty to disable TLS)")
	f.StringVar(&serverFlags.tlsKey, "tls-key", fileCfg.TLS.KeyFile,
		"Path to the PEM-encoded server TLS private key")
	f.StringVar(&serverFlags.tlsCA, "tls-ca", fileCfg.TLS.CAFile,
		"Path to the PEM-encoded CA certificate for mTLS client verification")

	// Operational
	f.StringVar(&serverFlags.logLevel, "log-level", fileCfg.EffectiveLogLevel(fileCfg.Server.LogLevel),
		"Log verbosity: debug, info, warn, error")

	rootCmd.AddCommand(serverCmd)
}

func runServer(cmd *cobra.Command, args []string) error {
	// ── Build config from flags (already seeded from ~/.notx/config.yml) ─────
	cfg := config.Default()
	cfg.EnableHTTP = serverFlags.httpEnabled
	cfg.EnableGRPC = serverFlags.grpcEnabled
	cfg.HTTPPort = serverFlags.httpPort
	cfg.GRPCPort = serverFlags.grpcPort
	cfg.Host = serverFlags.host
	cfg.DataDir = serverFlags.dataDir
	cfg.TLSCertFile = serverFlags.tlsCert
	cfg.TLSKeyFile = serverFlags.tlsKey
	cfg.TLSCAFile = serverFlags.tlsCA
	cfg.LogLevel = serverFlags.logLevel

	if err := cfg.Validate(); err != nil {
		return err
	}

	// ── Build logger ─────────────────────────────────────────────────────────
	log := buildLogger(cfg.LogLevel)

	log.Info("notx server starting",
		"http", cfg.EnableHTTP,
		"http_addr", cfg.HTTPAddr(),
		"grpc", cfg.EnableGRPC,
		"grpc_addr", cfg.GRPCAddr(),
		"data_dir", cfg.DataDir,
		"tls", cfg.TLSEnabled(),
		"mtls", cfg.MTLSEnabled(),
	)

	// ── Open file provider ───────────────────────────────────────────────────
	provider, err := file.New(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("open file provider at %q: %w", cfg.DataDir, err)
	}
	defer func() {
		if err := provider.Close(); err != nil {
			log.Warn("provider close error", "err", err)
		}
	}()

	// ── Build and run server ─────────────────────────────────────────────────
	srv, err := server.New(cfg, provider, log)
	if err != nil {
		return fmt.Errorf("build server: %w", err)
	}

	return srv.Run()
}

// ─────────────────────────────────────────────────────────────────────────────
// Address helpers
// ─────────────────────────────────────────────────────────────────────────────

// portFromAddr parses a "host:port" string and returns the port as an int.
// Returns fallback if the string is empty or cannot be parsed.
func portFromAddr(addr string, fallback int) int {
	if addr == "" {
		return fallback
	}
	var port int
	if _, err := fmt.Sscanf(addr[len(addr)-5:], ":%d", &port); err != nil {
		// Try full string as ":PORT"
		if _, err2 := fmt.Sscanf(addr, ":%d", &port); err2 != nil {
			return fallback
		}
	}
	if port < 1 || port > 65535 {
		return fallback
	}
	return port
}

// hostFromAddr parses a "host:port" string and returns the host portion.
// Returns fallback (typically "") if addr is just ":port".
func hostFromAddr(addr string, fallback string) string {
	if addr == "" {
		return fallback
	}
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			h := addr[:i]
			if h == "" {
				return fallback
			}
			return h
		}
	}
	return fallback
}

// ─────────────────────────────────────────────────────────────────────────────
// Logger
// ─────────────────────────────────────────────────────────────────────────────

// buildLogger constructs a structured slog.Logger for the given level string.
func buildLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: lvl,
	})
	return slog.New(handler)
}
