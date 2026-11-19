// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

//go:build go1.18
// +build go1.18

package fuzz

import (
	"testing"

	"github.com/dotandev/glassbox/internal/simulator"
)

// FuzzFootprintDecode is the package-fuzz entry point that forwards to the
// simulator decoder. Prefer the simulator-local target in CI; this exists so
// `go test -fuzz=FuzzFootprintDecode ./internal/fuzz` also works [Issue #1114].
func FuzzFootprintDecode(f *testing.F) {
	f.Add([]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
	f.Add([]byte{0xFF, 0xFF, 0x00, 0x00})
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = simulator.DecodeFootprintBytes(data)
	})
}
