package clientconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Types
// ─────────────────────────────────────────────────────────────────────────────

// Shortcut represents a named alias for a note URN. Shortcuts are stored
// locally in ~/.notx/shortcuts.json and are used by commands such as
// `notx pull` to refer to frequently accessed notes by a human-readable name.
type Shortcut struct {
	// URN is the fully-qualified note URN, e.g. "urn:notx:note:1a9670dd-...".
	URN string `json:"urn"`

	// Name is the note's own name field as returned by the server.
	Name string `json:"name"`

	// CreatedAt is the RFC3339 UTC timestamp at which the shortcut was created.
	CreatedAt string `json:"created_at"`

	// Description is a human-readable summary, auto-filled as
	// "ProjectName / FolderName" or just the note name when no project context
	// is available.
	Description string `json:"description"`
}

// Shortcuts is the full map stored in ~/.notx/shortcuts.json.
// The map key is the shortcut alias (e.g. "my-meeting").
type Shortcuts map[string]*Shortcut

// ─────────────────────────────────────────────────────────────────────────────
// Path resolution
// ─────────────────────────────────────────────────────────────────────────────

// ShortcutsPath returns the full path to the shortcuts file: ~/.notx/shortcuts.json.
func ShortcutsPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "shortcuts.json"), nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Load / Save
// ─────────────────────────────────────────────────────────────────────────────

// LoadShortcuts reads ~/.notx/shortcuts.json and returns the parsed Shortcuts
// map. If the file does not yet exist, an empty Shortcuts map is returned with
// a nil error so that callers can treat a missing file as having no shortcuts.
func LoadShortcuts() (Shortcuts, error) {
	path, err := ShortcutsPath()
	if err != nil {
		return Shortcuts{}, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Shortcuts{}, nil
		}
		return nil, fmt.Errorf("clientconfig: read %s: %w", path, err)
	}

	var s Shortcuts
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("clientconfig: parse %s: %w", path, err)
	}

	return s, nil
}

// SaveShortcuts writes s to ~/.notx/shortcuts.json, creating the directory if
// needed. The file is written atomically via a temp-file rename. The output is
// pretty-printed JSON for human readability.
func SaveShortcuts(s Shortcuts) error {
	dir, err := Dir()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("clientconfig: create config dir %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(s, "", "    ")
	if err != nil {
		return fmt.Errorf("clientconfig: marshal shortcuts: %w", err)
	}
	// Ensure the file ends with a newline.
	data = append(data, '\n')

	// Write to a temp file first, then rename for atomic replacement.
	scPath := filepath.Join(dir, "shortcuts.json")
	tmp, err := os.CreateTemp(dir, ".shortcuts.*.json.tmp")
	if err != nil {
		return fmt.Errorf("clientconfig: create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("clientconfig: write shortcuts: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("clientconfig: sync shortcuts: %w", err)
	}
	tmp.Close()

	if err := os.Rename(tmpPath, scPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("clientconfig: save shortcuts to %s: %w", scPath, err)
	}

	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Lookup helpers
// ─────────────────────────────────────────────────────────────────────────────

// Resolve looks up alias in the map and returns the corresponding Shortcut.
// It returns (shortcut, true) when found and (nil, false) when not.
func (s Shortcuts) Resolve(alias string) (*Shortcut, bool) {
	sc, ok := s[alias]
	return sc, ok
}

// FindByURN performs a reverse lookup: given a note URN it returns the alias
// that points to it. It returns ("", nil, false) when no matching shortcut
// exists. When multiple aliases share the same URN, the first match in an
// unspecified iteration order is returned.
func (s Shortcuts) FindByURN(urn string) (alias string, sc *Shortcut, found bool) {
	for k, v := range s {
		if v != nil && v.URN == urn {
			return k, v, true
		}
	}
	return "", nil, false
}

// ─────────────────────────────────────────────────────────────────────────────
// Validation
// ─────────────────────────────────────────────────────────────────────────────

// validShortcutName matches alias strings that consist solely of lowercase
// ASCII letters, digits, and hyphens — but does not permit leading or trailing
// hyphens (those are rejected separately for a clearer error message).
var validShortcutName = regexp.MustCompile(`^[a-z0-9-]+$`)

// ValidateShortcutName checks that name is a well-formed shortcut alias.
// A valid alias:
//   - is non-empty,
//   - contains only lowercase letters (a–z), digits (0–9), and hyphens (-),
//   - does not start with a hyphen,
//   - does not end with a hyphen.
func ValidateShortcutName(name string) error {
	if name == "" {
		return errors.New("clientconfig: shortcut name must not be empty")
	}
	if name[0] == '-' {
		return fmt.Errorf("clientconfig: shortcut name %q must not start with a hyphen", name)
	}
	if name[len(name)-1] == '-' {
		return fmt.Errorf("clientconfig: shortcut name %q must not end with a hyphen", name)
	}
	if !validShortcutName.MatchString(name) {
		return fmt.Errorf("clientconfig: shortcut name %q contains invalid characters (only lowercase letters, digits, and hyphens are allowed)", name)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Mutation
// ─────────────────────────────────────────────────────────────────────────────

// Add validates alias via ValidateShortcutName and then inserts or replaces
// the entry in s. The Shortcut's CreatedAt field is set to the current UTC
// time in RFC3339 format when it is not already populated.
//
// Add returns an error only when the alias fails validation. Conflict checking
// (i.e. whether an alias already exists and whether the caller should prompt
// the user) is the responsibility of the caller.
func (s Shortcuts) Add(alias string, sc *Shortcut) error {
	if err := ValidateShortcutName(alias); err != nil {
		return err
	}
	if sc.CreatedAt == "" {
		sc.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	s[alias] = sc
	return nil
}
