// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package telemetry

import (
	"math/rand"
)

// Sampler limits event emission for high-frequency telemetry topics.
// A rate of 1.0 emits every event; 0.1 emits ~10% of events; 0.0 emits none.
type Sampler struct {
	rate float64
}

// NewSampler returns a Sampler with the given sample rate clamped to [0.0, 1.0].
func NewSampler(rate float64) *Sampler {
	if rate < 0 {
		rate = 0
	}
	if rate > 1 {
		rate = 1
	}
	return &Sampler{rate: rate}
}

// ShouldEmit returns true if the event should be emitted based on the sample rate.
// At rate 1.0 all events pass; at 0.0 none pass.
func (s *Sampler) ShouldEmit() bool {
	if s.rate >= 1.0 {
		return true
	}
	if s.rate <= 0.0 {
		return false
	}
	return rand.Float64() < s.rate //nolint:gosec // sampling does not require cryptographic randomness; math/rand statistical uniformity is sufficient
}

// Rate returns the configured sample rate, always in [0.0, 1.0].
// A rate of 1.0 emits all events; 0.0 emits none.
func (s *Sampler) Rate() float64 {
	return s.rate
}
