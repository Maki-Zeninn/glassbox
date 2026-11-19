// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// note_export.go — Rendering analyst notes in trace export formats.
//
// Notes travel in TraceAnnotations.Notes and are rendered alongside the steps
// they are anchored to in HTML, Markdown, plain text, and JSON exports.
//
// Invariant: notes are always rendered from a pre-resolved snapshot so that
// export code never calls into trace data; the exported artifact is
// self-contained. This also means re-exporting the same trace twice produces
// identical bytes regardless of whether the execution data changed.
//
// Notes do NOT affect execution hashes. The trace Fingerprint() and the
// ExportJSON envelope's transaction_hash are derived from execution fields
// only; the Notes slice is excluded from both.

package trace

import (
	"fmt"
	"strings"
	"time"
)

// ExportedNote is the render-ready projection of an AnalystNote used by
// every export format. It carries pre-resolved step context and formatted
// timestamps so templates and text renderers stay declarative.
type ExportedNote struct {
	ID         string
	Target     string // pre-rendered target string (e.g. "step-3-a9f2b8c1" or "token.rs:55")
	Body       string
	Tags       []string
	CreatedAt  string
	UpdatedAt  string
	StepIndex  int    // -1 for trace-wide notes; set by resolution
	IsDangling bool
	DanglingReason string
}

// NoteExportSet is the collection of notes ready for a single export run.
// Notes are grouped for the rendering layer: ByStep maps state-index → notes,
// TraceWide holds notes targeting the whole trace, and Dangling holds notes
// whose targets no longer resolve.
type NoteExportSet struct {
	ByStep    map[int][]ExportedNote
	TraceWide []ExportedNote
	Dangling  []ExportedNote
}

// BuildNoteExportSet resolves the trace's analyst notes and groups them for
// rendering. This is the only place in the export pipeline where notes are
// resolved; every format receives a pre-built NoteExportSet.
func BuildNoteExportSet(t *ExecutionTrace) *NoteExportSet {
	set := &NoteExportSet{
		ByStep: make(map[int][]ExportedNote),
	}
	if t == nil || len(t.Annotations.Notes) == 0 {
		return set
	}

	resolutions := ResolveNotes(t, t.Annotations.Notes)
	for _, r := range resolutions {
		en := renderNote(r)
		switch r.Status {
		case NoteTraceWide:
			set.TraceWide = append(set.TraceWide, en)
		case NoteResolved:
			set.ByStep[r.StepIndex] = append(set.ByStep[r.StepIndex], en)
		case NoteDangling:
			set.Dangling = append(set.Dangling, en)
		}
	}
	return set
}

// renderNote converts a ResolvedNote into an ExportedNote.
func renderNote(r ResolvedNote) ExportedNote {
	en := ExportedNote{
		ID:         r.Note.ID,
		Target:     r.Note.Target.String(),
		Body:       r.Note.Body,
		Tags:       r.Note.Tags,
		StepIndex:  r.StepIndex,
		CreatedAt:  r.Note.CreatedAt.UTC().Format(time.RFC3339),
		IsDangling: r.Status == NoteDangling,
		DanglingReason: r.Reason,
	}
	if !r.Note.UpdatedAt.IsZero() {
		en.UpdatedAt = r.Note.UpdatedAt.UTC().Format(time.RFC3339)
	}
	return en
}

// ── Text rendering ────────────────────────────────────────────────────────────

// RenderNotesText renders the NoteExportSet as a plain-text block suitable
// for appending to a trace text export.
func RenderNotesText(set *NoteExportSet) string {
	if set == nil {
		return ""
	}
	total := len(set.TraceWide) + len(set.Dangling)
	for _, notes := range set.ByStep {
		total += len(notes)
	}
	if total == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("Analyst notes:\n")
	b.WriteString("--------------\n")

	if len(set.TraceWide) > 0 {
		b.WriteString("\nTrace-wide notes:\n")
		for _, n := range set.TraceWide {
			writeNoteText(&b, "  ", n)
		}
	}

	if len(set.Dangling) > 0 {
		b.WriteString("\nNotes with unresolved targets:\n")
		for _, n := range set.Dangling {
			writeNoteText(&b, "  ", n)
			fmt.Fprintf(&b, "  > Target not found: %s\n", n.DanglingReason)
		}
	}

	return b.String()
}

func writeNoteText(b *strings.Builder, indent string, n ExportedNote) {
	fmt.Fprintf(b, "%s[%s] on %s (%s)\n", indent, n.ID, n.Target, n.CreatedAt)
	if len(n.Tags) > 0 {
		fmt.Fprintf(b, "%s  Tags: %s\n", indent, strings.Join(n.Tags, ", "))
	}
	if n.Body != "" {
		for _, line := range strings.Split(n.Body, "\n") {
			fmt.Fprintf(b, "%s  %s\n", indent, line)
		}
	}
}

// RenderNotesForStep returns the inline text for notes anchored to a specific
// step, for embedding inside a per-step block in text/markdown exports.
func RenderNotesForStep(set *NoteExportSet, stepIndex int) string {
	if set == nil {
		return ""
	}
	notes := set.ByStep[stepIndex]
	if len(notes) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("  Analyst notes:\n")
	for _, n := range notes {
		writeNoteText(&b, "    ", n)
	}
	return b.String()
}
