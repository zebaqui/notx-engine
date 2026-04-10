package core

import (
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// LineRange
// ---------------------------------------------------------------------------

// LineRange is a contiguous range of 1-based line numbers.
type LineRange struct {
	Start int
	End   int
}

// ---------------------------------------------------------------------------
// BurstConfig
// ---------------------------------------------------------------------------

// BurstConfig holds the configuration parameters for burst extraction.
type BurstConfig struct {
	WindowLines           int     // ±N context lines around each changed line (default: 2)
	ChunkSize             int     // max lines per window before splitting (default: 20)
	SkipWindowSeconds     int     // look-back window for skip check in seconds (default: 300)
	SkipThreshold         float64 // Jaccard threshold to trigger consecutive skip (default: 0.80)
	BurstRetentionDays    int     // days to retain bursts (default: 90)
	OverlapThreshold      float64 // Jaccard gate for candidate insertion (default: 0.12)
	CandidateLookbackDays int     // days to look back for candidates (default: 30)
	CandidateLookbackN    int     // max recent bursts to score (default: 100)
}

// DefaultBurstConfig returns a BurstConfig with the spec-recommended defaults.
func DefaultBurstConfig() BurstConfig {
	return BurstConfig{
		WindowLines:           2,
		ChunkSize:             20,
		SkipWindowSeconds:     300,
		SkipThreshold:         0.80,
		BurstRetentionDays:    90,
		OverlapThreshold:      0.12,
		CandidateLookbackDays: 30,
		CandidateLookbackN:    100,
	}
}

// ---------------------------------------------------------------------------
// Burst
// ---------------------------------------------------------------------------

// Burst is a self-contained excerpt of note content extracted at event time.
type Burst struct {
	ID         string // UUIDv7
	NoteURN    string
	ProjectURN string
	FolderURN  string
	AuthorURN  string
	Sequence   int // event sequence that produced this burst
	LineStart  int
	LineEnd    int
	Text       string   // raw extracted text
	Tokens     []string // normalized token set
	Truncated  bool
	CreatedAt  time.Time
}

// ---------------------------------------------------------------------------
// CandidatePair
// ---------------------------------------------------------------------------

// CandidatePair is a pair of burst IDs with their Jaccard similarity score.
type CandidatePair struct {
	BurstA       Burst
	BurstB       Burst
	NoteURN_A    string
	NoteURN_B    string
	ProjectURN   string
	OverlapScore float64
}

// ---------------------------------------------------------------------------
// GroupAffectedLines
// ---------------------------------------------------------------------------

// GroupAffectedLines groups the affected line numbers from event entries into
// contiguous windows with ±windowLines padding, clamped to [1, totalLines].
// Two groups are separate when their context windows would not overlap
// (gap > windowLines*2+1).
func GroupAffectedLines(entries []LineEntry, windowLines int, totalLines int) []LineRange {
	if totalLines < 1 {
		totalLines = 1
	}

	// Collect line numbers for set/setEmpty/delete operations.
	seen := make(map[int]struct{})
	for _, e := range entries {
		switch e.Op {
		case LineOpSet, LineOpSetEmpty, LineOpDelete:
			seen[e.LineNumber] = struct{}{}
		}
	}

	if len(seen) == 0 {
		return nil
	}

	lineNums := make([]int, 0, len(seen))
	for n := range seen {
		lineNums = append(lineNums, n)
	}
	sort.Ints(lineNums)

	// Group lines whose context windows overlap.
	// Two windows overlap when gap between consecutive lines <= windowLines*2+1.
	maxGap := windowLines*2 + 1

	type group struct {
		first, last int
	}
	var groups []group
	cur := group{first: lineNums[0], last: lineNums[0]}
	for i := 1; i < len(lineNums); i++ {
		gap := lineNums[i] - lineNums[i-1]
		if gap > maxGap {
			groups = append(groups, cur)
			cur = group{first: lineNums[i], last: lineNums[i]}
		} else {
			cur.last = lineNums[i]
		}
	}
	groups = append(groups, cur)

	// Build LineRange for each group with ±windowLines padding, clamped.
	ranges := make([]LineRange, 0, len(groups))
	for _, g := range groups {
		start := g.first - windowLines
		end := g.last + windowLines
		if start < 1 {
			start = 1
		}
		if end > totalLines {
			end = totalLines
		}
		ranges = append(ranges, LineRange{Start: start, End: end})
	}

	return ranges
}

// ---------------------------------------------------------------------------
// SplitRange
// ---------------------------------------------------------------------------

// SplitRange splits a LineRange that is larger than chunkSize into
// overlapping sub-windows of chunkSize with overlap=2 lines between adjacent
// sub-windows.
func SplitRange(r LineRange, chunkSize int) []LineRange {
	size := r.End - r.Start + 1
	if size <= chunkSize {
		return []LineRange{r}
	}

	var result []LineRange
	// Each new sub-window starts (chunkSize - 2) lines after the previous start,
	// giving a 2-line overlap between adjacent windows.
	step := chunkSize - 2
	if step < 1 {
		step = 1
	}

	start := r.Start
	for start <= r.End {
		end := start + chunkSize - 1
		if end > r.End {
			end = r.End
		}
		result = append(result, LineRange{Start: start, End: end})
		if end >= r.End {
			break
		}
		start += step
	}

	return result
}

// ---------------------------------------------------------------------------
// JaccardScore
// ---------------------------------------------------------------------------

// JaccardScore computes the Jaccard similarity between two token sets.
// Returns 0.0 if both sets are empty.
func JaccardScore(a, b []string) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0.0
	}

	setA := make(map[string]struct{}, len(a))
	for _, t := range a {
		setA[t] = struct{}{}
	}

	setB := make(map[string]struct{}, len(b))
	for _, t := range b {
		setB[t] = struct{}{}
	}

	// Intersection
	intersection := 0
	for t := range setA {
		if _, ok := setB[t]; ok {
			intersection++
		}
	}

	// Union = |A| + |B| - |A∩B|
	union := len(setA) + len(setB) - intersection
	if union == 0 {
		return 0.0
	}

	return float64(intersection) / float64(union)
}

