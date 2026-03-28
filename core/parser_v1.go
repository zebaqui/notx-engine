package core

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	headerKeyValueRe = regexp.MustCompile(`^#\s+([a-z_]+):\s*(.*)$`)
	snapshotHeaderRe = regexp.MustCompile(`^snapshot:(\d+):(.+)$`)
	lineEntryRe      = regexp.MustCompile(`^(\d+) \|(.*)$`)
)

// ParserV1 implements Parser for notx format version 1.0
type ParserV1 struct {
	rd io.Reader
}

// NewParserV1 creates a new V1 parser
func NewParserV1(rd io.Reader) *ParserV1 {
	return &ParserV1{rd: rd}
}

// Parse implements the Parser interface for V1 format
func (p *ParserV1) Parse() (*Note, error) {
	scanner := bufio.NewScanner(p.rd)
	var lines []string

	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("parser: read error: %w", err)
	}

	return p.parseLines(lines)
}

func (p *ParserV1) parseLines(lines []string) (*Note, error) {
	if len(lines) == 0 {
		return nil, fmt.Errorf("parser: empty file")
	}

	headerEnd, metadata, err := p.parseHeader(lines)
	if err != nil {
		return nil, fmt.Errorf("parser: header: %w", err)
	}

	noteURN, err := ParseURN(metadata["note_urn"])
	if err != nil {
		return nil, fmt.Errorf("parser: invalid note_urn: %w", err)
	}

	createdAt, err := parseISO8601(metadata["created_at"])
	if err != nil {
		return nil, fmt.Errorf("parser: invalid created_at: %w", err)
	}

	noteType, err := ParseNoteType(metadata["note_type"])
	if err != nil {
		return nil, fmt.Errorf("parser: invalid note_type: %w", err)
	}

	note := NewNote(noteURN, metadata["name"], createdAt)
	note.NoteType = noteType

	if projURNStr, ok := metadata["project_urn"]; ok && projURNStr != "" {
		projURN, err := ParseURN(projURNStr)
		if err != nil {
			return nil, fmt.Errorf("parser: invalid project_urn: %w", err)
		}
		note.ProjectURN = &projURN
	}

	if folderURNStr, ok := metadata["folder_urn"]; ok && folderURNStr != "" {
		folderURN, err := ParseURN(folderURNStr)
		if err != nil {
			return nil, fmt.Errorf("parser: invalid folder_urn: %w", err)
		}
		note.FolderURN = &folderURN
	}

	if deletedStr, ok := metadata["deleted"]; ok && deletedStr != "" {
		note.Deleted = strings.ToLower(deletedStr) == "true"
	}

	remainingLines := lines[headerEnd:]
	if err := p.parseEventStream(note, remainingLines); err != nil {
		return nil, fmt.Errorf("parser: event stream: %w", err)
	}

	return note, nil
}

func (p *ParserV1) parseHeader(lines []string) (int, map[string]string, error) {
	metadata := make(map[string]string)
	var i int

	for i = 0; i < len(lines); i++ {
		line := lines[i]

		if strings.TrimSpace(line) == "" {
			continue
		}

		if !strings.HasPrefix(line, "#") {
			break
		}

		matches := headerKeyValueRe.FindStringSubmatch(line)
		if len(matches) == 3 {
			key := matches[1]
			value := matches[2]
			metadata[key] = value
		}
	}

	required := []string{"note_urn", "name", "created_at", "head_sequence"}
	for _, field := range required {
		if _, ok := metadata[field]; !ok {
			return 0, nil, fmt.Errorf("missing required field: %s", field)
		}
	}

	return i, metadata, nil
}

// parseEventHeader parses an event header line.
// Format: <sequence>:<timestamp>:<author-urn>
// The timestamp is ISO-8601 UTC ending with 'Z', followed by a colon.
func parseEventHeader(line string) (seq int, ts time.Time, author URN, err error) {
	// Find first colon (after sequence)
	colonIdx := strings.IndexByte(line, ':')
	if colonIdx < 0 {
		err = fmt.Errorf("no colon found for sequence")
		return
	}

	seqStr := line[:colonIdx]
	seq, err = strconv.Atoi(seqStr)
	if err != nil {
		err = fmt.Errorf("invalid sequence: %w", err)
		return
	}

	// The timestamp should end with 'Z', followed by ':'
	// Find the 'Z' and assume the next character is ':'
	rest := line[colonIdx+1:]
	zIdx := strings.IndexByte(rest, 'Z')
	if zIdx < 0 {
		err = fmt.Errorf("timestamp missing Z suffix")
		return
	}

	tsStr := rest[:zIdx+1] // Include the 'Z'
	ts, err = parseISO8601(tsStr)
	if err != nil {
		err = fmt.Errorf("invalid timestamp: %w", err)
		return
	}

	// Skip the colon after 'Z'
	authorStart := colonIdx + 1 + zIdx + 1
	if authorStart >= len(line) || line[authorStart] != ':' {
		err = fmt.Errorf("expected colon after timestamp")
		return
	}

	authorStr := line[authorStart+1:]
	author, err = ParseURN(authorStr)
	if err != nil {
		err = fmt.Errorf("invalid author URN: %w", err)
		return
	}

	return
}

