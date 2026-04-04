package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zebaqui/notx-engine/internal/clientconfig"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "View and edit notx configuration",
	Long: `Interactively view and update the notx configuration file.

The configuration is stored at ~/.notx/config.json and controls both the
CLI client (which server to dial) and the server/admin defaults.

Running with no sub-command starts the interactive editor:

  notx config

Sub-commands:
  notx config show    — print the current config and its file path
  notx config reset   — overwrite the config with built-in defaults (config.json)
`,
	RunE: runConfigInteractive,
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Print the current configuration",
	RunE:  runConfigShow,
}

var configResetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Reset the configuration to built-in defaults",
	RunE:  runConfigReset,
}

func init() {
	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configResetCmd)
	rootCmd.AddCommand(configCmd)
}

// ─────────────────────────────────────────────────────────────────────────────
// show
// ─────────────────────────────────────────────────────────────────────────────

func runConfigShow(cmd *cobra.Command, args []string) error {
	cfg, err := clientconfig.Load()
	if err != nil {
		return err
	}

	path, _ := clientconfig.Path()

	printHeader("Current configuration")
	fmt.Printf("  File: %s\n\n", path)
	printConfigTable(cfg)
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// reset
// ─────────────────────────────────────────────────────────────────────────────

func runConfigReset(cmd *cobra.Command, args []string) error {
	path, _ := clientconfig.Path()

	fmt.Printf("  This will overwrite %s with built-in defaults.\n", path)
	if !confirm("  Continue? [y/N] ") {
		fmt.Println("  Aborted.")
		return nil
	}

	if err := clientconfig.Save(clientconfig.Default()); err != nil {
		return err
	}

	fmt.Printf("\n  \033[1;32m✓\033[0m  Config reset → %s\n\n", path)
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// interactive editor
// ─────────────────────────────────────────────────────────────────────────────

func runConfigInteractive(cmd *cobra.Command, args []string) error {
	cfg, err := clientconfig.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	path, _ := clientconfig.Path()

	printHeader("notx config")
	fmt.Printf("  File: %s\n", path)
	fmt.Println("  Press Enter to keep the current value. Ctrl-C to abort.")

	sc := bufio.NewScanner(os.Stdin)

	// ── Client ───────────────────────────────────────────────────────────────
	printSection("Client  (CLI → server connection)")

	cfg.Client.GRPCAddr = promptString(sc,
		"  gRPC server address",
		cfg.Client.GRPCAddr,
		"host:port the CLI dials for gRPC calls (e.g. localhost:50051)",
	)

	cfg.Client.Namespace = promptString(sc,
		"  URN namespace",
		cfg.Client.Namespace,
		"namespace used when generating note URNs (e.g. notx, myorg)",
	)

	cfg.Client.Insecure = promptBool(sc,
		"  Insecure (skip TLS verification)",
		cfg.Client.Insecure,
		"set true for development servers without TLS",
	)

	// ── Server ───────────────────────────────────────────────────────────────
	printSection("Server  (notx server listeners)")

	cfg.Server.GRPCAddr = promptString(sc,
		"  gRPC bind address",
		cfg.Server.GRPCAddr,
		"host:port the gRPC server listens on",
	)

	cfg.Server.HTTPAddr = promptString(sc,
		"  HTTP bind address",
		cfg.Server.HTTPAddr,
		"host:port the HTTP/JSON API server listens on",
	)

	cfg.Server.EnableHTTP = promptBool(sc,
		"  Enable HTTP",
		cfg.Server.EnableHTTP,
		"enable the HTTP/JSON API layer",
	)

	cfg.Server.EnableGRPC = promptBool(sc,
		"  Enable gRPC",
		cfg.Server.EnableGRPC,
		"enable the gRPC layer",
	)

	cfg.Server.ShutdownTimeoutSec = promptInt(sc,
		"  Shutdown timeout (seconds)",
		cfg.Server.ShutdownTimeoutSec,
		"graceful-shutdown window",
	)

	// ── Admin ─────────────────────────────────────────────────────────────────
	printSection("Admin UI  (embedded dashboard)")

	cfg.Admin.Addr = promptString(sc,
		"  Admin bind address",
		cfg.Admin.Addr,
		"host:port the admin UI server listens on",
	)

	cfg.Admin.APIAddr = promptString(sc,
		"  API proxy target",
		cfg.Admin.APIAddr,
		"base URL the admin UI proxies API calls to (e.g. http://localhost:4060)",
	)

	// ── Storage ───────────────────────────────────────────────────────────────
	printSection("Storage")

	cfg.Storage.DataDir = promptString(sc,
		"  Data directory",
		cfg.Storage.DataDir,
		"root directory for note files and the Badger index",
	)

	// ── TLS ───────────────────────────────────────────────────────────────────
	printSection("TLS  (leave blank to disable)")

	cfg.TLS.CertFile = promptStringAllowEmpty(sc,
		"  TLS cert file",
		cfg.TLS.CertFile,
		"path to PEM-encoded server certificate",
	)

	cfg.TLS.KeyFile = promptStringAllowEmpty(sc,
		"  TLS key file",
		cfg.TLS.KeyFile,
		"path to PEM-encoded server private key",
	)

	cfg.TLS.CAFile = promptStringAllowEmpty(sc,
		"  TLS CA file (mTLS)",
		cfg.TLS.CAFile,
		"path to PEM-encoded CA cert for mutual TLS — leave blank to skip",
	)

	// ── Logging ───────────────────────────────────────────────────────────────
	printSection("Logging")

	cfg.Log.Level = promptEnum(sc,
		"  Log level",
		cfg.Log.Level,
		[]string{"debug", "info", "warn", "error"},
	)

	// ── Save ──────────────────────────────────────────────────────────────────
	fmt.Println()
	printSection("Summary")
	printConfigTable(cfg)

	if !confirm("  Save these settings? [Y/n] ") {
		fmt.Println("  Aborted — no changes written.")
		return nil
	}

	if err := clientconfig.Save(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Printf("\n  \033[1;32m✓\033[0m  Saved → %s\n\n", path)
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Prompt helpers
// ─────────────────────────────────────────────────────────────────────────────

func promptString(sc *bufio.Scanner, label, current, hint string) string {
	fmt.Printf("\n  \033[1m%s\033[0m\n", label)
	if hint != "" {
		fmt.Printf("  \033[2m%s\033[0m\n", hint)
	}
	fmt.Printf("  Current: \033[36m%s\033[0m\n", current)
	fmt.Print("  New value: ")

	if sc.Scan() {
		if v := strings.TrimSpace(sc.Text()); v != "" {
			return v
		}
	}
	return current
}

// promptStringAllowEmpty is like promptString but an explicit empty input
// clears the field (useful for optional TLS paths).
func promptStringAllowEmpty(sc *bufio.Scanner, label, current, hint string) string {
	fmt.Printf("\n  \033[1m%s\033[0m\n", label)
	if hint != "" {
		fmt.Printf("  \033[2m%s\033[0m\n", hint)
	}
	display := current
	if display == "" {
		display = "(none)"
	}
	fmt.Printf("  Current: \033[36m%s\033[0m\n", display)
	fmt.Print("  New value (Enter = keep, \"-\" = clear): ")

	if sc.Scan() {
		v := strings.TrimSpace(sc.Text())
		switch v {
		case "":
			return current
		case "-":
			return ""
		default:
			return v
		}
	}
	return current
}

func promptBool(sc *bufio.Scanner, label string, current bool, hint string) bool {
	cur := "false"
	if current {
		cur = "true"
	}
	fmt.Printf("\n  \033[1m%s\033[0m\n", label)
	if hint != "" {
		fmt.Printf("  \033[2m%s\033[0m\n", hint)
	}
	fmt.Printf("  Current: \033[36m%s\033[0m\n", cur)
	fmt.Print("  New value [true/false]: ")

	if sc.Scan() {
		switch strings.TrimSpace(strings.ToLower(sc.Text())) {
		case "true", "yes", "1", "y":
			return true
		case "false", "no", "0", "n":
			return false
		}
	}
	return current
}

func promptInt(sc *bufio.Scanner, label string, current int, hint string) int {
	fmt.Printf("\n  \033[1m%s\033[0m\n", label)
	if hint != "" {
		fmt.Printf("  \033[2m%s\033[0m\n", hint)
	}
	fmt.Printf("  Current: \033[36m%d\033[0m\n", current)
	fmt.Print("  New value: ")

	if sc.Scan() {
		v := strings.TrimSpace(sc.Text())
		if v == "" {
			return current
		}
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
			return n
		}
		fmt.Printf("  \033[33m  Invalid number — keeping %d\033[0m\n", current)
	}
	return current
}

func promptEnum(sc *bufio.Scanner, label, current string, choices []string) string {
	fmt.Printf("\n  \033[1m%s\033[0m\n", label)
	fmt.Printf("  \033[2mChoices: %s\033[0m\n", strings.Join(choices, ", "))
	fmt.Printf("  Current: \033[36m%s\033[0m\n", current)
	fmt.Print("  New value: ")

	if sc.Scan() {
		v := strings.TrimSpace(strings.ToLower(sc.Text()))
		if v == "" {
			return current
		}
		for _, c := range choices {
			if v == c {
				return v
			}
		}
		fmt.Printf("  \033[33m  Invalid choice — keeping %q\033[0m\n", current)
	}
	return current
}

func confirm(prompt string) bool {
	fmt.Print(prompt)
	sc := bufio.NewScanner(os.Stdin)
	if sc.Scan() {
		v := strings.TrimSpace(strings.ToLower(sc.Text()))
		// Default yes for "Save?" prompt (empty = Y), default no for "Continue?" (empty = N)
		if strings.HasSuffix(strings.TrimSpace(prompt), "[Y/n] ") {
			return v == "" || v == "y" || v == "yes"
		}
		return v == "y" || v == "yes"
	}
	return false
}

// ─────────────────────────────────────────────────────────────────────────────
// Display helpers
// ─────────────────────────────────────────────────────────────────────────────

func printHeader(title string) {
	fmt.Printf("\n  \033[1;34m▶\033[0m  \033[1m%s\033[0m\n", title)
}

func printSection(title string) {
	fmt.Printf("\n  \033[1;2m── %s\033[0m\n", title)
}

func kv(label, value string) {
	fmt.Printf("    %-32s \033[36m%s\033[0m\n", label, value)
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func printConfigTable(cfg *clientconfig.Config) {
	printSection("Client")
	kv("grpc_addr", cfg.Client.GRPCAddr)
	kv("namespace", cfg.Client.Namespace)
	kv("insecure", boolStr(cfg.Client.Insecure))

	printSection("Server")
	kv("grpc_addr", cfg.Server.GRPCAddr)
	kv("http_addr", cfg.Server.HTTPAddr)
	kv("enable_grpc", boolStr(cfg.Server.EnableGRPC))
	kv("enable_http", boolStr(cfg.Server.EnableHTTP))
	kv("shutdown_timeout_sec", fmt.Sprintf("%d", cfg.Server.ShutdownTimeoutSec))

	printSection("Admin")
	kv("addr", cfg.Admin.Addr)
	kv("api_addr", cfg.Admin.APIAddr)

	printSection("Storage")
	kv("data_dir", cfg.Storage.DataDir)

	printSection("TLS")
	if cfg.TLS.CertFile != "" {
		kv("cert_file", cfg.TLS.CertFile)
		kv("key_file", cfg.TLS.KeyFile)
	} else {
		kv("tls", "(disabled)")
	}
	if cfg.TLS.CAFile != "" {
		kv("ca_file (mTLS)", cfg.TLS.CAFile)
	}

	printSection("Log")
	kv("level", cfg.Log.Level)

	fmt.Println()
}
