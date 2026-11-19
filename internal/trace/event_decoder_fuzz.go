// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

//go:build go1.18
// +build go1.18

package trace

import (
	"testing"
)

// FuzzParseContractEvent tests contract event parsing with malformed input.
// This targets the ParseContractEvent function which decodes event strings.
func FuzzParseContractEvent(f *testing.F) {
	// Seed corpus with valid and edge-case inputs
	f.Add(`{"contract_id":"test","topics":["a","b"],"data":"c3RyaW5n","type":"contract"}`) // Valid event
	f.Add(`{}`) // Empty object
	f.Add(`[]`) // Empty array
	f.Add(``) // Empty string
	f.Add(`{invalid`) // Malformed JSON
	f.Add(`{"contract_id":"","topics":[],"data":"","type":""}`) // All empty
	f.Add(make([]byte, 1000)) // Large input

	f.Fuzz(func(t *testing.T, data []byte) {
		_, err := ParseContractEvent(string(data))
		_ = err // Expect errors for malformed input, but no panics
	})
}

// FuzzParseEventEnvelope tests the raw event envelope parsing.
func FuzzParseEventEnvelope(f *testing.F) {
	f.Add(`{"contract_id":"test","topics":["a"],"data":"test","type":"contract"}`)
	f.Add(`{}`)
	f.Add(`invalid`)

	f.Fuzz(func(t *testing.T, data []byte) {
		var envelope rawEventEnvelope
		err := envelope.UnmarshalJSON(data)
		_ = err
	})
}

// FuzzApplyEventSchema tests schema application to events.
func FuzzApplyEventSchema(f *testing.F) {
	f.Add(`{"contract_id":"test","topics":["a"],"data":"c3RyaW5n","type":"contract"}`, 
		`[{"name":"value","type":"string"}]`)
	f.Add(`{}`, `[]`)

	f.Fuzz(func(t *testing.T, eventData []byte, schemaData []byte) {
		var event ContractEvent
		var schema []EventFieldSchema
		
		// Parse event
		if err := event.UnmarshalJSON(eventData); err != nil {
			return
		}
		
		// Parse schema
		if err := schema.UnmarshalJSON(schemaData); err != nil {
			return
		}
		
		// Apply schema
		_, err := ApplyEventSchema(event, schema)
		_ = err
	})
}
