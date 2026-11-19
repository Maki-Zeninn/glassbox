// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	stderrors "errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dotandev/glassbox/internal/errors"
	"github.com/dotandev/glassbox/internal/plan"
	"github.com/dotandev/glassbox/internal/session"
	"github.com/dotandev/glassbox/internal/version"
	"github.com/spf13/cobra"
)

var (
	sessionIDFlag          string
	sessionNameFlag        string
	sessionPinEndpointFlag string
	sessionSavePlanFlag    bool // --plan: show execution plan without saving
	sessionSaveForceFlag   bool // --force: overwrite even on revision conflict [Issue #813]
	sessionResumeReadOnly  bool // --read-only: resume without migrating [Issue #1115]
	sessionMigrateDryRun   bool // migrate --dry-run [Issue #1115]
	sessionMigrateIDFlag   string

	// Session encryption [Issue #560]. Persistent flags on sessionCmd so
	// every subcommand that opens the store (save, load, list, doctor,
	// import, share, gc) shares one configuration.
	sessionEncryptFlag       bool
	sessionKeyProviderFlag   string
	sessionKeyPassphraseFlag string

	// Secret scanning flags for session export
	secretScanModeFlag       string
	secretScanOverrideFlag   []string
)

// openSessionStore opens the session store at the default location and, if
// session encryption was requested via flags or environment, configures its
// key provider. Every CLI command that touches the session store should use
// this instead of calling session.NewStore() directly, so encryption
// configuration is applied consistently everywhere.
func openSessionStore() (*session.Store, error) {
	store, err := session.NewStore()
	if err != nil {
		return nil, err
	}
	if kp, kpErr := resolveSessionKeyProviderFromFlags(); kpErr != nil {
		store.Close()
		return nil, kpErr
	} else if kp != nil {
		store.SetKeyProvider(kp)
	}
	return store, nil
}

// resolveSessionKeyProviderFromFlags builds a session.KeyProvider from CLI
// flags and their environment-variable fallbacks. It returns (nil, nil) when
// encryption was not requested, so callers can tell "not configured" apart
// from "configured but invalid."
func resolveSessionKeyProviderFromFlags() (session.KeyProvider, error) {
	encrypt := sessionEncryptFlag || os.Getenv("GLASSBOX_SESSION_ENCRYPTION") != ""
	providerName := sessionKeyProviderFlag
	if providerName == "" {
		providerName = os.Getenv("GLASSBOX_SESSION_KEY_PROVIDER")
	}
	passphrase := sessionKeyPassphraseFlag
	if passphrase == "" {
		passphrase = os.Getenv("GLASSBOX_SESSION_KEY_PASSPHRASE")
	}

	if !encrypt && providerName == "" && passphrase == "" {
		return nil, nil
	}
	return session.ResolveKeyProvider(providerName, passphrase)
}

// currentData holds the active session context from debug command
var currentData *session.Data

// SetCurrentSession stores the active session for later saving
func SetCurrentSession(data *session.Data) {
	currentData = data
}

// GetCurrentSession returns the active session if any
func GetCurrentSession() *session.Data {
	return currentData
}

var sessionCmd = &cobra.Command{
	Use:     "session",
	GroupID: "management",
	Short:   "Manage debugging sessions",
	Long: `Save, resume, and manage debugging sessions to preserve state across CLI invocations.

Sessions store complete transaction data, simulation results, and analysis context,
allowing you to:
  • Resume debugging work later
  • Share debugging context with team members
  • Build a history of investigated transactions
  • Analyze patterns across multiple sessions

Available subcommands:
  save        - Save current session to disk
  resume      - Restore a saved session
  list        - View all saved sessions
  delete      - Remove a saved session
  recover     - Restore a session interrupted by an unexpected process exit
  doctor      - Check saved sessions for schema and integrity problems
  lock:status - Print the advisory lock status for a session`,
	Example: `  # Save current debug session
  glassbox session save

  # List all sessions
  glassbox session list

  # Resume a specific session
  glassbox session resume <session-id>

  # Save and load a named bookmark
  glassbox session save --name payroll-bug
  glassbox session load payroll-bug

  # Recover a session after an unexpected exit
  glassbox session recover

  # Delete a session
  glassbox session delete <session-id>`,
}

