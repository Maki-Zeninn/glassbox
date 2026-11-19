// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package depcompat_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/dotandev/glassbox/internal/depcompat"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── markdownBadge (via RenderMarkdown) ───────────────────────────────────────

// TestRenderMarkdown_BadgePass verifies the green PASS badge is emitted when
// there are no diffs and no errors.
func TestRenderMarkdown_BadgePass(t *testing.T) {
	t.Parallel()
	r := depcompat.NewCompatReport("run-pass", depcompat.DepGroupStellarSDK)
	r.AddResult(depcompat.OutputResult{
		DepGroup:   depcompat.DepGroupStellarSDK,
		OutputKind: depcompat.OutputReplay,
		Class:      depcompat.DiffClassNone,
	})
	r.Finalize()

	var buf bytes.Buffer
	require.NoError(t, depcompat.RenderMarkdown(r, &buf))
	out := buf.String()
	assert.Contains(t, out, "PASS", "expected PASS badge for all-matching output")
	assert.NotContains(t, out, "FAIL", "should not contain FAIL badge")
	assert.NotContains(t, out, "WARN", "should not contain WARN badge")
}

// TestRenderMarkdown_BadgeWarn verifies the yellow WARN badge when only
// expected (schema) diffs exist.
func TestRenderMarkdown_BadgeWarn(t *testing.T) {
	t.Parallel()
	r := depcompat.NewCompatReport("run-warn", depcompat.DepGroupCrypto)
	r.AddResult(depcompat.OutputResult{
		DepGroup:   depcompat.DepGroupCrypto,
		OutputKind: depcompat.OutputAudit,
		Class:      depcompat.DiffClassExpected,
		Diffs: []depcompat.FieldDiff{
			{JSONPath: "$.schema_version", Class: depcompat.DiffClassExpected, Reason: "schema version bump"},
		},
	})
	r.Finalize()

	var buf bytes.Buffer
	require.NoError(t, depcompat.RenderMarkdown(r, &buf))
	out := buf.String()
	assert.Contains(t, out, "WARN", "expected WARN badge for expected diffs only")
	assert.NotContains(t, out, "FAIL", "should not contain FAIL badge")
}

// TestRenderMarkdown_BadgeFail verifies the red FAIL badge when unexpected
// diffs exist.
func TestRenderMarkdown_BadgeFail(t *testing.T) {
	t.Parallel()
	r := depcompat.NewCompatReport("run-fail", depcompat.DepGroupSorobanHost)
	r.AddResult(depcompat.OutputResult{
		DepGroup:   depcompat.DepGroupSorobanHost,
		OutputKind: depcompat.OutputTrace,
		Class:      depcompat.DiffClassUnexpected,
		Diffs: []depcompat.FieldDiff{
			{JSONPath: "$.value", GoldenValue: "1", ActualValue: "2", Class: depcompat.DiffClassUnexpected},
		},
	})
	r.Finalize()

	var buf bytes.Buffer
	require.NoError(t, depcompat.RenderMarkdown(r, &buf))
	out := buf.String()
	assert.Contains(t, out, "FAIL", "expected FAIL badge for unexpected diffs")
}

// TestRenderMarkdown_BadgeFailOnError verifies the FAIL badge is also
// emitted when the report has errors (even without explicit diffs).
func TestRenderMarkdown_BadgeFailOnError(t *testing.T) {
	t.Parallel()
	r := depcompat.NewCompatReport("run-error", depcompat.DepGroupRPCClient)
	r.AddResult(depcompat.OutputResult{
		DepGroup:   depcompat.DepGroupRPCClient,
		OutputKind: depcompat.OutputBinding,
		Class:      depcompat.DiffClassUnexpected,
		Error:      "capture tool exited 1",
	})
	r.Finalize()

	var buf bytes.Buffer
	require.NoError(t, depcompat.RenderMarkdown(r, &buf))
	out := buf.String()
	assert.Contains(t, out, "FAIL", "error result should trigger FAIL badge")
	assert.Contains(t, out, "ERROR", "ERROR status should appear in the results table")
}

// ─── markdownStatus (via RenderMarkdown results table) ────────────────────────

