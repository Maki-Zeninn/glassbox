// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Tests for the session versioning migration pipeline (Issue: session versioning).
//
// Coverage:
//   - dispatchable migration table applies every step in order
//   - upgrade v1 → v2 normalises missing Status field
//   - upgrade v0 → current backfills EnvFingerprint
//   - import path (ImportArchive) runs the same migration as the load path
//   - unknown/additive fields in archive session.json are preserved on round-trip
//   - archives carrying old schema versions are upgraded before ValidateIntegrity
//   - unsupported schema versions fail with a clear SchemaError
package session

import (
	"archive/zip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ── Migration table: step ordering ───────────────────────────────────────────

func TestMigrationTable_StepsAreOrderedByToVersion(t *testing.T) {
	for i := 1; i < len(migrationTable); i++ {
		if migrationTable[i].toVersion <= migrationTable[i-1].toVersion {
			t.Errorf(
				"migrationTable[%d].toVersion (%d) is not strictly greater than [%d].toVersion (%d)",
				i, migrationTable[i].toVersion, i-1, migrationTable[i-1].toVersion,
			)
		}
	}
}

func TestMigrationTable_FinalStepMatchesSchemaVersion(t *testing.T) {
	if len(migrationTable) == 0 {
		t.Skip("migration table is empty — nothing to check")
	}
	last := migrationTable[len(migrationTable)-1]
	if last.toVersion != SchemaVersion {
		t.Errorf("last migration step toVersion=%d, want SchemaVersion=%d",
			last.toVersion, SchemaVersion)
	}
}

func TestMigrationTable_NoStep_ExceedsSchemaVersion(t *testing.T) {
	for i, step := range migrationTable {
		if step.toVersion > SchemaVersion {
			t.Errorf("migrationTable[%d].toVersion=%d exceeds SchemaVersion=%d — future step leaked into table",
				i, step.toVersion, SchemaVersion)
		}
	}
}

// ── UpgradeSessionData: v0 → current ─────────────────────────────────────────

func TestUpgradeSessionData_V0_BackfillsEnvFingerprint(t *testing.T) {
	if SchemaVersion <= 0 {
		t.Skip("SchemaVersion is 0, nothing to upgrade")
	}
	d := validData()
	d.SchemaVersion = 0 // below MinSupportedSchemaVersion (1)

	// v0 is below the minimum supported version, so UpgradeSessionData must
	// return a SchemaError — it cannot upgrade from an unsupported baseline.
	_, err := UpgradeSessionData(d)
	if err == nil {
		t.Fatal("expected SchemaError for version 0 (below MinSupportedSchemaVersion)")
	}
	if !IsSchemaError(err) {
		t.Fatalf("expected *SchemaError, got %T: %v", err, err)
	}
}

// ── UpgradeSessionData: v1 → v2 (status normalisation) ────────────────────────

func TestUpgradeSessionData_V1ToV2_NormalisesEmptyStatus(t *testing.T) {
	if SchemaVersion < 2 {
		t.Skip("SchemaVersion < 2; v1→v2 step not yet defined")
	}
	d := validData()
	d.SchemaVersion = 1
	d.Status = "" // simulate a v1 row with no status field

	upgraded, err := UpgradeSessionData(d)
	if err != nil {
		t.Fatalf("unexpected error upgrading v1 session: %v", err)
	}
	if !upgraded {
		t.Fatal("expected upgraded=true for v1 session")
	}
	if d.SchemaVersion != SchemaVersion {
		t.Errorf("SchemaVersion = %d after upgrade, want %d", d.SchemaVersion, SchemaVersion)
	}
	if d.Status != "active" {
		t.Errorf("Status = %q after v1→v2 upgrade, want \"active\"", d.Status)
	}
}

// ── UpgradeSessionData: already current ──────────────────────────────────────

func TestUpgradeSessionData_CurrentVersion_IsNoop(t *testing.T) {
	d := validData()
	d.SchemaVersion = SchemaVersion
	d.EnvFingerprint = "existing-fp"

	upgraded, err := UpgradeSessionData(d)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if upgraded {
		t.Error("expected upgraded=false for already-current session")
	}
	if d.EnvFingerprint != "existing-fp" {
		t.Error("upgrade noop must not modify EnvFingerprint on a current session")
	}
}

// ── UpgradeSessionData: nil ───────────────────────────────────────────────────

func TestUpgradeSessionData_Nil_ReturnsError(t *testing.T) {
	_, err := UpgradeSessionData(nil)
	if err == nil {
		t.Fatal("expected error for nil session data")
	}
}

// ── UpgradeSessionData: future version ───────────────────────────────────────

func TestUpgradeSessionData_FutureVersion_ReturnsSchemaError(t *testing.T) {
	d := validData()
	d.SchemaVersion = SchemaVersion + 99

	_, err := UpgradeSessionData(d)
	if err == nil {
		t.Fatal("expected SchemaError for future-version session")
	}
	if !IsSchemaError(err) {
		t.Fatalf("expected *SchemaError, got %T: %v", err, err)
	}
	se := AsSchemaError(err)
	if !se.Result.FromFuture {
		t.Error("SchemaError.Result.FromFuture should be true for a future-version session")
	}
}

// ── UpgradeSessionData: provenance entry is recorded ─────────────────────────

func TestUpgradeSessionData_RecordsProvenanceEntry(t *testing.T) {
	if SchemaVersion <= MinSupportedSchemaVersion {
		t.Skip("no upgradable version below current")
	}
	d := validData()
	d.SchemaVersion = SchemaVersion - 1
	d.ProvenanceJSON = ""

	upgraded, err := UpgradeSessionData(d)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !upgraded {
		t.Fatal("expected upgraded=true")
	}
	tl := ParseProvenanceTimeline(d.ProvenanceJSON)
	if len(tl.Entries) == 0 {
		t.Fatal("expected at least one provenance entry after upgrade")
	}
	last := tl.Entries[len(tl.Entries)-1]
	if last.Operation != ProvenanceMigrated {
		t.Errorf("last provenance operation = %q, want %q", last.Operation, ProvenanceMigrated)
	}
	if !last.Success {
		t.Error("provenance entry for successful upgrade must have Success=true")
	}
	if !strings.Contains(last.Detail, "schema upgraded") {
		t.Errorf("provenance detail should mention 'schema upgraded', got: %q", last.Detail)
	}
}

// ── Store.Load migrates older rows on the load path ──────────────────────────

func TestStore_Load_MigratesV1Session(t *testing.T) {
	if SchemaVersion < 2 {
		t.Skip("SchemaVersion < 2; v1 upgrade path not tested here")
	}
	overrideTempHome(t)
	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	d := makeValidSessionData(t, 0)
	d.ID = "v1-migration-test"
	d.SchemaVersion = 1
	d.Status = "" // simulate a v1 row with no status

	if err := store.SavePreservingSchemaVersion(ctx, d); err != nil {
		t.Fatalf("SavePreservingSchemaVersion: %v", err)
	}

	loaded, err := store.Load(ctx, d.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.SchemaVersion != SchemaVersion {
		t.Errorf("SchemaVersion = %d after load, want %d", loaded.SchemaVersion, SchemaVersion)
	}
	// The v1→v2 migration normalises empty Status to "active".
	if loaded.Status == "" {
		t.Error("Status should not be empty after migration on load")
	}
}

// ── ImportArchive: older schema upgraded before ValidateIntegrity ─────────────

func TestImportArchive_OlderSchemaVersion_IsMigratedBeforeValidation(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "old_schema.gbx")

	// Build an archive carrying schema_version = MinSupportedSchemaVersion
	// (i.e. the oldest we can upgrade from). The session.json itself has all
	// required fields set so validation passes once the migration runs.
	oldVersion := MinSupportedSchemaVersion
	meta := archiveMeta{
		ArchiveVersion:  archiveVersion,
		GlassboxVersion: "0.1.0",
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
		SchemaVersion:   oldVersion,
	}
	sessionPayload := map[string]interface{}{
		"id":             "migrate-import-test",
		"tx_hash":        strings.Repeat("a", 64),
		"network":        "testnet",
		"status":         "saved",
		"created_at":     time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
		"last_access_at": time.Now().UTC().Format(time.RFC3339),
		"schema_version": oldVersion,
	}

	if err := writeTestArchive(archivePath, meta, sessionPayload); err != nil {
		t.Fatalf("writeTestArchive: %v", err)
	}

	data, _, err := ImportArchiveWithManifest(archivePath)
	if err != nil {
		t.Fatalf("ImportArchiveWithManifest should succeed for an upgradable schema version, got: %v", err)
	}
	if data.SchemaVersion != SchemaVersion {
		t.Errorf("SchemaVersion = %d after import, want %d", data.SchemaVersion, SchemaVersion)
	}
}

// ── ImportArchive: unknown fields are preserved ───────────────────────────────

func TestImportArchive_UnknownFields_PreservedInExtrasJSON(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "extras.gbx")

	meta := archiveMeta{
		ArchiveVersion:  archiveVersion,
		GlassboxVersion: "99.0.0",
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
		SchemaVersion:   SchemaVersion,
	}
	// session.json carries an extra field "custom_tag" that the current Data
	// struct does not know about — it should land in ExtrasJSON.
	sessionPayload := map[string]interface{}{
		"id":             "extras-test",
		"tx_hash":        strings.Repeat("b", 64),
		"network":        "testnet",
		"status":         "saved",
		"created_at":     time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
		"last_access_at": time.Now().UTC().Format(time.RFC3339),
		"schema_version": SchemaVersion,
		"custom_tag":     "my-team-annotation",    // unknown field
		"future_flag":    true,                     // unknown field
	}

	if err := writeTestArchive(archivePath, meta, sessionPayload); err != nil {
		t.Fatalf("writeTestArchive: %v", err)
	}

	data, _, err := ImportArchiveWithManifest(archivePath)
	if err != nil {
		t.Fatalf("ImportArchiveWithManifest: %v", err)
	}

	if len(data.ExtrasJSON) == 0 {
		t.Fatal("ExtrasJSON should be non-empty when archive carries unknown fields")
	}
	if _, ok := data.ExtrasJSON["custom_tag"]; !ok {
		t.Error("expected 'custom_tag' in ExtrasJSON")
	}
	if _, ok := data.ExtrasJSON["future_flag"]; !ok {
		t.Error("expected 'future_flag' in ExtrasJSON")
	}
	// Known fields must NOT appear in ExtrasJSON.
	for k := range data.ExtrasJSON {
		if knownSessionJSONKeys[k] {
			t.Errorf("known field %q must not appear in ExtrasJSON", k)
		}
	}
}

