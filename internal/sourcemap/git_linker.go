// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package sourcemap

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/dotandev/glassbox/internal/logger"
	"github.com/dotandev/glassbox/internal/pathutil"
)

// GitLinker resolves source file paths relative to a repository root and
// constructs GitHub blob URLs. It supports nested Cargo workspaces and
// monorepo layouts where a contract lives in a subdirectory.
type GitLinker struct {
	// repoRoot is the absolute path to the .git directory's parent.
	repoRoot string
	// remoteURL is the parsed GitHub remote origin URL.
	remoteURL string
	// defaultBranch is retained only for explicit ambiguous-link overrides.
	defaultBranch  string
	provenance     RevisionProvenance
	allowAmbiguous bool
}

// NewGitLinker creates a GitLinker by discovering the repository root upward
// from startPath. startPath may be a file or directory inside the repo.
func NewGitLinker(startPath string) (*GitLinker, error) {
	return NewGitLinkerWithRevision(startPath, RevisionOptions{})
}

// NewGitLinkerWithRevision creates a linker pinned to the replay revision.
// Manifest and session revisions take precedence over the checkout. A link is
// refused when the checkout is dirty or its revision cannot be proven, unless
// AllowAmbiguous was explicitly requested.
func NewGitLinkerWithRevision(startPath string, opts RevisionOptions) (*GitLinker, error) {
	root, err := findRepoRoot(startPath)
	if err != nil {
		return nil, fmt.Errorf("repository root not found from %q: %w", startPath, err)
	}
	remote, err := gitRemoteOrigin(root)
	if err != nil {
		return nil, fmt.Errorf("could not determine remote origin for repo at %q: %w", root, err)
	}
	if _, _, err := parseGitHubRemote(remote); err != nil {
		return nil, fmt.Errorf("unsupported repository remote %q: %w", remote, err)
	}
	provenance, revisionErr := resolveRevision(root, opts)
	if revisionErr != nil && !opts.AllowAmbiguous {
		logger.Logger.Debug("refusing ambiguous source link", "reason", revisionErr)
		return nil, fmt.Errorf("cannot create immutable source links: %w", revisionErr)
	}
	branch, _ := gitDefaultBranch(root)
	if branch == "" {
		branch = "main"
	}
	return &GitLinker{repoRoot: root, remoteURL: remote, defaultBranch: branch, provenance: provenance, allowAmbiguous: opts.AllowAmbiguous}, nil
}

// GitHubURL returns the GitHub blob URL for the given absolute source file path.
// It normalises the path relative to the repository root so that contracts in
// nested workspaces (e.g. contracts/token/src/lib.rs) resolve correctly.
func (g *GitLinker) GitHubURL(absFilePath string) (string, error) {
	owner, repo, err := parseGitHubRemote(g.remoteURL)
	if err != nil {
		return "", fmt.Errorf("cannot parse remote URL %q: %w", g.remoteURL, err)
	}
	if owner == "" || repo == "" {
		return "", fmt.Errorf("could not extract owner/repo from remote %q", g.remoteURL)
	}

	rel, err := pathutil.RelToSlash(g.repoRoot, absFilePath)
	if err != nil {
		return "", fmt.Errorf("cannot make %q relative to repo root %q: %w", absFilePath, g.repoRoot, err)
	}

	if strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("file %q is outside the repository root %q", absFilePath, g.repoRoot)
	}

	if err := validateSourcePath(rel); err != nil {
		return "", err
	}
	revision := g.provenance.Revision
	if !g.provenance.Immutable() {
		// Compatibility for callers that constructed GitLinker directly. Production
		// constructors never enter this branch unless the caller opted in.
		if !g.allowAmbiguous && g.provenance.Revision != "" {
			return "", fmt.Errorf("refusing ambiguous source link (%s)", g.provenance.Label())
		}
		if !g.allowAmbiguous && g.provenance.Revision == "" {
			revision = g.defaultBranch
		} else if revision == "" {
			revision = g.defaultBranch
		}
	}
	return fmt.Sprintf("https://github.com/%s/%s/blob/%s/%s", owner, repo, url.PathEscape(revision), escapeGitHubPath(rel)), nil
}

