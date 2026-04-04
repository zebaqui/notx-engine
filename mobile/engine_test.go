package mobile_test

import (
	"testing"

	"github.com/zebaqui/notx-engine/mobile"
)

func TestEngineNew(t *testing.T) {
	p := newStubPlatform(t)
	e, err := mobile.New(p)
	if err != nil {
		t.Fatalf("mobile.New: %v", err)
	}
	defer e.Close()
}

func TestEnsureDeviceURN(t *testing.T) {
	p := newStubPlatform(t)
	e, err := mobile.New(p)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()

	urn1, err := e.EnsureDeviceURN()
	if err != nil {
		t.Fatal(err)
	}
	if urn1 == "" {
		t.Fatal("expected non-empty URN")
	}

	// Second call must return same URN.
	urn2, err := e.EnsureDeviceURN()
	if err != nil {
		t.Fatal(err)
	}
	if urn1 != urn2 {
		t.Fatalf("URN changed between calls: %q → %q", urn1, urn2)
	}
}

func TestIsPaired_False(t *testing.T) {
	p := newStubPlatform(t)
	e, _ := mobile.New(p)
	defer e.Close()

	paired, err := e.IsPaired()
	if err != nil {
		t.Fatal(err)
	}
	if paired {
		t.Fatal("expected not paired")
	}
}

func TestOnPairingComplete(t *testing.T) {
	p := newStubPlatform(t)
	e, _ := mobile.New(p)
	defer e.Close()

	err := e.OnPairingComplete(
		"urn:notx:device:00000000-0000-4000-8000-000000000001",
		"localhost:9090",
		mobile.AliasDeviceKeyV1,
	)
	if err != nil {
		t.Fatal(err)
	}

	// IsPaired should be false: OnPairingComplete only stores config values,
	// but IsPaired also requires a cert (AliasDeviceCert) and an active key —
	// neither of which the stub has stored via this path.
	paired, err := e.IsPaired()
	if err != nil {
		t.Fatal(err)
	}
	if paired {
		t.Fatal("expected not paired (no cert/key in platform)")
	}
}

