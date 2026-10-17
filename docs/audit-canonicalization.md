# Audit Log Canonicalization

This document describes the serialization guarantees that make Glassbox audit
logs suitable for cryptographic verification across runtimes, platforms, and
time.

## Quick reference

Each rule below links to the conformance test that pins it. Rules marked
**Go ≠ TS** behave differently in the two implementations; avoid those inputs
in payloads that must verify across both.

| Rule | Behavior | Go test (`internal/cmd/canonical_test.go`) | TS test (`tests/audit-canonical.test.ts`) |
|------|----------|--------------------------------------------|-------------------------------------------|
| Key ordering | Keys sorted at every nesting level (Go `sort.Strings`, byte order) | `TestCanonicalJSON_KeyOrdering`, `TestCanonicalJSON_ComplexNesting` | `sorts keys lexicographically`, `nested objects have sorted keys at every level` |
| Arrays | Insertion order kept, never sorted | `TestCanonicalJSON_Arrays` | `arrays preserve insertion order` |
| Whitespace | None: no spaces after `:` / `,`, no newlines | `TestCanonicalJSON_KeyOrdering` | `different key insertion order produces the same canonical string` |
| Numbers | Shortest round-trip decimal, e.g. `42.5`, `2`, `1e+21`. Go decodes every number to `float64` first, so integers above 2^53 lose precision | `TestCanonicalJSON_DataTypes` (`number`) | — |
| Control-character escaping | `\n`, `\t`, `\"` use short escapes | `TestCanonicalJSON_DataTypes` (`string with special characters`) | — |
| HTML characters (**Go ≠ TS**) | Go writes `<`, `>`, `&` as `<`, `>`, `&`; TS writes them literally | — (not asserted) | — (not asserted) |
| Non-ASCII | Written as raw UTF-8, not `\u` escaped | — | `unicode strings are preserved byte-for-byte` |
| Empty values | `{}`, `[]`, `""` serialized as-is | `TestCanonicalJSON_EmptyValues` | `empty collections serialize deterministically` |
| Unsupported values | TS: `NaN`, `Infinity` and `-Infinity` rejected by schema validation before hashing. Go: `json.Marshal` returns an error for NaN/Inf, functions and channels | — | `rejects NaN in input`, `rejects Infinity in state`, `rejects -Infinity in nested value` |
| Determinism | Same input gives the same bytes and hash on every call | `TestCanonicalJSON_Deterministic`, `TestGenerate_DeterministicHash` | `produces identical bytes across 100 invocations`, `produces identical SHA-256 hashes across 100 invocations` |

### Deterministic example

Input:

```json
{ "timestamp": "2026-01-01T00:00:00.000Z", "state": { "y": 9, "x": 8 }, "input": { "b": 2, "note": "line\nbreak", "a": 1.5 }, "events": ["E1"] }
```

Canonical bytes (both implementations):

```
{"events":["E1"],"input":{"a":1.5,"b":2,"note":"line\nbreak"},"state":{"x":8,"y":9},"timestamp":"2026-01-01T00:00:00.000Z"}
```

SHA-256 of the canonical bytes:

```
685fe712f3172d6df328dbd7034bf887f20c7d6523b1d722bc18dd0390b5ad43
```

To reproduce it:

```bash
printf '%s' '{"events":["E1"],"input":{"a":1.5,"b":2,"note":"line\nbreak"},"state":{"x":8,"y":9},"timestamp":"2026-01-01T00:00:00.000Z"}' | sha256sum
```

## Why canonicalization matters

A signed audit log is only trustworthy if the verifier can reproduce the exact
byte sequence that was signed. Without a canonical form, two logically identical
payloads — `{"a":1,"b":2}` and `{"b":2,"a":1}` — would produce different
hashes and different signatures, making cross-platform verification impossible.

## Canonical form

Glassbox uses **RFC 8785 (JCS) inspired** canonical JSON with the following
rules, implemented via `fast-json-stable-stringify` (TypeScript) and a custom
recursive encoder (Go):

| Rule | Detail |
|------|--------|
| Key ordering | Object keys sorted lexicographically (Unicode code point order) at every nesting level |
| Whitespace | No extra whitespace — no spaces after `:` or `,`, no newlines |
| String encoding | UTF-8; no locale-specific collation; escape sequences follow JSON spec |
| Numbers | IEEE 754 double precision; `NaN` and `Infinity` are rejected before serialization |
| Arrays | Insertion order preserved (arrays are not sorted) |
| Null | Serialized as `null` |
| Booleans | Serialized as `true` / `false` |

