// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package deterministic

import (
	"encoding/hex"
	"testing"
)

func TestSeedProvider_SetSeed(t *testing.T) {
	provider := NewSeedProvider()

	var seed [32]byte
	for i := range seed {
		seed[i] = byte(i)
	}

	provider.SetSeed(seed)

	if !provider.IsSet() {
		t.Error("seed should be set")
	}

	retrieved := provider.Seed()
	if retrieved != seed {
		t.Error("retrieved seed does not match set seed")
	}
}

func TestSeedProvider_SetSeedFromHex(t *testing.T) {
	provider := NewSeedProvider()

	hexStr := "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"
	err := provider.SetSeedFromHex(hexStr)
	if err != nil {
		t.Fatalf("failed to set seed from hex: %v", err)
	}

	if !provider.IsSet() {
		t.Error("seed should be set")
	}

	retrievedHex := provider.SeedHex()
	if retrievedHex != hexStr {
		t.Errorf("retrieved hex %q does not match %q", retrievedHex, hexStr)
	}
}

func TestSeedProvider_SetSeedFromHex_Invalid(t *testing.T) {
	provider := NewSeedProvider()

	// Invalid hex
	err := provider.SetSeedFromHex("invalid")
	if err == nil {
		t.Error("expected error for invalid hex")
	}

	// Wrong length
	err = provider.SetSeedFromHex("0102")
	if err == nil {
		t.Error("expected error for wrong length")
	}
}

func TestSeedProvider_GenerateSeed(t *testing.T) {
	provider := NewSeedProvider()

	err := provider.GenerateSeed()
	if err != nil {
		t.Fatalf("failed to generate seed: %v", err)
	}

	if !provider.IsSet() {
		t.Error("seed should be set after generation")
	}

	seed := provider.Seed()
	// Check that seed is not all zeros
	var zeroSeed [32]byte
	if seed == zeroSeed {
		t.Error("generated seed should not be all zeros")
	}
}

func TestSeedProvider_EnableDisable(t *testing.T) {
	provider := NewSeedProvider()

	if provider.IsEnabled() {
		t.Error("provider should be disabled by default")
	}

	provider.Enable()
	if !provider.IsEnabled() {
		t.Error("provider should be enabled after Enable()")
	}

	provider.Disable()
	if provider.IsEnabled() {
		t.Error("provider should be disabled after Disable()")
	}
}

func TestSeedProvider_Int63(t *testing.T) {
	provider := NewSeedProvider()

	var seed [32]byte
	for i := range seed {
		seed[i] = byte(i)
	}
	provider.SetSeed(seed)

	val := provider.Int63()
	if val < 0 {
		t.Error("Int63 should return non-negative value")
	}

	// Test determinism - same seed should produce same value
	val2 := provider.Int63()
	if val != val2 {
		t.Error("Int63 should be deterministic for same seed")
	}
}

func TestGlobalSeed(t *testing.T) {
	// Reset global state
	DisableDeterministicMode()

	var seed [32]byte
	for i := range seed {
		seed[i] = byte(i)
	}

	SetGlobalSeed(seed)

	if !GlobalSeed().IsSet() {
		t.Error("global seed should be set")
	}

	EnableDeterministicMode()
	if !GlobalSeed().(*DefaultSeedProvider).IsEnabled() {
		t.Error("global deterministic mode should be enabled")
	}
}

func TestSeedHex_Roundtrip(t *testing.T) {
	provider := NewSeedProvider()

	var seed [32]byte
	for i := range seed {
		seed[i] = byte(i % 256)
	}

	provider.SetSeed(seed)
	hexStr := provider.SeedHex()

	provider2 := NewSeedProvider()
	err := provider2.SetSeedFromHex(hexStr)
	if err != nil {
		t.Fatalf("failed to set seed from hex: %v", err)
	}

	if provider.Seed() != provider2.Seed() {
		t.Error("roundthrough hex conversion failed")
	}
}
