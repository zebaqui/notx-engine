package clientconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zebaqui/notx-engine/core"
)

// ─────────────────────────────────────────────────────────────────────────────
// Config
// ─────────────────────────────────────────────────────────────────────────────

// Config is the unified configuration file for both the notx CLI client and
// the notx server. It lives at ~/.notx/config.json and is written automatically
// on first use by EnsureConfig().
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

	Client ClientConfig `json:"client"`

	// ── Server ───────────────────────────────────────────────────────────────

	Server ServerConfig `json:"server"`

	// ── Admin UI ─────────────────────────────────────────────────────────────

	Admin AdminConfig `json:"admin"`

	// ── Storage ──────────────────────────────────────────────────────────────

	Storage StorageConfig `json:"storage"`

	// ── TLS ──────────────────────────────────────────────────────────────────

	TLS TLSConfig `json:"tls"`

	// ── Logging ──────────────────────────────────────────────────────────────

	Log LogConfig `json:"log"`
}

// ClientConfig controls how CLI commands connect to the notx gRPC server.
type ClientConfig struct {
	// GRPCAddr is the host:port the CLI dials for gRPC calls.
	// Default: "localhost:50051"
	GRPCAddr string `json:"grpc_addr"`

	// Namespace is the URN namespace used when generating note/event URNs.
	// Default: "notx"
	Namespace string `json:"namespace"`

	// Insecure disables TLS verification on the client dial (dev only).
	// Default: true (matches the server default of no TLS in dev)
	Insecure bool `json:"insecure"`
}

// ServerConfig controls the notx gRPC/HTTP server listeners.
type ServerConfig struct {
	// HTTPAddr is the host:port the HTTP server binds to.
	// Default: ":4060"
	HTTPAddr string `json:"http_addr"`

	// GRPCAddr is the host:port the gRPC server binds to.
	// Default: ":50051"
	GRPCAddr string `json:"grpc_addr"`

	// EnableHTTP toggles the HTTP/JSON API layer.
	// Default: true
	EnableHTTP bool `json:"enable_http"`

	// EnableGRPC toggles the gRPC layer.
	// Default: true
	EnableGRPC bool `json:"enable_grpc"`

	// ShutdownTimeoutSec is the graceful-shutdown window in seconds.
	// Default: 30
	ShutdownTimeoutSec int `json:"shutdown_timeout_sec"`

	// LogLevel overrides the global log level for the server process.
	// Accepted: "debug", "info", "warn", "error". Empty → use Log.Level.
	LogLevel string `json:"log_level,omitempty"`
}

// AdminConfig controls the embedded admin UI server.
type AdminConfig struct {
	// Addr is the host:port the admin HTTP server binds to.
	// Default: ":9090"
	Addr string `json:"addr"`

	// APIAddr is the notx server base URL the admin UI proxies API calls to.
	// Default: "http://localhost:4060"
	APIAddr string `json:"api_addr"`

	// DeviceURN is the fully-qualified URN of the admin device that was
	// registered on this machine via `notx admin --remote`. When non-empty,
	// the admin UI sends this URN as the X-Device-ID header on every request
	// instead of the hardcoded local-mode sentinel.
	//
	// This field is written automatically by `notx admin --remote` after a
	// successful admin device registration. Do not set it manually unless you
	// know what you are doing.
	DeviceURN string `json:"device_urn,omitempty"`

	// AdminDeviceURN is the fully-qualified URN of the built-in local-mode
	// admin device that is bootstrapped on every server startup.
	//
	// This value is generated once on first run (using a random UUIDv4) and
	// persisted here so that the same URN is reused across restarts while
	// still being unique per installation. This provides security-by-obscurity
	// on top of the role-based access controls: an attacker cannot predict the
	// admin device URN without access to this config file.
	//
	// Generated automatically by EnsureConfig(). Do not set it manually.
	AdminDeviceURN string `json:"admin_device_urn,omitempty"`

	// AdminOwnerURN is the fully-qualified URN of the user that owns the
	// built-in local-mode admin device.
	//
	// Like AdminDeviceURN, this is generated once on first run and persisted
	// so it remains stable across restarts while being unique per installation.
	//
	// Generated automatically by EnsureConfig(). Do not set it manually.
	AdminOwnerURN string `json:"admin_owner_urn,omitempty"`
}

// StorageConfig controls where note data is persisted on disk.
type StorageConfig struct {
	// DataDir is the root directory for note files and the Badger index.
	// Default: "~/.notx/data"
	DataDir string `json:"data_dir"`
}

