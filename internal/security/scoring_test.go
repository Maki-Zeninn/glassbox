// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package security

import (
"testing"
)

func TestToScoredFinding_VerifiedRiskIsDefinite(t *testing.T) {
f := Finding{Type: FindingVerifiedRisk, Severity: SeverityHigh, Title: "Overflow", Evidence: "log:overflow"}
sf := ToScoredFinding(f)
if sf.Confidence != ConfidenceDefinite {
t.Errorf("expected DEFINITE confidence for VERIFIED_RISK, got %s", sf.Confidence)
}
if sf.Exploitability != ExploitabilityHigh {
t.Errorf("expected HIGH exploitability for HIGH severity, got %s", sf.Exploitability)
}
if len(sf.EvidenceRefs) == 0 {
t.Error("expected EvidenceRefs to be populated from Evidence field")
}
if sf.SeverityRationale == "" {
t.Error("expected non-empty SeverityRationale")
}
}

func TestToScoredFinding_HeuristicIsProbable(t *testing.T) {
f := Finding{Type: FindingHeuristicWarn, Severity: SeverityMedium, Title: "Upgrade Surface"}
sf := ToScoredFinding(f)
if sf.Confidence != ConfidenceProbable {
t.Errorf("expected PROBABLE confidence for HEURISTIC_WARNING, got %s", sf.Confidence)
}
if sf.Exploitability != ExploitabilityMedium {
t.Errorf("expected MEDIUM exploitability for MEDIUM severity, got %s", sf.Exploitability)
}
}

func TestToScoredFinding_InfoIsInformational(t *testing.T) {
f := Finding{Type: FindingHeuristicWarn, Severity: SeverityInfo, Title: "Randomness Usage"}
sf := ToScoredFinding(f)
if sf.Confidence != ConfidenceInformational {
t.Errorf("expected INFORMATIONAL for INFO severity, got %s", sf.Confidence)
}
if sf.Exploitability != ExploitabilityLow {
t.Errorf("expected LOW exploitability for INFO severity, got %s", sf.Exploitability)
}
}

func TestMeetsThreshold(t *testing.T) {
tests := []struct {
name   string
f      ScoredFinding
thr    PolicyThreshold
expect bool
}{
{
name:   "HIGH+DEFINITE meets HIGH+DEFINITE threshold",
f:      ScoredFinding{Finding: Finding{Severity: SeverityHigh}, Confidence: ConfidenceDefinite},
thr:    PolicyThreshold{MinSeverity: SeverityHigh, MinConfidence: ConfidenceDefinite},
expect: true,
},
{
name:   "MEDIUM+PROBABLE does not meet HIGH+DEFINITE threshold",
f:      ScoredFinding{Finding: Finding{Severity: SeverityMedium}, Confidence: ConfidenceProbable},
thr:    PolicyThreshold{MinSeverity: SeverityHigh, MinConfidence: ConfidenceDefinite},
expect: false,
},
{
name:   "HIGH+INFORMATIONAL does not meet HIGH+DEFINITE threshold",
f:      ScoredFinding{Finding: Finding{Severity: SeverityHigh}, Confidence: ConfidenceInformational},
thr:    PolicyThreshold{MinSeverity: SeverityHigh, MinConfidence: ConfidenceDefinite},
expect: false,
},
{
name:   "LOW+INFORMATIONAL meets INFO+INFORMATIONAL threshold",
f:      ScoredFinding{Finding: Finding{Severity: SeverityLow}, Confidence: ConfidenceInformational},
thr:    PolicyThreshold{MinSeverity: SeverityInfo, MinConfidence: ConfidenceInformational},
expect: true,
},
}

for _, tt := range tests {
t.Run(tt.name, func(t *testing.T) {
got := MeetsThreshold(tt.f, tt.thr)
if got != tt.expect {
t.Errorf("MeetsThreshold() = %v, want %v", got, tt.expect)
}
})
}
}

func TestPolicyCheck_BlocksOnHighDefinite(t *testing.T) {
pc := DefaultPolicyCheck()

findings := []ScoredFinding{
{Finding: Finding{Severity: SeverityHigh, Title: "Overflow"}, Confidence: ConfidenceDefinite},
{Finding: Finding{Severity: SeverityMedium, Title: "Auth Pattern"}, Confidence: ConfidenceProbable},
{Finding: Finding{Severity: SeverityInfo, Title: "Randomness"}, Confidence: ConfidenceInformational},
}

surfaced, violations := pc.Apply(findings)
if len(surfaced) != 3 {
t.Errorf("expected 3 surfaced findings, got %d", len(surfaced))
}
if len(violations) != 1 {
t.Errorf("expected 1 blocking violation, got %d", len(violations))
}
if violations[0].Finding.Title != "Overflow" {
t.Errorf("expected Overflow to be the violation, got %s", violations[0].Finding.Title)
}
}

