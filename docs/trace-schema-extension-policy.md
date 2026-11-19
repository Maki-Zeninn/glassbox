# Trace Schema and Extension Policy

This guide documents the stable fields that external trace consumers can rely
on, how to add new event kinds without breaking existing viewers, the extension
namespace convention, unknown-field behaviour, and versioning requirements.

## Stable fields

External consumers should parse **only** the fields listed below. These fields
are guaranteed to be present in every conforming trace and will not change
semantically within a major version.

### Top-level trace

| Field | Type | Stable since | Notes |
|-------|------|-------------|-------|
| `transaction_hash` | string (SHA-256 hex) | 1.0.0 | Unique transaction identifier |
| `start_time` | string (RFC 3339) | 1.0.0 | Trace start timestamp |
| `end_time` | string (RFC 3339) | 1.0.0 | Trace end timestamp |
| `states` | array\<ExecutionState\> | 1.0.0 | Ordered execution states |
| `schema_version` | string (semver) | 1.0.0 | Schema version this trace conforms to |

### ExecutionState

| Field | Type | Stable since | Notes |
|-------|------|-------------|-------|
| `step` | integer | 1.0.0 | Zero-based step index |
| `status` | string enum | 1.0.0 | One of: `success`, `failure`, `skipped` |
| `contract_id` | string | 1.0.0 | Contract identifier (hex or C… address) |
| `function_name` | string | 1.0.0 | Soroban function invoked |
| `source_location` | object \| null | 1.0.0 | Resolved source mapping (see below) |

### SourceLocation

| Field | Type | Stable since | Notes |
|-------|------|-------------|-------|
| `file` | string | 1.0.0 | Relative path to source file |
| `line` | integer | 1.0.0 | 1-based line number |
| `column` | integer | 1.0.0 | 1-based column number (0 when unavailable) |
| `function` | string | 1.0.0 | Demangled function name |

### DiagnosticEvent

| Field | Type | Stable since | Notes |
|-------|------|-------------|-------|
| `event_type` | string enum | 1.0.0 | One of: `contract`, `system`, `error` |
| `topics` | array\<string\> | 1.0.0 | Event topics (may be empty) |
| `data` | string (base64) | 1.0.0 | Encoded event payload |
| `in_successful_contract_call` | boolean | 1.0.0 | Whether event occurred in a successful call |

## Optional stable fields

These fields are **present when available** and will not be removed within a
major version, but are not required:

| Field | Type | Since | Description |
|-------|------|-------|-------------|
| `snapshots` | array\<StateSnapshot\> | 1.0.0 | State snapshots for reconstruction |
| `decoded_events` | array\<ContractEvent\> | 1.0.0 | Decoded contract events |
| `annotations` | object | 1.0.0 | User-defined annotations |
| `host_calls` | array\<HostCallRecord\> | 2.0.0 | Host function call records |
| `resource_limits` | object | 2.0.0 | Simulator resource limit summary |
| `trap_cause` | object | 2.0.0 | Structured trap cause |

## Unknown-field policy

Consumers **must** tolerate unknown fields. New fields are added without
removing or renaming existing ones. When parsing:

1. Read only the fields you need.
2. Ignore any field not in your schema.
3. Never fail on the presence of an unknown field.
4. Log unknown fields at debug level for forward-compatibility testing.

```javascript
// Correct: pick only what you need
const { transaction_hash, states, schema_version } = trace;

// Incorrect: reject on unknown fields
const { transaction_hash, states, schema_version, unknown_field } = trace;
// This pattern is fine; destructure ignores unknown fields.
```

```python
# Correct: use .get() with defaults
tx_hash = trace.get("transaction_hash")

# Incorrect: direct key access that raises on missing
tx_hash = trace["transaction_hash"]  # Only for required fields
```

## Extension namespaces

New event kinds and diagnostic data should use the `x_` prefix to indicate
extension fields that are not part of the stable schema:

| Namespace | Purpose | Example |
|-----------|---------|---------|
| `x_glassbox` | Glassbox-specific extensions | `x_glassbox.source_mapping` |
| `x_<vendor>` | Third-party tool extensions | `x_custom_tool.gas_breakdown` |

### Extension field rules

1. **Prefix with `x_`** — all extension fields must start with `x_` followed by
   a vendor or tool identifier.
2. **Nested under a parent** — prefer adding extensions as sub-objects rather
   than top-level fields:
   ```json
   {
     "states": [...],
     "x_glassbox": {
       "gas_optimization": { "suggestions": [...] }
     }
   }
   ```
3. **Never collide** — check existing `x_` prefixes before adding a new vendor
   namespace.
4. **Document** — add a comment or README entry describing the extension field's
   purpose and schema.
5. **Optional** — extension fields are always optional; core consumers must not
   require them.

## Adding new event kinds

When you need a new event kind (e.g. `memory_usage`, `io_trace`):

### 1. Determine version impact

| Change | Version bump | Migration |
|--------|-------------|-----------|
| New optional field in `diagnostic_events` | Minor | None — consumers ignore unknown fields |
| New `event_type` enum value | Minor | Consumers must handle unknown enum values gracefully |
| New required field in `ExecutionState` | Major | Migration guide required |
| Changed field type | Major | Breaking — new major version |

### 2. Add the field as optional

```json
{
  "event_type": "memory_usage",
  "topics": ["heap", "alloc"],
  "data": "base64-encoded-payload",
  "in_successful_contract_call": true,
  "x_glassbox": {
    "heap_bytes": 1024,
    "alloc_count": 42
  }
}
```

