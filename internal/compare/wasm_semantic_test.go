// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package compare

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// buildSection appends a WASM section with the given id and payload to buf.
func buildSection(buf []byte, id byte, payload []byte) []byte {
	buf = append(buf, id)
	buf = appendUvarint(buf, uint64(len(payload)))
	return append(buf, payload...)
}

// buildCustomSection appends a custom section (ID=0) with the given name
// and body payload.
func buildCustomSection(buf []byte, name string, body []byte) []byte {
	// name is length-prefixed inside the custom section payload.
	var sectionPayload []byte
	sectionPayload = appendUvarint(sectionPayload, uint64(len(name)))
	sectionPayload = append(sectionPayload, []byte(name)...)
	sectionPayload = append(sectionPayload, body...)
	return buildSection(buf, 0, sectionPayload)
}

// buildWASMWith constructs a WASM binary with the given sections appended
// after the standard 8-byte header.
func buildWASMWith(sections []byte) []byte {
	return append(wasmHeader, sections...)
}

// ── classifySection ───────────────────────────────────────────────────────────

func TestClassifySection_StandardSections(t *testing.T) {
	cases := []struct {
		id       byte
		expected SectionClass
	}{
		{1, SectionClassExecutable},  // Type
		{3, SectionClassExecutable},  // Function
		{10, SectionClassExecutable}, // Code
		{2, SectionClassABI},         // Import
		{7, SectionClassABI},         // Export
	}
	for _, tc := range cases {
		s := WASMSection{ID: tc.id}
		got := classifySection(s)
		assert.Equal(t, tc.expected, got, "section ID %d", tc.id)
	}
}

func TestClassifyCustomSection_KnownNames(t *testing.T) {
	cases := []struct {
		name     string
		expected SectionClass
	}{
		{"name", SectionClassDebug},
		{".debug_info", SectionClassDebug},
		{".debug_line", SectionClassDebug},
		{"sourceMappingURL", SectionClassDebug},
		{"producers", SectionClassMetadata},
		{"target_features", SectionClassMetadata},
		{"build_id", SectionClassMetadata},
		{"my_custom_section", SectionClassUnknown},
		{"", SectionClassUnknown},
	}
	for _, tc := range cases {
		got := classifyCustomSection(tc.name)
		assert.Equal(t, tc.expected, got, "custom name %q", tc.name)
	}
}

// ── parseCustomSectionName ────────────────────────────────────────────────────

func TestParseCustomSectionName_Valid(t *testing.T) {
	var payload []byte
	payload = appendUvarint(payload, uint64(len("producers")))
	payload = append(payload, "producers"...)
	payload = append(payload, 0xDE, 0xAD) // body

	got := parseCustomSectionName(payload)
	assert.Equal(t, "producers", got)
}

func TestParseCustomSectionName_Empty(t *testing.T) {
	assert.Equal(t, "", parseCustomSectionName(nil))
	assert.Equal(t, "", parseCustomSectionName([]byte{}))
}

// ── DiffWASMSemantic: identical binaries ──────────────────────────────────────

func TestDiffWASMSemantic_Identical_NoFindings(t *testing.T) {
	data := makeWASM([]byte{0x01, 0x02})
	result := DiffWASMSemantic(data, data, DefaultNormalizeOptions())

	assert.False(t, result.Raw.HasDivergence)
	assert.False(t, result.ExecutableChanged)
	assert.False(t, result.ABIChanged)
	assert.False(t, result.MetadataOnlyDiff)
	assert.Contains(t, result.Summary, "identical")
}

// ── DiffWASMSemantic: metadata-only diff is not a semantic finding ────────────

func TestDiffWASMSemantic_MetadataOnlyDiff_NotFlagged(t *testing.T) {
	// local has a "producers" custom section; remote does not.
	var localSections []byte
	localSections = buildCustomSection(localSections, "producers", []byte("clang-17"))

	local := buildWASMWith(localSections)
	remote := buildWASMWith(nil) // no sections

	opts := NormalizeOptions{IgnoreMetadata: true, IgnoreUnknownCustom: true}
	result := DiffWASMSemantic(local, remote, opts)

	assert.True(t, result.Raw.HasDivergence, "raw diff should detect the difference")
	assert.True(t, result.MetadataOnlyDiff,
		"only metadata changed — should be flagged as MetadataOnlyDiff")
	assert.False(t, result.ExecutableChanged)
	assert.False(t, result.ABIChanged)
	assert.Contains(t, result.Summary, "metadata")
}

// ── DiffWASMSemantic: real executable change is detected ─────────────────────

func TestDiffWASMSemantic_ExecutableChange_Detected(t *testing.T) {
	// local: one Code section (ID=10, 3-byte payload)
	// remote: one Code section with different payload
	var localSecs []byte
	localSecs = buildSection(localSecs, 10, []byte{0x01, 0x02, 0x03})
	local := buildWASMWith(localSecs)

	var remoteSecs []byte
	remoteSecs = buildSection(remoteSecs, 10, []byte{0x04, 0x05, 0x06})
	remote := buildWASMWith(remoteSecs)

	result := DiffWASMSemantic(local, remote, DefaultNormalizeOptions())

	assert.True(t, result.Raw.HasDivergence)
	assert.True(t, result.ExecutableChanged, "code section change must be detected")
	assert.False(t, result.MetadataOnlyDiff)
	assert.Contains(t, result.Summary, "executable")
}

