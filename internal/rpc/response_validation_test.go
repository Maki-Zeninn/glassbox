// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package rpc

import (
	"errors"
	"testing"

	glassboxerrors "github.com/dotandev/glassbox/internal/errors"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func requireInvalidResponse(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected ErrRPCInvalidResponse, got nil")
	}
	if !errors.Is(err, glassboxerrors.ErrRPCInvalidResponse) {
		t.Fatalf("expected ErrRPCInvalidResponse, got %T: %v", err, err)
	}
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ── validateEnvelope ─────────────────────────────────────────────────────────

func TestValidateEnvelope_ValidSuccessResponse(t *testing.T) {
	err := validateEnvelope("https://rpc.example.com", "getHealth", "2.0", true, false)
	requireNoError(t, err)
}

func TestValidateEnvelope_ValidErrorResponse(t *testing.T) {
	err := validateEnvelope("https://rpc.example.com", "getHealth", "2.0", false, true)
	requireNoError(t, err)
}

func TestValidateEnvelope_WrongVersion(t *testing.T) {
	err := validateEnvelope("https://rpc.example.com", "getHealth", "1.0", true, false)
	requireInvalidResponse(t, err)
}

func TestValidateEnvelope_EmptyVersion(t *testing.T) {
	err := validateEnvelope("https://rpc.example.com", "getHealth", "", true, false)
	requireInvalidResponse(t, err)
}

func TestValidateEnvelope_BothResultAndError(t *testing.T) {
	err := validateEnvelope("https://rpc.example.com", "getHealth", "2.0", true, true)
	requireInvalidResponse(t, err)
}

func TestValidateEnvelope_NeitherResultNorError(t *testing.T) {
	// Completely truncated / empty body that decoded to zero values.
	err := validateEnvelope("https://rpc.example.com", "getHealth", "2.0", false, false)
	requireInvalidResponse(t, err)
}

func TestValidateEnvelope_ErrorMessageContainsEndpointAndMethod(t *testing.T) {
	err := validateEnvelope("https://rpc.test", "myMethod", "1.0", true, false)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	for _, want := range []string{"https://rpc.test", "myMethod", "jsonrpc"} {
		if !containsStr(msg, want) {
			t.Errorf("error message %q missing %q", msg, want)
		}
	}
}

// ── validateRPCErrorObject ────────────────────────────────────────────────────

func TestValidateRPCErrorObject_ValidError(t *testing.T) {
	err := validateRPCErrorObject("https://rpc.example.com", "getHealth", -32600, "Invalid Request")
	requireNoError(t, err)
}

func TestValidateRPCErrorObject_EmptyMessage(t *testing.T) {
	// The JSON-RPC error object must carry a non-empty message field.
	err := validateRPCErrorObject("https://rpc.example.com", "getHealth", -32600, "")
	requireInvalidResponse(t, err)
}

// ── ValidateGetHealthResponse ────────────────────────────────────────────────

func TestValidateGetHealthResponse_ValidHealthy(t *testing.T) {
	resp := &GetHealthResponse{
		Jsonrpc: "2.0",
		Result: struct {
			Status                string `json:"status"`
			LatestLedger          uint32 `json:"latestLedger"`
			OldestLedger          uint32 `json:"oldestLedger"`
			LedgerRetentionWindow uint32 `json:"ledgerRetentionWindow"`
		}{
			Status:       "healthy",
			LatestLedger: 1000,
		},
	}
	requireNoError(t, ValidateGetHealthResponse("https://rpc.example.com", resp))
}

func TestValidateGetHealthResponse_ValidErrorEnvelope(t *testing.T) {
	resp := &GetHealthResponse{
		Jsonrpc: "2.0",
		Error: &struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}{Code: -32603, Message: "Internal error"},
	}
	requireNoError(t, ValidateGetHealthResponse("https://rpc.example.com", resp))
}

