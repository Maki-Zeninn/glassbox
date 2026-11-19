// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotandev/glassbox/internal/manifest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// generateTestKey writes a fresh Ed25519 PKCS#8 PEM key to a temp file and
// returns the path.
func generateTestKey(t *testing.T) string {
	t.Helper()
	pemBytes, err := generatePEMKey()
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "key.pem")
	require.NoError(t, os.WriteFile(path, pemBytes, 0600))
	return path
}

func TestDetectArtifacts_ClassifiesFiles(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"glassbox-linux-amd64.tar.gz":   "archive",
		"glassbox-darwin-arm64.tar.gz":  "archive",
		"glassbox-windows-amd64.zip":    "archive",
		"checksums.sha256":              "checksums",
		"version.txt":                   "metadata",
		"manifest.json":                 "SKIP",
	}
	for name, _ := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644))
	}

	entries, err := detectArtifacts(dir)
	require.NoError(t, err)

	names := make(map[string]bool)
	for _, e := range entries {
		names[e.Name] = true
	}

	assert.True(t, names["glassbox-linux-amd64.tar.gz"])
	assert.True(t, names["glassbox-darwin-arm64.tar.gz"])
	assert.True(t, names["glassbox-windows-amd64.zip"])
	assert.True(t, names["checksums.sha256"])
	assert.True(t, names["version.txt"])
	// manifest.json must be excluded.
	assert.False(t, names["manifest.json"], "manifest.json must be excluded from artifact list")
}

func TestPlatformFromName(t *testing.T) {
	cases := []struct {
		name     string
		expected string
	}{
		{"glassbox-linux-amd64.tar.gz", "linux/amd64"},
		{"glassbox-linux-arm64.tar.gz", "linux/arm64"},
		{"glassbox-darwin-amd64.tar.gz", "darwin/amd64"},
		{"glassbox-darwin-arm64.tar.gz", "darwin/arm64"},
		{"glassbox-windows-amd64.zip", "windows/amd64"},
		{"checksums.sha256", ""},
		{"version.txt", ""},
	}
	for _, c := range cases {
		got := platformFromName(c.name)
		assert.Equal(t, c.expected, got, "platform for %q", c.name)
	}
}

func TestKindFromName(t *testing.T) {
	cases := []struct {
		name string
		kind manifest.ArtifactKind
	}{
		{"glassbox-linux-amd64.tar.gz", manifest.KindArchive},
		{"glassbox-windows-amd64.zip", manifest.KindArchive},
		{"checksums.sha256", manifest.KindChecksum},
		{"version.txt", manifest.KindMetadata},
		{"sbom.spdx.json", manifest.KindMetadata},
		{"glassbox-linux-amd64", manifest.KindBinary},
	}
	for _, c := range cases {
		got := kindFromName(c.name)
		assert.Equal(t, c.kind, got, "kind for %q", c.name)
	}
}

func TestRun_MissingRequiredFlags(t *testing.T) {
	err := run([]string{"--dist", "."})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--version")
	assert.Contains(t, err.Error(), "--commit")
	assert.Contains(t, err.Error(), "--build-date")
}

