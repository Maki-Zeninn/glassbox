// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// printer_golden_test.go compares every supported trace output mode against
// checked-in golden files, using the canonical fixtures defined in
// golden_fixtures_test.go. The goal is to catch semantic drift between the
// terminal, text, Markdown, HTML, and JSON printers: each compiles and has
// isolated tests, but a field silently dropped from one format is invisible
// without a cross-format comparison.
//
// Golden files live under testdata/printer_golden/. CI fails when they are
// stale. Regenerate with:
//
//	go test ./internal/trace -run TestPrinterGolden -update
//
// then review the diff — the golden diff IS the review artifact for an
// intentional formatting change. See docs/printer-golden-tests.md.
//
// Normalization policy: fixtures pin every wall-clock timestamp at build
// time, so the only volatile bytes left are OS line endings, and comparison
// normalizes exactly that (CRLF → LF). Do not widen normalization: scrubbing
// stable fields would hide exactly the drift these tests exist to catch.
package trace

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// -update regenerates the golden files instead of comparing.
var updateGolden = flag.Bool("update", false, "regenerate printer golden files")

const printerGoldenDir = "testdata/printer_golden"

// terminalModeName is the interactive printer (PrintExecutionTrace). It is not
// an export format but drifts the same way, so it is golden-tested alongside.
const terminalModeName = "terminal"

// goldenModeNames returns every output mode under golden test: the terminal
// printer plus each supported export format. Deriving the list from
// SupportedExportFormats means a newly added export format fails these tests
// until a renderer is wired and goldens are generated — that is the CI gate
// for accidental omissions.
func goldenModeNames() []string {
	return append([]string{terminalModeName}, SupportedExportFormats()...)
}

// renderGoldenMode renders a fixture in the given output mode.
func renderGoldenMode(f goldenFixture, mode string) (string, error) {
	switch mode {
	case terminalModeName:
		var buf bytes.Buffer
		PrintExecutionTrace(f.trace, PrintOptions{NoColor: true, MaxWidth: 100, Output: &buf})
		return buf.String(), nil
	case "html":
		return GenerateTraceHTMLWithOptions(f.trace, f.opts)
	case "markdown":
		return GenerateTraceMarkdownWithOptions(f.trace, f.opts)
	case "text":
		return GenerateTracePlainTextWithOptions(f.trace, f.opts)
	case "json":
		// Mirrors the json branch of ExportExecutionTraceWithOptions.
		b, err := json.MarshalIndent(f.trace, "", "  ")
		if err != nil {
			return "", err
		}
		return string(b), nil
	default:
		return "", fmt.Errorf("no golden renderer wired for output mode %q — add a case in renderGoldenMode and regenerate goldens with -update", mode)
	}
}

// normalizeGoldenOutput unifies line endings, the only environment-volatile
// bytes in the otherwise fully deterministic fixture output.
func normalizeGoldenOutput(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

func goldenPath(fixture, mode string) string {
	return filepath.Join(printerGoldenDir, fixture+"_"+mode+".golden")
}

// firstDiffLine returns a 1-based line number and the differing want/got lines
// for a compact failure message.
func firstDiffLine(want, got string) (int, string, string) {
	wantLines := strings.Split(want, "\n")
	gotLines := strings.Split(got, "\n")
	n := len(wantLines)
	if len(gotLines) > n {
		n = len(gotLines)
	}
	for i := 0; i < n; i++ {
		var w, g string
		if i < len(wantLines) {
			w = wantLines[i]
		} else {
			w = "<missing>"
		}
		if i < len(gotLines) {
			g = gotLines[i]
		} else {
			g = "<missing>"
		}
		if w != g {
			return i + 1, w, g
		}
	}
	return 0, "", ""
}

// ── golden comparison ────────────────────────────────────────────────────────

// TestPrinterGolden renders every fixture in every output mode and compares
// the normalized output against the checked-in golden file.
func TestPrinterGolden(t *testing.T) {
	for _, fixture := range goldenFixtures() {
		for _, mode := range goldenModeNames() {
			t.Run(fixture.name+"/"+mode, func(t *testing.T) {
				out, err := renderGoldenMode(fixture, mode)
				if err != nil {
					t.Fatalf("render %s/%s: %v", fixture.name, mode, err)
				}
				got := normalizeGoldenOutput(out)
				path := goldenPath(fixture.name, mode)

				if *updateGolden {
					if err := os.MkdirAll(printerGoldenDir, 0o755); err != nil {
						t.Fatalf("mkdir %s: %v", printerGoldenDir, err)
					}
					if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
						t.Fatalf("write golden %s: %v", path, err)
					}
					t.Logf("updated golden: %s", path)
					return
				}

				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf(
						"golden %s not found — run 'go test ./internal/trace -run TestPrinterGolden -update' to generate it",
						path,
					)
				}
				want := normalizeGoldenOutput(string(raw))

				if want != got {
					line, w, g := firstDiffLine(want, got)
					t.Errorf(
						"%s output for fixture %q is stale (first diff at line %d)\n"+
							"  want: %q\n"+
							"  got:  %q\n"+
							"  golden: %s\n"+
							"  If this change is intentional, run:\n"+
							"    go test ./internal/trace -run TestPrinterGolden -update\n"+
							"  and commit the golden diff for review.",
						mode, fixture.name, line, w, g, path,
					)
				}
			})
		}
	}
}

