// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"testing"
	"time"
)

func TestCompareTracesNormalized_IdenticalTraces(t *testing.T) {
	baseline := createTestTrace("test123", 100, 50, 10)
	current := createTestTrace("test123", 100, 50, 10)

	config := DefaultComparisonConfig()
	result := CompareTracesNormalized(baseline, current, "Baseline", "Current", config)

	if result.HasRegression {
		t.Fatalf("Expected no regression for identical traces, got HasRegression=true")
	}
	if len(result.Diffs) > 0 {
		t.Fatalf("Expected no diffs for identical traces, got %d", len(result.Diffs))
	}
}

func TestCompareTracesNormalized_CPURegression(t *testing.T) {
	baseline := createTestTrace("test123", 1000, 50, 10)
	current := createTestTrace("test123", 1200, 50, 10) // 20% CPU increase

	config := DefaultComparisonConfig()
	result := CompareTracesNormalized(baseline, current, "Baseline", "Current", config)

	if !result.HasRegression {
		t.Fatalf("Expected regression for CPU increase, got HasRegression=false")
	}

	// Find CPU diff
	cpuDiff := findDiffByCategory(result.Diffs, "cpu")
	if cpuDiff == nil {
		t.Fatalf("Expected CPU diff, found none")
	}
	if cpuDiff.PercentDelta < 19.0 || cpuDiff.PercentDelta > 21.0 {
		t.Fatalf("Expected CPU delta ~20%%, got %.2f%%", cpuDiff.PercentDelta)
	}
	if cpuDiff.Severity != "warning" {
		t.Fatalf("Expected warning severity for 20%% increase, got %s", cpuDiff.Severity)
	}
}

func TestCompareTracesNormalized_CPUBelowThreshold(t *testing.T) {
	baseline := createTestTrace("test123", 10000, 50, 10)
	current := createTestTrace("test123", 10500, 50, 10) // 5% CPU increase

	config := DefaultComparisonConfig()
	config.CPUThresholdPct = 10.0
	result := CompareTracesNormalized(baseline, current, "Baseline", "Current", config)

	if result.HasRegression {
		t.Fatalf("Expected no regression for CPU increase below threshold")
	}

	cpuDiff := findDiffByCategory(result.Diffs, "cpu")
	if cpuDiff != nil && cpuDiff.Severity == "critical" || cpuDiff.Severity == "warning" {
		t.Fatalf("Expected info severity for increase below threshold, got %s", cpuDiff.Severity)
	}
}

func TestCompareTracesNormalized_MemoryRegression(t *testing.T) {
	baseline := createTestTrace("test123", 100, 1000, 10)
	current := createTestTrace("test123", 100, 1200, 10) // 20% memory increase

	config := DefaultComparisonConfig()
	result := CompareTracesNormalized(baseline, current, "Baseline", "Current", config)

	if !result.HasRegression {
		t.Fatalf("Expected regression for memory increase")
	}

	memDiff := findDiffByCategory(result.Diffs, "memory")
	if memDiff == nil {
		t.Fatalf("Expected memory diff, found none")
	}
	if memDiff.PercentDelta < 19.0 || memDiff.PercentDelta > 21.0 {
		t.Fatalf("Expected memory delta ~20%%, got %.2f%%", memDiff.PercentDelta)
	}
}

func TestCompareTracesNormalized_HostCallRegression(t *testing.T) {
	baseline := createTestTraceWithHostCalls("test123", 100, 50, map[string]int{
		"get_ledger_value": 10,
		"put_ledger_value": 5,
	})
	current := createTestTraceWithHostCalls("test123", 100, 50, map[string]int{
		"get_ledger_value": 15, // 50% increase
		"put_ledger_value": 5,
	})

	config := DefaultComparisonConfig()
	result := CompareTracesNormalized(baseline, current, "Baseline", "Current", config)

	hostCallDiffs := findDiffsByCategory(result.Diffs, "host_calls")
	if len(hostCallDiffs) == 0 {
		t.Fatalf("Expected host call diffs, found none")
	}

	// Check for specific function diff
	getLedgerDiff := findDiffByPath(hostCallDiffs, "host_call::get_ledger_value")
	if getLedgerDiff == nil {
		t.Fatalf("Expected diff for get_ledger_value, found none")
	}
	if getLedgerDiff.PercentDelta < 49.0 || getLedgerDiff.PercentDelta > 51.0 {
		t.Fatalf("Expected get_ledger_value delta ~50%%, got %.2f%%", getLedgerDiff.PercentDelta)
	}
}

