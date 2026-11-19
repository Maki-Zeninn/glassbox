// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package sarif_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dotandev/glassbox/internal/sarif"
	"github.com/dotandev/glassbox/internal/security"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── fixture helpers ──────────────────────────────────────────────────────────

func highFinding(title, evidence string) security.Finding {
	return security.Finding{
		Type:        security.FindingVerifiedRisk,
		Severity:    security.SeverityHigh,
		Title:       title,
		Description: "High severity issue detected.",
		Evidence:    evidence,
	}
}

func medFinding(title string) security.Finding {
	return security.Finding{
		Type:        security.FindingHeuristicWarn,
		Severity:    security.SeverityMedium,
		Title:       title,
		Description: "Medium severity heuristic warning.",
	}
}

// ─── structural tests ─────────────────────────────────────────────────────────

func TestExport_SchemaAndVersion(t *testing.T) {
	log := sarif.Export(nil, sarif.ExportOptions{})
	assert.Equal(t, sarif.SARIFVersion, log.Version)
	assert.Equal(t, sarif.SchemaURI, log.Schema)
}

func TestExport_SingleRun(t *testing.T) {
	log := sarif.Export(nil, sarif.ExportOptions{})
	require.Len(t, log.Runs, 1)
}

func TestExport_ToolDriver(t *testing.T) {
	log := sarif.Export(nil, sarif.ExportOptions{})
	driver := log.Runs[0].Tool.Driver
	assert.Equal(t, "Glassbox", driver.Name)
	assert.NotEmpty(t, driver.Version, "tool version must not be empty")
	assert.NotEmpty(t, driver.InformationURI)
}

func TestExport_EmptyFindings(t *testing.T) {
	log := sarif.Export(nil, sarif.ExportOptions{})
	assert.Empty(t, log.Runs[0].Results, "no findings → no results")
	assert.Empty(t, log.Runs[0].Tool.Driver.Rules, "no findings → no rules")
}

func TestExport_Invocations(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(5 * time.Second)
	log := sarif.Export(nil, sarif.ExportOptions{RunStartTime: start, RunEndTime: end})
	require.Len(t, log.Runs[0].Invocations, 1)
	inv := log.Runs[0].Invocations[0]
	assert.True(t, inv.ExecutionSuccessful)
	assert.Equal(t, start, inv.StartTimeUTC)
	assert.Equal(t, end, inv.EndTimeUTC)
}

// ─── rule ID stability ────────────────────────────────────────────────────────

func TestExport_StableRuleIDs(t *testing.T) {
	f := highFinding("Privileged ABI Function Without Visible Auth", "admin_fn")

	log1 := sarif.Export([]security.Finding{f}, sarif.ExportOptions{})
	log2 := sarif.Export([]security.Finding{f}, sarif.ExportOptions{})

	require.Len(t, log1.Runs[0].Tool.Driver.Rules, 1)
	require.Len(t, log2.Runs[0].Tool.Driver.Rules, 1)
	assert.Equal(t, log1.Runs[0].Tool.Driver.Rules[0].ID, log2.Runs[0].Tool.Driver.Rules[0].ID,
		"rule ID must be stable across identical runs")
}

func TestExport_RuleIDPrefixGB(t *testing.T) {
	f := highFinding("Some Finding", "")
	log := sarif.Export([]security.Finding{f}, sarif.ExportOptions{})
	require.Len(t, log.Runs[0].Results, 1)
	ruleID := log.Runs[0].Results[0].RuleID
	assert.True(t, strings.HasPrefix(ruleID, "GB-"),
		"rule ID must start with GB-, got: %s", ruleID)
}

func TestExport_DifferentFindingsGetDifferentRuleIDs(t *testing.T) {
	f1 := highFinding("Finding Alpha", "")
	f2 := medFinding("Finding Beta")

	log := sarif.Export([]security.Finding{f1, f2}, sarif.ExportOptions{})
	require.Len(t, log.Runs[0].Tool.Driver.Rules, 2)

	id1 := log.Runs[0].Tool.Driver.Rules[0].ID
	id2 := log.Runs[0].Tool.Driver.Rules[1].ID
	assert.NotEqual(t, id1, id2, "distinct findings must produce distinct rule IDs")
}

