// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package sourcemap

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dotandev/glassbox/internal/dwarf"
	"github.com/dotandev/glassbox/internal/logger"
)

// Resolver coordinates fetching verified source code from a registry,
// with optional local caching and auto-discovery of local DWARF symbols.
type Resolver struct {
	registry        *RegistryClient
	cache           *SourceCache
	githubRetriever *GitHubRetriever
	// contractSourceOverride is an explicit local path to the contract source
	// directory. When set, it is used as a fallback before prompting the user.
	contractSourceOverride string
	// aliasResolver translates workspace-relative path prefixes to real
	// filesystem paths before source files are opened.
	aliasResolver *AliasResolver
	// nonInteractive disables the stdin prompt so Resolve never blocks in CI
	// pipelines or automated environments. When true and all automatic stages
	// fail, Resolve returns ErrSourceNotFound instead of reading from stdin.
	nonInteractive bool
	// buildManifestPath is the explicit path to a glassbox-build-manifest.json
	// file supplied via --build-manifest. When set it bypasses auto-discovery.
	buildManifestPath string
	// buildManifest is the loaded and validated manifest. It is populated
	// once (lazily or at construction time) and reused for all Resolve calls.
	buildManifest *BuildManifest
	// wasmPathForVerification is the local WASM artifact path forwarded from
	// --wasm so that the manifest's artifact_hash can be verified on load.
	wasmPathForVerification string
	// dwarfCaps is the DWARF capability report computed by the most recent
	// AutoDiscoverLocalSymbols call. Nil until that method succeeds in reading
	// a WASM file. Exposed via DWARFCapabilityReport().
	dwarfCaps *CapabilityReport
	// pathNormalizer provides cross-platform path normalization for DWARF source paths.
	pathNormalizer *PathNormalizer
}

// ErrSourceNotFound is a sentinel returned by Resolve when all discovery
// stages are exhausted and no interactive prompt is available (e.g. CI mode).
// Callers should check for it with errors.Is and surface a diagnostic.
var ErrSourceNotFound = fmt.Errorf("contract source not found: all discovery stages exhausted")

// ResolverOption is a functional option for configuring the Resolver.
type ResolverOption func(*Resolver)

// WithCache enables caching with the specified directory.
func WithCache(cacheDir string) ResolverOption {
	return func(r *Resolver) {
		cache, err := NewSourceCache(filepath.Join(cacheDir, "sourcemap"))
		if err != nil {
			logger.Logger.Warn("Failed to create source cache, caching disabled", "error", err)
			return
		}
		r.cache = cache
	}
}

// WithRegistryClient sets a custom registry client.
func WithRegistryClient(rc *RegistryClient) ResolverOption {
	return func(r *Resolver) {
		r.registry = rc
	}
}

// WithContractSource sets an explicit local path to the contract source
// directory. When automatic discovery fails, this path is used before
// prompting the user interactively (Issue #117).
func WithContractSource(path string) ResolverOption {
	return func(r *Resolver) {
		r.contractSourceOverride = path
	}
}

// WithAliasResolver sets an AliasResolver that translates workspace-relative
// path prefixes to real filesystem paths when resolving source file locations.
func WithAliasResolver(ar *AliasResolver) ResolverOption {
	return func(r *Resolver) {
		r.aliasResolver = ar
	}
}

// WithNonInteractive disables the stdin prompt so Resolve never blocks
// waiting for user input. Use this in CI pipelines and automated environments.
// When all discovery stages fail in non-interactive mode, Resolve returns
// ErrSourceNotFound instead of prompting.
func WithNonInteractive() ResolverOption {
	return func(r *Resolver) {
		r.nonInteractive = true
	}
}

// WithBuildManifest sets an explicit path to a glassbox-build-manifest.json
// file. When supplied the resolver loads and validates the manifest before
// the first Resolve call and uses it as stage 0 of the discovery pipeline.
// The manifest's artifact_hash is verified against wasmPath when non-empty.
func WithBuildManifest(manifestPath, wasmPath string) ResolverOption {
	return func(r *Resolver) {
		r.buildManifestPath = manifestPath
		r.wasmPathForVerification = wasmPath
	}
}

// WithBuildManifestLoaded injects an already-parsed BuildManifest directly,
// skipping file I/O. Useful in tests and when the caller has already called
// LoadManifestAndVerify.
func WithBuildManifestLoaded(m *BuildManifest) ResolverOption {
	return func(r *Resolver) {
		r.buildManifest = m
	}
}

