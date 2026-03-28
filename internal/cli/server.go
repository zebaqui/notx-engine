package cli

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"github.com/zebaqui/notx-engine/internal/repo/file"
	"github.com/zebaqui/notx-engine/internal/server"
	"github.com/zebaqui/notx-engine/internal/server/config"
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Start the notx server",
	Long: `Start the notx server with HTTP and/or gRPC listeners.

By default both HTTP (port 4060) and gRPC (port 50051) are enabled.
Use --http / --grpc to enable only one protocol, or supply --http-port /
--grpc-port to change the default ports.

Examples:
  # Start both servers on default ports
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
// They are copied into config.Config inside runServer.
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
	f := serverCmd.Flags()

	// Protocol toggles
	f.BoolVar(&serverFlags.httpEnabled, "http", true, "Enable the HTTP/JSON API server")
	f.BoolVar(&serverFlags.grpcEnabled, "grpc", true, "Enable the gRPC server")

	// Network
	f.IntVar(&serverFlags.httpPort, "http-port", config.DefaultHTTPPort,
		fmt.Sprintf("TCP port for the HTTP server (default: %d)", config.DefaultHTTPPort))
	f.IntVar(&serverFlags.grpcPort, "grpc-port", config.DefaultGRPCPort,
		fmt.Sprintf("TCP port for the gRPC server (default: %d)", config.DefaultGRPCPort))
	f.StringVar(&serverFlags.host, "host", "",
		"Bind address for both servers (default: all interfaces)")

	// Storage
	f.StringVar(&serverFlags.dataDir, "data-dir", "./data",
		"Root directory for note files and the Badger index")

	// TLS / mTLS
	f.StringVar(&serverFlags.tlsCert, "tls-cert", "",
		"Path to the PEM-encoded server TLS certificate (leave empty to disable TLS)")
	f.StringVar(&serverFlags.tlsKey, "tls-key", "",
		"Path to the PEM-encoded server TLS private key")
	f.StringVar(&serverFlags.tlsCA, "tls-ca", "",
		"Path to the PEM-encoded CA certificate for mTLS client verification")

	// Operational
	f.StringVar(&serverFlags.logLevel, "log-level", "info",
		"Log verbosity: debug, info, warn, error")

	rootCmd.AddCommand(serverCmd)
}

func runServer(cmd *cobra.Command, args []string) error {
	// ── Build config ─────────────────────────────────────────────────────────
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
