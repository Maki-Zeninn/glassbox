// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// runtime_validator.go provides runtime validation for the four external
// payload types that cross the Glassbox binding boundary: command inputs,
// trace payloads, audit records, and session envelopes.
//
// All validators share a common FieldError type that carries a dot-separated
// field path so callers can pinpoint the offending value without parsing free-
// form error messages.  Error codes are aligned with the stable codes defined
// in internal/errors/glassbox_error_code.go so automation can key on them.
package bindings

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

// ─── Shared types ─────────────────────────────────────────────────────────────

// RuntimeValidationCode is a stable string code carried by every FieldError.
// It mirrors the ErstErrorCode taxonomy so TypeScript consumers can map codes
// one-to-one to Go stable codes.
type RuntimeValidationCode string

const (
	// CodeRequiredFieldMissing indicates a required field is absent.
	CodeRequiredFieldMissing RuntimeValidationCode = "REQUIRED_FIELD_MISSING"
	// CodeWrongType indicates the value's runtime type does not match the schema.
	CodeWrongType RuntimeValidationCode = "WRONG_TYPE"
	// CodeInvalidEnumValue indicates a string enum is outside the allowed set.
	CodeInvalidEnumValue RuntimeValidationCode = "INVALID_ENUM_VALUE"
	// CodeInvalidValue indicates a value that is syntactically correct but
	// semantically invalid (e.g. an empty timestamp, NaN).
	CodeInvalidValue RuntimeValidationCode = "INVALID_VALUE"
	// CodeUnknownField indicates an unrecognised field when strict mode is on.
	CodeUnknownField RuntimeValidationCode = "UNKNOWN_FIELD"
	// CodeMutualExclusion indicates mutually exclusive fields are both present.
	CodeMutualExclusion RuntimeValidationCode = "MUTUAL_EXCLUSION_VIOLATED"
)

// FieldError is a single field-level validation failure.
type FieldError struct {
	// Path is the dot-separated path to the field, e.g. "trace.input.amount".
	Path string `json:"path"`
	// Message is a human-readable description of the failure.
	Message string `json:"message"`
	// Code is the stable automation-friendly error code.
	Code RuntimeValidationCode `json:"code"`
}

func (e FieldError) Error() string {
	return fmt.Sprintf("[%s] %s: %s", e.Code, e.Path, e.Message)
}

// RuntimeValidationResult carries the outcome of a runtime validation call.
type RuntimeValidationResult struct {
	// Valid is true when all checks passed.
	Valid bool `json:"valid"`
	// Errors is empty when Valid is true.
	Errors []FieldError `json:"errors"`
}

// ValidationMode controls how unknown fields are treated.
type ValidationMode int

const (
	// Strict rejects unknown fields in addition to schema violations.
	Strict ValidationMode = iota
	// Permissive silently ignores unknown additive fields.  Use this when
	// deserialising external JSON that may contain additive fields from a
	// newer Glassbox version.
	Permissive
)

// ─── Trace payload validator ──────────────────────────────────────────────────