var sessionSaveCmd = &cobra.Command{
	Use:   "save",
	Short: "Save the current debugging session",
	Long: `Save the current debug session state to disk for later resumption.

	You must run 'Glassbox debug <tx-hash>' first to create an active session.
	The session ID can be auto-generated or specified with --id flag.

	Before persisting, Glassbox validates required session fields and any audit-chain
	metadata (audit_hash, audit_signature, previous_session_hash) so malformed or
	incomplete chain state is rejected with actionable diagnostics instead of being
	written to the store.

Concurrency safety:
  Glassbox protects session saves with two mechanisms [Issue #813]:

  1. Advisory lock – a per-session lock file in ~/.Glassbox/locks/ ensures that
     only one writer is active at a time.  A lock held by a crashed process is
     removed automatically after 5 minutes.
  2. Revision check – every session carries a monotonically increasing revision
     counter.  If another process saved the session between your last load and
     now, this command fails with a SESSION_WRITE_CONFLICT error and explains
     how to recover.

  To overwrite a conflicting save without merging:
    glassbox session save --force

Validation:
  The session data is validated before saving. The following checks are made:
    • Transaction hash is present
    • Network is one of: testnet, mainnet, futurenet
    • Status is a recognized value (auto-set to 'active' if empty)
    • Session name, if provided, must not exceed 128 characters
    • Horizon URL is auto-populated from the network if not provided

  If any check fails an actionable error is printed with a remediation hint.`,
	Example: `  # Save with auto-generated ID
  glassbox session save

  # Save with custom ID
  glassbox session save --id my-debug-session

  # Save a named bookmark
  glassbox session save --name payroll-bug

  # Save and pin a custom RPC endpoint
  glassbox session save --pin-endpoint https://soroban-testnet.stellar.org

  # Force-overwrite a conflicting save
  glassbox session save --force`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		// --plan: show what session save will do without any side effects.
		if sessionSavePlanFlag {
			dbPath := session.DefaultDBPath()
			sessionID := sessionIDFlag
			execPlan := plan.BuildSessionSavePlan(plan.SessionPlanOptions{
				SessionID: sessionID,
				Name:      sessionNameFlag,
				DBPath:    dbPath,
			})
			fmt.Fprint(cmd.OutOrStdout(), execPlan.RenderText())
			return nil
		}

		// Check if we have an active session
		data := GetCurrentSession()
		if data == nil {
			return errors.WrapSimulationLogicError("no active session to save. Run 'Glassbox debug <tx-hash>' first")
		}

		// Generate or use provided ID
		if sessionIDFlag != "" {
			data.ID = sessionIDFlag
		} else if data.ID == "" {
			data.ID = session.GenerateID(data.TxHash)
		}
		if sessionNameFlag != "" {
			name := strings.TrimSpace(sessionNameFlag)
			if len(name) > 128 {
				return fmt.Errorf(
					"session name is too long (%d characters, max 128)\n"+
						"  Fix: provide a shorter name with --name",
					len(name),
				)
			}
			data.Name = name
		}

		if sessionPinEndpointFlag != "" {
			data.PinnedEndpoint = sessionPinEndpointFlag
		}

		data.Status = "saved"
		data.LastAccessAt = time.Now()

		if data.EnvFingerprint == "" {
			data.EnvFingerprint = session.BuildEnvFingerprint()
		}

		// Open session store
		store, err := openSessionStore()
		if err != nil {
			return errors.WrapValidationError(fmt.Sprintf("failed to open session store: %v", err))
		}
		defer store.Close()

		// Run cleanup before save
		err = store.Cleanup(ctx, session.DefaultTTL, session.DefaultMaxSessions)
		if err != nil {
			// Log but don't fail on cleanup errors
			fmt.Fprintf(os.Stderr, "Warning: cleanup failed: %v\n", err)
		}

		// Record this save in the session's provenance timeline before
		// persisting, so the timeline itself is captured in the same write.
		_ = session.RecordProvenance(data, session.ProvenanceSaved, session.ActorUser,
			version.Version, data.EnvFingerprint, "", true)

		// Save with validation so corrupt or incomplete sessions are rejected
		// early with a clear diagnostic instead of a silent partial write.
		// When --force is set, bypass the optimistic revision check.
		var saveErr error
		if sessionSaveForceFlag {
			saveErr = store.SaveForce(ctx, data)
		} else {
			saveErr = store.SaveWithValidation(ctx, data)
		}
		if saveErr != nil {
			// Surface session-conflict errors with structured guidance so
			// users know exactly what to do next (Issue #813).
			if stderrors.Is(saveErr, session.ErrSessionConflict) {
				return errors.WrapSessionConflict(data.ID, data.Revision-1, data.Revision).(*errors.ErstError).
					WithHint(fmt.Sprintf(
						"Run 'glassbox session resume %s' to reload the latest version "+
							"and re-apply your changes, or re-run with --force to overwrite it.",
						data.ID,
					))
			}
			if stderrors.Is(saveErr, session.ErrLockHeld) {
				return errors.WrapSessionLockHeld(data.ID, 0).(*errors.ErstError).
					WithHint("Another Glassbox instance is saving this session. " +
						"Wait a moment and retry. If the other process has crashed, " +
						"the lock clears automatically after 5 minutes.")
			}
			return errors.WrapValidationError(fmt.Sprintf("failed to save session: %v", saveErr))
		}

		fmt.Printf("Session saved: %s\n", data.ID)
		if data.Name != "" {
			fmt.Printf("  Name: %s\n", data.Name)
		}
		fmt.Printf("  Transaction: %s\n", data.TxHash)
		fmt.Printf("  Network: %s\n", data.Network)
		fmt.Printf("  Created: %s\n", data.CreatedAt.Format(time.RFC3339))
		fmt.Printf("  Revision: %d\n", data.Revision)

		return nil
	},
}

