// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package deterministic

import (
	"encoding/hex"
	"testing"
)

func TestDeterministicReplay_SameSeedSameOutput(t *testing.T) {
	// Test that the same seed produces deterministic output
	provider := NewSeedProvider()

	seedHex := "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"
	err := provider.SetSeedFromHex(seedHex)
	if err != nil {
		t.Fatalf("failed to set seed: %v", err)
	}

	provider.Enable()

	// Generate multiple values and verify they're deterministic
	values := make([]int64, 10)
	for i := range values {
		values[i] = provider.Int63()
	}

	// Reset and regenerate with same seed
	provider2 := NewSeedProvider()
	provider2.SetSeedFromHex(seedHex)
	provider2.Enable()

	for i, expected := range values {
		actual := provider2.Int63()
		if actual != expected {
			t.Errorf("iteration %d: expected %d, got %d", i, expected, actual)
		}
	}
}

func TestDeterministicReplay_DifferentSeedDifferentOutput(t *testing.T) {
	provider1 := NewSeedProvider()
	seed1Hex := "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"
	provider1.SetSeedFromHex(seed1Hex)
	provider1.Enable()

	provider2 := NewSeedProvider()
	seed2Hex := "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f21"
	provider2.SetSeedFromHex(seed2Hex)
	provider2.Enable()

	// Even one bit difference should produce different output
	val1 := provider1.Int63()
	val2 := provider2.Int63()

	if val1 == val2 {
		t.Error("different seeds should produce different values")
	}
}

func TestDeterministicReplay_ClockDeterminism(t *testing.T) {
	clock := NewClockProvider()

	fixedTime := clock.Now()
	clock.SetFixedTime(fixedTime)

	// Multiple calls should return the same time
	for i := 0; i < 10; i++ {
		now := clock.Now()
		if !now.Equal(fixedTime) {
			t.Errorf("iteration %d: expected %v, got %v", i, fixedTime, now)
		}
	}

	// Advancing should change time deterministically
	clock.Advance(1000000000) // 1 second in nanoseconds
	expected := fixedTime.Add(1000000000)
	actual := clock.Now()

	if !actual.Equal(expected) {
		t.Errorf("after advance: expected %v, got %v", expected, actual)
	}
}

func TestDeterministicReplay_GlobalState(t *testing.T) {
	// Reset global state
	DisableDeterministicMode()

	seedHex := "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"
	err := SetGlobalSeedFromHex(seedHex)
	if err != nil {
		t.Fatalf("failed to set global seed: %v", err)
	}

	EnableDeterministicMode()

	// Get value from global seed
	val1 := GlobalSeed().Int63()

	// Reset and set same seed again
	DisableDeterministicMode()
	SetGlobalSeedFromHex(seedHex)
	EnableDeterministicMode()

	val2 := GlobalSeed().Int63()

	if val1 != val2 {
		t.Errorf("global seed not deterministic: %d != %d", val1, val2)
	}
}

func TestDeterministicReplay_SeedRecording(t *testing.T) {
	provider := NewSeedProvider()

	seedHex := "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"
	provider.SetSeedFromHex(seedHex)
	provider.Enable()

	// Verify seed can be retrieved as hex
	recordedHex := provider.SeedHex()
	if recordedHex != seedHex {
		t.Errorf("seed recording failed: expected %s, got %s", seedHex, recordedHex)
	}

	// Verify seed bytes match
	recordedSeed := provider.Seed()
	expectedSeed, _ := hex.DecodeString(seedHex)
	if len(recordedSeed) != len(expectedSeed) {
		t.Fatal("seed length mismatch")
	}
	for i := range recordedSeed {
		if recordedSeed[i] != expectedSeed[i] {
			t.Errorf("seed byte %d mismatch: expected %d, got %d", i, expectedSeed[i], recordedSeed[i])
		}
	}
}

func TestDeterministicReplay_DisabledModeNonDeterministic(t *testing.T) {
	provider := NewSeedProvider()

	// Without enabling deterministic mode, should return zero
	val := provider.Int63()
	if val != 0 {
		t.Errorf("disabled mode should return zero, got %d", val)
	}

	// Enable and set seed
	provider.Enable()
	seedHex := "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"
	provider.SetSeedFromHex(seedHex)

	val = provider.Int63()
	if val == 0 {
		t.Error("enabled mode with seed should return non-zero value")
	}

	// Disable again
	provider.Disable()
	val = provider.Int63()
	if val != 0 {
		t.Errorf("disabled mode after disable should return zero, got %d", val)
	}
}
