// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cache

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// newTestManager creates a cache Manager backed by a temp directory so tests
// do not touch the real user cache directory.
func newTestManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	return NewManager(dir, DefaultConfig())
}

// writeSyntheticFile creates a small file inside the cache dir to simulate a
// cached entry.
func writeSyntheticFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))
}

// TestCacheManager_ConcurrentGetSize verifies that GetCacheSize is safe to
// call from multiple goroutines concurrently.
// Run with: go test -race ./internal/cache/ -run TestCacheManager_ConcurrentGetSize
func TestCacheManager_ConcurrentGetSize(t *testing.T) {
	m := newTestManager(t)
	dir, err := m.GetCacheDir()
	require.NoError(t, err)

	// Seed a few files.
	for i := 0; i < 10; i++ {
		writeSyntheticFile(t, dir, fmt.Sprintf("entry-%d.bin", i), fmt.Sprintf("data-%d", i))
	}

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			size, err := m.GetCacheSize()
			if err != nil {
				t.Errorf("GetCacheSize: unexpected error: %v", err)
			}
			if size < 0 {
				t.Errorf("GetCacheSize: got negative size %d", size)
			}
		}()
	}

	wg.Wait()
}

// TestCacheManager_ConcurrentListAndClean verifies that concurrent calls to
// ListCachedFiles and CleanLRU do not race on the underlying directory walk.
// Run with: go test -race ./internal/cache/ -run TestCacheManager_ConcurrentListAndClean
func TestCacheManager_ConcurrentListAndClean(t *testing.T) {
	// Use a tiny max size so CleanLRU actually deletes something.
	dir := t.TempDir()
	cfg := Config{MaxSizeBytes: 50} // very small threshold
	m := NewManager(dir, cfg)

	// Seed 20 files, each ~10 bytes.
	for i := 0; i < 20; i++ {
		writeSyntheticFile(t, dir, fmt.Sprintf("file-%02d.bin", i), "0123456789")
	}

	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		g := g
		go func() {
			defer wg.Done()
			if g%2 == 0 {
				_, err := m.ListCachedFiles()
				if err != nil {
					t.Errorf("ListCachedFiles: unexpected error: %v", err)
				}
			} else {
				_, err := m.CleanLRU()
				if err != nil {
					t.Errorf("CleanLRU: unexpected error: %v", err)
				}
			}
		}()
	}

	wg.Wait()
}

// TestCacheManager_ConcurrentSortAndList exercises SortFilesByAccessTime on
// a slice produced by ListCachedFiles while other goroutines are also
// listing, ensuring no shared state is mutated.
// Run with: go test -race ./internal/cache/ -run TestCacheManager_ConcurrentSortAndList
func TestCacheManager_ConcurrentSortAndList(t *testing.T) {
	m := newTestManager(t)
	dir, err := m.GetCacheDir()
	require.NoError(t, err)

	for i := 0; i < 15; i++ {
		writeSyntheticFile(t, dir, fmt.Sprintf("sort-%02d.bin", i), "abcdef")
	}

	const goroutines = 16
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			files, err := m.ListCachedFiles()
			if err != nil {
				t.Errorf("ListCachedFiles: unexpected error: %v", err)
				return
			}
			// Each goroutine sorts its own copy — no sharing intended.
			SortFilesByAccessTime(files)
		}()
	}

	wg.Wait()
}
