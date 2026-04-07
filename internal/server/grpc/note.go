package grpc

import (
	"context"
	b64 "encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/zebaqui/notx-engine/core"
	pb "github.com/zebaqui/notx-engine/proto"
	"github.com/zebaqui/notx-engine/repo"
)

// NoteServer implements pb.NoteServiceServer backed by a NoteRepository.
type NoteServer struct {
	pb.UnimplementedNoteServiceServer
	repo        repo.NoteRepository
	contextRepo repo.ContextRepository // may be nil when context layer is disabled
	defaultPage int
	maxPage     int
}

// NewNoteServer returns a ready-to-register NoteServer.
func NewNoteServer(r repo.NoteRepository, defaultPage, maxPage int) *NoteServer {
	if defaultPage <= 0 {
		defaultPage = 50
	}
	if maxPage <= 0 {
		maxPage = 200
	}
	return &NoteServer{repo: r, defaultPage: defaultPage, maxPage: maxPage}
}

// NewNoteServerWithContext is like NewNoteServer but also wires the context
// repository so UpdateNote can trigger IndexNoteIntoProject on project changes.
func NewNoteServerWithContext(r repo.NoteRepository, contextRepo repo.ContextRepository, defaultPage, maxPage int) *NoteServer {
	s := NewNoteServer(r, defaultPage, maxPage)
	s.contextRepo = contextRepo
	return s
}

// ── GetNote ──────────────────────────────────────────────────────────────────

func (s *NoteServer) GetNote(ctx context.Context, req *pb.GetNoteRequest) (*pb.GetNoteResponse, error) {
	if req.Urn == "" {
		return nil, status.Error(codes.InvalidArgument, "urn is required")
	}

	note, err := s.repo.Get(ctx, req.Urn)
	if err != nil {
		return nil, repoErrToStatus(err, req.Urn)
	}

	events, err := s.repo.Events(ctx, req.Urn, 1)
	if err != nil {
		return nil, repoErrToStatus(err, req.Urn)
	}

	pbEvents := make([]*pb.Event, 0, len(events))
	for _, ev := range events {
		pbEvents = append(pbEvents, coreEventToProto(ev))
	}

	return &pb.GetNoteResponse{
		Header: coreNoteToHeader(note),
		Events: pbEvents,
	}, nil
}

// ── ListNotes ────────────────────────────────────────────────────────────────

func (s *NoteServer) ListNotes(ctx context.Context, req *pb.ListNotesRequest) (*pb.ListNotesResponse, error) {
	pageSize := int(req.PageSize)
	if pageSize <= 0 {
		pageSize = s.defaultPage
	}
	if pageSize > s.maxPage {
		pageSize = s.maxPage
	}

	opts := repo.ListOptions{
		ProjectURN:     req.ProjectUrn,
		FolderURN:      req.FolderUrn,
		IncludeDeleted: req.IncludeDeleted,
		PageSize:       pageSize,
		PageToken:      req.PageToken,
	}

	if req.NoteType != pb.NoteType_NOTE_TYPE_UNSPECIFIED {
		opts.FilterByType = true
		opts.NoteTypeFilter = protoNoteTypeToCore(req.NoteType)
	}

	result, err := s.repo.List(ctx, opts)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list notes: %v", err)
	}

	headers := make([]*pb.NoteHeader, 0, len(result.Notes))
	for _, n := range result.Notes {
		headers = append(headers, coreNoteToHeader(n))
	}

	return &pb.ListNotesResponse{
		Notes:         headers,
		NextPageToken: result.NextPageToken,
	}, nil
}

// ── CreateNote ───────────────────────────────────────────────────────────────

