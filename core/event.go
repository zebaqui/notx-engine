package core

import (
	"fmt"
	"strings"
	"time"
)

// LineOp describes what kind of change a LineEntry applies to a document line.
type LineOp int

const (
	// LineOpSet replaces (or appends) the line at the given number with Content.
	LineOpSet LineOp = iota
	// LineOpSetEmpty sets the line at the given number to an empty string.
	LineOpSetEmpty
	// LineOpDelete removes the line at the given number, shifting subsequent
	// lines up by one.
	LineOpDelete
)

// LineEntry represents a single line-level change within an event.
//
// Format in the .notx file:
//
//	N | content    → LineOpSet,      Content = "content"
//	N |            → LineOpSetEmpty, Content = ""
//	N |-           → LineOpDelete,   Content = ""
type LineEntry struct {
	// LineNumber is the 1-based target line number, interpreted relative to the
	// document state before the event began.
	LineNumber int

	// Op is the operation to apply to the line.
	Op LineOp

	// Content is the new value for the line. Only meaningful when Op is
	// LineOpSet. Empty for LineOpSetEmpty and LineOpDelete.
	Content string
}

// Event represents a single history event in a notx document.
//
// An event captures a set of line-level changes made at a specific point in
// time by a specific author. Events are immutable once written.
type Event struct {
	// URN is the globally unique identifier for this event.
	// Format: <namespace>:event:<uuid>
	// May be zero-value if the event was parsed from a file that does not
	// carry per-event URNs (the file format encodes events inline without
	// individual URNs).
	URN URN

	// NoteURN is the URN of the note this event belongs to.
	NoteURN URN

	// Sequence is the monotonically increasing position of this event within
	// its note. Starts at 1; no gaps are allowed.
	Sequence int

	// AuthorURN is the URN of the user who authored the change, or the
	// instance-specific anonymous sentinel (e.g. "notx:usr:anon").
	AuthorURN URN

	// CreatedAt is the UTC timestamp of when this event was recorded.
	CreatedAt time.Time

	// Label is an optional human-readable description of the change
	// (e.g. "Edit at 2:34 PM"). Not stored in the .notx file format; used
	// only when events are managed in a database layer.
	Label string

	// Entries contains the ordered list of line-level changes for this event.
	Entries []LineEntry
}

// Payload returns the lane-format string representation of the event's line
// entries, as it would appear between the `->` separator and the next blank
// line in a .notx file.
//
// Example output:
//
//	1 | # Meeting Notes
//	2 |
//	3 |-
func (e *Event) Payload() string {
	if len(e.Entries) == 0 {
		return ""
	}

	var sb strings.Builder
	for i, entry := range e.Entries {
		if i > 0 {
			sb.WriteByte('\n')
		}
		switch entry.Op {
		case LineOpDelete:
			fmt.Fprintf(&sb, "%d |-", entry.LineNumber)
		case LineOpSetEmpty:
			fmt.Fprintf(&sb, "%d |", entry.LineNumber)
		default: // LineOpSet
			fmt.Fprintf(&sb, "%d | %s", entry.LineNumber, entry.Content)
		}
	}
	return sb.String()
}
