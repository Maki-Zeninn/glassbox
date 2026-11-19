// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package protocolreg

import (
	"strings"
	"testing"
)

// validTraceparent is a well-formed W3C traceparent used across trace-context tests.
const validTraceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
const validTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
const validSpanID = "00f067aa0ba902b7"

// ── traceparent parameter ─────────────────────────────────────────────────────

func TestParseDebugURI_Traceparent_Valid(t *testing.T) {
	uri := baseURI + "&traceparent=" + validTraceparent
	parsed, err := ParseDebugURI(uri)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.Traceparent != validTraceparent {
		t.Errorf("expected Traceparent=%q, got %q", validTraceparent, parsed.Traceparent)
	}
	if parsed.TraceID != validTraceID {
		t.Errorf("expected TraceID=%q extracted from traceparent, got %q", validTraceID, parsed.TraceID)
	}
	if parsed.SpanID != validSpanID {
		t.Errorf("expected SpanID=%q extracted from traceparent, got %q", validSpanID, parsed.SpanID)
	}
}

func TestParseDebugURI_Traceparent_Absent_FieldsEmpty(t *testing.T) {
	parsed, err := ParseDebugURI(baseURI)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.Traceparent != "" {
		t.Errorf("Traceparent should be empty when not supplied, got %q", parsed.Traceparent)
	}
	if parsed.TraceID != "" {
		t.Errorf("TraceID should be empty when not supplied, got %q", parsed.TraceID)
	}
	if parsed.SpanID != "" {
		t.Errorf("SpanID should be empty when not supplied, got %q", parsed.SpanID)
	}
}

func TestParseDebugURI_Traceparent_InvalidFormat_Rejected(t *testing.T) {
	bad := []struct {
		val string
		err string
	}{
		{"not-a-traceparent", "W3C format"},
		{"01-" + validTraceID + "-" + validSpanID + "-01", "unsupported traceparent version"},
		{"00-" + strings.Repeat("0", 32) + "-" + validSpanID + "-01", "trace-id must not be all zeros"},
		{"00-" + validTraceID + "-" + strings.Repeat("0", 16) + "-01", "parent-id must not be all zeros"},
		{"00-" + validTraceID + "-" + validSpanID + "-01" + "\x00", "null bytes"},
		{"ZZ-" + validTraceID + "-" + validSpanID + "-01", "W3C format"},
	}
	for _, tc := range bad {
		uri := baseURI + "&traceparent=" + tc.val
		_, err := ParseDebugURI(uri)
		if err == nil {
			t.Errorf("traceparent=%q: expected error, got nil", tc.val)
			continue
		}
		if !strings.Contains(err.Error(), tc.err) {
			t.Errorf("traceparent=%q: error should mention %q, got: %v", tc.val, tc.err, err)
		}
	}
}

func TestParseDebugURI_Traceparent_CaseNormalized(t *testing.T) {
	// Uppercase traceparent should be normalised to lowercase.
	upper := "00-4BF92F3577B34DA6A3CE929D0E0E4736-00F067AA0BA902B7-01"
	uri := baseURI + "&traceparent=" + upper
	parsed, err := ParseDebugURI(uri)
	if err != nil {
		t.Fatalf("uppercase traceparent should be accepted: %v", err)
	}
	if parsed.Traceparent != strings.ToLower(upper) {
		t.Errorf("Traceparent should be lowercased, got %q", parsed.Traceparent)
	}
}

// ── tracestate parameter ──────────────────────────────────────────────────────

func TestParseDebugURI_Tracestate_Valid(t *testing.T) {
	ts := "vendorA=value1,vendorB=value2"
	uri := baseURI + "&traceparent=" + validTraceparent + "&tracestate=" + ts
	parsed, err := ParseDebugURI(uri)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.Tracestate != ts {
		t.Errorf("expected Tracestate=%q, got %q", ts, parsed.Tracestate)
	}
}

func TestParseDebugURI_Tracestate_TooLong_Rejected(t *testing.T) {
	ts := strings.Repeat("a", maxTracestateLen+1)
	uri := baseURI + "&tracestate=" + ts
	_, err := ParseDebugURI(uri)
	if err == nil {
		t.Fatal("expected error for tracestate exceeding max length")
	}
	if !strings.Contains(err.Error(), "too long") {
		t.Errorf("error should mention 'too long', got: %v", err)
	}
}

