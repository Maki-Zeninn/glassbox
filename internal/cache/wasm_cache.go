// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/dotandev/glassbox/internal/logger"
)

// DefaultWASMCacheTTL is the TTL applied when NewWASMCache is called with a
// zero or negative ttl argument.
const DefaultWASMCacheTTL = 72 * time.Hour

// WASMEntry is one persisted cache record. It stores both the full CacheKey
// (for later auditing / diagnostics) and the analysis output as raw bytes.
type WASMEntry struct {
	// Key is the full structured key that produced this entry.
	Key CacheKey `json:"key"`

	// Digest is the pre-computed CacheKey.Digest() value used as the
	// on-disk filename. Stored here so readers can cross-check it.
	Digest string `json:"digest"`

	// Payload is the opaque output of the analysis pass: optimised WASM
	// bytes, a validation report JSON, a source-map JSON blob, etc.
	Payload []byte `json:"payload"`

	// PayloadHash is the SHA-256 hex digest of Payload, used to detect
	// on-disk corruption or partial writes.
	PayloadHash string `json:"payload_hash"`

	// CachedAt records when the entry was written.
	CachedAt time.Time `json:"cached_at"`

	// ExpiresAt is the wall-clock time after which the entry is treated as
	// stale and will be evicted on the next read or GC sweep. A zero time.Time
	// is interpreted as "already expired" and will cause immediate eviction.
	ExpiresAt time.Time `json:"expires_at"`
}

// WASMCache is a content-addressed, disk-backed cache for all WASM analysis
// passes (compilation, validation, source-map derivation, optimisation).
//
// Cache entries are keyed by CacheKey.Digest() so that any change to WASM
// content, tool version, configuration, or source-map inputs immediately
// produces a cache miss without any explicit invalidation call.
//
// Corrupt entries (truncated files, bad JSON, hash mismatch) are silently
// discarded and treated as misses; the caller re-runs the analysis and a
// fresh entry is written.
//
// Bounded size is enforced by the shared Manager / CleanLRU mechanism.
// WASMCache itself does not spawn background goroutines.
type WASMCache struct {
	manager *Manager
	ttl     time.Duration
	diag    *Diagnostics
	mu      sync.RWMutex // protects concurrent access to cache operations (Set, Get, Invalidate)
}

// NewWASMCache creates a WASMCache backed by manager with the given TTL.
// A zero or negative ttl uses DefaultWASMCacheTTL (72 hours).
// diag may be nil; if provided, all hits and misses are recorded.
func NewWASMCache(manager *Manager, ttl time.Duration, diag *Diagnostics) *WASMCache {
	if manager == nil {
		panic("cache: NewWASMCache called with nil manager")
	}
	if ttl < 0 {
		panic("cache: NewWASMCache called with negative TTL")
	}
	if ttl == 0 {
		ttl = DefaultWASMCacheTTL
	}
	return &WASMCache{
		manager: manager,
		ttl:     ttl,
		diag:    diag,
	}
}

// Get looks up an entry by its CacheKey. It returns (entry, true, nil) on a
// hit, (nil, false, nil) on a miss or stale entry, and (nil, false, err) only
// when an unexpected I/O error occurs (not when the file is simply absent).
//
// Corrupt entries are removed from disk and reported as misses — the caller
// should treat them identically to a normal miss.
func (w *WASMCache) Get(key CacheKey) (*WASMEntry, bool, error) {
	if err := key.Validate(); err != nil {
		return nil, false, fmt.Errorf("wasm cache get: invalid key: %w", err)
	}

	digest, err := key.Digest()
	if err != nil {
		return nil, false, fmt.Errorf("wasm cache get: could not compute digest: %w", err)
	}

	path, err := w.entryPath(key.Kind, digest)
	if err != nil {
		return nil, false, err
	}

	w.mu.RLock()
	raw, readErr := os.ReadFile(path)
	w.mu.RUnlock()

	if readErr != nil {
		if os.IsNotExist(readErr) {
			w.recordMiss(key.Kind)
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("wasm cache get: read error: %w", readErr)
	}

	entry, corrupt, parseErr := w.parseAndVerify(raw)
	if parseErr != nil || corrupt {
		// Corrupt entry — discard and report as miss.
		logger.Logger.Warn("Discarding corrupt WASM cache entry",
			"path", path, "kind", string(key.Kind), "error", parseErr)
		_ = os.Remove(path)
		w.recordMiss(key.Kind)
		if w.diag != nil {
			w.diag.RecordCorrupt(key.Kind)
		}
		return nil, false, nil
	}

	// Stale check.
	if time.Now().After(entry.ExpiresAt) {
		_ = os.Remove(path)
		w.recordMiss(key.Kind)
		return nil, false, nil
	}

	// Verify the on-disk digest matches the requested key to guard against
	// hash collisions or accidental cross-kind contamination.
	if entry.Digest != digest {
		logger.Logger.Warn("WASM cache digest mismatch — discarding",
			"want", digest, "got", entry.Digest)
		_ = os.Remove(path)
		w.recordMiss(key.Kind)
		if w.diag != nil {
			w.diag.RecordCorrupt(key.Kind)
		}
		return nil, false, nil
	}

	w.recordHit(key.Kind)
	return entry, true, nil
}

// Set writes payload into the cache under key. Any previous entry for the
// same key is atomically replaced. Set is safe to call concurrently.
func (w *WASMCache) Set(key CacheKey, payload []byte) error {
	if err := key.Validate(); err != nil {
		return fmt.Errorf("wasm cache set: invalid key: %w", err)
	}
	if len(payload) == 0 {
		return fmt.Errorf("wasm cache set: payload must not be empty")
	}

	digest, err := key.Digest()
	if err != nil {
		return fmt.Errorf("wasm cache set: could not compute digest: %w", err)
	}

	payloadHash := HashWASMContent(payload)

	entry := WASMEntry{
		Key:         key,
		Digest:      digest,
		Payload:     payload,
		PayloadHash: payloadHash,
		CachedAt:    time.Now(),
		ExpiresAt:   time.Now().Add(w.ttl),
	}

	raw, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("wasm cache set: marshal error: %w", err)
	}

	path, err := w.entryPath(key.Kind, digest)
	if err != nil {
		return err
	}

	// Ensure directory exists.
	if mkErr := os.MkdirAll(filepath.Dir(path), 0755); mkErr != nil {
		return fmt.Errorf("wasm cache set: mkdir error: %w", mkErr)
	}

	// Atomic write: write to a temp file then rename.
	tmp := path + ".tmp"
	w.mu.Lock()
	writeErr := os.WriteFile(tmp, raw, 0600)
	if writeErr == nil {
		writeErr = os.Rename(tmp, path)
	}
	w.mu.Unlock()

	if writeErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("wasm cache set: write error: %w", writeErr)
	}

	logger.Logger.Debug("WASM cache entry written",
		"kind", string(key.Kind),
		"digest", digest[:12]+"…",
		"payload_bytes", len(payload))

	return nil
}

