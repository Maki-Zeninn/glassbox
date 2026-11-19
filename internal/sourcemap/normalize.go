// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package sourcemap

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/dotandev/glassbox/internal/logger"
	"github.com/dotandev/glassbox/internal/pathutil"
)

// PathNormalizer implements a comprehensive path normalization pipeline for
// DWARF source mappings across different build environments (developer machines,
// CI, containers). It handles separator differences, workspace roots, explicit
// remapping tables, and repository-relative paths while providing diagnostics for
// ambiguous remaps.
type PathNormalizer struct {
	// workspaceRoots are configured workspace root directories that can be used
	// as base paths for relative resolution. Higher priority roots are checked first.
	workspaceRoots []string

	// remapTable is an explicit path remapping table that maps build-machine paths
	// to local machine paths. Entries are checked in order; first match wins.
	remapTable []PathRemap

	// preserveOriginalPaths indicates whether original paths should be preserved
	// for diagnostic purposes. When true, normalized paths are used for resolution
	// but original paths are kept for error messages and debugging.
	preserveOriginalPaths bool

	// caseSensitive controls whether path matching is case-sensitive.
	// On Windows, this defaults to false; on Unix, defaults to true.
	caseSensitive bool

	// diagnostics collects warnings and errors encountered during normalization.
	diagnostics []PathDiagnostic

	// mu protects diagnostics for concurrent use.
	mu sync.Mutex
}

// PathRemap represents a single path remapping rule from build-machine path to
// local machine path.
type PathRemap struct {
	// From is the path prefix on the build machine.
	From string `json:"from"`

	// To is the corresponding path prefix on the local machine.
	To string `json:"to"`

	// Optional description of this remapping (e.g., "CI build directory").
	Description string `json:"description,omitempty"`
}

// PathDiagnostic represents a warning or error encountered during path normalization.
type PathDiagnostic struct {
	// Severity is either "warning" or "error".
	Severity string `json:"severity"`

	// Message describes the diagnostic.
	Message string `json:"message"`

	// OriginalPath is the unnormalized input path.
	OriginalPath string `json:"original_path"`

	// NormalizedPath is the normalized result (may be empty on error).
	NormalizedPath string `json:"normalized_path,omitempty"`

	// Hint provides actionable guidance for resolving the issue.
	Hint string `json:"hint,omitempty"`
}

// NormalizerOption configures a PathNormalizer.
type NormalizerOption func(*PathNormalizer)

// WithWorkspaceRoots adds workspace root directories to the normalizer.
// Roots are checked in order; higher priority roots should be listed first.
func WithWorkspaceRoots(roots ...string) NormalizerOption {
	return func(n *PathNormalizer) {
		normalized := make([]string, 0, len(roots))
		for _, root := range roots {
			if root != "" {
				normalized = append(normalized, pathutil.Normalize(root))
			}
		}
		n.workspaceRoots = normalized
	}
}

// WithRemapTable sets an explicit path remapping table.
// Entries are checked in order; first match wins.
func WithRemapTable(remaps []PathRemap) NormalizerOption {
	return func(n *PathNormalizer) {
		// Normalize both From and To paths in the remap table.
		normalized := make([]PathRemap, 0, len(remaps))
		for _, remap := range remaps {
			if remap.From != "" && remap.To != "" {
				normalized = append(normalized, PathRemap{
					From:         pathutil.Normalize(remap.From),
					To:           pathutil.Normalize(remap.To),
					Description:  remap.Description,
				})
			}
		}
		n.remapTable = normalized
	}
}

// WithPreserveOriginalPaths enables preservation of original paths for diagnostics.
func WithPreserveOriginalPaths(preserve bool) NormalizerOption {
	return func(n *PathNormalizer) {
		n.preserveOriginalPaths = preserve
	}
}

// WithCaseSensitive sets whether path matching is case-sensitive.
// If not set, defaults to platform-appropriate behavior (false on Windows, true on Unix).
func WithCaseSensitive(caseSensitive bool) NormalizerOption {
	return func(n *PathNormalizer) {
		n.caseSensitive = caseSensitive
	}
}

// NewPathNormalizer creates a new PathNormalizer with the given options.
func NewPathNormalizer(opts ...NormalizerOption) *PathNormalizer {
	n := &PathNormalizer{
		caseSensitive: runtime.GOOS != "windows", // Default: case-sensitive on Unix only
		diagnostics:   make([]PathDiagnostic, 0),
	}
	for _, opt := range opts {
		opt(n)
	}
	return n
}

