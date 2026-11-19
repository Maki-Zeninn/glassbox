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

func validRecord(fingerprint, scopeValue string, scope SuppressionScope) *SuppressionRecord {
now := time.Now().UTC()
return &SuppressionRecord{
Fingerprint: fingerprint,
Scope:       scope,
ScopeValue:  scopeValue,
Reason:      "reviewed and accepted",
Owner:       "security-team",
Reviewer:    "alice@example.com",
Signature:   "sig-abc",
CreatedAt:   now,
ExpiresAt:   now.Add(30 * 24 * time.Hour),
}
}

// ── policy validation ────────────────────────────────────────────────────────

func TestValidateSuppressions_PassesCleanRecords(t *testing.T) {
policy := SuppressionPolicyConfig{
AllowGlobalScope: true,
RequireReviewer:  false,
MaxExpiryDays:    0,
}
rec := &SuppressionRecord{
Fingerprint: "abc123",
Scope:       ScopeGlobal,
Reason:      "test",
Owner:       "team",
CreatedAt:   time.Now().UTC(),
}
report := ValidateSuppressions([]*SuppressionRecord{rec}, policy, nil)
if report.HasErrors() {
t.Errorf("expected no violations; got: %v", report.Violations)
}
}

func TestValidateSuppressions_RejectsGlobalScopeWhenDisallowed(t *testing.T) {
policy := DefaultSuppressionPolicy()
rec := validRecord("fp1", "", ScopeGlobal)
rec.Scope = ScopeGlobal
rec.ScopeValue = ""
// DefaultSuppressionPolicy disallows global scope.
report := ValidateSuppressions([]*SuppressionRecord{rec}, policy, nil)
if !report.HasErrors() {
t.Error("expected violation for global scope when disallowed")
}
found := false
for _, v := range report.Violations {
if strings.Contains(v.Reason, "global scope") {
found = true
}
}
if !found {
t.Errorf("expected global-scope violation message; got: %v", report.Violations)
}
}

func TestValidateSuppressions_RejectsPathTraversal(t *testing.T) {
policy := SuppressionPolicyConfig{AllowGlobalScope: true}
rec := &SuppressionRecord{
Fingerprint: "fp1",
Scope:       ScopePath,
ScopeValue:  "../../etc/passwd",
Reason:      "test",
Owner:       "team",
CreatedAt:   time.Now().UTC(),
ExpiresAt:   time.Now().UTC().Add(24 * time.Hour),
}
report := ValidateSuppressions([]*SuppressionRecord{rec}, policy, nil)
if !report.HasErrors() {
t.Error("expected violation for path traversal in scope_value")
}
found := false
for _, v := range report.Violations {
if strings.Contains(v.Reason, "path traversal") {
found = true
}
}
if !found {
t.Errorf("expected path-traversal violation message; got: %v", report.Violations)
}
}

func TestValidateSuppressions_RejectsExpiryExceedingMax(t *testing.T) {
policy := SuppressionPolicyConfig{
AllowGlobalScope: true,
MaxExpiryDays:    30,
}
rec := &SuppressionRecord{
Fingerprint: "fp1",
Scope:       ScopeGlobal,
Reason:      "test",
Owner:       "team",
CreatedAt:   time.Now().UTC(),
ExpiresAt:   time.Now().UTC().Add(365 * 24 * time.Hour), // 365 days > 30 day max
}
report := ValidateSuppressions([]*SuppressionRecord{rec}, policy, nil)
if !report.HasErrors() {
t.Error("expected violation for expiry exceeding max")
}
}

func TestValidateSuppressions_RequiresExpiryWhenMaxSet(t *testing.T) {
policy := SuppressionPolicyConfig{
AllowGlobalScope: true,
MaxExpiryDays:    30,
}
rec := &SuppressionRecord{
Fingerprint: "fp1",
Scope:       ScopeGlobal,
Reason:      "test",
Owner:       "team",
CreatedAt:   time.Now().UTC(),
// ExpiresAt is zero — no expiry set.
}
report := ValidateSuppressions([]*SuppressionRecord{rec}, policy, nil)
if !report.HasErrors() {
t.Error("expected violation when max_expiry_days set but no expiry on record")
}
}

func TestValidateSuppressions_RequiresReviewer(t *testing.T) {
policy := SuppressionPolicyConfig{
AllowGlobalScope: true,
RequireReviewer:  true,
}
rec := &SuppressionRecord{
Fingerprint: "fp1",
Scope:       ScopeGlobal,
Reason:      "test",
Owner:       "team",
CreatedAt:   time.Now().UTC(),
// Reviewer is empty.
}
report := ValidateSuppressions([]*SuppressionRecord{rec}, policy, nil)
if !report.HasErrors() {
t.Error("expected violation for missing reviewer")
}
found := false
for _, v := range report.Violations {
if strings.Contains(v.Reason, "reviewer") {
found = true
}
}
if !found {
t.Errorf("expected reviewer violation; got: %v", report.Violations)
}
}

