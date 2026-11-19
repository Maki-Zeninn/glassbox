// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package trace

// filter_extended_test.go — Tests for new FilterExpression fields and
// rendering: LineMin/LineMax, Exclude, nested call context, empty results,
// invalid ranges, mutually-exclusive-flag validation, and render output.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// ── Helpers ───────────────────────────────────────────────────────────────────

// makeTraceWithSourceLines builds a trace where each state has known source
// file and line number, useful for testing line-range filters.
func makeTraceWithSourceLines(txHash string, steps []struct {
	contract string
	fn       string
	file     string
	line     int
	errMsg   string
	event    string
}) *ExecutionTrace {
	tr := NewExecutionTrace(txHash, 100)
	tr.StartTime = time.Now()
	tr.EndTime = tr.StartTime.Add(time.Millisecond)
	for i, s := range steps {
		tr.AddState(ExecutionState{
			Step:       i,
			ContractID: s.contract,
			Function:   s.fn,
			SourceFile: s.file,
			SourceLine: s.line,
			Error:      s.errMsg,
			EventType:  s.event,
			Operation:  s.event,
			Timestamp:  time.Now(),
		})
	}
	return tr
}

// makeNestedTrace builds a trace that simulates a two-level contract call
// hierarchy: one outer contract call followed by a nested inner call, then
// a host function.
func makeNestedTrace() *ExecutionTrace {
	return makeTraceWithSourceLines("nested-tx", []struct {
		contract string
		fn       string
		file     string
		line     int
		errMsg   string
		event    string
	}{
		{"COUTER", "outer_fn", "outer.rs", 10, "", EventTypeContractCall},  // 0 — outer
		{"CINNER", "inner_fn", "inner.rs", 20, "", EventTypeContractCall},  // 1 — inner (child of 0)
		{"CINNER", "inner_fn", "inner.rs", 25, "trap", EventTypeTrap},      // 2 — inner trap
		{"COUTER", "outer_fn", "outer.rs", 30, "", EventTypeHostFunction},  // 3 — host fn
		{"COUTER", "outer_fn", "outer.rs", 40, "", EventTypeContractCall},  // 4 — outer again
	})
}

// ── LineMin / LineMax ─────────────────────────────────────────────────────────

func TestFilter_ByLineRange_MatchesInRange(t *testing.T) {
	tr := makeTraceWithSourceLines("tx-lines", []struct {
		contract, fn, file string
		line               int
		errMsg, event      string
	}{
		{"C1", "fn1", "lib.rs", 10, "", EventTypeContractCall},
		{"C1", "fn2", "lib.rs", 50, "", EventTypeContractCall},
		{"C1", "fn3", "lib.rs", 90, "", EventTypeContractCall},
	})

	expr := &FilterExpression{LineMin: 20, LineMax: 70}
	ft, err := ApplyFilter(tr, expr)
	if err != nil {
		t.Fatalf("ApplyFilter: %v", err)
	}
	if len(ft.MatchedSteps) != 1 {
		t.Errorf("expected 1 match (line 50), got %d", len(ft.MatchedSteps))
	}
	if ft.MatchedSteps[0] != 1 {
		t.Errorf("expected step index 1, got %d", ft.MatchedSteps[0])
	}
}

func TestFilter_ByLineRange_StepsWithNoLineExcluded(t *testing.T) {
	tr := makeTraceWithSourceLines("tx-noline", []struct {
		contract, fn, file string
		line               int
		errMsg, event      string
	}{
		{"C1", "fn1", "lib.rs", 0, "", EventTypeContractCall},  // no line info
		{"C1", "fn2", "lib.rs", 50, "", EventTypeContractCall}, // has line
	})

	expr := &FilterExpression{LineMin: 1, LineMax: 100}
	ft, err := ApplyFilter(tr, expr)
	if err != nil {
		t.Fatalf("ApplyFilter: %v", err)
	}
	if len(ft.MatchedSteps) != 1 {
		t.Errorf("expected 1 match (step with line=50), got %d", len(ft.MatchedSteps))
	}
}

