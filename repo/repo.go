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

	// UpdateEventWrappedKeys merges the provided wrappedKeys map into the
	// WrappedKeys field of every event belonging to the given secure note.
	// Keys present in wrappedKeys overwrite existing values; keys absent from
	// wrappedKeys are left unchanged.
	//
	// This is the server-side half of the share-secure-note protocol: the
	// client re-wraps the Content Encryption Key (CEK) for each recipient
	// device and uploads the wrapped keys; the server stores them against the
	// encrypted event blobs without ever seeing the plaintext CEK.
	//
	// Returns ErrNotFound if the note does not exist.
	UpdateEventWrappedKeys(ctx context.Context, noteURN string, wrappedKeys map[string][]byte) (int, error)

	// ReceiveSharedNote stores a note header and its full event stream that
	// have been forwarded from a paired server. It is idempotent: if the note
	// already exists the header is updated and any events with sequences higher
	// than the current head are appended.
	//
	// This is used by the cross-server note-sharing flow: Server A pushes a
	// note to Server B so that Client B (registered on Server B) can read it.
	ReceiveSharedNote(ctx context.Context, note *core.Note, events []*core.Event) error

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

// ─────────────────────────────────────────────────────────────────────────────
// UserListOptions / UserListResult
// ─────────────────────────────────────────────────────────────────────────────

// UserListOptions controls filtering and pagination for UserRepository.ListUsers.
type UserListOptions struct {
	// IncludeDeleted includes soft-deleted users when true.
	IncludeDeleted bool
	// PageSize is the maximum number of users to return. 0 means provider default.
	PageSize int
	// PageToken is the opaque continuation token from a previous call.
	PageToken string
}

// UserListResult is the return value of UserRepository.ListUsers.
type UserListResult struct {
	Users         []*core.User
	NextPageToken string
}

// ─────────────────────────────────────────────────────────────────────────────
// UserRepository — storage for human users
// ─────────────────────────────────────────────────────────────────────────────

// UserRepository is the storage abstraction for user records.
// Implementations must be safe for concurrent use.
type UserRepository interface {
	// CreateUser persists a new user.
	// Returns ErrAlreadyExists if a user with the same URN already exists.
	CreateUser(ctx context.Context, u *core.User) error

	// GetUser retrieves a user by URN.
	// Returns ErrNotFound if no user with that URN exists.
	GetUser(ctx context.Context, urn string) (*core.User, error)

	// ListUsers returns a paginated list of users.
	ListUsers(ctx context.Context, opts UserListOptions) (*UserListResult, error)

	// UpdateUser persists changes to mutable fields (DisplayName, Email, Deleted).
	// Returns ErrNotFound if the user does not exist.
	UpdateUser(ctx context.Context, u *core.User) error

	// DeleteUser soft-deletes a user by setting Deleted=true.
	// Returns ErrNotFound if the user does not exist.
	DeleteUser(ctx context.Context, urn string) error
}

// ─────────────────────────────────────────────────────────────────────────────
// LinkRepository — anchors, backlinks, and external links
// ─────────────────────────────────────────────────────────────────────────────

// AnchorRecord is the server-side representation of a declared anchor.
type AnchorRecord struct {
	NoteURN   string    `json:"note_urn"`
	AnchorID  string    `json:"anchor_id"`
	Line      int       `json:"line"`
	CharStart int       `json:"char_start"`
	CharEnd   int       `json:"char_end"`
	Preview   string    `json:"preview"`
	Status    string    `json:"status"` // "ok", "broken", "deprecated"
	UpdatedAt time.Time `json:"updated_at"`
}

