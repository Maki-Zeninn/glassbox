// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package sourcemap — workspace_discovery.go
//
// WorkspaceDiscovery resolves the correct Soroban WASM artifact and source root
// for each member of a Cargo workspace.  The problem it solves:
//
//   - A workspace may contain many crates; only some compile to WASM contracts.
//   - The build output lives under each member's own target/ tree (or a shared
//     workspace target/).
//   - DiscoverLocalSymbols only searches one root, so it silently picks the
//     wrong artifact when multiple contracts share a workspace.
//
// This file adds:
//   - WorkspaceCandidate   — a (crate_name, wasm_path, hash) triple.
//   - WorkspaceDiscoveryResult — ranked candidates + ambiguity diagnostics.
//   - DiscoverWorkspaceCandidates — the entry point that parses workspace
//     metadata, builds a candidate graph, ranks by hash, and reports ambiguity.
package sourcemap

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dotandev/glassbox/internal/logger"
	"github.com/dotandev/glassbox/internal/wasmvalidate"
)

// WorkspaceCandidate describes a single WASM artifact discovered inside a
// Cargo workspace member.
type WorkspaceCandidate struct {
	// CrateName is the [package] name from Cargo.toml.
	CrateName string
	// CrateDir is the absolute path to the crate directory (contains Cargo.toml).
	CrateDir string
	// WasmPath is the absolute path to the compiled WASM artifact.
	WasmPath string
	// WasmHash is the SHA-256 hex digest of the WASM binary.
	WasmHash string
	// MatchesOnChain is true when WasmHash equals the caller-supplied on-chain hash.
	MatchesOnChain bool
	// IsWorkspaceMember is true when this crate is listed in the workspace
	// [workspace] members table (as opposed to a transitive dependency).
	IsWorkspaceMember bool
}

// WorkspaceDiscoveryResult is the output of DiscoverWorkspaceCandidates.
type WorkspaceDiscoveryResult struct {
	// WorkspaceRoot is the directory that contains the root Cargo.toml with a
	// [workspace] section.  Empty when no workspace was found.
	WorkspaceRoot string
	// Members lists the crate directories that are workspace members.
	Members []string
	// Candidates is every WASM artifact found across all member target dirs,
	// sorted so exact on-chain matches come first.
	Candidates []WorkspaceCandidate
	// ExactMatches contains only the candidates where MatchesOnChain is true.
	ExactMatches []WorkspaceCandidate
	// Ambiguous is true when more than one candidate matches the on-chain hash.
	Ambiguous bool
	// Warnings lists non-fatal issues encountered during scanning.
	Warnings []string
	// AmbiguityDiagnostic is a human-readable message printed when Ambiguous is
	// true, explaining which artifacts matched and how to resolve the conflict.
	AmbiguityDiagnostic string
}

