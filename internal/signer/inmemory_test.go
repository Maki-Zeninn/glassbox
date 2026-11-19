// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package signer

import (
	"crypto/ed25519"
	"encoding/hex"
	"testing"
)

func TestInMemorySigner_CloseZerosSeed(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	s := NewInMemorySignerFromKey(priv)
	defer func() { _ = s.Close() }()

	payload := []byte("glassbox-audit-payload")
	sig, err := s.Sign(payload)
	if err != nil {
		t.Fatalf("Sign before Close: %v", err)
	}
	if len(sig) != ed25519.SignatureSize {
		t.Fatalf("signature length %d, want %d", len(sig), ed25519.SignatureSize)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !s.SeedIsZeroed() {
		t.Fatal("seed must be zeroed after Close")
	}
	if _, err := s.Sign(payload); err == nil {
		t.Fatal("Sign after Close must fail")
	}
}

func TestInMemorySigner_KeyOriginIncludesMemoryLock(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i + 3)
	}
	s, err := NewInMemorySigner(hex.EncodeToString(seed))
	if err != nil {
		t.Fatalf("NewInMemorySigner: %v", err)
	}
	defer s.Close()

	meta := s.KeyOrigin()
	if meta.MemoryLock == "" {
		t.Fatal("KeyOrigin.MemoryLock must be populated for audit metadata")
	}
	if meta.Provider != "software" {
		t.Errorf("Provider=%q, want software", meta.Provider)
	}
}
