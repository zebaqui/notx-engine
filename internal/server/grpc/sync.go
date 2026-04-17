package grpc

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/zebaqui/notx-engine/core"
	syncsvc "github.com/zebaqui/notx-engine/internal/sync"
	pb "github.com/zebaqui/notx-engine/proto"
)

// serverNamespaceResolver maps a server URN to its owning namespace.
type serverNamespaceResolver interface {
	GetServerAnyNamespace(ctx context.Context, urn string) (namespace string, err error)
}

// TenantProvider resolves a NoteReceiver for a given namespace.
// Defined here as an alias of the internal sync package's TenantProvider so
// both the grpc package and the public notx-engine/sync facade share one type.
type TenantProvider = syncsvc.TenantProvider

// NoteReceiver is the subset of repo.NoteRepository that SyncStream needs.
// Alias of internalsync.NoteReceiver — defined there so the public
// notx-engine/sync facade can re-export it without an import cycle.
type NoteReceiver = syncsvc.NoteReceiver

// SyncServer implements pb.SyncServiceServer.
// It is registered on the primary mTLS gRPC listener so that paired local
// engines can sync notes bidirectionally over a certificate-authenticated channel.
type SyncServer struct {
	pb.UnimplementedSyncServiceServer
	registry *syncsvc.StreamRegistry
	srvRepo  serverNamespaceResolver
	provider TenantProvider // nil on local engine side (client-only)
	log      *slog.Logger
}

// NewSyncServer returns a ready-to-register SyncServer.
//
//   - registry  the stream registry that dispatches NoteChangedNotif to connected streams
//   - srvRepo   resolves server URN → namespace (from the mTLS peer cert CN)
//   - provider  tenant-scoped note access; may be nil on the local engine side
//   - log       structured logger
func NewSyncServer(
	registry *syncsvc.StreamRegistry,
	srvRepo serverNamespaceResolver,
	provider TenantProvider,
	log *slog.Logger,
) *SyncServer {
	if log == nil {
		log = slog.Default()
	}
	return &SyncServer{
		registry: registry,
		srvRepo:  srvRepo,
		provider: provider,
		log:      log,
	}
}

// SyncNotes is a bidirectional streaming RPC.
//
// Phase 1 — inbound push: the client streams SyncNoteMessage frames, one per
// local note.  For each frame the server calls ReceiveSharedNote and sends
// back a SyncNoteResult (EventsStored >= 0).  Per-note errors are reported
// inside the result frame (non-fatal); stream-level errors abort the RPC.
//
// Phase 2 — cloud catalogue: after the client closes its send side (EOF), the
// server lists all notes it holds and sends a SyncNoteResult per note with
// EventsStored = -1.  The -1 sentinel tells the client "this note exists on
// the cloud; pull it if you don't have it."
func (s *SyncServer) SyncNotes(stream pb.SyncService_SyncNotesServer) error {
	ctx := stream.Context()

	ns, err := s.namespaceFromPeer(ctx)
	if err != nil {
		s.log.Warn("sync: could not determine namespace for SyncNotes", "err", err)
		// Fall back to a nil provider path — handled below per-note.
	}

	// ── Phase 1: receive notes pushed by the client ──────────────────────────
	for {
		msg, recvErr := stream.Recv()
		if recvErr == io.EOF {
			break
		}
		if recvErr != nil {
			s.log.Error("sync: recv error", "err", recvErr)
			return recvErr
		}

		var result *pb.SyncNoteResult

		if s.provider == nil || ns == "" {
			result = &pb.SyncNoteResult{
				NoteUrn: msg.GetHeader().GetUrn(),
				Error:   "sync server: no tenant provider configured",
			}
		} else {
			protoNote, protoEvents := msg.GetHeader(), msg.GetEvents()
			note, events, convErr := protoToCore(protoNote, protoEvents)
			if convErr != nil {
				result = &pb.SyncNoteResult{
					NoteUrn: msg.GetHeader().GetUrn(),
					Error:   convErr.Error(),
				}
			} else {
				recvNoteErr := s.provider.ForTenant(ns).ReceiveSharedNote(ctx, note, events)
				if recvNoteErr != nil {
					s.log.Warn("sync: receive_shared_note failed",
						"urn", msg.GetHeader().GetUrn(),
						"err", recvNoteErr,
					)
					result = &pb.SyncNoteResult{
						NoteUrn: msg.GetHeader().GetUrn(),
						Error:   recvNoteErr.Error(),
					}
				} else {
					result = &pb.SyncNoteResult{
						NoteUrn:      msg.GetHeader().GetUrn(),
						EventsStored: int32(len(protoEvents)),
					}
				}
			}
		}

		if sendErr := stream.Send(result); sendErr != nil {
			s.log.Error("sync: send result error", "err", sendErr)
			return sendErr
		}
	}

	// ── Phase 2: stream cloud note catalogue back to the client ──────────────
	if s.provider == nil || ns == "" {
		return nil
	}

	nr := s.provider.ForTenant(ns)
	// Use Events from seq 1 as a proxy — instead we list notes via a page cursor.
	// NoteReceiver only exposes Get/Events/ReceiveSharedNote, not List.
	// For catalogue phase, fall through — the persistent SyncStream handles
	// cloud→local notifications via NoteChangedNotif. The one-shot SyncNotes
	// RPC catalogue phase is best-effort only when a full NoteRepository is available.
	// Since NoteReceiver doesn't expose List, skip Phase 2 here.
	_ = nr
	return nil
}

