// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package sarif

// families_test.go covers the GB1xxx and GB2xxx diagnostic rule families: the
// enumerated rule metadata, the logical-location handling for findings with no
// source file, and the merge into a log that already carries security results.

import (
	"encoding/json"
	"testing"

	"github.com/dotandev/glassbox/internal/security"
)

func testFailures() []SourceMapFailure {
	return []SourceMapFailure{
		{
			RuleID:   RuleSourceMapOffsetNotFound,
			Offset:   0x1a4c,
			Reason:   "no DWARF line table entry for this offset",
			Fix:      "build the contract with debug = true",
			Function: "transfer",
		},
		{
			RuleID: RuleSourceMapDwarfMissing,
			Offset: 0,
			Reason: "binary has no .debug_line section",
			Fix:    "build the contract with debug = true",
		},
		{
			RuleID: RuleSourceMapNoGitRepository,
			Reason: "no Git repository detected in the workspace",
			Fix:    "run git init, or configure external_source_repos",
		},
	}
}

func testWarnings() []SecurityWarning {
	return []SecurityWarning{
		{
			RuleID:     RuleDeprecatedHostFunction,
			Title:      "bytes_new_from_linear_memory",
			Description: "removed in a future protocol release",
			File:       "src/lib.rs",
			Line:       88,
			WasmOffset: 0x2b0,
			Function:   "bytes_new_from_linear_memory",
		},
		{
			RuleID:     RuleDeprecatedHostFunction,
			Title:      "vec_unpack_to_linear_memory",
			Description: "removed in a future protocol release",
			WasmOffset: 0x31c,
			Function:   "vec_unpack_to_linear_memory",
		},
		{
			RuleID:     RuleAllowlistedUnsafeHostFunction,
			Title:      "storage_put",
			Description: "writes to storage without an authorization check",
			WasmOffset: 0x40,
			Function:   "storage_put",
		},
	}
}

func TestDiagnosticRuleIDsAreEnumerated(t *testing.T) {
	want := []string{
		RuleSourceMapOffsetNotFound,
		RuleSourceMapDwarfMissing,
		RuleSourceMapNoGitRepository,
		RuleDeprecatedHostFunction,
		RuleAllowlistedUnsafeHostFunction,
	}

	rules := DiagnosticRules()
	if len(rules) != len(want) {
		t.Fatalf("expected %d diagnostic rules, got %d", len(want), len(rules))
	}

	for i, id := range want {
		if rules[i].ID != id {
			t.Errorf("rule[%d].ID = %q, want %q", i, rules[i].ID, id)
		}
	}
}

func TestDiagnosticRulesCarryRequiredMetadata(t *testing.T) {
	for _, rule := range DiagnosticRules() {
		if rule.ShortDescription.Text == "" {
			t.Errorf("rule %s has no shortDescription", rule.ID)
		}
		if rule.FullDescription == nil || rule.FullDescription.Text == "" {
			t.Errorf("rule %s has no fullDescription", rule.ID)
		}
		if rule.HelpURI == "" {
			t.Errorf("rule %s has no helpUri", rule.ID)
		}
		if rule.DefaultConfig == nil || rule.DefaultConfig.Level == "" {
			t.Errorf("rule %s has no defaultConfiguration level", rule.ID)
		}
		switch rule.DefaultConfig.Level {
		case LevelError, LevelWarning, LevelNote:
		default:
			t.Errorf("rule %s has an invalid level %q", rule.ID, rule.DefaultConfig.Level)
		}
	}
}

func TestRuleByID(t *testing.T) {
	if _, ok := RuleByID(RuleSourceMapDwarfMissing); !ok {
		t.Error("expected to look up a known rule")
	}
	if _, ok := RuleByID("GB9999"); ok {
		t.Error("expected an unknown rule to miss")
	}
}

func TestIsDiagnosticRuleID(t *testing.T) {
	if !isDiagnosticRuleID(RuleDeprecatedHostFunction) {
		t.Error("GB2001 should be recognised")
	}
	if isDiagnosticRuleID("GB-0123") {
		t.Error("derived security rule IDs are not diagnostic rules")
	}
}