// ── semantic consistency across modes ────────────────────────────────────────

// TestPrinterGolden_SemanticConsistency asserts that the semantic fields of
// each fixture (hashes, functions, contracts, errors, costs, sources,
// comments) survive into every output mode that is supposed to carry them.
// This is the direct guard against a printer dropping meaning while its own
// isolated tests stay green.
func TestPrinterGolden_SemanticConsistency(t *testing.T) {
	containsToken := func(out, mode, token string) bool {
		if strings.Contains(out, token) {
			return true
		}
		// html/template escapes content; accept the escaped form.
		return mode == "html" && strings.Contains(out, template.HTMLEscapeString(token))
	}

	for _, fixture := range goldenFixtures() {
		for _, mode := range goldenModeNames() {
			t.Run(fixture.name+"/"+mode, func(t *testing.T) {
				out, err := renderGoldenMode(fixture, mode)
				if err != nil {
					t.Fatalf("render %s/%s: %v", fixture.name, mode, err)
				}

				for _, token := range fixture.commonTokens {
					if !containsToken(out, mode, token) {
						t.Errorf("mode %s dropped semantic token %q for fixture %q", mode, token, fixture.name)
					}
				}
				if mode == terminalModeName {
					// The terminal printer intentionally omits export-only
					// detail (source locations, annotations).
					return
				}
				for _, token := range fixture.exportTokens {
					if !containsToken(out, mode, token) {
						t.Errorf("export mode %s dropped semantic token %q for fixture %q", mode, token, fixture.name)
					}
				}
			})
		}
	}
}

// ── coverage and omission gates ──────────────────────────────────────────────

// TestPrinterGolden_ExportPathCoversAllFormats proves the canonical format
// list and the ExportExecutionTrace switch cannot drift apart: every listed
// format must export a representative fixture end-to-end.
func TestPrinterGolden_ExportPathCoversAllFormats(t *testing.T) {
	fixture := goldenFixtures()[0] // calls
	for _, format := range SupportedExportFormats() {
		t.Run(format, func(t *testing.T) {
			if err := ValidateTraceFormatCompatibility(fixture.trace, format); err != nil {
				t.Fatalf("supported format %q rejected by compatibility check: %v", format, err)
			}
			out := filepath.Join(t.TempDir(), "trace."+format)
			if err := ExportExecutionTraceWithOptions(fixture.trace, format, out, fixture.opts); err != nil {
				t.Fatalf("supported format %q failed to export: %v", format, err)
			}
			info, err := os.Stat(out)
			if err != nil || info.Size() == 0 {
				t.Fatalf("supported format %q produced no output file", format)
			}
		})
	}
}

// TestPrinterGolden_NoOrphanGoldens fails when testdata/printer_golden
// contains files that no fixture/mode pair produces — e.g. leftovers from a
// renamed fixture, which would otherwise rot silently.
func TestPrinterGolden_NoOrphanGoldens(t *testing.T) {
	expected := map[string]bool{}
	for _, fixture := range goldenFixtures() {
		for _, mode := range goldenModeNames() {
			expected[fixture.name+"_"+mode+".golden"] = true
		}
	}

	entries, err := os.ReadDir(printerGoldenDir)
	if err != nil {
		t.Fatalf("read %s: %v — run 'go test ./internal/trace -run TestPrinterGolden -update' to generate goldens", printerGoldenDir, err)
	}
	for _, e := range entries {
		if !expected[e.Name()] {
			t.Errorf("orphan golden file %s/%s — delete it or restore its fixture", printerGoldenDir, e.Name())
		}
	}
	if len(entries) != len(expected) {
		t.Logf("golden files present: %d, expected: %d (missing ones are reported by TestPrinterGolden)", len(entries), len(expected))
	}
}
