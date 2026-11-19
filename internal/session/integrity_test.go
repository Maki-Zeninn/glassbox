// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package session

import (
	"strings"
	"testing"
	"time"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// validData returns a fully populated Data record that passes all integrity
// checks. Individual tests mutate a copy to exercise specific failure paths.
func validData() *Data {
	now := time.Now()
	return &Data{
		ID:           "session-abc123",
		TxHash:       "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Network:      "testnet",
		Status:       "saved",
		CreatedAt:    now.Add(-10 * time.Minute),
		LastAccessAt: now,
		SchemaVersion: SchemaVersion,
	}
}

// ── ValidateIntegrity — happy path ────────────────────────────────────────────

func TestValidateIntegrity_ValidSession_OK(t *testing.T) {
	report := ValidateIntegrity(validData())
	if !report.OK {
		t.Errorf("expected OK=true for valid session, got issues: %v", report.Issues)
	}
	if len(report.Issues) != 0 {
		t.Errorf("expected zero issues for valid session, got %d: %v", len(report.Issues), report.Issues)
	}
	if !report.SchemaCompatible {
		t.Error("expected SchemaCompatible=true for current schema version")
	}
}

func TestValidateIntegrity_ReportsSessionID(t *testing.T) {
	d := validData()
	d.ID = "my-session"
	report := ValidateIntegrity(d)
	if report.SessionID != "my-session" {
		t.Errorf("report.SessionID = %q, want %q", report.SessionID, "my-session")
	}
}

// ── Required field validation ─────────────────────────────────────────────────

func TestValidateIntegrity_EmptyID_ReportsIssue(t *testing.T) {
	d := validData()
	d.ID = ""
	report := ValidateIntegrity(d)
	if report.OK {
		t.Fatal("expected OK=false when ID is empty")
	}
	requireIssueField(t, report, "ID")
}

func TestValidateIntegrity_EmptyTxHash_ReportsIssue(t *testing.T) {
	d := validData()
	d.TxHash = ""
	report := ValidateIntegrity(d)
	if report.OK {
		t.Fatal("expected OK=false when TxHash is empty")
	}
	requireIssueField(t, report, "TxHash")
}

func TestValidateIntegrity_EmptyNetwork_ReportsIssue(t *testing.T) {
	d := validData()
	d.Network = ""
	report := ValidateIntegrity(d)
	if report.OK {
		t.Fatal("expected OK=false when Network is empty")
	}
	requireIssueField(t, report, "Network")
}

func TestValidateIntegrity_InvalidNetwork_ReportsIssue(t *testing.T) {
	d := validData()
	d.Network = "devnet" // not a valid Stellar network
	report := ValidateIntegrity(d)
	if report.OK {
		t.Fatal("expected OK=false for unrecognised network")
	}
	requireIssueField(t, report, "Network")
	// Issue description should name the bad value.
	for _, issue := range report.Issues {
		if issue.Field == "Network" && !strings.Contains(issue.Description, "devnet") {
			t.Errorf("issue description should name the invalid value 'devnet', got: %q", issue.Description)
		}
	}
}

func TestValidateIntegrity_AllValidNetworks_OK(t *testing.T) {
	for _, net := range []string{"testnet", "mainnet", "futurenet"} {
		d := validData()
		d.Network = net
		report := ValidateIntegrity(d)
		for _, issue := range report.Issues {
			if issue.Field == "Network" {
				t.Errorf("valid network %q should not produce a Network issue, got: %v", net, issue)
			}
		}
	}
}

// ── Status validation ─────────────────────────────────────────────────────────

func TestValidateIntegrity_EmptyStatus_ReportsIssue(t *testing.T) {
	d := validData()
	d.Status = ""
	report := ValidateIntegrity(d)
	if report.OK {
		t.Fatal("expected OK=false when Status is empty")
	}
	requireIssueField(t, report, "Status")
}

func TestValidateIntegrity_UnknownStatus_ReportsIssue(t *testing.T) {
	d := validData()
	d.Status = "corrupted"
	report := ValidateIntegrity(d)
	if report.OK {
		t.Fatal("expected OK=false for unknown status")
	}
	requireIssueField(t, report, "Status")
}

func TestValidateIntegrity_AllValidStatuses_OK(t *testing.T) {
	for _, status := range []string{"active", "saved", "resumed", "recovered", "expired"} {
		d := validData()
		d.Status = status
		report := ValidateIntegrity(d)
		for _, issue := range report.Issues {
			if issue.Field == "Status" {
				t.Errorf("valid status %q should not produce a Status issue, got: %v", status, issue)
			}
		}
	}
}

// ── Timestamp validation ──────────────────────────────────────────────────────

func TestValidateIntegrity_ZeroCreatedAt_ReportsIssue(t *testing.T) {
	d := validData()
	d.CreatedAt = time.Time{}
	report := ValidateIntegrity(d)
	if report.OK {
		t.Fatal("expected OK=false when CreatedAt is zero")
	}
	requireIssueField(t, report, "CreatedAt")
}

func TestValidateIntegrity_ZeroLastAccessAt_ReportsIssue(t *testing.T) {
	d := validData()
	d.LastAccessAt = time.Time{}
	report := ValidateIntegrity(d)
	if report.OK {
		t.Fatal("expected OK=false when LastAccessAt is zero")
	}
	requireIssueField(t, report, "LastAccessAt")
}

func TestValidateIntegrity_LastAccessBeforeCreated_ReportsIssue(t *testing.T) {
	d := validData()
	// Flip: last access is before creation time.
	d.CreatedAt = time.Now()
	d.LastAccessAt = time.Now().Add(-5 * time.Minute)
	report := ValidateIntegrity(d)
	if report.OK {
		t.Fatal("expected OK=false when last_access_at is before created_at")
	}
	requireIssueField(t, report, "LastAccessAt")
}

func TestValidateIntegrity_LastAccessEqualsCreated_OK(t *testing.T) {
	d := validData()
	ts := time.Now()
	d.CreatedAt = ts
	d.LastAccessAt = ts // same moment is valid
	report := ValidateIntegrity(d)
	for _, issue := range report.Issues {
		if issue.Field == "LastAccessAt" {
			t.Errorf("equal timestamps should not produce a LastAccessAt issue, got: %v", issue)
		}
	}
}

// ── Schema version compatibility ──────────────────────────────────────────────

func TestValidateIntegrity_NewerSchema_IncompatibleAndIssue(t *testing.T) {
	d := validData()
	d.SchemaVersion = SchemaVersion + 99
	report := ValidateIntegrity(d)
	if report.SchemaCompatible {
		t.Error("expected SchemaCompatible=false for a newer schema version")
	}
	if report.OK {
		t.Fatal("expected OK=false for incompatible schema version")
	}
	requireIssueField(t, report, "SchemaVersion")
}

func TestValidateIntegrity_OlderSchema_Compatible(t *testing.T) {
	d := validData()
	if SchemaVersion > 1 {
		d.SchemaVersion = SchemaVersion - 1
		report := ValidateIntegrity(d)
		if !report.SchemaCompatible {
			t.Error("expected SchemaCompatible=true for an older (still supported) schema version")
		}
	}
}

func TestValidateIntegrity_CurrentSchema_Compatible(t *testing.T) {
	d := validData()
	d.SchemaVersion = SchemaVersion
	report := ValidateIntegrity(d)
	if !report.SchemaCompatible {
		t.Error("expected SchemaCompatible=true for current schema version")
	}
}

// ── Consistency: SimRequestJSON implies EnvelopeXdr ───────────────────────────

func TestValidateIntegrity_SimRequestWithoutEnvelope_ReportsIssue(t *testing.T) {
	d := validData()
	d.SimRequestJSON = `{"envelope_xdr":"abc"}`
	d.EnvelopeXdr = "" // missing envelope
	report := ValidateIntegrity(d)
	if report.OK {
		t.Fatal("expected OK=false when SimRequestJSON is set but EnvelopeXdr is empty")
	}
	requireIssueField(t, report, "EnvelopeXdr")
}

func TestValidateIntegrity_SimRequestWithEnvelope_OK(t *testing.T) {
	d := validData()
	d.SimRequestJSON = `{"envelope_xdr":"abc"}`
	d.EnvelopeXdr = "base64-xdr-data"
	report := ValidateIntegrity(d)
	for _, issue := range report.Issues {
		if issue.Field == "EnvelopeXdr" {
			t.Errorf("should not issue EnvelopeXdr problem when both fields are set, got: %v", issue)
		}
	}
}

// ── Audit chain integrity ─────────────────────────────────────────────────────

func TestValidateIntegrity_NilData_ReportsIssue(t *testing.T) {
	report := ValidateIntegrity(nil)
	if report == nil {
		t.Fatal("expected non-nil report for nil data")
	}
	if report.OK {
		t.Fatal("expected OK=false for nil data")
	}
	requireIssueField(t, report, "Session")
}

func TestValidateIntegrity_AuditChainComplete_OK(t *testing.T) {
	d := validData()
	d.AuditHash = strings.Repeat("a", 64)
	d.AuditSignature = strings.Repeat("b", 128)
	d.PreviousSessionHash = strings.Repeat("c", 64)

	report := ValidateIntegrity(d)
	for _, field := range []string{"AuditHash", "AuditSignature", "PreviousSessionHash"} {
		for _, issue := range report.Issues {
			if issue.Field == field {
				t.Fatalf("valid audit chain should not produce %s issue: %+v", field, issue)
			}
		}
	}
}

func TestValidateIntegrity_AuditHashWithoutSignature_ReportsIssue(t *testing.T) {
	d := validData()
	d.AuditHash = strings.Repeat("a", 64)

	report := ValidateIntegrity(d)
	if report.OK {
		t.Fatal("expected OK=false when audit_hash is present without audit_signature")
	}
	requireIssueField(t, report, "AuditSignature")
}

func TestValidateIntegrity_AuditSignatureWithoutHash_ReportsIssue(t *testing.T) {
	d := validData()
	d.AuditSignature = strings.Repeat("b", 128)

	report := ValidateIntegrity(d)
	if report.OK {
		t.Fatal("expected OK=false when audit_signature is present without audit_hash")
	}
	requireIssueField(t, report, "AuditHash")
}

func TestValidateIntegrity_AuditSignatureMalformed_ReportsIssue(t *testing.T) {
	d := validData()
	d.AuditHash = strings.Repeat("a", 64)
	d.AuditSignature = "zz"

	report := ValidateIntegrity(d)
	if report.OK {
		t.Fatal("expected OK=false for malformed audit_signature")
	}
	requireIssueField(t, report, "AuditSignature")
}

func TestValidateIntegrity_PreviousSessionHashWithoutAuditHash_ReportsIssue(t *testing.T) {
	d := validData()
	d.PreviousSessionHash = strings.Repeat("c", 64)

	report := ValidateIntegrity(d)
	if report.OK {
		t.Fatal("expected OK=false when previous_session_hash is present without audit_hash")
	}
	requireIssueField(t, report, "AuditHash")
	requireIssueField(t, report, "AuditSignature")
}

func TestValidateIntegrity_PreviousSessionHashSelfReference_ReportsIssue(t *testing.T) {
	d := validData()
	d.AuditHash = strings.Repeat("a", 64)
	d.AuditSignature = strings.Repeat("b", 128)
	d.PreviousSessionHash = strings.Repeat("a", 64)

	report := ValidateIntegrity(d)
	if report.OK {
		t.Fatal("expected OK=false for self-referential previous_session_hash")
	}
	requireIssueField(t, report, "PreviousSessionHash")
}

// ── Hints are non-empty for all issues ────────────────────────────────────────

func TestValidateIntegrity_AllIssuesHaveHints(t *testing.T) {
	scenarios := []*Data{
		func() *Data { d := validData(); d.ID = ""; return d }(),
		func() *Data { d := validData(); d.TxHash = ""; return d }(),
		func() *Data { d := validData(); d.Network = ""; return d }(),
		func() *Data { d := validData(); d.Network = "badnet"; return d }(),
		func() *Data { d := validData(); d.Status = ""; return d }(),
		func() *Data { d := validData(); d.Status = "invalid"; return d }(),
		func() *Data { d := validData(); d.CreatedAt = time.Time{}; return d }(),
		func() *Data { d := validData(); d.LastAccessAt = time.Time{}; return d }(),
		func() *Data {
			d := validData()
			d.CreatedAt = time.Now()
			d.LastAccessAt = time.Now().Add(-time.Minute)
			return d
		}(),
		func() *Data { d := validData(); d.SchemaVersion = SchemaVersion + 1; return d }(),
		func() *Data { d := validData(); d.SimRequestJSON = "x"; d.EnvelopeXdr = ""; return d }(),
		func() *Data { d := validData(); d.AuditHash = strings.Repeat("a", 64); return d }(),
		func() *Data { d := validData(); d.AuditHash = strings.Repeat("a", 64); d.AuditSignature = "zz"; return d }(),
		func() *Data { d := validData(); d.PreviousSessionHash = strings.Repeat("c", 64); return d }(),
	}

	for i, d := range scenarios {
		report := ValidateIntegrity(d)
		for _, issue := range report.Issues {
			if strings.TrimSpace(issue.Hint) == "" {
				t.Errorf("scenario %d: issue for field %q has an empty hint", i, issue.Field)
			}
		}
	}
}

// ── Multiple issues accumulate ────────────────────────────────────────────────

func TestValidateIntegrity_MultipleProblems_AllReported(t *testing.T) {
	d := &Data{
		// ID, TxHash, Network, Status all empty — 4 issues minimum
		SchemaVersion: SchemaVersion,
		CreatedAt:     time.Now(),
		LastAccessAt:  time.Now(),
	}
	report := ValidateIntegrity(d)
	if len(report.Issues) < 4 {
		t.Errorf("expected at least 4 issues for a mostly-empty session, got %d: %v", len(report.Issues), report.Issues)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

// requireIssueField asserts that at least one issue targets the named field.
func requireIssueField(t *testing.T, report *IntegrityReport, field string) {
	t.Helper()
	for _, issue := range report.Issues {
		if issue.Field == field {
			return
		}
	}
	t.Errorf("expected an issue for field %q, got: %v", field, report.Issues)
}

// ── SaveWithValidation ────────────────────────────────────────────────────────

func TestSaveWithValidation_ValidSession_Succeeds(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())

	store, err := NewStore()
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	ctx := t.Context()
	d := validData()
	if err := store.SaveWithValidation(ctx, d); err != nil {
		t.Errorf("SaveWithValidation should succeed for a valid session, got: %v", err)
	}
}

func TestSaveWithValidation_NilData_ReturnsError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())

	store, err := NewStore()
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	if err := store.SaveWithValidation(t.Context(), nil); err == nil {
		t.Fatal("expected error for nil session data")
	}
}

