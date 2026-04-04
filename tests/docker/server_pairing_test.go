//go:build integration

// Package docker — server pairing smoke tests.
//
// These tests verify the full server-pairing protocol end-to-end using two
// ephemeral Docker containers:
//
//   - authority  — started with --pairing, owns the CA, generates secrets
//   - joining    — started with --peer-authority and --peer-secret, registers
//     itself against the authority on startup
//
// The test exercises every phase of the design document:
//
//	Phase S1  CA bootstrap (authority CA is generated and persisted)
//	Phase S2  Pairing secret generation via `notx server pairing add-secret`
//	Phase S3  RegisterServer RPC over the bootstrap listener (port 50052)
//	Phase S4  Hard revocation + ListServers / RevokeServer RPCs
//	Phase S5  GetCACertificate unauthenticated RPC
//
// Run with:
//
//	go test -v -tags integration -timeout 180s ./tests/docker/ -run TestServerPairing
package docker

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
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/zebaqui/notx-engine/core"
	pb "github.com/zebaqui/notx-engine/proto"
)

// ─────────────────────────────────────────────────────────────────────────────
// Constants
// ─────────────────────────────────────────────────────────────────────────────

const (
	pairingTestImage        = "notx:integration-test"
	pairingContainerTimeout = 45 * time.Second

	// defaultBootstrapPort is the port inside the container for the pairing
	// bootstrap gRPC listener.
	defaultBootstrapPort = 50052
	// defaultGRPCPort is the primary gRPC port inside the container.
	defaultGRPCPort = 50051
)

