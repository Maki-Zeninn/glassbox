// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package report

import (
	"strings"
	"testing"

	"github.com/dotandev/glassbox/internal/trace"
)

func TestNewDiagnosticReportSeveritySummary(t *testing.T) {
	tr := trace.NewExecutionTrace("tx123", 0)
	tr.AddState(trace.ExecutionState{
		Operation:  "invoke",
		ContractID: "CA",
		Error:      "panic: index out of bounds",
	})
	tr.AddState(trace.ExecutionState{
		Operation:       "invoke",
		ContractID:      "CB",
		WasmInstruction: "unreachable",
	})

	r := NewDiagnosticReport(tr)
	if r.Counts["critical"] != 2 {
		t.Fatalf("critical count = %d, want 2", r.Counts["critical"])
	}
	if len(r.Diagnostics) != 2 {
		t.Fatalf("diagnostics len = %d, want 2", len(r.Diagnostics))
	}
}

func TestDiagnosticReportText(t *testing.T) {
	tr := trace.NewExecutionTrace("tx456", 0)
	tr.AddState(trace.ExecutionState{Operation: "invoke", Error: "failed"})

	text := NewDiagnosticReport(tr).Text()
	if !strings.Contains(text, "[HIGH] execution: Execution error") {
		t.Errorf("text output should contain execution error header, got:\n%s", text)
	}
}

func TestDiagnosticReport_WithSourceMapping_IncludesLocation(t *testing.T) {
	tr := trace.NewExecutionTrace("tx789", 0)
	tr.AddState(trace.ExecutionState{
		Operation:  "invoke",
		ContractID: "CTEST",
		Error:      "panic: index out of bounds",
		SourceFile: "src/contract.rs",
		SourceLine: 42,
	})

	r := NewDiagnosticReport(tr)
	if len(r.Diagnostics) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(r.Diagnostics))
	}

	d := r.Diagnostics[0]
	if d.Contract != "CTEST" {
		t.Errorf("expected Contract=CTEST, got %s", d.Contract)
	}
	if !strings.Contains(d.Action, "src/contract.rs") {
		t.Errorf("action should include source file, got: %s", d.Action)
	}
	if !strings.Contains(d.Action, ":42") {
		t.Errorf("action should include source line, got: %s", d.Action)
	}
}

func TestDiagnosticReport_WithoutSourceMapping_NoSourceHint(t *testing.T) {
	tr := trace.NewExecutionTrace("tx000", 0)
	tr.AddState(trace.ExecutionState{
		Operation:  "invoke",
		ContractID: "CTEST",
		Error:      "failed",
	})

	r := NewDiagnosticReport(tr)
	d := r.Diagnostics[0]
	if strings.Contains(d.Action, "Source:") {
		t.Errorf("action should not include source hint when no SourceRef, got: %s", d.Action)
	}
}
}