func TestSaveWithValidation_InvalidSession_ReturnsDescriptiveError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())

	store, err := NewStore()
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	d := validData()
	d.TxHash = ""   // missing required field
	d.Network = ""  // missing required field

	err = store.SaveWithValidation(t.Context(), d)
	if err == nil {
		t.Fatal("expected error for invalid session data")
	}
	msg := err.Error()
	// Error must list the specific failing fields.
	if !strings.Contains(msg, "TxHash") {
		t.Errorf("error should mention 'TxHash', got: %v", err)
	}
	if !strings.Contains(msg, "Network") {
		t.Errorf("error should mention 'Network', got: %v", err)
	}
	// Error must include a Hint for each issue.
	if !strings.Contains(msg, "Hint:") {
		t.Errorf("error should include at least one 'Hint:', got: %v", err)
	}
}

// ── Save inline validation ────────────────────────────────────────────────────

func TestSave_EmptyTxHash_ReturnsError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())

	store, err := NewStore()
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	d := validData()
	d.TxHash = ""
	if err := store.Save(t.Context(), d); err == nil {
		t.Fatal("expected error for missing TxHash")
	}
}

func TestSave_EmptyNetwork_ReturnsError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())

	store, err := NewStore()
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	d := validData()
	d.Network = ""
	if err := store.Save(t.Context(), d); err == nil {
		t.Fatal("expected error for missing Network")
	}
}