func TestValidateSuppressions_RequiresApprovalMetadata(t *testing.T) {
policy := SuppressionPolicyConfig{
AllowGlobalScope:        true,
RequireApprovalMetadata: true,
}
rec := &SuppressionRecord{
Fingerprint: "fp1",
Scope:       ScopeGlobal,
Reason:      "test",
Owner:       "team",
CreatedAt:   time.Now().UTC(),
Reviewer:    "alice",
// Signature is empty.
}
report := ValidateSuppressions([]*SuppressionRecord{rec}, policy, nil)
if !report.HasErrors() {
t.Error("expected violation for missing signature when approval metadata required")
}
}

func TestValidateSuppressions_RejectsUnknownRuleID(t *testing.T) {
policy := SuppressionPolicyConfig{
AllowGlobalScope: true,
KnownRuleIDs:     []string{"known-fp"},
}
rec := &SuppressionRecord{
Fingerprint: "unknown-fp",
Scope:       ScopeGlobal,
Reason:      "test",
Owner:       "team",
CreatedAt:   time.Now().UTC(),
}
report := ValidateSuppressions([]*SuppressionRecord{rec}, policy, nil)
if !report.HasErrors() {
t.Error("expected violation for unknown rule ID")
}
found := false
for _, v := range report.Violations {
if strings.Contains(v.Reason, "known rule ID") {
found = true
}
}
if !found {
t.Errorf("expected unknown-rule-id violation; got: %v", report.Violations)
}
}

// ── expired suppressions ────────────────────────────────────────────────────

func TestValidateSuppressions_ReportsExpiredSeparately(t *testing.T) {
policy := SuppressionPolicyConfig{AllowGlobalScope: true}
expired := &SuppressionRecord{
Fingerprint: "fp-expired",
Scope:       ScopeGlobal,
Reason:      "test",
Owner:       "team",
CreatedAt:   time.Now().UTC().Add(-48 * time.Hour),
ExpiresAt:   time.Now().UTC().Add(-1 * time.Hour),
}
report := ValidateSuppressions([]*SuppressionRecord{expired}, policy, nil)
// Expired records are not policy violations — they are surfaced separately.
if report.HasErrors() {
t.Errorf("expired records should not be violations; got: %v", report.Violations)
}
if len(report.Expired) != 1 {
t.Errorf("expected 1 expired record, got %d", len(report.Expired))
}
if report.Expired[0].Fingerprint != "fp-expired" {
t.Error("wrong expired fingerprint")
}
}

func TestValidateSuppressions_ExpiredDoesNotHideFindings(t *testing.T) {
// Expired suppressions should not appear in GetActive and therefore must
// not suppress any findings.
registry := NewSuppressionRegistry()
expired := &SuppressionRecord{
Fingerprint: "fp-expired",
Scope:       ScopeGlobal,
Reason:      "test",
Owner:       "team",
CreatedAt:   time.Now().UTC().Add(-48 * time.Hour),
ExpiresAt:   time.Now().UTC().Add(-1 * time.Hour),
}
// Add bypasses Validate (which rejects past-expiry); insert directly.
registry.records = append(registry.records, expired)

findings := []Finding{
{Type: FindingVerifiedRisk, Severity: SeverityHigh, Title: "Overflow", Evidence: "fp-expired"},
}
active, suppressed := registry.ApplyToFindings(findings, "")
if len(active) != 1 {
t.Errorf("expired suppression should not hide findings; active=%d", len(active))
}
if len(suppressed) != 0 {
t.Errorf("expired suppression should not suppress findings; suppressed=%d", len(suppressed))
}
}

// ── unused suppressions ─────────────────────────────────────────────────────

func TestValidateSuppressions_ReportsUnusedSuppressions(t *testing.T) {
policy := SuppressionPolicyConfig{AllowGlobalScope: true}
now := time.Now().UTC()
rec := &SuppressionRecord{
Fingerprint: "fp-unused",
Scope:       ScopeGlobal,
Reason:      "test",
Owner:       "team",
CreatedAt:   now,
ExpiresAt:   now.Add(24 * time.Hour),
}
// Active findings contain a different fingerprint.
activeFingerprints := map[string]struct{}{"fp-active": {}}
report := ValidateSuppressions([]*SuppressionRecord{rec}, policy, activeFingerprints)
if len(report.Unused) == 0 {
t.Error("expected unused suppression to be reported")
}
if report.Unused[0] != "fp-unused" {
t.Errorf("expected fp-unused in unused list; got: %v", report.Unused)
}
}

// ── format ─────────────────────────────────────────────────────────────────

