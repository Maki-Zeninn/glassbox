# Normalized Trace Comparison Guide

## Overview

The normalized trace comparison system provides structured, threshold-based analysis of execution trace differences. It focuses on material resource regressions while suppressing nondeterministic fields that create false positives.

## Key Features

- **Normalized Categories**: CPU, memory, host calls, events, and call paths
- **Percentage & Absolute Deltas**: Both relative and absolute change metrics
- **Threshold Configuration**: Configurable regression thresholds per category
- **Nondeterministic Field Suppression**: Automatic suppression of timestamps, IDs, and other unstable fields
- **Multiple Output Formats**: Human-readable tables and schema-versioned JSON
- **CI Integration**: Exit status control for automated testing
- **Informational Mode**: Non-failing mode for exploratory analysis

## Usage

### Basic Comparison

```bash
glassbox trace compare baseline.json current.json
```

### With Custom Thresholds

```bash
glassbox trace compare baseline.json current.json \
  --cpu-threshold 15.0 \
  --memory-threshold 20.0
```

### JSON Output for CI/CD

```bash
glassbox trace compare baseline.json current.json --format json
```

### Informational Mode (Never Fails)

```bash
glassbox trace compare baseline.json current.json --info
```

### With Configuration File

```bash
glassbox trace compare baseline.json current.json --config comparison-config.json
```

### CI Mode with Exit Status

```bash
glassbox trace compare baseline.json current.json --fail-on-violation
```

## Configuration File Format

Create a JSON configuration file to customize comparison behavior:

```json
{
  "cpu_threshold_pct": 10.0,
  "memory_threshold_pct": 10.0,
  "host_call_threshold_pct": 5.0,
  "event_count_threshold_pct": 0.0,
  "cpu_absolute_threshold": 1000,
  "memory_absolute_threshold": 1024,
  "suppressed_fields": [
    "timestamp",
    "sequence_id",
    "parent_sequence_id",
    "wasm_instruction"
  ],
  "fail_on_threshold_violation": false
}
```

### Configuration Parameters

- **cpu_threshold_pct**: CPU regression threshold percentage (default: 10.0)
- **memory_threshold_pct**: Memory regression threshold percentage (default: 10.0)
- **host_call_threshold_pct**: Host call regression threshold percentage (default: 5.0)
- **event_count_threshold_pct**: Event count regression threshold percentage (default: 0.0)
- **cpu_absolute_threshold**: Minimum CPU delta to report in instructions (default: 1000)
- **memory_absolute_threshold**: Minimum memory delta to report in bytes (default: 1024)
- **suppressed_fields**: Fields to exclude from comparison (nondeterministic)
- **fail_on_threshold_violation**: Whether to exit with error on threshold violations

## Output Interpretation

### Table Output

The table output groups differences by category:

```
════════════════════════════════ Normalized Trace Comparison ════════════════════════════════
  Baseline vs Current
  Schema Version: 1.0.0

── CPU ──
Path                                    Baseline     Current      Abs Delta     % Delta   Severity
──────────────────────────────────────────────────────────────────────────────────────────────────────────────
total_cpu_instructions                  100000       115000        15000         15.0%     warning

── MEMORY ──
Path                                    Baseline     Current      Abs Delta     % Delta   Severity
──────────────────────────────────────────────────────────────────────────────────────────────────────────────
total_memory_bytes                      50000        55000         5000          10.0%     warning

── HOST_CALLS ──
Path                                    Baseline     Current      Abs Delta     % Delta   Severity
──────────────────────────────────────────────────────────────────────────────────────────────────────────────
total_host_calls                        50           55            5             10.0%     warning
host_call::get_ledger_value             30           35            5             16.7%     warning
host_call::put_ledger_value             20           20            0             0.0%      info

── Summary ──
  Total Differences:      4
  Critical:               0
  Warnings:               4
  Info:                   0
  Threshold Violations:   2

  ⚠️  REGRESSION DETECTED
```

### Severity Levels

- **critical**: Delta exceeds 2x the threshold (indicating significant regression)
- **warning**: Delta exceeds threshold but is below 2x (potential regression)
- **info**: Delta below threshold or decrease (informational only)

### JSON Output

JSON output includes schema versioning for programmatic consumption:

