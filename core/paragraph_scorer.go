package core

import (
	"math"
)

// ─────────────────────────────────────────────────────────────────────────────
// ScorerWeights
// ─────────────────────────────────────────────────────────────────────────────

// ScorerWeights mirrors repo.ParagraphWeights but lives in core so the scorer
// has no dependency on the repo package.
type ScorerWeights struct {
	// Signal dimension weights
	WProximityTier float64
	WRolePair      float64
	WOverlap       float64
	WCue           float64
	WPattern       float64
	// Per-tier proximity multipliers
	TierSameDoc     float64
	TierSameFolder  float64
	TierSameProject float64
	TierGlobal      float64
}

// DefaultWeights returns the initial heuristic weights from the PRD.
func DefaultWeights() ScorerWeights {
	return ScorerWeights{
		WProximityTier:  0.20,
		WRolePair:       0.25,
		WOverlap:        0.20,
		WCue:            0.20,
		WPattern:        0.15,
		TierSameDoc:     1.00,
		TierSameFolder:  0.75,
		TierSameProject: 0.50,
		TierGlobal:      0.25,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// AnnotatedParagraph — scorer input
// ─────────────────────────────────────────────────────────────────────────────

// AnnotatedParagraph is the scorer's view of a paragraph (no DB types).
type AnnotatedParagraph struct {
	ID           string
	NoteURN      string
	FolderURN    string
	ProjectURN   string
	Position     int
	Text         string
	Role         ParagraphRole
	MainConcepts []string // normalized
}

// ─────────────────────────────────────────────────────────────────────────────
// ScoredRelation — scorer output
// ─────────────────────────────────────────────────────────────────────────────

// ScoredRelation is returned by ScoreCandidate for one (a, b) pair.
type ScoredRelation struct {
	RelationType  RelationType
	ProximityTier ProximityTier
	Score         float64
	PatternHash   string
	ReasonSignals []string
	CuePresent    bool
}

// ─────────────────────────────────────────────────────────────────────────────
// ResolveTier
// ─────────────────────────────────────────────────────────────────────────────

// ResolveTier returns the proximity tier for two annotated paragraphs.
func ResolveTier(a, b AnnotatedParagraph) ProximityTier {
	if a.NoteURN == b.NoteURN {
		return TierSameDoc
	}
	if a.FolderURN != "" && a.FolderURN == b.FolderURN {
		return TierSameFolder
	}
	if a.ProjectURN != "" && a.ProjectURN == b.ProjectURN {
		return TierSameProject
	}
	return TierGlobal
}

// ─────────────────────────────────────────────────────────────────────────────
// ScoreCandidate
// ─────────────────────────────────────────────────────────────────────────────

// ScoreCandidate scores the candidate relation from paragraph a to paragraph b.
// patternScores maps patternHash → net_score ([-1,1]); pass nil for no pattern signal.
// Returns the best-scoring ScoredRelation. The caller should filter by MinScore.
func ScoreCandidate(a, b AnnotatedParagraph, patternScores map[string]float64, w ScorerWeights) ScoredRelation {
	tier := ResolveTier(a, b)

	// Determine relation type — prefer cue over role-pair default.
	relType, cuePresent := CueRelationType(b.Text)
	if !cuePresent {
		relType = defaultRelType(a.Role, b.Role)
	}

	hash := PatternHash(string(a.Role), string(b.Role), string(relType), string(tier), cuePresent)

	// ── Score each signal ────────────────────────────────────────────────────
	proxScore := proximityTierScore(a, b, tier, w)
	roleScore := RolePairScore(a.Role, b.Role)
	overlapScore := ConceptOverlapScore(a.MainConcepts, b.MainConcepts)
	cScore := cueScore(cuePresent)
	patScore := patternSignal(hash, patternScores)

	total := w.WProximityTier*proxScore +
		w.WRolePair*roleScore +
		w.WOverlap*overlapScore +
		w.WCue*cScore +
		w.WPattern*patScore

	// Clamp to [0, 1]
	total = math.Max(0, math.Min(1, total))

	// Build reason signals list
	var signals []string
	if proxScore > 0 {
		signals = append(signals, "proximity_tier:"+string(tier))
	}
	if roleScore >= 0.7 {
		signals = append(signals, "role_pair")
	}
	if overlapScore > 0 {
		signals = append(signals, "concept_overlap")
	}
	if cuePresent {
		signals = append(signals, "cue_phrase")
	}
	if patScore != 0 {
		signals = append(signals, "pattern_feedback")
	}

	return ScoredRelation{
		RelationType:  relType,
		ProximityTier: tier,
		Score:         total,
		PatternHash:   hash,
		ReasonSignals: signals,
		CuePresent:    cuePresent,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Sub-scorers
// ─────────────────────────────────────────────────────────────────────────────

func proximityTierScore(a, b AnnotatedParagraph, tier ProximityTier, w ScorerWeights) float64 {
	var multiplier float64
	switch tier {
	case TierSameDoc:
		multiplier = w.TierSameDoc
	case TierSameFolder:
		multiplier = w.TierSameFolder
	case TierSameProject:
		multiplier = w.TierSameProject
	default:
		multiplier = w.TierGlobal
	}
	if tier == TierSameDoc {
		return multiplier * DistanceScore(a.Position, b.Position)
	}
	return multiplier
}

// DistanceScore returns a score based on how many positions apart two
// paragraphs are within the same document.
func DistanceScore(posA, posB int) float64 {
	d := posA - posB
	if d < 0 {
		d = -d
	}
	switch d {
	case 0:
		return 0 // same paragraph — shouldn't happen in practice
	case 1:
		return 1.0
	case 2:
		return 0.7
	case 3:
		return 0.4
	default:
		return 0.1
	}
}

// RolePairScore returns how well two roles relate to each other.
func RolePairScore(a, b ParagraphRole) float64 {
	type pair struct{ a, b ParagraphRole }
	scores := map[pair]float64{
		{RoleDefinition, RoleExample}:  0.90,
		{RoleQuestion, RoleClaim}:      0.90,
		{RoleQuestion, RoleExample}:    0.85,
		{RoleContrast, RoleContrast}:   0.85,
		{RoleCauseEffect, RoleClaim}:   0.80,
		{RoleCauseEffect, RoleExample}: 0.75,
		{RoleClaim, RoleExample}:       0.75,
		{RoleDefinition, RoleClaim}:    0.70,
		{RoleClaim, RoleDefinition}:    0.65,
		{RoleClaim, RoleContrast}:      0.60,
		{RoleExample, RoleClaim}:       0.55,
		{RoleContrast, RoleClaim}:      0.55,
		{RoleClaim, RoleCauseEffect}:   0.55,
		{RoleClaim, RoleClaim}:         0.45,
		{RoleExample, RoleExample}:     0.40,
	}
	if s, ok := scores[pair{a, b}]; ok {
		return s
	}
	// Symmetric fallback
	if s, ok := scores[pair{b, a}]; ok {
		return s * 0.9
	}
	return 0.30
}

// ConceptOverlapScore computes Jaccard similarity over two normalized concept
// slices. Both slices must already be normalized.
func ConceptOverlapScore(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	setA := make(map[string]bool, len(a))
	for _, v := range a {
		setA[v] = true
	}
	inter := 0
	for _, v := range b {
		if setA[v] {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

func cueScore(present bool) float64 {
	if present {
		return 1.0
	}
	return 0.0
}

func patternSignal(hash string, patternScores map[string]float64) float64 {
	if patternScores == nil {
		return 0
	}
	s, ok := patternScores[hash]
	if !ok {
		return 0
	}
	// Clamp to [-1, 1] then scale to [0, 1] for use as an additive signal.
	// Negative net_score makes this a mild penalty; positive makes it a boost.
	if s < -1 {
		s = -1
	}
	if s > 1 {
		s = 1
	}
	// Map [-1,1] → [0,1]
	return (s + 1) / 2
}

// defaultRelType returns the most likely relation type for a role pair when no
// lexical cue is present.
func defaultRelType(a, b ParagraphRole) RelationType {
	switch {
	case b == RoleExample:
		return RelIllustrates
	case b == RoleContrast || a == RoleContrast:
		return RelContrastsWith
	case a == RoleCauseEffect || b == RoleCauseEffect:
		return RelCauses
	case a == RoleQuestion:
		return RelAnswers
	case b == RoleClaim || a == RoleDefinition:
		return RelElaborates
	default:
		return RelSupports
	}
}
