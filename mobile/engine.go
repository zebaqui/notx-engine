// Package mobile is the gomobile-exported package for the notx engine.
// This file contains the top-level Engine struct and its exported methods.
//
// gomobile bind limitations that shape this API:
//   - No map types in exported signatures
//   - No multiple return values beyond (T, error)
//   - Interfaces passed from Swift must be declared in this package
//   - All slice element types must be pointer types
package mobile

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/zebaqui/notx-engine/core"
	"github.com/zebaqui/notx-engine/repo"
	"github.com/zebaqui/notx-engine/repo/sqlite"
)

// RenewalThreshold is the minimum remaining cert validity before the engine
// triggers an automatic renewal.
const RenewalThreshold = 7 * 24 * time.Hour // 7 days

// Engine is the top-level coordinator exposed to Swift via gomobile bind.
// Create one instance per app session and hold it alive for the app lifetime.
//
// All exported methods are safe to call from multiple goroutines.
type Engine struct {
	platform Platform
	provider *sqlite.Provider
	mu       sync.RWMutex
}

// New creates and returns a new Engine using the provided Platform implementation.
// It opens the SQLite index, runs the EnsureIndex startup sequence, and returns
// a ready-to-use engine.
//
// On iOS, call this from NotxEngineHost.init(appGroupID:) after constructing
// iOSPlatform.
func New(p Platform) (*Engine, error) {
	dataDir, err := p.DataDir()
	if err != nil {
		return nil, fmt.Errorf("mobile: platform.DataDir: %w", err)
	}

	provider, err := sqlite.New(dataDir, nil)
	if err != nil {
		return nil, fmt.Errorf("mobile: open sqlite provider: %w", err)
	}

	return &Engine{
		platform: p,
		provider: provider,
	}, nil
}

// Close releases all resources held by the engine.
func (e *Engine) Close() error {
	return e.provider.Close()
}

// ─────────────────────────────────────────────────────────────────────────────
// Device identity
// ─────────────────────────────────────────────────────────────────────────────

// EnsureDeviceURN returns the device's own URN, creating and persisting a new
// one if none exists yet. The URN format is "urn:notx:device:<uuidv7>".
//
// This is called by the Swift PairingCoordinator before the pairing RPC.
func (e *Engine) EnsureDeviceURN() (string, error) {
	existing, err := e.platform.GetConfig(ConfigKeyDeviceURN)
	if err != nil {
		return "", fmt.Errorf("mobile: read device URN: %w", err)
	}
	if existing != "" {
		return existing, nil
	}
	urn := core.NewURN(core.ObjectTypeDevice).String()
	if err := e.platform.SetConfig(ConfigKeyDeviceURN, urn); err != nil {
		return "", fmt.Errorf("mobile: persist device URN: %w", err)
	}
	return urn, nil
}

// IsPaired reports whether the device has completed the pairing flow.
// Returns true if a device cert and active key alias are present.
func (e *Engine) IsPaired() (bool, error) {
	hasCert, err := e.platform.HasCert(AliasDeviceCert)
	if err != nil {
		return false, err
	}
	if !hasCert {
		return false, nil
	}
	alias, err := e.platform.GetConfig(ConfigKeyActiveKeyAlias)
	if err != nil {
		return false, err
	}
	if alias == "" {
		return false, nil
	}
	return e.platform.HasKey(alias)
}

