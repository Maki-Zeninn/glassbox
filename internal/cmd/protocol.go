// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/dotandev/glassbox/internal/clioutput"
	"github.com/dotandev/glassbox/internal/deeplink"
	"github.com/dotandev/glassbox/internal/plan"
	"github.com/dotandev/glassbox/internal/protocolreg"
	"github.com/spf13/cobra"
)

// protocolDiagnoseJSON controls whether protocol:diagnose emits JSON output.
var protocolDiagnoseJSON bool

// protocolDiagnoseFormat controls the output format for protocol:diagnose ("text" or "json").
var protocolDiagnoseFormat string

// protocolVerifyProbe runs an optional glassbox://doctor-probe handler check.
var protocolVerifyProbe bool

// protocolRegisterDryRun previews what register would do without modifying OS state.
var protocolRegisterDryRun bool

var protocolRegisterCmd = &cobra.Command{
	Use:     "protocol:register",
	Aliases: []string{"pb:register"},
	Short:   "Register the glassbox:// protocol handler in the operating system",
	Long: `Register the glassbox:// URI scheme so the OS dispatches deep links to Glassbox.

On Linux, a .desktop file and a wrapper script are written under ~/.local/share/.
On macOS, an app bundle is created in ~/Applications/ and registered with LaunchServices.
On Windows, registry keys are written under HKEY_CURRENT_USER\Software\Classes\Glassbox.

Use --dry-run to preview what would be written without modifying any system state.
Run 'glassbox protocol:verify' after registration to confirm the handler is working.
If registration fails, run 'glassbox protocol:diagnose' for a root-cause breakdown.`,
	Example: `  # Register the protocol handler on the current platform
  glassbox protocol:register

  # Preview the registration without writing any OS state
  glassbox protocol:register --dry-run

  # Confirm the registration worked after registering
  glassbox protocol:register && glassbox protocol:verify`,
	GroupID: "utility",
	RunE: func(cmd *cobra.Command, args []string) error {
		registrar, err := protocolreg.NewRegistrar()
		if err != nil {
			return fmt.Errorf("initialise registrar: %w\n  Fix: ensure the glassbox binary is installed and accessible", err)
		}

		if protocolRegisterDryRun {
			diag := registrar.Diagnose()
			execPath, execErr := os.Executable()
			if execErr != nil {
				execPath = "(unknown)"
			}

			execPlan := plan.BuildProtocolRegisterPlan(plan.ProtocolRegisterPlanOptions{
				Platform:          diag.Platform,
				ExecutablePath:    execPath,
				AlreadyRegistered: diag.Status == protocolreg.StatusOK,
				RegisteredHandler: diag.RegisteredHandler,
			})

			fmt.Fprint(cmd.OutOrStdout(), execPlan.RenderText())
			return nil
		}

		if err := registrar.Register(); err != nil {
			return fmt.Errorf("%w\n  Tip: run 'glassbox protocol:diagnose' for a detailed breakdown, or 'glassbox protocol:repair' to attempt automatic repair", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Registered GLASSBOX Protocol handler for %s://\n", protocolreg.Scheme)
		fmt.Fprintln(cmd.OutOrStdout(), "Tip: run 'glassbox protocol:verify' to confirm the registration is working.")
		return nil
	},
}

var protocolUnregisterCmd = &cobra.Command{
	Use:     "protocol:unregister",
	Aliases: []string{"pb:unregister"},
	Short:   "Unregister the glassbox:// protocol handler from the operating system",
	Long: `Remove the glassbox:// protocol handler registration from the operating system.

On Linux, the .desktop file and wrapper script are deleted from ~/.local/share/.
On macOS, the app bundle is removed from ~/Applications/ and unregistered from LaunchServices.
On Windows, the registry key HKEY_CURRENT_USER\Software\Classes\Glassbox is deleted.

After unregistering, glassbox:// deep links will no longer open Glassbox automatically.
Use 'glassbox protocol:register' to re-register at any time.`,
	Example: `  # Remove the protocol handler registration
  glassbox protocol:unregister

  # Verify it was removed
  glassbox protocol:unregister && glassbox protocol:status`,
	GroupID: "utility",
	RunE: func(cmd *cobra.Command, args []string) error {
		registrar, err := protocolreg.NewRegistrar()
		if err != nil {
			return err
		}
		if err := registrar.Unregister(); err != nil {
			return err
		}

		fmt.Fprintln(cmd.OutOrStdout(), "Unregistered GLASSBOX Protocol handler")
		return nil
	},
}

var protocolStatusCmd = &cobra.Command{
	Use:     "protocol:status",
	Aliases: []string{"pb:status"},
	Short:   "Check current registration status of the glassbox:// protocol handler",
	Long: `Check whether the glassbox:// protocol handler is currently registered on this system.

The command runs a quick diagnostic pass and prints each passing check as [OK].
If the handler is not registered or is broken, issues and remediation steps are
printed to stderr and the command exits with a non-zero exit code.

For a more detailed root-cause analysis, use 'glassbox protocol:diagnose'.
To fix a broken registration automatically, use 'glassbox protocol:repair'.`,
	Example: `  # Check whether the protocol handler is registered
  glassbox protocol:status

  # Use in a script to gate further steps on registration
  glassbox protocol:status || glassbox protocol:register`,
	GroupID: "utility",
	RunE: func(cmd *cobra.Command, args []string) error {
		registrar, err := protocolreg.NewRegistrar()
		if err != nil {
			return err
		}

		diag := registrar.Diagnose()
		fmt.Fprintf(cmd.OutOrStdout(), "Summary: %s\n", diag.Summary())
		for _, check := range diag.Checks {
			fmt.Fprintf(cmd.OutOrStdout(), "[OK] %s\n", check)
		}

		if diag.Status == protocolreg.StatusOK {
			fmt.Fprintln(cmd.OutOrStdout(), "GLASSBOX Protocol handler is currently REGISTERED")
			return nil
		}

		fmt.Fprintln(cmd.ErrOrStderr(), "GLASSBOX Protocol handler is NOT REGISTERED")
		if len(diag.RemediationSteps) > 0 {
			fmt.Fprintln(cmd.ErrOrStderr(), "\nTo register the protocol handler:")
			for i, step := range diag.RemediationSteps {
				fmt.Fprintf(cmd.ErrOrStderr(), "  %d. %s\n", i+1, step)
			}
		}
		return fmt.Errorf("GLASSBOX Protocol handler is NOT REGISTERED")
	},
}

var protocolVerifyCmd = &cobra.Command{
	Use:     "protocol:verify",
	Aliases: []string{"pb:verify", "verify-protocol-registration"},
	Short:   "Verify the native OS registration for the glassbox:// protocol handler",
	Long: `Verify that the glassbox:// protocol handler is registered correctly on this system.

The command inspects OS registration artefacts and reports pass/fail for each check.
Use --probe to simulate a glassbox://doctor-probe deep link and confirm the handler
responds without side effects.

On failure, remediation steps are printed to help repair the registration.`,
	Example: `  # Verify the registration is working
  glassbox protocol:verify

  # Verify and run a live probe that exercises the OS dispatch path
  glassbox protocol:verify --probe

  # Register first, then verify
  glassbox protocol:register && glassbox protocol:verify`,
	GroupID: "utility",
	RunE: func(cmd *cobra.Command, args []string) error {
		registrar, err := protocolreg.NewRegistrar()
		if err != nil {
			return err
		}

		report, err := registrar.Verify()
		for _, check := range report.Checks {
			fmt.Fprintf(cmd.OutOrStdout(), "[OK] %s\n", check)
		}
		for _, issue := range report.Issues {
			fmt.Fprintf(cmd.ErrOrStderr(), "[FAIL] %s\n", issue)
		}
		probePassed := true
		if protocolVerifyProbe {
			exePath, exeErr := os.Executable()
			if exeErr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "[FAIL] Handler probe: cannot resolve executable: %v\n", exeErr)
				probePassed = false
			} else if deeplink.ProbeHandler(exePath) {
				fmt.Fprintf(cmd.OutOrStdout(), "[OK] Handler probe (%s) exited cleanly\n", deeplink.MockURL)
			} else {
				probePassed = false
				fmt.Fprintf(cmd.ErrOrStderr(), "[FAIL] Handler probe (%s) did not complete successfully\n", deeplink.MockURL)
				fmt.Fprintf(cmd.ErrOrStderr(), "       Ensure the binary handles --deep-link %s\n", deeplink.MockURL)
			}
		}

		if err != nil || !probePassed {
			diag := registrar.Diagnose()
			fmt.Fprintf(cmd.ErrOrStderr(), "\nSummary: %s\n", diag.Summary())
			if len(diag.RemediationSteps) > 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "\nRemediation steps:\n")
				for i, step := range diag.RemediationSteps {
					fmt.Fprintf(cmd.ErrOrStderr(), "  %d. %s\n", i+1, step)
				}
			}
		}

		if err != nil {
			return err
		}
		if !probePassed {
			return fmt.Errorf("protocol handler probe failed")
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Verified GLASSBOX Protocol registration on %s (%dms)\n", report.Platform, report.ElapsedMs)
		fmt.Fprintf(cmd.OutOrStdout(), "Summary: %s\n", report.Summary())
		return nil
	},
}

