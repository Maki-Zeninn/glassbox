// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package session

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sampleData() *Data {
	return &Data{
		ID:              "test-session-01",
		TxHash:          "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
		Network:         "testnet",
		Status:          "saved",
		CreatedAt:       time.Now().UTC().Truncate(time.Second),
		LastAccessAt:    time.Now().UTC().Truncate(time.Second),
		EnvelopeXdr:     "AAAA==",
		SimResponseJSON: `{"status":"SUCCESS"}`,
		SchemaVersion:   SchemaVersion,
	}
}

func TestExportAndImportArchive(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "session.gbx")

	original := sampleData()
	if err := ExportArchive(original, archivePath); err != nil {
		t.Fatalf("ExportArchive failed: %v", err)
	}

	// Verify the file exists and is non-empty.
	info, err := os.Stat(archivePath)
	if err != nil {
		t.Fatalf("archive file not found: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("archive file is empty")
	}

	restored, err := ImportArchive(archivePath)
	if err != nil {
		t.Fatalf("ImportArchive failed: %v", err)
	}

	if restored.ID != original.ID {
		t.Errorf("ID mismatch: got %q, want %q", restored.ID, original.ID)
	}
	if restored.TxHash != original.TxHash {
		t.Errorf("TxHash mismatch: got %q, want %q", restored.TxHash, original.TxHash)
	}
	if restored.Network != original.Network {
		t.Errorf("Network mismatch: got %q, want %q", restored.Network, original.Network)
	}
	if restored.EnvelopeXdr != original.EnvelopeXdr {
		t.Errorf("EnvelopeXdr mismatch: got %q, want %q", restored.EnvelopeXdr, original.EnvelopeXdr)
	}
}

func TestExportArchive_NilData(t *testing.T) {
	err := ExportArchive(nil, "/tmp/x.gbx")
	if err == nil {
		t.Fatal("expected error for nil data")
	}
}

func TestExportArchive_EmptyPath(t *testing.T) {
	err := ExportArchive(sampleData(), "")
	if err == nil {
		t.Fatal("expected error for empty dest path")
	}
}

func TestImportArchive_InvalidFile(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.gbx")
	if err := os.WriteFile(bad, []byte("not a zip"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := ImportArchive(bad)
	if err == nil {
		t.Fatal("expected error for non-zip archive")
	}
}

// ── ValidateArchivePath ───────────────────────────────────────────────────────

func TestValidateArchivePath_EmptyPath_ReturnsError(t *testing.T) {
	if err := ValidateArchivePath(""); err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestValidateArchivePath_WhitespacePath_ReturnsError(t *testing.T) {
	if err := ValidateArchivePath("   "); err == nil {
		t.Fatal("expected error for whitespace-only path")
	}
}

func TestValidateArchivePath_UnsupportedExtension_ReturnsError(t *testing.T) {
	err := ValidateArchivePath("/tmp/session.tar.gz")
	if err == nil {
		t.Fatal("expected error for unsupported extension")
	}
	if !strings.Contains(err.Error(), ".gbx") {
		t.Errorf("error should mention '.gbx', got: %v", err)
	}
}

func TestValidateArchivePath_NoExtension_ReturnsError(t *testing.T) {
	err := ValidateArchivePath("/tmp/session")
	if err == nil {
		t.Fatal("expected error for missing extension")
	}
}

func TestValidateArchivePath_GbxExtension_OK(t *testing.T) {
	if err := ValidateArchivePath("/tmp/session.gbx"); err != nil {
		t.Errorf("expected no error for .gbx extension, got: %v", err)
	}
}

func TestValidateArchivePath_ZipExtension_OK(t *testing.T) {
	if err := ValidateArchivePath("/tmp/session.zip"); err != nil {
		t.Errorf("expected no error for .zip extension, got: %v", err)
	}
}

func TestValidateArchivePath_UppercaseExtension_OK(t *testing.T) {
	if err := ValidateArchivePath("/tmp/session.GBX"); err != nil {
		t.Errorf("expected no error for uppercase extension .GBX, got: %v", err)
	}
}

// ── ImportArchive: unsupported extension ────────────────────────────────────

func TestImportArchive_UnsupportedExtension_ReturnsActionableError(t *testing.T) {
	_, err := ImportArchive("/tmp/session.tar.gz")
	if err == nil {
		t.Fatal("expected error for unsupported extension")
	}
	if !strings.Contains(err.Error(), ".gbx") {
		t.Errorf("error should mention the supported extension, got: %v", err)
	}
	if !strings.Contains(err.Error(), "Fix:") {
		t.Errorf("error should include a Fix hint, got: %v", err)
	}
}

// ── ImportArchive: empty path ────────────────────────────────────────────────

func TestImportArchive_EmptyPath_ReturnsError(t *testing.T) {
	_, err := ImportArchive("")
	if err == nil {
		t.Fatal("expected error for empty archive path")
	}
}

// ── ExportArchive: unsupported extension rejected ───────────────────────────

func TestExportArchive_UnsupportedExtension_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	err := ExportArchive(sampleData(), filepath.Join(dir, "session.tar.gz"))
	if err == nil {
		t.Fatal("expected error for unsupported extension")
	}
	if !strings.Contains(err.Error(), "Fix:") {
		t.Errorf("error should include a Fix hint, got: %v", err)
	}
}

// ── ImportArchive: missing meta.json ────────────────────────────────────────

func TestImportArchive_MissingMetaJSON_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "no_meta.gbx")

	// Build a zip without meta.json.
	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("session.json")
	_, _ = w.Write([]byte(`{}`))
	_ = zw.Close()
	_ = f.Close()

	_, err = ImportArchive(archivePath)
	if err == nil {
		t.Fatal("expected error when meta.json is absent")
	}
	if !strings.Contains(err.Error(), "meta.json") {
		t.Errorf("error should mention 'meta.json', got: %v", err)
	}
}

