// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Tests for improved user guidance, validation, and error messaging in the
// trace command's export flow (PreRunE and RunE paths).

package cmd

import (
	"strings"
	"testing"
)

// ── PreRunE: --export-format validation ──────────────────────────────────────

// TestTracePreRunE_InvalidExportFormat rejects unknown --export-format values
// and lists the supported ones in the error message.
func TestTracePreRunE_InvalidExportFormat(t *testing.T) {
	t.Cleanup(resetTraceFlags)
	traceExportPath = "./output.trace"
	traceExportFormat = "yaml"

	err := traceCmd.PreRunE(traceCmd, []string{})
	if err == nil {
		t.Fatal("expected error for invalid --export-format")
	}
	msg := err.Error()
	if !strings.Contains(msg, "yaml") {
		t.Errorf("error should name the invalid format, got: %q", msg)
	}
	if !strings.Contains(msg, "html") || !strings.Contains(msg, "json") {
		t.Errorf("error should list valid formats, got: %q", msg)
	}
	if !strings.Contains(msg, "Fix:") {
		t.Errorf("error should include a Fix hint, got: %q", msg)
	}
}

// TestTracePreRunE_ValidExportFormats verifies all supported formats are accepted.
func TestTracePreRunE_ValidExportFormats(t *testing.T) {
	for _, format := range []string{"html", "markdown", "md", "json", "text"} {
		format := format
		t.Run(format, func(t *testing.T) {
			t.Cleanup(resetTraceFlags)
			traceExportPath = "./output.trace"
			traceExportFormat = format

			err := traceCmd.PreRunE(traceCmd, []string{})
			if err != nil {
				t.Errorf("unexpected error for valid --export-format %q: %v", format, err)
			}
		})
	}
}

// ── PreRunE: directory path rejection ────────────────────────────────────────

// TestTracePreRunE_ExportDirectoryPath rejects --export paths that end with a
// separator (indicating the user forgot to include a filename).
func TestTracePreRunE_ExportDirectoryPath(t *testing.T) {
	t.Cleanup(resetTraceFlags)
	traceExportPath = "./traces/"
	traceExportFormat = "html"

	err := traceCmd.PreRunE(traceCmd, []string{})
	if err == nil {
		t.Fatal("expected error for --export path that looks like a directory")
	}
	msg := err.Error()
	if !strings.Contains(msg, "directory path") {
		t.Errorf("error should mention 'directory path', got: %q", msg)
	}
	if !strings.Contains(msg, "Fix:") {
		t.Errorf("error should include a Fix hint, got: %q", msg)
	}
}

// TestTracePreRunE_ExportMarkdownDirectoryPath rejects --export-markdown paths
// that end with a directory separator.
func TestTracePreRunE_ExportMarkdownDirectoryPath(t *testing.T) {
	t.Cleanup(resetTraceFlags)
	traceExportMarkdown = "./reports/"

	err := traceCmd.PreRunE(traceCmd, []string{})
	if err == nil {
		t.Fatal("expected error for --export-markdown path that looks like a directory")
	}
	msg := err.Error()
	if !strings.Contains(msg, "directory path") {
		t.Errorf("error should mention 'directory path', got: %q", msg)
	}
}

// TestTracePreRunE_OutputJSONDirectoryPath rejects --output-json paths that
// end with a directory separator.
func TestTracePreRunE_OutputJSONDirectoryPath(t *testing.T) {
	t.Cleanup(resetTraceFlags)
	traceOutputJSON = "./json-exports/"

	err := traceCmd.PreRunE(traceCmd, []string{})
	if err == nil {
		t.Fatal("expected error for --output-json path that looks like a directory")
	}
	msg := err.Error()
	if !strings.Contains(msg, "directory path") {
		t.Errorf("error should mention 'directory path', got: %q", msg)
	}
}

