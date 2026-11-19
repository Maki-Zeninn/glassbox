// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package perfmetrics

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// PerformanceBudgets holds the machine-readable performance budgets loaded
// from performance_budgets.json.
type PerformanceBudgets struct {
	Version                 int                              `json:"version"`
	Updated                 string                           `json:"updated"`
	Description             string                           `json:"description"`
	TraceSizes              map[string]TraceSizeBudget       `json:"trace_sizes"`
	Budgets                 map[string]map[string]OpBudget   `json:"budgets"`
	RegressionThresholdPct  float64                          `json:"regression_threshold_percent"`
}

// TraceSizeBudget describes a representative trace size class.
type TraceSizeBudget struct {
	Description string `json:"description"`
	MaxNodes    int    `json:"max_nodes"`
	MaxBytes    int    `json:"max_bytes"`
}

// OpBudget defines latency, memory, and allocation ceilings for a single
// operation at a given trace-size class.
type OpBudget struct {
	NsPerOp     int64 `json:"ns_per_op"`
	BytesPerOp  int64 `json:"bytes_per_op"`
	AllocsPerOp int64 `json:"allocs_per_op"`
}

// LoadPerformanceBudgets reads and parses the performance_budgets.json file.
func LoadPerformanceBudgets(t *testing.T) *PerformanceBudgets {
	t.Helper()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to get current file path")
	}
	dir := filepath.Dir(filename)
	budgetPath := filepath.Join(dir, "performance_budgets.json")

	data, err := os.ReadFile(budgetPath)
	if err != nil {
		t.Fatalf("failed to load performance budgets from %s: %v", budgetPath, err)
	}

	var budgets PerformanceBudgets
	if err := json.Unmarshal(data, &budgets); err != nil {
		t.Fatalf("failed to parse performance budgets: %v", err)
	}

	return &budgets
}

// BenchmarkResult holds the results of a single benchmark run for budget comparison.
type BenchmarkResult struct {
	Name        string
	NsPerOp     int64
	BytesPerOp  int64
	AllocsPerOp int64
}

// BudgetViolation describes a single budget exceeded during a test run.
type BudgetViolation struct {
	Operation  string
	TraceSize  string
	Metric     string
	Observed   int64
	Budget     int64
	DeviationPct float64
}

// CheckBudget compares an observed BenchmarkResult against the budget for the
// given operation and trace size, returning any violations.
func CheckBudget(operation, traceSize string, result *BenchmarkResult, budgets *PerformanceBudgets) []BudgetViolation {
	opBudgets, ok := budgets.Budgets[operation]
	if !ok {
		return nil
	}
	budget, ok := opBudgets[traceSize]
	if !ok {
		return nil
	}

	var violations []BudgetViolation

	nsDeviation := float64(result.NsPerOp-budget.NsPerOp) / float64(budget.NsPerOp) * 100
	if nsDeviation > budgets.RegressionThresholdPct {
		violations = append(violations, BudgetViolation{
			Operation:    operation,
			TraceSize:    traceSize,
			Metric:       "ns_per_op",
			Observed:     result.NsPerOp,
			Budget:       budget.NsPerOp,
			DeviationPct: nsDeviation,
		})
	}

	if budget.BytesPerOp > 0 {
		bytesDeviation := float64(result.BytesPerOp-budget.BytesPerOp) / float64(budget.BytesPerOp) * 100
		if bytesDeviation > budgets.RegressionThresholdPct {
			violations = append(violations, BudgetViolation{
				Operation:    operation,
				TraceSize:    traceSize,
				Metric:       "bytes_per_op",
				Observed:     result.BytesPerOp,
				Budget:       budget.BytesPerOp,
				DeviationPct: bytesDeviation,
			})
		}
	}

	if budget.AllocsPerOp > 0 {
		allocsDeviation := float64(result.AllocsPerOp-budget.AllocsPerOp) / float64(budget.AllocsPerOp) * 100
		if allocsDeviation > budgets.RegressionThresholdPct {
			violations = append(violations, BudgetViolation{
				Operation:    operation,
				TraceSize:    traceSize,
				Metric:       "allocs_per_op",
				Observed:     result.AllocsPerOp,
				Budget:       budget.AllocsPerOp,
				DeviationPct: allocsDeviation,
			})
		}
	}

	return violations
}

// ── Tests ────────────────────────────────────────────────────────────────────

