// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"

	"github.com/dotandev/glassbox/internal/telemetry"
	"github.com/spf13/cobra"
)

// telemetryCmd is the parent "telemetry" command group.
var telemetryCmd = &cobra.Command{
	Use:   "telemetry",
	Short: "Manage telemetry consent state",
	Long: `Manage the opt-in telemetry consent state for Glassbox.

Telemetry is disabled by default and is never enabled without your explicit
consent. The consent decision is persisted to:

  ~/.Glassbox/telemetry_consent.json

Environment variable GLASSBOX_TELEMETRY (true/false) overrides the persisted
state and takes the highest precedence.

No secrets are exported — transaction hashes, contract IDs, and file paths are
sanitized client-side before any data leaves the machine.

Subcommands:
  enable   Opt in to telemetry
  disable  Opt out of telemetry
  status   Show the current effective telemetry state`,
	// Running "glassbox telemetry" with no subcommand shows status.
	RunE: runTelemetryStatus,
}

// telemetryEnableCmd opts the user in to telemetry.
var telemetryEnableCmd = &cobra.Command{
	Use:   "enable",
	Short: "Opt in to anonymized command usage telemetry",
	Long: `Enable telemetry by writing an opt-in record to:

  ~/.Glassbox/telemetry_consent.json

The GLASSBOX_TELEMETRY environment variable, when set, takes precedence over
this file. Use 'glassbox telemetry status' to confirm the effective state.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := telemetry.WriteConsent(true); err != nil {
			return fmt.Errorf("failed to enable telemetry: %w", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Telemetry enabled.")
		fmt.Fprintf(cmd.OutOrStdout(), "Consent stored at: %s\n", telemetry.ConsentFilePath())
		fmt.Fprintln(cmd.OutOrStdout(), "\nTo disable at any time, run: glassbox telemetry disable")
		fmt.Fprintln(cmd.OutOrStdout(), "To disable for the current session only, run:")
		fmt.Fprintln(cmd.OutOrStdout(), "  export GLASSBOX_TELEMETRY=false")
		return nil
	},
}

// telemetryDisableCmd opts the user out of telemetry.
var telemetryDisableCmd = &cobra.Command{
	Use:   "disable",
	Short: "Opt out of telemetry",
	Long: `Disable telemetry by writing an opt-out record to:

  ~/.Glassbox/telemetry_consent.json

The GLASSBOX_TELEMETRY environment variable, when set, takes precedence over
this file. Use 'glassbox telemetry status' to confirm the effective state.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := telemetry.WriteConsent(false); err != nil {
			return fmt.Errorf("failed to disable telemetry: %w", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Telemetry disabled.")
		fmt.Fprintf(cmd.OutOrStdout(), "Consent stored at: %s\n", telemetry.ConsentFilePath())
		return nil
	},
}

// telemetryStatusCmd shows the current effective telemetry state.
var telemetryStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the current effective telemetry state",
	RunE:  runTelemetryStatus,
}

// runTelemetryStatus prints the current effective telemetry state to cmd's
// output writer.
func runTelemetryStatus(cmd *cobra.Command, args []string) error {
	ec := telemetry.ResolveConsent()

	fmt.Fprintln(cmd.OutOrStdout(), "Telemetry status:")
	fmt.Fprintln(cmd.OutOrStdout())

	// Report source and effective state.
	switch ec.Source {
	case telemetry.ConsentSourceEnv:
		fmt.Fprintf(cmd.OutOrStdout(), "  Source:   environment variable (GLASSBOX_TELEMETRY=%s)\n", ec.EnvValue)
		fmt.Fprintf(cmd.OutOrStdout(), "  Enabled:  %v  (env override — takes precedence over consent file)\n", ec.Enabled)
	case telemetry.ConsentSourceFile:
		state, err := telemetry.ReadConsent()
		if err == nil {
			fmt.Fprintf(cmd.OutOrStdout(), "  Source:   consent file (%s)\n", telemetry.ConsentFilePath())
			fmt.Fprintf(cmd.OutOrStdout(), "  Enabled:  %v\n", state.Enabled)
			if state.UpdatedAt != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "  Updated:  %s\n", state.UpdatedAt)
			}
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "  Source:   consent file (error reading: %v)\n", err)
			fmt.Fprintf(cmd.OutOrStdout(), "  Enabled:  %v  (defaulting to disabled)\n", false)
		}
	default: // ConsentSourceDefault
		fmt.Fprintln(cmd.OutOrStdout(), "  Source:   default (no consent file, no env override)")
		fmt.Fprintln(cmd.OutOrStdout(), "  Enabled:  false")
	}

	fmt.Fprintln(cmd.OutOrStdout())

	// Always show how to change it.
	if ec.Enabled {
		fmt.Fprintln(cmd.OutOrStdout(), "To disable telemetry persistently:")
		fmt.Fprintln(cmd.OutOrStdout(), "  glassbox telemetry disable")
		fmt.Fprintln(cmd.OutOrStdout(), "To disable for the current shell session only:")
		fmt.Fprintln(cmd.OutOrStdout(), "  export GLASSBOX_TELEMETRY=false")
	} else {
		fmt.Fprintln(cmd.OutOrStdout(), "To enable telemetry persistently:")
		fmt.Fprintln(cmd.OutOrStdout(), "  glassbox telemetry enable")
		fmt.Fprintln(cmd.OutOrStdout(), "To enable for the current shell session only:")
		fmt.Fprintln(cmd.OutOrStdout(), "  export GLASSBOX_TELEMETRY=true")
	}

	return nil
}

func init() {
	telemetryCmd.AddCommand(telemetryEnableCmd)
	telemetryCmd.AddCommand(telemetryDisableCmd)
	telemetryCmd.AddCommand(telemetryStatusCmd)
	rootCmd.AddCommand(telemetryCmd)
}
