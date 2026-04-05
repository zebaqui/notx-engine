package mobile

// NoteHeader is a gomobile-compatible summary of a note returned by list and
// search operations.
//
// gomobile cannot bridge map types or pointer fields, so URN references are
// flat strings (empty string = absent).
type NoteHeader struct {
	// URN is the globally unique note identifier (e.g. "urn:notx:note:<uuidv7>").
	URN string

	// Name is the human-readable title.
	Name string

	// NoteType is "normal" or "secure".
	NoteType string

	// ProjectURN is the owning project URN, or empty if none.
	ProjectURN string

	// FolderURN is the containing folder URN, or empty if none.
	FolderURN string

	// Deleted is true when the note has been soft-deleted.
	Deleted bool

	// CreatedAtMs is the creation timestamp in Unix milliseconds.
	CreatedAtMs int64

	// UpdatedAtMs is the last-update timestamp in Unix milliseconds.
	UpdatedAtMs int64

	// HeadSequence is the sequence number of the most recent event.
	HeadSequence int
}

// NoteList is a page of NoteHeader results returned by Engine.ListNotes.
// gomobile cannot bridge []*NoteHeader directly, so items are accessed via
// Count / Item instead.
type NoteList struct {
	items []*NoteHeader

	// NextPageToken is the opaque continuation token for the next page,
	// or empty string when this is the last page.
	NextPageToken string
}

// Count returns the number of notes in this page.
func (l *NoteList) Count() int {
	if l == nil {
		return 0
	}
	return len(l.items)
}

// Item returns the NoteHeader at index i (0-based).
// Returns nil when i is out of range.
func (l *NoteList) Item(i int) *NoteHeader {
	if l == nil || i < 0 || i >= len(l.items) {
		return nil
	}
	return l.items[i]
}

// ProjectHeader is a gomobile-compatible summary of a project.
type ProjectHeader struct {
	// URN is the project URN.
	URN string

	// Name is the human-readable display name.
	Name string

	// Deleted is true when the project has been soft-deleted.
	Deleted bool

	// CreatedAtMs is the creation timestamp in Unix milliseconds.
	CreatedAtMs int64

	// UpdatedAtMs is the last-update timestamp in Unix milliseconds.
	UpdatedAtMs int64
}

// ProjectList is the return value of Engine.ListProjects.
// gomobile cannot bridge []*ProjectHeader directly, so items are accessed via
// Count / Item instead.
type ProjectList struct {
	items []*ProjectHeader
}

// Count returns the number of projects in the list.
func (l *ProjectList) Count() int {
	if l == nil {
		return 0
	}
	return len(l.items)
}

// Item returns the ProjectHeader at index i (0-based).
// Returns nil when i is out of range.
func (l *ProjectList) Item(i int) *ProjectHeader {
	if l == nil || i < 0 || i >= len(l.items) {
		return nil
	}
	return l.items[i]
}

// FolderHeader is a gomobile-compatible summary of a folder.
type FolderHeader struct {
	// URN is the folder URN.
	URN string

	// ProjectURN is the URN of the owning project.
	ProjectURN string

	// Name is the human-readable display name.
	Name string

	// Deleted is true when the folder has been soft-deleted.
	Deleted bool

	// CreatedAtMs is the creation timestamp in Unix milliseconds.
	CreatedAtMs int64

	// UpdatedAtMs is the last-update timestamp in Unix milliseconds.
	UpdatedAtMs int64
}

// FolderList is the return value of Engine.ListFolders.
// gomobile cannot bridge []*FolderHeader directly, so items are accessed via
// Count / Item instead.
type FolderList struct {
	items []*FolderHeader
}

// Count returns the number of folders in the list.
func (l *FolderList) Count() int {
	if l == nil {
		return 0
	}
	return len(l.items)
}

// Item returns the FolderHeader at index i (0-based).
// Returns nil when i is out of range.
func (l *FolderList) Item(i int) *FolderHeader {
	if l == nil || i < 0 || i >= len(l.items) {
		return nil
	}
	return l.items[i]
}

// ListOptions controls filtering and pagination for Engine.ListNotes.
type ListOptions struct {
	// ProjectURN restricts results to notes in this project (empty = all).
	ProjectURN string

	// FolderURN restricts results to notes in this folder (empty = all).
	FolderURN string

	// NoteType filters by note type ("normal", "secure", or "" for all).
	NoteType string

	// IncludeDeleted includes soft-deleted notes when true.
	IncludeDeleted bool

	// PageSize is the maximum number of results. 0 = provider default.
	PageSize int

	// PageToken is the continuation token from a previous call, or "".
	PageToken string
}

// SearchOptions controls full-text search for Engine.SearchNotes.
type SearchOptions struct {
	// Query is the search string. Required.
	Query string

	// PageSize is the maximum number of results. 0 = provider default.
	PageSize int

	// PageToken is the continuation token from a previous call, or "".
	PageToken string
}

// SearchResult is a single full-text search match.
type SearchResult struct {
	// Note is the matching note header.
	Note *NoteHeader

	// Excerpt is a short snippet with matched terms (provider-defined format).
	Excerpt string
}

// SearchResults is the return value of Engine.SearchNotes.
// gomobile cannot bridge []*SearchResult directly, so items are accessed via
// Count / Item instead.
type SearchResults struct {
	items []*SearchResult

	// NextPageToken is the opaque continuation token, or empty for last page.
	NextPageToken string
}

// Count returns the number of search results in this page.
func (s *SearchResults) Count() int {
	if s == nil {
		return 0
	}
	return len(s.items)
}

// Item returns the SearchResult at index i (0-based).
// Returns nil when i is out of range.
func (s *SearchResults) Item(i int) *SearchResult {
	if s == nil || i < 0 || i >= len(s.items) {
		return nil
	}
	return s.items[i]
}
