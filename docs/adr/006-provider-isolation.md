# ADR-006: Provider Isolation

## Status

Accepted

## Context

Glassbox supports three signing provider backends — software Ed25519, PKCS#11
(HSM), and AWS KMS — through a common `Signer` interface. Each backend has a
fundamentally different trust posture: the software provider holds raw key
material in process memory; the PKCS#11 provider loads a vendor-supplied shared
library into the host process; the KMS provider transmits signing requests over
the network to AWS infrastructure.

Without explicit isolation boundaries between the provider and the rest of the
CLI host, a misconfigured or malicious PKCS#11 module could, in principle, read
process memory (including the session state, snapshot cache, or even another
provider's key material if both are loaded). Conversely, without clearly defined
interfaces and lifecycle controls, an operator cannot reason about what key
material is present in memory at any given point or whether it has been
correctly zeroed on teardown.

This ADR records what isolation each provider tier provides, what it does not
provide, how providers are selected and composed, and what the consequences are
for multi-provider configurations.

## Decision

### 1. The `Signer` interface is the only crossing point

All provider backends implement `internal/signer/signer.go`:

```go
// Signer is the only interface the CLI host uses to perform a signing operation.
type Signer interface {
    // Sign accepts raw bytes and returns the digital signature.
    // By convention the CLI layer passes the 32-byte SHA-256 digest of
    // the canonical payload (hash[:]) — the interface itself does not
    // enforce this; the constraint lives in internal/cmd/audit.go.
    Sign(data []byte) ([]byte, error)

    // PublicKey returns the raw public key bytes associated with the
    // signing key held by this provider.
    PublicKey() ([]byte, error)

    // Algorithm returns the signing algorithm name (e.g. "ed25519").
    Algorithm() string
}
```

The host never casts a `Signer` to a concrete type after construction. The
signing path passes only:
- the 32-byte SHA-256 digest (as `data []byte`) — never the raw canonical payload
- and receives a 64-byte signature and 32-byte public key in return

This is the minimal interface needed to sign and verify; no key material, PIN,
or credential is reachable through it.

### 2. Provider selection and lifecycle

Providers are selected **once at startup** by `internal/signer/factory.go`,
registered in `internal/signer/registry.go`, and not changed for the duration
of the process.

Selection precedence as implemented in `factory.go` (first match wins):

| Priority | Config path |
|---|---|
| 1 | `GLASSBOX_SIGNER_TYPE` environment variable — values: `software`, `pkcs11` |
| 2 | Default: `software` (when env var is absent or empty) |

The higher-level `cmd` layer reads `audit.signing_provider` from the TOML config
and translates it to the correct `GLASSBOX_SIGNER_TYPE` / `ProviderConfig` before
calling the factory. AWS KMS is wired through the registry (`internal/signer/registry.go`)
and the `cmd` layer directly; it is not handled by `NewFromEnv()` in `factory.go`.

Once the factory constructs a provider, the raw configuration values (PIN,
key bytes, AWS credentials) are consumed and not stored as accessible fields on
the returned `Signer`. The lifecycle is:

```
startup → factory / registry → Signer interface → use → process exit
```

There is no runtime provider switching. A session that starts with the PKCS#11
provider uses the PKCS#11 provider for every signing operation in that session.

### 3. Software provider isolation

**Component:** `internal/signer/inmemory.go`, `internal/signer/provider_software.go`

The software provider holds the raw 64-byte Ed25519 key (seed + public key) in
Go process heap memory.

Isolation properties:
- Key material is visible to any goroutine in the host process via the Go
  garbage collector scan; there is no memory pinning or explicit zeroing on
  teardown in the current implementation.
- Key material does NOT cross the simulator or plugin subprocess boundary
  (env is stripped; the key is not passed as an IPC payload field).
- Key material is NOT written to the snapshot store, session DB, or telemetry.
- Input validation is performed at construction time: PEM format, key type
  (must be Ed25519 PKCS#8), and key length are all checked before the key is
  accepted.

**Not provided:**
- Memory-locked (mlock) pages to prevent key material from being swapped to disk.
- Explicit zeroing of key bytes on `Signer` teardown.
- Protection against a local attacker with read access to the process's
  `/proc/<pid>/mem`.

These properties are noted as limitations, not bugs; they are consistent with
the current threat model (single-user workstation, not a multi-tenant server).

### 4. PKCS#11 provider isolation

**Component:** `internal/signer/provider_pkcs11.go`

The PKCS#11 provider loads a vendor-supplied shared library (`.so` / `.dylib` /
`.dll`) into the host process via cgo/dlopen. The private key **never leaves the
HSM token** — the host sends only the digest to the module for signing via
`C_SignInit` / `C_Sign`, and receives only the signature bytes back.

Isolation properties:
- The PKCS#11 module executes in the same process address space as the CLI host.
  A compromised or malicious PKCS#11 module has unrestricted access to process
  memory.
- Module file existence and type (`.so`/`.dylib`/`.dll`, not a directory) are
  validated before `dlopen`.
- A 10-second timeout is enforced on module initialisation; a hung module is
  killed and the CLI exits with an error.
- PIN authentication is performed once per session; the raw PIN string is held
  in memory only during the `C_Login` call.
- The `--validate-only` preflight (`glassbox audit:sign --validate-only
  --signing-provider pkcs11`) runs the full module/slot/token/session/PIN/key/
  test-sign sequence without committing to a real signing operation.

**Not provided:**
- Address Space Layout Randomisation (ASLR) specific to the loaded module; this
  is an OS responsibility.
- Verification that the PKCS#11 module is the expected binary (hash pinning of
  the `.so` itself). Operators are responsible for verifying module provenance.
