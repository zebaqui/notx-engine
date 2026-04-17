package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/crypto/bcrypt"

	"github.com/zebaqui/notx-engine/config"
	"github.com/zebaqui/notx-engine/internal/clientconfig"
	pairingsecret "github.com/zebaqui/notx-engine/internal/pairing"
	"github.com/zebaqui/notx-engine/internal/server"
	"github.com/zebaqui/notx-engine/repo/sqlite"
)

// ─────────────────────────────────────────────────────────────────────────────
// Daemon file paths
// ─────────────────────────────────────────────────────────────────────────────

func notxDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".notx"
	}
	return filepath.Join(home, ".notx")
}

func pidFilePath() string   { return filepath.Join(notxDir(), "server.pid") }
func logFilePath() string   { return filepath.Join(notxDir(), "server.log") }
func portsFilePath() string { return filepath.Join(notxDir(), "server.ports") }

// ─────────────────────────────────────────────────────────────────────────────
// PID helpers
// ─────────────────────────────────────────────────────────────────────────────

func writePID(pid int) error {
	return os.WriteFile(pidFilePath(), []byte(strconv.Itoa(pid)+"\n"), 0o644)
}

func readPID() (int, error) {
	data, err := os.ReadFile(pidFilePath())
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("corrupt pid file: %w", err)
	}
	return pid, nil
}

func removePID() { os.Remove(pidFilePath()) }

// ─────────────────────────────────────────────────────────────────────────────
// Ports file helpers
// ─────────────────────────────────────────────────────────────────────────────

// serverPorts is written to ~/.notx/server.ports when the daemon worker starts
// so that other commands (e.g. sync) can discover which ports the server is
// actually listening on, regardless of what flags were passed.
type serverPorts struct {
	HTTPPort      int    `json:"http_port"`
	GRPCPort      int    `json:"grpc_port"`
	PeerCertDir   string `json:"peer_cert_dir,omitempty"`
	PeerAuthority string `json:"peer_authority,omitempty"`
}

func writePorts(httpPort, grpcPort int, peerCertDir, peerAuthority string) error {
	data, err := json.Marshal(serverPorts{
		HTTPPort:      httpPort,
		GRPCPort:      grpcPort,
		PeerCertDir:   peerCertDir,
		PeerAuthority: peerAuthority,
	})
	if err != nil {
		return err
	}
	return os.WriteFile(portsFilePath(), data, 0o644)
}

// ReadServerPorts reads ~/.notx/server.ports and returns the ports the running
// server is listening on.  Returns an error when the file does not exist (i.e.
// no server has been started yet).
func ReadServerPorts() (serverPorts, error) {
	data, err := os.ReadFile(portsFilePath())
	if err != nil {
		return serverPorts{}, err
	}
	var p serverPorts
	if err := json.Unmarshal(data, &p); err != nil {
		return serverPorts{}, fmt.Errorf("corrupt ports file: %w", err)
	}
	return p, nil
}

func removePorts() { os.Remove(portsFilePath()) }

// processAlive returns true when a process with the given PID exists and is
// reachable via kill(pid, 0).
func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Admin URN helper (unchanged from previous version)
// ─────────────────────────────────────────────────────────────────────────────

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

// ─────────────────────────────────────────────────────────────────────────────
// serverFlags — shared by the daemon worker invocation
// ─────────────────────────────────────────────────────────────────────────────

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

	pairingEnabled   bool
	pairingPort      int
	peeringAuthority string
	peerSecret       string
	peerCertDir      string

	// internal — set when this process is the background worker itself
	daemon bool

	// foreground skips the daemon fork and runs the server in the foreground.
	// Set automatically when running as PID 1 (e.g. inside a Docker container).
	foreground bool
}

