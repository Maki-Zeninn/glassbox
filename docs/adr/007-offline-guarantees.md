# ADR-007: Offline Guarantees

## Status

Accepted

## Context

Glassbox is primarily designed as a connected tool: it fetches transaction data
from a Soroban JSON-RPC node, submits signed audit logs via RPC, and optionally
sends telemetry to an OTLP collector. However, several important workflows must
remain functional without network access:

1. **Air-gapped audit signing** — a security-sensitive environment may require
   that signing happen on a machine with no network connection.
2. **Snapshot replay** — re-running a simulation from a previously captured
   local snapshot (e.g. for reproducible debugging, regression tests, or CI
   without RPC access).
3. **Offline verification** — verifying a signed audit log using only the public
   key and the log file, without contacting any Glassbox service.
4. **Deferred submission** — signing locally and submitting the signed envelope
   later when network connectivity is available.

Without an explicit record of what is and is not guaranteed to work offline, an
operator cannot know whether an air-gapped signing pipeline is safe, or whether
a CI job that loses RPC access mid-run will silently degrade.

## Decision

### 1. Offline-capable operations

The following operations are guaranteed to work with no network access,
provided the required local inputs are available:

| Operation | Required local inputs | Network used? |
|---|---|---|
| `audit:sign` (software or PKCS#11 provider) | Canonical payload or payload file, Ed25519 PEM key or PKCS#11 token | No |
| `audit:sign` (envelope file flow, `internal/offline/envelope.go`) | `EnvelopeFile` with embedded payload, signing key | No |
| `audit:verify` | Signed audit log file, public key (embedded or `--public-key`) | No |
| Snapshot replay (`--offline` / local snapshot) | `PersistedSnapshot` file in `~/.glassbox/cache/snapshots/` | No |
| Binding staleness check (`check-bindings`) against a local WASM or ABI file | WASM binary or JSON ABI file on disk | No |
| `glassbox config show` | Local config TOML | No |

### 2. Operations that require network access

| Operation | Why network is required |
|---|---|
| `audit:sign` (AWS KMS provider) | Every `kms:Sign` call requires HTTPS to the KMS API |
| `debug` command (live transaction) | Fetches envelope XDR and ledger state from Soroban RPC |
| RPC submission of signed audit envelope | `internal/offline/submitter.go` pushes the signed envelope to RPC |
| Telemetry emission (OTLP) | Sends trace spans to configured collector |
| Crash reporting (Sentry) | Sends panic reports to configured endpoint |
| IPFS / Arweave publish (`--publish-ipfs`, `--publish-arweave`) | Uploads signed data to decentralised storage |

### 3. The air-gapped signing pipeline

`internal/offline/envelope.go` implements a three-stage pipeline for
environments where the signing machine has no network access:

```
Stage 1 (online machine):   Fetch transaction → produce EnvelopeFile
Stage 2 (air-gapped signer): Load EnvelopeFile → sign → produce SignedEnvelopeFile
Stage 3 (online machine):   Load SignedEnvelopeFile → submit to RPC
```

**EnvelopeFile structure:**
- Payload (canonical JSON blob)
- SHA-256 checksum of the payload (computed before transfer to air-gapped machine)
- Metadata (timestamp, network, transaction hash)

**Signing stage controls:**
- The signer computes its own SHA-256 of the received payload and compares it
  to the embedded checksum before signing. A mismatch is a hard error; the
  operation is aborted.
- The signer uses the local software or PKCS#11 provider; no network call is
  made.
- The output `SignedEnvelopeFile` carries the signature, the public key, and
  the original payload checksum.

**Submission stage:**
- `internal/offline/submitter.go` reads the `SignedEnvelopeFile`, re-verifies
  the signature against the embedded public key, and submits via RPC.
- Re-verification before submission ensures the file was not tampered with
  during the transfer from the air-gapped machine back to the online machine.

### 4. Snapshot replay guarantees

The snapshot store (`~/.glassbox/cache/snapshots/`) is a content-addressed
filesystem store (SHA-256 keyed, atomic writes). Replay from a stored snapshot
does not contact the network:

- The CLI host loads the `PersistedSnapshot` from disk.
- The embedded fingerprint (SHA-256 of sorted ledger entry key-value pairs) is
  verified on load. A mismatch is logged as a `DRIFT WARNING`; the snapshot is
  still returned to the caller so the operator can inspect it, but the warning
  signals that the stored content may have been tampered with or corrupted.
- The snapshot is passed to the simulator subprocess as the ledger state for
  the replay run.
- No RPC calls are made during a pure replay; the simulator operates entirely
  on the in-memory ledger state provided by the host.

**What the snapshot does NOT guarantee:**
- The snapshot reflects the on-chain state at the time of capture, not the
  current on-chain state. A replay is therefore a historical re-execution, not
  a proof of current correctness.
- A fingerprint mismatch is a warning, not a hard rejection. A corrupt or
  tampered snapshot will still be replayed with the corrupted state; the
  `DRIFT WARNING` is the only signal to the operator.
- The `network`, `tx_hash`, and `saved_at` metadata fields are stored but are
  not included in the content hash (see ADR-004 and `docs/snapshot-deduplication.md`).
  A snapshot cannot prove which transaction it came from; provenance depends on
  the operator's capture workflow.

### 5. Offline verification guarantee

`glassbox audit:verify` re-derives the payload hash from the stored `trace`
field using the same canonicalisation algorithm used at signing time (see
ADR-005) and verifies the Ed25519 signature using the embedded or out-of-band
public key. No network access, no Glassbox service, and no external state are
required. The four-step verification procedure is:

1. Reconstruct canonical bytes from stored `trace` (and `hardware_attestation`
   if present).
2. `hash = SHA-256(canonical_bytes)`.
3. Assert `hash == stored trace_hash`.
4. Assert `Ed25519.Verify(public_key, hash_bytes, signature)`.

This guarantee is unconditional: a verifier that has only the log file and the
public key can always verify, regardless of network availability.

### 6. RPC failover and degradation behaviour

When the primary RPC endpoint is unavailable, the CLI applies the configured
`failover_strategy`:

| Strategy | Behaviour |
|---|---|
| `round-robin` | Cycle through `soroban_rpc_urls` list in order |
| `first-available` | Try each URL in order, use the first that responds |
| (none configured) | Single URL; error immediately on failure |

Failover applies to live transaction fetching and RPC submission only. It does
not affect offline-capable operations.

**Degradation is never silent.** If all RPC endpoints are unreachable, the CLI
exits with an explicit error. The operator is not left with a partial result
that silently omits network-fetched data.

### 7. KMS provider and offline signing

The AWS KMS provider is **not compatible with air-gapped signing**. Every
signing call requires `kms:Sign` over HTTPS to the AWS KMS API. Operators who
need air-gapped signing must use the software or PKCS#11 provider. Attempting
to use the KMS provider without network access will produce a clear error from
the AWS SDK.

### 8. Telemetry and crash reporting offline behaviour

If the OTLP collector or Sentry endpoint is unreachable:
- Telemetry spans are dropped silently (OTLP export failures do not abort the
  CLI command).
- Crash reports are attempted once; a failed send is silently swallowed (the
  panic is still re-raised and the CLI exits non-zero regardless).

These are best-effort transmissions; offline operation does not degrade the
core CLI functionality.

## Rationale

### Why is the air-gapped pipeline a three-stage design rather than a single offline binary?

A single binary that signs and submits would need to be deployed to the
air-gapped machine, which may itself be a compliance violation. The three-stage
design allows the signing binary to be audited and deployed to the air-gapped
machine independently; the submission stage runs on a network-connected machine
that never holds the signing key.

### Why is the payload checksum verified again on the air-gapped machine?

A transfer medium (USB drive, QR code, encrypted email) between the online fetch
stage and the air-gapped signing stage is not trusted. The checksum verification
ensures the air-gapped signer is signing exactly the payload that was prepared
by the online stage, not a modified version.

### Why is the snapshot content hash omitted from provenance metadata?

Transaction hash, network, and timestamp are excluded from the content hash
(used for deduplication) because the same logical snapshot (identical ledger
state) from two different transactions or networks should deduplicate to a single
file. Provenance is carried in the `metadata` fields, which are not covered by
the content hash. Operators who need cryptographic provenance of a snapshot's
origin should use the signed audit log, not the snapshot metadata.

### Why does snapshot replay not contact the network even when a network is available?

Network access during replay would introduce non-determinism: re-running the
same simulation at a later time could produce a different result if on-chain
state has changed. Strict offline replay ensures that two runs with the same
snapshot always produce the same simulation output.

### Alternatives considered

**Encrypt the EnvelopeFile for the air-gapped transfer:** Would protect the
payload in transit. The current design relies on the transfer medium's security
(e.g. an encrypted USB drive) rather than adding application-layer encryption.
Application-layer encryption of the envelope is a candidate future enhancement.

**Offline KMS via AWS CloudHSM custom key store:** Would allow KMS-style API
with a local HSM, eliminating the network requirement. This is a valid
deployment pattern and is outside Glassbox's control; the PKCS#11 provider
already supports any PKCS#11-compliant HSM including CloudHSM.

**Cache RPC responses for offline replay automatically:** Would capture ledger
state transparently during live debug sessions. The current design requires
explicit snapshot capture. Automatic caching is a candidate for a future `--auto-snapshot` flag.

## Implementation

| Claim | Verified in |
|---|---|
| Air-gapped envelope: checksum verified before signing | `internal/offline/envelope.go` |
| Air-gapped envelope: re-verified before RPC submission | `internal/offline/submitter.go` |
| Software + PKCS#11 providers require no network | `internal/signer/inmemory.go`, `internal/signer/provider_pkcs11.go` |
| KMS provider requires network for every sign call | `internal/signer/kms.go`, `docs/audit-kms-signing.md` |
| Snapshot replay uses local store, no RPC | `internal/snapshot/`, `docs/snapshot-deduplication.md` |
| Snapshot fingerprint: mismatch logged as `DRIFT WARNING` (load proceeds) | `docs/snapshot-deduplication.md` |
| `audit:verify` requires no network | `docs/audit-verify-command.md` |
| Telemetry / crash report failures are silent | `cmd/glassbox/main.go` |
| RPC failover strategy (round-robin / first-available) | `glassbox.example.toml` |

## Consequences

**Positive:**
- The air-gapped signing pipeline allows security-sensitive organisations to
  keep signing keys on hardware with no network exposure while still producing
  signed audit logs that are submittable and verifiable.
- Offline verification means a signed log remains auditable indefinitely,
  independent of Glassbox service availability or version.

**Negative / trade-offs:**
- Air-gapped signing requires manual coordination of three stages; operators
  must establish and document the transfer workflow themselves.
- Snapshot replay captures point-in-time ledger state; a simulation that passes
  on a stale snapshot may fail against current on-chain state. Operators must
  track when snapshots were taken relative to the contract upgrade lifecycle.
- The KMS provider cannot participate in air-gapped workflows; organisations
  that standardise on KMS must maintain a fallback PKCS#11 token for air-gapped
  environments.

**Migration impact:**
- Operators who currently run `glassbox audit:sign` on connected machines and
  want to migrate to air-gapped signing must adopt the envelope file workflow
  (`internal/offline/envelope.go`) and adjust their CI/CD pipeline accordingly.
- Existing snapshot files in the flat storage format (pre-deduplication) are
  compatible with replay; the dedup index is rebuilt automatically on first load.

## Related

- [ADR-003: Trust Boundaries and Component Trust Levels](003-trust-boundaries.md)
- [ADR-004: Data Classification and Cross-Boundary Data Flows](004-data-classification.md)
- [ADR-005: Canonicalization Ownership](005-canonicalization-ownership.md)
- [ADR-006: Provider Isolation](006-provider-isolation.md)
- [Audit Signing](../audit-signing.md)
- [AWS KMS Signing](../audit-kms-signing.md)
- [Audit Verify Command](../audit-verify-command.md)
- [Snapshot Deduplication](../snapshot-deduplication.md)
- [Sandboxed Replay](../sandboxed-replay.md)
