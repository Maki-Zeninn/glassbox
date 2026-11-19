// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package session

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
)

func TestUpgradeSessionData_InterruptedStep_RollsBack(t *testing.T) {
	if SchemaVersion < 2 {
		t.Skip("need multi-step migration table")
	}

	for _, step := range migrationTable {
		if step.toVersion <= 1 || step.toVersion > SchemaVersion {
			continue
		}
		toVer := step.toVersion
		t.Run(fmt.Sprintf("interrupt_at_v%d", toVer), func(t *testing.T) {
			defer SetMigrationFaultHook(nil)
			SetMigrationFaultHook(func(v int) error {
				if v == toVer {
					return fmt.Errorf("injected fault at v%d", v)
				}
				return nil
			})

			d := &Data{
				ID:            "interrupt-sess",
				SchemaVersion: 1,
				Status:        "",
				TagsJSON:      "",
			}
			_, err := UpgradeSessionData(d)
			if err == nil {
				t.Fatal("expected rolled-back error")
			}
			// Session must remain consistent: either still at a prior version
			// (before the failing step) or unchanged from the start.
			if d.SchemaVersion >= toVer {
				t.Fatalf("SchemaVersion=%d after rollback, want < %d", d.SchemaVersion, toVer)
			}
		})
	}
}

func TestUpgradeSessionDataWithJournal_Interrupted_RecordsRolledBack(t *testing.T) {
	if SchemaVersion < 2 {
		t.Skip("need multi-step migration")
	}
	dir := t.TempDir()
	store, err := NewStoreAt(dir)
	if err != nil {
		t.Fatalf("NewStoreAt: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	d := &Data{
		ID:            "journal-sess",
		TxHash:        "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Network:       "testnet",
		HorizonURL:    "https://horizon-testnet.stellar.org",
		Status:        "active",
		SchemaVersion: 1,
		TagsJSON:      "",
	}
	if err := store.SavePreservingSchemaVersion(ctx, d); err != nil {
		t.Fatalf("SavePreservingSchemaVersion: %v", err)
	}

	target := 2
	for _, step := range migrationTable {
		if step.toVersion > 1 && step.toVersion <= SchemaVersion {
			target = step.toVersion
			break
		}
	}

	defer SetMigrationFaultHook(nil)
	SetMigrationFaultHook(func(v int) error {
		if v == target {
			return fmt.Errorf("kill -9 simulated at v%d", v)
		}
		return nil
	})

	loaded := *d
	_, upErr := store.UpgradeSessionDataWithJournal(ctx, &loaded)
	if upErr == nil {
		t.Fatal("expected error from fault injection")
	}

	entries, err := store.ListMigrationJournal(ctx, d.ID)
	if err != nil {
		t.Fatalf("ListMigrationJournal: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.Result == MigrationResultRolledBack && e.ToVersion == target {
			found = true
			if e.BinaryVersion == "" || e.AppliedAt == "" {
				t.Errorf("journal entry missing metadata: %+v", e)
			}
		}
	}
	if !found {
		t.Fatalf("expected rolled_back journal entry for v%d, got %+v", target, entries)
	}
	if loaded.SchemaVersion >= target {
		t.Fatalf("in-memory schema %d should be rolled back below %d", loaded.SchemaVersion, target)
	}
}

func TestDryRunMigrationPlan_NoMutation(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStoreAt(dir)
	if err != nil {
		t.Fatalf("NewStoreAt: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	d := &Data{
		ID:            "dry-run-sess",
		TxHash:        "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Network:       "testnet",
		HorizonURL:    "https://horizon-testnet.stellar.org",
		Status:        "active",
		SchemaVersion: 1,
		TagsJSON:      "[]",
	}
	if err := store.SavePreservingSchemaVersion(ctx, d); err != nil {
		t.Fatalf("save: %v", err)
	}

	dbPath := filepath.Join(dir, "sessions.db")
	steps, err := DryRunMigrationPlanForSession(dbPath, d.ID)
	if err != nil {
		t.Fatalf("DryRunMigrationPlanForSession: %v", err)
	}
	if len(steps) == 0 && SchemaVersion > 1 {
		t.Fatal("expected pending steps for schema v1")
	}

	// Reloading read-only must still see v1.
	loaded, warn, err := store.LoadReadOnly(ctx, d.ID)
	if err != nil {
		t.Fatalf("LoadReadOnly: %v", err)
	}
	if loaded.SchemaVersion != 1 {
		t.Fatalf("SchemaVersion=%d, want 1 (dry-run must not migrate)", loaded.SchemaVersion)
	}
	if SchemaVersion > 1 && warn == "" {
		t.Fatal("expected stale-schema warning from LoadReadOnly")
	}
}

func TestPendingMigrationSteps_EmptyWhenCurrent(t *testing.T) {
	steps := PendingMigrationSteps(SchemaVersion)
	if len(steps) != 0 {
		t.Fatalf("expected no steps at current version, got %+v", steps)
	}
}
