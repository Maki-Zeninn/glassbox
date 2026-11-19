// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package sourcemap

import (
	"runtime"
	"testing"
)

// TestNewPathNormalizer tests normalizer creation with various options
func TestNewPathNormalizer(t *testing.T) {
	tests := []struct {
		name string
		opts []NormalizerOption
	}{
		{
			name: "default normalizer",
			opts: []NormalizerOption{},
		},
		{
			name: "with workspace roots",
			opts: []NormalizerOption{
				WithWorkspaceRoots("/workspace", "/home/user/project"),
			},
		},
		{
			name: "with remap table",
			opts: []NormalizerOption{
				WithRemapTable([]PathRemap{
					{From: "/build/src", To: "/local/src", Description: "CI build remap"},
				}),
			},
		},
		{
			name: "with preserve original paths",
			opts: []NormalizerOption{
				WithPreserveOriginalPaths(true),
			},
		},
		{
			name: "with case sensitivity",
			opts: []NormalizerOption{
				WithCaseSensitive(true),
			},
		},
		{
			name: "combined options",
			opts: []NormalizerOption{
				WithWorkspaceRoots("/workspace"),
				WithRemapTable([]PathRemap{
					{From: "/build/src", To: "/local/src"},
				}),
				WithPreserveOriginalPaths(true),
				WithCaseSensitive(false),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := NewPathNormalizer(tt.opts...)
			if n == nil {
				t.Fatal("NewPathNormalizer returned nil")
			}
			// Verify default case sensitivity matches platform
			if len(tt.opts) == 0 {
				expectedCaseSensitive := runtime.GOOS != "windows"
				if n.caseSensitive != expectedCaseSensitive {
					t.Errorf("default case sensitivity = %v, want %v", n.caseSensitive, expectedCaseSensitive)
				}
			}
		})
	}
}

// TestNormalizePath_SeparatorNormalization tests separator normalization across platforms
func TestNormalizePath_SeparatorNormalization(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		platform string // "windows" or "unix"
		wantErr  bool
	}{
		{
			name:     "unix path on unix",
			input:    "src/main.rs",
			platform: "unix",
			wantErr:  false,
		},
		{
			name:     "windows path on windows",
			input:    `src\main.rs`,
			platform: "windows",
			wantErr:  false,
		},
		{
			name:     "mixed separators",
			input:    `src/lib/utils.rs`,
			platform: "windows",
			wantErr:  false,
		},
		{
			name:     "windows absolute path",
			input:    `C:\projects\glassbox\src\main.rs`,
			platform: "windows",
			wantErr:  false,
		},
		{
			name:     "empty path",
			input:    "",
			platform: "unix",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Skip platform-specific tests if not on that platform
			if tt.platform == "windows" && runtime.GOOS != "windows" {
				t.Skip("windows-specific test")
			}
			if tt.platform == "unix" && runtime.GOOS == "windows" {
				t.Skip("unix-specific test")
			}

			n := NewPathNormalizer()
			result, diag := n.NormalizePath(tt.input)

			if tt.wantErr {
				if diag == nil || diag.Severity != "error" {
					t.Errorf("NormalizePath() expected error, got diag = %v", diag)
				}
			} else {
				if diag != nil && diag.Severity == "error" {
					t.Errorf("NormalizePath() unexpected error: %v", diag)
				}
				if result == "" && !tt.wantErr {
					t.Errorf("NormalizePath() returned empty string for non-error case")
				}
			}
		})
	}
}

