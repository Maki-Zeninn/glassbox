# Runtime Binding Validation

Glassbox generated bindings rely on compile-time TypeScript types for
correctness at build time, but JSON payloads received from external callers,
CI pipelines, or older Glassbox versions can carry malformed data that bypasses
compile-time checks entirely.

Runtime validators fill that gap by checking the four external payload types
**before** they reach execution logic, with field-path diagnostics that
identify exactly which nested field failed and why.

---

## Validated payload types

| Type | Validator (TypeScript) | Validator (Go) |
|---|---|---|
| Command inputs | `validateCommandOptions` | schema-derived checks in `internal/bindings/schema.go` |
| Trace payloads | `validateTracePayload` | `bindings.ValidateTracePayload` |
| Audit records | `validateAuditRecord` | `bindings.ValidateAuditRecord` |
| Session envelopes | `validateSessionEnvelope` | `bindings.ValidateSessionEnvelope` |

---

## TypeScript usage

All validators live in `src/bindings/binding-runtime-validators.ts` and share
the same `RuntimeValidationResult` return type.

### Strict mode (default)

Unknown fields are rejected.  Use this for pre-execution validation of
caller-supplied JSON.

```typescript
import {
  validateTracePayload,
  validateAuditRecord,
  validateSessionEnvelope,
  parseAndValidateAuditRecord,
} from './src/bindings/binding-runtime-validators';

const result = validateTracePayload(payload);
if (!result.valid) {
  for (const err of result.errors) {
    // err.path  → "trace.input.amount"
    // err.code  → "REQUIRED_FIELD_MISSING" | "WRONG_TYPE" | ...
    // err.message → human-readable explanation
    console.error(err.path, err.code, err.message);
  }
}
```

### Permissive mode

Unknown additive fields are silently ignored.  Use this when consuming JSON
from a newer Glassbox version that may have added fields not yet in this
client's schema.

```typescript
const result = validateAuditRecord(payload, 'permissive');
```

### Parse-and-validate helpers

The `parseAndValidate*` helpers parse a raw JSON string and validate in one
step, returning both the parsed object and the validation result:

```typescript
const { raw, result } = parseAndValidateAuditRecord(jsonString);
if (!result.valid) {
  // handle errors before using raw
}
```

### Error codes

Error codes are stable and aligned one-to-one with Go codes in
`internal/bindings/runtime_validator.go`:

| Code | Meaning |
|---|---|
| `REQUIRED_FIELD_MISSING` | A required field is absent |
| `WRONG_TYPE` | The value's runtime type does not match the schema |
| `INVALID_ENUM_VALUE` | A string is outside the allowed enum set |
| `INVALID_VALUE` | Syntactically valid but semantically invalid (e.g. NaN, empty timestamp) |
| `UNKNOWN_FIELD` | An unrecognised field in strict mode |
| `MUTUAL_EXCLUSION_VIOLATED` | Mutually exclusive fields are both present |

---

## Go usage

```go
import "github.com/dotandev/glassbox/internal/bindings"

// Deserialise and validate in one step.
raw, result := bindings.UnmarshalAndValidateAuditRecord(jsonBytes, bindings.Strict)
if !result.Valid {
    fmt.Println(bindings.FormatValidationErrors(result.Errors))
    // Output:
    //   [REQUIRED_FIELD_MISSING] audit.hash — required field missing
    //   [INVALID_VALUE] audit.trace.timestamp — not a valid RFC 3339 timestamp
}

// Validate a pre-decoded map.
result = bindings.ValidateTracePayload(rawMap, bindings.Permissive)
```

---

## Field-path format

Paths use dot notation with bracket notation for arrays:

```
trace.input.amount          # nested object field
trace.events[2]             # array element
audit.signer.provider       # nested required field
session.trace.state.ledger  # deeply nested
```

---

## Strict vs permissive mode

| Behaviour | Strict | Permissive |
|---|---|---|
| Required fields absent | Error | Error |
| Wrong type | Error | Error |
| Invalid enum value | Error | Error |
| NaN / Infinity in object | Error | Error |
| Unknown additive field | Error | Silently ignored |

The additive-field rule follows Glassbox's documented minor-version policy:
adding a new optional field to an output schema is backward compatible and
must not break permissive-mode consumers.

---

## Integration with command validators

Command input options are validated by `validateCommandOptions` from
`src/bindings/command-validators.ts`, which is generated from the canonical
schema.  The runtime validators in this file complement that by covering the
JSON payload types that flow through bindings at runtime rather than at the
CLI flag layer.

See [docs/binding-validation.md](binding-validation.md) for ABI binding
staleness detection, and [src/bindings/README.md](../src/bindings/README.md)
for the command-schema TypeScript artifacts.