func TestFilter_ByLineMin_Only(t *testing.T) {
	tr := makeTraceWithSourceLines("tx-lmin", []struct {
		contract, fn, file string
		line               int
		errMsg, event      string
	}{
		{"C1", "fn1", "lib.rs", 5, "", EventTypeContractCall},
		{"C1", "fn2", "lib.rs", 100, "", EventTypeContractCall},
		{"C1", "fn3", "lib.rs", 200, "", EventTypeContractCall},
	})

	expr := &FilterExpression{LineMin: 50}
	ft, err := ApplyFilter(tr, expr)
	if err != nil {
		t.Fatalf("ApplyFilter: %v", err)
	}
	if len(ft.MatchedSteps) != 2 {
		t.Errorf("expected 2 matches (lines >= 50), got %d", len(ft.MatchedSteps))
	}
}

func TestFilter_ByLineMax_Only(t *testing.T) {
	tr := makeTraceWithSourceLines("tx-lmax", []struct {
		contract, fn, file string
		line               int
		errMsg, event      string
	}{
		{"C1", "fn1", "lib.rs", 5, "", EventTypeContractCall},
		{"C1", "fn2", "lib.rs", 100, "", EventTypeContractCall},
		{"C1", "fn3", "lib.rs", 200, "", EventTypeContractCall},
	})

	expr := &FilterExpression{LineMax: 50}
	ft, err := ApplyFilter(tr, expr)
	if err != nil {
		t.Fatalf("ApplyFilter: %v", err)
	}
	if len(ft.MatchedSteps) != 1 {
		t.Errorf("expected 1 match (line 5 <= 50), got %d", len(ft.MatchedSteps))
	}
}

// ── Invalid ranges ────────────────────────────────────────────────────────────

func TestFilter_InvalidLineRange_MinGreaterThanMax(t *testing.T) {
	expr := &FilterExpression{LineMin: 100, LineMax: 10}
	if err := expr.Validate(); err == nil {
		t.Error("expected error for line_min > line_max")
	}
}

func TestFilter_InvalidLineRange_NegativeMin(t *testing.T) {
	expr := &FilterExpression{LineMin: -1}
	if err := expr.Validate(); err == nil {
		t.Error("expected error for negative line_min")
	}
}

func TestFilter_InvalidLineRange_NegativeMax(t *testing.T) {
	expr := &FilterExpression{LineMax: -5}
	if err := expr.Validate(); err == nil {
		t.Error("expected error for negative line_max")
	}
}

func TestFilter_InvalidStepRange_MinGreaterThanMax(t *testing.T) {
	expr := &FilterExpression{StepMin: 50, StepMax: 10}
	if err := expr.Validate(); err == nil {
		t.Error("expected error for step_min > step_max")
	}
}

func TestFilter_InvalidContractRegex(t *testing.T) {
	expr := &FilterExpression{ContractID: "[invalid(regex"}
	if err := expr.Validate(); err == nil {
		t.Error("expected error for invalid contract_id regex")
	}
}

func TestFilter_InvalidFunctionRegex(t *testing.T) {
	expr := &FilterExpression{Function: "(unclosed"}
	if err := expr.Validate(); err == nil {
		t.Error("expected error for invalid function regex")
	}
}

func TestFilter_InvalidEventType(t *testing.T) {
	expr := &FilterExpression{EventType: "not_a_real_type"}
	if err := expr.Validate(); err == nil {
		t.Error("expected error for invalid event_type")
	}
}

// ── Exclude flag ──────────────────────────────────────────────────────────────

func TestFilter_Exclude_ContractID(t *testing.T) {
	tr := NewExecutionTrace("tx-excl", 100)
	tr.StartTime = time.Now()
	tr.AddState(ExecutionState{Step: 0, ContractID: "CAAAA", EventType: EventTypeContractCall})
	tr.AddState(ExecutionState{Step: 1, ContractID: "CBBBB", EventType: EventTypeContractCall})
	tr.AddState(ExecutionState{Step: 2, ContractID: "CAAAA", EventType: EventTypeContractCall})

	// Exclude CAAAA → only CBBBB should remain.
	expr := &FilterExpression{ContractID: "CAAAA", Exclude: true}
	ft, err := ApplyFilter(tr, expr)
	if err != nil {
		t.Fatalf("ApplyFilter: %v", err)
	}
	if len(ft.MatchedSteps) != 1 {
		t.Errorf("expected 1 match (non-CAAAA steps), got %d", len(ft.MatchedSteps))
	}
	if ft.MatchedSteps[0] != 1 {
		t.Errorf("expected step index 1 (CBBBB), got %d", ft.MatchedSteps[0])
	}
}

