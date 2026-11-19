# ADR-005: Canonicalization Ownership

## Status

Accepted

## Context

An Ed25519 signature over a JSON payload is only reproducible if the verifier
can reconstruct the exact same byte sequence that the signer hashed. JSON
serialisation is non-deterministic by default: key ordering varies across
runtimes, compilers, and standard library versions; number formatting may
differ; whitespace is unspecified.

Glassbox audit logs are produced by the Go CLI (`internal/cmd/audit.go`) and
may be verified by downstream tools including TypeScript (browser, CI), other
Go processes, and out-of-band scripts with only the public key and the signed
file. Cross-runtime reproducibility is therefore not an optional nicety — it is
a hard requirement for the signing guarantee to be meaningful.

The question this ADR answers is: **which component owns the canonical form
definition, who must implement it, and how is cross-language equivalence
guaranteed?**

## Decision

### 1. The CLI host process owns the canonical form

The Tier-0 CLI host (Go) is the sole authority on what constitutes the canonical
form of an audit payload. The TypeScript SDK implements the same algorithm
independently, but the Go implementation is the reference. Any discrepancy is
treated as a TypeScript bug.

Rationale for Go as reference: the audit log is created and signed by the Go
CLI. Verification of a log signed by the CLI must work with the same bytes the
CLI produced. Starting from the signer's output and working backwards is the
only definition that guarantees correctness.

### 2. Canonical form specification

The canonical form is **RFC 8785 (JCS) inspired** deterministic JSON with the
following invariants, implemented identically in Go and TypeScript:

| Rule | Detail | Rationale |
|---|---|---|
| Key ordering | Object keys sorted by Unicode code-point order (lexicographic), applied recursively at every nesting level | Deterministic across all JSON producers |
| Whitespace | Zero extra whitespace — no spaces after `:` or `,`, no newlines, no indentation | Minimal and unambiguous byte sequence |
| String encoding | UTF-8; escape sequences follow JSON spec; no locale-specific collation | Cross-platform stability |
| Numbers | IEEE 754 double precision; `NaN` and `Infinity` are rejected before serialisation | JSON does not support non-finite floats; rejection ensures no silent encoding variation |
| Arrays | Insertion order preserved; arrays are not sorted | Arrays carry semantic ordering |
| Null | Serialised as `null` | Standard JSON |
| Booleans | Serialised as `true` / `false` | Standard JSON |

**The canonical form is NOT full RFC 8785.** It is inspired by JCS but is not
a complete implementation. In particular, Unicode normalisation of string values
is not applied, and the number serialisation diverges from the RFC's ULP-exact
approach. The invariants above are the operative spec; the RFC is a reference
point, not an authority.

### 3. Hashing

```
canonical_bytes = canonical_json(payload)              // UTF-8 encoded
hash             = SHA-256(canonical_bytes)            // 32 bytes
```

When hardware attestation is present it is included in the hash input to prevent
strip-and-replace attacks:

```
canonical_bytes = canonical_json({ trace, hardware_attestation })
hash             = SHA-256(canonical_bytes)
```

The `trace_hash` field embedded in the signed audit log is the hex encoding of
this SHA-256 digest.

### 4. Go implementation

Located in `internal/cmd/canonical.go` and used by `internal/cmd/audit.go`.

Algorithm:
1. Marshal the Go struct to raw JSON (`encoding/json`).
2. Unmarshal into `interface{}` to erase struct field ordering imposed by
   Go's reflection-based encoder.
3. Recursively sort all `map[string]interface{}` keys with `sort.Strings`.
4. Re-encode using `encoding/json` without indentation.

```go
// internal/cmd/canonical.go (representative pseudocode)
func marshalCanonical(v interface{}) ([]byte, error) {
    raw, _ := json.Marshal(v)
    var generic interface{}
    json.Unmarshal(raw, &generic)
    sorted := sortMapKeys(generic)
    return json.Marshal(sorted)
}

// internal/cmd/audit.go
// Note: Signer.Sign(data []byte) accepts raw bytes; the CLI always passes
// the 32-byte hash slice — the interface does not enforce this constraint.
payloadBytes, _ := marshalCanonical(payload)
hash := sha256.Sum256(payloadBytes)
signature, _ := signer.Sign(hash[:])
```

