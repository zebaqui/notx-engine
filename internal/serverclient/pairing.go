// Package serverclient implements the joining-server side of the server
// pairing protocol. It handles initial registration, cert persistence,
// and automatic renewal.
package serverclient

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
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/zebaqui/notx-engine/internal/grpcclient"
	"github.com/zebaqui/notx-engine/config"
	pb "github.com/zebaqui/notx-engine/internal/server/proto"
)

const (
	keyFile  = "server.key"
	certFile = "server.crt"
	caFile   = "ca.crt"
)

// PairingClient manages the joining-server lifecycle:
// initial registration and background cert renewal.
type PairingClient struct {
	cfg       *config.Config
	log       *slog.Logger
	serverURN string
}

// NewPairingClient creates a PairingClient.
func NewPairingClient(cfg *config.Config, serverURN string, log *slog.Logger) *PairingClient {
	return &PairingClient{cfg: cfg, log: log, serverURN: serverURN}
}

// EnsurePaired checks for an existing cert; if absent or expired it performs
// RegisterServer against the authority's bootstrap port (50052).
// On success the cert and CA cert are written to PeerCertDir.
func (c *PairingClient) EnsurePaired(ctx context.Context) error {
	certDir := c.cfg.Pairing.PeerCertDir
	if certDir == "" {
		return fmt.Errorf("serverclient: peer-cert-dir is required")
	}
	if err := os.MkdirAll(certDir, 0o700); err != nil {
		return fmt.Errorf("serverclient: create cert dir: %w", err)
	}

	certPath := filepath.Join(certDir, certFile)

	// Check for an existing valid cert.
	if existing, err := loadCert(certPath); err == nil {
		remaining := time.Until(existing.Leaf.NotAfter)
		if remaining > c.cfg.Pairing.RenewalThreshold {
			c.log.Info("serverclient: existing cert is valid, skipping registration",
				"expires_in", remaining.Round(time.Hour).String(),
			)
			return nil
		}
		c.log.Info("serverclient: cert near expiry, will re-register")
	}

	// No valid cert — register.
	if c.cfg.Pairing.PeerSecret == "" {
		return fmt.Errorf("serverclient: peer-secret is required for initial registration")
	}

	return c.register(ctx, certDir)
}

// register performs the RegisterServer RPC against the authority's bootstrap port.
func (c *PairingClient) register(ctx context.Context, certDir string) error {
	authority := c.cfg.Pairing.PeerAuthority
	if authority == "" {
		return fmt.Errorf("serverclient: peer-authority is required")
	}

	keyPath := filepath.Join(certDir, keyFile)
	caPath := filepath.Join(certDir, caFile)

	// Generate or load our EC P-256 key.
	key, err := loadOrGenerateKey(keyPath)
	if err != nil {
		return fmt.Errorf("serverclient: load/generate key: %w", err)
	}

	// Build the CSR.
	csrDER, err := buildCSR(key, c.serverURN)
	if err != nil {
		return fmt.Errorf("serverclient: build CSR: %w", err)
	}

	// Dial the bootstrap port. Use the pinned CA if available, otherwise
	// accept the server cert provisionally on first contact (DialBootstrap
	// sets InsecureSkipVerify when caPool is nil).
	var caPool *x509.CertPool
	if _, err := os.Stat(caPath); err == nil {
		caPool, err = grpcclient.LoadCAPool(caPath)
		if err != nil {
			return fmt.Errorf("serverclient: load pinned CA: %w", err)
		}
	}

	conn, err := grpcclient.DialBootstrap(authority, caPool)
	if err != nil {
		return fmt.Errorf("serverclient: dial authority: %w", err)
	}
	defer conn.Close()

	client := conn.Pairing()

	endpoint := c.cfg.GRPCAddr()
	resp, err := client.RegisterServer(ctx, &pb.RegisterServerRequest{
		ServerUrn:     c.serverURN,
		Csr:           csrDER,
		PairingSecret: c.cfg.Pairing.PeerSecret,
		ServerName:    "notx-" + c.serverURN,
		Endpoint:      endpoint,
	})
	if err != nil {
		return fmt.Errorf("serverclient: RegisterServer: %w", err)
	}

	// Write cert.
	certPath := filepath.Join(certDir, certFile)
	if err := writeFile(certPath, resp.Certificate, 0o644); err != nil {
		return fmt.Errorf("serverclient: write cert: %w", err)
	}
	// Write CA cert.
	if err := writeFile(caPath, resp.CaCertificate, 0o644); err != nil {
		return fmt.Errorf("serverclient: write CA cert: %w", err)
	}

	// Zero the pairing secret from memory.
	c.cfg.Pairing.PeerSecret = strings.Repeat("\x00", len(c.cfg.Pairing.PeerSecret))
	c.cfg.Pairing.PeerSecret = ""
	runtime.GC()

	c.log.Info("serverclient: registration complete",
		"server_urn", c.serverURN,
		"expires_at", resp.ExpiresAt.AsTime(),
	)
	return nil
}