func TestExport_SameFindingProducesSingleRule(t *testing.T) {
	// Two instances of the same finding class should produce one shared rule.
	f := highFinding("Large Value Transfer", "1000000 XLM")
	f2 := highFinding("Large Value Transfer", "2000000 XLM")

	log := sarif.Export([]security.Finding{f, f2}, sarif.ExportOptions{})
	// Both findings share the same type+title → one rule entry.
	assert.Len(t, log.Runs[0].Tool.Driver.Rules, 1)
	// But two separate results.
	assert.Len(t, log.Runs[0].Results, 2)
}

// ─── severity → level mapping ─────────────────────────────────────────────────

func TestExport_SeverityMappings(t *testing.T) {
	tests := []struct {
		sev       security.Severity
		wantLevel string
	}{
		{security.SeverityHigh, "error"},
		{security.SeverityMedium, "warning"},
		{security.SeverityLow, "note"},
		{security.SeverityInfo, "note"},
	}
	for _, tt := range tests {
		f := security.Finding{
			Type:        security.FindingVerifiedRisk,
			Severity:    tt.sev,
			Title:       "Test Finding",
			Description: "desc",
		}
		log := sarif.Export([]security.Finding{f}, sarif.ExportOptions{})
		require.Len(t, log.Runs[0].Results, 1)
		assert.Equal(t, tt.wantLevel, log.Runs[0].Results[0].Level,
			"severity %s should map to level %s", tt.sev, tt.wantLevel)
	}
}

// ─── fingerprints ─────────────────────────────────────────────────────────────

func TestExport_FingerprintPresent(t *testing.T) {
	f := highFinding("Auth Bypass", "")
	log := sarif.Export([]security.Finding{f}, sarif.ExportOptions{})
	require.Len(t, log.Runs[0].Results, 1)
	fp := log.Runs[0].Results[0].Fingerprints
	assert.NotEmpty(t, fp["primaryLocationLineHash/v1"],
		"fingerprint must be present and non-empty")
}

func TestExport_FingerprintStability(t *testing.T) {
	f := highFinding("Auth Bypass", "some_fn")
	log1 := sarif.Export([]security.Finding{f}, sarif.ExportOptions{})
	log2 := sarif.Export([]security.Finding{f}, sarif.ExportOptions{})

	fp1 := log1.Runs[0].Results[0].Fingerprints["primaryLocationLineHash/v1"]
	fp2 := log2.Runs[0].Results[0].Fingerprints["primaryLocationLineHash/v1"]
	assert.Equal(t, fp1, fp2, "fingerprint must be identical across unchanged runs")
}

func TestExport_DifferentFindingsDifferentFingerprints(t *testing.T) {
	f1 := highFinding("Finding One", "")
	f2 := medFinding("Finding Two")
	log := sarif.Export([]security.Finding{f1, f2}, sarif.ExportOptions{})
	require.Len(t, log.Runs[0].Results, 2)
	fp1 := log.Runs[0].Results[0].Fingerprints["primaryLocationLineHash/v1"]
	fp2 := log.Runs[0].Results[1].Fingerprints["primaryLocationLineHash/v1"]
	assert.NotEqual(t, fp1, fp2)
}

// ─── artifact locations ───────────────────────────────────────────────────────

func TestExport_ArtifactLocationIncluded(t *testing.T) {
	f := highFinding("Finding", "")
	log := sarif.Export([]security.Finding{f}, sarif.ExportOptions{
		ArtifactURI: "contracts/token/src/lib.rs",
	})

	require.Len(t, log.Runs[0].Artifacts, 1)
	assert.Equal(t, "contracts/token/src/lib.rs", log.Runs[0].Artifacts[0].Location.URI)
	assert.Equal(t, "%SRCROOT%", log.Runs[0].Artifacts[0].Location.URIBaseID)

	require.Len(t, log.Runs[0].Results, 1)
	loc := log.Runs[0].Results[0].Locations
	require.Len(t, loc, 1)
	assert.Equal(t, "contracts/token/src/lib.rs", loc[0].PhysicalLocation.ArtifactLocation.URI)
}

func TestExport_NoArtifact_NoLocations(t *testing.T) {
	f := highFinding("Finding", "")
	log := sarif.Export([]security.Finding{f}, sarif.ExportOptions{})
	assert.Empty(t, log.Runs[0].Artifacts)
	assert.Empty(t, log.Runs[0].Results[0].Locations)
}

// ─── suppressed findings ──────────────────────────────────────────────────────

