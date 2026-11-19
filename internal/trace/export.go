// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dotandev/glassbox/internal/abi"
	"github.com/dotandev/glassbox/internal/security"
)

type exportState struct {
	Step             int
	StepID           string
	Summary          string
	Operation        string
	EventType        string
	Contract         string
	Function         string
	ContractMetadata *abi.ContractMetadata
	Args             string
	Return           string
	Error            string
	SourceFile       string
	SourceLine       int
	SourceColumn     int
	ConfidenceLevel  string
	ConfidenceReason string
	GitHubLink       string
	CostSummary      string
	CostBreakdown    []string
	Details          []string
	// Comments holds the reviewer comments anchored to this step, so every
	// export format can render a comment next to what it is about.
	Comments []exportComment
}

// exportComment is the render-ready projection of a ReviewerComment. Times are
// pre-formatted and the target is pre-rendered so templates stay declarative.
type exportComment struct {
	ID         string
	Target     string
	Author     string
	Body       string
	Severity   string
	Resolution string
	CreatedAt  string
	UpdatedAt  string
	// DanglingReason is set only for comments in exportData.DanglingComments
	// and explains why the target could not be resolved.
	DanglingReason string
}

type exportData struct {
	TransactionHash string
	StartTime       string
	EndTime         string
	TotalSteps      int
	Annotations     TraceAnnotations
	States          []exportState
	// TraceComments are reviewer comments targeting the trace as a whole.
	TraceComments []exportComment
	// DanglingComments are reviewer comments whose target does not resolve
	// against this trace. They are rendered in their own section rather than
	// dropped, so a filtered or migrated export never loses review history.
	DanglingComments []exportComment
}

type ExportOptions struct {
	Comments        []string
	SessionMetadata map[string]string
	// ReviewerComments are merged into the trace's own reviewer comments for
	// this export only, matching how Comments and SessionMetadata behave.
	ReviewerComments []ReviewerComment
	// SecretScanMode controls whether secret scanning is enabled and how it behaves
	SecretScanMode security.ScannerMode
	// SecretScanOverrides are paths that are allowed to contain secrets (for test fixtures)
	SecretScanOverrides []string
}

