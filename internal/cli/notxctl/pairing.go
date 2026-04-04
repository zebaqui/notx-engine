package notxctl

import (
	"encoding/base64"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	pb "github.com/zebaqui/notx-engine/proto"
)

// ─────────────────────────────────────────────────────────────────────────────
// pairing — top-level group
// ─────────────────────────────────────────────────────────────────────────────

var pairingCmd = &cobra.Command{
	Use:   "pairing",
	Short: "Manage server-to-server pairing (ServerPairingService)",
	Long: `Commands for the ServerPairingService gRPC endpoints.

Sub-commands:
  list      ListServers        — list all registered peer servers
  revoke    RevokeServer       — permanently revoke a peer server's certificate
  ca        GetCACertificate   — print the authority CA certificate in PEM format
  register  RegisterServer     — register this server with an authority (bootstrap port)
  renew     RenewCertificate   — renew the current server certificate (primary mTLS port)

CONNECTION NOTES:
  'list', 'revoke', 'renew' require the primary mTLS port (default :50051).
  'register' targets the bootstrap port (default :50052) — use --addr to override.
  'ca' is unauthenticated and available on both ports.

EXAMPLES:
  # list all peer servers including revoked ones
  notxctl pairing list --revoked

  # revoke a peer server immediately
  notxctl pairing revoke notx:srv:…

  # print the authority CA cert (PEM)
  notxctl pairing ca

  # register this server with an authority (bootstrap flow)
  notxctl --addr authority.example.com:50052 --insecure \
      pairing register \
      --urn  notx:srv:… \
      --name "dc-b replica" \
      --endpoint grpc.dc-b.example.com:50051 \
      --secret NTXP-AAAAA-BBBBB-CCCCC-DDDDD-EEEEE`,
}

func init() {
	pairingCmd.AddCommand(pairingListCmd)
	pairingCmd.AddCommand(pairingRevokeCmd)
	pairingCmd.AddCommand(pairingCACmd)
	pairingCmd.AddCommand(pairingRegisterCmd)
	pairingCmd.AddCommand(pairingRenewCmd)
}

// ─────────────────────────────────────────────────────────────────────────────
// pairing list
// ─────────────────────────────────────────────────────────────────────────────

var pairingListFlags struct {
	includeRevoked bool
}

var pairingListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all registered peer servers",
	Long: `Calls ListServers and prints a table of registered peer servers.

By default only active (non-revoked) servers are shown.
Pass --revoked to include revoked servers in the output.

Examples:
  notxctl pairing list
  notxctl pairing list --revoked
  notxctl pairing list -o json`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		resp, err := conn.Pairing().ListServers(ctx, &pb.ListServersRequest{
			IncludeRevoked: pairingListFlags.includeRevoked,
		})
		if err != nil {
			return fmt.Errorf("ListServers: %w", err)
		}

		switch outputFromCtx(cmd) {
		case "json":
			type serverOut struct {
				URN          string    `json:"server_urn"`
				Name         string    `json:"server_name"`
				Endpoint     string    `json:"endpoint"`
				Revoked      bool      `json:"revoked"`
				RegisteredAt time.Time `json:"registered_at"`
				ExpiresAt    time.Time `json:"expires_at"`
				LastSeenAt   time.Time `json:"last_seen_at,omitempty"`
			}
			var servers []serverOut
			for _, s := range resp.Servers {
				o := serverOut{
					URN:          s.ServerUrn,
					Name:         s.ServerName,
					Endpoint:     s.Endpoint,
					Revoked:      s.Revoked,
					RegisteredAt: s.RegisteredAt.AsTime(),
					ExpiresAt:    s.ExpiresAt.AsTime(),
				}
				if s.LastSeenAt != nil {
					o.LastSeenAt = s.LastSeenAt.AsTime()
				}
				servers = append(servers, o)
			}
			return printJSON(map[string]any{"servers": servers})

		default:
			tw := newTabWriter()
			defer tw.Flush()
			header(tw, "URN", "NAME", "ENDPOINT", "STATUS", "EXPIRES", "LAST SEEN")
			for _, s := range resp.Servers {
				status := "active"
				if s.Revoked {
					status = "revoked"
				}
				lastSeen := "—"
				if s.LastSeenAt != nil && !s.LastSeenAt.AsTime().IsZero() {
					lastSeen = fmtTime(s.LastSeenAt.AsTime())
				}
				row(tw,
					shortURN(s.ServerUrn),
					s.ServerName,
					s.Endpoint,
					status,
					fmtTime(s.ExpiresAt.AsTime()),
					lastSeen,
				)
			}
			fmt.Printf("\ntotal: %d server(s)\n", len(resp.Servers))
		}
		return nil
	},
}

