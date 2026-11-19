// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"testing"
)

// TestEventSequencer_MonotonicSequenceIDs verifies that sequence IDs are
// assigned monotonically increasing.
func TestEventSequencer_MonotonicSequenceIDs(t *testing.T) {
	seq := NewEventSequencer()
	
	id1 := seq.NextSequenceID()
	id2 := seq.NextSequenceID()
	id3 := seq.NextSequenceID()
	
	if id1 >= id2 || id2 >= id3 {
		t.Errorf("Sequence IDs not monotonic: %d, %d, %d", id1, id2, id3)
	}
	
	if id1 != 1 {
		t.Errorf("First sequence ID should be 1, got %d", id1)
	}
}

// TestEventSequencer_ParentStack verifies parent relationship tracking.
func TestEventSequencer_ParentStack(t *testing.T) {
	seq := NewEventSequencer()
	
	// Initially no parent
	if seq.CurrentParent() != 0 {
		t.Errorf("Initial parent should be 0, got %d", seq.CurrentParent())
	}
	
	// Push parent
	parentID := seq.NextSequenceID()
	seq.PushParent(parentID)
	
	if seq.CurrentParent() != parentID {
		t.Errorf("Current parent should be %d, got %d", parentID, seq.CurrentParent())
	}
	
	// Push nested parent
	nestedParentID := seq.NextSequenceID()
	seq.PushParent(nestedParentID)
	
	if seq.CurrentParent() != nestedParentID {
		t.Errorf("Current parent should be %d, got %d", nestedParentID, seq.CurrentParent())
	}
	
	// Pop nested parent
	seq.PopParent()
	
	if seq.CurrentParent() != parentID {
		t.Errorf("After pop, current parent should be %d, got %d", parentID, seq.CurrentParent())
	}
	
	// Pop all parents
	seq.PopParent()
	
	if seq.CurrentParent() != 0 {
		t.Errorf("After popping all, parent should be 0, got %d", seq.CurrentParent())
	}
}

// TestEventSequencer_ConcurrentUse verifies thread safety.
func TestEventSequencer_ConcurrentUse(t *testing.T) {
	seq := NewEventSequencer()
	done := make(chan bool, 10)
	
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				seq.NextSequenceID()
			}
			done <- true
		}()
	}
	
	for i := 0; i < 10; i++ {
		<-done
	}
	
	// Verify we got exactly 1000 unique IDs
	if seq.nextSeqID != 1001 {
		t.Errorf("Expected 1001 next ID, got %d", seq.nextSeqID)
	}
}

// TestEventOrdering_SortStatesBySequenceID verifies deterministic sorting.
func TestEventOrdering_SortStatesBySequenceID(t *testing.T) {
	ordering := &EventOrdering{}
	
	states := []ExecutionState{
		{Step: 2, SequenceID: 3},
		{Step: 0, SequenceID: 1},
		{Step: 1, SequenceID: 2},
	}
	
	ordering.SortStatesBySequenceID(states)
	
	if states[0].SequenceID != 1 || states[1].SequenceID != 2 || states[2].SequenceID != 3 {
		t.Errorf("States not sorted by SequenceID")
	}
}

// TestEventOrdering_SortDiagnosticEvents verifies deterministic event sorting.
func TestEventOrdering_SortDiagnosticEvents(t *testing.T) {
	ordering := &EventOrdering{}
	
	contractID := "CONTRACT123"
	events := []DiagnosticEvent{
		{SequenceID: 3, ContractID: &contractID, EventType: "diagnostic"},
		{SequenceID: 1, ContractID: &contractID, EventType: "contract"},
		{SequenceID: 2, ContractID: &contractID, EventType: "system"},
	}
	
	ordering.SortDiagnosticEventsBySequenceID(events)
	
	if events[0].SequenceID != 1 || events[1].SequenceID != 2 || events[2].SequenceID != 3 {
		t.Errorf("Events not sorted by SequenceID")
	}
}

