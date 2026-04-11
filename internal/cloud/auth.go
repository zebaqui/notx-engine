package cloud

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/zebaqui/notx-engine/internal/clientconfig"
)

// AllClaims holds the decoded JWT payload fields useful for display.
type AllClaims struct {
	Sub            string `json:"sub"`
	Email          string `json:"email"`
	Username       string `json:"username"`
	OrganizationID string `json:"organization_id"`
	Exp            int64  `json:"exp"`
	RefreshToken   string `json:"rt"`
}

// CloudBaseURL returns the base URL for the notx cloud API.
// When NOTX_DEV=1 is set, it returns the local dev server URL.
func CloudBaseURL() string {
	// if os.Getenv("NOTX_DEV") == "1" {
	return "http://localhost:8080"
	// }
	// return "https://notx.zebaqui.com"
}

// WebBaseURL returns the base URL for the notx web frontend.
// When NOTX_DEV=1 is set, it returns the local dev frontend URL.
func WebBaseURL() string {
	// if os.Getenv("NOTX_DEV") == "1" {
	return "http://localhost:5173"
	// }
	// return "https://notx.zebaqui.com"
}

// EnsureToken returns a valid JWT for cloud mode, refreshing or triggering a
// browser login as needed. Any updates to token fields are persisted back to
// cfg and saved to disk.
//
// Priority:
//  1. Token present and not expired → return as-is.
//  2. Token present but expired     → refresh via refresh-token endpoint.
//  3. No token                      → open browser OAuth flow.
func EnsureToken(cfg *clientconfig.ClientJSON) (string, error) {
	token := cfg.Settings.CloudToken
	expiry := cfg.Settings.CloudTokenExpiry

	if token != "" {
		if expiry > time.Now().Unix() {
			// Token is still valid.
			return token, nil
		}
		// Token is present but expired — attempt a refresh.
		return RefreshToken(cfg)
	}

	// No token at all — run the interactive browser flow.
	return LoginWithBrowser(cfg)
}

// ForceLogin clears any stored token and unconditionally runs the browser login
// flow, regardless of whether a valid token already exists. Use this for
// `notx login` where the user explicitly wants to (re-)authenticate.
func ForceLogin(cfg *clientconfig.ClientJSON) (string, error) {
	// Wipe the stored token so EnsureToken won't short-circuit.
	cfg.Settings.CloudToken = ""
	cfg.Settings.CloudRefreshToken = ""
	cfg.Settings.CloudTokenExpiry = 0
	return LoginWithBrowser(cfg)
}

// ParseAllClaims decodes all displayable JWT claims without verifying the
// signature. Returns an error only when the token is not a valid JWT.
func ParseAllClaims(token string) (*AllClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("not a valid JWT")
	}

	payload := parts[1]
	decoded, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("base64-decode JWT payload: %w", err)
	}

	var c AllClaims
	if err := json.Unmarshal(decoded, &c); err != nil {
		return nil, fmt.Errorf("parse JWT claims: %w", err)
	}
	return &c, nil
}

