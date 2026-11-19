# Observability Troubleshooting Guide

This guide is the single reference for understanding, enabling, and
troubleshooting every observability signal emitted by Glassbox: structured
logs, Prometheus metrics, OpenTelemetry traces, and telemetry events.  It
cross-links existing documentation where relevant and focuses on practical,
copyable examples with real-looking (but fake) values.

---

## Quick-reference diagnostic commands

```bash
# Print current telemetry state and how to change it
glassbox telemetry

# Show config including active RPC endpoints and feature flags
glassbox config show

# Dump a diagnostics bundle to a zip file (logs, env, config redacted)
glassbox diagnostics bundle --output ./diagnostics-$(date +%s).zip

# Verify RPC connectivity and simulator presence
glassbox debug --dry-run --network testnet <tx-hash>

# Check daemon metrics endpoint
curl -s http://localhost:9090/metrics | grep glassbox_
```

---

## 1. Logs

### Enabling structured logging

Glassbox writes human-readable logs to stderr by default.  Pass `--log-level`
to increase verbosity:

```bash
# Levels: error | warn | info | debug
glassbox --log-level debug debug abc123...
```

Redirect both stdout and stderr to capture everything:

```bash
glassbox debug abc123... > out.log 2> err.log
```

For machine-readable JSON logs (suitable for log aggregators):

```bash
glassbox --json debug abc123... 2>&1 | tee run.json
```

See [docs/json-output.md](json-output.md) for the full output schema.

### Privacy defaults

Logs are subject to the same redaction rules as telemetry.  The following
values are **never written to stderr** in their raw form:

| Value type | Treatment |
|---|---|
| Transaction hashes | Client-side SHA-256 fingerprint |
| File paths | Basename only |
| Config tokens / passwords / keys | Fully redacted (`[REDACTED]`) |
| Contract IDs | Fingerprinted |

If you need unredacted paths for debugging a local issue, use
`--log-level debug` in a trusted local environment only.

See also [docs/telemetry-metadata.md](telemetry-metadata.md) and
[docs/security-warnings.md](security-warnings.md).

### Correlation IDs

Every `glassbox debug` invocation creates a session.  The session ID printed at
the end of output (e.g., `sess_abc123`) acts as a correlation ID across log
lines emitted during that session.  You can search your log aggregator for the
session ID to reconstruct the full execution context:

```
Session created: sess_abc123
Run 'glassbox session save' to persist this session.
```

If a run crashes before printing the session ID, look for the partial session
directory in `~/.Glassbox/sessions/`.  Use `glassbox session recover` to
surface it:

```bash
glassbox session recover
```

### Common log failure patterns

| Message | Cause | Fix |
|---|---|---|
| `failed to open log file: permission denied` | Log directory not writable | Check permissions on `~/.Glassbox/` |
| `failed to rotate log: disk full` | Disk exhausted | Free space or redirect logs with `2>/dev/null` |
| `logger not initialized` | Internal startup error | File an issue; run with `--log-level debug` for the stack |

---

## 2. Prometheus metrics

The Glassbox daemon (started with `glassbox daemon start`) exposes a
`/metrics` endpoint compatible with Prometheus scraping.

### Enabling the daemon and metrics

```bash
glassbox daemon start
# Metrics are now available at http://localhost:9090/metrics
```

### Exposed metrics

| Metric | Type | Labels | Description |
|---|---|---|---|
| `remote_node_last_response_timestamp_seconds` | Gauge | `node_address`, `network` | Unix timestamp of last successful RPC response |
| `remote_node_response_total` | Counter | `node_address`, `network`, `status` | Total RPC responses by status (`success`/`error`) |
| `remote_node_response_duration_seconds` | Histogram | `node_address`, `network` | RPC response latency |
| `simulation_execution_total` | Counter | `status` | Total simulation executions by status |

See [internal/metrics/README.md](../internal/metrics/README.md) for PromQL
examples and Grafana dashboard reference.

### Checking metric values manually

```bash
# Scrape the local daemon
curl -s http://localhost:9090/metrics | grep remote_node_response_total

# Example output:
# remote_node_response_total{network="testnet",node_address="https://soroban-testnet.stellar.org",status="success"} 42
# remote_node_response_total{network="testnet",node_address="https://soroban-testnet.stellar.org",status="error"} 3
```

