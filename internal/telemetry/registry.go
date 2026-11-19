// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package telemetry provides an event registry for tracking, validating,
// and documenting all emitted events with ownership, versioning, and
// privacy controls.
package telemetry

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
)

// Event type constants
// These constants define the canonical event type identifiers for all events
// emitted by Glassbox. Using named constants instead of inline string literals
// makes the full event vocabulary visible in one place and prevents typos.
const (
	// EventDebugProgress is emitted during long-running debug operations to
	// report phase transitions and status updates.
	EventDebugProgress = "debug.progress"
)

// StabilityLevel indicates the maturity and stability of an event schema.
type StabilityLevel string

const (
	// StabilityExperimental is for new events under active development.
	// Fields may change without notice.
	StabilityExperimental StabilityLevel = "experimental"
	
	// StabilityStable is for events with committed schemas.
	// Additive changes only; breaking changes require version bump.
	StabilityStable StabilityLevel = "stable"
	
	// StabilityDeprecated is for events that will be removed.
	// Should not be used in new code.
	StabilityDeprecated StabilityLevel = "deprecated"
)

// SensitivityLevel indicates the privacy classification of event data.
type SensitivityLevel string

const (
	// SensitivityPublic contains no sensitive data.
	SensitivityPublic SensitivityLevel = "public"
	
	// SensitivityInternal contains internal-only data (no PII, no secrets).
	SensitivityInternal SensitivityLevel = "internal"
	
	// SensitivityPII contains personally identifiable information.
	SensitivityPII SensitivityLevel = "pii"
	
	// SensitivitySecret contains secrets or credentials (should never be emitted).
	SensitivitySecret SensitivityLevel = "secret"
)

// RetentionPolicy defines how long event data should be retained.
type RetentionPolicy string

const (
	// RetentionTransient is for events not persisted (in-memory only).
	RetentionTransient RetentionPolicy = "transient"
	
	// RetentionShort is for events retained for 7-30 days.
	RetentionShort RetentionPolicy = "short"
	
	// RetentionLong is for events retained for 1-12 months.
	RetentionLong RetentionPolicy = "long"
	
	// RetentionPermanent is for events retained indefinitely (audit logs).
	RetentionPermanent RetentionPolicy = "permanent"
)

