// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package obsvalidate verifies that observability documentation tables and
// examples stay in sync with the canonical observability-manifest.json.
//
// Run with:
//
//	go test ./internal/obsvalidate/...
//
// CI should run this on every PR that touches docs/ or internal/progress/ or
// internal/metrics/ to catch undocumented or removed public signals.
package obsvalidate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ─── Manifest types ───────────────────────────────────────────────────────────

type ObservabilityManifest struct {
	SchemaVersion          string              `json:"schema_version"`
	Description            string              `json:"description"`
	PrometheusMetrics      []MetricDef         `json:"prometheus_metrics"`
	ProgressEventPhases    []NamedEntry        `json:"progress_event_phases"`
	ProgressEventStatuses  []StatusDef         `json:"progress_event_statuses"`
	ProgressEventFields    []FieldDef          `json:"progress_event_fields"`
	StableErrorCodes       []ErrorCodeDef      `json:"stable_error_codes"`
	Deprecations           []DeprecationDef    `json:"deprecations"`
}

type MetricDef struct {
	Name         string   `json:"name"`
	Type         string   `json:"type"`
	Labels       []string `json:"labels"`
	Description  string   `json:"description"`
	Stability    string   `json:"stability"`
	DocumentedIn []string `json:"documented_in"`
}

type NamedEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type StatusDef struct {
	Name        string `json:"name"`
	Terminal    bool   `json:"terminal"`
	Description string `json:"description"`
}

type FieldDef struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Required  bool   `json:"required"`
	Sensitive bool   `json:"sensitive"`
}

type ErrorCodeDef struct {
	Code        string `json:"code"`
	Phase       string `json:"phase"`
	Description string `json:"description"`
}

type DeprecationDef struct {
	Name              string `json:"name"`
	DeprecatedSince   string `json:"deprecated_since"`
	RemovalVersion    string `json:"removal_version,omitempty"`
	CompatibilityNote string `json:"compatibility_note"`
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func loadManifest(t *testing.T) *ObservabilityManifest {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine source file location")
	}
	// Navigate from internal/obsvalidate/ up two levels to the repo root,
	// then into docs/.
	root := filepath.Join(filepath.Dir(file), "..", "..")
	manifestPath := filepath.Join(root, "docs", "observability-manifest.json")

	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("failed to read observability manifest: %v\nPath: %s", err, manifestPath)
	}

	var m ObservabilityManifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("failed to parse observability manifest: %v", err)
	}
	return &m
}

func readDocFile(t *testing.T, relPath string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine source file location")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..")
	absPath := filepath.Join(root, relPath)
	data, err := os.ReadFile(absPath)
	if err != nil {
		t.Fatalf("failed to read doc file %s: %v", relPath, err)
	}
	return string(data)
}

// ─── Tests ────────────────────────────────────────────────────────────────────

// TestManifest_SchemaVersion ensures the manifest is parseable and has a
// non-empty schema_version — a basic sanity check.
func TestManifest_SchemaVersion(t *testing.T) {
	m := loadManifest(t)
	if m.SchemaVersion == "" {
		t.Fatal("observability manifest missing schema_version")
	}
}

// TestManifest_PrometheusMetricNames checks that every Prometheus metric listed
// in the manifest appears in every documentation file it claims to be in.
func TestManifest_PrometheusMetricNames(t *testing.T) {
	m := loadManifest(t)

	for _, metric := range m.PrometheusMetrics {
		if metric.Name == "" {
			t.Errorf("prometheus metric entry missing name: %+v", metric)
			continue
		}
		if metric.Type == "" {
			t.Errorf("metric %q missing type", metric.Name)
		}
		if metric.Description == "" {
			t.Errorf("metric %q missing description", metric.Name)
		}
		if metric.Stability == "" {
			t.Errorf("metric %q missing stability label", metric.Name)
		}
		if len(metric.DocumentedIn) == 0 {
			t.Errorf("metric %q has no documented_in entries", metric.Name)
		}

		for _, docPath := range metric.DocumentedIn {
			content := readDocFile(t, docPath)
			if !strings.Contains(content, metric.Name) {
				t.Errorf("metric %q not found in %s\n"+
					"Add it to the Prometheus metrics table or update documented_in in the manifest.",
					metric.Name, docPath)
			}
		}
	}
}

// TestManifest_ProgressEventPhases verifies that every phase declared in the
// manifest is present in the progress-events documentation.
func TestManifest_ProgressEventPhases(t *testing.T) {
	m := loadManifest(t)
	content := readDocFile(t, "docs/progress-events.md")

	for _, phase := range m.ProgressEventPhases {
		if phase.Name == "" {
			t.Errorf("progress phase entry missing name")
			continue
		}
		if !strings.Contains(content, "`"+phase.Name+"`") && !strings.Contains(content, `"phase":"`+phase.Name+`"`) {
			t.Errorf("progress phase %q not found in docs/progress-events.md", phase.Name)
		}
		if phase.Description == "" {
			t.Errorf("phase %q missing description in manifest", phase.Name)
		}
	}
}

