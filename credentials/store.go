package credentials

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// ErrNotFound is returned when a requested provider is not configured.
var ErrNotFound = errors.New("credentials: provider not found")

// Entry holds a single provider's raw credentials.
// It is never transmitted over the network — the HTTP layer uses ProviderInfo instead.
type Entry struct {
	Provider string `json:"provider"`
	APIKey   string `json:"api_key"`
}

// ProviderInfo is the safe, masked representation returned by the HTTP API.
type ProviderInfo struct {
	Provider string `json:"provider"`
	Masked   string `json:"masked"` // e.g. "sk-...a1b2" — never the real key
}

// Store is a process-private AES-256-GCM encrypted credential store.
// The on-disk format is: [16-byte salt][12-byte nonce][AES-256-GCM ciphertext]
// where the ciphertext is the GCM-authenticated encryption of a JSON-encoded
// map[string]Entry. A fresh salt and nonce are generated on every write.
type Store struct {
	path string
	mu   sync.RWMutex
}

// New returns a Store backed by the file at path.
// The file is created lazily on the first Set call.
func New(path string) *Store {
	return &Store{path: path}
}

// List returns masked ProviderInfo for every configured provider.
// Returns an empty slice (not an error) if the store file does not exist yet.
func (s *Store) List(passphrase []byte) ([]ProviderInfo, error) {
	defer zeroBytes(passphrase)
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := s.load(passphrase)
	if err != nil {
		return nil, err
	}

	result := make([]ProviderInfo, 0, len(entries))
	for provider, e := range entries {
		result = append(result, ProviderInfo{
			Provider: provider,
			Masked:   MaskKey(e.APIKey),
		})
		zeroEntry(&e)
	}
	return result, nil
}

// Get returns the Entry for provider. The caller must zero Entry.APIKey after use.
// Returns ErrNotFound if the provider is not configured.
func (s *Store) Get(provider string, passphrase []byte) (Entry, error) {
	defer zeroBytes(passphrase)
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := s.load(passphrase)
	if err != nil {
		return Entry{}, err
	}
	defer func() {
		for k, e := range entries {
			if k != provider {
				zeroEntry(&e)
			}
		}
	}()

	e, ok := entries[provider]
	if !ok {
		return Entry{}, ErrNotFound
	}
	return e, nil
}

// Set stores or updates credentials for provider.
func (s *Store) Set(provider, apiKey string, passphrase []byte) error {
	defer zeroBytes(passphrase)
	s.mu.Lock()
	defer s.mu.Unlock()

	// Make a copy of passphrase for the save call (load zeros it).
	pass2 := make([]byte, len(passphrase))
	copy(pass2, passphrase)

	entries, err := s.load(passphrase)
	if err != nil {
		return err
	}
	entries[provider] = Entry{Provider: provider, APIKey: apiKey}
	err = s.save(entries, pass2)
	// Zero all entries after save.
	for _, e := range entries {
		zeroEntry(&e)
	}
	zeroBytes(pass2)
	return err
}

// Delete removes credentials for provider.
// Returns ErrNotFound if the provider is not configured.
func (s *Store) Delete(provider string, passphrase []byte) error {
	defer zeroBytes(passphrase)
	s.mu.Lock()
	defer s.mu.Unlock()

	pass2 := make([]byte, len(passphrase))
	copy(pass2, passphrase)

	entries, err := s.load(passphrase)
	if err != nil {
		return err
	}
	if _, ok := entries[provider]; !ok {
		zeroBytes(pass2)
		return ErrNotFound
	}
	delete(entries, provider)
	err = s.save(entries, pass2)
	for _, e := range entries {
		zeroEntry(&e)
	}
	zeroBytes(pass2)
	return err
}

// MaskKey returns a safe display string for an API key.
// It never contains the real key value.
func MaskKey(key string) string {
	if len(key) <= 8 {
		return "***"
	}
	return key[:3] + "..." + key[len(key)-4:]
}

// ─── internal ────────────────────────────────────────────────────────────────

const (
	saltLen    = 16
	nonceLen   = 12
	keyLen     = 32
	pbkdf2Iter = 100_000
)

