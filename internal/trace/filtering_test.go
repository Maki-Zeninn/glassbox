// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"testing"
	"time"

	"github.com/dotandev/glassbox/internal/dwarf"
)

// TestFilterByConfidence tests filtering trace states by confidence level
func TestFilterByConfidence(t *testing.T) {
	// Create a test trace with various confidence levels
	trace := &ExecutionTrace{
		TransactionHash: "test_hash",
		StartTime:       time.Now(),
		EndTime:         time.Now(),
		States: []ExecutionState{
			{
				Step:            1,
				Operation:       "exact_operation",
				ConfidenceLevel:  "exact",
				ConfidenceReason: "dwarf_exact",
			},
			{
				Step:            2,
				Operation:       "high_operation",
				ConfidenceLevel:  "high",
				ConfidenceReason: "dwarf_line_only",
			},
			{
				Step:            3,
				Operation:       "medium_operation",
				ConfidenceLevel:  "medium",
				ConfidenceReason: "inline_expansion",
			},
			{
				Step:            4,
				Operation:       "low_operation",
				ConfidenceLevel:  "low",
				ConfidenceReason: "heuristic_match",
			},
			{
				Step:            5,
				Operation:       "unknown_operation",
				ConfidenceLevel:  "",
				ConfidenceReason: "",
			},
		},
	}

	tests := []struct {
		name      string
		minLevel  ConfidenceLevel
		wantCount int
	}{
		{
			name:      "filter by exact",
			minLevel:  ConfidenceExact,
			wantCount: 1, // only exact level
		},
		{
			name:      "filter by high",
			minLevel:  ConfidenceHigh,
			wantCount: 2, // exact + high
		},
		{
			name:      "filter by medium",
			minLevel: ConfidenceMedium,
			wantCount: 3, // exact + high + medium
		},
		{
			name:      "filter by low",
			minLevel:  ConfidenceLow,
			wantCount: 4, // exact + high + medium + low
		},
		{
			name:      "filter by unknown",
			minLevel:  ConfidenceUnknown,
			wantCount: 5, // all states
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filtered := trace.FilterByConfidence(tt.minLevel)
			if filtered == nil {
				t.Error("filtered trace should not be nil")
				return
			}
			if len(filtered.States) != tt.wantCount {
				t.Errorf("FilterByConfidence() returned %d states, want %d", len(filtered.States), tt.wantCount)
			}
		})
	}
}

// TestFilterByConfidenceReason tests filtering trace states by reason codes
func TestFilterByConfidenceReason(t *testing.T) {
	trace := &ExecutionTrace{
		TransactionHash: "test_hash",
		StartTime:       time.Now(),
		EndTime:         time.Now(),
		States: []ExecutionState{
			{
				Step:            1,
				Operation:       "dwarf_exact_op",
				ConfidenceLevel:  "exact",
				ConfidenceReason: "dwarf_exact",
			},
			{
				Step:            2,
				Operation:       "dwarf_line_op",
				ConfidenceLevel:  "high",
				ConfidenceReason: "dwarf_line_only",
			},
			{
				Step:            3,
				Operation:       "inline_op",
				ConfidenceLevel:  "medium",
				ConfidenceReason: "inline_expansion",
			},
			{
				Step:            4,
				Operation:       "heuristic_op",
				ConfidenceLevel:  "low",
				ConfidenceReason: "heuristic_match",
			},
		},
	}

	tests := []struct {
		name         string
		reasons      []ReasonCode
		wantCount    int
	}{
		{
			name:      "filter by dwarf exact",
			reasons:   []ReasonCode{ReasonDWARFExact},
			wantCount: 1,
		},
		{
			name:      "filter by dwarf reasons",
			reasons:   []ReasonCode{ReasonDWARFExact, ReasonDWARFLineOnly},
			wantCount: 2,
		},
		{
			name:      "filter by non-existent reason",
			reasons:   []ReasonCode{ReasonCode("non_existent")},
			wantCount: 0,
		},
		{
			name:      "filter by multiple reasons",
			reasons:   []ReasonCode{ReasonDWARFExact, ReasonHeuristicMatch},
			wantCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filtered := trace.FilterByConfidenceReason(tt.reasons...)
			if filtered == nil {
				t.Error("filtered trace should not be nil")
				return
			}
			if len(filtered.States) != tt.wantCount {
				t.Errorf("FilterByConfidenceReason() returned %d states, want %d", len(filtered.States), tt.wantCount)
			}
		})
	}
}

