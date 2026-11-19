# ADR-004: Data Classification and Cross-Boundary Data Flows

## Status

Accepted

## Context

Glassbox handles several categories of data with materially different
sensitivity: signing credentials, session tokens, transaction payloads,
simulation results, telemetry, and user-supplied metadata. The same session can
involve all of them simultaneously, and they flow across at least five process
or network boundaries (see ADR-003).

Without an explicit classification of what data is sensitive and a description
of what controls apply at each boundary crossing, it is impossible to audit
whether sensitive data is being inadvertently leaked — for example into telemetry
payloads, simulator subprocess arguments, crash reports, or the signed audit log.

This ADR records the classification taxonomy, the controls that apply at each
crossing, and the properties that are absent and therefore must not be claimed.

## Decision

### 1. Data classification taxonomy

| Class | Label | Examples | Controls required |
|---|---|---|---|
| **Signing credentials** | `SECRET` | Ed25519 PEM / hex seed, PKCS#11 PIN (`GLASSBOX_PKCS11_PIN`), AWS IAM key/secret | Never logged, never telemetered, never passed to subprocesses, redacted in audit log |
| **RPC / network credentials** | `SECRET` | RPC bearer tokens (`--rpc-token`), URL userinfo (`user:password@host`) | Redacted from audit log, stripped from displayed URLs, not passed to simulator subprocess |
| **Private key material (HSM/KMS)** | `SECRET` | Raw Ed25519 seed (software provider), KMS private key bytes | Software seed lives only in process memory; KMS key never leaves AWS |
| **Transaction payload** | `INTERNAL` | Envelope XDR, ledger entries, linear memory, `ResultMetaXdr` | Stays within CLI host and simulator boundary; not transmitted to telemetry or crash endpoints |
| **Simulation result** | `INTERNAL` | Trace events, budget usage, auth diagnostics, stack traces | Crosses CLI↔simulator boundary via bounded JSON IPC; validated before host consumption |
| **Signed audit log** | `INTERNAL` | Canonical payload, SHA-256 hash, Ed25519 signature, public key | Written to disk by host only; secrets are redacted before signing |
| **Snapshot / ledger state** | `INTERNAL` | `ledgerEntries`, `linearMemory`, fingerprint | Stored content-addressed locally (`~/.glassbox/cache/snapshots/`); not transmitted to external services |
| **User metadata** | `INTERNAL` | `--metadata key=value` entries | Length-capped (key ≤128 chars, value ≤1024 chars), null-byte stripped, embedded in signed audit log |
| **Telemetry / OTLP** | `PUBLIC` | Command name, trace spans, hash fingerprints | Command name sanitised (alphanumeric/dash/colon/underscore, ≤64 chars); hash values replaced with 32-char fingerprint (`sha256:<prefix>`) |
| **Crash reports** | `PUBLIC` | Panic stack, error message | Opt-in only; no transaction payload, no credentials, version/commit only |
| **ABI / contract spec** | `PUBLIC` | Function signatures, struct layouts | May be written to disk, embedded in TypeScript bindings, or transmitted as part of check-bindings CI workflow |
| **Deep link parameters** | `UNTRUSTED INPUT` | `glassbox://` URI query params | Validated before dispatch; null bytes rejected; `source` and `signature` params are free-form and explicitly untrusted |

### 2. Boundary crossing catalogue

Each row describes data that physically crosses a boundary, the mechanism, and
the controls applied.

#### Boundary A: CLI Host → Simulator Subprocess

| Data | Direction | Mechanism | Controls |
|---|---|---|---|
| `SimulationRequestSchema` (XDR envelope, ledger overrides, network, sandbox config) | Host → Simulator | Length-bounded stdin JSON | Subprocess environment inherits parent env with `RUST_LOG` adjusted; credentials must not be exported into the CLI process environment |
| `SimulationResponseSchema` (trace, budget, auth, stack, error codes) | Simulator → Host | Length-bounded stdout JSON | Host validates schema before consuming any field; stdout capped at 10 MB, stderr capped at 1 MB |

