package sqlite_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zebaqui/notx-engine/core"
	"github.com/zebaqui/notx-engine/repo"
	"github.com/zebaqui/notx-engine/repo/sqlite"
)

// newTestProvider creates a Provider in a fresh temp directory and registers
// cleanup hooks to close and remove it.
func newTestProvider(t *testing.T) *sqlite.Provider {
	t.Helper()
	dir, err := os.MkdirTemp("", "sqlite-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	p, err := sqlite.New(dir, nil)
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	t.Cleanup(func() { p.Close() })
	return p
}

// noteURN builds a deterministic note URN for tests.
func noteURN(suffix string) core.URN {
	return core.MustParseURN("urn:notx:note:00000000-0000-4000-8000-" + suffix)
}

// projectURN builds a deterministic project URN for tests.
func projectURN(suffix string) core.URN {
	return core.MustParseURN("urn:notx:proj:00000000-0000-4000-8000-" + suffix)
}

// folderURN builds a deterministic folder URN for tests.
func folderURN(suffix string) core.URN {
	return core.MustParseURN("urn:notx:folder:00000000-0000-4000-8000-" + suffix)
}

// ─────────────────────────────────────────────────────────────────────────────
// TestNewProvider
// ─────────────────────────────────────────────────────────────────────────────

func TestNewProvider(t *testing.T) {
	dir, err := os.MkdirTemp("", "sqlite-test-new-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	p, err := sqlite.New(dir, nil)
	if err != nil {
		t.Fatalf("sqlite.New returned error: %v", err)
	}

	if err := p.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	// Verify the index file was created.
	indexPath := filepath.Join(dir, "index.db")
	if _, err := os.Stat(indexPath); err != nil {
		t.Fatalf("expected index.db to exist: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestNotesCRUD
// ─────────────────────────────────────────────────────────────────────────────

func TestNotesCRUD(t *testing.T) {
	p := newTestProvider(t)
	ctx := context.Background()

	urn := noteURN("000000000001")
	now := time.Now().UTC().Truncate(time.Millisecond)

	n := core.NewNote(urn, "Test Note", now)

	// Create
	if err := p.Create(ctx, n); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Get — verify fields round-trip
	got, err := p.Get(ctx, urn.String())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.URN.String() != urn.String() {
		t.Errorf("URN: got %q, want %q", got.URN.String(), urn.String())
	}
	if got.Name != "Test Note" {
		t.Errorf("Name: got %q, want %q", got.Name, "Test Note")
	}
	if got.Deleted {
		t.Error("expected Deleted=false")
	}
	if got.NoteType != core.NoteTypeNormal {
		t.Errorf("NoteType: got %q, want normal", got.NoteType)
	}

	// List without deleted — should include the note
	result, err := p.List(ctx, repo.ListOptions{PageSize: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(result.Notes) != 1 {
		t.Fatalf("List: expected 1 note, got %d", len(result.Notes))
	}

	// Soft-delete
	if err := p.Delete(ctx, urn.String()); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Get after delete — should still be retrievable
	got, err = p.Get(ctx, urn.String())
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if !got.Deleted {
		t.Error("expected Deleted=true after Delete()")
	}

	// List without deleted — should be empty
	result, err = p.List(ctx, repo.ListOptions{PageSize: 10})
	if err != nil {
		t.Fatalf("List (no deleted): %v", err)
	}
	if len(result.Notes) != 0 {
		t.Fatalf("List (no deleted): expected 0 notes, got %d", len(result.Notes))
	}

	// List with deleted — should include the note
	result, err = p.List(ctx, repo.ListOptions{PageSize: 10, IncludeDeleted: true})
	if err != nil {
		t.Fatalf("List (include deleted): %v", err)
	}
	if len(result.Notes) != 1 {
		t.Fatalf("List (include deleted): expected 1 note, got %d", len(result.Notes))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestListPagination
// ─────────────────────────────────────────────────────────────────────────────

func TestListPagination(t *testing.T) {
	p := newTestProvider(t)
	ctx := context.Background()

	suffixes := []string{
		"000000000001",
		"000000000002",
		"000000000003",
		"000000000004",
		"000000000005",
	}

	// Create 5 notes with slightly different timestamps so ordering is stable.
	base := time.Now().UTC().Truncate(time.Millisecond)
	for i, s := range suffixes {
		urn := noteURN(s)
		ts := base.Add(time.Duration(i) * time.Millisecond)
		n := core.NewNote(urn, "Note "+s, ts)
		if err := p.Create(ctx, n); err != nil {
			t.Fatalf("Create note %s: %v", s, err)
		}
	}

	// First page — pageSize=2
	page1, err := p.List(ctx, repo.ListOptions{PageSize: 2})
	if err != nil {
		t.Fatalf("List page1: %v", err)
	}
	if len(page1.Notes) != 2 {
		t.Fatalf("page1: expected 2 notes, got %d", len(page1.Notes))
	}
	if page1.NextPageToken == "" {
		t.Fatal("page1: expected non-empty NextPageToken")
	}

	// Second page
	page2, err := p.List(ctx, repo.ListOptions{PageSize: 2, PageToken: page1.NextPageToken})
	if err != nil {
		t.Fatalf("List page2: %v", err)
	}
	if len(page2.Notes) != 2 {
		t.Fatalf("page2: expected 2 notes, got %d", len(page2.Notes))
	}
	if page2.NextPageToken == "" {
		t.Fatal("page2: expected non-empty NextPageToken")
	}

	// Third page — the last one
	page3, err := p.List(ctx, repo.ListOptions{PageSize: 2, PageToken: page2.NextPageToken})
	if err != nil {
		t.Fatalf("List page3: %v", err)
	}
	if len(page3.Notes) != 1 {
		t.Fatalf("page3: expected 1 note, got %d", len(page3.Notes))
	}
	if page3.NextPageToken != "" {
		t.Fatalf("page3: expected empty NextPageToken, got %q", page3.NextPageToken)
	}

	// Verify no duplicates across pages.
	seen := make(map[string]bool)
	for _, page := range []*repo.ListResult{page1, page2, page3} {
		for _, note := range page.Notes {
			urn := note.URN.String()
			if seen[urn] {
				t.Errorf("duplicate note in pages: %s", urn)
			}
			seen[urn] = true
		}
	}
	if len(seen) != 5 {
		t.Fatalf("expected 5 unique notes across pages, got %d", len(seen))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestProjectCRUD
// ─────────────────────────────────────────────────────────────────────────────

func TestProjectCRUD(t *testing.T) {
	p := newTestProvider(t)
	ctx := context.Background()

	urn := projectURN("000000000001")
	now := time.Now().UTC().Truncate(time.Millisecond)

	proj := &core.Project{
		URN:       urn,
		Name:      "My Project",
		CreatedAt: now,
		UpdatedAt: now,
	}

	// Create
	if err := p.CreateProject(ctx, proj); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Get
	got, err := p.GetProject(ctx, urn.String())
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got.URN.String() != urn.String() {
		t.Errorf("URN: got %q, want %q", got.URN.String(), urn.String())
	}
	if got.Name != "My Project" {
		t.Errorf("Name: got %q, want %q", got.Name, "My Project")
	}
	if got.Deleted {
		t.Error("expected Deleted=false")
	}

	// List
	result, err := p.ListProjects(ctx, repo.ProjectListOptions{PageSize: 10})
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(result.Projects) != 1 {
		t.Fatalf("ListProjects: expected 1 project, got %d", len(result.Projects))
	}
	if result.Projects[0].Name != "My Project" {
		t.Errorf("ListProjects[0].Name: got %q, want %q", result.Projects[0].Name, "My Project")
	}

	// Update
	got.Name = "Renamed Project"
	got.UpdatedAt = time.Now().UTC()
	if err := p.UpdateProject(ctx, got); err != nil {
		t.Fatalf("UpdateProject: %v", err)
	}
	updated, err := p.GetProject(ctx, urn.String())
	if err != nil {
		t.Fatalf("GetProject after update: %v", err)
	}
	if updated.Name != "Renamed Project" {
		t.Errorf("after update Name: got %q, want %q", updated.Name, "Renamed Project")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestFolderCRUD
// ─────────────────────────────────────────────────────────────────────────────

func TestFolderCRUD(t *testing.T) {
	p := newTestProvider(t)
	ctx := context.Background()

	projURN := projectURN("000000000001")
	now := time.Now().UTC().Truncate(time.Millisecond)

	// Create the parent project first.
	proj := &core.Project{
		URN:       projURN,
		Name:      "Parent Project",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := p.CreateProject(ctx, proj); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	furn := folderURN("000000000001")
	f := &core.Folder{
		URN:        furn,
		ProjectURN: projURN,
		Name:       "My Folder",
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	// Create
	if err := p.CreateFolder(ctx, f); err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}

	// Get
	got, err := p.GetFolder(ctx, furn.String())
	if err != nil {
		t.Fatalf("GetFolder: %v", err)
	}
	if got.URN.String() != furn.String() {
		t.Errorf("URN: got %q, want %q", got.URN.String(), furn.String())
	}
	if got.Name != "My Folder" {
		t.Errorf("Name: got %q, want %q", got.Name, "My Folder")
	}
	if got.ProjectURN.String() != projURN.String() {
		t.Errorf("ProjectURN: got %q, want %q", got.ProjectURN.String(), projURN.String())
	}

	// List by project
	result, err := p.ListFolders(ctx, repo.FolderListOptions{
		ProjectURN: projURN.String(),
		PageSize:   10,
	})
	if err != nil {
		t.Fatalf("ListFolders: %v", err)
	}
	if len(result.Folders) != 1 {
		t.Fatalf("ListFolders: expected 1 folder, got %d", len(result.Folders))
	}
	if result.Folders[0].Name != "My Folder" {
		t.Errorf("ListFolders[0].Name: got %q, want %q", result.Folders[0].Name, "My Folder")
	}

	// List for a different project URN — expect empty.
	otherProjURN := projectURN("000000000099")
	result2, err := p.ListFolders(ctx, repo.FolderListOptions{
		ProjectURN: otherProjURN.String(),
		PageSize:   10,
	})
	if err != nil {
		t.Fatalf("ListFolders (other project): %v", err)
	}
	if len(result2.Folders) != 0 {
		t.Fatalf("ListFolders (other project): expected 0 folders, got %d", len(result2.Folders))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestRebuildOnMissingDB
// ─────────────────────────────────────────────────────────────────────────────

func TestRebuildOnMissingDB(t *testing.T) {
	dir, err := os.MkdirTemp("", "sqlite-test-rebuild-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	// Confirm index.db does NOT exist before calling New.
	indexPath := filepath.Join(dir, "index.db")
	if _, err := os.Stat(indexPath); !os.IsNotExist(err) {
		t.Fatal("expected index.db to not exist before New()")
	}

	p, err := sqlite.New(dir, nil)
	if err != nil {
		t.Fatalf("sqlite.New on missing DB: %v", err)
	}
	defer p.Close()

	// Confirm index.db was created.
	if _, err := os.Stat(indexPath); err != nil {
		t.Fatalf("expected index.db to exist after New(): %v", err)
	}

	// Confirm the provider is usable — create a note.
	ctx := context.Background()
	urn := noteURN("000000000001")
	n := core.NewNote(urn, "Rebuild Note", time.Now().UTC())
	if err := p.Create(ctx, n); err != nil {
		t.Fatalf("Create after rebuild: %v", err)
	}
	got, err := p.Get(ctx, urn.String())
	if err != nil {
		t.Fatalf("Get after rebuild: %v", err)
	}
	if got.Name != "Rebuild Note" {
		t.Errorf("Name: got %q, want %q", got.Name, "Rebuild Note")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestProjectionVersionRebuild — TODO
// ─────────────────────────────────────────────────────────────────────────────

func TestProjectionVersionRebuild(t *testing.T) {
	t.Skip("TODO: requires manipulating projection_meta to simulate a version bump")
}

// ─────────────────────────────────────────────────────────────────────────────
// TestEnsureIndex_IntegrityCheck — TODO
// ─────────────────────────────────────────────────────────────────────────────

func TestEnsureIndex_IntegrityCheck(t *testing.T) {
	t.Skip("TODO: requires corrupting the SQLite file to trigger integrity rebuild")
}
