// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

//go:build go1.18
// +build go1.18

package demangle

import (
	"fmt"
	"strings"
	"testing"
)

// FuzzSafeDemangleSymbol tests SafeDemangleSymbol with malformed and oversized inputs
func FuzzSafeDemangleSymbol(f *testing.F) {
	// Add seed inputs for valid symbols
	f.Add([]byte("_ZN11my_contract6invoke17h1a2b3c4d5e6f7890E"))
	f.Add([]byte("_ZN11soroban_sdk3log17habcdef1234567890E"))
	f.Add([]byte("_ZN11my_contract6client4call17hdeadbeef1234E"))
	f.Add([]byte("_ZN11my_contract6invokeE"))
	f.Add([]byte("my_contract::invoke"))
	f.Add([]byte("transfer"))
	f.Add([]byte(""))
	
	// Add seed inputs for malformed symbols
	f.Add([]byte("_ZNinvalid"))
	f.Add([]byte("_ZN"))
	f.Add([]byte("_ZNabc6invokeE"))
	f.Add([]byte("_ZN<script>alert(1)</script>6invokeE"))
	f.Add([]byte("_ZN\x00\x01\x02invalid6invokeE"))
	
	// Add seed inputs for oversized inputs
	oversized := make([]byte, MaxInputLength+100)
	copy(oversized, "_ZN")
	for i := 3; i < len(oversized)-1; i++ {
		oversized[i] = 'a'
	}
	oversized[len(oversized)-1] = 'E'
	f.Add(oversized)
	
	// Add seed inputs with special characters
	f.Add([]byte("_ZN<script>alert(1)</script>6invokeE"))
	f.Add([]byte("_ZN\n\t\r6invokeE"))
	f.Add([]byte("_ZN<script>6invokeE"))
	f.Add([]byte("_ZN&<>'\"6invokeE"))
	
	f.Fuzz(func(t *testing.T, data []byte) {
		// Convert to string for demangling
		input := string(data)
		
		// This should never panic
		result := SafeDemangleSymbol(input)
		
		// Basic validation of results
		if result.Error != nil {
			// If there was an error, we should have a fallback label
			if result.Demangled == input {
				t.Errorf("Error occurred but no fallback label provided for input: %q", truncateString(input, 50))
			}
		}
		
		// Output should not contain dangerous characters
		if containsDangerousChars(result.Demangled) {
			t.Errorf("Output contains dangerous characters: %q", result.Demangled)
		}
		
		// Output should be reasonably bounded
		if len(result.Demangled) > MaxOutputLength+100 {
			t.Errorf("Output exceeds reasonable bounds: %d bytes", len(result.Demangled))
		}
	})
}

// FuzzSafeDemangleLegacy tests legacy demangling with malformed inputs
func FuzzSafeDemangleLegacy(f *testing.F) {
	// Add seed inputs for valid legacy symbols
	f.Add([]byte("_ZN11my_contract6invoke17h1a2b3c4d5e6f7890E"))
	f.Add([]byte("_ZN11my_contract6client4call17hdeadbeef1234E"))
	f.Add([]byte("_ZN11my_contract6invokeE"))
	
	// Add seed inputs for malformed legacy symbols
	f.Add([]byte("_ZNinvalid"))
	f.Add([]byte("_ZN"))
	f.Add([]byte("_ZNabc6invokeE"))
	f.Add([]byte("_ZN<script>alert(1)</script>6invokeE"))
	f.Add([]byte("_ZN\x00\x01\x02invalid6invokeE"))
	
	// Add seed inputs with invalid length prefixes
	f.Add([]byte("_ZN9999999999999999999999999999invokeE"))
	f.Add([]byte("_ZN-1invokeE"))
	f.Add([]byte("_ZNinvokeE"))
	
	f.Fuzz(func(t *testing.T, data []byte) {
		input := string(data)
		
		// Test through the public SafeDemangleSymbol interface
		// This should never panic
		result := SafeDemangleSymbol(input)
		
		// For legacy symbols, errors are expected for malformed input, but no panics
		_ = result.Error
	})
}

