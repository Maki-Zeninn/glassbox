// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/dotandev/glassbox/internal/decoder"
	"github.com/dotandev/glassbox/internal/diagnostics"
	"github.com/dotandev/glassbox/internal/errors"
	"github.com/dotandev/glassbox/internal/gasmodel"
	"github.com/dotandev/glassbox/internal/security"
	"github.com/dotandev/glassbox/internal/termctx"
	"github.com/dotandev/glassbox/internal/trace"
	"github.com/dotandev/glassbox/internal/visualizer"
	"github.com/spf13/cobra"
)

var (
	traceFile                  string
	traceThemeFlag             string
	tracePrint                 bool
	traceNoColor               bool
	traceExportSVG             string
	traceOutputJSON            string
	traceExportPath            string
	traceExportFormat          string
	traceExportMarkdown        string
	traceAnnotationsFlag       string
	traceAnnotationsExportPath string
	traceAnnotationsStrict     bool
	traceBookmarksOnConflict   string
	traceBookmarksPreview      bool
	traceGasModelPath          string
	traceComments              []string
	traceMetadata              []string
	traceVerbosity             string
	traceDryRunFlag            bool
	traceShowTimingFlag        bool
	traceTimingsFlag           bool // --timings: structured phase timing via diagnostics
	traceForceFlag             bool
	traceFormatAlias           string // --format is a user-friendly alias for --export-format
	traceVerifyExportFlag      string // --verify-export: verify integrity of an existing export file

	// Secret scanning flags
	secretScanModeFlag       string
	secretScanOverrideFlag   []string

	// Secret scanning flags
	secretScanModeFlag       string
	secretScanOverrideFlag   []string

	// Secret scanning flags
	secretScanModeFlag       string
	secretScanOverrideFlag   []string

	// Secret scanning flags
	secretScanModeFlag       string
	secretScanOverrideFlag   []string

	// eventSchemas is optionally populated by other subsystems (e.g. schema
	// loading) before the trace command runs. Nil is safe — PrintExecutionTrace
	// handles the absence gracefully.
	eventSchemas *trace.EventSchemaSet
)

// validTraceExportFormats lists the formats accepted by --export-format / --format.
var validTraceExportFormats = map[string]bool{
	"html":     true,
	"markdown": true,
	"md":       true,
	"json":     true,
	"text":     true,
}

