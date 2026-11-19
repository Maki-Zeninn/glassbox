// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package sourcemap

import (
	"testing"
)

// TestConfidenceLevelFromMatchKind tests match kind to confidence level conversion
func TestConfidenceLevelFromMatchKind(t *testing.T) {
	tests := []struct {
		kind MatchKind
		want string
	}{
		{MatchExactOffset, "exact"},
		{MatchFunction, "high"},
		{MatchLineTable, "medium"},
		{MatchHeuristic, "low"},
		{MatchUnknown, "unknown"},
		{MatchKind("invalid"), "unknown"},
	}

	for _, tt := range tests {
		t.Run(string(tt.kind), func(t *testing.T) {
			if got := ConfidenceLevelFromMatchKind(tt.kind); got != tt.want {
				t.Errorf("ConfidenceLevelFromMatchKind() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestReasonFromMatchKind tests match kind to reason code conversion
func TestReasonFromMatchKind(t *testing.T) {
	tests := []struct {
		kind MatchKind
		want string
	}{
		{MatchExactOffset, "dwarf_exact_offset"},
		{MatchFunction, "dwarf_function_level"},
		{MatchLineTable, "dwarf_partial_line_table"},
		{MatchHeuristic, "heuristic_inference"},
		{MatchUnknown, "no_mapping_available"},
		{MatchKind("invalid"), "unknown_reason"},
	}

	for _, tt := range tests {
		t.Run(string(tt.kind), func(t *testing.T) {
			if got := ReasonFromMatchKind(tt.kind); got != tt.want {
				t.Errorf("ReasonFromMatchKind() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestDetailedConfidenceStructure tests detailed confidence structure
func TestDetailedConfidenceStructure(t *testing.T) {
	conf := &DetailedConfidence{
		Level:   "exact",
		Reason:  "dwarf_exact_offset",
		Context: "stage=full_dwarf,addr=0x1234",
	}

	if conf.Level != "exact" {
		t.Errorf("detailed confidence level = %v, want exact", conf.Level)
	}
	if conf.Reason != "dwarf_exact_offset" {
		t.Errorf("detailed confidence reason = %v, want dwarf_exact_offset", conf.Reason)
	}
	if conf.Context != "stage=full_dwarf,addr=0x1234" {
		t.Errorf("detailed confidence context = %v, want stage info", conf.Context)
	}
}

// TestConfidenceForKind tests confidence scoring
func TestConfidenceForKind(t *testing.T) {
	tests := []struct {
		name           string
		kind           MatchKind
		lineKnown      bool
		columnKnown    bool
		heuristicStage string
		want           int
	}{
		{
			name:           "exact offset with column",
			kind:           MatchExactOffset,
			lineKnown:      true,
			columnKnown:    true,
			heuristicStage: "",
			want:           ConfidenceExactOffset,
		},
		{
			name:           "exact offset without column",
			kind:           MatchExactOffset,
			lineKnown:      true,
			columnKnown:    false,
			heuristicStage: "",
			want:           ConfidenceExactOffset - 2,
		},
		{
			name:           "function level",
			kind:           MatchFunction,
			lineKnown:      true,
			columnKnown:    false,
			heuristicStage: "",
			want:           ConfidenceFunction,
		},
		{
			name:           "line table",
			kind:           MatchLineTable,
			lineKnown:      true,
			columnKnown:    false,
			heuristicStage: "",
			want:           ConfidenceLineTable,
		},
		{
			name:           "heuristic symbol",
			kind:           MatchHeuristic,
			lineKnown:      false,
			columnKnown:    false,
			heuristicStage: "symbol_heuristic",
			want:           ConfidenceHeuristicSymbol,
		},
		{
			name:           "heuristic cargo",
			kind:           MatchHeuristic,
			lineKnown:      false,
			columnKnown:    false,
			heuristicStage: "cargo_manifest",
			want:           ConfidenceHeuristicCargo,
		},
		{
			name:           "unknown",
			kind:           MatchUnknown,
			lineKnown:      false,
			columnKnown:    false,
			heuristicStage: "",
			want:           ConfidenceUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ConfidenceForKind(tt.kind, tt.lineKnown, tt.columnKnown, tt.heuristicStage)
			if got != tt.want {
				t.Errorf("ConfidenceForKind() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestLinkPresentationForConfidence tests link presentation policy
func TestLinkPresentationForConfidence(t *testing.T) {
	tests := []struct {
		name     string
		score    int
		hasFile  bool
		want     LinkPresentation
	}{
		{
			name:    "high confidence with file",
			score:   ConfidenceExactOffset,
			hasFile: true,
			want:    LinkAuto,
		},
		{
			name:    "function level with file",
			score:   ConfidenceFunction,
			hasFile: true,
			want:    LinkAuto,
		},
		{
			name:    "line table with file",
			score:   ConfidenceLineTable,
			hasFile: true,
			want:    LinkCandidate,
		},
		{
			name:    "heuristic with file",
			score:   ConfidenceHeuristicSymbol,
			hasFile: true,
			want:    LinkCandidate,
		},
		{
			name:    "low heuristic with file",
			score:   ConfidenceHeuristicCargo,
			hasFile: true,
			want:    LinkCandidate,
		},
		{
			name:    "unknown with file",
			score:   ConfidenceUnknown,
			hasFile: true,
			want:    LinkNone,
		},
		{
			name:    "high confidence without file",
			score:   ConfidenceExactOffset,
			hasFile: false,
			want:    LinkNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LinkPresentationForConfidence(tt.score, tt.hasFile)
			if got != tt.want {
				t.Errorf("LinkPresentationForConfidence() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestFallbackResultUserSummary tests user-visible summary formatting
func TestFallbackResultUserSummary(t *testing.T) {
	tests := []struct {
		name  string
		result *FallbackResult
		want  string
	}{
		{
			name: "full DWARF result",
			result: &FallbackResult{
				File:       "token.rs",
				Line:       42,
				Column:     7,
				Confidence: ConfidenceExactOffset,
				MatchKind:  MatchExactOffset,
			},
			want: "token.rs:42 (confidence 100, exact_offset)",
		},
		{
			name: "function level result",
			result: &FallbackResult{
				File:       "pool.rs",
				Line:       88,
				Column:     0,
				Confidence: ConfidenceFunction,
				MatchKind:  MatchFunction,
			},
			want: "pool.rs:88 (confidence 72, function)",
		},
		{
			name: "heuristic result",
			result: &FallbackResult{
				File:       "auth.rs",
				Line:       0,
				Column:     0,
				Confidence: ConfidenceHeuristicSymbol,
				MatchKind:  MatchHeuristic,
			},
			want: "auth.rs (confidence 28, heuristic — line unknown)",
		},
		{
			name: "unknown result",
			result: &FallbackResult{
				File:       "",
				Line:       0,
				Column:     0,
				Confidence: ConfidenceUnknown,
				MatchKind:  MatchUnknown,
				Quality:    MappingQualityUnknown,
			},
			want: "unmapped (confidence 0)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.result.UserSummary()
			if got != tt.want {
				t.Errorf("FallbackResult.UserSummary() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestFallbackResultWithDetailedConfidence tests fallback result with detailed confidence
func TestFallbackResultWithDetailedConfidence(t *testing.T) {
	result := &FallbackResult{
		File:       "token.rs",
		Line:       42,
		Column:     7,
		Confidence: ConfidenceExactOffset,
		MatchKind:  MatchExactOffset,
		DetailedConfidence: &DetailedConfidence{
			Level:   "exact",
			Reason:  "dwarf_exact_offset",
			Context: "stage=full_dwarf,addr=0x1234",
		},
	}

	if result.DetailedConfidence == nil {
		t.Error("expected detailed confidence to be set")
	}
	if result.DetailedConfidence.Level != "exact" {
		t.Errorf("detailed confidence level = %v, want exact", result.DetailedConfidence.Level)
	}
	if result.DetailedConfidence.Reason != "dwarf_exact_offset" {
		t.Errorf("detailed confidence reason = %v, want dwarf_exact_offset", result.DetailedConfidence.Reason)
	}
}

// TestApplyConfidencePopulatesDetailedConfidence tests that applyConfidence populates detailed confidence
func TestApplyConfidencePopulatesDetailedConfidence(t *testing.T) {
	result := &FallbackResult{
		File:   "token.rs",
		Line:   42,
		Column: 7,
	}
	
	applyConfidence(result, 0x1234, MatchExactOffset, "full_dwarf", "")
	
	if result.DetailedConfidence == nil {
		t.Error("expected detailed confidence to be populated")
	}
	if result.DetailedConfidence.Level != "exact" {
		t.Errorf("detailed confidence level = %v, want exact", result.DetailedConfidence.Level)
	}
	if result.DetailedConfidence.Reason != "dwarf_exact_offset" {
		t.Errorf("detailed confidence reason = %v, want dwarf_exact_offset", result.DetailedConfidence.Reason)
	}
	if result.DetailedConfidence.Context == "" {
		t.Error("expected detailed confidence context to be set")
	}
}

// TestConfidenceFixtureExactOffset represents exact offset mapping fixture
func TestConfidenceFixtureExactOffset(t *testing.T) {
	result := &FallbackResult{
		File:       "src/token.rs",
		Line:       42,
		Column:     7,
		Confidence: ConfidenceExactOffset,
		MatchKind:  MatchExactOffset,
	}
	
	applyConfidence(result, 0x1000, MatchExactOffset, "full_dwarf", "")
	
	if result.DetailedConfidence.Level != "exact" {
		t.Errorf("fixture exact offset confidence level = %v, want exact", result.DetailedConfidence.Level)
	}
	if result.DetailedConfidence.Reason != "dwarf_exact_offset" {
		t.Errorf("fixture exact offset confidence reason = %v, want dwarf_exact_offset", result.DetailedConfidence.Reason)
	}
	
	// Verify auto-link presentation
	if result.LinkPresentation != LinkAuto {
		t.Errorf("fixture exact offset link presentation = %v, want auto", result.LinkPresentation)
	}
}

// TestConfidenceFixtureFunctionLevel represents function level mapping fixture
func TestConfidenceFixtureFunctionLevel(t *testing.T) {
	result := &FallbackResult{
		File:       "src/pool.rs",
		Line:       88,
		Column:     0,
		Confidence: ConfidenceFunction,
		MatchKind:  MatchFunction,
	}
	
	applyConfidence(result, 0x2000, MatchFunction, "full_dwarf", "")
	
	if result.DetailedConfidence.Level != "high" {
		t.Errorf("fixture function level confidence = %v, want high", result.DetailedConfidence.Level)
	}
	if result.DetailedConfidence.Reason != "dwarf_function_level" {
		t.Errorf("fixture function level confidence reason = %v, want dwarf_function_level", result.DetailedConfidence.Reason)
	}
}

// TestConfidenceFixtureLineTable represents partial DWARF line table fixture
func TestConfidenceFixtureLineTable(t *testing.T) {
	result := &FallbackResult{
		File:       "src/auth.rs",
		Line:       15,
		Column:     0,
		Confidence: ConfidenceLineTable,
		MatchKind:  MatchLineTable,
	}
	
	applyConfidence(result, 0x3000, MatchLineTable, "partial_dwarf", "")
	
	if result.DetailedConfidence.Level != "medium" {
		t.Errorf("fixture line table confidence = %v, want medium", result.DetailedConfidence.Level)
	}
	if result.DetailedConfidence.Reason != "dwarf_partial_line_table" {
		t.Errorf("fixture line table confidence reason = %v, want dwarf_partial_line_table", result.DetailedConfidence.Reason)
	}
}

// TestConfidenceFixtureHeuristic represents heuristic mapping fixture
func TestConfidenceFixtureHeuristic(t *testing.T) {
	result := &FallbackResult{
		File:       "src/utils.rs",
		Line:       0,
		Column:     0,
		Confidence: ConfidenceHeuristicSymbol,
		MatchKind:  MatchHeuristic,
	}
	
	applyConfidence(result, 0x4000, MatchHeuristic, "symbol_heuristic", "my_package")
	
	if result.DetailedConfidence.Level != "low" {
		t.Errorf("fixture heuristic confidence = %v, want low", result.DetailedConfidence.Level)
	}
	if result.DetailedConfidence.Reason != "heuristic_inference" {
		t.Errorf("fixture heuristic confidence reason = %v, want heuristic_inference", result.DetailedConfidence.Reason)
	}
}
