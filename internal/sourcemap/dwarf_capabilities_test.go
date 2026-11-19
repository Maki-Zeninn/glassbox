// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package sourcemap

// Tests for the DWARF capability detection layer in the sourcemap package.
//
// These tests are intentionally self-contained: they build WASM fixtures in
// memory using the same helpers as internal/dwarf/capability_test.go, so no
// binary fixtures are checked into the repository.
//
// Fixture taxonomy mirrors the acceptance criteria:
//   stripped    – a WASM with no debug sections
//   partial     – a WASM with only .debug_line (no .debug_info / .debug_abbrev)
//   modern      – a fully instrumented DWARF v5 WASM
//   unsupported – a DWARF v1 WASM (below MinSupportedDWARFVersion)
//   future      – a DWARF v6 WASM (above MaxSupportedDWARFVersion)

import (
	"strings"
	"testing"
)

// ─── fixture helpers (mirror of internal/dwarf/capability_test.go) ───────────
// These are purposely duplicated here so the sourcemap package tests have no
// test-only import cycle back to internal/dwarf.

func smEncodeULEB128(v uint64) []byte {
	var out []byte
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			b |= 0x80
		}
		out = append(out, b)
		if v == 0 {
			return out
		}
	}
}

func smWasmCustomSection(name string, content []byte) []byte {
	payload := append(smEncodeULEB128(uint64(len(name))), []byte(name)...)
	payload = append(payload, content...)
	sec := []byte{0x00}
	sec = append(sec, smEncodeULEB128(uint64(len(payload)))...)
	return append(sec, payload...)
}

func smBuildWASM(sections map[string][]byte) []byte {
	out := []byte{
		0x00, 0x61, 0x73, 0x6d,
		0x01, 0x00, 0x00, 0x00,
	}
	for name, content := range sections {
		out = append(out, smWasmCustomSection(name, content)...)
	}
	return out
}

func smDebugInfoWithVersion(version uint16) []byte {
	return []byte{
		0x20, 0x00, 0x00, 0x00,
		byte(version), byte(version >> 8),
		0x00, 0x00, 0x00, 0x00,
		0x04,
	}
}

// ─── DetectCapabilitiesFromBytes ─────────────────────────────────────────────

// TestDetectCapabilitiesFromBytes_Stripped verifies that a stripped WASM binary
// (no debug sections) produces:
//   - a non-nil report
//   - DWARF.Supported = false
//   - exactly one error-severity issue with check "dwarf_missing_debug_sections"
//   - a non-empty Hint pointing to a build command
func TestDetectCapabilitiesFromBytes_Stripped(t *testing.T) {
	data := smBuildWASM(nil) // no debug sections
	report := DetectCapabilitiesFromBytes(data)

	if report == nil {
		t.Fatal("DetectCapabilitiesFromBytes returned nil for stripped binary")
	}
	if report.DWARF == nil {
		t.Fatal("report.DWARF is nil")
	}
	if report.DWARF.Supported {
		t.Error("DWARF.Supported = true, want false for stripped binary")
	}
	if report.OK() {
		t.Error("OK() = true, want false for stripped binary")
	}

	requireExactlyOneIssue(t, report.Issues, "dwarf_missing_debug_sections", "error")

	if report.Issues[0].Hint == "" {
		t.Error("issue.Hint is empty; want a build-command hint for stripped binary")
	}
	// The hint should reference Cargo so users know exactly what to run.
	if !strings.Contains(report.Issues[0].Hint, "cargo") &&
		!strings.Contains(report.Issues[0].Hint, "Cargo") {
		t.Errorf("Hint = %q, want a reference to cargo/Cargo.toml", report.Issues[0].Hint)
	}
}

// TestDetectCapabilitiesFromBytes_Partial verifies a WASM with only .debug_line
// (info stripped) produces a warning-severity partial-info issue, not an error,
// because line mapping is still available — distinct from a missing-symbols case.
func TestDetectCapabilitiesFromBytes_Partial(t *testing.T) {
	data := smBuildWASM(map[string][]byte{
		".debug_line": {0x01, 0x02, 0x03},
	})
	report := DetectCapabilitiesFromBytes(data)

	if report.DWARF.Version != 0 {
		t.Errorf("Version = %d, want 0 (no .debug_info)", report.DWARF.Version)
	}
	if !report.DWARF.Mappings.SourceLines {
		t.Error("SourceLines = false, want true with .debug_line")
	}
	if report.DWARF.Mappings.LocalVars {
		t.Error("LocalVars = true, want false without .debug_info/.debug_abbrev")
	}
	if !report.DWARF.Supported {
		t.Error("DWARF.Supported = false, want true (partial line mapping available)")
	}
	// Partial debug info must not block mapping — it's a warning, not an error.
	requireExactlyOneIssue(t, report.Issues, "dwarf_partial_debug_info", "warning")
	// A partial binary is still OK() from the sourcemap perspective.
	if !report.OK() {
		t.Error("OK() = false, want true for partial binary (line mapping available)")
	}
}

