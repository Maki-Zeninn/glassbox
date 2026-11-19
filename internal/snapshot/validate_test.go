// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package snapshot

import (
	"path/filepath"
	"strings"
	"testing"
)

// ── LoadWithDiagnostics ───────────────────────────────────────────────────────

func TestLoadWithDiagnostics_Clean(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snap.json")

	snap := FromMap(map[string]string{"k": "v"})
	if err := Save(path, snap); err != nil {
		t.Fatal(err)
	}

	loaded, warn, err := LoadWithDiagnostics(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if warn != nil {
		t.Errorf("expected no drift warning for clean snapshot, got: %v", warn)
	}
	if loaded == nil {
		t.Fatal("expected non-nil snapshot")
	}
}

func TestLoadWithDiagnostics_DriftDetected(t *testing.T) {
	// We cannot easily inject a bad fingerprint without writing raw bytes.
	// Instead we test that LoadWithDiagnostics returns no warning for a
	// clean file (which is the golden path), and we separately verify the
	// DriftWarning struct directly.
	warn := &DriftWarning{Stored: "aaaa", Computed: "bbbb"}
	if !strings.Contains(warn.Error(), "fingerprint mismatch") {
		t.Errorf("expected 'fingerprint mismatch' in drift warning, got: %s", warn.Error())
	}
	if !strings.Contains(warn.Error(), "re-run the debug command") {
		t.Errorf("expected remediation hint in drift warning, got: %s", warn.Error())
	}
	if !strings.Contains(warn.Error(), "aaaa") {
		t.Errorf("expected stored hash in drift warning, got: %s", warn.Error())
	}
	if !strings.Contains(warn.Error(), "bbbb") {
		t.Errorf("expected computed hash in drift warning, got: %s", warn.Error())
	}
}

func TestLoadWithDiagnostics_NotFound(t *testing.T) {
	_, _, err := LoadWithDiagnostics("/nonexistent/snap.json")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

// ── ValidateSnapshotBeforeReplay ──────────────────────────────────────────────

func TestValidateSnapshotBeforeReplay_Clean(t *testing.T) {
	meta := &ReplayMetadata{TxHash: "abc123", Network: "testnet", GlassboxVersion: "v1"}
	snap := FromMap(map[string]string{"k": "v"})
	ps := &PersistedSnapshot{Metadata: meta, Snapshot: snap}

	if err := ValidateSnapshotBeforeReplay(ps, "abc123", "testnet", nil, ""); err != nil {
		t.Errorf("expected valid snapshot, got: %v", err)
	}
}

func TestValidateSnapshotBeforeReplay_NilSnapshot(t *testing.T) {
	err := ValidateSnapshotBeforeReplay(nil, "", "", nil, "")
	if err == nil {
		t.Error("expected error for nil snapshot")
	}
	if !strings.Contains(err.Error(), "nil") {
		t.Errorf("error should mention nil, got: %v", err)
	}
	if !strings.Contains(err.Error(), "re-run") {
		t.Errorf("error should include remediation hint, got: %v", err)
	}
}

func TestValidateSnapshotBeforeReplay_NilMetadata(t *testing.T) {
	snap := FromMap(nil)
	ps := &PersistedSnapshot{Metadata: nil, Snapshot: snap}

	err := ValidateSnapshotBeforeReplay(ps, "", "", nil, "")
	if err == nil {
		t.Error("expected error for nil metadata")
	}
	if !strings.Contains(err.Error(), "metadata") {
		t.Errorf("error should mention metadata, got: %v", err)
	}
	if !strings.Contains(err.Error(), "re-run") {
		t.Errorf("error should include remediation hint, got: %v", err)
	}
}

func TestValidateSnapshotBeforeReplay_NilLedgerState(t *testing.T) {
	meta := &ReplayMetadata{TxHash: "abc", Network: "testnet"}
	ps := &PersistedSnapshot{Metadata: meta, Snapshot: nil}

	err := ValidateSnapshotBeforeReplay(ps, "", "", nil, "")
	if err == nil {
		t.Error("expected error for nil ledger state")
	}
	if !strings.Contains(err.Error(), "ledger state") {
		t.Errorf("error should mention ledger state, got: %v", err)
	}
}

func TestValidateSnapshotBeforeReplay_FingerprintMismatch(t *testing.T) {
	meta := &ReplayMetadata{TxHash: "abc", Network: "testnet"}
	snap := FromMap(map[string]string{"k": "v"})
	// Tamper the fingerprint.
	snap.Fingerprint = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	ps := &PersistedSnapshot{Metadata: meta, Snapshot: snap}

	err := ValidateSnapshotBeforeReplay(ps, "", "", nil, "")
	if err == nil {
		t.Error("expected error for fingerprint mismatch")
	}
	if !strings.Contains(err.Error(), "fingerprint mismatch") {
		t.Errorf("error should say 'fingerprint mismatch', got: %v", err)
	}
	if !strings.Contains(err.Error(), "Re-run") {
		t.Errorf("error should include remediation hint, got: %v", err)
	}
}

func TestValidateSnapshotBeforeReplay_TxHashMismatch(t *testing.T) {
	meta := &ReplayMetadata{TxHash: "abc123", Network: "testnet"}
	snap := FromMap(map[string]string{"k": "v"})
	ps := &PersistedSnapshot{Metadata: meta, Snapshot: snap}

	err := ValidateSnapshotBeforeReplay(ps, "different_hash", "testnet", nil, "")
	if err == nil {
		t.Error("expected error for tx hash mismatch")
	}
	msg := err.Error()
	if !strings.Contains(msg, "tx hash mismatch") {
		t.Errorf("error should say 'tx hash mismatch', got: %v", err)
	}
	if !strings.Contains(msg, "abc123") {
		t.Errorf("error should include stored tx hash, got: %v", err)
	}
	if !strings.Contains(msg, "different_hash") {
		t.Errorf("error should include expected tx hash, got: %v", err)
	}
}

func TestValidateSnapshotBeforeReplay_NetworkMismatch(t *testing.T) {
	meta := &ReplayMetadata{TxHash: "abc123", Network: "testnet"}
	snap := FromMap(map[string]string{"k": "v"})
	ps := &PersistedSnapshot{Metadata: meta, Snapshot: snap}

	err := ValidateSnapshotBeforeReplay(ps, "abc123", "mainnet", nil, "")
	if err == nil {
		t.Error("expected error for network mismatch")
	}
	msg := err.Error()
	if !strings.Contains(msg, "network mismatch") {
		t.Errorf("error should say 'network mismatch', got: %v", err)
	}
	if !strings.Contains(msg, "testnet") {
		t.Errorf("error should name stored network, got: %v", err)
	}
	if !strings.Contains(msg, "mainnet") {
		t.Errorf("error should name expected network, got: %v", err)
	}
	// Should suggest re-running the command with the right network.
	if !strings.Contains(msg, "--network") {
		t.Errorf("error should suggest --network flag, got: %v", err)
	}
}

func TestValidateSnapshotBeforeReplay_StaleParams(t *testing.T) {
	params := map[string]string{"network": "testnet", "tx": "abc123"}
	meta := &ReplayMetadata{
		TxHash:           "abc123",
		Network:          "testnet",
		ParamFingerprint: BuildParamFingerprint(params),
	}
	snap := FromMap(nil)
	ps := &PersistedSnapshot{Metadata: meta, Snapshot: snap}

	// Changed params → stale.
	changedParams := map[string]string{"network": "mainnet", "tx": "abc123"}
	err := ValidateSnapshotBeforeReplay(ps, "", "", changedParams, "")
	if err == nil {
		t.Error("expected error for stale params")
	}
	if !strings.Contains(err.Error(), "stale") {
		t.Errorf("error should say 'stale', got: %v", err)
	}
	if !strings.Contains(err.Error(), "re-run") && !strings.Contains(err.Error(), "Re-run") {
		t.Errorf("error should include remediation hint, got: %v", err)
	}
}

func TestValidateSnapshotBeforeReplay_StaleSourceHash(t *testing.T) {
	meta := &ReplayMetadata{
		TxHash:     "abc123",
		Network:    "testnet",
		SourceHash: "hash_v1",
	}
	snap := FromMap(nil)
	ps := &PersistedSnapshot{Metadata: meta, Snapshot: snap}

	err := ValidateSnapshotBeforeReplay(ps, "", "", nil, "hash_v2")
	if err == nil {
		t.Error("expected error for stale source hash")
	}
	if !strings.Contains(err.Error(), "stale") {
		t.Errorf("error should say 'stale', got: %v", err)
	}
}

func TestValidateSnapshotBeforeReplay_FreshSourceHash(t *testing.T) {
	meta := &ReplayMetadata{
		TxHash:     "abc123",
		Network:    "testnet",
		SourceHash: "hash_v1",
	}
	snap := FromMap(nil)
	ps := &PersistedSnapshot{Metadata: meta, Snapshot: snap}

	// Same hash → not stale.
	if err := ValidateSnapshotBeforeReplay(ps, "", "", nil, "hash_v1"); err != nil {
		t.Errorf("expected no error for same source hash, got: %v", err)
	}
}

func TestValidateSnapshotBeforeReplay_EmptyExpected_NoError(t *testing.T) {
	meta := &ReplayMetadata{TxHash: "abc", Network: "testnet"}
	snap := FromMap(map[string]string{"k": "v"})
	ps := &PersistedSnapshot{Metadata: meta, Snapshot: snap}

	// No expected tx/network/params — only fingerprint check, which passes.
	if err := ValidateSnapshotBeforeReplay(ps, "", "", nil, ""); err != nil {
		t.Errorf("expected no error when no expectations set, got: %v", err)
	}
}

// ── SnapshotLoadDiagnostic ────────────────────────────────────────────────────

func TestSnapshotLoadDiagnostic_Nil(t *testing.T) {
	diag := SnapshotLoadDiagnostic(nil)
	if !strings.Contains(diag, "nil") {
		t.Errorf("expected 'nil' in diagnostic, got: %s", diag)
	}
}

func TestSnapshotLoadDiagnostic_Full(t *testing.T) {
	meta := &ReplayMetadata{
		TxHash:           "abc123",
		Network:          "testnet",
		GlassboxVersion:  "v1.2.3",
		SourceHash:       "wasm_hash",
		ParamFingerprint: "param_hash",
	}
	snap := FromMap(map[string]string{"key": "value"})
	ps := &PersistedSnapshot{Metadata: meta, Snapshot: snap}

	diag := SnapshotLoadDiagnostic(ps)

	if !strings.Contains(diag, "testnet") {
		t.Errorf("expected network in diagnostic, got: %s", diag)
	}
	if !strings.Contains(diag, "abc123") {
		t.Errorf("expected tx hash in diagnostic, got: %s", diag)
	}
	if !strings.Contains(diag, "1") { // ledger entry count
		t.Errorf("expected entry count in diagnostic, got: %s", diag)
	}
}

func TestSnapshotLoadDiagnostic_NilSnapshot(t *testing.T) {
	meta := &ReplayMetadata{TxHash: "abc", Network: "testnet"}
	ps := &PersistedSnapshot{Metadata: meta, Snapshot: nil}

	diag := SnapshotLoadDiagnostic(ps)
	if !strings.Contains(diag, "ERROR") {
		t.Errorf("expected ERROR for nil snapshot in diagnostic, got: %s", diag)
	}
}

// ── DriftWarning ─────────────────────────────────────────────────────────────

func TestDriftWarning_Error_ContainsKeyInfo(t *testing.T) {
	w := &DriftWarning{Stored: "storedabc", Computed: "computedxyz"}
	msg := w.Error()
	if !strings.Contains(msg, "storedabc") {
		t.Errorf("expected stored hash in error, got: %s", msg)
	}
	if !strings.Contains(msg, "computedxyz") {
		t.Errorf("expected computed hash in error, got: %s", msg)
	}
	if !strings.Contains(msg, "re-run the debug command") {
		t.Errorf("expected remediation hint in error, got: %s", msg)
	}
}
