// Package notxctl implements the notxctl debug/ops CLI.
//
// notxctl is a thin command-line client that exposes every notx gRPC endpoint
// as a sub-command so operators can inspect and manipulate a running notx
// server directly — without going through the HTTP layer or the notx
// application CLI.
//
// Global connection flags (--addr, --insecure, --cert, --key, --ca) are
// resolved once at the root level and passed down to every sub-command via the
// rootContext type stored in cobra's context.
//
// Output is printed in one of three formats selected by --output / -o:
//
//	table  (default) — human-readable, aligned columns
//	json             — pretty-printed JSON
//	yaml             — YAML (via simple JSON→YAML conversion)
package notxctl

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/zebaqui/notx-engine/internal/buildinfo"
	"github.com/zebaqui/notx-engine/internal/grpcclient"
)

// ─────────────────────────────────────────────────────────────────────────────
// Global flag values (populated by cobra before any RunE fires)
// ─────────────────────────────────────────────────────────────────────────────

var globalFlags struct {
	addr     string
	insecure bool
	certFile string
	keyFile  string
	caFile   string
	output   string // "table" | "json"
	timeout  time.Duration
}

// ─────────────────────────────────────────────────────────────────────────────
// rootCtxKey — key for the *rootContext value stored in cobra's context
// ─────────────────────────────────────────────────────────────────────────────

type rootCtxKey struct{}

// rootContext is injected into every command's context so sub-commands can
// obtain a ready-to-use *grpcclient.Conn without re-parsing flags.
type rootContext struct {
	conn   *grpcclient.Conn
	output string
}

// connFromCtx retrieves the shared *grpcclient.Conn from cmd's context.
// It panics if called before the root PersistentPreRunE has run — which
// can only happen if a sub-command overrides PersistentPreRunE without
// calling parent hooks.
func connFromCtx(cmd *cobra.Command) *grpcclient.Conn {
	rc, ok := cmd.Context().Value(rootCtxKey{}).(*rootContext)
	if !ok || rc == nil {
		panic("notxctl: rootContext not found in command context — this is a bug")
	}
	return rc.conn
}

// outputFromCtx returns the --output format for the current invocation.
func outputFromCtx(cmd *cobra.Command) string {
	rc, ok := cmd.Context().Value(rootCtxKey{}).(*rootContext)
	if !ok || rc == nil {
		return "table"
	}
	return rc.output
}

// ─────────────────────────────────────────────────────────────────────────────
// Root command
// ─────────────────────────────────────────────────────────────────────────────

var rootCmd = &cobra.Command{
	Use:   "notxctl",
	Short: "notxctl — notx gRPC debug & ops CLI",
	Long: `notxctl is a direct gRPC client for notx servers.

Every notx service endpoint is exposed as a sub-command so you can inspect and
manipulate a running notx server without going through the HTTP API layer or
the notx application CLI.

CONNECTION FLAGS (apply to every sub-command):

  --addr       host:port of the notx gRPC server  (default: localhost:50051)
  --insecure   disable transport security (plaintext)
  --cert       path to PEM client certificate (mTLS)
  --key        path to PEM client private key  (mTLS)
  --ca         path to PEM CA certificate (custom CA / self-signed)

OUTPUT FLAGS:

  -o / --output   table (default) | json

EXAMPLES:

  # list all notes on a local dev server
  notxctl notes list

  # get a specific note as JSON
  notxctl notes get notx:note:… -o json

  # connect to a remote TLS server
  notxctl --addr grpc.example.com:50051 --ca /etc/notx/ca.crt notes list

  # mTLS
  notxctl --addr grpc.example.com:50051 \
          --cert /etc/notx/client.crt   \
          --key  /etc/notx/client.key   \
          --ca   /etc/notx/ca.crt       \
          pairing list

  # version
  notxctl version`,

	SilenceErrors: true,
	SilenceUsage:  true,

	// PersistentPreRunE fires before every leaf command and opens the gRPC
	// connection that all sub-commands share.
	PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
		// Skip connection setup for commands that don't need a server.
		if cmd.Name() == "version" || cmd.Name() == "help" {
			return nil
		}

		opts := grpcclient.Options{
			Addr:        globalFlags.addr,
			Insecure:    globalFlags.insecure,
			CertFile:    globalFlags.certFile,
			KeyFile:     globalFlags.keyFile,
			CAFile:      globalFlags.caFile,
			DialTimeout: globalFlags.timeout,
		}

		conn, err := grpcclient.Dial(opts)
		if err != nil {
			return fmt.Errorf("connect to %s: %w", globalFlags.addr, err)
		}

		out := strings.ToLower(strings.TrimSpace(globalFlags.output))
		if out != "json" && out != "table" {
			return fmt.Errorf("--output must be table or json, got %q", out)
		}

		rc := &rootContext{conn: conn, output: out}
		ctx := context.WithValue(cmd.Context(), rootCtxKey{}, rc)
		cmd.SetContext(ctx)
		return nil
	},

	// PersistentPostRunE closes the shared connection after the leaf command
	// finishes (success or failure).
	PersistentPostRunE: func(cmd *cobra.Command, _ []string) error {
		rc, ok := cmd.Context().Value(rootCtxKey{}).(*rootContext)
		if ok && rc != nil && rc.conn != nil {
			return rc.conn.Close()
		}
		return nil
	},
}