var protocolHandlerCmd = &cobra.Command{
	Use:     "protocol:handle <uri>",
	Aliases: []string{"protocol-handler", "pb:handle"},
	Short:   "Handle an glassbox:// protocol URI and dispatch it to the debugger",
	Long: `Parse and dispatch a glassbox:// deep link URI to the Glassbox debugger.

This command is invoked automatically by the OS when a glassbox:// link is opened
(e.g. from a browser or another application). It validates the URI, extracts the
transaction hash and query parameters, and re-invokes 'glassbox debug' with the
appropriate flags.

URI format:
  glassbox://debug/<64-char-hex>?network=<network>[&op=<n>][&view=<mode>]

Required parameters:
  network   One of: testnet, mainnet, futurenet

Optional parameters:
  op        Zero-based operation index (e.g. op=0 for the first operation)
  operation Alias for op (legacy; op takes precedence when both are provided)
  view      Initial view mode: trace, flamegraph, events, auth, budget, storage
  source    Free-form source identifier (e.g. "dashboard")
  signature Free-form signature hint

Exit codes:
  0  — dispatch succeeded
  1  — URI is invalid or the debug sub-command failed`,
	Example: `  # Open a transaction debug session from a deep link
  glassbox protocol:handle "glassbox://debug/abc123...def?network=testnet"

  # Open a specific operation on futurenet with the flamegraph view
  glassbox protocol:handle "glassbox://debug/abc123...def?network=futurenet&op=1&view=flamegraph"

  # Test the handler manually with a known-good URI
  glassbox protocol:handle "glassbox://debug/0000000000000000000000000000000000000000000000000000000000000001?network=testnet"`,
	GroupID: "utility",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		raw := strings.TrimSpace(args[0])

		// Dispatch glassbox://trace/ URIs to the trace-step navigator.
		if strings.HasPrefix(raw, protocolreg.Scheme+"://trace/") {
			return dispatchTraceStepURI(cmd, raw)
		}

		parsed, err := protocolreg.ParseDebugURI(raw)
		if err != nil {
			return fmt.Errorf(
				"%w\n"+
					"  Expected format: glassbox://debug/<64-char-hex>?network=<testnet|mainnet|futurenet>[&op=<n>][&view=<mode>]\n"+
					"  or trace format: glassbox://trace/<64-char-hex>/step/<N>?network=<testnet|mainnet|futurenet>\n"+
					"  Run 'glassbox protocol:handle --help' for full parameter documentation",
				err,
			)
		}

		executablePath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve executable path: %w", err)
		}

		debugArgs := []string{"debug", parsed.TransactionHash, "--network", parsed.Network}

		// Forward the operation index when present (prefer Op, fall back to Operation).
		opIdx := parsed.Op
		if opIdx == nil {
			opIdx = parsed.Operation
		}
		if opIdx != nil {
			debugArgs = append(debugArgs, "--op", fmt.Sprintf("%d", *opIdx))
		}

		// Forward the view mode when present.
		if parsed.View != "" {
			debugArgs = append(debugArgs, "--view", parsed.View)
		}

		// Forward protocol version override when present.
		if parsed.ProtocolVersion != nil {
			debugArgs = append(debugArgs, "--protocol-version", fmt.Sprintf("%d", *parsed.ProtocolVersion))
		}

		// Forward mock ledger manifest when present.
		if parsed.MockLedgerManifest != "" {
			debugArgs = append(debugArgs, "--mock-ledger-manifest", parsed.MockLedgerManifest)
		}

		// Forward mock ledger entries when present.
		for _, entry := range parsed.MockLedgerEntries {
			debugArgs = append(debugArgs, "--mock-ledger-entry", entry)
		}

		// Validate W3C trace-context fields when present.
		// Invalid trace context does not abort dispatch — it surfaces a warning
		// so the user knows correlation may be incomplete without blocking the
		// debug session.  The validated context is propagated to the child via
		// environment variables so callers that embed trace propagation headers
		// can correlate the debug session with an originating distributed trace.
		tc := protocolreg.TraceContextFromURI(parsed)
		if tc != nil {
			validation := protocolreg.ValidateTraceContext(tc)
			if !validation.OK {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"[WARN] trace context in URI has validation issues — "+
						"distributed trace correlation may be incomplete:\n%s\n",
					validation.Summary(),
				)
			} else {
				// Propagate valid trace context via environment so the child
				// debug process can attach to the originating trace span.
				if tc.Traceparent != "" {
					_ = os.Setenv("GLASSBOX_TRACEPARENT", tc.Traceparent)
				}
				if tc.Tracestate != "" {
					_ = os.Setenv("GLASSBOX_TRACESTATE", tc.Tracestate)
				}
				if tc.TraceID != "" {
					_ = os.Setenv("GLASSBOX_TRACE_ID", tc.TraceID)
				}
				if tc.SpanID != "" {
					_ = os.Setenv("GLASSBOX_SPAN_ID", tc.SpanID)
				}
			}
		}

		child := exec.CommandContext(cmd.Context(), executablePath, debugArgs...)
		child.Stdout = cmd.OutOrStdout()
		child.Stderr = cmd.ErrOrStderr()
		return child.Run()
	},
}

