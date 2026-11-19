// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package session

import (
	"context"
	"strings"
	"testing"
	"time"
)

func gcTestData(id string, lastAccess time.Time, status, name string) *Data {
	return &Data{
		ID:            id,
		Name:          name,
		TxHash:        "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
		Network:       "testnet",
		HorizonURL:    "https://horizon-testnet.stellar.org",
		Status:        status,
		CreatedAt:     lastAccess.Add(-time.Hour),
		LastAccessAt:  lastAccess,
		SchemaVersion: SchemaVersion,
	}
}

// backdateLastAccess directly rewrites a session's last_access_at, bypassing
// Save's "always bump to now" behavior so GC age-based eligibility can be
// tested deterministically.
func backdateLastAccess(t *testing.T, store *Store, id string, when time.Time) {
	t.Helper()
	if _, err := store.db.Exec(
		"UPDATE sessions SET last_access_at = ? WHERE id = ?",
		when.UTC().Format(time.RFC3339), id,
	); err != nil {
		t.Fatalf("backdateLastAccess(%s): %v", id, err)
	}
}

func TestPlanGC_ExpiredSession_Eligible(t *testing.T) {
	overrideTempHome(t)
	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	old := gcTestData("old-session", time.Now().Add(-100*24*time.Hour), "saved", "")
	if err := store.Save(ctx, old); err != nil {
		t.Fatalf("Save: %v", err)
	}
	backdateLastAccess(t, store, "old-session", time.Now().Add(-100*24*time.Hour))

	plan, err := store.PlanGC(ctx, GCOptions{MaxAge: 30 * 24 * time.Hour})
	if err != nil {
		t.Fatalf("PlanGC: %v", err)
	}
	if len(plan.ToDelete) != 1 {
		t.Fatalf("expected 1 eligible session, got %d: %+v", len(plan.ToDelete), plan.ToDelete)
	}
	if plan.ToDelete[0].ID != "old-session" {
		t.Errorf("expected old-session to be eligible, got %q", plan.ToDelete[0].ID)
	}
}

func TestPlanGC_PinnedSession_NeverEligible(t *testing.T) {
	overrideTempHome(t)
	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	pinned := gcTestData("pinned-session", time.Now().Add(-100*24*time.Hour), "saved", "important-bug")
	if err := store.Save(ctx, pinned); err != nil {
		t.Fatalf("Save: %v", err)
	}

	plan, err := store.PlanGC(ctx, GCOptions{MaxAge: 30 * 24 * time.Hour})
	if err != nil {
		t.Fatalf("PlanGC: %v", err)
	}
	for _, e := range plan.ToDelete {
		if e.ID == "pinned-session" {
			t.Fatal("a pinned (bookmarked) session must never be eligible for deletion")
		}
	}
	if len(plan.Entries) != 1 || !plan.Entries[0].Pinned {
		t.Fatalf("expected the entry to be marked Pinned, got: %+v", plan.Entries)
	}
}

func TestPlanGC_ActiveSession_NeverEligible(t *testing.T) {
	overrideTempHome(t)
	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	active := gcTestData("active-session", time.Now().Add(-100*24*time.Hour), "active", "")
	if err := store.Save(ctx, active); err != nil {
		t.Fatalf("Save: %v", err)
	}

	plan, err := store.PlanGC(ctx, GCOptions{MaxAge: 30 * 24 * time.Hour, MaxCount: 0})
	if err != nil {
		t.Fatalf("PlanGC: %v", err)
	}
	for _, e := range plan.ToDelete {
		if e.ID == "active-session" {
			t.Fatal("an active session must never be eligible for deletion")
		}
	}
}

func TestRunGC_DryRun_DeletesNothing(t *testing.T) {
	overrideTempHome(t)
	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	old := gcTestData("old-session", time.Now().Add(-100*24*time.Hour), "saved", "")
	if err := store.Save(ctx, old); err != nil {
		t.Fatalf("Save: %v", err)
	}
	backdateLastAccess(t, store, "old-session", time.Now().Add(-100*24*time.Hour))

	plan, err := store.RunGC(ctx, GCOptions{MaxAge: 30 * 24 * time.Hour}, true)
	if err != nil {
		t.Fatalf("RunGC dry-run: %v", err)
	}
	if len(plan.ToDelete) != 1 {
		t.Fatalf("expected the dry-run plan to list 1 eligible session, got %d", len(plan.ToDelete))
	}
	if plan.DeleteSize() <= 0 {
		t.Error("expected a non-zero total size for the eligible session")
	}

	// Dry-run must not have deleted anything.
	if _, loadErr := store.Load(ctx, "old-session"); loadErr != nil {
		t.Errorf("dry-run must not delete: session missing after dry run: %v", loadErr)
	}
}

