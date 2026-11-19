// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package telemetry

import (
	"testing"
)

func TestRegistry_Register(t *testing.T) {
	registry := NewRegistry()

	// Test valid registration
	def := &EventDefinition{
		Name:        "test.event",
		Version:     1,
		Owner:       "test-team",
		Description: "Test event",
		Stability:   StabilityStable,
		Sensitivity: SensitivityPublic,
		Retention:   RetentionTransient,
		Fields: []FieldType{
			{Name: "id", Type: "string", Required: true, Sensitive: false},
		},
	}

	err := registry.Register(def)
	if err != nil {
		t.Fatalf("Failed to register valid event: %v", err)
	}

	// Test duplicate registration
	err = registry.Register(def)
	if err == nil {
		t.Error("Expected error when registering duplicate event")
	}

	// Test invalid registration (empty name)
	def.Name = ""
	err = registry.Register(def)
	if err == nil {
		t.Error("Expected error when registering event with empty name")
	}

	// Test invalid registration (forbidden field)
	def.Name = "test.forbidden"
	def.Fields = []FieldType{
		{Name: "password", Type: "string", Required: true, Sensitive: false},
	}
	err = registry.Register(def)
	if err == nil {
		t.Error("Expected error when registering event with forbidden field")
	}
}

func TestRegistry_Get(t *testing.T) {
	registry := NewRegistry()

	def := &EventDefinition{
		Name:        "test.get",
		Version:     1,
		Owner:       "test-team",
		Description: "Test event",
		Stability:   StabilityStable,
		Sensitivity: SensitivityPublic,
		Retention:   RetentionTransient,
		Fields:      []FieldType{},
	}

	registry.Register(def)

	// Test getting existing event
	retrieved := registry.Get("test.get")
	if retrieved == nil {
		t.Error("Failed to retrieve registered event")
	}
	if retrieved.Name != "test.get" {
		t.Errorf("Retrieved wrong event: got %s, want test.get", retrieved.Name)
	}

	// Test getting non-existent event
	retrieved = registry.Get("nonexistent")
	if retrieved != nil {
		t.Error("Expected nil for non-existent event")
	}
}

func TestRegistry_Validate(t *testing.T) {
	registry := NewRegistry()

	def := &EventDefinition{
		Name:        "test.validate",
		Version:     1,
		Owner:       "test-team",
		Description: "Test event",
		Stability:   StabilityStable,
		Sensitivity: SensitivityPublic,
		Retention:   RetentionTransient,
		Fields: []FieldType{
			{Name: "id", Type: "string", Required: true, Sensitive: false},
			{Name: "count", Type: "int", Required: false, Sensitive: false},
			{Name: "status", Type: "string", Required: false, Sensitive: false, Enum: []string{"active", "inactive"}},
		},
	}

	registry.Register(def)

	// Test valid payload
	payload := map[string]interface{}{
		"id":     "123",
		"count":  42,
		"status": "active",
	}
	err := registry.Validate("test.validate", payload)
	if err != nil {
		t.Errorf("Valid payload failed validation: %v", err)
	}

	// Test missing required field
	payload = map[string]interface{}{
		"count": 42,
	}
	err = registry.Validate("test.validate", payload)
	if err == nil {
		t.Error("Expected error for missing required field")
	}

	// Test invalid field type
	payload = map[string]interface{}{
		"id":    123, // Should be string
		"count": 42,
	}
	err = registry.Validate("test.validate", payload)
	if err == nil {
		t.Error("Expected error for invalid field type")
	}

	// Test invalid enum value
	payload = map[string]interface{}{
		"id":     "123",
		"status": "invalid",
	}
	err = registry.Validate("test.validate", payload)
	if err == nil {
		t.Error("Expected error for invalid enum value")
	}

	// Test unregistered event (non-strict mode)
	err = registry.Validate("unregistered", payload)
	if err != nil {
		t.Errorf("Unregistered event should be allowed in non-strict mode: %v", err)
	}

	// Test unregistered event (strict mode)
	registry.SetStrictMode(true)
	err = registry.Validate("unregistered", payload)
	if err == nil {
		t.Error("Expected error for unregistered event in strict mode")
	}
}

func TestRegistry_ValidateDeprecated(t *testing.T) {
	registry := NewRegistry()

	def := &EventDefinition{
		Name:              "test.deprecated",
		Version:           1,
		Owner:             "test-team",
		Description:       "Test event",
		Stability:         StabilityDeprecated,
		Sensitivity:       SensitivityPublic,
		Retention:         RetentionTransient,
		DeprecatedVersion: 2,
		DeprecatedBy:      "test.new_event",
		Fields:            []FieldType{},
	}

	registry.Register(def)

	payload := map[string]interface{}{}
	err := registry.Validate("test.deprecated", payload)
	if err == nil {
		t.Error("Expected error for deprecated event")
	}
}

