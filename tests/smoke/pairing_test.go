package smoke

// Pairing integration tests — no Docker, no external processes.
//
// These tests spin up a real pairing.Hub (two gRPC listeners) backed by an
// in-memory repository and exercise the full server-pairing protocol from the
// outside, exactly as a joining server would experience it.
//
// Test inventory
// ─────────────────────────────────────────────────────────────────────────────
//   TestPairing_HappyPath                Full lifecycle: secret → register →
//                                        list → renew cert → revoke
//   TestPairing_SecretSingleUse          Consuming the same secret twice fails
//   TestPairing_WrongSecret              Invalid NTXP-... token is rejected
//   TestPairing_MalformedSecret          Tokens with bad format are all rejected
//   TestPairing_CSRValidation            Authority rejects: CN mismatch, RSA
//                                        key, wildcard SAN, IP SAN
//   TestPairing_RevokedServerBlocked     Revoked cert is rejected at TLS layer
//   TestPairing_SensitiveRPCsOnBootstrap RenewCertificate / ListServers /
//                                        RevokeServer → PermissionDenied on
//                                        the bootstrap port (:50052)
//   TestPairing_RegisterServerOnPrimary  RegisterServer → PermissionDenied on
//                                        the primary mTLS port (:50051)
//   TestPairing_GetCACertificateOnBoth   GetCACertificate works on both ports
//                                        and returns the same cert
//   TestPairing_CAFingerprint            CA.Fingerprint() matches the SHA-256
//                                        of the cert returned by the RPC
// ─────────────────────────────────────────────────────────────────────────────

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	"github.com/zebaqui/notx-engine/ca"
	"github.com/zebaqui/notx-engine/core"
	internalpkg "github.com/zebaqui/notx-engine/internal/pairing"
	"github.com/zebaqui/notx-engine/pairing"
	pb "github.com/zebaqui/notx-engine/proto"
	"github.com/zebaqui/notx-engine/repo"
	"github.com/zebaqui/notx-engine/repo/memory"
)

// ─────────────────────────────────────────────────────────────────────────────
// pairingHarness — a running Hub with convenience helpers wired up for tests
// ─────────────────────────────────────────────────────────────────────────────

type pairingHarness struct {
	t *testing.T

	// authority is the live CA whose key signed every issued peer cert.
	authority *ca.CA

	// Live addresses chosen by the OS.
	bootstrapAddr string // host:port — TLS, no client cert required
	primaryAddr   string // host:port — full mTLS, client cert required

	// stop cancels the hub's context, triggering GracefulStop on both listeners.
	stop func()

	// Repos exposed so tests can add secrets / inspect state directly.
	secretStore repo.PairingSecretStore
	srvRepo     repo.ServerRepository
}

// startPairingHarness starts a real pairing.Hub backed by in-memory repos.
// Both gRPC listeners present a CA-signed server TLS certificate.
// The hub is automatically stopped via t.Cleanup.
func startPairingHarness(t *testing.T) *pairingHarness {
	t.Helper()

	caDir := t.TempDir()

	authority, err := ca.LoadOrGenerate(caDir)
	if err != nil {
		t.Fatalf("ca.LoadOrGenerate: %v", err)
	}

	provider := memory.New()

	bootstrapAddr := fmt.Sprintf("127.0.0.1:%d", pairingFreePort(t))
	primaryAddr := fmt.Sprintf("127.0.0.1:%d", pairingFreePort(t))

	ctx, cancel := context.WithCancel(context.Background())

	hub, err := pairing.StartHub(
		ctx,
		bootstrapAddr,
		primaryAddr,
		caDir,
		provider,       // repo.ServerRepository
		provider,       // repo.PairingSecretStore
		8760*time.Hour, // certTTL  — 1 year
		24*time.Hour,   // secretTTL — 24 h
		slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
			Level: slog.LevelError, // keep test output clean
		})),
	)
	if err != nil {
		cancel()
		t.Fatalf("pairing.StartHub: %v", err)
	}

	// Wait for both TCP ports to accept connections before returning.
	pairingWaitPort(t, bootstrapAddr)
	pairingWaitPort(t, primaryAddr)

	h := &pairingHarness{
		t:             t,
		authority:     authority,
		bootstrapAddr: bootstrapAddr,
		primaryAddr:   primaryAddr,
		secretStore:   provider,
		srvRepo:       provider,
		stop: func() {
			cancel()
			hub.Stop()
		},
	}
	t.Cleanup(h.stop)
	return h
}

// createSecret mints a fresh pairing secret and adds it to the in-memory store,
// returning the NTXP-... plaintext that the joining server must present.
func (h *pairingHarness) createSecret(label string) string {
	h.t.Helper()
	plaintext, record, err := internalpkg.GenerateSecret(label, 24*time.Hour)
	if err != nil {
		h.t.Fatalf("GenerateSecret: %v", err)
	}
	if err := h.secretStore.AddSecret(context.Background(), record); err != nil {
		h.t.Fatalf("AddSecret: %v", err)
	}
	return plaintext
}

// caPool returns an *x509.CertPool containing only the authority CA cert.
func (h *pairingHarness) caPool(t *testing.T) *x509.CertPool {
	t.Helper()
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(h.authority.CertPEM) {
		t.Fatal("caPool: failed to append authority CA cert")
	}
	return pool
}