// TracePayload is the shape of a Glassbox execution trace JSON payload as
// received from external callers or stored in trace export files.
type TracePayload struct {
	// Input is the command/function input parameters.
	Input map[string]interface{} `json:"input"`
	// State is the ledger / contract state snapshot.
	State map[string]interface{} `json:"state"`
	// Events is the ordered list of diagnostic events.
	Events []interface{} `json:"events"`
	// Timestamp is an RFC 3339 / ISO 8601 timestamp.
	Timestamp string `json:"timestamp"`
	// Metadata is an optional extension map.
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// ValidateTracePayload validates a trace payload decoded from external JSON.
// Returns a RuntimeValidationResult with field-path diagnostics.
func ValidateTracePayload(raw map[string]interface{}, mode ValidationMode) RuntimeValidationResult {
	var errs []FieldError

	knownFields := map[string]bool{
		"input": true, "state": true, "events": true,
		"timestamp": true, "metadata": true,
	}

	// Unknown fields (strict only).
	if mode == Strict {
		for k := range raw {
			if !knownFields[k] {
				errs = append(errs, FieldError{
					Path:    "trace." + k,
					Message: fmt.Sprintf("unknown field %q", k),
					Code:    CodeUnknownField,
				})
			}
		}
	}

	// Required: input.
	if v, ok := raw["input"]; !ok {
		errs = append(errs, FieldError{Path: "trace.input", Message: "required field missing", Code: CodeRequiredFieldMissing})
	} else if !isObject(v) {
		errs = append(errs, FieldError{Path: "trace.input", Message: "must be an object", Code: CodeWrongType})
	} else {
		errs = append(errs, validateNoNaNInf(v, "trace.input")...)
	}

	// Required: state.
	if v, ok := raw["state"]; !ok {
		errs = append(errs, FieldError{Path: "trace.state", Message: "required field missing", Code: CodeRequiredFieldMissing})
	} else if !isObject(v) {
		errs = append(errs, FieldError{Path: "trace.state", Message: "must be an object", Code: CodeWrongType})
	} else {
		errs = append(errs, validateNoNaNInf(v, "trace.state")...)
	}

	// Required: events.
	if v, ok := raw["events"]; !ok {
		errs = append(errs, FieldError{Path: "trace.events", Message: "required field missing", Code: CodeRequiredFieldMissing})
	} else if _, isArr := v.([]interface{}); !isArr {
		errs = append(errs, FieldError{Path: "trace.events", Message: "must be an array", Code: CodeWrongType})
	}

	// Required: timestamp.
	if v, ok := raw["timestamp"]; !ok {
		errs = append(errs, FieldError{Path: "trace.timestamp", Message: "required field missing", Code: CodeRequiredFieldMissing})
	} else if ts, isStr := v.(string); !isStr || ts == "" {
		errs = append(errs, FieldError{Path: "trace.timestamp", Message: "must be a non-empty string", Code: CodeWrongType})
	} else if _, err := time.Parse(time.RFC3339, ts); err != nil {
		// Try ISO 8601 with ms (common from JS Date.toISOString).
		if _, err2 := time.Parse("2006-01-02T15:04:05.999Z07:00", ts); err2 != nil {
			errs = append(errs, FieldError{Path: "trace.timestamp", Message: fmt.Sprintf("not a valid RFC 3339 timestamp: %s", ts), Code: CodeInvalidValue})
		}
	}

	// Optional: metadata.
	if v, ok := raw["metadata"]; ok && v != nil {
		if !isObject(v) {
			errs = append(errs, FieldError{Path: "trace.metadata", Message: "must be an object when present", Code: CodeWrongType})
		}
	}

	return RuntimeValidationResult{Valid: len(errs) == 0, Errors: errs}
}

// ─── Audit record validator ───────────────────────────────────────────────────

// ValidateAuditRecord validates an external audit log JSON object.  The audit
// record shape mirrors AuditLogger.SignedAuditLog from the TypeScript side.
func ValidateAuditRecord(raw map[string]interface{}, mode ValidationMode) RuntimeValidationResult {
	var errs []FieldError

	knownFields := map[string]bool{
		"trace": true, "hash": true, "signature": true,
		"algorithm": true, "publicKey": true, "signer": true,
		"hardware_attestation": true,
	}

	if mode == Strict {
		for k := range raw {
			if !knownFields[k] {
				errs = append(errs, FieldError{
					Path:    "audit." + k,
					Message: fmt.Sprintf("unknown field %q", k),
					Code:    CodeUnknownField,
				})
			}
		}
	}

	// Required: trace (object).
	if v, ok := raw["trace"]; !ok {
		errs = append(errs, FieldError{Path: "audit.trace", Message: "required field missing", Code: CodeRequiredFieldMissing})
	} else if m, ok := v.(map[string]interface{}); !ok {
		errs = append(errs, FieldError{Path: "audit.trace", Message: "must be an object", Code: CodeWrongType})
	} else {
		inner := ValidateTracePayload(m, mode)
		errs = append(errs, inner.Errors...)
	}

	// Required: hash (non-empty string, 64 hex chars).
	errs = append(errs, requireNonEmptyString(raw, "audit.hash", "hash")...)
	if h, ok := raw["hash"].(string); ok && h != "" && len(h) != 64 {
		errs = append(errs, FieldError{Path: "audit.hash", Message: "must be a 64-character hex SHA-256 digest", Code: CodeInvalidValue})
	}

	// Required: signature (non-empty string).
	errs = append(errs, requireNonEmptyString(raw, "audit.signature", "signature")...)

	// Required: algorithm.
	if v, ok := raw["algorithm"]; !ok {
		errs = append(errs, FieldError{Path: "audit.algorithm", Message: "required field missing", Code: CodeRequiredFieldMissing})
	} else if s, ok := v.(string); !ok || s == "" {
		errs = append(errs, FieldError{Path: "audit.algorithm", Message: "must be a non-empty string", Code: CodeWrongType})
	} else if s != "Ed25519" && s != "PKCS11-Ed25519" && s != "KMS-Ed25519" {
		errs = append(errs, FieldError{Path: "audit.algorithm", Message: fmt.Sprintf("unsupported algorithm %q; expected one of Ed25519, PKCS11-Ed25519, KMS-Ed25519", s), Code: CodeInvalidEnumValue})
	}

	// Required: publicKey.
	errs = append(errs, requireNonEmptyString(raw, "audit.publicKey", "publicKey")...)

	// Required: signer (object with provider).
	if v, ok := raw["signer"]; !ok {
		errs = append(errs, FieldError{Path: "audit.signer", Message: "required field missing", Code: CodeRequiredFieldMissing})
	} else if m, ok := v.(map[string]interface{}); !ok {
		errs = append(errs, FieldError{Path: "audit.signer", Message: "must be an object", Code: CodeWrongType})
	} else {
		if pv, ok := m["provider"]; !ok {
			errs = append(errs, FieldError{Path: "audit.signer.provider", Message: "required field missing", Code: CodeRequiredFieldMissing})
		} else if _, ok := pv.(string); !ok {
			errs = append(errs, FieldError{Path: "audit.signer.provider", Message: "must be a string", Code: CodeWrongType})
		}
	}

	return RuntimeValidationResult{Valid: len(errs) == 0, Errors: errs}
}

// ─── Session envelope validator ───────────────────────────────────────────────

// ValidateSessionEnvelope validates an external session envelope JSON object.
// Session envelopes are the top-level containers written by `glassbox session save`.
func ValidateSessionEnvelope(raw map[string]interface{}, mode ValidationMode) RuntimeValidationResult {
	var errs []FieldError

	knownFields := map[string]bool{
		"session_id": true, "version": true, "created_at": true,
		"network": true, "tx_hash": true, "status": true,
		"snapshot": true, "trace": true, "metadata": true,
	}

	if mode == Strict {
		for k := range raw {
			if !knownFields[k] {
				errs = append(errs, FieldError{
					Path:    "session." + k,
					Message: fmt.Sprintf("unknown field %q", k),
					Code:    CodeUnknownField,
				})
			}
		}
	}

	// Required: session_id.
	errs = append(errs, requireNonEmptyString(raw, "session.session_id", "session_id")...)

	// Required: version (non-empty string).
	errs = append(errs, requireNonEmptyString(raw, "session.version", "version")...)

	// Required: created_at (RFC 3339 timestamp).
	if v, ok := raw["created_at"]; !ok {
		errs = append(errs, FieldError{Path: "session.created_at", Message: "required field missing", Code: CodeRequiredFieldMissing})
	} else if ts, ok := v.(string); !ok || ts == "" {
		errs = append(errs, FieldError{Path: "session.created_at", Message: "must be a non-empty RFC 3339 timestamp", Code: CodeWrongType})
	} else if _, err := time.Parse(time.RFC3339, ts); err != nil {
		errs = append(errs, FieldError{Path: "session.created_at", Message: fmt.Sprintf("not a valid RFC 3339 timestamp: %s", ts), Code: CodeInvalidValue})
	}

	// Required: network.
	validNetworks := map[string]bool{"mainnet": true, "testnet": true, "futurenet": true}
	if v, ok := raw["network"]; !ok {
		errs = append(errs, FieldError{Path: "session.network", Message: "required field missing", Code: CodeRequiredFieldMissing})
	} else if s, ok := v.(string); !ok || s == "" {
		errs = append(errs, FieldError{Path: "session.network", Message: "must be a non-empty string", Code: CodeWrongType})
	} else if !validNetworks[s] {
		errs = append(errs, FieldError{Path: "session.network", Message: fmt.Sprintf("unknown network %q; expected mainnet, testnet, or futurenet", s), Code: CodeInvalidEnumValue})
	}

	// Required: status.
	validStatuses := map[string]bool{"success": true, "failed": true, "pending": true}
	if v, ok := raw["status"]; !ok {
		errs = append(errs, FieldError{Path: "session.status", Message: "required field missing", Code: CodeRequiredFieldMissing})
	} else if s, ok := v.(string); !ok || s == "" {
		errs = append(errs, FieldError{Path: "session.status", Message: "must be a non-empty string", Code: CodeWrongType})
	} else if !validStatuses[s] {
		errs = append(errs, FieldError{Path: "session.status", Message: fmt.Sprintf("unknown status %q; expected success, failed, or pending", s), Code: CodeInvalidEnumValue})
	}

	// Optional: trace.
	if v, ok := raw["trace"]; ok && v != nil {
		if m, ok := v.(map[string]interface{}); !ok {
			errs = append(errs, FieldError{Path: "session.trace", Message: "must be an object when present", Code: CodeWrongType})
		} else {
			inner := ValidateTracePayload(m, mode)
			errs = append(errs, inner.Errors...)
		}
	}

	return RuntimeValidationResult{Valid: len(errs) == 0, Errors: errs}
}

// ─── Deserialise-and-validate helpers ─────────────────────────────────────────

// UnmarshalAndValidateTrace deserialises raw JSON bytes and validates the
// resulting trace payload.  Returns the parsed map and validation result.
func UnmarshalAndValidateTrace(data []byte, mode ValidationMode) (map[string]interface{}, RuntimeValidationResult) {
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, RuntimeValidationResult{
			Valid: false,
			Errors: []FieldError{{
				Path:    "",
				Message: fmt.Sprintf("invalid JSON: %v", err),
				Code:    CodeWrongType,
			}},
		}
	}
	return raw, ValidateTracePayload(raw, mode)
}

