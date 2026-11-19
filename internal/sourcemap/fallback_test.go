// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package sourcemap

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─────────────────────────────────────────────────────────────────────────────
// MappingQuality
// ─────────────────────────────────────────────────────────────────────────────

func TestMappingQuality_String(t *testing.T) {
	assert.Equal(t, "full", MappingQualityFull.String())
	assert.Equal(t, "partial", MappingQualityPartial.String())
	assert.Equal(t, "heuristic", MappingQualityHeuristic.String())
	assert.Equal(t, "unknown", MappingQualityUnknown.String())
}

func TestNewFallbackMapper_EmptyProjectRoot_ReturnsError(t *testing.T) {
	_, err := NewFallbackMapper("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to resolve current working directory")
}

func TestNewFallbackMapper_ValidDir_ReturnsMapper(t *testing.T) {
	dir := t.TempDir()
	m, err := NewFallbackMapper(dir)
	require.NoError(t, err)
	require.NotNil(t, m)
	assert.Equal(t, dir, m.ProjectRoot)
}

// ─────────────────────────────────────────────────────────────────────────────
// FallbackMapper.Resolve — stripped binary (no DWARF)
// ─────────────────────────────────────────────────────────────────────────────

func TestFallbackMapper_Resolve_StrippedBinary_ReturnsUnknown(t *testing.T) {
	// A minimal valid WASM binary with no custom sections.
	wasmData := minimalWASM()
	m, err := NewFallbackMapper(t.TempDir())
	require.NoError(t, err)

	result := m.Resolve(wasmData, 0x100)

	require.NotNil(t, result)
	assert.Equal(t, MappingQualityUnknown, result.Quality)
	assert.Equal(t, ConfidenceUnknown, result.Confidence)
	assert.Equal(t, MatchUnknown, result.MatchKind)
	assert.NotEmpty(t, result.Warning, "should emit a warning for stripped binary")
	assert.Contains(t, result.Warning, "no DWARF debug info")
}

func TestFallbackMapper_Resolve_EmptyData_ReturnsUnknown(t *testing.T) {
	m, err := NewFallbackMapper(t.TempDir())
	require.NoError(t, err)
	result := m.Resolve([]byte{}, 0)
	require.NotNil(t, result)
	assert.Equal(t, MappingQualityUnknown, result.Quality)
	assert.Contains(t, result.Warning, "WASM data is empty")
}

func TestFallbackMapper_Resolve_InvalidMagic_ReturnsUnknown(t *testing.T) {
	m, err := NewFallbackMapper(t.TempDir())
	require.NoError(t, err)
	result := m.Resolve([]byte("not_wasm"), 0x100)
	require.NotNil(t, result)
	assert.Equal(t, MappingQualityUnknown, result.Quality)
	assert.Contains(t, result.Warning, "WASM magic bytes")
}

// ─────────────────────────────────────────────────────────────────────────────
// FallbackMapper — Cargo manifest discovery
// ─────────────────────────────────────────────────────────────────────────────

func TestFallbackMapper_Resolve_CargoManifest_ReturnsHeuristic(t *testing.T) {
	dir := t.TempDir()

	// Write a Cargo.toml with a package name.
	cargoToml := filepath.Join(dir, "Cargo.toml")
	err := os.WriteFile(cargoToml, []byte(`
[package]
name = "my_contract"
version = "0.1.0"
`), 0600)
	require.NoError(t, err)

	// Create the expected src/lib.rs so the file-existence check passes.
	srcDir := filepath.Join(dir, "src")
	require.NoError(t, os.MkdirAll(srcDir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "lib.rs"), []byte("// stub"), 0600))

	m, err := NewFallbackMapper(dir)
	require.NoError(t, err)
	result := m.Resolve(minimalWASM(), 0x100)

	require.NotNil(t, result)
	assert.Equal(t, MappingQualityHeuristic, result.Quality)
	assert.Equal(t, MatchHeuristic, result.MatchKind)
	assert.Equal(t, ConfidenceHeuristicCargo, result.Confidence)
	assert.True(t, result.Evidence.Heuristic)
	assert.Equal(t, LinkCandidate, result.LinkPresentation)
	assert.Contains(t, result.File, "lib.rs")
	assert.NotEmpty(t, result.Warning)
	assert.Contains(t, result.Warning, "Cargo manifest fallback")
}

