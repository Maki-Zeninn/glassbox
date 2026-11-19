// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package demangle

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Constants for bounded parsing
const (
	// MaxInputLength is the maximum allowed length for a mangled symbol input
	MaxInputLength = 4096

	// MaxOutputLength is the maximum allowed length for a demangled symbol output
	MaxOutputLength = 1024

	// MaxPathSegments is the maximum number of path segments allowed in a demangled name
	MaxPathSegments = 20

	// MaxSegmentLength is the maximum allowed length for a single path segment
	MaxSegmentLength = 256
)

// DemangleError represents a structured error from demangling operations
type DemangleError struct {
	Input      string // Original input that caused the error
	Reason     string // Human-readable reason for the failure
	Scheme     string // The mangling scheme that was attempted
	Recovered  string // Fallback label that was used instead
	InputSize  int    // Size of the input in bytes
	OutputSize int    // Size of the attempted output in bytes
}

// Error implements the error interface
func (e *DemangleError) Error() string {
	return fmt.Sprintf("demangle error: %s (scheme: %s, input size: %d)", e.Reason, e.Scheme, e.InputSize)
}

// DemangleResult contains the result of a demangling operation
type DemangleResult struct {
	Demangled string // The demangled output
	Original  string // The original input
	Error     *DemangleError // Error if demangling failed
	Truncated bool   // Whether the output was truncated due to length limits
}

// SafeDemangleSymbol safely demangles a symbol with bounded parsing and error handling
func SafeDemangleSymbol(mangled string) DemangleResult {
	if mangled == "" {
		return DemangleResult{Demangled: "", Original: ""}
	}

	// Check input length bounds
	if len(mangled) > MaxInputLength {
		fallback := createFallbackLabel(mangled, "input_too_long")
		return DemangleResult{
			Demangled: fallback,
			Original:  mangled,
			Error: &DemangleError{
				Input:      truncateString(mangled, 100),
				Reason:     "input exceeds maximum length",
				Scheme:     "unknown",
				Recovered:  fallback,
				InputSize:  len(mangled),
				OutputSize: len(fallback),
			},
		}
	}

	var demangled string
	var scheme string
	var err error

	// Try each demangling scheme
	if strings.HasPrefix(mangled, "_R") {
		scheme = "rust_v0"
		demangled, err = safeDemangleV0(mangled)
	} else if strings.HasPrefix(mangled, "_ZN") {
		scheme = "rust_legacy"
		demangled, err = safeDemangleLegacy(mangled)
	} else {
		// Unknown scheme - return as-is
		return DemangleResult{
			Demangled: mangled,
			Original:  mangled,
		}
	}

	// Handle demangling errors
	if err != nil {
		fallback := createFallbackLabel(mangled, "demangle_error")
		return DemangleResult{
			Demangled: fallback,
			Original:  mangled,
			Error: &DemangleError{
				Input:      truncateString(mangled, 100),
				Reason:     err.Error(),
				Scheme:     scheme,
				Recovered:  fallback,
				InputSize:  len(mangled),
				OutputSize: len(fallback),
			},
		}
	}

	// Check output length bounds
	if len(demangled) > MaxOutputLength {
		fallback := createFallbackLabel(mangled, "output_too_long")
		return DemangleResult{
			Demangled: fallback,
			Original:  mangled,
			Truncated: true,
			Error: &DemangleError{
				Input:      truncateString(mangled, 100),
				Reason:     "output exceeds maximum length",
				Scheme:     scheme,
				Recovered:  fallback,
				InputSize:  len(mangled),
				OutputSize: len(demangled),
			},
		}
	}

	return DemangleResult{
		Demangled: demangled,
		Original:  mangled,
	}
}

// SafeDemangleTrace safely demangles a trace string with bounded parsing
func SafeDemangleTrace(trace string, table SymbolTable) string {
	if len(table) == 0 {
		return trace
	}

	return reWASMFuncID.ReplaceAllStringFunc(trace, func(match string) string {
		var idx uint32
		if _, err := fmt.Sscanf(match, "func[%d]", &idx); err != nil {
			return match
		}

		mangled, ok := table[idx]
		if !ok {
			return match
		}

		result := SafeDemangleSymbol(mangled)
		return result.Demangled
	})
}

// safeDemangleLegacy safely demangles legacy Rust symbols with bounds checking
func safeDemangleLegacy(sym string) (string, error) {
	if len(sym) < 4 {
		return sym, fmt.Errorf("symbol too short for legacy format")
	}

	inner := strings.TrimPrefix(sym, "_ZN")
	if !strings.HasSuffix(sym, "E") {
		return sym, fmt.Errorf("missing terminator E")
	}
	inner = strings.TrimSuffix(inner, "E")

	parts, err := safeParseLengthPrefixed(inner)
	if err != nil {
		return sym, err
	}

	if len(parts) == 0 {
		return sym, fmt.Errorf("no valid path segments found")
	}

	// Drop rustc hash suffix
	if len(parts) > 1 && isHashSuffix(parts[len(parts)-1]) {
		parts = parts[:len(parts)-1]
	}

	// Check path segment count
	if len(parts) > MaxPathSegments {
		return sym, fmt.Errorf("too many path segments: %d", len(parts))
	}

	// Validate each segment
	for i, part := range parts {
		if len(part) > MaxSegmentLength {
			return sym, fmt.Errorf("segment %d too long: %d bytes", i, len(part))
		}
		if !isValidIdentifier(part) {
			return sym, fmt.Errorf("segment %d contains invalid characters", i)
		}
	}

	demangled := strings.Join(parts, "::")
	if len(demangled) > MaxOutputLength {
		return sym, fmt.Errorf("demangled output too long: %d bytes", len(demangled))
	}

	return demangled, nil
}