// StartRenewalLoop starts a background goroutine that periodically checks
// cert expiry and renews when within RenewalThreshold.
// The goroutine stops when ctx is cancelled.
func (c *PairingClient) StartRenewalLoop(ctx context.Context, authorityPrimaryAddr string) {
	go c.renewalLoop(ctx, authorityPrimaryAddr)
}

func (c *PairingClient) renewalLoop(ctx context.Context, authorityPrimaryAddr string) {
	ticker := time.NewTicker(c.cfg.Pairing.RenewalCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.maybeRenew(ctx, authorityPrimaryAddr); err != nil {
				c.log.Error("cert_renewal_failed",
					"event", "cert_renewal_failed",
					"server_urn", c.serverURN,
					"error", err,
				)
			}
		}
	}
}

func (c *PairingClient) maybeRenew(ctx context.Context, authorityPrimaryAddr string) error {
	certDir := c.cfg.Pairing.PeerCertDir
	certPath := filepath.Join(certDir, certFile)

	cert, err := loadCert(certPath)
	if err != nil {
		return fmt.Errorf("load cert: %w", err)
	}

	remaining := time.Until(cert.Leaf.NotAfter)
	if remaining > c.cfg.Pairing.RenewalThreshold {
		return nil // not yet
	}

	c.log.Info("cert_renewal_triggered",
		"event", "cert_renewal_triggered",
		"server_urn", c.serverURN,
		"days_remaining", int(remaining.Hours()/24),
	)

	return c.renew(ctx, authorityPrimaryAddr, certDir)
}

func (c *PairingClient) renew(ctx context.Context, authorityPrimaryAddr string, certDir string) error {
	keyPath := filepath.Join(certDir, keyFile)
	certPath := filepath.Join(certDir, certFile)
	caPath := filepath.Join(certDir, caFile)

	// Generate new key for rotation.
	key, err := generateKey(keyPath)
	if err != nil {
		return fmt.Errorf("generate renewal key: %w", err)
	}

	csrDER, err := buildCSR(key, c.serverURN)
	if err != nil {
		return fmt.Errorf("build renewal CSR: %w", err)
	}

	// Dial the primary port with mTLS — present our current cert.
	caPool, err := grpcclient.LoadCAPool(caPath)
	if err != nil {
		return fmt.Errorf("load CA pool: %w", err)
	}
	clientCert, err := grpcclient.LoadClientCert(certPath, keyPath)
	if err != nil {
		return fmt.Errorf("load client cert/key: %w", err)
	}

	conn, err := grpcclient.DialMTLS(authorityPrimaryAddr, clientCert, caPool)
	if err != nil {
		return fmt.Errorf("dial authority (mTLS): %w", err)
	}
	defer conn.Close()

	client := conn.Pairing()
	resp, err := client.RenewCertificate(ctx, &pb.RenewCertificateRequest{
		ServerUrn: c.serverURN,
		Csr:       csrDER,
	})
	if err != nil {
		return fmt.Errorf("RenewCertificate RPC: %w", err)
	}

	// Atomic cert write: write to .tmp then rename.
	tmpPath := certPath + ".tmp"
	if err := writeFile(tmpPath, resp.Certificate, 0o644); err != nil {
		return fmt.Errorf("write cert tmp: %w", err)
	}
	if err := os.Rename(tmpPath, certPath); err != nil {
		return fmt.Errorf("rename cert: %w", err)
	}

	c.log.Info("cert_renewal_success",
		"event", "cert_renewal_success",
		"server_urn", c.serverURN,
		"new_expires_at", resp.ExpiresAt.AsTime(),
	)
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

func loadOrGenerateKey(path string) (*ecdsa.PrivateKey, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return generateKey(path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("no PEM block in %s", path)
	}
	return x509.ParseECPrivateKey(block.Bytes)
}

func generateKey(path string) (*ecdsa.PrivateKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	pemData := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	if err := writeFile(path, pemData, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

func buildCSR(key *ecdsa.PrivateKey, cn string) ([]byte, error) {
	tmpl := &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: cn},
	}
	return x509.CreateCertificateRequest(rand.Reader, tmpl, key)
}

func loadCert(path string) (*tls.Certificate, error) {
	// We need to load and parse the cert to get Leaf populated.
	certPEM, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Find the key next to the cert.
	keyPath := filepath.Join(filepath.Dir(path), keyFile)
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	// Parse leaf to get NotAfter.
	block, _ := pem.Decode(certPEM)
	if block != nil {
		leaf, err := x509.ParseCertificate(block.Bytes)
		if err == nil {
			cert.Leaf = leaf
		}
	}
	return &cert, nil
}

func writeFile(path string, data []byte, mode os.FileMode) error {
	return os.WriteFile(path, data, mode)
}