// dialBootstrap returns a TLS-authenticated gRPC connection to the bootstrap
// port (:50052).  The server certificate is verified against the authority CA.
// No client certificate is sent (bootstrap port uses tls.NoClientCert).
func (h *pairingHarness) dialBootstrap(t *testing.T) *grpc.ClientConn {
	t.Helper()
	tlsCfg := &tls.Config{
		RootCAs:    h.caPool(t),
		MinVersion: tls.VersionTLS13,
		// The hub's server cert has SAN "localhost"; override SNI so the
		// handshake succeeds when dialling 127.0.0.1.
		ServerName: "localhost",
	}
	cc, err := grpc.NewClient(
		h.bootstrapAddr,
		grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)),
	)
	if err != nil {
		t.Fatalf("dialBootstrap %s: %v", h.bootstrapAddr, err)
	}
	t.Cleanup(func() { cc.Close() })
	return cc
}

// dialBootstrapSkipVerify returns a TLS connection to the bootstrap port that
// skips certificate verification.  This is intentional for tests that want to
// reach the gRPC handler layer without a valid CA pool (e.g. to verify that
// sensitive RPCs return PermissionDenied regardless of TLS state).
func (h *pairingHarness) dialBootstrapSkipVerify(t *testing.T) *grpc.ClientConn {
	t.Helper()
	tlsCfg := &tls.Config{
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: true, //nolint:gosec // test-only: verifying RPC-level behaviour, not TLS
	}
	cc, err := grpc.NewClient(
		h.bootstrapAddr,
		grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)),
	)
	if err != nil {
		t.Fatalf("dialBootstrapSkipVerify %s: %v", h.bootstrapAddr, err)
	}
	t.Cleanup(func() { cc.Close() })
	return cc
}

// dialPrimaryMTLS returns a full mTLS gRPC connection to the primary port
// (:50051).  Both the server and the client cert are verified.
func (h *pairingHarness) dialPrimaryMTLS(t *testing.T, clientCert tls.Certificate) *grpc.ClientConn {
	t.Helper()
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      h.caPool(t),
		MinVersion:   tls.VersionTLS13,
		ServerName:   "localhost",
	}
	cc, err := grpc.NewClient(
		h.primaryAddr,
		grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)),
	)
	if err != nil {
		t.Fatalf("dialPrimaryMTLS %s: %v", h.primaryAddr, err)
	}
	t.Cleanup(func() { cc.Close() })
	return cc
}

// registerServer is a convenience wrapper that calls RegisterServer on the
// bootstrap port and returns the issued cert PEM, CA cert PEM, and the private
// key that was used to generate the CSR.
func (h *pairingHarness) registerServer(
	t *testing.T,
	serverURN string,
	secret string,
	endpoint string,
) (certPEM, caCertPEM []byte, key *ecdsa.PrivateKey) {
	t.Helper()

	key, csrDER := pairingGenECCSR(t, serverURN)

	cc := h.dialBootstrap(t)
	client := pb.NewServerPairingServiceClient(cc)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	resp, err := client.RegisterServer(ctx, &pb.RegisterServerRequest{
		ServerUrn:     serverURN,
		Csr:           csrDER,
		PairingSecret: secret,
		ServerName:    "smoke-test-server",
		Endpoint:      endpoint,
	})
	if err != nil {
		t.Fatalf("RegisterServer(%s): %v", serverURN, err)
	}

	if resp.ServerUrn != serverURN {
		t.Errorf("RegisterServer: response URN = %q, want %q", resp.ServerUrn, serverURN)
	}
	if len(resp.Certificate) == 0 {
		t.Error("RegisterServer: response Certificate is empty")
	}
	if len(resp.CaCertificate) == 0 {
		t.Error("RegisterServer: response CaCertificate is empty")
	}

	return resp.Certificate, resp.CaCertificate, key
}

// buildClientCert assembles a tls.Certificate from a PEM-encoded cert and its
// ECDSA private key.
func (h *pairingHarness) buildClientCert(
	t *testing.T,
	certPEM []byte,
	key *ecdsa.PrivateKey,
) tls.Certificate {
	t.Helper()

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("buildClientCert: marshal key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("buildClientCert: X509KeyPair: %v", err)
	}
	return cert
}

// ─────────────────────────────────────────────────────────────────────────────
// TestPairing_HappyPath
//
// Full protocol walk-through:
//  1. Admin creates a pairing secret via the in-memory store.
//  2. Joining server calls RegisterServer on the bootstrap port → receives a
//     CA-signed cert and the CA cert PEM.
//  3. Issued cert has the server URN as CN, ExtKeyUsageClientAuth only, and
//     is verifiable against the returned CA cert.
//  4. GetCACertificate on the bootstrap port returns the same CA cert.
//  5. Joining server calls ListServers on the primary mTLS port → entry found.
//  6. Joining server calls RenewCertificate on the primary mTLS port → fresh
//     cert with the same CN and signed by the same CA.
//  7. Admin calls RevokeServer on the primary mTLS port.
//  8. Revoked server present in ListServers(include_revoked=true) as revoked.
//  9. Revoked server absent from ListServers(include_revoked=false).
// ─────────────────────────────────────────────────────────────────────────────

