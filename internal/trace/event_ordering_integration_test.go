// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"encoding/json"
	"testing"
)

// TestFilteringPreservesSequenceOrder verifies that filtering operations
// preserve sequence IDs and parent relationships.
func TestFilteringPreservesSequenceOrder(t *testing.T) {
	trace := NewExecutionTrace("filter_test", 10)
	
	// Add states with sequence IDs
	contractID := "CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABSC4"
	for i := 0; i < 10; i++ {
		state := ExecutionState{
			Operation:  "contract_call",
			ContractID: contractID,
			Function:   "func",
		}
		if i%2 == 0 {
			state.Error = "error"
		}
		trace.AddState(state)
	}
	
	// Apply filter to get only error states
	filter := &FilterExpression{
		Severity: FilterSeverityError,
	}
	
	ft, err := ApplyFilter(trace, filter)
	if err != nil {
		t.Fatalf("ApplyFilter failed: %v", err)
	}
	
	// Verify matched steps preserve original sequence IDs
	for _, idx := range ft.MatchedSteps {
		originalSeqID := trace.States[idx].SequenceID
		if originalSeqID == 0 {
			t.Errorf("State at index %d has no sequence ID", idx)
		}
	}
	
	// Verify sequence IDs are still monotonic in matched steps
	for i := 1; i < len(ft.MatchedSteps); i++ {
		prevIdx := ft.MatchedSteps[i-1]
		currIdx := ft.MatchedSteps[i]
		if trace.States[currIdx].SequenceID <= trace.States[prevIdx].SequenceID {
			t.Errorf("Sequence IDs not monotonic in filtered result: %d <= %d",
				trace.States[currIdx].SequenceID, trace.States[prevIdx].SequenceID)
		}
	}
}

// TestSerializationPreservesSequenceOrder verifies that JSON serialization
// and deserialization preserve sequence IDs and parent relationships.
func TestSerializationPreservesSequenceOrder(t *testing.T) {
	trace := NewExecutionTrace("serialize_test", 10)
	
	// Add states with parent relationships
	contractID := "CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABSC4"
	
	// Top-level state
	trace.AddState(ExecutionState{
		Operation:  "contract_call",
		ContractID: contractID,
		Function:   "main",
	})
	parentID := trace.States[0].SequenceID
	
	// Nested states
	trace.sequencer.PushParent(parentID)
	for i := 0; i < 5; i++ {
		trace.AddState(ExecutionState{
			Operation:  "contract_call",
			ContractID: contractID,
			Function:   "nested",
		})
	}
	trace.sequencer.PopParent()
	
	// Serialize to JSON
	data, err := trace.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON failed: %v", err)
	}
	
	// Deserialize from JSON
	restored, err := FromJSON(data)
	if err != nil {
		t.Fatalf("FromJSON failed: %v", err)
	}
	
	// Verify sequence IDs are preserved
	for i := range trace.States {
		if restored.States[i].SequenceID != trace.States[i].SequenceID {
			t.Errorf("Sequence ID not preserved at index %d: got %d, want %d",
				i, restored.States[i].SequenceID, trace.States[i].SequenceID)
		}
		if restored.States[i].ParentSequenceID != trace.States[i].ParentSequenceID {
			t.Errorf("ParentSequenceID not preserved at index %d: got %d, want %d",
				i, restored.States[i].ParentSequenceID, trace.States[i].ParentSequenceID)
		}
	}
	
	// Verify sequence order is still valid
	ordering := &EventOrdering{}
	if idx := ordering.ValidateSequenceOrder(restored.States); idx != -1 {
		t.Errorf("Sequence order violation after deserialization at index %d", idx)
	}
}

// TestDiagnosticEventOrderingPreserved verifies that diagnostic events
// maintain their sequence IDs through transformations.
func TestDiagnosticEventOrderingPreserved(t *testing.T) {
	trace := NewExecutionTrace("diag_test", 10)
	
	// Add diagnostic events with sequence IDs
	contractID := "CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABSC4"
	events := []DiagnosticEvent{
		{EventType: "contract", ContractID: &contractID, SequenceID: 1},
		{EventType: "system", ContractID: &contractID, SequenceID: 2},
		{EventType: "diagnostic", ContractID: &contractID, SequenceID: 3},
	}
	
	trace.DiagnosticEvents = events
	
	// Serialize and deserialize
	data, err := json.Marshal(trace)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	
	var restored ExecutionTrace
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	
	// Verify sequence IDs preserved
	for i := range events {
		if restored.DiagnosticEvents[i].SequenceID != events[i].SequenceID {
			t.Errorf("Diagnostic event sequence ID not preserved at index %d", i)
		}
	}
}

// TestContractEventOrderingPreserved verifies that contract events
// maintain their sequence IDs through transformations.
func TestContractEventOrderingPreserved(t *testing.T) {
	events := []*ContractEvent{
		{ContractID: "C1", Type: "contract", SequenceID: 1},
		{ContractID: "C2", Type: "system", SequenceID: 2},
		{ContractID: "C3", Type: "diagnostic", SequenceID: 3},
	}
	
	// Serialize and deserialize
	data, err := json.Marshal(events)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	
	var restored []*ContractEvent
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	
	// Verify sequence IDs preserved
	for i := range events {
		if restored[i].SequenceID != events[i].SequenceID {
			t.Errorf("Contract event sequence ID not preserved at index %d", i)
		}
	}
}

