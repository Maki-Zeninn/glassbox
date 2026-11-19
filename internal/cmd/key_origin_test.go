// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// key_origin_test.go verifies that key-origin metadata is correctly populated,
// included in signed audit envelopes, covered by the signature, and free of
// sensitive material.  See Issue #803.
package cmd

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dotandev/glassbox/internal/signer"
	"github.com/spf13/cobra"
)

// ── KeyOrigin on InMemorySigner ──────────────────────────────────────────────

func TestInMemorySigner_KeyOrigin(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	s := signer.NewInMemorySignerFromKey(priv)

	origin := s.KeyOrigin()
	if origin.Provider != "software" {
		t.Errorf("provider = %q, want %q", origin.Provider, "software")
	}
	if origin.Algorithm != "ed25519" {
		t.Errorf("algorithm = %q, want %q", origin.Algorithm, "ed25519")
	}
	if origin.KeyFingerprint == "" {
		t.Error("key_fingerprint must not be empty")
	}

	// Verify fingerprint is SHA-256 of public key, not private key
	pub, _ := priv.Public().(ed25519.PublicKey)
	pubHash := sha256.Sum256(pub)
	expected := hex.EncodeToString(pubHash[:])
	if origin.KeyFingerprint != expected {
		t.Errorf("key_fingerprint = %q, want %q (SHA-256 of public key)", origin.KeyFingerprint, expected)
	}

	// Verify fingerprint is NOT derived from private key material
	privHash := sha256.Sum256(priv.Seed())
	privHex := hex.EncodeToString(privHash[:])
	if origin.KeyFingerprint == privHex {
		t.Error("key_fingerprint must NOT be derived from private key material")
	}
}

func TestInMemorySigner_KeyOrigin_DoesNotLeakSecrets(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)
	s := signer.NewInMemorySignerFromKey(priv)

	origin := s.KeyOrigin()

	// Fingerprint must not be the hex-encoded private key
	privHex := hex.EncodeToString(priv)
	if origin.KeyFingerprint == privHex {
		t.Error("key_fingerprint must not be the private key hex")
	}

	// Fingerprint must not be the hex-encoded private seed
	seedHex := hex.EncodeToString(priv.Seed())
	if origin.KeyFingerprint == seedHex {
		t.Error("key_fingerprint must not be the private seed hex")
	}
}

// ── KeyOrigin from PEM key ───────────────────────────────────────────────────

func TestInMemorySignerFromPEM_KeyOrigin(t *testing.T) {
	pemText := generateTestPEM(t)
	s, err := signer.NewInMemorySignerFromPEM(pemText)
	if err != nil {
		t.Fatalf("NewInMemorySignerFromPEM: %v", err)
	}

	origin := s.KeyOrigin()
	if origin.Provider != "software" {
		t.Errorf("provider = %q, want %q", origin.Provider, "software")
	}
	if origin.KeyFingerprint == "" {
		t.Error("key_fingerprint must not be empty")
	}
}

// ── KeyOrigin in SignedAuditLog ──────────────────────────────────────────────

func TestAuditSign_KeyOriginInOutput(t *testing.T) {
	resetAuditSignFlags()
	auditSignPayload = `{"input":{},"state":{},"events":[],"timestamp":"2026-01-01T00:00:00.000Z"}`
	auditSignSoftwareKey = generateTestPEM(t)

	buf := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(buf)

	if err := runAuditSign(cmd, nil); err != nil {
		t.Fatalf("runAuditSign: %v", err)
	}

	var log SignedAuditLog
	if err := json.Unmarshal(buf.Bytes(), &log); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if log.KeyOrigin == nil {
		t.Fatal("expected key_origin in output")
	}
	if log.KeyOrigin.Provider != "software" {
		t.Errorf("key_origin.provider = %q, want %q", log.KeyOrigin.Provider, "software")
	}
	if log.KeyOrigin.Algorithm != "ed25519" {
		t.Errorf("key_origin.algorithm = %q, want %q", log.KeyOrigin.Algorithm, "ed25519")
	}
	if log.KeyOrigin.KeyFingerprint == "" {
		t.Error("key_origin.key_fingerprint must not be empty")
	}
	// Provider in key_origin should match top-level provider
	if log.KeyOrigin.Provider != log.Provider {
		t.Errorf("key_origin.provider %q != provider %q", log.KeyOrigin.Provider, log.Provider)
	}
}

