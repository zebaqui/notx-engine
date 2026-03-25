package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/zebaqui/notx-engine/core"
	"github.com/zebaqui/notx-engine/internal/tui"
	"github.com/zebaqui/notx-engine/internal/validate"
)

var validateCmd = &cobra.Command{
	Use:   "validate <path-to-file.notx>",
	Short: "Validate a .notx document against the specification",
	Long:  "Validate a .notx document file to ensure it conforms to the specification and report any errors found.",
	Args:  cobra.ExactArgs(1),
	RunE:  runValidate,
}

func runValidate(cmd *cobra.Command, args []string) error {
	filePath := args[0]

	note, err := core.NewNoteFromFile(filePath)
	if err != nil {
		return fmt.Errorf("could not parse file: %w", err)
	}

	report := validate.Validate(note, filePath)
	tui.DisplayValidationReport(report)

	// Exit with appropriate code based on validation result
	if !report.Passed {
		os.Exit(1)
	}

	return nil
}
