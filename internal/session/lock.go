// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package session provides the lock and optimistic-revision machinery used to
// prevent concurrent Glassbox processes from silently overwriting each other's
// session saves.
//
// # Concurrency policy
//
// Two processes that want to write the same session are subject to an
// optimistic revision check: every saved session carries a monotonically
// increasing Revision counter.  When a writer reads Revision N and then
// attempts to save, it supplies the revision it read.  If the on-disk revision
// is still N, the save succeeds and the counter becomes N+1.  If another
// process saved in the meantime (disk revision is N+k, k > 0) the write is
// rejected with ErrSessionConflict, which carries the current disk revision so
// callers can decide between a force overwrite (Store.SaveForce) or an abort.
//
// In addition to the optimistic check, each active write holds a lightweight
// advisory lock file in ~/.Glassbox/locks/<session-id>.lock.  The lock is not
// mandatory (advisory), so read-only operations like List and Load never
// acquire it and are not blocked.  The lock is identified by the writing
// process's PID so stale locks left by crashed processes can be detected and
// cleaned up by the next writer.
//
// # Lock metadata
//
// Alongside each .lock file, AcquireLock atomically writes a <session-id>.lock.meta
// JSON file containing richer diagnostic information: PID, hostname,
// acquired_at, glassbox_version, and session_id.  When a lock acquisition
// fails because a live process holds the lock, the metadata is read and
// included in the error hint so the developer knows exactly who holds the lock
// and when it was acquired.
//
// # Stale lock recovery
//
// RecoverStaleLock checks whether the PID in the metadata is still running.
// If the process is gone, it removes both the .lock file and the .lock.meta
// file and returns (true, nil).  AcquireLock calls RecoverStaleLock before
// surfacing a lock-conflict error, retrying the acquisition once after a
// successful recovery.
//
// # Network filesystems
//
// On network filesystems (NFS, SMB, CIFS) advisory file locks are typically
// not enforced by the kernel.  Glassbox falls back to optimistic revision
// checks in this case; the revision check is still effective as long as both
// writers use the same SQLite database file, because SQLite's WAL mode
// serialises concurrent writes at the database level.  See
// docs/session-locking.md for details and known limitations.
package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/dotandev/glassbox/internal/version"
)

// ErrSessionConflict is the sentinel returned when an optimistic revision
// check fails.  Use errors.Is to detect it; the wrapping ConflictError carries
// the session ID and both revision numbers for diagnostics.
var ErrSessionConflict = errors.New("session write conflict")

// ConflictError is returned by Save and SaveWithValidation when the on-disk
// revision is ahead of the revision the caller last read.  It implements
// error so it can be returned directly and identified via errors.Is with the
// ErrSessionConflict sentinel.
type ConflictError struct {
	// SessionID identifies the session that was being written.
	SessionID string
	// ExpectedRevision is the revision the caller supplied (last-read value).
	ExpectedRevision int64
	// ActualRevision is the on-disk revision at the time of the check.
	ActualRevision int64
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf(
		"session %q write conflict: expected revision %d but disk has revision %d — "+
			"another process saved this session while you were editing it",
		e.SessionID, e.ExpectedRevision, e.ActualRevision,
	)
}

func (e *ConflictError) Is(target error) bool {
	return target == ErrSessionConflict
}

// Unwrap exposes the sentinel so errors.Is(err, ErrSessionConflict) works.
func (e *ConflictError) Unwrap() error {
	return ErrSessionConflict
}

// ErrLockHeld is the sentinel returned when AcquireLock finds a live lock.
var ErrLockHeld = errors.New("session advisory lock is held by another process")

// LockHeldError carries detail about who holds the lock.
type LockHeldError struct {
	SessionID string
	HolderPID int
	Since     time.Time
	// Metadata contains the full lock metadata when available; may be nil
	// when the metadata file could not be read.
	Metadata *LockMetadata
}