// DiscoverWorkspaceCandidates scans a Cargo workspace rooted at workspaceRoot,
// discovers all WASM artifacts produced by workspace members, and ranks them
// by their SHA-256 hash against onChainHash.
//
// Validation:
//   - workspaceRoot must not be empty or whitespace-only.
//   - workspaceRoot must not contain null bytes.
//   - workspaceRoot must exist and be a directory.
//   - onChainHash may be empty; when it is, all candidates are returned without
//     hash-match ranking (MatchesOnChain is always false).
//
// Discovery strategy:
//  1. Read the root Cargo.toml and parse the [workspace] members list.
//  2. For each member, look for Cargo.toml to extract the package name.
//  3. Scan the standard target directories for .wasm files:
//     a. <member>/target/wasm32-unknown-unknown/release/
//     b. <workspaceRoot>/target/wasm32-unknown-unknown/release/  (shared target)
//  4. For each .wasm file: validate magic bytes + structural integrity,
//     compute SHA-256, compare to onChainHash.
//  5. Sort candidates: exact matches first, then by crate name for determinism.
//  6. If more than one candidate has MatchesOnChain=true, set Ambiguous=true
//     and populate AmbiguityDiagnostic with remediation guidance.
func DiscoverWorkspaceCandidates(workspaceRoot, onChainHash string) (*WorkspaceDiscoveryResult, error) {
	if strings.TrimSpace(workspaceRoot) == "" {
		return nil, fmt.Errorf(
			"workspace discovery: workspaceRoot must not be empty\n" +
				"  Hint: pass the path to the Cargo workspace root " +
				"(the directory that contains the [workspace] Cargo.toml).",
		)
	}
	if strings.ContainsRune(workspaceRoot, 0) {
		return nil, fmt.Errorf(
			"workspace discovery: workspaceRoot contains null bytes and cannot be used\n" +
				"  Hint: remove null bytes from the path.",
		)
	}

	info, err := os.Stat(workspaceRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf(
				"workspace discovery: directory not found: %q\n"+
					"  Hint: verify the workspace root path is correct.",
				workspaceRoot,
			)
		}
		return nil, fmt.Errorf("workspace discovery: cannot access %q: %w", workspaceRoot, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf(
			"workspace discovery: %q is a file, not a directory\n"+
				"  Hint: provide the workspace root directory, not a file.",
			workspaceRoot,
		)
	}

	result := &WorkspaceDiscoveryResult{WorkspaceRoot: workspaceRoot}

	// ── Step 1: parse workspace members ──────────────────────────────────────
	rootToml := filepath.Join(workspaceRoot, "Cargo.toml")
	rootContent, err := os.ReadFile(rootToml)
	if err != nil {
		// Not a fatal error — may be a non-workspace single-crate project.
		// Fall back to treating workspaceRoot as the sole member.
		logger.Logger.Debug("workspace discovery: no root Cargo.toml found; treating as single-crate project",
			"root", workspaceRoot)
		result.Members = []string{workspaceRoot}
	} else {
		members := parseWorkspaceMembersFromContent(string(rootContent), workspaceRoot)
		if len(members) == 0 {
			// Root Cargo.toml exists but has no [workspace] section — single crate.
			result.Members = []string{workspaceRoot}
		} else {
			result.Members = members
			result.WorkspaceRoot = workspaceRoot
		}
	}

	// Deduplicate and validate members.
	result.Members = deduplicateMembers(result.Members)

	// ── Step 2 & 3: scan each member for WASM artifacts ──────────────────────
	seen := make(map[string]bool) // avoid counting the same file twice
	sharedTargetDir := filepath.Join(workspaceRoot, wasmTargetPath)

	for _, memberDir := range result.Members {
		pkgName := parseCratePackageName(memberDir)

		// Per-member target dir (created when the member has its own target/).
		memberTargetDir := filepath.Join(memberDir, wasmTargetPath)
		result.scanTargetDir(memberTargetDir, memberDir, pkgName, true, onChainHash, seen)
	}

	// Shared workspace target dir (produced when workspace uses a single target/).
	result.scanTargetDir(sharedTargetDir, workspaceRoot, "", false, onChainHash, seen)

	// ── Step 4: rank candidates ───────────────────────────────────────────────
	sort.Slice(result.Candidates, func(i, j int) bool {
		ci, cj := result.Candidates[i], result.Candidates[j]
		// Exact hash matches first.
		if ci.MatchesOnChain != cj.MatchesOnChain {
			return ci.MatchesOnChain
		}
		// Then workspace members before non-members.
		if ci.IsWorkspaceMember != cj.IsWorkspaceMember {
			return ci.IsWorkspaceMember
		}
		// Stable sort by crate name then wasm path.
		if ci.CrateName != cj.CrateName {
			return ci.CrateName < cj.CrateName
		}
		return ci.WasmPath < cj.WasmPath
	})

	// Collect exact matches.
	for _, c := range result.Candidates {
		if c.MatchesOnChain {
			result.ExactMatches = append(result.ExactMatches, c)
		}
	}

	// ── Step 5: detect and describe ambiguity ─────────────────────────────────
	if len(result.ExactMatches) > 1 {
		result.Ambiguous = true
		result.AmbiguityDiagnostic = buildAmbiguityDiagnostic(result.ExactMatches, onChainHash)
	}

	return result, nil
}

// BestCandidate returns the single best WASM candidate to use for source
// mapping, or nil when the result is ambiguous or no candidates matched.
//
// It returns nil (not an error) so callers can fall through to alternative
// discovery strategies without hard-failing.  The caller should check
// result.Ambiguous and present result.AmbiguityDiagnostic to the user when
// nil is returned due to ambiguity.
func (r *WorkspaceDiscoveryResult) BestCandidate() *WorkspaceCandidate {
	if r.Ambiguous || len(r.ExactMatches) == 0 {
		return nil
	}
	c := r.ExactMatches[0]
	return &c
}

// ── internal helpers ──────────────────────────────────────────────────────────

