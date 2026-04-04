package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	googlegrpc "google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/reflection"

	"github.com/zebaqui/notx-engine/ca"
	"github.com/zebaqui/notx-engine/config"
	"github.com/zebaqui/notx-engine/core"
	httpsvc "github.com/zebaqui/notx-engine/http"
	"github.com/zebaqui/notx-engine/internal/relay"
	grpcsvc "github.com/zebaqui/notx-engine/internal/server/grpc"
	pb "github.com/zebaqui/notx-engine/proto"
	"github.com/zebaqui/notx-engine/repo"
)

// Server is the top-level orchestrator. It owns the lifecycle of the HTTP
// and gRPC servers and coordinates graceful shutdown when a signal arrives.
type Server struct {
	cfg         *config.Config
	repo        repo.NoteRepository
	projRepo    repo.ProjectRepository
	devRepo     repo.DeviceRepository
	userRepo    repo.UserRepository
	srvRepo     repo.ServerRepository
	secretStore repo.PairingSecretStore
	log         *slog.Logger

	httpHandler     *httpsvc.Handler
	grpcServer      *googlegrpc.Server
	pairingService  *grpcsvc.PairingServer
	bootstrapServer *googlegrpc.Server
}

// New creates a Server from the given config and repository.
// It wires all sub-components but does not start any listeners yet.
func New(cfg *config.Config, r repo.NoteRepository, projRepo repo.ProjectRepository, devRepo repo.DeviceRepository, userRepo repo.UserRepository, srvRepo repo.ServerRepository, secretStore repo.PairingSecretStore, log *slog.Logger) (*Server, error) {
	if log == nil {
		log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}

	s := &Server{
		cfg:         cfg,
		repo:        r,
		projRepo:    projRepo,
		devRepo:     devRepo,
		userRepo:    userRepo,
		srvRepo:     srvRepo,
		secretStore: secretStore,
		log:         log,
	}

	// ── Bootstrap the built-in admin device ─────────────────────────────────
	if err := bootstrapAdminDevice(cfg, devRepo, log); err != nil {
		return nil, fmt.Errorf("server: bootstrap admin device: %w", err)
	}

	// ── Build relay policy from config ───────────────────────────────────────
	relayPolicy := relay.Policy{
		AllowedHosts:         cfg.Relay.AllowedHosts,
		AllowLocalhost:       cfg.Relay.AllowLocalhost,
		MaxSteps:             cfg.Relay.MaxSteps,
		MaxRequestBodyBytes:  cfg.Relay.MaxRequestBodyBytes,
		MaxResponseBodyBytes: cfg.Relay.MaxResponseBodyBytes,
		RequestTimeoutSecs:   cfg.Relay.RequestTimeoutSecs,
		MaxRedirects:         cfg.Relay.MaxRedirects,
	}
	// Apply defaults for any zero values (e.g. when Relay section is absent).
	if relayPolicy.MaxSteps == 0 {
		relayPolicy.MaxSteps = 20
	}
	if relayPolicy.MaxRequestBodyBytes == 0 {
		relayPolicy.MaxRequestBodyBytes = 1 << 20
	}
	if relayPolicy.MaxResponseBodyBytes == 0 {
		relayPolicy.MaxResponseBodyBytes = 4 << 20
	}
	if relayPolicy.RequestTimeoutSecs == 0 {
		relayPolicy.RequestTimeoutSecs = 10
	}
	if relayPolicy.MaxRedirects == 0 {
		relayPolicy.MaxRedirects = 5
	}

	relaySvc := grpcsvc.NewRelayServer(devRepo, relayPolicy, log, nil)

	// ── Build gRPC service instances (shared by both HTTP and gRPC layers) ───
	noteSvc := grpcsvc.NewNoteServer(r, cfg.DefaultPageSize, cfg.MaxPageSize)
	projSvc := grpcsvc.NewProjectServer(projRepo, cfg.DefaultPageSize, cfg.MaxPageSize)
	folderSvc := grpcsvc.NewFolderServer(projRepo, cfg.DefaultPageSize, cfg.MaxPageSize)
	deviceSvc := grpcsvc.NewDeviceServer(devRepo)
	deviceAdminSvc := grpcsvc.NewDeviceAdminServer(devRepo)
	userSvc := grpcsvc.NewUserServer(userRepo, cfg.DefaultPageSize, cfg.MaxPageSize)

	// ── Build pairing service before HTTP handler (HTTP handler needs it) ────
	if cfg.Pairing.Enabled {
		authority, err := ca.LoadOrGenerate(cfg.CADir())
		if err != nil {
			return nil, fmt.Errorf("server: load/generate CA: %w", err)
		}
		log.Info("authority CA ready", "ca_dir", cfg.CADir())

		pairingSvc := grpcsvc.NewPairingServer(authority, cfg, srvRepo, secretStore, cfg.Pairing.CertTTL, cfg.Pairing.SecretTTL, log)
		if err := pairingSvc.RebuildDenySet(context.Background()); err != nil {
			return nil, fmt.Errorf("server: rebuild deny set: %w", err)
		}

		// Start background deny-set refresh to bound revocation propagation window.
		if cfg.Pairing.DenySetRefreshInterval > 0 {
			go func() {
				ticker := time.NewTicker(cfg.Pairing.DenySetRefreshInterval)
				defer ticker.Stop()
				for range ticker.C {
					if err := pairingSvc.RebuildDenySet(context.Background()); err != nil {
						log.Warn("deny_set_refresh_failed", "error", err)
					} else {
						log.Debug("deny_set_refreshed", "event", "deny_set_refreshed")
					}
				}
			}()
		}

		s.pairingService = pairingSvc

		// Bootstrap listener (TLS only, no client cert).
		bootstrapSrv, err := buildBootstrapGRPCServer(cfg, pairingSvc, log)
		if err != nil {
			return nil, fmt.Errorf("server: build bootstrap gRPC server: %w", err)
		}
		s.bootstrapServer = bootstrapSrv
	}

	if cfg.EnableHTTP {
		s.httpHandler = httpsvc.New(
			cfg,
			noteSvc,
			projSvc,
			folderSvc,
			deviceSvc,
			deviceAdminSvc,
			userSvc,
			log,
			s.pairingService,
			secretStore,
			relaySvc,
		)
	}

	if cfg.EnableGRPC {
		grpcSrv, err := buildGRPCServer(cfg, noteSvc, projSvc, folderSvc, deviceSvc, deviceAdminSvc, userSvc, s.pairingService, relaySvc, log)
		if err != nil {
			return nil, fmt.Errorf("server: build gRPC server: %w", err)
		}
		s.grpcServer = grpcSrv
	}

	return s, nil
}