// ─────────────────────────────────────────────────────────────────────────────
// serverCmd (notx server)
// ─────────────────────────────────────────────────────────────────────────────

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Manage the notx background server",
	Long: `Start and manage the notx server as a background daemon.

Running "notx server" (with no sub-command) starts the server in the
background and returns immediately.  The server process writes its PID to
~/.notx/server.pid and all output to ~/.notx/server.log.

Sub-commands:
  status          — show whether the server is running
  stop            — gracefully stop the running server
  restart         — stop then start the server
  logs            — print the server log (--tail to follow)
  pairing         — manage server-to-server pairing

Pairing sub-commands (notx server pairing ...):
  add-secret      — generate a new NTXP-... pairing secret (authority)

Examples:
  notx server                        # start in background
  notx server status
  notx server stop
  notx server restart
  notx server logs
  notx server logs --tail

  # Authority mode — enable pairing service + bootstrap listener
  notx server --pairing

  # Generate a secret for a joining server
  notx server pairing add-secret --label "dc-b" --data-dir /var/notx/data

  # Joining server mode — pair with an authority on first startup
  notx server --peer-authority grpc.authority.example.com:50052 \
              --peer-secret "NTXP-ABCDE-FGHIJ-KLMNO-PQRST-UVWXY-Z" \
              --peer-cert-dir /var/notx/certs
`,
	RunE: runServerStart,
}

func init() {
	fileCfg, _ := clientconfig.Load()
	f := serverCmd.Flags()

	f.BoolVar(&serverFlags.httpEnabled, "http", fileCfg.Server.EnableHTTP,
		"Enable the HTTP/JSON API server")
	f.BoolVar(&serverFlags.grpcEnabled, "grpc", fileCfg.Server.EnableGRPC,
		"Enable the gRPC server")

	httpPort := portFromAddr(fileCfg.Server.HTTPAddr, config.DefaultHTTPPort)
	grpcPort := portFromAddr(fileCfg.Server.GRPCAddr, config.DefaultGRPCPort)
	host := hostFromAddr(fileCfg.Server.HTTPAddr, "")

	f.IntVar(&serverFlags.httpPort, "http-port", httpPort,
		fmt.Sprintf("TCP port for the HTTP server (default from config: %d)", httpPort))
	f.IntVar(&serverFlags.grpcPort, "grpc-port", grpcPort,
		fmt.Sprintf("TCP port for the gRPC server (default from config: %d)", grpcPort))
	f.StringVar(&serverFlags.host, "host", host,
		"Bind address for both servers (default: all interfaces)")

	f.StringVar(&serverFlags.dataDir, "data-dir", fileCfg.Storage.DataDir,
		"Root directory for note files and the Badger index")

	f.StringVar(&serverFlags.tlsCert, "tls-cert", fileCfg.TLS.CertFile,
		"Path to the PEM-encoded server TLS certificate (leave empty to disable TLS)")
	f.StringVar(&serverFlags.tlsKey, "tls-key", fileCfg.TLS.KeyFile,
		"Path to the PEM-encoded server TLS private key")
	f.StringVar(&serverFlags.tlsCA, "tls-ca", fileCfg.TLS.CAFile,
		"Path to the PEM-encoded CA certificate for mTLS client verification")

	f.StringVar(&serverFlags.logLevel, "log-level", fileCfg.EffectiveLogLevel(fileCfg.Server.LogLevel),
		"Log verbosity: debug, info, warn, error")

	f.BoolVar(&serverFlags.deviceAutoApprove, "device-auto-approve", false,
		"Automatically approve newly registered devices")

	f.StringVar(&serverFlags.adminPassphrase, "admin-passphrase", "",
		"Passphrase required to register an admin device from a remote machine")

	// Hidden flag — used internally when this process is the daemon worker.
	f.BoolVar(&serverFlags.pairingEnabled, "pairing", false,
		"Enable the ServerPairingService on this instance (authority mode)")
	f.IntVar(&serverFlags.pairingPort, "pairing-port", 50052,
		"Bootstrap listener port for server pairing")
	f.StringVar(&serverFlags.peeringAuthority, "peer-authority", "",
		"Authority gRPC endpoint this server should pair with (joining server mode)")
	f.StringVar(&serverFlags.peerSecret, "peer-secret", "",
		"Pairing secret for initial registration (used once, then ignored)")
	f.StringVar(&serverFlags.peerCertDir, "peer-cert-dir", "",
		"Directory to store this server's client cert and key")

	f.BoolVar(&serverFlags.daemon, "daemon", false, "run as background worker (internal use)")
	f.MarkHidden("daemon") //nolint:errcheck

	f.BoolVar(&serverFlags.foreground, "foreground", false,
		"Run the server in the foreground instead of spawning a background daemon (useful in containers)")

	serverCmd.AddCommand(serverStatusCmd)
	serverCmd.AddCommand(serverStopCmd)
	serverCmd.AddCommand(serverRestartCmd)
	serverCmd.AddCommand(serverLogsCmd)

	rootCmd.AddCommand(serverCmd)
}