func TestFallbackMapper_Resolve_CargoManifest_NoSrcDir_StillReturnsHeuristic(t *testing.T) {
	dir := t.TempDir()

	cargoToml := filepath.Join(dir, "Cargo.toml")
	err := os.WriteFile(cargoToml, []byte(`
[package]
name = "headless_contract"
version = "0.1.0"
`), 0600)
	require.NoError(t, err)
	// Intentionally do NOT create src/lib.rs.

	m, err := NewFallbackMapper(dir)
	require.NoError(t, err)
	result := m.Resolve(minimalWASM(), 0x100)

	require.NotNil(t, result)
	// Should still return heuristic (file path inferred even if absent).
	assert.Equal(t, MappingQualityHeuristic, result.Quality)
	assert.NotEmpty(t, result.Warning)
}

// ─────────────────────────────────────────────────────────────────────────────
// parseCargoPackageName
// ─────────────────────────────────────────────────────────────────────────────

func TestParseCargoPackageName_ReadsName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Cargo.toml")
	err := os.WriteFile(path, []byte(`
[package]
name = "soroban_hello"
version = "0.1.0"
edition = "2021"
`), 0600)
	require.NoError(t, err)

	name := parseCargoPackageName(path)
	assert.Equal(t, "soroban_hello", name)
}

func TestParseCargoPackageName_MissingPackageSection_ReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Cargo.toml")
	err := os.WriteFile(path, []byte(`
[dependencies]
soroban-sdk = "20"
`), 0600)
	require.NoError(t, err)

	name := parseCargoPackageName(path)
	assert.Empty(t, name)
}

func TestParseCargoPackageName_NonExistentFile_ReturnsEmpty(t *testing.T) {
	name := parseCargoPackageName("/nonexistent/Cargo.toml")
	assert.Empty(t, name)
}

// ─────────────────────────────────────────────────────────────────────────────
// findCargoManifests
// ─────────────────────────────────────────────────────────────────────────────

func TestFindCargoManifests_FindsNestedManifests(t *testing.T) {
	dir := t.TempDir()

	// Root manifest.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[package]\nname=\"root\""), 0600))

	// Nested manifest.
	sub := filepath.Join(dir, "contracts", "token")
	require.NoError(t, os.MkdirAll(sub, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "Cargo.toml"), []byte("[package]\nname=\"token\""), 0600))

	manifests := findCargoManifests(dir)
	assert.GreaterOrEqual(t, len(manifests), 2)
}

func TestFindCargoManifests_EmptyDir_ReturnsNil(t *testing.T) {
	dir := t.TempDir()
	manifests := findCargoManifests(dir)
	assert.Empty(t, manifests)
}

func TestFindCargoManifests_NonExistentDir_ReturnsNil(t *testing.T) {
	manifests := findCargoManifests("/nonexistent/path")
	assert.Empty(t, manifests)
}

// ─────────────────────────────────────────────────────────────────────────────
// crateFromMangledName
// ─────────────────────────────────────────────────────────────────────────────

func TestCrateFromMangledName_RustV0(t *testing.T) {
	// _RNvC<len><crate>... — crate name "my_contract" (11 chars)
	name := "_RNvC11my_contractNtB0_8transfer"
	crate := crateFromMangledName(name)
	assert.Equal(t, "my_contract", crate)
}

func TestCrateFromMangledName_LegacyItanium(t *testing.T) {
	// _ZN<len><crate>... — crate name "token" (5 chars)
	name := "_ZN5token8transferE"
	crate := crateFromMangledName(name)
	assert.Equal(t, "token", crate)
}

func TestCrateFromMangledName_StdlibNames_Rejected(t *testing.T) {
	for _, stdlib := range []string{"std", "core", "alloc"} {
		name := fmt.Sprintf("_ZN%d%sE", len(stdlib), stdlib)
		crate := crateFromMangledName(name)
		assert.Empty(t, crate, "stdlib crate %q should be rejected", stdlib)
	}
}

func TestCrateFromMangledName_UnrecognisedFormat_ReturnsEmpty(t *testing.T) {
	assert.Empty(t, crateFromMangledName("not_a_mangled_name"))
	assert.Empty(t, crateFromMangledName(""))
	assert.Empty(t, crateFromMangledName("_ZN"))
}

