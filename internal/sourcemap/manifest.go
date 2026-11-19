// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package sourcemap provides source mapping utilities for resolving WASM
// instruction addresses back to Rust source locations.
package sourcemap

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dotandev/glassbox/internal/pathutil"
)

// ManifestFileName is the conventional name searched during auto-discovery.
const ManifestFileName = "glassbox-build-manifest.json"

// manifestSearchDirs lists paths relative to the project root where
// auto-discovery looks for a build manifest.
var manifestSearchDirs = []string{
	".",
	"target",
	"target/wasm32-unknown-unknown/release",
}

// BuildManifest records everything needed to reproduce a WASM artifact and
// remap source paths across machines.
//
// Schema fields:
//
//	source_root          – absolute or workspace-relative path to the source
//	                       tree on the machine that produced the artifact.
//	                       Used to strip the build-machine prefix so only the
//	                       repo-relative tail survives remapping.
//	repository_revision  – full Git commit SHA (40 hex chars) or short hash
//	                       of the revision the artifact was built from.
//	                       Required when GitHub source links are requested so
//	                       the URL points at the exact commit rather than HEAD.
//	compiler_version     – rustc version string recorded for informational
//	                       purposes (e.g. "rustc 1.77.2 (25ef9e3d8 2024-04-09)").
//	artifact_hash        – lowercase hex SHA-256 of the compiled WASM binary.
//	                       Must match the local file when the manifest is loaded
//	                       alongside a --wasm artifact; mismatches are rejected.
type BuildManifest struct {
	// SourceRoot is the absolute path of the source tree on the build machine.
	SourceRoot string `json:"source_root"`

	// RepositoryRevision is the full or abbreviated Git commit SHA that was
	// compiled. Required for generating GitHub permalink source links.
	RepositoryRevision string `json:"repository_revision"`

	// CompilerVersion is the rustc version string, recorded for diagnostics.
	CompilerVersion string `json:"compiler_version,omitempty"`

	// ArtifactHash is the lowercase hex SHA-256 digest of the compiled WASM
	// binary. When non-empty the loader verifies the local WASM file matches.
	ArtifactHash string `json:"artifact_hash"`
}

// ManifestHashMismatchError is returned by LoadManifestAndVerify when the
// SHA-256 of the local WASM file does not match ArtifactHash in the manifest.
type ManifestHashMismatchError struct {
	ManifestPath string
	WasmPath     string
	Manifest     string // hash recorded in manifest
	Local        string // hash of the local file
}

func (e *ManifestHashMismatchError) Error() string {
	return fmt.Sprintf(
		"build manifest mismatch: artifact hash in manifest %q (%s) does not match "+
			"local WASM hash (%s) for file %q\n"+
			"  The manifest was generated from a different build — rebuild with\n"+
			"  'cargo build --release --target wasm32-unknown-unknown' and regenerate\n"+
			"  the manifest, or omit --build-manifest to skip hash verification.",
		e.ManifestPath, e.Manifest, e.Local, e.WasmPath,
	)
}

// LoadManifest reads and parses a BuildManifest from path, normalises all
// embedded paths, and validates the schema.  It does NOT check the artifact
// hash against a WASM file; use LoadManifestAndVerify for that.
func LoadManifest(path string) (*BuildManifest, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("build manifest: path must not be empty")
	}
	if strings.ContainsRune(path, 0) {
		return nil, fmt.Errorf(
			"build manifest: path contains null bytes and cannot be used\n" +
				"  Fix: remove any null bytes from the --build-manifest path.",
		)
	}

	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf(
				"build manifest: file not found: %q\n"+
					"  Generate a manifest with 'glassbox generate-manifest' or provide\n"+
					"  a valid path to an existing glassbox-build-manifest.json file.",
				path,
			)
		}
		return nil, fmt.Errorf("build manifest: cannot read %q: %w", path, err)
	}

	var m BuildManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf(
			"build manifest: failed to parse %q as JSON: %w\n"+
				"  The file must be a valid JSON object matching the BuildManifest schema.\n"+
				"  Required fields: source_root, repository_revision, artifact_hash.",
			path, err,
		)
	}

	if err := ValidateManifest(&m, path); err != nil {
		return nil, err
	}

	// Normalize the embedded source root so separator differences between
	// build machine and current machine don't break prefix stripping.
	m.SourceRoot = pathutil.Normalize(m.SourceRoot)

	return &m, nil
}

