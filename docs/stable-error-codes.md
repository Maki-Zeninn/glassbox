# Stable Error Code Policy

Glassbox commands surface failures through a shared error type so that
automation scripts and CI pipelines can key on a **stable code** rather than
parsing free-form text.

## Error type

Every structured error is an `*errors.ErstError` (defined in
`internal/errors/errors.go`):

```go
type ErstError struct {
    Code    ErstErrorCode  // stable string, e.g. "RPC_CONNECTION_FAILED"
    Message string         // human-readable; may change between releases
    OrigErr error          // original cause, preserved via Unwrap()
    Hint    string         // optional actionable remediation for the user
}
```

- `Code` is the **automation contract** — do not key on `Message`.
- `Hint` is never included in `Error()` so it does not pollute log entries.
  It is surfaced by `errors.Hint(err)` and printed separately.
- `OrigErr` is returned by `Unwrap()` so `errors.Is` / `errors.As` chains
  work transparently.

## Output adapters (internal/clioutput)

| Function | When to use |
|---|---|
| `clioutput.WriteError(w, cmd, err, ctx)` | JSON mode — writes a schema-versioned envelope with `error.code` |
| `clioutput.FormatErrorText(err)` | Text mode — returns `[CODE] message\nHint: …` |

Both adapters extract the same `Code` from the error, so text and JSON modes
are always consistent.

### JSON envelope shape

```jsonc
{
  "schema_version": "1.0",
  "glassbox_version": "1.2.3",
  "generated_at": "2026-07-24T12:00:00Z",
  "command": "debug",
  "data": {
    "error": {
      "code": "RPC_CONNECTION_FAILED",   // ← stable, key on this
      "severity": "error",
      "message": "RPC connection failed: dial tcp …",
      "remediation": "Check your internet connection …",
      "context": {
        "network": "testnet"
      }
    }
  }
}
```

## Stable code catalogue

### General

| Code | Meaning | Exit |
|---|---|---|
| `VALIDATION_FAILED` | Bad flag, argument, or config value | 1 |
| `ARGUMENT_REQUIRED` | Required flag or positional arg missing | 1 |
| `CONFIG_ERROR` | Config file unreadable or invalid | 2 |
| `UNKNOWN` | Unclassified error | 3 |

### RPC

| Code | Meaning | Exit |
|---|---|---|
| `RPC_CONNECTION_FAILED` | Endpoint unreachable | 3 |
| `RPC_TIMEOUT` | Request timed out | 3 |
| `ALL_RPC_FAILED` | All failover endpoints failed | 3 |
| `RPC_ERROR` | RPC server returned a non-200 / error response | 3 |
| `TRANSACTION_NOT_FOUND` | TX hash not found on the selected network | 1 |
| `LEDGER_NOT_FOUND` | Ledger sequence unavailable | 1 |
| `LEDGER_ARCHIVED` | Ledger has been archived | 1 |
| `RATE_LIMIT_EXCEEDED` | RPC rate limit hit | 1 |
| `UNAUTHORIZED` | Missing or invalid auth token | 1 |
| `INVALID_NETWORK` | Unrecognised `--network` value | 1 |
| `NETWORK_NOT_FOUND` | Named network has no known endpoint | 1 |

### Simulator

| Code | Meaning | Exit |
|---|---|---|
| `SIMULATOR_NOT_FOUND` | Simulator binary absent or not executable | 2 |
| `SIMULATION_FAILED` | Simulator process returned non-zero | 3 |
| `SIMULATOR_CRASH` | Simulator process crashed unexpectedly | 3 |
| `SIMULATION_LOGIC_ERROR` | Contract logic error detected | 1 |

### Other

| Code | Meaning | Exit |
|---|---|---|
| `SOURCE_DISCOVERY_FAILED` | Source mapping unavailable for contract | 3 |

### Session concurrency (#813)

| Code | Meaning | Exit |
|---|---|---|
| `SESSION_WRITE_CONFLICT` | A concurrent writer saved a newer revision; reload or use `--force` | 1 |
| `SESSION_LOCK_HELD` | Advisory lock held by a live process; retry after it exits | 1 |

---

