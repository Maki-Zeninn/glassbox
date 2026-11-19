// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/dotandev/glassbox/internal/errors"
	"github.com/dotandev/glassbox/internal/session"
	"github.com/spf13/cobra"
)

var (
	sessionGCDryRunFlag   bool
	sessionGCMaxAgeFlag   time.Duration
	sessionGCMaxCountFlag int
	sessionGCMaxSizeFlag  int64
	sessionGCStatusFlag   string
	sessionGCExcludeFlag  []string
	sessionGCRootFlag     string
)

var sessionGCCmd = &cobra.Command{
	Use:   "gc",
	Short: "List and clean up expired or excess sessions",
	Long: `List saved sessions with their age and size, and remove those that exceed
the configured retention policy.

Active sessions and bookmarked (named) sessions are never deleted, regardless
of age, count, or size. Use --dry-run to preview exactly what would be removed
without deleting anything.

Retention criteria (all that are set apply jointly):
  --max-age     Remove sessions older than this duration
  --max-count   Keep at most this many sessions
  --max-size    Keep total retained session bytes at or below this budget
  --status      Only remove sessions with this status (saved, expired, recovered)
  --exclude     Protect additional named sessions beyond bookmarks (repeatable)`,
	Example: `  # Preview what garbage collection would remove
  glassbox session gc --dry-run

  # Remove sessions older than 7 days, keeping at most 100
  glassbox session gc --max-age 168h --max-count 100

  # Enforce a 50 MiB total size budget
  glassbox session gc --max-size 52428800

  # Only consider expired sessions for removal
  glassbox session gc --status expired --dry-run

  # Run cleanup against a non-default data directory
  glassbox session gc --dry-run --root /path/to/.Glassbox`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		opts := session.DefaultGCOptions()
		if cmd.Flags().Changed("max-age") {
			opts.MaxAge = sessionGCMaxAgeFlag
		}
		if cmd.Flags().Changed("max-count") {
			opts.MaxCount = sessionGCMaxCountFlag
		}
		if cmd.Flags().Changed("max-size") {
			opts.MaxTotalSize = sessionGCMaxSizeFlag
		}
		if cmd.Flags().Changed("status") {
			opts.RequireStatus = sessionGCStatusFlag
		}
		if len(sessionGCExcludeFlag) > 0 {
			opts.ExcludeTags = sessionGCExcludeFlag
		}

		root := sessionGCRootFlag
		if root == "" {
			root = filepath.Dir(session.DefaultDBPath())
		}
		if err := session.ValidateGCRoot(root); err != nil {
			return errors.WrapValidationError(err.Error())
		}

		store, err := session.NewStoreAt(root)
		if err != nil {
			return errors.WrapValidationError(fmt.Sprintf("failed to open session store: %v", err))
		}
		defer store.Close()

		plan, err := store.RunGC(ctx, opts, sessionGCDryRunFlag)
		if err != nil {
			return err
		}

		out := cmd.OutOrStdout()
		if len(plan.Entries) == 0 {
			fmt.Fprintln(out, "No sessions found.")
			return nil
		}

		verb := "Would remove"
		if !sessionGCDryRunFlag {
			verb = "Removed"
		}

		fmt.Fprintf(out, "Sessions considered: %d (total size: %s)\n", len(plan.Entries), formatBytes(plan.TotalSize))
		for _, e := range plan.Entries {
			marker := " "
			switch {
			case e.Pinned:
				marker = "P" // pinned/bookmarked — protected
			case e.Active:
				marker = "A" // active — protected
			case e.Eligible:
				marker = "X" // eligible for deletion
			}
			corrupt := ""
			if e.Corrupt {
				corrupt = " [corrupt]"
			}
			fmt.Fprintf(out, "  [%s] %-24s age=%-12s size=%s%s\n",
				marker, e.ID, e.Age.Round(time.Minute), formatBytes(e.SizeBytes), corrupt)
		}

		if len(plan.ToDelete) == 0 {
			fmt.Fprintln(out, "\nNothing eligible for cleanup.")
			return nil
		}

		fmt.Fprintf(out, "\n%s %d session(s), freeing %s.\n", verb, len(plan.ToDelete), formatBytes(plan.DeleteSize()))
		if sessionGCDryRunFlag {
			fmt.Fprintln(out, "Re-run without --dry-run to perform the cleanup.")
		}
		return nil
	},
}

func init() {
	sessionGCCmd.Flags().BoolVar(&sessionGCDryRunFlag, "dry-run", false, "Preview what would be removed without deleting anything")
	sessionGCCmd.Flags().DurationVar(&sessionGCMaxAgeFlag, "max-age", session.DefaultTTL, "Maximum session age before it becomes eligible for cleanup")
	sessionGCCmd.Flags().IntVar(&sessionGCMaxCountFlag, "max-count", session.DefaultMaxSessions, "Maximum number of sessions to retain")
	sessionGCCmd.Flags().Int64Var(&sessionGCMaxSizeFlag, "max-size", 0, "Maximum total retained size in bytes; oldest excess is removed (0 = unlimited)")
	sessionGCCmd.Flags().StringVar(&sessionGCStatusFlag, "status", "", "Only remove sessions with this status: saved, expired, or recovered")
	sessionGCCmd.Flags().StringArrayVar(&sessionGCExcludeFlag, "exclude", nil, "Protect additional named sessions from deletion (repeatable)")
	sessionGCCmd.Flags().StringVar(&sessionGCRootFlag, "root", "", "Glassbox data directory to clean up (default: ~/.Glassbox)")

	sessionCmd.AddCommand(sessionGCCmd)
}