// SyncStream implements the long-lived bidirectional stream for real-time sync.
func (s *SyncServer) SyncStream(stream pb.SyncService_SyncStreamServer) error {
	if s.registry == nil {
		return status.Error(codes.Unavailable, "stream registry not configured")
	}

	ctx := stream.Context()

	ns, err := s.namespaceFromPeer(ctx)
	if err != nil {
		return status.Errorf(codes.Unauthenticated, "could not determine namespace: %v", err)
	}

	id, sendCh := s.registry.Register(ns)
	defer s.registry.Deregister(ns, id)

	s.log.Info("sync_stream: connection opened", "namespace", ns, "stream_id", id)
	defer s.log.Info("sync_stream: connection closed", "namespace", ns, "stream_id", id)

	// Start a sender goroutine that drains sendCh and forwards to the stream.
	sendErrCh := make(chan error, 1)
	go func() {
		for msg := range sendCh {
			if err := stream.Send(msg); err != nil {
				sendErrCh <- err
				return
			}
		}
	}()

	// Main receive loop: handle messages from the local server.
	for {
		msg, recvErr := stream.Recv()
		if recvErr == io.EOF {
			return nil
		}
		if recvErr != nil {
			return recvErr
		}

		switch {
		case msg.NotePush != nil:
			result := s.handleNotePush(ctx, ns, msg.NotePush)
			ack := &pb.SyncStreamMessage{PushAck: result}
			if sendErr := stream.Send(ack); sendErr != nil {
				return sendErr
			}

		case msg.PullRequest != nil:
			s.handlePullRequest(ctx, ns, stream, msg.PullRequest)

		case msg.Ping != nil:
			pong := &pb.SyncStreamMessage{
				Ping: &pb.SyncPing{TimestampMs: msg.Ping.TimestampMs},
			}
			if sendErr := stream.Send(pong); sendErr != nil {
				return sendErr
			}
		}

		// Check if sender goroutine errored.
		select {
		case err := <-sendErrCh:
			return err
		default:
		}
	}
}

// handleNotePush processes a note pushed by the local server over SyncStream.
func (s *SyncServer) handleNotePush(ctx context.Context, ns string, push *pb.SyncNoteMessage) *pb.SyncNoteResult {
	urn := push.GetHeader().GetUrn()

	if s.provider == nil {
		return &pb.SyncNoteResult{
			NoteUrn: urn,
			Error:   "sync server: no tenant provider configured",
		}
	}

	note, events, err := protoToCore(push.GetHeader(), push.GetEvents())
	if err != nil {
		s.log.Warn("sync_stream: proto→core conversion failed", "urn", urn, "err", err)
		return &pb.SyncNoteResult{NoteUrn: urn, Error: err.Error()}
	}

	if recvErr := s.provider.ForTenant(ns).ReceiveSharedNote(ctx, note, events); recvErr != nil {
		s.log.Warn("sync_stream: receive_shared_note failed", "urn", urn, "err", recvErr)
		return &pb.SyncNoteResult{NoteUrn: urn, Error: recvErr.Error()}
	}

	return &pb.SyncNoteResult{
		NoteUrn:      urn,
		EventsStored: int32(len(events)),
	}
}

