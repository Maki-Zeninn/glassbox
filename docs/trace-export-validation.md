# Trace Export Validation and Diagnostics

The `glassbox debug` and `glassbox trace` commands include comprehensive validation and diagnostic capabilities for trace export operations. This document describes the validation checks, error handling, and troubleshooting guidance for both commands.

---

## Overview

Trace export validation occurs at multiple stages:

1. **Pre-flight validation** — CLI flag validation before any simulation or file load
2. **Pre-export validation** — Trace data and configuration validation before export
3. **Format compatibility** — Format-specific checks for data compatibility
4. **Export execution** — File system and I/O validation during write

---

## Pre-flight Validation (CLI Flags)

The debug command validates trace-related flags in `PreRunE` before any network or simulator operations:

### `--trace-verbosity`

**Valid values:** `summary`, `normal`, `verbose`

**Validation:**
- Must be one of the three supported values (case-insensitive)
- Checked at parse time before any execution

**Error example:**
```
invalid --trace-verbosity "ultra" — must be one of: summary, normal, verbose
  Fix: use --trace-verbosity normal (default), summary (minimal), or verbose (detailed)
```

### `--format`

**Valid values:** `text`, `json`, `html`, `markdown` (or `md`)

**Validation:**
- Must be one of the supported export formats
- Checked before simulation begins

**Error example:**
```
invalid trace export format "yaml" — must be one of: text, json, html, markdown
  Fix: use --format html (interactive), json (machine-readable), markdown (shareable), or text (CLI output)
```

### `--trace-output`

**Validation:**
- Must be a file path, not a directory path (no trailing `/` or `\`)
- Cannot contain null bytes
- Path traversal sequences (`..`) trigger a security warning
- Parent directory must exist or be creatable

**Error examples:**
```
--trace-output "./traces/" looks like a directory path; provide a full file path
  Fix: specify a complete file path (e.g. ./traces/trace.html or ./output/trace.json)
  Example: glassbox debug --trace-output ./traces/debug-$(date +%Y%m%d).html <tx-hash>
```

```
--trace-output "../../../etc/passwd" contains directory traversal sequences (..)
  Fix: use absolute paths or relative paths without '..' for security
  Example: use './output/trace.html' instead of '../output/trace.html'
```

---

## Pre-export Validation

Before attempting to write a trace export, the system validates all export parameters using comprehensive validation functions: `ValidateTraceExportParams()` and `ValidateTraceFormatCompatibility()`.

### Trace Data Validation (ValidateTraceExportParams)

**Checks:**
- Trace object is not nil
- Trace contains at least one execution state
- Transaction hash is present and non-empty
- Start and end times are valid (not zero, end >= start)
- Event types are recognized (unrecognized types trigger warnings)

**Error example:**
```
trace has no execution states — empty trace cannot be exported
  Fix: verify that the trace was captured correctly and contains at least one step
  Tip: check that the traced transaction actually executed any code
```

Another example with time validation:

```
trace end time is before start time — invalid temporal ordering
  Fix: verify the trace timestamps were recorded correctly
  Start: 2026-01-02T15:04:05Z, End: 2026-01-02T15:04:00Z
```

### Format and Path Validation

**Checks:**
- Export format is not empty and is supported
- Output path is not empty
- Output path is not a directory
- Output path does not contain invalid characters

**Error example:**
```
export format is empty — must specify one of: html, markdown, json, text
  Fix: provide --format html (default), markdown, json, or text
```

### Export Options Validation

**Checks:**
- Comment count does not exceed 100
- Individual comment length does not exceed 10,000 characters
- Session metadata keys and values are valid strings

**Error example:**
```
too many comments (150) — maximum is 100 comments per trace export
  Fix: reduce the number of comments or split into multiple exports
```

---

## Format Compatibility Checks

Format compatibility validation is performed by `ValidateTraceFormatCompatibility()` to ensure trace data is suitable for the target export format. Each format has specific requirements and constraints.

### JSON Format

**Requirements:**
- All trace data must be JSON-serializable
- No circular references
- Step indices must be sequential and match array position

**Error example:**
```
trace step mismatch at position 5: expected step 5 but got 10 — trace may be corrupted
```

### HTML Format

**Compatibility constraints:**
- Traces with >50,000 steps may cause browser rendering to be slow or unresponsive
- Individual error messages >1MB will cause rendering issues

**Error example:**
```
trace has 60000 steps — too large for HTML export (browser may become unresponsive)
  Fix: use --format json for large traces or filter the trace verbosity
  Alternatively: use --trace-verbosity summary to reduce output size
```

### Markdown Format

**Compatibility constraints:**
- Traces with >10,000 steps produce very large markdown files (>1MB)
- Code fence markers (```) in error messages should be reviewed for formatting

### Text Format

**Requirements:**
- Most permissive format
- No special constraints

---

## Export Execution Errors

Errors that occur during file write operations include detailed remediation:

### Directory Creation Failure

```
failed to create trace export directory: permission denied
  Directory: /restricted/traces
  Fix: ensure you have write permissions to the parent directory
  Or choose a different output path with --trace-output
```

