// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// ComparisonConfig defines thresholds and suppression rules for trace comparison
type ComparisonConfig struct {
	// Thresholds for resource regressions (percentage change)
	CPUThresholdPct       float64 `json:"cpu_threshold_pct"`
	MemoryThresholdPct    float64 `json:"memory_threshold_pct"`
	HostCallThresholdPct  float64 `json:"host_call_threshold_pct"`
	EventCountThresholdPct float64 `json:"event_count_threshold_pct"`

	// Absolute thresholds (minimum delta to report)
	CPUAbsoluteThreshold    uint64  `json:"cpu_absolute_threshold"`
	MemoryAbsoluteThreshold uint64  `json:"memory_absolute_threshold"`

	// Fields to suppress from comparison (nondeterministic fields)
	SuppressedFields []string `json:"suppressed_fields"`

	// Whether to fail on threshold violations
	FailOnThresholdViolation bool `json:"fail_on_threshold_violation"`
}

// DefaultComparisonConfig returns a sensible default configuration
func DefaultComparisonConfig() *ComparisonConfig {
	return &ComparisonConfig{
		CPUThresholdPct:        10.0,  // 10% CPU increase is a regression
		MemoryThresholdPct:     10.0,  // 10% memory increase is a regression
		HostCallThresholdPct:  5.0,   // 5% host call increase is a regression
		EventCountThresholdPct: 0.0,   // Any event count change is reported
		CPUAbsoluteThreshold:   1000,  // Ignore CPU deltas < 1000 instructions
		MemoryAbsoluteThreshold: 1024, // Ignore memory deltas < 1KB
		SuppressedFields: []string{
			"timestamp",
			"sequence_id",
			"parent_sequence_id",
			"wasm_instruction",
		},
		FailOnThresholdViolation: false,
	}
}

// NormalizedDiff represents a normalized difference between traces
type NormalizedDiff struct {
	Category    string  `json:"category"`    // "cpu", "memory", "host_calls", "events", "call_path"
	Path        string  `json:"path"`        // Stable path identifier (e.g., contract::function)
	Baseline    float64 `json:"baseline"`    // Baseline value
	Current     float64 `json:"current"`     // Current value
	AbsoluteDelta float64 `json:"absolute_delta"` // Current - Baseline
	PercentDelta float64 `json:"percent_delta"`  // (Current - Baseline) / Baseline * 100
	Severity    string  `json:"severity"`    // "info", "warning", "critical"
	Reason      string  `json:"reason,omitempty"` // Human-readable explanation
}

// NormalizedComparisonResult contains the complete normalized comparison
type NormalizedComparisonResult struct {
	SchemaVersion string           `json:"schema_version"`
	BaselineName  string           `json:"baseline_name"`
	CurrentName   string           `json:"current_name"`
	Config        ComparisonConfig `json:"config"`
	Diffs         []NormalizedDiff `json:"diffs"`
	Summary       ComparisonSummary `json:"summary"`
	HasRegression bool             `json:"has_regression"`
}

// ComparisonSummary provides aggregate statistics
type ComparisonSummary struct {
	TotalDiffs      int `json:"total_diffs"`
	CriticalDiffs   int `json:"critical_diffs"`
	WarningDiffs    int `json:"warning_diffs"`
	InfoDiffs       int `json:"info_diffs"`
	ThresholdViolations int `json:"threshold_violations"`
}

// CompareTracesNormalized performs a normalized comparison with thresholds and suppression
func CompareTracesNormalized(baseline, current *ExecutionTrace, baselineName, currentName string, config *ComparisonConfig) *NormalizedComparisonResult {
	if config == nil {
		config = DefaultComparisonConfig()
	}

	if baselineName == "" {
		baselineName = "Baseline"
	}
	if currentName == "" {
		currentName = "Current"
	}

	result := &NormalizedComparisonResult{
		SchemaVersion: "1.0.0",
		BaselineName:  baselineName,
		CurrentName:   currentName,
		Config:        *config,
		Diffs:         []NormalizedDiff{},
	}

	// Compare CPU usage
	result.Diffs = append(result.Diffs, compareCPU(baseline, current, config)...)

	// Compare memory usage
	result.Diffs = append(result.Diffs, compareMemory(baseline, current, config)...)

	// Compare host calls
	result.Diffs = append(result.Diffs, compareHostCalls(baseline, current, config)...)

	// Compare event counts
	result.Diffs = append(result.Diffs, compareEventCounts(baseline, current, config)...)

	// Compare call paths (normalized)
	result.Diffs = append(result.Diffs, compareCallPathsNormalized(baseline, current, config)...)

	// Calculate summary
	result.Summary = calculateSummary(result.Diffs, config)

	// Determine if there's a regression
	result.HasRegression = result.Summary.CriticalDiffs > 0 || 
		(config.FailOnThresholdViolation && result.Summary.ThresholdViolations > 0)

	return result
}

