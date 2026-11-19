// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package signer

import (
	"sync"
	"time"
)

// idempotencyCache is a tiny TTL+LRU table keyed by an opaque string.
// It is internal to the signer package and is intentionally not
// exported: callers use it via KMSSigner.Sign/SignWithMetadata and do
// not need direct access.
//
// Concurrency model: all public methods are safe for concurrent use and
// hold the mutex for their entire critical section. Operations are
// O(1) expected (amortised) and never allocate beyond a single entry.
//
// Privacy: the cache stores signature bytes only — never messages,
// digests, or key ids. The lookup key does bind key id and a digest
// hash (which is a public value, the signature hash of an audit log)
// but never the plain message.
type idempotencyCache struct {
	mu         sync.Mutex
	entries    map[string]idempotencyEntry
	order      []string
	maxEntries int
	ttl        time.Duration
	// now is the time source. Tests inject a deterministic function to
	// verify expiry without sleeping; production uses time.Now.
	now func() time.Time
}

type idempotencyEntry struct {
	value     []byte
	expiresAt time.Time
}

// newIdempotencyCache builds a cache with the given capacity and TTL.
// A zero or negative TTL or capacity effectively disables caching at
// runtime even when the cache instance is non-nil; callers that want
// caching-off can pass nil instead.
func newIdempotencyCache(maxEntries int, ttl time.Duration) *idempotencyCache {
	return &idempotencyCache{
		entries:    make(map[string]idempotencyEntry),
		maxEntries: maxEntries,
		ttl:        ttl,
		now:        time.Now,
	}
}

// get returns a cached value and a hit flag. Expired entries are
// dropped on read so callers always receive fresh data within the TTL.
func (c *idempotencyCache) get(key string) ([]byte, bool) {
	if c == nil || c.ttl <= 0 || c.maxEntries <= 0 {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if c.now().After(e.expiresAt) {
		delete(c.entries, key)
		c.removeFromOrderLocked(key)
		return nil, false
	}
	c.touchOrderLocked(key)
	return e.value, true
}

// put inserts a value, evicts LRU entries when over capacity, and
// updates the recency order. No-op on nil cache or zero/negative TTL.
func (c *idempotencyCache) put(key string, value []byte) {
	if c == nil || c.ttl <= 0 || c.maxEntries <= 0 || key == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[key]; !exists && len(c.entries) >= c.maxEntries {
		// Evict the oldest entry — front of the recency order slice.
		if len(c.order) > 0 {
			victim := c.order[0]
			c.order = c.order[1:]
			delete(c.entries, victim)
		}
	}
	c.entries[key] = idempotencyEntry{value: cloneBytes(value), expiresAt: c.now().Add(c.ttl)}
	c.touchOrderLocked(key)
}

// size returns the current entry count for observability and tests.
func (c *idempotencyCache) size() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// touchOrderLocked moves key to the back of the recency order. Caller
// must hold c.mu.
func (c *idempotencyCache) touchOrderLocked(key string) {
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
	c.order = append(c.order, key)
}

// removeFromOrderLocked removes key from the recency order. Caller
// must hold c.mu.
func (c *idempotencyCache) removeFromOrderLocked(key string) {
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			return
		}
	}
}

// cloneBytes returns a copy of b so the cache owns its storage and the
// caller can safely reuse / overwrite the original buffer.
func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}
