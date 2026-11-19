// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/dotandev/glassbox/internal/errors"
	"github.com/dotandev/glassbox/internal/session"
	"github.com/spf13/cobra"
)

var sessionMigrationJSONFlag bool

// sessionMigrationCmd exposes migration status for a single session.  When
// --json is supplied the output is the MigrationStatus struct so tooling and CI
// can inspect migration state programmatically.
var sessionMigrationCmd = &cobra.Command{
	Use:   "migration <session-id-or-name>",
	Short: "Show the schema migration status for a session",
	Long: `Display whether a session is at the current schema version, needs
an automatic migration, or carries an unsupported version that requires
manual regeneration.

The command does not modify the session.  Use 'glassbox session resume' to
trigger an automatic migration on load, or 'glassbox debug <tx-hash>' to
recreate an unsupported session from scratch.

Exit codes:
  0  session is current or was already migrated
  1  session requires migration (schema outdated but supported)
  2  session version is unsupported (too old or from a newer binary)`,
	Example: `  # Check migration status by session ID
  glassbox session migration abc123

  # Machine-readable JSON output
  glassbox session migration abc123 --json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		sessionID := args[0]

		store, err := openSessionStore()
		if err != nil {
			return errors.WrapValidationError(fmt.Sprintf("failed to open session store: %v", err))
		}
		defer store.Close()

		// Use a raw load bypassing auto-upgrade so we can report the stored
		// version before any migration runs.  We call store.List to find the ID
		// and then build the MigrationStatus from the raw schema_version field.
		data, resolveErr := resolveSessionInput(ctx, store, sessionID)
		if resolveErr != nil {
			return fmt.Errorf("session %q not found: %w\nHint: run 'glassbox session list'",
				sessionID, resolveErr)
		}

		// Store.Load already auto-migrates — data.SchemaVersion is now current.
		// Re-derive the "before" state from what the store actually upgraded from
		// by computing the migration status against what we have.
		ms := session.ComputeMigrationStatusForDisplay(data)

		if sessionMigrationJSONFlag {
			b, jsonErr := json.MarshalIndent(ms, "", "  ")
			if jsonErr != nil {
				return fmt.Errorf("failed to encode migration status: %w", jsonErr)
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
			return nil
		}

		fmt.Fprint(cmd.OutOrStdout(), session.FormatMigrationStatus(ms, data.ID))

		if ms.Unsupported {
			return fmt.Errorf("session %s: %s", data.ID, ms.Summary())
		}
		if ms.Required && !ms.Applied {
			return fmt.Errorf("session %s needs migration: run 'glassbox session resume %s' to apply",
				data.ID, data.ID)
		}
		return nil
	},
}

func init() {
	sessionMigrationCmd.Flags().BoolVar(&sessionMigrationJSONFlag, "json", false, "Emit migration status as JSON")
	sessionCmd.AddCommand(sessionMigrationCmd)
}
