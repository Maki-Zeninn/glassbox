// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package apicompat – CLI surface snapshot tests (issue #859).
//
// These tests generate deterministic snapshots of the CLI's public contract:
//   - Exit status codes (names + numeric values)
//   - Command names extracted from cobra Use fields across internal/cmd
//   - Error-code identifiers that map to specific exit statuses
//
// Snapshot files live in testdata/api-snapshots/cli-*.txt.
// Regenerate after intentional changes:
//
//	go test ./internal/apicompat/... -update
package apicompat

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// ─── CLI snapshot helpers ──────────────────────────────────────────────────────

// extractExitCodes parses the given file and returns a sorted slice of
// "NAME=value" strings for all integer const declarations.
func extractExitCodes(filename string) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", filename, err)
	}

	var results []string
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if !name.IsExported() {
					continue
				}
				// Try to resolve to an integer literal.
				if i < len(vs.Values) {
					val := resolveConstInt(vs.Values[i])
					if val != nil {
						results = append(results, fmt.Sprintf("%s=%s", name.Name, val.String()))
					}
				}
			}
		}
	}
	sort.Strings(results)
	return results, nil
}

// resolveConstInt attempts to evaluate a simple numeric constant expression.
// Returns nil when the expression is not a plain integer literal.
func resolveConstInt(expr ast.Expr) *constant.Value {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind == token.INT {
			v := constant.MakeFromLiteral(e.Value, e.Kind, 0)
			return &v
		}
	case *ast.UnaryExpr:
		inner := resolveConstInt(e.X)
		if inner == nil {
			return nil
		}
		v := constant.UnaryOp(e.Op, *inner, 0)
		return &v
	}
	return nil
}

// extractCobraUseFields walks all non-test Go source files in dir and returns
// a sorted list of cobra.Command Use field string values.
func extractCobraUseFields(dir string) ([]string, error) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", dir, err)
	}

	seen := map[string]struct{}{}
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				cl, ok := n.(*ast.CompositeLit)
				if !ok {
					return true
				}
				// Look for composite literals whose type selector ends in "Command".
				if !isCobraCommandLit(cl) {
					return true
				}
				for _, elt := range cl.Elts {
					kv, ok := elt.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					key, ok := kv.Key.(*ast.Ident)
					if !ok || key.Name != "Use" {
						continue
					}
					if lit, ok := kv.Value.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						val, err := strconv.Unquote(lit.Value)
						if err == nil && val != "" {
							// The Use field is "command [flags]..." – keep only the
							// first word (the command name) for stability.
							name := strings.Fields(val)[0]
							seen[name] = struct{}{}
						}
					}
				}
				return true
			})
		}
	}

	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names, nil
}

// isCobraCommandLit reports whether the composite literal appears to be a
// cobra.Command{...} initializer by checking the type selector.
func isCobraCommandLit(cl *ast.CompositeLit) bool {
	switch t := cl.Type.(type) {
	case *ast.SelectorExpr:
		return t.Sel.Name == "Command"
	case *ast.Ident:
		return t.Name == "Command"
	}
	return false
}

// formatCLISnapshot renders a labelled list of entries as a sorted text block.
func formatCLISnapshot(section string, entries []string) string {
	var b strings.Builder
	b.WriteString("# " + section + "\n")
	for _, e := range entries {
		b.WriteString(e + "\n")
	}
	return b.String()
}

// ─── Tests ─────────────────────────────────────────────────────────────────────

// TestCLIExitCodeSnapshot asserts that the exit-code constants in
// internal/cmd/exitcode.go and internal/cmd/interrupt.go have not changed.
// A change in numeric value is a breaking contract change for scripts and CI
// pipelines that rely on exit status.
func TestCLIExitCodeSnapshot(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")

	sources := []string{
		filepath.Join(repoRoot, "internal", "cmd", "exitcode.go"),
		filepath.Join(repoRoot, "internal", "cmd", "interrupt.go"),
	}

	var allCodes []string
	for _, src := range sources {
		if _, err := os.Stat(src); os.IsNotExist(err) {
			t.Skipf("source file %s does not exist", src)
		}
		codes, err := extractExitCodes(src)
		if err != nil {
			t.Fatalf("extractExitCodes(%s): %v", src, err)
		}
		allCodes = append(allCodes, codes...)
	}
	sort.Strings(allCodes)

	current := formatCLISnapshot("exit-codes", allCodes)
	label := "cli-exit-codes"

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
		if err := writeSnapshot(label, current); err != nil {
			t.Fatalf("write initial snapshot %s: %v", label, err)
		}
		t.Logf("created initial snapshot: %s", snapshotPath(label))
		return
	}
	if stored != current {
		added, removed := diffSymbolSets(parseSymbols(stored), parseSymbols(current))
		if len(removed) > 0 {
			t.Errorf(
				"exit-code contract broken — %d constant(s) removed or renamed:\n  - %s\n\n"+
					"Exit codes are part of the public CLI API. Scripts and CI pipelines\n"+
					"depend on these numeric values. If this change is intentional, update\n"+
					"the compatibility matrix and regenerate:\n"+
					"  go test ./internal/apicompat/... -update",
				len(removed),
				strings.Join(removed, "\n  - "),
			)
		}
		if len(added) > 0 {
			t.Logf("exit-code snapshot: %d new constant(s) added (non-breaking):\n  + %s",
				len(added), strings.Join(added, "\n  + "))
			if err := writeSnapshot(label, current); err != nil {
				t.Errorf("auto-update snapshot: %v", err)
			}
		}
	}
}

