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

// ── helpers ───────────────────────────────────────────────────────────────────

// minimalWasm builds a tiny but valid WASM binary that passes magic and
// wasmvalidate checks. Optionally appends a distinguishing suffix byte so
// different "contracts" produce different hashes.
func minimalWasm(suffix byte) []byte {
	// WASM magic + version (8 bytes) — no sections, so section validation passes.
	return []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, suffix}
}

func wasmHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func writeWasm(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0755))
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, data, 0644))
	return path
}

func writeCargoToml(t *testing.T, dir, pkgName string) {
	t.Helper()
	content := "[package]\nname = \"" + pkgName + "\"\nversion = \"0.1.0\"\n"
	require.NoError(t, os.MkdirAll(dir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte(content), 0644))
}

func writeWorkspaceToml(t *testing.T, dir string, members []string) {
	t.Helper()
	quoted := make([]string, len(members))
	for i, m := range members {
		quoted[i] = "    \"" + m + "\""
	}
	content := "[workspace]\nmembers = [\n" + strings.Join(quoted, ",\n") + "\n]\n"
	require.NoError(t, os.MkdirAll(dir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte(content), 0644))
}

const wasmRelPath = "target/wasm32-unknown-unknown/release"

// ── input validation ──────────────────────────────────────────────────────────

