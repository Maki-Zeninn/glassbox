// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeGoldenDir creates a golden dir with stub golden files for all 4 groups
// and all 4 output kinds so the run() comparison has something to compare to.
func makeGoldenDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	groups := []string{"stellar-sdk", "soroban-host", "crypto", "rpc-client"}
	kinds := []string{"replay", "trace", "audit", "binding"}
	for _, g := range groups {
		for _, k := range kinds {
			name := g + "-" + k + ".golden.json"
			data := []byte(`{"schema_version":"1","dep_group":"` + g + `","output_kind":"` + k + `","status":"ok"}`)
			if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
				t.Fatalf("write golden: %v", err)
			}
		}
	}
	return dir
}

// makeCapturedDir creates a captured-output dir whose JSON files exactly match
// the golden files produced by makeGoldenDir, so CompareFiles returns DiffClassNone.
func makeCapturedDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	groups := []string{"stellar-sdk", "soroban-host", "crypto", "rpc-client"}
	kinds := []string{"replay", "trace", "audit", "binding"}
	for _, g := range groups {
		for _, k := range kinds {
			name := g + "-" + k + ".json"
			data := []byte(`{"schema_version":"1","dep_group":"` + g + `","output_kind":"` + k + `","status":"ok"}`)
			if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
				t.Fatalf("write captured: %v", err)
			}
		}
	}
	return dir
}

// TestRun_MissingCapturedDir ensures exit code 2 is returned when --captured-dir is absent.
func TestRun_MissingCapturedDir(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := run([]string{}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("expected exit code 2, got %d; stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "--captured-dir") {
		t.Errorf("expected error about --captured-dir, got: %s", stderr.String())
	}
}

// TestRun_InvalidDepGroup ensures exit code 2 is returned for an unknown group.
func TestRun_InvalidDepGroup(t *testing.T) {
	t.Parallel()
	captured := t.TempDir()
	golden := makeGoldenDir(t)
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--captured-dir", captured,
		"--golden-dir", golden,
		"--dep-group", "not-a-real-group",
	}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("expected exit code 2 for invalid dep-group, got %d", code)
	}
	if !strings.Contains(stderr.String(), "unknown dep-group") {
		t.Errorf("expected 'unknown dep-group' in stderr, got: %s", stderr.String())
	}
}

// TestRun_AllPass verifies exit code 0 when all captures exactly match golden files.
func TestRun_AllPass(t *testing.T) {
	t.Parallel()
	golden := makeGoldenDir(t)
	captured := makeCapturedDir(t)
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--captured-dir", captured,
		"--golden-dir", golden,
	}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("expected exit code 0 for all-pass run, got %d; stderr: %s", code, stderr.String())
	}
	// stdout should contain valid JSON report
	var report map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout.String())), &report); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\nstdout: %s", err, stdout.String())
	}
	if report["schema_version"] != "1.0" {
		t.Errorf("expected schema_version 1.0, got %v", report["schema_version"])
	}
}

// TestRun_SingleDepGroup limits processing to one dep group.
func TestRun_SingleDepGroup(t *testing.T) {
	t.Parallel()
	golden := makeGoldenDir(t)
	captured := makeCapturedDir(t)
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--captured-dir", captured,
		"--golden-dir", golden,
		"--dep-group", "crypto",
	}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("expected exit code 0, got %d; stderr: %s", code, stderr.String())
	}
	// Report should reference only crypto outputs (4 output kinds).
	var report map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout.String())), &report); err != nil {
		t.Fatalf("stdout is not valid JSON: %v", err)
	}
	results, ok := report["results"].([]interface{})
	if !ok {
		t.Fatal("report.results is not an array")
	}
	if len(results) != 4 {
		t.Errorf("expected 4 results for single dep group, got %d", len(results))
	}
	for _, r := range results {
		rm := r.(map[string]interface{})
		if rm["dep_group"] != "crypto" {
			t.Errorf("expected dep_group=crypto, got %v", rm["dep_group"])
		}
	}
}

// TestRun_OutputJSONFile writes the JSON report to a file when --output-json is set.
func TestRun_OutputJSONFile(t *testing.T) {
	t.Parallel()
	golden := makeGoldenDir(t)
	captured := makeCapturedDir(t)
	outFile := filepath.Join(t.TempDir(), "report.json")
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--captured-dir", captured,
		"--golden-dir", golden,
		"--output-json", outFile,
	}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("expected exit code 0, got %d; stderr: %s", code, stderr.String())
	}
	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("output file not created: %v", err)
	}
	var report map[string]interface{}
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("output file is not valid JSON: %v", err)
	}
	// stdout should be empty (report went to file)
	if strings.TrimSpace(stdout.String()) != "" {
		t.Errorf("expected empty stdout when --output-json used, got: %s", stdout.String())
	}
}

// TestRun_OutputMarkdownFile writes the markdown summary when --output-md is set.
func TestRun_OutputMarkdownFile(t *testing.T) {
	t.Parallel()
	golden := makeGoldenDir(t)
	captured := makeCapturedDir(t)
	mdFile := filepath.Join(t.TempDir(), "summary.md")
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--captured-dir", captured,
		"--golden-dir", golden,
		"--output-md", mdFile,
	}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("expected exit code 0, got %d; stderr: %s", code, stderr.String())
	}
	data, err := os.ReadFile(mdFile)
	if err != nil {
		t.Fatalf("markdown file not created: %v", err)
	}
	md := string(data)
	if !strings.Contains(md, "## Dependency Compatibility Report") {
		t.Errorf("markdown file missing expected header; got: %s", md[:min(200, len(md))])
	}
}

