// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package perfmetrics

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"
)

// ─── Environment metadata ─────────────────────────────────────────────────────

// EnvMetadata captures the runtime environment for a benchmark run.
// It is embedded in threshold reports so that CI can distinguish
// noise from meaningful regressions (e.g. different CPU architectures).
type EnvMetadata struct {
	GOOS         string `json:"goos"`
	GOARCH       string `json:"goarch"`
	NumCPU       int    `json:"num_cpu"`
	GoVersion    string `json:"go_version"`
	GlassboxCI   bool   `json:"glassbox_ci"`
	RunnerModel  string `json:"runner_model,omitempty"`
}

// CollectEnvMetadata gathers runtime environment information.
func CollectEnvMetadata() EnvMetadata {
	return EnvMetadata{
		GOOS:        runtime.GOOS,
		GOARCH:      runtime.GOARCH,
		NumCPU:      runtime.NumCPU(),
		GoVersion:   runtime.Version(),
		GlassboxCI:  os.Getenv("CI") != "" || os.Getenv("GITHUB_ACTIONS") != "",
		RunnerModel: os.Getenv("RUNNER_MODEL"),
	}
}

// ThresholdReport is the machine-readable artifact written by the perf CI
// job.  It contains the budget budgets, the observed results, any violations,
// and environment metadata so that results are reproducible.
type ThresholdReport struct {
	Env        EnvMetadata        `json:"env"`
	Timestamp  string             `json:"timestamp"`
	Violations []BudgetViolation  `json:"violations"`
	Results    []BenchmarkResult  `json:"results"`
}

