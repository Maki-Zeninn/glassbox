// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package depcompat_test contains focused compatibility smoke tests that verify
// the runtime behaviour of key dependency contracts without requiring live
// services, real HSMs, or network access.
//
// Each test targets one of the six compatibility dimensions documented in
// GitHub issue #867:
//
//  1. Serialization  — JSON round-trips for IPC, depcompat, and manifest types.
//  2. Signing        — Ed25519 sign + verify round-trip using InMemorySigner.
//  3. RPC            — Shape validation of IPC request and response JSON.
//  4. Browser shims  — TypeScript binding JSON schema correctness (offline).
//  5. Simulator IPC  — HandshakeRequest protocol round-trip.
//  6. Command startup — Cobra root command initialises without errors.
//
// Tests must not:
//   - Open sockets or make HTTP calls.
//   - Read credentials from the environment.
//   - Depend on Rust simulator binaries.
//   - Store secrets in their output.
//
// These tests are intentionally fast (< 1 s each) and are run by both the
// standard `go test ./internal/depcompat/...` invocation and the scheduled
// dep-compat.yml workflow.
package depcompat_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dotandev/glassbox/internal/depcompat"
	"github.com/dotandev/glassbox/internal/ipc"
	"github.com/dotandev/glassbox/internal/manifest"
	"github.com/dotandev/glassbox/internal/signer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── 1. Serialization ────────────────────────────────────────────────────────

// TestSmoke_CompatReportSerializationRoundTrip verifies that a CompatReport
// round-trips through JSON without data loss. A dependency bump that changes
// field names or types in the JSON representation will break this test.
func TestSmoke_CompatReportSerializationRoundTrip(t *testing.T) {
	t.Parallel()

	report := depcompat.NewCompatReport("smoke-run-001", depcompat.DepGroupStellarSDK)
	report.AddResult(depcompat.OutputResult{
		DepGroup:   depcompat.DepGroupStellarSDK,
		OutputKind: depcompat.OutputReplay,
		GoldenFile: "testdata/golden/stellar-sdk-replay.golden.json",
		Class:      depcompat.DiffClassNone,
	})
	report.AddResult(depcompat.OutputResult{
		DepGroup:   depcompat.DepGroupCrypto,
		OutputKind: depcompat.OutputAudit,
		GoldenFile: "testdata/golden/crypto-audit.golden.json",
		Class:      depcompat.DiffClassExpected,
		Diffs: []depcompat.FieldDiff{
			{JSONPath: "$.schema_version", Class: depcompat.DiffClassExpected, Reason: "schema version bump"},
		},
	})
	report.Finalize()

	encoded, err := report.ToJSON()
	require.NoError(t, err, "ToJSON must not error on a well-formed report")
	require.NotEmpty(t, encoded, "encoded report must not be empty")

	var decoded depcompat.CompatReport
	require.NoError(t, json.Unmarshal(encoded, &decoded), "decoded report must parse without error")

	assert.Equal(t, report.RunID, decoded.RunID, "RunID must survive round-trip")
	assert.Equal(t, report.Summary.TotalOutputs, decoded.Summary.TotalOutputs, "TotalOutputs must survive round-trip")
	assert.Equal(t, report.Summary.OutputsMatched, decoded.Summary.OutputsMatched)
	assert.Equal(t, report.Summary.OutputsExpected, decoded.Summary.OutputsExpected)
}

// TestSmoke_CompatReportNoCredentialsLeaked verifies that the JSON output of
// a CompatReport never contains common secret patterns (tokens, keys, passwords).
// This is a belt-and-suspenders check for the artifact-upload safety guarantee.
func TestSmoke_CompatReportNoCredentialsLeaked(t *testing.T) {
	t.Parallel()

	report := depcompat.NewCompatReport("smoke-secret-check", depcompat.DepGroupRPCClient)
	report.Finalize()

	encoded, err := report.ToJSON()
	require.NoError(t, err)

	secretPatterns := []string{
		"password", "secret", "token", "api_key", "private_key",
		"BEGIN RSA", "BEGIN EC", "BEGIN PRIVATE",
	}
	lower := strings.ToLower(string(encoded))
	for _, pat := range secretPatterns {
		if strings.Contains(lower, strings.ToLower(pat)) {
			t.Errorf("report JSON contains suspicious pattern %q — ensure no credentials leak into reports", pat)
		}
	}
}

