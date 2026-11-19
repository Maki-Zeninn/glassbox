// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

//go:build !windows

package session

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── POSIX process tests ──────────────────────────────────────────────────────

func TestProcessAlive_SelfPID(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Error("current process should be alive")
	}
}

func TestProcessAlive_InvalidPID(t *testing.T) {
	// PID 0 is the swapper/scheduler on Linux and should be alive,
	// but PID -1 should not be alive on any platform.
	if processAlive(-1) {
		t.Error("PID -1 should not be alive")
	}
}

func TestProcessAlive_KnownDeadPID(t *testing.T) {
	// PID 1 on most systems is init and alive; very high PIDs that have
	// never existed should not be alive.
	if processAlive(99999999) {
		t.Error("very high PID should not be alive")
	}
}

// ── syncDir POSIX test ───────────────────────────────────────────────────────

func TestSyncDirPOSIX_NonexistentDir(t *testing.T) {
	err := syncDir(filepath.Join(t.TempDir(), "nonexistent"))
	// syncDir should return an error for a nonexistent directory on POSIX.
	assert.Error(t, err)
}

func TestSyncDirPOSIX_ExistingDir(t *testing.T) {
	dir := t.TempDir()
	err := syncDir(dir)
	// Should succeed for an existing directory.
	assert.NoError(t, err)
}

// ── Checkpoint POSIX tests ──────────────────────────────────────────────────

func TestCheckpoint_WriteAndLoad(t *testing.T) {
	cp := &Checkpoint{
		SessionID: "test-session-posix",
		TxHash:    "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
		Network:   "testnet",
	}

	err := WriteCheckpoint(cp)
	require.NoError(t, err)
	defer ClearCheckpoint()

	loaded, err := LoadCheckpoint()
	require.NoError(t, err)
	require.NotNil(t, loaded)

	assert.Equal(t, cp.SessionID, loaded.SessionID)
	assert.Equal(t, cp.TxHash, loaded.TxHash)
	assert.Equal(t, cp.Network, loaded.Network)
	assert.Equal(t, os.Getpid(), loaded.PID)
}

func TestCheckpoint_IsOrphaned_SelfPID(t *testing.T) {
	cp := &Checkpoint{
		SessionID: "test",
		TxHash:    "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
		Network:   "testnet",
		PID:       os.Getpid(),
	}

	// Self PID should not be orphaned.
	if cp.IsOrphaned() {
		t.Error("current process should not be orphaned")
	}
}

func TestCheckpoint_IsOrphaned_InvalidPID(t *testing.T) {
	cp := &Checkpoint{
		SessionID: "test",
		TxHash:    "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
		Network:   "testnet",
		PID:       -1,
	}

	// Invalid PID should be considered orphaned.
	if !cp.IsOrphaned() {
		t.Error("PID -1 should be orphaned")
	}
}

func TestCheckpoint_IsOrphaned_ZeroPID(t *testing.T) {
	cp := &Checkpoint{
		SessionID: "test",
		TxHash:    "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
		Network:   "testnet",
		PID:       0,
	}

	// PID 0 should be considered orphaned (not a valid user process).
	if !cp.IsOrphaned() {
		t.Error("PID 0 should be orphaned")
	}
}

// ── writeFileAtomic POSIX permissions ────────────────────────────────────────

func TestWriteFileAtomic_Permissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "perm-test.json")

	require.NoError(t, writeFileAtomic(path, []byte("test"), 0o600))

	info, err := os.Stat(path)
	require.NoError(t, err)

	perm := info.Mode().Perm()
	assert.Equal(t, os.FileMode(0o600), perm,
		"file should have 0600 permissions, got %o", perm)
}

func TestWriteFileAtomic_DifferentPermissions(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name string
		perm os.FileMode
	}{
		{"0600", 0o600},
		{"0644", 0o644},
		{"0755", 0o755},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(dir, "perm-"+tt.name+".json")
			require.NoError(t, writeFileAtomic(path, []byte("data"), tt.perm))

			info, err := os.Stat(path)
			require.NoError(t, err)
			assert.Equal(t, tt.perm, info.Mode().Perm())
		})
	}
}

// ── syscall.Kill signal 0 probe ──────────────────────────────────────────────

func TestSignalZero_Probe(t *testing.T) {
	// Signal 0 (existence probe) should succeed for the current process.
	err := syscall.Kill(os.Getpid(), 0)
	if err != nil {
		t.Errorf("signal 0 to self failed: %v", err)
	}

	// Signal 0 to PID -1 should fail (invalid).
	err = syscall.Kill(-1, 0)
	if err == nil {
		t.Error("signal 0 to PID -1 should have failed")
	}
}

// ── syncDir edge cases ──────────────────────────────────────────────────────

func TestSyncDir_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	err := syncDir(dir)
	assert.NoError(t, err)
}

func TestSyncDir_Nonexistent(t *testing.T) {
	err := syncDir(filepath.Join(t.TempDir(), "does-not-exist"))
	assert.Error(t, err)
}