// TestGetHighConfidenceStates tests retrieving high confidence states
func TestGetHighConfidenceStates(t *testing.T) {
	trace := &ExecutionTrace{
		TransactionHash: "test_hash",
		StartTime:       time.Now(),
		EndTime:         time.Now(),
		States: []ExecutionState{
			{Step: 1, Operation: "exact_op", ConfidenceLevel: "exact"},
			{Step: 2, Operation: "high_op", ConfidenceLevel: "high"},
			{Step: 3, Operation: "medium_op", ConfidenceLevel: "medium"},
			{Step: 4, Operation: "low_op", ConfidenceLevel: "low"},
		},
	}

	highConfidence := trace.GetHighConfidenceStates()
	if len(highConfidence) != 2 {
		t.Errorf("GetHighConfidenceStates() returned %d states, want 2", len(highConfidence))
	}

	for _, state := range highConfidence {
		if state.ConfidenceLevel != "exact" && state.ConfidenceLevel != "high" {
			t.Errorf("high confidence state has level %s", state.ConfidenceLevel)
		}
	}
}

// TestGetLowConfidenceStates tests retrieving low confidence states
func TestGetLowConfidenceStates(t *testing.T) {
	trace := &ExecutionTrace{
		TransactionHash: "test_hash",
		StartTime:       time.Now(),
		EndTime:         time.Now(),
		States: []ExecutionState{
			{Step: 1, Operation: "exact_op", ConfidenceLevel: "exact"},
			{Step: 2, Operation: "high_op", ConfidenceLevel: "high"},
			{Step: 3, Operation: "medium_op", ConfidenceLevel: "medium"},
			{Step: 4, Operation: "low_op", ConfidenceLevel: "low"},
			{Step: 5, Operation: "unknown_op", ConfidenceLevel: ""},
		},
	}

	lowConfidence := trace.GetLowConfidenceStates()
	if len(lowConfidence) != 2 {
		t.Errorf("GetLowConfidenceStates() returned %d states, want 2", len(lowConfidence))
	}

	for _, state := range lowConfidence {
		if state.ConfidenceLevel != "low" && state.ConfidenceLevel != "" {
			t.Errorf("low confidence state has level %s", state.ConfidenceLevel)
		}
	}
}

// TestGetConfidenceSummary tests confidence level summary
func TestGetConfidenceSummary(t *testing.T) {
	trace := &ExecutionTrace{
		TransactionHash: "test_hash",
		StartTime:       time.Now(),
		EndTime:         time.Now(),
		States: []ExecutionState{
			{Step: 1, Operation: "exact_op", ConfidenceLevel: "exact"},
			{Step: 2, Operation: "high_op", ConfidenceLevel: "high"},
			{Step: 3, Operation: "high_op", ConfidenceLevel: "high"},
			{Step: 4, Operation: "medium_op", ConfidenceLevel: "medium"},
			{Step: 5, Operation: "low_op", ConfidenceLevel: "low"},
			{Step: 6, Operation: "unknown_op", ConfidenceLevel: ""},
		},
	}

	summary := trace.GetConfidenceSummary()
	if summary == nil {
		t.Error("GetConfidenceSummary() returned nil")
		return
	}

	tests := []struct {
		level ConfidenceLevel
		want  int
	}{
		{ConfidenceExact, 1},
		{ConfidenceHigh, 2},
		{ConfidenceMedium, 1},
		{ConfidenceLow, 1},
		{ConfidenceUnknown, 1},
	}

	for _, tt := range tests {
		if summary[tt.level] != tt.want {
			t.Errorf("summary[%v] = %d, want %d", tt.level, summary[tt.level], tt.want)
		}
	}
}

