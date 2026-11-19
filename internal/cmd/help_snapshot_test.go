// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// help_snapshot_test.go validates that command help text remains stable
// and complete across releases.  It checks:
//
//  1. Short descriptions are within a reasonable length
//  2. Long help and Example fields are set for critical commands
//  3. Group assignments are correct
//  4. Help snapshots for key commands are deterministic
//
// Run with -update to regenerate snapshots:
//
//	go test ./internal/cmd -run TestHelpSnapshot -update-help
package cmd

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// -update regenerates the golden help snapshot files instead of comparing.
var updateHelpSnapshots = flag.Bool("update-help", false, "regenerate help snapshots")

const helpSnapshotDir = "testdata/help"

// ── Short description length validation ──────────────────────────────────────

// TestAllCommandsHaveShortDescription verifies every non-hidden command
// has a non-empty Short description.
func TestAllCommandsHaveShortDescription(t *testing.T) {
	var walk func(cmds []*cobra.Command)
	walk = func(cmds []*cobra.Command) {
		for _, cmd := range cmds {
			if cmd.Hidden {
				continue
			}
			if cmd.Short == "" {
				t.Errorf("command %q has empty Short description", cmd.Use)
			}
			if len(cmd.Short) > 100 {
				t.Errorf("command %q Short description too long: %d chars (max 100): %q",
					cmd.Use, len(cmd.Short), cmd.Short)
			}
			walk(cmd.Commands())
		}
	}
	walk(rootCmd.Commands())
}

// TestAllCommandsHaveNonEmptyUse verifies every visible command has a Use field.
func TestAllCommandsHaveNonEmptyUse(t *testing.T) {
	var walk func(cmds []*cobra.Command)
	walk = func(cmds []*cobra.Command) {
		for _, cmd := range cmds {
			if cmd.Hidden {
				continue
			}
			if strings.TrimSpace(cmd.Use) == "" {
				t.Errorf("command has empty Use field")
			}
			walk(cmd.Commands())
		}
	}
	walk(rootCmd.Commands())
}

// ── Critical command structure validation ────────────────────────────────────

// TestCriticalCommandsHaveLongAndExample verifies that critical commands
// have both Long and Example fields set, which is required for proper help
// output and documentation generation.
func TestCriticalCommandsHaveLongAndExample(t *testing.T) {
	criticalCmds := map[string]bool{
		"debug":        true,
		"audit:sign":   true,
		"audit:verify": true,
		"session":      true,
		"version":      true,
		"doctor":       true,
		"diagnostics":  true,
		"init":         true,
	}

	var walk func(cmds []*cobra.Command)
	walk = func(cmds []*cobra.Command) {
		for _, cmd := range cmds {
			if cmd.Hidden {
				continue
			}
			name := cmd.Use
			if criticalCmds[name] {
				if cmd.Long == "" {
					t.Errorf("critical command %q missing Long description", name)
				}
				if cmd.Example == "" {
					t.Errorf("critical command %q missing Example field", name)
				}
			}
			walk(cmd.Commands())
		}
	}
	walk(rootCmd.Commands())
}

// ── Group assignment validation ──────────────────────────────────────────────

// TestCommandsHaveValidGroupIDs verifies that all commands with a GroupID
// set have it assigned to one of the known groups.
func TestCommandsHaveValidGroupIDs(t *testing.T) {
	validGroups := map[string]bool{
		"core":        true,
		"testing":     true,
		"management":  true,
		"development": true,
		"utility":     true,
	}

	var walk func(cmds []*cobra.Command)
	walk = func(cmds []*cobra.Command) {
		for _, cmd := range cmds {
			if cmd.GroupID != "" && !validGroups[cmd.GroupID] {
				t.Errorf("command %q has unknown GroupID %q", cmd.Use, cmd.GroupID)
			}
			walk(cmd.Commands())
		}
	}
	walk(rootCmd.Commands())
}

// TestAllCommandGroupsExist verifies that all defined groups have at least
// one command assigned to them.
func TestAllCommandGroupsExist(t *testing.T) {
	groups := map[string]bool{
		"core":        false,
		"testing":     false,
		"management":  false,
		"development": false,
		"utility":     false,
	}

	var walk func(cmds []*cobra.Command)
	walk = func(cmds []*cobra.Command) {
		for _, cmd := range cmds {
			if cmd.GroupID != "" {
				groups[cmd.GroupID] = true
			}
			walk(cmd.Commands())
		}
	}
	walk(rootCmd.Commands())

	for group, found := range groups {
		if !found {
			t.Errorf("group %q has no commands assigned", group)
		}
	}
}

// ── Help text does not leak secrets ──────────────────────────────────────────

// TestHelpTextNeverLeaksSecrets verifies that help text for all commands
// does not contain sensitive patterns like private keys or PINs.
func TestHelpTextNeverLeaksSecrets(t *testing.T) {
	sensitive := []string{
		"-----BEGIN",
		"PRIVATE KEY",
		"password",
		"secret",
	}

	var walk func(cmds []*cobra.Command)
	walk = func(cmds []*cobra.Command) {
		for _, cmd := range cmds {
			if cmd.Hidden {
				continue
			}
			helpText := cmd.Short + " " + cmd.Long + " " + cmd.Example
			for _, pat := range sensitive {
				if strings.Contains(strings.ToLower(helpText), strings.ToLower(pat)) {
					t.Errorf("command %q help contains sensitive pattern %q", cmd.Use, pat)
				}
			}
			walk(cmd.Commands())
		}
	}
	walk(rootCmd.Commands())
}