### File Write Failure

```
failed to write trace export file: no space left on device
  Path: /tmp/trace.html
  Fix: ensure you have write permissions and sufficient disk space
  Check: ls -la /tmp
```

### Template Rendering Failure

```
failed to generate HTML trace: template execution error: ...
  This may indicate invalid trace data or a template rendering error
  Check that all trace fields are properly populated
```

---

## Validation in Dry-Run Mode

When using `--dry-run`, trace output configuration is validated without executing the simulation:

```sh
glassbox debug --dry-run --trace-output ./invalid/ --network testnet <tx-hash>
```

**Output:**
```
Additional environment checks:
[FAIL] Trace output validation failed: --trace-output "./invalid/" looks like a directory path
       Fix: ensure trace output path is valid and format is correct
```

---

## Multiple Validation Errors

When multiple validation errors are detected, all failures are reported together so they can be fixed in a single pass:

```
3 trace input validation error(s):
  1. invalid --trace-verbosity "ultra" — must be one of: summary, normal, verbose
     Fix: use --trace-verbosity normal (default), summary (minimal), or verbose (detailed)
  2. invalid trace export format "yaml" — must be one of: text, json, html, markdown
     Fix: use --format html (interactive), json (machine-readable), markdown (shareable), or text (CLI output)
  3. --trace-output "./traces/" looks like a directory path; provide a full file path
     Fix: specify a complete file path (e.g. ./traces/trace.html or ./output/trace.json)
```

---

## Best Practices

### 1. Use Dry-Run for Validation

Always run with `--dry-run` first when setting up trace export in CI/CD:

```sh
glassbox debug --dry-run \
  --network testnet \
  --trace-output ./artifacts/trace.html \
  --format html \
  <tx-hash>
```

### 2. Choose the Right Format

- **HTML**: Interactive viewing in browsers, best for manual analysis
- **JSON**: Machine-readable, best for CI/CD and automated processing
- **Markdown**: Shareable in chat/issues, best for collaboration
- **Text**: Plain CLI output, best for simple logging

#### Format comparison

| | HTML | JSON | Markdown | Text |
|---|---|---|---|---|
| **Flag** | `--format html` | `--format json` | `--format markdown` | `--format text` |
| **Extension** | `.html` | `.json` | `.md` | `.txt` |
| **Human-readable** | Yes — interactive | No | Yes | Yes |
| **Machine-readable** | No | **Yes** | No | No |
| **Round-trip loadable** | No | **Yes** — `glassbox trace` can reload | No | No |
| **Recoverable** | No | **Yes** — `RecoverTrace()` | No | No |
| **Integrity metadata** | Optional `.meta.json` | Optional `.meta.json` | Optional `.meta.json` | Optional `.meta.json` |
| **Max recommended steps** | 50,000 | Unlimited | 10,000 | 100,000 |
| **Step index validation** | No | **Yes** — sequential index required | No | No |
| **GitHub import** | No | No | Embed as code block | No |
| **SARIF consumer** | No | No | No | No |
| **Best for** | Manual debugging | CI/CD, replay, archival | PR comments, issues | Quick terminal review |

**Key metadata differences:**

- `--output-json` produces a versioned envelope (`schema_version: "1.0"`) that `glassbox trace` can reload and validate. The `--export --format json` flag uses the same content but a different envelope shape (`version: {major, minor, patch}`). Both shapes are detected automatically by `LoadExecutionTrace`.
- Only JSON format exports carry a step-count integrity check in the `.meta.json` companion file. For HTML, Markdown, and Text exports, the checksum still verifies byte-for-byte integrity, but step count cannot be validated because the format is not machine-parseable.
- JSON is the only format that supports `--verify-export` step-count checking and `RecoverTrace()` repair. Always export in JSON when archival or recovery capability is required.

### 3. Organize Output Paths

Use dated directories for trace exports:

```sh
glassbox debug \
  --trace-output "./traces/$(date +%Y-%m-%d)/${TX_HASH}.html" \
  --format html \
  $TX_HASH
```

### 4. Validate Before Large Exports

For traces with many steps, validate format compatibility first:

```sh
# Check trace size first
glassbox debug --format json $TX_HASH | jq '.States | length'

# Use JSON for very large traces (>1000 steps)
if [ $STEPS -gt 1000 ]; then
  glassbox debug --format json --trace-output ./trace.json $TX_HASH
else
  glassbox debug --format html --trace-output ./trace.html $TX_HASH
fi
```

---

## Troubleshooting

### "Trace contains no steps"

**Cause:** The simulator did not produce any diagnostic events.

**Solutions:**
1. Verify the transaction hash is correct
2. Run `glassbox doctor` to check simulator compatibility
3. Check that the transaction actually executed on the network
4. Ensure the simulator binary is up-to-date

### "Trace step mismatch"

**Cause:** The trace data structure is corrupted.

**Solutions:**
1. Re-run the debug command to regenerate the trace
2. Check for filesystem corruption if using `--save-snapshots`
3. Verify the simulator version matches the CLI version

