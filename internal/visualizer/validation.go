// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package visualizer

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

// ValidationError describes a specific validation failure in a flamegraph.
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("flamegraph validation: %s: %s", e.Field, e.Message)
}

// ValidationResult holds the outcome of validating a flamegraph export.
type ValidationResult struct {
	Valid    bool               `json:"valid"`
	Errors   []ValidationError  `json:"errors,omitempty"`
	Warnings []string           `json:"warnings,omitempty"`
}

// ValidateSVG checks that the given SVG string is structurally valid for
// use as a flamegraph. It verifies:
//   - The string contains an <svg> element
//   - Required xmlns attribute is present
//   - Frame elements (rect, text, g) exist
//   - Labels are properly escaped
//   - The viewBox is present
//
// Returns a ValidationResult indicating pass/fail with specific error details.
func ValidateSVG(svg string) ValidationResult {
	result := ValidationResult{Valid: true}

	if svg == "" {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "svg",
			Message: "SVG content is empty — cannot validate an empty flamegraph",
		})
		return result
	}

	lower := strings.ToLower(svg)

	// Check for <svg> element
	if !strings.Contains(lower, "<svg") {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "svg_element",
			Message: "missing <svg> element — content does not appear to be SVG",
		})
		return result
	}

	// Check xmlns
	if !strings.Contains(svg, "xmlns") {
		result.Warnings = append(result.Warnings, "SVG is missing xmlns attribute — may not render in all viewers")
	}

	// Check viewBox
	if !strings.Contains(lower, "viewbox") && !strings.Contains(lower, "width=") {
		result.Warnings = append(result.Warnings, "SVG has no viewBox or width attribute — sizing may be unpredictable")
	}

	// Check for frame elements
	hasRect := strings.Contains(lower, "<rect")
	hasText := strings.Contains(lower, "<text")
	hasGroup := strings.Contains(lower, "<g")

	if !hasRect && !hasText {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "frames",
			Message: "SVG contains no <rect> or <text> elements — flamegraph has no visible frames",
		})
	}

	if !hasGroup {
		result.Warnings = append(result.Warnings, "SVG has no <g> group elements — frame grouping may be absent")
	}

	// Validate label escaping (check for unescaped HTML entities in text elements)
	if hasText {
		textRe := regexp.MustCompile(`(?i)<text[^>]*>([^<]*)</text>`)
		matches := textRe.FindAllStringSubmatch(svg, -1)
		for _, m := range matches {
			if len(m) > 1 {
				label := m[1]
				if containsUnescapedHTML(label) {
					result.Valid = false
					result.Errors = append(result.Errors, ValidationError{
						Field:   "label_escape",
						Message: fmt.Sprintf("label contains unescaped HTML: %q — labels must use HTML entity encoding", truncate(label, 80)),
					})
				}
			}
		}
	}

	return result
}

// ValidateHTML checks that the given HTML string is a valid interactive
// flamegraph export. It verifies:
//   - DOCTYPE declaration is present
//   - SVG is embedded within the HTML
//   - Interactive JavaScript features are present
//   - Dark mode support is included
//   - No external dependencies (standalone file)
func ValidateHTML(htmlContent string) ValidationResult {
	result := ValidationResult{Valid: true}

	if htmlContent == "" {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "html",
			Message: "HTML content is empty — cannot validate an empty flamegraph",
		})
		return result
	}

	lower := strings.ToLower(htmlContent)

	// Check DOCTYPE
	if !strings.Contains(lower, "<!doctype html>") && !strings.Contains(lower, "<!doctype html>") {
		result.Warnings = append(result.Warnings, "HTML is missing <!DOCTYPE html> declaration")
	}

	// Check SVG embedding
	if !strings.Contains(lower, "<svg") {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "svg_embedded",
			Message: "HTML does not contain an embedded <svg> element",
		})
		return result
	}

	// Check interactive features
	features := []struct {
		name    string
		pattern string
	}{
		{"hover", "mouseover"},
		{"click-to-zoom", "click"},
		{"search", "search"},
	}

	for _, feat := range features {
		if !strings.Contains(lower, feat.pattern) {
			result.Warnings = append(result.Warnings, fmt.Sprintf("missing interactive feature: %s", feat.name))
		}
	}

	// Check dark mode support
	if !strings.Contains(lower, "prefers-color-scheme") {
		result.Warnings = append(result.Warnings, "missing dark mode CSS media query support")
	}

	// Check for external dependencies
	if strings.Contains(htmlContent, "src=\"http") || strings.Contains(htmlContent, "href=\"http") {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "standalone",
			Message: "HTML references external resources — flamegraph must be self-contained",
		})
	}

	return result
}

// ValidateFlamegraph performs comprehensive validation on the given content
// based on the specified format.
func ValidateFlamegraph(content string, format ExportFormat) ValidationResult {
	switch format {
	case FormatSVG:
		return ValidateSVG(content)
	case FormatHTML:
		return ValidateHTML(content)
	default:
		return ValidationResult{
			Valid: false,
			Errors: []ValidationError{{
				Field:   "format",
				Message: fmt.Sprintf("unsupported export format: %s", format),
			}},
		}
	}
}

// EscapeLabel sanitizes a label string for safe embedding in SVG/HTML.
// It escapes HTML entities and truncates excessively long labels.
func EscapeLabel(label string) string {
	// Escape HTML entities
	escaped := html.EscapeString(label)

	// Truncate very long labels (keep first 200 chars + ellipsis)
	const maxLen = 200
	if len(escaped) > maxLen {
		escaped = escaped[:maxLen] + "..."
	}

	return escaped
}

// ValidateLabels checks that all labels in the given SVG content are
// properly escaped and contain no dangerous content.
func ValidateLabels(svg string) []ValidationError {
	var errs []ValidationError

	textRe := regexp.MustCompile(`(?i)<text[^>]*>([^<]*)</text>`)
	matches := textRe.FindAllStringSubmatch(svg, -1)

	seen := make(map[string]bool)
	for _, m := range matches {
		if len(m) > 1 {
			label := m[1]
			if label == "" {
				continue
			}
			if seen[label] {
				continue
			}
			seen[label] = true

			if containsUnescapedHTML(label) {
				errs = append(errs, ValidationError{
					Field:   "label",
					Message: fmt.Sprintf("unescaped HTML in label: %q", truncate(label, 80)),
				})
			}
			if len(label) > 500 {
				errs = append(errs, ValidationError{
					Field:   "label_length",
					Message: fmt.Sprintf("label exceeds 500 chars (%d): %q", len(label), truncate(label, 80)),
				})
			}
		}
	}

	return errs
}

// containsUnescapedHTML checks if a string contains HTML-sensitive characters
// that are not properly escaped.
func containsUnescapedHTML(s string) bool {
	// Check for raw HTML entities that should be escaped
	if strings.Contains(s, "<") && !strings.Contains(s, "&lt;") {
		return true
	}
	if strings.Contains(s, ">") && !strings.Contains(s, "&gt;") {
		return true
	}
	if strings.Contains(s, "&") && !strings.Contains(s, "&amp;") {
		// Allow &amp; but flag raw & that's not part of an entity
		re := regexp.MustCompile(`&(?!amp;|lt;|gt;|quot;|apos;|#)`)
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