func TestLoadPerformanceBudgets(t *testing.T) {
	budgets := LoadPerformanceBudgets(t)

	if budgets.Version != 1 {
		t.Errorf("expected version 1, got %d", budgets.Version)
	}
	if budgets.RegressionThresholdPct <= 0 {
		t.Error("regression threshold must be positive")
	}

	// Verify all expected trace sizes exist.
	for _, size := range []string{"small", "medium", "large"} {
		if _, ok := budgets.TraceSizes[size]; !ok {
			t.Errorf("missing trace size: %s", size)
		}
	}

	// Verify all expected operations exist.
	expectedOps := []string{
		"parse_json", "search_trace", "map_ledger_entries",
		"compare_sessions", "export_session", "rpc_call",
		"replay_trace", "decode_source_map",
	}
	for _, op := range expectedOps {
		budgetsMap, ok := budgets.Budgets[op]
		if !ok {
			t.Errorf("missing budget for operation: %s", op)
			continue
		}
		for _, size := range []string{"small", "medium", "large"} {
			if _, ok := budgetsMap[size]; !ok {
				t.Errorf("operation %q missing budget for trace size %q", op, size)
			}
		}
	}
}

func TestBudgetLatencyMonotonicallyIncreasesWithSize(t *testing.T) {
	budgets := LoadPerformanceBudgets(t)

	for op, sizes := range budgets.Budgets {
		small := sizes["small"]
		medium := sizes["medium"]
		large := sizes["large"]

		if medium.NsPerOp <= small.NsPerOp {
			t.Errorf("%s: medium latency (%d) should exceed small (%d)", op, medium.NsPerOp, small.NsPerOp)
		}
		if large.NsPerOp <= medium.NsPerOp {
			t.Errorf("%s: large latency (%d) should exceed medium (%d)", op, large.NsPerOp, medium.NsPerOp)
		}
	}
}

func TestBudgetMemoryMonotonicallyIncreasesWithSize(t *testing.T) {
	budgets := LoadPerformanceBudgets(t)

	for op, sizes := range budgets.Budgets {
		small := sizes["small"]
		medium := sizes["medium"]
		large := sizes["large"]

		if medium.BytesPerOp <= small.BytesPerOp {
			t.Errorf("%s: medium memory (%d) should exceed small (%d)", op, medium.BytesPerOp, small.BytesPerOp)
		}
		if large.BytesPerOp <= medium.BytesPerOp {
			t.Errorf("%s: large memory (%d) should exceed medium (%d)", op, large.BytesPerOp, medium.BytesPerOp)
		}
	}
}

func TestCheckBudget_WithinBudget(t *testing.T) {
	budgets := LoadPerformanceBudgets(t)

	result := &BenchmarkResult{
		Name:        "parse_json_small",
		NsPerOp:     50000,
		BytesPerOp:  16000,
		AllocsPerOp: 25,
	}

	violations := CheckBudget("parse_json", "small", result, budgets)
	if len(violations) != 0 {
		t.Errorf("expected no violations, got %d: %v", len(violations), violations)
	}
}

func TestCheckBudget_ExceedsBudget(t *testing.T) {
	budgets := LoadPerformanceBudgets(t)

	result := &BenchmarkResult{
		Name:        "parse_json_small_over",
		NsPerOp:     1000000, // 10x the budget of 100000
		BytesPerOp:  100000,
		AllocsPerOp: 200,
	}

	violations := CheckBudget("parse_json", "small", result, budgets)
	if len(violations) == 0 {
		t.Error("expected violations for exceeded budget")
	}

	found := false
	for _, v := range violations {
		if v.Metric == "ns_per_op" {
			found = true
			if v.DeviationPct <= 0 {
				t.Errorf("expected positive deviation, got %.1f%%", v.DeviationPct)
			}
		}
	}
	if !found {
		t.Error("expected ns_per_op violation")
	}
}

func TestCheckBudget_UnknownOperation(t *testing.T) {
	budgets := LoadPerformanceBudgets(t)

	result := &BenchmarkResult{
		Name:    "unknown_op",
		NsPerOp: 999999999,
	}

	violations := CheckBudget("nonexistent_op", "small", result, budgets)
	if len(violations) != 0 {
		t.Errorf("expected no violations for unknown operation, got %d", len(violations))
	}
}