func (s *NoteServer) CreateNote(ctx context.Context, req *pb.CreateNoteRequest) (*pb.CreateNoteResponse, error) {
	if req.Header == nil {
		return nil, status.Error(codes.InvalidArgument, "header is required")
	}
	if req.Header.Urn == "" {
		return nil, status.Error(codes.InvalidArgument, "header.urn is required")
	}
	if req.Header.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "header.name is required")
	}

	note, err := protoHeaderToCoreNote(req.Header)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid header: %v", err)
	}

	if err := s.repo.Create(ctx, note); err != nil {
		if errors.Is(err, repo.ErrAlreadyExists) {
			return nil, status.Errorf(codes.AlreadyExists, "note %q already exists", req.Header.Urn)
		}
		return nil, status.Errorf(codes.Internal, "create note: %v", err)
	}

	return &pb.CreateNoteResponse{
		Header: coreNoteToHeader(note),
	}, nil
}

// ── DeleteNote ───────────────────────────────────────────────────────────────

func (s *NoteServer) DeleteNote(ctx context.Context, req *pb.DeleteNoteRequest) (*pb.DeleteNoteResponse, error) {
	if req.Urn == "" {
		return nil, status.Error(codes.InvalidArgument, "urn is required")
	}

	if err := s.repo.Delete(ctx, req.Urn); err != nil {
		return nil, repoErrToStatus(err, req.Urn)
	}

	return &pb.DeleteNoteResponse{Deleted: true}, nil
}

// ── AppendEvent ──────────────────────────────────────────────────────────────

func (s *NoteServer) AppendEvent(ctx context.Context, req *pb.AppendEventRequest) (*pb.AppendEventResponse, error) {
	if req.Event == nil {
		return nil, status.Error(codes.InvalidArgument, "event is required")
	}
	if req.Event.NoteUrn == "" {
		return nil, status.Error(codes.InvalidArgument, "event.note_urn is required")
	}
	if req.Event.Sequence <= 0 {
		return nil, status.Error(codes.InvalidArgument, "event.sequence must be >= 1")
	}
	if req.Event.AuthorUrn == "" {
		return nil, status.Error(codes.InvalidArgument, "event.author_urn is required")
	}

	ev, err := protoEventToCore(req.Event)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid event: %v", err)
	}

	opts := repo.AppendEventOptions{
		ExpectSequence: int(req.Event.Sequence),
	}

	if err := s.repo.AppendEvent(ctx, ev, opts); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "note %q not found", req.Event.NoteUrn)
		}
		if errors.Is(err, repo.ErrSequenceConflict) {
			return nil, status.Errorf(codes.Aborted, "sequence conflict: %v", err)
		}
		return nil, status.Errorf(codes.Internal, "append event: %v", err)
	}

	return &pb.AppendEventResponse{Sequence: req.Event.Sequence}, nil
}

// ── StreamEvents ─────────────────────────────────────────────────────────────

func (s *NoteServer) StreamEvents(req *pb.StreamEventsRequest, stream pb.NoteService_StreamEventsServer) error {
	if req.NoteUrn == "" {
		return status.Error(codes.InvalidArgument, "note_urn is required")
	}

	fromSeq := int(req.FromSequence)
	if fromSeq < 1 {
		fromSeq = 1
	}

	events, err := s.repo.Events(stream.Context(), req.NoteUrn, fromSeq)
	if err != nil {
		return repoErrToStatus(err, req.NoteUrn)
	}

	for _, ev := range events {
		if err := stream.Send(&pb.StreamEventsResponse{Event: coreEventToProto(ev)}); err != nil {
			return status.Errorf(codes.Unavailable, "stream send: %v", err)
		}
	}

	return nil
}

// ── SearchNotes ──────────────────────────────────────────────────────────────