// TestTracePreRunE_ExportSVGDirectoryPath rejects --export-svg paths that
// end with a directory separator.
func TestTracePreRunE_ExportSVGDirectoryPath(t *testing.T) {
	t.Cleanup(resetTraceFlags)
	traceExportSVG = "./svg/"

	err := traceCmd.PreRunE(traceCmd, []string{})
	if err == nil {
		t.Fatal("expected error for --export-svg path that looks like a directory")
	}
	msg := err.Error()
	if !strings.Contains(msg, "directory path") {
		t.Errorf("error should mention 'directory path', got: %q", msg)
	}
}

// ── PreRunE: mutual exclusion guards ─────────────────────────────────────────

// TestTracePreRunE_ExportAndPrintMutuallyExclusive rejects --export + --print.
func TestTracePreRunE_ExportAndPrintMutuallyExclusive(t *testing.T) {
	t.Cleanup(resetTraceFlags)
	traceExportPath = "./output.html"
	traceExportFormat = "html"
	tracePrint = true

	err := traceCmd.PreRunE(traceCmd, []string{})
	if err == nil {
		t.Fatal("expected error when both --export and --print are set")
	}
	msg := err.Error()
	if !strings.Contains(msg, "--export") || !strings.Contains(msg, "--print") {
		t.Errorf("error should mention both flags, got: %q", msg)
	}
}

// TestTracePreRunE_ExportMarkdownAndExportMutuallyExclusive rejects
// --export-markdown + --export.
func TestTracePreRunE_ExportMarkdownAndExportMutuallyExclusive(t *testing.T) {
	t.Cleanup(resetTraceFlags)
	traceExportMarkdown = "./report.md"
	traceExportPath = "./output.html"
	traceExportFormat = "html"

	err := traceCmd.PreRunE(traceCmd, []string{})
	if err == nil {
		t.Fatal("expected error when both --export-markdown and --export are set")
	}
	msg := err.Error()
	if !strings.Contains(msg, "--export-markdown") {
		t.Errorf("error should mention --export-markdown, got: %q", msg)
	}
}

// ── PreRunE: --trace-verbosity validation ────────────────────────────────────

// TestTracePreRunE_InvalidVerbosity rejects unknown --trace-verbosity values
// and names the valid options.
func TestTracePreRunE_InvalidVerbosity(t *testing.T) {
	t.Cleanup(resetTraceFlags)
	traceVerbosity = "extreme"

	err := traceCmd.PreRunE(traceCmd, []string{})
	if err == nil {
		t.Fatal("expected error for invalid --trace-verbosity")
	}
	msg := err.Error()
	if !strings.Contains(msg, "extreme") {
		t.Errorf("error should name the invalid value, got: %q", msg)
	}
	if !strings.Contains(msg, "summary") || !strings.Contains(msg, "verbose") {
		t.Errorf("error should list valid options, got: %q", msg)
	}
}

// TestTracePreRunE_ValidVerbosityValues passes for all supported verbosity levels.
func TestTracePreRunE_ValidVerbosityValues(t *testing.T) {
	for _, v := range []string{"summary", "normal", "verbose"} {
		v := v
		t.Run(v, func(t *testing.T) {
			t.Cleanup(resetTraceFlags)
			traceVerbosity = v

			err := traceCmd.PreRunE(traceCmd, []string{})
			if err != nil {
				t.Errorf("unexpected error for valid verbosity %q: %v", v, err)
			}
		})
	}
}

// ── PreRunE: missing file guards ─────────────────────────────────────────────

// TestTracePreRunE_AnnotationsFileNotFound rejects a nonexistent --annotations
// file path with a clear message.
func TestTracePreRunE_AnnotationsFileNotFound(t *testing.T) {
	t.Cleanup(resetTraceFlags)
	traceAnnotationsFlag = "/nonexistent/annotations.json"

	err := traceCmd.PreRunE(traceCmd, []string{})
	if err == nil {
		t.Fatal("expected error for nonexistent --annotations file")
	}
	msg := err.Error()
	if !strings.Contains(msg, "--annotations") {
		t.Errorf("error should mention --annotations, got: %q", msg)
	}
	if !strings.Contains(msg, "not found") {
		t.Errorf("error should say 'not found', got: %q", msg)
	}
}

