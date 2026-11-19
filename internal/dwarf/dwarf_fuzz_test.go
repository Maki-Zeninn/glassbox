// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

//go:build go1.18

package dwarf

import "testing"

// FuzzNewParser fuzzes DWARF binary format parsing with arbitrary byte sequences.
//
// Security boundary: NewParser accepts untrusted WASM, ELF, Mach-O, and PE
// binaries that may arrive from a Soroban RPC node or be uploaded by users.
// Any panic, hang, or excessive allocation is a bug regardless of the error
// value returned.
//
// Seed corpus covers magic bytes for each supported format, truncated headers,
// garbage bytes, and crafted section-length overflows.
func FuzzNewParser(f *testing.F) {
	// WASM magic + version (minimal valid header).
	f.Add([]byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00})
	// ELF64 LE magic (16-byte ident).
	f.Add([]byte{0x7f, 0x45, 0x4c, 0x46, 0x02, 0x01, 0x01, 0x00, 0, 0, 0, 0, 0, 0, 0, 0})
	// Mach-O 64-bit little-endian magic.
	f.Add([]byte{0xcf, 0xfa, 0xed, 0xfe, 0x07, 0x00, 0x00, 0x01})
	// PE (MZ) header.
	f.Add([]byte{0x4d, 0x5a, 0x90, 0x00, 0x03, 0x00, 0x00, 0x00})
	// Empty input.
	f.Add([]byte{})
	// Garbage bytes (no recognisable magic).
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
	// WASM magic only — truncated before version field.
	f.Add([]byte{0x00, 0x61, 0x73, 0x6d})
	// WASM magic + version + custom section with overflowed ULEB128 length.
	f.Add([]byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x00,                                     // section id = 0 (custom)
		0xff, 0xff, 0xff, 0xff, 0x0f,            // ULEB128: 0x7FFFFFFF (overlong)
	})
	// Single null byte.
	f.Add([]byte{0x00})

	f.Fuzz(func(t *testing.T, data []byte) {
		parser, err := NewParser(data)
		if err != nil {
			return
		}
		// Calling public API on a successfully-parsed result must not panic.
		_, _ = parser.GetSubprograms()
	})
}

// FuzzGetSourceLocation fuzzes source location lookup with arbitrary binary
// data and program-counter addresses.
//
// GetSourceLocation is called on every Soroban trap to map a wasm PC to a
// Rust source line. It must not panic or hang on crafted DWARF content.
func FuzzGetSourceLocation(f *testing.F) {
	wasmMagic := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	f.Add(wasmMagic, uint64(0))
	f.Add(wasmMagic, uint64(0xffffffffffffffff))
	f.Add(wasmMagic, uint64(0x1000))
	f.Add([]byte{}, uint64(0))
	f.Add([]byte{0xff, 0xfe, 0xfd}, uint64(42))

	f.Fuzz(func(t *testing.T, data []byte, addr uint64) {
		parser, err := NewParser(data)
		if err != nil {
			return
		}
		_, _ = parser.GetSourceLocation(addr)
		_, _ = parser.FindLocalVarsAt(addr)
		_, _ = parser.FindSubprogramAt(addr)
	})
}
