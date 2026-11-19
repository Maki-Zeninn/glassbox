// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// generate-dashboard aggregates CI artifact streams produced by the Glassbox
// CI workflows into a deterministic, privacy-safe validation dashboard report.
//
// # Usage
//
//	generate-dashboard [flags]
//	  --input    <path>   Input manifest JSON (required)
//	  --output   <path>   Output report JSON (default: dashboard.json)
//	  --format   text|json  Output format (default: json)
//	  --offline           Allow missing artifact files (emit unknown status)
//
// # Input manifest
//
// The tool reads an InputManifest JSON file that lists the CI artifact files
// to aggregate. See docs/ci-dashboard.md for the full schema.
//
// # Privacy
//
// The generated report never includes:
//   - Secrets, tokens, PEM keys, or connection strings
//   - Full absolute paths (basenames only)
//   - Full test output bodies
//   - Environment variable values
//
// # Determinism
//
// Given identical input artifacts the tool always produces the same output.
// Journey records are sorted by name; fingerprints are keyed on stable fields.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/dotandev/glassbox/internal/dashboard"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

type config struct {
	inputPath   string
	outputPath  string
	format      string
	offline     bool
	exitOnFail  bool
}

func run(args []string) error {
	fs := flag.NewFlagSet("generate-dashboard", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var cfg config
	fs.StringVar(&cfg.inputPath, "input", "", "Input manifest JSON file (required)")
	fs.StringVar(&cfg.outputPath, "output", "dashboard.json", "Output report file path")
	fs.StringVar(&cfg.format, "format", "json", "Output format: json or text")
	fs.BoolVar(&cfg.offline, "offline", false, "Allow missing artifact files (emit unknown status)")
	fs.BoolVar(&cfg.exitOnFail, "exit-on-fail", false, "Exit with code 1 when any journey failed")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if cfg.inputPath == "" {
		fs.Usage()
		return fmt.Errorf("--input is required")
	}

	switch strings.ToLower(cfg.format) {
	case "json", "text":
	default:
		return fmt.Errorf("--format must be 'json' or 'text', got %q", cfg.format)
	}

	// Load the input manifest.
	manifestData, err := os.ReadFile(cfg.inputPath)
	if err != nil {
		return fmt.Errorf("read input manifest %q: %w", cfg.inputPath, err)
	}
	m, err := dashboard.UnmarshalInputManifest(manifestData)
	if err != nil {
		return err
	}

	// Validate artifact sources when not in offline mode.
	if !cfg.offline {
		for _, src := range m.Artifacts {
			if _, statErr := os.Stat(src.Path); os.IsNotExist(statErr) {
				return fmt.Errorf("artifact file not found: %q\n"+
					"  Use --offline to allow missing artifacts (they will be emitted as 'unknown')",
					src.Path)
			}
		}
	}

	// Run the aggregator.
	agg := dashboard.NewAggregator(m)
	report, err := agg.Aggregate()
	if err != nil {
		return fmt.Errorf("aggregation failed: %w", err)
	}

	// Write the output.
	switch strings.ToLower(cfg.format) {
	case "text":
		writeTextReport(os.Stdout, report)
	default: // json
		data, marshalErr := dashboard.Marshal(report)
		if marshalErr != nil {
			return marshalErr
		}
		if cfg.outputPath == "-" {
			fmt.Println(string(data))
		} else {
			if writeErr := os.WriteFile(cfg.outputPath, data, 0o644); writeErr != nil {
				return fmt.Errorf("write report to %q: %w", cfg.outputPath, writeErr)
			}
			fmt.Fprintf(os.Stderr, "Dashboard report written to: %s\n", cfg.outputPath)
			fmt.Fprintf(os.Stderr, "  Journeys: %d total, %d failed\n",
				report.TotalJourneys, report.FailedJourneys)
			if report.AllPassed {
				fmt.Fprintln(os.Stderr, "  Status: ALL PASSED")
			} else if report.FailedJourneys > 0 {
				fmt.Fprintf(os.Stderr, "  Status: %d FAILED\n", report.FailedJourneys)
			}
		}
	}

	if cfg.exitOnFail && !report.AllPassed && report.FailedJourneys > 0 {
		os.Exit(1)
	}

	return nil
}

func writeTextReport(out *os.File, r dashboard.Report) {
	fmt.Fprintln(out, "CI Validation Dashboard")
	fmt.Fprintln(out, "═══════════════════════════════════════════════════════════")
	fmt.Fprintf(out, "  Generated : %s\n", r.GeneratedAt.Format(time.RFC3339))
	if r.Commit != "" {
		fmt.Fprintf(out, "  Commit    : %s\n", r.Commit)
	}
	fmt.Fprintf(out, "  Journeys  : %d total, %d failed\n", r.TotalJourneys, r.FailedJourneys)
	statusStr := "ALL PASSED"
	if !r.AllPassed {
		if r.FailedJourneys > 0 {
			statusStr = fmt.Sprintf("%d FAILED", r.FailedJourneys)
		} else {
			statusStr = "INCOMPLETE"
		}
	}
	fmt.Fprintf(out, "  Status    : %s\n\n", statusStr)

	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "JOURNEY\tPLATFORM\tSTATUS\tPASS\tFAIL\tSKIP\tDURATION\tCOMMIT")
	fmt.Fprintln(tw, "───────\t────────\t──────\t────\t────\t────\t────────\t──────")
	for _, j := range r.Journeys {
		dur := ""
		if j.DurationMs > 0 {
			dur = fmt.Sprintf("%dms", j.DurationMs)
		}
		commit := ""
		if len(j.Commit) >= 8 {
			commit = j.Commit[:8]
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%d\t%s\t%s\n",
			j.Name, j.Platform, strings.ToUpper(string(j.Status)),
			j.PassCount, j.FailCount, j.SkipCount, dur, commit)
		if j.Status == dashboard.JourneyStatusFailed && j.FailureLink != "" {
			fmt.Fprintf(tw, "  → failure: %s\t\t\t\t\t\t\t\n", j.FailureLink)
		}
	}
	_ = tw.Flush()
}