### Example

Input (any key order):
```json
{ "timestamp": "2026-01-01T00:00:00.000Z", "state": { "y": 9, "x": 8 }, "input": { "b": 2, "a": 1 }, "events": ["E1"] }
```

Canonical form:
```json
{"events":["E1"],"input":{"a":1,"b":2},"state":{"x":8,"y":9},"timestamp":"2026-01-01T00:00:00.000Z"}
```

## Hashing

The canonical JSON string is hashed with **SHA-256**. The hash is computed over
the UTF-8 bytes of the canonical string.

When hardware attestation is present, the attestation object is included in the
hash input to prevent it from being stripped after signing:

```
hash = SHA-256( canonical_json({ trace, hardware_attestation }) )   // with attestation
hash = SHA-256( canonical_json({ trace }) )                          // without attestation
```

## Schema validation

Before any signing operation, the payload is validated against the strict
`AuditPayload` schema (TypeScript: `src/audit/AuditPayloadSchema.ts`):

| Field | Type | Required | Constraint |
|-------|------|----------|------------|
| `timestamp` | string | yes | Non-empty, valid ISO 8601 date-time |
| `input` | object | yes | Plain object (not array, not null) |
| `state` | object | yes | Plain object (not array, not null) |
| `events` | array | yes | Any array |
| `metadata` | object | no | Plain object when present |

Additional constraints enforced at all nesting levels:
- No `NaN` values (cannot be represented in JSON)
- No `Infinity` or `-Infinity` values
- No circular references

Validation errors are surfaced as a single thrown `Error` with all violations
listed, so the caller sees the full picture in one shot rather than one error
at a time.

## Implementation

### TypeScript (`src/audit/AuditLogger.ts`)

```typescript
import stringify from 'fast-json-stable-stringify';
import { assertValidAuditPayload } from './AuditPayloadSchema';

// 1. Validate schema
assertValidAuditPayload(trace);

// 2. Canonical serialization
const canonicalString = stringify({ trace, hardware_attestation });

// 3. Hash
const hash = createHash('sha256').update(canonicalString).digest('hex');

// 4. Sign the hash bytes
const signature = await signer.sign(Buffer.from(hash));
```

### Go (`internal/cmd/audit.go` + `internal/cmd/canonical.go`)

```go
// marshalCanonical: marshal → unmarshal to interface{} → recursive sort + encode
payloadBytes, _ := marshalCanonical(payload)
hash := sha256.Sum256(payloadBytes)
signature, _ := signer.Sign(hash[:])
```

Both implementations produce byte-identical SHA-256 hashes for the same logical
payload, enabling cross-language verification.

## Verification

To verify a signed audit log:

1. Reconstruct the canonical JSON string from the stored `trace` (and
   `hardware_attestation` if present) using the same rules above.
2. Compute `SHA-256` of the canonical string.
3. Compare the computed hash to the stored `hash` field.
4. Verify the Ed25519 signature over the hash bytes using the stored `publicKey`.

All four steps must pass for the log to be considered valid.

## Test coverage

| Test file | What is verified |
|-----------|-----------------|
| `tests/audit-canonical.test.ts` | Key ordering, byte-identical output across 100 invocations, hash stability, schema validation (valid + 15 invalid cases), AuditLogger enforcement |
| `internal/cmd/canonical_test.go` | Go canonical encoder key ordering, arrays, data types, struct marshaling, determinism across 10 invocations |
| `internal/cmd/canonical_test.go::TestGenerate_DeterministicHash` | End-to-end: same payload → same `TraceHash` across 20 Generate calls |
| `internal/cmd/audit_test.go` | Tamper detection for payload, attestation removal, attestation modification |

## Stability guarantees

- The canonical form is **stable across Node.js LTS versions** because
  `fast-json-stable-stringify` does not rely on `JSON.stringify` key ordering
  (which is insertion-order in V8 but not guaranteed by the spec).
- The canonical form is **stable across Go versions** because the custom
  recursive encoder uses `sort.Strings` on map keys, which is deterministic.
- The canonical form is **stable across operating systems** because no
  locale-sensitive operations are used.
- Adding new **optional** fields to the schema is backward compatible: old
  verifiers that do not know about the new field will fail to verify logs that
  include it (by design — the hash covers all fields).