// Invalidate removes the entry for key from disk. It is a no-op when the
// entry does not exist.
func (w *WASMCache) Invalidate(key CacheKey) error {
	if err := key.Validate(); err != nil {
		return fmt.Errorf("wasm cache invalidate: invalid key: %w", err)
	}
	digest, err := key.Digest()
	if err != nil {
		return fmt.Errorf("wasm cache invalidate: %w", err)
	}
	path, err := w.entryPath(key.Kind, digest)
	if err != nil {
		return err
	}
	if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
		return fmt.Errorf("wasm cache invalidate: %w", removeErr)
	}
	return nil
}

// InvalidateKind removes all entries of a particular WASMCacheKind.
func (w *WASMCache) InvalidateKind(kind WASMCacheKind) (int, error) {
	dir, err := w.kindDir(kind)
	if err != nil {
		return 0, err
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return 0, nil
		}
		return 0, fmt.Errorf("wasm cache invalidate-kind: %w", readErr)
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if rmErr := os.Remove(filepath.Join(dir, e.Name())); rmErr == nil {
			count++
		}
	}
	return count, nil
}

// PurgeExpired walks all kind directories and removes expired entries.
// Returns the number of entries removed.
func (w *WASMCache) PurgeExpired() (int, error) {
	baseDir, err := w.manager.GetCacheDir()
	if err != nil {
		return 0, err
	}
	wasmRoot := filepath.Join(baseDir, "wasm")

	count := 0
	now := time.Now()

	walkErr := filepath.WalkDir(wasmRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}

		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}

		entry, corrupt, parseErr := w.parseAndVerify(raw)
		if parseErr != nil || corrupt || now.After(entry.ExpiresAt) {
			if rmErr := os.Remove(path); rmErr == nil {
				count++
			}
		}
		return nil
	})

	return count, walkErr
}

// Stats returns a snapshot of hit/miss counts from the attached Diagnostics.
// Returns zero values when no Diagnostics instance was provided.
func (w *WASMCache) Stats() DiagnosticsSnapshot {
	if w.diag == nil {
		return DiagnosticsSnapshot{}
	}
	return w.diag.Snapshot()
}

// ── internal helpers ──────────────────────────────────────────────────────────

func (w *WASMCache) kindDir(kind WASMCacheKind) (string, error) {
	baseDir, err := w.manager.GetCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(baseDir, "wasm", string(kind)), nil
}

func (w *WASMCache) entryPath(kind WASMCacheKind, digest string) (string, error) {
	dir, err := w.kindDir(kind)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, digest+".json"), nil
}

// parseAndVerify unmarshals raw JSON into a WASMEntry and validates the
// payload integrity hash. Returns (nil, true, err) when the entry is corrupt.
func (w *WASMCache) parseAndVerify(raw []byte) (*WASMEntry, bool, error) {
	var entry WASMEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return nil, true, fmt.Errorf("json unmarshal: %w", err)
	}

	// Schema version check — reject entries written by future schema versions.
	if entry.Key.SchemaVersion != CacheKeyVersion {
		return nil, true, fmt.Errorf("schema version mismatch: entry=%d current=%d",
			entry.Key.SchemaVersion, CacheKeyVersion)
	}

	// Payload integrity check.
	if len(entry.Payload) == 0 {
		return nil, true, fmt.Errorf("payload is empty")
	}
	actualHash := HashWASMContent(entry.Payload)
	if actualHash != entry.PayloadHash {
		return nil, true, fmt.Errorf("payload hash mismatch: want %s got %s",
			entry.PayloadHash, actualHash)
	}

	return &entry, false, nil
}

func (w *WASMCache) recordHit(kind WASMCacheKind) {
	if w.diag != nil {
		w.diag.RecordHit(kind)
	}
}

func (w *WASMCache) recordMiss(kind WASMCacheKind) {
	if w.diag != nil {
		w.diag.RecordMiss(kind)
	}
}
