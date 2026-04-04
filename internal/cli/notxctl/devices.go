package notxctl

import (
	"encoding/base64"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/zebaqui/notx-engine/core"
	pb "github.com/zebaqui/notx-engine/proto"
)

// ─────────────────────────────────────────────────────────────────────────────
// devices — top-level group
// ─────────────────────────────────────────────────────────────────────────────

var devicesCmd = &cobra.Command{
	Use:   "devices",
	Short: "Manage devices (DeviceService)",
	Long: `Commands for the DeviceService gRPC endpoints.

Sub-commands:
  list        ListDevices          — list devices, optionally filtered by owner
  get-key     GetDevicePublicKey   — retrieve the public key for a device
  register    RegisterDevice       — register a new device
  revoke      RevokeDevice         — revoke a device
  pair start  InitiatePairing      — start a browser pairing session
  pair complete CompletePairing    — complete a browser pairing session`,
}

func init() {
	devicesCmd.AddCommand(devicesListCmd)
	devicesCmd.AddCommand(devicesGetKeyCmd)
	devicesCmd.AddCommand(devicesRegisterCmd)
	devicesCmd.AddCommand(devicesRevokeCmd)

	// pair is a sub-group under devices
	devicesCmd.AddCommand(devicesPairCmd)
	devicesPairCmd.AddCommand(devicesPairStartCmd)
	devicesPairCmd.AddCommand(devicesPairCompleteCmd)
}

// ─────────────────────────────────────────────────────────────────────────────
// devices list
// ─────────────────────────────────────────────────────────────────────────────

var devicesListFlags struct {
	ownerURN string
}

var devicesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List registered devices",
	Long: `Calls ListDevices and prints a table of registered devices.

Examples:
  notxctl devices list
  notxctl devices list --owner urn:notx:usr:…`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		resp, err := conn.Devices().ListDevices(ctx, &pb.ListDevicesRequest{
			OwnerUrn: devicesListFlags.ownerURN,
		})
		if err != nil {
			return fmt.Errorf("ListDevices: %w", err)
		}

		switch outputFromCtx(cmd) {
		case "json":
			type deviceOut struct {
				URN          string    `json:"device_urn"`
				Name         string    `json:"device_name"`
				PublicKeyB64 string    `json:"public_key_b64"`
				RegisteredAt time.Time `json:"registered_at"`
				LastSeenAt   time.Time `json:"last_seen_at,omitempty"`
			}
			var devices []deviceOut
			for _, d := range resp.Devices {
				devices = append(devices, deviceOut{
					URN:          d.DeviceUrn,
					Name:         d.DeviceName,
					PublicKeyB64: base64.StdEncoding.EncodeToString(d.PublicKey),
					RegisteredAt: d.RegisteredAt.AsTime(),
					LastSeenAt:   d.LastSeenAt.AsTime(),
				})
			}
			return printJSON(map[string]any{"devices": devices})

		default:
			tw := newTabWriter()
			defer tw.Flush()
			header(tw, "URN", "NAME", "REGISTERED", "LAST SEEN")
			for _, d := range resp.Devices {
				lastSeen := "—"
				if d.LastSeenAt != nil && !d.LastSeenAt.AsTime().IsZero() {
					lastSeen = fmtTime(d.LastSeenAt.AsTime())
				}
				row(tw,
					shortURN(d.DeviceUrn),
					d.DeviceName,
					fmtTime(d.RegisteredAt.AsTime()),
					lastSeen,
				)
			}
			fmt.Printf("\ntotal: %d device(s)\n", len(resp.Devices))
		}
		return nil
	},
}

func init() {
	devicesListCmd.Flags().StringVar(&devicesListFlags.ownerURN, "owner", "",
		"filter by owner URN (notx:usr:…)")
}

// ─────────────────────────────────────────────────────────────────────────────
// devices get-key <urn>
// ─────────────────────────────────────────────────────────────────────────────

var devicesGetKeyCmd = &cobra.Command{
	Use:   "get-key <urn>",
	Short: "Retrieve the public key for a device",
	Long: `Calls GetDevicePublicKey and prints the raw public key in base64.

Example:
  notxctl devices get-key urn:notx:device:…`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		resp, err := conn.Devices().GetDevicePublicKey(ctx, &pb.GetDevicePublicKeyRequest{
			DeviceUrn: args[0],
		})
		if err != nil {
			return fmt.Errorf("GetDevicePublicKey: %w", err)
		}

		b64Key := base64.StdEncoding.EncodeToString(resp.PublicKey)

		switch outputFromCtx(cmd) {
		case "json":
			return printJSON(map[string]any{
				"device_urn":     resp.DeviceUrn,
				"public_key_b64": b64Key,
			})
		default:
			tw := newTabWriter()
			defer tw.Flush()
			fmt.Fprintf(tw, "URN\t%s\n", resp.DeviceUrn)
			fmt.Fprintf(tw, "Public Key (base64)\t%s\n", b64Key)
		}
		return nil
	},
}