func TestValidateGetHealthResponse_NilResponse(t *testing.T) {
	requireInvalidResponse(t, ValidateGetHealthResponse("https://rpc.example.com", nil))
}

func TestValidateGetHealthResponse_WrongVersion(t *testing.T) {
	resp := &GetHealthResponse{
		Jsonrpc: "1.0",
		Result: struct {
			Status                string `json:"status"`
			LatestLedger          uint32 `json:"latestLedger"`
			OldestLedger          uint32 `json:"oldestLedger"`
			LedgerRetentionWindow uint32 `json:"ledgerRetentionWindow"`
		}{Status: "healthy"},
	}
	requireInvalidResponse(t, ValidateGetHealthResponse("https://rpc.example.com", resp))
}

func TestValidateGetHealthResponse_EmptyStatus(t *testing.T) {
	// Truncated response: jsonrpc is correct but result.status is missing/null.
	resp := &GetHealthResponse{
		Jsonrpc: "2.0",
		Result: struct {
			Status                string `json:"status"`
			LatestLedger          uint32 `json:"latestLedger"`
			OldestLedger          uint32 `json:"oldestLedger"`
			LedgerRetentionWindow uint32 `json:"ledgerRetentionWindow"`
		}{
			// Status intentionally left empty.
			LatestLedger: 100,
		},
	}
	requireInvalidResponse(t, ValidateGetHealthResponse("https://rpc.example.com", resp))
}

func TestValidateGetHealthResponse_ErrorWithEmptyMessage(t *testing.T) {
	// Error envelope with missing message field.
	resp := &GetHealthResponse{
		Jsonrpc: "2.0",
		Error: &struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}{Code: -32603, Message: ""},
	}
	requireInvalidResponse(t, ValidateGetHealthResponse("https://rpc.example.com", resp))
}

func TestValidateGetHealthResponse_BothResultAndError(t *testing.T) {
	resp := &GetHealthResponse{
		Jsonrpc: "2.0",
		Result: struct {
			Status                string `json:"status"`
			LatestLedger          uint32 `json:"latestLedger"`
			OldestLedger          uint32 `json:"oldestLedger"`
			LedgerRetentionWindow uint32 `json:"ledgerRetentionWindow"`
		}{Status: "healthy"},
		Error: &struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}{Code: -32603, Message: "oops"},
	}
	requireInvalidResponse(t, ValidateGetHealthResponse("https://rpc.example.com", resp))
}

// ── ValidateGetLatestLedgerResponse ──────────────────────────────────────────

func TestValidateGetLatestLedgerResponse_Valid(t *testing.T) {
	resp := &GetLatestLedgerResponse{
		Jsonrpc: "2.0",
		Result: struct {
			ID          string `json:"id"`
			Sequence    int    `json:"sequence"`
			CloseTime   string `json:"closeTime"`
			HeaderXdr   string `json:"headerXdr"`
			MetadataXdr string `json:"metadataXdr"`
		}{Sequence: 42},
	}
	requireNoError(t, ValidateGetLatestLedgerResponse("https://rpc.example.com", resp))
}

func TestValidateGetLatestLedgerResponse_NilResponse(t *testing.T) {
	requireInvalidResponse(t, ValidateGetLatestLedgerResponse("https://rpc.example.com", nil))
}

func TestValidateGetLatestLedgerResponse_ZeroSequence(t *testing.T) {
	// Zero sequence can happen on a truncated or null result field.
	resp := &GetLatestLedgerResponse{
		Jsonrpc: "2.0",
		Result: struct {
			ID          string `json:"id"`
			Sequence    int    `json:"sequence"`
			CloseTime   string `json:"closeTime"`
			HeaderXdr   string `json:"headerXdr"`
			MetadataXdr string `json:"metadataXdr"`
		}{Sequence: 0},
	}
	requireInvalidResponse(t, ValidateGetLatestLedgerResponse("https://rpc.example.com", resp))
}

