package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zebaqui/notx-engine/core"
	"github.com/zebaqui/notx-engine/internal/repo"
)

// Provider is a fully in-memory implementation of repo.NoteRepository.
//
// It is intended exclusively for testing. No data survives beyond the lifetime
// of the Provider instance. All operations are safe for concurrent use.
type Provider struct {
	mu       sync.RWMutex
	notes    map[string]*core.Note          // urn → note (header + applied events)
	events   map[string][]*core.Event       // urn → ordered event slice
	projects map[string]*core.Project       // urn → project
	folders  map[string]*core.Folder        // urn → folder
	devices  map[string]*core.Device        // urn → device
	users    map[string]*core.User          // urn → user
	servers  map[string]*core.Server        // urn → server
	secrets  map[string]*repo.PairingSecret // id → pairing secret
}

// New returns an empty, ready-to-use in-memory Provider.
func New() *Provider {
	return &Provider{
		notes:    make(map[string]*core.Note),
		events:   make(map[string][]*core.Event),
		projects: make(map[string]*core.Project),
		folders:  make(map[string]*core.Folder),
		devices:  make(map[string]*core.Device),
		users:    make(map[string]*core.User),
		servers:  make(map[string]*core.Server),
		secrets:  make(map[string]*repo.PairingSecret),
	}
}

// Close is a no-op for the in-memory provider. It exists to satisfy the
// repo.NoteRepository interface.
func (p *Provider) Close() error { return nil }

// ─────────────────────────────────────────────────────────────────────────────
// Note lifecycle
// ─────────────────────────────────────────────────────────────────────────────

// Create stores a new note. Returns repo.ErrAlreadyExists if the URN is taken.
func (p *Provider) Create(ctx context.Context, note *core.Note) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	urn := note.URN.String()

	p.mu.Lock()
	defer p.mu.Unlock()

	if _, exists := p.notes[urn]; exists {
		return fmt.Errorf("%w: %s", repo.ErrAlreadyExists, urn)
	}

	// Store a shallow copy of the header so the caller cannot mutate our state.
	p.notes[urn] = cloneHeader(note)
	p.events[urn] = nil
	return nil
}

// Get returns the note for the given URN with its full event stream applied.
// Returns repo.ErrNotFound if the URN is unknown.
func (p *Provider) Get(ctx context.Context, urn string) (*core.Note, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	note, ok := p.notes[urn]
	if !ok {
		return nil, fmt.Errorf("%w: %s", repo.ErrNotFound, urn)
	}

	// Return a clone so callers cannot mutate internal state.
	return cloneNoteWithEvents(note, p.events[urn])
}

// List returns a filtered, paginated list of note headers.
func (p *Provider) List(ctx context.Context, opts repo.ListOptions) (*repo.ListResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	// Collect and sort URNs for stable ordering.
	urns := make([]string, 0, len(p.notes))
	for urn := range p.notes {
		urns = append(urns, urn)
	}
	sort.Strings(urns)

	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}

	// Find the start position from the page token.
	startIdx := 0
	if opts.PageToken != "" {
		for i, urn := range urns {
			if urn == opts.PageToken {
				startIdx = i + 1
				break
			}
		}
	}

	var results []*core.Note
	for _, urn := range urns[startIdx:] {
		n := p.notes[urn]

		// Apply filters.
		if !opts.IncludeDeleted && n.Deleted {
			continue
		}
		if opts.ProjectURN != "" {
			if n.ProjectURN == nil || n.ProjectURN.String() != opts.ProjectURN {
				continue
			}
		}
		if opts.FolderURN != "" {
			if n.FolderURN == nil || n.FolderURN.String() != opts.FolderURN {
				continue
			}
		}
		if opts.FilterByType && n.NoteType != opts.NoteTypeFilter {
			continue
		}

		results = append(results, cloneHeader(n))

		if len(results) >= pageSize {
			break
		}
	}

	nextToken := ""
	if len(results) == pageSize {
		nextToken = results[len(results)-1].URN.String()
	}

	if results == nil {
		results = []*core.Note{}
	}

	return &repo.ListResult{
		Notes:         results,
		NextPageToken: nextToken,
	}, nil
}