var sessionResumeCmd = &cobra.Command{
	Use:     "resume <session-id-or-name>",
	Aliases: []string{"load"},
	Short:   "Restore a saved debugging session",
	Long: `Resume a previously saved debug session by ID. This restores all transaction data,
simulation results, and analysis context from the saved session.

The command runs an integrity check on the loaded session before making it
active. Any data-consistency problems are reported with actionable hints so you
know exactly what to fix before continuing.

Use 'Glassbox session list' to see available session IDs and names.`,
	Example: `  # Resume a session
  glassbox session resume abc123

  # Load by bookmark name
  glassbox session load payroll-bug

  # List available sessions first, then resume
  glassbox session list
  glassbox session resume <session-id-or-name>

  # Run diagnostics if a session behaves unexpectedly
  glassbox session doctor`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		sessionID := args[0]

		// Validate the session ID argument is non-empty.
		if strings.TrimSpace(sessionID) == "" {
			return errors.WrapValidationError(
				"session ID is required\n" +
					"Usage: glassbox session resume <session-id-or-name>\n" +
					"Run 'glassbox session list' to see available sessions",
			)
		}

		// Open session store
		store, err := openSessionStore()
		if err != nil {
			return errors.WrapValidationError(fmt.Sprintf(
				"failed to open session store: %v\n"+
					"Hint: ensure ~/.Glassbox/ is writable and not corrupted", err))
		}
		defer store.Close()

		// Run cleanup (best-effort — don't fail resume on cleanup errors).
		if cleanErr := store.Cleanup(ctx, session.DefaultTTL, session.DefaultMaxSessions); cleanErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: session cleanup failed: %v\n", cleanErr)
		}

		// Resolve session by exact ID, partial ID prefix, tx hash, or fuzzy match.
		// Load validates schema compatibility and auto-upgrades older sessions
		// unless --read-only is set [Issue #1115].
		var data *session.Data
		if sessionResumeReadOnly {
			loaded, warn, loadErr := store.LoadReadOnly(ctx, sessionID)
			if loadErr != nil {
				// Try bookmark name → id without migrating.
				if id, nameErr := store.LookupIDByName(ctx, sessionID); nameErr == nil {
					loaded, warn, loadErr = store.LoadReadOnly(ctx, id)
				}
			}
			if loadErr != nil {
				return fmt.Errorf(
					"session %q not found: %w\n"+
						"Hint: run 'glassbox session list' to see all available sessions",
					sessionID, loadErr,
				)
			}
			if warn != "" {
				fmt.Fprintln(os.Stderr, warn)
			}
			data = loaded
		} else {
			var resolveErr error
			data, resolveErr = resolveSessionInput(ctx, store, sessionID)
			if resolveErr != nil {
				if session.IsSchemaError(resolveErr) {
					return resolveErr
				}
				return fmt.Errorf(
					"session %q not found: %w\n"+
						"Hint: run 'glassbox session list' to see all available sessions",
					sessionID, resolveErr,
				)
			}
		}

		// ── Integrity check ───────────────────────────────────────────────────
		// Run before making the session active so corrupt sessions never become
		// the current session and cause confusing downstream failures.
		report := session.ValidateIntegrity(data)
		if !report.OK {
			fmt.Fprintf(os.Stderr, "\nSession integrity check FAILED for %s:\n", data.ID)
			for i, issue := range report.Issues {
				fmt.Fprintf(os.Stderr, "  %d. [%s] %s\n", i+1, issue.Field, issue.Description)
				if issue.Hint != "" {
					fmt.Fprintf(os.Stderr, "     Hint: %s\n", issue.Hint)
				}
			}
			fmt.Fprintf(os.Stderr, "\nThis session cannot be resumed safely.\n")
			fmt.Fprintf(os.Stderr, "To remove it:  glassbox session delete %s\n", data.ID)
			fmt.Fprintf(os.Stderr, "To re-debug:   glassbox debug %s --network %s\n",
				data.TxHash, data.Network)
			return fmt.Errorf("session %s failed integrity validation (%d issue(s))", data.ID, len(report.Issues))
		}

		// Update status and make it current.
		data.Status = "resumed"
		SetCurrentSession(data)

		// Display session info
		fmt.Printf("Session resumed: %s\n", data.ID)
		if data.Name != "" {
			fmt.Printf("  Name: %s\n", data.Name)
		}
		fmt.Printf("  Transaction: %s\n", data.TxHash)
		fmt.Printf("  Network: %s\n", data.Network)
		if data.PinnedEndpoint != "" {
			fmt.Printf("  Pinned endpoint: %s\n", data.PinnedEndpoint)
		}
		fmt.Printf("  Created: %s\n", data.CreatedAt.Format(time.RFC3339))
		fmt.Printf("  Last accessed: %s\n", data.LastAccessAt.Format(time.RFC3339))

		// Show transaction envelope info
		if data.EnvelopeXdr != "" {
			fmt.Printf("\nTransaction Envelope:\n")
			fmt.Printf("  Size: %d bytes\n", len(data.EnvelopeXdr))
		}

		// Show simulation results if available
		if data.SimResponseJSON != "" {
			resp, simErr := data.ToSimulationResponse()
			if simErr == nil {
				fmt.Printf("\nSimulation Results:\n")
				fmt.Printf("  Status: %s\n", resp.Status)
				if resp.Error != "" {
					fmt.Printf("  Error: %s\n", resp.Error)
				}
				if len(resp.Events) > 0 {
					fmt.Printf("  Events: %d\n", len(resp.Events))
				}
				if len(resp.Logs) > 0 {
					fmt.Printf("  Logs: %d\n", len(resp.Logs))
				}
			} else {
				fmt.Fprintf(os.Stderr, "Warning: simulation response data is unreadable: %v\n", simErr)
				fmt.Fprintf(os.Stderr, "Hint: re-run 'glassbox debug %s --network %s' to regenerate it.\n",
					data.TxHash, data.Network)
			}
		}

		// Show persisted viewer state if any (best-effort).
		if uiStore, uiErr := session.NewUIStateStore(); uiErr == nil {
			defer uiStore.Close()
			if sections, secErr := uiStore.LoadSectionState(ctx, data.TxHash); secErr == nil && len(sections) > 0 {
				fmt.Printf("\nViewer state: [%s]\n", strings.Join(sections, ", "))
			}
			if queries, qErr := uiStore.RecentSearches(ctx, 5); qErr == nil && len(queries) > 0 {
				fmt.Printf("Recent searches: %s\n", strings.Join(queries, ", "))
			}
		}

		return nil
	},
}

var sessionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all saved debugging sessions",
	Long: `List all saved debug sessions, ordered by most recently accessed.

Displays session ID, bookmark name (if set), network, last access time, and
transaction hash for each session. Expired sessions are pruned automatically.

Sessions can be resumed with:
  glassbox session resume <session-id>

To filter or inspect a session before resuming:
  glassbox session resume <session-id> --dry-run  (not yet available)

See also:
  glassbox session save    – save the current session
  glassbox session resume  – restore a saved session
  glassbox session delete  – remove a session`,
	Example: `  # List all sessions
  glassbox session list

  # Then resume or delete by the ID shown
  glassbox session resume <session-id>
  glassbox session delete <session-id>`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		// Open session store
		store, err := openSessionStore()
		if err != nil {
			return errors.WrapValidationError(fmt.Sprintf("failed to open session store: %v", err))
		}
		defer store.Close()

		// Run cleanup
		err = store.Cleanup(ctx, session.DefaultTTL, session.DefaultMaxSessions)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: session cleanup failed: %v\n", err)
		}

		// List sessions
		sessions, err := store.List(ctx, 50)
		if err != nil {
			return errors.WrapValidationError(fmt.Sprintf("failed to list sessions: %v", err))
		}

		if len(sessions) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No saved sessions found.")
			fmt.Fprintln(cmd.OutOrStdout(), "Tip: run 'glassbox debug <tx-hash> --network testnet' to start a new session,")
			fmt.Fprintln(cmd.OutOrStdout(), "     then 'glassbox session save' to persist it.")
			return nil
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Saved sessions (%d):\n\n", len(sessions))
		fmt.Fprintf(cmd.OutOrStdout(), "%-20s %-20s %-12s %-20s %-66s\n", "ID", "Name", "Network", "Last Accessed", "Transaction Hash")
		fmt.Fprintln(cmd.OutOrStdout(), "--------------------------------------------------------------------------------")

		for _, s := range sessions {
			lastAccess := s.LastAccessAt.Format("2006-01-02 15:04")
			txHash := s.TxHash
			if len(txHash) > 64 {
				txHash = txHash[:64] + "..."
			}
			name := s.Name
			if name == "" {
				name = "-"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%-20s %-20s %-12s %-20s %-66s\n", s.ID, name, s.Network, lastAccess, txHash)
		}

		return nil
	},
}

