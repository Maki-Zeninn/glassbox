// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package bundle_test

import (
	"testing"

	"github.com/dotandev/glassbox/internal/bundle"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── NewBundlePathPolicy ───────────────────────────────────────────────────────

func TestNewBundlePathPolicy_Valid(t *testing.T) {
	logical := map[string]string{
		bundle.ArtifactSourceMap: "contracts/token/glassbox-build-manifest.json",
	}
	orig := map[string]string{
		bundle.ArtifactSourceMap: "/home/builder/projects/token/glassbox-build-manifest.json",
	}

	pp, err := bundle.NewBundlePathPolicy("/home/builder/projects/token", logical, orig)
	require.NoError(t, err)
	assert.NotNil(t, pp)
	assert.Equal(t, "home/builder/projects/token", pp.OriginRoot)
	assert.Equal(t, logical[bundle.ArtifactSourceMap], pp.LogicalPaths[bundle.ArtifactSourceMap])
	// Diagnostic paths must be stored but should match originals.
	assert.Equal(t, orig[bundle.ArtifactSourceMap], pp.DiagnosticPaths[bundle.ArtifactSourceMap])
}

func TestNewBundlePathPolicy_TraversalInLogicalPath(t *testing.T) {
	logical := map[string]string{
		bundle.ArtifactSourceMap: "../../etc/passwd",
	}
	_, err := bundle.NewBundlePathPolicy("/workspace", logical, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "path traversal")
}

func TestNewBundlePathPolicy_NullByteInLogicalPath(t *testing.T) {
	logical := map[string]string{
		bundle.ArtifactSourceMap: "contracts/token\x00.json",
	}
	_, err := bundle.NewBundlePathPolicy("/workspace", logical, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "null bytes")
}

func TestNewBundlePathPolicy_TraversalInRoot(t *testing.T) {
	_, err := bundle.NewBundlePathPolicy("../../etc", nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "path traversal")
}

func TestNewBundlePathPolicy_EmptyRoot(t *testing.T) {
	_, err := bundle.NewBundlePathPolicy("", nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not be empty")
}

// ── RewritePaths ──────────────────────────────────────────────────────────────

func TestRewritePaths_GlobalNewRoot(t *testing.T) {
	pp := &bundle.BundlePathPolicy{
		OriginRoot: "/home/builder/projects/token",
		LogicalPaths: map[string]string{
			bundle.ArtifactSourceMap: "contracts/token/glassbox-build-manifest.json",
		},
	}
	mapping := &bundle.ImportPathMapping{
		NewRoot: "/home/user/checkout",
	}

	result := pp.RewritePaths(mapping)
	assert.Empty(t, result.Errors, "no errors expected with valid mapping")
	resolved, ok := result.Resolved[bundle.ArtifactSourceMap]
	require.True(t, ok, "source_map should be resolved")
	assert.Contains(t, resolved, "contracts")
	assert.Contains(t, resolved, "glassbox-build-manifest.json")
}

func TestRewritePaths_PerArtifactOverride(t *testing.T) {
	pp := &bundle.BundlePathPolicy{
		LogicalPaths: map[string]string{
			bundle.ArtifactSourceMap: "manifests/token.json",
			bundle.ArtifactTrace:     "traces/trace.json",
		},
	}
	mapping := &bundle.ImportPathMapping{
		NewRoot: "/global/root",
		Overrides: map[string]string{
			bundle.ArtifactTrace: "/special/traces/root",
		},
	}

	result := pp.RewritePaths(mapping)
	assert.Empty(t, result.Errors)

	// trace should use the override root
	traceResolved := result.Resolved[bundle.ArtifactTrace]
	assert.Contains(t, traceResolved, "/special/traces/root")

	// source_map should use the global root
	smResolved := result.Resolved[bundle.ArtifactSourceMap]
	assert.Contains(t, smResolved, "/global/root")
}

func TestRewritePaths_TraversalAttemptProducesError(t *testing.T) {
	// Even if the input slips through storage, RewritePaths must catch
	// traversal on join.  We test by providing a mapping that tries to escape.
	pp := &bundle.BundlePathPolicy{
		LogicalPaths: map[string]string{
			bundle.ArtifactSourceMap: "../../../etc/passwd",
		},
	}
	// We use a root that is shallow enough that ../../.. escapes it.
	mapping := &bundle.ImportPathMapping{NewRoot: "/a/b"}

	result := pp.RewritePaths(mapping)
	// The traversal should produce a warning (ArtifactSourceMap is optional).
	assert.NotEmpty(t, result.Warnings, "traversal on optional path should produce a warning")
	_, ok := result.Resolved[bundle.ArtifactSourceMap]
	assert.False(t, ok, "traversing path must not be placed in Resolved")
}

func TestRewritePaths_NoMapping_RelativePathPassthrough(t *testing.T) {
	pp := &bundle.BundlePathPolicy{
		LogicalPaths: map[string]string{
			bundle.ArtifactSourceMap: "contracts/token/manifest.json",
		},
	}

	result := pp.RewritePaths(nil)
	resolved, ok := result.Resolved[bundle.ArtifactSourceMap]
	require.True(t, ok)
	assert.Equal(t, "contracts/token/manifest.json", resolved)
}

func TestRewritePaths_NoMapping_AbsoluteOptionalProducesWarning(t *testing.T) {
	pp := &bundle.BundlePathPolicy{
		LogicalPaths: map[string]string{
			bundle.ArtifactSourceMap: "/home/builder/absolute/path.json",
		},
	}

	result := pp.RewritePaths(nil)
	assert.Empty(t, result.Errors, "optional artifact missing mapping is a warning")
	assert.NotEmpty(t, result.Warnings)
}

func TestRewritePaths_EmptyLogicalPaths(t *testing.T) {
	pp := &bundle.BundlePathPolicy{}
	result := pp.RewritePaths(&bundle.ImportPathMapping{NewRoot: "/some/root"})
	assert.Empty(t, result.Errors)
	assert.Empty(t, result.Warnings)
	assert.Empty(t, result.Resolved)
}

// ── ValidateBundlePathPolicy ──────────────────────────────────────────────────

func TestValidateBundlePathPolicy_Nil(t *testing.T) {
	assert.NoError(t, bundle.ValidateBundlePathPolicy(nil))
}

func TestValidateBundlePathPolicy_Clean(t *testing.T) {
	pp := &bundle.BundlePathPolicy{
		OriginRoot: "/build/root",
		LogicalPaths: map[string]string{
			bundle.ArtifactSourceMap: "contracts/token",
		},
	}
	assert.NoError(t, bundle.ValidateBundlePathPolicy(pp))
}

func TestValidateBundlePathPolicy_TraversalRejected(t *testing.T) {
	pp := &bundle.BundlePathPolicy{
		LogicalPaths: map[string]string{
			bundle.ArtifactSourceMap: "../../etc/shadow",
		},
	}
	err := bundle.ValidateBundlePathPolicy(pp)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "path traversal")
}

// ── PathPolicyError ───────────────────────────────────────────────────────────

func TestIsPathPolicyError(t *testing.T) {
	err := &bundle.PathPolicyError{
		Errors: []bundle.PathRewriteError{{LogicalPath: "foo", Reason: "bar"}},
	}
	assert.True(t, bundle.IsPathPolicyError(err))
	assert.Contains(t, err.Error(), "foo")
}

// ── Bundle round-trip with PathPolicy ────────────────────────────────────────

func TestManifest_PathPolicy_RoundTrip(t *testing.T) {
	m := validManifest()
	pp, err := bundle.NewBundlePathPolicy(
		"/workspace/token",
		map[string]string{bundle.ArtifactSourceMap: "contracts/token/manifest.json"},
		map[string]string{bundle.ArtifactSourceMap: "/workspace/token/contracts/token/manifest.json"},
	)
	require.NoError(t, err)
	m.PathPolicy = pp

	dir := t.TempDir()
	path := dir + "/bundle.json"
	require.NoError(t, m.SaveToFile(path))

	loaded, err := bundle.LoadFromFile(path)
	require.NoError(t, err)
	require.NotNil(t, loaded.PathPolicy)
	assert.Equal(t, "workspace/token", loaded.PathPolicy.OriginRoot)
	assert.Equal(t,
		"contracts/token/manifest.json",
		loaded.PathPolicy.LogicalPaths[bundle.ArtifactSourceMap],
	)
}

// ── Unix / Windows path style tests ──────────────────────────────────────────

func TestRewritePaths_WindowsStylePaths(t *testing.T) {
	pp := &bundle.BundlePathPolicy{
		LogicalPaths: map[string]string{
			bundle.ArtifactSourceMap: "contracts\\token\\manifest.json",
		},
	}
	mapping := &bundle.ImportPathMapping{NewRoot: "C:\\Users\\dev\\checkout"}
	result := pp.RewritePaths(mapping)
	// No traversal, just Windows-style separators — should resolve cleanly.
	assert.Empty(t, result.Errors)
}