// runServerStart is called when the user runs `notx server` (no sub-command).
// If --daemon is set this process IS the worker; otherwise it forks a daemon.
// When --foreground is set, or when we detect we are running as PID 1 (i.e.
// inside a container), we skip the fork and run in the foreground directly.
func runServerStart(cmd *cobra.Command, args []string) error {
	if serverFlags.daemon {
		return runDaemonWorker(cmd, args)
	}
	if serverFlags.foreground || os.Getpid() == 1 {
		return runDaemonWorker(cmd, args)
	}
	return spawnDaemon(cmd)
}

// ─────────────────────────────────────────────────────────────────────────────
// Spawn helper — fork this binary as a background worker
// ─────────────────────────────────────────────────────────────────────────────

func spawnDaemon(cmd *cobra.Command) error {
	// If a server is already running, tell the user.
	if pid, err := readPID(); err == nil && processAlive(pid) {
		fmt.Fprintf(os.Stdout, "  \033[33m⚠\033[0m  notx server is already running (pid %d)\n", pid)
		fmt.Fprintf(os.Stdout, "       Run \033[1mnotx server status\033[0m for details.\n")
		return nil
	}

	// Ensure the ~/.notx directory exists.
	if err := os.MkdirAll(notxDir(), 0o700); err != nil {
		return fmt.Errorf("create notx dir: %w", err)
	}

	// Re-execute this binary with --daemon plus every flag the user passed.
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}

	// Build the argv for the child, forwarding only explicitly-changed flags.
	argv := []string{self, "server", "--daemon"}
	cmd.Flags().Visit(func(f *pflag.Flag) {
		if f.Name == "daemon" {
			return
		}
		argv = append(argv, "--"+f.Name, f.Value.String())
	})

	logFile, err := os.OpenFile(logFilePath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	defer logFile.Close()

	child := exec.Command(argv[0], argv[1:]...)
	child.Stdout = logFile
	child.Stderr = logFile
	child.Stdin = nil
	// Detach from the current process group so the child survives the parent.
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := child.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}

	pid := child.Process.Pid
	if err := writePID(pid); err != nil {
		// Non-fatal — server is still running.
		fmt.Fprintf(os.Stderr, "  \033[33m⚠\033[0m  Could not write pid file: %v\n", err)
	}

	// Detach — let the child run independently.
	child.Process.Release() //nolint:errcheck

	fmt.Fprintf(os.Stdout, "  \033[1;32m✓\033[0m  notx server started \033[2m(pid %d)\033[0m\n", pid)
	fmt.Fprintf(os.Stdout, "       Logs → \033[36m%s\033[0m\n", logFilePath())
	fmt.Fprintf(os.Stdout, "       Run \033[1mnotx server logs --tail\033[0m to follow output.\n")
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Daemon worker — the actual server process
// ─────────────────────────────────────────────────────────────────────────────

