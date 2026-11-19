// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package bundle_test

import (
	"encoding/json"
	"testing"

	"github.com/dotandev/glassbox/internal/bundle"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── BuildContentManifest ──────────────────────────────────────────────────────

func TestBuildContentManifest_HasAllRequiredEntries(t *testing.T) {
	m := validManifest()
	cm := m.ContentManifest
	require.NotNil(t, cm)
	assert.Equal(t, bundle.ContentManifestVersion, cm.Version)

	byPath := make(map[string]bundle.ArtifactEntry, len(cm.Artifacts))
	for _, a := range cm.Artifacts {
		byPath[a.LogicalPath] = a
	}

	required := []string{
		bundle.ArtifactEnvelopeXDR,
		bundle.ArtifactResultMetaXDR,
		bundle.ArtifactLedgerState,
		bundle.ArtifactProvenance,
		bundle.ArtifactNetwork,
	}
	for _, r := range required {
		entry, ok := byPath[r]
		require.True(t, ok, "missing required artifact %s", r)
		assert.Equal(t, bundle.RoleRequired, entry.Role)
		assert.NotEmpty(t, entry.SHA256, "required artifact %s should have a hash", r)
		assert.Greater(t, entry.SizeBytes, int64(0), "required artifact %s should have size > 0", r)
	}

	optional := []string{bundle.ArtifactTrace, bundle.ArtifactSourceMap, bundle.ArtifactSignature}
	for _, o := range optional {
		entry, ok := byPath[o]
		require.True(t, ok, "missing optional artifact entry %s", o)
		assert.Equal(t, bundle.RoleOptional, entry.Role)
	}
}

func TestBuildContentManifest_WithExtras(t *testing.T) {
	m := validManifest()
	cm := bundle.BuildContentManifest(m, map[string]string{"custom.annotation": "some-value"})
	require.NotNil(t, cm)

	var found bool
	for _, a := range cm.Artifacts {
		if a.LogicalPath == "custom.annotation" {
			assert.Equal(t, bundle.RoleExtension, a.Role)
			assert.NotEmpty(t, a.SHA256)
			found = true
		}
	}
	assert.True(t, found, "extension artifact should be in manifest")
}

func TestBuildContentManifest_IsDeterministic(t *testing.T) {
	m := validManifest()
	cm1 := bundle.BuildContentManifest(m, nil)
	cm2 := bundle.BuildContentManifest(m, nil)
	require.NotNil(t, cm1)
	require.NotNil(t, cm2)
	require.Equal(t, len(cm1.Artifacts), len(cm2.Artifacts))
	for i := range cm1.Artifacts {
		assert.Equal(t, cm1.Artifacts[i].SHA256, cm2.Artifacts[i].SHA256,
			"artifact %s hash must be deterministic", cm1.Artifacts[i].LogicalPath)
	}
}

// ── ContentManifest.Validate ──────────────────────────────────────────────────

func TestContentManifest_Validate_OK(t *testing.T) {
	m := validManifest()
	hardErr, _ := m.ValidateContent()
	assert.Nil(t, hardErr, "expected no hard error for valid bundle")
}

func TestContentManifest_Validate_NilManifest_IsLegacyWarning(t *testing.T) {
	m := validManifest()
	m.ContentManifest = nil
	_, warn := m.ValidateContent()
	require.NotNil(t, warn)
	assert.True(t, warn.LegacyBundle)
}

func TestContentManifest_Validate_MissingRequiredArtifact(t *testing.T) {
	m := validManifest()
	require.NotNil(t, m.ContentManifest)

	// Build the expected live map, then drop the envelope entry.
	live := liveMapFor(m)
	delete(live, bundle.ArtifactEnvelopeXDR)

	hardErr, _ := m.ContentManifest.Validate(live)
	require.NotNil(t, hardErr, "missing required artifact must produce a hard error")
	assert.Contains(t, hardErr.Missing, bundle.ArtifactEnvelopeXDR)
	assert.True(t, bundle.IsContentManifestError(hardErr))
	assert.Contains(t, hardErr.Error(), "missing required artifact")
}

func TestContentManifest_Validate_ModifiedArtifact(t *testing.T) {
	m := validManifest()
	require.NotNil(t, m.ContentManifest)

	live := liveMapFor(m)
	// Tamper with the envelope value after the manifest was sealed.
	live[bundle.ArtifactEnvelopeXDR] = "TAMPERED_VALUE_THAT_WILL_HASH_DIFFERENTLY"

	hardErr, _ := m.ContentManifest.Validate(live)
	require.NotNil(t, hardErr)
	assert.Contains(t, hardErr.Modified, bundle.ArtifactEnvelopeXDR)
	assert.Contains(t, hardErr.Error(), "modified artifact")
}

func TestContentManifest_Validate_UnlistedArtifact(t *testing.T) {
	m := validManifest()
	require.NotNil(t, m.ContentManifest)

	live := liveMapFor(m)
	live["sneaky.injection"] = "malicious"

	hardErr, _ := m.ContentManifest.Validate(live)
	require.NotNil(t, hardErr)
	assert.Contains(t, hardErr.Unlisted, "sneaky.injection")
	assert.Contains(t, hardErr.Error(), "unlisted artifact")
}

func TestContentManifest_Validate_MissingOptionalArtifact_IsWarning(t *testing.T) {
	m := validManifest()
	require.NotNil(t, m.ContentManifest)

	live := liveMapFor(m)
	delete(live, bundle.ArtifactTrace)
	delete(live, bundle.ArtifactSourceMap)
	delete(live, bundle.ArtifactSignature)

	hardErr, _ := m.ContentManifest.Validate(live)
	assert.Nil(t, hardErr, "missing optional artifacts must not produce a hard error")
}

func TestContentManifest_Validate_RedactedEntrySkipped(t *testing.T) {
	m := validManifest()
	require.NotNil(t, m.ContentManifest)

	for i, a := range m.ContentManifest.Artifacts {
		if a.LogicalPath == bundle.ArtifactSignature {
			m.ContentManifest.Artifacts[i].Redacted = true
			m.ContentManifest.Artifacts[i].SHA256 = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
		}
	}

	live := liveMapFor(m)
	live[bundle.ArtifactSignature] = "completely_different_value"

	hardErr, _ := m.ContentManifest.Validate(live)
	assert.Nil(t, hardErr, "redacted artifact must not produce a hash-mismatch error")
}

func TestContentManifest_Validate_ExtensionEntry(t *testing.T) {
	m := validManifest()
	cm := bundle.BuildContentManifest(m, map[string]string{"my.extension": "hello"})
	require.NotNil(t, cm)

	live := liveMapFor(m)
	live["my.extension"] = "hello"

	hardErr, _ := cm.Validate(live)
	assert.Nil(t, hardErr, "extension artifacts declared in manifest must not be unlisted")
}

// ── ValidateContent integration ───────────────────────────────────────────────

func TestManifest_ValidateContent_PropagatesHardError(t *testing.T) {
	m := validManifest()
	// Corrupt the stored hash for envelope_xdr in the content manifest so
	// it no longer matches the live value — simulates post-seal tampering.
	for i, a := range m.ContentManifest.Artifacts {
		if a.LogicalPath == bundle.ArtifactEnvelopeXDR {
			m.ContentManifest.Artifacts[i].SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
		}
	}
	hardErr, _ := m.ValidateContent()
	require.NotNil(t, hardErr)
	assert.Contains(t, hardErr.Modified, bundle.ArtifactEnvelopeXDR)
}

func TestManifest_ContentManifest_RoundTrip(t *testing.T) {
	m := validManifest()
	require.NotNil(t, m.ContentManifest)

	dir := t.TempDir()
	path := dir + "/bundle.json"
	require.NoError(t, m.SaveToFile(path))

	loaded, err := bundle.LoadFromFile(path)
	require.NoError(t, err)
	require.NotNil(t, loaded.ContentManifest)

	hardErr, _ := loaded.ValidateContent()
	assert.Nil(t, hardErr)
}

func TestManifest_ContentManifest_IsGeneratedOnNew(t *testing.T) {
	m := validManifest()
	require.NotNil(t, m.ContentManifest,
		"New() must generate a ContentManifest automatically")
	assert.Equal(t, bundle.ContentManifestVersion, m.ContentManifest.Version)
}

// ── helper ────────────────────────────────────────────────────────────────────

// liveMapFor reconstructs the live artifact value map that was used when the
// ContentManifest was built.  It replicates the logic of liveArtifactValues
// (which is package-private) using only the exported fields of the Manifest.
//
// liveArtifactValues stores:
//   - ArtifactEnvelopeXDR   → m.Transaction.EnvelopeXDR (raw string)
//   - ArtifactResultMetaXDR → m.Transaction.ResultMetaXDR (raw string)
//   - ArtifactLedgerState   → m.Checksums[MemberLedgerState] (the hex hash of the state)
//   - ArtifactProvenance    → json.Marshal(m.Provenance) (JSON bytes as string)
//   - ArtifactNetwork       → json.Marshal(m.Network) (JSON bytes as string)
//   - ArtifactTrace         → m.TraceData (if non-empty)
//   - ArtifactSourceMap     → m.SourceMapRef (if non-empty)
//   - ArtifactSignature     → m.Signature (if non-empty)
//
// The ledger state value stored in the content manifest is
// sha256HexOfLedgerState(m.LedgerState), which equals m.Checksums[MemberLedgerState].
func liveMapFor(m *bundle.Manifest) map[string]string {
	live := make(map[string]string, 8)
	live[bundle.ArtifactEnvelopeXDR] = m.Transaction.EnvelopeXDR
	live[bundle.ArtifactResultMetaXDR] = m.Transaction.ResultMetaXDR

	// Ledger state: the value stored in liveArtifactValues is
	// sha256HexOfLedgerState, which is the same value stored in
	// m.Checksums[MemberLedgerState].
	if cs, ok := m.Checksums[bundle.MemberLedgerState]; ok {
		live[bundle.ArtifactLedgerState] = cs
	}

	// Provenance and Network are stored as their JSON representations.
	if b, err := json.Marshal(m.Provenance); err == nil {
		live[bundle.ArtifactProvenance] = string(b)
	}
	if b, err := json.Marshal(m.Network); err == nil {
		live[bundle.ArtifactNetwork] = string(b)
	}

	if m.TraceData != "" {
		live[bundle.ArtifactTrace] = m.TraceData
	}
	if m.SourceMapRef != "" {
		live[bundle.ArtifactSourceMap] = m.SourceMapRef
	}
	if m.Signature != "" {
		live[bundle.ArtifactSignature] = m.Signature
	}

	return live
}
