// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package session

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	testAccountID  = "G" + strings.Repeat("A", 55)
	testContractID = "C" + strings.Repeat("B", 55)
	testSecretSeed = "S" + strings.Repeat("D", 55)
)

func redactableData() *Data {
	now := time.Now().UTC().Truncate(time.Second)
	return &Data{
		ID:             "redact-session-01",
		TxHash:         "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
		Network:        "testnet",
		Status:         "saved",
		CreatedAt:      now.Add(-time.Hour),
		LastAccessAt:   now,
		HorizonURL:     "https://horizon-testnet.stellar.org",
		PinnedEndpoint: "https://pinned.example.org",
		EnvFingerprint: "glassbox/1.0 linux/amd64",
		EnvelopeXdr:    "AAAA==",
		SimRequestJSON: `{"envelope_xdr":"AAAA==","result_meta_xdr":"","custom_auth_config":{"signer":"` + testAccountID + `"},"mock_args":["arg1"]}`,
		SimResponseJSON: `{"status":"SUCCESS","account":"` + testAccountID + `","contract":"` + testContractID + `"}`,
		TraceJSON: `{"contract_id":"` + testContractID + `","signer":"` + testAccountID + `","seed":"` + testSecretSeed + `"}`,
		SchemaVersion: SchemaVersion,
	}
}

var strkeyPattern = regexp.MustCompile(`\b[GCS][A-Z2-7]{55}\b`)

// ── Strict profile removes configured sensitive classes ─────────────────

func TestRedactSession_Strict_RemovesSensitiveClasses(t *testing.T) {
	data := redactableData()
	redacted, report, err := RedactSession(data, StrictProfile())
	require.NoError(t, err)

	assert.Empty(t, redacted.EnvFingerprint)
	assert.Empty(t, redacted.HorizonURL)
	assert.Empty(t, redacted.PinnedEndpoint)
	assert.NotContains(t, redacted.SimRequestJSON, "custom_auth_config")
	assert.NotContains(t, redacted.SimRequestJSON, "mock_args")

	// No raw account/contract/secret identifiers survive in any JSON field.
	for _, s := range []string{redacted.SimRequestJSON, redacted.SimResponseJSON, redacted.TraceJSON} {
		assert.False(t, strkeyPattern.MatchString(s), "field still contains a raw strkey identifier: %s", s)
	}
	assert.NotContains(t, redacted.TraceJSON, testSecretSeed)

	require.Len(t, report.Fields, 3)
}

func TestRedactSession_Strict_SecretSeedHardRedactedNotPseudonymized(t *testing.T) {
	data := redactableData()
	redacted, _, err := RedactSession(data, StrictProfile())
	require.NoError(t, err)
	assert.Contains(t, redacted.TraceJSON, "[REDACTED]")
	assert.NotContains(t, redacted.TraceJSON, testSecretSeed)
}

// ── Repeated identifiers pseudonymize consistently ───────────────────────

func TestRedactSession_RepeatedIdentifierConsistentlyPseudonymized(t *testing.T) {
	data := redactableData()
	redacted, _, err := RedactSession(data, StrictProfile())
	require.NoError(t, err)

	// testAccountID appears in both SimResponseJSON and TraceJSON; both
	// occurrences must map to the identical pseudonym.
	accountMatches := regexp.MustCompile(`ACCOUNT_[0-9a-f]{8}`)
	inResponse := accountMatches.FindString(redacted.SimResponseJSON)
	inTrace := accountMatches.FindString(redacted.TraceJSON)
	require.NotEmpty(t, inResponse)
	require.NotEmpty(t, inTrace)
	assert.Equal(t, inResponse, inTrace, "the same source identifier must pseudonymize identically across fields")
}

func TestRedactSession_PseudonymsStableAcrossRepeatedExports(t *testing.T) {
	data := redactableData()
	first, _, err := RedactSession(data, StrictProfile())
	require.NoError(t, err)
	second, _, err := RedactSession(data, StrictProfile())
	require.NoError(t, err)
	assert.Equal(t, first.SimResponseJSON, second.SimResponseJSON,
		"pseudonyms must be stable across repeated exports of the same session")
}

