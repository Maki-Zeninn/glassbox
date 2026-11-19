// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package telemetry

import (
	"context"
	"strings"
	"testing"
)

func TestInit(t *testing.T) {
	ctx := context.Background()

	// Test with tracing disabled
	cleanup, err := Init(ctx, Config{
		Enabled: false,
	})
	if err != nil {
		t.Fatalf("Failed to initialize telemetry with disabled config: %v", err)
	}
	cleanup()

	// Graceful degradation: Init must never fail even when collector is unreachable
	cleanup, err = Init(ctx, Config{
		Enabled:     true,
		ExporterURL: "http://localhost:4318",
		ServiceName: "test-service",
	})
	if err != nil {
		t.Fatalf("Init must not fail when collector is down (graceful degradation): %v", err)
	}
	cleanup()

	// Tracer is always available (no-op if collector was unreachable)
	tracer := GetTracer()
	if tracer == nil {
		t.Fatal("Tracer should not be nil after initialization")
	}
	_, span := tracer.Start(ctx, "test-span")
	span.End()
}

func TestGetTracer(t *testing.T) {
	// Should not panic even if not initialized
	tracer := GetTracer()
	if tracer == nil {
		t.Fatal("GetTracer should never return nil")
	}

	// Should be able to create spans (no-op if not initialized)
	ctx := context.Background()
	_, span := tracer.Start(ctx, "test-span")
	span.End()
}

func TestSanitizeValueAndAttr(t *testing.T) {
	// Hash-like key should be fingerprinted, not include raw value
	raw := "5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab"
	sv := SanitizeValue("transaction.hash", raw)
	if !strings.HasPrefix(sv, "sha256:") {
		t.Fatalf("expected fingerprint prefix, got: %s", sv)
	}
	if strings.Contains(sv, raw[:8]) {
		t.Fatalf("sanitized value must not contain raw prefix")
	}

	kv := Attr("transaction.hash", raw)
	if !strings.HasPrefix(kv.Value.AsString(), "sha256:") {
		t.Fatalf("Attr did not sanitize value: %v", kv)
	}
}

// TestInit_UnreachableCollector proves graceful degradation: with tracing enabled
// and an unreachable OTLP endpoint, Init succeeds and core paths (GetTracer, spans)
// work without blocking or error. Run with: go test ./internal/telemetry/... -v -run TestInit_UnreachableCollector
func TestInit_UnreachableCollector(t *testing.T) {
	ctx := context.Background()
	// Use a port that nothing listens on so the collector is "down"
	cleanup, err := Init(ctx, Config{
		Enabled:     true,
		ExporterURL: "http://127.0.0.1:37999",
		ServiceName: "test-service",
	})
	if err != nil {
		t.Fatalf("graceful degradation: Init must not fail when collector is down, got: %v", err)
	}
	defer cleanup()

	tracer := GetTracer()
	if tracer == nil {
		t.Fatal("GetTracer must never return nil")
	}
	_, span := tracer.Start(ctx, "telemetry-test-span")
	span.End()
	// If we get here without blocking or panic, telemetry fails silently as intended
}
func TestEnvMetadata(t *testing.T) {
	ctx := context.Background()

	// Initialize with anonymization disabled to capture env metadata
	cleanup, err := Init(ctx, Config{
		Enabled:     true,
		ExporterURL: "http://localhost:4318",
		ServiceName: "test-service",
		Anonymized:  false,
	})
	if err != nil {
		t.Fatalf("Failed to initialize: %v", err)
	}
	defer cleanup()

	// Verify env metadata was captured
	meta := GetEnvMetadata()
	if meta.Version == "" {
		t.Error("Expected version to be set")
	}
	if meta.Platform == "" {
		t.Error("Expected platform to be set")
	}
	if meta.Arch == "" {
		t.Error("Expected arch to be set")
	}
	// Feature flags may be empty but should not panic
	_ = meta.FeatureFlags
}

func TestEnvMetadata_Anonymized(t *testing.T) {
	ctx := context.Background()

	// Initialize with anonymization enabled
	cleanup, err := Init(ctx, Config{
		Enabled:     true,
		ExporterURL: "http://localhost:4318",
		ServiceName: "test-service",
		Anonymized:  true,
	})
	if err != nil {
		t.Fatalf("Failed to initialize: %v", err)
	}
	defer cleanup()

	// When anonymized, sensitive env data should not be exposed in traces
	meta := GetEnvMetadata()
	if !meta.Anonymized {
		t.Error("Expected anonymized flag to be true")
	}
}

func TestRecordCommandUsage_WithMetadata(t *testing.T) {
	ctx := context.Background()

	// Initialize with metadata collection
	cleanup, err := Init(ctx, Config{
		Enabled:     true,
		ExporterURL: "http://localhost:4318",
		ServiceName: "test-service",
		Anonymized:  false,
	})
	if err != nil {
		t.Fatalf("Failed to initialize: %v", err)
	}
	defer cleanup()

	// Record a command usage event - should not panic
	RecordCommandUsage(ctx, "test-command")

	// If we get here without panic, the metadata was handled correctly
}

func TestGetVersion(t *testing.T) {
	// Test that getVersion returns non-empty string
	// In dev mode it returns "dev", in production it would be the actual version
	version := getVersion()
	if version == "" {
		t.Error("Expected non-empty version")
	}
}

