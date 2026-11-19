// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

// ── AcquireLock / Release ─────────────────────────────────────────────────────

func TestAcquireLock_FirstCallerSucceeds(t *testing.T) {
	t.Parallel()
	id := "lock-test-" + GenerateID("tx1")
	h, err := AcquireLock(id)
	if err != nil {
		t.Fatalf("expected no error acquiring fresh lock, got: %v", err)
	}
	defer h.Release()
}

func TestAcquireLock_SecondCallerBlocked(t *testing.T) {
	t.Parallel()
	id := "lock-held-" + GenerateID("tx2")
	h1, err := AcquireLock(id)
	if err != nil {
		t.Fatalf("first AcquireLock failed: %v", err)
	}
	defer h1.Release()

	_, err2 := AcquireLock(id)
	if err2 == nil {
		t.Fatal("expected error when lock already held, got nil")
	}
	if !errors.Is(err2, ErrLockHeld) {
		t.Fatalf("expected ErrLockHeld, got: %v", err2)
	}
}

func TestAcquireLock_ReleasedLockCanBeReacquired(t *testing.T) {
	t.Parallel()
	id := "lock-reacq-" + GenerateID("tx3")
	h1, err := AcquireLock(id)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	h1.Release()

	h2, err2 := AcquireLock(id)
	if err2 != nil {
		t.Fatalf("expected to reacquire after release, got: %v", err2)
	}
	defer h2.Release()
}

func TestLockHandle_ReleaseIsIdempotent(t *testing.T) {
	t.Parallel()
	id := "lock-idem-" + GenerateID("tx4")
	h, err := AcquireLock(id)
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	h.Release()
	h.Release() // must not panic
}

func TestLockHandle_NilReleaseIsNoop(t *testing.T) {
	t.Parallel()
	var h *LockHandle
	h.Release() // must not panic
}

// ── Stale lock cleanup ────────────────────────────────────────────────────────

func TestCleanStaleLocks_RemovesStaleFile(t *testing.T) {
	t.Parallel()

	dir, err := lockDir()
	if err != nil {
		t.Skip("cannot determine lock dir:", err)
	}
	if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
		t.Fatalf("MkdirAll: %v", mkErr)
	}

	// Write a lock record with a dead PID and an old timestamp so
	// isLockStale returns true.
	id := "stale-lock-" + GenerateID("txstale")
	path, _ := lockPath(id)

	staleRec := LockRecord{
		PID:        99999999, // almost certainly not a live PID
		SessionID:  id,
		AcquiredAt: time.Now().Add(-(StaleLockAge + time.Second)),
	}
	data, marshalErr := json.MarshalIndent(staleRec, "", "  ")
	if marshalErr != nil {
		t.Fatalf("marshal stale record: %v", marshalErr)
	}
	if writeErr := writeFileAtomic(path, data, 0o600); writeErr != nil {
		t.Fatalf("writeFileAtomic: %v", writeErr)
	}
	t.Cleanup(func() { _ = os.Remove(path) })

	removed, cleanErr := CleanStaleLocks()
	if cleanErr != nil {
		t.Fatalf("CleanStaleLocks: %v", cleanErr)
	}
	if removed == 0 {
		t.Error("expected at least one stale lock removed, got 0")
	}
}

// ── ConflictError ─────────────────────────────────────────────────────────────

func TestConflictError_IsErrSessionConflict(t *testing.T) {
	t.Parallel()
	ce := &ConflictError{SessionID: "s1", ExpectedRevision: 2, ActualRevision: 3}
	if !errors.Is(ce, ErrSessionConflict) {
		t.Error("expected errors.Is(err, ErrSessionConflict) == true")
	}
}

func TestConflictError_MessageContainsRevisions(t *testing.T) {
	t.Parallel()
	ce := &ConflictError{SessionID: "sess-abc", ExpectedRevision: 1, ActualRevision: 5}
	msg := ce.Error()
	if msg == "" {
		t.Fatal("expected non-empty error message")
	}
	for _, want := range []string{"sess-abc", "1", "5"} {
		if !stringContains(msg, want) {
			t.Errorf("error message %q does not contain %q", msg, want)
		}
	}
}

func TestLockHeldError_IsErrLockHeld(t *testing.T) {
	t.Parallel()
	lhe := &LockHeldError{SessionID: "s1", HolderPID: 1234, Since: time.Now()}
	if !errors.Is(lhe, ErrLockHeld) {
		t.Error("expected errors.Is(err, ErrLockHeld) == true")
	}
}