func (s *NoteServer) SearchNotes(ctx context.Context, req *pb.SearchNotesRequest) (*pb.SearchNotesResponse, error) {
	if strings.TrimSpace(req.Query) == "" {
		return nil, status.Error(codes.InvalidArgument, "query is required")
	}

	pageSize := int(req.PageSize)
	if pageSize <= 0 {
		pageSize = s.defaultPage
	}
	if pageSize > s.maxPage {
		pageSize = s.maxPage
	}

	opts := repo.SearchOptions{
		Query:     req.Query,
		PageSize:  pageSize,
		PageToken: req.PageToken,
	}

	results, err := s.repo.Search(ctx, opts)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "search: %v", err)
	}

	pbResults := make([]*pb.SearchResult, 0, len(results.Results))
	for _, r := range results.Results {
		pbResults = append(pbResults, &pb.SearchResult{
			Header:  coreNoteToHeader(r.Note),
			Excerpt: r.Excerpt,
		})
	}

	return &pb.SearchNotesResponse{
		Results:       pbResults,
		NextPageToken: results.NextPageToken,
	}, nil
}

// ── ShareSecureNote ──────────────────────────────────────────────────────────

// ShareSecureNote merges per-device wrapped CEKs into every event stored for
// the given secure note.
//
// The client calls this after it has:
//  1. Fetched the public key of each recipient device from
//     GET /v1/devices/:urn (or DeviceService.GetDevicePublicKey).
//  2. Wrapped (encrypted) the note's CEK with each recipient's public key
//     using asymmetric encryption (e.g. ECIES / X25519).
//  3. Assembled the resulting map[deviceURN → wrappedCEK] and sent it here.
//
// The server writes the wrapped keys into the WrappedKeys field of every
// event for the note and returns the count of events updated.
func (s *NoteServer) ShareSecureNote(ctx context.Context, req *pb.ShareSecureNoteRequest) (*pb.ShareSecureNoteResponse, error) {
	if req.NoteUrn == "" {
		return nil, status.Error(codes.InvalidArgument, "note_urn is required")
	}
	if len(req.WrappedKeys) == 0 {
		return nil, status.Error(codes.InvalidArgument, "wrapped_keys must not be empty")
	}

	// Confirm the note exists and is of type secure before writing anything.
	note, err := s.repo.Get(ctx, req.NoteUrn)
	if err != nil {
		return nil, shareRepoErrToStatus(err, req.NoteUrn)
	}
	if note.NoteType != core.NoteTypeSecure {
		return nil, status.Errorf(codes.InvalidArgument,
			"note %q is not a secure note; ShareSecureNote only applies to secure notes",
			req.NoteUrn)
	}

	// The proto WrappedKeys map carries raw bytes (the wrapped CEK blob). No
	// base64 decoding is needed here — the gRPC binary encoding handles the
	// bytes field directly. We copy directly into a Go map[string][]byte.
	wrappedKeys := make(map[string][]byte, len(req.WrappedKeys))
	for deviceURN, blob := range req.WrappedKeys {
		if deviceURN == "" {
			return nil, status.Error(codes.InvalidArgument, "wrapped_keys contains an empty device URN key")
		}
		if len(blob) == 0 {
			return nil, status.Errorf(codes.InvalidArgument,
				"wrapped_keys[%q] has an empty key blob", deviceURN)
		}
		wrappedKeys[deviceURN] = blob
	}

	count, err := s.repo.UpdateEventWrappedKeys(ctx, req.NoteUrn, wrappedKeys)
	if err != nil {
		return nil, shareRepoErrToStatus(err, req.NoteUrn)
	}

	return &pb.ShareSecureNoteResponse{
		EventsUpdated: int32(count),
	}, nil
}

// ── ReceiveSharedNote ─────────────────────────────────────────────────────────

