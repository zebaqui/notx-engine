package pairing

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

	"github.com/zebaqui/notx-engine/ca"
	"github.com/zebaqui/notx-engine/core"
	internalpkg "github.com/zebaqui/notx-engine/internal/pairing"
	pb "github.com/zebaqui/notx-engine/proto"
	"github.com/zebaqui/notx-engine/repo"
)

// ─────────────────────────────────────────────────────────────────────────────
// pairingCore — shared state and business logic
// ─────────────────────────────────────────────────────────────────────────────

// pairingCore holds all shared state and implements the underlying business
// logic for both the bootstrap and primary pairing services.
type pairingCore struct {
	ca          *ca.CA
	srvRepo     repo.ServerRepository
	secretStore repo.PairingSecretStore
	certTTL     time.Duration
	secretTTL   time.Duration
	log         *slog.Logger

	// denySet is the in-memory set of revoked certificate serial numbers
	// (hex-encoded). Rebuilt from the repo on startup; updated on revokeServer.
	denyMu  sync.RWMutex
	denySet map[string]struct{}
}

// NewPairingCore creates a pairingCore ready to be wrapped by
// BootstrapPairingService and PrimaryPairingService.
func NewPairingCore(
	authority *ca.CA,
	srvRepo repo.ServerRepository,
	secretStore repo.PairingSecretStore,
	certTTL time.Duration,
	secretTTL time.Duration,
	log *slog.Logger,
) *pairingCore {
	return &pairingCore{
		ca:          authority,
		srvRepo:     srvRepo,
		secretStore: secretStore,
		certTTL:     certTTL,
		secretTTL:   secretTTL,
		log:         log,
		denySet:     make(map[string]struct{}),
	}
}

// RebuildDenySet rebuilds the in-memory revocation deny-set from the
// repository. Call this once during server startup before accepting
// connections.
func (s *pairingCore) RebuildDenySet(ctx context.Context) error {
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
func (s *pairingCore) IsCertRevoked(serial *big.Int) bool {
	hexSerial := fmt.Sprintf("%x", serial.Bytes())
	s.denyMu.RLock()
	defer s.denyMu.RUnlock()
	_, revoked := s.denySet[hexSerial]
	return revoked
}

// ─────────────────────────────────────────────────────────────────────────────
// Core business-logic methods (unexported — called through service wrappers)
// ─────────────────────────────────────────────────────────────────────────────

func (s *pairingCore) createPairingSecret(ctx context.Context, req *pb.CreatePairingSecretRequest) (*pb.CreatePairingSecretResponse, error) {
	label := req.GetLabel()
	if label == "" {
		label = "admin-generated"
	}

	plaintext, record, err := internalpkg.GenerateSecret(label, s.secretTTL)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "generate pairing secret: %v", err)
	}

	if err := s.secretStore.AddSecret(ctx, record); err != nil {
		return nil, status.Errorf(codes.Internal, "store pairing secret: %v", err)
	}

	s.log.Info("pairing_secret_created",
		"event", "pairing_secret_created",
		"id", record.ID,
		"label", label,
		"expires_at", record.ExpiresAt,
	)

	return &pb.CreatePairingSecretResponse{
		Id:        record.ID,
		Label:     record.LabelHint,
		Plaintext: plaintext,
		ExpiresAt: timestamppb.New(record.ExpiresAt),
	}, nil
}

// namespaceAwareSecretStore is an optional extension of repo.PairingSecretStore
// that also returns the tenant namespace that owns the consumed secret.
// The platform's CrossNamespaceHub implements this interface so that
// registerServer can rewrite the engine-supplied URN to the correct namespace
// before persisting — without changing the base repo interface.
type namespaceAwareSecretStore interface {
	ConsumeSecretWithNamespace(ctx context.Context, plaintext string) (*repo.PairingSecret, string, error)
}

// pairingNamespaceKey is the context key used to pass the tenant namespace
// from registerServer to CrossNamespaceServerRepo.RegisterServer.
type pairingNamespaceKey struct{}

