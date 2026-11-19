// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package protocolreg

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"testing"
	"time"

	"github.com/dotandev/glassbox/internal/signer"
)

func TestSignAndVerifyRegistryMetadata(t *testing.T) {
	privKey := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	s := signer.NewInMemorySignerFromKey(privKey)

	store := NewTrustedKeyStore()
	pub, _ := s.PublicKey()
	hash := sha256Sum(pub)
	store.AddKey(TrustedKey{
		KeyID:     "test-key-1",
		PublicKey: ed25519.PublicKey(pub),
		PublicHex: hex.EncodeToString(hash),
		ValidFrom: time.Now().Add(-time.Hour),
	})

	m := &RegistryMetadata{
		SchemaVersion:   RegistryMetadataSchemaVersion,
		ProtocolID:      "soroban-rpc",
		ProtocolVersion: "21.0.0",
		RegisteredAt:    time.Now().Truncate(time.Second),
		ExecutablePath:  "/usr/local/bin/glassbox",
		Platform:        "linux",
		Scheme:          "glassbox",
		Enabled:         true,
	}

	if err := SignRegistryMetadata(m, s); err != nil {
		t.Fatal(err)
	}

	if m.Signature == "" {
		t.Fatal("signature not set after signing")
	}
	if m.KeyFingerprint == "" {
		t.Fatal("key fingerprint not set after signing")
	}

	result := VerifyRegistryMetadata(m, store)
	if !result.OK {
		t.Errorf("verification failed: %v", result.Diagnostics)
	}
	if len(result.Diagnostics) == 0 {
		t.Error("expected at least one diagnostic")
	}
}

func TestVerifyTamperedMetadata(t *testing.T) {
	privKey := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	s := signer.NewInMemorySignerFromKey(privKey)

	store := NewTrustedKeyStore()
	pub, _ := s.PublicKey()
	hash := sha256Sum(pub)
	store.AddKey(TrustedKey{
		KeyID:     "test-key-1",
		PublicKey: ed25519.PublicKey(pub),
		PublicHex: hex.EncodeToString(hash),
		ValidFrom: time.Now().Add(-time.Hour),
	})

	m := &RegistryMetadata{
		SchemaVersion:   RegistryMetadataSchemaVersion,
		ProtocolID:      "soroban-rpc",
		ProtocolVersion: "21.0.0",
		RegisteredAt:    time.Now().Truncate(time.Second),
		ExecutablePath:  "/usr/local/bin/glassbox",
		Platform:        "linux",
		Scheme:          "glassbox",
		Enabled:         true,
	}
	_ = SignRegistryMetadata(m, s)

	// Tamper with the content.
	m.ExecutablePath = "/tmp/evil/glassbox"

	result := VerifyRegistryMetadata(m, store)
	if result.OK {
		t.Error("expected verification to fail for tampered metadata")
	}
}

