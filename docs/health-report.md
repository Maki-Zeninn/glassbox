# Repository Health and Stale-Code Report

`scripts/health-report.sh` scans the repository for stale markers, placeholder
implementations, unreferenced documentation, generated-file drift, and old
compatibility shims. It is designed to run in CI without treating every TODO
as a build failure.

## Running the report

```bash
# Human-readable output (default)
scripts/health-report.sh

# Machine-readable JSON (suitable for CI dashboards and automation)
scripts/health-report.sh --json

# Use a custom suppression file
scripts/health-report.sh --suppress /path/to/my-suppress.json
```

Environment variable shorthand:
```bash
HEALTH_REPORT_OUTPUT_FORMAT=json scripts/health-report.sh
```

## What the report scans

| Category | What is detected |
|----------|-----------------|
| `code_marker` | `TODO`, `FIXME`, `HACK`, `PLACEHOLDER`, `XXX`, `WORKAROUND` in Go and Rust files |
| `placeholder_impl` | Functions that `panic("not implemented")` or similar |
| `unreferenced_doc` | Markdown files in `docs/` not referenced by any Go, Markdown, or shell file |
| `generated_drift` | `*_gen.go` files whose source is newer (possible stale generation) |
| `compat_shim` | Code annotated with `deprecated`, `legacy`, `backcompat`, or removal hints |

Generated files (`*_gen.go`, `*.pb.go`), vendor directories, and `testdata/`
directories are excluded from all scans.

## Exit codes

| Exit code | Meaning |
|-----------|---------|
| `0` | Report ran successfully; no stale suppressions found |
| `1` | One or more suppressions have passed their expiry date |

Regular findings (TODOs, placeholders, etc.) do **not** cause a non-zero exit.
Only stale suppressions do, because they represent commitments that were made
but not honoured.

## Suppression file

To intentionally suppress a finding, create `.glassbox-health-suppress.json`
at the repository root (see `.glassbox-health-suppress.example.json`):

```json
[
  {
    "pattern": "TODO: remove after protocol v22 ships",
    "owner": "@alice",
    "expires": "2026-12-31",
    "reason": "Waiting for protocol v22 deployment before removing the shim.",
    "issue": "https://github.com/pugsley76/glassbox/issues/500"
  }
]
```

| Field | Required | Description |
|-------|----------|-------------|
| `pattern` | yes | Substring to match against the finding text |
| `owner` | no | GitHub handle responsible for the suppression |
| `expires` | yes | ISO 8601 date (`YYYY-MM-DD`); the suppression is stale after this date |
| `reason` | yes | Why this finding is intentional and not actionable right now |
| `issue` | no | Link to the tracking issue or removal plan |

### Stale suppressions

When a suppression's `expires` date has passed the script:

1. Prints a `STALE_SUPPRESS` warning to stderr.
2. Continues scanning (the suppressed finding is still reported).
3. Exits with code `1` so CI can alert the owner.

This prevents suppressions from accumulating silently over time without a
linked removal plan.

## Linking findings to issues

Every actionable finding should have a corresponding GitHub issue. The report
output includes file and line number so maintainers can open the file and link
it directly:

```
── code_marker ──────────────────────────────────────
  internal/sourcemap/resolver.go:42  // TODO: add retry logic for flaky registries
  internal/trace/printer.go:119      // FIXME: column alignment breaks on wide terminals
```

For findings without a linked issue, open one and either fix the item in the
next sprint or add a suppression entry with an appropriate expiry date.

## CI integration

Add the following step to your CI pipeline to surface health findings without
blocking merges:

```yaml
# .github/workflows/health.yml
- name: Repository health report
  run: scripts/health-report.sh --json > health-report.json
  continue-on-error: true   # findings don't block; only stale suppressions do

- name: Upload health report artifact
  uses: actions/upload-artifact@v4
  with:
    name: health-report
    path: health-report.json
```

To **block** on stale suppressions (recommended):

```yaml
- name: Repository health report
  run: scripts/health-report.sh
  # Exits 1 only when a suppression has passed its expiry date.
```
