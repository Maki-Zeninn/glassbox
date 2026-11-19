// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package rpc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// FailureCause is a machine-stable label for why a single RPC attempt failed.
// It is attached to AttemptRecord so diagnostic output can attribute failures
// to specific network-layer causes without parsing error strings.
type FailureCause string

const (
	// FailureCauseNone means the attempt succeeded (no failure).
	FailureCauseNone FailureCause = ""
	// FailureCauseTimeout means the per-attempt context deadline was exceeded.
	FailureCauseTimeout FailureCause = "timeout"
	// FailureCauseConnectionRefused means the remote host actively rejected the connection.
	FailureCauseConnectionRefused FailureCause = "connection_refused"
	// FailureCauseDNS means the hostname could not be resolved.
	FailureCauseDNS FailureCause = "dns_failure"
	// FailureCauseUnreachable means the network or host was unreachable (no route).
	FailureCauseUnreachable FailureCause = "network_unreachable"
	// FailureCauseRateLimited means the server responded with HTTP 429.
	FailureCauseRateLimited FailureCause = "rate_limited"
	// FailureCauseServerError means the server responded with a 5xx status code.
	FailureCauseServerError FailureCause = "server_error"
	// FailureCauseClientError means the server responded with a 4xx status code
	// that is not retryable (e.g. 401 Unauthorized, 400 Bad Request).
	FailureCauseClientError FailureCause = "client_error"
	// FailureCauseContextCanceled means the parent context was canceled before
	// the attempt completed.
	FailureCauseContextCanceled FailureCause = "context_canceled"
	// FailureCauseEOF means the connection was closed unexpectedly by the server.
	FailureCauseEOF FailureCause = "connection_reset"
	// FailureCauseUnknown is used when the error does not match any known pattern.
	FailureCauseUnknown FailureCause = "unknown"
)

// ClassifyFailureCause derives a FailureCause from the error and HTTP status
// code returned by a single RPC attempt. It is intentionally conservative:
// it never inspects response bodies, only transport-layer signals.
func ClassifyFailureCause(err error, httpStatusCode int) FailureCause {
	if err == nil {
		return FailureCauseNone
	}

	if errors.Is(err, context.Canceled) {
		return FailureCauseContextCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return FailureCauseTimeout
	}

	// HTTP status code takes priority over error text for server-side failures.
	if httpStatusCode > 0 {
		switch {
		case httpStatusCode == http.StatusTooManyRequests:
			return FailureCauseRateLimited
		case httpStatusCode == http.StatusRequestTimeout:
			return FailureCauseTimeout
		case httpStatusCode >= 500:
			return FailureCauseServerError
		case httpStatusCode >= 400:
			return FailureCauseClientError
		}
	}

	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "connection refused"):
		return FailureCauseConnectionRefused
	case strings.Contains(msg, "no such host"),
		strings.Contains(msg, "dns"), strings.Contains(msg, "lookup"):
		return FailureCauseDNS
	case strings.Contains(msg, "no route to host"),
		strings.Contains(msg, "network unreachable"),
		strings.Contains(msg, "network is unreachable"):
		return FailureCauseUnreachable
	case strings.Contains(msg, "connection reset"),
		strings.Contains(msg, "eof"):
		return FailureCauseEOF
	case strings.Contains(msg, "i/o timeout"),
		strings.Contains(msg, "timeout"):
		return FailureCauseTimeout
	}

	return FailureCauseUnknown
}