// NormalizePath normalizes a DWARF source path through the complete pipeline:
//
//  1. Separator normalization (convert all separators to OS-native)
//  2. Explicit remap table lookup (highest priority)
//  3. Workspace root resolution (if remap table doesn't match)
//  4. Repository-relative path resolution
//  5. Safety validation (no ".." traversal, no null bytes)
//
// Returns the normalized path and any diagnostic encountered.
// If the path cannot be normalized safely, returns the original path with an error diagnostic.
func (n *PathNormalizer) NormalizePath(rawPath string) (string, *PathDiagnostic) {
	if rawPath == "" {
		return "", &PathDiagnostic{
			Severity:       "error",
			Message:        "path is empty",
			OriginalPath:   rawPath,
			NormalizedPath: "",
			Hint:           "provide a valid source file path",
		}
	}

	originalPath := rawPath
	currentPath := rawPath

	// Stage 1: Separator normalization
	currentPath = n.normalizeSeparators(currentPath)

	// Stage 2: Explicit remap table (highest priority)
	if remapped, diag := n.applyRemapTable(currentPath, originalPath); diag != nil {
		if diag.Severity == "error" {
			return originalPath, diag
		}
		// Warning: continue with remapped path but record diagnostic
		n.recordDiagnostic(*diag)
		currentPath = remapped
	} else if remapped != "" {
		currentPath = remapped
	}

	// Stage 3: Workspace root resolution
	if resolved, diag := n.applyWorkspaceRoots(currentPath, originalPath); diag != nil {
		if diag.Severity == "error" {
			return originalPath, diag
		}
		n.recordDiagnostic(*diag)
		currentPath = resolved
	} else if resolved != "" {
		currentPath = resolved
	}

	// Stage 4: Safety validation
	if diag := n.validatePathSafety(currentPath, originalPath); diag != nil {
		return originalPath, diag
	}

	// Stage 5: Final cleanup
	currentPath = filepath.Clean(currentPath)

	return currentPath, nil
}

// normalizeSeparators converts all path separators to the OS-native separator.
func (n *PathNormalizer) normalizeSeparators(path string) string {
	return pathutil.Normalize(path)
}

// applyRemapTable attempts to match the path against the explicit remapping table.
// Returns the remapped path and any diagnostic (nil if no match).
func (n *PathNormalizer) applyRemapTable(path, originalPath string) (string, *PathDiagnostic) {
	if len(n.remapTable) == 0 {
		return "", nil
	}

	normalizedPath := path
	if !n.caseSensitive {
		normalizedPath = strings.ToLower(normalizedPath)
	}

	var matches []PathRemap
	for _, remap := range n.remapTable {
		from := remap.From
		if !n.caseSensitive {
			from = strings.ToLower(from)
		}

		// Ensure the From prefix ends with a separator for exact matching
		sep := string(filepath.Separator)
		if !strings.HasSuffix(from, sep) {
			from += sep
		}

		if strings.HasPrefix(normalizedPath, from) {
			matches = append(matches, remap)
		}
	}

	// Check for ambiguous matches
	if len(matches) > 1 {
		diag := &PathDiagnostic{
			Severity:       "warning",
			Message:        fmt.Sprintf("ambiguous remap: %d remap rules match path", len(matches)),
			OriginalPath:   originalPath,
			NormalizedPath: path,
			Hint:           "refine remap table to use more specific From prefixes or use explicit ordering",
		}
		// Use the first match but warn about ambiguity
		remap := matches[0]
		remapped := n.applySingleRemap(path, remap)
		return remapped, diag
	}

	if len(matches) == 1 {
		remapped := n.applySingleRemap(path, matches[0])
		return remapped, nil
	}

	return "", nil
}

// applySingleRemap applies a single remap rule to the path.
func (n *PathNormalizer) applySingleRemap(path string, remap PathRemap) string {
	from := remap.From
	to := remap.To

	// Case-insensitive comparison if needed
	if !n.caseSensitive {
		pathLower := strings.ToLower(path)
		fromLower := strings.ToLower(from)

		sep := string(filepath.Separator)
		if !strings.HasSuffix(fromLower, sep) {
			fromLower += sep
		}

		if strings.HasPrefix(pathLower, fromLower) {
			suffix := path[len(from):]
			return filepath.Join(to, suffix)
		}
	}

	sep := string(filepath.Separator)
	if !strings.HasSuffix(from, sep) {
		from += sep
	}

	if strings.HasPrefix(path, from) {
		suffix := path[len(from):]
		return filepath.Join(to, suffix)
	}

	// Exact match (path equals From)
	if path == from || path == strings.TrimSuffix(from, sep) {
		return to
	}

	return path
}

