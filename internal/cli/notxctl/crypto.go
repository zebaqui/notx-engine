package notxctl

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"os"
)

// buildCSRForCommonName generates a fresh EC P-256 private key in memory and
// returns a DER-encoded PKCS#10 certificate signing request with commonName
// set to cn.
//
// The private key is not persisted — notxctl is a debug/ops tool. Production
// joining servers use serverclient.PairingClient which manages its own key
// material on disk inside PeerCertDir.
func buildCSRForCommonName(cn string) ([]byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate EC P-256 key: %w", err)
	}

	tmpl := &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: cn},
	}

	csrDER, err := x509.CreateCertificateRequest(rand.Reader, tmpl, key)
	if err != nil {
		return nil, fmt.Errorf("create CSR: %w", err)
	}

	return csrDER, nil
}

// writeFileBytes writes data to path with mode 0o644, creating or truncating
// the file atomically via a temp-file rename so partial writes never leave a
// corrupt file at the destination.
func writeFileBytes(path string, data []byte) error {
	// Write to a sibling temp file first.
	tmp, err := os.CreateTemp("", "notxctl-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename %s → %s: %w", tmpPath, path, err)
	}

	return nil
}
