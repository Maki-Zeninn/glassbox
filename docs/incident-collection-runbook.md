# Operator Runbook: Incident Collection

This runbook covers the repeatable sequence for collecting traces, health data,
logs, crash reports, and telemetry artifacts during an active incident or
post-mortem analysis.  Every command listed here is safe — it does not modify
contract state, ledger data, or deployments.  Steps that require human judgment
are marked as decision points.

**Audience:** Support engineers, incident responders, and operators who need to
build a sanitised evidence bundle without exposing secrets or altering evidence.

---

## Table of Contents

1. [Before you start](#before-you-start)
2. [Step 1 — Classify the incident](#step-1--classify-the-incident)
3. [Step 2 — Collect environment snapshot](#step-2--collect-environment-snapshot)
4. [Step 3 — Collect traces](#step-3--collect-traces)
5. [Step 4 — Collect health data](#step-4--collect-health-data)
6. [Step 5 — Collect structured logs](#step-5--collect-structured-logs)
7. [Step 6 — Collect telemetry queue state](#step-6--collect-telemetry-queue-state)
8. [Step 7 — Collect crash reports](#step-7--collect-crash-reports)
9. [Decision point — network vs offline incident](#decision-point--network-vs-offline-incident)
10. [Step 8 — Build the sanitised bundle](#step-8--build-the-sanitised-bundle)
11. [Step 9 — Verify artifact integrity](#step-9--verify-artifact-integrity)
12. [Step 10 — Redact before sharing](#step-10--redact-before-sharing)
13. [Retention guidance](#retention-guidance)
14. [Escalation data](#escalation-data)
15. [Validation checklist](#validation-checklist)
16. [Expected stable errors](#expected-stable-errors)

---

## Before you start

Set these environment variables for the session before running any collection
command.  They ensure consistent, redacted output and prevent accidental
telemetry emission during the collection run.

```sh
# Disable telemetry during collection so no evidence is silently sent.
export GLASSBOX_TELEMETRY=false

# Enable structured JSON logs so output is machine-parseable.
export GLASSBOX_LOG_FORMAT=json
export GLASSBOX_LOG_LEVEL=debug

# Point to the incident transaction hash (replace with the real value).
export INCIDENT_TX=<64-char-hex-transaction-hash>

# Choose the network (testnet | mainnet | futurenet).
export INCIDENT_NETWORK=testnet

# Working directory for all collected artifacts.
export INCIDENT_DIR="./incident-$(date +%Y%m%d-%H%M%S)"
mkdir -p "$INCIDENT_DIR"
```

Verify the environment is correct before proceeding:

```sh
glassbox config show --json | jq '{rpc_url, network, telemetry_enabled}'
```

Expected output example (telemetry must be false):

```json
{
  "rpc_url": "https://soroban-testnet.stellar.org",
  "network": "testnet",
  "telemetry_enabled": false
}
```

---

## Step 1 — Classify the incident

Determine whether the incident is a network incident (requires live RPC calls)
or an offline incident (reproducible from saved snapshots).  The collection
sequence differs — see the [decision point](#decision-point--network-vs-offline-incident)
below.

Collect basic version and configuration facts first:

```sh
# Record version info — required for all escalations.
glassbox version --json > "$INCIDENT_DIR/version.json"

# Record active configuration (secrets are automatically excluded).
glassbox config show --json > "$INCIDENT_DIR/config.json"
```

---

## Step 2 — Collect environment snapshot

```sh
# Dry-run validation — checks hash format, network reachability, and simulator
# presence without executing a simulation.
glassbox debug --dry-run \
  --network "$INCIDENT_NETWORK" \
  "$INCIDENT_TX" \
  > "$INCIDENT_DIR/dry-run.json" 2>&1

# Check the exit code.
echo "dry-run exit code: $?"
```

Stable errors from dry-run that can be ignored at this stage:
- `RPC_CONNECTION_FAILED` — expected when investigating an offline incident.
- `TRANSACTION_NOT_FOUND` — expected when the TX is only on another network.

---

## Step 3 — Collect traces

Run the debug command with verbose trace output and save to a file.  The
`--json` flag ensures machine-readable output regardless of terminal colour
settings.

```sh
# Live network trace (skip if offline incident).
glassbox debug \
  --network "$INCIDENT_NETWORK" \
  --json \
  --trace-verbosity verbose \
  --trace-output "$INCIDENT_DIR/trace.json" \
  "$INCIDENT_TX" \
  > "$INCIDENT_DIR/debug-output.json" 2>&1

echo "trace collection exit code: $?"
```

For an **offline incident** using a pre-saved snapshot:

```sh
glassbox debug \
  --load-snapshots "$INCIDENT_DIR/snapshots.json" \
  --json \
  --trace-verbosity verbose \
  --trace-output "$INCIDENT_DIR/trace-offline.json" \
  > "$INCIDENT_DIR/debug-offline.json" 2>&1
```

Validate the trace file is non-empty and well-formed:

```sh
jq 'has("events")' "$INCIDENT_DIR/trace.json" \
  && echo "trace: OK" \
  || echo "trace: missing or malformed"
```

---

## Step 4 — Collect health data

```sh
# Full health report including RPC, simulator, session store, and telemetry.
glassbox doctor \
  --verbose \
  --json \
  > "$INCIDENT_DIR/health.json" 2>&1

echo "health collection exit code: $?"
```

Extract the key health fields for quick review:

```sh
jq '{status, checks: [.checks[] | {name, status, message}]}' \
  "$INCIDENT_DIR/health.json"
```

Expected stable output even during an incident:
- `cache: ok` — the local LRU cache is not network-dependent.
- `session_store: ok` — local session storage is independent of RPC.

---

## Step 5 — Collect structured logs

If `GLASSBOX_LOG_FORMAT=json` is set (recommended in the setup above), logs are
already in NDJSON format.  Redirect them to a file during the collection run or
capture from the debug command's stderr:

```sh
# Re-run with log capture.
glassbox debug \
  --network "$INCIDENT_NETWORK" \
  --json \
  "$INCIDENT_TX" \
  > "$INCIDENT_DIR/debug-stdout.json" \
  2> "$INCIDENT_DIR/debug-logs.ndjson"

echo "log lines captured: $(wc -l < "$INCIDENT_DIR/debug-logs.ndjson")"
```

Filter for correlation IDs to group log lines by operation:

```sh
# Extract all distinct operation IDs from the log.
jq -r 'select(.operation_id != null) | .operation_id' \
  "$INCIDENT_DIR/debug-logs.ndjson" | sort -u
```

Filter by a specific operation ID to reconstruct the full request lifecycle:

```sh
OPERATION_ID=<paste-from-above>
jq "select(.operation_id == \"$OPERATION_ID\")" \
  "$INCIDENT_DIR/debug-logs.ndjson" \
  > "$INCIDENT_DIR/logs-op-${OPERATION_ID}.ndjson"
```

Verify that no secrets appear in the captured logs:

```sh
# These patterns must not appear in logs — fail if any match.
for pattern in "PKCS11_PIN" "private_key" "api_key" "password" "token"; do
  if grep -qi "$pattern" "$INCIDENT_DIR/debug-logs.ndjson" 2>/dev/null; then
    echo "WARNING: potential secret pattern '$pattern' found in logs — review before sharing"
  fi
done
```

---

## Step 6 — Collect telemetry queue state

The offline telemetry queue accumulates events when the collector is
unreachable.  Its state is useful for diagnosing connectivity problems and
quantifying dropped events.

```sh
# Show queue statistics.
glassbox telemetry \
  > "$INCIDENT_DIR/telemetry-status.txt" 2>&1

echo "telemetry collection exit code: $?"
```

The telemetry status output includes:
- Current consent level (`disabled` / `anonymous` / `full`)
- Number of queued events
- Oldest event age
- Dropped-by-size and dropped-by-age counters
- Queue file path

If you need to clear the queue as part of incident cleanup:

```sh
# Remove the offline queue file (does not affect consent settings).
# PLANNED: glassbox telemetry queue --delete
# Until the dedicated command ships, use:
rm -f ~/.Glassbox/telemetry_queue.ndjson && echo "queue cleared"
```

Note: this command is marked **planned** in the CLI surface.  The manual `rm`
path is the current supported approach.

---

## Step 7 — Collect crash reports

If the process crashed during the incident, the crash suppression state records
what has already been reported.  This is useful for distinguishing new failures
from known repeated ones.

```sh
# Show crash report deduplication stats.
glassbox doctor --verbose \
  | grep -A 20 "crash\|dedup\|suppress" \
  > "$INCIDENT_DIR/crash-stats.txt" 2>&1

# Show the suppress file path (no secrets — only opaque fingerprints).
cat ~/.Glassbox/crash_suppress.json 2>/dev/null \
  > "$INCIDENT_DIR/crash-suppress-state.json" \
  || echo "{}" > "$INCIDENT_DIR/crash-suppress-state.json"

echo "crash suppress entries: $(jq '.entries | length' "$INCIDENT_DIR/crash-suppress-state.json")"
```

The suppress file contains only opaque hex fingerprints and counts — no error
messages, stack traces, or user data.

---

## Decision point — network vs offline incident

Answer the following questions to choose the correct collection path:

**Question 1:** Can you reach the RPC endpoint?

```sh
curl -s -o /dev/null -w "%{http_code}" \
  "$(glassbox config show --json | jq -r '.rpc_url')/health" \
  || echo "unreachable"
```

- HTTP 200 → **network incident** — continue with live RPC steps.
- Non-200 or timeout → **offline incident** — use the offline path.

**Question 2:** Do you have a pre-saved snapshot?

```sh
ls "$INCIDENT_DIR"/snapshots.json 2>/dev/null && echo "snapshot found" || echo "no snapshot"
```

- Snapshot found → proceed with `--load-snapshots` in all debug commands.
- No snapshot → skip to local WASM replay if the WASM binary is available.

**Decision table:**

| RPC reachable | Snapshot available | Recommended path |
|---|---|---|
| Yes | Any | Live network collection (Steps 3-4 above) |
| No | Yes | Offline replay via --load-snapshots |
| No | No | WASM-only replay via --wasm (limited) |
| No | No | Bundle from existing logs only |

**For offline incidents:**

```sh
# WASM-only replay when no snapshot is available.
glassbox debug \
  --wasm ./path/to/contract.wasm \
  --json \
  --trace-verbosity verbose \
  --trace-output "$INCIDENT_DIR/trace-wasm.json" \
  > "$INCIDENT_DIR/debug-wasm.json" 2>&1
```

**For saving a snapshot during a live run (for future offline replay):**

```sh
glassbox debug \
  --network "$INCIDENT_NETWORK" \
  --save-snapshots "$INCIDENT_DIR/snapshots.json" \
  "$INCIDENT_TX" \
  > "$INCIDENT_DIR/debug-with-snapshot.json" 2>&1
```

---

## Step 8 — Build the sanitised bundle

After collecting individual artifacts, combine them into a portable diagnostics
bundle.  The bundle command automatically applies redaction rules.

```sh
# Build a named bundle from the incident directory.
glassbox doctor \
  --bundle \
  --bundle-output "$INCIDENT_DIR/incident-bundle.gbdiag" \
  --verbose \
  > "$INCIDENT_DIR/bundle-build.log" 2>&1

echo "bundle build exit code: $?"
ls -lh "$INCIDENT_DIR/incident-bundle.gbdiag"
```

The generated `.gbdiag` bundle:
- Contains version info, health check results, and log excerpts.
- Excludes private keys, tokens, RPC secrets, and file system paths.
- Is JSON-line encoded for easy parsing.

---

## Step 9 — Verify artifact integrity

Before sharing any artifact, verify it has not been tampered with and that it
contains the expected structure.

```sh
# Verify trace file structure.
jq 'type == "object" and has("events")' "$INCIDENT_DIR/trace.json" \
  && echo "trace: valid" \
  || echo "trace: invalid or missing"

# Verify health file structure.
jq 'has("status") and has("checks")' "$INCIDENT_DIR/health.json" \
  && echo "health: valid" \
  || echo "health: invalid or missing"

# Verify version file.
jq 'has("version")' "$INCIDENT_DIR/version.json" \
  && echo "version: valid" \
  || echo "version: invalid or missing"

# Verify the bundle file exists and is non-empty.
[ -s "$INCIDENT_DIR/incident-bundle.gbdiag" ] \
  && echo "bundle: present and non-empty" \
  || echo "bundle: missing or empty"
```

Compute a SHA-256 digest of each artifact for the chain-of-custody record:

```sh
sha256sum "$INCIDENT_DIR"/*.json "$INCIDENT_DIR"/*.gbdiag 2>/dev/null \
  > "$INCIDENT_DIR/checksums.sha256"
cat "$INCIDENT_DIR/checksums.sha256"
```

---

## Step 10 — Redact before sharing

Even though Glassbox applies automatic redaction, perform a final manual sweep
before attaching any artifact to an issue, email, or support ticket.

```sh
# Run the redact script over the entire incident directory.
# This strips common patterns: addresses, auth tokens, paths, PIDs.
bash scripts/redact-logs.sh "$INCIDENT_DIR" \
  > "$INCIDENT_DIR/redact.log" 2>&1

echo "redaction exit code: $?"
```

Verify the key patterns are absent from the redacted output:

```sh
# Patterns that must not appear in shared artifacts.
SENSITIVE_PATTERNS=(
  "GLASSBOX_RPC_TOKEN"
  "GLASSBOX_PKCS11_PIN"
  "private_key"
  "password"
  "api_key"
  "BEGIN PRIVATE KEY"
  "BEGIN EC PRIVATE KEY"
)

for pat in "${SENSITIVE_PATTERNS[@]}"; do
  matches=$(grep -rl "$pat" "$INCIDENT_DIR" 2>/dev/null | grep -v "redact.log")
  if [ -n "$matches" ]; then
    echo "FAIL: pattern '$pat' found in: $matches"
  else
    echo "OK:   '$pat' not found"
  fi
done
```

Verify transaction hashes are fingerprinted, not raw:

```sh
# After redaction, the raw TX hash should not appear verbatim.
# (It is replaced with a sha256: prefix + 25 hex chars by SanitizeValue.)
if grep -r "$INCIDENT_TX" "$INCIDENT_DIR" 2>/dev/null | grep -v "checksums\|bundle-build\|redact.log" | grep -q .; then
  echo "WARNING: raw TX hash still present — review before sharing"
else
  echo "OK: raw TX hash not found in artifacts"
fi
```

---

## Retention guidance

| Artifact | Retention period | Storage location | Notes |
|---|---|---|---|
| Incident bundle (.gbdiag) | 90 days | Secure ticket attachment | Auto-redacted |
| Trace JSON | 30 days | Incident directory | May contain contract call metadata |
| Health JSON | 30 days | Incident directory | No sensitive data |
| Structured logs (.ndjson) | 14 days | Incident directory | Redact before sharing |
| Snapshot registry | 7 days | Incident directory | Delete after replay verified |
| Crash suppress state | 7 days | ~/.Glassbox/ | Opaque fingerprints only |
| Telemetry queue | 48 hours | ~/.Glassbox/ | Auto-evicted by age policy |
| Checksums | Indefinitely | Incident directory | Chain-of-custody record |

To purge all incident artifacts after retention expires:

```sh
# Remove the incident directory (after verifying the ticket is resolved).
rm -rf "$INCIDENT_DIR"

# Remove local Glassbox state files (optional — preserves config).
rm -f ~/.Glassbox/telemetry_queue.ndjson
rm -f ~/.Glassbox/crash_suppress.json
```

---

## Escalation data

Include the following in every escalation (GitHub issue, support ticket, or
on-call page):

Required fields:

```
Glassbox version:      <output of: glassbox version --json | jq -r '.version'>
Network:               <testnet | mainnet | futurenet>
Transaction hash:      <first 8 chars only, e.g. "5c0a1bc2...">
Exit code:             <numeric exit code>
Error code:            <stable error code, e.g. RPC_CONNECTION_FAILED>
Platform:              <output of: uname -srm>
```

Optional fields (include when available):

```
Incident bundle:       <attach incident-bundle.gbdiag>
Trace file:            <attach trace.json if < 1 MB>
Health report:         <paste key fields from health.json>
Correlation IDs:       <paste operation_id values from logs>
Simulator version:     <output of: glassbox-sim --version 2>/dev/null>
Reproducible offline:  <yes | no | unknown>
```

Escalation contacts:

- GitHub Issues: https://github.com/dotandev/glassbox/issues
- Discussions: https://github.com/dotandev/glassbox/discussions
- Stellar Developer Discord: #glassbox channel

Do not paste raw transaction hashes, contract IDs, private keys, or RPC tokens
in public issues.  Attach a redacted bundle instead.

---

## Validation checklist

Use this checklist to confirm the incident collection is complete before
escalating.  Mark each item as complete before proceeding to the next:

Environment setup:
- GLASSBOX_TELEMETRY=false was set before any collection command
- GLASSBOX_LOG_FORMAT=json was set for structured log output
- An incident working directory was created and all artifacts directed there

Artifacts collected:
- version.json collected and non-empty
- config.json collected and non-empty (no secrets visible)
- dry-run.json collected
- trace.json or trace-offline.json collected and validates as JSON with an "events" key
- health.json collected and validates with "status" and "checks" keys
- debug-logs.ndjson collected and contains structured log lines
- telemetry-status.txt collected

Integrity verification:
- All artifact checksums computed and saved to checksums.sha256
- trace.json validates with jq
- health.json validates with jq
- version.json validates with jq
- incident-bundle.gbdiag is present and non-empty

Redaction verification:
- scripts/redact-logs.sh applied to the incident directory
- All SENSITIVE_PATTERNS verified absent
- Raw transaction hash verified absent from shared artifacts

Escalation readiness:
- Required escalation fields are filled in
- Incident bundle prepared for attachment
- Retention period recorded for each artifact

---

## Expected stable errors

These errors are expected in specific scenarios and do not require escalation:

| Error | Scenario | Action |
|---|---|---|
| `RPC_CONNECTION_FAILED` | Offline incident | Use --load-snapshots |
| `TRANSACTION_NOT_FOUND` | Wrong network specified | Retry with correct --network |
| `SIMULATOR_NOT_FOUND` | Simulator not built | Run glassbox doctor --fix |
| `SOURCE_DISCOVERY_FAILED` | No debug symbols | Add --skip-source-mapping |
| `dry-run: skipped simulation` | --dry-run mode | Expected; not an error |
| Exit code 1 | Any CLI error | See error_code field in JSON output |
| Exit code 2 | Validation error | Check input hash format or flags |

---

## See also

- [Observability Troubleshooting](./observability-troubleshooting.md) — Prometheus metrics, OTEL traces, correlation IDs
- [Diagnostics Bundle](./diagnostics-bundle.md) — bundle format and redaction rules
- [Operator Runbook: RPC and Simulator Failures](./operator-runbook.md) — symptom-based remediation
- [Telemetry Metadata](./telemetry-metadata.md) — what is collected and how to disable it
- [Stable Error Codes](./stable-error-codes.md) — full error code catalogue
- [Debug Command](./debug-command.md) — full CLI reference