// FuzzSafeDemangleV0 tests v0 demangling with malformed inputs
func FuzzSafeDemangleV0(f *testing.F) {
	// Add seed inputs for valid v0 symbols
	f.Add([]byte("_RNvCs1234abcd_11my_contract6invoke"))
	f.Add([]byte("_RNvCsomething_11my_contract6invoke"))
	
	// Add seed inputs for malformed v0 symbols
	f.Add([]byte("_R"))
	f.Add([]byte("_RNv\x00\x01\x02invalid"))
	f.Add([]byte("_RNv<script>alert(1)</script>"))
	
	// Add seed inputs with excessive structural markers
	oversized := make([]byte, MaxInputLength+50)
	copy(oversized, "_RNv")
	for i := 4; i < len(oversized); i++ {
		oversized[i] = byte('0' + (i % 10))
	}
	f.Add(oversized)
	
	f.Fuzz(func(t *testing.T, data []byte) {
		input := string(data)
		
		// Test through the public SafeDemangleSymbol interface
		// This should never panic
		result := SafeDemangleSymbol(input)
		
		// For v0 symbols, errors are expected for malformed input, but no panics
		_ = result.Error
	})
}

// FuzzSafeParseLengthPrefixed tests length-prefixed parsing with malformed inputs
func FuzzSafeParseLengthPrefixed(f *testing.F) {
	// Add seed inputs for valid length-prefixed strings
	f.Add([]byte("11my_contract6invoke"))
	f.Add([]byte("11my_contract6client4call"))
	f.Add([]byte("11my_contract6invoke"))
	
	// Add seed inputs for malformed length-prefixed strings
	f.Add([]byte("invalid"))
	f.Add([]byte("9999999999999999999999999999invoke"))
	f.Add([]byte("-1invoke"))
	f.Add([]byte("invoke"))
	f.Add([]byte("\x00\x01\x02"))
	
	// Add seed inputs with excessive segments
	var builder []byte
	for i := 0; i < MaxPathSegments+5; i++ {
		segment := "a"
		builder = append(builder, []byte(fmt.Sprintf("%d%s", len(segment), segment))...)
	}
	f.Add(builder)
	
	f.Fuzz(func(t *testing.T, data []byte) {
		input := string(data)
		
		// Test through a legacy symbol wrapper
		testInput := "_ZN" + input + "E"
		
		// This should never panic
		result := SafeDemangleSymbol(testInput)
		
		// Errors are expected for malformed input, but no panics
		_ = result.Error
	})
}

// FuzzSafeParseV0Path tests v0 path parsing with malformed inputs
func FuzzSafeParseV0Path(f *testing.F) {
	// Add seed inputs for valid v0 paths
	f.Add([]byte("Cs1234abcd_11my_contract6invoke"))
	f.Add([]byte("Csomething_11my_contract6invoke"))
	
	// Add seed inputs for malformed v0 paths
	f.Add([]byte("\x00\x01\x02"))
	f.Add([]byte("<script>alert(1)</script>"))
	f.Add([]byte("normal_text"))
	
	// Add seed inputs with excessive length
	oversized := make([]byte, MaxInputLength+50)
	for i := 0; i < len(oversized); i++ {
		oversized[i] = byte('a' + (i % 26))
	}
	f.Add(oversized)
	
	f.Fuzz(func(t *testing.T, data []byte) {
		input := string(data)
		
		// Test through a v0 symbol wrapper
		testInput := "_R" + input
		
		// This should never panic
		result := SafeDemangleSymbol(testInput)
		
		// Errors are expected for malformed input, but no panics
		_ = result.Error
	})
}