// NamespaceFromContext retrieves the tenant namespace injected by registerServer.
// Returns ("", false) when no namespace is present in the context.
func NamespaceFromContext(ctx context.Context) (string, bool) {
	ns, ok := ctx.Value(pairingNamespaceKey{}).(string)
	return ns, ok && ns != ""
}

func (s *pairingCore) registerServer(ctx context.Context, req *pb.RegisterServerRequest) (*pb.RegisterServerResponse, error) {
	remoteAddr := remoteAddrFromCtx(ctx)

	// Validate and consume the pairing secret. Never log the plaintext.
	// The namespace is extracted from the secret so the server record lands in
	// the correct tenant namespace regardless of the URN format.
	var (
		secret    *repo.PairingSecret
		namespace string
		err       error
	)
	if nsStore, ok := s.secretStore.(namespaceAwareSecretStore); ok {
		secret, namespace, err = nsStore.ConsumeSecretWithNamespace(ctx, req.PairingSecret)
	} else {
		secret, err = s.secretStore.ConsumeSecret(ctx, req.PairingSecret)
	}
	if err != nil {
		s.log.Info("pairing_secret_rejected",
			"event", "pairing_secret_rejected",
			"reason", "wrong_or_expired_or_used",
			"remote_addr", remoteAddr,
		)
		return nil, status.Errorf(codes.Unauthenticated, "invalid or expired pairing secret")
	}

	// Under the new URN scheme, namespace is not part of the URN identity
	// (urn:notx:<type>:<uuidv7>). No rewriting is needed.
	serverURN := req.ServerUrn

	s.log.Info("pairing_secret_consumed",
		"event", "pairing_secret_consumed",
		"server_urn", serverURN,
		"label", secret.LabelHint,
		"remote_addr", remoteAddr,
	)

	// Validate the CSR before signing.
	if err := ca.ValidateCSR(req.Csr, ca.CSRValidationOptions{
		RequiredCN:    serverURN,
		AllowedSANDNS: []string{hostnameFromEndpoint(req.Endpoint)},
	}); err != nil {
		s.log.Info("csr_validation_failed",
			"event", "csr_validation_failed",
			"server_urn", serverURN,
			"remote_addr", remoteAddr,
			"error", err,
		)
		return nil, status.Errorf(codes.InvalidArgument, "CSR validation failed: %v", err)
	}

	// Sign the CSR presented by the registering server.
	certPEM, serialHex, err := s.ca.SignCSR(req.Csr, serverURN, hostnameFromEndpoint(req.Endpoint), s.certTTL)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "sign CSR: %v", err)
	}

	now := time.Now().UTC()
	expiresAt := now.Add(s.certTTL)

	urn, err := core.ParseURN(serverURN)
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

	// Inject the tenant namespace (derived from the consumed secret) into the
	// context so CrossNamespaceServerRepo can route to the correct tenant
	// without having to parse it from the URN (which uses urn:notx:srv: format,
	// not namespace:type:id format).
	if namespace != "" {
		ctx = context.WithValue(ctx, pairingNamespaceKey{}, namespace)
	}

	if err := s.srvRepo.RegisterServer(ctx, sv); err != nil {
		return nil, status.Errorf(codes.Internal, "store server record: %v", err)
	}

	s.log.Info("server_cert_issued",
		"event", "server_cert_issued",
		"server_urn", serverURN,
		"serial", serialHex,
		"expires_at", expiresAt,
	)

	return &pb.RegisterServerResponse{
		ServerUrn:     serverURN,
		Certificate:   certPEM,
		CaCertificate: s.ca.CertPEM,
		ExpiresAt:     timestamppb.New(expiresAt),
		RegisteredAt:  timestamppb.New(now),
	}, nil
}