// WithPathNormalizer sets a PathNormalizer for cross-platform path normalization.
// This enables separator normalization, workspace root resolution, and explicit
// remapping tables for DWARF source paths.
func WithPathNormalizer(normalizer *PathNormalizer) ResolverOption {
	return func(r *Resolver) {
		r.pathNormalizer = normalizer
	}
}

// NewResolver creates a Resolver with the given options.
func NewResolver(opts ...ResolverOption) *Resolver {
	r := &Resolver{
		registry: NewRegistryClient(),
	}
	for _, opt := range opts {
		opt(r)
	}
	// Eagerly load the build manifest when a path was provided so any file or
	// schema errors surface at construction time rather than mid-resolve.
	if r.buildManifestPath != "" && r.buildManifest == nil {
		m, err := LoadManifestAndVerify(r.buildManifestPath, r.wasmPathForVerification)
		if err != nil {
			// Surface as a warning; Resolve will still attempt other stages.
			logger.Logger.Warn("Failed to load build manifest; manifest stage disabled",
				"path", r.buildManifestPath, "error", err)
		} else {
			r.buildManifest = m
			logger.Logger.Info("Build manifest loaded",
				"path", r.buildManifestPath,
				"source_root", m.SourceRoot,
				"revision", m.RepositoryRevision,
			)
		}
	}
	return r
}