// FuzzCreateFallbackLabel tests fallback label generation with various inputs
func FuzzCreateFallbackLabel(f *testing.F) {
	// Add seed inputs
	f.Add([]byte("_ZN11my_contract6invokeE"))
	f.Add([]byte("_ZN<script>alert(1)</script>6invokeE"))
	f.Add([]byte(""))
	f.Add([]byte("\x00\x01\x02"))
	f.Add([]byte("normal_symbol"))
	f.Add([]byte("symbol_with_special_chars_!@#$%^&*()"))
	
	f.Fuzz(func(t *testing.T, data []byte) {
		input := string(data)
		
		// This should never panic
		fallback := createFallbackLabel(input, "test_reason")
		
		// Fallback should be safe
		if containsDangerousChars(fallback) {
			t.Errorf("Fallback contains dangerous characters: %q", fallback)
		}
		
		// Fallback should be reasonably bounded
		if len(fallback) > 100 {
			t.Errorf("Fallback exceeds reasonable bounds: %d bytes", len(fallback))
		}
	})
}

// FuzzSanitizeForDisplay tests sanitization with various inputs
func FuzzSanitizeForDisplay(f *testing.F) {
	// Add seed inputs
	f.Add([]byte("normal_symbol"))
	f.Add([]byte("<script>alert(1)</script>"))
	f.Add([]byte("\x00\x01\x02"))
	f.Add([]byte("symbol_with_special_chars_!@#$%^&*()"))
	f.Add([]byte("symbol\nwith\nnewlines"))
	f.Add([]byte("symbol\twith\ttabs"))
	
	f.Fuzz(func(t *testing.T, data []byte) {
		input := string(data)
		
		// This should never panic
		sanitized := sanitizeForDisplay(input)
		
		// Sanitized output should not contain dangerous characters
		if containsDangerousChars(sanitized) {
			t.Errorf("Sanitized output contains dangerous characters: %q", sanitized)
		}
	})
}

// FuzzEscapeForHTML tests HTML escaping with various inputs
func FuzzEscapeForHTML(f *testing.F) {
	// Add seed inputs
	f.Add([]byte("normal_symbol"))
	f.Add([]byte("<script>alert(1)</script>"))
	f.Add([]byte("&<>'\""))
	f.Add([]byte("\x00\x01\x02"))
	
	f.Fuzz(func(t *testing.T, data []byte) {
		input := string(data)
		
		// This should never panic
		escaped := EscapeForHTML(input)
		
		// Escaped output should not contain raw HTML tags
		if strings.Contains(escaped, "<script>") || strings.Contains(escaped, "</script>") {
			t.Errorf("HTML escaping failed for input: %q", input)
		}
	})
}

// FuzzEscapeForJSON tests JSON escaping with various inputs
func FuzzEscapeForJSON(f *testing.F) {
	// Add seed inputs
	f.Add([]byte("normal_symbol"))
	f.Add([]byte(`test"quote"`))
	f.Add([]byte(`test\backslash`))
	f.Add([]byte("test\nwith\nnewlines"))
	f.Add([]byte("\x00\x01\x02"))
	
	f.Fuzz(func(t *testing.T, data []byte) {
		input := string(data)
		
		// This should never panic
		escaped := EscapeForJSON(input)
		
		// Escaped output should not contain unescaped control characters
		for i := 0; i < len(escaped); i++ {
			if escaped[i] < 0x20 && escaped[i] != '\n' && escaped[i] != '\r' && escaped[i] != '\t' {
				t.Errorf("JSON escaping failed for control character at position %d", i)
			}
		}
	})
}

// Helper function to check for dangerous characters
func containsDangerousChars(s string) bool {
	dangerousPatterns := []string{
		"<script", "</script>", "javascript:", "onerror=", "onload=",
		"\x00", "\x01", "\x02", "\x03", "\x04", "\x05",
	}
	
	for _, pattern := range dangerousPatterns {
		if strings.Contains(s, pattern) {
			return true
		}
	}
	
	return false
}
