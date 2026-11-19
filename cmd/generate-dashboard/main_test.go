// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dotandev/glassbox/internal/dashboard"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func mustMarshal(t *testing.T, v interface{}) []byte {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	require.NoError(t, err)
	return b
}

// ─── JUnit ingestion ──────────────────────────────────────────────────────────

const junitAllPass = `<?xml version="1.0" encoding="UTF-8"?>
<testsuites>
  <testsuite name="github.com/dotandev/glassbox/internal/rpc" tests="5" failures="0" errors="0" skipped="0" time="1.250">
    <testcase name="TestProviderPool_SingleProviderSuccess" time="0.100"/>
    <testcase name="TestProviderPool_FailoverToSecondProvider" time="0.200"/>
    <testcase name="TestProviderPool_AllProvidersFail" time="0.300"/>
    <testcase name="TestProviderPool_PinnedModeSuccess" time="0.150"/>
    <testcase name="TestProviderPool_TimeoutIsRetryable" time="0.500"/>
  </testsuite>
</testsuites>`

const junitWithFailure = `<?xml version="1.0" encoding="UTF-8"?>
<testsuites>
  <testsuite name="github.com/dotandev/glassbox/internal/cmd" tests="3" failures="1" errors="0" skipped="1" time="0.850">
    <testcase name="TestDebugPreRunE_CompareNetworkSameAsNetwork" time="0.100"/>
    <testcase name="TestDebugPreRunE_WatchTimeoutZeroRejected" time="0.200">
      <failure message="assertion failed">Expected error, got nil</failure>
    </testcase>
    <testcase name="TestDebugPreRunE_ValidFormatsAccepted" time="0.050">
      <skipped/>
    </testcase>
  </testsuite>
</testsuites>`

func TestAggregator_JUnit_AllPass(t *testing.T) {
	dir := t.TempDir()
	junitPath := filepath.Join(dir, "junit.xml")
	writeFile(t, junitPath, junitAllPass)

	m := dashboard.InputManifest{
		SchemaVersion: "1.0",
		GeneratedAt:   time.Now(),
		Artifacts: []dashboard.ArtifactSource{
			{Path: junitPath, Kind: "junit", Platform: "ubuntu-latest", JobName: "go-unit"},
		},
	}
	agg := dashboard.NewAggregator(m)
	report, err := agg.Aggregate()
	require.NoError(t, err)

	require.Len(t, report.Journeys, 1)
	j := report.Journeys[0]
	assert.Equal(t, dashboard.JourneyStatusPassed, j.Status)
	assert.Equal(t, 5, j.PassCount)
	assert.Equal(t, 0, j.FailCount)
	assert.Equal(t, 0, j.SkipCount)
	assert.InDelta(t, 1250, j.DurationMs, 50)
}

func TestAggregator_JUnit_WithFailure(t *testing.T) {
	dir := t.TempDir()
	junitPath := filepath.Join(dir, "junit.xml")
	writeFile(t, junitPath, junitWithFailure)

	m := dashboard.InputManifest{
		SchemaVersion: "1.0",
		GeneratedAt:   time.Now(),
		Artifacts: []dashboard.ArtifactSource{
			{Path: junitPath, Kind: "junit", Platform: "ubuntu-latest", JobName: "go-unit"},
		},
	}
	agg := dashboard.NewAggregator(m)
	report, err := agg.Aggregate()
	require.NoError(t, err)

	require.Len(t, report.Journeys, 1)
	j := report.Journeys[0]
	assert.Equal(t, dashboard.JourneyStatusFailed, j.Status)
	assert.Equal(t, 1, j.FailCount)
	assert.Equal(t, 1, j.SkipCount)
	assert.True(t, report.FailedJourneys > 0)
	assert.False(t, report.AllPassed)
}

// ─── gotestsum JSON ingestion ─────────────────────────────────────────────────

const gotestsumJSON = `{"Action":"run","Test":"TestFoo"}
{"Action":"pass","Test":"TestFoo","Elapsed":0.100}
{"Action":"run","Test":"TestBar"}
{"Action":"fail","Test":"TestBar","Elapsed":0.200}
{"Action":"run","Test":"TestBaz"}
{"Action":"skip","Test":"TestBaz"}
{"Action":"output","Test":"TestFoo","Output":"secret_token=abc123\n"}
`