var protocolDiagnoseCmd = &cobra.Command{
	Use:     "protocol:diagnose",
	Aliases: []string{"pb:diagnose"},
	Short:   "Inspect the glassbox:// protocol registration and report root causes",
	GroupID: "utility",
	Long: `Inspect the glassbox:// protocol registration on the current OS and produce
a structured diagnostic report.

The command checks:
  • Whether the protocol handler is registered with the OS
  • Whether the registered handler points to the current executable
  • Platform-specific registration artefacts (.desktop file, registry key, app bundle)
  • xdg-mime / LaunchServices / registry consistency

Exit codes:
  0  — registration is healthy
  1  — registration is missing or broken (issues are printed to stderr)`,
	Example: `  # Inspect the registration and print a text report
  glassbox protocol:diagnose

  # Emit the diagnostic report as machine-readable JSON
  glassbox protocol:diagnose --format json

  # Use --json shorthand for JSON output
  glassbox protocol:diagnose --json

  # Pipe JSON output to jq for filtering
  glassbox protocol:diagnose --json | jq '.status'`,
	RunE: func(cmd *cobra.Command, args []string) error {
		registrar, err := protocolreg.NewRegistrar()
		if err != nil {
			return err
		}

		report := registrar.Diagnose()

		// Validate and resolve the output format.
		normalizedFormat := strings.ToLower(strings.TrimSpace(protocolDiagnoseFormat))
		if normalizedFormat != "" && normalizedFormat != "text" && normalizedFormat != "json" {
			return fmt.Errorf("invalid --format %q: must be 'text' or 'json'", protocolDiagnoseFormat)
		}
		if clioutput.WantsJSON(protocolDiagnoseJSON, normalizedFormat) {
			return clioutput.Write(cmd.OutOrStdout(), "protocol:diagnose", report)
		}

		for _, check := range report.Checks {
			fmt.Fprintf(cmd.OutOrStdout(), "[OK]   %s\n", check)
		}
		for _, issue := range report.Issues {
			fmt.Fprintf(cmd.ErrOrStderr(), "[FAIL] %s\n", issue)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "\nSummary: %s\n", report.Summary())
		fmt.Fprintf(cmd.OutOrStdout(), "Status: %s  (platform: %s, scheme: %s://)\n",
			report.Status, report.Platform, report.Scheme)

		if report.RegisteredHandler != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "Registered handler: %s\n", report.RegisteredHandler)
			if report.HandlerMatchesSelf {
				fmt.Fprintf(cmd.OutOrStdout(), "Handler matches current executable: yes\n")
			} else if report.ConflictDetected {
				// Conflict: a foreign binary owns the registration.
				fmt.Fprintf(cmd.ErrOrStderr(),
					"Handler matches current executable: NO (conflict — registered handler is %s)\n",
					report.ConflictingHandler)
				fmt.Fprintf(cmd.ErrOrStderr(),
					"⚠  Protocol conflict detected: the glassbox:// scheme is currently handled by\n"+
						"   a different binary (%s).\n"+
						"   Run 'glassbox protocol:repair' to reclaim the registration.\n",
					report.ConflictingHandler)
			} else {
				fmt.Fprintf(cmd.ErrOrStderr(), "Handler matches current executable: NO (stale path)\n")
			}
		}

		if len(report.RemediationSteps) > 0 {
			fmt.Fprintf(cmd.ErrOrStderr(), "\nRemediation steps:\n")
			for i, step := range report.RemediationSteps {
				fmt.Fprintf(cmd.ErrOrStderr(), "  %d. %s\n", i+1, step)
			}
		}

		if report.Status != protocolreg.StatusOK {
			if report.ConflictDetected {
				return fmt.Errorf(
					"protocol registration conflict: glassbox:// is claimed by %s — run 'glassbox protocol:repair' to resolve",
					report.ConflictingHandler,
				)
			}
			return fmt.Errorf("protocol registration is %s", report.Status)
		}
		return nil
	},
}