// TestDetectCapabilitiesFromBytes_Modern verifies a fully instrumented DWARF v5
// WASM with all three required sections produces zero issues and is fully
// supported — this is the "maps as before" acceptance criterion.
func TestDetectCapabilitiesFromBytes_Modern(t *testing.T) {
	data := smBuildWASM(map[string][]byte{
		".debug_info":   smDebugInfoWithVersion(5),
		".debug_abbrev": {0x01, 0x02, 0x03},
		".debug_line":   {0x01, 0x02, 0x03},
	})
	report := DetectCapabilitiesFromBytes(data)

	if report.DWARF.Version != 5 {
		t.Errorf("Version = %d, want 5", report.DWARF.Version)
	}
	if !report.DWARF.Supported {
		t.Error("DWARF.Supported = false, want true for modern DWARF v5")
	}
	if len(report.Issues) != 0 {
		t.Errorf("Issues = %+v, want none for fully supported binary", report.Issues)
	}
	if !report.DWARF.Mappings.SourceLines || !report.DWARF.Mappings.LocalVars || !report.DWARF.Mappings.InlineFrames {
		t.Errorf("Mappings = %+v, want all true for modern binary", report.DWARF.Mappings)
	}
	if !report.OK() {
		t.Error("OK() = false, want true for modern binary")
	}
}

// TestDetectCapabilitiesFromBytes_UnsupportedOld verifies a DWARF v1 binary
// produces an error-severity issue that names the detected version (1) and the
// supported range (v2–v5) — distinct from "missing debug sections".
func TestDetectCapabilitiesFromBytes_UnsupportedOld(t *testing.T) {
	data := smBuildWASM(map[string][]byte{
		".debug_info":   smDebugInfoWithVersion(1),
		".debug_abbrev": {0x01},
		".debug_line":   {0x01},
	})
	report := DetectCapabilitiesFromBytes(data)

	if report.DWARF.Version != 1 {
		t.Fatalf("Version = %d, want 1", report.DWARF.Version)
	}
	requireExactlyOneIssue(t, report.Issues, "dwarf_unsupported_dwarf_version", "error")

	msg := report.Issues[0].Description
	if !strings.Contains(msg, "1") {
		t.Errorf("Description = %q, want the detected version (1) named", msg)
	}
	// Must mention the supported range so users know what they need.
	if !strings.Contains(msg, "v2") && !strings.Contains(msg, "2") {
		t.Errorf("Description = %q, want the supported range mentioned (v2–v5)", msg)
	}
	// Hint must tell the user how to fix it.
	if report.Issues[0].Hint == "" {
		t.Error("Hint is empty; want compiler guidance for unsupported DWARF version")
	}
	if report.OK() {
		t.Error("OK() = true, want false when DWARF version is unsupported")
	}
}

// TestDetectCapabilitiesFromBytes_UnsupportedFuture verifies a hypothetical
// DWARF v6 binary (newer than the supported range) is reported with an
// error-severity issue rather than silently failing.
func TestDetectCapabilitiesFromBytes_UnsupportedFuture(t *testing.T) {
	data := smBuildWASM(map[string][]byte{
		".debug_info": smDebugInfoWithVersion(6),
	})
	report := DetectCapabilitiesFromBytes(data)

	if report.DWARF.Version != 6 {
		t.Fatalf("Version = %d, want 6", report.DWARF.Version)
	}
	if len(report.Issues) == 0 {
		t.Fatal("Issues is empty, want at least one unsupported-version issue")
	}
	// Check that at least one issue is the unsupported-version kind.
	found := false
	for _, iss := range report.Issues {
		if iss.Check == "dwarf_unsupported_dwarf_version" && iss.Severity == "error" {
			found = true
		}
	}
	if !found {
		t.Errorf("Issues = %+v, want a dwarf_unsupported_dwarf_version error issue", report.Issues)
	}
	if report.OK() {
		t.Error("OK() = true, want false for future unsupported DWARF version")
	}
}

// TestDetectCapabilitiesFromBytes_MissingAbbrev verifies that .debug_info
// present without .debug_abbrev produces a partial-info warning (not an error),
// since line mapping via .debug_line may still work.
func TestDetectCapabilitiesFromBytes_MissingAbbrev(t *testing.T) {
	data := smBuildWASM(map[string][]byte{
		".debug_info": smDebugInfoWithVersion(4),
		".debug_line": {0x01},
		// .debug_abbrev deliberately absent
	})
	report := DetectCapabilitiesFromBytes(data)

	if report.DWARF.Mappings.LocalVars {
		t.Error("LocalVars = true, want false without .debug_abbrev")
	}
	if !report.DWARF.Mappings.SourceLines {
		t.Error("SourceLines = false, want true with .debug_line + supported version")
	}

	foundPartial := false
	for _, iss := range report.Issues {
		if iss.Check == "dwarf_partial_debug_info" {
			foundPartial = true
			if iss.Severity != "warning" {
				t.Errorf("partial_debug_info severity = %q, want warning", iss.Severity)
			}
		}
	}
	if !foundPartial {
		t.Errorf("Issues = %+v, want dwarf_partial_debug_info warning", report.Issues)
	}
}

