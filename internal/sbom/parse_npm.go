// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package sbom

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// packageLockV2 is the subset of package-lock.json (npm lockfile v2/v3) that
// we need. v1 lockfiles use a "dependencies" map; v2/v3 use "packages".
// We handle both layouts.
type packageLockV2 struct {
	LockfileVersion int `json:"lockfileVersion"`
	// Packages is the v2/v3 map keyed by install path, e.g. "node_modules/foo".
	Packages map[string]npmPackageEntry `json:"packages"`
	// Dependencies is the v1 map keyed by package name.
	Dependencies map[string]npmDepV1Entry `json:"dependencies"`
}

type npmPackageEntry struct {
	Version  string `json:"version"`
	Dev      bool   `json:"dev"`
	Optional bool   `json:"optional"`
	// Link is true for workspace symlinks, which we skip.
	Link bool `json:"link"`
}

type npmDepV1Entry struct {
	Version  string `json:"version"`
	Dev      bool   `json:"dev"`
	Optional bool   `json:"optional"`
	// Recursive dependencies in v1 format.
	Dependencies map[string]npmDepV1Entry `json:"dependencies"`
}

// ParsePackageLock reads an npm package-lock.json file (v1, v2, or v3) from r
// and returns a Component for every non-workspace, non-dev-optional package.
//
// Dev-only and optional packages ARE included because they are part of the
// build toolchain used to produce release artifacts (e.g. TypeScript compiler,
// jest). The caller can filter them out if needed.
//
// Workspace root entries (the entry whose key is "" in v2/v3 lockfiles) are
// skipped because they represent the project itself, not a dependency.
func ParsePackageLock(r io.Reader) ([]Component, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("reading package-lock.json: %w", err)
	}

	var lock packageLockV2
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, fmt.Errorf("parsing package-lock.json: %w", err)
	}

	switch lock.LockfileVersion {
	case 0, 1:
		// v1 format — use "dependencies" map.
		return parseNPMV1(lock.Dependencies), nil
	default:
		// v2 / v3 format — use "packages" map.
		return parseNPMV2(lock.Packages), nil
	}
}

// parseNPMV2 processes the "packages" map from a v2/v3 lockfile.
func parseNPMV2(packages map[string]npmPackageEntry) []Component {
	components := make([]Component, 0, len(packages))
	for installPath, entry := range packages {
		// Skip the workspace root (empty key or key == ".").
		if installPath == "" || installPath == "." {
			continue
		}
		// Skip workspace symlinks.
		if entry.Link {
			continue
		}
		if entry.Version == "" {
			continue
		}

		name := npmNameFromInstallPath(installPath)
		version := strings.TrimSpace(entry.Version)

		components = append(components, Component{
			Ecosystem: EcosystemNPM,
			Name:      name,
			Version:   version,
			PURL:      BuildPURL(EcosystemNPM, name, version),
			SPDXID:    BuildSPDXID(EcosystemNPM, name, version),
		})
	}
	return components
}

// parseNPMV1 recursively processes the "dependencies" map from a v1 lockfile.
func parseNPMV1(deps map[string]npmDepV1Entry) []Component {
	var components []Component
	for name, entry := range deps {
		if entry.Version == "" {
			continue
		}
		version := strings.TrimSpace(entry.Version)
		components = append(components, Component{
			Ecosystem: EcosystemNPM,
			Name:      name,
			Version:   version,
			PURL:      BuildPURL(EcosystemNPM, name, version),
			SPDXID:    BuildSPDXID(EcosystemNPM, name, version),
		})
		// Recurse into nested dependencies (v1 can nest arbitrarily).
		if len(entry.Dependencies) > 0 {
			components = append(components, parseNPMV1(entry.Dependencies)...)
		}
	}
	return components
}

// npmNameFromInstallPath extracts the package name from a v2/v3 install path.
// Paths look like:
//
//	"node_modules/foo"           → "foo"
//	"node_modules/@scope/foo"    → "@scope/foo"
//	"node_modules/a/node_modules/b" → "b"   (nested install, take the last segment)
func npmNameFromInstallPath(path string) string {
	// Strip any "node_modules/" prefix, recurring for nested installs.
	const prefix = "node_modules/"
	for strings.HasPrefix(path, prefix) {
		path = path[len(prefix):]
	}
	// Handle scoped packages: "@scope/name" — the last two components after
	// splitting on "/" give us "@scope" + "name".
	if strings.HasPrefix(path, "@") {
		parts := strings.SplitN(path, "/", 3)
		if len(parts) >= 2 {
			return parts[0] + "/" + parts[1]
		}
	}
	// Non-scoped: take everything up to the first slash (strips nested path).
	if idx := strings.IndexByte(path, '/'); idx >= 0 {
		return path[:idx]
	}
	return path
}