var traceCmd = &cobra.Command{
	Use:     "trace <trace-file>",
	GroupID: "core",
	Short:   "Interactive trace navigation and debugging",
	Long: `Launch an interactive trace viewer for bi-directional navigation through execution traces.

The trace viewer allows you to:
- Step forward and backward through execution
- Jump to specific steps
- Reconstruct state at any point
- View memory and host state changes

Use --print for a one-shot, colour-coded ASCII tree report suitable for CI
logs or piping to other tools. Add --no-color to disable ANSI colours.

Export formats (--export / --format):
  html      — interactive HTML page, best for manual analysis in a browser
  markdown  — shareable Markdown report, best for issues and chat
  json      — machine-readable JSON, best for CI/CD and automated processing
  text      — plain text, best for simple logging or piping

Reviewer comments:
  Use --annotations to import a JSON file of reviewer comments and
  --export-annotations to write them back out. Comments carry an author,
  severity, and resolution state, and are anchored to a stable step ID or a
  source location, so exports show each comment next to the step it is about.
  Comments whose target is missing from the trace are reported as dangling and
  still exported — add --annotations-strict to make that a hard failure.

Performance notes:
  Large traces (>5 000 steps) can produce slow HTML rendering.
  Use --format json for large traces or CI pipelines.
  Use --trace-verbosity summary to reduce output size significantly.
  Use --dry-run to validate parameters without writing any files.

Export verification:
  Use --verify-export <file> to check the integrity of an existing export.
  The command verifies the checksum, step count, schema version recorded in
  the companion .meta.json file, and that the file extension matches the
  declared format.  No trace file argument is required in this mode.`,
	Example: `  # Open the interactive trace viewer
  glassbox trace execution.json

  # Validate parameters without writing any output (dry-run)
  glassbox trace --dry-run --export trace.html execution.json

  # Export trace as interactive HTML
  glassbox trace --export trace.html --format html execution.json

  # Export as JSON (best for large traces and CI pipelines)
  glassbox trace --export trace.json --format json execution.json

  # Export as markdown for sharing in chat or issue trackers
  glassbox trace --export trace.md --format markdown execution.json

  # Export as plain text
  glassbox trace --export trace.txt --format text execution.json

  # Print a colour-coded ASCII tree and exit (suitable for CI logs)
  glassbox trace --print execution.json

  # Print with timing info
  glassbox trace --print --show-timing execution.json

  # Export call graph as SVG
  glassbox trace --export-svg callgraph.svg execution.json

  # Export with comments and session metadata
  glassbox trace --export report.md --format markdown \
    --comment "Reviewed with Alice" --meta env=testnet execution.json

  # Import reviewer comments and render them next to the steps they target
  glassbox trace --annotations review.json --export report.md --format markdown execution.json

  # Export reviewer comments to a portable file to hand to another reviewer
  glassbox trace --export-annotations review.json execution.json

  # Fail the run if any imported comment targets a step that is not in the trace
  glassbox trace --annotations review.json --annotations-strict execution.json

  # Force overwrite of an existing output file
  glassbox trace --export trace.html --force execution.json

  # Verify the integrity of an existing export (checksum, step count, schema version)
  glassbox trace --verify-export trace.json

  # Verify-export exits with a non-zero code when integrity checks fail (suitable for CI)
  glassbox trace --verify-export ./artifacts/trace.json && echo "OK"`,
	Args: cobra.MaximumNArgs(1),
	PreRunE: func(cmd *cobra.Command, args []string) error {
		var failures []string

		// --format is an alias for --export-format; merge the two.
		// If the user typed --format instead of --export-format, honour it.
		if cmd.Flags().Changed("format") && !cmd.Flags().Changed("export-format") {
			traceExportFormat = traceFormatAlias
		}

		// --dry-run: validate but don't write anything.
		// It implies an export target must be specified (otherwise nothing to validate).
		if traceDryRunFlag {
			if traceExportPath == "" && traceExportMarkdown == "" && traceOutputJSON == "" && traceExportSVG == "" {
				failures = append(failures, "--dry-run requires an export target (--export, --output-json, or --export-svg)\n"+
					"  Fix: add an export flag, e.g. --export trace.html --format html")
			}
		}

		// Validate --export-format (and its --format alias) when --export is set.
		if traceExportPath != "" {
			normalised := strings.ToLower(strings.TrimSpace(traceExportFormat))
			if !validTraceExportFormats[normalised] {
				failures = append(failures, fmt.Sprintf(
					"invalid --format %q — must be one of: html, markdown, json, text\n"+
						"  Fix: use --format html (interactive), markdown (shareable), json (machine-readable), or text (plain)",
					traceExportFormat,
				))
			}
			// Validate path safety: null bytes, symlink resolution, existing-directory guard.
			if _, err := ValidateOutputPath("export", traceExportPath); err != nil {
				failures = append(failures, err.Error())
			}
		}

		// Validate --export-markdown path (deprecated alias).
		if traceExportMarkdown != "" {
			if _, err := ValidateOutputPath("export-markdown", traceExportMarkdown); err != nil {
				failures = append(failures, err.Error())
			}
		}

		// Validate --output-json path.
		if traceOutputJSON != "" {
			if _, err := ValidateOutputPath("output-json", traceOutputJSON); err != nil {
				failures = append(failures, err.Error())
			}
		}

		// Validate --export-svg path.
		if traceExportSVG != "" {
			if _, err := ValidateOutputPath("export-svg", traceExportSVG); err != nil {
				failures = append(failures, err.Error())
			}
		}

		// In non-interactive mode, default to --print output instead of the
		// interactive viewer, which cannot function without a terminal.
		if termctx.GlobalNonInteractive() && !tracePrint && traceExportPath == "" && traceExportMarkdown == "" && traceOutputJSON == "" && traceExportSVG == "" {
			tracePrint = true
			fmt.Fprintln(cmd.ErrOrStderr(), "Non-interactive mode: falling back to --print output")
		}

		// --export and --print are mutually exclusive.
		if traceExportPath != "" && tracePrint {
			failures = append(failures, "cannot specify both --export and --print\n"+
				"  Fix: use --export to write to a file, or --print to output to stdout — not both")
		}

		// --export-markdown and --export are mutually exclusive.
		if traceExportMarkdown != "" && traceExportPath != "" {
			failures = append(failures, "cannot specify both --export-markdown and --export\n"+
				"  Fix: use --export --format markdown for the same result as --export-markdown")
		}

		// Validate --trace-verbosity when set.
		if traceVerbosity != "" {
			if _, err := trace.ParseVerbosity(traceVerbosity); err != nil {
				failures = append(failures, fmt.Sprintf(
					"invalid --trace-verbosity %q — must be one of: summary, normal, verbose\n"+
						"  Fix: use --trace-verbosity normal (default), summary (minimal), or verbose (detailed)",
					traceVerbosity,
				))
			}
		}

		// Validate --annotations file exists when set. The file's contents are
		// parsed and validated at run time by trace.LoadAnnotationFile; this
		// pre-flight check only confirms the path is usable so a typo is
		// reported alongside every other flag error in a single pass.
		if traceAnnotationsFlag != "" {
			if _, err := ValidateInputPath("annotations", traceAnnotationsFlag); err != nil {
				failures = append(failures, err.Error())
			}
		}

		// --annotations-strict only means something when annotations are
		// actually being resolved against a trace.
		if traceAnnotationsStrict && traceAnnotationsFlag == "" && traceAnnotationsExportPath == "" {
			failures = append(failures,
				"--annotations-strict requires --annotations or --export-annotations\n"+
					"  Fix: add --annotations <file> to import comments, or drop --annotations-strict")
		}

		// --export-annotations must name a file, not a directory.
		if traceAnnotationsExportPath != "" {
			if strings.HasSuffix(traceAnnotationsExportPath, "/") || strings.HasSuffix(traceAnnotationsExportPath, "\\") {
				failures = append(failures, fmt.Sprintf(
					"--export-annotations %q looks like a directory path; provide a full file path\n"+
						"  Fix: specify a filename (e.g. --export-annotations ./review.json)",
					traceAnnotationsExportPath))
			}
		}

		// Validate --gas-model file exists when set.
		if traceGasModelPath != "" {
			if _, err := ValidateInputPath("gas-model", traceGasModelPath); err != nil {
				failures = append(failures, err.Error())
			}
		}

		// Validate --meta values are in key=value format with a non-empty key.
		// This pre-flight check surfaces malformed flags before the trace file
		// is loaded so users fix all CLI issues in a single pass.
		for _, entry := range traceMetadata {
			parts := strings.SplitN(entry, "=", 2)
			if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
				failures = append(failures, fmt.Sprintf(
					"--meta value %q is not in key=value format\n"+
						"  Fix: supply metadata as key=value pairs, e.g. --meta env=testnet --meta version=1.2",
					entry,
				))
			}
		}

		// Validate --comment values are not empty or whitespace-only.
		for i, comment := range traceComments {
			if strings.TrimSpace(comment) == "" {
				failures = append(failures, fmt.Sprintf(
					"--comment value at position %d is empty or whitespace-only\n"+
						"  Fix: provide non-empty comment text or omit the empty --comment flag",
					i,
				))
			}
		}

		// Validate --verify-export path when set.
		if traceVerifyExportFlag != "" {
			if _, err := ValidateInputPath("verify-export", traceVerifyExportFlag); err != nil {
				failures = append(failures, err.Error())
			}
		}

		if len(failures) == 1 {
			return errors.WrapValidationError(failures[0])
		}
		if len(failures) > 1 {
			lines := make([]string, 0, len(failures)+1)
			lines = append(lines, fmt.Sprintf("%d trace command validation error(s):", len(failures)))
			for i, f := range failures {
				lines = append(lines, fmt.Sprintf("  %d. %s", i+1, f))
			}
			return errors.WrapValidationError(strings.Join(lines, "\n"))
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		// diagCollector is active only when --timings is set.
		var diagCollector *diagnostics.Collector
		if traceTimingsFlag {
			diagCollector = diagnostics.NewCollector()
		} else {
			diagCollector = diagnostics.Noop()
		}

		// --verify-export: verify integrity of an existing export file and exit.
		// This mode does not require a trace file argument — it only reads the
		// export artifact and its companion .meta.json file.
		if traceVerifyExportFlag != "" {
			if err := trace.VerifyExport(traceVerifyExportFlag); err != nil {
				return errors.WrapValidationError(fmt.Sprintf(
					"export integrity check failed for %q:\n  %s\n"+
						"  Fix: re-export the trace with 'glassbox trace --export <file> --format <fmt> <trace>'",
					traceVerifyExportFlag, err.Error(),
				))
			}
			fmt.Printf("%s Export integrity verified: %s\n",
				visualizer.Symbol("success"), traceVerifyExportFlag)
			return nil
		}

		// Capture the cobra context so we can check for Ctrl-C throughout.
		ctx := cmd.Context()

		// Apply theme if specified, otherwise auto-detect.
		if traceThemeFlag != "" {
			visualizer.SetTheme(visualizer.Theme(traceThemeFlag))
		} else {
			visualizer.SetTheme(visualizer.DetectTheme())
		}

		// Resolve the trace file path (positional arg or --file flag).
		var filename string
		if len(args) > 0 {
			filename = args[0]
		} else if traceFile != "" {
			filename = traceFile
		} else {
			return errors.WrapValidationError(
				"trace file is required\n" +
					"  Usage: glassbox trace <trace-file>\n" +
					"  Or:    glassbox trace --file <trace-file>\n" +
					"  Run 'glassbox trace --help' for all available options")
		}

		// Validate the trace file path — normalizes, resolves symlinks, checks
		// existence and readability using the security-aware path validator.
		normalizedFilename, pathErr := ValidateInputPath("file", filename)
		if pathErr != nil {
			// Produce a user-friendly error that includes the original value.
			return errors.WrapValidationError(fmt.Sprintf(
				"trace file not found or not readable: %q\n"+
					"  Fix: verify the path is correct and the file exists\n"+
					"  Tip: trace files are produced by 'glassbox debug --trace-output <file>'",
				filename,
			))
		}
		filename = normalizedFilename

		var loadStart time.Time
		if traceShowTimingFlag {
			loadStart = time.Now()
		}

		doneLoad := diagCollector.Start(diagnostics.PhaseTraceLoad)
		data, err := os.ReadFile(filename)
		if err != nil {
			doneLoad(err)
			return errors.WrapValidationError(fmt.Sprintf(
				"failed to read trace file %q: %v\n"+
					"  Fix: ensure you have read permissions for the file",
				filename, err,
			))
		}

		executionTrace, err := trace.FromJSON(data)
		doneLoad(err)
		if err != nil {
			return errors.WrapUnmarshalFailed(err,
				fmt.Sprintf(
					"failed to parse trace file %q — the file may be corrupted or not a valid Glassbox trace\n"+
						"  Fix: verify the file is valid JSON with 'jq . %q'\n"+
						"  Tip: re-export the trace with 'glassbox debug --trace-output <file>'",
					filename, filename,
				))
		}

		if traceShowTimingFlag {
			fmt.Fprintf(cmd.ErrOrStderr(), "  load:   %s (%d steps, %d bytes)\n",
				time.Since(loadStart).Round(time.Millisecond), len(executionTrace.States), len(data))
		}

		// Emit size/performance warnings for large traces via the compatibility
		// validator so users know before the export starts.
		if traceExportPath != "" || traceExportMarkdown != "" {
			targetFmt := traceExportFormat
			if traceExportMarkdown != "" {
				targetFmt = "markdown"
			}
			compatWarnings := trace.ValidateFormatCompatibility(executionTrace, targetFmt, trace.DefaultCompatibilityOptions())
			for _, w := range compatWarnings {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: %s\n", w)
			}
		}

		// Annotate with gas model estimates if requested.
		if traceGasModelPath != "" {
			model, err := gasmodel.ParseGasModel(traceGasModelPath)
			if err != nil {
				return errors.WrapValidationError(fmt.Sprintf(
					"failed to load gas model from %q: %v\n"+
						"  Fix: verify the gas model JSON file is valid",
					traceGasModelPath, err,
				))
			}
			trace.AnnotateExecutionCosts(executionTrace, nil, model)
		}

		// Merge any --comment / --meta annotations supplied on the command line.
		if len(traceComments) > 0 || len(traceMetadata) > 0 {
			opts, err := traceExportOptions()
			if err != nil {
				return err
			}
			executionTrace.Annotations.Comments = append(executionTrace.Annotations.Comments, opts.Comments...)
			if executionTrace.Annotations.SessionMetadata == nil {
				executionTrace.Annotations.SessionMetadata = make(map[string]string)
			}
			for k, v := range opts.SessionMetadata {
				executionTrace.Annotations.SessionMetadata[k] = v
			}
		}

		// --annotations: import reviewer comments from a portable annotation
		// file. This happens before the verbosity filter runs so targets are
		// resolved against the trace at full fidelity; a later filter that
		// breaks an anchor is reported, not silently applied.
		if traceAnnotationsFlag != "" {
			file, loadErr := trace.LoadAnnotationFile(traceAnnotationsFlag)
			if loadErr != nil {
				return errors.WrapValidationError(loadErr.Error())
			}
			report, attachErr := executionTrace.AttachReviewerComments(file.Comments)
			if attachErr != nil {
				return errors.WrapValidationError(attachErr.Error())
			}
			// Dangling references are reported but never cause valid comments
			// to be dropped; --annotations-strict turns them into an error for
			// callers that want a hard gate (e.g. CI).
			for _, w := range report.Warnings() {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: %s\n", w)
			}
			if traceAnnotationsStrict && report.HasDangling() {
				return errors.WrapValidationError(fmt.Sprintf(
					"%d of %d imported annotation(s) have targets that do not resolve against this trace\n"+
						"  Fix: re-export the annotations against this trace, or drop --annotations-strict "+
						"to import them anyway",
					len(report.Dangling), len(file.Comments)))
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "Imported %d annotation(s) from %s (%d resolved, %d dangling)\n",
				len(file.Comments), traceAnnotationsFlag, len(report.Resolved), len(report.Dangling))

			// Bookmarks [Issue #562]: merged conflict-aware rather than
			// overwritten, so importing a colleague's bookmarks can never
			// silently clobber your own — a real conflict is either
			// rejected (the default) or kept alongside the existing
			// bookmark under a new identity, never picked for you.
			if len(file.Bookmarks) > 0 {
				policy, policyErr := trace.ParseBookmarkConflictPolicy(traceBookmarksOnConflict)
				if policyErr != nil {
					return errors.WrapValidationError(policyErr.Error())
				}

				if traceBookmarksPreview {
					conflicts := trace.DetectBookmarkConflicts(executionTrace, executionTrace.Annotations.Bookmarks, file.Bookmarks)
					if len(conflicts) == 0 {
						fmt.Fprintf(cmd.OutOrStdout(), "No bookmark conflicts: %d bookmark(s) would be imported cleanly.\n", len(file.Bookmarks))
					} else {
						fmt.Fprintf(cmd.OutOrStdout(), "%d bookmark conflict(s):\n", len(conflicts))
						for i, c := range conflicts {
							fmt.Fprintf(cmd.OutOrStdout(), "  %d. %q vs existing %q: %s\n", i+1, c.Incoming.Name, c.Existing.Name, c.Reason)
						}
						fmt.Fprintf(cmd.OutOrStdout(), "\nRe-run with --bookmarks-on-conflict rename or merge to keep both.\n")
					}
					return nil
				}

				merged, mergeResult, mergeErr := trace.MergeBookmarks(
					executionTrace, executionTrace.Annotations.Bookmarks, file.Bookmarks, policy)
				if mergeErr != nil {
					return errors.WrapValidationError(mergeErr.Error())
				}
				executionTrace.Annotations.Bookmarks = merged
				fmt.Fprintf(cmd.ErrOrStderr(), "Imported %d bookmark(s) (%d kept under a new identity to avoid a conflict)\n",
					len(file.Bookmarks), len(mergeResult.Renamed))
			}
		}

		// --export-annotations: write the trace's reviewer comments to a
		// portable file. Exporting what was just imported is a byte-stable
		// round trip, which is what makes annotations shareable between
		// reviewers.
		if traceAnnotationsExportPath != "" && !traceDryRunFlag {
			file, exportErr := trace.ExportAnnotationFile(executionTrace, time.Now())
			if exportErr != nil {
				return errors.WrapValidationError(exportErr.Error())
			}
			if !traceForceFlag {
				if _, statErr := os.Stat(traceAnnotationsExportPath); statErr == nil {
					fmt.Fprintf(cmd.ErrOrStderr(),
						"Warning: %q already exists and will be overwritten. Use --force to suppress this warning.\n",
						traceAnnotationsExportPath)
				}
			}
			if saveErr := file.Save(traceAnnotationsExportPath); saveErr != nil {
				removeIfCancelled(ctx, traceAnnotationsExportPath)
				if ctx.Err() != nil {
					return ErrInterrupted
				}
				return errors.WrapValidationError(saveErr.Error())
			}
			fmt.Printf("%s Annotations exported to: %s (%d comment(s), %d bookmark(s))\n",
				visualizer.Symbol("success"), traceAnnotationsExportPath, len(file.Comments), len(file.Bookmarks))

			// Every other export flag in this command writes its file and
			// exits. Composing --export-annotations with a report export is
			// useful, so only exit when nothing else was asked for — otherwise
			// exporting annotations alone would drop into the interactive
			// viewer, which no other export flag does.
			if traceExportPath == "" && traceExportMarkdown == "" && traceExportSVG == "" &&
				traceOutputJSON == "" && !tracePrint {
				printTimings(cmd, diagCollector, traceTimingsFlag)
				return nil
			}
		}

		// --dry-run: validate parameters and compatibility but write nothing.
		if traceDryRunFlag {
			issues := trace.ValidateExecutionTrace(executionTrace)
			if len(issues) > 0 {
				for _, issue := range issues {
					fmt.Fprintf(cmd.ErrOrStderr(), "Warning: %s\n", issue)
				}
			}
			targetFmt := traceExportFormat
			if traceExportMarkdown != "" {
				targetFmt = "markdown"
			}
			if err := trace.ValidateTraceExportParams(executionTrace, targetFmt, traceExportPath, trace.ExportOptions{}); err != nil {
				return errors.WrapValidationError(fmt.Sprintf("dry-run validation failed: %v", err))
			}
			fmt.Printf("%s Dry-run complete — %d step(s), format %q validated. No files written.\n",
				visualizer.Symbol("success"), len(executionTrace.States), targetFmt)
			return nil
		}

		// --output-json: write deterministic schema'd JSON export and exit.
		if traceOutputJSON != "" {
			if !traceForceFlag {
				if _, statErr := os.Stat(traceOutputJSON); statErr == nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "Warning: %q already exists and will be overwritten. Use --force to suppress this warning.\n", traceOutputJSON)
				}
			}
			var jsonStart time.Time
			if traceShowTimingFlag {
				jsonStart = time.Now()
			}
			jsonData, err := executionTrace.ExportJSON(trace.CurrentJSONSchemaVersion, time.Now())
			if err != nil {
				return errors.WrapValidationError(fmt.Sprintf(
					"failed to serialize trace as JSON: %v\n"+
						"  This may indicate the trace contains non-serializable data",
					err,
				))
			}
			doneExport := diagCollector.Start(diagnostics.PhaseTraceExport)
			writeErr := os.WriteFile(traceOutputJSON, jsonData, 0o644)
			doneExport(writeErr)
			if writeErr != nil {
				removeIfCancelled(ctx, traceOutputJSON)
				if ctx.Err() != nil {
					return ErrInterrupted
				}
				return errors.WrapValidationError(fmt.Sprintf(
					"failed to write JSON export to %q: %v\n"+
						"  Fix: ensure you have write permissions and sufficient disk space",
					traceOutputJSON, writeErr,
				))
			}
			sizeStr := humanFileSize(int64(len(jsonData)))
			if traceShowTimingFlag {
				fmt.Fprintf(cmd.ErrOrStderr(), "  export: %s\n", time.Since(jsonStart).Round(time.Millisecond))
			}
			printTimings(cmd, diagCollector, traceTimingsFlag)
			fmt.Printf("%s Trace exported to: %s (%s)\n", visualizer.Symbol("success"), traceOutputJSON, sizeStr)
			return nil
		}

		// Apply verbosity filter before rendering.
		verbosityLevel, err := trace.ParseVerbosity(traceVerbosity)
		if err != nil {
			return errors.WrapValidationError(fmt.Sprintf(
				"invalid --trace-verbosity %q — must be one of: summary, normal, verbose\n"+
					"  Fix: use --trace-verbosity normal (default), summary (minimal), or verbose (detailed)",
				traceVerbosity,
			))
		}
		executionTrace = trace.FilterExecutionTrace(executionTrace, verbosityLevel)

		// --print: render a rich ASCII tree report then exit (non-interactive).
		if tracePrint {
			var printStart time.Time
			if traceShowTimingFlag {
				printStart = time.Now()
			}
			opts := trace.PrintOptions{
				NoColor:      traceNoColor || NoColorFlag,
				EventSchemas: eventSchemas,
			}
			doneRender := diagCollector.Start(diagnostics.PhaseTraceRender)
			trace.PrintExecutionTrace(executionTrace, opts)
			doneRender(nil)
			if traceShowTimingFlag {
				fmt.Fprintf(cmd.ErrOrStderr(), "  render: %s\n", time.Since(printStart).Round(time.Millisecond))
			}
			printTimings(cmd, diagCollector, traceTimingsFlag)
			return nil
		}

		// --export-markdown: deprecated alias — emit a deprecation notice.
		if traceExportMarkdown != "" {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"Deprecated: --export-markdown is deprecated; use --export %s --format markdown instead.\n",
				traceExportMarkdown,
			)
			if !traceForceFlag {
				if _, statErr := os.Stat(traceExportMarkdown); statErr == nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "Warning: %q already exists and will be overwritten. Use --force to suppress this warning.\n", traceExportMarkdown)
				}
			}
			var mdStart time.Time
			if traceShowTimingFlag {
				fmt.Fprintf(cmd.ErrOrStderr(), "Exporting %d steps as markdown...\n", len(executionTrace.States))
				mdStart = time.Now()
			}
			doneExport := diagCollector.Start(diagnostics.PhaseTraceExport)
			mdErr := trace.ExportExecutionTrace(executionTrace, "markdown", traceExportMarkdown)
			doneExport(mdErr)
			if mdErr != nil {
				removeIfCancelled(ctx, traceExportMarkdown)
				if ctx.Err() != nil {
					return ErrInterrupted
				}
				return errors.WrapValidationError(fmt.Sprintf(
					"failed to export trace as Markdown to %q: %v\n"+
						"  Fix: ensure the output directory exists and you have write permissions",
					traceExportMarkdown, mdErr,
				))
			}
			sizeStr := traceExportedFileSize(traceExportMarkdown)
			if traceShowTimingFlag {
				fmt.Fprintf(cmd.ErrOrStderr(), "  export: %s\n", time.Since(mdStart).Round(time.Millisecond))
			}
			printTimings(cmd, diagCollector, traceTimingsFlag)
			fmt.Printf("%s Trace exported to: %s%s\n", visualizer.Symbol("success"), traceExportMarkdown, sizeStr)
			return nil
		}

		// --export-svg: generate a call graph SVG and exit.
		if traceExportSVG != "" {
			if len(executionTrace.DiagnosticEvents) == 0 {
				return errors.WrapValidationError(
					"no diagnostic events found in trace — call graph cannot be generated\n" +
						"  Possible causes:\n" +
						"    - The trace was captured without diagnostic events\n" +
						"    - The transaction did not call any contracts\n" +
						"  Fix: re-run with a transaction that includes contract calls\n" +
						"  Tip: use --trace-verbosity verbose when capturing the trace for maximum detail",
				)
			}
			if !traceForceFlag {
				if _, statErr := os.Stat(traceExportSVG); statErr == nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "Warning: %q already exists and will be overwritten. Use --force to suppress this warning.\n", traceExportSVG)
				}
			}

			maxDepth := 50
			callTree, err := decoder.DecodeDiagnosticEvents(executionTrace.DiagnosticEvents, maxDepth)
			if err != nil {
				return errors.WrapValidationError(fmt.Sprintf(
					"failed to decode call tree from diagnostic events: %v\n"+
						"  Fix: verify the trace file is complete and not corrupted",
					err,
				))
			}
			svg := visualizer.GenerateCallGraphSVG(callTree, maxDepth)
			if err := os.WriteFile(traceExportSVG, []byte(svg), 0o644); err != nil {
				return errors.WrapValidationError(fmt.Sprintf(
					"failed to write SVG to %q: %v\n"+
						"  Fix: ensure the output directory exists and you have write permissions",
					traceExportSVG, err,
				))
			}
			sizeStr := humanFileSize(int64(len(svg)))
			fmt.Printf("%s Call graph exported to: %s (%s)\n", visualizer.Symbol("success"), traceExportSVG, sizeStr)
			return nil
		}

		// --export: write a trace report in the specified format and exit.
		if traceExportPath != "" {
			if !traceForceFlag {
				if _, statErr := os.Stat(traceExportPath); statErr == nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "Warning: %q already exists and will be overwritten. Use --force to suppress this warning.\n", traceExportPath)
				}
			}

			opts, err := traceExportOptions()
			if err != nil {
				return err
			}

			// Emit a pre-export progress line so large exports don't appear to hang.
			fmt.Fprintf(cmd.ErrOrStderr(), "Exporting %d steps as %s...\n", len(executionTrace.States), traceExportFormat)
			var exportStart time.Time
			if traceShowTimingFlag {
				exportStart = time.Now()
			}

			// Route through ExportWithCompatibility so size warnings and version
			// information are correctly applied (bridges the gap between the
			// lower-level ExportExecutionTraceWithOptions and the compatibility layer).
			doneExport := diagCollector.Start(diagnostics.PhaseTraceExport)
			exportErr := trace.ExportWithCompatibility(executionTrace, traceExportFormat, traceExportPath, opts, trace.DefaultCompatibilityOptions())
			doneExport(exportErr)
			if exportErr != nil {
				removeIfCancelled(ctx, traceExportPath)
				if ctx.Err() != nil {
					return ErrInterrupted
				}
				return errors.WrapValidationError(fmt.Sprintf(
					"failed to export trace as %s to %q: %v\n"+
						"  Fix: ensure the output directory exists and you have write permissions",
					traceExportFormat, traceExportPath, exportErr,
				))
			}

			sizeStr := traceExportedFileSize(traceExportPath)
			if traceShowTimingFlag {
				fmt.Fprintf(cmd.ErrOrStderr(), "  export: %s\n", time.Since(exportStart).Round(time.Millisecond))
			}
			printTimings(cmd, diagCollector, traceTimingsFlag)
			fmt.Printf("%s Trace exported to: %s%s\n", visualizer.Symbol("success"), traceExportPath, sizeStr)
			return nil
		}

		// Default: start the interactive viewer.
		viewer := trace.NewInteractiveViewer(executionTrace)
		return viewer.Start()
	},
}

