package core

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// Anchor types
// ---------------------------------------------------------------------------

// AnchorStatus represents the health status of an anchor.
type AnchorStatus string

const (
	AnchorStatusOK         AnchorStatus = "ok"
	AnchorStatusBroken     AnchorStatus = "broken"
	AnchorStatusDeprecated AnchorStatus = "deprecated"
)

// Anchor represents a named position declared in a note's header anchor table.
// Format in .notx header: # anchor: <id>  line:<L>  char:<S>-<E>  [status:<status>]
type Anchor struct {
	ID        string       // [a-z0-9_-]+, max 128 chars, unique within note
	Line      int          // 1-based line number (position hint, not identity)
	CharStart int          // 0-based inclusive start of sub-line char range
	CharEnd   int          // 0-based inclusive end of sub-line char range (== CharStart means whole line)
	Status    AnchorStatus // ok, broken, or deprecated
	Preview   string       // optional: text content of the anchored span
}

// ---------------------------------------------------------------------------
// Link types
// ---------------------------------------------------------------------------

// LinkType identifies the kind of link token.
type LinkType string

const (
	LinkTypeNotxID      LinkType = "notx:lnk:id"   // cross-note or same-note ID link
	LinkTypeExternalURI LinkType = "world:lnk:uri" // external URI link
)

// LinkStatus represents the resolution status of a link.
type LinkStatus string

const (
	LinkStatusOK          LinkStatus = "ok"
	LinkStatusDrift       LinkStatus = "drift"
	LinkStatusBroken      LinkStatus = "broken"
	LinkStatusDeprecated  LinkStatus = "deprecated"
	LinkStatusNotFound    LinkStatus = "not_found"
	LinkStatusMalformed   LinkStatus = "malformed"
	LinkStatusRemoteError LinkStatus = "remote_error"
	LinkStatusUnresolved  LinkStatus = "unresolved"
)

// ParsedLink is the result of parsing a single link token from note content.
type ParsedLink struct {
	Token        string // the raw token string
	LinkType     LinkType
	TargetURN    string // note URN for notx:lnk:id, empty for world:lnk:uri
	TargetAnchor string // anchor ID for notx:lnk:id, empty for world:lnk:uri
	URI          string // for world:lnk:uri only
	Label        string // display text from [label](token) wrapper, if present
	Line         int    // 1-based line where token appears in note content
	CharStart    int    // 0-based start of token on that line
	CharEnd      int    // 0-based end of token on that line
	Status       LinkStatus
}

// ---------------------------------------------------------------------------
// Break-detection types
// ---------------------------------------------------------------------------

// AnchorBreak describes a hard break detected during anchor impact analysis.
type AnchorBreak struct {
	AnchorID          string
	NoteURN           string
	Line              int
	CharStart         int
	CharEnd           int
	Referrers         []string // note URNs that link to this anchor
	ResolutionOptions []string // "reassign", "delete", "force"
}

// BreakDetectionResult is returned by DetectAnchorBreaks when hard breaks exist.
type BreakDetectionResult struct {
	Status string // "ok" or "anchor_break_detected"
	Breaks []AnchorBreak
}

// ---------------------------------------------------------------------------
// Anchor validation & formatting
// ---------------------------------------------------------------------------

// anchorIDRe matches valid anchor IDs: [a-z0-9_-]+, max 128 chars.
var anchorIDRe = regexp.MustCompile(`^[a-z0-9_-]+$`)

// ValidateAnchorID reports whether id is a valid anchor identifier:
// [a-z0-9_-]+, max 128 chars.
func ValidateAnchorID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	return anchorIDRe.MatchString(id)
}

