package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/zebaqui/notx-engine/internal/clientconfig"
	"github.com/zebaqui/notx-engine/internal/grpcclient"
	pb "github.com/zebaqui/notx-engine/proto"
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

	grpcAddr := cfg.Client.GRPCAddr

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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	notes := conn.Notes()

	// Fetch the current note state so we can display the before/after project.
	getResp, err := notes.GetNote(ctx, &pb.GetNoteRequest{Urn: noteURN})
	if err != nil {
		return fmt.Errorf("fetch note: %w", err)
	}

	oldProject := getResp.Header.GetProjectUrn()
	if oldProject == "" {
		oldProject = "(none)"
	}

	clearing := moveFlags.projectURN == "CLEAR"

	// Build the header and field mask.  We always include project_urn; we
	// add folder_urn to the mask only when the flag was explicitly supplied.
	header := &pb.NoteHeader{}
	maskPaths := []string{"project_urn"}

	if clearing {
		header.ProjectUrn = ""
	} else {
		header.ProjectUrn = moveFlags.projectURN
	}

	if moveFlags.folderURN != "" {
		header.FolderUrn = moveFlags.folderURN
		maskPaths = append(maskPaths, "folder_urn")
	}

	updateResp, err := notes.UpdateNote(ctx, &pb.UpdateNoteRequest{
		Urn:        noteURN,
		Header:     header,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: maskPaths},
	})
	if err != nil {
		return fmt.Errorf("update note: %w", err)
	}

	h := updateResp.GetHeader()

	noteName := ""
	if h != nil {
		noteName = h.GetName()
	}

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