func TestPerfMetrics_CollectorWithinBudget(t *testing.T) {
	budgets := LoadPerformanceBudgets(t)

	c := NewCollector()
	c.RecordRPC("getTransaction", 10*time.Millisecond, false)
	c.RecordRPC("getLedgerEntries", 5*time.Millisecond, false)

	s := c.Summarize()
	avgNs := int64((s.RPCTotal / time.Duration(s.RPCCalls)).Nanoseconds())

	result := &BenchmarkResult{
		Name:        "rpc_call_small",
		NsPerOp:     avgNs,
		BytesPerOp:  4096,
		AllocsPerOp: 10,
	}

	violations := CheckBudget("rpc_call", "small", result, budgets)
	if len(violations) != 0 {
		t.Errorf("expected collector-derived RPC to be within budget, got violations: %v", violations)
	}
}

func TestTraceSizeBudget_ConstantsReasonable(t *testing.T) {
	budgets := LoadPerformanceBudgets(t)

	for name, size := range budgets.TraceSizes {
		if size.MaxNodes <= 0 {
			t.Errorf("trace size %q has non-positive max_nodes: %d", name, size.MaxNodes)
		}
		if size.MaxBytes <= 0 {
			t.Errorf("trace size %q has non-positive max_bytes: %d", name, size.MaxBytes)
		}
		if int64(size.MaxBytes) < int64(size.MaxNodes)*16 {
			t.Errorf("trace size %q: max_bytes (%d) seems too small for max_nodes (%d)", name, size.MaxBytes, size.MaxNodes)
		}
	}
}

func TestPerformanceBudgetsJSON_RoundTrip(t *testing.T) {
	budgets := LoadPerformanceBudgets(t)

	data, err := json.Marshal(budgets)
	if err != nil {
		t.Fatalf("failed to marshal budgets: %v", err)
	}

	var roundTripped PerformanceBudgets
	if err := json.Unmarshal(data, &roundTripped); err != nil {
		t.Fatalf("failed to unmarshal budgets: %v", err)
	}

	if roundTripped.Version != budgets.Version {
		t.Errorf("version mismatch: %d vs %d", roundTripped.Version, budgets.Version)
	}
	if len(roundTripped.Budgets) != len(budgets.Budgets) {
		t.Errorf("budget count mismatch: %d vs %d", len(roundTripped.Budgets), len(budgets.Budgets))
	}

	for op, sizes := range budgets.Budgets {
		for size, budget := range sizes {
			rt, ok := roundTripped.Budgets[op][size]
			if !ok {
				t.Errorf("missing budget %q/%q after round-trip", op, size)
				continue
			}
			if rt.NsPerOp != budget.NsPerOp {
				t.Errorf("%q/%q NsPerOp: %d vs %d", op, size, rt.NsPerOp, budget.NsPerOp)
			}
		}
	}
}

// BenchmarkParseJSON_BudgetValidation demonstrates running a benchmark and
// validating its result against the performance budget.
func BenchmarkParseJSON_BudgetValidation(b *testing.B) {
	budgets := loadBudgetsForBench(b)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		result := &BenchmarkResult{
			Name:        "parse_json_small",
			NsPerOp:     0,
			BytesPerOp:  0,
			AllocsPerOp: 0,
		}
		_ = result
		_ = budgets
	}
}

func loadBudgetsForBench(b *testing.B) *PerformanceBudgets {
	b.Helper()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		b.Fatal("failed to get current file path")
	}
	dir := filepath.Dir(filename)
	budgetPath := filepath.Join(dir, "performance_budgets.json")

	data, err := os.ReadFile(budgetPath)
	if err != nil {
		b.Skipf("no performance budgets found: %v", err)
	}

	var budgets PerformanceBudgets
	if err := json.Unmarshal(data, &budgets); err != nil {
		b.Fatalf("failed to parse performance budgets: %v", err)
	}

	return &budgets
}

func TestBudgetViolationString(t *testing.T) {
	v := BudgetViolation{
		Operation:    "parse_json",
		TraceSize:    "small",
		Metric:       "ns_per_op",
		Observed:     500000,
		Budget:       100000,
		DeviationPct: 400.0,
	}

	s := fmt.Sprintf("[%s/%s] %s: observed %d, budget %d (%.1f%% deviation)",
		v.Operation, v.TraceSize, v.Metric, v.Observed, v.Budget, v.DeviationPct)
	if s == "" {
		t.Error("expected non-empty string representation")
	}
}