// ParseAnchorLine parses a single "# anchor: ..." header line.
// Returns (anchor, true) on success, (Anchor{}, false) on failure.
// Expected format: # anchor: <id>  line:<L>  char:<S>-<E>  [status:<status>]
func ParseAnchorLine(line string) (Anchor, bool) {
	// Strip the "# anchor:" prefix (allowing optional spaces around "#").
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "#") {
		return Anchor{}, false
	}
	after := strings.TrimSpace(trimmed[1:])
	if !strings.HasPrefix(after, "anchor:") {
		return Anchor{}, false
	}
	rest := strings.TrimSpace(after[len("anchor:"):])

	fields := strings.Fields(rest)
	// Minimum required: <id> line:<L> char:<S>-<E>
	if len(fields) < 3 {
		return Anchor{}, false
	}

	id := fields[0]
	if !ValidateAnchorID(id) {
		return Anchor{}, false
	}

	var lineNum, charStart, charEnd int
	var status AnchorStatus = AnchorStatusOK
	lineFound := false
	charFound := false

	for _, f := range fields[1:] {
		switch {
		case strings.HasPrefix(f, "line:"):
			val := f[len("line:"):]
			n, err := strconv.Atoi(val)
			if err != nil || n < 1 {
				return Anchor{}, false
			}
			lineNum = n
			lineFound = true

		case strings.HasPrefix(f, "char:"):
			val := f[len("char:"):]
			parts := strings.SplitN(val, "-", 2)
			if len(parts) != 2 {
				return Anchor{}, false
			}
			s, err1 := strconv.Atoi(parts[0])
			e, err2 := strconv.Atoi(parts[1])
			if err1 != nil || err2 != nil || s < 0 || e < 0 {
				return Anchor{}, false
			}
			charStart = s
			charEnd = e
			charFound = true

		case strings.HasPrefix(f, "status:"):
			val := f[len("status:"):]
			switch AnchorStatus(val) {
			case AnchorStatusOK, AnchorStatusBroken, AnchorStatusDeprecated:
				status = AnchorStatus(val)
			default:
				return Anchor{}, false
			}
		}
	}

	if !lineFound || !charFound {
		return Anchor{}, false
	}

	return Anchor{
		ID:        id,
		Line:      lineNum,
		CharStart: charStart,
		CharEnd:   charEnd,
		Status:    status,
	}, true
}

// FormatAnchorLine formats an Anchor as a .notx header line.
// Returns e.g.: # anchor: node-reject  line:5  char:0-0
// If status is non-ok and non-empty, appends  status:<status>
func FormatAnchorLine(a Anchor) string {
	s := fmt.Sprintf("# anchor: %s  line:%d  char:%d-%d", a.ID, a.Line, a.CharStart, a.CharEnd)
	if a.Status != "" && a.Status != AnchorStatusOK {
		s += fmt.Sprintf("  status:%s", a.Status)
	}
	return s
}

// ---------------------------------------------------------------------------
// Link token parsing
// ---------------------------------------------------------------------------

// ParseLinkToken parses a single link token string (no wrapper).
// Handles:
//
//	notx:lnk:id:urn:notx:note:<uuid>:<anchor-id>   (cross-note)
//	notx:lnk:id::<anchor-id>                         (same-note self-ref)
//	world:lnk:uri:<uri>                              (external)
//
// Returns (ParsedLink, nil) on success, error on malformed input.
func ParseLinkToken(token string) (ParsedLink, error) {
	switch {
	case strings.HasPrefix(token, "world:lnk:uri:"):
		uri := token[len("world:lnk:uri:"):]
		if uri == "" {
			return ParsedLink{}, fmt.Errorf("links: malformed world:lnk:uri token: empty URI in %q", token)
		}
		return ParsedLink{
			Token:    token,
			LinkType: LinkTypeExternalURI,
			URI:      uri,
			Status:   LinkStatusUnresolved,
		}, nil

	case strings.HasPrefix(token, "notx:lnk:id:"):
		return parseNotxIDToken(token)

	default:
		return ParsedLink{}, fmt.Errorf("links: unrecognised link token prefix in %q", token)
	}
}