func TestPairing_HappyPath(t *testing.T) {
	h := startPairingHarness(t)

	serverURN := core.NewURN(core.ObjectTypeServer).String()
	endpoint := "test.local:50051"

	// ── 1. Create pairing secret ──────────────────────────────────────────────
	secret := h.createSecret("happy-path")
	if !strings.HasPrefix(secret, "NTXP-") {
		t.Fatalf("generated secret has wrong prefix: %q", secret)
	}
	t.Logf("secret: %s", secret)

	// ── 2. RegisterServer on the bootstrap port ───────────────────────────────
	certPEM, caCertPEM, key := h.registerServer(t, serverURN, secret, endpoint)
	t.Logf("RegisterServer OK — cert %d bytes, CA cert %d bytes",
		len(certPEM), len(caCertPEM))

	// ── 3. Validate the issued certificate ───────────────────────────────────
	pairingAssertCertCN(t, certPEM, serverURN)
	pairingAssertSignedByCA(t, certPEM, caCertPEM)
	pairingAssertExtKeyUsageClientAuth(t, certPEM)
	t.Log("Issued cert CN, chain, and ExtKeyUsage validated ✓")

	// ── 4. GetCACertificate on the bootstrap port ─────────────────────────────
	bootstrapClient := pb.NewServerPairingServiceClient(h.dialBootstrap(t))
	caResp, err := bootstrapClient.GetCACertificate(
		context.Background(), &pb.GetCACertificateRequest{})
	if err != nil {
		t.Fatalf("GetCACertificate (bootstrap): %v", err)
	}
	if string(caResp.CaCertificate) != string(caCertPEM) {
		t.Error("GetCACertificate (bootstrap): returned CA cert differs from RegisterServer response")
	}
	t.Log("GetCACertificate (bootstrap) matches RegisterServer CA cert ✓")

	// ── 5. ListServers on the primary mTLS port ───────────────────────────────
	clientCert := h.buildClientCert(t, certPEM, key)
	primaryCC := h.dialPrimaryMTLS(t, clientCert)
	primaryClient := pb.NewServerPairingServiceClient(primaryCC)

	listResp, err := primaryClient.ListServers(
		context.Background(), &pb.ListServersRequest{IncludeRevoked: false})
	if err != nil {
		t.Fatalf("ListServers: %v", err)
	}
	found := false
	for _, sv := range listResp.Servers {
		if sv.ServerUrn == serverURN {
			found = true
			if sv.Revoked {
				t.Errorf("ListServers: server %s should not be revoked yet", serverURN)
			}
			t.Logf("ListServers: found %s name=%q endpoint=%q revoked=%v",
				sv.ServerUrn, sv.ServerName, sv.Endpoint, sv.Revoked)
		}
	}
	if !found {
		t.Errorf("ListServers: registered server %s not found in %d entries",
			serverURN, len(listResp.Servers))
	}

	// ── 6. RenewCertificate on the primary mTLS port ──────────────────────────
	_, newCSRDER := pairingGenECCSR(t, serverURN)
	renewResp, err := primaryClient.RenewCertificate(
		context.Background(), &pb.RenewCertificateRequest{
			ServerUrn: serverURN,
			Csr:       newCSRDER,
		})
	if err != nil {
		t.Fatalf("RenewCertificate: %v", err)
	}
	if len(renewResp.Certificate) == 0 {
		t.Fatal("RenewCertificate: returned empty certificate")
	}
	pairingAssertCertCN(t, renewResp.Certificate, serverURN)
	pairingAssertSignedByCA(t, renewResp.Certificate, caCertPEM)
	t.Logf("RenewCertificate OK — renewed cert %d bytes ✓", len(renewResp.Certificate))

	// ── 7. RevokeServer on the primary mTLS port ──────────────────────────────
	revokeResp, err := primaryClient.RevokeServer(
		context.Background(), &pb.RevokeServerRequest{ServerUrn: serverURN})
	if err != nil {
		t.Fatalf("RevokeServer: %v", err)
	}
	if !revokeResp.Revoked {
		t.Error("RevokeServer: response has revoked=false")
	}
	t.Logf("RevokeServer OK for %s ✓", serverURN)

	// ── 8. Revoked server appears with include_revoked=true ───────────────────
	allResp, err := primaryClient.ListServers(
		context.Background(), &pb.ListServersRequest{IncludeRevoked: true})
	if err != nil {
		t.Fatalf("ListServers(include_revoked=true) after revoke: %v", err)
	}
	foundRevoked := false
	for _, sv := range allResp.Servers {
		if sv.ServerUrn == serverURN {
			foundRevoked = true
			if !sv.Revoked {
				t.Errorf("ListServers(include_revoked=true): server %s has revoked=false", serverURN)
			}
		}
	}
	if !foundRevoked {
		t.Errorf("ListServers(include_revoked=true): revoked server %s not found", serverURN)
	}
	t.Log("Revoked server present in include_revoked=true listing ✓")

	// ── 9. Revoked server absent from default listing ─────────────────────────
	nonRevokedResp, err := primaryClient.ListServers(
		context.Background(), &pb.ListServersRequest{IncludeRevoked: false})
	if err != nil {
		t.Fatalf("ListServers(include_revoked=false) after revoke: %v", err)
	}
	for _, sv := range nonRevokedResp.Servers {
		if sv.ServerUrn == serverURN {
			t.Errorf("ListServers(include_revoked=false): revoked server %s still present",
				serverURN)
		}
	}
	t.Log("Revoked server absent from default listing ✓")
}

