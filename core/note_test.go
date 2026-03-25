package core

import (
	"testing"
	"time"
)

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────────

var (
	testNoteURN   = MustParseURN("notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a")
	testAuthorURN = MustParseURN("notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b")
	testAuthor2   = MustParseURN("notx:usr:3c4d5e6f-7a8b-9c0d-1e2f-3a4b5c6d7e8f")
	testT0        = time.Date(2025, 1, 15, 9, 0, 0, 0, time.UTC)
)

// newNote returns a blank Note wired up with the shared test URN/name.
func newNote() *Note {
	return NewNote(testNoteURN, "Meeting Notes", testT0)
}

// makeEvent builds an Event for the given sequence / author / entries.
func makeEvent(seq int, author URN, at time.Time, entries ...LineEntry) *Event {
	return &Event{
		NoteURN:   testNoteURN,
		Sequence:  seq,
		AuthorURN: author,
		CreatedAt: at,
		Entries:   entries,
	}
}

// set is a shorthand for a LineOpSet entry.
func set(line int, content string) LineEntry {
	return LineEntry{LineNumber: line, Op: LineOpSet, Content: content}
}

// empty is a shorthand for a LineOpSetEmpty entry.
func empty(line int) LineEntry {
	return LineEntry{LineNumber: line, Op: LineOpSetEmpty}
}

// del is a shorthand for a LineOpDelete entry.
func del(line int) LineEntry {
	return LineEntry{LineNumber: line, Op: LineOpDelete}
}

