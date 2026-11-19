// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package crashreport — dedup.go implements crash report deduplication using
// stable content fingerprints together with a local rate-limit window.
//
// # Fingerprinting
//
// A fingerprint is a short SHA-256 digest built from the error class and a
// normalised stack excerpt.  Volatile data is stripped before hashing so that
// the same logical crash produces the same fingerprint across machines and
// restarts:
//
//   - Absolute file paths are reduced to their basename.
//   - Memory addresses (0x…) are removed.
//   - Line numbers are removed from stack frames.
//   - Timestamps, PIDs, goroutine IDs, and random suffixes are removed.
//   - Command arguments (potential secrets) are never included.
//
// # Rate limiting
//
// A fingerprint is suppressed when it has been reported more than
// [MaxReportsPerFingerprint] times within [RateLimitWindow].  The counters
// are persisted to ~/.Glassbox/crash_suppress.json so suppression survives
// process restarts within the same window.
//
// The suppression file is never sent to any remote endpoint.  It contains
// only fingerprints (opaque hashes) and counts.
//
// # Observability
//
// [DedupStats] returns a snapshot of current suppression state for display
// by `glassbox doctor`.
package crashreport

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

// ── Constants ─────────────────────────────────────────────────────────────────

const (
	// MaxReportsPerFingerprint is the maximum number of times the same crash
	// fingerprint may be reported within RateLimitWindow.
	MaxReportsPerFingerprint = 3

	// RateLimitWindow is the rolling window for the per-fingerprint counter.
	RateLimitWindow = 24 * time.Hour

	// suppressFileName is the basename of the local suppression state file.
	suppressFileName = "crash_suppress.json"

	// suppressFilePerms are the permissions for the suppression file.
	suppressFilePerms = 0600
	suppressDirPerms  = 0700
)

// ── Types ─────────────────────────────────────────────────────────────────────

// suppressEntry is one record inside the suppression file.
type suppressEntry struct {
	// Count is the number of times this fingerprint has been seen in the window.
	Count int `json:"count"`
	// WindowStart is the RFC3339 UTC time at which the current window began.
	WindowStart string `json:"window_start"`
}

// suppressStore is the in-memory representation of crash_suppress.json.
type suppressStore struct {
	// Entries maps fingerprint → suppression entry.
	Entries map[string]suppressEntry `json:"entries"`
}

// DedupStats is a point-in-time snapshot of deduplication state.
type DedupStats struct {
	// SuppressedTotal is the number of reports suppressed so far this process.
	SuppressedTotal int64
	// UniqueFingerprints is the number of distinct fingerprints currently tracked.
	UniqueFingerprints int
	// ActiveSuppressed is the count of fingerprints currently rate-limited.
	ActiveSuppressed int
}

// ── Process-lifetime counters ─────────────────────────────────────────────────

var (
	dedupMu          sync.Mutex
	suppressedTotal  int64
)

// SuppressedTotal returns the number of crash reports suppressed due to
// deduplication since the current process started.
func SuppressedTotal() int64 {
	dedupMu.Lock()
	defer dedupMu.Unlock()
	return suppressedTotal
}

// ── File path ─────────────────────────────────────────────────────────────────

func suppressFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".Glassbox", suppressFileName), nil
}

// SuppressFilePath returns the suppression file path for diagnostics.
func SuppressFilePath() string {
	p, _ := suppressFilePath()
	return p
}

// ── Fingerprinting ────────────────────────────────────────────────────────────