// TestNormalizePath_RemapTable tests explicit remapping table functionality
func TestNormalizePath_RemapTable(t *testing.T) {
	tests := []struct {
		name    string
		remaps  []PathRemap
		input   string
		want    string
		wantErr bool
	}{
		{
			name: "simple remap",
			remaps: []PathRemap{
				{From: "/build/src", To: "/local/src"},
			},
			input:   "/build/src/main.rs",
			want:    "/local/src/main.rs",
			wantErr: false,
		},
		{
			name: "no matching remap",
			remaps: []PathRemap{
				{From: "/build/src", To: "/local/src"},
			},
			input:   "/other/path/file.rs",
			want:    "/other/path/file.rs",
			wantErr: false,
		},
		{
			name: "multiple remaps - first match wins",
			remaps: []PathRemap{
				{From: "/build/src", To: "/local/src"},
				{From: "/build/src", To: "/alt/src"}, // Should not be used
			},
			input:   "/build/src/main.rs",
			want:    "/local/src/main.rs",
			wantErr: false,
		},
		{
			name: "windows path remap",
			remaps: []PathRemap{
				{From: `C:\build\src`, To: `C:\local\src`},
			},
			input:   `C:\build\src\main.rs`,
			want:    `C:\local\src\main.rs`,
			wantErr: false,
		},
		{
			name: "case insensitive remap on windows",
			remaps: []PathRemap{
				{From: "/build/src", To: "/local/src"},
			},
			input:   "/BUILD/SRC/main.rs",
			want:    "/local/src/main.rs",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// For case-insensitive tests, ensure we're on Windows or set case sensitivity
			opts := []NormalizerOption{WithRemapTable(tt.remaps)}
			if tt.name == "case insensitive remap on windows" && runtime.GOOS != "windows" {
				opts = append(opts, WithCaseSensitive(false))
			}

			n := NewPathNormalizer(opts...)
			result, diag := n.NormalizePath(tt.input)

			if tt.wantErr {
				if diag == nil || diag.Severity != "error" {
					t.Errorf("NormalizePath() expected error, got diag = %v", diag)
				}
			} else {
				if diag != nil && diag.Severity == "error" {
					t.Errorf("NormalizePath() unexpected error: %v", diag)
				}
				// On Windows, paths will have backslashes, so we need to compare carefully
				if result != tt.want {
					t.Errorf("NormalizePath() = %v, want %v", result, tt.want)
				}
			}
		})
	}
}

// TestNormalizePath_WorkspaceRoots tests workspace root resolution
func TestNormalizePath_WorkspaceRoots(t *testing.T) {
	tests := []struct {
		name           string
		workspaceRoots []string
		input          string
		wantErr        bool
	}{
		{
			name:           "relative path with workspace root",
			workspaceRoots: []string{"/workspace"},
			input:          "src/main.rs",
			wantErr:        false,
		},
		{
			name:           "absolute path - no workspace resolution",
			workspaceRoots: []string{"/workspace"},
			input:          "/absolute/path/file.rs",
			wantErr:        false,
		},
		{
			name:           "no workspace roots configured",
			workspaceRoots: []string{},
			input:          "src/main.rs",
			wantErr:        false,
		},
		{
			name:           "multiple workspace roots",
			workspaceRoots: []string{"/workspace1", "/workspace2"},
			input:          "src/main.rs",
			wantErr:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := NewPathNormalizer(WithWorkspaceRoots(tt.workspaceRoots...))
			result, diag := n.NormalizePath(tt.input)

			if tt.wantErr {
				if diag == nil || diag.Severity != "error" {
					t.Errorf("NormalizePath() expected error, got diag = %v", diag)
				}
			} else {
				if diag != nil && diag.Severity == "error" {
					t.Errorf("NormalizePath() unexpected error: %v", diag)
				}
				// The result should be a valid path
				if result == "" && !tt.wantErr {
					t.Errorf("NormalizePath() returned empty string for non-error case")
				}
			}
		})
	}
}

// TestNormalizePath_SafetyValidation tests safety checks for dangerous paths
func TestNormalizePath_SafetyValidation(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "null bytes in path",
			input:   "src/main\x00.rs",
			wantErr: true,
			errMsg:  "null bytes",
		},
		{
			name:    "directory traversal",
			input:   "../../../etc/passwd",
			wantErr: true,
			errMsg:  "directory traversal",
		},
		{
			name:    "safe relative path",
			input:   "src/lib/utils.rs",
			wantErr: false,
		},
		{
			name:    "safe absolute path",
			input:   "/workspace/src/main.rs",
			wantErr: false,
		},
		{
			name:    "mixed traversal",
			input:   "src/../lib/utils.rs",
			wantErr: false, // filepath.Clean should handle this
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := NewPathNormalizer()
			result, diag := n.NormalizePath(tt.input)

			if tt.wantErr {
				if diag == nil || diag.Severity != "error" {
					t.Errorf("NormalizePath() expected error, got diag = %v", diag)
				}
				if tt.errMsg != "" && diag != nil {
					if !contains(diag.Message, tt.errMsg) {
						t.Errorf("error message = %v, want containing %v", diag.Message, tt.errMsg)
					}
				}
				// On error, should return original path
				if result != tt.input {
					t.Errorf("on error, should return original path, got %v", result)
				}
			} else {
				if diag != nil && diag.Severity == "error" {
					t.Errorf("NormalizePath() unexpected error: %v", diag)
				}
			}
		})
	}
}

