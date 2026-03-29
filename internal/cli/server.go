package cli

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/bcrypt"

	"github.com/zebaqui/notx-engine/internal/clientconfig"
	"github.com/zebaqui/notx-engine/internal/repo/file"
	"github.com/zebaqui/notx-engine/internal/server"
	"github.com/zebaqui/notx-engine/internal/server/config"
)

// adminURNsFromConfig returns the admin device and owner URNs stored in the
// client config, falling back to the well-known all-zero sentinels only when
// the config cannot be loaded at all (should never happen in practice because
// runServer calls EnsureConfig first).
func adminURNsFromConfig(fileCfg *clientconfig.Config) (deviceURN, ownerURN string) {
	deviceURN = fileCfg.Admin.AdminDeviceURN
	ownerURN = fileCfg.Admin.AdminOwnerURN

	if deviceURN == "" {
		deviceURN = config.DefaultAdminDeviceURN
	}
	if ownerURN == "" {
		ownerURN = config.DefaultAdminOwnerURN
	}
	return deviceURN, ownerURN
}

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Start the notx server",
	Long: `Start the notx server with HTTP and/or gRPC listeners.

Default values are read from ~/.notx/config.json. Flags override the config
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
// Defaults are seeded from ~/.notx/config.json in init(); flags override them.
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

	deviceAutoApprove bool
	adminPassphrase   string
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

	// Device onboarding
	f.BoolVar(&serverFlags.deviceAutoApprove, "device-auto-approve", false,
		"Automatically approve newly registered devices (skip manual approval step)")

	// Admin passphrase — when set, remote `notx admin --remote` can register
	// an admin device by presenting this passphrase. The plaintext is never
	// stored; only a bcrypt hash is kept in memory for the lifetime of the
	// server process.
	f.StringVar(&serverFlags.adminPassphrase, "admin-passphrase", "",
		"Passphrase required to register an admin device from a remote machine (plaintext; hashed at startup)")

	rootCmd.AddCommand(serverCmd)
}

func runServer(cmd *cobra.Command, args []string) error {
	// ── Ensure ~/.notx/config.json exists on first run ───────────────────────
	if created, err := clientconfig.EnsureConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "  \033[33m⚠\033[0m  Could not write default config: %v\n", err)
	} else if created {
		path, _ := clientconfig.Path()
		fmt.Fprintf(os.Stdout, "\n  \033[1;32m✓\033[0m  First run — created default config at \033[36m%s\033[0m\n", path)
		fmt.Fprintf(os.Stdout, "       Run \033[1mnotx config\033[0m to customise it.\n\n")
	}

	// ── Build config from flags (already seeded from ~/.notx/config.json) ─────
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
	cfg.DeviceOnboarding.AutoApprove = serverFlags.deviceAutoApprove

	// Populate admin URNs from the persisted config so that every installation
	// has its own unique, unpredictable admin identity rather than the shared
	// all-zero sentinel. EnsureConfig (called above) guarantees these are
	// already written to ~/.notx/config.json before we reach this point.
	fileCfg, _ := clientconfig.Load()
	cfg.Admin.DeviceURN, cfg.Admin.OwnerURN = adminURNsFromConfig(fileCfg)

	// Hash the admin passphrase if one was supplied.
	if serverFlags.adminPassphrase != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(serverFlags.adminPassphrase), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("hash admin passphrase: %w", err)
		}
		cfg.Admin.AdminPassphraseHash = string(hash)
		fmt.Fprintf(os.Stdout, "  \033[1;32m✓\033[0m  Admin passphrase set — remote admin registration enabled\n\n")
	}

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
		"device_auto_approve", cfg.DeviceOnboarding.AutoApprove,
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
	srv, err := server.New(cfg, provider, provider, provider, provider, log)
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
