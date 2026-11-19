// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"context"
	"testing"
	"time"

	"github.com/dotandev/glassbox/internal/simulator"
	"github.com/dotandev/glassbox/internal/snapshot"
)

// mockRunner implements simulator.RunnerInterface for testing
type mockRunner struct {
	runCalled bool
	runError  error
}

func (m *mockRunner) Run(ctx context.Context, req *simulator.SimulationRequest) (*simulator.SimulationResponse, error) {
	m.runCalled = true
	if m.runError != nil {
		return nil, m.runError
	}
	return &simulator.SimulationResponse{
		Status: "success",
	}, nil
}

func (m *mockRunner) Close() error {
	return nil
}

func createRefreshTestTrace(numSteps int) *ExecutionTrace {
	trace := NewExecutionTrace("test_tx_hash", DefaultSnapshotInterval)
	
	for i := 0; i < numSteps; i++ {
		state := ExecutionState{
			Step:       i,
			Operation:  "contract_call",
			ContractID: "CTEST123",
			Function:   "test_function",
			HostState:  map[string]interface{}{"balance": i * 100},
			Memory:     map[string]interface{}{"mem_addr": i},
		}
		trace.AddState(state)
	}
	
	return trace
}

func TestNewIncrementalRefresher(t *testing.T) {
	runner := &mockRunner{}
	refresher := NewIncrementalRefresher(runner)
	
	if refresher == nil {
		t.Fatal("expected non-nil refresher")
	}
	if refresher.runner != runner {
		t.Error("runner not set correctly")
	}
}

func TestRefresh_InvalidRequest(t *testing.T) {
	runner := &mockRunner{}
	refresher := NewIncrementalRefresher(runner)
	ctx := context.Background()
	
	tests := []struct {
		name string
		req  *RefreshRequest
	}{
		{
			name: "nil original trace",
			req: &RefreshRequest{
				OriginalTrace:   nil,
				UpdatedSnapshot: snapshot.FromMap(map[string]string{"k": "v"}),
				StartStep:       0,
				EndStep:         5,
			},
		},
		{
			name: "nil updated snapshot",
			req: &RefreshRequest{
				OriginalTrace:   createRefreshTestTrace(10),
				UpdatedSnapshot: nil,
				StartStep:       0,
				EndStep:         5,
			},
		},
		{
			name: "invalid start step",
			req: &RefreshRequest{
				OriginalTrace:   createRefreshTestTrace(10),
				UpdatedSnapshot: snapshot.FromMap(map[string]string{"k": "v"}),
				StartStep:       -1,
				EndStep:         5,
			},
		},
		{
			name: "start step >= trace length",
			req: &RefreshRequest{
				OriginalTrace:   createRefreshTestTrace(10),
				UpdatedSnapshot: snapshot.FromMap(map[string]string{"k": "v"}),
				StartStep:       10,
				EndStep:         15,
			},
		},
		{
			name: "end step < start step",
			req: &RefreshRequest{
				OriginalTrace:   createRefreshTestTrace(10),
				UpdatedSnapshot: snapshot.FromMap(map[string]string{"k": "v"}),
				StartStep:       5,
				EndStep:         3,
			},
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := refresher.Refresh(ctx, tt.req)
			if err == nil {
				t.Error("expected error for invalid request")
			}
		})
	}
}

