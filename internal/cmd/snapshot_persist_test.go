// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotandev/glassbox/internal/snapshot"
)

// writeLedgerStateFile writes a snapshot.Snapshot as a plain JSON file
// (the format produced by the debug command's --save-snapshots flag).
func writeLedgerStateFile(t *testing.T, dir string, entries map[string]string) string {
	t.Helper()
	snap := snapshot.FromMap(entries)
	path := filepath.Join(dir, "state.json")
	if err := snapshot.Save(path, snap); err != nil {
		t.Fatalf("failed to write ledger state file: %v", err)
	}
	return path
}

// --- runSnapshotSave ---

func TestRunSnapshotSave_MissingTxFlag(t *testing.T) {
	snapSaveTxHashFlag = ""
	snapSaveInputFlag = "some.json"
	defer func() {
		snapSaveTxHashFlag = ""
		snapSaveInputFlag = ""
	}()

	err := snapshotSaveCmd.RunE(snapshotSaveCmd, nil)
	if err == nil {
		t.Error("expected error for missing --tx flag")
	}
}

func TestRunSnapshotSave_MissingInputFlag(t *testing.T) {
	snapSaveTxHashFlag = "abc123"
	snapSaveInputFlag = ""
	defer func() {
		snapSaveTxHashFlag = ""
		snapSaveInputFlag = ""
	}()

	err := snapshotSaveCmd.RunE(snapshotSaveCmd, nil)
	if err == nil {
		t.Error("expected error for missing --input flag")
	}
}

func TestRunSnapshotSave_InputNotFound(t *testing.T) {
	snapSaveTxHashFlag = "abc123"
	snapSaveInputFlag = "/nonexistent/state.json"
	defer func() {
		snapSaveTxHashFlag = ""
		snapSaveInputFlag = ""
	}()

	err := snapshotSaveCmd.RunE(snapshotSaveCmd, nil)
	if err == nil {
		t.Error("expected error for missing input file")
	}
}

func TestRunSnapshotSave_Success(t *testing.T) {
	dir := t.TempDir()
	inputPath := writeLedgerStateFile(t, dir, map[string]string{
		"ledger-key-1": "ledger-val-1",
		"ledger-key-2": "ledger-val-2",
	})
	outputPath := filepath.Join(dir, "output.snap.json")

	snapSaveTxHashFlag = "abc123def456"
	snapSaveNetworkFlag = "testnet"
	snapSaveInputFlag = inputPath
	snapSaveOutputFlag = outputPath
	snapSaveEnvXdrFlag = "envelopeXDR"
	snapSaveMetaXdrFlag = "resultMetaXDR"
	defer func() {
		snapSaveTxHashFlag = ""
		snapSaveNetworkFlag = "testnet"
		snapSaveInputFlag = ""
		snapSaveOutputFlag = ""
		snapSaveEnvXdrFlag = ""
		snapSaveMetaXdrFlag = ""
	}()

	err := snapshotSaveCmd.RunE(snapshotSaveCmd, nil)
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}

	// Verify the output file exists and is valid.
	ps, err := snapshot.LoadPersisted(outputPath)
	if err != nil {
		t.Fatalf("LoadPersisted failed: %v", err)
	}
	if ps.Metadata.TxHash != "abc123def456" {
		t.Errorf("expected TxHash abc123def456, got %s", ps.Metadata.TxHash)
	}
	if ps.Metadata.Network != "testnet" {
		t.Errorf("expected Network testnet, got %s", ps.Metadata.Network)
	}
	if len(ps.Snapshot.LedgerEntries) != 2 {
		t.Errorf("expected 2 ledger entries, got %d", len(ps.Snapshot.LedgerEntries))
	}
}

func TestRunSnapshotSave_WithWasm(t *testing.T) {
	dir := t.TempDir()
	inputPath := writeLedgerStateFile(t, dir, map[string]string{"k": "v"})
	outputPath := filepath.Join(dir, "wasm.snap.json")

	// Create a dummy WASM file.
	wasmPath := filepath.Join(dir, "contract.wasm")
	if err := os.WriteFile(wasmPath, []byte("wasm binary content"), 0644); err != nil {
		t.Fatal(err)
	}

	snapSaveTxHashFlag = "txhash"
	snapSaveNetworkFlag = "testnet"
	snapSaveInputFlag = inputPath
	snapSaveOutputFlag = outputPath
	snapSaveWasmFlag = wasmPath
	defer func() {
		snapSaveTxHashFlag = ""
		snapSaveNetworkFlag = "testnet"
		snapSaveInputFlag = ""
		snapSaveOutputFlag = ""
		snapSaveWasmFlag = ""
	}()

	err := snapshotSaveCmd.RunE(snapshotSaveCmd, nil)
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}

	ps, err := snapshot.LoadPersisted(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if ps.Metadata.SourceHash == "" {
		t.Error("expected non-empty source hash when WASM is provided")
	}
}

func TestRunSnapshotSave_InvalidNetwork_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	inputPath := writeLedgerStateFile(t, dir, map[string]string{"k": "v"})

	snapSaveTxHashFlag = "abc123"
	snapSaveNetworkFlag = "devnet"
	snapSaveInputFlag = inputPath
	snapSaveOutputFlag = filepath.Join(dir, "out.snap.json")
	defer func() {
		snapSaveTxHashFlag = ""
		snapSaveNetworkFlag = "testnet"
		snapSaveInputFlag = ""
		snapSaveOutputFlag = ""
	}()

	err := snapshotSaveCmd.RunE(snapshotSaveCmd, nil)
	if err == nil {
		t.Fatal("expected error for invalid network")
	}
	if !strings.Contains(err.Error(), "devnet") && !strings.Contains(err.Error(), "network") {
		t.Errorf("error should mention invalid network, got: %v", err)
	}
}

