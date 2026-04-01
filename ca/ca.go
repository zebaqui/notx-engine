package ca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

const (
	caKeyFile  = "ca.key"
	caCertFile = "ca.crt"
)

// CA holds the authority Certificate Authority key-pair and certificate.
type CA struct {
	Key  *ecdsa.PrivateKey
	Cert *x509.Certificate
	// CertPEM is the PEM-encoded CA certificate (public, distributable).
	CertPEM []byte
}

// LoadOrGenerate loads the CA from caDir, or generates a new one if it doesn't exist.
// caDir is created if it does not exist.
func LoadOrGenerate(caDir string) (*CA, error) {
	if err := os.MkdirAll(caDir, 0o700); err != nil {
		return nil, fmt.Errorf("ca: create dir %q: %w", caDir, err)
	}

	keyPath := filepath.Join(caDir, caKeyFile)
	certPath := filepath.Join(caDir, caCertFile)

	_, keyErr := os.Stat(keyPath)
	_, certErr := os.Stat(certPath)

	if os.IsNotExist(keyErr) || os.IsNotExist(certErr) {
		return generate(caDir)
	}
	if keyErr != nil {
		return nil, fmt.Errorf("ca: stat key: %w", keyErr)
	}
	if certErr != nil {
		return nil, fmt.Errorf("ca: stat cert: %w", certErr)
	}
	return load(keyPath, certPath)
}

// generate creates a new EC P-256 CA key-pair and self-signed certificate,
// writes them to caDir, and returns the loaded CA.
func generate(caDir string) (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("ca: generate key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("ca: generate serial: %w", err)
	}

	now := time.Now().UTC()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "notx Authority CA",
			Organization: []string{"notx"},
		},
		NotBefore:             now.Add(-time.Minute),              // small clock-skew tolerance
		NotAfter:              now.Add(10 * 365 * 24 * time.Hour), // 10 years
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("ca: create certificate: %w", err)
	}

	// Write key (mode 0600).
	keyPath := filepath.Join(caDir, caKeyFile)
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("ca: marshal key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return nil, fmt.Errorf("ca: write key: %w", err)
	}

	// Write cert (mode 0644).
	certPath := filepath.Join(caDir, caCertFile)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return nil, fmt.Errorf("ca: write cert: %w", err)
	}

	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, fmt.Errorf("ca: parse generated cert: %w", err)
	}

	return &CA{Key: key, Cert: cert, CertPEM: certPEM}, nil
}

// load reads an existing CA key and certificate from disk.
func load(keyPath, certPath string) (*CA, error) {
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("ca: read key: %w", err)
	}
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("ca: read cert: %w", err)
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, fmt.Errorf("ca: decode key PEM: no block found")
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("ca: parse key: %w", err)
	}

	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return nil, fmt.Errorf("ca: decode cert PEM: no block found")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("ca: parse cert: %w", err)
	}

	return &CA{Key: key, Cert: cert, CertPEM: certPEM}, nil
}

// SignCSR signs a PKCS#10 CSR with the CA, using the provided subject CN,
// validity TTL, and SAN DNS name. Returns the signed certificate in PEM form
// and the certificate's serial number as a hex string.
func (ca *CA) SignCSR(csrDER []byte, cn string, sanDNS string, ttl time.Duration) (certPEM []byte, serialHex string, err error) {
	csr, err := x509.ParseCertificateRequest(csrDER)
	if err != nil {
		return nil, "", fmt.Errorf("ca: parse CSR: %w", err)
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, "", fmt.Errorf("ca: CSR signature invalid: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, "", fmt.Errorf("ca: generate serial: %w", err)
	}

	now := time.Now().UTC()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(ttl),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	if sanDNS != "" {
		tmpl.DNSNames = []string{sanDNS}
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Cert, csr.PublicKey, ca.Key)
	if err != nil {
		return nil, "", fmt.Errorf("ca: sign certificate: %w", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	serialHex = fmt.Sprintf("%x", serial.Bytes())
	return certPEM, serialHex, nil
}
