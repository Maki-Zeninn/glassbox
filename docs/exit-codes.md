# Glassbox Exit-Code Registry

This document is the authoritative cross-language reference for all exit codes
produced by the Glassbox CLI (TypeScript wrapper) and Go binary.  Shell scripts
and CI pipelines **must** key on these codes, never on message text.

## Stable codes

| Code | Name               | When used |
|-----:|--------------------|-----------|
|    0 | SUCCESS            | Command completed successfully. |
|    1 | UNKNOWN_ERROR      | Unexpected or unclassified runtime error. |
|    2 | VALIDATION_ERROR   | Invalid user input: bad arguments, malformed payload, wrong format. |
|    3 | CONFIGURATION_ERROR| Configuration or provider error: missing key, unsupported platform, registration failure. |
|    4 | NETWORK_ERROR      | Network or RPC error: connection refused, timeout, endpoint unavailable. |
|    5 | SECURITY_ERROR     | Security or authorization error: invalid signature, unauthorized origin, rate limit exceeded. |
|    6 | RESOURCE_ERROR     | Resource or concurrency error: lock acquisition failed, resource busy. |
|  130 | SIGINT             | Process received SIGINT (Ctrl-C). POSIX: 128 + 2. |
|  143 | SIGTERM            | Process received SIGTERM. POSIX: 128 + 15. |

Codes 128–143 are reserved for fatal signal termination per POSIX (128 + signal
number).  No application-level code may be assigned in that range.

## TypeScript registry

All TypeScript command wrappers import exit codes from `src/exit-codes.ts`:

```typescript
import { ExitCode } from '../exit-codes';
process.exit(ExitCode.VALIDATION_ERROR); // 2
```

The registry is an `as const` object so callers get full type inference:

```typescript
export const ExitCode = {
    SUCCESS:              0,
    UNKNOWN_ERROR:        1,
    VALIDATION_ERROR:     2,
    CONFIGURATION_ERROR:  3,
    NETWORK_ERROR:        4,
    SECURITY_ERROR:       5,
    RESOURCE_ERROR:       6,
    SIGINT:             130,
    SIGTERM:            143,
} as const;
```

## Go binary equivalents

The Go binary surfaces structured errors via `internal/errors/errors.go`.
Each `ErstError` carries a stable `Code` string (e.g. `"RPC_CONNECTION_FAILED"`)
that maps to an exit code before the process terminates.  See
`docs/stable-error-codes.md` for the full Go code catalogue.

| Go ErstError code             | Exit code | Name               |
|-------------------------------|----------:|--------------------|
| *(no error)*                  |         0 | SUCCESS            |
| `UNKNOWN`                     |         1 | UNKNOWN_ERROR      |
| `INVALID_ARGUMENT`, `PARSE_*` |         2 | VALIDATION_ERROR   |
| `CONFIG_*`, `PROVIDER_*`      |         3 | CONFIGURATION_ERROR|
| `RPC_*`, `NETWORK_*`          |         4 | NETWORK_ERROR      |
| `AUTH_*`, `SIGNATURE_*`       |         5 | SECURITY_ERROR     |
| `LOCK_*`, `RESOURCE_*`        |         6 | RESOURCE_ERROR     |

## Shell script example

```sh
glassbox audit:verify --file report.json
case $? in
  0) echo "Verified OK" ;;
  2) echo "Bad input — check your arguments" ;;
  5) echo "Signature invalid or expired" ;;
  *) echo "Unexpected error (code $?)" ;;
esac
```

## Rules for contributors

1. **Always use the registry.**  Do not hard-code numeric literals; import
   `ExitCode` (TypeScript) or map to the table above (Go).
2. **Prefer specific codes over UNKNOWN_ERROR.**  If a failure falls into a
   documented category, use that category's code.
3. **SUCCESS must be zero.**  Any non-zero code is an error condition.
4. **Do not add codes without updating this file.**  New codes require a PR
   that adds the row here, updates `src/exit-codes.ts`, and adds a test.
