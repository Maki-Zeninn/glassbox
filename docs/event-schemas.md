# Event Schemas

## Contract Event Schemas

Glassbox can decode contract event payloads in audit logs and trace output when
you provide event schemas.

Use JSON schema files:

```json
{
  "events": [
    {
      "name": "transfer",
      "fields": [
        {"name": "from", "type": "address"},
        {"name": "to", "type": "address"},
        {"name": "amount", "type": "i128"}
      ]
    }
  ]
}
```

ABI-style event entries are also accepted:

```json
[
  {
    "type": "event",
    "name": "mint",
    "inputs": [
      {"name": "admin", "type": "address"},
      {"name": "amount", "type": "i128"}
    ]
  }
]
```

Trace output can load schemas with:

```sh
glassbox trace --print --event-schema events.json trace.json
```

When schemas are loaded, trace JSON written by Glassbox includes decoded events
under `decoded_events`.

Audit generation can attach decoded events by setting
`GenerateOptions.EventSchemas`. Decoded events are written to
`payload.decoded_events` and are covered by the audit hash and signature.

## Execution Trace Schema

Glassbox execution traces follow a formal schema versioning system to ensure
compatibility across versions. The trace schema defines the structure of
serialized traces used by reports, comparison, profiling, sessions, and the
visualizer.

### Schema Versioning

The trace schema uses semantic versioning (MAJOR.MINOR.PATCH):

- **Major version** (e.g., 2.x.x → 3.x.x): Breaking changes. Migration required.
  Fields may be removed or have their types changed.
- **Minor version** (e.g., 2.0.x → 2.1.x): Additive changes only. New optional
  fields may be added; existing consumers remain compatible.
- **Patch version** (e.g., 2.0.0 → 2.0.1): Documentation and clarification
  changes. No structural changes.

Current schema version: **2.0.0**

### Export Format Versions

Glassbox uses two related versioning systems:

1. **Trace Schema Version** (`schema_version`): The structure of the trace data itself
2. **Export Format Version** (`version`): The envelope format for exported files

The export format version is defined in `internal/trace/export_compatibility.go` and
currently at version 1.0.0.

### Required Fields

All traces must include these fields:

| Field | Type | Since | Description |
|-------|------|-------|-------------|
| `transaction_hash` | string (SHA-256) | 1.0.0 | Fingerprinted transaction hash |
| `start_time` | string (RFC 3339) | 1.0.0 | Trace start timestamp |
| `end_time` | string (RFC 3339) | 1.0.0 | Trace end timestamp |
| `states` | array\<ExecutionState\> | 1.0.0 | Ordered execution states |
| `schema_version` | string (semver) | 1.0.0 | Schema version this trace conforms to |

### Optional Fields

| Field | Type | Since | Description |
|-------|------|-------|-------------|
| `snapshots` | array\<StateSnapshot\> | 1.0.0 | State snapshots for reconstruction |
| `diagnostic_events` | array\<DiagnosticEvent\> | 1.0.0 | Raw simulator events |
| `decoded_events` | array\<ContractEvent\> | 1.0.0 | Decoded contract events |
| `annotations` | object | 1.0.0 | User-defined annotations |
| `current_step` | integer | 1.0.0 | Current navigation step |
| `snapshot_interval` | integer | 1.0.0 | Interval between snapshots |
| `host_calls` | array\<HostCallRecord\> | 2.0.0 | Host function call records |
| `resource_limits` | object | 2.0.0 | Simulator resource limit summary |
| `trap_cause` | object | 2.0.0 | Structured trap cause |

### Compatibility Policy

Traces can be loaded if:

1. The `schema_version` field is present and in `SupportedTraceSchemaVersions`
2. All required fields are present
3. Unknown fields are preserved (forward compatibility)

Traces from older schema versions are automatically migrated to the current
version using deterministic migration adapters.

### Export Formats

Glassbox supports multiple export formats, each with version awareness:

- **JSON (versioned)**: Uses `ExportJSON` with `schema_version` and `generated_at` fields
- **JSON (legacy)**: Plain `ExecutionTrace` JSON via `SaveToFile`
- **HTML/Markdown/Text**: Presentation formats derived from the trace structure

### Migration

Current migration paths:

| From | To | Strategy |
|------|----|----------|
| 1.0 / 1.0.0 | 2.0.0 | Additive: new optional fields left absent. `schema_version` updated. |

### Validation

Traces are validated via `trace.ValidateTraceSchema()` which checks:
- Presence of `schema_version` field
- Version is in `SupportedTraceSchemaVersions`
- All required fields are present

Invalid traces fail with remediation hints pointing to the schema documentation.

### Loading Traces

Use `LoadExecutionTrace(path)` for a single entry-point that handles all envelope
shapes:

```go
trace, err := trace.LoadExecutionTrace("execution.json")
```

This function accepts:
- Versioned `ExportJSON` envelopes (with `schema_version`)
- Versioned `VersionedTrace` envelopes (with `version` object)
- Legacy plain `ExecutionTrace` JSON (no version info)

### Exporting Traces

Use version-aware export functions:

```go
// Export with schema version (recommended)
jsonData, err := trace.ExportJSON(trace.CurrentJSONSchemaVersion, time.Now())

// Legacy format (no version envelope)
err := trace.SaveToFile("execution.json")
```

For the CLI:

```sh
# Export with schema version
glassbox trace --output-json trace.json input.json

# Export in other formats
glassbox trace --export report.html --format html input.json
```

### Unknown Field Handling

When loading traces with unknown fields (from future versions):

- Unknown fields in the envelope are preserved but not parsed
- Unknown fields in the trace object are stored in a map for future use
- Export re-writes the trace in the current schema version

This allows forward compatibility: traces from future versions can be loaded
and exported without data loss, though unrecognized features won't be used.

### See Also

- [Trace Schema Documentation](schema/trace-schema.md) - Detailed schema specification
- [Trace Migration Guide](schema/trace-migration-guide.md) - Migration procedures
- [JSON Output Documentation](json-output.md) - JSON export format details
