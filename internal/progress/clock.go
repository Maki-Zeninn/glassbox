// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package progress

import "time"

// Clock is an abstraction over time that allows tests to inject deterministic
// timestamps.  Production code uses RealClock.
type Clock interface {
	// Now returns the current time.
	Now() time.Time
}

// RealClock delegates to time.Now.
type RealClock struct{}

// Now returns the current wall-clock time in UTC.
func (RealClock) Now() time.Time { return time.Now().UTC() }

// FakeClock is a monotonically advancing clock suitable for unit tests.
// Each call to Now advances the clock by Step.
type FakeClock struct {
	// Current is the time returned by the first call to Now.
	Current time.Time
	// Step is the amount the clock advances after each call to Now.
	// Defaults to one millisecond if zero.
	Step time.Duration
}

// NewFakeClock returns a FakeClock starting at t.
func NewFakeClock(t time.Time) *FakeClock {
	return &FakeClock{
		Current: t,
		Step:    time.Millisecond,
	}
}

// Now returns the current fake time and then advances by Step.
func (f *FakeClock) Now() time.Time {
	step := f.Step
	if step == 0 {
		step = time.Millisecond
	}
	t := f.Current
	f.Current = f.Current.Add(step)
	return t
}

// Advance moves the clock forward by d without returning a time.
func (f *FakeClock) Advance(d time.Duration) {
	f.Current = f.Current.Add(d)
}
