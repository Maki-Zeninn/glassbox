// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/dotandev/glassbox/internal/sourcemap"
	"github.com/spf13/cobra"
)

var (
	sourcemapExplainWasm        string
	sourcemapExplainAddr        string
	sourcemapExplainFormat      string
	sourcemapExplainProjectRoot string
	sourcemapExplainContractID  string
)

// sourcemapCmd groups source-map inspection tools.
var sourcemapCmd = &cobra.Command{
	Use:     "sourcemap",
	GroupID: "utility",
	Short:   "Source-map inspection and debugging tools",
	Long: `Commands for inspecting and debugging WASM-to-source mapping decisions.

Use 'glassbox sourcemap explain' to produce an auditable trace of every
resolution stage attempted for a WASM address or contract ID, along with
the reason each candidate was accepted or rejected.`,
}

// sourcemapExplainCmd explains the resolution decision for a WASM address
// or a contract ID's source discovery pipeline.
var sourcemapExplainCmd = &cobra.Command{
	Use:   "explain",
	Short: "Explain why a source location was chosen or rejected",
	Long: `Explain the source-map resolution decision for a WASM instruction address
or a contract source-discovery lookup.

For DWARF address resolution, provide --wasm and --addr:

  glassbox sourcemap explain --wasm ./contract.wasm --addr 0x1234
  glassbox sourcemap explain --wasm ./contract.wasm --addr 0x1234 --format json

For contract source-discovery, provide --contract-id:

  glassbox sourcemap explain --contract-id C...
  glassbox sourcemap explain --contract-id C... --format json

Output fields:
  stage      — which pipeline stage was attempted
  accepted   — whether the stage produced the final result
  reason     — why the candidate was accepted or rejected
  location   — resolved file and line (when available)
  quality    — full | partial | heuristic | unknown
  confidence — 0–100 score for the final mapping`,
	PreRunE: func(cmd *cobra.Command, args []string) error {
		if sourcemapExplainWasm == "" && sourcemapExplainContractID == "" {
			return fmt.Errorf("provide either --wasm <path> (with --addr <hex>) or --contract-id <id>")
		}
		if sourcemapExplainWasm != "" && sourcemapExplainAddr == "" {
			return fmt.Errorf("--addr is required when --wasm is provided (e.g. --addr 0x1234)")
		}
		switch strings.ToLower(sourcemapExplainFormat) {
		case "text", "json":
		default:
			return fmt.Errorf("--format must be 'text' or 'json', got %q", sourcemapExplainFormat)
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		if sourcemapExplainWasm != "" {
			return runSourcemapExplainWasm(cmd)
		}
		return runSourcemapExplainContract(cmd)
	},
}

func runSourcemapExplainWasm(cmd *cobra.Command) error {
	addrStr := strings.TrimPrefix(sourcemapExplainAddr, "0x")
	addrStr = strings.TrimPrefix(addrStr, "0X")
	addr, err := strconv.ParseUint(addrStr, 16, 64)
	if err != nil {
		return fmt.Errorf("--addr %q is not a valid hex address: %w", sourcemapExplainAddr, err)
	}

	wasmData, err := os.ReadFile(sourcemapExplainWasm)
	if err != nil {
		return fmt.Errorf("failed to read WASM file %q: %w", sourcemapExplainWasm, err)
	}

	projectRoot := sourcemapExplainProjectRoot
	if projectRoot == "" {
		var cwdErr error
		projectRoot, cwdErr = os.Getwd()
		if cwdErr != nil {
			return fmt.Errorf("failed to determine project root: %w", cwdErr)
		}
	}

	mapper := sourcemap.NewFallbackMapper(projectRoot)
	_, trace := mapper.ResolveWithExplain(wasmData, addr)

	return printExplainTrace(cmd, trace)
}

func runSourcemapExplainContract(cmd *cobra.Command) error {
	r := sourcemap.NewResolver(sourcemap.WithNonInteractive())
	_, trace, err := r.ResolveWithExplain(context.Background(), sourcemapExplainContractID)
	// An ErrSourceNotFound error is expected when all stages fail; still print the trace.
	if err != nil && trace.Quality != "unknown" {
		return err
	}

	if printErr := printExplainTrace(cmd, trace); printErr != nil {
		return printErr
	}
	// Return the resolution error after printing the trace so the caller has
	// the full picture before the process exits.
	return err
}

func printExplainTrace(cmd *cobra.Command, trace sourcemap.ExplainTrace) error {
	switch strings.ToLower(sourcemapExplainFormat) {
	case "json":
		data, err := sourcemap.FormatExplainJSON(trace)
		if err != nil {
			return fmt.Errorf("failed to serialise explain trace: %w", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), string(data))
	default:
		fmt.Fprint(cmd.OutOrStdout(), sourcemap.FormatExplainText(trace))
	}
	return nil
}

func init() {
	sourcemapExplainCmd.Flags().StringVar(&sourcemapExplainWasm, "wasm", "", "Path to compiled WASM binary")
	sourcemapExplainCmd.Flags().StringVar(&sourcemapExplainAddr, "addr", "", "WASM instruction address to explain (hex, e.g. 0x1234)")
	sourcemapExplainCmd.Flags().StringVar(&sourcemapExplainContractID, "contract-id", "", "Contract ID for source-discovery explain")
	sourcemapExplainCmd.Flags().StringVar(&sourcemapExplainFormat, "format", "text", "Output format: text or json")
	sourcemapExplainCmd.Flags().StringVar(&sourcemapExplainProjectRoot, "project-root", "", "Project root for path resolution (defaults to current directory)")

	_ = sourcemapExplainCmd.RegisterFlagCompletionFunc("format", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"text", "json"}, cobra.ShellCompDirectiveNoFileComp
	})

	sourcemapCmd.AddCommand(sourcemapExplainCmd)
	rootCmd.AddCommand(sourcemapCmd)
}
