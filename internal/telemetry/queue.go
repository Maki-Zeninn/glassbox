// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package telemetry — queue.go implements a bounded offline telemetry queue.
//
// When the OTLP collector is unreachable, events are buffered to a local
// NDJSON file at ~/.Glassbox/telemetry_queue.ndjson.  The queue enforces:
//
//   - MaxQueueSize (500 events)   — oldest events are dropped head-first
//   - MaxQueueBytes (512 KiB)     — oldest events are dropped until under limit
//   - MaxEventAge  (48 h)         — events older than the TTL are removed on append
//
// Every append is atomic: the full file is rewritten to a sibling temp file,
// then renamed over the destination so a crash during write leaves the previous
// state intact.
//
// All attribute values are redacted via SanitizeValue before being stored so
// the queue never persists paths, raw hashes, or long free-form strings.
//
// Drop counters are in-memory (sync/atomic) and are NOT persisted; they reset
// on process restart.  They are exposed via QueueStats for doctor diagnostics.
//
// The queue contains NO networking code.  It is a pure disk buffer; draining
// to the collector is the responsibility of a separate component.
package telemetry

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// ── Constants ────────────────────────────────────────────────────────────────

const (
	// MaxQueueSize is the maximum number of events retained in the queue.
	// Oldest events are dropped head-first when this limit is exceeded.
	MaxQueueSize = 500

	// MaxQueueBytes is the maximum on-disk size of the queue file in bytes.
	// Oldest events are dropped head-first when this limit is exceeded.
	MaxQueueBytes = 512 * 1024 // 512 KiB

	// MaxEventAge is the maximum age of an event before it is evicted.
	MaxEventAge = 48 * time.Hour

	// queueFileName is the basename of the NDJSON queue file inside ~/.Glassbox.
	queueFileName = "telemetry_queue.ndjson"

	// queueFilePerms is the permission mask for the queue file and its directory.
	queueFilePerms = 0600
	queueDirPerms  = 0700
)

// ── Types ────────────────────────────────────────────────────────────────────

// QueueEntry is one line in the NDJSON file.
type QueueEntry struct {
	// Event is the sanitized command or event name.
	Event string `json:"event"`
	// Ts is the RFC3339 UTC timestamp at which the event was enqueued.
	Ts string `json:"ts"`
	// Attrs holds redacted key-value attributes.
	Attrs map[string]string `json:"attrs,omitempty"`
}

// QueueStats is a point-in-time snapshot of queue health, safe to display in
// doctor output without triggering any I/O beyond a single file stat+read.
type QueueStats struct {
	// EventCount is the number of events currently in the queue.
	EventCount int
	// OldestEventAge is the age of the oldest retained event, or 0 if empty.
	OldestEventAge time.Duration
	// Bytes is the current on-disk size of the queue file.
	Bytes int64
	// DroppedBySize is the number of events dropped due to size/count limits
	// since the current process started.
	DroppedBySize int64
	// DroppedByAge is the number of events dropped due to TTL expiry since
	// the current process started.
	DroppedByAge int64
}

// ── Process-lifetime drop counters ───────────────────────────────────────────

var (
	queueDroppedBySize atomic.Int64
	queueDroppedByAge  atomic.Int64
)

// DroppedBySize returns the number of events dropped due to size/count limits
// since the current process started.
func DroppedBySize() int64 { return queueDroppedBySize.Load() }

// DroppedByAge returns the number of events dropped due to TTL expiry since
// the current process started.
func DroppedByAge() int64 { return queueDroppedByAge.Load() }

// ── File path ────────────────────────────────────────────────────────────────

// queueFilePath returns the canonical path of the queue file.
func queueFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".Glassbox", queueFileName), nil
}

// QueueFilePath returns the queue file path for display / diagnostic purposes.
// Returns an empty string if the home directory cannot be determined.
func QueueFilePath() string {
	p, _ := queueFilePath()
	return p
}

// ── Write mutex ──────────────────────────────────────────────────────────────

// queueMu serialises all writes to the queue file within this process.
// Cross-process exclusion relies on atomic rename; a concurrent writer that
// loses the race simply overwrites with its own consistent snapshot.
var queueMu sync.Mutex

// ── Public API ───────────────────────────────────────────────────────────────

// EnqueueEvent appends a redacted event to the offline queue, enforcing age
// eviction and size/count limits.  The call is safe to make from multiple
// goroutines; it never blocks the caller for more than the time needed for a
// single small file rewrite, and it never performs network I/O.
//
// Attributes are redacted via SanitizeValue before storage.
//
// EnqueueEvent returns nil on success.  Errors are informational — a failure
// to write the queue file (e.g. read-only FS) is not fatal for the caller.
func EnqueueEvent(event string, attrs map[string]string) error {
	queueMu.Lock()
	defer queueMu.Unlock()

	path, err := queueFilePath()
	if err != nil {
		return err
	}

	// 1. Load existing entries.
	entries, err := loadEntries(path)
	if err != nil {
		// If we can't read the file start fresh rather than failing silently
		// and leaking stale data.
		entries = nil
	}

	// 2. Evict stale entries (age-first, cheaper than size-first).
	entries, aged := evictByAge(entries, time.Now().UTC())
	queueDroppedByAge.Add(int64(aged))

	// 3. Build the new entry with redacted attrs.
	newEntry := QueueEntry{
		Event: sanitizeCommandName(event),
		Ts:    time.Now().UTC().Format(time.RFC3339),
		Attrs: redactAttrs(attrs),
	}
	entries = append(entries, newEntry)

	// 4. Enforce size/count limits — drop from head (oldest first).
	entries, dropped := evictBySize(entries)
	queueDroppedBySize.Add(int64(dropped))

	// 5. Atomic write.
	return atomicWrite(path, entries)
}