**What does NOT cross this boundary in the payload:**
- Any signing credential or RPC token (these are not serialised into the JSON request body)
- The raw Ed25519 seed or PKCS#11 PIN
- The signed audit log or snapshot store paths

**Important:** `simulatorEnv()` in `internal/simulator/runner.go` starts from `os.Environ()` and only adjusts `RUST_LOG`; it does **not** strip secrets from the inherited environment. Operators must not export signing credentials (e.g. `GLASSBOX_SOFTWARE_PRIVATE_KEY_HEX`, `GLASSBOX_PKCS11_PIN`) into the shell environment in which the CLI runs if the simulator subprocess must not see them.

#### Boundary B: CLI Host → Plugin Subprocess

| Data | Direction | Mechanism | Controls |
|---|---|---|---|
| Plugin input payload (command-specific JSON) | Host → Plugin | stdin JSON | Minimal env (`GLASSBOX_PLUGIN_NAME`, `GLASSBOX_PLUGIN_VERSION`, `GLASSBOX_API_VERSION`, `PATH`); plugin binary checksum verified before exec |
| Plugin output payload | Plugin → Host | stdout JSON | Host validates; per-call `context.WithTimeout(10s)` kills the child process if it does not respond in time; stderr is discarded (`io.Discard`) |

**What does NOT cross this boundary:**
- Signing credentials
- Session secrets or RPC tokens
- Raw ledger state or snapshots (unless the plugin has `read_fs` permission explicitly granted)

#### Boundary C: CLI Host → Signing Provider

| Data | Direction | Mechanism | Controls |
|---|---|---|---|
| `data []byte` — the 32-byte SHA-256 digest of the canonical payload, passed as `hash[:]` | Host → Provider | In-process function call (`Signer.Sign(data []byte)`) for software/PKCS#11, or HTTPS to AWS KMS | The `Signer` interface accepts raw bytes; by convention the CLI passes only the 32-byte hash. Raw payload bytes are never passed. |
| Ed25519 signature bytes (64 bytes) | Provider → Host | `Signer.Sign` return value | Returned to host for embedding in audit log |
| Public key bytes (32 bytes) | Provider → Host | `Signer.PublicKey()` return value | Embedded in audit log |

**Note on the interface:** `internal/signer/signer.go` declares `Sign(data []byte) ([]byte, error)` — the parameter is untyped bytes. The CLI layer (`internal/cmd/audit.go`) is responsible for passing `hash[:]` (the SHA-256 digest) rather than the raw payload. The interface itself does not enforce this.

**What does NOT cross this boundary for KMS:**
- The canonical JSON payload
- The raw private key bytes (key never leaves AWS)

#### Boundary D: CLI Host → External RPC / Network Services

| Data | Direction | Mechanism | Controls |
|---|---|---|---|
| Transaction hash, network identifier | Host → RPC | HTTPS JSON-RPC | Not a secret; used to fetch transaction data |
| Ledger entries, XDR response | RPC → Host | HTTPS JSON-RPC | Treated as untrusted input; parsed and validated before use |
| OTLP trace spans | Host → Telemetry collector | HTTPS | Command name sanitised; hash values fingerprinted; no payload data |
| Crash report (panic + stack) | Host → Sentry endpoint | HTTPS | Opt-in; no credentials, no payload; version/commit only |

**What does NOT cross this boundary:**
- The signed audit log or snapshot files (these are local only, unless the
  operator explicitly uses `--publish-ipfs` or `--publish-arweave`)
- Signing credentials

#### Boundary E: CLI Host → Local Persistence

| Data | Direction | Mechanism | Controls |
|---|---|---|---|
| `PersistedSnapshot` (ledger entries, linear memory, metadata) | Host → Filesystem | Atomic write (write-to-tmp then rename) | Fingerprint computed and embedded; verified on load |
| Session state | Host → SQLite (`modernc.org/sqlite`) | In-process SQL | No credentials stored; session IDs only |
| Signed audit log | Host → Filesystem | Write to operator-specified path | Secrets redacted before signing; canonical form is the signed artifact |