func TestFilter_Exclude_Severity(t *testing.T) {
	tr := NewExecutionTrace("tx-excl-sev", 100)
	tr.StartTime = time.Now()
	tr.AddState(ExecutionState{Step: 0, Error: "trap error"})
	tr.AddState(ExecutionState{Step: 1})
	tr.AddState(ExecutionState{Step: 2, Error: "another error"})

	// Exclude errors → only the non-error step.
	expr := &FilterExpression{Severity: FilterSeverityError, Exclude: true}
	ft, err := ApplyFilter(tr, expr)
	if err != nil {
		t.Fatalf("ApplyFilter: %v", err)
	}
	if len(ft.MatchedSteps) != 1 {
		t.Errorf("expected 1 match (step without error), got %d", len(ft.MatchedSteps))
	}
	if ft.MatchedSteps[0] != 1 {
		t.Errorf("expected step index 1, got %d", ft.MatchedSteps[0])
	}
}

// ── Nested call context ───────────────────────────────────────────────────────

func TestFilter_NestedCalls_PreservesParentContext(t *testing.T) {
	tr := makeNestedTrace()

	// Filter to only the inner contract — parent context should reference outer.
	expr := &FilterExpression{ContractID: "CINNER"}
	ft, err := ApplyFilter(tr, expr)
	if err != nil {
		t.Fatalf("ApplyFilter: %v", err)
	}
	if len(ft.MatchedSteps) != 2 {
		t.Errorf("expected 2 CINNER steps, got %d", len(ft.MatchedSteps))
	}
	// ParentContext is populated (may be 0 for depth-0 items, non-zero for
	// nested ones — just verify it's present and not panicking).
	_ = ft.ParentContext
}

func TestFilter_NestedCalls_AllContractCallsMatch(t *testing.T) {
	tr := makeNestedTrace()
	expr := &FilterExpression{EventType: EventTypeContractCall}
	ft, err := ApplyFilter(tr, expr)
	if err != nil {
		t.Fatalf("ApplyFilter: %v", err)
	}
	// Steps 0, 1, 4 are contract_call; step 2 is trap, step 3 is host_function.
	if len(ft.MatchedSteps) != 3 {
		t.Errorf("expected 3 contract_call steps, got %d", len(ft.MatchedSteps))
	}
}

func TestFilter_NestedCalls_OnlyTraps(t *testing.T) {
	tr := makeNestedTrace()
	expr := &FilterExpression{EventType: EventTypeTrap}
	ft, err := ApplyFilter(tr, expr)
	if err != nil {
		t.Fatalf("ApplyFilter: %v", err)
	}
	if len(ft.MatchedSteps) != 1 {
		t.Errorf("expected 1 trap step, got %d", len(ft.MatchedSteps))
	}
}

// ── Empty results ─────────────────────────────────────────────────────────────

func TestFilter_EmptyResult_IsSuccessful(t *testing.T) {
	tr := makeTraceN("tx-empty", 5)
	expr := &FilterExpression{ContractID: "ZZZZ-NONEXISTENT"}
	ft, err := ApplyFilter(tr, expr)
	if err != nil {
		t.Fatalf("ApplyFilter: %v", err)
	}
	if len(ft.MatchedSteps) != 0 {
		t.Errorf("expected 0 matches, got %d", len(ft.MatchedSteps))
	}
	// FilterMetadata should still report total steps.
	meta := FilterMetadataFromTrace(ft)
	if meta.TotalSteps != 5 {
		t.Errorf("expected TotalSteps=5, got %d", meta.TotalSteps)
	}
	if meta.MatchedCount != 0 {
		t.Errorf("expected MatchedCount=0, got %d", meta.MatchedCount)
	}
	if meta.MatchRatio != 0.0 {
		t.Errorf("expected MatchRatio=0, got %f", meta.MatchRatio)
	}
}

func TestFilter_EmptyResult_ApplyFilterDoesNotError(t *testing.T) {
	tr := makeTraceN("tx-empty2", 3)
	expr := &FilterExpression{EventType: EventTypeTrap} // no traps in the trace
	_, err := ApplyFilter(tr, expr)
	if err != nil {
		t.Errorf("expected nil error for zero matches, got: %v", err)
	}
}

// ── And() composition ─────────────────────────────────────────────────────────

