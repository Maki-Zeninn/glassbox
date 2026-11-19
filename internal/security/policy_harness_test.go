// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package security

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeFixture creates a temporary source file with the given content and
// returns its path.
func writeFixture(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("writeFixture: %v", err)
	}
	return p
}

// ── pass case ────────────────────────────────────────────────────────────────

// TestRunPolicyTests_PassCase exercises a clean source file that produces no
// security findings.  The case asserts exit_zero and should pass.
func TestRunPolicyTests_PassCase(t *testing.T) {
	src := `
fn get_balance(env: Env, user: Address) -> i128 {
    user.require_auth();
    env.storage().instance().get(&user).unwrap_or(0)
}
`
	fixture := writeFixture(t, "clean.rs", src)

	manifest := PolicyManifest{
		Version: "1",
		Cases: []PolicyTestCase{
			{
				Name:     "clean source — no findings",
				Fixture:  fixture,
				ExitZero: true,
			},
		},
	}
	report := RunPolicyTests(manifest, "")
	if report.Fail != 0 {
		t.Errorf("expected 0 failures, got %d:\n%s", report.Fail, FormatReport(report))
	}
	if report.Pass != 1 {
		t.Errorf("expected 1 pass, got %d", report.Pass)
	}
}

// ── fail case ────────────────────────────────────────────────────────────────

// TestRunPolicyTests_FailCase verifies that an expected finding that is absent
// causes the test case to fail with a useful diff message.
func TestRunPolicyTests_FailCase_MissingExpectedFinding(t *testing.T) {
	// Clean source — no findings will be generated.
	src := `fn greet() { println!("hello"); }`
	fixture := writeFixture(t, "clean.rs", src)

	manifest := PolicyManifest{
		Version: "1",
		Cases: []PolicyTestCase{
			{
				Name:    "expects finding that is absent",
				Fixture: fixture,
				ExpectedFindings: []ExpectedFinding{
					{Title: "Panic-Prone Source Pattern", Severity: SeverityHigh},
				},
				ExitZero: false,
			},
		},
	}
	report := RunPolicyTests(manifest, "")
	if report.Fail != 1 {
		t.Errorf("case should fail (expected finding absent); got fail=%d pass=%d\n%s",
			report.Fail, report.Pass, FormatReport(report))
	}
	if !strings.Contains(report.Results[0].Diff, "MISSING finding") {
		t.Errorf("diff should say MISSING finding; got: %s", report.Results[0].Diff)
	}
}

// TestRunPolicyTests_FindingPresent_CasePasses shows that when an expected
// finding IS present the case passes.
func TestRunPolicyTests_FindingPresent_CasePasses(t *testing.T) {
	// Source has panic! which triggers "Panic-Prone Source Pattern".
	src := `fn risky() { panic!("not implemented"); }`
	fixture := writeFixture(t, "risky.rs", src)

	manifest := PolicyManifest{
		Version: "1",
		Cases: []PolicyTestCase{
			{
				Name:    "panic source matches expected finding",
				Fixture: fixture,
				ExpectedFindings: []ExpectedFinding{
					{Title: "Panic-Prone Source Pattern", Severity: SeverityHigh},
				},
				ExitZero: false,
			},
		},
	}
	report := RunPolicyTests(manifest, "")
	if report.Fail != 0 {
		t.Errorf("case should pass (expected finding present); got fail=%d\n%s",
			report.Fail, FormatReport(report))
	}
}

// TestRunPolicyTests_UnexpectedFinding_ExitZeroFails verifies that an
// unexpected active finding fails a case marked exit_zero.
func TestRunPolicyTests_UnexpectedFinding_ExitZeroFails(t *testing.T) {
	src := `fn owner_action(env: Env) { panic!("todo"); }`
	fixture := writeFixture(t, "owner.rs", src)

	manifest := PolicyManifest{
		Version: "1",
		Cases: []PolicyTestCase{
			{
				Name:     "exit_zero violated by active finding",
				Fixture:  fixture,
				ExitZero: true,
			},
		},
	}
	report := RunPolicyTests(manifest, "")
	if report.Fail != 1 {
		t.Errorf("case should fail (unexpected finding with exit_zero=true); got fail=%d\n%s",
			report.Fail, FormatReport(report))
	}
	if !strings.Contains(report.Results[0].Diff, "UNEXPECTED active finding") {
		t.Errorf("diff should say UNEXPECTED active finding; got: %s", report.Results[0].Diff)
	}
}

// ── expired suppression ───────────────────────────────────────────────────────

// TestRunPolicyTests_ExpiredSuppression verifies that an expired suppression
// record is flagged as an error rather than silently ignored.
func TestRunPolicyTests_ExpiredSuppression(t *testing.T) {
	src := `fn noop() { println!("ok"); }`
	fixture := writeFixture(t, "noop.rs", src)

	expired := &SuppressionRecord{
		Fingerprint: "deadbeef00000000",
		Scope:       ScopeGlobal,
		Reason:      "was reviewed last quarter",
		Owner:       "security-team",
		CreatedAt:   time.Now().Add(-720 * time.Hour),
		ExpiresAt:   time.Now().Add(-1 * time.Hour), // already expired
	}

	manifest := PolicyManifest{
		Version: "1",
		Cases: []PolicyTestCase{
			{
				Name:         "expired suppression flagged",
				Fixture:      fixture,
				Suppressions: []*SuppressionRecord{expired},
				ExitZero:     true,
			},
		},
	}
	report := RunPolicyTests(manifest, "")
	caseResult := report.Results[0]
	if len(caseResult.Errors) == 0 {
		t.Error("expired suppression should produce a case error")
	}
	found := false
	for _, e := range caseResult.Errors {
		if strings.Contains(e, "expired") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("error message should mention expiry; got: %v", caseResult.Errors)
	}
}

