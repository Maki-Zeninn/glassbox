// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package secutil

import "testing"

func TestMemzero(t *testing.T) {
	data := []byte{1, 2, 3, 4, 5}
	Memzero(data)
	for i, b := range data {
		if b != 0 {
			t.Errorf("index %d: got %d, want 0", i, b)
		}
	}
}

func TestMemzero_EmptySlice(t *testing.T) {
	Memzero([]byte{})
}

func TestMemzero_NilSlice(t *testing.T) {
	Memzero(nil)
}

func TestMemzeroRetainsLength(t *testing.T) {
	data := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	Memzero(data)
	if len(data) != 4 {
		t.Errorf("slice length changed: got %d, want 4", len(data))
	}
}

// BenchmarkMemzero_1KB measures the cost of zeroing a 1 KB buffer.
// A sudden drop in ns/op (≈10× faster than baseline) would indicate the
// compiler is eliding the zeroing loop despite runtime.KeepAlive, which
// would leave sensitive key material in memory — a security regression.
func BenchmarkMemzero_1KB(b *testing.B) {
	buf := make([]byte, 1024)
	for i := range buf {
		buf[i] = 0xff
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Memzero(buf)
	}
}