// TestTracePreRunE_GasModelFileNotFound rejects a nonexistent --gas-model path.
func TestTracePreRunE_GasModelFileNotFound(t *testing.T) {
	t.Cleanup(resetTraceFlags)
	traceGasModelPath = "/nonexistent/gas.json"

	err := traceCmd.PreRunE(traceCmd, []string{})
	if err == nil {
		t.Fatal("expected error for nonexistent --gas-model file")
	}
	msg := err.Error()
	if !strings.Contains(msg, "--gas-model") {
		t.Errorf("error should mention --gas-model, got: %q", msg)
	}
	if !strings.Contains(msg, "not found") {
		t.Errorf("error should say 'not found', got: %q", msg)
	}
}

// ── PreRunE: multiple failures collected ─────────────────────────────────────

// TestTracePreRunE_MultipleErrors ensures all validation failures are reported
// in a single pass so users can fix them together.
func TestTracePreRunE_MultipleErrors(t *testing.T) {
	t.Cleanup(resetTraceFlags)
	traceExportPath = "./traces/" // directory path
	traceExportFormat = "yaml"   // invalid format

	err := traceCmd.PreRunE(traceCmd, []string{})
	if err == nil {
		t.Fatal("expected error for multiple invalid inputs")
	}
	msg := err.Error()
	// Should report both failures.
	if !strings.Contains(msg, "yaml") {
		t.Errorf("error should mention invalid format 'yaml', got: %q", msg)
	}
	if !strings.Contains(msg, "directory path") {
		t.Errorf("error should mention directory path issue, got: %q", msg)
	}
	// Multiple errors are numbered.
	if !strings.Contains(msg, "1.") || !strings.Contains(msg, "2.") {
		t.Errorf("multiple errors should be numbered, got: %q", msg)
	}
}

// ── RunE: trace file not found ────────────────────────────────────────────────

// TestTraceRunE_FileNotFound produces a clear error with remediation when the
// trace file does not exist.
func TestTraceRunE_FileNotFound(t *testing.T) {
	t.Cleanup(resetTraceFlags)

	err := traceCmd.RunE(traceCmd, []string{"/nonexistent/trace.json"})
	if err == nil {
		t.Fatal("expected error for nonexistent trace file")
	}
	msg := err.Error()
	if !strings.Contains(msg, "not found") {
		t.Errorf("error should say file not found, got: %q", msg)
	}
	// Should tell the user how to produce a trace file.
	if !strings.Contains(msg, "glassbox debug") {
		t.Errorf("error should mention 'glassbox debug' as the way to produce trace files, got: %q", msg)
	}
}

// TestTraceRunE_NoFileArg produces a clear usage message when no trace file
// is specified and --file is also absent.
func TestTraceRunE_NoFileArg(t *testing.T) {
	t.Cleanup(resetTraceFlags)

	err := traceCmd.RunE(traceCmd, []string{})
	if err == nil {
		t.Fatal("expected error when no trace file is given")
	}
	msg := err.Error()
	if !strings.Contains(msg, "trace file is required") {
		t.Errorf("error should say trace file is required, got: %q", msg)
	}
	if !strings.Contains(msg, "glassbox trace") {
		t.Errorf("error should include usage guidance, got: %q", msg)
	}
}

// ── traceExportOptions: --meta format validation ─────────────────────────────

// TestTraceExportOptions_BadMetaFormat verifies that malformed --meta values
// (not key=value) produce a clear, actionable error.
func TestTraceExportOptions_BadMetaFormat(t *testing.T) {
	prev := traceMetadata
	t.Cleanup(func() { traceMetadata = prev })

	traceMetadata = []string{"no-equals-sign"}
	_, err := traceExportOptions()
	if err == nil {
		t.Fatal("expected error for --meta value without '='")
	}
	msg := err.Error()
	if !strings.Contains(msg, "no-equals-sign") {
		t.Errorf("error should include the bad value, got: %q", msg)
	}
	if !strings.Contains(msg, "key=value") {
		t.Errorf("error should explain key=value format, got: %q", msg)
	}
}

