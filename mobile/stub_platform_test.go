package mobile_test

import (
	"fmt"
	"sync"
)

// stubPlatform is an in-memory Platform implementation for tests.
type stubPlatform struct {
	mu      sync.Mutex
	keys    map[string][]byte
	certs   map[string][]byte
	config  map[string]string
	dataDir string
}

func newStubPlatform(t interface {
	TempDir() string
	Fatal(args ...interface{})
}) *stubPlatform {
	return &stubPlatform{
		keys:    make(map[string][]byte),
		certs:   make(map[string][]byte),
		config:  make(map[string]string),
		dataDir: t.TempDir(),
	}
}

func (s *stubPlatform) GenerateKey(alias string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := []byte("stub-public-key-" + alias)
	s.keys[alias] = key
	return key, nil
}

func (s *stubPlatform) Sign(alias string, digest []byte) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.keys[alias]; !ok {
		return nil, fmt.Errorf("stub: key not found: %s", alias)
	}
	return append([]byte("sig:"), digest...), nil
}

func (s *stubPlatform) BuildCSR(alias, commonName string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.keys[alias]; !ok {
		return nil, fmt.Errorf("stub: key not found: %s", alias)
	}
	return []byte("stub-csr:" + alias + ":" + commonName), nil
}

func (s *stubPlatform) PublicKeyDER(alias string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if k, ok := s.keys[alias]; ok {
		return k, nil
	}
	return nil, fmt.Errorf("stub: key not found: %s", alias)
}

func (s *stubPlatform) DeleteKey(alias string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.keys, alias)
	return nil
}

func (s *stubPlatform) HasKey(alias string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.keys[alias]
	return ok, nil
}

func (s *stubPlatform) StoreCert(alias string, certPEM []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.certs[alias] = certPEM
	return nil
}

func (s *stubPlatform) LoadCert(alias string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.certs[alias]; ok {
		return c, nil
	}
	return nil, fmt.Errorf("stub: cert not found: %s", alias)
}

func (s *stubPlatform) DeleteCert(alias string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.certs, alias)
	return nil
}

func (s *stubPlatform) HasCert(alias string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.certs[alias]
	return ok, nil
}

func (s *stubPlatform) GetConfig(key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.config[key], nil
}

func (s *stubPlatform) SetConfig(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config[key] = value
	return nil
}

func (s *stubPlatform) DataDir() (string, error) {
	return s.dataDir, nil
}
