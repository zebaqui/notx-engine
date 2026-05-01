package core

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// ─────────────────────────────────────────────────────────────────────────────
// Role types
// ─────────────────────────────────────────────────────────────────────────────

// ParagraphRole classifies the rhetorical function of a paragraph.
type ParagraphRole string

const (
	RoleDefinition  ParagraphRole = "definition"
	RoleExample     ParagraphRole = "example"
	RoleContrast    ParagraphRole = "contrast"
	RoleCauseEffect ParagraphRole = "cause_effect"
	RoleQuestion    ParagraphRole = "question"
	RoleClaim       ParagraphRole = "claim" // default / catch-all
)

// ─────────────────────────────────────────────────────────────────────────────
// Relation types
// ─────────────────────────────────────────────────────────────────────────────

// RelationType describes the semantic relationship between two paragraphs.
type RelationType string

const (
	RelElaborates    RelationType = "elaborates"
	RelSupports      RelationType = "supports"
	RelContrastsWith RelationType = "contrasts_with"
	RelAnswers       RelationType = "answers"
	RelCauses        RelationType = "causes"
	RelIllustrates   RelationType = "illustrates"
)

// ─────────────────────────────────────────────────────────────────────────────
// Proximity tiers
// ─────────────────────────────────────────────────────────────────────────────

// ProximityTier describes how close two paragraphs are in the document hierarchy.
type ProximityTier string

const (
	TierSameDoc     ProximityTier = "same_doc"
	TierSameFolder  ProximityTier = "same_folder"
	TierSameProject ProximityTier = "same_project"
	TierGlobal      ProximityTier = "global"
)

// ─────────────────────────────────────────────────────────────────────────────
// RawParagraph
// ─────────────────────────────────────────────────────────────────────────────

// RawParagraph is the output of SplitParagraphs before classification.
type RawParagraph struct {
	Text      string
	LineStart int // 0-based
	LineEnd   int // 0-based, inclusive
}

// ─────────────────────────────────────────────────────────────────────────────
// SplitParagraphs
// ─────────────────────────────────────────────────────────────────────────────

// SplitParagraphs splits note text into paragraphs on blank lines.
// Empty paragraphs (whitespace-only) are skipped.
func SplitParagraphs(text string) []RawParagraph {
	lines := strings.Split(text, "\n")
	var paragraphs []RawParagraph
	start := -1
	var buf []string

	flush := func(end int) {
		if len(buf) == 0 {
			return
		}
		joined := strings.TrimSpace(strings.Join(buf, "\n"))
		if joined != "" {
			paragraphs = append(paragraphs, RawParagraph{
				Text:      joined,
				LineStart: start,
				LineEnd:   end,
			})
		}
		buf = buf[:0]
		start = -1
	}

	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			flush(i - 1)
		} else {
			if start == -1 {
				start = i
			}
			buf = append(buf, line)
		}
	}
	flush(len(lines) - 1)
	return paragraphs
}

// ─────────────────────────────────────────────────────────────────────────────
// ClassifyRole
// ─────────────────────────────────────────────────────────────────────────────

// rolePattern pairs a compiled regex with the role it signals.
type rolePattern struct {
	re   *regexp.Regexp
	role ParagraphRole
}

var rolePatterns = []rolePattern{
	// Question — must check before definition to catch "What is X?"
	{regexp.MustCompile(`(?i)^\s*(what|how|why|when|where|who|which|is|are|can|does|do|should)\s+\w`), RoleQuestion},
	{regexp.MustCompile(`\?`), RoleQuestion},
	// Definition
	{regexp.MustCompile(`(?i)\b(is defined as|is called|refers to|means|is a type of|is an? )\b`), RoleDefinition},
	{regexp.MustCompile(`(?i)^\s*\w[\w\s]*\s+is\s+(a|an|the)\s+\w`), RoleDefinition},
	// Example
	{regexp.MustCompile(`(?i)\b(for example|for instance|such as|e\.g\.|e\.g,|like when|as an example)\b`), RoleExample},
	// Contrast
	{regexp.MustCompile(`(?i)^\s*(however|but|on the other hand|in contrast|unlike|whereas|although|yet|still|nevertheless|conversely)\b`), RoleContrast},
	{regexp.MustCompile(`(?i)\b(however|but|unlike|whereas|in contrast|on the other hand)\b`), RoleContrast},
	// Cause/Effect
	{regexp.MustCompile(`(?i)\b(because|therefore|as a result|this leads to|thus|hence|consequently|due to|since|so that|which causes|which leads)\b`), RoleCauseEffect},
}

// ClassifyRole infers the rhetorical role of a paragraph from cue phrases.
// Falls through to RoleClaim when no pattern matches.
func ClassifyRole(text string) ParagraphRole {
	for _, p := range rolePatterns {
		if p.re.MatchString(text) {
			return p.role
		}
	}
	return RoleClaim
}

// ─────────────────────────────────────────────────────────────────────────────
// CueRelationType
// ─────────────────────────────────────────────────────────────────────────────

// cueEntry maps a compiled regex to the relation type it signals.
type cueEntry struct {
	re  *regexp.Regexp
	rel RelationType
}

