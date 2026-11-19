# CI Validation Dashboard

The `generate-dashboard` tool aggregates CI artifact streams produced by the
Glassbox workflows into a deterministic, privacy-safe validation dashboard
report. It provides maintainers with one view showing whether critical journeys
remain healthy across platforms, protocol versions, and artifact formats.

## Overview

The tool reads an **InputManifest** (a JSON file listing CI artifact paths),
ingests the artifacts, groups results by journey name, and writes a **Report**
JSON file. Reports can also be rendered as a text table for terminal inspection.

The aggregator is offline-capable: it can run from downloaded CI artifacts
without any network access or CI API calls.

## Usage

```sh
# Generate from a prepared input manifest
generate-dashboard --input manifest.json --output dashboard.json

# Text table for terminal inspection
generate-dashboard --input manifest.json --format text

# Allow missing artifacts (emit 'unknown' status instead of failing)
generate-dashboard --input manifest.json --offline

# Exit with code 1 when any journey failed (useful for CI gating)
generate-dashboard --input manifest.json --exit-on-fail

# Write to stdout (useful for piping)
generate-dashboard --input manifest.json --output -
```

## Input manifest schema

The input manifest is a JSON file with schema version `"1.0"`:

```json
{
  "schema_version": "1.0",
  "generated_at": "2026-09-25T00:00:00Z",
  "critical_journeys": [
    "go-unit",
    "go-integration",
    "ts-test",
    "validation-suite"
  ],
  "artifacts": [
    {
      "path": "ci-artifacts/unit/junit.xml",
      "kind": "junit",
      "platform": "ubuntu-latest",
      "commit": "abc1234567890abcdef",
      "run_id": "12345678",
      "job_name": "go-unit"
    },
    {
      "path": "ci-artifacts/unit/test-output.json",
      "kind": "gotestsum-json",
      "platform": "ubuntu-latest",
      "commit": "abc1234567890abcdef",
      "run_id": "12345678",
      "job_name": "go-unit"
    },
    {
      "path": "ci-artifacts/validation/summary.json",
      "kind": "validation-summary",
      "platform": "ubuntu-latest",
      "commit": "abc1234567890abcdef",
      "run_id": "12345678"
    }
  ]
}
```

### ArtifactSource fields

| Field | Required | Description |
|-------|----------|-------------|
| `path` | yes | Filesystem path to the artifact file |
| `kind` | yes | One of: `junit`, `gotestsum-json`, `validation-summary`, `coverage` |
| `platform` | no | OS/arch label (e.g. `ubuntu-latest`, `windows-latest`) |
| `commit` | no | Git commit SHA this artifact corresponds to |
| `run_id` | no | CI run identifier (e.g. GitHub Actions `run_id`) |
| `job_name` | no | CI job name; used as the journey name when set |

### Supported artifact kinds

| Kind | Source | Format |
|------|--------|--------|
| `junit` | `gotestsum --junitfile` | JUnit XML (`<testsuites>` root) |
| `gotestsum-json` | `gotestsum --jsonfile` | Newline-delimited JSON events |
| `validation-summary` | `scripts/run-validation-suite.sh` | JSON `{"journeys":[...]}` |
| `coverage` | `go test -coverprofile` | `.out` coverage profile (counted only) |

## Output report schema

```json
{
  "schema_version": "1.0",
  "generated_at": "2026-09-25T12:00:00Z",
  "commit": "abc1234567890abcdef",
  "total_journeys": 4,
  "failed_journeys": 1,
  "all_passed": false,
  "journeys": [
    {
      "name": "go-unit/ubuntu-latest",
      "status": "passed",
      "platform": "ubuntu-latest",
      "commit": "abc1234",
      "duration_ms": 12500,
      "pass_count": 147,
      "fail_count": 0,
      "skip_count": 3,
      "artifact_kind": "junit",
      "run_id": "12345678",
      "fingerprint": "a3f8c12d4e5b"
    }
  ]
}
```

### JourneyRecord fields

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Stable journey identifier (`job_name/platform` or filename-derived) |
| `status` | string | `passed`, `failed`, `skipped`, or `unknown` |
| `platform` | string | OS/arch this record was collected on |
| `commit` | string | Git commit SHA |
| `duration_ms` | int | Wall-clock duration in milliseconds |
| `pass_count` | int | Number of passing tests/checks |
| `fail_count` | int | Number of failing tests/checks |
| `skip_count` | int | Number of skipped tests/checks |
| `failure_link` | string | Basename of failure artifact (never a full path) |
| `issue_ref` | string | Optional tracking issue URL for persistent failures |
| `artifact_kind` | string | Source artifact type |
| `run_id` | string | CI run that produced this record |
| `fingerprint` | string | 12-char hex, stable for same name/status/commit/platform |