// scanTargetDir walks a single WASM release directory, validates each .wasm
// file, and appends a WorkspaceCandidate to result.Candidates.
func (r *WorkspaceDiscoveryResult) scanTargetDir(
	targetDir, crateDir, crateName string,
	isWorkspaceMember bool,
	onChainHash string,
	seen map[string]bool,
) {
	if _, err := os.Stat(targetDir); err != nil {
		// Target dir not present — member may not have been compiled yet.
		return
	}

	entries, err := os.ReadDir(targetDir)
	if err != nil {
		r.Warnings = append(r.Warnings,
			fmt.Sprintf("workspace discovery: cannot read %q: %v (skipped)", targetDir, err))
		return
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".wasm") {
			continue
		}

		fullPath := filepath.Join(targetDir, entry.Name())
		if seen[fullPath] {
			continue
		}
		seen[fullPath] = true

		content, readErr := os.ReadFile(fullPath)
		if readErr != nil {
			r.Warnings = append(r.Warnings,
				fmt.Sprintf("workspace discovery: cannot read %q: %v (skipped)", fullPath, readErr))
			continue
		}

		if !hasWasmMagic(content) {
			r.Warnings = append(r.Warnings,
				fmt.Sprintf("workspace discovery: %q lacks WASM magic bytes (\\0asm) — skipped", fullPath))
			continue
		}

		if rep := wasmvalidate.Validate(content, wasmvalidate.DefaultLimits()); !rep.OK {
			r.Warnings = append(r.Warnings,
				fmt.Sprintf("workspace discovery: %q failed WASM validation and was skipped: %v",
					fullPath, rep.Error()))
			continue
		}

		sum := sha256.Sum256(content)
		hash := hex.EncodeToString(sum[:])

		// Infer the crate name from the file name when we don't have it from Cargo.toml.
		// e.g. "my_contract.wasm" → "my_contract"  (shared target dir case)
		name := crateName
		if name == "" {
			name = strings.TrimSuffix(entry.Name(), ".wasm")
		}

		candidate := WorkspaceCandidate{
			CrateName:         name,
			CrateDir:          crateDir,
			WasmPath:          fullPath,
			WasmHash:          hash,
			MatchesOnChain:    onChainHash != "" && hash == onChainHash,
			IsWorkspaceMember: isWorkspaceMember,
		}
		r.Candidates = append(r.Candidates, candidate)

		logger.Logger.Debug("workspace discovery: indexed candidate",
			"crate", name,
			"wasm", filepath.Base(fullPath),
			"hash", hash[:12]+"…",
			"match", candidate.MatchesOnChain,
		)
	}
}

// buildAmbiguityDiagnostic produces an actionable error message when multiple
// WASM artifacts have the same on-chain hash.
func buildAmbiguityDiagnostic(matches []WorkspaceCandidate, onChainHash string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(
		"Ambiguous WASM resolution: %d workspace members produced an artifact "+
			"with hash %s.\n", len(matches), onChainHash))
	sb.WriteString("  Matching artifacts:\n")
	for _, m := range matches {
		sb.WriteString(fmt.Sprintf("    • %s  (%s)\n", m.WasmPath, m.CrateName))
	}
	sb.WriteString("\n  To resolve:\n")
	sb.WriteString("    1. Use --contract-source <path> to point directly at the " +
		"correct contract's source directory.\n")
	sb.WriteString("    2. Or rebuild only the target contract so its hash is unique:\n")
	sb.WriteString("       cargo build -p <crate-name> --release --target wasm32-unknown-unknown\n")
	return sb.String()
}

// parseWorkspaceMembersFromContent parses the [workspace] members list from
// the content of a Cargo.toml file.  Returns nil when no [workspace] section
// is found.
func parseWorkspaceMembersFromContent(content, rootDir string) []string {
	var members []string
	lines := strings.Split(content, "\n")
	inWorkspace := false
	inMembers := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		if strings.HasPrefix(trimmed, "[") {
			if inWorkspace && trimmed != "[workspace]" {
				inWorkspace = false
				inMembers = false
			}
			if trimmed == "[workspace]" {
				inWorkspace = true
			}
			continue
		}

		if !inWorkspace {
			continue
		}

		if strings.HasPrefix(trimmed, "members") && strings.Contains(trimmed, "=") {
			inMembers = true
			rest := strings.SplitN(trimmed, "=", 2)[1]
			rest = strings.TrimSpace(rest)
			if strings.Contains(rest, "]") {
				// Single-line array: members = ["a", "b"]
				rest = strings.Trim(rest, "[]")
				for _, part := range strings.Split(rest, ",") {
					p := strings.Trim(strings.TrimSpace(part), "\"'")
					if p != "" && !strings.HasPrefix(p, "#") {
						members = append(members, filepath.Join(rootDir, p))
					}
				}
				inMembers = false
			}
			continue
		}

		if inMembers {
			if strings.Contains(trimmed, "]") {
				inMembers = false
				continue
			}
			p := strings.Trim(trimmed, " \t,\"'[]")
			if p != "" && !strings.HasPrefix(p, "#") {
				members = append(members, filepath.Join(rootDir, p))
			}
		}
	}
	return members
}

// parseCratePackageName reads <crateDir>/Cargo.toml and returns the [package]
// name field.  Returns an empty string on any error.
func parseCratePackageName(crateDir string) string {
	return parseCargoPackageName(filepath.Join(crateDir, "Cargo.toml"))
}

// deduplicateMembers returns a deduplicated, stable-ordered copy of the slice.
func deduplicateMembers(dirs []string) []string {
	seen := make(map[string]bool, len(dirs))
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}
