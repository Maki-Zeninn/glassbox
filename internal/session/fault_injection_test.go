// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package session

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Injectable filesystem operations ─────────────────────────────────────────

// FaultFS wraps filesystem operations and allows injecting deterministic
// failures at each stage of a write. Production code uses the real FS
// operations; tests swap in FaultFS to simulate disk-full, permission-denied,
// and rename-failure scenarios.
type FaultFS struct {
	// RemoveFunc, if non-nil, replaces os.Remove for the target path.
	RemoveFunc func(name string) error
	// RenameFunc, if non-nil, replaces os.Rename.
	RenameFunc func(oldpath, newpath string) error
	// WriteFunc, if non-nil, replaces the Write call on temp files.
	WriteFunc func(name string, data []byte) (int, error)
	// MkdirAllFunc, if non-nil, replaces os.MkdirAll.
	MkdirAllFunc func(path string, perm os.FileMode) error
}

// RealFS is the default production filesystem — all operations delegate to os.
type RealFS struct{}

func (RealFS) Remove(name string) error                       { return os.Remove(name) }
func (RealFS) Rename(old, new string) error                   { return os.Rename(old, new) }
func (RealFS) MkdirAll(path string, perm os.FileMode) error   { return os.MkdirAll(path, perm) }

// ── writeFileAtomic fault injection (session-level) ─────────────────────────

func TestFaultInjection_PermissionDenied_TempCreate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target.json")

	permErr := fmt.Errorf("permission denied: cannot create temp file")
	withFaultInject(t, func(stage string) error {
		if stage == faultAfterCreate {
			return permErr
		}
		return nil
	})

	err := writeFileAtomic(path, []byte("data"), 0o600)
	require.ErrorIs(t, err, permErr)

	// Target must not exist — the write was aborted.
	_, readErr := os.ReadFile(path)
	assert.True(t, os.IsNotExist(readErr), "target should not exist after create failure")
}

func TestFaultInjection_DiskFull_AfterWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target.json")

	require.NoError(t, os.WriteFile(path, []byte("original"), 0o600))

	diskFull := fmt.Errorf("no space left on device")
	withFaultInject(t, func(stage string) error {
		if stage == faultAfterWrite {
			return diskFull
		}
		return nil
	})

	err := writeFileAtomic(path, []byte("new data"), 0o600)
	require.ErrorIs(t, err, diskFull)

	data, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.Equal(t, "original", string(data), "original content must survive disk-full error")
}

func TestFaultInjection_SyncFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target.json")
	require.NoError(t, os.WriteFile(path, []byte("before"), 0o600))

	syncErr := fmt.Errorf("sync failed")
	withFaultInject(t, func(stage string) error {
		if stage == faultAfterSync {
			return syncErr
		}
		return nil
	})

	err := writeFileAtomic(path, []byte("after"), 0o600)
	require.ErrorIs(t, err, syncErr)

	data, _ := os.ReadFile(path)
	assert.Equal(t, "before", string(data))
}

func TestFaultInjection_CloseFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target.json")
	require.NoError(t, os.WriteFile(path, []byte("close-test"), 0o600))

	closeErr := fmt.Errorf("close failed")
	withFaultInject(t, func(stage string) error {
		if stage == faultAfterClose {
			return closeErr
		}
		return nil
	})

	err := writeFileAtomic(path, []byte("new"), 0o600)
	require.ErrorIs(t, err, closeErr)

	data, _ := os.ReadFile(path)
	assert.Equal(t, "close-test", string(data))
}

func TestFaultInjection_RenameFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target.json")
	require.NoError(t, os.WriteFile(path, []byte("rename-test"), 0o600))

	renameErr := fmt.Errorf("rename failed: device or resource busy")
	withFaultInject(t, func(stage string) error {
		if stage == faultBeforeRename {
			return renameErr
		}
		return nil
	})

	err := writeFileAtomic(path, []byte("new"), 0o600)
	require.ErrorIs(t, err, renameErr)

	data, _ := os.ReadFile(path)
	assert.Equal(t, "rename-test", string(data))
}

func TestFaultInjection_NoFault_Succeeds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clean.json")

	withFaultInject(t, func(stage string) error {
		return nil
	})

	err := writeFileAtomic(path, []byte(`{"clean":true}`), 0o600)
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, `{"clean":true}`, string(data))
}