func TestRegistry_ValidateSecret(t *testing.T) {
	registry := NewRegistry()

	def := &EventDefinition{
		Name:        "test.secret",
		Version:     1,
		Owner:       "test-team",
		Description: "Test event",
		Stability:   StabilityStable,
		Sensitivity: SensitivitySecret,
		Retention:   RetentionTransient,
		Fields:      []FieldType{},
	}

	registry.Register(def)

	payload := map[string]interface{}{}
	err := registry.Validate("test.secret", payload)
	if err == nil {
		t.Error("Expected error for secret sensitivity event")
	}
}

func TestRegistry_ForbiddenFields(t *testing.T) {
	registry := NewRegistry()

	// Test that forbidden fields are rejected
	forbiddenNames := []string{"password", "secret", "token", "api_key"}

	for _, name := range forbiddenNames {
		def := &EventDefinition{
			Name:        "test." + name,
			Version:     1,
			Owner:       "test-team",
			Description: "Test event",
			Stability:   StabilityStable,
			Sensitivity: SensitivityPublic,
			Retention:   RetentionTransient,
			Fields: []FieldType{
				{Name: name, Type: "string", Required: true, Sensitive: false},
			},
		}

		err := registry.Register(def)
		if err == nil {
			t.Errorf("Expected error for forbidden field name: %s", name)
		}
	}

	// Test that adding custom forbidden fields works
	registry.AddForbiddenField("custom_forbidden")

	def := &EventDefinition{
		Name:        "test.custom",
		Version:     1,
		Owner:       "test-team",
		Description: "Test event",
		Stability:   StabilityStable,
		Sensitivity: SensitivityPublic,
		Retention:   RetentionTransient,
		Fields: []FieldType{
			{Name: "custom_forbidden", Type: "string", Required: true, Sensitive: false},
		},
	}

	err := registry.Register(def)
	if err == nil {
		t.Error("Expected error for custom forbidden field")
	}
}

func TestGlobalRegistry(t *testing.T) {
	// Reset global registry for testing
	globalRegistry = NewRegistry()

	def := &EventDefinition{
		Name:        "global.test",
		Version:     1,
		Owner:       "test-team",
		Description: "Test event",
		Stability:   StabilityStable,
		Sensitivity: SensitivityPublic,
		Retention:   RetentionTransient,
		Fields:      []FieldType{},
	}

	err := Register(def)
	if err != nil {
		t.Fatalf("Failed to register in global registry: %v", err)
	}

	retrieved := Get("global.test")
	if retrieved == nil {
		t.Error("Failed to retrieve from global registry")
	}

	list := List()
	if len(list) != 1 {
		t.Errorf("Expected 1 event in global registry, got %d", len(list))
	}

	payload := map[string]interface{}{}
	err = Validate("global.test", payload)
	if err != nil {
		t.Errorf("Validation failed: %v", err)
	}

	SetStrictMode(true)
	err = Validate("unregistered", payload)
	if err == nil {
		t.Error("Expected error in strict mode for unregistered event")
	}
}

func TestGenerateDocs(t *testing.T) {
	// Reset global registry for testing
	globalRegistry = NewRegistry()

	def := &EventDefinition{
		Name:        "test.docs",
		Version:     1,
		Owner:       "test-team",
		Description: "Test event for docs generation",
		Stability:   StabilityStable,
		Sensitivity: SensitivityPublic,
		Retention:   RetentionTransient,
		Fields: []FieldType{
			{Name: "id", Type: "string", Required: true, Sensitive: false},
			{Name: "status", Type: "string", Required: false, Sensitive: false, Enum: []string{"active", "inactive"}},
		},
		MigrationRules: []MigrationRule{
			{
				FromVersion: 1,
				ToVersion:   2,
				Transform:   "Rename field",
				FieldMap:    map[string]string{"old_field": "new_field"},
			},
		},
	}

	Register(def)

	docs := GenerateDocs()
	if docs == "" {
		t.Error("GenerateDocs returned empty string")
	}

	// Check that key information is present
	if !contains(docs, "test.docs") {
		t.Error("Generated docs missing event name")
	}
	if !contains(docs, "v1") {
		t.Error("Generated docs missing version")
	}
	if !contains(docs, "test-team") {
		t.Error("Generated docs missing owner")
	}
	if !contains(docs, "Stable") {
		t.Error("Generated docs missing stability")
	}
	if !contains(docs, "Public") {
		t.Error("Generated docs missing sensitivity")
	}
	if !contains(docs, "Transient") {
		t.Error("Generated docs missing retention")
	}
	if !contains(docs, "id") {
		t.Error("Generated docs missing field name")
	}
	if !contains(docs, "Migration Rules") {
		t.Error("Generated docs missing migration rules")
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && findSubstring(s, substr) >= 0
}

func findSubstring(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
