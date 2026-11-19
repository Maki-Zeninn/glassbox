// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dotandev/glassbox/internal/errors"
	"github.com/dotandev/glassbox/internal/session"
	"github.com/dotandev/glassbox/internal/snapshot"
	"github.com/spf13/cobra"
)

var (
	compactDryRunFlag     bool
	compactForceFlag      bool
	compactMinAgeFlag     time.Duration
	compactPreserveFlag   []string
	compactRootFlag       string
	compactJSONFlag       bool
)

var compactCmd = &cobra.Command{
	Use:   "compact",
	Short: "Compact ledger state snapshots to reclaim disk space",
	Long: `Remove unreferenced ledger state snapshots to free disk space.

This command performs garbage collection on ledger state snapshots stored in
the deduplication store. It uses a mark-and-sweep algorithm to identify and
remove snapshots that are not referenced by any registry, session, or bundle.

Safety features:
  - Dry-run mode to preview changes without modifying data
  - Age-based protection to avoid removing recent snapshots
  - Explicit hash preservation for critical snapshots
  - Atomic commits with recovery manifests for crash safety
  - Reference tracking across registries, sessions, and bundles

The compaction never removes state referenced by a valid session, registry,
or bundle. Use --dry-run to preview exactly what would be removed.`,
	Example: `  # Preview what compaction would remove
  glassbox compact --dry-run

  # Remove unreferenced snapshots older than 7 days
  glassbox compact --min-age 168h

  # Preserve specific snapshots by content hash
  glassbox compact --preserve abc123... --preserve def456...

  # Force compaction even if interrupted previously
  glassbox compact --force

  # Run against a non-default data directory
  glassbox compact --root /path/to/.glassbox --dry-run`,
	Args: cobra.NoArgs,
	RunE: runCompact,
}

func init() {
	compactCmd.Flags().BoolVar(&compactDryRunFlag, "dry-run", false, "Preview what would be removed without deleting anything")
	compactCmd.Flags().BoolVar(&compactForceFlag, "force", false, "Force compaction even if interrupted previously (use with caution)")
	compactCmd.Flags().DurationVar(&compactMinAgeFlag, "min-age", 0, "Minimum age before a snapshot is eligible for removal (0 = no age restriction)")
	compactCmd.Flags().StringArrayVar(&compactPreserveFlag, "preserve", nil, "Preserve snapshots with these content hashes (repeatable)")
	compactCmd.Flags().StringVar(&compactRootFlag, "root", "", "Glassbox data directory to compact (default: ~/.glassbox)")
	compactCmd.Flags().BoolVar(&compactJSONFlag, "json", false, "Output compaction report as JSON")

	rootCmd.AddCommand(compactCmd)
}

func runCompact(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()

	// Determine root directory
	root := compactRootFlag
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return errors.WrapValidationError(fmt.Sprintf("failed to determine home directory: %v", err))
		}
		root = filepath.Join(home, ".glassbox")
	}

	// Validate root directory
	if err := session.ValidateGCRoot(root); err != nil {
		return errors.WrapValidationError(err.Error())
	}

	// Create dedup store
	snapshotDir := filepath.Join(root, "cache")
	dedupStore := snapshot.NewDedupStore(snapshotDir)

	// Build compaction options
	opts := snapshot.CompactionOptions{
		DryRun:         compactDryRunFlag,
		Force:          compactForceFlag,
		MinAge:         compactMinAgeFlag,
		PreserveHashes: compactPreserveFlag,
	}

	// Safety checks
	if err := snapshot.ValidateCompactionSafety(dedupStore, opts); err != nil {
		return errors.WrapValidationError(err.Error())
	}

	// Perform compaction
	if !compactJSONFlag {
		fmt.Fprintf(out, "Scanning ledger state snapshots in %s\n", snapshotDir)
		if !compactDryRunFlag {
			fmt.Fprintln(out, "Building reference model from registries, sessions, and bundles...")
		}
	}

	report, err := snapshot.CompactLedgerState(dedupStore, opts)
	if err != nil {
		return err
	}

	// Output report
	if compactJSONFlag {
		return outputCompactJSON(out, report)
	}

	return outputCompactText(out, report, opts.DryRun)
}

