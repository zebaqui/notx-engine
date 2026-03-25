package core

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

var (
	versionRe = regexp.MustCompile(`^#\s+notx/(\d+\.\d+)`)
)

// Parser is the interface for parsing notx files.
// Different versions of the format should implement this interface.
type Parser interface {
	Parse() (*Note, error)
}

// NewNoteFromFile reads a .notx file and returns a parsed Note.
// Automatically detects the format version and returns an error if the version is not supported.
func NewNoteFromFile(filePath string) (*Note, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("cannot open file: %w", err)
	}
	defer file.Close()

	return NewNoteFromReader(file)
}

// NewNoteFromString parses a notx document from a string.
// Automatically detects the format version and returns an error if the version is not supported.
func NewNoteFromString(content string) (*Note, error) {
	return NewNoteFromReader(strings.NewReader(content))
}

// NewNoteFromReader parses a notx document from a reader.
// Automatically detects the format version and returns an error if the version is not supported.
func NewNoteFromReader(rd io.Reader) (*Note, error) {
	// Read all content to detect version
	content, err := io.ReadAll(rd)
	if err != nil {
		return nil, fmt.Errorf("cannot read input: %w", err)
	}

	// Detect version
	lines := strings.Split(string(content), "\n")
	version, err := detectVersion(lines)
	if err != nil {
		return nil, err
	}

	// Parse with appropriate parser
	var parser Parser
	switch version {
	case "1.0":
		parser = NewParserV1(strings.NewReader(string(content)))
	default:
		return nil, fmt.Errorf("notx version %s is not supported", version)
	}

	return parser.Parse()
}

// detectVersion extracts the format version from the file header.
// Returns an error if the version header is not found.
func detectVersion(lines []string) (string, error) {
	for _, line := range lines {
		if matches := versionRe.FindStringSubmatch(line); len(matches) == 2 {
			return matches[1], nil
		}
		// Stop looking after first non-comment line
		if !strings.HasPrefix(strings.TrimSpace(line), "#") && strings.TrimSpace(line) != "" {
			break
		}
	}
	return "", fmt.Errorf("cannot detect notx version: missing version header")
}

// Deprecated: Use NewNoteFromFile or NewNoteFromString instead.
// NewParser creates a parser for the given reader (kept for backward compatibility).
func NewParser(rd io.Reader) *ParserV1 {
	return NewParserV1(rd)
}
