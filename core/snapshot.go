package core

import "time"

// Snapshot represents a materialized content checkpoint within a notx
// document's event stream.
//
// A snapshot captures the complete, fully-replayed state of the document at a
// specific sequence number. It is purely an optimization — removing all
// snapshots from a .notx file does not change the behavior of a correct parser.
// The event stream is always the source of truth.
//
// In the .notx file format a snapshot block looks like:
//
//	snapshot:10:2025-01-15T11:00:00Z
//	=>
//	1 | # Meeting Notes
//	2 | Attendees: Alice, Bob
//	3 |
//	4 | ## Action Items
type Snapshot struct {
	// NoteURN is the URN of the note this snapshot belongs to.
	NoteURN URN

	// Sequence is the sequence number of the last event that was included when
	// this snapshot was materialized. A snapshot at sequence N contains the
	// result of replaying events 1 through N inclusive.
	Sequence int

	// Lines holds the fully materialized document content at Sequence.
	// Each element corresponds to one document line (1-based: Lines[0] is
	// line 1). Lines never contains a trailing empty sentinel — the slice
	// length equals the number of lines in the document.
	Lines []string

	// CreatedAt is the UTC timestamp of when the snapshot was written.
	CreatedAt time.Time
}

// Content returns the snapshot's materialized document as a single string with
// lines joined by newline characters, matching the canonical in-memory
// representation used by Note.Content().
func (s *Snapshot) Content() string {
	return joinLines(s.Lines)
}

// LineCount returns the number of lines in the snapshot.
func (s *Snapshot) LineCount() int {
	return len(s.Lines)
}