// ── DiffWASMSemantic: ABI change is detected separately ──────────────────────

func TestDiffWASMSemantic_ABIChange_Detected(t *testing.T) {
	var localSecs []byte
	localSecs = buildSection(localSecs, 7, []byte{0x01, 0xAA}) // Export
	local := buildWASMWith(localSecs)

	var remoteSecs []byte
	remoteSecs = buildSection(remoteSecs, 7, []byte{0x02, 0xBB}) // Export — different content
	remote := buildWASMWith(remoteSecs)

	result := DiffWASMSemantic(local, remote, DefaultNormalizeOptions())

	assert.True(t, result.ABIChanged, "export section change must be detected as ABI change")
}

// ── DiffWASMSemantic: debug sections respected when not ignored ───────────────

func TestDiffWASMSemantic_DebugChange_DetectedWhenNotIgnored(t *testing.T) {
	var localSecs []byte
	localSecs = buildCustomSection(localSecs, ".debug_info", []byte{0x11})
	local := buildWASMWith(localSecs)

	var remoteSecs []byte
	remoteSecs = buildCustomSection(remoteSecs, ".debug_info", []byte{0x22})
	remote := buildWASMWith(remoteSecs)

	opts := NormalizeOptions{IgnoreMetadata: true, IgnoreDebug: false, IgnoreUnknownCustom: true}
	result := DiffWASMSemantic(local, remote, opts)

	assert.True(t, result.DebugChanged, "debug section change must be detected when IgnoreDebug=false")
}

func TestDiffWASMSemantic_DebugChange_IgnoredWhenFlagged(t *testing.T) {
	var localSecs []byte
	localSecs = buildCustomSection(localSecs, ".debug_info", []byte{0x11})
	local := buildWASMWith(localSecs)

	remote := buildWASMWith(nil)

	opts := NormalizeOptions{IgnoreMetadata: true, IgnoreDebug: true, IgnoreUnknownCustom: true}
	result := DiffWASMSemantic(local, remote, opts)

	assert.False(t, result.ExecutableChanged)
	assert.False(t, result.ABIChanged)
	assert.True(t, result.MetadataOnlyDiff || !result.Raw.HasDivergence || result.DebugChanged == false,
		"debug sections must be ignored when IgnoreDebug=true")
}

// ── DiffWASMSemantic: normalization manifest records dropped classes ──────────

func TestDiffWASMSemantic_ManifestRecordsDroppedSections(t *testing.T) {
	var localSecs []byte
	localSecs = buildCustomSection(localSecs, "producers", []byte("clang"))
	local := buildWASMWith(localSecs)

	remote := buildWASMWith(nil)

	opts := NormalizeOptions{IgnoreMetadata: true}
	result := DiffWASMSemantic(local, remote, opts)

	found := false
	for _, d := range result.Manifest.DroppedLocal {
		if d == string(SectionClassMetadata) {
			found = true
		}
	}
	assert.True(t, found, "manifest should record that metadata class was dropped from local binary")
}

// ── DiffWASMSemanticFiles ─────────────────────────────────────────────────────

func TestDiffWASMSemanticFiles_MetadataOnlyChange(t *testing.T) {
	dir := t.TempDir()

	var localSecs []byte
	localSecs = buildCustomSection(localSecs, "producers", []byte("clang-17"))
	local := buildWASMWith(localSecs)
	remote := buildWASMWith(nil)

	lp := filepath.Join(dir, "local.wasm")
	rp := filepath.Join(dir, "remote.wasm")
	require.NoError(t, os.WriteFile(lp, local, 0644))
	require.NoError(t, os.WriteFile(rp, remote, 0644))

	result, err := DiffWASMSemanticFiles(lp, rp, DefaultNormalizeOptions())
	require.NoError(t, err)
	assert.True(t, result.MetadataOnlyDiff)
}

func TestDiffWASMSemanticFiles_MissingFile(t *testing.T) {
	dir := t.TempDir()
	remote := filepath.Join(dir, "remote.wasm")
	require.NoError(t, os.WriteFile(remote, makeWASM(nil), 0644))

	_, err := DiffWASMSemanticFiles("/nonexistent.wasm", remote, DefaultNormalizeOptions())
	assert.Error(t, err)
}

// ── SectionFindings have stable paths ────────────────────────────────────────

func TestDiffWASMSemantic_SectionFindingsStablePaths(t *testing.T) {
	data := makeWASM([]byte{0x01})
	result := DiffWASMSemantic(data, data, DefaultNormalizeOptions())

	// Every finding must have a non-empty Class.
	for _, f := range result.SectionFindings {
		assert.NotEmpty(t, string(f.Class), "every SectionFinding must have a non-empty Class")
		assert.NotEmpty(t, f.Description, "every SectionFinding must have a Description")
	}
}

// ── DefaultNormalizeOptions ───────────────────────────────────────────────────

func TestDefaultNormalizeOptions(t *testing.T) {
	opts := DefaultNormalizeOptions()
	assert.True(t, opts.IgnoreMetadata, "metadata should be ignored by default")
	assert.False(t, opts.IgnoreDebug, "debug sections should NOT be ignored by default")
	assert.True(t, opts.IgnoreUnknownCustom, "unknown custom sections should be ignored by default")
}