// TestRenderMarkdown_StatusPASS verifies the PASS status row for a matching result.
func TestRenderMarkdown_StatusPASS(t *testing.T) {
	t.Parallel()
	r := depcompat.NewCompatReport("run-status-pass", "")
	r.AddResult(depcompat.OutputResult{
		DepGroup:   depcompat.DepGroupStellarSDK,
		OutputKind: depcompat.OutputReplay,
		Class:      depcompat.DiffClassNone,
	})
	r.Finalize()

	var buf bytes.Buffer
	require.NoError(t, depcompat.RenderMarkdown(r, &buf))
	out := buf.String()
	assert.Contains(t, out, ":white_check_mark:", "PASS result should show checkmark emoji")
}

// TestRenderMarkdown_StatusEXPECTED verifies the EXPECTED status row.
func TestRenderMarkdown_StatusEXPECTED(t *testing.T) {
	t.Parallel()
	r := depcompat.NewCompatReport("run-status-exp", "")
	r.AddResult(depcompat.OutputResult{
		DepGroup:   depcompat.DepGroupStellarSDK,
		OutputKind: depcompat.OutputReplay,
		Class:      depcompat.DiffClassExpected,
		Diffs: []depcompat.FieldDiff{
			{JSONPath: "$.schema_version", Class: depcompat.DiffClassExpected},
		},
	})
	r.Finalize()

	var buf bytes.Buffer
	require.NoError(t, depcompat.RenderMarkdown(r, &buf))
	out := buf.String()
	assert.Contains(t, out, ":warning:", "expected diff result should show warning emoji")
}

// TestRenderMarkdown_StatusFAIL verifies the FAIL status row.
func TestRenderMarkdown_StatusFAIL(t *testing.T) {
	t.Parallel()
	r := depcompat.NewCompatReport("run-status-fail", "")
	r.AddResult(depcompat.OutputResult{
		DepGroup:   depcompat.DepGroupCrypto,
		OutputKind: depcompat.OutputTrace,
		Class:      depcompat.DiffClassUnexpected,
		Diffs: []depcompat.FieldDiff{
			{JSONPath: "$.count", GoldenValue: "3", ActualValue: "5", Class: depcompat.DiffClassUnexpected},
		},
	})
	r.Finalize()

	var buf bytes.Buffer
	require.NoError(t, depcompat.RenderMarkdown(r, &buf))
	out := buf.String()
	assert.Contains(t, out, ":x:", "unexpected diff result should show X emoji")
}

// ─── RenderText with error results ────────────────────────────────────────────

// TestRenderText_WithError verifies that a result carrying an Error field
// is rendered with the ERROR label and the message in the text output.
func TestRenderText_WithError(t *testing.T) {
	t.Parallel()
	r := depcompat.NewCompatReport("run-text-err", depcompat.DepGroupSorobanHost)
	r.AddResult(depcompat.OutputResult{
		DepGroup:   depcompat.DepGroupSorobanHost,
		OutputKind: depcompat.OutputReplay,
		Class:      depcompat.DiffClassUnexpected,
		Error:      "binary not found",
	})
	r.Finalize()

	var buf bytes.Buffer
	require.NoError(t, depcompat.RenderText(r, &buf))
	out := buf.String()
	assert.Contains(t, out, "ERROR")
	assert.Contains(t, out, "binary not found")
}

// TestRenderText_AllDepGroups verifies that the text renderer shows "all groups"
// when the DepGroup field is empty.
func TestRenderText_AllDepGroups(t *testing.T) {
	t.Parallel()
	r := depcompat.NewCompatReport("run-all", "")
	r.Finalize()

	var buf bytes.Buffer
	require.NoError(t, depcompat.RenderText(r, &buf))
	out := buf.String()
	assert.Contains(t, out, "all groups", "empty dep group should render as 'all groups'")
}

// TestRenderText_StatusLabels verifies that PASS, WARN, and ERROR status labels
// are all reachable.
func TestRenderText_StatusLabels(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		summary  depcompat.ReportSummary
		contains string
	}{
		{
			name:     "pass",
			summary:  depcompat.ReportSummary{TotalOutputs: 1, OutputsMatched: 1},
			contains: "PASS",
		},
		{
			name:    "warn",
			summary: depcompat.ReportSummary{TotalOutputs: 1, OutputsExpected: 1},
			contains: "WARN",
		},
		{
			name:    "fail-unexpected",
			summary: depcompat.ReportSummary{TotalOutputs: 1, OutputsUnexpected: 1, HasUnexpectedDiffs: true},
			contains: "FAIL",
		},
		{
			name:    "fail-error",
			summary: depcompat.ReportSummary{TotalOutputs: 1, OutputsErrored: 1, HasErrors: true},
			contains: "ERROR",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := &depcompat.CompatReport{
				SchemaVersion: "1.0",
				RunID:         "run-status-" + tc.name,
				Summary:       tc.summary,
				Results:       []depcompat.OutputResult{},
			}
			var buf bytes.Buffer
			require.NoError(t, depcompat.RenderText(r, &buf))
			assert.Contains(t, buf.String(), tc.contains)
		})
	}
}

