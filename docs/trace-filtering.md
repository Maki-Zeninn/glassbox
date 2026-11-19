# Trace Filtering

Large traces are difficult to inspect when you need to focus on a specific
contract, source location, event category, or failure path. The trace filter
reduces noise without changing the underlying evidence.

## Principles

- **Filters are AND-combined.** All specified criteria must match for a step
  to be included. Omitting a flag means "no restriction" for that dimension.
- **Original step IDs are preserved.** Filtered output always uses the same
  step numbers as the source trace. Filtering never renumbers steps.
- **Empty matches are successful results.** Zero matched steps is not an
  error — it is a deterministic, repeatable result.
- **The source trace is never mutated.** `ApplyFilter` returns a
  `FilteredTrace` view; the original `ExecutionTrace` is unchanged.
- **Active filters are always in the output header** so the result is
  self-describing.

## Available filters

| Flag / field | Type | Description |
|---|---|---|
| `--contract` | regex | Match `ContractID` |
| `--function` | regex | Match `Function` name |
| `--event-type` | enum | `trap`, `contract_call`, `host_function`, `auth` |
| `--severity` | enum | `error`, `warning`, `info`, `all` |
| `--source-file` | substring | Match `SourceFile` path |
| `--step-min` | int | Minimum step index (inclusive, 0-based) |
| `--step-max` | int | Maximum step index (inclusive, 0-based) |
| `--line-min` | int | Minimum source line number (inclusive) |
| `--line-max` | int | Maximum source line number (inclusive) |
| `--exclude` | bool | Invert: include steps that do **not** match |

### Source line range notes

- Steps with `SourceLine == 0` (line information not recorded, e.g. steps
  without DWARF debug info) **never** match a non-zero line range. This
  avoids false positives on steps that lack source mapping.
- `--line-min` and `--line-max` can be used independently (open-ended ranges
  are fine).

### Exclude mode

`--exclude` inverts the filter result. Combined with any other flag, it
means "show me everything **except** steps matching these criteria":

```sh
# Show everything except host_function noise
glassbox trace filter tx.json --event-type host_function --exclude
```

`--exclude` requires at least one other filter flag — `--exclude` alone
would exclude every step, which is almost certainly a mistake.

## Filter composition

### How multiple filters combine

All criteria on a single `FilterExpression` are **AND-combined**: every
non-zero field must match for a step to be included. Omitting a field places no
restriction on that dimension.

This is verified by `TestFilter_CombinedFilters` in
`internal/trace/filter_test.go`:

```go
// Three steps: (CA, fn1, error), (CA, fn2, ok), (CB, fn1, error)
// Filter: ContractID="CA" AND Severity="error"
// Result: 1 match — only (CA, fn1, error) passes both criteria.
expr := &FilterExpression{ContractID: "CA", Severity: FilterSeverityError}
```

The same rule applies at the CLI: each flag you supply narrows the result
further. There is no OR mode and no flag precedence — all flags are peers.

### Two-filter example

Suppose a 1 200-step trace contains calls across two contracts. You want only
the steps in `CTOKEN` that also fall between source lines 40 and 80 of
`token.rs`:

```sh
glassbox trace filter tx.json \
  --contract CTOKEN \
  --source-file token.rs \
  --line-min 40 \
  --line-max 80
```

Expected inclusion behavior, step by step:

| Step | ContractID | SourceFile | SourceLine | Included? | Reason |
|---|---|---|---|---|---|
| 0 | `CTOKEN` | `token.rs` | 55 | **yes** | all three criteria match |
| 1 | `CTOKEN` | `token.rs` | 12 | no | line 12 < `line-min` 40 |
| 2 | `CTOKEN` | `other.rs` | 55 | no | `source-file` does not match |
| 3 | `COTHER` | `token.rs` | 55 | no | `contract` does not match |
| 4 | `CTOKEN` | `token.rs` | 0 | no | `SourceLine == 0` never matches a non-zero line range (no source info recorded) |

This matches the behaviour verified by
`TestFilter_SourceFileAndLineRange_Combined` in
`internal/trace/filter_extended_test.go`.

The `filter_summary` block in JSON output shows every active criterion so the
result is self-describing:

```json
{
  "filter_summary": {
    "total_steps": 1200,
    "matched_count": 3,
    "match_ratio": 0.0025,
    "active_filters": {
      "contract_id": "CTOKEN",
      "source_file": "token.rs",
      "line_min": "40",
      "line_max": "80"
    },
    "exclude_mode": false
  }
}
```

### Composing expressions programmatically with `And()`

`FilterExpression.And(other)` merges two expressions into one. The rules
(from `filter.go` and verified by `TestFilter_And_*`):

- **Non-zero fields from `other` override the same field in `f`.** If both
  expressions set `ContractID`, `other`'s value wins.