// ---------------------------------------------------------------------------
// SimilaritySkip
// ---------------------------------------------------------------------------

// SimilaritySkip returns true if the candidate token set is similar enough
// to the existing token set (Jaccard >= threshold) that a new burst should
// be skipped.
func SimilaritySkip(existing, candidate []string, threshold float64) bool {
	return JaccardScore(existing, candidate) >= threshold
}

// ---------------------------------------------------------------------------
// TokenizeBurst
// ---------------------------------------------------------------------------

// TokenizeBurst tokenizes text using the same algorithm as the FTS5 index:
// lowercase, split on non-alphanumeric, discard single-char tokens, deduplicate.
// This mirrors the tokenise function in repo/index/index.go.
// Note: this does NOT remove stop words (stop words are removed in SlugFromText
// for slug generation, but burst tokenization keeps all tokens for Jaccard scoring).
func TokenizeBurst(text string) []string {
	var sb strings.Builder
	for _, r := range strings.ToLower(text) {
		if isBurstAlphaNum(r) {
			sb.WriteRune(r)
		} else {
			sb.WriteRune(' ')
		}
	}

	words := strings.Fields(sb.String())
	seen := make(map[string]struct{}, len(words))
	out := make([]string, 0, len(words))
	for _, w := range words {
		if len(w) < 2 {
			continue
		}
		if _, dup := seen[w]; dup {
			continue
		}
		seen[w] = struct{}{}
		out = append(out, w)
	}
	return out
}

// isBurstAlphaNum reports whether r is an ASCII letter or digit.
func isBurstAlphaNum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}

// ---------------------------------------------------------------------------
// ExtractBursts
// ---------------------------------------------------------------------------

