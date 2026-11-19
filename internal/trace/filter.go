// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package trace

// filter.go — Composable trace filtering by contract, function, and severity.
// Issue #540: Add trace filtering by contract, function, and severity
//
// Provides a filter expression model that can be combined predictably,
// applied after validation, preserves parent context for matched descendants,
// and never mutates the source trace.

import (
	"fmt"
	"regexp"
	"strings"
)

// FilterExpression is a composable set of filter criteria for trace steps.
// Multiple criteria are AND-combined (all must match).
type FilterExpression struct {
	ContractID   string `json:"contract_id,omitempty"`
	Function     string `json:"function,omitempty"`
	EventType    string `json:"event_type,omitempty"`
	Severity     string `json:"severity,omitempty"` // "error", "warning", "info", "all"
	SourceFile   string `json:"source_file,omitempty"`
	StepMin      int    `json:"step_min,omitempty"` // 0 = no lower bound
	StepMax      int    `json:"step_max,omitempty"` // 0 = no upper bound
	// LineMin and LineMax filter by source line number (ExecutionState.SourceLine).
	// Both are inclusive. 0 means no bound on that side.
	// A step with SourceLine == 0 (line not recorded) never matches a non-zero
	// line range, so the filter has no false positives on steps without source info.
	LineMin int `json:"line_min,omitempty"` // 0 = no lower bound
	LineMax int `json:"line_max,omitempty"` // 0 = no upper bound
	// Exclude reverses the filter: a step matches when it does NOT match the
	// other criteria. Useful for "show everything except contract X".
	Exclude bool `json:"exclude,omitempty"`

	ContractIDRe *regexp.Regexp `json:"-"`
	FunctionRe   *regexp.Regexp `json:"-"`
}

// Filter severity levels. Named distinctly from AnnotationSeverity
// (reviewer_comments.go) since the two are unrelated concepts that happen to
// share level names — this one grades trace step filtering, that one grades
// reviewer comments.
const (
	FilterSeverityError   = "error"
	FilterSeverityWarning = "warning"
	FilterSeverityInfo    = "info"
	FilterSeverityAll     = "all"
)

// Validate checks that the filter expression is well-formed.
// Returns an error if regex patterns are invalid or severity is unrecognized.
func (f *FilterExpression) Validate() error {
	if f == nil {
		return nil
	}

	// Validate severity
	if f.Severity != "" && f.Severity != FilterSeverityError &&
		f.Severity != FilterSeverityWarning && f.Severity != FilterSeverityInfo && f.Severity != FilterSeverityAll {
		return fmt.Errorf("invalid severity %q: must be one of %s, %s, %s, %s, %s",
			f.Severity, FilterSeverityError, FilterSeverityWarning, FilterSeverityInfo, FilterSeverityAll, "")
	}

	// Validate event type
	if f.EventType != "" {
		validTypes := AllFilterableEventTypes()
		found := false
		for _, vt := range validTypes {
			if f.EventType == vt {
				found = true
				break
			}
		}
		if !found && f.EventType != EventTypeOther {
			return fmt.Errorf("invalid event_type %q: must be one of %v", f.EventType, validTypes)
		}
	}

	// Compile regex patterns
	if f.ContractID != "" {
		re, err := regexp.Compile(f.ContractID)
		if err != nil {
			return fmt.Errorf("invalid contract_id regex: %w", err)
		}
		f.ContractIDRe = re
	}
	if f.Function != "" {
		re, err := regexp.Compile(f.Function)
		if err != nil {
			return fmt.Errorf("invalid function regex: %w", err)
		}
		f.FunctionRe = re
	}

	// Validate step range
	if f.StepMin > 0 && f.StepMax > 0 && f.StepMin > f.StepMax {
		return fmt.Errorf("step_min (%d) cannot be greater than step_max (%d)", f.StepMin, f.StepMax)
	}

	// Validate line range
	if f.LineMin < 0 {
		return fmt.Errorf("line_min (%d) cannot be negative", f.LineMin)
	}
	if f.LineMax < 0 {
		return fmt.Errorf("line_max (%d) cannot be negative", f.LineMax)
	}
	if f.LineMin > 0 && f.LineMax > 0 && f.LineMin > f.LineMax {
		return fmt.Errorf("line_min (%d) cannot be greater than line_max (%d)", f.LineMin, f.LineMax)
	}

	return nil
}

// Matches checks if a single ExecutionState matches the filter.
// When Exclude is true the result is inverted: steps that would normally
// match are rejected, and steps that would normally be rejected are included.
func (f *FilterExpression) Matches(state *ExecutionState) bool {
	if f == nil {
		return true
	}
	if state == nil {
		return false
	}
	return applyExclude(f.matchesInclusive(state), f.Exclude)
}

