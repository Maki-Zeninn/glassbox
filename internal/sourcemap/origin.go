// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package sourcemap — origin classification for DWARF-resolved source paths.
//
// OriginClass tags every resolved source location so trace viewers and
// formatters can present user-authored code differently from generated build
// artifacts and external crate dependencies without hiding any frames.
//
// Classification is heuristic-based and opt-in: callers pass the resolved
// path to Classifier.Classify and receive one of four OriginClass values.
// The classifier never touches the filesystem; it works purely on path
// strings so it is cheap to call for every frame in a trace.

package sourcemap

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// osReadFile is a package-level variable so tests can override filesystem reads.
var osReadFile = os.ReadFile

// OriginClass describes where a resolved source path came from.
// The zero value is OriginUnknown.
//
// The string representation of each constant matches the JSON value used in
// trace exports (see internal/trace/splitpane.go's SourceRef.OriginClass).
// Keep these values stable — they are part of the published JSON schema.
type OriginClass string

const (
	// OriginUser is user-authored source code under the project root.
	// Frames with this class are shown without additional decoration.
	OriginUser OriginClass = "user"

	// OriginGenerated is machine-generated build output: Rust WASM artifacts
	// under target/, macro-expanded code, proc-macro output, or other files
	// produced during compilation rather than hand-written by the developer.
	// Trace formatters label these frames "[generated]" so developers know
	// the path may not exist in their workspace.
	OriginGenerated OriginClass = "generated"

	// OriginExternal is source code from an external crate or a dependency
	// outside the project workspace.  This includes Cargo registry paths,
	// Git checkouts, and any absolute path that falls outside the project
	// root.  Formatters label these frames "[external]".
	OriginExternal OriginClass = "external"

	// OriginUnknown is used when the classifier cannot determine the origin,
	// typically because the path is empty or does not match any known pattern.
	OriginUnknown OriginClass = "unknown"
)

// ClassifierOptions configures the heuristics applied by Classifier.Classify.
// All fields are optional; zero values produce reasonable defaults.
type ClassifierOptions struct {
	// ProjectRoot is the absolute path to the workspace root.  When set, any
	// absolute path that is not a child of ProjectRoot and not under a known
	// build directory is classified as OriginExternal.
	ProjectRoot string

	// ExtraBuildDirs is an optional list of additional build-directory
	// prefixes to treat as OriginGenerated (relative or absolute).
	// These are checked after the built-in patterns.
	ExtraBuildDirs []string

	// ExtraExternalPrefixes is an optional list of path prefixes (relative or
	// absolute) that should always be classified as OriginExternal.
	ExtraExternalPrefixes []string
}

// Classifier classifies DWARF-resolved source paths by their origin.
// Construct with NewClassifier and reuse across multiple Classify calls.
type Classifier struct {
	opts ClassifierOptions
}

// NewClassifier creates a Classifier configured with the given options.
// Passing a zero-value ClassifierOptions is valid and produces a classifier
// that applies only the built-in heuristics.
func NewClassifier(opts ClassifierOptions) *Classifier {
	// Normalise the project root once at construction time.
	if opts.ProjectRoot != "" {
		opts.ProjectRoot = filepath.ToSlash(opts.ProjectRoot)
		opts.ProjectRoot = strings.TrimRight(opts.ProjectRoot, "/")
	}
	return &Classifier{opts: opts}
}