// Resolve attempts to find verified source code for the given contract ID
// through a multi-stage discovery pipeline:
//
//  0. Build manifest (explicit --build-manifest path or auto-discovered
//     glassbox-build-manifest.json) — produces a SourceCode whose Repository
//     field is the manifest's SourceRoot so DWARF path remapping works
//     immediately on any machine.
//  1. Local cache
//  2. Registry (stellar.expert)
//  3. GitHub retriever (when configured)
//  4. --contract-source override (Issue #117)
//  5. Interactive stdin prompt — or ErrSourceNotFound in non-interactive mode
//
// Failure modes:
//   - Invalid contractID: returns a validation error immediately.
//   - Manifest artifact_hash mismatch: returns ManifestHashMismatchError immediately.
//   - All stages exhausted, non-interactive mode: returns ErrSourceNotFound.
//   - Prompt read failure (interactive): returns a wrapped I/O error.
//   - Returns (nil, nil) only when the user provides an empty path at the
//     interactive prompt (explicit opt-out of source mapping).
func (r *Resolver) Resolve(ctx context.Context, contractID string) (*SourceCode, error) {
	if err := validateContractID(contractID); err != nil {
		return nil, fmt.Errorf("invalid contract ID %q: %w\n"+
			"  Contract IDs must start with 'C' and be exactly 56 characters long.", contractID, err)
	}

	// 0. Build manifest stage — highest priority because it encodes the exact
	//    build provenance and can remap paths without any network access.
	if r.buildManifest != nil {
		logger.Logger.Info("Source resolved from build manifest",
			"contract_id", contractID,
			"source_root", r.buildManifest.SourceRoot,
			"revision", r.buildManifest.RepositoryRevision,
		)
		return &SourceCode{
			ContractID: contractID,
			// Repository is set to SourceRoot so downstream DWARF resolvers use
			// it as the base directory for relative path lookups.
			Repository: r.buildManifest.SourceRoot,
			// WasmHash carries the artifact_hash for any downstream hash checks.
			WasmHash:  r.buildManifest.ArtifactHash,
			Files:     map[string]string{},
			FetchedAt: time.Now(),
		}, nil
	}

	// 1. Check cache first.
	if r.cache != nil {
		if cached := r.cache.Get(contractID); cached != nil {
			logger.Logger.Info("Source resolved from cache", "contract_id", contractID)
			return cached, nil
		}
	}

	// 2. Fetch from registry.
	source, err := r.registry.FetchVerifiedSource(ctx, contractID)
	if err != nil {
		logger.Logger.Debug("Registry lookup failed", "contract_id", contractID, "error", err)
	}

	// 3. GitHub fallback.
	if source == nil && r.githubRetriever != nil {
		ghSource, ghErr := r.githubRetriever.Retrieve(ctx, contractID)
		if ghErr != nil {
			logger.Logger.Debug("GitHub source retrieval failed",
				"contract_id", contractID, "error", ghErr)
		} else if ghSource != nil {
			logger.Logger.Info("Source resolved from GitHub",
				"contract_id", contractID,
				"repository", ghSource.Repository,
				"file_count", len(ghSource.Files),
			)
			if r.cache != nil {
				if cacheErr := r.cache.Put(ghSource); cacheErr != nil {
					logger.Logger.Warn("Failed to cache GitHub source",
						"contract_id", contractID, "error", cacheErr)
				}
			}
			return ghSource, nil
		}
	}

	// 4. Fallback: use explicit override path if provided (Issue #117).
	//    Validate it here so callers get actionable errors rather than a
	//    silent no-op if the path is wrong.
	if source == nil && r.contractSourceOverride != "" {
		overridePath := strings.TrimSpace(r.contractSourceOverride)
		if overridePath == "" {
			return nil, fmt.Errorf(
				"--contract-source: value must not be empty or whitespace\n" +
					"  Provide the path to your contract's source directory (the one containing src/).",
			)
		}
		info, statErr := os.Stat(overridePath)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				return nil, fmt.Errorf(
					"--contract-source: directory not found: %q\n"+
						"  Provide the path to your contract's source directory (the one containing src/).\n"+
						"  Source mapping will be unavailable without a valid path.",
					overridePath,
				)
			}
			return nil, fmt.Errorf(
				"--contract-source: cannot access %q: %w", overridePath, statErr)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf(
				"--contract-source: %q is a file, not a directory\n"+
					"  Provide the path to your contract's source directory, not a file.",
				overridePath,
			)
		}
		logger.Logger.Info("Using --contract-source override for source mapping",
			"contract_id", contractID,
			"path", overridePath,
		)
		return &SourceCode{
			ContractID: contractID,
			Repository: overridePath,
			Files:      map[string]string{},
			FetchedAt:  time.Now(),
		}, nil
	}

	// 5. Prompt or non-interactive failure.
	if source == nil {
		if r.nonInteractive {
			logger.Logger.Warn("Source discovery exhausted all stages (non-interactive mode)",
				"contract_id", contractID)
			return nil, fmt.Errorf(
				"%w for contract %q\n"+
					"  Stages tried: build manifest, cache, registry (stellar.expert), GitHub retriever, --contract-source override\n"+
					"  To resolve: provide --build-manifest <path> pointing to a glassbox-build-manifest.json,\n"+
					"  or --contract-source <path> pointing to the contract source directory,\n"+
					"  or verify the contract on stellar.expert to enable registry lookup.\n"+
					"  Use --skip-source-mapping to proceed without source mapping.",
				ErrSourceNotFound, contractID,
			)
		}

		logger.Logger.Info("Contract source unresolved automatically; prompting user",
			"contract_id", contractID)
		manualPath, promptErr := r.PromptForWasmPath()
		if promptErr != nil {
			return nil, fmt.Errorf(
				"failed to read manual WASM path from stdin: %w\n"+
					"  In non-interactive environments use --contract-source <path> or --skip-source-mapping.",
				promptErr,
			)
		}
		manualPath = strings.TrimSpace(manualPath)
		if manualPath == "" {
			logger.Logger.Info("User chose to skip manual WASM path entry")
			return nil, nil
		}
		info, statErr := os.Stat(manualPath)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				return nil, fmt.Errorf(
					"manual WASM path %q does not exist\n"+
						"  Provide a valid path to the contract WASM file or use --contract-source <path> or --skip-source-mapping.",
					manualPath,
				)
			}
			return nil, fmt.Errorf("manual WASM path %q is not accessible: %w", manualPath, statErr)
		}
		if info.IsDir() {
			return nil, fmt.Errorf(
				"manual WASM path %q is a directory, not a file\n"+
					"  Provide the path to the compiled .wasm artifact.",
				manualPath,
			)
		}
		logger.Logger.Info("Manual WASM path provided by user", "path", manualPath)
		return nil, nil
	}

	// 6. Cache the successfully resolved result.
	if r.cache != nil {
		if cacheErr := r.cache.Put(source); cacheErr != nil {
			logger.Logger.Warn("Failed to cache source",
				"contract_id", contractID, "error", cacheErr)
		}
	}

	logger.Logger.Info("Source resolved from registry",
		"contract_id", contractID,
		"repository", source.Repository,
		"file_count", len(source.Files),
	)
	return source, nil
}

// PromptForWasmPath pauses execution and asks the user for a manual WASM path.
// Required by Issue #372: "Please provide path to contract WASM for better mapping".
func (r *Resolver) PromptForWasmPath() (string, error) {
	fmt.Print("Please provide path to contract WASM for better mapping: ")
	reader := bufio.NewReader(os.Stdin)
	path, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(path), nil
}