// compareCPU compares CPU instruction counts between traces
func compareCPU(baseline, current *ExecutionTrace, config *ComparisonConfig) []NormalizedDiff {
	var diffs []NormalizedDiff

	baselineCPU := extractTotalCPU(baseline)
	currentCPU := extractTotalCPU(current)

	if baselineCPU == 0 && currentCPU == 0 {
		return diffs
	}

	delta := int64(currentCPU) - int64(baselineCPU)
	absDelta := math.Abs(float64(delta))

	// Skip if below absolute threshold
	if absDelta < float64(config.CPUAbsoluteThreshold) {
		return diffs
	}

	var percentDelta float64
	if baselineCPU > 0 {
		percentDelta = (float64(delta) / float64(baselineCPU)) * 100
	}

	severity := determineSeverity(percentDelta, config.CPUThresholdPct, delta > 0)

	path := "total_cpu_instructions"
	reason := fmt.Sprintf("CPU instructions changed from %d to %d", baselineCPU, currentCPU)

	diffs = append(diffs, NormalizedDiff{
		Category:      "cpu",
		Path:          path,
		Baseline:      float64(baselineCPU),
		Current:       float64(currentCPU),
		AbsoluteDelta: float64(delta),
		PercentDelta:  percentDelta,
		Severity:      severity,
		Reason:        reason,
	})

	return diffs
}

// compareMemory compares memory usage between traces
func compareMemory(baseline, current *ExecutionTrace, config *ComparisonConfig) []NormalizedDiff {
	var diffs []NormalizedDiff

	baselineMem := extractTotalMemory(baseline)
	currentMem := extractTotalMemory(current)

	if baselineMem == 0 && currentMem == 0 {
		return diffs
	}

	delta := int64(currentMem) - int64(baselineMem)
	absDelta := math.Abs(float64(delta))

	// Skip if below absolute threshold
	if absDelta < float64(config.MemoryAbsoluteThreshold) {
		return diffs
	}

	var percentDelta float64
	if baselineMem > 0 {
		percentDelta = (float64(delta) / float64(baselineMem)) * 100
	}

	severity := determineSeverity(percentDelta, config.MemoryThresholdPct, delta > 0)

	path := "total_memory_bytes"
	reason := fmt.Sprintf("Memory usage changed from %d to %d bytes", baselineMem, currentMem)

	diffs = append(diffs, NormalizedDiff{
		Category:      "memory",
		Path:          path,
		Baseline:      float64(baselineMem),
		Current:       float64(currentMem),
		AbsoluteDelta: float64(delta),
		PercentDelta:  percentDelta,
		Severity:      severity,
		Reason:        reason,
	})

	return diffs
}

// compareHostCalls compares host function call counts between traces
func compareHostCalls(baseline, current *ExecutionTrace, config *ComparisonConfig) []NormalizedDiff {
	var diffs []NormalizedDiff

	baselineCalls := countHostCalls(baseline)
	currentCalls := countHostCalls(current)

	// Compare total calls
	if baselineCalls.Total != currentCalls.Total {
		delta := currentCalls.Total - baselineCalls.Total
		var percentDelta float64
		if baselineCalls.Total > 0 {
			percentDelta = (float64(delta) / float64(baselineCalls.Total)) * 100
		}

		severity := determineSeverity(percentDelta, config.HostCallThresholdPct, delta > 0)

		diffs = append(diffs, NormalizedDiff{
			Category:      "host_calls",
			Path:          "total_host_calls",
			Baseline:      float64(baselineCalls.Total),
			Current:       float64(currentCalls.Total),
			AbsoluteDelta: float64(delta),
			PercentDelta:  percentDelta,
			Severity:      severity,
			Reason:        fmt.Sprintf("Total host calls changed from %d to %d", baselineCalls.Total, currentCalls.Total),
		})
	}

	// Compare per-function calls
	for funcName, baselineCount := range baselineCalls.ByFunction {
		currentCount := currentCalls.ByFunction[funcName]
		if baselineCount != currentCount {
			delta := currentCount - baselineCount
			var percentDelta float64
			if baselineCount > 0 {
				percentDelta = (float64(delta) / float64(baselineCount)) * 100
			}

			severity := determineSeverity(percentDelta, config.HostCallThresholdPct, delta > 0)

			path := fmt.Sprintf("host_call::%s", funcName)
			diffs = append(diffs, NormalizedDiff{
				Category:      "host_calls",
				Path:          path,
				Baseline:      float64(baselineCount),
				Current:       float64(currentCount),
				AbsoluteDelta: float64(delta),
				PercentDelta:  percentDelta,
				Severity:      severity,
				Reason:        fmt.Sprintf("Host function %s calls changed from %d to %d", funcName, baselineCount, currentCount),
			})
		}
	}

	// Report new functions in current
	for funcName, currentCount := range currentCalls.ByFunction {
		if _, exists := baselineCalls.ByFunction[funcName]; !exists {
			diffs = append(diffs, NormalizedDiff{
				Category:      "host_calls",
				Path:          fmt.Sprintf("host_call::%s", funcName),
				Baseline:      0,
				Current:       float64(currentCount),
				AbsoluteDelta: float64(currentCount),
				PercentDelta:  100.0, // Infinite increase, cap at 100%
				Severity:      "warning",
				Reason:        fmt.Sprintf("New host function called: %s (%d times)", funcName, currentCount),
			})
		}
	}

	return diffs
}