func TestCompareTracesNormalized_NewHostFunction(t *testing.T) {
	baseline := createTestTraceWithHostCalls("test123", 100, 50, map[string]int{
		"get_ledger_value": 10,
	})
	current := createTestTraceWithHostCalls("test123", 100, 50, map[string]int{
		"get_ledger_value": 10,
		"has_contract_data": 5, // New function
	})

	config := DefaultComparisonConfig()
	result := CompareTracesNormalized(baseline, current, "Baseline", "Current", config)

	hostCallDiffs := findDiffsByCategory(result.Diffs, "host_calls")
	newFuncDiff := findDiffByPath(hostCallDiffs, "host_call::has_contract_data")
	if newFuncDiff == nil {
		t.Fatalf("Expected diff for new host function, found none")
	}
	if newFuncDiff.Severity != "warning" {
		t.Fatalf("Expected warning severity for new function, got %s", newFuncDiff.Severity)
	}
}

func TestCompareTracesNormalized_EventCountChange(t *testing.T) {
	baseline := createTestTrace("test123", 100, 50, 10)
	current := createTestTrace("test123", 100, 50, 15) // 50% more events

	config := DefaultComparisonConfig()
	result := CompareTracesNormalized(baseline, current, "Baseline", "Current", config)

	eventDiff := findDiffByCategory(result.Diffs, "events")
	if eventDiff == nil {
		t.Fatalf("Expected event count diff, found none")
	}
	if eventDiff.PercentDelta < 49.0 || eventDiff.PercentDelta > 51.0 {
		t.Fatalf("Expected event count delta ~50%%, got %.2f%%", eventDiff.PercentDelta)
	}
}

func TestCompareTracesNormalized_CallPathChange(t *testing.T) {
	baseline := createTestTraceWithCallPaths("test123", []string{"CABC123::init", "CABC123::transfer"})
	current := createTestTraceWithCallPaths("test123", []string{"CABC123::init", "CABC123::transfer", "CDEF456::approve"})

	config := DefaultComparisonConfig()
	result := CompareTracesNormalized(baseline, current, "Baseline", "Current", config)

	callPathDiffs := findDiffsByCategory(result.Diffs, "call_path")
	if len(callPathDiffs) == 0 {
		t.Fatalf("Expected call path diffs, found none")
	}

	// Should detect new path
	newPathDiff := findDiffByPath(callPathDiffs, "CDEF456::approve")
	if newPathDiff == nil {
		t.Fatalf("Expected diff for new call path, found none")
	}
}

func TestCompareTracesNormalized_AbsoluteThreshold(t *testing.T) {
	baseline := createTestTrace("test123", 100000, 50, 10)
	current := createTestTrace("test123", 100500, 50, 10) // 500 CPU increase (0.5%)

	config := DefaultComparisonConfig()
	config.CPUAbsoluteThreshold = 1000 // Ignore deltas < 1000
	result := CompareTracesNormalized(baseline, current, "Baseline", "Current", config)

	cpuDiff := findDiffByCategory(result.Diffs, "cpu")
	if cpuDiff != nil {
		t.Fatalf("Expected no CPU diff below absolute threshold, found diff with delta %.0f", cpuDiff.AbsoluteDelta)
	}
}

func TestCompareTracesNormalized_CriticalSeverity(t *testing.T) {
	baseline := createTestTrace("test123", 1000, 50, 10)
	current := createTestTrace("test123", 1300, 50, 10) // 30% CPU increase (2x threshold)

	config := DefaultComparisonConfig()
	config.CPUThresholdPct = 10.0
	result := CompareTracesNormalized(baseline, current, "Baseline", "Current", config)

	cpuDiff := findDiffByCategory(result.Diffs, "cpu")
	if cpuDiff == nil {
		t.Fatalf("Expected CPU diff, found none")
	}
	if cpuDiff.Severity != "critical" {
		t.Fatalf("Expected critical severity for 2x threshold increase, got %s", cpuDiff.Severity)
	}
}

func TestCompareTracesNormalized_ThresholdViolation(t *testing.T) {
	baseline := createTestTrace("test123", 1000, 50, 10)
	current := createTestTrace("test123", 1150, 50, 10) // 15% CPU increase

	config := DefaultComparisonConfig()
	config.CPUThresholdPct = 10.0
	config.FailOnThresholdViolation = true
	result := CompareTracesNormalized(baseline, current, "Baseline", "Current", config)

	if !result.HasRegression {
		t.Fatalf("Expected regression when FailOnThresholdViolation=true and threshold exceeded")
	}
	if result.Summary.ThresholdViolations == 0 {
		t.Fatalf("Expected threshold violations, got 0")
	}
}