var sessionDeleteCmd = &cobra.Command{
	Use:   "delete <session-id>",
	Short: "Remove a saved debugging session",
	Long: `Delete a saved debug session by ID or name. This action cannot be undone.

Use 'glassbox session list' to see all available session IDs and names.

Use 'Glassbox session list' to see available sessions.`,
	Example: `  # Delete a specific session
  glassbox session delete abc123

  # List sessions first to find the ID
  glassbox session list
  glassbox session delete <session-id>`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		sessionID := strings.TrimSpace(args[0])

		if sessionID == "" {
			return errors.WrapValidationError(
				"session ID is required\n" +
					"Usage: glassbox session delete <session-id-or-name>\n" +
					"Run 'glassbox session list' to see available sessions",
			)
		}

		// Open session store
		store, err := openSessionStore()
		if err != nil {
			return errors.WrapValidationError(fmt.Sprintf("failed to open session store: %v", err))
		}
		defer store.Close()

		// Resolve to a valid session ID before deleting
		resolved, resolveErr := resolveSessionInput(ctx, store, sessionID)
		if resolveErr != nil {
			return fmt.Errorf(
				"session %q not found: %w\n"+
					"Hint: run 'glassbox session list' to see all available sessions",
				sessionID, resolveErr,
			)
		}

		if err := store.Delete(ctx, resolved.ID); err != nil {
			return errors.WrapValidationError(fmt.Sprintf("failed to delete session %q: %v\nHint: run 'glassbox session list' to verify the session exists", resolved.ID, err))
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Session deleted: %s\n", resolved.ID)
		return nil
	},
}

