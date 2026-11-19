// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package sourcemap

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dotandev/glassbox/internal/wasmvalidate"
)

// PreflightIssue describes a single environment problem found during preflight.
type PreflightIssue struct {
	// Check is a short label for the check that failed (e.g. "wasm_target").
	Check string
	// Severity is "error" (blocks source mapping) or "warning" (degrades quality).
	Severity string
	// Description explains what is wrong.
	Description string
	// Hint is an actionable remediation step.
	Hint string
}

// PreflightReport is the result of RunSourceMapPreflight.
type PreflightReport struct {
	// OK is true when no error-severity issues were found.
	OK bool
	// Issues lists every problem found (errors and warnings).
	Issues []PreflightIssue
}

// Summary returns a human-readable description of all issues in the report,
// suitable for diagnostic output. It returns an empty string when there are no
// issues. Each issue is formatted as:
//
//	[severity] check: description
//	  Hint: <actionable step>
func (r *PreflightReport) Summary() string {
	if len(r.Issues) == 0 {
		return ""
	}
	var sb strings.Builder
	for i, issue := range r.Issues {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(fmt.Sprintf("[%s] %s: %s", issue.Severity, issue.Check, issue.Description))
		if issue.Hint != "" {
			sb.WriteString("\n  Hint: ")
			sb.WriteString(issue.Hint)
		}
	}
	return sb.String()
}