func (e *LockHeldError) Error() string {
	if e.Metadata != nil {
		return fmt.Sprintf(
			"session %q is locked by process %d on host %q (since %s, glassbox %s) — "+
				"another Glassbox instance is currently saving this session",
			e.SessionID, e.HolderPID, e.Metadata.Hostname,
			e.Since.Format(time.RFC3339), e.Metadata.GlassboxVersion,
		)
	}
	return fmt.Sprintf(
		"session %q is locked by process %d (since %s) — "+
			"another Glassbox instance is currently saving this session",
		e.SessionID, e.HolderPID, e.Since.Format(time.RFC3339),
	)
}

func (e *LockHeldError) Is(target error) bool {
	return target == ErrLockHeld
}

// Unwrap exposes the sentinel so errors.Is(err, ErrLockHeld) works.
func (e *LockHeldError) Unwrap() error {
	return ErrLockHeld
}

// LockRecord is the JSON payload written into an advisory lock file.
type LockRecord struct {
	// PID is the OS process that holds the lock.
	PID int `json:"pid"`
	// SessionID is the session being written.
	SessionID string `json:"session_id"`
	// AcquiredAt is the wall-clock time the lock was taken.
	AcquiredAt time.Time `json:"acquired_at"`
}

// LockMetadata is the richer JSON payload written alongside the advisory lock
// file as <session-id>.lock.meta.  It contains all the information a developer
// needs to diagnose a lock-conflict: who holds it, from where, and when.
type LockMetadata struct {
	// PID is the OS process ID of the lock holder.
	PID int `json:"pid"`
	// Hostname is the machine name where the lock was acquired.
	Hostname string `json:"hostname"`
	// AcquiredAt is the wall-clock time the lock was taken.
	AcquiredAt time.Time `json:"acquired_at"`
	// GlassboxVersion is the version string of the holding binary.
	GlassboxVersion string `json:"glassbox_version"`
	// SessionID is the session that is being written.
	SessionID string `json:"session_id"`
}

// StaleLockAge is the minimum age a lock file must have before it is
// considered stale (the owning process has most likely crashed).  Any lock
// file older than this and whose PID is no longer alive is removed
// automatically by the next writer.
const StaleLockAge = 5 * time.Minute

// lockDir returns the path of the directory that holds advisory lock files.
func lockDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home directory unavailable: %w", err)
	}
	return filepath.Join(home, ".Glassbox", "locks"), nil
}

// lockPath returns the filesystem path for the advisory lock file of sessionID.
func lockPath(sessionID string) (string, error) {
	dir, err := lockDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, sessionID+".lock"), nil
}

// lockMetaPath returns the filesystem path for the lock metadata file of sessionID.
func lockMetaPath(sessionID string) (string, error) {
	dir, err := lockDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, sessionID+".lock.meta"), nil
}

// readLockRecord reads and unmarshals the lock record at path.
func readLockRecord(path string) (*LockRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rec LockRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("malformed lock file: %w", err)
	}
	return &rec, nil
}

// ReadLockMetadata reads and parses the lock metadata file for sessionID.
// Returns (nil, nil) when no metadata file exists.
func ReadLockMetadata(sessionID string) (*LockMetadata, error) {
	metaPath, err := lockMetaPath(sessionID)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(metaPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read lock metadata: %w", err)
	}
	var meta LockMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("malformed lock metadata file: %w", err)
	}
	return &meta, nil
}

// writeLockMetadata atomically writes a LockMetadata file for sessionID using
// a temp-file-then-rename pattern so readers never see a partial file.
func writeLockMetadata(sessionID string, meta *LockMetadata) error {
	metaPath, err := lockMetaPath(sessionID)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal lock metadata: %w", err)
	}
	if err := writeFileAtomic(metaPath, data, 0o600); err != nil {
		return fmt.Errorf("failed to write lock metadata: %w", err)
	}
	return nil
}