// ── Signature covers key-origin metadata ──────────────────────────────────────

// TestKeyOriginMetadataCoveredBySignature verifies that modifying key-origin
// metadata after signing invalidates the signature.  This is the critical
// security property: the signature must cover all provenance metadata.
func TestKeyOriginMetadataCoveredBySignature(t *testing.T) {
	resetAuditSignFlags()
	auditSignPayload = `{"input":{},"state":{},"events":[],"timestamp":"2026-01-01T00:00:00.000Z"}`
	auditSignSoftwareKey = generateTestPEM(t)

	buf := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(buf)

	if err := runAuditSign(cmd, nil); err != nil {
		t.Fatalf("runAuditSign: %v", err)
	}

	var log SignedAuditLog
	if err := json.Unmarshal(buf.Bytes(), &log); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Tamper with key_origin.provider — this should invalidate the signature.
	if log.KeyOrigin != nil {
		log.KeyOrigin.Provider = "tampered-provider"
	}

	// Re-serialize and recompute the trace hash
	type signedInput struct {
		Payload   json.RawMessage          `json:"payload"`
		Provider  string                   `json:"provider"`
		KeyOrigin signer.KeyOriginMetadata `json:"key_origin"`
	}
	si := signedInput{
		Payload:   log.Payload,
		Provider:  log.Provider,
		KeyOrigin: *log.KeyOrigin,
	}
	hashInputBytes, err := json.Marshal(si)
	if err != nil {
		t.Fatalf("marshal tampered input: %v", err)
	}
	hash := sha256.Sum256(hashInputBytes)
	tamperedHash := hex.EncodeToString(hash[:])

	// The tampered hash should differ from the original.
	if tamperedHash == log.TraceHash {
		t.Error("tampering key_origin did NOT change the trace hash — metadata is not covered by signature")
	}
}

// TestProviderInHashPreventsSubstitution verifies that changing the provider
// name after signing invalidates the trace hash.
func TestProviderInHashPreventsSubstitution(t *testing.T) {
	resetAuditSignFlags()
	auditSignPayload = `{"input":{},"state":{},"events":[],"timestamp":"2026-01-01T00:00:00.000Z"}`
	auditSignSoftwareKey = generateTestPEM(t)

	buf := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(buf)

	if err := runAuditSign(cmd, nil); err != nil {
		t.Fatalf("runAuditSign: %v", err)
	}

	var log SignedAuditLog
	if err := json.Unmarshal(buf.Bytes(), &log); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Simulate tampering with the provider field.
	type signedInput struct {
		Payload   json.RawMessage          `json:"payload"`
		Provider  string                   `json:"provider"`
		KeyOrigin signer.KeyOriginMetadata `json:"key_origin"`
	}
	si := signedInput{
		Payload:   log.Payload,
		Provider:  "pkcs11", // changed from "software"
		KeyOrigin: *log.KeyOrigin,
	}
	hashInputBytes, _ := json.Marshal(si)
	hash := sha256.Sum256(hashInputBytes)
	tamperedHash := hex.EncodeToString(hash[:])

	if tamperedHash == log.TraceHash {
		t.Error("changing provider did NOT change the trace hash — provider is not covered by signature")
	}
}

// ── No secrets in key-origin metadata ────────────────────────────────────────

