// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/dotandev/glassbox/internal/profile"
	"github.com/dotandev/glassbox/internal/simulator"
	"github.com/dotandev/glassbox/internal/trace"
	"github.com/spf13/cobra"
)

var (
	profileTraceFile string
	profileOutput    string
	profileXdr       string
	profileNetwork   string
	profileRPCURL    string
	profileRPCToken  string
	profileOutJSON   string
)

var profileCmd = &cobra.Command{
	Use:     "profile [trace-file]",
	Aliases: []string{"ps"},
	GroupID: "testing",
	Short:   "Advanced gas usage analysis and optimization advisor",
	Long: `Analyze gas consumption and receive actionable optimization recommendations.

This command can either:
  1) Load an existing execution trace file (pprof format by default)
  2) Run a new simulation from an XDR envelope and generate a comprehensive report

The report includes a breakdown of CPU/Memory usage, an efficiency score, and
specific tips for reducing gas costs.

Provide exactly one input source:
  • --xdr <file-or-base64>   Run a live simulation from a transaction envelope
  • --file <trace-file>      Analyze an existing trace (or pass as a positional arg)

Output options:
  • -o / --output <file>     Path for the pprof profile (default: profile.pb.gz)
  • --out-json <file>        Export the optimization report as a JSON file

Performance notes:
  Traces with no gas-instrumented steps produce an empty pprof profile.
  Use 'glassbox profile --xdr <tx.xdr>' for a live gas report with full budget data.
  The --out-json flag writes a machine-readable optimization report; combine with
  CI pipelines to track gas regressions across builds.

Example:
  glassbox profile execution.json -o gas.pb.gz
  glassbox profile --xdr <tx.xdr> --network testnet
  glassbox profile --xdr <tx.xdr> --out-json profile.json`,
	Args: cobra.MaximumNArgs(1),
	PreRunE: func(cmd *cobra.Command, args []string) error {
		// Determine the active input source.
		hasXDR := profileXdr != ""
		hasFile := profileTraceFile != "" || len(args) > 0

		// Guard: require at least one source.
		if !hasXDR && !hasFile {
			return fmt.Errorf(
				"no input provided — specify one of:\n" +
					"  --xdr <file-or-base64>   simulate from a transaction envelope\n" +
					"  --file <trace-file>      analyze an existing trace file\n" +
					"  <trace-file>             positional trace file argument\n\n" +
					"Run 'glassbox profile --help' for full usage")
		}

		// Guard: reject ambiguous dual-input.
		if hasXDR && hasFile {
			return fmt.Errorf(
				"--xdr and --file (or a positional trace-file argument) are mutually exclusive; " +
					"provide only one input source")
		}

		// Validate network when running a live simulation.
		if hasXDR {
			switch strings.ToLower(profileNetwork) {
			case "testnet", "mainnet", "futurenet":
				// valid
			default:
				return fmt.Errorf(
					"invalid --network %q; must be one of: testnet, mainnet, futurenet",
					profileNetwork)
			}

			// When --xdr points to a file path, validate it exists before we
			// attempt any simulation.
			if _, statErr := os.Stat(profileXdr); statErr == nil {
				// It is a file path — validate readability.
				if _, readErr := os.Open(profileXdr); readErr != nil {
					return fmt.Errorf("cannot read XDR file %q: %w", profileXdr, readErr)
				}
			}
		}

		// Validate the trace file exists when running in file mode.
		if hasFile && !hasXDR {
			filename := profileTraceFile
			if len(args) > 0 {
				filename = args[0]
			}
			if _, err := os.Stat(filename); err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf(
						"trace file not found: %q\n"+
							"Tip: generate a trace first with 'glassbox debug --save-snapshots <file>'",
						filename)
				}
				return fmt.Errorf("cannot access trace file %q: %w", filename, err)
			}
		}

		// Validate --output (pprof output path): must not look like a directory
		// and the parent directory must exist.
		if profileOutput != "" && profileOutput != "profile.pb.gz" {
			if strings.HasSuffix(profileOutput, "/") || strings.HasSuffix(profileOutput, "\\") {
				return fmt.Errorf(
					"--output %q looks like a directory path; provide a full file path\n"+
						"  Fix: specify a filename (e.g. --output ./profiles/gas.pb.gz)",
					profileOutput,
				)
			}
			outDir := filepath.Dir(profileOutput)
			if outDir != "." && outDir != "" {
				if info, err := os.Stat(outDir); err != nil || !info.IsDir() {
					return fmt.Errorf(
						"--output directory %q does not exist; create it before running\n"+
							"  Fix: run 'mkdir -p %s' or choose a path in an existing directory",
						outDir, outDir)
				}
			}
		}

		// Validate --out-json: must not look like a directory and the parent
		// directory must exist.
		if profileOutJSON != "" {
			if strings.HasSuffix(profileOutJSON, "/") || strings.HasSuffix(profileOutJSON, "\\") {
				return fmt.Errorf(
					"--out-json %q looks like a directory path; provide a full file path\n"+
						"  Fix: specify a filename (e.g. --out-json ./reports/gas-report.json)",
					profileOutJSON,
				)
			}
			dir := filepath.Dir(profileOutJSON)
			if dir != "." && dir != "" {
				if info, err := os.Stat(dir); err != nil || !info.IsDir() {
					return fmt.Errorf(
						"--out-json directory %q does not exist; create it before running\n"+
							"  Fix: run 'mkdir -p %s' or choose a path in an existing directory",
						dir, dir)
				}
			}
		}

		return nil
	},
	RunE: runProfile,
}

