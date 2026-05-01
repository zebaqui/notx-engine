package service

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/zebaqui/notx-engine/repo"
)

// ─────────────────────────────────────────────────────────────────────────────
// ParagraphService interface
// ─────────────────────────────────────────────────────────────────────────────

// ParagraphService defines the business-logic contract for the paragraph role
// graph: paragraph queries, relation queries, feedback recording, weight
// management, and full graph rebuilds.
type ParagraphService interface {
	// ── Paragraphs ────────────────────────────────────────────────────────────

	ListParagraphs(ctx context.Context, opts repo.ParagraphListOptions) ([]repo.ParagraphRecord, string, error)
	GetParagraph(ctx context.Context, id string) (repo.ParagraphRecord, error)

	// ── Relations ─────────────────────────────────────────────────────────────

	ListRelations(ctx context.Context, opts repo.ParagraphRelationListOptions) ([]repo.ParagraphRelationRecord, string, error)
	GetRelation(ctx context.Context, id string) (repo.ParagraphRelationRecord, error)
	// RecordFeedback records a thumbs-up or thumbs-down vote on a relation,
	// updates the anonymous pattern score table, and adjusts global weights.
	// vote must be "up" or "down".
	RecordFeedback(ctx context.Context, relationID, vote string) error

	// ── Weights ───────────────────────────────────────────────────────────────

	GetWeights(ctx context.Context) (repo.ParagraphWeights, error)
	// SetWeights replaces the global weights row. UpdatedAt is always set to now.
	SetWeights(ctx context.Context, w repo.ParagraphWeights) (repo.ParagraphWeights, error)

	// ── Rebuild ───────────────────────────────────────────────────────────────

	// RebuildGraph wipes all paragraphs and relations, resets the processing
	// queue so the background runner reprocesses every note.
	// Weights and pattern scores are preserved.
	RebuildGraph(ctx context.Context) error
}

// ─────────────────────────────────────────────────────────────────────────────
// paragraphService — concrete implementation
// ─────────────────────────────────────────────────────────────────────────────

type paragraphService struct {
	repo        repo.ParagraphRepository
	defaultPage int
	maxPage     int
}

func newParagraphService(r repo.ParagraphRepository, defaultPage, maxPage int) *paragraphService {
	dp, mx := resolvePageDefaults(defaultPage, maxPage)
	return &paragraphService{repo: r, defaultPage: dp, maxPage: mx}
}

// ── Paragraphs ────────────────────────────────────────────────────────────────

func (s *paragraphService) ListParagraphs(ctx context.Context, opts repo.ParagraphListOptions) ([]repo.ParagraphRecord, string, error) {
	opts.PageSize = clampPageSize(opts.PageSize, s.defaultPage, s.maxPage)
	return s.repo.ListParagraphs(ctx, opts)
}

func (s *paragraphService) GetParagraph(ctx context.Context, id string) (repo.ParagraphRecord, error) {
	if id == "" {
		return repo.ParagraphRecord{}, fmt.Errorf("%w: id is required", ErrInvalidInput)
	}
	return s.repo.GetParagraph(ctx, id)
}

// ── Relations ─────────────────────────────────────────────────────────────────

func (s *paragraphService) ListRelations(ctx context.Context, opts repo.ParagraphRelationListOptions) ([]repo.ParagraphRelationRecord, string, error) {
	opts.PageSize = clampPageSize(opts.PageSize, s.defaultPage, s.maxPage)
	return s.repo.ListRelations(ctx, opts)
}

func (s *paragraphService) GetRelation(ctx context.Context, id string) (repo.ParagraphRelationRecord, error) {
	if id == "" {
		return repo.ParagraphRelationRecord{}, fmt.Errorf("%w: id is required", ErrInvalidInput)
	}
	return s.repo.GetRelation(ctx, id)
}

// RecordFeedback is the full feedback loop:
//  1. Persist the vote on the relation row.
//  2. Update (or create) the anonymous pattern score record.
//  3. Nudge the global weight dimensions and tier multiplier.
func (s *paragraphService) RecordFeedback(ctx context.Context, relationID, vote string) error {
	if relationID == "" {
		return fmt.Errorf("%w: relationID is required", ErrInvalidInput)
	}
	if vote != "up" && vote != "down" {
		return fmt.Errorf("%w: vote must be 'up' or 'down'", ErrInvalidInput)
	}

	// 1. Persist vote + get updated relation
	rel, err := s.repo.RecordFeedback(ctx, relationID, vote)
	if err != nil {
		return err
	}

	// 2. Update pattern score
	if err := s.updatePatternScore(ctx, rel, vote); err != nil {
		// Non-fatal — log and continue
		fmt.Printf("paragraph service: WARN: updatePatternScore: %v\n", err)
	}

	// 3. Nudge global weights
	if err := s.nudgeWeights(ctx, rel, vote); err != nil {
		fmt.Printf("paragraph service: WARN: nudgeWeights: %v\n", err)
	}

	return nil
}