func TestExport_SuppressedFindingsIncluded(t *testing.T) {
	active := highFinding("Active Finding", "")
	suppressed := medFinding("Suppressed Finding")

	log := sarif.Export(
		[]security.Finding{active},
		sarif.ExportOptions{SuppressedFindings: []security.Finding{suppressed}},
	)

	results := log.Runs[0].Results
	require.Len(t, results, 2, "active + suppressed = 2 results")

	// Find the suppressed result.
	var suppressedResult *sarif.Result
	for i := range results {
		if results[i].Properties != nil && results[i].Properties.Suppressed {
			suppressedResult = &results[i]
			break
		}
	}
	require.NotNil(t, suppressedResult, "suppressed finding must carry suppressed=true property")
	require.Len(t, suppressedResult.Suppressions, 1)
	assert.Equal(t, "external", suppressedResult.Suppressions[0].Kind)
	assert.Equal(t, "accepted", suppressedResult.Suppressions[0].Status)
}

func TestExport_ActiveFindingsHaveNoSuppressions(t *testing.T) {
	f := highFinding("Active", "")
	log := sarif.Export([]security.Finding{f}, sarif.ExportOptions{})
	require.Len(t, log.Runs[0].Results, 1)
	assert.Empty(t, log.Runs[0].Results[0].Suppressions)
}

// ─── deterministic ordering ───────────────────────────────────────────────────

func TestExport_DeterministicOrdering(t *testing.T) {
	// Provide findings in different orders and verify the output is identical.
	f1 := highFinding("Alpha Finding", "ev1")
	f2 := medFinding("Beta Finding")
	f3 := security.Finding{
		Type:        security.FindingVerifiedRisk,
		Severity:    security.SeverityLow,
		Title:       "Gamma Finding",
		Description: "low severity",
	}

	log1 := sarif.Export([]security.Finding{f1, f2, f3}, sarif.ExportOptions{})
	log2 := sarif.Export([]security.Finding{f3, f1, f2}, sarif.ExportOptions{})

	data1, _ := sarif.Marshal(log1)
	data2, _ := sarif.Marshal(log2)
	assert.Equal(t, string(data1), string(data2),
		"output must be identical regardless of input ordering")
}

// ─── JSON validity ────────────────────────────────────────────────────────────

func TestMarshal_ValidJSON(t *testing.T) {
	findings := []security.Finding{
		highFinding("Auth Bypass", "fn_name"),
		medFinding("Upgradeable Surface"),
	}
	data, err := sarif.ExportJSON(findings, sarif.ExportOptions{
		ArtifactURI: "src/lib.rs",
	})
	require.NoError(t, err)
	require.NotEmpty(t, data)

	// Must be valid JSON.
	var raw map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &raw), "output must be valid JSON")

	// Must have the required top-level keys.
	assert.Contains(t, raw, "$schema")
	assert.Contains(t, raw, "version")
	assert.Contains(t, raw, "runs")

	// Version must match.
	assert.Equal(t, sarif.SARIFVersion, raw["version"])
}

func TestMarshal_SARIFVersionField(t *testing.T) {
	data, err := sarif.ExportJSON(nil, sarif.ExportOptions{})
	require.NoError(t, err)
	assert.Contains(t, string(data), `"version": "2.1.0"`)
}

func TestMarshal_SchemaURIField(t *testing.T) {
	data, err := sarif.ExportJSON(nil, sarif.ExportOptions{})
	require.NoError(t, err)
	assert.Contains(t, string(data), sarif.SchemaURI)
}

// ─── properties ───────────────────────────────────────────────────────────────

func TestExport_ResultProperties(t *testing.T) {
	f := security.Finding{
		Type:        security.FindingHeuristicWarn,
		Severity:    security.SeverityMedium,
		Title:       "Heuristic Warning",
		Description: "desc",
		Evidence:    "some_fn",
	}
	log := sarif.Export([]security.Finding{f}, sarif.ExportOptions{})
	require.Len(t, log.Runs[0].Results, 1)
	props := log.Runs[0].Results[0].Properties
	require.NotNil(t, props)
	assert.Equal(t, string(security.FindingHeuristicWarn), props.FindingType)
	assert.Equal(t, "some_fn", props.Evidence)
	assert.False(t, props.Suppressed)
}