func init() {
	profileCmd.Flags().StringVarP(&profileTraceFile, "file", "f", "", "Trace file to profile")
	profileCmd.Flags().StringVarP(&profileOutput, "output", "o", "profile.pb.gz", "Output path for the pprof profile")
	profileCmd.Flags().StringVar(&profileXdr, "xdr", "", "XDR encoded transaction envelope to profile")
	profileCmd.Flags().StringVarP(&profileNetwork, "network", "n", "mainnet", "Stellar network for simulation (testnet, mainnet, futurenet)")
	profileCmd.Flags().StringVar(&profileRPCURL, "rpc-url", "", "Custom Horizon RPC URL")
	profileCmd.Flags().StringVar(&profileRPCToken, "rpc-token", "", "RPC authentication token")
	profileCmd.Flags().StringVar(&profileOutJSON, "out-json", "", "Export the optimization report as JSON")

	rootCmd.AddCommand(profileCmd)
}

func runProfile(cmd *cobra.Command, args []string) error {
	if profileXdr != "" {
		return runSimulationProfile(cmd)
	}

	var filename string
	if len(args) > 0 {
		filename = args[0]
	} else if profileTraceFile != "" {
		filename = profileTraceFile
	} else {
		// PreRunE catches this before RunE is reached, but guard defensively.
		return fmt.Errorf(
			"no input provided; use --xdr for a live simulation or provide a trace file\n" +
				"Run 'glassbox profile --help' for usage")
	}

	return runTraceProfile(filename)
}

func runSimulationProfile(cmd *cobra.Command) error {
	ctx := cmd.Context()

	// Handle XDR input — accept a file path or a raw base64 string.
	var xdrB64 string
	if _, err := os.Stat(profileXdr); err == nil {
		content, err := os.ReadFile(profileXdr)
		if err != nil {
			return fmt.Errorf("failed to read XDR file %q: %w\nCheck that the file exists and is readable", profileXdr, err)
		}
		xdrB64 = string(bytes.TrimSpace(content))
		if xdrB64 == "" {
			return fmt.Errorf("XDR file %q is empty; provide a file containing a base64-encoded transaction envelope", profileXdr)
		}
	} else {
		xdrB64 = profileXdr
		if xdrB64 == "" {
			return fmt.Errorf("--xdr value is empty; provide a base64-encoded transaction envelope or a path to a .xdr file")
		}
	}

	// Create simulator runner.
	runner, err := simulator.NewRunner("", false)
	if err != nil {
		return fmt.Errorf(
			"failed to create simulator runner: %w\n"+
				"Tip: run 'glassbox doctor' to verify the simulator binary is installed",
			err)
	}
	defer runner.Close()

	// Build simulation request.
	builder := simulator.NewSimulationRequestBuilder().
		WithEnvelopeXDR(xdrB64).
		WithResultMetaXDR("AAAAAQ=="). // Placeholder
		WithOptimizationAdvisor(true)

	req, err := builder.Build()
	if err != nil {
		return fmt.Errorf("failed to build simulation request: %w", err)
	}

	fmt.Println("[DEPLOY] Running gas analysis simulation...")
	resp, err := runner.Run(ctx, req)
	if err != nil {
		return fmt.Errorf(
			"simulation failed: %w\n"+
				"Tip: verify the XDR envelope is valid and the network (%s) is reachable",
			err, profileNetwork)
	}

	if resp.OptimizationReport == nil {
		return fmt.Errorf(
			"simulator did not return an optimization report\n" +
				"This may indicate an unsupported protocol version or a simulator version mismatch; " +
				"run 'glassbox doctor' to check compatibility")
	}

	// Display report.
	displayOptimizationReport(cmd.OutOrStdout(), resp.OptimizationReport, resp.BudgetUsage)

	// Export to JSON if requested.
	if profileOutJSON != "" {
		data, err := json.MarshalIndent(resp.OptimizationReport, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal optimization report to JSON: %w", err)
		}
		if err := os.WriteFile(profileOutJSON, data, 0644); err != nil {
			return fmt.Errorf("failed to write JSON report to %q: %w", profileOutJSON, err)
		}
		fmt.Printf("\n[OK] Optimization report exported to: %s\n", profileOutJSON)
	}

	return nil
}

