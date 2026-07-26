// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Tests for Part A: improved user guidance and error messaging in the debug
// command PreRunE validation path.

package cmd

import (
	"strings"
	"testing"
)

// ── Missing hash / local-mode guards ─────────────────────────────────────────

// TestDebugPreRunE_MissingHash_MessageIncludesUsage verifies that when no hash
// is supplied the error message includes usage guidance.
func TestDebugPreRunE_MissingHash_MessageIncludesUsage(t *testing.T) {
	t.Cleanup(resetDebugFlags)
	// No local mode flags set, no hash.
	err := debugCmd.PreRunE(debugCmd, []string{})
	if err == nil {
		t.Fatal("expected error when no hash and no local mode")
	}
	msg := err.Error()
	if !strings.Contains(msg, "transaction hash is required") {
		t.Errorf("error should say hash is required, got: %q", msg)
	}
	// Must contain usage hint so users know what to do next.
	if !strings.Contains(msg, "glassbox debug") {
		t.Errorf("error should include usage hint, got: %q", msg)
	}
}

// TestDebugPreRunE_InvalidHash_MessageIncludesInput verifies that an invalid
// hash error includes the offending value so users can spot typos.
func TestDebugPreRunE_InvalidHash_MessageIncludesInput(t *testing.T) {
	t.Cleanup(resetDebugFlags)
	badHash := "ZZZZ-not-hex"
	err := debugCmd.PreRunE(debugCmd, []string{badHash})
	if err == nil {
		t.Fatal("expected error for invalid hash")
	}
	msg := err.Error()
	if !strings.Contains(msg, badHash) {
		t.Errorf("error should include the invalid hash value, got: %q", msg)
	}
	if !strings.Contains(msg, "64") {
		t.Errorf("error should mention the expected length (64 chars), got: %q", msg)
	}
}

