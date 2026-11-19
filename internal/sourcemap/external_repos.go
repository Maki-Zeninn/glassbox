// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package sourcemap

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/dotandev/glassbox/internal/pathutil"
)

// ExternalRepoMapping maps a local source path prefix to a remote Git repository.
type ExternalRepoMapping struct {
	// Prefix is the absolute or workspace-relative path prefix for sources in the external repo.
	Prefix string `json:"prefix"`
	// RemoteURL is the GitHub repository URL (HTTPS or git@github.com: form).
	RemoteURL string `json:"remote_url"`
	// Branch is used in blob URLs when set; otherwise "main" is used.
	Branch string `json:"branch,omitempty"`
}

// ExternalRepoRegistry resolves GitHub links for files that live outside the workspace repo.
type ExternalRepoRegistry struct {
	mappings []ExternalRepoMapping
}

// NewExternalRepoRegistry creates a registry from zero or more mappings.
func NewExternalRepoRegistry(mappings []ExternalRepoMapping) *ExternalRepoRegistry {
	normalized := make([]ExternalRepoMapping, 0, len(mappings))
	for _, m := range mappings {
		if m.Prefix == "" || m.RemoteURL == "" {
			continue
		}
		prefix, err := filepath.Abs(m.Prefix)
		if err != nil {
			prefix = filepath.Clean(m.Prefix)
		}
		branch := m.Branch
		if branch == "" {
			branch = "main"
		}
		normalized = append(normalized, ExternalRepoMapping{
			Prefix:    pathutil.Normalize(prefix),
			RemoteURL: m.RemoteURL,
			Branch:    branch,
		})
	}
	return &ExternalRepoRegistry{mappings: normalized}
}

// GitHubURL returns a GitHub blob URL when absFilePath falls under a configured external prefix.
func (r *ExternalRepoRegistry) GitHubURL(absFilePath string) (string, error) {
	if r == nil || len(r.mappings) == 0 {
		return "", fmt.Errorf("no external source mappings configured")
	}
	abs, err := filepath.Abs(absFilePath)
	if err != nil {
		return "", err
	}
	abs = pathutil.Normalize(abs)

	for _, m := range r.mappings {
		prefix := m.Prefix
		if !strings.HasSuffix(prefix, "/") {
			prefix += "/"
		}
		if abs != strings.TrimSuffix(m.Prefix, "/") && !strings.HasPrefix(abs, prefix) {
			continue
		}
		owner, repo, err := parseGitHubRemote(m.RemoteURL)
		if err != nil {
			return "", err
		}
		rel, err := pathutil.RelToSlash(m.Prefix, abs)
		if err != nil {
			rel = filepath.Base(abs)
		}
		if strings.HasPrefix(rel, "../") {
			return "", fmt.Errorf("file %q is outside external prefix %q", absFilePath, m.Prefix)
		}
		if err := validateSourcePath(rel); err != nil {
			return "", err
		}
		// A configured branch is an explicit user override for external trees.
		// It is retained for backward compatibility, but callers that replay an
		// artifact should prefer Revision in a repository-local GitLinker.
		return fmt.Sprintf("https://github.com/%s/%s/blob/%s/%s",
			owner, repo, url.PathEscape(m.Branch), escapeGitHubPath(rel)), nil
	}
	return "", fmt.Errorf("no external mapping for %q", absFilePath)
}

// ResolveGitHubURL tries the workspace GitLinker first, then external mappings.
func ResolveGitHubURL(startPath, filePath string, external *ExternalRepoRegistry) (string, error) {
	url, _, err := ResolveGitHubURLWithProvenance(startPath, filePath, external, RevisionOptions{})
	return url, err
}

// ResolveGitHubURLWithProvenance resolves an immutable replay link and the
// label that must be rendered beside it. It refuses unknown and dirty workspace
// revisions unless opts.AllowAmbiguous was expressly set.
func ResolveGitHubURLWithProvenance(startPath, filePath string, external *ExternalRepoRegistry, opts RevisionOptions) (string, RevisionProvenance, error) {
	abs := filePath
	if !filepath.IsAbs(abs) { abs = pathutil.Join(startPath, filePath) }
	if linker, err := NewGitLinkerWithRevision(abs, opts); err == nil {
		url, linkErr := linker.GitHubURL(abs)
		if linkErr == nil { return url, linker.Provenance(), nil }
	} else if root, rootErr := findRepoRoot(abs); rootErr == nil {
		// Preserve the diagnostic provenance even though a safe URL is refused.
		provenance, _ := resolveRevision(root, opts)
		return "", provenance, err
	}
	if external != nil {
		url, err := external.GitHubURL(abs)
		if err == nil { return url, RevisionProvenance{Source: "external mapping", Revision: "configured ref"}, nil }
	}
	return "", RevisionProvenance{Source: "unknown"}, fmt.Errorf("could not resolve GitHub URL for %q", filePath)
}
