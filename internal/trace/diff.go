// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package trace

// diff.go — Trace diff visualization (Issue #541: Add trace diff visualization)
//
// Produces a step-aligned diff with inserted, removed, changed, and divergence
// markers, including gas and state differences where available.
// Includes text, JSON, and HTML renderers.

import (
	"encoding/json"
	"fmt"
	"html/template"
	"strings"
	"time"
)

// DiffChangeKind classifies what changed between two aligned trace steps.
type DiffChangeKind string

const (
	DiffEqual    DiffChangeKind = "equal"     // Steps match
	DiffInsert   DiffChangeKind = "inserted"  // Step exists only in the new trace
	DiffRemove   DiffChangeKind = "removed"   // Step exists only in the old trace
	DiffChanged  DiffChangeKind = "changed"   // Step exists in both but differs
	DiffDiverge  DiffChangeKind = "divergent" // First meaningful divergence point
)

// DiffEntry represents one aligned pair of trace steps in the diff output.
type DiffEntry struct {
	Kind        DiffChangeKind `json:"kind"`
	OldStep      int           `json:"old_step,omitempty"`
	NewStep      int           `json:"new_step,omitempty"`
	OldSummary   string        `json:"old_summary,omitempty"`
	NewSummary   string        `json:"new_summary,omitempty"`
	Changes      []DiffField   `json:"changes,omitempty"`
}

// DiffField describes a single field-level difference.
type DiffField struct {
	Field     string `json:"field"`
	OldValue  string `json:"old_value"`
	NewValue  string `json:"new_value"`
}

// TraceDiff holds the complete diff between two execution traces.
type TraceDiff struct {
	OldTxHash     string     `json:"old_tx_hash"`
	NewTxHash     string     `json:"new_tx_hash"`
	GeneratedAt   time.Time  `json:"generated_at"`
	Entries       []DiffEntry `json:"entries"`
	OldStepCount  int        `json:"old_step_count"`
	NewStepCount  int        `json:"new_step_count"`
	DivergenceIdx int        `json:"divergence_index"` // -1 if no divergence
	IsEmpty       bool       `json:"is_empty"`         // true if traces are equivalent
}

// stepKey produces a stable alignment key for a step, preferring
// sequence ID for deterministic ordering, falling back to
// contract ID + function + operation for backward compatibility.
func stepKey(s *ExecutionState) string {
	if s == nil {
		return ""
	}
	// Prefer sequence ID for deterministic ordering
	if s.SequenceID > 0 {
		return fmt.Sprintf("seq:%d", s.SequenceID)
	}
	// Fallback to contract ID + function + operation
	parts := []string{s.ContractID, s.Function, s.Operation}
	return strings.Join(parts, "|")
}

// stepSummary produces a short human-readable summary for diff display.
func stepSummary(s *ExecutionState) string {
	if s == nil {
		return ""
	}
	parts := []string{}
	if s.ContractID != "" {
		parts = append(parts, s.ContractID[:min(12, len(s.ContractID))])
	}
	if s.Function != "" {
		parts = append(parts, s.Function)
	}
	if s.Error != "" {
		parts = append(parts, "ERR:"+s.Error)
	}
	if len(parts) == 0 {
		parts = append(parts, s.Operation)
	}
	return strings.Join(parts, " / ")
}

// compareFields returns field-level differences between two states.
func compareFields(old, new *ExecutionState) []DiffField {
	var diffs []DiffField
	if old == nil || new == nil {
		return diffs
	}
	if old.Operation != new.Operation {
		diffs = append(diffs, DiffField{"operation", old.Operation, new.Operation})
	}
	if old.ContractID != new.ContractID {
		diffs = append(diffs, DiffField{"contract_id", old.ContractID, new.ContractID})
	}
	if old.Function != new.Function {
		diffs = append(diffs, DiffField{"function", old.Function, new.Function})
	}
	if old.Error != new.Error {
		diffs = append(diffs, DiffField{"error", old.Error, new.Error})
	}
	if old.SourceFile != new.SourceFile || old.SourceLine != new.SourceLine {
		diffs = append(diffs, DiffField{
			"source",
			fmt.Sprintf("%s:%d", old.SourceFile, old.SourceLine),
			fmt.Sprintf("%s:%d", new.SourceFile, new.SourceLine),
		})
	}
	if old.Cost != nil && new.Cost != nil {
		if old.Cost.CPU != new.Cost.CPU {
			diffs = append(diffs, DiffField{"gas_cpu",
				fmt.Sprintf("%d", old.Cost.CPU),
				fmt.Sprintf("%d", new.Cost.CPU)})
		}
		if old.Cost.MemoryBytes != new.Cost.MemoryBytes {
			diffs = append(diffs, DiffField{"gas_mem",
				fmt.Sprintf("%d", old.Cost.MemoryBytes),
				fmt.Sprintf("%d", new.Cost.MemoryBytes)})
		}
	}
	return diffs
}

