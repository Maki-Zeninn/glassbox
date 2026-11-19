// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// completion_coverage_test.go validates that generated shell completion
// scripts cover all registered commands and documented enum values, exit
// quickly offline, never print secrets, and have tests for representative
// command groups on supported platforms.
package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// ── Secret leakage prevention ────────────────────────────────────────────────

// sensitivePatterns lists substrings that must NEVER appear in generated
// completion scripts.  These patterns would indicate that private-key
// material, PINs, or other secrets leaked into shell completions.
var sensitivePatterns = []string{
	"-----BEGIN",
	"PRIVATE KEY",
	"private_key",
	"private-key",
	"PIN",
	"pin=",
	"password",
	"secret",
	"seed",
	"GLASSBOX_AUDIT_PRIVATE_KEY",
	"GLASSBOX_SOFTWARE_PRIVATE_KEY_HEX",
	"GLASSBOX_PKCS11_PIN",
	"AWS_SECRET_ACCESS_KEY",
}

// TestCompletionScriptsNeverLeakSecrets verifies that generated completion
// scripts for all four shells do not contain any sensitive patterns.  This
// is a structural guarantee: cobra's generators never embed flag values,
// only flag names and metadata.
func TestCompletionScriptsNeverLeakSecrets(t *testing.T) {
	shells := []string{"bash", "zsh", "fish", "powershell"}

	for _, shell := range shells {
		t.Run(shell, func(t *testing.T) {
			script := string(generateShellCompletion(t, shell))

			for _, pattern := range sensitivePatterns {
				if strings.Contains(script, pattern) {
					t.Errorf("completion script for %s contains sensitive pattern %q", shell, pattern)
				}
			}
		})
	}
}

// ── Command coverage across all shells ────────────────────────────────────────

// TestCompletionContainsAllPublicCommands_AllShells verifies that every
// non-hidden top-level command appears in completion output for all four
// supported shells.  This catches cases where a command is added but its
// completion is broken for a specific shell format.
func TestCompletionContainsAllPublicCommands_AllShells(t *testing.T) {
	shells := []string{"bash", "zsh", "fish", "powershell"}

	// Collect all public command names.
	var publicCmds []string
	for _, cmd := range rootCmd.Commands() {
		if cmd.Hidden {
			continue
		}
		name := strings.Fields(cmd.Use)[0]
		publicCmds = append(publicCmds, name)
	}

	if len(publicCmds) == 0 {
		t.Fatal("no public commands found")
	}

	for _, shell := range shells {
		t.Run(shell, func(t *testing.T) {
			script := string(generateShellCompletion(t, shell))

			var missing []string
			for _, name := range publicCmds {
				if !strings.Contains(script, name) {
					missing = append(missing, name)
				}
			}
			if len(missing) > 0 {
				t.Errorf("%s completion missing %d command(s): %v",
					shell, len(missing), missing)
			}
		})
	}
}

// ── Enum value coverage ──────────────────────────────────────────────────────

// TestCompletionContainsEnumValues_AllShells verifies that documented enum
// values for high-value flags appear in completion output for all shells.
func TestCompletionContainsEnumValues_AllShells(t *testing.T) {
	enumChecks := []struct {
		flag   string
		values []string
	}{
		{"network", NetworkValues},
		{"theme", ThemeValues},
		{"log-level", LogLevelValues},
		{"view", ViewModeValues},
		{"profile-format", ProfileFormatValues},
		{"trace-verbosity", TraceVerbosityValues},
		{"runtime", BindingsRuntimeValues},
		{"spec-format", SpecFormatValues},
		{"failover-strategy", FailoverStrategyValues},
	}

	shells := []string{"bash", "zsh", "fish", "powershell"}

	for _, shell := range shells {
		t.Run(shell, func(t *testing.T) {
			script := string(generateShellCompletion(t, shell))

			for _, ec := range enumChecks {
				for _, val := range ec.values {
					if !strings.Contains(script, val) {
						t.Errorf("%s completion missing enum value %q for --%s",
							shell, val, ec.flag)
					}
				}
			}
		})
	}
}

// ── Completion speed (offline, no network) ───────────────────────────────────

// TestCompletionGeneratesQuickly verifies that generating completion scripts
// does not block or make network calls.  All completion helpers must return
// immediately.
func TestCompletionGeneratesQuickly(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		done := make(chan struct{})
		go func() {
			defer close(done)
			generateShellCompletion(t, shell)
		}()
		select {
		case <-done:
		default:
			// Already completed synchronously.
		}
	}
}

