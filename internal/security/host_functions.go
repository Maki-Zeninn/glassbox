// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package security

// host_functions.go owns the Soroban host function allow/deny lists.
//
// The deprecated list previously lived as a package-private variable inside
// internal/cmd, which meant only `glassbox debug` could report a deprecated
// host call. Promoting it here lets the SARIF writer, the LSP diagnostics
// publisher and any future consumer share one definition instead of each
// re-deriving the list.

import "strings"

// HostFunctionStatus classifies a Soroban host function.
type HostFunctionStatus string

const (
	// HostFunctionSupported means the function is current and safe to use.
	HostFunctionSupported HostFunctionStatus = "supported"
	// HostFunctionDeprecated means the function still works but is scheduled
	// for removal in a future protocol release.
	HostFunctionDeprecated HostFunctionStatus = "deprecated"
	// HostFunctionUnsafe means the function is still permitted but its use is
	// unsafe without an authorization check.
	HostFunctionUnsafe HostFunctionStatus = "unsafe"
)

// DeprecatedHostFunctions lists the Soroban host functions that are scheduled
// for removal. Contracts calling these will stop working at protocol upgrade
// time, so they are reported at warning level.
var DeprecatedHostFunctions = []string{
	"bytes_copy_from_linear_memory",
	"bytes_copy_to_linear_memory",
	"bytes_new_from_linear_memory",
	"map_new_from_linear_memory",
	"map_unpack_to_linear_memory",
	"symbol_new_from_linear_memory",
	"string_new_from_linear_memory",
	"vec_new_from_linear_memory",
	"vec_unpack_to_linear_memory",
}

// UnsafeHostFunctions lists host functions that Glassbox still permits but
// whose use is unsafe without an explicit authorization check. They are
// reported at error level.
var UnsafeHostFunctions = []string{
	"storage_put",
	"contract_call",
}

// HostFunctionDescriptions explains each deprecated function's replacement so
// a developer gets an actionable message rather than just a name.
var HostFunctionDescriptions = map[string]string{
	"bytes_copy_from_linear_memory": "use bytes_new_from_linear_memory and copy the slice instead",
	"bytes_copy_to_linear_memory":   "use bytes_new_from_linear_memory and copy the slice instead",
	"bytes_new_from_linear_memory":  "use bytes_new_from_linear_memory_from_slice where available",
	"map_new_from_linear_memory":    "construct the map on the host and pass it as an argument",
	"map_unpack_to_linear_memory":   "construct the map on the host and pass it as an argument",
	"symbol_new_from_linear_memory": "use symbol_new_from_linear_memory with an explicit length",
	"string_new_from_linear_memory": "use string_new_from_linear_memory with an explicit length",
	"vec_new_from_linear_memory":    "construct the vector on the host and pass it as an argument",
	"vec_unpack_to_linear_memory":   "construct the vector on the host and pass it as an argument",
}

// HostFunctionStatusFor reports the classification of a single host function.
func HostFunctionStatusFor(name string) HostFunctionStatus {
	lower := strings.ToLower(name)
	for _, fn := range DeprecatedHostFunctions {
		if lower == fn {
			return HostFunctionDeprecated
		}
	}
	for _, fn := range UnsafeHostFunctions {
		if lower == fn {
			return HostFunctionUnsafe
		}
	}
	return HostFunctionSupported
}

// DeprecatedHostFunctionIn reports the first deprecated host function mentioned
// in text. Matching is case-insensitive substring matching, because the input
// is a diagnostic event's free-form data rather than a parsed symbol name.
func DeprecatedHostFunctionIn(text string) (string, bool) {
	return findInList(text, DeprecatedHostFunctions)
}

// UnsafeHostFunctionIn reports the first allowlisted-but-unsafe host function
// mentioned in text.
func UnsafeHostFunctionIn(text string) (string, bool) {
	return findInList(text, UnsafeHostFunctions)
}

func findInList(text string, names []string) (string, bool) {
	lower := strings.ToLower(text)
	for _, name := range names {
		if strings.Contains(lower, name) {
			return name, true
		}
	}
	return "", false
}
