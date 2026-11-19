// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package sourcemap

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── DiscoverLocalSymbols — input validation ───────────────────────────────────

func TestDiscoverLocalSymbols_EmptyProjectRoot_ReturnsError(t *testing.T) {
	result, err := DiscoverLocalSymbols("")
	assert.Nil(t, result)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "projectRoot must not be empty",
		"error should state that projectRoot is required")
	assert.Contains(t, err.Error(), "--contract-source",
		"error should mention --contract-source as a remedy")
}

func TestDiscoverLocalSymbols_NonExistentProjectRoot_ReturnsDescriptiveError(t *testing.T) {
	result, err := DiscoverLocalSymbols("/nonexistent/project/root/that/does/not/exist")
	// Should return a non-nil result (empty) and an error with guidance.
	require.NotNil(t, result, "should return a non-nil DiscoveryResult even on error")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found",
		"error should say directory was not found")
	assert.Contains(t, err.Error(), "cargo build",
		"error should suggest running cargo build")
	assert.Contains(t, err.Error(), "--contract-source",
		"error should suggest --contract-source as an alternative")
}

func TestDiscoverLocalSymbols_PathIsFile_ReturnsDescriptiveError(t *testing.T) {
	dir := t.TempDir()
	// Create the full target path as a regular file instead of a directory.
	targetDir := filepath.Join(dir, "target", "wasm32-unknown-unknown", "release")
	require.NoError(t, os.MkdirAll(filepath.Dir(targetDir), 0755))
	require.NoError(t, os.WriteFile(targetDir, []byte("not a dir"), 0644))

	result, err := DiscoverLocalSymbols(dir)
	require.NotNil(t, result)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected a directory",
		"error should clarify that a directory is required")
}

// ── DiscoverLocalSymbols — successful scan ────────────────────────────────────

func TestDiscoverLocalSymbols_EmptyBuildDir_ReturnsEmptyResult(t *testing.T) {
	dir := t.TempDir()
	targetDir := filepath.Join(dir, "target", "wasm32-unknown-unknown", "release")
	require.NoError(t, os.MkdirAll(targetDir, 0755))

	result, err := DiscoverLocalSymbols(dir)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Empty(t, result.Found, "no WASM files → empty Found map")
	assert.Empty(t, result.Warnings)
	assert.Equal(t, targetDir, result.SearchDir)
}

func TestDiscoverLocalSymbols_WasmFilesIndexedByHash(t *testing.T) {
	dir := t.TempDir()
	targetDir := filepath.Join(dir, "target", "wasm32-unknown-unknown", "release")
	require.NoError(t, os.MkdirAll(targetDir, 0755))

	// Write two minimal WASM files with known content.
	wasm1 := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, 0xAA}
	wasm2 := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, 0xBB}
	wasm1Path := filepath.Join(targetDir, "contract_a.wasm")
	wasm2Path := filepath.Join(targetDir, "contract_b.wasm")
	require.NoError(t, os.WriteFile(wasm1Path, wasm1, 0644))
	require.NoError(t, os.WriteFile(wasm2Path, wasm2, 0644))
	// Also write a non-WASM file that should be ignored.
	require.NoError(t, os.WriteFile(filepath.Join(targetDir, "notes.txt"), []byte("ignore me"), 0644))

	result, err := DiscoverLocalSymbols(dir)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, result.Found, 2, "only .wasm files should be indexed")

	hash1 := sha256hash(wasm1)
	hash2 := sha256hash(wasm2)
	assert.Equal(t, wasm1Path, result.Found[hash1], "contract_a.wasm path should be under its hash")
	assert.Equal(t, wasm2Path, result.Found[hash2], "contract_b.wasm path should be under its hash")
}

