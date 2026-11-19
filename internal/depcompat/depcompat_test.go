// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package depcompat_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotandev/glassbox/internal/depcompat"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── CompareBytes ─────────────────────────────────────────────────────────────

func TestCompareBytes_NoDiff(t *testing.T) {
	t.Parallel()
	golden := []byte(`{"status":"success","count":3}`)
	actual := []byte(`{"status":"success","count":3}`)
	res := depcompat.CompareBytes(depcompat.DepGroupStellarSDK, depcompat.OutputReplay, golden, actual)
	assert.Equal(t, depcompat.DiffClassNone, res.Class)
	assert.Empty(t, res.Diffs)
	assert.Empty(t, res.Error)
}

func TestCompareBytes_UnexpectedValueChange(t *testing.T) {
	t.Parallel()
	golden := []byte(`{"status":"success","count":3}`)
	actual := []byte(`{"status":"success","count":7}`)
	res := depcompat.CompareBytes(depcompat.DepGroupStellarSDK, depcompat.OutputReplay, golden, actual)
	assert.Equal(t, depcompat.DiffClassUnexpected, res.Class)
	require.Len(t, res.Diffs, 1)
	assert.Equal(t, "$.count", res.Diffs[0].JSONPath)
	assert.Equal(t, depcompat.DiffClassUnexpected, res.Diffs[0].Class)
}

func TestCompareBytes_SchemaVersionChangeIsExpected(t *testing.T) {
	t.Parallel()
	golden := []byte(`{"schema_version":"1","payload":{"value":42}}`)
	actual := []byte(`{"schema_version":"2","payload":{"value":42}}`)
	res := depcompat.CompareBytes(depcompat.DepGroupCrypto, depcompat.OutputAudit, golden, actual)
	assert.Equal(t, depcompat.DiffClassExpected, res.Class)
	require.Len(t, res.Diffs, 1)
	assert.Equal(t, depcompat.DiffClassExpected, res.Diffs[0].Class)
}

func TestCompareBytes_NewFieldIsExpected(t *testing.T) {
	t.Parallel()
	golden := []byte(`{"status":"ok"}`)
	actual := []byte(`{"status":"ok","new_optional_field":null}`)
	res := depcompat.CompareBytes(depcompat.DepGroupSorobanHost, depcompat.OutputTrace, golden, actual)
	assert.Equal(t, depcompat.DiffClassExpected, res.Class)
	require.Len(t, res.Diffs, 1)
	assert.Equal(t, depcompat.DiffClassExpected, res.Diffs[0].Class)
	assert.Contains(t, res.Diffs[0].Reason, "schema addition")
}

func TestCompareBytes_MissingFieldIsUnexpected(t *testing.T) {
	t.Parallel()
	golden := []byte(`{"status":"ok","required_field":"value"}`)
	actual := []byte(`{"status":"ok"}`)
	res := depcompat.CompareBytes(depcompat.DepGroupRPCClient, depcompat.OutputBinding, golden, actual)
	assert.Equal(t, depcompat.DiffClassUnexpected, res.Class)
	require.Len(t, res.Diffs, 1)
	assert.Contains(t, res.Diffs[0].Reason, "missing")
}

func TestCompareBytes_ArrayLengthGrew_Expected(t *testing.T) {
	t.Parallel()
	golden := []byte(`{"events":[{"type":"call"},{"type":"return"}]}`)
	actual := []byte(`{"events":[{"type":"call"},{"type":"return"},{"type":"error"}]}`)
	res := depcompat.CompareBytes(depcompat.DepGroupSorobanHost, depcompat.OutputTrace, golden, actual)
	// Array growth is expected; but we may also have element diffs.
	// At minimum the array-length diff should be expected.
	hasExpectedLen := false
	for _, d := range res.Diffs {
		if strings.Contains(d.JSONPath, "events") && d.Class == depcompat.DiffClassExpected {
			hasExpectedLen = true
		}
	}
	assert.True(t, hasExpectedLen, "array growth should produce an expected diff")
}