// FailureCauseRemediationHint returns a concise, actionable hint for the given
// cause. The hint is intended for direct display in CLI output and JSON error
// envelopes so users know what to do without opening docs.
func FailureCauseRemediationHint(cause FailureCause) string {
	switch cause {
	case FailureCauseTimeout:
		return "The endpoint did not respond in time. Try a different endpoint with --rpc-url, or increase --rpc-timeout if supported."
	case FailureCauseConnectionRefused:
		return "The endpoint actively refused the connection. Verify the URL is correct and the RPC node is running."
	case FailureCauseDNS:
		return "The hostname could not be resolved. Check your network connection and confirm the endpoint URL is spelled correctly."
	case FailureCauseUnreachable:
		return "The network or host was unreachable. Check your internet connection, firewall rules, and VPN settings."
	case FailureCauseRateLimited:
		return "The RPC endpoint is rate-limiting your requests. Wait briefly before retrying, or switch to a different endpoint with --rpc-url."
	case FailureCauseServerError:
		return "The RPC server returned an internal error. The node may be overloaded or temporarily unavailable — retry in a few moments."
	case FailureCauseClientError:
		return "The RPC server rejected the request (4xx). Check your authentication token (--rpc-token / GLASSBOX_RPC_TOKEN) and confirm the endpoint supports the requested method."
	case FailureCauseEOF:
		return "The connection was closed by the server before a response was received. The node may be restarting — retry shortly."
	case FailureCauseContextCanceled:
		return "The request was cancelled before it completed. If this was not intentional, retry the command."
	default:
		return "An unclassified network error occurred. Use --verbose for more detail, or run 'glassbox doctor' for a full environment check."
	}
}

// RPCFailureSummary is a structured, user-facing report of all endpoint
// attempts made during a single RPC operation (e.g. GetTransaction).
// It is produced by FormatRPCFailureSummary and attached to AllNodesFailedError
// diagnostics surfaced in the debug command.
type RPCFailureSummary struct {
	// TotalAttempts is the number of provider attempts made.
	TotalAttempts int `json:"total_attempts"`
	// TotalDurationMs is the elapsed wall-clock time across all attempts in ms.
	TotalDurationMs int64 `json:"total_duration_ms"`
	// Attempts is the per-attempt diagnostic record.
	Attempts []AttemptSummary `json:"attempts"`
	// SucceededVia is the URL that ultimately succeeded, or "" if all failed.
	SucceededVia string `json:"succeeded_via,omitempty"`
	// PrimaryFailedVia is the primary endpoint's failure cause when it failed.
	PrimaryFailedVia FailureCause `json:"primary_failed_via,omitempty"`
	// UsedFallback reports whether a non-primary provider was used.
	UsedFallback bool `json:"used_fallback"`
	// AllFailed reports whether every provider failed.
	AllFailed bool `json:"all_failed"`
	// RemediationHints are actionable suggestions derived from the failure causes seen.
	RemediationHints []string `json:"remediation_hints,omitempty"`
}

// AttemptSummary is a human-readable record of a single provider attempt.
type AttemptSummary struct {
	URL            string       `json:"url"`
	LatencyMs      int64        `json:"latency_ms"`
	HTTPStatusCode int          `json:"http_status_code,omitempty"`
	Cause          FailureCause `json:"cause,omitempty"`
	Retryable      bool         `json:"retryable"`
	Success        bool         `json:"success"`
}

// BuildRPCFailureSummary converts an AttemptDiagnostics into a structured
// RPCFailureSummary, classifying each attempt and computing remediation hints.
func BuildRPCFailureSummary(d AttemptDiagnostics) RPCFailureSummary {
	s := RPCFailureSummary{
		TotalAttempts:   len(d.Attempts),
		TotalDurationMs: d.TotalDuration.Milliseconds(),
		SucceededVia:    d.SucceededURL,
		AllFailed:       d.SucceededURL == "",
	}

	seenCauses := make(map[FailureCause]bool)

	for i, a := range d.Attempts {
		cause := ClassifyFailureCause(a.Err, a.HTTPStatusCode)
		success := a.Err == nil

		as := AttemptSummary{
			URL:            a.URL,
			LatencyMs:      a.Latency.Milliseconds(),
			HTTPStatusCode: a.HTTPStatusCode,
			Cause:          cause,
			Retryable:      a.Retryable,
			Success:        success,
		}
		s.Attempts = append(s.Attempts, as)

		// Track primary endpoint failure cause (first attempt is the primary).
		if i == 0 && !success {
			s.PrimaryFailedVia = cause
		}

		// Mark fallback usage when a non-first attempt succeeded.
		if i > 0 && success {
			s.UsedFallback = true
		}

		if !success && !seenCauses[cause] {
			seenCauses[cause] = true
		}
	}

	// Collect distinct, actionable remediation hints (de-duplicated by cause).
	for cause := range seenCauses {
		if hint := FailureCauseRemediationHint(cause); hint != "" {
			s.RemediationHints = append(s.RemediationHints, hint)
		}
	}

	return s
}

