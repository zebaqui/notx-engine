// Package sync is the public facade for the notx real-time sync subsystem.
//
// It exposes:
//
//   - StreamRegistry  — tracks active SyncStream connections per namespace and
//     delivers NoteChangedNotif messages to them.  The registry also implements
//     the StreamNotifier interface that the postgres provider calls after every
//     committed write.
//
//   - NewSyncServiceServer — builds a pb.SyncServiceServer (concretely a
//     *grpc.SyncServer) wired with the registry and a server-namespace resolver.
//     Pass the result to pairing.StartHub so the primary mTLS listener registers
//     the bidirectional SyncStream RPC.
//
// notx (the platform) imports this package instead of the internal grpc package
// so that Go's internal-package visibility rules are respected.
package sync

import (
	"context"
	"log/slog"
	"time"

	"github.com/zebaqui/notx-engine/core"
	grpcsvc "github.com/zebaqui/notx-engine/internal/server/grpc"
	internalsync "github.com/zebaqui/notx-engine/internal/sync"
	pb "github.com/zebaqui/notx-engine/proto"
)

// StreamRegistry tracks active SyncStream gRPC connections keyed by namespace
// and fans out NoteChangedNotif messages to every connected stream.
//
// It implements the StreamNotifier shape: call Notify after every committed note
// write and the registry will push a NoteChangedNotif to all streams registered
// for that namespace.
type StreamRegistry = internalsync.StreamRegistry

// NewStreamRegistry returns a ready-to-use StreamRegistry.
func NewStreamRegistry() *StreamRegistry {
	return internalsync.NewStreamRegistry()
}

// NoteReceiver is the subset of repo.NoteRepository that SyncStream needs on
// the cloud (authority) side. *postgres.TenantScopedProvider satisfies this
// interface structurally.
//
// This is a re-export of internalsync.NoteReceiver.
type NoteReceiver = internalsync.NoteReceiver

// TenantProvider resolves a NoteReceiver for a given namespace.
// Implement this with a thin adapter over *postgres.PgProvider.
//
// This is a re-export of internalsync.TenantProvider.
type TenantProvider = internalsync.TenantProvider

// ServerNamespaceResolver maps a server URN (the CN of its mTLS certificate)
// to the tenant namespace that owns it. Implement this with
// *postgres.CrossNamespaceServerRepo.
type ServerNamespaceResolver interface {
	GetServerAnyNamespace(ctx context.Context, urn string) (namespace string, err error)
}

// NewSyncServiceServer builds a pb.SyncServiceServer that:
//
//   - accepts long-lived SyncStream connections from paired local engines,
//   - fans out NoteChangedNotif messages via registry when the cloud writes a note,
//   - receives note pushes from local engines and stores them via provider.ForTenant,
//   - serves pull requests from local engines by reading from provider.ForTenant.
//
// Pass the returned value to pairing.StartHub as the syncSvc argument.
//
//	registry   built with NewStreamRegistry(); also wired to the postgres
//	           provider via provider.SetStreamNotifier(registry).
//	srvRepo    resolves the peer certificate CN to a tenant namespace.
//	provider   tenant-scoped note access (may be nil on the local engine side).
//	log        structured logger; defaults to slog.Default() when nil.
func NewSyncServiceServer(
	registry *StreamRegistry,
	srvRepo ServerNamespaceResolver,
	provider TenantProvider,
	log *slog.Logger,
) pb.SyncServiceServer {
	return grpcsvc.NewSyncServer(registry, srvRepo, provider, log)
}

// Compile-time assertions ────────────────────────────────────────────────────

// Ensure StreamRegistry implements the StreamNotifier shape expected by
// postgres.PgProvider.SetStreamNotifier.
var _ interface {
	Notify(namespace, noteURN string, headSeq int32, updatedAt time.Time)
} = (*StreamRegistry)(nil)

// Ensure NoteReceiver is correctly typed (references core types from notx-engine).
var _ NoteReceiver = (interface {
	ReceiveSharedNote(ctx context.Context, note *core.Note, events []*core.Event) error
	Get(ctx context.Context, urn string) (*core.Note, error)
	Events(ctx context.Context, noteURN string, fromSequence int) ([]*core.Event, error)
})(nil)
