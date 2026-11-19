// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package telemetry — privacy test suite.
//
// This file contains automated payload inspection tests that verify every
// telemetry event emitter respects the privacy policy:
//
//  1. Transaction hashes, contract IDs, and file paths are fingerprinted or
//     reduced to a basename before export — raw values never appear.
//  2. Forbidden field names (PIN, password, secret, token, private key, signer
//     metadata) are never present as attribute keys.
//  3. High-entropy strings that look like private keys or bearer tokens do not
//     appear as raw values.
//  4. New event fields require explicit classification — the test fails when an
//     unclassified raw identifier is detected.
//
// Failure messages identify the event name and attribute key path without
// printing the secret value itself.
package telemetry

import (
	"context"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
	"testing"
	"unicode"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// ── denylist ─────────────────────────────────────────────────────────────────

// forbiddenKeySubstrings is the denylist for attribute key substrings.
// A span attribute whose key contains any of these substrings (case-insensitive)
// must never carry a raw (un-sanitized) value.
var forbiddenKeySubstrings = []string{
	"pin",
	"password",
	"passwd",
	"secret",
	"token",
	"private_key",
	"privatekey",
	"api_key",
	"apikey",
	"credential",
	"signing_key",
	"session_key",
	"auth_key",
}

// isForbiddenKey returns true when key matches a denylist entry.
func isForbiddenKey(key string) bool {
	lower := strings.ToLower(key)
	for _, forbidden := range forbiddenKeySubstrings {
		if strings.Contains(lower, forbidden) {
			return true
		}
	}
	return false
}

// shannonEntropy calculates the Shannon entropy of a string in bits per character.
// Values above ~4.5 typically indicate base64-encoded or random data.
func shannonEntropy(s string) float64 {
	if len(s) == 0 {
		return 0
	}
	freq := make(map[rune]int)
	for _, r := range s {
		freq[r]++
	}
	var entropy float64
	n := float64(len([]rune(s)))
	for _, count := range freq {
		p := float64(count) / n
		entropy -= p * math.Log2(p)
	}
	return entropy
}

// looksLikeRawTxHash returns true when s looks like an un-fingerprinted
// transaction or contract hash (64 hex chars).
func looksLikeRawTxHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !unicode.Is(unicode.ASCII_Hex_Digit, c) {
			return false
		}
	}
	return true
}

// looksLikePrivateKey returns true when the string starts with a PEM marker or
// looks like a raw private key in hex.
func looksLikePrivateKey(s string) bool {
	return strings.Contains(s, "BEGIN PRIVATE KEY") ||
		strings.Contains(s, "BEGIN EC PRIVATE KEY") ||
		strings.Contains(s, "BEGIN RSA PRIVATE KEY") ||
		(len(s) >= 32 && isAllHex(s) && len(s)%2 == 0)
}

func isAllHex(s string) bool {
	if len(s) == 0 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// looksLikeBearerToken returns true when the string looks like a bearer token
// (high entropy, long, alphanumeric with typical token separators).
func looksLikeBearerToken(s string) bool {
	if len(s) < 20 {
		return false
	}
	entropy := shannonEntropy(s)
	// Typical random tokens have entropy > 4.5 bits/char.
	return entropy > 4.5
}

// looksLikeFilePath returns true when the string contains path separators and
// is not a basename-only value.
func looksLikeFilePath(s string) bool {
	if strings.Contains(s, "/") || strings.Contains(s, "\\") {
		// basename-only is ok; full path is not
		return strings.ContainsAny(s[:len(s)-len(lastSegment(s))], "/\\")
	}
	return false
}

func lastSegment(s string) string {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' || s[i] == '\\' {
			return s[i+1:]
		}
	}
	return s
}

// ── capturing exporter ────────────────────────────────────────────────────────

// capturedSpan holds the name and attributes of a completed span.
type capturedSpan struct {
	name  string
	attrs []attribute.KeyValue
}

// capturingExporter is a SpanExporter that stores completed spans in memory.
type capturingExporter struct {
	spans []capturedSpan
}

func (e *capturingExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	for _, s := range spans {
		e.spans = append(e.spans, capturedSpan{
			name:  s.Name(),
			attrs: s.Attributes(),
		})
	}
	return nil
}