### 3. Update the schema

1. Add the new field to `docs/schema/trace-schema.md` with `Since` version.
2. If using JSON Schema, update the relevant `.schema.json` file.
3. Add the field to the `Optional Fields` or `Event Types` table.

### 4. Add a golden test

Include a test fixture with the new field:

```json
{
  "transaction_hash": "5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab",
  "start_time": "2026-01-15T10:30:00Z",
  "end_time": "2026-01-15T10:30:01Z",
  "states": [],
  "schema_version": "2.1.0",
  "x_glassbox": {
    "memory_usage": {
      "heap_bytes": 1024,
      "alloc_count": 42
    }
  }
}
```

### 5. Document the change

Update `CHANGES_QUICK_REFERENCE.md` with:

```
- Added `x_glassbox.memory_usage` extension field to trace schema v2.1.0
```

## JSON examples

### Minimal valid trace

```json
{
  "transaction_hash": "5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab",
  "start_time": "2026-01-15T10:30:00Z",
  "end_time": "2026-01-15T10:30:01Z",
  "states": [
    {
      "step": 0,
      "status": "success",
      "contract_id": "CABCDEF1234567890abcdef1234567890abcdef1234567890abcdef12345678",
      "function_name": "transfer",
      "source_location": {
        "file": "src/lib.rs",
        "line": 42,
        "column": 5,
        "function": "process_transfer"
      }
    }
  ],
  "schema_version": "2.0.0"
}
```

### Trace with extensions

```json
{
  "transaction_hash": "5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab",
  "start_time": "2026-01-15T10:30:00Z",
  "end_time": "2026-01-15T10:30:01Z",
  "states": [
    {
      "step": 0,
      "status": "success",
      "contract_id": "CABCDEF1234567890abcdef1234567890abcdef1234567890abcdef12345678",
      "function_name": "transfer",
      "source_location": {
        "file": "src/lib.rs",
        "line": 42,
        "column": 5,
        "function": "process_transfer"
      }
    }
  ],
  "schema_version": "2.0.0",
  "diagnostic_events": [
    {
      "event_type": "contract",
      "topics": ["transfer"],
      "data": "AAAAAQ==",
      "in_successful_contract_call": true
    }
  ],
  "x_glassbox": {
    "gas_optimization": {
      "suggestions": [
        {
          "step": 0,
          "message": "Consider using storage reads instead of auth checks",
          "estimated_savings": 1500
        }
      ]
    }
  }
}
```

### Error trace with trap cause

```json
{
  "transaction_hash": "5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab",
  "start_time": "2026-01-15T10:30:00Z",
  "end_time": "2026-01-15T10:30:00.500Z",
  "states": [
    {
      "step": 0,
      "status": "failure",
      "contract_id": "CABCDEF1234567890abcdef1234567890abcdef1234567890abcdef12345678",
      "function_name": "initialize",
      "source_location": {
        "file": "src/lib.rs",
        "line": 15,
        "column": 3,
        "function": "initialize"
      }
    }
  ],
  "schema_version": "2.0.0",
  "trap_cause": {
    "kind": "ContractError",
    "message": "already initialized",
    "code": 12
  }
}
```

## Compatibility examples

### Safe: adding an optional field (minor version)

```json
// v2.0.0 — consumer parses this
{ "states": [...], "schema_version": "2.0.0" }

// v2.1.0 — same consumer ignores the new field
{ "states": [...], "schema_version": "2.1.0", "host_calls": [...] }
```

### Safe: new event_type value (minor version)

```json
// Consumer handles unknown event_types by logging and skipping
{
  "event_type": "gas_breakdown",
  "topics": [],
  "data": "...",
  "in_successful_contract_call": true
}
```

### Breaking: removing a required field (major version)

```json
// v2.0.0 — consumer expects transaction_hash
{ "transaction_hash": "abc...", "states": [...] }

// v3.0.0 — transaction_hash removed; consumer breaks
{ "states": [...] }
```

### Breaking: changing a field type (major version)

```json
// v2.0.0 — step is integer
{ "step": 42 }

// v3.0.0 — step is now a string (breaking)
{ "step": "step-42" }
```

## Schema change review checklist

Before submitting a PR that modifies the trace schema:

- [ ] **Version impact assessed** — minor for additive, major for breaking
- [ ] **New fields are optional** — no existing required field changed
- [ ] **`x_` prefix used** for extension fields
- [ ] **Golden test added** — test fixture includes the new field
- [ ] **Documentation updated** — `trace-schema.md` and `CHANGES_QUICK_REFERENCE.md`
- [ ] **JSON Schema updated** — `.schema.json` file matches the markdown spec
- [ ] **Backward compatibility verified** — old consumers can parse new traces
- [ ] **Forward compatibility verified** — new consumers can parse old traces
- [ ] **Enum values added, never removed** — new `event_type` values are additive
- [ ] **Field descriptions are clear** — no ambiguous or overlapping semantics
- [ ] **Migration guide updated** — for major version bumps only
- [ ] **CI passes** — `node docs/schema/validate-schemas.js` succeeds

## See also

- [Trace schema](./schema/trace-schema.md) — formal schema definition
- [Schema registry](./schema/README.md) — JSON Schema files and versioning
- [Trace export validation](./trace-export-validation.md) — export checks
- [Trace export annotations](./trace-export-annotations.md) — annotation schema
- [Stable error codes](./stable-error-codes.md) — error code policy