// bootstrapAdminDevice upserts the well-known local admin device into the
// device repository on every startup.
//
// This runs only in local-mode — i.e. when NO admin passphrase is configured.
// In that mode the server automatically registers and approves a well-known
// sentinel device URN so that `notx admin` on the same machine works without
// any registration step.
//
// When an AdminPassphraseHash IS set the bootstrap is skipped entirely.
// Remote admin clients must register themselves via POST /v1/devices with the
// correct passphrase to receive role=admin + approval_status=approved.
//
// The operation is idempotent: if the sentinel device already exists its
// approval status and revocation flag are reset to the canonical values so
// that accidental tampering is repaired on restart.
func bootstrapAdminDevice(cfg *config.Config, devRepo repo.DeviceRepository, log *slog.Logger) error {
	// Remote-mode: passphrase is set, so skip the local bootstrap entirely.
	// Admin devices must authenticate themselves during registration.
	if cfg.Admin.AdminPassphraseHash != "" {
		log.Debug("admin passphrase configured — skipping local bootstrap device")
		return nil
	}

	ctx := context.Background()

	deviceURN, err := core.ParseURN(cfg.Admin.DeviceURN)
	if err != nil {
		return fmt.Errorf("parse admin device URN %q: %w", cfg.Admin.DeviceURN, err)
	}
	ownerURN, err := core.ParseURN(cfg.Admin.OwnerURN)
	if err != nil {
		return fmt.Errorf("parse admin owner URN %q: %w", cfg.Admin.OwnerURN, err)
	}

	existing, err := devRepo.GetDevice(ctx, cfg.Admin.DeviceURN)
	if err != nil && !errors.Is(err, repo.ErrNotFound) {
		return fmt.Errorf("look up admin device: %w", err)
	}

	if existing == nil {
		// First boot — register the sentinel as an approved admin device.
		d := &core.Device{
			URN:            deviceURN,
			Name:           "notx-admin",
			OwnerURN:       ownerURN,
			Role:           core.DeviceRoleAdmin,
			ApprovalStatus: core.DeviceApprovalApproved,
			Revoked:        false,
			RegisteredAt:   time.Now().UTC(),
		}
		if err := devRepo.RegisterDevice(ctx, d); err != nil {
			return fmt.Errorf("register admin device: %w", err)
		}
		log.Info("admin device registered (local-mode)", "device_urn", cfg.Admin.DeviceURN)
		return nil
	}

	// Subsequent boots — ensure the sentinel is always admin + approved + not revoked.
	needsUpdate := existing.ApprovalStatus != core.DeviceApprovalApproved ||
		existing.Revoked ||
		existing.Role != core.DeviceRoleAdmin
	if needsUpdate {
		existing.Role = core.DeviceRoleAdmin
		existing.ApprovalStatus = core.DeviceApprovalApproved
		existing.Revoked = false
		if err := devRepo.UpdateDevice(ctx, existing); err != nil {
			return fmt.Errorf("restore admin device: %w", err)
		}
		log.Warn("admin device restored to canonical state", "device_urn", cfg.Admin.DeviceURN)
		return nil
	}

	log.Debug("admin device ok (local-mode)", "device_urn", cfg.Admin.DeviceURN)
	return nil
}