// TLSConfig holds optional paths to TLS/mTLS certificate material.
// When CertFile and KeyFile are both non-empty, TLS is enabled on the server
// and the CLI uses TLS when dialling.
type TLSConfig struct {
	// CertFile is the path to the PEM-encoded server certificate.
	CertFile string `json:"cert_file,omitempty"`

	// KeyFile is the path to the PEM-encoded server private key.
	KeyFile string `json:"key_file,omitempty"`

	// CAFile is the path to the PEM-encoded CA certificate for mTLS.
	CAFile string `json:"ca_file,omitempty"`
}

// LogConfig controls log output.
type LogConfig struct {
	// Level is the minimum log level. Accepted: "debug", "info", "warn", "error".
	// Default: "info"
	Level string `json:"level"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Defaults
// ─────────────────────────────────────────────────────────────────────────────

// Default returns a Config populated with all production-safe defaults.
// Callers should start from Default() and override only what they need.
//
// The Admin.AdminDeviceURN and Admin.AdminOwnerURN fields are left empty here
// intentionally: EnsureConfig() is responsible for generating and persisting
// fresh UUIDs on first run. Load() will always return non-empty values for
// those fields on subsequent runs because they are stored in the config file.
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

// generateAdminURNs returns a fresh pair of (deviceURN, ownerURN) strings
// built from random UUIDv4 values. The URNs follow the notx format:
//
//	notx:device:<uuidv4>
//	notx:usr:<uuidv4>
func generateAdminURNs() (deviceURN, ownerURN string) {
	deviceURN = core.NewURN(core.ObjectTypeDevice).String()
	ownerURN = core.NewURN(core.ObjectTypeUser).String()
	return deviceURN, ownerURN
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

// Path returns the full path to the config file: ~/.notx/config.json
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
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
// Load / Save / EnsureConfig
// ─────────────────────────────────────────────────────────────────────────────

// Load reads the config file from ~/.notx/config.json.
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
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("clientconfig: parse %s: %w", path, err)
	}

	return cfg, nil
}

// EnsureConfig writes the default configuration to ~/.notx/config.json if and
// only if the file does not already exist. It is safe to call on every startup:
// it is a no-op when the file is already present.
//
// On first run it also generates unique UUIDv4-based URNs for the built-in
// admin device and its owner, storing them under Admin.AdminDeviceURN and
// Admin.AdminOwnerURN. This ensures every installation has its own
// unpredictable admin URNs rather than sharing the well-known all-zero
// sentinel, providing an additional layer of security by obscurity.
//
// Returns (true, nil) when the file was created, (false, nil) when it already
// existed, and (false, err) on any I/O error.
func EnsureConfig() (created bool, err error) {
	path, err := Path()
	if err != nil {
		return false, err
	}

	// Already exists — check whether the admin URNs need to be back-filled
	// (handles configs created before this feature was introduced).
	if _, statErr := os.Stat(path); statErr == nil {
		return false, ensureAdminURNs(path)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return false, fmt.Errorf("clientconfig: stat %s: %w", path, statErr)
	}

	// First run — create a default config with freshly generated admin URNs.
	cfg := Default()
	cfg.Admin.AdminDeviceURN, cfg.Admin.AdminOwnerURN = generateAdminURNs()

	if err := Save(cfg); err != nil {
		return false, err
	}
	return true, nil
}

// ensureAdminURNs reads the config at path and, if either AdminDeviceURN or
// AdminOwnerURN is empty (e.g. the file pre-dates this feature), generates
// fresh ones and saves the updated config back. It is a no-op when both URNs
// are already populated.
func ensureAdminURNs(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("clientconfig: read %s: %w", path, err)
	}

	cfg := Default()
	if err := json.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("clientconfig: parse %s: %w", path, err)
	}

	if cfg.Admin.AdminDeviceURN != "" && cfg.Admin.AdminOwnerURN != "" {
		return nil // already populated, nothing to do
	}

	cfg.Admin.AdminDeviceURN, cfg.Admin.AdminOwnerURN = generateAdminURNs()
	return Save(cfg)
}

// Save writes cfg to ~/.notx/config.json, creating the directory if needed.
// The file is written atomically via a temp-file rename.
// The output is pretty-printed JSON for human readability.
func Save(cfg *Config) error {
	dir, err := Dir()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("clientconfig: create config dir %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(cfg, "", "    ")
	if err != nil {
		return fmt.Errorf("clientconfig: marshal config: %w", err)
	}
	// Ensure the file ends with a newline.
	data = append(data, '\n')

	// Write to a temp file first, then rename for atomic replacement.
	cfgPath := filepath.Join(dir, "config.json")
	tmp, err := os.CreateTemp(dir, ".config.*.json.tmp")
	if err != nil {
		return fmt.Errorf("clientconfig: create temp file: %w", err)
	}
	tmpPath := tmp.Name()

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
