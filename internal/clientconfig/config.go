package clientconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ─────────────────────────────────────────────────────────────────────────────
// Config
// ─────────────────────────────────────────────────────────────────────────────

// Config is the unified configuration file for both the notx CLI client and
// the notx server. It lives at ~/.notx/config.yml and is created automatically
// on first use.
//
// Fields are grouped by concern:
//   - [server]  — what address/port the server listens on (used by `notx server`)
//   - [client]  — where the CLI dials to (used by `notx add`, `notx config`, …)
//   - [admin]   — where the admin UI listens (used by `notx admin`)
//   - [storage] — where note data is persisted
//   - [tls]     — optional TLS/mTLS paths (shared by server and client)
//   - [log]     — log verbosity
type Config struct {
	// ── Client ───────────────────────────────────────────────────────────────

	Client ClientConfig `yaml:"client"`

	// ── Server ───────────────────────────────────────────────────────────────

	Server ServerConfig `yaml:"server"`

	// ── Admin UI ─────────────────────────────────────────────────────────────

	Admin AdminConfig `yaml:"admin"`

	// ── Storage ──────────────────────────────────────────────────────────────

	Storage StorageConfig `yaml:"storage"`

	// ── TLS ──────────────────────────────────────────────────────────────────

	TLS TLSConfig `yaml:"tls"`

	// ── Logging ──────────────────────────────────────────────────────────────

	Log LogConfig `yaml:"log"`
}

// ClientConfig controls how CLI commands connect to the notx gRPC server.
type ClientConfig struct {
	// GRPCAddr is the host:port the CLI dials for gRPC calls.
	// Default: "localhost:50051"
	GRPCAddr string `yaml:"grpc_addr"`

	// Namespace is the URN namespace used when generating note/event URNs.
	// Default: "notx"
	Namespace string `yaml:"namespace"`

	// Insecure disables TLS verification on the client dial (dev only).
	// Default: true (matches the server default of no TLS in dev)
	Insecure bool `yaml:"insecure"`
}

// ServerConfig controls the notx gRPC/HTTP server listeners.
type ServerConfig struct {
	// HTTPAddr is the host:port the HTTP server binds to.
	// Default: ":4060"
	HTTPAddr string `yaml:"http_addr"`

	// GRPCAddr is the host:port the gRPC server binds to.
	// Default: ":50051"
	GRPCAddr string `yaml:"grpc_addr"`

	// EnableHTTP toggles the HTTP/JSON API layer.
	// Default: true
	EnableHTTP bool `yaml:"enable_http"`

	// EnableGRPC toggles the gRPC layer.
	// Default: true
	EnableGRPC bool `yaml:"enable_grpc"`

	// ShutdownTimeoutSec is the graceful-shutdown window in seconds.
	// Default: 30
	ShutdownTimeoutSec int `yaml:"shutdown_timeout_sec"`

	// LogLevel overrides the global log level for the server process.
	// Accepted: "debug", "info", "warn", "error". Empty → use Log.Level.
	LogLevel string `yaml:"log_level,omitempty"`
}

// AdminConfig controls the embedded admin UI server.
type AdminConfig struct {
	// Addr is the host:port the admin HTTP server binds to.
	// Default: ":9090"
	Addr string `yaml:"addr"`

	// APIAddr is the notx server base URL the admin UI proxies API calls to.
	// Default: "http://localhost:4060"
	APIAddr string `yaml:"api_addr"`
}

// StorageConfig controls where note data is persisted on disk.
type StorageConfig struct {
	// DataDir is the root directory for note files and the Badger index.
	// Default: "~/.notx/data"
	DataDir string `yaml:"data_dir"`
}

// TLSConfig holds optional paths to TLS/mTLS certificate material.
// When CertFile and KeyFile are both non-empty, TLS is enabled on the server
// and the CLI uses TLS when dialling.
type TLSConfig struct {
	// CertFile is the path to the PEM-encoded server certificate.
	CertFile string `yaml:"cert_file,omitempty"`

	// KeyFile is the path to the PEM-encoded server private key.
	KeyFile string `yaml:"key_file,omitempty"`

	// CAFile is the path to the PEM-encoded CA certificate for mTLS.
	CAFile string `yaml:"ca_file,omitempty"`
}

