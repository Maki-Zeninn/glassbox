// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package security

import (
"fmt"
"sort"
)

// Confidence expresses how certain the scanner is that the finding is real.
type Confidence string

const (
// ConfidenceDefinite means the signal is unambiguous (e.g. overflow log line).
ConfidenceDefinite Confidence = "DEFINITE"
// ConfidenceProbable means heuristic evidence is strong but not conclusive.
ConfidenceProbable Confidence = "PROBABLE"
// ConfidenceInformational means the pattern is worth reviewing but may be benign.
ConfidenceInformational Confidence = "INFORMATIONAL"
)

// Exploitability expresses the ease with which the finding could be abused.
type Exploitability string

const (
// ExploitabilityHigh means exploitation requires no special access.
ExploitabilityHigh Exploitability = "HIGH"
// ExploitabilityMedium means exploitation requires some preconditions.
ExploitabilityMedium Exploitability = "MEDIUM"
// ExploitabilityLow means exploitation is constrained or theoretical.
ExploitabilityLow Exploitability = "LOW"
)

// RemediationCategory groups findings by the kind of fix required.
type RemediationCategory string

const (
RemediationAuthControl   RemediationCategory = "AUTH_CONTROL"
RemediationArithmetic    RemediationCategory = "ARITHMETIC"
RemediationInputHandling RemediationCategory = "INPUT_HANDLING"
RemediationAssetControl  RemediationCategory = "ASSET_CONTROL"
RemediationCryptography  RemediationCategory = "CRYPTOGRAPHY"
RemediationCodeQuality   RemediationCategory = "CODE_QUALITY"
RemediationGeneral       RemediationCategory = "GENERAL"
)

// EvidenceRef points to the source location or trace entry that triggered
// a finding, keeping evidence traceable without duplicating large payloads.
type EvidenceRef struct {
// Kind describes where the evidence came from.
Kind string `json:"kind"` // "source_path", "log_line", "event", "abi_function"
// Location is the file path, event index, or ABI function name.
Location string `json:"location"`
// Excerpt is a short (<=120 char) verbatim snippet from the source.
Excerpt string `json:"excerpt,omitempty"`
}

// ScoredFinding extends Finding with structured scoring dimensions.
// All new findings produced by the scanner embed this; the legacy Finding
// fields are preserved for backward-compatible text output.
type ScoredFinding struct {
Finding

// Confidence is the scanner's certainty that the issue is real.
Confidence Confidence `json:"confidence"`
// Exploitability is the assessed ease of exploitation.
Exploitability Exploitability `json:"exploitability"`
// RemediationCategory groups the finding for triage.
RemediationCategory RemediationCategory `json:"remediation_category"`
// EvidenceRefs are traceable pointers to the triggering signals.
EvidenceRefs []EvidenceRef `json:"evidence_refs,omitempty"`
// SeverityRationale explains why this severity was assigned.
SeverityRationale string `json:"severity_rationale,omitempty"`
}

// PolicyThreshold defines the minimum score dimensions required for a finding
// to be surfaced at a given severity level.  Threshold policies can be changed
// without touching raw scanner classification.
type PolicyThreshold struct {
// MinSeverity is the minimum raw Severity for a finding to pass this threshold.
MinSeverity Severity `json:"min_severity"`
// MinConfidence is the minimum Confidence required.
MinConfidence Confidence `json:"min_confidence"`
// BlockOn, when true, causes the policy check to return an error for findings
// that meet or exceed this threshold.
BlockOn bool `json:"block_on"`
}

// SeverityOrder maps severity levels to numeric values for comparison.
var SeverityOrder = map[Severity]int{
SeverityHigh:   4,
SeverityMedium: 3,
SeverityLow:    2,
SeverityInfo:   1,
}

// ConfidenceOrder maps confidence levels to numeric values for comparison.
var ConfidenceOrder = map[Confidence]int{
ConfidenceDefinite:      3,
ConfidenceProbable:      2,
ConfidenceInformational: 1,
}

// MeetsThreshold reports whether a ScoredFinding satisfies a PolicyThreshold.
func MeetsThreshold(f ScoredFinding, t PolicyThreshold) bool {
if SeverityOrder[f.Severity] < SeverityOrder[t.MinSeverity] {
return false
}
if ConfidenceOrder[f.Confidence] < ConfidenceOrder[t.MinConfidence] {
return false
}
return true
}

