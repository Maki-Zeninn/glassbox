// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package deterministic

import (
	"testing"
)

// TestDeterministicReplay_Integration tests the full deterministic replay workflow
func TestDeterministicReplay_Integration(t *testing.T) {
	// Simulate a replay scenario:
	// 1. First run with a seed
	// 2. Record the seed
	// 3. Second run with the same seed
	// 4. Verify outputs match

	// First run
	provider1 := NewSeedProvider()
	seedHex := "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	provider1.SetSeedFromHex(seedHex)
	provider1.Enable()

	clock1 := NewClockProvider()
	startTime := clock1.Now()
	clock1.SetFixedTime(startTime)

	// Simulate some operations
	var results1 []int64
	for i := 0; i < 5; i++ {
		results1 = append(results1, provider1.Int63())
		clock1.Advance(1000000) // 1ms
	}

	// Record seed for replay
	recordedSeed := provider1.SeedHex()
	finalTime1 := clock1.Now()

	// Second run (replay)
	provider2 := NewSeedProvider()
	provider2.SetSeedFromHex(recordedSeed)
	provider2.Enable()

	clock2 := NewClockProvider()
	clock2.SetFixedTime(startTime)

	var results2 []int64
	for i := 0; i < 5; i++ {
		results2 = append(results2, provider2.Int63())
		clock2.Advance(1000000) // 1ms
	}

	finalTime2 := clock2.Now()

	// Verify deterministic results
	if len(results1) != len(results2) {
		t.Fatalf("result count mismatch: %d vs %d", len(results1), len(results2))
	}

	for i := range results1 {
		if results1[i] != results2[i] {
			t.Errorf("result %d mismatch: %d vs %d", i, results1[i], results2[i])
		}
	}

	if !finalTime1.Equal(finalTime2) {
		t.Errorf("final time mismatch: %v vs %v", finalTime1, finalTime2)
	}

	// Verify seed matches
	if recordedSeed != seedHex {
		t.Errorf("recorded seed mismatch: %s vs %s", recordedSeed, seedHex)
	}
}

// TestDeterministicReplay_CrossRunConsistency tests that results are consistent across multiple runs
func TestDeterministicReplay_CrossRunConsistency(t *testing.T) {
	seedHex := "cafebabecafebabecafebabecafebabecafebabecafebabecafebabecafebabe"

	// Run 1
	provider1 := NewSeedProvider()
	provider1.SetSeedFromHex(seedHex)
	provider1.Enable()
	val1 := provider1.Int63()

	// Run 2
	provider2 := NewSeedProvider()
	provider2.SetSeedFromHex(seedHex)
	provider2.Enable()
	val2 := provider2.Int63()

	// Run 3
	provider3 := NewSeedProvider()
	provider3.SetSeedFromHex(seedHex)
	provider3.Enable()
	val3 := provider3.Int63()

	if val1 != val2 || val2 != val3 {
		t.Errorf("cross-run inconsistency: %d, %d, %d", val1, val2, val3)
	}
}