```json
{
  "schema_version": "1.0.0",
  "baseline_name": "Baseline",
  "current_name": "Current",
  "config": {
    "cpu_threshold_pct": 10.0,
    "memory_threshold_pct": 10.0,
    "host_call_threshold_pct": 5.0,
    "event_count_threshold_pct": 0.0,
    "cpu_absolute_threshold": 1000,
    "memory_absolute_threshold": 1024,
    "suppressed_fields": [
      "timestamp",
      "sequence_id",
      "parent_sequence_id",
      "wasm_instruction"
    ],
    "fail_on_threshold_violation": false
  },
  "diffs": [
    {
      "category": "cpu",
      "path": "total_cpu_instructions",
      "baseline": 100000,
      "current": 115000,
      "absolute_delta": 15000,
      "percent_delta": 15.0,
      "severity": "warning",
      "reason": "CPU instructions changed from 100000 to 115000"
    }
  ],
  "summary": {
    "total_diffs": 4,
    "critical_diffs": 0,
    "warning_diffs": 4,
    "info_diffs": 0,
    "threshold_violations": 2
  },
  "has_regression": true
}
```

## Missing Metrics

### When Metrics Are Missing

Some traces may not contain cost annotations (CPU, memory, operations). This occurs when:

1. Traces were generated before cost annotation was implemented
2. Traces were generated with older simulator versions
3. Cost annotations were explicitly disabled during trace generation

### Interpretation

- **Missing CPU/Memory**: The comparison will report 0 for these metrics. This is treated as "no data available" rather than a regression.
- **Missing Host Calls**: If no host function calls are present in either trace, the comparison will report no differences.
- **Missing Event Counts**: This is rare as event counts are derived from trace structure, but if both traces are empty, no comparison is performed.

### Recommendations

- Ensure traces are generated with the latest simulator version for complete metrics
- Use `--info` mode when comparing traces with incomplete metrics to avoid false regressions
- Consider re-generating baseline traces if critical metrics are missing

## Changed Limits

### Protocol Version Changes

When comparing traces from different protocol versions:

1. **Budget Limits May Change**: Protocol upgrades often adjust CPU/memory limits
2. **Host Function Costs May Change**: New protocols may introduce different cost models
3. **Comparison Still Valid**: The normalized comparison focuses on relative changes, not absolute limits

### Interpretation

- **Absolute Thresholds**: May need adjustment when comparing across protocol versions
- **Percentage Thresholds**: Generally remain valid as they measure relative change
- **Call Path Changes**: May indicate protocol-level behavioral changes

### Recommendations

- Use percentage-based thresholds when comparing across protocol versions
- Consider protocol version when interpreting absolute resource changes
- Document protocol version differences in baseline documentation

## Nondeterministic Fields

The following fields are automatically suppressed to prevent false positives:

- `timestamp`: Execution timestamps vary between runs
- `sequence_id`: Auto-generated sequence identifiers
- `parent_sequence_id`: Parent relationship identifiers
- `wasm_instruction`: Low-level instruction addresses may shift

### Adding Custom Suppressions

Add additional fields to suppress in your configuration file:

```json
{
  "suppressed_fields": [
    "timestamp",
    "sequence_id",
    "parent_sequence_id",
    "wasm_instruction",
    "custom_field_to_ignore"
  ]
}
```

## CI/CD Integration

### Exit Status Codes

- **0**: No regressions detected or informational mode
- **1**: Regression detected with `--fail-on-violation` flag
- **2**: Error (invalid arguments, file not found, etc.)

### Example CI Configuration

```yaml
# GitHub Actions example
- name: Compare traces
  run: |
    glassbox trace compare baseline.json current.json \
      --format json \
      --fail-on-violation \
      --cpu-threshold 15.0 \
      --memory-threshold 15.0 \
      > comparison.json
    
    # Upload results for review
    artifact upload comparison.json
```

### Best Practices for CI

1. **Use JSON Output**: Easier to parse and archive in CI systems
2. **Set Appropriate Thresholds**: Adjust based on your project's tolerance for regressions
3. **Archive Results**: Store comparison results for historical analysis
4. **Use Baselines from Stable Branches**: Compare against known-good baselines
5. **Informational Mode for PRs**: Use `--info` in pull requests to avoid blocking on minor changes

## Interpreting Differences

Not every difference reported by `CompareTracesNormalized` is a regression.
The examples below show one meaningful difference and one benign difference,
drawn from the test fixtures in `internal/trace/normalized_compare_test.go`.

### Meaningful difference — CPU regression

**Setup:** The baseline trace has each step annotated with 100 CPU instructions
(10 steps × 10 cpu/step = 1 000 total). The current trace has the same step
count but 20% more CPU per step (1 200 total). This reproduces
`TestCompareTracesNormalized_CPURegression`.