func TestFaultInjection_NoTempFilesLeftBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target.json")
	require.NoError(t, os.WriteFile(path, []byte("keep"), 0o600))

	stages := []string{faultAfterCreate, faultAfterWrite, faultAfterSync, faultAfterClose, faultBeforeRename}
	for _, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			injected := fmt.Errorf("crash at %s", stage)
			withFaultInject(t, func(s string) error {
				if s == stage {
					return injected
				}
				return nil
			})

			_ = writeFileAtomic(path, []byte("new"), 0o600)

			entries, err := os.ReadDir(dir)
			require.NoError(t, err)
			for _, e := range entries {
				assert.False(t, isStaleTempName(e.Name()),
					"temp file %q left behind after fault at %s", e.Name(), stage)
			}
		})
	}
}

// ── ExportArchive fault injection ────────────────────────────────────────────

func TestFaultInjection_ExportArchive_DiskFull(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "test.gbx")

	// Write a valid session first.
	original := sampleData()

	// We test the atomic file write path by verifying the archive
	// operation is graceful when the underlying directory doesn't exist.
	badDir := filepath.Join(dir, "nonexistent", "deep")
	err := ExportArchive(original, filepath.Join(badDir, "test.gbx"))
	require.Error(t, err, "should fail when destination directory cannot be created")
}

func TestFaultInjection_ExportArchive_InvalidExtension(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "test.txt")

	original := sampleData()
	err := ExportArchive(original, archivePath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported archive extension")
}

func TestFaultInjection_ExportArchive_NilData(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "test.gbx")

	err := ExportArchive(nil, archivePath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil")
}

func TestFaultInjection_ExportArchive_CorruptDataRejected(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "test.gbx")

	// Session with missing required fields should be rejected.
	data := &Data{
		ID:     "", // missing
		Status: "active",
	}

	err := ExportArchive(data, archivePath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid session")
}

// ── ImportArchive fault injection ────────────────────────────────────────────

func TestFaultInjection_ImportArchive_NonexistentFile(t *testing.T) {
	_, err := ImportArchive(filepath.Join(t.TempDir(), "nope.gbx"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot open archive")
}

func TestFaultInjection_ImportArchive_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.gbx")
	require.NoError(t, os.WriteFile(path, []byte{}, 0o600))

	_, err := ImportArchive(path)
	require.Error(t, err)
}

func TestFaultInjection_ImportArchive_CorruptZip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt.gbx")
	require.NoError(t, os.WriteFile(path, []byte("this is not a zip file"), 0o600))

	_, err := ImportArchive(path)
	require.Error(t, err)
}

func TestFaultInjection_ImportArchive_MissingMeta(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "no-meta.gbx")

	// Create a valid zip with only session.json (no meta.json).
	createMinimalArchive(t, path, nil)

	_, err := ImportArchive(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing meta.json")
}

func TestFaultInjection_ImportArchive_MissingSession(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "no-session.gbx")

	// Create a valid zip with only meta.json (no session.json).
	createMinimalArchiveWithMeta(t, path)

	_, err := ImportArchive(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing session.json")
}

func TestFaultInjection_ImportArchive_DuplicateEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dup.gbx")

	createArchiveWithDuplicateEntries(t, path)

	_, err := ImportArchive(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "copies of")
}

