// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TraceFormatVersion represents the version of the trace export format
type TraceFormatVersion struct {
	Major int `json:"major"`
	Minor int `json:"minor"`
	Patch int `json:"patch"`
}

// CurrentFormatVersion is the current trace export format version
var CurrentFormatVersion = TraceFormatVersion{Major: 1, Minor: 0, Patch: 0}

// CurrentJSONSchemaVersion is the schema_version string embedded in the
// ExportJSON (--output-json) envelope. It is defined here rather than as a
// literal at each call site so that all callers stay in sync automatically
// when the schema evolves.
//
// Format: "MAJOR.MINOR" — the patch component is omitted because patch
// changes are documentation-only and do not affect the envelope structure.
const CurrentJSONSchemaVersion = "1.0"

// SupportedJSONSchemaVersions lists all schema_version values that this
// binary can load from an ExportJSON envelope without error. When a new
// minor version is introduced it should be appended here so older files
// continue to load with a deprecation warning rather than a hard failure.
var SupportedJSONSchemaVersions = []string{"1.0"}

// IsJSONSchemaVersionSupported reports whether the given schema_version
// string from an ExportJSON envelope is loadable by this binary.
func IsJSONSchemaVersionSupported(v string) bool {
	for _, s := range SupportedJSONSchemaVersions {
		if s == v {
			return true
		}
	}
	return false
}

// String returns the version as a string (e.g., "1.0.0")
func (v TraceFormatVersion) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// IsCompatibleWith checks if this version is compatible with another version
func (v TraceFormatVersion) IsCompatibleWith(other TraceFormatVersion) bool {
	// Major version must match for compatibility
	if v.Major != other.Major {
		return false
	}
	// Minor version backward compatibility: newer can read older
	return v.Minor >= other.Minor
}

// VersionedTrace wraps an ExecutionTrace with version information for compatibility
type VersionedTrace struct {
	Version TraceFormatVersion `json:"version"`
	Trace   *ExecutionTrace    `json:"trace"`
}

// CompatibilityOptions controls backward/forward compatibility behavior
type CompatibilityOptions struct {
	// StrictVersionCheck requires exact version match
	StrictVersionCheck bool
	
	// AllowNewerMinor allows loading traces from newer minor versions
	AllowNewerMinor bool
	
	// AllowDowngrade allows exporting to older format versions (lossy)
	AllowDowngrade bool
	
	// PreserveLegacyFields keeps deprecated fields during export
	PreserveLegacyFields bool
}

// DefaultCompatibilityOptions returns sensible compatibility defaults
func DefaultCompatibilityOptions() CompatibilityOptions {
	return CompatibilityOptions{
		StrictVersionCheck:   false,
		AllowNewerMinor:      true,
		AllowDowngrade:       false,
		PreserveLegacyFields: true,
	}
}

// ExportVersionedTrace exports a trace with version information
func ExportVersionedTrace(trace *ExecutionTrace, format, outputPath string, opts ExportOptions, compatOpts CompatibilityOptions) error {
	if err := ValidateTraceExportParams(trace, format, outputPath, opts); err != nil {
		return fmt.Errorf("pre-export validation failed: %w", err)
	}

	if err := ValidateTraceFormatCompatibility(trace, format); err != nil {
		return fmt.Errorf("format compatibility check failed: %w", err)
	}

	versioned := VersionedTrace{
		Version: CurrentFormatVersion,
		Trace:   trace,
	}

	if strings.ToLower(format) == "json" {
		data, err := json.MarshalIndent(versioned, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal versioned trace: %w", err)
		}

		if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
			return fmt.Errorf("failed to create output directory: %w\n"+
				"  Path: %s\n"+
				"  Fix: ensure the parent directory exists and is writable",
				err, filepath.Dir(outputPath))
		}

		if err := os.WriteFile(outputPath, data, 0o644); err != nil {
			return fmt.Errorf("failed to write versioned trace file: %w\n"+
				"  Path: %s\n"+
				"  Fix: check disk space and file permissions",
				err, outputPath)
		}

		return nil
	}

	return ExportExecutionTraceWithOptions(trace, format, outputPath, opts)
}

