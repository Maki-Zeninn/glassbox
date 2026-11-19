// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package security

import (
"strings"
"testing"
"time"
)

func TestScoreSecretFinding_PrivateKeyIsCritical(t *testing.T) {
f := SecretFinding{Type: SecretTypePrivateKey, Location: "config/keys.json", Context: "-----BEGIN PRIVATE KEY-----"}
sf := ScoreSecretFinding(f)
if sf.Severity != SecretSeverityCritical {
t.Errorf("expected CRITICAL severity for private key, got %s", sf.Severity)
}
if sf.Confidence != SecretConfidenceDefinite {
t.Errorf("expected DEFINITE confidence for private key, got %s", sf.Confidence)
}
if sf.SeverityRationale == "" {
t.Error("expected non-empty SeverityRationale")
}
if len(sf.EvidenceRefs) == 0 {
t.Error("expected EvidenceRefs to be populated")
}
if sf.EvidenceRefs[0].Location != "config/keys.json" {
t.Errorf("expected EvidenceRef location to be 'config/keys.json'; got %q", sf.EvidenceRefs[0].Location)
}
}

func TestScoreSecretFinding_GitHubTokenIsCritical(t *testing.T) {
f := SecretFinding{Type: SecretTypeGitHubToken, Location: "env.GH_TOKEN"}
sf := ScoreSecretFinding(f)
if sf.Severity != SecretSeverityCritical {
t.Errorf("expected CRITICAL for GitHub token, got %s", sf.Severity)
}
}

func TestScoreSecretFinding_AWSKeyIsCritical(t *testing.T) {
f := SecretFinding{Type: SecretTypeAWSKey, Location: "config.aws_key"}
sf := ScoreSecretFinding(f)
if sf.Severity != SecretSeverityCritical {
t.Errorf("expected CRITICAL for AWS key, got %s", sf.Severity)
}
}

func TestScoreSecretFinding_BearerTokenIsHighDefinite(t *testing.T) {
f := SecretFinding{Type: SecretTypeBearerToken, Location: "auth.header"}
sf := ScoreSecretFinding(f)
if sf.Severity != SecretSeverityHigh {
t.Errorf("expected HIGH for bearer token, got %s", sf.Severity)
}
if sf.Confidence != SecretConfidenceDefinite {
t.Errorf("expected DEFINITE for bearer token, got %s", sf.Confidence)
}
}

func TestScoreSecretFinding_APIKeyIsHighProbable(t *testing.T) {
f := SecretFinding{Type: SecretTypeAPIKey, Location: "config.api_key"}
sf := ScoreSecretFinding(f)
if sf.Severity != SecretSeverityHigh {
t.Errorf("expected HIGH for API key, got %s", sf.Severity)
}
if sf.Confidence != SecretConfidenceProbable {
t.Errorf("expected PROBABLE for API key, got %s", sf.Confidence)
}
}

func TestScoreSecretFinding_PEMIsMedium(t *testing.T) {
f := SecretFinding{Type: SecretTypePEM, Location: "tls.cert"}
sf := ScoreSecretFinding(f)
if sf.Severity != SecretSeverityMedium {
t.Errorf("expected MEDIUM for PEM, got %s", sf.Severity)
}
}

func TestScoreSecretFinding_UnknownTypeIsMediumInformational(t *testing.T) {
f := SecretFinding{Type: "UNKNOWN_TYPE", Location: "some.field"}
sf := ScoreSecretFinding(f)
if sf.Severity != SecretSeverityMedium {
t.Errorf("expected MEDIUM for unknown type, got %s", sf.Severity)
}
if sf.Confidence != SecretConfidenceInformational {
t.Errorf("expected INFORMATIONAL for unknown type, got %s", sf.Confidence)
}
}

