package config

import (
	"fmt"
	"path/filepath"
	"time"
)

// ServerMode describes which protocol servers are active.
type ServerMode int

const (
	// ModeHTTP runs only the HTTP layer.
	ModeHTTP ServerMode = 1 << iota
	// ModeGRPC runs only the gRPC layer.
	ModeGRPC
	// ModeBoth runs both HTTP and gRPC simultaneously.
	ModeBoth ServerMode = ModeHTTP | ModeGRPC
)

// Default port assignments.
const (
	DefaultHTTPPort = 4060
	DefaultGRPCPort = 50051
)

// DeviceOnboardingConfig controls how newly registered devices are handled
// before they are allowed to pull data from the server.
type DeviceOnboardingConfig struct {
	// AutoApprove, when true, immediately sets a newly registered device's
	// approval status to "approved" so it can start pulling data right away.
	// When false (the default) devices start in "pending" status and an
	// administrator must explicitly approve them via
	// PATCH /v1/devices/:urn/approve before they can access any data.
	AutoApprove bool
}

// AdminConfig holds configuration for the built-in server admin identity.
type AdminConfig struct {
	// DeviceURN is the fully-qualified URN of the admin device that is
	// automatically registered and approved on every server startup when
	// no AdminPassphraseHash is set (local-mode shortcut).
	//
	// When AdminPassphraseHash is set this field is ignored — admin devices
	// must register themselves via POST /v1/devices with a matching passphrase.
	//
	// Default: "urn:notx:device:00000000-0000-0000-0000-000000000000"
	DeviceURN string

	// OwnerURN is the user URN associated with the bootstrapped admin device
	// (local-mode only).
	//
	// Default: "urn:notx:usr:00000000-0000-0000-0000-000000000000"
	OwnerURN string

	// AdminPassphraseHash is a bcrypt hash of the admin registration
	// passphrase. When non-empty:
	//
	//  • The local-mode bootstrap device is NOT automatically created.
	//  • POST /v1/devices requests that include a matching "admin_passphrase"
	//    field are registered with role=admin and approval_status=approved
	//    immediately, bypassing the normal approval flow.
	//  • POST /v1/devices requests without a passphrase (or with a wrong one)
	//    are registered as role=client with the normal pending/auto-approve
	//    behaviour unchanged.
	//
	// Set this via the --admin-passphrase flag on `notx server` (the flag
	// accepts the plaintext passphrase and hashes it automatically).
	// Never store the plaintext passphrase in the config.
	AdminPassphraseHash string
}

// RelayPolicyConfig controls the security and resource limits for the relay
// execution engine.
type RelayPolicyConfig struct {
	// AllowedHosts is an explicit allowlist of hostnames the relay may contact.
	// When empty, all hosts are permitted (subject to the built-in block-list).
	// In production this should always be set.
	AllowedHosts []string

	// AllowLocalhost permits connections to loopback / RFC-1918 ranges.
	// Should only be true in development environments.
	AllowLocalhost bool

	// MaxSteps is the maximum number of steps in a single flow. Default: 20.
	MaxSteps int

	// MaxRequestBodyBytes caps the outbound request body. Default: 1 MiB.
	MaxRequestBodyBytes int64

	// MaxResponseBodyBytes caps the upstream response body read. Default: 4 MiB.
	MaxResponseBodyBytes int64

	// RequestTimeoutSecs is the per-request deadline in seconds. Default: 10.
	RequestTimeoutSecs int

	// MaxRedirects is the maximum redirects followed per request. Default: 5.
	MaxRedirects int
}

// ServerPairingConfig controls the server-to-server pairing subsystem.
type ServerPairingConfig struct {
	// Enabled activates the ServerPairingService on this instance.
	Enabled bool

	// BootstrapPort is the TCP port the pairing bootstrap listener binds to.
	// Default: 50052.
	BootstrapPort int

	// CertTTL is how long issued server certificates are valid.
	// Default: 720h (30 days).
	CertTTL time.Duration

	// SecretTTL is how long a generated pairing secret remains valid.
	// Default: 15m.
	SecretTTL time.Duration

	// CADir is the directory where the authority CA key and cert are stored.
	// Default: "<data-dir>/ca".
	CADir string

	// RenewalCheckInterval is how often a joining server checks its cert expiry.
	// Default: 6h.
	RenewalCheckInterval time.Duration

	// RenewalThreshold is the remaining TTL at which the joining server
	// automatically renews its certificate.
	// Default: 168h (7 days).
	RenewalThreshold time.Duration

	// PeerAuthority is the gRPC endpoint of the authority server this instance
	// should pair with (joining server mode).
	PeerAuthority string

	// PeerSecret is the NTXP-... pairing secret used once at startup to register.
	// Cleared from memory after successful registration.
	PeerSecret string

	// PeerCertDir is the directory where this server's client cert and key are stored.
	PeerCertDir string

	// PeerCAFile is the path to the PEM-encoded CA certificate of the authority
	// this server is pairing with. When set, the bootstrap dial verifies the
	// authority's TLS certificate against this CA instead of using
	// InsecureSkipVerify. Either PeerCAFile or PeerCAFingerprint must be set
	// when PeerAuthority is configured.
	PeerCAFile string

	// PeerCAFingerprint is the SHA-256 fingerprint (hex, colon-separated,
	// uppercase) of the authority CA certificate. Used when only the fingerprint
	// is known, not the full PEM. Either field may be set; both are checked.
	// Format: "AA:BB:CC:DD:..." (64 hex chars separated by 63 colons = 191 chars).
	PeerCAFingerprint string

	// DenySetRefreshInterval is how often the in-memory revocation deny-set is
	// rebuilt from the repository. This bounds the revocation propagation window
	// in multi-instance deployments.
	// Default: 5m.
	DenySetRefreshInterval time.Duration
}

