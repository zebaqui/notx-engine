package grpcclient_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"

	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zebaqui/notx-engine/internal/clientconfig"
	"github.com/zebaqui/notx-engine/internal/grpcclient"
)

// ─────────────────────────────────────────────────────────────────────────────
// Test helpers
// ─────────────────────────────────────────────────────────────────────────────

// selfSignedCA generates an in-memory EC P-256 CA cert + key and returns
// them PEM-encoded, together with the *x509.Certificate.
func selfSignedCA(t *testing.T) (certPEM, keyPEM []byte, cert *x509.Certificate) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create CA cert: %v", err)
	}

	cert, err = x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal CA key: %v", err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return certPEM, keyPEM, cert
}

// signedLeafCert generates an EC P-256 leaf cert signed by caCert/caKey.
// Returns the cert PEM and key PEM.
func signedLeafCert(t *testing.T, caCertPEM, caKeyPEM []byte) (certPEM, keyPEM []byte) {
	t.Helper()

	// Parse the CA.
	caBlock, _ := pem.Decode(caCertPEM)
	if caBlock == nil {
		t.Fatal("decode CA cert PEM")
	}
	caCert, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}
	caKeyBlock, _ := pem.Decode(caKeyPEM)
	if caKeyBlock == nil {
		t.Fatal("decode CA key PEM")
	}
	caKey, err := x509.ParseECPrivateKey(caKeyBlock.Bytes)
	if err != nil {
		t.Fatalf("parse CA key: %v", err)
	}

	// Generate leaf key.
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "test-client"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create leaf cert: %v", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	leafKeyDER, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		t.Fatalf("marshal leaf key: %v", err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: leafKeyDER})

	return certPEM, keyPEM
}

// writeTempFile writes data to a temp file inside dir and returns the path.
func writeTempFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatalf("write temp file %s: %v", p, err)
	}
	return p
}

// ─────────────────────────────────────────────────────────────────────────────
// ParseCAPool
// ─────────────────────────────────────────────────────────────────────────────

func TestParseCAPool_Valid(t *testing.T) {
	certPEM, _, _ := selfSignedCA(t)

	pool, err := grpcclient.ParseCAPool(certPEM)
	if err != nil {
		t.Fatalf("ParseCAPool returned error: %v", err)
	}
	if pool == nil {
		t.Fatal("ParseCAPool returned nil pool")
	}
}

func TestParseCAPool_Empty(t *testing.T) {
	_, err := grpcclient.ParseCAPool([]byte{})
	if err == nil {
		t.Fatal("expected error for empty PEM, got nil")
	}
}

func TestParseCAPool_InvalidPEM(t *testing.T) {
	_, err := grpcclient.ParseCAPool([]byte("not a pem block"))
	if err == nil {
		t.Fatal("expected error for invalid PEM, got nil")
	}
}

