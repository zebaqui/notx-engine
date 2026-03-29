package core

import (
	"fmt"
	"strings"
	"time"
)

// Note is the central entity of the notx engine. It holds all in-memory state
// for a single notx document: its metadata, the full ordered event stream, and
// any snapshot checkpoints.
//
// A Note is the source of truth for the document's current and historical
// content. All mutations go through ApplyEvent; the content is derived
// exclusively by replaying events (or using snapshots as a starting point).
//
// Note is NOT safe for concurrent use. Callers that need concurrent access must
// synchronise externally.
type Note struct {
	// --- Identity & Metadata ------------------------------------------------

	// URN is the globally unique identifier for this note.
	// Format: <namespace>:note:<uuid>
	URN URN

	// Name is the human-readable title / display name of the note.
	Name string

	// NoteType classifies this note as either a normal note or a secure
	// (end-to-end encrypted) note. It is set at creation time and is
	// immutable for the lifetime of the note.
	// Absence of the field in a .notx file defaults to NoteTypeNormal.
	NoteType NoteType

	// ProjectURN is the optional URN of the project this note belongs to.
	// Nil when the note is not associated with any project.
	ProjectURN *URN

	// FolderURN is the optional URN of the folder that contains this note.
	// Nil when the note is not inside any folder.
	FolderURN *URN

	// ParentURN is the optional URN of the parent note (for hierarchical notes).
	// Nil when the note has no parent.
	ParentURN *URN

	// NodeLinks is a free-form map of named graph links to other note URNs.
	// Keys are arbitrary labels (e.g. "requirements", "api_docs"); values are
	// note URNs that may live on any instance (cross-instance references are
	// allowed).
	NodeLinks map[string]URN

	// Deleted indicates whether this note has been soft-deleted.
	Deleted bool

	// CreatedAt is the UTC timestamp of when the note was originally created.
	// This field is immutable once set.
	CreatedAt time.Time

	// UpdatedAt is the UTC timestamp of the last applied event. It is derived
	// from the most-recently applied event's CreatedAt and updated automatically
	// by ApplyEvent.
	UpdatedAt time.Time

	// --- Event Stream -------------------------------------------------------

	// events is the ordered, append-only list of all history events.
	// Invariant: events[i].Sequence == i+1 for all i.
	events []*Event

	// --- Snapshot Cache -----------------------------------------------------

	// snapshots holds the optional materialized checkpoints, ordered by
	// ascending Sequence. They are an optimisation only; correctness never
	// depends on them.
	snapshots []*Snapshot
}

// NewNote creates a new, empty Note with the given URN, name, creation
// timestamp, and note type. The event stream is empty; HeadSequence() returns 0.
//
// Pass NoteTypeNormal for a standard note or NoteTypeSecure for an
// end-to-end encrypted note. The type is immutable once set.
func NewNote(urn URN, name string, createdAt time.Time) *Note {
	return &Note{
		URN:       urn,
		Name:      name,
		NoteType:  NoteTypeNormal,
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
		NodeLinks: make(map[string]URN),
	}
}

// NewNoteAtSequence creates a Note pre-seeded with a snapshot at the given
// headSequence and content string. This is used by the file provider's Get
// fast path to reconstruct a note from Badger's materialised cache without
// replaying the full event history from disk.
//
// The returned note has:
//   - HeadSequence() == headSequence
//   - Content() == content (if non-empty)
//   - No events in its event stream (only a snapshot checkpoint)
//
// The note is ready to accept AppendEvent calls for sequence headSequence+1.
func NewNoteAtSequence(urn URN, name string, createdAt, updatedAt time.Time, headSequence int, content string) *Note {
	n := &Note{
		URN:       urn,
		Name:      name,
		NoteType:  NoteTypeNormal,
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
		NodeLinks: make(map[string]URN),
	}
	if headSequence < 1 || content == "" {
		return n
	}
	// Seed the event slice with headSequence placeholder entries so that
	// HeadSequence() returns the correct value. Each placeholder carries a
	// single no-content marker entry; the snapshot below provides the actual
	// content so linesAt() never needs to replay these placeholders.
	n.events = make([]*Event, headSequence)
	for i := 0; i < headSequence; i++ {
		n.events[i] = &Event{
			NoteURN:   urn,
			Sequence:  i + 1,
			AuthorURN: AnonURN(urn.Namespace),
			CreatedAt: createdAt,
			Entries:   []LineEntry{{Op: LineOpSetEmpty, LineNumber: 1}},
		}
	}
	n.snapshots = []*Snapshot{
		{
			NoteURN:   urn,
			Sequence:  headSequence,
			Lines:     SplitLines(content),
			CreatedAt: updatedAt,
		},
	}
	return n
}