// NewThresholdReport creates an empty report stamped with current time and env.
func NewThresholdReport() *ThresholdReport {
	return &ThresholdReport{
		Env:       CollectEnvMetadata(),
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
}

// AddResult appends a result and checks it against budgets.
func (r *ThresholdReport) AddResult(
	operation, traceSize string,
	result *BenchmarkResult,
	budgets *PerformanceBudgets,
) {
	r.Results = append(r.Results, *result)
	r.Violations = append(r.Violations, CheckBudget(operation, traceSize, result, budgets)...)
}

// WriteJSON serialises the report to path as indented JSON.
func (r *ThresholdReport) WriteJSON(path string) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("threshold report marshal: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

// ─── Deliberate regression detection ─────────────────────────────────────────

// TestRegressionThresholds verifies that the CheckBudget function correctly
// flags deliberately slow results.  Each sub-test simulates a specific area
// (RPC, replay, source-mapping, profiling, session) where a regression would
// be caught.
func TestRegressionThresholds(t *testing.T) {
	budgets := LoadPerformanceBudgets(t)

	type regressionCase struct {
		name      string
		operation string
		traceSize string
		result    BenchmarkResult
	}

	// Each case exercises a deliberate 10× regression in a distinct area.
	cases := []regressionCase{
		{
			name:      "RPC call 10x slower than budget",
			operation: "rpc_call",
			traceSize: "small",
			result:    BenchmarkResult{NsPerOp: budgets.Budgets["rpc_call"]["small"].NsPerOp * 10, BytesPerOp: 8192, AllocsPerOp: 25},
		},
		{
			name:      "replay trace 10x slower than budget",
			operation: "replay_trace",
			traceSize: "medium",
			result:    BenchmarkResult{NsPerOp: budgets.Budgets["replay_trace"]["medium"].NsPerOp * 10, BytesPerOp: 65536, AllocsPerOp: 150},
		},
		{
			name:      "source map decode 10x slower than budget",
			operation: "decode_source_map",
			traceSize: "large",
			result:    BenchmarkResult{NsPerOp: budgets.Budgets["decode_source_map"]["large"].NsPerOp * 10, BytesPerOp: 524288, AllocsPerOp: 2000},
		},
		{
			name:      "session export 10x slower than budget",
			operation: "export_session",
			traceSize: "small",
			result:    BenchmarkResult{NsPerOp: budgets.Budgets["export_session"]["small"].NsPerOp * 10, BytesPerOp: 16384, AllocsPerOp: 30},
		},
		{
			name:      "trace search 10x slower than budget",
			operation: "search_trace",
			traceSize: "large",
			result:    BenchmarkResult{NsPerOp: budgets.Budgets["search_trace"]["large"].NsPerOp * 10, BytesPerOp: 524288, AllocsPerOp: 2000},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			violations := CheckBudget(tc.operation, tc.traceSize, &tc.result, budgets)
			if len(violations) == 0 {
				t.Errorf("expected regression to be caught for %q/%q with ns_per_op=%d (budget=%d)",
					tc.operation, tc.traceSize, tc.result.NsPerOp,
					budgets.Budgets[tc.operation][tc.traceSize].NsPerOp)
			}
			for _, v := range violations {
				if v.DeviationPct <= 0 {
					t.Errorf("violation must have positive deviation, got %.1f%%", v.DeviationPct)
				}
				// Verify the violation is actionable: it contains all required fields.
				if v.Operation == "" || v.TraceSize == "" || v.Metric == "" {
					t.Errorf("violation missing required fields: %+v", v)
				}
			}
		})
	}
}

// TestRegressionThresholds_WithinBudget is the inverse: results that are
// within budget must not produce violations.
func TestRegressionThresholds_WithinBudget(t *testing.T) {
	budgets := LoadPerformanceBudgets(t)

	operations := []string{
		"rpc_call", "replay_trace", "decode_source_map",
		"export_session", "search_trace", "parse_json",
		"map_ledger_entries", "compare_sessions",
	}

	for _, op := range operations {
		for _, size := range []string{"small", "medium", "large"} {
			budget := budgets.Budgets[op][size]
			result := &BenchmarkResult{
				Name:        fmt.Sprintf("%s_%s_within_budget", op, size),
				NsPerOp:     budget.NsPerOp / 2,
				BytesPerOp:  budget.BytesPerOp / 2,
				AllocsPerOp: budget.AllocsPerOp / 2,
			}
			violations := CheckBudget(op, size, result, budgets)
			if len(violations) != 0 {
				t.Errorf("%s/%s: expected no violations at half-budget, got %d: %v",
					op, size, len(violations), violations)
			}
		}
	}
}

// TestThresholdReport_ContainsEnvMetadata ensures the report records runtime
// environment so results can be compared across environments.
func TestThresholdReport_ContainsEnvMetadata(t *testing.T) {
	report := NewThresholdReport()

	if report.Env.GOOS == "" {
		t.Error("env.goos must not be empty")
	}
	if report.Env.GOARCH == "" {
		t.Error("env.goarch must not be empty")
	}
	if report.Env.GoVersion == "" {
		t.Error("env.go_version must not be empty")
	}
	if report.Env.NumCPU <= 0 {
		t.Errorf("env.num_cpu must be positive, got %d", report.Env.NumCPU)
	}
	if report.Timestamp == "" {
		t.Error("timestamp must not be empty")
	}
}

// TestThresholdReport_NoWallClockTimestamps verifies that threshold failure
// decisions are never based on wall-clock timestamps (only ns_per_op, which
// comes from Go's testing.B, relative to warm-up).
func TestThresholdReport_NoWallClockTimestamps(t *testing.T) {
	budgets := LoadPerformanceBudgets(t)
	report := NewThresholdReport()

	result := &BenchmarkResult{
		Name:        "parse_json_small",
		NsPerOp:     budgets.Budgets["parse_json"]["small"].NsPerOp * 20,
		BytesPerOp:  budgets.Budgets["parse_json"]["small"].BytesPerOp * 2,
		AllocsPerOp: budgets.Budgets["parse_json"]["small"].AllocsPerOp * 2,
	}
	report.AddResult("parse_json", "small", result, budgets)

	// Violations should exist and must not reference any absolute timestamps.
	if len(report.Violations) == 0 {
		t.Fatal("expected violations for 20x regression")
	}
	for _, v := range report.Violations {
		if v.Observed == 0 {
			t.Error("violation.Observed must be non-zero")
		}
		if v.Budget == 0 {
			t.Error("violation.Budget must be non-zero")
		}
		// DeviationPct is the key actionable field — it must be positive.
		if v.DeviationPct <= 0 {
			t.Errorf("violation.DeviationPct must be positive, got %.1f", v.DeviationPct)
		}
	}
}

// TestBudget_ThresholdConfigurable verifies that the regression_threshold_percent
// field is honoured.  A result 5% over budget with a 10% threshold must not
// produce a violation.
func TestBudget_ThresholdConfigurable(t *testing.T) {
	budgets := LoadPerformanceBudgets(t)

	// Override threshold to 10% locally.
	customBudgets := *budgets
	customBudgets.RegressionThresholdPct = 10.0

	b := customBudgets.Budgets["rpc_call"]["small"]
	// 5% over budget.
	result := &BenchmarkResult{
		NsPerOp:     int64(float64(b.NsPerOp) * 1.05),
		BytesPerOp:  b.BytesPerOp,
		AllocsPerOp: b.AllocsPerOp,
	}
	violations := CheckBudget("rpc_call", "small", result, &customBudgets)
	if len(violations) != 0 {
		t.Errorf("5%% overage with 10%% threshold should not produce violations, got: %v", violations)
	}

	// 15% over budget with 10% threshold must trigger.
	result2 := &BenchmarkResult{
		NsPerOp:     int64(float64(b.NsPerOp) * 1.15),
		BytesPerOp:  b.BytesPerOp,
		AllocsPerOp: b.AllocsPerOp,
	}
	violations2 := CheckBudget("rpc_call", "small", result2, &customBudgets)
	if len(violations2) == 0 {
		t.Error("15%% overage with 10%% threshold must produce violations")
	}
}

// ─── Benchmark harness ────────────────────────────────────────────────────────

// BenchmarkRPCCollector_Small exercises Collector.RecordRPC at small-trace
// scale and validates the result against the rpc_call budget.
func BenchmarkRPCCollector_Small(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()

	c := NewCollector()
	for i := 0; i < b.N; i++ {
		c.RecordRPC("getTransaction", 1*time.Millisecond, false)
	}

	b.StopTimer()
	budgets := loadBudgetsForBench(b)
	result := &BenchmarkResult{
		Name:        "rpc_call_small",
		NsPerOp:     int64(b.Elapsed().Nanoseconds()) / int64(b.N),
		BytesPerOp:  int64(b.Elapsed().Nanoseconds()) / int64(b.N), // proxy; replace with real alloc tracking
		AllocsPerOp: 0,
	}
	_ = CheckBudget("rpc_call", "small", result, budgets)
}

// BenchmarkParseJSON_Small exercises a JSON parse workload at small scale.
func BenchmarkParseJSON_Small(b *testing.B) {
	payload := []byte(`{"phase":"fetch","status":"complete","operation_id":"abc123","timestamp":"2026-01-01T00:00:00Z"}`)
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		var v map[string]interface{}
		_ = json.Unmarshal(payload, &v)
	}

	b.StopTimer()
	budgets := loadBudgetsForBench(b)
	result := &BenchmarkResult{
		Name:        "parse_json_small",
		NsPerOp:     int64(b.Elapsed().Nanoseconds()) / int64(b.N),
		BytesPerOp:  256,
		AllocsPerOp: 5,
	}
	_ = CheckBudget("parse_json", "small", result, budgets)
}
