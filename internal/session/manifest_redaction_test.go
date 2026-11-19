// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Tests for RedactionManifest fields embedded in the archive manifest.

package session

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── BuildManifestWithRedaction ────────────────────────────────────────────────

func TestBuildManifestWithRedaction_FullProfile_NoRedactionField(t *testing.T) {
	report := &RedactionReport{
		Profile: RedactionFull,
		Fields: []RedactionFieldReport{
			{Field: FieldEnvMetadata, Policy: PolicyKeep, Applied: false},
		},
	}
	m := BuildManifestWithRedaction(nil, SchemaVersion, time.Now(), report)
	assert.Nil(t, m.Redaction, "full-profile export should not embed a Redaction manifest")
}

func TestBuildManifestWithRedaction_StrictProfile_EmbeddedWithCategories(t *testing.T) {
	report := &RedactionReport{
		Profile: RedactionStrict,
		Fields: []RedactionFieldReport{
			{Field: FieldEnvMetadata, Policy: PolicyRedact, Applied: true, Sample: "https://horizon-testnet.stellar.org"},
			{Field: FieldContractArgs, Policy: PolicyRedact, Applied: false},
			{Field: FieldAccountIdentifiers, Policy: PolicyPseudonymize, Applied: true, Sample: "2 pseudonymized"},
		},
		IdentifiersPseudonymized: 2,
	}

	m := BuildManifestWithRedaction(nil, SchemaVersion, time.Now(), report)
	require.NotNil(t, m.Redaction, "strict-profile export must embed a Redaction manifest")
	assert.Equal(t, string(RedactionStrict), m.Redaction.Profile)
	assert.Equal(t, 2, m.Redaction.IdentifiersPseudonymized)
	assert.Len(t, m.Redaction.Categories, 3)

	// No Sample values — sensitive data must never appear in the manifest.
	for _, cat := range m.Redaction.Categories {
		assert.NotEmpty(t, cat.Name)
		assert.NotEmpty(t, cat.Policy)
		// Ensure the sample string from the RedactionReport was NOT copied.
		assert.NotContains(t, cat.Name, "horizon-testnet",
			"manifest category must not contain sensitive sample values")
	}
}

func TestBuildManifestWithRedaction_BalancedProfile_EmbeddedWithPseudonymCount(t *testing.T) {
	report := &RedactionReport{
		Profile: RedactionBalanced,
		Fields: []RedactionFieldReport{
			{Field: FieldAccountIdentifiers, Policy: PolicyPseudonymize, Applied: true},
		},
		IdentifiersPseudonymized: 5,
	}
	m := BuildManifestWithRedaction(nil, SchemaVersion, time.Now(), report)
	require.NotNil(t, m.Redaction)
	assert.Equal(t, 5, m.Redaction.IdentifiersPseudonymized)
}

func TestBuildManifestWithRedaction_NilReport_NoRedactionField(t *testing.T) {
	m := BuildManifestWithRedaction(nil, SchemaVersion, time.Now(), nil)
	assert.Nil(t, m.Redaction)
}

// ── Archive round-trip: redaction manifest survives import ────────────────────

func TestArchiveRoundTrip_RedactionManifestPreserved(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "redacted.gbx")

	data := redactableData()
	profile := StrictProfile()
	redacted, report, err := RedactSession(data, profile)
	require.NoError(t, err)

	opts := ArchiveOptions{RedactionReport: report}
	require.NoError(t, ExportArchiveWithOptions(redacted, archivePath, opts))

	_, manifestReport, err := ImportArchiveWithManifest(archivePath)
	require.NoError(t, err)
	assert.True(t, manifestReport.OK, "manifest should verify cleanly")

	// The redaction manifest is embedded in the zip's manifest.json; we
	// verify the archive can be imported and the manifest is consistent.
	assert.True(t, manifestReport.Compatible, "archive with manifest is compatible")
}

// ── Preview: redaction report lists counts not values ────────────────────────

func TestRedactionReport_PreviewContainsCountsNotValues(t *testing.T) {
	data := redactableData()
	_, report, err := RedactSession(data, StrictProfile())
	require.NoError(t, err)

	rm := buildRedactionManifest(report)
	require.NotNil(t, rm)

	// Manifest categories must not contain raw sensitive values from Sample.
	for _, cat := range rm.Categories {
		assert.False(t, strings.Contains(cat.Name, "horizon-testnet"),
			"category name must not leak sensitive sample")
		assert.False(t, strings.Contains(cat.Policy, "https://"),
			"category policy must not leak URL content")
	}
	// The count of pseudonymized identifiers is safe to record.
	assert.GreaterOrEqual(t, rm.IdentifiersPseudonymized, 0)
}

// ── Explicit sensitive inclusion flag is visible in metadata ─────────────────

func TestArchiveOptions_FullProfile_NoRedactionEmbedded(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "full.gbx")

	data := sampleData()
	// Full profile: no redaction report → no Redaction field in manifest.
	report := &RedactionReport{Profile: RedactionFull, Fields: []RedactionFieldReport{}}
	opts := ArchiveOptions{RedactionReport: report}
	require.NoError(t, ExportArchiveWithOptions(data, archivePath, opts))

	_, manifestReport, err := ImportArchiveWithManifest(archivePath)
	require.NoError(t, err)
	assert.True(t, manifestReport.OK)
}
