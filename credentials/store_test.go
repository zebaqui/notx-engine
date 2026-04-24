package credentials

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// pp returns a fresh []byte passphrase from a string literal.
// Because every Store method zeros the slice it receives, callers must pass a
// new copy for every call — this helper makes that ergonomic.
func pp(s string) []byte { return []byte(s) }

// ─── TestNew_EmptyStore ───────────────────────────────────────────────────────

func TestNew_EmptyStore(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "creds.enc"))

	list, err := s.List(pp("any-pass"))
	if err != nil {
		t.Fatalf("List on non-existent file returned error: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty slice, got %d entries", len(list))
	}
}

// ─── TestSetAndGet ────────────────────────────────────────────────────────────

func TestSetAndGet(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "creds.enc"))

	const provider = "openai"
	const apiKey = "sk-supersecretkey1234"
	const pass = "correct-horse-battery-staple"

	if err := s.Set(provider, apiKey, pp(pass)); err != nil {
		t.Fatalf("Set: %v", err)
	}

	entry, err := s.Get(provider, pp(pass))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if entry.APIKey != apiKey {
		t.Errorf("APIKey mismatch: got %q, want %q", entry.APIKey, apiKey)
	}
	if entry.Provider != provider {
		t.Errorf("Provider mismatch: got %q, want %q", entry.Provider, provider)
	}
}

// ─── TestList_MasksKeys ───────────────────────────────────────────────────────

func TestList_MasksKeys(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "creds.enc"))

	const pass = "list-mask-pass"
	providers := map[string]string{
		"openai":    "sk-openai-plaintextkey9999",
		"anthropic": "sk-ant-plaintextkey8888",
	}

	for prov, key := range providers {
		if err := s.Set(prov, key, pp(pass)); err != nil {
			t.Fatalf("Set(%q): %v", prov, err)
		}
	}

	list, err := s.List(pp(pass))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(list))
	}

	for _, info := range list {
		plainKey, ok := providers[info.Provider]
		if !ok {
			t.Errorf("unexpected provider %q in list", info.Provider)
			continue
		}
		// The masked value must never equal the plaintext key.
		if info.Masked == plainKey {
			t.Errorf("provider %q: masked key equals plaintext key", info.Provider)
		}
		// The masked value must not be empty.
		if info.Masked == "" {
			t.Errorf("provider %q: masked key is empty", info.Provider)
		}
		// The masked value must not contain the full plaintext key as a substring.
		if len(plainKey) > 0 && contains(info.Masked, plainKey) {
			t.Errorf("provider %q: masked key %q contains plaintext key", info.Provider, info.Masked)
		}
	}
}

// contains reports whether sub is a substring of s (avoids importing strings).
func contains(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ─── TestGet_NotFound ─────────────────────────────────────────────────────────

func TestGet_NotFound(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "creds.enc"))

	// Store is empty — no Set calls at all.
	_, err := s.Get("nonexistent-provider", pp("pass"))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// ─── TestDelete ───────────────────────────────────────────────────────────────

func TestDelete(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "creds.enc"))

	const pass = "delete-pass"
	const provider = "openai"

	if err := s.Set(provider, "sk-todelete", pp(pass)); err != nil {
		t.Fatalf("Set: %v", err)
	}

	if err := s.Delete(provider, pp(pass)); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := s.Get(provider, pp(pass))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("after Delete: expected ErrNotFound, got %v", err)
	}
}

// ─── TestDelete_NotFound ──────────────────────────────────────────────────────

func TestDelete_NotFound(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "creds.enc"))

	err := s.Delete("ghost-provider", pp("pass"))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// ─── TestSet_Overwrite ────────────────────────────────────────────────────────