// ReceiveSharedNote accepts a note header and its full event stream forwarded
// from a paired server and stores them locally.
//
// This is the server-side entry point of the cross-server note-sharing flow:
//
//	Client A  →  Server A  →  (mTLS / HTTP push)  →  Server B
//	                                                       ↓
//	                                                   Client B reads
//
// For secure notes the events contain only ciphertext; the server never sees
// the plaintext CEK. Client B retrieves the note's events and decrypts them
// locally using its private key after Server A (or Client A directly) has
// called ShareSecureNote to deliver Client B's wrapped CEK to Server B.
//
// The operation is idempotent: events with a sequence number at or below the
// current local head are skipped, so the call can be retried safely.
func (s *NoteServer) ReceiveSharedNote(ctx context.Context, req *pb.ReceiveSharedNoteRequest) (*pb.ReceiveSharedNoteResponse, error) {
	if req.Header == nil {
		return nil, status.Error(codes.InvalidArgument, "header is required")
	}
	if req.Header.Urn == "" {
		return nil, status.Error(codes.InvalidArgument, "header.urn is required")
	}
	if req.Header.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "header.name is required")
	}

	// Parse the note header.
	noteURN, err := core.ParseURN(req.Header.Urn)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid header.urn: %v", err)
	}

	noteType := protoNoteTypeToCore(req.Header.NoteType)

	createdAt := time.Now().UTC()
	if req.Header.CreatedAt != nil {
		createdAt = req.Header.CreatedAt.AsTime()
	}

	var note *core.Note
	if noteType == core.NoteTypeSecure {
		note = core.NewSecureNote(noteURN, req.Header.Name, createdAt)
	} else {
		note = core.NewNote(noteURN, req.Header.Name, createdAt)
	}
	note.Deleted = req.Header.Deleted

	if req.Header.UpdatedAt != nil {
		note.UpdatedAt = req.Header.UpdatedAt.AsTime()
	}
	if req.Header.ProjectUrn != "" {
		projURN, err := core.ParseURN(req.Header.ProjectUrn)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid header.project_urn: %v", err)
		}
		note.ProjectURN = &projURN
	}
	if req.Header.FolderUrn != "" {
		folderURN, err := core.ParseURN(req.Header.FolderUrn)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid header.folder_urn: %v", err)
		}
		note.FolderURN = &folderURN
	}

	// Parse and validate the event stream.
	events := make([]*core.Event, 0, len(req.Events))
	for _, evProto := range req.Events {
		ev, err := sharedEventToCore(evProto)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument,
				"invalid event at sequence %d: %v", evProto.GetSequence(), err)
		}
		events = append(events, ev)
	}

	// Persist — idempotent via ReceiveSharedNote semantics.
	if err := s.repo.ReceiveSharedNote(ctx, note, events); err != nil {
		return nil, shareRepoErrToStatus(err, req.Header.Urn)
	}

	return &pb.ReceiveSharedNoteResponse{
		NoteUrn:      req.Header.Urn,
		EventsStored: int32(len(events)),
	}, nil
}

// ── UpdateNote ───────────────────────────────────────────────────────────────

