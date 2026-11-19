// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"

	"github.com/dotandev/glassbox/internal/compare"
	"github.com/dotandev/glassbox/internal/errors"
	"github.com/spf13/cobra"
)

var (
	wasmDiffSemanticFlag        bool
	wasmDiffIgnoreMetadataFlag  bool
	wasmDiffIgnoreDebugFlag     bool
	wasmDiffIgnoreUnknownFlag   bool
)

var wasmDiffCmd = &cobra.Command{
	Use:     "wasm-diff <local-wasm> <remote-wasm>",
	GroupID: "development",
	Short:   "Compare two WASM binaries for source mapping compatibility",
	Long: `Compare a local WASM build artifact with an on-chain or reference WASM binary
to identify mismatches that can cause source mapping and debug issues.

Raw mode (default):
  Inspects both binaries for SHA-256 hash, file size, and section count.
  Reports divergence at the byte level.

Semantic / normalized mode (--semantic):
  Normalizes each module into comparable sections and classifies changes
  by category: executable code, ABI (imports/exports), debug info, and
  metadata. Ignored sections (producers, target_features, build_id by
  default) do not produce findings.

Exit code 0 is returned when the binaries are identical (or semantically
equivalent in --semantic mode); non-zero when they differ meaningfully.`,
	Example: `  # Raw byte-level comparison
  glassbox wasm-diff ./target/wasm32-unknown-unknown/release/contract.wasm ./onchain.wasm

  # Semantic diff — ignore compiler metadata, report executable/ABI changes
  glassbox wasm-diff --semantic ./contract.wasm ./onchain.wasm

  # Semantic diff keeping debug sections in the comparison
  glassbox wasm-diff --semantic --ignore-debug=false ./contract.wasm ./onchain.wasm`,
	Args: cobra.ExactArgs(2),
	PreRunE: func(cmd *cobra.Command, args []string) error {
		if err := validateFilePath("local-wasm", args[0]); err != nil {
			return err
		}
		if err := validateFilePath("remote-wasm", args[1]); err != nil {
			return err
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		localPath := args[0]
		remotePath := args[1]

		if wasmDiffSemanticFlag {
			opts := compare.NormalizeOptions{
				IgnoreMetadata:      wasmDiffIgnoreMetadataFlag,
				IgnoreDebug:         wasmDiffIgnoreDebugFlag,
				IgnoreUnknownCustom: wasmDiffIgnoreUnknownFlag,
			}
			result, err := compare.DiffWASMSemanticFiles(localPath, remotePath, opts)
			if err != nil {
				return errors.WrapValidationError(fmt.Sprintf("wasm-diff failed: %v", err))
			}
			printSemanticWASMDiff(result, localPath, remotePath)
			if result.ExecutableChanged || result.ABIChanged {
				return fmt.Errorf("semantic WASM diff found executable or ABI changes")
			}
			return nil
		}

		result, err := compare.DiffWASMFiles(localPath, remotePath)
		if err != nil {
			return errors.WrapValidationError(fmt.Sprintf("wasm-diff failed: %v", err))
		}

		printWASMDiff(result, localPath, remotePath)

		if result.HasDivergence {
			return fmt.Errorf("WASM binaries differ — source mapping may not match the deployed contract")
		}
		return nil
	},
}

func printWASMDiff(result *compare.WASMDiffResult, localPath, remotePath string) {
	fmt.Println()
	fmt.Println("WASM Binary Comparison")
	fmt.Println("──────────────────────────────────────────────────────────────────")
	fmt.Printf("  Local  : %s\n", localPath)
	fmt.Printf("  Remote : %s\n", remotePath)
	fmt.Println()

	printDiffRow("Hash match", result.HashMatch,
		abbreviate(result.Local.Hash, 16), abbreviate(result.Remote.Hash, 16))
	printDiffRow("Size match", result.SizeMatch,
		fmt.Sprintf("%d bytes", result.Local.Size), fmt.Sprintf("%d bytes", result.Remote.Size))
	printDiffRow("Section count", result.SectionMatch,
		fmt.Sprintf("%d section(s)", result.Local.SectionCount),
		fmt.Sprintf("%d section(s)", result.Remote.SectionCount))

	if !result.Local.IsValidWASM {
		fmt.Println()
		fmt.Println("  WARNING: local file does not appear to be a valid WASM binary (missing magic bytes)")
	}
	if !result.Remote.IsValidWASM {
		fmt.Println()
		fmt.Println("  WARNING: remote file does not appear to be a valid WASM binary (missing magic bytes)")
	}

	fmt.Println()
	if result.HasDivergence {
		fmt.Printf("  Result : [DIFF] %s\n", result.Summary)
	} else {
		fmt.Printf("  Result : [OK]   %s\n", result.Summary)
	}
	fmt.Println()
}

func printSemanticWASMDiff(result *compare.SemanticDiffResult, localPath, remotePath string) {
	fmt.Println()
	fmt.Println("WASM Semantic Comparison")
	fmt.Println("──────────────────────────────────────────────────────────────────")
	fmt.Printf("  Local  : %s\n", localPath)
	fmt.Printf("  Remote : %s\n", remotePath)
	fmt.Println()

	// Raw header line.
	rawMark := "[OK]  "
	if result.Raw.HasDivergence {
		rawMark = "[DIFF]"
	}
	fmt.Printf("  %s  Raw bytes  local=%d remote=%d\n",
		rawMark, result.Raw.Local.Size, result.Raw.Remote.Size)

	// Normalization manifest.
	if len(result.Manifest.DroppedLocal)+len(result.Manifest.DroppedRemote) > 0 {
		fmt.Printf("  [INFO] Ignored section classes: local=%v remote=%v\n",
			result.Manifest.DroppedLocal, result.Manifest.DroppedRemote)
	}
	fmt.Println()

	// Per-class findings.
	fmt.Println("  Section class findings:")
	for _, f := range result.SectionFindings {
		mark := "[OK]  "
		if f.Changed {
			mark = "[DIFF]"
		}
		fmt.Printf("    %s  %s\n", mark, f.Description)
	}

	fmt.Println()
	if result.MetadataOnlyDiff {
		fmt.Printf("  Result : [OK]   %s\n", result.Summary)
	} else if result.ExecutableChanged || result.ABIChanged {
		fmt.Printf("  Result : [DIFF] %s\n", result.Summary)
	} else {
		fmt.Printf("  Result : [OK]   %s\n", result.Summary)
	}
	fmt.Println()
}

func printDiffRow(label string, match bool, localVal, remoteVal string) {
	mark := "[OK]  "
	if !match {
		mark = "[DIFF]"
	}
	fmt.Printf("  %s  %-20s  local=%-24s  remote=%s\n", mark, label, localVal, remoteVal)
}

func abbreviate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func init() {
	wasmDiffCmd.Flags().BoolVar(&wasmDiffSemanticFlag, "semantic", false, "Use semantic/normalized diff mode instead of raw byte comparison")
	wasmDiffCmd.Flags().BoolVar(&wasmDiffIgnoreMetadataFlag, "ignore-metadata", true, "Ignore producer/build-id metadata sections in semantic mode (default: true)")
	wasmDiffCmd.Flags().BoolVar(&wasmDiffIgnoreDebugFlag, "ignore-debug", false, "Ignore DWARF/name debug sections in semantic mode (default: false)")
	wasmDiffCmd.Flags().BoolVar(&wasmDiffIgnoreUnknownFlag, "ignore-unknown-custom", true, "Ignore unrecognised custom sections in semantic mode (default: true)")
	rootCmd.AddCommand(wasmDiffCmd)
}
