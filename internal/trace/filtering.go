// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"github.com/dotandev/glassbox/internal/dwarf"
)

// FilterByConfidence filters trace states based on confidence level.
// Returns a new ExecutionTrace containing only states that meet the confidence criteria.
// States without confidence information are treated as ConfidenceUnknown.
func (t *ExecutionTrace) FilterByConfidence(minLevel ConfidenceLevel) *ExecutionTrace {
	if t == nil {
		return nil
	}

	filtered := &ExecutionTrace{
		TransactionHash: t.TransactionHash,
		StartTime:       t.StartTime,
		EndTime:         t.EndTime,
		SnapshotInterval: t.SnapshotInterval,
		Annotations:     t.Annotations,
	}

	for _, state := range t.States {
		confidenceLevel := dwarf.ConfidenceUnknown
		if state.ConfidenceLevel != "" {
			// Convert string level to ConfidenceLevel
			switch state.ConfidenceLevel {
			case "exact":
				confidenceLevel = dwarf.ConfidenceExact
			case "high":
				confidenceLevel = dwarf.ConfidenceHigh
			case "medium":
				confidenceLevel = dwarf.ConfidenceMedium
			case "low":
				confidenceLevel = dwarf.ConfidenceLow
			}
		}

		// Include states that meet or exceed the minimum confidence level
		if confidenceLevel >= dwarf.ConfidenceLevel(minLevel) {
			filtered.States = append(filtered.States, state)
		}
	}

	return filtered
}

// FilterByConfidenceReason filters trace states based on specific reason codes.
// Returns a new ExecutionTrace containing only states with matching reason codes.
// States without confidence information are excluded.
func (t *ExecutionTrace) FilterByConfidenceReason(reasons ...ReasonCode) *ExecutionTrace {
	if t == nil || len(reasons) == 0 {
		return t
	}

	filtered := &ExecutionTrace{
		TransactionHash: t.TransactionHash,
		StartTime:       t.StartTime,
		EndTime:         t.EndTime,
		SnapshotInterval: t.SnapshotInterval,
		Annotations:     t.Annotations,
	}

	reasonSet := make(map[ReasonCode]bool)
	for _, reason := range reasons {
		reasonSet[reason] = true
	}

	for _, state := range t.States {
		if state.ConfidenceReason != "" {
			if reasonSet[ReasonCode(state.ConfidenceReason)] {
				filtered.States = append(filtered.States, state)
			}
		}
	}

	return filtered
}

// GetHighConfidenceStates returns only states with high or exact confidence.
func (t *ExecutionTrace) GetHighConfidenceStates() []*ExecutionState {
	if t == nil {
		return nil
	}

	var highConfidenceStates []*ExecutionState
	for i := range t.States {
		state := &t.States[i]
		if state.ConfidenceLevel == "exact" || state.ConfidenceLevel == "high" {
			highConfidenceStates = append(highConfidenceStates, state)
		}
	}

	return highConfidenceStates
}

// GetLowConfidenceStates returns only states with low or unknown confidence.
func (t *ExecutionTrace) GetLowConfidenceStates() []*ExecutionState {
	if t == nil {
		return nil
	}

	var lowConfidenceStates []*ExecutionState
	for i := range t.States {
		state := &t.States[i]
		if state.ConfidenceLevel == "low" || state.ConfidenceLevel == "unknown" || state.ConfidenceLevel == "" {
			lowConfidenceStates = append(lowConfidenceStates, state)
		}
	}

	return lowConfidenceStates
}

// GetConfidenceSummary returns a summary of confidence levels across all states.
func (t *ExecutionTrace) GetConfidenceSummary() map[ConfidenceLevel]int {
	if t == nil {
		return nil
	}

	summary := map[ConfidenceLevel]int{
		ConfidenceUnknown: 0,
	}

	for _, state := range t.States {
		switch state.ConfidenceLevel {
		case "exact":
			summary[ConfidenceExact]++
		case "high":
			summary[ConfidenceHigh]++
		case "medium":
			summary[ConfidenceMedium]++
		case "low":
			summary[ConfidenceLow]++
		default:
			summary[ConfidenceUnknown]++
		}
	}

	return summary
}

// GetReasonSummary returns a summary of reason codes across all states.
func (t *ExecutionTrace) GetReasonSummary() map[ReasonCode]int {
	if t == nil {
		return nil
	}

	summary := make(map[ReasonCode]int)

	for _, state := range t.States {
		if state.ConfidenceReason != "" {
			summary[ReasonCode(state.ConfidenceReason)]++
		}
	}

	return summary
}
