// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dotandev/glassbox/internal/errors"
	"github.com/dotandev/glassbox/internal/report"
	"github.com/dotandev/glassbox/internal/sarif"
	"github.com/dotandev/glassbox/internal/security"
	"github.com/dotandev/glassbox/internal/trace"
	"github.com/spf13/cobra"
)

var (
	reportFormat string
	reportOutput string
	reportFile   string
)

// validReportFormats are the accepted --format values for the report command.
var validReportFormats = map[string]bool{
	"html":     true,
	"pdf":      true,
	"html,pdf": true,
	"pdf,html": true,
	"json":     true,
	"text":     true,
	"sarif":    true,
}

var reportCmd = &cobra.Command{
	Use:     "report",
	GroupID: "utility",
	Short:   "Generate debugging reports from traces",
	Long: `Generate HTML, PDF, JSON, or plain-text reports from execution traces.

Reports include:
  - Executive summary with key findings
  - Detailed execution steps and call stacks
  - Contract interaction analytics
  - Risk assessment with detected issues
  - Timeline and event distribution

The --file flag is required and must point to a valid JSON trace file
produced by 'glassbox debug' or 'glassbox trace'.

The --format flag defaults to 'html'. Specify 'html,pdf' to produce both
formats in a single pass.`,
	Example: `  # Generate an HTML report (default)
  glassbox report --file trace.json --output reports/

  # Generate a PDF report
  glassbox report --file trace.json --format pdf --output reports/

  # Generate both HTML and PDF
  glassbox report --file trace.json --format html,pdf --output reports/

  # Generate a JSON report for programmatic consumption
  glassbox report --file trace.json --format json --output reports/

  # Generate a plain-text report for terminals or logs
  glassbox report --file trace.json --format text`,
	PreRunE: validateReportFlags,
	RunE:    reportExec,
}

// validateReportFlags performs all flag validation before any I/O so
// failures surface before the user waits for file reads.
func validateReportFlags(cmd *cobra.Command, args []string) error {
	if reportFile == "" {
		return errors.WrapValidationError(
			"--file is required; provide the path to a JSON trace file\n" +
				"Usage: glassbox report --file <trace.json> [--format html|pdf|json|text|sarif]")
	}

	// Validate --format early so the user gets a clear error before any I/O.
	normalized := strings.ToLower(strings.TrimSpace(reportFormat))
	if !validReportFormats[normalized] {
		valid := "html, pdf, html,pdf, json, text, sarif"
		return errors.WrapValidationError(fmt.Sprintf(
			"invalid --format %q; must be one of: %s", reportFormat, valid,
		))
	}

	return nil
}

