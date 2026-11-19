// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package sourcemap

import (
	"os"
	"path/filepath"
)

// MappingResolver scores WASM-to-source mappings, attaches source links
// according to confidence thresholds, and classifies each resolved source
// path with a SourceOrigin so downstream consumers (terminal printer, SARIF
// writer, LSP publisher) can render the location appropriately.
type MappingResolver struct {
	mapper        *FallbackMapper
	startPath     string
	external      *ExternalRepoRegistry
	// workspaceRoot is the project root used for origin classification.
	// Defaults to startPath when empty.
	workspaceRoot string
	// cargoHome is the path to CARGO_HOME (e.g. ~/.cargo).
	// Populated automatically from the CARGO_HOME environment variable when
	// not supplied explicitly.
	cargoHome     string
	// cargoLockPath is the absolute path to the workspace Cargo.lock file.
	// Used by ExtractGitOrigin when generating links for git dependencies.
	cargoLockPath string
}

// MappingResolverOption is a functional option for NewMappingResolver.
type MappingResolverOption func(*MappingResolver)

// WithWorkspaceRoot sets the workspace root used for origin classification.
// When not provided the projectRoot passed to NewMappingResolver is used.
func WithWorkspaceRoot(root string) MappingResolverOption {
	return func(r *MappingResolver) { r.workspaceRoot = root }
}

// WithCargoHome sets the CARGO_HOME directory used for origin classification.
func WithCargoHome(cargoHome string) MappingResolverOption {
	return func(r *MappingResolver) { r.cargoHome = cargoHome }
}

// WithCargoLockPath sets the Cargo.lock path used when generating links for
// git-sourced crate dependencies.
func WithCargoLockPath(path string) MappingResolverOption {
	return func(r *MappingResolver) { r.cargoLockPath = path }
}

// NewMappingResolver creates a resolver that uses projectRoot for path
// resolution and optional external repo mappings for cross-repository links.
// Pass functional options to supply Cargo-specific metadata required for
// origin classification and external URL generation.
func NewMappingResolver(projectRoot string, external *ExternalRepoRegistry, opts ...MappingResolverOption) *MappingResolver {
	r := &MappingResolver{
		mapper:    NewFallbackMapper(projectRoot),
		startPath: projectRoot,
		external:  external,
	}
	for _, o := range opts {
		o(r)
	}
	// Fill defaults after applying options.
	if r.workspaceRoot == "" {
		r.workspaceRoot = projectRoot
	}
	if r.cargoHome == "" {
		if ch := os.Getenv("CARGO_HOME"); ch != "" {
			r.cargoHome = ch
		} else {
			// Standard default: ~/.cargo
			if home, err := os.UserHomeDir(); err == nil {
				r.cargoHome = filepath.Join(home, ".cargo")
			}
		}
	}
	if r.cargoLockPath == "" && projectRoot != "" {
		candidate := filepath.Join(projectRoot, "Cargo.lock")
		if _, err := os.Stat(candidate); err == nil {
			r.cargoLockPath = candidate
		}
	}
	return r
}

// Resolve maps wasmData+addr to a source location, applies confidence
// scoring, classifies the source origin, and populates SourceLink,
// CandidateLink, Origin, and ExternalURL fields of the returned result.
func (r *MappingResolver) Resolve(wasmData []byte, addr uint64) *FallbackResult {
	result := r.mapper.Resolve(wasmData, addr)
	if result == nil {
		return nil
	}
	r.attachSourceLink(result)
	r.attachOrigin(result)
	return result
}

// attachOrigin classifies the resolved file path and fills result.Origin and
// result.ExternalURL.  It is called after attachSourceLink so that SourceLink
// (the workspace GitHub permalink) is already set when we decide whether an
// ExternalURL is also needed.
func (r *MappingResolver) attachOrigin(result *FallbackResult) {
	if result.File == "" {
		result.Origin = OriginUnknownSource
		return
	}

	origin := ClassifySourcePath(result.File, r.workspaceRoot, r.cargoHome)
	result.Origin = origin

	// ExternalURL is only generated for non-local, non-unknown origins.
	// Local files have no web-accessible URL by definition.
	switch origin {
	case OriginLocal, OriginUnknownSource:
		result.ExternalURL = ""
	case OriginRegistry:
		result.ExternalURL = docsRSURLForPath(result.File)
	case OriginGit:
		result.ExternalURL = gitHubURLForCargoGit(result.File, r.cargoLockPath)
	}
}

func (r *MappingResolver) attachSourceLink(result *FallbackResult) {
	if result.File == "" {
		return
	}
	switch result.LinkPresentation {
	case LinkAuto, LinkCandidate:
	default:
		return
	}
	url, provenance, err := ResolveGitHubURLWithProvenance(r.startPath, result.File, r.external, RevisionOptions{})
	result.LinkProvenance = provenance.Label()
	if err != nil {
		// Keep the label in machine-readable mapping output even when policy
		// correctly refuses to create a potentially misleading link.
		return
	}
	if result.LinkPresentation == LinkAuto {
		result.SourceLink = url
		return
	}
	result.CandidateLink = url
}