func runTraceProfile(filename string) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf(
				"trace file not found: %q\n"+
					"Generate a trace with: glassbox debug --save-snapshots <file>",
				filename)
		}
		return fmt.Errorf("failed to read trace file %q: %w", filename, err)
	}

	executionTrace, err := trace.FromJSON(data)
	if err != nil {
		return fmt.Errorf(
			"failed to parse trace file %q: %w\n"+
				"Ensure the file was generated by 'glassbox debug' and has not been modified",
			filename, err)
	}

	stepCount := len(executionTrace.States)
	fmt.Printf("Analyzing trace: %s\n", filename)
	fmt.Printf("Steps: %d\n", stepCount)

	// Warn early if the trace carries no steps — WritePprof will still succeed
	// (producing an empty profile) but the user deserves a clear heads-up.
	if stepCount == 0 {
		fmt.Fprintf(os.Stderr,
			"Warning: trace contains no execution steps — the pprof profile will be empty.\n"+
				"  Possible causes:\n"+
				"    - The simulation ran successfully but produced no diagnostic events\n"+
				"    - The trace file was generated by an older simulator version\n"+
				"  Tip: re-run with 'glassbox debug --save-snapshots <file>' to capture a\n"+
				"       fresh trace, or use 'glassbox profile --xdr <tx.xdr>' for a live\n"+
				"       report with full gas budget data\n",
		)
	}

	f, err := os.Create(profileOutput)
	if err != nil {
		return fmt.Errorf(
			"failed to create output file %q: %w\n"+
				"  Fix: check that the directory exists and you have write permission\n"+
				"  Tip: use -o to specify a different output path (e.g. -o /tmp/gas.pb.gz)",
			profileOutput, err)
	}
	defer f.Close()

	if err := profile.WritePprof(executionTrace, f); err != nil {
		return fmt.Errorf(
			"failed to generate pprof profile: %w\n"+
				"  Fix: re-run 'glassbox debug --save-snapshots <file>' to regenerate a clean trace\n"+
				"  Check: run 'glassbox doctor' to verify simulator compatibility",
			err)
	}

	fmt.Printf("[OK] Profile saved to: %s\n", profileOutput)
	fmt.Println("Analyze with: go tool pprof " + profileOutput)
	if stepCount == 0 {
		fmt.Println("Note: the profile is empty because the trace contained no steps (see warnings above)")
	}
	return nil
}

func displayOptimizationReport(w io.Writer, report *simulator.OptimizationReport, budget *simulator.BudgetUsage) {
	fmt.Fprintf(w, "\n=== Gas optimization report ===\n")
	fmt.Fprintf(w, "Overall Efficiency: %.1f%%\n", report.OverallEfficiency*100)
	fmt.Fprintf(w, "Status: %s\n", report.ComparisonToBaseline)

	if budget != nil {
		fmt.Fprintf(w, "\nResource Usage:\n")
		fmt.Fprintf(w, "  CPU Instructions: %d (%.1f%% of limit)\n", budget.CPUInstructions, budget.CPUUsagePercent)
		fmt.Fprintf(w, "  Memory Bytes:     %d (%.1f%% of limit)\n", budget.MemoryBytes, budget.MemoryUsagePercent)
		fmt.Fprintf(w, "  Operations:       %d\n", budget.OperationsCount)
	}

	if len(report.BudgetBreakdown) > 0 {
		fmt.Fprintf(w, "\nBudget Breakdown:\n")
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "Category\tCost (Instructions)\tPercentage")
		var total float64
		for _, v := range report.BudgetBreakdown {
			total += v
		}
		for cat, cost := range report.BudgetBreakdown {
			pct := (cost / total) * 100
			fmt.Fprintf(tw, "%s\t%.0f\t%.1f%%\n", cat, cost, pct)
		}
		tw.Flush()
	}

	if len(report.Tips) > 0 {
		fmt.Fprintf(w, "\n[INFO] Optimization Tips:\n")
		for _, tip := range report.Tips {
			severity := tip.Severity
			icon := " "
			switch severity {
			case "High":
				icon = " "
			case "Medium":
				icon = " "
			}
			fmt.Fprintf(w, "\n[%s%s] %s: %s\n", icon, severity, tip.Category, tip.Message)
			if tip.EstimatedSavings != "" {
				fmt.Fprintf(w, "   Estimated Savings: %s\n", tip.EstimatedSavings)
			}
			if tip.CodeLocation != nil {
				fmt.Fprintf(w, "   Location: %s\n", *tip.CodeLocation)
			}
		}
	} else {
		fmt.Fprintln(w, "\n[OK] No specific optimizations identified. Your contract seems gas-efficient!")
	}
}