// OnPairingComplete is called by Swift after the RegisterServer RPC succeeds.
// It stores the device URN, authority address, and active key alias in config.
// This is the ONLY method involved in pairing on the Go side — no token,
// no CSR, no cert handling. Those all live in Swift.
func (e *Engine) OnPairingComplete(deviceURN, authorityAddr, activeKeyAlias string) error {
	if err := e.platform.SetConfig(ConfigKeyDeviceURN, deviceURN); err != nil {
		return fmt.Errorf("mobile: set device URN: %w", err)
	}
	if err := e.platform.SetConfig(ConfigKeyAuthorityAddr, authorityAddr); err != nil {
		return fmt.Errorf("mobile: set authority addr: %w", err)
	}
	if err := e.platform.SetConfig(ConfigKeyActiveKeyAlias, activeKeyAlias); err != nil {
		return fmt.Errorf("mobile: set active key alias: %w", err)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Cert renewal
// ─────────────────────────────────────────────────────────────────────────────

// RenewIfNeeded checks the current cert expiry and performs a renewal if the
// remaining validity is below RenewalThreshold.
//
// Renewal flow (Go-orchestrated, Swift-signed):
//  1. Read active key alias and current cert
//  2. Parse cert expiry — if > threshold, return nil (nothing to do)
//  3. Allocate next version alias
//  4. Platform.GenerateKey(newAlias) — Swift creates key in Secure Enclave
//  5. Platform.BuildCSR(newAlias, deviceURN) — Swift builds + signs CSR
//  6. Dial authority with current cert, call RenewCertificate RPC
//  7. Validate new cert; on failure delete newAlias key and return error
//  8. Platform.StoreCert(AliasDeviceCert, newCert) — overwrites cert
//  9. Platform.SetConfig(ConfigKeyActiveKeyAlias, newAlias) — promote
//
// 10. Platform.DeleteKey(activeAlias) — delete old key after promotion
//
// If the gRPC dial/RPC is not yet implemented, this method returns nil for
// certs with remaining validity > threshold and ErrRenewal otherwise.
// Full gRPC implementation is Phase M5.
func (e *Engine) RenewIfNeeded(ctx context.Context) error {
	activeAlias, err := e.platform.GetConfig(ConfigKeyActiveKeyAlias)
	if err != nil {
		return fmt.Errorf("mobile: RenewIfNeeded: read active alias: %w", err)
	}
	if activeAlias == "" {
		return ErrNotPaired
	}

	certPEM, err := e.platform.LoadCert(AliasDeviceCert)
	if err != nil {
		return fmt.Errorf("mobile: RenewIfNeeded: load cert: %w", err)
	}

	expiry, err := parseCertExpiry(certPEM)
	if err != nil {
		return fmt.Errorf("mobile: RenewIfNeeded: parse cert: %w", err)
	}

	if time.Until(expiry) > RenewalThreshold {
		return nil // nothing to do
	}

	newAlias := NextVersionAlias(activeAlias)

	// Step 4: generate new key in Secure Enclave.
	if _, err := e.platform.GenerateKey(newAlias); err != nil {
		return fmt.Errorf("mobile: RenewIfNeeded: GenerateKey(%q): %w", newAlias, err)
	}

	// Step 5: build CSR in Swift using the new SE key.
	deviceURN, err := e.platform.GetConfig(ConfigKeyDeviceURN)
	if err != nil {
		_ = e.platform.DeleteKey(newAlias)
		return fmt.Errorf("mobile: RenewIfNeeded: read device URN: %w", err)
	}

	_, err = e.platform.BuildCSR(newAlias, deviceURN)
	if err != nil {
		_ = e.platform.DeleteKey(newAlias)
		return fmt.Errorf("mobile: RenewIfNeeded: BuildCSR: %w", err)
	}

	// Steps 6–7: gRPC RenewCertificate RPC — Phase M5.
	// Placeholder: return ErrRenewal to signal that the RPC is not yet wired.
	// In Phase M5 this will dial the authority, call the RPC, validate the
	// returned cert, and continue to steps 8–10.
	_ = e.platform.DeleteKey(newAlias) // clean up the key we won't use yet
	return fmt.Errorf("%w: gRPC renewal RPC not yet implemented (Phase M5)", ErrRenewal)
}

// parseCertExpiry decodes a PEM-encoded certificate and returns its NotAfter time.
func parseCertExpiry(certPEM []byte) (time.Time, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return time.Time{}, fmt.Errorf("mobile: invalid PEM certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}, fmt.Errorf("mobile: parse certificate: %w", err)
	}
	return cert.NotAfter, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Note operations
// ─────────────────────────────────────────────────────────────────────────────

// CreateNote creates a new note and returns its URN.
func (e *Engine) CreateNote(name, projectURN, folderURN string) (string, error) {
	ctx := context.Background()
	urn := core.NewURN(core.ObjectTypeNote)
	now := time.Now().UTC()

	n := &core.Note{
		URN:       urn,
		Name:      name,
		NoteType:  core.NoteTypeNormal,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if projectURN != "" {
		u, err := core.ParseURN(projectURN)
		if err != nil {
			return "", fmt.Errorf("mobile: CreateNote: invalid projectURN: %w", err)
		}
		n.ProjectURN = &u
	}
	if folderURN != "" {
		u, err := core.ParseURN(folderURN)
		if err != nil {
			return "", fmt.Errorf("mobile: CreateNote: invalid folderURN: %w", err)
		}
		n.FolderURN = &u
	}

	if err := e.provider.Create(ctx, n); err != nil {
		return "", fmt.Errorf("mobile: CreateNote: %w", err)
	}
	return urn.String(), nil
}

// GetNote returns the note header for the given URN.
func (e *Engine) GetNote(urn string) (*NoteHeader, error) {
	ctx := context.Background()
	n, err := e.provider.Get(ctx, urn)
	if err != nil {
		return nil, fmt.Errorf("mobile: GetNote: %w", err)
	}
	return noteToHeader(n), nil
}

// ListNotes returns a page of note headers.
func (e *Engine) ListNotes(opts *ListOptions) (*NoteList, error) {
	if opts == nil {
		opts = &ListOptions{}
	}
	ctx := context.Background()
	repoOpts := repo.ListOptions{
		ProjectURN:     opts.ProjectURN,
		FolderURN:      opts.FolderURN,
		IncludeDeleted: opts.IncludeDeleted,
		PageSize:       opts.PageSize,
		PageToken:      opts.PageToken,
	}
	if opts.NoteType != "" {
		nt, err := core.ParseNoteType(opts.NoteType)
		if err != nil {
			return nil, fmt.Errorf("mobile: ListNotes: invalid NoteType: %w", err)
		}
		repoOpts.FilterByType = true
		repoOpts.NoteTypeFilter = nt
	}

	result, err := e.provider.List(ctx, repoOpts)
	if err != nil {
		return nil, fmt.Errorf("mobile: ListNotes: %w", err)
	}

	headers := make([]*NoteHeader, len(result.Notes))
	for i, n := range result.Notes {
		headers[i] = noteToHeader(n)
	}
	return &NoteList{items: headers, NextPageToken: result.NextPageToken}, nil
}

// DeleteNote soft-deletes the note with the given URN.
func (e *Engine) DeleteNote(urn string) error {
	ctx := context.Background()
	if err := e.provider.Delete(ctx, urn); err != nil {
		return fmt.Errorf("mobile: DeleteNote: %w", err)
	}
	return nil
}

// SearchNotes performs full-text search over normal note content.
func (e *Engine) SearchNotes(opts *SearchOptions) (*SearchResults, error) {
	if opts == nil || opts.Query == "" {
		return &SearchResults{}, nil
	}
	ctx := context.Background()
	repoOpts := repo.SearchOptions{
		Query:     opts.Query,
		PageSize:  opts.PageSize,
		PageToken: opts.PageToken,
	}
	result, err := e.provider.Search(ctx, repoOpts)
	if err != nil {
		return nil, fmt.Errorf("mobile: SearchNotes: %w", err)
	}
	results := make([]*SearchResult, len(result.Results))
	for i, r := range result.Results {
		results[i] = &SearchResult{
			Note:    noteToHeader(r.Note),
			Excerpt: r.Excerpt,
		}
	}
	return &SearchResults{items: results, NextPageToken: result.NextPageToken}, nil
}

// DataDir returns the engine's data directory path as resolved by the Platform.
// Useful for debugging and for applying iOS file protection attributes in Swift.
func (e *Engine) DataDir() (string, error) {
	return e.platform.DataDir()
}

// NotesDir returns the path to the notes subdirectory where .notx files are stored.
func (e *Engine) NotesDir() (string, error) {
	dataDir, err := e.platform.DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dataDir, "notes"), nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Project operations
// ─────────────────────────────────────────────────────────────────────────────

// CreateProject creates a new project and returns its URN.
func (e *Engine) CreateProject(name string) (string, error) {
	ctx := context.Background()
	urn := core.NewURN(core.ObjectTypeProject)
	now := time.Now().UTC()
	p := &core.Project{
		URN:       urn,
		Name:      name,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := e.provider.CreateProject(ctx, p); err != nil {
		return "", fmt.Errorf("mobile: CreateProject: %w", err)
	}
	return urn.String(), nil
}

// ListProjects returns all non-deleted projects.
func (e *Engine) ListProjects() (*ProjectList, error) {
	ctx := context.Background()
	result, err := e.provider.ListProjects(ctx, repo.ProjectListOptions{PageSize: 1000})
	if err != nil {
		return nil, fmt.Errorf("mobile: ListProjects: %w", err)
	}
	headers := make([]*ProjectHeader, len(result.Projects))
	for i, p := range result.Projects {
		headers[i] = &ProjectHeader{
			URN:         p.URN.String(),
			Name:        p.Name,
			Deleted:     p.Deleted,
			CreatedAtMs: p.CreatedAt.UnixMilli(),
			UpdatedAtMs: p.UpdatedAt.UnixMilli(),
		}
	}
	return &ProjectList{items: headers}, nil
}

// ─── Folder operations ───────────────────────────────────────────────────────

// CreateFolder creates a new folder inside a project and returns its URN.
func (e *Engine) CreateFolder(name, projectURN string) (string, error) {
	ctx := context.Background()
	projURN, err := core.ParseURN(projectURN)
	if err != nil {
		return "", fmt.Errorf("mobile: CreateFolder: invalid projectURN: %w", err)
	}
	urn := core.NewURN(core.ObjectTypeFolder)
	now := time.Now().UTC()
	f := &core.Folder{
		URN:        urn,
		ProjectURN: projURN,
		Name:       name,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := e.provider.CreateFolder(ctx, f); err != nil {
		return "", fmt.Errorf("mobile: CreateFolder: %w", err)
	}
	return urn.String(), nil
}

// ListFolders returns all non-deleted folders for the given project.
func (e *Engine) ListFolders(projectURN string) (*FolderList, error) {
	ctx := context.Background()
	result, err := e.provider.ListFolders(ctx, repo.FolderListOptions{
		ProjectURN: projectURN,
		PageSize:   1000,
	})
	if err != nil {
		return nil, fmt.Errorf("mobile: ListFolders: %w", err)
	}
	headers := make([]*FolderHeader, len(result.Folders))
	for i, f := range result.Folders {
		headers[i] = &FolderHeader{
			URN:         f.URN.String(),
			ProjectURN:  f.ProjectURN.String(),
			Name:        f.Name,
			Deleted:     f.Deleted,
			CreatedAtMs: f.CreatedAt.UnixMilli(),
			UpdatedAtMs: f.UpdatedAt.UnixMilli(),
		}
	}
	return &FolderList{items: headers}, nil
}

// DeleteFolder soft-deletes the folder with the given URN.
func (e *Engine) DeleteFolder(urn string) error {
	ctx := context.Background()
	if err := e.provider.DeleteFolder(ctx, urn); err != nil {
		return fmt.Errorf("mobile: DeleteFolder: %w", err)
	}
	return nil
}

// ─── Content operations ──────────────────────────────────────────────────────

// AppendNoteContent appends a single event to the note that sets the full
// document content. Each line of content becomes a LineOpSet entry.
func (e *Engine) AppendNoteContent(noteURN, content string) error {
	ctx := context.Background()

	note, err := e.provider.Get(ctx, noteURN)
	if err != nil {
		return fmt.Errorf("mobile: AppendNoteContent: get note: %w", err)
	}

	parsedNoteURN, err := core.ParseURN(noteURN)
	if err != nil {
		return fmt.Errorf("mobile: AppendNoteContent: invalid noteURN: %w", err)
	}

	lines := strings.Split(content, "\n")
	entries := make([]core.LineEntry, len(lines))
	for i, line := range lines {
		entries[i] = core.LineEntry{
			LineNumber: i + 1,
			Op:         core.LineOpSet,
			Content:    line,
		}
	}

	event := core.Event{
		NoteURN:   parsedNoteURN,
		Sequence:  note.HeadSequence() + 1,
		AuthorURN: core.AnonURN(),
		CreatedAt: time.Now().UTC(),
		Entries:   entries,
	}

	if err := e.provider.AppendEvent(ctx, &event, repo.AppendEventOptions{}); err != nil {
		return fmt.Errorf("mobile: AppendNoteContent: %w", err)
	}
	return nil
}

// GetNoteContent returns the materialised plain-text content of the note.
func (e *Engine) GetNoteContent(noteURN string) (string, error) {
	ctx := context.Background()
	note, err := e.provider.Get(ctx, noteURN)
	if err != nil {
		return "", fmt.Errorf("mobile: GetNoteContent: %w", err)
	}
	return note.Content(), nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

func noteToHeader(n *core.Note) *NoteHeader {
	h := &NoteHeader{
		URN:          n.URN.String(),
		Name:         n.Name,
		NoteType:     n.NoteType.String(),
		Deleted:      n.Deleted,
		CreatedAtMs:  n.CreatedAt.UnixMilli(),
		UpdatedAtMs:  n.UpdatedAt.UnixMilli(),
		HeadSequence: n.HeadSequence(),
	}
	if n.ProjectURN != nil {
		h.ProjectURN = n.ProjectURN.String()
	}
	if n.FolderURN != nil {
		h.FolderURN = n.FolderURN.String()
	}
	return h
}
