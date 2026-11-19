// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package telemetry

import (
	"os"
	"strings"
	"testing"
	"time"
)

// ── ConsentLevel.String ───────────────────────────────────────────────────────

func TestConsentLevel_String(t *testing.T) {
	cases := []struct {
		lvl  ConsentLevel
		want string
	}{
		{ConsentLevelDisabled, "disabled"},
		{ConsentLevelAnonymous, "anonymous"},
		{ConsentLevelFull, "full"},
		{ConsentLevel(99), "disabled"},
	}
	for _, c := range cases {
		if got := c.lvl.String(); got != c.want {
			t.Errorf("ConsentLevel(%d).String() = %q, want %q", c.lvl, got, c.want)
		}
	}
}

// ── ResolveConsentLevel ───────────────────────────────────────────────────────

func TestResolveConsentLevel_DefaultDisabled(t *testing.T) {
	t.Setenv("GLASSBOX_TELEMETRY", "")
	t.Setenv("GLASSBOX_TELEMETRY_LEVEL", "")

	lvl := ResolveConsentLevel()
	if lvl != ConsentLevelDisabled {
		t.Errorf("default consent level = %v, want disabled", lvl)
	}
}

func TestResolveConsentLevel_EnvLevelFull(t *testing.T) {
	t.Setenv("GLASSBOX_TELEMETRY_LEVEL", "full")
	t.Setenv("GLASSBOX_TELEMETRY", "")

	lvl := ResolveConsentLevel()
	if lvl != ConsentLevelFull {
		t.Errorf("expected full, got %v", lvl)
	}
}

func TestResolveConsentLevel_EnvLevelAnonymous(t *testing.T) {
	t.Setenv("GLASSBOX_TELEMETRY_LEVEL", "anonymous")
	t.Setenv("GLASSBOX_TELEMETRY", "")

	lvl := ResolveConsentLevel()
	if lvl != ConsentLevelAnonymous {
		t.Errorf("expected anonymous, got %v", lvl)
	}
}

func TestResolveConsentLevel_EnvLevelDisabled(t *testing.T) {
	t.Setenv("GLASSBOX_TELEMETRY_LEVEL", "disabled")
	t.Setenv("GLASSBOX_TELEMETRY", "true") // Should be overridden by level env.

	lvl := ResolveConsentLevel()
	if lvl != ConsentLevelDisabled {
		t.Errorf("expected disabled, got %v", lvl)
	}
}

func TestResolveConsentLevel_BoolEnvTrue(t *testing.T) {
	t.Setenv("GLASSBOX_TELEMETRY_LEVEL", "")
	t.Setenv("GLASSBOX_TELEMETRY", "true")

	lvl := ResolveConsentLevel()
	if lvl != ConsentLevelFull {
		t.Errorf("GLASSBOX_TELEMETRY=true should yield full, got %v", lvl)
	}
}

func TestResolveConsentLevel_BoolEnvFalse(t *testing.T) {
	t.Setenv("GLASSBOX_TELEMETRY_LEVEL", "")
	t.Setenv("GLASSBOX_TELEMETRY", "false")

	lvl := ResolveConsentLevel()
	if lvl != ConsentLevelDisabled {
		t.Errorf("GLASSBOX_TELEMETRY=false should yield disabled, got %v", lvl)
	}
}

// ── IsCategoryAllowed ─────────────────────────────────────────────────────────

func TestIsCategoryAllowed_Disabled_AllFalse(t *testing.T) {
	cats := []EventCategory{CategoryUsage, CategoryPerformance, CategoryDiagnostics, CategoryCrash}
	for _, cat := range cats {
		if IsCategoryAllowed(cat, ConsentLevelDisabled) {
			t.Errorf("category %q should not be allowed when disabled", cat)
		}
	}
}

func TestIsCategoryAllowed_Anonymous_OnlyAllowed(t *testing.T) {
	allowed := []EventCategory{CategoryUsage, CategoryPerformance}
	denied := []EventCategory{CategoryDiagnostics, CategoryCrash}

	for _, cat := range allowed {
		if !IsCategoryAllowed(cat, ConsentLevelAnonymous) {
			t.Errorf("category %q should be allowed at anonymous level", cat)
		}
	}
	for _, cat := range denied {
		if IsCategoryAllowed(cat, ConsentLevelAnonymous) {
			t.Errorf("category %q should be denied at anonymous level", cat)
		}
	}
}