var (
	// reAddr strips memory addresses like 0x7fff1234abcd.
	reAddr = regexp.MustCompile(`0x[0-9a-fA-F]+`)
	// rePath normalises path separators so cross-platform crashes match.
	// Processed by filepath.Base per frame, see normalizeStack.
	// reLineNum removes ":NNN" suffixes from file:line references.
	reLineNum = regexp.MustCompile(`:\d+`)
	// reGoroutine strips goroutine IDs and state annotations.
	reGoroutine = regexp.MustCompile(`(?m)^goroutine \d+ \[.*?\]:`)
	// reTimestamp removes common timestamp patterns.
	reTimestamp = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}`)
	// reArg removes function argument values like (0x..., 0x...).
	reArg = regexp.MustCompile(`\((?:[^)]*)\)`)
)

// NormalisedStack returns a cleaned, deterministic form of the raw stack
// trace suitable for fingerprinting.  All volatile data is stripped.
func NormalisedStack(raw string) string {
	s := raw

	// 1. Strip goroutine headers.
	s = reGoroutine.ReplaceAllString(s, "")
	// 2. Strip timestamps.
	s = reTimestamp.ReplaceAllString(s, "")
	// 3. Strip memory addresses.
	s = reAddr.ReplaceAllString(s, "")
	// 4. Strip function argument lists.
	s = reArg.ReplaceAllString(s, "()")
	// 5. Normalise file paths to basenames and remove line numbers.
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// File path lines contain a slash or backslash followed by ".go".
		if strings.Contains(trimmed, ".go") {
			// Extract basename.
			base := filepath.Base(trimmed)
			// Remove line numbers.
			base = reLineNum.ReplaceAllString(base, "")
			lines = append(lines, base)
		} else {
			lines = append(lines, trimmed)
		}
	}
	return strings.Join(lines, "\n")
}

// ErrorClass extracts the stable class of an error for fingerprinting.
// It strips any pointer suffixes, memory addresses, and path components from
// the error message, retaining only the static prefix up to the first colon or
// opening parenthesis.
func ErrorClass(errMsg string) string {
	if errMsg == "" {
		return "unknown"
	}
	// Redact memory addresses.
	s := reAddr.ReplaceAllString(errMsg, "")
	// Redact path separators (take only the base of any file path-like token).
	parts := strings.Fields(s)
	var cleaned []string
	for _, p := range parts {
		if strings.ContainsAny(p, "/\\") {
			cleaned = append(cleaned, filepath.Base(p))
		} else {
			cleaned = append(cleaned, p)
		}
	}
	s = strings.Join(cleaned, " ")
	// Truncate at the first colon (e.g. "open /home/user/file: no such file")
	// preserves the verb but drops the volatile path argument.
	if idx := strings.Index(s, ":"); idx > 0 {
		s = s[:idx]
	}
	// Hard cap to prevent absurdly long strings from becoming fingerprint inputs.
	if len(s) > 256 {
		s = s[:256]
	}
	return strings.TrimSpace(s)
}

// Fingerprint returns a short, stable, privacy-safe identifier for the given
// error message and raw stack trace.  The returned string is a 16-character
// hex prefix of a SHA-256 digest.
func Fingerprint(errMsg, rawStack string) string {
	class := ErrorClass(errMsg)
	normStack := NormalisedStack(rawStack)

	// Build the hash input from the normalised class and the first N frames of
	// the normalised stack to keep fingerprints stable across minor code churn.
	const maxStackLines = 10
	stackLines := strings.Split(normStack, "\n")
	if len(stackLines) > maxStackLines {
		stackLines = stackLines[:maxStackLines]
	}
	input := class + "\n" + strings.Join(stackLines, "\n")

	h := sha256.Sum256([]byte(input))
	return fmt.Sprintf("%x", h[:8]) // 16 hex chars from first 8 bytes
}

// FingerprintFromReport is a convenience wrapper that derives the fingerprint
// directly from a [Report].
func FingerprintFromReport(r Report) string {
	return Fingerprint(r.ErrorMessage, r.StackTrace)
}

// CurrentStackFingerprint generates a fingerprint from the current goroutine
// stack at the call site.  Useful for testing or for defensive captures where
// an error object is not available.
func CurrentStackFingerprint(errMsg string) string {
	return Fingerprint(errMsg, string(debug.Stack()))
}

// ── Suppression store ─────────────────────────────────────────────────────────

func loadSuppressStore(path string) suppressStore {
	data, err := os.ReadFile(path)
	if err != nil {
		return suppressStore{Entries: make(map[string]suppressEntry)}
	}
	var store suppressStore
	if err := json.Unmarshal(data, &store); err != nil {
		return suppressStore{Entries: make(map[string]suppressEntry)}
	}
	if store.Entries == nil {
		store.Entries = make(map[string]suppressEntry)
	}
	return store
}

func saveSuppressStore(path string, store suppressStore) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, suppressDirPerms); err != nil {
		return fmt.Errorf("create suppress dir: %w", err)
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal suppress store: %w", err)
	}
	// Atomic write.
	tmp, err := os.CreateTemp(dir, ".crash_suppress_*.json")
	if err != nil {
		return fmt.Errorf("create temp suppress file: %w", err)
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(suppressFilePerms); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename suppress file: %w", err)
	}
	ok = true
	return nil
}

// ── Public dedup API ──────────────────────────────────────────────────────────

// IsSuppressed returns true when the fingerprint derived from errMsg and
// rawStack has already been reported [MaxReportsPerFingerprint] times in the
// last [RateLimitWindow].  It increments the counter and persists it.
//
// If the suppression file cannot be read or written the call returns false
// (allow) so that a broken FS does not silently swallow all crash reports.
func IsSuppressed(errMsg, rawStack string) bool {
	fp := Fingerprint(errMsg, rawStack)
	return isFingerprintSuppressed(fp)
}

// IsSuppressedReport is a convenience wrapper for a [Report].
func IsSuppressedReport(r Report) bool {
	fp := FingerprintFromReport(r)
	return isFingerprintSuppressed(fp)
}

func isFingerprintSuppressed(fp string) bool {
	dedupMu.Lock()
	defer dedupMu.Unlock()

	path, err := suppressFilePath()
	if err != nil {
		return false
	}

	store := loadSuppressStore(path)
	now := time.Now().UTC()

	entry, exists := store.Entries[fp]
	if exists {
		// Check whether the window has expired.
		windowStart, perr := time.Parse(time.RFC3339, entry.WindowStart)
		if perr == nil && now.Sub(windowStart) < RateLimitWindow {
			// Still within the window.
			if entry.Count >= MaxReportsPerFingerprint {
				suppressedTotal++
				return true
			}
			entry.Count++
		} else {
			// Window expired — start fresh.
			entry = suppressEntry{
				Count:       1,
				WindowStart: now.Format(time.RFC3339),
			}
		}
	} else {
		entry = suppressEntry{
			Count:       1,
			WindowStart: now.Format(time.RFC3339),
		}
	}

	store.Entries[fp] = entry
	// Best-effort save — if it fails we continue.
	_ = saveSuppressStore(path, store)
	return false
}

// PurgeSuppressStore removes the suppression file entirely.  Used by tests
// and `glassbox doctor --fix`.
func PurgeSuppressStore() error {
	dedupMu.Lock()
	defer dedupMu.Unlock()

	path, err := suppressFilePath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("purge suppress store: %w", err)
	}
	return nil
}

// GetDedupStats returns a point-in-time snapshot of deduplication state.
func GetDedupStats() DedupStats {
	dedupMu.Lock()
	defer dedupMu.Unlock()

	path, _ := suppressFilePath()
	store := loadSuppressStore(path)

	now := time.Now().UTC()
	active := 0
	for _, e := range store.Entries {
		ws, err := time.Parse(time.RFC3339, e.WindowStart)
		if err != nil {
			continue
		}
		if now.Sub(ws) < RateLimitWindow && e.Count >= MaxReportsPerFingerprint {
			active++
		}
	}

	return DedupStats{
		SuppressedTotal:    suppressedTotal,
		UniqueFingerprints: len(store.Entries),
		ActiveSuppressed:   active,
	}
}
