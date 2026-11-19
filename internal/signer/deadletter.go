// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package signer — dead-letter queue for KMS signing failures.
//
// When all KMS retry attempts are exhausted and signing fails, the unsigned
// payload and the full per-attempt audit trail are written atomically to a
// dead-letter file in ~/.Glassbox/dead-letter/kms/.  Operators can later
// inspect these files, resolve the KMS issue, and re-sign them using the
// `glassbox audit:redeliver` command.
//
// File layout
//
//	~/.Glassbox/dead-letter/kms/<session-id>-<unix-nano>.json
//
// Format
//
// Each dead-letter file is a self-contained JSON document matching the
// DeadLetterEntry struct.  The status field is always "unsigned" so
// automated tooling can distinguish un-signed payloads from signed ones.
// The signing_attempts array mirrors the format used in SignedAuditLog so
// the same audit tooling works for both.
//
// Atomicity
//
// Files are written via a temp-file-then-rename pattern (identical to the
// one used in internal/audit/atomic.go) so partial writes never appear as
// valid dead-letter entries.
package signer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// DeadLetterEntry is the JSON structure of a dead-letter file.  It mirrors
// SignedAuditLog in shape so the same audit tooling can read both, but
// instead of Signature/PublicKey it carries Status:"unsigned" and the raw
// unsigned payload together with the full signing attempt history.
type DeadLetterEntry struct {
	// Status is always "unsigned" for dead-letter entries.
	Status string `json:"status"`
	// SessionID identifies the signing session that failed.  Taken from the
	// correlation_id passed to SignWithMetadata when present.
	SessionID string `json:"session_id,omitempty"`
	// IdempotencyKey is the cache key computed by the signer for this payload
	// (safe to store — it is a SHA-256-derived key, not the raw payload bytes).
	IdempotencyKey string `json:"idempotency_key"`
	// FailedAt is the wall-clock time at which the last retry was exhausted.
	FailedAt time.Time `json:"failed_at"`
	// Provider is the signing provider that was used (e.g. "aws-kms").
	Provider string `json:"provider,omitempty"`
	// ErrorCode is the final AWS error code (or "NetworkError", etc.) that
	// caused retry exhaustion.
	ErrorCode string `json:"error_code,omitempty"`
	// ErrorClass is the coarse category of the final error.
	ErrorClass string `json:"error_class,omitempty"`
	// SigningAttempts records every KMS API call made during the retry loop.
	// Included verbatim so a reviewer can reconstruct the full failure history.
	SigningAttempts []SigningAttempt `json:"signing_attempts"`
	// Payload is the raw unsigned JSON payload that was submitted for signing.
	// It is stored as a json.RawMessage so it is embedded as-is without
	// double-encoding.
	Payload json.RawMessage `json:"payload"`
}

// deadLetterDir returns the path to the dead-letter directory for KMS failures.
func deadLetterDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("dead-letter: home directory unavailable: %w", err)
	}
	return filepath.Join(home, ".Glassbox", "dead-letter", "kms"), nil
}

// DeadLetterWriter writes unsigned payloads to the dead-letter queue when
// KMS signing fails after all retries are exhausted.
type DeadLetterWriter struct {
	// dir is the directory into which dead-letter files are written.
	// Defaults to ~/.Glassbox/dead-letter/kms when empty.
	dir string
	// now returns the current time; injectable for tests.
	now func() time.Time
}

// NewDeadLetterWriter creates a DeadLetterWriter that uses the default
// ~/.Glassbox/dead-letter/kms directory.
func NewDeadLetterWriter() *DeadLetterWriter {
	return &DeadLetterWriter{now: time.Now}
}

// newDeadLetterWriterAt creates a DeadLetterWriter targeting a specific
// directory.  Used by tests to avoid touching ~/.Glassbox.
func newDeadLetterWriterAt(dir string) *DeadLetterWriter {
	return &DeadLetterWriter{dir: dir, now: time.Now}
}

// resolveDir returns the configured directory, falling back to the default.
func (w *DeadLetterWriter) resolveDir() (string, error) {
	if w.dir != "" {
		return w.dir, nil
	}
	return deadLetterDir()
}

