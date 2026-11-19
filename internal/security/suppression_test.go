// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package security

import (
	"testing"
	"time"
)

func TestSuppressionRecord_Validate(t *testing.T) {
	tests := []struct {
		name    string
		record  *SuppressionRecord
		wantErr bool
	}{
		{
			name: "valid global suppression",
			record: &SuppressionRecord{
				Fingerprint: "abc123",
				Scope:       ScopeGlobal,
				Reason:      "False positive in test fixture",
				Owner:       "security-team",
				CreatedAt:   time.Now().UTC(),
			},
			wantErr: false,
		},
		{
			name: "valid contract-scoped suppression",
			record: &SuppressionRecord{
				Fingerprint: "abc123",
				Scope:       ScopeContract,
				ScopeValue:  "contract123",
				Reason:      "Known issue in contract",
				Owner:       "security-team",
				CreatedAt:   time.Now().UTC(),
			},
			wantErr: false,
		},
		{
			name: "valid suppression with expiration",
			record: &SuppressionRecord{
				Fingerprint: "abc123",
				Scope:       ScopeGlobal,
				Reason:      "Temporary suppression",
				Owner:       "security-team",
				CreatedAt:   time.Now().UTC(),
				ExpiresAt:   time.Now().UTC().Add(24 * time.Hour),
			},
			wantErr: false,
		},
		{
			name: "valid signed suppression",
			record: &SuppressionRecord{
				Fingerprint: "abc123",
				Scope:       ScopeGlobal,
				Reason:      "Reviewed and approved",
				Owner:       "security-team",
				CreatedAt:   time.Now().UTC(),
				Signature:   "sig123",
				Reviewer:    "reviewer@example.com",
			},
			wantErr: false,
		},
		{
			name: "empty fingerprint",
			record: &SuppressionRecord{
				Fingerprint: "",
				Scope:       ScopeGlobal,
				Reason:      "Test",
				Owner:       "security-team",
				CreatedAt:   time.Now().UTC(),
			},
			wantErr: true,
		},
		{
			name: "empty reason",
			record: &SuppressionRecord{
				Fingerprint: "abc123",
				Scope:       ScopeGlobal,
				Reason:      "",
				Owner:       "security-team",
				CreatedAt:   time.Now().UTC(),
			},
			wantErr: true,
		},
		{
			name: "empty owner",
			record: &SuppressionRecord{
				Fingerprint: "abc123",
				Scope:       ScopeGlobal,
				Reason:      "Test",
				Owner:       "",
				CreatedAt:   time.Now().UTC(),
			},
			wantErr: true,
		},
		{
			name: "zero created_at",
			record: &SuppressionRecord{
				Fingerprint: "abc123",
				Scope:       ScopeGlobal,
				Reason:      "Test",
				Owner:       "security-team",
				CreatedAt:   time.Time{},
			},
			wantErr: true,
		},
		{
			name: "missing scope_value for contract scope",
			record: &SuppressionRecord{
				Fingerprint: "abc123",
				Scope:       ScopeContract,
				ScopeValue:  "",
				Reason:      "Test",
				Owner:       "security-team",
				CreatedAt:   time.Now().UTC(),
			},
			wantErr: true,
		},
		{
			name: "invalid scope",
			record: &SuppressionRecord{
				Fingerprint: "abc123",
				Scope:       "invalid",
				Reason:      "Test",
				Owner:       "security-team",
				CreatedAt:   time.Now().UTC(),
			},
			wantErr: true,
		},
		{
			name: "expired expiration date",
			record: &SuppressionRecord{
				Fingerprint: "abc123",
				Scope:       ScopeGlobal,
				Reason:      "Test",
				Owner:       "security-team",
				CreatedAt:   time.Now().UTC(),
				ExpiresAt:   time.Now().UTC().Add(-1 * time.Hour),
			},
			wantErr: true,
		},
		{
			name: "signature without reviewer",
			record: &SuppressionRecord{
				Fingerprint: "abc123",
				Scope:       ScopeGlobal,
				Reason:      "Test",
				Owner:       "security-team",
				CreatedAt:   time.Now().UTC(),
				Signature:   "sig123",
				Reviewer:    "",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.record.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("SuppressionRecord.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSuppressionRecord_IsActive(t *testing.T) {
	now := time.Now().UTC()

	tests := []struct {
		name string
		record *SuppressionRecord
		want bool
	}{
		{
			name: "no expiration",
			record: &SuppressionRecord{
				ExpiresAt: time.Time{},
			},
			want: true,
		},
		{
			name: "future expiration",
			record: &SuppressionRecord{
				ExpiresAt: now.Add(1 * time.Hour),
			},
			want: true,
		},
		{
			name: "past expiration",
			record: &SuppressionRecord{
				ExpiresAt: now.Add(-1 * time.Hour),
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.record.IsActive(); got != tt.want {
				t.Errorf("SuppressionRecord.IsActive() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSuppressionRecord_Matches(t *testing.T) {
	tests := []struct {
		name       string
		record     *SuppressionRecord
		fingerprint string
		scopeValue string
		want       bool
	}{
		{
			name: "global scope matches any scope value",
			record: &SuppressionRecord{
				Fingerprint: "abc123",
				Scope:       ScopeGlobal,
			},
			fingerprint: "abc123",
			scopeValue:  "anything",
			want:        true,
		},
		{
			name: "contract scope matches exact value",
			record: &SuppressionRecord{
				Fingerprint: "abc123",
				Scope:       ScopeContract,
				ScopeValue:  "contract123",
			},
			fingerprint: "abc123",
			scopeValue:  "contract123",
			want:        true,
		},
		{
			name: "contract scope does not match different value",
			record: &SuppressionRecord{
				Fingerprint: "abc123",
				Scope:       ScopeContract,
				ScopeValue:  "contract123",
			},
			fingerprint: "abc123",
			scopeValue:  "contract456",
			want:        false,
		},
		{
			name: "different fingerprint does not match",
			record: &SuppressionRecord{
				Fingerprint: "abc123",
				Scope:       ScopeGlobal,
			},
			fingerprint: "def456",
			scopeValue:  "anything",
			want:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.record.Matches(tt.fingerprint, tt.scopeValue); got != tt.want {
				t.Errorf("SuppressionRecord.Matches() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestComputeFinding(t *testing.T) {
	finding := Finding{
		Type:        FindingHeuristicWarn,
		Severity:    SeverityHigh,
		Title:       "Test Finding",
		Evidence:    "test evidence",
	}

	fingerprint := ComputeFinding(finding)
	if fingerprint == "" {
		t.Error("ComputeFinding returned empty string")
	}

	// Same finding should produce same fingerprint
	fingerprint2 := ComputeFinding(finding)
	if fingerprint != fingerprint2 {
		t.Error("ComputeFinding produced different fingerprints for same finding")
	}

	// Different finding should produce different fingerprint
	differentFinding := Finding{
		Type:        FindingVerifiedRisk,
		Severity:    SeverityMedium,
		Title:       "Different Finding",
		Evidence:    "different evidence",
	}
	fingerprint3 := ComputeFinding(differentFinding)
	if fingerprint == fingerprint3 {
		t.Error("ComputeFinding produced same fingerprint for different findings")
	}
}

func TestComputeSecretFingerprint(t *testing.T) {
	finding := SecretFinding{
		Type:     SecretTypeAPIKey,
		Location: "test/location",
		Context:  "test context",
	}

	fingerprint := ComputeSecretFingerprint(finding)
	if fingerprint == "" {
		t.Error("ComputeSecretFingerprint returned empty string")
	}

	// Same finding should produce same fingerprint
	fingerprint2 := ComputeSecretFingerprint(finding)
	if fingerprint != fingerprint2 {
		t.Error("ComputeSecretFingerprint produced different fingerprints for same finding")
	}

	// Different finding should produce different fingerprint
	differentFinding := SecretFinding{
		Type:     SecretTypeBearerToken,
		Location: "different/location",
		Context:  "different context",
	}
	fingerprint3 := ComputeSecretFingerprint(differentFinding)
	if fingerprint == fingerprint3 {
		t.Error("ComputeSecretFingerprint produced same fingerprint for different findings")
	}
}

func TestSuppressionRegistry_Add(t *testing.T) {
	registry := NewSuppressionRegistry()

	record := &SuppressionRecord{
		Fingerprint: "abc123",
		Scope:       ScopeGlobal,
		Reason:      "Test",
		Owner:       "security-team",
		CreatedAt:   time.Now().UTC(),
	}

	err := registry.Add(record)
	if err != nil {
		t.Errorf("Add() error = %v", err)
	}

	// Adding invalid record should fail
	invalidRecord := &SuppressionRecord{
		Fingerprint: "",
		Scope:       ScopeGlobal,
		Reason:      "Test",
		Owner:       "security-team",
		CreatedAt:   time.Now().UTC(),
	}

	err = registry.Add(invalidRecord)
	if err == nil {
		t.Error("Add() should return error for invalid record")
	}
}

func TestSuppressionRegistry_IsSuppressed(t *testing.T) {
	registry := NewSuppressionRegistry()

	record := &SuppressionRecord{
		Fingerprint: "abc123",
		Scope:       ScopeGlobal,
		Reason:      "Test",
		Owner:       "security-team",
		CreatedAt:   time.Now().UTC(),
	}
	registry.Add(record)

	// Matching fingerprint should be suppressed
	if !registry.IsSuppressed("abc123", "anything") {
		t.Error("IsSuppressed() should return true for matching fingerprint")
	}

	// Non-matching fingerprint should not be suppressed
	if registry.IsSuppressed("def456", "anything") {
		t.Error("IsSuppressed() should return false for non-matching fingerprint")
	}
}

func TestSuppressionRegistry_ApplyToFindings(t *testing.T) {
	registry := NewSuppressionRegistry()

	// Add suppression for a specific finding
	finding := Finding{
		Type:        FindingHeuristicWarn,
		Severity:    SeverityHigh,
		Title:       "Test Finding",
		Evidence:    "test evidence",
	}
	fingerprint := ComputeFinding(finding)

	record := &SuppressionRecord{
		Fingerprint: fingerprint,
		Scope:       ScopeGlobal,
		Reason:      "False positive",
		Owner:       "security-team",
		CreatedAt:   time.Now().UTC(),
	}
	registry.Add(record)

	// Create findings list
	findings := []Finding{
		finding,
		{
			Type:        FindingVerifiedRisk,
			Severity:    SeverityMedium,
			Title:       "Different Finding",
			Evidence:    "different evidence",
		},
	}

	active, suppressed := registry.ApplyToFindings(findings, "")

	if len(active) != 1 {
		t.Errorf("ApplyToFindings() returned %d active findings, want 1", len(active))
	}
	if len(suppressed) != 1 {
		t.Errorf("ApplyToFindings() returned %d suppressed findings, want 1", len(suppressed))
	}
	if suppressed[0].Fingerprint != fingerprint {
		t.Error("Suppressed finding has wrong fingerprint")
	}
}

func TestSuppressionRegistry_ApplyToSecretFindings(t *testing.T) {
	registry := NewSuppressionRegistry()

	// Add suppression for a specific secret finding
	finding := SecretFinding{
		Type:     SecretTypeAPIKey,
		Location: "test/location",
		Context:  "test context",
	}
	fingerprint := ComputeSecretFingerprint(finding)

	record := &SuppressionRecord{
		Fingerprint: fingerprint,
		Scope:       ScopeGlobal,
		Reason:      "Test fixture",
		Owner:       "security-team",
		CreatedAt:   time.Now().UTC(),
	}
	registry.Add(record)

	// Create findings list
	findings := []SecretFinding{
		finding,
		{
			Type:     SecretTypeBearerToken,
			Location: "different/location",
			Context:  "different context",
		},
	}

	active, suppressed := registry.ApplyToSecretFindings(findings, "")

	if len(active) != 1 {
		t.Errorf("ApplyToSecretFindings() returned %d active findings, want 1", len(active))
	}
	if len(suppressed) != 1 {
		t.Errorf("ApplyToSecretFindings() returned %d suppressed findings, want 1", len(suppressed))
	}
	if suppressed[0].Fingerprint != fingerprint {
		t.Error("Suppressed finding has wrong fingerprint")
	}
}

func TestSuppressionRegistry_CleanupExpired(t *testing.T) {
	registry := NewSuppressionRegistry()

	// Add active record
	activeRecord := &SuppressionRecord{
		Fingerprint: "abc123",
		Scope:       ScopeGlobal,
		Reason:      "Test",
		Owner:       "security-team",
		CreatedAt:   time.Now().UTC(),
		ExpiresAt:   time.Now().UTC().Add(1 * time.Hour),
	}
	registry.Add(activeRecord)

	// Add expired record
	expiredRecord := &SuppressionRecord{
		Fingerprint: "def456",
		Scope:       ScopeGlobal,
		Reason:      "Test",
		Owner:       "security-team",
		CreatedAt:   time.Now().UTC(),
		ExpiresAt:   time.Now().UTC().Add(-1 * time.Hour),
	}
	registry.Add(expiredRecord)

	// Cleanup should remove expired records
	count := registry.CleanupExpired()
	if count != 1 {
		t.Errorf("CleanupExpired() removed %d records, want 1", count)
	}

	// Expired record should no longer be active
	activeRecords := registry.GetActive()
	if len(activeRecords) != 1 {
		t.Errorf("GetActive() returned %d records, want 1", len(activeRecords))
	}
}

func TestSuppressionRegistry_ToJSON(t *testing.T) {
	registry := NewSuppressionRegistry()

	record := &SuppressionRecord{
		Fingerprint: "abc123",
		Scope:       ScopeGlobal,
		Reason:      "Test",
		Owner:       "security-team",
		CreatedAt:   time.Now().UTC(),
	}
	registry.Add(record)

	jsonStr, err := registry.ToJSON()
	if err != nil {
		t.Errorf("ToJSON() error = %v", err)
	}
	if jsonStr == "" {
		t.Error("ToJSON() returned empty string")
	}
}

func TestNormalizeScopeValue(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{
			name:  "lowercase",
			value: "CONTRACT123",
			want:  "contract123",
		},
		{
			name:  "trim spaces",
			value: "  contract123  ",
			want:  "contract123",
		},
		{
			name:  "lowercase and trim",
			value: "  CONTRACT123  ",
			want:  "contract123",
		},
		{
			name:  "already normalized",
			value: "contract123",
			want:  "contract123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeScopeValue(tt.value); got != tt.want {
				t.Errorf("NormalizeScopeValue() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReportFormatter(t *testing.T) {
	formatter := NewReportFormatter(true)

	// Test detector report formatting
	detectorResult := DetectorResultWithSuppression{
		ActiveFindings: []Finding{
			{
				Type:        FindingHeuristicWarn,
				Severity:    SeverityHigh,
				Title:       "Active Finding",
				Description: "This is an active finding",
				Evidence:    "evidence123",
			},
		},
		SuppressedFindings: []SuppressedFinding{
			{
				Finding: Finding{
					Type:        FindingVerifiedRisk,
					Severity:    SeverityMedium,
					Title:       "Suppressed Finding",
					Description: "This is suppressed",
					Evidence:    "evidence456",
				},
				Record: &SuppressionRecord{
					Fingerprint: "abc123",
					Scope:       ScopeGlobal,
					Reason:      "False positive",
					Owner:       "security-team",
					CreatedAt:   time.Now().UTC(),
				},
				Fingerprint: "abc123",
			},
		},
	}

	report := formatter.FormatDetectorReport(detectorResult)
	if report == "" {
		t.Error("FormatDetectorReport() returned empty string")
	}
	if !contains(report, "Active Finding") {
		t.Error("Report missing active finding")
	}
	if !contains(report, "Suppressed Finding") {
		t.Error("Report missing suppressed finding")
	}
	if !contains(report, "False positive") {
		t.Error("Report missing suppression reason")
	}
	if !contains(report, "security-team") {
		t.Error("Report missing owner")
	}

	// Test secret report formatting
	secretResult := ScanResultWithSuppression{
		ActiveFindings: []SecretFinding{
			{
				Type:     SecretTypeAPIKey,
				Location: "test/location",
				Context:  "test context",
			},
		},
		SuppressedFindings: []SuppressedSecretFinding{
			{
				Finding: SecretFinding{
					Type:     SecretTypeBearerToken,
					Location: "different/location",
					Context:  "different context",
				},
				Record: &SuppressionRecord{
					Fingerprint: "def456",
					Scope:       ScopeGlobal,
					Reason:      "Test fixture",
					Owner:       "security-team",
					CreatedAt:   time.Now().UTC(),
				},
				Fingerprint: "def456",
			},
		},
		HasSecrets: true,
	}

	report = formatter.FormatSecretReport(secretResult)
	if report == "" {
		t.Error("FormatSecretReport() returned empty string")
	}
	if !contains(report, "API_KEY") {
		t.Error("Report missing active secret type")
	}
	if !contains(report, "BEARER_TOKEN") {
		t.Error("Report missing suppressed secret type")
	}

	// Test JSON formatting
	jsonReport, err := formatter.FormatDetectorJSON(detectorResult)
	if err != nil {
		t.Errorf("FormatDetectorJSON() error = %v", err)
	}
	if jsonReport == "" {
		t.Error("FormatDetectorJSON() returned empty string")
	}
}
