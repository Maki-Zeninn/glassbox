// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package diagnostics_test

import (
	"archive/zip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotandev/glassbox/internal/diagnostics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateBundle_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "test.gbdiag")

	path, err := diagnostics.GenerateBundle(context.Background(), diagnostics.BundleOptions{
		OutputPath: out,
	})
	require.NoError(t, err)
	assert.Equal(t, out, path)

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Greater(t, info.Size(), int64(0))

	// File permissions must be owner-only (0o600).
	// Skip on Windows where permission bits are not enforced.
	if os.Getenv("GOOS") != "windows" {
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
}

func TestGenerateBundle_ZipExtensionAccepted(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "test.zip")

	_, err := diagnostics.GenerateBundle(context.Background(), diagnostics.BundleOptions{
		OutputPath: out,
	})
	require.NoError(t, err)
}

func TestGenerateBundle_InvalidExtension(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "test.tar.gz")

	_, err := diagnostics.GenerateBundle(context.Background(), diagnostics.BundleOptions{
		OutputPath: out,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported diagnostics archive extension")
}

func TestGenerateBundle_ManifestContents(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "diag.gbdiag")

	checks := []diagnostics.CheckResult{
		{ID: "go", Name: "Go", OK: true, Version: "go1.21.0"},
		{ID: "rust", Name: "Rust", OK: false, FixHint: "install rustup"},
	}

	_, err := diagnostics.GenerateBundle(context.Background(), diagnostics.BundleOptions{
		OutputPath:    out,
		IncludeChecks: checks,
	})
	require.NoError(t, err)

	zr, err := zip.OpenReader(out)
	require.NoError(t, err)
	defer zr.Close()

	var manifestFile *zip.File
	var readmeFile *zip.File
	for _, f := range zr.File {
		switch f.Name {
		case "manifest.json":
			manifestFile = f
		case "README.txt":
			readmeFile = f
		}
	}
	require.NotNil(t, manifestFile, "manifest.json must be in archive")
	require.NotNil(t, readmeFile, "README.txt must be in archive")

	rc, err := manifestFile.Open()
	require.NoError(t, err)
	defer rc.Close()

	var manifest diagnostics.Manifest
	require.NoError(t, json.NewDecoder(rc).Decode(&manifest))

	assert.Equal(t, diagnostics.ManifestSchemaVersion, manifest.SchemaVersion)
	assert.NotZero(t, manifest.GeneratedAt)
	assert.NotEmpty(t, manifest.Meta.GoVersion)
	assert.NotEmpty(t, manifest.Platform.OS)
	assert.Len(t, manifest.Checks, 2)
	assert.Equal(t, "go", manifest.Checks[0].ID)
	assert.True(t, manifest.Checks[0].OK)
	assert.Equal(t, "rust", manifest.Checks[1].ID)
	assert.False(t, manifest.Checks[1].OK)
}

// TestGenerateBundle_NoSecrets asserts that representative secret-like values
// are redacted in the manifest regardless of their source.
func TestGenerateBundle_NoSecrets(t *testing.T) {
	// Set env vars that should be redacted.
	t.Setenv("GLASSBOX_RPC_TOKEN", "supersecrettoken123")
	t.Setenv("GLASSBOX_SENTRY_DSN", "https://abc@sentry.io/123")

	dir := t.TempDir()
	out := filepath.Join(dir, "diag.gbdiag")

	_, err := diagnostics.GenerateBundle(context.Background(), diagnostics.BundleOptions{
		OutputPath: out,
	})
	require.NoError(t, err)

	zr, err := zip.OpenReader(out)
	require.NoError(t, err)
	defer zr.Close()

	for _, f := range zr.File {
		rc, err := f.Open()
		require.NoError(t, err)
		var buf strings.Builder
		_, _ = buf.ReadFrom(rc)
		rc.Close()

		content := buf.String()
		assert.NotContains(t, content, "supersecrettoken123", "file %s must not contain raw token", f.Name)
		assert.NotContains(t, content, "sentry.io/123", "file %s must not contain raw Sentry DSN", f.Name)
	}
}

func TestRedactString_PrivateKeys(t *testing.T) {
	cases := []struct {
		input    string
		wantSame bool
	}{
		// 64-hex ed25519-like private key → redact
		{"aabbccdd112233440011223344556677aabbccdd11223344001122334455667788", false},
		// Stellar secret key → redact
		{"SDXL6BUWX7HQZQBZJN5HQUV3LQHZ7FJVHQXQ5Y3T6GFLQY2SDNM2Q", false},
		// Normal URL → keep
		{"https://soroban-testnet.stellar.org", true},
		// Empty → keep
		{"", true},
		// Short string → keep
		{"testnet", true},
	}

	for _, tc := range cases {
		got := diagnostics.RedactString(tc.input)
		if tc.wantSame {
			assert.Equal(t, tc.input, got, "input %q should not be redacted", tc.input)
		} else {
			assert.Equal(t, diagnostics.RedactedPlaceholder, got, "input %q should be redacted", tc.input)
		}
	}
}

func TestRedactPath_HomeDirMasked(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	p := filepath.Join(home, ".Glassbox", "config.json")
	redacted := diagnostics.RedactPath(p)
	assert.True(t, strings.HasPrefix(redacted, "~"), "home dir path should start with ~")
	assert.NotContains(t, redacted, home)
}

