package grpc

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
	"strings"
	"sync"

	"time"

	capkg "github.com/zebaqui/notx-engine/ca"
	"github.com/zebaqui/notx-engine/config"
	"github.com/zebaqui/notx-engine/core"
	"github.com/zebaqui/notx-engine/internal/grpcclient"
	pairingpkg "github.com/zebaqui/notx-engine/pairing"
	pb "github.com/zebaqui/notx-engine/proto"
	"github.com/zebaqui/notx-engine/repo"
)

// pairingCoreIface is a local interface satisfied by *pairing.pairingCore.
// Because pairingCore is unexported from the pairing package, we use an
// interface to hold the value returned by NewPairingCore and call the methods
// we need without relying on the concrete (unexported) type name.
type pairingCoreIface interface {
	RebuildDenySet(ctx context.Context) error
	BuildMTLSConfig(serverCert tls.Certificate) (*tls.Config, error)
	MakeTLSVerifyCallback() func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error
	IsCertRevoked(serial *big.Int) bool
}

// PairingServer is the grpc-layer wrapper that:
//   - Holds a bootstrapSvc registered on the bootstrap gRPC listener (:50052).
//   - Holds a primarySvc registered on the primary mTLS gRPC listener (:50051).
//   - Delegates RPC calls from the HTTP handler to primarySvc.
//   - Owns the outbound RegisterWithPeer logic (unchanged from prior version).
//
// The exported type name is preserved so the HTTP handler and server.go do not
// need signature changes.
type PairingServer struct {
	// core holds the shared pairingCore via interface so we can call
	// RebuildDenySet, BuildMTLSConfig, MakeTLSVerifyCallback without
	// referencing the unexported concrete type.
	core pairingCoreIface

	// bootstrapSvc is registered on the bootstrap gRPC server (:50052).
	bootstrapSvc *pairingpkg.BootstrapPairingService
	// primarySvc is registered on the primary mTLS gRPC server (:50051).
	primarySvc *pairingpkg.PrimaryPairingService

	// ca is needed for RegisterWithPeer (outbound pairing).
	ca  *capkg.CA
	cfg *config.Config
	log *slog.Logger

	// urnOnce ensures the server URN is resolved exactly once.
	urnOnce sync.Once
	urn     string
}

