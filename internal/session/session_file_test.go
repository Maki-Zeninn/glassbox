// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Tests for transactional on-disk session persistence (SaveToFile / LoadFromFile)
// and stale temp-file recovery (RecoverStaleSessionFiles).

package session

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── SaveToFile / LoadFromFile round-trip ─────────────────────────────────────

func TestSaveToFile_LoadFromFile_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")

	original := sampleData()
	require.NoError(t, SaveToFile(original, path))

	restored, err := LoadFromFile(path)
	require.NoError(t, err)

	assert.Equal(t, original.ID, restored.ID)
	assert.Equal(t, original.TxHash, restored.TxHash)
	assert.Equal(t, original.Network, restored.Network)
}

func TestSaveToFile_NilData_ReturnsError(t *testing.T) {
	err := SaveToFile(nil, filepath.Join(t.TempDir(), "session.json"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil")
}

func TestSaveToFile_EmptyPath_ReturnsError(t *testing.T) {
	err := SaveToFile(sampleData(), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "path")
}

func TestLoadFromFile_MissingFile_ReturnsError(t *testing.T) {
	_, err := LoadFromFile(filepath.Join(t.TempDir(), "nonexistent.json"))
	require.Error(t, err)
}

func TestLoadFromFile_EmptyPath_ReturnsError(t *testing.T) {
	_, err := LoadFromFile("")
	require.Error(t, err)
}

func TestLoadFromFile_CorruptJSON_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	require.NoError(t, os.WriteFile(path, []byte("not json {{{"), 0o600))
	_, err := LoadFromFile(path)
	require.Error(t, err)
}

// ── Atomicity: previous file untouched on write failure ──────────────────────

func TestSaveToFile_CrashBeforeRename_PreviousFileUntouched(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")

	original := sampleData()
	require.NoError(t, SaveToFile(original, path))
	before, err := os.ReadFile(path)
	require.NoError(t, err)

	injected := errors.New("simulated crash before rename")
	withFaultInject(t, func(stage string) error {
		if stage == faultBeforeRename {
			return injected
		}
		return nil
	})

	modified := sampleData()
	modified.TxHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	err = SaveToFile(modified, path)
	require.ErrorIs(t, err, injected)

	after, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.Equal(t, before, after, "original file must be untouched after crash before rename")

	// No leftover temp file.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		assert.False(t, isStaleTempName(e.Name()),
			"no temp file should remain after a failed write: %s", e.Name())
	}
}

func TestSaveToFile_DiskFullAfterWrite_PreviousFileUntouched(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")

	original := sampleData()
	require.NoError(t, SaveToFile(original, path))

	diskFull := errors.New("no space left on device")
	withFaultInject(t, func(stage string) error {
		if stage == faultAfterWrite {
			return diskFull
		}
		return nil
	})

	err := SaveToFile(sampleData(), path)
	require.ErrorIs(t, err, diskFull)

	loaded, readErr := LoadFromFile(path)
	require.NoError(t, readErr, "file must still be loadable after disk-full error")
	assert.Equal(t, original.ID, loaded.ID)
}

// ── Readers see either old complete or new complete session ──────────────────

func TestSaveToFile_AllFaultStages_OldOrNewComplete(t *testing.T) {
	stages := []string{
		faultAfterCreate, faultAfterWrite,
		faultAfterSync, faultAfterClose, faultBeforeRename,
	}
	for _, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "session.json")

			old := sampleData()
			old.TxHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			require.NoError(t, SaveToFile(old, path))

			injected := errors.New("crash at " + stage)
			withFaultInject(t, func(s string) error {
				if s == stage {
					return injected
				}
				return nil
			})

			new := sampleData()
			new.TxHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
			err := SaveToFile(new, path)
			require.Error(t, err)

			// File must be either the old complete session or, if the rename
			// succeeded before the fault fired, the new complete session.
			loaded, readErr := LoadFromFile(path)
			require.NoError(t, readErr,
				"file must be readable after fault at stage %s", stage)
			assert.True(t,
				loaded.TxHash == old.TxHash || loaded.TxHash == new.TxHash,
				"file must contain either the old or new complete session at stage %s, got %s",
				stage, loaded.TxHash,
			)
		})
	}
}

// ── RecoverStaleSessionFiles ──────────────────────────────────────────────────

func TestRecoverStaleSessionFiles_RemovesOldTempFiles(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-2 * time.Hour)

	makeFile := func(name string, mtime time.Time) {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte("x"), 0o600))
		require.NoError(t, os.Chtimes(p, mtime, mtime))
	}

	makeFile("session.json.tmp-stale", old)
	makeFile("active_session.json.journal", old)
	makeFile("session.json.tmp-fresh", time.Now()) // too young
	makeFile("sessions.db", old)                  // real file, must survive

	n, names, err := RecoverStaleSessionFiles(dir, time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.Contains(t, names, "session.json.tmp-stale")
	assert.Contains(t, names, "active_session.json.journal")

	// Fresh temp and real files must survive.
	_, err = os.Stat(filepath.Join(dir, "session.json.tmp-fresh"))
	assert.NoError(t, err, "fresh temp file must not be removed")
	_, err = os.Stat(filepath.Join(dir, "sessions.db"))
	assert.NoError(t, err, "real file must not be removed")
}

func TestRecoverStaleSessionFiles_NonExistentDir_NoError(t *testing.T) {
	n, names, err := RecoverStaleSessionFiles(
		filepath.Join(t.TempDir(), "does-not-exist"), time.Hour,
	)
	require.NoError(t, err)
	assert.Equal(t, 0, n)
	assert.Empty(t, names)
}

func TestRecoverStaleSessionFiles_EmptyDir_NoError(t *testing.T) {
	n, names, err := RecoverStaleSessionFiles(t.TempDir(), time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 0, n)
	assert.Empty(t, names)
}

// ── SaveToFile: no leftover temp on success ───────────────────────────────────

func TestSaveToFile_NoLeftoverTempOnSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")

	require.NoError(t, SaveToFile(sampleData(), path))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "only the final file should remain after a successful write")
	assert.Equal(t, "session.json", entries[0].Name())
}

// ── SaveToFile: creates parent directory ─────────────────────────────────────

func TestSaveToFile_CreatesParentDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deep", "session.json")

	require.NoError(t, SaveToFile(sampleData(), path))

	_, err := os.Stat(path)
	assert.NoError(t, err, "file must exist after SaveToFile even when parent dirs were absent")
}
