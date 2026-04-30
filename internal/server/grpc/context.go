package grpc

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/zebaqui/notx-engine/proto"
	"github.com/zebaqui/notx-engine/repo"
	"github.com/zebaqui/notx-engine/service"
)

// ContextServer implements pb.ContextServiceServer as a thin proto adapter,
// delegating all business logic to service.ContextService.
type ContextServer struct {
	pb.UnimplementedContextServiceServer
	svc service.ContextService
}

// NewContextServer returns a ready-to-register ContextServer.
func NewContextServer(svc service.ContextService) *ContextServer {
	return &ContextServer{svc: svc}
}

// ─────────────────────────────────────────────────────────────────────────────
// ListBursts
// ─────────────────────────────────────────────────────────────────────────────

func (s *ContextServer) ListBursts(ctx context.Context, req *pb.ListBurstsRequest) (*pb.ListBurstsResponse, error) {
	bursts, nextToken, err := s.svc.ListBursts(ctx, req.NoteUrn, int(req.SinceSequence), int(req.PageSize))
	if err != nil {
		return nil, svcErrToStatus(err, req.NoteUrn)
	}

	pbBursts := make([]*pb.BurstRecord, 0, len(bursts))
	for i := range bursts {
		pbBursts = append(pbBursts, burstToProto(&bursts[i]))
	}

	return &pb.ListBurstsResponse{
		Bursts:        pbBursts,
		NextPageToken: nextToken,
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// GetBurst
// ─────────────────────────────────────────────────────────────────────────────

func (s *ContextServer) GetBurst(ctx context.Context, req *pb.GetBurstRequest) (*pb.GetBurstResponse, error) {
	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	burst, err := s.svc.GetBurst(ctx, req.Id)
	if err != nil {
		return nil, svcErrToStatus(err, req.Id)
	}

	return &pb.GetBurstResponse{Burst: burstToProto(&burst)}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// ListCandidates
// ─────────────────────────────────────────────────────────────────────────────

func (s *ContextServer) ListCandidates(ctx context.Context, req *pb.ListCandidatesRequest) (*pb.ListCandidatesResponse, error) {
	opts := repo.CandidateListOptions{
		ProjectURN: req.ProjectUrn,
		NoteURN:    req.NoteUrn,
		Status:     req.Status,
		MinScore:   req.MinScore,
		PageSize:   int(req.PageSize),
		PageToken:  req.PageToken,
	}

	candidates, nextToken, err := s.svc.ListCandidates(ctx, opts, req.IncludeBursts)
	if err != nil {
		return nil, svcErrToStatus(err, "")
	}

	pbCandidates := make([]*pb.CandidateRecord, 0, len(candidates))
	for i := range candidates {
		c := &candidates[i]
		pbC := candidateToProto(&c.Candidate)
		if c.BurstA != nil {
			pbC.BurstA = burstToProto(c.BurstA)
		}
		if c.BurstB != nil {
			pbC.BurstB = burstToProto(c.BurstB)
		}
		pbCandidates = append(pbCandidates, pbC)
	}

	return &pb.ListCandidatesResponse{
		Candidates:    pbCandidates,
		NextPageToken: nextToken,
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// GetCandidate
// ─────────────────────────────────────────────────────────────────────────────

func (s *ContextServer) GetCandidate(ctx context.Context, req *pb.GetCandidateRequest) (*pb.GetCandidateResponse, error) {
	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	c, err := s.svc.GetCandidate(ctx, req.Id)
	if err != nil {
		return nil, svcErrToStatus(err, req.Id)
	}

	pbC := candidateToProto(&c.Candidate)
	if c.BurstA != nil {
		pbC.BurstA = burstToProto(c.BurstA)
	}
	if c.BurstB != nil {
		pbC.BurstB = burstToProto(c.BurstB)
	}

	return &pb.GetCandidateResponse{Candidate: pbC}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// PromoteCandidate
// ─────────────────────────────────────────────────────────────────────────────

func (s *ContextServer) PromoteCandidate(ctx context.Context, req *pb.PromoteCandidateRequest) (*pb.PromoteCandidateResponse, error) {
	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	direction := req.Direction
	if direction == "" {
		direction = "both"
	}

	opts := repo.PromoteOptions{
		Label:       req.Label,
		Direction:   direction,
		ReviewerURN: req.ReviewerUrn,
	}

	result, updated, err := s.svc.PromoteCandidate(ctx, req.Id, opts)
	if err != nil {
		return nil, svcErrToStatus(err, req.Id)
	}

	resp := &pb.PromoteCandidateResponse{
		AnchorAId: result.AnchorAID,
		AnchorBId: result.AnchorBID,
		LinkAToB:  result.LinkAToB,
		LinkBToA:  result.LinkBToA,
	}
	if updated != nil {
		resp.Candidate = candidateToProto(updated)
	}
	return resp, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// DismissCandidate
// ─────────────────────────────────────────────────────────────────────────────

func (s *ContextServer) DismissCandidate(ctx context.Context, req *pb.DismissCandidateRequest) (*pb.DismissCandidateResponse, error) {
	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	updated, err := s.svc.DismissCandidate(ctx, req.Id, req.ReviewerUrn)
	if err != nil {
		return nil, svcErrToStatus(err, req.Id)
	}

	resp := &pb.DismissCandidateResponse{}
	if updated != nil {
		resp.Candidate = candidateToProto(updated)
	}
	return resp, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// GetStats
// ─────────────────────────────────────────────────────────────────────────────

func (s *ContextServer) GetStats(ctx context.Context, req *pb.GetStatsRequest) (*pb.GetStatsResponse, error) {
	stats, err := s.svc.GetStats(ctx, req.ProjectUrn)
	if err != nil {
		return nil, svcErrToStatus(err, req.ProjectUrn)
	}

	return &pb.GetStatsResponse{
		Stats: &pb.ContextStats{
			BurstsTotal:                 int64(stats.BurstsTotal),
			BurstsToday:                 int64(stats.BurstsToday),
			CandidatesPending:           int64(stats.CandidatesPending),
			CandidatesPendingUnenriched: int64(stats.CandidatesPendingUnenriched),
			CandidatesPromoted:          int64(stats.CandidatesPromoted),
			CandidatesDismissed:         int64(stats.CandidatesDismissed),
			OldestPendingAgeDays:        stats.OldestPendingAgeDays,
		},
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// GetProjectConfig
// ─────────────────────────────────────────────────────────────────────────────

func (s *ContextServer) GetProjectConfig(ctx context.Context, req *pb.GetProjectConfigRequest) (*pb.GetProjectConfigResponse, error) {
	if req.ProjectUrn == "" {
		return nil, status.Error(codes.InvalidArgument, "project_urn is required")
	}

	cfg, err := s.svc.GetProjectConfig(ctx, req.ProjectUrn)
	if err != nil {
		return nil, svcErrToStatus(err, req.ProjectUrn)
	}

	return &pb.GetProjectConfigResponse{
		Config: projectContextConfigToProto(&cfg),
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// SetProjectConfig
// ─────────────────────────────────────────────────────────────────────────────

func (s *ContextServer) SetProjectConfig(ctx context.Context, req *pb.SetProjectConfigRequest) (*pb.SetProjectConfigResponse, error) {
	if req.ProjectUrn == "" {
		return nil, status.Error(codes.InvalidArgument, "project_urn is required")
	}

	cfg := repo.ProjectContextConfig{
		ProjectURN: req.ProjectUrn,
	}

	// 0 means "reset to global default" → nil pointer.
	if req.BurstMaxPerNotePerDay > 0 {
		v := int(req.BurstMaxPerNotePerDay)
		cfg.BurstMaxPerNotePerDay = &v
	}
	if req.BurstMaxPerProjectPerDay > 0 {
		v := int(req.BurstMaxPerProjectPerDay)
		cfg.BurstMaxPerProjectPerDay = &v
	}

	result, err := s.svc.SetProjectConfig(ctx, cfg)
	if err != nil {
		return nil, svcErrToStatus(err, req.ProjectUrn)
	}

	return &pb.SetProjectConfigResponse{
		Config: projectContextConfigToProto(&result),
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Proto conversion helpers
// ─────────────────────────────────────────────────────────────────────────────

func burstToProto(b *repo.BurstRecord) *pb.BurstRecord {
	return &pb.BurstRecord{
		Id:         b.ID,
		NoteUrn:    b.NoteURN,
		ProjectUrn: b.ProjectURN,
		FolderUrn:  b.FolderURN,
		AuthorUrn:  b.AuthorURN,
		Sequence:   int32(b.Sequence),
		LineStart:  int32(b.LineStart),
		LineEnd:    int32(b.LineEnd),
		Text:       b.Text,
		Tokens:     b.Tokens,
		Truncated:  b.Truncated,
		CreatedAt:  safeTimestamp(b.CreatedAt),
	}
}

func candidateToProto(c *repo.CandidateRecord) *pb.CandidateRecord {
	rec := &pb.CandidateRecord{
		Id:           c.ID,
		BurstAId:     c.BurstAID,
		BurstBId:     c.BurstBID,
		NoteUrnA:     c.NoteURN_A,
		NoteUrnB:     c.NoteURN_B,
		ProjectUrn:   c.ProjectURN,
		OverlapScore: c.OverlapScore,
		Bm25Score:    c.BM25Score,
		Status:       c.Status,
		CreatedAt:    safeTimestamp(c.CreatedAt),
		ReviewedBy:   c.ReviewedBy,
		PromotedLink: c.PromotedLink,
	}
	if c.ReviewedAt != nil {
		rec.ReviewedAt = safeTimestamp(*c.ReviewedAt)
	}
	return rec
}

func projectContextConfigToProto(c *repo.ProjectContextConfig) *pb.ProjectContextConfig {
	p := &pb.ProjectContextConfig{
		ProjectUrn: c.ProjectURN,
		UpdatedAt:  safeTimestamp(c.UpdatedAt),
	}
	if c.BurstMaxPerNotePerDay != nil {
		p.BurstMaxPerNotePerDay = int32(*c.BurstMaxPerNotePerDay)
	}
	if c.BurstMaxPerProjectPerDay != nil {
		p.BurstMaxPerProjectPerDay = int32(*c.BurstMaxPerProjectPerDay)
	}
	return p
}

// safeTimestamp converts a time.Time to a protobuf Timestamp.
// A zero time is returned as nil rather than a zero Timestamp.
func safeTimestamp(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

// ─────────────────────────────────────────────────────────────────────────────
// Non-gRPC direct methods (HTTP layer calls these — signatures must not change)
// ─────────────────────────────────────────────────────────────────────────────

// GetFullStats returns the complete ContextStats (including inference counts)
// directly as a repo.ContextStats, bypassing the proto mapping.
// The HTTP handler uses this to avoid needing proto fields for new stats.
func (s *ContextServer) GetFullStats(ctx context.Context, projectURN string) (repo.ContextStats, error) {
	return s.svc.GetStats(ctx, projectURN)
}

// ListInferences returns a paginated list of inference records.
func (s *ContextServer) ListInferences(ctx context.Context, opts repo.InferenceListOptions) ([]repo.InferenceRecord, string, error) {
	return s.svc.ListInferences(ctx, opts)
}

// GetInference returns a single inference record by ID.
func (s *ContextServer) GetInference(ctx context.Context, id string) (repo.InferenceRecord, error) {
	return s.svc.GetInference(ctx, id)
}

// GetNoteInference returns the active pending inference for a note.
func (s *ContextServer) GetNoteInference(ctx context.Context, noteURN string) (repo.InferenceRecord, bool, error) {
	return s.svc.GetNoteInference(ctx, noteURN)
}

// AcceptInference marks an inference as accepted and applies the accepted fields.
func (s *ContextServer) AcceptInference(ctx context.Context, id string, opts repo.AcceptInferenceOptions) error {
	return s.svc.AcceptInference(ctx, id, opts)
}

// RejectInference marks an inference as rejected.
func (s *ContextServer) RejectInference(ctx context.Context, id, reviewerURN string) error {
	return s.svc.RejectInference(ctx, id, reviewerURN)
}

// SearchBursts performs a full-text search over burst text and tokens.
func (s *ContextServer) SearchBursts(ctx context.Context, q string, pageSize int) ([]repo.BurstSearchResult, error) {
	return s.svc.SearchBursts(ctx, q, pageSize)
}