// NewPairingServer constructs a PairingServer by building the shared
// pairingCore and wrapping it in both service implementations.
func NewPairingServer(
	authority *capkg.CA,
	cfg *config.Config,
	srvRepo repo.ServerRepository,
	secretStore repo.PairingSecretStore,
	certTTL time.Duration,
	secretTTL time.Duration,
	log *slog.Logger,
) *PairingServer {
	core := pairingpkg.NewPairingCore(authority, srvRepo, secretStore, certTTL, secretTTL, log)
	return &PairingServer{
		core:         core,
		bootstrapSvc: pairingpkg.NewBootstrapPairingService(core),
		primarySvc:   pairingpkg.NewPrimaryPairingService(core),
		ca:           authority,
		cfg:          cfg,
		log:          log,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Accessors used by server.go
// ─────────────────────────────────────────────────────────────────────────────

// BootstrapService returns the gRPC service implementation for the bootstrap
// listener (:50052). Register this on the bootstrap gRPC server, not the
// primary mTLS server.
func (s *PairingServer) BootstrapService() *pairingpkg.BootstrapPairingService {
	return s.bootstrapSvc
}

// PrimaryService returns the gRPC service implementation for the primary
// mTLS listener (:50051).
func (s *PairingServer) PrimaryService() *pairingpkg.PrimaryPairingService {
	return s.primarySvc
}

// ─────────────────────────────────────────────────────────────────────────────
// Core delegation — startup / TLS helpers
// ─────────────────────────────────────────────────────────────────────────────

// RebuildDenySet rebuilds the in-memory revocation deny-set from the
// repository. Call once at startup (before accepting connections) and
// periodically thereafter to bound the revocation propagation window.
func (s *PairingServer) RebuildDenySet(ctx context.Context) error {
	return s.core.RebuildDenySet(ctx)
}

// BuildMTLSConfig builds a *tls.Config for the primary mTLS listener (port
// 50051). It requires and verifies client certificates signed by the authority
// CA, and checks each connection against the deny-set via VerifyPeerCertificate.
func (s *PairingServer) BuildMTLSConfig(serverCert tls.Certificate) (*tls.Config, error) {
	return s.core.BuildMTLSConfig(serverCert)
}

// MakeTLSVerifyCallback returns a tls.Config.VerifyPeerCertificate function
// that rejects connections whose certificate serial is in the deny-set.
// Attach this to the primary mTLS listener's tls.Config.
func (s *PairingServer) MakeTLSVerifyCallback() func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
	return s.core.MakeTLSVerifyCallback()
}

// IsCertRevoked reports whether the certificate with the given serial number
// has been revoked. Exposed for callers that hold a *PairingServer reference.
func (s *PairingServer) IsCertRevoked(serial *big.Int) bool {
	return s.core.IsCertRevoked(serial)
}

// ─────────────────────────────────────────────────────────────────────────────
// HTTP-handler RPC delegation — routes to primarySvc
// ─────────────────────────────────────────────────────────────────────────────

// ListServers delegates to the primary mTLS service.
func (s *PairingServer) ListServers(ctx context.Context, req *pb.ListServersRequest) (*pb.ListServersResponse, error) {
	return s.primarySvc.ListServers(ctx, req)
}

// RevokeServer delegates to the primary mTLS service.
func (s *PairingServer) RevokeServer(ctx context.Context, req *pb.RevokeServerRequest) (*pb.RevokeServerResponse, error) {
	return s.primarySvc.RevokeServer(ctx, req)
}

// CreatePairingSecret delegates to the primary mTLS service.
func (s *PairingServer) CreatePairingSecret(ctx context.Context, req *pb.CreatePairingSecretRequest) (*pb.CreatePairingSecretResponse, error) {
	return s.primarySvc.CreatePairingSecret(ctx, req)
}

// GetCACertificate delegates to the primary mTLS service.
func (s *PairingServer) GetCACertificate(ctx context.Context, req *pb.GetCACertificateRequest) (*pb.GetCACertificateResponse, error) {
	return s.primarySvc.GetCACertificate(ctx, req)
}

// ─────────────────────────────────────────────────────────────────────────────
// URN helpers
// ─────────────────────────────────────────────────────────────────────────────

// URN returns this server's own stable URN (notx:srv:<uuid>).
// The URN is resolved once via resolveServerURN and cached for the lifetime
// of the service. It is based on a UUID, not the CA certificate serial.
func (s *PairingServer) URN() string {
	s.urnOnce.Do(func() {
		urn, err := s.resolveServerURN()
		if err != nil {
			// Fall back to an ephemeral UUID so the server can still start.
			if s.log != nil {
				s.log.Warn("could not resolve server URN, using ephemeral URN", "err", err)
			}
			urn = core.NewURN(core.ObjectTypeServer).String()
		}
		s.urn = urn
	})
	return s.urn
}

// resolveServerURN returns a stable, valid notx URN for this server instance.
//
// Format: notx:srv:<uuid>
//
// On first call a new UUID v4 is generated and written to
// <PeerCertDir>/server.urn so that the same identity is presented to the
// authority on every subsequent startup and cert renewal. If PeerCertDir is
// not configured a fresh UUID is generated each time (acceptable for
// single-run scenarios but will create a new server record on every pairing).
func (s *PairingServer) resolveServerURN() (string, error) {
	const urnFile = "server.urn"

	certDir := ""
	if s.cfg != nil {
		certDir = s.cfg.Pairing.PeerCertDir
	}

	if certDir != "" {
		if err := os.MkdirAll(certDir, 0o700); err != nil {
			return "", fmt.Errorf("create cert dir: %w", err)
		}

		urnPath := filepath.Join(certDir, urnFile)

		// Return the persisted URN if it exists and is valid.
		if data, err := os.ReadFile(urnPath); err == nil {
			candidate := strings.TrimSpace(string(data))
			if _, parseErr := core.ParseURN(candidate); parseErr == nil {
				return candidate, nil
			}
			// File exists but is invalid (e.g. old format) — regenerate below.
		}

		// Generate a fresh URN and persist it.
		urn := core.NewURN(core.ObjectTypeServer).String()
		if err := os.WriteFile(urnPath, []byte(urn+"\n"), 0o600); err != nil {
			// Non-fatal: log and continue with the in-memory URN.
			if s.log != nil {
				s.log.Warn("could not persist server URN", "path", urnPath, "err", err)
			}
		}
		return urn, nil
	}

	// No cert dir — generate an ephemeral URN.
	return core.NewURN(core.ObjectTypeServer).String(), nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Outbound pairing — this server registers with a remote authority
// ─────────────────────────────────────────────────────────────────────────────

// RegisterWithPeer dials the remote authority's bootstrap listener, registers
// this server, and returns the issued certificate response.
//
// Either cfg.Pairing.PeerCAFile or cfg.Pairing.PeerCAFingerprint must be set;
// insecure dials are no longer accepted.
//
// The issued cert, key, and CA cert are written to cfg.Pairing.PeerCertDir
// when that field is non-empty.
func (s *PairingServer) RegisterWithPeer(ctx context.Context, authorityAddr, secret string) (*pb.RegisterServerResponse, error) {
	// Strip any http:// or https:// scheme — gRPC targets must be bare host:port.
	if after, ok := strings.CutPrefix(authorityAddr, "https://"); ok {
		authorityAddr = after
	} else if after, ok := strings.CutPrefix(authorityAddr, "http://"); ok {
		authorityAddr = after
	}

	// 1. Resolve a stable server URN.
	serverURN, err := s.resolveServerURN()
	if err != nil {
		return nil, fmt.Errorf("resolve server URN: %w", err)
	}

	// 2. Generate ephemeral key + CSR.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	tmpl := &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: serverURN},
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, tmpl, key)
	if err != nil {
		return nil, fmt.Errorf("build CSR: %w", err)
	}

	// 3. Dial bootstrap with verified TLS.
	// At least one of PeerCAFile or PeerCAFingerprint must be configured.
	var conn *grpcclient.Conn
	if s.cfg != nil && s.cfg.Pairing.PeerCAFile != "" {
		caPool, loadErr := grpcclient.LoadCAPool(s.cfg.Pairing.PeerCAFile)
		if loadErr != nil {
			return nil, fmt.Errorf("load peer CA: %w", loadErr)
		}
		conn, err = grpcclient.DialBootstrap(authorityAddr, caPool)
		if err != nil {
			return nil, fmt.Errorf("dial bootstrap %s: %w", authorityAddr, err)
		}
	} else if s.cfg != nil && s.cfg.Pairing.PeerCAFingerprint != "" {
		conn, err = grpcclient.DialBootstrapWithFingerprint(authorityAddr, s.cfg.Pairing.PeerCAFingerprint)
		if err != nil {
			return nil, fmt.Errorf("dial bootstrap (fingerprint) %s: %w", authorityAddr, err)
		}
	} else {
		// No CA configured — refuse outright rather than silently skip TLS
		// verification.
		return nil, fmt.Errorf(
			"pairing: PeerCAFile or PeerCAFingerprint must be configured before pairing with %s",
			authorityAddr,
		)
	}
	defer conn.Close()

	endpoint := ""
	if s.cfg != nil {
		endpoint = s.cfg.GRPCAddr()
	}

	// Derive a human-readable name from the endpoint so the authority UI can
	// display something meaningful (e.g. "notx-engine :50051").
	serverName := "notx-engine " + endpoint

	// 4. RegisterServer RPC.
	resp, err := conn.Pairing().RegisterServer(ctx, &pb.RegisterServerRequest{
		ServerUrn:     serverURN,
		Csr:           csrDER,
		PairingSecret: secret,
		ServerName:    serverName,
		Endpoint:      endpoint,
	})
	if err != nil {
		return nil, fmt.Errorf("RegisterServer RPC: %w", err)
	}

	// 5. Persist certs if PeerCertDir is configured.
	if s.cfg != nil && s.cfg.Pairing.PeerCertDir != "" {
		if mkErr := os.MkdirAll(s.cfg.Pairing.PeerCertDir, 0o700); mkErr == nil {
			_ = os.WriteFile(filepath.Join(s.cfg.Pairing.PeerCertDir, "server.crt"), resp.Certificate, 0o644)
			_ = os.WriteFile(filepath.Join(s.cfg.Pairing.PeerCertDir, "ca.crt"), resp.CaCertificate, 0o644)
			if keyDER, marshalErr := x509.MarshalECPrivateKey(key); marshalErr == nil {
				keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
				_ = os.WriteFile(filepath.Join(s.cfg.Pairing.PeerCertDir, "server.key"), keyPEM, 0o600)
			}
		}
	}

	s.log.Info("outbound_pair_success",
		"event", "outbound_pair_success",
		"authority", authorityAddr,
		"server_urn", resp.ServerUrn,
		"expires_at", resp.ExpiresAt.AsTime(),
	)

	return resp, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Package-private helpers (kept for internal use)
// ─────────────────────────────────────────────────────────────────────────────

// hostnameFromEndpoint strips the port from an "host:port" endpoint string.
// Returns the original string if it cannot be parsed.
func hostnameFromEndpoint(endpoint string) string {
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil {
		return endpoint
	}
	return host
}