var cueEntries = []cueEntry{
	{regexp.MustCompile(`(?i)\b(for example|for instance|such as|e\.g\.)\b`), RelIllustrates},
	{regexp.MustCompile(`(?i)^\s*(however|but|in contrast|unlike|whereas|on the other hand|conversely)\b`), RelContrastsWith},
	{regexp.MustCompile(`(?i)\b(because|therefore|as a result|this leads to|thus|hence|consequently)\b`), RelCauses},
	{regexp.MustCompile(`(?i)\b(supports|confirms|shows that|demonstrates that|proves that)\b`), RelSupports},
	{regexp.MustCompile(`(?i)\b(elaborates|expands on|further explains|in addition|moreover|furthermore)\b`), RelElaborates},
	{regexp.MustCompile(`(?i)^\s*(to answer|in response|the answer is|this answers)\b`), RelAnswers},
}

// CueRelationType returns the relation type signalled by a cue phrase in text.
// Returns false when no cue is found.
func CueRelationType(text string) (RelationType, bool) {
	for _, e := range cueEntries {
		if e.re.MatchString(text) {
			return e.rel, true
		}
	}
	return "", false
}

// ─────────────────────────────────────────────────────────────────────────────
// Concept extraction + normalization
// ─────────────────────────────────────────────────────────────────────────────

// stopWords is the set of words excluded from concept extraction.
var stopWords = map[string]bool{
	"a": true, "an": true, "the": true, "and": true, "or": true, "but": true,
	"in": true, "on": true, "at": true, "to": true, "for": true, "of": true,
	"with": true, "by": true, "from": true, "is": true, "are": true, "was": true,
	"were": true, "be": true, "been": true, "being": true, "have": true, "has": true,
	"had": true, "do": true, "does": true, "did": true, "will": true, "would": true,
	"could": true, "should": true, "may": true, "might": true, "shall": true,
	"can": true, "this": true, "that": true, "these": true, "those": true,
	"it": true, "its": true, "it's": true, "i": true, "we": true, "you": true,
	"he": true, "she": true, "they": true, "their": true, "our": true, "your": true,
	"as": true, "if": true, "so": true, "then": true, "when": true, "which": true,
	"who": true, "what": true, "how": true, "why": true, "not": true, "no": true,
}

// conceptFamilySeed maps normalized concept tokens to their family name.
// Extend this map over time to grow coverage.
var conceptFamilySeed = map[string]string{
	"learn": "learning", "learning": "learning", "memory": "learning",
	"knowledge": "learning", "understand": "learning", "understanding": "learning",
	"study": "learning", "recall": "learning", "retention": "learning",
	"cognition": "cognition", "cognitive": "cognition", "brain": "cognition",
	"schema": "cognition", "mental": "cognition", "process": "cognition",
	"data": "data", "dataset": "data", "model": "data",
	"algorithm": "software", "code": "software", "function": "software",
	"system": "software", "api": "software", "interface": "software",
	"user": "product", "feature": "product", "design": "product",
	"cause": "causality", "effect": "causality", "result": "causality",
	"pattern": "pattern", "structure": "pattern",
}

var nonAlpha = regexp.MustCompile(`[^a-z0-9\s]`)

// NormalizeConcept lowercases s, strips non-alphanumeric characters, and trims
// whitespace. Returns "" for stop words and tokens shorter than 3 characters.
// The 3-char minimum filters noise like "il", "va", "au", "hi", "tu" etc.
// that appear in multilingual content and create false cross-doc relations.
func NormalizeConcept(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = nonAlpha.ReplaceAllString(s, "")
	s = strings.TrimSpace(s)
	if len(s) < 3 || stopWords[s] {
		return ""
	}
	return s
}

// ExtractConcepts extracts and normalizes concepts from text.
// Returns (main, supporting, families) — all values are normalized.
// main: tokens that appear at least twice or are in the family seed.
// supporting: remaining meaningful tokens.
// families: deduplicated family names derived from main concepts.
func ExtractConcepts(text string) (main, supporting, families []string) {
	// Tokenize
	words := strings.FieldsFunc(text, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r)
	})

	// Count normalized token frequencies
	freq := make(map[string]int)
	for _, w := range words {
		n := NormalizeConcept(w)
		if n != "" {
			freq[n]++
		}
	}

	familySet := make(map[string]bool)
	mainSet := make(map[string]bool)
	suppSet := make(map[string]bool)

	for token, count := range freq {
		_, inSeed := conceptFamilySeed[token]
		if count >= 2 || inSeed {
			if !mainSet[token] {
				main = append(main, token)
				mainSet[token] = true
			}
			if fam, ok := conceptFamilySeed[token]; ok && !familySet[fam] {
				families = append(families, fam)
				familySet[fam] = true
			}
		} else if count == 1 {
			if !suppSet[token] {
				supporting = append(supporting, token)
				suppSet[token] = true
			}
		}
	}
	return main, supporting, families
}

// ─────────────────────────────────────────────────────────────────────────────
// PatternHash
// ─────────────────────────────────────────────────────────────────────────────

// PatternHash returns a deterministic 16-hex-char fingerprint that identifies
// a structural pattern without referencing any note content.
// Inputs are: roleA, roleB, relationType, proximityTier, cuePresent.
func PatternHash(roleA, roleB, relType, tier string, cue bool) string {
	cueStr := "0"
	if cue {
		cueStr = "1"
	}
	input := fmt.Sprintf("%s:%s:%s:%s:%s", roleA, roleB, relType, tier, cueStr)
	sum := sha256.Sum256([]byte(input))
	return fmt.Sprintf("%x", sum[:8]) // 16 hex chars
}
