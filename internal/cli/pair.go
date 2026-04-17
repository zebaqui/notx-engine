package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/zebaqui/notx-engine/config"
	"github.com/zebaqui/notx-engine/internal/clientconfig"
)

// ─────────────────────────────────────────────────────────────────────────────
// notx pair
// ─────────────────────────────────────────────────────────────────────────────

var pairFlags struct {
	token  string
	server string
	api    string
}

var pairCmd = &cobra.Command{
	Use:   "pair",
	Short: "Pair this server with a remote notx authority",
	Long: `Register this notx server as a trusted peer of a remote authority.

The remote authority must have server pairing enabled (started with --pairing)
and a pairing secret must have been generated on it beforehand — either via
the admin UI ("Generate Secret" on the Servers page) or via:

  notx server pairing add-secret --label "my-node"

Pass the printed NTXP-... token to this command along with the authority's
bootstrap gRPC address (host:port, default port 50052):

  notx pair --token NTXP-434C9-D0628-B34B8-4EC89 \
            --server notx.zebaqui.com:50052

This command talks to the local notx server's HTTP API to perform the
outbound registration. By default it targets http://localhost:4060.
Override with --api if your local server runs on a different address:

  notx pair --token NTXP-... --server remote:50052 --api http://localhost:8080

On success the remote authority issues a signed certificate for this server.
The server URN and certificate expiry are printed to stdout.`,

	RunE: runPair,
}

func init() {
	fileCfg, _ := clientconfig.Load()

	apiAddr := fileCfg.Admin.APIAddr
	if apiAddr == "" {
		apiAddr = "http://localhost:4060"
	}

	f := pairCmd.Flags()
	f.StringVar(&pairFlags.token, "token", "",
		"NTXP-... pairing secret generated on the remote authority (required)")
	f.StringVar(&pairFlags.server, "server", "",
		"Bootstrap gRPC address of the remote authority, e.g. notx.example.com:50052 (required)")
	f.StringVar(&pairFlags.api, "api", apiAddr,
		fmt.Sprintf("Base URL of the local notx HTTP API (default from config: %s)", apiAddr))

	_ = pairCmd.MarkFlagRequired("token")
	_ = pairCmd.MarkFlagRequired("server")

	rootCmd.AddCommand(pairCmd)
}

// ─────────────────────────────────────────────────────────────────────────────
// Wire types — mirrors httpserver outboundPairRequest / outboundPairResponse
// ─────────────────────────────────────────────────────────────────────────────

type pairRequest struct {
	URL    string `json:"url"`
	Secret string `json:"secret"`
}

type pairResponse struct {
	ServerURN string    `json:"server_urn"`
	ExpiresAt time.Time `json:"expires_at"`
}

type pairErrorResponse struct {
	Error string `json:"error"`
}

// IsAlreadyPaired returns true when all three mTLS certificate files
// (server.crt, server.key, ca.crt) exist in peerCertDir.
func IsAlreadyPaired(peerCertDir string) bool {
	for _, f := range []string{"server.crt", "server.key", "ca.crt"} {
		if _, err := os.Stat(filepath.Join(peerCertDir, f)); err != nil {
			return false
		}
	}
	return true
}

// ─────────────────────────────────────────────────────────────────────────────
// runPair
// ─────────────────────────────────────────────────────────────────────────────

func runPair(cmd *cobra.Command, args []string) error {
	token := strings.TrimSpace(pairFlags.token)
	server := strings.TrimSpace(pairFlags.server)
	apiBase := strings.TrimRight(strings.TrimSpace(pairFlags.api), "/")

	// Strip any accidental http(s):// scheme from the gRPC server address —
	// the bootstrap listener speaks gRPC-over-TLS, not HTTP.
	if after, ok := strings.CutPrefix(server, "https://"); ok {
		server = after
	} else if after, ok := strings.CutPrefix(server, "http://"); ok {
		server = after
	}

	fmt.Fprintf(cmd.OutOrStdout(), "\n  \033[1;34m▶\033[0m  Pairing with remote authority\n")
	fmt.Fprintf(cmd.OutOrStdout(), "       Server  → \033[36m%s\033[0m\n", server)
	fmt.Fprintf(cmd.OutOrStdout(), "       API     → \033[36m%s\033[0m\n\n", apiBase)

	// Build request body.
	payload := pairRequest{
		URL:    server,
		Secret: token,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	// POST /v1/servers/outbound-pair on the local server.
	endpoint := apiBase + "/v1/servers/outbound-pair"
	req, err := http.NewRequestWithContext(cmd.Context(), http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Inject the local admin device URN so the request passes the device
	// auth middleware (same approach as the admin UI).
	fileCfg, _ := clientconfig.Load()
	deviceURN := fileCfg.Admin.AdminDeviceURN
	if deviceURN == "" {
		deviceURN = config.DefaultAdminDeviceURN
	}
	req.Header.Set("X-Device-ID", deviceURN)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf(
			"could not reach local notx server at %s\n"+
				"       Make sure the server is running (notx server) and try again.\n"+
				"       Error: %w",
			apiBase, err,
		)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode == http.StatusServiceUnavailable {
		return fmt.Errorf(
			"server pairing is not enabled on this instance.\n" +
				"       Restart the local server with the --pairing flag to enable it.",
		)
	}

	if resp.StatusCode != http.StatusCreated {
		// Try to extract a structured error message.
		var errResp pairErrorResponse
		if jsonErr := json.Unmarshal(respBody, &errResp); jsonErr == nil && errResp.Error != "" {
			return fmt.Errorf("pairing failed: %s", errResp.Error)
		}
		return fmt.Errorf("pairing failed: server returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var result pairResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "  \033[1;32m✓\033[0m  Paired successfully!\n\n")
	fmt.Fprintf(cmd.OutOrStdout(), "       Server URN  → \033[36m%s\033[0m\n", result.ServerURN)
	fmt.Fprintf(cmd.OutOrStdout(), "       Cert expiry → \033[36m%s\033[0m\n\n", result.ExpiresAt.Format("2006-01-02 15:04:05 UTC"))

	return nil
}