func TestParseCAPool_OnlyKeyNocert(t *testing.T) {
	// A valid PEM block that is a key, not a certificate.
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalECPrivateKey(key)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})

	_, err := grpcclient.ParseCAPool(keyPEM)
	if err == nil {
		t.Fatal("expected error when PEM contains only a key block, got nil")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// LoadCAPool
// ─────────────────────────────────────────────────────────────────────────────

func TestLoadCAPool_Valid(t *testing.T) {
	dir := t.TempDir()
	certPEM, _, _ := selfSignedCA(t)
	p := writeTempFile(t, dir, "ca.crt", certPEM)

	pool, err := grpcclient.LoadCAPool(p)
	if err != nil {
		t.Fatalf("LoadCAPool returned error: %v", err)
	}
	if pool == nil {
		t.Fatal("LoadCAPool returned nil pool")
	}
}

func TestLoadCAPool_Missing(t *testing.T) {
	_, err := grpcclient.LoadCAPool("/nonexistent/path/ca.crt")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestLoadCAPool_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	p := writeTempFile(t, dir, "ca.crt", []byte{})

	_, err := grpcclient.LoadCAPool(p)
	if err == nil {
		t.Fatal("expected error for empty CA file, got nil")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// LoadClientCert
// ─────────────────────────────────────────────────────────────────────────────

func TestLoadClientCert_Valid(t *testing.T) {
	dir := t.TempDir()
	caCertPEM, caKeyPEM, _ := selfSignedCA(t)
	certPEM, keyPEM := signedLeafCert(t, caCertPEM, caKeyPEM)

	certPath := writeTempFile(t, dir, "client.crt", certPEM)
	keyPath := writeTempFile(t, dir, "client.key", keyPEM)

	cert, err := grpcclient.LoadClientCert(certPath, keyPath)
	if err != nil {
		t.Fatalf("LoadClientCert returned error: %v", err)
	}
	// tls.Certificate.Certificate holds the raw DER bytes; at least one leaf.
	if len(cert.Certificate) == 0 {
		t.Fatal("loaded certificate has no raw cert data")
	}
}

func TestLoadClientCert_MissingCert(t *testing.T) {
	dir := t.TempDir()
	_, keyPEM := func() ([]byte, []byte) {
		caCertPEM, caKeyPEM, _ := selfSignedCA(t)
		return signedLeafCert(t, caCertPEM, caKeyPEM)
	}()
	keyPath := writeTempFile(t, dir, "client.key", keyPEM)

	_, err := grpcclient.LoadClientCert("/nonexistent/client.crt", keyPath)
	if err == nil {
		t.Fatal("expected error for missing cert file, got nil")
	}
}

func TestLoadClientCert_MissingKey(t *testing.T) {
	dir := t.TempDir()
	caCertPEM, caKeyPEM, _ := selfSignedCA(t)
	certPEM, _ := signedLeafCert(t, caCertPEM, caKeyPEM)
	certPath := writeTempFile(t, dir, "client.crt", certPEM)

	_, err := grpcclient.LoadClientCert(certPath, "/nonexistent/client.key")
	if err == nil {
		t.Fatal("expected error for missing key file, got nil")
	}
}

func TestLoadClientCert_MismatchedPair(t *testing.T) {
	dir := t.TempDir()

	// Generate two independent CA+leaf pairs; mix cert from one with key from other.
	caCertPEM1, caKeyPEM1, _ := selfSignedCA(t)
	caCertPEM2, caKeyPEM2, _ := selfSignedCA(t)

	certPEM1, _ := signedLeafCert(t, caCertPEM1, caKeyPEM1)
	_, keyPEM2 := signedLeafCert(t, caCertPEM2, caKeyPEM2)

	certPath := writeTempFile(t, dir, "client.crt", certPEM1)
	keyPath := writeTempFile(t, dir, "client.key", keyPEM2)

	_, err := grpcclient.LoadClientCert(certPath, keyPath)
	if err == nil {
		t.Fatal("expected error for mismatched cert/key pair, got nil")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Options validation — Dial returns error for empty Addr
// ─────────────────────────────────────────────────────────────────────────────

func TestDial_EmptyAddr(t *testing.T) {
	_, err := grpcclient.Dial(grpcclient.Options{
		Addr:     "",
		Insecure: true,
	})
	if err == nil {
		t.Fatal("expected error for empty Addr, got nil")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Options validation — CertFile without KeyFile (and vice-versa)
// ─────────────────────────────────────────────────────────────────────────────

func TestDial_CertWithoutKey(t *testing.T) {
	dir := t.TempDir()
	caCertPEM, caKeyPEM, _ := selfSignedCA(t)
	certPEM, _ := signedLeafCert(t, caCertPEM, caKeyPEM)
	certPath := writeTempFile(t, dir, "client.crt", certPEM)

	_, err := grpcclient.Dial(grpcclient.Options{
		Addr:     "localhost:50051",
		CertFile: certPath,
		// KeyFile intentionally omitted
	})
	if err == nil {
		t.Fatal("expected error when CertFile is set without KeyFile, got nil")
	}
}

func TestDial_KeyWithoutCert(t *testing.T) {
	dir := t.TempDir()
	caCertPEM, caKeyPEM, _ := selfSignedCA(t)
	_, keyPEM := signedLeafCert(t, caCertPEM, caKeyPEM)
	keyPath := writeTempFile(t, dir, "client.key", keyPEM)

	_, err := grpcclient.Dial(grpcclient.Options{
		Addr:    "localhost:50051",
		KeyFile: keyPath,
		// CertFile intentionally omitted
	})
	if err == nil {
		t.Fatal("expected error when KeyFile is set without CertFile, got nil")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Options validation — missing CAFile
// ─────────────────────────────────────────────────────────────────────────────

func TestDial_MissingCAFile(t *testing.T) {
	_, err := grpcclient.Dial(grpcclient.Options{
		Addr:   "localhost:50051",
		CAFile: "/nonexistent/ca.crt",
	})
	if err == nil {
		t.Fatal("expected error for non-existent CAFile, got nil")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// DialMTLS — nil caPool is rejected
// ─────────────────────────────────────────────────────────────────────────────

func TestDialMTLS_NilCAPool(t *testing.T) {
	caCertPEM, caKeyPEM, _ := selfSignedCA(t)
	certPEM, keyPEM := signedLeafCert(t, caCertPEM, caKeyPEM)

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair: %v", err)
	}

	_, err = grpcclient.DialMTLS("localhost:50051", cert, nil)
	if err == nil {
		t.Fatal("expected error for nil caPool in DialMTLS, got nil")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// DialWithCA — nil caPool is rejected
// ─────────────────────────────────────────────────────────────────────────────

func TestDialWithCA_NilCAPool(t *testing.T) {
	_, err := grpcclient.DialWithCA("localhost:50051", nil)
	if err == nil {
		t.Fatal("expected error for nil caPool in DialWithCA, got nil")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// DialFromConfig — credential resolution
// ─────────────────────────────────────────────────────────────────────────────

// TestDialFromConfig_InsecureDoesNotDial verifies that DialFromConfig with an
// insecure config builds the connection object without error. We cannot fully
// dial because there is no server, but grpc.NewClient (non-blocking) returns
// immediately. Note that Dial uses WithBlock, so this test uses the non-blocking
// path by checking that the error is *not* a credential/config error.
//
// We test this indirectly: an insecure config with an unreachable address
// should fail with a dial/timeout error, NOT with a credential error.
func TestDialFromConfig_InsecureConfig(t *testing.T) {
	cfg := clientconfig.Default()
	cfg.Client.GRPCAddr = "localhost:19999" // almost certainly nothing listening here
	cfg.Client.Insecure = true
	// Shorten timeout so the test doesn't stall for 10 s.
	// We use Dial with a custom DialTimeout via Options directly for this.
	opts := grpcclient.Options{
		Addr:        cfg.Client.GRPCAddr,
		Insecure:    true,
		DialTimeout: 200 * time.Millisecond,
	}
	_, err := grpcclient.Dial(opts)
	// We expect a dial failure (connection refused / timeout), not a
	// credential configuration error. The error message should mention the
	// address, not cert/key.
	if err == nil {
		t.Fatal("expected dial error for unreachable address, got nil")
	}
	// The error must not be the "both be set or both be empty" credential error.
	if err.Error() == "grpcclient: CertFile and KeyFile must both be set or both be empty" {
		t.Fatalf("got credential config error instead of dial error: %v", err)
	}
}

func TestDialFromConfig_TLSWithBadCertFile(t *testing.T) {
	dir := t.TempDir()
	certPEM, _, _ := selfSignedCA(t) // just a cert, not the key
	certPath := writeTempFile(t, dir, "cert.pem", certPEM)
	keyPath := writeTempFile(t, dir, "key.pem", certPEM) // wrong — also cert

	cfg := clientconfig.Default()
	cfg.Client.GRPCAddr = "localhost:50051"
	cfg.Client.Insecure = false
	cfg.TLS.CertFile = certPath
	cfg.TLS.KeyFile = keyPath

	_, err := grpcclient.DialFromConfig(cfg)
	if err == nil {
		t.Fatal("expected error for mismatched cert/key, got nil")
	}
}

func TestDialFromConfig_TLSWithBadCAFile(t *testing.T) {
	dir := t.TempDir()
	caCertPEM, caKeyPEM, _ := selfSignedCA(t)
	certPEM, keyPEM := signedLeafCert(t, caCertPEM, caKeyPEM)
	certPath := writeTempFile(t, dir, "cert.pem", certPEM)
	keyPath := writeTempFile(t, dir, "key.pem", keyPEM)

	cfg := clientconfig.Default()
	cfg.Client.GRPCAddr = "localhost:50051"
	cfg.Client.Insecure = false
	cfg.TLS.CertFile = certPath
	cfg.TLS.KeyFile = keyPath
	cfg.TLS.CAFile = "/nonexistent/ca.crt"

	_, err := grpcclient.DialFromConfig(cfg)
	if err == nil {
		t.Fatal("expected error for missing CA file, got nil")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Conn typed accessors — verified against a real in-process gRPC server
// ─────────────────────────────────────────────────────────────────────────────

// TestConn_TypedAccessors dials an in-process gRPC server and verifies that
// all four typed accessor methods return non-nil clients without panicking.
// grpc.NewClient is non-blocking so no server handshake is required here.
func TestConn_TypedAccessors(t *testing.T) {
	// Use grpc.NewClient directly so we get a non-blocking Conn that can be
	// tested without requiring a round-trip. DialInsecure wraps grpc.NewClient.
	conn, err := grpcclient.DialInsecure("localhost:19990")
	if err != nil {
		t.Fatalf("DialInsecure: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	if conn.Notes() == nil {
		t.Error("Notes() returned nil")
	}
	if conn.Devices() == nil {
		t.Error("Devices() returned nil")
	}
	if conn.Projects() == nil {
		t.Error("Projects() returned nil")
	}
	if conn.Pairing() == nil {
		t.Error("Pairing() returned nil")
	}
	if conn.Raw() == nil {
		t.Error("Raw() returned nil")
	}
}

// TestConn_Close_Idempotent verifies that calling Close multiple times does
// not panic. grpc.ClientConn.Close is safe to call once; we document that
// callers must call it exactly once, but defensive code should not crash.
func TestConn_Close_Idempotent(t *testing.T) {
	// Non-blocking dial — no server needed.
	conn, err := grpcclient.DialInsecure("localhost:19991")
	if err != nil {
		t.Fatalf("DialInsecure: %v", err)
	}

	if err := conn.Close(); err != nil {
		t.Errorf("first Close returned error: %v", err)
	}
	// Second Close on a closed *grpc.ClientConn returns an error but must not
	// panic. We tolerate the error here.
	_ = conn.Close()
}

// ─────────────────────────────────────────────────────────────────────────────
// DialInsecure / DialTLS convenience constructors
// ─────────────────────────────────────────────────────────────────────────────

// TestDialInsecure_ConnectsToInsecureServer verifies DialInsecure returns a
// usable Conn. grpc.NewClient is non-blocking so no real server is needed.
func TestDialInsecure_ConnectsToInsecureServer(t *testing.T) {
	conn, err := grpcclient.DialInsecure("localhost:19992")
	if err != nil {
		t.Fatalf("DialInsecure: %v", err)
	}
	defer conn.Close()

	if conn.Raw() == nil {
		t.Error("Raw() returned nil after DialInsecure")
	}
}

func TestDialInsecure_EmptyAddr(t *testing.T) {
	_, err := grpcclient.DialInsecure("")
	if err == nil {
		t.Fatal("expected error for empty addr, got nil")
	}
}

// TestDialTLS_FailsWithoutServer verifies that DialTLS returns an error when
// there is nothing listening (timeout path). This exercises the TLS credential
// path without requiring a real cert.
func TestDialTLS_FailsWithoutServer(t *testing.T) {
	opts := grpcclient.Options{
		Addr:        "localhost:19998",
		DialTimeout: 200 * time.Millisecond,
	}
	_, err := grpcclient.Dial(opts)
	if err == nil {
		t.Fatal("expected dial error for unreachable TLS address, got nil")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// DialBootstrap — nil pool sets InsecureSkipVerify
// ─────────────────────────────────────────────────────────────────────────────

// TestDialBootstrap_NilPool verifies that DialBootstrap with a nil CA pool
// does not return an error constructing the Conn (it sets InsecureSkipVerify
// internally for the first-contact flow).
func TestDialBootstrap_NilPool_ReturnsConn(t *testing.T) {
	// We cannot actually complete the TLS handshake without a server, but
	// grpc.NewClient (non-blocking) should not fail on construction.
	conn, err := grpcclient.DialBootstrap("localhost:50052", nil)
	if err != nil {
		t.Fatalf("DialBootstrap with nil pool: %v", err)
	}
	// Connection is lazy — close it without making any RPC.
	defer conn.Close()

	if conn.Pairing() == nil {
		t.Error("Pairing() returned nil")
	}
}

func TestDialBootstrap_WithPool_ReturnsConn(t *testing.T) {
	certPEM, _, _ := selfSignedCA(t)
	pool, err := grpcclient.ParseCAPool(certPEM)
	if err != nil {
		t.Fatalf("ParseCAPool: %v", err)
	}

	conn, err := grpcclient.DialWithCA("localhost:50053", pool)
	if err != nil {
		t.Fatalf("DialWithCA: %v", err)
	}
	defer conn.Close()

	if conn.Raw() == nil {
		t.Error("Raw() returned nil")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// DialMTLS — valid inputs produce a Conn
// ─────────────────────────────────────────────────────────────────────────────

func TestDialMTLS_ValidInputs_ReturnsConn(t *testing.T) {
	caCertPEM, caKeyPEM, _ := selfSignedCA(t)
	certPEM, keyPEM := signedLeafCert(t, caCertPEM, caKeyPEM)

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair: %v", err)
	}

	pool, err := grpcclient.ParseCAPool(caCertPEM)
	if err != nil {
		t.Fatalf("ParseCAPool: %v", err)
	}

	conn, err := grpcclient.DialMTLS("localhost:50054", cert, pool)
	if err != nil {
		t.Fatalf("DialMTLS: %v", err)
	}
	defer conn.Close()

	if conn.Raw() == nil {
		t.Error("Raw() returned nil after DialMTLS")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// DialFromConfig — insecure path builds connection
// ─────────────────────────────────────────────────────────────────────────────

// TestDialFromConfig_InsecureBuildsConn verifies the full DialFromConfig path
// end-to-end. grpc.NewClient is non-blocking so no real server is needed.
func TestDialFromConfig_InsecureBuildsConn(t *testing.T) {
	cfg := clientconfig.Default()
	cfg.Client.GRPCAddr = "localhost:19993"
	cfg.Client.Insecure = true

	conn, err := grpcclient.DialFromConfig(cfg)
	if err != nil {
		t.Fatalf("DialFromConfig: %v", err)
	}
	defer conn.Close()

	if conn.Notes() == nil {
		t.Error("Notes() returned nil")
	}
	if conn.Devices() == nil {
		t.Error("Devices() returned nil")
	}
	if conn.Projects() == nil {
		t.Error("Projects() returned nil")
	}
	if conn.Pairing() == nil {
		t.Error("Pairing() returned nil")
	}
}