// matchesInclusive evaluates all criteria without applying the Exclude flag.
func (f *FilterExpression) matchesInclusive(state *ExecutionState) bool {
	// Contract ID filter
	if f.ContractID != "" {
		if f.ContractIDRe != nil {
			if !f.ContractIDRe.MatchString(state.ContractID) {
				return false
			}
		} else if state.ContractID != f.ContractID {
			return false
		}
	}

	// Function filter
	if f.Function != "" {
		if f.FunctionRe != nil {
			if !f.FunctionRe.MatchString(state.Function) {
				return false
			}
		} else if state.Function != f.Function {
			return false
		}
	}

	// Event type filter
	if f.EventType != "" {
		if ClassifyEventType(state) != f.EventType {
			return false
		}
	}

	// Severity filter
	if f.Severity != "" && f.Severity != FilterSeverityAll {
		hasError := state.Error != ""
		switch f.Severity {
		case FilterSeverityError:
			if !hasError {
				return false
			}
		case FilterSeverityInfo:
			if hasError {
				return false
			}
		}
	}

	// Source file filter
	if f.SourceFile != "" {
		if !strings.Contains(state.SourceFile, f.SourceFile) {
			return false
		}
	}

	// Step range filter
	if f.StepMin > 0 && state.Step < f.StepMin {
		return false
	}
	if f.StepMax > 0 && state.Step > f.StepMax {
		return false
	}

	// Source line range filter.
	// A step with no recorded source line (SourceLine == 0) does not match a
	// non-zero line range, avoiding false positives on steps without DWARF info.
	if f.LineMin > 0 || f.LineMax > 0 {
		if state.SourceLine == 0 {
			return false
		}
		if f.LineMin > 0 && state.SourceLine < f.LineMin {
			return false
		}
		if f.LineMax > 0 && state.SourceLine > f.LineMax {
			return false
		}
	}

	return true
}

// applyExclude inverts matched when exclude is true.
func applyExclude(matched bool, exclude bool) bool {
	if exclude {
		return !matched
	}
	return matched
}

// And combines two filter expressions with logical AND.
// Fields from other override fields from f when both are set.
func (f *FilterExpression) And(other *FilterExpression) *FilterExpression {
	result := &FilterExpression{}
	if f != nil {
		result.ContractID = f.ContractID
		result.Function = f.Function
		result.EventType = f.EventType
		result.Severity = f.Severity
		result.SourceFile = f.SourceFile
		result.StepMin = f.StepMin
		result.StepMax = f.StepMax
		result.LineMin = f.LineMin
		result.LineMax = f.LineMax
		result.Exclude = f.Exclude
	}
	if other != nil {
		if other.ContractID != "" {
			result.ContractID = other.ContractID
		}
		if other.Function != "" {
			result.Function = other.Function
		}
		if other.EventType != "" {
			result.EventType = other.EventType
		}
		if other.Severity != "" {
			result.Severity = other.Severity
		}
		if other.SourceFile != "" {
			result.SourceFile = other.SourceFile
		}
		if other.StepMin > 0 {
			result.StepMin = other.StepMin
		}
		if other.StepMax > 0 {
			result.StepMax = other.StepMax
		}
		if other.LineMin > 0 {
			result.LineMin = other.LineMin
		}
		if other.LineMax > 0 {
			result.LineMax = other.LineMax
		}
		if other.Exclude {
			result.Exclude = true
		}
	}
	return result
}

// FilteredTrace holds a filtered view of a trace, preserving the original
// trace and recording which steps matched (with parent context).
type FilteredTrace struct {
	OriginalTrace *ExecutionTrace
	MatchedSteps  []int  // indices into OriginalTrace.States
	FilterExpr    *FilterExpression
	ParentContext map[int]int // matched step index → parent matched step index
}

// ApplyFilter returns a FilteredTrace containing only the steps that match the
// filter expression, preserving parent context for matched descendants.
// The original trace is never modified. Sequence IDs are preserved to maintain
// deterministic ordering across filtering operations.
func ApplyFilter(t *ExecutionTrace, expr *FilterExpression) (*FilteredTrace, error) {
	if t == nil {
		return nil, fmt.Errorf("trace is nil")
	}
	if expr != nil {
		if err := expr.Validate(); err != nil {
			return nil, fmt.Errorf("invalid filter expression: %w", err)
		}
	}

	ft := &FilteredTrace{
		OriginalTrace: t,
		FilterExpr:    expr,
		ParentContext: make(map[int]int),
	}

	// Track the call depth to preserve parent context
	depthStack := []int{} // stack of matched step indices by depth

	for i := range t.States {
		state := &t.States[i]
		matched := expr == nil || expr.Matches(state)

		if matched {
			ft.MatchedSteps = append(ft.MatchedSteps, i)

			// Preserve parent context: the nearest ancestor that also matched
			// is the parent. For now we use a simple depth-based approach.
			if len(depthStack) > 0 {
				ft.ParentContext[i] = depthStack[len(depthStack)-1]
			}
		}

		// Track depth changes (entering/leaving contract calls)
		et := ClassifyEventType(state)
		if et == EventTypeContractCall && matched {
			depthStack = append(depthStack, i)
		}
		// Pop when we see a return (heuristic: next non-call step at same depth)
		if len(depthStack) > 0 && i > depthStack[len(depthStack)-1] && et != EventTypeContractCall {
			depthStack = depthStack[:len(depthStack)-1]
		}
	}

	return ft, nil
}

// FilterMetadata holds metadata about a filter operation for exported reports.
type FilterMetadata struct {
	Expression    *FilterExpression `json:"expression"`
	MatchedCount  int               `json:"matched_count"`
	TotalSteps    int               `json:"total_steps"`
	MatchRatio    float64           `json:"match_ratio"`
}

// FilterMetadataFromTrace extracts filter metadata from a FilteredTrace.
func FilterMetadataFromTrace(ft *FilteredTrace) *FilterMetadata {
	total := 0
	if ft != nil && ft.OriginalTrace != nil {
		total = len(ft.OriginalTrace.States)
	}
	matched := 0
	if ft != nil {
		matched = len(ft.MatchedSteps)
	}
	ratio := 0.0
	if total > 0 {
		ratio = float64(matched) / float64(total)
	}
	return &FilterMetadata{
		Expression:   ft.FilterExpr,
		MatchedCount: matched,
		TotalSteps:   total,
		MatchRatio:   ratio,
	}
}
