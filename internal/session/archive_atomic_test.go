// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExportArchive_NoLeftoverTempOrJournalOnSuccess(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "session.gbx")

	require.NoError(t, ExportArchive(sampleData(), archivePath))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "only the final archive should remain")
	assert.Equal(t, "session.gbx", entries[0].Name())
}

func TestExportArchive_PreviousArchiveUntouchedOnFailure(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "session.gbx")

	require.NoError(t, ExportArchive(sampleData(), archivePath))
	before, err := os.ReadFile(archivePath)
	require.NoError(t, err)

	// A session that fails ValidateIntegrity aborts before any temp file is
	// created, so re-exporting an invalid session must never disturb a
	// previously exported good archive at the same path.
	invalid := sampleData()
	invalid.Network = "not-a-real-network"
	err = ExportArchive(invalid, archivePath)
	require.Error(t, err)

	after, err := os.ReadFile(archivePath)
	require.NoError(t, err)
	assert.Equal(t, before, after, "existing archive must be untouched when a re-export fails")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "no leftover temp or journal file after a failed export")
}

func TestWriteExportJournal_LeftoverIsCleanedByDoctor(t *testing.T) {
	dir := t.TempDir()
	destPath := filepath.Join(dir, "session.gbx")
	journalPath := destPath + ".journal"

	// Simulate a crash: the journal was written but the process died before
	// ExportArchive's deferred cleanup ran.
	require.NoError(t, writeExportJournal(journalPath, destPath))
	require.NoError(t, os.Chtimes(journalPath, time.Now().Add(-2*time.Hour), time.Now().Add(-2*time.Hour)))

	removed, err := CleanStaleTempFiles(dir, time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 1, removed)

	_, statErr := os.Stat(journalPath)
	assert.True(t, os.IsNotExist(statErr), "stale journal should be removed")
}
