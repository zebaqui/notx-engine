package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/zebaqui/notx-engine/core"
	"github.com/zebaqui/notx-engine/internal/clientconfig"
	"github.com/zebaqui/notx-engine/internal/grpcclient"
	pb "github.com/zebaqui/notx-engine/proto"
)

var addNoteCmd = &cobra.Command{
	Use:   "add [file]",
	Short: "Create a new note from a file, or update an existing one",
	Long: `Create a new note by sending it to the running notx gRPC server,
or update an existing note's content when --urn is provided.

When --urn is given the file content is diffed against the note's current
state on the server. Only changed lines are written as a new event — the
full document is never re-stored verbatim.

The note name is derived from the file's base name (without extension).
The server address is read from ~/.notx/config.json (client.grpc_addr).
Override it for a single invocation with --addr.

Examples:
  # Create a normal note from meeting-notes.txt
  notx add meeting-notes.txt

  # Push a new version of an existing note
  notx add meeting-notes.txt --urn notx:note:1a9670dd-1a65-481a-ad17-03d77de021e5

  # Create a secure (E2EE) note and delete the source file afterwards
  notx add secrets.txt --secure -d

  # Point at a non-default server for this invocation
  notx add todo.md --addr localhost:9000
`,
	Args: cobra.ExactArgs(1),
	RunE: runAddNote,
}

var addNoteFlags struct {
	addr   string // override client.grpc_addr for this invocation
	urn    string // when set, update an existing note instead of creating
	delete bool
	secure bool
}

func init() {
	f := addNoteCmd.Flags()
	f.StringVar(&addNoteFlags.addr, "addr", "",
		"gRPC server address to dial (overrides config client.grpc_addr)")
	f.StringVar(&addNoteFlags.urn, "urn", "",
		"URN of an existing note to update (skips creation, diffs and appends an event)")
	f.BoolVarP(&addNoteFlags.delete, "delete", "d", false,
		"Delete the source file after successfully creating the note")
	f.BoolVar(&addNoteFlags.secure, "secure", false,
		"Mark the note as secure (end-to-end encrypted)")

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

	// ── If --urn is set, push a content update via HTTP ───────────────────────
	if addNoteFlags.urn != "" {
		return runUpdateContent(absPath, addNoteFlags.urn, content, cfg)
	}

	// ── Derive note name from filename ────────────────────────────────────────
	base := filepath.Base(absPath)
	ext := filepath.Ext(base)
	noteName := strings.TrimSuffix(base, ext)
	if noteName == "" {
		noteName = base
	}

	lines := splitContentLines(content)

	// --addr flag overrides the config value for this invocation.
	grpcAddr := cfg.Client.GRPCAddr
	if addNoteFlags.addr != "" {
		grpcAddr = addNoteFlags.addr
	}

	// ── Dial the gRPC server ──────────────────────────────────────────────────
	dialOpts := grpcclient.Options{
		Addr:     grpcAddr,
		Insecure: cfg.Client.Insecure && !cfg.TLSEnabled(),
	}
	if cfg.TLSEnabled() {
		dialOpts.CertFile = cfg.TLS.CertFile
		dialOpts.KeyFile = cfg.TLS.KeyFile
	}
	if cfg.TLS.CAFile != "" {
		dialOpts.CAFile = cfg.TLS.CAFile
	}

	conn, err := grpcclient.Dial(dialOpts)
	if err != nil {
		return fmt.Errorf("dial gRPC server at %s: %w", grpcAddr, err)
	}
	defer conn.Close()

	client := conn.Notes()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// ── Build and send CreateNote ─────────────────────────────────────────────
	noteURNStr := core.NewURN(core.ObjectTypeNote).String()

	noteType := pb.NoteType_NOTE_TYPE_NORMAL
	if addNoteFlags.secure {
		noteType = pb.NoteType_NOTE_TYPE_SECURE
	}

	now := timestamppb.Now()

	createResp, err := client.CreateNote(ctx, &pb.CreateNoteRequest{
		Header: &pb.NoteHeader{
			Urn:       noteURNStr,
			Name:      noteName,
			NoteType:  noteType,
			CreatedAt: now,
			UpdatedAt: now,
		},
	})
	if err != nil {
		return fmt.Errorf("create note on server: %w", err)
	}

	// ── Append initial content event (only for non-empty files) ───────────────
	if len(lines) > 0 {
		entries := make([]*pb.LineEntry, 0, len(lines))
		for i, line := range lines {
			lineNum := int32(i + 1)
			if line == "" {
				entries = append(entries, &pb.LineEntry{
					Op:         1, // LineOpSetEmpty
					LineNumber: lineNum,
				})
			} else {
				entries = append(entries, &pb.LineEntry{
					Op:         0, // LineOpSet
					LineNumber: lineNum,
					Content:    line,
				})
			}
		}

		eventURNStr := core.NewURN(core.ObjectTypeEvent).String()
		authorURNStr := core.AnonURN().String()

		_, err = client.AppendEvent(ctx, &pb.AppendEventRequest{
			Event: &pb.Event{
				Urn:       eventURNStr,
				NoteUrn:   noteURNStr,
				Sequence:  1,
				AuthorUrn: authorURNStr,
				CreatedAt: now,
				Entries:   entries,
			},
		})
		if err != nil {
			return fmt.Errorf("append content event: %w", err)
		}
	}

	// ── Success output ────────────────────────────────────────────────────────
	typeLabel := "normal"
	if addNoteFlags.secure {
		typeLabel = "secure"
	}

	urn := noteURNStr
	if createResp.Header != nil {
		urn = createResp.Header.Urn
	}

	fmt.Printf("\n  \033[1;32m✓\033[0m  Note created\n")
	fmt.Printf("     name   : %s\n", noteName)
	fmt.Printf("     urn    : %s\n", urn)
	fmt.Printf("     type   : %s\n", typeLabel)
	fmt.Printf("     lines  : %d\n", len(lines))
	fmt.Printf("     server : %s\n\n", grpcAddr)

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
	httpAddr := cfg.Server.HTTPAddr
	if addNoteFlags.addr != "" {
		// --addr was passed; assume it is the HTTP base when --urn is used.
		httpAddr = addNoteFlags.addr
	}
	// Normalise: strip leading colon so ":4060" → "localhost:4060".
	if strings.HasPrefix(httpAddr, ":") {
		httpAddr = "localhost" + httpAddr
	}

	authorURN := core.AnonURN().String()

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

	url := fmt.Sprintf("http://%s/v1/notes/%s/content",
		httpAddr, percentEncodeURN(noteURN))

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

	if !result.Changed {
		fmt.Printf("\n  \033[1;33m–\033[0m  No changes detected\n")
		fmt.Printf("     urn      : %s\n", noteURN)
		fmt.Printf("     sequence : %d (unchanged)\n", result.Sequence)
		fmt.Printf("     server   : %s\n\n", httpAddr)
	} else {
		fmt.Printf("\n  \033[1;32m✓\033[0m  Note updated\n")
		fmt.Printf("     urn      : %s\n", noteURN)
		fmt.Printf("     sequence : %d\n", result.Sequence)
		fmt.Printf("     lines    : %d\n", len(lines))
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
