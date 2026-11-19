// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// snapshot_integrity.go — CLI subcommands for offline snapshot integrity
// checking and hash repair.
//
// Two subcommands are added to the existing `snapshot` parent command:
//
//	glassbox snapshot verify  — load a registry, run VerifyIntegrityFull,
//	                            and report per-entry status. Exits non-zero
//	                            on any tampered entry.
//
//	glassbox snapshot repair  — load a registry, back-fill any missing
//	                            ContentHash values, and rewrite the file
//	                            atomically. Safe to run on legacy registries.
//
// Neither command requires signing keys. Integrity is detection, not
// authenticity — see docs/snapshot-integrity.md for the distinction.

package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/dotandev/glassbox/internal/errors"
	"github.com/dotandev/glassbox/internal/replay"
	"github.com/spf13/cobra"
)

var (
	snapVerifyPathFlag   string
	snapVerifyJSONFlag   bool

	snapRepairPathFlag   string
	snapRepairDryRunFlag bool
)

// ── glassbox snapshot verify ──────────────────────────────────────────────────

var snapshotVerifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Verify the content integrity of a snapshot registry (offline, no keys required)",
	Long: `Load a snapshot registry file and verify each entry's content hash using
canonical JSON (SHA-256, sorted keys, no extra whitespace).

This check is integrity detection, not authenticity. It detects accidental
truncation or on-disk modification but does NOT prove who created the file.
For authenticity use the audit signing commands (glassbox audit sign/verify).

Legacy registries saved before content hashes were introduced are reported
as "legacy" and not treated as failures — their hashes are computed and
displayed but not written back. Run 'glassbox snapshot repair' to persist
the back-filled hashes.

Exit codes:
  0 — all entries are OK or legacy (no tampered entries)
  1 — one or more entries have hash mismatches (tampered or error)

Examples:
  # Verify a registry file
  glassbox snapshot verify --path ./replay.json

  # Verify and emit machine-readable JSON diagnostics
  glassbox snapshot verify --path ./replay.json --json`,
	RunE: runSnapshotVerify,
}

func init() {
	snapshotVerifyCmd.Flags().StringVar(&snapVerifyPathFlag, "path", "", "Path to the snapshot registry file")
	snapshotVerifyCmd.Flags().BoolVar(&snapVerifyJSONFlag, "json", false, "Emit diagnostics as JSON (for CI / machine consumption)")
	snapshotCmd.AddCommand(snapshotVerifyCmd)
}

func runSnapshotVerify(_ *cobra.Command, _ []string) error {
	if snapVerifyPathFlag == "" {
		return errors.WrapCliArgumentRequired("path")
	}

	// Validate path before doing any I/O.
	if info, err := os.Stat(snapVerifyPathFlag); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf(
				"registry file %q not found\n"+
					"  Fix: provide the path to a registry JSON file saved by 'glassbox debug'",
				snapVerifyPathFlag,
			)
		}
		return fmt.Errorf("failed to stat registry file %q: %w", snapVerifyPathFlag, err)
	} else if info.IsDir() {
		return fmt.Errorf(
			"--path %q is a directory, not a registry file\n"+
				"  Fix: provide the full path to the registry JSON file",
			snapVerifyPathFlag,
		)
	}

	reg, err := replay.LoadFromFile(snapVerifyPathFlag)
	if err != nil {
		return errors.WrapValidationError(fmt.Sprintf("failed to load registry: %v", err))
	}

	report := reg.VerifyIntegrityFull()

	if snapVerifyJSONFlag {
		return printVerifyJSON(report, snapVerifyPathFlag)
	}
	return printVerifyHuman(report, snapVerifyPathFlag)
}

