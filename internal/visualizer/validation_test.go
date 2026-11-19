// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package visualizer

import (
	"strings"
	"testing"
)

func TestValidateSVG_ValidFlamegraph(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1200 600">
  <g><rect x="0" y="0" width="1200" height="20" fill="#e63946"/><text x="5" y="15">main</text></g>
  <g><rect x="0" y="20" width="600" height="20" fill="#457b9d"/><text x="5" y="35">transfer</text></g>
</svg>`

	result := ValidateSVG(svg)
	if !result.Valid {
		t.Errorf("expected valid SVG, got errors: %v", result.Errors)
	}
}

func TestValidateSVG_EmptyString(t *testing.T) {
	result := ValidateSVG("")
	if result.Valid {
		t.Error("expected invalid for empty string")
	}
	if len(result.Errors) == 0 {
		t.Error("expected error messages")
	}
}

func TestValidateSVG_MissingSVGElement(t *testing.T) {
	result := ValidateSVG("<html><body>not svg</body></html>")
	if result.Valid {
		t.Error("expected invalid for non-SVG content")
	}
}

func TestValidateSVG_MissingFrames(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><circle r="10"/></svg>`
	result := ValidateSVG(svg)
	if result.Valid {
		t.Error("expected invalid for SVG without frames")
	}
}

func TestValidateSVG_MissingViewBox(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg">
  <g><rect x="0" y="0" width="100" height="20"/><text>x</text></g>
</svg>`
	result := ValidateSVG(svg)
	// Should be valid but with warning
	if !result.Valid {
		t.Errorf("expected valid with warning, got errors: %v", result.Errors)
	}
	if len(result.Warnings) == 0 {
		t.Error("expected warning about missing viewBox")
	}
}

func TestValidateSVG_UnescapedLabels(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg">
  <g><rect x="0" y="0" width="100" height="20"/><text>x</text></g>
  <g><rect x="0" y="20" width="100" height="20"/><text>x</text></g>
</svg>`
	// This SVG has raw < in a text element
	svgWithBadLabel := strings.Replace(svg, "<text>x</text>", `<text>x < y</text>`, 1)
	result := ValidateSVG(svgWithBadLabel)
	if result.Valid {
		t.Error("expected invalid for unescaped label content")
	}
}

func TestValidateSVG_ProperlyEscapedLabels(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg">
  <g><rect x="0" y="0" width="100" height="20"/><text>x</text></g>
  <g><rect x="0" y="20" width="100" height="20"><title>x &amp; y</title></g></g>
</svg>`
	result := ValidateSVG(svg)
	// Properly escaped &amp; should not cause validation failure
	if !result.Valid {
		t.Errorf("expected valid SVG with escaped entities, got: %v", result.Errors)
	}
}

func TestValidateHTML_ValidInteractiveFlamegraph(t *testing.T) {
	html := `<!DOCTYPE html>
<html>
<head><style>@media (prefers-color-scheme: dark) {}</style></head>
<body>
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1200 600">
  <g><rect x="0" y="0" width="100" height="20"/><text>x</text></g>
</svg>
<script>
  svg.addEventListener('mouseover', handleMouseOver);
  svg.addEventListener('click', handleClick);
  searchInput.addEventListener('input', performSearch);
</script>
</body>
</html>`

	result := ValidateHTML(html)
	if !result.Valid {
		t.Errorf("expected valid HTML, got errors: %v", result.Errors)
	}
}

func TestValidateHTML_EmptyString(t *testing.T) {
	result := ValidateHTML("")
	if result.Valid {
		t.Error("expected invalid for empty string")
	}
}

func TestValidateHTML_MissingSVG(t *testing.T) {
	result := ValidateHTML("<!DOCTYPE html><html><body>no svg here</body></html>")
	if result.Valid {
		t.Error("expected invalid for HTML without SVG")
	}
}

func TestValidateHTML_ExternalDependencies(t *testing.T) {
	html := `<!DOCTYPE html>
<html>
<body>
<svg xmlns="http://www.w3.org/2000/svg"><g><rect/></g></svg>
<script src="http://example.com/lib.js"></script>
</body>
</html>`
	result := ValidateHTML(html)
	if result.Valid {
		t.Error("expected invalid for HTML with external dependencies")
	}
}

func TestValidateHTML_MissingDarkMode(t *testing.T) {
	html := `<!DOCTYPE html>
<html>
<body>
<svg xmlns="http://www.w3.org/2000/svg"><g><rect/></g></svg>
<script>
  svg.addEventListener('mouseover', handleMouseOver);
  svg.addEventListener('click', handleClick);
  searchInput.addEventListener('input', performSearch);
</script>
</body>
</html>`
	result := ValidateHTML(html)
	if !result.Valid {
		t.Errorf("expected valid, got errors: %v", result.Errors)
	}
	if len(result.Warnings) == 0 {
		t.Error("expected warning about missing dark mode")
	}
}

func TestValidateFlamegraph_InvalidFormat(t *testing.T) {
	result := ValidateFlamegraph("content", ExportFormat("pdf"))
	if result.Valid {
		t.Error("expected invalid for unsupported format")
	}
}

func TestEscapeLabel_NormalString(t *testing.T) {
	result := EscapeLabel("transfer")
	if result != "transfer" {
		t.Errorf("expected 'transfer', got %q", result)
	}
}

func TestEscapeLabel_HTMLEntities(t *testing.T) {
	result := EscapeLabel("<script>alert('xss')</script>")
	if result != "&lt;script&gt;alert(&#39;xss&#39;)&lt;/script&gt;" {
		t.Errorf("expected escaped HTML, got %q", result)
	}
}

func TestEscapeLabel_Ampersand(t *testing.T) {
	result := EscapeLabel("x & y")
	if result != "x &amp; y" {
		t.Errorf("expected escaped ampersand, got %q", result)
	}
}

func TestEscapeLabel_LongLabel(t *testing.T) {
	longLabel := strings.Repeat("a", 300)
	result := EscapeLabel(longLabel)
	if len(result) > 210 {
		t.Errorf("expected truncated label, got length %d", len(result))
	}
	if !strings.HasSuffix(result, "...") {
		t.Error("truncated label should end with ...")
	}
}

func TestValidateLabels_MixedContent(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg">
  <g><rect x="0" y="0" width="100" height="20"/><text>x</text></g>
  <g><rect x="0" y="20" width="100" height="20"><title>good label</title></g></g>
</svg>`

	errs := ValidateLabels(svg)
	// No unescaped HTML in these labels
	for _, e := range errs {
		if e.Field == "label" {
			t.Errorf("unexpected label error: %s", e.Message)
		}
	}
}

func TestContainsUnescapedHTML(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"plain text", false},
		{"x & y", true},
		{"x &amp; y", false},
		{"<div>", true},
		{"&lt;div&gt;", false},
		{"hello > world", true},
		{"hello &gt; world", false},
	}

	for _, tt := range tests {
		got := containsUnescapedHTML(tt.input)
		if got != tt.want {
			t.Errorf("containsUnescapedHTML(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}