// TestCLICommandNameSnapshot asserts that the set of cobra.Command Use fields
// registered in internal/cmd has not lost any previously-recorded command names.
// Removing or renaming a command is a breaking change for users and scripts.
func TestCLICommandNameSnapshot(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	cmdDir := filepath.Join(repoRoot, "internal", "cmd")

	if _, err := os.Stat(cmdDir); os.IsNotExist(err) {
		t.Skipf("command directory %s does not exist", cmdDir)
	}

	names, err := extractCobraUseFields(cmdDir)
	if err != nil {
		t.Fatalf("extractCobraUseFields: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("no cobra.Command Use fields found – check the AST extractor")
	}

	current := formatCLISnapshot("command-names", names)
	label := "cli-command-names"

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
		if err := writeSnapshot(label, current); err != nil {
			t.Fatalf("write initial snapshot %s: %v", label, err)
		}
		t.Logf("created initial snapshot: %s", snapshotPath(label))
		return
	}
	if stored == current {
		return
	}
	added, removed := diffSymbolSets(parseSymbols(stored), parseSymbols(current))
	if len(removed) > 0 {
		t.Errorf(
			"CLI contract broken — %d command name(s) removed:\n  - %s\n\n"+
				"Command names are part of the public CLI API. Users and scripts\n"+
				"depend on these names. If the removal is intentional, document the\n"+
				"migration path and regenerate:\n"+
				"  go test ./internal/apicompat/... -update",
			len(removed),
			strings.Join(removed, "\n  - "),
		)
	}
	if len(added) > 0 {
		t.Logf("command-name snapshot: %d new command(s) added (non-breaking):\n  + %s",
			len(added), strings.Join(added, "\n  + "))
		if err := writeSnapshot(label, current); err != nil {
			t.Errorf("auto-update snapshot: %v", err)
		}
	}
}

// TestCLIErrorCodeExitMappingSnapshot snapshots which exported error code
// constants are classified as user errors vs configuration errors.  Changing
// the mapping alters the exit status scripts observe, which is a breaking
// change.
func TestCLIErrorCodeExitMappingSnapshot(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	exitcodeSrc := filepath.Join(repoRoot, "internal", "cmd", "exitcode.go")

	if _, err := os.Stat(exitcodeSrc); os.IsNotExist(err) {
		t.Skipf("exitcode.go not found at %s", exitcodeSrc)
	}

	mapping, err := extractErrorCodeMapping(exitcodeSrc)
	if err != nil {
		t.Fatalf("extractErrorCodeMapping: %v", err)
	}

	current := formatCLISnapshot("error-code-exit-mapping", mapping)
	label := "cli-error-code-mapping"

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
		if err := writeSnapshot(label, current); err != nil {
			t.Fatalf("write initial snapshot %s: %v", label, err)
		}
		t.Logf("created initial snapshot: %s", snapshotPath(label))
		return
	}
	if stored == current {
		return
	}
	added, removed := diffSymbolSets(parseSymbols(stored), parseSymbols(current))
	if len(removed) > 0 {
		t.Errorf(
			"error-code→exit-status mapping changed — %d mapping(s) removed:\n  - %s\n\n"+
				"Scripts that check exit codes rely on this mapping. Regenerate after\n"+
				"adding a compatibility note:\n"+
				"  go test ./internal/apicompat/... -update",
			len(removed),
			strings.Join(removed, "\n  - "),
		)
	}
	if len(added) > 0 {
		t.Logf("error-code mapping snapshot: %d mapping(s) added:\n  + %s",
			len(added), strings.Join(added, "\n  + "))
		if err := writeSnapshot(label, current); err != nil {
			t.Errorf("auto-update snapshot: %v", err)
		}
	}
}

// extractErrorCodeMapping parses exitcode.go to find map literals that map
// error-code constants to an exit category.  It returns sorted
// "ErrorCodeName → category" lines, where category is "user" or "config".
func extractErrorCodeMapping(filename string) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", filename, err)
	}

	// Heuristic: find var declarations whose type is map[...] bool.
	// The variable name carries the category: userErrorCodes → "user",
	// configErrorCodes → "config".
	categoryOf := map[string]string{
		"userErrorCodes":   "user",
		"configErrorCodes": "config",
	}

	var entries []string
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, nameIdent := range vs.Names {
				cat, known := categoryOf[nameIdent.Name]
				if !known {
					continue
				}
				if i >= len(vs.Values) {
					continue
				}
				cl, ok := vs.Values[i].(*ast.CompositeLit)
				if !ok {
					continue
				}
				for _, elt := range cl.Elts {
					kv, ok := elt.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					// Key is errors.ErstXxx
					var keyName string
					switch k := kv.Key.(type) {
					case *ast.SelectorExpr:
						keyName = k.Sel.Name
					case *ast.Ident:
						keyName = k.Name
					}
					if keyName != "" {
						entries = append(entries, fmt.Sprintf("%s → %s", keyName, cat))
					}
				}
			}
		}
	}

	sort.Strings(entries)
	return entries, nil
}