// LoadVersionedTrace loads a trace with version compatibility checking.
// It understands two JSON envelope shapes:
//
//  1. The VersionedTrace envelope written by ExportVersionedTrace / ExportWithCompatibility:
//     {"version":{"major":1,"minor":0,"patch":0},"trace":{...}}
//
//  2. The ExportJSON envelope written by --output-json:
//     {"schema_version":"1.0","generated_at":"...","trace":{...}}
//
// Both shapes carry the trace payload at the "trace" key. Legacy files that
// are plain ExecutionTrace JSON (no envelope at all) are also accepted with a
// deprecation warning.
func LoadVersionedTrace(path string, compatOpts CompatibilityOptions) (*ExecutionTrace, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read trace file: %w\n"+
			"  Path: %s\n"+
			"  Fix: ensure the file exists and you have read permissions", err, path)
	}

	// Probe top-level keys to detect the envelope shape before full parsing.
	var probe struct {
		// VersionedTrace shape
		Version *TraceFormatVersion `json:"version"`
		// ExportJSON shape
		SchemaVersion string `json:"schema_version"`
		// Both shapes share this key
		Trace *ExecutionTrace `json:"trace"`
	}
	probeErr := json.Unmarshal(data, &probe)

	// ── ExportJSON envelope ────────────────────────────────────────────────
	// Detect by presence of "schema_version" string key (not the semver object).
	if probeErr == nil && probe.SchemaVersion != "" && probe.Trace != nil {
		if !IsJSONSchemaVersionSupported(probe.SchemaVersion) {
			return nil, fmt.Errorf(
				"unsupported schema_version %q in trace file %q\n"+
					"  This binary supports schema versions: %s\n"+
					"  Fix: re-export the trace with the current CLI version, or upgrade Glassbox",
				probe.SchemaVersion, path,
				joinVersions(SupportedJSONSchemaVersions),
			)
		}
		// Validate the schema version string itself (format, numeric components).
		if err := ValidateJSONSchemaVersion(probe.SchemaVersion); err != nil {
			return nil, fmt.Errorf(
				"malformed schema_version %q in trace file %q: %w\n"+
					"  Fix: re-export the trace with the current CLI — schema_version must be \"MAJOR.MINOR\"",
				probe.SchemaVersion, path, err)
		}
		if probe.SchemaVersion != CurrentJSONSchemaVersion {
			fmt.Fprintf(os.Stderr,
				"Warning: trace file %q uses schema_version %q; current is %q\n"+
					"  Consider re-exporting with the current CLI for full compatibility\n",
				path, probe.SchemaVersion, CurrentJSONSchemaVersion,
			)
			// For schema version migration, we would need to reload the full envelope
			// and apply MigrateTrace. For now, we load the trace directly since
			// the structure is compatible between 1.0 and 2.0 schema versions.
		}
		return probe.Trace, nil
	}

	// ── VersionedTrace envelope ────────────────────────────────────────────
	var versioned VersionedTrace
	if probeErr == nil && probe.Version != nil && probe.Trace != nil {
		versioned.Version = *probe.Version
		versioned.Trace = probe.Trace
	} else if probeErr != nil || probe.Trace == nil {
		// Not a recognised envelope — try legacy plain ExecutionTrace.
		var plain ExecutionTrace
		if err2 := json.Unmarshal(data, &plain); err2 != nil {
			return nil, fmt.Errorf("failed to parse trace file (tried versioned, schema, and legacy formats): %w\n"+
				"  This may be a corrupted file or an unsupported format\n"+
				"  Fix: verify the file is valid JSON with 'jq . %s'", probeErr, path)
		}
		fmt.Fprintf(os.Stderr, "Warning: loaded legacy trace format (no version info)\n")
		fmt.Fprintf(os.Stderr, "  Consider re-exporting with current version for full compatibility\n")
		return &plain, nil
	}

	// ── Semver compatibility check ─────────────────────────────────────────
	if compatOpts.StrictVersionCheck {
		if versioned.Version != CurrentFormatVersion {
			return nil, fmt.Errorf("version mismatch: trace is version %s but CLI requires exactly version %s\n"+
				"  Fix: export trace with compatible CLI version or set StrictVersionCheck=false",
				versioned.Version.String(), CurrentFormatVersion.String())
		}
	} else {
		if !CurrentFormatVersion.IsCompatibleWith(versioned.Version) {
			if versioned.Version.Major != CurrentFormatVersion.Major {
				return nil, fmt.Errorf("incompatible major version: trace is %s but CLI is %s\n"+
					"  Major version differences are not compatible\n"+
					"  Fix: use a CLI version with matching major version or export trace with current CLI",
					versioned.Version.String(), CurrentFormatVersion.String())
			}

			if versioned.Version.Minor > CurrentFormatVersion.Minor && !compatOpts.AllowNewerMinor {
				return nil, fmt.Errorf("trace from newer minor version: trace is %s but CLI is %s\n"+
					"  Set AllowNewerMinor=true to attempt loading (may lose new features)\n"+
					"  Recommended: upgrade CLI to version %d.%d.x or higher",
					versioned.Version.String(), CurrentFormatVersion.String(),
					versioned.Version.Major, versioned.Version.Minor)
			}
		}
	}

	// Apply migrations if loading older version.
	if versioned.Version.Minor < CurrentFormatVersion.Minor {
		migrated, err := migrateTrace(versioned.Trace, versioned.Version, CurrentFormatVersion)
		if err != nil {
			return nil, fmt.Errorf("failed to migrate trace from version %s to %s: %w\n"+
				"  The trace format may have changed in incompatible ways\n"+
				"  Fix: try re-exporting the trace with the older CLI version",
				versioned.Version.String(), CurrentFormatVersion.String(), err)
		}
		versioned.Trace = migrated
		fmt.Fprintf(os.Stderr, "Info: migrated trace from version %s to %s\n",
			versioned.Version.String(), CurrentFormatVersion.String())
	}

	return versioned.Trace, nil
}

