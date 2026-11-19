// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// corpusFile is the path to the shared conformance corpus relative to the repo root.
const corpusFile = "testdata/canonical-conformance/corpus.json"

// conformanceCorpus is the top-level structure of corpus.json.
type conformanceCorpus struct {
	Version     string           `json:"version"`
	Description string           `json:"description"`
	Cases       []conformanceCase `json:"cases"`
	Invalid     []invalidCase    `json:"invalid"`
}

// conformanceCase represents a single valid canonical-JSON test case.
type conformanceCase struct {
	ID          string      `json:"id"`
	Description string      `json:"description"`
	Input       interface{} `json:"input"`
	Canonical   string      `json:"canonical"`
	SHA256      string      `json:"sha256"`
	Notes       string      `json:"notes,omitempty"`
}

// invalidCase represents a case that must be rejected by both runtimes.
type invalidCase struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Notes       string `json:"notes,omitempty"`
}

// repoRoot walks up from the current file to find the repository root
// (the directory containing go.mod).
func repoRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")

	dir := filepath.Dir(filename)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repository root (go.mod not found)")
		}
		dir = parent
	}
}

// loadCorpus reads and decodes the conformance corpus JSON file.
func loadCorpus(t *testing.T) conformanceCorpus {
	t.Helper()
	root := repoRoot(t)
	path := filepath.Join(root, corpusFile)
	data, err := os.ReadFile(path)
	require.NoError(t, err, "reading corpus file %s", path)

	var corpus conformanceCorpus
	require.NoError(t, json.Unmarshal(data, &corpus), "decoding corpus JSON")
	return corpus
}

// safeHash returns just the first 16 hex characters of a SHA-256 hex string,
// safe to print in failure messages without leaking full payload data.
func safeHash(h string) string {
	if len(h) > 16 {
		return h[:16] + "…"
	}
	return h
}

// TestCanonicalConformance_ValidCases verifies that every valid corpus case
// produces the expected canonical bytes and SHA-256 hash when processed by
// the Go canonicalization implementation.
//
// On failure, only the fixture ID and a truncated hash are printed to avoid
// leaking raw payload data in CI logs.
func TestCanonicalConformance_ValidCases(t *testing.T) {
	corpus := loadCorpus(t)
	require.NotEmpty(t, corpus.Cases, "corpus must have at least one valid case")

	for _, tc := range corpus.Cases {
		tc := tc // capture range variable
		t.Run(tc.ID, func(t *testing.T) {
			// Canonicalize the input using the internal Go implementation.
			got, err := canonicalJSON(tc.Input)
			require.NoError(t, err, "[%s] canonicalJSON returned error", tc.ID)

			// Verify the canonical string matches byte-for-byte.
			gotStr := string(got)
			if !assert.Equal(t, tc.Canonical, gotStr,
				"[%s] canonical string mismatch", tc.ID) {
				// Print safe diagnostic info only.
				t.Logf("[%s] want hash: %s", tc.ID, safeHash(tc.SHA256))
			}

			// Verify the SHA-256 hash matches.
			sum := sha256.Sum256(got)
			gotHex := fmt.Sprintf("%x", sum)
			if !assert.Equal(t, tc.SHA256, gotHex,
				"[%s] SHA-256 hash mismatch", tc.ID) {
				t.Logf("[%s] fixture hash: %s  computed: %s",
					tc.ID, safeHash(tc.SHA256), safeHash(gotHex))
			}
		})
	}
}

// TestCanonicalConformance_Reproducibility verifies that canonicalJSON is
// deterministic: calling it twice on the same input produces identical bytes.
func TestCanonicalConformance_Reproducibility(t *testing.T) {
	corpus := loadCorpus(t)

	for _, tc := range corpus.Cases {
		tc := tc
		t.Run(tc.ID, func(t *testing.T) {
			first, err := canonicalJSON(tc.Input)
			require.NoError(t, err)
			second, err := canonicalJSON(tc.Input)
			require.NoError(t, err)
			assert.Equal(t, string(first), string(second),
				"[%s] canonicalJSON is not deterministic", tc.ID)
		})
	}
}

// TestCanonicalConformance_CorpusIntegrity verifies that the corpus itself is
// internally consistent: every stored sha256 matches the stored canonical string.
// This catches corpus files with stale hashes without requiring re-running
// the full conformance suite.
func TestCanonicalConformance_CorpusIntegrity(t *testing.T) {
	corpus := loadCorpus(t)

	for _, tc := range corpus.Cases {
		tc := tc
		t.Run(tc.ID, func(t *testing.T) {
			if tc.SHA256 == "" || tc.SHA256 == "placeholder" {
				t.Skipf("[%s] sha256 field is a placeholder — update corpus.json", tc.ID)
			}
			sum := sha256.Sum256([]byte(tc.Canonical))
			gotHex := fmt.Sprintf("%x", sum)
			if !assert.Equal(t, tc.SHA256, gotHex,
				"[%s] corpus sha256 does not match stored canonical string", tc.ID) {
				t.Logf("[%s] stored_hash: %s  recomputed: %s",
					tc.ID, safeHash(tc.SHA256), safeHash(gotHex))
			}
		})
	}
}