// Update persists changes to a note's mutable header fields.
// Returns repo.ErrNoteTypeImmutable if the caller tries to change NoteType.
// Returns repo.ErrNotFound if the URN is unknown.
func (p *Provider) Update(ctx context.Context, note *core.Note) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	urn := note.URN.String()

	p.mu.Lock()
	defer p.mu.Unlock()

	existing, ok := p.notes[urn]
	if !ok {
		return fmt.Errorf("%w: %s", repo.ErrNotFound, urn)
	}
	if existing.NoteType != note.NoteType {
		return fmt.Errorf("%w: cannot change note_type from %q to %q",
			repo.ErrNoteTypeImmutable, existing.NoteType, note.NoteType)
	}

	updated := cloneHeader(note)
	// Preserve the event stream sequence count on the header.
	updated.UpdatedAt = note.UpdatedAt
	p.notes[urn] = updated
	return nil
}

// Delete soft-deletes a note by setting its Deleted flag.
// Returns repo.ErrNotFound if the URN is unknown.
func (p *Provider) Delete(ctx context.Context, urn string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	n, ok := p.notes[urn]
	if !ok {
		return fmt.Errorf("%w: %s", repo.ErrNotFound, urn)
	}
	n.Deleted = true
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Event stream
// ─────────────────────────────────────────────────────────────────────────────

// AppendEvent appends a single event to an existing note's stream.
// Returns repo.ErrNotFound, repo.ErrSequenceConflict as appropriate.
func (p *Provider) AppendEvent(ctx context.Context, event *core.Event, opts repo.AppendEventOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	noteURN := event.NoteURN.String()

	p.mu.Lock()
	defer p.mu.Unlock()

	n, ok := p.notes[noteURN]
	if !ok {
		return fmt.Errorf("%w: %s", repo.ErrNotFound, noteURN)
	}

	currentHead := len(p.events[noteURN])

	// Optimistic concurrency check.
	if opts.ExpectSequence > 0 && currentHead != opts.ExpectSequence-1 {
		return fmt.Errorf("%w: expected head %d, got %d",
			repo.ErrSequenceConflict, opts.ExpectSequence-1, currentHead)
	}

	expectedSeq := currentHead + 1
	if event.Sequence != expectedSeq {
		return fmt.Errorf("%w: expected sequence %d, got %d",
			repo.ErrSequenceConflict, expectedSeq, event.Sequence)
	}

	// Apply the event to the header note so HeadSequence and UpdatedAt stay current.
	if err := n.ApplyEvent(event); err != nil {
		return fmt.Errorf("memory provider: apply event: %w", err)
	}

	p.events[noteURN] = append(p.events[noteURN], event)
	return nil
}

// Events returns all events for a note starting from fromSequence (inclusive).
// Returns repo.ErrNotFound if the URN is unknown.
func (p *Provider) Events(ctx context.Context, noteURN string, fromSequence int) ([]*core.Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	if _, ok := p.notes[noteURN]; !ok {
		return nil, fmt.Errorf("%w: %s", repo.ErrNotFound, noteURN)
	}

	all := p.events[noteURN]
	if fromSequence <= 1 {
		return copyEvents(all), nil
	}

	var filtered []*core.Event
	for _, ev := range all {
		if ev.Sequence >= fromSequence {
			filtered = append(filtered, ev)
		}
	}
	return copyEvents(filtered), nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Search
// ─────────────────────────────────────────────────────────────────────────────

// Search performs a simple case-insensitive substring match over normal note
// names and content. Secure notes are never included in results.
func (p *Provider) Search(ctx context.Context, opts repo.SearchOptions) (*repo.SearchResults, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	query := strings.ToLower(strings.TrimSpace(opts.Query))
	if query == "" {
		return &repo.SearchResults{Results: []*repo.SearchResult{}}, nil
	}

	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	urns := make([]string, 0, len(p.notes))
	for urn := range p.notes {
		urns = append(urns, urn)
	}
	sort.Strings(urns)

	var results []*repo.SearchResult
	for _, urn := range urns {
		n := p.notes[urn]

		// Security invariant: never include secure notes in search results.
		if n.NoteType == core.NoteTypeSecure {
			continue
		}
		if n.Deleted {
			continue
		}

		// Match against name.
		if strings.Contains(strings.ToLower(n.Name), query) {
			results = append(results, &repo.SearchResult{
				Note:    cloneHeader(n),
				Excerpt: fmt.Sprintf("matched %q in name", opts.Query),
			})
			if len(results) >= pageSize {
				break
			}
			continue
		}

		// Match against event content.
		for _, ev := range p.events[urn] {
			matched := false
			for _, entry := range ev.Entries {
				if entry.Op == core.LineOpSet &&
					strings.Contains(strings.ToLower(entry.Content), query) {
					matched = true
					break
				}
			}
			if matched {
				results = append(results, &repo.SearchResult{
					Note:    cloneHeader(n),
					Excerpt: fmt.Sprintf("matched %q in content", opts.Query),
				})
				break
			}
		}

		if len(results) >= pageSize {
			break
		}
	}

	if results == nil {
		results = []*repo.SearchResult{}
	}

	return &repo.SearchResults{Results: results}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// ProjectRepository implementation
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) CreateProject(ctx context.Context, proj *core.Project) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	urn := proj.URN.String()
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.projects[urn]; exists {
		return fmt.Errorf("%w: %s", repo.ErrAlreadyExists, urn)
	}
	clone := *proj
	p.projects[urn] = &clone
	return nil
}

func (p *Provider) GetProject(ctx context.Context, urn string) (*core.Project, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	proj, ok := p.projects[urn]
	if !ok {
		return nil, fmt.Errorf("%w: %s", repo.ErrNotFound, urn)
	}
	clone := *proj
	return &clone, nil
}

func (p *Provider) ListProjects(ctx context.Context, opts repo.ProjectListOptions) (*repo.ProjectListResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()

	urns := make([]string, 0, len(p.projects))
	for urn := range p.projects {
		urns = append(urns, urn)
	}
	sort.Strings(urns)

	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}

	startIdx := 0
	if opts.PageToken != "" {
		for i, u := range urns {
			if u == opts.PageToken {
				startIdx = i + 1
				break
			}
		}
	}

	var results []*core.Project
	for _, urn := range urns[startIdx:] {
		proj := p.projects[urn]
		if !opts.IncludeDeleted && proj.Deleted {
			continue
		}
		clone := *proj
		results = append(results, &clone)
		if len(results) >= pageSize {
			break
		}
	}

	nextToken := ""
	if len(results) == pageSize {
		nextToken = results[len(results)-1].URN.String()
	}
	if results == nil {
		results = []*core.Project{}
	}
	return &repo.ProjectListResult{Projects: results, NextPageToken: nextToken}, nil
}

