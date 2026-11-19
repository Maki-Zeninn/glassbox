// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// filter_render.go — Text and JSON rendering of FilteredTrace results.
//
// Both renderers prepend an active-filter metadata header so the output is
// self-describing: a reader can see exactly which filters were applied, how
// many steps matched, and the original sequence IDs of matched steps.
//
// Design rules:
//   - Original Step IDs are preserved in output — filtering never renumbers
//     steps, so line references stay stable across filter invocations.
//   - Empty match sets are successful, deterministic results, not errors.
//   - The filter summary is always included even when zero steps matched.

package trace

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// ── Text rendering ────────────────────────────────────────────────────────────

// RenderFilteredText renders a FilteredTrace as plain text.
// It begins with an active-filter header, then prints each matched step in the
// same concise format used by GenerateTracePlainText, preserving the original
// step sequence IDs.
func RenderFilteredText(ft *FilteredTrace) (string, error) {
	if ft == nil {
		return "", fmt.Errorf("filtered trace is nil")
	}

	var buf strings.Builder
	meta := FilterMetadataFromTrace(ft)

	// Active-filter header — always present.
	buf.WriteString("Glassbox Filtered Trace\n")
	buf.WriteString("=======================\n\n")
	writeFilterHeader(&buf, meta)

	if len(ft.MatchedSteps) == 0 {
		buf.WriteString("\nNo steps matched the filter criteria.\n")
		buf.WriteString("This is a successful, deterministic result — not an error.\n")
		return buf.String(), nil
	}

	buf.WriteString("\nMatched steps:\n")
	buf.WriteString("--------------\n")

	tr := ft.OriginalTrace
	for _, idx := range ft.MatchedSteps {
		if idx < 0 || idx >= len(tr.States) {
			continue
		}
		s := &tr.States[idx]

		// Indent nested calls to preserve hierarchy context.
		indent := ""
		if _, hasParent := ft.ParentContext[idx]; hasParent {
			indent = "  "
		}

		fmt.Fprintf(&buf, "\n%sStep %d: %s\n", indent, s.Step, stepSummary(s))
		if s.ContractID != "" {
			fmt.Fprintf(&buf, "%s  Contract:  %s\n", indent, s.ContractID)
		}
		if s.Function != "" {
			fmt.Fprintf(&buf, "%s  Function:  %s\n", indent, s.Function)
		}
		if s.EventType != "" {
			fmt.Fprintf(&buf, "%s  Event:     %s\n", indent, s.EventType)
		}
		if s.SourceFile != "" {
			if s.SourceLine > 0 {
				fmt.Fprintf(&buf, "%s  Source:    %s:%d\n", indent, s.SourceFile, s.SourceLine)
			} else {
				fmt.Fprintf(&buf, "%s  Source:    %s\n", indent, s.SourceFile)
			}
		}
		if s.Error != "" {
			fmt.Fprintf(&buf, "%s  Error:     %s\n", indent, s.Error)
		}
		if len(s.Arguments) > 0 {
			fmt.Fprintf(&buf, "%s  Args:      %v\n", indent, s.Arguments)
		}
	}

	buf.WriteString("\n")
	return buf.String(), nil
}

// writeFilterHeader writes the active-filter summary block.
func writeFilterHeader(buf *strings.Builder, meta *FilterMetadata) {
	fmt.Fprintf(buf, "Filter summary:\n")
	fmt.Fprintf(buf, "  Matched:   %d / %d steps (%.1f%%)\n",
		meta.MatchedCount, meta.TotalSteps,
		meta.MatchRatio*100,
	)

	if meta.Expression == nil {
		fmt.Fprintf(buf, "  Filters:   (none — all steps included)\n")
		return
	}

	expr := meta.Expression
	activeFilters := filterSummaryLines(expr)
	if len(activeFilters) == 0 {
		fmt.Fprintf(buf, "  Filters:   (none — all steps included)\n")
	} else {
		fmt.Fprintf(buf, "  Filters:\n")
		for _, line := range activeFilters {
			fmt.Fprintf(buf, "    %s\n", line)
		}
	}
	if expr.Exclude {
		fmt.Fprintf(buf, "  Mode:      EXCLUDE (results are steps that do NOT match the above)\n")
	}
}

// filterSummaryLines returns a human-readable list of active filter criteria.
func filterSummaryLines(expr *FilterExpression) []string {
	if expr == nil {
		return nil
	}
	var lines []string
	if expr.ContractID != "" {
		lines = append(lines, fmt.Sprintf("contract_id   = %q (regex)", expr.ContractID))
	}
	if expr.Function != "" {
		lines = append(lines, fmt.Sprintf("function      = %q (regex)", expr.Function))
	}
	if expr.EventType != "" {
		lines = append(lines, fmt.Sprintf("event_type    = %q", expr.EventType))
	}
	if expr.Severity != "" {
		lines = append(lines, fmt.Sprintf("severity      = %q", expr.Severity))
	}
	if expr.SourceFile != "" {
		lines = append(lines, fmt.Sprintf("source_file   = %q (contains)", expr.SourceFile))
	}
	if expr.StepMin > 0 || expr.StepMax > 0 {
		lines = append(lines, fmt.Sprintf("step_range    = [%d, %d]", expr.StepMin, expr.StepMax))
	}
	if expr.LineMin > 0 || expr.LineMax > 0 {
		lines = append(lines, fmt.Sprintf("line_range    = [%d, %d]", expr.LineMin, expr.LineMax))
	}
	return lines
}