func TestAuditReportFormatText(t *testing.T) {
now := time.Now().UTC()
report := SuppressionAuditReport{
Violations: []SuppressionViolation{
{RecordIndex: 0, Fingerprint: "fp1", Reason: "global scope not permitted"},
},
Expired: []*SuppressionRecord{
{Fingerprint: "fp2", ExpiresAt: now.Add(-time.Hour)},
},
Unused: []string{"fp3"},
}

text := report.FormatText()
if !strings.Contains(text, "global scope not permitted") {
t.Errorf("expected violation reason in output; got: %s", text)
}
if !strings.Contains(text, "fp2") {
t.Errorf("expected expired fingerprint in output; got: %s", text)
}
if !strings.Contains(text, "fp3") {
t.Errorf("expected unused fingerprint in output; got: %s", text)
}
}

// ── load/save roundtrip ─────────────────────────────────────────────────────

func TestLoadSaveSuppressionFile_Roundtrip(t *testing.T) {
dir := t.TempDir()
path := filepath.Join(dir, "suppressions.json")

now := time.Now().UTC().Truncate(time.Second)
records := []*SuppressionRecord{
{
Fingerprint: "fp-roundtrip",
Scope:       ScopeContract,
ScopeValue:  "contract-abc",
Reason:      "false positive",
Owner:       "team",
CreatedAt:   now,
ExpiresAt:   now.Add(24 * time.Hour),
},
}

if err := SaveSuppressionFile(path, records); err != nil {
t.Fatalf("SaveSuppressionFile: %v", err)
}

loaded, err := LoadSuppressionFile(path)
if err != nil {
t.Fatalf("LoadSuppressionFile: %v", err)
}
if len(loaded) != 1 {
t.Fatalf("expected 1 record, got %d", len(loaded))
}
if loaded[0].Fingerprint != "fp-roundtrip" {
t.Errorf("fingerprint mismatch: %s", loaded[0].Fingerprint)
}
if loaded[0].ScopeValue != "contract-abc" {
t.Errorf("scope_value mismatch: %s", loaded[0].ScopeValue)
}
}

func TestLoadSuppressionFile_InvalidJSON(t *testing.T) {
dir := t.TempDir()
path := filepath.Join(dir, "bad.json")
if err := os.WriteFile(path, []byte("{bad json"), 0o644); err != nil {
t.Fatal(err)
}
if _, err := LoadSuppressionFile(path); err == nil {
t.Error("expected error for invalid JSON")
}
}

func TestSuppressionViolationError(t *testing.T) {
v := SuppressionViolation{RecordIndex: 2, Fingerprint: "fp1", Reason: "global scope not permitted"}
msg := v.Error()
if !strings.Contains(msg, "fp1") || !strings.Contains(msg, "global scope") {
t.Errorf("unexpected error message: %s", msg)
}
}

// ── policy output in audit is included in suppressed report ─────────────────

func TestDetectorReport_IncludesSuppressionDecision(t *testing.T) {
registry := NewSuppressionRegistry()
finding := Finding{
Type:     FindingHeuristicWarn,
Severity: SeverityHigh,
Title:    "Open Auth Pattern",
Evidence: "fn_admin",
}
fp := ComputeFinding(finding)

now := time.Now().UTC()
rec := &SuppressionRecord{
Fingerprint: fp,
Scope:       ScopeGlobal,
Reason:      "accepted risk Q1",
Owner:       "security-team",
CreatedAt:   now,
ExpiresAt:   now.Add(30 * 24 * time.Hour),
}
// Bypass Validate to allow global scope in test.
registry.records = append(registry.records, rec)

d := NewDetectorWithSuppression(registry, "")
d.findings = []Finding{finding}

result := d.GetFindingsWithSuppression()
if len(result.ActiveFindings) != 0 {
t.Errorf("expected finding to be suppressed; active=%d", len(result.ActiveFindings))
}
if len(result.SuppressedFindings) != 1 {
t.Fatalf("expected 1 suppressed finding, got %d", len(result.SuppressedFindings))
}
sf := result.SuppressedFindings[0]
if sf.Record == nil {
t.Fatal("suppressed finding should carry its suppression record")
}
if sf.Record.Reason != "accepted risk Q1" {
t.Errorf("expected reason 'accepted risk Q1'; got %q", sf.Record.Reason)
}

// Verify the formatter includes suppression info.
formatter := NewReportFormatter(true)
text := formatter.FormatDetectorReport(result)
if !strings.Contains(text, "accepted risk Q1") {
t.Errorf("report should mention suppression reason; got:\n%s", text)
}

// Verify JSON includes suppressed_findings.
jsonOut, err := formatter.FormatDetectorJSON(result)
if err != nil {
t.Fatalf("FormatDetectorJSON: %v", err)
}
var parsed map[string]interface{}
if err := json.Unmarshal([]byte(jsonOut), &parsed); err != nil {
t.Fatalf("invalid JSON: %v", err)
}
if _, ok := parsed["suppressed_findings"]; !ok {
t.Error("JSON output missing suppressed_findings key")
}
}