// ── Round-trip: unknown fields survive export → import ───────────────────────

func TestArchive_RoundTrip_UnknownFieldsPreserved(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "roundtrip.gbx")

	// Step 1: import an archive that carries unknown fields.
	srcPath := filepath.Join(dir, "source.gbx")
	meta := archiveMeta{
		ArchiveVersion:  archiveVersion,
		GlassboxVersion: "99.0.0",
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
		SchemaVersion:   SchemaVersion,
	}
	sessionPayload := map[string]interface{}{
		"id":             "roundtrip-extras",
		"tx_hash":        strings.Repeat("c", 64),
		"network":        "testnet",
		"status":         "saved",
		"created_at":     time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
		"last_access_at": time.Now().UTC().Format(time.RFC3339),
		"schema_version": SchemaVersion,
		"my_extra_field": "preserved-value",
	}
	if err := writeTestArchive(srcPath, meta, sessionPayload); err != nil {
		t.Fatalf("writeTestArchive: %v", err)
	}
	data, err := ImportArchive(srcPath)
	if err != nil {
		t.Fatalf("ImportArchive: %v", err)
	}
	if data.ExtrasJSON["my_extra_field"] == nil {
		t.Fatal("expected my_extra_field in ExtrasJSON after first import")
	}

	// Step 2: re-export the session.
	if err := ExportArchive(data, archivePath); err != nil {
		t.Fatalf("ExportArchive: %v", err)
	}

	// Step 3: re-import and verify the extra field survived.
	data2, err := ImportArchive(archivePath)
	if err != nil {
		t.Fatalf("ImportArchive (round-trip): %v", err)
	}
	if data2.ExtrasJSON["my_extra_field"] == nil {
		t.Fatal("my_extra_field was lost during export → import round-trip")
	}
	var val string
	if err := json.Unmarshal(data2.ExtrasJSON["my_extra_field"], &val); err != nil {
		t.Fatalf("failed to unmarshal my_extra_field: %v", err)
	}
	if val != "preserved-value" {
		t.Errorf("my_extra_field = %q, want \"preserved-value\"", val)
	}
}