func TestSave_InvalidNetwork_ReturnsError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())

	store, err := NewStore()
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	d := validData()
	d.Network = "devnet"
	err = store.Save(t.Context(), d)
	if err == nil {
		t.Fatal("expected error for invalid Network")
	}
	if !strings.Contains(err.Error(), "devnet") {
		t.Errorf("error should name the invalid network, got: %v", err)
	}
}

func TestSave_InvalidStatus_ReturnsError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())

	store, err := NewStore()
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	d := validData()
	d.Status = "corrupted"
	if err := store.Save(t.Context(), d); err == nil {
		t.Fatal("expected error for invalid Status")
	}
}

func TestSave_NameTooLong_ReturnsError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())

	store, err := NewStore()
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	d := validData()
	d.Name = string(make([]byte, 129)) // 129 characters
	if err := store.Save(t.Context(), d); err == nil {
		t.Fatal("expected error for name exceeding 128 chars")
	}
}

func TestSave_NameAtMaxLength_Succeeds(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())

	store, err := NewStore()
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	d := validData()
	d.Name = string(make([]byte, 128)) // exactly 128
	if err := store.Save(t.Context(), d); err != nil {
		t.Errorf("expected success for name at max length, got: %v", err)
	}
}

func TestSave_EmptyStatus_AutoPopulatesActive(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())

	store, err := NewStore()
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	d := validData()
	d.Status = ""
	if err := store.Save(t.Context(), d); err != nil {
		t.Fatalf("expected save to succeed with auto-populated status: %v", err)
	}
	loaded, err := store.Load(t.Context(), d.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Status != "active" {
		t.Errorf("expected auto-populated status 'active', got %q", loaded.Status)
	}
}

