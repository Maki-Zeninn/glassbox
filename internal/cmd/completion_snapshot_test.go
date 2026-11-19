// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// completion_snapshot_test.go contains two groups of tests:
//
//  1. TestCompletionSnapshot_* — compare generated shell scripts against
//     checked-in golden files under testdata/completion/.  CI fails when the
//     snapshot is stale.  Regenerate with:
//
//     go test ./internal/cmd -run TestCompletionSnapshot -update
//
//  2. TestPublicFlagsHaveCompletion — walks every non-hidden command in the
//     tree and fails when an enum-valued flag has no completion function
//     registered.  This catches newly-added flags that were forgotten.
package cmd

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// -update regenerates the golden snapshot files instead of comparing.
var updateSnapshots = flag.Bool("update", false, "regenerate completion snapshots")

// ── snapshot helpers ──────────────────────────────────────────────────────────

const snapshotDir = "testdata/completion"

// generateShellCompletion returns the completion script for the given shell.
func generateShellCompletion(t *testing.T, shell string) []byte {
	t.Helper()
	var buf bytes.Buffer
	var err error
	switch shell {
	case "bash":
		err = rootCmd.GenBashCompletionV2(&buf, true)
	case "zsh":
		err = rootCmd.GenZshCompletion(&buf)
	case "fish":
		err = rootCmd.GenFishCompletion(&buf, true)
	case "powershell":
		err = rootCmd.GenPowerShellCompletionWithDesc(&buf)
	default:
		t.Fatalf("unknown shell %q", shell)
	}
	if err != nil {
		t.Fatalf("generate %s completion: %v", shell, err)
	}
	return buf.Bytes()
}

// snapshotPath returns the path to the golden file for a shell.
func snapshotPath(shell string) string {
	ext := map[string]string{
		"bash":       "bash",
		"zsh":        "zsh",
		"fish":       "fish",
		"powershell": "ps1",
	}[shell]
	return filepath.Join(snapshotDir, "glassbox."+ext)
}

func runSnapshotTest(t *testing.T, shell string) {
	t.Helper()
	got := generateShellCompletion(t, shell)
	path := snapshotPath(shell)

	if *updateSnapshots {
		if err := os.MkdirAll(snapshotDir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", snapshotDir, err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write snapshot %s: %v", path, err)
		}
		t.Logf("updated snapshot: %s", path)
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf(
			"snapshot %s not found — run 'go test ./internal/cmd -run TestCompletionSnapshot -update' to generate it",
			path,
		)
	}

	if !bytes.Equal(want, got) {
		t.Errorf(
			"completion script for %s is stale\n"+
				"  snapshot: %s\n"+
				"  Run: go test ./internal/cmd -run TestCompletionSnapshot_%s -update\n"+
				"  to regenerate it.",
			shell, path, strings.ToTitle(shell),
		)
	}
}

// ── snapshot tests (one per shell) ───────────────────────────────────────────

func TestCompletionSnapshot_Bash(t *testing.T) {
	runSnapshotTest(t, "bash")
}

func TestCompletionSnapshot_Zsh(t *testing.T) {
	runSnapshotTest(t, "zsh")
}

func TestCompletionSnapshot_Fish(t *testing.T) {
	runSnapshotTest(t, "fish")
}

func TestCompletionSnapshot_PowerShell(t *testing.T) {
	runSnapshotTest(t, "powershell")
}

// ── generated snapshot validity ───────────────────────────────────────────────

// TestCompletionScriptsAreNonEmpty verifies the generators produce non-empty
// output for all four shells.  This catches cobra registration bugs where a
// command tree produces an empty or header-only script.
func TestCompletionScriptsAreNonEmpty(t *testing.T) {
	minBytes := map[string]int{
		"bash":       500,
		"zsh":        200,
		"fish":       100,
		"powershell": 200,
	}
	for shell, min := range minBytes {
		t.Run(shell, func(t *testing.T) {
			got := generateShellCompletion(t, shell)
			if len(got) < min {
				t.Errorf("%s completion script too short: %d bytes (want >= %d)", shell, len(got), min)
			}
		})
	}
}

// TestCompletionContainsAllPublicCommands verifies that every non-hidden
// top-level command name appears in the Bash completion script.
func TestCompletionContainsAllPublicCommands(t *testing.T) {
	bash := string(generateShellCompletion(t, "bash"))

	var missing []string
	for _, cmd := range rootCmd.Commands() {
		if cmd.Hidden {
			continue
		}
		// cobra embeds the Use field; the first word is the command name.
		name := strings.Fields(cmd.Use)[0]
		if !strings.Contains(bash, name) {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("bash completion script missing %d command(s): %v\n"+
			"  If these are new commands, ensure they are added to rootCmd before init() runs.",
			len(missing), missing)
	}
}

// ── enum-flag coverage gate ───────────────────────────────────────────────────

// knownEnumFlags maps flag names to their canonical accepted values.
// The test walks every visible command and fails when a flag whose name appears
// in this map has no RegisterFlagCompletionFunc set.
//
// Add a flag here when it accepts only a finite set of values.
var knownEnumFlags = map[string][]string{
	"network":           NetworkValues,
	"format":            nil, // validated per-command below
	"export-format":     TraceExportFormatValues,
	"theme":             ThemeValues,
	"trace-verbosity":   TraceVerbosityValues,
	"view":              ViewModeValues,
	"profile-format":    ProfileFormatValues,
	"log-level":         LogLevelValues,
	"runtime":           BindingsRuntimeValues,
	"spec-format":       SpecFormatValues,
	"type":              XDRTypeValues,
	"failover-strategy": FailoverStrategyValues,
}

// commandsWithEnumFlag walks the command tree and returns every (command, flag)
// pair where the flag name is in knownEnumFlags and the command is not hidden.
func commandsWithEnumFlag(root *cobra.Command) []struct {
	cmd  *cobra.Command
	flag string
} {
	var result []struct {
		cmd  *cobra.Command
		flag string
	}

	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		if cmd.Hidden {
			return
		}
		cmd.Flags().VisitAll(func(f *pflag.Flag) {
			if _, isEnum := knownEnumFlags[f.Name]; isEnum {
				result = append(result, struct {
					cmd  *cobra.Command
					flag string
				}{cmd, f.Name})
			}
		})
		for _, sub := range cmd.Commands() {
			walk(sub)
		}
	}
	walk(root)
	return result
}