// Classify returns the OriginClass for the given raw source path.
//
// Classification rules (applied in priority order):
//
//  1. Empty path → OriginUnknown.
//  2. Path is under a Cargo registry or Cargo git checkout → OriginExternal.
//  3. Path ends with .wasm or contains target/wasm32 → OriginGenerated.
//  4. Path contains /target/ (or starts with target/) → OriginGenerated.
//  5. ExtraBuildDirs match → OriginGenerated.
//  6. ExtraExternalPrefixes match → OriginExternal.
//  7. ProjectRoot is set and path is absolute and not under ProjectRoot
//     → OriginExternal.
//  8. Otherwise → OriginUser.
//
// The rules are intentionally conservative: a path that matches neither a
// build-directory pattern nor an external-dependency pattern is classified
// as OriginUser so user frames are never accidentally hidden.
func (c *Classifier) Classify(rawPath string) OriginClass {
	if rawPath == "" {
		return OriginUnknown
	}

	// Normalise separators for consistent matching.
	p := filepath.ToSlash(rawPath)

	// ── 1. Cargo registry / git dependency paths → external ────────────────
	if isCargoPath(p) {
		return OriginExternal
	}

	// ── 2. WASM artifacts and wasm32 target directory → generated ───────────
	if strings.HasSuffix(p, ".wasm") ||
		strings.Contains(p, "target/wasm32-unknown-unknown") ||
		strings.Contains(p, "target/wasm32") {
		return OriginGenerated
	}

	// ── 3. Generic Rust build output directories → generated ────────────────
	if strings.Contains(p, "/target/") || strings.HasPrefix(p, "target/") {
		return OriginGenerated
	}

	// ── 4. Caller-supplied extra build directories → generated ───────────────
	for _, dir := range c.opts.ExtraBuildDirs {
		normalised := filepath.ToSlash(strings.TrimRight(dir, "/"))
		if normalised != "" && (strings.Contains(p, normalised) || strings.HasPrefix(p, normalised)) {
			return OriginGenerated
		}
	}

	// ── 5. Caller-supplied extra external prefixes → external ───────────────
	for _, prefix := range c.opts.ExtraExternalPrefixes {
		normalised := filepath.ToSlash(strings.TrimRight(prefix, "/"))
		if normalised != "" && strings.HasPrefix(p, normalised) {
			return OriginExternal
		}
	}

	// ── 6. Absolute path outside the project root → external ─────────────────
	if c.opts.ProjectRoot != "" && isAbsoluteSlash(p) {
		if !strings.HasPrefix(p, c.opts.ProjectRoot+"/") && p != c.opts.ProjectRoot {
			return OriginExternal
		}
	}

	// ── 7. Default → user source ─────────────────────────────────────────────
	return OriginUser
}

// ClassifyPath is a convenience wrapper that creates a one-shot Classifier
// with only the ProjectRoot configured and classifies the given path.
// For hot paths (e.g. classifying many frames in a single trace), prefer
// constructing a Classifier once and calling Classify repeatedly.
func ClassifyPath(rawPath, projectRoot string) OriginClass {
	return NewClassifier(ClassifierOptions{ProjectRoot: projectRoot}).Classify(rawPath)
}

// Label returns a short human-readable label for the origin class suitable for
// appending to a source location line in terminal and JSON output.
//
//	OriginUser      → ""           (no annotation; user code is the happy path)
//	OriginGenerated → "[generated]"
//	OriginExternal  → "[external]"
//	OriginUnknown   → "[unknown origin]"
func (c OriginClass) Label() string {
	switch c {
	case OriginGenerated:
		return "[generated]"
	case OriginExternal:
		return "[external]"
	case OriginUnknown:
		return "[unknown origin]"
	default:
		return ""
	}
}

// ── Internal helpers ───────────────────────────────────────────────────────

// isCargoPath returns true for paths that come from the Cargo registry or a
// Cargo git checkout — these are always external dependencies.
func isCargoPath(p string) bool {
	return strings.Contains(p, "/.cargo/registry") ||
		strings.Contains(p, "/.cargo/git") ||
		strings.Contains(p, ".cargo/registry/src/") ||
		strings.Contains(p, "/registry/src/") ||
		strings.Contains(p, "\\.cargo\\registry") ||
		strings.Contains(p, "\\.cargo\\git")
}

// isAbsoluteSlash reports whether the forward-slash normalised path is absolute
// (starts with / or has a Windows drive letter prefix like C:/).
func isAbsoluteSlash(p string) bool {
	if strings.HasPrefix(p, "/") {
		return true
	}
	// Windows drive letter: C:/...
	if len(p) >= 3 && p[1] == ':' && p[2] == '/' {
		return true
	}
	return false
}

// ─── SourceOrigin: integer enum for in-process link generation ───────────────
//
// SourceOrigin is a distinct type from OriginClass.  OriginClass (a string
// enum) is the stable, published JSON schema value that appears in trace
// exports.  SourceOrigin (an int enum) is the in-process type used by the
// link generator and terminal printer so that switch exhaustion is enforced
// at compile time.  The two are kept consistent via ClassifySourcePath.

// SourceOrigin is the in-process origin classification used by the link
// generator and output formatters.
type SourceOrigin int