// LoadManifestAndVerify loads the manifest and then checks that the SHA-256 of
// wasmPath matches m.ArtifactHash.  A ManifestHashMismatchError is returned
// when they differ so callers can surface a clear diagnostic.
//
// wasmPath may be empty when the caller only needs path remapping (e.g. when
// --wasm was not provided); in that case hash verification is skipped.
func LoadManifestAndVerify(manifestPath, wasmPath string) (*BuildManifest, error) {
	m, err := LoadManifest(manifestPath)
	if err != nil {
		return nil, err
	}

	if wasmPath == "" || m.ArtifactHash == "" {
		return m, nil
	}

	wasmData, err := os.ReadFile(wasmPath) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf(
			"build manifest: cannot read WASM file %q for hash verification: %w", wasmPath, err)
	}

	sum := sha256.Sum256(wasmData)
	localHash := hex.EncodeToString(sum[:])

	if !strings.EqualFold(localHash, m.ArtifactHash) {
		return nil, &ManifestHashMismatchError{
			ManifestPath: manifestPath,
			WasmPath:     wasmPath,
			Manifest:     strings.ToLower(m.ArtifactHash),
			Local:        localHash,
		}
	}

	return m, nil
}

// ValidateManifest checks that all required fields are present and well-formed.
func ValidateManifest(m *BuildManifest, filePath string) error {
	if m == nil {
		return fmt.Errorf("build manifest: manifest must not be nil")
	}

	label := "build manifest"
	if filePath != "" {
		label = fmt.Sprintf("build manifest %q", filePath)
	}

	if strings.TrimSpace(m.SourceRoot) == "" {
		return fmt.Errorf(
			"%s: missing required field 'source_root'\n"+
				"  Set it to the absolute path of the source tree on the build machine.",
			label,
		)
	}

	if strings.TrimSpace(m.RepositoryRevision) == "" {
		return fmt.Errorf(
			"%s: missing required field 'repository_revision'\n"+
				"  Set it to the full Git commit SHA (e.g. output of 'git rev-parse HEAD').",
			label,
		)
	}

	if err := validateRevision(m.RepositoryRevision, label); err != nil {
		return err
	}

	if m.ArtifactHash != "" {
		if err := validateHexHash(m.ArtifactHash, "artifact_hash", label); err != nil {
			return err
		}
	}

	if strings.ContainsRune(m.SourceRoot, 0) {
		return fmt.Errorf(
			"%s: 'source_root' contains null bytes and cannot be used",
			label,
		)
	}

	// Prevent path traversal: after normalization the source root must not
	// contain ".." components.
	normalized := filepath.Clean(pathutil.Normalize(m.SourceRoot))
	if strings.Contains(normalized, "..") {
		return fmt.Errorf(
			"%s: 'source_root' contains path traversal sequences (..)\n"+
				"  Use an absolute path or a clean relative path without '..'.",
			label,
		)
	}

	return nil
}

// RemapPath rewrites a build-machine path using the manifest's SourceRoot so
// that source lookups work on a machine with a different checkout location.
//
// Algorithm:
//  1. Normalize both rawPath and SourceRoot to the platform separator.
//  2. Strip the SourceRoot prefix (and any trailing separator) from rawPath.
//  3. Clean the result so it is a plain repo-relative path with no leading
//     separator or ".." traversal.
//
// If rawPath does not share the SourceRoot prefix the original path is
// returned unchanged, so the function is safe to call unconditionally.
func (m *BuildManifest) RemapPath(rawPath string) string {
	if m == nil || m.SourceRoot == "" || rawPath == "" {
		return rawPath
	}

	norm := pathutil.Normalize(rawPath)
	root := pathutil.Normalize(m.SourceRoot)

	// Ensure the root ends with a separator for exact prefix matching.
	sep := string(filepath.Separator)
	if !strings.HasSuffix(root, sep) {
		root += sep
	}

	if norm == strings.TrimSuffix(root, sep) {
		// rawPath IS the source root — map to "."
		return "."
	}

	if !strings.HasPrefix(norm, root) {
		// No match; return original so callers can still attempt other resolvers.
		return rawPath
	}

	rel := norm[len(root):]

	// Safety: reject any remaining traversal sequences that could escape the
	// repo root after stripping the source root prefix.
	cleaned := filepath.Clean(rel)
	if strings.HasPrefix(cleaned, "..") {
		return rawPath
	}

	return cleaned
}