func TestIsCategoryAllowed_Full_AllAllowed(t *testing.T) {
	cats := []EventCategory{CategoryUsage, CategoryPerformance, CategoryDiagnostics, CategoryCrash}
	for _, cat := range cats {
		if !IsCategoryAllowed(cat, ConsentLevelFull) {
			t.Errorf("category %q should be allowed at full level", cat)
		}
	}
}

// ── RetryDelay ────────────────────────────────────────────────────────────────

func TestRetryDelay_Exponential(t *testing.T) {
	d1 := RetryDelay(1)
	d2 := RetryDelay(2)
	d3 := RetryDelay(3)

	if d1 != RetryBackoffBase {
		t.Errorf("attempt 1 delay = %v, want %v", d1, RetryBackoffBase)
	}
	if d2 != 2*RetryBackoffBase {
		t.Errorf("attempt 2 delay = %v, want %v", d2, 2*RetryBackoffBase)
	}
	if d3 != 4*RetryBackoffBase {
		t.Errorf("attempt 3 delay = %v, want %v", d3, 4*RetryBackoffBase)
	}
}

func TestRetryDelay_Cap(t *testing.T) {
	// After enough attempts the delay must not exceed the cap.
	for i := 1; i <= 20; i++ {
		d := RetryDelay(i)
		if d > RetryBackoffCap {
			t.Errorf("attempt %d delay %v exceeds cap %v", i, d, RetryBackoffCap)
		}
	}
}

func TestRetryDelay_ZeroAttempt(t *testing.T) {
	// attempt 0 or negative should behave like attempt 1.
	if RetryDelay(0) != RetryDelay(1) {
		t.Error("attempt 0 should equal attempt 1 delay")
	}
}

// ── RedactPayload ─────────────────────────────────────────────────────────────

func TestRedactPayload_TransactionHash(t *testing.T) {
	attrs := map[string]string{
		"tx_hash":    "abc123def456",
		"command":    "debug",
		"version":    "1.2.3",
	}
	out := RedactPayload(attrs)

	if out["tx_hash"] != redactedPlaceholder {
		t.Errorf("tx_hash should be redacted, got %q", out["tx_hash"])
	}
	if out["command"] != "debug" {
		t.Errorf("command should not be redacted, got %q", out["command"])
	}
}

func TestRedactPayload_ContractID(t *testing.T) {
	attrs := map[string]string{"contract_id": "CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC"}
	out := RedactPayload(attrs)
	if out["contract_id"] != redactedPlaceholder {
		t.Errorf("contract_id should be redacted, got %q", out["contract_id"])
	}
}

func TestRedactPayload_FilePath(t *testing.T) {
	attrs := map[string]string{"file_path": "/home/user/.Glassbox/config.json"}
	out := RedactPayload(attrs)
	if out["file_path"] != redactedPlaceholder {
		t.Errorf("file_path should be redacted, got %q", out["file_path"])
	}
}

func TestRedactPayload_Credentials(t *testing.T) {
	sensitive := map[string]string{
		"api_key":    "sk-secret-1234",
		"password":   "hunter2",
		"token":      "ghp_XXXX",
		"secret_key": "aws-secret",
	}
	out := RedactPayload(sensitive)
	for k, v := range out {
		if v != redactedPlaceholder {
			t.Errorf("key %q with sensitive value should be redacted, got %q", k, v)
		}
	}
}

func TestRedactPayload_SourceCode(t *testing.T) {
	attrs := map[string]string{
		"wasm_bytes": "AAABBBCCC",
		"source":     "fn main() {}",
	}
	out := RedactPayload(attrs)
	for k, v := range out {
		if v != redactedPlaceholder {
			t.Errorf("key %q should be redacted, got %q", k, v)
		}
	}
}

func TestRedactPayload_CommandArgs(t *testing.T) {
	attrs := map[string]string{"arg_value": "some-user-input"}
	out := RedactPayload(attrs)
	if out["arg_value"] != redactedPlaceholder {
		t.Errorf("arg_value should be redacted, got %q", out["arg_value"])
	}
}

func TestRedactPayload_NilInput(t *testing.T) {
	if out := RedactPayload(nil); out != nil {
		t.Errorf("expected nil for nil input, got %v", out)
	}
}