// UnmarshalAndValidateAuditRecord deserialises raw JSON bytes and validates the
// audit record.
func UnmarshalAndValidateAuditRecord(data []byte, mode ValidationMode) (map[string]interface{}, RuntimeValidationResult) {
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, RuntimeValidationResult{
			Valid: false,
			Errors: []FieldError{{
				Path:    "",
				Message: fmt.Sprintf("invalid JSON: %v", err),
				Code:    CodeWrongType,
			}},
		}
	}
	return raw, ValidateAuditRecord(raw, mode)
}

// UnmarshalAndValidateSessionEnvelope deserialises raw JSON bytes and validates
// the session envelope.
func UnmarshalAndValidateSessionEnvelope(data []byte, mode ValidationMode) (map[string]interface{}, RuntimeValidationResult) {
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, RuntimeValidationResult{
			Valid: false,
			Errors: []FieldError{{
				Path:    "",
				Message: fmt.Sprintf("invalid JSON: %v", err),
				Code:    CodeWrongType,
			}},
		}
	}
	return raw, ValidateSessionEnvelope(raw, mode)
}

// ─── Internal helpers ─────────────────────────────────────────────────────────

func isObject(v interface{}) bool {
	_, ok := v.(map[string]interface{})
	return ok
}

// requireNonEmptyString appends a FieldError if the named key is absent or not
// a non-empty string.
func requireNonEmptyString(raw map[string]interface{}, path, key string) []FieldError {
	v, ok := raw[key]
	if !ok {
		return []FieldError{{Path: path, Message: "required field missing", Code: CodeRequiredFieldMissing}}
	}
	s, ok := v.(string)
	if !ok {
		return []FieldError{{Path: path, Message: "must be a string", Code: CodeWrongType}}
	}
	if s == "" {
		return []FieldError{{Path: path, Message: "must not be empty", Code: CodeInvalidValue}}
	}
	return nil
}