// compareEventCounts compares event counts between traces
func compareEventCounts(baseline, current *ExecutionTrace, config *ComparisonConfig) []NormalizedDiff {
	var diffs []NormalizedDiff

	baselineCount := len(baseline.States)
	currentCount := len(current.States)

	if baselineCount != currentCount {
		delta := currentCount - baselineCount
		var percentDelta float64
		if baselineCount > 0 {
			percentDelta = (float64(delta) / float64(baselineCount)) * 100
		}

		severity := determineSeverity(percentDelta, config.EventCountThresholdPct, delta > 0)

		diffs = append(diffs, NormalizedDiff{
			Category:      "events",
			Path:          "total_events",
			Baseline:      float64(baselineCount),
			Current:       float64(currentCount),
			AbsoluteDelta: float64(delta),
			PercentDelta:  percentDelta,
			Severity:      severity,
			Reason:        fmt.Sprintf("Total execution steps changed from %d to %d", baselineCount, currentCount),
		})
	}

	return diffs
}

// compareCallPathsNormalized compares call paths with normalized representation
func compareCallPathsNormalized(baseline, current *ExecutionTrace, config *ComparisonConfig) []NormalizedDiff {
	var diffs []NormalizedDiff

	baselinePaths := extractCallPaths(baseline)
	currentPaths := extractCallPaths(current)

	// Compare path sets
	baselineSet := make(map[string]bool)
	for _, path := range baselinePaths {
		baselineSet[path] = true
	}

	currentSet := make(map[string]bool)
	for _, path := range currentPaths {
		currentSet[path] = true
	}

	// Report removed paths
	for path := range baselineSet {
		if !currentSet[path] {
			diffs = append(diffs, NormalizedDiff{
				Category:      "call_path",
				Path:          path,
				Baseline:      1,
				Current:       0,
				AbsoluteDelta: -1,
				PercentDelta:  -100,
				Severity:      "critical",
				Reason:        fmt.Sprintf("Call path removed: %s", path),
			})
		}
	}

	// Report added paths
	for path := range currentSet {
		if !baselineSet[path] {
			diffs = append(diffs, NormalizedDiff{
				Category:      "call_path",
				Path:          path,
				Baseline:      0,
				Current:       1,
				AbsoluteDelta: 1,
				PercentDelta:  100,
				Severity:      "warning",
				Reason:        fmt.Sprintf("New call path added: %s", path),
			})
		}
	}

	return diffs
}

// Helper types and functions

type HostCallStats struct {
	Total      int
	ByFunction map[string]int
}

func countHostCalls(trace *ExecutionTrace) HostCallStats {
	stats := HostCallStats{
		ByFunction: make(map[string]int),
	}

	for _, state := range trace.States {
		if state.Operation == "host_function" && state.Function != "" {
			stats.Total++
			stats.ByFunction[state.Function]++
		}
	}

	return stats
}

func extractTotalCPU(trace *ExecutionTrace) uint64 {
	// Extract CPU from Cost annotations if present
	var total uint64
	for _, state := range trace.States {
		if state.Cost != nil && state.Cost.CPU > 0 {
			total += state.Cost.CPU
		}
	}
	return total
}

func extractTotalMemory(trace *ExecutionTrace) uint64 {
	// Extract memory from Cost annotations if present
	var total uint64
	for _, state := range trace.States {
		if state.Cost != nil && state.Cost.Memory > 0 {
			total += state.Cost.Memory
		}
	}
	return total
}