func TestRedactPayload_SafeFields_NotRedacted(t *testing.T) {
	attrs := map[string]string{
		"command":   "debug",
		"platform":  "linux",
		"arch":      "amd64",
		"version":   "1.0.0",
		"event":     "command_usage",
	}
	out := RedactPayload(attrs)
	for k, want := range attrs {
		if out[k] != want {
			t.Errorf("safe field %q should not be redacted: got %q, want %q", k, out[k], want)
		}
	}
}

// ── EnqueueCategorisedEvent ───────────────────────────────────────────────────

func TestEnqueueCategorisedEvent_Disabled_NoWrite(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("GLASSBOX_TELEMETRY", "false")
	t.Setenv("GLASSBOX_TELEMETRY_LEVEL", "")

	err := EnqueueCategorisedEvent("cmd_usage", CategoryUsage, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Queue file must not exist when telemetry is disabled.
	entries, err := ReadQueue()
	if err != nil {
		t.Fatalf("ReadQueue error: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 queued events when disabled, got %d", len(entries))
	}
}

func TestEnqueueCategorisedEvent_Full_Enqueues(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("GLASSBOX_TELEMETRY", "true")
	t.Setenv("GLASSBOX_TELEMETRY_LEVEL", "full")

	err := EnqueueCategorisedEvent("cmd_debug", CategoryUsage, map[string]string{
		"command": "debug",
		"version": "1.0.0",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entries, err := ReadQueue()
	if err != nil {
		t.Fatalf("ReadQueue error: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 queued event, got %d", len(entries))
	}
	if entries[0].Event != "cmd_debug" {
		t.Errorf("unexpected event name: %q", entries[0].Event)
	}
}

func TestEnqueueCategorisedEvent_Anonymous_DiagnosticsBlocked(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("GLASSBOX_TELEMETRY_LEVEL", "anonymous")
	t.Setenv("GLASSBOX_TELEMETRY", "")

	err := EnqueueCategorisedEvent("diag_event", CategoryDiagnostics, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entries, _ := ReadQueue()
	if len(entries) != 0 {
		t.Errorf("diagnostics category should be blocked at anonymous level, got %d events", len(entries))
	}
}

func TestEnqueueCategorisedEvent_Anonymous_UsageAllowed(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("GLASSBOX_TELEMETRY_LEVEL", "anonymous")
	t.Setenv("GLASSBOX_TELEMETRY", "")

	err := EnqueueCategorisedEvent("cmd_version", CategoryUsage, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entries, _ := ReadQueue()
	if len(entries) != 1 {
		t.Errorf("usage category should be allowed at anonymous level, got %d events", len(entries))
	}
}

// ── Payload redaction via EnqueueCategorisedEvent ─────────────────────────────

func TestEnqueueCategorisedEvent_SensitiveAttrs_Redacted(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("GLASSBOX_TELEMETRY_LEVEL", "full")
	t.Setenv("GLASSBOX_TELEMETRY", "")

	err := EnqueueCategorisedEvent("cmd_debug", CategoryUsage, map[string]string{
		"tx_hash":     "abc123sensitive",
		"contract_id": "CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC",
		"command":     "debug",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entries, err := ReadQueue()
	if err != nil {
		t.Fatalf("ReadQueue error: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one entry")
	}
	attrs := entries[0].Attrs
	// Sensitive fields must be redacted.
	if attrs["tx_hash"] != redactedPlaceholder {
		t.Errorf("tx_hash not redacted in queue: %q", attrs["tx_hash"])
	}
	if attrs["contract_id"] != redactedPlaceholder {
		t.Errorf("contract_id not redacted in queue: %q", attrs["contract_id"])
	}
	// Safe field must survive.
	if attrs["command"] != "debug" {
		t.Errorf("command should not be redacted: %q", attrs["command"])
	}
}

// ── Queue bounds enforcement ──────────────────────────────────────────────────

func TestQueueBoundsConstants(t *testing.T) {
	// Sanity-check that the queue bounds are within acceptable operational ranges.
	if MaxQueueSize < 100 {
		t.Errorf("MaxQueueSize %d is suspiciously small", MaxQueueSize)
	}
	if MaxQueueBytes < 64*1024 {
		t.Errorf("MaxQueueBytes %d is suspiciously small", MaxQueueBytes)
	}
	if MaxEventAge < 12*time.Hour {
		t.Errorf("MaxEventAge %v is suspiciously small", MaxEventAge)
	}
}

// ── DeleteQueue ───────────────────────────────────────────────────────────────

func TestDeleteQueue_RemovesQueueFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("GLASSBOX_TELEMETRY_LEVEL", "full")
	t.Setenv("GLASSBOX_TELEMETRY", "true")

	// Enqueue something so the file exists.
	_ = EnqueueEvent("test", nil)

	if err := DeleteQueue(); err != nil {
		t.Fatalf("DeleteQueue error: %v", err)
	}

	path := QueueFilePath()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("queue file should be gone after DeleteQueue")
	}
}

func TestDeleteQueue_Idempotent(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// Call twice without error.
	if err := DeleteQueue(); err != nil {
		t.Fatalf("first DeleteQueue error: %v", err)
	}
	if err := DeleteQueue(); err != nil {
		t.Fatalf("second DeleteQueue error: %v", err)
	}
}

// ── CI and air-gapped environment guidance ────────────────────────────────────

// TestCIEnvironmentGuidance verifies the documented environment-variable
// approach for disabling telemetry in CI and air-gapped environments.
func TestCIEnvironmentGuidance(t *testing.T) {
	// In CI or air-gapped environments, operators should set:
	//   GLASSBOX_TELEMETRY=false
	// This test validates that the documented approach works.
	t.Setenv("GLASSBOX_TELEMETRY", "false")
	t.Setenv("GLASSBOX_TELEMETRY_LEVEL", "")

	if IsTelemetryEnabled() {
		t.Error("GLASSBOX_TELEMETRY=false should disable telemetry in CI/air-gapped environments")
	}
	if ResolveConsentLevel() != ConsentLevelDisabled {
		t.Error("consent level should be disabled in CI/air-gapped environments")
	}
}

// TestAirGappedNoNetworkCalls verifies that a disabled consent level causes
// EnqueueCategorisedEvent to skip the queue entirely (no file I/O, no network).
func TestAirGappedNoNetworkCalls(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("GLASSBOX_TELEMETRY", "false")
	t.Setenv("GLASSBOX_TELEMETRY_LEVEL", "")

	// Fire several events — none should be written.
	for _, cat := range []EventCategory{CategoryUsage, CategoryPerformance, CategoryDiagnostics, CategoryCrash} {
		if err := EnqueueCategorisedEvent("air_gap_test", cat, nil); err != nil {
			t.Fatalf("unexpected error for category %q: %v", cat, err)
		}
	}

	queuePath := QueueFilePath()
	if _, err := os.Stat(queuePath); !os.IsNotExist(err) {
		t.Error("queue file should not be created when telemetry is disabled")
	}
}

// TestPayloadNeverContainsTransactionContents is a final integration-style
// check that the full EnqueueCategorisedEvent→ReadQueue path never stores raw
// transaction hashes, contract IDs, or file paths in the queue file.
func TestPayloadNeverContainsTransactionContents(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("GLASSBOX_TELEMETRY_LEVEL", "full")
	t.Setenv("GLASSBOX_TELEMETRY", "")

	sensitiveValues := []string{
		"5c0a1bc2def3456789abcdef01234567",
		"CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC",
		"/home/user/.ssh/id_rsa",
	}

	_ = EnqueueCategorisedEvent("cmd_debug", CategoryUsage, map[string]string{
		"tx_hash":      sensitiveValues[0],
		"contract_id":  sensitiveValues[1],
		"private_path": sensitiveValues[2],
		"command":      "debug",
	})

	entries, err := ReadQueue()
	if err != nil {
		t.Fatalf("ReadQueue error: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no events written")
	}

	// Encode all attrs to a single string for easy substring search.
	var sb strings.Builder
	for k, v := range entries[0].Attrs {
		sb.WriteString(k)
		sb.WriteString("=")
		sb.WriteString(v)
		sb.WriteString(";")
	}
	payload := sb.String()

	for _, sensitive := range sensitiveValues {
		if strings.Contains(payload, sensitive) {
			t.Errorf("queue payload contains sensitive value %q: %s", sensitive, payload)
		}
	}
}