const (
	// OriginUnknownSource is the zero value — path was empty or unrecognised.
	// No external URL is generated.
	OriginUnknownSource SourceOrigin = iota
	// OriginLocal means the source file lives in the developer's own workspace
	// (under the project root).  No external URL is produced because the file
	// exists only on the developer's machine.
	OriginLocal
	// OriginRegistry means the crate was downloaded from a Cargo registry
	// (crates.io or a private mirror).  An external docs.rs URL pointing at the
	// specific version is generated.
	OriginRegistry
	// OriginGit means the crate is pinned to a specific commit in a remote Git
	// repository via Cargo.lock.  A URL anchored to that commit is generated by
	// ExtractGitOrigin.
	OriginGit
)

// OriginPrefix returns the terminal display prefix for the origin.
//
//	OriginLocal            → "[local]"
//	OriginRegistry         → "[crates.io]"
//	OriginGit              → "[git]"
//	OriginUnknownSource    → ""
func (o SourceOrigin) OriginPrefix() string {
	switch o {
	case OriginLocal:
		return "[local]"
	case OriginRegistry:
		return "[crates.io]"
	case OriginGit:
		return "[git]"
	default:
		return ""
	}
}

// ClassifySourcePath converts a raw file path to a SourceOrigin using the
// same heuristics as OriginClass but returning the int-typed enum that the
// link generator and terminal printer switch on.
//
// cargoHome is the path to the user's CARGO_HOME directory (e.g.
// ~/.cargo on Unix).  An empty string disables the per-home check and falls
// back to the path-pattern heuristics that are always applied.
func ClassifySourcePath(rawPath, workspaceRoot, cargoHome string) SourceOrigin {
	if rawPath == "" {
		return OriginUnknownSource
	}
	p := filepath.ToSlash(rawPath)

	// ── Registry paths (crates.io / private mirror) ──────────────────────────
	// Standard Cargo registry layout: ~/.cargo/registry/src/<index>/<crate-ver>/
	if isCargoPath(p) {
		if strings.Contains(p, "/.cargo/git") ||
			strings.Contains(p, "\\.cargo\\git") ||
			strings.Contains(p, "/git/checkouts/") {
			return OriginGit
		}
		return OriginRegistry
	}

	// Explicit cargoHome prefix check (covers non-standard CARGO_HOME locations).
	if cargoHome != "" {
		ch := filepath.ToSlash(strings.TrimRight(cargoHome, "/"))
		if strings.HasPrefix(p, ch+"/registry/") {
			return OriginRegistry
		}
		if strings.HasPrefix(p, ch+"/git/") {
			return OriginGit
		}
	}

	// ── Local workspace ───────────────────────────────────────────────────────
	if workspaceRoot == "" {
		// Without a root we cannot tell local from external; conservative default.
		return OriginLocal
	}
	root := filepath.ToSlash(strings.TrimRight(workspaceRoot, "/"))
	if isAbsoluteSlash(p) {
		if strings.HasPrefix(p, root+"/") || p == root {
			return OriginLocal
		}
		// Absolute path outside the workspace root — treat as unknown (could be
		// an external crate that is not under .cargo; the SARIF/terminal caller
		// will see OriginUnknownSource and omit the URL).
		return OriginUnknownSource
	}
	// Relative paths are workspace-relative by convention.
	return OriginLocal
}

// ─── Cargo.lock parsing ───────────────────────────────────────────────────────

// GitOriginInfo holds the remote repository URL and commit hash extracted from
// a Cargo.lock [[package]] entry for a git-sourced dependency.
type GitOriginInfo struct {
	// RepoURL is the bare HTTPS repository URL, e.g. "https://github.com/owner/repo".
	RepoURL string
	// CommitHash is the full 40-character commit SHA pinned in Cargo.lock.
	CommitHash string
}