func init() {
	pairingListCmd.Flags().BoolVar(&pairingListFlags.includeRevoked, "revoked", false,
		"include revoked servers in the output")
}

// ─────────────────────────────────────────────────────────────────────────────
// pairing revoke <urn>
// ─────────────────────────────────────────────────────────────────────────────

var pairingRevokeCmd = &cobra.Command{
	Use:   "revoke <urn>",
	Short: "Permanently revoke a peer server's certificate",
	Long: `Calls RevokeServer to immediately and permanently invalidate a peer server.

The server's TLS certificate is added to the in-memory deny-set on the
authority; subsequent handshakes from that server will be rejected without
waiting for a restart.

This operation is irreversible. The peer server must re-register (obtaining
a new pairing secret from the authority admin) to reconnect.

Example:
  notxctl pairing revoke notx:srv:1a9670dd-1a65-481a-ad17-03d77de021e5`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		resp, err := conn.Pairing().RevokeServer(ctx, &pb.RevokeServerRequest{
			ServerUrn: args[0],
		})
		if err != nil {
			return fmt.Errorf("RevokeServer: %w", err)
		}

		switch outputFromCtx(cmd) {
		case "json":
			return printJSON(map[string]any{
				"server_urn": args[0],
				"revoked":    resp.Revoked,
			})
		default:
			fmt.Printf("revoked  %s\n", args[0])
		}
		return nil
	},
}

// ─────────────────────────────────────────────────────────────────────────────
// pairing ca
// ─────────────────────────────────────────────────────────────────────────────

var pairingCAFlags struct {
	outFile string
}

var pairingCACmd = &cobra.Command{
	Use:   "ca",
	Short: "Print (or save) the authority CA certificate in PEM format",
	Long: `Calls GetCACertificate (unauthenticated) and prints the authority CA cert.

This endpoint is available on both the primary (:50051) and bootstrap
(:50052) listeners.

Use --out to write the PEM directly to a file instead of stdout.

Examples:
  # print to stdout
  notxctl pairing ca

  # save to disk for use as --ca in subsequent commands
  notxctl --addr authority.example.com:50052 --insecure \
      pairing ca --out /etc/notx/ca.crt

  # JSON output includes the raw PEM string
  notxctl pairing ca -o json`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		resp, err := conn.Pairing().GetCACertificate(ctx, &pb.GetCACertificateRequest{})
		if err != nil {
			return fmt.Errorf("GetCACertificate: %w", err)
		}

		pem := string(resp.CaCertificate)

		switch outputFromCtx(cmd) {
		case "json":
			return printJSON(map[string]any{"ca_certificate": pem})
		default:
			if pairingCAFlags.outFile != "" {
				if err := writeFilePEM(pairingCAFlags.outFile, resp.CaCertificate); err != nil {
					return fmt.Errorf("write CA cert to %s: %w", pairingCAFlags.outFile, err)
				}
				fmt.Printf("CA certificate written to %s\n", pairingCAFlags.outFile)
				return nil
			}
			fmt.Print(pem)
		}
		return nil
	},
}

func init() {
	pairingCACmd.Flags().StringVar(&pairingCAFlags.outFile, "out", "",
		"write the PEM CA certificate to this file instead of stdout")
}

// ─────────────────────────────────────────────────────────────────────────────
// pairing register
// ─────────────────────────────────────────────────────────────────────────────

var pairingRegisterFlags struct {
	serverURN  string
	serverName string
	endpoint   string
	secret     string
	certOut    string
	caOut      string
}

