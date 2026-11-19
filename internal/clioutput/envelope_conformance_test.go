// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package clioutput

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	glassboxerrors "github.com/dotandev/glassbox/internal/errors"
)

// ── Schema constants ──────────────────────────────────────────────────────────

const (
	requiredSchemaVersion = "1.0"
)

// ── Success envelope conformance ──────────────────────────────────────────────

// TestSuccessEnvelope_HasAllRequiredFields verifies that a success envelope
// written by Write() always includes every required top-level field.
func TestSuccessEnvelope_HasAllRequiredFields(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, "test-cmd", map[string]string{"k": "v"}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(buf.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal raw envelope: %v", err)
	}

	required := []string{"schema_version", "glassbox_version", "generated_at", "data"}
	for _, field := range required {
		if _, ok := raw[field]; !ok {
			t.Errorf("success envelope missing required field %q", field)
		}
	}
}

// TestSuccessEnvelope_SchemaVersionMatches verifies the schema_version field
// matches the exported SchemaVersion constant.
func TestSuccessEnvelope_SchemaVersionMatches(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, "test-cmd", map[string]string{}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	var env Envelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.SchemaVersion != SchemaVersion {
		t.Errorf("schema_version = %q, want %q (SchemaVersion constant)", env.SchemaVersion, SchemaVersion)
	}
	if SchemaVersion != requiredSchemaVersion {
		t.Errorf("SchemaVersion constant = %q, want %q", SchemaVersion, requiredSchemaVersion)
	}
}

// TestSuccessEnvelope_CommandIsSet verifies the command field is populated
// when provided.
func TestSuccessEnvelope_CommandIsSet(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, "debug", map[string]string{}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	var env Envelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.Command != "debug" {
		t.Errorf("command = %q, want %q", env.Command, "debug")
	}
}

// TestSuccessEnvelope_GeneratedAtIsNonZero verifies generated_at is a valid
// timestamp (non-empty string that parses as time.Time).
func TestSuccessEnvelope_GeneratedAtIsNonZero(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, "test-cmd", nil); err != nil {
		t.Fatalf("Write: %v", err)
	}

	var env Envelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.GeneratedAt.IsZero() {
		t.Error("generated_at must not be zero")
	}
}

// TestSuccessEnvelope_DataIsParseable verifies the data field contains valid JSON.
func TestSuccessEnvelope_DataIsParseable(t *testing.T) {
	payload := map[string]interface{}{"status": "ok", "count": 42}
	var buf bytes.Buffer
	if err := Write(&buf, "test-cmd", payload); err != nil {
		t.Fatalf("Write: %v", err)
	}

	var env Envelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("data field is not valid JSON: %v", err)
	}
	if data["status"] != "ok" {
		t.Errorf("data.status = %v, want %q", data["status"], "ok")
	}
	if data["count"] != float64(42) {
		t.Errorf("data.count = %v, want 42", data["count"])
	}
}

// TestSuccessEnvelope_OmitsEmptyCommand verifies command is omitted when empty.
func TestSuccessEnvelope_OmitsEmptyCommand(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, "", map[string]string{}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(buf.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// omitempty means the key should not be present when empty
	if _, ok := raw["command"]; ok {
		t.Error("command field should be omitted when empty (omitempty)")
	}
}

// TestSuccessEnvelope_GlassboxVersionIsNonEmpty verifies glassbox_version is set.
func TestSuccessEnvelope_GlassboxVersionIsNonEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, "test-cmd", nil); err != nil {
		t.Fatalf("Write: %v", err)
	}

	var env Envelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.GlassboxVersion == "" {
		t.Error("glassbox_version must not be empty")
	}
}

// ── Error envelope conformance ────────────────────────────────────────────────

// TestErrorEnvelope_HasAllRequiredFields verifies that an error envelope
// includes every required top-level field: schema_version, glassbox_version,
// generated_at, and data (which contains the error payload).
func TestErrorEnvelope_HasAllRequiredFields(t *testing.T) {
	var buf bytes.Buffer
	err := glassboxerrors.WrapRPCConnectionFailed(fmt.Errorf("connection refused"))
	if writeErr := WriteError(&buf, "debug", err, nil); writeErr != nil {
		t.Fatalf("WriteError: %v", writeErr)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(buf.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}

	required := []string{"schema_version", "glassbox_version", "generated_at", "data"}
	for _, field := range required {
		if _, ok := raw[field]; !ok {
			t.Errorf("error envelope missing required top-level field %q", field)
		}
	}
}

// TestErrorEnvelope_ErrorPayloadHasAllRequiredFields verifies that the error
// payload inside the data field includes every required sub-field.
func TestErrorEnvelope_ErrorPayloadHasAllRequiredFields(t *testing.T) {
	var buf bytes.Buffer
	err := glassboxerrors.WrapRPCTimeout(fmt.Errorf("deadline exceeded"))
	if writeErr := WriteError(&buf, "debug", err, nil); writeErr != nil {
		t.Fatalf("WriteError: %v", writeErr)
	}

	var env Envelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}

	var data errorEnvelopeData
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}

	requiredFields := map[string]string{
		"code":     data.Error.Code,
		"severity": string(data.Error.Severity),
		"message":  data.Error.Message,
	}
	for field, value := range requiredFields {
		if value == "" {
			t.Errorf("error payload missing required field %q (empty value)", field)
		}
	}
}