// Config holds all runtime configuration for the notx server.
//
// It is populated once at startup (from CLI flags, env vars, or a config file)
// and treated as read-only afterwards. All sub-components receive a pointer to
// the same Config so there is a single source of truth.
type Config struct {
	// ── Protocol toggles ────────────────────────────────────────────────────

	// EnableHTTP activates the HTTP/JSON API layer.
	EnableHTTP bool

	// EnableGRPC activates the gRPC layer.
	EnableGRPC bool

	// ── Network ─────────────────────────────────────────────────────────────

	// HTTPPort is the TCP port the HTTP server listens on.
	// Default: 4060.
	HTTPPort int

	// GRPCPort is the TCP port the gRPC server listens on.
	// Default: 50051.
	GRPCPort int

	// Host is the bind address for both servers.
	// Default: "" (all interfaces).
	Host string

	// ── Storage ─────────────────────────────────────────────────────────────

	// DataDir is the root directory used by the file-based provider.
	// Notes are stored as <DataDir>/notes/<urn>.notx.
	// The Badger index lives at <DataDir>/index/.
	// Default: "./data".
	DataDir string

	// ── TLS / mTLS (Phase 5) ────────────────────────────────────────────────

	// TLSCertFile is the path to the PEM-encoded server certificate.
	// Leave empty to run without TLS (development only).
	TLSCertFile string

	// TLSKeyFile is the path to the PEM-encoded server private key.
	TLSKeyFile string

	// TLSCAFile is the path to the PEM-encoded CA certificate used to
	// validate client certificates (mTLS). Leave empty to skip client auth.
	TLSCAFile string

	// ── Operational ─────────────────────────────────────────────────────────

	// ShutdownTimeout is the maximum time the server waits for in-flight
	// requests to complete during graceful shutdown.
	// Default: 30 seconds.
	ShutdownTimeout time.Duration

	// MaxPageSize is the maximum number of items returned by list/search RPCs.
	// Default: 200.
	MaxPageSize int

	// DefaultPageSize is the page size used when the caller does not specify one.
	// Default: 50.
	DefaultPageSize int

	// LogLevel controls verbosity. Accepted values: "debug", "info", "warn", "error".
	// Default: "info".
	LogLevel string

	// ── Device onboarding ────────────────────────────────────────────────────

	// DeviceOnboarding controls whether newly registered devices are
	// auto-approved or held in a pending state awaiting manual approval.
	DeviceOnboarding DeviceOnboardingConfig

	// ── Admin identity ───────────────────────────────────────────────────────

	// Admin holds the configuration for the built-in server admin device.
	// This device is upserted on every startup with ApprovalStatus "approved"
	// so administrative operations are never blocked by the device auth gate.
	Admin AdminConfig

	// Pairing holds the configuration for the server-to-server pairing subsystem.
	Pairing ServerPairingConfig

	// Relay holds the security policy for the outbound HTTP relay execution engine.
	Relay RelayPolicyConfig
}

// DefaultAdminDeviceURN is the well-known URN reserved for the server's
// built-in admin device. All-zero UUID makes it visually distinct and
// impossible to collide with any client-generated UUIDv4/v7.
const DefaultAdminDeviceURN = "urn:notx:device:00000000-0000-0000-0000-000000000000"

// DefaultAdminOwnerURN is the well-known URN reserved for the admin user
// that owns the built-in admin device.
const DefaultAdminOwnerURN = "urn:notx:usr:00000000-0000-0000-0000-000000000000"

