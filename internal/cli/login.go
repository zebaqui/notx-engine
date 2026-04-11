package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
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
