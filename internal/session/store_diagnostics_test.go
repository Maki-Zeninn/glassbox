// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Tests for Issue #312: session persistence validation and diagnostics.
// Covers RunStoreDiagnostics, StoreDiagnosticsResult.Summary, and the
// behaviour when the store contains healthy vs. degraded sessions.

package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// overrideTempHome points HOME / USERPROFILE at a temp dir so the Store never
// writes to the real user home directory during tests.
func overrideTempHome(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	// Pre-create the .Glassbox directory so SQLite doesn't race.
	_ = os.MkdirAll(filepath.Join(dir, ".Glassbox"), 0755)
}

// ── StoreDiagnosticsResult.Summary ───────────────────────────────────────────

func TestStoreDiagnosticsResult_Summary_NoSessions(t *testing.T) {
	r := &StoreDiagnosticsResult{}
	s := r.Summary()
	if !strings.Contains(s, "no sessions") {
		t.Errorf("Summary() for empty store should mention 'no sessions'; got: %q", s)
	}
}

func TestStoreDiagnosticsResult_Summary_AllHealthy(t *testing.T) {
	r := &StoreDiagnosticsResult{
		TotalSessions:   3,
		HealthySessions: 3,
	}
	s := r.Summary()
	if !strings.Contains(s, "healthy") {
		t.Errorf("Summary() should mention healthy sessions; got: %q", s)
	}
	if strings.Contains(s, "degraded") {
		t.Errorf("Summary() should not mention degraded when none exist; got: %q", s)
	}
}

func TestStoreDiagnosticsResult_Summary_SomeDegraded(t *testing.T) {
	r := &StoreDiagnosticsResult{
		TotalSessions:    5,
		HealthySessions:  3,
		DegradedSessions: 2,
	}
	s := r.Summary()
	if !strings.Contains(s, "degraded") {
		t.Errorf("Summary() should mention degraded sessions; got: %q", s)
	}
	if !strings.Contains(s, "glassbox session list") {
		t.Errorf("Summary() should hint at 'glassbox session list'; got: %q", s)
	}
}

// ── RunStoreDiagnostics — empty store ────────────────────────────────────────

func TestRunStoreDiagnostics_EmptyStore_ReturnsZeroCounts(t *testing.T) {
	overrideTempHome(t)
	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	result, err := store.RunStoreDiagnostics(context.Background())
	if err != nil {
		t.Fatalf("RunStoreDiagnostics: %v", err)
	}
	if result.TotalSessions != 0 {
		t.Errorf("TotalSessions = %d, want 0", result.TotalSessions)
	}
	if result.DegradedSessions != 0 {
		t.Errorf("DegradedSessions = %d, want 0", result.DegradedSessions)
	}
}

// ── RunStoreDiagnostics — all healthy ────────────────────────────────────────

func TestRunStoreDiagnostics_HealthySessions_AllCountedHealthy(t *testing.T) {
	overrideTempHome(t)
	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		d := makeValidSessionData(t, i)
		if err := store.Save(ctx, d); err != nil {
			t.Fatalf("Save session %d: %v", i, err)
		}
	}

	result, err := store.RunStoreDiagnostics(ctx)
	if err != nil {
		t.Fatalf("RunStoreDiagnostics: %v", err)
	}
	if result.TotalSessions != 3 {
		t.Errorf("TotalSessions = %d, want 3", result.TotalSessions)
	}
	if result.HealthySessions != 3 {
		t.Errorf("HealthySessions = %d, want 3", result.HealthySessions)
	}
	if result.DegradedSessions != 0 {
		t.Errorf("DegradedSessions = %d, want 0", result.DegradedSessions)
	}
	if len(result.Reports) != 0 {
		t.Errorf("expected no degraded reports; got %d", len(result.Reports))
	}
}

// ── RunStoreDiagnostics — degraded session detected ───────────────────────────