func TestDiscoverWorkspaceCandidates_EmptyRoot_Error(t *testing.T) {
	_, err := DiscoverWorkspaceCandidates("", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not be empty")
}

func TestDiscoverWorkspaceCandidates_WhitespaceRoot_Error(t *testing.T) {
	_, err := DiscoverWorkspaceCandidates("   ", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not be empty")
}

func TestDiscoverWorkspaceCandidates_NullByteRoot_Error(t *testing.T) {
	_, err := DiscoverWorkspaceCandidates("/path\x00injection", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "null bytes")
}

func TestDiscoverWorkspaceCandidates_NonExistentRoot_Error(t *testing.T) {
	_, err := DiscoverWorkspaceCandidates("/nonexistent/workspace/root", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestDiscoverWorkspaceCandidates_RootIsFile_Error(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "notadir")
	require.NoError(t, os.WriteFile(filePath, []byte("x"), 0644))
	_, err := DiscoverWorkspaceCandidates(filePath, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a directory")
}

// ── single-crate (no workspace) ───────────────────────────────────────────────

func TestDiscoverWorkspaceCandidates_SingleCrate_NoWasm_EmptyCandidates(t *testing.T) {
	dir := t.TempDir()
	writeCargoToml(t, dir, "my-contract")

	res, err := DiscoverWorkspaceCandidates(dir, "")
	require.NoError(t, err)
	assert.Empty(t, res.Candidates)
	assert.Empty(t, res.ExactMatches)
	assert.False(t, res.Ambiguous)
}

func TestDiscoverWorkspaceCandidates_SingleCrate_WithWasm_FoundCandidate(t *testing.T) {
	dir := t.TempDir()
	writeCargoToml(t, dir, "my-contract")

	wasm := minimalWasm(0xAA)
	targetDir := filepath.Join(dir, wasmRelPath)
	writeWasm(t, targetDir, "my_contract.wasm", wasm)

	res, err := DiscoverWorkspaceCandidates(dir, "")
	require.NoError(t, err)
	require.Len(t, res.Candidates, 1)
	assert.Equal(t, wasmHash(wasm), res.Candidates[0].WasmHash)
	assert.False(t, res.Candidates[0].MatchesOnChain, "no onChainHash supplied → no match")
}

func TestDiscoverWorkspaceCandidates_SingleCrate_HashMatch(t *testing.T) {
	dir := t.TempDir()
	writeCargoToml(t, dir, "token-contract")

	wasm := minimalWasm(0xBB)
	hash := wasmHash(wasm)
	targetDir := filepath.Join(dir, wasmRelPath)
	writeWasm(t, targetDir, "token_contract.wasm", wasm)

	res, err := DiscoverWorkspaceCandidates(dir, hash)
	require.NoError(t, err)
	require.Len(t, res.ExactMatches, 1)
	assert.Equal(t, hash, res.ExactMatches[0].WasmHash)
	assert.True(t, res.ExactMatches[0].MatchesOnChain)
	assert.False(t, res.Ambiguous)

	best := res.BestCandidate()
	require.NotNil(t, best)
	assert.Equal(t, hash, best.WasmHash)
}

// ── workspace with multiple members ───────────────────────────────────────────

func TestDiscoverWorkspaceCandidates_WorkspaceMultiMember_OnlyMatchReturned(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceToml(t, root, []string{"contracts/token", "contracts/nft", "contracts/dao"})

	tokenWasm := minimalWasm(0x01)
	nftWasm := minimalWasm(0x02)
	daoWasm := minimalWasm(0x03)
	targetHash := wasmHash(nftWasm)

	for _, member := range []struct {
		path string
		name string
		data []byte
	}{
		{"contracts/token", "token-contract", tokenWasm},
		{"contracts/nft", "nft-contract", nftWasm},
		{"contracts/dao", "dao-contract", daoWasm},
	} {
		memberDir := filepath.Join(root, member.path)
		writeCargoToml(t, memberDir, member.name)
		writeWasm(t, filepath.Join(memberDir, wasmRelPath), member.name+".wasm", member.data)
	}

	res, err := DiscoverWorkspaceCandidates(root, targetHash)
	require.NoError(t, err)
	require.Len(t, res.ExactMatches, 1, "only nft contract should match")
	assert.Equal(t, targetHash, res.ExactMatches[0].WasmHash)
	assert.False(t, res.Ambiguous)

	// All 3 candidates must be discovered.
	assert.Len(t, res.Candidates, 3)
	// Exact match should be sorted first.
	assert.True(t, res.Candidates[0].MatchesOnChain)
}

func TestDiscoverWorkspaceCandidates_WorkspaceMembersListed(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceToml(t, root, []string{"crates/alpha", "crates/beta"})

	for _, sub := range []string{"alpha", "beta"} {
		d := filepath.Join(root, "crates", sub)
		writeCargoToml(t, d, sub)
	}

	res, err := DiscoverWorkspaceCandidates(root, "")
	require.NoError(t, err)
	assert.Len(t, res.Members, 2)
	assert.Equal(t, root, res.WorkspaceRoot)
}

// ── ambiguity detection ───────────────────────────────────────────────────────

func TestDiscoverWorkspaceCandidates_AmbiguousMatch_DiagnosticSet(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceToml(t, root, []string{"crates/a", "crates/b"})

	// Both crates produce an identical WASM (same content → same hash).
	identical := minimalWasm(0xFF)
	hash := wasmHash(identical)

	for _, sub := range []string{"a", "b"} {
		d := filepath.Join(root, "crates", sub)
		writeCargoToml(t, d, sub)
		writeWasm(t, filepath.Join(d, wasmRelPath), sub+".wasm", identical)
	}

	res, err := DiscoverWorkspaceCandidates(root, hash)
	require.NoError(t, err)
	assert.True(t, res.Ambiguous, "two identical hashes should be ambiguous")
	assert.Len(t, res.ExactMatches, 2)
	assert.NotEmpty(t, res.AmbiguityDiagnostic)
	assert.Contains(t, res.AmbiguityDiagnostic, "--contract-source",
		"diagnostic should suggest --contract-source as a remedy")
	assert.Contains(t, res.AmbiguityDiagnostic, "cargo build -p",
		"diagnostic should suggest targeted rebuild")

	// BestCandidate returns nil when ambiguous.
	assert.Nil(t, res.BestCandidate())
}

func TestDiscoverWorkspaceCandidates_NoMatch_BestCandidateNil(t *testing.T) {
	root := t.TempDir()
	writeCargoToml(t, root, "my-contract")
	writeWasm(t, filepath.Join(root, wasmRelPath), "my_contract.wasm", minimalWasm(0x11))

	res, err := DiscoverWorkspaceCandidates(root, strings.Repeat("0", 64))
	require.NoError(t, err)
	assert.Nil(t, res.BestCandidate(), "no hash match → BestCandidate is nil")
	assert.Len(t, res.Candidates, 1, "candidate still discovered even if no match")
}

// ── shared workspace target dir ───────────────────────────────────────────────

func TestDiscoverWorkspaceCandidates_SharedTargetDir_Discovered(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceToml(t, root, []string{"contracts/foo"})

	fooDir := filepath.Join(root, "contracts", "foo")
	writeCargoToml(t, fooDir, "foo")

	// No per-member target — only shared workspace target.
	sharedWasm := minimalWasm(0x77)
	hash := wasmHash(sharedWasm)
	sharedTarget := filepath.Join(root, wasmRelPath)
	writeWasm(t, sharedTarget, "foo.wasm", sharedWasm)

	res, err := DiscoverWorkspaceCandidates(root, hash)
	require.NoError(t, err)
	require.Len(t, res.Candidates, 1, "WASM in shared target should be discovered")
	assert.True(t, res.Candidates[0].MatchesOnChain)
}

// ── unrelated members ignored ─────────────────────────────────────────────────

func TestDiscoverWorkspaceCandidates_UnrelatedMemberHasNoWasm_Ignored(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceToml(t, root, []string{"lib", "contract"})

	// lib/ is a pure library — no WASM output.
	libDir := filepath.Join(root, "lib")
	writeCargoToml(t, libDir, "my-lib")

	// contract/ produces WASM.
	contractDir := filepath.Join(root, "contract")
	writeCargoToml(t, contractDir, "my-contract")
	contractWasm := minimalWasm(0x99)
	writeWasm(t, filepath.Join(contractDir, wasmRelPath), "my_contract.wasm", contractWasm)

	res, err := DiscoverWorkspaceCandidates(root, wasmHash(contractWasm))
	require.NoError(t, err)
	assert.Len(t, res.Candidates, 1, "only the member that produced WASM should be a candidate")
	assert.Len(t, res.ExactMatches, 1)
}

// ── bad magic / validation warnings ──────────────────────────────────────────

func TestDiscoverWorkspaceCandidates_BadMagicFile_Warning(t *testing.T) {
	root := t.TempDir()
	writeCargoToml(t, root, "my-contract")

	targetDir := filepath.Join(root, wasmRelPath)
	// Write a file with ELF magic instead of WASM magic.
	writeWasm(t, targetDir, "bad.wasm", []byte{0x7f, 0x45, 0x4c, 0x46, 0x00, 0x00, 0x00, 0x00})
	// Write a valid WASM alongside.
	writeWasm(t, targetDir, "good.wasm", minimalWasm(0xAA))

	res, err := DiscoverWorkspaceCandidates(root, "")
	require.NoError(t, err)
	assert.Len(t, res.Candidates, 1, "only valid WASM should be indexed")
	assert.Len(t, res.Warnings, 1, "bad-magic file should produce a warning")
	assert.Contains(t, res.Warnings[0], "bad.wasm")
}

// ── nested workspace layout ───────────────────────────────────────────────────

func TestDiscoverWorkspaceCandidates_NestedWorkspace_ThreeLevels(t *testing.T) {
	// Layout:
	//   root/
	//     Cargo.toml  (workspace with members: ["packages/alpha", "packages/beta/core"])
	//     packages/alpha/Cargo.toml
	//     packages/beta/core/Cargo.toml
	//     packages/alpha/target/wasm32-unknown-unknown/release/alpha.wasm
	//     packages/beta/core/target/wasm32-unknown-unknown/release/core.wasm
	root := t.TempDir()
	writeWorkspaceToml(t, root, []string{"packages/alpha", "packages/beta/core"})

	alphaDir := filepath.Join(root, "packages", "alpha")
	coreDir := filepath.Join(root, "packages", "beta", "core")
	writeCargoToml(t, alphaDir, "alpha")
	writeCargoToml(t, coreDir, "core")

	alphaWasm := minimalWasm(0xA1)
	coreWasm := minimalWasm(0xC1)
	writeWasm(t, filepath.Join(alphaDir, wasmRelPath), "alpha.wasm", alphaWasm)
	writeWasm(t, filepath.Join(coreDir, wasmRelPath), "core.wasm", coreWasm)

	res, err := DiscoverWorkspaceCandidates(root, wasmHash(coreWasm))
	require.NoError(t, err)
	assert.Len(t, res.Members, 2)
	assert.Len(t, res.Candidates, 2)
	require.Len(t, res.ExactMatches, 1, "only core should match")
	assert.Equal(t, wasmHash(coreWasm), res.ExactMatches[0].WasmHash)
	assert.False(t, res.Ambiguous)
}

// ── parseWorkspaceMembersFromContent ─────────────────────────────────────────

func TestParseWorkspaceMembersFromContent_MultiLine(t *testing.T) {
	content := `
[workspace]
members = [
    "contracts/token",
    "contracts/nft",
]
`
	members := parseWorkspaceMembersFromContent(content, "/project")
	assert.Equal(t, []string{"/project/contracts/token", "/project/contracts/nft"}, members)
}

func TestParseWorkspaceMembersFromContent_InlineArray(t *testing.T) {
	content := `[workspace]
members = ["alpha", "beta"]
`
	members := parseWorkspaceMembersFromContent(content, "/root")
	assert.Equal(t, []string{"/root/alpha", "/root/beta"}, members)
}

func TestParseWorkspaceMembersFromContent_NoWorkspace_Nil(t *testing.T) {
	content := `[package]
name = "standalone"
`
	members := parseWorkspaceMembersFromContent(content, "/root")
	assert.Empty(t, members)
}

func TestParseWorkspaceMembersFromContent_CommentsIgnored(t *testing.T) {
	content := `
[workspace]
members = [
    # "old-contract",
    "current-contract",
]
`
	members := parseWorkspaceMembersFromContent(content, "/root")
	assert.Equal(t, []string{"/root/current-contract"}, members)
}

// ── deduplicateMembers ────────────────────────────────────────────────────────

func TestDeduplicateMembers_RemovesDuplicates(t *testing.T) {
	dirs := []string{"/a", "/b", "/a", "/c", "/b"}
	deduped := deduplicateMembers(dirs)
	assert.Equal(t, []string{"/a", "/b", "/c"}, deduped)
}

func TestDeduplicateMembers_EmptySlice(t *testing.T) {
	assert.Empty(t, deduplicateMembers(nil))
}