// ─── RenderMarkdown with error results ────────────────────────────────────────

// TestRenderMarkdown_WithError verifies the error callout block renders in the
// detailed diffs section.
func TestRenderMarkdown_WithError(t *testing.T) {
	t.Parallel()
	r := depcompat.NewCompatReport("run-md-err", depcompat.DepGroupCrypto)
	r.AddResult(depcompat.OutputResult{
		DepGroup:   depcompat.DepGroupCrypto,
		OutputKind: depcompat.OutputAudit,
		Class:      depcompat.DiffClassUnexpected,
		Error:      "timeout after 30s",
	})
	r.Finalize()

	var buf bytes.Buffer
	require.NoError(t, depcompat.RenderMarkdown(r, &buf))
	out := buf.String()
	// The error should appear both in the table and in the detail section.
	assert.Contains(t, out, "ERROR")
	assert.Contains(t, out, "timeout after 30s")
}

// TestRenderMarkdown_NoDiffsDetailSection verifies the "No diffs" line is
// rendered for a result with class != none but empty diffs list (edge case).
func TestRenderMarkdown_NoDiffsDetailSection(t *testing.T) {
	t.Parallel()
	r := depcompat.NewCompatReport("run-nodiff-section", "")
	r.AddResult(depcompat.OutputResult{
		DepGroup:   depcompat.DepGroupRPCClient,
		OutputKind: depcompat.OutputReplay,
		Class:      depcompat.DiffClassExpected,
		Diffs:      nil, // empty diffs despite non-none class
	})
	r.Finalize()

	var buf bytes.Buffer
	require.NoError(t, depcompat.RenderMarkdown(r, &buf))
	out := buf.String()
	assert.Contains(t, out, "_No diffs._", "expected 'No diffs.' placeholder for empty diffs list")
}

// TestRenderMarkdown_PipeEscaping verifies that pipe characters in values
// are escaped so they do not break Markdown table cells.
func TestRenderMarkdown_PipeEscaping(t *testing.T) {
	t.Parallel()
	r := depcompat.NewCompatReport("run-pipe", "")
	r.AddResult(depcompat.OutputResult{
		DepGroup:   depcompat.DepGroupStellarSDK,
		OutputKind: depcompat.OutputTrace,
		Class:      depcompat.DiffClassUnexpected,
		Diffs: []depcompat.FieldDiff{
			{
				JSONPath:    "$.field",
				GoldenValue: `"a|b"`,
				ActualValue: `"c|d"`,
				Class:       depcompat.DiffClassUnexpected,
				Reason:      "pipe | in value",
			},
		},
	})
	r.Finalize()

	var buf bytes.Buffer
	require.NoError(t, depcompat.RenderMarkdown(r, &buf))
	out := buf.String()
	// Unescaped pipe characters in the table body would break Markdown.
	// Check that the literal sequence " | " does not appear inside backtick spans.
	// A simpler check: the escaped form \| should appear.
	if !strings.Contains(out, `\|`) {
		t.Errorf("pipe characters should be escaped in Markdown table; output: %s", out)
	}
}

// TestRenderMarkdown_VersionsUnknown verifies that "(unknown)" is rendered for
// empty version fields.
func TestRenderMarkdown_VersionsUnknown(t *testing.T) {
	t.Parallel()
	r := depcompat.NewCompatReport("run-versions", "")
	// Leave all version fields empty.
	r.Finalize()

	var buf bytes.Buffer
	require.NoError(t, depcompat.RenderMarkdown(r, &buf))
	out := buf.String()
	assert.Contains(t, out, "(unknown)", "empty version fields should render as '(unknown)'")
}

// TestRenderMarkdown_AllDepGroupsLabel verifies "all groups" is rendered
// when the DepGroup is empty.
func TestRenderMarkdown_AllDepGroupsLabel(t *testing.T) {
	t.Parallel()
	r := depcompat.NewCompatReport("run-all-groups", "")
	r.Finalize()

	var buf bytes.Buffer
	require.NoError(t, depcompat.RenderMarkdown(r, &buf))
	out := buf.String()
	assert.Contains(t, out, "all groups")
}