func init() {
	traceCmd.Flags().StringVarP(&traceFile, "file", "f", "", "Trace file to load")
	traceCmd.Flags().StringVar(&traceThemeFlag, "theme", "", "Color theme (default, dark, light, deuteranopia, protanopia, tritanopia, high-contrast)")
	traceCmd.Flags().BoolVar(&tracePrint, "print", false, "Print a rich ASCII tree report and exit (non-interactive)")
	traceCmd.Flags().BoolVar(&traceNoColor, "no-color", false, "Disable ANSI colour output (also honoured via NO_COLOR env var)")
	traceCmd.Flags().StringVar(&traceExportSVG, "export-svg", "", "Export call graph as SVG to specified file")
	traceCmd.Flags().StringVar(&traceOutputJSON, "output-json", "", "Export trace as deterministic JSON to specified file (includes schema_version)")
	traceCmd.Flags().StringVar(&traceExportPath, "export", "", "Export trace report to a file (use with --format)")
	traceCmd.Flags().StringVar(&traceExportFormat, "export-format", "html", "Trace export format: html, markdown, json, or text (use --format as an alias)")
	traceCmd.Flags().StringVar(&traceFormatAlias, "format", "", "Export format alias for --export-format: html, markdown, json, or text")
	traceCmd.Flags().StringVar(&traceExportMarkdown, "export-markdown", "", "Export trace as Markdown to specified file (deprecated: use --export --format markdown)")
	traceCmd.Flags().StringVar(&traceAnnotationsFlag, "annotations", "", "Import reviewer comments from a JSON annotation file and attach them to the trace")
	traceCmd.Flags().StringVar(&traceAnnotationsExportPath, "export-annotations", "", "Export the trace's reviewer comments to a portable JSON annotation file")
	traceCmd.Flags().BoolVar(&traceAnnotationsStrict, "annotations-strict", false, "Fail if any imported annotation targets a step or source location missing from the trace")
	traceCmd.Flags().StringVar(&traceBookmarksOnConflict, "bookmarks-on-conflict", "fail", "Conflict resolution policy for bookmarks imported via --annotations: fail, rename, or merge")
	traceCmd.Flags().BoolVar(&traceBookmarksPreview, "bookmarks-preview", false, "Show bookmark conflicts from --annotations without applying them")
	traceCmd.Flags().StringVar(&traceGasModelPath, "gas-model", "", "Gas model JSON used to annotate contract call cost estimates")
	traceCmd.Flags().StringVar(&traceVerbosity, "trace-verbosity", "normal", "Trace detail level: summary, normal, or verbose")
	traceCmd.Flags().StringArrayVar(&traceComments, "comment", nil, "Comment to include in exported trace artifacts; repeatable")
	traceCmd.Flags().StringArrayVar(&traceMetadata, "meta", nil, "Session metadata for exported trace artifacts in key=value form; repeatable")
	traceCmd.Flags().BoolVar(&traceDryRunFlag, "dry-run", false, "Validate parameters and trace data without writing any files")
	traceCmd.Flags().BoolVar(&traceForceFlag, "force", false, "Overwrite existing output files without prompting")
	traceCmd.Flags().BoolVar(&traceShowTimingFlag, "show-timing", false, "Print load, render, and export timing to stderr")
	traceCmd.Flags().BoolVar(&traceTimingsFlag, "timings", false, "Print per-phase timing breakdown to stderr after the operation completes")
	traceCmd.Flags().StringVar(&traceVerifyExportFlag, "verify-export", "", "Verify integrity of an existing export file (checksum, step count, schema version)")

	// Secret scanning flags
	traceCmd.Flags().StringVar(&secretScanModeFlag, "secret-scan-mode", "", "Secret scanning mode: opt-in (warn only) or strict (block export)")
	traceCmd.Flags().StringArrayVar(&secretScanOverrideFlag, "secret-scan-override", nil, "Paths allowed to contain secrets (for test fixtures); repeatable")

	// Secret scanning flags
	traceCmd.Flags().StringVar(&secretScanModeFlag, "secret-scan-mode", "", "Secret scanning mode: opt-in (warn only) or strict (block export)")
	traceCmd.Flags().StringArrayVar(&secretScanOverrideFlag, "secret-scan-override", nil, "Paths allowed to contain secrets (for test fixtures); repeatable")

	// Secret scanning flags
	traceCmd.Flags().StringVar(&secretScanModeFlag, "secret-scan-mode", "", "Secret scanning mode: opt-in (warn only) or strict (block export)")
	traceCmd.Flags().StringArrayVar(&secretScanOverrideFlag, "secret-scan-override", nil, "Paths allowed to contain secrets (for test fixtures); repeatable")

	// Secret scanning flags
	traceCmd.Flags().StringVar(&secretScanModeFlag, "secret-scan-mode", "", "Secret scanning mode: opt-in (warn only) or strict (block export)")
	traceCmd.Flags().StringArrayVar(&secretScanOverrideFlag, "secret-scan-override", nil, "Paths allowed to contain secrets (for test fixtures); repeatable")

	_ = traceCmd.RegisterFlagCompletionFunc("theme", completeThemeFlag)
	_ = traceCmd.RegisterFlagCompletionFunc("export-format", completeTraceExportFormatFlag)
	_ = traceCmd.RegisterFlagCompletionFunc("format", completeTraceExportFormatFlag)
	_ = traceCmd.RegisterFlagCompletionFunc("trace-verbosity", completeTraceVerbosityFlag)

	rootCmd.AddCommand(traceCmd)
}

