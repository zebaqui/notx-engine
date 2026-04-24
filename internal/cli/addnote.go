package cli

import (
	"bufio"
	"bytes"
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
)

var addNoteCmd = &cobra.Command{
	Use:   "add [file]",
	Short: "Create a new note from a file, or update an existing one",
	Long: `Create a new note by sending it to the running notx HTTP server,
or update an existing note's content when --urn is provided.

When --urn is given the file content is diffed against the note's current
state on the server. Only changed lines are written as a new event — the
full document is never re-stored verbatim.

The note name is derived from the file's base name (without extension).
The server address is read from ~/.notx/config.json (server.http_addr).
Override it for a single invocation with --addr.

Examples:
  # Create a normal note from meeting-notes.txt
  notx add meeting-notes.txt

  # Push a new version of an existing note
  notx add meeting-notes.txt --urn notx:note:1a9670dd-1a65-481a-ad17-03d77de021e5

  # Create a secure (E2EE) note and delete the source file afterwards
  notx add secrets.txt --secure -d

  # Point at a non-default server for this invocation
  notx add todo.md --addr http://localhost:7430
`,
	Args: cobra.ExactArgs(1),
	RunE: runAddNote,
}

var addNoteFlags struct {
	addr       string // override HTTP server address for this invocation
	urn        string // when set, update an existing note instead of creating
	delete     bool
	secure     bool
	projectURN string // optional project URN for candidate detection
	folderURN  string // optional folder URN
}

func init() {
	f := addNoteCmd.Flags()
	f.StringVar(&addNoteFlags.addr, "addr", "",
		"HTTP server address to dial (overrides config server.http_addr)")
	f.StringVar(&addNoteFlags.urn, "urn", "",
		"URN of an existing note to update (skips creation, diffs and appends an event)")
	f.BoolVarP(&addNoteFlags.delete, "delete", "d", false,
		"Delete the source file after successfully creating the note")
	f.BoolVar(&addNoteFlags.secure, "secure", false,
		"Mark the note as secure (end-to-end encrypted)")
	f.StringVar(&addNoteFlags.projectURN, "project", "",
		"Project URN to assign the note to (enables candidate detection)")
	f.StringVar(&addNoteFlags.folderURN, "folder", "",
		"Folder URN to assign the note to (optional, requires --project)")

	// Register as both a named sub-command and the default command when
	// the root receives a bare file argument.
	rootCmd.AddCommand(addNoteCmd)
	rootCmd.Args = cobra.ArbitraryArgs
	rootCmd.RunE = runAddNoteFromRoot
}

// runAddNoteFromRoot is set as rootCmd.RunE so that:
//
//	notx some/file.txt [flags]
//
// behaves identically to:
//
//	notx add some/file.txt [flags]
func runAddNoteFromRoot(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}
	return runAddNote(cmd, args)
}

