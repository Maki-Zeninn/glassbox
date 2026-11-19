// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package rpc provides access to Stellar Horizon and Soroban RPC endpoints.
// response_validation.go implements structural validation of JSON-RPC responses
// immediately after receipt, before any decoding or replay logic runs.
//
// Design goals:
//   - Fail at the RPC boundary: every response is validated before its fields
//     are used, so callers never encounter nil-pointer panics from missing data.
//   - Preserve forward compatibility: unknown additive fields are always
//     tolerated; only the explicitly required fields are checked.
//   - No credential logging: validation errors carry endpoint and method
//     context only — response body bytes are never included in error messages.
//   - Stable diagnostics: errors wrap ErrRPCInvalidResponse so callers can use
//     errors.Is for reliable programmatic handling.
package rpc

import (
	"github.com/dotandev/glassbox/internal/errors"
)

// jsonrpcVersion is the only version string currently accepted by the Soroban
// RPC.  Responses carrying any other value (or no value) are rejected.
const jsonrpcVersion = "2.0"

// validateEnvelope checks the base JSON-RPC envelope fields that every
// Soroban RPC response must carry.  It must be called before inspecting the
// result or error fields of any response type.
//
// Rules:
//   - "jsonrpc" must equal "2.0".
//   - A well-formed response must contain exactly one of "result" or "error".
//
// Unknown fields in the response are silently tolerated (forward compatibility).
func validateEnvelope(endpoint, method, jsonrpc string, hasResult, hasRPCError bool) error {
	if jsonrpc != jsonrpcVersion {
		return errors.WrapRPCInvalidResponse(endpoint, method,
			"jsonrpc",
			"must be \"2.0\", got \""+jsonrpc+"\"",
		)
	}

	// The JSON-RPC 2.0 spec forbids a response that contains both "result"
	// and "error" simultaneously (§5).  We flag this as a validation failure
	// rather than silently preferring one over the other.
	if hasResult && hasRPCError {
		return errors.WrapRPCInvalidResponse(endpoint, method,
			"result/error",
			"response must not contain both \"result\" and \"error\" fields",
		)
	}

	// A response with neither "result" nor "error" is structurally incomplete.
	if !hasResult && !hasRPCError {
		return errors.WrapRPCInvalidResponse(endpoint, method,
			"result/error",
			"response must contain exactly one of \"result\" or \"error\" fields",
		)
	}

	return nil
}

// validateRPCErrorObject checks that a JSON-RPC error object has the required
// fields. It is called only when the "error" field is present (non-nil).
func validateRPCErrorObject(endpoint, method string, code int, message string) error {
	// Per JSON-RPC 2.0 §5.1 the error code must be an integer; we already
	// have it as int from the struct, so we only need to reject zero-value
	// message strings which indicate the field was omitted.
	if message == "" {
		return errors.WrapRPCInvalidResponse(endpoint, method,
			"error.message",
			"error object must include a non-empty \"message\" field",
		)
	}

	// Code 0 is not a valid JSON-RPC error code; it suggests the field was
	// missing or defaulted.  We warn rather than hard-fail to stay forward-
	// compatible with server extensions.
	_ = code // reserved for future strict-mode enforcement
	return nil
}

// ---- Method-specific result validators ----------------------------------------
// Each validator checks only the minimum set of fields required by the current
// Soroban RPC specification.  They intentionally do not enumerate every field
// in the response so that additive changes on the server side remain accepted.

// validateGetHealthResult verifies the result portion of a getHealth response.
//
// Required fields: "status" (non-empty string).
func validateGetHealthResult(endpoint string, status string) error {
	if status == "" {
		return errors.WrapRPCInvalidResponse(endpoint, "getHealth",
			"result.status",
			"\"status\" field is required and must be a non-empty string",
		)
	}
	return nil
}

// validateGetLatestLedgerResult verifies the result portion of a
// getLatestLedger response.
//
// Required fields: "sequence" (positive integer).
func validateGetLatestLedgerResult(endpoint string, sequence int) error {
	if sequence <= 0 {
		return errors.WrapRPCInvalidResponse(endpoint, "getLatestLedger",
			"result.sequence",
			"\"sequence\" field must be a positive integer",
		)
	}
	return nil
}