// TestMergingPreservesSequenceOrder verifies that merging traces
// preserves sequence IDs by reindexing appropriately.
func TestMergingPreservesSequenceOrder(t *testing.T) {
	trace1 := NewExecutionTrace("trace1", 10)
	trace2 := NewExecutionTrace("trace2", 10)
	
	// Add states to both traces
	for i := 0; i < 5; i++ {
		trace1.AddState(ExecutionState{Operation: "op1"})
		trace2.AddState(ExecutionState{Operation: "op2"})
	}
	
	// Merge by appending
	merged := NewExecutionTrace("merged", 10)
	merged.States = append(merged.States, trace1.States...)
	merged.States = append(merged.States, trace2.States...)
	
	// Reindex to ensure monotonic sequence IDs
	ordering := &EventOrdering{}
	ordering.ReindexSequenceIDs(merged.States)
	
	// Verify sequence IDs are monotonic
	if idx := ordering.ValidateSequenceOrder(merged.States); idx != -1 {
		t.Errorf("Sequence order violation after merge at index %d", idx)
	}
	
	// Verify all states have sequence IDs
	for i, state := range merged.States {
		if state.SequenceID == 0 {
			t.Errorf("State at index %d has no sequence ID after merge", i)
		}
	}
}

// TestDiffWithSequenceIDs verifies that diff operations use sequence IDs
// for deterministic alignment.
func TestDiffWithSequenceIDs(t *testing.T) {
	trace1 := NewExecutionTrace("trace1", 10)
	trace2 := NewExecutionTrace("trace2", 10)
	
	// Add identical states with same sequence IDs
	contractID := "CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABSC4"
	for i := 0; i < 5; i++ {
		state := ExecutionState{
			Operation:  "contract_call",
			ContractID: contractID,
			Function:   "func",
		}
		trace1.AddState(state)
		trace2.AddState(state)
	}
	
	// Compute diff
	diff := ComputeTraceDiff(trace1, trace2)
	
	// Should be empty since traces are identical
	if !diff.IsEmpty {
		t.Errorf("Expected empty diff for identical traces with sequence IDs")
	}
	
	// Modify trace2
	trace2.States[2].Function = "different"
	
	// Compute diff again
	diff = ComputeTraceDiff(trace1, trace2)
	
	// Should detect the change
	if diff.IsEmpty {
		t.Errorf("Expected non-empty diff after modification")
	}
	
	// Verify divergence is detected
	if diff.DivergenceIdx < 0 {
		t.Errorf("Expected divergence index >= 0")
	}
}

// TestBackwardCompatibility verifies that traces without sequence IDs
// still work correctly (backward compatibility).
func TestBackwardCompatibility(t *testing.T) {
	// Create a trace without sequence IDs (old format)
	trace := &ExecutionTrace{
		TransactionHash: "old_trace",
		States: []ExecutionState{
			{Step: 0, Operation: "op1"},
			{Step: 1, Operation: "op2"},
			{Step: 2, Operation: "op3"},
		},
	}
	
	// Reindex to add sequence IDs
	ordering := &EventOrdering{}
	ordering.ReindexSequenceIDs(trace.States)
	
	// Verify sequence IDs are now present
	for i, state := range trace.States {
		if state.SequenceID != uint64(i+1) {
			t.Errorf("Expected sequence ID %d at index %d, got %d", i+1, i, state.SequenceID)
		}
	}
	
	// Verify sequence order is valid
	if idx := ordering.ValidateSequenceOrder(trace.States); idx != -1 {
		t.Errorf("Sequence order violation after reindexing at index %d", idx)
	}
}

// TestConcurrentEventCollection verifies that concurrent event collection
// with the sequencer produces deterministic ordering.
func TestConcurrentEventCollection(t *testing.T) {
	trace := NewExecutionTrace("concurrent_test", 10)
	
	// Simulate concurrent state additions
	done := make(chan bool, 10)
	
	for i := 0; i < 10; i++ {
		go func(idx int) {
			state := ExecutionState{
				Operation: "op",
			}
			trace.AddState(state)
			done <- true
		}(i)
	}
	
	for i := 0; i < 10; i++ {
		<-done
	}
	
	// Verify all states have unique sequence IDs
	seenIDs := make(map[uint64]bool)
	for _, state := range trace.States {
		if seenIDs[state.SequenceID] {
			t.Errorf("Duplicate sequence ID: %d", state.SequenceID)
		}
		seenIDs[state.SequenceID] = true
	}
	
	// Verify sequence IDs are monotonic
	ordering := &EventOrdering{}
	if idx := ordering.ValidateSequenceOrder(trace.States); idx != -1 {
		t.Errorf("Sequence order violation at index %d", idx)
	}
}
