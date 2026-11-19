// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package sarif

// families.go converts the two diagnostic families — source map failures
// (GB1xxx) and host-function security warnings (GB2xxx) — into SARIF results
// and merges them into a SARIF log alongside the security findings that
// Export already produces.
//
// The key difference from security findings is location handling:
//
//   - A source map failure has no source file to point at, because resolving
//     the offset is exactly what failed. SARIF's logicalLocation is the
//     correct primitive, so the result carries the WASM offset as a logical
//     location and stays schema-valid.
//   - A security warning points at a real Rust source line when source
//     mapping succeeded, and falls back to a logical WASM-offset location
//     when it did not.

import (
	"fmt"
	"sort"
	"time"

	"github.com/dotandev/glassbox/internal/security"
)

// diagnosticFingerprintKey is the SARIF partialFingerprints key used for the
// stable per-result fingerprints emitted by the diagnostic families.
const diagnosticFingerprintKey = "primaryLocationLineHash/v1"

// LogicalLocationKindWASMOffset is the SARIF logical location kind used when a
// finding cannot be attributed to a source file.
const LogicalLocationKindWASMOffset = "wasmOffset"

// LogicalLocationKindHostFunction marks a finding located by host function.
const LogicalLocationKindHostFunction = "hostFunction"

// DiagnosticOptions controls how diagnostic results are rendered.
type DiagnosticOptions struct {
	// ArtifactURI is the fallback URI for physical locations. When empty, only
	// logical locations are emitted.
	ArtifactURI string
	// RunStartTime and RunEndTime are copied onto the run invocation.
	RunStartTime time.Time
	RunEndTime   time.Time
}

// sourceMapFailureMessage composes the SARIF message for a source map failure.
func sourceMapFailureMessage(f SourceMapFailure) string {
	reason := f.Reason
	if reason == "" {
		reason = "instruction offset could not be mapped to a source line"
	}
	msg := fmt.Sprintf("WASM offset 0x%x: %s", f.Offset, reason)
	if f.Fix != "" {
		msg += ". Fix: " + f.Fix
	}
	return msg
}

// securityWarningMessage composes the SARIF message for a security warning.
func securityWarningMessage(w SecurityWarning) string {
	msg := w.Title
	if w.Description != "" {
		msg += ": " + w.Description
	}
	if w.File != "" {
		if w.Line > 0 {
			msg += fmt.Sprintf(" (%s:%d)", w.File, w.Line)
		} else {
			msg += fmt.Sprintf(" (%s)", w.File)
		}
	} else {
		msg += fmt.Sprintf(" (wasm offset 0x%x)", w.WasmOffset)
	}
	return msg
}

// levelForSourceMapFailure maps a source map rule to its default level.
func levelForSourceMapFailure(ruleID string) string {
	switch ruleID {
	case RuleSourceMapDwarfMissing:
		return LevelError
	case RuleSourceMapNoGitRepository:
		return LevelNote
	default:
		return LevelWarning
	}
}

// levelForSecurityWarning maps a security warning rule to its default level.
func levelForSecurityWarning(ruleID string) string {
	if ruleID == RuleAllowlistedUnsafeHostFunction {
		return LevelError
	}
	return LevelWarning
}

// offsetLocation builds a logical location for a WASM offset. This is the only
// location kind available when source mapping did not succeed.
func offsetLocation(offset uint64) Location {
	return Location{
		LogicalLocation: &LogicalLocation{
			Name:               fmt.Sprintf("0x%x", offset),
			Kind:               LogicalLocationKindWASMOffset,
			FullyQualifiedName: fmt.Sprintf("wasm::offset::0x%x", offset),
		},
	}
}

// physicalLocation builds a physical location for a resolved source line.
// When externalURL is non-empty it is used as the URI (giving reviewers a
// web-accessible link); otherwise the raw file path is used. The region is
// omitted when the line is unknown, which keeps the result schema-valid
// instead of emitting an invalid startLine of 0.
func physicalLocation(uri, externalURL string, line int) *PhysicalLocation {
	effectiveURI := uri
	if externalURL != "" {
		effectiveURI = externalURL
	}
	loc := &PhysicalLocation{ArtifactLocation: ArtifactLocation{URI: effectiveURI}}
	if line > 0 {
		loc.Region = &Region{StartLine: line}
	}
	return loc
}

// ResultForSourceMapFailure converts one source map failure to a SARIF result.
func ResultForSourceMapFailure(f SourceMapFailure, artifactURI string) Result {
	message := sourceMapFailureMessage(f)

	loc := offsetLocation(f.Offset)
	if artifactURI != "" {
		loc.PhysicalLocation = physicalLocation(artifactURI, "", 0)
	}

	return Result{
		RuleID:  f.RuleID,
		Level:   levelForSourceMapFailure(f.RuleID),
		Message: Message{Text: message},
		Locations: []Location{
			loc,
		},
		Fingerprints: map[string]string{
			diagnosticFingerprintKey: fingerprintForResult(f.RuleID, message, fmt.Sprintf("0x%x", f.Offset), 0),
		},
	}
}

