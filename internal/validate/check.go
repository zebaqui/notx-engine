package validate

import (
	"fmt"
	"strings"
	"time"

	"github.com/zebaqui/notx-engine/core"
)

// CheckResult represents the result of a single validation check
type CheckResult struct {
	Name   string // Name of the check
	Passed bool   // Whether the check passed
	Error  string // Error message if failed, empty if passed
}

// ValidationReport contains all check results for a document
type ValidationReport struct {
	FilePath     string
	Passed       bool
	TotalChecks  int
	PassedChecks int
	FailedChecks int
	Checks       []CheckResult
}

// Validate performs comprehensive validation on a parsed Note
func Validate(note *core.Note, filePath string) ValidationReport {
	report := ValidationReport{
		FilePath: filePath,
		Checks:   []CheckResult{},
	}

	// Run all validation checks
	report.addCheck(checkDocumentLoaded(note))
	report.addCheck(checkURNFormat(note))
	report.addCheck(checkBasicMetadata(note))
	report.addCheck(checkSequentialEvents(note))
	report.addCheck(checkEventTimestamps(note))
	report.addCheck(checkEventConsistency(note))
	report.addCheck(checkHeadSequence(note))

	// Calculate summary
	report.TotalChecks = len(report.Checks)
	for _, check := range report.Checks {
		if check.Passed {
			report.PassedChecks++
		} else {
			report.FailedChecks++
		}
	}
	report.Passed = report.FailedChecks == 0

	return report
}

func (r *ValidationReport) addCheck(check CheckResult) {
	r.Checks = append(r.Checks, check)
}

// Individual check functions

func checkDocumentLoaded(note *core.Note) CheckResult {
	if note == nil {
		return CheckResult{
			Name:   "Document Loaded",
			Passed: false,
			Error:  "document is nil",
		}
	}
	return CheckResult{
		Name:   "Document Loaded",
		Passed: true,
	}
}

func checkURNFormat(note *core.Note) CheckResult {
	check := CheckResult{Name: "URN Format Valid"}

	urnStr := note.URN.String()
	if !strings.HasPrefix(urnStr, "notx:note:") {
		check.Error = fmt.Sprintf("invalid note URN format: %s", urnStr)
		return check
	}

	check.Passed = true
	return check
}

func checkBasicMetadata(note *core.Note) CheckResult {
	check := CheckResult{Name: "Basic Metadata Present"}

	if note.Name == "" {
		check.Error = "note name is empty"
		return check
	}

	if note.CreatedAt.IsZero() {
		check.Error = "created_at is not set"
		return check
	}

	if note.UpdatedAt.IsZero() {
		check.Error = "updated_at is not set"
		return check
	}

	check.Passed = true
	return check
}

func checkSequentialEvents(note *core.Note) CheckResult {
	check := CheckResult{Name: "Sequential Event Numbers"}

	history := note.History()
	if len(history) == 0 {
		// Empty document is valid
		check.Passed = true
		return check
	}

	for i, entry := range history {
		expectedSeq := i + 1
		if entry.Sequence != expectedSeq {
			check.Error = fmt.Sprintf("event sequence gap at position %d: expected %d, got %d", i, expectedSeq, entry.Sequence)
			return check
		}
	}

	check.Passed = true
	return check
}

func checkEventTimestamps(note *core.Note) CheckResult {
	check := CheckResult{Name: "Event Timestamps Valid"}

	history := note.History()
	var prevTime time.Time

	for i, entry := range history {
		if entry.CreatedAt.IsZero() {
			check.Error = fmt.Sprintf("event %d has invalid timestamp", entry.Sequence)
			return check
		}

		if i > 0 && entry.CreatedAt.Before(prevTime) {
			check.Error = fmt.Sprintf("event timestamps not monotonic: event %d is before event %d",
				entry.Sequence, history[i-1].Sequence)
			return check
		}

		prevTime = entry.CreatedAt
	}

	check.Passed = true
	return check
}

func checkEventConsistency(note *core.Note) CheckResult {
	check := CheckResult{Name: "Event Content Consistency"}

	history := note.History()
	for _, entry := range history {
		authorStr := entry.AuthorURN.String()
		if !strings.HasPrefix(authorStr, "notx:usr:") {
			check.Error = fmt.Sprintf("event %d has invalid author URN: %s", entry.Sequence, authorStr)
			return check
		}

		if len(entry.Entries) == 0 {
			check.Error = fmt.Sprintf("event %d has no line entries", entry.Sequence)
			return check
		}

		// Check line entries are valid
		for _, le := range entry.Entries {
			if le.Op == 0 && le.LineNumber == 0 {
				check.Error = fmt.Sprintf("event %d has invalid line operation", entry.Sequence)
				return check
			}
		}
	}

	check.Passed = true
	return check
}

func checkHeadSequence(note *core.Note) CheckResult {
	check := CheckResult{Name: "Head Sequence Matches"}

	history := note.History()
	expectedHead := len(history)
	actualHead := note.HeadSequence()

	if actualHead != expectedHead {
		check.Error = fmt.Sprintf("head sequence mismatch: expected %d, got %d", expectedHead, actualHead)
		return check
	}

	check.Passed = true
	return check
}