// GitHubRevisionURL returns the GitHub permalink for filePath using the
// revision recorded in the manifest.  owner, repo, and filePath are caller-
// supplied; the manifest contributes only the revision component.
//
// filePath should already be repo-relative (e.g. produced by RemapPath).
// Returns an error when RepositoryRevision is empty so callers can fall back
// gracefully.
func (m *BuildManifest) GitHubRevisionURL(owner, repo, filePath string) (string, error) {
	if m == nil || m.RepositoryRevision == "" {
		return "", fmt.Errorf(
			"build manifest: repository_revision is empty; " +
				"cannot generate a GitHub permalink — set it to the full Git commit SHA",
		)
	}
	if owner == "" || repo == "" {
		return "", fmt.Errorf("build manifest: owner and repo must not be empty")
	}
	// Normalise to forward slashes for a valid URL path segment.
	slashPath := pathutil.ToSlash(filePath)
	slashPath = strings.TrimPrefix(slashPath, "/")
	return fmt.Sprintf("https://github.com/%s/%s/blob/%s/%s",
		owner, repo, m.RepositoryRevision, slashPath), nil
}

// DiscoverManifest searches conventional locations relative to projectRoot for
// a glassbox-build-manifest.json file and returns the first one found.
// Returns ("", nil) when nothing is found.
func DiscoverManifest(projectRoot string) (string, error) {
	if strings.TrimSpace(projectRoot) == "" {
		return "", nil
	}
	if strings.ContainsRune(projectRoot, 0) {
		return "", fmt.Errorf(
			"manifest discovery: projectRoot contains null bytes and cannot be used",
		)
	}

	for _, dir := range manifestSearchDirs {
		candidate := filepath.Join(projectRoot, dir, ManifestFileName)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", nil
}

// ─── internal validation helpers ─────────────────────────────────────────────

// validateRevision checks that rev is a plausible Git revision string.
// It accepts full SHAs (40 hex chars), short SHAs (7–39 hex chars), and
// branch/tag names (printable ASCII, no whitespace).
func validateRevision(rev, label string) error {
	rev = strings.TrimSpace(rev)
	if rev == "" {
		return fmt.Errorf("%s: repository_revision must not be empty", label)
	}
	if strings.ContainsAny(rev, " \t\n\r\x00") {
		return fmt.Errorf(
			"%s: repository_revision %q contains whitespace or null bytes\n"+
				"  Use the output of 'git rev-parse HEAD' (no spaces).",
			label, rev,
		)
	}
	// A purely hexadecimal string must be 7–40 characters (short or full SHA).
	if isHexString(rev) && (len(rev) < 7 || len(rev) > 40) {
		return fmt.Errorf(
			"%s: repository_revision %q looks like a hex SHA but has length %d "+
				"(expected 7–40)\n"+
				"  Use the full SHA from 'git rev-parse HEAD' or a short hash of ≥7 chars.",
			label, rev, len(rev),
		)
	}
	return nil
}

// validateHexHash checks that hash is a non-empty lowercase hex string of
// even length (a valid hash digest).
func validateHexHash(hash, fieldName, label string) error {
	hash = strings.ToLower(hash)
	if len(hash) == 0 || len(hash)%2 != 0 {
		return fmt.Errorf(
			"%s: %q must be a lowercase hex string of even length (e.g. SHA-256 = 64 chars), got %d chars",
			label, fieldName, len(hash),
		)
	}
	if !isHexString(hash) {
		return fmt.Errorf(
			"%s: %q contains non-hex characters; expected a lowercase hex digest",
			label, fieldName,
		)
	}
	return nil
}

// isHexString returns true when every byte in s is in [0-9a-fA-F].
func isHexString(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}