func TestValidateGetLatestLedgerResponse_NegativeSequence(t *testing.T) {
	resp := &GetLatestLedgerResponse{
		Jsonrpc: "2.0",
		Result: struct {
			ID          string `json:"id"`
			Sequence    int    `json:"sequence"`
			CloseTime   string `json:"closeTime"`
			HeaderXdr   string `json:"headerXdr"`
			MetadataXdr string `json:"metadataXdr"`
		}{Sequence: -1},
	}
	requireInvalidResponse(t, ValidateGetLatestLedgerResponse("https://rpc.example.com", resp))
}

func TestValidateGetLatestLedgerResponse_WrongVersion(t *testing.T) {
	resp := &GetLatestLedgerResponse{
		Jsonrpc: "3.0",
		Result: struct {
			ID          string `json:"id"`
			Sequence    int    `json:"sequence"`
			CloseTime   string `json:"closeTime"`
			HeaderXdr   string `json:"headerXdr"`
			MetadataXdr string `json:"metadataXdr"`
		}{Sequence: 10},
	}
	requireInvalidResponse(t, ValidateGetLatestLedgerResponse("https://rpc.example.com", resp))
}

func TestValidateGetLatestLedgerResponse_ValidErrorEnvelope(t *testing.T) {
	resp := &GetLatestLedgerResponse{
		Jsonrpc: "2.0",
		Error: &struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}{Code: -32601, Message: "Method not found"},
	}
	requireNoError(t, ValidateGetLatestLedgerResponse("https://rpc.example.com", resp))
}

// ── ValidateGetLedgerEntriesResponse ─────────────────────────────────────────

func TestValidateGetLedgerEntriesResponse_ValidWithEntries(t *testing.T) {
	resp := &GetLedgerEntriesResponse{
		Jsonrpc: "2.0",
		Result: struct {
			Entries      []LedgerEntryResult `json:"entries"`
			LatestLedger int                 `json:"latestLedger"`
		}{
			LatestLedger: 500,
			Entries: []LedgerEntryResult{
				{Key: "AAAA", Xdr: "BBBB", LastModifiedLedger: 400},
			},
		},
	}
	requireNoError(t, ValidateGetLedgerEntriesResponse("https://rpc.example.com", resp))
}

func TestValidateGetLedgerEntriesResponse_ValidEmptyEntries(t *testing.T) {
	// entries:[] is a legitimate response when none of the keys exist.
	resp := &GetLedgerEntriesResponse{
		Jsonrpc: "2.0",
		Result: struct {
			Entries      []LedgerEntryResult `json:"entries"`
			LatestLedger int                 `json:"latestLedger"`
		}{
			LatestLedger: 500,
			Entries:      []LedgerEntryResult{}, // non-nil empty slice
		},
	}
	requireNoError(t, ValidateGetLedgerEntriesResponse("https://rpc.example.com", resp))
}

func TestValidateGetLedgerEntriesResponse_NilResponse(t *testing.T) {
	requireInvalidResponse(t, ValidateGetLedgerEntriesResponse("https://rpc.example.com", nil))
}

func TestValidateGetLedgerEntriesResponse_NullEntries(t *testing.T) {
	// Null entries field (absent from JSON) → nil slice → should fail.
	resp := &GetLedgerEntriesResponse{
		Jsonrpc: "2.0",
		Result: struct {
			Entries      []LedgerEntryResult `json:"entries"`
			LatestLedger int                 `json:"latestLedger"`
		}{
			LatestLedger: 500,
			Entries:      nil, // nil means absent/null in JSON
		},
	}
	requireInvalidResponse(t, ValidateGetLedgerEntriesResponse("https://rpc.example.com", resp))
}

func TestValidateGetLedgerEntriesResponse_ZeroLatestLedger(t *testing.T) {
	resp := &GetLedgerEntriesResponse{
		Jsonrpc: "2.0",
		Result: struct {
			Entries      []LedgerEntryResult `json:"entries"`
			LatestLedger int                 `json:"latestLedger"`
		}{
			LatestLedger: 0,
			Entries:      []LedgerEntryResult{},
		},
	}
	requireInvalidResponse(t, ValidateGetLedgerEntriesResponse("https://rpc.example.com", resp))
}

