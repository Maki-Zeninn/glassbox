// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package trace

// schema_version.go — Formal execution trace schema versioning.
// Issue #538: Version the execution trace schema formally
//
// Defines schema versions, required fields, additive-change rules,
// and migration behavior for stored traces.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// TraceSchemaVersion identifies the formal trace schema version.
// Traces exported with this version follow the compatibility policy
// documented in docs/schema/trace-schema.md.
const TraceSchemaVersion = "2.0.0"

// TraceSchemaMinorVersion is the minor version component for additive changes.
// Minor version increments add optional fields without breaking consumers.
// Major version increments indicate breaking changes requiring migration.
const (
	TraceSchemaMajor = 2
	TraceSchemaMinor = 0
	TraceSchemaPatch = 0
)

// TraceSchemaField describes a required or optional field in the trace schema.
type TraceSchemaField struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Description string `json:"description"`
	Since       string `json:"since"` // schema version when introduced
}

// TraceSchemaDefinition describes the complete trace schema for a version.
type TraceSchemaDefinition struct {
	Version      string              `json:"version"`
	Description  string              `json:"description"`
	RequiredFields []TraceSchemaField `json:"required_fields"`
	OptionalFields []TraceSchemaField `json:"optional_fields"`
	AdditiveOnly  bool                `json:"additive_only"` // true = only additive changes in minor versions
}

// CurrentTraceSchema defines the fields and rules for the current schema version.
var CurrentTraceSchema = TraceSchemaDefinition{
	Version:     TraceSchemaVersion,
	Description: "Glassbox Execution Trace Schema — formal version for Go, TypeScript, and fixture compatibility",
	AdditiveOnly: true,
	RequiredFields: []TraceSchemaField{
		{Name: "transaction_hash", Type: "string", Required: true, Description: "SHA-256 fingerprinted transaction hash", Since: "1.0.0"},
		{Name: "start_time", Type: "string (RFC 3339)", Required: true, Description: "Trace start timestamp", Since: "1.0.0"},
		{Name: "end_time", Type: "string (RFC 3339)", Required: true, Description: "Trace end timestamp", Since: "1.0.0"},
		{Name: "states", Type: "array<ExecutionState>", Required: true, Description: "Ordered list of execution states", Since: "1.0.0"},
		{Name: "schema_version", Type: "string (semver)", Required: true, Description: "Schema version this trace conforms to", Since: "1.0.0"},
	},
	OptionalFields: []TraceSchemaField{
		{Name: "snapshots", Type: "array<StateSnapshot>", Required: false, Description: "State snapshots for efficient reconstruction", Since: "1.0.0"},
		{Name: "diagnostic_events", Type: "array<DiagnosticEvent>", Required: false, Description: "Raw diagnostic events from simulator", Since: "1.0.0"},
		{Name: "decoded_events", Type: "array<ContractEvent>", Required: false, Description: "Decoded contract events", Since: "1.0.0"},
		{Name: "annotations", Type: "object", Required: false, Description: "User-defined annotations", Since: "1.0.0"},
		{Name: "current_step", Type: "integer", Required: false, Description: "Current navigation step", Since: "1.0.0"},
		{Name: "snapshot_interval", Type: "integer", Required: false, Description: "Interval between snapshots", Since: "1.0.0"},
		{Name: "host_calls", Type: "array<HostCallRecord>", Required: false, Description: "Host function call records", Since: "2.0.0"},
		{Name: "resource_limits", Type: "object", Required: false, Description: "Simulator resource limit summary", Since: "2.0.0"},
		{Name: "trap_cause", Type: "object", Required: false, Description: "Structured trap cause", Since: "2.0.0"},
	},
}

// SupportedTraceSchemaVersions lists all schema versions that can be loaded.
var SupportedTraceSchemaVersions = []string{"1.0", "1.0.0", "2.0.0"}

// IsTraceSchemaVersionSupported returns true if the version is loadable.
func IsTraceSchemaVersionSupported(v string) bool {
	for _, s := range SupportedTraceSchemaVersions {
		if s == v {
			return true
		}
	}
	return false
}

