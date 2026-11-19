// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package sourcemap

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// newTestSourceCache creates an isolated SourceCache backed by a temp directory.
func newTestSourceCache(t *testing.T) *SourceCache {
	t.Helper()
	dir := t.TempDir()
	sc, err := NewSourceCache(dir)
	require.NoError(t, err)
	return sc
}

// syntheticSource builds a minimal SourceCode stub for a given contract ID.
func syntheticSource(contractID string) *SourceCode {
	return &SourceCode{
		ContractID: contractID,
		Files:      map[string]string{"lib.rs": fmt.Sprintf("// contract %s", contractID)},
	}
}

// TestSourceCache_ConcurrentGetPut verifies that simultaneous Get and Put
// operations on a shared SourceCache do not race.
// Run with: go test -race ./internal/sourcemap/ -run TestSourceCache_ConcurrentGetPut
func TestSourceCache_ConcurrentGetPut(t *testing.T) {
	sc := newTestSourceCache(t)

	const goroutines = 20
	const iterations = 25

	var wg sync.WaitGroup
	wg.Add(goroutines * 2)

	// Concurrent writers
	for g := 0; g < goroutines; g++ {
		g := g
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				id := fmt.Sprintf("C%03d%03d", g, i)
				if err := sc.Put(syntheticSource(id)); err != nil {
					t.Errorf("Put(%s): unexpected error: %v", id, err)
				}
			}
		}()
	}

	// Concurrent readers (overlap with writers intentionally)
	for g := 0; g < goroutines; g++ {
		g := g
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				id := fmt.Sprintf("C%03d%03d", g, i)
				// A nil result (miss) is acceptable; only errors matter.
				_ = sc.Get(id)
			}
		}()
	}

	wg.Wait()
}

// TestSourceCache_ConcurrentInvalidation verifies that Invalidate calls
// during concurrent Gets do not cause a data race.
// Run with: go test -race ./internal/sourcemap/ -run TestSourceCache_ConcurrentInvalidation
func TestSourceCache_ConcurrentInvalidation(t *testing.T) {
	sc := newTestSourceCache(t)

	const contracts = 30

	// Pre-populate the cache.
	for i := 0; i < contracts; i++ {
		id := fmt.Sprintf("CINV%03d", i)
		require.NoError(t, sc.Put(syntheticSource(id)))
	}

	var wg sync.WaitGroup
	wg.Add(contracts * 2)

	// Concurrent invalidates
	for i := 0; i < contracts; i++ {
		i := i
		go func() {
			defer wg.Done()
			id := fmt.Sprintf("CINV%03d", i)
			if err := sc.Invalidate(id); err != nil {
				t.Errorf("Invalidate(%s): unexpected error: %v", id, err)
			}
		}()
	}

	// Concurrent reads during invalidation
	for i := 0; i < contracts; i++ {
		i := i
		go func() {
			defer wg.Done()
			_ = sc.Get(fmt.Sprintf("CINV%03d", i))
		}()
	}

	wg.Wait()
}

// TestSourceCache_ConcurrentClear exercises SourceCache.Clear while Get/Put
// operations are in progress to catch any lock ordering issues.
// Run with: go test -race ./internal/sourcemap/ -run TestSourceCache_ConcurrentClear
func TestSourceCache_ConcurrentClear(t *testing.T) {
	sc := newTestSourceCache(t)

	const goroutines = 10

	var wg sync.WaitGroup
	wg.Add(goroutines + 1)

	for g := 0; g < goroutines; g++ {
		g := g
		go func() {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				id := fmt.Sprintf("CCLEAR%d%d", g, i)
				_ = sc.Put(syntheticSource(id))
				_ = sc.Get(id)
			}
		}()
	}

	// Clear while the other goroutines are active.
	go func() {
		defer wg.Done()
		time.Sleep(time.Millisecond)
		if err := sc.Clear(); err != nil {
			t.Errorf("Clear: unexpected error: %v", err)
		}
	}()

	wg.Wait()

	// Cache should still be functional after Clear.
	require.NoError(t, sc.Put(syntheticSource("post-clear")))
	src := sc.Get("post-clear")
	require.NotNil(t, src)
	require.Equal(t, "post-clear", src.ContractID)
}

// TestSourceCache_ConcurrentTTLExpiry writes entries with a very short TTL and
// reads them back after expiry; concurrent access during the TTL window must
// not race.
// Run with: go test -race ./internal/sourcemap/ -run TestSourceCache_ConcurrentTTLExpiry
func TestSourceCache_ConcurrentTTLExpiry(t *testing.T) {
	sc := newTestSourceCache(t)
	sc.SetTTL(10 * time.Millisecond) // Expire quickly

	const goroutines = 15

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		g := g
		go func() {
			defer wg.Done()
			id := fmt.Sprintf("TTL%03d", g)
			_ = sc.Put(syntheticSource(id))
			// Small sleep so some goroutines read before expiry, some after.
			if g%3 == 0 {
				time.Sleep(15 * time.Millisecond)
			}
			_ = sc.Get(id)
		}()
	}

	wg.Wait()
}