// ExtractGitOrigin locates the Cargo.lock entry whose source field matches the
// crate embedded in sourcePath and returns the remote repository URL and
// pinned commit hash.
//
// sourcePath should be the full path to a source file inside a Cargo git
// checkout, for example:
//
//	/home/user/.cargo/git/checkouts/soroban-sdk-abc123/src/lib.rs
//
// cargoLockPath is the absolute path to the workspace Cargo.lock.  If it is
// empty the function returns an error.
//
// The parser is a minimal line-by-line TOML reader; no third-party TOML
// library is required.
func ExtractGitOrigin(sourcePath, cargoLockPath string) (GitOriginInfo, error) {
	if cargoLockPath == "" {
		return GitOriginInfo{}, fmt.Errorf("ExtractGitOrigin: cargoLockPath must not be empty")
	}
	if sourcePath == "" {
		return GitOriginInfo{}, fmt.Errorf("ExtractGitOrigin: sourcePath must not be empty")
	}

	data, err := readFile(cargoLockPath)
	if err != nil {
		return GitOriginInfo{}, fmt.Errorf("ExtractGitOrigin: cannot read Cargo.lock at %q: %w", cargoLockPath, err)
	}

	return parseCargoLockForGitOrigin(sourcePath, string(data))
}

// readFile is a thin wrapper around os.ReadFile to keep the dependency
// surface of the package-level function testable.
func readFile(path string) ([]byte, error) {
	return osReadFile(path)
}

// parseCargoLockForGitOrigin implements the actual Cargo.lock parsing. It is
// split from ExtractGitOrigin so tests can inject content without touching the
// filesystem.
//
// Cargo.lock v3 format relevant section:
//
//	[[package]]
//	name    = "soroban-sdk"
//	version = "21.7.6"
//	source  = "git+https://github.com/stellar/rs-soroban-sdk?rev=abc1234#abcdef0123456789..."
//	checksum = "..."
func parseCargoLockForGitOrigin(sourcePath, lockContent string) (GitOriginInfo, error) {
	p := filepath.ToSlash(sourcePath)

	// Extract a candidate crate directory name from the source path.
	// Cargo git checkouts follow the layout:
	//   <CARGO_HOME>/git/checkouts/<crate>-<hash>/<short-hash>/src/...
	// We grab the first directory component after "git/checkouts/".
	crateDir := extractGitCheckoutDir(p)

	lines := strings.Split(lockContent, "\n")

	type pkgEntry struct {
		name     string
		version  string
		source   string
		checksum string
	}

	var (
		packages []pkgEntry
		current  pkgEntry
		inPkg    bool
	)

	flush := func() {
		if inPkg && current.source != "" {
			packages = append(packages, current)
		}
		current = pkgEntry{}
	}

	for _, raw := range lines {
		line := strings.TrimSpace(raw)

		if line == "[[package]]" {
			flush()
			inPkg = true
			continue
		}
		if strings.HasPrefix(line, "[[") && line != "[[package]]" {
			flush()
			inPkg = false
			continue
		}
		if !inPkg {
			continue
		}

		key, val := splitTOMLKeyVal(line)
		switch key {
		case "name":
			current.name = val
		case "version":
			current.version = val
		case "source":
			current.source = val
		case "checksum":
			current.checksum = val
		}
	}
	flush()

	// Now find the best-matching package entry.
	// Priority: entry whose name appears in the checkout directory name (or
	// vice-versa), then the first git entry found.
	// The checkout dir format is "<crate-name>-<cargo-hash>" so we check
	// whether the directory name starts with the crate name (hyphen or underscore).
	var best *pkgEntry
	for i := range packages {
		entry := &packages[i]
		if !strings.HasPrefix(entry.source, "git+") {
			continue
		}
		if crateDir != "" && crateDirMatchesName(crateDir, entry.name) {
			best = entry
			break
		}
		if best == nil {
			best = entry
		}
	}

	if best == nil {
		return GitOriginInfo{}, fmt.Errorf(
			"ExtractGitOrigin: no git-sourced [[package]] entry found in Cargo.lock for path %q",
			sourcePath,
		)
	}

	repoURL, commitHash := parseGitSource(best.source, best.checksum)
	if repoURL == "" {
		return GitOriginInfo{}, fmt.Errorf(
			"ExtractGitOrigin: could not parse repository URL from source %q",
			best.source,
		)
	}

	return GitOriginInfo{RepoURL: repoURL, CommitHash: commitHash}, nil
}

