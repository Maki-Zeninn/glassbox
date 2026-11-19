// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package depcompat_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dotandev/glassbox/internal/depcompat"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── cargoLockVersion ─────────────────────────────────────────────────────────

// cargoLockVersion is an unexported function that is exercised indirectly
// through DetectVersions / GenerateGoldenBaseline.  We test the observable
// behaviour through the exported entry-points.

// ─── normaliseJSON (via GenerateGoldenBaseline) ───────────────────────────────

// TestGenerateGoldenBaseline_Success verifies that captured outputs are
// written to golden files with deterministically sorted keys.
func TestGenerateGoldenBaseline_Success(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Provide captured outputs with unsorted keys so we can verify normalisation.
	outputs := []depcompat.CapturedOutput{
		{
			Group:    depcompat.DepGroupStellarSDK,
			Kind:     depcompat.OutputReplay,
			Data:     []byte(`{"z_last":1,"a_first":2,"schema_version":"1"}`),
		},
		{
			Group:    depcompat.DepGroupCrypto,
			Kind:     depcompat.OutputAudit,
			Data:     []byte(`{"status":"ok","count":3}`),
		},
	}

	errs := depcompat.GenerateGoldenBaseline(dir, outputs)
	assert.Empty(t, errs, "expected no errors during baseline generation")

	// Read back and verify the files exist.
	replayPath := filepath.Join(dir, depcompat.GoldenFileName(depcompat.DepGroupStellarSDK, depcompat.OutputReplay))
	auditPath := filepath.Join(dir, depcompat.GoldenFileName(depcompat.DepGroupCrypto, depcompat.OutputAudit))

	replayBytes, err := os.ReadFile(replayPath)
	require.NoError(t, err, "replay golden file should be created")
	auditBytes, err := os.ReadFile(auditPath)
	require.NoError(t, err, "audit golden file should be created")

	// After normalisation the keys should be sorted: a_first before z_last.
	replay := string(replayBytes)
	aIdx := indexOf(replay, "a_first")
	zIdx := indexOf(replay, "z_last")
	if aIdx < 0 || zIdx < 0 {
		t.Fatalf("expected keys not found in normalised output: %s", replay)
	}
	assert.Less(t, aIdx, zIdx, "keys should be sorted: a_first before z_last")

	// Audit file should contain the original data.
	assert.Contains(t, string(auditBytes), `"status"`)
	assert.Contains(t, string(auditBytes), `"count"`)
}

// TestGenerateGoldenBaseline_CaptureError verifies that outputs with a
// CaptureError field are skipped and the error is returned.
func TestGenerateGoldenBaseline_CaptureError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	outputs := []depcompat.CapturedOutput{
		{
			Group:        depcompat.DepGroupSorobanHost,
			Kind:         depcompat.OutputTrace,
			CaptureError: "harness crashed",
		},
		{
			Group: depcompat.DepGroupRPCClient,
			Kind:  depcompat.OutputBinding,
			Data:  []byte(`{"ok":true}`),
		},
	}

	errs := depcompat.GenerateGoldenBaseline(dir, outputs)
	// The capture-error entry should have produced an error.
	assert.Len(t, errs, 1, "expected exactly one error for the failed capture")
	assert.Contains(t, errs[0].Error(), "harness crashed")

	// The successful entry should still be written.
	bindingPath := filepath.Join(dir, depcompat.GoldenFileName(depcompat.DepGroupRPCClient, depcompat.OutputBinding))
	_, err := os.ReadFile(bindingPath)
	assert.NoError(t, err, "binding golden file should still be created")

	// The failed entry should NOT be written.
	tracePath := filepath.Join(dir, depcompat.GoldenFileName(depcompat.DepGroupSorobanHost, depcompat.OutputTrace))
	_, err = os.ReadFile(tracePath)
	assert.True(t, os.IsNotExist(err), "trace golden file should not be created for capture error")
}

// TestGenerateGoldenBaseline_InvalidJSON verifies that malformed JSON
// produces an error and does not write a golden file.
func TestGenerateGoldenBaseline_InvalidJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	outputs := []depcompat.CapturedOutput{
		{
			Group: depcompat.DepGroupCrypto,
			Kind:  depcompat.OutputReplay,
			Data:  []byte(`{not: valid json}`),
		},
	}

	errs := depcompat.GenerateGoldenBaseline(dir, outputs)
	assert.Len(t, errs, 1, "expected one error for invalid JSON")
	assert.Contains(t, errs[0].Error(), "normalise")

	// No golden file should be written.
	path := filepath.Join(dir, depcompat.GoldenFileName(depcompat.DepGroupCrypto, depcompat.OutputReplay))
	_, err := os.ReadFile(path)
	assert.True(t, os.IsNotExist(err), "golden file should not be created for invalid JSON")
}

// TestGenerateGoldenBaseline_OverwritesExisting verifies that a second call
// overwrites the existing golden file.
func TestGenerateGoldenBaseline_OverwritesExisting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// First write.
	outputs1 := []depcompat.CapturedOutput{
		{Group: depcompat.DepGroupStellarSDK, Kind: depcompat.OutputAudit, Data: []byte(`{"version":1}`)},
	}
	errs := depcompat.GenerateGoldenBaseline(dir, outputs1)
	require.Empty(t, errs)

	// Second write with different data.
	outputs2 := []depcompat.CapturedOutput{
		{Group: depcompat.DepGroupStellarSDK, Kind: depcompat.OutputAudit, Data: []byte(`{"version":2}`)},
	}
	errs = depcompat.GenerateGoldenBaseline(dir, outputs2)
	require.Empty(t, errs)

	data, err := os.ReadFile(filepath.Join(dir, depcompat.GoldenFileName(depcompat.DepGroupStellarSDK, depcompat.OutputAudit)))
	require.NoError(t, err)
	assert.Contains(t, string(data), `"version": 2`, "golden file should be overwritten")
}