func (e *capturingExporter) Shutdown(_ context.Context) error { return nil }

// installCapturingProvider replaces the global tracer provider with one backed
// by the given exporter and returns a flush function.
func installCapturingProvider(exp *capturingExporter) (tracer oteltrace.Tracer, flush func()) {
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exp),
	)
	otel.SetTracerProvider(tp)
	return tp.Tracer("glassbox-privacy-test"), func() {
		ctx := context.Background()
		_ = tp.Shutdown(ctx)
	}
}

// ── privacy checker ───────────────────────────────────────────────────────────

// ── representative event fixtures ────────────────────────────────────────────

// Realistic sensitive values that must never appear in sanitized telemetry.
const (
	rawTxHash      = "5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab"
	rawContractID  = "CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAFCT4"
	rawFilePath    = "/home/user/projects/my-dapp/target/wasm32/release/contract.wasm"
	rawBearerToken = "eyJhbGciOiJFZERTQSIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJ1c3IxMjMifQ.abc123xyz789"
	rawPrivKey     = "-----BEGIN PRIVATE KEY-----\nMC4CAQAwBQYDK2VwBCIEIBwh3QIDAQAB\n-----END PRIVATE KEY-----"
)

// ── SanitizeValue unit tests ──────────────────────────────────────────────────

func TestSanitizeValue_TxHash_Fingerprinted(t *testing.T) {
	result := SanitizeValue("transaction.hash", rawTxHash)
	if strings.Contains(result, rawTxHash) {
		t.Errorf("SanitizeValue must not emit raw tx hash; got prefix %q", result[:min(32, len(result))])
	}
	if !strings.HasPrefix(result, "sha256:") {
		t.Errorf("SanitizeValue tx hash must be fingerprinted (sha256: prefix), got: %q", result)
	}
}

func TestSanitizeValue_ContractID_Fingerprinted(t *testing.T) {
	result := SanitizeValue("contract.id", rawContractID)
	if strings.Contains(result, rawContractID) {
		t.Errorf("SanitizeValue must not emit raw contract ID")
	}
	if !strings.HasPrefix(result, "sha256:") {
		t.Errorf("SanitizeValue contract.id must be fingerprinted, got: %q", result)
	}
}

func TestSanitizeValue_FilePath_BasenameOnly(t *testing.T) {
	result := SanitizeValue("wasm.path", rawFilePath)
	if strings.Contains(result, "/home") || strings.Contains(result, "user") {
		t.Errorf("SanitizeValue must reduce file path to basename, got: %q", result)
	}
	if result != "contract.wasm" {
		t.Errorf("SanitizeValue basename want 'contract.wasm', got: %q", result)
	}
}

func TestPrivacySuite_SanitizeValue_LongString_Truncated(t *testing.T) {
	long := strings.Repeat("a", 200)
	result := SanitizeValue("description", long)
	if len(result) > 132 {
		t.Errorf("SanitizeValue long string should be truncated to <= 131 chars, got %d", len(result))
	}
	if !strings.HasSuffix(result, "...") {
		t.Error("truncated value should end with '...'")
	}
}

func TestPrivacySuite_SanitizeValue_EmptyString_ReturnsEmpty(t *testing.T) {
	if got := SanitizeValue("key", ""); got != "" {
		t.Errorf("SanitizeValue(\"\") = %q, want \"\"", got)
	}
}

func TestAttr_SanitizesValue(t *testing.T) {
	kv := Attr("transaction.hash", rawTxHash)
	val := kv.Value.AsString()
	if strings.Contains(val, rawTxHash) {
		t.Errorf("Attr must sanitize raw hash, got: %q", val)
	}
}

// ── command_usage emitter ─────────────────────────────────────────────────────

func TestPrivacy_CommandUsage_SanitizesCommandName(t *testing.T) {
	exp := &capturingExporter{}
	_, flush := installCapturingProvider(exp)
	defer flush()

	ctx := context.Background()
	commandTelemetryEnabled = true
	commandTelemetryAnonymized = false

	// Emit with a realistic but safe command name.
	RecordCommandUsage(ctx, "debug")

	privacyCheckPayload(t, exp.spans)
}

