# Structured Progress Events (`debug --progress-json`)

## Overview

`glassbox debug --progress-json` opts in to **structured progress events** emitted as newline-delimited JSON (NDJSON) to **stderr**.

Scripts and CI pipelines can parse these events to know exactly when each phase of a debug operation has started, completed, been skipped, or failed — without parsing human-readable progress messages.

`stdout` remains byte-for-byte compatible with its normal content when `--progress-json` is active, so the flag can be combined safely with `--format json` or pipe redirection.

## Usage

```sh
# Emit progress events to stderr while debug output goes to stdout
glassbox debug --progress-json abc123...def789

# Capture progress separately from payload
glassbox debug --progress-json abc123...def789 \
  2>progress.ndjson \
  1>payload.json

# Use with --format json for fully machine-readable output
glassbox debug --progress-json --format json abc123...def789
```

## Event Schema

Every event is a single JSON object on its own line:

```jsonc
{
  "operation_id": "a1b2c3d4e5f6...",  // hex string, shared by all events in one run
  "phase": "fetch",                    // see Phase Values below
  "status": "start",                  // see Status Values below
  "timestamp": "2026-07-24T12:00:00.123Z",
  "message": "fetching transaction abc from testnet",  // human-readable, never secret
  "error_code": "",        // non-empty only on status=error; stable snake_case identifier
  "meta": {                // optional safe metadata for the phase
    "tx_hash": "abc123...",
    "network": "testnet",
    "envelope_bytes": 512
  }
}
```

### Phase Values

| Phase | Description |
|-------|-------------|
| `init` | Command initialisation (flag validation, telemetry setup) |
| `fetch` | Fetching the transaction envelope from the network |
| `simulate` | Running the local simulator |
| `analyze` | Post-simulation analysis (security, token flows, suggestions) |
| `export` | Writing trace or snapshot output files |
| `done` | Terminal event — command completed successfully |

### Status Values

| Status | Terminal? | Description |
|--------|-----------|-------------|
| `start` | No | Phase has begun |
| `complete` | Yes | Phase completed without error |
| `error` | Yes | Phase failed; `error_code` is always populated |
| `skipped` | Yes | Phase was intentionally bypassed (e.g. local-envelope fetch) |

### Stable Error Codes

| `error_code` | Phase | Trigger |
|--------------|-------|---------|
| `invalid_dry_run_flags` | `init` | `--dry-run` combined with incompatible flags |
| `rpc_fetch_failed` | `fetch` | Network fetch of transaction failed |
| `simulation_failed` | `simulate` | Simulator returned an error |
| `export_failed` | `export` | Writing trace output file failed |

## Guarantees

- **Every phase emits a `start` event followed by exactly one terminal event** (`complete`, `error`, or `skipped`). There are no orphaned starts.
- **All events from one invocation share a single `operation_id`**, enabling reliable log correlation.
- **`error_code` is non-empty on every `status=error` event** and uses a stable, snake_case identifier.
- **`stdout` is not written to** by the progress machinery. All progress output goes exclusively to `stderr`.
- **`meta` values never contain secret material** (tokens, keys, DSNs). Only safe identifiers such as tx hashes, networks, and byte counts appear.
- **Timestamps are UTC and non-decreasing** within a single invocation.

## Example Output

```ndjson
{"operation_id":"a1b2c3d4e5f6a1b2","phase":"init","status":"start","timestamp":"2026-07-24T12:00:00Z","message":"debug command starting"}
{"operation_id":"a1b2c3d4e5f6a1b2","phase":"init","status":"complete","timestamp":"2026-07-24T12:00:00.01Z","message":"initialization complete"}
{"operation_id":"a1b2c3d4e5f6a1b2","phase":"fetch","status":"start","timestamp":"2026-07-24T12:00:00.02Z","message":"fetching transaction abc123 from testnet","meta":{"network":"testnet","tx_hash":"abc123"}}
{"operation_id":"a1b2c3d4e5f6a1b2","phase":"fetch","status":"complete","timestamp":"2026-07-24T12:00:00.45Z","message":"transaction fetched (1024 bytes)","meta":{"envelope_bytes":1024,"network":"testnet","tx_hash":"abc123"}}
{"operation_id":"a1b2c3d4e5f6a1b2","phase":"simulate","status":"start","timestamp":"2026-07-24T12:00:00.46Z","message":"running simulation on testnet","meta":{"ledger_entries":12,"network":"testnet"}}
{"operation_id":"a1b2c3d4e5f6a1b2","phase":"simulate","status":"complete","timestamp":"2026-07-24T12:00:01.1Z","message":"simulation complete: success","meta":{"network":"testnet","status":"success"}}
{"operation_id":"a1b2c3d4e5f6a1b2","phase":"analyze","status":"start","timestamp":"2026-07-24T12:00:01.11Z","message":"running post-simulation analysis"}
{"operation_id":"a1b2c3d4e5f6a1b2","phase":"analyze","status":"complete","timestamp":"2026-07-24T12:00:01.2Z","message":"analysis complete"}
{"operation_id":"a1b2c3d4e5f6a1b2","phase":"done","status":"complete","timestamp":"2026-07-24T12:00:01.21Z","message":"debug session complete"}
```

### Fetch-skip example (local envelope mode)

```ndjson
{"operation_id":"b2c3d4e5f6a1b2c3","phase":"init","status":"start",...}
{"operation_id":"b2c3d4e5f6a1b2c3","phase":"init","status":"complete",...}
{"operation_id":"b2c3d4e5f6a1b2c3","phase":"fetch","status":"skipped","message":"local envelope input — no network fetch required"}
{"operation_id":"b2c3d4e5f6a1b2c3","phase":"simulate","status":"start",...}
...
```

### Error example

```ndjson
{"operation_id":"c3d4e5f6a1b2c3d4","phase":"fetch","status":"error","message":"transaction fetch failed: dial tcp: connection refused","error_code":"rpc_fetch_failed"}
```
