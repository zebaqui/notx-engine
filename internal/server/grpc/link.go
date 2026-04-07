package grpc

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/zebaqui/notx-engine/proto"
	"github.com/zebaqui/notx-engine/repo"
)

// LinkServer implements pb.LinkServiceServer backed by a repo.LinkRepository.
type LinkServer struct {
	pb.UnimplementedLinkServiceServer
	repo repo.LinkRepository
}

// NewLinkServer returns a ready-to-register LinkServer.
func NewLinkServer(r repo.LinkRepository) *LinkServer {
	return &LinkServer{repo: r}
}

// ─────────────────────────────────────────────────────────────────────────────
// Anchor handlers
// ─────────────────────────────────────────────────────────────────────────────

func (s *LinkServer) UpsertAnchor(ctx context.Context, req *pb.UpsertAnchorRequest) (*pb.UpsertAnchorResponse, error) {
	if req.NoteUrn == "" {
		return nil, status.Error(codes.InvalidArgument, "note_urn is required")
	}
	if req.AnchorId == "" {
		return nil, status.Error(codes.InvalidArgument, "anchor_id is required")
	}

	st := req.Status
	if st == "" {
		st = "ok"
	}

	a := repo.AnchorRecord{
		NoteURN:   req.NoteUrn,
		AnchorID:  req.AnchorId,
		Line:      int(req.Line),
		CharStart: int(req.CharStart),
		CharEnd:   int(req.CharEnd),
		Preview:   req.Preview,
		Status:    st,
		UpdatedAt: time.Now().UTC(),
	}

	if err := s.repo.UpsertAnchor(ctx, a); err != nil {
		return nil, linkRepoErrToStatus(err, req.AnchorId)
	}

	stored, err := s.repo.GetAnchor(ctx, req.NoteUrn, req.AnchorId)
	if err != nil {
		return nil, linkRepoErrToStatus(err, req.AnchorId)
	}

	return &pb.UpsertAnchorResponse{Anchor: anchorToProto(stored)}, nil
}

func (s *LinkServer) DeleteAnchor(ctx context.Context, req *pb.DeleteAnchorRequest) (*pb.DeleteAnchorResponse, error) {
	if req.NoteUrn == "" {
		return nil, status.Error(codes.InvalidArgument, "note_urn is required")
	}
	if req.AnchorId == "" {
		return nil, status.Error(codes.InvalidArgument, "anchor_id is required")
	}

	if err := s.repo.DeleteAnchor(ctx, req.NoteUrn, req.AnchorId, req.Tombstone); err != nil {
		return nil, linkRepoErrToStatus(err, req.AnchorId)
	}

	return &pb.DeleteAnchorResponse{Deleted: true}, nil
}

func (s *LinkServer) GetAnchor(ctx context.Context, req *pb.GetAnchorRequest) (*pb.GetAnchorResponse, error) {
	if req.NoteUrn == "" {
		return nil, status.Error(codes.InvalidArgument, "note_urn is required")
	}
	if req.AnchorId == "" {
		return nil, status.Error(codes.InvalidArgument, "anchor_id is required")
	}

	a, err := s.repo.GetAnchor(ctx, req.NoteUrn, req.AnchorId)
	if err != nil {
		return nil, linkRepoErrToStatus(err, req.AnchorId)
	}

	return &pb.GetAnchorResponse{Anchor: anchorToProto(a)}, nil
}