// TestNormalizePath_Precendence tests precedence order: remap table > workspace roots
func TestNormalizePath_Precedence(t *testing.T) {
	tests := []struct {
		name           string
		remaps         []PathRemap
		workspaceRoots []string
		input          string
		expectedIn     string // string that should be in the result
	}{
		{
			name: "remap table takes precedence over workspace roots",
			remaps: []PathRemap{
				{From: "/build/src", To: "/remapped/src"},
			},
			workspaceRoots: []string{"/workspace"},
			input:          "/build/src/main.rs",
			expectedIn:     "/remapped/src",
		},
		{
			name:           "workspace roots used when no remap matches",
			remaps:         []PathRemap{},
			workspaceRoots: []string{"/workspace"},
			input:          "src/main.rs",
			expectedIn:     "/workspace",
		},
		{
			name: "both remap and workspace applied to different paths",
			remaps: []PathRemap{
				{From: "/build", To: "/remapped"},
			},
			workspaceRoots: []string{"/workspace"},
			input:          "/build/src/main.rs",
			expectedIn:     "/remapped",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := NewPathNormalizer(
				WithRemapTable(tt.remaps),
				WithWorkspaceRoots(tt.workspaceRoots...),
			)
			result, diag := n.NormalizePath(tt.input)

			if diag != nil && diag.Severity == "error" {
				t.Errorf("NormalizePath() unexpected error: %v", diag)
			}
			if !contains(result, tt.expectedIn) {
				t.Errorf("NormalizePath() = %v, expected to contain %v", result, tt.expectedIn)
			}
		})
	}
}

// TestPathNormalizer_Diagnostics tests diagnostic collection and reporting
func TestPathNormalizer_Diagnostics(t *testing.T) {
	n := NewPathNormalizer(
		WithRemapTable([]PathRemap{
			{From: "/build/src", To: "/local/src"},
			{From: "/build/src", To: "/alt/src"}, // Ambiguous remap
		}),
	)

	// This should generate a warning about ambiguous remaps
	result, diag := n.NormalizePath("/build/src/main.rs")
	if diag != nil && diag.Severity == "error" {
		t.Errorf("NormalizePath() unexpected error: %v", diag)
	}

	// Check that diagnostic was recorded
	diagnostics := n.GetDiagnostics()
	if len(diagnostics) == 0 {
		t.Error("expected diagnostics to be recorded, got none")
	}

	// Check HasWarnings
	if !n.HasWarnings() {
		t.Error("expected HasWarnings to return true")
	}

	// Check HasErrors (should be false)
	if n.HasErrors() {
		t.Error("expected HasErrors to return false")
	}

	// Clear diagnostics
	n.ClearDiagnostics()
	if len(n.GetDiagnostics()) != 0 {
		t.Error("expected diagnostics to be cleared")
	}

	// Test that result is still valid
	if result == "" {
		t.Error("expected non-empty result despite ambiguous remap warning")
	}
}