func TestSave_EmptyHorizonURL_AutoPopulates(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())

	store, err := NewStore()
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	tests := []struct {
		network string
		wantURL string
	}{
		{"testnet", "https://horizon-testnet.stellar.org"},
		{"mainnet", "https://horizon.stellar.org"},
		{"futurenet", "https://horizon-futurenet.stellar.org"},
	}
	for _, tc := range tests {
		d := validData()
		d.Network = tc.network
		d.HorizonURL = ""
		if err := store.Save(t.Context(), d); err != nil {
			t.Fatalf("Save(%s): %v", tc.network, err)
		}
		loaded, err := store.Load(t.Context(), d.ID)
		if err != nil {
			t.Fatalf("Load(%s): %v", tc.network, err)
		}
		if loaded.HorizonURL != tc.wantURL {
			t.Errorf("Save(%s): HorizonURL = %q, want %q", tc.network, loaded.HorizonURL, tc.wantURL)
		}
	}
}

func TestValidateIntegrity_NameTooLong_ReportsIssue(t *testing.T) {
	d := validData()
	d.Name = string(make([]byte, 129))
	report := ValidateIntegrity(d)
	if report.OK {
		t.Fatal("expected OK=false when Name exceeds 128 chars")
	}
	requireIssueField(t, report, "Name")
}