// printVerifyHuman renders a human-readable integrity report.
func printVerifyHuman(report *replay.RegistryIntegrityReport, path string) error {
	fmt.Printf("Snapshot registry: %s\n", path)
	fmt.Printf("Algorithm:         %s\n", report.Algorithm)
	fmt.Printf("Entries:           %d total — %d OK, %d legacy, %d tampered, %d error\n",
		len(report.Results),
		report.OKCount, report.LegacyCount, report.TamperedCount, report.ErrorCount,
	)

	if len(report.Results) > 0 {
		fmt.Println()
		for _, r := range report.Results {
			icon := statusIcon(r.Status)
			switch r.Status {
			case replay.IntegrityOK:
				fmt.Printf("  %s entry[%d] ts=%-12d hash=%s\n", icon, r.Index, r.Timestamp, r.ComputedHash[:16])
			case replay.IntegrityLegacy:
				fmt.Printf("  %s entry[%d] ts=%-12d (no stored hash — legacy file; back-filled %s)\n",
					icon, r.Index, r.Timestamp, r.ComputedHash[:16])
			case replay.IntegrityTampered:
				fmt.Printf("  %s entry[%d] ts=%-12d stored=%s computed=%s\n",
					icon, r.Index, r.Timestamp, r.StoredHash[:16], r.ComputedHash[:16])
			case replay.IntegrityError:
				fmt.Printf("  %s entry[%d] ts=%-12d error: %v\n", icon, r.Index, r.Timestamp, r.Err)
			}
		}
	}

	fmt.Println()
	if report.Passed() {
		if report.LegacyCount > 0 {
			fmt.Printf("Integrity: OK (with %d legacy entries — run 'glassbox snapshot repair' to persist hashes)\n", report.LegacyCount)
		} else {
			fmt.Println("Integrity: OK")
		}
		return nil
	}

	errs := report.Errors()
	fmt.Printf("Integrity: FAILED (%d issue(s))\n", len(errs))
	for _, e := range errs {
		fmt.Printf("  • %v\n", e)
	}
	fmt.Println("\nIf the file was modified intentionally, re-run the debug command to regenerate the registry.")
	return fmt.Errorf("snapshot integrity check failed: %d tampered, %d errors", report.TamperedCount, report.ErrorCount)
}

// printVerifyJSON emits a machine-readable JSON report to stdout.
func printVerifyJSON(report *replay.RegistryIntegrityReport, path string) error {
	type entryJSON struct {
		Index        int    `json:"index"`
		Timestamp    int64  `json:"timestamp"`
		Status       string `json:"status"`
		StoredHash   string `json:"stored_hash,omitempty"`
		ComputedHash string `json:"computed_hash,omitempty"`
		Error        string `json:"error,omitempty"`
	}
	type reportJSON struct {
		Path          string      `json:"path"`
		Algorithm     string      `json:"algorithm"`
		Passed        bool        `json:"passed"`
		TotalEntries  int         `json:"total_entries"`
		OKCount       int         `json:"ok"`
		LegacyCount   int         `json:"legacy"`
		TamperedCount int         `json:"tampered"`
		ErrorCount    int         `json:"error"`
		Entries       []entryJSON `json:"entries"`
	}

	entries := make([]entryJSON, 0, len(report.Results))
	for _, r := range report.Results {
		e := entryJSON{
			Index:        r.Index,
			Timestamp:    r.Timestamp,
			Status:       string(r.Status),
			ComputedHash: r.ComputedHash,
		}
		if r.StoredHash != "" {
			e.StoredHash = r.StoredHash
		}
		if r.Err != nil {
			e.Error = r.Err.Error()
		}
		entries = append(entries, e)
	}

	out := reportJSON{
		Path:          path,
		Algorithm:     report.Algorithm,
		Passed:        report.Passed(),
		TotalEntries:  len(report.Results),
		OKCount:       report.OKCount,
		LegacyCount:   report.LegacyCount,
		TamperedCount: report.TamperedCount,
		ErrorCount:    report.ErrorCount,
		Entries:       entries,
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return fmt.Errorf("failed to encode JSON report: %w", err)
	}

	if !report.Passed() {
		return fmt.Errorf("snapshot integrity check failed: %d tampered, %d errors", report.TamperedCount, report.ErrorCount)
	}
	return nil
}

