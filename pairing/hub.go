package pairing

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/zebaqui/notx-engine/ca"
	pb "github.com/zebaqui/notx-engine/internal/server/proto"
	"github.com/zebaqui/notx-engine/repo"
)

// Hub manages both gRPC listeners for server pairing:
//
//   - Bootstrap listener (e.g. :50052) — TLS, no client cert required;
//     the pairing secret authenticates the registering server.
//   - Primary listener  (e.g. :50051) — full mTLS, client cert required;
//     used for certificate renewal after initial registration.
//
// Obtain a Hub via StartHub. Shut it down by cancelling the context passed to
// StartHub, or by calling Stop directly.
type Hub struct {
	bootstrapSrv *grpc.Server
	primarySrv   *grpc.Server
	log          *slog.Logger
}

// Stop gracefully shuts down both gRPC listeners, waiting for in-flight RPCs
// to complete before returning.
func (h *Hub) Stop() {
	h.bootstrapSrv.GracefulStop()
	h.primarySrv.GracefulStop()
	h.log.Info("pairing hub stopped")
}

// StartHub starts both gRPC pairing listeners and returns a running Hub.
//
//   - bootstrapAddr  e.g. ":50052" — TLS, no client cert; pairing secret is auth
//   - primaryAddr    e.g. ":50051" — mTLS, client cert required for renewals
//   - caDir          directory holding (or to generate) the CA key + cert
//   - srvRepo        ServerRepository scoped to the appropriate tenant/namespace
//   - secretStore    PairingSecretStore scoped to the same tenant/namespace
//   - certTTL        validity window for issued server certificates
//   - log            structured logger
//
// On first run, StartHub generates a CA and a server TLS certificate under
// caDir. On subsequent runs both are loaded from disk.
//
// The Hub shuts down gracefully when ctx is cancelled.
func StartHub(
	ctx context.Context,
	bootstrapAddr, primaryAddr, caDir string,
	srvRepo repo.ServerRepository,
	secretStore repo.PairingSecretStore,
	certTTL time.Duration,
	log *slog.Logger,
) (*Hub, error) {
	// ── 1. Load or generate the platform CA ──────────────────────────────────
	authority, err := ca.LoadOrGenerate(caDir)
	if err != nil {
		return nil, fmt.Errorf("pairing hub: load CA: %w", err)
	}

	// ── 2. Load or generate the server TLS certificate ───────────────────────
	serverCert, err := LoadServerCert(caDir, authority)
	if err != nil {
		return nil, fmt.Errorf("pairing hub: load server cert: %w", err)
	}

	// ── 3. Build the PairingService ───────────────────────────────────────────
	svc := NewPairingService(authority, srvRepo, secretStore, certTTL, log)

	// ── 4. Rebuild the in-memory revocation deny-set ─────────────────────────
	if err := svc.RebuildDenySet(ctx); err != nil {
		return nil, fmt.Errorf("pairing hub: rebuild deny set: %w", err)
	}

	// ── 5. Bootstrap listener — TLS, no client cert ───────────────────────────
	bootstrapCreds := credentials.NewTLS(BuildBootstrapTLSConfig(serverCert))
	bootstrapSrv := grpc.NewServer(grpc.Creds(bootstrapCreds))
	pb.RegisterServerPairingServiceServer(bootstrapSrv, svc)

	bootstrapLn, err := net.Listen("tcp", bootstrapAddr)
	if err != nil {
		return nil, fmt.Errorf("pairing hub: bootstrap listen on %s: %w", bootstrapAddr, err)
	}

	go func() {
		log.Info("pairing bootstrap listener started", "addr", bootstrapAddr)
		if err := bootstrapSrv.Serve(bootstrapLn); err != nil {
			log.Error("pairing bootstrap listener stopped", "error", err)
		}
	}()

	// ── 6. Primary listener — full mTLS ──────────────────────────────────────
	mtlsCfg, err := svc.BuildMTLSConfig(serverCert)
	if err != nil {
		bootstrapSrv.Stop()
		return nil, fmt.Errorf("pairing hub: build mTLS config: %w", err)
	}
	primaryCreds := credentials.NewTLS(mtlsCfg)
	primarySrv := grpc.NewServer(grpc.Creds(primaryCreds))
	pb.RegisterServerPairingServiceServer(primarySrv, svc)

	primaryLn, err := net.Listen("tcp", primaryAddr)
	if err != nil {
		bootstrapSrv.Stop()
		return nil, fmt.Errorf("pairing hub: primary listen on %s: %w", primaryAddr, err)
	}

	go func() {
		log.Info("pairing primary listener started", "addr", primaryAddr)
		if err := primarySrv.Serve(primaryLn); err != nil {
			log.Error("pairing primary listener stopped", "error", err)
		}
	}()

	hub := &Hub{
		bootstrapSrv: bootstrapSrv,
		primarySrv:   primarySrv,
		log:          log,
	}

	// ── 7. Context-driven graceful shutdown ───────────────────────────────────
	go func() {
		<-ctx.Done()
		hub.Stop()
	}()

	log.Info("pairing hub started",
		"bootstrap_addr", bootstrapAddr,
		"primary_addr", primaryAddr,
		"ca_dir", caDir,
		"cert_ttl", certTTL,
	)

	return hub, nil
}

// LoadServerCert loads the server TLS certificate from caDir (files
// "server.crt" and "server.key"). If either file is absent a new key-pair is
// generated and signed by authority, then written to disk for reuse.
//
// The returned tls.Certificate is suitable for use with both
// BuildBootstrapTLSConfig and PairingService.BuildMTLSConfig.
func LoadServerCert(caDir string, authority *ca.CA) (tls.Certificate, error) {
	certPath := filepath.Join(caDir, "server.crt")
	keyPath := filepath.Join(caDir, "server.key")

	_, certErr := os.Stat(certPath)
	_, keyErr := os.Stat(keyPath)

	if certErr == nil && keyErr == nil {
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("load server cert: %w", err)
		}
		return cert, nil
	}

	// Generate a fresh EC P-256 key and a CA-signed server certificate.
	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("load server cert: generate key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("load server cert: generate serial: %w", err)
	}

	now := time.Now().UTC()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "notx platform server",
			Organization: []string{"notx"},
		},
		NotBefore:   now.Add(-time.Minute),
		NotAfter:    now.Add(10 * 365 * 24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:    []string{"localhost"},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, authority.Cert, &serverKey.PublicKey, authority.Key)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("load server cert: sign certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return tls.Certificate{}, fmt.Errorf("load server cert: write cert: %w", err)
	}

	keyDER, err := x509.MarshalECPrivateKey(serverKey)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("load server cert: marshal key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return tls.Certificate{}, fmt.Errorf("load server cert: write key: %w", err)
	}

	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("load server cert: parse key pair: %w", err)
	}
	return tlsCert, nil
}