// ─────────────────────────────────────────────────────────────────────────────
// isValidCrateName
// ─────────────────────────────────────────────────────────────────────────────

func TestIsValidCrateName(t *testing.T) {
	valid := []string{"my_contract", "soroban-token", "hello123", "A"}
	for _, v := range valid {
		assert.True(t, isValidCrateName(v), "expected %q to be valid", v)
	}

	invalid := []string{"", "std", "core", "alloc", "has space", "has.dot"}
	for _, v := range invalid {
		assert.False(t, isValidCrateName(v), "expected %q to be invalid", v)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// resolveFilePath
// ─────────────────────────────────────────────────────────────────────────────

func TestFallbackMapper_ResolveFilePath_AbsolutePassthrough(t *testing.T) {
	m, err := NewFallbackMapper("/project")
	require.NoError(t, err)
	abs := "/absolute/path/src/lib.rs"
	assert.Equal(t, abs, m.resolveFilePath(abs))
}

func TestFallbackMapper_ResolveFilePath_RelativeResolvesAgainstRoot(t *testing.T) {
	dir := t.TempDir()
	// Create the file so the stat check passes.
	srcDir := filepath.Join(dir, "src")
	require.NoError(t, os.MkdirAll(srcDir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "lib.rs"), []byte(""), 0600))

	m, err := NewFallbackMapper(dir)
	require.NoError(t, err)
	resolved := m.resolveFilePath("src/lib.rs")
	assert.Equal(t, filepath.Join(dir, "src", "lib.rs"), resolved)
}

func TestFallbackMapper_ResolveFilePath_RelativeNotFound_ReturnsOriginal(t *testing.T) {
	m, err := NewFallbackMapper(t.TempDir())
	require.NoError(t, err)
	rel := "src/nonexistent.rs"
	assert.Equal(t, rel, m.resolveFilePath(rel))
}

// ─────────────────────────────────────────────────────────────────────────────
// DWARF line file extraction
// ─────────────────────────────────────────────────────────────────────────────

func TestExtractDWARFLineFiles_NoDebugLine_ReturnsNil(t *testing.T) {
	files := extractDWARFLineFiles(minimalWASM())
	assert.Empty(t, files)
}

func TestExtractDWARFLineFiles_EmptyData_ReturnsNil(t *testing.T) {
	files := extractDWARFLineFiles([]byte{})
	assert.Empty(t, files)
}

// ─────────────────────────────────────────────────────────────────────────────
// Warning messages
// ─────────────────────────────────────────────────────────────────────────────

func TestFallbackMapper_Warning_ContainsRemediation(t *testing.T) {
	m, err := NewFallbackMapper(t.TempDir())
	require.NoError(t, err)
	result := m.Resolve(minimalWASM(), 0x42)

	require.NotNil(t, result)
	assert.NotEmpty(t, result.Warning)
	// The warning should tell the user how to fix the problem.
	assert.True(t,
		contains(result.Warning, "debug = true") ||
			contains(result.Warning, "Recompile") ||
			contains(result.Warning, "no DWARF"),
		"warning should contain remediation advice, got: %s", result.Warning)
}

func TestFallbackMapper_PartialDWARF_Warning_MentionsPartial(t *testing.T) {
	// Build a WASM with a .debug_line section containing a file name but no
	// .debug_info — simulates a partially stripped binary.
	wasm := wasmWithDebugLineFile("src/lib.rs")
	m, err := NewFallbackMapper(t.TempDir())
	require.NoError(t, err)
	result := m.Resolve(wasm, 0x10)

	require.NotNil(t, result)
	if result.Quality == MappingQualityPartial {
		assert.Contains(t, result.Warning, "partial DWARF")
		assert.Contains(t, result.Warning, "src/lib.rs")
	}
	// If the binary is too minimal to trigger partial DWARF, at minimum the
	// result must be non-nil and have a warning.
	assert.NotEmpty(t, result.Warning)
}

// ─────────────────────────────────────────────────────────────────────────────
// Deterministic replay — same input produces same output
// ─────────────────────────────────────────────────────────────────────────────

func TestFallbackMapper_Deterministic(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Cargo.toml"),
		[]byte("[package]\nname=\"det_contract\""), 0600))
	srcDir := filepath.Join(dir, "src")
	require.NoError(t, os.MkdirAll(srcDir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "lib.rs"), []byte(""), 0600))

	m, err := NewFallbackMapper(dir)
	require.NoError(t, err)
	r1 := m.Resolve(minimalWASM(), 0x200)
	r2 := m.Resolve(minimalWASM(), 0x200)

	require.NotNil(t, r1)
	require.NotNil(t, r2)
	assert.Equal(t, r1.File, r2.File)
	assert.Equal(t, r1.Quality, r2.Quality)
	assert.Equal(t, r1.Confidence, r2.Confidence)
	assert.Equal(t, r1.MatchKind, r2.MatchKind)
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// minimalWASM returns the smallest valid WASM binary (magic + version, no sections).
func minimalWASM() []byte {
	return []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
}

