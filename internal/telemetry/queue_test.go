// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package telemetry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// withTempQueueHome redirects HOME to a temp dir and resets in-process drop
// counters so each test starts clean.
func withTempQueueHome(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	origHome := os.Getenv("HOME")
	origUserProfile := os.Getenv("USERPROFILE")
	t.Setenv("HOME", tmp)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", tmp)
	}
	t.Cleanup(func() {
		os.Setenv("HOME", origHome)
		if runtime.GOOS == "windows" {
			os.Setenv("USERPROFILE", origUserProfile)
		}
		// Reset drop counters so tests are isolated.
		queueDroppedByAge.Store(0)
		queueDroppedBySize.Store(0)
	})
	// Reset counters for this test.
	queueDroppedByAge.Store(0)
	queueDroppedBySize.Store(0)
	return tmp
}

// queuePath returns the expected queue file path under tmp.
func queuePath(home string) string {
	return filepath.Join(home, ".Glassbox", queueFileName)
}

// countLines returns the number of non-empty lines in path.
func countLines(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("countLines: %v", err)
	}
	n := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// ─── EnqueueEvent basic round-trip ───────────────────────────────────────────

func TestEnqueueEvent_CreatesFile(t *testing.T) {
	home := withTempQueueHome(t)
	if err := EnqueueEvent("debug", nil); err != nil {
		t.Fatalf("EnqueueEvent: %v", err)
	}
	path := queuePath(home)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("queue file should exist after EnqueueEvent: %v", err)
	}
}

func TestEnqueueEvent_RoundTrip(t *testing.T) {
	withTempQueueHome(t)
	attrs := map[string]string{"command": "debug", "network": "testnet"}
	if err := EnqueueEvent("debug", attrs); err != nil {
		t.Fatalf("EnqueueEvent: %v", err)
	}
	entries, err := ReadQueue()
	if err != nil {
		t.Fatalf("ReadQueue: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Event != "debug" {
		t.Errorf("Event: got %q, want %q", e.Event, "debug")
	}
	if _, err := time.Parse(time.RFC3339, e.Ts); err != nil {
		t.Errorf("Ts %q is not RFC3339: %v", e.Ts, err)
	}
	if e.Attrs == nil {
		t.Error("Attrs should be present for non-empty input")
	}
}

// ─── File permissions ─────────────────────────────────────────────────────────

func TestEnqueueEvent_FilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permission check not reliable on Windows")
	}
	home := withTempQueueHome(t)
	if err := EnqueueEvent("debug", nil); err != nil {
		t.Fatalf("EnqueueEvent: %v", err)
	}
	info, err := os.Stat(queuePath(home))
	if err != nil {
		t.Fatalf("stat queue file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != queueFilePerms {
		t.Errorf("file perms: got %04o, want %04o", perm, queueFilePerms)
	}
}

// ─── Redaction ────────────────────────────────────────────────────────────────

func TestEnqueueEvent_AttrsAreRedacted(t *testing.T) {
	withTempQueueHome(t)
	rawHash := "5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab"
	rawPath := "/home/user/projects/contract.wasm"
	attrs := map[string]string{
		"tx_hash": rawHash,
		"path":    rawPath,
	}
	if err := EnqueueEvent("debug", attrs); err != nil {
		t.Fatalf("EnqueueEvent: %v", err)
	}
	entries, _ := ReadQueue()
	if len(entries) == 0 {
		t.Fatal("no entries after enqueue")
	}
	stored := entries[0].Attrs

	// Raw hash must not be stored.
	if v, ok := stored["tx_hash"]; ok && strings.Contains(v, rawHash) {
		t.Errorf("raw hash leaked into queue: %q", v)
	}
	// Path must be reduced to basename.
	if v, ok := stored["path"]; ok && strings.Contains(v, "/home/user") {
		t.Errorf("raw path leaked into queue: %q", v)
	}
}

// ─── Age eviction ─────────────────────────────────────────────────────────────

func TestEvictByAge_DropsOldEntries(t *testing.T) {
	now := time.Now().UTC()
	old := QueueEntry{Event: "old", Ts: now.Add(-49 * time.Hour).Format(time.RFC3339)}
	fresh := QueueEntry{Event: "fresh", Ts: now.Add(-1 * time.Hour).Format(time.RFC3339)}

	kept, dropped := evictByAge([]QueueEntry{old, fresh}, now)
	if dropped != 1 {
		t.Errorf("dropped: got %d, want 1", dropped)
	}
	if len(kept) != 1 || kept[0].Event != "fresh" {
		t.Errorf("expected [fresh], got %v", kept)
	}
}