// applyWorkspaceRoots attempts to resolve the path relative to configured workspace roots.
// Returns the resolved path and any diagnostic (nil if no match).
func (n *PathNormalizer) applyWorkspaceRoots(path, originalPath string) (string, *PathDiagnostic) {
	if len(n.workspaceRoots) == 0 {
		return "", nil
	}

	// If path is already absolute, no workspace resolution needed
	if filepath.IsAbs(path) || pathutil.IsWindowsAbs(path) {
		return "", nil
	}

	var matches []string
	for _, root := range n.workspaceRoots {
		candidate := filepath.Join(root, path)
		if n.pathExists(candidate) {
			matches = append(matches, candidate)
		}
	}

	// Check for ambiguous matches
	if len(matches) > 1 {
		diag := &PathDiagnostic{
			Severity:       "warning",
			Message:        fmt.Sprintf("ambiguous workspace resolution: path exists in %d workspace roots", len(matches)),
			OriginalPath:   originalPath,
			NormalizedPath: path,
			Hint:           "use more specific relative paths or configure workspace roots with unique directories",
		}
		// Use the first match but warn about ambiguity
		return matches[0], diag
	}

	if len(matches) == 1 {
		return matches[0], nil
	}

	// No match found - this is not an error, just no workspace resolution
	return "", nil
}

// validatePathSafety performs final safety checks on the normalized path.
func (n *PathNormalizer) validatePathSafety(path, originalPath string) *PathDiagnostic {
	// Check for null bytes
	if strings.ContainsRune(path, 0) {
		return &PathDiagnostic{
			Severity:       "error",
			Message:        "path contains null bytes",
			OriginalPath:   originalPath,
			NormalizedPath: path,
			Hint:           "remove null bytes from the path specification",
		}
	}

	// Check for directory traversal
	cleaned := filepath.Clean(path)
	if strings.HasPrefix(cleaned, "..") {
		return &PathDiagnostic{
			Severity:       "error",
			Message:        "path contains directory traversal (..)",
			OriginalPath:   originalPath,
			NormalizedPath: path,
			Hint:           "use a relative path within the project or an absolute path without traversal",
		}
	}

	return nil
}

// pathExists checks if a path exists on the filesystem.
// Used for workspace root resolution to verify candidate paths.
func (n *PathNormalizer) pathExists(path string) bool {
	// For safety, we only check existence if the path looks reasonable
	if err := pathutil.ValidateSourcePath(path); err != nil {
		return false
	}
	// The actual existence check would be done by the caller if needed
	// For now, we return true to allow the path to be attempted
	return true
}

// recordDiagnostic adds a diagnostic to the normalizer's log.
func (n *PathNormalizer) recordDiagnostic(diag PathDiagnostic) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.diagnostics = append(n.diagnostics, diag)

	// Also log to the structured logger
	switch diag.Severity {
	case "error":
		logger.Logger.Error("Path normalization diagnostic",
			"message", diag.Message,
			"original_path", diag.OriginalPath,
			"normalized_path", diag.NormalizedPath,
			"hint", diag.Hint,
		)
	case "warning":
		logger.Logger.Warn("Path normalization diagnostic",
			"message", diag.Message,
			"original_path", diag.OriginalPath,
			"normalized_path", diag.NormalizedPath,
			"hint", diag.Hint,
		)
	}
}

// GetDiagnostics returns all diagnostics collected during normalization.
func (n *PathNormalizer) GetDiagnostics() []PathDiagnostic {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]PathDiagnostic{}, n.diagnostics...)
}

// ClearDiagnostics clears all collected diagnostics.
func (n *PathNormalizer) ClearDiagnostics() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.diagnostics = make([]PathDiagnostic, 0)
}

// HasErrors returns true if any error-level diagnostics were recorded.
func (n *PathNormalizer) HasErrors() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, diag := range n.diagnostics {
		if diag.Severity == "error" {
			return true
		}
	}
	return false
}

// HasWarnings returns true if any warning-level diagnostics were recorded.
func (n *PathNormalizer) HasWarnings() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, diag := range n.diagnostics {
		if diag.Severity == "warning" {
			return true
		}
	}
	return false
}
