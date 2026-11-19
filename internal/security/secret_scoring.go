// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package security

import (
"fmt"
"sort"
"strings"
)

// SecretSeverity classifies how sensitive a detected secret is.
type SecretSeverity string

const (
SecretSeverityCritical SecretSeverity = "CRITICAL"
SecretSeverityHigh     SecretSeverity = "HIGH"
SecretSeverityMedium   SecretSeverity = "MEDIUM"
SecretSeverityLow      SecretSeverity = "LOW"
)

// SecretConfidence mirrors the Confidence type from scoring.go but is defined
// on the scanner side to avoid coupling the secret scanner to the security
// detector.
type SecretConfidence string

const (
SecretConfidenceDefinite      SecretConfidence = "DEFINITE"
SecretConfidenceProbable      SecretConfidence = "PROBABLE"
SecretConfidenceInformational SecretConfidence = "INFORMATIONAL"
)

// ScoredSecretFinding extends SecretFinding with severity, confidence, and
// evidence references so callers get full provenance for each detected secret.
type ScoredSecretFinding struct {
SecretFinding

// Severity is the assessed sensitivity of the leaked secret.
Severity SecretSeverity `json:"severity"`
// Confidence is the scanner's certainty that this is a real secret.
Confidence SecretConfidence `json:"confidence"`
// EvidenceRefs are traceable pointers to the triggering patterns.
EvidenceRefs []EvidenceRef `json:"evidence_refs,omitempty"`
// SeverityRationale explains the severity assignment.
SeverityRationale string `json:"severity_rationale,omitempty"`
}

// secretTypeSeverity maps SecretType to a default severity and confidence.
var secretTypeSeverity = map[SecretType]struct {
severity   SecretSeverity
confidence SecretConfidence
rationale  string
}{
SecretTypePrivateKey:    {SecretSeverityCritical, SecretConfidenceDefinite, "Private keys allow direct asset control; exposure is always critical."},
SecretTypeGitHubToken:   {SecretSeverityCritical, SecretConfidenceDefinite, "GitHub tokens with repo/write access enable supply-chain attacks."},
SecretTypeAWSKey:        {SecretSeverityCritical, SecretConfidenceDefinite, "AWS credentials enable full infrastructure access if not scoped."},
SecretTypeBearerToken:   {SecretSeverityHigh, SecretConfidenceDefinite, "Bearer tokens grant authenticated API access for their lifetime."},
SecretTypeJWT:           {SecretSeverityHigh, SecretConfidenceProbable, "JWTs carry authenticated claims; exposure enables session replay."},
SecretTypeAPIKey:        {SecretSeverityHigh, SecretConfidenceProbable, "API keys authenticate service calls; impact depends on granted scopes."},
SecretTypePEM:           {SecretSeverityMedium, SecretConfidenceProbable, "PEM data may contain certificates or keys requiring further review."},
SecretTypeGenericSecret: {SecretSeverityMedium, SecretConfidenceInformational, "Generic secret pattern detected; manual review required to confirm sensitivity."},
}

// ScoreSecretFinding converts a SecretFinding to a ScoredSecretFinding with
// severity, confidence, and evidence references populated.
func ScoreSecretFinding(f SecretFinding) ScoredSecretFinding {
meta, ok := secretTypeSeverity[f.Type]
if !ok {
meta = secretTypeSeverity[SecretTypeGenericSecret]
}

var refs []EvidenceRef
if f.Location != "" {
refs = append(refs, EvidenceRef{
Kind:     "location",
Location: f.Location,
Excerpt:  truncate(f.Context, 80),
})
}

return ScoredSecretFinding{
SecretFinding:     f,
Severity:          meta.severity,
Confidence:        meta.confidence,
EvidenceRefs:      refs,
SeverityRationale: meta.rationale,
}
}

// secretSeverityOrder maps SecretSeverity to a numeric value for sorting.
var secretSeverityOrder = map[SecretSeverity]int{
SecretSeverityCritical: 4,
SecretSeverityHigh:     3,
SecretSeverityMedium:   2,
SecretSeverityLow:      1,
}

// secretConfidenceOrder maps SecretConfidence to a numeric value for sorting.
var secretConfidenceOrder = map[SecretConfidence]int{
SecretConfidenceDefinite:      3,
SecretConfidenceProbable:      2,
SecretConfidenceInformational: 1,
}

// SortScoredSecretFindings sorts findings deterministically: by severity desc,
// then confidence desc, then location asc. This ensures consistent ordering
// across repeated scan runs.
func SortScoredSecretFindings(findings []ScoredSecretFinding) {
sort.SliceStable(findings, func(i, j int) bool {
si := secretSeverityOrder[findings[i].Severity]
sj := secretSeverityOrder[findings[j].Severity]
if si != sj {
return si > sj
}
ci := secretConfidenceOrder[findings[i].Confidence]
cj := secretConfidenceOrder[findings[j].Confidence]
if ci != cj {
return ci > cj
}
return findings[i].Location < findings[j].Location
})
}

// ScanResultScored extends ScanResult with scored findings for callers that
// need full provenance.
type ScanResultScored struct {
ActiveFindings     []ScoredSecretFinding `json:"active_findings"`
SuppressedFindings []SuppressedSecretFinding `json:"suppressed_findings,omitempty"`
HasSecrets         bool `json:"has_secrets"`
// Summary is a short human-readable description of the findings.
Summary string `json:"summary,omitempty"`
}

// GetScanResultScored runs suppression and scoring over a raw ScanResult,
// returning a fully annotated ScanResultScored.
func (s *SecretScanner) GetScanResultScored(result ScanResult) ScanResultScored {
withSuppression := s.GetScanResultWithSuppression(result)

scored := make([]ScoredSecretFinding, 0, len(withSuppression.ActiveFindings))
for _, f := range withSuppression.ActiveFindings {
scored = append(scored, ScoreSecretFinding(f))
}
SortScoredSecretFindings(scored)

return ScanResultScored{
ActiveFindings:     scored,
SuppressedFindings: withSuppression.SuppressedFindings,
HasSecrets:         withSuppression.HasSecrets,
Summary:            secretSummary(scored),
}
}

func secretSummary(findings []ScoredSecretFinding) string {
if len(findings) == 0 {
return "No secrets detected."
}
counts := make(map[SecretSeverity]int)
for _, f := range findings {
counts[f.Severity]++
}
parts := make([]string, 0, 4)
for _, sev := range []SecretSeverity{SecretSeverityCritical, SecretSeverityHigh, SecretSeverityMedium, SecretSeverityLow} {
if n := counts[sev]; n > 0 {
parts = append(parts, fmt.Sprintf("%d %s", n, strings.ToLower(string(sev))))
}
}
return fmt.Sprintf("%d secret(s) detected: %s.", len(findings), strings.Join(parts, ", "))
}