func TestParseDebugURI_Tracestate_NullByte_Rejected(t *testing.T) {
	uri := baseURI + "&tracestate=vendor=val\x00ue"
	_, err := ParseDebugURI(uri)
	if err == nil {
		t.Fatal("expected error for null byte in tracestate")
	}
	if !strings.Contains(err.Error(), "null bytes") {
		t.Errorf("error should mention 'null bytes', got: %v", err)
	}
}

// ── trace-id standalone parameter ────────────────────────────────────────────

func TestParseDebugURI_TraceID_Standalone_Valid(t *testing.T) {
	uri := baseURI + "&trace-id=" + validTraceID
	parsed, err := ParseDebugURI(uri)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.TraceID != validTraceID {
		t.Errorf("expected TraceID=%q, got %q", validTraceID, parsed.TraceID)
	}
	// Traceparent should remain empty when only trace-id is supplied.
	if parsed.Traceparent != "" {
		t.Errorf("Traceparent should be empty when only trace-id is supplied, got %q", parsed.Traceparent)
	}
}

func TestParseDebugURI_TraceID_Standalone_Invalid(t *testing.T) {
	bad := []struct {
		val string
		err string
	}{
		{"tooshort", "32-character"},
		{strings.Repeat("0", 32), "must not be all zeros"},
		{strings.Repeat("g", 32), "32-character"},
		{"UPPERCASE" + strings.Repeat("0", 23), "32-character"},
	}
	for _, tc := range bad {
		uri := baseURI + "&trace-id=" + tc.val
		_, err := ParseDebugURI(uri)
		if err == nil {
			t.Errorf("trace-id=%q: expected error, got nil", tc.val)
			continue
		}
		if !strings.Contains(err.Error(), tc.err) {
			t.Errorf("trace-id=%q: error should mention %q, got: %v", tc.val, tc.err, err)
		}
	}
}

func TestParseDebugURI_TraceID_Traceparent_Takes_Precedence(t *testing.T) {
	// When both traceparent and trace-id are present, traceparent wins.
	otherID := strings.Repeat("a", 32)
	uri := baseURI + "&traceparent=" + validTraceparent + "&trace-id=" + otherID
	parsed, err := ParseDebugURI(uri)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// TraceID must come from traceparent, not the standalone trace-id param.
	if parsed.TraceID != validTraceID {
		t.Errorf("TraceID should be extracted from traceparent (%q), got %q", validTraceID, parsed.TraceID)
	}
}

// ── span-id standalone parameter ─────────────────────────────────────────────

func TestParseDebugURI_SpanID_Standalone_Valid(t *testing.T) {
	uri := baseURI + "&span-id=" + validSpanID
	parsed, err := ParseDebugURI(uri)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.SpanID != validSpanID {
		t.Errorf("expected SpanID=%q, got %q", validSpanID, parsed.SpanID)
	}
}

func TestParseDebugURI_SpanID_Standalone_Invalid(t *testing.T) {
	bad := []struct {
		val string
		err string
	}{
		{"tooshort", "16-character"},
		{strings.Repeat("0", 16), "must not be all zeros"},
		{"UPPERCASE0000000", "16-character"},
	}
	for _, tc := range bad {
		uri := baseURI + "&span-id=" + tc.val
		_, err := ParseDebugURI(uri)
		if err == nil {
			t.Errorf("span-id=%q: expected error, got nil", tc.val)
			continue
		}
		if !strings.Contains(err.Error(), tc.err) {
			t.Errorf("span-id=%q: error should mention %q, got: %v", tc.val, tc.err, err)
		}
	}
}

// ── URITraceContext helpers ───────────────────────────────────────────────────

func TestURITraceContext_IsEmpty_NilReturnTrue(t *testing.T) {
	var tc *URITraceContext
	if !tc.IsEmpty() {
		t.Error("nil URITraceContext must be considered empty")
	}
}

func TestURITraceContext_IsEmpty_AllFieldsEmpty(t *testing.T) {
	tc := &URITraceContext{}
	if !tc.IsEmpty() {
		t.Error("zero-value URITraceContext must be considered empty")
	}
}

func TestURITraceContext_IsEmpty_WithTraceID_NotEmpty(t *testing.T) {
	tc := &URITraceContext{TraceID: validTraceID}
	if tc.IsEmpty() {
		t.Error("URITraceContext with TraceID must not be empty")
	}
}

func TestURITraceContext_String_Nil(t *testing.T) {
	var tc *URITraceContext
	s := tc.String()
	if s == "" {
		t.Error("String() on nil should return a non-empty fallback message")
	}
}

