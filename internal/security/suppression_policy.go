// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package security

import (
"encoding/json"
"fmt"
"os"
"path/filepath"
"strings"
"time"
)

// SuppressionPolicyConfig holds the full policy governing suppression file
// validation. It is separate from individual SuppressionRecord validation so
// that policy thresholds can be changed without touching scanner logic.
type SuppressionPolicyConfig struct {
// AllowedScopes is the set of SuppressionScope values that are permitted.
// An empty slice means all scopes are allowed.
AllowedScopes []SuppressionScope `json:"allowed_scopes,omitempty"`

// MaxExpiryDays is the maximum number of days a suppression may be valid.
// Zero means no limit. When set, suppressions without an ExpiresAt are
// rejected.
MaxExpiryDays int `json:"max_expiry_days,omitempty"`

// RequireReviewer, when true, requires every suppression to carry a
// Reviewer field.
RequireReviewer bool `json:"require_reviewer"`

// RequireApprovalMetadata, when true, requires both Reviewer + Signature.
RequireApprovalMetadata bool `json:"require_approval_metadata"`

// KnownRuleIDs is the set of valid finding fingerprints. When non-empty,
// any suppression whose Fingerprint is not in this set is rejected.
KnownRuleIDs []string `json:"known_rule_ids,omitempty"`

// AllowGlobalScope, when false, rejects suppressions with ScopeGlobal.
AllowGlobalScope bool `json:"allow_global_scope"`
}

// DefaultSuppressionPolicy returns a policy that is secure by default:
// global scope is disallowed, reviewer is required, max expiry is 90 days.
func DefaultSuppressionPolicy() SuppressionPolicyConfig {
return SuppressionPolicyConfig{
AllowGlobalScope:        false,
RequireReviewer:         true,
RequireApprovalMetadata: false,
MaxExpiryDays:           90,
}
}

// SuppressionViolation describes a single policy violation found during
// validation.
type SuppressionViolation struct {
// RecordIndex is the zero-based index of the offending record.
RecordIndex int
// Fingerprint identifies the offending record (may be empty for malformed).
Fingerprint string
// Reason is a human-readable description of the violation.
Reason string
}

func (v SuppressionViolation) Error() string {
if v.Fingerprint != "" {
return fmt.Sprintf("suppression[%d] %q: %s", v.RecordIndex, v.Fingerprint, v.Reason)
}
return fmt.Sprintf("suppression[%d]: %s", v.RecordIndex, v.Reason)
}

// SuppressionAuditReport is the result of running ValidateSuppressions.
type SuppressionAuditReport struct {
// Violations are records that failed policy validation.
Violations []SuppressionViolation
// Expired holds records that have passed their ExpiresAt time. They are
// reported separately so they cannot silently hide findings.
Expired []*SuppressionRecord
// Unused holds fingerprints present in the suppression file but not
// matched against any active finding during the audit run.
Unused []string
}

// HasErrors reports whether the audit found any blocking violations.
func (r *SuppressionAuditReport) HasErrors() bool {
return len(r.Violations) > 0
}

// FormatText returns a human-readable summary of the audit report.
func (r *SuppressionAuditReport) FormatText() string {
var sb strings.Builder
sb.WriteString("Suppression Audit Report\n")
sb.WriteString("========================\n\n")

if len(r.Violations) == 0 {
sb.WriteString("Violations: none\n")
} else {
sb.WriteString(fmt.Sprintf("Violations: %d\n", len(r.Violations)))
for _, v := range r.Violations {
sb.WriteString("  ERROR: " + v.Error() + "\n")
}
}

if len(r.Expired) == 0 {
sb.WriteString("\nExpired suppressions: none\n")
} else {
sb.WriteString(fmt.Sprintf("\nExpired suppressions: %d\n", len(r.Expired)))
for _, e := range r.Expired {
sb.WriteString(fmt.Sprintf("  EXPIRED: %q (expired %s)\n",
e.Fingerprint, e.ExpiresAt.UTC().Format(time.RFC3339)))
}
}

if len(r.Unused) == 0 {
sb.WriteString("\nUnused suppressions: none\n")
} else {
sb.WriteString(fmt.Sprintf("\nUnused suppressions: %d\n", len(r.Unused)))
for _, u := range r.Unused {
sb.WriteString(fmt.Sprintf("  UNUSED: %q\n", u))
}
}

return sb.String()
}

// validateStructural performs the subset of SuppressionRecord.Validate checks
// that do NOT include the expiry-in-future constraint. Expiry is handled
// separately by the policy layer so expired records can be reported in the
// Expired bucket rather than as violations.
func validateStructural(rec *SuppressionRecord) error {
if rec.Fingerprint == "" {
return fmt.Errorf("fingerprint cannot be empty")
}
if rec.Reason == "" {
return fmt.Errorf("reason cannot be empty")
}
if rec.Owner == "" {
return fmt.Errorf("owner cannot be empty")
}
if rec.CreatedAt.IsZero() {
return fmt.Errorf("created_at cannot be zero")
}
switch rec.Scope {
case ScopeContract, ScopePath, ScopeTransaction:
if rec.ScopeValue == "" {
return fmt.Errorf("scope_value is required for scope %s", rec.Scope)
}
case ScopeGlobal:
// scope_value is optional for global
default:
return fmt.Errorf("invalid scope: %s", rec.Scope)
}
if rec.Signature != "" && rec.Reviewer == "" {
return fmt.Errorf("reviewer is required when signature is present")
}
return nil
}

