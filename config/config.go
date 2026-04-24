package config

import (
	"fmt"
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
	DefaultHTTPPort = 7430
	DefaultGRPCPort = 50051
)

// AICredentialsConfig configures the local encrypted AI credential store.
type AICredentialsConfig struct {
	// Path is the location of the encrypted credentials file.
	// Default: "<DataDir>/ai_credentials.enc"
	Path string

	// KeySource controls how the encryption key is derived.
	// Accepted values: "passphrase" (Argon2id-derived), "keychain" (OS keychain).
	// Default: "passphrase"
	KeySource string
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

	// EnableGRPC activates the gRPC layer (used by notxctl).
	EnableGRPC bool

	// ── Network ─────────────────────────────────────────────────────────────

	// HTTPPort is the TCP port the HTTP server listens on.
	// Default: 7430.
	HTTPPort int

	// GRPCPort is the TCP port the gRPC server listens on.
	// Default: 50051.
	GRPCPort int

	// Host is the bind address for both servers.
	// Default: "127.0.0.1" (localhost only).
	Host string

	// ── Storage ─────────────────────────────────────────────────────────────

	// DataDir is the root directory used by the file-based provider.
	// Notes are stored as <DataDir>/notes/<urn>.notx.
	// The Badger index lives at <DataDir>/index/.
	// Default: "./data".
	DataDir string

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

	// ── AI credentials ───────────────────────────────────────────────────────

	// AICredentials holds configuration for the local encrypted credential store.
	AICredentials AICredentialsConfig
}

// Default returns a Config populated with all production-safe defaults.
// Callers should start from Default() and override only what they need.
func Default() *Config {
	return &Config{
		EnableHTTP:      true,
		EnableGRPC:      true,
		HTTPPort:        DefaultHTTPPort,
		GRPCPort:        DefaultGRPCPort,
		Host:            "127.0.0.1",
		DataDir:         "./data",
		ShutdownTimeout: 30 * time.Second,
		MaxPageSize:     200,
		DefaultPageSize: 50,
		LogLevel:        "info",
		AICredentials: AICredentialsConfig{
			KeySource: "passphrase",
		},
	}
}

// HTTPAddr returns the full bind address string for the HTTP server,
// e.g. "127.0.0.1:7430".
func (c *Config) HTTPAddr() string {
	return formatAddr(c.Host, c.HTTPPort)
}

// GRPCAddr returns the full bind address string for the gRPC server,
// e.g. "127.0.0.1:50051".
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