func extractCallPaths(trace *ExecutionTrace) []string {
	paths := make(map[string]bool)
	for _, state := range trace.States {
		if state.ContractID != "" && state.Function != "" {
			path := fmt.Sprintf("%s::%s", state.ContractID, state.Function)
			paths[path] = true
		}
	}

	result := make([]string, 0, len(paths))
	for path := range paths {
		result = append(result, path)
	}
	return result
}

func determineSeverity(percentDelta, threshold float64, isIncrease bool) string {
	if isIncrease {
		if percentDelta >= threshold*2 {
			return "critical"
		}
		if percentDelta >= threshold {
			return "warning"
		}
	}
	return "info"
}

func calculateSummary(diffs []NormalizedDiff, config *ComparisonConfig) ComparisonSummary {
	summary := &ComparisonSummary{}

	for _, diff := range diffs {
		summary.TotalDiffs++
		switch diff.Severity {
		case "critical":
			summary.CriticalDiffs++
		case "warning":
			summary.WarningDiffs++
		case "info":
			summary.InfoDiffs++
		}

		// Check threshold violations
		if isThresholdViolation(diff, config) {
			summary.ThresholdViolations++
		}
	}

	return *summary
}

func isThresholdViolation(diff NormalizedDiff, config *ComparisonConfig) bool {
	switch diff.Category {
	case "cpu":
		return diff.PercentDelta > config.CPUThresholdPct
	case "memory":
		return diff.PercentDelta > config.MemoryThresholdPct
	case "host_calls":
		return diff.PercentDelta > config.HostCallThresholdPct
	case "events":
		return config.EventCountThresholdPct > 0 && diff.PercentDelta > config.EventCountThresholdPct
	default:
		return false
	}
}

// ToJSON serializes the normalized comparison result to JSON
func (r *NormalizedComparisonResult) ToJSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

// RenderTable prints a human-readable table of the normalized comparison
func (r *NormalizedComparisonResult) RenderTable() {
	fmt.Printf("\n%s Normalized Trace Comparison %s\n", strings.Repeat("═", 20), strings.Repeat("═", 20))
	fmt.Printf("  %s vs %s\n", r.BaselineName, r.CurrentName)
	fmt.Printf("  Schema Version: %s\n\n", r.SchemaVersion)

	// Group diffs by category
	categories := map[string][]NormalizedDiff{}
	for _, diff := range r.Diffs {
		categories[diff.Category] = append(categories[diff.Category], diff)
	}

	// Render each category
	for _, cat := range []string{"cpu", "memory", "host_calls", "events", "call_path"} {
		if diffs, ok := categories[cat]; ok && len(diffs) > 0 {
			renderCategoryTable(cat, diffs)
		}
	}

	// Render summary
	renderSummaryTable(r.Summary, r.HasRegression)
}

func renderCategoryTable(category string, diffs []NormalizedDiff) {
	fmt.Printf("\n── %s ──\n", strings.ToUpper(category))
	fmt.Printf("%-40s %12s %12s %12s %12s %10s\n", "Path", "Baseline", "Current", "Abs Delta", "% Delta", "Severity")
	fmt.Printf("%s\n", strings.Repeat("-", 110))

	for _, diff := range diffs {
		severityColor := getSeverityColor(diff.Severity)
		fmt.Printf("%-40s %12.0f %12.0f %12.0f %11.1f%% %s%-9s%s\n",
			diff.Path,
			diff.Baseline,
			diff.Current,
			diff.AbsoluteDelta,
			diff.PercentDelta,
			severityColor, diff.Severity, "\x1b[0m")
	}
}

func renderSummaryTable(summary ComparisonSummary, hasRegression bool) {
	fmt.Printf("\n── Summary ──\n")
	fmt.Printf("  Total Differences:      %d\n", summary.TotalDiffs)
	fmt.Printf("  Critical:               %d\n", summary.CriticalDiffs)
	fmt.Printf("  Warnings:               %d\n", summary.WarningDiffs)
	fmt.Printf("  Info:                   %d\n", summary.InfoDiffs)
	fmt.Printf("  Threshold Violations:   %d\n", summary.ThresholdViolations)

	if hasRegression {
		fmt.Printf("\n  ⚠️  REGRESSION DETECTED\n")
	} else {
		fmt.Printf("\n  ✓ No regressions detected\n")
	}
	fmt.Println()
}

func getSeverityColor(severity string) string {
	switch severity {
	case "critical":
		return "\x1b[31m" // Red
	case "warning":
		return "\x1b[33m" // Yellow
	case "info":
		return "\x1b[36m" // Cyan
	default:
		return "\x1b[0m"
	}
}