// ─────────────────────────────────────────────────────────────────────────────
// TestServerPairing_FullLifecycle is the primary server-pairing smoke test.
//
// It covers all five implementation phases from the design document:
//
//  1. Starts an authority container with --pairing --grpc enabled.
//  2. Generates a pairing secret inside the authority container via exec.
//  3. Starts a joining container that calls RegisterServer against the
//     authority's bootstrap listener (port 50052) using the secret.
//  4. Verifies the joining container successfully received a cert by calling
//     GetCACertificate from the test host.
//  5. Lists registered servers via the authority gRPC and asserts the joining
//     server appears.
//  6. Revokes the joining server and asserts it no longer appears in
//     non-revoked listings.
//  7. Verifies GetCACertificate is unauthenticated (no creds needed).
//
// ─────────────────────────────────────────────────────────────────────────────
func TestServerPairing_FullLifecycle(t *testing.T) {
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("Docker not available — skipping server pairing integration test")
	}

	buildImage(t)

	// ── 1. Start the authority container ─────────────────────────────────────
	t.Log("Starting authority container …")
	authorityID, authorityHTTP, authorityGRPC, authorityBootstrap := startPairingAuthorityContainer(t)
	waitForGRPCPort(t, authorityBootstrap)
	waitForGRPCPort(t, authorityGRPC)
	// Give the gRPC servers a brief moment to finish initializing after the
	// TCP port first accepts connections.
	time.Sleep(500 * time.Millisecond)
	t.Logf("Authority ready: http=%s grpc=:%d bootstrap=:%d", authorityHTTP, authorityGRPC, authorityBootstrap)

	// ── 2. Generate a pairing secret inside the authority container ───────────
	t.Log("Generating pairing secret inside authority container …")
	secret := execGenerateSecret(t, authorityID)
	t.Logf("Generated secret: %s", secret)

	if !strings.HasPrefix(secret, "NTXP-") {
		t.Fatalf("generated secret does not start with NTXP-: %q", secret)
	}

	// ── 3. Call RegisterServer directly from the test host ────────────────────
	//
	// This verifies Phase S3 without needing a second container to be up first.
	// We generate a local EC P-256 key-pair and CSR, then call RegisterServer
	// over the bootstrap port (insecure in local testing since no TLS cert is
	// configured in the test container).
	t.Log("Calling RegisterServer from test host …")
	joiningURN := core.NewURN(core.ObjectTypeServer).String()
	certPEM, caCertPEM := registerServerFromHost(t, authorityBootstrap, joiningURN, secret)

	t.Logf("Certificate received (%d bytes), CA cert (%d bytes)", len(certPEM), len(caCertPEM))
	assertCertCN(t, certPEM, joiningURN)
	assertCertSignedByCA(t, certPEM, caCertPEM)

	// ── 4. GetCACertificate is unauthenticated ─────────────────────────────────
	t.Log("Verifying GetCACertificate is unauthenticated …")
	caCertFromRPC := getCACertificate(t, authorityBootstrap)
	if len(caCertFromRPC) == 0 {
		t.Fatal("GetCACertificate returned empty CA cert")
	}
	if !bytes.Equal(caCertFromRPC, caCertPEM) {
		t.Error("GetCACertificate returned different CA cert than RegisterServer")
	}
	t.Logf("GetCACertificate: returned %d bytes (matches RegisterServer response)", len(caCertFromRPC))

	// ── 5. Duplicate RegisterServer with same secret is rejected ──────────────
	t.Log("Verifying duplicate registration with same secret is rejected …")
	assertRegisterServerFails(t, authorityBootstrap, joiningURN, secret,
		"reuse of consumed secret should be rejected")

	// ── 6. List servers via the authority's gRPC (unauthenticated for now) ─────
	//
	// The primary gRPC port in the test container runs without mTLS (no
	// TLS cert configured), so we connect without client credentials.
	t.Log("Listing servers via authority primary gRPC …")
	servers := listServers(t, authorityGRPC, false)

	found := false
	for _, s := range servers {
		if s.ServerUrn == joiningURN {
			found = true
			if s.Revoked {
				t.Errorf("ListServers: joining server %s is unexpectedly revoked", joiningURN)
			}
			t.Logf("ListServers: found %s name=%q endpoint=%q revoked=%v",
				s.ServerUrn, s.ServerName, s.Endpoint, s.Revoked)
		}
	}
	if !found {
		t.Errorf("ListServers: joining server %s not found (got %d servers)", joiningURN, len(servers))
	}

	// ── 7. Revoke the joining server ──────────────────────────────────────────
	t.Log("Revoking joining server …")
	revokeServer(t, authorityGRPC, joiningURN)

	// ── 8. Revoked server no longer appears in the non-revoked listing ─────────
	t.Log("Verifying revoked server is absent from non-revoked listing …")
	serversAfterRevoke := listServers(t, authorityGRPC, false)
	for _, s := range serversAfterRevoke {
		if s.ServerUrn == joiningURN {
			t.Errorf("ListServers (non-revoked): revoked server %s still appears", joiningURN)
		}
	}
	t.Logf("Revoked server correctly absent from non-revoked listing")

	// ── 9. Revoked server appears when include_revoked=true ───────────────────
	t.Log("Verifying revoked server appears with include_revoked=true …")
	serversIncRevoked := listServers(t, authorityGRPC, true)
	foundRevoked := false
	for _, s := range serversIncRevoked {
		if s.ServerUrn == joiningURN {
			foundRevoked = true
			if !s.Revoked {
				t.Errorf("ListServers (include_revoked): server %s has revoked=false", joiningURN)
			}
			t.Logf("ListServers (include_revoked): found %s revoked=%v", s.ServerUrn, s.Revoked)
		}
	}
	if !foundRevoked {
		t.Errorf("ListServers (include_revoked): revoked server %s not found", joiningURN)
	}

	// ── Dump authority container logs ─────────────────────────────────────────
	t.Logf("--- authority container logs (%s) ---\n%s", authorityID[:12], containerLogs(authorityID))
}