func TestRedactPath_NonHomePath(t *testing.T) {
	p := "/usr/local/bin/glassbox"
	assert.Equal(t, p, diagnostics.RedactPath(p))
}

func TestRedactConfigMap(t *testing.T) {
	m := map[string]string{
		"GLASSBOX_RPC_TOKEN": "mysecret",
		"GLASSBOX_NETWORK":   "testnet",
		"rpc_token":          "another-secret",
		"log_level":          "debug",
	}
	redacted := diagnostics.RedactConfigMap(m)
	assert.Equal(t, diagnostics.RedactedPlaceholder, redacted["GLASSBOX_RPC_TOKEN"])
	assert.Equal(t, diagnostics.RedactedPlaceholder, redacted["rpc_token"])
	assert.Equal(t, "testnet", redacted["GLASSBOX_NETWORK"])
	assert.Equal(t, "debug", redacted["log_level"])
}

// ── #843: privacy contract additions ─────────────────────────────────────────

// TestIsSecretFile_MarkerFiles verifies that well-known credential files are
// classified as secret and would be excluded from scanning.
func TestIsSecretFile_MarkerFiles(t *testing.T) {
	secretPaths := []string{
		"/home/user/.env",
		"/home/user/id_rsa",
		"/home/user/.ssh/id_ed25519",
		"/home/user/credentials",
		"C:\\Users\\bob\\id_dsa",
		"/etc/ssl/private/cert.pem",
		"token.key",
		"keystore.json",
		"wallet.p12",
		".netrc",
		".npmrc",
	}
	for _, p := range secretPaths {
		assert.True(t, diagnostics.IsSecretFile(p), "expected %q to be classified as a secret file", p)
	}
}

// TestIsSecretFile_SafeFiles verifies that normal source and config files are
// not misclassified as secret.
func TestIsSecretFile_SafeFiles(t *testing.T) {
	safePaths := []string{
		"/home/user/.bashrc",
		"/etc/glassbox/config.toml",
		"main.go",
		"config.json",
		"README.md",
		"glassbox.toml",
	}
	for _, p := range safePaths {
		assert.False(t, diagnostics.IsSecretFile(p), "expected %q NOT to be classified as a secret file", p)
	}
}

// TestGenerateBundle_ManifestContainsPolicy asserts that the manifest includes
// a redaction policy with a collector version and non-empty inventory.
func TestGenerateBundle_ManifestContainsPolicy(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "diag.gbdiag")

	_, err := diagnostics.GenerateBundle(context.Background(), diagnostics.BundleOptions{
		OutputPath: out,
	})
	require.NoError(t, err)

	zr, err := zip.OpenReader(out)
	require.NoError(t, err)
	defer zr.Close()

	var manifest diagnostics.Manifest
	for _, f := range zr.File {
		if f.Name != "manifest.json" {
			continue
		}
		rc, err := f.Open()
		require.NoError(t, err)
		defer rc.Close()
		require.NoError(t, json.NewDecoder(rc).Decode(&manifest))
	}

	assert.NotEmpty(t, manifest.Policy.CollectorVersion, "policy must include a collector version")
	assert.NotEmpty(t, manifest.Policy.Inventory, "policy inventory must be non-empty")
	assert.False(t, manifest.Policy.VerboseMode, "default bundle should have verbose=false")
}

// TestGenerateBundle_VerboseFlag propagates to the manifest policy.
func TestGenerateBundle_VerboseFlag(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "verbose.gbdiag")

	_, err := diagnostics.GenerateBundle(context.Background(), diagnostics.BundleOptions{
		OutputPath: out,
		Verbose:    true,
	})
	require.NoError(t, err)

	zr, err := zip.OpenReader(out)
	require.NoError(t, err)
	defer zr.Close()

	var manifest diagnostics.Manifest
	for _, f := range zr.File {
		if f.Name != "manifest.json" {
			continue
		}
		rc, err := f.Open()
		require.NoError(t, err)
		defer rc.Close()
		require.NoError(t, json.NewDecoder(rc).Decode(&manifest))
	}

	assert.True(t, manifest.Policy.VerboseMode, "verbose bundle should have verbose=true")
}

// TestInspectManifest_ListsFields verifies that InspectManifest returns a
// non-empty description with field classification labels.
func TestInspectManifest_ListsFields(t *testing.T) {
	out := diagnostics.InspectManifest(false)
	assert.Contains(t, out, "redacted", "inventory should list fields classified as redacted")
	assert.Contains(t, out, "safe", "inventory should list fields classified as safe")
	assert.Contains(t, out, "[REDACTED]", "description should mention the redaction placeholder")
}

// TestPathAllowedForScan_BlocksSecretFiles verifies the path allowlist rejects
// credential files regardless of directory.
func TestPathAllowedForScan_BlocksSecretFiles(t *testing.T) {
	assert.False(t, diagnostics.PathAllowedForScan("/data/id_rsa"), "id_rsa must be blocked")
	assert.False(t, diagnostics.PathAllowedForScan("secrets.json"), "secrets.json must be blocked")
	assert.True(t, diagnostics.PathAllowedForScan("/etc/glassbox/config.toml"), "config.toml must be allowed")
}