**What does NOT cross this boundary:**
- Raw signing credentials (the key is never written to the snapshot store or session DB)

#### Boundary F: OS / Browser → CLI (Deep Link)

| Data | Direction | Mechanism | Controls |
|---|---|---|---|
| `glassbox://` URI | OS → CLI | `protocol:handle` command dispatch | Null bytes rejected; hash must be 64 hex chars; network must be allowlisted enum; `mock-ledger-manifest` path checked for null bytes; `mock-ledger-entry` values must be valid base64 |
| Translated CLI flags | Deep link parser → `debug` command | Internal re-invocation | All validation happens in `ParseDebugURI` before flags are constructed |

#### Boundary G: TypeScript Bindings (Browser) → RPC

| Data | Direction | Mechanism | Controls |
|---|---|---|---|
| Simulation request (contract ID, function args) | Browser → RPC | `fetch` API (HTTPS) | No local subprocess; `child_process`/`fs`/`path` excluded from bundle |
| Simulation response | RPC → Browser | `fetch` API (HTTPS) | Consumed by generated client code; no CLI host process involved |

### 3. Redaction rules for the audit log

Before a payload is canonicalised and signed, the following redaction pass is
applied by `internal/cmd/audit.go`:

| Trigger | Action |
|---|---|
| CLI flag name contains `token`, `secret`, `password`, `private`, `key`, `pin`, or `passphrase` | Flag value replaced with `REDACTED` |
| Long (≥16 char) flag value that contains hex characters | Value replaced with `REDACTED` (likely-secret heuristic) |
| Error message contains a file path (`/…` or `C:\…`) | Path segments replaced with `<path>` |
| Error message contains a likely-secret value | Value replaced with `REDACTED` |
| `--metadata` key empty or >128 chars, value >1024 chars, or contains null bytes | Entry skipped silently |

### 4. Properties explicitly NOT claimed

The following properties are **not** provided and must not be assumed:

- **End-to-end encryption of the IPC channel.** Communication between the CLI
  host and the simulator subprocess is via local stdio — there is no
  cryptographic confidentiality or integrity protection beyond OS process
  isolation. A local attacker with sufficient privilege could observe or tamper
  with the pipe.
- **Simulator environment credential isolation.** `simulatorEnv()` inherits the
  full parent environment and only adjusts `RUST_LOG`. Signing credentials and
  RPC tokens present in the shell environment are visible to the simulator
  subprocess. Operators are responsible for not exporting secrets before
  invoking the CLI.
- **Confidentiality of the snapshot store.** Snapshots are stored in
  `~/.glassbox/cache/snapshots/` in plaintext JSON. The fingerprint detects
  tampering but does not encrypt contents. An attacker with read access to the
  home directory can read ledger state.
- **Integrity of the telemetry pipeline.** Telemetry is emitted over HTTPS but
  the OTLP collector is operator-configured and its security is outside
  Glassbox's control.
- **Audit log confidentiality.** The audit log is signed but not encrypted;
  anyone with the file can read the payload (post-redaction).

## Rationale

### Why classify at data level rather than component level?

Component-level controls (e.g. "the simulator is untrusted") do not answer the
question "can the PKCS#11 PIN reach the simulator?" Data-level classification
paired with an explicit boundary-crossing catalogue answers that question
directly.

### Why is ledger state `INTERNAL` rather than `SECRET`?

Ledger state is transaction data that the operator is actively debugging; it
is not a credential. However, it may contain sensitive business logic, so it is
classified `INTERNAL` (not transmitted to external services, not included in
telemetry) rather than `PUBLIC`.

### Why is the deep link `source` parameter explicitly untrusted?