### 5. TypeScript implementation

Located in `src/audit/AuditLogger.ts`. Uses `fast-json-stable-stringify` for
key-sorted serialisation, which does not rely on `JSON.stringify` key ordering
(insertion-order in V8, unspecified by spec) and is therefore stable across
Node.js LTS versions.

```typescript
// src/audit/AuditLogger.ts (representative pseudocode)
import stringify from 'fast-json-stable-stringify';
const canonicalString = stringify({ trace, hardware_attestation });
const hash = createHash('sha256').update(canonicalString).digest('hex');
const signature = await signer.sign(Buffer.from(hash));
```

### 6. Schema validation precedes canonicalisation

Before either implementation computes the canonical form, it validates the
payload against the `AuditPayload` schema:

| Field | Type | Required | Constraint |
|---|---|---|---|
| `timestamp` | string | yes | Non-empty, valid ISO 8601 |
| `input` | object | yes | Plain object (not array, not null) |
| `state` | object | yes | Plain object (not array, not null) |
| `events` | array | yes | Any array |
| `metadata` | object | no | Plain object when present |

Additional constraints at all nesting levels: no `NaN`, no `Infinity`, no
circular references. Validation precedes canonicalisation so that
malformed payloads cannot reach the signing path.

### 7. Stability guarantees

The canonical form is stable — meaning the same logical payload always produces
the same bytes — across:
- Go versions (uses `sort.Strings`, not reflection key ordering)
- Node.js LTS versions (`fast-json-stable-stringify` does not use
  `JSON.stringify` key order)
- Operating systems (no locale-sensitive operations)
- Time (timestamp is a field value, not used in sorting)

**Not stable when:**
- Optional fields are added to the schema. Adding a new optional field changes
  the canonical bytes and invalidates previously signed logs when the new field
  is present. Old verifiers that do not know about the new field will correctly
  reject logs that include it (by design — the hash covers all fields).

### 8. Verification procedure

A verifier that holds only the signed audit log file and the signer's public
key performs four steps, all of which must pass:

1. **Reconstruct canonical bytes:** apply the algorithm above to the stored
   `trace` field (and `hardware_attestation` if present).
2. **Compute hash:** `SHA-256(canonical_bytes)`.
3. **Compare to stored hash:** the computed digest must equal `trace_hash`.
4. **Verify signature:** Ed25519 verify(`signature`, `hash_bytes`, `public_key`)
   must pass. (`Signer.Sign(data []byte)` accepts the hash bytes as `data`;
   the interface does not name the argument `digest`, but by convention the
   CLI always passes the 32-byte SHA-256 hash.)

This is the complete verification; no network access or external state is
required.

### 9. Cross-language equivalence test

Both implementations must produce byte-identical SHA-256 hashes for the same
logical payload. This is verified by:

| Test | Location |
|---|---|
| Go canonical encoder: key ordering, arrays, types, struct marshaling, determinism across 10 invocations | `internal/cmd/canonical_test.go` |
| Go end-to-end: same payload → same `TraceHash` across 20 `Generate` calls | `internal/cmd/canonical_test.go::TestGenerate_DeterministicHash` |
| TypeScript: key ordering, byte-identical output across 100 invocations, hash stability, schema validation (15 invalid cases) | `tests/audit-canonical.test.ts` |
| TypeScript: tamper detection for payload, attestation removal, attestation modification | `internal/cmd/audit_test.go` |

Cross-language byte-identity is validated in the test suite via a shared
fixture file; any divergence is a test failure.

## Rationale

### Why Go as the reference rather than a shared library?

A shared Rust library via CGO would introduce a cgo build dependency that breaks
cross-compilation and complicates Windows support. A shared WASM module would
require a WASM runtime in the verifier. Independent identical implementations
with cross-language fixture tests provide the same correctness guarantee without
build complexity.

