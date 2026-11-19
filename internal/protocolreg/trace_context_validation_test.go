// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package protocolreg

import (
	"strings"
	"testing"
)

// ── ValidateTraceContext — nil / empty ────────────────────────────────────────

func TestValidateTraceContext_Nil_IsOK(t *testing.T) {
	res := ValidateTraceContext(nil)
	if !res.OK {
		t.Errorf("nil URITraceContext should be OK, got issues: %v", res.Issues)
	}
	if len(res.Issues) != 0 {
		t.Errorf("nil context should produce no issues, got %d", len(res.Issues))
	}
}

func TestValidateTraceContext_Empty_IsOK(t *testing.T) {
	res := ValidateTraceContext(&URITraceContext{})
	if !res.OK {
		t.Errorf("empty URITraceContext should be OK")
	}
}

// ── ValidateTraceContext — valid Traceparent ──────────────────────────────────

func TestValidateTraceContext_ValidTraceparent_IsOK(t *testing.T) {
	tc := &URITraceContext{
		Traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		TraceID:     "4bf92f3577b34da6a3ce929d0e0e4736",
		SpanID:      "00f067aa0ba902b7",
	}
	res := ValidateTraceContext(tc)
	if !res.OK {
		t.Errorf("valid traceparent should pass, got: %s", res.Summary())
	}
}

func TestValidateTraceContext_ValidTraceparent_WithTracestate_IsOK(t *testing.T) {
	tc := &URITraceContext{
		Traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		Tracestate:  "vendorA=value1,vendorB=value2",
		TraceID:     "4bf92f3577b34da6a3ce929d0e0e4736",
		SpanID:      "00f067aa0ba902b7",
	}
	res := ValidateTraceContext(tc)
	if !res.OK {
		t.Errorf("valid traceparent + tracestate should pass, got: %s", res.Summary())
	}
}

// ── ValidateTraceContext — Traceparent format errors ─────────────────────────

func TestValidateTraceContext_Traceparent_WrongFormat_Error(t *testing.T) {
	tc := &URITraceContext{Traceparent: "not-a-traceparent"}
	res := ValidateTraceContext(tc)
	if res.OK {
		t.Error("invalid traceparent format should fail")
	}
	assertIssueField(t, res, "traceparent", "error")
}

func TestValidateTraceContext_Traceparent_UnsupportedVersion_Error(t *testing.T) {
	tc := &URITraceContext{
		Traceparent: "01-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
	}
	res := ValidateTraceContext(tc)
	if res.OK {
		t.Error("traceparent version '01' should fail")
	}
	assertIssueFieldContains(t, res, "traceparent", "error", "version")
}

func TestValidateTraceContext_Traceparent_AllZeroTraceID_Error(t *testing.T) {
	tc := &URITraceContext{
		Traceparent: "00-" + strings.Repeat("0", 32) + "-00f067aa0ba902b7-01",
	}
	res := ValidateTraceContext(tc)
	if res.OK {
		t.Error("all-zero trace-id should fail")
	}
	assertIssueFieldContains(t, res, "traceparent", "error", "trace-id")
}

func TestValidateTraceContext_Traceparent_AllZeroParentID_Error(t *testing.T) {
	tc := &URITraceContext{
		Traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-" + strings.Repeat("0", 16) + "-01",
	}
	res := ValidateTraceContext(tc)
	if res.OK {
		t.Error("all-zero parent-id should fail")
	}
	assertIssueFieldContains(t, res, "traceparent", "error", "parent-id")
}

func TestValidateTraceContext_Traceparent_NullByte_Error(t *testing.T) {
	tc := &URITraceContext{
		Traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01\x00",
	}
	res := ValidateTraceContext(tc)
	if res.OK {
		t.Error("null byte in traceparent should fail")
	}
	assertIssueFieldContains(t, res, "traceparent", "error", "null bytes")
}

// ── ValidateTraceContext — Tracestate errors ──────────────────────────────────

func TestValidateTraceContext_Tracestate_TooLong_Error(t *testing.T) {
	tc := &URITraceContext{
		Traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		Tracestate:  strings.Repeat("a", maxTracestateLen+1),
		TraceID:     "4bf92f3577b34da6a3ce929d0e0e4736",
		SpanID:      "00f067aa0ba902b7",
	}
	res := ValidateTraceContext(tc)
	if res.OK {
		t.Error("too-long tracestate should fail")
	}
	assertIssueFieldContains(t, res, "tracestate", "error", "too long")
}