// ValidateTraceSchema checks that a trace conforms to the declared schema version.
// Returns an error with remediation hints if required fields are missing.
func ValidateTraceSchema(data map[string]interface{}) error {
	version, ok := data["schema_version"].(string)
	if !ok {
		return fmt.Errorf("trace schema validation failed: missing 'schema_version' field. " +
			"Add \"schema_version\": \"%s\" to the trace envelope. " +
			"See docs/schema/trace-schema.md for details.", TraceSchemaVersion)
	}

	if !IsTraceSchemaVersionSupported(version) {
		return fmt.Errorf("unsupported trace schema version %q. Supported versions: %v. "+
			"Use a migration adapter to convert to %s.",
			version, SupportedTraceSchemaVersions, TraceSchemaVersion)
	}

	// Check required fields
	for _, field := range CurrentTraceSchema.RequiredFields {
		if _, exists := data[field.Name]; !exists {
			// For wrapped traces, check the "trace" sub-object
			if traceObj, ok := data["trace"].(map[string]interface{}); ok {
				if _, exists := traceObj[field.Name]; exists {
					continue
				}
			}
			return fmt.Errorf("trace schema validation failed: missing required field %q (%s). "+
				"Field introduced in schema %s. See docs/schema/trace-schema.md.",
				field.Name, field.Description, field.Since)
		}
	}

	return nil
}

// MigrationAdapter converts a trace from an older schema version to the current one.
type MigrationAdapter func(map[string]interface{}) (map[string]interface{}, error)

// traceSchemaMigrations maps old version → migration adapter.
var traceSchemaMigrations = map[string]MigrationAdapter{
	"1.0":   migrateV1toV2,
	"1.0.0": migrateV1toV2,
}

// MigrateTrace attempts to migrate a trace envelope to the current schema version.
// If the trace is already at the current version, it is returned unchanged.
// If no migration path exists, an error is returned with remediation guidance.
func MigrateTrace(data map[string]interface{}) (map[string]interface{}, error) {
	version, _ := data["schema_version"].(string)
	if version == TraceSchemaVersion {
		return data, nil
	}

	adapter, ok := traceSchemaMigrations[version]
	if !ok {
		return nil, fmt.Errorf("no migration path from schema version %q to %q. "+
			"Supported source versions: %v. "+
			"Re-export the trace from Glassbox or manually add \"schema_version\": \"%s\".",
			version, TraceSchemaVersion, supportedMigrationVersions(), TraceSchemaVersion)
	}

	return adapter(data)
}

func supportedMigrationVersions() []string {
	keys := make([]string, 0, len(traceSchemaMigrations))
	for k := range traceSchemaMigrations {
		keys = append(keys, k)
	}
	return keys
}

// migrateV1toV2 migrates a trace from schema 1.0/1.0.0 to 2.0.0.
// V2 adds optional fields (host_calls, resource_limits, trap_cause) which
// are simply left absent in migrated traces.
func migrateV1toV2(data map[string]interface{}) (map[string]interface{}, error) {
	data["schema_version"] = TraceSchemaVersion

	// For ExportJSON envelope format, update the schema_version field
	// and ensure trace sub-object has required migration fields
	if traceObj, ok := data["trace"].(map[string]interface{}); ok {
		// Ensure snapshot_interval exists (it was optional in V1 but is now expected)
		if _, exists := traceObj["snapshot_interval"]; !exists {
			traceObj["snapshot_interval"] = 100
		}
		// V2 new fields are left absent (forward compatibility)
		// host_calls, resource_limits, trap_cause are not added
	}

	return data, nil
}

// SaveSchemaDefinition writes the current schema definition to a JSON file.
func SaveSchemaDefinition(dir string) error {
	path := filepath.Join(dir, "trace-schema-definition.json")
	data, err := json.MarshalIndent(CurrentTraceSchema, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal schema: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// GetSchemaDefinition returns the current schema definition.
func GetSchemaDefinition() *TraceSchemaDefinition {
	return &CurrentTraceSchema
}
