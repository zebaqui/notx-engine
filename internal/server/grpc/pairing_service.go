package grpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"

	"github.com/zebaqui/notx-engine/core"
	"github.com/zebaqui/notx-engine/internal/ca"
	"github.com/zebaqui/notx-engine/internal/repo"
	pb "github.com/zebaqui/notx-engine/internal/server/proto"
)

// PairingService implements pb.ServerPairingServiceServer.
// It manages server registration, cert issuance, renewal, revocation, and CA
// certificate distribution.
type PairingService struct {
	pb.UnimplementedServerPairingServiceServer

	ca          *ca.CA
	srvRepo     repo.ServerRepository
	secretStore repo.PairingSecretStore
	certTTL     time.Duration
	log         *slog.Logger

	// denySet is the in-memory set of revoked certificate serial numbers
	// (hex-encoded). Rebuilt from the repo on startup; updated on RevokeServer.
	denyMu  sync.RWMutex
	denySet map[string]struct{}
}

// NewPairingService creates a PairingService ready to be registered with a
// grpc.Server.
func NewPairingService(
	authority *ca.CA,
	srvRepo repo.ServerRepository,
	secretStore repo.PairingSecretStore,
	certTTL time.Duration,
	log *slog.Logger,
) *PairingService {
	return &PairingService{
		ca:          authority,
		srvRepo:     srvRepo,
		secretStore: secretStore,
		certTTL:     certTTL,
		log:         log,
		denySet:     make(map[string]struct{}),
	}
}

// RebuildDenySet rebuilds the in-memory revocation deny-set from the
// repository. Call this once during server startup before accepting
// connections.
func (s *PairingService) RebuildDenySet(ctx context.Context) error {
	result, err := s.srvRepo.ListServers(ctx, repo.ServerListOptions{IncludeRevoked: true})
	if err != nil {
		return fmt.Errorf("pairing: rebuild deny set: %w", err)
	}
	s.denyMu.Lock()
	defer s.denyMu.Unlock()
	s.denySet = make(map[string]struct{}, len(result.Servers))
	for _, sv := range result.Servers {
		if sv.Revoked && sv.CertSerial != "" {
			s.denySet[sv.CertSerial] = struct{}{}
		}
	}
	return nil
}

// IsCertRevoked reports whether the certificate with the given serial number
// has been revoked. Called from the TLS VerifyPeerCertificate callback.
func (s *PairingService) IsCertRevoked(serial *big.Int) bool {
	hexSerial := fmt.Sprintf("%x", serial.Bytes())
	s.denyMu.RLock()
	defer s.denyMu.RUnlock()
	_, revoked := s.denySet[hexSerial]
	return revoked
}

// ─────────────────────────────────────────────────────────────────────────────
// RegisterServer — bootstrap listener only (port 50052, no client cert)
// ─────────────────────────────────────────────────────────────────────────────