func TestCompareBytes_ArrayLengthShrunk_Unexpected(t *testing.T) {
	t.Parallel()
	golden := []byte(`{"events":[{"type":"call"},{"type":"return"},{"type":"error"}]}`)
	actual := []byte(`{"events":[{"type":"call"}]}`)
	res := depcompat.CompareBytes(depcompat.DepGroupSorobanHost, depcompat.OutputTrace, golden, actual)
	assert.Equal(t, depcompat.DiffClassUnexpected, res.Class)
}

func TestCompareBytes_TimestampDiffIsExpected(t *testing.T) {
	t.Parallel()
	golden := []byte(`{"generated_at":"2026-01-01T00:00:00Z","value":1}`)
	actual := []byte(`{"generated_at":"2026-07-01T12:00:00Z","value":1}`)
	res := depcompat.CompareBytes(depcompat.DepGroupStellarSDK, depcompat.OutputAudit, golden, actual)
	assert.Equal(t, depcompat.DiffClassExpected, res.Class)
}

func TestCompareBytes_TypeMismatch_Unexpected(t *testing.T) {
	t.Parallel()
	golden := []byte(`{"value":42}`)
	actual := []byte(`{"value":"42"}`)
	res := depcompat.CompareBytes(depcompat.DepGroupCrypto, depcompat.OutputReplay, golden, actual)
	assert.Equal(t, depcompat.DiffClassUnexpected, res.Class)
	assert.Contains(t, res.Diffs[0].Reason, "type changed")
}

func TestCompareBytes_InvalidJSON(t *testing.T) {
	t.Parallel()
	golden := []byte(`{not json}`)
	actual := []byte(`{"status":"ok"}`)
	res := depcompat.CompareBytes(depcompat.DepGroupStellarSDK, depcompat.OutputReplay, golden, actual)
	assert.NotEmpty(t, res.Error)
	assert.Equal(t, depcompat.DiffClassUnexpected, res.Class)
}

// ─── CompareFiles ─────────────────────────────────────────────────────────────

func TestCompareFiles_MatchingFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	data := []byte(`{"status":"success"}`)
	goldenPath := filepath.Join(dir, "golden.json")
	actualPath := filepath.Join(dir, "actual.json")
	require.NoError(t, os.WriteFile(goldenPath, data, 0o644))
	require.NoError(t, os.WriteFile(actualPath, data, 0o644))

	res := depcompat.CompareFiles(depcompat.DepGroupStellarSDK, depcompat.OutputReplay, goldenPath, actualPath)
	assert.Equal(t, depcompat.DiffClassNone, res.Class)
}

func TestCompareFiles_MissingGolden(t *testing.T) {
	t.Parallel()
	res := depcompat.CompareFiles(
		depcompat.DepGroupStellarSDK, depcompat.OutputReplay,
		"/nonexistent/golden.json", "/nonexistent/actual.json",
	)
	assert.NotEmpty(t, res.Error)
	assert.Equal(t, depcompat.DiffClassUnexpected, res.Class)
}

// ─── CompatReport ─────────────────────────────────────────────────────────────

func TestCompatReport_Finalize(t *testing.T) {
	t.Parallel()
	r := depcompat.NewCompatReport("run-123", depcompat.DepGroupStellarSDK)
	r.AddResult(depcompat.OutputResult{Class: depcompat.DiffClassNone})
	r.AddResult(depcompat.OutputResult{Class: depcompat.DiffClassExpected})
	r.AddResult(depcompat.OutputResult{Class: depcompat.DiffClassUnexpected})
	// Error result: counted in OutputsErrored; the Class is also set to Unexpected
	// but Finalize checks Error != "" first so it goes into ErroredCnt, not UnexpectedCnt.
	r.AddResult(depcompat.OutputResult{Error: "capture failed", Class: depcompat.DiffClassUnexpected})
	r.Finalize()

	assert.Equal(t, 4, r.Summary.TotalOutputs)
	assert.Equal(t, 1, r.Summary.OutputsMatched)
	assert.Equal(t, 1, r.Summary.OutputsExpected)
	// The third result (no error, class=unexpected) increments OutputsUnexpected.
	assert.Equal(t, 1, r.Summary.OutputsUnexpected)
	// The fourth result (has error) increments OutputsErrored, not OutputsUnexpected.
	assert.Equal(t, 1, r.Summary.OutputsErrored)
	assert.True(t, r.Summary.HasUnexpectedDiffs)
	assert.True(t, r.Summary.HasErrors)
}

