// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package main_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// repoRoot returns the repository root by walking up from this file.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	dir := filepath.Dir(file)
	// Walk up to find go.mod
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root")
		}
		dir = parent
	}
}

func hasBash(t *testing.T) bool {
	t.Helper()
	_, err := exec.LookPath("bash")
	return err == nil
}

func hasPython3(t *testing.T) bool {
	t.Helper()
	_, err := exec.LookPath("python3")
	return err == nil
}

func runSBOMDiff(t *testing.T, root, oldSBOM, newSBOM string, extraArgs ...string) (stdout string, exitCode int) {
	t.Helper()
	args := append([]string{
		filepath.Join(root, "scripts", "sbom-diff.sh"),
		oldSBOM, newSBOM,
		"--policy", filepath.Join(root, "license-policy.json"),
	}, extraArgs...)
	cmd := exec.Command("bash", args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	stdout = string(out)
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}
	return
}

func TestSBOMDiff_AdditionAndRemoval(t *testing.T) {
	if !hasBash(t) || !hasPython3(t) {
		t.Skip("bash and python3 required for SBOM diff script tests")
	}
	root := repoRoot(t)
	testdata := filepath.Join(root, "cmd", "generate-sbom", "testdata")

	out, code := runSBOMDiff(t,
		root,
		filepath.Join(testdata, "sbom-old.spdx.json"),
		filepath.Join(testdata, "sbom-new.spdx.json"),
	)

	// Exit 0 — additions and removals are not failures by default (only
	// policy violations trigger exit 1).
	assert.Equal(t, 0, code, "non-policy diff should exit 0; output:\n%s", out)
	assert.Contains(t, out, "github.com/new/dep", "added dep must appear in report")
	assert.Contains(t, out, "github.com/old/dep", "removed dep must appear in report")
}

func TestSBOMDiff_RemovalNotReportedAsAddition(t *testing.T) {
	if !hasBash(t) || !hasPython3(t) {
		t.Skip("bash and python3 required")
	}
	root := repoRoot(t)
	testdata := filepath.Join(root, "cmd", "generate-sbom", "testdata")

	out, _ := runSBOMDiff(t,
		root,
		filepath.Join(testdata, "sbom-old.spdx.json"),
		filepath.Join(testdata, "sbom-new.spdx.json"),
	)

	// The removed dep must appear under "Removed components", not "Added".
	// We look for the "-" prefix marker in text output.
	assert.Contains(t, out, "github.com/old/dep")
	// Critically, old/dep must NOT appear in the additions section.
	// The additions line starts with "+" and new/dep starts with "+".
	assert.Contains(t, out, "github.com/new/dep")
}

func TestSBOMDiff_VersionChange(t *testing.T) {
	if !hasBash(t) || !hasPython3(t) {
		t.Skip("bash and python3 required")
	}
	root := repoRoot(t)
	testdata := filepath.Join(root, "cmd", "generate-sbom", "testdata")

	out, code := runSBOMDiff(t,
		root,
		filepath.Join(testdata, "sbom-old.spdx.json"),
		filepath.Join(testdata, "sbom-new.spdx.json"),
	)

	assert.Equal(t, 0, code)
	// shared/lib was upgraded v1.5.0 -> v1.6.0
	assert.Contains(t, out, "shared/lib")
	assert.Contains(t, out, "v1.5.0")
	assert.Contains(t, out, "v1.6.0")
}

func TestSBOMDiff_ProhibitedLicenseFails(t *testing.T) {
	if !hasBash(t) || !hasPython3(t) {
		t.Skip("bash and python3 required")
	}
	root := repoRoot(t)
	testdata := filepath.Join(root, "cmd", "generate-sbom", "testdata")

	out, code := runSBOMDiff(t,
		root,
		filepath.Join(testdata, "sbom-old.spdx.json"),
		filepath.Join(testdata, "sbom-prohibited-license.spdx.json"),
	)

	assert.Equal(t, 1, code, "prohibited license must exit 1; output:\n%s", out)
	assert.Contains(t, out, "GPL-3.0")
}

func TestSBOMDiff_JSONOutput(t *testing.T) {
	if !hasBash(t) || !hasPython3(t) {
		t.Skip("bash and python3 required")
	}
	root := repoRoot(t)
	testdata := filepath.Join(root, "cmd", "generate-sbom", "testdata")

	outFile := filepath.Join(t.TempDir(), "diff.json")
	out, _ := runSBOMDiff(t,
		root,
		filepath.Join(testdata, "sbom-old.spdx.json"),
		filepath.Join(testdata, "sbom-new.spdx.json"),
		"--output", outFile,
		"--format", "json",
	)
	_ = out

	raw, err := os.ReadFile(outFile)
	require.NoError(t, err, "output report file must be written")

	var report map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &report), "report must be valid JSON")

	summary, ok := report["summary"].(map[string]interface{})
	require.True(t, ok, "report must have summary")

	added := summary["added"].(float64)
	removed := summary["removed"].(float64)
	assert.Equal(t, float64(1), added, "1 component added")
	assert.Equal(t, float64(1), removed, "1 component removed")
}

func TestSBOMDiff_SourcePackageAndVersionInReport(t *testing.T) {
	if !hasBash(t) || !hasPython3(t) {
		t.Skip("bash and python3 required")
	}
	root := repoRoot(t)
	testdata := filepath.Join(root, "cmd", "generate-sbom", "testdata")

	outFile := filepath.Join(t.TempDir(), "diff.json")
	_, _ = runSBOMDiff(t,
		root,
		filepath.Join(testdata, "sbom-old.spdx.json"),
		filepath.Join(testdata, "sbom-new.spdx.json"),
		"--output", outFile,
		"--format", "json",
	)

	raw, err := os.ReadFile(outFile)
	require.NoError(t, err)
	var report map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &report))

	// Each added/removed entry must contain name and version.
	for _, section := range []string{"added", "removed"} {
		entries, ok := report[section].([]interface{})
		require.True(t, ok, "report.%s must be a list", section)
		for _, raw := range entries {
			entry := raw.(map[string]interface{})
			assert.NotEmpty(t, entry["name"], "entry in %s must have name", section)
			assert.NotEmpty(t, entry["version"], "entry in %s must have version", section)
		}
	}
}