// TestEventOrdering_ValidateSequenceOrder detects violations.
func TestEventOrdering_ValidateSequenceOrder(t *testing.T) {
	ordering := &EventOrdering{}
	
	validStates := []ExecutionState{
		{SequenceID: 1},
		{SequenceID: 2},
		{SequenceID: 3},
	}
	
	if idx := ordering.ValidateSequenceOrder(validStates); idx != -1 {
		t.Errorf("Valid states reported violation at index %d", idx)
	}
	
	invalidStates := []ExecutionState{
		{SequenceID: 1},
		{SequenceID: 3},
		{SequenceID: 2}, // Violation: 2 < 3
	}
	
	if idx := ordering.ValidateSequenceOrder(invalidStates); idx != 2 {
		t.Errorf("Expected violation at index 2, got %d", idx)
	}
}

// TestEventOrdering_ValidateParentRelationships detects invalid parent references.
func TestEventOrdering_ValidateParentRelationships(t *testing.T) {
	ordering := &EventOrdering{}
	
	validStates := []ExecutionState{
		{SequenceID: 1, ParentSequenceID: 0}, // Top level
		{SequenceID: 2, ParentSequenceID: 1}, // Child of 1
		{SequenceID: 3, ParentSequenceID: 1}, // Another child of 1
	}
	
	if idx := ordering.ValidateParentRelationships(validStates); idx != -1 {
		t.Errorf("Valid parent relationships reported violation at index %d", idx)
	}
	
	// Test parent with larger sequence ID (invalid)
	invalidStates1 := []ExecutionState{
		{SequenceID: 1, ParentSequenceID: 2}, // Parent ID > child ID
	}
	
	if idx := ordering.ValidateParentRelationships(invalidStates1); idx != 0 {
		t.Errorf("Expected violation at index 0, got %d", idx)
	}
	
	// Test non-existent parent
	invalidStates2 := []ExecutionState{
		{SequenceID: 1, ParentSequenceID: 0},
		{SequenceID: 2, ParentSequenceID: 999}, // Parent doesn't exist
	}
	
	if idx := ordering.ValidateParentRelationships(invalidStates2); idx != 1 {
		t.Errorf("Expected violation at index 1, got %d", idx)
	}
}

// TestEventOrdering_ReindexSequenceIDs reassigns sequence IDs.
func TestEventOrdering_ReindexSequenceIDs(t *testing.T) {
	ordering := &EventOrdering{}
	
	states := []ExecutionState{
		{SequenceID: 10},
		{SequenceID: 20},
		{SequenceID: 30},
	}
	
	ordering.ReindexSequenceIDs(states)
	
	if states[0].SequenceID != 1 || states[1].SequenceID != 2 || states[2].SequenceID != 3 {
		t.Errorf("Sequence IDs not reindexed correctly")
	}
}

// TestEventOrdering_PreserveParentRelationships maps old to new IDs.
func TestEventOrdering_PreserveParentRelationships(t *testing.T) {
	ordering := &EventOrdering{}
	
	states := []ExecutionState{
		{SequenceID: 100, ParentSequenceID: 0},
		{SequenceID: 200, ParentSequenceID: 100},
		{SequenceID: 300, ParentSequenceID: 100},
	}
	
	ordering.ReindexSequenceIDs(states)
	ordering.PreserveParentRelationships(states)
	
	// After reindex: 100->1, 200->2, 300->3
	// Parent of state 2 should be 1 (was 100)
	// Parent of state 3 should be 1 (was 100)
	if states[1].ParentSequenceID != 1 {
		t.Errorf("Expected parent ID 1, got %d", states[1].ParentSequenceID)
	}
	if states[2].ParentSequenceID != 1 {
		t.Errorf("Expected parent ID 1, got %d", states[2].ParentSequenceID)
	}
}