// ─────────────────────────────────────────────────────────────────────────────
// devices register
// ─────────────────────────────────────────────────────────────────────────────

var devicesRegisterFlags struct {
	urn       string
	name      string
	ownerURN  string
	publicKey string // base64-encoded
}

var devicesRegisterCmd = &cobra.Command{
	Use:   "register",
	Short: "Register a new device",
	Long: `Calls RegisterDevice to store a new device and its public key.

If --urn is omitted a random urn:notx:device:<uuidv7> is generated.
--key must be a base64-encoded Ed25519 public key (32 bytes).

Examples:
  notxctl devices register \
      --name "My Laptop" \
      --owner urn:notx:usr:… \
      --key "$(cat pubkey.b64)"`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		if devicesRegisterFlags.name == "" {
			return fmt.Errorf("--name is required")
		}
		if devicesRegisterFlags.ownerURN == "" {
			return fmt.Errorf("--owner is required")
		}
		if devicesRegisterFlags.publicKey == "" {
			return fmt.Errorf("--key is required")
		}

		keyBytes, err := base64.StdEncoding.DecodeString(devicesRegisterFlags.publicKey)
		if err != nil {
			// Try without padding (URL-safe base64).
			keyBytes, err = base64.RawStdEncoding.DecodeString(devicesRegisterFlags.publicKey)
			if err != nil {
				return fmt.Errorf("--key: invalid base64: %w", err)
			}
		}

		urn := devicesRegisterFlags.urn
		if urn == "" {
			urn = core.NewURN(core.ObjectTypeDevice).String()
		}

		resp, err := conn.Devices().RegisterDevice(ctx, &pb.RegisterDeviceRequest{
			DeviceUrn:  urn,
			DeviceName: devicesRegisterFlags.name,
			OwnerUrn:   devicesRegisterFlags.ownerURN,
			PublicKey:  keyBytes,
		})
		if err != nil {
			return fmt.Errorf("RegisterDevice: %w", err)
		}

		switch outputFromCtx(cmd) {
		case "json":
			return printJSON(map[string]any{
				"device_urn":    resp.DeviceUrn,
				"registered_at": resp.RegisteredAt.AsTime(),
			})
		default:
			fmt.Printf("registered  %s\n", resp.DeviceUrn)
			fmt.Printf("at          %s\n", fmtTime(resp.RegisteredAt.AsTime()))
		}
		return nil
	},
}

func init() {
	f := devicesRegisterCmd.Flags()
	f.StringVar(&devicesRegisterFlags.urn, "urn", "",
		"device URN (auto-generated if omitted)")
	f.StringVar(&devicesRegisterFlags.name, "name", "",
		"human-readable device name (required)")
	f.StringVar(&devicesRegisterFlags.ownerURN, "owner", "",
		"owner URN urn:notx:usr:… (required)")
	f.StringVar(&devicesRegisterFlags.publicKey, "key", "",
		"base64-encoded Ed25519 public key (required)")
}

// ─────────────────────────────────────────────────────────────────────────────
// devices revoke <urn>
// ─────────────────────────────────────────────────────────────────────────────

var devicesRevokeCmd = &cobra.Command{
	Use:   "revoke <urn>",
	Short: "Revoke a device",
	Long: `Calls RevokeDevice to permanently revoke a device by URN.

The device is removed from the registry. Future secure note shares will
not include the revoked device.

Example:
  notxctl devices revoke urn:notx:device:…`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		resp, err := conn.Devices().RevokeDevice(ctx, &pb.RevokeDeviceRequest{
			DeviceUrn: args[0],
		})
		if err != nil {
			return fmt.Errorf("RevokeDevice: %w", err)
		}

		switch outputFromCtx(cmd) {
		case "json":
			return printJSON(map[string]any{
				"device_urn": args[0],
				"revoked":    resp.Revoked,
			})
		default:
			fmt.Printf("revoked  %s\n", args[0])
		}
		return nil
	},
}

// ─────────────────────────────────────────────────────────────────────────────
// devices pair — sub-group
// ─────────────────────────────────────────────────────────────────────────────

var devicesPairCmd = &cobra.Command{
	Use:   "pair",
	Short: "Browser pairing flow (InitiatePairing / CompletePairing)",
	Long: `Sub-commands for the two-step browser device pairing flow.

  start     InitiatePairing  — generate a session token for QR display
  complete  CompletePairing  — register the browser device using the token`,
}

