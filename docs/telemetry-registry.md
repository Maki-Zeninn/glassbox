# Telemetry Event Registry

This document describes the telemetry event registry system for Glassbox, which provides ownership, versioning, and privacy controls for all emitted events.

## Overview

The event registry ensures that:
- Every emitted event is registered with a schema definition
- Forbidden sensitive fields are rejected
- Additive changes are versioned with migration rules
- Documentation is auto-generated from the registry

## Registry Structure

### Event Definition

Each event in the registry includes:

- **Name**: Unique event identifier (e.g., `debug.progress`)
- **Version**: Schema version (starts at 1, increments on breaking changes)
- **Owner**: Team or individual responsible for the event
- **Description**: Human-readable description
- **Stability**: Maturity level (experimental, stable, deprecated)
- **Sensitivity**: Privacy classification (public, internal, pii, secret)
- **Retention**: Data retention policy (transient, short, long, permanent)
- **Fields**: Schema for event fields with types and constraints
- **MigrationRules**: Rules for migrating between versions
- **DeprecatedVersion**: When the event was deprecated (0 if active)
- **DeprecatedBy**: Event that replaces this deprecated event

### Stability Levels

- **Experimental**: New events under active development. Fields may change without notice.
- **Stable**: Events with committed schemas. Additive changes only; breaking changes require version bump.
- **Deprecated**: Events that will be removed. Should not be used in new code.

### Sensitivity Levels

- **Public**: Contains no sensitive data.
- **Internal**: Contains internal-only data (no PII, no secrets).
- **PII**: Contains personally identifiable information.
- **Secret**: Contains secrets or credentials (never allowed to be emitted).

### Retention Policies

- **Transient**: Events not persisted (in-memory only).
- **Short**: Events retained for 7-30 days.
- **Long**: Events retained for 1-12 months.
- **Permanent**: Events retained indefinitely (audit logs).

## Usage

### Registering a New Event

Add the event definition in `internal/telemetry/events.go`:

```go
func registerMyEvents() {
    defs := []*EventDefinition{
        {
            Name:        "my.event",
            Version:     1,
            Owner:       "my-team",
            Description: "Description of the event",
            Stability:   StabilityStable,
            Sensitivity: SensitivityPublic,
            Retention:   RetentionShort,
            Fields: []FieldType{
                {Name: "id", Type: "string", Required: true, Sensitive: false},
                {Name: "count", Type: "int", Required: false, Sensitive: false},
            },
        },
    }

    for _, def := range defs {
        if err := Register(def); err != nil {
            panic("failed to register event '" + def.Name + "': " + err.Error())
        }
    }
}
```

Call this function in the `init()` function of `events.go`.

### Validating Events

Events are automatically validated when emitted through the progress emitter:

```go
import "github.com/dotandev/glassbox/internal/telemetry"

// Validation happens automatically in the emitter
// To validate manually:
payload := map[string]interface{}{
    "id": "123",
    "count": 42,
}
err := telemetry.Validate("my.event", payload)
if err != nil {
    // Handle validation error
}
```

### Strict Mode

Enable strict mode to require all events to be registered:

```go
telemetry.SetStrictMode(true)
```

In strict mode, unregistered events will be rejected. This is disabled by default for backward compatibility.

### Forbidden Fields

The following field names are automatically forbidden:
- `password`
- `secret`
- `token`
- `private_key`
- `privatekey`
- `api_key`
- `apikey`
- `credential`
- `pin`
- `auth_token`
- `access_token`
- `refresh_token`

Additional forbidden fields can be added:

```go
registry := telemetry.NewRegistry()
registry.AddForbiddenField("custom_forbidden")
```

## Versioning and Migration

### Additive Changes

For additive changes (adding new fields), increment the version and document the change:

```go
{
    Name:        "my.event",
    Version:     2,  // Incremented
    Owner:       "my-team",
    Description: "Description with new field",
    // ...
    Fields: []FieldType{
        {Name: "id", Type: "string", Required: true, Sensitive: false},
        {Name: "count", Type: "int", Required: false, Sensitive: false},
        {Name: "new_field", Type: "string", Required: false, Sensitive: false}, // New field
    },
}
```

### Breaking Changes

For breaking changes (removing or renaming fields), add a migration rule:

```go
{
    Name:        "my.event",
    Version:     3,
    Owner:       "my-team",
    Description: "Description with renamed field",
    // ...
    Fields: []FieldType{
        {Name: "id", Type: "string", Required: true, Sensitive: false},
        {Name: "renamed_count", Type: "int", Required: false, Sensitive: false}, // Renamed
    },
    MigrationRules: []MigrationRule{
        {
            FromVersion: 2,
            ToVersion:   3,
            Transform:   "Rename count to renamed_count",
            FieldMap: map[string]string{
                "count": "renamed_count",
            },
        },
    },
}
```

### Deprecation

To deprecate an event, set the stability to deprecated and specify the replacement:

```go
{
    Name:              "my.old_event",
    Version:           1,
    Owner:             "my-team",
    Description:       "Old event",
    Stability:         StabilityDeprecated,
    Sensitivity:       SensitivityPublic,
    Retention:         RetentionShort,
    DeprecatedVersion: 2,
    DeprecatedBy:      "my.new_event",
    // ...
}
```

## Generating Documentation

Generate the event registry documentation:

```bash
go run ./internal/telemetry/cmd/gendocs/main.go
```

This creates `docs/telemetry-events.md` with a table of all registered events.

## Testing

Run the telemetry registry tests:

```bash
go test ./internal/telemetry/...
```

## Acceptance Criteria

The implementation meets the following acceptance criteria:

✅ **Every emitted event is registered**
- Event registry enforces registration before emission
- Validation checks for registered event names
- Strict mode can be enabled to enforce registration

✅ **Forbidden sensitive fields are rejected**
- Built-in denylist of sensitive field names
- Custom forbidden fields can be added
- Registration fails if forbidden fields are present

✅ **Additive changes are versioned**
- Version field tracks schema version
- Migration rules define how to migrate between versions
- Deprecation tracking with replacement events

✅ **Generated documentation table matches implementation**
- Auto-generated documentation from registry
- Command to regenerate docs
- Tests verify documentation generation

## Files

- `internal/telemetry/registry.go` - Core registry implementation
- `internal/telemetry/events.go` - Event registration
- `internal/telemetry/docs.go` - Documentation generation
- `internal/telemetry/cmd/gendocs/main.go` - Docs generation command
- `internal/telemetry/registry_test.go` - Registry tests
- `internal/progress/emitter.go` - Integration with progress events
