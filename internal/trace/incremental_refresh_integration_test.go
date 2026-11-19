// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Integration tests for the incremental trace refresh pipeline.
//
// Coverage:
//   - dependency tracking: RecordStateDependency populates correct affected steps
//   - simple state change → refresh recomputes only the affected range
//   - cascading changes: a change to an early step triggers re-simulation
//     of all dependent downstream steps
//   - no-op refresh when snapshot is identical
//   - progress metrics: RefreshResult carries correct counts and duration
//   - BuildExecutionTraceFromSimResponseWithDetector wires dependencies
//   - ViewerRefreshHandler.InitializeWithSnapshot seeds fingerprints
package trace

import (
	"context"
	"testing"
	"time"

	"github.com/dotandev/glassbox/internal/simulator"
	"github.com/dotandev/glassbox/internal/snapshot"
)

// ── Dependency tracking ───────────────────────────────────────────────────────

func TestDependencyTracking_AffectedSteps_MatchRecordedDependencies(t *testing.T) {
	base := snapshot.FromMap(map[string]string{
		"balance_key": "1000",
		"supply_key":  "5000",
	})
	detector := NewStateChangeDetector(base)

	// Record which steps depend on which ledger keys.
	detector.RecordStateDependency(3, "balance_key")
	detector.RecordStateDependency(7, "balance_key")
	detector.RecordStateDependency(12, "supply_key")

	// Modify balance_key → steps 3 and 7 should be affected.
	updated := snapshot.FromMap(map[string]string{
		"balance_key": "2000", // changed
		"supply_key":  "5000",
	})
	changes, err := detector.UpdateSnapshot(updated)
	if err != nil {
		t.Fatalf("UpdateSnapshot: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}

	affected := GetAffectedSteps(changes)
	stepSet := make(map[int]bool, len(affected))
	for _, s := range affected {
		stepSet[s] = true
	}
	if !stepSet[3] {
		t.Error("step 3 should be affected by balance_key change")
	}
	if !stepSet[7] {
		t.Error("step 7 should be affected by balance_key change")
	}
	if stepSet[12] {
		t.Error("step 12 should NOT be affected — it depends on supply_key which did not change")
	}
}

func TestDependencyTracking_MultipleKeysChanged_UnionOfAffectedSteps(t *testing.T) {
	base := snapshot.FromMap(map[string]string{
		"key_a": "a1",
		"key_b": "b1",
	})
	detector := NewStateChangeDetector(base)

	detector.RecordStateDependency(2, "key_a")
	detector.RecordStateDependency(5, "key_b")
	detector.RecordStateDependency(8, "key_a")
	detector.RecordStateDependency(8, "key_b") // step 8 depends on both

	updated := snapshot.FromMap(map[string]string{
		"key_a": "a2",
		"key_b": "b2",
	})
	changes, err := detector.UpdateSnapshot(updated)
	if err != nil {
		t.Fatalf("UpdateSnapshot: %v", err)
	}

	affected := GetAffectedSteps(changes)
	stepSet := make(map[int]bool, len(affected))
	for _, s := range affected {
		stepSet[s] = true
	}
	for _, want := range []int{2, 5, 8} {
		if !stepSet[want] {
			t.Errorf("step %d should be in the affected set", want)
		}
	}
}

// ── Simple refresh ────────────────────────────────────────────────────────────

func TestIncrementalRefresh_SimpleChange_OnlyAffectedRangeResimulated(t *testing.T) {
	runner := &mockRunner{}
	refresher := NewIncrementalRefresher(runner)

	tr := createRefreshTestTrace(30)
	base := snapshot.FromMap(map[string]string{"ledger_key": "old"})
	detector := NewStateChangeDetector(base)
	refresher.SetDetector(detector)

	// Record that steps 10 and 15 depend on ledger_key.
	detector.RecordStateDependency(10, "ledger_key")
	detector.RecordStateDependency(15, "ledger_key")

	updated := snapshot.FromMap(map[string]string{"ledger_key": "new"})
	changes, err := detector.UpdateSnapshot(updated)
	if err != nil {
		t.Fatalf("UpdateSnapshot: %v", err)
	}
	if len(changes) == 0 {
		t.Fatal("expected at least one change")
	}

	ctx := context.Background()
	result, err := refresher.QuickRefresh(ctx, tr, changes)
	if err != nil {
		t.Fatalf("QuickRefresh: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected Success=true, got Error=%v", result.Error)
	}

	// Steps 0–9 (before first affected) must be in PreservedSteps.
	preserved := make(map[int]bool, len(result.PreservedSteps))
	for _, s := range result.PreservedSteps {
		preserved[s] = true
	}
	for i := 0; i < 10; i++ {
		if !preserved[i] {
			t.Errorf("step %d (before affected range) should be preserved", i)
		}
	}

	// At least steps 10 and 15 must be in RefreshedSteps.
	refreshed := make(map[int]bool, len(result.RefreshedSteps))
	for _, s := range result.RefreshedSteps {
		refreshed[s] = true
	}
	if len(result.RefreshedSteps) == 0 {
		t.Error("expected at least one refreshed step")
	}
}

// ── Cascading changes ─────────────────────────────────────────────────────────

func TestIncrementalRefresh_CascadingChange_RefreshesFromFirstAffected(t *testing.T) {
	// When step 5 is affected, all steps from 5 to the end must be refreshed
	// because downstream steps could depend on step 5's output state.
	runner := &mockRunner{}
	refresher := NewIncrementalRefresher(runner)

	const totalSteps = 20
	tr := createRefreshTestTrace(totalSteps)
	base := snapshot.FromMap(map[string]string{"root_key": "v1"})
	detector := NewStateChangeDetector(base)
	refresher.SetDetector(detector)

	// Only step 5 is explicitly recorded, but the conservative policy
	// cascades the refresh to totalSteps-1.
	detector.RecordStateDependency(5, "root_key")

	updated := snapshot.FromMap(map[string]string{"root_key": "v2"})
	changes, err := detector.UpdateSnapshot(updated)
	if err != nil {
		t.Fatalf("UpdateSnapshot: %v", err)
	}

	startStep, endStep := ComputeRefreshRange(GetAffectedSteps(changes), totalSteps)
	if startStep != 5 {
		t.Errorf("startStep = %d, want 5", startStep)
	}
	if endStep != totalSteps-1 {
		t.Errorf("endStep = %d, want %d", endStep, totalSteps-1)
	}

	// Verify the full refresh covers the expected range.
	ctx := context.Background()
	req := &RefreshRequest{
		OriginalTrace:      tr,
		UpdatedSnapshot:    updated,
		Changes:            changes,
		StartStep:          startStep,
		EndStep:            endStep,
		PreserveUnaffected: false,
	}
	result, err := refresher.Refresh(ctx, req)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected Success=true")
	}
	// Steps 0–4 preserved, 5–19 refreshed.
	if len(result.PreservedSteps) != 5 {
		t.Errorf("expected 5 preserved steps (0–4), got %d", len(result.PreservedSteps))
	}
	if len(result.RefreshedSteps) != totalSteps-5 {
		t.Errorf("expected %d refreshed steps (5–%d), got %d",
			totalSteps-5, totalSteps-1, len(result.RefreshedSteps))
	}
}

// ── Noop refresh when snapshot unchanged ─────────────────────────────────────

func TestIncrementalRefresh_IdenticalSnapshot_NoStepsRefreshed(t *testing.T) {
	runner := &mockRunner{}
	refresher := NewIncrementalRefresher(runner)

	tr := createRefreshTestTrace(15)
	base := snapshot.FromMap(map[string]string{"k": "v"})
	detector := NewStateChangeDetector(base)
	refresher.SetDetector(detector)
	detector.RecordStateDependency(5, "k")

	// Same snapshot — zero changes.
	same := snapshot.FromMap(map[string]string{"k": "v"})
	changes, err := detector.UpdateSnapshot(same)
	if err != nil {
		t.Fatalf("UpdateSnapshot: %v", err)
	}
	if len(changes) != 0 {
		t.Fatalf("expected 0 changes for identical snapshot, got %d", len(changes))
	}

	ctx := context.Background()
	result, err := refresher.QuickRefresh(ctx, tr, changes)
	if err != nil {
		t.Fatalf("QuickRefresh: %v", err)
	}
	if !result.Success {
		t.Errorf("expected Success=true")
	}
	if len(result.RefreshedSteps) != 0 {
		t.Errorf("expected 0 refreshed steps for identical snapshot, got %d", len(result.RefreshedSteps))
	}
	if len(result.PreservedSteps) != len(tr.States) {
		t.Errorf("expected all %d steps preserved, got %d", len(tr.States), len(result.PreservedSteps))
	}
}

// ── Progress metrics ──────────────────────────────────────────────────────────

func TestRefreshResult_ProgressMetrics_AreSane(t *testing.T) {
	runner := &mockRunner{}
	refresher := NewIncrementalRefresher(runner)

	const total = 50
	tr := createRefreshTestTrace(total)
	updated := snapshot.FromMap(map[string]string{"k": "new"})

	req := &RefreshRequest{
		OriginalTrace:      tr,
		UpdatedSnapshot:    updated,
		Changes:            []StateChange{{ChangeType: "ledger_entry", Key: "k", AffectedSteps: []int{20}}},
		StartStep:          20,
		EndStep:            total - 1,
		PreserveUnaffected: false,
	}

	before := time.Now()
	ctx := context.Background()
	result, err := refresher.Refresh(ctx, req)
	after := time.Now()

	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected Success=true")
	}

	// Duration must be positive and not exceed the wall time.
	if result.Duration <= 0 {
		t.Error("Duration should be positive")
	}
	if result.Duration > after.Sub(before)+time.Millisecond {
		t.Errorf("reported Duration %v exceeds actual elapsed %v", result.Duration, after.Sub(before))
	}

	// Step counts must add up to total.
	totalCounted := len(result.RefreshedSteps) + len(result.PreservedSteps)
	if totalCounted != total {
		t.Errorf("RefreshedSteps(%d) + PreservedSteps(%d) = %d, want %d",
			len(result.RefreshedSteps), len(result.PreservedSteps), totalCounted, total)
	}

	// No duplicates between refreshed and preserved.
	refreshed := make(map[int]bool, len(result.RefreshedSteps))
	for _, s := range result.RefreshedSteps {
		if refreshed[s] {
			t.Errorf("step %d appears twice in RefreshedSteps", s)
		}
		refreshed[s] = true
	}
	for _, s := range result.PreservedSteps {
		if refreshed[s] {
			t.Errorf("step %d appears in both RefreshedSteps and PreservedSteps", s)
		}
	}
}