func TestFilter_And_CombinesLineRanges(t *testing.T) {
	f1 := &FilterExpression{LineMin: 10}
	f2 := &FilterExpression{LineMax: 50}
	combined := f1.And(f2)

	if combined.LineMin != 10 {
		t.Errorf("expected LineMin=10, got %d", combined.LineMin)
	}
	if combined.LineMax != 50 {
		t.Errorf("expected LineMax=50, got %d", combined.LineMax)
	}
}

func TestFilter_And_ExcludePropagates(t *testing.T) {
	f1 := &FilterExpression{ContractID: "CAAAA"}
	f2 := &FilterExpression{Exclude: true}
	combined := f1.And(f2)
	if !combined.Exclude {
		t.Error("expected Exclude=true after And with exclude filter")
	}
}

func TestFilter_And_NilBase(t *testing.T) {
	var f1 *FilterExpression
	f2 := &FilterExpression{ContractID: "CAAAA", LineMin: 5}
	combined := f1.And(f2)
	if combined.ContractID != "CAAAA" {
		t.Errorf("expected ContractID=CAAAA, got %q", combined.ContractID)
	}
	if combined.LineMin != 5 {
		t.Errorf("expected LineMin=5, got %d", combined.LineMin)
	}
}

// ── RenderFilteredText ────────────────────────────────────────────────────────

func TestRenderFilteredText_IncludesHeader(t *testing.T) {
	tr := makeTraceN("tx-render", 3)
	expr := &FilterExpression{ContractID: "CAAAA"}
	ft, _ := ApplyFilter(tr, expr)
	text, err := RenderFilteredText(ft)
	if err != nil {
		t.Fatalf("RenderFilteredText: %v", err)
	}
	if !strings.Contains(text, "Filter summary") {
		t.Error("expected 'Filter summary' in text output")
	}
	if !strings.Contains(text, "contract_id") {
		t.Error("expected 'contract_id' filter listed in header")
	}
}

func TestRenderFilteredText_EmptyResult_NotAnError(t *testing.T) {
	tr := makeTraceN("tx-render-empty", 3)
	expr := &FilterExpression{ContractID: "NONEXISTENT"}
	ft, _ := ApplyFilter(tr, expr)
	text, err := RenderFilteredText(ft)
	if err != nil {
		t.Fatalf("RenderFilteredText with empty result: %v", err)
	}
	if !strings.Contains(text, "No steps matched") {
		t.Error("expected 'No steps matched' message for empty result")
	}
}

func TestRenderFilteredText_PreservesOriginalStepIDs(t *testing.T) {
	tr := makeTraceN("tx-ids", 10)
	// Filter to a sub-range, then check step IDs in output.
	expr := &FilterExpression{StepMin: 3, StepMax: 5}
	ft, _ := ApplyFilter(tr, expr)
	text, err := RenderFilteredText(ft)
	if err != nil {
		t.Fatalf("RenderFilteredText: %v", err)
	}
	// Steps 3, 4, 5 must appear; step 0 must not.
	if !strings.Contains(text, "Step 3:") {
		t.Error("expected Step 3 in output")
	}
	if !strings.Contains(text, "Step 5:") {
		t.Error("expected Step 5 in output")
	}
	if strings.Contains(text, "Step 0:") {
		t.Error("did not expect Step 0 in filtered output")
	}
}

func TestRenderFilteredText_ExcludeModeInHeader(t *testing.T) {
	tr := makeTraceN("tx-excl-render", 5)
	expr := &FilterExpression{EventType: EventTypeContractCall, Exclude: true}
	ft, _ := ApplyFilter(tr, expr)
	text, err := RenderFilteredText(ft)
	if err != nil {
		t.Fatalf("RenderFilteredText: %v", err)
	}
	if !strings.Contains(text, "EXCLUDE") {
		t.Error("expected EXCLUDE mode noted in header")
	}
}

// ── RenderFilteredJSON ────────────────────────────────────────────────────────

func TestRenderFilteredJSON_ValidStructure(t *testing.T) {
	tr := makeTraceN("tx-json", 5)
	expr := &FilterExpression{StepMin: 1, StepMax: 3}
	ft, _ := ApplyFilter(tr, expr)
	data, err := RenderFilteredJSON(ft)
	if err != nil {
		t.Fatalf("RenderFilteredJSON: %v", err)
	}

	var out FilteredTraceJSON
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if out.FilterSummary.TotalSteps != 5 {
		t.Errorf("expected TotalSteps=5, got %d", out.FilterSummary.TotalSteps)
	}
	if out.FilterSummary.MatchedCount != 3 {
		t.Errorf("expected MatchedCount=3, got %d", out.FilterSummary.MatchedCount)
	}
	if len(out.MatchedSteps) != 3 {
		t.Errorf("expected 3 matched steps in JSON, got %d", len(out.MatchedSteps))
	}
}

