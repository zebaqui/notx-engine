package core

import (
	"strings"
	"time"
	"unicode"
)

// FrontMatter holds the optional YAML front matter that can appear at the very
// beginning of a markdown note, delimited by "---" lines, e.g.:
//
//	---
//	id: post-001
//	title: Some Title
//	slug: some-title
//	description: Short summary
//	date: 2026-04-25
//	updated: 2026-04-25
//	author: Julio
//	tags:
//	  - engineering
//	status: published
//	draft: false
//	---
//
// Unknown keys are collected in Extra so callers can access custom fields
// without losing information.
type FrontMatter struct {
	// ID is an arbitrary string identifier for the document (e.g. "post-001").
	ID string

	// Title is the human-readable title of the document.
	Title string

	// Slug is the URL-friendly identifier (e.g. "some-title").
	Slug string

	// Description is a short one-line summary of the document.
	Description string

	// Date is the publication / creation date of the document.
	// Zero value means the field was absent.
	Date time.Time

	// Updated is the last-modified date of the document.
	// Zero value means the field was absent.
	Updated time.Time

	// Author is the name (or identifier) of the document author.
	Author string

	// Tags is the list of taxonomy tags associated with the document.
	Tags []string

	// Status holds a publication status string (e.g. "published", "draft").
	Status string

	// Draft indicates whether the document is still in draft state.
	Draft bool

	// Extra holds any additional key/value pairs found in the front matter
	// that are not covered by the fields above. Values are raw strings exactly
	// as they appear in the source (list items are joined with ", ").
	Extra map[string]string
}

// HasFrontMatter reports whether content begins with a YAML front matter block,
// i.e. whether its first non-empty line is exactly "---".
func HasFrontMatter(content string) bool {
	for _, line := range strings.SplitN(content, "\n", 10) {
		trimmed := strings.TrimRightFunc(line, unicode.IsSpace)
		if trimmed == "" {
			continue
		}
		return trimmed == "---"
	}
	return false
}

// ParseFrontMatter parses the optional YAML front matter block at the start of
// content.  It returns the populated FrontMatter, the remaining markdown body
// (everything after the closing "---"), and any parse error.
//
// If no front matter is present the returned FrontMatter is nil, body equals
// content unchanged, and err is nil.
//
// The parser intentionally handles only the simple subset of YAML that is used
// in practice for document front matter: scalar string values, boolean scalars,
// plain date scalars (YYYY-MM-DD), and block-sequence lists (lines starting
// with "  - ").  It does not depend on any external YAML library.
func ParseFrontMatter(content string) (*FrontMatter, string, error) {
	if !HasFrontMatter(content) {
		return nil, content, nil
	}

	lines := strings.Split(content, "\n")

	// Find the opening "---".
	openIdx := -1
	for i, line := range lines {
		trimmed := strings.TrimRightFunc(line, unicode.IsSpace)
		if trimmed == "" {
			continue
		}
		if trimmed == "---" {
			openIdx = i
		}
		break
	}
	if openIdx < 0 {
		return nil, content, nil
	}

	// Find the closing "---" after the opening delimiter.
	closeIdx := -1
	for i := openIdx + 1; i < len(lines); i++ {
		if strings.TrimRightFunc(lines[i], unicode.IsSpace) == "---" {
			closeIdx = i
			break
		}
	}
	if closeIdx < 0 {
		// Unclosed front matter — treat the whole rest as front matter.
		closeIdx = len(lines)
	}

	fmLines := lines[openIdx+1 : closeIdx]

	fm, err := parseFrontMatterLines(fmLines)
	if err != nil {
		return nil, content, err
	}

	// Body is everything after the closing delimiter.
	var body string
	if closeIdx < len(lines) {
		body = strings.Join(lines[closeIdx+1:], "\n")
		// Trim a single leading newline that typically follows the closing "---".
		body = strings.TrimPrefix(body, "\n")
	}

	return fm, body, nil
}