func runAddNote(cmd *cobra.Command, args []string) error {
	srcPath := args[0]

	// ── Resolve & validate the source file ───────────────────────────────────
	absPath, err := filepath.Abs(srcPath)
	if err != nil {
		return fmt.Errorf("resolve path %q: %w", srcPath, err)
	}

	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("file not found: %s", absPath)
		}
		return fmt.Errorf("stat %q: %w", absPath, err)
	}
	if info.IsDir() {
		return fmt.Errorf("%q is a directory, not a file", absPath)
	}

	// ── Read file content ─────────────────────────────────────────────────────
	raw, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("read %q: %w", absPath, err)
	}
	content := strings.TrimRight(string(raw), "\n")

	// ── Load config ───────────────────────────────────────────────────────────
	cfg, err := clientconfig.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// ── Derive note name from filename ────────────────────────────────────────
	base := filepath.Base(absPath)
	ext := filepath.Ext(base)
	noteName := strings.TrimSuffix(base, ext)
	if noteName == "" {
		noteName = base
	}

	lines := splitContentLines(content)

	// ── If --urn is set, push a content update via HTTP ───────────────────────
	if addNoteFlags.urn != "" {
		return runUpdateContent(absPath, addNoteFlags.urn, content, cfg)
	}

	// ── Resolve the HTTP base URL ─────────────────────────────────────────────
	httpAddr := resolveHTTPBase(cfg, addNoteFlags.addr)

	// ── Derive note URN and type ──────────────────────────────────────────────
	noteURNStr := core.NewURN(core.ObjectTypeNote).String()

	noteType := "normal"
	if addNoteFlags.secure {
		noteType = "secure"
	}

	nowStr := time.Now().UTC().Format(time.RFC3339)

	// ── POST /v1/notes ────────────────────────────────────────────────────────
	createBody := struct {
		URN        string `json:"urn"`
		Name       string `json:"name"`
		NoteType   string `json:"note_type"`
		ProjectURN string `json:"project_urn,omitempty"`
		FolderURN  string `json:"folder_urn,omitempty"`
		CreatedAt  string `json:"created_at"`
		UpdatedAt  string `json:"updated_at"`
	}{
		URN:        noteURNStr,
		Name:       noteName,
		NoteType:   noteType,
		ProjectURN: addNoteFlags.projectURN,
		FolderURN:  addNoteFlags.folderURN,
		CreatedAt:  nowStr,
		UpdatedAt:  nowStr,
	}

	createBodyBytes, err := json.Marshal(createBody)
	if err != nil {
		return fmt.Errorf("marshal create request: %w", err)
	}

	createURL := httpAddr + "/v1/notes"
	createResp, err := http.Post(createURL, "application/json", bytes.NewReader(createBodyBytes)) //nolint:noctx
	if err != nil {
		return fmt.Errorf("POST %s: %w", createURL, err)
	}
	defer createResp.Body.Close()

	createRespBody, _ := io.ReadAll(createResp.Body)
	if createResp.StatusCode >= 400 {
		var errBody struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(createRespBody, &errBody)
		msg := errBody.Error
		if msg == "" {
			msg = strings.TrimSpace(string(createRespBody))
		}
		return fmt.Errorf("create note (%d): %s", createResp.StatusCode, msg)
	}

	// Parse the response to get the canonical URN back from the server.
	var createResult struct {
		URN  string `json:"urn"`
		Name string `json:"name"`
		// Some servers wrap in a header sub-object.
		Header *struct {
			URN  string `json:"urn"`
			Name string `json:"name"`
		} `json:"header,omitempty"`
	}
	if err := json.Unmarshal(createRespBody, &createResult); err == nil {
		if createResult.Header != nil && createResult.Header.URN != "" {
			noteURNStr = createResult.Header.URN
		} else if createResult.URN != "" {
			noteURNStr = createResult.URN
		}
	}

	// ── POST /v1/notes/{urn}/content (only for non-empty files) ───────────────
	if len(lines) > 0 {
		authorURN := core.AnonURN().String()
		if cfg.Admin.AdminOwnerURN != "" {
			authorURN = cfg.Admin.AdminOwnerURN
		}

		contentBody := struct {
			Content   string `json:"content"`
			AuthorURN string `json:"author_urn,omitempty"`
		}{
			Content:   content,
			AuthorURN: authorURN,
		}

		contentBodyBytes, err := json.Marshal(contentBody)
		if err != nil {
			return fmt.Errorf("marshal content request: %w", err)
		}

		contentURL := fmt.Sprintf("%s/v1/notes/%s/content", httpAddr, percentEncodeURN(noteURNStr))
		contentResp, err := http.Post(contentURL, "application/json", bytes.NewReader(contentBodyBytes)) //nolint:noctx
		if err != nil {
			return fmt.Errorf("POST %s: %w", contentURL, err)
		}
		defer contentResp.Body.Close()

		contentRespBody, _ := io.ReadAll(contentResp.Body)
		if contentResp.StatusCode >= 400 {
			var errBody struct {
				Error string `json:"error"`
			}
			_ = json.Unmarshal(contentRespBody, &errBody)
			msg := errBody.Error
			if msg == "" {
				msg = strings.TrimSpace(string(contentRespBody))
			}
			return fmt.Errorf("set note content (%d): %s", contentResp.StatusCode, msg)
		}
	}

	// ── Success output ────────────────────────────────────────────────────────
	noteURL := fmt.Sprintf("%s/n/%s", httpAddr, noteURNStr)

	fmt.Printf("\n  \033[1;32m✓\033[0m  Note created\n")
	fmt.Printf("     name   : %s\n", noteName)
	fmt.Printf("     urn    : %s\n", noteURNStr)
	fmt.Printf("     type   : %s\n", noteType)
	fmt.Printf("     lines  : %d\n", len(lines))
	if addNoteFlags.projectURN != "" {
		fmt.Printf("     project: %s\n", addNoteFlags.projectURN)
	}
	fmt.Printf("     url    : %s\n", noteURL)
	fmt.Printf("     server : %s\n\n", httpAddr)

	// ── Optionally delete the source file ─────────────────────────────────────
	if addNoteFlags.delete {
		if err := os.Remove(absPath); err != nil {
			return fmt.Errorf("delete source file %q: %w", absPath, err)
		}
		fmt.Printf("  \033[1;33m✓\033[0m  Deleted source file: %s\n\n", absPath)
	}

	return nil
}