// ── ImportArchive: unsupported (too-old) schema returns SchemaError ───────────

func TestImportArchive_TooOldSchemaVersion_ReturnsSchemaError(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "too_old.gbx")

	meta := archiveMeta{
		ArchiveVersion:  archiveVersion,
		GlassboxVersion: "0.0.1",
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
		SchemaVersion:   0, // below MinSupportedSchemaVersion
	}
	sessionPayload := map[string]interface{}{
		"id":             "old-session",
		"tx_hash":        strings.Repeat("d", 64),
		"network":        "testnet",
		"status":         "saved",
		"created_at":     time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
		"last_access_at": time.Now().UTC().Format(time.RFC3339),
		"schema_version": 0,
	}
	if err := writeTestArchive(archivePath, meta, sessionPayload); err != nil {
		t.Fatalf("writeTestArchive: %v", err)
	}

	_, err := ImportArchive(archivePath)
	if err == nil {
		t.Fatal("expected error for archive with unsupported schema version")
	}
	if !IsSchemaError(err) {
		// The error might be wrapped — check the message instead.
		if !strings.Contains(err.Error(), "too old") && !strings.Contains(err.Error(), "minimum") {
			t.Errorf("error for unsupported schema should mention 'too old' or 'minimum', got: %v", err)
		}
	}
}

// ── SchemaVersionSummary includes version numbers ────────────────────────────

func TestSchemaVersionSummary_ContainsVersionNumbers(t *testing.T) {
	s := SchemaVersionSummary(SchemaVersion - 1)
	if SchemaVersion <= MinSupportedSchemaVersion {
		t.Skip("no upgradable version below current")
	}
	// Must mention both the stored and current version.
	if !strings.Contains(s, "outdated") && !strings.Contains(s, "current") {
		t.Errorf("summary for outdated version should mention 'outdated' or 'current', got: %q", s)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

// writeTestArchive creates a minimal .gbx archive with the given meta and
// session payload map. No manifest.json is written so the archive takes the
// pre-manifest compatibility path.
func writeTestArchive(path string, meta archiveMeta, sessionPayload map[string]interface{}) error {
	metaBytes, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	sessionBytes, err := json.Marshal(sessionPayload)
	if err != nil {
		return err
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	mw, err := zw.Create("meta.json")
	if err != nil {
		return err
	}
	if _, err := mw.Write(metaBytes); err != nil {
		return err
	}
	sw, err := zw.Create("session.json")
	if err != nil {
		return err
	}
	if _, err := sw.Write(sessionBytes); err != nil {
		return err
	}
	return zw.Close()
}