func (p *Provider) UpdateProject(ctx context.Context, proj *core.Project) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	urn := proj.URN.String()
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.projects[urn]; !ok {
		return fmt.Errorf("%w: %s", repo.ErrNotFound, urn)
	}
	clone := *proj
	p.projects[urn] = &clone
	return nil
}

func (p *Provider) DeleteProject(ctx context.Context, urn string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	proj, ok := p.projects[urn]
	if !ok {
		return fmt.Errorf("%w: %s", repo.ErrNotFound, urn)
	}
	proj.Deleted = true
	return nil
}

func (p *Provider) CreateFolder(ctx context.Context, f *core.Folder) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	urn := f.URN.String()
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.folders[urn]; exists {
		return fmt.Errorf("%w: %s", repo.ErrAlreadyExists, urn)
	}
	clone := *f
	p.folders[urn] = &clone
	return nil
}

func (p *Provider) GetFolder(ctx context.Context, urn string) (*core.Folder, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	f, ok := p.folders[urn]
	if !ok {
		return nil, fmt.Errorf("%w: %s", repo.ErrNotFound, urn)
	}
	clone := *f
	return &clone, nil
}

func (p *Provider) ListFolders(ctx context.Context, opts repo.FolderListOptions) (*repo.FolderListResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()

	urns := make([]string, 0, len(p.folders))
	for urn := range p.folders {
		urns = append(urns, urn)
	}
	sort.Strings(urns)

	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}

	startIdx := 0
	if opts.PageToken != "" {
		for i, u := range urns {
			if u == opts.PageToken {
				startIdx = i + 1
				break
			}
		}
	}

	var results []*core.Folder
	for _, urn := range urns[startIdx:] {
		f := p.folders[urn]
		if !opts.IncludeDeleted && f.Deleted {
			continue
		}
		if opts.ProjectURN != "" && f.ProjectURN.String() != opts.ProjectURN {
			continue
		}
		clone := *f
		results = append(results, &clone)
		if len(results) >= pageSize {
			break
		}
	}

	nextToken := ""
	if len(results) == pageSize {
		nextToken = results[len(results)-1].URN.String()
	}
	if results == nil {
		results = []*core.Folder{}
	}
	return &repo.FolderListResult{Folders: results, NextPageToken: nextToken}, nil
}