func TestDiscoverLocalSymbols_SubdirectoriesIgnored(t *testing.T) {
	dir := t.TempDir()
	targetDir := filepath.Join(dir, "target", "wasm32-unknown-unknown", "release")
	require.NoError(t, os.MkdirAll(targetDir, 0755))

	// Create a subdirectory with a WASM inside — should NOT be indexed.
	subDir := filepath.Join(targetDir, "subdir")
	require.NoError(t, os.MkdirAll(subDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(subDir, "nested.wasm"),
		[]byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}, 0644))

	result, err := DiscoverLocalSymbols(dir)
	require.NoError(t, err)
	assert.Empty(t, result.Found, "files inside subdirectories should be skipped")
}

// ── DiscoverLocalSymbols — unreadable file → warning, not hard error ─────────

func TestDiscoverLocalSymbols_UnreadableFile_AddedToWarnings(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root — permission test not meaningful")
	}

	dir := t.TempDir()
	targetDir := filepath.Join(dir, "target", "wasm32-unknown-unknown", "release")
	require.NoError(t, os.MkdirAll(targetDir, 0755))

	wasmContent := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	goodPath := filepath.Join(targetDir, "good.wasm")
	badPath := filepath.Join(targetDir, "unreadable.wasm")
	require.NoError(t, os.WriteFile(goodPath, wasmContent, 0644))
	require.NoError(t, os.WriteFile(badPath, wasmContent, 0000))
	t.Cleanup(func() { _ = os.Chmod(badPath, 0644) })

	result, err := DiscoverLocalSymbols(dir)
	require.NoError(t, err, "unreadable file should produce a warning, not an error")
	require.NotNil(t, result)
	assert.Len(t, result.Found, 1, "only the readable WASM should be in Found")
	assert.Len(t, result.Warnings, 1, "should have one warning for the unreadable file")
	assert.Contains(t, result.Warnings[0], "unreadable.wasm")
}

// ── CheckHashMismatch — input validation ─────────────────────────────────────

func TestCheckHashMismatch_EmptyPath_ReturnsError(t *testing.T) {
	err := CheckHashMismatch("", "somehash")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not be empty")
}

func TestCheckHashMismatch_EmptyHash_ReturnsError(t *testing.T) {
	err := CheckHashMismatch("/some/path.wasm", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not be empty")
}

func TestCheckHashMismatch_NonExistentFile_ReturnsError(t *testing.T) {
	err := CheckHashMismatch("/nonexistent/contract.wasm", "abcdef")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read WASM file")
}

func TestCheckHashMismatch_InvalidMagic_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	wasmPath := filepath.Join(dir, "contract.wasm")
	require.NoError(t, os.WriteFile(wasmPath, []byte("not_wasm_data_here"), 0644))

	err := CheckHashMismatch(wasmPath, "abcdef")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a valid WASM binary")
}