func reportExec(cmd *cobra.Command, args []string) error {
	// Verify the trace file exists and is readable before doing anything else.
	if _, statErr := os.Stat(reportFile); os.IsNotExist(statErr) {
		return errors.WrapValidationError(fmt.Sprintf(
			"trace file not found: %q\n"+
				"Provide a trace file exported with 'glassbox debug --json' or 'glassbox trace'",
			reportFile,
		))
	}

	if reportOutput == "" {
		reportOutput = "."
	}

	// Ensure the output directory exists (or can be created).
	if info, err := os.Stat(reportOutput); err == nil && !info.IsDir() {
		return errors.WrapValidationError(fmt.Sprintf(
			"--output %q exists but is not a directory; specify a directory path",
			reportOutput,
		))
	} else if os.IsNotExist(err) {
		if mkErr := os.MkdirAll(reportOutput, 0o755); mkErr != nil {
			return errors.WrapValidationError(fmt.Sprintf(
				"--output %q does not exist and could not be created: %v\n"+
					"  Fix: ensure you have write permissions to the parent directory",
				reportOutput, mkErr,
			))
		}
	}

	traceData, err := os.ReadFile(reportFile)
	if err != nil {
		return errors.WrapValidationError(fmt.Sprintf("failed to read trace file %q: %v", reportFile, err))
	}

	if len(traceData) == 0 {
		return errors.WrapValidationError(fmt.Sprintf(
			"trace file %q is empty; the file must contain valid JSON trace data",
			reportFile,
		))
	}

	executionTrace, err := trace.FromJSON(traceData)
	if err != nil {
		return errors.WrapUnmarshalFailed(err, fmt.Sprintf(
			"failed to parse trace file %q — ensure the file was produced by "+
				"'glassbox debug --json' or 'glassbox trace'",
			reportFile,
		))
	}

	builder := report.NewBuilder("Execution Trace Report")
	builder.WithTransactionHash(executionTrace.TransactionHash)

	// Build summary
	totalSteps := len(executionTrace.States)
	errorCount := countErrors(executionTrace.States)
	successRate := calculateSuccessRate(executionTrace.States)

	duration := executionTrace.EndTime.Sub(executionTrace.StartTime).String()
	builder.SetSummary("success", duration, totalSteps, errorCount, countContracts(executionTrace.States), successRate)

	// Add execution steps
	for i, state := range executionTrace.States {
		op := state.Operation
		if state.ContractID != "" && state.Function != "" {
			op = state.ContractID + "::" + state.Function
		}

		status := "success"
		if state.Error != "" {
			status = "error"
		}

		builder.AddExecutionStep(i, op, status, state.Error)
	}

	// Analyze for findings
	if errorCount > 0 {
		builder.AddKeyFinding(fmt.Sprintf("%d error(s) detected during execution", errorCount))
	}

	contractCount := countContracts(executionTrace.States)
	builder.AddKeyFinding(fmt.Sprintf("%d unique contract(s) called", contractCount))

	// Risk assessment
	riskLevel := assessRisk(executionTrace.States)
	builder.SetRiskAssessment(riskLevel, calculateRiskScore(executionTrace.States))

	// Metadata
	builder.SetMetadata("execution_trace", "1.0.0", map[string]string{
		"generated_by": "glassbox",
		"timestamp":    time.Now().Format(time.RFC3339),
	})

	generatedReport := builder.Build()

	// ── Diagnostic text / JSON formats ─────────────────────────────────────
	diagnosticReport := report.NewDiagnosticReport(executionTrace)

	normalized := strings.ToLower(strings.TrimSpace(reportFormat))
	switch normalized {
	case "sarif":
		// Derive security findings from the execution trace errors and events.
		findings := deriveSecurityFindingsFromTrace(executionTrace)
		sarifData, sarErr := sarif.ExportJSON(findings, sarif.ExportOptions{
			ArtifactURI: reportFile,
		})
		if sarErr != nil {
			return errors.WrapValidationError(fmt.Sprintf("failed to export SARIF: %v", sarErr))
		}
		filename := filepath.Join(reportOutput, "report.sarif")
		if writeErr := os.WriteFile(filename, sarifData, 0o644); writeErr != nil {
			return errors.WrapValidationError(fmt.Sprintf(
				"failed to write SARIF to %q: %v", filename, writeErr,
			))
		}
		fmt.Fprintf(cmd.OutOrStdout(), "[OK] SARIF report generated: %s (%d finding(s))\n",
			filename, len(findings))
		return nil

	case "text":
		filename := filepath.Join(reportOutput, "report.txt")
		if writeErr := os.WriteFile(filename, []byte(diagnosticReport.Text()), 0644); writeErr != nil {
			return errors.WrapValidationError(fmt.Sprintf(
				"failed to write text report to %q: %v", filename, writeErr,
			))
		}
		fmt.Fprintf(cmd.OutOrStdout(), "[OK] Report generated: %s\n", filename)
		return nil

	case "json":
		jsonData, marshalErr := diagnosticReport.JSON()
		if marshalErr != nil {
			return errors.WrapMarshalFailed(marshalErr)
		}
		filename := filepath.Join(reportOutput, "report.json")
		if writeErr := os.WriteFile(filename, jsonData, 0644); writeErr != nil {
			return errors.WrapValidationError(fmt.Sprintf(
				"failed to write JSON report to %q: %v", filename, writeErr,
			))
		}
		fmt.Fprintf(cmd.OutOrStdout(), "[OK] Report generated: %s\n", filename)
		return nil
	}

	// ── HTML / PDF formats via exporter ───────────────────────────────────
	exporter, err := report.NewExporter(reportOutput)
	if err != nil {
		return errors.WrapValidationError(fmt.Sprintf(
			"failed to create report exporter for output directory %q: %v", reportOutput, err,
		))
	}

	var formats []string
	switch normalized {
	case "html":
		formats = []string{"html"}
	case "pdf":
		formats = []string{"pdf"}
	case "html,pdf", "pdf,html":
		formats = []string{"html", "pdf"}
	default:
		formats = []string{"html"} // safe fallback (already validated above)
	}

	results, exportErr := exporter.ExportMultiple(generatedReport, formats)
	if exportErr != nil {
		return errors.WrapValidationError(fmt.Sprintf(
			"report export failed: %v\n"+
				"Ensure the output directory is writable and try again",
			exportErr,
		))
	}

	for format, path := range results {
		fmt.Fprintf(cmd.OutOrStdout(), "[OK] %s report generated: %s\n", strings.ToUpper(string(format)), path)
	}

	return nil
}