// ── Help text structural invariants ──────────────────────────────────────────

// TestHelpTextDoesNotContainVersionPlaceholder verifies that help text
// doesn't contain unexpanded version placeholders.
func TestHelpTextDoesNotContainVersionPlaceholder(t *testing.T) {
	var walk func(cmds []*cobra.Command)
	walk = func(cmds []*cobra.Command) {
		for _, cmd := range cmds {
			if cmd.Hidden {
				continue
			}
			helpText := cmd.Short + " " + cmd.Long
			if strings.Contains(helpText, "{{version}}") {
				t.Errorf("command %q help contains unexpanded {{version}} placeholder", cmd.Use)
			}
			walk(cmd.Commands())
		}
	}
	walk(rootCmd.Commands())
}

// TestShortDescriptionsAreNotIdentical verifies that no two commands have
// the same Short description (which would indicate a copy-paste error).
func TestShortDescriptionsAreNotIdentical(t *testing.T) {
	seen := map[string]string{} // Short -> first command Use
	var walk func(cmds []*cobra.Command)
	walk = func(cmds []*cobra.Command) {
		for _, cmd := range cmds {
			if cmd.Hidden || cmd.Short == "" {
				continue
			}
			if first, ok := seen[cmd.Short]; ok {
				t.Errorf("commands %q and %q have identical Short description: %q",
					first, cmd.Use, cmd.Short)
			}
			seen[cmd.Short] = cmd.Use
			walk(cmd.Commands())
		}
	}
	walk(rootCmd.Commands())
}

// ── Help snapshot tests ──────────────────────────────────────────────────────

// runHelpSnapshotTest captures the help output for a command and compares
// it against a golden file.  Use -update-help to regenerate.
func runHelpSnapshotTest(t *testing.T, name string, cmd *cobra.Command) {
	t.Helper()

	helpBuf := &bytes.Buffer{}
	cmd.SetOut(helpBuf)
	cmd.SetArgs([]string{"--help"})

	// Execute the command to capture help output.
	err := cmd.Execute()
	// help commands typically exit with 0 after printing help.
	// We capture the output regardless of exit code.
	_ = err

	got := helpBuf.Bytes()
	if len(got) == 0 {
		t.Fatalf("help output for %q is empty", name)
	}

	path := filepath.Join(helpSnapshotDir, name+".txt")

	if *updateHelpSnapshots {
		if err := os.MkdirAll(helpSnapshotDir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", helpSnapshotDir, err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write snapshot %s: %v", path, err)
		}
		t.Logf("updated help snapshot: %s", path)
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf(
			"help snapshot %s not found — run 'go test ./internal/cmd -run %s -update-help' to generate it",
			path, t.Name(),
		)
	}

	if !bytes.Equal(want, got) {
		t.Errorf(
			"help text for %s is stale\n"+
				"  snapshot: %s\n"+
				"  Run: go test ./internal/cmd -run %s -update-help\n"+
				"  to regenerate it.",
			name, path, t.Name(),
		)
	}
}

// TestHelpSnapshot_Root verifies the root command help output.
func TestHelpSnapshot_Root(t *testing.T) {
	runHelpSnapshotTest(t, "root", rootCmd)
}

// TestHelpSnapshot_Debug verifies the debug command help output.
func TestHelpSnapshot_Debug(t *testing.T) {
	runHelpSnapshotTest(t, "debug", debugCmd)
}

// TestHelpSnapshot_Version verifies the version command help output.
func TestHelpSnapshot_Version(t *testing.T) {
	runHelpSnapshotTest(t, "version", versionCmd)
}

// TestHelpSnapshot_AuditSign verifies the audit:sign command help output.
func TestHelpSnapshot_AuditSign(t *testing.T) {
	runHelpSnapshotTest(t, "audit_sign", auditSignCmd)
}

// TestHelpSnapshot_AuditVerify verifies the audit:verify command help output.
func TestHelpSnapshot_AuditVerify(t *testing.T) {
	runHelpSnapshotTest(t, "audit_verify", auditVerifyCmd)
}

// TestHelpSnapshot_Session verifies the session command help output.
func TestHelpSnapshot_Session(t *testing.T) {
	runHelpSnapshotTest(t, "session", sessionCmd)
}

// TestHelpSnapshot_Completion verifies the completion command help output.
func TestHelpSnapshot_Completion(t *testing.T) {
	runHelpSnapshotTest(t, "completion", completionCmd)
}

// TestHelpSnapshot_Diagnostics verifies the diagnostics command help output.
func TestHelpSnapshot_Diagnostics(t *testing.T) {
	runHelpSnapshotTest(t, "diagnostics", diagnosticsCmd)
}

// TestHelpSnapshot_Doctor verifies the doctor command help output.
func TestHelpSnapshot_Doctor(t *testing.T) {
	runHelpSnapshotTest(t, "doctor", doctorCmd)
}

// Ensure updateHelpSnapshots is used (suppress unused warning in non-update builds)
var _ = updateHelpSnapshots
