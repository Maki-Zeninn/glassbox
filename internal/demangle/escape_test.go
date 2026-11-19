// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package demangle_test

import (
	"strings"
	"testing"

	"github.com/dotandev/glassbox/internal/demangle"
)

// TestEscapeForHTML tests HTML escaping
func TestEscapeForHTML(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "normal symbol",
			input: "my_contract::invoke",
			want:  "my_contract::invoke",
		},
		{
			name:  "HTML script tag",
			input: "<script>alert(1)</script>",
			want:  "scriptalert1script",
		},
		{
			name:  "HTML entities",
			input: "test & < > \" '",
			want:  "test   ",
		},
		{
			name:  "null byte",
			input: "test\x00symbol",
			want:  "testsymbol",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := demangle.EscapeForHTML(tc.input)
			if got != tc.want {
				t.Errorf("EscapeForHTML(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestEscapeForMarkdown tests Markdown escaping
func TestEscapeForMarkdown(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "normal symbol",
			input: "my_contract::invoke",
			want:  "my\\_contract::invoke",
		},
		{
			name:  "asterisks",
			input: "test*bold*",
			want:  "test\\*bold\\*",
		},
		{
			name:  "backticks",
			input: "test`code`",
			want:  "test\\`code\\`",
		},
		{
			name:  "brackets",
			input: "test[link]",
			want:  "test\\[link\\]",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := demangle.EscapeForMarkdown(tc.input)
			if got != tc.want {
				t.Errorf("EscapeForMarkdown(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestEscapeForText tests plain text escaping
func TestEscapeForText(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "normal symbol",
			input: "my_contract::invoke",
			want:  "my_contract::invoke",
		},
		{
			name:  "dangerous characters",
			input: "<script>alert(1)</script>",
			want:  "scriptalert1script",
		},
		{
			name:  "control characters",
			input: "test\nsymbol",
			want:  "testsymbol",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := demangle.EscapeForText(tc.input)
			if got != tc.want {
				t.Errorf("EscapeForText(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestEscapeForJSON tests JSON escaping
func TestEscapeForJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "normal symbol",
			input: "my_contract::invoke",
			want:  "my_contract::invoke",
		},
		{
			name:  "quotes",
			input: `test"quote"`,
			want:  `test\"quote\"`,
		},
		{
			name:  "backslash",
			input: `test\path`,
			want:  `test\\path`,
		},
		{
			name:  "control characters",
			input: "test\nsymbol",
			want:  "test\\nsymbol",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := demangle.EscapeForJSON(tc.input)
			if got != tc.want {
				t.Errorf("EscapeForJSON(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestSafeDemangleForHTML tests demangling with HTML escaping
func TestSafeDemangleForHTML(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "valid symbol",
			input: "_ZN11my_contract6invoke17h1a2b3c4d5e6f7890E",
			want:  "my_contract::invoke",
		},
		{
			name:  "malformed with dangerous chars",
			input: "_ZN<script>alert(1)</script>6invokeE",
			want:  "<scriptalert1script:demangle_error>",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := demangle.SafeDemangleForHTML(tc.input)
			if got != tc.want {
				t.Errorf("SafeDemangleForHTML(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestSafeDemangleForMarkdown tests demangling with Markdown escaping
func TestSafeDemangleForMarkdown(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "valid symbol",
			input: "_ZN11my_contract6invoke17h1a2b3c4d5e6f7890E",
			want:  "my\\_contract::invoke",
		},
		{
			name:  "malformed with dangerous chars",
			input: "_ZN<script>alert(1)</script>6invokeE",
			want:  "<scriptalert1script:demangle\\_error>",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := demangle.SafeDemangleForMarkdown(tc.input)
			if got != tc.want {
				t.Errorf("SafeDemangleForMarkdown(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestSafeDemangleForText tests demangling with text escaping
func TestSafeDemangleForText(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "valid symbol",
			input: "_ZN11my_contract6invoke17h1a2b3c4d5e6f7890E",
			want:  "my_contract::invoke",
		},
		{
			name:  "malformed with dangerous chars",
			input: "_ZN<script>alert(1)</script>6invokeE",
			want:  "<scriptalert1script:demangle_error>",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := demangle.SafeDemangleForText(tc.input)
			if got != tc.want {
				t.Errorf("SafeDemangleForText(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestSafeDemangleForJSON tests demangling with JSON escaping
func TestSafeDemangleForJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "valid symbol",
			input: "_ZN11my_contract6invoke17h12b3c4d5e6f7890E",
			want:  "my_contract::invoke",
		},
		{
			name:  "malformed with dangerous chars",
			input: "_ZN<script>alert(1)</script>6invokeE",
			want:  "<scriptalert1script:demangle_error>",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := demangle.SafeDemangleForJSON(tc.input)
			if got != tc.want {
				t.Errorf("SafeDemangleForJSON(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestXSSPrevention tests that XSS attacks are prevented
func TestXSSPrevention(t *testing.T) {
	xssInputs := []string{
		"<script>alert('XSS')</script>",
		"<img src=x onerror=alert('XSS')>",
		"javascript:alert('XSS')",
		"<svg onload=alert('XSS')>",
		"</script><script>alert('XSS')</script>",
	}

	for _, input := range xssInputs {
		t.Run(input, func(t *testing.T) {
			htmlEscaped := demangle.EscapeForHTML(input)
			if strings.Contains(htmlEscaped, "<script>") || strings.Contains(htmlEscaped, "onerror") || strings.Contains(htmlEscaped, "javascript:") {
				t.Errorf("XSS not prevented in HTML escaping: %q -> %q", input, htmlEscaped)
			}

			markdownEscaped := demangle.EscapeForMarkdown(input)
			if strings.Contains(markdownEscaped, "<script>") || strings.Contains(markdownEscaped, "javascript:") {
				t.Errorf("XSS not prevented in Markdown escaping: %q -> %q", input, markdownEscaped)
			}

			textEscaped := demangle.EscapeForText(input)
			if strings.Contains(textEscaped, "<script>") || strings.Contains(textEscaped, "javascript:") {
				t.Errorf("XSS not prevented in text escaping: %q -> %q", input, textEscaped)
			}
		})
	}
}
