# JSON Output for Automation

This guide is the authoritative reference for every machine-readable JSON
surface that Glassbox exposes. It is written for developers who consume
Glassbox output from scripts, CI pipelines, or other programs that cannot
parse human-readable terminal output reliably.

All examples derive from the fixtures in `test/regression/fixtures/` and the
conformance tests in `internal/clioutput/` and `internal/trace/`. They match
the current schema versions and are kept in sync by the API snapshot checks in
CI.

---

## Quick start

```bash
# Full JSON output for a debug run
glassbox debug --json <txhash>

# Pipe to jq to extract a single field
glassbox debug --json <txhash> | jq '.data.status'

# Capture progress events separately (stderr) while keeping JSON payload on stdout
glassbox debug --progress-json --json <txhash> \
  2>progress.ndjson \
  1>payload.json
```

---

## The envelope

Every command that supports `--json` or `--format json` wraps its payload in a
stable top-level envelope defined in `internal/clioutput/output.go`.

```json
{
  "schema_version": "1.0",
  "glassbox_version": "1.2.3",
  "generated_at": "2026-07-24T12:00:00.123456789Z",
  "command": "debug",
  "data": { }
}
```

### Envelope fields

| Field | Type | Stability | Description |
|---|---|---|---|
| `schema_version` | string | **stable** | Envelope format version. Currently `"1.0"`. Only increments on breaking changes. |
| `glassbox_version` | string | stable | Semver string of the binary that produced this output (e.g. `"1.2.3"`, `"0.0.0-dev"`). |
| `generated_at` | string (RFC 3339) | stable | UTC timestamp when the output was produced. Nanosecond precision where available. |
| `command` | string | stable | The CLI command that produced the output (e.g. `"debug"`, `"audit:sign"`, `"protocol:diagnose"`). May be empty for legacy output. |
| `data` | object | stable | Command-specific payload. Always an object; never null. Structure depends on `command`. See per-command sections below. |

### Forward-compatibility rule

**Always ignore unknown fields at every level of the envelope and its
payloads.** Glassbox adds new optional fields in minor releases without
incrementing `schema_version`. Parsers that reject unknown fields will break.

---

## Commands with JSON output

| Command | Flag(s) | Notes |
|---|---|---|
| `debug` | `--json`, `--format json` | Full diagnostic envelope |
| `trace` | `--output-json <file>`, `--format json` | Writes to file, not stdout |
| `audit:sign` | `--json` | Signed audit log envelope |
| `audit:verify` | `--json` | Verification result |
| `protocol:diagnose` | `--json`, `--format json` | Registration diagnostic |
| `generate-bindings` | `--json`, `--format json` | Binding generation result |
| `check-bindings` | `--json` | Binding check result |
| `config show` | `--json` | Active configuration |
| `bench` | `--json` | Benchmark results |
| `version` | `--json` | Version info (not wrapped in envelope — see below) |

### `glassbox version --json`

This is the one command whose JSON is **not** wrapped in the envelope. It
emits a flat object directly:

```json
{
  "version": "1.2.3",
  "commit_sha": "a3f8c1d2e4b7091f3d6a5c8e2b4f0d1e7c9a2b5f",
  "build_date": "2026-07-24T00:00:00Z",
  "go_version": "go1.22.2",
  "is_dev": false,
  "user_agent": "glassbox/1.2.3"
}
```

All other commands use the envelope.

---

## Trace JSON schema

The execution trace written by `glassbox trace --output-json` or
`glassbox debug --json` carries a versioned schema.

**Current trace schema version: `2.0.0`** (defined in
`internal/trace/export_versioned.go`).

### Top-level fields

```json
{
  "schema_version": "2.0.0",
  "generated_at": "2026-07-24T12:00:00Z",
  "transaction_hash": "5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab",
  "start_time": "2026-07-24T12:00:00Z",
  "end_time": "2026-07-24T12:00:01.5Z",
  "states": [ ],
  "diagnostic_events": [ ],
  "decoded_events": [ ],
  "annotations": { },
  "current_step": 0,
  "snapshot_interval": 10,
  "host_calls": [ ],
  "resource_limits": { },
  "trap_cause": null
}
```

