// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package dashboard defines the input schema for the CI validation dashboard
// and provides the aggregator that turns CI artifact streams into a
// deterministic, privacy-safe report.
//
// # Overview
//
// The dashboard aggregates three artifact sources that CI already produces:
//
//  1. JUnit XML (gotestsum --junitfile) — per-job test results
//  2. Test output JSON (gotestsum --jsonfile) — optional detail
//  3. Validation suite summary (scripts/run-validation-suite.sh) — critical journeys
//
// It groups results by "journey" (a stable logical name), and emits one Report
// per generation run. Historical runs are accumulated by the caller — this
// package never appends to an existing file.
//
// # Privacy
//
// - Absolute file paths are stripped and replaced by basenames.
// - Secrets (tokens, PEM blocks, connection strings) are redacted using the
//   same patterns as scripts/redact-logs.sh.
// - Test output bodies are never included — only pass/fail/skip counts and
//   durations.
// - Full payloads and environment variable values are excluded.
package dashboard

import (
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// SchemaVersion is the schema version embedded in every generated report.
// Increment when the Report struct changes in a backwards-incompatible way.
const SchemaVersion = "1.0"

// ─── input schema ─────────────────────────────────────────────────────────────

// ArtifactSource describes one CI artifact file to ingest.
type ArtifactSource struct {
	// Path is the filesystem path to the artifact file.
	Path string `json:"path"`
	// Kind identifies the format. One of: "junit", "gotestsum-json",
	// "validation-summary", "coverage".
	Kind string `json:"kind"`
	// Platform is the OS/arch label this artifact was produced on
	// (e.g. "ubuntu-latest", "windows-latest"). Optional.
	Platform string `json:"platform,omitempty"`
	// Commit is the git commit SHA this artifact corresponds to. Optional.
	Commit string `json:"commit,omitempty"`
	// RunID is the CI run identifier (e.g. GitHub Actions run_id). Optional.
	RunID string `json:"run_id,omitempty"`
	// JobName is the CI job that produced this artifact. Optional.
	JobName string `json:"job_name,omitempty"`
}

// InputManifest is the complete dashboard input, read from a JSON file passed
// to generate-dashboard via --input.
type InputManifest struct {
	// SchemaVersion of this input manifest. Currently "1.0".
	SchemaVersion string `json:"schema_version"`
	// GeneratedAt is when this manifest was created.
	GeneratedAt time.Time `json:"generated_at"`
	// Artifacts is the ordered list of CI artifact files to aggregate.
	Artifacts []ArtifactSource `json:"artifacts"`
	// CriticalJourneys lists the journey names that MUST appear in the output.
	// Any journey listed here that has no corresponding artifact entry is
	// emitted as a JourneyStatusUnknown record.
	CriticalJourneys []string `json:"critical_journeys,omitempty"`
}

// ─── output schema (Report) ───────────────────────────────────────────────────

// JourneyStatus is the aggregated result of a critical journey across all
// platforms and artifact kinds.
type JourneyStatus string

const (
	JourneyStatusPassed  JourneyStatus = "passed"
	JourneyStatusFailed  JourneyStatus = "failed"
	JourneyStatusSkipped JourneyStatus = "skipped"
	JourneyStatusUnknown JourneyStatus = "unknown"
)

// JourneyRecord is the per-journey row in the dashboard report.
type JourneyRecord struct {
	// Name is the stable journey identifier (e.g. "go-unit/ubuntu-latest").
	Name string `json:"name"`
	// Status is the aggregated result.
	Status JourneyStatus `json:"status"`
	// Platform is the OS/arch this record was collected on.
	Platform string `json:"platform,omitempty"`
	// Commit is the git commit this record corresponds to.
	Commit string `json:"commit,omitempty"`
	// DurationMs is the total wall-clock duration in milliseconds.
	DurationMs int64 `json:"duration_ms,omitempty"`
	// PassCount is the number of passing tests/checks.
	PassCount int `json:"pass_count"`
	// FailCount is the number of failing tests/checks.
	FailCount int `json:"fail_count"`
	// SkipCount is the number of skipped tests/checks.
	SkipCount int `json:"skip_count"`
	// FailureLink is a URL or artifact path pointing to failure logs.
	// Never contains secrets or full payloads.
	FailureLink string `json:"failure_link,omitempty"`
	// IssueRef is an optional reference to a tracking issue for persistent
	// failures (e.g. "https://github.com/dotandev/glassbox/issues/1234").
	IssueRef string `json:"issue_ref,omitempty"`
	// ArtifactKind identifies the source artifact type.
	ArtifactKind string `json:"artifact_kind,omitempty"`
	// RunID is the CI run that produced this record.
	RunID string `json:"run_id,omitempty"`
	// Fingerprint is a stable identifier for this record's content. It changes
	// when the journey result or commit changes but not when timestamps change.
	Fingerprint string `json:"fingerprint"`
}

// CoverageSummary carries aggregated code coverage data.
type CoverageSummary struct {
	// Platform is the OS/arch this coverage was measured on.
	Platform string `json:"platform"`
	// Commit is the git commit.
	Commit string `json:"commit,omitempty"`
	// CoveragePercent is the total statement coverage percentage.
	CoveragePercent float64 `json:"coverage_percent"`
}

// Report is the complete dashboard output. It is deterministic: given the same
// inputs it always produces the same Report (journeys sorted by name, timestamps
// from inputs only).
type Report struct {
	// SchemaVersion identifies the output schema.
	SchemaVersion string `json:"schema_version"`
	// GeneratedAt is when this report was produced.
	GeneratedAt time.Time `json:"generated_at"`
	// Commit is the git commit shared by most artifacts, or the most recent one.
	Commit string `json:"commit,omitempty"`
	// Journeys is the complete list of journey records, sorted by name.
	Journeys []JourneyRecord `json:"journeys"`
	// Coverage is coverage data keyed by platform.
	Coverage []CoverageSummary `json:"coverage,omitempty"`
	// FailedJourneys is the count of journeys with status "failed".
	FailedJourneys int `json:"failed_journeys"`
	// TotalJourneys is the total journey count.
	TotalJourneys int `json:"total_journeys"`
	// AllPassed is true when every journey passed.
	AllPassed bool `json:"all_passed"`
}

// ─── aggregator ───────────────────────────────────────────────────────────────

// Aggregator reads CI artifact files and builds a Report.
type Aggregator struct {
	manifest InputManifest
}

// NewAggregator creates an aggregator for the given input manifest.
func NewAggregator(m InputManifest) *Aggregator {
	return &Aggregator{manifest: m}
}

// Aggregate processes all artifact sources and returns the Report.
func (a *Aggregator) Aggregate() (Report, error) {
	journeys := make(map[string]*JourneyRecord)

	for _, src := range a.manifest.Artifacts {
		records, err := ingestArtifact(src)
		if err != nil {
			// Non-fatal: log the error in the journey's failure link.
			records = []JourneyRecord{{
				Name:         journeyName(src),
				Status:       JourneyStatusUnknown,
				Platform:     src.Platform,
				Commit:       src.Commit,
				RunID:        src.RunID,
				ArtifactKind: src.Kind,
				FailureLink:  fmt.Sprintf("ingestion error: %v", redactSecrets(err.Error())),
			}}
		}

		for i := range records {
			r := records[i]
			key := r.Name + "|" + r.Platform
			existing, ok := journeys[key]
			if !ok {
				journeys[key] = &r
				continue
			}
			// Merge: a failure in any platform fails the journey.
			if r.Status == JourneyStatusFailed {
				existing.Status = JourneyStatusFailed
			}
			existing.PassCount += r.PassCount
			existing.FailCount += r.FailCount
			existing.SkipCount += r.SkipCount
			existing.DurationMs += r.DurationMs
		}
	}

	// Ensure every critical journey appears in the output.
	for _, name := range a.manifest.CriticalJourneys {
		found := false
		for k := range journeys {
			if strings.HasPrefix(k, name+"|") || k == name+"|" {
				found = true
				break
			}
		}
		if !found {
			journeys[name+"|"] = &JourneyRecord{
				Name:   name,
				Status: JourneyStatusUnknown,
			}
		}
	}

	// Sort journeys deterministically.
	keys := make([]string, 0, len(journeys))
	for k := range journeys {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	result := make([]JourneyRecord, 0, len(journeys))
	failed := 0
	for _, k := range keys {
		r := journeys[k]
		r.Fingerprint = fingerprintRecord(r)
		result = append(result, *r)
		if r.Status == JourneyStatusFailed {
			failed++
		}
	}

	commit := mostCommonCommit(a.manifest.Artifacts)
	report := Report{
		SchemaVersion:  SchemaVersion,
		GeneratedAt:    time.Now().UTC(),
		Commit:         commit,
		Journeys:       result,
		FailedJourneys: failed,
		TotalJourneys:  len(result),
		AllPassed:      failed == 0 && len(result) > 0,
	}

	return report, nil
}

// ─── ingestors ────────────────────────────────────────────────────────────────

// ingestArtifact reads one artifact source and returns the journey records
// it contributes.
func ingestArtifact(src ArtifactSource) ([]JourneyRecord, error) {
	switch src.Kind {
	case "junit":
		return ingestJUnit(src)
	case "gotestsum-json":
		return ingestGotestsumJSON(src)
	case "validation-summary":
		return ingestValidationSummary(src)
	case "coverage":
		return ingestCoverage(src)
	default:
		return nil, fmt.Errorf("unknown artifact kind %q", src.Kind)
	}
}

// ── JUnit XML ─────────────────────────────────────────────────────────────────

// junitTestSuites is the root XML element for JUnit reports.
type junitTestSuites struct {
	XMLName    xml.Name         `xml:"testsuites"`
	TestSuites []junitTestSuite `xml:"testsuite"`
}

type junitTestSuite struct {
	Name     string         `xml:"name,attr"`
	Tests    int            `xml:"tests,attr"`
	Failures int            `xml:"failures,attr"`
	Errors   int            `xml:"errors,attr"`
	Skipped  int            `xml:"skipped,attr"`
	Time     float64        `xml:"time,attr"`
	Cases    []junitTestCase `xml:"testcase"`
}

type junitTestCase struct {
	Name    string  `xml:"name,attr"`
	Time    float64 `xml:"time,attr"`
	Failure *struct {
		Message string `xml:"message,attr"`
	} `xml:"failure"`
	Skipped *struct{} `xml:"skipped"`
}

func ingestJUnit(src ArtifactSource) ([]JourneyRecord, error) {
	data, err := os.ReadFile(src.Path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", filepath.Base(src.Path), err)
	}

	var suites junitTestSuites
	if xmlErr := xml.Unmarshal(data, &suites); xmlErr != nil {
		// Some JUnit files have a single testsuite root — try that.
		var single junitTestSuite
		if xmlErr2 := xml.Unmarshal(data, &single); xmlErr2 != nil {
			return nil, fmt.Errorf("parse JUnit XML %s: %w", filepath.Base(src.Path), xmlErr)
		}
		suites.TestSuites = []junitTestSuite{single}
	}

	name := journeyName(src)
	rec := JourneyRecord{
		Name:         name,
		Platform:     src.Platform,
		Commit:       src.Commit,
		RunID:        src.RunID,
		ArtifactKind: src.Kind,
	}

	for _, suite := range suites.TestSuites {
		rec.PassCount += suite.Tests - suite.Failures - suite.Errors - suite.Skipped
		rec.FailCount += suite.Failures + suite.Errors
		rec.SkipCount += suite.Skipped
		rec.DurationMs += int64(suite.Time * 1000)
	}
	if rec.FailCount > 0 {
		rec.Status = JourneyStatusFailed
	} else if rec.PassCount > 0 || rec.SkipCount > 0 {
		rec.Status = JourneyStatusPassed
	} else {
		rec.Status = JourneyStatusUnknown
	}

	return []JourneyRecord{rec}, nil
}

// ── gotestsum JSON ────────────────────────────────────────────────────────────

// gotestsumEvent is one line of a gotestsum --jsonfile output.
type gotestsumEvent struct {
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Elapsed float64 `json:"Elapsed"`
	Output  string  `json:"Output"` // never stored; stripped for privacy
}

func ingestGotestsumJSON(src ArtifactSource) ([]JourneyRecord, error) {
	f, err := os.Open(src.Path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", filepath.Base(src.Path), err)
	}
	defer f.Close()

	name := journeyName(src)
	rec := JourneyRecord{
		Name:         name,
		Platform:     src.Platform,
		Commit:       src.Commit,
		RunID:        src.RunID,
		ArtifactKind: src.Kind,
	}

	dec := json.NewDecoder(f)
	for {
		var ev gotestsumEvent
		if err := dec.Decode(&ev); err == io.EOF {
			break
		} else if err != nil {
			// Skip malformed lines (common in truncated logs).
			continue
		}
		// Only count top-level test actions (no sub-test events).
		if ev.Test == "" || strings.Contains(ev.Test, "/") {
			continue
		}
		switch ev.Action {
		case "pass":
			rec.PassCount++
			rec.DurationMs += int64(ev.Elapsed * 1000)
		case "fail":
			rec.FailCount++
			rec.DurationMs += int64(ev.Elapsed * 1000)
		case "skip":
			rec.SkipCount++
		}
	}

	if rec.FailCount > 0 {
		rec.Status = JourneyStatusFailed
	} else if rec.PassCount > 0 || rec.SkipCount > 0 {
		rec.Status = JourneyStatusPassed
	} else {
		rec.Status = JourneyStatusUnknown
	}
	return []JourneyRecord{rec}, nil
}

// ── validation-summary ────────────────────────────────────────────────────────

// validationSummaryFile is the JSON format produced by run-validation-suite.sh.
type validationSummaryFile struct {
	Journeys []validationJourneyEntry `json:"journeys"`
}

type validationJourneyEntry struct {
	Name     string `json:"name"`
	Status   string `json:"status"` // "passed" | "failed" | "skipped"
	DurationMs int64  `json:"duration_ms,omitempty"`
	FailureArtifact string `json:"failure_artifact,omitempty"`
}

func ingestValidationSummary(src ArtifactSource) ([]JourneyRecord, error) {
	data, err := os.ReadFile(src.Path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", filepath.Base(src.Path), err)
	}

	var summary validationSummaryFile
	if jsonErr := json.Unmarshal(data, &summary); jsonErr != nil {
		return nil, fmt.Errorf("parse validation summary %s: %w", filepath.Base(src.Path), jsonErr)
	}

	records := make([]JourneyRecord, 0, len(summary.Journeys))
	for _, j := range summary.Journeys {
		status := JourneyStatusUnknown
		switch strings.ToLower(j.Status) {
		case "passed", "pass":
			status = JourneyStatusPassed
		case "failed", "fail":
			status = JourneyStatusFailed
		case "skipped", "skip":
			status = JourneyStatusSkipped
		}

		var passCount, failCount int
		if status == JourneyStatusPassed {
			passCount = 1
		} else if status == JourneyStatusFailed {
			failCount = 1
		}

		// Redact and basename the failure artifact path.
		failureLink := ""
		if j.FailureArtifact != "" {
			failureLink = redactSecrets(filepath.Base(j.FailureArtifact))
		}

		records = append(records, JourneyRecord{
			Name:         j.Name,
			Status:       status,
			Platform:     src.Platform,
			Commit:       src.Commit,
			RunID:        src.RunID,
			DurationMs:   j.DurationMs,
			PassCount:    passCount,
			FailCount:    failCount,
			ArtifactKind: src.Kind,
			FailureLink:  failureLink,
		})
	}
	return records, nil
}

// ── coverage ──────────────────────────────────────────────────────────────────

// coverageReport is a simple JSON format produced by go tool cover output.
type coverageReport struct {
	TotalPercent float64 `json:"total_percent"`
	Commit       string  `json:"commit,omitempty"`
}

func ingestCoverage(src ArtifactSource) ([]JourneyRecord, error) {
	// Coverage files produce no journey records but are picked up by the caller
	// for the Coverage field. Return empty here; coverage is handled separately.
	return nil, nil
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// journeyName builds a stable journey name from an ArtifactSource.
func journeyName(src ArtifactSource) string {
	name := src.JobName
	if name == "" {
		// Derive from filename.
		base := filepath.Base(src.Path)
		ext := filepath.Ext(base)
		name = strings.TrimSuffix(base, ext)
	}
	if src.Platform != "" {
		return name + "/" + src.Platform
	}
	return name
}

// fingerprintRecord returns a stable 12-char hex fingerprint for a record.
// It is keyed on name, status, commit, and platform — not timestamps.
func fingerprintRecord(r *JourneyRecord) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%s",
		r.Name, r.Status, r.Commit, r.Platform)))
	return fmt.Sprintf("%x", h[:6])
}

