// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"testing"
	"time"
)

// ── Issue #540: Trace filtering tests ─────────────────────────────────────────

func TestFilter_NoFilter(t *testing.T) {
	tr := makeTraceN("tx1", 5)
	expr := &FilterExpression{}
	if err := expr.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	ft, err := ApplyFilter(tr, expr)
	if err != nil {
		t.Fatalf("ApplyFilter: %v", err)
	}
	if len(ft.MatchedSteps) != 5 {
		t.Errorf("expected 5 matches with no filter, got %d", len(ft.MatchedSteps))
	}
}

func TestFilter_ByContractID(t *testing.T) {
	tr := NewExecutionTrace("tx2", 100)
	tr.StartTime = time.Now()
	tr.AddState(ExecutionState{Step: 0, ContractID: "CAAAA", Function: "fn1", EventType: EventTypeContractCall})
	tr.AddState(ExecutionState{Step: 1, ContractID: "CBBBB", Function: "fn2", EventType: EventTypeContractCall})
	tr.AddState(ExecutionState{Step: 2, ContractID: "CAAAA", Function: "fn3", EventType: EventTypeContractCall})

	expr := &FilterExpression{ContractID: "CAAAA"}
	ft, err := ApplyFilter(tr, expr)
	if err != nil {
		t.Fatalf("ApplyFilter: %v", err)
	}
	if len(ft.MatchedSteps) != 2 {
		t.Errorf("expected 2 matches for CAAAA, got %d", len(ft.MatchedSteps))
	}
}

func TestFilter_ByFunction(t *testing.T) {
	tr := NewExecutionTrace("tx3", 100)
	tr.StartTime = time.Now()
	tr.AddState(ExecutionState{Step: 0, ContractID: "C1", Function: "transfer", EventType: EventTypeContractCall})
	tr.AddState(ExecutionState{Step: 1, ContractID: "C2", Function: "balance", EventType: EventTypeContractCall})
	tr.AddState(ExecutionState{Step: 2, ContractID: "C3", Function: "transfer", EventType: EventTypeContractCall})

	expr := &FilterExpression{Function: "transfer"}
	ft, err := ApplyFilter(tr, expr)
	if err != nil {
		t.Fatalf("ApplyFilter: %v", err)
	}
	if len(ft.MatchedSteps) != 2 {
		t.Errorf("expected 2 matches for transfer, got %d", len(ft.MatchedSteps))
	}
}

func TestFilter_BySeverity(t *testing.T) {
	tr := NewExecutionTrace("tx4", 100)
	tr.StartTime = time.Now()
	tr.AddState(ExecutionState{Step: 0, ContractID: "C1", Function: "fn1", Error: "some error"})
	tr.AddState(ExecutionState{Step: 1, ContractID: "C2", Function: "fn2"})
	tr.AddState(ExecutionState{Step: 2, ContractID: "C3", Function: "fn3", Error: "another error"})

	expr := &FilterExpression{Severity: FilterSeverityError}
	ft, err := ApplyFilter(tr, expr)
	if err != nil {
		t.Fatalf("ApplyFilter: %v", err)
	}
	if len(ft.MatchedSteps) != 2 {
		t.Errorf("expected 2 error steps, got %d", len(ft.MatchedSteps))
	}
}

func TestFilter_ByStepRange(t *testing.T) {
	tr := makeTraceN("tx5", 10)
	expr := &FilterExpression{StepMin: 3, StepMax: 7}
	ft, err := ApplyFilter(tr, expr)
	if err != nil {
		t.Fatalf("ApplyFilter: %v", err)
	}
	if len(ft.MatchedSteps) != 5 {
		t.Errorf("expected 5 matches (steps 3-7), got %d", len(ft.MatchedSteps))
	}
}

func TestFilter_CombinedFilters(t *testing.T) {
	tr := NewExecutionTrace("tx6", 100)
	tr.StartTime = time.Now()
	tr.AddState(ExecutionState{Step: 0, ContractID: "CA", Function: "fn1", Error: "err"})
	tr.AddState(ExecutionState{Step: 1, ContractID: "CA", Function: "fn2"})
	tr.AddState(ExecutionState{Step: 2, ContractID: "CB", Function: "fn1", Error: "err"})

	expr := &FilterExpression{ContractID: "CA", Severity: FilterSeverityError}
	ft, err := ApplyFilter(tr, expr)
	if err != nil {
		t.Fatalf("ApplyFilter: %v", err)
	}
	if len(ft.MatchedSteps) != 1 {
		t.Errorf("expected 1 match (CA + error), got %d", len(ft.MatchedSteps))
	}
}

func TestFilter_DoesNotMutateSource(t *testing.T) {
	tr := makeTraceN("tx7", 5)
	originalCount := len(tr.States)
	expr := &FilterExpression{ContractID: "nonexistent"}
	_, err := ApplyFilter(tr, expr)
	if err != nil {
		t.Fatalf("ApplyFilter: %v", err)
	}
	if len(tr.States) != originalCount {
		t.Error("filter mutated the source trace")
	}
}

func TestFilter_InvalidSeverity(t *testing.T) {
	expr := &FilterExpression{Severity: "invalid"}
	if err := expr.Validate(); err == nil {
		t.Error("expected error for invalid severity")
	}
}

func TestFilter_InvalidStepRange(t *testing.T) {
	expr := &FilterExpression{StepMin: 10, StepMax: 5}
	if err := expr.Validate(); err == nil {
		t.Error("expected error for invalid step range")
	}
}

func TestFilter_NoMatch(t *testing.T) {
	tr := makeTraceN("tx8", 5)
	expr := &FilterExpression{ContractID: "ZZZZZ"}
	ft, err := ApplyFilter(tr, expr)
	if err != nil {
		t.Fatalf("ApplyFilter: %v", err)
	}
	if len(ft.MatchedSteps) != 0 {
		t.Errorf("expected 0 matches, got %d", len(ft.MatchedSteps))
	}
}

func TestFilterMetadata(t *testing.T) {
	tr := makeTraceN("tx9", 10)
	expr := &FilterExpression{StepMin: 0, StepMax: 4}
	ft, _ := ApplyFilter(tr, expr)
	meta := FilterMetadataFromTrace(ft)
	if meta.MatchedCount != 5 {
		t.Errorf("expected 5 matched, got %d", meta.MatchedCount)
	}
	if meta.TotalSteps != 10 {
		t.Errorf("expected 10 total, got %d", meta.TotalSteps)
	}
	expectedRatio := 0.5
	if meta.MatchRatio < expectedRatio-0.01 || meta.MatchRatio > expectedRatio+0.01 {
		t.Errorf("expected ratio ~%.2f, got %.4f", expectedRatio, meta.MatchRatio)
	}
}
