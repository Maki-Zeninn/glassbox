// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package audit

// policy_level_test.go covers the three directory-verification policy levels:
// a clean directory, a chain break, a structural problem and a signature
// failure, at each level.

import "testing"

func TestParsePolicyLevel(t *testing.T) {
	cases := []struct {
		input string
		want  PolicyLevel
		ok    bool
	}{
		{"strict", PolicyStrict, true},
		{"  WARN ", PolicyWarn, true},
		{"Permissive", PolicyPermissive, true},
		{"", DefaultPolicyLevel, true},
		{"lenient", "", false},
	}

	for _, tc := range cases {
		got, ok := ParsePolicyLevel(tc.input)
		if ok != tc.ok {
			t.Errorf("ParsePolicyLevel(%q) ok = %v, want %v", tc.input, ok, tc.ok)
		}
		if ok && got != tc.want {
			t.Errorf("ParsePolicyLevel(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestIsValidPolicyLevel(t *testing.T) {
	for _, level := range PolicyLevels() {
		if !IsValidPolicyLevel(level) {
			t.Errorf("IsValidPolicyLevel(%q) = false, want true", level)
		}
	}
	if IsValidPolicyLevel("nonsense") {
		t.Error("IsValidPolicyLevel(\"nonsense\") = true, want false")
	}
}

func TestNewPolicyEnforcerFallsBackOnUnknownLevel(t *testing.T) {
	enforcer := NewPolicyEnforcer("bogus")
	if got := enforcer.Level(); got != DefaultPolicyLevel {
		t.Errorf("Level() = %q, want the strict default %q", got, DefaultPolicyLevel)
	}
}

func TestPolicyStrictFailsOnAnyIssue(t *testing.T) {
	enforcer := NewPolicyEnforcer(PolicyStrict)

	if outcome := enforcer.Enforce(nil); outcome.Fatal {
		t.Error("strict policy should not fail on a clean directory")
	}

	outcome := enforcer.Enforce([]ChainIssue{{Kind: IssueBrokenChainLink, Detail: "gap"}})
	if !outcome.Fatal {
		t.Error("strict policy must fail on a broken chain link")
	}
	if outcome.Level != PolicyStrict {
		t.Errorf("outcome level = %q, want %q", outcome.Level, PolicyStrict)
	}
}

func TestPolicyWarnReportsChainIssuesWithoutFailing(t *testing.T) {
	enforcer := NewPolicyEnforcer(PolicyWarn)

	outcome := enforcer.Enforce([]ChainIssue{
		{Kind: IssueBrokenChainLink},
		{Kind: IssueSequenceGap},
		{Kind: IssueUnsignedFile},
	})
	if outcome.Fatal {
		t.Error("warn policy must not fail on chain irregularities")
	}
	if len(outcome.Issues) != 3 {
		t.Errorf("expected the issues to be reported, got %d", len(outcome.Issues))
	}
}

func TestPolicyWarnStillFailsOnSignatureFailure(t *testing.T) {
	outcome := NewPolicyEnforcer(PolicyWarn).Enforce([]ChainIssue{{Kind: IssueSignature}})
	if !outcome.Fatal {
		t.Error("a bad signature is fatal even at warn policy")
	}
}

func TestPolicyPermissiveOnlyFailsOnSignatureFailure(t *testing.T) {
	enforcer := NewPolicyEnforcer(PolicyPermissive)

	if outcome := enforcer.Enforce([]ChainIssue{{Kind: IssueStructural}}); outcome.Fatal {
		t.Error("permissive policy must not fail on a structural issue")
	}
	if outcome := enforcer.Enforce([]ChainIssue{{Kind: IssuePolicy}}); outcome.Fatal {
		t.Error("permissive policy must not fail on a policy violation")
	}
	if outcome := enforcer.Enforce([]ChainIssue{{Kind: IssueSignature}}); !outcome.Fatal {
		t.Error("permissive policy must still fail on a signature failure")
	}
}

func TestIssuesFromDirPolicyResult(t *testing.T) {
	if got := IssuesFromDirPolicyResult(nil); len(got) != 0 {
		t.Errorf("expected no issues for a nil result, got %d", len(got))
	}

	clean := IssuesFromDirPolicyResult(&DirPolicyResult{ChainValid: true})
	if len(clean) != 0 {
		t.Errorf("expected no issues for a clean result, got %v", clean)
	}

	broken := IssuesFromDirPolicyResult(&DirPolicyResult{
		ChainValid:         false,
		PolicyViolations:   2,
		StructuralIssues:   1,
	})
	if len(broken) != 3 {
		t.Fatalf("expected 3 issues, got %d: %v", len(broken), broken)
	}
	if broken[0].Kind != IssueBrokenChainLink {
		t.Errorf("expected a broken chain link first, got %q", broken[0].Kind)
	}
	if broken[0].Detail == "" {
		t.Error("expected the chain break to carry a detail message")
	}
	if broken[1].Detail != "2 policy violations" {
		t.Errorf("policy issue detail = %q", broken[1].Detail)
	}
	if broken[2].Detail != "1 structural issue" {
		t.Errorf("structural issue detail = %q", broken[2].Detail)
	}
}

func TestEnforceResultIsFatalAtEveryLevelWhenSignatureFails(t *testing.T) {
	result := &DirPolicyResult{ChainValid: false}
	issues := []ChainIssue{{Kind: IssueSignature}}

	for _, level := range PolicyLevels() {
		outcome := NewPolicyEnforcer(level).Enforce(issues)
		if !outcome.Fatal {
			t.Errorf("level %q must fail on a signature failure", level)
		}
	}

	if outcome := NewPolicyEnforcer(PolicyWarn).EnforceResult(result); outcome.Fatal {
		t.Error("warn policy must not fail on a chain break alone")
	}
}

func TestPluralIssues(t *testing.T) {
	if got := pluralIssues(1, "policy violation"); got != "1 policy violation" {
		t.Errorf("pluralIssues(1) = %q", got)
	}
	if got := pluralIssues(4, "structural issue"); got != "4 structural issues" {
		t.Errorf("pluralIssues(4) = %q", got)
	}
}