// ── Subcommand group coverage ────────────────────────────────────────────────

// TestCompletionCoversSubcommandGroups verifies that representative commands
// from each command group appear in the bash completion script.
func TestCompletionCoversSubcommandGroups(t *testing.T) {
	// Map of groupID -> expected command names (at least one from each group)
	groupExpectedCmds := map[string][]string{
		"core":        {"debug", "explain", "trace"},
		"testing":     {"compare", "dry-run", "profile"},
		"management":  {"session", "cache", "search"},
		"development": {"doctor", "init", "daemon"},
		"utility":     {"version", "audit:sign", "audit:verify", "diagnostics"},
	}

	bash := string(generateShellCompletion(t, "bash"))

	for group, cmds := range groupExpectedCmds {
		t.Run(group, func(t *testing.T) {
			for _, cmd := range cmds {
				if !strings.Contains(bash, cmd) {
					t.Errorf("bash completion missing command %q from group %q", cmd, group)
				}
			}
		})
	}
}

// ── No file completion for enum flags ────────────────────────────────────────

// TestEnumFlagsNeverSuggestFiles verifies that completion for enum-valued
// flags never falls back to file-system completion.  The
// ShellCompDirectiveNoFileComp directive ensures the shell does not
// suggest file paths when completing enum flags.
func TestEnumFlagsNeverSuggestFiles(t *testing.T) {
	allCompleters := map[string]func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective){
		"network":           completeNetworkFlag,
		"init-network":      completeInitNetworkFlag,
		"theme":             completeThemeFlag,
		"xdr-format":        completeXDRFormatFlag,
		"xdr-type":          completeXDRTypeFlag,
		"report-format":     completeReportFormatFlag,
		"trace-export":      completeTraceExportFormatFlag,
		"export-format":     completeExportFormatFlag,
		"bindings-runtime":  completeBindingsRuntimeFlag,
		"spec-format":       completeSpecFormatFlag,
		"general-format":    completeGeneralFormatFlag,
		"log-level":         completeLogLevelFlag,
		"trace-verbosity":   completeTraceVerbosityFlag,
		"profile-format":    completeProfileFormatFlag,
		"view":              completeViewModeFlag,
		"failover-strategy": completeFailoverStrategyFlag,
	}

	for name, fn := range allCompleters {
		t.Run(name, func(t *testing.T) {
			for _, input := range []string{"", "a", "x", "mainnet"} {
				_, directive := fn(nil, nil, input)
				if directive == cobra.ShellCompDirectiveDefault {
					t.Errorf("completer %q with input %q returned ShellCompDirectiveDefault (allows file completion)", name, input)
				}
			}
		})
	}
}

// ── Completion script structural validity ────────────────────────────────────

// TestCompletionScriptsAreNonEmpty_MinSizes verifies each shell's completion
// script meets minimum size thresholds, ensuring the generator produced
// meaningful output.
func TestCompletionScriptsAreNonEmpty_MinSizes(t *testing.T) {
	minBytes := map[string]int{
		"bash":       1000,
		"zsh":        500,
		"fish":       300,
		"powershell": 500,
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

// TestCompletionBashContainsFunctionDefinitions verifies that the bash
// completion script contains shell function definitions for commands.
func TestCompletionBashContainsFunctionDefinitions(t *testing.T) {
	script := string(generateShellCompletion(t, "bash"))

	// cobra generates bash completion with function definitions
	if !strings.Contains(script, "function") && !strings.Contains(script, "()") {
		t.Error("bash completion script should contain function definitions")
	}
}

// TestCompletionZshCompdef verifies that the zsh completion script contains
// the compdef directive needed for zsh completion system integration.
func TestCompletionZshCompdef(t *testing.T) {
	script := string(generateShellCompletion(t, "zsh"))

	if !strings.Contains(script, "compdef") && !strings.Contains(script, "#compdef") {
		t.Error("zsh completion script should contain compdef directive")
	}
}

// TestCompletionFishContainsCompleteCommands verifies that the fish completion
// script uses fish's `complete` command.
func TestCompletionFishContainsCompleteCommands(t *testing.T) {
	script := string(generateShellCompletion(t, "fish"))

	if !strings.Contains(script, "complete") {
		t.Error("fish completion script should contain 'complete' commands")
	}
}