var pairingRegisterCmd = &cobra.Command{
	Use:   "register",
	Short: "Register this server with an authority (bootstrap flow)",
	Long: `Calls RegisterServer on the authority's bootstrap listener (port 50052).

This is the joining-server side of the pairing protocol. You must supply a
one-time pairing secret generated by the authority admin. The command
generates a fresh EC P-256 keypair in memory, builds a CSR, and exchanges
it for a signed certificate.

The signed certificate and CA certificate are printed to stdout in PEM
format (or written to --cert-out / --ca-out if supplied).

  --urn        self-assigned notx:srv:<uuid> for this server (required)
  --name       human-readable name, e.g. "datacenter-b replica" (required)
  --endpoint   gRPC address reachable by the authority, e.g. grpc.dc-b.example.com:50051 (required)
  --secret     one-time NTXP-… pairing secret from the authority admin (required)
  --cert-out   write the signed certificate PEM to this file
  --ca-out     write the authority CA certificate PEM to this file

EXAMPLE:
  notxctl --addr authority.example.com:50052 --insecure \
      pairing register \
      --urn      notx:srv:$(uuidgen | tr A-Z a-z) \
      --name     "dc-b replica" \
      --endpoint grpc.dc-b.example.com:50051 \
      --secret   NTXP-AAAAA-BBBBB-CCCCC-DDDDD-EEEEE \
      --cert-out /etc/notx/server.crt \
      --ca-out   /etc/notx/ca.crt`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if pairingRegisterFlags.serverURN == "" {
			return fmt.Errorf("--urn is required")
		}
		if pairingRegisterFlags.serverName == "" {
			return fmt.Errorf("--name is required")
		}
		if pairingRegisterFlags.endpoint == "" {
			return fmt.Errorf("--endpoint is required")
		}
		if pairingRegisterFlags.secret == "" {
			return fmt.Errorf("--secret is required")
		}

		csrDER, err := generateCSR(pairingRegisterFlags.serverURN)
		if err != nil {
			return fmt.Errorf("generate CSR: %w", err)
		}

		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		resp, err := conn.Pairing().RegisterServer(ctx, &pb.RegisterServerRequest{
			ServerUrn:     pairingRegisterFlags.serverURN,
			Csr:           csrDER,
			PairingSecret: pairingRegisterFlags.secret,
			ServerName:    pairingRegisterFlags.serverName,
			Endpoint:      pairingRegisterFlags.endpoint,
		})
		if err != nil {
			return fmt.Errorf("RegisterServer: %w", err)
		}

		switch outputFromCtx(cmd) {
		case "json":
			return printJSON(map[string]any{
				"server_urn":     resp.ServerUrn,
				"certificate":    base64.StdEncoding.EncodeToString(resp.Certificate),
				"ca_certificate": base64.StdEncoding.EncodeToString(resp.CaCertificate),
				"expires_at":     resp.ExpiresAt.AsTime(),
				"registered_at":  resp.RegisteredAt.AsTime(),
			})
		default:
			if pairingRegisterFlags.certOut != "" {
				if err := writeFilePEM(pairingRegisterFlags.certOut, resp.Certificate); err != nil {
					return fmt.Errorf("write cert to %s: %w", pairingRegisterFlags.certOut, err)
				}
				fmt.Printf("certificate written to  %s\n", pairingRegisterFlags.certOut)
			} else {
				fmt.Print(string(resp.Certificate))
			}

			if pairingRegisterFlags.caOut != "" {
				if err := writeFilePEM(pairingRegisterFlags.caOut, resp.CaCertificate); err != nil {
					return fmt.Errorf("write CA cert to %s: %w", pairingRegisterFlags.caOut, err)
				}
				fmt.Printf("CA certificate written to %s\n", pairingRegisterFlags.caOut)
			} else {
				fmt.Print(string(resp.CaCertificate))
			}

			fmt.Printf("server URN     %s\n", resp.ServerUrn)
			fmt.Printf("registered at  %s\n", fmtTime(resp.RegisteredAt.AsTime()))
			fmt.Printf("expires at     %s\n", fmtTime(resp.ExpiresAt.AsTime()))
		}
		return nil
	},
}

func init() {
	f := pairingRegisterCmd.Flags()
	f.StringVar(&pairingRegisterFlags.serverURN, "urn", "",
		"self-assigned URN for this server: notx:srv:<uuid> (required)")
	f.StringVar(&pairingRegisterFlags.serverName, "name", "",
		"human-readable server label, e.g. \"dc-b replica\" (required)")
	f.StringVar(&pairingRegisterFlags.endpoint, "endpoint", "",
		"gRPC endpoint reachable by the authority, e.g. grpc.dc-b.example.com:50051 (required)")
	f.StringVar(&pairingRegisterFlags.secret, "secret", "",
		"one-time NTXP-… pairing secret from the authority admin (required)")
	f.StringVar(&pairingRegisterFlags.certOut, "cert-out", "",
		"write the signed certificate PEM to this file (prints to stdout if omitted)")
	f.StringVar(&pairingRegisterFlags.caOut, "ca-out", "",
		"write the CA certificate PEM to this file (prints to stdout if omitted)")
}