// AutoDiscoverManifest attempts to find a glassbox-build-manifest.json in
// conventional locations under projectRoot and, if found, loads and validates
// it (including artifact hash verification against wasmPath when non-empty).
// On success the manifest is stored in the resolver for use by ResolveFilePath
// and Resolve.  The method is a no-op when the resolver already has a manifest
// (explicit --build-manifest takes priority over auto-discovery).
func (r *Resolver) AutoDiscoverManifest(projectRoot, wasmPath string) {
	if r.buildManifest != nil {
		return // explicit manifest wins; don't override
	}

	discovered, err := DiscoverManifest(projectRoot)
	if err != nil {
		logger.Logger.Debug("Manifest auto-discovery error", "project_root", projectRoot, "error", err)
		return
	}
	if discovered == "" {
		logger.Logger.Debug("No build manifest found during auto-discovery", "project_root", projectRoot)
		return
	}

	m, loadErr := LoadManifestAndVerify(discovered, wasmPath)
	if loadErr != nil {
		logger.Logger.Warn("Auto-discovered manifest could not be loaded; skipping manifest stage",
			"path", discovered, "error", loadErr)
		return
	}

	r.buildManifest = m
	logger.Logger.Info("Build manifest auto-discovered and loaded",
		"path", discovered,
		"source_root", m.SourceRoot,
		"revision", m.RepositoryRevision,
	)
}

// AutoDiscoverLocalSymbols scans the project root for local WASM builds.
// If a bytecode hash match is found, it loads and merges DWARF debug symbols.
//
// Validation:
//   - projectRoot must not be empty — returns an error immediately.
//   - expectedHash must not be empty — returns an error immediately.
//   - A missing build directory is treated as a non-fatal debug hint; the
//     caller can continue without local symbols.
func (r *Resolver) AutoDiscoverLocalSymbols(projectRoot string, expectedHash string) error {
	if projectRoot == "" {
		return fmt.Errorf(
			"AutoDiscoverLocalSymbols: projectRoot must not be empty\n" +
				"  Hint: pass the path to your Rust workspace root.",
		)
	}
	if expectedHash == "" {
		return fmt.Errorf(
			"AutoDiscoverLocalSymbols: expectedHash must not be empty\n" +
				"  Hint: provide the SHA-256 hex hash of the on-chain contract bytecode.",
		)
	}

	discovery, discErr := DiscoverLocalSymbols(projectRoot)
	if discErr != nil {
		// A missing build directory is diagnostic, not fatal.
		logger.Logger.Debug("Local symbol discovery: build directory not available",
			"project_root", projectRoot, "reason", discErr.Error())
		return nil
	}

	for _, w := range discovery.Warnings {
		logger.Logger.Warn("Local symbol discovery warning", "detail", w)
	}

	matchedPath, ok := discovery.Found[expectedHash]
	if !ok {
		logger.Logger.Debug("No local WASM matches on-chain hash",
			"expected_hash", expectedHash,
			"candidates_checked", len(discovery.Found),
			"search_dir", discovery.SearchDir,
		)
		return nil
	}

	content, readErr := os.ReadFile(matchedPath)
	if readErr != nil {
		return fmt.Errorf(
			"local WASM match found at %q but could not be read: %w\n"+
				"  Check file permissions and try again.",
			matchedPath, readErr,
		)
	}

	logger.Logger.Info("Found local WASM match",
		"file", filepath.Base(matchedPath), "path", matchedPath)

	// ── DWARF capability detection ────────────────────────────────────────────
	// Run capability detection *before* attempting any DWARF tree parsing.
	// This is cheap (reads the section table only) and gives precise,
	// structured diagnostics that distinguish:
	//   • stripped binaries (no debug sections)
	//   • unsupported DWARF version (names the version and the supported range)
	//   • partial debug info (names the missing sections and lost capabilities)
	capReport := DetectCapabilitiesFromBytes(content)
	r.dwarfCaps = capReport

	// Emit a structured log entry for each issue so operators can filter by
	// the "dwarf_*" check label.
	for _, issue := range capReport.Issues {
		switch issue.Severity {
		case "error":
			logger.Logger.Error("DWARF capability check failed",
				"check", issue.Check,
				"description", issue.Description,
				"hint", issue.Hint,
				"file", filepath.Base(matchedPath),
			)
		default:
			logger.Logger.Warn("DWARF capability warning",
				"check", issue.Check,
				"description", issue.Description,
				"hint", issue.Hint,
				"file", filepath.Base(matchedPath),
			)
		}
	}

	// When the binary has no usable mapping capability at all, surface the
	// DWARF summary and return early with a user-friendly error — do not
	// attempt further parsing that would produce a generic ErrNoDebugInfo.
	if !capReport.DWARF.Supported {
		logger.Logger.Warn(
			"Local WASM found but DWARF capabilities are insufficient for source mapping",
			"file", filepath.Base(matchedPath),
			"dwarf_summary", capReport.DWARF.Summary(),
		)
		// Return nil (not an error) so the caller can continue without symbols;
		// the structured issues in r.dwarfCaps are available for display.
		return nil
	}

	// ── Parse DWARF tree ─────────────────────────────────────────────────────
	parser, parseErr := dwarf.NewParser(content)
	if parseErr != nil {
		// Attach the capability report to the error message so the user knows
		// exactly which version/sections were detected.
		return fmt.Errorf(
			"failed to parse DWARF from %q: %w\n"+
				"  DWARF capabilities: %s\n"+
				"  Ensure the WASM was compiled with 'debug = true' in [profile.release].",
			matchedPath, parseErr, capReport.DWARF.Summary(),
		)
	}

	// Apply path normalizer if configured
	if r.pathNormalizer != nil {
		parser.SetPathNormalizer(r.pathNormalizer)
	}

	if !parser.HasDebugInfo() {
		logger.Logger.Warn(
			"Local WASM found but contains no DWARF debug symbols — source mapping will be limited",
			"file", filepath.Base(matchedPath),
			"hint", "recompile with 'debug = true' in [profile.release] for accurate source mapping",
		)
		return nil
	}

	// When the parser has a capability report of its own (set by NewParser),
	// merge any additional warnings it produced into our stored report so the
	// accessor always reflects the most up-to-date information.
	if pc := parser.Capabilities(); pc != nil && len(pc.Warnings) > len(capReport.DWARF.Warnings) {
		merged := DetectCapabilitiesFromBytes(content)
		r.dwarfCaps = merged
	}

	subprograms, spErr := parser.GetSubprograms()
	if spErr != nil {
		return fmt.Errorf(
			"failed to extract DWARF subprograms from %q: %w",
			matchedPath, spErr,
		)
	}

	logger.Logger.Info("Automatically merged symbols from local build",
		"file", filepath.Base(matchedPath),
		"count", len(subprograms),
		"dwarf_version", capReport.DWARF.Version,
		"supported_mappings", fmt.Sprintf("lines=%v vars=%v inlines=%v",
			capReport.DWARF.Mappings.SourceLines,
			capReport.DWARF.Mappings.LocalVars,
			capReport.DWARF.Mappings.InlineFrames,
		),
	)
	return nil
}

