// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package trace

import "time"

// AnalysisBudget defines the resource limits that bound the Go-side analysis
// pipeline introduced by Issue #838.  Every field is optional (zero-value means
// unlimited) so callers can enable only the budgets they care about.
//
// The pipeline checks context cancellation AND these budgets; whichever fires
// first wins.  Partial results are reported via AnalysisResult.Incomplete.
type AnalysisBudget struct {
	// Timeout is the maximum wall-clock duration for the entire Go-side
	// analysis pipeline (depth analysis + cost annotation + parser).
	// Zero means no wall-clock limit.
	Timeout time.Duration

	// MaxNodes is the maximum number of TraceNode tree nodes the depth
	// analyzer and cost annotator are allowed to visit in total.
	// Zero means unlimited.
	MaxNodes int

	// MaxDepth is the maximum recursion depth the depth analyzer and cost
	// annotator will descend into.  Nodes beyond this depth are skipped.
	// Zero means unlimited.
	MaxDepth int

	// MaxInputBytes caps the total size of diagnostic-event data (summed
	// EventData string lengths) that ParseSimulationResponse will process
	// before stopping.  Zero means unlimited.
	MaxInputBytes int64
}

// DefaultAnalysisBudget returns a conservative default budget suitable for CI
// and interactive use.  All limits are generous enough to handle real-world
// Soroban traces while protecting against adversarial or degenerate inputs.
func DefaultAnalysisBudget() AnalysisBudget {
	return AnalysisBudget{
		Timeout:       30 * time.Second,
		MaxNodes:      50_000,
		MaxDepth:      500,
		MaxInputBytes: 32 * 1024 * 1024, // 32 MB
	}
}

// UnlimitedAnalysisBudget returns a budget with all limits disabled.  Use only
// in controlled environments where trace inputs are fully trusted.
func UnlimitedAnalysisBudget() AnalysisBudget {
	return AnalysisBudget{}
}

// TruncationReason identifies which budget triggered early termination.
type TruncationReason string

const (
	TruncationReasonNone    TruncationReason = ""
	TruncationReasonTimeout TruncationReason = "timeout"
	TruncationReasonNodes   TruncationReason = "max_nodes"
	TruncationReasonDepth   TruncationReason = "max_depth"
	TruncationReasonBytes   TruncationReason = "max_bytes"
	TruncationReasonCancel  TruncationReason = "context_cancelled"
)

// AnalysisPhase names the pipeline stage that was truncated.
type AnalysisPhase string

const (
	AnalysisPhaseDepth      AnalysisPhase = "depth_analysis"
	AnalysisPhaseAnnotation AnalysisPhase = "cost_annotation"
	AnalysisPhaseParser     AnalysisPhase = "parser"
	AnalysisPhaseSecurity   AnalysisPhase = "security_scan"
	AnalysisPhaseSourceScan AnalysisPhase = "source_scan"
)

// AnalysisResult bundles findings from a single analysis phase together with
// incompleteness metadata.  When Incomplete is true callers MUST NOT present
// the results as a complete analysis; they should surface TruncatedAt and
// TruncationReason to the user.
//
// Findings generated before truncation are always trustworthy.  Findings that
// required visiting nodes or bytes beyond the budget limit are absent.
type AnalysisResult struct {
	// Phase identifies which analysis phase produced this result.
	Phase AnalysisPhase `json:"phase"`

	// Incomplete is true when the phase was halted before visiting the full
	// input.  The caller must flag partial output clearly.
	Incomplete bool `json:"incomplete,omitempty"`

	// TruncatedAt names the budget type that triggered early termination.
	TruncatedAt TruncationReason `json:"truncated_at,omitempty"`

	// NodesSeen is the number of TraceNode tree nodes visited before
	// truncation (0 if not applicable to this phase).
	NodesSeen int `json:"nodes_seen,omitempty"`

	// NodesLimit is the MaxNodes budget that was active (0 if unlimited).
	NodesLimit int `json:"nodes_limit,omitempty"`

	// BytesSeen is the number of input bytes processed before truncation
	// (0 if not applicable to this phase).
	BytesSeen int64 `json:"bytes_seen,omitempty"`

	// BytesLimit is the MaxInputBytes budget that was active (0 if unlimited).
	BytesLimit int64 `json:"bytes_limit,omitempty"`

	// DepthReached is the maximum recursion depth reached before truncation.
	DepthReached int `json:"depth_reached,omitempty"`

	// DepthLimit is the MaxDepth budget that was active (0 if unlimited).
	DepthLimit int `json:"depth_limit,omitempty"`

	// DepthAnalysis is populated when Phase == AnalysisPhaseDepth.
	DepthAnalysis *DepthAnalysis `json:"depth_analysis,omitempty"`
}

// newIncompleteResult constructs an AnalysisResult marked as truncated.
func newIncompleteResult(phase AnalysisPhase, reason TruncationReason) AnalysisResult {
	return AnalysisResult{
		Phase:       phase,
		Incomplete:  true,
		TruncatedAt: reason,
	}
}

// IsContextError returns true if the truncation was caused by context
// cancellation or deadline exceeded (as opposed to an explicit budget limit).
func (r *AnalysisResult) IsContextError() bool {
	return r.TruncatedAt == TruncationReasonCancel || r.TruncatedAt == TruncationReasonTimeout
}