| Field | Type | Since | Required | Description |
|---|---|---|---|---|
| `schema_version` | string (semver) | 1.0.0 | **yes** | Schema version this trace conforms to. |
| `generated_at` | string (RFC 3339) | 1.0.0 | **yes** | When the file was written. |
| `transaction_hash` | string (64 hex) | 1.0.0 | **yes** | The transaction this trace was captured from. |
| `start_time` | string (RFC 3339) | 1.0.0 | **yes** | Trace start time. |
| `end_time` | string (RFC 3339) | 1.0.0 | **yes** | Trace end time. |
| `states` | array\<ExecutionState\> | 1.0.0 | **yes** | Ordered execution steps. May be empty. |
| `diagnostic_events` | array\<DiagnosticEvent\> | 1.0.0 | no | Raw simulator diagnostic events. |
| `decoded_events` | array\<ContractEvent\> | 1.0.0 | no | Decoded contract events (requires `--event-schema`). |
| `annotations` | object | 1.0.0 | no | User-defined annotation map. |
| `current_step` | integer | 1.0.0 | no | Last navigation step (viewer state). |
| `snapshot_interval` | integer | 1.0.0 | no | Steps between state snapshots. |
| `host_calls` | array\<HostCallRecord\> | 2.0.0 | no | Host function call records. |
| `resource_limits` | object | 2.0.0 | no | CPU/memory limit summary. |
| `trap_cause` | object\|null | 2.0.0 | no | Structured WASM trap cause. |

### `ExecutionState` fields

```json
{
  "step": 3,
  "timestamp": "2026-07-24T12:00:00.456Z",
  "operation": "contract_call",
  "event_type": "contract_call",
  "contract_id": "CTEST00000000000000000000000000000000000000000000000000000001",
  "function": "transfer",
  "arguments": [ ],
  "return_value": null,
  "error": "",
  "cpu_delta": 15000,
  "memory_delta": 2048,
  "host_state": null,
  "source_ref": {
    "file": "src/lib.rs",
    "line": 42,
    "column": 8,
    "origin_class": "user"
  }
}
```

| Field | Type | Description |
|---|---|---|
| `step` | integer | Zero-based step index within this trace. |
| `timestamp` | string (RFC 3339) | When this step was captured. |
| `operation` | string | Operation type: `contract_call`, `host_fn`, `error`, `event`, `auth`, `log`. |
| `event_type` | string | Classifier for trace filtering. |
| `contract_id` | string | Stellar contract ID (C-prefixed, 56 chars), or empty. |
| `function` | string | Function name. |
| `arguments` | array | Call arguments (present in verbose mode). |
| `return_value` | any\|null | Return value when available. |
| `error` | string | Error message when this step failed. Empty on success. |
| `cpu_delta` | integer | CPU instructions consumed by this step (0 when unavailable). |
| `memory_delta` | integer | Memory bytes consumed by this step (0 when unavailable). |
| `host_state` | object\|null | Ledger state before/after (present for `StateUpdate` steps). |
| `source_ref` | object\|null | DWARF-resolved source location. See below. |

### `source_ref` fields

```json
{
  "file": "src/lib.rs",
  "line": 42,
  "column": 8,
  "origin_class": "user"
}
```

| Field | Type | Values | Description |
|---|---|---|---|
| `file` | string | — | Source file path (may be absolute, relative, or build-relative). |
| `line` | integer | ≥ 1 | 1-based source line number. |
| `column` | integer | ≥ 1 | 1-based column number. 0 when unavailable. |
| `origin_class` | string | `user`, `generated`, `external`, `unknown` | Origin of the source path. See `docs/source-origin-classification.md`. |

---

## Error envelope

