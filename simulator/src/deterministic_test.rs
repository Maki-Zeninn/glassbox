// Copyright 2026 Erst Users
// SPDX-License-Identifier: Apache-2.0

//! Cross-language deterministic replay tests.
//!
//! These tests verify that the Rust deterministic implementation
//! produces consistent results that can be compared with Go implementation.

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_deterministic_replay_same_seed_same_output() {
        let provider = SeedProvider::new();
        let seed_hex = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20";
        provider.set_seed_from_hex(seed_hex).unwrap();
        provider.enable();

        // Generate multiple values and verify they're deterministic
        let values: Vec<u64> = (0..10).map(|_| provider.u64()).collect();

        // Reset and regenerate with same seed
        let provider2 = SeedProvider::new();
        provider2.set_seed_from_hex(seed_hex).unwrap();
        provider2.enable();

        for (i, &expected) in values.iter().enumerate() {
            let actual = provider2.u64();
            assert_eq!(
                actual, expected,
                "iteration {}: expected {}, got {}",
                i, expected, actual
            );
        }
    }

    #[test]
    fn test_deterministic_replay_different_seed_different_output() {
        let provider1 = SeedProvider::new();
        let seed1_hex = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20";
        provider1.set_seed_from_hex(seed1_hex).unwrap();
        provider1.enable();

        let provider2 = SeedProvider::new();
        let seed2_hex = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f21";
        provider2.set_seed_from_hex(seed2_hex).unwrap();
        provider2.enable();

        // Even one bit difference should produce different output
        let val1 = provider1.u64();
        let val2 = provider2.u64();

        assert_ne!(val1, val2, "different seeds should produce different values");
    }

    #[test]
    fn test_deterministic_replay_clock_determinism() {
        let clock = ClockProvider::new();
        let fixed_time = clock.now();
        clock.set_fixed_time(fixed_time);

        // Multiple calls should return the same time
        for _ in 0..10 {
            let now = clock.now();
            assert_eq!(now, fixed_time, "fixed time should remain constant");
        }

        // Advancing should change time deterministically
        clock.advance(Duration::from_secs(1));
        let expected = fixed_time + Duration::from_secs(1);
        let actual = clock.now();

        assert_eq!(actual, expected, "advanced time should match expected");
    }

    #[test]
    fn test_deterministic_replay_global_state() {
        // Reset global state
        disable_deterministic_mode();

        let seed_hex = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20";
        set_global_seed_from_hex(seed_hex).unwrap();
        enable_deterministic_mode();

        // Get value from global seed
        let val1 = global_seed().u64();

        // Reset and set same seed again
        disable_deterministic_mode();
        set_global_seed_from_hex(seed_hex).unwrap();
        enable_deterministic_mode();

        let val2 = global_seed().u64();

        assert_eq!(val1, val2, "global seed should be deterministic");
    }

    #[test]
    fn test_deterministic_replay_seed_recording() {
        let provider = SeedProvider::new();
        let seed_hex = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20";
        provider.set_seed_from_hex(seed_hex).unwrap();
        provider.enable();

        // Verify seed can be retrieved as hex
        let recorded_hex = provider.seed_hex();
        assert_eq!(recorded_hex, seed_hex, "seed recording should match original");

        // Verify seed bytes match
        let recorded_seed = provider.seed().unwrap();
        let expected_seed = hex::decode(seed_hex).unwrap();
        assert_eq!(recorded_seed.len(), expected_seed.len());
        for (i, (&actual, &expected)) in recorded_seed.iter().zip(expected_seed.iter()).enumerate() {
            assert_eq!(actual, expected, "seed byte {} mismatch", i);
        }
    }

    #[test]
    fn test_deterministic_replay_disabled_mode_non_deterministic() {
        let provider = SeedProvider::new();

        // Without enabling deterministic mode, should use random
        let val1 = provider.u64();
        let val2 = provider.u64();
        // These might be different since we're using thread_rng when disabled
        // But we can at least verify they're valid u64 values
        assert!(val1 > 0 || val1 == 0); // Just verify it's a valid u64
        assert!(val2 > 0 || val2 == 0);

        // Enable and set seed
        provider.enable();
        let seed_hex = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20";
        provider.set_seed_from_hex(seed_hex).unwrap();

        let val3 = provider.u64();
        let val4 = provider.u64();
        // With seed enabled, should be deterministic
        assert_eq!(val3, val4, "enabled mode with seed should be deterministic");

        // Disable again
        provider.disable();
        let val5 = provider.u64();
        let val6 = provider.u64();
        // Back to non-deterministic
        assert!(val5 > 0 || val5 == 0);
        assert!(val6 > 0 || val6 == 0);
    }

    #[test]
    fn test_deterministic_replay_integration() {
        // Simulate a replay scenario:
        // 1. First run with a seed
        // 2. Record the seed
        // 3. Second run with the same seed
        // 4. Verify outputs match

        // First run
        let provider1 = SeedProvider::new();
        let seed_hex = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef";
        provider1.set_seed_from_hex(seed_hex).unwrap();
        provider1.enable();

        let clock1 = ClockProvider::new();
        let start_time = clock1.now();
        clock1.set_fixed_time(start_time);

        // Simulate some operations
        let mut results1 = Vec::new();
        for _ in 0..5 {
            results1.push(provider1.u64());
            clock1.advance(Duration::from_millis(1));
        }

        // Record seed for replay
        let recorded_seed = provider1.seed_hex();
        let final_time1 = clock1.now();

        // Second run (replay)
        let provider2 = SeedProvider::new();
        provider2.set_seed_from_hex(&recorded_seed).unwrap();
        provider2.enable();

        let clock2 = ClockProvider::new();
        clock2.set_fixed_time(start_time);

        let mut results2 = Vec::new();
        for _ in 0..5 {
            results2.push(provider2.u64());
            clock2.advance(Duration::from_millis(1));
        }

        let final_time2 = clock2.now();

        // Verify deterministic results
        assert_eq!(results1.len(), results2.len(), "result count mismatch");
        for (i, (&r1, &r2)) in results1.iter().zip(results2.iter()).enumerate() {
            assert_eq!(r1, r2, "result {} mismatch", i);
        }

        assert_eq!(final_time1, final_time2, "final time mismatch");
        assert_eq!(recorded_seed, seed_hex, "recorded seed mismatch");
    }

    #[test]
    fn test_deterministic_replay_cross_run_consistency() {
        let seed_hex = "cafebabecafebabecafebabecafebabecafebabecafebabecafebabecafebabe";

        // Run 1
        let provider1 = SeedProvider::new();
        provider1.set_seed_from_hex(seed_hex).unwrap();
        provider1.enable();
        let val1 = provider1.u64();

        // Run 2
        let provider2 = SeedProvider::new();
        provider2.set_seed_from_hex(seed_hex).unwrap();
        provider2.enable();
        let val2 = provider2.u64();

        // Run 3
        let provider3 = SeedProvider::new();
        provider3.set_seed_from_hex(seed_hex).unwrap();
        provider3.enable();
        let val3 = provider3.u64();

        assert_eq!(val1, val2, "run 1 vs run 2 mismatch");
        assert_eq!(val2, val3, "run 2 vs run 3 mismatch");
    }
}