// ResultForSecurityWarning converts one security warning to a SARIF result.
// When w.ExternalURL is non-empty it is used as the artifactLocation.uri so
// that SARIF consumers receive a web-accessible permalink rather than a
// machine-local file path.
func ResultForSecurityWarning(w SecurityWarning, artifactURI string) Result {
	message := securityWarningMessage(w)
	locationURI := w.File
	if locationURI == "" {
		locationURI = fmt.Sprintf("wasm:0x%x", w.WasmOffset)
	}

	var loc Location
	if w.File != "" {
		// Use ExternalURL as the primary URI when available; fall back to the
		// local file path so the SARIF result always has a URI.
		fileURI := w.File
		if w.ExternalURL != "" {
			fileURI = w.ExternalURL
		}
		loc = Location{
			PhysicalLocation: physicalLocation(fileURI, "", w.Line),
			LogicalLocation: &LogicalLocation{
				Name: w.Function,
				Kind: LogicalLocationKindHostFunction,
			},
		}
	} else {
		loc = offsetLocation(w.WasmOffset)
		if artifactURI != "" {
			loc.PhysicalLocation = physicalLocation(artifactURI, "", 0)
		}
	}

	return Result{
		RuleID:  w.RuleID,
		Level:   levelForSecurityWarning(w.RuleID),
		Message: Message{Text: message},
		Locations: []Location{
			loc,
		},
		Fingerprints: map[string]string{
			diagnosticFingerprintKey: fingerprintForResult(w.RuleID, message, locationURI, w.Line),
		},
	}
}

// resultsForFailures converts every source map failure, skipping entries whose
// rule is not one of the enumerated GB1xxx rules.
func resultsForFailures(failures []SourceMapFailure, artifactURI string) []Result {
	out := make([]Result, 0, len(failures))
	for _, f := range failures {
		if !isDiagnosticRuleID(f.RuleID) {
			continue
		}
		out = append(out, ResultForSourceMapFailure(f, artifactURI))
	}
	return out
}

// resultsForWarnings converts every security warning, skipping entries whose
// rule is not one of the enumerated GB2xxx rules.
func resultsForWarnings(warnings []SecurityWarning, artifactURI string) []Result {
	out := make([]Result, 0, len(warnings))
	for _, w := range warnings {
		if !isDiagnosticRuleID(w.RuleID) {
			continue
		}
		out = append(out, ResultForSecurityWarning(w, artifactURI))
	}
	return out
}

// rulesForResults returns the rule descriptors for the rule IDs present in the
// given results, ordered by rule ID so the output stays deterministic.
func rulesForResults(results []Result) []Rule {
	seen := make(map[string]Rule, len(results))
	for _, result := range results {
		if _, ok := seen[result.RuleID]; ok {
			continue
		}
		if rule, ok := RuleByID(result.RuleID); ok {
			seen[result.RuleID] = rule
		}
	}

	rules := make([]Rule, 0, len(seen))
	for _, rule := range seen {
		rules = append(rules, rule)
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].ID < rules[j].ID })
	return rules
}

// mergeDiagnosticLog folds diagnostic results and their rules into a log built
// by Export, keeping the tool, invocation and security results intact.
func mergeDiagnosticLog(
	base Log,
	failures []SourceMapFailure,
	warnings []SecurityWarning,
	opts DiagnosticOptions,
) Log {
	if len(base.Runs) == 0 {
		return base
	}

	run := base.Runs[0]
	newResults := append(
		resultsForFailures(failures, opts.ArtifactURI),
		resultsForWarnings(warnings, opts.ArtifactURI)...,
	)
	if len(newResults) == 0 {
		return base
	}

	run.Results = append(run.Results, newResults...)

	newRules := rulesForResults(newResults)
	if len(newRules) > 0 {
		existing := make(map[string]bool, len(run.Tool.Driver.Rules))
		for _, rule := range run.Tool.Driver.Rules {
			existing[rule.ID] = true
		}
		for _, rule := range newRules {
			if !existing[rule.ID] {
				run.Tool.Driver.Rules = append(run.Tool.Driver.Rules, rule)
			}
		}
		sort.Slice(run.Tool.Driver.Rules, func(i, j int) bool {
			return run.Tool.Driver.Rules[i].ID < run.Tool.Driver.Rules[j].ID
		})
	}

	sort.Slice(run.Results, func(i, j int) bool {
		if run.Results[i].RuleID != run.Results[j].RuleID {
			return run.Results[i].RuleID < run.Results[j].RuleID
		}
		return run.Results[i].Message.Text < run.Results[j].Message.Text
	})
	base.Runs[0] = run
	return base
}

// ExportDiagnostics builds a SARIF log from source map failures and security
// warnings only. Use ExportDiagnosticsWithFindings when the run also produced
// security findings.
func ExportDiagnostics(failures []SourceMapFailure, warnings []SecurityWarning, opts DiagnosticOptions) Log {
	return mergeDiagnosticLog(
		Export(nil, ExportOptions{RunStartTime: opts.RunStartTime, RunEndTime: opts.RunEndTime}),
		failures,
		warnings,
		opts,
	)
}

// ExportDiagnosticsWithFindings builds a SARIF log containing the security
// findings from a run plus both diagnostic families. This is the entry point
// used by `glassbox debug --format sarif`.
func ExportDiagnosticsWithFindings(
	findings []security.Finding,
	failures []SourceMapFailure,
	warnings []SecurityWarning,
	opts DiagnosticOptions,
) Log {
	base := Export(findings, ExportOptions{
		ArtifactURI:  opts.ArtifactURI,
		RunStartTime: opts.RunStartTime,
		RunEndTime:   opts.RunEndTime,
	})
	return mergeDiagnosticLog(base, failures, warnings, opts)
}