func TestRunGC_ActualRun_DeletesEligibleOnly(t *testing.T) {
	overrideTempHome(t)
	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	old := gcTestData("old-session", time.Now().Add(-100*24*time.Hour), "saved", "")
	pinned := gcTestData("pinned-session", time.Now().Add(-100*24*time.Hour), "saved", "keep-me")
	if err := store.Save(ctx, old); err != nil {
		t.Fatalf("Save old: %v", err)
	}
	if err := store.Save(ctx, pinned); err != nil {
		t.Fatalf("Save pinned: %v", err)
	}
	backdateLastAccess(t, store, "old-session", time.Now().Add(-100*24*time.Hour))
	backdateLastAccess(t, store, "pinned-session", time.Now().Add(-100*24*time.Hour))

	plan, err := store.RunGC(ctx, GCOptions{MaxAge: 30 * 24 * time.Hour}, false)
	if err != nil {
		t.Fatalf("RunGC: %v", err)
	}
	if len(plan.ToDelete) != 1 || plan.ToDelete[0].ID != "old-session" {
		t.Fatalf("expected only old-session to be deleted, got: %+v", plan.ToDelete)
	}

	if _, loadErr := store.Load(ctx, "old-session"); loadErr == nil {
		t.Error("expected old-session to be deleted after RunGC")
	}
	if _, loadErr := store.Load(ctx, "pinned-session"); loadErr != nil {
		t.Errorf("pinned-session must survive RunGC: %v", loadErr)
	}
}

func TestPlanGC_CorruptEntry_DoesNotCrashAndIsFlagged(t *testing.T) {
	overrideTempHome(t)
	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	corrupt := gcTestData("corrupt-session", time.Now().Add(-1*time.Hour), "saved", "")
	corrupt.TxHash = "" // integrity violation, but still a valid DB row via the legacy path
	if err := store.SavePreservingSchemaVersion(ctx, corrupt); err != nil {
		t.Fatalf("SavePreservingSchemaVersion: %v", err)
	}

	plan, err := store.PlanGC(ctx, DefaultGCOptions())
	if err != nil {
		t.Fatalf("PlanGC must not error on a corrupt entry: %v", err)
	}
	found := false
	for _, e := range plan.Entries {
		if e.ID == "corrupt-session" {
			found = true
			if !e.Corrupt {
				t.Error("expected the entry to be flagged Corrupt")
			}
		}
	}
	if !found {
		t.Fatal("expected the corrupt session to appear in the plan")
	}
}

func TestPlanGC_MaxCount_TrimsOldestExcess(t *testing.T) {
	overrideTempHome(t)
	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	base := time.Now()
	for i := 0; i < 5; i++ {
		lastAccess := base.Add(-time.Duration(5-i) * time.Hour) // earlier index => older
		d := gcTestData(gcID(i), lastAccess, "saved", "")
		if err := store.Save(ctx, d); err != nil {
			t.Fatalf("Save %d: %v", i, err)
		}
		backdateLastAccess(t, store, gcID(i), lastAccess)
	}

	plan, err := store.PlanGC(ctx, GCOptions{MaxCount: 2})
	if err != nil {
		t.Fatalf("PlanGC: %v", err)
	}
	if len(plan.ToDelete) != 3 {
		t.Fatalf("expected 3 sessions trimmed to respect MaxCount=2, got %d: %+v", len(plan.ToDelete), plan.ToDelete)
	}
	// The two most-recently-accessed sessions must survive.
	survivors := map[string]bool{gcID(3): true, gcID(4): true}
	for _, e := range plan.ToDelete {
		if survivors[e.ID] {
			t.Errorf("session %q should have survived MaxCount trimming", e.ID)
		}
	}
}

func gcID(i int) string { return "count-session-" + string(rune('a'+i)) }

// ── ValidateGCRoot ────────────────────────────────────────────────────────────

func TestValidateGCRoot_Empty_Rejected(t *testing.T) {
	if err := ValidateGCRoot(""); err == nil {
		t.Fatal("expected error for empty root")
	}
}

func TestValidateGCRoot_FilesystemRoot_Rejected(t *testing.T) {
	if err := ValidateGCRoot("/"); err == nil {
		t.Fatal("expected error for filesystem root")
	}
}

func TestValidateGCRoot_NonGlassboxDir_Rejected(t *testing.T) {
	err := ValidateGCRoot("/tmp/some-other-directory")
	if err == nil {
		t.Fatal("expected error for a directory that isn't .Glassbox")
	}
	if !strings.Contains(err.Error(), ".Glassbox") {
		t.Errorf("error should mention .Glassbox, got: %v", err)
	}
}

func TestValidateGCRoot_GlassboxDir_OK(t *testing.T) {
	dir := t.TempDir()
	if err := ValidateGCRoot(dir + "/.Glassbox"); err != nil {
		t.Errorf("expected no error for a .Glassbox directory, got: %v", err)
	}
}
