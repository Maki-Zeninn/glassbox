// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package secutil

import "testing"

func TestSecureBytes_ZeroClearsMemory(t *testing.T) {
	sb, err := NewSecureBytes(32)
	if err != nil {
		t.Fatalf("NewSecureBytes: %v", err)
	}
	src := make([]byte, 32)
	for i := range src {
		src[i] = byte(i + 1)
	}
	sb.CopyFrom(src)

	got := sb.Bytes()
	if got == nil {
		t.Fatal("Bytes() returned nil before Zero")
	}
	for i := range src {
		if got[i] != src[i] {
			t.Fatalf("byte[%d]=%d, want %d", i, got[i], src[i])
		}
	}

	if err := sb.Zero(); err != nil {
		t.Fatalf("Zero: %v", err)
	}
	// After Zero the backing array must contain only zeros. We inspect via the
	// still-allocated slice reference captured before Zero cleared the flag.
	for i, b := range got {
		if b != 0 {
			t.Errorf("byte[%d]=%d after Zero, want 0", i, b)
		}
	}
	if sb.Bytes() != nil {
		t.Error("Bytes() must return nil after Zero")
	}
}

func TestSecureBytes_DoubleZero(t *testing.T) {
	sb, err := NewSecureBytes(8)
	if err != nil {
		t.Fatalf("NewSecureBytes: %v", err)
	}
	sb.CopyFrom([]byte("deadbeef"))
	if err := sb.Zero(); err != nil {
		t.Fatalf("first Zero: %v", err)
	}
	if err := sb.Zero(); err != nil {
		t.Fatalf("second Zero: %v", err)
	}
}

func TestNewSecureBytes_InvalidSize(t *testing.T) {
	_, err := NewSecureBytes(-1)
	if err == nil {
		t.Fatal("expected error for negative size")
	}
}

func TestMlockStatus_NonEmpty(t *testing.T) {
	status := MlockStatus()
	if status == "" {
		t.Fatal("MlockStatus must return a non-empty string")
	}
}
