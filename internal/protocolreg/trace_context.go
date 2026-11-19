// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package protocolreg

import "strings"

// URITraceContext carries the W3C Trace Context identifiers that were embedded
// in a glassbox:// URI or attached to a protocol registration event.
//
// These values allow the Glassbox UI and backend telemetry pipeline to correlate
// a deep-link invocation or a registration verification with the originating
// distributed trace, improving trace accuracy and session attribution.
//
// Fields are always lowercase hex strings; empty string means the field was
// not provided.
type URITraceContext struct {
	// Traceparent is the full W3C traceparent header value:
	//   00-<trace-id:32hex>-<parent-id:16hex>-<flags:2hex>
	// Empty when no traceparent was supplied.
	Traceparent string `json:"traceparent,omitempty"`
	// Tracestate is the optional W3C tracestate vendor data.
	Tracestate string `json:"tracestate,omitempty"`
	// TraceID is the 32-character hex trace identifier extracted from
	// Traceparent, or supplied standalone via the "trace-id" query parameter.
	TraceID string `json:"trace_id,omitempty"`
	// SpanID is the 16-character hex span/parent-span identifier extracted from
	// Traceparent, or supplied standalone via the "span-id" query parameter.
	SpanID string `json:"span_id,omitempty"`
}

// IsEmpty returns true when no trace-context fields have been populated.
func (tc *URITraceContext) IsEmpty() bool {
	if tc == nil {
		return true
	}
	return tc.Traceparent == "" && tc.Tracestate == "" &&
		tc.TraceID == "" && tc.SpanID == ""
}

// String returns a compact human-readable summary for log output.
// Example: "traceparent=00-abc...def-0011223344556677-01 trace_id=abc...def"
func (tc *URITraceContext) String() string {
	if tc == nil || tc.IsEmpty() {
		return "<no trace context>"
	}
	var parts []string
	if tc.Traceparent != "" {
		parts = append(parts, "traceparent="+tc.Traceparent)
	}
	if tc.TraceID != "" && tc.Traceparent == "" {
		// Only emit standalone trace-id when traceparent is absent (to avoid duplication).
		parts = append(parts, "trace_id="+tc.TraceID)
	}
	if tc.SpanID != "" && tc.Traceparent == "" {
		parts = append(parts, "span_id="+tc.SpanID)
	}
	if tc.Tracestate != "" {
		parts = append(parts, "tracestate="+tc.Tracestate)
	}
	return strings.Join(parts, " ")
}

// TraceContextFromURI extracts a URITraceContext from a successfully parsed
// ParsedDebugURI. Returns nil when the URI carries no trace-context fields.
func TraceContextFromURI(u *ParsedDebugURI) *URITraceContext {
	if u == nil {
		return nil
	}
	tc := &URITraceContext{
		Traceparent: u.Traceparent,
		Tracestate:  u.Tracestate,
		TraceID:     u.TraceID,
		SpanID:      u.SpanID,
	}
	if tc.IsEmpty() {
		return nil
	}
	return tc
}
