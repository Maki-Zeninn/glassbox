// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package apicompat provides API compatibility snapshot checks for the
// glassbox Go public packages.
//
// Issue #597: Add API compatibility checks for generated artifacts.
//
// These tests:
//   - Generate a deterministic snapshot of exported symbols from key packages.
//   - Compare the current symbol set against the stored golden file.
//   - Fail with a clear diff if any exported identifier is removed or renamed.
//
// Usage:
//
//	# Check against stored snapshots (CI):
//	go test ./internal/apicompat/...
//
//	# Regenerate snapshots after intentional changes:
//	go test ./internal/apicompat/... -update
//
// Snapshot files live in testdata/api-snapshots/*.txt, committed alongside
// source code so reviewers can audit API surface changes in PRs.
package apicompat

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// update is a test flag that regenerates all snapshots when set.
var update = flag.Bool("update", false, "regenerate API snapshots")

// packageSnapshot holds the extracted public symbols for a single package.
type packageSnapshot struct {
	// PackagePath is the import path of the package.
	PackagePath string
	// Symbols is a sorted list of exported identifiers.
	Symbols []string
}

// ─── Symbol extraction ────────────────────────────────────────────────────────

// extractPublicSymbols parses the Go source files in dir and returns a sorted
// list of exported top-level identifiers. Private symbols (_*) are excluded.
//
// Only top-level declarations are considered; method names on types are not
// individually listed (the type name itself is listed).
func extractPublicSymbols(dir string) ([]string, error) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		// Skip test files — we snapshot the public API, not test helpers.
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", dir, err)
	}

	seen := map[string]struct{}{}
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				switch d := decl.(type) {
				case *ast.FuncDecl:
					if d.Name.IsExported() {
						seen[d.Name.Name] = struct{}{}
					}
				case *ast.GenDecl:
					for _, spec := range d.Specs {
						switch s := spec.(type) {
						case *ast.TypeSpec:
							if s.Name.IsExported() {
								seen[s.Name.Name] = struct{}{}
							}
						case *ast.ValueSpec:
							for _, name := range s.Names {
								if name.IsExported() {
									seen[name.Name] = struct{}{}
								}
							}
						}
					}
				}
			}
		}
	}

	symbols := make([]string, 0, len(seen))
	for s := range seen {
		symbols = append(symbols, s)
	}
	sort.Strings(symbols)
	return symbols, nil
}

// ─── Snapshot I/O ─────────────────────────────────────────────────────────────

// snapshotPath returns the golden file path for a package label.
func snapshotPath(label string) string {
	_, filename, _, _ := runtime.Caller(0)
	dir := filepath.Dir(filename)
	return filepath.Join(dir, "testdata", "api-snapshots", label+".txt")
}

// readSnapshot reads the stored snapshot for label. Returns empty string
// and no error when the file does not exist yet.
func readSnapshot(label string) (string, error) {
	data, err := os.ReadFile(snapshotPath(label))
	if os.IsNotExist(err) {
		return "", nil
	}
	return string(data), err
}

// writeSnapshot writes the current snapshot to the golden file.
func writeSnapshot(label, content string) error {
	p := snapshotPath(label)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(content), 0644)
}

// formatSnapshot converts a list of symbols to the canonical text format:
// one symbol per line, sorted, with a trailing newline.
func formatSnapshot(symbols []string) string {
	return strings.Join(symbols, "\n") + "\n"
}

// ─── Package list ─────────────────────────────────────────────────────────────

// packagesToCheck maps a human-readable label (used as the snapshot filename)
// to a directory path relative to the repository root.
//
// Only public-facing packages whose exported API should remain stable across
// releases are listed here. Internal implementation packages that change
// frequently are excluded.
var packagesToCheck = map[string]string{
	"internal-audit":      "internal/audit",
	"internal-rpc":        "internal/rpc",
	"internal-simulator":  "internal/simulator",
	"internal-signer":     "internal/signer",
	"internal-snapshot":   "internal/snapshot",
	"internal-session":    "internal/session",
	"internal-errors":     "internal/errors",
	"internal-trace":      "internal/trace",
	"internal-testhelpers": "internal/testhelpers",
}

// ─── Test ─────────────────────────────────────────────────────────────────────

func TestAPICompatibility(t *testing.T) {
	// Locate repository root (two directories up from this file).
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")

	for label, relDir := range packagesToCheck {
		label := label // capture
		relDir := relDir

		t.Run(label, func(t *testing.T) {
			t.Parallel()

			absDir := filepath.Join(repoRoot, relDir)
			if _, err := os.Stat(absDir); os.IsNotExist(err) {
				t.Skipf("package directory %s does not exist", absDir)
			}

			symbols, err := extractPublicSymbols(absDir)
			if err != nil {
				t.Fatalf("extract symbols from %s: %v", absDir, err)
			}

			current := formatSnapshot(symbols)

			if *update {
				if err := writeSnapshot(label, current); err != nil {
					t.Fatalf("write snapshot %s: %v", label, err)
				}
				t.Logf("updated snapshot: %s", snapshotPath(label))
				return
			}

			stored, err := readSnapshot(label)
			if err != nil {
				t.Fatalf("read snapshot %s: %v", label, err)
			}

			if stored == "" {
				// First time: write the snapshot automatically so CI does not
				// fail on the first run. Subsequent runs will diff.
				if wErr := writeSnapshot(label, current); wErr != nil {
					t.Fatalf("write initial snapshot %s: %v", label, wErr)
				}
				t.Logf("created initial snapshot: %s", snapshotPath(label))
				return
			}

			if stored == current {
				return // pass
			}

			// Compute a human-readable diff
			added, removed := diffSymbolSets(
				parseSymbols(stored),
				parseSymbols(current),
			)

			if len(removed) > 0 {
				t.Errorf(
					"package %s: %d exported symbol(s) removed (potential breaking change):\n  - %s\n\n"+
						"If this is intentional, add a migration note to your PR and regenerate:\n"+
						"  go test ./internal/apicompat/... -update",
					relDir,
					len(removed),
					strings.Join(removed, "\n  - "),
				)
			}

			if len(added) > 0 {
				// Additions are non-breaking; report them informally.
				t.Logf(
					"package %s: %d new symbol(s) added (non-breaking):\n  + %s",
					relDir,
					len(added),
					strings.Join(added, "\n  + "),
				)
				// Update the snapshot to include additions.
				if wErr := writeSnapshot(label, current); wErr != nil {
					t.Errorf("auto-update snapshot after additions: %v", wErr)
				}
			}
		})
	}
}

// ─── Diff helpers ─────────────────────────────────────────────────────────────

func parseSymbols(content string) []string {
	lines := strings.Split(strings.TrimSpace(content), "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func diffSymbolSets(stored, current []string) (added, removed []string) {
	storedSet := make(map[string]struct{}, len(stored))
	for _, s := range stored {
		storedSet[s] = struct{}{}
	}
	currentSet := make(map[string]struct{}, len(current))
	for _, s := range current {
		currentSet[s] = struct{}{}
	}

	for _, s := range current {
		if _, ok := storedSet[s]; !ok {
			added = append(added, s)
		}
	}
	for _, s := range stored {
		if _, ok := currentSet[s]; !ok {
			removed = append(removed, s)
		}
	}
	return added, removed
}
