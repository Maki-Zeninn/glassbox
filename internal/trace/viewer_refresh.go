// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"context"
	"fmt"
	"time"

	"github.com/dotandev/glassbox/internal/snapshot"
	"github.com/dotandev/glassbox/internal/visualizer"
)

// ViewerRefreshHandler manages incremental trace refresh in the interactive viewer
type ViewerRefreshHandler struct {
	viewer   *InteractiveViewer
	refresher *IncrementalRefresher
	detector  *StateChangeDetector
	// autoRefresh enables automatic refresh when state changes are detected
	autoRefresh bool
	// lastRefreshTime tracks when the last refresh occurred
	lastRefreshTime time.Time
}

// NewViewerRefreshHandler creates a refresh handler for the given viewer
func NewViewerRefreshHandler(viewer *InteractiveViewer, refresher *IncrementalRefresher) *ViewerRefreshHandler {
	return &ViewerRefreshHandler{
		viewer:    viewer,
		refresher: refresher,
		detector:  NewStateChangeDetector(nil), // Will be initialized with base snapshot
		autoRefresh: false,
	}
}

// InitializeWithSnapshot sets the base snapshot for change detection
func (h *ViewerRefreshHandler) InitializeWithSnapshot(baseSnapshot *snapshot.Snapshot) {
	h.detector = NewStateChangeDetector(baseSnapshot)
	h.refresher.SetDetector(h.detector)
	
	// Record initial state dependencies from the trace
	for i, state := range h.viewer.trace.States {
		h.detector.RecordStateFingerprint(i, &state)
	}
}

// EnableAutoRefresh turns on automatic refresh when state changes
func (h *ViewerRefreshHandler) EnableAutoRefresh() {
	h.autoRefresh = true
}

// DisableAutoRefresh turns off automatic refresh
func (h *ViewerRefreshHandler) DisableAutoRefresh() {
	h.autoRefresh = false
}

// RefreshTrace performs an incremental refresh with the given snapshot
func (h *ViewerRefreshHandler) RefreshTrace(ctx context.Context, updatedSnapshot *snapshot.Snapshot) error {
	// Detect changes
	changes, err := h.detector.UpdateSnapshot(updatedSnapshot)
	if err != nil {
		return fmt.Errorf("failed to detect state changes: %w", err)
	}
	
	if len(changes) == 0 {
		fmt.Printf("%s No state changes detected - trace is up to date\n", visualizer.Symbol("check"))
		return nil
	}
	
	// Display detected changes
	fmt.Printf("%s Detected %d state change(s):\n", visualizer.Symbol("info"), len(changes))
	for i, change := range changes {
		fmt.Printf("  %d. Type: %s, Key: %s (affects %d step(s))\n",
			i+1, change.ChangeType, truncateString(change.Key, 40), len(change.AffectedSteps))
	}
	
	// Compute refresh range
	affectedSteps := GetAffectedSteps(changes)
	startStep, endStep := ComputeRefreshRange(affectedSteps, len(h.viewer.trace.States))
	
	fmt.Printf("%s Refreshing steps %d to %d (%d total)...\n",
		visualizer.Symbol("refresh"), startStep, endStep, endStep-startStep+1)
	
	// Perform incremental refresh
	req := &RefreshRequest{
		OriginalTrace:      h.viewer.trace,
		UpdatedSnapshot:    updatedSnapshot,
		Changes:            changes,
		StartStep:          startStep,
		EndStep:            endStep,
		PreserveUnaffected: true,
	}
	
	result, err := h.refresher.Refresh(ctx, req)
	if err != nil {
		return fmt.Errorf("refresh failed: %w", err)
	}
	
	if !result.Success {
		return fmt.Errorf("refresh unsuccessful: %v", result.Error)
	}
	
	// Update viewer's trace with refreshed version
	h.viewer.trace = result.UpdatedTrace
	h.lastRefreshTime = time.Now()
	
	// Re-index search nodes if search is active
	if h.viewer.search.mode == SearchModeActive {
		h.viewer.search.IndexNodes(h.viewer.flatTraceNodes())
		h.viewer.search.engine.Search(h.viewer.search.flatNodes)
	}
	
	// Display refresh summary
	fmt.Printf("%s Refresh completed in %v\n", visualizer.Symbol("success"), result.Duration)
	fmt.Printf("  - Refreshed: %d steps\n", len(result.RefreshedSteps))
	fmt.Printf("  - Preserved: %d steps\n", len(result.PreservedSteps))
	
	return nil
}

// RefreshCurrentView refreshes only the visible portion of the trace
func (h *ViewerRefreshHandler) RefreshCurrentView(ctx context.Context, updatedSnapshot *snapshot.Snapshot) error {
	// Get the current viewport range (steps visible on screen)
	currentStep := h.viewer.trace.CurrentStep
	viewportSize := 20 // Approximate number of visible steps
	
	startStep := max(0, currentStep-viewportSize/2)
	endStep := min(len(h.viewer.trace.States)-1, currentStep+viewportSize/2)
	
	// Detect changes
	changes, err := h.detector.UpdateSnapshot(updatedSnapshot)
	if err != nil {
		return fmt.Errorf("failed to detect state changes: %w", err)
	}
	
	// Filter changes to only those affecting the current view
	viewChanges := make([]StateChange, 0)
	for _, change := range changes {
		for _, step := range change.AffectedSteps {
			if step >= startStep && step <= endStep {
				viewChanges = append(viewChanges, change)
				break
			}
		}
	}
	
	if len(viewChanges) == 0 {
		return nil // Nothing to refresh in current view
	}
	
	// Perform targeted refresh
	req := &RefreshRequest{
		OriginalTrace:      h.viewer.trace,
		UpdatedSnapshot:    updatedSnapshot,
		Changes:            viewChanges,
		StartStep:          startStep,
		EndStep:            endStep,
		PreserveUnaffected: true,
	}
	
	result, err := h.refresher.Refresh(ctx, req)
	if err != nil {
		return err
	}
	
	if result.Success {
		h.viewer.trace = result.UpdatedTrace
		h.lastRefreshTime = time.Now()
	}
	
	return nil
}

// GetRefreshStatus returns information about the last refresh
func (h *ViewerRefreshHandler) GetRefreshStatus() map[string]interface{} {
	return map[string]interface{}{
		"auto_refresh_enabled": h.autoRefresh,
		"last_refresh_time":    h.lastRefreshTime,
		"time_since_refresh":   time.Since(h.lastRefreshTime),
	}
}

// HandleRefreshCommand processes the 'refresh' command in the interactive viewer
func (h *ViewerRefreshHandler) HandleRefreshCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("refresh command requires a snapshot path argument")
	}
	
	snapshotPath := args[0]
	
	// Load updated snapshot
	updatedSnapshot, err := snapshot.Load(snapshotPath)
	if err != nil {
		return fmt.Errorf("failed to load snapshot from %s: %w", snapshotPath, err)
	}
	
	// Perform refresh
	return h.RefreshTrace(ctx, updatedSnapshot)
}

// truncateString truncates a string to the specified length
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
