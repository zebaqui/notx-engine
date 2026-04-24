package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/zebaqui/notx-engine/internal/clientconfig"
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

	shortcuts, err := clientconfig.LoadShortcuts()
	if err != nil {
		return fmt.Errorf("pull: load shortcuts: %w", err)
	}

	base := resolveHTTPBase(cfg, pullFlags.addr)

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
		notes, err := plainFetchNoteList(base)
		if err != nil {
			return fmt.Errorf("pull: %w", err)
		}

		projectNames, folderNames := plainFetchProjectNames(base)
		items := buildPickerItems(notes, shortcuts, projectNames, folderNames)
		selected, ok := RunPicker(items)
		if !ok {
			return nil
		}
		urn = selected.URN
	}

	// ── GET /v1/notes/<urn> ───────────────────────────────────────────────────
	noteResp, err := plainFetchNote(base, urn)
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
		evResp, err := plainFetchEvents(base, urn)
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

	shortcuts, err := clientconfig.LoadShortcuts()
	if err != nil {
		return fmt.Errorf("pull shortcut: load shortcuts: %w", err)
	}

	base := resolveHTTPBase(cfg, pullFlags.addr)

	// ── Fetch note list and run picker ────────────────────────────────────────
	notes, err := plainFetchNoteList(base)
	if err != nil {
		return fmt.Errorf("pull shortcut: %w", err)
	}

	projectNames, folderNames := plainFetchProjectNames(base)
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
// Plain HTTP helpers (no auth headers)
// ─────────────────────────────────────────────────────────────────────────────

// plainDoGet performs a plain GET request and returns the response body on a
// 2xx status, or an error otherwise.
func plainDoGet(url string) ([]byte, error) {
	resp, err := http.Get(url) //nolint:noctx
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

// plainFetchNoteList retrieves GET /v1/notes?page_size=500.
func plainFetchNoteList(base string) ([]noteHeader, error) {
	url := base + "/v1/notes?page_size=500"
	body, err := plainDoGet(url)
	if err != nil {
		return nil, err
	}

	var result noteListResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode note list: %w", err)
	}
	return result.Notes, nil
}

// plainFetchProjectNames retrieves all projects and folders and returns two
// maps: projectNames (URN → name) and folderNames (URN → name).
// Both maps are empty (not nil) on error so the picker still works.
func plainFetchProjectNames(base string) (projectNames map[string]string, folderNames map[string]string) {
	projectNames = make(map[string]string)
	folderNames = make(map[string]string)

	projBody, err := plainDoGet(base + "/v1/projects?page_size=500")
	if err == nil {
		var projResp projectListResponse
		if err := json.Unmarshal(projBody, &projResp); err == nil {
			for _, p := range projResp.Projects {
				if p.URN != "" && p.Name != "" {
					projectNames[p.URN] = p.Name
				}
			}
		}
	}

	folderBody, err := plainDoGet(base + "/v1/folders?page_size=500")
	if err == nil {
		var folderResp folderListResponse
		if err := json.Unmarshal(folderBody, &folderResp); err == nil {
			for _, f := range folderResp.Folders {
				if f.URN != "" && f.Name != "" {
					folderNames[f.URN] = f.Name
				}
			}
		}
	}

	return projectNames, folderNames
}

// plainFetchNote retrieves GET /v1/notes/<encoded-urn>.
func plainFetchNote(base, urn string) (*noteGetResponse, error) {
	url := base + "/v1/notes/" + percentEncodeURN(urn)
	body, err := plainDoGet(url)
	if err != nil {
		return nil, err
	}

	var result noteGetResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode note: %w", err)
	}
	return &result, nil
}

// plainFetchEvents retrieves GET /v1/notes/<encoded-urn>/events.
func plainFetchEvents(base, urn string) (*eventsResponse, error) {
	url := base + "/v1/notes/" + percentEncodeURN(urn) + "/events"
	body, err := plainDoGet(url)
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
	clean := strings.ReplaceAll(last, "-", "")
	if len(clean) >= 8 {
		return last[:8] + "…"
	}
	return last + "…"
}

// buildDescription returns a human-readable label for a note's project/folder
// context.
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