func (p *Provider) UpdateFolder(ctx context.Context, f *core.Folder) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	urn := f.URN.String()
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.folders[urn]; !ok {
		return fmt.Errorf("%w: %s", repo.ErrNotFound, urn)
	}
	clone := *f
	p.folders[urn] = &clone
	return nil
}

func (p *Provider) DeleteFolder(ctx context.Context, urn string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	f, ok := p.folders[urn]
	if !ok {
		return fmt.Errorf("%w: %s", repo.ErrNotFound, urn)
	}
	f.Deleted = true
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// DeviceRepository
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) RegisterDevice(ctx context.Context, d *core.Device) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	urn := d.URN.String()
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.devices[urn]; exists {
		return fmt.Errorf("%w: %s", repo.ErrAlreadyExists, urn)
	}
	clone := *d
	p.devices[urn] = &clone
	return nil
}

func (p *Provider) GetDevice(ctx context.Context, urn string) (*core.Device, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	d, ok := p.devices[urn]
	if !ok {
		return nil, fmt.Errorf("%w: %s", repo.ErrNotFound, urn)
	}
	clone := *d
	return &clone, nil
}

func (p *Provider) ListDevices(ctx context.Context, opts repo.DeviceListOptions) (*repo.DeviceListResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()

	urns := make([]string, 0, len(p.devices))
	for urn := range p.devices {
		urns = append(urns, urn)
	}
	sort.Strings(urns)

	var results []*core.Device
	for _, urn := range urns {
		d := p.devices[urn]
		if !opts.IncludeRevoked && d.Revoked {
			continue
		}
		if opts.OwnerURN != "" && d.OwnerURN.String() != opts.OwnerURN {
			continue
		}
		clone := *d
		results = append(results, &clone)
	}
	if results == nil {
		results = []*core.Device{}
	}
	return &repo.DeviceListResult{Devices: results}, nil
}

func (p *Provider) UpdateDevice(ctx context.Context, d *core.Device) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	urn := d.URN.String()
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.devices[urn]; !ok {
		return fmt.Errorf("%w: %s", repo.ErrNotFound, urn)
	}
	clone := *d
	p.devices[urn] = &clone
	return nil
}

func (p *Provider) RevokeDevice(ctx context.Context, urn string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	d, ok := p.devices[urn]
	if !ok {
		return fmt.Errorf("%w: %s", repo.ErrNotFound, urn)
	}
	d.Revoked = true
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// UserRepository
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) CreateUser(ctx context.Context, u *core.User) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	urn := u.URN.String()
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.users[urn]; exists {
		return fmt.Errorf("%w: %s", repo.ErrAlreadyExists, urn)
	}
	clone := *u
	p.users[urn] = &clone
	return nil
}

func (p *Provider) GetUser(ctx context.Context, urn string) (*core.User, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	u, ok := p.users[urn]
	if !ok {
		return nil, fmt.Errorf("%w: %s", repo.ErrNotFound, urn)
	}
	clone := *u
	return &clone, nil
}

func (p *Provider) ListUsers(ctx context.Context, opts repo.UserListOptions) (*repo.UserListResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()

	urns := make([]string, 0, len(p.users))
	for urn := range p.users {
		urns = append(urns, urn)
	}
	sort.Strings(urns)

	// Pagination: use URN string as cursor.
	startIdx := 0
	if opts.PageToken != "" {
		for i, urn := range urns {
			if urn > opts.PageToken {
				startIdx = i
				break
			}
		}
	}

	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}

	var results []*core.User
	var nextToken string
	for i := startIdx; i < len(urns); i++ {
		u := p.users[urns[i]]
		if !opts.IncludeDeleted && u.Deleted {
			continue
		}
		if len(results) == pageSize {
			nextToken = urns[i]
			break
		}
		clone := *u
		results = append(results, &clone)
	}
	if results == nil {
		results = []*core.User{}
	}
	return &repo.UserListResult{Users: results, NextPageToken: nextToken}, nil
}

func (p *Provider) UpdateUser(ctx context.Context, u *core.User) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	urn := u.URN.String()
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.users[urn]; !ok {
		return fmt.Errorf("%w: %s", repo.ErrNotFound, urn)
	}
	clone := *u
	p.users[urn] = &clone
	return nil
}

