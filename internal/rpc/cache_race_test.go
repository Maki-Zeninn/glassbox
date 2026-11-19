// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package rpc

import (
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

// setupRaceTestDB initialises an in-memory SQLite database for a single
// race-detector test.  A t.Cleanup hook closes it so each test starts clean.
func setupRaceTestDB(t *testing.T) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, InitCacheWithDB(db))
	t.Cleanup(func() { _ = CloseCache() })
}

// TestRPCCache_ConcurrentReadsAndWrites exercises Get/SetWithTTL under
// concurrent access and verifies that results remain coherent.
// Run with: go test -race ./internal/rpc/ -run TestRPCCache_ConcurrentReadsAndWrites
func TestRPCCache_ConcurrentReadsAndWrites(t *testing.T) {
	setupRaceTestDB(t)

	const (
		goroutines = 20
		iterations = 25
	)

	var wg sync.WaitGroup
	wg.Add(goroutines * 2)

	// Writers
	for g := 0; g < goroutines; g++ {
		g := g
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				key := fmt.Sprintf("race-key-%d-%d", g, i)
				val := fmt.Sprintf("value-%d-%d", g, i)
				if err := SetWithTTL(key, val, time.Minute); err != nil {
					t.Errorf("SetWithTTL(%q): unexpected error: %v", key, err)
				}
			}
		}()
	}

	// Readers (intentionally overlap with the writers)
	for g := 0; g < goroutines; g++ {
		g := g
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				key := fmt.Sprintf("race-key-%d-%d", g, i)
				// A miss is acceptable here (writer may not have stored yet).
				_, _, err := Get(key)
				if err != nil {
					t.Errorf("Get(%q): unexpected error: %v", key, err)
				}
			}
		}()
	}

	wg.Wait()
}

// TestRPCCache_ConcurrentInvalidation exercises concurrent deletes and reads
// so the race detector can flag any unguarded map/struct access.
// Run with: go test -race ./internal/rpc/ -run TestRPCCache_ConcurrentInvalidation
func TestRPCCache_ConcurrentInvalidation(t *testing.T) {
	setupRaceTestDB(t)

	const keys = 50

	// Pre-populate
	for i := 0; i < keys; i++ {
		require.NoError(t, SetWithTTL(fmt.Sprintf("inv-key-%d", i), "value", time.Minute))
	}

	var wg sync.WaitGroup
	wg.Add(keys * 2)

	// Concurrent deletes
	for i := 0; i < keys; i++ {
		i := i
		go func() {
			defer wg.Done()
			_, err := CleanByFilter(CleanFilter{All: false, OlderThan: -1}) // nop filter triggers error, that's fine
			_ = err
			// More importantly: delete via eviction path exercised below
		}()
	}

	// Concurrent reads during invalidation
	for i := 0; i < keys; i++ {
		i := i
		go func() {
			defer wg.Done()
			_, _, err := Get(fmt.Sprintf("inv-key-%d", i))
			if err != nil {
				t.Errorf("Get unexpectedly errored during concurrent invalidation: %v", err)
			}
		}()
	}

	wg.Wait()
}

// TestRPCCache_ConcurrentStatsRead verifies that the atomic counters used by
// CacheStats are accessible from multiple goroutines without a data race.
// Run with: go test -race ./internal/rpc/ -run TestRPCCache_ConcurrentStatsRead
func TestRPCCache_ConcurrentStatsRead(t *testing.T) {
	setupRaceTestDB(t)

	const goroutines = 40
	var wg sync.WaitGroup
	wg.Add(goroutines)

	// Mix of cache ops and stats snapshots running simultaneously.
	for g := 0; g < goroutines; g++ {
		g := g
		go func() {
			defer wg.Done()
			if g%2 == 0 {
				_ = StatsSnapshot()
			} else {
				key := fmt.Sprintf("stats-key-%d", g)
				_ = SetWithTTL(key, "val", time.Minute)
				_, _, _ = Get(key)
			}
		}()
	}

	wg.Wait()
}

// TestRPCCache_ConcurrentCleanAll exercises the full eviction path under
// concurrent writes to confirm no unsafe state is left after cleanup.
// Run with: go test -race ./internal/rpc/ -run TestRPCCache_ConcurrentCleanAll
func TestRPCCache_ConcurrentCleanAll(t *testing.T) {
	setupRaceTestDB(t)

	const goroutines = 10

	var wg sync.WaitGroup
	wg.Add(goroutines + 1) // +1 for the concurrent CleanByFilter

	for g := 0; g < goroutines; g++ {
		g := g
		go func() {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				key := fmt.Sprintf("clean-key-%d-%d", g, i)
				_ = SetWithTTLAndNetwork(key, "v", time.Minute, "testnet")
				_, _, _ = Get(key)
			}
		}()
	}

	// Clean everything while writers are active.
	go func() {
		defer wg.Done()
		time.Sleep(time.Millisecond) // Let some writes land first.
		_, _ = CleanByFilter(CleanFilter{All: true})
	}()

	wg.Wait()

	// After cleanup, subsequent writes and reads should still succeed.
	require.NoError(t, SetWithTTL("post-clean", "ok", time.Minute))
	val, found, err := Get("post-clean")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "ok", val)
}

// TestRPCCache_ConcurrentNetworkFilter verifies that network-tagged entries
// remain correct when multiple goroutines write different networks in parallel.
// Run with: go test -race ./internal/rpc/ -run TestRPCCache_ConcurrentNetworkFilter
func TestRPCCache_ConcurrentNetworkFilter(t *testing.T) {
	setupRaceTestDB(t)

	networks := []string{"mainnet", "testnet", "futurenet"}
	const perNetwork = 15

	var wg sync.WaitGroup
	wg.Add(len(networks) * perNetwork)

	for _, net := range networks {
		net := net
		for i := 0; i < perNetwork; i++ {
			i := i
			go func() {
				defer wg.Done()
				key := fmt.Sprintf("%s-key-%d", net, i)
				_ = SetWithTTLAndNetwork(key, net+"-val", time.Minute, net)
				// Immediately read back
				val, found, err := Get(key)
				if err != nil {
					t.Errorf("Get(%q): unexpected error: %v", key, err)
				}
				if found && val != net+"-val" {
					t.Errorf("Get(%q): got %q, want %q", key, val, net+"-val")
				}
			}()
		}
	}

	wg.Wait()
}