// FormatRPCFailureSummaryText renders an RPCFailureSummary as a concise,
// human-readable block suitable for stderr output. It is called by the debug
// command when all providers fail.
func FormatRPCFailureSummaryText(s RPCFailureSummary) string {
	var b strings.Builder

	b.WriteString("RPC Diagnostic Report\n")
	b.WriteString("─────────────────────\n")
	fmt.Fprintf(&b, "  Attempts : %d  |  Duration: %dms\n", s.TotalAttempts, s.TotalDurationMs)

	if s.SucceededVia != "" {
		fmt.Fprintf(&b, "  Result   : succeeded via %s", s.SucceededVia)
		if s.UsedFallback {
			b.WriteString(" (fallback endpoint used)")
		}
		b.WriteString("\n")
	} else {
		b.WriteString("  Result   : all endpoints failed\n")
	}

	if len(s.Attempts) > 0 {
		b.WriteString("\n  Endpoint attempts:\n")
		for i, a := range s.Attempts {
			label := "primary"
			if i > 0 {
				label = fmt.Sprintf("fallback #%d", i)
			}
			if a.Success {
				fmt.Fprintf(&b, "    [%s] %s — OK (%dms)\n", label, a.URL, a.LatencyMs)
			} else {
				statusStr := ""
				if a.HTTPStatusCode > 0 {
					statusStr = fmt.Sprintf(", HTTP %d", a.HTTPStatusCode)
				}
				fmt.Fprintf(&b, "    [%s] %s — FAILED: %s%s (%dms)\n",
					label, a.URL, a.Cause, statusStr, a.LatencyMs)
			}
		}
	}

	if len(s.RemediationHints) > 0 {
		b.WriteString("\n  Suggestions:\n")
		for _, hint := range s.RemediationHints {
			fmt.Fprintf(&b, "    • %s\n", hint)
		}
	}

	return b.String()
}

// AllNodesFailedDiagnostic combines AttemptDiagnostics with the provider pool
// state snapshot to produce a complete diagnostic when all endpoints fail.
// The provider states are included to show users which nodes are Degraded/Down
// and have been skipped by the circuit breaker.
type AllNodesFailedDiagnostic struct {
	// Summary is the structured per-attempt report.
	Summary RPCFailureSummary `json:"summary"`
	// ProviderStates is a snapshot of every provider's health state at the
	// time of failure. Nil when the pool state is not available.
	ProviderStates []ProviderState `json:"provider_states,omitempty"`
	// Network is the Stellar network that was targeted.
	Network string `json:"network,omitempty"`
	// Timestamp is when this diagnostic was captured.
	Timestamp time.Time `json:"timestamp"`
}

// FormatAllNodesFailedText renders an AllNodesFailedDiagnostic as a
// complete, CLI-friendly text block for display on stderr.
func FormatAllNodesFailedText(d AllNodesFailedDiagnostic) string {
	var b strings.Builder

	b.WriteString("\n╔══════════════════════════════════════════════════════╗\n")
	b.WriteString("║          RPC: All Endpoints Failed                  ║\n")
	b.WriteString("╚══════════════════════════════════════════════════════╝\n\n")

	if d.Network != "" {
		fmt.Fprintf(&b, "Network : %s\n", d.Network)
	}

	b.WriteString(FormatRPCFailureSummaryText(d.Summary))

	if len(d.ProviderStates) > 0 {
		b.WriteString("\n  Provider pool state:\n")
		for _, ps := range d.ProviderStates {
			age := ""
			if !ps.LastFailureAt.IsZero() {
				age = fmt.Sprintf(", last failure %s ago", time.Since(ps.LastFailureAt).Round(time.Second))
			}
			fmt.Fprintf(&b, "    %-8s %s  (failures: %d%s)\n",
				"["+ps.Status.String()+"]", ps.URL, ps.ConsecutiveFailures, age)
		}
	}

	b.WriteString("\n  Recovery options:\n")
	b.WriteString("    • Specify an alternate endpoint:  --rpc-url <url>\n")
	b.WriteString("    • Check endpoint health:          glassbox doctor\n")
	b.WriteString("    • Verify network connectivity:    glassbox status --network <name>\n")

	return b.String()
}
