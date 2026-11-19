// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package simulator

import (
	"errors"
	"testing"

	"github.com/dotandev/glassbox/internal/ipc"
)

// buildResponse builds a HandshakeResponse with sensible defaults for tests.
func buildResponse(build string, proto uint32, features ...string) ipc.HandshakeResponse {
	return ipc.HandshakeResponse{
		Type:              ipc.HandshakeResponseType,
		SimulatorBuild:    build,
		ProtocolVersion:   proto,
		SupportedFeatures: features,
		MaxRequestBytes:   10 * 1024 * 1024,
	}
}

// ── ValidateHandshakeResponse ─────────────────────────────────────────────────

func TestHandshake_Compatible(t *testing.T) {
	resp := buildResponse("v1.2.3", 22, "soroban_invoke", "snapshot")
	result, err := ValidateHandshakeResponse(resp, 22, []string{"soroban_invoke"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.SimulatorBuild != "v1.2.3" {
		t.Errorf("build = %q, want %q", result.SimulatorBuild, "v1.2.3")
	}
	if result.ProtocolVersion != 22 {
		t.Errorf("protocol = %d, want 22", result.ProtocolVersion)
	}
	if result.MaxRequestBytes != 10*1024*1024 {
		t.Errorf("MaxRequestBytes = %d", result.MaxRequestBytes)
	}
}

func TestHandshake_NoRequiredFeatures(t *testing.T) {
	resp := buildResponse("v2.0.0", 21)
	_, err := ValidateHandshakeResponse(resp, 20, nil)
	if err != nil {
		t.Fatalf("unexpected error with no required features: %v", err)
	}
}

func TestHandshake_NewerSimulatorIsAccepted(t *testing.T) {
	// Simulator reports protocol 23 but caller only requires 22.
	resp := buildResponse("next", 23, "soroban_invoke")
	_, err := ValidateHandshakeResponse(resp, 22, nil)
	if err != nil {
		t.Fatalf("newer protocol should be accepted: %v", err)
	}
}

// ── Incompatible version ──────────────────────────────────────────────────────

func TestHandshake_OlderSimulator_Fails(t *testing.T) {
	resp := buildResponse("old-build", 20)
	_, err := ValidateHandshakeResponse(resp, 22, nil)
	if err == nil {
		t.Fatal("expected ErrIncompatibleVersion for protocol 20 < required 22")
	}
	if !errors.Is(err, ErrIncompatibleVersion) {
		t.Errorf("expected ErrIncompatibleVersion, got %T: %v", err, err)
	}
	msg := err.Error()
	for _, want := range []string{"20", "22", "update"} {
		if !containsStr(msg, want) {
			t.Errorf("error message missing %q: %s", want, msg)
		}
	}
}

// ── Missing capability ────────────────────────────────────────────────────────

func TestHandshake_MissingRequiredFeature(t *testing.T) {
	resp := buildResponse("v1.0.0", 22, "soroban_invoke") // no "snapshot"
	_, err := ValidateHandshakeResponse(resp, 22, []string{"soroban_invoke", "snapshot"})
	if err == nil {
		t.Fatal("expected ErrMissingCapability")
	}
	if !errors.Is(err, ErrMissingCapability) {
		t.Errorf("expected ErrMissingCapability, got %T: %v", err, err)
	}
	msg := err.Error()
	for _, want := range []string{"snapshot", "v1.0.0", "update"} {
		if !containsStr(msg, want) {
			t.Errorf("error message missing %q: %s", want, msg)
		}
	}
}

func TestHandshake_MultipleRequiredFeatures_AllPresent(t *testing.T) {
	resp := buildResponse("v2.1.0", 22, "soroban_invoke", "snapshot", "auth_trace")
	_, err := ValidateHandshakeResponse(resp, 22, []string{"soroban_invoke", "snapshot", "auth_trace"})
	if err != nil {
		t.Fatalf("all features present, unexpected error: %v", err)
	}
}

// ── Simulator rejects the handshake ──────────────────────────────────────────

func TestHandshake_SimulatorReturnsError(t *testing.T) {
	resp := ipc.HandshakeResponse{
		Type:  ipc.HandshakeResponseType,
		Error: "unsupported network: futurenet",
	}
	_, err := ValidateHandshakeResponse(resp, 0, nil)
	// Error field in response triggers ErrHandshakeFailed when processed via
	// PerformHandshake; ValidateHandshakeResponse does not check the Error field —
	// that is PerformHandshake's responsibility. Verify the error field is preserved.
	_ = err // ValidateHandshakeResponse only checks version/features, not Error field.
	// Confirm the Response.Error field round-trips through marshal/unmarshal.
	b, _ := resp.Marshal()
	decoded, decErr := ipc.UnmarshalHandshakeResponse(b)
	if decErr != nil {
		t.Fatalf("unmarshal: %v", decErr)
	}
	if decoded.Error != "unsupported network: futurenet" {
		t.Errorf("error field not preserved: %q", decoded.Error)
	}
}

// ── HandshakeDiagnostics ──────────────────────────────────────────────────────

func TestHandshakeDiagnostics_Nil(t *testing.T) {
	d := HandshakeDiagnostics(nil)
	if d["handshake"] != "not_performed" {
		t.Errorf("nil result: got %v", d)
	}
}

func TestHandshakeDiagnostics_WithResult(t *testing.T) {
	r := &HandshakeResult{
		SimulatorBuild:    "build-abc",
		ProtocolVersion:   22,
		SupportedFeatures: []string{"soroban_invoke"},
		MaxRequestBytes:   8 * 1024 * 1024,
	}
	d := HandshakeDiagnostics(r)
	if d["simulator_build"] != "build-abc" {
		t.Errorf("build: %v", d)
	}
	if d["protocol_version"] != uint32(22) {
		t.Errorf("protocol: %v", d)
	}
}

func TestHandshakeDiagnosticsJSON_Valid(t *testing.T) {
	r := &HandshakeResult{SimulatorBuild: "x", ProtocolVersion: 21}
	s := HandshakeDiagnosticsJSON(r)
	if s == "{}" || len(s) < 2 {
		t.Errorf("unexpected JSON: %s", s)
	}
	if !containsStr(s, "simulator_build") {
		t.Errorf("missing simulator_build in JSON: %s", s)
	}
}

// ── IPC round-trip ────────────────────────────────────────────────────────────

func TestHandshakeRequest_MarshalRoundtrip(t *testing.T) {
	req := ipc.HandshakeRequest{
		Type:             ipc.HandshakeRequestType,
		ProtocolVersion:  22,
		RequiredFeatures: []string{"soroban_invoke"},
		MaxRequestBytes:  5 * 1024 * 1024,
	}
	b, err := req.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := ipc.UnmarshalHandshakeRequest(b)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Type != ipc.HandshakeRequestType {
		t.Errorf("type = %q", got.Type)
	}
	if got.ProtocolVersion != 22 {
		t.Errorf("protocol = %d", got.ProtocolVersion)
	}
	if len(got.RequiredFeatures) != 1 || got.RequiredFeatures[0] != "soroban_invoke" {
		t.Errorf("features = %v", got.RequiredFeatures)
	}
}

func TestHandshakeResponse_MarshalRoundtrip(t *testing.T) {
	resp := buildResponse("sha-abc123", 22, "soroban_invoke", "snapshot")
	b, err := resp.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := ipc.UnmarshalHandshakeResponse(b)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.SimulatorBuild != "sha-abc123" {
		t.Errorf("build = %q", got.SimulatorBuild)
	}
	if len(got.SupportedFeatures) != 2 {
		t.Errorf("features = %v", got.SupportedFeatures)
	}
}

// containsStr is a simple string-contains helper to avoid importing strings.
func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