- **Zero/empty fields in `other` leave `f`'s value unchanged.** Omitting a
  field in `other` does not clear it in `f`.
- **`Exclude: true` propagates.** If either expression has `Exclude` set, the
  combined expression excludes matching steps.
- **`And(nil)` is safe.** A nil receiver or nil argument is treated as an empty
  expression.

```go
// Start with a line range from one source
f1 := &FilterExpression{LineMin: 10}   // steps at line ≥ 10

// Narrow with an upper bound from another source
f2 := &FilterExpression{LineMax: 50}   // steps at line ≤ 50

combined := f1.And(f2)
// combined: LineMin=10, LineMax=50 — equivalent to --line-min 10 --line-max 50
// Verified by TestFilter_And_CombinesLineRanges
```

Another example — adding exclude mode after the fact:

```go
base := &FilterExpression{ContractID: "CNOISE"}
excl := &FilterExpression{Exclude: true}

// Show everything except CNOISE
result := base.And(excl)
// Verified by TestFilter_And_ExcludePropagates
```

### What `And()` does not do

- It does not OR criteria. There is no way to express "contract A or contract B"
  with a single `FilterExpression`. Run two separate filter passes and merge the
  results in your application if you need OR semantics.
- It does not validate the result. Call `Validate()` on the combined expression
  before passing it to `ApplyFilter`.

---

## CLI usage

```
glassbox trace filter <trace-file> [flags]
```

### Output formats

| `--format` | Description |
|---|---|
| `text` (default) | Plain text with active-filter header |
| `json` | Machine-readable JSON with `filter_summary` block |

Use `--output <file>` to write to a file instead of stdout.

### Examples

```sh
# All steps for a specific contract
glassbox trace filter tx.json --contract CAAAA

# Only error steps, as JSON for CI consumption
glassbox trace filter tx.json --severity error --format json

# Steps in a source line range
glassbox trace filter tx.json --source-file token.rs --line-min 40 --line-max 80

# All traps only
glassbox trace filter tx.json --event-type trap

# Combine contract and function (AND)
glassbox trace filter tx.json --contract CAAAA --function "transfer.*"

# Everything except a specific contract
glassbox trace filter tx.json --contract CNOISE --exclude

# Save filtered result to a file
glassbox trace filter tx.json --contract CAAAA --output ca_steps.json --format json
```

## JSON output format

```json
{
  "filter_summary": {
    "total_steps": 1200,
    "matched_count": 47,
    "match_ratio": 0.039,
    "active_filters": {
      "contract_id": "CAAAA",
      "severity": "error"
    },
    "exclude_mode": false
  },
  "matched_steps": [
    {
      "original_step": 14,
      "has_parent": true,
      "parent_step": 12,
      "state": { ... }
    }
  ]
}
```

- `original_step` is always the unmodified step index from the source trace.
- `has_parent` / `parent_step` provide call-nesting context so readers can
  understand the hierarchy even in a filtered view.
- `active_filters` lists only the criteria that were actually set.

## Text output format

```
Glassbox Filtered Trace
=======================

Filter summary:
  Matched:   47 / 1200 steps (3.9%)
  Filters:
    contract_id   = "CAAAA" (regex)
    severity      = "error"

Matched steps:
--------------

Step 14: contract_call
  Contract:  CAAAA
  Function:  transfer
  Source:    token.rs:55
  Error:     trap: integer overflow
```

## Programmatic API

```go
// Build an expression
expr := &trace.FilterExpression{
    ContractID: "CAAAA",  // compiled as regex on Validate()
    Severity:   trace.FilterSeverityError,
    LineMin:    40,
    LineMax:    80,
}
if err := expr.Validate(); err != nil {
    // invalid regex, bad severity, line_min > line_max, etc.
}

// Apply — never mutates the source trace
ft, err := trace.ApplyFilter(executionTrace, expr)

// Render
text, _ := trace.RenderFilteredText(ft)
jsonBytes, _ := trace.RenderFilteredJSON(ft)

// Compose filters with AND
combined := expr1.And(expr2)

// Metadata
meta := trace.FilterMetadataFromTrace(ft)
fmt.Printf("%d/%d steps matched (%.1f%%)\n",
    meta.MatchedCount, meta.TotalSteps, meta.MatchRatio*100)
```

## Implementation locations

| File | Purpose |
|---|---|
| `internal/trace/filter.go` | `FilterExpression`, `ApplyFilter`, `FilteredTrace`, `FilterMetadata` |
| `internal/trace/filter_render.go` | `RenderFilteredText`, `RenderFilteredJSON` |
| `internal/cmd/trace_filter.go` | `glassbox trace filter` CLI command |
| `internal/trace/filter_test.go` | Existing filter tests |
| `internal/trace/filter_extended_test.go` | Line-range, Exclude, nested calls, empty results, render tests |