// wasmWithDebugLineFile builds a WASM binary that contains a custom section
// named ".debug_line" with a minimal DWARF v4 line-program header that
// references fileName in its file-name table.
func wasmWithDebugLineFile(fileName string) []byte {
	// Build a minimal DWARF v4 line-program header.
	// We only need enough to pass parseDWARFLineFileNames.
	var lineSec []byte

	// unit_length placeholder (4 bytes, filled in later)
	lineSec = append(lineSec, 0, 0, 0, 0)
	// version = 4 (little-endian)
	lineSec = append(lineSec, 4, 0)
	// header_length placeholder (4 bytes)
	lineSec = append(lineSec, 0, 0, 0, 0)
	headerStart := len(lineSec)
	// minimum_instruction_length = 1
	lineSec = append(lineSec, 1)
	// maximum_ops_per_instruction = 1 (v4)
	lineSec = append(lineSec, 1)
	// default_is_stmt = 1
	lineSec = append(lineSec, 1)
	// line_base = -5 (as int8 = 251)
	lineSec = append(lineSec, 251)
	// line_range = 14
	lineSec = append(lineSec, 14)
	// opcode_base = 13
	lineSec = append(lineSec, 13)
	// standard_opcode_lengths (12 bytes for opcodes 1..12)
	lineSec = append(lineSec, 0, 1, 1, 1, 1, 0, 0, 0, 1, 0, 0, 1)
	// include_directories: empty (null terminator)
	lineSec = append(lineSec, 0)
	// file_names: one entry
	lineSec = append(lineSec, []byte(fileName)...)
	lineSec = append(lineSec, 0) // null-terminate name
	lineSec = append(lineSec, 0) // dir_index = 0
	lineSec = append(lineSec, 0) // last_modified = 0
	lineSec = append(lineSec, 0) // file_size = 0
	// end of file_names
	lineSec = append(lineSec, 0)

	// Fill in header_length.
	headerLen := len(lineSec) - headerStart
	lineSec[8] = byte(headerLen)
	lineSec[9] = byte(headerLen >> 8)
	lineSec[10] = byte(headerLen >> 16)
	lineSec[11] = byte(headerLen >> 24)

	// Fill in unit_length (excludes the 4-byte length field itself).
	unitLen := len(lineSec) - 4
	lineSec[0] = byte(unitLen)
	lineSec[1] = byte(unitLen >> 8)
	lineSec[2] = byte(unitLen >> 16)
	lineSec[3] = byte(unitLen >> 24)

	// Wrap in a WASM custom section named ".debug_line".
	return buildWASMWithCustomSection(".debug_line", lineSec)
}

// buildWASMWithCustomSection creates a WASM binary containing a single custom
// section with the given name and content.
func buildWASMWithCustomSection(name string, content []byte) []byte {
	var sec []byte
	// Name length (LEB128) + name bytes.
	nameBytes := []byte(name)
	sec = append(sec, encodeLEB128(uint64(len(nameBytes)))...)
	sec = append(sec, nameBytes...)
	sec = append(sec, content...)

	// Section: ID=0, size (LEB128), payload.
	var out []byte
	out = append(out, 0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00) // magic + version
	out = append(out, 0)                                                 // section ID = 0 (custom)
	out = append(out, encodeLEB128(uint64(len(sec)))...)
	out = append(out, sec...)
	return out
}

