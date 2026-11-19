// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Command dep-compat-report generates a human-readable compatibility report
// from a captured dep-compat output directory and committed golden baselines.
//
// This is the Go-layer counterpart to scripts/dep-compat-compare.sh and is
// used both directly by the CI workflow (via `go run`) and as a local
// debugging tool.
//
// Usage:
//
//	go run ./cmd/dep-compat-report [flags]
//
// Flags:
//
//	--captured-dir DIR  Directory containing captured JSON from dep-compat-capture.sh.
//	--golden-dir DIR    Golden baseline directory. Default: internal/depcompat/testdata/golden
//	--dep-group GROUP   Limit to one dep group. Default: all groups.
//	--output-json FILE  Write CompatReport JSON to FILE. Default: stdout.
//	--output-md FILE    Write Markdown summary to FILE. (Optional.)
//	--run-id ID         Run identifier for the report. Default: local-<timestamp>.
//	--update-golden     Overwrite goldens from the captured directory.
//	--verbose           Print field-level diff details to stderr.
//
// Exit codes:
//
//	0   No unexpected diffs.
//	1   Unexpected diffs detected, or errors.
//	2   Usage error.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/dotandev/glassbox/internal/depcompat"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("dep-compat-report", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		capturedDir  = fs.String("captured-dir", "", "Directory with captured JSON files (required).")
		goldenDir    = fs.String("golden-dir", "", "Golden baseline directory. Default: internal/depcompat/testdata/golden")
		depGroupStr  = fs.String("dep-group", "", "Limit to one dep group (stellar-sdk|soroban-host|crypto|rpc-client).")
		outputJSON   = fs.String("output-json", "", "Write JSON report to file (default: stdout).")
		outputMD     = fs.String("output-md", "", "Write Markdown summary to file.")
		runID        = fs.String("run-id", "", "Run identifier. Default: local-<timestamp>.")
		updateGolden = fs.Bool("update-golden", false, "Overwrite golden baselines from captured outputs.")
		verbose      = fs.Bool("verbose", false, "Print field-level diff details to stderr.")
	)

	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *capturedDir == "" {
		fmt.Fprintln(stderr, "error: --captured-dir is required")
		fs.Usage()
		return 2
	}

	// Resolve golden dir relative to the repo root (walk up from the binary).
	if *goldenDir == "" {
		repoRoot := findRepoRoot()
		*goldenDir = filepath.Join(repoRoot, "internal", "depcompat", "testdata", "golden")
	}

	if *runID == "" {
		*runID = "local-" + strconv.FormatInt(time.Now().Unix(), 10)
	}

	// Resolve dep group filter.
	var depGroup depcompat.DepGroup
	if *depGroupStr != "" {
		depGroup = depcompat.DepGroup(*depGroupStr)
		valid := false
		for _, g := range depcompat.AllDepGroups {
			if g == depGroup {
				valid = true
				break
			}
		}
		if !valid {
			fmt.Fprintf(stderr, "error: unknown dep-group %q. Valid: stellar-sdk, soroban-host, crypto, rpc-client\n", *depGroupStr)
			return 2
		}
	}

	// Determine which groups to process.
	groups := depcompat.AllDepGroups
	if depGroup != "" {
		groups = []depcompat.DepGroup{depGroup}
	}

	// Build the report.
	report := depcompat.NewCompatReport(*runID, depGroup)

	// Attempt to detect versions from the repo.
	repoRoot := findRepoRoot()
	vi := depcompat.DetectVersions(repoRoot)
	report.Versions = vi.ToDepVersions()

	hasUnexpected := false
	hasErrors := false

	for _, group := range groups {
		for _, kind := range depcompat.AllOutputKinds {
			goldenPath := filepath.Join(*goldenDir, depcompat.GoldenFileName(group, kind))
			actualPath := filepath.Join(*capturedDir, fmt.Sprintf("%s-%s.json", group, kind))

			result := depcompat.CompareFiles(group, kind, goldenPath, actualPath)
			report.AddResult(result)

			if *verbose {
				for _, d := range result.Diffs {
					fmt.Fprintf(stderr, "  [%s] %s/%s %s: %s → %s\n    %s\n",
						d.Class, group, kind, d.JSONPath,
						d.GoldenValue, d.ActualValue, d.Reason)
				}
			}

			if result.Error != "" {
				hasErrors = true
			}
			if result.Class == depcompat.DiffClassUnexpected {
				hasUnexpected = true
			}
		}
	}

	report.Finalize()

	// Write JSON report.
	jsonBytes, err := report.ToJSON()
	if err != nil {
		fmt.Fprintf(stderr, "error: marshal report JSON: %v\n", err)
		return 1
	}

	if *outputJSON == "" {
		fmt.Fprintf(stdout, "%s\n", jsonBytes)
	} else {
		if err := os.WriteFile(*outputJSON, jsonBytes, 0o644); err != nil { //nolint:gosec
			fmt.Fprintf(stderr, "error: write JSON report: %v\n", err)
			return 1
		}
		fmt.Fprintf(stderr, "JSON report written to: %s\n", *outputJSON)
	}

	// Write Markdown summary if requested.
	if *outputMD != "" {
		mdFile, err := os.Create(*outputMD) //nolint:gosec
		if err != nil {
			fmt.Fprintf(stderr, "error: create markdown file: %v\n", err)
			return 1
		}
		defer func() { _ = mdFile.Close() }()
		if err := depcompat.RenderMarkdown(report, mdFile); err != nil {
			fmt.Fprintf(stderr, "error: render markdown: %v\n", err)
			return 1
		}
		fmt.Fprintf(stderr, "Markdown summary written to: %s\n", *outputMD)
	}

	// Update goldens if requested.
	if *updateGolden {
		for _, group := range groups {
			for _, kind := range depcompat.AllOutputKinds {
				srcPath := filepath.Join(*capturedDir, fmt.Sprintf("%s-%s.json", group, kind))
				data, readErr := os.ReadFile(srcPath)
				if readErr != nil {
					fmt.Fprintf(stderr, "warn: update golden: cannot read %s: %v\n", srcPath, readErr)
					continue
				}
				if writeErr := depcompat.WriteGolden(*goldenDir, group, kind, data); writeErr != nil {
					fmt.Fprintf(stderr, "warn: update golden: %v\n", writeErr)
				} else {
					fmt.Fprintf(stderr, "updated golden: %s\n", depcompat.GoldenFileName(group, kind))
				}
			}
		}
		fmt.Fprintf(stderr, "\nGolden baselines updated. Review with: git diff %s\n", *goldenDir)
	}

	// Print summary to stderr.
	s := report.Summary
	fmt.Fprintf(stderr, "\n=== Compatibility Summary ===\n")
	fmt.Fprintf(stderr, "  Total   : %d\n", s.TotalOutputs)
	fmt.Fprintf(stderr, "  Matched : %d\n", s.OutputsMatched)
	fmt.Fprintf(stderr, "  Expected: %d\n", s.OutputsExpected)
	fmt.Fprintf(stderr, "  FAIL    : %d\n", s.OutputsUnexpected)
	fmt.Fprintf(stderr, "  Errors  : %d\n", s.OutputsErrored)

	if hasErrors {
		fmt.Fprintln(stderr, "\nErrors detected — check captured output files.")
		return 1
	}
	if hasUnexpected {
		fmt.Fprintln(stderr, "\nUnexpected diffs detected — review the report and update goldens if intentional.")
		return 1
	}
	fmt.Fprintln(stderr, "\nAll compatibility checks passed.")
	return 0
}

// findRepoRoot walks up the directory tree looking for go.mod to find the
// module root. Falls back to the current directory if not found.
func findRepoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "."
}