## Critical journeys

List journey names in `critical_journeys` to ensure they always appear in the
output. Any journey listed that has no corresponding artifact entry is emitted
with `status: "unknown"` so gaps are visible rather than silent.

**Agreed critical journeys for quarterly review:**

| Journey name | Coverage |
|-------------|----------|
| `go-unit` | Go unit tests across all platforms |
| `go-integration` | Go integration tests (ubuntu) |
| `ts-test` | TypeScript/Node tests |
| `validation-suite` | Quarterly critical-path validation |
| `offline-mode` | Offline replay without network |
| `protocol-v22` | Protocol version 22 compatibility |
| `security-providers` | Signing provider compatibility |
| `artifact-formats` | XDR/JSON artifact format validation |

## Privacy guarantees

The dashboard report **never** includes:

- Absolute filesystem paths (basenames only)
- Secrets, tokens, PEM blocks, or connection strings (redacted to `[REDACTED]`)
- Test output bodies (only pass/fail/skip counts and durations)
- Full environment variable values
- Full CI payloads

Failure links contain only the basename of the artifact file, not the full path.

## Fingerprints

Every journey record has a `fingerprint`: a 12-char hex value derived from the
SHA-256 of `name + status + commit + platform`. Fingerprints are:

- **Stable** when the journey result and commit are unchanged
- **Different** when status or commit changes
- **Independent** of timestamps and durations

This allows historical reports to be diffed by fingerprint to identify when a
journey first started failing.

## Determinism

Given the same input artifacts, `generate-dashboard` always produces the same
output:

- Journey records are sorted by name (ascending)
- Fingerprints are keyed on stable fields only
- `generated_at` reflects wall-clock time but does not affect fingerprints
- Multiple artifacts for the same journey are merged deterministically
  (failure in any platform → journey fails)

## GitHub Actions integration

```yaml
- name: Download all CI artifacts
  uses: actions/download-artifact@v4
  with:
    path: ci-artifacts/

- name: Generate validation dashboard
  run: |
    # Build the input manifest from downloaded artifacts
    cat > manifest.json << 'EOF'
    {
      "schema_version": "1.0",
      "generated_at": "${{ steps.date.outputs.date }}",
      "critical_journeys": ["go-unit", "go-integration", "ts-test"],
      "artifacts": [
        {
          "path": "ci-artifacts/failure-unit-ubuntu-latest-${{ github.run_id }}/junit.xml",
          "kind": "junit",
          "platform": "ubuntu-latest",
          "commit": "${{ github.sha }}",
          "run_id": "${{ github.run_id }}",
          "job_name": "go-unit"
        }
      ]
    }
    EOF
    go run ./cmd/generate-dashboard --input manifest.json \
      --output dashboard.json --offline

- name: Upload dashboard
  uses: actions/upload-artifact@v4
  with:
    name: validation-dashboard-${{ github.run_id }}
    path: dashboard.json
    retention-days: 90
```

## Offline generation from downloaded artifacts

```sh
# Download artifacts from a specific run
gh run download 12345678 --dir ci-artifacts/

# Write the input manifest
cat > manifest.json << 'EOF'
{
  "schema_version": "1.0",
  "generated_at": "2026-09-25T00:00:00Z",
  "critical_journeys": ["go-unit", "go-integration"],
  "artifacts": [
    {
      "path": "ci-artifacts/failure-unit-ubuntu-latest-12345678/junit.xml",
      "kind": "junit",
      "platform": "ubuntu-latest",
      "job_name": "go-unit"
    }
  ]
}
EOF

# Generate
go run ./cmd/generate-dashboard --input manifest.json --format text
```

## Quarterly review process

1. Download the last four validation-suite artifacts (`validation-*` from GitHub).
2. Write an InputManifest referencing each file with the appropriate `commit` and `platform`.
3. Run `generate-dashboard --input manifest.json --output quarterly-review.json`.
4. Review `quarterly-review.json` — any `failed` or `unknown` journeys need a tracking issue in `issue_ref`.
5. Archive `quarterly-review.json` to the `docs/` directory under a date-stamped name.

Historical reports are deterministic and privacy-safe, making them safe to
commit to the repository for long-term trend tracking.
