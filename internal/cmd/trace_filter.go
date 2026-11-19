// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// trace_filter.go — CLI subcommand: glassbox trace filter
//
// Applies composable filters to a trace file and writes the result as text
// or JSON.  All filter flags are independently optional — omitting every flag
// returns the full trace.  Empty match sets are a successful exit.
//
// Usage:
//
//	glassbox trace filter <trace-file> [flags]
//
// Examples:
//
//	glassbox trace filter tx.json --contract CAAAA
//	glassbox trace filter tx.json --function "transfer.*" --severity error
//	glassbox trace filter tx.json --source-file token.rs --line-min 40 --line-max 80
//	glassbox trace filter tx.json --event-type trap --format json --output filtered.json
//	glassbox trace filter tx.json --contract CAAAA --exclude

package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/dotandev/glassbox/internal/errors"
	"github.com/dotandev/glassbox/internal/trace"
	"github.com/spf13/cobra"
)

var (
	tfContractFlag   string
	tfFunctionFlag   string
	tfEventTypeFlag  string
	tfSeverityFlag   string
	tfSourceFileFlag string
	tfStepMinFlag    int
	tfStepMaxFlag    int
	tfLineMinFlag    int
	tfLineMaxFlag    int
	tfExcludeFlag    bool
	tfFormatFlag     string // "text" (default) or "json"
	tfOutputFlag     string // output file path; stdout when empty
)