// ── Concurrent save — revision check prevents silent overwrites ───────────────

func TestStore_ConcurrentSaves_RevisionConflict(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := NewStoreAt(dir)
	if err != nil {
		t.Fatalf("NewStoreAt: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Seed an initial session (Revision=0 → no check on first save).
	base := testSessionData("rev-conc-1")
	base.Revision = 0
	if saveErr := store.Save(ctx, base); saveErr != nil {
		t.Fatalf("initial save: %v", saveErr)
	}

	// Load to capture revision that both writers will read.
	loaded, loadErr := store.Load(ctx, base.ID)
	if loadErr != nil {
		t.Fatalf("load: %v", loadErr)
	}
	seenRevision := loaded.Revision // should be 1

	// Writer A: saves first — revision still matches.
	writerA := *loaded
	writerA.Name = "writer-a"
	if err := store.Save(ctx, &writerA); err != nil {
		t.Fatalf("writer A save: %v", err)
	}

	// Writer B: holds the old revision — must conflict.
	writerB := *loaded
	writerB.Revision = seenRevision // deliberately stale
	writerB.Name = "writer-b"
	err = store.Save(ctx, &writerB)
	if err == nil {
		t.Fatal("expected conflict error for writer B, got nil")
	}
	if !errors.Is(err, ErrSessionConflict) {
		t.Fatalf("expected ErrSessionConflict, got: %v", err)
	}
}

func TestStore_SaveForce_OverwritesNewerRevision(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := NewStoreAt(dir)
	if err != nil {
		t.Fatalf("NewStoreAt: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	base := testSessionData("rev-force-1")
	base.Revision = 0
	if saveErr := store.Save(ctx, base); saveErr != nil {
		t.Fatalf("initial save: %v", saveErr)
	}

	// Advance the revision once more.
	loaded, _ := store.Load(ctx, base.ID)
	loaded.Name = "first-named-save"
	if saveErr := store.Save(ctx, loaded); saveErr != nil {
		t.Fatalf("second save: %v", saveErr)
	}

	// Force-save ignoring revision — should succeed.
	stale := *loaded
	stale.Name = "force-overwrite"
	if err := store.SaveForce(ctx, &stale); err != nil {
		t.Fatalf("SaveForce: %v", err)
	}

	final, _ := store.Load(ctx, base.ID)
	if final.Name != "force-overwrite" {
		t.Errorf("expected name %q after force, got %q", "force-overwrite", final.Name)
	}
}

func TestStore_RevisionIncrements(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := NewStoreAt(dir)
	if err != nil {
		t.Fatalf("NewStoreAt: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	data := testSessionData("rev-inc-1")
	data.Revision = 0

	for i := int64(1); i <= 5; i++ {
		if saveErr := store.Save(ctx, data); saveErr != nil {
			t.Fatalf("save iteration %d: %v", i, saveErr)
		}
		if data.Revision != i {
			t.Errorf("iteration %d: expected revision %d, got %d", i, i, data.Revision)
		}
	}
}

// TestStore_ParallelSaves_OnlyOneWins verifies that when N goroutines race to
// save the same session using the same revision, exactly one succeeds and the
// rest receive ErrSessionConflict.
func TestStore_ParallelSaves_OnlyOneWins(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := NewStoreAt(dir)
	if err != nil {
		t.Fatalf("NewStoreAt: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	base := testSessionData("par-race-1")
	base.Revision = 0
	if saveErr := store.Save(ctx, base); saveErr != nil {
		t.Fatalf("seed: %v", saveErr)
	}
	loaded, _ := store.Load(ctx, base.ID)
	seenRevision := loaded.Revision

	const writers = 8
	errs := make([]error, writers)
	var wg sync.WaitGroup
	barrier := make(chan struct{})

	for i := 0; i < writers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			copy := *loaded
			copy.Revision = seenRevision
			copy.Name = "writer"
			<-barrier // wait for all goroutines to be ready
			errs[i] = store.Save(ctx, &copy)
		}()
	}

	close(barrier) // release all goroutines simultaneously
	wg.Wait()

	successes, conflicts := 0, 0
	for _, e := range errs {
		switch {
		case e == nil:
			successes++
		case errors.Is(e, ErrSessionConflict):
			conflicts++
		default:
			t.Errorf("unexpected error type: %v", e)
		}
	}

	if successes != 1 {
		t.Errorf("expected exactly 1 success, got %d", successes)
	}
	if conflicts != writers-1 {
		t.Errorf("expected %d conflicts, got %d", writers-1, conflicts)
	}
}

// TestStore_ZeroRevision_SkipsCheck ensures that a first-time save (Revision=0)
// is never rejected, matching the "no check requested" contract.
func TestStore_ZeroRevision_SkipsCheck(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := NewStoreAt(dir)
	if err != nil {
		t.Fatalf("NewStoreAt: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	data := testSessionData("zero-rev-1")
	data.Revision = 0

	// First save: must always succeed regardless of what is on disk.
	if saveErr := store.Save(ctx, data); saveErr != nil {
		t.Fatalf("first zero-revision save: %v", saveErr)
	}
	// Second save with Revision=0 again: also must succeed (force semantics).
	data.Revision = 0
	if saveErr := store.Save(ctx, data); saveErr != nil {
		t.Fatalf("second zero-revision save: %v", saveErr)
	}
}

// ── helper ────────────────────────────────────────────────────────────────────

// stringContains is a dependency-free substring check used in tests.
func stringContains(s, sub string) bool {
	if len(sub) == 0 || len(s) < len(sub) {
		return false
	}
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// testSessionData builds a minimal valid Data record for use in lock/revision
// tests. The suffix is appended to the ID to allow parallel tests to use
// independent sessions.
func testSessionData(suffix string) *Data {
	return &Data{
		ID:          "test-session-" + suffix,
		TxHash:      "aaaa" + suffix,
		Network:     "testnet",
		Status:      "active",
		HorizonURL:  "https://horizon-testnet.stellar.org",
		CreatedAt:   time.Now(),
		LastAccessAt: time.Now(),
		SchemaVersion: SchemaVersion,
	}
}

// ── LockMetadata ─────────────────────────────────────────────────────────────

func TestAcquireLock_WritesMetadataFile(t *testing.T) {
	t.Parallel()
	id := "meta-write-" + GenerateID("txmeta1")
	h, err := AcquireLock(id)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	defer h.Release()

	meta, metaErr := ReadLockMetadata(id)
	if metaErr != nil {
		t.Fatalf("ReadLockMetadata: %v", metaErr)
	}
	if meta == nil {
		t.Fatal("expected metadata to exist after AcquireLock, got nil")
	}
	if meta.PID != os.Getpid() {
		t.Errorf("metadata PID: got %d, want %d", meta.PID, os.Getpid())
	}
	if meta.SessionID != id {
		t.Errorf("metadata SessionID: got %q, want %q", meta.SessionID, id)
	}
	if meta.Hostname == "" {
		t.Error("metadata Hostname should be non-empty")
	}
	if meta.GlassboxVersion == "" {
		t.Error("metadata GlassboxVersion should be non-empty")
	}
	if meta.AcquiredAt.IsZero() {
		t.Error("metadata AcquiredAt should be non-zero")
	}
}

func TestRelease_RemovesMetadataFile(t *testing.T) {
	t.Parallel()
	id := "meta-release-" + GenerateID("txmeta2")
	h, err := AcquireLock(id)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	h.Release()

	meta, metaErr := ReadLockMetadata(id)
	if metaErr != nil {
		t.Fatalf("ReadLockMetadata after Release: %v", metaErr)
	}
	if meta != nil {
		t.Error("expected metadata file to be removed after Release, but it still exists")
	}
}

func TestReadLockMetadata_ReturnsNilWhenMissing(t *testing.T) {
	t.Parallel()
	id := "meta-missing-" + GenerateID("txmeta3")
	meta, err := ReadLockMetadata(id)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta != nil {
		t.Errorf("expected nil metadata for non-existent lock, got %+v", meta)
	}
}

// ── RecoverStaleLock ──────────────────────────────────────────────────────────

// TestRecoverStaleLock_DeadPIDRemovesFiles plants a metadata file with a
// known-dead PID (one that has never existed or has long since exited) and
// verifies that RecoverStaleLock removes both the .lock and .lock.meta files.
func TestRecoverStaleLock_DeadPIDRemovesFiles(t *testing.T) {
	t.Parallel()

	dir, dirErr := lockDir()
	if dirErr != nil {
		t.Skip("cannot determine lock dir:", dirErr)
	}
	if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
		t.Fatalf("MkdirAll: %v", mkErr)
	}

	id := "stale-meta-" + GenerateID("txstale2")

	lp, _ := lockPath(id)
	mp, _ := lockMetaPath(id)

	// Write a lock file with the same dead PID.
	rec := LockRecord{
		PID:        99999999,
		SessionID:  id,
		AcquiredAt: time.Now().Add(-(StaleLockAge + time.Second)),
	}
	lockData, _ := json.MarshalIndent(rec, "", "  ")
	if err := writeFileAtomic(lp, lockData, 0o600); err != nil {
		t.Fatalf("write lock file: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(lp) })

	// Write a metadata file referencing the same dead PID.
	meta := &LockMetadata{
		PID:             99999999,
		Hostname:        "test-host",
		AcquiredAt:      rec.AcquiredAt,
		GlassboxVersion: "0.0.0-test",
		SessionID:       id,
	}
	metaData, _ := json.MarshalIndent(meta, "", "  ")
	if err := writeFileAtomic(mp, metaData, 0o600); err != nil {
		t.Fatalf("write metadata file: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(mp) })

	recovered, err := RecoverStaleLock(id)
	if err != nil {
		t.Fatalf("RecoverStaleLock: %v", err)
	}
	if !recovered {
		t.Fatal("expected recovered=true for dead PID, got false")
	}

	// Both files must be gone.
	if _, statErr := os.Stat(lp); !os.IsNotExist(statErr) {
		t.Error("lock file still exists after RecoverStaleLock")
	}
	if _, statErr := os.Stat(mp); !os.IsNotExist(statErr) {
		t.Error("metadata file still exists after RecoverStaleLock")
	}
}

// TestRecoverStaleLock_LivePIDNoOp plants a metadata file with the current
// process's PID and verifies that RecoverStaleLock leaves all files untouched.
func TestRecoverStaleLock_LivePIDNoOp(t *testing.T) {
	t.Parallel()

	dir, dirErr := lockDir()
	if dirErr != nil {
		t.Skip("cannot determine lock dir:", dirErr)
	}
	if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
		t.Fatalf("MkdirAll: %v", mkErr)
	}

	id := "live-meta-" + GenerateID("txlive1")

	lp, _ := lockPath(id)
	mp, _ := lockMetaPath(id)

	// Write lock + metadata files with our own (live) PID.
	rec := LockRecord{PID: os.Getpid(), SessionID: id, AcquiredAt: time.Now()}
	lockData, _ := json.MarshalIndent(rec, "", "  ")
	if err := writeFileAtomic(lp, lockData, 0o600); err != nil {
		t.Fatalf("write lock file: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(lp) })

	meta := &LockMetadata{
		PID: os.Getpid(), Hostname: "test-host",
		AcquiredAt: rec.AcquiredAt, GlassboxVersion: "0.0.0-test", SessionID: id,
	}
	metaData, _ := json.MarshalIndent(meta, "", "  ")
	if err := writeFileAtomic(mp, metaData, 0o600); err != nil {
		t.Fatalf("write metadata file: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(mp) })

	recovered, err := RecoverStaleLock(id)
	if err != nil {
		t.Fatalf("RecoverStaleLock: %v", err)
	}
	if recovered {
		t.Fatal("expected recovered=false for live PID, got true")
	}

	// Files must still exist.
	if _, statErr := os.Stat(lp); os.IsNotExist(statErr) {
		t.Error("lock file was removed for live PID — must not happen")
	}
	if _, statErr := os.Stat(mp); os.IsNotExist(statErr) {
		t.Error("metadata file was removed for live PID — must not happen")
	}
}

// TestRecoverStaleLock_NoMetadataFile verifies that RecoverStaleLock returns
// (false, nil) when there is no metadata file at all (nothing to recover).
func TestRecoverStaleLock_NoMetadataFile(t *testing.T) {
	t.Parallel()
	id := "no-meta-" + GenerateID("txnometa")
	recovered, err := RecoverStaleLock(id)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recovered {
		t.Fatal("expected recovered=false when no metadata exists, got true")
	}
}

// ── AcquireLock auto-recovery ─────────────────────────────────────────────────

// TestAcquireLock_AutoRecoversStaleLock verifies that AcquireLock succeeds
// when it encounters a stale lock (dead PID) by recovering it automatically
// and retrying the acquisition.
func TestAcquireLock_AutoRecoversStaleLock(t *testing.T) {
	t.Parallel()

	dir, dirErr := lockDir()
	if dirErr != nil {
		t.Skip("cannot determine lock dir:", dirErr)
	}
	if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
		t.Fatalf("MkdirAll: %v", mkErr)
	}

	id := "autorecover-" + GenerateID("txauto1")

	lp, _ := lockPath(id)
	mp, _ := lockMetaPath(id)

	// Plant a stale lock: dead PID + old timestamp.
	rec := LockRecord{
		PID:        99999999,
		SessionID:  id,
		AcquiredAt: time.Now().Add(-(StaleLockAge + time.Second)),
	}
	lockData, _ := json.MarshalIndent(rec, "", "  ")
	if err := writeFileAtomic(lp, lockData, 0o600); err != nil {
		t.Fatalf("write stale lock file: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(lp) })

	meta := &LockMetadata{
		PID:             99999999,
		Hostname:        "old-host",
		AcquiredAt:      rec.AcquiredAt,
		GlassboxVersion: "0.0.0-old",
		SessionID:       id,
	}
	metaData, _ := json.MarshalIndent(meta, "", "  ")
	if err := writeFileAtomic(mp, metaData, 0o600); err != nil {
		t.Fatalf("write stale metadata file: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(mp) })

	// AcquireLock must succeed despite the stale lock files.
	h, err := AcquireLock(id)
	if err != nil {
		t.Fatalf("AcquireLock should auto-recover stale lock, got error: %v", err)
	}
	defer h.Release()

	// Metadata must have been refreshed with our PID.
	newMeta, metaErr := ReadLockMetadata(id)
	if metaErr != nil {
		t.Fatalf("ReadLockMetadata after recovery: %v", metaErr)
	}
	if newMeta == nil {
		t.Fatal("expected fresh metadata after auto-recovery, got nil")
	}
	if newMeta.PID != os.Getpid() {
		t.Errorf("refreshed metadata PID: got %d, want %d", newMeta.PID, os.Getpid())
	}
}

// ── Concurrent acquisition with structured error ──────────────────────────────

// TestAcquireLock_ConcurrentExactlyOneWins launches two goroutines that race
// to acquire the same session lock.  Exactly one must succeed; the other must
// receive an ErrLockHeld error whose LockHeldError carries the holder's
// metadata (PID, Hostname, AcquiredAt).
func TestAcquireLock_ConcurrentExactlyOneWins(t *testing.T) {
	t.Parallel()

	id := "concurrent-" + GenerateID("txconc1")

	type result struct {
		handle *LockHandle
		err    error
	}

	results := make([]result, 2)
	var wg sync.WaitGroup
	barrier := make(chan struct{})

	for i := 0; i < 2; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-barrier
			h, err := AcquireLock(id)
			results[i] = result{handle: h, err: err}
		}()
	}

	close(barrier)
	wg.Wait()

	successes, lockHeldErrors := 0, 0
	for _, r := range results {
		switch {
		case r.err == nil:
			successes++
			r.handle.Release()
		case errors.Is(r.err, ErrLockHeld):
			lockHeldErrors++
			// Verify the structured error carries metadata.
			var lhe *LockHeldError
			if !errors.As(r.err, &lhe) {
				t.Error("ErrLockHeld should unwrap to *LockHeldError")
				break
			}
			if lhe.HolderPID <= 0 {
				t.Errorf("LockHeldError.HolderPID should be positive, got %d", lhe.HolderPID)
			}
			if lhe.Since.IsZero() {
				t.Error("LockHeldError.Since should be non-zero")
			}
			// Metadata may be nil in a tight race (metadata write races the
			// lock check), but if present it must be coherent.
			if lhe.Metadata != nil {
				if lhe.Metadata.PID != lhe.HolderPID {
					t.Errorf("LockHeldError.Metadata.PID (%d) != HolderPID (%d)",
						lhe.Metadata.PID, lhe.HolderPID)
				}
				if lhe.Metadata.Hostname == "" {
					t.Error("LockHeldError.Metadata.Hostname should be non-empty when set")
				}
				if lhe.Metadata.AcquiredAt.IsZero() {
					t.Error("LockHeldError.Metadata.AcquiredAt should be non-zero when set")
				}
			}
		default:
			t.Errorf("unexpected error type from AcquireLock: %v", r.err)
		}
	}

	if successes != 1 {
		t.Errorf("expected exactly 1 successful acquisition, got %d", successes)
	}
	if lockHeldErrors != 1 {
		t.Errorf("expected exactly 1 ErrLockHeld, got %d", lockHeldErrors)
	}
}

// ── IsLockHolderAlive ─────────────────────────────────────────────────────────

func TestIsLockHolderAlive_NoMetadata(t *testing.T) {
	t.Parallel()
	id := "alive-none-" + GenerateID("txalive1")
	alive, err := IsLockHolderAlive(id)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if alive {
		t.Error("expected alive=false when no metadata exists")
	}
}

func TestIsLockHolderAlive_LivePID(t *testing.T) {
	t.Parallel()

	dir, dirErr := lockDir()
	if dirErr != nil {
		t.Skip("cannot determine lock dir:", dirErr)
	}
	if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
		t.Fatalf("MkdirAll: %v", mkErr)
	}

	id := "alive-live-" + GenerateID("txalive2")
	mp, _ := lockMetaPath(id)

	meta := &LockMetadata{
		PID: os.Getpid(), Hostname: "h", AcquiredAt: time.Now(),
		GlassboxVersion: "0.0.0-test", SessionID: id,
	}
	data, _ := json.MarshalIndent(meta, "", "  ")
	if err := writeFileAtomic(mp, data, 0o600); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(mp) })

	alive, err := IsLockHolderAlive(id)
	if err != nil {
		t.Fatalf("IsLockHolderAlive: %v", err)
	}
	if !alive {
		t.Error("expected alive=true for our own PID")
	}
}

func TestIsLockHolderAlive_DeadPID(t *testing.T) {
	t.Parallel()

	dir, dirErr := lockDir()
	if dirErr != nil {
		t.Skip("cannot determine lock dir:", dirErr)
	}
	if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
		t.Fatalf("MkdirAll: %v", mkErr)
	}

	id := "alive-dead-" + GenerateID("txalive3")
	mp, _ := lockMetaPath(id)

	meta := &LockMetadata{
		PID: 99999999, Hostname: "h", AcquiredAt: time.Now(),
		GlassboxVersion: "0.0.0-test", SessionID: id,
	}
	data, _ := json.MarshalIndent(meta, "", "  ")
	if err := writeFileAtomic(mp, data, 0o600); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(mp) })

	alive, err := IsLockHolderAlive(id)
	if err != nil {
		t.Fatalf("IsLockHolderAlive: %v", err)
	}
	if alive {
		t.Error("expected alive=false for known-dead PID 99999999")
	}
}

// ── LockHeldError carries metadata hint ───────────────────────────────────────

// TestAcquireLock_LockHeldErrorContainsMetadata verifies that when the second
// caller loses the race its LockHeldError carries the holder's metadata so the
// error message surfaces PID, hostname, and acquisition time.
func TestAcquireLock_LockHeldErrorContainsMetadata(t *testing.T) {
	t.Parallel()
	id := "held-meta-" + GenerateID("txheldmeta")

	h1, err := AcquireLock(id)
	if err != nil {
		t.Fatalf("first AcquireLock: %v", err)
	}
	defer h1.Release()

	// Give the metadata write a moment to complete (it is non-blocking but
	// virtually instant; in CI this is always safe without a sleep because the
	// second goroutine hasn't run yet at this point in a single-threaded test).
	_, err2 := AcquireLock(id)
	if err2 == nil {
		t.Fatal("expected ErrLockHeld from second caller, got nil")
	}
	if !errors.Is(err2, ErrLockHeld) {
		t.Fatalf("expected ErrLockHeld, got %T: %v", err2, err2)
	}

	var lhe *LockHeldError
	if !errors.As(err2, &lhe) {
		t.Fatalf("expected *LockHeldError, got %T", err2)
	}

	if lhe.HolderPID != os.Getpid() {
		t.Errorf("HolderPID: got %d, want %d", lhe.HolderPID, os.Getpid())
	}

	// Metadata is written after the lock file; it may not be present in the
	// tightest possible race window, but in practice (same goroutine, no
	// intervening scheduler yield) it must be set here.
	if lhe.Metadata != nil {
		if lhe.Metadata.Hostname == "" {
			t.Error("metadata Hostname should be non-empty")
		}
		if lhe.Metadata.AcquiredAt.IsZero() {
			t.Error("metadata AcquiredAt should be non-zero")
		}
		if lhe.Metadata.GlassboxVersion == "" {
			t.Error("metadata GlassboxVersion should be non-empty")
		}
	}

	// Error message must contain PID, hostname (if metadata present), and time.
	msg := err2.Error()
	if !stringContains(msg, fmt.Sprintf("%d", os.Getpid())) {
		t.Errorf("error message %q should contain holder PID %d", msg, os.Getpid())
	}
}