// RunSourceMapPreflight inspects the local environment for conditions that are
// required (or strongly recommended) for accurate source mapping.
//
// Checks performed:
//   - projectRoot, when non-empty, must not contain null bytes (error if it does)
//   - projectRoot, when non-empty, exists and is a directory (error if not)
//   - WASM target directory exists under projectRoot (target/wasm32-unknown-unknown)
//   - At least one .wasm file is present in the release output directory
//   - GLASSBOX_SKIP_SOURCE_MAPPING env var is not set to a truthy value
//     (if it is, warn so users know source mapping is disabled)
//   - GLASSBOX_SOURCE_MAP_CACHE env var, when set, points to a writable directory
//
// projectRoot may be empty, in which case only environment variable checks are run.
func RunSourceMapPreflight(projectRoot string) *PreflightReport {
	report := &PreflightReport{}

	// ── WASM build artifact checks ────────────────────────────────────────────
	if projectRoot != "" {
		// Reject null bytes before any filesystem access.
		if strings.ContainsRune(projectRoot, 0) {
			report.Issues = append(report.Issues, PreflightIssue{
				Check:       "project_root",
				Severity:    "error",
				Description: "project root path contains null bytes and cannot be used",
				Hint:        "Remove null bytes from the project root path.",
			})
			report.OK = false
			return report
		}

		rootInfo, rootErr := os.Stat(projectRoot)
		if os.IsNotExist(rootErr) {
			report.Issues = append(report.Issues, PreflightIssue{
				Check:       "project_root",
				Severity:    "error",
				Description: fmt.Sprintf("project root directory does not exist: %s", projectRoot),
				Hint:        "Ensure the project root path is correct and the directory has been created.",
			})
			report.OK = false
			return report
		} else if rootErr == nil && !rootInfo.IsDir() {
			report.Issues = append(report.Issues, PreflightIssue{
				Check:       "project_root",
				Severity:    "error",
				Description: fmt.Sprintf("%q is not a directory", projectRoot),
				Hint:        "Provide the path to the contract project root directory, not a file.",
			})
			report.OK = false
			return report
		}

		wasmTargetDir := filepath.Join(projectRoot, "target", "wasm32-unknown-unknown", "release")
		if _, err := os.Stat(wasmTargetDir); os.IsNotExist(err) {
			report.Issues = append(report.Issues, PreflightIssue{
				Check:    "wasm_target_dir",
				Severity: "warning",
				Description: fmt.Sprintf(
					"WASM target directory not found: %s", wasmTargetDir,
				),
				Hint: "Build your contract first: cd <contract-dir> && cargo build --target wasm32-unknown-unknown --release",
			})
		} else if err == nil {
			// Directory exists — check for at least one .wasm file.
			wasmFiles, _ := filepath.Glob(filepath.Join(wasmTargetDir, "*.wasm"))
			if len(wasmFiles) == 0 {
				report.Issues = append(report.Issues, PreflightIssue{
					Check:    "wasm_artifacts",
					Severity: "warning",
					Description: fmt.Sprintf(
						"no .wasm files found in %s", wasmTargetDir,
					),
					Hint: "Compile the contract with debug symbols: cargo build --target wasm32-unknown-unknown --release",
				})
			}

			// Structurally validate every discovered .wasm file so corrupt or
			// hostile artifacts are reported here, with a field-specific
			// diagnostic, rather than surfacing as an opaque parser error
			// once deep source mapping begins.
			for _, wasmFile := range wasmFiles {
				content, readErr := os.ReadFile(wasmFile)
				if readErr != nil {
					report.Issues = append(report.Issues, PreflightIssue{
						Check:       "wasm_structural",
						Severity:    "warning",
						Description: fmt.Sprintf("could not read %q: %v", wasmFile, readErr),
						Hint:        "Ensure the file is readable and try rebuilding the contract.",
					})
					continue
				}
				validation := wasmvalidate.Validate(content, wasmvalidate.DefaultLimits())
				for _, issue := range validation.Issues {
					severity := "warning"
					if issue.Class == wasmvalidate.ClassStructural {
						severity = "error"
					}
					report.Issues = append(report.Issues, PreflightIssue{
						Check:       "wasm_" + string(issue.Class),
						Severity:    severity,
						Description: fmt.Sprintf("%s: %s (%s)", wasmFile, issue.Description, issue.Field),
						Hint:        issue.Hint,
					})
				}
			}
		}
	}

	// ── GLASSBOX_SKIP_SOURCE_MAPPING ─────────────────────────────────────────
	if isTruthy(os.Getenv("GLASSBOX_SKIP_SOURCE_MAPPING")) {
		report.Issues = append(report.Issues, PreflightIssue{
			Check:       "skip_source_mapping_env",
			Severity:    "warning",
			Description: "GLASSBOX_SKIP_SOURCE_MAPPING is set — source mapping is disabled",
			Hint:        "Unset GLASSBOX_SKIP_SOURCE_MAPPING to re-enable source line resolution.",
		})
	}

	// ── GLASSBOX_SOURCE_MAP_CACHE ─────────────────────────────────────────────
	if cacheDir := os.Getenv("GLASSBOX_SOURCE_MAP_CACHE"); cacheDir != "" {
		if strings.ContainsRune(cacheDir, 0) {
			report.Issues = append(report.Issues, PreflightIssue{
				Check:       "source_map_cache_dir",
				Severity:    "error",
				Description: fmt.Sprintf("GLASSBOX_SOURCE_MAP_CACHE=%q contains null bytes and cannot be used", cacheDir),
				Hint:        "Unset GLASSBOX_SOURCE_MAP_CACHE or set it to a valid directory path without null bytes.",
			})
		} else if err := validateCacheDir(cacheDir); err != nil {
			report.Issues = append(report.Issues, PreflightIssue{
				Check:       "source_map_cache_dir",
				Severity:    "error",
				Description: fmt.Sprintf("GLASSBOX_SOURCE_MAP_CACHE=%q is not usable: %v", cacheDir, err),
				Hint:        "Set GLASSBOX_SOURCE_MAP_CACHE to an existing, writable directory or unset it to use the default.",
			})
		}
	}

	// Report is OK when there are no error-severity issues.
	report.OK = !hasErrors(report.Issues)
	return report
}

// isTruthy returns true for "1", "true", "yes" (case-insensitive).
func isTruthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// validateCacheDir checks that cacheDir is a usable (existing, writable) directory.
func validateCacheDir(dir string) error {
	info, err := os.Stat(dir)
	if os.IsNotExist(err) {
		return fmt.Errorf("directory does not exist: %q", dir)
	}
	if err != nil {
		return fmt.Errorf("cannot access directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%q is not a directory", dir)
	}
	// Probe writability with a temp file.
	probe := filepath.Join(dir, ".glassbox_write_probe")
	f, err := os.Create(probe) //nolint:gosec // probe file, no user input
	if err != nil {
		return fmt.Errorf("directory is not writable: %w", err)
	}
	_ = f.Close()
	_ = os.Remove(probe)
	return nil
}

// hasErrors returns true if any issue has severity "error".
func hasErrors(issues []PreflightIssue) bool {
	for _, i := range issues {
		if i.Severity == "error" {
			return true
		}
	}
	return false
}
