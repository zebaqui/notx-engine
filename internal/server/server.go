package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/zebaqui/notx-engine/config"
	httpsvc "github.com/zebaqui/notx-engine/http"
	grpcsvc "github.com/zebaqui/notx-engine/internal/server/grpc"
	"github.com/zebaqui/notx-engine/repo"
	"github.com/zebaqui/notx-engine/snip"
)

// Server is the top-level orchestrator. It owns the lifecycle of the HTTP
// and gRPC servers and coordinates graceful shutdown when a signal arrives.
type Server struct {
	cfg         *config.Config
	repo        repo.NoteRepository
	projRepo    repo.ProjectRepository
	contextRepo repo.ContextRepository
	linkRepo    repo.LinkRepository
	log         *slog.Logger

	httpHandler *httpsvc.Handler
	grpcServer  *grpcsvc.Server
	plugins     []snip.SnipPlugin
}

// New creates a Server from the given config and repositories.
// It wires all sub-components but does not start any listeners yet.
func New(
	cfg *config.Config,
	r repo.NoteRepository,
	projRepo repo.ProjectRepository,
	contextRepo repo.ContextRepository,
	linkRepo repo.LinkRepository,
	log *slog.Logger,
	plugins []snip.SnipPlugin,
) (*Server, error) {
	if log == nil {
		log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}

	s := &Server{
		cfg:         cfg,
		repo:        r,
		projRepo:    projRepo,
		contextRepo: contextRepo,
		linkRepo:    linkRepo,
		log:         log,
	}

	// ── Build gRPC service instances (shared by both HTTP and gRPC layers) ───
	noteSvc := grpcsvc.NewNoteServerWithContext(r, contextRepo, cfg.DefaultPageSize, cfg.MaxPageSize)
	projSvc := grpcsvc.NewProjectServer(projRepo, cfg.DefaultPageSize, cfg.MaxPageSize)
	folderSvc := grpcsvc.NewFolderServer(projRepo, cfg.DefaultPageSize, cfg.MaxPageSize)

	var contextSvc *grpcsvc.ContextServer
	if contextRepo != nil {
		contextSvc = grpcsvc.NewContextServer(contextRepo, cfg.DefaultPageSize, cfg.MaxPageSize)
	}

	var linkSvc *grpcsvc.LinkServer
	if linkRepo != nil {
		linkSvc = grpcsvc.NewLinkServer(linkRepo)
	}

	// ── Build HTTP handler ───────────────────────────────────────────────────
	if cfg.EnableHTTP {
		s.httpHandler = httpsvc.New(
			cfg,
			noteSvc,
			projSvc,
			folderSvc,
			contextSvc,
			linkSvc,
			log,
			plugins,
		)
	}

	// ── Build gRPC server ────────────────────────────────────────────────────
	if cfg.EnableGRPC {
		grpcSrv, err := grpcsvc.NewServer(cfg, r, projRepo, contextRepo, linkRepo, log)
		if err != nil {
			return nil, fmt.Errorf("server: build gRPC server: %w", err)
		}
		s.grpcServer = grpcSrv
	}

	// ── Wire snip plugins ────────────────────────────────────────────────────
	if len(plugins) > 0 {
		registry := snip.NewRegistry()

		var sqlDB *sql.DB
		if dbp, ok := r.(interface{ DB() *sql.DB }); ok {
			sqlDB = dbp.DB()
		}

		for _, p := range plugins {
			env := snip.PluginEnv{
				DB:       sqlDB,
				NoteRepo: r,
				ProjRepo: projRepo,
				Config:   cfg,
				Log:      log.With("plugin", p.Type()),
			}
			if err := p.Init(context.Background(), env); err != nil {
				return nil, fmt.Errorf("snip plugin %s: init: %w", p.Type(), err)
			}
			registry.Register(p)
		}
		noteSvc.SetSnipRegistry(registry)
		s.plugins = plugins
	}

	return s, nil
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
	errCh := make(chan error, 2) // at most two servers (http, grpc)
	var wg sync.WaitGroup

	// ── Startup summary ──────────────────────────────────────────────────────
	s.log.Info("notx engine ready")

	// ── Start snip plugins ───────────────────────────────────────────────────
	for _, p := range s.plugins {
		if err := p.Start(ctx); err != nil {
			s.log.Warn("snip plugin start failed", "plugin", p.Type(), "err", err)
		}
	}

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
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.log.Info("grpc server starting", "addr", s.cfg.GRPCAddr())
			if err := s.grpcServer.Serve(); err != nil {
				errCh <- fmt.Errorf("grpc server: %w", err)
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

	// ── Stop snip plugins ────────────────────────────────────────────────────
	for _, p := range s.plugins {
		_ = p.Stop(shutdownCtx)
	}

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
			s.grpcServer.Shutdown(ctx)
		}()
	}

	wg.Wait()
}
