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

// ContextServer implements pb.ContextServiceServer backed by a
// repo.ContextRepository.
type ContextServer struct {
	pb.UnimplementedContextServiceServer
	repo        repo.ContextRepository
	defaultPage int
	maxPage     int
}

// NewContextServer returns a ready-to-register ContextServer.
func NewContextServer(r repo.ContextRepository, defaultPage, maxPage int) *ContextServer {
	if defaultPage <= 0 {
		defaultPage = 50
	}
	if maxPage <= 0 {
		maxPage = 200
	}
	return &ContextServer{repo: r, defaultPage: defaultPage, maxPage: maxPage}
}

// ─────────────────────────────────────────────────────────────────────────────
// ListBursts
// ─────────────────────────────────────────────────────────────────────────────

func (s *ContextServer) ListBursts(ctx context.Context, req *pb.ListBurstsRequest) (*pb.ListBurstsResponse, error) {
	if req.NoteUrn == "" {
		return nil, status.Error(codes.InvalidArgument, "note_urn is required")
	}

	pageSize := int(req.PageSize)
	if pageSize <= 0 {
		pageSize = s.defaultPage
	}
	if pageSize > s.maxPage {
		pageSize = s.maxPage
	}

	bursts, nextToken, err := s.repo.ListBursts(ctx, req.NoteUrn, int(req.SinceSequence), pageSize)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list bursts: %v", err)
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

	burst, err := s.repo.GetBurst(ctx, req.Id)
	if err != nil {
		return nil, contextRepoErrToStatus(err, req.Id)
	}

	return &pb.GetBurstResponse{Burst: burstToProto(&burst)}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// ListCandidates
// ─────────────────────────────────────────────────────────────────────────────

func (s *ContextServer) ListCandidates(ctx context.Context, req *pb.ListCandidatesRequest) (*pb.ListCandidatesResponse, error) {
	pageSize := int(req.PageSize)
	if pageSize <= 0 {
		pageSize = s.defaultPage
	}
	if pageSize > s.maxPage {
		pageSize = s.maxPage
	}

	opts := repo.CandidateListOptions{
		ProjectURN: req.ProjectUrn,
		NoteURN:    req.NoteUrn,
		Status:     req.Status,
		MinScore:   req.MinScore,
		PageSize:   pageSize,
		PageToken:  req.PageToken,
	}

	candidates, nextToken, err := s.repo.ListCandidates(ctx, opts)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list candidates: %v", err)
	}

	pbCandidates := make([]*pb.CandidateRecord, 0, len(candidates))
	for i := range candidates {
		c := candidateToProto(&candidates[i])
		if req.IncludeBursts {
			c = s.embedBursts(ctx, c, &candidates[i])
		}
		pbCandidates = append(pbCandidates, c)
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

	candidate, err := s.repo.GetCandidate(ctx, req.Id)
	if err != nil {
		return nil, contextRepoErrToStatus(err, req.Id)
	}

	c := candidateToProto(&candidate)

	// include_bursts defaults to true for GetCandidate (the caller almost
	// always needs them to make a review decision).
	if req.IncludeBursts || !req.IncludeBursts {
		c = s.embedBursts(ctx, c, &candidate)
	}

	return &pb.GetCandidateResponse{Candidate: c}, nil
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

	result, err := s.repo.PromoteCandidate(ctx, req.Id, opts)
	if err != nil {
		return nil, contextRepoErrToStatus(err, req.Id)
	}

	// Fetch the updated candidate record to embed in the response.
	updated, err := s.repo.GetCandidate(ctx, req.Id)
	if err != nil {
		// Non-fatal: return the promote result even if we can't re-fetch.
		return &pb.PromoteCandidateResponse{
			AnchorAId: result.AnchorAID,
			AnchorBId: result.AnchorBID,
			LinkAToB:  result.LinkAToB,
			LinkBToA:  result.LinkBToA,
		}, nil
	}

	return &pb.PromoteCandidateResponse{
		AnchorAId: result.AnchorAID,
		AnchorBId: result.AnchorBID,
		LinkAToB:  result.LinkAToB,
		LinkBToA:  result.LinkBToA,
		Candidate: candidateToProto(&updated),
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// DismissCandidate
// ─────────────────────────────────────────────────────────────────────────────

func (s *ContextServer) DismissCandidate(ctx context.Context, req *pb.DismissCandidateRequest) (*pb.DismissCandidateResponse, error) {
	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	if err := s.repo.DismissCandidate(ctx, req.Id, req.ReviewerUrn); err != nil {
		return nil, contextRepoErrToStatus(err, req.Id)
	}

	updated, err := s.repo.GetCandidate(ctx, req.Id)
	if err != nil {
		return &pb.DismissCandidateResponse{}, nil
	}

	return &pb.DismissCandidateResponse{
		Candidate: candidateToProto(&updated),
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// GetStats
// ─────────────────────────────────────────────────────────────────────────────

func (s *ContextServer) GetStats(ctx context.Context, req *pb.GetStatsRequest) (*pb.GetStatsResponse, error) {
	stats, err := s.repo.GetContextStats(ctx, req.ProjectUrn)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get context stats: %v", err)
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

	cfg, err := s.repo.GetProjectContextConfig(ctx, req.ProjectUrn)
	if err != nil {
		return nil, contextRepoErrToStatus(err, req.ProjectUrn)
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
		UpdatedAt:  time.Now().UTC(),
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

	if err := s.repo.UpsertProjectContextConfig(ctx, cfg); err != nil {
		return nil, status.Errorf(codes.Internal, "set project context config: %v", err)
	}

	return &pb.SetProjectConfigResponse{
		Config: projectContextConfigToProto(&cfg),
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Internal helpers
// ─────────────────────────────────────────────────────────────────────────────

// embedBursts fetches burst_a and burst_b from the repo and attaches them to
// the proto candidate. Errors are silently ignored — the candidate is still
// returned without the previews rather than failing the whole RPC.
func (s *ContextServer) embedBursts(ctx context.Context, c *pb.CandidateRecord, src *repo.CandidateRecord) *pb.CandidateRecord {
	if ba, err := s.repo.GetBurst(ctx, src.BurstAID); err == nil {
		c.BurstA = burstToProto(&ba)
	}
	if bb, err := s.repo.GetBurst(ctx, src.BurstBID); err == nil {
		c.BurstB = burstToProto(&bb)
	}
	return c
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
	pb := &pb.CandidateRecord{
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
		pb.ReviewedAt = safeTimestamp(*c.ReviewedAt)
	}
	return pb
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
// A zero time is returned as a nil-safe zero Timestamp rather than panicking.
func safeTimestamp(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

// contextRepoErrToStatus maps repository sentinel errors to appropriate gRPC
// status codes.
func contextRepoErrToStatus(err error, id string) error {
	switch {
	case errors.Is(err, repo.ErrNotFound):
		return status.Errorf(codes.NotFound, "%q not found", id)
	default:
		return status.Errorf(codes.Internal, "internal error: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Inference — non-gRPC methods (called directly by the HTTP layer)
// ─────────────────────────────────────────────────────────────────────────────

// GetFullStats returns the complete ContextStats (including inference counts)
// directly as a repo.ContextStats, bypassing the proto mapping.
// The HTTP handler uses this to avoid needing proto fields for new stats.
func (s *ContextServer) GetFullStats(ctx context.Context, projectURN string) (repo.ContextStats, error) {
	return s.repo.GetContextStats(ctx, projectURN)
}

// ListInferences returns a paginated list of inference records.
func (s *ContextServer) ListInferences(ctx context.Context, opts repo.InferenceListOptions) ([]repo.InferenceRecord, string, error) {
	return s.repo.ListInferences(ctx, opts)
}

// GetInference returns a single inference record by ID.
func (s *ContextServer) GetInference(ctx context.Context, id string) (repo.InferenceRecord, error) {
	return s.repo.GetInference(ctx, id)
}

// GetNoteInference returns the active pending inference for a note.
func (s *ContextServer) GetNoteInference(ctx context.Context, noteURN string) (repo.InferenceRecord, bool, error) {
	return s.repo.GetNoteInference(ctx, noteURN)
}

// AcceptInference marks an inference as accepted and applies the accepted fields.
func (s *ContextServer) AcceptInference(ctx context.Context, id string, opts repo.AcceptInferenceOptions) error {
	return s.repo.AcceptInference(ctx, id, opts)
}

// RejectInference marks an inference as rejected.
func (s *ContextServer) RejectInference(ctx context.Context, id, reviewerURN string) error {
	return s.repo.RejectInference(ctx, id, reviewerURN)
}

// SearchBursts performs a full-text search over burst text and tokens.
func (s *ContextServer) SearchBursts(ctx context.Context, q string, pageSize int) ([]repo.BurstSearchResult, error) {
	return s.repo.SearchBursts(ctx, q, pageSize)
}