// updatePatternScore increments the vote tally for the relation's pattern hash
// and recomputes net_score.
func (s *paragraphService) updatePatternScore(ctx context.Context, rel repo.ParagraphRelationRecord, vote string) error {
	if rel.PatternHash == "" {
		return nil
	}

	ps, err := s.repo.GetPatternScore(ctx, rel.PatternHash)
	if err != nil {
		// Not found — create a new record
		cuePresent := false
		for _, sig := range rel.ReasonSignals {
			if sig == "cue_phrase" {
				cuePresent = true
				break
			}
		}
		ps = repo.PatternScoreRecord{
			PatternHash:   rel.PatternHash,
			RoleA:         "",
			RoleB:         "",
			RelationType:  rel.RelationType,
			ProximityTier: rel.ProximityTier,
			CuePresent:    cuePresent,
		}
	}

	if vote == "up" {
		ps.UpCount++
	} else {
		ps.DownCount++
	}

	// net_score = (up - down) / (up + down + smoothing), clamped to [-1, 1]
	const smoothing = 2.0
	total := float64(ps.UpCount+ps.DownCount) + smoothing
	ps.NetScore = float64(ps.UpCount-ps.DownCount) / total
	ps.NetScore = math.Max(-1, math.Min(1, ps.NetScore))
	ps.UpdatedAt = time.Now().UTC()

	return s.repo.UpsertPatternScore(ctx, ps)
}

// nudgeWeights adjusts global weight dimensions and the relevant tier
// multiplier based on which signals were active in this relation.
func (s *paragraphService) nudgeWeights(ctx context.Context, rel repo.ParagraphRelationRecord, vote string) error {
	w, err := s.repo.GetWeights(ctx)
	if err != nil {
		return err
	}

	const delta = 0.02
	sign := 1.0
	if vote == "down" {
		sign = -1.0
	}

	// Map active reason signals to their weight dimension
	for _, sig := range rel.ReasonSignals {
		switch sig {
		case "concept_overlap":
			w.WOverlap = clampWeight(w.WOverlap+sign*delta, 0.05, 0.60)
		case "role_pair":
			w.WRolePair = clampWeight(w.WRolePair+sign*delta, 0.05, 0.60)
		case "cue_phrase":
			w.WCue = clampWeight(w.WCue+sign*delta, 0.05, 0.60)
		case "pattern_feedback":
			w.WPattern = clampWeight(w.WPattern+sign*delta, 0.05, 0.60)
		}
		if len(sig) > len("proximity_tier:") && sig[:len("proximity_tier:")] == "proximity_tier:" {
			w.WProximityTier = clampWeight(w.WProximityTier+sign*delta, 0.05, 0.60)
		}
	}

	// Also nudge the specific tier multiplier
	switch rel.ProximityTier {
	case "same_doc":
		w.TierSameDoc = clampWeight(w.TierSameDoc+sign*delta, 0.10, 1.00)
	case "same_folder":
		w.TierSameFolder = clampWeight(w.TierSameFolder+sign*delta, 0.10, 1.00)
	case "same_project":
		w.TierSameProject = clampWeight(w.TierSameProject+sign*delta, 0.10, 1.00)
	case "global":
		w.TierGlobal = clampWeight(w.TierGlobal+sign*delta, 0.10, 1.00)
	}

	w.UpdatedAt = time.Now().UTC()
	return s.repo.UpsertWeights(ctx, w)
}

// clampWeight keeps a weight within [min, max].
func clampWeight(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// ── Weights ───────────────────────────────────────────────────────────────────

func (s *paragraphService) GetWeights(ctx context.Context) (repo.ParagraphWeights, error) {
	return s.repo.GetWeights(ctx)
}

func (s *paragraphService) SetWeights(ctx context.Context, w repo.ParagraphWeights) (repo.ParagraphWeights, error) {
	w.UpdatedAt = time.Now().UTC()
	if err := s.repo.UpsertWeights(ctx, w); err != nil {
		return repo.ParagraphWeights{}, err
	}
	return w, nil
}

// ── Rebuild ───────────────────────────────────────────────────────────────────

func (s *paragraphService) RebuildGraph(ctx context.Context) error {
	return s.repo.ResetGraph(ctx)
}
