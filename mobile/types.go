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
type NoteList struct {
	// Items holds the notes in this page.
	Items []*NoteHeader

	// NextPageToken is the opaque continuation token for the next page,
	// or empty string when this is the last page.
	NextPageToken string
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
type SearchResults struct {
	// Results holds the matches for this page.
	Results []*SearchResult

	// NextPageToken is the opaque continuation token, or empty for last page.
	NextPageToken string
}
