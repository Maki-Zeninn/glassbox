// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

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

// withFaultInject installs f as the fault hook for the duration of the test
// and restores the previous (nil) hook afterwards, so tests never leak state
// into one another.
func withFaultInject(t *testing.T, f func(stage string) error) {
	t.Helper()
	prev := faultInject
	faultInject = f
	t.Cleanup(func() { faultInject = prev })
}

func TestWriteFileAtomic_Succeeds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.json")

	require.NoError(t, writeFileAtomic(path, []byte(`{"ok":true}`), 0o600))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, `{"ok":true}`, string(data))

	// No leftover temp files.
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "only the final file should remain")
}

func TestWriteFileAtomic_PreservesPreviousContentOnCrash(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.json")
	require.NoError(t, os.WriteFile(path, []byte("previous"), 0o600))

	stages := []string{faultAfterCreate, faultAfterWrite, faultAfterSync, faultAfterClose, faultBeforeRename}
	for _, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			injected := errors.New("simulated crash at " + stage)
			withFaultInject(t, func(s string) error {
				if s == stage {
					return injected
				}
				return nil
			})

			err := writeFileAtomic(path, []byte("new content"), 0o600)
			require.ErrorIs(t, err, injected)

			// The original file must be untouched — a crash at any stage
			// must never leave a partially written target file.
			data, readErr := os.ReadFile(path)
			require.NoError(t, readErr)
			assert.Equal(t, "previous", string(data), "target file must survive a crash at %s unchanged", stage)

			// No leftover temp file from this attempt.
			entries, err := os.ReadDir(dir)
			require.NoError(t, err)
			for _, e := range entries {
				assert.False(t, isStaleTempName(e.Name()) && e.Name() != filepath.Base(path),
					"temp file %q should have been cleaned up after a crash at %s", e.Name(), stage)
			}
		})
	}
}

func TestWriteFileAtomic_CreatesTargetOnFirstWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "out.json")

	require.NoError(t, writeFileAtomic(path, []byte("hello"), 0o600))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(data))
}

func TestCleanStaleTempFiles_RemovesOldTempAndJournalFiles(t *testing.T) {
	dir := t.TempDir()

	old := time.Now().Add(-2 * time.Hour)
	makeFile := func(name string, mtime time.Time) {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte("x"), 0o600))
		require.NoError(t, os.Chtimes(p, mtime, mtime))
	}

	makeFile("session.gbx.tmp-abc123", old)
	makeFile("session.gbx.journal", old)
	makeFile("fresh.tmp-def456", time.Now())
	makeFile("session.gbx", old) // real file, must never be removed
	makeFile("unrelated.txt", old)

	removed, err := CleanStaleTempFiles(dir, time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 2, removed)

	remaining := map[string]bool{}
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		remaining[e.Name()] = true
	}
	assert.False(t, remaining["session.gbx.tmp-abc123"])
	assert.False(t, remaining["session.gbx.journal"])
	assert.True(t, remaining["fresh.tmp-def456"], "temp files younger than maxAge must be kept")
	assert.True(t, remaining["session.gbx"], "real files must never be removed")
	assert.True(t, remaining["unrelated.txt"], "unrelated files must never be removed")
}

func TestCleanStaleTempFiles_MissingDirIsNotAnError(t *testing.T) {
	removed, err := CleanStaleTempFiles(filepath.Join(t.TempDir(), "does-not-exist"), time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 0, removed)
}