func TestURITraceContext_String_WithTraceparent(t *testing.T) {
	tc := &URITraceContext{
		Traceparent: validTraceparent,
		TraceID:     validTraceID,
		SpanID:      validSpanID,
	}
	s := tc.String()
	if !strings.Contains(s, "traceparent=") {
		t.Errorf("String() should contain 'traceparent=', got %q", s)
	}
	if !strings.Contains(s, validTraceparent) {
		t.Errorf("String() should contain the traceparent value, got %q", s)
	}
}

func TestURITraceContext_String_StandaloneIDs(t *testing.T) {
	tc := &URITraceContext{TraceID: validTraceID, SpanID: validSpanID}
	s := tc.String()
	if !strings.Contains(s, "trace_id="+validTraceID) {
		t.Errorf("String() should include trace_id, got %q", s)
	}
	if !strings.Contains(s, "span_id="+validSpanID) {
		t.Errorf("String() should include span_id, got %q", s)
	}
}

func TestURITraceContext_String_TraceparentSuppressesStandaloneIDs(t *testing.T) {
	// When traceparent is set, standalone trace_id/span_id should NOT be
	// duplicated in String() output (they are already embedded in traceparent).
	tc := &URITraceContext{
		Traceparent: validTraceparent,
		TraceID:     validTraceID,
		SpanID:      validSpanID,
	}
	s := tc.String()
	if strings.Contains(s, "trace_id=") {
		t.Errorf("String() should suppress standalone trace_id when traceparent is set, got %q", s)
	}
	if strings.Contains(s, "span_id=") {
		t.Errorf("String() should suppress standalone span_id when traceparent is set, got %q", s)
	}
}

// ── TraceContextFromURI ───────────────────────────────────────────────────────

func TestTraceContextFromURI_Nil_ReturnsNil(t *testing.T) {
	if tc := TraceContextFromURI(nil); tc != nil {
		t.Errorf("TraceContextFromURI(nil) should return nil, got %+v", tc)
	}
}

func TestTraceContextFromURI_NoTraceFields_ReturnsNil(t *testing.T) {
	u := &ParsedDebugURI{TransactionHash: validHash, Network: "testnet"}
	if tc := TraceContextFromURI(u); tc != nil {
		t.Errorf("expected nil when URI has no trace context, got %+v", tc)
	}
}

func TestTraceContextFromURI_WithTraceparent_PopulatesAll(t *testing.T) {
	u := &ParsedDebugURI{
		Traceparent: validTraceparent,
		Tracestate:  "vendor=v1",
		TraceID:     validTraceID,
		SpanID:      validSpanID,
	}
	tc := TraceContextFromURI(u)
	if tc == nil {
		t.Fatal("expected non-nil URITraceContext")
	}
	if tc.Traceparent != validTraceparent {
		t.Errorf("Traceparent mismatch: got %q", tc.Traceparent)
	}
	if tc.TraceID != validTraceID {
		t.Errorf("TraceID mismatch: got %q", tc.TraceID)
	}
	if tc.SpanID != validSpanID {
		t.Errorf("SpanID mismatch: got %q", tc.SpanID)
	}
	if tc.Tracestate != "vendor=v1" {
		t.Errorf("Tracestate mismatch: got %q", tc.Tracestate)
	}
}

func TestTraceContextFromURI_RoundTrip_ViaParseDebugURI(t *testing.T) {
	uri := baseURI + "&traceparent=" + validTraceparent + "&tracestate=ns%3Dval"
	parsed, err := ParseDebugURI(uri)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tc := TraceContextFromURI(parsed)
	if tc == nil {
		t.Fatal("expected non-nil trace context after full round-trip")
	}
	if tc.TraceID != validTraceID {
		t.Errorf("round-trip TraceID mismatch: got %q", tc.TraceID)
	}
	if tc.SpanID != validSpanID {
		t.Errorf("round-trip SpanID mismatch: got %q", tc.SpanID)
	}
}

// ── DiagnosticReport.WithTraceContext ─────────────────────────────────────────

func TestDiagnosticReport_WithTraceContext_Chains(t *testing.T) {
	r := newTestRegistrar(t)
	report := r.Diagnose()
	tc := &URITraceContext{TraceID: validTraceID}
	returned := report.WithTraceContext(tc)
	if returned != report {
		t.Error("WithTraceContext should return the same report for chaining")
	}
	if report.TraceContext == nil || report.TraceContext.TraceID != validTraceID {
		t.Errorf("WithTraceContext did not attach context: %+v", report.TraceContext)
	}
}