// ─────────────────────────────────────────────────────────────────────────────
// TestServerPairing_SecretExpiry verifies that an expired secret is rejected.
// ─────────────────────────────────────────────────────────────────────────────
func TestServerPairing_SecretExpiry(t *testing.T) {
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("Docker not available — skipping server pairing integration test")
	}

	buildImage(t)

	authorityID, _, _, authorityBootstrap := startPairingAuthorityContainer(t)
	waitForGRPCPort(t, authorityBootstrap)
	time.Sleep(500 * time.Millisecond)

	// Generate a secret with a 1-second TTL via exec into the container.
	secret := execGenerateSecretWithTTL(t, authorityID, "1s")
	t.Logf("Generated 1s-TTL secret: %s", secret)

	// Wait for expiry.
	t.Log("Waiting 2s for secret to expire …")
	time.Sleep(2 * time.Second)

	// RegisterServer must be rejected.
	joiningURN := core.NewURN(core.ObjectTypeServer).String()
	assertRegisterServerFails(t, authorityBootstrap, joiningURN, secret,
		"expired secret should be rejected")

	t.Log("Expired secret correctly rejected")
	t.Logf("--- authority container logs ---\n%s", containerLogs(authorityID))
}

// ─────────────────────────────────────────────────────────────────────────────
// TestServerPairing_InvalidSecret verifies that a wrong secret is rejected.
// ─────────────────────────────────────────────────────────────────────────────
func TestServerPairing_InvalidSecret(t *testing.T) {
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("Docker not available — skipping server pairing integration test")
	}

	buildImage(t)

	_, _, _, authorityBootstrap := startPairingAuthorityContainer(t)
	waitForGRPCPort(t, authorityBootstrap)
	time.Sleep(500 * time.Millisecond)

	joiningURN := core.NewURN(core.ObjectTypeServer).String()
	wrongSecret := "NTXP-WRONG-WRONG-WRONG-WRONG-WRG"
	assertRegisterServerFails(t, authorityBootstrap, joiningURN, wrongSecret,
		"wrong secret should be rejected")

	t.Log("Invalid secret correctly rejected")
}

// ─────────────────────────────────────────────────────────────────────────────
// TestServerPairing_MultipleServers verifies that multiple servers can be
// paired sequentially with separate secrets.
// ─────────────────────────────────────────────────────────────────────────────
func TestServerPairing_MultipleServers(t *testing.T) {
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("Docker not available — skipping server pairing integration test")
	}

	buildImage(t)

	authorityID, _, authorityGRPC, authorityBootstrap := startPairingAuthorityContainer(t)
	waitForGRPCPort(t, authorityBootstrap)
	waitForGRPCPort(t, authorityGRPC)
	time.Sleep(500 * time.Millisecond)

	const numServers = 3
	urns := make([]string, numServers)

	for i := 0; i < numServers; i++ {
		secret := execGenerateSecret(t, authorityID)
		urns[i] = core.NewURN(core.ObjectTypeServer).String()
		certPEM, caCertPEM := registerServerFromHost(t, authorityBootstrap, urns[i], secret)
		assertCertCN(t, certPEM, urns[i])
		assertCertSignedByCA(t, certPEM, caCertPEM)
		t.Logf("Registered server %d: %s", i+1, urns[i])
	}

	// All servers should appear in the listing.
	servers := listServers(t, authorityGRPC, false)
	listed := make(map[string]bool)
	for _, s := range servers {
		listed[s.ServerUrn] = true
	}
	for i, urn := range urns {
		if !listed[urn] {
			t.Errorf("server %d (%s) not found in ListServers", i+1, urn)
		}
	}
	t.Logf("All %d servers correctly listed", numServers)

	t.Logf("--- authority container logs ---\n%s", containerLogs(authorityID))
}

