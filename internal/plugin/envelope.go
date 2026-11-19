// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package plugin – envelope.go
// Issue #828: Plugin lifecycle isolation.
//
// Defines the wire-level request and response envelopes exchanged between the
// host process and a sandboxed plugin binary. Every call is wrapped in a
// PluginRequest; every reply (or error) comes back as a PluginResponse.
// Using a versioned envelope means the host can evolve the protocol without
// breaking existing plugin binaries.

package plugin

import (
	"encoding/json"
	"time"
)

// EnvelopeVersion is the current request/response schema version.
// Plugins must echo this value in their PluginResponse.Version field.
const EnvelopeVersion = "1"

// PluginMethod identifies the operation the host is requesting from the plugin.
type PluginMethod string

const (
	// MethodInit asks the plugin to initialise its internal state.
	MethodInit PluginMethod = "init"
	// MethodDecode asks the plugin to decode a raw event payload.
	MethodDecode PluginMethod = "decode"
	// MethodHealthCheck asks the plugin to report liveness.
	MethodHealthCheck PluginMethod = "health_check"
	// MethodCleanup asks the plugin to release resources before exit.
	MethodCleanup PluginMethod = "cleanup"
)

// ResourceLimits constrains the resources a plugin invocation may consume.
// Zero values mean "no limit imposed by the host".
type ResourceLimits struct {
	// TimeoutMs is the wall-clock budget in milliseconds.
	// When non-zero the executor cancels the invocation after this duration.
	TimeoutMs int64 `json:"timeout_ms,omitempty"`

	// MaxOutputBytes is the maximum number of bytes the plugin may write to
	// stdout for a single call. Responses larger than this are rejected.
	MaxOutputBytes int `json:"max_output_bytes,omitempty"`

	// MaxInputBytes is the maximum size of the serialised request payload.
	// The executor rejects oversized payloads before spawning a process.
	MaxInputBytes int `json:"max_input_bytes,omitempty"`
}

// DefaultResourceLimits returns conservative per-call limits that are safe for
// most plugins. Individual callers may override them as needed.
func DefaultResourceLimits() ResourceLimits {
	return ResourceLimits{
		TimeoutMs:      int64(defaultPluginTimeout / time.Millisecond),
		MaxOutputBytes: 4 * 1024 * 1024,  // 4 MiB
		MaxInputBytes:  1 * 1024 * 1024,  // 1 MiB
	}
}

// PluginRequest is the JSON envelope sent to the plugin binary over stdin.
// Each field is stable; new optional fields may be added in future versions.
type PluginRequest struct {
	// Version is the envelope schema version. Plugins should verify this.
	Version string `json:"version"`

	// ID is a caller-assigned opaque identifier echoed in the response so the
	// host can correlate responses to concurrent or pipelined calls.
	ID string `json:"id"`

	// Method is the operation being requested.
	Method PluginMethod `json:"method"`

	// EventType is populated for MethodDecode requests.
	EventType string `json:"event_type,omitempty"`

	// Data is the raw input payload for MethodDecode requests.
	Data json.RawMessage `json:"data,omitempty"`

	// Limits carries per-call resource constraints the plugin should respect.
	Limits ResourceLimits `json:"limits,omitempty"`
}

// PluginResponseStatus classifies the outcome of a plugin invocation.
type PluginResponseStatus string

const (
	// StatusOK indicates a successful invocation.
	StatusOK PluginResponseStatus = "ok"
	// StatusError indicates a plugin-level logical error (not a crash).
	StatusError PluginResponseStatus = "error"
)

// PluginResponse is the JSON envelope the plugin binary writes to stdout.
type PluginResponse struct {
	// Version must match EnvelopeVersion. Responses with a mismatched version
	// are rejected by the executor as potentially corrupt.
	Version string `json:"version"`

	// ID echoes the ID from the corresponding PluginRequest.
	ID string `json:"id"`

	// Status is StatusOK on success, StatusError on logical failure.
	Status PluginResponseStatus `json:"status"`

	// Result contains the decoded payload for MethodDecode responses.
	// It is nil for all other methods.
	Result json.RawMessage `json:"result,omitempty"`

	// Error contains a human-readable error message when Status == StatusError.
	Error string `json:"error,omitempty"`

	// DiagnosticContext holds optional key/value pairs that help plugin authors
	// debug failures without exposing sensitive request inputs to the host logs.
	DiagnosticContext map[string]string `json:"diagnostic_context,omitempty"`
}

// IsOK reports whether the response signals a successful invocation.
func (r *PluginResponse) IsOK() bool {
	return r != nil && r.Status == StatusOK
}

// Validate checks that a PluginResponse from a plugin binary is well-formed
// and safe to use. It returns a descriptive error for any anomaly that could
// corrupt trace or session state in the host.
func (r *PluginResponse) Validate(expectedID string) error {
	if r == nil {
		return &PluginProtocolError{Reason: "nil response from plugin"}
	}
	if r.Version != EnvelopeVersion {
		return &PluginProtocolError{
			Reason: "response version mismatch",
			Detail: "expected " + EnvelopeVersion + ", got " + r.Version,
		}
	}
	if r.ID != expectedID {
		return &PluginProtocolError{
			Reason: "response ID mismatch",
			Detail: "expected " + expectedID + ", got " + r.ID,
		}
	}
	if r.Status != StatusOK && r.Status != StatusError {
		return &PluginProtocolError{
			Reason: "unknown response status",
			Detail: string(r.Status),
		}
	}
	// If the plugin claims success but result is not valid JSON, reject it.
	if r.Status == StatusOK && r.Result != nil && !json.Valid(r.Result) {
		return &PluginProtocolError{Reason: "result field is not valid JSON"}
	}
	return nil
}

// PluginProtocolError is returned when a plugin sends a response that violates
// the envelope contract. It is always an isolated, stable error: the host
// remains usable and the plugin is quarantined automatically.
type PluginProtocolError struct {
	Reason string
	Detail string
}

func (e *PluginProtocolError) Error() string {
	if e.Detail == "" {
		return "plugin protocol error: " + e.Reason
	}
	return "plugin protocol error: " + e.Reason + " (" + e.Detail + ")"
}
