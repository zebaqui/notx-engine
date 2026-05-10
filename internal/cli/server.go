package cli

import (
	"bufio"
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

	"github.com/zebaqui/notx-engine/config"
	"github.com/zebaqui/notx-engine/internal/clientconfig"
	"github.com/zebaqui/notx-engine/internal/server"
	"github.com/zebaqui/notx-engine/repo/sqlite"
	"github.com/zebaqui/notx-engine/snip"
	"github.com/zebaqui/notx-engine/snip/plugins/prompt"
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
// so that other commands can discover which ports the server is actually
// listening on, regardless of what flags were passed.
type serverPorts struct {
	HTTPPort int `json:"http_port"`
	GRPCPort int `json:"grpc_port"`
}

func writePorts(httpPort, grpcPort int) error {
	data, err := json.Marshal(serverPorts{
		HTTPPort: httpPort,
		GRPCPort: grpcPort,
	})
	if err != nil {
		return err
	}
	return os.WriteFile(portsFilePath(), data, 0o644)
}

// ReadServerPorts reads ~/.notx/server.ports and returns the ports the running
// server is listening on. Returns an error when the file does not exist (i.e.
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
// serverFlags — shared by the daemon worker invocation
// ─────────────────────────────────────────────────────────────────────────────

var serverFlags struct {
	httpEnabled bool
	grpcEnabled bool

	httpPort int
	grpcPort int
	host     string

	dataDir string

	logLevel string

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
background and returns immediately. The server process writes its PID to
~/.notx/server.pid and all output to ~/.notx/server.log.

Sub-commands:
  status   — show whether the server is running
  stop     — gracefully stop the running server
  restart  — stop then start the server
  logs     — print the server log (--tail to follow)

Examples:
  notx server                  # start in background
  notx server status
  notx server stop
  notx server restart
  notx server logs
  notx server logs --tail
  notx server --foreground     # run in foreground (container mode)
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
	host := hostFromAddr(fileCfg.Server.HTTPAddr, "127.0.0.1")

	f.IntVar(&serverFlags.httpPort, "http-port", httpPort,
		fmt.Sprintf("TCP port for the HTTP server (default from config: %d)", httpPort))
	f.IntVar(&serverFlags.grpcPort, "grpc-port", grpcPort,
		fmt.Sprintf("TCP port for the gRPC server (default from config: %d)", grpcPort))
	f.StringVar(&serverFlags.host, "host", host,
		"Bind address for both servers (default: 127.0.0.1)")

	f.StringVar(&serverFlags.dataDir, "data-dir", fileCfg.Storage.DataDir,
		"Root directory for note files and the SQLite index")

	f.StringVar(&serverFlags.logLevel, "log-level", fileCfg.EffectiveLogLevel(fileCfg.Server.LogLevel),
		"Log verbosity: debug, info, warn, error")

	// Hidden internal flag — set when this process is the daemon worker.
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

func runDaemonWorker(cmd *cobra.Command, _ []string) error {
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
	cfg.LogLevel = serverFlags.logLevel

	if err := cfg.Validate(); err != nil {
		return err
	}

	log := buildLogger(cfg.LogLevel)
	log.Info("notx server starting",
		"http", cfg.EnableHTTP,
		"http_addr", cfg.HTTPAddr(),
		"grpc", cfg.EnableGRPC,
		"grpc_addr", cfg.GRPCAddr(),
		"data_dir", cfg.DataDir,
	)

	// Write the ports file so other commands can find the server.
	if err := writePorts(cfg.HTTPPort, cfg.GRPCPort); err != nil {
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

	plugins := []snip.SnipPlugin{prompt.New()}
	srv, err := server.New(cfg, provider, provider, provider, provider, log, plugins, provider)
	if err != nil {
		return fmt.Errorf("build server: %w", err)
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
			fmt.Fprintf(os.Stdout, "       HTTP     → \033[36mhttp://127.0.0.1:%d\033[0m\n", p.HTTPPort)
			fmt.Fprintf(os.Stdout, "       gRPC     → \033[36m127.0.0.1:%d\033[0m\n", p.GRPCPort)
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
// Address helpers
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
// Logger
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
