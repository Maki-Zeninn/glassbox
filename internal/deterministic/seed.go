// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package deterministic

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"sync"
)

// SeedProvider is an interface for providing deterministic seeds.
type SeedProvider interface {
	// Seed returns the current seed value.
	Seed() [32]byte
	// SetSeed sets a new seed value.
	SetSeed(seed [32]byte)
	// GenerateSeed generates a new random seed if none is set.
	GenerateSeed() error
	// IsSet returns true if a seed has been explicitly set.
	IsSet() bool
}

// DefaultSeedProvider provides a thread-safe seed with optional override.
type DefaultSeedProvider struct {
	seed     [32]byte
	seedSet  bool
	mu       sync.RWMutex
	override [32]byte
	enabled  bool
}

// NewSeedProvider creates a new seed provider.
func NewSeedProvider() *DefaultSeedProvider {
	return &DefaultSeedProvider{
		enabled: false, // Disabled by default for production security
	}
}

// Seed returns the current seed value.
func (p *DefaultSeedProvider) Seed() [32]byte {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.enabled && p.seedSet {
		return p.seed
	}
	// Return zero seed if not enabled or set
	return [32]byte{}
}

// SetSeed sets a new seed value.
func (p *DefaultSeedProvider) SetSeed(seed [32]byte) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.seed = seed
	p.seedSet = true
	p.enabled = true
}

// SetSeedFromHex sets a seed from a hex string.
func (p *DefaultSeedProvider) SetSeedFromHex(hexStr string) error {
	seed, err := hex.DecodeString(hexStr)
	if err != nil {
		return fmt.Errorf("invalid hex seed: %w", err)
	}
	if len(seed) != 32 {
		return errors.New("seed must be exactly 32 bytes")
	}

	var seedArr [32]byte
	copy(seedArr[:], seed)
	p.SetSeed(seedArr)
	return nil
}

// GenerateSeed generates a new random seed.
func (p *DefaultSeedProvider) GenerateSeed() error {
	var seed [32]byte
	_, err := rand.Read(seed[:])
	if err != nil {
		return fmt.Errorf("failed to generate seed: %w", err)
	}

	p.SetSeed(seed)
	return nil
}

// IsSet returns true if a seed has been explicitly set.
func (p *DefaultSeedProvider) IsSet() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.seedSet
}

// Enable enables the seed provider (opt-in for deterministic mode).
func (p *DefaultSeedProvider) Enable() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.enabled = true
}

// Disable disables the seed provider (returns to non-deterministic mode).
func (p *DefaultSeedProvider) Disable() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.enabled = false
}

// IsEnabled returns true if deterministic mode is enabled.
func (p *DefaultSeedProvider) IsEnabled() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.enabled
}

// SeedHex returns the seed as a hex string.
func (p *DefaultSeedProvider) SeedHex() string {
	seed := p.Seed()
	return hex.EncodeToString(seed[:])
}

// Int63 returns a non-negative int63 from the seed.
// This is deterministic when a seed is set.
func (p *DefaultSeedProvider) Int63() int64 {
	seed := p.Seed()
	// Simple deterministic conversion from seed to int63
	// In production, this would use a proper seeded PRNG
	var bigInt big.Int
	bigInt.SetBytes(seed[:])
	return bigInt.Int64() & 0x7FFFFFFFFFFFFFFF
}

// Global seed provider instance
var globalSeed = NewSeedProvider()

// GlobalSeed returns the global seed provider.
func GlobalSeed() SeedProvider {
	return globalSeed
}

// SetGlobalSeed sets the global seed.
func SetGlobalSeed(seed [32]byte) {
	globalSeed.SetSeed(seed)
}

// SetGlobalSeedFromHex sets the global seed from a hex string.
func SetGlobalSeedFromHex(hexStr string) error {
	return globalSeed.(*DefaultSeedProvider).SetSeedFromHex(hexStr)
}

// EnableDeterministicMode enables deterministic mode globally.
func EnableDeterministicMode() {
	globalSeed.(*DefaultSeedProvider).Enable()
}

// DisableDeterministicMode disables deterministic mode globally.
func DisableDeterministicMode() {
	globalSeed.(*DefaultSeedProvider).Disable()
}