var sessionRecoverCmd = &cobra.Command{
	Use:   "recover",
	Short: "Restore a session interrupted by an unexpected process termination",
	Long: `Check for an orphaned session checkpoint left by a previous Glassbox process
that terminated unexpectedly (crash, SIGKILL, power loss).

If a recoverable checkpoint is found, the session is validated for integrity and
then restored. Any issues found during validation are reported with actionable
hints. The checkpoint is removed after a successful recovery.

If the checkpoint references a session that was never flushed to the store (the
process crashed before saving), the stale checkpoint is cleared and guidance is
printed so you know how to re-run the debug command.

This command also checks for stale advisory lock files left by the crashed
process and removes them before attempting to reload the session, so subsequent
save operations are not blocked by a phantom lock.

Validation:
  The checkpoint file is validated for completeness before it is trusted.
  Missing session ID, transaction hash, network, or invalid PID values are
  detected and reported with actionable diagnostics. If the checkpoint is
  corrupt, it is cleared and guidance is printed for starting a fresh session.`,
	Example: `  # Check for and restore an orphaned session after a crash
  glassbox session recover

  # If recovery finds nothing, start a fresh debug session
  glassbox debug <tx-hash> --network testnet`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		cp, err := session.LoadCheckpoint()
		if err != nil {
			// Surface a clear error when the checkpoint file exists but is malformed.
			return fmt.Errorf(
				"failed to read crash-recovery checkpoint: %w\n"+
					"Hint: the checkpoint file (~/.Glassbox/active_session.json) may be corrupt.\n"+
					"      Delete it manually to clear the error, then run 'glassbox debug' again.",
				err,
			)
		}
		if cp == nil {
			fmt.Println("No crash-recovery checkpoint found. Nothing to recover.")
			return nil
		}

		// Validate checkpoint fields before trusting them.
		cpReport := session.ValidateCheckpoint(cp)
		if !cpReport.OK {
			fmt.Fprintf(os.Stderr, "Checkpoint validation failed (%d issue(s)):\n", len(cpReport.Issues))
			for i, issue := range cpReport.Issues {
				fmt.Fprintf(os.Stderr, "  %d. [%s] %s\n", i+1, issue.Field, issue.Description)
				if issue.Hint != "" {
					fmt.Fprintf(os.Stderr, "     Hint: %s\n", issue.Hint)
				}
			}
			fmt.Fprintf(os.Stderr, "\nClearing corrupt checkpoint.\n")
			fmt.Fprintf(os.Stderr, "Hint: re-run 'glassbox debug <tx-hash>' to start a fresh session.\n")
			if clearErr := session.ClearCheckpoint(); clearErr != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to clear checkpoint: %v\n", clearErr)
			}
			return fmt.Errorf("checkpoint is corrupt and cannot be recovered (%d issue(s))", len(cpReport.Issues))
		}

		// Liveness probe: the process must be gone before we can take over the session.
		if !cp.IsOrphaned() {
			fmt.Fprintf(os.Stderr,
				"Checkpoint found but the owning process (PID %d) is still running.\n"+
					"Hint: use 'glassbox session list' to view active sessions,\n"+
					"      or wait for that process to exit before recovering.\n",
				cp.PID)
			return fmt.Errorf(
				"process PID %d is still alive — session %s cannot be recovered yet",
				cp.PID, cp.SessionID,
			)
		}

		fmt.Printf("Orphaned session checkpoint detected (PID %d no longer running).\n", cp.PID)
		fmt.Printf("  Session : %s\n", cp.SessionID)
		fmt.Printf("  Tx Hash : %s\n", cp.TxHash)
		fmt.Printf("  Network : %s\n", cp.Network)
		fmt.Printf("  Started : %s\n", cp.StartedAt.Format(time.RFC3339))
		fmt.Println()

		// Recover stale advisory lock that the crashed process may have left
		// behind.  This must happen before we try to open the store and save,
		// otherwise the store's AcquireLock call will be blocked by the phantom
		// lock from the dead process.
		if lockRecovered, lockErr := session.RecoverStaleLock(cp.SessionID); lockErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: stale lock recovery probe failed for session %s: %v\n",
				cp.SessionID, lockErr)
		} else if lockRecovered {
			fmt.Printf("Removed stale advisory lock left by PID %d.\n", cp.PID)
		}

		// Attempt to load the session from the store.
		store, storeErr := openSessionStore()
		if storeErr != nil {
			return errors.WrapValidationError(fmt.Sprintf(
				"failed to open session store: %v\n"+
					"Hint: ensure ~/.Glassbox/ is writable and not corrupted", storeErr))
		}
		defer store.Close()

		data, loadErr := store.Load(ctx, cp.SessionID)
		if loadErr != nil {
			// Checkpoint exists but the session was never flushed to the store
			// (process crashed before the first save).
			fmt.Fprintf(os.Stderr,
				"Warning: session %s not found in the store (%v).\n"+
					"This means the process crashed before saving any session data.\n",
				cp.SessionID, loadErr)
			fmt.Fprintf(os.Stderr, "Clearing stale checkpoint.\n")
			fmt.Fprintf(os.Stderr,
				"Hint: to re-debug, run: glassbox debug %s --network %s\n",
				cp.TxHash, cp.Network)
			if clearErr := session.ClearCheckpoint(); clearErr != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to clear checkpoint: %v\n", clearErr)
			}
			return nil
		}

		// Integrity check on the loaded session data before making it active.
		report := session.ValidateIntegrity(data)
		if !report.OK {
			fmt.Fprintf(os.Stderr, "\nSession integrity check FAILED for %s:\n", data.ID)
			for i, issue := range report.Issues {
				fmt.Fprintf(os.Stderr, "  %d. [%s] %s\n", i+1, issue.Field, issue.Description)
				if issue.Hint != "" {
					fmt.Fprintf(os.Stderr, "     Hint: %s\n", issue.Hint)
				}
			}
			fmt.Fprintf(os.Stderr, "\nThe session exists in the store but has data integrity problems.\n")
			fmt.Fprintf(os.Stderr, "To remove it:  glassbox session delete %s\n", data.ID)
			fmt.Fprintf(os.Stderr, "To re-debug:   glassbox debug %s --network %s\n",
				cp.TxHash, cp.Network)
			// Clear the orphaned checkpoint even for a corrupt session so
			// subsequent invocations don't keep hitting the same error.
			_ = session.ClearCheckpoint()
			return fmt.Errorf(
				"recovered session %s failed integrity validation (%d issue(s)) — checkpoint cleared",
				data.ID, len(report.Issues),
			)
		}

		data.Status = "recovered"
		data.LastAccessAt = time.Now()
		_ = session.RecordProvenance(data, session.ProvenanceRecovered, session.ActorSystem,
			version.Version, data.EnvFingerprint, "recovered from crash checkpoint", true)
		if saveErr := store.SaveWithValidation(ctx, data); saveErr != nil {
			return errors.WrapValidationError(fmt.Sprintf(
				"failed to update recovered session: %v", saveErr))
		}

		SetCurrentSession(data)

		// Remove the checkpoint now that the session is safely restored.
		if clearErr := session.ClearCheckpoint(); clearErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to clear checkpoint: %v\n", clearErr)
		}

		fmt.Printf("Session recovered: %s\n", data.ID)
		fmt.Println("Use 'glassbox session resume <id>' to re-enter the session,")
		fmt.Println("or 'glassbox session list' to view all sessions.")
		return nil
	},
}