func (s *PairingService) RegisterServer(ctx context.Context, req *pb.RegisterServerRequest) (*pb.RegisterServerResponse, error) {
	remoteAddr := remoteAddrFromCtx(ctx)

	// Validate and consume the pairing secret. Never log the plaintext.
	secret, err := s.secretStore.ConsumeSecret(ctx, req.PairingSecret)
	if err != nil {
		s.log.Info("pairing_secret_rejected",
			"event", "pairing_secret_rejected",
			"reason", "wrong_or_expired_or_used",
			"remote_addr", remoteAddr,
		)
		return nil, status.Errorf(codes.Unauthenticated, "invalid or expired pairing secret")
	}

	s.log.Info("pairing_secret_consumed",
		"event", "pairing_secret_consumed",
		"server_urn", req.ServerUrn,
		"label", secret.LabelHint,
		"remote_addr", remoteAddr,
	)

	// Sign the CSR presented by the registering server.
	certPEM, serialHex, err := s.ca.SignCSR(req.Csr, req.ServerUrn, hostnameFromEndpoint(req.Endpoint), s.certTTL)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "sign CSR: %v", err)
	}

	now := time.Now().UTC()
	expiresAt := now.Add(s.certTTL)

	urn, err := core.ParseURN(req.ServerUrn)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid server URN: %v", err)
	}

	sv := &core.Server{
		URN:          urn,
		Name:         req.ServerName,
		Endpoint:     req.Endpoint,
		CertPEM:      certPEM,
		CertSerial:   serialHex,
		Revoked:      false,
		RegisteredAt: now,
		ExpiresAt:    expiresAt,
		LastSeenAt:   now,
	}

	if err := s.srvRepo.RegisterServer(ctx, sv); err != nil {
		return nil, status.Errorf(codes.Internal, "store server record: %v", err)
	}

	s.log.Info("server_cert_issued",
		"event", "server_cert_issued",
		"server_urn", req.ServerUrn,
		"serial", serialHex,
		"expires_at", expiresAt,
	)

	return &pb.RegisterServerResponse{
		ServerUrn:     req.ServerUrn,
		Certificate:   certPEM,
		CaCertificate: s.ca.CertPEM,
		ExpiresAt:     timestamppb.New(expiresAt),
		RegisteredAt:  timestamppb.New(now),
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// RenewCertificate — primary listener only (port 50051, mTLS required)
// ─────────────────────────────────────────────────────────────────────────────

func (s *PairingService) RenewCertificate(ctx context.Context, req *pb.RenewCertificateRequest) (*pb.RenewCertificateResponse, error) {
	sv, err := s.srvRepo.GetServer(ctx, req.ServerUrn)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "server not found: %v", err)
	}
	if sv.Revoked {
		return nil, status.Errorf(codes.PermissionDenied, "server is revoked")
	}

	certPEM, serialHex, err := s.ca.SignCSR(req.Csr, req.ServerUrn, hostnameFromEndpoint(sv.Endpoint), s.certTTL)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "sign CSR: %v", err)
	}

	now := time.Now().UTC()
	expiresAt := now.Add(s.certTTL)

	sv.CertPEM = certPEM
	sv.CertSerial = serialHex
	sv.ExpiresAt = expiresAt
	sv.LastSeenAt = now

	if err := s.srvRepo.UpdateServer(ctx, sv); err != nil {
		return nil, status.Errorf(codes.Internal, "update server record: %v", err)
	}

	s.log.Info("server_cert_renewed",
		"event", "server_cert_renewed",
		"server_urn", req.ServerUrn,
		"serial", serialHex,
		"expires_at", expiresAt,
	)

	return &pb.RenewCertificateResponse{
		Certificate: certPEM,
		ExpiresAt:   timestamppb.New(expiresAt),
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// GetCACertificate — unauthenticated, available on both listeners
// ─────────────────────────────────────────────────────────────────────────────

func (s *PairingService) GetCACertificate(_ context.Context, _ *pb.GetCACertificateRequest) (*pb.GetCACertificateResponse, error) {
	return &pb.GetCACertificateResponse{CaCertificate: s.ca.CertPEM}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// ListServers — primary listener only (mTLS)
// ─────────────────────────────────────────────────────────────────────────────

func (s *PairingService) ListServers(ctx context.Context, req *pb.ListServersRequest) (*pb.ListServersResponse, error) {
	result, err := s.srvRepo.ListServers(ctx, repo.ServerListOptions{IncludeRevoked: req.IncludeRevoked})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list servers: %v", err)
	}

	infos := make([]*pb.ServerInfo, 0, len(result.Servers))
	for _, sv := range result.Servers {
		info := &pb.ServerInfo{
			ServerUrn:    sv.URN.String(),
			ServerName:   sv.Name,
			Endpoint:     sv.Endpoint,
			Revoked:      sv.Revoked,
			RegisteredAt: timestamppb.New(sv.RegisteredAt),
			ExpiresAt:    timestamppb.New(sv.ExpiresAt),
		}
		if !sv.LastSeenAt.IsZero() {
			info.LastSeenAt = timestamppb.New(sv.LastSeenAt)
		}
		infos = append(infos, info)
	}

	return &pb.ListServersResponse{Servers: infos}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// RevokeServer — primary listener only (mTLS)
// ─────────────────────────────────────────────────────────────────────────────

func (s *PairingService) RevokeServer(ctx context.Context, req *pb.RevokeServerRequest) (*pb.RevokeServerResponse, error) {
	sv, err := s.srvRepo.GetServer(ctx, req.ServerUrn)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "server not found: %v", err)
	}

	if err := s.srvRepo.RevokeServer(ctx, req.ServerUrn); err != nil {
		return nil, status.Errorf(codes.Internal, "revoke server: %v", err)
	}

	// Immediately add the cert serial to the in-memory deny-set so that any
	// subsequent TLS handshakes from this server are rejected without waiting
	// for a restart.
	if sv.CertSerial != "" {
		s.denyMu.Lock()
		s.denySet[sv.CertSerial] = struct{}{}
		s.denyMu.Unlock()
	}

	s.log.Info("server_revoked",
		"event", "server_revoked",
		"server_urn", req.ServerUrn,
		"serial", sv.CertSerial,
	)

	return &pb.RevokeServerResponse{Revoked: true}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// TLS helpers
// ─────────────────────────────────────────────────────────────────────────────

// MakeTLSVerifyCallback returns a tls.Config.VerifyPeerCertificate function
// that rejects connections whose certificate serial is in the deny-set.
// Attach this to the primary mTLS listener's tls.Config.
func (s *PairingService) MakeTLSVerifyCallback() func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
	return func(_ [][]byte, verifiedChains [][]*x509.Certificate) error {
		for _, chain := range verifiedChains {
			for _, cert := range chain {
				if s.IsCertRevoked(cert.SerialNumber) {
					hexSerial := fmt.Sprintf("%x", cert.SerialNumber.Bytes())
					s.log.Info("server_handshake_rejected",
						"event", "server_handshake_rejected",
						"serial", hexSerial,
					)
					return fmt.Errorf("certificate has been revoked")
				}
			}
		}
		return nil
	}
}