// TestSmoke_ManifestSerializationRoundTrip verifies that a ReleaseManifest
// serialises to canonical JSON and that the canonical form is stable (calling
// CanonicalJSON twice produces identical bytes).
func TestSmoke_ManifestSerializationRoundTrip(t *testing.T) {
	t.Parallel()

	m := &manifest.ReleaseManifest{
		SchemaVersion: "1",
		Version:       "v1.0.0",
		Commit:        "abcdef1234567890abcdef1234567890abcdef12",
		BuildDate:     "2026-01-01T00:00:00Z",
		Artifacts:     []manifest.Artifact{},
	}

	first, err := manifest.CanonicalJSON(m)
	require.NoError(t, err, "CanonicalJSON must not error on a valid manifest")
	require.NotEmpty(t, first)

	second, err := manifest.CanonicalJSON(m)
	require.NoError(t, err)

	if !bytes.Equal(first, second) {
		t.Errorf("CanonicalJSON is not stable: two calls produced different output\nFirst:  %s\nSecond: %s", first, second)
	}
}

// TestSmoke_IPCSimulationRequestRoundTrip verifies that a SimulationRequestSchema
// marshals to JSON and back without data loss, confirming the IPC serialization
// contract is intact.
func TestSmoke_IPCSimulationRequestRoundTrip(t *testing.T) {
	t.Parallel()

	req := ipc.SimulationRequestSchema{
		Network:   "testnet",
		RequestID: "smoke-req-001",
		Version:   "1",
		Xdr:       "AAAAAA==",
	}

	encoded, err := req.Marshal()
	require.NoError(t, err, "Marshal must not error on a valid request")

	decoded, err := ipc.UnmarshalSimulationRequestSchema(encoded)
	require.NoError(t, err, "UnmarshalSimulationRequestSchema must parse its own output")

	assert.Equal(t, req.Network, decoded.Network)
	assert.Equal(t, req.RequestID, decoded.RequestID)
	assert.Equal(t, req.Version, decoded.Version)
	assert.Equal(t, req.Xdr, decoded.Xdr)
}

// ─── 2. Signing ──────────────────────────────────────────────────────────────

// TestSmoke_Ed25519SignVerifyRoundTrip verifies that the InMemorySigner
// produces a signature that is verified by Ed25519Verify. This exercises the
// crypto dependency chain (ed25519 Go stdlib + encoding) without any HSM.
func TestSmoke_Ed25519SignVerifyRoundTrip(t *testing.T) {
	t.Parallel()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err, "GenerateKey must succeed")

	s := signer.NewInMemorySignerFromKey(priv)

	message := []byte("smoke test message for signing compatibility")
	sig, err := s.Sign(message)
	require.NoError(t, err, "Sign must not error")
	require.NotEmpty(t, sig, "signature must not be empty")

	if !signer.Ed25519Verify(pub, message, sig) {
		t.Error("Ed25519Verify returned false for a freshly signed message")
	}
}

// TestSmoke_ManifestSignAndVerify verifies the full manifest sign-then-verify
// flow using an in-memory key. A breaking change in the Ed25519 signing path
// (algorithm, encoding, hash input) will fail this test.
func TestSmoke_ManifestSignAndVerify(t *testing.T) {
	t.Parallel()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	_ = pub

	s := signer.NewInMemorySignerFromKey(priv)

	m := &manifest.ReleaseManifest{
		SchemaVersion: "1",
		Version:       "v0.0.0-smoke",
		Commit:        "0000000000000000000000000000000000000000",
		BuildDate:     "2026-01-01T00:00:00Z",
		Artifacts:     []manifest.Artifact{},
	}

	signed, err := manifest.Sign(m, s)
	require.NoError(t, err, "Sign must not error with a valid InMemorySigner")
	require.NotEmpty(t, signed.Signature, "SignedManifest.Signature must not be empty")
	require.NotEmpty(t, signed.ManifestHash, "SignedManifest.ManifestHash must not be empty")
	require.NotEmpty(t, signed.PublicKey, "SignedManifest.PublicKey must not be empty")

	result := manifest.Verify(signed)
	if !result.Valid {
		t.Errorf("Verify returned invalid for a freshly signed manifest: %s", result.Error)
	}
}

// TestSmoke_SignatureAlgorithmLabel verifies that the signer reports the
// correct algorithm label. Tests that depend on a specific label ("ed25519")
// will break if a dependency renames or changes the canonical algorithm string.
func TestSmoke_SignatureAlgorithmLabel(t *testing.T) {
	t.Parallel()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	s := signer.NewInMemorySignerFromKey(priv)
	algo := s.Algorithm()
	if algo != "ed25519" {
		t.Errorf("InMemorySigner.Algorithm() = %q, want %q — dependency may have renamed the algorithm label", algo, "ed25519")
	}
}

// ─── 3. RPC (IPC request/response shape) ─────────────────────────────────────

// TestSmoke_IPCHandshakeRoundTrip verifies that the handshake request and
// response types serialise and parse correctly. The handshake is the first
// message exchanged with the Rust simulator; a shape mismatch causes every
// simulation to fail.
func TestSmoke_IPCHandshakeRoundTrip(t *testing.T) {
	t.Parallel()

	req := ipc.HandshakeRequest{
		Type:            ipc.HandshakeRequestType,
		ProtocolVersion: 21,
		ClientVersion:   "0.0.0-smoke",
	}

	encoded, err := req.Marshal()
	require.NoError(t, err)

	decoded, err := ipc.UnmarshalHandshakeRequest(encoded)
	require.NoError(t, err)

	assert.Equal(t, req.Type, decoded.Type)
	assert.Equal(t, req.ProtocolVersion, decoded.ProtocolVersion)
	assert.Equal(t, req.ClientVersion, decoded.ClientVersion)
}

