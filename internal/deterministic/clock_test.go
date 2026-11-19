// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package deterministic

import (
	"testing"
	"time"
)

func TestClockProvider_Now(t *testing.T) {
	provider := NewClockProvider()

	now := provider.Now()
	if now.IsZero() {
		t.Error("Now() should return non-zero time")
	}
}

func TestClockProvider_FixedTime(t *testing.T) {
	provider := NewClockProvider()

	fixed := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	provider.SetFixedTime(fixed)

	if !provider.IsFixed() {
		t.Error("clock should be fixed")
	}

	now := provider.Now()
	if !now.Equal(fixed) {
		t.Errorf("expected %v, got %v", fixed, now)
	}

	provider.ClearFixedTime()
	if provider.IsFixed() {
		t.Error("clock should not be fixed after ClearFixedTime")
	}
}

func TestClockProvider_Since(t *testing.T) {
	provider := NewClockProvider()

	fixed := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	provider.SetFixedTime(fixed)

	past := time.Date(2024, 1, 1, 11, 0, 0, 0, time.UTC)
	duration := provider.Since(past)

	expected := time.Hour
	if duration != expected {
		t.Errorf("expected %v, got %v", expected, duration)
	}
}

func TestClockProvider_Unix(t *testing.T) {
	provider := NewClockProvider()

	fixed := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	provider.SetFixedTime(fixed)

	unix := provider.Unix()
	expected := int64(1704110400)
	if unix != expected {
		t.Errorf("expected %d, got %d", expected, unix)
	}
}

func TestClockProvider_Advance(t *testing.T) {
	provider := NewClockProvider()

	fixed := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	provider.SetFixedTime(fixed)

	provider.Advance(time.Hour)

	now := provider.Now()
	expected := time.Date(2024, 1, 1, 13, 0, 0, 0, time.UTC)
	if !now.Equal(expected) {
		t.Errorf("expected %v, got %v", expected, now)
	}
}

func TestClockProvider_Advance_WithoutFixed(t *testing.T) {
	provider := NewClockProvider()

	provider.Advance(time.Hour)

	// Should not panic, just adjust offset
	now := provider.Now()
	if now.IsZero() {
		t.Error("Now() should return non-zero time")
	}
}

func TestClockProvider_SetOffset(t *testing.T) {
	provider := NewClockProvider()

	provider.SetOffset(time.Hour)

	// Time should be approximately 1 hour in the future
	before := time.Now()
	now := provider.Now()
	after := time.Now()

	// now should be roughly before + 1 hour
	expectedMin := before.Add(time.Hour - time.Second)
	expectedMax := after.Add(time.Hour + time.Second)

	if now.Before(expectedMin) || now.After(expectedMax) {
		t.Errorf("time with offset not in expected range: got %v", now)
	}
}

func TestGlobalClock(t *testing.T) {
	// Reset global state
	ClearGlobalFixedTime()

	fixed := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	SetGlobalFixedTime(fixed)

	now := Now()
	if !now.Equal(fixed) {
		t.Errorf("expected %v, got %v", fixed, now)
	}

	unix := Unix()
	expected := int64(1704110400)
	if unix != expected {
		t.Errorf("expected %d, got %d", expected, unix)
	}

	ClearGlobalFixedTime()
}

func TestNow(t *testing.T) {
	// Test the convenience function
	ClearGlobalFixedTime()

	now := Now()
	if now.IsZero() {
		t.Error("Now() should return non-zero time")
	}
}

func TestUnix(t *testing.T) {
	// Test the convenience function
	ClearGlobalFixedTime()

	unix := Unix()
	if unix == 0 {
		t.Error("Unix() should return non-zero timestamp")
	}

	// Should be approximately current time
	expected := time.Now().Unix()
	if unix < expected-10 || unix > expected+10 {
		t.Errorf("Unix timestamp %d not close to current time %d", unix, expected)
	}
}
