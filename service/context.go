package service

import (
	"context"
	"fmt"
	"time"

	"github.com/zebaqui/notx-engine/repo"
)

// ─────────────────────────────────────────────────────────────────────────────
// ContextService interface
// ─────────────────────────────────────────────────────────────────────────────

// ContextService defines the business-logic contract for the context graph:
// burst storage, candidate review, project config, and inference management.
type ContextService interface {
	// ── Bursts ────────────────────────────────────────────────────────────────

	// ListBursts returns a paginated list of bursts for the given note,
	// starting after sinceSequence. pageSize <= 0 uses the service default.
	ListBursts(ctx context.Context, noteURN string, sinceSequence, pageSize int) ([]repo.BurstRecord, string, error)

	// GetBurst returns the burst identified by id.
	GetBurst(ctx context.Context, id string) (repo.BurstRecord, error)

	// SearchBursts performs a full-text search over burst text and tokens.
	SearchBursts(ctx context.Context, query string, pageSize int) ([]repo.BurstSearchResult, error)

	// ── Candidates ────────────────────────────────────────────────────────────

	// ListCandidates returns a paginated list of link candidates.
	// When includeBursts is true, BurstA and BurstB are embedded in each result.
	ListCandidates(ctx context.Context, opts repo.CandidateListOptions, includeBursts bool) ([]CandidateWithBursts, string, error)

	// GetCandidate returns the candidate identified by id with its bursts
	// always embedded (burst previews are almost always required for review).
	GetCandidate(ctx context.Context, id string) (CandidateWithBursts, error)

	// PromoteCandidate marks a candidate as promoted and creates the
	// corresponding link anchors and backlinks. Returns the promotion result
	// and the updated candidate (which may be nil if the re-fetch fails).
	PromoteCandidate(ctx context.Context, id string, opts repo.PromoteOptions) (repo.PromoteResult, *repo.CandidateRecord, error)

	// DismissCandidate marks a candidate as dismissed. Returns the updated
	// candidate (which may be nil if the re-fetch fails — non-fatal).
	DismissCandidate(ctx context.Context, id, reviewerURN string) (*repo.CandidateRecord, error)

	// ── Stats & config ────────────────────────────────────────────────────────

	// GetStats returns aggregate context statistics, optionally scoped to a
	// project. Pass an empty projectURN for global stats.
	GetStats(ctx context.Context, projectURN string) (repo.ContextStats, error)

	// GetProjectConfig returns the context configuration for the given project.
	GetProjectConfig(ctx context.Context, projectURN string) (repo.ProjectContextConfig, error)

	// SetProjectConfig persists the context configuration for a project.
	// UpdatedAt is always set to the current time. Returns the stored config.
	SetProjectConfig(ctx context.Context, cfg repo.ProjectContextConfig) (repo.ProjectContextConfig, error)

	// ── Inferences ────────────────────────────────────────────────────────────

	// ListInferences returns a paginated list of inference records.
	ListInferences(ctx context.Context, opts repo.InferenceListOptions) ([]repo.InferenceRecord, string, error)

	// GetInference returns the inference record identified by id.
	GetInference(ctx context.Context, id string) (repo.InferenceRecord, error)

	// GetNoteInference returns the active pending inference for a note, if any.
	// The bool is false when no pending inference exists.
	GetNoteInference(ctx context.Context, noteURN string) (repo.InferenceRecord, bool, error)

	// AcceptInference marks an inference as accepted and applies the inferred fields.
	AcceptInference(ctx context.Context, id string, opts repo.AcceptInferenceOptions) error

	// RejectInference marks an inference as rejected.
	RejectInference(ctx context.Context, id, reviewerURN string) error
}

// CandidateWithBursts wraps a CandidateRecord together with the two burst
// records that seeded it. Either burst may be nil if it could not be fetched
// (non-fatal; the candidate is still usable without the previews).
type CandidateWithBursts struct {
	Candidate repo.CandidateRecord
	BurstA    *repo.BurstRecord
	BurstB    *repo.BurstRecord
}