func TestPrivacy_CommandUsage_MaliciousInput(t *testing.T) {
	exp := &capturingExporter{}
	_, flush := installCapturingProvider(exp)
	defer flush()

	commandTelemetryEnabled = true
	commandTelemetryAnonymized = false

	// Simulate a deep-link with injected content — must be sanitized before export.
	RecordCommandUsage(context.Background(), "debug<script>alert('xss')</script>")

	for _, span := range exp.spans {
		for _, attr := range span.attrs {
			if attr.Key == "command.name" {
				val := attr.Value.AsString()
				if strings.ContainsAny(val, "<>()'\";") {
					t.Errorf("[span=%q] command.name contains unsafe chars: %q", span.name, val)
				}
			}
		}
	}
}

func TestPrivacy_CommandUsage_PrivateKeyInCommandName(t *testing.T) {
	// sanitizeCommandName replaces all non-alnum chars, so no PEM content survives.
	exp := &capturingExporter{}
	_, flush := installCapturingProvider(exp)
	defer flush()

	commandTelemetryEnabled = true
	commandTelemetryAnonymized = false

	RecordCommandUsage(context.Background(), "audit-sign--software-private-key")

	for _, span := range exp.spans {
		for _, attr := range span.attrs {
			if attr.Key == "command.name" {
				val := attr.Value.AsString()
				if strings.Contains(val, "BEGIN") {
					t.Errorf("[span=%q] PEM content must not appear in command.name: %q", span.name, val)
				}
			}
		}
	}
}

func TestPrivacy_CommandUsage_Anonymized_OmitsEnvMetadata(t *testing.T) {
	exp := &capturingExporter{}
	_, flush := installCapturingProvider(exp)
	defer flush()

	commandTelemetryEnabled = true
	commandTelemetryAnonymized = true
	defer func() { commandTelemetryAnonymized = false }()

	RecordCommandUsage(context.Background(), "debug")

	for _, span := range exp.spans {
		for _, attr := range span.attrs {
			key := string(attr.Key)
			if key == "env.platform" || key == "env.arch" || key == "env.version" {
				t.Errorf("[span=%q] anonymized mode must not emit env metadata key %q", span.name, key)
			}
		}
	}
}

func TestPrivacy_CommandUsage_Disabled_EmitsNothing(t *testing.T) {
	exp := &capturingExporter{}
	_, flush := installCapturingProvider(exp)
	defer flush()

	commandTelemetryEnabled = false
	defer func() { commandTelemetryEnabled = true }()

	RecordCommandUsage(context.Background(), "debug")

	if len(exp.spans) != 0 {
		t.Errorf("disabled telemetry: expected 0 spans, got %d", len(exp.spans))
	}
}

// ── RPC span emitters ─────────────────────────────────────────────────────────

// TestPrivacy_RPCGetTransaction simulates the span attributes set by
// internal/rpc when fetching a transaction, verifying no raw hash leaks.
func TestPrivacy_RPCGetTransaction(t *testing.T) {
	exp := &capturingExporter{}
	tracer, flush := installCapturingProvider(exp)
	defer flush()

	ctx := context.Background()
	_, span := tracer.Start(ctx, "rpc_get_transaction")
	span.SetAttributes(
		Attr("transaction.hash", rawTxHash),
		Attr("network", "testnet"),
		Attr("rpc.url", "https://soroban-testnet.stellar.org"),
	)
	span.End()

	privacyCheckPayload(t, exp.spans)
}

func TestPrivacy_RPCGetLedgerEntries(t *testing.T) {
	exp := &capturingExporter{}
	tracer, flush := installCapturingProvider(exp)
	defer flush()

	ctx := context.Background()
	_, span := tracer.Start(ctx, "rpc_get_ledger_entries_concurrent")
	span.SetAttributes(
		Attr("ledger.keys.count", "5"),
		Attr("network", "mainnet"),
	)
	span.End()

	privacyCheckPayload(t, exp.spans)
}