### Metric collection failures

**Symptom**: `curl: (7) Failed to connect to localhost port 9090`

The daemon is not running.  Start it:

```bash
glassbox daemon start
```

**Symptom**: `remote_node_response_total` counter is not incrementing

1. Confirm the RPC endpoint is reachable:
   ```bash
   glassbox debug --dry-run --network testnet <any-tx-hash>
   ```
2. Check that you are running commands through the daemon, not the standalone
   CLI (the standalone CLI does not update daemon metrics).

**Symptom**: Staleness alert fires (`remote_node_last_response_timestamp_seconds` old)

The last successful RPC call was longer ago than expected.  Check:
```bash
glassbox cache status --rpc
glassbox rpc status --network testnet
```

---

## 3. OpenTelemetry traces

### Enabling OTLP export

Glassbox emits OpenTelemetry spans when you supply an OTLP endpoint.  Configure
via environment variable or config file:

```bash
# Jaeger running locally
export GLASSBOX_TELEMETRY=true
export GLASSBOX_TELEMETRY_ENDPOINT=http://localhost:4318

glassbox debug abc123...
```

Config file (`~/.Glassbox/config.json`):
```json
{
  "telemetry_enabled": true,
  "telemetry_endpoint": "http://localhost:4318"
}
```

A Docker Compose file for a local Jaeger instance is provided:

```bash
docker compose -f docker-compose.jaeger.yml up -d
```

### Span attributes

| Attribute | Value | Privacy |
|---|---|---|
| `service.name` | `glassbox` | Public |
| `glassbox.command` | e.g., `debug` | Public |
| `glassbox.version` | e.g., `1.2.3` | Public |
| `glassbox.network` | e.g., `testnet` | Public |
| `glassbox.tx_hash` | SHA-256 fingerprint | Fingerprinted (never raw) |
| `glassbox.session_id` | e.g., `sess_abc123` | Session-scoped |
| `glassbox.platform` | e.g., `linux/amd64` | Public |

When `--telemetry-anonymized` is set (or `GLASSBOX_TELEMETRY_ANONYMIZED=true`),
only `glassbox.command` is emitted; all other attributes are dropped.

### Correlating CLI output with traces

The session ID from CLI output maps directly to the `glassbox.session_id` span
attribute.  In Jaeger:

1. Open the UI at `http://localhost:16686`.
2. Select service `glassbox`.
3. Search by tag `glassbox.session_id=sess_abc123`.

### OTLP exporter errors

**Symptom**: `failed to export spans: connection refused`

The OTLP collector is unreachable.  Verify the endpoint:
```bash
curl -s -o /dev/null -w "%{http_code}" http://localhost:4318/v1/traces
# Expected: 405 (method not allowed) or 200, not connection refused
```

**Symptom**: Spans exported but not visible in Jaeger

1. Confirm the service name filter is `glassbox`.
2. Check Jaeger ingester logs: `docker compose logs jaeger`.
3. Verify the OTLP endpoint matches what Jaeger listens on (default: `4318`
   for HTTP, `4317` for gRPC).

**Symptom**: `NaN or Infinity in span attribute`

This indicates a metric value that cannot be serialised.  File an issue with
the command and `--log-level debug` output.

---

## 4. Telemetry events

Glassbox telemetry is **opt-in** and **disabled by default**.

### Current state

```bash
glassbox telemetry
```

Example output:
```
Telemetry: disabled
To enable: set telemetry_enabled = true in ~/.Glassbox/config.json
           or run: glassbox --telemetry <command>
To disable for this shell: export GLASSBOX_TELEMETRY=false
```

### Opting in

```bash
# Persistent (config file)
# Add to ~/.Glassbox/config.json:
# { "telemetry_enabled": true }

# Per-invocation
glassbox --telemetry debug abc123...

# Anonymized (command name only, no env metadata)
glassbox --telemetry --telemetry-anonymized debug abc123...
```

### What is and is not sent