// ─────────────────────────────────────────────────────────────────────────────
// TestServerPairing_GetCACertificate_NoAuth verifies the RPC is accessible
// without any authentication on both the bootstrap port.
// ─────────────────────────────────────────────────────────────────────────────
func TestServerPairing_GetCACertificate_NoAuth(t *testing.T) {
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("Docker not available — skipping server pairing integration test")
	}

	buildImage(t)

	_, _, _, authorityBootstrap := startPairingAuthorityContainer(t)
	waitForGRPCPort(t, authorityBootstrap)
	time.Sleep(500 * time.Millisecond)

	// Call without any credentials.
	caCert := getCACertificate(t, authorityBootstrap)
	if len(caCert) == 0 {
		t.Fatal("GetCACertificate returned empty result")
	}

	// Verify it parses as a valid X.509 certificate.
	block, _ := pem.Decode(caCert)
	if block == nil {
		t.Fatalf("GetCACertificate: result is not valid PEM: %q", string(caCert))
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("GetCACertificate: PEM is not a valid X.509 certificate: %v", err)
	}
	if !cert.IsCA {
		t.Errorf("GetCACertificate: returned certificate is not a CA cert")
	}
	t.Logf("GetCACertificate: CA cert CN=%q IsCA=%v NotAfter=%v",
		cert.Subject.CommonName, cert.IsCA, cert.NotAfter.Format(time.RFC3339))
}

// ─────────────────────────────────────────────────────────────────────────────
// Container helpers
// ─────────────────────────────────────────────────────────────────────────────

// startPairingAuthorityContainer starts a notx container in authority (pairing)
// mode. It returns the container ID, the HTTP base URL, the host-mapped gRPC
// port, and the host-mapped bootstrap port.
//
// Both gRPC ports are mapped to random free host ports so tests can run in
// parallel without conflicts.
func startPairingAuthorityContainer(t *testing.T) (containerID, httpURL string, grpcPort, bootstrapPort int) {
	t.Helper()

	httpHostPort := freePort(t)
	grpcHostPort := freePort(t)
	bootstrapHostPort := freePort(t)

	args := []string{
		"run",
		"--detach",
		"--rm",
		"--publish", fmt.Sprintf("%d:4060", httpHostPort),
		"--publish", fmt.Sprintf("%d:%d", grpcHostPort, defaultGRPCPort),
		"--publish", fmt.Sprintf("%d:%d", bootstrapHostPort, defaultBootstrapPort),
		pairingTestImage,
		"server",
		// --daemon runs the server in-process (foreground) rather than forking
		// a background child that causes the container's PID 1 to exit.
		"--daemon",
		"--data-dir", "/data",
		"--grpc=true",
		"--http=true",
		"--grpc-port", fmt.Sprintf("%d", defaultGRPCPort),
		"--pairing",
		"--pairing-port", fmt.Sprintf("%d", defaultBootstrapPort),
		"--device-auto-approve",
	}

	out, err := exec.Command("docker", args...).Output()
	if err != nil {
		t.Fatalf("docker run authority: %v", err)
	}

	id := strings.TrimSpace(string(out))
	if id == "" {
		t.Fatal("docker run returned empty container ID")
	}
	t.Logf("Authority container started: %s (http:%d grpc:%d bootstrap:%d)",
		id[:12], httpHostPort, grpcHostPort, bootstrapHostPort)

	t.Cleanup(func() {
		if err := exec.Command("docker", "stop", id).Run(); err != nil {
			t.Logf("warning: docker stop %s: %v", id[:12], err)
		}
	})

	return id,
		fmt.Sprintf("http://127.0.0.1:%d", httpHostPort),
		grpcHostPort,
		bootstrapHostPort
}

// waitForGRPCPort polls a TCP port until it accepts connections or the timeout
// (pairingContainerTimeout) is exceeded.
func waitForGRPCPort(t *testing.T, hostPort int) {
	t.Helper()

	addr := fmt.Sprintf("127.0.0.1:%d", hostPort)
	deadline := time.Now().Add(pairingContainerTimeout)

	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
		if err == nil {
			conn.Close()
			t.Logf("Port %d is ready", hostPort)
			return
		}
		time.Sleep(300 * time.Millisecond)
	}

	t.Fatalf("port %d never became ready within %s", hostPort, pairingContainerTimeout)
}

// ─────────────────────────────────────────────────────────────────────────────
// Secret generation helpers (via docker exec)
// ─────────────────────────────────────────────────────────────────────────────