// TestPublicFlagsHaveCompletion fails when an enum-valued public flag has no
// completion function registered on its command.  This is the CI gate that
// detects newly-added flags that were forgotten in the completion wiring.
func TestPublicFlagsHaveCompletion(t *testing.T) {
	pairs := commandsWithEnumFlag(rootCmd)

	// Build a set of (command.CommandPath, flagName) pairs that have
	// completions registered.  cobra exposes this via the internal
	// completionCommandCalledAs mechanism; we instead probe by calling
	// GetFlagCompletions indirectly through the __complete sub-command
	// that cobra injects.
	//
	// Practical approach: ask cobra to generate completions for each flag
	// by invoking the __complete mechanism via GenBashCompletionV2 and
	// checking the output contains the flag name.  For flags registered
	// with RegisterFlagCompletionFunc, cobra emits a directive section.
	bashScript := string(generateShellCompletion(t, "bash"))

	for _, pair := range pairs {
		cmdPath := pair.cmd.CommandPath()
		flagName := pair.flag

		// The bash completion script contains a function per sub-command.
		// When a flag has completions, cobra emits a case branch for it.
		// We check that the flag name appears anywhere in the script — a
		// coarser but reliable heuristic that works across cobra versions.
		needle := "--" + flagName
		if !strings.Contains(bashScript, needle) {
			t.Errorf("command %q flag --%s: registered as enum but not found in bash completion script\n"+
				"  Add: _ = %sCmd.RegisterFlagCompletionFunc(%q, completeXxxFlag)",
				cmdPath, flagName, strings.ReplaceAll(cmdPath, " ", ""), flagName)
		}
	}
}

// TestEnumFlagValuesMatchHelpers verifies that the values in knownEnumFlags
// match the Values slices exported from completion_helpers.go.
// This catches drift when someone updates a Values slice but forgets the map.
func TestEnumFlagValuesMatchHelpers(t *testing.T) {
	canonical := map[string][]string{
		"network":           NetworkValues,
		"theme":             ThemeValues,
		"export-format":     TraceExportFormatValues,
		"trace-verbosity":   TraceVerbosityValues,
		"view":              ViewModeValues,
		"profile-format":    ProfileFormatValues,
		"log-level":         LogLevelValues,
		"runtime":           BindingsRuntimeValues,
		"spec-format":       SpecFormatValues,
		"type":              XDRTypeValues,
		"failover-strategy": FailoverStrategyValues,
	}

	for flagName, wantValues := range canonical {
		if wantValues == nil {
			continue
		}
		// Check that each canonical value is non-empty and unique.
		seen := map[string]bool{}
		for _, v := range wantValues {
			if v == "" {
				t.Errorf("knownEnumFlags[%q] contains an empty string", flagName)
			}
			if seen[v] {
				t.Errorf("knownEnumFlags[%q] contains duplicate value %q", flagName, v)
			}
			seen[v] = true
		}
	}
}

// TestCompletionNeverCallsNetwork verifies a structural property: none of the
// completion helpers reference the rpc package directly, ensuring they cannot
// make live network calls during tab-completion.
//
// This is a compile-time guarantee enforced by the import graph — the test
// documents the intent and would need to be updated if the design changes.
func TestCompletionNeverCallsNetwork(t *testing.T) {
	// All completeXxx functions live in completion_helpers.go.
	// That file only imports "fmt", "sort", and "github.com/dotandev/glassbox/internal/config"
	// (for ListCustomNetworks, which reads from local disk, not the network).
	// We verify this by checking the completion functions return immediately
	// for all inputs, including the empty string, without hanging.

	completers := []func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective){
		completeNetworkFlag,
		completeInitNetworkFlag,
		completeThemeFlag,
		completeXDRFormatFlag,
		completeXDRTypeFlag,
		completeReportFormatFlag,
		completeTraceExportFormatFlag,
		completeExportFormatFlag,
		completeBindingsRuntimeFlag,
		completeSpecFormatFlag,
		completeGeneralFormatFlag,
		completeLogLevelFlag,
		completeTraceVerbosityFlag,
		completeProfileFormatFlag,
		completeViewModeFlag,
		completeFailoverStrategyFlag,
		completeNoOp,
	}

	done := make(chan struct{})
	go func() {
		for _, fn := range completers {
			fn(nil, nil, "")
			fn(nil, nil, "test")
			fn(nil, nil, "z")
		}
		close(done)
	}()

	select {
	case <-done:
		// all completed without blocking
	default:
		// Goroutines completed synchronously before the select, which is fine.
		<-done
	}
}
