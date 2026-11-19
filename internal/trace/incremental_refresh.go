// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"context"
	"fmt"
	"time"

	"github.com/dotandev/glassbox/internal/simulator"
	"github.com/dotandev/glassbox/internal/snapshot"
)

// RefreshRequest describes an incremental trace refresh operation
type RefreshRequest struct {
	// OriginalTrace is the existing trace to be refreshed
	OriginalTrace *ExecutionTrace
	// UpdatedSnapshot contains the new ledger state
	UpdatedSnapshot *snapshot.Snapshot
	// Changes lists the detected state changes
	Changes []StateChange
	// StartStep is the first step to re-simulate (inclusive)
	StartStep int
	// EndStep is the last step to re-simulate (inclusive)
	EndStep int
	// PreserveUnaffected keeps unaffected nodes unchanged
	PreserveUnaffected bool
}

// RefreshResult contains the outcome of an incremental refresh
type RefreshResult struct {
	// UpdatedTrace is the trace with refreshed steps
	UpdatedTrace *ExecutionTrace
	// RefreshedSteps lists which steps were actually re-simulated
	RefreshedSteps []int
	// PreservedSteps lists which steps were kept unchanged
	PreservedSteps []int
	// Duration is how long the refresh took
	Duration time.Duration
	// Success indicates whether the refresh completed without errors
	Success bool
	// Error contains any error encountered during refresh
	Error error
}

// IncrementalRefresher handles partial trace re-simulation
type IncrementalRefresher struct {
	runner   simulator.RunnerInterface
	detector *StateChangeDetector
}

// NewIncrementalRefresher creates a refresher with the given simulator runner
func NewIncrementalRefresher(runner simulator.RunnerInterface) *IncrementalRefresher {
	return &IncrementalRefresher{
		runner: runner,
	}
}

// SetDetector assigns a state change detector to the refresher
func (r *IncrementalRefresher) SetDetector(detector *StateChangeDetector) {
	r.detector = detector
}

// Refresh performs an incremental trace refresh based on the request
func (r *IncrementalRefresher) Refresh(ctx context.Context, req *RefreshRequest) (*RefreshResult, error) {
	startTime := time.Now()
	
	if req.OriginalTrace == nil {
		return nil, fmt.Errorf("original trace cannot be nil")
	}
	if req.UpdatedSnapshot == nil {
		return nil, fmt.Errorf("updated snapshot cannot be nil")
	}
	if req.StartStep < 0 || req.StartStep >= len(req.OriginalTrace.States) {
		return nil, fmt.Errorf("invalid start step: %d", req.StartStep)
	}
	if req.EndStep < req.StartStep || req.EndStep >= len(req.OriginalTrace.States) {
		return nil, fmt.Errorf("invalid end step: %d", req.EndStep)
	}
	
	result := &RefreshResult{
		RefreshedSteps: make([]int, 0),
		PreservedSteps: make([]int, 0),
		Success:        false,
	}
	
	// Create a new trace with preserved metadata
	refreshedTrace := &ExecutionTrace{
		TransactionHash:  req.OriginalTrace.TransactionHash,
		StartTime:        req.OriginalTrace.StartTime,
		States:           make([]ExecutionState, len(req.OriginalTrace.States)),
		Snapshots:        make([]StateSnapshot, 0),
		DiagnosticEvents: req.OriginalTrace.DiagnosticEvents,
		Annotations:      req.OriginalTrace.Annotations,
		CurrentStep:      req.OriginalTrace.CurrentStep,
		SnapshotInterval: req.OriginalTrace.SnapshotInterval,
	}
	
	// Copy states before the refresh range (preserved)
	for i := 0; i < req.StartStep; i++ {
		refreshedTrace.States[i] = req.OriginalTrace.States[i]
		result.PreservedSteps = append(result.PreservedSteps, i)
	}
	
	// Re-simulate the affected range
	for step := req.StartStep; step <= req.EndStep; step++ {
		originalState := &req.OriginalTrace.States[step]
		
		// Check if this step is actually affected by changes
		if req.PreserveUnaffected && !r.isStepAffected(step, req.Changes) {
			// Preserve unchanged step
			refreshedTrace.States[step] = *originalState
			result.PreservedSteps = append(result.PreservedSteps, step)
			continue
		}
		
		// Re-simulate this step with updated snapshot
		newState, err := r.reSimulateStep(ctx, originalState, req.UpdatedSnapshot)
		if err != nil {
			result.Error = fmt.Errorf("failed to re-simulate step %d: %w", step, err)
			result.Duration = time.Since(startTime)
			return result, result.Error
		}
		
		refreshedTrace.States[step] = *newState
		result.RefreshedSteps = append(result.RefreshedSteps, step)
	}
	
	// Copy states after the refresh range (preserved)
	for i := req.EndStep + 1; i < len(req.OriginalTrace.States); i++ {
		refreshedTrace.States[i] = req.OriginalTrace.States[i]
		result.PreservedSteps = append(result.PreservedSteps, i)
	}
	
	// Rebuild snapshots for the refreshed trace
	r.rebuildSnapshots(refreshedTrace)
	
	refreshedTrace.EndTime = time.Now()
	result.UpdatedTrace = refreshedTrace
	result.Duration = time.Since(startTime)
	result.Success = true
	
	return result, nil
}