// ValidateSuppressions checks every record in records against policy and
// reports violations, expired entries, and unused fingerprints.
// activeFingerprints is the set of finding fingerprints present in the
// current scan; pass nil to skip the unused-suppression check.
func ValidateSuppressions(
records []*SuppressionRecord,
policy SuppressionPolicyConfig,
activeFingerprints map[string]struct{},
) SuppressionAuditReport {
var report SuppressionAuditReport
now := time.Now().UTC()

knownSet := make(map[string]struct{}, len(policy.KnownRuleIDs))
for _, id := range policy.KnownRuleIDs {
knownSet[id] = struct{}{}
}

allowedScopeSet := make(map[SuppressionScope]struct{}, len(policy.AllowedScopes))
for _, s := range policy.AllowedScopes {
allowedScopeSet[s] = struct{}{}
}

// Track fingerprints that exist (for unused detection). We count expired
// and valid records alike; only missing records are flagged unused.
matchedFingerprints := make(map[string]bool, len(records))

for i, rec := range records {
idx := i

// Structural validation (excludes expiry-future check).
if err := validateStructural(rec); err != nil {
report.Violations = append(report.Violations, SuppressionViolation{
RecordIndex: idx,
Fingerprint: rec.Fingerprint,
Reason:      "structural validation failed: " + err.Error(),
})
continue
}

// Expired records are reported separately, not as violations.
if !rec.ExpiresAt.IsZero() && rec.ExpiresAt.Before(now) {
report.Expired = append(report.Expired, rec)
matchedFingerprints[rec.Fingerprint] = true
continue
}

// Path traversal guard on ScopeValue.
if rec.ScopeValue != "" {
clean := filepath.Clean(rec.ScopeValue)
if strings.Contains(clean, "..") {
report.Violations = append(report.Violations, SuppressionViolation{
RecordIndex: idx,
Fingerprint: rec.Fingerprint,
Reason:      "scope_value contains path traversal sequence",
})
continue
}
}

// Global scope check.
if rec.Scope == ScopeGlobal && !policy.AllowGlobalScope {
report.Violations = append(report.Violations, SuppressionViolation{
RecordIndex: idx,
Fingerprint: rec.Fingerprint,
Reason:      "global scope is not permitted by policy; use contract, path, or transaction scope",
})
continue
}

// Allowed scopes check.
if len(allowedScopeSet) > 0 {
if _, ok := allowedScopeSet[rec.Scope]; !ok {
report.Violations = append(report.Violations, SuppressionViolation{
RecordIndex: idx,
Fingerprint: rec.Fingerprint,
Reason:      fmt.Sprintf("scope %q is not in the allowed scope list", rec.Scope),
})
continue
}
}

// Max expiry check.
if policy.MaxExpiryDays > 0 {
maxExpiry := rec.CreatedAt.Add(time.Duration(policy.MaxExpiryDays) * 24 * time.Hour)
if rec.ExpiresAt.IsZero() {
report.Violations = append(report.Violations, SuppressionViolation{
RecordIndex: idx,
Fingerprint: rec.Fingerprint,
Reason: fmt.Sprintf("suppression has no expiry; policy requires expiry within %d days",
policy.MaxExpiryDays),
})
continue
}
if rec.ExpiresAt.After(maxExpiry) {
report.Violations = append(report.Violations, SuppressionViolation{
RecordIndex: idx,
Fingerprint: rec.Fingerprint,
Reason: fmt.Sprintf("expiry %s exceeds max allowed expiry of %s (%d days from creation)",
rec.ExpiresAt.UTC().Format(time.RFC3339),
maxExpiry.UTC().Format(time.RFC3339),
policy.MaxExpiryDays),
})
continue
}
}

// Reviewer requirement.
if policy.RequireReviewer && rec.Reviewer == "" {
report.Violations = append(report.Violations, SuppressionViolation{
RecordIndex: idx,
Fingerprint: rec.Fingerprint,
Reason:      "policy requires a reviewer field",
})
continue
}

// Approval metadata (signature + reviewer).
if policy.RequireApprovalMetadata && (rec.Signature == "" || rec.Reviewer == "") {
report.Violations = append(report.Violations, SuppressionViolation{
RecordIndex: idx,
Fingerprint: rec.Fingerprint,
Reason:      "policy requires both signature and reviewer (approval metadata)",
})
continue
}

// Known rule ID check.
if len(knownSet) > 0 {
if _, known := knownSet[rec.Fingerprint]; !known {
report.Violations = append(report.Violations, SuppressionViolation{
RecordIndex: idx,
Fingerprint: rec.Fingerprint,
Reason:      "fingerprint is not in the known rule ID list",
})
continue
}
}

matchedFingerprints[rec.Fingerprint] = true
}

// Unused suppression detection.
if activeFingerprints != nil {
for fp := range matchedFingerprints {
if _, active := activeFingerprints[fp]; !active {
report.Unused = append(report.Unused, fp)
}
}
}

return report
}

// LoadSuppressionFile reads a JSON suppression file and returns its records.
// No policy validation is applied here; call ValidateSuppressions separately.
func LoadSuppressionFile(path string) ([]*SuppressionRecord, error) {
data, err := os.ReadFile(path)
if err != nil {
return nil, fmt.Errorf("reading suppression file %q: %w", path, err)
}
var records []*SuppressionRecord
if err := json.Unmarshal(data, &records); err != nil {
return nil, fmt.Errorf("parsing suppression file %q: %w", path, err)
}
return records, nil
}

// SaveSuppressionFile writes records to a JSON file.
func SaveSuppressionFile(path string, records []*SuppressionRecord) error {
data, err := json.MarshalIndent(records, "", "  ")
if err != nil {
return fmt.Errorf("marshalling suppression records: %w", err)
}
if err := os.WriteFile(path, data, 0o644); err != nil {
return fmt.Errorf("writing suppression file %q: %w", path, err)
}
return nil
}