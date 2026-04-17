package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/zebaqui/notx-engine/config"
	"github.com/zebaqui/notx-engine/internal/clientconfig"
	"github.com/zebaqui/notx-engine/internal/cloud"
)

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate with notx cloud",
	Long: `Open a browser to sign in to your notx cloud account.

When source is set to "cloud" in ~/.notx/client.json this command always
opens the browser login page — even if a token is already stored — letting
you switch accounts or re-authenticate after a revocation.

The JWT is written back to ~/.notx/client.json automatically on success.
Subsequent commands (notx <file>, notx pull, …) will reuse it until it
expires, at which point it is silently refreshed using the stored refresh
token.

If source is not "cloud" the command prints a hint and exits.
`,
	Args: cobra.NoArgs,
	RunE: runLogin,
}

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Clear the stored cloud credentials",
	Long: `Remove the JWT and refresh token stored in ~/.notx/client.json.

The next cloud command will require a fresh login.
`,
	Args: cobra.NoArgs,
	RunE: runLogout,
}

func init() {
	rootCmd.AddCommand(loginCmd)
	rootCmd.AddCommand(logoutCmd)
}

// ─────────────────────────────────────────────────────────────────────────────
// login
// ─────────────────────────────────────────────────────────────────────────────

func runLogin(_ *cobra.Command, _ []string) error {
	clientJSON, err := clientconfig.LoadClientJSON()
	if err != nil {
		return fmt.Errorf("login: load client.json: %w", err)
	}

	if clientJSON.Settings.Source != "cloud" {
		fmt.Fprintf(os.Stderr,
			"\n  \033[1;33m⚠\033[0m  source is %q — login is only used in cloud mode.\n"+
				"     Set  \"source\": \"cloud\"  in ~/.notx/client.json to switch.\n\n",
			clientJSON.Settings.Source,
		)
		return nil
	}

	token, err := cloud.ForceLogin(clientJSON)
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}

	// ── Print session info ────────────────────────────────────────────────────
	claims, err := cloud.ParseAllClaims(token)
	if err != nil {
		// Non-fatal — token works even if we can't display the claims.
		fmt.Printf("\n  \033[1;32m✓\033[0m  Logged in\n\n")
		autoPairAfterLogin(os.Stdout, token)
		return nil
	}

	fmt.Printf("\n  \033[1;32m✓\033[0m  Logged in\n")

	if claims.Email != "" {
		fmt.Printf("     email   : %s\n", claims.Email)
	}
	if claims.Username != "" {
		fmt.Printf("     user    : %s\n", claims.Username)
	}
	if claims.OrganizationID != "" {
		fmt.Printf("     org     : %s\n", claims.OrganizationID)
	}
	if claims.Exp > 0 {
		expiry := time.Unix(claims.Exp, 0)
		fmt.Printf("     expires : %s\n", expiry.Local().Format("2006-01-02 15:04 MST"))
	}
	fmt.Printf("     server  : %s\n\n", cloud.CloudBaseURL())

	autoPairAfterLogin(os.Stdout, token)

	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// logout
// ─────────────────────────────────────────────────────────────────────────────

