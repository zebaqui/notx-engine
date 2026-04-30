package grpc

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/zebaqui/notx-engine/proto"
	"github.com/zebaqui/notx-engine/repo"
	"github.com/zebaqui/notx-engine/service"
)

// LinkServer implements pb.LinkServiceServer by delegating all business
// logic to service.LinkService.
type LinkServer struct {
	pb.UnimplementedLinkServiceServer
	svc service.LinkService
}

// NewLinkServer returns a ready-to-register LinkServer.
func NewLinkServer(svc service.LinkService) *LinkServer {
	return &LinkServer{svc: svc}
}

// ─────────────────────────────────────────────────────────────────────────────
// Anchor handlers
// ─────────────────────────────────────────────────────────────────────────────

func (s *LinkServer) UpsertAnchor(ctx context.Context, req *pb.UpsertAnchorRequest) (*pb.UpsertAnchorResponse, error) {
	record := repo.AnchorRecord{
		NoteURN:   req.NoteUrn,
		AnchorID:  req.AnchorId,
		Line:      int(req.Line),
		CharStart: int(req.CharStart),
		CharEnd:   int(req.CharEnd),
		Preview:   req.Preview,
		Status:    req.Status,
	}

	stored, err := s.svc.UpsertAnchor(ctx, record)
	if err != nil {
		return nil, svcErrToStatus(err, req.AnchorId)
	}

	return &pb.UpsertAnchorResponse{Anchor: anchorToProto(stored)}, nil
}

func (s *LinkServer) DeleteAnchor(ctx context.Context, req *pb.DeleteAnchorRequest) (*pb.DeleteAnchorResponse, error) {
	if err := s.svc.DeleteAnchor(ctx, req.NoteUrn, req.AnchorId, req.Tombstone); err != nil {
		return nil, svcErrToStatus(err, req.AnchorId)
	}

	return &pb.DeleteAnchorResponse{Deleted: true}, nil
}

func (s *LinkServer) GetAnchor(ctx context.Context, req *pb.GetAnchorRequest) (*pb.GetAnchorResponse, error) {
	a, err := s.svc.GetAnchor(ctx, req.NoteUrn, req.AnchorId)
	if err != nil {
		return nil, svcErrToStatus(err, req.AnchorId)
	}

	return &pb.GetAnchorResponse{Anchor: anchorToProto(a)}, nil
}

func (s *LinkServer) ListAnchors(ctx context.Context, req *pb.ListAnchorsRequest) (*pb.ListAnchorsResponse, error) {
	anchors, err := s.svc.ListAnchors(ctx, req.NoteUrn)
	if err != nil {
		return nil, svcErrToStatus(err, req.NoteUrn)
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
	record := repo.BacklinkRecord{
		SourceURN:    req.SourceUrn,
		TargetURN:    req.TargetUrn,
		TargetAnchor: req.TargetAnchor,
		Label:        req.Label,
	}

	stored, err := s.svc.UpsertBacklink(ctx, record)
	if err != nil {
		return nil, svcErrToStatus(err, req.SourceUrn)
	}

	return &pb.UpsertBacklinkResponse{Backlink: backlinkToProto(stored)}, nil
}

func (s *LinkServer) DeleteBacklink(ctx context.Context, req *pb.DeleteBacklinkRequest) (*pb.DeleteBacklinkResponse, error) {
	if err := s.svc.DeleteBacklink(ctx, req.SourceUrn, req.TargetUrn, req.TargetAnchor); err != nil {
		return nil, svcErrToStatus(err, req.SourceUrn)
	}

	return &pb.DeleteBacklinkResponse{Deleted: true}, nil
}

func (s *LinkServer) ListBacklinks(ctx context.Context, req *pb.ListBacklinksRequest) (*pb.ListBacklinksResponse, error) {
	backlinks, err := s.svc.ListBacklinks(ctx, req.TargetUrn, req.AnchorId)
	if err != nil {
		return nil, svcErrToStatus(err, req.TargetUrn)
	}

	pbBacklinks := make([]*pb.BacklinkRecord, 0, len(backlinks))
	for _, b := range backlinks {
		pbBacklinks = append(pbBacklinks, backlinkToProto(b))
	}

	return &pb.ListBacklinksResponse{Backlinks: pbBacklinks}, nil
}

func (s *LinkServer) ListOutboundLinks(ctx context.Context, req *pb.ListOutboundLinksRequest) (*pb.ListOutboundLinksResponse, error) {
	links, err := s.svc.ListOutboundLinks(ctx, req.SourceUrn)
	if err != nil {
		return nil, svcErrToStatus(err, req.SourceUrn)
	}

	pbLinks := make([]*pb.BacklinkRecord, 0, len(links))
	for _, b := range links {
		pbLinks = append(pbLinks, backlinkToProto(b))
	}

	return &pb.ListOutboundLinksResponse{Links: pbLinks}, nil
}

func (s *LinkServer) GetReferrers(ctx context.Context, req *pb.GetReferrersRequest) (*pb.GetReferrersResponse, error) {
	urns, err := s.svc.GetReferrers(ctx, req.TargetUrn, req.AnchorId)
	if err != nil {
		return nil, svcErrToStatus(err, req.TargetUrn)
	}

	return &pb.GetReferrersResponse{SourceUrns: urns}, nil
}

// RecentBacklinks returns recently created backlinks with optional filters.
// Limit clamping (1-200, default 50) lives here in the gRPC layer.
func (s *LinkServer) RecentBacklinks(ctx context.Context, req *pb.RecentBacklinksRequest) (*pb.RecentBacklinksResponse, error) {
	limit := int(req.Limit)
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	opts := repo.RecentBacklinksOptions{
		NoteURN: req.NoteUrn,
		Label:   req.Label,
		Limit:   limit,
	}

	records, err := s.svc.RecentBacklinks(ctx, opts)
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
	record := repo.ExternalLinkRecord{
		SourceURN: req.SourceUrn,
		URI:       req.Uri,
		Label:     req.Label,
	}

	stored, err := s.svc.UpsertExternalLink(ctx, record)
	if err != nil {
		return nil, svcErrToStatus(err, req.Uri)
	}

	return &pb.UpsertExternalLinkResponse{Link: externalLinkToProto(stored)}, nil
}

func (s *LinkServer) DeleteExternalLink(ctx context.Context, req *pb.DeleteExternalLinkRequest) (*pb.DeleteExternalLinkResponse, error) {
	if err := s.svc.DeleteExternalLink(ctx, req.SourceUrn, req.Uri); err != nil {
		return nil, svcErrToStatus(err, req.Uri)
	}

	return &pb.DeleteExternalLinkResponse{Deleted: true}, nil
}

func (s *LinkServer) ListExternalLinks(ctx context.Context, req *pb.ListExternalLinksRequest) (*pb.ListExternalLinksResponse, error) {
	links, err := s.svc.ListExternalLinks(ctx, req.SourceUrn)
	if err != nil {
		return nil, svcErrToStatus(err, req.SourceUrn)
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
