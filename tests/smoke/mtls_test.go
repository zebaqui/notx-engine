package smoke

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zebaqui/notx-engine/repo/memory"
	"github.com/zebaqui/notx-engine/internal/server"
	"github.com/zebaqui/notx-engine/config"
)

// ─────────────────────────────────────────────────────────────────────────────
// In-process PKI helpers
// ─────────────────────────────────────────────────────────────────────────────

// certBundle holds a CA cert pool and the PEM-encoded artefacts written to
// disk so the server can load them via file paths.
type certBundle struct {
	// paths written to t.TempDir() — passed to config
	caCertFile     string
	serverCertFile string
	serverKeyFile  string

	// in-memory pools and certs — used to build test HTTP clients
	caPool     *x509.CertPool
	clientCert tls.Certificate // valid client cert signed by the same CA
	wrongCert  tls.Certificate // valid cert signed by a *different* CA — must be rejected
}

// newPKI generates a complete test PKI in dir:
//
//	CA  → signs server cert + valid client cert
//	rogue CA → signs the wrong-CA client cert
func newPKI(t *testing.T, dir string) *certBundle {
	t.Helper()

	// ── CA ────────────────────────────────────────────────────────────────────
	caKey, caCert, caCertPEM := mustGenCA(t, "Test CA")

	// ── Server cert (SANs: 127.0.0.1 + localhost) ─────────────────────────────
	serverCertPEM, serverKeyPEM := mustGenLeaf(t, "server", caCert, caKey,
		[]net.IP{net.ParseIP("127.0.0.1")},
		[]string{"localhost"},
	)

	// ── Valid client cert (signed by same CA) ─────────────────────────────────
	clientCertPEM, clientKeyPEM := mustGenLeaf(t, "client", caCert, caKey, nil, nil)

	// ── Rogue CA + client cert (must be rejected by mTLS server) ─────────────
	rogueKey, rogueCACert, _ := mustGenCA(t, "Rogue CA")
	wrongCertPEM, wrongKeyPEM := mustGenLeaf(t, "wrong-client", rogueCACert, rogueKey, nil, nil)

	// ── Write server-side files to disk ───────────────────────────────────────
	caCertFile := writeFile(t, dir, "ca.crt", caCertPEM)
	serverCertFile := writeFile(t, dir, "server.crt", serverCertPEM)
	serverKeyFile := writeFile(t, dir, "server.key", serverKeyPEM)

	// ── Build CA pool for clients ─────────────────────────────────────────────
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caCertPEM) {
		t.Fatal("newPKI: failed to add CA cert to pool")
	}

	// ── Parse client TLS certs ────────────────────────────────────────────────
	clientTLSCert, err := tls.X509KeyPair(clientCertPEM, clientKeyPEM)
	if err != nil {
		t.Fatalf("newPKI: parse client cert: %v", err)
	}
	wrongTLSCert, err := tls.X509KeyPair(wrongCertPEM, wrongKeyPEM)
	if err != nil {
		t.Fatalf("newPKI: parse wrong cert: %v", err)
	}

	return &certBundle{
		caCertFile:     caCertFile,
		serverCertFile: serverCertFile,
		serverKeyFile:  serverKeyFile,
		caPool:         caPool,
		clientCert:     clientTLSCert,
		wrongCert:      wrongTLSCert,
	}
}

// mustGenCA generates a self-signed CA key + certificate and returns the key,
// the parsed *x509.Certificate, and the PEM-encoded certificate bytes.
func mustGenCA(t *testing.T, cn string) (*ecdsa.PrivateKey, *x509.Certificate, []byte) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("mustGenCA(%q): generate key: %v", cn, err)
	}

	tmpl := &x509.Certificate{
		SerialNumber:          mustSerial(t),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("mustGenCA(%q): create cert: %v", cn, err)
	}

	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("mustGenCA(%q): parse cert: %v", cn, err)
	}

	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return key, parsed, pemBytes
}

// mustGenLeaf generates a leaf certificate (server or client) signed by the
// given CA and returns the PEM-encoded cert and key.
func mustGenLeaf(
	t *testing.T,
	cn string,
	caCert *x509.Certificate,
	caKey *ecdsa.PrivateKey,
	ips []net.IP,
	dnsNames []string,
) (certPEM, keyPEM []byte) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("mustGenLeaf(%q): generate key: %v", cn, err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: mustSerial(t),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		IPAddresses:  ips,
		DNSNames:     dnsNames,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatalf("mustGenLeaf(%q): create cert: %v", cn, err)
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("mustGenLeaf(%q): marshal key: %v", cn, err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return
}

func mustSerial(t *testing.T) *big.Int {
	t.Helper()
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("mustSerial: %v", err)
	}
	return serial
}

func writeFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("writeFile %q: %v", path, err)
	}
	return path
}

// ─────────────────────────────────────────────────────────────────────────────
// Server bootstrap helpers
// ─────────────────────────────────────────────────────────────────────────────

// startTLSServer spins up a notx server with TLS enabled (but no mTLS) and
// returns the HTTPS base URL plus a stop function.
func startTLSServer(t *testing.T, pki *certBundle) (baseURL string, stop func()) {
	t.Helper()
	return startSecureServer(t, pki, false)
}

// startMTLSServer spins up a notx server with mTLS enabled and returns the
// HTTPS base URL plus a stop function.
func startMTLSServer(t *testing.T, pki *certBundle) (baseURL string, stop func()) {
	t.Helper()
	return startSecureServer(t, pki, true)
}

func startSecureServer(t *testing.T, pki *certBundle, mtls bool) (baseURL string, stop func()) {
	t.Helper()

	httpPort := freePort(t)
	grpcPort := freePort(t)

	cfg := config.Default()
	cfg.EnableHTTP = true
	cfg.EnableGRPC = false // gRPC TLS is tested separately; keep this focused on HTTP
	cfg.HTTPPort = httpPort
	cfg.GRPCPort = grpcPort
	cfg.TLSCertFile = pki.serverCertFile
	cfg.TLSKeyFile = pki.serverKeyFile
	cfg.DeviceOnboarding.AutoApprove = true
	if mtls {
		cfg.TLSCAFile = pki.caCertFile
	}

	provider := memory.New()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))

	srv, err := server.New(cfg, provider, provider, provider, provider, provider, provider, log)
	if err != nil {
		t.Fatalf("startSecureServer: server.New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- srv.RunWithContext(ctx) }()

	// Wait for the TLS port to accept TCP connections (up to 3 s).
	addr := fmt.Sprintf("127.0.0.1:%d", httpPort)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	stop = func() {
		cancel()
		select {
		case <-runErr:
		case <-time.After(5 * time.Second):
			t.Log("warning: server did not stop within 5 s")
		}
	}

	return fmt.Sprintf("https://127.0.0.1:%d", httpPort), stop
}

// ─────────────────────────────────────────────────────────────────────────────
// HTTP client factories
// ─────────────────────────────────────────────────────────────────────────────

// tlsClient returns an *http.Client that trusts the test CA but presents no
// client certificate (suitable for TLS-only, rejected by mTLS).
func tlsClient(pki *certBundle) *http.Client {
	return clientWithCert(pki, nil)
}

// mtlsClient returns an *http.Client that trusts the test CA and presents the
// valid client certificate signed by that CA.
func mtlsClient(pki *certBundle) *http.Client {
	return clientWithCert(pki, &pki.clientCert)
}

// wrongCAClient returns an *http.Client that trusts the test CA but presents a
// client certificate signed by the rogue CA — must be rejected by the mTLS server.
func wrongCAClient(pki *certBundle) *http.Client {
	return clientWithCert(pki, &pki.wrongCert)
}

func clientWithCert(pki *certBundle, cert *tls.Certificate) *http.Client {
	tlsCfg := &tls.Config{
		RootCAs:    pki.caPool,
		MinVersion: tls.VersionTLS13,
	}
	if cert != nil {
		tlsCfg.Certificates = []tls.Certificate{*cert}
	}
	return &http.Client{
		Transport: &http.Transport{TLSClientConfig: tlsCfg},
		Timeout:   5 * time.Second,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Assertion helpers
// ─────────────────────────────────────────────────────────────────────────────

// mustGET performs a GET and returns the response; it fatal-fails on network error.
func mustGET(t *testing.T, client *http.Client, url string) *http.Response {
	t.Helper()
	resp, err := client.Get(url) //nolint:noctx
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

// mustPOSTJSON POSTs JSON using the given client and returns the response.
func mustPOSTJSON(t *testing.T, client *http.Client, url string, body any) *http.Response {
	t.Helper()
	return mustPOSTJSONWithDeviceID(t, client, url, "", body)
}

// mustPOSTJSONWithDeviceID POSTs JSON with an optional X-Device-ID header.
func mustPOSTJSONWithDeviceID(t *testing.T, client *http.Client, url, deviceID string, body any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("mustPOSTJSONWithDeviceID marshal: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(b)) //nolint:noctx
	if err != nil {
		t.Fatalf("mustPOSTJSONWithDeviceID new request %s: %v", url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if deviceID != "" {
		req.Header.Set("X-Device-ID", deviceID)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("mustPOSTJSONWithDeviceID %s: %v", url, err)
	}
	return resp
}

// mustGETWithDeviceID performs a GET with an X-Device-ID header and returns
// the response; it fatal-fails on network error.
func mustGETWithDeviceID(t *testing.T, client *http.Client, url, deviceID string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil) //nolint:noctx
	if err != nil {
		t.Fatalf("mustGETWithDeviceID new request %s: %v", url, err)
	}
	if deviceID != "" {
		req.Header.Set("X-Device-ID", deviceID)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("mustGETWithDeviceID %s: %v", url, err)
	}
	return resp
}

// registerSecureTestDevice registers a device against a TLS/mTLS server using
// the provided client (which must already have the correct TLS config).
// The server must have AutoApprove=true so the device is immediately usable.
func registerSecureTestDevice(t *testing.T, client *http.Client, baseURL string) string {
	t.Helper()
	const (
		devURN   = "notx:device:bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
		ownerURN = "notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b"
	)
	resp := mustPOSTJSONWithDeviceID(t, client, baseURL+"/v1/devices", "", registerDeviceRequest{
		URN:      devURN,
		Name:     "mtls-test-device",
		OwnerURN: ownerURN,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("registerSecureTestDevice: expected 201, got %d — %s", resp.StatusCode, body)
	}
	var out registerDeviceResponse
	raw, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("registerSecureTestDevice: decode response: %v", err)
	}
	if out.ApprovalStatus != "approved" {
		t.Fatalf("registerSecureTestDevice: expected approval_status=approved, got %q", out.ApprovalStatus)
	}
	return out.URN
}

// expectStatus asserts the response has the given HTTP status code.
func expectStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	defer resp.Body.Close()
	if resp.StatusCode != want {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("expected status %d, got %d — body: %s", want, resp.StatusCode, body)
	}
}

// expectTLSHandshakeFailure asserts that the error returned by an HTTP client
// operation is a TLS handshake failure (connection refused at the TLS layer),
// not a successful connection.
func expectTLSHandshakeFailure(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Error("expected TLS handshake failure but request succeeded")
		return
	}
	// The error may be wrapped; the string representation always contains a
	// recognisable TLS-layer marker regardless of Go version.
	errStr := err.Error()
	for _, marker := range []string{
		"tls:", "handshake failure", "certificate required",
		"bad certificate", "remote error", "EOF",
		"connection reset",
	} {
		if contains(errStr, marker) {
			return // confirmed TLS-layer rejection
		}
	}
	t.Errorf("expected TLS-layer error but got: %v", err)
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 &&
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}

// ─────────────────────────────────────────────────────────────────────────────
// Tests
// ─────────────────────────────────────────────────────────────────────────────

// TestMTLS exercises four scenarios against the HTTP layer:
//
//  1. TLS-only server — a client with no cert can reach /healthz.
//  2. mTLS happy path — a client presenting the correct cert can create a note
//     and list it back.
//  3. mTLS no-cert rejection — a client with no cert is refused at the TLS
//     handshake when the server requires client auth.
//  4. mTLS wrong-CA rejection — a client presenting a cert from a rogue CA is
//     refused at the TLS handshake.
func TestMTLS(t *testing.T) {
	dir := t.TempDir()
	pki := newPKI(t, dir)

	// ── Sub-test 1: TLS-only (no client cert required) ────────────────────────
	t.Run("tls_only_no_client_cert", func(t *testing.T) {
		baseURL, stop := startTLSServer(t, pki)
		defer stop()

		client := tlsClient(pki) // no client cert
		resp := mustGET(t, client, baseURL+"/healthz")
		expectStatus(t, resp, http.StatusOK)
	})

	// ── Sub-test 2: mTLS happy path ───────────────────────────────────────────
	t.Run("mtls_valid_client_cert", func(t *testing.T) {
		baseURL, stop := startMTLSServer(t, pki)
		defer stop()

		client := mtlsClient(pki)

		// health probe (no device auth required)
		resp := mustGET(t, client, baseURL+"/healthz")
		expectStatus(t, resp, http.StatusOK)

		// register a device (open endpoint — no X-Device-ID required)
		deviceID := registerSecureTestDevice(t, client, baseURL)

		// create a note (requires X-Device-ID)
		createResp := mustPOSTJSONWithDeviceID(t, client, baseURL+"/v1/notes", deviceID, createNoteRequest{
			URN:      "notx:note:11111111-1111-4111-8111-111111111111",
			Name:     "mTLS smoke note",
			NoteType: "normal",
		})
		expectStatus(t, createResp, http.StatusCreated)

		// list it back (requires X-Device-ID)
		listResp := mustGETWithDeviceID(t, client, baseURL+"/v1/notes", deviceID)
		if listResp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(listResp.Body)
			listResp.Body.Close()
			t.Fatalf("list notes: expected 200, got %d — %s", listResp.StatusCode, body)
		}
		var listed listNotesResponse
		decodeBody(t, listResp, &listed)
		if len(listed.Notes) != 1 {
			t.Errorf("expected 1 note, got %d", len(listed.Notes))
		}
		if len(listed.Notes) > 0 && listed.Notes[0].Name != "mTLS smoke note" {
			t.Errorf("note name = %q, want %q", listed.Notes[0].Name, "mTLS smoke note")
		}
	})

	// ── Sub-test 3: mTLS — no client cert presented ───────────────────────────
	t.Run("mtls_no_client_cert_rejected", func(t *testing.T) {
		baseURL, stop := startMTLSServer(t, pki)
		defer stop()

		client := tlsClient(pki)                   // valid CA trust, but no client cert
		_, err := client.Get(baseURL + "/healthz") //nolint:noctx
		expectTLSHandshakeFailure(t, err)
	})

	// ── Sub-test 4: mTLS — client cert from wrong CA ──────────────────────────
	t.Run("mtls_wrong_ca_cert_rejected", func(t *testing.T) {
		baseURL, stop := startMTLSServer(t, pki)
		defer stop()

		client := wrongCAClient(pki)               // cert signed by rogue CA
		_, err := client.Get(baseURL + "/healthz") //nolint:noctx
		expectTLSHandshakeFailure(t, err)
	})
}

// TestMTLS_ReadyzAndHealthzBothReachable verifies that both health probes are
// accessible over mTLS with a valid client certificate.
func TestMTLS_ReadyzAndHealthzBothReachable(t *testing.T) {
	dir := t.TempDir()
	pki := newPKI(t, dir)
	baseURL, stop := startMTLSServer(t, pki)
	defer stop()

	client := mtlsClient(pki)

	for _, path := range []string{"/healthz", "/readyz"} {
		t.Run(path, func(t *testing.T) {
			resp := mustGET(t, client, baseURL+path)
			expectStatus(t, resp, http.StatusOK)
		})
	}
}

// TestMTLS_NoCertAgainstTLSOnlyServerSucceeds is the complement of the
// rejection test: a plain TLS server (no mTLS) must NOT reject a client that
// presents no certificate.
func TestMTLS_NoCertAgainstTLSOnlyServerSucceeds(t *testing.T) {
	dir := t.TempDir()
	pki := newPKI(t, dir)
	baseURL, stop := startTLSServer(t, pki)
	defer stop()

	// Even the wrong-CA client can reach a TLS-only server (it has the right
	// CA pool to verify the server cert, but the server doesn't care about the
	// client cert).
	for name, client := range map[string]*http.Client{
		"no_cert":    tlsClient(pki),
		"valid_cert": mtlsClient(pki),
		"wrong_ca":   wrongCAClient(pki),
	} {
		t.Run(name, func(t *testing.T) {
			resp := mustGET(t, client, baseURL+"/healthz")
			expectStatus(t, resp, http.StatusOK)
		})
	}
}

// TestMTLS_UntrustedServerCertRejected verifies that a client that does NOT
// have the test CA in its pool rejects the server's certificate — i.e., the
// server is presenting the right cert and the standard TLS chain validation
// works in both directions.
func TestMTLS_UntrustedServerCertRejected(t *testing.T) {
	dir := t.TempDir()
	pki := newPKI(t, dir)
	baseURL, stop := startTLSServer(t, pki)
	defer stop()

	// Client with the *system* pool (doesn't contain our test CA).
	untrustedClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13},
		},
		Timeout: 5 * time.Second,
	}

	_, err := untrustedClient.Get(baseURL + "/healthz") //nolint:noctx
	if err == nil {
		t.Error("expected server certificate verification failure but request succeeded")
	}
}