- Protection against a malicious PKCS#11 module reading other in-process
  secrets (RPC tokens, software key material if both providers are configured).

**Consequence of the same-process model:** operators who require that the
PKCS#11 module cannot read in-process memory should run Glassbox in an
environment where only the PKCS#11 provider is configured (no software key, no
RPC tokens in environment variables).

### 5. AWS KMS provider isolation

**Component:** `internal/signer/kms.go`

The KMS provider performs signing in AWS infrastructure. The private key never
leaves AWS KMS; the host only transmits the 32-byte SHA-256 digest to the KMS
`Sign` API and receives the 64-byte signature.

Isolation properties:
- No key material is present in the host process; the host holds only IAM
  credentials (environment variables or instance profile).
- IAM credentials are consumed by the AWS SDK v2 credential chain (explicit →
  profile → environment → EC2 IMDS → ECS task); they are not stored on the
  `Signer` struct after construction.
- The `kms:GetPublicKey` API is called once at construction to obtain the
  verifiable public key; subsequent signing operations require only `kms:Sign`.
- All KMS traffic is HTTPS; the AWS SDK validates the TLS certificate against
  the AWS trust store.

**Minimum required IAM permissions:**
```json
{
  "Action": ["kms:Sign", "kms:GetPublicKey", "kms:DescribeKey"],
  "Resource": "arn:aws:kms:REGION:ACCOUNT_ID:key/KEY_ID"
}
```

**Not provided:**
- Mutual TLS between the CLI host and the KMS API endpoint; standard AWS SDK
  TLS is used.
- Auditability of what the KMS service signed on the AWS side; AWS CloudTrail
  provides this at the AWS account level, not within Glassbox.
- Offline operation; the KMS provider requires network access for every signing
  call (see ADR-007).

### 6. Mock / test provider

A `mock` signer type (`GLASSBOX_SIGNER_TYPE=mock` or `glassbox.example.toml`)
is available for CI and local testing. It generates a fresh ephemeral Ed25519
key on each invocation and performs all signing in memory.

**The mock provider must not be used in production.** It provides no persistence
of the signing key; signatures produced by one invocation cannot be verified by
a subsequent invocation using the same configuration. The config documentation
clearly labels it as a test-only option.

### 7. Multiple providers are not simultaneously loaded

The factory constructs exactly one `Signer` per process. There is no
configuration path that loads both a PKCS#11 module and holds a software key
simultaneously in the same process (because selection is first-match and the
factory does not merge providers). This constraint limits the blast radius if
one provider backend is compromised: it cannot observe the key material of
another provider because that material is never in the same process.

### 8. Provider-specific input validation

Each provider validates its configuration at construction time and fails fast
with an actionable error before any signing work begins. The validation
sequence for PKCS#11 is: module path → file type → module load → slot
enumeration → token info → session open → PIN auth → key lookup → test sign.
A failure at any step produces a `[FAIL]` diagnostic and the process exits
non-zero.

## Rationale

### Why is the `Signer` interface restricted to `Sign(data []byte)` rather than `Sign(payload)`?