// joinVersions formats a []string as a comma-separated list for error messages.
func joinVersions(versions []string) string {
	if len(versions) == 0 {
		return "(none)"
	}
	out := make([]string, len(versions))
	for i, v := range versions {
		out[i] = "\"" + v + "\""
	}
	result := ""
	for i, o := range out {
		if i > 0 {
			result += ", "
		}
		result += o
	}
	return result
}

// SchemaCompatibilityReport describes the compatibility between two trace versions.
type SchemaCompatibilityReport struct {
	FromVersion       TraceFormatVersion
	ToVersion         TraceFormatVersion
	Compatible        bool
	RequiresMigration bool
	Warnings          []string
	Actions           []string
}

// CheckSchemaCompatibility produces a detailed compatibility report between
// two trace format versions, including actionable migration guidance.
func CheckSchemaCompatibility(from, to TraceFormatVersion) SchemaCompatibilityReport {
	r := SchemaCompatibilityReport{
		FromVersion: from,
		ToVersion:   to,
	}

	if from.Major != to.Major {
		r.Compatible = false
		r.RequiresMigration = true
		r.Warnings = append(r.Warnings,
			fmt.Sprintf("major version mismatch: %s → %s", from.String(), to.String()))
		r.Actions = append(r.Actions,
			fmt.Sprintf("use Glassbox CLI v%d.x.x to work with this trace", from.Major))
		return r
	}

	if from.Minor > to.Minor {
		r.Compatible = false
		r.RequiresMigration = true
		r.Warnings = append(r.Warnings,
			fmt.Sprintf("trace is newer minor (%s) than CLI (%s)", from.String(), to.String()))
		r.Actions = append(r.Actions,
			"upgrade Glassbox CLI to match the trace version")
		return r
	}

	if from.Minor < to.Minor {
		r.Compatible = true
		r.RequiresMigration = true
		r.Warnings = append(r.Warnings,
			fmt.Sprintf("trace uses older minor version (%s); migration to %s required", from.String(), to.String()))
		r.Actions = append(r.Actions,
			fmt.Sprintf("re-export trace with current CLI (%s) for best results", to.String()))
		return r
	}

	r.Compatible = true
	return r
}

// migrateTrace migrates a trace from one version to another
func migrateTrace(trace *ExecutionTrace, fromVersion, toVersion TraceFormatVersion) (*ExecutionTrace, error) {
	if trace == nil {
		return nil, fmt.Errorf("cannot migrate nil trace")
	}
	
	// Create a copy to avoid modifying original. Annotations are deep-copied
	// because they hold slices and a map — a shallow copy would leave the
	// migrated trace sharing reviewer comments with the original, so editing
	// one would silently edit the other.
	migrated := &ExecutionTrace{
		TransactionHash:  trace.TransactionHash,
		StartTime:        trace.StartTime,
		EndTime:          trace.EndTime,
		States:           make([]ExecutionState, len(trace.States)),
		Snapshots:        trace.Snapshots,
		DiagnosticEvents: trace.DiagnosticEvents,
		Annotations:      trace.Annotations.Clone(),
		CurrentStep:      trace.CurrentStep,
		SnapshotInterval: trace.SnapshotInterval,
	}
	copy(migrated.States, trace.States)
	
	// Apply version-specific migrations
	// Example: migrate from 1.0.x to 1.1.x
	if fromVersion.Minor == 0 && toVersion.Minor >= 1 {
		// Add migration logic here when we have version 1.1
		// For now, no migrations needed
	}
	
	return migrated, nil
}

