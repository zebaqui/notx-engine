// Package grpcclient provides a central factory for creating gRPC client
// connections to notx services.
//
// All TLS / mTLS / insecure credential logic lives here. Callers obtain a
// *Conn (which wraps a *grpc.ClientConn and implements io.Closer) and then
// call the typed accessor methods to get service clients:
//
//	conn, err := grpcclient.Dial(grpcclient.Options{
//	    Addr:     "localhost:50051",
//	    Insecure: true,
//	})
//	if err != nil { ... }
//	defer conn.Close()
//
//	notes := conn.Notes()
//	resp, err := notes.ListNotes(ctx, &pb.ListNotesRequest{})
//
// For the common case of dialling from the CLI config, use DialFromConfig:
//
//	conn, err := grpcclient.DialFromConfig(cfg)
//
// For the mTLS bootstrap/primary pairing ports, use DialBootstrap /
// DialMTLS instead:
//
//	conn, err := grpcclient.DialBootstrap("authority.example.com:50052", caPool)
//	conn, err := grpcclient.DialMTLS("authority.example.com:50051", clientCert, caPool)
package grpcclient

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	"github.com/zebaqui/notx-engine/internal/clientconfig"
	pb "github.com/zebaqui/notx-engine/proto"
)

// defaultDialTimeout is the maximum time allowed to establish a connection
// before Dial returns an error.
const defaultDialTimeout = 10 * time.Second

// keepaliveParams are applied to every outgoing connection so idle
// connections are recycled before NAT/firewall state expires.
var keepaliveParams = keepalive.ClientParameters{
	// Send a ping after this much idle time.
	Time: 30 * time.Second,
	// Wait this long for a ping ack before declaring the connection dead.
	Timeout: 10 * time.Second,
	// Allow pings even when there are no active RPCs.
	PermitWithoutStream: true,
}

// ─────────────────────────────────────────────────────────────────────────────
// Options
// ─────────────────────────────────────────────────────────────────────────────

// Options controls how Dial establishes a connection.
type Options struct {
	// Addr is the host:port of the gRPC server to connect to. Required.
	Addr string

	// Insecure disables transport security entirely. Suitable for local
	// development only. Mutually exclusive with CertFile/KeyFile/CAFile.
	Insecure bool

	// CertFile is the path to a PEM-encoded client certificate. When set
	// together with KeyFile, the client presents a certificate (mTLS).
	CertFile string

	// KeyFile is the path to the PEM-encoded private key for CertFile.
	KeyFile string

	// CAFile is the path to a PEM-encoded CA certificate. When set, the
	// server certificate is verified against this CA instead of the system
	// pool. Required for self-signed / private CA deployments.
	CAFile string

	// ServerName overrides the expected server name in the TLS handshake.
	// Useful when connecting via IP or when the cert CN does not match the
	// dial address.
	ServerName string

	// SkipVerify disables server certificate verification. Never use in
	// production. Provided for integration tests against local listeners
	// with self-signed certificates.
	SkipVerify bool

	// DialTimeout overrides the default 10-second connection timeout.
	// Zero means use defaultDialTimeout.
	DialTimeout time.Duration

	// ExtraDialOpts are appended to the grpc.DialOption slice after all
	// options derived from the fields above. Use sparingly.
	ExtraDialOpts []grpc.DialOption
}

// ─────────────────────────────────────────────────────────────────────────────
// Conn — the central handle
// ─────────────────────────────────────────────────────────────────────────────

// Conn wraps a *grpc.ClientConn and exposes typed accessors for each service
// defined in notx.proto.
//
// Conn is not safe for concurrent calls to Close — close it exactly once,
// typically via defer, after all RPCs on this connection have finished.
type Conn struct {
	cc *grpc.ClientConn
}

// WrapConn wraps an existing *grpc.ClientConn in a typed Conn. This is useful
// in tests where the caller dials manually (e.g. with insecure credentials via
// grpc.NewClient) and wants access to the typed service accessors without going
// through the Dial helper.
//
// The caller retains ownership of cc and must still call Conn.Close() (which
// delegates to cc.Close()) when done.
func WrapConn(cc *grpc.ClientConn) *Conn {
	return &Conn{cc: cc}
}

// Close tears down the underlying connection and releases all resources.
// It implements io.Closer.
func (c *Conn) Close() error {
	return c.cc.Close()
}

// Raw returns the underlying *grpc.ClientConn for callers that need to
// register their own service clients directly (e.g. third-party protos).
// Prefer the typed accessors below whenever possible.
func (c *Conn) Raw() *grpc.ClientConn {
	return c.cc
}

// ── Typed service accessors ───────────────────────────────────────────────────

