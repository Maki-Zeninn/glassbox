// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// pathpolicy.go implements portable-path handling for debug bundles.
//
// Problem
//   A bundle created on machine A may embed absolute paths (e.g. source map
//   roots, artifact references) that no longer resolve on machine B.  We need:
//
//   1. Logical paths stored in the bundle (platform-neutral, repo-relative).
//   2. An import-time root mapping so the consuming machine can rewrite them.
//   3. Traversal-attempt detection (.. sequences must be rejected).
//   4. Graceful degradation: missing optional source files are warnings; missing
//      required execution data is a stable, named error.
//
// Implementation
//   BundlePathPolicy is embedded in Manifest.PathPolicy.  It carries:
//     - OriginRoot:   the absolute source root on the machine that produced the bundle.
//     - LogicalPaths: a map of artifact logical-path → repo-relative path at export time.
//     - DiagnosticPaths: read-only copy of original absolute paths, for diagnostics only.
//
//   ImportPathMapping is the caller-supplied rewrite table passed to RewritePaths.
//   It maps each logical artifact path to the new workspace root.
package bundle

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dotandev/glassbox/internal/pathutil"
)

// BundlePathPolicy carries portable-path metadata embedded in a bundle.
type BundlePathPolicy struct {
	// OriginRoot is the absolute directory of the workspace on the machine
	// that exported the bundle.  It is stored for informational/diagnostic
	// purposes; import-time rewriting uses ImportPathMapping instead.
	OriginRoot string `json:"origin_root,omitempty"`

	// LogicalPaths maps artifact logical path (e.g. "source_map") to a
	// platform-neutral, repo-relative path (forward slashes, no leading /).
	// These are the paths that should resolve after import-time rewriting.
	LogicalPaths map[string]string `json:"logical_paths,omitempty"`

	// DiagnosticPaths is a read-only snapshot of the original absolute paths
	// at export time.  It MUST NOT be used for file I/O at import time; its
	// sole purpose is to help the user understand where the bundle came from.
	DiagnosticPaths map[string]string `json:"diagnostic_paths,omitempty"`
}

// ImportPathMapping is the caller-supplied rewrite table used at import time.
// Keys are artifact logical paths; values are the new workspace roots (or
// empty string to inherit a global NewRoot).
type ImportPathMapping struct {
	// NewRoot is the default new workspace root applied to every logical path
	// that does not have an explicit per-artifact override.
	// Empty means "no global rewrite; use per-artifact overrides only".
	NewRoot string
	// Overrides maps logical path → new workspace root for specific artifacts.
	Overrides map[string]string
}

// PathRewriteResult is the output of RewritePaths.
type PathRewriteResult struct {
	// Resolved maps logical path → rewritten filesystem path.
	Resolved map[string]string
	// Warnings lists logical paths that could not be resolved (optional artifacts).
	Warnings []PathRewriteWarning
	// Errors lists logical paths that failed to resolve and are required.
	Errors []PathRewriteError
}

// PathRewriteWarning is a non-fatal note about an unresolved optional path.
type PathRewriteWarning struct {
	LogicalPath string
	Reason      string
}

// PathRewriteError is a stable, named error for an unresolved required path.
type PathRewriteError struct {
	LogicalPath string
	Reason      string
}

// PathPolicyError is the error type returned when path validation or rewriting
// produces hard failures.
type PathPolicyError struct {
	Errors []PathRewriteError
}

func (e *PathPolicyError) Error() string {
	msgs := make([]string, len(e.Errors))
	for i, pe := range e.Errors {
		msgs[i] = fmt.Sprintf("[%s] %s", pe.LogicalPath, pe.Reason)
	}
	return "bundle path policy error: " + strings.Join(msgs, "; ")
}

// IsPathPolicyError reports whether err is a *PathPolicyError.
func IsPathPolicyError(err error) bool {
	_, ok := err.(*PathPolicyError)
	return ok
}

// optionalPathArtifacts lists logical paths where a missing resolution is only
// a warning.
var optionalPathArtifacts = map[string]bool{
	ArtifactSourceMap: true,
	ArtifactTrace:     true,
	ArtifactSignature: true,
}

// NewBundlePathPolicy constructs a BundlePathPolicy from the workspace root at
// export time.  logicalPaths maps each artifact logical-path to its
// repo-relative (forward-slash) path.  originalPaths maps the same keys to the
// original absolute paths and is stored read-only in DiagnosticPaths.
//
// Returns an error if workspaceRoot contains traversal sequences or null bytes.
func NewBundlePathPolicy(workspaceRoot string, logicalPaths, originalPaths map[string]string) (*BundlePathPolicy, error) {
	if err := validatePathRoot(workspaceRoot, "workspace_root"); err != nil {
		return nil, err
	}
	for lp, rp := range logicalPaths {
		if err := validateLogicalPath(lp, rp); err != nil {
			return nil, err
		}
	}

	pp := &BundlePathPolicy{
		OriginRoot:      pathutil.ToSlash(workspaceRoot),
		LogicalPaths:    make(map[string]string, len(logicalPaths)),
		DiagnosticPaths: make(map[string]string, len(originalPaths)),
	}
	for k, v := range logicalPaths {
		pp.LogicalPaths[k] = v
	}
	for k, v := range originalPaths {
		pp.DiagnosticPaths[k] = v
	}
	return pp, nil
}