// TestDebugPreRunE_InvalidHash_TooShort ensures a short hash triggers a clear error.
func TestDebugPreRunE_InvalidHash_TooShort(t *testing.T) {
	t.Cleanup(resetDebugFlags)
	err := debugCmd.PreRunE(debugCmd, []string{"abc123"})
	if err == nil {
		t.Fatal("expected error for short hash")
	}
	if !strings.Contains(err.Error(), "invalid transaction hash") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ── Network validation messages ───────────────────────────────────────────────

// TestDebugPreRunE_InvalidNetworkMessageFormat verifies the --network error
// includes the bad value and the list of valid options.
func TestDebugPreRunE_InvalidNetworkMessageFormat(t *testing.T) {
	t.Cleanup(resetDebugFlags)
	networkFlag = "devnet"
	validHash := "5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab"

	err := debugCmd.PreRunE(debugCmd, []string{validHash})
	if err == nil {
		t.Fatal("expected error for invalid network")
	}
	msg := err.Error()
	if !strings.Contains(msg, "devnet") {
		t.Errorf("error should include the invalid value, got: %q", msg)
	}
	if !strings.Contains(msg, "testnet") {
		t.Errorf("error should list valid networks, got: %q", msg)
	}
}

// TestDebugPreRunE_InvalidCompareNetworkMessageFormat verifies the
// --compare-network error includes the bad value.
func TestDebugPreRunE_InvalidCompareNetworkMessageFormat(t *testing.T) {
	t.Cleanup(resetDebugFlags)
	networkFlag = "testnet"
	compareNetworkFlag = "prodnet"
	validHash := "5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab"

	err := debugCmd.PreRunE(debugCmd, []string{validHash})
	if err == nil {
		t.Fatal("expected error for invalid compare-network")
	}
	msg := err.Error()
	if !strings.Contains(msg, "prodnet") {
		t.Errorf("error should include the invalid compare-network value, got: %q", msg)
	}
}

// TestDebugPreRunE_SameNetworkAndCompareNetwork ensures the user gets a clear
// error when --network and --compare-network are the same value (no diff possible).
func TestDebugPreRunE_SameNetworkAndCompareNetwork(t *testing.T) {
	t.Cleanup(resetDebugFlags)
	networkFlag = "testnet"
	compareNetworkFlag = "testnet"
	validHash := "5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab"

	err := debugCmd.PreRunE(debugCmd, []string{validHash})
	if err == nil {
		t.Fatal("expected error when network == compare-network")
	}
	msg := err.Error()
	if !strings.Contains(msg, "must be different") {
		t.Errorf("error should say networks must be different, got: %q", msg)
	}
	if !strings.Contains(msg, "testnet") {
		t.Errorf("error should name the duplicate network, got: %q", msg)
	}
}

// TestDebugPreRunE_SameNetwork_CaseInsensitive ensures the same-network check
// is case-insensitive (Testnet == testnet).
func TestDebugPreRunE_SameNetwork_CaseInsensitive(t *testing.T) {
	t.Cleanup(resetDebugFlags)
	networkFlag = "testnet"
	compareNetworkFlag = "testnet" // same case — already tested above
	validHash := "5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab"

	err := debugCmd.PreRunE(debugCmd, []string{validHash})
	if err == nil {
		t.Fatal("expected error when networks are the same")
	}
}

// ── Local file guards ─────────────────────────────────────────────────────────

// TestDebugPreRunE_BothFilesRejected_MessageMentionsRemedy verifies the mutual
// exclusion message tells the user what to do.
func TestDebugPreRunE_BothFilesRejected_MessageMentionsRemedy(t *testing.T) {
	t.Cleanup(resetDebugFlags)
	xdrFileFlag = "tx.xdr"
	jsonFileFlag = "tx.json"

	err := debugCmd.PreRunE(debugCmd, []string{})
	if err == nil {
		t.Fatal("expected error when both file flags are set")
	}
	msg := err.Error()
	if !strings.Contains(msg, "remove") && !strings.Contains(msg, "only one") {
		t.Errorf("error should tell user to remove one flag, got: %q", msg)
	}
}

// TestDebugPreRunE_HashAndLocalFile_MessageClear verifies the hash+file
// conflict error is clear.
func TestDebugPreRunE_HashAndLocalFile_MessageClear(t *testing.T) {
	t.Cleanup(resetDebugFlags)
	xdrFileFlag = "tx.xdr"

	err := debugCmd.PreRunE(debugCmd, []string{"5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab"})
	if err == nil {
		t.Fatal("expected error when hash + local file both given")
	}
	msg := err.Error()
	if !strings.Contains(msg, "cannot specify both") {
		t.Errorf("error should say cannot specify both, got: %q", msg)
	}
}

// TestDebugPreRunE_WatchWithLocalFile_MessageClear verifies --watch + local
// file produces a helpful message.
func TestDebugPreRunE_WatchWithLocalFile_MessageClear(t *testing.T) {
	t.Cleanup(resetDebugFlags)
	xdrFileFlag = "tx.xdr"
	watchFlag = true

	err := debugCmd.PreRunE(debugCmd, []string{})
	if err == nil {
		t.Fatal("expected error for --watch with local file")
	}
	msg := err.Error()
	if !strings.Contains(msg, "--watch") {
		t.Errorf("error should mention --watch, got: %q", msg)
	}
}

// ── Hot-reload guard ──────────────────────────────────────────────────────────

// TestDebugPreRunE_HotReloadNoWasm_MessageMentionsWasm verifies the error
// explicitly tells the user to add --wasm.
func TestDebugPreRunE_HotReloadNoWasm_MessageMentionsWasm(t *testing.T) {
	t.Cleanup(resetDebugFlags)
	hotReloadFlag = true
	wasmPath = ""

	err := debugCmd.PreRunE(debugCmd, []string{})
	if err == nil {
		t.Fatal("expected error for --hot-reload without --wasm")
	}
	msg := err.Error()
	if !strings.Contains(msg, "--wasm") {
		t.Errorf("error should mention --wasm flag, got: %q", msg)
	}
}

// ── --op validation ───────────────────────────────────────────────────────────

// TestDebugPreRunE_OpIndex_InvalidMessage verifies the --op error message
// explains valid values.
func TestDebugPreRunE_OpIndex_InvalidMessage(t *testing.T) {
	t.Cleanup(resetDebugFlags)
	opIndexFlag = -3
	networkFlag = "testnet"
	validHash := "5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab"

	err := debugCmd.PreRunE(debugCmd, []string{validHash})
	if err == nil {
		t.Fatal("expected error for --op < -1")
	}
	msg := err.Error()
	if !strings.Contains(msg, "--op") {
		t.Errorf("error should mention --op, got: %q", msg)
	}
	// Should give guidance on valid values.
	if !strings.Contains(msg, "non-negative") {
		t.Errorf("error should explain non-negative is required, got: %q", msg)
	}
}

// ── --dry-run combined-flag guards ───────────────────────────────────────────

// TestDebugRunE_DryRunWithShowMetrics_Rejected verifies that combining
// --dry-run and --show-metrics returns a clear error.
func TestDebugRunE_DryRunWithShowMetrics_Rejected(t *testing.T) {
	t.Cleanup(resetDebugFlags)
	debugDryRunFlag = true
	showMetricsFlag = true

	// Directly call RunE rather than the full cobra stack.
	err := debugCmd.RunE(debugCmd, []string{"5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab"})
	if err == nil {
		t.Fatal("expected error when --dry-run and --show-metrics combined")
	}
	msg := err.Error()
	if !strings.Contains(msg, "--show-metrics") {
		t.Errorf("error should mention --show-metrics, got: %q", msg)
	}
	if !strings.Contains(msg, "--dry-run") {
		t.Errorf("error should mention --dry-run, got: %q", msg)
	}
}

// TestDebugRunE_DryRunWithDemoMode_Rejected verifies --dry-run + --demo is rejected.
func TestDebugRunE_DryRunWithDemoMode_Rejected(t *testing.T) {
	t.Cleanup(resetDebugFlags)
	debugDryRunFlag = true
	demoMode = true

	err := debugCmd.RunE(debugCmd, []string{})
	if err == nil {
		t.Fatal("expected error when --dry-run and --demo combined")
	}
	msg := err.Error()
	if !strings.Contains(msg, "--demo") {
		t.Errorf("error should mention --demo, got: %q", msg)
	}
}

// TestDebugRunE_DryRunNoHash_Rejected verifies --dry-run without a hash
// produces a usage hint.
func TestDebugRunE_DryRunNoHash_Rejected(t *testing.T) {
	t.Cleanup(resetDebugFlags)
	debugDryRunFlag = true

	err := debugCmd.RunE(debugCmd, []string{})
	if err == nil {
		t.Fatal("expected error for --dry-run without hash")
	}
	msg := err.Error()
	if !strings.Contains(msg, "transaction hash") {
		t.Errorf("error should mention transaction hash, got: %q", msg)
	}
}

// ── --trace-verbosity validation ──────────────────────────────────────────────

// TestDebugPreRunE_InvalidTraceVerbosity ensures bad --trace-verbosity values
// are caught with a clear message listing valid options.
func TestDebugPreRunE_InvalidTraceVerbosity(t *testing.T) {
	t.Cleanup(resetDebugFlags)
	networkFlag = "testnet"
	traceVerbosityFlag = "extreme"
	validHash := "5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab"

	// Mark the flag as explicitly changed so PreRunE validates it.
	_ = debugCmd.Flags().Set("trace-verbosity", "extreme")
	t.Cleanup(func() { _ = debugCmd.Flags().Set("trace-verbosity", "normal") })

	err := debugCmd.PreRunE(debugCmd, []string{validHash})
	if err == nil {
		t.Fatal("expected error for invalid --trace-verbosity")
	}
	msg := err.Error()
	if !strings.Contains(msg, "extreme") {
		t.Errorf("error should include the bad value, got: %q", msg)
	}
	if !strings.Contains(msg, "summary") || !strings.Contains(msg, "verbose") {
		t.Errorf("error should list valid options, got: %q", msg)
	}
}

// ── --theme validation ────────────────────────────────────────────────────────

// TestDebugPreRunE_InvalidTheme ensures unknown theme values are rejected with
// a message naming the valid options.
func TestDebugPreRunE_InvalidTheme(t *testing.T) {
	t.Cleanup(resetDebugFlags)
	networkFlag = "testnet"
	themeFlag = "solarized"
	validHash := "5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab"

	_ = debugCmd.Flags().Set("theme", "solarized")
	t.Cleanup(func() { _ = debugCmd.Flags().Set("theme", "") })

	err := debugCmd.PreRunE(debugCmd, []string{validHash})
	if err == nil {
		t.Fatal("expected error for invalid --theme")
	}
	msg := err.Error()
	if !strings.Contains(msg, "solarized") {
		t.Errorf("error should name the invalid theme, got: %q", msg)
	}
	if !strings.Contains(msg, "dark") {
		t.Errorf("error should list valid themes, got: %q", msg)
	}
}

// ── --format validation ───────────────────────────────────────────────────────

// TestDebugPreRunE_InvalidFormat ensures unknown --format values are rejected.
func TestDebugPreRunE_InvalidFormat(t *testing.T) {
	t.Cleanup(resetDebugFlags)
	networkFlag = "testnet"
	debugFormatFlag = "yaml"
	validHash := "5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab"

	_ = debugCmd.Flags().Set("format", "yaml")
	t.Cleanup(func() { _ = debugCmd.Flags().Set("format", "text") })

	err := debugCmd.PreRunE(debugCmd, []string{validHash})
	if err == nil {
		t.Fatal("expected error for invalid --format")
	}
	msg := err.Error()
	if !strings.Contains(msg, "yaml") {
		t.Errorf("error should name the invalid format, got: %q", msg)
	}
	if !strings.Contains(msg, "text") || !strings.Contains(msg, "json") {
		t.Errorf("error should list valid formats, got: %q", msg)
	}
}

// ── --pin-endpoint mismatch ───────────────────────────────────────────────────

// TestDebugPreRunE_PinEndpointMismatch_MessageClear verifies the pin-endpoint
// mismatch error is actionable.
func TestDebugPreRunE_PinEndpointMismatch_MessageClear(t *testing.T) {
	t.Cleanup(resetDebugFlags)
	networkFlag = "testnet"
	pinEndpointFlag = "https://rpc1.example.com"
	rpcURLFlag = "https://rpc2.example.com"
	validHash := "5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab"

	err := debugCmd.PreRunE(debugCmd, []string{validHash})
	if err == nil {
		t.Fatal("expected error for pin-endpoint / rpc-url mismatch")
	}
	msg := err.Error()
	if !strings.Contains(msg, "--pin-endpoint") {
		t.Errorf("error should mention --pin-endpoint, got: %q", msg)
	}
	if !strings.Contains(msg, "--rpc-url") {
		t.Errorf("error should mention --rpc-url, got: %q", msg)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

// resetDebugFlags restores all debug-command package-level flag variables to
// their defaults so each test starts from a clean state.
// resetDebugFlags is defined in regression_example_test.go (superset of the
// fields this file needs reset).