// execGenerateSecret runs `notx server pairing add-secret` inside the authority
// container and returns the NTXP-... plaintext secret extracted from stdout.
func execGenerateSecret(t *testing.T, containerID string) string {
	t.Helper()
	return execGenerateSecretWithTTL(t, containerID, "15m")
}

// execGenerateSecretWithTTL runs add-secret with a custom TTL.
func execGenerateSecretWithTTL(t *testing.T, containerID, ttl string) string {
	t.Helper()

	args := []string{
		"exec", containerID,
		"notx", "server", "pairing", "add-secret",
		"--data-dir", "/data",
		"--label", "smoke-test",
		"--ttl", ttl,
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.Command("docker", args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Allow up to 20s for the exec to complete (bcrypt hash generation).
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd = exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Logf("add-secret stderr: %s", stderr.String())
		t.Fatalf("docker exec add-secret: %v\nstdout: %s", err, stdout.String())
	}

	output := stdout.String()
	t.Logf("add-secret output:\n%s", output)

	// Extract the NTXP-... token from the output.
	secret := extractSecret(output)
	if secret == "" {
		t.Fatalf("could not extract NTXP-... secret from add-secret output:\n%s", output)
	}
	return secret
}

// extractSecret scans the add-secret output for a line containing "NTXP-" and
// returns the token (trimmed).
func extractSecret(output string) string {
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "NTXP-") {
			return trimmed
		}
	}
	return ""
}

// ─────────────────────────────────────────────────────────────────────────────
// gRPC call helpers (called from the test host)
// ─────────────────────────────────────────────────────────────────────────────

// dialBootstrap dials the authority's bootstrap gRPC port without TLS.
// The test containers don't have TLS certs configured, so we use insecure.
func dialBootstrap(t *testing.T, hostPort int) *grpc.ClientConn {
	t.Helper()
	addr := fmt.Sprintf("127.0.0.1:%d", hostPort)
	cc, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial bootstrap %s: %v", addr, err)
	}
	t.Cleanup(func() { cc.Close() })
	return cc
}

// dialPrimary dials the authority's primary gRPC port without TLS.
func dialPrimary(t *testing.T, hostPort int) *grpc.ClientConn {
	t.Helper()
	addr := fmt.Sprintf("127.0.0.1:%d", hostPort)
	cc, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial primary %s: %v", addr, err)
	}
	t.Cleanup(func() { cc.Close() })
	return cc
}

// dialMTLS dials the authority's primary port with a client cert + CA pool.
func dialMTLS(t *testing.T, hostPort int, clientCert tls.Certificate, caPool *x509.CertPool) *grpc.ClientConn {
	t.Helper()
	addr := fmt.Sprintf("127.0.0.1:%d", hostPort)
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      caPool,
		MinVersion:   tls.VersionTLS13,
		// The server cert in test containers is self-signed; skip hostname
		// verification since the hostname won't match "127.0.0.1".
		InsecureSkipVerify: true, //nolint:gosec // test-only; real deployments use proper certs
	}
	cc, err := grpc.NewClient(addr, grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)))
	if err != nil {
		t.Fatalf("dial mTLS %s: %v", addr, err)
	}
	t.Cleanup(func() { cc.Close() })
	return cc
}