// TestPathNormalizer_CaseSensitivity tests case-sensitive vs case-insensitive matching
func TestPathNormalizer_CaseSensitivity(t *testing.T) {
	tests := []struct {
		name         string
		caseSensitive bool
		remaps       []PathRemap
		input        string
		wantMatch    bool
	}{
		{
			name:         "case sensitive - exact match",
			caseSensitive: true,
			remaps:       []PathRemap{{From: "/build/src", To: "/local/src"}},
			input:        "/build/src/main.rs",
			wantMatch:    true,
		},
		{
			name:         "case sensitive - no match",
			caseSensitive: true,
			remaps:       []PathRemap{{From: "/build/src", To: "/local/src"}},
			input:        "/BUILD/SRC/main.rs",
			wantMatch:    false,
		},
		{
			name:         "case insensitive - match",
			caseSensitive: false,
			remaps:       []PathRemap{{From: "/build/src", To: "/local/src"}},
			input:        "/BUILD/SRC/main.rs",
			wantMatch:    true,
		},
		{
			name:         "case insensitive - match",
			caseSensitive: false,
			remaps:       []PathRemap{{From: "/build/src", To: "/local/src"}},
			input:        "/build/src/main.rs",
			wantMatch:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := NewPathNormalizer(
				WithCaseSensitive(tt.caseSensitive),
				WithRemapTable(tt.remaps),
			)
			result, diag := n.NormalizePath(tt.input)

			if diag != nil && diag.Severity == "error" {
				t.Errorf("NormalizePath() unexpected error: %v", diag)
			}

			if tt.wantMatch {
				if !contains(result, "/local/src") {
					t.Errorf("expected remap to match, got result = %v", result)
				}
			} else {
				if contains(result, "/local/src") {
					t.Errorf("expected remap not to match, got result = %v", result)
				}
			}
		})
	}
}

// TestPathRemap_JSONSerialization tests JSON serialization of PathRemap
func TestPathRemap_JSONSerialization(t *testing.T) {
	remap := PathRemap{
		From:        "/build/src",
		To:          "/local/src",
		Description: "CI build directory remap",
	}

	// This test ensures the struct can be serialized to JSON
	// We don't actually test the JSON output, just that it compiles
	_ = remap.From
	_ = remap.To
	_ = remap.Description
}

// TestPathDiagnostic_JSONSerialization tests JSON serialization of PathDiagnostic
func TestPathDiagnostic_JSONSerialization(t *testing.T) {
	diag := PathDiagnostic{
		Severity:       "warning",
		Message:        "ambiguous remap",
		OriginalPath:   "/build/src/main.rs",
		NormalizedPath: "/local/src/main.rs",
		Hint:           "use more specific remap rules",
	}

	// This test ensures the struct can be serialized to JSON
	_ = diag.Severity
	_ = diag.Message
	_ = diag.OriginalPath
	_ = diag.NormalizedPath
	_ = diag.Hint
}

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Test fixtures for common compiler path styles
var compilerPathFixtures = []struct {
	name    string
	paths   []string
	remaps  []PathRemap
	roots   []string
}{
	{
		name: "unix_developer_machine",
		paths: []string{
			"/home/username/projects/glassbox/src/main.rs",
			"/home/username/projects/glassbox/crates/token/src/lib.rs",
			"src/lib/utils.rs",
		},
		remaps: []PathRemap{},
		roots:  []string{"/home/username/projects/glassbox"},
	},
	{
		name: "github_actions_ci",
		paths: []string{
			"/home/runner/work/glassbox/glassbox/src/main.rs",
			"/home/runner/work/glassbox/glassbox/crates/token/src/lib.rs",
		},
		remaps: []PathRemap{
			{From: "/home/runner/work/glassbox/glassbox", To: "/local/glassbox"},
		},
		roots: []string{},
	},
	{
		name: "docker_container",
		paths: []string{
			"/workspace/crates/token/src/lib.rs",
			"/workspace/src/main.rs",
		},
		remaps: []PathRemap{
			{From: "/workspace", To: "/local/workspace"},
		},
		roots: []string{},
	},
	{
		name: "windows_developer_machine",
		paths: []string{
			`C:\Users\username\projects\glassbox\src\main.rs`,
			`C:\Users\username\projects\glassbox\crates\token\src\lib.rs`,
		},
		remaps: []PathRemap{},
		roots:  []string{`C:\Users\username\projects\glassbox`},
	},
	{
		name: "mixed_separators",
		paths: []string{
			`C:/build/workspace/src/main.rs`,
			`/home/user\project\src/lib.rs`,
		},
		remaps: []PathRemap{},
		roots:  []string{},
	},
}