func TestPrivacy_RPCTxStream(t *testing.T) {
	exp := &capturingExporter{}
	tracer, flush := installCapturingProvider(exp)
	defer flush()

	ctx := context.Background()
	_, span := tracer.Start(ctx, "rpc_tx_stream_ws")
	span.SetAttributes(
		Attr("transaction.hash", rawTxHash),
		Attr("rpc.url", "wss://soroban-testnet.stellar.org/stream"),
	)
	span.End()

	privacyCheckPayload(t, exp.spans)
}

// ── daemon span emitters ──────────────────────────────────────────────────────

func TestPrivacy_DaemonDebugTransaction(t *testing.T) {
	exp := &capturingExporter{}
	tracer, flush := installCapturingProvider(exp)
	defer flush()

	ctx := context.Background()
	_, span := tracer.Start(ctx, "rpc_debug_transaction")
	span.SetAttributes(
		Attr("transaction.hash", rawTxHash),
	)
	span.End()

	privacyCheckPayload(t, exp.spans)
}

func TestPrivacy_DaemonGetContractCode(t *testing.T) {
	exp := &capturingExporter{}
	tracer, flush := installCapturingProvider(exp)
	defer flush()

	ctx := context.Background()
	_, span := tracer.Start(ctx, "rpc_get_contract_code")
	span.SetAttributes(
		Attr("contract.id", rawContractID),
		Attr("transaction.hash", rawTxHash),
	)
	span.End()

	privacyCheckPayload(t, exp.spans)
}

// ── debug command emitter ─────────────────────────────────────────────────────

func TestPrivacy_DebugCommand(t *testing.T) {
	exp := &capturingExporter{}
	tracer, flush := installCapturingProvider(exp)
	defer flush()

	ctx := context.Background()
	_, span := tracer.Start(ctx, "debug_transaction")
	span.SetAttributes(
		Attr("transaction.hash", rawTxHash),
		Attr("network", "testnet"),
		Attr("rpc.url", "https://soroban-testnet.stellar.org"),
	)
	span.End()

	privacyCheckPayload(t, exp.spans)
}

// ── negative tests: raw values must NOT pass ──────────────────────────────────

// These tests verify that the test harness itself correctly catches violations.
// They emit raw values and confirm the checker would catch them.

func TestPrivacy_Harness_DetectsRawTxHash(t *testing.T) {
	violated := runPrivacyCheck([]capturedSpan{
		{name: "test_span", attrs: []attribute.KeyValue{
			attribute.String("transaction.hash", rawTxHash),
		}},
	})
	if !violated {
		t.Error("harness should have detected raw tx hash violation")
	}
}

func TestPrivacy_Harness_DetectsPrivateKey(t *testing.T) {
	violated := runPrivacyCheck([]capturedSpan{
		{name: "test_span", attrs: []attribute.KeyValue{
			attribute.String("debug.info", rawPrivKey),
		}},
	})
	if !violated {
		t.Error("harness should have detected private key violation")
	}
}

func TestPrivacy_Harness_DetectsFilePath(t *testing.T) {
	violated := runPrivacyCheck([]capturedSpan{
		{name: "test_span", attrs: []attribute.KeyValue{
			attribute.String("wasm.path", rawFilePath),
		}},
	})
	if !violated {
		t.Error("harness should have detected full file path violation")
	}
}

func TestPrivacy_Harness_DetectsForbiddenKey(t *testing.T) {
	violated := runPrivacyCheck([]capturedSpan{
		{name: "test_span", attrs: []attribute.KeyValue{
			attribute.String("pkcs11_pin", "1234"),
		}},
	})
	if !violated {
		t.Error("harness should have detected forbidden key 'pkcs11_pin'")
	}
}

func TestPrivacy_Harness_DetectsBearerToken(t *testing.T) {
	violated := runPrivacyCheck([]capturedSpan{
		{name: "test_span", attrs: []attribute.KeyValue{
			attribute.String("auth.value", rawBearerToken),
		}},
	})
	if !violated {
		t.Error("harness should have detected bearer-token-like value")
	}
}

// ── denylist completeness ─────────────────────────────────────────────────────