var protocolRepairCmd = &cobra.Command{
	Use:     "protocol:repair",
	Aliases: []string{"pb:repair"},
	Short:   "Repair a broken or missing glassbox:// protocol registration",
	GroupID: "utility",
	Long: `Attempt to repair the glassbox:// protocol handler registration.

The command first runs a diagnostic check to understand what is broken, then
re-registers the protocol handler using the best available platform mechanism:

  • Linux  — recreates the .desktop file and updates xdg-mime
  • macOS  — rebuilds the app bundle and re-registers with LaunchServices
  • Windows — updates the HKEY_CURRENT_USER registry keys

After repair, a verification pass confirms the fix was successful.

PERMISSION NOTES
  On Windows, registry writes to HKEY_CURRENT_USER do not require elevation.
  On Linux and macOS, the handler is installed per-user (~/.local/share or
  ~/Applications) and does not require root.`,
	Example: `  # Attempt to repair a broken or missing registration
  glassbox protocol:repair

  # Diagnose first, then repair if needed
  glassbox protocol:diagnose || glassbox protocol:repair

  # Repair and verify the fix in one step
  glassbox protocol:repair && glassbox protocol:verify`,
	RunE: func(cmd *cobra.Command, args []string) error {
		registrar, err := protocolreg.NewRegistrar()
		if err != nil {
			return err
		}

		result := registrar.Repair()

		for _, action := range result.Actions {
			fmt.Fprintf(cmd.OutOrStdout(), "  • %s\n", action)
		}

		if result.PermissionHint != "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "\nPermission hint: %s\n", result.PermissionHint)
		}

		if result.Err != nil {
			return result.Err
		}

		if result.Repaired {
			fmt.Fprintf(cmd.OutOrStdout(), "\nRepair successful. The %s:// protocol handler is now registered.\n",
				protocolreg.Scheme)
		}
		return nil
	},
}