// parseFrontMatterLines processes the lines between the two "---" delimiters.
func parseFrontMatterLines(lines []string) (*FrontMatter, error) {
	fm := &FrontMatter{
		Extra: make(map[string]string),
	}

	var currentKey string
	var listValues []string
	inList := false

	flushList := func() {
		if !inList || currentKey == "" {
			return
		}
		assignFrontMatterField(fm, currentKey, listValues)
		currentKey = ""
		listValues = nil
		inList = false
	}

	for _, rawLine := range lines {
		// Detect list item continuation: line starts with optional spaces + "- ".
		stripped := strings.TrimRightFunc(rawLine, unicode.IsSpace)

		if inList {
			// A list item belonging to the current key?
			if isListItem(stripped) {
				listValues = append(listValues, parseListItem(stripped))
				continue
			}
			// Otherwise the list is done.
			flushList()
		}

		// Skip blank lines outside a list context.
		if strings.TrimSpace(stripped) == "" {
			continue
		}

		// Try to parse "key: value" or "key:" (start of a block sequence).
		key, value, ok := parseKeyValue(stripped)
		if !ok {
			continue
		}

		currentKey = key

		if value == "" {
			// Might be the start of a block sequence — peek handled by next iter.
			inList = true
			listValues = nil
			continue
		}

		// Scalar value — assign directly.
		assignFrontMatterField(fm, key, []string{value})
		currentKey = ""
	}

	// Flush any trailing list.
	flushList()

	return fm, nil
}

// parseKeyValue splits a line of the form "key: value" or "key:" into its
// parts. Returns ok=false if the line does not look like a key/value pair.
func parseKeyValue(line string) (key, value string, ok bool) {
	colonIdx := strings.IndexByte(line, ':')
	if colonIdx <= 0 {
		return "", "", false
	}

	rawKey := strings.TrimSpace(line[:colonIdx])
	rawVal := strings.TrimSpace(line[colonIdx+1:])

	// Keys must not contain spaces (avoids mis-parsing list items that
	// happen to contain a colon, such as URLs).
	if strings.ContainsAny(rawKey, " \t") {
		return "", "", false
	}

	// Strip surrounding quotes from the value if present.
	rawVal = stripQuotes(rawVal)

	return rawKey, rawVal, true
}

// isListItem reports whether line is a YAML block sequence item, e.g. "  - foo".
func isListItem(line string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	return strings.HasPrefix(trimmed, "- ") || trimmed == "-"
}

// parseListItem extracts the value from a YAML block sequence item line.
func parseListItem(line string) string {
	trimmed := strings.TrimLeft(line, " \t")
	if strings.HasPrefix(trimmed, "- ") {
		return stripQuotes(strings.TrimSpace(trimmed[2:]))
	}
	return ""
}

// stripQuotes removes a single pair of surrounding double- or single-quotes
// from s if present.
func stripQuotes(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') ||
			(s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// assignFrontMatterField populates the appropriate field on fm.
// For scalar fields values[0] is used; for list fields all values are used.
func assignFrontMatterField(fm *FrontMatter, key string, values []string) {
	scalar := ""
	if len(values) > 0 {
		scalar = values[0]
	}

	switch strings.ToLower(key) {
	case "id":
		fm.ID = scalar
	case "title":
		fm.Title = scalar
	case "slug":
		fm.Slug = scalar
	case "description":
		fm.Description = scalar
	case "author":
		fm.Author = scalar
	case "status":
		fm.Status = scalar
	case "draft":
		lower := strings.ToLower(scalar)
		fm.Draft = lower == "true" || lower == "yes" || lower == "1"
	case "date":
		if t, err := parseFrontMatterDate(scalar); err == nil {
			fm.Date = t
		}
	case "updated":
		if t, err := parseFrontMatterDate(scalar); err == nil {
			fm.Updated = t
		}
	case "tags":
		fm.Tags = append(fm.Tags, values...)
	default:
		// Store unknown keys in Extra, joining list values with ", ".
		fm.Extra[key] = strings.Join(values, ", ")
	}
}

// parseFrontMatterDate parses a date string in several common formats.
func parseFrontMatterDate(s string) (time.Time, error) {
	formats := []string{
		"2006-01-02",
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006/01/02",
		"01/02/2006",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, &invalidDateError{s}
}

type invalidDateError struct{ raw string }

func (e *invalidDateError) Error() string {
	return "frontmatter: cannot parse date: " + e.raw
}
