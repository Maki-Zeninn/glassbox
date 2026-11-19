// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"encoding/json"
	"testing"
)

// FuzzCanonicalJSON exercises the canonical JSON encoder with arbitrary inputs
// to ensure it never panics, always produces valid JSON, and is idempotent.
func FuzzCanonicalJSON(f *testing.F) {
	// Seed corpus: known inputs that exercise different code paths.
	f.Add(`{"zebra":1,"apple":2,"banana":3}`)
	f.Add(`{"nested":{"z":1,"a":2},"arr":[3,1,2]}`)
	f.Add(`{"a":"hello\nworld","b":"","c":null}`)
	f.Add(`{"num":42.5,"bool":true,"empty":{}}`)
	f.Add(`[1,2,3]`)
	f.Add(`"simple string"`)
	f.Add(`42`)
	f.Add(`true`)
	f.Add(`null`)
	f.Add(`{}`)
	f.Add(`[]`)

	f.Fuzz(func(t *testing.T, input string) {
		// Parse arbitrary JSON input.
		var raw interface{}
		if err := json.Unmarshal([]byte(input), &raw); err != nil {
			return // not valid JSON, skip
		}

		// Canonical encoding must never panic.
		result, err := canonicalJSON(raw)
		if err != nil {
			t.Fatalf("canonicalJSON returned error on valid input: %v\ninput: %s", err, input)
		}

		// Result must be valid JSON.
		var reparse interface{}
		if err := json.Unmarshal(result, &reparse); err != nil {
			t.Fatalf("canonicalJSON produced invalid JSON: %v\nresult: %s", err, result)
		}

		// Idempotency: encoding the canonical output again must produce the same bytes.
		result2, err := canonicalJSON(reparse)
		if err != nil {
			t.Fatalf("second canonicalJSON call failed: %v", err)
		}
		if string(result) != string(result2) {
			t.Fatalf("canonicalJSON is not idempotent:\n  first:  %s\n  second: %s", result, result2)
		}
	})
}

// FuzzMarshalCanonical exercises the struct-based canonical marshaler.
func FuzzMarshalCanonical(f *testing.F) {
	f.Add("envelope_data", "meta_data")
	f.Add("", "")
	f.Add("aaaa", "bbbb")
	f.Add(`{"key":"val"}`, `{"key2":"val2"}`)

	f.Fuzz(func(t *testing.T, envelopeXdr, resultMetaXdr string) {
		payload := Payload{
			EnvelopeXdr:   envelopeXdr,
			ResultMetaXdr: resultMetaXdr,
			Events:        []string{"event1"},
			Logs:          []string{"log1"},
		}

		result, err := marshalCanonical(payload)
		if err != nil {
			t.Fatalf("marshalCanonical returned error: %v", err)
		}

		// Result must be valid JSON.
		var parsed map[string]interface{}
		if err := json.Unmarshal(result, &parsed); err != nil {
			t.Fatalf("marshalCanonical produced invalid JSON: %v\nresult: %s", err, result)
		}

		// Idempotency.
		result2, err := marshalCanonical(payload)
		if err != nil {
			t.Fatalf("second marshalCanonical failed: %v", err)
		}
		if string(result) != string(result2) {
			t.Fatalf("marshalCanonical is not idempotent")
		}
	})
}
