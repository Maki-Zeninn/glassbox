// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package sbom

import (
	"fmt"
	"os"
	"strings"
)

// GenerateOptions configures a call to GenerateFromFiles.
type GenerateOptions struct {
	// GoModulesJSON is the path to the JSON file produced by
	// `go list -m -json all > go-modules.json`, OR a literal JSON string
	// starting with '{'. When empty, Go modules are skipped.
	GoModulesJSON string

	// CargoLockPath is the path to Cargo.lock. When empty, Cargo crates are skipped.
	CargoLockPath string

	// PackageLockPath is the path to package-lock.json. When empty, npm packages
	// are skipped.
	PackageLockPath string

	// Context provides the build provenance fields embedded in the SBOM header.
	Context BuildContext
}

// GenerateResult is the output of GenerateFromFiles.
type GenerateResult struct {
	// Document is the fully populated SPDX document.
	Document *SPDXDocument
	// ComponentCounts maps ecosystem name to the number of components sourced
	// from that ecosystem.
	ComponentCounts map[Ecosystem]int
	// Warnings lists non-fatal issues (e.g. an optional input file was missing).
	Warnings []string
}

// GenerateFromFiles parses all available ecosystem lock/module files, merges
// the resulting components, and returns a GenerateResult containing the
// assembled SPDX document.
//
// At least one of GoModulesJSON, CargoLockPath, or PackageLockPath in opts
// must be non-empty, otherwise an error is returned.
//
// Missing optional files are recorded as warnings rather than hard errors,
// because a project may not use all three ecosystems. The caller can inspect
// GenerateResult.Warnings to decide whether to abort.
func GenerateFromFiles(opts GenerateOptions) (*GenerateResult, error) {
	if opts.GoModulesJSON == "" && opts.CargoLockPath == "" && opts.PackageLockPath == "" {
		return nil, fmt.Errorf("sbom: at least one of GoModulesJSON, CargoLockPath, or PackageLockPath must be provided")
	}

	result := &GenerateResult{
		ComponentCounts: make(map[Ecosystem]int),
	}
	var allComponents []Component

	// ── Go modules ───────────────────────────────────────────────────────────
	if opts.GoModulesJSON != "" {
		goComps, warn, err := loadGoModules(opts.GoModulesJSON)
		if err != nil {
			return nil, fmt.Errorf("sbom: loading Go modules: %w", err)
		}
		if warn != "" {
			result.Warnings = append(result.Warnings, warn)
		}
		allComponents = append(allComponents, goComps...)
		result.ComponentCounts[EcosystemGo] = len(goComps)
	}

	// ── Cargo crates ─────────────────────────────────────────────────────────
	if opts.CargoLockPath != "" {
		cargoComps, warn, err := loadCargoLock(opts.CargoLockPath)
		if err != nil {
			return nil, fmt.Errorf("sbom: loading Cargo.lock: %w", err)
		}
		if warn != "" {
			result.Warnings = append(result.Warnings, warn)
		}
		allComponents = append(allComponents, cargoComps...)
		result.ComponentCounts[EcosystemCargo] = len(cargoComps)
	}

	// ── npm packages ─────────────────────────────────────────────────────────
	if opts.PackageLockPath != "" {
		npmComps, warn, err := loadPackageLock(opts.PackageLockPath)
		if err != nil {
			return nil, fmt.Errorf("sbom: loading package-lock.json: %w", err)
		}
		if warn != "" {
			result.Warnings = append(result.Warnings, warn)
		}
		allComponents = append(allComponents, npmComps...)
		result.ComponentCounts[EcosystemNPM] = len(npmComps)
	}

	if len(allComponents) == 0 && len(result.Warnings) > 0 {
		return nil, fmt.Errorf("sbom: no components loaded — all input files were missing or empty:\n  %s",
			strings.Join(result.Warnings, "\n  "))
	}

	doc := GenerateDocument(allComponents, opts.Context)
	if err := Validate(doc); err != nil {
		return nil, fmt.Errorf("sbom: generated document failed validation: %w", err)
	}

	result.Document = doc
	return result, nil
}

// ── private loaders ───────────────────────────────────────────────────────────

// loadGoModules reads Go module JSON from either a file path or an inline JSON
// string. Returns (components, warning, error).
func loadGoModules(pathOrJSON string) ([]Component, string, error) {
	r, warn, err := openInput(pathOrJSON)
	if err != nil {
		return nil, "", err
	}
	if r == nil {
		return nil, warn, nil
	}
	defer r.Close()
	comps, err := ParseGoModules(r)
	if err != nil {
		return nil, "", err
	}
	return comps, "", nil
}

// loadCargoLock reads Cargo.lock from disk. Returns (components, warning, error).
func loadCargoLock(path string) ([]Component, string, error) {
	r, warn, err := openInput(path)
	if err != nil {
		return nil, "", err
	}
	if r == nil {
		return nil, warn, nil
	}
	defer r.Close()
	comps, err := ParseCargoLock(r)
	if err != nil {
		return nil, "", err
	}
	return comps, "", nil
}

// loadPackageLock reads package-lock.json from disk. Returns (components, warning, error).
func loadPackageLock(path string) ([]Component, string, error) {
	r, warn, err := openInput(path)
	if err != nil {
		return nil, "", err
	}
	if r == nil {
		return nil, warn, nil
	}
	defer r.Close()
	comps, err := ParsePackageLock(r)
	if err != nil {
		return nil, "", err
	}
	return comps, "", nil
}

// openInput opens a path for reading. Returns (nil, warning, nil) when the
// file does not exist — missing ecosystem files are non-fatal warnings.
// Returns (nil, "", error) for permission or other hard errors.
// When path looks like inline JSON (starts with '{'), it wraps it in a
// strings.Reader without any disk access.
type nopCloser struct{ *strings.Reader }

func (nopCloser) Close() error { return nil }

func openInput(path string) (interface {
	Read([]byte) (int, error)
	Close() error
}, string, error) {
	// Inline JSON (for testing or pipe injection).
	if strings.HasPrefix(strings.TrimSpace(path), "{") {
		return nopCloser{strings.NewReader(path)}, "", nil
	}

	f, err := os.Open(path) //nolint:gosec
	if os.IsNotExist(err) {
		return nil, fmt.Sprintf("optional input file not found (skipped): %s", path), nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("opening %s: %w", path, err)
	}
	return f, "", nil
}