// ResolveFilePath applies path normalization (when configured), manifest path
// remapping (when a build manifest is loaded), and the alias resolver (if configured)
// to translate a build-machine source path to a real filesystem path on the current machine.
// Returns p unchanged when none are configured.
func (r *Resolver) ResolveFilePath(p string) string {
	// Stage 0: Apply path normalization if configured
	if r.pathNormalizer != nil {
		normalizedPath, diag := r.pathNormalizer.NormalizePath(p)
		if diag != nil && diag.Severity == "error" {
			// Log the error but continue with original path for compatibility
			// The diagnostic will be available via the normalizer's GetDiagnostics()
		} else if normalizedPath != "" {
			p = normalizedPath
		}
	}

	// Stage A: strip the build-machine SourceRoot prefix recorded in the
	// manifest so only the repo-relative tail survives.
	if r.buildManifest != nil {
		p = r.buildManifest.RemapPath(p)
	}
	// Stage B: alias resolver maps workspace-relative prefixes to real paths.
	if r.aliasResolver != nil {
		p = r.aliasResolver.Resolve(p)
	}
	return p
}

// Manifest returns the loaded BuildManifest, or nil when none was configured.
func (r *Resolver) Manifest() *BuildManifest {
	return r.buildManifest
}

// InvalidateCache removes a specific contract from the cache.
func (r *Resolver) InvalidateCache(contractID string) error {
	if r.cache == nil {
		return nil
	}
	return r.cache.Invalidate(contractID)
}

// ClearCache removes all cached source entries.
func (r *Resolver) ClearCache() error {
	if r.cache == nil {
		return nil
	}
	return r.cache.Clear()
}