func (s *LinkServer) ListAnchors(ctx context.Context, req *pb.ListAnchorsRequest) (*pb.ListAnchorsResponse, error) {
	if req.NoteUrn == "" {
		return nil, status.Error(codes.InvalidArgument, "note_urn is required")
	}

	anchors, err := s.repo.ListAnchors(ctx, req.NoteUrn)
	if err != nil {
		return nil, linkRepoErrToStatus(err, req.NoteUrn)
	}

	pbAnchors := make([]*pb.AnchorRecord, 0, len(anchors))
	for _, a := range anchors {
		pbAnchors = append(pbAnchors, anchorToProto(a))
	}

	return &pb.ListAnchorsResponse{Anchors: pbAnchors}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Backlink handlers
// ─────────────────────────────────────────────────────────────────────────────

func (s *LinkServer) UpsertBacklink(ctx context.Context, req *pb.UpsertBacklinkRequest) (*pb.UpsertBacklinkResponse, error) {
	if req.SourceUrn == "" {
		return nil, status.Error(codes.InvalidArgument, "source_urn is required")
	}
	if req.TargetUrn == "" {
		return nil, status.Error(codes.InvalidArgument, "target_urn is required")
	}

	b := repo.BacklinkRecord{
		SourceURN:    req.SourceUrn,
		TargetURN:    req.TargetUrn,
		TargetAnchor: req.TargetAnchor,
		Label:        req.Label,
		CreatedAt:    time.Now().UTC(),
	}

	if err := s.repo.UpsertBacklink(ctx, b); err != nil {
		return nil, linkRepoErrToStatus(err, req.SourceUrn)
	}

	return &pb.UpsertBacklinkResponse{Backlink: backlinkToProto(b)}, nil
}

func (s *LinkServer) DeleteBacklink(ctx context.Context, req *pb.DeleteBacklinkRequest) (*pb.DeleteBacklinkResponse, error) {
	if req.SourceUrn == "" {
		return nil, status.Error(codes.InvalidArgument, "source_urn is required")
	}
	if req.TargetUrn == "" {
		return nil, status.Error(codes.InvalidArgument, "target_urn is required")
	}

	if err := s.repo.DeleteBacklink(ctx, req.SourceUrn, req.TargetUrn, req.TargetAnchor); err != nil {
		return nil, linkRepoErrToStatus(err, req.SourceUrn)
	}

	return &pb.DeleteBacklinkResponse{Deleted: true}, nil
}

func (s *LinkServer) ListBacklinks(ctx context.Context, req *pb.ListBacklinksRequest) (*pb.ListBacklinksResponse, error) {
	if req.TargetUrn == "" {
		return nil, status.Error(codes.InvalidArgument, "target_urn is required")
	}

	backlinks, err := s.repo.ListBacklinks(ctx, req.TargetUrn, req.AnchorId)
	if err != nil {
		return nil, linkRepoErrToStatus(err, req.TargetUrn)
	}

	pbBacklinks := make([]*pb.BacklinkRecord, 0, len(backlinks))
	for _, b := range backlinks {
		pbBacklinks = append(pbBacklinks, backlinkToProto(b))
	}

	return &pb.ListBacklinksResponse{Backlinks: pbBacklinks}, nil
}

func (s *LinkServer) ListOutboundLinks(ctx context.Context, req *pb.ListOutboundLinksRequest) (*pb.ListOutboundLinksResponse, error) {
	if req.SourceUrn == "" {
		return nil, status.Error(codes.InvalidArgument, "source_urn is required")
	}

	links, err := s.repo.ListOutboundLinks(ctx, req.SourceUrn)
	if err != nil {
		return nil, linkRepoErrToStatus(err, req.SourceUrn)
	}

	pbLinks := make([]*pb.BacklinkRecord, 0, len(links))
	for _, b := range links {
		pbLinks = append(pbLinks, backlinkToProto(b))
	}

	return &pb.ListOutboundLinksResponse{Links: pbLinks}, nil
}

func (s *LinkServer) GetReferrers(ctx context.Context, req *pb.GetReferrersRequest) (*pb.GetReferrersResponse, error) {
	if req.TargetUrn == "" {
		return nil, status.Error(codes.InvalidArgument, "target_urn is required")
	}

	urns, err := s.repo.GetReferrers(ctx, req.TargetUrn, req.AnchorId)
	if err != nil {
		return nil, linkRepoErrToStatus(err, req.TargetUrn)
	}

	return &pb.GetReferrersResponse{SourceUrns: urns}, nil
}

// RecentBacklinks returns recently created backlinks with optional filters.
func (s *LinkServer) RecentBacklinks(ctx context.Context, req *pb.RecentBacklinksRequest) (*pb.RecentBacklinksResponse, error) {
	limit := int(req.Limit)
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	records, err := s.repo.RecentBacklinks(ctx, repo.RecentBacklinksOptions{
		NoteURN: req.NoteUrn,
		Label:   req.Label,
		Limit:   limit,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "recent backlinks: %v", err)
	}

	out := make([]*pb.BacklinkRecord, 0, len(records))
	for i := range records {
		out = append(out, backlinkToProto(records[i]))
	}
	return &pb.RecentBacklinksResponse{Backlinks: out}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// External link handlers
// ─────────────────────────────────────────────────────────────────────────────

func (s *LinkServer) UpsertExternalLink(ctx context.Context, req *pb.UpsertExternalLinkRequest) (*pb.UpsertExternalLinkResponse, error) {
	if req.SourceUrn == "" {
		return nil, status.Error(codes.InvalidArgument, "source_urn is required")
	}
	if req.Uri == "" {
		return nil, status.Error(codes.InvalidArgument, "uri is required")
	}

	e := repo.ExternalLinkRecord{
		SourceURN: req.SourceUrn,
		URI:       req.Uri,
		Label:     req.Label,
		CreatedAt: time.Now().UTC(),
	}

	if err := s.repo.UpsertExternalLink(ctx, e); err != nil {
		return nil, linkRepoErrToStatus(err, req.Uri)
	}

	return &pb.UpsertExternalLinkResponse{Link: externalLinkToProto(e)}, nil
}

func (s *LinkServer) DeleteExternalLink(ctx context.Context, req *pb.DeleteExternalLinkRequest) (*pb.DeleteExternalLinkResponse, error) {
	if req.SourceUrn == "" {
		return nil, status.Error(codes.InvalidArgument, "source_urn is required")
	}
	if req.Uri == "" {
		return nil, status.Error(codes.InvalidArgument, "uri is required")
	}

	if err := s.repo.DeleteExternalLink(ctx, req.SourceUrn, req.Uri); err != nil {
		return nil, linkRepoErrToStatus(err, req.Uri)
	}

	return &pb.DeleteExternalLinkResponse{Deleted: true}, nil
}

func (s *LinkServer) ListExternalLinks(ctx context.Context, req *pb.ListExternalLinksRequest) (*pb.ListExternalLinksResponse, error) {
	if req.SourceUrn == "" {
		return nil, status.Error(codes.InvalidArgument, "source_urn is required")
	}

	links, err := s.repo.ListExternalLinks(ctx, req.SourceUrn)
	if err != nil {
		return nil, linkRepoErrToStatus(err, req.SourceUrn)
	}

	pbLinks := make([]*pb.ExternalLinkRecord, 0, len(links))
	for _, e := range links {
		pbLinks = append(pbLinks, externalLinkToProto(e))
	}

	return &pb.ListExternalLinksResponse{Links: pbLinks}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Proto conversion helpers
// ─────────────────────────────────────────────────────────────────────────────

func anchorToProto(a repo.AnchorRecord) *pb.AnchorRecord {
	return &pb.AnchorRecord{
		NoteUrn:   a.NoteURN,
		AnchorId:  a.AnchorID,
		Line:      int32(a.Line),
		CharStart: int32(a.CharStart),
		CharEnd:   int32(a.CharEnd),
		Preview:   a.Preview,
		Status:    a.Status,
		UpdatedAt: timestamppb.New(a.UpdatedAt),
	}
}

func backlinkToProto(b repo.BacklinkRecord) *pb.BacklinkRecord {
	return &pb.BacklinkRecord{
		SourceUrn:    b.SourceURN,
		TargetUrn:    b.TargetURN,
		TargetAnchor: b.TargetAnchor,
		Label:        b.Label,
		CreatedAt:    timestamppb.New(b.CreatedAt),
	}
}

func externalLinkToProto(e repo.ExternalLinkRecord) *pb.ExternalLinkRecord {
	return &pb.ExternalLinkRecord{
		SourceUrn: e.SourceURN,
		Uri:       e.URI,
		Label:     e.Label,
		CreatedAt: timestamppb.New(e.CreatedAt),
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Error mapping
// ─────────────────────────────────────────────────────────────────────────────

func linkRepoErrToStatus(err error, id string) error {
	if errors.Is(err, repo.ErrNotFound) {
		return status.Errorf(codes.NotFound, "%q not found", id)
	}
	return status.Errorf(codes.Internal, "internal error: %v", err)
}