func TestSortScoredSecretFindings_DeterministicOrder(t *testing.T) {
findings := []ScoredSecretFinding{
{SecretFinding: SecretFinding{Type: SecretTypeAPIKey, Location: "z"}, Severity: SecretSeverityHigh, Confidence: SecretConfidenceProbable},
{SecretFinding: SecretFinding{Type: SecretTypePrivateKey, Location: "a"}, Severity: SecretSeverityCritical, Confidence: SecretConfidenceDefinite},
{SecretFinding: SecretFinding{Type: SecretTypePEM, Location: "m"}, Severity: SecretSeverityMedium, Confidence: SecretConfidenceProbable},
{SecretFinding: SecretFinding{Type: SecretTypeAPIKey, Location: "a"}, Severity: SecretSeverityHigh, Confidence: SecretConfidenceDefinite},
}

SortScoredSecretFindings(findings)

// First: CRITICAL
if findings[0].Severity != SecretSeverityCritical {
t.Errorf("expected CRITICAL first, got %s", findings[0].Severity)
}
// Second: HIGH+DEFINITE (location "a")
if findings[1].Severity != SecretSeverityHigh || findings[1].Confidence != SecretConfidenceDefinite {
t.Errorf("expected HIGH+DEFINITE second; got %s/%s", findings[1].Severity, findings[1].Confidence)
}
// Third: HIGH+PROBABLE
if findings[2].Severity != SecretSeverityHigh || findings[2].Confidence != SecretConfidenceProbable {
t.Errorf("expected HIGH+PROBABLE third; got %s/%s", findings[2].Severity, findings[2].Confidence)
}
// Last: MEDIUM
if findings[3].Severity != SecretSeverityMedium {
t.Errorf("expected MEDIUM last, got %s", findings[3].Severity)
}

// Idempotent.
SortScoredSecretFindings(findings)
if findings[0].Severity != SecretSeverityCritical {
t.Error("sort is not idempotent")
}
}

func TestGetScanResultScored_SortsAndScores(t *testing.T) {
scanner := NewSecretScanner(ModeOptIn)
result := ScanResult{
Findings: []SecretFinding{
{Type: SecretTypeAPIKey, Location: "config.key", Context: "api_key: sk-****"},
{Type: SecretTypePrivateKey, Location: "id_rsa", Context: "-----BEGIN PRIVATE KEY-----"},
},
HasSecrets: true,
}

scored := scanner.GetScanResultScored(result)

if !scored.HasSecrets {
t.Error("HasSecrets should be true")
}
if len(scored.ActiveFindings) != 2 {
t.Fatalf("expected 2 scored findings, got %d", len(scored.ActiveFindings))
}
// Private key (CRITICAL) should be first after sorting.
if scored.ActiveFindings[0].Severity != SecretSeverityCritical {
t.Errorf("expected CRITICAL first after sort, got %s", scored.ActiveFindings[0].Severity)
}
if scored.Summary == "" {
t.Error("expected non-empty Summary")
}
}

func TestGetScanResultScored_EmptyFindings(t *testing.T) {
scanner := NewSecretScanner(ModeOptIn)
result := ScanResult{Findings: []SecretFinding{}, HasSecrets: false}
scored := scanner.GetScanResultScored(result)
if scored.HasSecrets {
t.Error("HasSecrets should be false for empty findings")
}
if !strings.Contains(scored.Summary, "No secrets") {
t.Errorf("expected 'No secrets' summary; got %q", scored.Summary)
}
}

func TestGetScanResultScored_SuppressionRespected(t *testing.T) {
f := SecretFinding{Type: SecretTypeAPIKey, Location: "config.key", Context: "api_key: sk-****"}
fp := ComputeSecretFingerprint(f)

registry := NewSuppressionRegistry()
rec := &SuppressionRecord{
Fingerprint: fp,
Scope:       ScopeGlobal,
Reason:      "test fixture key",
Owner:       "team",
CreatedAt:   time.Now().UTC(),
}
// Bypass Validate to allow zero ExpiresAt and global scope in test.
registry.records = append(registry.records, rec)

scanner := NewSecretScannerWithSuppression(ModeOptIn, registry, "")
result := ScanResult{Findings: []SecretFinding{f}, HasSecrets: true}
scored := scanner.GetScanResultScored(result)

if scored.HasSecrets {
t.Error("suppressed finding should not count as active secret")
}
if len(scored.ActiveFindings) != 0 {
t.Errorf("expected 0 active findings after suppression, got %d", len(scored.ActiveFindings))
}
if len(scored.SuppressedFindings) != 1 {
t.Errorf("expected 1 suppressed finding, got %d", len(scored.SuppressedFindings))
}
}