### "Very large arguments"

**Cause:** Contract arguments exceed browser rendering limits for HTML export.

**Solutions:**
1. Use JSON format instead: `--format json`
2. Filter the trace to specific event types
3. Use `--trace-verbosity summary` for less detail

### "Permission denied"

**Cause:** Insufficient write permissions to the output directory.

**Solutions:**
1. Choose a different output path with write permissions
2. Create the output directory manually with correct permissions
3. Check filesystem mount options (read-only mounts)

---

## `glassbox trace` Command Export Validation

The `glassbox trace` command validates its export-related flags in `PreRunE` before loading or processing any trace file.

### `--export-format`

**Valid values:** `html`, `markdown` (or `md`), `json`, `text`

**Validation:**
- Only checked when `--export` is also provided
- Must be one of the four supported values (case-insensitive)
- Empty/default value (`html`) is always accepted

**Error example:**
```
invalid --export-format "yaml" — must be one of: html, markdown, json, text
  Fix: use --export-format html (interactive), markdown (shareable), json (machine-readable), or text (plain)
```

### `--export`

**Validation:**
- Path must not end with `/` or `\` (directory path guard)
- Cannot be combined with `--print`
- Cannot be combined with `--export-markdown`

**Error example:**
```
--export "./traces/" looks like a directory path; provide a full file path
  Fix: specify a filename (e.g. --export ./traces/output.html)
  Example: glassbox trace --export ./traces/report.html execution.json
```

```
cannot specify both --export and --print
  Fix: use --export to write to a file, or --print to output to stdout — not both
```

### `--export-markdown`

**Validation:**
- Path must not end with `/` or `\`
- Cannot be combined with `--export`

**Error example:**
```
--export-markdown "./reports/" looks like a directory path; provide a full file path
  Fix: specify a filename (e.g. --export-markdown ./traces/report.md)
```

### `--output-json`

**Validation:**
- Path must not end with `/` or `\`
- Produces a deterministic JSON envelope with `schema_version`, `generated_at`, and a nested `trace` object

**Schema version:**
The output envelope always embeds the current `schema_version` (e.g. `"1.0"`). The version is defined by the `CurrentJSONSchemaVersion` constant in the trace package — it is never hardcoded at call sites, so all paths stay in sync automatically when the schema evolves.

**Loading files back:**
`glassbox trace <file>` can load files written by `--output-json`. The loader detects the `schema_version` string envelope and validates it before parsing:

```
unsupported schema_version "99.0" in trace file "trace.json"
  This binary supports schema versions: "1.0"
  Fix: re-export the trace with the current CLI version, or upgrade Glassbox
```

Files written by a slightly older minor version produce a deprecation warning but load successfully:

```
Warning: trace file "old-trace.json" uses schema_version "0.9"; current is "1.0"
  Consider re-exporting with the current CLI for full compatibility
```

### `--export-svg`

**Validation:**
- Path must not end with `/` or `\`
- The trace must contain diagnostic events (error if empty, with remediation)

**Error example:**
```
no diagnostic events found in trace — call graph cannot be generated
  Possible causes:
    - The trace was captured without diagnostic events
    - The transaction did not call any contracts
  Fix: re-run with a transaction that includes contract calls
  Tip: use --trace-verbosity verbose when capturing the trace for maximum detail
```

### `--trace-verbosity`

**Valid values:** `summary`, `normal`, `verbose`

**Validation:**
- Must be one of the three supported values

**Error example:**
```
invalid --trace-verbosity "extreme" — must be one of: summary, normal, verbose
  Fix: use --trace-verbosity normal (default), summary (minimal), or verbose (detailed)
```

### `--annotations`

**Validation:**
- File must exist on disk

**Error example:**
```
--annotations: file not found: "/path/to/annotations.json"
  Fix: provide a valid path to an annotations JSON file
```

### `--gas-model`

**Validation:**
- File must exist on disk

**Error example:**
```
--gas-model: file not found: "/path/to/gas.json"
  Fix: provide a valid path to a gas model JSON file
```

### `--meta`

**Validation:**
- Every value must be in `key=value` format
- Key must not be empty

**Error example:**
```
--meta value "no-equals-sign" is not in key=value format
  Fix: supply metadata as key=value pairs, e.g. --meta env=testnet --meta version=1.2
```

### `--verify-export`

Verifies the integrity of an existing trace export file and exits. No trace
file argument is required — the command only reads the export artifact and its
companion `.meta.json` file.

```sh
glassbox trace --verify-export ./artifacts/trace.json
```

**Checks performed (in order):**

1. Metadata file exists (`.meta.json` alongside the export)
2. Metadata is valid JSON
3. `version` field in metadata is a recognized schema version
4. File checksum matches the SHA-256 digest recorded at export time
5. Step count in the trace file matches the count recorded in metadata (JSON format only)
6. File extension matches the declared format

**Success output:**

```
✓ Export integrity verified: ./artifacts/trace.json
```

**Failure output examples:**

```
export integrity check failed for "trace.json":
  checksum mismatch
    Expected: 3b4c5d...
    Actual:   9f2c41...
    The trace file may have been modified or corrupted
    Fix: re-export the trace with 'glassbox trace --export <file> --format <fmt> <trace>'