const traceHTMLTemplate = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Glassbox Trace Export</title>
  <style>
    /* Issue #542: High-contrast support */
    @media (prefers-contrast: high) {
      body { background: #000 !important; color: #fff !important; }
      details { border-color: #fff !important; background: #111 !important; }
      .state-meta, .header p { color: #ccc !important; }
      code { background: #222 !important; color: #fff !important; }
      a { color: #66c !important; text-decoration: underline !important; }
    }
    /* Issue #542: Reduced motion */
    @media (prefers-reduced-motion: reduce) {
      * { animation: none !important; transition: none !important; scroll-behavior: auto !important; }
    }
    /* Issue #542: Focus indicators */
    summary:focus-visible { outline: 2px solid #60a5fa; outline-offset: 2px; }
    button:focus-visible { outline: 2px solid #60a5fa; outline-offset: 2px; }
    a:focus-visible { outline: 2px solid #60a5fa; outline-offset: 2px; }

    body { font-family: system-ui, sans-serif; margin: 0; padding: 1rem; background: #0b1220; color: #e8eef9; }
    .header { border-bottom: 1px solid #334155; padding-bottom: 1rem; margin-bottom: 1rem; }
    .header h1 { margin: 0 0 .25rem; font-size: 1.6rem; }
    .header p { margin: .25rem 0; color: #94a3b8; }
    .controls { margin: .75rem 0; }
    .controls button { margin-right: .5rem; padding: .5rem .75rem; border: none; border-radius: .375rem; cursor: pointer; background: #334155; color: #f8fafc; }
    details { margin-bottom: .75rem; border: 1px solid #334155; border-radius: .5rem; background: #111827; padding: .75rem; }
    summary { font-size: 1rem; font-weight: 700; cursor: pointer; }
    .state-meta { margin-top: .5rem; color: #cbd5e1; }
    .state-meta span { display: inline-block; margin-right: 1rem; }
    .field { margin: .35rem 0; }
    .field strong { color: #e2e8f0; }
    a { color: #60a5fa; }
    code { display: inline-block; background: #1e293b; padding: .15rem .3rem; border-radius: .25rem; }
    .comment { border-left: 3px solid #475569; background: #0f172a; padding: .5rem .75rem; margin: .5rem 0; border-radius: .25rem; }
    .comment.severity-warning { border-left-color: #f59e0b; }
    .comment.severity-critical { border-left-color: #ef4444; }
    .comment.resolution-resolved { opacity: .7; }
    .comment-head { color: #94a3b8; font-size: .85rem; margin-bottom: .35rem; }
    .comment-head .author { color: #e2e8f0; font-weight: 700; }
    .comment-body { white-space: pre-wrap; }
    .badge { display: inline-block; padding: .05rem .4rem; border-radius: .75rem; font-size: .75rem; text-transform: uppercase; letter-spacing: .03em; background: #334155; color: #e2e8f0; margin-right: .35rem; }
    .badge.severity-warning { background: #78350f; color: #fde68a; }
    .badge.severity-critical { background: #7f1d1d; color: #fecaca; }
    .badge.resolution-resolved { background: #14532d; color: #bbf7d0; }
    .badge.resolution-wontfix { background: #3f3f46; color: #d4d4d8; }
    .dangling { border-left-color: #ef4444; }
    .dangling .reason { color: #fca5a5; font-size: .85rem; margin-top: .35rem; }
  </style>
</head>
<body>
  <a href="#trace-content" class="sr-only" accesskey="s">Skip to trace content</a>
  <div class="header" role="banner">
    <h1>Glassbox Trace Export</h1>
    <p>Transaction: {{ .TransactionHash }}</p>
    <p>Steps: {{ .TotalSteps }} &middot; Started: {{ .StartTime }} &middot; Ended: {{ .EndTime }}</p>
    {{ if .Annotations.Comments }}
    <div class="field"><strong>Comments:</strong><ul>{{ range .Annotations.Comments }}<li>{{ . }}</li>{{ end }}</ul></div>
    {{ end }}
    {{ if .Annotations.SessionMetadata }}
    <div class="field"><strong>Session metadata:</strong><ul>{{ range $k, $v := .Annotations.SessionMetadata }}<li><code>{{ $k }}</code>: {{ $v }}</li>{{ end }}</ul></div>
    {{ end }}
    {{ if .TraceComments }}
    <div class="field"><strong>Reviewer comments on the whole trace:</strong>
      {{ range .TraceComments }}
      <div class="comment severity-{{ .Severity }} resolution-{{ .Resolution }}">
        <div class="comment-head">
          <span class="badge severity-{{ .Severity }}">{{ .Severity }}</span>
          <span class="badge resolution-{{ .Resolution }}">{{ .Resolution }}</span>
          <span class="author">{{ .Author }}</span> on <code>{{ .Target }}</code> · {{ .CreatedAt }}{{ if .UpdatedAt }} (edited {{ .UpdatedAt }}){{ end }}
        </div>
        <div class="comment-body">{{ .Body }}</div>
      </div>
      {{ end }}
    </div>
    {{ end }}
    {{ if .DanglingComments }}
    <div class="field"><strong>Reviewer comments with unresolved targets:</strong>
      {{ range .DanglingComments }}
      <div class="comment dangling severity-{{ .Severity }}">
        <div class="comment-head">
          <span class="badge severity-{{ .Severity }}">{{ .Severity }}</span>
          <span class="badge resolution-{{ .Resolution }}">{{ .Resolution }}</span>
          <span class="author">{{ .Author }}</span> on <code>{{ .Target }}</code> · {{ .CreatedAt }}
        </div>
        <div class="comment-body">{{ .Body }}</div>
        <div class="reason">Target not found in this trace: {{ .DanglingReason }}</div>
      </div>
      {{ end }}
    </div>
    {{ end }}
    <div class="controls">
      <button onclick="setAll(true)">Expand all</button>
      <button onclick="setAll(false)">Collapse all</button>
    </div>
  </div>
  <main id="trace-content" role="main" aria-label="Execution trace steps">
  <div role="tree" aria-label="Trace step tree">
  {{ range .States }}
  <details open>
    <summary>#{{ .Step }} · {{ .Summary }}{{ if .Comments }} · {{ len .Comments }} comment(s){{ end }}</summary>
    <div class="state-meta">
      <span><strong>Step ID:</strong> <code>{{ .StepID }}</code></span>
      <span><strong>Operation:</strong> {{ .Operation }}</span>
      {{ if .EventType }}<span><strong>Event:</strong> {{ .EventType }}</span>{{ end }}
      {{ if .Contract }}<span><strong>Contract:</strong> {{ .Contract }}</span>{{ end }}
      {{ if .Function }}<span><strong>Function:</strong> {{ .Function }}</span>{{ end }}
      {{ if .SourceFile }}<span><strong>Source:</strong> {{ .SourceFile }}:{{ .SourceLine }}</span>{{ end }}
      {{ if .ConfidenceLevel }}<span><strong>Confidence:</strong> {{ .ConfidenceLevel }} ({{ .ConfidenceReason }})</span>{{ end }}
      {{ if .GitHubLink }}<span><strong>Link:</strong> <a href="{{ .GitHubLink }}" target="_blank" rel="noopener" aria-label="View source on GitHub">View on GitHub</a></span>{{ end }}
    </div>
    <div class="field"><strong>Arguments:</strong> <code>{{ .Args }}</code></div>
    {{ if .Return }}<div class="field"><strong>Return:</strong> <code>{{ .Return }}</code></div>{{ end }}
    {{ if .Error }}<div class="field status-error" role="alert"><strong>Error:</strong> <code>{{ .Error }}</code></div>{{ end }}
    {{ if .CostSummary }}<div class="field"><strong>Cost:</strong> <code>{{ .CostSummary }}</code></div>{{ end }}
    {{ if .CostBreakdown }}
    <div class="field"><strong>Cost breakdown:</strong>
      <ul>
      {{ range .CostBreakdown }}<li>{{ . }}</li>{{ end }}
      </ul>
    </div>
    {{ end }}
    {{ if .Details }}
    <div class="field"><strong>Details:</strong>
      <ul>
      {{ range .Details }}<li>{{ . }}</li>{{ end }}
      </ul>
    </div>
    {{ end }}
    {{ if .Comments }}
    <div class="field"><strong>Reviewer comments:</strong>
      {{ range .Comments }}
      <div class="comment severity-{{ .Severity }} resolution-{{ .Resolution }}">
        <div class="comment-head">
          <span class="badge severity-{{ .Severity }}">{{ .Severity }}</span>
          <span class="badge resolution-{{ .Resolution }}">{{ .Resolution }}</span>
          <span class="author">{{ .Author }}</span> on <code>{{ .Target }}</code> · {{ .CreatedAt }}{{ if .UpdatedAt }} (edited {{ .UpdatedAt }}){{ end }}
        </div>
        <div class="comment-body">{{ .Body }}</div>
      </div>
      {{ end }}
    </div>
    {{ end }}
  </details>
  {{ end }}
  </div>
  </main>
  <script>
    // Issue #542: Keyboard navigation + focus management
    function setAll(open) {
      document.querySelectorAll('details[role="treeitem"]').forEach(function(element) {
        element.open = open;
        element.setAttribute('aria-expanded', open ? 'true' : 'false');
      });
    }
    document.querySelectorAll('details[role="treeitem"]').forEach(function(el) {
      el.addEventListener('toggle', function() {
        el.setAttribute('aria-expanded', el.open ? 'true' : 'false');
      });
    });
    var steps = document.querySelectorAll('details[role="treeitem"]');
    var currentFocus = 0;
    document.addEventListener('keydown', function(e) {
      if (e.key === 'ArrowDown' || e.key === 'ArrowRight') {
        e.preventDefault();
        currentFocus = Math.min(currentFocus + 1, steps.length - 1);
        steps[currentFocus].querySelector('summary').focus();
      } else if (e.key === 'ArrowUp' || e.key === 'ArrowLeft') {
        e.preventDefault();
        currentFocus = Math.max(currentFocus - 1, 0);
        steps[currentFocus].querySelector('summary').focus();
      } else if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        var el = steps[currentFocus];
        el.open = !el.open;
        el.setAttribute('aria-expanded', el.open ? 'true' : 'false');
      }
    });
  </script>
</body>
</html>`

// markdownTemplateFuncs supplies helpers the Markdown template cannot express
// literally. `tick` returns a backtick: the template is itself a raw string
// literal, so a literal backtick would terminate it.
var markdownTemplateFuncs = template.FuncMap{
	"tick": func() string { return "`" },
}

const traceMarkdownTemplate = `# Glassbox Trace Export

**Transaction:** {{ .TransactionHash }}

**Steps:** {{ .TotalSteps }}

**Started:** {{ .StartTime }}

**Ended:** {{ .EndTime }}

{{ if .Annotations.Comments }}## Comments
{{ range .Annotations.Comments }}- {{ . }}
{{ end }}
{{ end }}{{ if .Annotations.SessionMetadata }}## Session Metadata
{{ range $k, $v := .Annotations.SessionMetadata }}- **{{ $k }}:** {{ $v }}
{{ end }}
{{ end }}{{ if .TraceComments }}## Reviewer Comments (whole trace)
{{ range .TraceComments }}
- **{{ .Author }}** on {{ tick }}{{ .Target }}{{ tick }} — _{{ .Severity }}_, _{{ .Resolution }}_ ({{ .CreatedAt }}{{ if .UpdatedAt }}, edited {{ .UpdatedAt }}{{ end }})

  {{ .Body }}
{{ end }}
{{ end }}{{ if .DanglingComments }}## Reviewer Comments With Unresolved Targets

These comments are preserved but their target is not present in this trace.
{{ range .DanglingComments }}
- **{{ .Author }}** on {{ tick }}{{ .Target }}{{ tick }} — _{{ .Severity }}_, _{{ .Resolution }}_ ({{ .CreatedAt }})

  {{ .Body }}

  > Target not found in this trace: {{ .DanglingReason }}
{{ end }}
{{ end }}
{{ range .States }}
## Step {{ .Step }}: {{ .Summary }}

- **Step ID:** {{ tick }}{{ .StepID }}{{ tick }}
- **Operation:** {{ .Operation }}
{{ if .EventType }}- **Event:** {{ .EventType }}
{{ end }}{{ if .Contract }}- **Contract:** {{ .Contract }}
{{ end }}{{ if .Function }}- **Function:** {{ .Function }}
{{ end }}{{ if .SourceFile }}- **Source:** {{ .SourceFile }}:{{ .SourceLine }}
{{ end }}{{ if .ConfidenceLevel }}- **Confidence:** {{ .ConfidenceLevel }} ({{ .ConfidenceReason }})
{{ end }}{{ if .GitHubLink }}- **GitHub:** [View on GitHub]({{ .GitHubLink }})
{{ end }}- **Arguments:** {{ .Args }}
{{ if .Return }}- **Return:** {{ .Return }}
{{ end }}{{ if .Error }}- **Error:** {{ .Error }}
{{ end }}{{ if .CostSummary }}- **Cost:** {{ .CostSummary }}
{{ end }}{{ if .CostBreakdown }}- **Cost breakdown:**
  {{ range .CostBreakdown }}
  - {{ . }}
  {{ end }}
{{ end }}{{ if .Details }}- **Details:**
  {{ range .Details }}
  - {{ . }}
  {{ end }}
{{ end }}{{ if .Comments }}
### Reviewer comments on step {{ .Step }}
{{ range .Comments }}
- **{{ .Author }}** on {{ tick }}{{ .Target }}{{ tick }} — _{{ .Severity }}_, _{{ .Resolution }}_ ({{ .CreatedAt }}{{ if .UpdatedAt }}, edited {{ .UpdatedAt }}{{ end }})

  {{ .Body }}
{{ end }}
{{ end }}

{{ end }}`

// ValidateTraceExportParams performs comprehensive validation of trace export parameters.
// It checks the trace, format, output path, and export options for validity before export.
// Returns an error if any validation check fails, with clear and actionable guidance.
// All failures are collected and returned together so callers can fix them in one pass.
func ValidateTraceExportParams(trace *ExecutionTrace, format string, outputPath string, opts ExportOptions) error {
	var validationErrors []string

	// 1. Validate trace is not nil
	if trace == nil {
		validationErrors = append(validationErrors, "trace is nil — cannot export a nil trace\n"+
			"  Fix: ensure a valid trace object is provided to the export function\n"+
			"  This typically means the trace failed to load or deserialize correctly")
	} else {
		// 2. Validate trace has states (no steps recorded means nothing to export)
		if len(trace.States) == 0 {
			validationErrors = append(validationErrors, "trace has no execution states (no steps recorded) — empty trace cannot be exported\n"+
				"  Possible causes:\n"+
				"    - Simulation did not produce any diagnostic events\n"+
				"    - Transaction envelope is invalid\n"+
				"    - Simulator version is incompatible\n"+
				"  Fix: verify that the trace was captured correctly and contains at least one step\n"+
				"  Tip: check that the traced transaction actually executed any code")
		}

		// 3. Validate transaction hash is present
		if strings.TrimSpace(trace.TransactionHash) == "" {
			validationErrors = append(validationErrors, "trace has no transaction hash — transaction context is missing\n"+
				"  Fix: ensure the trace was created with a valid transaction hash\n"+
				"  This is usually set automatically when loading a trace from a file")
		}

		// 4. Validate time fields are sensible
		if trace.StartTime.IsZero() {
			validationErrors = append(validationErrors, "trace start time is zero — missing temporal context\n"+
				"  Fix: verify the trace was properly initialized with a start timestamp")
		}
		if trace.EndTime.IsZero() {
			validationErrors = append(validationErrors, "trace end time is zero — missing temporal context\n"+
				"  Fix: verify the trace was properly finalized with an end timestamp")
		}
		if !trace.StartTime.IsZero() && !trace.EndTime.IsZero() && trace.EndTime.Before(trace.StartTime) {
			validationErrors = append(validationErrors, "trace end time is before start time — invalid temporal ordering\n"+
				"  Fix: verify the trace timestamps were recorded correctly\n"+
				"  Start: "+trace.StartTime.String()+", End: "+trace.EndTime.String())
		}
	}

	// 5. Validate format string
	if strings.TrimSpace(format) == "" {
		validationErrors = append(validationErrors, "--export-format is empty (format is empty) — must specify a format\n"+
			"  Fix: use --export-format with one of: html, markdown, json, text\n"+
			"  Default is html if not specified during export")
	} else {
		normalizedFormat := strings.ToLower(strings.TrimSpace(format))
		validFormats := map[string]bool{"md": true}
		for _, f := range SupportedExportFormats() {
			validFormats[f] = true
		}
		if !validFormats[normalizedFormat] {
			validationErrors = append(validationErrors, fmt.Sprintf(
				"invalid or unsupported --export-format %q — must be one of: html, markdown, json, text\n"+
					"  Fix: use a supported format\n"+
					"  html     — interactive HTML (best for browsers)\n"+
					"  markdown — GitHub-friendly markdown report\n"+
					"  json     — machine-readable JSON\n"+
					"  text     — plain text ASCII output",
				format))
		}
	}

	// 6. Validate output path
	if strings.TrimSpace(outputPath) == "" {
		validationErrors = append(validationErrors, "output path is empty — must specify a target file\n"+
			"  Fix: provide an output file path (e.g., ./trace.html or /tmp/report.md)\n"+
			"  Example: glassbox trace --export ./output/trace.html --format html input.json")
	} else {
		// Check for directory-like paths (ending with / or \)
		if strings.HasSuffix(outputPath, "/") || strings.HasSuffix(outputPath, "\\") {
			validationErrors = append(validationErrors, fmt.Sprintf(
				"output path %q looks like a directory path (ends with %q); provide a full file path\n"+
					"  Fix: specify a complete filename\n"+
					"  Example: --export ./output/trace.html instead of --export ./output/",
				outputPath, string(outputPath[len(outputPath)-1])))
		}

		// Null bytes in paths are a shell-injection risk.
		if strings.ContainsRune(outputPath, 0) {
			validationErrors = append(validationErrors, "output path contains null bytes — invalid file path\n"+
				"  Fix: remove any null bytes from the path")
		}

		// Check parent directory viability (can surface permission issues early)
		parentDir := filepath.Dir(outputPath)
		if parentDir != "." && parentDir != "" {
			if info, err := os.Stat(parentDir); err != nil {
				if os.IsPermission(err) {
					validationErrors = append(validationErrors, fmt.Sprintf(
						"output directory %q is not accessible — permission denied\n"+
							"  Fix: ensure you have read and execute permissions on the parent directory\n"+
							"  Check: ls -ld %s", parentDir, parentDir))
				}
				// Non-existent parent directory is OK — created at export time.
			} else if !info.IsDir() {
				validationErrors = append(validationErrors, fmt.Sprintf(
					"output path parent %q exists but is not a directory — invalid path\n"+
						"  Fix: provide a path where the parent is a directory\n"+
						"  Check: ls -l %s", parentDir, parentDir))
			}
		}
	}

	// 7. Validate ExportOptions.Comments count and length
	if len(opts.Comments) > MaxTraceComments {
		validationErrors = append(validationErrors, fmt.Sprintf(
			"too many comments (%d) — maximum is %d comments per trace export\n"+
				"  Fix: reduce the number of comments or split into multiple exports",
			len(opts.Comments), MaxTraceComments,
		))
	}
	for i, comment := range opts.Comments {
		if len(comment) > MaxCommentLength {
			validationErrors = append(validationErrors, fmt.Sprintf(
				"comment #%d exceeds maximum length of %d characters (got %d)\n"+
					"  Fix: shorten the comment or split it into multiple shorter comments",
				i+1, MaxCommentLength, len(comment),
			))
		} else if strings.TrimSpace(comment) == "" {
			validationErrors = append(validationErrors, fmt.Sprintf(
				"--comment index %d is empty or whitespace-only\n"+
					"  Fix: provide non-empty comments or omit empty ones", i))
		}
	}

	// 7b. Validate reviewer comments — both the ones already on the trace and
	// the ones supplied for this export, since either can push the artifact
	// past the limits. Structural problems are reported per comment so a
	// single malformed entry does not hide the rest.
	existing := 0
	if trace != nil {
		existing = len(trace.Annotations.ReviewerComments)
	}
	if total := existing + len(opts.ReviewerComments); total > MaxTraceComments {
		validationErrors = append(validationErrors, fmt.Sprintf(
			"too many reviewer comments (%d) — maximum is %d per trace export\n"+
				"  The trace carries %d and this export adds %d\n"+
				"  Fix: resolve and remove obsolete comments, or split the review across exports",
			total, MaxTraceComments, existing, len(opts.ReviewerComments),
		))
	}
	for i, comment := range opts.ReviewerComments {
		normalized := comment
		normalized.Normalize()
		if err := normalized.Validate(); err != nil {
			validationErrors = append(validationErrors, fmt.Sprintf(
				"reviewer comment #%d is invalid: %s", i+1, err.Error()))
		}
	}

	// 8. Validate ExportOptions.SessionMetadata keys and values
	for key, value := range opts.SessionMetadata {
		if strings.TrimSpace(key) == "" {
			validationErrors = append(validationErrors, "session metadata key is empty or whitespace-only\n"+
				"  Fix: provide non-empty keys for all metadata entries")
		}
		if strings.TrimSpace(value) == "" {
			validationErrors = append(validationErrors, fmt.Sprintf(
				"session metadata value for key %q is empty or whitespace-only\n"+
					"  Fix: provide non-empty values or omit the metadata entry", key))
		}
	}

	// Return aggregated errors if any
	if len(validationErrors) > 0 {
		if len(validationErrors) == 1 {
			return fmt.Errorf("%s", validationErrors[0])
		}
		msg := fmt.Sprintf("%d trace export validation error(s):\n", len(validationErrors))
		for i, err := range validationErrors {
			msg += fmt.Sprintf("  %d. %s\n", i+1, err)
		}
		return fmt.Errorf("%s", strings.TrimRight(msg, "\n"))
	}

	return nil
}

// ValidateTraceFormatCompatibility checks if the trace data is suitable for the target export format.
// Some formats have specific requirements or may produce suboptimal results with certain trace data.
// Returns an error if the trace is fundamentally incompatible with the format, or nil if compatible.
func ValidateTraceFormatCompatibility(trace *ExecutionTrace, format string) error {
	if trace == nil {
		return fmt.Errorf("trace is nil — cannot check format compatibility")
	}

	normalizedFormat := strings.ToLower(strings.TrimSpace(format))

	// Format-specific validation checks
	switch normalizedFormat {
	case "html":
		// HTML format handles most traces well, but check for problematic sizes
		// Large traces may cause browser rendering issues
		if len(trace.States) > 50000 {
			return fmt.Errorf(
				"trace has %d steps — too large for HTML export (browser may become unresponsive)\n"+
					"  Fix: use --format json for large traces or filter the trace verbosity\n"+
					"  Alternatively: use --trace-verbosity summary to reduce output size",
				len(trace.States))
		}

		// Check for extremely large step details that could break HTML rendering.
		// This covers both long error/operation strings and very large argument lists.
		maxDetailSize := 1000000 // 1MB
		for i, state := range trace.States {
			detailSize := len(state.Error) + len(state.Operation) + len(state.Function)
			if detailSize > maxDetailSize {
				return fmt.Errorf(
					"trace step %d has excessively large detail fields (%d bytes total) — incompatible with HTML export\n"+
						"  Fix: use --format json for traces with large step details",
					i, detailSize)
			}
			// Arguments are rendered as a single string in HTML; check their
			// serialised size so the browser doesn't receive a multi-megabyte payload.
			argStr := fmt.Sprintf("%v", state.Arguments)
			if len(argStr) > 50000 {
				return fmt.Errorf(
					"trace step %d has very large arguments (%d chars) that may cause browser rendering issues — incompatible with HTML export\n"+
						"  Fix: use --format json for traces with large argument payloads",
					i, len(argStr))
			}
		}

	case "markdown", "md":
		// Markdown format requires careful handling of special characters
		if len(trace.States) > 10000 {
			return fmt.Errorf(
				"trace has %d steps — markdown output will be extremely long (>1MB) and difficult to view\n"+
					"  Fix: use --format json for large traces or filter the trace verbosity",
				len(trace.States))
		}

		// Check for problematic markdown characters in error messages
		for i, state := range trace.States {
			if strings.Count(state.Error, "```") > 0 {
				return fmt.Errorf(
					"trace step %d error contains markdown code fence markers (`) — may break markdown formatting\n"+
						"  This is usually OK and will be handled gracefully, but you may want to review the step details",
					i)
			}
		}

	case "json":
		// JSON format is most flexible, but validate structural correctness
		// that would prevent successful serialization or round-trip loading.

		// Step indices must be sequential — a mismatch indicates a corrupted
		// or externally-modified trace that cannot be reloaded reliably.
		for i, state := range trace.States {
			if state.Step != i {
				return fmt.Errorf(
					"trace step mismatch at position %d: expected step %d but got %d — trace may be corrupted\n"+
						"  Fix: re-export or sanitize the trace before converting to JSON\n"+
						"  Tip: use ExportWithResilience to auto-repair step indices",
					i, i, state.Step)
			}
		}

	case "text":
		// Plain text format is permissive but may produce very large files
		if len(trace.States) > 100000 {
			return fmt.Errorf(
				"trace has %d steps — plain text export will be extremely large (likely >5MB) and slow to generate\n"+
					"  Fix: use --format json for very large traces or filter the trace verbosity",
				len(trace.States))
		}

	default:
		return fmt.Errorf(
			"unsupported trace export format: %q\n"+
				"  Supported formats: html, markdown, json, text\n"+
				"  Fix: use --export-format with one of the supported values",
			format)
	}

	return nil
}

// SupportedExportFormats returns the canonical list of trace export formats.
// "md" is accepted as an alias for "markdown" but is not listed separately.
//
// Adding a format here requires wiring it into ExportExecutionTraceWithOptions,
// ValidateTraceFormatCompatibility, and the printer golden tests
// (internal/trace/printer_golden_test.go) — the golden coverage test fails
// until every listed format has checked-in golden fixtures.
func SupportedExportFormats() []string {
	return []string{"html", "markdown", "json", "text"}
}

func ExportExecutionTrace(trace *ExecutionTrace, format string, outputPath string) error {
	return ExportExecutionTraceWithOptions(trace, format, outputPath, ExportOptions{})
}

func ExportExecutionTraceWithOptions(trace *ExecutionTrace, format string, outputPath string, opts ExportOptions) error {
	// Comprehensive pre-flight validation
	if err := ValidateTraceExportParams(trace, format, outputPath, opts); err != nil {
		return fmt.Errorf("trace export validation failed: %w", err)
	}

	// Format compatibility check
	if err := ValidateTraceFormatCompatibility(trace, format); err != nil {
		return fmt.Errorf("trace format compatibility check failed: %w", err)
	}

	// Accuracy and context audit — surface warnings before writing the file
	// so users can see degraded-accuracy conditions without the export failing.
	if err := ValidateTraceAccuracy(trace); err != nil {
		if te, ok := err.(*TraceInputError); ok {
			for _, f := range te.Failures {
				fmt.Fprintf(os.Stderr, "Warning: %s\n", f)
			}
		}
	}

	// Secret scanning — detect and optionally block exports containing secrets
	if opts.SecretScanMode != "" {
		scanner := security.NewSecretScanner(opts.SecretScanMode)
		for _, override := range opts.SecretScanOverrides {
			scanner.AddOverride(override)
		}

		// Scan session metadata
		if len(opts.SessionMetadata) > 0 {
			result := scanner.ScanMap(opts.SessionMetadata, "session_metadata")
			if result.HasSecrets {
				if scanner.ShouldBlockExport(result) {
					return fmt.Errorf(scanner.GetErrorMessage(result))
				}
				fmt.Fprintf(os.Stderr, "Warning: %s\n", scanner.GetErrorMessage(result))
			}
		}

		// Scan comments
		for i, comment := range opts.Comments {
			location := fmt.Sprintf("comments[%d]", i)
			finding := scanner.ScanString(comment, location)
			if finding.Type != "" {
				result := security.ScanResult{
					Findings:   []security.SecretFinding{finding},
					HasSecrets: true,
				}
				if scanner.ShouldBlockExport(result) {
					return fmt.Errorf(scanner.GetErrorMessage(result))
				}
				fmt.Fprintf(os.Stderr, "Warning: %s\n", scanner.GetErrorMessage(result))
			}
		}

		// Scan reviewer comments
		for i, comment := range opts.ReviewerComments {
			location := fmt.Sprintf("reviewer_comments[%d].body", i)
			finding := scanner.ScanString(comment.Body, location)
			if finding.Type != "" {
				result := security.ScanResult{
					Findings:   []security.SecretFinding{finding},
					HasSecrets: true,
				}
				if scanner.ShouldBlockExport(result) {
					return fmt.Errorf(scanner.GetErrorMessage(result))
				}
				fmt.Fprintf(os.Stderr, "Warning: %s\n", scanner.GetErrorMessage(result))
			}
		}
	}

	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" {
		format = "html"
	}

	var content string
	var err error
	switch format {
	case "html":
		content, err = GenerateTraceHTMLWithOptions(trace, opts)
		if err != nil {
			return fmt.Errorf("failed to generate HTML trace: %w\n"+
				"  This may indicate invalid trace data or a template rendering error\n"+
				"  Check that all trace fields are properly populated", err)
		}
	case "markdown", "md":
		content, err = GenerateTraceMarkdownWithOptions(trace, opts)
		if err != nil {
			return fmt.Errorf("failed to generate Markdown trace: %w\n"+
				"  This may indicate invalid trace data or a template rendering error", err)
		}
	case "json":
		// For JSON, we marshal the trace directly
		jsonBytes, jsonErr := json.MarshalIndent(trace, "", "  ")
		if jsonErr != nil {
			return fmt.Errorf("failed to marshal trace as JSON: %w\n"+
				"  This indicates the trace contains non-serializable data\n"+
				"  Check for circular references or invalid field values", jsonErr)
		}
		content = string(jsonBytes)
	case "text":
		content, err = GenerateTracePlainTextWithOptions(trace, opts)
		if err != nil {
			return fmt.Errorf("failed to generate plain text trace: %w", err)
		}
	default:
		return fmt.Errorf("unsupported trace export format: %s\n"+
			"  Supported formats: html, markdown, json, text\n"+
			"  Fix: use --format with one of the supported values", format)
	}
	if err != nil {
		return err
	}

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return fmt.Errorf("failed to create trace export directory: %w\n"+
			"  Directory: %s\n"+
			"  Fix: ensure you have write permissions to the parent directory\n"+
			"  Or choose a different output path with --trace-output", err, filepath.Dir(outputPath))
	}

	// Write the file
	if err := os.WriteFile(outputPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("failed to write trace export file: %w\n"+
			"  Path: %s\n"+
			"  Fix: ensure you have write permissions and sufficient disk space\n"+
			"  Check: ls -la %s", err, outputPath, filepath.Dir(outputPath))
	}

	return nil
}

func GenerateTraceHTML(trace *ExecutionTrace) (string, error) {
	return GenerateTraceHTMLWithOptions(trace, ExportOptions{})
}

func GenerateTraceHTMLWithOptions(trace *ExecutionTrace, opts ExportOptions) (string, error) {
	if trace == nil {
		return "", fmt.Errorf("trace is nil")
	}
	data := buildExportData(trace, opts)

	tmpl, err := template.New("trace-html").Parse(traceHTMLTemplate)
	if err != nil {
		return "", fmt.Errorf("failed to parse trace export template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to render trace HTML: %w", err)
	}
	return buf.String(), nil
}

func GenerateTraceMarkdown(trace *ExecutionTrace) (string, error) {
	return GenerateTraceMarkdownWithOptions(trace, ExportOptions{})
}

func GenerateTraceMarkdownWithOptions(trace *ExecutionTrace, opts ExportOptions) (string, error) {
	if trace == nil {
		return "", fmt.Errorf("trace is nil")
	}
	data := buildExportData(trace, opts)

	// The template itself is a backtick-delimited raw string, so Markdown code
	// spans cannot be written literally inside it; `tick` emits the backtick.
	tmpl, err := template.New("trace-md").Funcs(markdownTemplateFuncs).Parse(traceMarkdownTemplate)
	if err != nil {
		return "", fmt.Errorf("failed to parse trace export template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to render trace markdown: %w", err)
	}
	return buf.String(), nil
}

// GenerateTracePlainText renders a shareable plain-text trace with indented hierarchy.
func GenerateTracePlainText(trace *ExecutionTrace) (string, error) {
	return GenerateTracePlainTextWithOptions(trace, ExportOptions{})
}

// GenerateTracePlainTextWithOptions renders a plain-text trace including any
// comments and session metadata, matching the semantic coverage of the HTML
// and Markdown exporters (previously the text format silently dropped
// annotations).
func GenerateTracePlainTextWithOptions(trace *ExecutionTrace, opts ExportOptions) (string, error) {
	if trace == nil {
		return "", fmt.Errorf("trace is nil")
	}
	annotations := mergeTraceAnnotations(trace.Annotations, opts)
	byStep, traceLevel, dangling := buildExportComments(trace, annotations)

	data := exportData{
		TransactionHash:  trace.TransactionHash,
		StartTime:        trace.StartTime.Format(time.RFC3339),
		EndTime:          trace.EndTime.Format(time.RFC3339),
		TotalSteps:       len(trace.States),
		Annotations:      annotations,
		States:           buildExportStates(trace, byStep),
		TraceComments:    traceLevel,
		DanglingComments: dangling,
	}

	var buf strings.Builder
	fmt.Fprintf(&buf, "Glassbox Trace Export\n")
	fmt.Fprintf(&buf, "=====================\n\n")
	fmt.Fprintf(&buf, "Transaction: %s\n", data.TransactionHash)
	fmt.Fprintf(&buf, "Steps:       %d\n", data.TotalSteps)
	fmt.Fprintf(&buf, "Started:     %s\n", data.StartTime)
	fmt.Fprintf(&buf, "Ended:       %s\n\n", data.EndTime)

	if len(data.Annotations.Comments) > 0 {
		buf.WriteString("Comments:\n")
		for _, c := range data.Annotations.Comments {
			fmt.Fprintf(&buf, "  - %s\n", c)
		}
		buf.WriteString("\n")
	}
	if len(data.Annotations.SessionMetadata) > 0 {
		keys := make([]string, 0, len(data.Annotations.SessionMetadata))
		for k := range data.Annotations.SessionMetadata {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteString("Session metadata:\n")
		for _, k := range keys {
			fmt.Fprintf(&buf, "  - %s: %s\n", k, data.Annotations.SessionMetadata[k])
		}
		buf.WriteString("\n")
	}

	for _, s := range data.States {
		fmt.Fprintf(&buf, "Step %d: %s\n", s.Step, s.Summary)
		fmt.Fprintf(&buf, "  Step ID:   %s\n", s.StepID)
		fmt.Fprintf(&buf, "  Operation: %s\n", s.Operation)
		if s.EventType != "" {
			fmt.Fprintf(&buf, "  Event:     %s\n", s.EventType)
		}
		if s.Contract != "" {
			fmt.Fprintf(&buf, "  Contract:  %s\n", s.Contract)
		}
		if s.Function != "" {
			fmt.Fprintf(&buf, "  Function:  %s\n", s.Function)
		}
		if s.SourceFile != "" {
			fmt.Fprintf(&buf, "  Source:    %s:%d\n", s.SourceFile, s.SourceLine)
		}
		if s.ConfidenceLevel != "" {
			fmt.Fprintf(&buf, "  Confidence: %s (%s)\n", s.ConfidenceLevel, s.ConfidenceReason)
		}
		if s.GitHubLink != "" {
			fmt.Fprintf(&buf, "  GitHub:    %s\n", s.GitHubLink)
		}
		fmt.Fprintf(&buf, "  Arguments: %s\n", s.Args)
		if s.Return != "" && s.Return != "<nil>" {
			fmt.Fprintf(&buf, "  Return:    %s\n", s.Return)
		}
		if s.Error != "" {
			fmt.Fprintf(&buf, "  Error:     %s\n", s.Error)
		}
		if s.CostSummary != "" {
			fmt.Fprintf(&buf, "  Cost:      %s\n", s.CostSummary)
		}
		for _, line := range s.CostBreakdown {
			fmt.Fprintf(&buf, "    * %s\n", line)
		}
		for _, detail := range s.Details {
			fmt.Fprintf(&buf, "    - %s\n", detail)
		}
		if len(s.Comments) > 0 {
			buf.WriteString("  Reviewer comments:\n")
			for _, c := range s.Comments {
				writePlainTextComment(&buf, "    ", c)
			}
		}
		buf.WriteString("\n")
	}

	if len(data.TraceComments) > 0 {
		buf.WriteString("Reviewer comments (whole trace):\n")
		for _, c := range data.TraceComments {
			writePlainTextComment(&buf, "  ", c)
		}
		buf.WriteString("\n")
	}

	if len(data.DanglingComments) > 0 {
		buf.WriteString("Reviewer comments with unresolved targets:\n")
		for _, c := range data.DanglingComments {
			writePlainTextComment(&buf, "  ", c)
			fmt.Fprintf(&buf, "    Target not found in this trace: %s\n", c.DanglingReason)
		}
		buf.WriteString("\n")
	}

	return buf.String(), nil
}

// writePlainTextComment renders one comment with its target, so the plain-text
// export associates a comment with what it is about just as clearly as the
// HTML and Markdown formats do.
func writePlainTextComment(buf *strings.Builder, indent string, c exportComment) {
	fmt.Fprintf(buf, "%s- [%s/%s] %s on %s (%s", indent, c.Severity, c.Resolution, c.Author, c.Target, c.CreatedAt)
	if c.UpdatedAt != "" {
		fmt.Fprintf(buf, ", edited %s", c.UpdatedAt)
	}
	buf.WriteString(")\n")
	for _, line := range strings.Split(c.Body, "\n") {
		fmt.Fprintf(buf, "%s    %s\n", indent, line)
	}
}

// buildExportStates projects the trace's states into render-ready form.
// commentsByStep is keyed by state index and may be nil when the trace carries
// no reviewer comments.
func buildExportStates(trace *ExecutionTrace, commentsByStep map[int][]exportComment) []exportState {
	states := make([]exportState, 0, len(trace.States))
	for i, s := range trace.States {
		details := make([]string, 0)
		if s.Error != "" {
			details = append(details, fmt.Sprintf("error: %s", s.Error))
		}
		if s.Operation != "" {
			details = append(details, fmt.Sprintf("operation: %s", s.Operation))
		}
		if s.ContractID != "" {
			details = append(details, fmt.Sprintf("contract: %s", s.ContractID))
		}
		if s.Function != "" {
			details = append(details, fmt.Sprintf("function: %s", s.Function))
		}
		if s.WasmInstruction != "" {
			details = append(details, fmt.Sprintf("wasm instruction: %s", s.WasmInstruction))
		}
		if len(s.Arguments) > 0 {
			details = append(details, fmt.Sprintf("arguments: %v", s.Arguments))
		}
		if s.RawArguments != nil && len(s.RawArguments) > 0 {
			details = append(details, fmt.Sprintf("raw arguments: %v", s.RawArguments))
		}
		if s.HostState != nil {
			details = append(details, fmt.Sprintf("host state entries: %d", len(s.HostState)))
		}
		if s.Memory != nil {
			details = append(details, fmt.Sprintf("memory entries: %d", len(s.Memory)))
		}
		if s.Cost != nil {
			details = append(details, fmt.Sprintf("cost: %s", FormatCostAnnotation(s.Cost)))
		}

		// A nil return value must render as "absent" in every format; formatting
		// it with %v would leak a literal "<nil>" into Markdown and HTML output
		// while the text exporter suppresses it.
		returnStr := ""
		if s.ReturnValue != nil {
			returnStr = fmt.Sprintf("%v", s.ReturnValue)
		}

		summary := s.Operation
		if summary == "" {
			summary = s.EventType
		}
		if summary == "" && s.ContractID != "" {
			summary = s.ContractID
		}
		if summary == "" {
			summary = fmt.Sprintf("step %d", s.Step)
		}

		states = append(states, exportState{
			Step:             s.Step,
			StepID:           StepIDOf(&trace.States[i]),
			Summary:          summary,
			Operation:        s.Operation,
			EventType:        s.EventType,
			Contract:         s.ContractID,
			Function:         s.Function,
			ContractMetadata: s.ContractMetadata,
			Args:             fmt.Sprintf("%v", s.Arguments),
			Return:           returnStr,
			Error:            s.Error,
			SourceFile:       s.SourceFile,
			SourceLine:       s.SourceLine,
			SourceColumn:     s.SourceColumn,
			ConfidenceLevel:  s.ConfidenceLevel,
			ConfidenceReason: s.ConfidenceReason,
			GitHubLink:       s.GitHubLink,
			CostSummary:      FormatCostAnnotation(s.Cost),
			CostBreakdown:    FormatCostBreakdown(s.Cost),
			Details:          details,
			Comments:         commentsByStep[i],
		})
	}
	return states
}

// renderComment projects a ReviewerComment into its render-ready form.
func renderComment(c ReviewerComment) exportComment {
	out := exportComment{
		ID:         c.ID,
		Target:     c.Target.String(),
		Author:     c.Author,
		Body:       c.Body,
		Severity:   string(c.Severity),
		Resolution: string(c.Resolution),
		CreatedAt:  c.CreatedAt.UTC().Format(time.RFC3339),
	}
	if out.Severity == "" {
		out.Severity = string(DefaultAnnotationSeverity)
	}
	if out.Resolution == "" {
		out.Resolution = string(DefaultAnnotationResolution)
	}
	if !c.UpdatedAt.IsZero() {
		out.UpdatedAt = c.UpdatedAt.UTC().Format(time.RFC3339)
	}
	return out
}

// buildExportComments resolves the annotations against the trace and groups
// them the way every export format needs them: comments anchored to a step
// (keyed by state index), comments about the whole trace, and comments whose
// target no longer resolves.
//
// Dangling comments are returned for rendering rather than discarded — an
// export of a filtered or migrated trace must still carry the full review
// history, with the broken anchor made visible.
func buildExportComments(trace *ExecutionTrace, annotations TraceAnnotations) (byStep map[int][]exportComment, traceLevel, dangling []exportComment) {
	ordered := SortReviewerComments(trace, annotations.ReviewerComments)
	if len(ordered) == 0 {
		return nil, nil, nil
	}

	report := ValidateAnnotationRefs(trace, ordered)
	byStep = make(map[int][]exportComment)
	for _, r := range report.Resolved {
		rendered := renderComment(r.Comment)
		if r.StepIndex < 0 {
			traceLevel = append(traceLevel, rendered)
			continue
		}
		byStep[r.StepIndex] = append(byStep[r.StepIndex], rendered)
	}
	for _, d := range report.Dangling {
		rendered := renderComment(d.Comment)
		rendered.DanglingReason = d.Reason
		dangling = append(dangling, rendered)
	}
	return byStep, traceLevel, dangling
}

// buildExportData assembles the full render model shared by the HTML,
// Markdown, and plain-text exporters.
func buildExportData(trace *ExecutionTrace, opts ExportOptions) exportData {
	annotations := mergeTraceAnnotations(trace.Annotations, opts)
	byStep, traceLevel, dangling := buildExportComments(trace, annotations)

	return exportData{
		TransactionHash:  trace.TransactionHash,
		StartTime:        trace.StartTime.Format(time.RFC3339),
		EndTime:          trace.EndTime.Format(time.RFC3339),
		TotalSteps:       len(trace.States),
		Annotations:      annotations,
		States:           buildExportStates(trace, byStep),
		TraceComments:    traceLevel,
		DanglingComments: dangling,
	}
}

func mergeTraceAnnotations(base TraceAnnotations, opts ExportOptions) TraceAnnotations {
	out := base
	if len(opts.Comments) > 0 {
		out.Comments = append(append([]string(nil), base.Comments...), opts.Comments...)
	}
	if len(opts.ReviewerComments) > 0 {
		// Matched by ID so an export-time comment updates rather than
		// duplicates one already carried by the trace.
		merged := append([]ReviewerComment(nil), base.ReviewerComments...)
		position := make(map[string]int, len(merged))
		for i, c := range merged {
			position[c.ID] = i
		}
		for _, c := range opts.ReviewerComments {
			c.Normalize()
			if i, ok := position[c.ID]; ok {
				merged[i] = c
				continue
			}
			position[c.ID] = len(merged)
			merged = append(merged, c)
		}
		out.ReviewerComments = merged
	}
	if len(opts.SessionMetadata) > 0 {
		merged := make(map[string]string, len(base.SessionMetadata)+len(opts.SessionMetadata))
		for k, v := range base.SessionMetadata {
			merged[k] = v
		}
		for k, v := range opts.SessionMetadata {
			merged[k] = v
		}
		out.SessionMetadata = merged
	}
	if out.GeneratedAt.IsZero() && (len(out.Comments) > 0 || len(out.SessionMetadata) > 0) {
		out.GeneratedAt = time.Now()
	}
	return out
}