// mustApply calls ApplyEvent and fails the test on error.
func mustApply(t *testing.T, n *Note, e *Event) {
	t.Helper()
	if err := n.ApplyEvent(e); err != nil {
		t.Fatalf("ApplyEvent(seq=%d): %v", e.Sequence, err)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// NewNote
// ──────────────────────────────────────────────────────────────────────────────

func TestNewNote_InitialState(t *testing.T) {
	n := newNote()

	if !n.URN.Equal(testNoteURN) {
		t.Errorf("URN: got %v, want %v", n.URN, testNoteURN)
	}
	if n.Name != "Meeting Notes" {
		t.Errorf("Name: got %q", n.Name)
	}
	if n.HeadSequence() != 0 {
		t.Errorf("HeadSequence: got %d, want 0", n.HeadSequence())
	}
	if n.EventCount() != 0 {
		t.Errorf("EventCount: got %d, want 0", n.EventCount())
	}
	if n.Content() != "" {
		t.Errorf("Content: got %q, want empty string", n.Content())
	}
	if n.NodeLinks == nil {
		t.Error("NodeLinks should be initialised (non-nil map)")
	}
	if n.Deleted {
		t.Error("Deleted should be false initially")
	}
	if !n.CreatedAt.Equal(testT0) {
		t.Errorf("CreatedAt: got %v, want %v", n.CreatedAt, testT0)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// ApplyEvent — validation
// ──────────────────────────────────────────────────────────────────────────────

func TestApplyEvent_WrongSequenceRejected(t *testing.T) {
	n := newNote()

	// Sequence 2 when head is 0 — must error.
	e := makeEvent(2, testAuthorURN, testT0, set(1, "hello"))
	if err := n.ApplyEvent(e); err == nil {
		t.Error("expected error for out-of-order sequence, got nil")
	}
}

func TestApplyEvent_ReplaySequenceRejected(t *testing.T) {
	n := newNote()
	mustApply(t, n, makeEvent(1, testAuthorURN, testT0, set(1, "line one")))

	// Trying to re-apply sequence 1.
	e := makeEvent(1, testAuthorURN, testT0, set(1, "overwrite"))
	if err := n.ApplyEvent(e); err == nil {
		t.Error("expected error when re-applying sequence 1, got nil")
	}
}

func TestApplyEvent_EmptyEntriesRejected(t *testing.T) {
	n := newNote()
	e := &Event{
		NoteURN:   testNoteURN,
		Sequence:  1,
		AuthorURN: testAuthorURN,
		CreatedAt: testT0,
		Entries:   []LineEntry{}, // no entries
	}
	if err := n.ApplyEvent(e); err == nil {
		t.Error("expected error for event with no entries, got nil")
	}
}

func TestApplyEvent_InvalidLineNumberRejected(t *testing.T) {
	n := newNote()
	e := makeEvent(1, testAuthorURN, testT0, LineEntry{LineNumber: 0, Op: LineOpSet, Content: "bad"})
	if err := n.ApplyEvent(e); err == nil {
		t.Error("expected error for line number 0, got nil")
	}
}

func TestApplyEvent_AdvancesHeadSequence(t *testing.T) {
	n := newNote()
	mustApply(t, n, makeEvent(1, testAuthorURN, testT0, set(1, "hello")))
	if n.HeadSequence() != 1 {
		t.Errorf("HeadSequence: got %d, want 1", n.HeadSequence())
	}
	mustApply(t, n, makeEvent(2, testAuthorURN, testT0, set(2, "world")))
	if n.HeadSequence() != 2 {
		t.Errorf("HeadSequence: got %d, want 2", n.HeadSequence())
	}
}

func TestApplyEvent_UpdatesUpdatedAt(t *testing.T) {
	n := newNote()
	later := testT0.Add(5 * time.Minute)
	mustApply(t, n, makeEvent(1, testAuthorURN, later, set(1, "hello")))
	if !n.UpdatedAt.Equal(later) {
		t.Errorf("UpdatedAt: got %v, want %v", n.UpdatedAt, later)
	}
}

func TestApplyEvent_UpdatedAtDoesNotGoBackward(t *testing.T) {
	n := newNote()
	later := testT0.Add(10 * time.Minute)
	earlier := testT0.Add(3 * time.Minute)

	mustApply(t, n, makeEvent(1, testAuthorURN, later, set(1, "a")))
	mustApply(t, n, makeEvent(2, testAuthorURN, earlier, set(2, "b")))

	if !n.UpdatedAt.Equal(later) {
		t.Errorf("UpdatedAt regressed: got %v, want %v", n.UpdatedAt, later)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Content replay — the canonical example from NOTX_FORMAT.md
// ──────────────────────────────────────────────────────────────────────────────

// buildSpecNote reproduces the four-event example from the format specification.
//
//	Event 1: lines 1-3 created
//	Event 2: line 3 updated
//	Event 3: lines 4-6 added
//	Event 4: line 2 deleted
//
// Final document (5 lines):
//
//	# Meeting Notes
//	Attendees: Alice, Bob, Carol
//	(empty)
//	## Action Items
//	- Alice: send recap
func buildSpecNote(t *testing.T) *Note {
	t.Helper()
	n := newNote()

	mustApply(t, n, makeEvent(1, testAuthorURN, testT0,
		set(1, "# Meeting Notes"),
		empty(2),
		set(3, "Attendees: Alice, Bob"),
	))
	mustApply(t, n, makeEvent(2, testAuthorURN, testT0.Add(15*time.Minute),
		set(3, "Attendees: Alice, Bob, Carol"),
	))
	mustApply(t, n, makeEvent(3, testAuthor2, testT0.Add(30*time.Minute),
		empty(4),
		set(5, "## Action Items"),
		set(6, "- Alice: send recap"),
	))
	mustApply(t, n, makeEvent(4, testAuthorURN, testT0.Add(60*time.Minute),
		del(2),
	))

	return n
}

func TestContent_SpecExample_FinalState(t *testing.T) {
	n := buildSpecNote(t)

	want := "# Meeting Notes\nAttendees: Alice, Bob, Carol\n\n## Action Items\n- Alice: send recap"
	got := n.Content()
	if got != want {
		t.Errorf("Content mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestContentAt_SpecExample_AfterEvent1(t *testing.T) {
	n := buildSpecNote(t)

	want := "# Meeting Notes\n\nAttendees: Alice, Bob"
	got, err := n.ContentAt(1)
	if err != nil {
		t.Fatalf("ContentAt(1): %v", err)
	}
	if got != want {
		t.Errorf("ContentAt(1) mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestContentAt_SpecExample_AfterEvent2(t *testing.T) {
	n := buildSpecNote(t)

	want := "# Meeting Notes\n\nAttendees: Alice, Bob, Carol"
	got, err := n.ContentAt(2)
	if err != nil {
		t.Fatalf("ContentAt(2): %v", err)
	}
	if got != want {
		t.Errorf("ContentAt(2) mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestContentAt_SpecExample_AfterEvent3(t *testing.T) {
	n := buildSpecNote(t)

	want := "# Meeting Notes\n\nAttendees: Alice, Bob, Carol\n\n## Action Items\n- Alice: send recap"
	got, err := n.ContentAt(3)
	if err != nil {
		t.Fatalf("ContentAt(3): %v", err)
	}
	if got != want {
		t.Errorf("ContentAt(3) mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestContentAt_Sequence0_IsEmpty(t *testing.T) {
	n := buildSpecNote(t)
	got, err := n.ContentAt(0)
	if err != nil {
		t.Fatalf("ContentAt(0): %v", err)
	}
	if got != "" {
		t.Errorf("ContentAt(0): got %q, want empty string", got)
	}
}

func TestContentAt_BeyondHead_Errors(t *testing.T) {
	n := buildSpecNote(t)
	_, err := n.ContentAt(n.HeadSequence() + 1)
	if err == nil {
		t.Error("expected error for sequence beyond head, got nil")
	}
}

func TestContentAt_NegativeSequence_Errors(t *testing.T) {
	n := newNote()
	_, err := n.ContentAt(-1)
	if err == nil {
		t.Error("expected error for negative sequence, got nil")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Line-level apply semantics
// ──────────────────────────────────────────────────────────────────────────────

func TestApplyEvent_SetReplaceExistingLine(t *testing.T) {
	n := newNote()
	mustApply(t, n, makeEvent(1, testAuthorURN, testT0,
		set(1, "original"),
	))
	mustApply(t, n, makeEvent(2, testAuthorURN, testT0,
		set(1, "updated"),
	))
	if got := n.Content(); got != "updated" {
		t.Errorf("got %q, want %q", got, "updated")
	}
}

func TestApplyEvent_SetEmptyLine(t *testing.T) {
	n := newNote()
	mustApply(t, n, makeEvent(1, testAuthorURN, testT0,
		set(1, "text"),
		empty(2),
		set(3, "more"),
	))
	lines, _ := n.LinesAt(1)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	if lines[1] != "" {
		t.Errorf("line 2 should be empty string, got %q", lines[1])
	}
}

func TestApplyEvent_AppendBeyondEnd(t *testing.T) {
	n := newNote()
	// Start with 2 lines, then set line 5 (skipping 3 and 4).
	mustApply(t, n, makeEvent(1, testAuthorURN, testT0,
		set(1, "a"),
		set(2, "b"),
	))
	mustApply(t, n, makeEvent(2, testAuthorURN, testT0,
		set(5, "e"),
	))
	lines, _ := n.LinesAt(2)
	if len(lines) != 5 {
		t.Fatalf("expected 5 lines after gap-append, got %d: %v", len(lines), lines)
	}
	if lines[2] != "" {
		t.Errorf("line 3 (gap): got %q, want empty", lines[2])
	}
	if lines[3] != "" {
		t.Errorf("line 4 (gap): got %q, want empty", lines[3])
	}
	if lines[4] != "e" {
		t.Errorf("line 5: got %q, want %q", lines[4], "e")
	}
}

func TestApplyEvent_DeleteShiftsLines(t *testing.T) {
	n := newNote()
	mustApply(t, n, makeEvent(1, testAuthorURN, testT0,
		set(1, "a"),
		set(2, "b"),
		set(3, "c"),
	))
	mustApply(t, n, makeEvent(2, testAuthorURN, testT0,
		del(2), // remove "b", "c" shifts to position 2
	))
	lines, _ := n.LinesAt(2)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines after delete, got %d", len(lines))
	}
	if lines[0] != "a" {
		t.Errorf("line 1: got %q, want %q", lines[0], "a")
	}
	if lines[1] != "c" {
		t.Errorf("line 2 (shifted): got %q, want %q", lines[1], "c")
	}
}

func TestApplyEvent_DeleteNonExistentLine_Noop(t *testing.T) {
	n := newNote()
	mustApply(t, n, makeEvent(1, testAuthorURN, testT0,
		set(1, "only line"),
	))
	// Delete line 99 — idempotent no-op.
	mustApply(t, n, makeEvent(2, testAuthorURN, testT0,
		del(99),
	))
	if got := n.Content(); got != "only line" {
		t.Errorf("content changed after no-op delete: %q", got)
	}
}

func TestApplyEvent_MultipleDeletesInOneEvent(t *testing.T) {
	n := newNote()
	mustApply(t, n, makeEvent(1, testAuthorURN, testT0,
		set(1, "a"),
		set(2, "b"),
		set(3, "c"),
		set(4, "d"),
		set(5, "e"),
	))
	// Delete lines 2 and 4 in a single event (batch semantics — line numbers
	// are relative to the state BEFORE the event).
	mustApply(t, n, makeEvent(2, testAuthorURN, testT0,
		del(2),
		del(4),
	))
	lines, _ := n.LinesAt(2)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %v", len(lines), lines)
	}
	if lines[0] != "a" || lines[1] != "c" || lines[2] != "e" {
		t.Errorf("unexpected lines after multi-delete: %v", lines)
	}
}

func TestApplyEvent_DeleteFirstLine(t *testing.T) {
	n := newNote()
	mustApply(t, n, makeEvent(1, testAuthorURN, testT0,
		set(1, "first"),
		set(2, "second"),
	))
	mustApply(t, n, makeEvent(2, testAuthorURN, testT0, del(1)))
	lines, _ := n.LinesAt(2)
	if len(lines) != 1 || lines[0] != "second" {
		t.Errorf("unexpected lines after deleting first: %v", lines)
	}
}

func TestApplyEvent_DeleteLastLine(t *testing.T) {
	n := newNote()
	mustApply(t, n, makeEvent(1, testAuthorURN, testT0,
		set(1, "first"),
		set(2, "second"),
	))
	mustApply(t, n, makeEvent(2, testAuthorURN, testT0, del(2)))
	lines, _ := n.LinesAt(2)
	if len(lines) != 1 || lines[0] != "first" {
		t.Errorf("unexpected lines after deleting last: %v", lines)
	}
}

func TestApplyEvent_DeleteOnlyLine_EmptyDocument(t *testing.T) {
	n := newNote()
	mustApply(t, n, makeEvent(1, testAuthorURN, testT0, set(1, "solo")))
	mustApply(t, n, makeEvent(2, testAuthorURN, testT0, del(1)))
	if got := n.Content(); got != "" {
		t.Errorf("expected empty document after deleting only line, got %q", got)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// LinesAt
// ──────────────────────────────────────────────────────────────────────────────

func TestLinesAt_ReturnsCopy(t *testing.T) {
	n := newNote()
	mustApply(t, n, makeEvent(1, testAuthorURN, testT0,
		set(1, "original"),
	))
	lines, _ := n.LinesAt(1)
	lines[0] = "mutated"

	// Internal state must be unaffected.
	if got := n.Content(); got != "original" {
		t.Errorf("LinesAt returned a reference to internal state: content is now %q", got)
	}
}

func TestLinesAt_BeyondHead_Errors(t *testing.T) {
	n := newNote()
	_, err := n.LinesAt(1)
	if err == nil {
		t.Error("expected error for sequence 1 when head is 0")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// EventAt
// ──────────────────────────────────────────────────────────────────────────────

func TestEventAt_ReturnsCorrectEvent(t *testing.T) {
	n := buildSpecNote(t)
	e, err := n.EventAt(2)
	if err != nil {
		t.Fatalf("EventAt(2): %v", err)
	}
	if e.Sequence != 2 {
		t.Errorf("Sequence: got %d, want 2", e.Sequence)
	}
	if len(e.Entries) != 1 || e.Entries[0].Content != "Attendees: Alice, Bob, Carol" {
		t.Errorf("unexpected entries: %+v", e.Entries)
	}
}

func TestEventAt_OutOfRange(t *testing.T) {
	n := newNote()
	if _, err := n.EventAt(0); err == nil {
		t.Error("expected error for sequence 0")
	}
	if _, err := n.EventAt(1); err == nil {
		t.Error("expected error for sequence 1 when empty")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Events()
// ──────────────────────────────────────────────────────────────────────────────

func TestEvents_EmptyNote(t *testing.T) {
	n := newNote()
	if n.Events() != nil {
		t.Error("Events() on empty note should return nil")
	}
}

func TestEvents_ReturnsCopy(t *testing.T) {
	n := buildSpecNote(t)
	evts := n.Events()
	if len(evts) != 4 {
		t.Fatalf("expected 4 events, got %d", len(evts))
	}
	// Mutating the returned slice must not affect the note.
	evts[0] = nil
	if n.events[0] == nil {
		t.Error("Events() returned a reference to the internal slice")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Snapshot management
// ──────────────────────────────────────────────────────────────────────────────

func TestBuildSnapshot_MaterialisesCorrectly(t *testing.T) {
	n := buildSpecNote(t)
	snap, err := n.BuildSnapshot(2)
	if err != nil {
		t.Fatalf("BuildSnapshot(2): %v", err)
	}
	if snap.Sequence != 2 {
		t.Errorf("Sequence: got %d, want 2", snap.Sequence)
	}
	wantContent := "# Meeting Notes\n\nAttendees: Alice, Bob, Carol"
	if got := snap.Content(); got != wantContent {
		t.Errorf("snapshot Content:\ngot:  %q\nwant: %q", got, wantContent)
	}
}

func TestBuildSnapshot_StoredAndRetrievable(t *testing.T) {
	n := buildSpecNote(t)
	_, err := n.BuildSnapshot(2)
	if err != nil {
		t.Fatalf("BuildSnapshot: %v", err)
	}
	snaps := n.Snapshots()
	if len(snaps) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(snaps))
	}
	if snaps[0].Sequence != 2 {
		t.Errorf("snapshot Sequence: got %d, want 2", snaps[0].Sequence)
	}
}

func TestBuildSnapshot_OutOfRange_Errors(t *testing.T) {
	n := newNote()
	if _, err := n.BuildSnapshot(1); err == nil {
		t.Error("expected error for snapshot beyond head sequence")
	}
}

func TestBuildSnapshot_Sequence0_Errors(t *testing.T) {
	n := buildSpecNote(t)
	if _, err := n.BuildSnapshot(0); err == nil {
		t.Error("expected error for snapshot at sequence 0")
	}
}

func TestAddSnapshot_ReplacesExistingAtSameSequence(t *testing.T) {
	n := buildSpecNote(t)

	snap1 := &Snapshot{
		NoteURN:  testNoteURN,
		Sequence: 2,
		Lines:    []string{"old content"},
	}
	if err := n.AddSnapshot(snap1); err != nil {
		t.Fatalf("AddSnapshot: %v", err)
	}

	snap2 := &Snapshot{
		NoteURN:  testNoteURN,
		Sequence: 2,
		Lines:    []string{"new content"},
	}
	if err := n.AddSnapshot(snap2); err != nil {
		t.Fatalf("AddSnapshot (replace): %v", err)
	}

	snaps := n.Snapshots()
	if len(snaps) != 1 {
		t.Fatalf("expected exactly 1 snapshot after replace, got %d", len(snaps))
	}
	if snaps[0].Lines[0] != "new content" {
		t.Errorf("snapshot was not replaced: got %q", snaps[0].Lines[0])
	}
}

func TestAddSnapshot_MaintainsSortedOrder(t *testing.T) {
	n := buildSpecNote(t)

	seqs := []int{4, 2, 3, 1}
	for _, seq := range seqs {
		snap := &Snapshot{NoteURN: testNoteURN, Sequence: seq, Lines: []string{}}
		if err := n.AddSnapshot(snap); err != nil {
			t.Fatalf("AddSnapshot(seq=%d): %v", seq, err)
		}
	}

	snaps := n.Snapshots()
	for i := 1; i < len(snaps); i++ {
		if snaps[i].Sequence <= snaps[i-1].Sequence {
			t.Errorf("snapshots not sorted: snaps[%d].Sequence=%d <= snaps[%d].Sequence=%d",
				i, snaps[i].Sequence, i-1, snaps[i-1].Sequence)
		}
	}
}

func TestAddSnapshot_InvalidSequence_Errors(t *testing.T) {
	n := buildSpecNote(t)

	// Beyond head.
	snap := &Snapshot{NoteURN: testNoteURN, Sequence: 99, Lines: []string{}}
	if err := n.AddSnapshot(snap); err == nil {
		t.Error("expected error for snapshot beyond head sequence")
	}

	// Sequence 0.
	snap0 := &Snapshot{NoteURN: testNoteURN, Sequence: 0, Lines: []string{}}
	if err := n.AddSnapshot(snap0); err == nil {
		t.Error("expected error for snapshot at sequence 0")
	}
}

func TestSnapshots_ReturnsCopy(t *testing.T) {
	n := buildSpecNote(t)
	_, _ = n.BuildSnapshot(2)

	snaps := n.Snapshots()
	snaps[0] = nil // mutate returned slice

	if n.snapshots[0] == nil {
		t.Error("Snapshots() returned a reference to internal slice")
	}
}

func TestSnapshots_EmptyNote(t *testing.T) {
	n := newNote()
	if n.Snapshots() != nil {
		t.Error("Snapshots() on fresh note should return nil")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Snapshot-accelerated replay
// ──────────────────────────────────────────────────────────────────────────────

// TestContentAt_UsesSnapshot verifies that ContentAt still returns the correct
// content when a snapshot has been added as a starting point for replay.
func TestContentAt_UsesSnapshot(t *testing.T) {
	n := buildSpecNote(t)

	// Build a snapshot at sequence 2 to serve as a replay start point.
	_, err := n.BuildSnapshot(2)
	if err != nil {
		t.Fatalf("BuildSnapshot: %v", err)
	}

	// Content at 3 must equal the expected value regardless of snapshot usage.
	want := "# Meeting Notes\n\nAttendees: Alice, Bob, Carol\n\n## Action Items\n- Alice: send recap"
	got, err := n.ContentAt(3)
	if err != nil {
		t.Fatalf("ContentAt(3): %v", err)
	}
	if got != want {
		t.Errorf("ContentAt(3) with snapshot:\ngot:  %q\nwant: %q", got, want)
	}

	// And the full head must still be correct.
	wantHead := "# Meeting Notes\nAttendees: Alice, Bob, Carol\n\n## Action Items\n- Alice: send recap"
	if got := n.Content(); got != wantHead {
		t.Errorf("Content() with snapshot:\ngot:  %q\nwant: %q", got, wantHead)
	}
}

// TestContentAt_SnapshotDoesNotMutate ensures that snapshot lines are not
// modified by subsequent replay operations.
func TestContentAt_SnapshotDoesNotMutate(t *testing.T) {
	n := buildSpecNote(t)

	snap, err := n.BuildSnapshot(2)
	if err != nil {
		t.Fatalf("BuildSnapshot: %v", err)
	}
	snapLinesBefore := make([]string, len(snap.Lines))
	copy(snapLinesBefore, snap.Lines)

	// Trigger a replay that starts from the snapshot.
	_, _ = n.ContentAt(4)

	for i, line := range snap.Lines {
		if line != snapLinesBefore[i] {
			t.Errorf("snapshot line %d mutated: got %q, want %q", i+1, line, snapLinesBefore[i])
		}
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// History & DiffAt
// ──────────────────────────────────────────────────────────────────────────────

func TestHistory_Length(t *testing.T) {
	n := buildSpecNote(t)
	h := n.History()
	if len(h) != 4 {
		t.Errorf("expected 4 history entries, got %d", len(h))
	}
}

func TestHistory_Fields(t *testing.T) {
	n := buildSpecNote(t)
	h := n.History()

	if h[0].Sequence != 1 {
		t.Errorf("entry[0].Sequence: got %d, want 1", h[0].Sequence)
	}
	if !h[0].AuthorURN.Equal(testAuthorURN) {
		t.Errorf("entry[0].AuthorURN: got %v, want %v", h[0].AuthorURN, testAuthorURN)
	}
	if !h[2].AuthorURN.Equal(testAuthor2) {
		t.Errorf("entry[2].AuthorURN: got %v, want %v", h[2].AuthorURN, testAuthor2)
	}
}

func TestHistory_EntriesCopied(t *testing.T) {
	n := buildSpecNote(t)
	h := n.History()
	h[0].Entries[0].Content = "mutated"

	// Original event must be untouched.
	orig, _ := n.EventAt(1)
	if orig.Entries[0].Content == "mutated" {
		t.Error("History() did not deep-copy entries; mutation leaked into event stream")
	}
}

func TestDiffAt_ReturnsCorrectEntries(t *testing.T) {
	n := buildSpecNote(t)
	entries, err := n.DiffAt(4)
	if err != nil {
		t.Fatalf("DiffAt(4): %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Op != LineOpDelete || entries[0].LineNumber != 2 {
		t.Errorf("unexpected entry: %+v", entries[0])
	}
}

func TestDiffAt_OutOfRange_Errors(t *testing.T) {
	n := newNote()
	if _, err := n.DiffAt(1); err == nil {
		t.Error("expected error for DiffAt(1) on empty note")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// AuthorURNs
// ──────────────────────────────────────────────────────────────────────────────

func TestAuthorURNs_DeduplicatedAndOrdered(t *testing.T) {
	n := buildSpecNote(t)
	authors := n.AuthorURNs()

	// testAuthorURN appears in events 1, 2, 4 — testAuthor2 in event 3.
	if len(authors) != 2 {
		t.Fatalf("expected 2 unique authors, got %d: %v", len(authors), authors)
	}
	if !authors[0].Equal(testAuthorURN) {
		t.Errorf("authors[0]: got %v, want %v", authors[0], testAuthorURN)
	}
	if !authors[1].Equal(testAuthor2) {
		t.Errorf("authors[1]: got %v, want %v", authors[1], testAuthor2)
	}
}

func TestAuthorURNs_EmptyNote(t *testing.T) {
	n := newNote()
	if got := n.AuthorURNs(); len(got) != 0 {
		t.Errorf("expected empty author list, got %v", got)
	}
}

func TestAuthorURNs_AnonIncluded(t *testing.T) {
	n := newNote()
	anon := AnonURN("notx")
	mustApply(t, n, makeEvent(1, anon, testT0, set(1, "anonymous edit")))

	authors := n.AuthorURNs()
	if len(authors) != 1 {
		t.Fatalf("expected 1 author, got %d", len(authors))
	}
	if !authors[0].IsAnon() {
		t.Errorf("expected anon author, got %v", authors[0])
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Event.Payload()
// ──────────────────────────────────────────────────────────────────────────────

func TestEventPayload_SetLines(t *testing.T) {
	e := &Event{
		Sequence: 1,
		Entries: []LineEntry{
			set(1, "# Meeting Notes"),
			empty(2),
			set(3, "Attendees: Alice, Bob"),
		},
	}
	want := "1 | # Meeting Notes\n2 |\n3 | Attendees: Alice, Bob"
	if got := e.Payload(); got != want {
		t.Errorf("Payload():\ngot:  %q\nwant: %q", got, want)
	}
}

func TestEventPayload_DeleteLine(t *testing.T) {
	e := &Event{
		Sequence: 1,
		Entries:  []LineEntry{del(2)},
	}
	want := "2 |-"
	if got := e.Payload(); got != want {
		t.Errorf("Payload(): got %q, want %q", got, want)
	}
}

func TestEventPayload_EmptyEntries(t *testing.T) {
	e := &Event{Sequence: 1, Entries: nil}
	if got := e.Payload(); got != "" {
		t.Errorf("Payload() for empty event: got %q, want empty string", got)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Snapshot.Content() and Snapshot.LineCount()
// ──────────────────────────────────────────────────────────────────────────────

func TestSnapshotContent(t *testing.T) {
	snap := &Snapshot{
		Lines: []string{"# Title", "", "Body text"},
	}
	want := "# Title\n\nBody text"
	if got := snap.Content(); got != want {
		t.Errorf("Snapshot.Content(): got %q, want %q", got, want)
	}
}

func TestSnapshotLineCount(t *testing.T) {
	snap := &Snapshot{
		Lines: []string{"a", "b", "c"},
	}
	if got := snap.LineCount(); got != 3 {
		t.Errorf("Snapshot.LineCount(): got %d, want 3", got)
	}
}

func TestSnapshotContent_Empty(t *testing.T) {
	snap := &Snapshot{Lines: nil}
	if got := snap.Content(); got != "" {
		t.Errorf("Snapshot.Content() for nil lines: got %q, want empty", got)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Note metadata fields
// ──────────────────────────────────────────────────────────────────────────────

func TestNote_OptionalURNFields(t *testing.T) {
	n := newNote()

	projURN := MustParseURN("notx:proj:3a1b2c3d-4e5f-6a7b-8c9d-0e1f2a3b4c5d")
	folderURN := MustParseURN("notx:folder:1c2d3e4f-5a6b-7c8d-9e0f-1a2b3c4d5e6f")
	parentURN := MustParseURN("notx:note:9e8d7c6b-5a4f-3e2d-1c0b-9a8f7e6d5c4b")

	n.ProjectURN = &projURN
	n.FolderURN = &folderURN
	n.ParentURN = &parentURN

	if n.ProjectURN == nil || !n.ProjectURN.Equal(projURN) {
		t.Errorf("ProjectURN: got %v, want %v", n.ProjectURN, projURN)
	}
	if n.FolderURN == nil || !n.FolderURN.Equal(folderURN) {
		t.Errorf("FolderURN: got %v, want %v", n.FolderURN, folderURN)
	}
	if n.ParentURN == nil || !n.ParentURN.Equal(parentURN) {
		t.Errorf("ParentURN: got %v, want %v", n.ParentURN, parentURN)
	}
}

func TestNote_NodeLinks(t *testing.T) {
	n := newNote()
	reqURN := MustParseURN("mycompany:note:7c3e9f1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b")
	n.NodeLinks["requirements"] = reqURN

	got, ok := n.NodeLinks["requirements"]
	if !ok {
		t.Fatal("expected 'requirements' key in NodeLinks")
	}
	if !got.Equal(reqURN) {
		t.Errorf("NodeLinks[requirements]: got %v, want %v", got, reqURN)
	}
}

func TestNote_SoftDelete(t *testing.T) {
	n := newNote()
	if n.Deleted {
		t.Error("note should not be deleted initially")
	}
	n.Deleted = true
	if !n.Deleted {
		t.Error("Deleted flag not set")
	}
}