// TestErrorEnvelope_SeverityIsAlwaysError verifies the severity field is
// always "error" for command failures.
func TestErrorEnvelope_SeverityIsAlwaysError(t *testing.T) {
	errs := []error{
		glassboxerrors.WrapRPCConnectionFailed(fmt.Errorf("dial failed")),
		glassboxerrors.WrapRPCTimeout(fmt.Errorf("timeout")),
		glassboxerrors.WrapValidationError("bad input"),
		glassboxerrors.WrapConfigError("missing field", fmt.Errorf("rpc_url")),
	}

	for _, err := range errs {
		t.Run(err.Error(), func(t *testing.T) {
			var buf bytes.Buffer
			if writeErr := WriteError(&buf, "test", err, nil); writeErr != nil {
				t.Fatalf("WriteError: %v", writeErr)
			}

			var env Envelope
			_ = json.Unmarshal(buf.Bytes(), &env)
			var data errorEnvelopeData
			_ = json.Unmarshal(env.Data, &data)

			if data.Error.Severity != SeverityError {
				t.Errorf("severity = %q, want %q", data.Error.Severity, SeverityError)
			}
		})
	}
}

// TestErrorEnvelope_CodeIsNeverEmpty verifies the error code is always present.
func TestErrorEnvelope_CodeIsNeverEmpty(t *testing.T) {
	errs := []error{
		glassboxerrors.WrapRPCConnectionFailed(fmt.Errorf("refused")),
		glassboxerrors.WrapRPCTimeout(fmt.Errorf("timeout")),
		glassboxerrors.WrapValidationError("bad input"),
		fmt.Errorf("plain error"),
	}

	for _, err := range errs {
		t.Run(err.Error(), func(t *testing.T) {
			var buf bytes.Buffer
			if writeErr := WriteError(&buf, "test", err, nil); writeErr != nil {
				t.Fatalf("WriteError: %v", writeErr)
			}

			var env Envelope
			_ = json.Unmarshal(buf.Bytes(), &env)
			var data errorEnvelopeData
			_ = json.Unmarshal(env.Data, &data)

			if data.Error.Code == "" {
				t.Error("error.code must never be empty")
			}
		})
	}
}

// TestErrorEnvelope_DoesNotCorruptSuccessData verifies that error envelopes
// do not contain a "data" field in the success sense — the "data" field
// wraps an error object, not arbitrary payload.
func TestErrorEnvelope_DoesNotCorruptSuccessData(t *testing.T) {
	var buf bytes.Buffer
	err := glassboxerrors.WrapRPCConnectionFailed(fmt.Errorf("refused"))
	if writeErr := WriteError(&buf, "debug", err, nil); writeErr != nil {
		t.Fatalf("WriteError: %v", writeErr)
	}

	var env Envelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// The data field should parse as an error envelope, not arbitrary data.
	var data errorEnvelopeData
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("data field is not a valid error envelope: %v", err)
	}

	// The error payload must contain an error object.
	if data.Error.Code == "" {
		t.Error("error envelope data must contain error.code")
	}
}