func TestValidateGetLedgerEntriesResponse_EntryEmptyKey(t *testing.T) {
	resp := &GetLedgerEntriesResponse{
		Jsonrpc: "2.0",
		Result: struct {
			Entries      []LedgerEntryResult `json:"entries"`
			LatestLedger int                 `json:"latestLedger"`
		}{
			LatestLedger: 500,
			Entries: []LedgerEntryResult{
				{Key: "", Xdr: "BBBB"}, // empty key
			},
		},
	}
	requireInvalidResponse(t, ValidateGetLedgerEntriesResponse("https://rpc.example.com", resp))
}

func TestValidateGetLedgerEntriesResponse_EntryEmptyXdr(t *testing.T) {
	resp := &GetLedgerEntriesResponse{
		Jsonrpc: "2.0",
		Result: struct {
			Entries      []LedgerEntryResult `json:"entries"`
			LatestLedger int                 `json:"latestLedger"`
		}{
			LatestLedger: 500,
			Entries: []LedgerEntryResult{
				{Key: "AAAA", Xdr: ""}, // empty xdr
			},
		},
	}
	requireInvalidResponse(t, ValidateGetLedgerEntriesResponse("https://rpc.example.com", resp))
}

func TestValidateGetLedgerEntriesResponse_WrongVersion(t *testing.T) {
	resp := &GetLedgerEntriesResponse{
		Jsonrpc: "2.0-rc1",
		Result: struct {
			Entries      []LedgerEntryResult `json:"entries"`
			LatestLedger int                 `json:"latestLedger"`
		}{
			LatestLedger: 500,
			Entries:      []LedgerEntryResult{},
		},
	}
	requireInvalidResponse(t, ValidateGetLedgerEntriesResponse("https://rpc.example.com", resp))
}

func TestValidateGetLedgerEntriesResponse_ValidErrorEnvelope(t *testing.T) {
	resp := &GetLedgerEntriesResponse{
		Jsonrpc: "2.0",
		Error: &struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}{Code: -32001, Message: "Transaction failed"},
	}
	requireNoError(t, ValidateGetLedgerEntriesResponse("https://rpc.example.com", resp))
}

func TestValidateGetLedgerEntriesResponse_ErrorWithEmptyMessage(t *testing.T) {
	resp := &GetLedgerEntriesResponse{
		Jsonrpc: "2.0",
		Error: &struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}{Code: -32603, Message: ""},
	}
	requireInvalidResponse(t, ValidateGetLedgerEntriesResponse("https://rpc.example.com", resp))
}

// ── ValidateSimulateTransactionResponse ──────────────────────────────────────

func TestValidateSimulateTransactionResponse_ValidMinResourceFee(t *testing.T) {
	resp := &SimulateTransactionResponse{
		Jsonrpc: "2.0",
		Result: struct {
			MinResourceFee  string `json:"minResourceFee,omitempty"`
			TransactionData string `json:"transactionData,omitempty"`
			Cost            struct {
				CpuInsns  int64 `json:"cpuInsns,omitempty"`
				MemBytes  int64 `json:"memBytes,omitempty"`
				CpuInsns_ int64 `json:"cpu_insns,omitempty"`
				MemBytes_ int64 `json:"mem_bytes,omitempty"`
			} `json:"cost,omitempty"`
		}{MinResourceFee: "1000"},
	}
	requireNoError(t, ValidateSimulateTransactionResponse("https://rpc.example.com", resp))
}

