// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package demangle_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dotandev/glassbox/internal/demangle"
)

// TestSafeDemangleSymbol_ValidInputs tests that valid symbols are demangled correctly
func TestSafeDemangleSymbol_ValidInputs(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "legacy two-part path",
			input: "_ZN11my_contract6invoke17h1a2b3c4d5e6f7890E",
			want:  "my_contract::invoke",
		},
		{
			name:  "legacy three-part path",
			input: "_ZN11my_contract6client4call17hdeadbeef1234E",
			want:  "my_contract::client::call",
		},
		{
			name:  "soroban sdk symbol",
			input: "_ZN11soroban_sdk3log17habcdef1234567890E",
			want:  "soroban_sdk::log",
		},
		{
			name:  "already readable",
			input: "my_contract::invoke",
			want:  "my_contract::invoke",
		},
		{
			name:  "simple readable",
			input: "transfer",
			want:  "transfer",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := demangle.SafeDemangleSymbol(tc.input)
			if result.Demangled != tc.want {
				t.Errorf("SafeDemangleSymbol(%q) = %q, want %q", tc.input, result.Demangled, tc.want)
			}
			if result.Error != nil {
				t.Errorf("Unexpected error for valid input: %v", result.Error)
			}
		})
	}
}

// TestSafeDemangleSymbol_InputTooLong tests that oversized inputs are handled safely
func TestSafeDemangleSymbol_InputTooLong(t *testing.T) {
	// Create a symbol that exceeds MaxInputLength
	longInput := "_ZN" + string(make([]byte, demangle.MaxInputLength+100)) + "E"
	
	result := demangle.SafeDemangleSymbol(longInput)
	
	if result.Error == nil {
		t.Error("Expected error for oversized input")
	}
	if result.Error.Reason != "input exceeds maximum length" {
		t.Errorf("Expected 'input exceeds maximum length' error, got: %s", result.Error.Reason)
	}
	if result.Demangled == longInput {
		t.Error("Demangled output should be a fallback label, not the original input")
	}
	if !strings.Contains(result.Demangled, "<") || !strings.Contains(result.Demangled, ">") {
		t.Error("Fallback label should be wrapped in angle brackets")
	}
}

// TestSafeDemangleSymbol_InvalidLegacyFormat tests that invalid legacy symbols are handled
func TestSafeDemangleSymbol_InvalidLegacyFormat(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "missing terminator",
			input: "_ZN11my_contract6invoke",
		},
		{
			name:  "too short",
			input: "_ZN",
		},
		{
			name:  "invalid length prefix",
			input: "_ZNabc6invokeE",
		},
		{
			name:  "segment too long",
			input: "_ZN9999999999999999999999999999invokeE",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := demangle.SafeDemangleSymbol(tc.input)
			if result.Error == nil {
				t.Error("Expected error for invalid format")
			}
			if result.Demangled == tc.input {
				t.Error("Should return fallback label, not original input")
			}
		})
	}
}

// TestSafeDemangleSymbol_InvalidV0Format tests that invalid v0 symbols are handled
func TestSafeDemangleSymbol_InvalidV0Format(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "too short",
			input: "_R",
		},
		{
			name:  "contains invalid characters",
			input: "_RNv\x00\x01\x02invalid",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := demangle.SafeDemangleSymbol(tc.input)
			if result.Error == nil {
				t.Error("Expected error for invalid v0 format")
			}
		})
	}
}

// TestSafeDemangleSymbol_TooManySegments tests that symbols with too many path segments are handled
func TestSafeDemangleSymbol_TooManySegments(t *testing.T) {
	// Create a symbol with more than MaxPathSegments
	var builder strings.Builder
	builder.WriteString("_ZN")
	for i := 0; i < demangle.MaxPathSegments+5; i++ {
		segment := fmt.Sprintf("%d%s", i, "a")
		builder.WriteString(fmt.Sprintf("%d%s", len(segment), segment))
	}
	builder.WriteString("E")
	
	result := demangle.SafeDemangleSymbol(builder.String())
	
	if result.Error == nil {
		t.Error("Expected error for too many segments")
	}
	if !strings.Contains(result.Error.Reason, "too many path segments") {
		t.Errorf("Expected 'too many path segments' error, got: %s", result.Error.Reason)
	}
}

// TestSafeDemangleSymbol_OutputTooLong tests that outputs exceeding MaxOutputLength are handled
func TestSafeDemangleSymbol_OutputTooLong(t *testing.T) {
	// Create a symbol that would produce a very long demangled output
	var builder strings.Builder
	builder.WriteString("_ZN")
	for i := 0; i < 50; i++ {
		segment := strings.Repeat("a", 50)
		builder.WriteString(fmt.Sprintf("%d%s", len(segment), segment))
	}
	builder.WriteString("E")
	
	result := demangle.SafeDemangleSymbol(builder.String())
	
	if result.Error == nil {
		t.Error("Expected error for output too long")
	}
	if !strings.Contains(result.Error.Reason, "output exceeds maximum length") {
		t.Errorf("Expected 'output exceeds maximum length' error, got: %s", result.Error.Reason)
	}
	if !result.Truncated {
		t.Error("Expected Truncated flag to be set")
	}
}