// Provenance returns the revision information that must be displayed with a link.
func (g *GitLinker) Provenance() RevisionProvenance { return g.provenance }

func validateSourcePath(rel string) error {
	clean := path.Clean(rel)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(rel, "\\") {
		return fmt.Errorf("unsafe repository-relative path %q", rel)
	}
	for _, segment := range strings.Split(clean, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("unsafe repository-relative path %q", rel)
		}
	}
	return nil
}

func escapeGitHubPath(rel string) string {
	parts := strings.Split(rel, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}

// RepoRoot returns the discovered repository root path.
func (g *GitLinker) RepoRoot() string { return g.repoRoot }

// findRepoRoot walks upward from startPath searching for a .git directory,
// Cargo.toml workspace manifest, or other repository root indicators.
// It returns the first directory that contains a .git entry.
func findRepoRoot(startPath string) (string, error) {
	abs, err := filepath.Abs(startPath)
	if err != nil {
		return "", err
	}

	// If startPath is a file, begin from its directory.
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	dir := abs
	if !info.IsDir() {
		dir = filepath.Dir(abs)
	}

	for {
		if isRepoRoot(dir) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break // reached filesystem root
		}
		dir = parent
	}

	return "", fmt.Errorf("no .git directory found above %q", startPath)
}

// isRepoRoot returns true when dir contains a .git entry (directory or file
// for git worktrees).
func isRepoRoot(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// parseGitHubRemote accepts only an actual github.com remote. Unlike generic
// repository configuration parsing it deliberately rejects owner/repo shorthand
// and lookalike hosts, which must not silently produce a GitHub link.
func parseGitHubRemote(raw string) (string, string, error) {
	if strings.HasPrefix(raw, "git@github.com:") {
		return parseGitHubURL(raw)
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "ssh") || !strings.EqualFold(u.Hostname(), "github.com") {
		return "", "", fmt.Errorf("remote is not a github.com URL")
	}
	return parseGitHubURL(raw)
}

// gitRemoteOrigin returns the remote origin URL for the repository at repoRoot.
func gitRemoteOrigin(repoRoot string) (string, error) {
	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git remote get-url origin: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// gitDefaultBranch returns the default branch name for the repository.
func gitDefaultBranch(repoRoot string) (string, error) {
	// Try symbolic-ref HEAD first (works for local checkouts).
	cmd := exec.Command("git", "symbolic-ref", "--short", "HEAD")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err == nil {
		branch := strings.TrimSpace(string(out))
		if branch != "" {
			return branch, nil
		}
	}

	// Fallback: ask the remote for its HEAD.
	cmd2 := exec.Command("git", "remote", "show", "origin")
	cmd2.Dir = repoRoot
	out2, err := cmd2.Output()
	if err != nil {
		return "main", nil
	}
	for _, line := range strings.Split(string(out2), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "HEAD branch:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "HEAD branch:")), nil
		}
	}
	return "main", nil
}

// NormalizeSourcePath resolves a potentially relative source path to an
// absolute path anchored at repoRoot, then returns the GitHub URL.
// Handles DWARF debug info with embedded relative or Windows-style paths.
func (g *GitLinker) NormalizeSourcePath(rawPath string) (string, error) {
	// Normalize cross-platform separators before checking absoluteness.
	normalized := pathutil.Normalize(rawPath)
	if filepath.IsAbs(normalized) || pathutil.IsWindowsAbs(rawPath) {
		return g.GitHubURL(normalized)
	}
	// Treat relative paths as relative to the repo root.
	abs := pathutil.Join(g.repoRoot, normalized)
	return g.GitHubURL(abs)
}
