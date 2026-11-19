// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"github.com/dotandev/glassbox/internal/dwarf"
)

// Confidence is an alias for the dwarf.Confidence type for use in trace structures.
// This allows trace components to use the same confidence system as DWARF parsing.
type Confidence = dwarf.Confidence

// ConfidenceLevel is an alias for the dwarf.ConfidenceLevel type.
type ConfidenceLevel = dwarf.ConfidenceLevel

// ReasonCode is an alias for the dwarf.ReasonCode type.
type ReasonCode = dwarf.ReasonCode

// Confidence constants from dwarf package
const (
	ConfidenceExact    = dwarf.ConfidenceExact
	ConfidenceHigh     = dwarf.ConfidenceHigh
	ConfidenceMedium   = dwarf.ConfidenceMedium
	ConfidenceLow      = dwarf.ConfidenceLow
	ConfidenceUnknown  = dwarf.ConfidenceUnknown
	
	ReasonDWARFExact         = dwarf.ReasonDWARFExact
	ReasonDWARFLineOnly      = dwarf.ReasonDWARFLineOnly
	ReasonInlineExpansion    = dwarf.ReasonInlineExpansion
	ReasonInstructionRange   = dwarf.ReasonInstructionRange
	ReasonHeuristicMatch     = dwarf.ReasonHeuristicMatch
	ReasonFallback           = dwarf.ReasonFallback
	ReasonMissingDebugInfo   = dwarf.ReasonMissingDebugInfo
	ReasonStrippedBinary     = dwarf.ReasonStrippedBinary
	ReasonPartialCoverage    = dwarf.ReasonPartialCoverage
	ReasonPathNormalization = dwarf.ReasonPathNormalization
	ReasonMultipleCandidates = dwarf.ReasonMultipleCandidates
)

// DefaultConfidence is an alias for the dwarf.DefaultConfidence function.
func DefaultConfidence(level ConfidenceLevel, reason ReasonCode) Confidence {
	return dwarf.DefaultConfidence(dwarf.ConfidenceLevel(level), dwarf.ReasonCode(reason))
}

// ConfidenceWithMessage is an alias for the dwarf.ConfidenceWithMessage function.
func ConfidenceWithMessage(level ConfidenceLevel, reason ReasonCode, context string) Confidence {
	return dwarf.ConfidenceWithMessage(dwarf.ConfidenceLevel(level), dwarf.ReasonCode(reason), context)
}
