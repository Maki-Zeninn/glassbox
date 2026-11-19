# SARIF diagnostic rule families

Alongside the hash-derived security rules (`GB-XXXX`) documented in
[sarif-export.md](sarif-export.md), the SARIF writer emits two **enumerated**
diagnostic rule families for findings that are not `security.Finding` objects.

They are enumerated rather than derived because each one corresponds to a
distinct remediation, and because CI code scanning needs rule IDs that stay
stable across runs in order to track a rule's history.

> **Relationship to CLI error codes** — The GB1xxx and GB2xxx rules are
> *findings within a run*, not failure codes for the command itself. A run
> can exit 0 while still emitting GB1001 warnings, and a
> `SOURCE_DISCOVERY_FAILED` error code can appear without any GB1xxx result
> if source discovery failed before SARIF export ran. See
> [stable-error-codes.md § Cross-reference](stable-error-codes.md#cross-reference-error-codes-and-sarif-rules)
> for the full mapping between error codes and rule IDs.

## Source map diagnostics (GB1xxx)

Reported when a WASM instruction offset cannot be mapped back to a Rust source
line. Previously these were printed as human-readable warnings on stderr, so a
CI run produced no durable record of them.

| Rule | Name | Level | Meaning |
| ---- | ---- | ----- | ------- |
| `GB1001` | `SourceMapOffsetNotFound` | `warning` | No DWARF line table entry for the offset |
| `GB1002` | `SourceMapDwarfMissing` | `error` | The WASM binary has no usable debug sections at all |
| `GB1003` | `SourceMapNoGitRepository` | `note` | No Git repository detected, so no source links were generated |

`GB1002` is an error rather than a warning because it is not an isolated
mapping miss: without debug sections, *no* offset in the binary can be mapped,
so every failure in the run is unattributable.

### Location handling

A source map failure has no source file to point at — resolving the offset is
precisely what failed. Emitting an artifact location anyway would produce a
result pointing at a file that does not contain the problem, so these results
carry a SARIF `logicalLocation` keyed on the WASM offset instead:

```json
{
  "ruleId": "GB1001",
  "level": "warning",
  "message": { "text": "WASM offset 0x1a4c: no DWARF line table entry for this offset. Fix: build the contract with debug = true" },
  "locations": [
    {
      "logicalLocation": {
        "name": "0x1a4c",
        "kind": "wasmOffset",
        "fullyQualifiedName": "wasm::offset::0x1a4c"
      }
    }
  ]
}
```

`logicalLocation` is only emitted when there is genuinely no source file. When
the caller did resolve a location after all, the result carries both a
`physicalLocation` and the logical offset, so a consumer can use whichever it
has a base URI for.

## Security warnings (GB2xxx)

Reported for host function calls that are permitted but undesirable. The
deprecated list previously lived as a package-private variable inside
`internal/cmd`, so only `glassbox debug` could report it; it now lives in
`internal/security` and is shared with the LSP diagnostics publisher.

| Rule | Name | Level | Meaning |
| ---- | ---- | ----- | ------- |
| `GB2001` | `DeprecatedHostFunction` | `warning` | A Soroban host function scheduled for removal |
| `GB2002` | `AllowlistedUnsafeHostFunction` | `error` | A permitted host function used unsafely |

### Location handling

A security warning points at a real Rust source line when source mapping
succeeded, and falls back to the logical WASM offset when it did not — the
opposite of the source map failures, which never have a source file. When a
physical location is used, a `logicalLocation` of kind `hostFunction` carries
the function name alongside it.

## API

```go
log := sarif.ExportDiagnosticsWithFindings(
    findings,  // []security.Finding, as before
    failures,  // []sarif.SourceMapFailure
    warnings,  // []sarif.SecurityWarning
    sarif.DiagnosticOptions{ArtifactURI: "contract.wasm"},
)

encoded, err := sarif.Marshal(log)
```

`ExportDiagnostics` is the same call without security findings. Both are pure
functions: no I/O, and two exports of the same input are byte-identical, which
is what keeps a SARIF diff meaningful.

Results and rules are sorted by rule ID and message, and each result carries a
`primaryLocationLineHash/v1` fingerprint so a code-scanning platform can track
a finding across runs.

## Rule metadata

Every enumerated rule carries a `shortDescription`, a `fullDescription`, a
`helpUri` and a `defaultConfiguration.level`, plus `tags` and a `precision`
label. `sarif.RuleByID` looks one up, and `sarif.DiagnosticRules` returns them
all in rule-ID order.

---

## Concrete result example

The excerpt below is a single sanitized SARIF result for rule `GB2001`
(`DeprecatedHostFunction`) with source mapping resolved to a Rust source line.
It is representative of what `glassbox scan --format sarif` produces and can be
used to build parsers and CI integrations without reading the full SARIF
specification.

The example is drawn from the deterministic test input in
`internal/sarif/families_test.go` (`testWarnings()[0]`), which uses the same
code path as production output.

```json
{
  "ruleId": "GB2001",
  "level": "warning",
  "message": {
    "text": "bytes_new_from_linear_memory: removed in a future protocol release (src/lib.rs:88)"
  },
  "locations": [
    {
      "physicalLocation": {
        "artifactLocation": {
          "uri": "src/lib.rs"
        },
        "region": {
          "startLine": 88
        }
      },
      "logicalLocation": {
        "name": "bytes_new_from_linear_memory",
        "kind": "hostFunction"
      }
    }
  ],
  "fingerprints": {
    "primaryLocationLineHash/v1": "a3c9f1e24b870d56"
  }
}
```

### Field notes

| Field | Value in example | Notes |
|---|---|---|
| `ruleId` | `GB2001` | Registered enumerated rule ID — stable across runs. Not a hash-derived `GB-XXXX` security rule. |
| `level` | `warning` | `GB2001` default; `GB2002` (`AllowlistedUnsafeHostFunction`) uses `error`. |
| `message.text` | human-readable | Includes title, description, and resolved location. **Do not parse this string** — key on `ruleId` and `physicalLocation`. |
| `physicalLocation.artifactLocation.uri` | `src/lib.rs` | Present because source mapping resolved the WASM offset to a Rust source line. When mapping fails this field is absent and only `logicalLocation` is emitted (see the GB1001 example above). |
| `physicalLocation.region.startLine` | `88` | 1-based. Omitted entirely (rather than set to `0`) when the line is unknown, keeping the result schema-valid. |
| `logicalLocation.kind` | `hostFunction` | Carries the host function name when a physical location is present. For unmapped results the kind is `wasmOffset`. |
| `fingerprints` | `primaryLocationLineHash/v1` | 16-char hex prefix of SHA-256(`ruleId + \0 + message + \0 + locationUri + \0 + startLine`). Identical inputs always produce identical fingerprints; use this to deduplicate across runs. |

Missing fields (`suppressions`, `properties`, `partialFingerprints`) are omitted
here for clarity. They are included in production output when applicable — see
[sarif-export.md](sarif-export.md) for the full result schema.

---

## Cross-reference: rule IDs and CLI error codes

The SARIF rules and the `ErstErrorCode` values in `docs/stable-error-codes.md`
are parallel systems — a SARIF rule ID identifies a **finding type**, while an
error code identifies **why a command failed**. The table below lists the
verified relationships and explicitly marks cases where no direct mapping
exists.

| Rule | CLI error code | Relationship |
|---|---|---|
| `GB1001` (`SourceMapOffsetNotFound`) | `SOURCE_DISCOVERY_FAILED` | Related, not identical. `GB1001` is a per-offset warning inside a successful run. `SOURCE_DISCOVERY_FAILED` means the entire source-discovery pipeline was aborted before SARIF export. A run can emit `GB1001` warnings and still exit 0. |
| `GB1002` (`SourceMapDwarfMissing`) | `SOURCE_DISCOVERY_FAILED` | Related. Both indicate a contract built without debug symbols. `GB1002` is emitted when the binary has no DWARF sections; `SOURCE_DISCOVERY_FAILED` is the exit-path error when that condition causes the command to fail. Neither implies the other. |
| `GB1003` (`SourceMapNoGitRepository`) | *(no mapping)* | Informational `note` — does not cause command failure. No corresponding `ErstErrorCode`. |
| `GB2001` (`DeprecatedHostFunction`) | *(no mapping)* | A deprecation warning does not fail the simulation. No corresponding `ErstErrorCode`. A deprecated function call may be *correlated* with `SIMULATION_LOGIC_ERROR` if the function is removed in the active protocol version, but there is no guaranteed link. |
| `GB2002` (`AllowlistedUnsafeHostFunction`) | *(no mapping)* | An `error`-level SARIF finding, but the command still exits 0 when this is the only problem. No corresponding `ErstErrorCode`. |
| `GB-XXXX` (security findings) | *(no mapping)* | Hash-derived security rule IDs have no stable `ErstErrorCode` counterpart. They describe contract vulnerabilities, not command failures. |