func TestGetFeatureFlags(t *testing.T) {
	// Test feature flag detection
	// Note: This test may behave differently based on environment
	flags := getFeatureFlags()
	// Should return a slice (may be empty)
	if flags == nil {
		t.Error("Expected non-nil feature flags slice")
	}
}

// ── sanitizeCommandName ───────────────────────────────────────────────────────

func TestSanitizeCommandName_SafeName_Preserved(t *testing.T) {
	for _, name := range []string{"debug", "audit:sign", "protocol:diagnose", "session_save"} {
		got := sanitizeCommandName(name)
		if got != name {
			t.Errorf("sanitizeCommandName(%q) = %q, want unchanged", name, got)
		}
	}
}

func TestSanitizeCommandName_UnsafeChars_Replaced(t *testing.T) {
	got := sanitizeCommandName("debug<script>alert(1)</script>")
	if strings.Contains(got, "<") || strings.Contains(got, ">") || strings.Contains(got, "(") {
		t.Errorf("sanitizeCommandName should replace unsafe chars, got: %q", got)
	}
}

func TestSanitizeCommandName_TooLong_Truncated(t *testing.T) {
	long := strings.Repeat("a", 200)
	got := sanitizeCommandName(long)
	if len(got) > 64 {
		t.Errorf("sanitizeCommandName should truncate at 64 chars, got len=%d", len(got))
	}
}

func TestSanitizeCommandName_Empty_ReturnsEmpty(t *testing.T) {
	if got := sanitizeCommandName(""); got != "" {
		t.Errorf("sanitizeCommandName(\"\") = %q, want \"\"", got)
	}
}

func TestSanitizeCommandName_NullBytes_Replaced(t *testing.T) {
	got := sanitizeCommandName("cmd\x00injected")
	if strings.ContainsRune(got, 0) {
		t.Error("sanitizeCommandName should not allow null bytes in output")
	}
}

// ── SanitizeValue — path and truncation coverage ──────────────────────────────

func TestSanitizeValue_PathValue_ReducedToBasename(t *testing.T) {
	got := SanitizeValue("output", "/home/user/projects/myapp/trace.json")
	if strings.Contains(got, "/home") {
		t.Errorf("SanitizeValue should reduce path to basename, got: %q", got)
	}
	if got != "trace.json" {
		t.Errorf("expected basename 'trace.json', got: %q", got)
	}
}

func TestSanitizeValue_LongString_Truncated(t *testing.T) {
	long := strings.Repeat("x", 200)
	got := SanitizeValue("description", long)
	if len(got) > 132 { // 128 + "..." = 131
		t.Errorf("SanitizeValue should truncate long strings, got len=%d", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("truncated value should end with '...', got: %q", got)
	}
}

func TestSanitizeValue_EmptyString_ReturnsEmpty(t *testing.T) {
	if got := SanitizeValue("key", ""); got != "" {
		t.Errorf("SanitizeValue(\"\") = %q, want \"\"", got)
	}
}

func TestSanitizeValue_HashKey_FingerprintNotFullHash(t *testing.T) {
	raw := "5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab"
	got := SanitizeValue("transaction.hash", raw)
	// Must not contain the raw value.
	if strings.Contains(got, raw) {
		t.Errorf("SanitizeValue should not emit the raw hash, got: %q", got)
	}
	// Must start with fingerprint prefix.
	if !strings.HasPrefix(got, "sha256:") {
		t.Errorf("fingerprint must start with 'sha256:', got: %q", got)
	}
	// Fingerprint length is exactly 32 chars (7 prefix + 25 hex).
	if len(got) != 32 {
		t.Errorf("fingerprint length = %d, want 32", len(got))
	}
}

func TestCorrelationID(t *testing.T) {
	ctx := context.Background()

	// 1. Initially empty
	if id := CorrelationIDFromContext(ctx); id != "" {
		t.Fatalf("expected empty correlation ID from background context, got %q", id)
	}

	// 2. EnsureCorrelationID generates a new ID
	ctx1, id1 := EnsureCorrelationID(ctx)
	if id1 == "" {
		t.Fatal("EnsureCorrelationID generated empty ID")
	}
	if CorrelationIDFromContext(ctx1) != id1 {
		t.Fatalf("CorrelationIDFromContext = %q, want %q", CorrelationIDFromContext(ctx1), id1)
	}

	// 3. EnsureCorrelationID is idempotent on existing context
	ctx1Again, id1Again := EnsureCorrelationID(ctx1)
	if id1Again != id1 {
		t.Fatalf("EnsureCorrelationID changed existing ID from %q to %q", id1, id1Again)
	}
	_ = ctx1Again

	// 4. Concurrent contexts receive distinct IDs
	ctx2, id2 := EnsureCorrelationID(context.Background())
	if id1 == id2 {
		t.Fatalf("separate EnsureCorrelationID calls produced non-unique IDs: %q", id1)
	}
	if CorrelationIDFromContext(ctx2) != id2 {
		t.Fatalf("CorrelationIDFromContext for ctx2 = %q, want %q", CorrelationIDFromContext(ctx2), id2)
	}
}