// extractGitCheckoutDir returns the directory component immediately after
// "git/checkouts/" in a Cargo git checkout path, or "" if not found.
func extractGitCheckoutDir(p string) string {
	const marker = "/git/checkouts/"
	idx := strings.Index(p, marker)
	if idx < 0 {
		return ""
	}
	rest := p[idx+len(marker):]
	// The next path component is the crate directory (e.g. soroban-sdk-abc123).
	end := strings.IndexByte(rest, '/')
	if end < 0 {
		return rest
	}
	return rest[:end]
}

// crateDirMatchesName reports whether a Cargo git checkout directory name
// (e.g. "soroban-sdk-abc123ef" or "soroban_env_host-deadbeef") corresponds
// to a crate named crateName.
//
// Cargo appends a hex suffix separated by a hyphen to the crate name when
// creating the checkout directory, so the directory always starts with the
// crate name followed by a "-".  Both hyphen and underscore variants of the
// name are checked because Cargo normalises hyphens to underscores in some
// contexts.
func crateDirMatchesName(checkoutDir, crateName string) bool {
	if checkoutDir == "" || crateName == "" {
		return false
	}
	// Normalise to hyphen so comparisons are consistent.
	dirNorm := strings.ReplaceAll(checkoutDir, "_", "-")
	nameNorm := strings.ReplaceAll(crateName, "_", "-")
	// The directory starts with "<crate-name>-" followed by the cargo hash.
	return strings.HasPrefix(dirNorm, nameNorm+"-") || dirNorm == nameNorm
}

// splitTOMLKeyVal splits a TOML key = "value" line and returns the unquoted
// key and value.  Returns ("", "") for lines that are not key=value pairs.
func splitTOMLKeyVal(line string) (string, string) {
	idx := strings.IndexByte(line, '=')
	if idx < 0 {
		return "", ""
	}
	key := strings.TrimSpace(line[:idx])
	val := strings.TrimSpace(line[idx+1:])
	// Strip surrounding quotes (single or double).
	if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') ||
		(val[0] == '\'' && val[len(val)-1] == '\'')) {
		val = val[1 : len(val)-1]
	}
	return key, val
}

// parseGitSource parses a Cargo.lock git source string of the form:
//
//	git+https://github.com/owner/repo?rev=abc#fullsha
//	git+https://github.com/owner/repo#fullsha
//
// and returns the bare repository URL and the 40-char commit hash.
// When the hash is absent or shorter than 40 chars the function falls back to
// the first 40 chars of checksum.
func parseGitSource(source, checksum string) (repoURL, commitHash string) {
	// Strip leading "git+".
	s := strings.TrimPrefix(source, "git+")

	// Extract the hash after "#".
	if hashIdx := strings.LastIndexByte(s, '#'); hashIdx >= 0 {
		commitHash = s[hashIdx+1:]
		s = s[:hashIdx]
	}

	// Strip query string (?rev=... or ?branch=... or ?tag=...).
	// Check for a rev= parameter first — it may be a short SHA; if so we
	// prefer the fragment hash (full SHA) we already extracted.
	if qIdx := strings.IndexByte(s, '?'); qIdx >= 0 {
		query := s[qIdx+1:]
		s = s[:qIdx]
		// If no fragment hash was found, try the rev= query parameter.
		if commitHash == "" {
			for _, param := range strings.Split(query, "&") {
				if strings.HasPrefix(param, "rev=") {
					commitHash = strings.TrimPrefix(param, "rev=")
					break
				}
			}
		}
	}

	// Strip ".git" suffix from the repository URL for clean links.
	repoURL = strings.TrimSuffix(s, ".git")

	// If commitHash is shorter than 40 chars, fall back to the checksum field
	// (first 40 hex chars of the package checksum).
	if len(commitHash) < 40 && len(checksum) >= 40 {
		commitHash = checksum[:40]
	}

	return repoURL, commitHash
}

// ─── URL generation helpers ───────────────────────────────────────────────────

// ExternalURLForResult generates an appropriate external URL for a source
// location based on its SourceOrigin.
//
//   - OriginLocal: returns "" (the file is on the developer's machine only).
//   - OriginRegistry: returns a docs.rs permalink for the crate and version.
//   - OriginGit: parses Cargo.lock (via cargoLockPath) and returns a GitHub
//     permalink anchored to the pinned commit.
//   - OriginUnknownSource: returns "".
//
// The function never errors out fatally; callers that need fault details should
// call ClassifySourcePath + ExtractGitOrigin separately.
func ExternalURLForResult(sourcePath, workspaceRoot, cargoHome, cargoLockPath string) string {
	origin := ClassifySourcePath(sourcePath, workspaceRoot, cargoHome)
	switch origin {
	case OriginLocal, OriginUnknownSource:
		return ""
	case OriginRegistry:
		return docsRSURLForPath(sourcePath)
	case OriginGit:
		return gitHubURLForCargoGit(sourcePath, cargoLockPath)
	}
	return ""
}