## Cross-reference: error codes and SARIF rules

The same failure can surface in two forms: as a CLI error code in `--json`
output and as a SARIF rule ID in `--format sarif` output. These are
**related but not identical**: an error code describes why a command failed,
while a SARIF rule ID describes a specific class of finding inside a run.

No runtime mapping is performed between the two systems — the relationship is
informational only, to help you correlate outputs without mistaking one for
the other.

| ErstErrorCode | Related SARIF rule(s) | Nature of relationship |
|---|---|---|
| `SOURCE_DISCOVERY_FAILED` | `GB1002` (`SourceMapDwarfMissing`) | Both indicate a contract without usable debug symbols, but `SOURCE_DISCOVERY_FAILED` means the whole source-mapping stage was skipped; `GB1002` is emitted per-run when the binary has no DWARF sections at all. One does not imply the other. |
| `SOURCE_DISCOVERY_FAILED` | `GB1001` (`SourceMapOffsetNotFound`) | `GB1001` is a narrower condition — a single WASM offset failed to resolve — and does not cause command failure. `SOURCE_DISCOVERY_FAILED` is fatal to the source-mapping pipeline. |
| `SOURCE_DISCOVERY_FAILED` | `GB1003` (`SourceMapNoGitRepository`) | `GB1003` is informational (`note` level) and does not fail the run; `SOURCE_DISCOVERY_FAILED` does. No direct causal link. |
| `SIMULATION_LOGIC_ERROR` | `GB2001` (`DeprecatedHostFunction`) | A deprecated host function call is a SARIF warning, not a simulation logic error. `SIMULATION_LOGIC_ERROR` means the contract itself failed; `GB2001` means a specific function is deprecated and may fail in a future protocol release. |
| `SIMULATION_LOGIC_ERROR` | `GB2002` (`AllowlistedUnsafeHostFunction`) | Similarly, `GB2002` is a security finding about an unsafe-but-permitted call; it does not cause `SIMULATION_LOGIC_ERROR` unless the unsafe call is also the cause of the simulation trap. |
| `VALIDATION_FAILED` | *(no direct mapping)* | Validation failures happen before analysis begins; no SARIF output is produced. |
| `RPC_CONNECTION_FAILED` | *(no direct mapping)* | Network errors prevent the run from producing any SARIF findings. |

### Reading correlations across output formats

When a `glassbox scan` or `glassbox debug` run produces both `--json` and
`--format sarif` output, use the following approach to correlate them:

1. Check `--json` for `error.code` to understand why the command failed.
2. Inspect the SARIF `results[]` array for rule IDs in the `GB1xxx`/`GB2xxx`
   range — these are diagnostic findings from the same run.
3. Do not assume a SARIF result means the corresponding error code was set, or
   vice versa.  A run can produce `GB1001` warnings and still exit 0; and a
   `SOURCE_DISCOVERY_FAILED` error can occur without any `GB1xxx` result if
   the discovery failure happened before SARIF export ran.

For the full SARIF rule catalogue see
[sarif-diagnostic-rules.md](sarif-diagnostic-rules.md) and
[sarif-export.md](sarif-export.md).

## Adding a new code

1. Add the constant to `internal/errors/glassbox_error_code.go`.
2. Add it to the `codeToSentinel` map with a matching sentinel error.
3. Add it to `errorCodeRegistry` in `errors.go` if a sentinel should map to it.
4. Update the table above.
5. Decide whether it maps to exit code 1, 2, or 3 and add it to
   `userErrorCodes` or `configErrorCodes` in `internal/cmd/exitcode.go`.

## Exit code taxonomy

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | User error (bad input, validation failure) |
| 2 | Config / environment error |
| 3 | Internal / runtime error |
| 130 | Interrupted (SIGINT / Ctrl-C) |

## Migrated commands

The following commands emit `ErstError` for all failure paths and have test
coverage of at least one validation error and one runtime error:

- `debug`
- `trace`
- `audit:sign`
- `audit:verify`
- `session` (save, resume, list, delete, recover, doctor)
- `protocol:register`, `protocol:diagnose`, `protocol:verify`
- `config show` (load and explain paths)