func TestDiagnosticReport_WithTraceContext_NilIsAccepted(t *testing.T) {
	r := newTestRegistrar(t)
	report := r.Diagnose()
	// Attaching nil must not panic and should leave TraceContext as nil.
	report.WithTraceContext(nil)
	if report.TraceContext != nil {
		t.Errorf("WithTraceContext(nil) should leave TraceContext nil, got %+v", report.TraceContext)
	}
}

// ── VerificationReport.TraceContext ───────────────────────────────────────────

func TestVerificationReport_TraceContext_FieldExists(t *testing.T) {
	report := &VerificationReport{
		Platform:     "linux",
		Scheme:       Scheme,
		TraceContext: &URITraceContext{TraceID: validTraceID, SpanID: validSpanID},
	}
	if report.TraceContext == nil {
		t.Fatal("TraceContext should not be nil after explicit assignment")
	}
	if report.TraceContext.TraceID != validTraceID {
		t.Errorf("expected TraceID=%q, got %q", validTraceID, report.TraceContext.TraceID)
	}
}

func TestVerificationReport_TraceContext_NilByDefault(t *testing.T) {
	r := newTestRegistrar(t)
	report, _ := r.Verify()
	if report.TraceContext != nil {
		t.Errorf("TraceContext should be nil by default (no URI context), got %+v", report.TraceContext)
	}
}

// ── All trace params combined ─────────────────────────────────────────────────

func TestParseDebugURI_AllTraceParams_Combined(t *testing.T) {
	uri := baseURI +
		"&traceparent=" + validTraceparent +
		"&tracestate=vendorA%3Dval1%2CvendorB%3Dval2" +
		"&trace-id=" + strings.Repeat("b", 32) + // ignored: traceparent wins
		"&span-id=" + strings.Repeat("c", 16) // ignored: traceparent wins
	parsed, err := ParseDebugURI(uri)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// traceparent should dominate trace-id and span-id.
	if parsed.TraceID != validTraceID {
		t.Errorf("TraceID should come from traceparent, got %q", parsed.TraceID)
	}
	if parsed.SpanID != validSpanID {
		t.Errorf("SpanID should come from traceparent, got %q", parsed.SpanID)
	}
	if parsed.Tracestate == "" {
		t.Error("Tracestate should be populated")
	}
}

func TestParseDebugURI_TraceAndOtherParams_DoNotInterfere(t *testing.T) {
	// Ensure trace-context params do not disrupt other URI fields.
	uri := "glassbox://debug/" + validHash +
		"?network=mainnet&op=3&view=flamegraph" +
		"&traceparent=" + validTraceparent +
		"&source=dashboard"
	parsed, err := ParseDebugURI(uri)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.Network != "mainnet" {
		t.Errorf("Network should be mainnet, got %q", parsed.Network)
	}
	if parsed.Op == nil || *parsed.Op != 3 {
		t.Errorf("Op should be 3, got %v", parsed.Op)
	}
	if parsed.View != "flamegraph" {
		t.Errorf("View should be flamegraph, got %q", parsed.View)
	}
	if parsed.Source != "dashboard" {
		t.Errorf("Source should be dashboard, got %q", parsed.Source)
	}
	if parsed.TraceID != validTraceID {
		t.Errorf("TraceID should be set from traceparent, got %q", parsed.TraceID)
	}
}

// ── Error messages include Fix: hints ────────────────────────────────────────

func TestParseDebugURI_Traceparent_InvalidFormat_ErrorHasFix(t *testing.T) {
	_, err := ParseDebugURI(baseURI + "&traceparent=notvalid")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "Fix:") {
		t.Errorf("traceparent format error should include Fix: hint, got: %v", err)
	}
}

func TestParseDebugURI_TraceID_Invalid_ErrorHasFix(t *testing.T) {
	_, err := ParseDebugURI(baseURI + "&trace-id=short")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "Fix:") {
		t.Errorf("trace-id error should include Fix: hint, got: %v", err)
	}
}

func TestParseDebugURI_SpanID_Invalid_ErrorHasFix(t *testing.T) {
	_, err := ParseDebugURI(baseURI + "&span-id=short")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "Fix:") {
		t.Errorf("span-id error should include Fix: hint, got: %v", err)
	}
}
