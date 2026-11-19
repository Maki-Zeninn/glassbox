// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package security

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// PolicyManifest is the compact, fixture-driven description of a policy test
// suite.  It is loaded from a JSON file that lives alongside the fixture
// sources it references.  Tests run offline and deterministically: no network
// calls are made and no arbitrary code is executed from policy files.
type PolicyManifest struct {
	// Version identifies the manifest format; currently "1".
	Version string `json:"version"`
	// PolicyFile is an optional path to a packaged or repository-local policy
	// file whose suppressions are loaded before running cases.
	PolicyFile string `json:"policy_file,omitempty"`
	// Cases is the ordered list of test scenarios.
	Cases []PolicyTestCase `json:"cases"`
}

// PolicyTestCase is a single test scenario that asserts expected findings,
// severities, suppressions, and exit behaviour for a given source fixture.
type PolicyTestCase struct {
	// Name is a short, human-readable identifier shown in failure output.
	Name string `json:"name"`
	// Fixture is the path (relative to the manifest's directory, or absolute)
	// to a source file or directory that the detector will analyse.
	Fixture string `json:"fixture"`
	// ExpectedFindings lists security findings that must appear in the active
	// output.  An empty slice means no findings are required.
	ExpectedFindings []ExpectedFinding `json:"expected_findings,omitempty"`
	// ExpectedSuppressed lists finding titles expected to be suppressed rather
	// than active.  Useful for validating that suppression records work.
	ExpectedSuppressed []string `json:"expected_suppressed,omitempty"`
	// ExitZero asserts that the case produces zero active findings.
	// If true and active findings are present the case fails with a diff.
	ExitZero bool `json:"exit_zero"`
	// Suppressions is the list of suppression records applied during this case.
	// Expired suppressions are flagged as errors rather than silently ignored.
	Suppressions []*SuppressionRecord `json:"suppressions,omitempty"`
}

// ExpectedFinding describes a finding that must appear in the detector output.
// Fields left empty are not matched; at least Title or Severity must be set.
type ExpectedFinding struct {
	Title    string      `json:"title,omitempty"`
	Severity Severity    `json:"severity,omitempty"`
	Type     FindingType `json:"type,omitempty"`
}

// PolicyCaseResult is the outcome of a single test case.
type PolicyCaseResult struct {
	// Name mirrors PolicyTestCase.Name.
	Name string
	// Passed is true when all assertions were satisfied and no errors occurred.
	Passed bool
	// Diff is a human-readable description of assertion failures.
	Diff string
	// Errors holds non-fatal collector errors such as expired suppressions or
	// missing fixture paths.
	Errors []string
}

// PolicyTestReport summarises the results of running a full PolicyManifest.
type PolicyTestReport struct {
	Pass    int
	Fail    int
	Results []PolicyCaseResult
}

// RunPolicyTests executes every case in manifest using fixtureDir as the base
// directory for relative fixture paths.  The detector runs offline and
// deterministically; no network calls are made.
func RunPolicyTests(manifest PolicyManifest, fixtureDir string) PolicyTestReport {
	var report PolicyTestReport
	for _, tc := range manifest.Cases {
		result := runPolicyCase(tc, fixtureDir)
		report.Results = append(report.Results, result)
		if result.Passed {
			report.Pass++
		} else {
			report.Fail++
		}
	}
	return report
}