func runDaemonWorker(cmd *cobra.Command, args []string) error {
	if created, err := clientconfig.EnsureConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "  \033[33m⚠\033[0m  Could not write default config: %v\n", err)
	} else if created {
		path, _ := clientconfig.Path()
		fmt.Fprintf(os.Stdout, "\n  \033[1;32m✓\033[0m  First run — created default config at \033[36m%s\033[0m\n", path)
	}

	cfg := config.Default()
	if cmd.Flags().Changed("http") {
		cfg.EnableHTTP = serverFlags.httpEnabled
	}
	if cmd.Flags().Changed("grpc") {
		cfg.EnableGRPC = serverFlags.grpcEnabled
	}
	cfg.HTTPPort = serverFlags.httpPort
	cfg.GRPCPort = serverFlags.grpcPort
	cfg.Host = serverFlags.host
	cfg.DataDir = serverFlags.dataDir
	cfg.TLSCertFile = serverFlags.tlsCert
	cfg.TLSKeyFile = serverFlags.tlsKey
	cfg.TLSCAFile = serverFlags.tlsCA
	cfg.LogLevel = serverFlags.logLevel
	cfg.DeviceOnboarding.AutoApprove = serverFlags.deviceAutoApprove

	fileCfg, _ := clientconfig.Load()
	cfg.Admin.DeviceURN, cfg.Admin.OwnerURN = adminURNsFromConfig(fileCfg)

	if serverFlags.adminPassphrase != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(serverFlags.adminPassphrase), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("hash admin passphrase: %w", err)
		}
		cfg.Admin.AdminPassphraseHash = string(hash)
	}

	cfg.Pairing.Enabled = serverFlags.pairingEnabled
	cfg.Pairing.BootstrapPort = serverFlags.pairingPort
	cfg.Pairing.PeerAuthority = serverFlags.peeringAuthority
	cfg.Pairing.PeerSecret = serverFlags.peerSecret
	cfg.Pairing.PeerCertDir = serverFlags.peerCertDir

	if err := cfg.Validate(); err != nil {
		return err
	}

	log := buildLogger(cfg.LogLevel)
	log.Info("notx server starting",
		"http", cfg.EnableHTTP,
		"http_addr", cfg.HTTPAddr(),
		"grpc", cfg.EnableGRPC,
		"grpc_addr", cfg.GRPCAddr(),
		"authority_mode", cfg.Pairing.Enabled,
		"pairing_bootstrap_addr", cfg.PairingBootstrapAddr(),
		"data_dir", cfg.DataDir,
		"tls", cfg.TLSEnabled(),
		"mtls", cfg.MTLSEnabled(),
		"device_auto_approve", cfg.DeviceOnboarding.AutoApprove,
	)

	// Write the ports file so other commands (e.g. sync) can find the server.
	if err := writePorts(cfg.HTTPPort, cfg.GRPCPort, cfg.Pairing.PeerCertDir, cfg.Pairing.PeerAuthority); err != nil {
		log.Warn("could not write server.ports file", "err", err)
	}

	provider, err := sqlite.New(cfg.DataDir, nil)
	if err != nil {
		return fmt.Errorf("open sqlite provider at %q: %w", cfg.DataDir, err)
	}
	defer func() {
		if err := provider.Close(); err != nil {
			log.Warn("provider close error", "err", err)
		}
	}()

	srv, err := server.New(cfg, provider, provider, provider, provider, provider, provider, provider, provider, log, provider)
	if err != nil {
		return fmt.Errorf("build server: %w", err)
	}

	// Wire the sync bus back into the provider so AppendEvent notifies it.
	if srv.Bus() != nil {
		provider.SetSyncBus(srv.Bus())
	}

	runErr := srv.Run()

	// Clean up the PID and ports files when the server exits normally.
	removePID()
	removePorts()

	log.Info("notx server stopped")
	return runErr
}

// ─────────────────────────────────────────────────────────────────────────────
// notx server status
// ─────────────────────────────────────────────────────────────────────────────

var serverStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show whether the notx server is running",
	RunE: func(cmd *cobra.Command, args []string) error {
		pid, err := readPID()
		if err != nil {
			fmt.Fprintf(os.Stdout, "  \033[31m✗\033[0m  notx server is \033[1mnot running\033[0m\n")
			fmt.Fprintf(os.Stdout, "       Run \033[1mnotx server\033[0m to start it.\n")
			return nil
		}

		if !processAlive(pid) {
			fmt.Fprintf(os.Stdout, "  \033[31m✗\033[0m  notx server is \033[1mnot running\033[0m \033[2m(stale pid %d)\033[0m\n", pid)
			removePID()
			fmt.Fprintf(os.Stdout, "       Run \033[1mnotx server\033[0m to start it.\n")
			return nil
		}

		fmt.Fprintf(os.Stdout, "  \033[1;32m✓\033[0m  notx server is \033[1;32mrunning\033[0m \033[2m(pid %d)\033[0m\n", pid)
		fmt.Fprintf(os.Stdout, "       Log file → \033[36m%s\033[0m\n", logFilePath())
		if p, err := ReadServerPorts(); err == nil {
			fmt.Fprintf(os.Stdout, "       HTTP     → \033[36mhttp://localhost:%d\033[0m\n", p.HTTPPort)
			fmt.Fprintf(os.Stdout, "       gRPC     → \033[36mlocalhost:%d\033[0m\n", p.GRPCPort)
		}
		return nil
	},
}

