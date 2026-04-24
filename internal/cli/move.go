package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/zebaqui/notx-engine/internal/clientconfig"
)

var moveCmd = &cobra.Command{
	Use:   "move <note-urn>",
	Short: "Assign a note to a project (and optionally a folder)",
	Long: `Assigns an existing note to a project, enabling context burst candidate
detection with other notes in the same project.

When a note is moved into a project, the server automatically backfills all
existing bursts with the new project URN and runs candidate detection against
the project's existing burst pool. New candidates appear in the review queue
within a few seconds.

The note's content is NOT re-sent — the server backfills from the bursts that
were already extracted from previous events. If the note was created before
the context pipeline was active (e.g. with an anon author), use:

  notx add <file> --urn <note-urn> --project <proj-urn>

to push a content update, which will trigger fresh burst extraction.

Use CLEAR as the project URN to remove the note from its current project.

Examples:
  notx move notx:note:… --project notx:proj:…
  notx move notx:note:… --project notx:proj:… --folder notx:folder:…
  notx move notx:note:… --project CLEAR`,
	Args: cobra.ExactArgs(1),
	RunE: runMove,
}

var moveFlags struct {
	projectURN string
	folderURN  string
}

func init() {
	f := moveCmd.Flags()
	f.StringVar(&moveFlags.projectURN, "project", "",
		`project URN to assign the note to (required, or "CLEAR" to remove)`)
	f.StringVar(&moveFlags.folderURN, "folder", "",
		"folder URN to assign the note to (optional)")

	rootCmd.AddCommand(moveCmd)
}

func runMove(cmd *cobra.Command, args []string) error {
	noteURN := args[0]

	if moveFlags.projectURN == "" {
		return fmt.Errorf("--project is required (use CLEAR to remove the project assignment)")
	}

	cfg, err := clientconfig.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	base := resolveHTTPBase(cfg, "")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// ── GET /v1/notes/{urn} — fetch current state ─────────────────────────────
	getURL := fmt.Sprintf("%s/v1/notes/%s", base, percentEncodeURN(noteURN))
	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, getURL, nil)
	if err != nil {
		return fmt.Errorf("build GET request: %w", err)
	}

	getResp, err := http.DefaultClient.Do(getReq)
	if err != nil {
		return fmt.Errorf("GET %s: %w", getURL, err)
	}
	defer getResp.Body.Close()

	getBody, _ := io.ReadAll(getResp.Body)
	if getResp.StatusCode >= 400 {
		return fmt.Errorf("fetch note (%d): %s", getResp.StatusCode, strings.TrimSpace(string(getBody)))
	}

	var noteState struct {
		Header struct {
			Name       string `json:"name"`
			ProjectURN string `json:"project_urn"`
		} `json:"header"`
	}
	if err := json.Unmarshal(getBody, &noteState); err != nil {
		return fmt.Errorf("decode note response: %w", err)
	}

	oldProject := noteState.Header.ProjectURN
	if oldProject == "" {
		oldProject = "(none)"
	}

	clearing := moveFlags.projectURN == "CLEAR"

	// ── PATCH /v1/notes/{urn} — update project/folder ─────────────────────────
	patchPayload := make(map[string]interface{})
	if clearing {
		patchPayload["project_urn"] = ""
	} else {
		patchPayload["project_urn"] = moveFlags.projectURN
	}
	if moveFlags.folderURN != "" {
		patchPayload["folder_urn"] = moveFlags.folderURN
	}

	patchBytes, err := json.Marshal(patchPayload)
	if err != nil {
		return fmt.Errorf("marshal patch request: %w", err)
	}

	patchURL := fmt.Sprintf("%s/v1/notes/%s", base, percentEncodeURN(noteURN))
	patchReq, err := http.NewRequestWithContext(ctx, http.MethodPatch, patchURL, bytes.NewReader(patchBytes))
	if err != nil {
		return fmt.Errorf("build PATCH request: %w", err)
	}
	patchReq.Header.Set("Content-Type", "application/json")

	patchResp, err := http.DefaultClient.Do(patchReq)
	if err != nil {
		return fmt.Errorf("PATCH %s: %w", patchURL, err)
	}
	defer patchResp.Body.Close()

	patchBody, _ := io.ReadAll(patchResp.Body)
	if patchResp.StatusCode >= 400 {
		return fmt.Errorf("update note (%d): %s", patchResp.StatusCode, strings.TrimSpace(string(patchBody)))
	}

	var patchResult struct {
		Header *struct {
			Name string `json:"name"`
		} `json:"header,omitempty"`
		Name string `json:"name,omitempty"`
	}
	_ = json.Unmarshal(patchBody, &patchResult)

	noteName := noteState.Header.Name
	if patchResult.Header != nil && patchResult.Header.Name != "" {
		noteName = patchResult.Header.Name
	} else if patchResult.Name != "" {
		noteName = patchResult.Name
	}

	// ── Success output ────────────────────────────────────────────────────────
	fmt.Printf("\n  \033[1;32m✓\033[0m  Note moved\n")
	fmt.Printf("     urn     : %s\n", noteURN)
	fmt.Printf("     name    : %s\n", noteName)
	if clearing {
		fmt.Printf("     project : %s  →  \033[33m(removed)\033[0m\n", oldProject)
	} else {
		fmt.Printf("     project : %s  →  %s\n", oldProject, moveFlags.projectURN)
		if moveFlags.folderURN != "" {
			fmt.Printf("     folder  : %s\n", moveFlags.folderURN)
		}
		fmt.Printf("     context : \033[2mbackfilling bursts into project (background)…\033[0m\n")
	}
	fmt.Println()

	return nil
}