// Execute is the entry point called from main.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "\nerror: %v\n", err)
		os.Exit(1)
	}
}

func init() {
	f := rootCmd.PersistentFlags()

	f.StringVar(&globalFlags.addr, "addr", "localhost:50051",
		"notx gRPC server address (host:port)")
	f.BoolVar(&globalFlags.insecure, "insecure", false,
		"disable transport security (plaintext — dev only)")
	f.StringVar(&globalFlags.certFile, "cert", "",
		"path to PEM client certificate for mTLS")
	f.StringVar(&globalFlags.keyFile, "key", "",
		"path to PEM client private key for mTLS")
	f.StringVar(&globalFlags.caFile, "ca", "",
		"path to PEM CA certificate (overrides system roots)")
	f.StringVarP(&globalFlags.output, "output", "o", "table",
		"output format: table | json")
	f.DurationVar(&globalFlags.timeout, "timeout", 10*time.Second,
		"dial + per-RPC deadline")

	// Register all service command groups.
	rootCmd.AddCommand(notesCmd)
	rootCmd.AddCommand(devicesCmd)
	rootCmd.AddCommand(projectsCmd)
	rootCmd.AddCommand(foldersCmd)
	rootCmd.AddCommand(pairingCmd)
	rootCmd.AddCommand(versionCmd)
}

// ─────────────────────────────────────────────────────────────────────────────
// version command
// ─────────────────────────────────────────────────────────────────────────────

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print notxctl version information",
	Run: func(cmd *cobra.Command, _ []string) {
		fmt.Printf("notxctl %s (commit %s, built %s)\n",
			buildinfo.Version, buildinfo.Commit, buildinfo.BuildTime)
	},
}

// ─────────────────────────────────────────────────────────────────────────────
// Output helpers — shared by all sub-commands
// ─────────────────────────────────────────────────────────────────────────────

// printJSON pretty-prints v as JSON to stdout.
func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// newTabWriter returns a *tabwriter.Writer configured for aligned table output.
// Callers must call Flush() when done.
func newTabWriter() *tabwriter.Writer {
	return tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
}

// header prints a tab-separated column header line followed by a separator.
func header(tw *tabwriter.Writer, cols ...string) {
	fmt.Fprintln(tw, strings.Join(cols, "\t"))
	seps := make([]string, len(cols))
	for i, c := range cols {
		seps[i] = strings.Repeat("─", len(c))
	}
	fmt.Fprintln(tw, strings.Join(seps, "\t"))
}

// row writes a single tab-separated data row to tw.
func row(tw *tabwriter.Writer, vals ...string) {
	fmt.Fprintln(tw, strings.Join(vals, "\t"))
}

// fmtTime formats a time.Time into a compact human-readable string.
// Zero values are displayed as "—".
func fmtTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.UTC().Format("2006-01-02 15:04:05Z")
}

// fmtBool renders a bool as a short label.
func fmtBool(b bool, trueVal, falseVal string) string {
	if b {
		return trueVal
	}
	return falseVal
}

// shortURN returns the last 8 hex characters of the UUID segment of a notx URN.
// e.g. "urn:notx:note:1a9670dd-1a65-481a-ad17-03d77de021e5" → "1a9670dd-1a65-481a-ad17-03d77de021e5"
// Falls back to the full string if the URN is malformed.
func shortURN(urn string) string {
	parts := strings.Split(urn, ":")
	if len(parts) < 3 {
		return urn
	}
	uuid := parts[len(parts)-1]
	clean := strings.ReplaceAll(uuid, "-", "")
	if len(clean) > 12 {
		return "…" + clean[len(clean)-12:]
	}
	return clean
}

// rpcCtx returns a context with the per-RPC deadline set from --timeout.
func rpcCtx(cmd *cobra.Command) (context.Context, context.CancelFunc) {
	return context.WithTimeout(cmd.Context(), globalFlags.timeout)
}
