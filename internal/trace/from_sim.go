// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"time"

	"github.com/dotandev/glassbox/internal/simulator"
)

// BuildExecutionTraceFromSimResponse constructs an ExecutionTrace from a
// SimulationResponse. Each DiagnosticEvent becomes an ExecutionState with its
// event type, contract ID, and topics preserved. The resulting trace can be
// exported via ExportExecutionTrace or used for gas profiling.
//
// If simResp is nil or has no diagnostic events, a minimal trace is returned
// with zero states (callers should check len(trace.States) before exporting).
//
// A StateChangeDetector may be supplied via detector to record step-level
// ledger key dependencies as the trace is built. Passing nil disables
// dependency recording without affecting any other behaviour.
func BuildExecutionTraceFromSimResponse(txHash string, simResp *simulator.SimulationResponse) *ExecutionTrace {
	return BuildExecutionTraceFromSimResponseWithDetector(txHash, simResp, nil)
}

// BuildExecutionTraceFromSimResponseWithDetector is the full form of
// BuildExecutionTraceFromSimResponse. It accepts an optional
// *StateChangeDetector; when non-nil, every ledger key present in a step's
// HostState is recorded as a dependency for that step so that incremental
// refresh can later compute the minimal re-simulation range.
func BuildExecutionTraceFromSimResponseWithDetector(txHash string, simResp *simulator.SimulationResponse, detector *StateChangeDetector) *ExecutionTrace {
	t := NewExecutionTrace(txHash, DefaultSnapshotInterval)
	t.StartTime = time.Now()

	if simResp == nil {
		t.EndTime = t.StartTime
		return t
	}

	// Attach observed budget data as a gas map on each state's HostState so
	// that profile.TraceToPprof / profile.GenerateHTML can read it.
	var budgetPerEvent map[string]interface{}
	if simResp.BudgetUsage != nil {
		budget := simResp.BudgetUsage
		n := len(simResp.DiagnosticEvents)
		if n == 0 {
			n = 1
		}
		budgetPerEvent = map[string]interface{}{
			// Spread total gas evenly across events so per-frame totals are
			// meaningful rather than all zero.
			"gas_used":         float64(budget.CPUInstructions) / float64(n),
			"memory_bytes":     float64(budget.MemoryBytes) / float64(n),
			"cpu_limit":        budget.CPULimit,
			"memory_limit":     budget.MemoryLimit,
			"operations_count": budget.OperationsCount,
		}
	}

	for _, ev := range simResp.DiagnosticEvents {
		state := ExecutionState{
			Operation: ev.EventType,
			EventType: ev.EventType,
			Timestamp: time.Now(),
		}
		if ev.ContractID != nil {
			state.ContractID = *ev.ContractID
		}
		if len(ev.Topics) > 0 {
			args := make([]interface{}, len(ev.Topics))
			for i, t := range ev.Topics {
				args[i] = t
			}
			state.Arguments = args
		}
		if ev.Data != "" {
			state.ReturnValue = ev.Data
		}
		if budgetPerEvent != nil {
			// Shallow copy so each state has its own map.
			hs := make(map[string]interface{}, len(budgetPerEvent))
			for k, v := range budgetPerEvent {
				hs[k] = v
			}
			state.HostState = hs
		}
		t.AddState(state)

		// Record ledger key dependencies for every key present in this
		// step's HostState. This populates the detector's dependency map so
		// that UpdateSnapshot can later identify which steps are affected by
		// each changed ledger entry and compute the minimal refresh range.
		if detector != nil {
			stepIdx := len(t.States) - 1
			for key := range state.HostState {
				detector.RecordStateDependency(stepIdx, key)
			}
			// Also capture a state fingerprint for drift detection.
			detector.RecordStateFingerprint(stepIdx, &state)
		}
	}

	t.EndTime = time.Now()
	// Attach raw diagnostic events so downstream consumers (e.g. SVG exporter)
	// can access the full event payload without rebuilding it.
	t.DiagnosticEvents = simResp.DiagnosticEvents

	return t
}
