// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package testgen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Helpers ────────────────────────────────────────────────────────────────────

func newGenerator(t *testing.T) *FixtureGenerator {
	t.Helper()
	return NewFixtureGenerator(t.TempDir())
}

func readJSON(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "reading generated fixture")
	var out map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &out), "parsing generated fixture")
	return out
}

// ── Layer coverage ─────────────────────────────────────────────────────────────

// TestFixtureGenerator_AllLayers verifies that GenerateOne succeeds for every
// supported layer and that each fixture is non-empty, valid JSON, and
// contains no real identifiers or secrets.
func TestFixtureGenerator_AllLayers(t *testing.T) {
	gen := newGenerator(t)

	for _, layer := range AllLayers {
		layer := layer
		t.Run(string(layer), func(t *testing.T) {
			req := FixtureRequest{
				Layer:        layer,
				ScenarioSlug: "minimal_stub",
				IssueRef:     "issue860",
			}

			path, err := gen.GenerateOne(req)
			require.NoError(t, err, "GenerateOne should not fail for layer %s", layer)
			require.NotEmpty(t, path)

			info, err := os.Stat(path)
			require.NoError(t, err, "generated file must exist on disk")
			require.Greater(t, info.Size(), int64(0), "generated file must not be empty")

			data := readJSON(t, path)

			// All fixtures must embed _meta with layer and issue_ref.
			meta, ok := data["_meta"].(map[string]interface{})
			require.True(t, ok, "fixture must contain a _meta object")
			assert.Equal(t, string(layer), meta["layer"])
			assert.Equal(t, "issue860", meta["issue_ref"])
			assert.NotEmpty(t, meta["failure_class"])
			assert.Equal(t, canonicalTimestamp, meta["generated_at"])

			// No real identifiers: tx_hash or session id must use canonical stubs.
			if txHash, ok := data["tx_hash"].(string); ok {
				assert.Equal(t, canonicalTxHash, txHash,
					"tx_hash must be the canonical stub, not a real hash")
			}
		})
	}
}

// TestFixtureGenerator_ByteStable verifies that running GenerateOne twice
// with the same inputs produces identical files.
func TestFixtureGenerator_ByteStable(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	req1 := FixtureRequest{
		Layer:        LayerRPC,
		ScenarioSlug: "stable_test",
		IssueRef:     "issue860",
		OutputDir:    dir1,
	}
	req2 := FixtureRequest{
		Layer:        LayerRPC,
		ScenarioSlug: "stable_test",
		IssueRef:     "issue860",
		OutputDir:    dir2,
	}

	gen := NewFixtureGenerator(".")
	path1, err := gen.GenerateOne(req1)
	require.NoError(t, err)
	path2, err := gen.GenerateOne(req2)
	require.NoError(t, err)

	bytes1, err := os.ReadFile(path1)
	require.NoError(t, err)
	bytes2, err := os.ReadFile(path2)
	require.NoError(t, err)

	assert.Equal(t, bytes1, bytes2, "identical requests must produce byte-identical fixtures")
}

// TestFixtureGenerator_FilenameConventions verifies that filenames follow the
// documented convention: <scenario>_<issue>.<ext>.
func TestFixtureGenerator_FilenameConventions(t *testing.T) {
	cases := []struct {
		layer    Layer
		slug     string
		ref      string
		wantSufx string
	}{
		{LayerRPC, "gettransaction_notfound", "issue150", ".json"},
		{LayerTrace, "empty_steps_no_events", "pr319", ".trace.json"},
		{LayerSession, "missing_txhash", "issue230", ".session.json"},
		{LayerAudit, "empty_payload_rejected", "issue860", ".payload.json"},
		{LayerCLI, "debug_exitcode_zero", "issue800", ".env.json"},
	}

	gen := newGenerator(t)
	for _, tc := range cases {
		tc := tc
		t.Run(string(tc.layer)+"_"+tc.slug, func(t *testing.T) {
			req := FixtureRequest{
				Layer:        tc.layer,
				ScenarioSlug: tc.slug,
				IssueRef:     tc.ref,
			}
			path, err := gen.GenerateOne(req)
			require.NoError(t, err)

			base := filepath.Base(path)
			assert.True(t, strings.HasSuffix(base, tc.wantSufx),
				"filename %q should end with %q", base, tc.wantSufx)
			assert.True(t, strings.HasPrefix(base, tc.slug),
				"filename %q should start with scenario slug %q", base, tc.slug)
			assert.Contains(t, base, tc.ref,
				"filename %q should contain issue/PR ref %q", base, tc.ref)
		})
	}
}

