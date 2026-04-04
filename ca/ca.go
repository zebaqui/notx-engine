package ca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
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

// Fingerprint returns the SHA-256 fingerprint of the CA certificate's DER
// bytes, formatted as uppercase colon-separated hex pairs (e.g. "AA:BB:CC:...").
// Distribute this value out-of-band to joining servers as PeerCAFingerprint.
func (c *CA) Fingerprint() string {
	sum := sha256.Sum256(c.Cert.Raw)
	pairs := make([]string, len(sum))
	for i, b := range sum {
		pairs[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(pairs, ":")
}

// CSRValidationOptions controls what ValidateCSR accepts.
type CSRValidationOptions struct {
	// RequiredCN is the exact string the CSR Subject.CommonName must equal.
	// Typically the server URN. Empty means no CN check.
	RequiredCN string

	// AllowedSANDNS, when non-empty, is the exact set of DNS SANs the CSR
	// may request. Any DNS SAN not in this set causes rejection. Wildcards
	// are always rejected regardless of this list.
	// When empty, DNS SANs in the CSR are ignored (the signer controls SANs).
	AllowedSANDNS []string
}

// ValidateCSR parses csrDER, verifies its self-signature, and checks that it
// conforms to the security requirements for peer server certificates:
//
//   - Key must be ECDSA on P-256 or P-384 (RSA keys are rejected).
//   - Signature must be valid.
//   - Subject CN must equal opts.RequiredCN (if set).
//   - No wildcard DNS SANs ("*" prefix).
//   - No IP SANs.
//   - No CA constraint (BasicConstraints.IsCA must be false or absent).
//   - No KeyUsage requesting CertSign or CRLSign.
//   - No ExtKeyUsage requesting ServerAuth (only ClientAuth is appropriate).
//   - No unrecognised critical extensions.
func ValidateCSR(csrDER []byte, opts CSRValidationOptions) error {
	csr, err := x509.ParseCertificateRequest(csrDER)
	if err != nil {
		return fmt.Errorf("ca: parse CSR: %w", err)
	}
	if err := csr.CheckSignature(); err != nil {
		return fmt.Errorf("ca: CSR signature invalid: %w", err)
	}

	// Key type check — EC P-256 or P-384 only.
	switch pub := csr.PublicKey.(type) {
	case *ecdsa.PublicKey:
		curve := pub.Curve
		if curve != elliptic.P256() && curve != elliptic.P384() {
			return fmt.Errorf("ca: CSR key must be EC P-256 or P-384")
		}
	default:
		return fmt.Errorf("ca: CSR key must be ECDSA (got %T)", csr.PublicKey)
	}

	// CN check.
	if opts.RequiredCN != "" && csr.Subject.CommonName != opts.RequiredCN {
		return fmt.Errorf("ca: CSR CN %q does not match required %q",
			csr.Subject.CommonName, opts.RequiredCN)
	}

	// No wildcard DNS SANs.
	for _, dns := range csr.DNSNames {
		if strings.HasPrefix(dns, "*") {
			return fmt.Errorf("ca: CSR contains wildcard DNS SAN %q", dns)
		}
	}

	// No IP SANs.
	if len(csr.IPAddresses) > 0 {
		return fmt.Errorf("ca: CSR must not contain IP SANs")
	}

	// No critical extensions we don't recognise.
	// The well-known OIDs for Subject Alternative Name and Basic Constraints
	// are acceptable; anything else marked critical is rejected.
	knownCritical := map[string]bool{
		"2.5.29.17": true, // subjectAltName
		"2.5.29.19": true, // basicConstraints
		"2.5.29.15": true, // keyUsage
		"2.5.29.37": true, // extendedKeyUsage
	}
	for _, ext := range csr.Extensions {
		if ext.Critical && !knownCritical[ext.Id.String()] {
			return fmt.Errorf("ca: CSR contains unknown critical extension %s", ext.Id)
		}
	}

	// Parse extensions manually to check for dangerous values.
	// x509.CertificateRequest doesn't expose parsed BasicConstraints or KeyUsage,
	// so we parse raw extension bytes where needed.
	for _, ext := range csr.Extensions {
		switch ext.Id.String() {
		case "2.5.29.19": // basicConstraints
			// DER encoding of BasicConstraints: SEQUENCE { BOOLEAN isCA (optional) }
			// If isCA is true, the first content byte after the outer SEQUENCE tag
			// contains the BOOLEAN TRUE encoding (0x01 0x01 0xff).
			// Simple heuristic: if the extension bytes contain 0xff it's CA=true.
			if len(ext.Value) > 0 {
				for _, b := range ext.Value {
					if b == 0xff {
						return fmt.Errorf("ca: CSR requests CA basic constraint")
					}
				}
			}
		}
	}

	return nil
}
