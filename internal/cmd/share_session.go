// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dotandev/glassbox/internal/errors"
	"github.com/dotandev/glassbox/internal/security"
	"github.com/dotandev/glassbox/internal/session"
	"github.com/spf13/cobra"
)

var sessionShareCmd = &cobra.Command{
	Use:   "share [session-id]",
	Short: "Export a debug session as a portable archive",
	Long: `Package a saved debug session into a self-contained archive file (.gbx).

The archive contains all replay inputs, simulation results, and metadata
required to reproduce the session on another machine. Load the archive with
'Glassbox session load <archive>'.

If no session-id is provided, the currently active session is archived.

Validation:
  The session data is validated before export so that corrupt or incomplete
  sessions are rejected early with a clear diagnostic rather than silently
  producing an archive that cannot be imported on the other side.`,
	Example: `  # Export the active session
  Glassbox session share

  # Export a specific saved session
  Glassbox session share abc123 --output ./debug-session.gbx`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		outputFlag, _ := cmd.Flags().GetString("output")
		redactFlag, _ := cmd.Flags().GetString("redact")
		previewFlag, _ := cmd.Flags().GetBool("preview")

		// Parse secret scan mode
		var scanMode security.ScannerMode
		if secretScanModeFlag != "" {
			switch strings.ToUpper(strings.TrimSpace(secretScanModeFlag)) {
			case "OPT_IN":
				scanMode = security.ModeOptIn
			case "STRICT":
				scanMode = security.ModeStrict
			default:
				return errors.WrapValidationError(
					fmt.Sprintf(
						"--secret-scan-mode must be either 'opt-in' or 'strict', got %q\n"+
							"  Fix: use --secret-scan-mode opt-in (warn only) or --secret-scan-mode strict (block export)",
						secretScanModeFlag,
					),
				)
			}
		}

		profile, err := session.ParseRedactionProfile(redactFlag)
		if err != nil {
			return errors.WrapValidationError(err.Error())
		}

		var data *session.Data

		if len(args) == 0 {
			// Use the active session.
			data = GetCurrentSession()
			if data == nil {
				return errors.WrapSimulationLogicError(
					"no active session to share. Run 'Glassbox debug <tx-hash>' first or specify a session-id",
				)
			}
		} else {
			// Load a saved session by ID.
			store, err := openSessionStore()
			if err != nil {
				return errors.WrapValidationError(fmt.Sprintf("failed to open session store: %v", err))
			}
			defer store.Close()

			data, err = resolveSessionInput(ctx, store, args[0])
			if err != nil {
				return err
			}
		}

		// Determine the output path.
		dest := outputFlag
		if dest == "" {
			safeName := strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(data.ID)
			dest = fmt.Sprintf("glassbox-session-%s-%s.gbx",
				safeName, time.Now().UTC().Format("20060102-150405"))
		}
		// Ensure parent directory exists.
		if dir := filepath.Dir(dest); dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return errors.WrapValidationError(fmt.Sprintf("failed to create output directory: %v", err))
			}
		}

		redacted, report, err := session.RedactSession(data, profile)
		if err != nil {
			return fmt.Errorf("failed to apply redaction profile: %w", err)
		}

		if previewFlag {
			printRedactionReport(cmd.OutOrStdout(), report)
			return nil
		}
		if report.Profile != session.RedactionFull {
			printRedactionReport(cmd.OutOrStdout(), report)
		}

		// Export with secret scanning options
		archiveOpts := session.ArchiveOptions{
			SecretScanMode:      scanMode,
			SecretScanOverrides: secretScanOverrideFlag,
			RedactionReport:     report,
		}
		if err := session.ExportArchiveWithOptions(redacted, dest, archiveOpts); err != nil {
			return fmt.Errorf("failed to export session archive: %w", err)
		}

		info, _ := os.Stat(dest)
		size := int64(0)
		if info != nil {
			size = info.Size()
		}

		fmt.Printf("Session exported: %s\n", dest)
		fmt.Printf("  Session ID:  %s\n", data.ID)
		fmt.Printf("  Transaction: %s\n", data.TxHash)
		fmt.Printf("  Network:     %s\n", data.Network)
		fmt.Printf("  Redaction:   %s\n", report.Profile)
		fmt.Printf("  Archive:     %s (%d bytes)\n", dest, size)
		fmt.Printf("\nTo load on another machine:\n")
		fmt.Printf("  Glassbox session load %s\n", dest)

		return nil
	},
}