func countErrors(states []trace.ExecutionState) int {
	count := 0
	for _, state := range states {
		if state.Error != "" {
			count++
		}
	}
	return count
}

func countContracts(states []trace.ExecutionState) int {
	contracts := make(map[string]bool)
	for _, state := range states {
		if state.ContractID != "" {
			contracts[state.ContractID] = true
		}
	}
	return len(contracts)
}

func calculateSuccessRate(states []trace.ExecutionState) float64 {
	if len(states) == 0 {
		return 100.0
	}

	successful := 0
	for _, state := range states {
		if state.Error == "" {
			successful++
		}
	}

	return (float64(successful) / float64(len(states))) * 100
}

func assessRisk(states []trace.ExecutionState) string {
	errorCount := countErrors(states)

	switch {
	case errorCount >= len(states)/2:
		return "critical"
	case errorCount >= len(states)/4:
		return "high"
	case errorCount > 0:
		return "medium"
	default:
		return "low"
	}
}

func calculateRiskScore(states []trace.ExecutionState) float64 {
	if len(states) == 0 {
		return 0
	}

	errorCount := countErrors(states)
	return (float64(errorCount) / float64(len(states))) * 100
}

func init() {
	reportCmd.Flags().StringVar(&reportFormat, "format", "html", "Output format: text, json, html, pdf, html,pdf, or sarif")
	reportCmd.Flags().StringVar(&reportOutput, "output", ".", "Output directory for reports")
	reportCmd.Flags().StringVar(&reportFile, "file", "", "JSON trace file to analyze (required)")

	_ = reportCmd.RegisterFlagCompletionFunc("format", completeReportFormatFlag)

	rootCmd.AddCommand(reportCmd)
}

// deriveSecurityFindingsFromTrace converts an execution trace into a list of
// Glassbox security findings for SARIF export. Each execution state that
// contains an error is represented as a verified-risk finding. This is a
// best-effort derivation; callers that want richer findings should use
// 'glassbox scan' against the contract source directly.
func deriveSecurityFindingsFromTrace(t *trace.ExecutionTrace) []security.Finding {
	if t == nil {
		return nil
	}
	var findings []security.Finding
	for _, state := range t.States {
		if state.Error == "" {
			continue
		}
		findings = append(findings, security.Finding{
			Type:        security.FindingVerifiedRisk,
			Severity:    security.SeverityMedium,
			Title:       "Execution Error in Trace",
			Description: state.Error,
			Evidence:    state.Operation,
		})
	}
	return findings
}
