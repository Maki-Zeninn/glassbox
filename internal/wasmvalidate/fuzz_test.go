// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package wasmvalidate

import "testing"

// FuzzValidate exercises Validate with arbitrary bytes to ensure it never
// panics and never hangs, regardless of truncated or internally
// inconsistent section data.
func FuzzValidate(f *testing.F) {
	f.Add([]byte{})
	f.Add(wasmMagic)
	f.Add(buildModule(buildImportSection(1), buildFunctionSection(1), buildCodeSection(1)))
	f.Add(buildModule(buildFunctionSection(3), buildCodeSection(1))) // inconsistent counts
	f.Add(buildModule(buildCustomSection(".debug_info", 64)))
	f.Add(append(append([]byte{}, wasmMagic...), 0x01, 0xff, 0xff, 0x03))             // truncated section
	f.Add(append(append([]byte{}, wasmMagic...), 0x01, 0x80, 0x80, 0x80, 0x80, 0x80)) // malformed varint
	f.Add([]byte{0x00, 0x61, 0x73, 0x6d, 0x02, 0x00, 0x00, 0x00})                     // bad version

	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Validate panicked on input %x: %v", data, r)
			}
		}()
		_ = Validate(data, DefaultLimits())
	})
}