func TestValidateSimulateTransactionResponse_ValidTransactionData(t *testing.T) {
	resp := &SimulateTransactionResponse{
		Jsonrpc: "2.0",
		Result: struct {
			MinResourceFee  string `json:"minResourceFee,omitempty"`
			TransactionData string `json:"transactionData,omitempty"`
			Cost            struct {
				CpuInsns  int64 `json:"cpuInsns,omitempty"`
				MemBytes  int64 `json:"memBytes,omitempty"`
				CpuInsns_ int64 `json:"cpu_insns,omitempty"`
				MemBytes_ int64 `json:"mem_bytes,omitempty"`
			} `json:"cost,omitempty"`
		}{TransactionData: "AAAAAA=="},
	}
	requireNoError(t, ValidateSimulateTransactionResponse("https://rpc.example.com", resp))
}

func TestValidateSimulateTransactionResponse_ValidBothFields(t *testing.T) {
	resp := &SimulateTransactionResponse{
		Jsonrpc: "2.0",
		Result: struct {
			MinResourceFee  string `json:"minResourceFee,omitempty"`
			TransactionData string `json:"transactionData,omitempty"`
			Cost            struct {
				CpuInsns  int64 `json:"cpuInsns,omitempty"`
				MemBytes  int64 `json:"memBytes,omitempty"`
				CpuInsns_ int64 `json:"cpu_insns,omitempty"`
				MemBytes_ int64 `json:"mem_bytes,omitempty"`
			} `json:"cost,omitempty"`
		}{MinResourceFee: "1000", TransactionData: "AAAAAA=="},
	}
	requireNoError(t, ValidateSimulateTransactionResponse("https://rpc.example.com", resp))
}

func TestValidateSimulateTransactionResponse_NilResponse(t *testing.T) {
	requireInvalidResponse(t, ValidateSimulateTransactionResponse("https://rpc.example.com", nil))
}

func TestValidateSimulateTransactionResponse_EmptyResult(t *testing.T) {
	// Both minResourceFee and transactionData absent (truncated body).
	resp := &SimulateTransactionResponse{
		Jsonrpc: "2.0",
	}
	requireInvalidResponse(t, ValidateSimulateTransactionResponse("https://rpc.example.com", resp))
}

func TestValidateSimulateTransactionResponse_WrongVersion(t *testing.T) {
	resp := &SimulateTransactionResponse{
		Jsonrpc: "2.0-future",
		Result: struct {
			MinResourceFee  string `json:"minResourceFee,omitempty"`
			TransactionData string `json:"transactionData,omitempty"`
			Cost            struct {
				CpuInsns  int64 `json:"cpuInsns,omitempty"`
				MemBytes  int64 `json:"memBytes,omitempty"`
				CpuInsns_ int64 `json:"cpu_insns,omitempty"`
				MemBytes_ int64 `json:"mem_bytes,omitempty"`
			} `json:"cost,omitempty"`
		}{MinResourceFee: "500"},
	}
	requireInvalidResponse(t, ValidateSimulateTransactionResponse("https://rpc.example.com", resp))
}

func TestValidateSimulateTransactionResponse_ValidErrorEnvelope(t *testing.T) {
	resp := &SimulateTransactionResponse{
		Jsonrpc: "2.0",
		Error: &struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}{Code: -32002, Message: "simulation failed"},
	}
	requireNoError(t, ValidateSimulateTransactionResponse("https://rpc.example.com", resp))
}

// ── ValidateTransactionResponse ──────────────────────────────────────────────

func TestValidateTransactionResponse_Valid(t *testing.T) {
	resp := &TransactionResponse{
		EnvelopeXdr:   "AAAA",
		ResultXdr:     "BBBB",
		ResultMetaXdr: "CCCC",
	}
	requireNoError(t, ValidateTransactionResponse("https://horizon.example.com", "abc123", resp))
}

func TestValidateTransactionResponse_NilResponse(t *testing.T) {
	requireInvalidResponse(t, ValidateTransactionResponse("https://horizon.example.com", "abc123", nil))
}