// Run starts all configured servers and blocks until they all exit.
// It listens for SIGINT / SIGTERM and initiates a graceful shutdown on receipt.
// Run returns nil on clean shutdown and a non-nil error if any server fails to
// start or exits unexpectedly.
func (s *Server) Run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return s.run(ctx)
}

// RunWithContext starts all configured servers and blocks until they all exit
// or the supplied context is cancelled. It is the test-friendly counterpart to
// Run: instead of waiting for an OS signal, shutdown is triggered by cancelling
// ctx. RunWithContext returns nil on clean shutdown.
func (s *Server) RunWithContext(ctx context.Context) error {
	return s.run(ctx)
}

// run is the shared implementation used by both Run and RunWithContext.
func (s *Server) run(ctx context.Context) error {
	errCh := make(chan error, 3) // at most three servers (http, grpc, bootstrap)
	var wg sync.WaitGroup

	// ── Startup summary ──────────────────────────────────────────────────────
	logAttrs := []any{"device_urn", s.cfg.Admin.DeviceURN}
	if s.pairingService != nil {
		logAttrs = append(logAttrs, "server_urn", s.pairingService.URN())
	}
	s.log.Info("notx engine ready", logAttrs...)

	// ── Start HTTP ───────────────────────────────────────────────────────────
	if s.httpHandler != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.httpHandler.Serve(); err != nil {
				errCh <- fmt.Errorf("http server: %w", err)
			}
		}()
		s.log.Info("http server starting", "addr", s.cfg.HTTPAddr())
	}

	// ── Start gRPC ───────────────────────────────────────────────────────────
	if s.grpcServer != nil {
		ln, err := net.Listen("tcp", s.cfg.GRPCAddr())
		if err != nil {
			// HTTP may already be running; shut it down before returning.
			s.initiateShutdown(context.Background())
			wg.Wait()
			return fmt.Errorf("grpc: listen on %s: %w", s.cfg.GRPCAddr(), err)
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			s.log.Info("grpc server starting", "addr", s.cfg.GRPCAddr())
			if err := s.grpcServer.Serve(ln); err != nil {
				errCh <- fmt.Errorf("grpc server: %w", err)
			}
		}()
	}

	// ── Start bootstrap gRPC (pairing) ───────────────────────────────────────
	if s.bootstrapServer != nil {
		bootstrapAddr := s.cfg.PairingBootstrapAddr()
		ln, err := net.Listen("tcp", bootstrapAddr)
		if err != nil {
			s.initiateShutdown(context.Background())
			wg.Wait()
			return fmt.Errorf("bootstrap grpc: listen on %s: %w", bootstrapAddr, err)
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			s.log.Info("bootstrap grpc server starting", "addr", bootstrapAddr)
			if err := s.bootstrapServer.Serve(ln); err != nil {
				errCh <- fmt.Errorf("bootstrap grpc server: %w", err)
			}
		}()
	}

	// ── Wait for shutdown signal or a server error ───────────────────────────
	select {
	case <-ctx.Done():
		s.log.Info("shutdown signal received")
	case err := <-errCh:
		s.log.Error("server error — initiating shutdown", "err", err)
	}

	// ── Graceful shutdown ────────────────────────────────────────────────────
	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
	defer cancel()

	s.initiateShutdown(shutdownCtx)
	wg.Wait()

	// Drain the error channel to report any remaining failures.
	close(errCh)
	var errs []error
	for err := range errCh {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// initiateShutdown stops all active servers, respecting the provided context
// deadline. It is safe to call multiple times.
func (s *Server) initiateShutdown(ctx context.Context) {
	var wg sync.WaitGroup

	if s.httpHandler != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.log.Info("shutting down http server")
			if err := s.httpHandler.Shutdown(ctx); err != nil {
				s.log.Warn("http shutdown error", "err", err)
			}
		}()
	}

	if s.grpcServer != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.log.Info("shutting down grpc server")
			// GracefulStop drains in-flight RPCs; fall back to Stop if the
			// context deadline is exceeded.
			stopped := make(chan struct{})
			go func() {
				s.grpcServer.GracefulStop()
				close(stopped)
			}()
			select {
			case <-stopped:
			case <-ctx.Done():
				s.log.Warn("grpc graceful stop timed out — forcing stop")
				s.grpcServer.Stop()
			}
		}()
	}

	if s.bootstrapServer != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.log.Info("shutting down bootstrap grpc server")
			stopped := make(chan struct{})
			go func() {
				s.bootstrapServer.GracefulStop()
				close(stopped)
			}()
			select {
			case <-stopped:
			case <-ctx.Done():
				s.log.Warn("bootstrap grpc graceful stop timed out — forcing stop")
				s.bootstrapServer.Stop()
			}
		}()
	}

	wg.Wait()
}

