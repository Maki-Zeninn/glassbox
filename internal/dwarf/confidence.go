// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package dwarf

// ConfidenceLevel represents the confidence level of a source mapping.
// Higher confidence indicates more reliable source location information.
type ConfidenceLevel int

const (
	// ConfidenceExact indicates the source mapping is known to be precise.
	ConfidenceExact ConfidenceLevel = iota

	// ConfidenceHigh indicates the source mapping is very reliable but may
	// have minor limitations (e.g., line known but column unknown, or from
	// inline function expansion).
	ConfidenceHigh

	// ConfidenceMedium indicates the source mapping is approximate.
	// The location is likely correct but may be off by a few lines or
	// derived from heuristic matching.
	ConfidenceMedium

	// ConfidenceLow indicates the source mapping is inferred or estimated.
	// The location should be treated as a best guess and may be significantly
	// inaccurate. Users should verify before relying on it.
	ConfidenceLow

	// ConfidenceUnknown indicates no confidence information is available.
	ConfidenceUnknown
)

// String returns a human-readable representation of the confidence level.
func (c ConfidenceLevel) String() string {
	switch c {
	case ConfidenceExact:
		return "exact"
	case ConfidenceHigh:
		return "high"
	case ConfidenceMedium:
		return "medium"
	case ConfidenceLow:
		return "low"
	case ConfidenceUnknown:
		return "unknown"
	default:
		return "invalid"
	}
}

// ReasonCode explains why a particular confidence level was assigned.
type ReasonCode string

const (
	// ReasonDWARFExact indicates exact DWARF debug information with full
	// line and column precision.
	ReasonDWARFExact ReasonCode = "dwarf_exact"

	// ReasonDWARFLineOnly indicates DWARF info with line but no column data.
	ReasonDWARFLineOnly ReasonCode = "dwarf_line_only"

	// ReasonInlineExpansion indicates the location comes from inlined function
	// expansion, which may have slight imprecision.
	ReasonInlineExpansion ReasonCode = "inline_expansion"

	// ReasonInstructionRange indicates the location is estimated from an
	// instruction address range rather than exact debug info.
	ReasonInstructionRange ReasonCode = "instruction_range"

	// ReasonHeuristicMatch indicates the location was determined through
	// heuristic pattern matching rather than debug symbols.
	ReasonHeuristicMatch ReasonCode = "heuristic_match"

	// ReasonFallback indicates this is a fallback location when no better
	// information is available.
	ReasonFallback ReasonCode = "fallback"

	// ReasonMissingDebugInfo indicates no DWARF debug info was available.
	ReasonMissingDebugInfo ReasonCode = "missing_debug_info"

	// ReasonStrippedBinary indicates the binary was stripped of debug symbols.
	ReasonStrippedBinary ReasonCode = "stripped_binary"

	// ReasonPartialCoverage indicates only partial debug coverage is available.
	ReasonPartialCoverage ReasonCode = "partial_coverage"

	// ReasonPathNormalization indicates uncertainty from path normalization
	// or remapping between build and local environments.
	ReasonPathNormalization ReasonCode = "path_normalization"

	// ReasonMultipleCandidates indicates multiple possible source locations
	// were found, and one was chosen heuristically.
	ReasonMultipleCandidates ReasonCode = "multiple_candidates"
)

// Confidence combines a confidence level with an explanatory reason code.
type Confidence struct {
	Level  ConfidenceLevel `json:"level"`
	Reason ReasonCode      `json:"reason"`
	// Optional additional context about why this confidence was assigned
	Context string `json:"context,omitempty"`
}

// DefaultConfidence returns a confidence with the given level and reason.
func DefaultConfidence(level ConfidenceLevel, reason ReasonCode) Confidence {
	return Confidence{
		Level:  level,
		Reason: reason,
	}
}

// ConfidenceWithMessage returns a confidence with the given level, reason, and context.
func ConfidenceWithMessage(level ConfidenceLevel, reason ReasonCode, context string) Confidence {
	return Confidence{
		Level:   level,
		Reason:  reason,
		Context: context,
	}
}

// IsHighConfidence returns true if the confidence level is Exact or High.
func (c Confidence) IsHighConfidence() bool {
	return c.Level == ConfidenceExact || c.Level == ConfidenceHigh
}

// IsLowConfidence returns true if the confidence level is Low or Unknown.
func (c Confidence) IsLowConfidence() bool {
	return c.Level == ConfidenceLow || c.Level == ConfidenceUnknown
}

// confidenceReasonDescriptions provides human-readable descriptions for reason codes.
var confidenceReasonDescriptions = map[ReasonCode]string{
	ReasonDWARFExact:         "Exact location from DWARF debug symbols with line and column precision",
	ReasonDWARFLineOnly:      "Line information from DWARF, but column data unavailable",
	ReasonInlineExpansion:    "Location from inlined function expansion, may have minor imprecision",
	ReasonInstructionRange:   "Estimated from instruction address range rather than exact debug info",
	ReasonHeuristicMatch:     "Determined through heuristic pattern matching",
	ReasonFallback:           "Fallback location when no better information available",
	ReasonMissingDebugInfo:   "No DWARF debug information available for this location",
	ReasonStrippedBinary:     "Binary was stripped of debug symbols",
	ReasonPartialCoverage:    "Only partial debug coverage available in this region",
	ReasonPathNormalization: "Uncertainty from path normalization or remapping",
	ReasonMultipleCandidates: "Multiple possible locations found, chosen heuristically",
}

// Description returns a human-readable description of the confidence reason.
func (c Confidence) Description() string {
	if desc, ok := confidenceReasonDescriptions[c.Reason]; ok {
		return desc
	}
	return "Unknown confidence reason"
}

// ConfidenceLevelFromDWARFQuality determines confidence based on DWARF data quality.
func ConfidenceLevelFromDWARFQuality(hasLineInfo, hasColumnInfo, hasInlineInfo bool) Confidence {
	if hasLineInfo && hasColumnInfo {
		return DefaultConfidence(ConfidenceExact, ReasonDWARFExact)
	}
	if hasLineInfo {
		return DefaultConfidence(ConfidenceHigh, ReasonDWARFLineOnly)
	}
	if hasInlineInfo {
		return DefaultConfidence(ConfidenceMedium, ReasonInlineExpansion)
	}
	return DefaultConfidence(ConfidenceLow, ReasonMissingDebugInfo)
}
