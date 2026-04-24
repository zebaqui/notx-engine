package export

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zebaqui/notx-engine/core"
)

// engineVersion is embedded in the manifest to identify the producer.
const engineVersion = "1"

// Manifest is the archive-level metadata written to manifest.json.
type Manifest struct {
	Version       string    `json:"version"`
	ExportedAt    time.Time `json:"exported_at"`
	NoteCount     int       `json:"note_count"`
	EngineVersion string    `json:"engine_version,omitempty"`
}

// writeTarEntry writes a single regular file entry into tw with the given name
// and byte content.
func writeTarEntry(tw *tar.Writer, name string, data []byte) error {
	hdr := &tar.Header{
		Name:     name,
		Typeflag: tar.TypeReg,
		Mode:     0644,
		Size:     int64(len(data)),
		ModTime:  time.Now().UTC(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("export: write tar header %q: %w", name, err)
	}
	if _, err := tw.Write(data); err != nil {
		return fmt.Errorf("export: write tar body %q: %w", name, err)
	}
	return nil
}

// writeManifest serialises m and writes it as the first tar entry.
func writeManifest(tw *tar.Writer, m Manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("export: marshal manifest: %w", err)
	}
	return writeTarEntry(tw, "manifest.json", data)
}

// sanitiseURN converts a URN string such as "urn:notx:note:<uuid>" into a
// filesystem-safe name by replacing colons with underscores.
func sanitiseURN(urn string) string {
	return strings.ReplaceAll(urn, ":", "_")
}

// Pack writes a .gnotx archive to dest containing the given .notx file paths.
// filePaths is a list of absolute (or relative) paths to .notx files on disk.
// The archive will contain manifest.json and notes/<basename>.notx for each file.
func Pack(filePaths []string, dest io.Writer) error {
	gw := gzip.NewWriter(dest)
	tw := tar.NewWriter(gw)

	m := Manifest{
		Version:       "1",
		ExportedAt:    time.Now().UTC(),
		NoteCount:     len(filePaths),
		EngineVersion: engineVersion,
	}
	if err := writeManifest(tw, m); err != nil {
		return err
	}

	for _, fp := range filePaths {
		data, err := os.ReadFile(fp)
		if err != nil {
			return fmt.Errorf("export: read file %q: %w", fp, err)
		}
		entryName := "notes/" + filepath.Base(fp)
		if err := writeTarEntry(tw, entryName, data); err != nil {
			return err
		}
	}

	if err := tw.Close(); err != nil {
		return fmt.Errorf("export: close tar writer: %w", err)
	}
	if err := gw.Close(); err != nil {
		return fmt.Errorf("export: close gzip writer: %w", err)
	}
	return nil
}

// PackDir packs all .notx files found (non-recursively) in dir.
func PackDir(dir string, dest io.Writer) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("export: read dir %q: %w", dir, err)
	}

	var filePaths []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.EqualFold(filepath.Ext(e.Name()), ".notx") {
			filePaths = append(filePaths, filepath.Join(dir, e.Name()))
		}
	}

	return Pack(filePaths, dest)
}

// Unpack reads a .gnotx archive from src and writes the .notx files into destDir.
// Returns the manifest and the list of file paths written.
func Unpack(src io.Reader, destDir string) (Manifest, []string, error) {
	var m Manifest
	var written []string

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return m, nil, fmt.Errorf("export: create dest dir %q: %w", destDir, err)
	}

	gr, err := gzip.NewReader(src)
	if err != nil {
		return m, nil, fmt.Errorf("export: open gzip reader: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return m, written, fmt.Errorf("export: read tar entry: %w", err)
		}

		if hdr.Typeflag != tar.TypeReg {
			continue
		}

		data, err := io.ReadAll(tr)
		if err != nil {
			return m, written, fmt.Errorf("export: read tar body %q: %w", hdr.Name, err)
		}

		switch hdr.Name {
		case "manifest.json":
			if err := json.Unmarshal(data, &m); err != nil {
				return m, written, fmt.Errorf("export: unmarshal manifest: %w", err)
			}

		default:
			// Only extract entries under the notes/ prefix.
			if !strings.HasPrefix(hdr.Name, "notes/") {
				continue
			}
			base := filepath.Base(hdr.Name)
			destPath := filepath.Join(destDir, base)

			//nolint:gosec // destDir is caller-supplied and base is tar-basename only.
			if err := os.WriteFile(destPath, data, 0644); err != nil {
				return m, written, fmt.Errorf("export: write file %q: %w", destPath, err)
			}
			written = append(written, destPath)
		}
	}

	return m, written, nil
}

// noteJSON is a lightweight envelope used by PackNotes to serialise a
// core.Note to JSON. Unexported fields (events, snapshots) are intentionally
// omitted — the archive captures current metadata only.
type noteJSON struct {
	URN          string            `json:"urn"`
	Name         string            `json:"name"`
	NoteType     core.NoteType     `json:"note_type,omitempty"`
	ProjectURN   *string           `json:"project_urn,omitempty"`
	FolderURN    *string           `json:"folder_urn,omitempty"`
	ParentURN    *string           `json:"parent_urn,omitempty"`
	SnipType     *string           `json:"snip_type,omitempty"`
	ParentAnchor *string           `json:"parent_anchor,omitempty"`
	NodeLinks    map[string]string `json:"node_links,omitempty"`
	Deleted      bool              `json:"deleted,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

func urnPtrToString(u *core.URN) *string {
	if u == nil {
		return nil
	}
	s := u.String()
	return &s
}

func noteToJSON(n *core.Note) ([]byte, error) {
	nj := noteJSON{
		URN:          n.URN.String(),
		Name:         n.Name,
		NoteType:     n.NoteType,
		ProjectURN:   urnPtrToString(n.ProjectURN),
		FolderURN:    urnPtrToString(n.FolderURN),
		ParentURN:    urnPtrToString(n.ParentURN),
		SnipType:     n.SnipType,
		ParentAnchor: n.ParentAnchor,
		Deleted:      n.Deleted,
		CreatedAt:    n.CreatedAt,
		UpdatedAt:    n.UpdatedAt,
	}
	if len(n.NodeLinks) > 0 {
		nj.NodeLinks = make(map[string]string, len(n.NodeLinks))
		for k, v := range n.NodeLinks {
			nj.NodeLinks[k] = v.String()
		}
	}
	return json.MarshalIndent(nj, "", "  ")
}

// PackNotes serialises core.Note values directly (without needing files on disk).
// Each note is marshalled to JSON and written as notes/<sanitised-urn>.notx.
// This is the in-memory variant used by the engine's export endpoint.
func PackNotes(notes []*core.Note, dest io.Writer) error {
	gw := gzip.NewWriter(dest)
	tw := tar.NewWriter(gw)

	m := Manifest{
		Version:       "1",
		ExportedAt:    time.Now().UTC(),
		NoteCount:     len(notes),
		EngineVersion: engineVersion,
	}
	if err := writeManifest(tw, m); err != nil {
		return err
	}

	for _, n := range notes {
		data, err := noteToJSON(n)
		if err != nil {
			return fmt.Errorf("export: marshal note %s: %w", n.URN, err)
		}
		entryName := "notes/" + sanitiseURN(n.URN.String()) + ".notx"
		if err := writeTarEntry(tw, entryName, data); err != nil {
			return err
		}
	}

	if err := tw.Close(); err != nil {
		return fmt.Errorf("export: close tar writer: %w", err)
	}
	if err := gw.Close(); err != nil {
		return fmt.Errorf("export: close gzip writer: %w", err)
	}
	return nil
}