func (s *NoteServer) UpdateNote(ctx context.Context, req *pb.UpdateNoteRequest) (*pb.UpdateNoteResponse, error) {
	if req.Urn == "" {
		return nil, status.Error(codes.InvalidArgument, "urn is required")
	}

	note, err := s.repo.Get(ctx, req.Urn)
	if err != nil {
		return nil, repoErrToStatus(err, req.Urn)
	}

	// Capture the project URN before any mutations so we can detect changes.
	oldProjectURN := ""
	if note.ProjectURN != nil {
		oldProjectURN = note.ProjectURN.String()
	}

	if req.Header != nil {
		if req.Header.Name != "" {
			note.Name = req.Header.Name
		}

		switch req.Header.ProjectUrn {
		case "CLEAR":
			note.ProjectURN = nil
		case "":
			// no-op
		default:
			projURN, err := core.ParseURN(req.Header.ProjectUrn)
			if err != nil {
				return nil, status.Errorf(codes.InvalidArgument, "invalid project_urn: %v", err)
			}
			note.ProjectURN = &projURN
		}

		switch req.Header.FolderUrn {
		case "CLEAR":
			note.FolderURN = nil
		case "":
			// no-op
		default:
			folderURN, err := core.ParseURN(req.Header.FolderUrn)
			if err != nil {
				return nil, status.Errorf(codes.InvalidArgument, "invalid folder_urn: %v", err)
			}
			note.FolderURN = &folderURN
		}
	}

	note.UpdatedAt = time.Now().UTC()

	if err := s.repo.Update(ctx, note); err != nil {
		return nil, repoErrToStatus(err, req.Urn)
	}

	// Determine the final project URN after the update.
	newProjectURN := ""
	if note.ProjectURN != nil {
		newProjectURN = note.ProjectURN.String()
	}

	// If the project assignment changed and the context repo is wired,
	// backfill bursts into the new project asynchronously.
	if s.contextRepo != nil && newProjectURN != "" && newProjectURN != oldProjectURN {
		go func(noteURN, projectURN string) {
			bgCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			n, err := s.contextRepo.IndexNoteIntoProject(bgCtx, noteURN, projectURN)
			if err != nil {
				// Non-fatal — log but don't surface to caller
				_ = err
			}
			_ = n
		}(req.Urn, newProjectURN)
	}

	return &pb.UpdateNoteResponse{Header: coreNoteToHeader(note)}, nil
}

// ── ReplaceContent ───────────────────────────────────────────────────────────