// Write persists entry to a new dead-letter file atomically.
//
// The file name is <session-id>-<unix-nano>.json, or <unix-nano>.json when
// SessionID is empty.  MkdirAll is called on first use so callers do not
// need to pre-create the directory.
//
// The write is atomic: data is written to a temp file in the same directory,
// synced, then renamed into place.  A partial write can never be observed as
// a valid dead-letter file.
func (w *DeadLetterWriter) Write(entry DeadLetterEntry) error {
	dir, err := w.resolveDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("dead-letter: failed to create directory %q: %w", dir, err)
	}

	// Stamp FailedAt if the caller omitted it.
	if entry.FailedAt.IsZero() {
		entry.FailedAt = w.now()
	}
	// Always set Status to "unsigned".
	entry.Status = "unsigned"

	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return fmt.Errorf("dead-letter: failed to marshal entry: %w", err)
	}

	// Build the filename from the session ID and a nanosecond timestamp for
	// uniqueness.
	ts := w.now().UnixNano()
	var filename string
	if entry.SessionID != "" {
		filename = fmt.Sprintf("%s-%d.json", sanitizeFilename(entry.SessionID), ts)
	} else {
		filename = fmt.Sprintf("%d.json", ts)
	}
	dest := filepath.Join(dir, filename)

	return writeDeadLetterAtomic(dest, data)
}

// writeDeadLetterAtomic writes data to dest via a temp-file-then-rename
// pattern.  Mirrors the pattern used in internal/audit/atomic.go.
func writeDeadLetterAtomic(dest string, data []byte) error {
	dir := filepath.Dir(dest)
	base := filepath.Base(dest)

	tmp, err := os.CreateTemp(dir, base+".tmp-*")
	if err != nil {
		return fmt.Errorf("dead-letter: failed to create temp file: %w", err)
	}
	tmpName := tmp.Name()

	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}

	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("dead-letter: failed to write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("dead-letter: failed to sync temp file: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		// Non-fatal: best-effort permission hardening.
		_ = err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("dead-letter: failed to close temp file: %w", err)
	}
	if err := os.Rename(tmpName, dest); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("dead-letter: failed to rename into place: %w", err)
	}
	return nil
}

// sanitizeFilename replaces characters that are unsafe in filenames with
// underscores so session IDs (which may contain slashes or colons) can
// be used in file names.
func sanitizeFilename(s string) string {
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '/' || c == '\\' || c == ':' || c == '*' || c == '?' ||
			c == '"' || c == '<' || c == '>' || c == '|' || c == '\x00' {
			out[i] = '_'
		} else {
			out[i] = c
		}
	}
	return string(out)
}

// ReadDeadLetterDir returns all dead-letter entries from dir, sorted by
// FailedAt ascending (oldest first) so redeliver processes them in order.
// Files that cannot be parsed are skipped with a warning logged to stderr.
func ReadDeadLetterDir(dir string) ([]DeadLetterFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("dead-letter: failed to read directory %q: %w", dir, err)
	}

	var files []DeadLetterFile
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		p := filepath.Join(dir, e.Name())
		raw, readErr := os.ReadFile(p)
		if readErr != nil {
			_, _ = fmt.Fprintf(os.Stderr, "dead-letter: skipping unreadable file %q: %v\n", p, readErr)
			continue
		}
		var entry DeadLetterEntry
		if parseErr := json.Unmarshal(raw, &entry); parseErr != nil {
			_, _ = fmt.Fprintf(os.Stderr, "dead-letter: skipping malformed file %q: %v\n", p, parseErr)
			continue
		}
		if entry.Status != "unsigned" {
			// Skip any file that has already been re-signed or is not ours.
			continue
		}
		files = append(files, DeadLetterFile{Path: p, Entry: entry})
	}

	// Sort oldest-first by FailedAt.
	sortDeadLetterFiles(files)
	return files, nil
}

// DeadLetterFile pairs a dead-letter entry with the file path it was read from.
type DeadLetterFile struct {
	Path  string
	Entry DeadLetterEntry
}

// sortDeadLetterFiles sorts files by FailedAt ascending (insertion sort is
// fine here — the number of dead-letter files is expected to be tiny).
func sortDeadLetterFiles(files []DeadLetterFile) {
	for i := 1; i < len(files); i++ {
		for j := i; j > 0 && files[j].Entry.FailedAt.Before(files[j-1].Entry.FailedAt); j-- {
			files[j], files[j-1] = files[j-1], files[j]
		}
	}
}

// DefaultDeadLetterDir returns the default dead-letter directory path.
// Exported so CLI commands can display it in help text.
func DefaultDeadLetterDir() (string, error) {
	return deadLetterDir()
}