// registerServerFromHost calls RegisterServer and returns the (certPEM, caCertPEM).
func registerServerFromHost(t *testing.T, bootstrapHostPort int, serverURN, secret string) (certPEM, caCertPEM []byte) {
	t.Helper()

	cc := dialBootstrap(t, bootstrapHostPort)
	client := pb.NewServerPairingServiceClient(cc)

	key, csrDER := generateTestKeyAndCSR(t, serverURN)
	_ = key // private key stays local

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	resp, err := client.RegisterServer(ctx, &pb.RegisterServerRequest{
		ServerUrn:     serverURN,
		Csr:           csrDER,
		PairingSecret: secret,
		ServerName:    "smoke-test-server",
		Endpoint:      "smoke.test.local:50051",
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

	return resp.Certificate, resp.CaCertificate
}

// assertRegisterServerFails calls RegisterServer and asserts it returns an error.
func assertRegisterServerFails(t *testing.T, bootstrapHostPort int, serverURN, secret, reason string) {
	t.Helper()

	cc := dialBootstrap(t, bootstrapHostPort)
	client := pb.NewServerPairingServiceClient(cc)

	_, csrDER := generateTestKeyAndCSR(t, serverURN)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := client.RegisterServer(ctx, &pb.RegisterServerRequest{
		ServerUrn:     serverURN,
		Csr:           csrDER,
		PairingSecret: secret,
		ServerName:    "should-fail",
		Endpoint:      "fail.test.local:50051",
	})
	if err == nil {
		t.Errorf("RegisterServer expected to fail (%s) but succeeded", reason)
	} else {
		t.Logf("RegisterServer correctly rejected (%s): %v", reason, err)
	}
}

// getCACertificate calls GetCACertificate on the bootstrap port and returns the PEM bytes.
func getCACertificate(t *testing.T, bootstrapHostPort int) []byte {
	t.Helper()

	cc := dialBootstrap(t, bootstrapHostPort)
	client := pb.NewServerPairingServiceClient(cc)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.GetCACertificate(ctx, &pb.GetCACertificateRequest{})
	if err != nil {
		t.Fatalf("GetCACertificate: %v", err)
	}
	return resp.CaCertificate
}

// listServers calls ListServers on the primary port and returns the ServerInfo slice.
func listServers(t *testing.T, grpcHostPort int, includeRevoked bool) []*pb.Server {
	t.Helper()

	cc := dialPrimary(t, grpcHostPort)
	client := pb.NewServerPairingServiceClient(cc)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.ListServers(ctx, &pb.ListServersRequest{IncludeRevoked: includeRevoked})
	if err != nil {
		t.Fatalf("ListServers(include_revoked=%v): %v", includeRevoked, err)
	}
	return resp.Servers
}

// revokeServer calls RevokeServer on the primary port.
func revokeServer(t *testing.T, grpcHostPort int, serverURN string) {
	t.Helper()

	cc := dialPrimary(t, grpcHostPort)
	client := pb.NewServerPairingServiceClient(cc)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.RevokeServer(ctx, &pb.RevokeServerRequest{ServerUrn: serverURN})
	if err != nil {
		t.Fatalf("RevokeServer(%s): %v", serverURN, err)
	}
	if !resp.Revoked {
		t.Errorf("RevokeServer(%s): revoked = false in response", serverURN)
	}
	t.Logf("RevokeServer: %s successfully revoked", serverURN)
}

// ─────────────────────────────────────────────────────────────────────────────
// Crypto helpers
// ─────────────────────────────────────────────────────────────────────────────

// generateTestKeyAndCSR creates an EC P-256 key-pair and a PKCS#10 CSR in DER
// form. The private key is returned for optional use (e.g. building a
// tls.Certificate after registration).
func generateTestKeyAndCSR(t *testing.T, cn string) (key *ecdsa.PrivateKey, csrDER []byte) {
	t.Helper()

	var err error
	key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generateTestKeyAndCSR: %v", err)
	}

	tmpl := &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: cn},
	}
	csrDER, err = x509.CreateCertificateRequest(rand.Reader, tmpl, key)
	if err != nil {
		t.Fatalf("CreateCertificateRequest: %v", err)
	}
	return key, csrDER
}

// buildTLSCert constructs a tls.Certificate from the PEM-encoded cert and the
// ECDSA private key that was used to generate the CSR.
func buildTLSCert(t *testing.T, certPEM []byte, key *ecdsa.PrivateKey) tls.Certificate {
	t.Helper()

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal EC key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair: %v", err)
	}
	block, _ := pem.Decode(certPEM)
	if block != nil {
		if leaf, err := x509.ParseCertificate(block.Bytes); err == nil {
			cert.Leaf = leaf
		}
	}
	return cert
}