Passing the full payload to the provider would require every provider to
implement canonicalisation — creating the risk of divergence (see ADR-005).
Passing only the SHA-256 digest (as the `data` argument, by convention in the
CLI layer) also limits what data a potentially compromised PKCS#11 module can
observe: it sees 32 bytes of a SHA-256 hash, not the plaintext payload. The
interface itself accepts `[]byte` without enforcement; the restriction is upheld
by `internal/cmd/audit.go` always passing `hash[:]`.

### Why is the PKCS#11 module loaded in-process rather than a subprocess?

An out-of-process PKCS#11 bridge (similar to the simulator subprocess model)
would prevent the module from reading host memory, but would require a stable
IPC protocol between the bridge and the CLI, and would add latency per signing
operation. For a CLI tool where signing happens once per audit session, the
latency trade-off is acceptable but the implementation complexity is not.
Operators who require stronger isolation should use the AWS KMS provider, which
performs signing entirely outside the host process.

### Why is there no provider fallback chain?

A fallback chain (try PKCS#11, fall back to software) would silently degrade
security if the HSM becomes unavailable. Operators who require hardware-only
signing would have no reliable way to detect when the fallback was triggered.
Strict first-match single-provider semantics make this detectable (the process
exits with an error if the configured provider is unavailable).

### Alternatives considered

**Out-of-process PKCS#11 bridge:** Stronger isolation; rejected for complexity
and latency reasons (see above). A future ADR may revisit this.

**Key material in a Go `runtime.Pinner`-pinned allocation with explicit zero on
teardown:** Would prevent GC scan exposure and swapping. Deferred; requires
careful lifecycle management and is a low-priority hardening for single-user
workstations.

**Provider hot-swap without restart:** Would allow rotating keys without a
process restart. Rejected; the single-provider model is simpler to audit.

## Implementation

| Claim | Verified in |
|---|---|
| `Signer` interface definition (3 methods: `Sign`, `PublicKey`, `Algorithm`) | `internal/signer/signer.go` |
| Provider selected via `GLASSBOX_SIGNER_TYPE`; AWS KMS wired through registry/cmd | `internal/signer/factory.go`, `internal/signer/registry.go` |
| Provider registered in registry | `internal/signer/registry.go` |
| Software provider: key validated at construction | `internal/signer/provider_software.go` |
| PKCS#11: module path/type validated before load | `internal/signer/provider_pkcs11.go`, `docs/audit-signing.md` |
| PKCS#11: 10s module initialisation timeout | `internal/signer/provider_pkcs11.go` |
| PKCS#11: `--validate-only` preflight | `docs/audit-signing.md` |
| KMS: private key never leaves AWS | `internal/signer/kms.go`, `docs/audit-kms-signing.md` |
| KMS: `kms:GetPublicKey` called once at construction | `internal/signer/kms.go` |
| Mock provider documented as test-only | `glassbox.example.toml` |
| Only SHA-256 digest (not payload) passed as `data` to `Signer.Sign` | `internal/cmd/audit.go` |

## Consequences

**Positive:**
- The `Signer` interface allows operators to select a provider that matches
  their compliance requirements (software for development, PKCS#11 for
  on-premises HSM, KMS for cloud-native key management) without any change
  to the CLI signing path.
- Single-provider-per-process semantics mean a PKCS#11 module cannot observe
  software key material (and vice versa) because they are never co-loaded.

**Negative / trade-offs:**
- PKCS#11 module runs in-process; a malicious or vulnerable `.so` can read host
  memory. Operators must verify module provenance independently.
- The software provider does not mlock or zero key bytes; a swap file or core
  dump on a workstation could expose the key. Operators with strict key
  protection requirements should use PKCS#11 or KMS.
- The KMS provider requires network access; air-gapped signing is only available
  via the software or PKCS#11 providers.

**Migration impact:**
- Operators currently using `GLASSBOX_SIGNER_TYPE=mock` in production must
  migrate to a persistent signing provider before key continuity matters (i.e.,
  before distributing signed audit logs that must be verifiable in future
  sessions).
- Operators migrating from PKCS#11 to KMS (or vice versa) must re-sign any
  audit logs that were signed with the previous key, or distribute both public
  keys to verifiers.

## Related

- [ADR-003: Trust Boundaries and Component Trust Levels](003-trust-boundaries.md)
- [ADR-005: Canonicalization Ownership](005-canonicalization-ownership.md)
- [ADR-007: Offline Guarantees](007-offline-guarantees.md)
- [ADR-002: HSM Integration](002-hsm-integration.md)
- [Audit Signing](../audit-signing.md)
- [AWS KMS Signing](../audit-kms-signing.md)