// docsRSURLForPath builds a docs.rs URL for a Cargo registry source path.
//
// Registry layout:  .../registry/src/<index>/<crate-version>/src/...
// docs.rs layout:   https://docs.rs/<crate>/<version>/src/<crate>/<file-rel>.html
//
// The function extracts crate name and version from the directory name
// (<crate>-<version>) that immediately follows the registry index directory.
func docsRSURLForPath(sourcePath string) string {
	p := filepath.ToSlash(sourcePath)

	// Find the registry/src/ segment.
	const marker = "/registry/src/"
	idx := strings.Index(p, marker)
	if idx < 0 {
		return ""
	}
	rest := p[idx+len(marker):]

	// Skip the index directory (e.g. index.crates.io-6f17d22bba15001f).
	slashIdx := strings.IndexByte(rest, '/')
	if slashIdx < 0 {
		return ""
	}
	rest = rest[slashIdx+1:]

	// Next component is <crate>-<version>.
	slashIdx = strings.IndexByte(rest, '/')
	var crateVer, relPath string
	if slashIdx < 0 {
		crateVer = rest
	} else {
		crateVer = rest[:slashIdx]
		relPath = rest[slashIdx+1:]
	}

	// Split <crate>-<version>: version starts at the last component that begins
	// with a digit after a hyphen.
	name, version := splitCrateVersion(crateVer)
	if name == "" {
		return ""
	}

	if relPath == "" {
		return fmt.Sprintf("https://docs.rs/%s/%s", name, version)
	}
	// docs.rs source URL format: /crate/<name>/<version>/source/src/...
	// Strip a leading "src/" if present — docs.rs adds it automatically.
	relPath = strings.TrimPrefix(relPath, "src/")
	return fmt.Sprintf("https://docs.rs/crate/%s/%s/source/src/%s", name, version, relPath)
}

// splitCrateVersion splits a Cargo crate directory name of the form
// "<crate>-<version>" into its name and version parts.  The version is the
// last hyphen-separated suffix that begins with a digit.
func splitCrateVersion(crateVer string) (name, version string) {
	// Walk from the end looking for a component that starts with a digit.
	for i := len(crateVer) - 1; i >= 0; i-- {
		if crateVer[i] == '-' && i+1 < len(crateVer) && crateVer[i+1] >= '0' && crateVer[i+1] <= '9' {
			return crateVer[:i], crateVer[i+1:]
		}
	}
	return crateVer, ""
}

// gitHubURLForCargoGit generates a GitHub permalink for a git dependency by
// looking up the repository URL and pinned commit in Cargo.lock.
func gitHubURLForCargoGit(sourcePath, cargoLockPath string) string {
	if cargoLockPath == "" {
		return ""
	}
	info, err := ExtractGitOrigin(sourcePath, cargoLockPath)
	if err != nil || info.RepoURL == "" {
		return ""
	}

	// Build the repo-relative file path.
	// Git checkout layout: <CARGO_HOME>/git/checkouts/<crate>-<hash>/<short-hash>/<rel>
	p := filepath.ToSlash(sourcePath)
	const marker = "/git/checkouts/"
	idx := strings.Index(p, marker)
	if idx < 0 {
		return ""
	}
	rest := p[idx+len(marker):]
	// Skip crate directory.
	s1 := strings.IndexByte(rest, '/')
	if s1 < 0 {
		return ""
	}
	rest = rest[s1+1:]
	// Skip short-hash directory.
	s2 := strings.IndexByte(rest, '/')
	if s2 < 0 {
		return ""
	}
	relPath := rest[s2+1:]

	if relPath == "" || info.CommitHash == "" {
		return fmt.Sprintf("%s/tree/%s", info.RepoURL, info.CommitHash)
	}
	return fmt.Sprintf("%s/blob/%s/%s", info.RepoURL, info.CommitHash, relPath)
}
