package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/zebaqui/notx-engine/core"
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
	printValidationReport(report)

	if !report.Passed {
		os.Exit(1)
	}

	return nil
}

func printValidationReport(report validate.ValidationReport) {
	const (
		bold  = "\033[1m"
		dim   = "\033[2m"
		green = "\033[32m"
		red   = "\033[31m"
		reset = "\033[0m"
	)

	fmt.Println()
	fmt.Printf("  %sValidation Report%s\n", bold, reset)
	fmt.Printf("  %sFile: %s%s\n\n", dim, report.FilePath, reset)

	for _, check := range report.Checks {
		if check.Passed {
			fmt.Printf("  %s✓%s  %s\n", green, reset, check.Name)
		} else {
			fmt.Printf("  %s✗%s  %s\n", red, reset, check.Name)
			if check.Error != "" {
				fmt.Printf("       %s%s%s\n", dim, check.Error, reset)
			}
		}
	}

	fmt.Println()

	if report.Passed {
		fmt.Printf("  %s✓  All %d checks passed%s\n\n", green, report.TotalChecks, reset)
	} else {
		fmt.Printf("  %s✗  %d/%d checks failed%s\n\n", red, report.FailedChecks, report.TotalChecks, reset)
	}
}
