// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"archive/zip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotandev/glassbox/internal/diagnostics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sampleDeps returns a representative slice of DependencyStatus values that
// mirrors what runDoctor produces — one passing, one failing, one fixable.
func sampleDeps() []DependencyStatus {
	return []DependencyStatus{
		{
			ID:        DepGo,
			Name:      "Go",
			Installed: true,
			Version:   "go1.21.0",
			Path:      "/usr/local/go/bin/go",
		},
		{
			ID:        DepSimulator,
			Name:      "Simulator Binary (glassbox-sim)",
			Installed: false,
			FixHint:   "Build the simulator: cd simulator && cargo build --release",
			Fixable:   true,
		},
		{
			ID:        DepRPC,
			Name:      "RPC endpoint",
			Installed: false,
			FixHint:   `RPC endpoint "https://soroban-testnet.stellar.org" is unreachable`,
			Fixable:   false,
		},
	}
}

func TestRunDoctorBundle_WritesArchive(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "diag.gbdiag")

	path, err := runDoctorBundle(sampleDeps(), out)
	require.NoError(t, err)
	assert.Equal(t, out, path)

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Greater(t, info.Size(), int64(0))
}

func TestRunDoctorBundle_ManifestHasChecks(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "diag.gbdiag")

	_, err := runDoctorBundle(sampleDeps(), out)
	require.NoError(t, err)

	zr, err := zip.OpenReader(out)
	require.NoError(t, err)
	defer zr.Close()

	var manifestFile *zip.File
	for _, f := range zr.File {
		if f.Name == "manifest.json" {
			manifestFile = f
			break
		}
	}
	require.NotNil(t, manifestFile, "manifest.json must be present")

	rc, err := manifestFile.Open()
	require.NoError(t, err)
	defer rc.Close()

	var manifest diagnostics.Manifest
	require.NoError(t, json.NewDecoder(rc).Decode(&manifest))

	assert.Equal(t, diagnostics.ManifestSchemaVersion, manifest.SchemaVersion)
	assert.Len(t, manifest.Checks, 3)

	// First check — Go, passing
	assert.Equal(t, string(DepGo), manifest.Checks[0].ID)
	assert.True(t, manifest.Checks[0].OK)

	// Second check — Simulator, failing
	assert.Equal(t, string(DepSimulator), manifest.Checks[1].ID)
	assert.False(t, manifest.Checks[1].OK)
	assert.NotEmpty(t, manifest.Checks[1].FixHint)

	// Third check — RPC, failing
	assert.Equal(t, string(DepRPC), manifest.Checks[2].ID)
	assert.False(t, manifest.Checks[2].OK)
}

func TestRunDoctorBundle_PathsRedacted(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	deps := []DependencyStatus{
		{
			ID:        DepSimulator,
			Name:      "Simulator Binary",
			Installed: true,
			Version:   "1.0.0",
			// Path contains the real home dir — must be masked
			Path: filepath.Join(home, ".Glassbox", "cache", "glassbox-sim"),
		},
	}

	dir := t.TempDir()
	out := filepath.Join(dir, "diag.gbdiag")

	_, err = runDoctorBundle(deps, out)
	require.NoError(t, err)

	zr, err := zip.OpenReader(out)
	require.NoError(t, err)
	defer zr.Close()

	for _, f := range zr.File {
		rc, err := f.Open()
		require.NoError(t, err)
		raw, _ := io.ReadAll(rc)
		rc.Close()

		// Home dir must not appear verbatim in any archive file.
		assert.NotContains(t, string(raw), home,
			"file %s must not contain raw home directory path", f.Name)
	}
}

func TestRunDoctorBundle_DefaultOutputInTempDir(t *testing.T) {
	// Empty outputPath triggers auto-generated name in os.TempDir().
	path, err := runDoctorBundle(sampleDeps(), "")
	require.NoError(t, err)
	defer os.Remove(path)

	assert.True(t, strings.HasPrefix(path, os.TempDir()) ||
		strings.HasPrefix(filepath.ToSlash(path), filepath.ToSlash(os.TempDir())),
		"auto-generated path %q should be under temp dir", path)
	assert.True(t, strings.HasSuffix(path, diagnostics.BundleExtension) ||
		strings.HasSuffix(path, ".zip"),
		"auto-generated path should have .gbdiag extension")
}

func TestRunDoctorBundle_NoSecretMaterial(t *testing.T) {
	t.Setenv("GLASSBOX_RPC_TOKEN", "tok-secret-value-xyz")
	t.Setenv("GLASSBOX_SENTRY_DSN", "https://abc@sentry.io/999")

	dir := t.TempDir()
	out := filepath.Join(dir, "diag.gbdiag")

	_, err := runDoctorBundle(sampleDeps(), out)
	require.NoError(t, err)

	zr, err := zip.OpenReader(out)
	require.NoError(t, err)
	defer zr.Close()

	for _, f := range zr.File {
		rc, err := f.Open()
		require.NoError(t, err)
		raw, _ := io.ReadAll(rc)
		rc.Close()

		content := string(raw)
		assert.NotContains(t, content, "tok-secret-value-xyz",
			"file %s must not contain RPC token", f.Name)
		assert.NotContains(t, content, "sentry.io/999",
			"file %s must not contain Sentry DSN", f.Name)
	}
}