// Notes returns a client for NoteService.
func (c *Conn) Notes() pb.NoteServiceClient {
	return pb.NewNoteServiceClient(c.cc)
}

// Devices returns a client for DeviceService.
func (c *Conn) Devices() pb.DeviceServiceClient {
	return pb.NewDeviceServiceClient(c.cc)
}

// Projects returns a client for ProjectService.
func (c *Conn) Projects() pb.ProjectServiceClient {
	return pb.NewProjectServiceClient(c.cc)
}

// Folders returns a client for FolderService.
func (c *Conn) Folders() pb.FolderServiceClient {
	return pb.NewFolderServiceClient(c.cc)
}

// Context returns a client for ContextService.
func (c *Conn) Context() pb.ContextServiceClient {
	return pb.NewContextServiceClient(c.cc)
}

// Pairing returns a client for ServerPairingService.
func (c *Conn) Pairing() pb.ServerPairingServiceClient {
	return pb.NewServerPairingServiceClient(c.cc)
}

// Links returns a client for LinkService.
func (c *Conn) Links() pb.LinkServiceClient {
	return pb.NewLinkServiceClient(c.cc)
}

// Sync returns a client for SyncService.
func (c *Conn) Sync() pb.SyncServiceClient {
	return pb.NewSyncServiceClient(c.cc)
}

// ─────────────────────────────────────────────────────────────────────────────
// Dial — primary entry point
// ─────────────────────────────────────────────────────────────────────────────

// Dial establishes a gRPC connection using the supplied Options and returns a
// *Conn ready to use. The caller is responsible for calling Close when done.
//
// Credential resolution order:
//  1. opts.Insecure == true                → plaintext (no TLS)
//  2. opts.CertFile + opts.KeyFile set     → mTLS client cert presented
//  3. opts.CAFile set (no client cert)     → TLS with custom CA
//  4. opts.SkipVerify == true              → TLS, server cert NOT verified
//  5. otherwise                            → TLS with system root CAs
func Dial(opts Options) (*Conn, error) {
	if opts.Addr == "" {
		return nil, fmt.Errorf("grpcclient: Addr must not be empty")
	}

	creds, err := buildCredentials(opts)
	if err != nil {
		return nil, fmt.Errorf("grpcclient: build credentials: %w", err)
	}

	timeout := opts.DialTimeout
	if timeout == 0 {
		timeout = defaultDialTimeout
	}

	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(creds),
		grpc.WithKeepaliveParams(keepaliveParams),
		// Block until the connection is ready (up to timeout). This turns
		// dialling errors into immediate failures rather than silent no-ops.
		grpc.WithBlock(),
	}
	dialOpts = append(dialOpts, opts.ExtraDialOpts...)

	// grpc.NewClient does not honour WithBlock/timeout natively; we use
	// grpc.Dial (deprecated but still the correct hook for WithBlock).
	// TODO: migrate to grpc.NewClient + manual health-check once the gRPC-Go
	//       v2 API stabilises.
	//nolint:staticcheck // grpc.Dial is the only way to use WithBlock today
	cc, err := grpc.Dial(
		opts.Addr,
		append(dialOpts,
			grpc.WithTimeout(timeout), //nolint:staticcheck
		)...,
	)
	if err != nil {
		return nil, fmt.Errorf("grpcclient: dial %s: %w", opts.Addr, err)
	}

	return &Conn{cc: cc}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Convenience constructors
// ─────────────────────────────────────────────────────────────────────────────

// DialFromConfig creates a connection using the client section of a
// clientconfig.Config. This is the normal path for CLI commands.
//
// The returned connection is lazy (non-blocking) — the underlying transport is
// established on the first RPC. Use Dial with a DialTimeout if you need to
// verify connectivity eagerly at startup.
//
//	cfg, _ := clientconfig.Load()
//	conn, err := grpcclient.DialFromConfig(cfg)
func DialFromConfig(cfg *clientconfig.Config) (*Conn, error) {
	if cfg.Client.GRPCAddr == "" {
		return nil, fmt.Errorf("grpcclient: client.grpc_addr must not be empty")
	}

	creds, err := buildCredentials(Options{
		Addr:     cfg.Client.GRPCAddr,
		Insecure: cfg.Client.Insecure && !cfg.TLSEnabled(),
		CertFile: cfg.TLS.CertFile,
		KeyFile:  cfg.TLS.KeyFile,
		CAFile:   cfg.TLS.CAFile,
	})
	if err != nil {
		return nil, fmt.Errorf("grpcclient: build credentials: %w", err)
	}

	cc, err := grpc.NewClient(cfg.Client.GRPCAddr,
		grpc.WithTransportCredentials(creds),
		grpc.WithKeepaliveParams(keepaliveParams),
	)
	if err != nil {
		return nil, fmt.Errorf("grpcclient: dial %s: %w", cfg.Client.GRPCAddr, err)
	}
	return &Conn{cc: cc}, nil
}