// TestExecutionTrace_AddStateAssignsSequenceIDs verifies automatic sequence ID assignment.
func TestExecutionTrace_AddStateAssignsSequenceIDs(t *testing.T) {
	trace := NewExecutionTrace("test", 100)
	
	state1 := ExecutionState{Operation: "op1"}
	state2 := ExecutionState{Operation: "op2"}
	state3 := ExecutionState{Operation: "op3"}
	
	trace.AddState(state1)
	trace.AddState(state2)
	trace.AddState(state3)
	
	if trace.States[0].SequenceID != 1 {
		t.Errorf("Expected SequenceID 1, got %d", trace.States[0].SequenceID)
	}
	if trace.States[1].SequenceID != 2 {
		t.Errorf("Expected SequenceID 2, got %d", trace.States[1].SequenceID)
	}
	if trace.States[2].SequenceID != 3 {
		t.Errorf("Expected SequenceID 3, got %d", trace.States[2].SequenceID)
	}
}

// TestExecutionTrace_AddStateWithParentRelationships verifies parent tracking.
func TestExecutionTrace_AddStateWithParentRelationships(t *testing.T) {
	trace := NewExecutionTrace("test", 100)
	
	// Add top-level state
	parentState := ExecutionState{Operation: "parent"}
	trace.AddState(parentState)
	parentID := trace.States[0].SequenceID
	
	// Push parent and add child
	trace.sequencer.PushParent(parentID)
	childState := ExecutionState{Operation: "child"}
	trace.AddState(childState)
	
	if trace.States[1].ParentSequenceID != parentID {
		t.Errorf("Expected ParentSequenceID %d, got %d", parentID, trace.States[1].ParentSequenceID)
	}
	
	// Pop parent and add another top-level state
	trace.sequencer.PopParent()
	topLevelState := ExecutionState{Operation: "top_level"}
	trace.AddState(topLevelState)
	
	if trace.States[2].ParentSequenceID != 0 {
		t.Errorf("Expected ParentSequenceID 0 for top-level, got %d", trace.States[2].ParentSequenceID)
	}
}

// TestNestedAndSimultaneousEvents creates a fixture with nested and simultaneous events.
func TestNestedAndSimultaneousEvents(t *testing.T) {
	trace := NewExecutionTrace("nested_test", 10)
	
	// Top-level contract call
	contractID := "CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABSC4"
	trace.AddState(ExecutionState{
		Operation:  "contract_call",
		ContractID: contractID,
		Function:   "main",
	})
	mainID := trace.States[0].SequenceID
	
	// Push parent for nested calls
	trace.sequencer.PushParent(mainID)
	
	// Nested call 1
	trace.AddState(ExecutionState{
		Operation:  "contract_call",
		ContractID: contractID,
		Function:   "helper1",
	})
	
	// Nested call 2 (simultaneous with call 1 in execution order)
	trace.AddState(ExecutionState{
		Operation:  "contract_call",
		ContractID: contractID,
		Function:   "helper2",
	})
	
	// Host function within nested context
	trace.AddState(ExecutionState{
		Operation: "host_function",
		Function:  "get_ledger_entry",
	})
	
	// Pop back to top level
	trace.sequencer.PopParent()
	
	// Another top-level call
	trace.AddState(ExecutionState{
		Operation:  "contract_call",
		ContractID: contractID,
		Function:   "cleanup",
	})
	
	// Verify sequence IDs are monotonic
	ordering := &EventOrdering{}
	if idx := ordering.ValidateSequenceOrder(trace.States); idx != -1 {
		t.Errorf("Sequence order violation at index %d", idx)
	}
	
	// Verify parent relationships
	if idx := ordering.ValidateParentRelationships(trace.States); idx != -1 {
		t.Errorf("Parent relationship violation at index %d", idx)
	}
	
	// Verify nested states have correct parent
	if trace.States[1].ParentSequenceID != mainID {
		t.Errorf("Nested state 1 should have parent %d, got %d", mainID, trace.States[1].ParentSequenceID)
	}
	if trace.States[2].ParentSequenceID != mainID {
		t.Errorf("Nested state 2 should have parent %d, got %d", mainID, trace.States[2].ParentSequenceID)
	}
	if trace.States[3].ParentSequenceID != mainID {
		t.Errorf("Nested state 3 should have parent %d, got %d", mainID, trace.States[3].ParentSequenceID)
	}
	
	// Verify final state has no parent
	if trace.States[4].ParentSequenceID != 0 {
		t.Errorf("Final state should have no parent, got %d", trace.States[4].ParentSequenceID)
	}
}
