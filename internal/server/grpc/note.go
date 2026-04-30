package grpc

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/zebaqui/notx-engine/core"
	pb "github.com/zebaqui/notx-engine/proto"
	"github.com/zebaqui/notx-engine/repo"
	"github.com/zebaqui/notx-engine/service"
)

// NoteServer is a thin proto adapter that delegates all business logic to
// service.NoteService. It is responsible only for translating between the
// proto wire format and the core/repo types understood by the service layer.
type NoteServer struct {
	pb.UnimplementedNoteServiceServer
	svc service.NoteService
}

// NewNoteServer returns a NoteServer backed by the given NoteService.
func NewNoteServer(svc service.NoteService) *NoteServer {
	return &NoteServer{svc: svc}
}

// ── GetNote ──────────────────────────────────────────────────────────────────

func (s *NoteServer) GetNote(ctx context.Context, req *pb.GetNoteRequest) (*pb.GetNoteResponse, error) {
	note, events, err := s.svc.Get(ctx, req.Urn)
	if err != nil {
		return nil, svcErrToStatus(err, req.Urn)
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
	opts := repo.ListOptions{
		ProjectURN:     req.ProjectUrn,
		FolderURN:      req.FolderUrn,
		IncludeDeleted: req.IncludeDeleted,
		PageSize:       int(req.PageSize), // service clamps to its configured defaults
		PageToken:      req.PageToken,
	}
	if req.NoteType != pb.NoteType_NOTE_TYPE_UNSPECIFIED {
		opts.FilterByType = true
		opts.NoteTypeFilter = protoNoteTypeToCore(req.NoteType)
	}

	result, err := s.svc.List(ctx, opts)
	if err != nil {
		return nil, svcErrToStatus(err, "")
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

	note, err := protoHeaderToCoreNote(req.Header)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid header: %v", err)
	}

	if err := s.svc.Create(ctx, note); err != nil {
		return nil, svcErrToStatus(err, req.Header.Urn)
	}

	return &pb.CreateNoteResponse{Header: coreNoteToHeader(note)}, nil
}

// ── DeleteNote ───────────────────────────────────────────────────────────────

func (s *NoteServer) DeleteNote(ctx context.Context, req *pb.DeleteNoteRequest) (*pb.DeleteNoteResponse, error) {
	if err := s.svc.Delete(ctx, req.Urn); err != nil {
		return nil, svcErrToStatus(err, req.Urn)
	}

	return &pb.DeleteNoteResponse{Deleted: true}, nil
}

// ── AppendEvent ──────────────────────────────────────────────────────────────

func (s *NoteServer) AppendEvent(ctx context.Context, req *pb.AppendEventRequest) (*pb.AppendEventResponse, error) {
	if req.Event == nil {
		return nil, status.Error(codes.InvalidArgument, "event is required")
	}

	ev, err := protoEventToCore(req.Event)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid event: %v", err)
	}

	opts := repo.AppendEventOptions{
		ExpectSequence: int(req.Event.Sequence),
	}

	if err := s.svc.AppendEvent(ctx, ev, opts); err != nil {
		return nil, svcErrToStatus(err, req.Event.NoteUrn)
	}

	return &pb.AppendEventResponse{Sequence: req.Event.Sequence}, nil
}

// ── ListSnips ─────────────────────────────────────────────────────────────────

func (s *NoteServer) ListSnips(ctx context.Context, req *pb.ListSnipsRequest) (*pb.ListSnipsResponse, error) {
	opts := repo.ListSnipsOptions{
		SnipType:       req.SnipType,
		ProjectURN:     req.ProjectUrn,
		ParentURN:      req.ParentUrn,
		ParentAnchor:   req.ParentAnchor,
		IncludeDeleted: req.IncludeDeleted,
		PageSize:       int(req.PageSize), // service clamps to its configured defaults
		PageToken:      req.PageToken,
	}

	result, err := s.svc.ListSnips(ctx, opts)
	if err != nil {
		return nil, svcErrToStatus(err, "")
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
	events, err := s.svc.Events(stream.Context(), req.NoteUrn, int(req.FromSequence))
	if err != nil {
		return svcErrToStatus(err, req.NoteUrn)
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
	opts := repo.SearchOptions{
		Query:     req.Query,
		PageSize:  int(req.PageSize), // service clamps to its configured defaults
		PageToken: req.PageToken,
	}

	results, err := s.svc.Search(ctx, opts)
	if err != nil {
		return nil, svcErrToStatus(err, "")
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
	upd := service.NoteUpdate{}

	if req.Header != nil {
		upd.Name = req.Header.Name

		switch req.Header.ProjectUrn {
		case "CLEAR":
			upd.ClearProjectURN = true
		case "":
			// no-op
		default:
			upd.SetProjectURN = req.Header.ProjectUrn
		}

		switch req.Header.FolderUrn {
		case "CLEAR":
			upd.ClearFolderURN = true
		case "":
			// no-op
		default:
			upd.SetFolderURN = req.Header.FolderUrn
		}
	}

	note, err := s.svc.Update(ctx, req.Urn, upd)
	if err != nil {
		return nil, svcErrToStatus(err, req.Urn)
	}

	return &pb.UpdateNoteResponse{Header: coreNoteToHeader(note)}, nil
}

// ── ReplaceContent ───────────────────────────────────────────────────────────

func (s *NoteServer) ReplaceContent(ctx context.Context, req *pb.ReplaceContentRequest) (*pb.ReplaceContentResponse, error) {
	in := service.ReplaceContentInput{
		NoteURN:   req.NoteUrn,
		Content:   req.Content,
		AuthorURN: req.AuthorUrn,
	}

	result, err := s.svc.ReplaceContent(ctx, in)
	if err != nil {
		return nil, svcErrToStatus(err, req.NoteUrn)
	}

	return &pb.ReplaceContentResponse{
		Sequence: int32(result.Sequence),
		Changed:  result.Changed,
		NoteUrn:  result.NoteURN,
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
		if parsed, parseErr := core.ParseURN(p.Urn); parseErr == nil {
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