// stepSummary returns a short one-line description of a step for text output.
func stepSummary(s *ExecutionState) string {
	if s.Operation != "" {
		return s.Operation
	}
	if s.EventType != "" {
		return s.EventType
	}
	if s.ContractID != "" && s.Function != "" {
		return fmt.Sprintf("%s::%s", s.ContractID, s.Function)
	}
	if s.ContractID != "" {
		return s.ContractID
	}
	return fmt.Sprintf("step %d", s.Step)
}

// ── JSON rendering ────────────────────────────────────────────────────────────

// FilteredTraceJSON is the top-level JSON structure emitted by RenderFilteredJSON.
type FilteredTraceJSON struct {
	// FilterSummary describes which filters were active and how many steps matched.
	FilterSummary FilteredTraceSummaryJSON `json:"filter_summary"`
	// MatchedSteps contains the matched ExecutionStates with their original IDs.
	MatchedSteps []FilteredStepJSON `json:"matched_steps"`
}

// FilteredTraceSummaryJSON is the active-filter metadata block in JSON output.
type FilteredTraceSummaryJSON struct {
	TotalSteps    int                    `json:"total_steps"`
	MatchedCount  int                    `json:"matched_count"`
	MatchRatio    float64                `json:"match_ratio"`
	ActiveFilters map[string]interface{} `json:"active_filters"`
	ExcludeMode   bool                   `json:"exclude_mode"`
}

// FilteredStepJSON is one matched step in JSON output.
// OriginalStep is always the unmodified index from the source trace.
type FilteredStepJSON struct {
	// OriginalStep is the step index in the source trace — never renumbered.
	OriginalStep int    `json:"original_step"`
	HasParent    bool   `json:"has_parent,omitempty"`
	ParentStep   int    `json:"parent_step,omitempty"`
	State        *ExecutionState `json:"state"`
}

// RenderFilteredJSON renders a FilteredTrace as a JSON byte slice.
// The output always contains a filter_summary block regardless of match count.
func RenderFilteredJSON(ft *FilteredTrace) ([]byte, error) {
	if ft == nil {
		return nil, fmt.Errorf("filtered trace is nil")
	}

	meta := FilterMetadataFromTrace(ft)
	activeFilters := buildActiveFiltersMap(meta.Expression)

	excludeMode := false
	if meta.Expression != nil {
		excludeMode = meta.Expression.Exclude
	}

	summary := FilteredTraceSummaryJSON{
		TotalSteps:    meta.TotalSteps,
		MatchedCount:  meta.MatchedCount,
		MatchRatio:    meta.MatchRatio,
		ActiveFilters: activeFilters,
		ExcludeMode:   excludeMode,
	}

	tr := ft.OriginalTrace
	steps := make([]FilteredStepJSON, 0, len(ft.MatchedSteps))
	for _, idx := range ft.MatchedSteps {
		if idx < 0 || idx >= len(tr.States) {
			continue
		}
		state := &tr.States[idx]
		fj := FilteredStepJSON{
			OriginalStep: state.Step,
			State:        state,
		}
		if parentIdx, ok := ft.ParentContext[idx]; ok {
			fj.HasParent = true
			if parentIdx >= 0 && parentIdx < len(tr.States) {
				fj.ParentStep = tr.States[parentIdx].Step
			}
		}
		steps = append(steps, fj)
	}

	out := FilteredTraceJSON{
		FilterSummary: summary,
		MatchedSteps:  steps,
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		return nil, fmt.Errorf("failed to encode filtered trace JSON: %w", err)
	}
	return buf.Bytes(), nil
}

// buildActiveFiltersMap converts a FilterExpression to a map[string]interface{}
// containing only the criteria that are actually set. Used in JSON output.
func buildActiveFiltersMap(expr *FilterExpression) map[string]interface{} {
	m := make(map[string]interface{})
	if expr == nil {
		return m
	}
	if expr.ContractID != "" {
		m["contract_id"] = expr.ContractID
	}
	if expr.Function != "" {
		m["function"] = expr.Function
	}
	if expr.EventType != "" {
		m["event_type"] = expr.EventType
	}
	if expr.Severity != "" {
		m["severity"] = expr.Severity
	}
	if expr.SourceFile != "" {
		m["source_file"] = expr.SourceFile
	}
	if expr.StepMin > 0 {
		m["step_min"] = expr.StepMin
	}
	if expr.StepMax > 0 {
		m["step_max"] = expr.StepMax
	}
	if expr.LineMin > 0 {
		m["line_min"] = expr.LineMin
	}
	if expr.LineMax > 0 {
		m["line_max"] = expr.LineMax
	}
	return m
}
