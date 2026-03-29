package repo

import (
	"context"
	"errors"
	"time"

	"github.com/zebaqui/notx-engine/core"
)

// ─────────────────────────────────────────────────────────────────────────────
// Sentinel errors
// ─────────────────────────────────────────────────────────────────────────────

var (
	// ErrNotFound is returned when a requested note or event does not exist.
	ErrNotFound = errors.New("repo: not found")

	// ErrAlreadyExists is returned when creating a note whose URN already exists.
	ErrAlreadyExists = errors.New("repo: already exists")

	// ErrSequenceConflict is returned when appending an event whose sequence
	// number does not immediately follow the current head sequence.
	ErrSequenceConflict = errors.New("repo: sequence conflict")

	// ErrNoteTypeImmutable is returned when an operation attempts to change the
	// note_type of an existing note.
	ErrNoteTypeImmutable = errors.New("repo: note_type is immutable")

	// ErrInvalidURN is returned when a URN is malformed or has the wrong type.
	ErrInvalidURN = errors.New("repo: invalid URN")
)

// ─────────────────────────────────────────────────────────────────────────────
// Filter / list options
// ─────────────────────────────────────────────────────────────────────────────

// ListOptions controls filtering and pagination for NoteRepository.List.
type ListOptions struct {
	// ProjectURN, if non-empty, restricts results to notes belonging to this
	// project. The value must be a valid notx:proj:<uuid> URN string.
	ProjectURN string

	// FolderURN, if non-empty, restricts results to notes inside this folder.
	FolderURN string

	// NoteTypeFilter, if set to a specific NoteType value, restricts results
	// to notes of that type. The zero value (NoteTypeNormal) is ambiguous, so
	// use the FilterByType flag to opt-in to type filtering.
	NoteTypeFilter core.NoteType

	// FilterByType enables NoteTypeFilter. When false, all note types are
	// returned regardless of NoteTypeFilter's value.
	FilterByType bool

	// IncludeDeleted includes soft-deleted notes in the result set when true.
	IncludeDeleted bool

	// PageSize is the maximum number of notes to return.
	// A value of 0 means "use the provider's default".
	PageSize int

	// PageToken is an opaque continuation token returned by a previous List
	// call. Pass the empty string to start from the beginning.
	PageToken string
}

// ListResult is the return value of NoteRepository.List.
type ListResult struct {
	// Notes is the page of note headers. Implementations must never return nil
	// here; an empty page is represented by a zero-length slice.
	Notes []*core.Note

	// NextPageToken is the continuation token for the next page.
	// An empty string signals that this is the last page.
	NextPageToken string
}

// ─────────────────────────────────────────────────────────────────────────────
// Search options
// ─────────────────────────────────────────────────────────────────────────────

// SearchOptions controls full-text search behaviour.
type SearchOptions struct {
	// Query is the search string. Required.
	Query string

	// PageSize and PageToken work identically to ListOptions.
	PageSize  int
	PageToken string
}

// SearchResult is a single match returned by NoteRepository.Search.
type SearchResult struct {
	// Note is the matching note's header (no event stream).
	Note *core.Note

	// Excerpt is a short snippet of the matching content with the query terms
	// highlighted (provider-defined format).
	Excerpt string
}

// SearchResults is the return value of NoteRepository.Search.
type SearchResults struct {
	Results       []*SearchResult
	NextPageToken string
}

// ─────────────────────────────────────────────────────────────────────────────
// Event append options
// ─────────────────────────────────────────────────────────────────────────────

// AppendEventOptions carries optional metadata for AppendEvent.
type AppendEventOptions struct {
	// ExpectSequence, if > 0, causes AppendEvent to return ErrSequenceConflict
	// if the current head sequence of the note does not equal ExpectSequence-1.
	// This gives callers an optimistic-concurrency check without a separate
	// read. Set to 0 to skip the check.
	ExpectSequence int
}

// ─────────────────────────────────────────────────────────────────────────────
// NoteRepository — the core storage abstraction
// ─────────────────────────────────────────────────────────────────────────────

