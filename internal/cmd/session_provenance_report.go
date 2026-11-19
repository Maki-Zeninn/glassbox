// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dotandev/glassbox/internal/errors"
	"github.com/dotandev/glassbox/internal/session"
	"github.com/dotandev/glassbox/internal/version"
	"github.com/spf13/cobra"
)

var (
	sessionProvenanceReportJSONFlag   bool
	sessionProvenanceReportVerifyFlag bool
)

// provenanceReportOutput is the JSON-serialisable form of the full provenance
// report for a session.  It combines the ProvenanceTimeline (state transitions)
// with the ProvenanceChain (input/output hash linkage) so a single command
// provides a complete picture.
type provenanceReportOutput struct {
	SessionID       string                     `json:"session_id"`
	GeneratedAt     time.Time                  `json:"generated_at"`
	GlassboxVersion string                     `json:"glassbox_version"`
	Timeline        *session.ProvenanceTimeline `json:"timeline"`
	Chain           *session.ProvenanceChain   `json:"chain,omitempty"`
	ChainIntegrity  []string                   `json:"chain_integrity_issues,omitempty"`
	ChainValid      bool                       `json:"chain_valid"`
}

var sessionProvenanceReportCmd = &cobra.Command{
	Use:   "provenance-report <session-id-or-name>",
	Short: "Export a deterministic provenance report for a session",
	Long: `Generate a complete provenance report for a session, combining:

  • The session's state-transition timeline (ProvenanceTimeline) — every
    operation recorded since the session was first created.
  • The input/output hash chain (ProvenanceChain) — a causal record linking
    each operation's inputs and derived outputs by SHA-256 content hash, tool
    version, and timestamp.

The report is deterministic: running it twice on the same session without
intervening modifications produces identical output.

Modifying an input after the session was created produces a clear chain
mismatch when --verify is supplied, so reviewers can detect silent changes.

Secrets and private source contents are excluded from the report.  Only
content hashes, tool versions, timestamps, and non-sensitive configuration
appear.

WHAT THIS PROVES
  A complete provenance report proves that specific, unchanged inputs were
  consumed by a recorded version of Glassbox to produce the recorded outputs.

WHAT THIS DOES NOT PROVE
  Correctness of the tool, security of the environment, or that credentials
  were not compromised.

EXAMPLES
  # Human-readable report
  glassbox session provenance-report abc123

  # JSON report for programmatic consumption
  glassbox session provenance-report abc123 --json

  # Report with chain integrity verification
  glassbox session provenance-report abc123 --verify`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		sessionID := args[0]

		store, err := openSessionStore()
		if err != nil {
			return errors.WrapValidationError(fmt.Sprintf("failed to open session store: %v", err))
		}
		defer store.Close()

		data, err := resolveSessionInput(ctx, store, sessionID)
		if err != nil {
			return fmt.Errorf("session %q not found: %w\nHint: run 'glassbox session list'",
				sessionID, err)
		}

		timeline := session.ParseProvenanceTimeline(data.ProvenanceJSON)
		chain := session.SessionProvenanceChain(data)

		var chainIntegrity []string
		chainValid := true
		if sessionProvenanceReportVerifyFlag && len(chain.Records) > 0 {
			chainIntegrity = chain.VerifyChainIntegrity()
			chainValid = len(chainIntegrity) == 0
		} else if len(chain.Records) == 0 {
			// No chain recorded yet — build a genesis record from the current session.
			r := session.BuildSessionProvenanceRecord(data, version.Version)
			if appendErr := chain.Append(r); appendErr == nil {
				_ = session.AttachProvenanceChain(data, chain)
			}
			chainValid = true
		}

		report := provenanceReportOutput{
			SessionID:      data.ID,
			GeneratedAt:    time.Now().UTC(),
			GlassboxVersion: version.Version,
			Timeline:       timeline,
			Chain:          chain,
			ChainIntegrity: chainIntegrity,
			ChainValid:     chainValid,
		}

		if sessionProvenanceReportJSONFlag {
			b, jsonErr := json.MarshalIndent(report, "", "  ")
			if jsonErr != nil {
				return fmt.Errorf("failed to encode report: %w", jsonErr)
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
			if !chainValid {
				return fmt.Errorf("session %s: provenance chain integrity check failed (%d issue(s))",
					data.ID, len(chainIntegrity))
			}
			return nil
		}

		return printProvenanceReport(cmd, &report)
	},
}