func TestRefresh_Success(t *testing.T) {
	runner := &mockRunner{}
	refresher := NewIncrementalRefresher(runner)
	ctx := context.Background()
	
	originalTrace := createRefreshTestTrace(20)
	updatedSnapshot := snapshot.FromMap(map[string]string{
		"key1": "new_value",
	})
	
	req := &RefreshRequest{
		OriginalTrace:      originalTrace,
		UpdatedSnapshot:    updatedSnapshot,
		Changes:            []StateChange{{ChangeType: "ledger_entry", Key: "key1", AffectedSteps: []int{5, 10}}},
		StartStep:          5,
		EndStep:            15,
		PreserveUnaffected: false,
	}
	
	result, err := refresher.Refresh(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	
	if !result.Success {
		t.Error("expected successful refresh")
	}
	if result.UpdatedTrace == nil {
		t.Fatal("expected non-nil updated trace")
	}
	if len(result.UpdatedTrace.States) != len(originalTrace.States) {
		t.Errorf("trace length changed: got %d, want %d", len(result.UpdatedTrace.States), len(originalTrace.States))
	}
	if result.Duration <= 0 {
		t.Error("expected positive duration")
	}
}

func TestRefresh_PreserveUnaffected(t *testing.T) {
	runner := &mockRunner{}
	refresher := NewIncrementalRefresher(runner)
	ctx := context.Background()
	
	originalTrace := createRefreshTestTrace(20)
	updatedSnapshot := snapshot.FromMap(map[string]string{
		"key1": "new_value",
	})
	
	// Only steps 10 and 15 are affected
	changes := []StateChange{
		{
			ChangeType:    "ledger_entry",
			Key:           "key1",
			AffectedSteps: []int{10, 15},
		},
	}
	
	req := &RefreshRequest{
		OriginalTrace:      originalTrace,
		UpdatedSnapshot:    updatedSnapshot,
		Changes:            changes,
		StartStep:          5,
		EndStep:            18,
		PreserveUnaffected: true,
	}
	
	result, err := refresher.Refresh(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	
	// Check that unaffected steps are preserved
	if len(result.PreservedSteps) == 0 {
		t.Error("expected some preserved steps")
	}
	
	// Steps 0-4 should be preserved (before range)
	if len(result.PreservedSteps) < 5 {
		t.Errorf("expected at least 5 preserved steps before range, got %d", len(result.PreservedSteps))
	}
}

func TestRefresh_MetadataPreserved(t *testing.T) {
	runner := &mockRunner{}
	refresher := NewIncrementalRefresher(runner)
	ctx := context.Background()
	
	originalTrace := createRefreshTestTrace(10)
	originalTrace.TransactionHash = "original_hash_123"
	originalTrace.Annotations = TraceAnnotations{
		SessionMetadata: map[string]string{"env": "test"},
	}
	
	updatedSnapshot := snapshot.FromMap(map[string]string{"k": "v"})
	
	req := &RefreshRequest{
		OriginalTrace:   originalTrace,
		UpdatedSnapshot: updatedSnapshot,
		Changes:         []StateChange{{AffectedSteps: []int{5}}},
		StartStep:       2,
		EndStep:         7,
	}
	
	result, err := refresher.Refresh(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	
	// Check metadata preservation
	if result.UpdatedTrace.TransactionHash != originalTrace.TransactionHash {
		t.Error("transaction hash not preserved")
	}
	if result.UpdatedTrace.Annotations.SessionMetadata["env"] != "test" {
		t.Error("annotations not preserved")
	}
	if result.UpdatedTrace.SnapshotInterval != originalTrace.SnapshotInterval {
		t.Error("snapshot interval not preserved")
	}
}

func TestQuickRefresh_NoChanges(t *testing.T) {
	runner := &mockRunner{}
	refresher := NewIncrementalRefresher(runner)
	detector := NewStateChangeDetector(snapshot.FromMap(map[string]string{"k": "v"}))
	refresher.SetDetector(detector)
	
	ctx := context.Background()
	trace := createRefreshTestTrace(10)
	
	// No changes
	changes := []StateChange{}
	
	result, err := refresher.QuickRefresh(ctx, trace, changes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	
	if !result.Success {
		t.Error("expected successful quick refresh")
	}
	if len(result.RefreshedSteps) != 0 {
		t.Errorf("expected 0 refreshed steps, got %d", len(result.RefreshedSteps))
	}
	if len(result.PreservedSteps) != len(trace.States) {
		t.Errorf("expected all %d steps preserved, got %d", len(trace.States), len(result.PreservedSteps))
	}
}

func TestQuickRefresh_WithChanges(t *testing.T) {
	runner := &mockRunner{}
	refresher := NewIncrementalRefresher(runner)
	detector := NewStateChangeDetector(snapshot.FromMap(map[string]string{"k": "v"}))
	refresher.SetDetector(detector)
	
	ctx := context.Background()
	trace := createRefreshTestTrace(20)
	
	changes := []StateChange{
		{
			ChangeType:    "ledger_entry",
			Key:           "key1",
			AffectedSteps: []int{5, 10},
		},
	}
	
	result, err := refresher.QuickRefresh(ctx, trace, changes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	
	if !result.Success {
		t.Error("expected successful quick refresh")
	}
	if len(result.RefreshedSteps) == 0 {
		t.Error("expected some refreshed steps")
	}
}

func TestIsStepAffected(t *testing.T) {
	runner := &mockRunner{}
	refresher := NewIncrementalRefresher(runner)
	
	changes := []StateChange{
		{AffectedSteps: []int{1, 3, 5}},
		{AffectedSteps: []int{7, 9}},
	}
	
	tests := []struct {
		step     int
		expected bool
	}{
		{step: 1, expected: true},
		{step: 3, expected: true},
		{step: 5, expected: true},
		{step: 7, expected: true},
		{step: 9, expected: true},
		{step: 0, expected: false},
		{step: 2, expected: false},
		{step: 4, expected: false},
		{step: 6, expected: false},
		{step: 10, expected: false},
	}
	
	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			result := refresher.isStepAffected(tt.step, changes)
			if result != tt.expected {
				t.Errorf("step %d: got %v, want %v", tt.step, result, tt.expected)
			}
		})
	}
}

func TestRebuildSnapshots(t *testing.T) {
	runner := &mockRunner{}
	refresher := NewIncrementalRefresher(runner)
	
	trace := createRefreshTestTrace(250) // Will create snapshots at 0, 100, 200
	
	refresher.rebuildSnapshots(trace)
	
	expectedSnapshots := 3 // At steps 0, 100, 200
	if len(trace.Snapshots) != expectedSnapshots {
		t.Errorf("expected %d snapshots, got %d", expectedSnapshots, len(trace.Snapshots))
	}
	
	// Verify snapshot steps
	expectedSteps := []int{0, 100, 200}
	for i, snap := range trace.Snapshots {
		if snap.Step != expectedSteps[i] {
			t.Errorf("snapshot %d: expected step %d, got %d", i, expectedSteps[i], snap.Step)
		}
	}
}

func TestRefresh_TimingMetrics(t *testing.T) {
	runner := &mockRunner{}
	refresher := NewIncrementalRefresher(runner)
	ctx := context.Background()
	
	trace := createRefreshTestTrace(100)
	snapshot := snapshot.FromMap(map[string]string{"k": "v"})
	
	req := &RefreshRequest{
		OriginalTrace:   trace,
		UpdatedSnapshot: snapshot,
		Changes:         []StateChange{{AffectedSteps: []int{50}}},
		StartStep:       40,
		EndStep:         60,
	}
	
	start := time.Now()
	result, err := refresher.Refresh(ctx, req)
	elapsed := time.Since(start)
	
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	
	if result.Duration <= 0 {
		t.Error("expected positive duration")
	}
	if result.Duration > elapsed {
		t.Error("reported duration should not exceed actual elapsed time")
	}
}