// NoteRepository is the single storage interface used by all server layers.
//
// Implementations must be safe for concurrent use. All methods accept a
// context.Context; implementations should respect cancellation and deadlines.
//
// Security invariants that every implementation MUST enforce:
//
//  1. A note's NoteType is immutable after creation.
//  2. Secure note events (those carrying an EncryptedPayload) must be stored
//     verbatim — the implementation must never attempt to inspect or decrypt
//     the payload.
//  3. Only normal note content may be passed to the search index.
type NoteRepository interface {
	// ── Note lifecycle ───────────────────────────────────────────────────────

	// Create persists a new note header. The note's event stream must be empty
	// at creation time; events are added via AppendEvent.
	//
	// Returns ErrAlreadyExists if a note with the same URN already exists.
	Create(ctx context.Context, note *core.Note) error

	// Get retrieves a note by URN, including its full event stream.
	//
	// Returns ErrNotFound if no note with that URN exists.
	Get(ctx context.Context, urn string) (*core.Note, error)

	// List returns a filtered, paginated list of notes.
	// Implementations must never include secure note content in index lookups.
	List(ctx context.Context, opts ListOptions) (*ListResult, error)

	// Update persists changes to a note's mutable header fields (Name,
	// ProjectURN, FolderURN, ParentURN, NodeLinks, Deleted).
	//
	// NoteType MUST NOT be changed; Update must return ErrNoteTypeImmutable if
	// the caller attempts to alter it.
	//
	// Returns ErrNotFound if the note does not exist.
	Update(ctx context.Context, note *core.Note) error

	// Delete soft-deletes a note by setting its Deleted flag. The note and its
	// events remain in storage and can be retrieved with IncludeDeleted=true.
	//
	// Returns ErrNotFound if the note does not exist.
	Delete(ctx context.Context, urn string) error

	// ── Event stream ────────────────────────────────────────────────────────

	// AppendEvent appends a single event to an existing note's event stream.
	//
	// The implementation is responsible for:
	//   - Validating that event.Sequence == note.HeadSequence()+1.
	//   - Updating the note's UpdatedAt and HeadSequence.
	//   - For normal notes: forwarding line entries to the search index.
	//   - For secure notes: storing the encrypted blob verbatim, skipping index.
	//
	// Returns ErrNotFound if the note does not exist.
	// Returns ErrSequenceConflict if the sequence number is wrong.
	AppendEvent(ctx context.Context, event *core.Event, opts AppendEventOptions) error

	// Events returns all events for a note starting from fromSequence
	// (inclusive, 1-based). Pass fromSequence=1 to retrieve the full stream.
	//
	// Returns ErrNotFound if the note does not exist.
	Events(ctx context.Context, noteURN string, fromSequence int) ([]*core.Event, error)

	// ── Search ──────────────────────────────────────────────────────────────

	// Search performs a full-text search over normal note content only.
	// Implementations must guarantee that secure notes are never included in
	// results, even if the underlying index accidentally holds their metadata.
	//
	// Returns an empty SearchResults (not an error) when no matches are found.
	Search(ctx context.Context, opts SearchOptions) (*SearchResults, error)

	// ── Lifecycle ───────────────────────────────────────────────────────────

	// Close releases all resources held by the repository (open file handles,
	// database connections, etc.). After Close returns, no other method may be
	// called.
	Close() error
}

// ─────────────────────────────────────────────────────────────────────────────
// IndexEntry — the lightweight record written to the search / list index
// ─────────────────────────────────────────────────────────────────────────────

// IndexEntry is the compact representation stored in the Badger index for each
// note. It contains only the fields needed for list/search responses so that
// the full .notx file does not need to be read for every query.
//
// Implementations serialize IndexEntry to/from JSON (or msgpack) when writing
// to the index.
type IndexEntry struct {
	URN        string        `json:"urn"`
	Name       string        `json:"name"`
	NoteType   core.NoteType `json:"note_type"`
	ProjectURN string        `json:"project_urn,omitempty"`
	FolderURN  string        `json:"folder_urn,omitempty"`
	Deleted    bool          `json:"deleted"`
	CreatedAt  time.Time     `json:"created_at"`
	UpdatedAt  time.Time     `json:"updated_at"`
	// HeadSequence is the sequence number of the last persisted event.
	HeadSequence int `json:"head_sequence"`
}

// ─────────────────────────────────────────────────────────────────────────────
// ProjectRepository — storage for projects and folders
// ─────────────────────────────────────────────────────────────────────────────

// ProjectListOptions controls filtering and pagination for
// ProjectRepository.ListProjects.
type ProjectListOptions struct {
	// IncludeDeleted includes soft-deleted projects when true.
	IncludeDeleted bool
	// PageSize / PageToken mirror the semantics on ListOptions.
	PageSize  int
	PageToken string
}

// ProjectListResult is the return value of ProjectRepository.ListProjects.
type ProjectListResult struct {
	Projects      []*core.Project
	NextPageToken string
}

