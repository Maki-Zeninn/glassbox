# Glassbox Execution Trace Schema

## Version: 2.0.0

This document defines the formal schema for Glassbox execution traces.
All exported traces declare a schema version and must conform to the
requirements below.

## Compatibility Policy

- **Major version** (e.g., 2.x.x → 3.x.x): Breaking changes. Migration required.
  Fields may be removed or have their types changed.
- **Minor version** (e.g., 2.0.x → 2.1.x): Additive changes only. New optional
  fields may be added; existing consumers remain compatible.
- **Patch version** (e.g., 2.0.0 → 2.0.1): Documentation and clarification
  changes. No structural changes.

## Required Fields

| Field | Type | Since | Description |
|-------|------|-------|-------------|
| `transaction_hash` | string (SHA-256) | 1.0.0 | Fingerprinted transaction hash |
| `start_time` | string (RFC 3339) | 1.0.0 | Trace start timestamp |
| `end_time` | string (RFC 3339) | 1.0.0 | Trace end timestamp |
| `states` | array\<ExecutionState\> | 1.0.0 | Ordered execution states |
| `schema_version` | string (semver) | 1.0.0 | Schema version this trace conforms to |

## Optional Fields

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

## Migration

| From | To | Strategy |
|------|----|----------|
| 1.0 / 1.0.0 | 2.0.0 | Additive: new optional fields left absent. `schema_version` updated. |

## Validation

Traces are validated via `trace.ValidateTraceSchema()` which checks:
- Presence of `schema_version` field
- Version is in `SupportedTraceSchemaVersions`
- All required fields are present

Invalid traces fail with remediation hints pointing to this document.