// ── Trace metadata is always preserved ───────────────────────────────────────

func TestRefresh_TraceMetadata_AlwaysPreserved(t *testing.T) {
	runner := &mockRunner{}
	refresher := NewIncrementalRefresher(runner)

	tr := createRefreshTestTrace(10)
	tr.TransactionHash = "preserve-this-hash"
	tr.Annotations = TraceAnnotations{
		SessionMetadata: map[string]string{"owner": "test-suite"},
	}

	updated := snapshot.FromMap(map[string]string{"k": "new"})
	req := &RefreshRequest{
		OriginalTrace:   tr,
		UpdatedSnapshot: updated,
		Changes:         []StateChange{{AffectedSteps: []int{5}}},
		StartStep:       5,
		EndStep:         9,
	}

	ctx := context.Background()
	result, err := refresher.Refresh(ctx, req)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	if result.UpdatedTrace.TransactionHash != "preserve-this-hash" {
		t.Error("TransactionHash must be preserved after refresh")
	}
	if result.UpdatedTrace.Annotations.SessionMetadata["owner"] != "test-suite" {
		t.Error("Annotations must be preserved after refresh")
	}
	if result.UpdatedTrace.SnapshotInterval != tr.SnapshotInterval {
		t.Error("SnapshotInterval must be preserved after refresh")
	}
}