// ExtractBursts orchestrates burst extraction for a single event.
// It groups affected lines, splits oversized windows, applies the trivial
// filter, tokenizes, and enriches each burst with event/note context.
// Rate limit and skip checks require DB access and are handled by the caller
// (repo layer). This function only does the in-memory work.
// noteLines is the post-event materialized content of the note (slice of lines).
func ExtractBursts(note *Note, event *Event, noteLines []string, cfg BurstConfig) []Burst {
	totalLines := len(noteLines)
	if totalLines == 0 {
		return nil
	}

	// Skip trivial anon bursts.
	if event.AuthorURN.IsAnon() {
		return nil
	}

	// Step 1: group affected lines.
	groups := GroupAffectedLines(event.Entries, cfg.WindowLines, totalLines)
	if len(groups) == 0 {
		return nil
	}

	// Step 2: split oversized windows.
	var windows []LineRange
	for _, g := range groups {
		split := SplitRange(g, cfg.ChunkSize)
		windows = append(windows, split...)
	}

	// Step 3: extract text, tokenize, build bursts.
	var bursts []Burst
	now := time.Now().UTC()

	projectURN := ""
	if note.ProjectURN != nil {
		projectURN = note.ProjectURN.String()
	}
	folderURN := ""
	if note.FolderURN != nil {
		folderURN = note.FolderURN.String()
	}

	for _, w := range windows {
		// Extract lines for this window (0-based slice indexing).
		startIdx := w.Start - 1 // convert 1-based to 0-based
		endIdx := w.End         // exclusive upper bound for slice
		if startIdx < 0 {
			startIdx = 0
		}
		if endIdx > totalLines {
			endIdx = totalLines
		}

		windowLines := noteLines[startIdx:endIdx]

		// Strip blank lines from the edges.
		windowLines = trimBlankLines(windowLines)

		if len(windowLines) == 0 {
			continue
		}

		text := strings.Join(windowLines, "\n")
		if strings.TrimSpace(text) == "" {
			continue
		}

		tokens := TokenizeBurst(text)
		if len(tokens) < 3 {
			continue
		}

		// Generate a UUIDv7 for the burst ID.
		burstID, err := uuid.NewV7()
		if err != nil {
			// Extremely unlikely; skip this burst rather than panic.
			continue
		}

		burst := Burst{
			ID:         burstID.String(),
			NoteURN:    note.URN.String(),
			ProjectURN: projectURN,
			FolderURN:  folderURN,
			AuthorURN:  event.AuthorURN.String(),
			Sequence:   event.Sequence,
			LineStart:  w.Start,
			LineEnd:    w.End,
			Text:       text,
			Tokens:     tokens,
			Truncated:  false,
			CreatedAt:  now,
		}

		bursts = append(bursts, burst)
	}

	return bursts
}

// trimBlankLines strips leading and trailing blank lines from a slice.
func trimBlankLines(lines []string) []string {
	start := 0
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	end := len(lines)
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return lines[start:end]
}

// ---------------------------------------------------------------------------
// DetectCandidates
// ---------------------------------------------------------------------------

// DetectCandidates runs a single-stage Jaccard filter over recent bursts
// to find candidate relations for newBurst.
// recent should be bursts from different notes in the same project,
// limited to the last CandidateLookbackN entries.
// Returns pairs where jaccard >= threshold.
func DetectCandidates(newBurst Burst, recent []Burst, threshold float64) []CandidatePair {
	var pairs []CandidatePair

	for _, b := range recent {
		// Only consider bursts from different notes.
		if b.NoteURN == newBurst.NoteURN {
			continue
		}

		score := JaccardScore(newBurst.Tokens, b.Tokens)
		if score < threshold {
			continue
		}

		// Use the project URN from whichever burst has one set.
		projectURN := newBurst.ProjectURN
		if projectURN == "" {
			projectURN = b.ProjectURN
		}

		pairs = append(pairs, CandidatePair{
			BurstA:       newBurst,
			BurstB:       b,
			NoteURN_A:    newBurst.NoteURN,
			NoteURN_B:    b.NoteURN,
			ProjectURN:   projectURN,
			OverlapScore: score,
		})
	}

	return pairs
}

// ---------------------------------------------------------------------------
// ProjectBurstHint
// ---------------------------------------------------------------------------

// ProjectBurstHint is a lightweight burst reference used for project suggestion.
// It carries only the fields needed for Jaccard scoring against a note's bursts.
type ProjectBurstHint struct {
	ProjectURN string
	Tokens     []string
}

// ---------------------------------------------------------------------------
// ProjectSuggestion
// ---------------------------------------------------------------------------

// ProjectSuggestion is a ranked suggestion for which project a note without a
// project assignment should belong to, based on content similarity of existing
// bursts.
type ProjectSuggestion struct {
	ProjectURN string
	Score      float64 // average Jaccard similarity across matched bursts
	MatchCount int     // number of project bursts that scored above the threshold
}