// NewSecureNote creates a new, empty secure (E2EE) Note.
// It is identical to NewNote except that NoteType is set to NoteTypeSecure
// and cannot be changed.
func NewSecureNote(urn URN, name string, createdAt time.Time) *Note {
	n := NewNote(urn, name, createdAt)
	n.NoteType = NoteTypeSecure
	return n
}

// SecurityPolicy returns the immutable SecurityPolicy derived from the note's
// NoteType. See NoteSecurityPolicy for the full attribute set.
func (n *Note) SecurityPolicy() SecurityPolicy {
	return NoteSecurityPolicy(n.NoteType)
}

// ──────────────────────────────────────────────────────────────────────────────
// Sequence & basic accessors
// ──────────────────────────────────────────────────────────────────────────────

// HeadSequence returns the sequence number of the last applied event, or 0 if
// no events have been applied yet.
func (n *Note) HeadSequence() int {
	return len(n.events)
}

// EventCount returns the total number of events in the note's history.
func (n *Note) EventCount() int {
	return len(n.events)
}

// Events returns a shallow copy of the ordered event slice. Callers must not
// mutate the returned Event values.
func (n *Note) Events() []*Event {
	if len(n.events) == 0 {
		return nil
	}
	cp := make([]*Event, len(n.events))
	copy(cp, n.events)
	return cp
}