// ── BuildExecutionTraceFromSimResponseWithDetector wires dependencies ─────────

func TestBuildTraceWithDetector_PopulatesDependencies(t *testing.T) {
	detector := NewStateChangeDetector(snapshot.FromMap(map[string]string{}))

	simResp := &simulator.SimulationResponse{
		Status: "success",
		DiagnosticEvents: []simulator.DiagnosticEvent{
			{
				EventType:  "contract_call",
				ContractID: strPtr("CAAA"),
				Data:       "ok",
			},
			{
				EventType:  "host_function",
				ContractID: strPtr("CBBB"),
				Data:       "ok",
			},
		},
	}

	tr := BuildExecutionTraceFromSimResponseWithDetector("txhash", simResp, detector)
	if len(tr.States) != 2 {
		t.Fatalf("expected 2 states, got %d", len(tr.States))
	}

	// Detector should have fingerprints for both steps.
	for i := 0; i < 2; i++ {
		if _, ok := detector.stateFingerprints[i]; !ok {
			t.Errorf("expected fingerprint for step %d", i)
		}
	}
}

func TestBuildTraceWithDetector_NilDetector_DoesNotPanic(t *testing.T) {
	simResp := &simulator.SimulationResponse{
		Status: "success",
		DiagnosticEvents: []simulator.DiagnosticEvent{
			{EventType: "contract_call"},
		},
	}
	// Must not panic with nil detector.
	tr := BuildExecutionTraceFromSimResponseWithDetector("txhash", simResp, nil)
	if len(tr.States) != 1 {
		t.Errorf("expected 1 state, got %d", len(tr.States))
	}
}

// ── ViewerRefreshHandler initialisation ──────────────────────────────────────

func TestViewerRefreshHandler_InitializeWithSnapshot_SeedsFingerprints(t *testing.T) {
	tr := createRefreshTestTrace(5)
	viewer := NewInteractiveViewer(tr)

	runner := &mockRunner{}
	refresher := NewIncrementalRefresher(runner)
	handler := NewViewerRefreshHandler(viewer, refresher)

	base := snapshot.FromMap(map[string]string{"k": "v"})
	handler.InitializeWithSnapshot(base)

	// After initialization, the detector must have fingerprints for all steps.
	for i := range tr.States {
		if _, ok := handler.detector.stateFingerprints[i]; !ok {
			t.Errorf("expected fingerprint for step %d after InitializeWithSnapshot", i)
		}
	}
}

func TestViewerRefreshHandler_GetRefreshStatus_ReturnsZeroWhenNeverRefreshed(t *testing.T) {
	tr := createRefreshTestTrace(3)
	viewer := NewInteractiveViewer(tr)
	runner := &mockRunner{}
	refresher := NewIncrementalRefresher(runner)
	handler := NewViewerRefreshHandler(viewer, refresher)

	status := handler.GetRefreshStatus()
	if status["auto_refresh_enabled"] != false {
		t.Error("auto_refresh_enabled should default to false")
	}
	lastRefresh, ok := status["last_refresh_time"].(time.Time)
	if ok && !lastRefresh.IsZero() {
		t.Error("last_refresh_time should be zero when no refresh has run")
	}
}

// strPtr is defined in from_sim_test.go (same package); no redefinition needed here.