func TestRun_MissingSigningKey(t *testing.T) {
	// Unset env var to ensure it is not accidentally set.
	t.Setenv("GLASSBOX_MANIFEST_SIGNING_KEY", "")
	err := run([]string{
		"--version", "v1.0.0",
		"--commit", "abc123",
		"--build-date", "2026-01-01T00:00:00Z",
		"--dist", ".",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "signing key required")
}

func TestRun_EndToEnd(t *testing.T) {
	dir := t.TempDir()
	// Write some fake artifacts.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "glassbox-linux-amd64.tar.gz"), []byte("fake archive"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "checksums.sha256"), []byte("aabbcc  glassbox-linux-amd64.tar.gz\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "version.txt"), []byte("version=v1.0.0\ncommit=abc\nbuild_date=2026-01-01T00:00:00Z\n"), 0644))

	keyPath := generateTestKey(t)
	outPath := filepath.Join(dir, "manifest.json")

	err := run([]string{
		"--dist", dir,
		"--version", "v1.0.0",
		"--commit", strings.Repeat("a", 40),
		"--build-date", "2026-01-01T00:00:00Z",
		"--signing-key", keyPath,
		"--output", outPath,
		"--verify",
		"--signer-identity", "test-pipeline",
	})
	require.NoError(t, err)

	// Parse and check the written manifest.
	raw, err := os.ReadFile(outPath)
	require.NoError(t, err)

	var sm manifest.SignedManifest
	require.NoError(t, json.Unmarshal(raw, &sm))

	assert.Equal(t, "1", sm.SchemaVersion)
	assert.Equal(t, "v1.0.0", sm.Version)
	assert.NotEmpty(t, sm.ManifestHash)
	assert.NotEmpty(t, sm.Signature)
	assert.NotEmpty(t, sm.PublicKey)
	assert.Equal(t, 3, len(sm.Artifacts), "three artifacts expected")
	assert.NotNil(t, sm.Provenance)
	assert.Equal(t, "test-pipeline", sm.Provenance.SignerIdentity)

	// Verify signature programmatically.
	result := manifest.Verify(&sm)
	assert.True(t, result.Valid, "manifest must verify after round-trip")
}

// ─── SBOM detection helpers ───────────────────────────────────────────────────

func TestIsSBOMFile(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"glassbox-v1.0.0.spdx.json", true},
		{"glassbox-v1.0.0.sbom.json", true},
		{"glassbox-v1.0.0.cdx.json", true},
		{"checksums.sha256", false},
		{"glassbox-linux-amd64.tar.gz", false},
		{"version.txt", false},
		{"manifest.json", false},
	}
	for _, c := range cases {
		got := isSBOMFile(c.name)
		assert.Equal(t, c.want, got, "isSBOMFile(%q)", c.name)
	}
}

func TestDetectSBOMRef_FindsSPDXFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "glassbox-v1.0.0.spdx.json"), []byte("{}"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "checksums.sha256"), []byte("abc"), 0644))

	ref := detectSBOMRef(dir)
	assert.Equal(t, "glassbox-v1.0.0.spdx.json", ref)
}

func TestDetectSBOMRef_NoSBOM(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "checksums.sha256"), []byte("abc"), 0644))

	ref := detectSBOMRef(dir)
	assert.Empty(t, ref, "detectSBOMRef must return empty when no SBOM present")
}

func TestRun_AutoDetectsSBOMRef(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "glassbox-linux-amd64.tar.gz"), []byte("fake archive"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "checksums.sha256"), []byte("abc"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "glassbox-v1.0.0.spdx.json"), []byte(`{"spdxVersion":"SPDX-2.3"}`), 0644))

	keyPath := generateTestKey(t)
	outPath := filepath.Join(dir, "manifest.json")

	err := run([]string{
		"--dist", dir,
		"--version", "v1.0.0",
		"--commit", strings.Repeat("a", 40),
		"--build-date", "2026-01-01T00:00:00Z",
		"--signing-key", keyPath,
		"--output", outPath,
		"--json-only",
		// NOTE: no --sbom-ref flag — it should be auto-detected.
	})
	require.NoError(t, err)

	raw, err := os.ReadFile(outPath)
	require.NoError(t, err)
	var sm manifest.SignedManifest
	require.NoError(t, json.Unmarshal(raw, &sm))
	assert.Equal(t, "glassbox-v1.0.0.spdx.json", sm.SBOMRef,
		"SBOMRef must be auto-populated from the detected SPDX file")
}

// ─── Build provenance tests ───────────────────────────────────────────────────