// validateNoNaNInf recursively checks a value for NaN or Infinity, which cannot
// be represented in JSON and would corrupt canonical serialisation.
func validateNoNaNInf(v interface{}, path string) []FieldError {
	var errs []FieldError
	switch val := v.(type) {
	case float64:
		if math.IsNaN(val) {
			errs = append(errs, FieldError{Path: path, Message: "NaN cannot be serialised to JSON", Code: CodeInvalidValue})
		} else if math.IsInf(val, 0) {
			errs = append(errs, FieldError{Path: path, Message: "Infinity cannot be serialised to JSON", Code: CodeInvalidValue})
		}
	case map[string]interface{}:
		for k, child := range val {
			childPath := path
			if childPath != "" {
				childPath += "."
			}
			childPath += k
			errs = append(errs, validateNoNaNInf(child, childPath)...)
		}
	case []interface{}:
		for i, child := range val {
			errs = append(errs, validateNoNaNInf(child, fmt.Sprintf("%s[%d]", path, i))...)
		}
	}
	return errs
}

// FormatValidationErrors formats a slice of FieldErrors into a human-readable
// multi-line string suitable for logging or CLI output.
func FormatValidationErrors(errs []FieldError) string {
	if len(errs) == 0 {
		return ""
	}
	var b strings.Builder
	for _, e := range errs {
		fmt.Fprintf(&b, "  [%s] %s — %s\n", e.Code, e.Path, e.Message)
	}
	return strings.TrimRight(b.String(), "\n")
}