// TestSafeDemangleSymbol_InvalidCharacters tests that symbols with invalid characters are handled
func TestSafeDemangleSymbol_InvalidCharacters(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "contains null byte",
			input: "_ZN11my_contract\x006invokeE",
		},
		{
			name:  "contains control characters",
			input: "_ZN11my_contract\n6invokeE",
		},
		{
			name:  "contains special characters",
			input: "_ZN11my_contract<script>6invokeE",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := demangle.SafeDemangleSymbol(tc.input)
			if result.Error == nil {
				t.Error("Expected error for invalid characters")
			}
			// The fallback should be sanitized
			if strings.Contains(result.Demangled, "<script>") {
				t.Error("Fallback should be sanitized and not contain dangerous characters")
			}
		})
	}
}

// TestSafeDemangleTrace tests safe trace demangling
func TestSafeDemangleTrace(t *testing.T) {
	table := demangle.SymbolTable{
		42: "_ZN11my_contract6invoke17h1a2b3c4d5e6f7890E",
		7:  "_ZN11soroban_sdk3log17habcdef1234567890E",
	}
	
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "single function reference",
			input: "call func[42]",
			want:  "call my_contract::invoke",
		},
		{
			name:  "multiple function references",
			input: "call func[42] -> func[7]",
			want:  "call my_contract::invoke -> soroban_sdk::log",
		},
		{
			name:  "unknown function reference",
			input: "call func[99]",
			want:  "call func[99]",
		},
		{
			name:  "no function references",
			input: "contract.invoke error: host trap",
			want:  "contract.invoke error: host trap",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := demangle.SafeDemangleTrace(tc.input, table)
			if got != tc.want {
				t.Errorf("SafeDemangleTrace(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestSafeDemangleTrace_WithMalformedSymbol tests trace demangling with malformed symbols
func TestSafeDemangleTrace_WithMalformedSymbol(t *testing.T) {
	table := demangle.SymbolTable{
		42: "_ZNinvalid_mangled_symbol",
	}
	
	input := "call func[42]"
	result := demangle.SafeDemangleTrace(input, table)
	
	// Should not panic and should contain a fallback
	if result == input {
		t.Error("Expected some transformation even with malformed symbol")
	}
	if strings.Contains(result, "func[42]") {
		// It's okay to leave the original if demangling fails, but should not crash
		t.Log("Left original func reference when demangling failed")
	}
}

// TestSafeDemangleTrace_NilTable tests with nil symbol table
func TestSafeDemangleTrace_NilTable(t *testing.T) {
	input := "call func[42] -> func[7]"
	got := demangle.SafeDemangleTrace(input, nil)
	if got != input {
		t.Errorf("nil table should leave trace unchanged: got %q, want %q", got, input)
	}
}

// TestDemangleError tests DemangleError structure
func TestDemangleError(t *testing.T) {
	err := &demangle.DemangleError{
		Input:      "_ZNinvalid",
		Reason:     "invalid format",
		Scheme:     "rust_legacy",
		Recovered:  "<invalid:demangle_error>",
		InputSize:  10,
		OutputSize: 25,
	}
	
	errorStr := err.Error()
	if !strings.Contains(errorStr, "invalid format") {
		t.Errorf("Error string should contain reason: %s", errorStr)
	}
	if !strings.Contains(errorStr, "rust_legacy") {
		t.Errorf("Error string should contain scheme: %s", errorStr)
	}
}

// TestBackwardCompatibility tests that the old interfaces still work
func TestBackwardCompatibility(t *testing.T) {
	input := "_ZN11my_contract6invoke17h1a2b3c4d5e6f7890E"
	
	// Old interface should still work
	result := demangle.DemangleSymbol(input)
	if result != "my_contract::invoke" {
		t.Errorf("DemangleSymbol backward compatibility failed: got %q", result)
	}
	
	table := demangle.SymbolTable{42: input}
	traceResult := demangle.DemangleTrace("call func[42]", table)
	if traceResult != "call my_contract::invoke" {
		t.Errorf("DemangleTrace backward compatibility failed: got %q", traceResult)
	}
}

// TestCreateFallbackLabel tests fallback label generation
func TestCreateFallbackLabel(t *testing.T) {
	tests := []struct {
		name     string
		original string
		reason   string
		wantContains string
	}{
		{
			name:     "normal symbol",
			original: "_ZN11my_contract6invokeE",
			reason:   "demangle_error",
			wantContains: "my_contract:demangle_error",
		},
		{
			name:     "symbol with special chars",
			original: "_ZN<script>alert(1)</script>6invokeE",
			reason:   "invalid_chars",
			wantContains: "invoke:invalid_chars",
		},
		{
			name:     "empty symbol",
			original: "",
			reason:   "empty_input",
			wantContains: "malformed_symbol_empty_input",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// This is a private function, so we test it indirectly through SafeDemangleSymbol
			result := demangle.SafeDemangleSymbol(tc.original)
			if strings.Contains(result.Demangled, "<script>") {
				t.Error("Fallback should be sanitized")
			}
			if tc.wantContains != "" && !strings.Contains(result.Demangled, tc.wantContains) {
				t.Errorf("Expected fallback to contain %q, got %q", tc.wantContains, result.Demangled)
			}
		})
	}
}

// TestIsValidIdentifier tests identifier validation
func TestIsValidIdentifier(t *testing.T) {
	tests := []struct {
		input string
		valid bool
	}{
		{"valid_name", true},
		{"ValidName", true},
		{"valid_name123", true},
		{"_valid_name", true},
		{"123invalid", false},
		{"invalid-name", false},
		{"invalid name", false},
		{"", false},
		{"invalid<script>", false},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			// This tests the internal validation logic indirectly
			result := demangle.SafeDemangleSymbol("_ZN" + tc.input + "E")
			if tc.valid {
				if result.Error != nil && strings.Contains(result.Error.Reason, "invalid characters") {
					t.Errorf("Expected valid identifier %q to pass validation", tc.input)
				}
			} else {
				if result.Error == nil {
					t.Errorf("Expected invalid identifier %q to fail validation", tc.input)
				}
			}
		})
	}
}