var sessionLoadCmd = &cobra.Command{
	Use:   "load <archive>",
	Short: "Load a shared debug session archive",
	Long: `Restore a session from a .gbx archive created by 'Glassbox session share'.

The restored session is set as the active session and can be saved persistently
with 'Glassbox session save'. Source mappings and simulation results bundled in
the archive are available immediately without re-fetching from the network.`,
	Example: `  # Load an exported session archive
  Glassbox session load ./glassbox-session-abc123.gbx

  # Save the loaded session for later use
  Glassbox session load ./glassbox-session-abc123.gbx
  Glassbox session save --id restored-session`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		archivePath := args[0]

		data, err := session.ImportArchive(archivePath)
		if err != nil {
			return fmt.Errorf("failed to load session archive: %w", err)
		}

		if schemaErr := session.ValidateSchemaVersion(data.SchemaVersion, data.ID); schemaErr != nil {
			return schemaErr
		}
		if upgraded, upgradeErr := session.UpgradeSessionData(data); upgradeErr != nil {
			return upgradeErr
		} else if upgraded {
			fmt.Fprintf(cmd.ErrOrStderr(), "Session schema upgraded to version %d.\n", session.SchemaVersion)
		}

		report := session.ValidateIntegrity(data)
		if !report.OK {
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("loaded session failed integrity validation (%d issue(s)):\n", len(report.Issues)))
			for i, issue := range report.Issues {
				sb.WriteString(fmt.Sprintf("  %d. [%s] %s\n", i+1, issue.Field, issue.Description))
				if issue.Hint != "" {
					sb.WriteString(fmt.Sprintf("     Hint: %s\n", issue.Hint))
				}
			}
			return fmt.Errorf("%s", sb.String())
		}

		// Mark as active session.
		data.Status = "resumed"
		data.LastAccessAt = time.Now()
		SetCurrentSession(data)

		fmt.Printf("Session loaded from archive: %s\n", archivePath)
		fmt.Printf("  Session ID:  %s\n", data.ID)
		fmt.Printf("  Transaction: %s\n", data.TxHash)
		fmt.Printf("  Network:     %s\n", data.Network)
		fmt.Printf("  Created:     %s\n", data.CreatedAt.Format(time.RFC3339))

		if data.SimResponseJSON != "" {
			fmt.Printf("\nSimulation results are available. Run 'Glassbox trace' to view.\n")
		}
		fmt.Printf("\nTo persist this session:\n")
		fmt.Printf("  Glassbox session save --id <name>\n")

		return nil
	},
}

// printRedactionReport renders a RedactionReport as a preview the operator
// can review before an archive is written — or, when redaction was applied
// silently to a real export, as a record of what changed.
func printRedactionReport(w io.Writer, report *session.RedactionReport) {
	fmt.Fprintf(w, "Redaction profile: %s\n", report.Profile)
	for _, f := range report.Fields {
		status := "kept"
		switch f.Policy {
		case session.PolicyRedact:
			status = "removed"
		case session.PolicyPseudonymize:
			status = "pseudonymized"
		}
		if !f.Applied {
			fmt.Fprintf(w, "  - %-22s %s (nothing to change)\n", f.Field, status)
			continue
		}
		fmt.Fprintf(w, "  - %-22s %s: %s\n", f.Field, status, f.Sample)
	}
	if report.IdentifiersPseudonymized > 0 {
		fmt.Fprintf(w, "%d unique identifier(s) pseudonymized.\n", report.IdentifiersPseudonymized)
	}
}

func init() {
	sessionShareCmd.Flags().StringP("output", "o", "", "Output archive path (default: auto-generated .gbx file)")
	sessionShareCmd.Flags().String("redact", "full", "Redaction profile applied before export: strict, balanced, or full (default: full, unredacted)")
	sessionShareCmd.Flags().Bool("preview", false, "Show what --redact would remove without writing an archive")
	sessionShareCmd.Flags().String("secret-scan-mode", "", "Secret scanning mode: opt-in (warn only) or strict (block export)")
	sessionShareCmd.Flags().StringArray("secret-scan-override", nil, "Paths allowed to contain secrets (for test fixtures); repeatable")

	sessionCmd.AddCommand(sessionShareCmd)
	sessionCmd.AddCommand(sessionLoadCmd)
}
