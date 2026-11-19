// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package security

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// SuppressionScope defines where a suppression record applies.
type SuppressionScope string

const (
	// ScopeGlobal applies to all findings matching the fingerprint.
	ScopeGlobal SuppressionScope = "global"
	// ScopeContract applies only to findings for a specific contract.
	ScopeContract SuppressionScope = "contract"
	// ScopePath applies only to findings at a specific file path.
	ScopePath SuppressionScope = "path"
	// ScopeTransaction applies only to findings for a specific transaction.
	ScopeTransaction SuppressionScope = "transaction"
)

// SuppressionRecord represents a controlled suppression of a security finding.
type SuppressionRecord struct {
	// Fingerprint uniquely identifies the finding to suppress.
	Fingerprint string `json:"fingerprint"`
	
	// Scope defines where this suppression applies.
	Scope SuppressionScope `json:"scope"`
	
	// ScopeValue is the contract ID, path, or transaction hash for scoped suppressions.
	ScopeValue string `json:"scope_value,omitempty"`
	
	// Reason explains why this finding is suppressed.
	Reason string `json:"reason"`
	
	// Owner is the person or team who approved this suppression.
	Owner string `json:"owner"`
	
	// ExpiresAt is when this suppression record expires (zero for no expiration).
	ExpiresAt time.Time `json:"expires_at"`
	
	// CreatedAt is when this suppression record was created.
	CreatedAt time.Time `json:"created_at"`
	
	// Signature is an optional signature for reviewed suppressions.
	Signature string `json:"signature,omitempty"`
	
	// Reviewer is the person who reviewed this suppression (if signed).
	Reviewer string `json:"reviewer,omitempty"`
}

// IsActive returns true if the suppression record is currently active.
func (s *SuppressionRecord) IsActive() bool {
	if s.ExpiresAt.IsZero() {
		return true // No expiration
	}
	return time.Now().UTC().Before(s.ExpiresAt)
}

// Matches checks if this suppression record matches a finding.
func (s *SuppressionRecord) Matches(fingerprint, scopeValue string) bool {
	if s.Fingerprint != fingerprint {
		return false
	}
	
	switch s.Scope {
	case ScopeGlobal:
		return true
	case ScopeContract, ScopePath, ScopeTransaction:
		return s.ScopeValue == scopeValue
	default:
		return false
	}
}

// Validate checks if the suppression record is valid.
func (s *SuppressionRecord) Validate() error {
	if s.Fingerprint == "" {
		return fmt.Errorf("fingerprint cannot be empty")
	}
	if s.Reason == "" {
		return fmt.Errorf("reason cannot be empty")
	}
	if s.Owner == "" {
		return fmt.Errorf("owner cannot be empty")
	}
	if s.CreatedAt.IsZero() {
		return fmt.Errorf("created_at cannot be zero")
	}
	
	// Validate scope-specific requirements
	switch s.Scope {
	case ScopeContract, ScopePath, ScopeTransaction:
		if s.ScopeValue == "" {
			return fmt.Errorf("scope_value is required for scope %s", s.Scope)
		}
	case ScopeGlobal:
		// scope_value is optional for global
	default:
		return fmt.Errorf("invalid scope: %s", s.Scope)
	}
	
	// Validate expiration is in the future (if set)
	if !s.ExpiresAt.IsZero() && s.ExpiresAt.Before(time.Now().UTC()) {
		return fmt.Errorf("expires_at must be in the future")
	}
	
	// If signature is present, reviewer must also be present
	if s.Signature != "" && s.Reviewer == "" {
		return fmt.Errorf("reviewer is required when signature is present")
	}
	
	return nil
}

// ComputeFingerprint generates a unique fingerprint for a finding.
// The fingerprint is based on the finding's type, severity, title, and evidence.
func ComputeFinding(finding Finding) string {
	data := fmt.Sprintf("%s|%s|%s|%s", finding.Type, finding.Severity, finding.Title, finding.Evidence)
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}

// ComputeSecretFingerprint generates a unique fingerprint for a secret finding.
func ComputeSecretFingerprint(finding SecretFinding) string {
	data := fmt.Sprintf("%s|%s|%s", finding.Type, finding.Location, finding.Context)
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}

// SuppressionRegistry manages suppression records.
type SuppressionRegistry struct {
	records []*SuppressionRecord
}

// NewSuppressionRegistry creates a new suppression registry.
func NewSuppressionRegistry() *SuppressionRegistry {
	return &SuppressionRegistry{
		records: make([]*SuppressionRecord, 0),
	}
}

// Add adds a suppression record to the registry.
func (r *SuppressionRegistry) Add(record *SuppressionRecord) error {
	if err := record.Validate(); err != nil {
		return fmt.Errorf("invalid suppression record: %w", err)
	}
	r.records = append(r.records, record)
	return nil
}

// AddFromJSON adds suppression records from a JSON string.
func (r *SuppressionRegistry) AddFromJSON(jsonStr string) error {
	var records []*SuppressionRecord
	if err := json.Unmarshal([]byte(jsonStr), &records); err != nil {
		return fmt.Errorf("failed to unmarshal suppression records: %w", err)
	}
	
	for _, record := range records {
		if err := r.Add(record); err != nil {
			return fmt.Errorf("failed to add record: %w", err)
		}
	}
	return nil
}