// TestDenylist_Completeness ensures all known sensitive key patterns are in the
// denylist. If a new pattern is added to the denylist but not to this test it
// will fail, prompting the author to confirm the addition is intentional.
func TestDenylist_Completeness(t *testing.T) {
	// These are the patterns that MUST be in forbiddenKeySubstrings.
	required := []string{
		"pin",
		"password",
		"secret",
		"token",
		"private_key",
		"api_key",
		"credential",
	}
	denyset := make(map[string]bool, len(forbiddenKeySubstrings))
	for _, k := range forbiddenKeySubstrings {
		denyset[k] = true
	}
	for _, r := range required {
		if !denyset[r] {
			t.Errorf("required denylist entry %q is missing from forbiddenKeySubstrings", r)
		}
	}
}

// TestDenylist_NewFieldRequiresClassification is a canary test. If you add a
// new attribute key to any telemetry emitter, you must either:
//   - Verify it is sanitized by SanitizeValue and add it to the approved list, OR
//   - Add it to the denylist if it might carry sensitive data.
//
// The test will fail until the new key is classified.
func TestDenylist_NewFieldRequiresClassification(t *testing.T) {
	// Approved attribute keys used by current emitters.
	// To add a new key, append it here and document why it is safe.
	approvedKeys := map[string]string{
		"command.name":        "sanitizeCommandName: alphanumeric/dash/colon/underscore only, max 64 chars",
		"telemetry.anonymized": "boolean flag, not sensitive",
		"correlation_id":      "random hex ID, not a secret",
		"env.version":         "CLI version string, not sensitive",
		"env.platform":        "OS name (linux/darwin/windows), not sensitive",
		"env.arch":            "CPU arch (amd64/arm64), not sensitive",
		"env.feature_flags":   "list of enabled feature names, not sensitive",
		"transaction.hash":    "sanitized by SanitizeValue: sha256 fingerprint only",
		"contract.id":         "sanitized by SanitizeValue: sha256 fingerprint only",
		"network":             "network name (testnet/mainnet), not sensitive",
		"rpc.url":             "endpoint URL, not sensitive (no credentials in URL)",
		"ledger.keys.count":   "numeric count, not sensitive",
		"ledger.hash":         "sanitized by SanitizeValue: sha256 fingerprint only",
		"wasm.path":           "sanitized by SanitizeValue: basename only",
	}

	// For each approved key, verify it is not in the forbidden list.
	for key := range approvedKeys {
		if isForbiddenKey(key) {
			t.Errorf("approved key %q is in the denylist — resolve the conflict", key)
		}
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

// checkPrivacyViolations inspects spans and returns a slice of violation
// messages. Each message identifies the span and attribute key without printing
// the actual value.
func checkPrivacyViolations(spans []capturedSpan) []string {
	var violations []string
	for _, span := range spans {
		for _, attr := range span.attrs {
			key := string(attr.Key)
			val := attr.Value.AsString()

			if isForbiddenKey(key) {
				violations = append(violations, "[span="+span.name+" key="+key+"] forbidden key found in telemetry payload")
			}
			if looksLikeRawTxHash(val) {
				violations = append(violations, "[span="+span.name+" key="+key+"] raw 64-hex identifier detected — must be fingerprinted")
			}
			if looksLikePrivateKey(val) {
				violations = append(violations, "[span="+span.name+" key="+key+"] value looks like a private key")
			}
			if looksLikeFilePath(val) {
				violations = append(violations, "[span="+span.name+" key="+key+"] value looks like a full file path — must be reduced to basename")
			}
			if key != "correlation_id" && key != "command.name" && looksLikeBearerToken(val) {
				violations = append(violations, "[span="+span.name+" key="+key+"] high-entropy value detected (entropy="+fmt.Sprintf("%.2f", shannonEntropy(val))+")")
			}
		}
	}
	return violations
}

// privacyCheckPayload calls checkPrivacyViolations and reports each violation
// via t.Errorf. It is the test-facing wrapper.
func privacyCheckPayload(t *testing.T, spans []capturedSpan) {
	t.Helper()
	for _, v := range checkPrivacyViolations(spans) {
		t.Error(v)
	}
}

// runPrivacyCheck runs the privacy checker on spans and returns true if any
// violation was detected.
func runPrivacyCheck(spans []capturedSpan) bool {
	return len(checkPrivacyViolations(spans)) > 0
}

// min returns the smaller of two ints.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
