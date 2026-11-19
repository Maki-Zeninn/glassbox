# Trace Fixtures

Serialised `ExecutionTrace` JSON files used by trace-layer regression tests.

## File naming

```
trace_<scenario-slug>_<issue-or-pr-slug>.trace.json
```

Examples:
- `trace_empty_steps_pr319.trace.json`
- `trace_deprecated_host_fn_issue444.trace.json`
- `trace_inlined_chain_missing_source_issue512.trace.json`

## Format

Each file is a JSON object matching the shape produced by
`trace.ExecutionTrace.ExportJSON()` with `schema_version` present.  Only the
fields required to exercise the specific failure class need to be populated.

Required top-level fields:

```json
{
  "schema_version": "2.0.0",
  "transaction_hash": "5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab",
  "start_time": "2026-01-01T00:00:00Z",
  "end_time": "2026-01-01T00:00:00Z",
  "states": []
}
```

## Canonical stub values

| Field             | Value                                                                |
|-------------------|----------------------------------------------------------------------|
| transaction_hash  | `5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab`   |
| start_time        | `2026-01-01T00:00:00Z`                                               |
| contract_id       | `CTEST00000000000000000000000000000000000000000000000000000001`       |
| schema_version    | `2.0.0`                                                              |

## Rules

- Zero-state traces are valid fixtures for the "empty trace silently exported"
  failure class — keep `states: []` and add a `_comment` key.
- No real transaction hashes, contract IDs, or WASM bytecode from production.
- Generated-path frames must use build-directory patterns
  (`target/wasm32-unknown-unknown/release/`) so origin-classification tests
  can distinguish them from user-authored source.
