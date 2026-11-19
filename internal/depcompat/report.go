// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package depcompat

import (
	"fmt"
	"io"
	"strings"
	"text/template"
	"time"
)

// reportTextTemplate is the human-readable text template for a CompatReport.
// It is rendered by RenderText and embedded in the GitHub Actions job summary.
const reportTextTemplate = `Dependency Compatibility Report
================================
Run ID      : {{.RunID}}
Generated   : {{.GeneratedAt.Format "2006-01-02T15:04:05Z"}}
Dep Group   : {{depGroupLabel .DepGroup}}

Dependency Versions
-------------------
  Go stellar SDK  : {{or .Versions.StellarSDKVersion "(unknown)"}}
  Soroban host    : {{or .Versions.SorobanHostVersion "(unknown)"}}
  ed25519-dalek   : {{or .Versions.Ed25519DalekVersion "(unknown)"}}
  sha2            : {{or .Versions.Sha2Version "(unknown)"}}
  Go toolchain    : {{or .Versions.GoVersion "(unknown)"}}
  Rust toolchain  : {{or .Versions.RustVersion "(unknown)"}}

Summary
-------
  Total outputs tested : {{.Summary.TotalOutputs}}
  Matched (no diff)    : {{.Summary.OutputsMatched}}
  Expected diffs       : {{.Summary.OutputsExpected}}
  Unexpected diffs     : {{.Summary.OutputsUnexpected}}
  Errors               : {{.Summary.OutputsErrored}}
  Status               : {{statusLabel .Summary}}

Results
-------
{{range .Results}}  [{{classLabel .Class}}] {{.DepGroup}}/{{.OutputKind}}{{if .Error}}
    ERROR: {{.Error}}{{else if .Diffs}}
    Diffs ({{len .Diffs}}):{{range .Diffs}}
      {{.JSONPath}}: {{.GoldenValue}} → {{.ActualValue}} ({{.Class}})
        Reason: {{.Reason}}{{end}}{{else}}
    No diffs.{{end}}
{{end}}
`

// templateFuncs provides custom template functions.
var templateFuncs = template.FuncMap{
	"depGroupLabel": func(g DepGroup) string {
		if g == "" {
			return "all groups"
		}
		return string(g)
	},
	"classLabel": func(c DiffClass) string {
		switch c {
		case DiffClassNone:
			return "OK "
		case DiffClassExpected:
			return "EXP"
		case DiffClassUnexpected:
			return "FAIL"
		default:
			return "???"
		}
	},
	"statusLabel": func(s ReportSummary) string {
		switch {
		case s.HasErrors:
			return "ERROR — one or more capture/compare operations failed"
		case s.HasUnexpectedDiffs:
			return "FAIL  — unexpected serialization changes detected"
		case s.OutputsExpected > 0:
			return "WARN  — expected schema changes detected (review before accepting)"
		default:
			return "PASS  — all outputs match golden baselines"
		}
	},
}

// RenderText writes a human-readable text report to w.
func RenderText(r *CompatReport, w io.Writer) error {
	tmpl, err := template.New("report").Funcs(templateFuncs).Parse(reportTextTemplate)
	if err != nil {
		return fmt.Errorf("parse report template: %w", err)
	}
	if err := tmpl.Execute(w, r); err != nil {
		return fmt.Errorf("render report: %w", err)
	}
	return nil
}