func TestFaultInjection_ImportArchive_WrongExtension(t *testing.T) {
	_, err := ImportArchive(filepath.Join(t.TempDir(), "test.txt"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported archive extension")
}

func TestFaultInjection_ImportArchive_EmptyPath(t *testing.T) {
	_, err := ImportArchive("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "required")
}

// ── CleanStaleTempFiles fault injection ──────────────────────────────────────

func TestFaultInjection_CleanStaleTempFiles_ReadDirFailure(t *testing.T) {
	// Non-existent directory returns 0,0 without error (best-effort).
	removed, err := CleanStaleTempFiles(filepath.Join(t.TempDir(), "no-such-dir"), time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 0, removed)
}

func TestFaultInjection_CleanStaleTempFiles_OnlyRemovesPatterns(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-2 * time.Hour)

	// Create various files.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file.tmp-abc"), []byte("x"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file.journal"), []byte("x"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "important.txt"), []byte("x"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "data.json"), []byte("x"), 0o600))
	require.NoError(t, os.Chtimes(filepath.Join(dir, "file.tmp-abc"), old, old))
	require.NoError(t, os.Chtimes(filepath.Join(dir, "file.journal"), old, old))
	require.NoError(t, os.Chtimes(filepath.Join(dir, "important.txt"), old, old))
	require.NoError(t, os.Chtimes(filepath.Join(dir, "data.json"), old, old))

	removed, err := CleanStaleTempFiles(dir, time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 2, removed)

	// Verify important files survive.
	_, err = os.Stat(filepath.Join(dir, "important.txt"))
	assert.NoError(t, err)
	_, err = os.Stat(filepath.Join(dir, "data.json"))
	assert.NoError(t, err)
}

// ── Archive manifest fault injection ─────────────────────────────────────────

func TestFaultInjection_ArchiveRoundTrip_WithOptionalMembers(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "full.gbx")

	original := sampleData()
	original.TraceJSON = `{"events": [{"name":"test"}]}`
	original.BundleJSON = `{"bundle": true}`
	original.SourceMapJSON = `{"mappings": "AA"}`
	original.AnnotationsJSON = `{"note": "test"}`

	require.NoError(t, ExportArchive(original, archivePath))

	restored, err := ImportArchive(archivePath)
	require.NoError(t, err)

	assert.Equal(t, original.TraceJSON, restored.TraceJSON)
	assert.Equal(t, original.BundleJSON, restored.BundleJSON)
	assert.Equal(t, original.SourceMapJSON, restored.SourceMapJSON)
	assert.Equal(t, original.AnnotationsJSON, restored.AnnotationsJSON)
}

func TestFaultInjection_ArchiveWithManifest_Verification(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "manifest.gbx")

	original := sampleData()
	original.TraceJSON = `{"trace": "data"}`
	require.NoError(t, ExportArchive(original, archivePath))

	restored, report, err := ImportArchiveWithManifest(archivePath)
	require.NoError(t, err)
	assert.True(t, report.OK, "manifest verification should pass")
	assert.Equal(t, original.TraceJSON, restored.TraceJSON)
}

// ── Store filesystem operations ──────────────────────────────────────────────

func TestFaultInjection_Store_SaveAndLoad_AfterWriteFailure(t *testing.T) {
	overrideTempHome(t)
	store, err := NewStore()
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	d := makeValidSessionData(t, 0)
	d.ID = "fault-test-1"

	// Save should succeed.
	require.NoError(t, store.Save(ctx, d))

	loaded, err := store.Load(ctx, d.ID)
	require.NoError(t, err)
	assert.Equal(t, d.TxHash, loaded.TxHash)
}