func TestCreateAndListNotes(t *testing.T) {
	p := newStubPlatform(t)
	e, _ := mobile.New(p)
	defer e.Close()

	urn, err := e.CreateNote("My Note", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if urn == "" {
		t.Fatal("expected non-empty note URN")
	}

	list, err := e.ListNotes(&mobile.ListOptions{PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("expected 1 note, got %d", len(list.Items))
	}
	if list.Items[0].Name != "My Note" {
		t.Fatalf("expected name %q, got %q", "My Note", list.Items[0].Name)
	}
}

func TestCreateNote_URNFormat(t *testing.T) {
	p := newStubPlatform(t)
	e, _ := mobile.New(p)
	defer e.Close()

	urn, err := e.CreateNote("Format Check", "", "")
	if err != nil {
		t.Fatal(err)
	}
	// URN should start with "urn:notx:note:"
	const prefix = "urn:notx:note:"
	if len(urn) <= len(prefix) || urn[:len(prefix)] != prefix {
		t.Fatalf("unexpected URN format: %q (want prefix %q)", urn, prefix)
	}
}

func TestGetNote(t *testing.T) {
	p := newStubPlatform(t)
	e, _ := mobile.New(p)
	defer e.Close()

	urn, err := e.CreateNote("Fetch Me", "", "")
	if err != nil {
		t.Fatal(err)
	}

	header, err := e.GetNote(urn)
	if err != nil {
		t.Fatalf("GetNote: %v", err)
	}
	if header.URN != urn {
		t.Errorf("URN: got %q, want %q", header.URN, urn)
	}
	if header.Name != "Fetch Me" {
		t.Errorf("Name: got %q, want %q", header.Name, "Fetch Me")
	}
	if header.Deleted {
		t.Error("expected Deleted=false")
	}
	if header.NoteType != "normal" {
		t.Errorf("NoteType: got %q, want %q", header.NoteType, "normal")
	}
}

func TestDeleteNote(t *testing.T) {
	p := newStubPlatform(t)
	e, _ := mobile.New(p)
	defer e.Close()

	urn, err := e.CreateNote("To Delete", "", "")
	if err != nil {
		t.Fatal(err)
	}

	if err := e.DeleteNote(urn); err != nil {
		t.Fatalf("DeleteNote: %v", err)
	}

	// After deletion, ListNotes (no deleted) should return empty.
	list, err := e.ListNotes(&mobile.ListOptions{PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 0 {
		t.Fatalf("expected 0 notes after delete, got %d", len(list.Items))
	}

	// The note must still be retrievable directly.
	header, err := e.GetNote(urn)
	if err != nil {
		t.Fatalf("GetNote after delete: %v", err)
	}
	if !header.Deleted {
		t.Error("expected Deleted=true after DeleteNote()")
	}
}

func TestCreateAndListProjects(t *testing.T) {
	p := newStubPlatform(t)
	e, _ := mobile.New(p)
	defer e.Close()

	urn, err := e.CreateProject("Alpha Project")
	if err != nil {
		t.Fatal(err)
	}
	if urn == "" {
		t.Fatal("expected non-empty project URN")
	}

	projects, err := e.ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(projects))
	}
	if projects[0].Name != "Alpha Project" {
		t.Fatalf("expected name %q, got %q", "Alpha Project", projects[0].Name)
	}
}

func TestCreateProject_URNFormat(t *testing.T) {
	p := newStubPlatform(t)
	e, _ := mobile.New(p)
	defer e.Close()

	urn, err := e.CreateProject("URN Test Project")
	if err != nil {
		t.Fatal(err)
	}
	const prefix = "urn:notx:proj:"
	if len(urn) <= len(prefix) || urn[:len(prefix)] != prefix {
		t.Fatalf("unexpected project URN format: %q (want prefix %q)", urn, prefix)
	}
}

func TestListNotes_Empty(t *testing.T) {
	p := newStubPlatform(t)
	e, _ := mobile.New(p)
	defer e.Close()

	list, err := e.ListNotes(&mobile.ListOptions{PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 0 {
		t.Fatalf("expected 0 notes on fresh engine, got %d", len(list.Items))
	}
	if list.NextPageToken != "" {
		t.Fatalf("expected empty NextPageToken, got %q", list.NextPageToken)
	}
}

func TestListProjects_Empty(t *testing.T) {
	p := newStubPlatform(t)
	e, _ := mobile.New(p)
	defer e.Close()

	projects, err := e.ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 0 {
		t.Fatalf("expected 0 projects on fresh engine, got %d", len(projects))
	}
}

func TestListNotes_NilOptions(t *testing.T) {
	p := newStubPlatform(t)
	e, _ := mobile.New(p)
	defer e.Close()

	// Passing nil opts should not panic.
	list, err := e.ListNotes(nil)
	if err != nil {
		t.Fatal(err)
	}
	if list == nil {
		t.Fatal("expected non-nil NoteList")
	}
}

func TestDataDir(t *testing.T) {
	p := newStubPlatform(t)
	e, _ := mobile.New(p)
	defer e.Close()

	dir, err := e.DataDir()
	if err != nil {
		t.Fatal(err)
	}
	if dir == "" {
		t.Fatal("expected non-empty DataDir")
	}

	// Must match what the stub reports.
	stubDir, _ := p.DataDir()
	if dir != stubDir {
		t.Errorf("DataDir mismatch: engine=%q, stub=%q", dir, stubDir)
	}
}

func TestNotesDir(t *testing.T) {
	p := newStubPlatform(t)
	e, _ := mobile.New(p)
	defer e.Close()

	dir, err := e.NotesDir()
	if err != nil {
		t.Fatal(err)
	}
	if dir == "" {
		t.Fatal("expected non-empty NotesDir")
	}

	// NotesDir must be a subdirectory of DataDir.
	dataDir, _ := e.DataDir()
	const suffix = "/notes"
	want := dataDir + suffix
	if dir != want {
		t.Errorf("NotesDir: got %q, want %q", dir, want)
	}
}