// generateInputManifest is a helper invoked when --generate-input is passed.
// It scans a directory for CI artifacts and writes an InputManifest template.
func generateInputManifest(artifactDir, platform, commit, runID string) dashboard.InputManifest {
	return dashboard.InputManifest{
		SchemaVersion: "1.0",
		GeneratedAt:   time.Now().UTC(),
		Artifacts:     discoverArtifacts(artifactDir, platform, commit, runID),
		CriticalJourneys: []string{
			"go-unit",
			"go-integration",
			"ts-test",
			"validation-suite",
		},
	}
}

// discoverArtifacts scans a directory tree and classifies artifact files.
func discoverArtifacts(dir, platform, commit, runID string) []dashboard.ArtifactSource {
	var sources []dashboard.ArtifactSource
	_ = walkDir(dir, func(path string) {
		base := strings.ToLower(filepath.Base(path))
		var kind string
		switch {
		case strings.HasSuffix(base, ".xml") && strings.Contains(base, "junit"):
			kind = "junit"
		case base == "junit.xml":
			kind = "junit"
		case base == "test-output.json":
			kind = "gotestsum-json"
		case strings.Contains(base, "validation") && strings.HasSuffix(base, ".json"):
			kind = "validation-summary"
		case strings.Contains(base, "coverage") && strings.HasSuffix(base, ".out"):
			kind = "coverage"
		default:
			return
		}
		sources = append(sources, dashboard.ArtifactSource{
			Path:     path,
			Kind:     kind,
			Platform: platform,
			Commit:   commit,
			RunID:    runID,
		})
	})
	return sources
}

func walkDir(dir string, fn func(string)) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		full := filepath.Join(dir, e.Name())
		if e.IsDir() {
			_ = walkDir(full, fn)
		} else {
			fn(full)
		}
	}
	return nil
}

// GenerateInputJSON serializes an InputManifest to JSON bytes (used in tests).
func GenerateInputJSON(m dashboard.InputManifest) ([]byte, error) {
	return json.MarshalIndent(m, "", "  ")
}