// TestCapabilityReport_Summary verifies that Summary() returns a non-empty
// string for each fixture kind, and that it includes the DWARF summary prefix.
func TestCapabilityReport_Summary(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{"stripped", smBuildWASM(nil)},
		{"partial", smBuildWASM(map[string][]byte{".debug_line": {0x01}})},
		{"modern", smBuildWASM(map[string][]byte{
			".debug_info":   smDebugInfoWithVersion(5),
			".debug_abbrev": {0x01},
			".debug_line":   {0x01},
		})},
		{"unsupported_old", smBuildWASM(map[string][]byte{".debug_info": smDebugInfoWithVersion(1)})},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report := DetectCapabilitiesFromBytes(tc.data)
			summary := report.Summary()
			if summary == "" {
				t.Error("Summary() returned empty string, want a non-empty description")
			}
			// Every summary must mention the binary type so it is unambiguous.
			if !strings.Contains(summary, "wasm") {
				t.Errorf("Summary = %q, want 'wasm' mentioned", summary)
			}
		})
	}
}

// TestCapabilityReport_NilSafety verifies that a nil CapabilityReport never
// panics when its methods are called.
func TestCapabilityReport_NilSafety(t *testing.T) {
	var r *CapabilityReport
	if r.OK() {
		t.Error("nil.OK() = true, want false")
	}
	if r.Summary() != "" {
		t.Errorf("nil.Summary() = %q, want empty", r.Summary())
	}
}

// TestDetectCapabilitiesFromBytes_Unrecognised verifies that non-binary input
// produces a report with an error-severity issue rather than panicking.
func TestDetectCapabilitiesFromBytes_Unrecognised(t *testing.T) {
	report := DetectCapabilitiesFromBytes([]byte("this is not a binary"))
	if report == nil {
		t.Fatal("DetectCapabilitiesFromBytes returned nil for unrecognised input")
	}
	if len(report.Issues) == 0 {
		t.Error("Issues is empty for unrecognised input, want at least one issue")
	}
	// Unrecognised format cannot produce any mapping — must be error severity.
	allError := true
	for _, iss := range report.Issues {
		if iss.Severity != "error" {
			allError = false
		}
	}
	if !allError {
		t.Errorf("Issues = %+v, want all error-severity for unrecognised input", report.Issues)
	}
}

// TestTranslateDWARFWarnings_SeverityMapping exercises the severity mapping
// rules: missing_debug_sections → error, unsupported_dwarf_version → error,
// partial_debug_info → warning.
func TestTranslateDWARFWarnings_SeverityMapping(t *testing.T) {
	// stripped → error
	stripped := DetectCapabilitiesFromBytes(smBuildWASM(nil))
	for _, iss := range stripped.Issues {
		if iss.Check == "dwarf_missing_debug_sections" && iss.Severity != "error" {
			t.Errorf("missing_debug_sections severity = %q, want error", iss.Severity)
		}
	}

	// unsupported version → error
	unsup := DetectCapabilitiesFromBytes(smBuildWASM(map[string][]byte{
		".debug_info": smDebugInfoWithVersion(1),
	}))
	for _, iss := range unsup.Issues {
		if iss.Check == "dwarf_unsupported_dwarf_version" && iss.Severity != "error" {
			t.Errorf("unsupported_dwarf_version severity = %q, want error", iss.Severity)
		}
	}

	// partial → warning
	partial := DetectCapabilitiesFromBytes(smBuildWASM(map[string][]byte{
		".debug_line": {0x01},
	}))
	for _, iss := range partial.Issues {
		if iss.Check == "dwarf_partial_debug_info" && iss.Severity != "warning" {
			t.Errorf("partial_debug_info severity = %q, want warning", iss.Severity)
		}
	}
}

// TestResolver_DWARFCapabilityReport_InitiallyNil verifies that a freshly
// constructed Resolver returns nil from DWARFCapabilityReport before any
// AutoDiscoverLocalSymbols call.
func TestResolver_DWARFCapabilityReport_InitiallyNil(t *testing.T) {
	r := NewResolver(WithNonInteractive())
	if r.DWARFCapabilityReport() != nil {
		t.Error("DWARFCapabilityReport() is non-nil before any discovery; want nil")
	}
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// requireExactlyOneIssue asserts that issues contains exactly one issue with
// the given check label and severity, and fails the test otherwise.
func requireExactlyOneIssue(t *testing.T, issues []PreflightIssue, check, severity string) {
	t.Helper()
	var matched []PreflightIssue
	for _, iss := range issues {
		if iss.Check == check {
			matched = append(matched, iss)
		}
	}
	if len(matched) != 1 {
		t.Fatalf("Issues = %+v, want exactly one issue with check=%q (got %d)", issues, check, len(matched))
	}
	if matched[0].Severity != severity {
		t.Errorf("Issue[check=%q].Severity = %q, want %q", check, matched[0].Severity, severity)
	}
}