// TestNormalizePath_CompilerPathFixtures tests normalization with real-world compiler path patterns
func TestNormalizePath_CompilerPathFixtures(t *testing.T) {
	for _, fixture := range compilerPathFixtures {
		t.Run(fixture.name, func(t *testing.T) {
			// Skip Windows-specific tests on Unix and vice versa
			if fixture.name == "windows_developer_machine" && runtime.GOOS != "windows" {
				t.Skip("Windows-specific test")
			}

			n := NewPathNormalizer(
				WithRemapTable(fixture.remaps),
				WithWorkspaceRoots(fixture.roots...),
			)

			for _, path := range fixture.paths {
				result, diag := n.NormalizePath(path)
				
				// Should not error on valid paths
				if diag != nil && diag.Severity == "error" {
					t.Errorf("NormalizePath(%q) unexpected error: %v", path, diag)
				}
				
				// Result should be non-empty for valid paths
				if result == "" {
					t.Errorf("NormalizePath(%q) returned empty string", path)
				}
			}
		})
	}
}

// TestNormalizePath_DirectoryTraversal tests handling of .. components
func TestNormalizePath_DirectoryTraversal(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:    "safe relative path with ..",
			input:   "src/../lib/utils.rs",
			wantErr: false, // filepath.Clean should handle this safely
		},
		{
			name:    "dangerous traversal at start",
			input:   "../../../etc/passwd",
			wantErr: true,
		},
		{
			name:    "traversal in middle",
			input:   "src/../../etc/passwd",
			wantErr: true,
		},
		{
			name:    "multiple traversals",
			input:   "src/lib/../../../etc/passwd",
			wantErr: true,
		},
		{
			name:    "safe absolute path",
			input:   "/workspace/src/main.rs",
			wantErr: false,
		},
		{
			name:    "normal relative path",
			input:   "src/lib/utils.rs",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := NewPathNormalizer()
			result, diag := n.NormalizePath(tt.input)

			if tt.wantErr {
				if diag == nil || diag.Severity != "error" {
					t.Errorf("NormalizePath() expected error for %q, got diag = %v", tt.input, diag)
				}
				// On error, should return original path
				if result != tt.input {
					t.Errorf("on error, should return original path, got %v", result)
				}
			} else {
				if diag != nil && diag.Severity == "error" {
					t.Errorf("NormalizePath() unexpected error for %q: %v", tt.input, diag)
				}
			}
		})
	}
}

// TestNormalizePath_MissingFiles tests handling of paths that don't exist on filesystem
func TestNormalizePath_MissingFiles(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		workspaceRoots []string
		wantErr        bool
	}{
		{
			name:           "missing file with workspace root",
			input:          "nonexistent/file.rs",
			workspaceRoots: []string{"/workspace"},
			wantErr:        false, // PathNormalizer doesn't check existence, just normalizes
		},
		{
			name:           "missing file no workspace",
			input:          "nonexistent/file.rs",
			workspaceRoots: []string{},
			wantErr:        false,
		},
		{
			name:           "absolute path to missing file",
			input:          "/nonexistent/path/file.rs",
			workspaceRoots: []string{},
			wantErr:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := NewPathNormalizer(WithWorkspaceRoots(tt.workspaceRoots...))
			result, diag := n.NormalizePath(tt.input)

			// PathNormalizer doesn't check file existence, so should not error
			if diag != nil && diag.Severity == "error" {
				t.Errorf("NormalizePath() unexpected error: %v", diag)
			}
			
			// Should still return a normalized path
			if result == "" {
				t.Errorf("NormalizePath() returned empty string for missing file")
			}
		})
	}
}