var sessionLockStatusCmd = &cobra.Command{
	Use:   "lock:status <session-id>",
	Short: "Print the advisory lock status for a session",
	Long: `Print the advisory lock metadata for a session without attempting to acquire
the lock. This is a read-only diagnostic command.

If the session has an active advisory lock the command prints the lock holder's
PID, hostname, Glassbox version, and the time the lock was acquired.

If no lock file exists for the session the command prints "not locked".

Use this command to diagnose SESSION_LOCK_HELD errors before deciding whether
to wait for the holder to finish or to run 'glassbox session recover' to remove
a stale lock from a crashed process.`,
	Example: `  # Check whether a session is currently locked
  glassbox session lock:status <session-id>

  # If locked by a dead process, recover it
  glassbox session recover`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		sessionID := strings.TrimSpace(args[0])
		if sessionID == "" {
			return errors.WrapValidationError(
				"session ID is required\n" +
					"Usage: glassbox session lock:status <session-id>",
			)
		}

		meta, err := session.ReadLockMetadata(sessionID)
		if err != nil {
			return errors.WrapValidationError(fmt.Sprintf(
				"failed to read lock metadata for session %q: %v", sessionID, err))
		}

		out := cmd.OutOrStdout()
		if meta == nil {
			fmt.Fprintf(out, "Session %q: not locked\n", sessionID)
			return nil
		}

		fmt.Fprintf(out, "Session %q: locked\n", sessionID)
		fmt.Fprintf(out, "  PID              : %d\n", meta.PID)
		fmt.Fprintf(out, "  Hostname         : %s\n", meta.Hostname)
		fmt.Fprintf(out, "  Acquired at      : %s\n", meta.AcquiredAt.Format(time.RFC3339))
		fmt.Fprintf(out, "  Glassbox version : %s\n", meta.GlassboxVersion)

		// Surface whether the holder is still alive as a convenience hint.
		if meta.PID > 0 {
			alive, aliveErr := session.IsLockHolderAlive(sessionID)
			if aliveErr == nil {
				if alive {
					fmt.Fprintf(out, "  Status           : LIVE (process %d is running)\n", meta.PID)
				} else {
					fmt.Fprintf(out, "  Status           : STALE (process %d is no longer running)\n", meta.PID)
					fmt.Fprintf(out, "\nHint: run 'glassbox session recover' to remove the stale lock.\n")
				}
			}
		}

		return nil
	},
}

var sessionDoctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check saved sessions for schema and integrity problems",
	Long: `Run diagnostics on all persisted debug sessions in ~/.Glassbox/sessions.db.

Reports schema version mismatches, missing fields, and other integrity issues
with actionable remediation hints for each degraded session.`,
	Example: `  # Check all saved sessions for schema and integrity problems
  glassbox session doctor

  # Re-run the debug command to recreate any degraded sessions
  glassbox debug <tx-hash> --network testnet`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		store, err := openSessionStore()
		if err != nil {
			return errors.WrapValidationError(fmt.Sprintf(
				"failed to open session store: %v\n"+
					"Hint: ensure ~/.Glassbox/ is writable and not corrupted", err))
		}
		defer store.Close()

		result, err := store.RunStoreDiagnostics(ctx)
		if err != nil {
			return err
		}

		fmt.Println(result.Summary())

		if home, homeErr := os.UserHomeDir(); homeErr == nil {
			glassboxDir := filepath.Join(home, ".Glassbox")
			removed, cleanErr := session.CleanStaleTempFiles(glassboxDir, session.StaleTempFileAge)
			if cleanErr == nil && removed > 0 {
				fmt.Printf("Removed %d stale temp/recovery-journal file(s) from %s\n", removed, glassboxDir)
			}
			viewerDir := filepath.Join(glassboxDir, "viewer_state")
			if removedVS, vsErr := session.CleanStaleTempFiles(viewerDir, session.StaleTempFileAge); vsErr == nil && removedVS > 0 {
				fmt.Printf("Removed %d stale temp file(s) from %s\n", removedVS, viewerDir)
			}
			// Clean stale advisory lock files left by crashed processes [Issue #813].
			if removedLocks, lockErr := session.CleanStaleLocks(); lockErr == nil && removedLocks > 0 {
				fmt.Printf("Removed %d stale advisory lock file(s) from %s/locks/\n", removedLocks, glassboxDir)
			}
		}

		if result.DegradedSessions == 0 {
			return nil
		}

		fmt.Printf("\nDegraded sessions (%d):\n\n", result.DegradedSessions)
		for _, report := range result.Reports {
			fmt.Printf("Session %s:\n", report.SessionID)
			for i, issue := range report.Issues {
				fmt.Printf("  %d. [%s] %s\n", i+1, issue.Field, issue.Description)
				if issue.Hint != "" {
					fmt.Printf("     Hint: %s\n", issue.Hint)
				}
			}
			if report.StoredSchemaVersion > 0 && report.StoredSchemaVersion != session.SchemaVersion {
				fmt.Printf("  Schema: %s\n", session.SchemaVersionSummary(report.StoredSchemaVersion))
			}
			fmt.Println()
		}

		return fmt.Errorf("%d session(s) failed diagnostics", result.DegradedSessions)
	},
}

var sessionMigrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Inspect or apply session schema migrations",
	Long: `Show the pending schema migration plan for the session database, or
apply migrations. Use --dry-run to print the plan without modifying any
session files [Issue #1115].`,
	Example: `  glassbox session migrate --dry-run
  glassbox session migrate --dry-run --id <session-id>`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		dbPath := session.DefaultDBPath()
		if sessionMigrateDryRun {
			steps, err := session.DryRunMigrationPlanForSession(dbPath, sessionMigrateIDFlag)
			if err != nil {
				return err
			}
			if len(steps) == 0 {
				fmt.Println("No pending migrations — schema is current.")
				return nil
			}
			fmt.Println("Pending migration plan (dry-run, no changes made):")
			for i, step := range steps {
				fmt.Printf("  %d. v%d → v%d: %s\n", i+1, step.FromVersion, step.ToVersion, step.Description)
			}
			return nil
		}
		return fmt.Errorf("apply mode not requested; pass --dry-run to print the migration plan")
	},
}

func init() {
	sessionSaveCmd.Flags().StringVar(&sessionIDFlag, "id", "", "Custom session ID (default: auto-generated)")
	sessionSaveCmd.Flags().StringVar(&sessionNameFlag, "name", "", "Bookmark name for this session snapshot")
	sessionSaveCmd.Flags().StringVar(&sessionPinEndpointFlag, "pin-endpoint", "", "Pin an RPC endpoint URL with this session")
	sessionSaveCmd.Flags().BoolVar(&sessionSavePlanFlag, "plan", false, "Print the execution plan (DB path, session ID) without saving")
	sessionSaveCmd.Flags().BoolVar(&sessionSaveForceFlag, "force", false, "Overwrite the session even if another process has saved a newer revision (bypasses conflict check)")

	sessionResumeCmd.Flags().BoolVar(&sessionResumeReadOnly, "read-only", false,
		"Open the session without running schema migrations (warns if schema is stale)")

	sessionMigrateCmd.Flags().BoolVar(&sessionMigrateDryRun, "dry-run", false,
		"Print the pending migration plan without modifying the session database")
	sessionMigrateCmd.Flags().StringVar(&sessionMigrateIDFlag, "id", "",
		"Limit the dry-run plan to a single session ID")

	sessionCmd.PersistentFlags().BoolVar(&sessionEncryptFlag, "session-encrypt", false,
		"Encrypt sensitive session fields at rest (or set GLASSBOX_SESSION_ENCRYPTION)")
	sessionCmd.PersistentFlags().StringVar(&sessionKeyProviderFlag, "session-key-provider", "",
		"Session encryption key provider: passphrase (default) or env (or set GLASSBOX_SESSION_KEY_PROVIDER)")
	sessionCmd.PersistentFlags().StringVar(&sessionKeyPassphraseFlag, "session-key-passphrase", "",
		"Passphrase for session encryption (or set GLASSBOX_SESSION_KEY_PASSPHRASE)")
	_ = sessionCmd.PersistentFlags().MarkHidden("session-key-passphrase") // sensitive; hidden from default help

	sessionCmd.AddCommand(sessionSaveCmd)
	sessionCmd.AddCommand(sessionResumeCmd)
	sessionCmd.AddCommand(sessionListCmd)
	sessionCmd.AddCommand(sessionDeleteCmd)
	sessionCmd.AddCommand(sessionRecoverCmd)
	sessionCmd.AddCommand(sessionDoctorCmd)
	sessionCmd.AddCommand(sessionMigrateCmd)
	sessionCmd.AddCommand(sessionLockStatusCmd)

	rootCmd.AddCommand(sessionCmd)
}