func TestRun_BuildProvenanceEmbedded(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "glassbox-linux-amd64.tar.gz"), []byte("fake binary"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "checksums.sha256"), []byte("abc"), 0644))

	keyPath := generateTestKey(t)
	outPath := filepath.Join(dir, "manifest.json")

	commit := strings.Repeat("b", 40)
	err := run([]string{
		"--dist", dir,
		"--version", "v2.0.0",
		"--commit", commit,
		"--build-date", "2026-06-01T00:00:00Z",
		"--signing-key", keyPath,
		"--output", outPath,
		"--json-only",
		// Build provenance flags
		"--build-source-repository", "https://github.com/dotandev/glassbox",
		"--build-source-ref", "refs/tags/v2.0.0",
		"--build-go-version", "go1.26.0 linux/amd64",
		"--build-runner-os", "ubuntu-24.04",
		"--build-workflow", ".github/workflows/release.yml",
		"--build-run-id", "99999999",
	})
	require.NoError(t, err)

	raw, err := os.ReadFile(outPath)
	require.NoError(t, err)

	var sm manifest.SignedManifest
	require.NoError(t, json.Unmarshal(raw, &sm))

	require.NotNil(t, sm.BuildProvenance, "build_provenance must be present")
	assert.Equal(t, "https://github.com/dotandev/glassbox", sm.BuildProvenance.SourceRepository)
	assert.Equal(t, "refs/tags/v2.0.0", sm.BuildProvenance.SourceRef)
	assert.Equal(t, commit, sm.BuildProvenance.CommitSHA)
	assert.Equal(t, "go1.26.0 linux/amd64", sm.BuildProvenance.GoVersion)
	assert.Equal(t, "ubuntu-24.04", sm.BuildProvenance.BuildRunnerOS)
	assert.Equal(t, ".github/workflows/release.yml", sm.BuildProvenance.BuildWorkflow)
	assert.Equal(t, "99999999", sm.BuildProvenance.BuildRunID)

	// Signature must still be valid when build_provenance is present.
	result := manifest.Verify(&sm)
	assert.True(t, result.Valid, "manifest with build_provenance must still verify")
}

func TestRun_BuildProvenanceOmittedWhenEmpty(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "glassbox-linux-amd64.tar.gz"), []byte("data"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "checksums.sha256"), []byte("abc"), 0644))

	keyPath := generateTestKey(t)
	outPath := filepath.Join(dir, "manifest.json")

	err := run([]string{
		"--dist", dir,
		"--version", "v1.0.0",
		"--commit", strings.Repeat("c", 40),
		"--build-date", "2026-01-01T00:00:00Z",
		"--signing-key", keyPath,
		"--output", outPath,
		"--json-only",
		// No --build-* flags — provenance should be absent from JSON.
	})
	require.NoError(t, err)

	raw, err := os.ReadFile(outPath)
	require.NoError(t, err)

	// build_provenance key must be absent when no flags are supplied.
	assert.NotContains(t, string(raw), `"build_provenance"`,
		"build_provenance must be omitted when no provenance flags are provided")
}

func TestRun_TamperedProvenance_SignatureInvalid(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "glassbox-linux-amd64.tar.gz"), []byte("data"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "checksums.sha256"), []byte("abc"), 0644))

	keyPath := generateTestKey(t)
	outPath := filepath.Join(dir, "manifest.json")

	err := run([]string{
		"--dist", dir,
		"--version", "v1.0.0",
		"--commit", strings.Repeat("d", 40),
		"--build-date", "2026-01-01T00:00:00Z",
		"--signing-key", keyPath,
		"--output", outPath,
		"--json-only",
		"--build-source-repository", "https://github.com/dotandev/glassbox",
	})
	require.NoError(t, err)

	// Tamper with build_provenance after signing.
	raw, err := os.ReadFile(outPath)
	require.NoError(t, err)

	tampered := strings.ReplaceAll(
		string(raw),
		`"https://github.com/dotandev/glassbox"`,
		`"https://evil.example.com/tampered"`,
	)
	require.NoError(t, os.WriteFile(outPath, []byte(tampered), 0644))

	var sm manifest.SignedManifest
	require.NoError(t, json.Unmarshal([]byte(tampered), &sm))

	result := manifest.Verify(&sm)
	assert.False(t, result.HashValid, "tampered build_provenance must invalidate manifest_hash")
	assert.False(t, result.Valid, "tampered manifest must not verify")
}
