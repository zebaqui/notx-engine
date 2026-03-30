package grpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net"
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"

	"github.com/zebaqui/notx-engine/internal/relay"
	"github.com/zebaqui/notx-engine/internal/repo"
	"github.com/zebaqui/notx-engine/internal/server/config"
	pb "github.com/zebaqui/notx-engine/internal/server/proto"

	"time"
)

// Server wraps a grpc.Server and owns the lifecycle of the gRPC listener.
// It registers NoteServiceServer and DeviceServiceServer and handles TLS /
// mTLS configuration derived from Config.
type Server struct {
	cfg     *config.Config
	log     *slog.Logger
	gs      *grpc.Server
	noteS   *NoteServiceServer
	deviceS *DeviceServiceServer
	relayS  *RelayServiceServer
}

// NewServer creates a fully wired gRPC Server ready to call Serve on.
// It does NOT start listening; call Serve to begin accepting connections.
func NewServer(cfg *config.Config, r repo.NoteRepository, devRepo repo.DeviceRepository, log *slog.Logger) (*Server, error) {
	creds, err := buildTransportCredentials(cfg)
	if err != nil {
		return nil, fmt.Errorf("grpc: build TLS credentials: %w", err)
	}

	opts := []grpc.ServerOption{
		grpc.Creds(creds),
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

	noteS := NewNoteServiceServer(r, cfg.DefaultPageSize, cfg.MaxPageSize)
	deviceS := NewDeviceServiceServer(devRepo)
	relayPolicy := relay.DefaultPolicy()
	relayS := NewRelayServiceServer(devRepo, relayPolicy, log, nil)

	pb.RegisterNoteServiceServer(gs, noteS)
	pb.RegisterDeviceServiceServer(gs, deviceS)
	pb.RegisterRelayServiceServer(gs, relayS)

	// Enable gRPC server reflection so tools like grpcurl work out of the box.
	reflection.Register(gs)

	return &Server{
		cfg:     cfg,
		log:     log,
		gs:      gs,
		noteS:   noteS,
		deviceS: deviceS,
		relayS:  relayS,
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

	s.log.Info("grpc server listening",
		"addr", addr,
		"tls", s.cfg.TLSEnabled(),
		"mtls", s.cfg.MTLSEnabled(),
	)

	if err := s.gs.Serve(ln); err != nil {
		return fmt.Errorf("grpc: serve: %w", err)
	}
	return nil
}

// RelayService returns the RelayServiceServer so the HTTP adapter layer can
// call it directly without a network hop.
func (s *Server) RelayService() *RelayServiceServer {
	return s.relayS
}

// Shutdown initiates a graceful shutdown.  In-flight RPCs are allowed to
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

// ─────────────────────────────────────────────────────────────────────────────
// TLS helpers
// ─────────────────────────────────────────────────────────────────────────────

// buildTransportCredentials returns the appropriate gRPC transport credentials
// based on the Config:
//
//   - No TLS configured → insecure (development only)
//   - TLS cert + key    → TLS 1.3 server credentials
//   - TLS + CA cert     → mTLS (client certificate required)
func buildTransportCredentials(cfg *config.Config) (credentials.TransportCredentials, error) {
	if !cfg.TLSEnabled() {
		return insecure.NewCredentials(), nil
	}

	cert, err := tls.LoadX509KeyPair(cfg.TLSCertFile, cfg.TLSKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load server cert/key: %w", err)
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13, // enforce TLS 1.3 minimum (Phase 5 requirement)
	}

	if cfg.MTLSEnabled() {
		caPEM, err := os.ReadFile(cfg.TLSCAFile)
		if err != nil {
			return nil, fmt.Errorf("read CA cert %q: %w", cfg.TLSCAFile, err)
		}
		caPool := x509.NewCertPool()
		if !caPool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("parse CA cert %q: no valid PEM blocks found", cfg.TLSCAFile)
		}
		tlsCfg.ClientCAs = caPool
		tlsCfg.ClientAuth = tls.RequireAndVerifyClientCert
	}

	return credentials.NewTLS(tlsCfg), nil
}

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
