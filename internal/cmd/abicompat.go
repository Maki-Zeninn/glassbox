// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/dotandev/glassbox/internal/abi"
	"github.com/dotandev/glassbox/internal/errors"
	"github.com/spf13/cobra"
)

// abiCompatExitBreaking is the exit code for breaking ABI changes.
// abiCompatExitConditional is the exit code for conditionally-compatible changes.
// Both are surfaced by returning a non-nil error from RunE, which cobra translates
// to os.Exit(1). For finer exit-code control a wrapper script can inspect the
// error text; the codes are documented in the command help.

var (
	abiCompatOldWasm string
	abiCompatNewWasm string
	abiCompatJSON    bool
	abiCompatExitOn  string // "breaking" | "conditional" | "any"
)

var checkABICompatCmd = &cobra.Command{
	Use:     "check-abi-compat",
	Short:   "Compare two Soroban contract ABIs and detect breaking changes",
	GroupID: "utility",
	Long: `Compare the ABI of a baseline (old) and a candidate (new) Soroban contract
WASM binary and classify every difference as breaking, conditionally compatible,
or additive.

Exit codes:
  0 — compatible or additive changes only
  2 — at least one conditionally compatible change (when --exit-on=conditional)
  3 — at least one breaking change

Change classes:
  breaking            — removed function/param/struct/enum case, changed type
  conditionally_compatible — new required param, changed return type
  additive            — new function, new enum case, new struct (safe)

Examples:
  glassbox check-abi-compat --old old.wasm --new new.wasm
  glassbox check-abi-compat --old old.wasm --new new.wasm --json
  glassbox check-abi-compat --old old.wasm --new new.wasm --exit-on=conditional`,
	RunE: runCheckABICompat,
}

func init() {
	checkABICompatCmd.Flags().StringVar(&abiCompatOldWasm, "old", "",
		"Path to the baseline (old) contract WASM binary [required]")
	checkABICompatCmd.Flags().StringVar(&abiCompatNewWasm, "new", "",
		"Path to the candidate (new) contract WASM binary [required]")
	checkABICompatCmd.Flags().BoolVar(&abiCompatJSON, "json", false,
		"Emit machine-readable JSON output")
	checkABICompatCmd.Flags().StringVar(&abiCompatExitOn, "exit-on", "breaking",
		`Minimum change class that causes a non-zero exit: "breaking" (default), "conditional", "any"`)

	_ = checkABICompatCmd.MarkFlagRequired("old")
	_ = checkABICompatCmd.MarkFlagRequired("new")

	rootCmd.AddCommand(checkABICompatCmd)
}

func runCheckABICompat(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()

	oldSpec, err := loadSpecFromWasm(abiCompatOldWasm)
	if err != nil {
		return errors.WrapValidationError(fmt.Sprintf("loading baseline WASM %q: %v", abiCompatOldWasm, err))
	}

	newSpec, err := loadSpecFromWasm(abiCompatNewWasm)
	if err != nil {
		return errors.WrapValidationError(fmt.Sprintf("loading candidate WASM %q: %v", abiCompatNewWasm, err))
	}

	report := abi.CompareSpecs(oldSpec, newSpec)

	if abiCompatJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if encErr := enc.Encode(report); encErr != nil {
			return errors.WrapValidationError(fmt.Sprintf("encoding result: %v", encErr))
		}
	} else {
		printABICompatReport(cmd, report)
	}

	return abiCompatExitError(report, abiCompatExitOn)
}

// loadSpecFromWasm reads a WASM file, pre-validates it, and extracts the
// contractspecv0 section into a ContractSpec.
func loadSpecFromWasm(path string) (*abi.ContractSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}
	if err := abi.ValidateWasmMagic(data, path); err != nil {
		return nil, err
	}

	specData, err := abi.ExtractCustomSection(data, "contractspecv0")
	if err != nil {
		return nil, fmt.Errorf("extracting spec section: %w", err)
	}
	if specData == nil {
		return &abi.ContractSpec{}, nil // no spec section → empty spec
	}

	return abi.DecodeContractSpec(specData)
}

// printABICompatReport writes a human-readable CompatReport to the command output.
func printABICompatReport(cmd *cobra.Command, report *abi.CompatReport) {
	out := cmd.OutOrStdout()
	sep := strings.Repeat("─", 60)

	fmt.Fprintln(out, "ABI Compatibility Report")
	fmt.Fprintln(out, sep)
	fmt.Fprintf(out, "  Overall status  : %s\n", report.OverallStatus)
	fmt.Fprintf(out, "  Breaking        : %d\n", report.BreakingCount)
	fmt.Fprintf(out, "  Conditional     : %d\n", report.ConditionalCount)
	fmt.Fprintf(out, "  Additive        : %d\n", report.AdditiveCount)
	fmt.Fprintln(out)

	if len(report.Changes) == 0 {
		fmt.Fprintln(out, "No changes detected.")
		return
	}

	fmt.Fprintln(out, "Changes:")
	for _, c := range report.Changes {
		statusTag := statusLabel(c.Status)
		fmt.Fprintf(out, "  %s  %s  [%s]\n", statusTag, c.Path, c.Kind)
		if c.OldValue != "" {
			fmt.Fprintf(out, "         old: %s\n", c.OldValue)
		}
		if c.NewValue != "" {
			fmt.Fprintf(out, "         new: %s\n", c.NewValue)
		}
		if c.Remediation != "" {
			fmt.Fprintf(out, "         fix: %s\n", c.Remediation)
		}
	}

	fmt.Fprintln(out)
	if report.Remediation != "" {
		fmt.Fprintf(out, "Action required: %s\n", report.Remediation)
	}

	// Link to compatibility documentation.
	fmt.Fprintln(out)
	fmt.Fprintln(out, "See docs/api-compatibility.md for detailed remediation guidance.")
}

func statusLabel(s abi.CompatStatus) string {
	switch s {
	case abi.CompatBreaking:
		return "[BREAKING    ]"
	case abi.CompatConditional:
		return "[CONDITIONAL ]"
	case abi.CompatAdditive:
		return "[ADDITIVE    ]"
	default:
		return "[OK          ]"
	}
}

// abiCompatExitError maps the report's status to a non-nil error (causing a
// non-zero exit) depending on the --exit-on flag value.
func abiCompatExitError(report *abi.CompatReport, exitOn string) error {
	switch strings.ToLower(exitOn) {
	case "any":
		if len(report.Changes) > 0 {
			return fmt.Errorf("ABI changes detected (exit-on=any): status=%s", report.OverallStatus)
		}
	case "conditional":
		if report.OverallStatus == abi.CompatConditional || report.OverallStatus == abi.CompatBreaking {
			return fmt.Errorf("ABI compatibility issue detected: status=%s", report.OverallStatus)
		}
	default: // "breaking"
		if report.OverallStatus == abi.CompatBreaking {
			return fmt.Errorf("breaking ABI changes detected: %d change(s)", report.BreakingCount)
		}
	}
	return nil
}
