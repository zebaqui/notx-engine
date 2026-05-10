package core

import "math"

// ─────────────────────────────────────────────────────────────────────────────
// NoteAnalysis
// ─────────────────────────────────────────────────────────────────────────────

// NoteAnalysis is a holistic representation of a note derived from all its paragraphs.
type NoteAnalysis struct {
	NoteURN    string
	ProjectURN string
	FolderURN  string
	// All unique normalized concepts across all paragraphs (combined main + supporting).
	AllConcepts []string
	// Concepts that appear in >= 2 paragraphs — true "themes" of the note.
	ThemeConcepts []string
	// Concept families present in the note.
	Families []string
	// DominantRole is the ParagraphRole that appears most often across paragraphs.
	DominantRole ParagraphRole
	// RoleCounts maps role name → count of paragraphs with that role.
	RoleCounts map[string]int
	// ParagraphCount is the total number of paragraphs.
	ParagraphCount int
}

// ─────────────────────────────────────────────────────────────────────────────
// NoteRelationType
// ─────────────────────────────────────────────────────────────────────────────

// NoteRelationType describes the semantic relationship between two notes.
type NoteRelationType string

const (
	NoteRelSharesTheme   NoteRelationType = "shares_theme"
	NoteRelElaboratesOn  NoteRelationType = "elaborates_on"
	NoteRelContrastsWith NoteRelationType = "contrasts_with"
	NoteRelAnswers       NoteRelationType = "answers"
	NoteRelCauses        NoteRelationType = "causes"
)

// ─────────────────────────────────────────────────────────────────────────────
// ScoredNoteRelation
// ─────────────────────────────────────────────────────────────────────────────

// ScoredNoteRelation is returned by ScoreNoteRelation for one (a, b) pair.
type ScoredNoteRelation struct {
	RelationType  NoteRelationType
	Score         float64
	ReasonSignals []string
}

// ─────────────────────────────────────────────────────────────────────────────
// AnalyzeNote
// ─────────────────────────────────────────────────────────────────────────────

// AnalyzeNote builds a holistic NoteAnalysis from all of a note's annotated paragraphs.
func AnalyzeNote(noteURN, projectURN, folderURN string, paragraphs []AnnotatedParagraph) NoteAnalysis {
	// conceptFreq counts how many distinct paragraphs each concept appears in.
	conceptFreq := make(map[string]int)
	familySet := make(map[string]bool)
	roleCounts := make(map[string]int)

	for _, p := range paragraphs {
		roleCounts[string(p.Role)]++
		// Deduplicate within a single paragraph before counting.
		seen := make(map[string]bool)
		for _, c := range p.MainConcepts {
			if !seen[c] {
				conceptFreq[c]++
				seen[c] = true
			}
		}
		// Gather families from the concept seed.
		for _, c := range p.MainConcepts {
			if fam, ok := conceptFamilySeed[c]; ok {
				familySet[fam] = true
			}
		}
	}

	// Build AllConcepts and ThemeConcepts.
	var allConcepts, themeConcepts []string
	for concept, freq := range conceptFreq {
		allConcepts = append(allConcepts, concept)
		if freq >= 2 {
			themeConcepts = append(themeConcepts, concept)
		}
	}

	// Build Families slice.
	var families []string
	for fam := range familySet {
		families = append(families, fam)
	}

	// Find dominant role.
	var dominantRole ParagraphRole = RoleClaim
	maxCount := 0
	for role, count := range roleCounts {
		if count > maxCount {
			maxCount = count
			dominantRole = ParagraphRole(role)
		}
	}

	return NoteAnalysis{
		NoteURN:        noteURN,
		ProjectURN:     projectURN,
		FolderURN:      folderURN,
		AllConcepts:    allConcepts,
		ThemeConcepts:  themeConcepts,
		Families:       families,
		DominantRole:   dominantRole,
		RoleCounts:     roleCounts,
		ParagraphCount: len(paragraphs),
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ScoreNoteRelation
// ─────────────────────────────────────────────────────────────────────────────

// ScoreNoteRelation scores the candidate note-level relationship from a to b.
func ScoreNoteRelation(a, b NoteAnalysis) ScoredNoteRelation {
	themeOverlap := jaccardStrings(a.ThemeConcepts, b.ThemeConcepts)
	allConceptOverlap := jaccardStrings(a.AllConcepts, b.AllConcepts)
	familyOverlap := jaccardStrings(a.Families, b.Families)

	// Determine relation type from dominant role pair.
	relType := determineNoteRelType(a, b, themeOverlap)

	// Compute score.
	score := 0.5*themeOverlap + 0.3*allConceptOverlap + 0.2*familyOverlap
	score = math.Max(0, math.Min(1, score))

	// Build reason signals.
	var signals []string
	if themeOverlap > 0 {
		signals = append(signals, "theme_overlap")
	}
	if allConceptOverlap > 0 {
		signals = append(signals, "concept_overlap")
	}
	if familyOverlap > 0 {
		signals = append(signals, "family_overlap")
	}
	if a.DominantRole != RoleClaim || b.DominantRole != RoleClaim {
		signals = append(signals, "dominant_role")
	}

	return ScoredNoteRelation{
		RelationType:  relType,
		Score:         score,
		ReasonSignals: signals,
	}
}

// determineNoteRelType applies the role-pair heuristic to pick a NoteRelationType.
func determineNoteRelType(a, b NoteAnalysis, themeOverlap float64) NoteRelationType {
	switch {
	case a.DominantRole == RoleQuestion:
		return NoteRelAnswers
	case a.DominantRole == RoleContrast || b.DominantRole == RoleContrast:
		return NoteRelContrastsWith
	case a.DominantRole == RoleCauseEffect || b.DominantRole == RoleCauseEffect:
		return NoteRelCauses
	case themeOverlap > 0.3 && (a.DominantRole == RoleDefinition || b.DominantRole == RoleDefinition):
		return NoteRelElaboratesOn
	default:
		return NoteRelSharesTheme
	}
}

// jaccardStrings computes Jaccard similarity over two string slices.
func jaccardStrings(a, b []string) float64 {
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
