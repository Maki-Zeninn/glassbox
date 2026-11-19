# SARIF Export

Glassbox can export security findings in SARIF 2.1.0 format, making them
importable into GitHub Advanced Security, VS Code's SARIF viewer extension, and
any other SARIF-compatible scanner.

## Supported SARIF version

**SARIF 2.1.0** — the OASIS standard published at
https://docs.oasis-open.org/sarif/sarif/v2.1.0/sarif-v2.1.0.html

The schema URI embedded in every generated file is:
```
https://json.schemastore.org/sarif-2.1.0.json
```

## Usage

### From `glassbox scan`

```sh
# Write SARIF to stdout
glassbox scan ./contracts/token --format sarif

# Write SARIF to a file
glassbox scan ./contracts/token --format sarif --output findings.sarif

# With ABI metadata for richer analysis
glassbox scan ./contracts/token --abi ./token.abi.json --format sarif --output findings.sarif
```

### From `glassbox report`

```sh
# Generate SARIF from a trace file
glassbox report --file trace.json --format sarif --output reports/
```

The `report --format sarif` path derives findings from execution errors in the
trace. For full static analysis coverage, prefer `glassbox scan`.

## Rule IDs

Every Glassbox finding type maps to a stable rule ID of the form `GB-XXXX`.

The numeric suffix is derived from the first 16 bits of the SHA-256 hash of
`<FindingType>:<Title>`, so:

- The same finding class always produces the same rule ID across runs.
- Different finding classes always produce different rule IDs.
- Rule IDs never need updating when new finding classes are added.

### Current rule ID assignments

Rule IDs are deterministic but not enumerated here — they are computed at
runtime and embedded in the SARIF output. To discover the rule ID for a
specific finding class, run `glassbox scan` and inspect the `rules` array in
the output.

## Fingerprints

Every result includes a `primaryLocationLineHash/v1` fingerprint:

```json
"fingerprints": {
  "primaryLocationLineHash/v1": "a3f8c12d4e5b6f70"
}
```

The fingerprint is the first 8 bytes (16 hex chars) of the SHA-256 of:

```
ruleId + \0 + messageText + \0 + locationUri + \0 + startLine
```

**Stability guarantee**: identical inputs always produce identical fingerprints.
This allows CI tools (GitHub Code Scanning, Semgrep, etc.) to deduplicate and
suppress noise across runs where only unrelated code changed.

## Suppressed findings

Findings suppressed via the Glassbox suppression registry appear in the SARIF
output with an explicit suppression record:

```json
{
  "ruleId": "GB-1234",
  "level": "warning",
  "message": { "text": "..." },
  "suppressions": [{
    "kind": "external",
    "status": "accepted",
    "justification": "Finding suppressed via Glassbox suppression registry"
  }],
  "properties": {
    "glassbox/suppressed": true
  }
}
```

Suppressed findings are never silently dropped — they are always present in the
output so reviewers can see the full picture.

## Glassbox-specific extension fields

Results carry a `properties` object with Glassbox-specific fields:

| Field | Type | Description |
|-------|------|-------------|
| `glassbox/findingType` | string | `VERIFIED_RISK` or `HEURISTIC_WARNING` |
| `glassbox/evidence` | string | Raw evidence string from the detector |
| `glassbox/suppressed` | bool | `true` when the finding was suppressed |

Rules carry `properties.precision` set to `high` for verified risks and
`medium` for heuristic warnings, matching SARIF convention.

## Importing into GitHub Advanced Security

Upload the SARIF file using the GitHub CLI or the Code Scanning API:

```sh
gh api \
  --method POST \
  -H "Accept: application/vnd.github+json" \
  /repos/{owner}/{repo}/code-scanning/sarifs \
  -f commit_sha="$(git rev-parse HEAD)" \
  -f ref="refs/heads/main" \
  --input findings.sarif
```

Or use the `github/codeql-action/upload-sarif@v3` GitHub Actions step:

```yaml
- name: Upload Glassbox SARIF
  uses: github/codeql-action/upload-sarif@v3
  with:
    sarif_file: findings.sarif
    category: glassbox-security
```

## CI integration example

```yaml
- name: Run Glassbox security scan
  run: |
    glassbox scan ./contracts \
      --format sarif \
      --output glassbox-findings.sarif

- name: Upload to GitHub Security tab
  uses: github/codeql-action/upload-sarif@v3
  if: always()
  with:
    sarif_file: glassbox-findings.sarif
```
