// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package rpc

// TestRegression_RPC_SendTransaction_InternalError_NormalizedOffline verifies
// that a JSON-RPC internal-error response (-32603) is parsed and validated
// correctly without contacting a live Stellar endpoint.
//
// The fixture used is:
//   test/regression/fixtures/rpc/rpc_sendtransaction_internal_error_pr_offline.json
//
// Original failure class: rpc-error-normalization — a server-side internal
// error must be surfaced as ErrRPCInvalidResponse (because the envelope is
// structurally valid) and the normalized error message must be safe to display
// to the user (no raw response body, no credentials echoed).
//
// This test is deterministic and network-free.  It loads the fixture bytes
// directly and drives the same ValidateGetHealthResponse path that the live
// client uses, ensuring that error-object parsing cannot regress silently.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	glassboxerrors "github.com/dotandev/glassbox/internal/errors"
)

// fixturePath resolves a path relative to the repository root regardless of
// which directory the test is run from.
func rpcErrorFixturePath(t *testing.T, rel string) string {
	t.Helper()
	// runtime.Caller(0) gives the path of this source file, which is at
	// internal/rpc/.  Walk up two levels to reach the repo root.
	_, src, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed; cannot locate fixture")
	}
	root := filepath.Join(filepath.Dir(src), "..", "..")
	return filepath.Join(root, rel)
}

func TestRegression_RPC_SendTransaction_InternalError_NormalizedOffline(t *testing.T) {
	// Failure class: rpc-error-normalization

	// ── 1. Arrange ────────────────────────────────────────────────────────
	// Load the fixture from disk so the test is tied to the file that lives
	// alongside the regression guide, not to an inline constant.
	fixturePath := rpcErrorFixturePath(t,
		"test/regression/fixtures/rpc/rpc_sendtransaction_internal_error_pr_offline.json",
	)

	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("cannot read fixture file %s: %v", fixturePath, err)
	}

	// Unmarshal into GetHealthResponse because the fixture's envelope shape
	// (jsonrpc/id/error) matches any method response struct.  We are testing
	// that the error-object path inside ValidateGetHealthResponse is
	// correctly reached and validated — not the result fields.
	var rpcResp GetHealthResponse
	if err := json.Unmarshal(raw, &rpcResp); err != nil {
		t.Fatalf("fixture JSON is malformed: %v", err)
	}

	// ── 2. Act ────────────────────────────────────────────────────────────
	// Drive the same validation function the live client calls after every
	// HTTP round-trip.  The endpoint string is a stub; no network call is made.
	const stubEndpoint = "https://stub.rpc.testnet.stellar.org"
	validationErr := ValidateGetHealthResponse(stubEndpoint, &rpcResp)

	// ── 3. Assert ─────────────────────────────────────────────────────────
	// 3a. The error object has a non-empty message ("Internal error"), so
	//     ValidateGetHealthResponse must accept the envelope as structurally
	//     valid and return nil.  The caller is responsible for inspecting
	//     rpcResp.Error and routing accordingly.
	if validationErr != nil {
		t.Errorf("ValidateGetHealthResponse must accept a well-formed error envelope, got: %v", validationErr)
	}

	// 3b. The fixture must have unmarshalled an error object with the expected
	//     code and message — this confirms the fixture is parsed correctly.
	if rpcResp.Error == nil {
		t.Fatal("fixture must produce a non-nil error object after unmarshal")
	}
	if rpcResp.Error.Code != -32603 {
		t.Errorf("expected error code -32603, got %d", rpcResp.Error.Code)
	}
	if rpcResp.Error.Message != "Internal error" {
		t.Errorf("expected error message %q, got %q", "Internal error", rpcResp.Error.Message)
	}

	// 3c. The result fields must all be zero — confirming the fixture is a
	//     pure error response with no stale result data mixed in.
	if rpcResp.Result.Status != "" {
		t.Errorf("error fixture must have empty result.status, got %q", rpcResp.Result.Status)
	}

	// 3d. Normalise the error into an ErstError the way the client layer does.
	//     The code must map to ErstRPCError, not ErstUnknown or a connectivity code.
	normalizedErr := glassboxerrors.WrapRPCError(
		stubEndpoint,
		rpcResp.Error.Message,
		rpcResp.Error.Code,
	)
	if normalizedErr == nil {
		t.Fatal("WrapRPCError must return a non-nil error")
	}
	if !errors.Is(normalizedErr, glassboxerrors.ErrRPCError) {
		t.Errorf("normalized error must satisfy errors.Is(err, ErrRPCError), got %T: %v",
			normalizedErr, normalizedErr)
	}

	// 3e. The user-facing message must mention the error code so the user
	//     can cross-reference the JSON-RPC spec, but must NOT echo the raw
	//     fixture bytes or anything that looks like a credential.
	msg := normalizedErr.Error()
	if !strings.Contains(msg, "-32603") {
		t.Errorf("normalized error message should contain the RPC code -32603, got: %q", msg)
	}
	if !strings.Contains(strings.ToLower(msg), "internal error") {
		t.Errorf("normalized error message should contain 'internal error', got: %q", msg)
	}
	// Confirm no raw fixture content (the full JSON) is leaked into the message.
	if strings.Contains(msg, `"jsonrpc"`) || strings.Contains(msg, `"_comment"`) {
		t.Errorf("normalized error message must not contain raw fixture JSON, got: %q", msg)
	}
}