func statusIcon(s replay.IntegrityStatus) string {
	switch s {
	case replay.IntegrityOK:
		return "✓"
	case replay.IntegrityLegacy:
		return "○"
	case replay.IntegrityTampered:
		return "✗"
	case replay.IntegrityError:
		return "!"
	default:
		return "?"
	}
}

// ── glassbox snapshot repair ──────────────────────────────────────────────────

var snapshotRepairCmd = &cobra.Command{
	Use:   "repair",
	Short: "Back-fill missing content hashes in a snapshot registry and rewrite it",
	Long: `Load a snapshot registry file, compute canonical content hashes for any
entries that do not yet have one (legacy entries), and rewrite the file
atomically.

Repair is safe to run on any registry:
  - Entries that already have a valid ContentHash are untouched.
  - Entries with a hash mismatch (tampered) are NOT repaired — they are
    reported and the command exits with a non-zero code so you can
    investigate before overwriting.
  - Only legacy entries (ContentHash == "") are back-filled.

Use --dry-run to see what would change without writing any files.

Examples:
  # Repair a registry (write back-filled hashes)
  glassbox snapshot repair --path ./replay.json

  # Preview what would be repaired without writing
  glassbox snapshot repair --path ./replay.json --dry-run`,
	RunE: runSnapshotRepair,
}

func init() {
	snapshotRepairCmd.Flags().StringVar(&snapRepairPathFlag, "path", "", "Path to the snapshot registry file to repair")
	snapshotRepairCmd.Flags().BoolVar(&snapRepairDryRunFlag, "dry-run", false, "Preview changes without writing any files")
	snapshotCmd.AddCommand(snapshotRepairCmd)
}

func runSnapshotRepair(_ *cobra.Command, _ []string) error {
	if snapRepairPathFlag == "" {
		return errors.WrapCliArgumentRequired("path")
	}

	if info, err := os.Stat(snapRepairPathFlag); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("registry file %q not found", snapRepairPathFlag)
		}
		return fmt.Errorf("failed to stat registry file: %w", err)
	} else if info.IsDir() {
		return fmt.Errorf("--path %q is a directory, not a registry file", snapRepairPathFlag)
	}

	reg, err := replay.LoadFromFile(snapRepairPathFlag)
	if err != nil {
		return errors.WrapValidationError(fmt.Sprintf("failed to load registry: %v", err))
	}

	report := reg.VerifyIntegrityFull()

	// Abort if any entry is tampered — never silently overwrite suspicious data.
	if report.TamperedCount > 0 || report.ErrorCount > 0 {
		fmt.Printf("Repair aborted: %d tampered and %d errored entries detected.\n",
			report.TamperedCount, report.ErrorCount)
		fmt.Println("Investigate the tampered entries before rewriting the registry.")
		for _, e := range report.Errors() {
			fmt.Printf("  • %v\n", e)
		}
		return fmt.Errorf("repair aborted: tampered entries present")
	}

	if report.LegacyCount == 0 {
		fmt.Println("Nothing to repair: all entries already have content hashes.")
		return nil
	}

	fmt.Printf("Found %d legacy entries to back-fill.\n", report.LegacyCount)

	if snapRepairDryRunFlag {
		fmt.Println("Dry-run: no files written.")
		for _, r := range report.Results {
			if r.Status == replay.IntegrityLegacy {
				fmt.Printf("  would back-fill entry[%d] ts=%d with hash=%s\n",
					r.Index, r.Timestamp, r.ComputedHash[:16])
			}
		}
		return nil
	}

	// VerifyIntegrityFull already mutated reg.Entries[i].ContentHash in-memory
	// for legacy entries. Persist the updated registry atomically.
	if err := reg.SaveToFile(snapRepairPathFlag); err != nil {
		return fmt.Errorf("failed to rewrite registry: %w", err)
	}

	fmt.Printf("Repaired %d legacy entries and rewrote %s\n", report.LegacyCount, snapRepairPathFlag)
	return nil
}