func TestCompatReport_ToJSON(t *testing.T) {
	t.Parallel()
	r := depcompat.NewCompatReport("run-456", "")
	r.Finalize()
	data, err := r.ToJSON()
	require.NoError(t, err)
	assert.Contains(t, string(data), `"schema_version"`)
	assert.Contains(t, string(data), `"run_id"`)
	assert.Contains(t, string(data), "run-456")
}

// ─── RenderText ───────────────────────────────────────────────────────────────

func TestRenderText(t *testing.T) {
	t.Parallel()
	r := depcompat.NewCompatReport("run-789", depcompat.DepGroupSorobanHost)
	r.AddResult(depcompat.OutputResult{
		DepGroup:   depcompat.DepGroupSorobanHost,
		OutputKind: depcompat.OutputTrace,
		Class:      depcompat.DiffClassNone,
	})
	r.AddResult(depcompat.OutputResult{
		DepGroup:   depcompat.DepGroupSorobanHost,
		OutputKind: depcompat.OutputReplay,
		Class:      depcompat.DiffClassUnexpected,
		Diffs: []depcompat.FieldDiff{
			{JSONPath: "$.value", GoldenValue: "1", ActualValue: "2", Class: depcompat.DiffClassUnexpected, Reason: "test"},
		},
	})
	r.Finalize()

	var buf bytes.Buffer
	err := depcompat.RenderText(r, &buf)
	require.NoError(t, err)
	out := buf.String()
	assert.Contains(t, out, "run-789")
	assert.Contains(t, out, "soroban-host")
	assert.Contains(t, out, "FAIL")
}

// ─── RenderMarkdown ───────────────────────────────────────────────────────────

func TestRenderMarkdown(t *testing.T) {
	t.Parallel()
	r := depcompat.NewCompatReport("run-md", depcompat.DepGroupCrypto)
	r.AddResult(depcompat.OutputResult{
		DepGroup:   depcompat.DepGroupCrypto,
		OutputKind: depcompat.OutputAudit,
		Class:      depcompat.DiffClassExpected,
		Diffs: []depcompat.FieldDiff{
			{JSONPath: "$.schema_version", GoldenValue: `"1"`, ActualValue: `"2"`, Class: depcompat.DiffClassExpected},
		},
	})
	r.Finalize()

	var buf bytes.Buffer
	err := depcompat.RenderMarkdown(r, &buf)
	require.NoError(t, err)
	out := buf.String()
	assert.Contains(t, out, "## Dependency Compatibility Report")
	assert.Contains(t, out, "WARN")
	assert.Contains(t, out, "crypto")
}

// ─── WriteGolden / ReadGolden ─────────────────────────────────────────────────

func TestWriteAndReadGolden(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	data := []byte(`{"test":true}`)
	err := depcompat.WriteGolden(dir, depcompat.DepGroupCrypto, depcompat.OutputAudit, data)
	require.NoError(t, err)

	read, err := depcompat.ReadGolden(dir, depcompat.DepGroupCrypto, depcompat.OutputAudit)
	require.NoError(t, err)
	assert.Equal(t, data, read)
}

func TestGoldenExists(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	assert.False(t, depcompat.GoldenExists(dir, depcompat.DepGroupStellarSDK, depcompat.OutputReplay))
	require.NoError(t, depcompat.WriteGolden(dir, depcompat.DepGroupStellarSDK, depcompat.OutputReplay, []byte(`{}`)))
	assert.True(t, depcompat.GoldenExists(dir, depcompat.DepGroupStellarSDK, depcompat.OutputReplay))
}

func TestGoldenFileName(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "stellar-sdk-replay.golden.json", depcompat.GoldenFileName(depcompat.DepGroupStellarSDK, depcompat.OutputReplay))
	assert.Equal(t, "soroban-host-trace.golden.json", depcompat.GoldenFileName(depcompat.DepGroupSorobanHost, depcompat.OutputTrace))
	assert.Equal(t, "crypto-audit.golden.json", depcompat.GoldenFileName(depcompat.DepGroupCrypto, depcompat.OutputAudit))
	assert.Equal(t, "rpc-client-binding.golden.json", depcompat.GoldenFileName(depcompat.DepGroupRPCClient, depcompat.OutputBinding))
}