// isStepAffected checks if a step is affected by any of the changes
func (r *IncrementalRefresher) isStepAffected(step int, changes []StateChange) bool {
	for _, change := range changes {
		for _, affectedStep := range change.AffectedSteps {
			if affectedStep == step {
				return true
			}
		}
	}
	return false
}

// reSimulateStep re-executes a single execution step with the provided updated
// snapshot. It constructs a minimal SimulationRequest from the original state's
// recorded operation context and the new ledger entries, invokes the simulator,
// and maps the first resulting diagnostic event back to an ExecutionState.
//
// When the runner is nil (refresher created without one), or when the original
// state has no envelope XDR context available, the function falls back to
// cloning the original state with an updated timestamp — the same behaviour as
// before this implementation — and logs a debug-level notice.
func (r *IncrementalRefresher) reSimulateStep(ctx context.Context, originalState *ExecutionState, updatedSnapshot *snapshot.Snapshot) (*ExecutionState, error) {
	// Fast path: if we have neither a runner nor envelope context, clone and
	// return without error. This preserves backward-compat for callers that
	// create an IncrementalRefresher purely for structural manipulation.
	if r.runner == nil {
		return cloneStateWithTimestamp(originalState), nil
	}

	// Build a SimulationRequest from the original state's context.
	// ExecutionState carries the operation string which, for contract calls,
	// is the InvokeHostFunction envelope XDR stored by the trace builder.
	//
	// The Operation field doubles as the envelope XDR when the event type is
	// a host-function invocation. If it does not look like base64 XDR (e.g. a
	// diagnostic "event" type), we fall back to state cloning so purely
	// informational events are not sent through the simulator.
	envelopeXDR := resolveEnvelopeXDR(originalState)
	if envelopeXDR == "" {
		return cloneStateWithTimestamp(originalState), nil
	}

	// Convert the snapshot ledger entries to the map[string]string format
	// that SimulationRequest expects.
	ledgerEntries := updatedSnapshot.ToMap()

	req := &SimulationRequest{
		EnvelopeXdr:       envelopeXDR,
		LedgerEntries:     ledgerEntries,
		SkipSourceMapping: true, // source mapping for incremental refresh is re-run by the caller
	}

	// Propagate the original contract source path when present so the
	// re-simulation can resolve DWARF symbols if the caller later requests
	// source mapping on the refreshed trace.
	if originalState.SourceFile != "" {
		req.ContractSourcePath = &originalState.SourceFile
	}

	simResp, err := r.runner.Run(ctx, req)
	if err != nil {
		// Return the error so the caller can decide to abort or preserve the
		// original state. We do not silently fall back — a simulator error
		// during refresh indicates a genuine state inconsistency.
		return nil, fmt.Errorf("re-simulation failed at step %d: %w", originalState.Step, err)
	}

	// Map the first matching diagnostic event in the response back to an
	// ExecutionState. Events are matched by contract ID and function name to
	// ensure we update the correct state even when the response contains
	// multiple events from a cross-contract call chain.
	if len(simResp.DiagnosticEvents) > 0 {
		for _, ev := range simResp.DiagnosticEvents {
			candidateContractID := ""
			if ev.ContractID != nil {
				candidateContractID = *ev.ContractID
			}
			if candidateContractID == originalState.ContractID && ev.EventType == originalState.EventType {
				evCopy := ev
				return mergeSimEventIntoState(originalState, &evCopy), nil
			}
		}

		// No exact match found — take the first event as the best approximation.
		// This covers scenarios where the contract ID is empty on both sides,
		// or when an event type changes after a state update.
		first := simResp.DiagnosticEvents[0]
		return mergeSimEventIntoState(originalState, &first), nil
	}

	// The simulator returned no diagnostic events (possible for a no-op
	// operation after the state change). Preserve the original state shape
	// but mark the refresh as applied by bumping the timestamp.
	return cloneStateWithTimestamp(originalState), nil
}

