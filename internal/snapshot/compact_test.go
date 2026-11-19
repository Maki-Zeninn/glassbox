// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBuildReferenceModel(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir := t.TempDir()

	// Create a test registry file with snapshot references
	registryDir := filepath.Join(tmpDir, "registries")
	if err := os.MkdirAll(registryDir, 0755); err != nil {
		t.Fatalf("failed to create registry directory: %v", err)
	}

	testRegistry := struct {
		Entries []struct {
			Timestamp int64    `json:"timestamp"`
			Snapshot  *Snapshot `json:"snapshot"`
		} `json:"entries"`
	}{
		Entries: []struct {
			Timestamp int64    `json:"timestamp"`
			Snapshot  *Snapshot `json:"snapshot"`
		}{
			{
				Timestamp: 1234567890,
				Snapshot:  FromMap(map[string]string{"key1": "val1"}),
			},
		},
	}

	registryPath := filepath.Join(registryDir, "test-registry.json")
	registryData, err := json.MarshalIndent(testRegistry, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal test registry: %v", err)
	}
	if err := os.WriteFile(registryPath, registryData, 0644); err != nil {
		t.Fatalf("failed to write test registry: %v", err)
	}

	// Build reference model
	model, err := BuildReferenceModel(tmpDir)
	if err != nil {
		t.Fatalf("BuildReferenceModel failed: %v", err)
	}

	// Verify that the model was built
	if model == nil {
		t.Fatal("expected non-nil model")
	}

	// Verify that registry references were found
	if len(model.RegistryReferences) == 0 {
		t.Error("expected at least one registry reference")
	}

	// Verify that AllReferenced was populated
	if len(model.AllReferenced) == 0 {
		t.Error("expected at least one referenced hash")
	}
}

func TestCompactLedgerStateDryRun(t *testing.T) {
	tmpDir := t.TempDir()
	dedupStore := NewDedupStore(tmpDir)

	// Create some test snapshots
	meta := &ReplayMetadata{
		SchemaVersion:   PersistSchemaVersion,
		GlassboxVersion: "test",
		TxHash:          "test-tx-hash",
		Network:         "testnet",
		SavedAt:         time.Now().UTC(),
	}

	snap1 := FromMap(map[string]string{"key1": "val1"})
	snap2 := FromMap(map[string]string{"key2": "val2"})

	hash1, _, err := dedupStore.SaveWithDedup(meta, snap1)
	if err != nil {
		t.Fatalf("failed to save snapshot 1: %v", err)
	}

	hash2, _, err := dedupStore.SaveWithDedup(meta, snap2)
	if err != nil {
		t.Fatalf("failed to save snapshot 2: %v", err)
	}

	// Run dry-run compaction
	opts := CompactionOptions{
		DryRun: true,
	}

	report, err := CompactLedgerState(dedupStore, opts)
	if err != nil {
		t.Fatalf("CompactLedgerState dry-run failed: %v", err)
	}

	// Verify report
	if report.BeforeSize == 0 {
		t.Error("expected non-zero before size")
	}

	// In dry-run, nothing should be removed
	if len(report.RemovedHashes) != 0 {
		t.Errorf("expected no hashes to be removed in dry-run, got %d", len(report.RemovedHashes))
	}

	// Verify that snapshots still exist
	if _, err := dedupStore.LoadByHash(hash1); err != nil {
		t.Errorf("snapshot 1 should still exist after dry-run: %v", err)
	}
	if _, err := dedupStore.LoadByHash(hash2); err != nil {
		t.Errorf("snapshot 2 should still exist after dry-run: %v", err)
	}
}