// ComputeTraceDiff aligns two traces by step key and produces a TraceDiff.
// Uses a greedy alignment that prefers stable contract+function identifiers,
// falling back to positional matching when keys are ambiguous.
func ComputeTraceDiff(old, new *ExecutionTrace) *TraceDiff {
	diff := &TraceDiff{
		GeneratedAt:   time.Now().UTC(),
		DivergenceIdx: -1,
	}

	if old != nil {
		diff.OldTxHash = old.TransactionHash
		diff.OldStepCount = len(old.States)
	}
	if new != nil {
		diff.NewTxHash = new.TransactionHash
		diff.NewStepCount = len(new.States)
	}

	if old == nil || new == nil {
		diff.Entries = nil
		diff.IsEmpty = false
		return diff
	}

	// Build alignment using LCS-like approach with step keys
	oldStates := old.States
	newStates := new.States

	// Simple greedy alignment: match by key, fall back to position
	type alignPair struct {
		oldIdx, newIdx int
		matched        bool
	}

	matched := make([]bool, len(newStates))
	var entries []DiffEntry

	for oi, os := range oldStates {
		oldKey := stepKey(&os)
		// Find best match in new trace
		found := -1
		for ni := 0; ni < len(newStates); ni++ {
			if matched[ni] {
				continue
			}
			if stepKey(&newStates[ni]) == oldKey && oldKey != "||" {
				found = ni
				break
			}
		}

		if found >= 0 {
			matched[found] = true
			fieldDiffs := compareFields(&os, &newStates[found])
			kind := DiffEqual
			if len(fieldDiffs) > 0 {
				kind = DiffChanged
			}
			entries = append(entries, DiffEntry{
				Kind:      kind,
				OldStep:   oi,
				NewStep:   found,
				OldSummary: stepSummary(&os),
				NewSummary: stepSummary(&newStates[found]),
				Changes:   fieldDiffs,
			})
		} else {
			entries = append(entries, DiffEntry{
				Kind:      DiffRemove,
				OldStep:   oi,
				OldSummary: stepSummary(&os),
			})
		}
	}

	// Add unmatched new steps as insertions
	for ni, ns := range newStates {
		if !matched[ni] {
			entries = append(entries, DiffEntry{
				Kind:      DiffInsert,
				NewStep:   ni,
				NewSummary: stepSummary(&ns),
			})
		}
	}

	diff.Entries = entries
	diff.IsEmpty = true

	// Find first divergence and determine if diff is empty
	for i, e := range entries {
		if e.Kind != DiffEqual {
			diff.IsEmpty = false
			if diff.DivergenceIdx < 0 {
				diff.DivergenceIdx = i
			}
		}
	}

	return diff
}