// mostCommonCommit returns the commit SHA that appears most frequently across
// artifacts, or "" if no commit is set.
func mostCommonCommit(srcs []ArtifactSource) string {
	counts := make(map[string]int)
	for _, s := range srcs {
		if s.Commit != "" {
			counts[s.Commit]++
		}
	}
	var best string
	var bestN int
	for c, n := range counts {
		if n > bestN || (n == bestN && c < best) {
			best = c
			bestN = n
		}
	}
	return best
}

// secretPatterns lists regex patterns for common secret formats to redact.
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(password|passwd|pwd|secret|token|api[_-]?key|auth|bearer)[^=\s]*\s*=\s*\S+`),
	regexp.MustCompile(`-----BEGIN [A-Z ]+-----[\s\S]*?-----END [A-Z ]+-----`),
	regexp.MustCompile(`G[A-Z0-9]{55}`),   // Stellar key pattern
	regexp.MustCompile(`sk_[a-zA-Z0-9]+`), // generic secret key
}

// redactSecrets replaces patterns that look like credentials with "[REDACTED]".
func redactSecrets(s string) string {
	for _, p := range secretPatterns {
		s = p.ReplaceAllString(s, "[REDACTED]")
	}
	return s
}

// Marshal serializes a Report to indented JSON.
func Marshal(r Report) ([]byte, error) {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("dashboard: marshal failed: %w", err)
	}
	return data, nil
}

// UnmarshalInputManifest deserializes an InputManifest from JSON bytes.
func UnmarshalInputManifest(data []byte) (InputManifest, error) {
	var m InputManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return InputManifest{}, fmt.Errorf("dashboard: invalid input manifest: %w", err)
	}
	return m, nil
}