// parseNotxIDToken handles the notx:lnk:id: family of tokens.
//
// The token after stripping the "notx:lnk:id:" prefix is either:
//   - ":"<anchor>                          — self-reference (empty URN segment)
//   - "urn:notx:note:<uuid>:<anchor>"      — cross-note reference
func parseNotxIDToken(token string) (ParsedLink, error) {
	// Everything after "notx:lnk:id:"
	body := token[len("notx:lnk:id:"):]

	// Self-reference: body starts with ":" meaning the note URN is empty.
	if strings.HasPrefix(body, ":") {
		anchor := body[1:]
		if anchor == "" {
			return ParsedLink{}, fmt.Errorf("links: malformed notx:lnk:id token: empty anchor in %q", token)
		}
		if !ValidateAnchorID(anchor) {
			return ParsedLink{}, fmt.Errorf("links: malformed notx:lnk:id token: invalid anchor ID %q in %q", anchor, token)
		}
		return ParsedLink{
			Token:        token,
			LinkType:     LinkTypeNotxID,
			TargetURN:    "",
			TargetAnchor: anchor,
			Status:       LinkStatusUnresolved,
		}, nil
	}

	// Cross-note: body == "urn:notx:note:<uuid>:<anchor>"
	// The URN portion is "urn:notx:note:<uuid>" (4 colon-separated segments).
	// We split on ":" and expect at least 5 parts total:
	//   [0]=urn [1]=notx [2]=note [3]=<uuid> [4]=<anchor>
	// The UUID itself may contain hyphens but not colons, so a simple split works.
	parts := strings.SplitN(body, ":", 6)
	// parts: ["urn","notx","note","<uuid>","<anchor>"] or more
	if len(parts) < 5 {
		return ParsedLink{}, fmt.Errorf("links: malformed notx:lnk:id cross-note token: too few segments in %q", token)
	}
	if parts[0] != "urn" || parts[1] != "notx" || parts[2] != "note" {
		return ParsedLink{}, fmt.Errorf("links: malformed notx:lnk:id cross-note token: unexpected prefix in %q", token)
	}

	noteURN := strings.Join(parts[0:4], ":")
	anchor := parts[4]

	// Validate the note URN syntactically.
	if _, err := ParseURN(noteURN); err != nil {
		return ParsedLink{}, fmt.Errorf("links: malformed notx:lnk:id cross-note token: invalid note URN in %q: %w", token, err)
	}
	if anchor == "" {
		return ParsedLink{}, fmt.Errorf("links: malformed notx:lnk:id cross-note token: empty anchor in %q", token)
	}
	if !ValidateAnchorID(anchor) {
		return ParsedLink{}, fmt.Errorf("links: malformed notx:lnk:id cross-note token: invalid anchor ID %q in %q", anchor, token)
	}

	return ParsedLink{
		Token:        token,
		LinkType:     LinkTypeNotxID,
		TargetURN:    noteURN,
		TargetAnchor: anchor,
		Status:       LinkStatusUnresolved,
	}, nil
}

// ---------------------------------------------------------------------------
// Link extraction from content lines
// ---------------------------------------------------------------------------

// linkPrefixes are the raw token prefixes we scan for in content lines.
var linkPrefixes = []string{"notx:lnk:", "world:lnk:"}

// ExtractLinks scans all lines of note content and extracts every link token.
// Handles both plain tokens and [label](token) wrappers.
// Returns all parsed links found in the content.
func ExtractLinks(lines []string) []ParsedLink {
	var results []ParsedLink

	for lineIdx, line := range lines {
		lineNum := lineIdx + 1 // 1-based

		// We scan the line character by character looking for link prefixes.
		// We also need to detect [label](token) wrappers.
		i := 0
		for i < len(line) {
			// Check if we're at a link prefix.
			prefixStart, prefix := findLinkPrefix(line, i)
			if prefixStart < 0 {
				break
			}

			// Check whether this token is wrapped in [label](...).
			label, tokenStart, wrappedStart := extractLabelWrapper(line, prefixStart)
			if wrappedStart < 0 {
				tokenStart = prefixStart
			}

			// Extract the token: runs until whitespace or ')' (when wrapped) or end of line.
			token, tokenEnd := extractToken(line, tokenStart, wrappedStart >= 0)

			if token == "" || !strings.HasPrefix(token, prefix) {
				i = prefixStart + len(prefix)
				continue
			}

			pl, err := ParseLinkToken(token)
			if err != nil {
				// Record a malformed link so callers can surface it.
				results = append(results, ParsedLink{
					Token:     token,
					LinkType:  LinkType(prefix[:len(prefix)-1]), // strip trailing ":"
					Label:     label,
					Line:      lineNum,
					CharStart: tokenStart,
					CharEnd:   tokenEnd - 1,
					Status:    LinkStatusMalformed,
				})
				i = tokenEnd
				continue
			}

			pl.Label = label
			pl.Line = lineNum
			pl.CharStart = tokenStart
			pl.CharEnd = tokenEnd - 1

			results = append(results, pl)
			i = tokenEnd

			// If wrapped, skip past the closing ')'.
			if wrappedStart >= 0 && i < len(line) && line[i] == ')' {
				i++
			}
		}
	}

	return results
}