func printProvenanceReport(cmd *cobra.Command, r *provenanceReportOutput) error {
	out := cmd.OutOrStdout()
	sep := strings.Repeat("─", 60)

	fmt.Fprintln(out, "Session Provenance Report")
	fmt.Fprintln(out, sep)
	fmt.Fprintf(out, "  Session:         %s\n", r.SessionID)
	fmt.Fprintf(out, "  Generated:       %s\n", r.GeneratedAt.Format(time.RFC3339))
	fmt.Fprintf(out, "  Glassbox:        %s\n", r.GlassboxVersion)
	fmt.Fprintln(out)

	// Timeline.
	fmt.Fprintln(out, "State Transition Timeline")
	fmt.Fprintln(out, sep)
	fmt.Fprint(out, r.Timeline.RenderText())
	fmt.Fprintln(out)

	// Provenance chain.
	fmt.Fprintln(out, "Input/Output Hash Chain")
	fmt.Fprintln(out, sep)
	if r.Chain == nil || len(r.Chain.Records) == 0 {
		fmt.Fprintln(out, "  No provenance chain recorded.")
	} else {
		for i, rec := range r.Chain.Records {
			fmt.Fprintf(out, "  [%d] %s — %s (%s)\n",
				i+1, rec.Operation, rec.ToolVersion, rec.Timestamp.Format(time.RFC3339))
			if rec.RecordID != "" {
				fmt.Fprintf(out, "      RecordID:    %s…%s\n",
					rec.RecordID[:8], rec.RecordID[len(rec.RecordID)-8:])
			}
			if rec.ChainPredecessorID != "" {
				fmt.Fprintf(out, "      Predecessor: %s…%s\n",
					rec.ChainPredecessorID[:8], rec.ChainPredecessorID[len(rec.ChainPredecessorID)-8:])
			}
			for _, in := range rec.Inputs {
				fmt.Fprintf(out, "      Input  [%s]: %s…%s (%d bytes)\n",
					in.Role, in.SHA256[:8], in.SHA256[len(in.SHA256)-8:], in.Size)
			}
			for _, out2 := range rec.Outputs {
				fmt.Fprintf(out, "      Output [%s]: %s…%s (%d bytes)\n",
					out2.Role, out2.SHA256[:8], out2.SHA256[len(out2.SHA256)-8:], out2.Size)
			}
		}
	}
	fmt.Fprintln(out)

	// Chain integrity.
	if r.ChainValid {
		fmt.Fprintf(out, "Chain integrity: VALID (%d record(s))\n", len(r.Chain.Records))
	} else {
		fmt.Fprintf(out, "Chain integrity: INVALID (%d issue(s)):\n", len(r.ChainIntegrity))
		for _, issue := range r.ChainIntegrity {
			fmt.Fprintf(out, "  • %s\n", issue)
		}
		return fmt.Errorf("session %s: provenance chain integrity check failed", r.SessionID)
	}
	return nil
}

func init() {
	sessionProvenanceReportCmd.Flags().BoolVar(&sessionProvenanceReportJSONFlag, "json", false,
		"Emit the report as JSON")
	sessionProvenanceReportCmd.Flags().BoolVar(&sessionProvenanceReportVerifyFlag, "verify", false,
		"Run chain integrity verification on the recorded provenance chain")
	sessionCmd.AddCommand(sessionProvenanceReportCmd)
}
