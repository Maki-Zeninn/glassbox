// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotandev/glassbox/internal/sbom"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// writeFixture writes content to a temporary file inside dir and returns the path.
func writeFixture(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
	return path
}

// sampleGoModulesJSON is a minimal go list -m -json all output for testing.
const sampleGoModulesJSON = `
{"Path":"github.com/stretchr/testify","Version":"v1.11.1","Main":false}
{"Path":"github.com/fatih/color","Version":"v1.19.0","Main":false}
{"Path":"github.com/dotandev/glassbox","Version":"","Main":true}
`

// sampleCargoLock is a minimal Cargo.lock for testing.
const sampleCargoLock = `
version = 4

[[package]]
name = "serde"
version = "1.0.210"
source = "registry+https://github.com/rust-lang/crates.io-index"
checksum = "sha256:abcdef0000000000000000000000000000000000000000000000000000000000"

[[package]]
name = "simulator"
version = "0.1.0"
`

// samplePackageLockJSON is a minimal package-lock.json v2 for testing.
const samplePackageLockJSON = `{
  "name": "glassbox",
  "lockfileVersion": 2,
  "packages": {
    "": { "name": "glassbox", "version": "0.0.1" },
    "node_modules/chalk": { "version": "4.1.2" },
    "node_modules/commander": { "version": "14.0.2" }
  }
}`

// ─── flag validation ──────────────────────────────────────────────────────────

