// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"strings"
	"testing"
	"time"
)

func fingerprintFixture() *ExecutionTrace {
	tr := NewExecutionTrace("txhash-1", 1)
	tr.AddState(ExecutionState{Operation: "init", ContractID: "C1", Function: "start"})
	tr.AddState(ExecutionState{Operation: "call", ContractID: "C1", Function: "transfer"})
	tr.AddState(ExecutionState{Operation: "call", ContractID: "C2", Function: "check", Error: "trap"})
	return tr
}

func TestFingerprintDeterministic(t *testing.T) {
	a := fingerprintFixture()
	b := fingerprintFixture()

	fpA, fpB := a.Fingerprint(), b.Fingerprint()
	if fpA == "" || len(fpA) != 64 {
		t.Fatalf("fingerprint should be a 64-char hex digest, got %q", fpA)
	}
	if fpA != fpB {
		t.Errorf("identical traces must produce identical fingerprints: %s vs %s", fpA, fpB)
	}
	if fpA != strings.ToLower(fpA) {
		t.Errorf("fingerprint should be lowercase hex, got %s", fpA)
	}
}

func TestFingerprintIgnoresVolatileFields(t *testing.T) {
	a := fingerprintFixture()
	base := a.Fingerprint()

	// Navigating the trace or re-stamping volatile fields must not change
	// the fingerprint, or every viewing session would orphan its own state.
	if _, err := a.JumpToStep(2); err != nil {
		t.Fatal(err)
	}
	a.StartTime = a.StartTime.Add(time.Hour)
	a.EndTime = a.EndTime.Add(time.Hour)
	a.States[0].Timestamp = a.States[0].Timestamp.Add(time.Hour)

	if got := a.Fingerprint(); got != base {
		t.Errorf("volatile fields changed the fingerprint: %s vs %s", got, base)
	}
}

func TestFingerprintSensitiveToSemanticChanges(t *testing.T) {
	base := fingerprintFixture().Fingerprint()

	differentTx := fingerprintFixture()
	differentTx.TransactionHash = "txhash-2"
	if differentTx.Fingerprint() == base {
		t.Error("changing the transaction hash must change the fingerprint")
	}

	extraStep := fingerprintFixture()
	extraStep.AddState(ExecutionState{Operation: "call", Function: "extra"})
	if extraStep.Fingerprint() == base {
		t.Error("adding a step must change the fingerprint")
	}

	editedStep := fingerprintFixture()
	editedStep.States[1].Function = "transferFrom"
	if editedStep.Fingerprint() == base {
		t.Error("changing a step's function must change the fingerprint")
	}
}

func TestFingerprintFieldsCannotCollideAcrossBoundaries(t *testing.T) {
	// "ab"+"c" in adjacent fields must not hash equal to "a"+"bc".
	a := NewExecutionTrace("tx", 1)
	a.AddState(ExecutionState{Operation: "ab", Function: "c"})
	b := NewExecutionTrace("tx", 1)
	b.AddState(ExecutionState{Operation: "a", Function: "bc"})

	if a.Fingerprint() == b.Fingerprint() {
		t.Error("field boundaries must be separated in the fingerprint input")
	}
}

func TestFingerprintNilTrace(t *testing.T) {
	var tr *ExecutionTrace
	if got := tr.Fingerprint(); got != "" {
		t.Errorf("nil trace fingerprint should be empty, got %q", got)
	}
}