// BuildMTLSConfig builds a *tls.Config for the primary mTLS listener (port
// 50051). It requires and verifies client certificates signed by the authority
// CA, and checks each connection against the deny-set via VerifyPeerCertificate.
func (s *PairingService) BuildMTLSConfig(serverCert tls.Certificate) (*tls.Config, error) {
	pool := x509.NewCertPool()
	pool.AddCert(s.ca.Cert)

	return &tls.Config{
		Certificates:          []tls.Certificate{serverCert},
		ClientAuth:            tls.RequireAndVerifyClientCert,
		ClientCAs:             pool,
		VerifyPeerCertificate: s.MakeTLSVerifyCallback(),
		MinVersion:            tls.VersionTLS13,
	}, nil
}

// BuildBootstrapTLSConfig builds a *tls.Config for the bootstrap listener
// (port 50052). TLS is required for confidentiality but no client certificate
// is demanded — the pairing secret authenticates the caller instead.
func BuildBootstrapTLSConfig(serverCert tls.Certificate) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.NoClientCert,
		MinVersion:   tls.VersionTLS13,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Package-private helpers
// ─────────────────────────────────────────────────────────────────────────────

// remoteAddrFromCtx extracts the remote address string from a gRPC context.
func remoteAddrFromCtx(ctx context.Context) string {
	if p, ok := peer.FromContext(ctx); ok {
		return p.Addr.String()
	}
	return "unknown"
}

// hostnameFromEndpoint strips the port from an "host:port" endpoint string.
// Returns the original string if it cannot be parsed.
func hostnameFromEndpoint(endpoint string) string {
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil {
		return endpoint
	}
	return host
}

// parseCertSerial extracts the hex serial number from a PEM-encoded certificate.
// Exported for use in tests and administrative tooling.
func parseCertSerial(certPEM []byte) (string, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return "", fmt.Errorf("no PEM block found")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("parse certificate: %w", err)
	}
	return fmt.Sprintf("%x", cert.SerialNumber.Bytes()), nil
}