// DialInsecure creates a plaintext (no TLS) connection to addr. Suitable for
// local development and integration tests.
//
// The connection is lazy (non-blocking) — transport is established on the
// first RPC call.
func DialInsecure(addr string) (*Conn, error) {
	if addr == "" {
		return nil, fmt.Errorf("grpcclient: Addr must not be empty")
	}
	cc, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithKeepaliveParams(keepaliveParams),
	)
	if err != nil {
		return nil, fmt.Errorf("grpcclient: dial %s: %w", addr, err)
	}
	return &Conn{cc: cc}, nil
}

// DialTLS creates a TLS connection to addr, verifying the server certificate
// against the system root CA pool. Use this when the server has a certificate
// signed by a public CA.
func DialTLS(addr string) (*Conn, error) {
	return Dial(Options{Addr: addr})
}

// DialWithCA creates a TLS connection to addr, verifying the server
// certificate against the provided CA pool (e.g. a private / self-signed CA).
func DialWithCA(addr string, caPool *x509.CertPool) (*Conn, error) {
	if caPool == nil {
		return nil, fmt.Errorf("grpcclient: caPool must not be nil")
	}
	tlsCfg := &tls.Config{
		RootCAs:    caPool,
		MinVersion: tls.VersionTLS13,
	}
	cc, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)),
		grpc.WithKeepaliveParams(keepaliveParams),
	)
	if err != nil {
		return nil, fmt.Errorf("grpcclient: dial %s: %w", addr, err)
	}
	return &Conn{cc: cc}, nil
}

// DialBootstrap creates a verified TLS connection to the pairing bootstrap
// listener (default port 50052). The server certificate is verified against
// caPool when provided, or against the system root CA pool when caPool is nil.
//
// InsecureSkipVerify is never set. Use DialBootstrapWithFingerprint when only
// a SHA-256 fingerprint of the authority CA is known.
//
// This is the correct dial function for the RegisterServer RPC after a CA pool
// has been established (e.g. via DialBootstrapWithFingerprint phase 2).
func DialBootstrap(addr string, caPool *x509.CertPool) (*Conn, error) {
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS13,
	}
	if caPool != nil {
		tlsCfg.RootCAs = caPool
	}
	// When caPool is nil, system roots are used (tlsCfg.RootCAs == nil means system roots in Go).
	cc, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)),
		grpc.WithKeepaliveParams(keepaliveParams),
	)
	if err != nil {
		return nil, fmt.Errorf("grpcclient: dial bootstrap %s: %w", addr, err)
	}
	return &Conn{cc: cc}, nil
}

// DialBootstrapWithFingerprint dials the bootstrap gRPC listener at addr using
// a two-phase TOFU (Trust On First Use) approach:
//
//  1. Dial insecurely and call GetCACertificate to retrieve the authority's CA cert.
//  2. Verify the CA cert's SHA-256 fingerprint matches expectedFingerprint.
//  3. Re-dial with proper TLS verification using the pinned CA cert as the root.
//
// This handles self-signed CA certificates that are not in the system trust store.
// expectedFingerprint must be uppercase colon-separated hex (e.g. "AA:BB:CC:...").
func DialBootstrapWithFingerprint(addr, expectedFingerprint string) (*Conn, error) {
	if expectedFingerprint == "" {
		return nil, fmt.Errorf("grpcclient: expectedFingerprint must not be empty")
	}
	// Normalise: uppercase, trim spaces.
	expectedFingerprint = strings.TrimSpace(strings.ToUpper(expectedFingerprint))

	// ── Phase 1: fetch CA cert over a TLS connection with verification skipped.
	// We skip verification here ONLY to retrieve the CA cert; the cert is then
	// fingerprint-verified before being trusted as a root for the real dial.
	insecureConn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
			InsecureSkipVerify: true, //nolint:gosec // fingerprint verified below
			MinVersion:         tls.VersionTLS13,
		})),
		grpc.WithKeepaliveParams(keepaliveParams),
	)
	if err != nil {
		return nil, fmt.Errorf("grpcclient: dial bootstrap (insecure phase) %s: %w", addr, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultDialTimeout)
	defer cancel()

	pairingClient := pb.NewServerPairingServiceClient(insecureConn)
	caResp, err := pairingClient.GetCACertificate(ctx, &pb.GetCACertificateRequest{})
	insecureConn.Close()
	if err != nil {
		return nil, fmt.Errorf("grpcclient: fetch CA cert from %s: %w", addr, err)
	}
	if len(caResp.CaCertificate) == 0 {
		return nil, fmt.Errorf("grpcclient: empty CA certificate from %s", addr)
	}

	// ── Phase 2: verify the CA cert fingerprint ───────────────────────────────
	block, _ := pem.Decode(caResp.CaCertificate)
	if block == nil {
		return nil, fmt.Errorf("grpcclient: CA cert from %s is not valid PEM", addr)
	}
	caCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("grpcclient: parse CA cert from %s: %w", addr, err)
	}

	sum := sha256.Sum256(caCert.Raw)
	pairs := make([]string, len(sum))
	for i, b := range sum {
		pairs[i] = fmt.Sprintf("%02X", b)
	}
	actualFP := strings.Join(pairs, ":")
	if actualFP != expectedFingerprint {
		return nil, fmt.Errorf("grpcclient: CA fingerprint mismatch: got %s, expected %s", actualFP, expectedFingerprint)
	}

	// ── Phase 3: re-dial with the verified CA cert as trust root ──────────────
	caPool := x509.NewCertPool()
	caPool.AddCert(caCert)
	return DialBootstrap(addr, caPool)
}

