package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/zebaqui/notx-engine/core"
	"github.com/zebaqui/notx-engine/internal/clientconfig"
	"github.com/zebaqui/notx-engine/internal/cloud"
)

// ─────────────────────────────────────────────────────────────────────────────
// Commands
// ─────────────────────────────────────────────────────────────────────────────

var pullCmd = &cobra.Command{
	Use:   "pull [<id>]",
	Short: "Pull a note from the server and write it to a local file",
	Long: `Pull a note from the running notx server and write it as a local .notx file.

When called with no argument an interactive fuzzy-search picker is shown.
You can also supply a note URN, a bare UUID, or a shortcut alias:

  notx pull                          — interactive picker
  notx pull my-meeting               — pull by shortcut alias
  notx pull 1a9670dd-1a65-481a-...   — pull by bare UUID
  notx pull urn:notx:note:1a9670dd-… — pull by full URN

Flags:
  -t, --text     Write a plain .txt file (note content only, no .notx headers)
      --stdout   Print content to stdout instead of writing a file (requires -t)
      --addr     Override the HTTP server address for this invocation
  -n, --name     Override the output filename (no extension)
`,
	Args: cobra.MaximumNArgs(1),
	RunE: runPull,
}

var pullShortcutCmd = &cobra.Command{
	Use:   "shortcut",
	Short: "Interactively register a shortcut alias for a note",
	Long: `Launch the interactive picker, select a note, and assign it a local
shortcut alias. Shortcuts are stored in ~/.notx/shortcuts.json and can be
used anywhere a note URN is accepted:

  notx pull my-meeting

After registering a shortcut you can always re-run this command to update it.
`,
	Args: cobra.NoArgs,
	RunE: runPullShortcut,
}

// pullFlags holds all flag values for pullCmd.
var pullFlags struct {
	text   bool   // --text / -t
	stdout bool   // --stdout
	addr   string // --addr
	name   string // --name / -n
}

func init() {
	f := pullCmd.Flags()
	f.BoolVarP(&pullFlags.text, "text", "t", false,
		"Write plain .txt content only (no .notx headers)")
	f.BoolVarP(&pullFlags.stdout, "stdout", "s", false,
		"Print content to stdout instead of writing a file (only meaningful with -t)")
	f.StringVar(&pullFlags.addr, "addr", "",
		"Override the HTTP server address for this invocation")
	f.StringVarP(&pullFlags.name, "name", "n", "",
		"Override the output filename (no extension)")

	pullCmd.AddCommand(pullShortcutCmd)
}

// ─────────────────────────────────────────────────────────────────────────────
// API response types
// ─────────────────────────────────────────────────────────────────────────────