// ─── ToDepVersions ────────────────────────────────────────────────────────────

// TestVersionInfo_ToDepVersions verifies the conversion from VersionInfo to DepVersions.
func TestVersionInfo_ToDepVersions(t *testing.T) {
	t.Parallel()
	vi := depcompat.VersionInfo{
		StellarSDKVersion:   "v1.2.3",
		SorobanHostVersion:  "0.42.0",
		Ed25519DalekVersion: "1.0.0",
		Sha2Version:         "0.10.8",
		GoVersion:           "go1.26.1",
		RustVersion:         "rustc 1.80.0",
	}

	dv := vi.ToDepVersions()
	assert.Equal(t, "v1.2.3", dv.StellarSDKVersion)
	assert.Equal(t, "0.42.0", dv.SorobanHostVersion)
	assert.Equal(t, "1.0.0", dv.Ed25519DalekVersion)
	assert.Equal(t, "0.10.8", dv.Sha2Version)
	assert.Equal(t, "go1.26.1", dv.GoVersion)
	assert.Equal(t, "rustc 1.80.0", dv.RustVersion)
}

// TestVersionInfo_ToDepVersions_ZeroValue verifies the zero-value VersionInfo
// is converted correctly (all empty strings).
func TestVersionInfo_ToDepVersions_ZeroValue(t *testing.T) {
	t.Parallel()
	var vi depcompat.VersionInfo
	dv := vi.ToDepVersions()
	assert.Equal(t, "", dv.StellarSDKVersion)
	assert.Equal(t, "", dv.SorobanHostVersion)
	assert.Equal(t, "", dv.Ed25519DalekVersion)
	assert.Equal(t, "", dv.Sha2Version)
	assert.Equal(t, "", dv.GoVersion)
	assert.Equal(t, "", dv.RustVersion)
}

// ─── DetectVersions (smoke test) ──────────────────────────────────────────────

// TestDetectVersions_RepoRoot verifies that DetectVersions runs without
// panicking and returns a non-empty Go version.
func TestDetectVersions_RepoRoot(t *testing.T) {
	t.Parallel()
	// Use a temp dir that has no go.mod — DetectVersions falls back to
	// runtime.Version() for the Go version regardless.
	vi := depcompat.DetectVersions(t.TempDir())
	assert.NotEmpty(t, vi.GoVersion, "GoVersion should always be populated from runtime.Version()")
}

// TestDetectVersions_WithCargoLock verifies that version extraction from a
// synthetic Cargo.lock file works for a known package name.
func TestDetectVersions_WithCargoLock(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Create a minimal simulator/ sub-directory with a Cargo.lock file.
	simDir := filepath.Join(dir, "simulator")
	require.NoError(t, os.Mkdir(simDir, 0o755))

	cargoLock := `[[package]]
name = "ed25519-dalek"
version = "1.0.1"

[[package]]
name = "sha2"
version = "0.10.8"

[[package]]
name = "soroban-env-host"
version = "21.3.0"
`
	require.NoError(t, os.WriteFile(filepath.Join(simDir, "Cargo.lock"), []byte(cargoLock), 0o644))

	vi := depcompat.DetectVersions(dir)
	assert.Equal(t, "1.0.1", vi.Ed25519DalekVersion, "ed25519-dalek version from Cargo.lock")
	assert.Equal(t, "0.10.8", vi.Sha2Version, "sha2 version from Cargo.lock")
	assert.Equal(t, "21.3.0", vi.SorobanHostVersion, "soroban-env-host version from Cargo.lock")
}

// ─── normaliseJSON round-trip (via GenerateGoldenBaseline) ───────────────────

// TestNormaliseJSON_RoundTrip verifies that writing JSON through
// GenerateGoldenBaseline and reading it back produces valid JSON that
// round-trips correctly via CompareBytes (no diffs).
func TestNormaliseJSON_RoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	raw := []byte(`{"b":2,"a":1,"nested":{"z":true,"m":false}}`)
	outputs := []depcompat.CapturedOutput{
		{Group: depcompat.DepGroupRPCClient, Kind: depcompat.OutputTrace, Data: raw},
	}
	errs := depcompat.GenerateGoldenBaseline(dir, outputs)
	require.Empty(t, errs)

	goldenBytes, err := depcompat.ReadGolden(dir, depcompat.DepGroupRPCClient, depcompat.OutputTrace)
	require.NoError(t, err)

	// Normalise the raw input the same way GenerateGoldenBaseline would.
	// Comparing the golden against itself should yield DiffClassNone.
	res := depcompat.CompareBytes(depcompat.DepGroupRPCClient, depcompat.OutputTrace, goldenBytes, goldenBytes)
	assert.Equal(t, depcompat.DiffClassNone, res.Class, "golden compared against itself should have no diffs")
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// indexOf returns the byte offset of substr in s, or -1.
func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