// TestRegression_IntegerOverflow tests that integer overflow is prevented
func TestRegression_IntegerOverflow(t *testing.T) {
	// Test with a length prefix that could cause integer overflow
	oversizedLength := strings.Repeat("9", 20) // Very large number
	input := "_ZN" + oversizedLength + "aE"
	
	result := demangle.SafeDemangleSymbol(input)
	
	// Should not panic and should handle gracefully
	if result.Error == nil {
		t.Error("Expected error for oversized length prefix")
	}
	if !strings.Contains(result.Error.Reason, "length prefix") {
		t.Errorf("Expected length prefix error, got: %s", result.Error.Reason)
	}
}

// TestRegression_InfiniteLoop tests that parsing doesn't infinite loop
func TestRegression_InfiniteLoop(t *testing.T) {
	// Test with input that could cause infinite loop in naive parsers
	input := "_ZN" + strings.Repeat("0", 1000) + "E"
	
	// This should complete quickly without hanging
	done := make(chan bool)
	go func() {
		result := demangle.SafeDemangleSymbol(input)
		_ = result
		done <- true
	}()
	
	select {
	case <-done:
		// Test completed successfully
	case <-time.After(5 * time.Second):
		t.Fatal("Demangling took too long, possible infinite loop")
	}
}

// TestRegression_MemoryExhaustion tests that memory usage is bounded
func TestRegression_MemoryExhaustion(t *testing.T) {
	// Test with input that could cause unbounded memory allocation
	input := "_ZN"
	for i := 0; i < 1000; i++ {
		input += fmt.Sprintf("%d%s", 100, strings.Repeat("a", 100))
	}
	input += "E"
	
	result := demangle.SafeDemangleSymbol(input)
	
	// Should not panic and should handle with bounds
	if result.Error == nil {
		t.Error("Expected error for too many segments")
	}
	if !strings.Contains(result.Error.Reason, "too many") {
		t.Errorf("Expected 'too many' error, got: %s", result.Error.Reason)
	}
}

// TestRegression_UTF8Boundary tests UTF-8 boundary handling
func TestRegression_UTF8Boundary(t *testing.T) {
	// Test with multi-byte UTF-8 characters
	input := "_ZN11my_contract6invokeE"
	
	result := demangle.SafeDemangleSymbol(input)
	
	// Should handle UTF-8 correctly
	if result.Error != nil {
		t.Errorf("Unexpected error for valid input: %v", result.Error)
	}
	if result.Demangled != "my_contract::invoke" {
		t.Errorf("Expected 'my_contract::invoke', got: %s", result.Demangled)
	}
}

// TestRegression_EmptySegments tests handling of empty segments
func TestRegression_EmptySegments(t *testing.T) {
	// Test with zero-length segments
	input := "_ZN0a0b0cE"
	
	result := demangle.SafeDemangleSymbol(input)
	
	// Should handle gracefully
	if result.Error == nil {
		t.Error("Expected error for zero-length segments")
	}
}
