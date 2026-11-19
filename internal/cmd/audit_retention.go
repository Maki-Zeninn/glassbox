// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/dotandev/glassbox/internal/audit"
	"github.com/dotandev/glassbox/internal/errors"
	"github.com/spf13/cobra"
)

var (
	auditRetentionDir         string
	auditRetentionMaxAge      time.Duration
	auditRetentionMaxSize     int64
	auditRetentionMaxSegments int
	auditRetentionDryRun      bool
	auditRetentionJSON        bool
)

var auditRetentionCmd = &cobra.Command{
	Use:     "audit:retention",
	GroupID: "utility",
	Short:   "Report or apply audit log segment retention",
	Long: `Evaluate retention against closed audit log segments and report exactly
what will be removed.

The active segment is never eligible. Closed segments are selected by age,
total size budget, and/or maximum segment count. Use --dry-run (the default
safety posture when you only want a report) to preview removals without
deleting files.

Verification metadata is preserved for retained segments: each closed segment
keeps its immutable manifest and previous_segment_hash chain link.

EXAMPLES
  # Report what retention would remove
  glassbox audit:retention --dir ./audit-logs --max-age 720h --dry-run

  # Keep at most 30 closed segments
  glassbox audit:retention --dir ./audit-logs --max-segments 30

  # Enforce a 100 MiB budget on closed segments
  glassbox audit:retention --dir ./audit-logs --max-size 104857600`,
	Args:    cobra.NoArgs,
	PreRunE: auditRetentionPreRunE,
	RunE:    runAuditRetention,
}

func auditRetentionPreRunE(cmd *cobra.Command, args []string) error {
	if auditRetentionDir == "" {
		return errors.WrapCliArgumentRequired("dir")
	}
	info, err := os.Stat(auditRetentionDir)
	if err != nil {
		return errors.WrapValidationError(fmt.Sprintf("--dir %q is not readable: %v", auditRetentionDir, err))
	}
	if !info.IsDir() {
		return errors.WrapValidationError(fmt.Sprintf("--dir %q must be a directory", auditRetentionDir))
	}
	if auditRetentionMaxAge < 0 {
		return errors.WrapValidationError("--max-age must be >= 0")
	}
	if auditRetentionMaxSize < 0 {
		return errors.WrapValidationError("--max-size must be >= 0")
	}
	if auditRetentionMaxSegments < 0 {
		return errors.WrapValidationError("--max-segments must be >= 0")
	}
	if auditRetentionMaxAge == 0 && auditRetentionMaxSize == 0 && auditRetentionMaxSegments == 0 {
		return errors.WrapValidationError("at least one of --max-age, --max-size, or --max-segments must be set")
	}
	return nil
}

func init() {
	auditRetentionCmd.Flags().StringVar(&auditRetentionDir, "dir", "", "Path to the audit log directory (required)")
	auditRetentionCmd.Flags().DurationVar(&auditRetentionMaxAge, "max-age", 0, "Remove closed segments older than this duration (0 = unlimited)")
	auditRetentionCmd.Flags().Int64Var(&auditRetentionMaxSize, "max-size", 0, "Keep total closed-segment bytes at or below this budget (0 = unlimited)")
	auditRetentionCmd.Flags().IntVar(&auditRetentionMaxSegments, "max-segments", 0, "Keep at most this many closed segments (0 = unlimited)")
	auditRetentionCmd.Flags().BoolVar(&auditRetentionDryRun, "dry-run", false, "Report what will be removed without deleting")
	auditRetentionCmd.Flags().BoolVar(&auditRetentionJSON, "json", false, "Output the retention plan as JSON")
	rootCmd.AddCommand(auditRetentionCmd)
}

func runAuditRetention(cmd *cobra.Command, args []string) error {
	cfg := audit.RetentionConfig{
		MaxAge:       auditRetentionMaxAge,
		MaxSizeBytes: auditRetentionMaxSize,
		MaxSegments:  auditRetentionMaxSegments,
	}

	plan, err := audit.ApplyRetention(auditRetentionDir, cfg, auditRetentionDryRun)
	if err != nil {
		return errors.WrapValidationError(err.Error())
	}

	if auditRetentionJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		payload := map[string]interface{}{
			"dry_run":      auditRetentionDryRun,
			"keep":         plan.Keep,
			"remove":       plan.Remove,
			"remove_count": len(plan.Remove),
			"remove_bytes": plan.TotalRemoveBytes(),
			"keep_count":   len(plan.Keep),
		}
		if err := enc.Encode(payload); err != nil {
			return errors.WrapMarshalFailed(err)
		}
		return nil
	}

	out := cmd.OutOrStdout()
	verb := "Would remove"
	if !auditRetentionDryRun {
		verb = "Removed"
	}

	fmt.Fprintf(out, "Audit log directory: %s\n", auditRetentionDir)
	fmt.Fprintf(out, "Closed segments:     %d keep, %d remove\n", len(plan.Keep), len(plan.Remove))

	if len(plan.Remove) == 0 {
		fmt.Fprintln(out, "Nothing eligible for retention removal.")
		return nil
	}

	fmt.Fprintf(out, "\n%s %d segment(s), freeing %d bytes:\n", verb, len(plan.Remove), plan.TotalRemoveBytes())
	for _, s := range plan.Remove {
		fmt.Fprintf(out, "  - %s (seq=%d size=%d)\n", s.Manifest.Segment, s.Manifest.Sequence, s.SizeBytes)
	}
	if auditRetentionDryRun {
		fmt.Fprintln(out, "\nRe-run without --dry-run to perform the cleanup.")
	}
	return nil
}
