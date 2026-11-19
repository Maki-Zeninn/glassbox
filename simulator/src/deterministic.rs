// Copyright 2026 Erst Users
// SPDX-License-Identifier: Apache-2.0

//! Deterministic seed and clock providers for reproducible simulation.
//!
//! This module provides abstractions for seeds and time that can be
//! overridden to enable deterministic replay across invocations.

use rand::Rng;
use std::sync::{Arc, RwLock};
use std::time::{Duration, SystemTime, UNIX_EPOCH};

/// Seed provider for deterministic random number generation.
#[derive(Debug, Clone)]
pub struct SeedProvider {
    seed: Arc<RwLock<Option<[u8; 32]>>>,
    enabled: Arc<RwLock<bool>>,
}

impl SeedProvider {
    /// Create a new seed provider.
    pub fn new() -> Self {
        Self {
            seed: Arc::new(RwLock::new(None)),
            enabled: Arc::new(RwLock::new(false)),
        }
    }

    /// Get the current seed value.
    pub fn seed(&self) -> Option<[u8; 32]> {
        let seed = self.seed.read().unwrap();
        *seed
    }

    /// Set a seed value.
    pub fn set_seed(&self, seed: [u8; 32]) {
        let mut s = self.seed.write().unwrap();
        *s = Some(seed);
        let mut enabled = self.enabled.write().unwrap();
        *enabled = true;
    }

    /// Set a seed from a hex string.
    pub fn set_seed_from_hex(&self, hex_str: &str) -> Result<(), String> {
        let seed = hex::decode(hex_str).map_err(|e| format!("Invalid hex: {}", e))?;
        if seed.len() != 32 {
            return Err("Seed must be exactly 32 bytes".to_string());
        }
        let mut seed_arr = [0u8; 32];
        seed_arr.copy_from_slice(&seed);
        self.set_seed(seed_arr);
        Ok(())
    }

    /// Generate a new random seed.
    pub fn generate_seed(&self) -> Result<(), String> {
        let mut seed = [0u8; 32];
        let mut rng = rand::thread_rng();
        rng.fill(&mut seed);
        self.set_seed(seed);
        Ok(())
    }

    /// Check if a seed has been set.
    pub fn is_set(&self) -> bool {
        let seed = self.seed.read().unwrap();
        seed.is_some()
    }

    /// Enable deterministic mode.
    pub fn enable(&self) {
        let mut enabled = self.enabled.write().unwrap();
        *enabled = true;
    }

    /// Disable deterministic mode.
    pub fn disable(&self) {
        let mut enabled = self.enabled.write().unwrap();
        *enabled = false;
    }

    /// Check if deterministic mode is enabled.
    pub fn is_enabled(&self) -> bool {
        let enabled = self.enabled.read().unwrap();
        *enabled
    }

    /// Get the seed as a hex string.
    pub fn seed_hex(&self) -> String {
        let seed = self.seed.read().unwrap();
        match *seed {
            Some(s) => hex::encode(s),
            None => String::new(),
        }
    }

    /// Get a deterministic u64 value from the seed.
    pub fn u64(&self) -> u64 {
        let seed = self.seed.read().unwrap();
        match *seed {
            Some(s) => {
                let mut arr = [0u8; 8];
                arr.copy_from_slice(&s[..8]);
                u64::from_le_bytes(arr)
            }
            None => rand::thread_rng().gen(),
        }
    }
}

impl Default for SeedProvider {
    fn default() -> Self {
        Self::new()
    }
}

/// Clock provider for deterministic time values.
#[derive(Debug, Clone)]
pub struct ClockProvider {
    fixed_time: Arc<RwLock<Option<SystemTime>>>,
    offset: Arc<RwLock<Duration>>,
}

impl ClockProvider {
    /// Create a new clock provider.
    pub fn new() -> Self {
        Self {
            fixed_time: Arc::new(RwLock::new(None)),
            offset: Arc::new(RwLock::new(Duration::ZERO)),
        }
    }

    /// Get the current time.
    pub fn now(&self) -> SystemTime {
        let fixed = self.fixed_time.read().unwrap();
        if let Some(t) = *fixed {
            return t;
        }
        let offset = self.offset.read().unwrap();
        SystemTime::now() + *offset
    }

    /// Get the time elapsed since the given time.
    pub fn since(&self, t: SystemTime) -> Duration {
        self.now()
            .duration_since(t)
            .unwrap_or(Duration::ZERO)
    }

    /// Get the current Unix timestamp.
    pub fn unix(&self) -> i64 {
        self.now()
            .duration_since(UNIX_EPOCH)
            .unwrap_or(Duration::ZERO)
            .as_secs() as i64
    }

    /// Set a fixed time.
    pub fn set_fixed_time(&self, t: SystemTime) {
        let mut fixed = self.fixed_time.write().unwrap();
        *fixed = Some(t);
    }

    /// Clear the fixed time.
    pub fn clear_fixed_time(&self) {
        let mut fixed = self.fixed_time.write().unwrap();
        *fixed = None;
    }

    /// Check if a fixed time is set.
    pub fn is_fixed(&self) -> bool {
        let fixed = self.fixed_time.read().unwrap();
        fixed.is_some()
    }

    /// Set a time offset from real time.
    pub fn set_offset(&self, offset: Duration) {
        let mut off = self.offset.write().unwrap();
        *off = offset;
    }

    /// Advance the time by the given duration.
    pub fn advance(&self, duration: Duration) {
        let mut fixed = self.fixed_time.write().unwrap();
        if let Some(ref mut t) = *fixed {
            *t += duration;
        } else {
            drop(fixed);
            let mut off = self.offset.write().unwrap();
            *off += duration;
        }
    }
}