type noteHeader struct {
	URN        string `json:"urn"`
	Name       string `json:"name"`
	NoteType   string `json:"note_type"`
	ProjectURN string `json:"project_urn"`
	FolderURN  string `json:"folder_urn"`
	Deleted    bool   `json:"deleted"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

type noteListResponse struct {
	Notes         []noteHeader `json:"notes"`
	NextPageToken string       `json:"next_page_token"`
}

type projectItem struct {
	URN  string `json:"urn"`
	Name string `json:"name"`
}

type projectListResponse struct {
	Projects      []projectItem `json:"projects"`
	NextPageToken string        `json:"next_page_token"`
}

type folderItem struct {
	URN        string `json:"urn"`
	ProjectURN string `json:"project_urn"`
	Name       string `json:"name"`
}

type folderListResponse struct {
	Folders       []folderItem `json:"folders"`
	NextPageToken string       `json:"next_page_token"`
}

type noteGetResponse struct {
	Header  noteHeader `json:"header"`
	Content string     `json:"content"`
}

type lineEntry struct {
	Op         string `json:"op"`
	LineNumber int    `json:"line_number"`
	Content    string `json:"content"`
}

type noteEvent struct {
	URN       string      `json:"urn,omitempty"`
	NoteURN   string      `json:"note_urn,omitempty"`
	Sequence  int         `json:"sequence"`
	AuthorURN string      `json:"author_urn"`
	CreatedAt string      `json:"created_at"`
	Entries   []lineEntry `json:"entries"`
}

type eventsResponse struct {
	NoteURN string      `json:"note_urn"`
	Events  []noteEvent `json:"events"`
	Count   int         `json:"count"`
}

// ─────────────────────────────────────────────────────────────────────────────
// runPull
// ─────────────────────────────────────────────────────────────────────────────

func runPull(cmd *cobra.Command, args []string) error {
	cfg, err := clientconfig.Load()
	if err != nil {
		return fmt.Errorf("pull: load config: %w", err)
	}

	clientJSON, err := clientconfig.LoadClientJSON()
	if err != nil {
		return fmt.Errorf("pull: load client.json: %w", err)
	}

	if clientJSON.Settings.Source == "cloud" {
		return runPullCloud(args, clientJSON)
	}

	shortcuts, err := clientconfig.LoadShortcuts()
	if err != nil {
		return fmt.Errorf("pull: load shortcuts: %w", err)
	}

	// In remote mode, honour remoteUrl from client.json as the HTTP base.
	addrOverride := pullFlags.addr
	if addrOverride == "" && clientJSON.Settings.Source == "remote" && clientJSON.Settings.RemoteURL != "" {
		addrOverride = clientJSON.Settings.RemoteURL
	}
	base := httpBase(cfg, addrOverride)

	dc, err := newDeviceClient(base, cfg)
	if err != nil {
		return fmt.Errorf("pull: %w", err)
	}

	// ── Resolve the target URN ────────────────────────────────────────────────
	var urn string

	if len(args) == 1 {
		id := args[0]

		// Check shortcuts first.
		if sc, ok := shortcuts.Resolve(id); ok {
			urn = sc.URN
		} else {
			// Treat as URN or bare UUID.
			urn = expandURN(id)
		}
	} else {
		// No argument: fetch list and run interactive picker.
		notes, err := dc.fetchNoteList()
		if err != nil {
			return fmt.Errorf("pull: %w", err)
		}

		projectNames, folderNames, _ := dc.fetchProjectNames()
		items := buildPickerItems(notes, shortcuts, projectNames, folderNames)
		selected, ok := RunPicker(items)
		if !ok {
			return nil
		}
		urn = selected.URN
	}

	// ── GET /v1/notes/<urn> ───────────────────────────────────────────────────
	noteResp, err := dc.fetchNote(urn)
	if err != nil {
		return fmt.Errorf("pull: %w", err)
	}

	// ── Determine output filename ─────────────────────────────────────────────
	outName := pullFlags.name
	if outName == "" {
		outName = noteResp.Header.Name
	}
	if outName == "" {
		outName = shortURN(urn)
	}

	// ── --text --stdout: print content only and return early ─────────────────
	if pullFlags.text && pullFlags.stdout {
		_, err = fmt.Fprint(os.Stdout, noteResp.Content)
		if err != nil {
			return fmt.Errorf("pull: write stdout: %w", err)
		}
		return nil
	}

	// ── Determine file extension and path ─────────────────────────────────────
	ext := ".notx"
	if pullFlags.text {
		ext = ".txt"
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("pull: get working dir: %w", err)
	}

	outPath := filepath.Join(cwd, outName+ext)

	// ── Check for existing file ───────────────────────────────────────────────
	if _, err := os.Stat(outPath); err == nil {
		fmt.Fprintf(os.Stderr, "  File %s already exists. Overwrite? [y/N] ", outName+ext)
		sc := bufio.NewScanner(os.Stdin)
		if sc.Scan() {
			v := strings.TrimSpace(strings.ToLower(sc.Text()))
			if v != "y" && v != "yes" {
				fmt.Fprintln(os.Stderr, "  Aborted.")
				return nil
			}
		} else {
			fmt.Fprintln(os.Stderr, "  Aborted.")
			return nil
		}
	}

	// ── Write file ────────────────────────────────────────────────────────────
	if pullFlags.text {
		if err := os.WriteFile(outPath, []byte(noteResp.Content), 0o644); err != nil {
			return fmt.Errorf("pull: write %s: %w", outPath, err)
		}
	} else {
		// Full .notx format: fetch events first.
		evResp, err := dc.fetchEvents(urn)
		if err != nil {
			return fmt.Errorf("pull: %w", err)
		}

		if err := writeNotxFile(outPath, noteResp.Header, evResp); err != nil {
			return fmt.Errorf("pull: write %s: %w", outPath, err)
		}
	}

	// ── Success summary ───────────────────────────────────────────────────────
	relPath := "./" + outName + ext
	printPullSummary(urn, noteResp.Header.Name, relPath, noteResp.Header.ProjectURN, noteResp.Header.FolderURN, outName, shortcuts, false)

	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// runPullShortcut
// ─────────────────────────────────────────────────────────────────────────────

func runPullShortcut(cmd *cobra.Command, args []string) error {
	cfg, err := clientconfig.Load()
	if err != nil {
		return fmt.Errorf("pull shortcut: load config: %w", err)
	}

	clientJSON, err := clientconfig.LoadClientJSON()
	if err != nil {
		return fmt.Errorf("pull shortcut: load client.json: %w", err)
	}

	if clientJSON.Settings.Source == "cloud" {
		return runPullShortcutCloud(clientJSON)
	}

	shortcuts, err := clientconfig.LoadShortcuts()
	if err != nil {
		return fmt.Errorf("pull shortcut: load shortcuts: %w", err)
	}

	// In remote mode, honour remoteUrl from client.json as the HTTP base.
	addrOverride := pullFlags.addr
	if addrOverride == "" && clientJSON.Settings.Source == "remote" && clientJSON.Settings.RemoteURL != "" {
		addrOverride = clientJSON.Settings.RemoteURL
	}
	base := httpBase(cfg, addrOverride)

	dc, err := newDeviceClient(base, cfg)
	if err != nil {
		return fmt.Errorf("pull shortcut: %w", err)
	}

	// ── Fetch note list and run picker ────────────────────────────────────────
	notes, err := dc.fetchNoteList()
	if err != nil {
		return fmt.Errorf("pull shortcut: %w", err)
	}

	projectNames, folderNames, _ := dc.fetchProjectNames()
	items := buildPickerItems(notes, shortcuts, projectNames, folderNames)
	selected, ok := RunPicker(items)
	if !ok {
		return nil
	}

	fmt.Fprintf(os.Stderr, "\n  Selected: %s  (%s)\n", selected.Name, selected.URN)

	// ── Prompt for alias (up to 3 attempts) ──────────────────────────────────
	sc := bufio.NewScanner(os.Stdin)
	var alias string

	for attempt := 0; attempt < 3; attempt++ {
		fmt.Fprint(os.Stderr, "  Enter shortcut name: ")
		if !sc.Scan() {
			break
		}
		raw := strings.TrimSpace(sc.Text())

		if raw == "" {
			fmt.Fprintln(os.Stderr, "  (skipped)")
			return nil
		}

		if err := clientconfig.ValidateShortcutName(raw); err != nil {
			fmt.Fprintf(os.Stderr, "  \033[1;33m⚠\033[0m  %v\n", err)
			continue
		}

		alias = raw
		break
	}

	if alias == "" {
		fmt.Fprintln(os.Stderr, "  Too many invalid attempts — aborted.")
		return nil
	}

	// ── Conflict: this URN already has a shortcut? ────────────────────────────
	if existingAlias, _, found := shortcuts.FindByURN(selected.URN); found && existingAlias != alias {
		fmt.Fprintf(os.Stderr, "  \033[1;33m⚠\033[0m  This note already has shortcut %q. Replace? [y/N] ", existingAlias)
		if sc.Scan() {
			v := strings.TrimSpace(strings.ToLower(sc.Text()))
			if v != "y" && v != "yes" {
				fmt.Fprintln(os.Stderr, "  Aborted.")
				return nil
			}
		} else {
			fmt.Fprintln(os.Stderr, "  Aborted.")
			return nil
		}
	}

	// ── Conflict: alias already points to a different note? ───────────────────
	if existing, ok := shortcuts.Resolve(alias); ok && existing.URN != selected.URN {
		fmt.Fprintf(os.Stderr, "  \033[1;33m⚠\033[0m  Shortcut %q already points to %s. Overwrite? [y/N] ", alias, existing.Name)
		if sc.Scan() {
			v := strings.TrimSpace(strings.ToLower(sc.Text()))
			if v != "y" && v != "yes" {
				fmt.Fprintln(os.Stderr, "  Aborted.")
				return nil
			}
		} else {
			fmt.Fprintln(os.Stderr, "  Aborted.")
			return nil
		}
	}

	// ── Save shortcut ─────────────────────────────────────────────────────────
	sc2 := &clientconfig.Shortcut{
		URN:         selected.URN,
		Name:        selected.Name,
		Description: selected.Description,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}

	if err := shortcuts.Add(alias, sc2); err != nil {
		return fmt.Errorf("pull shortcut: %w", err)
	}

	if err := clientconfig.SaveShortcuts(shortcuts); err != nil {
		return fmt.Errorf("pull shortcut: save shortcuts: %w", err)
	}

	fmt.Fprintf(os.Stderr, "\n  \033[1;32m✓\033[0m  Shortcut %q → %s saved.\n\n", alias, selected.Name)
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// HTTP helpers
// ─────────────────────────────────────────────────────────────────────────────

// ─────────────────────────────────────────────────────────────────────────────
// runPullCloud / runPullShortcutCloud
// ─────────────────────────────────────────────────────────────────────────────

// runPullCloud is the cloud-mode equivalent of runPull. It uses cloud.NoteClient
// (Bearer JWT) instead of deviceClient (X-Device-ID).
func runPullCloud(args []string, clientJSON *clientconfig.ClientJSON) error {
	token, err := cloud.EnsureToken(clientJSON)
	if err != nil {
		return fmt.Errorf("pull cloud: auth: %w", err)
	}

	nc := cloud.NewNoteClient(token)
	ctx := context.Background()

	shortcuts, err := clientconfig.LoadShortcuts()
	if err != nil {
		return fmt.Errorf("pull cloud: load shortcuts: %w", err)
	}

	// ── Resolve target URN ────────────────────────────────────────────────────
	var urn string

	if len(args) == 1 {
		id := args[0]
		if sc, ok := shortcuts.Resolve(id); ok {
			urn = sc.URN
		} else {
			urn = expandURN(id)
		}
	} else {
		// Interactive picker — fetch note + project + folder lists.
		notes, err := nc.ListNotes(ctx)
		if err != nil {
			return fmt.Errorf("pull cloud: %w", err)
		}

		projectNames, folderNames := cloudProjectFolderNames(ctx, nc)

		// Convert cloud.NoteHeader slice to the local noteHeader slice that
		// buildPickerItems expects.
		localHeaders := make([]noteHeader, 0, len(notes))
		for _, n := range notes {
			localHeaders = append(localHeaders, noteHeader{
				URN:        n.URN,
				Name:       n.Name,
				NoteType:   n.NoteType,
				ProjectURN: n.ProjectURN,
				FolderURN:  n.FolderURN,
				Deleted:    n.Deleted,
				CreatedAt:  n.CreatedAt,
				UpdatedAt:  n.UpdatedAt,
			})
		}

		items := buildPickerItems(localHeaders, shortcuts, projectNames, folderNames)
		selected, ok := RunPicker(items)
		if !ok {
			return nil
		}
		urn = selected.URN
	}

	// ── Fetch note ────────────────────────────────────────────────────────────
	noteResp, err := nc.GetNote(ctx, urn)
	if err != nil {
		return fmt.Errorf("pull cloud: %w", err)
	}

	outName := pullFlags.name
	if outName == "" {
		outName = noteResp.Header.Name
	}
	if outName == "" {
		outName = shortURN(urn)
	}

	// ── --text --stdout ───────────────────────────────────────────────────────
	if pullFlags.text && pullFlags.stdout {
		_, err = fmt.Fprint(os.Stdout, noteResp.Content)
		return err
	}

	ext := ".notx"
	if pullFlags.text {
		ext = ".txt"
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("pull cloud: get working dir: %w", err)
	}
	outPath := filepath.Join(cwd, outName+ext)

	// ── Overwrite prompt ──────────────────────────────────────────────────────
	if _, err := os.Stat(outPath); err == nil {
		fmt.Fprintf(os.Stderr, "  File %s already exists. Overwrite? [y/N] ", outName+ext)
		sc := bufio.NewScanner(os.Stdin)
		if sc.Scan() {
			v := strings.TrimSpace(strings.ToLower(sc.Text()))
			if v != "y" && v != "yes" {
				fmt.Fprintln(os.Stderr, "  Aborted.")
				return nil
			}
		} else {
			fmt.Fprintln(os.Stderr, "  Aborted.")
			return nil
		}
	}

	// ── Write file ────────────────────────────────────────────────────────────
	if pullFlags.text {
		if err := os.WriteFile(outPath, []byte(noteResp.Content), 0o644); err != nil {
			return fmt.Errorf("pull cloud: write %s: %w", outPath, err)
		}
	} else {
		evResp, err := nc.GetEvents(ctx, urn)
		if err != nil {
			return fmt.Errorf("pull cloud: %w", err)
		}

		// Convert cloud.EventsResponse → local eventsResponse for writeNotxFile.
		localEv := cloudEventsToLocal(evResp)
		hdr := noteHeader{
			URN:        noteResp.Header.URN,
			Name:       noteResp.Header.Name,
			NoteType:   noteResp.Header.NoteType,
			ProjectURN: noteResp.Header.ProjectURN,
			FolderURN:  noteResp.Header.FolderURN,
			CreatedAt:  noteResp.Header.CreatedAt,
			UpdatedAt:  noteResp.Header.UpdatedAt,
		}
		if err := writeNotxFile(outPath, hdr, localEv); err != nil {
			return fmt.Errorf("pull cloud: write %s: %w", outPath, err)
		}
	}

	relPath := "./" + outName + ext
	printPullSummary(urn, noteResp.Header.Name, relPath, noteResp.Header.ProjectURN, noteResp.Header.FolderURN, outName, shortcuts, false)
	return nil
}

// runPullShortcutCloud is the cloud-mode equivalent of runPullShortcut.
func runPullShortcutCloud(clientJSON *clientconfig.ClientJSON) error {
	token, err := cloud.EnsureToken(clientJSON)
	if err != nil {
		return fmt.Errorf("pull shortcut cloud: auth: %w", err)
	}

	nc := cloud.NewNoteClient(token)
	ctx := context.Background()

	shortcuts, err := clientconfig.LoadShortcuts()
	if err != nil {
		return fmt.Errorf("pull shortcut cloud: load shortcuts: %w", err)
	}

	notes, err := nc.ListNotes(ctx)
	if err != nil {
		return fmt.Errorf("pull shortcut cloud: %w", err)
	}

	projectNames, folderNames := cloudProjectFolderNames(ctx, nc)

	localHeaders := make([]noteHeader, 0, len(notes))
	for _, n := range notes {
		localHeaders = append(localHeaders, noteHeader{
			URN:        n.URN,
			Name:       n.Name,
			NoteType:   n.NoteType,
			ProjectURN: n.ProjectURN,
			FolderURN:  n.FolderURN,
			Deleted:    n.Deleted,
			CreatedAt:  n.CreatedAt,
			UpdatedAt:  n.UpdatedAt,
		})
	}

	items := buildPickerItems(localHeaders, shortcuts, projectNames, folderNames)
	selected, ok := RunPicker(items)
	if !ok {
		return nil
	}

	fmt.Fprintf(os.Stderr, "\n  Selected: %s  (%s)\n", selected.Name, selected.URN)

	// ── Prompt for alias ──────────────────────────────────────────────────────
	sc := bufio.NewScanner(os.Stdin)
	var alias string

	for attempt := 0; attempt < 3; attempt++ {
		fmt.Fprint(os.Stderr, "  Enter shortcut name: ")
		if !sc.Scan() {
			break
		}
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			fmt.Fprintln(os.Stderr, "  (skipped)")
			return nil
		}
		if err := clientconfig.ValidateShortcutName(raw); err != nil {
			fmt.Fprintf(os.Stderr, "  \033[1;33m⚠\033[0m  %v\n", err)
			continue
		}
		alias = raw
		break
	}

	if alias == "" {
		fmt.Fprintln(os.Stderr, "  Too many invalid attempts — aborted.")
		return nil
	}

	if existingAlias, _, found := shortcuts.FindByURN(selected.URN); found && existingAlias != alias {
		fmt.Fprintf(os.Stderr, "  \033[1;33m⚠\033[0m  This note already has shortcut %q. Replace? [y/N] ", existingAlias)
		if sc.Scan() {
			v := strings.TrimSpace(strings.ToLower(sc.Text()))
			if v != "y" && v != "yes" {
				fmt.Fprintln(os.Stderr, "  Aborted.")
				return nil
			}
		} else {
			fmt.Fprintln(os.Stderr, "  Aborted.")
			return nil
		}
	}

	if existing, ok := shortcuts.Resolve(alias); ok && existing.URN != selected.URN {
		fmt.Fprintf(os.Stderr, "  \033[1;33m⚠\033[0m  Shortcut %q already points to %s. Overwrite? [y/N] ", alias, existing.Name)
		if sc.Scan() {
			v := strings.TrimSpace(strings.ToLower(sc.Text()))
			if v != "y" && v != "yes" {
				fmt.Fprintln(os.Stderr, "  Aborted.")
				return nil
			}
		} else {
			fmt.Fprintln(os.Stderr, "  Aborted.")
			return nil
		}
	}

	sc2 := &clientconfig.Shortcut{
		URN:         selected.URN,
		Name:        selected.Name,
		Description: selected.Description,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	if err := shortcuts.Add(alias, sc2); err != nil {
		return fmt.Errorf("pull shortcut cloud: %w", err)
	}
	if err := clientconfig.SaveShortcuts(shortcuts); err != nil {
		return fmt.Errorf("pull shortcut cloud: save shortcuts: %w", err)
	}

	fmt.Fprintf(os.Stderr, "\n  \033[1;32m✓\033[0m  Shortcut %q → %s saved.\n\n", alias, selected.Name)
	return nil
}

// cloudProjectFolderNames fetches project and folder name maps from the cloud,
// returning empty maps (not nil) on error so the picker still works.
func cloudProjectFolderNames(ctx context.Context, nc *cloud.NoteClient) (projectNames, folderNames map[string]string) {
	projectNames = make(map[string]string)
	folderNames = make(map[string]string)

	if projects, err := nc.ListProjects(ctx); err == nil {
		for _, p := range projects {
			if p.URN != "" && p.Name != "" {
				projectNames[p.URN] = p.Name
			}
		}
	}
	if folders, err := nc.ListFolders(ctx); err == nil {
		for _, f := range folders {
			if f.URN != "" && f.Name != "" {
				folderNames[f.URN] = f.Name
			}
		}
	}
	return projectNames, folderNames
}

// cloudEventsToLocal converts a *cloud.EventsResponse to a *eventsResponse
// so it can be passed to writeNotxFile unchanged.
func cloudEventsToLocal(ev *cloud.EventsResponse) *eventsResponse {
	local := &eventsResponse{
		NoteURN: ev.NoteURN,
		Count:   ev.Count,
		Events:  make([]noteEvent, 0, len(ev.Events)),
	}
	for _, e := range ev.Events {
		le := noteEvent{
			URN:       e.URN,
			NoteURN:   e.NoteURN,
			Sequence:  e.Sequence,
			AuthorURN: e.AuthorURN,
			CreatedAt: e.CreatedAt,
			Entries:   make([]lineEntry, 0, len(e.Entries)),
		}
		for _, en := range e.Entries {
			le.Entries = append(le.Entries, lineEntry{
				Op:         en.Op,
				LineNumber: en.LineNumber,
				Content:    en.Content,
			})
		}
		local.Events = append(local.Events, le)
	}
	return local
}

// ─────────────────────────────────────────────────────────────────────────────
// deviceClient — HTTP client that injects X-Device-ID on every request
// ─────────────────────────────────────────────────────────────────────────────

// deviceClient wraps an http.Client and stamps every outbound request with
// the X-Device-ID header required by the notx server middleware.
type deviceClient struct {
	base      string
	deviceURN string
	hc        *http.Client
}

// newDeviceClient resolves the device URN to use for pull requests and returns
// a ready-to-use deviceClient.
//
// Resolution order (mirrors the admin command):
//  1. cfg.Admin.AdminDeviceURN — the built-in per-installation admin device
//     that the server bootstraps automatically on first run. Used whenever the
//     server is local (i.e. no remote DeviceURN has been configured).
//  2. cfg.Admin.DeviceURN — a remotely registered admin device saved by
//     `notx admin --remote`. Used when the server is not local.
//  3. Auto-register — if neither URN is set (fresh install that has not run
//     `notx server` yet), register a new local device and persist its URN so
//     subsequent calls reuse it.
func newDeviceClient(base string, cfg *clientconfig.Config) (*deviceClient, error) {
	deviceURN := cfg.Admin.AdminDeviceURN
	if deviceURN == "" {
		deviceURN = cfg.Admin.DeviceURN
	}

	// If we still have no device URN, auto-register one and persist it.
	if deviceURN == "" {
		var err error
		deviceURN, err = autoRegisterDevice(base, cfg)
		if err != nil {
			return nil, fmt.Errorf("resolve device: %w", err)
		}
	}

	return &deviceClient{
		base:      base,
		deviceURN: deviceURN,
		hc:        &http.Client{},
	}, nil
}

// autoRegisterDevice registers a fresh local device against the server,
// persists its URN to cfg.Admin.AdminDeviceURN, and returns the URN.
// This mirrors the EnsureConfig admin-URN bootstrapping logic for the case
// where `notx server` has not been run yet on this machine.
func autoRegisterDevice(base string, cfg *clientconfig.Config) (string, error) {
	deviceURN := core.NewURN(core.ObjectTypeDevice).String()
	ownerURN := core.NewURN(core.ObjectTypeUser).String()

	type regReq struct {
		URN      string `json:"urn"`
		Name     string `json:"name"`
		OwnerURN string `json:"owner_urn"`
	}
	type regResp struct {
		URN            string `json:"urn"`
		ApprovalStatus string `json:"approval_status"`
	}

	body, _ := json.Marshal(regReq{
		URN:      deviceURN,
		Name:     "notx-cli",
		OwnerURN: ownerURN,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/devices", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build register request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("POST %s/v1/devices: %w", base, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("device registration failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	// Persist so subsequent calls reuse the same URN.
	cfg.Admin.AdminDeviceURN = deviceURN
	cfg.Admin.AdminOwnerURN = ownerURN
	if err := clientconfig.Save(cfg); err != nil {
		// Non-fatal: warn but continue — this session will work fine.
		fmt.Fprintf(os.Stderr, "  \033[1;33m⚠\033[0m  Could not persist device URN: %v\n", err)
	}

	return deviceURN, nil
}

// doGet performs a GET request with the X-Device-ID header set, returning the
// parsed body bytes on a 2xx response or an error on failure.
func (dc *deviceClient) doGet(url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("X-Device-ID", dc.deviceURN)

	resp, err := dc.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("GET %s: server returned %d: %s", url, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

// fetchNoteList retrieves GET /v1/notes?page_size=500.
func (dc *deviceClient) fetchNoteList() ([]noteHeader, error) {
	url := dc.base + "/v1/notes?page_size=500"
	body, err := dc.doGet(url)
	if err != nil {
		return nil, err
	}

	var result noteListResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode note list: %w", err)
	}
	return result.Notes, nil
}

// fetchProjectNames retrieves all projects and folders and returns two maps:
//
//	projectNames : project URN → project name
//	folderNames  : folder URN  → folder name
//
// Both maps are empty (not nil) on success even when there are no entries.
// Errors are non-fatal for the picker — callers should fall back gracefully.
func (dc *deviceClient) fetchProjectNames() (projectNames map[string]string, folderNames map[string]string, err error) {
	projectNames = make(map[string]string)
	folderNames = make(map[string]string)

	// ── Projects ──────────────────────────────────────────────────────────────
	projBody, err := dc.doGet(dc.base + "/v1/projects?page_size=500")
	if err != nil {
		return projectNames, folderNames, fmt.Errorf("fetch projects: %w", err)
	}
	var projResp projectListResponse
	if err := json.Unmarshal(projBody, &projResp); err != nil {
		return projectNames, folderNames, fmt.Errorf("decode projects: %w", err)
	}
	for _, p := range projResp.Projects {
		if p.URN != "" && p.Name != "" {
			projectNames[p.URN] = p.Name
		}
	}

	// ── Folders ───────────────────────────────────────────────────────────────
	folderBody, err := dc.doGet(dc.base + "/v1/folders?page_size=500")
	if err != nil {
		// Folders are optional — don't fail the whole picker.
		return projectNames, folderNames, nil
	}
	var folderResp folderListResponse
	if err := json.Unmarshal(folderBody, &folderResp); err != nil {
		return projectNames, folderNames, nil
	}
	for _, f := range folderResp.Folders {
		if f.URN != "" && f.Name != "" {
			folderNames[f.URN] = f.Name
		}
	}

	return projectNames, folderNames, nil
}

// fetchNote retrieves GET /v1/notes/<encoded-urn>.
func (dc *deviceClient) fetchNote(urn string) (*noteGetResponse, error) {
	url := dc.base + "/v1/notes/" + percentEncodeURN(urn)
	body, err := dc.doGet(url)
	if err != nil {
		return nil, err
	}

	var result noteGetResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode note: %w", err)
	}
	return &result, nil
}

// fetchEvents retrieves GET /v1/notes/<encoded-urn>/events.
func (dc *deviceClient) fetchEvents(urn string) (*eventsResponse, error) {
	url := dc.base + "/v1/notes/" + percentEncodeURN(urn) + "/events"
	body, err := dc.doGet(url)
	if err != nil {
		return nil, err
	}

	var result eventsResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode events: %w", err)
	}
	return &result, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// File writing
// ─────────────────────────────────────────────────────────────────────────────

// writeNotxFile writes a complete .notx file to path from the given header and
// event stream.
func writeNotxFile(path string, hdr noteHeader, ev *eventsResponse) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := bufio.NewWriter(f)

	// ── File header ───────────────────────────────────────────────────────────
	fmt.Fprintln(w, "# notx/1.0")
	fmt.Fprintf(w, "# note_urn:      %s\n", hdr.URN)

	noteType := hdr.NoteType
	if noteType == "" {
		noteType = "normal"
	}
	fmt.Fprintf(w, "# note_type:     %s\n", noteType)
	fmt.Fprintf(w, "# name:          %s\n", hdr.Name)

	if hdr.ProjectURN != "" {
		fmt.Fprintf(w, "# project_urn:   %s\n", hdr.ProjectURN)
	}
	if hdr.FolderURN != "" {
		fmt.Fprintf(w, "# folder_urn:    %s\n", hdr.FolderURN)
	}

	fmt.Fprintf(w, "# created_at:    %s\n", hdr.CreatedAt)

	// head_sequence is the sequence of the last event.
	headSeq := 0
	if len(ev.Events) > 0 {
		headSeq = ev.Events[len(ev.Events)-1].Sequence
	}
	fmt.Fprintf(w, "# head_sequence: %d\n", headSeq)

	// ── Events ────────────────────────────────────────────────────────────────
	for _, event := range ev.Events {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "%d:%s:%s\n", event.Sequence, event.CreatedAt, event.AuthorURN)
		fmt.Fprintln(w, "->")

		for _, entry := range event.Entries {
			switch entry.Op {
			case "set":
				fmt.Fprintf(w, "%d | %s\n", entry.LineNumber, entry.Content)
			case "set_empty":
				fmt.Fprintf(w, "%d |\n", entry.LineNumber)
			case "delete":
				fmt.Fprintf(w, "%d |-\n", entry.LineNumber)
			default:
				// Unknown op: write as set for safety.
				fmt.Fprintf(w, "%d | %s\n", entry.LineNumber, entry.Content)
			}
		}
	}

	// Trailing newline.
	fmt.Fprintln(w)

	return w.Flush()
}

// ─────────────────────────────────────────────────────────────────────────────
// Summary output
// ─────────────────────────────────────────────────────────────────────────────

// printPullSummary prints the post-pull success block to stderr.
// filePath is the relative path written (e.g. "./meeting-notes.notx");
// pass "" to omit the file line (--stdout mode).
// stdoutMode suppresses the shortcut hint entirely.
func printPullSummary(urn, name, filePath, projectURN, folderURN, noteName string, shortcuts clientconfig.Shortcuts, stdoutMode bool) {
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "  \033[1;32m✓\033[0m  Note pulled\n")
	fmt.Fprintf(os.Stderr, "     name     : %s\n", name)
	fmt.Fprintf(os.Stderr, "     urn      : %s\n", urn)

	if alias, _, found := shortcuts.FindByURN(urn); found {
		fmt.Fprintf(os.Stderr, "     shortcut : %s\n", alias)
	}

	if filePath != "" {
		fmt.Fprintf(os.Stderr, "     file     : %s\n", filePath)
	}

	if desc := buildDescription(projectURN, folderURN, nil, nil); desc != "" {
		fmt.Fprintf(os.Stderr, "     project  : %s\n", desc)
	}

	fmt.Fprintln(os.Stderr)

	if !stdoutMode {
		if _, _, found := shortcuts.FindByURN(urn); !found {
			fmt.Fprintf(os.Stderr, "  \033[2m💡 No shortcut yet — run  notx pull shortcut  to register one.\033[0m\n\n")
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Picker helpers
// ─────────────────────────────────────────────────────────────────────────────

// buildPickerItems converts a list of noteHeaders to PickerItems, decorating
// each with the shortcut alias if one exists and resolving project/folder URNs
// to human-readable names via the supplied lookup maps.
func buildPickerItems(notes []noteHeader, shortcuts clientconfig.Shortcuts, projectNames, folderNames map[string]string) []PickerItem {
	items := make([]PickerItem, 0, len(notes))
	for _, n := range notes {
		if n.Deleted {
			continue
		}
		alias := ""
		if a, _, found := shortcuts.FindByURN(n.URN); found {
			alias = a
		}
		items = append(items, PickerItem{
			URN:         n.URN,
			Name:        n.Name,
			ShortURN:    shortURN(n.URN),
			Description: buildDescription(n.ProjectURN, n.FolderURN, projectNames, folderNames),
			Shortcut:    alias,
		})
	}
	return items
}

// ─────────────────────────────────────────────────────────────────────────────
// Helper functions
// ─────────────────────────────────────────────────────────────────────────────

// httpBase normalises a server address to a http://host:port base URL.
// If addrOverride is non-empty it takes precedence over the config value.
// A bare ":4060" is expanded to "http://localhost:4060".
func httpBase(cfg *clientconfig.Config, addrOverride string) string {
	addr := cfg.Server.HTTPAddr
	if addrOverride != "" {
		addr = addrOverride
	}

	// Already a full URL — return as-is.
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return strings.TrimRight(addr, "/")
	}

	// ":port" → "http://localhost:port"
	if strings.HasPrefix(addr, ":") {
		return "http://localhost" + addr
	}

	return "http://" + addr
}

// expandURN takes an id and, if it looks like a bare UUID (no colons), prepends
// the standard notx note URN prefix.
func expandURN(id string) string {
	if !strings.Contains(id, ":") {
		return "urn:notx:note:" + id
	}
	return id
}

// shortURN extracts the UUID portion from a notx URN and returns the first
// 8 characters followed by "…".  Falls back to the raw id when parsing fails.
func shortURN(urn string) string {
	parts := strings.Split(urn, ":")
	if len(parts) == 0 {
		return urn
	}
	last := parts[len(parts)-1]
	if len(last) == 0 {
		return urn
	}
	// Remove hyphens to get a clean UUID string, then re-extract 8 chars.
	clean := strings.ReplaceAll(last, "-", "")
	if len(clean) >= 8 {
		return last[:8] + "…"
	}
	return last + "…"
}

// buildDescription returns a human-readable label for a note's project/folder
// context. It looks up real names from the supplied maps first; if the URN is
// not in the map (project not fetched, or note has no project) it returns "".
//
// Examples:
//
//	projectNames["urn:notx:proj:x"] = "Engineering"
//	folderNames["urn:notx:folder:y"]  = "Sprint 42"
//	→ "Engineering / Sprint 42"
//
//	projectNames["urn:notx:proj:x"] = "Engineering", no folder
//	→ "Engineering"
//
//	no project URN → ""
func buildDescription(projectURN, folderURN string, projectNames, folderNames map[string]string) string {
	var projName, folderName string
	if projectNames != nil {
		projName = projectNames[projectURN]
	}
	if folderNames != nil {
		folderName = folderNames[folderURN]
	}

	switch {
	case projName != "" && folderName != "":
		return truncateRunes(projName+" / "+folderName, 50)
	case projName != "":
		return truncateRunes(projName, 50)
	default:
		return ""
	}
}

// urnLastSegment returns the last colon-separated segment of a URN, or "" if
// urn is empty.
func urnLastSegment(urn string) string {
	if urn == "" {
		return ""
	}
	parts := strings.Split(urn, ":")
	return parts[len(parts)-1]
}

// truncateRunes shortens s to at most n runes, appending "…" when truncated.
func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}