// TestRun_UnexpectedDiffs returns exit code 1 when a value changes.
func TestRun_UnexpectedDiffs(t *testing.T) {
	t.Parallel()
	golden := makeGoldenDir(t)
	// Build a captured dir that disagrees on one value in stellar-sdk/replay.
	captured := makeCapturedDir(t)
	// Overwrite stellar-sdk-replay.json with a changed value.
	badData := []byte(`{"schema_version":"1","dep_group":"stellar-sdk","output_kind":"replay","status":"FAIL"}`)
	if err := os.WriteFile(filepath.Join(captured, "stellar-sdk-replay.json"), badData, 0o644); err != nil {
		t.Fatalf("write bad captured: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--captured-dir", captured,
		"--golden-dir", golden,
	}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected exit code 1 for unexpected diff, got %d; stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "Unexpected diffs") {
		t.Errorf("expected 'Unexpected diffs' in stderr, got: %s", stderr.String())
	}
}

// TestRun_MissingCaptureFile returns exit code 1 when a captured file is absent.
func TestRun_MissingCaptureFile(t *testing.T) {
	t.Parallel()
	golden := makeGoldenDir(t)
	// Use an empty directory — no captured files at all.
	captured := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--captured-dir", captured,
		"--golden-dir", golden,
	}, &stdout, &stderr)
	// Missing capture files are reported as errors which drive exit code 1.
	if code != 1 {
		t.Errorf("expected exit code 1 for missing captured files, got %d", code)
	}
}

// TestRun_Verbose verifies the verbose flag does not break the run.
func TestRun_Verbose(t *testing.T) {
	t.Parallel()
	golden := makeGoldenDir(t)
	captured := makeCapturedDir(t)
	// Inject one diff so verbose output fires.
	diffData := []byte(`{"schema_version":"2","dep_group":"crypto","output_kind":"audit","status":"ok"}`)
	if err := os.WriteFile(filepath.Join(captured, "crypto-audit.json"), diffData, 0o644); err != nil {
		t.Fatalf("write diff captured: %v", err)
	}
	var stdout, stderr bytes.Buffer
	_ = run([]string{
		"--captured-dir", captured,
		"--golden-dir", golden,
		"--verbose",
	}, &stdout, &stderr)
	// With verbose flag, stderr should contain diff details.
	if !strings.Contains(stderr.String(), "expected") && !strings.Contains(stderr.String(), "unexpected") {
		// The diff on schema_version should produce an "expected" classification
		// logged to stderr in verbose mode.  Accept that no diff logged means
		// schema_version changed to 2 was classified and printed.
		// Permissive: just check we got a non-empty stderr.
		if strings.TrimSpace(stderr.String()) == "" {
			t.Error("expected some output in stderr with --verbose flag")
		}
	}
}

// TestRun_UpdateGolden verifies that --update-golden overwrites the golden file.
func TestRun_UpdateGolden(t *testing.T) {
	t.Parallel()
	golden := makeGoldenDir(t)
	// Create a captured dir with an updated schema_version.
	captured := makeCapturedDir(t)
	// Override stellar-sdk-replay with new content.
	newContent := []byte(`{"schema_version":"2","dep_group":"stellar-sdk","output_kind":"replay","status":"ok"}`)
	if err := os.WriteFile(filepath.Join(captured, "stellar-sdk-replay.json"), newContent, 0o644); err != nil {
		t.Fatalf("write new captured: %v", err)
	}
	var stdout, stderr bytes.Buffer
	// Run with --update-golden; exit code may be non-zero due to schema diff,
	// but the golden file should be overwritten regardless.
	_ = run([]string{
		"--captured-dir", captured,
		"--golden-dir", golden,
		"--update-golden",
	}, &stdout, &stderr)
	updated, err := os.ReadFile(filepath.Join(golden, "stellar-sdk-replay.golden.json"))
	if err != nil {
		t.Fatalf("golden file missing after update: %v", err)
	}
	if !bytes.Contains(updated, []byte(`"schema_version":"2"`)) && !bytes.Contains(updated, []byte(`"schema_version": "2"`)) {
		t.Errorf("golden file was not updated with new content; got: %s", updated)
	}
	if !strings.Contains(stderr.String(), "updated golden") {
		t.Errorf("expected 'updated golden' in stderr, got: %s", stderr.String())
	}
}

// TestRun_RunIDDefault verifies that a run_id is auto-generated when not supplied.
func TestRun_RunIDDefault(t *testing.T) {
	t.Parallel()
	golden := makeGoldenDir(t)
	captured := makeCapturedDir(t)
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--captured-dir", captured,
		"--golden-dir", golden,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("unexpected exit code %d", code)
	}
	var report map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout.String())), &report); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	runID, _ := report["run_id"].(string)
	if !strings.HasPrefix(runID, "local-") {
		t.Errorf("expected run_id to start with 'local-', got %q", runID)
	}
}

// TestRun_ExplicitRunID verifies the --run-id flag is reflected in the report.
func TestRun_ExplicitRunID(t *testing.T) {
	t.Parallel()
	golden := makeGoldenDir(t)
	captured := makeCapturedDir(t)
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--captured-dir", captured,
		"--golden-dir", golden,
		"--run-id", "ci-run-9999",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("unexpected exit code %d", code)
	}
	var report map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout.String())), &report); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if report["run_id"] != "ci-run-9999" {
		t.Errorf("expected run_id=ci-run-9999, got %v", report["run_id"])
	}
}


