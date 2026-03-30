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

	"github.com/zebaqui/notx-engine/internal/repo"
)

const secretPrefix = "NTXP-"

// secretEncoding is base32 without padding, uppercase.
var secretEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// GenerateSecret generates a new cryptographically random NTXP-... pairing
// secret. Returns the plaintext (to be shown once to the admin) and a
// repo.PairingSecret record with only the bcrypt hash persisted.
func GenerateSecret(label string, ttl time.Duration) (plaintext string, record *repo.PairingSecret, err error) {
	// 16 bytes = 128 bits of entropy, base32-encoded → 26 characters.
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("pairing: generate entropy: %w", err)
	}
	encoded := secretEncoding.EncodeToString(raw)
	// Format: NTXP-AAAAA-BBBBB-CCCCC-DDDDD-EEEEE (groups of 5)
	plaintext = secretPrefix + chunkString(encoded, 5, "-")

	hash, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.DefaultCost)
	if err != nil {
		return "", nil, fmt.Errorf("pairing: hash secret: %w", err)
	}

	// ID is a random 12-char hex string used as the storage key.
	idBytes := make([]byte, 6)
	if _, err := rand.Read(idBytes); err != nil {
		return "", nil, fmt.Errorf("pairing: generate id: %w", err)
	}
	id := fmt.Sprintf("sec_%x", idBytes)

	record = &repo.PairingSecret{
		ID:         id,
		LabelHint:  label,
		HashBcrypt: string(hash),
		ExpiresAt:  time.Now().UTC().Add(ttl),
	}
	return plaintext, record, nil
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

// ConsumeSecret iterates all unexpired, unused secrets and bcrypt-compares
// the plaintext against each hash. On a match it marks the record used and
// returns it. Returns repo.ErrNotFound if no valid secret matches.
func (s *FileSecretStore) ConsumeSecret(ctx context.Context, plaintext string) (*repo.PairingSecret, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("secret store: read dir: %w", err)
	}

	now := time.Now().UTC()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		var rec secretRecord
		if err := json.Unmarshal(data, &rec); err != nil {
			continue
		}
		// Skip expired.
		if now.After(rec.ExpiresAt) {
			continue
		}
		// Skip already-used.
		if rec.UsedAt != nil {
			continue
		}
		// Bcrypt compare (timing-safe).
		if err := bcrypt.CompareHashAndPassword([]byte(rec.HashBcrypt), []byte(plaintext)); err != nil {
			continue
		}
		// Match — mark used.
		t := now
		rec.UsedAt = &t
		updated, err := json.MarshalIndent(rec, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("secret store: marshal used: %w", err)
		}
		if err := os.WriteFile(filepath.Join(s.dir, e.Name()), updated, 0o600); err != nil {
			return nil, fmt.Errorf("secret store: write used: %w", err)
		}
		ps := &repo.PairingSecret{
			ID:         rec.ID,
			LabelHint:  rec.Label,
			HashBcrypt: rec.HashBcrypt,
			ExpiresAt:  rec.ExpiresAt,
			UsedAt:     rec.UsedAt,
		}
		return ps, nil
	}
	return nil, fmt.Errorf("%w: no valid pairing secret matched", repo.ErrNotFound)
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
