package clientconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ClientJSON represents the ~/.notx/client.json file that controls how the
// notx CLI connects to a backend (local, remote, or cloud).
type ClientJSON struct {
	Settings ClientJSONSettings `json:"settings"`
	Account  ClientJSONAccount  `json:"account"`
}

// ClientJSONSettings holds connection and UI preferences written by the notx
// desktop/web app and read by the CLI.
type ClientJSONSettings struct {
	// Source determines the connection mode.
	// Accepted values: "local" | "remote" | "cloud"
	// Default: "local"
	Source string `json:"source"`

	// RemoteURL is the base URL of the remote notx server (remote mode only).
	RemoteURL string `json:"remoteUrl,omitempty"`

	// GRPCPort is the gRPC port on the remote server (remote mode only).
	GRPCPort int `json:"grpcPort,omitempty"`

	// PairingBootstrapPort is the bootstrap port used during device pairing.
	PairingBootstrapPort int `json:"pairingBootstrapPort,omitempty"`

	// CloudToken is the JWT bearer token for cloud mode.
	CloudToken string `json:"cloudToken,omitempty"`

	// CloudRefreshToken is the refresh token extracted from the JWT rt claim.
	CloudRefreshToken string `json:"cloudRefreshToken,omitempty"`

	// CloudTokenExpiry is the JWT exp claim as a Unix timestamp.
	CloudTokenExpiry int64 `json:"cloudTokenExpiry,omitempty"`

	// ── UI preferences (preserved across reads/writes) ────────────────────

	Theme          string `json:"theme,omitempty"`
	Accent         string `json:"accent,omitempty"`
	Animations     bool   `json:"animations"`
	BeautifyPrompt string `json:"beautifyPrompt,omitempty"`
	MeaningPrompt  string `json:"meaningPrompt,omitempty"`

	EditorThemeDark  string `json:"editorThemeDark,omitempty"`
	EditorThemeLight string `json:"editorThemeLight,omitempty"`

	DebugPanel bool `json:"debugPanel"`
}

// ClientJSONAccount holds the active user identity.
type ClientJSONAccount struct {
	ActiveUserURN string `json:"activeUserUrn,omitempty"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Path helpers
// ─────────────────────────────────────────────────────────────────────────────

// ClientJSONPath returns the full path to ~/.notx/client.json.
func ClientJSONPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", fmt.Errorf("clientconfig: resolve notx dir: %w", err)
	}
	return filepath.Join(dir, "client.json"), nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Load / Save
// ─────────────────────────────────────────────────────────────────────────────

// LoadClientJSON reads ~/.notx/client.json and returns the parsed ClientJSON.
//
// If the file does not exist a default value with source "local" is returned
// so callers can always treat the result as valid without a nil check.
func LoadClientJSON() (*ClientJSON, error) {
	path, err := ClientJSONPath()
	if err != nil {
		return defaultClientJSON(), nil //nolint:nilerr // best-effort fallback
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return defaultClientJSON(), nil
		}
		return nil, fmt.Errorf("clientconfig: read %s: %w", path, err)
	}

	cfg := defaultClientJSON()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("clientconfig: parse %s: %w", path, err)
	}

	return cfg, nil
}

// SaveClientJSON atomically writes cfg to ~/.notx/client.json.
// The directory is created if it does not already exist.
// The output is pretty-printed JSON so it remains human-readable.
func SaveClientJSON(c *ClientJSON) error {
	dir, err := Dir()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("clientconfig: create config dir %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(c, "", "    ")
	if err != nil {
		return fmt.Errorf("clientconfig: marshal client.json: %w", err)
	}
	data = append(data, '\n')

	clientPath := filepath.Join(dir, "client.json")

	// Atomic write: write to a temp file then rename.
	tmp, err := os.CreateTemp(dir, ".client.*.json.tmp")
	if err != nil {
		return fmt.Errorf("clientconfig: create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("clientconfig: write client.json: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("clientconfig: sync client.json: %w", err)
	}
	tmp.Close()

	if err := os.Rename(tmpPath, clientPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("clientconfig: save client.json to %s: %w", clientPath, err)
	}

	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// defaultClientJSON returns a ClientJSON with sensible defaults (local mode).
func defaultClientJSON() *ClientJSON {
	return &ClientJSON{
		Settings: ClientJSONSettings{
			Source: "local",
		},
	}
}