// RenderMarkdown writes a GitHub-flavoured Markdown report to w.
// This is suitable for use as a GitHub Actions job summary.
func RenderMarkdown(r *CompatReport, w io.Writer) error {
	var sb strings.Builder

	// Header
	fmt.Fprintf(&sb, "## Dependency Compatibility Report\n\n")
	fmt.Fprintf(&sb, "| Field | Value |\n|---|---|\n")
	fmt.Fprintf(&sb, "| Run ID | `%s` |\n", r.RunID)
	fmt.Fprintf(&sb, "| Generated | %s |\n", r.GeneratedAt.Format(time.RFC3339))
	if r.DepGroup != "" {
		fmt.Fprintf(&sb, "| Dep Group | `%s` |\n", r.DepGroup)
	} else {
		fmt.Fprintf(&sb, "| Dep Group | all groups |\n")
	}
	fmt.Fprintln(&sb)

	// Versions
	fmt.Fprintf(&sb, "### Dependency Versions\n\n")
	fmt.Fprintf(&sb, "| Dependency | Version |\n|---|---|\n")
	fmt.Fprintf(&sb, "| go-stellar-sdk | `%s` |\n", orUnknown(r.Versions.StellarSDKVersion))
	fmt.Fprintf(&sb, "| soroban-env-host | `%s` |\n", orUnknown(r.Versions.SorobanHostVersion))
	fmt.Fprintf(&sb, "| ed25519-dalek | `%s` |\n", orUnknown(r.Versions.Ed25519DalekVersion))
	fmt.Fprintf(&sb, "| sha2 | `%s` |\n", orUnknown(r.Versions.Sha2Version))
	fmt.Fprintf(&sb, "| Go toolchain | `%s` |\n", orUnknown(r.Versions.GoVersion))
	fmt.Fprintf(&sb, "| Rust toolchain | `%s` |\n", orUnknown(r.Versions.RustVersion))
	fmt.Fprintln(&sb)

	// Summary badge
	badge := markdownBadge(r.Summary)
	fmt.Fprintf(&sb, "### Summary %s\n\n", badge)
	fmt.Fprintf(&sb, "| Metric | Count |\n|---|---|\n")
	fmt.Fprintf(&sb, "| Total outputs tested | %d |\n", r.Summary.TotalOutputs)
	fmt.Fprintf(&sb, "| Matched (no diff) | %d |\n", r.Summary.OutputsMatched)
	fmt.Fprintf(&sb, "| Expected diffs | %d |\n", r.Summary.OutputsExpected)
	fmt.Fprintf(&sb, "| Unexpected diffs | **%d** |\n", r.Summary.OutputsUnexpected)
	fmt.Fprintf(&sb, "| Errors | **%d** |\n", r.Summary.OutputsErrored)
	fmt.Fprintln(&sb)

	// Results table
	fmt.Fprintf(&sb, "### Results\n\n")
	fmt.Fprintf(&sb, "| Dep Group | Output Kind | Status | Diffs |\n|---|---|---|---|\n")
	for _, res := range r.Results {
		status := markdownStatus(res)
		diffSummary := "-"
		if res.Error != "" {
			diffSummary = fmt.Sprintf("ERROR: %s", res.Error)
		} else if len(res.Diffs) > 0 {
			unexpected := 0
			for _, d := range res.Diffs {
				if d.Class == DiffClassUnexpected {
					unexpected++
				}
			}
			diffSummary = fmt.Sprintf("%d total, %d unexpected", len(res.Diffs), unexpected)
		}
		fmt.Fprintf(&sb, "| `%s` | `%s` | %s | %s |\n",
			res.DepGroup, res.OutputKind, status, diffSummary)
	}
	fmt.Fprintln(&sb)

	// Detailed diff sections (only for non-matching results).
	for _, res := range r.Results {
		if res.Class == DiffClassNone && res.Error == "" {
			continue
		}
		fmt.Fprintf(&sb, "#### `%s/%s` Diffs\n\n", res.DepGroup, res.OutputKind)
		if res.Error != "" {
			fmt.Fprintf(&sb, "> [!ERROR]\n> %s\n\n", res.Error)
			continue
		}
		if len(res.Diffs) == 0 {
			fmt.Fprintf(&sb, "_No diffs._\n\n")
			continue
		}
		fmt.Fprintf(&sb, "| JSON Path | Golden | Actual | Class | Reason |\n|---|---|---|---|---|\n")
		for _, d := range res.Diffs {
			fmt.Fprintf(&sb, "| `%s` | `%s` | `%s` | %s | %s |\n",
				mdEscape(d.JSONPath),
				mdEscape(d.GoldenValue),
				mdEscape(d.ActualValue),
				string(d.Class),
				mdEscape(d.Reason),
			)
		}
		fmt.Fprintln(&sb)
	}

	_, err := io.WriteString(w, sb.String())
	return err
}

// --- helpers -----------------------------------------------------------------

func orUnknown(s string) string {
	if s == "" {
		return "(unknown)"
	}
	return s
}

func markdownBadge(s ReportSummary) string {
	switch {
	case s.HasErrors || s.HasUnexpectedDiffs:
		return "![FAIL](https://img.shields.io/badge/compat-FAIL-red)"
	case s.OutputsExpected > 0:
		return "![WARN](https://img.shields.io/badge/compat-WARN-yellow)"
	default:
		return "![PASS](https://img.shields.io/badge/compat-PASS-brightgreen)"
	}
}

func markdownStatus(res OutputResult) string {
	if res.Error != "" {
		return ":x: ERROR"
	}
	switch res.Class {
	case DiffClassNone:
		return ":white_check_mark: PASS"
	case DiffClassExpected:
		return ":warning: EXPECTED"
	default:
		return ":x: FAIL"
	}
}

// mdEscape escapes pipe characters that would break Markdown table cells.
func mdEscape(s string) string {
	return strings.ReplaceAll(s, "|", "\\|")
}