func TestCheckHashMismatch_MismatchedHash_ReturnsHashMismatchError(t *testing.T) {
	dir := t.TempDir()
	wasmPath := filepath.Join(dir, "contract.wasm")
	content := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	require.NoError(t, os.WriteFile(wasmPath, content, 0644))

	err := CheckHashMismatch(wasmPath, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	require.Error(t, err)

	var mismatch *HashMismatchError
	require.ErrorAs(t, err, &mismatch)
	assert.Equal(t, wasmPath, mismatch.Path)
	assert.NotEmpty(t, mismatch.Local)
	assert.Equal(t, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef", mismatch.OnChain)
	// Error message should include remediation hint.
	assert.Contains(t, err.Error(), "cargo build")
}

func TestCheckHashMismatch_MatchingHash_ReturnsNil(t *testing.T) {
	dir := t.TempDir()
	wasmPath := filepath.Join(dir, "contract.wasm")
	content := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	require.NoError(t, os.WriteFile(wasmPath, content, 0644))

	sum := sha256.Sum256(content)
	correctHash := hex.EncodeToString(sum[:])

	err := CheckHashMismatch(wasmPath, correctHash)
	assert.NoError(t, err)
}

// ── HashMismatchError.Error message ──────────────────────────────────────────

func TestHashMismatchError_MessageContainsHint(t *testing.T) {
	e := &HashMismatchError{
		Path:    "/tmp/contract.wasm",
		Local:   "aabbcc",
		OnChain: "112233",
	}
	msg := e.Error()
	assert.Contains(t, msg, "aabbcc", "message should include local hash")
	assert.Contains(t, msg, "112233", "message should include on-chain hash")
	assert.True(t, strings.Contains(msg, "cargo build") || strings.Contains(msg, "--opt-level"),
		"message should include a remediation hint")
}

// ── DiscoverLocalSymbolsLegacy — backward-compat shim ────────────────────────

func TestDiscoverLocalSymbolsLegacy_EmptyRoot_ReturnsError(t *testing.T) {
	m, err := DiscoverLocalSymbolsLegacy("")
	assert.Nil(t, m)
	require.Error(t, err)
}

func TestDiscoverLocalSymbolsLegacy_ValidDir_ReturnsFlatMap(t *testing.T) {
	dir := t.TempDir()
	targetDir := filepath.Join(dir, "target", "wasm32-unknown-unknown", "release")
	require.NoError(t, os.MkdirAll(targetDir, 0755))

	content := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, 0xFF}
	require.NoError(t, os.WriteFile(filepath.Join(targetDir, "c.wasm"), content, 0644))

	m, err := DiscoverLocalSymbolsLegacy(dir)
	require.NoError(t, err)
	assert.Len(t, m, 1)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func sha256hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ── DiscoverLocalSymbols — WASM magic byte validation ────────────────────────

// TestDiscoverLocalSymbols_BadMagicFile_AddedToWarnings verifies that a file
// ending in .wasm but not starting with the WASM magic bytes (\0asm) is
// skipped with a warning rather than being silently hashed and indexed.
// This prevents corrupt or misnamed files from poisoning the hash table.
func TestDiscoverLocalSymbols_BadMagicFile_AddedToWarnings(t *testing.T) {
	dir := t.TempDir()
	targetDir := filepath.Join(dir, "target", "wasm32-unknown-unknown", "release")
	require.NoError(t, os.MkdirAll(targetDir, 0755))

	// Write a file named .wasm but with ELF magic bytes — not valid WASM.
	badContent := []byte{0x7f, 0x45, 0x4c, 0x46, 0x00, 0x00, 0x00, 0x00}
	require.NoError(t, os.WriteFile(
		filepath.Join(targetDir, "fake.wasm"), badContent, 0644))

	// Also write a genuinely valid WASM file.
	goodContent := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	require.NoError(t, os.WriteFile(
		filepath.Join(targetDir, "real.wasm"), goodContent, 0644))

	result, err := DiscoverLocalSymbols(dir)
	require.NoError(t, err, "bad magic file should produce a warning, not an error")
	require.NotNil(t, result)

	// Only the valid WASM should be indexed.
	assert.Len(t, result.Found, 1, "only the valid WASM should be in Found")
	// The bad file must appear as a warning.
	assert.Len(t, result.Warnings, 1, "should have one warning for the bad-magic file")
	assert.Contains(t, result.Warnings[0], "fake.wasm",
		"warning should name the offending file")
	assert.Contains(t, result.Warnings[0], "magic",
		"warning should mention WASM magic bytes")
}

// TestDiscoverLocalSymbols_TooSmallFile_NotIndexed verifies that a .wasm file
// shorter than 4 bytes (cannot contain a valid magic number) is skipped.
func TestDiscoverLocalSymbols_TooSmallFile_NotIndexed(t *testing.T) {
	dir := t.TempDir()
	targetDir := filepath.Join(dir, "target", "wasm32-unknown-unknown", "release")
	require.NoError(t, os.MkdirAll(targetDir, 0755))

	// Write a 2-byte file named .wasm — too short to hold magic bytes.
	require.NoError(t, os.WriteFile(
		filepath.Join(targetDir, "tiny.wasm"), []byte{0x00, 0x61}, 0644))

	result, err := DiscoverLocalSymbols(dir)
	require.NoError(t, err)
	assert.Empty(t, result.Found, "too-small file should not be indexed")
	assert.Len(t, result.Warnings, 1, "should warn about the too-small file")
}

// TestDiscoverLocalSymbols_AllValidWasm_NoWarnings verifies that a directory
// containing only valid WASM files produces no warnings.
func TestDiscoverLocalSymbols_AllValidWasm_NoWarnings(t *testing.T) {
	dir := t.TempDir()
	targetDir := filepath.Join(dir, "target", "wasm32-unknown-unknown", "release")
	require.NoError(t, os.MkdirAll(targetDir, 0755))

	goodContent := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	require.NoError(t, os.WriteFile(
		filepath.Join(targetDir, "a.wasm"), goodContent, 0644))

	result, err := DiscoverLocalSymbols(dir)
	require.NoError(t, err)
	assert.Len(t, result.Found, 1)
	assert.Empty(t, result.Warnings, "valid WASM files should not produce warnings")
}

// ── DiscoverLocalSymbols — projectRoot input sanitisation ────────────────────

// TestDiscoverLocalSymbols_WhitespaceOnlyRoot_ReturnsError verifies that a
// whitespace-only projectRoot is treated the same as an empty root — an
// explicit error is returned immediately without touching the filesystem.
func TestDiscoverLocalSymbols_WhitespaceOnlyRoot_ReturnsError(t *testing.T) {
	result, err := DiscoverLocalSymbols("   ")
	assert.Nil(t, result)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "projectRoot must not be empty",
		"whitespace-only root should produce the same 'must not be empty' error")
}

// TestDiscoverLocalSymbols_NullByteInRoot_ReturnsError verifies that a
// projectRoot containing a null byte is rejected before any filesystem access.
func TestDiscoverLocalSymbols_NullByteInRoot_ReturnsError(t *testing.T) {
	result, err := DiscoverLocalSymbols("/valid/path\x00injection")
	assert.Nil(t, result)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "null bytes",
		"null byte in projectRoot should produce a clear error")
}