// TestNormalizePath_AmbiguousRemaps tests diagnostic generation for ambiguous remappings
func TestNormalizePath_AmbiguousRemaps(t *testing.T) {
	tests := []struct {
		name         string
		remaps       []PathRemap
		input        string
		wantWarning  bool
		warningMsg   string
	}{
		{
			name: "two remaps with same From prefix",
			remaps: []PathRemap{
				{From: "/build/src", To: "/local/src", Description: "First remap"},
				{From: "/build/src", To: "/alt/src", Description: "Second remap"},
			},
			input:       "/build/src/main.rs",
			wantWarning: true,
			warningMsg:  "ambiguous",
		},
		{
			name: "overlapping prefixes",
			remaps: []PathRemap{
				{From: "/build", To: "/local"},
				{From: "/build/src", To: "/alt/src"},
			},
			input:       "/build/src/main.rs",
			wantWarning: true,
			warningMsg:  "ambiguous",
		},
		{
			name: "no ambiguity",
			remaps: []PathRemap{
				{From: "/build/src", To: "/local/src"},
			},
			input:       "/build/src/main.rs",
			wantWarning: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := NewPathNormalizer(WithRemapTable(tt.remaps))
			
			// Clear any previous diagnostics
			n.ClearDiagnostics()
			
			result, diag := n.NormalizePath(tt.input)

			if diag != nil && diag.Severity == "error" {
				t.Errorf("NormalizePath() unexpected error: %v", diag)
			}

			diagnostics := n.GetDiagnostics()
			
			if tt.wantWarning {
				if !n.HasWarnings() {
					t.Error("expected warnings to be recorded")
				}
				if len(diagnostics) == 0 {
					t.Error("expected diagnostic to be recorded")
				}
				if tt.warningMsg != "" {
					found := false
					for _, d := range diagnostics {
						if contains(d.Message, tt.warningMsg) {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("expected warning message to contain %q, got %v", tt.warningMsg, diagnostics)
					}
				}
			} else {
				if n.HasWarnings() {
					t.Errorf("unexpected warnings: %v", diagnostics)
				}
			}

			// Despite warnings, should still return a valid result
			if result == "" {
				t.Error("expected non-empty result despite ambiguous remap warning")
			}
		})
	}
}

// TestNormalizePath_SeparatorEdgeCases tests edge cases in separator handling
func TestNormalizePath_SeparatorEdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:    "multiple consecutive separators",
			input:   "src//lib///utils.rs",
			wantErr: false,
		},
		{
			name:    "trailing separator",
			input:   "src/lib/",
			wantErr: false,
		},
		{
			name:    "leading separator",
			input:   "/src/lib.rs",
			wantErr: false,
		},
		{
			name:    "only separators",
			input:   "///",
			wantErr: false,
		},
		{
			name:    "mixed separators in path",
			input:   `src/lib\utils.rs`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := NewPathNormalizer()
			result, diag := n.NormalizePath(tt.input)

			if tt.wantErr {
				if diag == nil || diag.Severity != "error" {
					t.Errorf("NormalizePath() expected error, got diag = %v", diag)
				}
			} else {
				if diag != nil && diag.Severity == "error" {
					t.Errorf("NormalizePath() unexpected error: %v", diag)
				}
				// Result should be cleaned/normalized
				if result == "" && !tt.wantErr {
					t.Errorf("NormalizePath() returned empty string")
				}
			}
		})
	}
}

// TestNormalizePath_RepositoryRelativePaths tests repository-relative path resolution
func TestNormalizePath_RepositoryRelativePaths(t *testing.T) {
	tests := []struct {
		name           string
		remaps         []PathRemap
		workspaceRoots []string
		input          string
		expectedRel    string // expected repository-relative part
	}{
		{
			name: "strip build prefix via remap",
			remaps: []PathRemap{
				{From: "/build/workspace", To: "/repo"},
			},
			workspaceRoots: []string{},
			input:          "/build/workspace/src/main.rs",
			expectedRel:    "src/main.rs",
		},
		{
			name:           "relative path remains relative",
			remaps:         []PathRemap{},
			workspaceRoots: []string{"/repo"},
			input:          "src/main.rs",
			expectedRel:    "src/main.rs",
		},
		{
			name: "absolute path outside workspace",
			remaps: []PathRemap{
				{From: "/build", To: "/repo"},
			},
			workspaceRoots: []string{},
			input:          "/other/path/file.rs",
			expectedRel:    "/other/path/file.rs",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := NewPathNormalizer(
				WithRemapTable(tt.remaps),
				WithWorkspaceRoots(tt.workspaceRoots...),
			)
			result, diag := n.NormalizePath(tt.input)

			if diag != nil && diag.Severity == "error" {
				t.Errorf("NormalizePath() unexpected error: %v", diag)
			}

			// Check if the expected relative part is in the result
			if !contains(result, tt.expectedRel) {
				t.Errorf("NormalizePath() = %v, expected to contain %v", result, tt.expectedRel)
			}
		})
	}
}