```

```
export integrity check failed for "trace.json":
  metadata file records unsupported schema_version "99.0"
    Supported versions: "1.0"
    Fix: re-export the trace with the current CLI version, or upgrade Glassbox
```

`--verify-export` exits with a non-zero code on any failure, making it suitable
for CI gates:

```sh
glassbox trace --verify-export ./artifacts/trace.json && echo "artifact OK"
```

**Validation:** The path must exist and be readable. A missing file is caught
in `PreRunE` alongside other flag errors.

### Multiple Validation Errors (`glassbox trace`)

All failures are collected and reported together:

```
2 trace command validation error(s):
  1. invalid --export-format "yaml" — must be one of: html, markdown, json, text
     Fix: use --export-format html (interactive), markdown (shareable), json (machine-readable), or text (plain)
  2. --export "./traces/" looks like a directory path; provide a full file path
     Fix: specify a filename (e.g. --export ./traces/output.html)
```

### Trace File Not Found (`glassbox trace`)

When the trace file argument does not exist:

```
trace file not found: "execution.json"
  Fix: verify the path is correct and the file exists
  Tip: trace files are produced by 'glassbox debug --trace-output <file>'
```

### No Trace File Supplied

```
trace file is required
  Usage: glassbox trace <trace-file>
  Or:    glassbox trace --file <trace-file>
  Run 'glassbox trace --help' for all available options
```

---

## Schema Stability and Upgrades

The `--output-json` flag produces a structured envelope with an explicit `schema_version` field. This section explains how Glassbox handles schema versioning, loading older files, and detecting incompatible versions.

### Schema Version Format

Schema versions use `MAJOR.MINOR` notation (e.g. `"1.0"`):

- **MAJOR** changes indicate breaking structural changes to the envelope or field layouts. Files from a different major version cannot be loaded.
- **MINOR** changes add new optional fields. Files from older minor versions load with a deprecation warning. Files from newer minor versions that are explicitly listed in `SupportedJSONSchemaVersions` also load successfully.

The current schema version constant is `CurrentJSONSchemaVersion = "1.0"`.

### Two JSON Envelope Formats

Glassbox uses two different JSON shapes depending on the export path:

| Flag | Envelope shape | Version field |
|------|----------------|---------------|
| `--output-json` | `{"schema_version":"1.0","generated_at":"...","trace":{...}}` | String: `"1.0"` |
| `--export --format json` | `{"version":{"major":1,"minor":0,"patch":0},"trace":{...}}` | Semver object |

`glassbox trace` detects and handles both shapes automatically. The detection is done by probing for the `schema_version` string key (ExportJSON shape) versus the `version` object key (VersionedTrace shape).

### Unsupported Schema Version Error

```
unsupported schema_version "99.0" in trace file "trace.json"
  This binary supports schema versions: "1.0"
  Fix: re-export the trace with the current CLI version, or upgrade Glassbox
```

### Older Minor Version Warning

```
Warning: trace file "old-trace.json" uses schema_version "0.9"; current is "1.0"
  Consider re-exporting with the current CLI for full compatibility
```

### Legacy File Warning (No Envelope)

Files produced before the schema envelope was introduced load with a warning:

```
Warning: loaded legacy trace format (no version info)
  Consider re-exporting with current version for full compatibility
```

### ExportJSON Schema Validation Function

`ValidateJSONSchemaVersion(version string) error` can be called independently to validate any schema version string before file I/O. It rejects:

- Empty or whitespace-only strings
- Strings not in `MAJOR.MINOR` format
- Non-numeric components
- Version strings not present in `SupportedJSONSchemaVersions`

All errors include a `Fix:` hint and reference the current expected version.

---

## Path Normalization and Safety

All trace export output paths go through a security-aware validation layer before any file I/O begins. This section describes what is checked, what errors are produced, and how the checks differ from a simple trailing-slash guard.

### What is validated

Every output path flag (`--export`, `--output-json`, `--export-svg`, `--export-markdown`, `--snapshot`) and every input path flag (`--annotations`, `--gas-model`) is now processed through:

1. **Null-byte rejection** — paths containing `\x00` are rejected immediately (shell injection risk)
2. **`filepath.Clean` + `filepath.Abs`** — resolves `.` and `..` components to their absolute form before any existence check
3. **Symlink resolution** — `filepath.EvalSymlinks` is called so that a symlink pointing outside an allowed root is caught, not just the raw string
4. **Existing-directory guard** — if the path already refers to a directory on disk, the write is rejected with a message that includes the flag name and a suggested filename

For input paths the validator additionally checks that the file exists and is not a directory.

### Error message format

All path errors include the flag name (`--export`, `--output-json`, etc.) so the failing flag is unambiguous:

**Null byte:**
```
--export: path contains null bytes and cannot be used: "/path/to/trace\x00.html"
```

**Existing directory:**
```
--output-json: "/traces" is a directory; provide a full file path (e.g. "/traces/output.json")
```

**Missing input file:**
```
--annotations: file not found: "/path/to/annotations.json"
  Check that the path is correct and the file exists
