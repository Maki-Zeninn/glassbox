// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"fmt"
	"strings"

	"github.com/dotandev/glassbox/internal/simulator"
)

// Verbosity controls how much detail appears in trace output.
type Verbosity int

const (
	// VerbositySummary shows only high-level step summaries.
	VerbositySummary Verbosity = iota
	// VerbosityNormal includes source locations and primary metadata.
	VerbosityNormal
	// VerbosityVerbose includes WASM instructions, raw arguments, and event payloads.
	VerbosityVerbose
)

// ParseVerbosity parses a CLI verbosity string (summary, normal, verbose).
func ParseVerbosity(s string) (Verbosity, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "normal":
		return VerbosityNormal, nil
	case "summary":
		return VerbositySummary, nil
	case "verbose":
		return VerbosityVerbose, nil
	default:
		return VerbosityNormal, fmt.Errorf("unknown trace verbosity %q; use summary, normal, or verbose", s)
	}
}

// FilterExecutionTrace returns a copy of the trace with states filtered for verbosity.
//
// Reviewer comments are carried across intact, but filtering can invalidate
// what they point at: VerbositySummary strips SourceFile and SourceLine, so
// source-anchored comments no longer resolve. Call AnnotationRefReport on the
// returned trace to find those broken anchors — they are reported rather than
// dropped, so no review history is lost by exporting at a lower verbosity.
func FilterExecutionTrace(t *ExecutionTrace, v Verbosity) *ExecutionTrace {
	if t == nil || v == VerbosityVerbose {
		return t
	}
	out := *t
	// Annotations contain slices and a map; without a deep copy the filtered
	// trace would share backing storage with the original.
	out.Annotations = t.Annotations.Clone()
	filtered := make([]ExecutionState, 0, len(t.States))
	for _, s := range t.States {
		filtered = append(filtered, filterState(s, v))
	}
	out.States = filtered
	if v == VerbositySummary {
		out.DiagnosticEvents = nil
	}
	return &out
}

func filterState(s ExecutionState, v Verbosity) ExecutionState {
	if v == VerbosityVerbose {
		return s
	}
	out := s
	if v == VerbositySummary {
		out.Arguments = nil
		out.RawArguments = nil
		out.ReturnValue = nil
		out.RawReturnValue = ""
		out.HostState = nil
		out.Memory = nil
		out.WasmInstruction = ""
		out.SourceFile = ""
		out.SourceLine = 0
		out.GitHubLink = ""
		return out
	}
	// normal: drop low-level instruction detail
	out.WasmInstruction = ""
	out.RawArguments = nil
	out.RawReturnValue = ""
	return out
}

// FilterDiagnosticEvents applies verbosity to simulator diagnostic events.
func FilterDiagnosticEvents(events []simulator.DiagnosticEvent, v Verbosity) []simulator.DiagnosticEvent {
	if v == VerbosityVerbose || len(events) == 0 {
		return events
	}
	out := make([]simulator.DiagnosticEvent, len(events))
	copy(out, events)
	for i := range out {
		if v == VerbositySummary {
			out[i].Data = ""
			out[i].Topics = nil
			out[i].WasmInstruction = nil
		} else {
			// normal: keep topics/data but drop wasm instruction strings
			out[i].WasmInstruction = nil
		}
	}
	return out
}