When a command fails in JSON mode, the `data` field contains a structured
error object instead of the command-specific payload. The outer envelope fields
(`schema_version`, `glassbox_version`, etc.) are always present.

```json
{
  "schema_version": "1.0",
  "glassbox_version": "1.2.3",
  "generated_at": "2026-07-24T12:00:00Z",
  "command": "debug",
  "data": {
    "error": {
      "code": "RPC_CONNECTION_FAILED",
      "severity": "error",
      "message": "RPC connection failed: dial tcp 127.0.0.1:8000: connect: connection refused",
      "remediation": "Check your --rpc-url and verify the endpoint is reachable. Run 'glassbox doctor' to diagnose.",
      "context": {
        "network": "testnet",
        "rpc_url": "http://127.0.0.1:8000"
      }
    }
  }
}
```

### Error object fields

| Field | Type | Stability | Description |
|---|---|---|---|
| `error.code` | string | **stable** | Stable snake_UPPER_CASE error code. Key on this in automation — never on `message`. See `docs/stable-error-codes.md`. |
| `error.severity` | string | stable | Always `"error"` for current commands. |
| `error.message` | string | informational | Human-readable description. May change between releases. |
| `error.remediation` | string | informational | Actionable fix guidance. May change. |
| `error.context` | object | informational | Optional key-value pairs that give additional debugging context. Fields vary by error. |

### Stable error codes (automation-relevant subset)

| Code | Exit | Trigger |
|---|---|---|
| `TRANSACTION_NOT_FOUND` | 2 | Hash does not exist on the selected network |
| `INVALID_ARGUMENT` | 2 | Bad flag or argument value |
| `RPC_CONNECTION_FAILED` | 4 | Network endpoint unreachable |
| `RPC_TIMEOUT` | 4 | Request timed out |
| `ALL_RPC_FAILED` | 4 | All failover endpoints failed |
| `SIMULATION_FAILED` | 3 | Simulator process returned non-zero |
| `SIMULATOR_NOT_FOUND` | 3 | `glassbox-sim` binary missing or not executable |
| `SOURCE_DISCOVERY_FAILED` | 3 | Source mapping unavailable (use `--skip-source-mapping`) |
| `SESSION_WRITE_CONFLICT` | 2 | Concurrent writer conflict — reload and retry |
| `SESSION_LOCK_HELD` | 2 | Advisory lock held by a live process |

Full catalogue: `docs/stable-error-codes.md`.

---

## Exit codes

Exit codes are stable across releases. Do not key on error message text.

| Code | Name | When |
|---|---|---|
| 0 | SUCCESS | Command completed without error |
| 1 | USER_ERROR | Bad input, validation failure, not-found |
| 2 | CONFIG_ERROR | Configuration or environment error |
| 3 | RUNTIME_ERROR | Internal or network runtime error |
| 130 | SIGINT | Process received Ctrl-C |
| 143 | SIGTERM | Process received SIGTERM |

```bash
glassbox debug --json <txhash>
case $? in
  0) jq '.data' payload.json ;;
  1) echo "Bad input or transaction not found" ;;
  3) echo "Runtime error — check .data.error.code" ;;
  *) echo "Unexpected exit $?" ;;
esac
```

Full reference: `docs/exit-codes.md`.

---

## Progress events (`--progress-json`)

Progress events are emitted as **newline-delimited JSON (NDJSON) to stderr**
when `--progress-json` is set. They do not affect stdout.

```ndjson
{"operation_id":"a1b2c3d4e5f6a1b2","phase":"init","status":"start","timestamp":"2026-07-24T12:00:00Z","message":"debug command starting"}
{"operation_id":"a1b2c3d4e5f6a1b2","phase":"fetch","status":"start","timestamp":"2026-07-24T12:00:00.02Z","message":"fetching transaction from testnet","meta":{"network":"testnet","tx_hash":"abc123"}}
{"operation_id":"a1b2c3d4e5f6a1b2","phase":"fetch","status":"complete","timestamp":"2026-07-24T12:00:00.45Z","message":"transaction fetched (1024 bytes)","meta":{"envelope_bytes":1024}}
{"operation_id":"a1b2c3d4e5f6a1b2","phase":"simulate","status":"start","timestamp":"2026-07-24T12:00:00.46Z","message":"running simulation"}
{"operation_id":"a1b2c3d4e5f6a1b2","phase":"simulate","status":"complete","timestamp":"2026-07-24T12:00:01.1Z","message":"simulation complete"}
{"operation_id":"a1b2c3d4e5f6a1b2","phase":"done","status":"complete","timestamp":"2026-07-24T12:00:01.21Z","message":"debug session complete"}
```