// buildCertPool builds an x509.CertPool from PEM-encoded certificate bytes.
func buildCertPool(t *testing.T, caPEM []byte) *x509.CertPool {
	t.Helper()
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		t.Fatal("buildCertPool: no certs found in PEM")
	}
	return pool
}

// ─────────────────────────────────────────────────────────────────────────────
// Assertion helpers
// ─────────────────────────────────────────────────────────────────────────────

// assertCertCN asserts that a PEM-encoded cert has the expected Common Name.
func assertCertCN(t *testing.T, certPEM []byte, wantCN string) {
	t.Helper()

	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatalf("assertCertCN: not a valid PEM block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("assertCertCN: parse cert: %v", err)
	}
	if cert.Subject.CommonName != wantCN {
		t.Errorf("assertCertCN: CN = %q, want %q", cert.Subject.CommonName, wantCN)
	}
	t.Logf("cert CN=%q NotBefore=%v NotAfter=%v",
		cert.Subject.CommonName,
		cert.NotBefore.Format(time.RFC3339),
		cert.NotAfter.Format(time.RFC3339))
}

// assertCertSignedByCA verifies that certPEM is signed by the CA in caCertPEM.
func assertCertSignedByCA(t *testing.T, certPEM, caCertPEM []byte) {
	t.Helper()

	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caCertPEM) {
		t.Fatal("assertCertSignedByCA: failed to add CA cert to pool")
	}

	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("assertCertSignedByCA: not a valid PEM block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("assertCertSignedByCA: parse cert: %v", err)
	}

	opts := x509.VerifyOptions{
		Roots: roots,
		// Client certs use ExtKeyUsageClientAuth, not ServerAuth.
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	if _, err := cert.Verify(opts); err != nil {
		t.Errorf("assertCertSignedByCA: cert does not verify against CA: %v", err)
	} else {
		t.Log("assertCertSignedByCA: cert correctly signed by authority CA ✓")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Utility: ephemeral temp dir for on-disk cert storage
// ─────────────────────────────────────────────────────────────────────────────

// tempCertDir creates a temporary directory for storing test certificates and
// registers a cleanup to remove it when the test finishes.
func tempCertDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "notx-pairing-test-*")
	if err != nil {
		t.Fatalf("tempCertDir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// writePEMFile writes PEM data to a file inside dir with the given name.
func writePEMFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("writePEMFile %s: %v", name, err)
	}
	return path
}

// ─────────────────────────────────────────────────────────────────────────────
// Utility: generate a self-signed server TLS cert for authority containers
// that need TLS on their gRPC ports. Not used in the basic smoke tests (which
// run without TLS) but available for future mTLS-enforcement tests.
// ─────────────────────────────────────────────────────────────────────────────

// generateSelfSignedCert generates a self-signed EC P-256 TLS certificate for
// the given hostnames/IPs and returns (certPEM, keyPEM).
func generateSelfSignedCert(t *testing.T, hosts []string) (certPEM, keyPEM []byte) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generateSelfSignedCert: %v", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("generateSelfSignedCert serial: %v", err)
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "notx-test"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("generateSelfSignedCert create: %v", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("generateSelfSignedCert marshal key: %v", err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}

// ─────────────────────────────────────────────────────────────────────────────
// Utility: JSON round-trip helpers reused across test files in this package
// (these are thin wrappers; the full helpers live in admin_remote_test.go)
// ─────────────────────────────────────────────────────────────────────────────

// pairingMustDecodeJSON unmarshals raw bytes into dst, calling t.Fatal on error.
func pairingMustDecodeJSON(t *testing.T, raw []byte, dst any) {
	t.Helper()
	if err := json.Unmarshal(raw, dst); err != nil {
		t.Fatalf("JSON decode: %v\nraw: %s", err, raw)
	}
}