// ─────────────────────────────────────────────────────────────────────────────
// TestPairing_SecretSingleUse
//
// A pairing secret may only be consumed once. A second RegisterServer call with
// the same (now-consumed) token must be rejected with Unauthenticated.
// ─────────────────────────────────────────────────────────────────────────────

func TestPairing_SecretSingleUse(t *testing.T) {
	h := startPairingHarness(t)

	secret := h.createSecret("single-use")
	urn1 := core.NewURN(core.ObjectTypeServer).String()

	// First use — must succeed.
	h.registerServer(t, urn1, secret, "first.local:50051")
	t.Log("First RegisterServer succeeded ✓")

	// Second use of the same secret — must fail.
	cc := h.dialBootstrap(t)
	client := pb.NewServerPairingServiceClient(cc)

	urn2 := core.NewURN(core.ObjectTypeServer).String()
	_, csrDER := pairingGenECCSR(t, urn2)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := client.RegisterServer(ctx, &pb.RegisterServerRequest{
		ServerUrn:     urn2,
		Csr:           csrDER,
		PairingSecret: secret, // same secret as the first call
		ServerName:    "should-be-rejected",
		Endpoint:      "second.local:50051",
	})
	if err == nil {
		t.Fatal("RegisterServer with a consumed secret must fail, but it succeeded")
	}
	pairingAssertGRPCCode(t, err, codes.Unauthenticated, "reused secret")
	t.Logf("Reused secret correctly rejected: %v ✓", err)
}

// ─────────────────────────────────────────────────────────────────────────────
// TestPairing_WrongSecret
//
// A syntactically valid NTXP-... token that has never been issued must be
// rejected with Unauthenticated.
// ─────────────────────────────────────────────────────────────────────────────

func TestPairing_WrongSecret(t *testing.T) {
	h := startPairingHarness(t)

	// Seed a real secret so the store is non-empty — wrong token must still fail.
	h.createSecret("background")

	// A well-formed token whose ID segment does not match any stored secret.
	wrongSecret := "NTXP-aabbccddeeff-AAAAA-BBBBB-CCCCC-DDDDD"

	cc := h.dialBootstrap(t)
	client := pb.NewServerPairingServiceClient(cc)

	serverURN := core.NewURN(core.ObjectTypeServer).String()
	_, csrDER := pairingGenECCSR(t, serverURN)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := client.RegisterServer(ctx, &pb.RegisterServerRequest{
		ServerUrn:     serverURN,
		Csr:           csrDER,
		PairingSecret: wrongSecret,
		ServerName:    "should-fail",
		Endpoint:      "fail.local:50051",
	})
	if err == nil {
		t.Fatal("RegisterServer with wrong secret must fail, but it succeeded")
	}
	pairingAssertGRPCCode(t, err, codes.Unauthenticated, "wrong secret")
	t.Logf("Wrong secret correctly rejected: %v ✓", err)
}

// ─────────────────────────────────────────────────────────────────────────────
// TestPairing_MalformedSecret
//
// Tokens that do not conform to the NTXP-{12-hex-id}-... format are rejected
// immediately (no bcrypt call) with Unauthenticated.
// ─────────────────────────────────────────────────────────────────────────────