// ─────────────────────────────────────────────────────────────────────────────
// devices pair start
// ─────────────────────────────────────────────────────────────────────────────

var devicesPairStartFlags struct {
	initiatorURN string
}

var devicesPairStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Initiate a browser pairing session",
	Long: `Calls InitiatePairing to obtain a short-lived session token.

The token should be encoded into a QR code and displayed to the user.
It is valid for 5 minutes.

Example:
  notxctl devices pair start --initiator urn:notx:device:…`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if devicesPairStartFlags.initiatorURN == "" {
			return fmt.Errorf("--initiator is required")
		}

		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		resp, err := conn.Devices().InitiatePairing(ctx, &pb.InitiatePairingRequest{
			InitiatorDeviceUrn: devicesPairStartFlags.initiatorURN,
		})
		if err != nil {
			return fmt.Errorf("InitiatePairing: %w", err)
		}

		switch outputFromCtx(cmd) {
		case "json":
			return printJSON(map[string]any{
				"session_token": resp.SessionToken,
				"expires_at":    resp.ExpiresAt.AsTime(),
			})
		default:
			tw := newTabWriter()
			defer tw.Flush()
			fmt.Fprintf(tw, "Session Token\t%s\n", resp.SessionToken)
			fmt.Fprintf(tw, "Expires At\t%s\n", fmtTime(resp.ExpiresAt.AsTime()))
		}
		return nil
	},
}

func init() {
	devicesPairStartCmd.Flags().StringVar(&devicesPairStartFlags.initiatorURN, "initiator", "",
		"URN of the trusted device initiating the pairing (required)")
}

// ─────────────────────────────────────────────────────────────────────────────
// devices pair complete
// ─────────────────────────────────────────────────────────────────────────────

var devicesPairCompleteFlags struct {
	sessionToken string
	deviceURN    string
	deviceName   string
	publicKey    string // base64-encoded
}

var devicesPairCompleteCmd = &cobra.Command{
	Use:   "complete",
	Short: "Complete a browser pairing session",
	Long: `Calls CompletePairing to register the browser device using the QR session token.

--key must be a base64-encoded Ed25519 public key (32 bytes).

Example:
  notxctl devices pair complete \
      --token <session-token>   \
      --name  "Chrome on MacBook" \
      --key   "$(cat browser_pubkey.b64)"`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if devicesPairCompleteFlags.sessionToken == "" {
			return fmt.Errorf("--token is required")
		}
		if devicesPairCompleteFlags.deviceName == "" {
			return fmt.Errorf("--name is required")
		}
		if devicesPairCompleteFlags.publicKey == "" {
			return fmt.Errorf("--key is required")
		}

		keyBytes, err := base64.StdEncoding.DecodeString(devicesPairCompleteFlags.publicKey)
		if err != nil {
			keyBytes, err = base64.RawStdEncoding.DecodeString(devicesPairCompleteFlags.publicKey)
			if err != nil {
				return fmt.Errorf("--key: invalid base64: %w", err)
			}
		}

		urn := devicesPairCompleteFlags.deviceURN
		if urn == "" {
			urn = core.NewURN(core.ObjectTypeDevice).String()
		}

		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		resp, err := conn.Devices().CompletePairing(ctx, &pb.CompletePairingRequest{
			SessionToken: devicesPairCompleteFlags.sessionToken,
			DeviceUrn:    urn,
			DeviceName:   devicesPairCompleteFlags.deviceName,
			PublicKey:    keyBytes,
		})
		if err != nil {
			return fmt.Errorf("CompletePairing: %w", err)
		}

		switch outputFromCtx(cmd) {
		case "json":
			return printJSON(map[string]any{
				"device_urn":    resp.DeviceUrn,
				"registered_at": resp.RegisteredAt.AsTime(),
			})
		default:
			fmt.Printf("paired    %s\n", resp.DeviceUrn)
			fmt.Printf("at        %s\n", fmtTime(resp.RegisteredAt.AsTime()))
		}
		return nil
	},
}

func init() {
	f := devicesPairCompleteCmd.Flags()
	f.StringVar(&devicesPairCompleteFlags.sessionToken, "token", "",
		"session token from QR code / pair start (required)")
	f.StringVar(&devicesPairCompleteFlags.deviceURN, "urn", "",
		"URN for the browser device (auto-generated if omitted)")
	f.StringVar(&devicesPairCompleteFlags.deviceName, "name", "",
		"human-readable name for the browser device (required)")
	f.StringVar(&devicesPairCompleteFlags.publicKey, "key", "",
		"base64-encoded Ed25519 public key (required)")
}
