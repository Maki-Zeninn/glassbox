# Operator Runbook: RPC and Simulator Failures

This runbook organises common failures by **symptom**, lists the **evidence to
collect**, provides **safe remediation** steps, and indicates **when to generate
a diagnostics bundle** for escalation.

> **Principle:** Every fix in this runbook is **safe** — it does not modify your
> contract, ledger state, or deployment. If a step would require modification,
> it is marked as "escalate" instead.

---

## Table of Contents

1. [RPC connection failures](#rpc-connection-failures)
2. [RPC timeouts](#rpc-timeouts)
3. [Transaction not found](#transaction-not-found)
4. [Rate limiting](#rate-limiting)
5. [Authentication errors](#authentication-errors)
6. [Simulator binary not found](#simulator-binary-not-found)
7. [Simulator crashes](#simulator-crashes)
8. [Simulation logic errors](#simulation-logic-errors)
9. [Source mapping failures](#source-mapping-failures)
10. [Protocol version mismatch](#protocol-version-mismatch)
11. [Snapshot corruption](#snapshot-corruption)
12. [Session integrity failures](#session-integrity-failures)
13. [Export failures](#export-failures)
14. [Diagnostics bundle](#generating-a-diagnostics-bundle)

---

## RPC connection failures

**Stable error code:** `RPC_CONNECTION_FAILED`

**Symptoms:**
- `glassbox debug` fails immediately with `RPC connection failed: dial tcp ...`
- All failover endpoints report the same error

**Evidence to collect:**

```sh
# Test basic connectivity
curl -s -o /dev/null -w "%{http_code}" https://soroban-testnet.stellar.org

# Check DNS resolution
nslookup soroban-testnet.stellar.org

# Verify Glassbox config
glassbox config show --json | jq '.rpc_url'
```

**Safe remediation:**

1. **Check internet connection** — open a browser or `curl` a known URL.
2. **Try an alternative endpoint** — pass `--rpc-url` with a different endpoint:
   ```sh
   glassbox debug --rpc-url https://rpc.stellar.org --network testnet <tx-hash>
   ```
3. **Check firewall rules** — ensure outbound HTTPS (443) is allowed.
4. **Verify RPC token** — if using a private endpoint, confirm the token is
   valid:
   ```sh
   echo $GLASSBOX_RPC_TOKEN | head -c 8  # should print first 8 chars only
   ```

**When to escalate:** If the endpoint is reachable via `curl` but Glassbox
cannot connect, generate a diagnostics bundle:

```sh
glassbox doctor --bundle --bundle-output ./diag-connection.gbdiag
```

---

## RPC timeouts

**Stable error code:** `RPC_TIMEOUT`

**Symptoms:**
- Debug run hangs for > 10 seconds then fails with timeout error
- `--show-metrics` shows high `rpc_avg_ms`

**Evidence to collect:**

```sh
# Measure RPC latency
time curl -s -X POST https://soroban-testnet.stellar.org \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"getHealth"}'

# Check Glassbox metrics
glassbox debug --show-metrics --network testnet <tx-hash>
```

**Safe remediation:**

1. **Check RPC endpoint health:**
   ```sh
   curl -s https://soroban-testnet.stellar.org | jq .
   ```
2. **Use a closer endpoint** — if on a different continent, try a regional
   endpoint.
3. **Increase timeout** — set `request_timeout` in `glassbox.toml`:
   ```toml
   [rpc]
   request_timeout = 30  # seconds (default: 15)
   ```
4. **Check for slow calls** — `--show-metrics` highlights calls > 3 seconds
   with a remediation tip.

**When to escalate:** If latency exceeds 5 seconds consistently, file an issue
with the `--show-metrics` JSON output attached.

---

## Transaction not found

**Stable error code:** `TRANSACTION_NOT_FOUND`

**Symptoms:**
- `transaction not found` error after successful RPC connection

**Evidence to collect:**

```sh
# Verify hash format (64 lowercase hex chars)
echo "<tx-hash>" | wc -c  # should be 65 (64 + newline)

# Check the correct network
glassbox debug --network testnet <tx-hash>
glassbox debug --network mainnet <tx-hash>

# Verify the transaction exists on the network
curl -s "https://soroban-testnet.stellar.org/transactions/<tx-hash>" | jq .
```

**Safe remediation:**

1. **Verify hash** — ensure 64 lowercase hexadecimal characters.
2. **Check network** — the transaction may be on a different network. Use
   `--network testnet`, `--network mainnet`, or `--network futurenet`.
3. **Wait for propagation** — newly submitted transactions may take a few
   seconds to appear. Use `--watch` to poll:
   ```sh
   glassbox debug --watch --watch-timeout 60 --network testnet <tx-hash>
   ```
4. **Check confirmation** — some RPC providers require confirmation before
   returning transaction data.

**When to escalate:** If the transaction is confirmed on the network explorer
but Glassbox cannot find it, generate a diagnostics bundle.

---

## Rate limiting

**Stable error code:** `RATE_LIMIT_EXCEEDED`

**Symptoms:**
- `rate limit exceeded` error from RPC
- Multiple rapid requests fail

**Evidence to collect:**

```sh
# Check current rate limit headers (if returned by endpoint)
curl -s -D - -X POST https://soroban-testnet.stellar.org \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"getHealth"}' | grep -i "ratelimit\|retry-after"
```

**Safe remediation:**

1. **Wait and retry** — respect the `Retry-After` header if present.
2. **Reduce request frequency** — avoid running multiple `glassbox debug`
   sessions simultaneously against the same endpoint.
3. **Use a private endpoint** — if available, configure `--rpc-url` with a
   dedicated endpoint.
4. **Enable caching** — use `--no-cache=false` (default) to leverage local
   ledger state caching.

**When to escalate:** Persistent rate limiting with low request volume indicates
a misconfiguration. Generate a diagnostics bundle.

---

## Authentication errors

**Stable error code:** `UNAUTHORIZED`

**Symptoms:**
- `unauthorized` or `missing/invalid auth token` error

**Evidence to collect:**

```sh
# Check token is set (show first 8 chars only)
echo "${GLASSBOX_RPC_TOKEN:0:8}..."

# Verify token format (typically a JWT or opaque string)
glassbox config show --json | jq '.rpc_token' | head -c 20
```

**Safe remediation:**

1. **Verify token** — ensure `GLASSBOX_RPC_TOKEN` is set and not expired.
2. **Re-authenticate** — generate a new token from your RPC provider dashboard.
3. **Check token scope** — some tokens are network-specific (testnet vs
   mainnet).

**When to escalate:** If the token is valid for other tools but rejected by
Glassbox, check for extra whitespace or newline characters in the environment
variable.

---

## Simulator binary not found

**Stable error code:** `SIMULATOR_NOT_FOUND`

**Symptoms:**
- `simulator binary not found` error
- `glassbox doctor --fix` suggests building the simulator

**Evidence to collect:**

```sh
# Check if simulator exists
which glassbox-sim 2>/dev/null || echo "not found"

# Check Glassbox doctor
glassbox doctor --verbose
```

**Safe remediation:**

1. **Run doctor --fix:**
   ```sh
   glassbox doctor --fix
   ```
   In an interactive terminal, Glassbox asks before each fix. In CI or another
   non-interactive environment, use `--yes` only when the job is authorized to
   apply those fixes; without it, the command returns an error instead of
   accepting the prompts automatically:
   ```sh
   glassbox --non-interactive doctor --fix --yes
   ```
2. **Build manually:**
   ```sh
   cd simulator && cargo build --release
   ```
3. **Set explicit path** — if the binary is in a non-standard location:
   ```toml
   # glassbox.toml
   [simulator]
   path = "/path/to/glassbox-sim"
   ```

**When to escalate:** If `cargo build` fails, check Rust toolchain version
(`rustc --version`) and ensure the `wasm32-unknown-unknown` target is installed:
```sh
rustup target add wasm32-unknown-unknown
```

---

## Simulator crashes

**Stable error code:** `SIMULATOR_CRASH`

**Symptoms:**
- Process exits unexpectedly with signal (SIGSEGV, SIGABRT)
- `simulator process crashed unexpectedly` error

**Evidence to collect:**

```sh
# Run with verbose logging
glassbox debug --verbose --network testnet <tx-hash>

# Check simulator version
glassbox-sim --version 2>/dev/null || echo "version unknown"

# Generate diagnostics bundle
glassbox doctor --bundle --bundle-output ./diag-crash.gbdiag
```

**Safe remediation:**

1. **Rebuild the simulator** — crashes may be caused by stale binaries:
   ```sh
   cd simulator && cargo build --release
   ```
2. **Check memory limits** — large contracts may exceed available memory.
   Monitor with `top` or `htop` during execution.
3. **Reduce trace depth** — if the contract has deep call stacks:
   ```sh
   glassbox debug --trace-verbosity summary --network testnet <tx-hash>
   ```

**When to escalate:** Generate a diagnostics bundle and file an issue with the
crash signal and stack trace.

---

## Simulation logic errors

**Stable error code:** `SIMULATION_LOGIC_ERROR`

**Symptoms:**
- Simulation completes but reports a contract logic error
- `simulation execution failed: <detail>`

**Evidence to collect:**

```sh
# Full trace output
glassbox debug --trace-verbosity verbose --json --network testnet <tx-hash> | jq .

# Check budget usage
glassbox debug --show-metrics --network testnet <tx-hash>

# Decode the error
glassbox debug --format json --network testnet <tx-hash> | jq '.result.error'
```

**Safe remediation:**

1. **Check the error code** — stable error codes are listed in
   [stable-error-codes.md](./stable-error-codes.md).
2. **Review the trace** — look at the `states` array for the failing step and
   its `source_location`.
3. **Check budget** — if `cpu_usage_percent` or `memory_usage_percent` is near
   100%, the contract hit resource limits.
4. **Verify arguments** — ensure `--args` match the contract's expected
   parameters.
5. **Compare with network** — run with `--compare-network` to see if the
   failure reproduces differently on another network.

**When to escalate:** If the simulation fails but the same transaction succeeds
on-chain, the issue may be a snapshot or ledger state mismatch. Generate a
diagnostics bundle with `--save-snapshots`.

---

## Source mapping failures

**Stable error code:** `SOURCE_DISCOVERY_FAILED`

**Symptoms:**
- `contract source not found: all discovery stages exhausted`
- Source locations show `unknown` or WAT disassembly

**Evidence to collect:**

```sh
# Dry-run source discovery
glassbox debug --dry-run --network testnet <tx-hash>

# Check WASM binary
file <path-to-wasm>
wasm-objdump -h <path-to-wasm> | grep debug

# Verify source path
glassbox debug --wasm <path> --contract-source ./src --dry-run <tx-hash>
```

**Safe remediation:**

1. **Provide explicit source path:**
   ```sh
   glassbox debug --wasm ./contract.wasm --contract-source ./src <tx-hash>
   ```
2. **Use source alias** for path remapping:
   ```sh
   glassbox debug --source-alias ./aliases.json --wasm ./contract.wasm <tx-hash>
   ```
3. **Skip source mapping** if only raw trace is needed:
   ```sh
   glassbox debug --skip-source-mapping --wasm ./contract.wasm <tx-hash>
   ```

See [source-mapping-troubleshooting.md](./source-mapping-troubleshooting.md)
for the full troubleshooting wizard.

---

## Protocol version mismatch

**Stable error code:** `SIMULATION_FAILED` (with protocol version context)

**Symptoms:**
- `unsupported protocol version` error
- Simulation fails with version-related diagnostic

**Evidence to collect:**

```sh
# Check Glassbox version
glassbox version --json

# Check network protocol
glassbox config show --json | jq '.network'
```

**Safe remediation:**

1. **Update Glassbox** — download the latest release for protocol support.
2. **Override protocol version** (if you know the correct version):
   ```sh
   glassbox debug --protocol-version <n> --network testnet <tx-hash>
   ```
3. **Check network compatibility** — `futurenet` may support protocols not yet
   on `testnet`.

**When to escalate:** If the protocol version is supported but simulation still
fails, file an issue with the `glassbox version --json` output.

---

## Snapshot corruption

**Stable error code:** `SIMULATION_FAILED` (with snapshot context)

**Symptoms:**
- `snapshot fingerprint mismatch` error
- `snapshot tx hash mismatch` or `snapshot network mismatch`
- `snapshot is stale` error

**Evidence to collect:**

```sh
# Inspect the snapshot registry
cat <registry-file> | jq .

# Check stored hash vs computed
glassbox debug --load-snapshots <registry-file> --verbose <tx-hash>
```

**Safe remediation:**

1. **Regenerate snapshots** — delete the old file and re-capture:
   ```sh
   glassbox debug --save-snapshots ./registry.json --network testnet <tx-hash>
   ```
2. **Check staleness** — CLI parameters must match between capture and replay.
   Re-capture with the same flags.
3. **Verify network** — snapshots are network-specific. A testnet snapshot
   cannot replay on mainnet.

**When to escalate:** If freshly captured snapshots fail immediately, file an
issue with the registry JSON attached.

---

## Session integrity failures

**Symptoms:**
- `Session integrity check FAILED` on `session resume`
- Checkpoint validation errors after a crash

**Evidence to collect:**

```sh
# List sessions
glassbox session list

# Attempt recovery
glassbox session recover
```

**Safe remediation:**

1. **Recover from crash:**
   ```sh
   glassbox session recover
   ```
   If recovery fails, the checkpoint is corrupt — proceed to step 2.
2. **Delete corrupt session:**
   ```sh
   glassbox session delete <session-id>
   ```
3. **Re-run debug:**
   ```sh
   glassbox debug --network testnet <tx-hash>
   ```

See [debug-command.md](./debug-command.md#session-recovery-and-integrity) for
full integrity field validation details.

---

## Export failures

**Symptoms:**
- `--trace-output` fails to write
- Exported file is empty or malformed
- SVG export fails

**Evidence to collect:**

```sh
# Check disk space
df -h .

# Check write permissions
touch ./test-write && rm ./test-write

# Validate trace output path
glassbox debug --dry-run --trace-output ./traces/debug.json --network testnet <tx-hash>
```

**Safe remediation:**

1. **Verify disk space** — ensure sufficient space for the output file.
2. **Check permissions** — the output directory must be writable.
3. **Use absolute paths** — relative paths may resolve unexpectedly:
   ```sh
   glassbox debug --trace-output /absolute/path/to/output.json --network testnet <tx-hash>
   ```
4. **Validate output** — after export, verify the file:
   ```sh
   jq . <output-file>  # should parse without error
   ```

**When to escalate:** If the directory is writable but export still fails,
generate a diagnostics bundle.

---

## Plugin policy blocked (untrusted / community plugins)

**Symptoms:**
- Error message: `plugin "..." has trust level "community" (source: ...) which is not allowed by policy`
- Plugins fail to load at startup or during `glassbox plugin list` / decoding.

**Evidence to collect:**

```sh
# List plugins and check their trust levels
glassbox plugin list
```

**Safe remediation:**
By default, Glassbox blocks community and untrusted plugins (`AllowUntrusted = false`) to protect against supply-chain compromises. To allow them explicitly:
1. **Pass `--allow-untrusted-plugins` on the CLI:**
   ```sh
   glassbox --allow-untrusted-plugins plugin list
   ```
2. **Or configure in `glassbox.toml`:**
   ```toml
   [plugin]
   allow_untrusted = true
   ```
3. **Or set environment variable:**
   ```sh
   export GLASSBOX_PLUGIN_ALLOW_UNTRUSTED=true
   ```

---

## Generating a diagnostics bundle

When a fix cannot be determined from the steps above, generate a portable
diagnostics archive:

```sh
# Standard bundle (auto-named in temp directory)
glassbox doctor --bundle

# Named bundle with verbose output
glassbox doctor --verbose --bundle --bundle-output ./diag.gbdiag

# Include in an issue or PR
gh issue create --title "Bug: <description>" \
  --body "Diagnostics: $(glassbox doctor --bundle --bundle-output ./diag.gbdiag)" \
  --repo pugsley76/glassbox
```

The bundle is automatically redacted — no private keys, tokens, or sensitive
data are included. See [diagnostics-bundle.md](./diagnostics-bundle.md) for
details.

---

## Offline reproduction

For reproducible offline debugging:

1. **Capture a snapshot:**
   ```sh
   glassbox debug --save-snapshots ./repro-registry.json --network testnet <tx-hash>
   ```
2. **Replay without network:**
   ```sh
   glassbox debug --load-snapshots ./repro-registry.json
   ```
3. **Share the snapshot** — it contains everything needed to reproduce the issue
   locally.

---

## Quick reference: error codes to symptoms

| Error code | Likely cause | First step |
|-----------|-------------|------------|
| `RPC_CONNECTION_FAILED` | Network or endpoint issue | Check internet, try `--rpc-url` |
| `RPC_TIMEOUT` | Slow endpoint or network | Check `--show-metrics`, try alternative endpoint |
| `TRANSACTION_NOT_FOUND` | Wrong network or hash | Verify network and hash format |
| `RATE_LIMIT_EXCEEDED` | Too many requests | Wait, reduce frequency, use private endpoint |
| `UNAUTHORIZED` | Invalid or expired token | Re-authenticate with provider |
| `SIMULATOR_NOT_FOUND` | Missing binary | `glassbox doctor --fix` |
| `SIMULATOR_CRASH` | Binary or memory issue | Rebuild simulator, reduce trace depth |
| `SIMULATION_LOGIC_ERROR` | Contract or state issue | Check trace, budget, arguments |
| `SOURCE_DISCOVERY_FAILED` | Missing source or symbols | Provide `--contract-source` |
| `ALL_RPC_FAILED` | All endpoints down | Check provider status page |

## See also

- [Debug command](./debug-command.md) — full command reference
- [Source mapping troubleshooting](./source-mapping-troubleshooting.md) — source mapping wizard
- [Diagnostics bundle](./diagnostics-bundle.md) — portable diagnostics
- [CI artifacts](./ci-artifacts.md) — reproducing CI failures
- [Stable error codes](./stable-error-codes.md) — error code catalogue
- [Sandboxed replay](./sandboxed-replay.md) — offline debugging