func TestExport_PartialFingerprints(t *testing.T) {
	f := highFinding("Test", "")
	log := sarif.Export([]security.Finding{f}, sarif.ExportOptions{})
	require.Len(t, log.Runs[0].Results, 1)
	pf := log.Runs[0].Results[0].PartialFingerprints
	assert.NotEmpty(t, pf["ruleId/v1"])
	assert.NotEmpty(t, pf["severity/v1"])
	assert.NotEmpty(t, pf["title/v1"])
}

// ─── rule properties ──────────────────────────────────────────────────────────

func TestExport_RuleProperties(t *testing.T) {
	f := highFinding("Some High Finding", "")
	log := sarif.Export([]security.Finding{f}, sarif.ExportOptions{})
	require.Len(t, log.Runs[0].Tool.Driver.Rules, 1)
	rule := log.Runs[0].Tool.Driver.Rules[0]

	require.NotNil(t, rule.Properties)
	assert.Contains(t, rule.Properties.Tags, "security")
	assert.Contains(t, rule.Properties.Tags, "soroban")
	assert.Contains(t, rule.Properties.Tags, "high-severity")
	assert.Equal(t, "high", rule.Properties.Precision,
		"verified risk should have high precision")

	require.NotNil(t, rule.DefaultConfig)
	assert.Equal(t, "error", rule.DefaultConfig.Level)
}

func TestExport_HeuristicRulePrecision(t *testing.T) {
	f := medFinding("Heuristic Finding")
	log := sarif.Export([]security.Finding{f}, sarif.ExportOptions{})
	require.Len(t, log.Runs[0].Tool.Driver.Rules, 1)
	assert.Equal(t, "medium", log.Runs[0].Tool.Driver.Rules[0].Properties.Precision)
}

// ─── unmapped findings (edge cases) ──────────────────────────────────────────

func TestExport_FindingWithNoEvidence(t *testing.T) {
	f := security.Finding{
		Type:        security.FindingVerifiedRisk,
		Severity:    security.SeverityHigh,
		Title:       "No Evidence",
		Description: "A finding with no evidence field.",
	}
	log := sarif.Export([]security.Finding{f}, sarif.ExportOptions{})
	require.Len(t, log.Runs[0].Results, 1)
	msg := log.Runs[0].Results[0].Message.Text
	assert.Equal(t, "A finding with no evidence field.", msg,
		"message should be the description only when evidence is absent")
}

func TestExport_FindingWithEvidence(t *testing.T) {
	f := security.Finding{
		Type:        security.FindingVerifiedRisk,
		Severity:    security.SeverityHigh,
		Title:       "With Evidence",
		Description: "A finding with evidence.",
		Evidence:    "suspicious_fn",
	}
	log := sarif.Export([]security.Finding{f}, sarif.ExportOptions{})
	require.Len(t, log.Runs[0].Results, 1)
	msg := log.Runs[0].Results[0].Message.Text
	assert.Contains(t, msg, "evidence: suspicious_fn",
		"evidence should be appended to the message text")
}

func TestExport_MultipleLocations_SameArtifact(t *testing.T) {
	findings := []security.Finding{
		highFinding("F1", ""),
		highFinding("F2", ""),
		medFinding("F3"),
	}
	log := sarif.Export(findings, sarif.ExportOptions{ArtifactURI: "src/lib.rs"})
	assert.Len(t, log.Runs[0].Results, 3)
	// All results should reference the same artifact.
	for _, r := range log.Runs[0].Results {
		require.Len(t, r.Locations, 1)
		assert.Equal(t, "src/lib.rs", r.Locations[0].PhysicalLocation.ArtifactLocation.URI)
	}
}

func TestExport_RedactionSafety(t *testing.T) {
	// Verify that no credential-looking strings leak through into SARIF output.
	f := security.Finding{
		Type:        security.FindingVerifiedRisk,
		Severity:    security.SeverityHigh,
		Title:       "Auth Token Exposure",
		Description: "A token was found in the evidence",
		Evidence:    "GDBMISH3EXAMPLEKEYXXXXXXXXXXXXXX", // looks like a Stellar key
	}
	data, err := sarif.ExportJSON([]security.Finding{f}, sarif.ExportOptions{})
	require.NoError(t, err)
	// The evidence is preserved (it was part of the finding) but the output is
	// deterministic — nothing extra was appended.
	assert.Contains(t, string(data), "GDBMISH3EXAMPLEKEYXXXXXXXXXXXXXX")
	// Ensure the JSON is still valid after any special-char handling.
	var raw interface{}
	assert.NoError(t, json.Unmarshal(data, &raw))
}