func TestAggregator_GotestsumJSON_Parsing(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "test-output.json")
	writeFile(t, jsonPath, gotestsumJSON)

	m := dashboard.InputManifest{
		SchemaVersion: "1.0",
		GeneratedAt:   time.Now(),
		Artifacts: []dashboard.ArtifactSource{
			{Path: jsonPath, Kind: "gotestsum-json", Platform: "ubuntu-latest", JobName: "go-unit"},
		},
	}
	agg := dashboard.NewAggregator(m)
	report, err := agg.Aggregate()
	require.NoError(t, err)

	require.Len(t, report.Journeys, 1)
	j := report.Journeys[0]
	assert.Equal(t, dashboard.JourneyStatusFailed, j.Status)
	assert.Equal(t, 1, j.PassCount)
	assert.Equal(t, 1, j.FailCount)
	assert.Equal(t, 1, j.SkipCount)
}

func TestAggregator_GotestsumJSON_OutputNotStored(t *testing.T) {
	// The output field must NEVER be included in journey records — privacy.
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "test-output.json")
	writeFile(t, jsonPath, gotestsumJSON)

	m := dashboard.InputManifest{
		SchemaVersion: "1.0",
		GeneratedAt:   time.Now(),
		Artifacts: []dashboard.ArtifactSource{
			{Path: jsonPath, Kind: "gotestsum-json", Platform: "ubuntu-latest", JobName: "go-unit"},
		},
	}
	agg := dashboard.NewAggregator(m)
	report, err := agg.Aggregate()
	require.NoError(t, err)

	// Serialise to JSON and verify no test output bodies leak.
	data := mustMarshal(t, report)
	assert.NotContains(t, string(data), "secret_token",
		"test output bodies must not leak into the dashboard report")
}

// ─── validation-summary ingestion ────────────────────────────────────────────

const validationSummaryJSON = `{
  "journeys": [
    {"name": "offline-mode", "status": "passed", "duration_ms": 1200},
    {"name": "protocol-v22", "status": "failed", "duration_ms": 800, "failure_artifact": "/home/runner/ci-artifacts/protocol-v22-failure.log"},
    {"name": "security-providers", "status": "skipped"}
  ]
}`

func TestAggregator_ValidationSummary(t *testing.T) {
	dir := t.TempDir()
	summaryPath := filepath.Join(dir, "validation-summary.json")
	writeFile(t, summaryPath, validationSummaryJSON)

	m := dashboard.InputManifest{
		SchemaVersion: "1.0",
		GeneratedAt:   time.Now(),
		Artifacts: []dashboard.ArtifactSource{
			{Path: summaryPath, Kind: "validation-summary", Platform: "ubuntu-latest"},
		},
	}
	agg := dashboard.NewAggregator(m)
	report, err := agg.Aggregate()
	require.NoError(t, err)

	require.Len(t, report.Journeys, 3)

	statusMap := make(map[string]dashboard.JourneyStatus)
	for _, j := range report.Journeys {
		statusMap[j.Name] = j.Status
	}
	assert.Equal(t, dashboard.JourneyStatusPassed, statusMap["offline-mode"])
	assert.Equal(t, dashboard.JourneyStatusFailed, statusMap["protocol-v22"])
	assert.Equal(t, dashboard.JourneyStatusSkipped, statusMap["security-providers"])
}

func TestAggregator_ValidationSummary_PathRedacted(t *testing.T) {
	dir := t.TempDir()
	summaryPath := filepath.Join(dir, "validation-summary.json")
	writeFile(t, summaryPath, validationSummaryJSON)

	m := dashboard.InputManifest{
		SchemaVersion: "1.0",
		GeneratedAt:   time.Now(),
		Artifacts: []dashboard.ArtifactSource{
			{Path: summaryPath, Kind: "validation-summary", Platform: "ubuntu-latest"},
		},
	}
	agg := dashboard.NewAggregator(m)
	report, err := agg.Aggregate()
	require.NoError(t, err)

	data := mustMarshal(t, report)
	// The full absolute path must not appear in the output.
	assert.NotContains(t, string(data), "/home/runner/",
		"absolute paths must not appear in dashboard output")
	// The basename should be retained.
	assert.Contains(t, string(data), "protocol-v22-failure.log")
}

// ─── critical journeys ────────────────────────────────────────────────────────