```
── CPU ──
Path                     Baseline     Current   Abs Delta   % Delta   Severity
────────────────────────────────────────────────────────────────────────────────
total_cpu_instructions     1000         1200        200       20.0%    warning
```

**Why this is meaningful:** The delta (200 instructions, 20.0%) exceeds the
default `cpu_threshold_pct` of 10.0%. That means the execution path genuinely
consumed more resources — likely a new code branch was taken, an extra
allocation occurred, or a host function was called more times. Investigate by
running `glassbox trace filter` on both traces with `--event-type host_function`
to see whether a particular host call accounts for the increase.

Severity scales with how far above the threshold the delta is:

- **warning** — delta ≥ threshold (10%) and < 2× threshold (20%)
- **critical** — delta ≥ 2× threshold (≥ 20%); see
  `TestCompareTracesNormalized_CriticalSeverity` (30% increase → critical)

A regression with `HasRegression: true` only appears in the JSON output when at
least one `critical` diff exists, or when `fail_on_threshold_violation` is
`true` and any threshold is exceeded.

### Benign difference — step count change from verbosity

**Setup:** The baseline was captured with `--trace-verbosity verbose`
(15 steps). The current run used `--trace-verbosity normal` (10 steps). This
produces a diff in the `events` category.

```
── EVENTS ──
Path           Baseline   Current   Abs Delta   % Delta   Severity
───────────────────────────────────────────────────────────────────
total_events      15         10         -5       -33.3%    info
```

**Why this is benign:** The severity is `info` because the step count
*decreased* — `determineSeverity` only escalates to `warning` or `critical` for
increases (`delta > 0`). The execution behavior is unchanged; fewer steps were
emitted because the verbosity level was lowered. The guide does not classify
every diff as a regression.

Common benign patterns to recognise:

| Diff | Likely cause | How to confirm |
|---|---|---|
| `total_events` changes, no CPU/memory change | Different `--trace-verbosity` between runs | Compare the verbosity flags used for each capture |
| `call_path` added, severity `warning` | A new contract path appeared (may be intended) | Review the new path name; `warning` is used because the baseline had no data, not because cost increased |
| `call_path` removed, severity `critical` | A previously-executed path is gone | Check whether the transaction logic changed or the baseline was captured from a different code version |
| Small CPU/memory deltas filtered by absolute threshold | Normal JIT or scheduler noise | Delta below `cpu_absolute_threshold` (default 1 000 instructions) or `memory_absolute_threshold` (default 1 KB) is suppressed entirely |

### When to use filters or canonicalization

Use `--info` (informational mode) when:

- Comparing traces from different verbosity levels — step counts will differ but
  resource deltas are what matter.
- Comparing across protocol versions — budget limits may change, so percentage
  thresholds are more stable than absolute ones.

Use the `suppressed_fields` configuration key when a field produces noise
without carrying signal. The defaults (`timestamp`, `sequence_id`,
`parent_sequence_id`, `wasm_instruction`) suppress fields whose values shift
between runs for reasons unrelated to execution behavior. Add custom fields
the same way:

```json
{
  "suppressed_fields": [
    "timestamp",
    "sequence_id",
    "parent_sequence_id",
    "wasm_instruction",
    "your_noisy_field"
  ]
}
```

Canonicalization through `--info` and `suppressed_fields` is appropriate when
you want the comparison to focus on resource regressions and ignore structural
metadata. It does not alter the underlying traces.

---

## Troubleshooting

### No Differences Reported

If you expect differences but none are reported:

1. Check that traces contain cost annotations
2. Verify absolute thresholds aren't filtering out small changes
3. Ensure suppressed fields aren't hiding relevant differences
4. Use `--info` mode to see all changes regardless of severity

### False Positives

If you're seeing false positive regressions:

1. Increase thresholds for noisy categories
2. Add fields to suppression list if they're nondeterministic
3. Use absolute thresholds to filter small changes
4. Verify traces are from comparable protocol versions

### Missing Categories

If certain comparison categories are missing:

1. Ensure traces contain the required data (cost annotations for CPU/memory)
2. Check that traces have host function calls for host call comparison
3. Verify traces have execution states for event count comparison

## Schema Versioning

JSON output includes a `schema_version` field for compatibility:

- **Current Version**: 1.0.0
- **Breaking Changes**: Will increment major version
- **Additive Changes**: Will increment minor version
- **Bug Fixes**: Will increment patch version

When consuming JSON output programmatically, check the schema version to ensure compatibility with your parser.