// safeDemangleV0 safely demangles v0 Rust symbols with bounds checking
func safeDemangleV0(sym string) (string, error) {
	if len(sym) < 2 {
		return sym, fmt.Errorf("symbol too short for v0 format")
	}

	inner := strings.TrimPrefix(sym, "_R")
	parts, err := safeParseV0Path(inner)
	if err != nil {
		return sym, err
	}

	if len(parts) == 0 {
		return sym, fmt.Errorf("no valid path segments found")
	}

	// Check path segment count
	if len(parts) > MaxPathSegments {
		return sym, fmt.Errorf("too many path segments: %d", len(parts))
	}

	// Validate each segment
	for i, part := range parts {
		if len(part) > MaxSegmentLength {
			return sym, fmt.Errorf("segment %d too long: %d bytes", i, len(part))
		}
		if !isValidIdentifier(part) {
			return sym, fmt.Errorf("segment %d contains invalid characters", i)
		}
	}

	demangled := strings.Join(parts, "::")
	if len(demangled) > MaxOutputLength {
		return sym, fmt.Errorf("demangled output too long: %d bytes", len(demangled))
	}

	return demangled, nil
}

// safeParseLengthPrefixed safely parses length-prefixed strings with bounds checking
func safeParseLengthPrefixed(s string) ([]string, error) {
	var parts []string
	totalProcessed := 0

	for len(s) > 0 && totalProcessed < MaxInputLength {
		// Prevent infinite loops
		if len(parts) > MaxPathSegments {
			return parts, fmt.Errorf("too many segments")
		}

		n := 0
		i := 0
		digitCount := 0

		// Parse length prefix with bounds
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			// Prevent integer overflow
			if n > (MaxSegmentLength/10) {
				return parts, fmt.Errorf("length prefix too large")
			}
			n = n*10 + int(s[i]-'0')
			i++
			digitCount++
			
			// Sanity check on digit count
			if digitCount > 10 {
				return parts, fmt.Errorf("length prefix has too many digits")
			}
		}

		if i == 0 {
			// No digits found, stop parsing
			break
		}

		// Validate length
		if n <= 0 || n > MaxSegmentLength {
			return parts, fmt.Errorf("invalid segment length: %d", n)
		}

		if i+n > len(s) {
			return parts, fmt.Errorf("segment length exceeds remaining input")
		}

		parts = append(parts, s[i:i+n])
		s = s[i+n:]
		totalProcessed += i + n
	}

	return parts, nil
}

// safeParseV0Path safely parses v0 path with bounds checking
func safeParseV0Path(s string) ([]string, error) {
	// Sanitize input with bounds
	if len(s) > MaxInputLength {
		return nil, fmt.Errorf("input too long for v0 parsing")
	}

	cleaned := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			return r
		}
		return -1
	}, s)

	if len(cleaned) > MaxInputLength {
		return nil, fmt.Errorf("cleaned input too long")
	}

	return safeParseLengthPrefixed(cleaned)
}

// createFallbackLabel creates a safe fallback label for demangling failures
func createFallbackLabel(original string, reason string) string {
	// Truncate original if needed
	truncated := truncateString(original, 50)
	
	// Sanitize for safe display
	safe := sanitizeForDisplay(truncated)
	
	if safe == "" {
		return fmt.Sprintf("<malformed_symbol_%s>", reason)
	}
	
	return fmt.Sprintf("<%s:%s>", safe, reason)
}

// truncateString safely truncates a string to a maximum length
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	
	// Truncate at rune boundary
	for i := maxLen; i > 0; i-- {
		if utf8.RuneStart(s[i]) {
			return s[:i] + "..."
		}
	}
	
	return "..."
}

// isValidIdentifier checks if a string is a valid identifier
func isValidIdentifier(s string) bool {
	if s == "" {
		return false
	}
	
	for i, r := range s {
		if i == 0 {
			// First character must be letter or underscore
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_') {
				return false
			}
		} else {
			// Subsequent characters can be letters, digits, or underscores
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_') {
				return false
			}
		}
	}
	
	return true
}

// sanitizeForDisplay sanitizes a string for safe display in reports/HTML
func sanitizeForDisplay(s string) string {
	// Remove potentially dangerous characters
	s = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == ':' {
			return r
		}
		return -1
	}, s)
	
	return s
}

// DemangleSymbol provides backward-compatible interface that returns just the string
func DemangleSymbol(mangled string) string {
	result := SafeDemangleSymbol(mangled)
	return result.Demangled
}

// DemangleTrace provides backward-compatible interface that returns just the string
func DemangleTrace(trace string, table SymbolTable) string {
	return SafeDemangleTrace(trace, table)
}