The `source` parameter is a free-form analytics label and is not validated
beyond URL encoding. Treating it as untrusted prevents future code from
relying on it for access control or audit provenance.

### Alternatives considered

**Encrypt the snapshot store at rest:** Would protect ledger state from local
read attacks. Rejected for the current version because key management for
at-rest encryption introduces its own complexity; the fingerprint provides
tamper detection. At-rest encryption is a candidate for a future ADR.

**Structured redaction schema (allowlist instead of denylist):** The current
redaction is heuristic (flag-name denylist + length/hex heuristic). An allowlist
of fields permitted in the audit log would be stronger. Deferred; the heuristic
is conservative enough for the current threat model and a schema-validated
approach is planned as the `AuditPayloadSchema` matures.

## Implementation

| Claim | Verified in |
|---|---|
| Simulator env: inherits parent env, adjusts `RUST_LOG` only | `internal/simulator/runner.go` (`simulatorEnv`) |
| Stdout cap 10 MB / stderr cap 1 MB on simulator | `internal/simulator/runner.go` (`limitedBuffer`) |
| Plugin minimal env (`GLASSBOX_PLUGIN_*`, `PATH` only) | `internal/plugin/sandbox.go` (`buildSandboxEnv`) |
| Plugin 10s per-call timeout via `context.WithTimeout` | `internal/plugin/sandbox.go` (`sandboxTimeout = 10 * time.Second`) |
| Plugin stderr discarded (`io.Discard`) | `internal/plugin/sandbox.go` (`cmd.Stderr = io.Discard`) |
| Redaction rules for audit log | `internal/cmd/audit.go`, `docs/security-warnings.md` |
| Metadata key/value validation | `docs/security-warnings.md` |
| Command name sanitisation for telemetry | `docs/security-warnings.md` |
| Hash fingerprinting for telemetry | `docs/security-warnings.md` |
| URL credential stripping in `config show` | `docs/security-warnings.md` |
| Snapshot atomic write + fingerprint | `internal/snapshot/`, `docs/snapshot-deduplication.md` |
| Deep link null-byte / base64 / length validation | `internal/protocolreg/uri.go`, `docs/adr/deeplink-parameters.md` |
| Browser bindings exclude `child_process`/`fs`/`path` | `docs/bindings-environments.md` |
| Crash reporting opt-in, no payload | `cmd/glassbox/main.go` |
| KMS: only hash bytes cross to AWS | `internal/signer/kms.go`, `docs/audit-kms-signing.md` |

## Consequences

**Positive:**
- A reviewer can trace any named piece of data (e.g. the PKCS#11 PIN) through
  the boundary catalogue and confirm it does not reach the simulator, plugins,
  telemetry, or crash endpoints.
- The explicit "not claimed" section prevents false assurances in security
  reviews.

**Negative / trade-offs:**
- Snapshot store is plaintext; operators with sensitive ledger data should
  apply OS-level filesystem permissions (chmod 700 on `~/.glassbox/`) until
  at-rest encryption is implemented.
- The heuristic redaction for audit logs will miss a credential whose flag name
  does not match the denylist and whose value is shorter than 16 chars or
  contains no hex. Operators should review `--metadata` payloads before
  distributing signed audit logs.

**Migration impact:**
- No changes to data handling are introduced by this ADR; it documents existing
  behaviour derived from code inspection.
- Teams using `--audit-log` in regulated environments should review the
  "not claimed" section and apply additional controls (filesystem ACLs, log
  encryption) appropriate to their compliance requirements.

## Related

- [ADR-003: Trust Boundaries and Component Trust Levels](003-trust-boundaries.md)
- [ADR-005: Canonicalization Ownership](005-canonicalization-ownership.md)
- [Audit Signing](../audit-signing.md)
- [Security Warnings and Redaction](../security-warnings.md)
- [Snapshot Deduplication](../snapshot-deduplication.md)
- [Deep Link Parameter Semantics](deeplink-parameters.md)
- [Bindings Environments](../bindings-environments.md)