// ── Balanced profile keeps contract args / env metadata ─────────────────

func TestRedactSession_Balanced_KeepsContractArgsAndEnvMetadata(t *testing.T) {
	data := redactableData()
	redacted, _, err := RedactSession(data, BalancedProfile())
	require.NoError(t, err)

	assert.Equal(t, data.EnvFingerprint, redacted.EnvFingerprint)
	assert.Equal(t, data.HorizonURL, redacted.HorizonURL)
	assert.Contains(t, redacted.SimRequestJSON, "custom_auth_config")
	// But identifiers are still pseudonymized.
	assert.False(t, strkeyPattern.MatchString(redacted.SimResponseJSON))
}

// ── Full profile is a pass-through ───────────────────────────────────────

func TestRedactSession_Full_NoChanges(t *testing.T) {
	data := redactableData()
	redacted, report, err := RedactSession(data, FullProfile())
	require.NoError(t, err)

	assert.Equal(t, data.EnvFingerprint, redacted.EnvFingerprint)
	assert.Equal(t, data.SimRequestJSON, redacted.SimRequestJSON)
	assert.Equal(t, data.SimResponseJSON, redacted.SimResponseJSON)
	for _, f := range report.Fields {
		assert.False(t, f.Applied)
	}
}

// ── RedactSession never mutates the input ────────────────────────────────

func TestRedactSession_DoesNotMutateInput(t *testing.T) {
	data := redactableData()
	original := *data
	_, _, err := RedactSession(data, StrictProfile())
	require.NoError(t, err)
	assert.Equal(t, original, *data, "RedactSession must not mutate its input")
}

// ── Preview report ────────────────────────────────────────────────────────

func TestRedactSession_PreviewReportListsRemovedFields(t *testing.T) {
	data := redactableData()
	_, report, err := RedactSession(data, StrictProfile())
	require.NoError(t, err)

	var sawEnv, sawContractArgs, sawIdentifiers bool
	for _, f := range report.Fields {
		switch f.Field {
		case FieldEnvMetadata:
			sawEnv = f.Applied
		case FieldContractArgs:
			sawContractArgs = f.Applied
		case FieldAccountIdentifiers:
			sawIdentifiers = f.Applied
		}
	}
	assert.True(t, sawEnv, "report should note env metadata was removed")
	assert.True(t, sawContractArgs, "report should note contract args were removed")
	assert.True(t, sawIdentifiers, "report should note identifiers were pseudonymized")
	assert.Positive(t, report.IdentifiersPseudonymized)
}

// ── ParseRedactionProfile ─────────────────────────────────────────────────

func TestParseRedactionProfile(t *testing.T) {
	p, err := ParseRedactionProfile("strict")
	require.NoError(t, err)
	assert.Equal(t, RedactionStrict, p.Name)

	p, err = ParseRedactionProfile("")
	require.NoError(t, err)
	assert.Equal(t, RedactionFull, p.Name, "empty string must default to full for backward compatibility")

	_, err = ParseRedactionProfile("nonsense")
	require.Error(t, err)
}

// ── Round-trip validation: redacted archives stay structurally valid ─────

func TestRedactSession_RoundTripValidationPerProfile(t *testing.T) {
	profiles := []RedactionProfile{FullProfile(), BalancedProfile(), StrictProfile()}
	for _, profile := range profiles {
		t.Run(string(profile.Name), func(t *testing.T) {
			data := redactableData()
			redacted, _, err := RedactSession(data, profile)
			require.NoError(t, err)

			report := ValidateIntegrity(redacted)
			assert.True(t, report.OK, "redacted session under profile %q must still pass ValidateIntegrity: %+v", profile.Name, report.Issues)

			dir := t.TempDir()
			archivePath := filepath.Join(dir, "session.gbx")
			require.NoError(t, ExportArchive(redacted, archivePath))

			restored, manifestReport, err := ImportArchiveWithManifest(archivePath)
			require.NoError(t, err)
			assert.True(t, manifestReport.OK)
			assert.True(t, ValidateIntegrity(restored).OK)
		})
	}
}