func (s *pairingCore) renewCertificate(ctx context.Context, req *pb.RenewCertificateRequest) (*pb.RenewCertificateResponse, error) {
	sv, err := s.srvRepo.GetServer(ctx, req.ServerUrn)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "server not found: %v", err)
	}
	if sv.Revoked {
		return nil, status.Errorf(codes.PermissionDenied, "server is revoked")
	}

	// Validate the CSR before signing.
	if err := ca.ValidateCSR(req.Csr, ca.CSRValidationOptions{
		RequiredCN:    req.ServerUrn,
		AllowedSANDNS: []string{hostnameFromEndpoint(sv.Endpoint)},
	}); err != nil {
		s.log.Info("csr_validation_failed",
			"event", "csr_validation_failed",
			"server_urn", req.ServerUrn,
			"error", err,
		)
		return nil, status.Errorf(codes.InvalidArgument, "CSR validation failed: %v", err)
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

func (s *pairingCore) getCACertificate(_ context.Context, _ *pb.GetCACertificateRequest) (*pb.GetCACertificateResponse, error) {
	return &pb.GetCACertificateResponse{CaCertificate: s.ca.CertPEM}, nil
}

func (s *pairingCore) listServers(ctx context.Context, req *pb.ListServersRequest) (*pb.ListServersResponse, error) {
	result, err := s.srvRepo.ListServers(ctx, repo.ServerListOptions{IncludeRevoked: req.IncludeRevoked})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list servers: %v", err)
	}

	infos := make([]*pb.Server, 0, len(result.Servers))
	for _, sv := range result.Servers {
		info := &pb.Server{
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

func (s *pairingCore) revokeServer(ctx context.Context, req *pb.RevokeServerRequest) (*pb.RevokeServerResponse, error) {
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

	return &pb.RevokeServerResponse{ServerUrn: req.ServerUrn, Revoked: true}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// TLS helpers (on pairingCore — called from hub.go)
// ─────────────────────────────────────────────────────────────────────────────

// MakeTLSVerifyCallback returns a tls.Config.VerifyPeerCertificate function
// that rejects connections whose certificate serial is in the deny-set.
// Attach this to the primary mTLS listener's tls.Config.
func (s *pairingCore) MakeTLSVerifyCallback() func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
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
func (s *pairingCore) BuildMTLSConfig(serverCert tls.Certificate) (*tls.Config, error) {
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
// BootstrapPairingService — registered on :50052 (TLS, no client cert)
// ─────────────────────────────────────────────────────────────────────────────

// BootstrapPairingService handles server registration and CA certificate
// distribution on the bootstrap port (:50052). It enforces that
// CreatePairingSecret is only callable from localhost. Operations that require
// mTLS (RenewCertificate, ListServers, RevokeServer) are explicitly blocked.
type BootstrapPairingService struct {
	pb.UnimplementedServerPairingServiceServer
	core *pairingCore
}

// NewBootstrapPairingService wraps a pairingCore for the bootstrap listener.
func NewBootstrapPairingService(core *pairingCore) *BootstrapPairingService {
	return &BootstrapPairingService{core: core}
}

// RegisterServer validates the pairing secret and issues a certificate for the
// registering server. Available on the bootstrap port only.
func (s *BootstrapPairingService) RegisterServer(ctx context.Context, req *pb.RegisterServerRequest) (*pb.RegisterServerResponse, error) {
	return s.core.registerServer(ctx, req)
}

// GetCACertificate returns the platform CA certificate. Available
// unauthenticated on both ports.
func (s *BootstrapPairingService) GetCACertificate(ctx context.Context, req *pb.GetCACertificateRequest) (*pb.GetCACertificateResponse, error) {
	return s.core.getCACertificate(ctx, req)
}

// CreatePairingSecret generates a new one-time pairing secret. On the bootstrap
// port this is restricted to loopback callers only; use the primary mTLS port
// for remote admin access.
func (s *BootstrapPairingService) CreatePairingSecret(ctx context.Context, req *pb.CreatePairingSecretRequest) (*pb.CreatePairingSecretResponse, error) {
	if !isLoopback(remoteAddrFromCtx(ctx)) {
		return nil, status.Errorf(codes.PermissionDenied,
			"CreatePairingSecret is only available via the primary mTLS port")
	}
	return s.core.createPairingSecret(ctx, req)
}

// RenewCertificate is not available on the bootstrap port.
func (s *BootstrapPairingService) RenewCertificate(_ context.Context, _ *pb.RenewCertificateRequest) (*pb.RenewCertificateResponse, error) {
	return nil, status.Errorf(codes.PermissionDenied,
		"RenewCertificate is only available on the primary mTLS port (:50051)")
}

// ListServers is not available on the bootstrap port.
func (s *BootstrapPairingService) ListServers(_ context.Context, _ *pb.ListServersRequest) (*pb.ListServersResponse, error) {
	return nil, status.Errorf(codes.PermissionDenied,
		"ListServers is only available on the primary mTLS port (:50051)")
}

// RevokeServer is not available on the bootstrap port.
func (s *BootstrapPairingService) RevokeServer(_ context.Context, _ *pb.RevokeServerRequest) (*pb.RevokeServerResponse, error) {
	return nil, status.Errorf(codes.PermissionDenied,
		"RevokeServer is only available on the primary mTLS port (:50051)")
}

// ─────────────────────────────────────────────────────────────────────────────
// PrimaryPairingService — registered on :50051 (full mTLS)
// ─────────────────────────────────────────────────────────────────────────────

// PrimaryPairingService handles certificate renewal, server listing, revocation,
// and pairing secret creation on the primary mTLS port (:50051). RegisterServer
// is explicitly blocked here — callers must use the bootstrap port.
type PrimaryPairingService struct {
	pb.UnimplementedServerPairingServiceServer
	core *pairingCore
}

// NewPrimaryPairingService wraps a pairingCore for the primary mTLS listener.
func NewPrimaryPairingService(core *pairingCore) *PrimaryPairingService {
	return &PrimaryPairingService{core: core}
}

// RegisterServer is not available on the primary port. Use the bootstrap port
// (:50052) for initial server registration.
func (s *PrimaryPairingService) RegisterServer(_ context.Context, _ *pb.RegisterServerRequest) (*pb.RegisterServerResponse, error) {
	return nil, status.Errorf(codes.PermissionDenied,
		"RegisterServer is only available on the bootstrap port (:50052)")
}

// RenewCertificate renews the certificate for an already-registered server.
// Requires a valid mTLS client certificate.
func (s *PrimaryPairingService) RenewCertificate(ctx context.Context, req *pb.RenewCertificateRequest) (*pb.RenewCertificateResponse, error) {
	return s.core.renewCertificate(ctx, req)
}

// GetCACertificate returns the platform CA certificate.
func (s *PrimaryPairingService) GetCACertificate(ctx context.Context, req *pb.GetCACertificateRequest) (*pb.GetCACertificateResponse, error) {
	return s.core.getCACertificate(ctx, req)
}

// ListServers returns registered servers. Requires a valid mTLS client
// certificate.
func (s *PrimaryPairingService) ListServers(ctx context.Context, req *pb.ListServersRequest) (*pb.ListServersResponse, error) {
	return s.core.listServers(ctx, req)
}

// RevokeServer revokes a registered server's certificate. Requires a valid
// mTLS client certificate.
func (s *PrimaryPairingService) RevokeServer(ctx context.Context, req *pb.RevokeServerRequest) (*pb.RevokeServerResponse, error) {
	return s.core.revokeServer(ctx, req)
}

// CreatePairingSecret generates a new one-time pairing secret. On the primary
// mTLS port there is no localhost restriction — the mTLS handshake provides
// authentication.
func (s *PrimaryPairingService) CreatePairingSecret(ctx context.Context, req *pb.CreatePairingSecretRequest) (*pb.CreatePairingSecretResponse, error) {
	return s.core.createPairingSecret(ctx, req)
}

// ─────────────────────────────────────────────────────────────────────────────
// Package-level helpers
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
