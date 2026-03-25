package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/zebaqui/notx-engine/core"
)

// DisplayAnalysis displays a detailed analysis of a .notx document
func DisplayAnalysis(note *core.Note, filePath string) {
	fmt.Println(strings.Repeat("=", 80))
	fmt.Println("NOTX DOCUMENT ANALYSIS")
	fmt.Println(strings.Repeat("=", 80))
	fmt.Println()

	// Basic metadata
	fmt.Println("METADATA")
	fmt.Println(strings.Repeat("-", 80))
	fmt.Printf("  File Path:      %s\n", filePath)
	fmt.Printf("  Note URN:       %s\n", note.URN.String())
	fmt.Printf("  Note Name:      %s\n", note.Name)
	fmt.Printf("  Created At:     %s\n", note.CreatedAt.Format(time.RFC3339))
	fmt.Printf("  Updated At:     %s\n", note.UpdatedAt.Format(time.RFC3339))
	fmt.Printf("  Soft Deleted:   %v\n", note.Deleted)

	if note.ProjectURN != nil {
		fmt.Printf("  Project URN:    %s\n", note.ProjectURN.String())
	}
	if note.FolderURN != nil {
		fmt.Printf("  Folder URN:     %s\n", note.FolderURN.String())
	}
	if note.ParentURN != nil {
		fmt.Printf("  Parent URN:     %s\n", note.ParentURN.String())
	}

	if len(note.NodeLinks) > 0 {
		fmt.Println("  Node Links:")
		for label, urn := range note.NodeLinks {
			fmt.Printf("    - %s: %s\n", label, urn.String())
		}
	}
	fmt.Println()

	// Document statistics
	fmt.Println("STATISTICS")
	fmt.Println(strings.Repeat("-", 80))
	fmt.Printf("  Total Events:     %d\n", note.EventCount())
	fmt.Printf("  Head Sequence:    %d\n", note.HeadSequence())

	lines, _ := note.LinesAt(note.HeadSequence())
	fmt.Printf("  Current Lines:    %d\n", len(lines))
	fmt.Printf("  Current Content Size: %d bytes\n", len(note.Content()))

	snapshots := note.Snapshots()
	fmt.Printf("  Snapshots:        %d\n", len(snapshots))

	authors := note.AuthorURNs()
	fmt.Printf("  Unique Authors:   %d\n", len(authors))
	fmt.Println()

	// Authors
	if len(authors) > 0 {
		fmt.Println("AUTHORS")
		fmt.Println(strings.Repeat("-", 80))
		for i, author := range authors {
			fmt.Printf("  %d. %s", i+1, author.String())
			if author.IsAnon() {
				fmt.Print(" (anonymous)")
			}
			fmt.Println()
		}
		fmt.Println()
	}

	// Event history
	fmt.Println("EVENT HISTORY")
	fmt.Println(strings.Repeat("-", 80))
	history := note.History()
	for _, entry := range history {
		fmt.Printf("  Event #%d @ %s (by %s)\n",
			entry.Sequence,
			entry.CreatedAt.Format("2006-01-02 15:04:05"),
			shortenURN(entry.AuthorURN.String()),
		)
		if entry.Label != "" {
			fmt.Printf("    Label: %s\n", entry.Label)
		}
		fmt.Printf("    Changes: %d line(s)\n", len(entry.Entries))

		for _, le := range entry.Entries {
			opStr := operationString(le.Op)
			if le.Op == core.LineOpSet || le.Op == core.LineOpSetEmpty {
				if le.Content == "" && le.Op == core.LineOpSetEmpty {
					fmt.Printf("      Line %d: %s (empty)\n", le.LineNumber, opStr)
				} else {
					preview := le.Content
					if len(preview) > 50 {
						preview = preview[:50] + "..."
					}
					fmt.Printf("      Line %d: %s %q\n", le.LineNumber, opStr, preview)
				}
			} else {
				fmt.Printf("      Line %d: %s\n", le.LineNumber, opStr)
			}
		}
	}
	fmt.Println()

	// Snapshots
	if len(snapshots) > 0 {
		fmt.Println("SNAPSHOTS")
		fmt.Println(strings.Repeat("-", 80))
		for _, snap := range snapshots {
			fmt.Printf("  Snapshot @ Seq %d (%s)\n",
				snap.Sequence,
				snap.CreatedAt.Format("2006-01-02 15:04:05"),
			)
			fmt.Printf("    Lines: %d\n", snap.LineCount())
		}
		fmt.Println()
	}

	// Current content
	fmt.Println("CURRENT CONTENT")
	fmt.Println(strings.Repeat("-", 80))
	content := note.Content()
	if content == "" {
		fmt.Println("  (empty document)")
	} else {
		contentPreview := content
		if len(content) > 500 {
			contentPreview = content[:500] + "\n... (" + fmt.Sprintf("%d bytes total", len(content)) + ")"
		}
		fmt.Println(contentPreview)
	}
	fmt.Println()

	fmt.Println(strings.Repeat("=", 80))
	fmt.Println("Analysis complete.")
	fmt.Println(strings.Repeat("=", 80))
}

func shortenURN(urn string) string {
	if len(urn) > 40 {
		return urn[:40] + "..."
	}
	return urn
}

func operationString(op core.LineOp) string {
	switch op {
	case core.LineOpSet:
		return "SET"
	case core.LineOpSetEmpty:
		return "EMPTY"
	case core.LineOpDelete:
		return "DELETE"
	default:
		return "UNKNOWN"
	}
}
