// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package ipc

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dotandev/glassbox/internal/errors"
)

// ToErstError converts an IPC Error from the Rust simulator into the unified ErstError type.
// The original Code and Message strings are preserved in OrigErr.
// Note: the Rust simulator currently emits plain message strings without structured codes,
// so classification falls back to message-based heuristics via classifyByMessage.
func (e *Error) ToErstError() *errors.ErstError {
	code := mapIPCCode(e.Code)
	if code == errors.CodeUnknown {
		code = classifyByMessage(e.Message)
	}
	return errors.NewSimError(code, fmt.Errorf("%s: %s", e.Code, e.Message))
}

// mapIPCCode translates structured IPC error code strings from the Rust simulator
// into the unified ErstErrorCode classification.
// Currently the Rust simulator does not emit structured codes, so this will
// return CodeUnknown in most cases and ToErstError will fall back to classifyByMessage.
func mapIPCCode(raw string) errors.ErstErrorCode {
	switch strings.ToUpper(raw) {
	case "SIMULATION_FAILED", "EXECUTION_FAILED":
		return errors.CodeSimExecFailed
	case "WASM_TRAP", "CONTRACT_TRAP":
		return errors.CodeSimCrash
	case "INVALID_INPUT", "VALIDATION_ERROR":
		return errors.CodeValidationFailed
	case "PROTOCOL_UNSUPPORTED":
		return errors.CodeSimProtoUnsup
	case "ERR_MEMORY_LIMIT_EXCEEDED", "MEMORY_LIMIT_EXCEEDED":
		return errors.CodeSimMemoryLimitExceeded
	default:
		return errors.CodeUnknown
	}
}

// classifyByMessage inspects the raw error message from the Rust simulator
// and maps it to the best-matching ErstErrorCode.
// This is a fallback for when the simulator does not emit a structured code field.
func classifyByMessage(msg string) errors.ErstErrorCode {
	switch {
	case strings.Contains(msg, "decode Envelope"),
		strings.Contains(msg, "decode LedgerKey"),
		strings.Contains(msg, "decode LedgerEntry"),
		strings.Contains(msg, "decode WASM"):
		return errors.CodeRPCUnmarshalFailed
	case strings.Contains(msg, "Wasm Trap"),
		strings.Contains(msg, "wasm trap"),
		strings.Contains(msg, "unreachable"),
		strings.Contains(msg, "stack overflow"),
		strings.Contains(msg, "out of bounds"):
		return errors.CodeSimCrash
	case strings.Contains(strings.ToLower(msg), "err_memory_limit_exceeded"),
		strings.Contains(strings.ToLower(msg), "memory limit exceeded"):
		return errors.CodeSimMemoryLimitExceeded
	case strings.Contains(msg, "InvalidInput"):
		return errors.CodeValidationFailed
	default:
		return errors.CodeSimExecFailed
	}
}

func UnmarshalSimulationRequestSchema(data []byte) (SimulationRequestSchema, error) {
	var r SimulationRequestSchema
	err := json.Unmarshal(data, &r)
	return r, err
}

func (r *SimulationRequestSchema) Marshal() ([]byte, error) {
	return json.Marshal(r)
}

func UnmarshalSimulationResponseSchema(data []byte) (SimulationResponseSchema, error) {
	var r SimulationResponseSchema
	err := json.Unmarshal(data, &r)
	return r, err
}

func (r *SimulationResponseSchema) Marshal() ([]byte, error) {
	return json.Marshal(r)
}

type SimulationRequestSchema struct {
	Network Network `json:"network"`
	// Client-generated unique request identifier
	RequestID string `json:"request_id"`
	Version   string `json:"version"`
	Xdr       string `json:"xdr"`
}