// FolderListOptions controls filtering and pagination for
// ProjectRepository.ListFolders.
type FolderListOptions struct {
	// ProjectURN, if non-empty, restricts results to folders inside this project.
	ProjectURN string
	// IncludeDeleted includes soft-deleted folders when true.
	IncludeDeleted bool
	// PageSize / PageToken mirror the semantics on ListOptions.
	PageSize  int
	PageToken string
}

// FolderListResult is the return value of ProjectRepository.ListFolders.
type FolderListResult struct {
	Folders       []*core.Folder
	NextPageToken string
}

// ProjectRepository is the storage abstraction for projects and folders.
// Both entity types are index-only — they have no on-disk file counterpart.
//
// All methods accept a context.Context and must respect cancellation.
// Implementations must be safe for concurrent use.
type ProjectRepository interface {
	// ── Projects ─────────────────────────────────────────────────────────────

	// CreateProject persists a new project.
	// Returns ErrAlreadyExists if a project with the same URN already exists.
	CreateProject(ctx context.Context, p *core.Project) error

	// GetProject retrieves a project by URN.
	// Returns ErrNotFound if no project with that URN exists.
	GetProject(ctx context.Context, urn string) (*core.Project, error)

	// ListProjects returns a filtered, paginated list of projects.
	ListProjects(ctx context.Context, opts ProjectListOptions) (*ProjectListResult, error)

	// UpdateProject persists changes to a project's mutable fields
	// (Name, Description, Deleted).
	// Returns ErrNotFound if the project does not exist.
	UpdateProject(ctx context.Context, p *core.Project) error

	// DeleteProject soft-deletes a project by setting its Deleted flag.
	// Returns ErrNotFound if the project does not exist.
	DeleteProject(ctx context.Context, urn string) error

	// ── Folders ──────────────────────────────────────────────────────────────

	// CreateFolder persists a new folder.
	// Returns ErrAlreadyExists if a folder with the same URN already exists.
	CreateFolder(ctx context.Context, f *core.Folder) error

	// GetFolder retrieves a folder by URN.
	// Returns ErrNotFound if no folder with that URN exists.
	GetFolder(ctx context.Context, urn string) (*core.Folder, error)

	// ListFolders returns a filtered, paginated list of folders.
	ListFolders(ctx context.Context, opts FolderListOptions) (*FolderListResult, error)

	// UpdateFolder persists changes to a folder's mutable fields
	// (Name, Description, Deleted).
	// Returns ErrNotFound if the folder does not exist.
	UpdateFolder(ctx context.Context, f *core.Folder) error

	// DeleteFolder soft-deletes a folder by setting its Deleted flag.
	// Returns ErrNotFound if the folder does not exist.
	DeleteFolder(ctx context.Context, urn string) error
}

// ─────────────────────────────────────────────────────────────────────────────
// DeviceListOptions / DeviceListResult
// ─────────────────────────────────────────────────────────────────────────────

// DeviceListOptions controls filtering for DeviceRepository.ListDevices.
type DeviceListOptions struct {
	// OwnerURN, if non-empty, restricts results to devices owned by this user.
	OwnerURN string
	// IncludeRevoked includes revoked devices in the result when true.
	IncludeRevoked bool
}

// DeviceListResult is the return value of DeviceRepository.ListDevices.
type DeviceListResult struct {
	Devices []*core.Device
}

// ─────────────────────────────────────────────────────────────────────────────
// DeviceRepository — storage for registered devices
// ─────────────────────────────────────────────────────────────────────────────

// DeviceRepository is the storage abstraction for client devices.
// Implementations must be safe for concurrent use.
type DeviceRepository interface {
	// RegisterDevice persists a new device.
	// Returns ErrAlreadyExists if a device with the same URN already exists.
	RegisterDevice(ctx context.Context, d *core.Device) error

	// GetDevice retrieves a device by URN.
	// Returns ErrNotFound if no device with that URN exists.
	GetDevice(ctx context.Context, urn string) (*core.Device, error)

	// ListDevices returns devices, optionally filtered by owner.
	ListDevices(ctx context.Context, opts DeviceListOptions) (*DeviceListResult, error)

	// UpdateDevice persists changes to mutable fields (Name, LastSeenAt).
	// Returns ErrNotFound if the device does not exist.
	UpdateDevice(ctx context.Context, d *core.Device) error

	// RevokeDevice permanently revokes a device by setting Revoked=true.
	// Returns ErrNotFound if the device does not exist.
	RevokeDevice(ctx context.Context, urn string) error
}