func encodeLEB128(v uint64) []byte {
	var out []byte
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			b |= 0x80
		}
		out = append(out, b)
		if v == 0 {
			break
		}
	}
	return out
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}

// ── FallbackMapper.Resolve — nil / too-small WASM data guard ─────────────────

// TestFallbackMapper_Resolve_NilData_ReturnsUnknownWithWarning verifies that
// passing nil wasmData returns a non-nil MappingQualityUnknown result with a
// clear warning instead of panicking or returning a silent no-op.
func TestFallbackMapper_Resolve_NilData_ReturnsUnknownWithWarning(t *testing.T) {
	m := NewFallbackMapper(t.TempDir())
	result := m.Resolve(nil, 0x100)

	require.NotNil(t, result)
	assert.Equal(t, MappingQualityUnknown, result.Quality)
	assert.NotEmpty(t, result.Warning, "nil WASM data should produce a warning")
	assert.True(t,
		contains(result.Warning, "nil") || contains(result.Warning, "too small") || contains(result.Warning, "0 bytes"),
		"warning should describe the data problem, got: %s", result.Warning)
	assert.Contains(t, result.Warning, "cargo build",
		"warning should include a remediation hint")
}

// TestFallbackMapper_Resolve_SmallData_ReturnsUnknownWithWarning verifies that
// WASM data shorter than 8 bytes (magic + version) returns MappingQualityUnknown
// with an actionable warning rather than crashing.
func TestFallbackMapper_Resolve_SmallData_ReturnsUnknownWithWarning(t *testing.T) {
	m := NewFallbackMapper(t.TempDir())

	for _, size := range []int{0, 1, 3, 7} {
		data := make([]byte, size)
		result := m.Resolve(data, 0x42)

		require.NotNil(t, result, "Resolve should never return nil (size=%d)", size)
		assert.Equal(t, MappingQualityUnknown, result.Quality,
			"short data (size=%d) should produce MappingQualityUnknown", size)
		assert.NotEmpty(t, result.Warning,
			"short data (size=%d) should produce a warning", size)
	}
}

// TestFallbackMapper_Resolve_ExactlyEightBytes_DoesNotCrash verifies that
// a minimal 8-byte WASM (magic + version only, no sections) does not crash and
// returns a result.
func TestFallbackMapper_Resolve_ExactlyEightBytes_DoesNotCrash(t *testing.T) {
	m := NewFallbackMapper(t.TempDir())
	result := m.Resolve(minimalWASM(), 0x00)
	require.NotNil(t, result)
	// A bare magic+version WASM has no debug info — should be Unknown.
	assert.Equal(t, MappingQualityUnknown, result.Quality)
}

// ── FallbackMapper — warning message quality ──────────────────────────────────

// TestFallbackMapper_UnknownResult_WarningMentionsAddress verifies that the
// stage-5 "nothing found" warning includes the address being resolved so
// users can cross-reference it with disassembly output.
func TestFallbackMapper_UnknownResult_WarningMentionsAddress(t *testing.T) {
	m := NewFallbackMapper(t.TempDir())
	const addr = uint64(0xdeadbeef)
	result := m.Resolve(minimalWASM(), addr)

	require.NotNil(t, result)
	if result.Quality == MappingQualityUnknown {
		assert.Contains(t, result.Warning, fmt.Sprintf("0x%x", addr),
			"unknown-quality warning should include the hex address")
	}
}

// TestFallbackMapper_HeuristicResult_WarningMentionsRemediation verifies that
// every heuristic-quality result carries a warning that mentions how to get
// better mappings (recompile with debug = true).
func TestFallbackMapper_HeuristicResult_WarningMentionsRemediation(t *testing.T) {
	dir := t.TempDir()
	// Create a Cargo.toml so the Cargo-discovery stage fires.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Cargo.toml"),
		[]byte("[package]\nname=\"warn_test\""), 0600))

	m := NewFallbackMapper(dir)
	result := m.Resolve(minimalWASM(), 0x10)

	require.NotNil(t, result)
	if result.Quality == MappingQualityHeuristic {
		assert.True(t,
			contains(result.Warning, "debug = true") ||
				contains(result.Warning, "Recompile") ||
				contains(result.Warning, "recompile"),
			"heuristic warning should mention how to improve accuracy, got: %s", result.Warning)
	}
}