// validateGetLedgerEntriesResult verifies the result portion of a
// getLedgerEntries response.
//
// Required fields: "latestLedger" (positive integer).
// The "entries" array may be empty (all keys were missing from the ledger)
// but must be present as a JSON array — the Go decoder gives us a nil slice
// for an absent field and an empty non-nil slice for "entries":[], so we
// distinguish the two.
func validateGetLedgerEntriesResult(endpoint string, latestLedger int, entriesPresent bool) error {
	if latestLedger <= 0 {
		return errors.WrapRPCInvalidResponse(endpoint, "getLedgerEntries",
			"result.latestLedger",
			"\"latestLedger\" field must be a positive integer",
		)
	}

	// entriesPresent false means the field was not marshalled into the struct
	// at all (both absent and explicitly-null JSON values produce this).
	if !entriesPresent {
		return errors.WrapRPCInvalidResponse(endpoint, "getLedgerEntries",
			"result.entries",
			"\"entries\" array is required (may be empty but must be present)",
		)
	}

	return nil
}

// validateLedgerEntryItem verifies a single LedgerEntryResult item returned
// inside getLedgerEntries.
//
// Required: "key" and "xdr" must both be non-empty base64 strings.
func validateLedgerEntryItem(endpoint string, idx int, key, xdr string) error {
	if key == "" {
		return errors.WrapRPCInvalidResponse(endpoint, "getLedgerEntries",
			"result.entries[].key",
			"entry item has empty \"key\" field",
		)
	}
	if xdr == "" {
		return errors.WrapRPCInvalidResponse(endpoint, "getLedgerEntries",
			"result.entries[].xdr",
			"entry item has empty \"xdr\" field",
		)
	}
	_ = idx // available for future per-index diagnostics
	return nil
}

// validateSimulateTransactionResult verifies the result portion of a
// simulateTransaction response.
//
// Soroban's simulateTransaction result is highly version-dependent, so we only
// check fields that have been stable across all deployed protocol versions:
//   - At least one of "minResourceFee" or "transactionData" must be non-empty
//     when the simulation succeeded (i.e. no top-level RPC error object).
func validateSimulateTransactionResult(endpoint, minResourceFee, transactionData string) error {
	if minResourceFee == "" && transactionData == "" {
		return errors.WrapRPCInvalidResponse(endpoint, "simulateTransaction",
			"result.minResourceFee/transactionData",
			"successful simulation response must include at least one of "+
				"\"minResourceFee\" or \"transactionData\"",
		)
	}
	return nil
}

// validateTransactionResponse verifies the fields of a TransactionResponse
// after it has been populated from the Horizon API.  All three XDR fields are
// required for a complete on-chain transaction record.
func validateTransactionResponse(endpoint, hash string, resp *TransactionResponse) error {
	if resp == nil {
		return errors.WrapRPCInvalidResponse(endpoint, "getTransaction",
			"",
			"transaction response is nil",
		)
	}
	if resp.EnvelopeXdr == "" {
		return errors.WrapRPCInvalidResponse(endpoint, "getTransaction",
			"envelopeXdr",
			"\"envelopeXdr\" field is required and must be a non-empty base64 string",
		)
	}
	if resp.ResultXdr == "" {
		return errors.WrapRPCInvalidResponse(endpoint, "getTransaction",
			"resultXdr",
			"\"resultXdr\" field is required and must be a non-empty base64 string",
		)
	}
	if resp.ResultMetaXdr == "" {
		return errors.WrapRPCInvalidResponse(endpoint, "getTransaction",
			"resultMetaXdr",
			"\"resultMetaXdr\" field is required and must be a non-empty base64 string",
		)
	}
	_ = hash // available for future per-hash diagnostics
	return nil
}

