// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

//go:build go1.18
// +build go1.18

package decoder

import (
	"encoding/json"
	"testing"
)

// FuzzDecodeEvents tests the event decoder with malformed JSON and base64 input.
// This targets the DecodeEvents function which parses base64-encoded XDR events.
func FuzzDecodeEvents(f *testing.F) {
	// Seed corpus with valid and edge-case inputs
	f.Add([]string{"AAAAAgAAAABAAAAA=="}, 10) // Valid event
	f.Add([]string{}, 10) // Empty events
	f.Add([]string{""}, 10) // Empty string event
	f.Add([]string{"invalid base64!"}, 10) // Invalid base64
	f.Add([]string{"AAA="}, 10) // Truncated base64
	f.Add(make([]string, 100), 10) // Many events

	f.Fuzz(func(t *testing.T, events []string, maxDepth int) {
		// The function under test
		_, err := DecodeEvents(events, maxDepth)
		
		// We expect errors for malformed input, but no panics
		_ = err
	})
}

// FuzzDecodeDiagnosticEvents tests the gas-aware diagnostic event decoder.
func FuzzDecodeDiagnosticEvents(f *testing.F) {
	f.Add([]string{"AAAAAgAAAABAAAAA=="}, 10) // Valid event
	f.Add([]string{}, 10) // Empty events
	f.Add([]string{"invalid"}, 10) // Invalid base64

	f.Fuzz(func(t *testing.T, events []string, maxDepth int) {
		_, err := DecodeDiagnosticEvents(events, maxDepth)
		_ = err
	})
}

// FuzzUnmarshalJSON tests JSON unmarshaling with malformed input.
// This targets the standard library JSON decoder used throughout.
func FuzzUnmarshalJSON(f *testing.F) {
	f.Add([]byte(`{"contract_id":"test","topics":["a","b"]}`)) // Valid JSON
	f.Add([]byte(`{}`)) // Empty object
	f.Add([]byte(`[]`)) // Empty array
	f.Add([]byte(``)) // Empty
	f.Add([]byte(`{invalid`)) // Malformed
	f.Add(make([]byte, 1000)) // Large input

	f.Fuzz(func(t *testing.T, data []byte) {
		var result map[string]interface{}
		err := json.Unmarshal(data, &result)
		_ = err // Expect errors for malformed JSON, but no panics
	})
}