func TestCompactLedgerStateWithMinAge(t *testing.T) {
	tmpDir := t.TempDir()
	dedupStore := NewDedupStore(tmpDir)

	// Create an old snapshot and a new snapshot
	meta := &ReplayMetadata{
		SchemaVersion:   PersistSchemaVersion,
		GlassboxVersion: "test",
		TxHash:          "test-tx-hash",
		Network:         "testnet",
		SavedAt:         time.Now().UTC(),
	}

	oldSnap := FromMap(map[string]string{"old-key": "old-val"})
	newSnap := FromMap(map[string]string{"new-key": "new-val"})

	oldHash, _, err := dedupStore.SaveWithDedup(meta, oldSnap)
	if err != nil {
		t.Fatalf("failed to save old snapshot: %v", err)
	}

	newHash, _, err := dedupStore.SaveWithDedup(meta, newSnap)
	if err != nil {
		t.Fatalf("failed to save new snapshot: %v", err)
	}

	// Make the old snapshot actually old on disk
	oldPath := filepath.Join(tmpDir, "snapshots", hashToFilename(oldHash))
	oldTime := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatalf("failed to set old snapshot time: %v", err)
	}

	// Run compaction with min-age of 24 hours
	opts := CompactionOptions{
		MinAge: 24 * time.Hour,
	}

	report, err := CompactLedgerState(dedupStore, opts)
	if err != nil {
		t.Fatalf("CompactLedgerState failed: %v", err)
	}

	// Both snapshots should be preserved since they're unreferenced but we're
	// not actually removing them in this test (no references exist)
	if len(report.PreservedHashes) == 0 {
		t.Error("expected at least one preserved hash due to age")
	}
}

