// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package simulator

import (
	"encoding/json"
	"testing"
)

// ── Issue #536: Protocol compatibility matrix tests ───────────────────────────

func TestCompatMatrix_SupportedVersions(t *testing.T) {
	versions := SupportedProtocolVersions()
	if len(versions) < 2 {
		t.Errorf("expected at least 2 supported versions, got %d", len(versions))
	}
	// Verify versions are sorted
	for i := 1; i < len(versions); i++ {
		if versions[i] <= versions[i-1] {
			t.Error("versions are not sorted ascending")
		}
	}
}

func TestCompatMatrix_IsSupported(t *testing.T) {
	if !IsProtocolSupported(20) {
		t.Error("protocol 20 should be supported")
	}
	if !IsProtocolSupported(21) {
		t.Error("protocol 21 should be supported")
	}
	if IsProtocolSupported(99) {
		t.Error("protocol 99 should not be supported")
	}
}

func TestCompatMatrix_GetCapabilities(t *testing.T) {
	pc, err := GetCapabilities(20)
	if err != nil {
		t.Fatalf("GetCapabilities(20): %v", err)
	}
	if pc.Version != 20 {
		t.Errorf("expected version 20, got %d", pc.Version)
	}
	if len(pc.Capabilities) == 0 {
		t.Error("expected non-empty capabilities")
	}
}

func TestCompatMatrix_UnsupportedVersion(t *testing.T) {
	_, err := GetCapabilities(99)
	if err == nil {
		t.Error("expected error for unsupported version")
	}
}

func TestCompatMatrix_HasCapability(t *testing.T) {
	// Protocol 20 should have invoke_contract
	supported, err := HasCapability(20, "invoke_contract")
	if err != nil {
		t.Fatalf("HasCapability: %v", err)
	}
	if !supported {
		t.Error("invoke_contract should be supported in protocol 20")
	}

	// Protocol 20 should NOT have enhanced_metering
	supported, err = HasCapability(20, "enhanced_metering")
	if err != nil {
		t.Fatalf("HasCapability: %v", err)
	}
	if supported {
		t.Error("enhanced_metering should NOT be supported in protocol 20")
	}

	// Protocol 21 SHOULD have enhanced_metering
	supported, err = HasCapability(21, "enhanced_metering")
	if err != nil {
		t.Fatalf("HasCapability: %v", err)
	}
	if !supported {
		t.Error("enhanced_metering should be supported in protocol 21")
	}
}

func TestCompatMatrix_Limitation(t *testing.T) {
	limit, err := GetCapabilityLimitation(20, "max_contract_size")
	if err != nil {
		t.Fatalf("GetCapabilityLimitation: %v", err)
	}
	if limit == "" {
		t.Error("expected non-empty limitation for max_contract_size")
	}
}

func TestCompatMatrix_UnsupportedCapability(t *testing.T) {
	_, err := GetCapabilityLimitation(20, "enhanced_metering")
	if err == nil {
		t.Error("expected error for unsupported capability")
	}
}

func TestCompatMatrix_ToJSON(t *testing.T) {
	data, err := CompatMatrixToJSON()
	if err != nil {
		t.Fatalf("CompatMatrixToJSON: %v", err)
	}
	if !json.Valid(data) {
		t.Error("produced invalid JSON")
	}
}

func TestCompatMatrix_ToMarkdown(t *testing.T) {
	md := CompatMatrixToMarkdown()
	if md == "" {
		t.Error("produced empty markdown")
	}
	// Should contain both versions
	if !contains(md, "Protocol 20") || !contains(md, "Protocol 21") {
		t.Error("markdown should contain protocol versions")
	}
}

