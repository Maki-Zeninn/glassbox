// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"testing"

	"github.com/dotandev/glassbox/internal/dwarf"
)

// TestConfidenceLevel_String tests string representation of confidence levels
func TestConfidenceLevel_String(t *testing.T) {
	tests := []struct {
		level ConfidenceLevel
		want  string
	}{
		{ConfidenceExact, "exact"},
		{ConfidenceHigh, "high"},
		{ConfidenceMedium, "medium"},
		{ConfidenceLow, "low"},
		{ConfidenceUnknown, "unknown"},
		{ConfidenceLevel(99), "invalid"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			// Test through the dwarf package since we're using aliases
			dwarfLevel := dwarf.ConfidenceLevel(tt.level)
			if got := dwarfLevel.String(); got != tt.want {
				t.Errorf("ConfidenceLevel.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestDefaultConfidence tests confidence creation
func TestDefaultConfidence(t *testing.T) {
	conf := DefaultConfidence(ConfidenceExact, ReasonDWARFExact)
	if conf.Level != dwarf.ConfidenceExact {
		t.Errorf("DefaultConfidence() level = %v, want %v", conf.Level, dwarf.ConfidenceExact)
	}
	if conf.Reason != dwarf.ReasonDWARFExact {
		t.Errorf("DefaultConfidence() reason = %v, want %v", conf.Reason, dwarf.ReasonDWARFExact)
	}
	if conf.Context != "" {
		t.Errorf("DefaultConfidence() context = %v, want empty", conf.Context)
	}
}

// TestConfidenceWithMessage tests confidence creation with context
func TestConfidenceWithMessage(t *testing.T) {
	conf := ConfidenceWithMessage(ConfidenceHigh, ReasonDWARFLineOnly, "column data unavailable")
	if conf.Level != dwarf.ConfidenceHigh {
		t.Errorf("ConfidenceWithMessage() level = %v, want %v", conf.Level, dwarf.ConfidenceHigh)
	}
	if conf.Reason != dwarf.ReasonDWARFLineOnly {
		t.Errorf("ConfidenceWithMessage() reason = %v, want %v", conf.Reason, dwarf.ReasonDWARFLineOnly)
	}
	if conf.Context != "column data unavailable" {
		t.Errorf("ConfidenceWithMessage() context = %v, want %v", conf.Context, "column data unavailable")
	}
}

// TestConfidence_IsHighConfidence tests high confidence detection
func TestConfidence_IsHighConfidence(t *testing.T) {
	tests := []struct {
		name  string
		conf  Confidence
		want  bool
	}{
		{"exact", DefaultConfidence(ConfidenceExact, ReasonDWARFExact), true},
		{"high", DefaultConfidence(ConfidenceHigh, ReasonDWARFLineOnly), true},
		{"medium", DefaultConfidence(ConfidenceMedium, ReasonInlineExpansion), false},
		{"low", DefaultConfidence(ConfidenceLow, ReasonHeuristicMatch), false},
		{"unknown", DefaultConfidence(ConfidenceUnknown, ReasonMissingDebugInfo), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.conf.IsHighConfidence(); got != tt.want {
				t.Errorf("Confidence.IsHighConfidence() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestConfidence_IsLowConfidence tests low confidence detection
func TestConfidence_IsLowConfidence(t *testing.T) {
	tests := []struct {
		name  string
		conf  Confidence
		want  bool
	}{
		{"exact", DefaultConfidence(ConfidenceExact, ReasonDWARFExact), false},
		{"high", DefaultConfidence(ConfidenceHigh, ReasonDWARFLineOnly), false},
		{"medium", DefaultConfidence(ConfidenceMedium, ReasonInlineExpansion), false},
		{"low", DefaultConfidence(ConfidenceLow, ReasonHeuristicMatch), true},
		{"unknown", DefaultConfidence(ConfidenceUnknown, ReasonMissingDebugInfo), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.conf.IsLowConfidence(); got != tt.want {
				t.Errorf("Confidence.IsLowConfidence() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestConfidence_Description tests confidence description generation
func TestConfidence_Description(t *testing.T) {
	tests := []struct {
		name string
		conf Confidence
		want string
	}{
		{
			name: "dwarf exact",
			conf: DefaultConfidence(ConfidenceExact, ReasonDWARFExact),
			want: "Exact location from DWARF debug symbols with line and column precision",
		},
		{
			name: "dwarf line only",
			conf: DefaultConfidence(ConfidenceHigh, ReasonDWARFLineOnly),
			want: "Line information from DWARF, but column data unavailable",
		},
		{
			name: "inline expansion",
			conf: DefaultConfidence(ConfidenceMedium, ReasonInlineExpansion),
			want: "Location from inlined function expansion, may have minor imprecision",
		},
		{
			name: "heuristic match",
			conf: DefaultConfidence(ConfidenceLow, ReasonHeuristicMatch),
			want: "Determined through heuristic pattern matching",
		},
		{
			name: "unknown reason",
			conf: DefaultConfidence(ConfidenceLow, ReasonCode("unknown_reason")),
			want: "Unknown confidence reason",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.conf.Description(); got != tt.want {
				t.Errorf("Confidence.Description() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestConfidenceLevelFromDWARFQuality tests confidence level determination from DWARF quality
func TestConfidenceLevelFromDWARFQuality(t *testing.T) {
	tests := []struct {
		name          string
		hasLineInfo   bool
		hasColumnInfo bool
		hasInlineInfo bool
		wantLevel     ConfidenceLevel
		wantReason    ReasonCode
	}{
		{
			name:          "full DWARF info",
			hasLineInfo:   true,
			hasColumnInfo: true,
			hasInlineInfo: false,
			wantLevel:     ConfidenceExact,
			wantReason:    ReasonDWARFExact,
		},
		{
			name:          "line only",
			hasLineInfo:   true,
			hasColumnInfo: false,
			hasInlineInfo: false,
			wantLevel:     ConfidenceHigh,
			wantReason:    ReasonDWARFLineOnly,
		},
		{
			name:          "inline only",
			hasLineInfo:   false,
			hasColumnInfo: false,
			hasInlineInfo: true,
			wantLevel:     ConfidenceMedium,
			wantReason:    ReasonInlineExpansion,
		},
		{
			name:          "no debug info",
			hasLineInfo:   false,
			hasColumnInfo: false,
			hasInlineInfo: false,
			wantLevel:     ConfidenceLow,
			wantReason:    ReasonMissingDebugInfo,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conf := dwarf.ConfidenceLevelFromDWARFQuality(tt.hasLineInfo, tt.hasColumnInfo, tt.hasInlineInfo)
			if conf.Level != dwarf.ConfidenceLevel(tt.wantLevel) {
				t.Errorf("ConfidenceLevelFromDWARFQuality() level = %v, want %v", conf.Level, dwarf.ConfidenceLevel(tt.wantLevel))
			}
			if conf.Reason != dwarf.ReasonCode(tt.wantReason) {
				t.Errorf("ConfidenceLevelFromDWARFQuality() reason = %v, want %v", conf.Reason, dwarf.ReasonCode(tt.wantReason))
			}
		})
	}
}

// TestSourceRefWithConfidence tests SourceRef with confidence metadata
func TestSourceRefWithConfidence(t *testing.T) {
	tests := []struct {
		name       string
		sourceRef  SourceRef
		hasConf    bool
		confLevel ConfidenceLevel
	}{
		{
			name: "with high confidence",
			sourceRef: SourceRef{
				File:       "token.rs",
				Line:       42,
				Column:     7,
				Function:   "transfer",
				Confidence: &Confidence{Level: ConfidenceHigh, Reason: ReasonDWARFLineOnly},
			},
			hasConf:    true,
			confLevel: ConfidenceHigh,
		},
		{
			name: "with low confidence",
			sourceRef: SourceRef{
				File:       "pool.rs",
				Line:       88,
				Column:     0,
				Function:   "deposit",
				Confidence: &Confidence{Level: ConfidenceLow, Reason: ReasonHeuristicMatch},
			},
			hasConf:    true,
			confLevel: ConfidenceLow,
		},
		{
			name: "without confidence",
			sourceRef: SourceRef{
				File:     "auth.rs",
				Line:     15,
				Column:   3,
				Function: "authenticate",
			},
			hasConf:    false,
			confLevel: ConfidenceUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.hasConf && tt.sourceRef.Confidence == nil {
				t.Error("expected confidence to be set, but it was nil")
			}
			if !tt.hasConf && tt.sourceRef.Confidence != nil {
				t.Error("expected confidence to be nil, but it was set")
			}
			if tt.hasConf && tt.sourceRef.Confidence.Level != dwarf.ConfidenceLevel(tt.confLevel) {
				t.Errorf("confidence level = %v, want %v", tt.sourceRef.Confidence.Level, dwarf.ConfidenceLevel(tt.confLevel))
			}
		})
	}
}

// TestConfidenceIntegration tests integration of confidence across the system
func TestConfidenceIntegration(t *testing.T) {
	// Test creating a complete confidence flow
	conf := ConfidenceWithMessage(ConfidenceExact, ReasonDWARFExact, "verified with column precision")
	
	// Verify all fields are populated correctly
	if conf.Level != dwarf.ConfidenceExact {
		t.Errorf("confidence level = %v, want exact", conf.Level)
	}
	if conf.Reason != dwarf.ReasonDWARFExact {
		t.Errorf("confidence reason = %v, want dwarf_exact", conf.Reason)
	}
	if conf.Context != "verified with column precision" {
		t.Errorf("confidence context = %v, want verification message", conf.Context)
	}
	
	// Test helper methods
	if !conf.IsHighConfidence() {
		t.Error("exact confidence should be high confidence")
	}
	if conf.IsLowConfidence() {
		t.Error("exact confidence should not be low confidence")
	}
	
	// Test low confidence scenario
	lowConf := DefaultConfidence(ConfidenceLow, ReasonHeuristicMatch)
	if lowConf.IsHighConfidence() {
		t.Error("low confidence should not be high confidence")
	}
	if !lowConf.IsLowConfidence() {
		t.Error("low confidence should be low confidence")
	}
}

// TestConfidenceFixtureExact represents exact DWARF mapping fixture
func TestConfidenceFixtureExact(t *testing.T) {
	conf := DefaultConfidence(ConfidenceExact, ReasonDWARFExact)
	if conf.Level != dwarf.ConfidenceExact {
		t.Errorf("fixture exact confidence level = %v, want exact", conf.Level)
	}
	if conf.Reason != dwarf.ReasonDWARFExact {
		t.Errorf("fixture exact confidence reason = %v, want dwarf_exact", conf.Reason)
	}
}

// TestConfidenceFixtureInline represents inline function expansion fixture
func TestConfidenceFixtureInline(t *testing.T) {
	conf := DefaultConfidence(ConfidenceMedium, ReasonInlineExpansion)
	if conf.Level != dwarf.ConfidenceMedium {
		t.Errorf("fixture inline confidence level = %v, want medium", conf.Level)
	}
	if conf.Reason != dwarf.ReasonInlineExpansion {
		t.Errorf("fixture inline confidence reason = %v, want inline_expansion", conf.Reason)
	}
}

// TestConfidenceFixtureMissing represents missing debug info fixture
func TestConfidenceFixtureMissing(t *testing.T) {
	conf := DefaultConfidence(ConfidenceLow, ReasonMissingDebugInfo)
	if conf.Level != dwarf.ConfidenceLow {
		t.Errorf("fixture missing confidence level = %v, want low", conf.Level)
	}
	if conf.Reason != dwarf.ReasonMissingDebugInfo {
		t.Errorf("fixture missing confidence reason = %v, want missing_debug_info", conf.Reason)
	}
}

// TestConfidenceFixtureHeuristic represents heuristic mapping fixture
func TestConfidenceFixtureHeuristic(t *testing.T) {
	conf := ConfidenceWithMessage(ConfidenceLow, ReasonHeuristicMatch, "symbol name pattern matching")
	if conf.Level != dwarf.ConfidenceLow {
		t.Errorf("fixture heuristic confidence level = %v, want low", conf.Level)
	}
	if conf.Reason != dwarf.ReasonHeuristicMatch {
		t.Errorf("fixture heuristic confidence reason = %v, want heuristic_match", conf.Reason)
	}
	if conf.Context != "symbol name pattern matching" {
		t.Errorf("fixture heuristic confidence context = %v, want pattern matching message", conf.Context)
	}
}
