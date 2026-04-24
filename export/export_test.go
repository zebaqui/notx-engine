package export

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zebaqui/notx-engine/core"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

// testWriteTarEntry writes a single regular-file entry into tw.
func testWriteTarEntry(tw *tar.Writer, name string, data []byte) error {
	hdr := &tar.Header{
		Name:     name,
		Typeflag: tar.TypeReg,
		Mode:     0644,
		Size:     int64(len(data)),
		ModTime:  time.Now().UTC(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

// buildArchive creates an in-memory gzip+tar archive from the provided entries
// (name → content). The first entry is always manifest.json if not supplied by
// the caller, so the helpers also work for the path-traversal test where we
// want full control over every entry.
func buildRawArchive(entries map[string][]byte) ([]byte, error) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	for name, data := range entries {
		if err := testWriteTarEntry(tw, name, data); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// readArchiveEntries decompresses a gzip+tar byte slice and returns the set of
// entry names it contains.
func readArchiveEntries(data []byte) ([]string, error) {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gr.Close()
	tr := tar.NewReader(gr)

	var names []string
	for {
		hdr, err := tr.Next()
		if errors.Is(err, errEOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		names = append(names, hdr.Name)
	}
	return names, nil
}

// io.EOF alias so we can reference it without importing "io" at the top level
// (we still need it for io.EOF comparison).
var errEOF = func() error {
	// Build a tiny archive and exhaust it to obtain io.EOF from the tar reader.
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	_ = tw.Close()
	_ = gw.Close()
	gr, _ := gzip.NewReader(&buf)
	tr := tar.NewReader(gr)
	_, err := tr.Next()
	return err
}()

// ─── TestPack_EmptyList ───────────────────────────────────────────────────────

func TestPack_EmptyList(t *testing.T) {
	var buf bytes.Buffer
	if err := Pack(nil, &buf); err != nil {
		t.Fatalf("Pack(nil): unexpected error: %v", err)
	}

	// The archive must be non-empty (gzip magic bytes at minimum).
	if buf.Len() == 0 {
		t.Fatal("Pack produced an empty buffer")
	}

	// Must be a valid gzip stream.
	gr, err := gzip.NewReader(&buf)
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	defer gr.Close()

	// Must contain a manifest.json entry.
	tr := tar.NewReader(gr)
	hdr, err := tr.Next()
	if err != nil {
		t.Fatalf("reading first tar entry: %v", err)
	}
	if hdr.Name != "manifest.json" {
		t.Errorf("first entry: got %q, want %q", hdr.Name, "manifest.json")
	}

	// NoteCount in manifest must be zero.
	manifestData := make([]byte, hdr.Size)
	if _, err := tr.Read(manifestData); err != nil && err.Error() != "EOF" {
		t.Fatalf("reading manifest data: %v", err)
	}
	if !strings.Contains(string(manifestData), `"note_count": 0`) {
		t.Errorf("manifest does not contain note_count:0; got:\n%s", manifestData)
	}
}

// ─── TestPackAndUnpack_RoundTrip ──────────────────────────────────────────────

func TestPackAndUnpack_RoundTrip(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	// Create a single .notx file with known content.
	const wantContent = `{"urn":"urn:notx:note:018e4f2a-9b1c-7d3e-8f2a-1b3c4d5e6f7a","name":"Round-trip note"}`
	notxPath := filepath.Join(srcDir, "round-trip.notx")
	if err := os.WriteFile(notxPath, []byte(wantContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Pack the file into an in-memory buffer.
	var buf bytes.Buffer
	if err := Pack([]string{notxPath}, &buf); err != nil {
		t.Fatalf("Pack: %v", err)
	}

	// Unpack into dstDir.
	manifest, written, err := Unpack(&buf, dstDir)
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}

	// Exactly one file must have been written.
	if len(written) != 1 {
		t.Fatalf("Unpack: expected 1 written file, got %d: %v", len(written), written)
	}

	// The manifest NoteCount must match.
	if manifest.NoteCount != 1 {
		t.Errorf("manifest.NoteCount = %d, want 1", manifest.NoteCount)
	}

	// Content of the unpacked file must match the original.
	got, err := os.ReadFile(written[0])
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", written[0], err)
	}
	if string(got) != wantContent {
		t.Errorf("file content mismatch:\n  got  %q\n  want %q", got, wantContent)
	}

	// The written file name must be the basename of the original.
	if filepath.Base(written[0]) != "round-trip.notx" {
		t.Errorf("unexpected written filename: %q", filepath.Base(written[0]))
	}
}

// ─── TestPackDir ──────────────────────────────────────────────────────────────

func TestPackDir(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	// Two .notx files that should be included.
	notxFiles := []string{"alpha.notx", "beta.notx"}
	for _, name := range notxFiles {
		content := `{"name":"` + name + `"}`
		if err := os.WriteFile(filepath.Join(srcDir, name), []byte(content), 0644); err != nil {
			t.Fatalf("WriteFile(%q): %v", name, err)
		}
	}

	// One .txt file that must be excluded.
	if err := os.WriteFile(filepath.Join(srcDir, "readme.txt"), []byte("ignore me"), 0644); err != nil {
		t.Fatalf("WriteFile(readme.txt): %v", err)
	}

	// Pack the directory.
	var buf bytes.Buffer
	if err := PackDir(srcDir, &buf); err != nil {
		t.Fatalf("PackDir: %v", err)
	}

	// Unpack and verify.
	manifest, written, err := Unpack(&buf, dstDir)
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}

	if manifest.NoteCount != 2 {
		t.Errorf("manifest.NoteCount = %d, want 2", manifest.NoteCount)
	}
	if len(written) != 2 {
		t.Fatalf("expected 2 written files, got %d: %v", len(written), written)
	}

	// The .txt file must not be present in the destination.
	if _, err := os.Stat(filepath.Join(dstDir, "readme.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Error("readme.txt should not have been unpacked but it was found in dstDir")
	}

	// Both .notx files must be present.
	writtenNames := make(map[string]bool, len(written))
	for _, p := range written {
		writtenNames[filepath.Base(p)] = true
	}
	for _, name := range notxFiles {
		if !writtenNames[name] {
			t.Errorf("%q was not unpacked", name)
		}
	}
}

// ─── TestUnpack_ManifestFields ────────────────────────────────────────────────

func TestUnpack_ManifestFields(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	// Create two .notx files.
	for _, name := range []string{"note1.notx", "note2.notx"} {
		if err := os.WriteFile(filepath.Join(srcDir, name), []byte(`{}`), 0644); err != nil {
			t.Fatalf("WriteFile(%q): %v", name, err)
		}
	}

	var buf bytes.Buffer
	if err := PackDir(srcDir, &buf); err != nil {
		t.Fatalf("PackDir: %v", err)
	}

	manifest, _, err := Unpack(&buf, dstDir)
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}

	if manifest.NoteCount != 2 {
		t.Errorf("NoteCount = %d, want 2", manifest.NoteCount)
	}
	if manifest.ExportedAt.IsZero() {
		t.Error("ExportedAt is zero — expected a non-zero timestamp")
	}
	if manifest.Version == "" {
		t.Error("Version is empty")
	}
}

// ─── TestUnpack_PathTraversal ─────────────────────────────────────────────────

func TestUnpack_PathTraversal(t *testing.T) {
	// outerDir is the parent of destDir; a successful traversal attack would
	// write files here.
	outerDir := t.TempDir()
	destDir := filepath.Join(outerDir, "dest")
	if err := os.MkdirAll(destDir, 0755); err != nil {
		t.Fatalf("MkdirAll destDir: %v", err)
	}

	// Build a malicious archive manually.
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	// Valid manifest entry.
	manifestJSON := []byte(`{"version":"1","exported_at":"2025-01-01T00:00:00Z","note_count":0,"engine_version":"1"}`)
	if err := testWriteTarEntry(tw, "manifest.json", manifestJSON); err != nil {
		t.Fatalf("writing manifest tar entry: %v", err)
	}

	// Malicious entry without a notes/ prefix — should be silently skipped.
	if err := testWriteTarEntry(tw, "../escape.txt", []byte("i must not appear outside destDir")); err != nil {
		t.Fatalf("writing malicious tar entry: %v", err)
	}

	// Malicious entry with notes/ prefix but path-traversal in the name.
	// filepath.Base strips the traversal, so it lands inside destDir (safe).
	// We verify the traversal component does NOT cause the file to land outside.
	if err := testWriteTarEntry(tw, "notes/../../traversal.notx", []byte("content")); err != nil {
		t.Fatalf("writing notes-prefixed traversal entry: %v", err)
	}

	if err := tw.Close(); err != nil {
		t.Fatalf("tar.Close: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip.Close: %v", err)
	}

	_, _, err := Unpack(&buf, destDir)
	if err != nil {
		t.Fatalf("Unpack returned unexpected error: %v", err)
	}

	// The ../escape.txt entry must NOT have been written next to destDir.
	escapedPath := filepath.Join(outerDir, "escape.txt")
	if _, err := os.Stat(escapedPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("path traversal succeeded: file found at %s", escapedPath)
	}

	// The notes/../../traversal.notx entry must NOT have escaped to outerDir.
	outerTraversal := filepath.Join(outerDir, "traversal.notx")
	if _, err := os.Stat(outerTraversal); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("notes/ traversal succeeded: file found at %s", outerTraversal)
	}
}

// ─── TestPackNotes ────────────────────────────────────────────────────────────

func TestPackNotes(t *testing.T) {
	dstDir := t.TempDir()

	// Create minimal core.Note values using the public constructor.
	now := time.Now().UTC()

	note1 := core.NewNote(core.NewURN(core.ObjectTypeNote), "First Note", now)
	note2 := core.NewNote(core.NewURN(core.ObjectTypeNote), "Second Note", now)

	var buf bytes.Buffer
	if err := PackNotes([]*core.Note{note1, note2}, &buf); err != nil {
		t.Fatalf("PackNotes: %v", err)
	}

	if buf.Len() == 0 {
		t.Fatal("PackNotes produced an empty buffer")
	}

	// Unpack into dstDir and inspect results.
	manifest, written, err := Unpack(&buf, dstDir)
	if err != nil {
		t.Fatalf("Unpack after PackNotes: %v", err)
	}

	if manifest.NoteCount != 2 {
		t.Errorf("manifest.NoteCount = %d, want 2", manifest.NoteCount)
	}
	if manifest.ExportedAt.IsZero() {
		t.Error("ExportedAt is zero")
	}

	if len(written) != 2 {
		t.Fatalf("expected 2 written files, got %d: %v", len(written), written)
	}

	// Each written file must be a .notx file inside dstDir.
	for _, path := range written {
		if filepath.Dir(path) != dstDir {
			t.Errorf("written file %q is not inside dstDir %q", path, dstDir)
		}
		if filepath.Ext(path) != ".notx" {
			t.Errorf("written file %q does not have .notx extension", path)
		}

		// Each .notx file must be valid JSON that contains a "urn" field.
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%q): %v", path, err)
		}
		if !strings.Contains(string(data), `"urn"`) {
			t.Errorf("unpacked file %q does not contain a \"urn\" field; content:\n%s", path, data)
		}
	}
}