func (p *ParserV1) parseEventStream(note *Note, lines []string) error {
	i := 0

	for i < len(lines) {
		line := strings.TrimSpace(lines[i])

		if line == "" {
			i++
			continue
		}

		// Check for snapshot header.
		if snapshotMatches := snapshotHeaderRe.FindStringSubmatch(line); len(snapshotMatches) == 3 {
			seq, _ := strconv.Atoi(snapshotMatches[1])
			ts, _ := parseISO8601(snapshotMatches[2])
			i++

			if i >= len(lines) || strings.TrimSpace(lines[i]) != "=>" {
				return fmt.Errorf("expected snapshot separator '=>'")
			}
			i++

			snapshotLines, nextI, err := p.parseSnapshotEntries(lines, i)
			if err != nil {
				return err
			}
			i = nextI

			snap := &Snapshot{
				NoteURN:   note.URN,
				Sequence:  seq,
				Lines:     snapshotLines,
				CreatedAt: ts,
			}
			note.AddSnapshot(snap)
			continue
		}

		// Try to parse as event header.
		if seq, ts, author, err := parseEventHeader(line); err == nil {
			i++

			if i >= len(lines) || strings.TrimSpace(lines[i]) != "->" {
				return fmt.Errorf("expected event separator '->'")
			}
			i++

			entries, nextI, err := p.parseEventEntries(lines, i)
			if err != nil {
				return err
			}
			i = nextI

			event := &Event{
				NoteURN:   note.URN,
				Sequence:  seq,
				AuthorURN: author,
				CreatedAt: ts,
				Entries:   entries,
			}
			if err := note.ApplyEvent(event); err != nil {
				return fmt.Errorf("apply event seq %d: %w", seq, err)
			}
			continue
		}

		i++
	}

	return nil
}

func (p *ParserV1) parseEventEntries(lines []string, start int) ([]LineEntry, int, error) {
	var entries []LineEntry
	i := start

	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		if trimmed == "" {
			i++
			break
		}

		if strings.HasPrefix(trimmed, "snapshot:") {
			break
		}

		// Check if it looks like an event header
		if seq, _, _, err := parseEventHeader(trimmed); err == nil && seq > 0 {
			break
		}

		matches := lineEntryRe.FindStringSubmatch(trimmed)
		if len(matches) == 3 {
			lineNum, _ := strconv.Atoi(matches[1])
			content := matches[2]

			var op LineOp
			if content == "-" {
				op = LineOpDelete
			} else if content == "" {
				op = LineOpSetEmpty
			} else {
				op = LineOpSet
			}

			entries = append(entries, LineEntry{
				LineNumber: lineNum,
				Op:         op,
				Content:    content,
			})
		}

		i++
	}

	return entries, i, nil
}

func (p *ParserV1) parseSnapshotEntries(lines []string, start int) ([]string, int, error) {
	var result []string
	i := start

	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		if trimmed == "" {
			i++
			break
		}

		if strings.HasPrefix(trimmed, "snapshot:") {
			break
		}

		matches := lineEntryRe.FindStringSubmatch(trimmed)
		if len(matches) == 3 {
			lineNum, _ := strconv.Atoi(matches[1])
			content := matches[2]

			for len(result) < lineNum {
				result = append(result, "")
			}
			result[lineNum-1] = content
		}

		i++
	}

	return result, i, nil
}

func parseISO8601(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err == nil {
		return t, nil
	}

	t, err = time.Parse("2006-01-02T15:04:05", s)
	if err == nil {
		return t, nil
	}

	return time.Time{}, fmt.Errorf("invalid ISO-8601 timestamp: %q", s)
}
