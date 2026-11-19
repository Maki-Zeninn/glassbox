# Trace Schema Migration Guide

This guide covers migration between major trace schema versions in Glassbox.
Trace schemas define the structure of exported execution traces used for
debugging, comparison, and audit workflows.

## Version History

| Version | Released | Changes |
|---------|----------|---------|
| 1.0.0 | Initial | Base trace fields: `transaction_hash`, `start_time`, `end_time`, `states`, `schema_version` |
| 1.0 | Alias | Same as 1.0.0 (two-digit alias) |
| 2.0.0 (current) | Current | Added `host_calls`, `resource_limits`, `trap_cause` optional fields |

## Compatibility Policy

Glassbox follows [Semantic Versioning](https://semver.org/) for trace schemas:

- **Major version** (e.g., 2.x.x → 3.x.x): Breaking changes. Fields may be
  removed or have their types changed. Migration required.
- **Minor version** (e.g., 2.0.x → 2.1.x): Additive changes only. New optional
  fields may be added; existing consumers remain compatible.
- **Patch version** (e.g., 2.0.0 → 2.0.1): Documentation changes only.

The `AdditiveOnly` flag in `CurrentTraceSchema` enforces that minor version
bumps only add optional fields.

## Version Detection

Detect the schema version of a trace programmatically:

```go
import "github.com/dotandev/glassbox/internal/trace"

// Check if a version is loadable
supported := trace.IsTraceSchemaVersionSupported("1.0.0") // true

// Validate a trace envelope
err := trace.ValidateTraceSchema(traceData)
if err != nil {
    // Error includes remediation guidance
    fmt.Println(err)
}
```

Or via CLI:

```bash
glassbox trace:info --file trace.json
```

## v1.0.0 → v2.0.0: Host Calls and Resource Limits

**Breaking:** No — the migration is additive and automatic.

**What changed:**
Three new optional fields were added:

| Field | Type | Description |
|-------|------|-------------|
| `host_calls` | `array<HostCallRecord>` | Host function call records for debugging host interactions |
| `resource_limits` | `object` | Simulator resource limit summary (CPU, memory, events) |
| `trap_cause` | `object` | Structured trap cause with type, message, and source location |

**Migration behavior:**
`MigrateTrace()` in `internal/trace/schema_version.go:135` handles the migration:

1. Updates `schema_version` from `"1.0.0"` to `"2.0.0"`
2. Sets `snapshot_interval` to `100` if absent (inside trace sub-object)
3. The three new optional fields are left absent — consumers must handle their
   absence gracefully

**Automatic migration command:**

```bash
glassbox trace:migrate --input trace-v1.json --output trace-v2.json
```

**Programmatic migration:**

```go
import "github.com/dotandev/glassbox/internal/trace"

migrated, err := trace.MigrateTrace(traceData)
if err != nil {
    log.Fatal(err)
}
// migrated["schema_version"] == "2.0.0"
```

**Rollback:** Traces at v2.0.0 can be downgraded to v1.0.0 by removing the
three new optional fields and setting `schema_version` back to `"1.0.0"`. This
is lossless since the new fields are optional.

```bash
glassbox trace:migrate --input trace-v2.json --output trace-v1.json --target-version 1.0.0
```

**Compatibility timeline:**
- v1.0.0 traces are supported indefinitely unless a future major version
  raises `MinSupportedTraceSchemaVersion`.
- Consumers (TypeScript, Go, Rust) must tolerate absent optional fields.

## v2.0.0 → Future Versions

When a future breaking change is introduced (v3.0.0), the migration will follow
the same pattern:

1. A new `migrateV2toV3()` function will be added to `traceSchemaMigrations`
2. `SupportedTraceSchemaVersions` will be updated
3. This guide will be updated with rollback and compatibility timelines

## Cross-Format Compatibility

Traces exported in different formats (JSON, HTML, Markdown) all carry the same
`schema_version` field. Migration applies to the data regardless of format.

| Format | Migration support |
|--------|-------------------|
| JSON | Full — data can be migrated programmatically |
| HTML | Embedded JSON is migrated; visual layout may differ |
| Markdown | Human-readable only — manual update of version header |

## Troubleshooting

### "unsupported trace schema version"

The trace version is not in `SupportedTraceSchemaVersions`. Migrate it:

```bash
glassbox trace:migrate --input trace.json --output trace-migrated.json
```

### "no migration path from schema version X to Y"

No automatic migration exists between these versions. Either:
1. Re-export the trace from Glassbox: `glassbox debug <tx-hash> --trace-output trace.json`
2. Manually add `"schema_version": "2.0.0"` if the trace structure is compatible

### "missing required field"

The trace is missing a field required by its declared schema version.
See `docs/schema/trace-schema.md` for the complete field list.

## Rollback Instructions

To roll back to a previous Glassbox version that only supports v1.0.0 traces:

1. Export traces before upgrading: `glassbox trace:migrate --input trace.json --output backup.json --target-version 1.0.0`
2. Downgrade Glassbox: `go install github.com/dotandev/glassbox/cmd/glassbox@<old-version>`
3. Use the v1.0.0 traces with the older binary