var traceFilterCmd = &cobra.Command{
	Use:   "filter <trace-file>",
	Short: "Filter a trace by contract, function, event type, severity, source location, or step range",
	Long: `Apply composable filters to a Glassbox trace file.

Filters are AND-combined: a step must satisfy all specified criteria to be
included.  Omitting a flag means "no restriction" for that dimension.

Active filters are always shown in the output header so the result is
self-describing.

Flag summary:
  --contract     regex matched against ContractID
  --function     regex matched against Function name
  --event-type   exact match: trap, contract_call, host_function, auth
  --severity     error | warning | info | all
  --source-file  substring matched against SourceFile path
  --step-min     minimum step index (inclusive, 0-based)
  --step-max     maximum step index (inclusive, 0-based)
  --line-min     minimum source line number (inclusive; steps without line info are excluded)
  --line-max     maximum source line number (inclusive; steps without line info are excluded)
  --exclude      invert the filter: include steps that do NOT match

Output:
  --format text  plain text (default)
  --format json  machine-readable JSON with filter_summary block
  --output <file> write to file instead of stdout

Exit codes:
  0 — filter ran successfully (even if zero steps matched)
  1 — input file error, invalid flag values, or internal error

Examples:
  # All steps for a specific contract
  glassbox trace filter tx.json --contract CAAAA

  # Errors only, in JSON for CI consumption
  glassbox trace filter tx.json --severity error --format json

  # Steps in a source line range
  glassbox trace filter tx.json --source-file token.rs --line-min 40 --line-max 80

  # Everything except a noisy host function
  glassbox trace filter tx.json --event-type host_function --exclude

  # Save filtered result to a file
  glassbox trace filter tx.json --contract CAAAA --output ca_steps.json --format json`,
	Args: cobra.ExactArgs(1),
	PreRunE: func(cmd *cobra.Command, args []string) error {
		var failures []string

		// Validate --event-type
		if tfEventTypeFlag != "" {
			valid := trace.AllFilterableEventTypes()
			found := false
			for _, v := range valid {
				if tfEventTypeFlag == v {
					found = true
					break
				}
			}
			if !found {
				failures = append(failures, fmt.Sprintf(
					"invalid --event-type %q — must be one of: %s\n"+
						"  Fix: use one of the listed event types",
					tfEventTypeFlag, strings.Join(valid, ", "),
				))
			}
		}

		// Validate --severity
		if tfSeverityFlag != "" {
			switch tfSeverityFlag {
			case trace.FilterSeverityError, trace.FilterSeverityWarning,
				trace.FilterSeverityInfo, trace.FilterSeverityAll:
				// ok
			default:
				failures = append(failures, fmt.Sprintf(
					"invalid --severity %q — must be one of: error, warning, info, all\n"+
						"  Fix: use one of the listed severity levels",
					tfSeverityFlag,
				))
			}
		}

		// Validate step range
		if tfStepMinFlag > 0 && tfStepMaxFlag > 0 && tfStepMinFlag > tfStepMaxFlag {
			failures = append(failures, fmt.Sprintf(
				"--step-min (%d) cannot be greater than --step-max (%d)\n"+
					"  Fix: swap the values or widen the range",
				tfStepMinFlag, tfStepMaxFlag,
			))
		}

		// Validate line range
		if tfLineMinFlag < 0 {
			failures = append(failures, fmt.Sprintf(
				"--line-min (%d) cannot be negative\n  Fix: use a value >= 0",
				tfLineMinFlag,
			))
		}
		if tfLineMaxFlag < 0 {
			failures = append(failures, fmt.Sprintf(
				"--line-max (%d) cannot be negative\n  Fix: use a value >= 0",
				tfLineMaxFlag,
			))
		}
		if tfLineMinFlag > 0 && tfLineMaxFlag > 0 && tfLineMinFlag > tfLineMaxFlag {
			failures = append(failures, fmt.Sprintf(
				"--line-min (%d) cannot be greater than --line-max (%d)\n"+
					"  Fix: swap the values or widen the range",
				tfLineMinFlag, tfLineMaxFlag,
			))
		}

		// --event-type and --severity are not mutually exclusive by design,
		// but --exclude with zero other filters is meaningless (excludes everything).
		if tfExcludeFlag {
			anyFilter := tfContractFlag != "" || tfFunctionFlag != "" ||
				tfEventTypeFlag != "" || tfSeverityFlag != "" ||
				tfSourceFileFlag != "" || tfStepMinFlag > 0 || tfStepMaxFlag > 0 ||
				tfLineMinFlag > 0 || tfLineMaxFlag > 0
			if !anyFilter {
				failures = append(failures,
					"--exclude requires at least one other filter flag\n"+
						"  Fix: add --contract, --function, --event-type, or another filter alongside --exclude",
				)
			}
		}

		// Validate --format
		if tfFormatFlag != "" && tfFormatFlag != "text" && tfFormatFlag != "json" {
			failures = append(failures, fmt.Sprintf(
				"invalid --format %q — must be text or json\n"+
					"  Fix: use --format text (default) or --format json",
				tfFormatFlag,
			))
		}

		if len(failures) == 1 {
			return errors.WrapValidationError(failures[0])
		}
		if len(failures) > 1 {
			lines := []string{fmt.Sprintf("%d trace filter validation error(s):", len(failures))}
			for i, f := range failures {
				lines = append(lines, fmt.Sprintf("  %d. %s", i+1, f))
			}
			return errors.WrapValidationError(strings.Join(lines, "\n"))
		}
		return nil
	},
	RunE: runTraceFilter,
}

func init() {
	traceFilterCmd.Flags().StringVar(&tfContractFlag, "contract", "", "Filter by ContractID (regex)")
	traceFilterCmd.Flags().StringVar(&tfFunctionFlag, "function", "", "Filter by function name (regex)")
	traceFilterCmd.Flags().StringVar(&tfEventTypeFlag, "event-type", "", "Filter by event type: trap, contract_call, host_function, auth")
	traceFilterCmd.Flags().StringVar(&tfSeverityFlag, "severity", "", "Filter by severity: error, warning, info, all")
	traceFilterCmd.Flags().StringVar(&tfSourceFileFlag, "source-file", "", "Filter by source file path (substring match)")
	traceFilterCmd.Flags().IntVar(&tfStepMinFlag, "step-min", 0, "Minimum step index (inclusive, 0-based)")
	traceFilterCmd.Flags().IntVar(&tfStepMaxFlag, "step-max", 0, "Maximum step index (inclusive, 0-based)")
	traceFilterCmd.Flags().IntVar(&tfLineMinFlag, "line-min", 0, "Minimum source line number (inclusive)")
	traceFilterCmd.Flags().IntVar(&tfLineMaxFlag, "line-max", 0, "Maximum source line number (inclusive)")
	traceFilterCmd.Flags().BoolVar(&tfExcludeFlag, "exclude", false, "Invert filter: include steps that do NOT match the criteria")
	traceFilterCmd.Flags().StringVar(&tfFormatFlag, "format", "text", "Output format: text (default) or json")
	traceFilterCmd.Flags().StringVar(&tfOutputFlag, "output", "", "Write output to a file (default: stdout)")

	_ = traceFilterCmd.RegisterFlagCompletionFunc("event-type", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return trace.AllFilterableEventTypes(), cobra.ShellCompDirectiveNoFileComp
	})
	_ = traceFilterCmd.RegisterFlagCompletionFunc("severity", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return []string{"error", "warning", "info", "all"}, cobra.ShellCompDirectiveNoFileComp
	})
	_ = traceFilterCmd.RegisterFlagCompletionFunc("format", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return []string{"text", "json"}, cobra.ShellCompDirectiveNoFileComp
	})

	traceCmd.AddCommand(traceFilterCmd)
}