func TestValidateTraceContext_Tracestate_AtMaxLen_IsOK(t *testing.T) {
	tc := &URITraceContext{
		Traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		Tracestate:  strings.Repeat("a", maxTracestateLen),
		TraceID:     "4bf92f3577b34da6a3ce929d0e0e4736",
		SpanID:      "00f067aa0ba902b7",
	}
	res := ValidateTraceContext(tc)
	if !res.OK {
		t.Errorf("tracestate at max length should pass, got: %s", res.Summary())
	}
}

func TestValidateTraceContext_Tracestate_NullByte_Error(t *testing.T) {
	tc := &URITraceContext{
		Traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		Tracestate:  "vendor=val\x00ue",
	}
	res := ValidateTraceContext(tc)
	if res.OK {
		t.Error("null byte in tracestate should fail")
	}
	assertIssueFieldContains(t, res, "tracestate", "error", "null bytes")
}

// ── ValidateTraceContext — standalone TraceID ─────────────────────────────────

func TestValidateTraceContext_StandaloneTraceID_Valid_IsOK(t *testing.T) {
	tc := &URITraceContext{TraceID: "4bf92f3577b34da6a3ce929d0e0e4736"}
	res := ValidateTraceContext(tc)
	if !res.OK {
		t.Errorf("valid standalone trace_id should pass, got: %s", res.Summary())
	}
}

func TestValidateTraceContext_StandaloneTraceID_TooShort_Error(t *testing.T) {
	tc := &URITraceContext{TraceID: "tooshort"}
	res := ValidateTraceContext(tc)
	if res.OK {
		t.Error("short trace_id should fail")
	}
	assertIssueField(t, res, "trace_id", "error")
}

func TestValidateTraceContext_StandaloneTraceID_AllZeros_Error(t *testing.T) {
	tc := &URITraceContext{TraceID: strings.Repeat("0", 32)}
	res := ValidateTraceContext(tc)
	if res.OK {
		t.Error("all-zero trace_id should fail")
	}
	assertIssueFieldContains(t, res, "trace_id", "error", "all zeros")
}

func TestValidateTraceContext_StandaloneTraceID_NullByte_Error(t *testing.T) {
	tc := &URITraceContext{TraceID: "4bf92f3577b34da6a\x003ce929d0e0e4736"}
	res := ValidateTraceContext(tc)
	if res.OK {
		t.Error("null byte in trace_id should fail")
	}
	assertIssueFieldContains(t, res, "trace_id", "error", "null bytes")
}

// Traceparent supersedes standalone trace_id — standalone validation is skipped.
func TestValidateTraceContext_StandaloneTraceID_IgnoredWhenTraceparentPresent(t *testing.T) {
	tc := &URITraceContext{
		Traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		TraceID:     strings.Repeat("0", 32), // would fail standalone validation
	}
	res := ValidateTraceContext(tc)
	// Should still pass because traceparent is present and valid; the all-zero
	// TraceID field is not re-validated independently when traceparent is set.
	if !res.OK {
		t.Errorf("standalone trace_id errors should be suppressed when traceparent is valid; got: %s", res.Summary())
	}
}

// ── ValidateTraceContext — standalone SpanID ──────────────────────────────────

func TestValidateTraceContext_StandaloneSpanID_Valid_IsOK(t *testing.T) {
	tc := &URITraceContext{
		TraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
		SpanID:  "00f067aa0ba902b7",
	}
	res := ValidateTraceContext(tc)
	if !res.OK {
		t.Errorf("valid standalone span_id with trace_id should pass, got: %s", res.Summary())
	}
}

func TestValidateTraceContext_StandaloneSpanID_TooShort_Error(t *testing.T) {
	tc := &URITraceContext{
		TraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
		SpanID:  "short",
	}
	res := ValidateTraceContext(tc)
	if res.OK {
		t.Error("short span_id should fail")
	}
	assertIssueField(t, res, "span_id", "error")
}