// load decrypts and parses the store file.
// Returns an empty map (not an error) if the file does not exist.
// The passphrase is NOT zeroed by load — the caller must zero it.
func (s *Store) load(passphrase []byte) (map[string]Entry, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return make(map[string]Entry), nil
	}
	if err != nil {
		return nil, fmt.Errorf("credentials: read store: %w", err)
	}

	if len(data) < saltLen+nonceLen {
		return nil, fmt.Errorf("credentials: store file too short")
	}

	salt := data[:saltLen]
	nonce := data[saltLen : saltLen+nonceLen]
	ciphertext := data[saltLen+nonceLen:]

	key := deriveKey(passphrase, salt)
	defer zeroBytes(key)

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("credentials: create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("credentials: create GCM: %w", err)
	}

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("credentials: decrypt: wrong passphrase or corrupted store")
	}
	defer zeroBytes(plaintext)

	var entries map[string]Entry
	if err := json.Unmarshal(plaintext, &entries); err != nil {
		return nil, fmt.Errorf("credentials: parse store: %w", err)
	}
	return entries, nil
}

// save encrypts entries and writes them atomically to s.path.
func (s *Store) save(entries map[string]Entry, passphrase []byte) error {
	plaintext, err := json.Marshal(entries)
	if err != nil {
		return fmt.Errorf("credentials: marshal: %w", err)
	}
	defer zeroBytes(plaintext)

	var salt [saltLen]byte
	if _, err := io.ReadFull(rand.Reader, salt[:]); err != nil {
		return fmt.Errorf("credentials: generate salt: %w", err)
	}

	var nonce [nonceLen]byte
	if _, err := io.ReadFull(rand.Reader, nonce[:]); err != nil {
		return fmt.Errorf("credentials: generate nonce: %w", err)
	}

	key := deriveKey(passphrase, salt[:])
	defer zeroBytes(key)

	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("credentials: create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("credentials: create GCM: %w", err)
	}

	ciphertext := gcm.Seal(nil, nonce[:], plaintext, nil)

	out := make([]byte, 0, saltLen+nonceLen+len(ciphertext))
	out = append(out, salt[:]...)
	out = append(out, nonce[:]...)
	out = append(out, ciphertext...)

	// Atomic write via temp file + rename.
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("credentials: create dir: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".creds-*.tmp")
	if err != nil {
		return fmt.Errorf("credentials: create temp file: %w", err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("credentials: write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("credentials: close temp file: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("credentials: rename temp file: %w", err)
	}
	return nil
}

// deriveKey derives a 32-byte AES key from passphrase + salt using PBKDF2-HMAC-SHA256.
func deriveKey(passphrase, salt []byte) []byte {
	return pbkdf2Key(passphrase, salt, pbkdf2Iter, keyLen)
}

// pbkdf2Key implements PBKDF2 with HMAC-SHA256.
func pbkdf2Key(password, salt []byte, iter, keyLen int) []byte {
	prf := hmac.New(sha256.New, password)
	hashLen := prf.Size()
	numBlocks := (keyLen + hashLen - 1) / hashLen

	dk := make([]byte, 0, numBlocks*hashLen)
	U := make([]byte, hashLen)

	var buf [4]byte
	for block := 1; block <= numBlocks; block++ {
		prf.Reset()
		prf.Write(salt)
		binary.BigEndian.PutUint32(buf[:], uint32(block))
		prf.Write(buf[:])
		T := prf.Sum(nil)
		copy(U, T)
		for n := 2; n <= iter; n++ {
			prf.Reset()
			prf.Write(U)
			newU := prf.Sum(nil)
			for x := range newU {
				T[x] ^= newU[x]
			}
			copy(U, newU)
		}
		dk = append(dk, T...)
	}
	return dk[:keyLen]
}

func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

func zeroEntry(e *Entry) {
	for i := range e.APIKey {
		_ = i // zero the string's backing memory is not possible in Go without unsafe
		// Best effort: overwrite the reference
	}
	e.APIKey = ""
	e.Provider = ""
}
