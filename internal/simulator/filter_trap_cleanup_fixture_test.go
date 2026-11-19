// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package simulator

import (
	"encoding/json"
	"os"
	"testing"
)

// ── Issue #533: Trap cause tests ──────────────────────────────────────────────

func TestParseTrapCause_ValidJSON(t *testing.T) {
	raw := []byte(`{"category":"authorization","host_error":"require_auth failed","wasm_function":"invoke"}`)
	cause, err := ParseTrapCause(raw)
	if err != nil {
		t.Fatalf("ParseTrapCause: %v", err)
	}
	if cause.Category != TrapCategoryAuth {
		t.Errorf("expected category %s, got %s", TrapCategoryAuth, cause.Category)
	}
	if cause.HostError != "require_auth failed" {
		t.Errorf("unexpected host_error: %s", cause.HostError)
	}
}

func TestParseTrapCause_InvalidJSON(t *testing.T) {
	raw := []byte(`{invalid json`)
	cause, err := ParseTrapCause(raw)
	if err != nil {
		t.Fatalf("expected no error on invalid JSON, got: %v", err)
	}
	if cause.Category != TrapCategoryUnknown {
		t.Errorf("expected unknown category, got %s", cause.Category)
	}
	if len(cause.OpaqueData) == 0 {
		t.Error("expected opaque data to be preserved")
	}
}

func TestMapErrorToCategory(t *testing.T) {
	tests := []struct {
		err      string
		expected TrapCategory
	}{
		{"require_auth failed for address GABC", TrapCategoryAuth},
		{"budget exceeded: cpu limit 1000000", TrapCategoryBudget},
		{"out of memory at offset 4096", TrapCategoryMemory},
		{"missing entry in storage", TrapCategoryMissingEntry},
		{"integer overflow in u64", TrapCategoryOverflow},
		{"division by zero", TrapCategoryDivisionByZero},
		{"index out of bounds: 10 >= 5", TrapCategoryIndexOOB},
		{"wasm unreachable instruction", TrapCategoryWasmTrap},
		{"something completely unknown", TrapCategoryUnknown},
	}
	for _, tt := range tests {
		got := MapErrorToCategory(tt.err)
		if got != tt.expected {
			t.Errorf("MapErrorToCategory(%q): expected %s, got %s", tt.err, tt.expected, got)
		}
	}
}

func TestTrapCause_GenerateRemediationHints(t *testing.T) {
	cause := &TrapCause{Category: TrapCategoryAuth}
	cause.GenerateRemediationHints()
	if len(cause.RemediationHints) == 0 {
		t.Error("expected remediation hints for auth trap")
	}
}

func TestTrapCause_ToSummary(t *testing.T) {
	cause := &TrapCause{
		Category:     TrapCategoryBudget,
		WasmFunction: "invoke",
		SourceFile:   "contract.rs",
		SourceLine:   42,
		HostError:    "cpu limit exceeded",
	}
	summary := cause.ToSummary()
	if summary == "" {
		t.Error("expected non-empty summary")
	}
}

// ── Issue #535: Fixture generation tests ──────────────────────────────────────

func TestGenerateFixture_Deterministic(t *testing.T) {
	dir := t.TempDir()
	config := DefaultFixtureGeneratorConfig(dir)
	config.IncludeTrace = false

	req, _ := json.Marshal(map[string]interface{}{
		"contract_id": "CAAAA",
		"function":    "test",
	})
	resp := &SimulationResponse{Status: "success"}

	fix1, path1, err := GenerateFixture("txhash1", req, resp, nil, 20, "testnet", config)
	if err != nil {
		t.Fatalf("GenerateFixture (1): %v", err)
	}
	if path1 == "" {
		t.Fatal("expected non-empty path")
	}

	// Generate again with same inputs — should produce same fingerprint
	fix2, path2, err := GenerateFixture("txhash1", req, resp, nil, 20, "testnet", config)
	if err != nil {
		t.Fatalf("GenerateFixture (2): %v", err)
	}

	if fix1.Manifest.ResultFingerprint != fix2.Manifest.ResultFingerprint {
		t.Error("expected deterministic result fingerprint")
	}
	if path1 != path2 {
		t.Error("expected deterministic file path")
	}
}

func TestGenerateFixture_RedactsSecrets(t *testing.T) {
	dir := t.TempDir()
	config := DefaultFixtureGeneratorConfig(dir)
	config.IncludeTrace = false

	req, _ := json.Marshal(map[string]interface{}{
		"contract_id": "CBBBB",
		"secret_key":  "SSECRET",
		"function":    "transfer",
		"password":    "mypassword",
	})
	resp := &SimulationResponse{Status: "success"}

	fix, _, err := GenerateFixture("txhash2", req, resp, nil, 20, "testnet", config)
	if err != nil {
		t.Fatalf("GenerateFixture: %v", err)
	}

	// Check that secret fields were redacted
	var reqMap map[string]interface{}
	json.Unmarshal(fix.Request, &reqMap)
	if reqMap["secret_key"] != "REDACTED" {
		t.Errorf("expected secret_key to be REDACTED, got %v", reqMap["secret_key"])
	}
	if reqMap["password"] != "REDACTED" {
		t.Errorf("expected password to be REDACTED, got %v", reqMap["password"])
	}
	if reqMap["contract_id"] != "CBBBB" {
		t.Error("non-sensitive field was incorrectly redacted")
	}

	// Verify redacted fields are listed
	found := false
	for _, f := range fix.Manifest.RedactedFields {
		if f == "secret_key" || f == "password" {
			found = true
		}
	}
	if !found {
		t.Error("redacted fields not listed in manifest")
	}
}

func TestGenerateFixture_WritesFile(t *testing.T) {
	dir := t.TempDir()
	config := DefaultFixtureGeneratorConfig(dir)
	config.IncludeTrace = false

	req, _ := json.Marshal(map[string]interface{}{"contract_id": "CCCCC"})
	resp := &SimulationResponse{Status: "success"}

	_, path, err := GenerateFixture("txhash3", req, resp, nil, 20, "testnet", config)
	if err != nil {
		t.Fatalf("GenerateFixture: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("fixture file not written: %v", err)
	}

	// Verify file is valid JSON
	data, _ := os.ReadFile(path)
	var fixture GeneratedFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("fixture file is not valid JSON: %v", err)
	}
	if fixture.Manifest.SchemaVersion == "" {
		t.Error("manifest schema_version is empty")
	}
}

func TestVerifyFixtureFingerprint(t *testing.T) {
	dir := t.TempDir()
	config := DefaultFixtureGeneratorConfig(dir)
	config.IncludeTrace = false

	req, _ := json.Marshal(map[string]interface{}{"contract_id": "DDDDD"})
	resp := &SimulationResponse{Status: "success"}

	fix, _, _ := GenerateFixture("txhash4", req, resp, nil, 20, "testnet", config)

	if !VerifyFixtureFingerprint(fix, resp) {
		t.Error("fingerprint verification should match same response")
	}

	differentResp := &SimulationResponse{Status: "failed", Error: "something else"}
	if VerifyFixtureFingerprint(fix, differentResp) {
		t.Error("fingerprint verification should not match different response")
	}
}
