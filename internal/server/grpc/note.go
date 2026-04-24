package grpc

import (
	"context"
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
	"github.com/zebaqui/notx-engine/snip"
)

// NoteServer implements pb.NoteServiceServer backed by a NoteRepository.
type NoteServer struct {
	pb.UnimplementedNoteServiceServer
	repo         repo.NoteRepository
	contextRepo  repo.ContextRepository // may be nil when context layer is disabled
	snipRegistry *snip.Registry
	defaultPage  int
	maxPage      int
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

// SetSnipRegistry wires a snip plugin registry into the NoteServer so that
// plugin hooks are dispatched on note create and event append.
func (s *NoteServer) SetSnipRegistry(r *snip.Registry) {
	s.snipRegistry = r
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

	s.dispatchSnipHook(ctx, note, nil)

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

	if s.snipRegistry != nil {
		if note, getErr := s.repo.Get(ctx, req.Event.NoteUrn); getErr == nil {
			s.dispatchSnipHook(ctx, note, ev)
		}
	}

	return &pb.AppendEventResponse{Sequence: req.Event.Sequence}, nil
}

// ── ListSnips ─────────────────────────────────────────────────────────────────

func (s *NoteServer) ListSnips(ctx context.Context, req *pb.ListSnipsRequest) (*pb.ListSnipsResponse, error) {
	pageSize := int(req.PageSize)
	if pageSize <= 0 {
		pageSize = s.defaultPage
	}
	if pageSize > s.maxPage {
		pageSize = s.maxPage
	}

	opts := repo.ListSnipsOptions{
		SnipType:       req.SnipType,
		ProjectURN:     req.ProjectUrn,
		ParentURN:      req.ParentUrn,
		ParentAnchor:   req.ParentAnchor,
		IncludeDeleted: req.IncludeDeleted,
		PageSize:       pageSize,
		PageToken:      req.PageToken,
	}

	result, err := s.repo.ListSnips(ctx, opts)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list snips: %v", err)
	}

	headers := make([]*pb.NoteHeader, 0, len(result.Notes))
	for _, n := range result.Notes {
		headers = append(headers, coreNoteToHeader(n))
	}

	return &pb.ListSnipsResponse{
		Snips:         headers,
		NextPageToken: result.NextPageToken,
	}, nil
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
	// Snip fields
	if n.SnipType != nil {
		h.SnipType = *n.SnipType
	}
	if n.ParentURN != nil {
		h.ParentUrn = n.ParentURN.String()
	}
	if n.ParentAnchor != nil {
		h.ParentAnchor = *n.ParentAnchor
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
	if h.SnipType != "" {
		st := h.SnipType
		note.SnipType = &st
	}
	if h.ParentUrn != "" {
		parentURN, err := core.ParseURN(h.ParentUrn)
		if err != nil {
			return nil, fmt.Errorf("invalid parent_urn: %w", err)
		}
		note.ParentURN = &parentURN
	}
	if h.ParentAnchor != "" {
		pa := h.ParentAnchor
		note.ParentAnchor = &pa
	}

	return note, nil
}

// dispatchSnipHook calls the registered plugin hook after a note write.
// Called after CreateNote (event==nil) or AppendEvent (event!=nil).
// Hook errors are logged but never fail the write.
func (s *NoteServer) dispatchSnipHook(ctx context.Context, note *core.Note, event *core.Event) {
	if s.snipRegistry == nil || note.SnipType == nil {
		return
	}
	plugin, ok := s.snipRegistry.Get(*note.SnipType)
	if !ok {
		return
	}
	var err error
	if event == nil {
		err = plugin.OnNoteCreated(ctx, note)
	} else {
		err = plugin.OnEventAppended(ctx, note, event)
	}
	if err != nil {
		// Non-fatal — plugin index is best-effort
		_ = err
	}
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
	if urn == (core.URN{}) {
		urn = core.NewURN(core.ObjectTypeEvent)
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
