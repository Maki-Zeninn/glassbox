// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package simulator

// trap_cause.go — Structured trap cause preservation across the simulator boundary.
// Issue #533: Preserve trap causes across the simulator boundary
//
// Low-level traps (Rust host/VM errors) are mapped into a serializable enum
// with category, host error, WASM location, backtrace, and remediation hints.
// Unknown traps remain inspectable as opaque data without causing decode failure.

import (
	"encoding/json"
	"fmt"
)

// TrapCategory classifies the category of a trap for structured handling.
type TrapCategory string

const (
	TrapCategoryAuth          TrapCategory = "authorization"
	TrapCategoryBudget         TrapCategory = "budget_exhausted"
	TrapCategoryMemory         TrapCategory = "memory"
	TrapCategoryMissingEntry   TrapCategory = "missing_entry"
	TrapCategoryOverflow       TrapCategory = "integer_overflow"
	TrapCategoryDivisionByZero TrapCategory = "division_by_zero"
	TrapCategoryIndexOOB      TrapCategory = "index_out_of_bounds"
	TrapCategoryWasmTrap      TrapCategory = "wasm_trap"
	TrapCategoryHostError     TrapCategory = "host_error"
	TrapCategoryUnknown       TrapCategory = "unknown"
)

// TrapCauseUnreachable is the human-readable cause string for an unreachable
// WASM instruction trap. Extracting it as a named constant allows tests and
// callers to reference it directly without duplicating the literal string.
const TrapCauseUnreachable = "unreachable instruction"

// TrapCause is a structured, serializable representation of a trap that
// preserves information across the Rust → Go boundary.
type TrapCause struct {
	// Category classifies the trap for suggestion and source mapping.
	Category TrapCategory `json:"category"`

	// HostError is the original Rust host error message (may be opaque).
	HostError string `json:"host_error,omitempty"`

	// WasmFunction is the WASM function name where the trap occurred.
	WasmFunction string `json:"wasm_function,omitempty"`

	// WasmOffset is the WASM instruction offset (if available).
	WasmOffset *uint64 `json:"wasm_offset,omitempty"`

	// SourceLocation is the resolved source location (file:line).
	SourceFile string `json:"source_file,omitempty"`
	SourceLine int    `json:"source_line,omitempty"`

	// Backtrace is the call stack at the trap point (human-readable frames).
	Backtrace []string `json:"backtrace,omitempty"`

	// HasBacktrace indicates whether a backtrace was captured.
	HasBacktrace bool `json:"has_backtrace"`

	// RemediationHints are actionable suggestions for the developer.
	RemediationHints []string `json:"remediation_hints,omitempty"`

	// OpaqueData preserves unknown trap data as a raw JSON string so it
	// remains inspectable without causing decode failure.
	OpaqueData json.RawMessage `json:"opaque_data,omitempty"`
}

// ParseTrapCause decodes a raw trap payload (from the Rust simulator) into a
// structured TrapCause. Unknown fields are preserved in OpaqueData.
func ParseTrapCause(raw []byte) (*TrapCause, error) {
	if len(raw) == 0 {
		return &TrapCause{Category: TrapCategoryUnknown}, nil
	}

	var cause TrapCause
	if err := json.Unmarshal(raw, &cause); err != nil {
		// Preserve raw data as opaque if structured parsing fails
		return &TrapCause{
			Category:   TrapCategoryUnknown,
			OpaqueData: json.RawMessage(raw),
			HostError:  fmt.Sprintf("failed to parse trap cause: %v", err),
		}, nil
	}

	// Ensure unknown traps have a category
	if cause.Category == "" {
		cause.Category = TrapCategoryUnknown
	}

	return &cause, nil
}