// TestTraceExportOptions_EmptyKeyMeta verifies that --meta values with an
// empty key are rejected.
func TestTraceExportOptions_EmptyKeyMeta(t *testing.T) {
	prev := traceMetadata
	t.Cleanup(func() { traceMetadata = prev })

	traceMetadata = []string{"=value-no-key"}
	_, err := traceExportOptions()
	if err == nil {
		t.Fatal("expected error for --meta value with empty key")
	}
	msg := err.Error()
	if !strings.Contains(msg, "key=value") {
		t.Errorf("error should explain key=value format, got: %q", msg)
	}
}

// TestTraceExportOptions_ValidMeta verifies that well-formed --meta values
// are parsed correctly.
func TestTraceExportOptions_ValidMeta(t *testing.T) {
	prev := traceMetadata
	t.Cleanup(func() { traceMetadata = prev })

	traceMetadata = []string{"env=testnet", "version=1.2", "team=platform"}
	opts, err := traceExportOptions()
	if err != nil {
		t.Fatalf("unexpected error for valid --meta values: %v", err)
	}
	if opts.SessionMetadata["env"] != "testnet" {
		t.Errorf("expected env=testnet, got: %v", opts.SessionMetadata["env"])
	}
	if opts.SessionMetadata["version"] != "1.2" {
		t.Errorf("expected version=1.2, got: %v", opts.SessionMetadata["version"])
	}
}

// TestTraceExportOptions_MetaValueWithEquals verifies that values containing
// '=' are preserved correctly.
func TestTraceExportOptions_MetaValueWithEquals(t *testing.T) {
	prev := traceMetadata
	t.Cleanup(func() { traceMetadata = prev })

	traceMetadata = []string{"filter=type=contract_call"}
	opts, err := traceExportOptions()
	if err != nil {
		t.Fatalf("unexpected error for --meta with '=' in value: %v", err)
	}
	if opts.SessionMetadata["filter"] != "type=contract_call" {
		t.Errorf("expected filter=type=contract_call, got: %v", opts.SessionMetadata["filter"])
	}
}

// ── PreRunE: --dry-run requires an export target ──────────────────────────────

// TestTracePreRunE_DryRunWithoutExportTarget rejects --dry-run when no export
// flag is set, since there is nothing to validate.
func TestTracePreRunE_DryRunWithoutExportTarget(t *testing.T) {
	t.Cleanup(resetTraceFlags)
	traceDryRunFlag = true // no --export, --output-json, or --export-svg

	err := traceCmd.PreRunE(traceCmd, []string{})
	if err == nil {
		t.Fatal("expected error: --dry-run with no export target")
	}
	msg := err.Error()
	if !strings.Contains(msg, "--dry-run") {
		t.Errorf("error should mention --dry-run, got: %q", msg)
	}
	if !strings.Contains(msg, "Fix:") {
		t.Errorf("error should include a Fix hint, got: %q", msg)
	}
}

// TestTracePreRunE_DryRunWithExportTarget passes --dry-run with an export path.
func TestTracePreRunE_DryRunWithExportTarget(t *testing.T) {
	t.Cleanup(resetTraceFlags)
	traceDryRunFlag = true
	traceExportPath = "./output.html"
	traceExportFormat = "html"

	err := traceCmd.PreRunE(traceCmd, []string{})
	if err != nil {
		t.Errorf("--dry-run with a valid export target should pass PreRunE, got: %v", err)
	}
}

// ── PreRunE: --format alias ───────────────────────────────────────────────────