// ── ImportArchive: missing session.json ─────────────────────────────────────

func TestImportArchive_MissingSessionJSON_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "no_session.gbx")

	meta := archiveMeta{
		ArchiveVersion:  archiveVersion,
		GlassboxVersion: "0.0.0",
		CreatedAt:       "2026-01-01T00:00:00Z",
		SchemaVersion:   SchemaVersion,
	}
	metaBytes, _ := json.Marshal(meta)

	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("meta.json")
	_, _ = w.Write(metaBytes)
	_ = zw.Close()
	_ = f.Close()

	_, err = ImportArchive(archivePath)
	if err == nil {
		t.Fatal("expected error when session.json is absent")
	}
	if !strings.Contains(err.Error(), "session.json") {
		t.Errorf("error should mention 'session.json', got: %v", err)
	}
}

// ── ExportArchive: invalid session rejected ─────────────────────────────────

func TestExportArchive_InvalidSession_Rejected(t *testing.T) {
	dir := t.TempDir()
	d := sampleData()
	d.TxHash = "" // make it invalid
	err := ExportArchive(d, filepath.Join(dir, "session.gbx"))
	if err == nil {
		t.Fatal("expected error when exporting invalid session")
	}
	if !strings.Contains(err.Error(), "TxHash") && !strings.Contains(err.Error(), "validation") {
		t.Errorf("error should mention validation failure, got: %v", err)
	}
}

// ── ImportArchive: invalid session in archive ────────────────────────────────

func TestImportArchive_InvalidDataInArchive_ReturnsDiagnostic(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "bad_data.gbx")

	// Build a zip with valid meta.json but invalid session.json (missing fields).
	meta := archiveMeta{
		ArchiveVersion:  archiveVersion,
		GlassboxVersion: "0.0.0",
		CreatedAt:       "2026-01-01T00:00:00Z",
		SchemaVersion:   SchemaVersion,
	}
	metaBytes, _ := json.Marshal(meta)

	// Session with all required fields empty.
	badData := map[string]interface{}{
		"id":             "",
		"tx_hash":        "",
		"network":        "",
		"status":         "",
		"created_at":     "2026-01-01T00:00:00Z",
		"last_access_at": "2026-01-01T00:00:00Z",
	}
	sessionBytes, _ := json.Marshal(badData)

	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("meta.json")
	_, _ = w.Write(metaBytes)
	w, _ = zw.Create("session.json")
	_, _ = w.Write(sessionBytes)
	_ = zw.Close()
	_ = f.Close()

	_, err = ImportArchive(archivePath)
	if err == nil {
		t.Fatal("expected error for invalid session data in archive")
	}
	if !strings.Contains(err.Error(), "invalid session") {
		t.Errorf("error should mention 'invalid session', got: %v", err)
	}
}

// ── ImportArchive: archive version too new ──────────────────────────────────

func TestImportArchive_ArchiveVersionTooNew_ReturnsUpgradeHint(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "future.gbx")

	meta := archiveMeta{
		ArchiveVersion:  archiveVersion + 99,
		GlassboxVersion: "99.0.0",
		CreatedAt:       "2026-01-01T00:00:00Z",
		SchemaVersion:   SchemaVersion,
	}
	metaBytes, _ := json.Marshal(meta)
	sessionBytes, _ := json.Marshal(sampleData())

	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("meta.json")
	_, _ = w.Write(metaBytes)
	w, _ = zw.Create("session.json")
	_, _ = w.Write(sessionBytes)
	_ = zw.Close()
	_ = f.Close()

	_, err = ImportArchive(archivePath)
	if err == nil {
		t.Fatal("expected error for archive version too new")
	}
	if !strings.Contains(err.Error(), "upgrade") && !strings.Contains(err.Error(), "newer") {
		t.Errorf("error should mention 'upgrade' or 'newer', got: %v", err)
	}
}

func TestImportArchive_SchemaVersionTooNew_ReturnsUpgradeHint(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "future_schema.gbx")

	meta := archiveMeta{
		ArchiveVersion:  archiveVersion,
		GlassboxVersion: "99.0.0",
		CreatedAt:       "2026-01-01T00:00:00Z",
		SchemaVersion:   SchemaVersion + 99,
	}
	metaBytes, _ := json.Marshal(meta)
	sessionBytes, _ := json.Marshal(sampleData())

	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("meta.json")
	_, _ = w.Write(metaBytes)
	w, _ = zw.Create("session.json")
	_, _ = w.Write(sessionBytes)
	_ = zw.Close()
	_ = f.Close()

	_, err = ImportArchive(archivePath)
	if err == nil {
		t.Fatal("expected error for schema version too new")
	}
	if !strings.Contains(err.Error(), "schema version") {
		t.Errorf("error should mention schema version, got: %v", err)
	}
}
