package tui

import (
	"fmt"
	"strings"

	"github.com/zebaqui/notx-engine/internal/validate"
)

// DisplayValidationReport displays the validation report with all checks and results.
func DisplayValidationReport(report validate.ValidationReport) {
	fmt.Println(strings.Repeat("=", 80))
	fmt.Println("NOTX DOCUMENT VALIDATION REPORT")
	fmt.Println(strings.Repeat("=", 80))
	fmt.Println()

	fmt.Println("FILE INFORMATION")
	fmt.Println(strings.Repeat("-", 80))
	fmt.Printf("  File Path: %s\n", report.FilePath)
	fmt.Println()

	fmt.Println("VALIDATION CHECKS")
	fmt.Println(strings.Repeat("-", 80))

	for _, check := range report.Checks {
		status := "[✓]"
		if !check.Passed {
			status = "[✗]"
		}

		fmt.Printf("  %s %s\n", status, check.Name)
		if !check.Passed && check.Error != "" {
			fmt.Printf("      Error: %s\n", check.Error)
		}
	}
	fmt.Println()

	fmt.Println("VALIDATION SUMMARY")
	fmt.Println(strings.Repeat("-", 80))
	fmt.Printf("  Total Checks:  %d\n", report.TotalChecks)
	fmt.Printf("  Passed:        %d\n", report.PassedChecks)
	fmt.Printf("  Failed:        %d\n", report.FailedChecks)

	if report.Passed {
		fmt.Printf("  Status:        VALID\n")
	} else {
		fmt.Printf("  Status:        INVALID\n")
	}
	fmt.Println()

	fmt.Println(strings.Repeat("=", 80))
	if report.Passed {
		fmt.Println("Validation PASSED - Document is valid")
	} else {
		fmt.Println("Validation FAILED - Document has errors")
	}
	fmt.Println(strings.Repeat("=", 80))
}