// Default returns a Config populated with all production-safe defaults.
// Callers should start from Default() and override only what they need.
func Default() *Config {
	return &Config{
		EnableHTTP:      true,
		EnableGRPC:      true,
		HTTPPort:        DefaultHTTPPort,
		GRPCPort:        DefaultGRPCPort,
		Host:            "",
		DataDir:         "./data",
		ShutdownTimeout: 30 * time.Second,
		MaxPageSize:     200,
		DefaultPageSize: 50,
		LogLevel:        "info",
		DeviceOnboarding: DeviceOnboardingConfig{
			AutoApprove: false,
		},
		Admin: AdminConfig{
			DeviceURN: DefaultAdminDeviceURN,
			OwnerURN:  DefaultAdminOwnerURN,
		},
		Pairing: ServerPairingConfig{
			Enabled:                false,
			BootstrapPort:          50052,
			CertTTL:                8760 * time.Hour, // 1 year
			SecretTTL:              24 * time.Hour,   // 24h
			RenewalCheckInterval:   6 * time.Hour,
			RenewalThreshold:       720 * time.Hour, // 30 days
			DenySetRefreshInterval: 5 * time.Minute,
		},
		Relay: RelayPolicyConfig{
			AllowLocalhost:       false,
			MaxSteps:             20,
			MaxRequestBodyBytes:  1 << 20,
			MaxResponseBodyBytes: 4 << 20,
			RequestTimeoutSecs:   10,
			MaxRedirects:         5,
		},
	}
}

// HTTPAddr returns the full bind address string for the HTTP server,
// e.g. ":4060" or "127.0.0.1:4060".
func (c *Config) HTTPAddr() string {
	return formatAddr(c.Host, c.HTTPPort)
}

// PairingBootstrapAddr returns the bind address for the bootstrap gRPC listener.
func (c *Config) PairingBootstrapAddr() string {
	return formatAddr(c.Host, c.Pairing.BootstrapPort)
}

// CADir returns the resolved CA directory (defaults to <DataDir>/ca).
func (c *Config) CADir() string {
	if c.Pairing.CADir != "" {
		return c.Pairing.CADir
	}
	return filepath.Join(c.DataDir, "ca")
}

// GRPCAddr returns the full bind address string for the gRPC server,
// e.g. ":50051" or "127.0.0.1:50051".
func (c *Config) GRPCAddr() string {
	return formatAddr(c.Host, c.GRPCPort)
}

// Mode returns the active ServerMode derived from the Enable* flags.
func (c *Config) Mode() ServerMode {
	var m ServerMode
	if c.EnableHTTP {
		m |= ModeHTTP
	}
	if c.EnableGRPC {
		m |= ModeGRPC
	}
	return m
}

// TLSEnabled reports whether TLS has been configured (cert + key both present).
func (c *Config) TLSEnabled() bool {
	return c.TLSCertFile != "" && c.TLSKeyFile != ""
}

// MTLSEnabled reports whether mutual TLS has been configured (TLS + CA cert).
func (c *Config) MTLSEnabled() bool {
	return c.TLSEnabled() && c.TLSCAFile != ""
}

// Validate returns a non-nil error if the configuration is inconsistent or
// missing required values.
func (c *Config) Validate() error {
	if !c.EnableHTTP && !c.EnableGRPC {
		return newConfigError("at least one of --http or --grpc must be enabled")
	}
	if c.HTTPPort < 1 || c.HTTPPort > 65535 {
		return newConfigError("http-port must be between 1 and 65535")
	}
	if c.GRPCPort < 1 || c.GRPCPort > 65535 {
		return newConfigError("grpc-port must be between 1 and 65535")
	}
	if c.EnableHTTP && c.EnableGRPC && c.HTTPPort == c.GRPCPort {
		return newConfigError("http-port and grpc-port must be different when both servers are enabled")
	}
	if c.DataDir == "" {
		return newConfigError("data-dir must not be empty")
	}
	if c.MaxPageSize < 1 {
		return newConfigError("max-page-size must be at least 1")
	}
	if c.DefaultPageSize < 1 || c.DefaultPageSize > c.MaxPageSize {
		return newConfigError("default-page-size must be between 1 and max-page-size")
	}
	if c.TLSCertFile != "" && c.TLSKeyFile == "" {
		return newConfigError("tls-key-file is required when tls-cert-file is set")
	}
	if c.TLSKeyFile != "" && c.TLSCertFile == "" {
		return newConfigError("tls-cert-file is required when tls-key-file is set")
	}
	if c.TLSCAFile != "" && !c.TLSEnabled() {
		return newConfigError("tls-ca-file requires tls-cert-file and tls-key-file to be set")
	}
	// Pairing config validation.
	if c.Pairing.Enabled {
		if c.Pairing.SecretTTL > 7*24*time.Hour {
			return newConfigError("pairing secret-ttl must not exceed 7 days")
		}
		if c.Pairing.PeerAuthority != "" &&
			c.Pairing.PeerCAFile == "" &&
			c.Pairing.PeerCAFingerprint == "" {
			return newConfigError(
				"pairing peer-ca-file or peer-ca-fingerprint is required when peer-authority is set")
		}
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Internal helpers
// ─────────────────────────────────────────────────────────────────────────────

func formatAddr(host string, port int) string {
	return fmt.Sprintf("%s:%d", host, port)
}

// ConfigError is returned by Validate for invalid configuration.
type ConfigError struct {
	msg string
}

func (e *ConfigError) Error() string { return "config: " + e.msg }

func newConfigError(msg string) error { return &ConfigError{msg: msg} }