func TestCompareTracesNormalized_InfoMode(t *testing.T) {
	baseline := createTestTrace("test123", 1000, 50, 10)
	current := createTestTrace("test123", 1300, 50, 10) // 30% CPU increase

	config := DefaultComparisonConfig()
	config.FailOnThresholdViolation = true
	result := CompareTracesNormalized(baseline, current, "Baseline", "Current", config)

	// Should have regression in normal mode
	if !result.HasRegression {
		t.Fatalf("Expected regression with FailOnThresholdViolation=true")
	}

	// Now test info mode
	config.FailOnThresholdViolation = false
	result = CompareTracesNormalized(baseline, current, "Baseline", "Current", config)

	// Should still have diffs but no regression flag
	if len(result.Diffs) == 0 {
		t.Fatalf("Expected diffs even in info mode")
	}
	if result.HasRegression {
		t.Fatalf("Expected no regression flag in info mode")
	}
}

func TestCompareTracesNormalized_SchemaVersion(t *testing.T) {
	baseline := createTestTrace("test123", 100, 50, 10)
	current := createTestTrace("test123", 100, 50, 10)

	config := DefaultComparisonConfig()
	result := CompareTracesNormalized(baseline, current, "Baseline", "Current", config)

	if result.SchemaVersion == "" {
		t.Fatalf("Expected schema version to be set")
	}
}

func TestDefaultComparisonConfig(t *testing.T) {
	config := DefaultComparisonConfig()

	if config.CPUThresholdPct != 10.0 {
		t.Fatalf("Expected CPU threshold 10.0, got %f", config.CPUThresholdPct)
	}
	if config.MemoryThresholdPct != 10.0 {
		t.Fatalf("Expected memory threshold 10.0, got %f", config.MemoryThresholdPct)
	}
	if config.HostCallThresholdPct != 5.0 {
		t.Fatalf("Expected host call threshold 5.0, got %f", config.HostCallThresholdPct)
	}
	if len(config.SuppressedFields) == 0 {
		t.Fatalf("Expected suppressed fields to be configured")
	}
}

// Helper functions

func createTestTrace(txHash string, cpu, memory uint64, stateCount int) *ExecutionTrace {
	trace := &ExecutionTrace{
		TransactionHash: txHash,
		StartTime:       time.Now(),
		EndTime:         time.Now().Add(time.Second),
		States:          make([]ExecutionState, stateCount),
	}

	for i := 0; i < stateCount; i++ {
		trace.States[i] = ExecutionState{
			Step:      i,
			Operation: "contract_call",
			ContractID: "CABC123",
			Function:  "test_function",
			Cost: &CostAnnotation{
				CPU:         cpu / uint64(stateCount),
				MemoryBytes: memory / uint64(stateCount),
			},
		}
	}

	return trace
}

func createTestTraceWithHostCalls(txHash string, cpu, memory uint64, hostCalls map[string]int) *ExecutionTrace {
	trace := &ExecutionTrace{
		TransactionHash: txHash,
		StartTime:       time.Now(),
		EndTime:         time.Now().Add(time.Second),
		States:          make([]ExecutionState, 0),
	}

	step := 0
	for funcName, count := range hostCalls {
		for i := 0; i < count; i++ {
			trace.States = append(trace.States, ExecutionState{
				Step:      step,
				Operation: "host_function",
				Function:  funcName,
				ContractID: "CABC123",
				Cost: &CostAnnotation{
					CPU:         cpu / uint64(len(hostCalls)),
					MemoryBytes: memory / uint64(len(hostCalls)),
				},
			})
			step++
		}
	}

	return trace
}

func createTestTraceWithCallPaths(txHash string, paths []string) *ExecutionTrace {
	trace := &ExecutionTrace{
		TransactionHash: txHash,
		StartTime:       time.Now(),
		EndTime:         time.Now().Add(time.Second),
		States:          make([]ExecutionState, len(paths)),
	}

	for i, path := range paths {
		parts := splitPath(path)
		trace.States[i] = ExecutionState{
			Step:      i,
			Operation: "contract_call",
			ContractID: parts[0],
			Function:  parts[1],
		}
	}

	return trace
}

func splitPath(path string) []string {
	// Simple split on "::"
	parts := make([]string, 2)
	for i, c := range path {
		if i > 0 && path[i-1:i+1] == "::" {
			parts[0] = path[:i-1]
			parts[1] = path[i+1:]
			break
		}
	}
	return parts
}

func findDiffByCategory(diffs []NormalizedDiff, category string) *NormalizedDiff {
	for _, diff := range diffs {
		if diff.Category == category {
			return &diff
		}
	}
	return nil
}

func findDiffsByCategory(diffs []NormalizedDiff, category string) []NormalizedDiff {
	var result []NormalizedDiff
	for _, diff := range diffs {
		if diff.Category == category {
			result = append(result, diff)
		}
	}
	return result
}

func findDiffByPath(diffs []NormalizedDiff, path string) *NormalizedDiff {
	for _, diff := range diffs {
		if diff.Path == path {
			return &diff
		}
	}
	return nil
}