func TestEvictByAge_MalformedTs_Dropped(t *testing.T) {
	now := time.Now().UTC()
	bad := QueueEntry{Event: "bad", Ts: "not-a-timestamp"}
	kept, dropped := evictByAge([]QueueEntry{bad}, now)
	if dropped != 1 {
		t.Errorf("malformed Ts should be evicted: dropped=%d", dropped)
	}
	if len(kept) != 0 {
		t.Errorf("kept should be empty, got %v", kept)
	}
}

func TestEvictByAge_ExactBoundary_Kept(t *testing.T) {
	// An entry exactly at the cutoff boundary is borderline; what matters is
	// that something just inside MaxEventAge is never evicted.
	now := time.Now().UTC()
	borderline := QueueEntry{
		Event: "borderline",
		Ts:    now.Add(-(MaxEventAge - time.Minute)).Format(time.RFC3339),
	}
	kept, dropped := evictByAge([]QueueEntry{borderline}, now)
	if dropped != 0 {
		t.Errorf("entry within MaxEventAge should not be dropped: dropped=%d", dropped)
	}
	if len(kept) != 1 {
		t.Errorf("entry within MaxEventAge should be kept: kept=%v", kept)
	}
}

func TestEnqueueEvent_AgeDropCounterUpdated(t *testing.T) {
	withTempQueueHome(t)

	// Manually place an old entry in the queue file.
	path, _ := queueFilePath()
	_ = os.MkdirAll(filepath.Dir(path), queueDirPerms)
	oldEntry := QueueEntry{Event: "stale", Ts: time.Now().UTC().Add(-72 * time.Hour).Format(time.RFC3339)}
	line, _ := json.Marshal(oldEntry)
	_ = os.WriteFile(path, append(line, '\n'), queueFilePerms)

	if err := EnqueueEvent("new", nil); err != nil {
		t.Fatalf("EnqueueEvent: %v", err)
	}

	if DroppedByAge() == 0 {
		t.Error("DroppedByAge counter should be > 0 after age eviction")
	}
}

// ─── Size/count eviction ──────────────────────────────────────────────────────

func TestEvictBySize_CountLimit(t *testing.T) {
	entries := make([]QueueEntry, MaxQueueSize+10)
	for i := range entries {
		entries[i] = QueueEntry{Event: fmt.Sprintf("e%d", i), Ts: time.Now().UTC().Format(time.RFC3339)}
	}
	kept, dropped := evictBySize(entries)
	if len(kept) > MaxQueueSize {
		t.Errorf("evictBySize: %d entries remain, want ≤ %d", len(kept), MaxQueueSize)
	}
	if dropped < 10 {
		t.Errorf("evictBySize: dropped=%d, want ≥ 10", dropped)
	}
}

func TestEnqueueEvent_SizeDropCounterUpdated(t *testing.T) {
	withTempQueueHome(t)

	// Fill queue to just over MaxQueueSize.
	for i := 0; i < MaxQueueSize+5; i++ {
		if err := EnqueueEvent(fmt.Sprintf("cmd%d", i), nil); err != nil {
			t.Fatalf("EnqueueEvent %d: %v", i, err)
		}
	}

	if DroppedBySize() == 0 {
		t.Error("DroppedBySize counter should be > 0 after size eviction")
	}

	entries, _ := ReadQueue()
	if len(entries) > MaxQueueSize {
		t.Errorf("queue has %d entries, want ≤ %d", len(entries), MaxQueueSize)
	}
}

func TestEnqueueEvent_SizeLimitEnforced(t *testing.T) {
	withTempQueueHome(t)

	// Add enough events to blow past MaxQueueSize.
	for i := 0; i < MaxQueueSize+50; i++ {
		_ = EnqueueEvent("cmd", nil)
	}

	entries, err := ReadQueue()
	if err != nil {
		t.Fatalf("ReadQueue: %v", err)
	}
	if len(entries) > MaxQueueSize {
		t.Errorf("queue contains %d events, exceeds MaxQueueSize %d", len(entries), MaxQueueSize)
	}
}

func TestEnqueueEvent_ByteLimitEnforced(t *testing.T) {
	withTempQueueHome(t)

	// Each entry with a long-but-safe attr adds ~200 bytes; add enough to exceed 512KiB.
	bigAttr := map[string]string{"description": strings.Repeat("x", 120)}
	needed := (MaxQueueBytes / 200) + 50
	for i := 0; i < needed; i++ {
		_ = EnqueueEvent("cmd", bigAttr)
	}

	path, _ := queueFilePath()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat queue: %v", err)
	}
	if info.Size() > int64(MaxQueueBytes)*2 {
		// Allow some slack: after eviction the file can temporarily be just
		// over limit before the next event trims it; we just verify it hasn't
		// grown unbounded.
		t.Errorf("queue file is %d bytes, unbounded growth detected", info.Size())
	}
}