// RefreshToken exchanges the stored (possibly expired) token for a fresh one
// by calling POST {CloudBaseURL()}/auth/refresh-token with the existing token
// as a Bearer credential. The cfg fields are updated in-place and saved.
func RefreshToken(cfg *clientconfig.ClientJSON) (string, error) {
	url := CloudBaseURL() + "/auth/refresh-token"

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, nil)
	if err != nil {
		return "", fmt.Errorf("cloud: build refresh request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Settings.CloudToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("cloud: refresh token request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("cloud: read refresh response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("cloud: refresh token failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var result struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("cloud: parse refresh response: %w", err)
	}
	if result.Token == "" {
		return "", fmt.Errorf("cloud: refresh response contained no token")
	}

	exp, rt, err := parseJWTClaims(result.Token)
	if err != nil {
		return "", fmt.Errorf("cloud: parse refreshed token claims: %w", err)
	}

	cfg.Settings.CloudToken = result.Token
	cfg.Settings.CloudTokenExpiry = exp
	cfg.Settings.CloudRefreshToken = rt

	if err := clientconfig.SaveClientJSON(cfg); err != nil {
		return "", fmt.Errorf("cloud: save refreshed token: %w", err)
	}

	return result.Token, nil
}

// LoginWithBrowser opens the notx web frontend in the user's default browser
// with a cli_callback query parameter pointing to a temporary local HTTP
// server. The server waits for a GET request carrying a ?token=<jwt> param,
// saves the token, and returns. The call blocks until authentication
// completes or the 5-minute timeout elapses.
func LoginWithBrowser(cfg *clientconfig.ClientJSON) (string, error) {
	// Pick a random available port.
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return "", fmt.Errorf("cloud: reserve callback port: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	callbackURL := fmt.Sprintf("http://localhost:%d", port)
	loginURL := fmt.Sprintf("%s/cli-login?cli_callback=%s", WebBaseURL(), callbackURL)

	fmt.Fprintf(os.Stderr, "\n  🔐  Login required\n     Opening: %s\n     Waiting for authentication...\n", loginURL)

	if err := openBrowser(loginURL); err != nil {
		// Non-fatal: user can open the URL manually.
		fmt.Fprintf(os.Stderr, "     (Could not open browser automatically: %v)\n", err)
	}

	// tokenCh carries the JWT after the response has been fully flushed.
	// doneCh is closed by the handler once it has finished writing, giving
	// the browser time to receive the complete page before we shut down.
	tokenCh := make(chan string, 1)
	doneCh := make(chan struct{})
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		tok := r.URL.Query().Get("token")
		if tok == "" {
			http.Error(w, "missing token parameter", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, successPage)

		// Flush the response to the browser before we signal completion.
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		// Signal: token received and response fully written.
		tokenCh <- tok
		close(doneCh)
	})

	srv := &http.Server{
		Handler:     mux,
		ReadTimeout: 10 * time.Second,
	}

	// Start serving on the pre-allocated listener.
	go func() {
		if serveErr := srv.Serve(listener); serveErr != nil && serveErr != http.ErrServerClosed {
			errCh <- serveErr
		}
	}()

	// Shutdown helper — waits for the handler to finish writing its response
	// (doneCh closed) before shutting down, so the browser always receives
	// the full success page. Falls through immediately if doneCh is already
	// closed or after a 2-second grace period, whichever comes first.
	shutdown := func() {
		select {
		case <-doneCh:
		case <-time.After(2 * time.Second):
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}

	select {
	case token := <-tokenCh:
		shutdown()

		exp, rt, err := parseJWTClaims(token)
		if err != nil {
			return "", fmt.Errorf("cloud: parse token claims: %w", err)
		}

		cfg.Settings.CloudToken = token
		cfg.Settings.CloudTokenExpiry = exp
		cfg.Settings.CloudRefreshToken = rt

		if err := clientconfig.SaveClientJSON(cfg); err != nil {
			return "", fmt.Errorf("cloud: save token: %w", err)
		}

		fmt.Fprintf(os.Stderr, "\n  ✓  Authenticated successfully\n\n")
		return token, nil

	case err := <-errCh:
		shutdown()
		return "", fmt.Errorf("cloud: callback server error: %w", err)

	case <-time.After(5 * time.Minute):
		shutdown()
		return "", fmt.Errorf("cloud: authentication timed out after 5 minutes")
	}
}

// openBrowser opens url in the system default browser using a platform-
// appropriate command.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default: // linux, bsd, …
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

// parseJWTClaims extracts the exp and rt claims from a JWT without verifying
// the signature. The middle (payload) segment is base64url-decoded and the
// JSON is parsed for the two fields of interest.
func parseJWTClaims(token string) (exp int64, refreshToken string, err error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return 0, "", fmt.Errorf("not a valid JWT (expected 3 parts, got %d)", len(parts))
	}

	// JWT uses base64url without padding; add padding as needed.
	payload := parts[1]
	switch len(payload) % 4 {
	case 2:
		payload += "=="
	case 3:
		payload += "="
	}

	decoded, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		// Some JWTs use RawURLEncoding (no padding at all).
		decoded, err = base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			return 0, "", fmt.Errorf("base64-decode JWT payload: %w", err)
		}
	}

	var claims struct {
		Exp          int64  `json:"exp"`
		RefreshToken string `json:"rt"`
	}
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return 0, "", fmt.Errorf("parse JWT claims: %w", err)
	}

	return claims.Exp, claims.RefreshToken, nil
}

// successPage is the HTML served to the browser after a successful login.
const successPage = `<!DOCTYPE html>
<html lang="en">
<head><meta charset="utf-8"><title>notx – authenticated</title></head>
<body style="font-family:sans-serif;padding:40px;max-width:480px;margin:0 auto">
  <h2>&#x2713; Authenticated</h2>
  <p>You can close this tab and return to your terminal.</p>
</body>
</html>
`