// ─────────────────────────────────────────────────────────────────────────────
// contextService — concrete implementation
// ─────────────────────────────────────────────────────────────────────────────

type contextService struct {
	repo        repo.ContextRepository
	defaultPage int
	maxPage     int
}

func newContextService(r repo.ContextRepository, defaultPage, maxPage int) *contextService {
	dp, mx := resolvePageDefaults(defaultPage, maxPage)
	return &contextService{repo: r, defaultPage: dp, maxPage: mx}
}

// ── Bursts ────────────────────────────────────────────────────────────────────

func (s *contextService) ListBursts(ctx context.Context, noteURN string, sinceSequence, pageSize int) ([]repo.BurstRecord, string, error) {
	if noteURN == "" {
		return nil, "", fmt.Errorf("%w: noteURN is required", ErrInvalidInput)
	}
	pageSize = clampPageSize(pageSize, s.defaultPage, s.maxPage)
	return s.repo.ListBursts(ctx, noteURN, sinceSequence, pageSize)
}

func (s *contextService) GetBurst(ctx context.Context, id string) (repo.BurstRecord, error) {
	if id == "" {
		return repo.BurstRecord{}, fmt.Errorf("%w: id is required", ErrInvalidInput)
	}
	return s.repo.GetBurst(ctx, id)
}

func (s *contextService) SearchBursts(ctx context.Context, query string, pageSize int) ([]repo.BurstSearchResult, error) {
	pageSize = clampPageSize(pageSize, s.defaultPage, s.maxPage)
	return s.repo.SearchBursts(ctx, query, pageSize)
}

// ── Candidates ────────────────────────────────────────────────────────────────

func (s *contextService) ListCandidates(ctx context.Context, opts repo.CandidateListOptions, includeBursts bool) ([]CandidateWithBursts, string, error) {
	opts.PageSize = clampPageSize(opts.PageSize, s.defaultPage, s.maxPage)

	candidates, nextToken, err := s.repo.ListCandidates(ctx, opts)
	if err != nil {
		return nil, "", err
	}

	out := make([]CandidateWithBursts, 0, len(candidates))
	for i := range candidates {
		c := CandidateWithBursts{Candidate: candidates[i]}
		if includeBursts {
			c = s.embedBursts(ctx, c)
		}
		out = append(out, c)
	}
	return out, nextToken, nil
}

func (s *contextService) GetCandidate(ctx context.Context, id string) (CandidateWithBursts, error) {
	if id == "" {
		return CandidateWithBursts{}, fmt.Errorf("%w: id is required", ErrInvalidInput)
	}

	candidate, err := s.repo.GetCandidate(ctx, id)
	if err != nil {
		return CandidateWithBursts{}, err
	}

	// Bursts are always embedded for GetCandidate — the caller almost always
	// needs them to make a review decision.
	c := CandidateWithBursts{Candidate: candidate}
	return s.embedBursts(ctx, c), nil
}

func (s *contextService) PromoteCandidate(ctx context.Context, id string, opts repo.PromoteOptions) (repo.PromoteResult, *repo.CandidateRecord, error) {
	if id == "" {
		return repo.PromoteResult{}, nil, fmt.Errorf("%w: id is required", ErrInvalidInput)
	}
	if opts.Direction == "" {
		opts.Direction = "both"
	}

	result, err := s.repo.PromoteCandidate(ctx, id, opts)
	if err != nil {
		return repo.PromoteResult{}, nil, err
	}

	// Re-fetch the updated candidate to return alongside the promotion result.
	// This is non-fatal — we return the result even if the re-fetch fails.
	updated, err := s.repo.GetCandidate(ctx, id)
	if err != nil {
		return result, nil, nil //nolint:nilerr // deliberate: re-fetch is best-effort
	}
	return result, &updated, nil
}