// isLockStale reports whether the lock record belongs to a dead process and
// the file is old enough that it is safe to steal.
func isLockStale(rec *LockRecord) bool {
	if rec.PID <= 0 {
		return true
	}
	if processAlive(rec.PID) {
		return false
	}
	// Process is gone; also require the file to be older than StaleLockAge so
	// we do not immediately steal a lock written by a process that is still
	// starting up or has just exited on the same clock tick.
	return time.Since(rec.AcquiredAt) >= StaleLockAge
}

// RecoverStaleLock checks whether the advisory lock for sessionID is stale
// (i.e. the PID recorded in the metadata file is no longer running) and, if
// so, removes both the .lock file and the .lock.meta file.
//
// Return values:
//
//   - (true, nil)  — stale lock detected and both files successfully removed.
//   - (false, nil) — the lock holder process is still alive; nothing removed.
//   - (false, err) — the metadata file could not be read or the PID check
//     failed; no files were modified.
func RecoverStaleLock(sessionID string) (bool, error) {
	meta, err := ReadLockMetadata(sessionID)
	if err != nil {
		return false, fmt.Errorf("RecoverStaleLock: could not read metadata for %q: %w", sessionID, err)
	}
	if meta == nil {
		// No metadata file — nothing to recover.
		return false, nil
	}

	if meta.PID <= 0 {
		// Invalid PID in metadata is treated as "process gone".
		return removeStaleLockFiles(sessionID)
	}

	if processAlive(meta.PID) {
		return false, nil
	}

	return removeStaleLockFiles(sessionID)
}

// removeStaleLockFiles removes the .lock and .lock.meta files for sessionID.
// Errors from the individual removals are silenced (best-effort) so that a
// partially-cleaned state does not block the caller.
func removeStaleLockFiles(sessionID string) (bool, error) {
	lp, err := lockPath(sessionID)
	if err != nil {
		return false, fmt.Errorf("removeStaleLockFiles: %w", err)
	}
	mp, err := lockMetaPath(sessionID)
	if err != nil {
		return false, fmt.Errorf("removeStaleLockFiles: %w", err)
	}
	_ = os.Remove(lp)
	_ = os.Remove(mp)
	return true, nil
}

// AcquireLock attempts to acquire the advisory lock for sessionID.
// It returns a non-nil *LockHandle on success.  If the lock is already held
// by a live process, it returns ErrLockHeld with full metadata in the error
// hint.  Stale locks from dead processes are recovered automatically: if a
// stale lock is detected, it is removed and the acquisition is retried once.
//
// On success, a LockMetadata file is written atomically alongside the lock
// file so future callers can identify the lock holder without acquiring the
// lock themselves.
//
// Callers must call LockHandle.Release when the write is complete.
func AcquireLock(sessionID string) (*LockHandle, error) {
	path, err := lockPath(sessionID)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create lock directory %q: %w", dir, err)
	}

	handle, acquireErr := tryAcquireLock(sessionID, path)
	if acquireErr == nil {
		return handle, nil
	}

	// On conflict, attempt a one-shot stale lock recovery before giving up.
	var lhe *LockHeldError
	if errors.As(acquireErr, &lhe) {
		recovered, recoverErr := RecoverStaleLock(sessionID)
		if recoverErr == nil && recovered {
			log.Printf("[INFO] glassbox: recovered stale advisory lock for session %q (previous holder PID %d)",
				sessionID, lhe.HolderPID)
			// Retry the acquisition once after recovery.
			handle, acquireErr = tryAcquireLock(sessionID, path)
			if acquireErr == nil {
				return handle, nil
			}
		}
	}

	return nil, acquireErr
}