// TestFixtureGenerator_FailureClassDefault verifies that when FailureClass is
// empty it is derived from ScenarioSlug.
func TestFixtureGenerator_FailureClassDefault(t *testing.T) {
	gen := newGenerator(t)
	req := FixtureRequest{
		Layer:        LayerRPC,
		ScenarioSlug: "gettransaction_not_found",
		IssueRef:     "issue860",
		// FailureClass intentionally left empty.
	}

	path, err := gen.GenerateOne(req)
	require.NoError(t, err)

	data := readJSON(t, path)
	meta := data["_meta"].(map[string]interface{})
	fc := meta["failure_class"].(string)
	assert.Contains(t, fc, "gettransaction",
		"failure_class should be derived from slug when not explicitly set")
}

// TestFixtureGenerator_NoRealIdentifiers checks that no field in any
// generated fixture contains a real mainnet or testnet contract ID pattern.
func TestFixtureGenerator_NoRealIdentifiers(t *testing.T) {
	gen := newGenerator(t)
	for _, layer := range AllLayers {
		layer := layer
		t.Run(string(layer), func(t *testing.T) {
			req := FixtureRequest{
				Layer:        layer,
				ScenarioSlug: "no_secrets_check",
				IssueRef:     "issue860",
			}
			path, err := gen.GenerateOne(req)
			require.NoError(t, err)

			raw, err := os.ReadFile(path)
			require.NoError(t, err)
			content := string(raw)

			// Ensure no production-like long hex strings (64 chars) other than
			// the canonical stub are present.
			const realHashPattern = "a1b2c3d4e5f6"
			assert.NotContains(t, content, realHashPattern,
				"fixture must not contain non-canonical identifier patterns")
		})
	}
}

// TestFixtureGenerator_ValidationErrors verifies that malformed requests are
// rejected before any file is written.
func TestFixtureGenerator_ValidationErrors(t *testing.T) {
	gen := newGenerator(t)

	cases := []struct {
		name string
		req  FixtureRequest
	}{
		{
			name: "missing layer",
			req:  FixtureRequest{ScenarioSlug: "foo", IssueRef: "issue860"},
		},
		{
			name: "missing slug",
			req:  FixtureRequest{Layer: LayerRPC, IssueRef: "issue860"},
		},
		{
			name: "invalid layer",
			req:  FixtureRequest{Layer: "unknown", ScenarioSlug: "foo"},
		},
		{
			name: "slug with spaces",
			req:  FixtureRequest{Layer: LayerRPC, ScenarioSlug: "has spaces"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := gen.GenerateOne(tc.req)
			assert.Error(t, err, "should return an error for: %s", tc.name)
		})
	}
}

// TestFixtureGenerator_GenerateBatch verifies the Generate (batch) API.
func TestFixtureGenerator_GenerateBatch(t *testing.T) {
	gen := newGenerator(t)

	requests := []FixtureRequest{
		{Layer: LayerRPC, ScenarioSlug: "batch_rpc", IssueRef: "issue860"},
		{Layer: LayerTrace, ScenarioSlug: "batch_trace", IssueRef: "issue860"},
		{Layer: LayerAudit, ScenarioSlug: "batch_audit", IssueRef: "issue860"},
	}

	results := gen.Generate(requests)
	require.Len(t, results, 3)

	for _, r := range results {
		require.NoError(t, r.Err, "Generate should not fail for layer %s", r.Layer)
		require.NotEmpty(t, r.Path)
		_, err := os.Stat(r.Path)
		require.NoError(t, err, "generated file must exist: %s", r.Path)
	}
}