func TestRun_MissingRequiredFlags(t *testing.T) {
	err := run([]string{"--go-modules", "/dev/null"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--version")
	assert.Contains(t, err.Error(), "--commit")
}

func TestRun_MissingEcosystemInput(t *testing.T) {
	err := run([]string{
		"--version", "v1.0.0",
		"--commit", strings.Repeat("a", 40),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one")
}

// ─── end-to-end runs ──────────────────────────────────────────────────────────

func TestRun_GoModulesOnly(t *testing.T) {
	dir := t.TempDir()
	goModPath := writeFixture(t, dir, "go-modules.json", sampleGoModulesJSON)
	outPath := filepath.Join(dir, "sbom.spdx.json")

	err := run([]string{
		"--version", "v1.0.0",
		"--commit", strings.Repeat("b", 40),
		"--go-modules", goModPath,
		"--output", outPath,
		"--verify",
		"--json-only",
	})
	require.NoError(t, err)

	raw, err := os.ReadFile(outPath)
	require.NoError(t, err)

	var doc sbom.SPDXDocument
	require.NoError(t, json.Unmarshal(raw, &doc))
	assert.Equal(t, "SPDX-2.3", doc.SPDXVersion)
	assert.Equal(t, "CC0-1.0", doc.DataLicense)
	// Should have root package + 2 go deps.
	assert.Equal(t, 3, len(doc.Packages))
	require.NoError(t, sbom.Validate(&doc))
}

func TestRun_AllThreeEcosystems(t *testing.T) {
	dir := t.TempDir()
	goModPath := writeFixture(t, dir, "go-modules.json", sampleGoModulesJSON)
	cargoPath := writeFixture(t, dir, "Cargo.lock", sampleCargoLock)
	npmPath := writeFixture(t, dir, "package-lock.json", samplePackageLockJSON)
	outPath := filepath.Join(dir, "glassbox-v1.0.0.spdx.json")

	err := run([]string{
		"--version", "v1.0.0",
		"--commit", strings.Repeat("c", 40),
		"--go-modules", goModPath,
		"--cargo-lock", cargoPath,
		"--package-lock", npmPath,
		"--output", outPath,
		"--verify",
		"--json-only",
	})
	require.NoError(t, err)

	raw, err := os.ReadFile(outPath)
	require.NoError(t, err)

	var doc sbom.SPDXDocument
	require.NoError(t, json.Unmarshal(raw, &doc))

	// Root package + 2 go + 1 cargo (serde; simulator is local) + 2 npm.
	assert.Equal(t, 6, len(doc.Packages))

	// Verify all three ecosystems are represented via PURLs.
	var purls []string
	for _, pkg := range doc.Packages {
		for _, ref := range pkg.ExternalRefs {
			if ref.ReferenceType == "purl" {
				purls = append(purls, ref.ReferenceLocator)
			}
		}
	}
	hasGo, hasCargo, hasNPM := false, false, false
	for _, p := range purls {
		if strings.HasPrefix(p, "pkg:golang/") {
			hasGo = true
		}
		if strings.HasPrefix(p, "pkg:cargo/") {
			hasCargo = true
		}
		if strings.HasPrefix(p, "pkg:npm/") {
			hasNPM = true
		}
	}
	assert.True(t, hasGo, "expected Go components in SBOM")
	assert.True(t, hasCargo, "expected Cargo components in SBOM")
	assert.True(t, hasNPM, "expected npm components in SBOM")
}

func TestRun_MissingOptionalFileIsWarning(t *testing.T) {
	dir := t.TempDir()
	goModPath := writeFixture(t, dir, "go-modules.json", sampleGoModulesJSON)
	outPath := filepath.Join(dir, "sbom.spdx.json")

	// Non-existent Cargo.lock should produce a warning but not fail.
	err := run([]string{
		"--version", "v1.0.0",
		"--commit", strings.Repeat("d", 40),
		"--go-modules", goModPath,
		"--cargo-lock", filepath.Join(dir, "nonexistent", "Cargo.lock"),
		"--output", outPath,
		"--json-only",
	})
	require.NoError(t, err, "missing optional file must not fail")
}

func TestRun_VersionMatchesLockfile(t *testing.T) {
	dir := t.TempDir()
	goModPath := writeFixture(t, dir, "go-modules.json", sampleGoModulesJSON)
	outPath := filepath.Join(dir, "sbom.spdx.json")

	err := run([]string{
		"--version", "v2.3.4",
		"--commit", strings.Repeat("e", 40),
		"--go-modules", goModPath,
		"--output", outPath,
		"--json-only",
	})
	require.NoError(t, err)

	raw, _ := os.ReadFile(outPath)
	var doc sbom.SPDXDocument
	require.NoError(t, json.Unmarshal(raw, &doc))

	// Find testify package and verify version matches the lockfile input exactly.
	found := false
	for _, pkg := range doc.Packages {
		if pkg.Name == "github.com/stretchr/testify" {
			assert.Equal(t, "v1.11.1", pkg.Version,
				"package version must match lockfile exactly")
			found = true
		}
	}
	assert.True(t, found, "testify must be in the SBOM")
}

func TestRun_DocumentHashIsReproducible(t *testing.T) {
	dir := t.TempDir()
	goModPath := writeFixture(t, dir, "go-modules.json", sampleGoModulesJSON)

	fixedTime := "2026-01-01T00:00:00Z"
	_ = fixedTime // GenerateDocument uses ctx.GeneratedAt; CLI uses time.Now()
	// Two runs with the same inputs must produce the same DocumentHash because
	// it is computed over the packages slice, not over the timestamp.
	out1 := filepath.Join(dir, "sbom1.spdx.json")
	out2 := filepath.Join(dir, "sbom2.spdx.json")
	args := func(out string) []string {
		return []string{
			"--version", "v1.0.0",
			"--commit", strings.Repeat("f", 40),
			"--go-modules", goModPath,
			"--output", out,
			"--json-only",
		}
	}
	require.NoError(t, run(args(out1)))
	require.NoError(t, run(args(out2)))

	var doc1, doc2 sbom.SPDXDocument
	raw1, _ := os.ReadFile(out1)
	raw2, _ := os.ReadFile(out2)
	require.NoError(t, json.Unmarshal(raw1, &doc1))
	require.NoError(t, json.Unmarshal(raw2, &doc2))

	assert.Equal(t, doc1.DocumentHash, doc2.DocumentHash,
		"DocumentHash must be reproducible across identical runs")
}

// TestRun_SBOMRefIntegration verifies the SBOM filename can be captured and
// forwarded as --sbom-ref to generate-release-manifest (pipeline integration).
func TestRun_SBOMRefIntegration(t *testing.T) {
	dir := t.TempDir()
	goModPath := writeFixture(t, dir, "go-modules.json", sampleGoModulesJSON)
	sbomOut := filepath.Join(dir, "glassbox-v1.0.0.spdx.json")

	err := run([]string{
		"--version", "v1.0.0",
		"--commit", strings.Repeat("a", 40),
		"--go-modules", goModPath,
		"--output", sbomOut,
		"--json-only",
	})
	require.NoError(t, err)

	// The output file must exist and be valid SPDX JSON — this is what
	// generate-release-manifest picks up via --sbom-ref.
	_, err = os.Stat(sbomOut)
	require.NoError(t, err, "SBOM output file must exist for manifest pipeline")

	raw, _ := os.ReadFile(sbomOut)
	var doc sbom.SPDXDocument
	require.NoError(t, json.Unmarshal(raw, &doc))
	assert.NotEmpty(t, doc.DocumentHash, "DocumentHash must be set for manifest reference")
}