// TestTracePreRunE_FormatAliasValid verifies that --format is accepted for all
// supported export formats (it is an alias for --export-format).
func TestTracePreRunE_FormatAliasValid(t *testing.T) {
	for _, format := range []string{"html", "markdown", "json", "text"} {
		format := format
		t.Run(format, func(t *testing.T) {
			t.Cleanup(resetTraceFlags)
			traceExportPath = "./output.trace"
			traceFormatAlias = format
			// Simulate the user typing --format (mark format flag as changed).
			// PreRunE merges traceFormatAlias → traceExportFormat when "format"
			// is marked changed. We do this by setting both here.
			traceExportFormat = format

			err := traceCmd.PreRunE(traceCmd, []string{})
			if err != nil {
				t.Errorf("--format=%q should be accepted, got: %v", format, err)
			}
		})
	}
}

// TestTracePreRunE_FormatAliasInvalid verifies that --format with an
// unsupported value is rejected with a clear Fix hint.
func TestTracePreRunE_FormatAliasInvalid(t *testing.T) {
	t.Cleanup(resetTraceFlags)
	traceExportPath = "./output.trace"
	traceExportFormat = "csv" // invalid

	err := traceCmd.PreRunE(traceCmd, []string{})
	if err == nil {
		t.Fatal("expected error for invalid --format value via alias")
	}
	msg := err.Error()
	if !strings.Contains(msg, "csv") {
		t.Errorf("error should mention the invalid format 'csv', got: %q", msg)
	}
	if !strings.Contains(msg, "Fix:") {
		t.Errorf("error should include a Fix hint, got: %q", msg)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

// resetTraceFlags restores all trace-command flag variables to their defaults.
func resetTraceFlags() {
	traceFile = ""
	traceThemeFlag = ""
	tracePrint = false
	traceNoColor = false
	traceExportSVG = ""
	traceOutputJSON = ""
	traceExportPath = ""
	traceExportFormat = "html"
	traceFormatAlias = ""
	traceExportMarkdown = ""
	traceAnnotationsFlag = ""
	traceAnnotationsExportPath = ""
	traceBookmarksOnConflict = "fail"
	traceBookmarksPreview = false
	traceGasModelPath = ""
	traceVerbosity = "normal"
	traceComments = nil
	traceMetadata = nil
	traceDryRunFlag = false
	traceForceFlag = false
	traceShowTimingFlag = false
}

// ── PreRunE: --meta pre-flight validation ────────────────────────────────────

// TestTracePreRunE_MetaNoEquals verifies that a --meta value without an '='
// separator is caught in PreRunE, before any trace file I/O.
func TestTracePreRunE_MetaNoEquals(t *testing.T) {
	t.Cleanup(resetTraceFlags)
	traceExportPath = "./output.html"
	traceExportFormat = "html"
	traceMetadata = []string{"no-equals-sign"}

	err := traceCmd.PreRunE(traceCmd, []string{})
	if err == nil {
		t.Fatal("expected error for --meta value without '='")
	}
	msg := err.Error()
	if !strings.Contains(msg, "no-equals-sign") {
		t.Errorf("error should echo the bad --meta value, got: %q", msg)
	}
	if !strings.Contains(msg, "key=value") {
		t.Errorf("error should explain key=value format, got: %q", msg)
	}
	if !strings.Contains(msg, "Fix:") {
		t.Errorf("error should include a Fix hint, got: %q", msg)
	}
}

// TestTracePreRunE_MetaEmptyKey verifies that a --meta value with an empty
// key (starts with '=') is rejected in PreRunE.
func TestTracePreRunE_MetaEmptyKey(t *testing.T) {
	t.Cleanup(resetTraceFlags)
	traceExportPath = "./output.html"
	traceExportFormat = "html"
	traceMetadata = []string{"=value-no-key"}

	err := traceCmd.PreRunE(traceCmd, []string{})
	if err == nil {
		t.Fatal("expected error for --meta with empty key")
	}
	if !strings.Contains(err.Error(), "key=value") {
		t.Errorf("error should explain key=value format, got: %q", err.Error())
	}
}

// TestTracePreRunE_MetaValid verifies that well-formed --meta values pass PreRunE.
func TestTracePreRunE_MetaValid(t *testing.T) {
	t.Cleanup(resetTraceFlags)
	traceExportPath = "./output.html"
	traceExportFormat = "html"
	traceMetadata = []string{"env=testnet", "version=1.2", "filter=type=contract_call"}

	err := traceCmd.PreRunE(traceCmd, []string{})
	if err != nil {
		t.Errorf("valid --meta values should pass PreRunE, got: %v", err)
	}
}

// TestTracePreRunE_MetaValueWithEquals verifies that a value containing '='
// is handled correctly (only the first '=' is used as the separator).
func TestTracePreRunE_MetaValueWithEquals(t *testing.T) {
	t.Cleanup(resetTraceFlags)
	traceExportPath = "./output.html"
	traceExportFormat = "html"
	// "filter=type=contract_call" — key="filter", value="type=contract_call"
	traceMetadata = []string{"filter=type=contract_call"}

	err := traceCmd.PreRunE(traceCmd, []string{})
	if err != nil {
		t.Errorf("--meta with '=' in value should be accepted, got: %v", err)
	}
}

// ── PreRunE: --comment pre-flight validation ──────────────────────────────────

// TestTracePreRunE_EmptyComment verifies that an empty --comment string is
// rejected in PreRunE with a clear message naming the position.
func TestTracePreRunE_EmptyComment(t *testing.T) {
	t.Cleanup(resetTraceFlags)
	traceExportPath = "./output.html"
	traceExportFormat = "html"
	traceComments = []string{""}

	err := traceCmd.PreRunE(traceCmd, []string{})
	if err == nil {
		t.Fatal("expected error for empty --comment value")
	}
	msg := err.Error()
	if !strings.Contains(msg, "empty") && !strings.Contains(msg, "whitespace") {
		t.Errorf("error should mention empty/whitespace, got: %q", msg)
	}
	if !strings.Contains(msg, "Fix:") {
		t.Errorf("error should include a Fix hint, got: %q", msg)
	}
}

// TestTracePreRunE_WhitespaceComment verifies that a whitespace-only --comment
// is also rejected.
func TestTracePreRunE_WhitespaceComment(t *testing.T) {
	t.Cleanup(resetTraceFlags)
	traceExportPath = "./output.html"
	traceExportFormat = "html"
	traceComments = []string{"   "}

	err := traceCmd.PreRunE(traceCmd, []string{})
	if err == nil {
		t.Fatal("expected error for whitespace-only --comment")
	}
}

// TestTracePreRunE_ValidComment verifies that a non-empty --comment passes
// PreRunE without error.
func TestTracePreRunE_ValidComment(t *testing.T) {
	t.Cleanup(resetTraceFlags)
	traceExportPath = "./output.html"
	traceExportFormat = "html"
	traceComments = []string{"Reviewed by Alice", "Approved for deploy"}

	err := traceCmd.PreRunE(traceCmd, []string{})
	if err != nil {
		t.Errorf("valid --comment values should pass PreRunE, got: %v", err)
	}
}

// TestTracePreRunE_MetaAndCommentBothFailTogether verifies that --meta and
// --comment failures are collected into a single numbered error list.
func TestTracePreRunE_MetaAndCommentBothFailTogether(t *testing.T) {
	t.Cleanup(resetTraceFlags)
	traceExportPath = "./output.html"
	traceExportFormat = "html"
	traceMetadata = []string{"bad-meta"}
	traceComments = []string{""}

	err := traceCmd.PreRunE(traceCmd, []string{})
	if err == nil {
		t.Fatal("expected errors for both bad --meta and empty --comment")
	}
	msg := err.Error()
	// Both failures must appear.
	if !strings.Contains(msg, "bad-meta") {
		t.Errorf("error should mention the bad --meta value, got: %q", msg)
	}
	if !strings.Contains(msg, "empty") && !strings.Contains(msg, "whitespace") {
		t.Errorf("error should mention the empty --comment, got: %q", msg)
	}
	// Multiple errors are numbered.
	if !strings.Contains(msg, "1.") || !strings.Contains(msg, "2.") {
		t.Errorf("multiple errors should be numbered, got: %q", msg)
	}
}
