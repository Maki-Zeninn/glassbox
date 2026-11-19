// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/dotandev/glassbox/internal/audit"
	"github.com/dotandev/glassbox/internal/errors"
	"github.com/spf13/cobra"
)

var (
	auditVerifyDirPath            string
	auditVerifyDirJSON            bool
	auditVerifyDirFailFast        bool
	auditVerifyDirPolicyConfig    string
	auditVerifyDirExpectedSigners string
	auditVerifyDirExpectedSchema  string
	auditVerifyDirMaxRetentionDays int
	auditVerifyDirRequireChain    bool
)

var auditVerifyDirCmd = &cobra.Command{
	Use:     "audit:verify-dir",
	GroupID: "utility",
	Short:   "Verify audit log segment manifests, checksum chain, and directory policy",
	Long: `Verify an audit log directory produced by rotating audit output.

The command checks each closed segment against its immutable manifest,
validates SHA-256 digests, and walks previous_segment_hash links so the
directory verifies as a tamper-evident chain. Missing segments and
interrupted rotations (segment without manifest, or vice versa) are reported
explicitly.

POLICY CHECKS [Issue #806]
  Directory verification goes beyond per-file signature checks.  Pass a policy
  to detect violations that only emerge when viewing the collection as a whole:

  --expected-schema-version <ver>  Fail any segment with a different schema_version.
  --max-retention-days <n>         Fail any segment older than n days.
  --require-chain                  Fail if the hash chain is incomplete or broken.
  --expected-signers <csv>         Note when signer identity cannot be verified from
                                   manifest metadata alone (requires per-record audit:verify).
  --policy-config <json-file>      Load a full DirPolicy JSON file.  CLI flags override
                                   individual fields in the file.
  --fail-fast                      Stop after the first violation instead of collecting all.

  Violations are classified separately:
    structural issues  — missing manifest, checksum mismatch, broken chain
    policy violations  — schema version, retention, signer identity

  Results are deterministic regardless of filesystem ordering (segments are
  sorted by sequence number ascending) and do not stop after the first bad
  segment unless --fail-fast is set.  Partial results in --json output are
  valid for the portion that was checked.

OUTPUT
  Human-readable output shows per-segment and aggregate results with stable
  codes.  Machine-readable JSON includes per-file SegmentVerifyResult objects
  and aggregate counts.

EXAMPLES
  # Verify a log directory
  glassbox audit:verify-dir --dir ./audit-logs

  # Verify with schema version and retention policy
  glassbox audit:verify-dir --dir ./audit-logs \
    --expected-schema-version 1 \
    --max-retention-days 90

  # Load full policy from a JSON file
  glassbox audit:verify-dir --dir ./audit-logs --policy-config ./audit-policy.json

  # Stop after the first violation
  glassbox audit:verify-dir --dir ./audit-logs --fail-fast

  # Machine-readable JSON output with per-segment detail
  glassbox audit:verify-dir --dir ./audit-logs --json`,
	Args:    cobra.NoArgs,
	PreRunE: auditVerifyDirPreRunE,
	RunE:    runAuditVerifyDir,
}

func auditVerifyDirPreRunE(cmd *cobra.Command, args []string) error {
	if auditVerifyDirPath == "" {
		return errors.WrapCliArgumentRequired("dir")
	}
	info, err := os.Stat(auditVerifyDirPath)
	if err != nil {
		return errors.WrapValidationError(fmt.Sprintf("--dir %q is not readable: %v", auditVerifyDirPath, err))
	}
	if !info.IsDir() {
		return errors.WrapValidationError(fmt.Sprintf("--dir %q must be a directory", auditVerifyDirPath))
	}
	if auditVerifyDirPolicyConfig != "" {
		if _, err := os.Stat(auditVerifyDirPolicyConfig); err != nil {
			return errors.WrapValidationError(fmt.Sprintf("--policy-config %q is not readable: %v", auditVerifyDirPolicyConfig, err))
		}
	}
	if auditVerifyDirMaxRetentionDays < 0 {
		return errors.WrapValidationError("--max-retention-days must be >= 0")
	}
	return nil
}

func init() {
	auditVerifyDirCmd.Flags().StringVar(&auditVerifyDirPath, "dir", "", "Path to the audit log directory (required)")
	auditVerifyDirCmd.Flags().BoolVar(&auditVerifyDirJSON, "json", false, "Output verification result as JSON (includes per-segment detail)")
	auditVerifyDirCmd.Flags().BoolVar(&auditVerifyDirFailFast, "fail-fast", false, "Stop after the first structural or policy violation")
	auditVerifyDirCmd.Flags().StringVar(&auditVerifyDirPolicyConfig, "policy-config", "", "Path to a DirPolicy JSON file; CLI flags override individual fields")
	auditVerifyDirCmd.Flags().StringVar(&auditVerifyDirExpectedSigners, "expected-signers", "", "Comma-separated list of expected signer identity substrings")
	auditVerifyDirCmd.Flags().StringVar(&auditVerifyDirExpectedSchema, "expected-schema-version", "", "Expected manifest schema_version for all segments")
	auditVerifyDirCmd.Flags().IntVar(&auditVerifyDirMaxRetentionDays, "max-retention-days", 0, "Flag segments older than this many days (0 = disabled)")
	auditVerifyDirCmd.Flags().BoolVar(&auditVerifyDirRequireChain, "require-chain", false, "Treat a missing or broken hash chain as a policy violation")
	rootCmd.AddCommand(auditVerifyDirCmd)
}