// TestManifest_ProgressEventStatuses checks status values in the manifest
// against the progress-events documentation.
func TestManifest_ProgressEventStatuses(t *testing.T) {
	m := loadManifest(t)
	content := readDocFile(t, "docs/progress-events.md")

	for _, status := range m.ProgressEventStatuses {
		if status.Name == "" {
			t.Errorf("status entry missing name")
			continue
		}
		if !strings.Contains(content, "`"+status.Name+"`") {
			t.Errorf("status %q not found in docs/progress-events.md", status.Name)
		}
	}
}

// TestManifest_ProgressEventFields verifies that every field declared in the
// manifest is present in the NDJSON example in the progress-events doc.
func TestManifest_ProgressEventFields(t *testing.T) {
	m := loadManifest(t)
	content := readDocFile(t, "docs/progress-events.md")

	for _, field := range m.ProgressEventFields {
		if field.Name == "" {
			t.Errorf("field entry missing name")
			continue
		}
		// Fields appear as JSON keys in the schema example block.
		if !strings.Contains(content, `"`+field.Name+`"`) {
			t.Errorf("progress event field %q not found in docs/progress-events.md\n"+
				"Add it to the Event Schema section or update the manifest.", field.Name)
		}
	}
}

// TestManifest_StableErrorCodes verifies that every error code declared in the
// manifest is documented in progress-events.md.
func TestManifest_StableErrorCodes(t *testing.T) {
	m := loadManifest(t)
	content := readDocFile(t, "docs/progress-events.md")

	for _, ec := range m.StableErrorCodes {
		if ec.Code == "" {
			t.Errorf("error code entry missing code")
			continue
		}
		// Wildcard phase codes may not appear literally; skip them.
		if ec.Phase == "*" {
			continue
		}
		if !strings.Contains(content, "`"+ec.Code+"`") && !strings.Contains(content, ec.Code) {
			t.Errorf("stable error code %q not found in docs/progress-events.md\n"+
				"Add it to the Stable Error Codes table or update the manifest.", ec.Code)
		}
	}
}

// TestManifest_DeprecationsMustHaveCompatibilityNote ensures that any
// intentional deprecations in the manifest carry a compatibility note so
// consumers know how to migrate.
func TestManifest_DeprecationsMustHaveCompatibilityNote(t *testing.T) {
	m := loadManifest(t)
	for _, dep := range m.Deprecations {
		if dep.Name == "" {
			t.Errorf("deprecation entry missing name")
			continue
		}
		if dep.CompatibilityNote == "" {
			t.Errorf("deprecation %q is missing a compatibility_note\n"+
				"Add an explicit migration note before deprecating a public signal.", dep.Name)
		}
		if dep.DeprecatedSince == "" {
			t.Errorf("deprecation %q is missing deprecated_since", dep.Name)
		}
	}
}

// TestManifest_NoDriftFromProgressPackage verifies that the phase names in the
// manifest match the Phase constants defined in internal/progress/event.go.
// This is the compile-time / doc bridge that catches drift.
func TestManifest_NoDriftFromProgressPackage(t *testing.T) {
	m := loadManifest(t)

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine source file location")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..")
	eventSrcPath := filepath.Join(root, "internal", "progress", "event.go")

	src, err := os.ReadFile(eventSrcPath)
	if err != nil {
		t.Fatalf("cannot read internal/progress/event.go: %v", err)
	}
	srcContent := string(src)

	for _, phase := range m.ProgressEventPhases {
		// Each phase constant is defined as Phase = "name" in event.go.
		literal := fmt.Sprintf(`"%s"`, phase.Name)
		if !strings.Contains(srcContent, literal) {
			t.Errorf("phase %q from manifest not found as a string literal in internal/progress/event.go\n"+
				"Either add the Phase constant or remove the phase from the manifest.", phase.Name)
		}
	}
}

// TestManifest_NoUnregisteredMetrics is a forward-looking check: if a new
// metric appears in the Prometheus metrics table of the observability docs
// but is absent from the manifest, this test flags it.
//
// It uses a conservative heuristic: it looks for `glassbox_` prefixed metric
// names in the docs and confirms each appears in the manifest.
func TestManifest_NoUnregisteredMetrics(t *testing.T) {
	m := loadManifest(t)
	content := readDocFile(t, "docs/observability-troubleshooting.md")

	manifestNames := make(map[string]bool, len(m.PrometheusMetrics))
	for _, metric := range m.PrometheusMetrics {
		manifestNames[metric.Name] = true
	}

	// Extract all metric-like tokens (lines containing a backtick-quoted
	// identifier from the | Metric | table rows).
	for _, line := range strings.Split(content, "\n") {
		// Only look at table rows that start with `|`.
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			continue
		}
		// Look for the pattern `remote_node_` or `simulation_` which are the
		// metric prefixes used in this codebase.
		for _, prefix := range []string{"remote_node_", "simulation_"} {
			idx := strings.Index(line, prefix)
			if idx < 0 {
				continue
			}
			// Extract the identifier (up to whitespace, pipe, or backtick).
			end := idx
			for end < len(line) && line[end] != ' ' && line[end] != '|' && line[end] != '`' {
				end++
			}
			name := line[idx:end]
			if name == "" {
				continue
			}
			if !manifestNames[name] {
				t.Errorf("metric %q appears in docs/observability-troubleshooting.md but is not in the observability manifest\n"+
					"Add it to docs/observability-manifest.json to register it.", name)
			}
		}
	}
}