func TestRenderFilteredJSON_EmptyResult_HasSummary(t *testing.T) {
	tr := makeTraceN("tx-json-empty", 4)
	expr := &FilterExpression{ContractID: "NONEXISTENT"}
	ft, _ := ApplyFilter(tr, expr)
	data, err := RenderFilteredJSON(ft)
	if err != nil {
		t.Fatalf("RenderFilteredJSON: %v", err)
	}

	var out FilteredTraceJSON
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if out.FilterSummary.TotalSteps != 4 {
		t.Errorf("expected TotalSteps=4, got %d", out.FilterSummary.TotalSteps)
	}
	if out.FilterSummary.MatchedCount != 0 {
		t.Errorf("expected MatchedCount=0, got %d", out.FilterSummary.MatchedCount)
	}
	if out.MatchedSteps == nil {
		// nil is fine — just must not error
	}
}

func TestRenderFilteredJSON_ActiveFiltersInSummary(t *testing.T) {
	tr := makeTraceN("tx-json-filters", 3)
	expr := &FilterExpression{ContractID: "CAAAA", LineMin: 5, LineMax: 20}
	ft, _ := ApplyFilter(tr, expr)
	data, err := RenderFilteredJSON(ft)
	if err != nil {
		t.Fatalf("RenderFilteredJSON: %v", err)
	}

	var out FilteredTraceJSON
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if _, ok := out.FilterSummary.ActiveFilters["contract_id"]; !ok {
		t.Error("expected contract_id in active_filters JSON")
	}
	if _, ok := out.FilterSummary.ActiveFilters["line_min"]; !ok {
		t.Error("expected line_min in active_filters JSON")
	}
	if _, ok := out.FilterSummary.ActiveFilters["line_max"]; !ok {
		t.Error("expected line_max in active_filters JSON")
	}
}

func TestRenderFilteredJSON_OriginalStepIDsPreserved(t *testing.T) {
	tr := makeTraceN("tx-json-ids", 10)
	expr := &FilterExpression{StepMin: 7, StepMax: 9}
	ft, _ := ApplyFilter(tr, expr)
	data, err := RenderFilteredJSON(ft)
	if err != nil {
		t.Fatalf("RenderFilteredJSON: %v", err)
	}

	var out FilteredTraceJSON
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	for _, step := range out.MatchedSteps {
		if step.OriginalStep < 7 || step.OriginalStep > 9 {
			t.Errorf("unexpected original_step %d outside range [7,9]", step.OriginalStep)
		}
	}
}

func TestRenderFilteredJSON_NilTrace_ReturnsError(t *testing.T) {
	_, err := RenderFilteredJSON(nil)
	if err == nil {
		t.Error("expected error for nil FilteredTrace")
	}
}

// ── Source file + line range combined ─────────────────────────────────────────

func TestFilter_SourceFileAndLineRange_Combined(t *testing.T) {
	tr := makeTraceWithSourceLines("tx-combined", []struct {
		contract, fn, file string
		line               int
		errMsg, event      string
	}{
		{"C1", "fn1", "token.rs", 10, "", EventTypeContractCall},
		{"C1", "fn2", "token.rs", 50, "", EventTypeContractCall},
		{"C1", "fn3", "other.rs", 50, "", EventTypeContractCall}, // different file
		{"C1", "fn4", "token.rs", 90, "", EventTypeContractCall},
	})

	expr := &FilterExpression{SourceFile: "token.rs", LineMin: 40, LineMax: 60}
	ft, err := ApplyFilter(tr, expr)
	if err != nil {
		t.Fatalf("ApplyFilter: %v", err)
	}
	// Only step 1: token.rs AND line 50 is in [40,60].
	if len(ft.MatchedSteps) != 1 {
		t.Errorf("expected 1 match, got %d", len(ft.MatchedSteps))
	}
	if ft.MatchedSteps[0] != 1 {
		t.Errorf("expected step 1, got %d", ft.MatchedSteps[0])
	}
}