// MapErrorToCategory infers a TrapCategory from an error message string.
// This is used when the simulator returns a flat error without structured data.
func MapErrorToCategory(errMsg string) TrapCategory {
	if errMsg == "" {
		return TrapCategoryUnknown
	}
	msg := []byte(errMsg)
	// Lowercase for matching
	lower := make([]byte, len(msg))
	for i, c := range msg {
		if c >= 'A' && c <= 'Z' {
			lower[i] = c + 32
		} else {
			lower[i] = c
		}
	}
	s := string(lower)

	switch {
	case contains(s, "auth"), contains(s, "require_auth"), contains(s, "unauthorized"):
		return TrapCategoryAuth
	case contains(s, "budget"), contains(s, "exceeded"), contains(s, "gas"), contains(s, "cpu"):
		return TrapCategoryBudget
	case contains(s, "memory"), contains(s, "oom"), contains(s, "out of memory"):
		return TrapCategoryMemory
	case contains(s, "missing"), contains(s, "not found"), contains(s, "no entry"):
		return TrapCategoryMissingEntry
	case contains(s, "overflow"):
		return TrapCategoryOverflow
	case contains(s, "division"), contains(s, "divide by zero"):
		return TrapCategoryDivisionByZero
	case contains(s, "index"), contains(s, "out of bounds"), contains(s, "bounds"):
		return TrapCategoryIndexOOB
	case contains(s, "wasm"), contains(s, TrapCauseUnreachable), contains(s, "unreachable"), contains(s, "trap"):
		return TrapCategoryWasmTrap
	case contains(s, "host"):
		return TrapCategoryHostError
	default:
		return TrapCategoryUnknown
	}
}

// GenerateRemediationHints produces actionable suggestions based on the trap category.
func (tc *TrapCause) GenerateRemediationHints() {
	tc.RemediationHints = tc.RemediationHints[:0]

	switch tc.Category {
	case TrapCategoryAuth:
		tc.RemediationHints = append(tc.RemediationHints,
			"Ensure require_auth() is called with the correct address",
			"Check that the invoking account has authorized this operation",
			"Verify the wallet connection and signature request flow")
	case TrapCategoryBudget:
		tc.RemediationHints = append(tc.RemediationHints,
			"Increase the CPU instruction budget for this transaction",
			"Optimize expensive loops or computations in the contract",
			"Consider batching operations to reduce per-transaction cost")
	case TrapCategoryMemory:
		tc.RemediationHints = append(tc.RemediationHints,
			"Reduce the size of data structures stored in contract memory",
			"Avoid creating large temporary buffers",
			"Check for memory leaks in recursive or iterative code")
	case TrapCategoryMissingEntry:
		tc.RemediationHints = append(tc.RemediationHints,
			"Verify the storage key being read exists",
			"Ensure initialization functions have been called",
			"Check for typos in contract ID or storage keys")
	case TrapCategoryOverflow:
		tc.RemediationHints = append(tc.RemediationHints,
			"Use checked arithmetic (checked_add, checked_mul) instead of wrapping ops",
			"Add bounds validation before arithmetic operations",
			"Consider using i128 for larger number ranges")
	case TrapCategoryDivisionByZero:
		tc.RemediationHints = append(tc.RemediationHints,
			"Add a zero check before division operations",
			"Use checked division functions")
	case TrapCategoryIndexOOB:
		tc.RemediationHints = append(tc.RemediationHints,
			"Validate array/vector indices before access",
			"Add bounds checking with .get() instead of direct indexing")
	default:
		tc.RemediationHints = append(tc.RemediationHints,
			"Review the contract source code at the trap location",
			"Enable debug logging for more detailed trap information")
	}
}

// ToSummary produces a short human-readable summary for text output.
func (tc *TrapCause) ToSummary() string {
	if tc == nil {
		return "unknown trap"
	}
	summary := fmt.Sprintf("trap [%s]", tc.Category)
	if tc.WasmFunction != "" {
		summary += fmt.Sprintf(" in %s", tc.WasmFunction)
	}
	if tc.SourceFile != "" {
		summary += fmt.Sprintf(" at %s:%d", tc.SourceFile, tc.SourceLine)
	}
	if tc.HostError != "" {
		summary += fmt.Sprintf(": %s", tc.HostError)
	}
	return summary
}

// MarshalJSON ensures TrapCause is serializable and includes opaque data.
func (tc TrapCause) MarshalJSON() ([]byte, error) {
	type Alias TrapCause
	return json.Marshal(struct {
		Alias
		Summary string `json:"summary"`
	}{
		Alias:   (Alias)(tc),
		Summary: tc.ToSummary(),
	})
}

// contains is a simple substring check (avoids importing strings to keep this
// file dependency-free for the hot path).
func contains(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