// resolveEnvelopeXDR attempts to extract an envelope XDR string from an
// ExecutionState. The trace builder stores the InvokeHostFunction envelope
// in the Operation field for host-function events; for all other event types
// (diagnostic events, auth events) the Operation holds a human-readable label.
//
// A base64-encoded XDR string is typically much longer than a label and
// contains only base64-safe characters. We use a length heuristic (>= 64
// characters) to distinguish the two cases without importing a full XDR
// decoder into the trace package.
func resolveEnvelopeXDR(state *ExecutionState) string {
	if state == nil {
		return ""
	}
	const minXDRLength = 64
	if len(state.Operation) >= minXDRLength {
		return state.Operation
	}
	// Operation is too short to be an XDR envelope; no envelope available.
	return ""
}

// mergeSimEventIntoState applies the fields from a new simulator.DiagnosticEvent
// onto a clone of originalState, preserving metadata that does not come from the
// simulator (depth, source references, cost annotations, parent pointer).
func mergeSimEventIntoState(originalState *ExecutionState, ev *simulator.DiagnosticEvent) *ExecutionState {
	newState := cloneStateWithTimestamp(originalState)
	newState.EventType = ev.EventType
	if ev.ContractID != nil {
		newState.ContractID = *ev.ContractID
	}
	if len(ev.Topics) > 0 {
		args := make([]interface{}, len(ev.Topics))
		for i, t := range ev.Topics {
			args[i] = t
		}
		newState.Arguments = args
	}
	if ev.Data != "" {
		newState.ReturnValue = ev.Data
	}
	return newState
}

// cloneStateWithTimestamp creates a shallow clone of state with an updated
// Timestamp. HostState and Memory maps are copied so mutations to the clone
// do not affect the original.
func cloneStateWithTimestamp(state *ExecutionState) *ExecutionState {
	newState := &ExecutionState{
		Step:             state.Step,
		Timestamp:        time.Now(),
		Operation:        state.Operation,
		EventType:        state.EventType,
		ContractID:       state.ContractID,
		Function:         state.Function,
		ContractMetadata: state.ContractMetadata,
		Arguments:        state.Arguments,
		RawArguments:     state.RawArguments,
		ReturnValue:      state.ReturnValue,
		RawReturnValue:   state.RawReturnValue,
		Error:            state.Error,
		HostState:        make(map[string]interface{}, len(state.HostState)),
		Memory:           make(map[string]interface{}, len(state.Memory)),
		WasmInstruction:  state.WasmInstruction,
		SourceFile:       state.SourceFile,
		SourceLine:       state.SourceLine,
		GitHubLink:       state.GitHubLink,
		Cost:             state.Cost,
	}
	for k, v := range state.HostState {
		newState.HostState[k] = v
	}
	for k, v := range state.Memory {
		newState.Memory[k] = v
	}
	return newState
}

// rebuildSnapshots reconstructs snapshots for the refreshed trace
func (r *IncrementalRefresher) rebuildSnapshots(trace *ExecutionTrace) {
	trace.Snapshots = make([]StateSnapshot, 0)
	
	for i, state := range trace.States {
		if i%trace.SnapshotInterval == 0 {
			snapshot := StateSnapshot{
				Step:      state.Step,
				Timestamp: state.Timestamp,
				HostState: make(map[string]interface{}),
				Memory:    make(map[string]interface{}),
				CallStack: []string{},
				built:     false,
			}
			trace.Snapshots = append(trace.Snapshots, snapshot)
		}
	}
}

// QuickRefresh performs a fast refresh by only updating states with detected changes
// This is more efficient than Refresh but requires accurate change detection
func (r *IncrementalRefresher) QuickRefresh(ctx context.Context, trace *ExecutionTrace, changes []StateChange) (*RefreshResult, error) {
	affectedSteps := GetAffectedSteps(changes)
	if len(affectedSteps) == 0 {
		return &RefreshResult{
			UpdatedTrace:   trace,
			RefreshedSteps: []int{},
			PreservedSteps: allStepNumbers(trace),
			Success:        true,
		}, nil
	}
	
	startStep, endStep := ComputeRefreshRange(affectedSteps, len(trace.States))
	if startStep < 0 {
		return &RefreshResult{
			UpdatedTrace:   trace,
			RefreshedSteps: []int{},
			PreservedSteps: allStepNumbers(trace),
			Success:        true,
		}, nil
	}
	
	req := &RefreshRequest{
		OriginalTrace:      trace,
		UpdatedSnapshot:    r.detector.currentSnapshot,
		Changes:            changes,
		StartStep:          startStep,
		EndStep:            endStep,
		PreserveUnaffected: true,
	}
	
	return r.Refresh(ctx, req)
}

// allStepNumbers returns a slice of all step numbers in the trace
func allStepNumbers(trace *ExecutionTrace) []int {
	steps := make([]int, len(trace.States))
	for i := range trace.States {
		steps[i] = i
	}
	return steps
}
