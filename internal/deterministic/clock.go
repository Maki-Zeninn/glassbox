// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package deterministic

import (
	"sync"
	"time"
)

// ClockProvider is an interface for providing time values.
type ClockProvider interface {
	// Now returns the current time.
	Now() time.Time
	// Since returns the time elapsed since t.
	Since(t time.Time) time.Duration
	// Unix returns the current Unix timestamp.
	Unix() int64
	// SetFixedTime sets a fixed time to return (for deterministic mode).
	SetFixedTime(t time.Time)
	// ClearFixedTime clears the fixed time and returns to real time.
	ClearFixedTime()
	// IsFixed returns true if a fixed time is set.
	IsFixed() bool
}

// DefaultClockProvider provides a clock with optional fixed time override.
type DefaultClockProvider struct {
	fixedTime  *time.Time
	mu         sync.RWMutex
	offset     time.Duration
	startTime  time.Time
}

// NewClockProvider creates a new clock provider.
func NewClockProvider() *DefaultClockProvider {
	return &DefaultClockProvider{
		startTime: time.Now(),
	}
}

// Now returns the current time, or fixed time if set.
func (c *DefaultClockProvider) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.fixedTime != nil {
		return *c.fixedTime
	}
	return time.Now().Add(c.offset)
}

// Since returns the time elapsed since t.
func (c *DefaultClockProvider) Since(t time.Time) time.Duration {
	return c.Now().Sub(t)
}

// Unix returns the current Unix timestamp.
func (c *DefaultClockProvider) Unix() int64 {
	return c.Now().Unix()
}

// SetFixedTime sets a fixed time to return.
func (c *DefaultClockProvider) SetFixedTime(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fixedTime = &t
}

// ClearFixedTime clears the fixed time.
func (c *DefaultClockProvider) ClearFixedTime() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fixedTime = nil
}

// IsFixed returns true if a fixed time is set.
func (c *DefaultClockProvider) IsFixed() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.fixedTime != nil
}

// SetOffset sets a time offset from real time.
func (c *DefaultClockProvider) SetOffset(offset time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.offset = offset
}

// Advance advances the fixed time by the given duration.
func (c *DefaultClockProvider) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.fixedTime != nil {
		*c.fixedTime = c.fixedTime.Add(d)
	} else {
		c.offset += d
	}
}

// Global clock provider instance
var globalClock = NewClockProvider()

// GlobalClock returns the global clock provider.
func GlobalClock() ClockProvider {
	return globalClock
}

// SetGlobalFixedTime sets a fixed time globally.
func SetGlobalFixedTime(t time.Time) {
	globalClock.SetFixedTime(t)
}

// ClearGlobalFixedTime clears the global fixed time.
func ClearGlobalFixedTime() {
	globalClock.ClearFixedTime()
}

// Now returns the current time using the global clock.
func Now() time.Time {
	return globalClock.Now()
}

// Unix returns the current Unix timestamp using the global clock.
func Unix() int64 {
	return globalClock.Unix()
}
