package core

import (
	"strings"
	"testing"
)

func TestParser_SimpleFile(t *testing.T) {
	content := `# notx/1.0
# note_urn:      urn:notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a
# name:          Test Note
# created_at:    2025-01-15T09:00:00Z
# head_sequence: 1

1:2025-01-15T09:00:00Z:urn:notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b
->
1 | Line one
2 | Line two
`

	parser := NewParserV1(strings.NewReader(content))
	note, err := parser.Parse()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	if note.Name != "Test Note" {
		t.Errorf("Name: got %q, want %q", note.Name, "Test Note")
	}

	if note.HeadSequence() != 1 {
		t.Errorf("HeadSequence: got %d, want 1", note.HeadSequence())
	}

	expectedContent := "Line one\nLine two"
	if got := note.Content(); got != expectedContent {
		t.Errorf("Content:\ngot:  %q\nwant: %q", got, expectedContent)
	}
}

func TestParser_MultipleEvents(t *testing.T) {
	content := `# notx/1.0
# note_urn:      urn:notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a
# name:          Multi Event
# created_at:    2025-01-15T09:00:00Z
# head_sequence: 2

1:2025-01-15T09:00:00Z:urn:notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b
->
1 | First

2:2025-01-15T09:05:00Z:urn:notx:usr:3c4d5e6f-7a8b-9c0d-1e2f-3a4b5c6d7e8f
->
2 | Second
`

	parser := NewParserV1(strings.NewReader(content))
	note, err := parser.Parse()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	if note.EventCount() != 2 {
		t.Errorf("EventCount: got %d, want 2", note.EventCount())
	}

	expectedContent := "First\nSecond"
	if got := note.Content(); got != expectedContent {
		t.Errorf("Content:\ngot:  %q\nwant: %q", got, expectedContent)
	}

	authors := note.AuthorURNs()
	if len(authors) != 2 {
		t.Errorf("Authors: got %d, want 2", len(authors))
	}
}

func TestParser_DeleteOperation(t *testing.T) {
	content := `# notx/1.0
# note_urn:      urn:notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a
# name:          Delete Test
# created_at:    2025-01-15T09:00:00Z
# head_sequence: 2

1:2025-01-15T09:00:00Z:urn:notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b
->
1 | Line A
2 | Line B
3 | Line C

2:2025-01-15T09:05:00Z:urn:notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b
->
2 |-
`

	parser := NewParserV1(strings.NewReader(content))
	note, err := parser.Parse()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	expectedContent := "Line A\nLine C"
	if got := note.Content(); got != expectedContent {
		t.Errorf("Content after delete:\ngot:  %q\nwant: %q", got, expectedContent)
	}
}

func TestParser_EmptyLineOperation(t *testing.T) {
	content := `# notx/1.0
# note_urn:      urn:notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a
# name:          Empty Test
# created_at:    2025-01-15T09:00:00Z
# head_sequence: 1

1:2025-01-15T09:00:00Z:urn:notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b
->
1 | Text
2 |
3 | More text
`

	parser := NewParserV1(strings.NewReader(content))
	note, err := parser.Parse()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	expectedContent := "Text\n\nMore text"
	if got := note.Content(); got != expectedContent {
		t.Errorf("Content with empty line:\ngot:  %q\nwant: %q", got, expectedContent)
	}
}

func TestParser_WithSnapshot(t *testing.T) {
	content := `# notx/1.0
# note_urn:      urn:notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a
# name:          Snapshot Test
# created_at:    2025-01-15T09:00:00Z
# head_sequence: 2

1:2025-01-15T09:00:00Z:urn:notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b
->
1 | First

snapshot:1:2025-01-15T09:00:00Z
=>
1 | First

2:2025-01-15T09:05:00Z:urn:notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b
->
2 | Second
`

	parser := NewParserV1(strings.NewReader(content))
	note, err := parser.Parse()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	snapshots := note.Snapshots()
	if len(snapshots) != 1 {
		t.Errorf("Snapshots: got %d, want 1", len(snapshots))
	}

	if snapshots[0].Sequence != 1 {
		t.Errorf("Snapshot sequence: got %d, want 1", snapshots[0].Sequence)
	}

	if snapshots[0].LineCount() != 1 {
		t.Errorf("Snapshot lines: got %d, want 1", snapshots[0].LineCount())
	}
}

func TestParser_AuthorURN(t *testing.T) {
	content := `# notx/1.0
# note_urn:      urn:notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a
# name:          Author Test
# created_at:    2025-01-15T09:00:00Z
# head_sequence: 1

1:2025-01-15T09:00:00Z:urn:notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b
->
1 | Content
`

	parser := NewParserV1(strings.NewReader(content))
	note, err := parser.Parse()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	event, _ := note.EventAt(1)
	wantAuthor := "urn:notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b"
	if event.AuthorURN.String() != wantAuthor {
		t.Errorf("AuthorURN: got %q, want %q", event.AuthorURN.String(), wantAuthor)
	}
}

func TestParser_GapAndAppend(t *testing.T) {
	content := `# notx/1.0
# note_urn:      urn:notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a
# name:          Gap Test
# created_at:    2025-01-15T09:00:00Z
# head_sequence: 1

1:2025-01-15T09:00:00Z:urn:notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b
->
1 | First
5 | Fifth
`

	parser := NewParserV1(strings.NewReader(content))
	note, err := parser.Parse()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	lines, _ := note.LinesAt(1)
	if len(lines) != 5 {
		t.Errorf("Lines after gap: got %d, want 5", len(lines))
	}

	if lines[0] != "First" {
		t.Errorf("line[0]: got %q, want %q", lines[0], "First")
	}

	if lines[4] != "Fifth" {
		t.Errorf("line[4]: got %q, want %q", lines[4], "Fifth")
	}
}

func TestParser_ContentAt(t *testing.T) {
	content := `# notx/1.0
# note_urn:      urn:notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a
# name:          History Test
# created_at:    2025-01-15T09:00:00Z
# head_sequence: 3

1:2025-01-15T09:00:00Z:urn:notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b
->
1 | A

2:2025-01-15T09:05:00Z:urn:notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b
->
2 | B

3:2025-01-15T09:10:00Z:urn:notx:usr:7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b
->
3 | C
`

	parser := NewParserV1(strings.NewReader(content))
	note, err := parser.Parse()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	// At sequence 1
	content1, _ := note.ContentAt(1)
	if content1 != "A" {
		t.Errorf("ContentAt(1): got %q, want %q", content1, "A")
	}

	// At sequence 2
	content2, _ := note.ContentAt(2)
	if content2 != "A\nB" {
		t.Errorf("ContentAt(2): got %q, want %q", content2, "A\nB")
	}

	// At sequence 3
	content3, _ := note.ContentAt(3)
	if content3 != "A\nB\nC" {
		t.Errorf("ContentAt(3): got %q, want %q", content3, "A\nB\nC")
	}
}