func TestVerifyRevokedEntry(t *testing.T) {
	privKey := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	s := signer.NewInMemorySignerFromKey(privKey)

	store := NewTrustedKeyStore()
	pub, _ := s.PublicKey()
	hash := sha256Sum(pub)
	store.AddKey(TrustedKey{
		KeyID:     "test-key-1",
		PublicKey: ed25519.PublicKey(pub),
		PublicHex: hex.EncodeToString(hash),
		ValidFrom: time.Now().Add(-time.Hour),
	})

	m := &RegistryMetadata{
		SchemaVersion:   RegistryMetadataSchemaVersion,
		ProtocolID:      "soroban-rpc",
		ProtocolVersion: "21.0.0",
		RegisteredAt:    time.Now().Truncate(time.Second),
		ExecutablePath:  "/usr/local/bin/glassbox",
		Platform:        "linux",
		Scheme:          "glassbox",
		Enabled:         true,
		Revoked:         true,
		RevocationNote:  "security vulnerability",
	}
	_ = SignRegistryMetadata(m, s)

	result := VerifyRegistryMetadata(m, store)
	if result.OK {
		t.Error("expected verification to fail for revoked entry")
	}
	found := false
	for _, d := range result.Diagnostics {
		if d.Code == DiagKeyRevoked {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected DiagKeyRevoked diagnostic")
	}
}

func TestVerifyExpiredEntry(t *testing.T) {
	privKey := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	s := signer.NewInMemorySignerFromKey(privKey)

	store := NewTrustedKeyStore()
	pub, _ := s.PublicKey()
	hash := sha256Sum(pub)
	store.AddKey(TrustedKey{
		KeyID:     "test-key-1",
		PublicKey: ed25519.PublicKey(pub),
		PublicHex: hex.EncodeToString(hash),
		ValidFrom: time.Now().Add(-time.Hour),
	})

	m := &RegistryMetadata{
		SchemaVersion:   RegistryMetadataSchemaVersion,
		ProtocolID:      "soroban-rpc",
		ProtocolVersion: "21.0.0",
		RegisteredAt:    time.Now().Truncate(time.Second),
		ExpiresAt:       time.Now().Add(-time.Hour),
		ExecutablePath:  "/usr/local/bin/glassbox",
		Platform:        "linux",
		Scheme:          "glassbox",
		Enabled:         true,
	}
	_ = SignRegistryMetadata(m, s)

	result := VerifyRegistryMetadata(m, store)
	if result.OK {
		t.Error("expected verification to fail for expired entry")
	}
}

func TestVerifyUnknownKey(t *testing.T) {
	privKey := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	s := signer.NewInMemorySignerFromKey(privKey)

	store := NewTrustedKeyStore()
	// Don't add the key to the store.

	m := &RegistryMetadata{
		SchemaVersion:   RegistryMetadataSchemaVersion,
		ProtocolID:      "soroban-rpc",
		ProtocolVersion: "21.0.0",
		RegisteredAt:    time.Now().Truncate(time.Second),
		ExecutablePath:  "/usr/local/bin/glassbox",
		Platform:        "linux",
		Scheme:          "glassbox",
		Enabled:         true,
	}
	_ = SignRegistryMetadata(m, s)

	result := VerifyRegistryMetadata(m, store)
	if result.OK {
		t.Error("expected verification to fail for unknown key")
	}
}

func TestVerifyOfflineMode(t *testing.T) {
	privKey := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	s := signer.NewInMemorySignerFromKey(privKey)

	m := &RegistryMetadata{
		SchemaVersion:   RegistryMetadataSchemaVersion,
		ProtocolID:      "soroban-rpc",
		ProtocolVersion: "21.0.0",
		RegisteredAt:    time.Now().Truncate(time.Second),
		ExecutablePath:  "/usr/local/bin/glassbox",
		Platform:        "linux",
		Scheme:          "glassbox",
		Enabled:         true,
	}
	_ = SignRegistryMetadata(m, s)

	// Offline: nil key store.
	result := VerifyRegistryMetadata(m, nil)
	if !result.OK {
		t.Error("structural checks should pass even offline")
	}
	found := false
	for _, d := range result.Diagnostics {
		if d.Code == DiagOfflineVerification {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected offline verification diagnostic")
	}
}

func TestVerifyNilMetadata(t *testing.T) {
	result := VerifyRegistryMetadata(nil, nil)
	if result.OK {
		t.Error("expected failure for nil metadata")
	}
}

func TestVerifyRevokedKey(t *testing.T) {
	privKey := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	s := signer.NewInMemorySignerFromKey(privKey)

	store := NewTrustedKeyStore()
	pub, _ := s.PublicKey()
	hash := sha256Sum(pub)
	store.AddKey(TrustedKey{
		KeyID:     "test-key-1",
		PublicKey: ed25519.PublicKey(pub),
		PublicHex: hex.EncodeToString(hash),
		ValidFrom: time.Now().Add(-time.Hour),
		Revoked:   true,
	})

	m := &RegistryMetadata{
		SchemaVersion:   RegistryMetadataSchemaVersion,
		ProtocolID:      "soroban-rpc",
		ProtocolVersion: "21.0.0",
		RegisteredAt:    time.Now().Truncate(time.Second),
		ExecutablePath:  "/usr/local/bin/glassbox",
		Platform:        "linux",
		Scheme:          "glassbox",
		Enabled:         true,
	}
	_ = SignRegistryMetadata(m, s)

	result := VerifyRegistryMetadata(m, store)
	if result.OK {
		t.Error("expected failure for revoked key")
	}
}

func TestTrustedKeyStoreFindKey(t *testing.T) {
	store := NewTrustedKeyStore()
	store.AddKey(TrustedKey{
		KeyID:     "key-1",
		PublicHex: "abc123",
	})
	store.AddKey(TrustedKey{
		KeyID:     "key-2",
		PublicHex: "def456",
	})

	if k := store.FindKey("key-1"); k == nil {
		t.Error("FindKey by ID should succeed")
	}
	if k := store.FindKey("abc123"); k == nil {
		t.Error("FindKey by hex should succeed")
	}
	if k := store.FindKey("nonexistent"); k != nil {
		t.Error("FindKey by nonexistent should return nil")
	}
}

func TestRegistryMetadataSchemaVersion(t *testing.T) {
	m := &RegistryMetadata{
		SchemaVersion:   999,
		ProtocolID:      "test",
		ProtocolVersion: "1.0",
		RegisteredAt:    time.Now(),
	}

	store := NewTrustedKeyStore()
	result := VerifyRegistryMetadata(m, store)

	if result.OK {
		t.Error("expected failure for unsupported schema version")
	}
	found := false
	for _, d := range result.Diagnostics {
		if d.Code == DiagSchemaUnsupported {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected DiagSchemaUnsupported diagnostic")
	}
}

func sha256Sum(data []byte) []byte {
	h := sha256.Sum256(data)
	return h[:]
}

// sha256Sum is a helper used in tests to compute SHA-256.
// It returns the raw bytes (not hex-encoded).
func sha256SumForTest(data []byte) []byte {
	h := sha256.Sum256(data)
	return h[:]
}

// generateTestKeyPair creates a test Ed25519 key pair.
func generateTestKeyPair() (ed25519.PublicKey, ed25519.PrivateKey) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	return pub, priv
}