// findLinkPrefix returns the index and matched prefix of the first link prefix
// found in line at or after position start. Returns (-1, "") if none found.
func findLinkPrefix(line string, start int) (int, string) {
	best := -1
	bestPrefix := ""
	for _, p := range linkPrefixes {
		idx := strings.Index(line[start:], p)
		if idx < 0 {
			continue
		}
		abs := start + idx
		if best < 0 || abs < best {
			best = abs
			bestPrefix = p
		}
	}
	return best, bestPrefix
}

// extractLabelWrapper checks whether the link token starting at tokenStart is
// inside a [label](token) Markdown-style wrapper. It looks backwards from
// tokenStart for the "](” pattern.
//
// Returns:
//   - label:        the text between "[" and "]"
//   - tokenStart:   the index in line where the token starts (unchanged)
//   - wrappedStart: the index of "(" if this is a wrapped token, -1 otherwise
func extractLabelWrapper(line string, tokenStart int) (label string, _ int, wrappedStart int) {
	// We expect "]('' immediately before tokenStart.
	if tokenStart < 2 {
		return "", tokenStart, -1
	}
	if line[tokenStart-1] != '(' {
		return "", tokenStart, -1
	}
	// Find the matching "[" and "]" scanning leftward from tokenStart-2.
	closeBracket := tokenStart - 2
	if closeBracket < 0 || line[closeBracket] != ']' {
		return "", tokenStart, -1
	}
	// Find the opening "[".
	openBracket := strings.LastIndex(line[:closeBracket], "[")
	if openBracket < 0 {
		return "", tokenStart, -1
	}
	label = line[openBracket+1 : closeBracket]
	return label, tokenStart, tokenStart - 1
}

// extractToken extracts a token from line starting at start.
// If wrapped is true the token ends at ')'; otherwise it ends at whitespace or end of line.
// Returns (token, endIndex) where endIndex is the first character after the token.
func extractToken(line string, start int, wrapped bool) (string, int) {
	end := start
	for end < len(line) {
		ch := line[end]
		if wrapped && ch == ')' {
			break
		}
		if !wrapped && (ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r') {
			break
		}
		end++
	}
	return line[start:end], end
}

// ---------------------------------------------------------------------------
// Self-reference expansion
// ---------------------------------------------------------------------------

// ExpandSelfReference expands a same-note self-reference token
// (notx:lnk:id::<anchor>) to its full form using the given note URN.
func ExpandSelfReference(token, noteURN string) string {
	if !strings.HasPrefix(token, "notx:lnk:id::") {
		return token
	}
	anchor := token[len("notx:lnk:id::"):]
	return fmt.Sprintf("notx:lnk:id:%s:%s", noteURN, anchor)
}

// ---------------------------------------------------------------------------
// Anchor break detection
// ---------------------------------------------------------------------------

