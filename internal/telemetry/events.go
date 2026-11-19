// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package telemetry

// init registers all event definitions.
func init() {
	// Register debug.progress events
	registerDebugProgressEvents()
}

// registerDebugProgressEvents registers the structured progress events for the debug command.
func registerDebugProgressEvents() {
	defs := []*EventDefinition{
		{
			Name:        EventDebugProgress,
			Version:     1,
			Owner:       "debug-team",
			Description: "Structured progress events for long-running debug operations",
			Stability:   StabilityStable,
			Sensitivity: SensitivityPublic,
			Retention:   RetentionTransient,
			Fields: []FieldType{
				{Name: "operation_id", Type: "string", Required: true, Sensitive: false},
				{Name: "phase", Type: "string", Required: true, Sensitive: false, Enum: []string{"init", "fetch", "simulate", "analyze", "export", "done"}},
				{Name: "status", Type: "string", Required: true, Sensitive: false, Enum: []string{"start", "complete", "error", "skipped"}},
				{Name: "timestamp", Type: "string", Required: true, Sensitive: false},
				{Name: "message", Type: "string", Required: false, Sensitive: false},
				{Name: "error_code", Type: "string", Required: false, Sensitive: false},
				{Name: "meta", Type: "object", Required: false, Sensitive: false},
			},
		},
	}

	for _, def := range defs {
		if err := Register(def); err != nil {
			panic("failed to register event '" + def.Name + "': " + err.Error())
		}
	}
}
