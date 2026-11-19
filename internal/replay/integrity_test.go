// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package replay

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotandev/glassbox/internal/snapshot"
)

// ── EntryContentHash ──────────────────────────────────────────────────────────

func TestEntryContentHash_NilEntry(t *testing.T) {
	h, err := EntryContentHash(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h != "" {
		t.Errorf("expected empty hash for nil entry, got %q", h)
	}
}

func TestEntryContentHash_NilSnapshot(t *testing.T) {
	e := &Entry{Timestamp: 1000, Snapshot: nil}
	h, err := EntryContentHash(e)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h != "" {
		t.Errorf("expected empty hash for nil snapshot, got %q", h)
	}
}

func TestEntryContentHash_Deterministic(t *testing.T) {
	snap := snapshot.FromMap(map[string]string{"key-a": "val-a", "key-b": "val-b"})
	e := &Entry{Timestamp: 999, Snapshot: snap}

	h1, err1 := EntryContentHash(e)
	h2, err2 := EntryContentHash(e)
	if err1 != nil || err2 != nil {
		t.Fatalf("errors: %v, %v", err1, err2)
	}
	if h1 != h2 {
		t.Errorf("hashes not deterministic: %q vs %q", h1, h2)
	}
}

// Hash must be the same regardless of the insertion order of ledger entries.
func TestEntryContentHash_FieldOrderIndependent(t *testing.T) {
	// Two maps with identical key-value pairs in different iteration order.
	m1 := map[string]string{"aaa": "1", "bbb": "2", "ccc": "3"}
	m2 := map[string]string{"ccc": "3", "aaa": "1", "bbb": "2"}

	snap1 := snapshot.FromMap(m1)
	snap2 := snapshot.FromMap(m2)

	e1 := &Entry{Timestamp: 1, Snapshot: snap1}
	e2 := &Entry{Timestamp: 1, Snapshot: snap2}

	h1, _ := EntryContentHash(e1)
	h2, _ := EntryContentHash(e2)
	if h1 != h2 {
		t.Errorf("hash differs by insertion order: %q vs %q", h1, h2)
	}
}

// Whitespace differences in values must change the hash.
func TestEntryContentHash_NewlineDifferenceChangesHash(t *testing.T) {
	snap1 := snapshot.FromMap(map[string]string{"k": "value"})
	snap2 := snapshot.FromMap(map[string]string{"k": "value\n"})

	e1 := &Entry{Timestamp: 1, Snapshot: snap1}
	e2 := &Entry{Timestamp: 1, Snapshot: snap2}

	h1, _ := EntryContentHash(e1)
	h2, _ := EntryContentHash(e2)
	if h1 == h2 {
		t.Error("expected different hashes for values differing by trailing newline")
	}
}

// Timestamp change alone must change the hash.
func TestEntryContentHash_TimestampChangesHash(t *testing.T) {
	snap := snapshot.FromMap(map[string]string{"k": "v"})
	e1 := &Entry{Timestamp: 1000, Snapshot: snap}
	e2 := &Entry{Timestamp: 2000, Snapshot: snap}

	h1, _ := EntryContentHash(e1)
	h2, _ := EntryContentHash(e2)
	if h1 == h2 {
		t.Error("expected different hashes for different timestamps")
	}
}

// Hash must change when a ledger value is modified.
func TestEntryContentHash_TamperedValueChangesHash(t *testing.T) {
	snap := snapshot.FromMap(map[string]string{"k": "original-value"})
	e := &Entry{Timestamp: 1, Snapshot: snap}
	original, _ := EntryContentHash(e)

	// Simulate tampering by changing a ledger entry value in-place.
	e.Snapshot.LedgerEntries[0][1] = "tampered-value"
	// Also reset the fingerprint so it won't short-circuit
	e.Snapshot.Fingerprint = ""

	tampered, _ := EntryContentHash(e)
	if original == tampered {
		t.Error("expected different hash after tampering ledger entry value")
	}
}

// ── marshalCanonicalJSON ──────────────────────────────────────────────────────

func TestMarshalCanonicalJSON_SortsKeys(t *testing.T) {
	// A struct with fields that would have non-alphabetical key order.
	type testStruct struct {
		Zebra  string `json:"zebra"`
		Apple  string `json:"apple"`
		Mango  string `json:"mango"`
	}
	s := testStruct{Zebra: "z", Apple: "a", Mango: "m"}
	data, err := marshalCanonicalJSON(s)
	if err != nil {
		t.Fatalf("marshalCanonicalJSON: %v", err)
	}
	raw := string(data)
	appleIdx := strings.Index(raw, `"apple"`)
	mangoIdx := strings.Index(raw, `"mango"`)
	zebraIdx := strings.Index(raw, `"zebra"`)
	if !(appleIdx < mangoIdx && mangoIdx < zebraIdx) {
		t.Errorf("keys not in lexicographic order: %s", raw)
	}
}

func TestMarshalCanonicalJSON_NoExtraWhitespace(t *testing.T) {
	data, err := marshalCanonicalJSON(map[string]string{"k": "v"})
	if err != nil {
		t.Fatalf("marshalCanonicalJSON: %v", err)
	}
	if strings.Contains(string(data), "  ") || strings.Contains(string(data), "\n") {
		t.Errorf("expected compact JSON, got: %s", data)
	}
}

// ── VerifyIntegrityFull ───────────────────────────────────────────────────────

func TestVerifyIntegrityFull_AllOK(t *testing.T) {
	r := newTestRegistry(t)
	r.Add(1000, makeSnap(t, map[string]string{"k": "v"}))
	r.Add(2000, makeSnap(t, map[string]string{"k2": "v2"}))

	report := r.VerifyIntegrityFull()

	if !report.Passed() {
		t.Errorf("expected passed, tampered=%d errors=%d", report.TamperedCount, report.ErrorCount)
	}
	if report.OKCount != 2 {
		t.Errorf("expected 2 OK, got %d", report.OKCount)
	}
	if report.LegacyCount != 0 {
		t.Errorf("expected 0 legacy, got %d", report.LegacyCount)
	}
}

// Legacy entry: ContentHash == "" should be back-filled, not treated as tampered.
func TestVerifyIntegrityFull_LegacyBackfill(t *testing.T) {
	r := newTestRegistry(t)
	snap := makeSnap(t, map[string]string{"key": "val"})
	// Insert entry manually without ContentHash to simulate a legacy registry.
	r.Entries = append(r.Entries, Entry{
		Timestamp:   500,
		Snapshot:    snap,
		Checksum:    computeSnapshotChecksum(snap),
		ContentHash: "", // legacy: no hash
	})

	report := r.VerifyIntegrityFull()

	if !report.Passed() {
		t.Errorf("expected passed for legacy entry, errors: %v", report.Errors())
	}
	if report.LegacyCount != 1 {
		t.Errorf("expected 1 legacy entry, got %d", report.LegacyCount)
	}
	if !report.BackfillApplied {
		t.Error("expected BackfillApplied to be true")
	}
	// Hash must have been set in-memory.
	if r.Entries[0].ContentHash == "" {
		t.Error("expected ContentHash to be back-filled in memory")
	}
}

// Tampered entry: ContentHash non-empty and wrong → IntegrityTampered.
func TestVerifyIntegrityFull_TamperedEntry(t *testing.T) {
	r := newTestRegistry(t)
	snap := makeSnap(t, map[string]string{"k": "v"})
	r.Add(1000, snap)
	// Corrupt the stored hash.
	r.Entries[0].ContentHash = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"

	report := r.VerifyIntegrityFull()

	if report.Passed() {
		t.Error("expected failed for tampered entry")
	}
	if report.TamperedCount != 1 {
		t.Errorf("expected 1 tampered, got %d", report.TamperedCount)
	}
	// The in-memory hash must NOT be overwritten for tampered entries.
	if r.Entries[0].ContentHash != "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef" {
		t.Error("tampered hash should not be overwritten in memory")
	}
}

func TestVerifyIntegrityFull_MultipleEntries_OneTampered(t *testing.T) {
	r := newTestRegistry(t)
	r.Add(100, makeSnap(t, map[string]string{"a": "1"}))
	r.Add(200, makeSnap(t, map[string]string{"b": "2"}))
	r.Add(300, makeSnap(t, map[string]string{"c": "3"}))
	// Tamper only the middle entry.
	r.Entries[1].ContentHash = "badhash"

	report := r.VerifyIntegrityFull()

	if report.TamperedCount != 1 {
		t.Errorf("expected 1 tampered, got %d", report.TamperedCount)
	}
	if report.OKCount != 2 {
		t.Errorf("expected 2 OK, got %d", report.OKCount)
	}
}

func TestVerifyIntegrityFull_Algorithm(t *testing.T) {
	r := newTestRegistry(t)
	r.Add(0, makeSnap(t, nil))
	report := r.VerifyIntegrityFull()
	if report.Algorithm != IntegrityAlgorithm {
		t.Errorf("expected algorithm %q, got %q", IntegrityAlgorithm, report.Algorithm)
	}
}

// ── Tamper-fixture round-trip ─────────────────────────────────────────────────
//
// Save a valid registry to disk, modify the file content to simulate
// tampering, reload, and assert VerifyIntegrityFull detects the tamper.

func TestTamperFixture_FileModifiedAfterSave(t *testing.T) {
	r := newTestRegistry(t)
	r.Add(1000, makeSnap(t, map[string]string{"ledger-key": "original-value"}))

	dir := t.TempDir()
	path := filepath.Join(dir, "registry.json")
	if err := r.SaveToFile(path); err != nil {
		t.Fatalf("SaveToFile: %v", err)
	}

	// Read the file and replace the ledger value to simulate tampering.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	tampered := strings.ReplaceAll(string(raw), "original-value", "tampered-value")
	if err := os.WriteFile(path, []byte(tampered), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Reload and verify.
	loaded, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	report := loaded.VerifyIntegrityFull()
	if report.Passed() {
		t.Error("expected integrity failure after file was tampered")
	}
	if report.TamperedCount != 1 {
		t.Errorf("expected 1 tampered entry, got %d", report.TamperedCount)
	}
}

func TestTamperFixture_AddKeyAfterSave(t *testing.T) {
	r := newTestRegistry(t)
	r.Add(1000, makeSnap(t, map[string]string{"k1": "v1"}))

	dir := t.TempDir()
	path := filepath.Join(dir, "registry.json")
	if err := r.SaveToFile(path); err != nil {
		t.Fatalf("SaveToFile: %v", err)
	}

	// Parse the file, inject an extra ledger entry.
	var raw map[string]interface{}
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	entries := raw["entries"].([]interface{})
	entry0 := entries[0].(map[string]interface{})
	snap := entry0["snapshot"].(map[string]interface{})
	ledgerEntries := snap["ledgerEntries"].([]interface{})
	// Inject an extra entry.
	ledgerEntries = append(ledgerEntries, []interface{}{"injected-key", "injected-val"})
	snap["ledgerEntries"] = ledgerEntries
	modified, _ := json.MarshalIndent(raw, "", "  ")
	if err := os.WriteFile(path, modified, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	loaded, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	report := loaded.VerifyIntegrityFull()
	if report.Passed() {
		t.Error("expected integrity failure after injecting ledger entry")
	}
}

// ── Repair round-trip ─────────────────────────────────────────────────────────

func TestRepair_LegacyRegistryGetsHashes(t *testing.T) {
	// Build a registry with no ContentHash (legacy).
	r := newTestRegistry(t)
	snap := makeSnap(t, map[string]string{"a": "1", "b": "2"})
	r.Entries = append(r.Entries, Entry{
		Timestamp:   1000,
		Snapshot:    snap,
		Checksum:    computeSnapshotChecksum(snap),
		ContentHash: "",
	})

	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.json")
	if err := r.SaveToFile(path); err != nil {
		t.Fatalf("SaveToFile: %v", err)
	}

	// Verify detects legacy, back-fills in memory, then save.
	loaded, _ := LoadFromFile(path)
	report := loaded.VerifyIntegrityFull()
	if report.LegacyCount != 1 {
		t.Fatalf("expected 1 legacy, got %d", report.LegacyCount)
	}
	if err := loaded.SaveToFile(path); err != nil {
		t.Fatalf("SaveToFile after repair: %v", err)
	}

	// Re-load and verify — should now be fully OK with no legacy entries.
	reloaded, _ := LoadFromFile(path)
	report2 := reloaded.VerifyIntegrityFull()
	if !report2.Passed() {
		t.Errorf("expected all OK after repair: %v", report2.Errors())
	}
	if report2.LegacyCount != 0 {
		t.Errorf("expected 0 legacy after repair, got %d", report2.LegacyCount)
	}
	if report2.OKCount != 1 {
		t.Errorf("expected 1 OK, got %d", report2.OKCount)
	}
}

// ── Cross-platform reproducibility ───────────────────────────────────────────
//
// The canonical hash must be identical no matter the platform.
// We test this by computing the hash on two identical snapshots built
// independently and asserting they match.

func TestEntryContentHash_CrossPlatformReproducibility(t *testing.T) {
	// Build the same snapshot twice from scratch.
	build := func() string {
		snap := snapshot.FromMap(map[string]string{
			"GCBBBBB": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA==",
			"GCAAAAA": "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB==",
		})
		e := &Entry{Timestamp: 12345678, Snapshot: snap}
		h, err := EntryContentHash(e)
		if err != nil {
			t.Fatalf("EntryContentHash: %v", err)
		}
		return h
	}

	h1 := build()
	h2 := build()
	if h1 != h2 {
		t.Errorf("hashes differ across independent builds: %q vs %q", h1, h2)
	}
	// Also assert the hash is non-empty and 64 hex chars (SHA-256).
	if len(h1) != 64 {
		t.Errorf("expected 64-char SHA-256 hex digest, got length %d: %q", len(h1), h1)
	}
}

// ── EntryIntegrityResult.String ───────────────────────────────────────────────

func TestEntryIntegrityResult_String_OK(t *testing.T) {
	r := EntryIntegrityResult{
		Index: 0, Timestamp: 1000, Status: IntegrityOK,
		ComputedHash: strings.Repeat("a", 64),
	}
	s := r.String()
	if !strings.Contains(s, "OK") {
		t.Errorf("expected OK in string, got: %s", s)
	}
}

func TestEntryIntegrityResult_String_Legacy(t *testing.T) {
	r := EntryIntegrityResult{
		Index: 1, Timestamp: 2000, Status: IntegrityLegacy,
		ComputedHash: strings.Repeat("b", 64),
	}
	s := r.String()
	if !strings.Contains(s, "LEGACY") {
		t.Errorf("expected LEGACY in string, got: %s", s)
	}
}

func TestEntryIntegrityResult_String_Tampered(t *testing.T) {
	r := EntryIntegrityResult{
		Index: 2, Timestamp: 3000, Status: IntegrityTampered,
		StoredHash:   strings.Repeat("c", 64),
		ComputedHash: strings.Repeat("d", 64),
	}
	s := r.String()
	if !strings.Contains(s, "TAMPERED") {
		t.Errorf("expected TAMPERED in string, got: %s", s)
	}
}

// ── RegistryIntegrityReport helpers ──────────────────────────────────────────

func TestRegistryIntegrityReport_Passed_OnlyLegacy(t *testing.T) {
	rpt := &RegistryIntegrityReport{LegacyCount: 3}
	if !rpt.Passed() {
		t.Error("expected Passed() true when only legacy entries")
	}
}

func TestRegistryIntegrityReport_Errors_OnlyTampered(t *testing.T) {
	rpt := &RegistryIntegrityReport{
		TamperedCount: 1,
		Results: []EntryIntegrityResult{
			{Index: 0, Timestamp: 1, Status: IntegrityTampered,
				StoredHash:   strings.Repeat("0", 64),
				ComputedHash: strings.Repeat("1", 64)},
		},
	}
	errs := rpt.Errors()
	if len(errs) != 1 {
		t.Errorf("expected 1 error, got %d", len(errs))
	}
	if !strings.Contains(errs[0].Error(), "content hash mismatch") {
		t.Errorf("expected 'content hash mismatch' in error, got: %v", errs[0])
	}
}