func TestAggregator_MissingCriticalJourneyIsUnknown(t *testing.T) {
	m := dashboard.InputManifest{
		SchemaVersion:    "1.0",
		GeneratedAt:      time.Now(),
		Artifacts:        nil, // no artifacts
		CriticalJourneys: []string{"go-unit", "go-integration", "ts-test"},
	}
	agg := dashboard.NewAggregator(m)
	report, err := agg.Aggregate()
	require.NoError(t, err)

	require.Len(t, report.Journeys, 3)
	for _, j := range report.Journeys {
		assert.Equal(t, dashboard.JourneyStatusUnknown, j.Status,
			"journey %q with no artifact data should be 'unknown'", j.Name)
	}
}

// ─── determinism ─────────────────────────────────────────────────────────────

func TestAggregator_DeterministicOutput(t *testing.T) {
	dir := t.TempDir()
	junitPath := filepath.Join(dir, "junit.xml")
	writeFile(t, junitPath, junitAllPass)

	m := dashboard.InputManifest{
		SchemaVersion: "1.0",
		GeneratedAt:   time.Now(),
		Artifacts: []dashboard.ArtifactSource{
			{Path: junitPath, Kind: "junit", Platform: "ubuntu-latest", Commit: "abc123", JobName: "go-unit"},
		},
		CriticalJourneys: []string{"go-unit", "go-integration"},
	}

	agg1 := dashboard.NewAggregator(m)
	r1, err := agg1.Aggregate()
	require.NoError(t, err)

	agg2 := dashboard.NewAggregator(m)
	r2, err := agg2.Aggregate()
	require.NoError(t, err)

	// Journey order and fingerprints must be identical.
	require.Len(t, r1.Journeys, r2.Len())
	for i := range r1.Journeys {
		assert.Equal(t, r1.Journeys[i].Name, r2.Journeys[i].Name)
		assert.Equal(t, r1.Journeys[i].Status, r2.Journeys[i].Status)
		assert.Equal(t, r1.Journeys[i].Fingerprint, r2.Journeys[i].Fingerprint,
			"fingerprint must be stable for journey %q", r1.Journeys[i].Name)
	}
}

// ─── fingerprints ─────────────────────────────────────────────────────────────

func TestAggregator_FingerprintsNonEmpty(t *testing.T) {
	dir := t.TempDir()
	junitPath := filepath.Join(dir, "junit.xml")
	writeFile(t, junitPath, junitAllPass)

	m := dashboard.InputManifest{
		SchemaVersion: "1.0",
		GeneratedAt:   time.Now(),
		Artifacts: []dashboard.ArtifactSource{
			{Path: junitPath, Kind: "junit", Platform: "ubuntu-latest", JobName: "go-unit"},
		},
	}
	agg := dashboard.NewAggregator(m)
	report, err := agg.Aggregate()
	require.NoError(t, err)

	for _, j := range report.Journeys {
		assert.NotEmpty(t, j.Fingerprint, "every journey must have a fingerprint")
		assert.Len(t, j.Fingerprint, 12, "fingerprint must be 12 hex chars")
	}
}

// ─── privacy / redaction ─────────────────────────────────────────────────────

func TestAggregator_SecretsRedacted(t *testing.T) {
	dir := t.TempDir()

	// A validation summary that contains a secret in the failure artifact field.
	summaryWithSecret := `{
  "journeys": [
    {"name": "auth-test", "status": "failed",
     "failure_artifact": "/ci/logs/token=sk_test_abc123def456"}
  ]
}`
	summaryPath := filepath.Join(dir, "validation-summary.json")
	writeFile(t, summaryPath, summaryWithSecret)

	m := dashboard.InputManifest{
		SchemaVersion: "1.0",
		GeneratedAt:   time.Now(),
		Artifacts: []dashboard.ArtifactSource{
			{Path: summaryPath, Kind: "validation-summary", Platform: "ubuntu-latest"},
		},
	}
	agg := dashboard.NewAggregator(m)
	report, err := agg.Aggregate()
	require.NoError(t, err)

	data := mustMarshal(t, report)
	assert.NotContains(t, string(data), "sk_test_abc123def456",
		"secret key patterns must be redacted from the report")
}

// ─── JSON schema validity ────────────────────────────────────────────────────