// DialMTLS creates an mTLS connection to the pairing primary listener
// (default port 50051). Both the server and the client present certificates
// signed by the authority CA. This is the correct dial function for
// RenewCertificate, ListServers, and RevokeServer RPCs.
func DialMTLS(addr string, clientCert tls.Certificate, caPool *x509.CertPool) (*Conn, error) {
	if caPool == nil {
		return nil, fmt.Errorf("grpcclient: caPool must not be nil for mTLS")
	}
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      caPool,
		MinVersion:   tls.VersionTLS13,
	}
	cc, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)),
		grpc.WithKeepaliveParams(keepaliveParams),
	)
	if err != nil {
		return nil, fmt.Errorf("grpcclient: dial mTLS %s: %w", addr, err)
	}
	return &Conn{cc: cc}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// CA pool helpers
// ─────────────────────────────────────────────────────────────────────────────

// LoadCAPool reads a PEM-encoded CA certificate from path and returns an
// *x509.CertPool containing it. Useful when building dial options from paths
// stored in config or on disk.
func LoadCAPool(path string) (*x509.CertPool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("grpcclient: read CA file %q: %w", path, err)
	}
	return ParseCAPool(data)
}

// ParseCAPool parses PEM-encoded CA certificate bytes and returns an
// *x509.CertPool. Useful when the CA cert arrives over the wire (e.g. in a
// RegisterServerResponse) rather than from disk.
func ParseCAPool(pemBytes []byte) (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		return nil, fmt.Errorf("grpcclient: no valid PEM certificates found")
	}
	return pool, nil
}

// LoadClientCert loads a PEM-encoded certificate/key pair from disk and
// returns a tls.Certificate. Convenience wrapper around tls.LoadX509KeyPair
// with a consistent error message.
func LoadClientCert(certFile, keyFile string) (tls.Certificate, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf(
			"grpcclient: load client cert/key (%q, %q): %w", certFile, keyFile, err,
		)
	}
	return cert, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Internal helpers
// ─────────────────────────────────────────────────────────────────────────────

// buildCredentials resolves the correct TransportCredentials for opts.
func buildCredentials(opts Options) (credentials.TransportCredentials, error) {
	// 1. Plaintext
	if opts.Insecure {
		return insecure.NewCredentials(), nil
	}

	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS13,
	}

	// 2. Server name override
	if opts.ServerName != "" {
		tlsCfg.ServerName = opts.ServerName
	}

	// 3. Skip server certificate verification (test / bootstrap only)
	if opts.SkipVerify {
		tlsCfg.InsecureSkipVerify = true //nolint:gosec // caller's explicit request
	}

	// 4. Custom CA pool
	if opts.CAFile != "" {
		pool, err := LoadCAPool(opts.CAFile)
		if err != nil {
			return nil, err
		}
		tlsCfg.RootCAs = pool
	}

	// 5. mTLS client certificate
	if opts.CertFile != "" && opts.KeyFile != "" {
		cert, err := LoadClientCert(opts.CertFile, opts.KeyFile)
		if err != nil {
			return nil, err
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	} else if opts.CertFile != "" || opts.KeyFile != "" {
		return nil, fmt.Errorf(
			"grpcclient: CertFile and KeyFile must both be set or both be empty",
		)
	}

	return credentials.NewTLS(tlsCfg), nil
}