func TestResultForSourceMapFailureUsesALogicalLocation(t *testing.T) {
	result := ResultForSourceMapFailure(testFailures()[0], "")

	if result.RuleID != RuleSourceMapOffsetNotFound {
		t.Errorf("ruleId = %q", result.RuleID)
	}
	if result.Level != LevelWarning {
		t.Errorf("level = %q, want %q", result.Level, LevelWarning)
	}
	if len(result.Locations) != 1 || result.Locations[0].LogicalLocation == nil {
		t.Fatalf("expected a logical location, got %+v", result.Locations)
	}

	logical := result.Locations[0].LogicalLocation
	if logical.Kind != LogicalLocationKindWASMOffset {
		t.Errorf("logical kind = %q, want %q", logical.Kind, LogicalLocationKindWASMOffset)
	}
	if logical.Name != "0x1a4c" {
		t.Errorf("logical name = %q, want the hex offset", logical.Name)
	}
	if result.Message.Text == "" {
		t.Error("expected a non-empty message")
	}
}

func TestSourceMapFailureMessageIncludesOffsetReasonAndFix(t *testing.T) {
	msg := sourceMapFailureMessage(testFailures()[0])
	for _, want := range []string{"0x1a4c", "no DWARF line table entry", "debug = true"} {
		if !containsSubstring(msg, want) {
			t.Errorf("message %q is missing %q", msg, want)
		}
	}
}

func TestSourceMapFailureMessageFallsBackToAGenericReason(t *testing.T) {
	msg := sourceMapFailureMessage(SourceMapFailure{RuleID: RuleSourceMapNoGitRepository, Offset: 8})
	if msg == "" {
		t.Fatal("expected a message even with no reason supplied")
	}
	if !containsSubstring(msg, "could not be mapped") {
		t.Errorf("expected the default reason, got %q", msg)
	}
}

func TestMissingDwarfIsAnError(t *testing.T) {
	for _, failure := range testFailures() {
		result := ResultForSourceMapFailure(failure, "")
		switch failure.RuleID {
		case RuleSourceMapDwarfMissing:
			if result.Level != LevelError {
				t.Errorf("GB1002 level = %q, want %q", result.Level, LevelError)
			}
		case RuleSourceMapNoGitRepository:
			if result.Level != LevelNote {
				t.Errorf("GB1003 level = %q, want %q", result.Level, LevelNote)
			}
		default:
			if result.Level != LevelWarning {
				t.Errorf("%s level = %q, want %q", failure.RuleID, result.Level, LevelWarning)
			}
		}
	}
}

func TestResultForSecurityWarningUsesAPhysicalLocationWhenMapped(t *testing.T) {
	result := ResultForSecurityWarning(testWarnings()[0], "")

	if result.Level != LevelWarning {
		t.Errorf("level = %q, want %q", result.Level, LevelWarning)
	}

	loc := result.Locations[0]
	if loc.PhysicalLocation == nil {
		t.Fatal("expected a physical location for a mapped warning")
	}
	if loc.PhysicalLocation.ArtifactLocation.URI != "src/lib.rs" {
		t.Errorf("uri = %q", loc.PhysicalLocation.ArtifactLocation.URI)
	}
	if loc.PhysicalLocation.Region == nil || loc.PhysicalLocation.Region.StartLine != 88 {
		t.Errorf("expected startLine 88, got %+v", loc.PhysicalLocation.Region)
	}
	if loc.LogicalLocation == nil || loc.LogicalLocation.Kind != LogicalLocationKindHostFunction {
		t.Errorf("expected a host-function logical location, got %+v", loc.LogicalLocation)
	}
}

func TestResultForSecurityWarningFallsBackToALogicalLocation(t *testing.T) {
	result := ResultForSecurityWarning(testWarnings()[1], "")

	loc := result.Locations[0]
	if loc.PhysicalLocation != nil {
		t.Error("an unmapped warning must not claim a physical location")
	}
	if loc.LogicalLocation == nil || loc.LogicalLocation.Name != "0x31c" {
		t.Errorf("expected the WASM offset as a logical location, got %+v", loc.LogicalLocation)
	}
}

func TestAllowlistedUnsafeHostFunctionIsAnError(t *testing.T) {
	result := ResultForSecurityWarning(testWarnings()[2], "")
	if result.Level != LevelError {
		t.Errorf("GB2002 level = %q, want %q", result.Level, LevelError)
	}
}

func TestSecurityWarningMessageDistinguishesMappedFromUnmapped(t *testing.T) {
	mapped := securityWarningMessage(testWarnings()[0])
	if !containsSubstring(mapped, "src/lib.rs:88") {
		t.Errorf("expected a file:line suffix, got %q", mapped)
	}

	unmapped := securityWarningMessage(testWarnings()[1])
	if !containsSubstring(unmapped, "wasm offset 0x31c") {
		t.Errorf("expected a WASM offset suffix, got %q", unmapped)
	}
}

