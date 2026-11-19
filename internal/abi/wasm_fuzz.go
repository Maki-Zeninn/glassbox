// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

//go:build go1.18
// +build go1.18

package abi

import (
	"testing"
)

// FuzzValidateWasmMagic tests WASM magic byte validation with malformed input.
// This targets the ValidateWasmMagic function which checks WASM file headers.
func FuzzValidateWasmMagic(f *testing.F) {
	// Seed corpus with valid and edge-case inputs
	f.Add([]byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}) // Valid WASM magic
	f.Add([]byte{}) // Empty
	f.Add([]byte{0x00, 0x61, 0x73, 0x6d}) // Magic only (too short)
	f.Add([]byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00}) // Missing version byte
	f.Add([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0x01, 0x00, 0x00, 0x00}) // Wrong magic
	f.Add(make([]byte, 1000)) // Large input

	f.Fuzz(func(t *testing.T, data []byte) {
		err := ValidateWasmMagic(data, "test.wasm")
		_ = err // Expect errors for invalid WASM, but no panics
	})
}

// FuzzExtractCustomSection tests custom section extraction from WASM binaries.
// This targets the ExtractCustomSection function which parses WASM sections.
func FuzzExtractCustomSection(f *testing.F) {
	// Seed with minimal valid WASM structure
	validWasm := []byte{
		0x00, 0x61, 0x73, 0x6d, // Magic
		0x01, 0x00, 0x00, 0x00, // Version
	}
	f.Add(validWasm, "custom_section")
	f.Add([]byte{}, "custom_section") // Empty
	f.Add([]byte{0x00, 0x61, 0x73, 0x6d}, "custom_section") // Magic only
	f.Add(make([]byte, 100), "custom_section") // Larger input
	f.Add(validWasm, "") // Empty section name

	f.Fuzz(func(t *testing.T, wasmData []byte, sectionName string) {
		_, err := ExtractCustomSection(wasmData, sectionName)
		_ = err // Expect errors for malformed WASM, but no panics
	})
}

// FuzzAnalyzeWasmSize tests WASM size analysis with malformed binaries.
// This targets the AnalyzeWasmSize function which calculates WASM metrics.
func FuzzAnalyzeWasmSize(f *testing.F) {
	f.Add([]byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}) // Valid magic
	f.Add([]byte{}) // Empty
	f.Add(make([]byte, 500)) // Larger input

	f.Fuzz(func(t *testing.T, data []byte) {
		_, err := AnalyzeWasmSize(data)
		_ = err // Expect errors for malformed WASM, but no panics
	})
}

// FuzzParseWasmContractSpec tests WASM contract spec parsing.
func FuzzParseWasmContractSpec(f *testing.F) {
	f.Add([]byte(`{"name":"test","functions":[],"env":{}}`)) // Valid spec
	f.Add([]byte(`{}`)) // Empty object
	f.Add([]byte(`invalid`)) // Invalid JSON
	f.Add([]byte{}) // Empty

	f.Fuzz(func(t *testing.T, data []byte) {
		_, err := DecodeContractSpec(data)
		_ = err // Expect errors for malformed JSON, but no panics
	})
}