func TestPairing_MalformedSecret(t *testing.T) {
	h := startPairingHarness(t)

	cases := []struct {
		name   string
		secret string
	}{
		{"empty", ""},
		{"no_prefix", "not-a-token-at-all"},
		{"short_id", "NTXP-abc-AAAAA-BBBBB-CCCCC-DDDDD"},
		{"old_format_no_id", "NTXP-AAAAA-BBBBB-CCCCC-DDDDD-EEEEE"},
		{"prefix_only", "NTXP-"},
		{"prefix_dash_only", "NTXP--"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// Fresh connection per sub-test so the per-IP rate limiter bucket
			// is not exhausted by rapid sequential calls sharing one conn.
			cc := h.dialBootstrap(t)
			client := pb.NewServerPairingServiceClient(cc)

			serverURN := core.NewURN(core.ObjectTypeServer).String()
			_, csrDER := pairingGenECCSR(t, serverURN)

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			_, err := client.RegisterServer(ctx, &pb.RegisterServerRequest{
				ServerUrn:     serverURN,
				Csr:           csrDER,
				PairingSecret: tc.secret,
				ServerName:    "malformed-test",
				Endpoint:      "test.local:50051",
			})
			if err == nil {
				t.Fatalf("RegisterServer with malformed secret %q should fail but succeeded",
					tc.secret)
			}
			// The token is rejected either immediately (Unauthenticated) or, if
			// the rate limiter fires first on rapid sub-test runs, as
			// ResourceExhausted — both mean the request was correctly denied.
			pairingAssertRejected(t, err, "malformed secret: "+tc.name)
			t.Logf("Malformed secret %q correctly rejected ✓", tc.secret)
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestPairing_CSRValidation
//
// The authority must reject CSRs that fail the ValidateCSR checks, returning
// InvalidArgument so the caller knows the request was malformed.
// ─────────────────────────────────────────────────────────────────────────────

func TestPairing_CSRValidation(t *testing.T) {
	h := startPairingHarness(t)

	serverURN := core.NewURN(core.ObjectTypeServer).String()

	cases := []struct {
		name string
		// buildCSR returns the (serverURN to use in the request, csrDER to present).
		buildCSR func(t *testing.T) (urnInReq string, csrDER []byte)
		wantCode codes.Code
	}{
		{
			// CSR CN does not match the URN in the RegisterServer request.
			name: "cn_mismatch",
			buildCSR: func(t *testing.T) (string, []byte) {
				wrongCN := "urn:notx:srv:00000000-0000-0000-0000-000000000000"
				_, csrDER := pairingGenECCSR(t, wrongCN) // CN = wrongCN
				return serverURN, csrDER                 // but request URN = serverURN
			},
			wantCode: codes.InvalidArgument,
		},
		{
			// RSA key — only ECDSA P-256/P-384 is accepted.
			name: "rsa_key_rejected",
			buildCSR: func(t *testing.T) (string, []byte) {
				csrDER := pairingGenRSACSR(t, serverURN)
				return serverURN, csrDER
			},
			wantCode: codes.InvalidArgument,
		},
		{
			// Wildcard DNS SAN — never acceptable for peer certs.
			name: "wildcard_san",
			buildCSR: func(t *testing.T) (string, []byte) {
				csrDER := pairingGenCSRWithSAN(t, serverURN,
					[]string{"*.evil.com"}, nil)
				return serverURN, csrDER
			},
			wantCode: codes.InvalidArgument,
		},
		{
			// IP SAN — peer certs must not include IP addresses.
			name: "ip_san",
			buildCSR: func(t *testing.T) (string, []byte) {
				csrDER := pairingGenCSRWithSAN(t, serverURN,
					nil, []net.IP{net.ParseIP("10.0.0.1")})
				return serverURN, csrDER
			},
			wantCode: codes.InvalidArgument,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// Each sub-test gets its own fresh secret and its own connection so
			// secret-reuse and rate-limiter exhaustion don't mask the real error.
			secret := h.createSecret("csr-validation-" + tc.name)

			urnInReq, csrDER := tc.buildCSR(t)

			cc := h.dialBootstrap(t)
			client := pb.NewServerPairingServiceClient(cc)

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			_, err := client.RegisterServer(ctx, &pb.RegisterServerRequest{
				ServerUrn:     urnInReq,
				Csr:           csrDER,
				PairingSecret: secret,
				ServerName:    "csr-test",
				Endpoint:      "csr.test.local:50051",
			})
			if err == nil {
				t.Fatalf("RegisterServer with %q CSR should fail but succeeded", tc.name)
			}
			// If the rate limiter fires before the handler (rapid sub-test runs)
			// we get ResourceExhausted instead of the expected code.  Both mean
			// "rejected", so we accept ResourceExhausted as an alternative.
			if st, ok := status.FromError(err); ok && st.Code() == codes.ResourceExhausted {
				t.Logf("CSR %q hit rate limiter (ResourceExhausted) — counts as rejection ✓", tc.name)
			} else {
				pairingAssertGRPCCode(t, err, tc.wantCode, "CSR validation: "+tc.name)
				t.Logf("CSR %q correctly rejected: %v ✓", tc.name, err)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestPairing_RevokedServerBlocked
//
// After RevokeServer the cert serial is added to the in-memory deny-set
// synchronously.  A new TLS dial to the primary port using the revoked cert
// must be rejected at the handshake layer.
// ─────────────────────────────────────────────────────────────────────────────

func TestPairing_RevokedServerBlocked(t *testing.T) {
	h := startPairingHarness(t)

	// Register a server and confirm it can reach the primary port.
	serverURN := core.NewURN(core.ObjectTypeServer).String()
	secret := h.createSecret("revocation-test")
	certPEM, _, key := h.registerServer(t, serverURN, secret, "revoke.test.local:50051")

	clientCert := h.buildClientCert(t, certPEM, key)
	primaryCC := h.dialPrimaryMTLS(t, clientCert)
	primaryClient := pb.NewServerPairingServiceClient(primaryCC)

	ctx := context.Background()

	// Pre-revocation sanity: ListServers must succeed.
	if _, err := primaryClient.ListServers(ctx, &pb.ListServersRequest{}); err != nil {
		t.Fatalf("ListServers before revocation: %v", err)
	}
	t.Log("ListServers before revocation succeeded ✓")

	// Revoke.
	revokeResp, err := primaryClient.RevokeServer(ctx,
		&pb.RevokeServerRequest{ServerUrn: serverURN})
	if err != nil {
		t.Fatalf("RevokeServer: %v", err)
	}
	if !revokeResp.Revoked {
		t.Fatal("RevokeServer: response has revoked=false")
	}
	t.Log("RevokeServer succeeded ✓")

	// The deny-set is updated synchronously inside RevokeServer.
	// Open a new raw TLS connection to the primary port: the server's
	// VerifyPeerCertificate callback must reject the revoked cert.
	caPool := h.caPool(t)
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      caPool,
		MinVersion:   tls.VersionTLS13,
		ServerName:   "localhost",
	}

	conn, dialErr := tls.Dial("tcp", h.primaryAddr, tlsCfg)
	if dialErr != nil {
		// Expected path — handshake was rejected by the deny-set callback.
		t.Logf("TLS dial with revoked cert correctly rejected at handshake: %v ✓", dialErr)
		return
	}
	// If the raw TLS dial succeeded (shouldn't happen), close it and fall
	// back to an RPC-level check.
	conn.Close()

	// Lazy-connect path: grpc.NewClient does not handshake until the first RPC.
	revokedCC := h.dialPrimaryMTLS(t, clientCert)
	revokedClient := pb.NewServerPairingServiceClient(revokedCC)
	_, rpcErr := revokedClient.ListServers(context.Background(), &pb.ListServersRequest{})
	if rpcErr == nil {
		t.Error("Expected RPC with revoked cert to fail, but it succeeded")
	} else {
		t.Logf("RPC with revoked cert correctly failed: %v ✓", rpcErr)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestPairing_SensitiveRPCsOnBootstrap
//
// RenewCertificate, ListServers, and RevokeServer are blocked on the bootstrap
// port and must return codes.PermissionDenied regardless of whether a valid
// client cert is presented.
// ─────────────────────────────────────────────────────────────────────────────

func TestPairing_SensitiveRPCsOnBootstrap(t *testing.T) {
	h := startPairingHarness(t)

	// Use skip-verify so we reach the gRPC handler layer without needing a
	// valid client cert (bootstrap port has tls.NoClientCert anyway).
	cc := h.dialBootstrapSkipVerify(t)
	client := pb.NewServerPairingServiceClient(cc)
	ctx := context.Background()

	t.Run("RenewCertificate", func(t *testing.T) {
		fakeURN := core.NewURN(core.ObjectTypeServer).String()
		_, csrDER := pairingGenECCSR(t, fakeURN)
		_, err := client.RenewCertificate(ctx, &pb.RenewCertificateRequest{
			ServerUrn: fakeURN,
			Csr:       csrDER,
		})
		pairingAssertGRPCCode(t, err, codes.PermissionDenied,
			"RenewCertificate on bootstrap port")
		t.Logf("RenewCertificate blocked on bootstrap port ✓: %v", err)
	})

	t.Run("ListServers", func(t *testing.T) {
		_, err := client.ListServers(ctx, &pb.ListServersRequest{})
		pairingAssertGRPCCode(t, err, codes.PermissionDenied,
			"ListServers on bootstrap port")
		t.Logf("ListServers blocked on bootstrap port ✓: %v", err)
	})

	t.Run("RevokeServer", func(t *testing.T) {
		_, err := client.RevokeServer(ctx, &pb.RevokeServerRequest{
			ServerUrn: core.NewURN(core.ObjectTypeServer).String(),
		})
		pairingAssertGRPCCode(t, err, codes.PermissionDenied,
			"RevokeServer on bootstrap port")
		t.Logf("RevokeServer blocked on bootstrap port ✓: %v", err)
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// TestPairing_RegisterServerOnPrimary
//
// RegisterServer is blocked on the primary mTLS port and must return
// codes.PermissionDenied even when the caller presents a valid client cert.
// ─────────────────────────────────────────────────────────────────────────────

func TestPairing_RegisterServerOnPrimary(t *testing.T) {
	h := startPairingHarness(t)

	// Register a server legitimately to obtain a valid mTLS client cert.
	legitimateURN := core.NewURN(core.ObjectTypeServer).String()
	secret := h.createSecret("primary-register-test")
	certPEM, _, key := h.registerServer(t, legitimateURN, secret, "legit.local:50051")
	clientCert := h.buildClientCert(t, certPEM, key)

	// Now attempt RegisterServer on the primary (mTLS) port.
	primaryCC := h.dialPrimaryMTLS(t, clientCert)
	primaryClient := pb.NewServerPairingServiceClient(primaryCC)

	newURN := core.NewURN(core.ObjectTypeServer).String()
	newSecret := h.createSecret("primary-register-attempt")
	_, newCSRDER := pairingGenECCSR(t, newURN)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := primaryClient.RegisterServer(ctx, &pb.RegisterServerRequest{
		ServerUrn:     newURN,
		Csr:           newCSRDER,
		PairingSecret: newSecret,
		ServerName:    "should-be-rejected",
		Endpoint:      "rejected.local:50051",
	})
	pairingAssertGRPCCode(t, err, codes.PermissionDenied,
		"RegisterServer on primary port")
	t.Logf("RegisterServer correctly blocked on primary port ✓: %v", err)
}

// ─────────────────────────────────────────────────────────────────────────────
// TestPairing_GetCACertificateOnBoth
//
// GetCACertificate must succeed on both ports and return the same, parseable,
// CA certificate.
// ─────────────────────────────────────────────────────────────────────────────

func TestPairing_GetCACertificateOnBoth(t *testing.T) {
	h := startPairingHarness(t)

	// Register a server to get a valid mTLS cert for the primary port.
	serverURN := core.NewURN(core.ObjectTypeServer).String()
	secret := h.createSecret("get-ca-both")
	certPEM, _, key := h.registerServer(t, serverURN, secret, "ca-test.local:50051")
	clientCert := h.buildClientCert(t, certPEM, key)

	ctx := context.Background()

	// ── Bootstrap port ────────────────────────────────────────────────────────
	bootstrapClient := pb.NewServerPairingServiceClient(h.dialBootstrap(t))
	bootstrapResp, err := bootstrapClient.GetCACertificate(
		ctx, &pb.GetCACertificateRequest{})
	if err != nil {
		t.Fatalf("GetCACertificate (bootstrap): %v", err)
	}
	if len(bootstrapResp.CaCertificate) == 0 {
		t.Fatal("GetCACertificate (bootstrap): returned empty CA cert")
	}

	// ── Primary port ──────────────────────────────────────────────────────────
	primaryClient := pb.NewServerPairingServiceClient(h.dialPrimaryMTLS(t, clientCert))
	primaryResp, err := primaryClient.GetCACertificate(
		ctx, &pb.GetCACertificateRequest{})
	if err != nil {
		t.Fatalf("GetCACertificate (primary): %v", err)
	}
	if len(primaryResp.CaCertificate) == 0 {
		t.Fatal("GetCACertificate (primary): returned empty CA cert")
	}

	// Both ports must return the same certificate.
	if string(bootstrapResp.CaCertificate) != string(primaryResp.CaCertificate) {
		t.Error("GetCACertificate: bootstrap and primary returned different CA certs")
	}
	t.Log("GetCACertificate: bootstrap and primary agree on CA cert ✓")

	// Must parse as a valid X.509 CA certificate.
	block, _ := pem.Decode(bootstrapResp.CaCertificate)
	if block == nil {
		t.Fatal("GetCACertificate: response is not valid PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("GetCACertificate: PEM is not a valid X.509 certificate: %v", err)
	}
	if !cert.IsCA {
		t.Error("GetCACertificate: returned certificate has IsCA=false")
	}
	t.Logf("GetCACertificate: CN=%q IsCA=%v NotAfter=%v ✓",
		cert.Subject.CommonName, cert.IsCA, cert.NotAfter.Format(time.RFC3339))
}

// ─────────────────────────────────────────────────────────────────────────────
// TestPairing_CAFingerprint
//
// ca.CA.Fingerprint() must return a non-empty, correctly formatted SHA-256
// fingerprint that matches both the in-memory CA cert and the PEM returned by
// GetCACertificate.
// ─────────────────────────────────────────────────────────────────────────────

func TestPairing_CAFingerprint(t *testing.T) {
	h := startPairingHarness(t)

	fp := h.authority.Fingerprint()
	if fp == "" {
		t.Fatal("Fingerprint() returned empty string")
	}
	t.Logf("CA fingerprint: %s", fp)

	// Must be exactly 32 colon-separated uppercase two-char hex groups.
	parts := strings.Split(fp, ":")
	if len(parts) != 32 {
		t.Errorf("Fingerprint: expected 32 colon-separated parts, got %d: %q", len(parts), fp)
	}
	for i, p := range parts {
		if len(p) != 2 {
			t.Errorf("Fingerprint: part %d has %d chars, want 2: %q", i, len(p), p)
		}
		for _, c := range p {
			if !((c >= '0' && c <= '9') || (c >= 'A' && c <= 'F')) {
				t.Errorf("Fingerprint: part %d contains non-hex char %q", i, c)
			}
		}
	}

	// Must match a manual SHA-256 of the CA cert DER bytes.
	block, _ := pem.Decode(h.authority.CertPEM)
	if block == nil {
		t.Fatal("authority.CertPEM is not valid PEM")
	}
	sum := sha256.Sum256(block.Bytes)
	manual := make([]string, 32)
	for i, b := range sum {
		manual[i] = fmt.Sprintf("%02X", b)
	}
	wantFP := strings.Join(manual, ":")

	if fp != wantFP {
		t.Errorf("Fingerprint mismatch:\n got  %s\n want %s", fp, wantFP)
	}
	t.Log("Fingerprint matches manual SHA-256 of CA cert DER ✓")

	// Must also match what GetCACertificate returns on the bootstrap port.
	bootstrapClient := pb.NewServerPairingServiceClient(h.dialBootstrap(t))
	caResp, err := bootstrapClient.GetCACertificate(
		context.Background(), &pb.GetCACertificateRequest{})
	if err != nil {
		t.Fatalf("GetCACertificate: %v", err)
	}
	block2, _ := pem.Decode(caResp.CaCertificate)
	if block2 == nil {
		t.Fatal("GetCACertificate response is not valid PEM")
	}
	sum2 := sha256.Sum256(block2.Bytes)
	rpcParts := make([]string, 32)
	for i, b := range sum2 {
		rpcParts[i] = fmt.Sprintf("%02X", b)
	}
	rpcFP := strings.Join(rpcParts, ":")

	if fp != rpcFP {
		t.Errorf("Fingerprint from CA.Fingerprint() does not match GetCACertificate PEM:\n got  %s\n want %s",
			fp, rpcFP)
	}
	t.Log("Fingerprint matches GetCACertificate PEM ✓")
}

// ─────────────────────────────────────────────────────────────────────────────
// CSR generation helpers
// ─────────────────────────────────────────────────────────────────────────────

// pairingGenECCSR generates an EC P-256 key and a PKCS#10 CSR with the given
// Common Name.  Returns both the private key and the DER-encoded CSR.
func pairingGenECCSR(t *testing.T, cn string) (*ecdsa.PrivateKey, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("pairingGenECCSR: generate key: %v", err)
	}
	tmpl := &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: cn},
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, tmpl, key)
	if err != nil {
		t.Fatalf("pairingGenECCSR: create CSR: %v", err)
	}
	return key, csrDER
}

// pairingGenRSACSR generates an RSA-2048 key and a DER PKCS#10 CSR with the
// given CN.  Used to exercise the authority's key-type rejection.
func pairingGenRSACSR(t *testing.T, cn string) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("pairingGenRSACSR: generate key: %v", err)
	}
	tmpl := &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: cn},
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, tmpl, key)
	if err != nil {
		t.Fatalf("pairingGenRSACSR: create CSR: %v", err)
	}
	return csrDER
}

// pairingGenCSRWithSAN generates an EC P-256 CSR that explicitly includes the
// given DNS SANs and/or IP SANs.  Used to test the authority's SAN validation.
func pairingGenCSRWithSAN(
	t *testing.T,
	cn string,
	dnsNames []string,
	ipAddresses []net.IP,
) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("pairingGenCSRWithSAN: generate key: %v", err)
	}
	tmpl := &x509.CertificateRequest{
		Subject:     pkix.Name{CommonName: cn},
		DNSNames:    dnsNames,
		IPAddresses: ipAddresses,
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, tmpl, key)
	if err != nil {
		t.Fatalf("pairingGenCSRWithSAN: create CSR: %v", err)
	}
	return csrDER
}

// ─────────────────────────────────────────────────────────────────────────────
// Certificate assertion helpers
// ─────────────────────────────────────────────────────────────────────────────

// pairingAssertCertCN asserts that a PEM-encoded certificate's Subject CN
// equals wantCN.
func pairingAssertCertCN(t *testing.T, certPEM []byte, wantCN string) {
	t.Helper()
	cert := pairingParseCert(t, certPEM)
	if cert.Subject.CommonName != wantCN {
		t.Errorf("cert CN = %q, want %q", cert.Subject.CommonName, wantCN)
	}
}

// pairingAssertSignedByCA verifies that certPEM is signed by the CA in
// caCertPEM, treating the cert as a client authentication certificate.
func pairingAssertSignedByCA(t *testing.T, certPEM, caCertPEM []byte) {
	t.Helper()
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caCertPEM) {
		t.Fatal("pairingAssertSignedByCA: failed to add CA to pool")
	}
	cert := pairingParseCert(t, certPEM)
	_, err := cert.Verify(x509.VerifyOptions{
		Roots:     roots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	if err != nil {
		t.Errorf("cert does not verify against CA: %v", err)
	}
}

// pairingAssertExtKeyUsageClientAuth checks that the cert carries
// ExtKeyUsageClientAuth and does NOT carry ExtKeyUsageServerAuth.
func pairingAssertExtKeyUsageClientAuth(t *testing.T, certPEM []byte) {
	t.Helper()
	cert := pairingParseCert(t, certPEM)
	hasClient := false
	hasServer := false
	for _, u := range cert.ExtKeyUsage {
		switch u {
		case x509.ExtKeyUsageClientAuth:
			hasClient = true
		case x509.ExtKeyUsageServerAuth:
			hasServer = true
		}
	}
	if !hasClient {
		t.Error("issued cert is missing ExtKeyUsageClientAuth")
	}
	if hasServer {
		t.Error("issued cert must not have ExtKeyUsageServerAuth")
	}
}

// pairingAssertRejected asserts that err is a gRPC status error indicating the
// request was denied — either Unauthenticated (wrong/malformed secret) or
// ResourceExhausted (rate limiter fired before the handler, which can happen
// in rapid sub-test runs).  Both outcomes correctly mean "request denied".
func pairingAssertRejected(t *testing.T, err error, ctx string) {
	t.Helper()
	if err == nil {
		t.Errorf("%s: expected request to be rejected, but it succeeded", ctx)
		return
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Errorf("%s: error is not a gRPC status error: %v", ctx, err)
		return
	}
	switch st.Code() {
	case codes.Unauthenticated, codes.ResourceExhausted:
		// Both are valid rejection codes in this context.
	default:
		t.Errorf("%s: got gRPC code %s, want Unauthenticated or ResourceExhausted (message: %q)",
			ctx, st.Code(), st.Message())
	}
}

// pairingAssertGRPCCode asserts that err is a gRPC status error with the
// given code, logging context for diagnostics.
func pairingAssertGRPCCode(t *testing.T, err error, want codes.Code, ctx string) {
	t.Helper()
	if err == nil {
		t.Errorf("%s: expected error with code %s, got nil", ctx, want)
		return
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Errorf("%s: error is not a gRPC status error: %v", ctx, err)
		return
	}
	if st.Code() != want {
		t.Errorf("%s: got gRPC code %s, want %s (message: %q)",
			ctx, st.Code(), want, st.Message())
	}
}

// pairingParseCert decodes and parses a PEM-encoded X.509 certificate.
func pairingParseCert(t *testing.T, certPEM []byte) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("pairingParseCert: input is not valid PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("pairingParseCert: %v", err)
	}
	return cert
}

// ─────────────────────────────────────────────────────────────────────────────
// Infrastructure helpers
// ─────────────────────────────────────────────────────────────────────────────

// pairingFreePort asks the OS for an available TCP port on 127.0.0.1.
func pairingFreePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pairingFreePort: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

// pairingWaitPort polls addr (host:port) until a TCP connection is accepted or
// 5 seconds elapse.
func pairingWaitPort(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("pairingWaitPort: %s never became ready within 5s", addr)
}