// ---------------------------------------------------------------------------
// SuggestProjectForNote
// ---------------------------------------------------------------------------

// SuggestProjectForNote scores the note's existing bursts (noteTokenSets)
// against projectBursts (bursts from various notes that already have a project
// assigned) and returns ranked project suggestions ordered by descending score.
//
// noteTokenSets is the list of token sets from the note's own bursts.
// projectBursts is a flat list of hints from other notes that already have a
// project assigned; each must carry a non-empty ProjectURN and Tokens slice.
//
// Only project/burst pairs with Jaccard score >= threshold are counted.
// Projects are de-duplicated across burst pairs: for each project burst the
// best score against any of the note's bursts is used before accumulating.
// The threshold value from BurstConfig.OverlapThreshold is a good default.
func SuggestProjectForNote(noteTokenSets [][]string, projectBursts []ProjectBurstHint, threshold float64) []ProjectSuggestion {
	if len(noteTokenSets) == 0 || len(projectBursts) == 0 {
		return nil
	}

	type accumulator struct {
		totalScore float64
		count      int
	}
	byProject := make(map[string]*accumulator)

	for _, pb := range projectBursts {
		if pb.ProjectURN == "" || len(pb.Tokens) == 0 {
			continue
		}
		bestScore := 0.0
		for _, noteToks := range noteTokenSets {
			if s := JaccardScore(noteToks, pb.Tokens); s > bestScore {
				bestScore = s
			}
		}
		if bestScore < threshold {
			continue
		}
		acc, ok := byProject[pb.ProjectURN]
		if !ok {
			acc = &accumulator{}
			byProject[pb.ProjectURN] = acc
		}
		acc.totalScore += bestScore
		acc.count++
	}

	suggestions := make([]ProjectSuggestion, 0, len(byProject))
	for projURN, acc := range byProject {
		suggestions = append(suggestions, ProjectSuggestion{
			ProjectURN: projURN,
			Score:      acc.totalScore / float64(acc.count),
			MatchCount: acc.count,
		})
	}

	sort.Slice(suggestions, func(i, j int) bool {
		if suggestions[i].Score != suggestions[j].Score {
			return suggestions[i].Score > suggestions[j].Score
		}
		return suggestions[i].MatchCount > suggestions[j].MatchCount
	})

	return suggestions
}

// ---------------------------------------------------------------------------
// InferTitle
// ---------------------------------------------------------------------------

// InferTitle derives a human-readable title from note content lines.
// It scans the first 6 non-blank lines looking for one that yields at least
// 2 significant tokens (non-stop-word, length >= 2).
//
// Returns:
//   - title:      title-cased space-joined significant tokens (empty if inconclusive)
//   - confidence: float in [0.0, 1.0]; values < 0.4 indicate low confidence
//   - basisLine:  the raw content line the title was derived from
func InferTitle(lines []string) (title string, confidence float64, basisLine string) {
	for i, line := range lines {
		if i >= 6 {
			break
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// Tokenize: lowercase + split on non-alphanumeric.
		var sb strings.Builder
		for _, r := range strings.ToLower(trimmed) {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				sb.WriteRune(r)
			} else {
				sb.WriteRune(' ')
			}
		}
		rawTokens := strings.Fields(sb.String())

		// Filter: discard single-char tokens, then stop words.
		var significant []string
		for _, t := range rawTokens {
			if len(t) < 2 {
				continue
			}
			if _, isStop := slugStopWords[t]; isStop {
				continue
			}
			significant = append(significant, t)
		}
		if len(significant) < 2 {
			continue // not enough signal on this line; try the next
		}

		// Take up to 4 significant tokens.
		if len(significant) > 4 {
			significant = significant[:4]
		}

		// Title-case each token.
		parts := make([]string, len(significant))
		for j, t := range significant {
			parts[j] = strings.ToUpper(t[:1]) + t[1:]
		}

		conf := float64(len(significant)) / 5.0
		if conf > 1.0 {
			conf = 1.0
		}
		return strings.Join(parts, " "), conf, trimmed
	}
	return "", 0.0, ""
}