// BacklinkRecord is a single entry in the backlink index.
type BacklinkRecord struct {
	SourceURN    string    `json:"source_urn"`
	TargetURN    string    `json:"target_urn"`
	TargetAnchor string    `json:"target_anchor"`
	Label        string    `json:"label,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// ExternalLinkRecord is a single entry in the external links index.
type ExternalLinkRecord struct {
	SourceURN string    `json:"source_urn"`
	URI       string    `json:"uri"`
	Label     string    `json:"label,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// RecentBacklinksOptions controls filtering for RecentBacklinks.
type RecentBacklinksOptions struct {
	NoteURN string // filter where source_urn OR target_urn matches (either side)
	Label   string // substring filter on label (case-insensitive LIKE)
	Limit   int    // 0 = default 50, max 200
}

// LinkRepository manages the anchor index, backlink index, and external links
// index as defined in the notx Link Specification.
type LinkRepository interface {
	// ── Anchors ──────────────────────────────────────────────────────────────

	// UpsertAnchor inserts or updates an anchor record in the server-side index.
	UpsertAnchor(ctx context.Context, a AnchorRecord) error

	// DeleteAnchor removes an anchor from the index. If createTombstone is true,
	// the anchor is updated to status="deprecated" instead of being removed.
	DeleteAnchor(ctx context.Context, noteURN, anchorID string, createTombstone bool) error

	// GetAnchor retrieves a single anchor by note URN and anchor ID.
	// Returns ErrNotFound if the anchor does not exist.
	GetAnchor(ctx context.Context, noteURN, anchorID string) (AnchorRecord, error)

	// ListAnchors returns all anchors declared in a note, ordered by line ASC.
	ListAnchors(ctx context.Context, noteURN string) ([]AnchorRecord, error)

	// ── Backlinks ─────────────────────────────────────────────────────────────

	// UpsertBacklink inserts or updates a backlink record.
	UpsertBacklink(ctx context.Context, b BacklinkRecord) error

	// DeleteBacklink removes a specific backlink record.
	DeleteBacklink(ctx context.Context, sourceURN, targetURN, targetAnchor string) error

	// ListBacklinks returns all inbound backlinks for a note (all anchors).
	// If anchorID is non-empty, restricts to backlinks for that anchor only.
	ListBacklinks(ctx context.Context, targetURN, anchorID string) ([]BacklinkRecord, error)

	// ListOutboundLinks returns all outbound backlink records from a source note.
	ListOutboundLinks(ctx context.Context, sourceURN string) ([]BacklinkRecord, error)

	// GetReferrers returns the URNs of all notes that link to a specific anchor.
	// Used by break detection to populate the referrers list.
	GetReferrers(ctx context.Context, targetURN, anchorID string) ([]string, error)

	// ── External links ────────────────────────────────────────────────────────

	// UpsertExternalLink inserts or updates an external link record.
	UpsertExternalLink(ctx context.Context, e ExternalLinkRecord) error

	// DeleteExternalLink removes an external link record.
	DeleteExternalLink(ctx context.Context, sourceURN, uri string) error

	// ListExternalLinks returns all external links from a source note.
	ListExternalLinks(ctx context.Context, sourceURN string) ([]ExternalLinkRecord, error)

	// RecentBacklinks returns the most recently created backlinks across all notes,
	// ordered by created_at DESC. All filter fields are optional — omit to browse
	// all. limit=0 defaults to 50, max 200.
	RecentBacklinks(ctx context.Context, opts RecentBacklinksOptions) ([]BacklinkRecord, error)
}

// ─────────────────────────────────────────────────────────────────────────────
// ContextRepository — context bursts and candidate relations
// ─────────────────────────────────────────────────────────────────────────────

// BurstRecord is the server-side representation of a context burst.
type BurstRecord struct {
	ID         string    `json:"id"`
	NoteURN    string    `json:"note_urn"`
	ProjectURN string    `json:"project_urn"`
	FolderURN  string    `json:"folder_urn"`
	AuthorURN  string    `json:"author_urn"`
	Sequence   int       `json:"sequence"`
	LineStart  int       `json:"line_start"`
	LineEnd    int       `json:"line_end"`
	Text       string    `json:"text"`
	Tokens     string    `json:"tokens"` // space-separated normalized token string
	Truncated  bool      `json:"truncated"`
	CreatedAt  time.Time `json:"created_at"`
}

// CandidateRecord is a candidate relation between two bursts.
type CandidateRecord struct {
	ID           string     `json:"id"`
	BurstAID     string     `json:"burst_a_id"`
	BurstBID     string     `json:"burst_b_id"`
	NoteURN_A    string     `json:"note_urn_a"`
	NoteURN_B    string     `json:"note_urn_b"`
	ProjectURN   string     `json:"project_urn"`
	OverlapScore float64    `json:"overlap_score"`
	BM25Score    float64    `json:"bm25_score"`
	Status       string     `json:"status"` // pending, promoted, dismissed, expired
	CreatedAt    time.Time  `json:"created_at"`
	ReviewedAt   *time.Time `json:"reviewed_at,omitempty"`
	ReviewedBy   string     `json:"reviewed_by,omitempty"`
	PromotedLink string     `json:"promoted_link,omitempty"`
}

// CandidateListOptions controls filtering and pagination for ContextRepository.ListCandidates.
type CandidateListOptions struct {
	ProjectURN string
	NoteURN    string  // filter to candidates involving this note (as note_urn_a OR note_urn_b)
	Status     string  // "pending", "promoted", "dismissed", "expired", or "" for all
	MinScore   float64 // filter by overlap_score floor
	PageSize   int
	PageToken  string
}

// PromoteOptions carries the parameters for promoting a candidate.
type PromoteOptions struct {
	Label       string // optional label for node_links key
	Direction   string // "both", "a_to_b", "b_to_a"
	ReviewerURN string
}

// PromoteResult is the result of a candidate promotion.
type PromoteResult struct {
	AnchorAID string `json:"anchor_a_id"`
	AnchorBID string `json:"anchor_b_id"`
	LinkAToB  string `json:"link_a_to_b"`
	LinkBToA  string `json:"link_b_to_a"`
}

// ProjectContextConfig holds per-project context graph rate limit overrides.
type ProjectContextConfig struct {
	ProjectURN               string    `json:"project_urn"`
	BurstMaxPerNotePerDay    *int      `json:"burst_max_per_note_per_day"`    // nil = use global default
	BurstMaxPerProjectPerDay *int      `json:"burst_max_per_project_per_day"` // nil = use global default
	UpdatedAt                time.Time `json:"updated_at"`
}

// ContextStats holds health and queue statistics for the context graph layer.
type ContextStats struct {
	BurstsTotal                 int     `json:"bursts_total"`
	BurstsToday                 int     `json:"bursts_today"`
	CandidatesPending           int     `json:"candidates_pending"`
	CandidatesPendingUnenriched int     `json:"candidates_pending_unenriched"`
	CandidatesPromoted          int     `json:"candidates_promoted"`
	CandidatesDismissed         int     `json:"candidates_dismissed"`
	OldestPendingAgeDays        float64 `json:"oldest_pending_age_days"`
}

// ContextRepository manages context bursts and candidate relations.
type ContextRepository interface {
	// ── Rate limits ──────────────────────────────────────────────────────────

	// BurstCountToday returns the number of bursts created today (UTC) for the
	// given note and project. Used for rate limit checks on the hot write path.
	BurstCountToday(ctx context.Context, noteURN, projectURN string) (noteCount, projectCount int, err error)

	// ── Bursts ───────────────────────────────────────────────────────────────

	// MostRecentBurst returns the most recent burst for a note, used for the
	// consecutive similarity skip check.
	// Returns (record, true, nil) if found, (zero, false, nil) if none exist.
	MostRecentBurst(ctx context.Context, noteURN string) (BurstRecord, bool, error)

	// StoreBurst persists a new burst record and its FTS5 row.
	StoreBurst(ctx context.Context, b BurstRecord) error

	// ListBursts returns bursts for a note ordered by sequence ASC.
	// sinceSeq=0 means all bursts. Returns (records, nextPageToken, error).
	ListBursts(ctx context.Context, noteURN string, sinceSeq, pageSize int) ([]BurstRecord, string, error)

	// GetBurst retrieves a single burst by ID.
	// Returns ErrNotFound if not present.
	GetBurst(ctx context.Context, id string) (BurstRecord, error)

	// SweepBursts deletes burst rows older than olderThan. Returns the count deleted.
	SweepBursts(ctx context.Context, olderThan time.Time) (int, error)

	// IndexNoteIntoProject backfills existing bursts for a note with the given
	// projectURN and runs candidate detection against the project's burst pool.
	// Call this after assigning a previously project-less (or differently-scoped)
	// note to a new project so existing content becomes visible to the scorer.
	// authorURN is stamped on any new candidates created. Returns the number of
	// new candidates created.
	IndexNoteIntoProject(ctx context.Context, noteURN, projectURN string) (newCandidates int, err error)

	// RecentBurstsInProject fetches up to limit bursts from different notes in the
	// same project, created within the last days, ordered by created_at DESC.
	// Used for candidate detection after a new burst is stored.
	RecentBurstsInProject(ctx context.Context, projectURN string, days, limit int) ([]BurstRecord, error)

	// ── Candidates ───────────────────────────────────────────────────────────

	// StoreCandidates batch-inserts new candidate relation records.
	StoreCandidates(ctx context.Context, candidates []CandidateRecord) error

	// UpdateCandidateBM25 updates the bm25_score for a single candidate.
	UpdateCandidateBM25(ctx context.Context, id string, score float64) error

	// ListCandidates returns a paginated list of candidates matching the options.
	// Ordered by bm25_score DESC, overlap_score DESC.
	ListCandidates(ctx context.Context, opts CandidateListOptions) ([]CandidateRecord, string, error)

	// GetCandidate retrieves a single candidate by ID.
	// Returns ErrNotFound if not present.
	GetCandidate(ctx context.Context, id string) (CandidateRecord, error)

	// PromoteCandidate converts a pending candidate to a promoted link.
	// Creates anchor entries, link tokens, and updates the candidate status.
	// Returns the created anchor IDs and link tokens.
	PromoteCandidate(ctx context.Context, id string, opts PromoteOptions) (PromoteResult, error)

	// DismissCandidate marks a candidate as dismissed.
	DismissCandidate(ctx context.Context, id, reviewerURN string) error

	// ── Per-project config ────────────────────────────────────────────────────

	// GetProjectContextConfig retrieves per-project rate limit overrides.
	// Returns ErrNotFound if no override exists for the project.
	GetProjectContextConfig(ctx context.Context, projectURN string) (ProjectContextConfig, error)

	// UpsertProjectContextConfig sets per-project rate limit overrides.
	UpsertProjectContextConfig(ctx context.Context, cfg ProjectContextConfig) error

	// ── Stats ─────────────────────────────────────────────────────────────────

	// GetContextStats returns queue health statistics.
	// If projectURN is non-empty, scopes stats to that project.
	GetContextStats(ctx context.Context, projectURN string) (ContextStats, error)
}