// tryAcquireLock makes a single acquisition attempt.  It is called by
// AcquireLock both on the initial attempt and after a successful stale
// recovery.
func tryAcquireLock(sessionID, path string) (*LockHandle, error) {
	// Check for an existing lock and decide whether it is stale.
	if existing, readErr := readLockRecord(path); readErr == nil {
		if isLockStale(existing) {
			// Safe to remove: the owning process is gone and the file is old.
			_ = os.Remove(path)
			metaPath, _ := lockMetaPath(sessionID)
			_ = os.Remove(metaPath)
		} else {
			// Live lock — build a rich error with full metadata.
			meta, _ := ReadLockMetadata(sessionID)
			lhe := &LockHeldError{
				SessionID: sessionID,
				HolderPID: existing.PID,
				Since:     existing.AcquiredAt,
				Metadata:  meta,
			}
			return nil, lhe
		}
	}

	hostname, _ := os.Hostname()

	record := LockRecord{
		PID:        os.Getpid(),
		SessionID:  sessionID,
		AcquiredAt: time.Now(),
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal lock record: %w", err)
	}

	// writeFileAtomic guarantees that the lock file is complete or absent.
	if err := writeFileAtomic(path, data, 0o600); err != nil {
		return nil, fmt.Errorf("failed to write advisory lock: %w", err)
	}

	// Write the richer metadata file atomically after the lock file is in
	// place.  A failure here is non-fatal: the lock is still valid.
	meta := &LockMetadata{
		PID:             record.PID,
		Hostname:        hostname,
		AcquiredAt:      record.AcquiredAt,
		GlassboxVersion: version.Version,
		SessionID:       sessionID,
	}
	if metaErr := writeLockMetadata(sessionID, meta); metaErr != nil {
		// Best-effort: log but don't fail the acquisition.
		log.Printf("[WARN] glassbox: failed to write lock metadata for session %q: %v", sessionID, metaErr)
	}

	return &LockHandle{sessionID: sessionID, path: path}, nil
}

// LockHandle represents a held advisory lock.  Release must be called when
// the guarded write is complete, whether or not it succeeded.
type LockHandle struct {
	sessionID string
	path      string
}

// Release removes the advisory lock file and its companion metadata file.
// Calling Release on an already-released handle or when the lock files have
// been cleaned up is a no-op.
func (h *LockHandle) Release() {
	if h == nil || h.path == "" {
		return
	}
	_ = os.Remove(h.path)
	metaPath, err := lockMetaPath(h.sessionID)
	if err == nil {
		_ = os.Remove(metaPath)
	}
}

// IsLockHolderAlive reports whether the process recorded in the lock metadata
// for sessionID is currently running.  It returns (false, nil) when no
// metadata file exists (no lock held).  It is safe to call without holding the
// lock; it does not modify any files.
func IsLockHolderAlive(sessionID string) (alive bool, err error) {
	meta, err := ReadLockMetadata(sessionID)
	if err != nil {
		return false, err
	}
	if meta == nil {
		return false, nil
	}
	return processAlive(meta.PID), nil
}

// CleanStaleLocks removes advisory lock files in the lock directory whose
// owning process is dead and whose file age exceeds StaleLockAge.
// Companion .lock.meta files are removed alongside their .lock counterparts.
// Individual removal errors are silently ignored (best effort).
// Returns the number of lock files removed.
func CleanStaleLocks() (int, error) {
	dir, err := lockDir()
	if err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("failed to read lock directory: %w", err)
	}

	removed := 0
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".lock" {
			continue
		}
		p := filepath.Join(dir, e.Name())
		rec, readErr := readLockRecord(p)
		if readErr != nil {
			// Unreadable lock file — remove it if old enough.
			info, infoErr := e.Info()
			if infoErr == nil && time.Since(info.ModTime()) >= StaleLockAge {
				if os.Remove(p) == nil {
					removed++
					// Also attempt to remove companion meta file.
					metaP := p + ".meta"
					_ = os.Remove(metaP)
				}
			}
			continue
		}
		if isLockStale(rec) {
			if os.Remove(p) == nil {
				removed++
				metaP := p + ".meta"
				_ = os.Remove(metaP)
			}
		}
	}
	return removed, nil
}