// ─────────────────────────────────────────────────────────────────────────────
// gRPC server construction
// ─────────────────────────────────────────────────────────────────────────────

func buildGRPCServer(
	cfg *config.Config,
	noteSvc *grpcsvc.NoteServer,
	projSvc *grpcsvc.ProjectServer,
	folderSvc *grpcsvc.FolderServer,
	deviceSvc *grpcsvc.DeviceServer,
	deviceAdminSvc *grpcsvc.DeviceAdminServer,
	userSvc *grpcsvc.UserServer,
	pairingSvc *grpcsvc.PairingServer,
	relaySvc *grpcsvc.RelayServer,
	log *slog.Logger,
) (*googlegrpc.Server, error) {
	opts := []googlegrpc.ServerOption{
		googlegrpc.UnaryInterceptor(loggingUnaryInterceptor(log)),
		googlegrpc.StreamInterceptor(loggingStreamInterceptor(log)),
	}

	if cfg.TLSEnabled() {
		creds, err := credentials.NewServerTLSFromFile(cfg.TLSCertFile, cfg.TLSKeyFile)
		if err != nil {
			return nil, fmt.Errorf("load TLS credentials: %w", err)
		}
		opts = append(opts, googlegrpc.Creds(creds))
		log.Info("gRPC TLS enabled", "cert", cfg.TLSCertFile)
	} else {
		log.Warn("gRPC TLS is disabled — suitable for development only")
	}

	srv := googlegrpc.NewServer(opts...)

	// Register the shared service instances.
	pb.RegisterNoteServiceServer(srv, noteSvc)
	pb.RegisterDeviceServiceServer(srv, deviceSvc)
	pb.RegisterDeviceAdminServiceServer(srv, deviceAdminSvc)
	pb.RegisterProjectServiceServer(srv, projSvc)
	pb.RegisterFolderServiceServer(srv, folderSvc)

	pb.RegisterUserServiceServer(srv, userSvc)
	pb.RegisterRelayServiceServer(srv, relaySvc)

	if pairingSvc != nil {
		pb.RegisterServerPairingServiceServer(srv, pairingSvc.PrimaryService())
	}

	// Enable server reflection so grpcurl and other tools work out of the box.
	reflection.Register(srv)

	return srv, nil
}

// buildBootstrapGRPCServer builds a TLS-only gRPC server for the bootstrap
// pairing listener (port 50052). Client certificates are not required.
func buildBootstrapGRPCServer(cfg *config.Config, pairingSvc *grpcsvc.PairingServer, log *slog.Logger) (*googlegrpc.Server, error) {
	opts := []googlegrpc.ServerOption{
		googlegrpc.UnaryInterceptor(loggingUnaryInterceptor(log)),
	}

	if cfg.TLSEnabled() {
		creds, err := credentials.NewServerTLSFromFile(cfg.TLSCertFile, cfg.TLSKeyFile)
		if err != nil {
			return nil, fmt.Errorf("bootstrap: load TLS credentials: %w", err)
		}
		opts = append(opts, googlegrpc.Creds(creds))
	}

	srv := googlegrpc.NewServer(opts...)
	pb.RegisterServerPairingServiceServer(srv, pairingSvc.BootstrapService())
	reflection.Register(srv)
	return srv, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// gRPC interceptors
// ─────────────────────────────────────────────────────────────────────────────

func loggingUnaryInterceptor(log *slog.Logger) googlegrpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *googlegrpc.UnaryServerInfo,
		handler googlegrpc.UnaryHandler,
	) (any, error) {
		resp, err := handler(ctx, req)
		if err != nil {
			log.Warn("grpc unary error", "method", info.FullMethod, "err", err)
		} else {
			log.Debug("grpc unary ok", "method", info.FullMethod)
		}
		return resp, err
	}
}

func loggingStreamInterceptor(log *slog.Logger) googlegrpc.StreamServerInterceptor {
	return func(
		srv any,
		ss googlegrpc.ServerStream,
		info *googlegrpc.StreamServerInfo,
		handler googlegrpc.StreamHandler,
	) error {
		err := handler(srv, ss)
		if err != nil {
			log.Warn("grpc stream error", "method", info.FullMethod, "err", err)
		} else {
			log.Debug("grpc stream ok", "method", info.FullMethod)
		}
		return err
	}
}
