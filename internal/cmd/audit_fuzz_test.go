// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"encoding/hex"
	"encoding/json"
	"testing"
)

// FuzzAuditLogVerification fuzzes the audit log verification pipeline with
// malformed input to ensure it never panics and always returns clean errors.
func FuzzAuditLogVerification(f *testing.F) {
	// Build a valid signed log as a seed.
	privHex, pubKey := generateTestKeyPair()
	validLog, err := Generate("tx_fuzz", "env", "meta", []string{"evt"}, []string{"log"}, privHex, nil)
	if err != nil {
		f.Fatalf("failed to generate seed log: %v", err)
	}
	validBytes, _ := json.Marshal(validLog)
	f.Add(validBytes)
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"signature":"","trace_hash":"","public_key":"","provider":"","payload":{}}`))
	f.Add([]byte(`not json`))
	f.Add([]byte(`{"version":"1.0.0"}`))

	_ = pubKey

	f.Fuzz(func(t *testing.T, data []byte) {
		var log SignedAuditLog
		if err := json.Unmarshal(data, &log); err != nil {
			return // not valid JSON, skip
		}

		// Field validation must never panic.
		_ = validateAuditLogFields(&log, false)

		// If we have the minimum required fields, try the full verify path.
		if log.TraceHash == "" || log.Signature == "" || log.PublicKey == "" {
			return
		}

		// Validate public key format.
		pubBytes, err := hex.DecodeString(log.PublicKey)
		if err != nil {
			return
		}
		if len(pubBytes) != 32 {
			return
		}

		// Validate signature format.
		sigBytes, err := hex.DecodeString(log.Signature)
		if err != nil {
			return
		}
		if len(sigBytes) != 64 {
			return
		}

		// Re-derive the hash from payload and verify — must not panic.
		var payload interface{}
		if err := json.Unmarshal(log.Payload, &payload); err != nil {
			return
		}
		canonicalBytes, err := marshalCanonical(payload)
		if err != nil {
			return
		}
		_ = canonicalBytes
	})
}

// FuzzValidateProvenance fuzzes the provenance validation with arbitrary inputs.
func FuzzValidateProvenance(f *testing.F) {
	f.Add("", "", "", "")
	f.Add("Alice", "key-1", "ed25519", "")
	f.Add("Bob", "key-2", "ed25519", "abc123")
	f.Add("", "", "", "0000000000000000000000000000000000000000000000000000000000000000")
	f.Add("Signer", "id", "ed25519", "not-a-hash")

	f.Fuzz(func(t *testing.T, identity, keyID, algorithm, prevHash string) {
		p := &SignatureProvenance{
			SignerIdentity:        identity,
			KeyID:                 keyID,
			Algorithm:             algorithm,
			PreviousSignatureHash: prevHash,
		}

		// validateProvenance must never panic.
		_, _ = validateProvenance(p)
	})
}

// FuzzValidateSHA256HexHash fuzzes the SHA-256 hex hash validator.
func FuzzValidateSHA256HexHash(f *testing.F) {
	f.Add("label", "0000000000000000000000000000000000000000000000000000000000000000")
	f.Add("label", "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
	f.Add("label", "")
	f.Add("label", "short")
	f.Add("label", "ghijkl0000000000000000000000000000000000000000000000000000000000")
	f.Add("label", "000000000000000000000000000000000000000000000000000000000000000000")

	f.Fuzz(func(t *testing.T, label, hash string) {
		// Must never panic regardless of input.
		_ = validateSHA256HexHash(label, hash)
	})
}

// FuzzAuditVerifyInputs fuzzes the input validation for the audit:verify command.
func FuzzAuditVerifyInputs(f *testing.F) {
	f.Add("", "", "")
	f.Add("0000000000000000000000000000000000000000000000000000000000000000", "", "")
	f.Add("not-hex", "", "")
	f.Add("", "/nonexistent/file.json", "")
	f.Add("", "", "0000000000000000000000000000000000000000000000000000000000000000")
	f.Add("", "", "tooshort")
	f.Add("000000000000000000000000000000000000000000000000000000000000000", "", "")

	f.Fuzz(func(t *testing.T, pubKeyHex, schemaPath, previousHash string) {
		// Must never panic regardless of input.
		_ = validateAuditVerifyInputs(pubKeyHex, schemaPath, previousHash)
	})
}

// FuzzCheckJSONType fuzzes the JSON type checker used in schema validation.
func FuzzCheckJSONType(f *testing.F) {
	f.Add("field", "hello", "string")
	f.Add("field", float64(42), "number")
	f.Add("field", true, "boolean")
	f.Add("field", nil, "null")
	f.Add("field", "hello", "object")
	f.Add("field", float64(1), "string")

	f.Fuzz(func(t *testing.T, field, val string, expected string) {
		// Must never panic regardless of input.
		_ = checkJSONType(field, val, expected)
	})
}
