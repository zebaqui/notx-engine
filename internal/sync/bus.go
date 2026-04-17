// Package sync provides the real-time event sync bus.
//
// On each local write the Bus enqueues the note URN and the background worker
// sends it over a long-lived bidirectional SyncStream gRPC connection. On any
// connection failure the stream is torn down and re-established with exponential
// backoff (1s → 2s → 4s … max 30s). Pending writes are retried via the 30-second
// backlog sweep so no events are lost during offline periods.
package sync

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
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
	// ReceiveNote stores a note and its events received from the cloud.
	ReceiveNote(ctx context.Context, note *core.Note, events []*core.Event) error
}

// Bus is a real-time event sync bus.
//
// It maintains a persistent SyncStream gRPC connection to the cloud authority.
// Local writes are pushed immediately over the stream; cloud notifications
// (NoteChangedNotif) are handled by sending a pull request back and storing the
// received note locally. On any stream error the connection is re-established
// with exponential backoff.
type Bus struct {
	ch      chan string // buffered channel of note URNs to push
	cfg     *config.Config
	repo    BusRepo
	log     *slog.Logger
	stopCh  chan struct{}
	stopped chan struct{}

	// stream state — owned by streamLoop, protected by streamMu
	streamMu   sync.Mutex
	streamConn *grpcclient.Conn
	stream     pb.SyncService_SyncStreamClient
}

// New creates a Bus and starts the background worker goroutines.
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
	go b.streamLoop()
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

// Stop signals the background goroutines and waits for them to exit.
func (b *Bus) Stop() {
	close(b.stopCh)
	<-b.stopped
}

// ─────────────────────────────────────────────────────────────────────────────
// loop — main worker: drains ch and runs the 30s backlog sweep
// ─────────────────────────────────────────────────────────────────────────────