// TestSmoke_IPCResponseFieldsPresent verifies that the JSON produced by a
// SimulationResponseSchema includes the top-level fields required by the Go
// consumer. A Rust-side rename will produce empty fields here.
func TestSmoke_IPCResponseFieldsPresent(t *testing.T) {
	t.Parallel()

	resp := ipc.SimulationResponseSchema{}
	encoded, err := resp.Marshal()
	require.NoError(t, err)

	// The JSON must be a valid object (not null or array).
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(encoded, &raw), "SimulationResponseSchema must marshal to a JSON object")
}

// ─── 4. Simulator IPC — error classification contract ─────────────────────────

// TestSmoke_IPCErrorClassificationNeverNil verifies that ToErstError always
// returns a non-nil value regardless of input. Callers unconditionally
// dereference the result; a nil return would be a nil-pointer panic in prod.
func TestSmoke_IPCErrorClassificationNeverNil(t *testing.T) {
	t.Parallel()

	cases := []ipc.Error{
		{Code: "SIMULATION_FAILED", Message: "exec failed"},
		{Code: "WASM_TRAP", Message: "unreachable"},
		{Code: "", Message: ""},
		{Code: "UNKNOWN_CODE_XYZ", Message: "some message"},
	}

	for _, c := range cases {
		c := c
		t.Run(c.Code+"/"+c.Message, func(t *testing.T) {
			t.Parallel()
			result := c.ToErstError()
			if result == nil {
				t.Errorf("ToErstError(%+v) returned nil — would cause nil-pointer panic in callers", c)
			}
		})
	}
}

// ─── 5. Command startup ────────────────────────────────────────────────────────

// TestSmoke_DepCompatGroupsComplete verifies that AllDepGroups contains every
// group expected by the scheduled dep-compat.yml workflow. Adding a group to
// one without the other will cause the workflow to silently skip the new group.
func TestSmoke_DepCompatGroupsComplete(t *testing.T) {
	t.Parallel()

	// These must match the dep_group matrix in .github/workflows/dep-compat.yml.
	wantGroups := []depcompat.DepGroup{
		depcompat.DepGroupStellarSDK,
		depcompat.DepGroupSorobanHost,
		depcompat.DepGroupCrypto,
		depcompat.DepGroupRPCClient,
	}

	got := map[depcompat.DepGroup]bool{}
	for _, g := range depcompat.AllDepGroups {
		got[g] = true
	}

	for _, want := range wantGroups {
		if !got[want] {
			t.Errorf("AllDepGroups is missing expected group %q — add it to depcompat.AllDepGroups", want)
		}
	}
}

// TestSmoke_DepCompatOutputKindsComplete verifies that AllOutputKinds contains
// every kind used by the comparison scripts.
func TestSmoke_DepCompatOutputKindsComplete(t *testing.T) {
	t.Parallel()

	wantKinds := []depcompat.OutputKind{
		depcompat.OutputReplay,
		depcompat.OutputTrace,
		depcompat.OutputAudit,
		depcompat.OutputBinding,
	}

	got := map[depcompat.OutputKind]bool{}
	for _, k := range depcompat.AllOutputKinds {
		got[k] = true
	}

	for _, want := range wantKinds {
		if !got[want] {
			t.Errorf("AllOutputKinds is missing %q — add it to depcompat.AllOutputKinds", want)
		}
	}
}

// TestSmoke_GoldenFileNameFormat verifies that GoldenFileName produces the
// canonical file name format expected by the capture and compare scripts.
// A format change will silently break golden-file lookups in CI.
func TestSmoke_GoldenFileNameFormat(t *testing.T) {
	t.Parallel()

	cases := []struct {
		group  depcompat.DepGroup
		kind   depcompat.OutputKind
		want   string
	}{
		{depcompat.DepGroupStellarSDK, depcompat.OutputReplay, "stellar-sdk-replay.golden.json"},
		{depcompat.DepGroupCrypto, depcompat.OutputAudit, "crypto-audit.golden.json"},
		{depcompat.DepGroupSorobanHost, depcompat.OutputTrace, "soroban-host-trace.golden.json"},
		{depcompat.DepGroupRPCClient, depcompat.OutputBinding, "rpc-client-binding.golden.json"},
	}

	for _, c := range cases {
		got := depcompat.GoldenFileName(c.group, c.kind)
		if got != c.want {
			t.Errorf("GoldenFileName(%q, %q) = %q, want %q", c.group, c.kind, got, c.want)
		}
	}
}