// PolicyCheck applies a set of thresholds to a slice of ScoredFindings and
// returns findings that exceed each threshold, plus a list of policy
// violations.  Raw classifications are not modified; only the policy layer
// decides what is actionable.
type PolicyCheck struct {
thresholds []PolicyThreshold
}

// NewPolicyCheck creates a PolicyCheck with the supplied thresholds.
func NewPolicyCheck(thresholds []PolicyThreshold) *PolicyCheck {
return &PolicyCheck{thresholds: thresholds}
}

// DefaultPolicyCheck returns a PolicyCheck that blocks on HIGH+DEFINITE,
// warns on MEDIUM+PROBABLE, and surfaces INFO+INFORMATIONAL for review.
func DefaultPolicyCheck() *PolicyCheck {
return NewPolicyCheck([]PolicyThreshold{
{MinSeverity: SeverityHigh, MinConfidence: ConfidenceDefinite, BlockOn: true},
{MinSeverity: SeverityMedium, MinConfidence: ConfidenceProbable, BlockOn: false},
{MinSeverity: SeverityInfo, MinConfidence: ConfidenceInformational, BlockOn: false},
})
}

// PolicyViolation describes a finding that crossed a blocking threshold.
type PolicyViolation struct {
Finding   ScoredFinding
Threshold PolicyThreshold
Message   string
}

// Apply evaluates findings against thresholds and returns violations and all
// findings that met at least one threshold.
func (p *PolicyCheck) Apply(findings []ScoredFinding) ([]ScoredFinding, []PolicyViolation) {
var surfaced []ScoredFinding
var violations []PolicyViolation

for _, f := range findings {
for _, thr := range p.thresholds {
if MeetsThreshold(f, thr) {
surfaced = append(surfaced, f)
if thr.BlockOn {
violations = append(violations, PolicyViolation{
Finding:   f,
Threshold: thr,
Message: fmt.Sprintf("[%s/%s] %s — exceeds blocking threshold (min: %s/%s)",
f.Severity, f.Confidence, f.Title,
thr.MinSeverity, thr.MinConfidence),
})
}
break // only match first threshold
}
}
}

return surfaced, violations
}

// SortScoredFindings sorts findings deterministically: by severity desc, then
// confidence desc, then title asc.  This ensures consistent report ordering
// across repeated runs.
func SortScoredFindings(findings []ScoredFinding) {
sort.SliceStable(findings, func(i, j int) bool {
si := SeverityOrder[findings[i].Severity]
sj := SeverityOrder[findings[j].Severity]
if si != sj {
return si > sj
}
ci := ConfidenceOrder[findings[i].Confidence]
cj := ConfidenceOrder[findings[j].Confidence]
if ci != cj {
return ci > cj
}
return findings[i].Title < findings[j].Title
})
}

// ToScoredFinding converts a legacy Finding to a ScoredFinding using
// heuristics based on finding type and severity.
func ToScoredFinding(f Finding) ScoredFinding {
conf := ConfidenceProbable
exploit := ExploitabilityMedium
rem := RemediationGeneral
rationale := "Severity assigned based on finding type and observed signal."

switch f.Type {
case FindingVerifiedRisk:
conf = ConfidenceDefinite
rationale = "VERIFIED_RISK: signal observed directly in execution log or event stream."
case FindingHeuristicWarn:
conf = ConfidenceProbable
rationale = "HEURISTIC_WARNING: pattern match on source or ABI metadata; manual review recommended."
}

switch f.Severity {
case SeverityHigh:
exploit = ExploitabilityHigh
rem = RemediationAuthControl
case SeverityMedium:
exploit = ExploitabilityMedium
rem = RemediationCodeQuality
case SeverityLow:
exploit = ExploitabilityLow
conf = ConfidenceInformational
rem = RemediationGeneral
case SeverityInfo:
exploit = ExploitabilityLow
conf = ConfidenceInformational
rem = RemediationGeneral
}

var refs []EvidenceRef
if f.Evidence != "" {
refs = []EvidenceRef{{Kind: "signal", Location: f.Evidence, Excerpt: truncate(f.Evidence, 120)}}
}

return ScoredFinding{
Finding:             f,
Confidence:          conf,
Exploitability:      exploit,
RemediationCategory: rem,
EvidenceRefs:        refs,
SeverityRationale:   rationale,
}
}

// truncate returns s truncated to at most n runes.
func truncate(s string, n int) string {
runes := []rune(s)
if len(runes) <= n {
return s
}
return string(runes[:n]) + "..."
}