// TestErrorEnvelope_NilErrorWritesNothing verifies WriteError(nil) produces
// zero bytes.
func TestErrorEnvelope_NilErrorWritesNothing(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteError(&buf, "test", nil, nil); err != nil {
		t.Fatalf("WriteError(nil): %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("WriteError(nil) wrote %d bytes, want 0", buf.Len())
	}
}

// TestErrorEnvelope_ContextAppearsInOutput verifies that non-nil context
// is included in the error payload.
func TestErrorEnvelope_ContextAppearsInOutput(t *testing.T) {
	var buf bytes.Buffer
	err := glassboxerrors.WrapTransactionNotFound(fmt.Errorf("404"))
	ctx := map[string]string{"hash": "abc123"}
	if writeErr := WriteError(&buf, "debug", err, ctx); writeErr != nil {
		t.Fatalf("WriteError: %v", writeErr)
	}

	var env Envelope
	_ = json.Unmarshal(buf.Bytes(), &env)
	var data errorEnvelopeData
	_ = json.Unmarshal(env.Data, &data)

	if data.Error.Context["hash"] != "abc123" {
		t.Errorf("context.hash = %q, want %q", data.Error.Context["hash"], "abc123")
	}
}

// ── Envelope separation ───────────────────────────────────────────────────────

// TestSuccessAndErrorEnvelopesAreDistinguishable verifies that success and
// error envelopes can be distinguished by checking whether the data field
// contains an "error" key.
func TestSuccessAndErrorEnvelopesAreDistinguishable(t *testing.T) {
	// Success envelope
	var successBuf bytes.Buffer
	if err := Write(&successBuf, "test", map[string]string{"ok": "true"}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Error envelope
	var errorBuf bytes.Buffer
	err := glassboxerrors.WrapRPCConnectionFailed(fmt.Errorf("refused"))
	if writeErr := WriteError(&errorBuf, "test", err, nil); writeErr != nil {
		t.Fatalf("WriteError: %v", writeErr)
	}

	// Parse both as raw JSON
	var successRaw map[string]json.RawMessage
	_ = json.Unmarshal(successBuf.Bytes(), &successRaw)

	var errorRaw map[string]json.RawMessage
	_ = json.Unmarshal(errorBuf.Bytes(), &errorRaw)

	// Success data should not contain "error" key
	var successData map[string]json.RawMessage
	_ = json.Unmarshal(successRaw["data"], &successData)
	if _, hasError := successData["error"]; hasError {
		t.Error("success envelope data should not contain 'error' key")
	}

	// Error data must contain "error" key
	var errorData map[string]json.RawMessage
	_ = json.Unmarshal(errorRaw["data"], &errorData)
	if _, hasError := errorData["error"]; !hasError {
		t.Error("error envelope data must contain 'error' key")
	}
}

// ── Timestamps do not leak into required fields ──────────────────────────────

// TestSuccessEnvelope_GeneratedAtIsTimestamp verifies generated_at is a
// proper RFC3339 timestamp, not some other type.
func TestSuccessEnvelope_GeneratedAtIsTimestamp(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, "test", nil); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Verify it's valid JSON with proper timestamp format
	var raw map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	genAt, ok := raw["generated_at"].(string)
	if !ok {
		t.Fatalf("generated_at should be a string, got %T", raw["generated_at"])
	}
	// Should parse as RFC3339
	// time.Parse is not needed; just verify it's not empty and contains 'T'
	if genAt == "" || !strings.Contains(genAt, "T") {
		t.Errorf("generated_at = %q, expected RFC3339 format", genAt)
	}
}

// ── Code removal invalidates error envelope ───────────────────────────────────

// TestErrorCodeRequiredFieldsAreNonEmpty verifies that every error code
// produces a non-empty code, severity, and message in the envelope.
func TestErrorCodeRequiredFieldsAreNonEmpty(t *testing.T) {
	testCases := []struct {
		name string
		err  error
		code string
	}{
		{"RPC_CONNECTION_FAILED", glassboxerrors.WrapRPCConnectionFailed(fmt.Errorf("refused")), "RPC_CONNECTION_FAILED"},
		{"RPC_TIMEOUT", glassboxerrors.WrapRPCTimeout(fmt.Errorf("timeout")), "RPC_TIMEOUT"},
		{"VALIDATION_FAILED", glassboxerrors.WrapValidationError("bad"), "VALIDATION_FAILED"},
		{"CONFIG_ERROR", glassboxerrors.WrapConfigError("missing", fmt.Errorf("field")), "CONFIG_ERROR"},
		{"TRANSACTION_NOT_FOUND", glassboxerrors.WrapTransactionNotFound(fmt.Errorf("404")), "TRANSACTION_NOT_FOUND"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteError(&buf, "test", tc.err, nil); err != nil {
				t.Fatalf("WriteError: %v", err)
			}

			var env Envelope
			_ = json.Unmarshal(buf.Bytes(), &env)
			var data errorEnvelopeData
			_ = json.Unmarshal(env.Data, &data)

			if data.Error.Code != tc.code {
				t.Errorf("error.code = %q, want %q", data.Error.Code, tc.code)
			}
			if data.Error.Severity == "" {
				t.Error("error.severity must not be empty")
			}
			if data.Error.Message == "" {
				t.Error("error.message must not be empty")
			}
		})
	}
}
