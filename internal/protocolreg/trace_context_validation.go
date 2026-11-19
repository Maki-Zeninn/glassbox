// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package protocolreg

import (
	"fmt"
	"strings"
)

// TraceContextValidationResult holds the outcome of ValidateTraceContext.
type TraceContextValidationResult struct {
	// OK is true when no error-severity issues were found.
	OK bool
	// Issues lists every validation problem found (errors and warnings).
	Issues []TraceContextIssue
}

// TraceContextIssue describes a single validation finding for a URITraceContext.
type TraceContextIssue struct {
	// Field is the name of the field that failed validation.
	Field string
	// Severity is "error" (invalid context) or "warning" (degraded correlation).
	Severity string
	// Description explains what is wrong.
	Description string
	// Fix is an actionable remediation step.
	Fix string
}

// ValidateTraceContext validates a URITraceContext independently of URI parsing.
// It is suitable for programmatic use (e.g. before attaching trace context to
// a diagnostic report) without needing to go through ParseDebugURI.
//
// Validation rules:
//   - Null bytes are not allowed in any field.
//   - When Traceparent is set it must conform to the W3C traceparent format:
//     version "00", non-zero 32-hex trace-id, non-zero 16-hex parent-id.
//   - Tracestate must not exceed maxTracestateLen characters.
//   - TraceID when set must be a 32-character lowercase hex string, non-zero.
//   - SpanID when set must be a 16-character lowercase hex string, non-zero.
//   - SpanID without TraceID is a warning (incomplete context, no error).
//
// Returns a result with OK=true when all checks pass (or the context is empty).
func ValidateTraceContext(tc *URITraceContext) *TraceContextValidationResult {
	res := &TraceContextValidationResult{OK: true}

	if tc == nil || tc.IsEmpty() {
		return res
	}

	// ── Traceparent ──────────────────────────────────────────────────────────
	if tc.Traceparent != "" {
		tp := strings.ToLower(strings.TrimSpace(tc.Traceparent))

		if strings.ContainsRune(tp, 0) {
			res.addError("traceparent", "traceparent contains null bytes and cannot be used",
				"Remove null bytes from the traceparent value.")
		} else if !traceparentPattern.MatchString(tp) {
			res.addError("traceparent",
				fmt.Sprintf("traceparent %q does not conform to W3C format 00-<32hex>-<16hex>-<2hex>", tp),
				"Use a valid W3C traceparent, e.g. 00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
		} else {
			parts := strings.SplitN(tp, "-", 4)
			if parts[0] != "00" {
				res.addError("traceparent",
					fmt.Sprintf("unsupported traceparent version %q: only version \"00\" is supported", parts[0]),
					"Ensure the traceparent begins with \"00-\".")
			}
			if parts[1] == strings.Repeat("0", 32) {
				res.addError("traceparent",
					"traceparent trace-id must not be all zeros",
					"Use a non-zero 32-character hex trace ID.")
			}
			if parts[2] == strings.Repeat("0", 16) {
				res.addError("traceparent",
					"traceparent parent-id must not be all zeros",
					"Use a non-zero 16-character hex span ID.")
			}
		}
	}

	// ── Tracestate ───────────────────────────────────────────────────────────
	if tc.Tracestate != "" {
		if strings.ContainsRune(tc.Tracestate, 0) {
			res.addError("tracestate", "tracestate contains null bytes and cannot be used",
				"Remove null bytes from the tracestate value.")
		} else if len(tc.Tracestate) > maxTracestateLen {
			res.addError("tracestate",
				fmt.Sprintf("tracestate is too long (%d characters, max %d)", len(tc.Tracestate), maxTracestateLen),
				fmt.Sprintf("Truncate the tracestate value to at most %d characters.", maxTracestateLen))
		}
	}

	// ── Standalone TraceID ───────────────────────────────────────────────────
	// Only validate when traceparent is absent (traceparent supersedes trace-id).
	if tc.TraceID != "" && tc.Traceparent == "" {
		tid := strings.ToLower(strings.TrimSpace(tc.TraceID))
		if strings.ContainsRune(tid, 0) {
			res.addError("trace_id", "trace_id contains null bytes and cannot be used",
				"Remove null bytes from the trace_id value.")
		} else if !traceIDPattern.MatchString(tid) {
			res.addError("trace_id",
				fmt.Sprintf("trace_id %q must be a 32-character lowercase hex string", tid),
				"Provide a valid 128-bit trace identifier, e.g. 4bf92f3577b34da6a3ce929d0e0e4736")
		} else if tid == strings.Repeat("0", 32) {
			res.addError("trace_id",
				"trace_id must not be all zeros",
				"Use a non-zero 32-character hex trace ID.")
		}
	}

	// ── Standalone SpanID ────────────────────────────────────────────────────
	if tc.SpanID != "" && tc.Traceparent == "" {
		sid := strings.ToLower(strings.TrimSpace(tc.SpanID))
		if strings.ContainsRune(sid, 0) {
			res.addError("span_id", "span_id contains null bytes and cannot be used",
				"Remove null bytes from the span_id value.")
		} else if !spanIDPattern.MatchString(sid) {
			res.addError("span_id",
				fmt.Sprintf("span_id %q must be a 16-character lowercase hex string", sid),
				"Provide a valid 64-bit span identifier, e.g. 00f067aa0ba902b7")
		} else if sid == strings.Repeat("0", 16) {
			res.addError("span_id",
				"span_id must not be all zeros",
				"Use a non-zero 16-character hex span ID.")
		}

		// SpanID without TraceID provides incomplete context — warn but do not error.
		if tc.TraceID == "" {
			res.addWarning("span_id",
				"span_id is set but trace_id is absent — trace correlation will be incomplete",
				"Provide trace_id alongside span_id, or use a full traceparent instead.")
		}
	}

	res.OK = !res.hasErrors()
	return res
}

// addError appends an error-severity issue to the result.
func (r *TraceContextValidationResult) addError(field, description, fix string) {
	r.Issues = append(r.Issues, TraceContextIssue{
		Field:       field,
		Severity:    "error",
		Description: description,
		Fix:         fix,
	})
}

// addWarning appends a warning-severity issue to the result.
func (r *TraceContextValidationResult) addWarning(field, description, fix string) {
	r.Issues = append(r.Issues, TraceContextIssue{
		Field:       field,
		Severity:    "warning",
		Description: description,
		Fix:         fix,
	})
}

// hasErrors returns true when any issue has error severity.
func (r *TraceContextValidationResult) hasErrors() bool {
	for _, i := range r.Issues {
		if i.Severity == "error" {
			return true
		}
	}
	return false
}

// Summary returns a concise human-readable description of all issues.
// Returns an empty string when there are no issues.
func (r *TraceContextValidationResult) Summary() string {
	if len(r.Issues) == 0 {
		return ""
	}
	var sb strings.Builder
	for i, issue := range r.Issues {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(fmt.Sprintf("[%s] %s: %s", issue.Severity, issue.Field, issue.Description))
		if issue.Fix != "" {
			sb.WriteString("\n  Fix: ")
			sb.WriteString(issue.Fix)
		}
	}
	return sb.String()
}