```

**Path traversal (`--trace-output` via `ValidateTraceInputs`):**
```
--trace-output "../../../etc/passwd" contains directory traversal sequences (..)
  Fix: use absolute paths or relative paths without '..' for security
  Example: use './output/trace.html' instead of '../output/trace.html'
```

### The traversal detection fix

The old traversal check used `strings.Contains(path, "..")` which produced false positives for filenames that legitimately contain double dots (e.g. `my..trace.html`) and could miss Windows-style traversal paths.

The new check uses `filepath.Clean` first:

```go
cleaned := filepath.Clean(outputPath)
if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
    // traversal detected
}
```

This correctly:
- Accepts `my..trace.html` (double dot in filename, not a traversal component)
- Rejects `../trace.html` and `../../etc/passwd` after cleaning
- Handles both POSIX (`/`) and Windows (`\`) separators

### Coverage by command

| Flag | Command | Validator |
|------|---------|-----------|
| `--export` | `trace` | `ValidateOutputPath` → `NormalizePath` |
| `--output-json` | `trace` | `ValidateOutputPath` → `NormalizePath` |
| `--export-svg` | `trace` | `ValidateOutputPath` → `NormalizePath` |
| `--export-markdown` | `trace` | `ValidateOutputPath` → `NormalizePath` |
| `--annotations` | `trace` | `ValidateInputPath` → `NormalizePath` |
| `--gas-model` | `trace` | `ValidateInputPath` → `NormalizePath` |
| trace file argument | `trace` | `ValidateInputPath` → `NormalizePath` |
| `--snapshot` (export) | `export` | `ValidateOutputPath` → `NormalizePath` |
| `--audit-log` | all | `ValidateOutputPath` → `NormalizePath` |
| `--trace-output` | `debug` | `ValidateTraceInputs` + `ValidateDebugOutputPaths` |
| `--save-snapshots`, `--export-svg` | `debug` | `ValidateDebugOutputPaths` → `NormalizePath` |

---

## Snapshot Reliability Validation

Before exporting traces that depend on snapshot data (sandboxed replay, step
navigation), Glassbox validates snapshot coverage using
`ValidateSnapshotForExport`. This surfaces snapshot problems early — before
the expensive export operation begins — so users get a clear, actionable error
rather than a partial or silently broken export.

### Validation checks

| Condition | Error produced |
|-----------|---------------|
| Simulation hit an OOM condition | Snapshot capture failed due to memory pressure |
| Steps executed but zero snapshots captured | No snapshots were captured |
| Snapshot count far below expected coverage | Sparse snapshot coverage warning |

### OOM error

```
snapshot capture failed due to memory pressure (OOM) — trace export may be incomplete
  The simulation ran out of memory before all snapshots could be saved.
  Fix: re-run with a smaller transaction or increase the simulator memory limit
  Tip: use --trace-verbosity summary to reduce memory usage during capture
```

### No snapshots captured

```
no snapshots were captured during simulation (250 steps executed, 0 snapshots) —
sandboxed replay and step-navigation will be unavailable
  Possible causes:
    - Snapshot interval is set too high (no step hit the interval)
    - Simulator version does not support snapshot capture
  Fix: lower --snapshot-interval or re-run with a simulator that supports snapshots
  Tip: run 'glassbox doctor' to check simulator snapshot support
```

### Sparse snapshot coverage

```
sparse snapshot coverage: 2 snapshot(s) for 1000 steps (expected at least 5) —
step-navigation may jump large gaps
  Fix: lower --snapshot-interval to capture more frequent snapshots
  Current coverage: 1 snapshot per ~500 steps
```

### Coverage threshold

The validator expects at minimum **1 snapshot per 200 steps**. This matches the
default snapshot interval. If you use a higher `--snapshot-interval`, the sparse
coverage warning may trigger for long-running transactions — lower the interval
or acknowledge the reduced navigation granularity.

---

---

## Export Integrity Verification (`VerifyExport`)

When a trace is exported with resilience options enabled (`ExportWithResilience`), a companion `.meta.json` file is written alongside the trace. This file records:

- `version` — the schema version of the export envelope
- `format` — the export format (`html`, `json`, `markdown`, `text`)
- `transaction_hash` — the transaction the trace belongs to
- `exported_at` — wall-clock time of the export
- `step_count` — number of execution steps in the trace at export time
- `checksum` — SHA-256 hex digest of the trace file content
- `cli_version` — Glassbox CLI version that produced the export
- `hostname` — machine that produced the export (omitted if unavailable)

### Verifying an export

Use `VerifyExport(tracePath)` (or the equivalent API call) to verify a previously exported trace:

```go
if err := trace.VerifyExport("./output/trace.json"); err != nil {
    fmt.Fprintf(os.Stderr, "Trace integrity check failed: %v\n", err)
}
```

**Checks performed:**

| Check | Error produced |
|-------|----------------|
| Metadata file missing | Descriptive note that the file can still be used but integrity cannot be verified |
| Metadata file corrupt | `failed to parse metadata file` with corruption hint |
| Metadata `version` field unsupported | `metadata file records unsupported schema_version` with supported list and upgrade hint |
| Checksum mismatch | `checksum mismatch` with expected/actual values and re-export hint |
| Step count mismatch (JSON only) | `step count mismatch` with recorded vs actual count and truncation hint |
| Format/extension mismatch | `format mismatch` with re-export hint |

**Metadata version error example:**

```
metadata file records unsupported schema_version "99.0"
  Supported versions: "1.0"
  Fix: re-export the trace with the current CLI version, or upgrade Glassbox