// ReadQueue reads and returns all entries currently in the queue.  It does NOT
// modify the queue file.  Returns nil, nil when the file does not exist.
func ReadQueue() ([]QueueEntry, error) {
	path, err := queueFilePath()
	if err != nil {
		return nil, err
	}
	return loadEntries(path)
}

// GetQueueStats returns a point-in-time snapshot of queue health without
// modifying the queue.
func GetQueueStats() QueueStats {
	path, _ := queueFilePath()

	entries, _ := loadEntries(path)

	stats := QueueStats{
		EventCount:    len(entries),
		DroppedBySize: queueDroppedBySize.Load(),
		DroppedByAge:  queueDroppedByAge.Load(),
	}

	if info, err := os.Stat(path); err == nil {
		stats.Bytes = info.Size()
	}

	if len(entries) > 0 {
		t, err := time.Parse(time.RFC3339, entries[0].Ts)
		if err == nil {
			stats.OldestEventAge = time.Since(t)
		}
	}

	return stats
}

// PurgeQueue removes the queue file entirely.  Used by tests and doctor --fix.
func PurgeQueue() error {
	queueMu.Lock()
	defer queueMu.Unlock()

	path, err := queueFilePath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("purge queue: %w", err)
	}
	return nil
}

// ── Internal helpers ─────────────────────────────────────────────────────────

// loadEntries reads all NDJSON lines from path.  A missing file returns nil, nil.
func loadEntries(path string) ([]QueueEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read queue: %w", err)
	}

	var entries []QueueEntry
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var e QueueEntry
		if err := json.Unmarshal(line, &e); err != nil {
			// Skip malformed lines rather than aborting — a corrupt entry
			// should not block future writes.
			continue
		}
		entries = append(entries, e)
	}
	return entries, scanner.Err()
}

// evictByAge removes entries whose Ts is older than now-MaxEventAge.
// Returns the surviving entries and the count that were removed.
func evictByAge(entries []QueueEntry, now time.Time) ([]QueueEntry, int) {
	cutoff := now.Add(-MaxEventAge)
	kept := entries[:0]
	dropped := 0
	for _, e := range entries {
		t, err := time.Parse(time.RFC3339, e.Ts)
		if err != nil || t.Before(cutoff) {
			dropped++
			continue
		}
		kept = append(kept, e)
	}
	return kept, dropped
}

// evictBySize drops oldest entries from the head until len(entries) ≤
// MaxQueueSize and the serialized byte size ≤ MaxQueueBytes.
// Returns the surviving entries and the drop count.
func evictBySize(entries []QueueEntry) ([]QueueEntry, int) {
	dropped := 0

	// Count-based eviction.
	if len(entries) > MaxQueueSize {
		excess := len(entries) - MaxQueueSize
		dropped += excess
		entries = entries[excess:]
	}

	// Byte-based eviction: estimate size and trim from head until within limit.
	for len(entries) > 0 {
		size := estimateBytes(entries)
		if size <= MaxQueueBytes {
			break
		}
		entries = entries[1:]
		dropped++
	}

	return entries, dropped
}

// estimateBytes approximates the NDJSON byte count for the given entries.
func estimateBytes(entries []QueueEntry) int {
	total := 0
	for _, e := range entries {
		b, err := json.Marshal(e)
		if err == nil {
			total += len(b) + 1 // +1 for newline
		}
	}
	return total
}

// atomicWrite serialises entries to NDJSON and writes them atomically via a
// sibling temp file + rename.  The parent directory is created if absent.
func atomicWrite(path string, entries []QueueEntry) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, queueDirPerms); err != nil {
		return fmt.Errorf("create queue dir: %w", err)
	}

	var buf bytes.Buffer
	for _, e := range entries {
		line, err := json.Marshal(e)
		if err != nil {
			continue // skip unmarshalable entry rather than failing
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}

	// Write to a temp file in the same directory so rename is atomic on POSIX.
	tmp, err := os.CreateTemp(dir, ".telemetry_queue_*.ndjson")
	if err != nil {
		return fmt.Errorf("create temp queue file: %w", err)
	}
	tmpName := tmp.Name()

	// Best-effort cleanup if we fail before rename.
	success := false
	defer func() {
		if !success {
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(queueFilePerms); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp queue file: %w", err)
	}

	if _, err := tmp.Write(buf.Bytes()); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp queue file: %w", err)
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp queue file: %w", err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename queue file: %w", err)
	}

	success = true
	return nil
}

// redactAttrs returns a copy of attrs with all values passed through
// SanitizeValue so no raw paths, hashes, or long strings are stored.
// Values that are already the policy redaction placeholder are preserved as-is.
func redactAttrs(attrs map[string]string) map[string]string {
	if len(attrs) == 0 {
		return nil
	}
	out := make(map[string]string, len(attrs))
	for k, v := range attrs {
		// Preserve values already redacted by the policy layer so that
		// [redacted] placeholders are not re-processed by SanitizeValue.
		if v == "[redacted]" {
			out[k] = v
		} else {
			out[k] = SanitizeValue(k, v)
		}
	}
	return out
}