// EventAt returns the event at the given 1-based sequence number, or an error
// if the sequence is out of range.
func (n *Note) EventAt(sequence int) (*Event, error) {
	if sequence < 1 || sequence > len(n.events) {
		return nil, fmt.Errorf("note: sequence %d out of range [1, %d]", sequence, len(n.events))
	}
	return n.events[sequence-1], nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Applying events
// ──────────────────────────────────────────────────────────────────────────────

// ApplyEvent appends a new event to the note's history and returns an error if
// the event is invalid.
//
// Validation rules:
//   - The event's Sequence must equal HeadSequence()+1 (no gaps, no rewrites).
//   - The event must have at least one LineEntry.
//   - Each LineEntry's LineNumber must be >= 1.
//
// On success the note's UpdatedAt is advanced to the event's CreatedAt.
func (n *Note) ApplyEvent(e *Event) error {
	want := n.HeadSequence() + 1
	if e.Sequence != want {
		return fmt.Errorf(
			"note: cannot apply event with sequence %d: expected %d",
			e.Sequence, want,
		)
	}
	if len(e.Entries) == 0 {
		return fmt.Errorf("note: event %d has no line entries", e.Sequence)
	}
	for i, entry := range e.Entries {
		if entry.LineNumber < 1 {
			return fmt.Errorf(
				"note: event %d entry[%d] has invalid line number %d (must be >= 1)",
				e.Sequence, i, entry.LineNumber,
			)
		}
	}

	n.events = append(n.events, e)
	if e.CreatedAt.After(n.UpdatedAt) {
		n.UpdatedAt = e.CreatedAt
	}
	return nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Content materialisation
// ──────────────────────────────────────────────────────────────────────────────

// Content returns the fully materialised document text at the current head
// sequence. Lines are joined with '\n'. Returns an empty string when no events
// have been applied.
func (n *Note) Content() string {
	lines, _ := n.linesAt(n.HeadSequence())
	return joinLines(lines)
}

// ContentAt returns the fully materialised document text at the given sequence
// number. It uses the nearest snapshot (sequence <= target) as a starting point
// and replays only the remaining events, bounding replay cost.
//
// sequence == 0 returns an empty string (the implicit initial state).
// sequence > HeadSequence() returns an error.
func (n *Note) ContentAt(sequence int) (string, error) {
	if sequence < 0 {
		return "", fmt.Errorf("note: sequence must be >= 0, got %d", sequence)
	}
	if sequence > n.HeadSequence() {
		return "", fmt.Errorf(
			"note: sequence %d is beyond head sequence %d",
			sequence, n.HeadSequence(),
		)
	}
	lines, err := n.linesAt(sequence)
	if err != nil {
		return "", err
	}
	return joinLines(lines), nil
}

// LinesAt returns the document lines at the given sequence as a slice of
// strings (one element per line). The returned slice is a fresh copy; callers
// may mutate it freely.
func (n *Note) LinesAt(sequence int) ([]string, error) {
	if sequence < 0 {
		return nil, fmt.Errorf("note: sequence must be >= 0, got %d", sequence)
	}
	if sequence > n.HeadSequence() {
		return nil, fmt.Errorf(
			"note: sequence %d is beyond head sequence %d",
			sequence, n.HeadSequence(),
		)
	}
	return n.linesAt(sequence)
}

// linesAt materialises the document at the requested sequence using the nearest
// snapshot as a starting point.
func (n *Note) linesAt(target int) ([]string, error) {
	// Fast path: empty document.
	if target == 0 {
		return []string{}, nil
	}

	// Find the nearest snapshot whose sequence is <= target.
	var lines []string
	startSeq := 0

	if snap := n.nearestSnapshot(target); snap != nil {
		// Seed from snapshot; deep-copy so replay does not mutate the snapshot.
		lines = make([]string, len(snap.Lines))
		copy(lines, snap.Lines)
		startSeq = snap.Sequence
	} else {
		lines = []string{}
	}

	// Replay events from (startSeq+1) through target.
	for seq := startSeq + 1; seq <= target; seq++ {
		e := n.events[seq-1]
		lines = applyEvent(lines, e)
	}

	return lines, nil
}

// applyEvent applies a single event's line entries to a lines slice and returns
// the updated slice. The input slice is mutated in-place; the same slice (or a
// grown one) is returned.
//
// Line numbers within an event are interpreted relative to the document state
// BEFORE the event began (batch semantics). Deletions are applied in reverse
// line-number order so that index shifting does not affect subsequent entries.
func applyEvent(lines []string, e *Event) []string {
	// Separate deletions from set operations to use the batch approach
	// described in NOTX_FILE_SEMANTICS.md §Event Semantics.
	type setOp struct {
		lineNumber int
		content    string
		empty      bool
	}

	var sets []setOp
	var deletes []int // line numbers, collected then applied highest-first

	for _, entry := range e.Entries {
		switch entry.Op {
		case LineOpDelete:
			deletes = append(deletes, entry.LineNumber)
		case LineOpSetEmpty:
			sets = append(sets, setOp{lineNumber: entry.LineNumber, empty: true})
		default: // LineOpSet
			sets = append(sets, setOp{lineNumber: entry.LineNumber, content: entry.Content})
		}
	}

	// Apply set / append operations first (they do not shift indices).
	for _, op := range sets {
		idx := op.lineNumber - 1 // convert to 0-based
		switch {
		case idx < len(lines):
			// Replace existing line.
			if op.empty {
				lines[idx] = ""
			} else {
				lines[idx] = op.content
			}
		case idx == len(lines):
			// Append immediately after current last line.
			if op.empty {
				lines = append(lines, "")
			} else {
				lines = append(lines, op.content)
			}
		default:
			// Gap beyond current end: fill with empty lines then append.
			for len(lines) < idx {
				lines = append(lines, "")
			}
			if op.empty {
				lines = append(lines, "")
			} else {
				lines = append(lines, op.content)
			}
		}
	}

	// Apply deletions from highest line number to lowest to avoid index drift.
	sortDeletesDesc(deletes)
	for _, lineNum := range deletes {
		idx := lineNum - 1
		if idx < 0 || idx >= len(lines) {
			// Idempotent: deleting a non-existent line is a no-op.
			continue
		}
		lines = append(lines[:idx], lines[idx+1:]...)
	}

	return lines
}

// sortDeletesDesc performs a simple insertion sort (descending) on a small
// slice of line-number integers. Avoids importing "sort" for this trivial case.
func sortDeletesDesc(ns []int) {
	for i := 1; i < len(ns); i++ {
		key := ns[i]
		j := i - 1
		for j >= 0 && ns[j] < key {
			ns[j+1] = ns[j]
			j--
		}
		ns[j+1] = key
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Snapshot management
// ──────────────────────────────────────────────────────────────────────────────

// AddSnapshot registers a pre-built snapshot checkpoint with the note.
// The snapshot's Sequence must be in the range [1, HeadSequence()].
// Duplicate snapshots for the same sequence replace the previous one.
func (n *Note) AddSnapshot(s *Snapshot) error {
	if s.Sequence < 1 || s.Sequence > n.HeadSequence() {
		return fmt.Errorf(
			"note: snapshot sequence %d is out of range [1, %d]",
			s.Sequence, n.HeadSequence(),
		)
	}

	// Replace existing snapshot at the same sequence if present.
	for i, existing := range n.snapshots {
		if existing.Sequence == s.Sequence {
			n.snapshots[i] = s
			return nil
		}
	}

	// Insert in sorted order (ascending by Sequence).
	inserted := false
	for i, existing := range n.snapshots {
		if existing.Sequence > s.Sequence {
			n.snapshots = append(n.snapshots, nil)
			copy(n.snapshots[i+1:], n.snapshots[i:])
			n.snapshots[i] = s
			inserted = true
			break
		}
	}
	if !inserted {
		n.snapshots = append(n.snapshots, s)
	}
	return nil
}

// BuildSnapshot materialises the document at the given sequence, stores it as a
// snapshot, and returns it. This is the canonical way to create a new snapshot
// for this note.
func (n *Note) BuildSnapshot(sequence int) (*Snapshot, error) {
	lines, err := n.LinesAt(sequence)
	if err != nil {
		return nil, fmt.Errorf("note: build snapshot: %w", err)
	}

	snap := &Snapshot{
		NoteURN:   n.URN,
		Sequence:  sequence,
		Lines:     lines,
		CreatedAt: time.Now().UTC(),
	}
	if err := n.AddSnapshot(snap); err != nil {
		return nil, err
	}
	return snap, nil
}

// Snapshots returns a shallow copy of the note's snapshot slice, ordered by
// ascending Sequence. Callers must not mutate the returned Snapshot values.
func (n *Note) Snapshots() []*Snapshot {
	if len(n.snapshots) == 0 {
		return nil
	}
	cp := make([]*Snapshot, len(n.snapshots))
	copy(cp, n.snapshots)
	return cp
}

// nearestSnapshot returns the snapshot with the largest Sequence that is still
// <= target, or nil if no such snapshot exists.
func (n *Note) nearestSnapshot(target int) *Snapshot {
	var best *Snapshot
	for _, s := range n.snapshots {
		if s.Sequence <= target {
			best = s
		} else {
			break // snapshots are sorted ascending
		}
	}
	return best
}

// ──────────────────────────────────────────────────────────────────────────────
// History & diff helpers
// ──────────────────────────────────────────────────────────────────────────────

// HistoryEntry summarises one event in the note's history for display purposes.
type HistoryEntry struct {
	Sequence  int
	AuthorURN URN
	CreatedAt time.Time
	Label     string
	// Entries is a copy of the raw line operations from the event.
	Entries []LineEntry
}

// History returns a slice of HistoryEntry values, one per event, ordered by
// ascending sequence. It provides a lightweight view of the event stream
// without exposing the full Event internals.
func (n *Note) History() []HistoryEntry {
	entries := make([]HistoryEntry, len(n.events))
	for i, e := range n.events {
		cpEntries := make([]LineEntry, len(e.Entries))
		copy(cpEntries, e.Entries)
		entries[i] = HistoryEntry{
			Sequence:  e.Sequence,
			AuthorURN: e.AuthorURN,
			CreatedAt: e.CreatedAt,
			Label:     e.Label,
			Entries:   cpEntries,
		}
	}
	return entries
}

// DiffAt returns the LineEntry operations applied by the event at the given
// sequence number. It is a convenience wrapper around EventAt for callers that
// only need the delta without the full Event struct.
func (n *Note) DiffAt(sequence int) ([]LineEntry, error) {
	e, err := n.EventAt(sequence)
	if err != nil {
		return nil, err
	}
	cp := make([]LineEntry, len(e.Entries))
	copy(cp, e.Entries)
	return cp, nil
}

// AuthorURNs returns the de-duplicated set of author URNs that appear across
// all events, in the order they first appear.
func (n *Note) AuthorURNs() []URN {
	seen := make(map[string]struct{})
	var result []URN
	for _, e := range n.events {
		key := e.AuthorURN.String()
		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
			result = append(result, e.AuthorURN)
		}
	}
	return result
}

// ──────────────────────────────────────────────────────────────────────────────
// Utility
// ──────────────────────────────────────────────────────────────────────────────

// DiffLines computes the minimal set of LineEntry operations needed to
// transform the document's current content into newContent.
//
// The algorithm does a line-by-line comparison:
//   - Lines that exist in both old and new at the same position with the same
//     content produce no entry (no-op).
//   - Lines that changed produce a LineOpSet (or LineOpSetEmpty for blank lines).
//   - Lines that exist in old but not in new (i.e. the new document is shorter)
//     produce LineOpDelete entries, emitted highest-to-lowest so the caller can
//     feed them directly into an Event without worrying about index drift.
//
// The returned slice is empty (not nil) when old and new are identical.
// sequence is passed through to help callers build the Event but is not used
// internally by this function.
func DiffLines(oldLines, newLines []string) []LineEntry {
	oldLen := len(oldLines)
	newLen := len(newLines)

	var entries []LineEntry

	// Walk the overlapping region.
	limit := oldLen
	if newLen > oldLen {
		limit = newLen
	}

	for i := 0; i < limit; i++ {
		lineNum := i + 1 // 1-based

		if i < newLen {
			// Line exists in new document.
			newLine := newLines[i]
			if i >= oldLen {
				// New document is longer — append line.
				if newLine == "" {
					entries = append(entries, LineEntry{LineNumber: lineNum, Op: LineOpSetEmpty})
				} else {
					entries = append(entries, LineEntry{LineNumber: lineNum, Op: LineOpSet, Content: newLine})
				}
			} else {
				// Line exists in both — only emit if changed.
				if oldLines[i] != newLine {
					if newLine == "" {
						entries = append(entries, LineEntry{LineNumber: lineNum, Op: LineOpSetEmpty})
					} else {
						entries = append(entries, LineEntry{LineNumber: lineNum, Op: LineOpSet, Content: newLine})
					}
				}
			}
		}
	}

	// Old document is longer — delete the trailing lines highest-first so index
	// arithmetic stays correct when the event is replayed.
	if oldLen > newLen {
		for i := oldLen; i > newLen; i-- {
			entries = append(entries, LineEntry{LineNumber: i, Op: LineOpDelete})
		}
	}

	if entries == nil {
		return []LineEntry{}
	}
	return entries
}

// SplitLines splits a string into lines on '\n'. An empty string returns an
// empty slice (not a one-element slice containing "").
func SplitLines(s string) []string {
	if s == "" {
		return []string{}
	}
	return strings.Split(s, "\n")
}

// joinLines joins a slice of lines with '\n'. Returns an empty string for a
// nil or empty slice.
func joinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}