// dispatchTraceStepURI handles a glassbox://trace/<txhash>/step/<N> URI by
// validating it and re-invoking the binary with appropriate flags.
//
// Currently the trace-step URI triggers `glassbox trace --step N --network
// <net> [--source-file <f>] [--source-line <n>] <txhash>`.  When the trace
// command or extension server is not running the user receives a clear error
// with a fix hint rather than a silent failure.
func dispatchTraceStepURI(cmd *cobra.Command, raw string) error {
	parsed, err := protocolreg.ParseTraceStepURI(raw)
	if err != nil {
		return fmt.Errorf(
			"%w\n"+
				"  Expected format: glassbox://trace/<64-char-hex>/step/<N>?network=<testnet|mainnet|futurenet>\n"+
				"  Optional params: &file=<path>&line=<n>&col=<n>&view=<mode>\n"+
				"  Run 'glassbox protocol:handle --help' for full parameter documentation",
			err,
		)
	}

	executablePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}

	traceArgs := []string{
		"trace",
		"--network", parsed.Network,
		"--step", fmt.Sprintf("%d", parsed.StepIndex),
	}

	if parsed.File != "" {
		traceArgs = append(traceArgs, "--source-file", parsed.File)
	}
	if parsed.Line > 0 {
		traceArgs = append(traceArgs, "--source-line", fmt.Sprintf("%d", parsed.Line))
	}
	if parsed.View != "" {
		traceArgs = append(traceArgs, "--view", parsed.View)
	}

	// Transaction hash is the positional argument for the trace command.
	traceArgs = append(traceArgs, parsed.TransactionHash)

	child := exec.CommandContext(cmd.Context(), executablePath, traceArgs...)
	child.Stdout = cmd.OutOrStdout()
	child.Stderr = cmd.ErrOrStderr()
	if runErr := child.Run(); runErr != nil {
		return fmt.Errorf(
			"trace-step navigation failed for step %d of tx %s: %w\n"+
				"  Tip: run 'glassbox trace --step %d --network %s %s' to debug this directly",
			parsed.StepIndex, parsed.TransactionHash, runErr,
			parsed.StepIndex, parsed.Network, parsed.TransactionHash,
		)
	}
	return nil
}

func init() {
	protocolDiagnoseCmd.Flags().BoolVar(&protocolDiagnoseJSON, "json", false,
		"Emit diagnostic report as JSON (shorthand for --format json)")
	protocolDiagnoseCmd.Flags().StringVar(&protocolDiagnoseFormat, "format", "",
		"Output format: 'text' (default) or 'json'")
	protocolVerifyCmd.Flags().BoolVar(&protocolVerifyProbe, "probe", false,
		"Run a dry-run glassbox://doctor-probe handler check after registration verification")
	protocolRegisterCmd.Flags().BoolVar(&protocolRegisterDryRun, "dry-run", false,
		"Preview what would be registered without modifying OS state")

	rootCmd.AddCommand(protocolRegisterCmd)
	rootCmd.AddCommand(protocolUnregisterCmd)
	rootCmd.AddCommand(protocolStatusCmd)
	rootCmd.AddCommand(protocolVerifyCmd)
	rootCmd.AddCommand(protocolHandlerCmd)
	rootCmd.AddCommand(protocolDiagnoseCmd)
	rootCmd.AddCommand(protocolRepairCmd)
}