// RenderDiffText produces a human-readable text diff.
func (d *TraceDiff) RenderText() string {
	if d == nil || d.IsEmpty {
		return "No differences found — traces are equivalent.\n"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Trace Diff: %s → %s\n", d.OldTxHash[:min(12, len(d.OldTxHash))], d.NewTxHash[:min(12, len(d.NewTxHash))]))
	sb.WriteString(fmt.Sprintf("Old: %d steps, New: %d steps\n", d.OldStepCount, d.NewStepCount))
	sb.WriteString(fmt.Sprintf("First divergence at entry #%d\n\n", d.DivergenceIdx))

	for i, e := range d.Entries {
		marker := " "
		switch e.Kind {
		case DiffInsert:
			marker = "+"
		case DiffRemove:
			marker = "-"
		case DiffChanged:
			marker = "~"
		case DiffDiverge:
			marker = "!"
		}

		if e.Kind == DiffEqual {
			continue // Skip equivalent steps for compactness
		}

		sb.WriteString(fmt.Sprintf("%s [%d] %s", marker, i, e.Kind))
		if e.OldSummary != "" {
			sb.WriteString(fmt.Sprintf("  old: %s", e.OldSummary))
		}
		if e.NewSummary != "" {
			sb.WriteString(fmt.Sprintf("  new: %s", e.NewSummary))
		}
		sb.WriteString("\n")
		for _, c := range e.Changes {
			sb.WriteString(fmt.Sprintf("    %s: %s → %s\n", c.Field, c.OldValue, c.NewValue))
		}
	}

	if d.DivergenceIdx >= 0 {
		sb.WriteString(fmt.Sprintf("\n⚠ First meaningful divergence at entry #%d\n", d.DivergenceIdx))
	}

	return sb.String()
}

// RenderDiffJSON produces a JSON representation of the diff.
func (d *TraceDiff) RenderJSON() ([]byte, error) {
	return json.MarshalIndent(d, "", "  ")
}

// diffHTMLTemplate is the HTML template for diff visualization.
const diffHTMLTemplate = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Glassbox Trace Diff</title>
  <style>
    body { font-family: system-ui, sans-serif; margin: 0; padding: 1rem; background: #0b1220; color: #e8eef9; }
    .header { border-bottom: 1px solid #334155; padding-bottom: 1rem; margin-bottom: 1rem; }
    .header h1 { margin: 0 0 .25rem; font-size: 1.6rem; }
    .header p { margin: .25rem 0; color: #94a3b8; }
    .diff-entry { margin-bottom: .5rem; padding: .5rem .75rem; border-radius: .375rem; font-family: monospace; font-size: .875rem; }
    .diff-equal { background: #1e293b; color: #94a3b8; }
    .diff-inserted { background: #052e16; color: #4ade80; border-left: 3px solid #22c55e; }
    .diff-removed { background: #450a0a; color: #f87171; border-left: 3px solid #ef4444; }
    .diff-changed { background: #422006; color: #fbbf24; border-left: 3px solid #f59e0b; }
    .diff-divergent { background: #422006; color: #fbbf24; border-left: 3px solid #f59e0b; font-weight: bold; }
    .field-change { margin-left: 1.5rem; font-size: .8rem; color: #cbd5e1; }
    .marker { display: inline-block; width: 1.5rem; font-weight: bold; }
  </style>
</head>
<body>
  <div class="header">
    <h1>Glassbox Trace Diff</h1>
    <p>Old: {{ .OldTxHash }} ({{ .OldStepCount }} steps) → New: {{ .NewTxHash }} ({{ .NewStepCount }} steps)</p>
    {{ if .IsEmpty }}<p style="color:#4ade80;">✓ No differences — traces are equivalent</p>
    {{ else }}<p style="color:#fbbf24;">⚠ First divergence at entry #{{ .DivergenceIdx }}</p>{{ end }}
  </div>
  {{ range .Entries }}
  {{ if eq .Kind "equal" }}{{ else }}
  <div class="diff-entry diff-{{ .Kind }}">
    <span class="marker">{{ if eq .Kind "inserted" }}+{{ else if eq .Kind "removed" }}-{{ else if eq .Kind "changed" }}~{{ else }}!{{ end }}</span>
    #{{ .OldStep }}{{ if .NewStep }} → #{{ .NewStep }}{{ end }}
    {{ if .OldSummary }} <span style="color:#94a3b8;">old: {{ .OldSummary }}</span>{{ end }}
    {{ if .NewSummary }} <span style="color:#60a5fa;">new: {{ .NewSummary }}</span>{{ end }}
    {{ range .Changes }}
    <div class="field-change">{{ .Field }}: <span style="color:#f87171;">{{ .OldValue }}</span> → <span style="color:#4ade80;">{{ .NewValue }}</span></div>
    {{ end }}
  </div>
  {{ end }}
  {{ end }}
</body>
</html>`

// RenderDiffHTML produces an HTML visualization of the diff.
func (d *TraceDiff) RenderHTML() (string, error) {
	tmpl, err := template.New("diff").Parse(diffHTMLTemplate)
	if err != nil {
		return "", fmt.Errorf("parse diff template: %w", err)
	}
	var sb strings.Builder
	if err := tmpl.Execute(&sb, d); err != nil {
		return "", fmt.Errorf("execute diff template: %w", err)
	}
	return sb.String(), nil
}