// RewritePaths applies the import mapping to every logical path in pp and
// returns a PathRewriteResult.  The caller can inspect Warnings and Errors
// independently and choose whether to abort on warnings.
//
// When mapping is nil, paths are resolved relative to the current working
// directory only if they are already relative.
func (pp *BundlePathPolicy) RewritePaths(mapping *ImportPathMapping) *PathRewriteResult {
	result := &PathRewriteResult{
		Resolved: make(map[string]string, len(pp.LogicalPaths)),
	}
	if pp == nil || len(pp.LogicalPaths) == 0 {
		return result
	}

	for logicalPath, repoRelPath := range pp.LogicalPaths {
		newRoot := resolveNewRoot(logicalPath, mapping)

		var resolved string
		if newRoot != "" {
			// Join new root with the repo-relative path.
			joined := pathutil.Join(newRoot, repoRelPath)
			// Safety: reject traversal after join.
			if err := validateJoinedPath(joined, newRoot); err != nil {
				if optionalPathArtifacts[logicalPath] {
					result.Warnings = append(result.Warnings, PathRewriteWarning{
						LogicalPath: logicalPath,
						Reason:      err.Error(),
					})
				} else {
					result.Errors = append(result.Errors, PathRewriteError{
						LogicalPath: logicalPath,
						Reason:      err.Error(),
					})
				}
				continue
			}
			resolved = joined
		} else if !filepath.IsAbs(repoRelPath) && !pathutil.IsWindowsAbs(repoRelPath) {
			// No mapping and path is relative — use as-is.
			resolved = repoRelPath
		} else {
			// Absolute path with no mapping provided.
			if optionalPathArtifacts[logicalPath] {
				result.Warnings = append(result.Warnings, PathRewriteWarning{
					LogicalPath: logicalPath,
					Reason: fmt.Sprintf(
						"absolute path %q cannot be used without an import root mapping for %q",
						repoRelPath, logicalPath),
				})
			} else {
				result.Errors = append(result.Errors, PathRewriteError{
					LogicalPath: logicalPath,
					Reason: fmt.Sprintf(
						"absolute path %q cannot be used without an import root mapping for %q",
						repoRelPath, logicalPath),
				})
			}
			continue
		}

		result.Resolved[logicalPath] = resolved
	}
	return result
}

// ValidateBundlePathPolicy validates the path policy embedded in a bundle
// before import.  It checks for traversal sequences and null bytes in every
// stored logical path.
func ValidateBundlePathPolicy(pp *BundlePathPolicy) error {
	if pp == nil {
		return nil // absent policy is valid (legacy bundle)
	}
	for lp, rp := range pp.LogicalPaths {
		if err := validateLogicalPath(lp, rp); err != nil {
			return err
		}
	}
	return nil
}

// ── internal helpers ──────────────────────────────────────────────────────────

func resolveNewRoot(logicalPath string, mapping *ImportPathMapping) string {
	if mapping == nil {
		return ""
	}
	if override, ok := mapping.Overrides[logicalPath]; ok {
		return override
	}
	return mapping.NewRoot
}

// validatePathRoot rejects empty, null-byte, or traversal-containing roots.
func validatePathRoot(root, fieldName string) error {
	if strings.TrimSpace(root) == "" {
		return fmt.Errorf("bundle path policy: %s must not be empty", fieldName)
	}
	if strings.ContainsRune(root, 0) {
		return fmt.Errorf("bundle path policy: %s contains null bytes", fieldName)
	}
	cleaned := filepath.Clean(pathutil.Normalize(root))
	if strings.Contains(cleaned, "..") {
		return fmt.Errorf("bundle path policy: %s %q contains path traversal (..)", fieldName, root)
	}
	return nil
}

// validateLogicalPath ensures the stored repo-relative path for an artifact
// does not contain traversal sequences or null bytes.
func validateLogicalPath(logicalPath, repoRelPath string) error {
	if strings.ContainsRune(repoRelPath, 0) {
		return fmt.Errorf("bundle path policy: logical path %q value contains null bytes", logicalPath)
	}
	if repoRelPath == "" {
		return nil // empty is allowed; means "not mapped"
	}
	// Normalise to the host separator and clean.
	cleaned := filepath.Clean(pathutil.Normalize(repoRelPath))
	if strings.HasPrefix(cleaned, "..") {
		return fmt.Errorf(
			"bundle path policy: logical path %q value %q contains path traversal (..)",
			logicalPath, repoRelPath,
		)
	}
	return nil
}

// validateJoinedPath ensures that after joining newRoot + repoRelPath the
// result is still a child of newRoot (no escape via ..).
func validateJoinedPath(joined, root string) error {
	normJoined := pathutil.Normalize(joined)
	normRoot := pathutil.Normalize(root)
	sep := string(filepath.Separator)
	if !strings.HasSuffix(normRoot, sep) {
		normRoot += sep
	}
	if !strings.HasPrefix(normJoined, normRoot) && normJoined != strings.TrimSuffix(normRoot, sep) {
		return fmt.Errorf("path traversal detected: %q escapes root %q", joined, root)
	}
	return nil
}