// ─────────────────────────────────────────────────────────────────────────────
// notx server stop
// ─────────────────────────────────────────────────────────────────────────────

var serverStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Gracefully stop the running notx server",
	RunE: func(cmd *cobra.Command, args []string) error {
		pid, err := readPID()
		if err != nil {
			fmt.Fprintf(os.Stdout, "  \033[33m⚠\033[0m  notx server does not appear to be running.\n")
			return nil
		}

		if !processAlive(pid) {
			fmt.Fprintf(os.Stdout, "  \033[33m⚠\033[0m  notx server is not running \033[2m(stale pid %d — cleaned up)\033[0m\n", pid)
			removePID()
			return nil
		}

		proc, err := os.FindProcess(pid)
		if err != nil {
			return fmt.Errorf("find process %d: %w", pid, err)
		}

		fmt.Fprintf(os.Stdout, "  \033[33m→\033[0m  Sending SIGTERM to notx server \033[2m(pid %d)\033[0m…\n", pid)
		if err := proc.Signal(syscall.SIGTERM); err != nil {
			return fmt.Errorf("send SIGTERM to pid %d: %w", pid, err)
		}

		// Wait up to 10 s for the process to exit.
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if !processAlive(pid) {
				removePID()
				fmt.Fprintf(os.Stdout, "  \033[1;32m✓\033[0m  notx server stopped.\n")
				return nil
			}
			time.Sleep(200 * time.Millisecond)
		}

		// Escalate to SIGKILL.
		fmt.Fprintf(os.Stdout, "  \033[33m⚠\033[0m  Server did not stop in time — sending SIGKILL…\n")
		if err := proc.Signal(syscall.SIGKILL); err != nil {
			return fmt.Errorf("send SIGKILL to pid %d: %w", pid, err)
		}
		time.Sleep(500 * time.Millisecond)
		removePID()
		fmt.Fprintf(os.Stdout, "  \033[1;32m✓\033[0m  notx server killed.\n")
		return nil
	},
}

// ─────────────────────────────────────────────────────────────────────────────
// notx server restart
// ─────────────────────────────────────────────────────────────────────────────

var serverRestartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Stop then start the notx server",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Attempt stop (best-effort — server may not be running).
		if err := serverStopCmd.RunE(cmd, args); err != nil {
			fmt.Fprintf(os.Stderr, "  \033[33m⚠\033[0m  stop error (continuing): %v\n", err)
		}

		// Brief pause to let ports be released.
		time.Sleep(500 * time.Millisecond)

		fmt.Fprintf(os.Stdout, "  \033[33m→\033[0m  Starting notx server…\n")
		return spawnDaemon(serverCmd)
	},
}

// ─────────────────────────────────────────────────────────────────────────────
// notx server logs [--tail]
// ─────────────────────────────────────────────────────────────────────────────

var serverLogsFlags struct {
	tail bool
}

var serverLogsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Print the notx server log",
	Long: `Print the notx server log file (~/.notx/server.log).

Use --tail / -f to follow the log in real-time (like "tail -f").
Press Ctrl-C to stop following.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		logPath := logFilePath()

		f, err := os.Open(logPath)
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Fprintf(os.Stdout, "  \033[33m⚠\033[0m  No log file found at %s\n", logPath)
				fmt.Fprintf(os.Stdout, "       Run \033[1mnotx server\033[0m to start the server first.\n")
				return nil
			}
			return fmt.Errorf("open log file: %w", err)
		}
		defer f.Close()

		if !serverLogsFlags.tail {
			// Dump the whole file and exit.
			_, err := io.Copy(os.Stdout, f)
			return err
		}

		// --tail: print existing content then follow new writes.
		reader := bufio.NewReader(f)
		for {
			line, err := reader.ReadString('\n')
			if len(line) > 0 {
				fmt.Print(line)
			}
			if err != nil {
				if err == io.EOF {
					// No new data yet — sleep briefly and retry.
					time.Sleep(200 * time.Millisecond)
					continue
				}
				return err
			}
		}
	},
}

func init() {
	serverLogsCmd.Flags().BoolVarP(&serverLogsFlags.tail, "tail", "f", false,
		"Follow the log output in real-time (Ctrl-C to stop)")
}

// ─────────────────────────────────────────────────────────────────────────────
// notx server pairing — pairing management subcommands
// ─────────────────────────────────────────────────────────────────────────────

var pairingCmd = &cobra.Command{
	Use:   "pairing",
	Short: "Manage server-to-server pairing",
	Long: `Manage server-to-server pairing on this notx instance.

Use 'add-secret' to generate a registration token for a joining server.

The pairing service must be enabled with --pairing when starting the server.`,
}

var pairingAddSecretFlags struct {
	label   string
	ttl     time.Duration
	dataDir string
}

var pairingAddSecretCmd = &cobra.Command{
	Use:   "add-secret",
	Short: "Generate a new NTXP-... pairing secret",
	Long: `Generate a new pairing secret and print it once to stdout.
The plaintext is never stored — only the bcrypt hash is persisted.

The generated secret must be passed to the joining server via --peer-secret.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		dataDir := pairingAddSecretFlags.dataDir
		if dataDir == "" {
			dataDir = serverFlags.dataDir
		}
		if dataDir == "" {
			cfg, _ := clientconfig.Load()
			dataDir = cfg.Storage.DataDir
		}
		if dataDir == "" {
			dataDir = "./data"
		}

		secretsDir := filepath.Join(dataDir, "pairing_secrets")
		store, err := pairingsecret.NewFileSecretStore(secretsDir)
		if err != nil {
			return fmt.Errorf("open secret store: %w", err)
		}

		ttl := pairingAddSecretFlags.ttl
		if ttl == 0 {
			ttl = 15 * time.Minute
		}

		plaintext, record, err := pairingsecret.GenerateSecret(pairingAddSecretFlags.label, ttl)
		if err != nil {
			return fmt.Errorf("generate secret: %w", err)
		}
		if err := store.AddSecret(context.Background(), record); err != nil {
			return fmt.Errorf("store secret: %w", err)
		}

		fmt.Printf("\n  Pairing secret (copy this — it will NOT be shown again):\n\n")
		fmt.Printf("    %s\n\n", plaintext)
		fmt.Printf("  Label:   %s\n", record.LabelHint)
		fmt.Printf("  Expires: %s\n\n", record.ExpiresAt.Format(time.RFC3339))
		return nil
	},
}

func init() {
	pairingAddSecretCmd.Flags().StringVar(&pairingAddSecretFlags.label, "label", "", "Human-readable label for audit logs")
	pairingAddSecretCmd.Flags().DurationVar(&pairingAddSecretFlags.ttl, "ttl", 15*time.Minute, "How long the secret is valid")
	pairingAddSecretCmd.Flags().StringVar(&pairingAddSecretFlags.dataDir, "data-dir", "", "Root data directory (defaults to server --data-dir or config value)")

	pairingCmd.AddCommand(pairingAddSecretCmd)
	serverCmd.AddCommand(pairingCmd)
}

// ─────────────────────────────────────────────────────────────────────────────
// Address helpers (unchanged)
// ─────────────────────────────────────────────────────────────────────────────

func portFromAddr(addr string, fallback int) int {
	if addr == "" {
		return fallback
	}
	var port int
	if _, err := fmt.Sscanf(addr[len(addr)-5:], ":%d", &port); err != nil {
		if _, err2 := fmt.Sscanf(addr, ":%d", &port); err2 != nil {
			return fallback
		}
	}
	if port < 1 || port > 65535 {
		return fallback
	}
	return port
}

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
// Logger (unchanged)
// ─────────────────────────────────────────────────────────────────────────────

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
	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})
	return slog.New(handler)
}
