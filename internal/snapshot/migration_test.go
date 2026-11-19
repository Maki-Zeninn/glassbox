// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package snapshot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func makeV1Persisted(t *testing.T, txHash, network string) *PersistedSnapshot {
	t.Helper()
	snap := FromMap(map[string]string{"k1": "v1", "k2": "v2"})
	ps := &PersistedSnapshot{
		Metadata: &ReplayMetadata{
			SchemaVersion:   1,
			GlassboxVersion: "v1.0.0",
			TxHash:          txHash,
			Network:         network,
			SavedAt:         time.Now().UTC(),
		},
		Snapshot: snap,
	}
	return ps
}

// writeV1File writes a schema-v1 persisted snapshot file directly without going
// through SavePersisted (which would bump to v2). Used to test the migration path.
func writeV1File(t *testing.T, path string, ps *PersistedSnapshot) {
	t.Helper()
	data, err := json.MarshalIndent(ps, "", "  ")
	if err != nil {
		t.Fatalf("marshal v1 fixture: %v", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write v1 fixture: %v", err)
	}
}

// ── MigrateSnapshot ───────────────────────────────────────────────────────────

func TestMigrateSnapshot_V1ToV2_SetsLedgerFormat(t *testing.T) {
	ps := makeV1Persisted(t, "abc123", "testnet")
	if ps.Metadata.LedgerFormat != "" {
		t.Error("expected empty LedgerFormat before migration")
	}

	steps, err := MigrateSnapshot(ps)
	if err != nil {
		t.Fatalf("MigrateSnapshot: %v", err)
	}
	if len(steps) != 1 {
		t.Errorf("expected 1 migration step, got %d", len(steps))
	}
	if ps.Metadata.LedgerFormat != "base64-xdr" {
		t.Errorf("expected LedgerFormat='base64-xdr', got %q", ps.Metadata.LedgerFormat)
	}
	if ps.Metadata.SchemaVersion != PersistSchemaVersion {
		t.Errorf("expected SchemaVersion=%d, got %d", PersistSchemaVersion, ps.Metadata.SchemaVersion)
	}
}

func TestMigrateSnapshot_AlreadyCurrent_NoOp(t *testing.T) {
	ps := makeV1Persisted(t, "abc123", "testnet")
	ps.Metadata.SchemaVersion = PersistSchemaVersion
	ps.Metadata.LedgerFormat = "base64-xdr"

	steps, err := MigrateSnapshot(ps)
	if err != nil {
		t.Fatalf("MigrateSnapshot: %v", err)
	}
	if len(steps) != 0 {
		t.Errorf("expected 0 steps for current version, got %d", len(steps))
	}
}

func TestMigrateSnapshot_RecordsLog(t *testing.T) {
	ps := makeV1Persisted(t, "abc", "testnet")
	_, err := MigrateSnapshot(ps)
	if err != nil {
		t.Fatalf("MigrateSnapshot: %v", err)
	}
	if len(ps.Metadata.MigrationLog) == 0 {
		t.Error("expected migration log entry to be recorded")
	}
	entry := ps.Metadata.MigrationLog[0]
	if entry.FromVersion != 1 {
		t.Errorf("expected FromVersion=1, got %d", entry.FromVersion)
	}
	if entry.ToVersion != 2 {
		t.Errorf("expected ToVersion=2, got %d", entry.ToVersion)
	}
	if entry.Description == "" {
		t.Error("expected non-empty migration description")
	}
	if entry.AppliedAt.IsZero() {
		t.Error("expected non-zero AppliedAt")
	}
}

func TestMigrateSnapshot_Idempotent(t *testing.T) {
	ps := makeV1Persisted(t, "abc", "testnet")

	// Run migration twice.
	if _, err := MigrateSnapshot(ps); err != nil {
		t.Fatal(err)
	}
	logLenAfterFirst := len(ps.Metadata.MigrationLog)

	if _, err := MigrateSnapshot(ps); err != nil {
		t.Fatal(err)
	}
	// Second call should produce no new log entries (already at current).
	if len(ps.Metadata.MigrationLog) != logLenAfterFirst {
		t.Errorf("idempotency failed: log grew from %d to %d on second call",
			logLenAfterFirst, len(ps.Metadata.MigrationLog))
	}
}

func TestMigrateSnapshot_PreservesLedgerState(t *testing.T) {
	ps := makeV1Persisted(t, "abc", "testnet")
	before := ps.Snapshot.ToMap()

	_, err := MigrateSnapshot(ps)
	if err != nil {
		t.Fatalf("MigrateSnapshot: %v", err)
	}
	after := ps.Snapshot.ToMap()

	for k, vBefore := range before {
		if after[k] != vBefore {
			t.Errorf("ledger entry %q changed during migration: %q → %q", k, vBefore, after[k])
		}
	}
	if len(after) != len(before) {
		t.Errorf("ledger entry count changed: %d → %d", len(before), len(after))
	}
}

// ── Unsupported version ───────────────────────────────────────────────────────

func TestMigrateSnapshot_TooOld_Rejected(t *testing.T) {
	ps := makeV1Persisted(t, "abc", "testnet")
	ps.Metadata.SchemaVersion = MinSupportedSchemaVersion - 1

	_, err := MigrateSnapshot(ps)
	if err == nil {
		t.Error("expected error for schema version below minimum supported")
	}
	if !IsSchemaError(err) {
		t.Errorf("expected SchemaError, got: %v", err)
	}
}

func TestMigrateSnapshot_FromFuture_Rejected(t *testing.T) {
	ps := makeV1Persisted(t, "abc", "testnet")
	ps.Metadata.SchemaVersion = PersistSchemaVersion + 99

	_, err := MigrateSnapshot(ps)
	if err == nil {
		t.Error("expected error for schema version from future binary")
	}
	if !IsSchemaError(err) {
		t.Errorf("expected SchemaError, got: %v", err)
	}
}

// ── LoadPersisted auto-migration ──────────────────────────────────────────────

func TestLoadPersisted_V1File_AutoMigrated(t *testing.T) {
	ps := makeV1Persisted(t, "abc123", "testnet")

	dir := t.TempDir()
	path := filepath.Join(dir, "snap-v1.snap.json")
	writeV1File(t, path, ps)

	loaded, err := LoadPersisted(path)
	if err != nil {
		t.Fatalf("LoadPersisted v1 file: %v", err)
	}
	if loaded.Metadata.SchemaVersion != PersistSchemaVersion {
		t.Errorf("expected SchemaVersion=%d after load, got %d",
			PersistSchemaVersion, loaded.Metadata.SchemaVersion)
	}
	if loaded.Metadata.LedgerFormat != "base64-xdr" {
		t.Errorf("expected LedgerFormat='base64-xdr' after migration, got %q",
			loaded.Metadata.LedgerFormat)
	}
	// Ledger state must be intact.
	m := loaded.Snapshot.ToMap()
	if m["k1"] != "v1" {
		t.Errorf("ledger entry corrupted after migration: %v", m)
	}
}

func TestLoadPersisted_V1File_MigrationLogPopulated(t *testing.T) {
	ps := makeV1Persisted(t, "abc123", "testnet")
	dir := t.TempDir()
	path := filepath.Join(dir, "snap-v1-log.snap.json")
	writeV1File(t, path, ps)

	loaded, _ := LoadPersisted(path)
	if len(loaded.Metadata.MigrationLog) == 0 {
		t.Error("expected migration log to be populated after auto-migration on load")
	}
}

func TestLoadPersisted_UnsupportedFutureVersion_Rejected(t *testing.T) {
	ps := makeV1Persisted(t, "abc", "testnet")
	ps.Metadata.SchemaVersion = PersistSchemaVersion + 99

	dir := t.TempDir()
	path := filepath.Join(dir, "future.snap.json")
	writeV1File(t, path, ps)

	_, err := LoadPersisted(path)
	if err == nil {
		t.Error("expected error for future schema version")
	}
	if !IsSchemaError(err) {
		t.Errorf("expected SchemaError, got: %T %v", err, err)
	}
	if !strings.Contains(err.Error(), "newer") {
		t.Errorf("expected 'newer' in error message, got: %v", err)
	}
}

func TestLoadPersisted_BeforeMinSupported_Rejected(t *testing.T) {
	ps := makeV1Persisted(t, "abc", "testnet")
	ps.Metadata.SchemaVersion = MinSupportedSchemaVersion - 1

	dir := t.TempDir()
	path := filepath.Join(dir, "ancient.snap.json")
	writeV1File(t, path, ps)

	_, err := LoadPersisted(path)
	if err == nil {
		t.Error("expected error for schema version below minimum")
	}
}

// ── SavePersisted emits v2 ────────────────────────────────────────────────────

func TestSavePersisted_EmitsV2(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snap-v2.snap.json")

	meta := &ReplayMetadata{
		GlassboxVersion: "v2.0.0",
		TxHash:          "deadbeef00000000deadbeef00000000deadbeef00000000deadbeef00000000",
		Network:         "testnet",
	}
	snap := FromMap(map[string]string{"key": "val"})
	if err := SavePersisted(path, meta, snap); err != nil {
		t.Fatalf("SavePersisted: %v", err)
	}

	loaded, err := LoadPersisted(path)
	if err != nil {
		t.Fatalf("LoadPersisted: %v", err)
	}
	if loaded.Metadata.SchemaVersion != PersistSchemaVersion {
		t.Errorf("expected v%d, got v%d", PersistSchemaVersion, loaded.Metadata.SchemaVersion)
	}
	if loaded.Metadata.LedgerFormat != "base64-xdr" {
		t.Errorf("expected LedgerFormat='base64-xdr', got %q", loaded.Metadata.LedgerFormat)
	}
}

// ── Round-trip state semantics ────────────────────────────────────────────────

func TestRoundTrip_V2_LedgerStatePreserved(t *testing.T) {
	original := map[string]string{
		"GCAAAAA": "AAAAAQAAAA==",
		"GCBBBBB": "BBBBAAAAAA==",
	}
	snap := FromMap(original)

	dir := t.TempDir()
	path := filepath.Join(dir, "roundtrip.snap.json")
	meta := &ReplayMetadata{
		GlassboxVersion: "v2.0.0",
		TxHash:          strings.Repeat("a", 64),
		Network:         "testnet",
	}
	if err := SavePersisted(path, meta, snap); err != nil {
		t.Fatalf("SavePersisted: %v", err)
	}

	loaded, err := LoadPersisted(path)
	if err != nil {
		t.Fatalf("LoadPersisted: %v", err)
	}
	got := loaded.Snapshot.ToMap()
	for k, v := range original {
		if got[k] != v {
			t.Errorf("key %q: expected %q, got %q", k, v, got[k])
		}
	}
	if len(got) != len(original) {
		t.Errorf("entry count mismatch: expected %d, got %d", len(original), len(got))
	}
}

func TestRoundTrip_FingerprintPreserved(t *testing.T) {
	snap := FromMap(map[string]string{"x": "y"})
	fpOriginal := snap.Fingerprint

	dir := t.TempDir()
	path := filepath.Join(dir, "fp.snap.json")
	meta := &ReplayMetadata{
		TxHash:  strings.Repeat("b", 64),
		Network: "testnet",
	}
	if err := SavePersisted(path, meta, snap); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadPersisted(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Snapshot.Fingerprint != fpOriginal {
		t.Errorf("fingerprint changed: %q → %q", fpOriginal, loaded.Snapshot.Fingerprint)
	}
}

// ── DiffPersistedSnapshots ────────────────────────────────────────────────────

func TestDiffPersistedSnapshots_V1Fixtures_IncludesVersionInfo(t *testing.T) {
	base := makeV1Persisted(t, "abc", "testnet")
	target := makeV1Persisted(t, "abc", "testnet")
	// Modify target to create a diff.
	target.Snapshot = FromMap(map[string]string{"k1": "changed", "k3": "new"})

	diff, err := DiffPersistedSnapshots(base, target)
	if err != nil {
		t.Fatalf("DiffPersistedSnapshots: %v", err)
	}
	// Both were v1; version info must appear in output.
	if diff.BaseSchemaVersion != 1 {
		t.Errorf("expected BaseSchemaVersion=1, got %d", diff.BaseSchemaVersion)
	}
	if diff.TargetSchemaVersion != 1 {
		t.Errorf("expected TargetSchemaVersion=1, got %d", diff.TargetSchemaVersion)
	}
	if len(diff.MigrationNotes) == 0 {
		t.Error("expected migration notes when diffing v1 fixtures")
	}
	if diff.TotalChanges() == 0 {
		t.Error("expected at least one diff between base and target")
	}
}

func TestDiffPersistedSnapshots_Identical_ZeroChanges(t *testing.T) {
	snap := FromMap(map[string]string{"a": "1"})
	base := &PersistedSnapshot{
		Metadata: &ReplayMetadata{SchemaVersion: PersistSchemaVersion, TxHash: "t", Network: "testnet", LedgerFormat: "base64-xdr"},
		Snapshot: snap,
	}
	target := &PersistedSnapshot{
		Metadata: &ReplayMetadata{SchemaVersion: PersistSchemaVersion, TxHash: "t", Network: "testnet", LedgerFormat: "base64-xdr"},
		Snapshot: snap,
	}
	diff, err := DiffPersistedSnapshots(base, target)
	if err != nil {
		t.Fatalf("DiffPersistedSnapshots: %v", err)
	}
	if diff.TotalChanges() != 0 {
		t.Errorf("expected 0 changes, got %d", diff.TotalChanges())
	}
}

func TestFormatDiff_ShowsVersionHeader(t *testing.T) {
	base := makeV1Persisted(t, "abc", "testnet")
	target := makeV1Persisted(t, "abc", "testnet")
	target.Snapshot = FromMap(map[string]string{"k1": "x"})

	diff, _ := DiffPersistedSnapshots(base, target)
	formatted := FormatDiff(diff)
	if !strings.Contains(formatted, "Schema versions") {
		t.Errorf("expected 'Schema versions' in FormatDiff output, got:\n%s", formatted)
	}
}

// ── MigrationResult.Summary ───────────────────────────────────────────────────

func TestMigrationResult_Summary_Upgraded(t *testing.T) {
	r := &MigrationResult{
		WasUpgraded: true,
		FromVersion: 1,
		Steps: []MigrationLogEntry{
			{FromVersion: 1, ToVersion: 2, Description: "test step"},
		},
	}
	s := r.Summary()
	if !strings.Contains(s, "migrated") {
		t.Errorf("expected 'migrated' in summary, got: %q", s)
	}
}

func TestMigrationResult_Summary_NoUpgrade(t *testing.T) {
	r := &MigrationResult{WasUpgraded: false}
	s := r.Summary()
	if !strings.Contains(s, "no migration needed") {
		t.Errorf("expected 'no migration needed' in summary, got: %q", s)
	}
}

// ── Backward compatibility — v1 fixture ──────────────────────────────────────
// This test ensures that a v1 fixture produced by the old binary still loads
// cleanly and that the schema migration does not alter the decoded ledger state.

func TestBackwardCompat_V1Fixture_LoadsAndMigrates(t *testing.T) {
	// Build a v1 file with a known, representative ledger state.
	ledger := map[string]string{
		"GCAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA": "AAAAAQID",
		"GCBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB": "AQIDBAUG",
	}
	ps := &PersistedSnapshot{
		Metadata: &ReplayMetadata{
			SchemaVersion:   1,
			GlassboxVersion: "v0.9.0",
			TxHash:          strings.Repeat("f", 64),
			Network:         "testnet",
			SavedAt:         time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		Snapshot: FromMap(ledger),
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "v1-compat-fixture.snap.json")
	writeV1File(t, path, ps)

	// Load — must succeed and auto-migrate.
	loaded, err := LoadPersisted(path)
	if err != nil {
		t.Fatalf("backward compat load failed: %v", err)
	}

	// Version upgraded.
	if loaded.Metadata.SchemaVersion != PersistSchemaVersion {
		t.Errorf("expected v%d after compat load, got v%d",
			PersistSchemaVersion, loaded.Metadata.SchemaVersion)
	}

	// Ledger state untouched.
	got := loaded.Snapshot.ToMap()
	for k, v := range ledger {
		if got[k] != v {
			t.Errorf("compat fixture: ledger entry %q changed: %q → %q", k, v, got[k])
		}
	}

	// Validate passes (fingerprint + metadata identity).
	if err := loaded.Validate(strings.Repeat("f", 64), "testnet"); err != nil {
		t.Errorf("Validate after migration failed: %v", err)
	}
}