// DetectAnchorBreaks analyzes a set of pending line edits (from an Event's Entries)
// against the note's current anchor table to detect hard breaks.
// Returns a BreakDetectionResult.
func DetectAnchorBreaks(anchors []Anchor, entries []LineEntry, noteURN string, referrers map[string][]string) BreakDetectionResult {
	// Build a set of lines that are being deleted or have their content changed
	// (LineOpSet with non-trivial effect, or LineOpDelete).
	// These represent potential hard breaks.
	type opInfo struct {
		op      LineOp
		content string
	}
	lineOps := make(map[int]opInfo)
	for _, e := range entries {
		switch e.Op {
		case LineOpDelete:
			lineOps[e.LineNumber] = opInfo{op: LineOpDelete}
		case LineOpSet, LineOpSetEmpty:
			lineOps[e.LineNumber] = opInfo{op: e.Op, content: e.Content}
		}
	}

	var breaks []AnchorBreak

	for _, a := range anchors {
		info, affected := lineOps[a.Line]
		if !affected {
			continue
		}

		isHardBreak := false
		switch info.op {
		case LineOpDelete:
			isHardBreak = true
		case LineOpSet:
			// Content change on an anchored line is a hard break.
			isHardBreak = true
		case LineOpSetEmpty:
			// Setting an anchored line to empty is a hard break.
			isHardBreak = true
		}

		if !isHardBreak {
			continue
		}

		var refs []string
		if referrers != nil {
			anchorKey := noteURN + "#" + a.ID
			refs = referrers[anchorKey]
		}

		breaks = append(breaks, AnchorBreak{
			AnchorID:          a.ID,
			NoteURN:           noteURN,
			Line:              a.Line,
			CharStart:         a.CharStart,
			CharEnd:           a.CharEnd,
			Referrers:         refs,
			ResolutionOptions: []string{"reassign", "delete", "force"},
		})
	}

	if len(breaks) == 0 {
		return BreakDetectionResult{Status: "ok"}
	}
	return BreakDetectionResult{
		Status: "anchor_break_detected",
		Breaks: breaks,
	}
}

// ---------------------------------------------------------------------------
// Anchor drift update
// ---------------------------------------------------------------------------

// UpdateAnchorDrift adjusts anchor line hints when lines are inserted/deleted
// above anchored lines (no content break — position drift only).
// Returns the updated anchor slice.
func UpdateAnchorDrift(anchors []Anchor, entries []LineEntry) []Anchor {
	// For drift purposes we only care about insertions (new LineOpSet on a line
	// number > existing max, effectively pushing lines down) and deletions of
	// lines above an anchor. We process entries in order and accumulate a
	// per-line-number offset shift.
	//
	// Strategy: build a sorted list of (lineNumber, delta) events and apply
	// them to each anchor's Line field. Deletes of a line shift everything
	// above that line down by -1; inserts (Set on a line that previously didn't
	// exist) shift everything at or below that line up by +1.
	//
	// We use the simplest correct approach: sort entries by line number
	// ascending, then for each anchor accumulate the net offset contributed
	// by entries with lineNumber <= anchor.Line.

	type delta struct {
		line int
		d    int // +1 for insert, -1 for delete
	}

	var deltas []delta
	for _, e := range entries {
		switch e.Op {
		case LineOpDelete:
			deltas = append(deltas, delta{line: e.LineNumber, d: -1})
		case LineOpSet:
			// A Set on a line is an insert only if we treat it as inserting content;
			// for drift purposes, a Set that creates a new line shifts subsequent
			// anchors. We count it as +1 shift for lines above.
			// Per spec: drift is only for lines inserted/deleted above an anchor.
			// We include LineOpSet here conservatively.
			deltas = append(deltas, delta{line: e.LineNumber, d: +1})
		}
	}

	// Sort by line number ascending for determinism.
	sort.Slice(deltas, func(i, j int) bool {
		return deltas[i].line < deltas[j].line
	})

	updated := make([]Anchor, len(anchors))
	for i, a := range anchors {
		shift := 0
		for _, d := range deltas {
			if d.line < a.Line {
				shift += d.d
			}
		}
		a.Line += shift
		if a.Line < 1 {
			a.Line = 1
		}
		updated[i] = a
	}

	return updated
}