// TestGetReasonSummary tests reason code summary
func TestGetReasonSummary(t *testing.T) {
	trace := &ExecutionTrace{
		TransactionHash: "test_hash",
		StartTime:       time.Now(),
		EndTime:         time.Now(),
		States: []ExecutionState{
			{Step: 1, Operation: "op1", ConfidenceReason: "dwarf_exact"},
			{Step: 2, Operation: "op2", ConfidenceReason: "dwarf_exact"},
			{Step: 3, Operation: "op3", ConfidenceReason: "dwarf_line_only"},
			{Step: 4, Operation: "op4", ConfidenceReason: "heuristic_match"},
		},
	}

	summary := trace.GetReasonSummary()
	if summary == nil {
		t.Error("GetReasonSummary() returned nil")
		return
	}

	tests := []struct {
		reason ReasonCode
		want   int
	}{
		{ReasonDWARFExact, 2},
		{ReasonDWARFLineOnly, 1},
		{ReasonHeuristicMatch, 1},
	}

	for _, tt := range tests {
		if summary[tt.reason] != tt.want {
			t.Errorf("summary[%v] = %d, want %d", tt.reason, summary[tt.reason], tt.want)
		}
	}
}

// TestFilteringWithNilTrace tests filtering with nil trace
func TestFilteringWithNilTrace(t *testing.T) {
	var trace *ExecutionState

	filtered := trace.FilterByConfidence(ConfidenceHigh)
	if filtered != nil {
		t.Error("FilterByConfidence() with nil trace should return nil")
	}

	filtered = trace.FilterByConfidenceReason(ReasonDWARFExact)
	if filtered != nil {
		t.Error("FilterByConfidenceReason() with nil trace should return nil")
	}

	highConfidence := trace.GetHighConfidenceStates()
	if highConfidence != nil {
		t.Error("GetHighConfidenceStates() with nil trace should return nil")
	}

	lowConfidence := trace.GetLowConfidenceStates()
	if lowConfidence != nil {
		t.Error("GetLowConfidenceStates() with nil trace should return nil")
	}

	summary := trace.GetConfidenceSummary()
	if summary != nil {
		t.Error("GetConfidenceSummary() with nil trace should return nil")
	}

	reasonSummary := trace.GetReasonSummary()
	if reasonSummary != nil {
		t.Error("GetReasonSummary() with nil trace should return nil")
	}
}

// TestFilteringPreservesMetadata tests that filtering preserves trace metadata
func TestFilteringPreservesMetadata(t *testing.T) {
	trace := &ExecutionTrace{
		TransactionHash: "test_hash",
		StartTime:       time.Now(),
		EndTime:         time.Now(),
		SnapshotInterval: 100,
		Annotations:     TraceAnnotations{Key: "value"},
		States: []ExecutionState{
			{Step: 1, Operation: "exact_op", ConfidenceLevel: "exact"},
			{Step: 2, Operation: "low_op", ConfidenceLevel: "low"},
		},
	}

	filtered := trace.FilterByConfidence(ConfidenceHigh)
	if filtered.TransactionHash != trace.TransactionHash {
		t.Error("filtering should preserve TransactionHash")
	}
	if filtered.SnapshotInterval != trace.SnapshotInterval {
		t.Error("filtering should preserve SnapshotInterval")
	}
	if len(filtered.Annotations) != len(trace.Annotations) {
		t.Error("filtering should preserve Annotations")
	}
}

// TestJSONFiltering tests that JSON consumers can filter by confidence
func TestJSONFiltering(t *testing.T) {
	trace := &ExecutionTrace{
		TransactionHash: "test_hash",
		StartTime:       time.Now(),
		EndTime:         time.Now(),
		States: []ExecutionState{
			{Step: 1, Operation: "exact_op", ConfidenceLevel: "exact", ConfidenceReason: "dwarf_exact"},
			{Step: 2, Operation: "low_op", ConfidenceLevel: "low", ConfidenceReason: "heuristic_match"},
		},
	}

	// Simulate JSON consumer filtering by confidence level
	highConfidence := trace.FilterByConfidence(ConfidenceHigh)
	if len(highConfidence.States) != 1 {
		t.Errorf("JSON consumer should be able to filter to get %d high confidence states, got %d", 1, len(highConfidence.States))
	}

	// Simulate JSON consumer filtering by reason code
	dwarfOnly := trace.FilterByConfidenceReason(ReasonDWARFExact, ReasonDWARFLineOnly)
	if len(dwarfOnly.States) != 1 {
		t.Errorf("JSON consumer should be able to filter by reason to get %d DWARF states, got %d", 1, len(dwarfOnly.States))
	}
}