// Remove removes a suppression record by fingerprint.
func (r *SuppressionRegistry) Remove(fingerprint string) {
	for i, record := range r.records {
		if record.Fingerprint == fingerprint {
			r.records = append(r.records[:i], r.records[i+1:]...)
			return
		}
	}
}

// GetActive returns all active suppression records.
func (r *SuppressionRegistry) GetActive() []*SuppressionRecord {
	active := make([]*SuppressionRecord, 0)
	for _, record := range r.records {
		if record.IsActive() {
			active = append(active, record)
		}
	}
	return active
}

// GetExpired returns all expired suppression records.
func (r *SuppressionRegistry) GetExpired() []*SuppressionRecord {
	expired := make([]*SuppressionRecord, 0)
	for _, record := range r.records {
		if !record.IsActive() {
			expired = append(expired, record)
		}
	}
	return expired
}

// IsSuppressed checks if a finding is suppressed.
func (r *SuppressionRegistry) IsSuppressed(fingerprint string, scopeValue string) bool {
	for _, record := range r.GetActive() {
		if record.Matches(fingerprint, scopeValue) {
			return true
		}
	}
	return false
}

// GetSuppressionRecord returns the suppression record for a finding, if any.
func (r *SuppressionRegistry) GetSuppressionRecord(fingerprint string, scopeValue string) *SuppressionRecord {
	for _, record := range r.GetActive() {
		if record.Matches(fingerprint, scopeValue) {
			return record
		}
	}
	return nil
}

// ApplyToFindings applies suppression to a list of findings, returning active and suppressed findings.
func (r *SuppressionRegistry) ApplyToFindings(findings []Finding, scopeValue string) ([]Finding, []SuppressedFinding) {
	active := make([]Finding, 0)
	suppressed := make([]SuppressedFinding, 0)
	
	for _, finding := range findings {
		fingerprint := ComputeFinding(finding)
		if r.IsSuppressed(fingerprint, scopeValue) {
			record := r.GetSuppressionRecord(fingerprint, scopeValue)
			suppressed = append(suppressed, SuppressedFinding{
				Finding:    finding,
				Record:     record,
				Fingerprint: fingerprint,
			})
		} else {
			active = append(active, finding)
		}
	}
	
	return active, suppressed
}

// ApplyToSecretFindings applies suppression to secret findings.
func (r *SuppressionRegistry) ApplyToSecretFindings(findings []SecretFinding, scopeValue string) ([]SecretFinding, []SuppressedSecretFinding) {
	active := make([]SecretFinding, 0)
	suppressed := make([]SuppressedSecretFinding, 0)
	
	for _, finding := range findings {
		fingerprint := ComputeSecretFingerprint(finding)
		if r.IsSuppressed(fingerprint, scopeValue) {
			record := r.GetSuppressionRecord(fingerprint, scopeValue)
			suppressed = append(suppressed, SuppressedSecretFinding{
				Finding:    finding,
				Record:     record,
				Fingerprint: fingerprint,
			})
		} else {
			active = append(active, finding)
		}
	}
	
	return active, suppressed
}

// CleanupExpired removes expired suppression records from the registry.
func (r *SuppressionRegistry) CleanupExpired() int {
	expired := r.GetExpired()
	count := len(expired)
	
	// Rebuild records without expired entries
	active := make([]*SuppressionRecord, 0)
	for _, record := range r.records {
		if record.IsActive() {
			active = append(active, record)
		}
	}
	r.records = active
	
	return count
}

// ToJSON exports the suppression registry to JSON.
func (r *SuppressionRegistry) ToJSON() (string, error) {
	data, err := json.MarshalIndent(r.records, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal suppression registry: %w", err)
	}
	return string(data), nil
}

// SuppressedFinding represents a finding that has been suppressed.
type SuppressedFinding struct {
	Finding    Finding
	Record     *SuppressionRecord
	Fingerprint string
}

// SuppressedSecretFinding represents a secret finding that has been suppressed.
type SuppressedSecretFinding struct {
	Finding    SecretFinding
	Record     *SuppressionRecord
	Fingerprint string
}

// ScanResultWithSuppression extends ScanResult with suppression information.
type ScanResultWithSuppression struct {
	ActiveFindings     []SecretFinding `json:"active_findings"`
	SuppressedFindings []SuppressedSecretFinding `json:"suppressed_findings"`
	HasSecrets         bool `json:"has_secrets"`
}

// DetectorResultWithSuppression extends detector results with suppression information.
type DetectorResultWithSuppression struct {
	ActiveFindings     []Finding `json:"active_findings"`
	SuppressedFindings []SuppressedFinding `json:"suppressed_findings"`
}

// NormalizeScopeValue normalizes a scope value for matching.
func NormalizeScopeValue(value string) string {
	return strings.TrimSpace(strings.ToLower(value))
}