// ── CheckHashMismatch — message quality ──────────────────────────────────────

// TestCheckHashMismatch_MessageSaysBuildMismatch verifies that the error
// message no longer says "opt-level mismatch" (which was misleading) and
// instead uses broader language like "build mismatch" or "hash mismatch".
func TestCheckHashMismatch_MessageSaysBuildMismatch(t *testing.T) {
	dir := t.TempDir()
	wasmPath := filepath.Join(dir, "contract.wasm")
	content := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	require.NoError(t, os.WriteFile(wasmPath, content, 0644))

	err := CheckHashMismatch(wasmPath, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	require.Error(t, err)

	msg := err.Error()
	// Must not use the misleading "opt-level mismatch" wording.
	assert.NotContains(t, msg, "opt-level mismatch",
		"error message should not say 'opt-level mismatch' — the cause may be unrelated to opt-level")
	// Must use broader, more accurate language.
	assert.True(t,
		strings.Contains(msg, "build mismatch") || strings.Contains(msg, "hash mismatch"),
		"error should describe the mismatch in general terms, got: %q", msg)
	// Must still include the remediation hint.
	assert.Contains(t, msg, "cargo build",
		"error should still suggest cargo build as a remedy")
}

// ── hasWasmMagic — unit tests ─────────────────────────────────────────────────

func TestHasWasmMagic_ValidMagic_ReturnsTrue(t *testing.T) {
	data := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	assert.True(t, hasWasmMagic(data))
}

func TestHasWasmMagic_InvalidMagic_ReturnsFalse(t *testing.T) {
	// ELF magic
	assert.False(t, hasWasmMagic([]byte{0x7f, 0x45, 0x4c, 0x46}))
	// PDF header
	assert.False(t, hasWasmMagic([]byte{'%', 'P', 'D', 'F'}))
}

func TestHasWasmMagic_TooShort_ReturnsFalse(t *testing.T) {
	assert.False(t, hasWasmMagic([]byte{0x00, 0x61}))
	assert.False(t, hasWasmMagic(nil))
	assert.False(t, hasWasmMagic([]byte{}))
}