// ─────────────────────────────────────────────────────────────────────────────
// pairing renew
// ─────────────────────────────────────────────────────────────────────────────

var pairingRenewFlags struct {
	serverURN string
	certOut   string
}

var pairingRenewCmd = &cobra.Command{
	Use:   "renew",
	Short: "Renew the current server certificate (primary mTLS port)",
	Long: `Calls RenewCertificate on the authority's primary listener (port 50051).

The caller must present its current valid mTLS client certificate. Use
--cert, --key, and --ca global flags to supply the current cert material.
A fresh EC P-256 keypair is generated in memory for the new CSR.

Use --cert-out to write the renewed certificate to a file.

EXAMPLE:
  notxctl \
      --addr authority.example.com:50051 \
      --cert /etc/notx/server.crt         \
      --key  /etc/notx/server.key         \
      --ca   /etc/notx/ca.crt             \
      pairing renew                        \
      --urn      notx:srv:…               \
      --cert-out /etc/notx/server.crt`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if pairingRenewFlags.serverURN == "" {
			return fmt.Errorf("--urn is required")
		}

		csrDER, err := generateCSR(pairingRenewFlags.serverURN)
		if err != nil {
			return fmt.Errorf("generate CSR: %w", err)
		}

		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		resp, err := conn.Pairing().RenewCertificate(ctx, &pb.RenewCertificateRequest{
			ServerUrn: pairingRenewFlags.serverURN,
			Csr:       csrDER,
		})
		if err != nil {
			return fmt.Errorf("RenewCertificate: %w", err)
		}

		switch outputFromCtx(cmd) {
		case "json":
			return printJSON(map[string]any{
				"server_urn":  pairingRenewFlags.serverURN,
				"certificate": base64.StdEncoding.EncodeToString(resp.Certificate),
				"expires_at":  resp.ExpiresAt.AsTime(),
			})
		default:
			if pairingRenewFlags.certOut != "" {
				if err := writeFilePEM(pairingRenewFlags.certOut, resp.Certificate); err != nil {
					return fmt.Errorf("write renewed cert to %s: %w", pairingRenewFlags.certOut, err)
				}
				fmt.Printf("renewed certificate written to  %s\n", pairingRenewFlags.certOut)
			} else {
				fmt.Print(string(resp.Certificate))
			}
			fmt.Printf("expires at  %s\n", fmtTime(resp.ExpiresAt.AsTime()))
		}
		return nil
	},
}

func init() {
	f := pairingRenewCmd.Flags()
	f.StringVar(&pairingRenewFlags.serverURN, "urn", "",
		"URN of the server whose certificate is being renewed (required)")
	f.StringVar(&pairingRenewFlags.certOut, "cert-out", "",
		"write the renewed certificate PEM to this file (prints to stdout if omitted)")
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers local to pairing
// ─────────────────────────────────────────────────────────────────────────────

// generateCSR creates a fresh EC P-256 key in memory and returns a DER-encoded
// PKCS#10 certificate signing request with the given commonName.
//
// The private key is intentionally not persisted here — this command is a
// debug/ops tool. Production joining servers (serverclient.PairingClient) manage
// their own key persistence in PeerCertDir.
func generateCSR(commonName string) ([]byte, error) {
	// Import crypto packages inline to keep the pairing.go surface clean.
	// Using a named helper avoids polluting the package-level import block with
	// crypto imports that are only needed for this one function.
	return buildCSRForCommonName(commonName)
}

// writeFilePEM writes data to path with mode 0o644, creating or truncating
// the file. Returns a descriptive error on failure.
func writeFilePEM(path string, data []byte) error {
	// Use os directly to avoid an extra import; os is already in scope via
	// the standard Go runtime (no explicit import needed in this file because
	// os is imported transitively — but we declare it here for clarity).
	return writeFileBytes(path, data)
}