func outputCompactText(out interface{ WriteString(string) (int, error) }, report *snapshot.CompactionReport, dryRun bool) error {
	fmt.Fprintf(out, "\nCompaction Report\n")
	fmt.Fprintf(out, "================\n\n")
	fmt.Fprintf(out, "Before size:     %s\n", formatBytes(report.BeforeSize))
	fmt.Fprintf(out, "After size:      %s\n", formatBytes(report.AfterSize))
	fmt.Fprintf(out, "Reclaimed space: %s\n", formatBytes(report.ReclaimedBytes))
	fmt.Fprintf(out, "Duration:        %s\n", report.Duration.Round(time.Millisecond))
	fmt.Fprintln(out)

	fmt.Fprintf(out, "Snapshot Statistics:\n")
	fmt.Fprintf(out, "  Referenced:     %d\n", report.ReferencedCount)
	fmt.Fprintf(out, "  Unreferenced:   %d\n", report.UnreferencedCount)
	fmt.Fprintln(out)

	if len(report.RemovedHashes) > 0 {
		fmt.Fprintf(out, "Snapshots to remove: %d\n", len(report.RemovedHashes))
		for i, hash := range report.RemovedHashes {
			if i >= 10 {
				fmt.Fprintf(out, "  ... and %d more\n", len(report.RemovedHashes)-10)
				break
			}
			fmt.Fprintf(out, "  - %s\n", hash[:16])
		}
		fmt.Fprintln(out)
	}

	if len(report.PreservedHashes) > 0 {
		fmt.Fprintf(out, "Snapshots preserved: %d\n", len(report.PreservedHashes))
		for i, hash := range report.PreservedHashes {
			if i >= 10 {
				fmt.Fprintf(out, "  ... and %d more\n", len(report.PreservedHashes)-10)
				break
			}
			fmt.Fprintf(out, "  - %s\n", hash[:16])
		}
		fmt.Fprintln(out)
	}

	if len(report.Errors) > 0 {
		fmt.Fprintf(out, "Errors encountered: %d\n", len(report.Errors))
		for _, err := range report.Errors {
			fmt.Fprintf(out, "  - %s\n", err)
		}
		fmt.Fprintln(out)
	}

	if dryRun {
		fmt.Fprintln(out, "This was a dry-run. Re-run without --dry-run to perform compaction.")
	} else {
		fmt.Fprintln(out, "Compaction completed successfully.")
	}

	return nil
}

func outputCompactJSON(out interface{ WriteString(string) (int, error) }, report *snapshot.CompactionReport) error {
	type JSONReport struct {
		BeforeSize        int64    `json:"before_size_bytes"`
		AfterSize         int64    `json:"after_size_bytes"`
		ReclaimedBytes    int64    `json:"reclaimed_bytes"`
		ReferencedCount   int      `json:"referenced_count"`
		UnreferencedCount int      `json:"unreferenced_count"`
		RemovedHashes     []string `json:"removed_hashes"`
		PreservedHashes   []string `json:"preserved_hashes"`
		Errors            []string `json:"errors"`
		DurationMs        int64    `json:"duration_ms"`
	}

	jsonReport := JSONReport{
		BeforeSize:        report.BeforeSize,
		AfterSize:         report.AfterSize,
		ReclaimedBytes:    report.ReclaimedBytes,
		ReferencedCount:   report.ReferencedCount,
		UnreferencedCount: report.UnreferencedCount,
		RemovedHashes:     report.RemovedHashes,
		PreservedHashes:   report.PreservedHashes,
		Errors:            report.Errors,
		DurationMs:        report.Duration.Milliseconds(),
	}

	data, err := json.MarshalIndent(jsonReport, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal report: %w", err)
	}

	fmt.Fprintln(out, string(data))
	return nil
}