func TestReport_MarshalValidJSON(t *testing.T) {
	dir := t.TempDir()
	junitPath := filepath.Join(dir, "junit.xml")
	writeFile(t, junitPath, junitAllPass)

	m := dashboard.InputManifest{
		SchemaVersion: "1.0",
		GeneratedAt:   time.Now(),
		Artifacts: []dashboard.ArtifactSource{
			{Path: junitPath, Kind: "junit", Platform: "ubuntu-latest", JobName: "go-unit"},
		},
	}
	agg := dashboard.NewAggregator(m)
	report, err := agg.Aggregate()
	require.NoError(t, err)

	data, err := dashboard.Marshal(report)
	require.NoError(t, err)

	var raw map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &raw), "output must be valid JSON")

	assert.Contains(t, raw, "schema_version")
	assert.Contains(t, raw, "generated_at")
	assert.Contains(t, raw, "journeys")
	assert.Contains(t, raw, "total_journeys")
	assert.Contains(t, raw, "failed_journeys")
	assert.Contains(t, raw, "all_passed")
}

// ─── CLI run() ────────────────────────────────────────────────────────────────

func TestRun_MissingInputFlag(t *testing.T) {
	err := run([]string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--input")
}

func TestRun_InvalidFormat(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "input.json")
	m := dashboard.InputManifest{
		SchemaVersion: "1.0",
		GeneratedAt:   time.Now(),
	}
	writeFile(t, manifestPath, string(mustMarshal(t, m)))

	err := run([]string{"--input", manifestPath, "--format", "xml"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--format")
}

func TestRun_EmptyManifest_WritesReport(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "input.json")
	outputPath := filepath.Join(dir, "report.json")
	m := dashboard.InputManifest{
		SchemaVersion: "1.0",
		GeneratedAt:   time.Now(),
	}
	writeFile(t, manifestPath, string(mustMarshal(t, m)))

	err := run([]string{"--input", manifestPath, "--output", outputPath})
	require.NoError(t, err)

	data, err := os.ReadFile(outputPath)
	require.NoError(t, err)

	var report dashboard.Report
	require.NoError(t, json.Unmarshal(data, &report))
	assert.Equal(t, dashboard.SchemaVersion, report.SchemaVersion)
}

func TestRun_JUnitInput_TextFormat(t *testing.T) {
	dir := t.TempDir()
	junitPath := filepath.Join(dir, "junit.xml")
	writeFile(t, junitPath, junitAllPass)

	m := dashboard.InputManifest{
		SchemaVersion: "1.0",
		GeneratedAt:   time.Now(),
		Artifacts: []dashboard.ArtifactSource{
			{Path: junitPath, Kind: "junit", Platform: "ubuntu-latest", JobName: "go-unit"},
		},
	}
	manifestPath := filepath.Join(dir, "input.json")
	writeFile(t, manifestPath, string(mustMarshal(t, m)))

	// Capture stdout by redirecting.
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := run([]string{"--input", manifestPath, "--output", "-", "--format", "json"})

	_ = w.Close()
	os.Stdout = old

	var buf strings.Builder
	_, _ = buf.ReadFrom(r)
	_ = r.Close()

	require.NoError(t, err)
	assert.Contains(t, buf.String(), `"schema_version"`)
	assert.Contains(t, buf.String(), `"journeys"`)
}

func TestRun_OfflineFlag_MissingArtifact(t *testing.T) {
	dir := t.TempDir()
	m := dashboard.InputManifest{
		SchemaVersion: "1.0",
		GeneratedAt:   time.Now(),
		Artifacts: []dashboard.ArtifactSource{
			{Path: filepath.Join(dir, "nonexistent.xml"), Kind: "junit", Platform: "ubuntu-latest"},
		},
	}
	manifestPath := filepath.Join(dir, "input.json")
	outputPath := filepath.Join(dir, "report.json")
	writeFile(t, manifestPath, string(mustMarshal(t, m)))

	// Without --offline: should fail because the artifact doesn't exist.
	err := run([]string{"--input", manifestPath, "--output", outputPath})
	require.Error(t, err, "missing artifact should fail without --offline")

	// With --offline: should succeed (unknown status).
	err = run([]string{"--input", manifestPath, "--output", outputPath, "--offline"})
	require.NoError(t, err, "--offline should allow missing artifacts")
}

// ─── helpers that require Report's Len() method ──────────────────────────────

// dashboard.Report doesn't have a Len() method — we add a helper here.
func (r dashboard.Report) Len() int { return len(r.Journeys) }