func TestRunStoreDiagnostics_DegradedSession_AppearsInReports(t *testing.T) {
	overrideTempHome(t)
	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()

	// Save one healthy session.
	if err := store.Save(ctx, makeValidSessionData(t, 0)); err != nil {
		t.Fatalf("Save healthy session: %v", err)
	}

	// Save one corrupt session (empty TxHash). SavePreservingSchemaVersion is
	// used here because Save() now rejects a missing TxHash outright; this
	// simulates a session that was already corrupt on disk (e.g. written by
	// an older, less strict version of Glassbox) so RunStoreDiagnostics has
	// something degraded to detect.
	corrupt := makeValidSessionData(t, 1)
	corrupt.TxHash = "" // integrity violation
	if err := store.SavePreservingSchemaVersion(ctx, corrupt); err != nil {
		t.Fatalf("Save corrupt session: %v", err)
	}

	result, err := store.RunStoreDiagnostics(ctx)
	if err != nil {
		t.Fatalf("RunStoreDiagnostics: %v", err)
	}
	if result.TotalSessions != 2 {
		t.Errorf("TotalSessions = %d, want 2", result.TotalSessions)
	}
	if result.HealthySessions != 1 {
		t.Errorf("HealthySessions = %d, want 1", result.HealthySessions)
	}
	if result.DegradedSessions != 1 {
		t.Errorf("DegradedSessions = %d, want 1", result.DegradedSessions)
	}
	if len(result.Reports) != 1 {
		t.Fatalf("expected 1 degraded report; got %d", len(result.Reports))
	}
	report := result.Reports[0]
	if report.OK {
		t.Error("degraded report must have OK=false")
	}
	// Must identify the TxHash issue.
	found := false
	for _, issue := range report.Issues {
		if issue.Field == "TxHash" {
			found = true
			if strings.TrimSpace(issue.Hint) == "" {
				t.Error("TxHash issue must carry an actionable Hint")
			}
		}
	}
	if !found {
		t.Errorf("expected a TxHash integrity issue; got: %v", report.Issues)
	}
}

// ── RunStoreDiagnostics — summary reflects degraded state ────────────────────

func TestRunStoreDiagnostics_Summary_MentionsDegraded(t *testing.T) {
	overrideTempHome(t)
	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	corrupt := makeValidSessionData(t, 0)
	corrupt.Network = "devnet" // invalid — see SavePreservingSchemaVersion note above
	if err := store.SavePreservingSchemaVersion(ctx, corrupt); err != nil {
		t.Fatalf("Save: %v", err)
	}

	result, err := store.RunStoreDiagnostics(ctx)
	if err != nil {
		t.Fatalf("RunStoreDiagnostics: %v", err)
	}
	s := result.Summary()
	if !strings.Contains(s, "degraded") {
		t.Errorf("Summary() should mention 'degraded' when issues exist; got: %q", s)
	}
}

func TestRunStoreDiagnostics_AuditChainIssue_AppearsInReports(t *testing.T) {
	overrideTempHome(t)
	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	corrupt := makeValidSessionData(t, 0)
	corrupt.AuditHash = strings.Repeat("a", 64)
	corrupt.PreviousSessionHash = strings.Repeat("b", 64)
	// Missing audit_signature makes the persisted chain incomplete.
	if err := store.Save(ctx, corrupt); err != nil {
		t.Fatalf("Save: %v", err)
	}

	result, err := store.RunStoreDiagnostics(ctx)
	if err != nil {
		t.Fatalf("RunStoreDiagnostics: %v", err)
	}
	if result.DegradedSessions != 1 {
		t.Fatalf("DegradedSessions = %d, want 1", result.DegradedSessions)
	}
	if len(result.Reports) != 1 {
		t.Fatalf("expected 1 degraded report; got %d", len(result.Reports))
	}

	report := result.Reports[0]
	foundAuditSignature := false
	for _, issue := range report.Issues {
		if issue.Field == "AuditSignature" {
			foundAuditSignature = true
			if strings.TrimSpace(issue.Hint) == "" {
				t.Error("AuditSignature issue should carry a hint")
			}
		}
	}
	if !foundAuditSignature {
		t.Fatalf("expected AuditSignature issue in degraded report, got: %+v", report.Issues)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func makeValidSessionData(t *testing.T, idx int) *Data {
	t.Helper()
	now := time.Now()
	return &Data{
		ID:            GenerateID("abcdef12") + string(rune('a'+idx)),
		TxHash:        "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
		Network:       "testnet",
		HorizonURL:    "https://horizon-testnet.stellar.org",
		Status:        "saved",
		CreatedAt:     now.Add(-time.Hour),
		LastAccessAt:  now,
		SchemaVersion: SchemaVersion,
	}
}