func (s *contextService) DismissCandidate(ctx context.Context, id, reviewerURN string) (*repo.CandidateRecord, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: id is required", ErrInvalidInput)
	}

	if err := s.repo.DismissCandidate(ctx, id, reviewerURN); err != nil {
		return nil, err
	}

	// Re-fetch is non-fatal.
	updated, err := s.repo.GetCandidate(ctx, id)
	if err != nil {
		return nil, nil //nolint:nilerr // deliberate: re-fetch is best-effort
	}
	return &updated, nil
}

// ── Stats & config ────────────────────────────────────────────────────────────

func (s *contextService) GetStats(ctx context.Context, projectURN string) (repo.ContextStats, error) {
	return s.repo.GetContextStats(ctx, projectURN)
}

func (s *contextService) GetProjectConfig(ctx context.Context, projectURN string) (repo.ProjectContextConfig, error) {
	if projectURN == "" {
		return repo.ProjectContextConfig{}, fmt.Errorf("%w: projectURN is required", ErrInvalidInput)
	}
	return s.repo.GetProjectContextConfig(ctx, projectURN)
}

func (s *contextService) SetProjectConfig(ctx context.Context, cfg repo.ProjectContextConfig) (repo.ProjectContextConfig, error) {
	if cfg.ProjectURN == "" {
		return repo.ProjectContextConfig{}, fmt.Errorf("%w: cfg.ProjectURN is required", ErrInvalidInput)
	}
	cfg.UpdatedAt = time.Now().UTC()
	if err := s.repo.UpsertProjectContextConfig(ctx, cfg); err != nil {
		return repo.ProjectContextConfig{}, err
	}
	return cfg, nil
}

// ── Inferences ────────────────────────────────────────────────────────────────

func (s *contextService) ListInferences(ctx context.Context, opts repo.InferenceListOptions) ([]repo.InferenceRecord, string, error) {
	return s.repo.ListInferences(ctx, opts)
}

func (s *contextService) GetInference(ctx context.Context, id string) (repo.InferenceRecord, error) {
	if id == "" {
		return repo.InferenceRecord{}, fmt.Errorf("%w: id is required", ErrInvalidInput)
	}
	return s.repo.GetInference(ctx, id)
}

func (s *contextService) GetNoteInference(ctx context.Context, noteURN string) (repo.InferenceRecord, bool, error) {
	if noteURN == "" {
		return repo.InferenceRecord{}, false, fmt.Errorf("%w: noteURN is required", ErrInvalidInput)
	}
	return s.repo.GetNoteInference(ctx, noteURN)
}

func (s *contextService) AcceptInference(ctx context.Context, id string, opts repo.AcceptInferenceOptions) error {
	if id == "" {
		return fmt.Errorf("%w: id is required", ErrInvalidInput)
	}
	return s.repo.AcceptInference(ctx, id, opts)
}

func (s *contextService) RejectInference(ctx context.Context, id, reviewerURN string) error {
	if id == "" {
		return fmt.Errorf("%w: id is required", ErrInvalidInput)
	}
	return s.repo.RejectInference(ctx, id, reviewerURN)
}

// ─────────────────────────────────────────────────────────────────────────────
// Internal helpers
// ─────────────────────────────────────────────────────────────────────────────

// embedBursts fetches BurstA and BurstB from the repo and attaches them to c.
// Errors are silently ignored — the candidate is returned without previews
// rather than failing the entire call.
func (s *contextService) embedBursts(ctx context.Context, c CandidateWithBursts) CandidateWithBursts {
	if ba, err := s.repo.GetBurst(ctx, c.Candidate.BurstAID); err == nil {
		c.BurstA = &ba
	}
	if bb, err := s.repo.GetBurst(ctx, c.Candidate.BurstBID); err == nil {
		c.BurstB = &bb
	}
	return c
}