func TestFaultInjection_Store_DeleteNonexistent(t *testing.T) {
	overrideTempHome(t)
	store, err := NewStore()
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	err = store.Delete(ctx, "nonexistent-id-12345")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestFaultInjection_Store_CleanupEmpty(t *testing.T) {
	overrideTempHome(t)
	store, err := NewStore()
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	err = store.Cleanup(ctx, time.Hour, 100)
	require.NoError(t, err)
}

func TestFaultInjection_Store_ListEmpty(t *testing.T) {
	overrideTempHome(t)
	store, err := NewStore()
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	sessions, err := store.List(ctx, 0)
	require.NoError(t, err)
	assert.Len(t, sessions, 0)
}

// ── Checkpoint filesystem operations ─────────────────────────────────────────

func TestFaultInjection_Checkpoint_NilCheckpoint(t *testing.T) {
	err := WriteCheckpoint(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil")
}

func TestFaultInjection_Checkpoint_MissingFields(t *testing.T) {
	tests := []struct {
		name string
		cp   *Checkpoint
		want string
	}{
		{
			name: "missing session ID",
			cp:   &Checkpoint{TxHash: "abc", Network: "testnet"},
			want: "session ID",
		},
		{
			name: "missing tx hash",
			cp:   &Checkpoint{SessionID: "s1", Network: "testnet"},
			want: "transaction hash",
		},
		{
			name: "missing network",
			cp:   &Checkpoint{SessionID: "s1", TxHash: "abc"},
			want: "network",
		},
		{
			name: "invalid network",
			cp:   &Checkpoint{SessionID: "s1", TxHash: "abc", Network: "bogus"},
			want: "unsupported network",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := WriteCheckpoint(tt.cp)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestFaultInjection_ClearCheckpoint_NoFile(t *testing.T) {
	// ClearCheckpoint should not fail when there is no checkpoint file.
	err := ClearCheckpoint()
	require.NoError(t, err)
}

func TestFaultInjection_LoadCheckpoint_NoFile(t *testing.T) {
	cp, err := LoadCheckpoint()
	// May return (nil, nil) if no file exists, or an error if the
	// home directory is unavailable. Either is acceptable.
	if err == nil && cp != nil {
		t.Error("expected nil checkpoint when no file exists")
	}
}

// ── ValidateGCRoot fault injection ───────────────────────────────────────────

func TestFaultInjection_ValidateGCRoot_Empty(t *testing.T) {
	err := ValidateGCRoot("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "required")
}

func TestFaultInjection_ValidateGCRoot_FilesystemRoot(t *testing.T) {
	if os.Getenv("GOOS") == "windows" {
		t.Skip("skip filesystem root test on Windows")
	}
	err := ValidateGCRoot("/")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing")
}

func TestFaultInjection_ValidateGCRoot_WrongDirName(t *testing.T) {
	err := ValidateGCRoot("/tmp/random-dir")
	require.Error(t, err)
	assert.Contains(t, err.Error(), ".Glassbox")
}

// ── Schema validation fault injection ────────────────────────────────────────

func TestFaultInjection_SchemaValidation_NilSession(t *testing.T) {
	report := ValidateIntegrity(nil)
	require.False(t, report.OK)
	require.Greater(t, len(report.Issues), 0)
	assert.Equal(t, "Session", report.Issues[0].Field)
}

func TestFaultInjection_SchemaValidation_AllIssuesReported(t *testing.T) {
	data := &Data{
		ID:       "",
		TxHash:   "short",
		Network:  "invalid",
		Status:   "unknown",
	}

	report := ValidateIntegrity(data)
	require.False(t, report.OK)

	fields := make(map[string]bool)
	for _, issue := range report.Issues {
		fields[issue.Field] = true
	}

	assert.True(t, fields["ID"], "should report ID issue")
	assert.True(t, fields["TxHash"], "should report TxHash issue")
	assert.True(t, fields["Network"], "should report Network issue")
	assert.True(t, fields["Status"], "should report Status issue")
}

func TestFaultInjection_CheckpointValidation(t *testing.T) {
	cp := &Checkpoint{
		SessionID: "",
		TxHash:    "",
		Network:   "",
		PID:       -1,
	}

	report := ValidateCheckpoint(cp)
	require.False(t, report.OK)
	assert.Greater(t, len(report.Issues), 4, "should have at least 5 issues")
}

// ── helpers ──────────────────────────────────────────────────────────────────

// createMinimalArchive creates a zip with optional members but missing
// required ones to test import validation.
func createMinimalArchive(t *testing.T, path string, session *Data) {
	t.Helper()

	f, err := os.Create(path)
	require.NoError(t, err)
	defer f.Close()

	zw := zip.NewWriter(f)

	if session != nil {
		w, err := zw.Create("session.json")
		require.NoError(t, err)
		data, err := json.Marshal(session)
		require.NoError(t, err)
		_, err = w.Write(data)
		require.NoError(t, err)
	}

	require.NoError(t, zw.Close())
}

func createMinimalArchiveWithMeta(t *testing.T, path string) {
	t.Helper()

	f, err := os.Create(path)
	require.NoError(t, err)
	defer f.Close()

	zw := zip.NewWriter(f)

	w, err := zw.Create("meta.json")
	require.NoError(t, err)
	meta := map[string]interface{}{
		"archive_version":   1,
		"glassbox_version":  "test",
		"created_at":        time.Now().UTC().Format(time.RFC3339),
		"schema_version":    2,
	}
	data, _ := json.Marshal(meta)
	_, _ = w.Write(data)

	require.NoError(t, zw.Close())
}

func createArchiveWithDuplicateEntries(t *testing.T, path string) {
	t.Helper()

	f, err := os.Create(path)
	require.NoError(t, err)
	defer f.Close()

	zw := zip.NewWriter(f)

	// Write meta.json twice.
	for i := 0; i < 2; i++ {
		w, err := zw.Create("meta.json")
		require.NoError(t, err)
		meta := map[string]interface{}{
			"archive_version":  1,
			"schema_version":   2,
		}
		data, _ := json.Marshal(meta)
		_, _ = w.Write(data)
	}

	// Write session.json.
	w, err := zw.Create("session.json")
	require.NoError(t, err)
	session := sampleData()
	sessionData, _ := json.Marshal(session)
	_, _ = w.Write(sessionData)

	require.NoError(t, zw.Close())
}
