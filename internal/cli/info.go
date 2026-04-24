package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zebaqui/notx-engine/core"
)

var infoCmd = &cobra.Command{
	Use:   "info <path-to-file.notx>",
	Short: "Analyze and display information about a .notx document",
	Long:  "Display detailed information and analysis about a .notx document file.",
	Args:  cobra.ExactArgs(1),
	RunE:  runInfo,
}

func runInfo(cmd *cobra.Command, args []string) error {
	filePath := args[0]

	note, err := core.NewNoteFromFile(filePath)
	if err != nil {
		return fmt.Errorf("could not parse file: %w", err)
	}

	printNoteAnalysis(note, filePath)
	return nil
}

func printNoteAnalysis(note *core.Note, filePath string) {
	const (
		bold  = "\033[1m"
		dim   = "\033[2m"
		cyan  = "\033[36m"
		green = "\033[32m"
		reset = "\033[0m"
	)

	fmt.Println()
	fmt.Printf("  %s%s%s\n", bold, filePath, reset)
	fmt.Printf("  %s%s%s\n\n", dim, strings.Repeat("─", 60), reset)

	// Identity
	fmt.Printf("  %surn%s        %s%s%s\n", dim, reset, cyan, note.URN.String(), reset)
	fmt.Printf("  %sname%s       %s\n", dim, reset, note.Name)

	noteType := "normal"
	if note.NoteType == core.NoteTypeSecure {
		noteType = "secure"
	}
	fmt.Printf("  %stype%s       %s\n", dim, reset, noteType)

	// Timestamps
	if !note.CreatedAt.IsZero() {
		fmt.Printf("  %screated%s    %s\n", dim, reset, note.CreatedAt.UTC().Format("2006-01-02 15:04:05 UTC"))
	}
	if !note.UpdatedAt.IsZero() {
		fmt.Printf("  %supdated%s    %s\n", dim, reset, note.UpdatedAt.UTC().Format("2006-01-02 15:04:05 UTC"))
	}

	// Optional associations
	if note.ProjectURN != nil {
		fmt.Printf("  %sproject%s    %s\n", dim, reset, note.ProjectURN.String())
	}
	if note.FolderURN != nil {
		fmt.Printf("  %sfolder%s     %s\n", dim, reset, note.FolderURN.String())
	}
	if note.ParentURN != nil {
		fmt.Printf("  %sparent%s     %s\n", dim, reset, note.ParentURN.String())
	}
	if note.SnipType != nil {
		fmt.Printf("  %ssnip_type%s  %s\n", dim, reset, *note.SnipType)
	}

	// Event stream stats
	events := note.Events()
	fmt.Printf("  %sevents%s     %s%d%s\n", dim, reset, green, len(events), reset)
	fmt.Printf("  %shead_seq%s   %d\n", dim, reset, note.HeadSequence())

	// Content snapshot
	content := note.Content()
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if content == "" {
		lines = nil
	}
	fmt.Printf("  %slines%s      %d\n", dim, reset, len(lines))

	// Node links
	if len(note.NodeLinks) > 0 {
		fmt.Printf("  %snode_links%s\n", dim, reset)
		for k, v := range note.NodeLinks {
			fmt.Printf("    %s%-20s%s %s\n", dim, k, reset, v.String())
		}
	}

	fmt.Println()

	// Content preview (first 10 lines)
	if len(lines) > 0 {
		fmt.Printf("  %s── content preview ──────────────────────────────────────%s\n", dim, reset)
		limit := len(lines)
		truncated := false
		if limit > 10 {
			limit = 10
			truncated = true
		}
		for i := 0; i < limit; i++ {
			fmt.Printf("  %s%3d%s  %s\n", dim, i+1, reset, lines[i])
		}
		if truncated {
			fmt.Printf("  %s     … (%d more lines)%s\n", dim, len(lines)-10, reset)
		}
		fmt.Println()
	}
}