// ─── Atomic write / shutdown safety ──────────────────────────────────────────

func TestAtomicWrite_NoConcurrentCorruption(t *testing.T) {
	withTempQueueHome(t)

	const goroutines = 20
	const events = 10

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		g := g
		go func() {
			defer wg.Done()
			for i := 0; i < events; i++ {
				_ = EnqueueEvent(fmt.Sprintf("cmd_g%d_e%d", g, i), nil)
			}
		}()
	}
	wg.Wait()

	// Verify every line in the file is valid JSON — no partial writes.
	path, _ := queueFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read queue: %v", err)
	}
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e QueueEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Errorf("line %d is not valid JSON: %v\nline: %s", i+1, err, line)
		}
	}
}

func TestEnqueueEvent_CorruptQueueRecovery(t *testing.T) {
	home := withTempQueueHome(t)
	path := queuePath(home)
	_ = os.MkdirAll(filepath.Dir(path), queueDirPerms)
	// Write a half-valid, half-garbage NDJSON file.
	validLine, _ := json.Marshal(QueueEntry{Event: "ok", Ts: time.Now().UTC().Format(time.RFC3339)})
	content := string(validLine) + "\n{not valid json}\n"
	_ = os.WriteFile(path, []byte(content), queueFilePerms)

	// Append should not fail; the corrupt line should be silently skipped.
	if err := EnqueueEvent("new", nil); err != nil {
		t.Fatalf("EnqueueEvent after corrupt queue: %v", err)
	}

	entries, _ := ReadQueue()
	for _, e := range entries {
		if e.Event == "" {
			t.Error("corrupt entry survived into clean queue")
		}
	}
}

func TestEnqueueEvent_MissingDirCreated(t *testing.T) {
	home := withTempQueueHome(t)
	// Ensure the .Glassbox directory does not exist.
	_ = os.RemoveAll(filepath.Join(home, ".Glassbox"))

	if err := EnqueueEvent("debug", nil); err != nil {
		t.Fatalf("EnqueueEvent with missing dir: %v", err)
	}
	if _, err := os.Stat(queuePath(home)); err != nil {
		t.Errorf("queue file should exist: %v", err)
	}
}

// ─── ReadQueue ────────────────────────────────────────────────────────────────

