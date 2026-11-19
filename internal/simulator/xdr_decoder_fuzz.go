// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

//go:build go1.18
// +build go1.18

package simulator

import (
	"encoding/base64"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// FuzzDecodeEnvelope tests the XDR envelope decoder with malformed input.
// It targets the DecodeEnvelope function which parses transaction envelopes
// from base64-encoded XDR, a critical security boundary.
func FuzzDecodeEnvelope(f *testing.F) {
	// Seed corpus with valid and edge-case inputs
	f.Add("AAAAAgAAAABAAAAAAAAAAAAAAAAAAAAAAABAAAAAEAAAAA=") // Minimal valid envelope
	f.Add("") // Empty input
	f.Add("invalid base64!") // Invalid base64
	f.Add("AAA=") // Truncated base64
	f.Add(string(make([]byte, 1000))) // Large input

	f.Fuzz(func(t *testing.T, data string) {
		// The function under test
		_, err := DecodeEnvelopeXDR(data)
		
		// We expect errors for malformed input, but no panics
		_ = err
	})
}

// FuzzXDRSafeUnmarshal tests the XDR unmarshaler with raw binary data.
// This targets the underlying xdr.SafeUnmarshal used throughout the codebase.
func FuzzXDRSafeUnmarshal(f *testing.F) {
	// Seed with various XDR structures
	f.Add([]byte{0, 0, 0, 1}) // Minimal XDR
	f.Add([]byte{}) // Empty
	f.Add(make([]byte, 100)) // Larger input

	f.Fuzz(func(t *testing.T, data []byte) {
		var diag xdr.DiagnosticEvent
		err := xdr.SafeUnmarshal(data, &diag)
		_ = err // Expect errors for malformed data, but no panics
	})
}

// FuzzDecodeDiagnosticEvent tests diagnostic event decoding.
func FuzzDecodeDiagnosticEvent(f *testing.F) {
	f.Add("AAAAAgAAAABAAAAA==") // Valid event
	f.Add("") // Empty
	f.Add("invalid")

	f.Fuzz(func(t *testing.T, data string) {
		decoded, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			return // Skip invalid base64
		}
		
		var diag xdr.DiagnosticEvent
		err = xdr.SafeUnmarshal(decoded, &diag)
		_ = err
	})
}
