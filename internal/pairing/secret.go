package pairing

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/zebaqui/notx-engine/repo"
)

const secretPrefix = "NTXP-"

// secretEncoding is base32 without padding, uppercase.
var secretEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// GenerateSecret generates a new cryptographically random NTXP-... pairing
// secret. Returns the plaintext (to be shown once to the admin) and a
// repo.PairingSecret record with only the bcrypt hash persisted.
func GenerateSecret(label string, ttl time.Duration) (plaintext string, record *repo.PairingSecret, err error) {
	// ID: 6 random bytes → 12-char hex string (lookup key, not secret).
	idBytes := make([]byte, 6)
	if _, err := rand.Read(idBytes); err != nil {
		return "", nil, fmt.Errorf("pairing: generate id: %w", err)
	}
	id := fmt.Sprintf("%x", idBytes)

	// Entropy: 13 random bytes → base32 (no padding) → first 20 chars → 4 groups of 5.
	// 13 bytes = 104 bits; base32 gives ceil(104/5)=21 chars, so we take first 20.
	raw := make([]byte, 13)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("pairing: generate entropy: %w", err)
	}
	encoded := secretEncoding.EncodeToString(raw)[:20]
	// Format: NTXP-{id12}-AAAAA-BBBBB-CCCCC-DDDDD
	plaintext = secretPrefix + id + "-" + chunkString(encoded, 5, "-")

	hash, err := bcrypt.GenerateFromPassword([]byte(plaintext), 8)
	if err != nil {
		return "", nil, fmt.Errorf("pairing: hash secret: %w", err)
	}

	record = &repo.PairingSecret{
		ID:         id,
		LabelHint:  label,
		HashBcrypt: string(hash),
		ExpiresAt:  time.Now().UTC().Add(ttl),
	}
	return plaintext, record, nil
}

// ExtractSecretID parses a token of the form "NTXP-{id12}-..." and returns
// the 12-char hex ID segment and true. Returns "", false if the token does
// not match the expected format.
func ExtractSecretID(plaintext string) (string, bool) {
	if !strings.HasPrefix(plaintext, secretPrefix) {
		return "", false
	}
	rest := strings.TrimPrefix(plaintext, secretPrefix)
	// rest is "{id12}-{g1}-{g2}-{g3}-{g4}"
	// id is the first segment before the first "-"
	parts := strings.SplitN(rest, "-", 2)
	if len(parts) != 2 || len(parts[0]) != 12 {
		return "", false
	}
	return parts[0], true
}

// chunkString splits s into chunks of size n, joined by sep.
func chunkString(s string, n int, sep string) string {
	var chunks []string
	for i := 0; i < len(s); i += n {
		end := i + n
		if end > len(s) {
			end = len(s)
		}
		chunks = append(chunks, s[i:end])
	}
	return strings.Join(chunks, sep)
}

// ─────────────────────────────────────────────────────────────────────────────
// File-backed PairingSecretStore
// ─────────────────────────────────────────────────────────────────────────────

type secretRecord struct {
	ID         string     `json:"id"`
	Label      string     `json:"label"`
	HashBcrypt string     `json:"hash_bcrypt"`
	ExpiresAt  time.Time  `json:"expires_at"`
	UsedAt     *time.Time `json:"used_at"`
}

// FileSecretStore is a file-backed implementation of repo.PairingSecretStore.
// Each secret is stored as a JSON file under dir.
type FileSecretStore struct {
	dir string
	mu  sync.Mutex
}

// NewFileSecretStore creates (or opens) a secret store rooted at dir.
func NewFileSecretStore(dir string) (*FileSecretStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("secret store: create dir: %w", err)
	}
	return &FileSecretStore{dir: dir}, nil
}

func (s *FileSecretStore) path(id string) string {
	return filepath.Join(s.dir, id+".json")
}

func (s *FileSecretStore) AddSecret(ctx context.Context, ps *repo.PairingSecret) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	rec := secretRecord{
		ID:         ps.ID,
		Label:      ps.LabelHint,
		HashBcrypt: ps.HashBcrypt,
		ExpiresAt:  ps.ExpiresAt,
		UsedAt:     ps.UsedAt,
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("secret store: marshal: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return os.WriteFile(s.path(ps.ID), data, 0o600)
}

// ConsumeSecret performs an O(1) file lookup by extracting the ID from the
// token, reading exactly one JSON file, validating it, and marking it used.
// Returns repo.ErrNotFound if the token is malformed, the file does not exist,
// the secret is already used, expired, or the bcrypt comparison fails.
func (s *FileSecretStore) ConsumeSecret(ctx context.Context, plaintext string) (*repo.PairingSecret, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Extract the ID from the token — O(1) file lookup instead of directory scan.
	id, ok := ExtractSecretID(plaintext)
	if !ok {
		return nil, fmt.Errorf("%w: malformed pairing secret token", repo.ErrNotFound)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.path(id)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: secret not found", repo.ErrNotFound)
		}
		return nil, fmt.Errorf("secret store: read secret: %w", err)
	}

	var rec secretRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("secret store: unmarshal secret: %w", err)
	}

	now := time.Now().UTC()

	// Already used.
	if rec.UsedAt != nil {
		return nil, fmt.Errorf("%w: pairing secret already used", repo.ErrNotFound)
	}
	// Expired.
	if now.After(rec.ExpiresAt) {
		return nil, fmt.Errorf("%w: pairing secret expired", repo.ErrNotFound)
	}
	// Bcrypt compare (one call, not N).
	if err := bcrypt.CompareHashAndPassword([]byte(rec.HashBcrypt), []byte(plaintext)); err != nil {
		return nil, fmt.Errorf("%w: pairing secret invalid", repo.ErrNotFound)
	}

	// Mark used atomically.
	rec.UsedAt = &now
	updated, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("secret store: marshal used: %w", err)
	}
	if err := os.WriteFile(path, updated, 0o600); err != nil {
		return nil, fmt.Errorf("secret store: write used: %w", err)
	}

	return &repo.PairingSecret{
		ID:         rec.ID,
		LabelHint:  rec.Label,
		HashBcrypt: rec.HashBcrypt,
		ExpiresAt:  rec.ExpiresAt,
		UsedAt:     &now,
	}, nil
}

// PruneExpired removes all expired secret records from storage.
func (s *FileSecretStore) PruneExpired(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return fmt.Errorf("secret store: read dir: %w", err)
	}

	now := time.Now().UTC()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(s.dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var rec secretRecord
		if err := json.Unmarshal(data, &rec); err != nil {
			continue
		}
		if now.After(rec.ExpiresAt) {
			_ = os.Remove(path)
		}
	}
	return nil
}