```

Metadata files produced before schema versioning was introduced carry an empty
`version` field.  These are accepted without error — the integrity checks
(checksum, step count, format) still apply.

**Step count mismatch error example:**

```
step count mismatch
  Metadata records 50 steps, trace file contains 12 steps
  The trace file may have been truncated, appended to, or partially overwritten
  Fix: re-export the trace with glassbox debug --trace-output
```

### Recovering a trace (`RecoverTrace`)

`RecoverTrace(tracePath)` performs best-effort recovery of a JSON trace export that may be partially corrupted or have mismatched metadata. It:

1. **Runs `VerifyExport` first** — surfaces any checksum or step-count mismatch as a warning before attempting content recovery. Missing metadata is silently accepted.
2. **Parses the JSON** with progressive tolerance (strict → lenient → unknown-fields allowed).
3. **Sanitizes** the recovered trace — fixes zero timestamps, step index mismatches, missing transaction hashes, and truncates excessively long error strings.
4. **Validates** the sanitized trace for structural correctness.

All warnings and repairs are returned as a slice of errors alongside the recovered trace object, so callers can surface the information at the appropriate severity level.

```go
recovered, warnings := trace.RecoverTrace("./corrupted/trace.json")
if recovered == nil {
    log.Fatalf("unrecoverable: %v", warnings)
}
for _, w := range warnings {
    fmt.Fprintf(os.Stderr, "Warning: %v\n", w)
}
// use recovered trace...
```

**Only JSON format exports can be recovered.** HTML, Markdown, and Text are presentation-only formats and cannot be parsed back to an `ExecutionTrace`. Always export in JSON format when recovery capability is required.

---

## Versioned JSON Export and Metadata

Glassbox provides a first-class versioned JSON envelope for trace exports that
embeds schema version and generation metadata.  Use this format when you need
reliable round-trip loading, schema compatibility checking, or audit trails.

### ExportJSON — Versioned Envelope Format

`(*ExecutionTrace).ExportJSON(schemaVersion string, generatedAt time.Time) ([]byte, error)`

Produces a JSON envelope of the form:

```json
{
  "schema_version": "1.0",
  "generated_at":   "2026-06-01T12:00:00Z",
  "trace": {
    "transaction_hash": "sha256:3b4c5d...",
    "start_time":       "2026-06-01T11:59:55Z",
    "end_time":         "2026-06-01T12:00:00Z",
    "states":           [ ... ],
    ...
  }
}
```

**Key properties:**

| Property | Detail |
|---|---|
| `schema_version` | Must be a `MAJOR.MINOR` string (e.g. `"1.0"`). Use `CurrentJSONSchemaVersion` in production code. Validated before writing — an invalid or unsupported version is rejected immediately. |
| `generated_at` | Truncated to second precision in UTC for deterministic output. |
| `trace.transaction_hash` | SHA-256 fingerprinted (`"sha256:<64 hex chars>"`) — the raw hash never appears in the file. |
| Determinism | Calling `ExportJSON` twice with identical inputs produces identical bytes. |
| Write-side validation | `schemaVersion` is validated with `ValidateJSONSchemaVersion` before any bytes are written, so callers learn about unsupported versions immediately. |

**Example (Go):**

```go
data, err := trace.ExportJSON(CurrentJSONSchemaVersion, time.Now())
if err != nil {
    return err
}
if err := os.WriteFile("trace.json", data, 0o644); err != nil {
    return err
}
```

**Write-side validation error example:**

```
ExportJSON called with invalid schema_version "99.0": schema_version "99.0" is not supported
  Supported versions: "1.0"
  Fix: use trace.CurrentJSONSchemaVersion as the schemaVersion argument