func TestValidateTraceContext_StandaloneSpanID_AllZeros_Error(t *testing.T) {
	tc := &URITraceContext{
		TraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
		SpanID:  strings.Repeat("0", 16),
	}
	res := ValidateTraceContext(tc)
	if res.OK {
		t.Error("all-zero span_id should fail")
	}
	assertIssueFieldContains(t, res, "span_id", "error", "all zeros")
}

func TestValidateTraceContext_StandaloneSpanID_WithoutTraceID_Warning(t *testing.T) {
	tc := &URITraceContext{SpanID: "00f067aa0ba902b7"}
	res := ValidateTraceContext(tc)
	// span_id without trace_id is a warning (incomplete context), not an error.
	if !res.OK {
		t.Errorf("span_id without trace_id should be a warning, not an error; got: %s", res.Summary())
	}
	assertIssueField(t, res, "span_id", "warning")
}

func TestValidateTraceContext_StandaloneSpanID_NullByte_Error(t *testing.T) {
	tc := &URITraceContext{
		TraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
		SpanID:  "00f067aa\x000ba902b7",
	}
	res := ValidateTraceContext(tc)
	if res.OK {
		t.Error("null byte in span_id should fail")
	}
	assertIssueFieldContains(t, res, "span_id", "error", "null bytes")
}

// ── ValidateTraceContext — Summary ────────────────────────────────────────────

func TestValidateTraceContext_Summary_NoIssues_EmptyString(t *testing.T) {
	res := ValidateTraceContext(nil)
	if res.Summary() != "" {
		t.Errorf("Summary with no issues should return empty string, got %q", res.Summary())
	}
}

func TestValidateTraceContext_Summary_WithIssues_NonEmpty(t *testing.T) {
	tc := &URITraceContext{Traceparent: "badformat"}
	res := ValidateTraceContext(tc)
	s := res.Summary()
	if s == "" {
		t.Error("Summary with issues should return non-empty string")
	}
	if !strings.Contains(s, "traceparent") {
		t.Errorf("Summary should mention the field name, got: %q", s)
	}
	if !strings.Contains(s, "Fix:") {
		t.Errorf("Summary should include a Fix: hint, got: %q", s)
	}
}

// ── ValidateTraceContext — round-trip from ParseDebugURI ─────────────────────

func TestValidateTraceContext_RoundTrip_ParsedURI_Valid(t *testing.T) {
	uri := baseURI + "&traceparent=00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	parsed, err := ParseDebugURI(uri)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	tc := TraceContextFromURI(parsed)
	if tc == nil {
		t.Fatal("expected non-nil trace context")
	}
	res := ValidateTraceContext(tc)
	if !res.OK {
		t.Errorf("trace context from valid URI should validate OK, got: %s", res.Summary())
	}
}

func TestValidateTraceContext_Fix_Hints_Present(t *testing.T) {
	cases := []struct {
		name string
		tc   *URITraceContext
	}{
		{"bad traceparent", &URITraceContext{Traceparent: "bad"}},
		{"short trace_id", &URITraceContext{TraceID: "short"}},
		{"short span_id", &URITraceContext{SpanID: "short", TraceID: "4bf92f3577b34da6a3ce929d0e0e4736"}},
		{"long tracestate", &URITraceContext{
			Traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
			Tracestate:  strings.Repeat("x", maxTracestateLen+1),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := ValidateTraceContext(tc.tc)
			for _, issue := range res.Issues {
				if issue.Severity == "error" && issue.Fix == "" {
					t.Errorf("[%s] error issue for field %q is missing a Fix hint", tc.name, issue.Field)
				}
			}
		})
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func assertIssueField(t *testing.T, res *TraceContextValidationResult, field, severity string) {
	t.Helper()
	for _, issue := range res.Issues {
		if issue.Field == field && issue.Severity == severity {
			return
		}
	}
	t.Errorf("expected a %s-severity issue for field %q; issues: %v", severity, field, res.Issues)
}

func assertIssueFieldContains(t *testing.T, res *TraceContextValidationResult, field, severity, substr string) {
	t.Helper()
	for _, issue := range res.Issues {
		if issue.Field == field && issue.Severity == severity {
			if !strings.Contains(issue.Description, substr) {
				t.Errorf("issue for field %q should mention %q, got: %q", field, substr, issue.Description)
			}
			return
		}
	}
	t.Errorf("expected a %s-severity issue for field %q containing %q; issues: %v",
		severity, field, substr, res.Issues)
}