func TestCompactLedgerStateWithPreserveHashes(t *testing.T) {
	tmpDir := t.TempDir()
	dedupStore := NewDedupStore(tmpDir)

	meta := &ReplayMetadata{
		SchemaVersion:   PersistSchemaVersion,
		GlassboxVersion: "test",
		TxHash:          "test-tx-hash",
		Network:         "testnet",
		SavedAt:         time.Now().UTC(),
	}

	snap1 := FromMap(map[string]string{"key1": "val1"})
	snap2 := FromMap(map[string]string{"key2": "val2"})

	hash1, _, err := dedupStore.SaveWithDedup(meta, snap1)
	if err != nil {
		t.Fatalf("failed to save snapshot 1: %v", err)
	}

	hash2, _, err := dedupStore.SaveWithDedup(meta, snap2)
	if err != nil {
		t.Fatalf("failed to save snapshot 2: %v", err)
	}

	// Run compaction preserving hash1
	opts := CompactionOptions{
		PreserveHashes: []string{hash1},
	}

	report, err := CompactLedgerState(dedupStore, opts)
	if err != nil {
		t.Fatalf("CompactLedgerState failed: %v", err)
	}

	// Verify that hash1 was preserved
	found := false
	for _, h := range report.PreservedHashes {
		if h == hash1 {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected hash1 to be preserved")
	}
}

func TestCompactWithAtomicCommit(t *testing.T) {
	tmpDir := t.TempDir()
	dedupStore := NewDedupStore(tmpDir)

	// Create test snapshots
	meta := &ReplayMetadata{
		SchemaVersion:   PersistSchemaVersion,
		GlassboxVersion: "test",
		TxHash:          "test-tx-hash",
		Network:         "testnet",
		SavedAt:         time.Now().UTC(),
	}

	snap := FromMap(map[string]string{"key": "val"})
	hash, _, err := dedupStore.SaveWithDedup(meta, snap)
	if err != nil {
		t.Fatalf("failed to save snapshot: %v", err)
	}

	// Verify snapshot exists
	if _, err := dedupStore.LoadByHash(hash); err != nil {
		t.Fatalf("snapshot should exist before compaction: %v", err)
	}

	// Run atomic compaction
	toRemove := []string{hash}
	if err := compactWithAtomicCommit(dedupStore, toRemove, false); err != nil {
		t.Fatalf("compactWithAtomicCommit failed: %v", err)
	}

	// Verify snapshot was removed
	if _, err := dedupStore.LoadByHash(hash); err == nil {
		t.Error("snapshot should be removed after compaction")
	}

	// Verify recovery manifest was cleaned up
	recoveryPath := filepath.Join(tmpDir, "snapshots") + ".recovery"
	if _, err := os.Stat(recoveryPath); !os.IsNotExist(err) {
		t.Error("recovery manifest should be removed after successful compaction")
	}
}

func TestRecoverFromInterruptedCompaction(t *testing.T) {
	tmpDir := t.TempDir()
	dedupStore := NewDedupStore(tmpDir)

	// Create a fake recovery manifest
	snapshotDir := filepath.Join(tmpDir, "snapshots")
	if err := os.MkdirAll(snapshotDir, 0755); err != nil {
		t.Fatalf("failed to create snapshot directory: %v", err)
	}

	recoveryPath := snapshotDir + ".recovery"
	recoveryManifest := struct {
		ToRemove  []string `json:"to_remove"`
		Timestamp string   `json:"timestamp"`
		Checksum  string   `json:"checksum"`
	}{
		ToRemove:  []string{"test-hash"},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	manifestData, _ := json.Marshal(recoveryManifest.ToRemove)
	sum := sha256.Sum256(manifestData)
	recoveryManifest.Checksum = hex.EncodeToString(sum[:])

	manifestBytes, err := json.MarshalIndent(recoveryManifest, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal recovery manifest: %v", err)
	}

	if err := os.WriteFile(recoveryPath, manifestBytes, 0644); err != nil {
		t.Fatalf("failed to write recovery manifest: %v", err)
	}

	// Attempt to recover
	err = RecoverFromInterruptedCompaction(dedupStore)
	if err == nil {
		t.Error("expected error when recovery manifest exists")
	}

	// Verify the error mentions the recovery manifest
	if err != nil && !containsString(err.Error(), "interrupted compaction") {
		t.Errorf("expected error about interrupted compaction, got: %v", err)
	}

	// Clean up
	os.Remove(recoveryPath)
}

func TestValidateCompactionSafety(t *testing.T) {
	tmpDir := t.TempDir()
	dedupStore := NewDedupStore(tmpDir)

	opts := CompactionOptions{}

	// Test normal case
	if err := ValidateCompactionSafety(dedupStore, opts); err != nil {
		t.Errorf("ValidateCompactionSafety should succeed for normal case: %v", err)
	}

	// Test with interrupted compaction
	snapshotDir := filepath.Join(tmpDir, "snapshots")
	if err := os.MkdirAll(snapshotDir, 0755); err != nil {
		t.Fatalf("failed to create snapshot directory: %v", err)
	}

	recoveryPath := snapshotDir + ".recovery"
	recoveryManifest := struct {
		ToRemove  []string `json:"to_remove"`
		Timestamp string   `json:"timestamp"`
		Checksum  string   `json:"checksum"`
	}{
		ToRemove:  []string{"test-hash"},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	manifestData, _ := json.Marshal(recoveryManifest.ToRemove)
	sum := sha256.Sum256(manifestData)
	recoveryManifest.Checksum = hex.EncodeToString(sum[:])

	manifestBytes, _ := json.MarshalIndent(recoveryManifest, "", "  ")
	os.WriteFile(recoveryPath, manifestBytes, 0644)

	// Should fail without force flag
	if err := ValidateCompactionSafety(dedupStore, opts); err == nil {
		t.Error("expected safety check to fail with interrupted compaction")
	}

	// Should succeed with force flag
	forceOpts := CompactionOptions{Force: true}
	if err := ValidateCompactionSafety(dedupStore, forceOpts); err != nil {
		t.Errorf("ValidateCompactionSafety with force should succeed: %v", err)
	}

	// Clean up
	os.Remove(recoveryPath)
}

func TestCompactPermissions(t *testing.T) {
	// This test verifies that compaction respects directory permissions
	tmpDir := t.TempDir()
	dedupStore := NewDedupStore(tmpDir)

	// Create snapshot directory
	snapshotDir := filepath.Join(tmpDir, "snapshots")
	if err := os.MkdirAll(snapshotDir, 0755); err != nil {
		t.Fatalf("failed to create snapshot directory: %v", err)
	}

	// Test write permission check
	opts := CompactionOptions{}
	if err := ValidateCompactionSafety(dedupStore, opts); err != nil {
		t.Errorf("permission check should succeed: %v", err)
	}
}

func TestRoundTripReplayConsistency(t *testing.T) {
	tmpDir := t.TempDir()
	dedupStore := NewDedupStore(tmpDir)

	// Create a snapshot with known content
	originalState := map[string]string{
		"key1": "value1",
		"key2": "value2",
		"key3": "value3",
	}

	meta := &ReplayMetadata{
		SchemaVersion:   PersistSchemaVersion,
		GlassboxVersion: "test",
		TxHash:          "test-tx-hash",
		Network:         "testnet",
		SavedAt:         time.Now().UTC(),
	}

	originalSnap := FromMap(originalState)
	hash, _, err := dedupStore.SaveWithDedup(meta, originalSnap)
	if err != nil {
		t.Fatalf("failed to save original snapshot: %v", err)
	}

	// Load the snapshot
	loadedPS, err := dedupStore.LoadByHash(hash)
	if err != nil {
		t.Fatalf("failed to load snapshot: %v", err)
	}

	// Convert back to map
	loadedState := loadedPS.Snapshot.ToMap()

	// Verify consistency
	if len(loadedState) != len(originalState) {
		t.Errorf("state size mismatch: original=%d, loaded=%d", len(originalState), len(loadedState))
	}

	for key, originalValue := range originalState {
		loadedValue, exists := loadedState[key]
		if !exists {
			t.Errorf("key %s missing from loaded state", key)
		}
		if loadedValue != originalValue {
			t.Errorf("value mismatch for key %s: original=%s, loaded=%s", key, originalValue, loadedValue)
		}
	}

	// Verify fingerprint consistency
	originalFingerprint := ComputeFingerprint(originalSnap)
	loadedFingerprint := ComputeFingerprint(loadedPS.Snapshot)

	if originalFingerprint != loadedFingerprint {
		t.Errorf("fingerprint mismatch: original=%s, loaded=%s", originalFingerprint, loadedFingerprint)
	}
}

func TestDuplicateKeyHandling(t *testing.T) {
	tmpDir := t.TempDir()
	dedupStore := NewDedupStore(tmpDir)

	meta := &ReplayMetadata{
		SchemaVersion:   PersistSchemaVersion,
		GlassboxVersion: "test",
		TxHash:          "test-tx-hash",
		Network:         "testnet",
		SavedAt:         time.Now().UTC(),
	}

	snap := FromMap(map[string]string{"key": "val"})

	// Save the same snapshot twice - should deduplicate
	hash1, isNew1, err := dedupStore.SaveWithDedup(meta, snap)
	if err != nil {
		t.Fatalf("first save failed: %v", err)
	}

	hash2, isNew2, err := dedupStore.SaveWithDedup(meta, snap)
	if err != nil {
		t.Fatalf("second save failed: %v", err)
	}

	// Verify deduplication
	if hash1 != hash2 {
		t.Error("expected same hash for identical snapshots")
	}

	if isNew1 && isNew2 {
		t.Error("expected second save to not be new (deduplicated)")
	}

	if !isNew1 {
		t.Error("expected first save to be new")
	}

	// Verify only one file exists
	snapshotDir := filepath.Join(tmpDir, "snapshots")
	entries, err := os.ReadDir(snapshotDir)
	if err != nil {
		t.Fatalf("failed to read snapshot directory: %v", err)
	}

	snapCount := 0
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
			snapCount++
		}
	}

	if snapCount != 1 {
		t.Errorf("expected 1 snapshot file, got %d", snapCount)
	}
}

func TestConcurrentReaderSafety(t *testing.T) {
	tmpDir := t.TempDir()
	dedupStore := NewDedupStore(tmpDir)

	meta := &ReplayMetadata{
		SchemaVersion:   PersistSchemaVersion,
		GlassboxVersion: "test",
		TxHash:          "test-tx-hash",
		Network:         "testnet",
		SavedAt:         time.Now().UTC(),
	}

	snap := FromMap(map[string]string{"key": "val"})
	hash, _, err := dedupStore.SaveWithDedup(meta, snap)
	if err != nil {
		t.Fatalf("failed to save snapshot: %v", err)
	}

	// Simulate concurrent reads
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			_, err := dedupStore.LoadByHash(hash)
			if err != nil {
				t.Errorf("concurrent read failed: %v", err)
			}
			done <- true
		}()
	}

	// Wait for all reads to complete
	for i := 0; i < 10; i++ {
		<-done
	}
}

// Helper function
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && 
		(s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || 
		findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
