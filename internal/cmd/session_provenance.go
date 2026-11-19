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

var sessionProvenanceJSONFlag bool

var sessionProvenanceCmd = &cobra.Command{
	Use:   "provenance <session-id-or-name>",
	Short: "Show a session's provenance timeline",
	Long: `Display the append-only history of state transitions recorded for a session:
when it was fetched, replayed, annotated, exported, imported, or migrated.

Each entry records the operation, the actor (user or system), the Glassbox
version that performed it, and whether it succeeded. The timeline is bounded
to the most recent entries and never includes raw host details.`,
	Example: `  # Show the timeline for a session
  glassbox session provenance abc123

  # Emit the timeline as JSON
  glassbox session provenance abc123 --json`,
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
			return fmt.Errorf(
				"session %q not found: %w\n"+
					"Hint: run 'glassbox session list' to see all available sessions",
				sessionID, err,
			)
		}

		timeline := session.ParseProvenanceTimeline(data.ProvenanceJSON)

		if sessionProvenanceJSONFlag {
			b, err := json.MarshalIndent(timeline, "", "  ")
			if err != nil {
				return fmt.Errorf("failed to encode provenance timeline: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
			return nil
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Session %s:\n", data.ID)
		fmt.Fprint(cmd.OutOrStdout(), timeline.RenderText())
		return nil
	},
}

func init() {
	sessionProvenanceCmd.Flags().BoolVar(&sessionProvenanceJSONFlag, "json", false, "Emit the timeline as JSON")
	sessionCmd.AddCommand(sessionProvenanceCmd)
}