// runUpdateContent POSTs the full new content to POST /v1/notes/:urn/content.
// The server diffs against the current state and appends only the changed lines
// as a new event — nothing is stored twice.
func runUpdateContent(srcPath, noteURN, content string, cfg *clientconfig.Config) error {
	httpAddr := resolveHTTPBase(cfg, addNoteFlags.addr)

	// Use the persisted admin owner URN from config so burst extraction is not
	// skipped by the anon guard in core.ExtractBursts. Fall back to anon only
	// when the config has not been initialised yet.
	authorURN := core.AnonURN().String()
	if cfg.Admin.AdminOwnerURN != "" {
		authorURN = cfg.Admin.AdminOwnerURN
	}

	body := struct {
		Content   string `json:"content"`
		AuthorURN string `json:"author_urn,omitempty"`
	}{
		Content:   content,
		AuthorURN: authorURN,
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/v1/notes/%s/content", httpAddr, percentEncodeURN(noteURN))

	resp, err := http.Post(url, "application/json", bytes.NewReader(bodyBytes)) //nolint:noctx
	if err != nil {
		return fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	var result struct {
		Sequence int    `json:"sequence"`
		Changed  bool   `json:"changed"`
		NoteURN  string `json:"note_urn"`
		Error    string `json:"error,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return fmt.Errorf("server error (%d): %s", resp.StatusCode, result.Error)
	}

	lines := splitContentLines(content)

	updateNoteURL := fmt.Sprintf("%s/n/%s", httpAddr, noteURN)

	if !result.Changed {
		fmt.Printf("\n  \033[1;33m–\033[0m  No changes detected\n")
		fmt.Printf("     urn      : %s\n", noteURN)
		fmt.Printf("     sequence : %d (unchanged)\n", result.Sequence)
		fmt.Printf("     url      : %s\n", updateNoteURL)
		fmt.Printf("     server   : %s\n\n", httpAddr)
	} else {
		fmt.Printf("\n  \033[1;32m✓\033[0m  Note updated\n")
		fmt.Printf("     urn      : %s\n", noteURN)
		fmt.Printf("     sequence : %d\n", result.Sequence)
		fmt.Printf("     lines    : %d\n", len(lines))
		fmt.Printf("     url      : %s\n", updateNoteURL)
		fmt.Printf("     server   : %s\n\n", httpAddr)
	}

	if addNoteFlags.delete {
		if err := os.Remove(srcPath); err != nil {
			return fmt.Errorf("delete source file %q: %w", srcPath, err)
		}
		fmt.Printf("  \033[1;33m✓\033[0m  Deleted source file: %s\n\n", srcPath)
	}

	return nil
}

// resolveHTTPBase returns the HTTP base URL to use for requests.
// Resolution order: --addr flag > config server.http_addr > default.
func resolveHTTPBase(cfg *clientconfig.Config, addrOverride string) string {
	addr := cfg.Server.HTTPAddr
	if addrOverride != "" {
		addr = addrOverride
	}
	if addr == "" {
		return "http://127.0.0.1:7430"
	}
	// Already a full URL — return as-is (trimmed).
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return strings.TrimRight(addr, "/")
	}
	// ":port" → "http://127.0.0.1:port"
	if strings.HasPrefix(addr, ":") {
		return "http://127.0.0.1" + addr
	}
	return "http://" + addr
}

// percentEncodeURN replaces ':' with '%3A' for safe use in URL path segments.
func percentEncodeURN(urn string) string {
	return strings.ReplaceAll(urn, ":", "%3A")
}

// splitContentLines splits content into lines, returning an empty slice for
// empty content rather than a one-element slice containing "".
func splitContentLines(content string) []string {
	if content == "" {
		return []string{}
	}
	return strings.Split(content, "\n")
}

// readLines reads a file and returns its lines with trailing newlines stripped.
// An empty file returns a nil slice (no event will be appended).
// Kept for compatibility with any future callers; new code uses splitContentLines.
func readLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// Trim trailing blank lines so we don't pad the note with empty content.
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	return lines, nil
}