// buildDirPolicy assembles a DirPolicy from the --policy-config file (if any)
// and then applies CLI flag overrides.
func buildDirPolicy() (*audit.DirPolicy, error) {
	var policy audit.DirPolicy

	// Load base policy from file when --policy-config is set.
	if auditVerifyDirPolicyConfig != "" {
		p, err := audit.LoadDirPolicy(auditVerifyDirPolicyConfig)
		if err != nil {
			return nil, errors.WrapValidationError(fmt.Sprintf("failed to load policy-config: %v", err))
		}
		policy = *p
	}

	// CLI flags override file values when explicitly set.
	if auditVerifyDirExpectedSigners != "" {
		parts := strings.Split(auditVerifyDirExpectedSigners, ",")
		for i, p := range parts {
			parts[i] = strings.TrimSpace(p)
		}
		policy.ExpectedSigners = parts
	}
	if auditVerifyDirExpectedSchema != "" {
		policy.ExpectedSchemaVersion = auditVerifyDirExpectedSchema
	}
	if auditVerifyDirMaxRetentionDays > 0 {
		policy.MaxRetentionDays = auditVerifyDirMaxRetentionDays
	}
	if auditVerifyDirRequireChain {
		policy.RequireHashChain = true
	}
	if auditVerifyDirFailFast {
		policy.FailFast = true
	}

	return &policy, nil
}

func runAuditVerifyDir(cmd *cobra.Command, args []string) error {
	policy, err := buildDirPolicy()
	if err != nil {
		return err
	}

	result, verifyErr := audit.VerifyDirectoryWithPolicy(auditVerifyDirPath, policy)
	if verifyErr != nil {
		return errors.WrapValidationError(verifyErr.Error())
	}

	if auditVerifyDirJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		if encErr := enc.Encode(result); encErr != nil {
			return errors.WrapMarshalFailed(encErr)
		}
	} else {
		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "Audit log directory: %s\n", result.Dir)
		fmt.Fprintf(out, "Segments checked:    %d\n", result.SegmentsChecked)
		fmt.Fprintf(out, "Active present:      %v\n", result.ActivePresent)
		fmt.Fprintf(out, "Chain valid:         %v\n", result.ChainValid)
		fmt.Fprintf(out, "Structural issues:   %d\n", result.StructuralIssues)
		fmt.Fprintf(out, "Policy violations:   %d\n", result.PolicyViolations)
		if result.Truncated {
			fmt.Fprintln(out, "Note: --fail-fast was set; results are partial (stopped after first violation).")
		}
		if result.FirstSegmentHash != "" {
			fmt.Fprintf(out, "First segment hash:  %s\n", result.FirstSegmentHash)
		}
		if result.LastSegmentHash != "" {
			fmt.Fprintf(out, "Last segment hash:   %s\n", result.LastSegmentHash)
		}

		// Per-segment summary.
		if len(result.Segments) > 0 {
			fmt.Fprintln(out)
			fmt.Fprintln(out, "Segment results:")
			for _, sr := range result.Segments {
				status := "PASS"
				if !sr.Valid {
					status = "FAIL"
				}
				fmt.Fprintf(out, "  [%s] seq=%d %s\n", status, sr.Sequence, sr.Segment)
				for _, issue := range sr.Issues {
					fmt.Fprintf(out, "         - %s\n", issue)
				}
			}
		}

		// Aggregate issues.
		if len(result.AggregateIssues) > 0 {
			fmt.Fprintln(out)
			fmt.Fprintln(out, "Issues:")
			for _, issue := range result.AggregateIssues {
				fmt.Fprintf(out, "  - %s\n", issue)
			}
		}

		fmt.Fprintln(out)
		if result.Valid {
			fmt.Fprintln(out, "Result: VALID — segment chain and policy verified.")
		} else {
			fmt.Fprintln(out, "Result: INVALID — audit log directory failed verification.")
		}
	}

	if !result.Valid {
		totalViolations := result.StructuralIssues + result.PolicyViolations
		return errors.WrapAuditDirPolicyViolation(
			"audit log directory failed verification",
			totalViolations,
		)
	}
	return nil
}