func (s *NoteServer) ReplaceContent(ctx context.Context, req *pb.ReplaceContentRequest) (*pb.ReplaceContentResponse, error) {
	if req.NoteUrn == "" {
		return nil, status.Error(codes.InvalidArgument, "note_urn is required")
	}

	note, err := s.repo.Get(ctx, req.NoteUrn)
	if err != nil {
		return nil, repoErrToStatus(err, req.NoteUrn)
	}

	if note.NoteType == core.NoteTypeSecure {
		return nil, status.Errorf(codes.InvalidArgument,
			"note %q is a secure note; ReplaceContent is not permitted on secure notes", req.NoteUrn)
	}

	oldLines := core.SplitLines(note.Content())
	newLines := core.SplitLines(req.Content)
	entries := core.DiffLines(oldLines, newLines)

	if len(entries) == 0 {
		return &pb.ReplaceContentResponse{
			Sequence: int32(note.HeadSequence()),
			Changed:  false,
			NoteUrn:  req.NoteUrn,
		}, nil
	}

	// Resolve author URN — default to the namespace-scoped anon sentinel.
	noteURN, err := core.ParseURN(req.NoteUrn)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid note_urn: %v", err)
	}

	var authorURN core.URN
	if req.AuthorUrn != "" {
		authorURN, err = core.ParseURN(req.AuthorUrn)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid author_urn: %v", err)
		}
	} else {
		authorURN = core.AnonURN()
	}

	nextSeq := note.HeadSequence() + 1
	eventURN := core.NewURN(core.ObjectTypeEvent)

	ev := &core.Event{
		URN:       eventURN,
		NoteURN:   noteURN,
		Sequence:  nextSeq,
		AuthorURN: authorURN,
		CreatedAt: time.Now().UTC(),
		Entries:   entries,
	}

	opts := repo.AppendEventOptions{
		ExpectSequence: nextSeq,
	}

	if err := s.repo.AppendEvent(ctx, ev, opts); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "note %q not found", req.NoteUrn)
		}
		if errors.Is(err, repo.ErrSequenceConflict) {
			return nil, status.Errorf(codes.Aborted, "sequence conflict: %v", err)
		}
		return nil, status.Errorf(codes.Internal, "append event: %v", err)
	}

	return &pb.ReplaceContentResponse{
		Sequence: int32(nextSeq),
		Changed:  true,
		NoteUrn:  req.NoteUrn,
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Proto ↔ core conversion helpers
// ─────────────────────────────────────────────────────────────────────────────

func coreNoteToHeader(n *core.Note) *pb.NoteHeader {
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

func protoHeaderToCoreNote(h *pb.NoteHeader) (*core.Note, error) {
	urn, err := core.ParseURN(h.Urn)
	if err != nil {
		return nil, fmt.Errorf("invalid urn: %w", err)
	}

	createdAt := time.Now().UTC()
	if h.CreatedAt != nil {
		createdAt = h.CreatedAt.AsTime()
	}

	noteType := protoNoteTypeToCore(h.NoteType)
	var note *core.Note
	if noteType == core.NoteTypeSecure {
		note = core.NewSecureNote(urn, h.Name, createdAt)
	} else {
		note = core.NewNote(urn, h.Name, createdAt)
	}

	note.Deleted = h.Deleted

	if h.ProjectUrn != "" {
		projURN, err := core.ParseURN(h.ProjectUrn)
		if err != nil {
			return nil, fmt.Errorf("invalid project_urn: %w", err)
		}
		note.ProjectURN = &projURN
	}
	if h.FolderUrn != "" {
		folderURN, err := core.ParseURN(h.FolderUrn)
		if err != nil {
			return nil, fmt.Errorf("invalid folder_urn: %w", err)
		}
		note.FolderURN = &folderURN
	}

	return note, nil
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
	if len(ev.WrappedKeys) > 0 {
		p.WrappedKeys = make(map[string][]byte, len(ev.WrappedKeys))
		for deviceURN, blob := range ev.WrappedKeys {
			p.WrappedKeys[deviceURN] = blob
		}
	}
	return p
}

func protoEventToCore(p *pb.Event) (*core.Event, error) {
	noteURN, err := core.ParseURN(p.NoteUrn)
	if err != nil {
		return nil, fmt.Errorf("invalid note_urn: %w", err)
	}
	authorURN, err := core.ParseURN(p.AuthorUrn)
	if err != nil {
		return nil, fmt.Errorf("invalid author_urn: %w", err)
	}

	createdAt := time.Now().UTC()
	if p.CreatedAt != nil {
		createdAt = p.CreatedAt.AsTime()
	}

	var urn core.URN
	if p.Urn != "" {
		if parsed, err := core.ParseURN(p.Urn); err == nil {
			urn = parsed
		}
	}

	entries := make([]core.LineEntry, 0, len(p.Entries))
	for _, e := range p.Entries {
		entries = append(entries, core.LineEntry{
			LineNumber: int(e.LineNumber),
			Op:         core.LineOp(e.Op),
			Content:    e.Content,
		})
	}

	return &core.Event{
		URN:       urn,
		NoteURN:   noteURN,
		Sequence:  int(p.Sequence),
		AuthorURN: authorURN,
		CreatedAt: createdAt,
		Entries:   entries,
	}, nil
}

func coreNoteTypeToProto(t core.NoteType) pb.NoteType {
	switch t {
	case core.NoteTypeSecure:
		return pb.NoteType_NOTE_TYPE_SECURE
	default:
		return pb.NoteType_NOTE_TYPE_NORMAL
	}
}

func protoNoteTypeToCore(t pb.NoteType) core.NoteType {
	switch t {
	case pb.NoteType_NOTE_TYPE_SECURE:
		return core.NoteTypeSecure
	default:
		return core.NoteTypeNormal
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Share helpers
// ─────────────────────────────────────────────────────────────────────────────

// sharedEventToCore converts a pb.Event into a *core.Event.
// It handles both raw-bytes and base64-encoded WrappedKeys (the HTTP adapter
// base64-encodes them; the gRPC path carries raw bytes).
func sharedEventToCore(p *pb.Event) (*core.Event, error) {
	if p.GetNoteUrn() == "" {
		return nil, fmt.Errorf("note_urn is required")
	}
	if p.GetAuthorUrn() == "" {
		return nil, fmt.Errorf("author_urn is required")
	}
	if p.GetSequence() < 1 {
		return nil, fmt.Errorf("sequence must be >= 1")
	}

	noteURN, err := core.ParseURN(p.GetNoteUrn())
	if err != nil {
		return nil, fmt.Errorf("invalid note_urn: %w", err)
	}
	authorURN, err := core.ParseURN(p.GetAuthorUrn())
	if err != nil {
		return nil, fmt.Errorf("invalid author_urn: %w", err)
	}

	createdAt := time.Now().UTC()
	if ts := p.GetCreatedAt(); ts != nil && ts.IsValid() {
		createdAt = ts.AsTime().UTC()
	}

	var evURN core.URN
	if p.GetUrn() != "" {
		if parsed, err := core.ParseURN(p.GetUrn()); err == nil {
			evURN = parsed
		}
	}

	entries := make([]core.LineEntry, 0, len(p.GetEntries()))
	for _, e := range p.GetEntries() {
		entries = append(entries, core.LineEntry{
			LineNumber: int(e.GetLineNumber()),
			Op:         core.LineOp(e.GetOp()),
			Content:    e.GetContent(),
		})
	}

	// Build the per-device wrapped-key map.
	// The WrappedKeys field on SharedEventProto carries map[string][]byte.
	// When the bytes arrive as raw binary (gRPC path) they are used directly.
	// When they arrive as base64 strings encoded into the []byte slice
	// (HTTP-JSON bridge path), we attempt to base64-decode them transparently.
	wrappedKeys := make(map[string][]byte, len(p.GetWrappedKeys()))
	for deviceURN, blob := range p.GetWrappedKeys() {
		if len(blob) == 0 {
			continue
		}
		// Attempt base64 decode: if it succeeds and produces a non-empty result
		// we assume the HTTP bridge encoded it; otherwise use raw bytes.
		if decoded, err := b64.StdEncoding.DecodeString(string(blob)); err == nil && len(decoded) > 0 {
			wrappedKeys[deviceURN] = decoded
		} else {
			wrappedKeys[deviceURN] = blob
		}
	}

	return &core.Event{
		URN:         evURN,
		NoteURN:     noteURN,
		Sequence:    int(p.GetSequence()),
		AuthorURN:   authorURN,
		CreatedAt:   createdAt,
		Entries:     entries,
		WrappedKeys: wrappedKeys,
	}, nil
}

// shareRepoErrToStatus maps NoteRepository errors to gRPC status codes for
// the share handlers.
func shareRepoErrToStatus(err error, urn string) error {
	switch {
	case errors.Is(err, repo.ErrNotFound):
		return status.Errorf(codes.NotFound, "%q not found", urn)
	case errors.Is(err, repo.ErrAlreadyExists):
		return status.Errorf(codes.AlreadyExists, "%q already exists", urn)
	case errors.Is(err, repo.ErrSequenceConflict):
		return status.Errorf(codes.Aborted, "sequence conflict: %v", err)
	default:
		return status.Errorf(codes.Internal, "internal error: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Error mapping
// ─────────────────────────────────────────────────────────────────────────────

func repoErrToStatus(err error, urn string) error {
	switch {
	case errors.Is(err, repo.ErrNotFound):
		return status.Errorf(codes.NotFound, "%q not found", urn)
	case errors.Is(err, repo.ErrAlreadyExists):
		return status.Errorf(codes.AlreadyExists, "%q already exists", urn)
	case errors.Is(err, repo.ErrSequenceConflict):
		return status.Errorf(codes.Aborted, "sequence conflict: %v", err)
	case errors.Is(err, repo.ErrNoteTypeImmutable):
		return status.Errorf(codes.InvalidArgument, "note_type is immutable: %v", err)
	case errors.Is(err, repo.ErrInvalidURN):
		return status.Errorf(codes.InvalidArgument, "invalid URN: %v", err)
	default:
		return status.Errorf(codes.Internal, "internal error: %v", err)
	}
}