### Progress event fields

| Field | Type | Stability | Description |
|---|---|---|---|
| `operation_id` | string (hex) | stable | Shared by all events in one invocation. Use for log correlation. |
| `phase` | string | stable | `init`, `fetch`, `simulate`, `analyze`, `export`, `done` |
| `status` | string | stable | `start`, `complete`, `error`, `skipped` |
| `timestamp` | string (RFC 3339) | stable | UTC, non-decreasing within an invocation. |
| `message` | string | informational | Human-readable. Never contains secrets. |
| `error_code` | string | stable | Non-empty only on `status=error`. Stable snake_case identifier. |
| `meta` | object | informational | Optional phase-specific key-value metadata. Safe: no tokens or keys. |

### Phase/status matrix

| Phase | Terminal statuses |
|---|---|
| `init` | `complete`, `error` |
| `fetch` | `complete`, `error`, `skipped` |
| `simulate` | `complete`, `error` |
| `analyze` | `complete`, `error`, `skipped` |
| `export` | `complete`, `error`, `skipped` |
| `done` | `complete`, `error` |

Every phase emits exactly one `start` event followed by exactly one terminal
event. There are no orphaned starts.

Full reference: `docs/progress-events.md`.

---

## Versioning and deprecation policy

### `schema_version` in the envelope

The envelope `schema_version` is `"1.0"` and will only increment on a
**breaking change** — a field removal or a type change. Additions of new
optional fields to `data` are non-breaking and do not increment this version.

### Trace `schema_version`

The trace schema uses semver (`MAJOR.MINOR.PATCH`):

- **MAJOR bump**: breaking change — a required field was removed or its type
  changed. Parsers must migrate.
- **MINOR bump**: new optional fields added. Existing parsers remain valid.
- **PATCH bump**: documentation or clarification only. No structural change.

### Deprecation lifecycle

1. A field is marked deprecated in the changelog fragment (`affects = ["json-output"]`).
2. The field remains present and populated for at least one major release.
3. The field is removed in the next major release, accompanied by a
   `migration_note` in `changelog/fragments/`.

Parsers must be prepared to receive both the deprecated and the replacement
field simultaneously during the transition period.

### Unknown field handling

Parsers **must** silently ignore fields they do not recognise. This is the
primary forward-compatibility mechanism. Do not reject or error on unknown
fields at any level of the JSON hierarchy.

---

## Parser example (language-agnostic)

The following pseudocode demonstrates a safe consumption pattern that handles
all envelope shapes, errors, and unknown fields without special-casing. It can
be adapted to any language that has a JSON library.

