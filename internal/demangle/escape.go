// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package demangle

import (
	"fmt"
	"html"
	"strings"
)

// EscapeForHTML escapes a symbol for safe HTML rendering
func EscapeForHTML(s string) string {
	// First sanitize the symbol to remove dangerous characters
	sanitized := sanitizeForDisplay(s)
	
	// Then escape for HTML
	return html.EscapeString(sanitized)
}

// EscapeForMarkdown escapes a symbol for safe Markdown rendering
func EscapeForMarkdown(s string) string {
	// Sanitize first
	sanitized := sanitizeForDisplay(s)
	
	// Escape Markdown special characters
	sanitized = strings.ReplaceAll(sanitized, "_", "\\_")
	sanitized = strings.ReplaceAll(sanitized, "*", "\\*")
	sanitized = strings.ReplaceAll(sanitized, "`", "\\`")
	sanitized = strings.ReplaceAll(sanitized, "[", "\\[")
	sanitized = strings.ReplaceAll(sanitized, "]", "\\]")
	sanitized = strings.ReplaceAll(sanitized, "(", "\\(")
	sanitized = strings.ReplaceAll(sanitized, ")", "\\)")
	sanitized = strings.ReplaceAll(sanitized, "#", "\\#")
	sanitized = strings.ReplaceAll(sanitized, "+", "\\+")
	sanitized = strings.ReplaceAll(sanitized, "-", "\\-")
	sanitized = strings.ReplaceAll(sanitized, ".", "\\.")
	sanitized = strings.ReplaceAll(sanitized, "!", "\\!")
	
	return sanitized
}

// EscapeForText escapes a symbol for safe plain text rendering
func EscapeForText(s string) string {
	// For plain text, just sanitize
	return sanitizeForDisplay(s)
}

// EscapeForJSON escapes a symbol for safe JSON rendering
func EscapeForJSON(s string) string {
	// Sanitize first
	sanitized := sanitizeForDisplay(s)
	
	// JSON strings need escaping for quotes, backslashes, and control characters
	var builder strings.Builder
	builder.Grow(len(sanitized) * 2) // Pre-allocate for worst case
	
	for _, r := range sanitized {
		switch r {
		case '"':
			builder.WriteString(`\"`)
		case '\\':
			builder.WriteString(`\\`)
		case '\b':
			builder.WriteString(`\b`)
		case '\f':
			builder.WriteString(`\f`)
		case '\n':
			builder.WriteString(`\n`)
		case '\r':
			builder.WriteString(`\r`)
		case '\t':
			builder.WriteString(`\t`)
		default:
			if r < 0x20 {
				// Control characters
				builder.WriteString(fmt.Sprintf("\\u%04x", uint16(r)))
			} else {
				builder.WriteRune(r)
			}
		}
	}
	
	return builder.String()
}

// SafeDemangleForHTML demangles a symbol and escapes it for HTML
func SafeDemangleForHTML(mangled string) string {
	result := SafeDemangleSymbol(mangled)
	return EscapeForHTML(result.Demangled)
}

// SafeDemangleForMarkdown demangles a symbol and escapes it for Markdown
func SafeDemangleForMarkdown(mangled string) string {
	result := SafeDemangleSymbol(mangled)
	return EscapeForMarkdown(result.Demangled)
}

// SafeDemangleForText demangles a symbol and escapes it for plain text
func SafeDemangleForText(mangled string) string {
	result := SafeDemangleSymbol(mangled)
	return EscapeForText(result.Demangled)
}

// SafeDemangleForJSON demangles a symbol and escapes it for JSON
func SafeDemangleForJSON(mangled string) string {
	result := SafeDemangleSymbol(mangled)
	return EscapeForJSON(result.Demangled)
}
