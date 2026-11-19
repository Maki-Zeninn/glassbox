// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// This file bridges the internal/dwarf capability-detection API with the
// sourcemap package so that callers (CLI commands, the debug harness, tests)
// receive structured, actionable warnings about DWARF limitations instead of
// generic "no debug info" errors.
//
// Design notes:
//   - DetectCapabilitiesFromFile is the primary entry point: it reads a WASM
//     (or other binary) from disk, calls dwarf.DetectCapabilities, and
//     converts the result into the sourcemap.PreflightIssue format the rest of
//     the package already uses.
//   - The Resolver.DWARFCapabilityReport accessor exposes the last report
//     computed by AutoDiscoverLocalSymbols so CLI commands can print a
//     capabilities table without re-reading the file.
//   - Three precise DWARF diagnostic cases are mapped to human-readable issues:
//       1. Missing debug sections  → error-severity (mapping is completely blocked)
//       2. Unsupported DWARF version → error-severity (names the version and range)
//       3. Partial debug info        → warning-severity (line mapping still works)

package sourcemap

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/dotandev/glassbox/internal/dwarf"
)

// CapabilityReport is a sourcemap-layer wrapper around dwarf.Capabilities that
// carries both the raw DWARF report and the translated PreflightIssues.  It is
// attached to the Resolver after a successful AutoDiscoverLocalSymbols call so
// callers do not need to re-read the WASM file to access the capability data.
type CapabilityReport struct {
	// DWARF is the raw capability report from the dwarf package.
	DWARF *dwarf.Capabilities
	// Issues is the translated list of PreflightIssues (errors and warnings)
	// derived from the DWARF report, using the same format as RunSourceMapPreflight.
	Issues []PreflightIssue
}

// OK returns true when the binary is fully or partially supported (i.e. at
// least one source-mapping capability is available and no error-severity issue
// exists).
func (r *CapabilityReport) OK() bool {
	if r == nil || r.DWARF == nil {
		return false
	}
	return r.DWARF.Supported && !hasErrors(r.Issues)
}

// Summary returns a multi-line human-readable description of the capability
// report, suitable for CLI diagnostic output.  It includes the binary type,
// DWARF version (when known), detected sections, available mappings, and every
// issue.  Returns an empty string when the report is nil.
func (r *CapabilityReport) Summary() string {
	if r == nil || r.DWARF == nil {
		return ""
	}
	base := r.DWARF.Summary()
	if len(r.Issues) == 0 {
		return base
	}
	issueReport := &PreflightReport{Issues: r.Issues}
	return fmt.Sprintf("%s\n%s", base, issueReport.Summary())
}

// DetectCapabilitiesFromFile reads the binary at path, runs DWARF capability
// detection, and returns a CapabilityReport with translated PreflightIssues.
//
// Failure modes:
//   - File not found or unreadable → returns (nil, err).
//   - Unrecognised binary format   → returns a CapabilityReport with a
//     single error-severity issue (no error return).
//   - All other cases return a non-nil report and a nil error even when the
//     binary is partially or fully unsupported; issues are in the report.
func DetectCapabilitiesFromFile(path string) (*CapabilityReport, error) {
	path = filepath.Clean(path)
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("reading binary for DWARF capability detection: %w", err)
	}
	return DetectCapabilitiesFromBytes(data), nil
}

// DetectCapabilitiesFromBytes runs DWARF capability detection on in-memory
// binary data and returns a CapabilityReport with translated PreflightIssues.
// It never returns nil; unrecognised input produces a report with one error.
func DetectCapabilitiesFromBytes(data []byte) *CapabilityReport {
	caps := dwarf.DetectCapabilities(data)
	issues := translateDWARFWarnings(caps)
	return &CapabilityReport{
		DWARF:  caps,
		Issues: issues,
	}
}

// translateDWARFWarnings converts dwarf.Warning values into sourcemap
// PreflightIssues using the following severity mapping:
//
//	WarnMissingDebugSections → "error"   (no mapping possible at all)
//	WarnUnsupportedVersion   → "error"   (.debug_info mapping blocked)
//	WarnPartialDebugInfo     → "warning" (degraded but not zero capability)
//
// The check label is prefixed with "dwarf_" so issues are distinguishable from
// the WASM structural checks added by RunSourceMapPreflight.
func translateDWARFWarnings(caps *dwarf.Capabilities) []PreflightIssue {
	if caps == nil {
		return nil
	}

	var issues []PreflightIssue
	for _, w := range caps.Warnings {
		severity := warningKindToSeverity(w.Kind)
		issues = append(issues, PreflightIssue{
			Check:       "dwarf_" + string(w.Kind),
			Severity:    severity,
			Description: w.Message,
			Hint:        w.Hint,
		})
	}
	return issues
}

// warningKindToSeverity maps a dwarf.WarningKind to its PreflightIssue
// severity level.
func warningKindToSeverity(kind dwarf.WarningKind) string {
	switch kind {
	case dwarf.WarnPartialDebugInfo:
		// Partial info → line mapping still works; degraded, not blocked.
		return "warning"
	default:
		// Missing sections and unsupported version fully block source mapping.
		return "error"
	}
}

// DWARFCapabilityReport returns the DWARF capability report computed by the
// most recent AutoDiscoverLocalSymbols call, or nil when that method has not
// been called or no WASM file was successfully read.
//
// Callers can use this report to display a capabilities table (e.g. in
// --debug-info mode) or to decide whether to abort source mapping before
// attempting any DWARF tree parsing.
func (r *Resolver) DWARFCapabilityReport() *CapabilityReport {
	return r.dwarfCaps
}