func runPolicyCase(tc PolicyTestCase, fixtureDir string) PolicyCaseResult {
	result := PolicyCaseResult{Name: tc.Name}

	// Build the suppression registry, flagging any expired records.
	registry := NewSuppressionRegistry()
	now := time.Now().UTC()
	for _, s := range tc.Suppressions {
		if !s.ExpiresAt.IsZero() && s.ExpiresAt.Before(now) {
			result.Errors = append(result.Errors,
				fmt.Sprintf("suppression %q expired at %s — renew or remove it",
					s.Fingerprint, s.ExpiresAt.Format(time.RFC3339)),
			)
			continue
		}
		if err := registry.Add(s); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("suppression add error: %v", err))
		}
	}

	// Resolve the fixture path.
	fixturePath := tc.Fixture
	if fixtureDir != "" && !strings.HasPrefix(fixturePath, "/") {
		fixturePath = fixtureDir + "/" + fixturePath
	}

	// Run the detector.  ScanSourcePath updates d.findings as a side-effect.
	d := NewDetectorWithSuppression(registry, fixturePath)
	if _, err := d.ScanSourcePath(fixturePath, nil); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("fixture scan error: %v", err))
	}

	withSuppression := d.GetFindingsWithSuppression()
	active := withSuppression.ActiveFindings
	suppressed := withSuppression.SuppressedFindings

	// Assert expected active findings are present.
	var diffLines []string
	for _, expected := range tc.ExpectedFindings {
		found := false
		for _, f := range active {
			if matchesExpected(f, expected) {
				found = true
				break
			}
		}
		if !found {
			diffLines = append(diffLines,
				fmt.Sprintf("  MISSING finding: [%s] %q (%s)", expected.Severity, expected.Title, expected.Type),
			)
		}
	}

	// Assert expected suppressed findings are present in the suppressed set.
	for _, title := range tc.ExpectedSuppressed {
		found := false
		for _, sf := range suppressed {
			if sf.Finding.Title == title {
				found = true
				break
			}
		}
		if !found {
			diffLines = append(diffLines, fmt.Sprintf("  MISSING suppressed finding: %q", title))
		}
	}

	// Assert exit-zero expectation.
	if tc.ExitZero {
		for _, f := range active {
			diffLines = append(diffLines,
				fmt.Sprintf("  UNEXPECTED active finding: [%s] %q", f.Severity, f.Title),
			)
		}
	}

	if len(diffLines) == 0 && len(result.Errors) == 0 {
		result.Passed = true
	} else {
		result.Diff = strings.Join(diffLines, "\n")
	}
	return result
}

// matchesExpected returns true when f satisfies every non-empty field of expected.
func matchesExpected(f Finding, expected ExpectedFinding) bool {
	if expected.Title != "" && f.Title != expected.Title {
		return false
	}
	if expected.Severity != "" && f.Severity != expected.Severity {
		return false
	}
	if expected.Type != "" && f.Type != expected.Type {
		return false
	}
	return true
}

// FormatReport returns a human-readable summary of a PolicyTestReport.
// A changed finding or severity fails with a useful diff so reviewers can
// see exactly what assertion was violated.
func FormatReport(report PolicyTestReport) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Policy Tests: %d passed, %d failed\n", report.Pass, report.Fail))
	sb.WriteString(strings.Repeat("─", 50) + "\n")
	for _, r := range report.Results {
		if r.Passed {
			sb.WriteString(fmt.Sprintf("  PASS  %s\n", r.Name))
		} else {
			sb.WriteString(fmt.Sprintf("  FAIL  %s\n", r.Name))
			if r.Diff != "" {
				sb.WriteString(r.Diff + "\n")
			}
			for _, e := range r.Errors {
				sb.WriteString(fmt.Sprintf("    ERROR: %s\n", e))
			}
		}
	}
	return sb.String()
}

// FormatReportJSON returns the report as indented JSON suitable for CI
// consumption or structured logging.
func FormatReportJSON(report PolicyTestReport) ([]byte, error) {
	type jsonCase struct {
		Name   string   `json:"name"`
		Passed bool     `json:"passed"`
		Diff   string   `json:"diff,omitempty"`
		Errors []string `json:"errors,omitempty"`
	}
	type jsonReport struct {
		Pass    int        `json:"pass"`
		Fail    int        `json:"fail"`
		Results []jsonCase `json:"results"`
	}
	out := jsonReport{Pass: report.Pass, Fail: report.Fail}
	for _, r := range report.Results {
		out.Results = append(out.Results, jsonCase{
			Name:   r.Name,
			Passed: r.Passed,
			Diff:   r.Diff,
			Errors: r.Errors,
		})
	}
	return json.MarshalIndent(out, "", "  ")
}
