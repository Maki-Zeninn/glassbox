// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/dotandev/glassbox/internal/errors"
	"github.com/dotandev/glassbox/internal/trace"
	"github.com/spf13/cobra"
)

// traceCompareCmd implements glassbox trace compare
var traceCompareCmd = &cobra.Command{
	Use:     "compare <baseline_trace.json> <current_trace.json>",
	GroupID: "testing",
	Short:   "Compare two trace files for differences and regressions",
	Long: `Compare two execution trace files side-by-side to detect differences
in execution paths, state changes, and contract invocations.

This is useful for regression testing to ensure your contract changes don't
alter behavior in unexpected ways.

Examples:
  glassbox trace compare baseline.json current.json
  glassbox trace compare v1.json v2.json --baseline-name "v1.0" --current-name "v2.0"`,
	Args: cobra.ExactArgs(2),
	RunE: runTraceCompare,
}

// traceCompareParentCmd is the parent command for trace sub-commands
var traceCompareParentCmd = &cobra.Command{
	Use:     "trace",
	GroupID: "utility",
	Short:   "Manage execution traces",
	Long: `Save, load, and compare execution traces for debugging and regression testing.`,
}

var (
	traceCompareBaselineNameFlag string
	traceCompareCurrentNameFlag  string
	traceSaveOutputFlag          string
	traceCompareOutputFormatFlag string
	traceCompareConfigFileFlag   string
	traceCompareCPUThresholdFlag float64
	traceCompareMemThresholdFlag float64
	traceCompareFailOnViolation  bool
	traceCompareInfoModeFlag     bool
)

func init() {
	// Trace save command
	traceSaveCmd := &cobra.Command{
		Use:   "save <output_path>",
		Short: "Save the current execution trace to a file",
		RunE:  runTraceSave, // TODO: implement this if we have a current trace
	}
	traceSaveCmd.Flags().StringVar(&traceSaveOutputFlag, "output", "", "Output file path (required)")

	// Trace compare command flags
	traceCompareCmd.Flags().StringVar(&traceCompareBaselineNameFlag, "baseline-name", "Baseline", "Name for the baseline trace in output")
	traceCompareCmd.Flags().StringVar(&traceCompareCurrentNameFlag, "current-name", "Current", "Name for the current trace in output")
	traceCompareCmd.Flags().StringVar(&traceCompareOutputFormatFlag, "format", "table", "Output format: table, json")
	traceCompareCmd.Flags().StringVar(&traceCompareConfigFileFlag, "config", "", "Path to comparison config JSON file")
	traceCompareCmd.Flags().Float64Var(&traceCompareCPUThresholdFlag, "cpu-threshold", 10.0, "CPU regression threshold percentage")
	traceCompareCmd.Flags().Float64Var(&traceCompareMemThresholdFlag, "memory-threshold", 10.0, "Memory regression threshold percentage")
	traceCompareCmd.Flags().BoolVar(&traceCompareFailOnViolation, "fail-on-violation", false, "Exit with error code on threshold violation")
	traceCompareCmd.Flags().BoolVar(&traceCompareInfoModeFlag, "info", false, "Informational mode (never fail on differences)")

	traceCompareParentCmd.AddCommand(traceCompareCmd)
	traceCompareParentCmd.AddCommand(traceSaveCmd)
	rootCmd.AddCommand(traceCompareParentCmd)
}

func runTraceCompare(cmd *cobra.Command, args []string) error {
	baselinePath := args[0]
	currentPath := args[1]

	fmt.Printf("Loading traces...\n")
	fmt.Printf("  Baseline: %s\n", baselinePath)
	fmt.Printf("  Current:  %s\n\n", currentPath)

	baselineTrace, err := trace.LoadExecutionTrace(baselinePath)
	if err != nil {
		return errors.WrapValidationError(fmt.Sprintf("failed to load baseline trace: %v", err))
	}

	currentTrace, err := trace.LoadExecutionTrace(currentPath)
	if err != nil {
		return errors.WrapValidationError(fmt.Sprintf("failed to load current trace: %v", err))
	}

	// Load or build comparison config
	config, err := loadComparisonConfig()
	if err != nil {
		return errors.WrapValidationError(fmt.Sprintf("failed to load comparison config: %v", err))
	}

	// In info mode, never fail on violations
	if traceCompareInfoModeFlag {
		config.FailOnThresholdViolation = false
	}

	fmt.Printf("Comparing traces with normalized analysis...\n\n")
	normalizedResult := trace.CompareTracesNormalized(baselineTrace, currentTrace, traceCompareBaselineNameFlag, traceCompareCurrentNameFlag, config)

	// Render based on format
	switch traceCompareOutputFormatFlag {
	case "json":
		jsonOutput, err := normalizedResult.ToJSON()
		if err != nil {
			return errors.WrapValidationError(fmt.Sprintf("failed to serialize JSON: %v", err))
		}
		fmt.Println(string(jsonOutput))
	case "table":
		normalizedResult.RenderTable()
	default:
		return errors.WrapValidationError(fmt.Sprintf("unknown output format: %s", traceCompareOutputFormatFlag))
	}

	// Determine exit status
	if normalizedResult.HasRegression && config.FailOnThresholdViolation {
		return errors.WrapValidationError("Regression detected - threshold violations exceeded")
	}

	return nil
}

func loadComparisonConfig() (*trace.ComparisonConfig, error) {
	// Start with defaults
	config := trace.DefaultComparisonConfig()

	// Override with config file if provided
	if traceCompareConfigFileFlag != "" {
		configBytes, err := os.ReadFile(traceCompareConfigFileFlag)
		if err != nil {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
		if err := json.Unmarshal(configBytes, config); err != nil {
			return nil, fmt.Errorf("failed to parse config file: %w", err)
		}
	}

	// Override with command-line flags
	if traceCompareCPUThresholdFlag > 0 {
		config.CPUThresholdPct = traceCompareCPUThresholdFlag
	}
	if traceCompareMemThresholdFlag > 0 {
		config.MemoryThresholdPct = traceCompareMemThresholdFlag
	}
	if traceCompareFailOnViolation {
		config.FailOnThresholdViolation = true
	}

	return config, nil
}

func runTraceSave(cmd *cobra.Command, args []string) error {
	// TODO: This would need access to a current trace, perhaps from a session or debug command
	return errors.WrapValidationError("trace save is not implemented yet - use debug command to generate traces")
}