func TestSet_Overwrite(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "creds.enc"))

	const pass = "overwrite-pass"
	const provider = "openai"
	const firstKey = "sk-first-key-aaaa"
	const secondKey = "sk-second-key-bbbb"

	if err := s.Set(provider, firstKey, pp(pass)); err != nil {
		t.Fatalf("first Set: %v", err)
	}
	if err := s.Set(provider, secondKey, pp(pass)); err != nil {
		t.Fatalf("second Set: %v", err)
	}

	entry, err := s.Get(provider, pp(pass))
	if err != nil {
		t.Fatalf("Get after overwrite: %v", err)
	}
	if entry.APIKey != secondKey {
		t.Errorf("expected second key %q, got %q", secondKey, entry.APIKey)
	}
}

// ─── TestWrongPassphrase ──────────────────────────────────────────────────────

func TestWrongPassphrase(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "creds.enc"))

	if err := s.Set("openai", "sk-secret", pp("correct-passphrase")); err != nil {
		t.Fatalf("Set: %v", err)
	}

	_, err := s.Get("openai", pp("wrong-passphrase"))
	if err == nil {
		t.Fatal("expected an error when using wrong passphrase, got nil")
	}
	// Must not be ErrNotFound — the error must indicate a decrypt failure.
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("expected a decryption error, not ErrNotFound")
	}
}

// ─── TestMaskKey ──────────────────────────────────────────────────────────────

func TestMaskKey(t *testing.T) {
	cases := []struct {
		name string
		key  string
		want string
	}{
		{
			name: "empty string",
			key:  "",
			want: "***",
		},
		{
			name: "one character",
			key:  "x",
			want: "***",
		},
		{
			name: "exactly 8 characters",
			key:  "abcdefgh",
			want: "***",
		},
		{
			name: "7 characters",
			key:  "1234567",
			want: "***",
		},
		{
			name: "9 characters — just over threshold",
			key:  "123456789",
			// key[:3]="123", key[5:]="6789"
			want: "123...6789",
		},
		{
			name: "long OpenAI-style key",
			key:  "sk-abcdefghijklmnopqrstu",
			// key[:3]="sk-", key[len-4:]="rstu"
			want: "sk-...rstu",
		},
		{
			name: "long Anthropic-style key",
			key:  "sk-ant-api03-abcdefghijklmnop",
			// key[:3]="sk-", last 4="mnop"
			want: "sk-...mnop",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MaskKey(tc.key)
			if got != tc.want {
				t.Errorf("MaskKey(%q) = %q, want %q", tc.key, got, tc.want)
			}
		})
	}
}

// ─── TestConcurrentAccess ─────────────────────────────────────────────────────
//
// Run with: go test -race ./credentials/...

func TestConcurrentAccess(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "creds.enc"))

	const pass = "concurrent-pass"
	const iterations = 5

	// Seed the store with an initial entry so List has something to return.
	if err := s.Set("seed-provider", "sk-seed-value-12345", pp(pass)); err != nil {
		t.Fatalf("seed Set: %v", err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, iterations*2)

	// Writer goroutine: repeatedly calls Set.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			// Each call needs its own passphrase slice because Set zeros it.
			key := "sk-concurrent-key-writer"
			if err := s.Set("concurrent-provider", key, pp(pass)); err != nil {
				errs <- err
				return
			}
		}
	}()

	// Reader goroutine: repeatedly calls List.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			if _, err := s.List(pp(pass)); err != nil {
				// A decrypt error is acceptable here only if the writer raced a
				// write mid-read; the store uses a RWMutex so this should never
				// happen — treat any error as a failure.
				errs <- err
				return
			}
		}
	}()

	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("concurrent operation failed: %v", err)
		}
	}
}

// ─── TestStoreFile_CreatedLazily ──────────────────────────────────────────────

func TestStoreFile_CreatedLazily(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "creds.enc")

	s := New(path)

	// File must not exist yet — New is lazy.
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected store file to not exist before first Set, but Stat returned: %v", err)
	}

	if err := s.Set("openai", "sk-lazytest12345678", pp("lazy-pass")); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// File must exist now.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected store file to exist after Set, but Stat returned: %v", err)
	}
}
