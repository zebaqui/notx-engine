// Package sync provides the real-time event sync bus.
// When an event is written locally the Bus immediately attempts to push the
// full note (header + events) to the cloud via SyncService.SyncNotes over an
// mTLS gRPC channel. On failure the pending_sync row is left intact so the
// 30-second backlog sweep retries it later.
package sync

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/zebaqui/notx-engine/config"
	"github.com/zebaqui/notx-engine/core"
	"github.com/zebaqui/notx-engine/internal/grpcclient"
	pb "github.com/zebaqui/notx-engine/proto"
)

// BusRepo is the subset of the sqlite Provider that Bus needs.
type BusRepo interface {
	// Get returns the note header.
	Get(ctx context.Context, urn string) (*core.Note, error)
	// Events returns all events for a note from fromSequence.
	Events(ctx context.Context, noteURN string, fromSequence int) ([]*core.Event, error)
	// ClearSyncPending removes the pending_sync row after successful push.
	ClearSyncPending(noteURN string) error
	// ListSyncPending returns all note URNs with pending rows.
	ListSyncPending() ([]string, error)
	// MarkSyncPending upserts a pending_sync row.
	MarkSyncPending(noteURN string) error
}

// Bus is a real-time event sync bus. On each local write it attempts to push
// the note to the cloud immediately; on failure the pending_sync row stays so
// the 30-second sweep can retry.
type Bus struct {
	ch      chan string // buffered channel of note URNs to push
	cfg     *config.Config
	repo    BusRepo
	log     *slog.Logger
	stopCh  chan struct{}
	stopped chan struct{}
}

// New creates a Bus and starts the background worker goroutine.
func New(cfg *config.Config, repo BusRepo, log *slog.Logger) *Bus {
	if log == nil {
		log = slog.Default()
	}
	b := &Bus{
		ch:      make(chan string, 256),
		cfg:     cfg,
		repo:    repo,
		log:     log,
		stopCh:  make(chan struct{}),
		stopped: make(chan struct{}),
	}
	go b.loop()
	return b
}

// Notify enqueues noteURN for an immediate push attempt. Non-blocking: if the
// channel is full the pending_sync row (already written by AppendEvent) will
// be picked up by the next 30-second sweep instead.
func (b *Bus) Notify(noteURN string) {
	select {
	case b.ch <- noteURN:
	default:
		// channel full — backlog sweep will handle it
	}
}

// Stop signals the background goroutine and waits for it to exit.
func (b *Bus) Stop() {
	close(b.stopCh)
	<-b.stopped
}

// loop is the background worker goroutine.
func (b *Bus) loop() {
	defer close(b.stopped)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case noteURN := <-b.ch:
			b.tryPush(noteURN)
		case <-ticker.C:
			urns, err := b.repo.ListSyncPending()
			if err != nil {
				b.log.Debug("sync_bus: list pending failed", "err", err)
				continue
			}
			for _, urn := range urns {
				b.tryPush(urn)
			}
		case <-b.stopCh:
			return
		}
	}
}

// tryPush dials the cloud, sends the note, and clears the pending_sync row on
// success. All errors are logged at debug level so offline periods don't spam.
func (b *Bus) tryPush(noteURN string) {
	certFile := filepath.Join(b.cfg.Pairing.PeerCertDir, "server.crt")
	keyFile := filepath.Join(b.cfg.Pairing.PeerCertDir, "server.key")
	caFile := filepath.Join(b.cfg.Pairing.PeerCertDir, "ca.crt")

	clientCert, err := grpcclient.LoadClientCert(certFile, keyFile)
	if err != nil {
		b.log.Debug("sync_bus: load client cert", "err", err)
		return
	}
	caPool, err := grpcclient.LoadCAPool(caFile)
	if err != nil {
		b.log.Debug("sync_bus: load CA pool", "err", err)
		return
	}
	conn, err := grpcclient.DialMTLS(b.cfg.Pairing.PeerAuthority, clientCert, caPool)
	if err != nil {
		b.log.Debug("sync_bus: dial mTLS", "addr", b.cfg.Pairing.PeerAuthority, "err", err)
		return
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	syncClient := pb.NewSyncServiceClient(conn.Raw())
	stream, err := syncClient.SyncNotes(ctx)
	if err != nil {
		b.log.Debug("sync_bus: open SyncNotes stream", "err", err)
		return
	}

	note, err := b.repo.Get(ctx, noteURN)
	if err != nil {
		b.log.Debug("sync_bus: get note", "urn", noteURN, "err", err)
		return
	}
	events, err := b.repo.Events(ctx, noteURN, 1)
	if err != nil {
		b.log.Debug("sync_bus: get events", "urn", noteURN, "err", err)
		return
	}

	msg := &pb.SyncNoteMessage{
		Header: coreNoteHeaderToProto(note),
		Events: coreEventsToProto(events),
	}
	if err := stream.Send(msg); err != nil {
		b.log.Debug("sync_bus: send SyncNoteMessage", "urn", noteURN, "err", err)
		return
	}
	if err := stream.CloseSend(); err != nil {
		b.log.Debug("sync_bus: CloseSend", "urn", noteURN, "err", err)
		return
	}

	// Drain responses until EOF.
	for {
		_, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			b.log.Debug("sync_bus: recv result", "urn", noteURN, "err", err)
			return
		}
	}

	// Success — remove the pending row.
	if err := b.repo.ClearSyncPending(noteURN); err != nil {
		b.log.Debug("sync_bus: clear pending", "urn", noteURN, "err", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Conversion helpers (core → proto)
// ─────────────────────────────────────────────────────────────────────────────

func coreNoteHeaderToProto(n *core.Note) *pb.NoteHeader {
	h := &pb.NoteHeader{
		Urn:       n.URN.String(),
		Name:      n.Name,
		NoteType:  coreNoteTypeToProto(n.NoteType),
		Deleted:   n.Deleted,
		CreatedAt: timestamppb.New(n.CreatedAt),
		UpdatedAt: timestamppb.New(n.UpdatedAt),
	}
	if n.ProjectURN != nil {
		h.ProjectUrn = n.ProjectURN.String()
	}
	if n.FolderURN != nil {
		h.FolderUrn = n.FolderURN.String()
	}
	return h
}

func coreNoteTypeToProto(t core.NoteType) pb.NoteType {
	switch t {
	case core.NoteTypeSecure:
		return pb.NoteType_NOTE_TYPE_SECURE
	default:
		return pb.NoteType_NOTE_TYPE_NORMAL
	}
}

func coreEventsToProto(events []*core.Event) []*pb.Event {
	out := make([]*pb.Event, 0, len(events))
	for _, ev := range events {
		out = append(out, coreEventToProto(ev))
	}
	return out
}

func coreEventToProto(ev *core.Event) *pb.Event {
	p := &pb.Event{
		Urn:       ev.URN.String(),
		NoteUrn:   ev.NoteURN.String(),
		Sequence:  int32(ev.Sequence),
		AuthorUrn: ev.AuthorURN.String(),
		CreatedAt: timestamppb.New(ev.CreatedAt),
	}
	for _, e := range ev.Entries {
		p.Entries = append(p.Entries, &pb.LineEntry{
			Op:         int32(e.Op),
			LineNumber: int32(e.LineNumber),
			Content:    e.Content,
		})
	}
	return p
}