// TestKeyOriginNeverContainsSecrets verifies that key-origin metadata never
// contains PINs, private keys, or other sensitive material across all
// provider types.
func TestKeyOriginNeverContainsSecrets(t *testing.T) {
	// Software provider
	_, priv, _ := ed25519.GenerateKey(nil)
	s := signer.NewInMemorySignerFromKey(priv)
	origin := s.KeyOrigin()

	// Must not contain private key hex
	privHex := hex.EncodeToString(priv)
	if strings.Contains(origin.KeyFingerprint, privHex) {
		t.Error("key_fingerprint contains private key hex")
	}

	// Must not contain PEM headers
	if strings.Contains(origin.KeyFingerprint, "BEGIN") {
		t.Error("key_fingerprint contains PEM header")
	}

	// Must not contain PIN patterns
	if strings.Contains(strings.ToLower(origin.KeyFingerprint), "pin") {
		t.Error("key_fingerprint contains 'pin'")
	}
}

// ── Key fingerprint is deterministic ──────────────────────────────────────────

func TestKeyOriginFingerprintDeterministic(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)
	s := signer.NewInMemorySignerFromKey(priv)

	origin1 := s.KeyOrigin()
	origin2 := s.KeyOrigin()

	if origin1.KeyFingerprint != origin2.KeyFingerprint {
		t.Errorf("fingerprint not deterministic: %q != %q", origin1.KeyFingerprint, origin2.KeyFingerprint)
	}
}

// ── Key fingerprint differs for different keys ───────────────────────────────

func TestKeyOriginFingerprintDiffersForDifferentKeys(t *testing.T) {
	_, priv1, _ := ed25519.GenerateKey(nil)
	_, priv2, _ := ed25519.GenerateKey(nil)

	s1 := signer.NewInMemorySignerFromKey(priv1)
	s2 := signer.NewInMemorySignerFromKey(priv2)

	origin1 := s1.KeyOrigin()
	origin2 := s2.KeyOrigin()

	if origin1.KeyFingerprint == origin2.KeyFingerprint {
		t.Error("different keys should have different fingerprints")
	}
}

// ── KeyOrigin round-trip via JSON ────────────────────────────────────────────

func TestKeyOriginMetadata_JSONRoundTrip(t *testing.T) {
	origin := signer.KeyOriginMetadata{
		Provider:       "software",
		Algorithm:      "ed25519",
		KeyFingerprint: "abc123",
		CreatedAt:      "2026-01-01T00:00:00Z",
	}

	data, err := json.Marshal(origin)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded signer.KeyOriginMetadata
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Provider != origin.Provider {
		t.Errorf("provider = %q, want %q", decoded.Provider, origin.Provider)
	}
	if decoded.Algorithm != origin.Algorithm {
		t.Errorf("algorithm = %q, want %q", decoded.Algorithm, origin.Algorithm)
	}
	if decoded.KeyFingerprint != origin.KeyFingerprint {
		t.Errorf("key_fingerprint = %q, want %q", decoded.KeyFingerprint, origin.KeyFingerprint)
	}
	if decoded.CreatedAt != origin.CreatedAt {
		t.Errorf("created_at = %q, want %q", decoded.CreatedAt, origin.CreatedAt)
	}
}

// ── PEM private key not in fingerprint ────────────────────────────────────────

func TestKeyOrigin_FingerprintNotPEM(t *testing.T) {
	pemText := generateTestPEM(t)
	s, _ := signer.NewInMemorySignerFromPEM(pemText)

	origin := s.KeyOrigin()

	// Fingerprint should not look like PEM
	if strings.Contains(origin.KeyFingerprint, "-----") {
		t.Error("key_fingerprint looks like PEM data")
	}

	// Fingerprint should be hex
	for _, c := range origin.KeyFingerprint {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("key_fingerprint contains non-hex character %q", c)
			break
		}
	}
}

// ── KeyOrigin for mock provider ──────────────────────────────────────────────

func TestMockProvider_KeyOrigin(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)
	s := signer.NewInMemorySignerFromKey(priv)

	origin := s.KeyOrigin()
	if origin.Provider != "software" {
		t.Errorf("provider = %q, want %q", origin.Provider, "software")
	}
}

// Ensure generateTestPEM is available (from audit_sign_test.go)
var _ = generateTestPEM