// ── incomplete analysis (missing fixture) ─────────────────────────────────────

// TestRunPolicyTests_MissingFixture verifies that a non-existent fixture path
// produces a case error rather than panicking.
func TestRunPolicyTests_MissingFixture(t *testing.T) {
	manifest := PolicyManifest{
		Version: "1",
		Cases: []PolicyTestCase{
			{
				Name:     "missing fixture",
				Fixture:  "/nonexistent/path/fixture.rs",
				ExitZero: true,
			},
		},
	}
	report := RunPolicyTests(manifest, "")
	caseResult := report.Results[0]
	if len(caseResult.Errors) == 0 {
		t.Error("missing fixture should produce a case error")
	}
}

// ── output formatters ─────────────────────────────────────────────────────────

func TestFormatReport_ContainsPASSAndFAIL(t *testing.T) {
	report := PolicyTestReport{
		Pass: 1,
		Fail: 1,
		Results: []PolicyCaseResult{
			{Name: "passing case", Passed: true},
			{Name: "failing case", Passed: false, Diff: "  MISSING finding: [HIGH] \"SomeTitle\" ()"},
		},
	}
	text := FormatReport(report)
	if !strings.Contains(text, "PASS") {
		t.Errorf("report should contain PASS: %s", text)
	}
	if !strings.Contains(text, "FAIL") {
		t.Errorf("report should contain FAIL: %s", text)
	}
	if !strings.Contains(text, "failing case") {
		t.Errorf("report should contain case name: %s", text)
	}
	if !strings.Contains(text, "MISSING finding") {
		t.Errorf("report should contain diff text: %s", text)
	}
}

func TestFormatReportJSON_ValidStructure(t *testing.T) {
	report := PolicyTestReport{
		Pass: 2,
		Fail: 0,
		Results: []PolicyCaseResult{
			{Name: "case1", Passed: true},
			{Name: "case2", Passed: true},
		},
	}
	data, err := FormatReportJSON(report)
	if err != nil {
		t.Fatalf("FormatReportJSON returned error: %v", err)
	}

	var parsed struct {
		Pass    int `json:"pass"`
		Fail    int `json:"fail"`
		Results []struct {
			Name   string `json:"name"`
			Passed bool   `json:"passed"`
		} `json:"results"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("JSON output is not valid: %v\n%s", err, data)
	}
	if parsed.Pass != 2 {
		t.Errorf("JSON pass=%d, want 2", parsed.Pass)
	}
	if len(parsed.Results) != 2 {
		t.Errorf("JSON results len=%d, want 2", len(parsed.Results))
	}
}

func TestFormatReportJSON_FailureIncludesDiff(t *testing.T) {
	report := PolicyTestReport{
		Pass: 0,
		Fail: 1,
		Results: []PolicyCaseResult{
			{Name: "bad case", Passed: false, Diff: "  MISSING finding: [HIGH] \"X\" ()"},
		},
	}
	data, err := FormatReportJSON(report)
	if err != nil {
		t.Fatalf("FormatReportJSON: %v", err)
	}
	if !strings.Contains(string(data), "MISSING finding") {
		t.Errorf("JSON output should include diff text: %s", data)
	}
}

// ── matchesExpected ───────────────────────────────────────────────────────────

func TestMatchesExpected_AllFieldsMatch(t *testing.T) {
	f := Finding{Title: "Auth Bypass", Severity: SeverityHigh, Type: FindingHeuristicWarn}
	e := ExpectedFinding{Title: "Auth Bypass", Severity: SeverityHigh, Type: FindingHeuristicWarn}
	if !matchesExpected(f, e) {
		t.Error("fully specified expected finding should match")
	}
}

func TestMatchesExpected_TitleOnly(t *testing.T) {
	f := Finding{Title: "Auth Bypass", Severity: SeverityHigh, Type: FindingHeuristicWarn}
	e := ExpectedFinding{Title: "Auth Bypass"}
	if !matchesExpected(f, e) {
		t.Error("title-only match should succeed")
	}
}

func TestMatchesExpected_SeverityMismatch(t *testing.T) {
	f := Finding{Title: "Auth Bypass", Severity: SeverityHigh}
	e := ExpectedFinding{Title: "Auth Bypass", Severity: SeverityLow}
	if matchesExpected(f, e) {
		t.Error("severity mismatch should not match")
	}
}

func TestMatchesExpected_EmptyExpected(t *testing.T) {
	f := Finding{Title: "Anything", Severity: SeverityInfo}
	e := ExpectedFinding{}
	if !matchesExpected(f, e) {
		t.Error("empty expected should match any finding")
	}
}