// FieldType describes the type and constraints of an event field.
type FieldType struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`     // string, int, bool, array, object
	Required bool     `json:"required"`
	Sensitive bool    `json:"sensitive"` // If true, value must be redacted
	Enum     []string `json:"enum,omitempty"` // Valid values if enum
}

// MigrationRule defines how to migrate data between event versions.
type MigrationRule struct {
	FromVersion int      `json:"from_version"`
	ToVersion   int      `json:"to_version"`
	Transform   string   `json:"transform"` // Description of transformation
	FieldMap    map[string]string `json:"field_map,omitempty"` // old_field -> new_field
}

// EventDefinition is the registry entry for an event type.
type EventDefinition struct {
	// Name is the unique event identifier (e.g., "debug.progress").
	Name string `json:"name"`
	
	// Version is the schema version (starts at 1, increments on breaking changes).
	Version int `json:"version"`
	
	// Owner is the team or individual responsible for this event.
	Owner string `json:"owner"`
	
	// Description is a human-readable description of the event.
	Description string `json:"description"`
	
	// Stability indicates the maturity of the event schema.
	Stability StabilityLevel `json:"stability"`
	
	// Sensitivity indicates the privacy classification.
	Sensitivity SensitivityLevel `json:"sensitivity"`
	
	// Retention defines how long event data should be kept.
	Retention RetentionPolicy `json:"retention"`
	
	// Fields defines the schema for event fields.
	Fields []FieldType `json:"fields"`
	
	// MigrationRules defines how to migrate between versions.
	MigrationRules []MigrationRule `json:"migration_rules,omitempty"`
	
	// DeprecatedVersion indicates when this event was deprecated (0 if active).
	DeprecatedVersion int `json:"deprecated_version,omitempty"`
	
	// DeprecatedBy is the event that replaces this deprecated event.
	DeprecatedBy string `json:"deprecated_by,omitempty"`
}

// Registry maintains the collection of all registered event definitions.
type Registry struct {
	mu         sync.RWMutex
	definitions map[string]*EventDefinition // name -> definition
	// forbiddenFields contains field names that are never allowed in events.
	forbiddenFields map[string]bool
	// strictMode requires all events to be registered before emission.
	strictMode bool
}

// NewRegistry creates a new empty event registry.
func NewRegistry() *Registry {
	return &Registry{
		definitions: make(map[string]*EventDefinition),
		forbiddenFields: map[string]bool{
			"password": true,
			"secret": true,
			"token": true,
			"private_key": true,
			"privatekey": true,
			"api_key": true,
			"apikey": true,
			"credential": true,
			"pin": true,
			"auth_token": true,
			"access_token": true,
			"refresh_token": true,
		},
		strictMode: false, // Disabled by default for backward compatibility
	}
}

// SetStrictMode enables or disables strict mode.
// When enabled, all events must be registered before emission.
func (r *Registry) SetStrictMode(enabled bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.strictMode = enabled
}

// Register adds an event definition to the registry.
// Returns an error if the event name is already registered or if the definition is invalid.
func (r *Registry) Register(def *EventDefinition) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if def.Name == "" {
		return fmt.Errorf("event name cannot be empty")
	}
	if def.Version < 1 {
		return fmt.Errorf("event version must be >= 1")
	}
	if def.Owner == "" {
		return fmt.Errorf("event owner cannot be empty")
	}
	if def.Stability == "" {
		return fmt.Errorf("event stability level cannot be empty")
	}
	if def.Sensitivity == "" {
		return fmt.Errorf("event sensitivity level cannot be empty")
	}
	if def.Retention == "" {
		return fmt.Errorf("event retention policy cannot be empty")
	}

	// Check for forbidden field names
	for _, field := range def.Fields {
		if r.isForbiddenField(field.Name) {
			return fmt.Errorf("field '%s' is forbidden in event '%s'", field.Name, def.Name)
		}
	}

	// Check if already registered
	if existing, ok := r.definitions[def.Name]; ok {
		return fmt.Errorf("event '%s' is already registered (version %d) by %s", 
			def.Name, existing.Version, existing.Owner)
	}

	r.definitions[def.Name] = def
	return nil
}

// Get retrieves an event definition by name.
// Returns nil if the event is not registered.
func (r *Registry) Get(name string) *EventDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.definitions[name]
}

// List returns all registered event definitions.
func (r *Registry) List() []*EventDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()

	defs := make([]*EventDefinition, 0, len(r.definitions))
	for _, def := range r.definitions {
		defs = append(defs, def)
	}
	return defs
}

// Validate checks if an event payload conforms to its registered definition.
// Returns an error if the event is not registered or if the payload is invalid.
// In strict mode, unregistered events are rejected.
func (r *Registry) Validate(eventName string, payload map[string]interface{}) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	def, ok := r.definitions[eventName]
	if !ok {
		if r.strictMode {
			return fmt.Errorf("event '%s' is not registered in the telemetry registry (strict mode enabled)", eventName)
		}
		// In non-strict mode, allow unregistered events but log a warning
		return nil
	}

	// Check stability
	if def.Stability == StabilityDeprecated {
		return fmt.Errorf("event '%s' is deprecated (version %d), use '%s' instead",
			eventName, def.Version, def.DeprecatedBy)
	}

	// Check sensitivity
	if def.Sensitivity == SensitivitySecret {
		return fmt.Errorf("event '%s' has sensitivity level 'secret' and cannot be emitted",
			eventName)
	}

	// Validate required fields
	for _, field := range def.Fields {
		if field.Required {
			if _, ok := payload[field.Name]; !ok {
				return fmt.Errorf("required field '%s' is missing from event '%s'",
					field.Name, eventName)
			}
		}

		// Check if field exists in payload
		if value, ok := payload[field.Name]; ok {
			// Validate field type
			if err := r.validateFieldType(field, value); err != nil {
				return fmt.Errorf("field '%s' validation failed: %w", field.Name, err)
			}

			// Validate enum values
			if len(field.Enum) > 0 {
				if strValue, ok := value.(string); ok {
					valid := false
					for _, enum := range field.Enum {
						if strValue == enum {
							valid = true
							break
						}
					}
					if !valid {
						return fmt.Errorf("field '%s' has invalid value '%s', must be one of: %v",
							field.Name, strValue, field.Enum)
					}
				}
			}
		}
	}

	// Check for unknown fields (optional, can be disabled for flexibility)
	// for key := range payload {
	// 	found := false
	// 	for _, field := range def.Fields {
	// 		if field.Name == key {
	// 			found = true
	// 			break
	// 		}
	// 	}
	// 	if !found {
	// 		return fmt.Errorf("unknown field '%s' in event '%s'", key, eventName)
	// 	}
	// }

	return nil
}

// validateFieldType checks if a value matches the expected field type.
func (r *Registry) validateFieldType(field FieldType, value interface{}) error {
	if value == nil {
		return nil // nil is allowed for optional fields
	}

	switch field.Type {
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("expected string, got %T", value)
		}
	case "int", "int64", "uint64":
		switch value.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			// OK
		default:
			return fmt.Errorf("expected integer, got %T", value)
		}
	case "bool":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("expected bool, got %T", value)
		}
	case "array":
		if reflect.TypeOf(value).Kind() != reflect.Slice {
			return fmt.Errorf("expected array, got %T", value)
		}
	case "object":
		if reflect.TypeOf(value).Kind() != reflect.Map {
			return fmt.Errorf("expected object, got %T", value)
		}
	default:
		return fmt.Errorf("unknown field type: %s", field.Type)
	}

	return nil
}

// isForbiddenField checks if a field name is forbidden.
func (r *Registry) isForbiddenField(name string) bool {
	lower := strings.ToLower(name)
	for forbidden := range r.forbiddenFields {
		if strings.Contains(lower, forbidden) {
			return true
		}
	}
	return false
}

// AddForbiddenField adds a field name to the forbidden list.
func (r *Registry) AddForbiddenField(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.forbiddenFields[strings.ToLower(name)] = true
}

// Global registry instance
var globalRegistry = NewRegistry()

// Register registers an event definition in the global registry.
func Register(def *EventDefinition) error {
	return globalRegistry.Register(def)
}

// Get retrieves an event definition from the global registry.
func Get(name string) *EventDefinition {
	return globalRegistry.Get(name)
}

// List returns all registered event definitions from the global registry.
func List() []*EventDefinition {
	return globalRegistry.List()
}

// Validate validates an event payload against its global registry definition.
func Validate(eventName string, payload map[string]interface{}) error {
	return globalRegistry.Validate(eventName, payload)
}

// SetStrictMode enables or disables strict mode for the global registry.
func SetStrictMode(enabled bool) {
	globalRegistry.SetStrictMode(enabled)
}