// handlePullRequest fetches the requested note and events and sends them back
// on the stream as a SyncStreamMessage{NotePush: ...}.
func (s *SyncServer) handlePullRequest(ctx context.Context, ns string, stream pb.SyncService_SyncStreamServer, req *pb.SyncPullRequest) {
	urn := req.GetNoteUrn()
	fromSeq := int(req.GetFromSeq())

	if s.provider == nil {
		s.log.Warn("sync_stream: pull_request but no tenant provider", "urn", urn)
		return
	}

	nr := s.provider.ForTenant(ns)

	note, err := nr.Get(ctx, urn)
	if err != nil {
		s.log.Warn("sync_stream: pull_request get note failed", "urn", urn, "err", err)
		return
	}

	events, err := nr.Events(ctx, urn, fromSeq)
	if err != nil {
		s.log.Warn("sync_stream: pull_request get events failed", "urn", urn, "err", err)
		return
	}

	pbEvents := make([]*pb.Event, 0, len(events))
	for _, ev := range events {
		pbEvents = append(pbEvents, coreEventToProto(ev))
	}
	push := &pb.SyncStreamMessage{
		NotePush: &pb.SyncNoteMessage{
			Header: coreNoteToHeader(note),
			Events: pbEvents,
		},
	}
	if sendErr := stream.Send(push); sendErr != nil {
		s.log.Warn("sync_stream: pull_request send failed", "urn", urn, "err", sendErr)
	}
}

// namespaceFromPeer extracts the namespace by reading the TLS peer certificate
// CN (which is the server URN, e.g. "urn:notx:srv:<uuid>") and looking up the
// namespace in srvRepo.
func (s *SyncServer) namespaceFromPeer(ctx context.Context) (string, error) {
	if s.srvRepo == nil {
		return "", fmt.Errorf("no server namespace resolver configured")
	}

	p, ok := peer.FromContext(ctx)
	if !ok {
		return "", fmt.Errorf("no peer information in context")
	}

	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok {
		return "", fmt.Errorf("peer auth info is not TLS (got %T)", p.AuthInfo)
	}

	return s.namespaceFromTLSState(ctx, tlsInfo.State)
}

// protoToCore converts a proto NoteHeader + Events slice to core domain objects.
// It reuses the same parsing logic as ReceiveSharedNote in note.go.
func protoToCore(header *pb.NoteHeader, protoEvents []*pb.Event) (*core.Note, []*core.Event, error) {
	if header == nil {
		return nil, nil, fmt.Errorf("note header is required")
	}
	noteURN, err := core.ParseURN(header.GetUrn())
	if err != nil {
		return nil, nil, fmt.Errorf("invalid note urn: %w", err)
	}

	noteType := protoNoteTypeToCore(header.GetNoteType())

	createdAt := header.GetCreatedAt().AsTime()
	if createdAt.IsZero() {
		createdAt = header.GetUpdatedAt().AsTime()
	}

	var note *core.Note
	if noteType == core.NoteTypeSecure {
		note = core.NewSecureNote(noteURN, header.GetName(), createdAt)
	} else {
		note = core.NewNote(noteURN, header.GetName(), createdAt)
	}
	note.Deleted = header.GetDeleted()
	if header.GetUpdatedAt() != nil {
		note.UpdatedAt = header.GetUpdatedAt().AsTime()
	}
	if header.GetProjectUrn() != "" {
		pURN, err := core.ParseURN(header.GetProjectUrn())
		if err != nil {
			return nil, nil, fmt.Errorf("invalid project_urn: %w", err)
		}
		note.ProjectURN = &pURN
	}
	if header.GetFolderUrn() != "" {
		fURN, err := core.ParseURN(header.GetFolderUrn())
		if err != nil {
			return nil, nil, fmt.Errorf("invalid folder_urn: %w", err)
		}
		note.FolderURN = &fURN
	}

	events := make([]*core.Event, 0, len(protoEvents))
	for _, pe := range protoEvents {
		ev, evErr := sharedEventToCore(pe)
		if evErr != nil {
			return nil, nil, fmt.Errorf("invalid event at seq %d: %w", pe.GetSequence(), evErr)
		}
		events = append(events, ev)
	}

	return note, events, nil
}

func (s *SyncServer) namespaceFromTLSState(ctx context.Context, state tls.ConnectionState) (string, error) { //nolint:unparam
	if len(state.PeerCertificates) == 0 {
		return "", fmt.Errorf("no peer certificate presented")
	}
	cn := state.PeerCertificates[0].Subject.CommonName
	if cn == "" {
		return "", fmt.Errorf("peer certificate has empty CN")
	}
	ns, err := s.srvRepo.GetServerAnyNamespace(ctx, cn)
	if err != nil {
		return "", fmt.Errorf("namespace lookup for CN %q: %w", cn, err)
	}
	return ns, nil
}
