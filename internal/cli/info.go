package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/zebaqui/notx-engine/core"
	"github.com/zebaqui/notx-engine/internal/tui"
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

	tui.DisplayAnalysis(note, filePath)
	return nil
}