// ValidateGetHealthResponse performs full structural validation of a
// GetHealthResponse, covering the JSON-RPC envelope and the result object.
// It returns the first validation error encountered, or nil if the response
// is well-formed.
//
// The caller is responsible for ensuring that rpcResp was obtained from a
// successful json.Unmarshal before calling this function.
func ValidateGetHealthResponse(endpoint string, rpcResp *GetHealthResponse) error {
	if rpcResp == nil {
		return errors.WrapRPCInvalidResponse(endpoint, "getHealth", "", "response is nil")
	}

	hasResult := rpcResp.Result.Status != "" ||
		rpcResp.Result.LatestLedger != 0 ||
		rpcResp.Result.OldestLedger != 0
	hasRPCError := rpcResp.Error != nil

	if err := validateEnvelope(endpoint, "getHealth", rpcResp.Jsonrpc, hasResult, hasRPCError); err != nil {
		return err
	}

	if hasRPCError {
		return validateRPCErrorObject(endpoint, "getHealth", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	return validateGetHealthResult(endpoint, rpcResp.Result.Status)
}

// ValidateGetLatestLedgerResponse performs full structural validation of a
// GetLatestLedgerResponse.
func ValidateGetLatestLedgerResponse(endpoint string, rpcResp *GetLatestLedgerResponse) error {
	if rpcResp == nil {
		return errors.WrapRPCInvalidResponse(endpoint, "getLatestLedger", "", "response is nil")
	}

	hasResult := rpcResp.Result.Sequence != 0
	hasRPCError := rpcResp.Error != nil

	if err := validateEnvelope(endpoint, "getLatestLedger", rpcResp.Jsonrpc, hasResult, hasRPCError); err != nil {
		return err
	}

	if hasRPCError {
		return validateRPCErrorObject(endpoint, "getLatestLedger", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	return validateGetLatestLedgerResult(endpoint, rpcResp.Result.Sequence)
}

// ValidateGetLedgerEntriesResponse performs full structural validation of a
// GetLedgerEntriesResponse, including each returned entry item.
func ValidateGetLedgerEntriesResponse(endpoint string, rpcResp *GetLedgerEntriesResponse) error {
	if rpcResp == nil {
		return errors.WrapRPCInvalidResponse(endpoint, "getLedgerEntries", "", "response is nil")
	}

	// Entries field is present when the slice is non-nil (including empty).
	// The JSON decoder leaves it nil when the field is absent from the payload.
	entriesPresent := rpcResp.Result.Entries != nil
	hasResult := rpcResp.Result.LatestLedger != 0 || entriesPresent
	hasRPCError := rpcResp.Error != nil

	if err := validateEnvelope(endpoint, "getLedgerEntries", rpcResp.Jsonrpc, hasResult, hasRPCError); err != nil {
		return err
	}

	if hasRPCError {
		return validateRPCErrorObject(endpoint, "getLedgerEntries", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	if err := validateGetLedgerEntriesResult(endpoint, rpcResp.Result.LatestLedger, entriesPresent); err != nil {
		return err
	}

	for i, entry := range rpcResp.Result.Entries {
		if err := validateLedgerEntryItem(endpoint, i, entry.Key, entry.Xdr); err != nil {
			return err
		}
	}

	return nil
}

// ValidateSimulateTransactionResponse performs full structural validation of a
// SimulateTransactionResponse.
func ValidateSimulateTransactionResponse(endpoint string, rpcResp *SimulateTransactionResponse) error {
	if rpcResp == nil {
		return errors.WrapRPCInvalidResponse(endpoint, "simulateTransaction", "", "response is nil")
	}

	hasResult := rpcResp.Result.MinResourceFee != "" || rpcResp.Result.TransactionData != ""
	hasRPCError := rpcResp.Error != nil

	if err := validateEnvelope(endpoint, "simulateTransaction", rpcResp.Jsonrpc, hasResult, hasRPCError); err != nil {
		return err
	}

	if hasRPCError {
		return validateRPCErrorObject(endpoint, "simulateTransaction", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	return validateSimulateTransactionResult(endpoint, rpcResp.Result.MinResourceFee, rpcResp.Result.TransactionData)
}

// ValidateTransactionResponse performs structural validation of a
// TransactionResponse returned by the Horizon API.
func ValidateTransactionResponse(endpoint, hash string, resp *TransactionResponse) error {
	return validateTransactionResponse(endpoint, hash, resp)
}