func TestExportDiagnosticsProducesResultsAndRules(t *testing.T) {
	log := ExportDiagnostics(testFailures(), testWarnings(), DiagnosticOptions{})

	if log.Version != SARIFVersion {
		t.Errorf("version = %q, want %q", log.Version, SARIFVersion)
	}
	if len(log.Runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(log.Runs))
	}

	run := log.Runs[0]
	if len(run.Results) != len(testFailures())+len(testWarnings()) {
		t.Fatalf("expected %d results, got %d", len(testFailures())+len(testWarnings()), len(run.Results))
	}
	if len(run.Tool.Driver.Rules) != 5 {
		t.Errorf("expected 5 diagnostic rules, got %d", len(run.Tool.Driver.Rules))
	}

	for _, result := range run.Results {
		if !isDiagnosticRuleID(result.RuleID) {
			t.Errorf("unexpected rule id %q", result.RuleID)
		}
		if result.Message.Text == "" {
			t.Errorf("result %s has no message", result.RuleID)
		}
		if len(result.Fingerprints) == 0 {
			t.Errorf("result %s has no fingerprint", result.RuleID)
		}
	}
}

func TestExportDiagnosticsSkipsUnknownRuleIDs(t *testing.T) {
	log := ExportDiagnostics(
		[]SourceMapFailure{{RuleID: "GB9999", Offset: 1, Reason: "nope"}},
		nil,
		DiagnosticOptions{},
	)
	if got := len(log.Runs[0].Results); got != 0 {
		t.Errorf("expected unknown rule ids to be skipped, got %d results", got)
	}
}

func TestExportDiagnosticsIsDeterministic(t *testing.T) {
	first := ExportDiagnostics(testFailures(), testWarnings(), DiagnosticOptions{})
	second := ExportDiagnostics(testFailures(), testWarnings(), DiagnosticOptions{})

	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("marshal first: %v", err)
	}
	secondJSON, err := json.Marshal(second)
	if err != nil {
		t.Fatalf("marshal second: %v", err)
	}
	if string(firstJSON) != string(secondJSON) {
		t.Error("expected two exports of the same input to be byte-identical")
	}
}

func TestExportDiagnosticsWithFindingsKeepsSecurityResults(t *testing.T) {
	findings := []security.Finding{{
		Type:        security.FindingVerifiedRisk,
		Severity:    security.SeverityHigh,
		Title:       "Unchecked Asset Minting",
		Description: "mint without auth",
	}}

	log := ExportDiagnosticsWithFindings(findings, testFailures(), testWarnings(), DiagnosticOptions{})

	results := log.Runs[0].Results
	if len(results) != len(findings)+len(testFailures())+len(testWarnings()) {
		t.Fatalf("expected security results to be preserved, got %d", len(results))
	}

	// Rules must stay sorted so the document is diff-stable for a SIEM.
	rules := log.Runs[0].Tool.Driver.Rules
	for i := 1; i < len(rules); i++ {
		if rules[i-1].ID > rules[i].ID {
			t.Errorf("rules are not sorted: %q before %q", rules[i-1].ID, rules[i].ID)
		}
	}
}

func TestExportDiagnosticsWithNoInputIsAnEmptyLog(t *testing.T) {
	log := ExportDiagnostics(nil, nil, DiagnosticOptions{})
	if len(log.Runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(log.Runs))
	}
	if len(log.Runs[0].Results) != 0 {
		t.Errorf("expected no results, got %d", len(log.Runs[0].Results))
	}
}

func TestLogicalLocationIsSerialisedForSchemaCompliance(t *testing.T) {
	result := ResultForSourceMapFailure(testFailures()[0], "")

	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !containsSubstring(string(encoded), `"logicalLocation"`) {
		t.Errorf("expected a serialised logicalLocation, got %s", encoded)
	}
	if containsSubstring(string(encoded), `"startLine":0`) {
		t.Errorf("an unmapped result must not emit an invalid startLine of 0: %s", encoded)
	}
}

func TestMarshalProducesValidJSON(t *testing.T) {
	log := ExportDiagnostics(testFailures(), testWarnings(), DiagnosticOptions{})

	encoded, err := Marshal(log)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var roundTripped Log
	if err := json.Unmarshal(encoded, &roundTripped); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if roundTripped.Version != SARIFVersion {
		t.Errorf("round-tripped version = %q", roundTripped.Version)
	}
}

// containsSubstring is a local helper to keep the assertions readable.
func containsSubstring(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