// LogConfig controls log output.
type LogConfig struct {
	// Level is the minimum log level. Accepted: "debug", "info", "warn", "error".
	// Default: "info"
	Level string `yaml:"level"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Defaults
// ─────────────────────────────────────────────────────────────────────────────

// Default returns a Config populated with all production-safe defaults.
// Callers should start from Default() and override only what they need.
func Default() *Config {
	return &Config{
		Client: ClientConfig{
			GRPCAddr:  "localhost:50051",
			Namespace: "notx",
			Insecure:  true,
		},
		Server: ServerConfig{
			HTTPAddr:           ":4060",
			GRPCAddr:           ":50051",
			EnableHTTP:         true,
			EnableGRPC:         true,
			ShutdownTimeoutSec: 30,
		},
		Admin: AdminConfig{
			Addr:    ":9090",
			APIAddr: "http://localhost:4060",
		},
		Storage: StorageConfig{
			DataDir: defaultDataDir(),
		},
		Log: LogConfig{
			Level: "info",
		},
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Path resolution
// ─────────────────────────────────────────────────────────────────────────────

// Dir returns the path to the notx config directory: ~/.notx/
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("clientconfig: resolve home dir: %w", err)
	}
	return filepath.Join(home, ".notx"), nil
}

// Path returns the full path to the config file: ~/.notx/config.yml
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yml"), nil
}

// defaultDataDir returns ~/.notx/data as the default storage root.
// Falls back to "./data" if the home directory cannot be resolved.
func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "./data"
	}
	return filepath.Join(home, ".notx", "data")
}

// ─────────────────────────────────────────────────────────────────────────────
// Load / Save
// ─────────────────────────────────────────────────────────────────────────────

// Load reads the config file from ~/.notx/config.yml.
//
// If the file does not exist, Load returns Default() and nil — callers can
// rely on a valid Config being returned even on a fresh machine.
//
// Fields present in the file are merged on top of Default() so that new
// fields added in future versions are always initialised to a sane value.
func Load() (*Config, error) {
	path, err := Path()
	if err != nil {
		return Default(), nil //nolint:nilerr // best-effort fallback
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Default(), nil
		}
		return nil, fmt.Errorf("clientconfig: read %s: %w", path, err)
	}

	// Start from defaults so missing keys are always populated.
	cfg := Default()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("clientconfig: parse %s: %w", path, err)
	}

	return cfg, nil
}

// Save writes cfg to ~/.notx/config.yml, creating the directory if needed.
// The file is written atomically via a temp-file rename.
func Save(cfg *Config) error {
	dir, err := Dir()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("clientconfig: create config dir %s: %w", dir, err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("clientconfig: marshal config: %w", err)
	}

	// Write to a temp file first, then rename for atomic replacement.
	cfgPath := filepath.Join(dir, "config.yml")
	tmp, err := os.CreateTemp(dir, ".config.*.yml.tmp")
	if err != nil {
		return fmt.Errorf("clientconfig: create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	// Prepend a header comment so the file is self-documenting.
	header := "# notx configuration — ~/.notx/config.yml\n" +
		"# Edit this file directly or run `notx config` to update interactively.\n\n"

	if _, err := tmp.WriteString(header); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("clientconfig: write header: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("clientconfig: write config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("clientconfig: sync config: %w", err)
	}
	tmp.Close()

	if err := os.Rename(tmpPath, cfgPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("clientconfig: save config to %s: %w", cfgPath, err)
	}

	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// TLSEnabled reports whether both cert and key file paths are configured.
func (c *Config) TLSEnabled() bool {
	return c.TLS.CertFile != "" && c.TLS.KeyFile != ""
}

// MTLSEnabled reports whether mutual TLS is configured (TLS + CA cert).
func (c *Config) MTLSEnabled() bool {
	return c.TLSEnabled() && c.TLS.CAFile != ""
}

// EffectiveLogLevel returns the log level to use for a given component.
// If the component-specific level is set it takes precedence; otherwise
// the global Log.Level is used.
func (c *Config) EffectiveLogLevel(componentLevel string) string {
	if componentLevel != "" {
		return componentLevel
	}
	if c.Log.Level != "" {
		return c.Log.Level
	}
	return "info"
}