```

> **Note for tests:** If you need to deliberately produce a file with an
> unsupported schema version (e.g. to exercise error-handling paths in
> `LoadVersionedTrace`), use `(*ExecutionTrace).ExportJSONUnchecked` instead.
> This variant skips the write-side validation and is only intended for test
> code.

---

### Transaction Hash Fingerprinting

The raw transaction hash is replaced with its SHA-256 digest prefixed by
`"sha256:"` before writing.  This ensures:

- Sensitive or private transaction identifiers do not leak through shared
  export files.
- The fingerprint is a stable, collision-resistant reference.

An empty transaction hash produces the sentinel value `"sha256:(empty)"` rather
than the digest of an empty string, making empty-hash conditions unambiguous.

---

### SaveToFile — Legacy Plain Format

`(*ExecutionTrace).SaveToFile(path string) error`

Writes the trace as a plain `ExecutionTrace` JSON object (no envelope, no
schema version field).  Use this only for simple persistence or when
interfacing with older tooling that expects the legacy format.

```go
if err := trace.SaveToFile("./trace-plain.json"); err != nil {
    log.Fatal(err)
}
```

> **Note**: Files written by `SaveToFile` carry no version information.  When
> loaded by `LoadExecutionTrace`, a deprecation warning is printed to stderr
> advising the operator to re-export with the current CLI.

---

### LoadExecutionTrace — Universal Loader

`LoadExecutionTrace(path string) (*ExecutionTrace, error)`

Single entry-point that loads a trace file regardless of its envelope shape:

| File shape | Detected by |
|---|---|
| ExportJSON envelope | Top-level `"schema_version"` string key |
| VersionedTrace envelope | Top-level `"version"` object key |
| Plain ExecutionTrace JSON | Fallback — neither key present |

```go
trace, err := LoadExecutionTrace("./trace.json")
if err != nil {
    // Error includes the file path and references glassbox commands:
    // failed to load execution trace from "./trace.json": ...
    //   Verify the file was produced by a glassbox command such as:
    //     glassbox debug <tx-hash> --trace-output trace.json
    log.Fatal(err)
}
```

Internally, `LoadExecutionTrace` delegates to `LoadVersionedTrace` with
`DefaultCompatibilityOptions()`.  Schema version validation, version migration,
and deprecation warnings are all handled by that function.

---

### Schema Version Lifecycle

| Constant | Value | Purpose |
|---|---|---|
| `CurrentJSONSchemaVersion` | `"1.0"` | Version embedded by `ExportJSON` in production |
| `SupportedJSONSchemaVersions` | `["1.0"]` | Versions `LoadVersionedTrace` accepts without error |

When the schema evolves:

1. **Patch change** (documentation only) — no code change required; the
   `MAJOR.MINOR` pair is unchanged.
2. **Minor change** (new optional fields) — append the new version string to
   `SupportedJSONSchemaVersions`; files produced by older CLIs continue to
   load with a deprecation warning.
3. **Major change** (breaking) — bump `CurrentJSONSchemaVersion` major component
   and add migration logic to `migrateTrace`.

**Error example for unsupported version:**

```
unsupported schema_version "99.0" in trace file "./old-trace.json"
  This binary supports schema versions: "1.0"
  Fix: re-export the trace with the current CLI version, or upgrade Glassbox