func TestValidateTransactionResponse_EmptyEnvelopeXdr(t *testing.T) {
	resp := &TransactionResponse{
		EnvelopeXdr:   "",
		ResultXdr:     "BBBB",
		ResultMetaXdr: "CCCC",
	}
	requireInvalidResponse(t, ValidateTransactionResponse("https://horizon.example.com", "abc123", resp))
}

func TestValidateTransactionResponse_EmptyResultXdr(t *testing.T) {
	resp := &TransactionResponse{
		EnvelopeXdr:   "AAAA",
		ResultXdr:     "",
		ResultMetaXdr: "CCCC",
	}
	requireInvalidResponse(t, ValidateTransactionResponse("https://horizon.example.com", "abc123", resp))
}

func TestValidateTransactionResponse_EmptyResultMetaXdr(t *testing.T) {
	resp := &TransactionResponse{
		EnvelopeXdr:   "AAAA",
		ResultXdr:     "BBBB",
		ResultMetaXdr: "",
	}
	requireInvalidResponse(t, ValidateTransactionResponse("https://horizon.example.com", "abc123", resp))
}

func TestValidateTransactionResponse_AllFieldsEmpty(t *testing.T) {
	resp := &TransactionResponse{}
	requireInvalidResponse(t, ValidateTransactionResponse("https://horizon.example.com", "abc123", resp))
}

// ── RPCInvalidResponseError diagnostic fields ─────────────────────────────────

func TestRPCInvalidResponseError_ContainsEndpointAndMethod(t *testing.T) {
	err := glassboxerrors.WrapRPCInvalidResponse(
		"https://rpc.stellar.org",
		"getLedgerEntries",
		"result.latestLedger",
		"must be positive",
	)
	msg := err.Error()
	for _, want := range []string{"https://rpc.stellar.org", "getLedgerEntries", "result.latestLedger", "must be positive"} {
		if !containsStr(msg, want) {
			t.Errorf("error message %q does not contain %q", msg, want)
		}
	}
}

func TestRPCInvalidResponseError_IsErrRPCInvalidResponse(t *testing.T) {
	err := glassboxerrors.WrapRPCInvalidResponse("url", "method", "field", "reason")
	if !errors.Is(err, glassboxerrors.ErrRPCInvalidResponse) {
		t.Errorf("errors.Is check failed for ErrRPCInvalidResponse")
	}
}

func TestRPCInvalidResponseError_NoFieldName(t *testing.T) {
	// When field is empty the message format should still be readable.
	err := glassboxerrors.WrapRPCInvalidResponse("https://rpc.stellar.org", "getHealth", "", "response is nil")
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	msg := err.Error()
	if !containsStr(msg, "https://rpc.stellar.org") {
		t.Errorf("expected endpoint in message, got %q", msg)
	}
	if !containsStr(msg, "getHealth") {
		t.Errorf("expected method in message, got %q", msg)
	}
}

// ── No credential leakage ─────────────────────────────────────────────────────

func TestValidationErrors_DoNotContainResponseBody(t *testing.T) {
	// Simulate a response body that contains a credential-like value.
	// The validation error must not echo the XDR bytes or any body fragment.
	sensitiveXdr := "AAABBBCCC_BEARER_TOKEN_abc123"

	resp := &GetLedgerEntriesResponse{
		Jsonrpc: "2.0",
		Result: struct {
			Entries      []LedgerEntryResult `json:"entries"`
			LatestLedger int                 `json:"latestLedger"`
		}{
			LatestLedger: 100,
			Entries: []LedgerEntryResult{
				{Key: sensitiveXdr, Xdr: ""}, // empty XDR triggers validation failure
			},
		},
	}
	err := ValidateGetLedgerEntriesResponse("https://rpc.example.com", resp)
	if err == nil {
		t.Fatal("expected validation error")
	}
	// The sensitive XDR value (the key) must not appear in the error message.
	// Only structural field names and reasons are allowed.
	if containsStr(err.Error(), sensitiveXdr) {
		t.Errorf("error message leaks response body data: %q", err.Error())
	}
}