func runTraceFilter(_ *cobra.Command, args []string) error {
	traceFile := args[0]

	// Validate the input file.
	if info, err := os.Stat(traceFile); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf(
				"trace file %q not found\n"+
					"  Fix: verify the path or run 'glassbox debug --trace-output <file>' to create a trace",
				traceFile,
			)
		}
		return fmt.Errorf("failed to stat trace file %q: %w", traceFile, err)
	} else if info.IsDir() {
		return fmt.Errorf("--file %q is a directory, not a trace file", traceFile)
	}

	data, err := os.ReadFile(traceFile)
	if err != nil {
		return errors.WrapValidationError(fmt.Sprintf("failed to read trace file: %v", err))
	}

	executionTrace, err := trace.FromJSON(data)
	if err != nil {
		return errors.WrapValidationError(fmt.Sprintf(
			"failed to parse trace file %q: %v\n"+
				"  Fix: verify the file is a valid Glassbox trace JSON",
			traceFile, err,
		))
	}

	// Build filter expression from flags.
	expr := &trace.FilterExpression{
		ContractID: tfContractFlag,
		Function:   tfFunctionFlag,
		EventType:  tfEventTypeFlag,
		Severity:   tfSeverityFlag,
		SourceFile: tfSourceFileFlag,
		StepMin:    tfStepMinFlag,
		StepMax:    tfStepMaxFlag,
		LineMin:    tfLineMinFlag,
		LineMax:    tfLineMaxFlag,
		Exclude:    tfExcludeFlag,
	}

	// Validate and compile regex patterns.
	if err := expr.Validate(); err != nil {
		return errors.WrapValidationError(fmt.Sprintf("invalid filter: %v", err))
	}

	// Apply the filter.
	filtered, err := trace.ApplyFilter(executionTrace, expr)
	if err != nil {
		return errors.WrapValidationError(fmt.Sprintf("filter failed: %v", err))
	}

	// Render output.
	format := strings.ToLower(strings.TrimSpace(tfFormatFlag))
	if format == "" {
		format = "text"
	}

	var output []byte
	switch format {
	case "json":
		output, err = trace.RenderFilteredJSON(filtered)
		if err != nil {
			return errors.WrapValidationError(fmt.Sprintf("failed to render JSON: %v", err))
		}
	default: // "text"
		rendered, textErr := trace.RenderFilteredText(filtered)
		if textErr != nil {
			return errors.WrapValidationError(fmt.Sprintf("failed to render text: %v", textErr))
		}
		output = []byte(rendered)
	}

	// Write to file or stdout.
	if tfOutputFlag != "" {
		if err := os.WriteFile(tfOutputFlag, output, 0o644); err != nil {
			return fmt.Errorf("failed to write output to %q: %w", tfOutputFlag, err)
		}
		meta := trace.FilterMetadataFromTrace(filtered)
		fmt.Printf("Filtered trace written to: %s (%d/%d steps matched)\n",
			tfOutputFlag, meta.MatchedCount, meta.TotalSteps)
	} else {
		if _, err := os.Stdout.Write(output); err != nil {
			return fmt.Errorf("failed to write output: %w", err)
		}
	}

	return nil
}