// ---------------------------------------------------------------------------
// Slug generation
// ---------------------------------------------------------------------------

// slugStopWords is the global set of function words removed in slug generation.
var slugStopWords = map[string]struct{}{
	"a": {}, "an": {}, "the": {}, "and": {}, "or": {}, "but": {},
	"is": {}, "are": {}, "was": {}, "were": {}, "be": {}, "been": {},
	"for": {}, "to": {}, "in": {}, "on": {}, "at": {}, "by": {}, "of": {},
	"it": {}, "its": {}, "this": {}, "that": {}, "with": {}, "from": {},
	"not": {}, "do": {}, "did": {}, "has": {}, "have": {}, "will": {}, "can": {},
}

// SlugFromText derives a short human-readable anchor ID from burst text.
//
// Algorithm (from spec):
//  1. Lowercase + split on non-alphanumeric
//  2. Remove single-char tokens
//  3. Remove global function words
//  4. Take first 2-3 remaining tokens (min 2, max 3)
//  5. Join with hyphens, truncate to 40 chars
//
// Fallback (fewer than 2 tokens survive step 3):
//
//	a. Take first token of length >= 2 from raw text (no stop-word filter)
//	b. Append hyphen + first 6 hex chars of SHA-256 of first 30 chars of raw text
//	c. Truncate to 40 chars
//
// If existingIDs contains the slug, append -2, -3, ... until unique.
func SlugFromText(text string, existingIDs map[string]struct{}) string {
	// Step 1: lowercase + split on non-alphanumeric.
	lower := strings.ToLower(text)
	var sb strings.Builder
	for _, r := range lower {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
		} else {
			sb.WriteRune(' ')
		}
	}
	rawTokens := strings.Fields(sb.String())

	// Step 2: remove single-char tokens.
	var filtered []string
	for _, t := range rawTokens {
		if len(t) >= 2 {
			filtered = append(filtered, t)
		}
	}

	// Step 3: remove stop words.
	var meaningful []string
	for _, t := range filtered {
		if _, isStop := slugStopWords[t]; !isStop {
			meaningful = append(meaningful, t)
		}
	}

	var slug string

	if len(meaningful) >= 2 {
		// Step 4: take first 2-3 tokens.
		take := 3
		if len(meaningful) < take {
			take = len(meaningful)
		}
		slug = strings.Join(meaningful[:take], "-")

		// Step 5: truncate to 40 chars.
		if len(slug) > 40 {
			slug = slug[:40]
			// Trim trailing hyphen fragment.
			slug = strings.TrimRight(slug, "-")
		}
	} else {
		// Fallback: take first token of length >= 2 from raw (no stop-word filter).
		base := ""
		for _, t := range rawTokens {
			if len(t) >= 2 {
				base = t
				break
			}
		}
		if base == "" {
			base = "anchor"
		}

		// Hash: first 30 chars of original text.
		source := text
		if len(source) > 30 {
			source = source[:30]
		}
		sum := sha256.Sum256([]byte(source))
		hashHex := fmt.Sprintf("%x", sum[:3]) // 6 hex chars from first 3 bytes

		slug = base + "-" + hashHex
		if len(slug) > 40 {
			slug = slug[:40]
			slug = strings.TrimRight(slug, "-")
		}
	}

	// Ensure uniqueness.
	candidate := slug
	if existingIDs == nil {
		return candidate
	}
	if _, exists := existingIDs[candidate]; !exists {
		return candidate
	}

	for n := 2; ; n++ {
		candidate = fmt.Sprintf("%s-%d", slug, n)
		if len(candidate) > 40 {
			// Trim the base to make room for the suffix.
			suffix := fmt.Sprintf("-%d", n)
			trimmed := slug
			if len(trimmed)+len(suffix) > 40 {
				trimmed = slug[:40-len(suffix)]
				trimmed = strings.TrimRight(trimmed, "-")
			}
			candidate = trimmed + suffix
		}
		if _, exists := existingIDs[candidate]; !exists {
			return candidate
		}
	}
}