func TestValidateIntegrity_NameAtMaxLength_OK(t *testing.T) {
	d := validData()
	d.Name = string(make([]byte, 128))
	report := ValidateIntegrity(d)
	for _, issue := range report.Issues {
		if issue.Field == "Name" {
			t.Errorf("expected no issue for Name at max length, got: %v", issue)
		}
	}
}

// ── Save with auto-generated ID ───────────────────────────────────────────────

func TestGenerateID_WithTxHash_ProducesPrefixedID(t *testing.T) {
	id := GenerateID("abcdef1234567890")
	if !strings.HasPrefix(id, "abcdef12-") {
		t.Errorf("expected ID to start with 'abcdef12-', got: %s", id)
	}
}

func TestGenerateID_EmptyTxHash_FallsBackToSessionPrefix(t *testing.T) {
	id := GenerateID("")
	if !strings.HasPrefix(id, "session-") {
		t.Errorf("expected ID to start with 'session-', got: %s", id)
	}
}

func TestSaveWithValidation_InvalidSession_ListsAllIssues(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())

	store, err := NewStore()
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	// Session with 4+ issues: empty ID, TxHash, Network, Status.
	d := &Data{
		SchemaVersion: SchemaVersion,
		CreatedAt:     time.Now().Add(-time.Hour),
		LastAccessAt:  time.Now(),
	}
	err = store.SaveWithValidation(t.Context(), d)
	if err == nil {
		t.Fatal("expected error listing multiple issues")
	}
	if !strings.Contains(err.Error(), "validation") {
		t.Errorf("error should mention 'validation', got: %v", err)
	}
}

func TestSaveWithValidation_InvalidAuditChain_ReturnsActionableError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())

	store, err := NewStore()
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	d := validData()
	d.AuditHash = strings.Repeat("a", 64)
	d.PreviousSessionHash = strings.Repeat("b", 64)
	// Missing audit_signature: should be rejected before persistence.

	err = store.SaveWithValidation(t.Context(), d)
	if err == nil {
		t.Fatal("expected error for invalid audit-chain state")
	}
	msg := err.Error()
	if !strings.Contains(msg, "AuditSignature") {
		t.Errorf("error should mention AuditSignature, got: %v", err)
	}
	if !strings.Contains(msg, "previous_session_hash") && !strings.Contains(msg, "audit_hash") {
		t.Errorf("error should mention the chain relationship, got: %v", err)
	}
	if !strings.Contains(msg, "Hint:") {
		t.Errorf("error should include remediation hints, got: %v", err)
	}
}