| Data | Sent? | Notes |
|---|---|---|
| Command name (`debug`, `trace`, etc.) | Yes | Always |
| CLI version | Yes | When not anonymized |
| OS and architecture | Yes | When not anonymized |
| Enabled feature flags | Yes | When not anonymized |
| Transaction hashes | No | Fingerprinted client-side |
| Contract IDs | No | Fingerprinted client-side |
| File paths | No | Basename only |
| Config values | No | Redacted |
| Secrets, keys, tokens | No | Redacted |

### Offline / air-gapped behavior

When the telemetry endpoint is unreachable, Glassbox **silently drops** the
event and continues normally.  No retry queue is persisted.  This means:

- Telemetry loss in offline environments is expected and by design.
- CLI behavior is identical whether telemetry succeeds or fails.

To suppress the outbound attempt entirely in air-gapped environments:

```bash
export GLASSBOX_TELEMETRY=false
```

### Telemetry sampling

Glassbox applies a sampling policy before exporting events to reduce noise.
See [docs/telemetry-sampling.md](telemetry-sampling.md) for the current policy.

---

## 5. Common end-to-end scenarios

### Scenario A: "My debug run produced no output"

1. Check exit code: `echo $?` — non-zero means an error occurred.
2. Re-run with stderr captured: `glassbox debug <tx> 2>err.log && cat err.log`.
3. Check the error code in the output:
   ```bash
   glassbox --json debug <tx> 2>&1 | python3 -m json.tool | grep '"code"'
   ```
   See [docs/stable-error-codes.md](stable-error-codes.md) for code meanings.
4. Run the dry-run to isolate RPC vs simulator issues:
   ```bash
   glassbox debug --dry-run --network testnet <tx>
   ```

### Scenario B: "Metrics are missing after upgrading Glassbox"

Metric names are stable across minor versions.  If a metric disappears:
1. Confirm the daemon was restarted after the upgrade.
2. Run `glassbox version` to verify the binary version.
3. Scrape `/metrics` directly: `curl http://localhost:9090/metrics`.
4. Check `internal/metrics/README.md` for any metric renames.

### Scenario C: "OTLP export is failing in CI"

1. Disable telemetry in CI unless you have a collector running:
   ```yaml
   env:
     GLASSBOX_TELEMETRY: "false"
   ```
2. If you want traces in CI, add a sidecar collector to your pipeline and set
   `GLASSBOX_TELEMETRY_ENDPOINT` to point to it.
3. Check that the collector's network policy allows inbound from the CI runner.

### Scenario D: "Log output contains sensitive path information"

All log redaction is applied at the logger layer.  If raw paths appear in logs:
1. Confirm you are on the latest release: `glassbox version`.
2. Run with `--no-color` and redirect stderr to compare:
   ```bash
   glassbox --no-color debug <tx> 2>&1 | grep "/"
   ```
3. File an issue with the sanitized log excerpt.

See [docs/no-color.md](no-color.md) for output formatting controls.

---

## 6. Redacting logs before sharing

The `scripts/redact-logs.sh` helper strips sensitive values from log files
before you share them in a bug report:

```bash
bash scripts/redact-logs.sh ./err.log > err-redacted.log
```

The script replaces:
- Transaction hashes (64-character hex strings) with `[TX_HASH]`
- Contract IDs (Stellar `C...` addresses) with `[CONTRACT_ID]`
- File paths outside the current directory with `[PATH]`
- Ed25519 public keys with `[PUBLIC_KEY]`

Always inspect the redacted file before sharing.

---

## 7. Further reading

| Document | Topic |
|---|---|
| [docs/telemetry-metadata.md](telemetry-metadata.md) | What env metadata is included in telemetry events |
| [docs/telemetry-sampling.md](telemetry-sampling.md) | How events are sampled |
| [docs/no-color.md](no-color.md) | Disabling ANSI output for log capture |
| [docs/security-warnings.md](security-warnings.md) | Deprecated host functions and security findings |
| [docs/stable-error-codes.md](stable-error-codes.md) | Stable error codes for automation |
| [docs/json-output.md](json-output.md) | Machine-readable `--json` output schema |
| [docs/diagnostics-bundle.md](diagnostics-bundle.md) | Collecting a full diagnostics bundle |
| [internal/metrics/README.md](../internal/metrics/README.md) | Prometheus metric reference and PromQL examples |