func TestCompatMatrix_TableDriven(t *testing.T) {
	// Table-driven test exercising every declared protocol version
	versions := SupportedProtocolVersions()
	for _, v := range versions {
		t.Run("protocol_"+string(rune('0'+v)), func(t *testing.T) {
			pc, err := GetCapabilities(v)
			if err != nil {
				t.Fatalf("GetCapabilities(%d): %v", v, err)
			}
			// Every protocol must have invoke_contract
			found := false
			for _, cap := range pc.Capabilities {
				if cap.Name == "invoke_contract" && cap.Supported {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("protocol %d must support invoke_contract", v)
			}
		})
	}
}

// ── Issue #534: Deterministic simulation environment tests ────────────────────

func TestSimEnvironment_Default(t *testing.T) {
	env := DefaultSimEnvironment()
	if env.DeterministicMode {
		t.Error("default environment should not be deterministic")
	}
	if env.IsDeterministic() {
		t.Error("default environment should not be deterministic")
	}
}

func TestSimEnvironment_Reproducible(t *testing.T) {
	env := ReproducibleSimEnvironment(1700000000, 12345)
	if !env.DeterministicMode {
		t.Error("reproducible environment should be deterministic")
	}
	if !env.IsDeterministic() {
		t.Error("reproducible environment should report deterministic")
	}
	if env.Timestamp != 1700000000 {
		t.Errorf("expected timestamp 1700000000, got %d", env.Timestamp)
	}
	if env.RandomSeed != 12345 {
		t.Errorf("expected seed 12345, got %d", env.RandomSeed)
	}
}

func TestSimEnvironment_Validate(t *testing.T) {
	// Valid deterministic
	env := ReproducibleSimEnvironment(1700000000, 1)
	if err := env.Validate(); err != nil {
		t.Errorf("expected valid, got: %v", err)
	}

	// Invalid: deterministic mode without timestamp
	invalidEnv := SimEnvironment{DeterministicMode: true, RandomSeed: 1}
	if err := invalidEnv.Validate(); err == nil {
		t.Error("expected error for deterministic mode without timestamp")
	}

	// Invalid: deterministic mode without seed
	invalidEnv2 := SimEnvironment{DeterministicMode: true, Timestamp: 100}
	if err := invalidEnv2.Validate(); err == nil {
		t.Error("expected error for deterministic mode without seed")
	}

	// Invalid: min > max close time
	invalidEnv3 := SimEnvironment{MinCloseTime: 100, MaxCloseTime: 50}
	if err := invalidEnv3.Validate(); err == nil {
		t.Error("expected error for min > max close time")
	}
}

func TestSimEnvironment_ApplyToRequest(t *testing.T) {
	env := ReproducibleSimEnvironment(1700000000, 42)
	env.LedgerSequence = 5
	req := &SimulationRequest{}
	env.ApplyToRequest(req)
	if req.Timestamp != 1700000000 {
		t.Errorf("expected timestamp 1700000000, got %d", req.Timestamp)
	}
	if req.LedgerSequence != 5 {
		t.Errorf("expected ledger sequence 5, got %d", req.LedgerSequence)
	}
}

func TestSimEnvironment_SessionMetadata(t *testing.T) {
	env := ReproducibleSimEnvironment(1700000000, 42)
	meta := env.ToSessionMetadata()
	if meta["timestamp"] != "1700000000" {
		t.Errorf("expected timestamp in metadata, got %v", meta["timestamp"])
	}
	if meta["random_seed"] != "42" {
		t.Errorf("expected seed in metadata, got %v", meta["random_seed"])
	}

	// Round-trip
	restored := FromSessionMetadata(meta)
	if restored.Timestamp != env.Timestamp {
		t.Error("round-trip failed for timestamp")
	}
	if restored.RandomSeed != env.RandomSeed {
		t.Error("round-trip failed for random_seed")
	}
}

func TestSimEnvironment_Fingerprint(t *testing.T) {
	env1 := ReproducibleSimEnvironment(100, 200)
	env2 := ReproducibleSimEnvironment(100, 200)
	env3 := ReproducibleSimEnvironment(100, 999)

	if env1.Fingerprint() != env2.Fingerprint() {
		t.Error("same environments should have same fingerprint")
	}
	if env1.Fingerprint() == env3.Fingerprint() {
		t.Error("different environments should have different fingerprints")
	}
}

// ── Issue #532: Host function call tests ──────────────────────────────────────

func TestHostCallArg_Normalize(t *testing.T) {
	arg := NormalizeHostCallArg("address", "GAAAA...address string that is very long")
	if arg.Type != "String" {
		t.Errorf("expected type String, got %s", arg.Type)
	}
}

func TestHostCallArg_Truncation(t *testing.T) {
	longValue := "x"
	for i := 0; i < MaxValueLen+100; i++ {
		longValue += "x"
	}
	arg := NormalizeHostCallArg("data", longValue)
	if !arg.Truncated {
		t.Error("expected truncation for long value")
	}
	if len(arg.Value) > MaxValueLen+3 { // +3 for "..."
		t.Error("truncated value is too long")
	}
}

func TestHostCallArg_Redaction(t *testing.T) {
	arg := NormalizeHostCallArg("secret_key", "SENSITIVE_DATA")
	if !arg.Redacted {
		t.Error("expected redaction for sensitive field")
	}
	if arg.Value != "REDACTED" {
		t.Errorf("expected REDACTED, got %s", arg.Value)
	}
}

func TestHostCallRecorder_Record(t *testing.T) {
	recorder := NewHostCallRecorder(100)
	recorder.Record(HostCallRecord{
		FunctionName: "put_ledger_entry",
		Step:        1,
		Arguments:   []HostCallArg{{Type: "Symbol", Value: "key1"}},
		Result:      &HostCallValue{Type: "Void", Value: "()"},
	})
	recorder.Record(HostCallRecord{
		FunctionName: "get_ledger_entry",
		Step:        2,
		Error:       "entry not found",
	})
	if recorder.Count() != 2 {
		t.Errorf("expected 2 records, got %d", recorder.Count())
	}
}

func TestHostCallRecorder_ByFunction(t *testing.T) {
	recorder := NewHostCallRecorder(100)
	recorder.Record(HostCallRecord{FunctionName: "require_auth", Step: 1})
	recorder.Record(HostCallRecord{FunctionName: "get_ledger_entry", Step: 2})
	recorder.Record(HostCallRecord{FunctionName: "require_auth", Step: 3})

	calls := recorder.ByFunction("require_auth")
	if len(calls) != 2 {
		t.Errorf("expected 2 require_auth calls, got %d", len(calls))
	}
}

func TestHostCallRecorder_FailedCalls(t *testing.T) {
	recorder := NewHostCallRecorder(100)
	recorder.Record(HostCallRecord{FunctionName: "fn1", Step: 1})
	recorder.Record(HostCallRecord{FunctionName: "fn2", Step: 2, Error: "failed"})

	failed := recorder.FailedCalls()
	if len(failed) != 1 {
		t.Errorf("expected 1 failed call, got %d", len(failed))
	}
}

func TestHostCallRecorder_MaxSize(t *testing.T) {
	recorder := NewHostCallRecorder(3)
	for i := 0; i < 5; i++ {
		recorder.Record(HostCallRecord{FunctionName: "fn", Step: i})
	}
	if recorder.Count() > 3 {
		t.Errorf("expected max 3 records, got %d", recorder.Count())
	}
}

// ── Issue #531: Resource limit reporting tests ───────────────────────────────

func TestResourceReport_BuildFromBudget(t *testing.T) {
	budget := &BudgetUsage{
		CPUInstructions: 50_000_000,
		MemoryBytes:     25_000_000,
		OperationsCount: 10,
		CPULimit:        100_000_000,
		MemoryLimit:     50_000_000,
		CPUUsagePercent: 50.0,
		MemoryUsagePercent: 50.0,
	}
	report := BuildResourceReport(budget)
	if !report.Available {
		t.Error("expected report to be available")
	}
	if report.CPUExceeded {
		t.Error("CPU should not be exceeded at 50%")
	}
	if report.MemoryExceeded {
		t.Error("memory should not be exceeded at 50%")
	}
	if report.CPURemaining != 50_000_000 {
		t.Errorf("expected 50M remaining CPU, got %d", report.CPURemaining)
	}
}

func TestResourceReport_ExceededCPU(t *testing.T) {
	budget := &BudgetUsage{
		CPUInstructions: 110_000_000,
		CPULimit:        100_000_000,
		CPUUsagePercent: 110.0,
		MemoryBytes:     25_000_000,
		MemoryLimit:     50_000_000,
		MemoryUsagePercent: 50.0,
	}
	report := BuildResourceReport(budget)
	if !report.CPUExceeded {
		t.Error("CPU should be exceeded")
	}
	if report.FirstExceeded != "cpu" {
		t.Errorf("expected first exceeded to be 'cpu', got %s", report.FirstExceeded)
	}
}

func TestResourceReport_ExceededMemory(t *testing.T) {
	budget := &BudgetUsage{
		CPUInstructions: 50_000_000,
		CPULimit:        100_000_000,
		CPUUsagePercent: 50.0,
		MemoryBytes:     60_000_000,
		MemoryLimit:     50_000_000,
		MemoryUsagePercent: 120.0,
	}
	report := BuildResourceReport(budget)
	if !report.MemoryExceeded {
		t.Error("memory should be exceeded")
	}
	if report.FirstExceeded != "memory" {
		t.Errorf("expected first exceeded to be 'memory', got %s", report.FirstExceeded)
	}
}

func TestResourceReport_NilBudget(t *testing.T) {
	report := BuildResourceReport(nil)
	if report.Available {
		t.Error("expected unavailable when budget is nil")
	}
}

func TestResourceReport_ToSummary(t *testing.T) {
	budget := &BudgetUsage{
		CPUInstructions: 5000, CPULimit: 10000, CPUUsagePercent: 50.0,
		MemoryBytes: 3000, MemoryLimit: 10000, MemoryUsagePercent: 30.0,
		OperationsCount: 5,
	}
	report := BuildResourceReport(budget)
	summary := report.ToSummary()
	if summary == "" {
		t.Error("expected non-empty summary")
	}
	if summary == "resources: unavailable" {
		t.Error("summary should show available resources")
	}
}

func TestResourceReport_ToMarkdownTable(t *testing.T) {
	budget := &BudgetUsage{
		CPUInstructions: 5000, CPULimit: 10000, CPUUsagePercent: 50.0,
		MemoryBytes: 3000, MemoryLimit: 10000, MemoryUsagePercent: 30.0,
	}
	report := BuildResourceReport(budget)
	md := report.ToMarkdownTable()
	if md == "" {
		t.Error("expected non-empty markdown table")
	}
}

func TestResourceReport_AttachToResponse(t *testing.T) {
	budget := &BudgetUsage{
		CPUInstructions: 5000, CPULimit: 10000, CPUUsagePercent: 50.0,
		MemoryBytes: 3000, MemoryLimit: 10000, MemoryUsagePercent: 30.0,
	}
	report := BuildResourceReport(budget)
	resp := &SimulationResponse{}
	AttachToResponse(resp, report)
	if resp.BudgetUsage == nil {
		t.Error("expected BudgetUsage to be set after attach")
	}
}