func traceExportOptions() (trace.ExportOptions, error) {
	metadata := make(map[string]string, len(traceMetadata))
	for _, entry := range traceMetadata {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
			return trace.ExportOptions{}, errors.WrapValidationError(
				fmt.Sprintf(
					"--meta value %q is not in key=value format\n"+
						"  Fix: supply metadata as key=value pairs, e.g. --meta env=testnet --meta version=1.2",
					entry,
				),
			)
		}
		metadata[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}

	// Parse secret scan mode
	var scanMode security.ScannerMode
	if secretScanModeFlag != "" {
		switch strings.ToUpper(strings.TrimSpace(secretScanModeFlag)) {
		case "OPT_IN":
			scanMode = security.ModeOptIn
		case "STRICT":
			scanMode = security.ModeStrict
		default:
			return trace.ExportOptions{}, errors.WrapValidationError(
				fmt.Sprintf(
					"--secret-scan-mode must be either 'opt-in' or 'strict', got %q\n"+
						"  Fix: use --secret-scan-mode opt-in (warn only) or --secret-scan-mode strict (block export)",
					secretScanModeFlag,
				),
			)
		}
	}

	return trace.ExportOptions{
		Comments:           traceComments,
		SessionMetadata:    metadata,
		SecretScanMode:     scanMode,
		SecretScanOverrides: secretScanOverrideFlag,
	}, nil
}

// printTimings writes the diagnostics timing table to stderr when enabled.
// It is a no-op when active is false, which keeps all non-timing code paths
// clean without any conditional logic at each call site.
func printTimings(cmd *cobra.Command, dc *diagnostics.Collector, active bool) {
	if !active {
		return
	}
	dc.PrintHuman(cmd.ErrOrStderr())
}

// removeIfCancelled deletes path when ctx is cancelled and the file exists.
// It is called after a write error to ensure no partial output is left on disk
// when the operation was interrupted by Ctrl-C.  Errors from Remove are
// intentionally ignored — a best-effort cleanup is all we need here.
func removeIfCancelled(ctx context.Context, path string) {
	if ctx.Err() == nil {
		return
	}
	if path == "" {
		return
	}
	_ = os.Remove(path)
}