func runLogout(_ *cobra.Command, _ []string) error {
	clientJSON, err := clientconfig.LoadClientJSON()
	if err != nil {
		return fmt.Errorf("logout: load client.json: %w", err)
	}

	if clientJSON.Settings.Source != "cloud" {
		fmt.Fprintf(os.Stderr,
			"\n  \033[1;33m⚠\033[0m  source is %q — logout is only used in cloud mode.\n\n",
			clientJSON.Settings.Source,
		)
		return nil
	}

	if clientJSON.Settings.CloudToken == "" {
		fmt.Fprintf(os.Stderr, "\n  \033[2m–\033[0m  No cloud session to clear.\n\n")
		return nil
	}

	// Capture email before wiping so the confirmation message is personal.
	email := ""
	if claims, err := cloud.ParseAllClaims(clientJSON.Settings.CloudToken); err == nil {
		email = claims.Email
	}

	clientJSON.Settings.CloudToken = ""
	clientJSON.Settings.CloudRefreshToken = ""
	clientJSON.Settings.CloudTokenExpiry = 0

	if err := clientconfig.SaveClientJSON(clientJSON); err != nil {
		return fmt.Errorf("logout: save client.json: %w", err)
	}

	if email != "" {
		fmt.Printf("\n  \033[1;32m✓\033[0m  Logged out  \033[2m(%s)\033[0m\n\n", email)
	} else {
		fmt.Printf("\n  \033[1;32m✓\033[0m  Logged out\n\n")
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// auto-pair after login
// ─────────────────────────────────────────────────────────────────────────────

// autoPairAfterLogin attempts to automatically pair with the cloud after a
// successful login. All errors are non-fatal and printed as warnings.
func autoPairAfterLogin(out io.Writer, token string) {
	// 1. Get pairing info from cloud.
	info, err := cloud.GetPairingInfo()
	if err != nil {
		fmt.Fprintf(out, "  \033[2m–\033[0m  Skipping auto-pair: could not fetch pairing info (%v)\n\n", err)
		return
	}

	// 2. Determine cert dir: from server.ports if running, else default ~/.notx/certs.
	certDir := ""
	ports, portsErr := ReadServerPorts()
	if portsErr == nil && ports.PeerCertDir != "" {
		certDir = ports.PeerCertDir
	}
	if certDir == "" {
		home, _ := os.UserHomeDir()
		certDir = filepath.Join(home, ".notx", "certs")
	}

	// 3. Already paired? Skip.
	if IsAlreadyPaired(certDir) {
		fmt.Fprintf(out, "  \033[2m✓\033[0m  Already paired with cloud\n\n")
		return
	}

	// 4. Check if local server is running.
	if portsErr != nil || ports.HTTPPort == 0 {
		fmt.Fprintf(out, "  \033[33m⚠\033[0m  Local server not running — start it with:\n")
		fmt.Fprintf(out, "       notx server --peer-cert-dir %s\n\n", certDir)
		return
	}

	// 5. Create pairing secret on cloud.
	secret, err := cloud.CreatePairingSecret(token, "auto-pair")
	if err != nil {
		fmt.Fprintf(out, "  \033[33m⚠\033[0m  Could not create pairing secret: %v\n\n", err)
		return
	}

	// 6. Build the authority address from cloud hostname + bootstrap port.
	authorityAddr := buildAuthorityAddr(cloud.CloudBaseURL(), info.BootstrapAddr)

	// 7. POST /v1/servers/outbound-pair on the local server.
	apiBase := fmt.Sprintf("http://localhost:%d", ports.HTTPPort)

	fileCfg, _ := clientconfig.Load()
	deviceURN := fileCfg.Admin.AdminDeviceURN
	if deviceURN == "" {
		deviceURN = config.DefaultAdminDeviceURN
	}

	fmt.Fprintf(out, "  \033[1;34m▶\033[0m  Pairing with cloud...\n")
	fmt.Fprintf(out, "       authority → \033[36m%s\033[0m\n", authorityAddr)
	fmt.Fprintf(out, "       cert dir  → \033[36m%s\033[0m\n\n", certDir)

	// outboundPairRequest mirrors http/pairing.go's outboundPairRequest —
	// the extra fields let the local server use the CA fingerprint and cert
	// dir supplied here rather than requiring them as startup flags.
	type outboundPairRequest struct {
		URL           string `json:"url"`
		Secret        string `json:"secret"`
		CAFingerprint string `json:"ca_fingerprint"`
		CertDir       string `json:"cert_dir"`
	}
	pairReq := outboundPairRequest{
		URL:           authorityAddr,
		Secret:        secret.Plaintext,
		CAFingerprint: info.CAFingerprint,
		CertDir:       certDir,
	}

	body, err := json.Marshal(pairReq)
	if err != nil {
		fmt.Fprintf(out, "  \033[33m⚠\033[0m  Could not marshal pair request: %v\n\n", err)
		return
	}

	endpoint := apiBase + "/v1/servers/outbound-pair"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(out, "  \033[33m⚠\033[0m  Could not build pair request: %v\n\n", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Device-ID", deviceURN)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(out, "  \033[33m⚠\033[0m  Could not reach local server: %v\n\n", err)
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(out, "  \033[33m⚠\033[0m  Could not read pair response: %v\n\n", err)
		return
	}

	if resp.StatusCode == http.StatusServiceUnavailable {
		fmt.Fprintf(out, "  \033[33m⚠\033[0m  Server pairing not enabled — restart with --pairing flag\n\n")
		return
	}

	if resp.StatusCode != http.StatusCreated {
		var errResp pairErrorResponse
		if jsonErr := json.Unmarshal(respBody, &errResp); jsonErr == nil && errResp.Error != "" {
			fmt.Fprintf(out, "  \033[33m⚠\033[0m  Pairing failed: %s\n\n", errResp.Error)
			return
		}
		fmt.Fprintf(out, "  \033[33m⚠\033[0m  Pairing failed: HTTP %d: %s\n\n", resp.StatusCode, strings.TrimSpace(string(respBody)))
		return
	}

	var result pairResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		fmt.Fprintf(out, "  \033[33m⚠\033[0m  Could not parse pair response: %v\n\n", err)
		return
	}

	fmt.Fprintf(out, "  \033[1;32m✓\033[0m  Paired with cloud!\n\n")
	fmt.Fprintf(out, "       Server URN  → \033[36m%s\033[0m\n", result.ServerURN)
	fmt.Fprintf(out, "       Cert expiry → \033[36m%s\033[0m\n\n", result.ExpiresAt.Format("2006-01-02 15:04:05 UTC"))

	// 8. Save PeerCertDir + PeerAuthority to clientconfig.
	fileCfg2, _ := clientconfig.Load()
	fileCfg2.Client.PeerCertDir = certDir
	fileCfg2.Client.PeerAuthority = buildPrimaryAddr(cloud.CloudBaseURL(), info.PrimaryAddr)
	_ = clientconfig.Save(fileCfg2)
}

// buildAuthorityAddr combines the host part of cloudBaseURL with the port from
// addrField. If addrField already contains a host (no leading ':'), it is
// returned as-is. If addrField is just a port like ":50052", the cloud host is
// prepended.
func buildAuthorityAddr(cloudBaseURL, addrField string) string {
	return buildAddr(cloudBaseURL, addrField)
}

// buildPrimaryAddr is the same as buildAuthorityAddr but for the primary gRPC
// listener address.
func buildPrimaryAddr(cloudBaseURL, addrField string) string {
	return buildAddr(cloudBaseURL, addrField)
}

// buildAddr extracts the host from cloudBaseURL and the port from addrField,
// combining them into host:port. If addrField already contains a non-empty
// host component (i.e. does not start with ':'), it is returned unchanged.
func buildAddr(cloudBaseURL, addrField string) string {
	// If addrField has a host already (does not start with ':'), use as-is.
	if addrField != "" && !strings.HasPrefix(addrField, ":") {
		return addrField
	}

	// Extract port from addrField (strip leading ':').
	port := strings.TrimPrefix(addrField, ":")

	// Extract host from cloudBaseURL.
	host := cloudHost(cloudBaseURL)

	if port == "" {
		return host
	}
	return host + ":" + port
}

// cloudHost extracts the bare hostname (no scheme, no path, no port) from a
// base URL like "https://notx.zebaqui.com" or "http://localhost:8080".
func cloudHost(rawURL string) string {
	// Use net/url to parse properly.
	u, err := url.Parse(rawURL)
	if err != nil {
		// Fallback: strip scheme manually.
		s := rawURL
		if after, ok := strings.CutPrefix(s, "https://"); ok {
			s = after
		} else if after, ok := strings.CutPrefix(s, "http://"); ok {
			s = after
		}
		// Remove path.
		if idx := strings.IndexByte(s, '/'); idx >= 0 {
			s = s[:idx]
		}
		// Remove port.
		if idx := strings.LastIndexByte(s, ':'); idx >= 0 {
			s = s[:idx]
		}
		return s
	}
	return u.Hostname()
}