func TestPolicyCheck_ThresholdChangesDoNotAffectRawClassification(t *testing.T) {
// Build two policy checks with different thresholds.
// The raw ScoredFinding should be unchanged regardless.
f := Finding{Type: FindingVerifiedRisk, Severity: SeverityHigh, Title: "Overflow", Evidence: "log:overflow"}
sf := ToScoredFinding(f)

// Strict policy: block on MEDIUM+PROBABLE and above.
strict := NewPolicyCheck([]PolicyThreshold{
{MinSeverity: SeverityMedium, MinConfidence: ConfidenceProbable, BlockOn: true},
})
// Lenient policy: only surface HIGH+DEFINITE, never block.
lenient := NewPolicyCheck([]PolicyThreshold{
{MinSeverity: SeverityHigh, MinConfidence: ConfidenceDefinite, BlockOn: false},
})

_, strictViolations := strict.Apply([]ScoredFinding{sf})
_, lenientViolations := lenient.Apply([]ScoredFinding{sf})

if len(strictViolations) == 0 {
t.Error("strict policy should produce a violation for HIGH+DEFINITE")
}
if len(lenientViolations) != 0 {
t.Error("lenient policy should not produce violations")
}

// The raw finding is unmodified.
if sf.Confidence != ConfidenceDefinite {
t.Error("raw ScoredFinding confidence was mutated by policy check")
}
if sf.Severity != SeverityHigh {
t.Error("raw ScoredFinding severity was mutated by policy check")
}
}

func TestSortScoredFindings_DeterministicOrder(t *testing.T) {
findings := []ScoredFinding{
{Finding: Finding{Severity: SeverityLow, Title: "Z"}, Confidence: ConfidenceInformational},
{Finding: Finding{Severity: SeverityHigh, Title: "A"}, Confidence: ConfidenceDefinite},
{Finding: Finding{Severity: SeverityHigh, Title: "B"}, Confidence: ConfidenceProbable},
{Finding: Finding{Severity: SeverityMedium, Title: "C"}, Confidence: ConfidenceProbable},
}

SortScoredFindings(findings)

// First should be HIGH+DEFINITE "A"
if findings[0].Title != "A" || findings[0].Severity != SeverityHigh {
t.Errorf("expected HIGH+DEFINITE 'A' first, got %s/%s", findings[0].Severity, findings[0].Title)
}
// Second: HIGH+PROBABLE "B"
if findings[1].Title != "B" {
t.Errorf("expected 'B' second, got %s", findings[1].Title)
}
// Third: MEDIUM "C"
if findings[2].Severity != SeverityMedium {
t.Errorf("expected MEDIUM third, got %s", findings[2].Severity)
}
// Last: LOW "Z"
if findings[3].Severity != SeverityLow {
t.Errorf("expected LOW last, got %s", findings[3].Severity)
}

// Running again must produce identical order (determinism).
SortScoredFindings(findings)
if findings[0].Title != "A" {
t.Error("sort is not deterministic on repeated calls")
}
}

func TestPolicyViolationMessage(t *testing.T) {
pc := DefaultPolicyCheck()
sf := ScoredFinding{
Finding:    Finding{Severity: SeverityHigh, Title: "Integer Overflow"},
Confidence: ConfidenceDefinite,
}
_, violations := pc.Apply([]ScoredFinding{sf})
if len(violations) == 0 {
t.Fatal("expected at least one violation")
}
msg := violations[0].Message
if msg == "" {
t.Error("violation message should not be empty")
}
}

func TestEvidenceRef_Populated(t *testing.T) {
f := Finding{
Type:     FindingVerifiedRisk,
Severity: SeverityHigh,
Title:    "Overflow",
Evidence: "log line: checked_mul overflow at offset 42",
}
sf := ToScoredFinding(f)
if len(sf.EvidenceRefs) == 0 {
t.Fatal("expected EvidenceRefs from Evidence field")
}
ref := sf.EvidenceRefs[0]
if ref.Kind == "" {
t.Error("EvidenceRef.Kind should not be empty")
}
if ref.Location == "" {
t.Error("EvidenceRef.Location should not be empty")
}
}