func (p *Provider) DeleteUser(ctx context.Context, urn string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	u, ok := p.users[urn]
	if !ok {
		return fmt.Errorf("%w: %s", repo.ErrNotFound, urn)
	}
	u.Deleted = true
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Internal helpers
// ─────────────────────────────────────────────────────────────────────────────

// cloneHeader returns a shallow copy of the note's header fields with no
// event stream. Used when returning list results.
func cloneHeader(n *core.Note) *core.Note {
	clone := core.NewNote(n.URN, n.Name, n.CreatedAt)
	clone.NoteType = n.NoteType
	clone.Deleted = n.Deleted
	clone.UpdatedAt = n.UpdatedAt

	if n.ProjectURN != nil {
		urn := *n.ProjectURN
		clone.ProjectURN = &urn
	}
	if n.FolderURN != nil {
		urn := *n.FolderURN
		clone.FolderURN = &urn
	}
	if n.ParentURN != nil {
		urn := *n.ParentURN
		clone.ParentURN = &urn
	}
	for k, v := range n.NodeLinks {
		clone.NodeLinks[k] = v
	}
	return clone
}

// ─────────────────────────────────────────────────────────────────────────────
// ServerRepository
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) RegisterServer(_ context.Context, s *core.Server) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := s.URN.String()
	cp := *s
	p.servers[key] = &cp
	return nil
}

func (p *Provider) GetServer(_ context.Context, urn string) (*core.Server, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	sv, ok := p.servers[urn]
	if !ok {
		return nil, fmt.Errorf("%w: server %q", repo.ErrNotFound, urn)
	}
	cp := *sv
	return &cp, nil
}

func (p *Provider) ListServers(_ context.Context, opts repo.ServerListOptions) (*repo.ServerListResult, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var out []*core.Server
	for _, sv := range p.servers {
		if sv.Revoked && !opts.IncludeRevoked {
			continue
		}
		cp := *sv
		out = append(out, &cp)
	}
	return &repo.ServerListResult{Servers: out}, nil
}

func (p *Provider) UpdateServer(_ context.Context, s *core.Server) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := s.URN.String()
	if _, ok := p.servers[key]; !ok {
		return fmt.Errorf("%w: server %q", repo.ErrNotFound, key)
	}
	cp := *s
	p.servers[key] = &cp
	return nil
}

func (p *Provider) RevokeServer(_ context.Context, urn string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	sv, ok := p.servers[urn]
	if !ok {
		return fmt.Errorf("%w: server %q", repo.ErrNotFound, urn)
	}
	sv.Revoked = true
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// PairingSecretStore
// ─────────────────────────────────────────────────────────────────────────────

func (p *Provider) AddSecret(_ context.Context, s *repo.PairingSecret) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	cp := *s
	p.secrets[s.ID] = &cp
	return nil
}

func (p *Provider) ConsumeSecret(_ context.Context, plaintext string) (*repo.PairingSecret, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	_ = plaintext // memory store: accept any non-empty secret for test convenience
	for _, s := range p.secrets {
		if s.UsedAt != nil {
			continue
		}
		now := timeNow()
		if now.After(s.ExpiresAt) {
			continue
		}
		s.UsedAt = &now
		cp := *s
		return &cp, nil
	}
	return nil, fmt.Errorf("%w: no valid pairing secret", repo.ErrNotFound)
}

func (p *Provider) PruneExpired(_ context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := timeNow()
	for id, s := range p.secrets {
		if now.After(s.ExpiresAt) {
			delete(p.secrets, id)
		}
	}
	return nil
}

// timeNow is a package-level variable so tests can override it.
var timeNow = func() time.Time { return time.Now().UTC() }

func cloneNoteWithEvents(base *core.Note, evts []*core.Event) (*core.Note, error) {
	n := cloneHeader(base)
	for _, ev := range evts {
		if err := n.ApplyEvent(ev); err != nil {
			return nil, fmt.Errorf("memory provider: replay event seq %d: %w", ev.Sequence, err)
		}
	}
	return n, nil
}

// copyEvents returns a new slice containing the same event pointers.
// The in-memory provider treats events as immutable once written, so pointer
// sharing is safe here.
func copyEvents(evts []*core.Event) []*core.Event {
	if len(evts) == 0 {
		return []*core.Event{}
	}
	out := make([]*core.Event, len(evts))
	copy(out, evts)
	return out
}