```

---

---

## Export Failure Reference

The table below maps each common export failure class to its verified cause and
a safe corrective action. Every row is backed by the validation logic in
`ValidateTraceExportParams`, `ValidateTraceFormatCompatibility`,
`ValidateTraceInputs`, and `ExportWithResilience` (see `internal/trace/`).

No remedy in this table overwrites existing data silently.
`ExportWithResilience` uses an atomic write-rename by default, so the old file
is left intact if the new write fails. When `BackupExisting` is enabled a
`.bak.<timestamp>` copy is made before any overwrite.

### Path and filesystem failures

| Failure | Typical error message | Safe correction |
|---|---|---|
| Output path is a directory | `output path "…" looks like a directory path (ends with "/")` | Add a filename: `--export ./out/trace.html` instead of `--export ./out/` |
| Parent directory does not exist | `failed to create trace export directory: …` | The directory is created automatically (`os.MkdirAll`). If this error appears, your process lacks write permission on the parent — choose a writable path or fix permissions. |
| Parent directory is a file, not a directory | `output path parent "…" exists but is not a directory` | Remove the conflicting file or choose a different output path. |
| Output path contains null bytes | `output path contains null bytes` | Remove null bytes from the path. This is a shell-injection guard; the path came from user input that should be sanitised before it reaches the CLI. |
| Path traversal (`..`) detected | `contains directory traversal sequences (..)` | Use an absolute path or a relative path without `..` components, e.g. `./output/trace.html`. Detection uses `filepath.Clean` so double dots inside a filename (`my..trace.html`) are not flagged. |
| Permission denied on parent directory | `output directory "…" is not accessible — permission denied` | Check that your user has read and execute permissions on the parent directory: `ls -ld <parent>`. |
| Disk full during write | `failed to write trace export file: no space left on device` | Free disk space or choose a path on a filesystem with sufficient headroom. The atomic write path leaves no partial file behind. |
| File write failure (non-disk-full) | `failed to write trace export file: …` | Check filesystem mount options (read-only mounts) and process file-descriptor limits. |

### Format failures

| Failure | Typical error message | Safe correction |
|---|---|---|
| Unknown format string | `invalid or unsupported --export-format "yaml"` | Use one of `html`, `markdown`, `json`, `text`. `md` is also accepted as an alias for `markdown`. |
| Empty format string | `--export-format is empty` | Omit the flag (defaults to `html`) or set it explicitly. |
| Trace too large for HTML | `trace has N steps — too large for HTML export` | Switch to `--format json` for large traces, or use `--trace-verbosity summary` to reduce step count before export. |
| Trace too large for Markdown | `trace has N steps — markdown output will be extremely long` | Use `--format json` or filter the trace with `glassbox trace filter` first. |
| Step detail too large for HTML | `trace step N has excessively large detail fields` | Use `--format json` for traces whose steps carry large argument or error payloads. |
| Step index mismatch (JSON) | `trace step mismatch at position N: expected step N but got M` | Re-export or sanitize with `ExportWithResilience`, which repairs step indices automatically via `SanitizeTrace`. |
| Markdown code-fence in error field | `trace step N error contains markdown code fence markers` | Usually safe to ignore — the export still succeeds. Review the raw step detail if the rendered markdown looks malformed. |

### Trace data failures

| Failure | Typical error message | Safe correction |
|---|---|---|
| Nil trace | `trace is nil — cannot export a nil trace` | Verify the trace was loaded or deserialized correctly before calling export. Check for earlier load errors. |
| Empty trace (no steps) | `trace has no execution states` | Confirm the traced transaction actually executed contract code. Run `glassbox doctor` to check simulator compatibility. |
| Missing transaction hash | `trace has no transaction hash` | The trace was constructed without a hash. Re-run the debug or trace command against a valid transaction. |
| Zero start or end time | `trace start time is zero` / `trace end time is zero` | The trace was not properly initialized. Re-capture with the current CLI version. |
| End time before start time | `trace end time is before start time` | Timestamps were corrupted after capture. Re-export from the raw simulator output. |
| Non-monotonic step timestamps | `[accuracy] step N: timestamp … is before previous step timestamp` | This is a hard accuracy warning (not a fatal error). Re-capture the trace; timestamps come from the simulator clock, so a clock skew in the simulator produced the anomaly. |
| Step index integrity | `[accuracy] step N: index mismatch (state.Step=M)` | Use `ExportWithResilience` — `SanitizeTrace` renumbers steps to match their slice position before writing. |
| Unsupported JSON schema version | `schema_version "99.0" is not supported` | Re-export with the current CLI (`glassbox trace --output-json <file> <trace>`). Supported versions are listed in `SupportedJSONSchemaVersions`. |
| Legacy trace file (no schema envelope) | `Warning: loaded legacy trace format (no version info)` | Re-export to get a versioned envelope. The trace is still usable but lacks schema stability guarantees. |

### Export options failures

| Failure | Typical error message | Safe correction |
|---|---|---|
| Too many comments | `too many comments (N) — maximum is 100` | Reduce the comment count or split the review across multiple exports. The limit applies to free-form comments and reviewer comments combined. |
| Comment too long | `comment #N exceeds maximum length of 10000 characters` | Shorten the comment or split it. |
| Empty comment | `--comment index N is empty or whitespace-only` | Remove the empty `--comment` flag or provide non-empty text. |
| Invalid session metadata key | `session metadata key is empty or whitespace-only` | All `--meta key=value` pairs must have a non-empty key. |
| Invalid reviewer comment | `reviewer comment #N is invalid: …` | Fix the named field (see the error detail). Common causes: unknown severity value, `updated_at` before `created_at`, empty author or body. |

### Integrity verification failures (VerifyExport)

| Failure | Typical error message | Safe correction |
|---|---|---|
| Metadata file missing | `metadata file not found: trace.json.meta.json` | The export was not produced with `ExportWithResilience` (metadata disabled). The trace file is still usable but cannot be integrity-checked. Re-export with resilience options to get a verifiable artifact. |
| Checksum mismatch | `checksum mismatch — Expected: … Actual: …` | The trace file was modified after export. Re-export from the original source. **Do not use a checksum-mismatched file as an authoritative artifact.** |
| Step count mismatch | `step count mismatch — Metadata records N steps, trace file contains M` | The file was truncated, appended to, or partially overwritten. Re-export. The old file is left in place by `ExportWithResilience`'s atomic write, so the intact version may still exist at `<path>.bak.<timestamp>` if `BackupExisting` was enabled. |
| Unsupported schema version in metadata | `metadata file records unsupported schema_version "99.0"` | Upgrade the Glassbox CLI to a version that supports the recorded schema, or re-export with the current CLI. |
| Format/extension mismatch | `format mismatch: metadata says "json" but file extension is ".html"` | Rename the file to match its declared format, or re-export. |

---

## See Also

- [Debug Command Reference](./debug-command.md)
- [Trace Export Annotations](./trace-export-annotations.md)
- [Trace Profiling and Performance](./trace-profiling.md)
- [Event Schemas](./event-schemas.md)
- [JSON Output Format](./json-output.md)
- [Snapshot Deduplication](./snapshot-deduplication.md)
- [Sandboxed Replay](./sandboxed-replay.md)
