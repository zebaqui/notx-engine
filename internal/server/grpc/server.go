package grpc

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"

	"github.com/zebaqui/notx-engine/config"
	pb "github.com/zebaqui/notx-engine/proto"
	"github.com/zebaqui/notx-engine/repo"
)

// Server wraps a grpc.Server and owns the lifecycle of the gRPC listener.
// It registers NoteServer, ProjectServer, FolderServer, and optionally
// ContextServer and LinkServer. All traffic is plaintext localhost-only.
type Server struct {
	cfg      *config.Config
	log      *slog.Logger
	gs       *grpc.Server
	noteS    *NoteServer
	projS    *ProjectServer
	folderS  *FolderServer
	contextS *ContextServer // optional — nil when contextRepo is nil
	linkS    *LinkServer    // optional — nil when linkRepo is nil
}

// NewServer creates a fully wired gRPC Server ready to call Serve on.
// contextRepo and linkRepo may be nil; their corresponding services are only
// registered when a non-nil repository is provided.
func NewServer(
	cfg *config.Config,
	r repo.NoteRepository,
	projRepo repo.ProjectRepository,
	contextRepo repo.ContextRepository,
	linkRepo repo.LinkRepository,
	log *slog.Logger,
) (*Server, error) {
	opts := []grpc.ServerOption{
		grpc.Creds(insecure.NewCredentials()),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle:     5 * time.Minute,
			MaxConnectionAge:      30 * time.Minute,
			MaxConnectionAgeGrace: 5 * time.Second,
			Time:                  2 * time.Minute,
			Timeout:               20 * time.Second,
		}),
		grpc.ChainUnaryInterceptor(
			loggingUnaryInterceptor(log),
			recoveryUnaryInterceptor(log),
		),
		grpc.ChainStreamInterceptor(
			loggingStreamInterceptor(log),
			recoveryStreamInterceptor(log),
		),
	}

	gs := grpc.NewServer(opts...)

	noteS := NewNoteServer(r, cfg.DefaultPageSize, cfg.MaxPageSize)
	projS := NewProjectServer(projRepo, cfg.DefaultPageSize, cfg.MaxPageSize)
	folderS := NewFolderServer(projRepo, cfg.DefaultPageSize, cfg.MaxPageSize)

	pb.RegisterNoteServiceServer(gs, noteS)
	pb.RegisterProjectServiceServer(gs, projS)
	pb.RegisterFolderServiceServer(gs, folderS)

	var contextS *ContextServer
	if contextRepo != nil {
		contextS = NewContextServer(contextRepo, cfg.DefaultPageSize, cfg.MaxPageSize)
		pb.RegisterContextServiceServer(gs, contextS)
	}

	var linkS *LinkServer
	if linkRepo != nil {
		linkS = NewLinkServer(linkRepo)
		pb.RegisterLinkServiceServer(gs, linkS)
	}

	// Enable gRPC server reflection so tools like grpcurl work out of the box.
	reflection.Register(gs)

	return &Server{
		cfg:      cfg,
		log:      log,
		gs:       gs,
		noteS:    noteS,
		projS:    projS,
		folderS:  folderS,
		contextS: contextS,
		linkS:    linkS,
	}, nil
}

// Serve starts listening on the configured gRPC address and blocks until the
// server stops. It returns nil on a graceful stop and a non-nil error on
// unexpected failure.
func (s *Server) Serve() error {
	addr := s.cfg.GRPCAddr()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("grpc: listen on %s: %w", addr, err)
	}

	s.log.Info("grpc server listening", "addr", addr)

	if err := s.gs.Serve(ln); err != nil {
		return fmt.Errorf("grpc: serve: %w", err)
	}
	return nil
}

// Shutdown initiates a graceful shutdown. In-flight RPCs are allowed to
// complete until ctx is cancelled, at which point the server is force-stopped.
func (s *Server) Shutdown(ctx context.Context) {
	stopped := make(chan struct{})
	go func() {
		s.gs.GracefulStop()
		close(stopped)
	}()

	select {
	case <-ctx.Done():
		s.log.Warn("grpc: graceful shutdown timed out — forcing stop")
		s.gs.Stop()
	case <-stopped:
		s.log.Info("grpc: graceful shutdown complete")
	}
}

// NoteService returns the NoteServer so the HTTP adapter layer can
// call it directly without a network hop.
func (s *Server) NoteService() *NoteServer { return s.noteS }

// ProjectService returns the ProjectServer so the HTTP adapter layer can
// call it directly without a network hop.
func (s *Server) ProjectService() *ProjectServer { return s.projS }

// FolderService returns the FolderServer so the HTTP adapter layer can
// call it directly without a network hop.
func (s *Server) FolderService() *FolderServer { return s.folderS }

// ContextService returns the ContextServer, or nil if no context repository
// was provided at construction time.
func (s *Server) ContextService() *ContextServer { return s.contextS }

// LinkService returns the LinkServer, or nil if no link repository was
// provided at construction time.
func (s *Server) LinkService() *LinkServer { return s.linkS }

// ─────────────────────────────────────────────────────────────────────────────
// Interceptors
// ─────────────────────────────────────────────────────────────────────────────

func loggingUnaryInterceptor(log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		log.Info("grpc unary",
			"method", info.FullMethod,
			"duration_ms", time.Since(start).Milliseconds(),
			"error", err,
		)
		return resp, err
	}
}

func recoveryUnaryInterceptor(log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (resp any, err error) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Error("grpc unary panic", "method", info.FullMethod, "panic", rec)
				err = fmt.Errorf("internal server error")
			}
		}()
		return handler(ctx, req)
	}
}

func loggingStreamInterceptor(log *slog.Logger) grpc.StreamServerInterceptor {
	return func(
		srv any,
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		start := time.Now()
		err := handler(srv, ss)
		log.Info("grpc stream",
			"method", info.FullMethod,
			"duration_ms", time.Since(start).Milliseconds(),
			"error", err,
		)
		return err
	}
}

func recoveryStreamInterceptor(log *slog.Logger) grpc.StreamServerInterceptor {
	return func(
		srv any,
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) (err error) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Error("grpc stream panic", "method", info.FullMethod, "panic", rec)
				err = fmt.Errorf("internal server error")
			}
		}()
		return handler(srv, ss)
	}
}
