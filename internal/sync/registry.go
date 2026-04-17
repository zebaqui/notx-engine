package sync

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/zebaqui/notx-engine/core"
	pb "github.com/zebaqui/notx-engine/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// NoteReceiver is the subset of repo.NoteRepository that SyncStream needs on
// the cloud (authority) side. *postgres.TenantScopedProvider satisfies this
// interface structurally.
type NoteReceiver interface {
	ReceiveSharedNote(ctx context.Context, note *core.Note, events []*core.Event) error
	Get(ctx context.Context, urn string) (*core.Note, error)
	Events(ctx context.Context, noteURN string, fromSequence int) ([]*core.Event, error)
}

// TenantProvider resolves a NoteReceiver for a given namespace.
// Implement this with a thin adapter over *postgres.PgProvider.
type TenantProvider interface {
	ForTenant(namespace string) NoteReceiver
}

// StreamRegistry tracks active SyncStream connections keyed by namespace.
type StreamRegistry struct {
	mu      sync.RWMutex
	streams map[string]map[string]chan *pb.SyncStreamMessage // namespace → id → sendCh
}

// NewStreamRegistry returns a ready-to-use StreamRegistry.
func NewStreamRegistry() *StreamRegistry {
	return &StreamRegistry{
		streams: make(map[string]map[string]chan *pb.SyncStreamMessage),
	}
}

// Register adds a new stream for namespace. Returns (id, sendCh).
// The caller drains sendCh and sends to the gRPC stream; calls Deregister on close.
func (r *StreamRegistry) Register(namespace string) (string, <-chan *pb.SyncStreamMessage) {
	id := uuid.New().String()
	ch := make(chan *pb.SyncStreamMessage, 64)
	r.mu.Lock()
	if r.streams[namespace] == nil {
		r.streams[namespace] = make(map[string]chan *pb.SyncStreamMessage)
	}
	r.streams[namespace][id] = ch
	r.mu.Unlock()
	return id, ch
}

// Deregister removes the stream entry and closes its channel.
func (r *StreamRegistry) Deregister(namespace, id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if m, ok := r.streams[namespace]; ok {
		if ch, exists := m[id]; exists {
			close(ch)
			delete(m, id)
		}
		if len(m) == 0 {
			delete(r.streams, namespace)
		}
	}
}

// Notify sends a NoteChangedNotif to all streams registered for namespace.
// Non-blocking: drops if a stream's channel is full.
func (r *StreamRegistry) Notify(namespace, noteURN string, headSeq int32, updatedAt time.Time) {
	msg := &pb.SyncStreamMessage{
		NoteChanged: &pb.NoteChangedNotif{
			NoteUrn:   noteURN,
			HeadSeq:   headSeq,
			UpdatedAt: timestamppb.New(updatedAt),
		},
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, ch := range r.streams[namespace] {
		select {
		case ch <- msg:
		default: // channel full — backlog sweep will catch it
		}
	}
}