impl Default for ClockProvider {
    fn default() -> Self {
        Self::new()
    }
}

/// Global seed provider instance.
static GLOBAL_SEED: once_cell::sync::Lazy<SeedProvider> =
    once_cell::sync::Lazy::new(SeedProvider::new);

/// Global clock provider instance.
static GLOBAL_CLOCK: once_cell::sync::Lazy<ClockProvider> =
    once_cell::sync::Lazy::new(ClockProvider::new);

/// Get the global seed provider.
pub fn global_seed() -> &'static SeedProvider {
    &GLOBAL_SEED
}

/// Set the global seed.
pub fn set_global_seed(seed: [u8; 32]) {
    global_seed().set_seed(seed);
}

/// Set the global seed from a hex string.
pub fn set_global_seed_from_hex(hex_str: &str) -> Result<(), String> {
    global_seed().set_seed_from_hex(hex_str)
}

/// Enable deterministic mode globally.
pub fn enable_deterministic_mode() {
    global_seed().enable();
}

/// Disable deterministic mode globally.
pub fn disable_deterministic_mode() {
    global_seed().disable();
}

/// Get the global clock provider.
pub fn global_clock() -> &'static ClockProvider {
    &GLOBAL_CLOCK
}

/// Set a fixed time globally.
pub fn set_global_fixed_time(t: SystemTime) {
    global_clock().set_fixed_time(t);
}

/// Clear the global fixed time.
pub fn clear_global_fixed_time() {
    global_clock().clear_fixed_time();
}

/// Get the current time using the global clock.
pub fn now() -> SystemTime {
    global_clock().now()
}

/// Get the current Unix timestamp using the global clock.
pub fn unix() -> i64 {
    global_clock().unix()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_seed_provider_set_seed() {
        let provider = SeedProvider::new();
        let seed = [1u8; 32];
        provider.set_seed(seed);

        assert!(provider.is_set());
        assert_eq!(provider.seed(), Some(seed));
    }

    #[test]
    fn test_seed_provider_set_seed_from_hex() {
        let provider = SeedProvider::new();
        let hex_str = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20";
        provider.set_seed_from_hex(hex_str).unwrap();

        assert!(provider.is_set());
        assert_eq!(provider.seed_hex(), hex_str);
    }

    #[test]
    fn test_seed_provider_set_seed_from_hex_invalid() {
        let provider = SeedProvider::new();

        assert!(provider.set_seed_from_hex("invalid").is_err());
        assert!(provider.set_seed_from_hex("0102").is_err());
    }

    #[test]
    fn test_seed_provider_generate_seed() {
        let provider = SeedProvider::new();
        provider.generate_seed().unwrap();

        assert!(provider.is_set());
        let seed = provider.seed().unwrap();
        assert_ne!(seed, [0u8; 32]);
    }

    #[test]
    fn test_seed_provider_enable_disable() {
        let provider = SeedProvider::new();

        assert!(!provider.is_enabled());
        provider.enable();
        assert!(provider.is_enabled());
        provider.disable();
        assert!(!provider.is_enabled());
    }

    #[test]
    fn test_seed_provider_u64() {
        let provider = SeedProvider::new();
        let seed = [1u8; 32];
        provider.set_seed(seed);

        let val1 = provider.u64();
        let val2 = provider.u64();
        assert_eq!(val1, val2); // Should be deterministic
    }

    #[test]
    fn test_clock_provider_now() {
        let provider = ClockProvider::new();
        let now = provider.now();
        assert!(now.duration_since(UNIX_EPOCH).is_ok());
    }

    #[test]
    fn test_clock_provider_fixed_time() {
        let provider = ClockProvider::new();
        let fixed = UNIX_EPOCH + Duration::from_secs(1704110400);
        provider.set_fixed_time(fixed);

        assert!(provider.is_fixed());
        assert_eq!(provider.now(), fixed);

        provider.clear_fixed_time();
        assert!(!provider.is_fixed());
    }

    #[test]
    fn test_clock_provider_since() {
        let provider = ClockProvider::new();
        let fixed = UNIX_EPOCH + Duration::from_secs(1704110400);
        provider.set_fixed_time(fixed);

        let past = UNIX_EPOCH + Duration::from_secs(1704106800); // 1 hour before
        let duration = provider.since(past);

        assert_eq!(duration, Duration::from_secs(3600));
    }

    #[test]
    fn test_clock_provider_unix() {
        let provider = ClockProvider::new();
        let fixed = UNIX_EPOCH + Duration::from_secs(1704110400);
        provider.set_fixed_time(fixed);

        assert_eq!(provider.unix(), 1704110400);
    }

    #[test]
    fn test_clock_provider_advance() {
        let provider = ClockProvider::new();
        let fixed = UNIX_EPOCH + Duration::from_secs(1704110400);
        provider.set_fixed_time(fixed);

        provider.advance(Duration::from_secs(3600));

        assert_eq!(
            provider.now(),
            UNIX_EPOCH + Duration::from_secs(1704114000)
        );
    }

    #[test]
    fn test_global_seed() {
        disable_deterministic_mode();
        let seed = [1u8; 32];
        set_global_seed(seed);

        assert!(global_seed().is_set());
        enable_deterministic_mode();
        assert!(global_seed().is_enabled());
    }

    #[test]
    fn test_global_clock() {
        clear_global_fixed_time();
        let fixed = UNIX_EPOCH + Duration::from_secs(1704110400);
        set_global_fixed_time(fixed);

        assert_eq!(now(), fixed);
        assert_eq!(unix(), 1704110400);

        clear_global_fixed_time();
    }
}