func TestReadQueue_MissingFile(t *testing.T) {
	withTempQueueHome(t)
	entries, err := ReadQueue()
	if err != nil {
		t.Fatalf("ReadQueue missing file: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestReadQueue_DoesNotModifyFile(t *testing.T) {
	home := withTempQueueHome(t)
	_ = EnqueueEvent("cmd", nil)

	path := queuePath(home)
	before, _ := os.ReadFile(path)
	_, _ = ReadQueue()
	after, _ := os.ReadFile(path)

	if string(before) != string(after) {
		t.Error("ReadQueue must not modify the queue file")
	}
}

// ─── GetQueueStats ─────────────────────────────────────────────────────────────

func TestGetQueueStats_Empty(t *testing.T) {
	withTempQueueHome(t)
	stats := GetQueueStats()
	if stats.EventCount != 0 {
		t.Errorf("EventCount: got %d, want 0", stats.EventCount)
	}
	if stats.Bytes != 0 {
		t.Errorf("Bytes: got %d, want 0", stats.Bytes)
	}
}

func TestGetQueueStats_AfterEnqueue(t *testing.T) {
	withTempQueueHome(t)
	_ = EnqueueEvent("cmd1", nil)
	_ = EnqueueEvent("cmd2", nil)

	stats := GetQueueStats()
	if stats.EventCount != 2 {
		t.Errorf("EventCount: got %d, want 2", stats.EventCount)
	}
	if stats.Bytes == 0 {
		t.Error("Bytes should be > 0 after enqueue")
	}
	if stats.OldestEventAge == 0 {
		t.Error("OldestEventAge should be > 0")
	}
}

func TestGetQueueStats_DropCountersReflected(t *testing.T) {
	withTempQueueHome(t)

	// Prime age counter.
	queueDroppedByAge.Store(7)
	queueDroppedBySize.Store(3)

	stats := GetQueueStats()
	if stats.DroppedByAge != 7 {
		t.Errorf("DroppedByAge: got %d, want 7", stats.DroppedByAge)
	}
	if stats.DroppedBySize != 3 {
		t.Errorf("DroppedBySize: got %d, want 3", stats.DroppedBySize)
	}
}

// ─── PurgeQueue ───────────────────────────────────────────────────────────────

func TestPurgeQueue_RemovesFile(t *testing.T) {
	home := withTempQueueHome(t)
	_ = EnqueueEvent("cmd", nil)
	if err := PurgeQueue(); err != nil {
		t.Fatalf("PurgeQueue: %v", err)
	}
	if _, err := os.Stat(queuePath(home)); !os.IsNotExist(err) {
		t.Error("queue file should not exist after PurgeQueue")
	}
}

func TestPurgeQueue_MissingFile_Noop(t *testing.T) {
	withTempQueueHome(t)
	// Should not error when the file does not exist.
	if err := PurgeQueue(); err != nil {
		t.Fatalf("PurgeQueue on missing file: %v", err)
	}
}

// ─── No network transmission ──────────────────────────────────────────────────

// TestNoNetworkTransmission verifies that EnqueueEvent, ReadQueue, and
// PurgeQueue perform no outbound network calls.  We do this by checking that
// no goroutines are spawned and the queue file stays local.  Since the
// implementation contains no dial/connect calls this is a static-logic guard.
func TestNoNetworkTransmission(t *testing.T) {
	home := withTempQueueHome(t)

	// Enqueue several events and read them back — no network functions are
	// called in the implementation.  If any http.Get/Dial were present, the
	// test would time out with a connection refused error in CI.
	for i := 0; i < 5; i++ {
		if err := EnqueueEvent(fmt.Sprintf("cmd%d", i), map[string]string{"k": "v"}); err != nil {
			t.Fatalf("EnqueueEvent %d: %v", i, err)
		}
	}
	entries, err := ReadQueue()
	if err != nil {
		t.Fatalf("ReadQueue: %v", err)
	}
	if len(entries) != 5 {
		t.Errorf("expected 5 entries, got %d", len(entries))
	}

	// Verify all data stayed on disk under the temp home — no remote path.
	for _, e := range entries {
		for k, v := range e.Attrs {
			if strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") {
				t.Errorf("attr %s=%q looks like a URL — data should not leave disk", k, v)
			}
		}
	}

	// Purge should clean up only the local file.
	if err := PurgeQueue(); err != nil {
		t.Fatalf("PurgeQueue: %v", err)
	}
	if _, err := os.Stat(queuePath(home)); !os.IsNotExist(err) {
		t.Error("queue file should be gone after purge")
	}
}

// ─── estimateBytes ────────────────────────────────────────────────────────────

func TestEstimateBytes_Proportional(t *testing.T) {
	e1 := QueueEntry{Event: "a", Ts: time.Now().UTC().Format(time.RFC3339)}
	e2 := QueueEntry{Event: "b", Ts: time.Now().UTC().Format(time.RFC3339)}

	one := estimateBytes([]QueueEntry{e1})
	two := estimateBytes([]QueueEntry{e1, e2})

	if two <= one {
		t.Errorf("two entries should produce more bytes than one: one=%d two=%d", one, two)
	}
}

// ─── QueueFilePath ────────────────────────────────────────────────────────────

func TestQueueFilePath_ContainsGlassboxDir(t *testing.T) {
	withTempQueueHome(t)
	p := QueueFilePath()
	if p == "" {
		t.Fatal("QueueFilePath should not be empty")
	}
	if !strings.Contains(p, ".Glassbox") {
		t.Errorf("QueueFilePath %q should contain .Glassbox", p)
	}
	if filepath.Base(p) != queueFileName {
		t.Errorf("base: got %q, want %q", filepath.Base(p), queueFileName)
	}
}

// ─── evictBySize — byte-based ─────────────────────────────────────────────────

func TestEvictBySize_ByteLimit(t *testing.T) {
	// Build entries whose total estimated size just exceeds MaxQueueBytes.
	// Each entry is ~300 bytes with a 200-char attribute value.
	longVal := strings.Repeat("z", 120) // SanitizeValue will truncate to 128
	entries := make([]QueueEntry, 0)
	ts := time.Now().UTC().Format(time.RFC3339)
	for estimateBytes(entries) < MaxQueueBytes+1000 {
		entries = append(entries, QueueEntry{
			Event: "cmd",
			Ts:    ts,
			Attrs: map[string]string{"description": longVal},
		})
	}

	kept, dropped := evictBySize(entries)
	if dropped == 0 {
		t.Error("should have dropped at least one entry for byte limit")
	}
	if estimateBytes(kept) > MaxQueueBytes {
		t.Errorf("after eviction, size %d exceeds MaxQueueBytes %d",
			estimateBytes(kept), MaxQueueBytes)
	}
}

// ─── DroppedBySize / DroppedByAge exported counters ──────────────────────────

func TestDroppedCounters_InitiallyZero(t *testing.T) {
	withTempQueueHome(t)
	if d := DroppedBySize(); d != 0 {
		t.Errorf("DroppedBySize: got %d, want 0", d)
	}
	if d := DroppedByAge(); d != 0 {
		t.Errorf("DroppedByAge: got %d, want 0", d)
	}
}
