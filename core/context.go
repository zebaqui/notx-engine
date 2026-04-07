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
	MaxWindows            int     // max sub-windows produced by one event (default: 10)
	SkipWindowSeconds     int     // look-back window for skip check in seconds (default: 300)
	SkipThreshold         float64 // Jaccard threshold to trigger consecutive skip (default: 0.80)
	MaxPerNotePerDay      int     // burst cap per note per day (default: 50)
	MaxPerProjectPerDay   int     // burst cap per project per day (default: 500)
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
		MaxWindows:            10,
		SkipWindowSeconds:     300,
		SkipThreshold:         0.80,
		MaxPerNotePerDay:      50,
		MaxPerProjectPerDay:   500,
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
	truncated := false
	for _, g := range groups {
		split := SplitRange(g, cfg.ChunkSize)
		windows = append(windows, split...)
	}

	// Step 3: cap at MaxWindows.
	if len(windows) > cfg.MaxWindows {
		windows = windows[:cfg.MaxWindows]
		truncated = true
	}

	// Step 4: extract text, tokenize, build bursts.
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

	for i, w := range windows {
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

		isTruncated := truncated && i == len(windows)-1

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
			Truncated:  isTruncated,
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