### Why not use a standard JCS library?

At the time of implementation, production-ready JCS libraries for Go were either
unmaintained or had known deviations from the RFC in number serialisation. The
custom recursive encoder is 30 lines, has full test coverage, and is
unambiguously specified by the invariant table above. If a well-maintained Go JCS
library matures, migration would be a drop-in replacement with the same tests.

### Why `fast-json-stable-stringify` rather than `JSON.stringify` with key sort?

`JSON.stringify` key ordering in V8 follows insertion order for string keys that
are not array indices; this is not guaranteed by the ECMAScript spec and has
differed between V8 versions for certain edge cases. `fast-json-stable-stringify`
sorts keys unconditionally and does not delegate to the engine's object property
enumeration order.

### Why reject `NaN` / `Infinity` rather than encoding them as `null`?

Silently replacing a non-finite value with `null` would change the meaning of
the payload. Rejection surfaces encoding bugs at the source and prevents a
signed log from containing a field whose value has been silently altered.

### Alternatives considered

**Full RFC 8785 implementation:** Would require ULP-exact number serialisation
(complex, rarely matters in practice for audit payloads) and Unicode
normalisation (introduces a dependency, rarely matters for ASCII-heavy payloads).
Rejected in favour of the simpler invariant set that covers all known Glassbox
payload types.

**CBOR canonical encoding (RFC 7049 section 3.9):** Binary; not human-readable;
would complicate manual verification. Rejected.

**Protocol Buffers with deterministic serialisation:** Requires a .proto schema
for every payload version; adds a code-generation step. Rejected; the JSON-based
approach allows ad-hoc payload fields without schema changes.

## Implementation

| Claim | Verified in |
|---|---|
| Go canonical encoder exists and uses recursive key sort | `internal/cmd/canonical.go` |
| Go implementation determinism across 20 invocations | `internal/cmd/canonical_test.go::TestGenerate_DeterministicHash` |
| TypeScript implementation uses `fast-json-stable-stringify` | `src/audit/AuditLogger.ts` |
| TypeScript determinism across 100 invocations | `tests/audit-canonical.test.ts` |
| Schema validation precedes canonicalisation | `src/audit/AuditPayloadSchema.ts`, `internal/cmd/audit.go` |
| `NaN`/`Infinity` rejected | `src/audit/AuditPayloadSchema.ts` |
| Hardware attestation included in hash when present | `docs/audit-canonicalization.md` |
| Four-step verification procedure | `docs/audit-verify-command.md` |

## Consequences

**Positive:**
- Any holder of the Ed25519 public key can independently verify a signed audit
  log without network access and without Glassbox installed, using only a
  standard SHA-256 and Ed25519 implementation.
- The canonical form is language-agnostic; a future Rust verifier, Python CI
  script, or Java audit tool can implement the same algorithm from the invariant
  table.

**Negative / trade-offs:**
- Adding optional fields to `AuditPayload` is a breaking change for logs that
  include those fields: existing verifiers will fail to verify them. Operators
  must update verifiers before deploying a version that adds optional fields.
- The non-standard NaN/Infinity rejection means payloads from runtimes that
  represent missing values as `NaN` (some Rust XDR decoders) must be
  post-processed before audit logging.

**Migration impact:**
- The canonical form has been stable since the initial implementation. Logs
  signed by earlier versions remain verifiable as long as the payload schema has
  not added new fields.
- If a future version adopts a different canonical form (e.g. full RFC 8785),
  a new `canonical_version` field should be added to the signed log envelope,
  and old logs should retain the old verifier path. This ADR should be
  superseded by the new one.

## Related

- [ADR-003: Trust Boundaries and Component Trust Levels](003-trust-boundaries.md)
- [ADR-004: Data Classification and Cross-Boundary Data Flows](004-data-classification.md)
- [ADR-006: Provider Isolation](006-provider-isolation.md)
- [Audit Canonicalization](../audit-canonicalization.md)
- [Audit Signing](../audit-signing.md)
- [Audit Verify Command](../audit-verify-command.md)