```
function parseGlassboxOutput(rawJson):
    obj = json.decode(rawJson)           # decode top-level object

    schemaVersion = obj["schema_version"] ?? "unknown"
    command       = obj["command"]        ?? ""
    data          = obj["data"]           ?? {}

    # Check for an error in the payload before processing success fields
    if data contains key "error":
        err = data["error"]
        errorCode    = err["code"]        ?? "UNKNOWN"
        errorMessage = err["message"]     ?? ""
        raise GlassboxError(code=errorCode, message=errorMessage)

    # Route to per-command parser
    match command:
        "debug":
            return parseDebugPayload(data)
        "audit:sign":
            return parseAuditPayload(data)
        _:
            # Unknown command — return raw data and let the caller decide
            return RawPayload(data=data, schemaVersion=schemaVersion)


function parseDebugPayload(data):
    # Always ignore unknown fields — forward-compat rule
    status  = data["status"]   ?? "unknown"
    txHash  = data["tx_hash"]  ?? ""
    trace   = data["trace"]    ?? null

    if trace is not null:
        states = trace["states"] ?? []
        for step in states:
            parseStep(step)         # ignore any unrecognised keys in step

    return DebugResult(status=status, txHash=txHash, trace=trace)


function parseStep(step):
    stepIndex   = step["step"]        ?? 0
    operation   = step["operation"]   ?? ""
    error       = step["error"]       ?? ""
    sourceRef   = step["source_ref"]  ?? null    # may be null or absent

    if sourceRef is not null:
        file        = sourceRef["file"]         ?? ""
        line        = sourceRef["line"]         ?? 0
        originClass = sourceRef["origin_class"] ?? "unknown"
        # do not reject unknown originClass values — new classes may be added

    # ignore all other fields in step (forward-compat)
    return Step(index=stepIndex, operation=operation, error=error)
```

### Go example

```go
import (
    "encoding/json"
    "fmt"
    "github.com/dotandev/glassbox/internal/clioutput"
)

// Decode the envelope — works for any Glassbox --json output.
var env clioutput.Envelope
if err := json.Unmarshal(raw, &env); err != nil {
    return fmt.Errorf("decode envelope: %w", err)
}

// Route on command name; use json.RawMessage for forward-compat.
switch env.Command {
case "debug":
    var payload struct {
        Status string `json:"status"`
        // Add only the fields you need. Unknown fields are silently dropped.
    }
    if err := json.Unmarshal(env.Data, &payload); err != nil {
        return fmt.Errorf("decode debug payload: %w", err)
    }
    fmt.Println("status:", payload.Status)
default:
    fmt.Printf("unknown command %q, raw data: %s\n", env.Command, env.Data)
}
```

### Shell + jq example

```bash
#!/usr/bin/env bash
set -euo pipefail

OUT=$(glassbox debug --json "$TX_HASH" 2>/dev/null)

# Check for an error payload before reading success fields
if echo "$OUT" | jq -e '.data.error' >/dev/null 2>&1; then
  CODE=$(echo "$OUT" | jq -r '.data.error.code')
  MSG=$(echo  "$OUT" | jq -r '.data.error.message')
  echo "Glassbox error [$CODE]: $MSG" >&2
  exit 1
fi

# Safely extract fields that may not exist (// null guard)
STATUS=$(echo "$OUT" | jq -r '.data.status // "unknown"')
STEPS=$(echo  "$OUT" | jq    '.data.trace.states | length // 0')

echo "Status: $STATUS  Steps: $STEPS"
```

---

## Validating examples against fixtures

The examples in this document derive from the conformance test fixtures in
`test/regression/fixtures/`. To verify them locally:

```bash
# Validate the trace fixture against the trace schema
go test -run TestTraceSchemaValidation ./internal/trace/...

# Check the CLI output envelope is byte-identical to the snapshot
scripts/api-snapshot.sh check

# Regenerate after an intentional change
scripts/api-snapshot.sh generate
go test ./internal/apicompat/... -update
```

CI runs both checks on every PR via the `ci-gate` job.

---

## Related documents

| Document | Content |
|---|---|
| `docs/stable-error-codes.md` | Full error code catalogue with exit code mappings |
| `docs/exit-codes.md` | Exit code registry (Go and TypeScript) |
| `docs/event-schemas.md` | Trace and contract event schema versioning |
| `docs/progress-events.md` | `--progress-json` NDJSON event schema |
| `docs/api-compatibility.md` | API snapshot methodology and snapshot update procedure |
| `docs/source-origin-classification.md` | `origin_class` values and classification rules |
| `internal/clioutput/output.go` | Envelope struct definition (`SchemaVersion = "1.0"`) |
| `internal/trace/export_versioned.go` | Trace JSON export schema |