func (b *Bus) loop() {
	defer close(b.stopped)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case noteURN := <-b.ch:
			b.sendNote(noteURN)
		case <-ticker.C:
			urns, err := b.repo.ListSyncPending()
			if err != nil {
				b.log.Debug("sync_bus: list pending failed", "err", err)
				continue
			}
			for _, urn := range urns {
				b.sendNote(urn)
			}
		case <-b.stopCh:
			b.closeStream()
			return
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// streamLoop — maintains the persistent SyncStream connection with backoff
// ─────────────────────────────────────────────────────────────────────────────

func (b *Bus) streamLoop() {
	backoff := time.Second
	const maxBackoff = 30 * time.Second

	for {
		select {
		case <-b.stopCh:
			return
		default:
		}

		conn, stream, err := b.dialStream()
		if err != nil {
			b.log.Debug("sync_bus: dial stream failed, retrying",
				"err", err,
				"backoff", backoff,
			)
			select {
			case <-time.After(backoff):
			case <-b.stopCh:
				return
			}
			backoff = min(backoff*2, maxBackoff)
			continue
		}

		// Connection established — reset backoff.
		backoff = time.Second
		b.setStream(conn, stream)
		b.log.Info("sync_bus: SyncStream connected",
			"peer", b.cfg.Pairing.PeerAuthority,
		)

		// Run the receive loop until it returns (stream error or stop).
		b.recvLoop(stream)

		// Clean up — recvLoop returned because the stream errored or was closed.
		b.closeStream()

		select {
		case <-b.stopCh:
			return
		default:
		}

		b.log.Debug("sync_bus: stream disconnected, reconnecting",
			"backoff", backoff,
		)
		select {
		case <-time.After(backoff):
		case <-b.stopCh:
			return
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

// dialStream dials the cloud authority with mTLS and opens a SyncStream.
// On success it sends an initial ping so the server can verify the stream.
func (b *Bus) dialStream() (*grpcclient.Conn, pb.SyncService_SyncStreamClient, error) {
	certFile := filepath.Join(b.cfg.Pairing.PeerCertDir, "server.crt")
	keyFile := filepath.Join(b.cfg.Pairing.PeerCertDir, "server.key")
	caFile := filepath.Join(b.cfg.Pairing.PeerCertDir, "ca.crt")

	clientCert, err := grpcclient.LoadClientCert(certFile, keyFile)
	if err != nil {
		return nil, nil, err
	}
	caPool, err := grpcclient.LoadCAPool(caFile)
	if err != nil {
		return nil, nil, err
	}
	conn, err := grpcclient.DialMTLS(b.cfg.Pairing.PeerAuthority, clientCert, caPool)
	if err != nil {
		return nil, nil, err
	}

	ctx := context.Background() // stream lives as long as the bus
	syncClient := pb.NewSyncServiceClient(conn.Raw())
	stream, err := syncClient.SyncStream(ctx)
	if err != nil {
		conn.Close()
		return nil, nil, err
	}

	// Send initial ping.
	ping := &pb.SyncStreamMessage{
		Ping: &pb.SyncPing{TimestampMs: time.Now().UnixMilli()},
	}
	if err := stream.Send(ping); err != nil {
		conn.Close()
		return nil, nil, err
	}

	return conn, stream, nil
}

// recvLoop reads messages from stream until it errors or the bus is stopped.
// Cloud-pushed NoteChangedNotif messages cause a pull request to be sent back.
func (b *Bus) recvLoop(stream pb.SyncService_SyncStreamClient) {
	for {
		select {
		case <-b.stopCh:
			return
		default:
		}

		msg, err := stream.Recv()
		if err == io.EOF {
			return
		}
		if err != nil {
			b.log.Debug("sync_bus: recv error", "err", err)
			return
		}

		switch {
		case msg.NoteChanged != nil:
			b.handleNoteChanged(stream, msg.NoteChanged)

		case msg.PushAck != nil:
			// Acknowledge a note push we sent. Clear the pending row if no error.
			ack := msg.PushAck
			if ack.Error != "" {
				b.log.Warn("sync_bus: push ack error",
					"urn", ack.NoteUrn,
					"err", ack.Error,
				)
			} else {
				if err := b.repo.ClearSyncPending(ack.NoteUrn); err != nil {
					b.log.Debug("sync_bus: clear pending after ack", "urn", ack.NoteUrn, "err", err)
				}
			}

		case msg.NotePush != nil:
			// Cloud is pushing a note to us (response to our pull request).
			b.handleNotePush(msg.NotePush)

		case msg.Ping != nil:
			// Pong — no action needed; the server echoed back our ping timestamp.
		}
	}
}

// handleNoteChanged is called when the cloud notifies us that a note changed.
// We determine our local head sequence and send a SyncPullRequest for any
// missing events.
func (b *Bus) handleNoteChanged(stream pb.SyncService_SyncStreamClient, notif *pb.NoteChangedNotif) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	localNote, err := b.repo.Get(ctx, notif.NoteUrn)
	localHeadSeq := int32(0)
	if err == nil && localNote != nil {
		localHeadSeq = int32(localNote.HeadSequence())
	}

	// If we're already at or ahead of the cloud's head, nothing to do.
	if localHeadSeq >= notif.HeadSeq {
		return
	}

	// Send a pull request to fetch events from our local head + 1 onwards.
	pullReq := &pb.SyncStreamMessage{
		PullRequest: &pb.SyncPullRequest{
			NoteUrn: notif.NoteUrn,
			FromSeq: localHeadSeq + 1,
		},
	}
	if sendErr := stream.Send(pullReq); sendErr != nil {
		b.log.Debug("sync_bus: send pull request failed",
			"urn", notif.NoteUrn,
			"err", sendErr,
		)
		// Mark as pending so the backlog sweep retries.
		_ = b.repo.MarkSyncPending(notif.NoteUrn)
	}
}

// handleNotePush stores a note pushed by the cloud (in response to our pull request).
func (b *Bus) handleNotePush(push *pb.SyncNoteMessage) {
	if push.Header == nil {
		return
	}
	urn := push.Header.Urn

	note, events, err := protoToCoreNote(push)
	if err != nil {
		b.log.Warn("sync_bus: proto→core failed", "urn", urn, "err", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := b.repo.ReceiveNote(ctx, note, events); err != nil {
		b.log.Warn("sync_bus: receive note failed", "urn", urn, "err", err)
		// Mark pending so the next sweep can retry via the old one-shot path.
		_ = b.repo.MarkSyncPending(urn)
		return
	}

	b.log.Debug("sync_bus: received note from cloud", "urn", urn, "events", len(events))
}

// ─────────────────────────────────────────────────────────────────────────────
// sendNote — push a local note to the cloud over the persistent stream
// ─────────────────────────────────────────────────────────────────────────────

// sendNote builds a SyncNoteMessage for noteURN and sends it on the persistent
// stream. On any error the pending_sync row is left so the backlog sweep retries.
func (b *Bus) sendNote(noteURN string) {
	stream := b.getStream()
	if stream == nil {
		// Stream not yet established — pending row will be retried by backlog sweep.
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

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

	msg := &pb.SyncStreamMessage{
		NotePush: &pb.SyncNoteMessage{
			Header: coreNoteHeaderToProto(note),
			Events: coreEventsToProto(events),
		},
	}
	if err := stream.Send(msg); err != nil {
		b.log.Debug("sync_bus: send note_push failed", "urn", noteURN, "err", err)
		// Invalidate the stream so streamLoop reconnects.
		b.invalidateStream()
		return
	}
	// Ack is received asynchronously in recvLoop — pending row cleared there.
}

// ─────────────────────────────────────────────────────────────────────────────
// stream state helpers
// ─────────────────────────────────────────────────────────────────────────────

func (b *Bus) setStream(conn *grpcclient.Conn, stream pb.SyncService_SyncStreamClient) {
	b.streamMu.Lock()
	defer b.streamMu.Unlock()
	b.streamConn = conn
	b.stream = stream
}

func (b *Bus) getStream() pb.SyncService_SyncStreamClient {
	b.streamMu.Lock()
	defer b.streamMu.Unlock()
	return b.stream
}

func (b *Bus) invalidateStream() {
	b.streamMu.Lock()
	defer b.streamMu.Unlock()
	b.stream = nil
	// Do NOT close the conn here — streamLoop owns the conn lifecycle.
}

func (b *Bus) closeStream() {
	b.streamMu.Lock()
	defer b.streamMu.Unlock()
	if b.stream != nil {
		_ = b.stream.CloseSend()
		b.stream = nil
	}
	if b.streamConn != nil {
		_ = b.streamConn.Close()
		b.streamConn = nil
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Conversion helpers (core ↔ proto)
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

// protoToCoreNote converts a SyncNoteMessage to a core.Note + []*core.Event.
func protoToCoreNote(msg *pb.SyncNoteMessage) (*core.Note, []*core.Event, error) {
	h := msg.Header

	noteURN, err := core.ParseURN(h.Urn)
	if err != nil {
		return nil, nil, err
	}

	var noteType core.NoteType
	if h.NoteType == pb.NoteType_NOTE_TYPE_SECURE {
		noteType = core.NoteTypeSecure
	}

	createdAt := time.Now().UTC()
	if h.CreatedAt != nil {
		createdAt = h.CreatedAt.AsTime()
	}

	var note *core.Note
	if noteType == core.NoteTypeSecure {
		note = core.NewSecureNote(noteURN, h.Name, createdAt)
	} else {
		note = core.NewNote(noteURN, h.Name, createdAt)
	}
	note.Deleted = h.Deleted
	if h.UpdatedAt != nil {
		note.UpdatedAt = h.UpdatedAt.AsTime()
	}
	if h.ProjectUrn != "" {
		pURN, err := core.ParseURN(h.ProjectUrn)
		if err != nil {
			return nil, nil, err
		}
		note.ProjectURN = &pURN
	}
	if h.FolderUrn != "" {
		fURN, err := core.ParseURN(h.FolderUrn)
		if err != nil {
			return nil, nil, err
		}
		note.FolderURN = &fURN
	}

	events := make([]*core.Event, 0, len(msg.Events))
	for _, pe := range msg.Events {
		noteURNParsed, err := core.ParseURN(pe.NoteUrn)
		if err != nil {
			return nil, nil, err
		}
		authorURN, err := core.ParseURN(pe.AuthorUrn)
		if err != nil {
			return nil, nil, err
		}
		evURN, err := core.ParseURN(pe.Urn)
		if err != nil {
			return nil, nil, err
		}
		createdAt := time.Now().UTC()
		if pe.CreatedAt != nil {
			createdAt = pe.CreatedAt.AsTime()
		}
		entries := make([]core.LineEntry, 0, len(pe.Entries))
		for _, le := range pe.Entries {
			entries = append(entries, core.LineEntry{
				Op:         core.LineOp(le.Op),
				LineNumber: int(le.LineNumber),
				Content:    le.Content,
			})
		}
		ev := &core.Event{
			URN:       evURN,
			NoteURN:   noteURNParsed,
			Sequence:  int(pe.Sequence),
			AuthorURN: authorURN,
			CreatedAt: createdAt,
			Entries:   entries,
		}
		events = append(events, ev)
	}

	return note, events, nil
}

// min returns the smaller of two durations.
func min(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