// DetectFormat attempts to detect the format of a trace file
func DetectFormat(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}
	
	// Try JSON first
	var jsonTest interface{}
	if err := json.Unmarshal(data, &jsonTest); err == nil {
		return "json", nil
	}
	
	// Check for HTML
	content := string(data)
	if strings.Contains(content, "<!doctype html>") || strings.Contains(content, "<html") {
		return "html", nil
	}
	
	// Check for Markdown
	if strings.HasPrefix(content, "# Glassbox Trace Export") {
		return "markdown", nil
	}
	
	// Check for plain text
	if strings.Contains(content, "Glassbox Trace Export") && strings.Contains(content, "=====") {
		return "text", nil
	}
	
	return "", fmt.Errorf("unable to detect format\n"+
		"  File does not match any known trace export format\n"+
		"  Supported formats: JSON, HTML, Markdown, Text")
}

// ConvertFormat converts a trace from one format to another
func ConvertFormat(inputPath, outputPath, targetFormat string) error {
	// Detect input format
	inputFormat, err := DetectFormat(inputPath)
	if err != nil {
		return fmt.Errorf("failed to detect input format: %w", err)
	}
	
	// Only JSON can be converted back to trace
	if inputFormat != "json" {
		return fmt.Errorf("can only convert from JSON format\n"+
			"  Input format: %s\n"+
			"  Fix: export original trace in JSON format for conversion capability\n"+
			"  Note: HTML, Markdown, and Text are presentation-only formats", inputFormat)
	}
	
	// Load trace from JSON
	trace, err := LoadVersionedTrace(inputPath, DefaultCompatibilityOptions())
	if err != nil {
		return fmt.Errorf("failed to load trace: %w", err)
	}
	
	// Export in target format
	if err := ExportExecutionTrace(trace, targetFormat, outputPath); err != nil {
		return fmt.Errorf("failed to export in %s format: %w", targetFormat, err)
	}
	
	fmt.Printf("Successfully converted trace from %s to %s\n", inputFormat, targetFormat)
	fmt.Printf("  Input:  %s\n", inputPath)
	fmt.Printf("  Output: %s\n", outputPath)
	
	return nil
}

// ValidateFormatCompatibility validates if a trace can be exported in the given format
func ValidateFormatCompatibility(trace *ExecutionTrace, format string, compatOpts CompatibilityOptions) []string {
	var warnings []string
	
	format = strings.ToLower(strings.TrimSpace(format))
	
	// Check for format-specific issues
	switch format {
	case "json":
		// JSON is most flexible, but check for non-serializable types
		// This is handled by ValidateTraceFormatCompatibility, but add extra checks
		if trace.cachedSubcallGraph != nil {
			warnings = append(warnings, "Cached subcall graph will be omitted from JSON export (it's regenerated on load)")
		}
		
	case "html":
		// Check for data that might cause browser issues
		for i, state := range trace.States {
			if len(state.Error) > 5000 {
				warnings = append(warnings, fmt.Sprintf("Step %d has a very long error message (%d chars) that may affect HTML rendering", i, len(state.Error)))
			}
		}
		
		if len(trace.States) > 10000 {
			warnings = append(warnings, fmt.Sprintf("Trace has %d steps which may cause slow HTML rendering in browsers - consider filtering or using JSON format", len(trace.States)))
		}
		
	case "markdown":
		// Check for markdown-unfriendly content
		for i, state := range trace.States {
			if strings.Contains(state.Error, "```") {
				warnings = append(warnings, fmt.Sprintf("Step %d error message contains markdown code fence markers which may break formatting", i))
			}
		}
		
	case "text":
		// Text format is most permissive
		if len(trace.States) > 5000 {
			warnings = append(warnings, fmt.Sprintf("Trace has %d steps - text output will be very long", len(trace.States)))
		}
	}
	
	// Surface reviewer comments whose targets no longer resolve. The comments
	// are still exported — this only tells the operator that an anchor broke,
	// typically because the trace was filtered or migrated since the review.
	for _, w := range trace.AnnotationRefReport().Warnings() {
		warnings = append(warnings, "Dangling annotation: "+w)
	}

	// Check for lossy conversions if downgrading
	if compatOpts.AllowDowngrade {
		warnings = append(warnings, "Downgrading to older format version may lose new features or fields")
	}

	return warnings
}

// ExportWithCompatibility exports a trace with full compatibility checks and warnings
func ExportWithCompatibility(trace *ExecutionTrace, format, outputPath string, opts ExportOptions, compatOpts CompatibilityOptions) error {
	// Run compatibility validation
	warnings := ValidateFormatCompatibility(trace, format, compatOpts)
	for _, warning := range warnings {
		fmt.Fprintf(os.Stderr, "Warning: %s\n", warning)
	}
	
	// Use versioned export for JSON
	if strings.ToLower(format) == "json" {
		return ExportVersionedTrace(trace, format, outputPath, opts, compatOpts)
	}
	
	// Use standard export for other formats
	return ExportExecutionTraceWithOptions(trace, format, outputPath, opts)
}
