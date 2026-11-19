// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"os"

	"github.com/dotandev/glassbox/internal/abi"
	"github.com/dotandev/glassbox/internal/errors"
	"github.com/dotandev/glassbox/internal/sarif"
	"github.com/dotandev/glassbox/internal/security"
	"github.com/dotandev/glassbox/internal/visualizer"
	"github.com/spf13/cobra"
)

var scanABIPath string
var scanFormatFlag string
var scanOutputFlag string

var scanCmd = &cobra.Command{
	Use:     "scan <source-file-or-directory>",
	GroupID: "testing",
	Short:   "Scan contract code and ABI metadata for vulnerability heuristics",
	Long: `Scan Soroban contract source and optional ABI metadata for security patterns
such as privileged functions without visible auth checks, panic-prone code paths,
persistent storage writes without visible auth, and randomness usage.

Output formats (--format):
  text  — human-readable findings list (default)
  json  — machine-readable JSON findings
  sarif — SARIF 2.1.0 for GitHub Advanced Security and compatible scanners`,
	Example: `  glassbox scan ./contracts/token
  glassbox scan ./contracts/token --abi ./token.abi.json
  glassbox scan ./contracts/token --format sarif --output findings.sarif`,
	Args: cobra.ExactArgs(1),
	PreRunE: func(cmd *cobra.Command, args []string) error {
		// Validate source path exists before any I/O.
		if err := validateFilePath("source", args[0]); err != nil {
			return err
		}
		// Validate ABI file path when provided.
		if scanABIPath != "" {
			if err := validateFilePath("abi", scanABIPath); err != nil {
				return err
			}
		}
		// Validate --format when set.
		if scanFormatFlag != "" {
			if err := validateEnum("--format", scanFormatFlag, []string{"text", "json", "sarif"}); err != nil {
				return err
			}
		}
		// Validate --output path when set.
		if scanOutputFlag != "" {
			if _, err := ValidateOutputPath("output", scanOutputFlag); err != nil {
				return err
			}
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		spec, err := loadScanABI(scanABIPath)
		if err != nil {
			return errors.WrapValidationError(fmt.Sprintf("failed to load ABI: %v", err))
		}

		detector := security.NewDetector()
		findings, err := detector.ScanSourcePath(args[0], spec)
		if err != nil {
			return errors.WrapValidationError(fmt.Sprintf("failed to scan source: %v", err))
		}

		switch scanFormatFlag {
		case "sarif":
			sarifData, sarErr := sarif.ExportJSON(findings, sarif.ExportOptions{
				ArtifactURI: args[0],
			})
			if sarErr != nil {
				return errors.WrapValidationError(fmt.Sprintf("failed to export SARIF: %v", sarErr))
			}
			if scanOutputFlag != "" {
				if writeErr := os.WriteFile(scanOutputFlag, sarifData, 0o644); writeErr != nil {
					return errors.WrapValidationError(fmt.Sprintf(
						"failed to write SARIF to %q: %v", scanOutputFlag, writeErr,
					))
				}
				fmt.Fprintf(cmd.OutOrStdout(), "SARIF report written to: %s (%d finding(s))\n",
					scanOutputFlag, len(findings))
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), string(sarifData))
			}
			return nil

		case "json":
			fmt.Fprintln(cmd.OutOrStdout(), security.FindingsToJSON(findings))
			return nil

		default: // "text"
			printSecurityFindings(findings)
			return nil
		}
	},
}

func loadScanABI(path string) (*abi.ContractSpec, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	switch abi.DetectFormat(data) {
	case abi.ImportFormatJSON:
		return abi.ImportFromJSON(data)
	default:
		return abi.ImportFromXDR(data)
	}
}

func printSecurityFindings(findings []security.Finding) {
	if len(findings) == 0 {
		fmt.Printf("%s No security issues detected\n", visualizer.Success())
		return
	}
	fmt.Printf("%s Security findings: %d\n", visualizer.Warning(), len(findings))
	for i, finding := range findings {
		fmt.Printf("%d. [%s] %s - %s\n", i+1, finding.Severity, finding.Title, finding.Description)
		if finding.Evidence != "" {
			fmt.Printf("   Evidence: %s\n", finding.Evidence)
		}
	}
}

func init() {
	scanCmd.Flags().StringVar(&scanABIPath, "abi", "", "Optional contract ABI/spec file (JSON or XDR) for metadata heuristics")
	scanCmd.Flags().StringVar(&scanFormatFlag, "format", "text", "Output format: text, json, or sarif")
	scanCmd.Flags().StringVar(&scanOutputFlag, "output", "", "Write output to file instead of stdout (useful with --format sarif)")
	_ = scanCmd.RegisterFlagCompletionFunc("format", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"text", "json", "sarif"}, cobra.ShellCompDirectiveDefault
	})
	rootCmd.AddCommand(scanCmd)
}