func TestRunSnapshotSave_DefaultOutputPath(t *testing.T) {
	dir := t.TempDir()
	inputPath := writeLedgerStateFile(t, dir, map[string]string{"k": "v"})

	snapSaveTxHashFlag = "abc123"
	snapSaveNetworkFlag = "testnet"
	snapSaveInputFlag = inputPath
	snapSaveOutputFlag = "" // use default
	defer func() {
		snapSaveTxHashFlag = ""
		snapSaveNetworkFlag = "testnet"
		snapSaveInputFlag = ""
		snapSaveOutputFlag = ""
	}()

	// The default path goes under ~/.glassbox/cache which may not be writable
	// in CI. We just verify the command doesn't panic; a write error is acceptable.
	_ = snapshotSaveCmd.RunE(snapshotSaveCmd, nil)
}

// --- runSnapshotLoad ---

func TestRunSnapshotLoad_MissingPathFlag(t *testing.T) {
	snapLoadPathFlag = ""
	defer func() { snapLoadPathFlag = "" }()

	err := snapshotLoadCmd.RunE(snapshotLoadCmd, nil)
	if err == nil {
		t.Error("expected error for missing --path flag")
	}
}

func TestRunSnapshotLoad_NotFound(t *testing.T) {
	snapLoadPathFlag = "/nonexistent/snap.json"
	defer func() { snapLoadPathFlag = "" }()

	err := snapshotLoadCmd.RunE(snapshotLoadCmd, nil)
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestRunSnapshotLoad_ValidSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snap.json")

	meta := &snapshot.ReplayMetadata{
		GlassboxVersion: "v1.0.0",
		TxHash:          "abc123",
		Network:         "testnet",
	}
	snap := snapshot.FromMap(map[string]string{"k": "v"})
	if err := snapshot.SavePersisted(path, meta, snap); err != nil {
		t.Fatal(err)
	}

	snapLoadPathFlag = path
	snapLoadVerifyFlag = false
	defer func() {
		snapLoadPathFlag = ""
		snapLoadVerifyFlag = false
	}()

	err := snapshotLoadCmd.RunE(snapshotLoadCmd, nil)
	if err != nil {
		t.Errorf("expected success, got: %v", err)
	}
}

func TestRunSnapshotLoad_WithVerify_Passes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snap.json")

	meta := &snapshot.ReplayMetadata{
		GlassboxVersion: "v1.0.0",
		TxHash:          "abc123",
		Network:         "testnet",
	}
	snap := snapshot.FromMap(map[string]string{"k": "v"})
	if err := snapshot.SavePersisted(path, meta, snap); err != nil {
		t.Fatal(err)
	}

	snapLoadPathFlag = path
	snapLoadTxHashFlag = "abc123"
	snapLoadNetworkFlag = "testnet"
	snapLoadVerifyFlag = true
	defer func() {
		snapLoadPathFlag = ""
		snapLoadTxHashFlag = ""
		snapLoadNetworkFlag = ""
		snapLoadVerifyFlag = false
	}()

	err := snapshotLoadCmd.RunE(snapshotLoadCmd, nil)
	if err != nil {
		t.Errorf("expected success with verify, got: %v", err)
	}
}

func TestRunSnapshotLoad_WithVerify_TxMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snap.json")

	meta := &snapshot.ReplayMetadata{
		GlassboxVersion: "v1.0.0",
		TxHash:          "abc123",
		Network:         "testnet",
	}
	snap := snapshot.FromMap(map[string]string{"k": "v"})
	if err := snapshot.SavePersisted(path, meta, snap); err != nil {
		t.Fatal(err)
	}

	snapLoadPathFlag = path
	snapLoadTxHashFlag = "different-hash"
	snapLoadVerifyFlag = true
	defer func() {
		snapLoadPathFlag = ""
		snapLoadTxHashFlag = ""
		snapLoadVerifyFlag = false
	}()

	err := snapshotLoadCmd.RunE(snapshotLoadCmd, nil)
	if err == nil {
		t.Error("expected error for tx hash mismatch")
	}
}

func TestRunSnapshotLoad_InvalidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("{bad json"), 0644); err != nil {
		t.Fatal(err)
	}

	snapLoadPathFlag = path
	defer func() { snapLoadPathFlag = "" }()

	err := snapshotLoadCmd.RunE(snapshotLoadCmd, nil)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestRunSnapshotLoad_WrongSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snap.json")

	type badPS struct {
		Metadata map[string]interface{} `json:"metadata"`
		Snapshot *snapshot.Snapshot     `json:"snapshot"`
	}
	ps := badPS{
		Metadata: map[string]interface{}{
			"schema_version":   999,
			"glassbox_version": "v1.0.0",
			"tx_hash":          "abc",
			"network":          "testnet",
		},
		Snapshot: snapshot.FromMap(nil),
	}
	data, _ := json.MarshalIndent(ps, "", "  ")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}

	snapLoadPathFlag = path
	defer func() { snapLoadPathFlag = "" }()

	err := snapshotLoadCmd.RunE(snapshotLoadCmd, nil)
	if err == nil {
		t.Error("expected error for wrong schema version")
	}
}
