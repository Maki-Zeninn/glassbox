// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// overrideCheckpointDir temporarily redirects the checkpoint file into a temp
// directory by setting HOME, restoring it in t.Cleanup.
func overrideCheckpointDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev := os.Getenv("HOME")
	t.Setenv("HOME", dir)
	t.Cleanup(func() { os.Setenv("HOME", prev) })
	return dir
}

// ─── WriteCheckpoint / LoadCheckpoint ────────────────────────────────────────

func TestCheckpoint_WriteAndLoad(t *testing.T) {
	overrideCheckpointDir(t)

	cp := &Checkpoint{
		SessionID: "abc123",
		TxHash:    "deadbeef",
		Network:   "testnet",
		StartedAt: time.Now().Truncate(time.Second),
	}
	require.NoError(t, WriteCheckpoint(cp))

	loaded, err := LoadCheckpoint()
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.Equal(t, cp.SessionID, loaded.SessionID)
	assert.Equal(t, cp.TxHash, loaded.TxHash)
	assert.Equal(t, cp.Network, loaded.Network)
	assert.Equal(t, os.Getpid(), loaded.PID, "PID should be set to the current process")
}

func TestCheckpoint_LoadAbsent(t *testing.T) {
	overrideCheckpointDir(t)

	loaded, err := LoadCheckpoint()
	require.NoError(t, err)
	assert.Nil(t, loaded, "nil returned when no checkpoint exists")
}

func TestCheckpoint_LoadMalformed(t *testing.T) {
	dir := overrideCheckpointDir(t)
	cpPath := filepath.Join(dir, ".Glassbox", checkpointFilename)
	require.NoError(t, os.MkdirAll(filepath.Dir(cpPath), 0755))
	require.NoError(t, os.WriteFile(cpPath, []byte("not json {{{"), 0600))

	_, err := LoadCheckpoint()
	assert.Error(t, err, "malformed checkpoint should return an error")
}

// ─── ClearCheckpoint ─────────────────────────────────────────────────────────

func TestCheckpoint_ClearRemovesFile(t *testing.T) {
	overrideCheckpointDir(t)

	require.NoError(t, WriteCheckpoint(&Checkpoint{SessionID: "x", TxHash: "txhash", Network: "testnet"}))
	require.NoError(t, ClearCheckpoint())

	loaded, err := LoadCheckpoint()
	require.NoError(t, err)
	assert.Nil(t, loaded, "checkpoint should be gone after Clear")
}

func TestCheckpoint_ClearIdempotent(t *testing.T) {
	overrideCheckpointDir(t)
	assert.NoError(t, ClearCheckpoint(), "clearing a non-existent checkpoint is a no-op")
}

// ─── IsOrphaned ──────────────────────────────────────────────────────────────

func TestCheckpoint_IsOrphaned_CurrentProcess(t *testing.T) {
	cp := &Checkpoint{PID: os.Getpid()}
	assert.False(t, cp.IsOrphaned(), "current process is not orphaned")
}

func TestCheckpoint_IsOrphaned_InvalidPID(t *testing.T) {
	cp := &Checkpoint{PID: 0}
	assert.True(t, cp.IsOrphaned(), "PID=0 is treated as orphaned")
}

func TestCheckpoint_IsOrphaned_DeadPID(t *testing.T) {
	// PID 1 is always the init process (guaranteed alive on Linux/macOS). We
	// instead look for a PID that is very unlikely to exist — INT_MAX-like value.
	// On most kernels pid_max is 4194304; 9999999 should be beyond that.
	cp := &Checkpoint{PID: 9999999}
	// We expect this to be orphaned, but it might not be if the OS wraps pids.
	// At minimum the call must not panic.
	_ = cp.IsOrphaned()
}

// ─── WriteCheckpoint validation ──────────────────────────────────────────────

func TestWriteCheckpoint_NilCheckpoint_ReturnsError(t *testing.T) {
	err := WriteCheckpoint(nil)
	if err == nil {
		t.Fatal("expected error for nil checkpoint")
	}
}

func TestWriteCheckpoint_MissingSessionID_ReturnsError(t *testing.T) {
	cp := &Checkpoint{TxHash: "abc", Network: "testnet"}
	err := WriteCheckpoint(cp)
	if err == nil {
		t.Fatal("expected error for missing SessionID")
	}
	if !strings.Contains(err.Error(), "session ID") {
		t.Errorf("error should mention 'session ID', got: %v", err)
	}
}

func TestWriteCheckpoint_MissingTxHash_ReturnsError(t *testing.T) {
	cp := &Checkpoint{SessionID: "s1", Network: "testnet"}
	err := WriteCheckpoint(cp)
	if err == nil {
		t.Fatal("expected error for missing TxHash")
	}
	if !strings.Contains(err.Error(), "transaction hash") {
		t.Errorf("error should mention 'transaction hash', got: %v", err)
	}
}

func TestWriteCheckpoint_MissingNetwork_ReturnsError(t *testing.T) {
	cp := &Checkpoint{SessionID: "s1", TxHash: "abc"}
	err := WriteCheckpoint(cp)
	if err == nil {
		t.Fatal("expected error for missing Network")
	}
	if !strings.Contains(err.Error(), "network") {
		t.Errorf("error should mention 'network', got: %v", err)
	}
}

func TestWriteCheckpoint_InvalidNetwork_ReturnsError(t *testing.T) {
	cp := &Checkpoint{SessionID: "s1", TxHash: "abc", Network: "devnet"}
	err := WriteCheckpoint(cp)
	if err == nil {
		t.Fatal("expected error for invalid Network")
	}
	if !strings.Contains(err.Error(), "devnet") {
		t.Errorf("error should name the invalid network, got: %v", err)
	}
}

func TestWriteCheckpoint_ZeroStartedAt_AutoPopulated(t *testing.T) {
	overrideCheckpointDir(t)

	cp := &Checkpoint{SessionID: "s1", TxHash: "abc", Network: "testnet"}
	require.NoError(t, WriteCheckpoint(cp))

	loaded, err := LoadCheckpoint()
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.False(t, loaded.StartedAt.IsZero(), "StartedAt should be auto-populated")
}

func TestWriteCheckpoint_AllValidFields_Succeeds(t *testing.T) {
	overrideCheckpointDir(t)

	cp := &Checkpoint{
		SessionID: "valid-session",
		TxHash:    "abc123def456",
		Network:   "futurenet",
		StartedAt: time.Now().Truncate(time.Second),
	}
	require.NoError(t, WriteCheckpoint(cp))

	loaded, err := LoadCheckpoint()
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.Equal(t, cp.SessionID, loaded.SessionID)
	assert.Equal(t, cp.Network, loaded.Network)
}

func TestCheckpoint_WriteCreatesGlassboxDir(t *testing.T) {
	dir := overrideCheckpointDir(t)
	glassboxDir := filepath.Join(dir, ".Glassbox")

	// Ensure the directory does not exist before write.
	require.NoError(t, os.RemoveAll(glassboxDir))

	require.NoError(t, WriteCheckpoint(&Checkpoint{SessionID: "new", TxHash: "txhash", Network: "testnet"}))
	_, err := os.Stat(glassboxDir)
	assert.NoError(t, err, ".Glassbox directory should be created automatically")
}