type SimulationResponseSchema struct {
	Error     *Error            `json:"error,omitempty"`
	RequestID string            `json:"request_id"`
	Result    *Result           `json:"result,omitempty"`
	Snapshots *SnapshotsPayload `json:"snapshots,omitempty"`
	Success   bool              `json:"success"`
	Version   string            `json:"version"`
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Result struct {
	// Fee charged in stroops
	FeeCharged string `json:"fee_charged"`
}

// SnapshotsPayload carries optional simulator snapshot data alongside the
// standard simulation response. Implementations may either embed snapshot data
// directly in Inline or return IDs that can be resolved lazily out-of-band.
// A response may include either representation or both.
type SnapshotsPayload struct {
	// Inline contains fully materialized snapshots keyed by snapshot ID.
	Inline map[string]InlineSnapshot `json:"inline,omitempty"`
	// IDs lists snapshot identifiers that can be resolved lazily by the caller.
	IDs []string `json:"ids,omitempty"`
}

// InlineSnapshot mirrors the bridge-friendly subset of the on-disk snapshot
// format so future bridge implementations can round-trip simulator state
// without inferring field semantics from CLI-specific code paths.
type InlineSnapshot struct {
	// LedgerEntries is a list of key/value XDR pairs encoded as base64 strings.
	LedgerEntries [][]string `json:"ledger_entries,omitempty"`
	// LinearMemory is an optional base64-encoded Wasm memory dump.
	LinearMemory string `json:"linear_memory,omitempty"`
}

type Network string

const (
	Futurenet Network = "futurenet"
	Public    Network = "public"
	Testnet   Network = "testnet"
)

// HandshakeRequest is sent to the simulator before any transaction is executed.
// It communicates the Go-side protocol version and the feature set that must be
// present for the session to proceed.
type HandshakeRequest struct {
	// Type is always "handshake" — used to distinguish this from a simulation request.
	Type string `json:"type"`
	// ProtocolVersion is the Stellar protocol version the client expects to run.
	ProtocolVersion uint32 `json:"protocol_version"`
	// ClientVersion is the semantic version of the Go-side client (e.g. "1.2.3").
	// The simulator uses this to select compatible behaviour when multiple
	// client versions are in the field.
	ClientVersion string `json:"client_version,omitempty"`
	// RequiredFeatures lists capability identifiers that must appear in the
	// simulator's SupportedFeatures response. The session is aborted if any are absent.
	RequiredFeatures []string `json:"required_features,omitempty"`
	// MaxRequestBytes is the largest IPC request payload the client will send.
	// The simulator may reject the session if it cannot accept payloads that large.
	MaxRequestBytes int64 `json:"max_request_bytes,omitempty"`
}

// HandshakeResponse is the simulator's reply to a HandshakeRequest.
type HandshakeResponse struct {
	// Type is always "handshake_ack".
	Type string `json:"type"`
	// SimulatorBuild is an opaque build identifier (e.g. git SHA or release tag).
	SimulatorBuild string `json:"simulator_build"`
	// ProtocolVersion is the simulator's native Stellar protocol version.
	ProtocolVersion uint32 `json:"protocol_version"`
	// MinClientVersion is the oldest client version the simulator still supports.
	// When the client's version is older than this, the session must not proceed.
	MinClientVersion string `json:"min_client_version,omitempty"`
	// MaxClientVersion is the newest client version the simulator supports.
	// When the client's version is newer than this, the session must not proceed.
	MaxClientVersion string `json:"max_client_version,omitempty"`
	// SupportedFeatures lists capability identifiers available in this build.
	SupportedFeatures []string `json:"supported_features"`
	// MaxRequestBytes is the largest IPC request payload the simulator accepts.
	MaxRequestBytes int64 `json:"max_request_bytes,omitempty"`
	// Error is non-empty when the simulator rejects the handshake.
	Error string `json:"error,omitempty"`
	// ErrorCode provides a machine-readable reason for the rejection.
	// Known values: "incompatible_version", "missing_capability", "payload_too_large".
	ErrorCode string `json:"error_code,omitempty"`
}

// HandshakeRequestType and HandshakeResponseType are the fixed JSON type tags.
const (
	HandshakeRequestType  = "handshake"
	HandshakeResponseType = "handshake_ack"
)

func UnmarshalHandshakeRequest(data []byte) (HandshakeRequest, error) {
	var r HandshakeRequest
	err := json.Unmarshal(data, &r)
	return r, err
}

func (r *HandshakeRequest) Marshal() ([]byte, error) {
	return json.Marshal(r)
}

func UnmarshalHandshakeResponse(data []byte) (HandshakeResponse, error) {
	var r HandshakeResponse
	err := json.Unmarshal(data, &r)
	return r, err
}

func (r *HandshakeResponse) Marshal() ([]byte, error) {
	return json.Marshal(r)
}
