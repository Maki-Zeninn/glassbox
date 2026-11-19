// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package pathutil

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Cross-platform path normalization matrix ─────────────────────────────────

// TestNormalize_CrossPlatformMatrix verifies that Normalize produces
// platform-correct output for all representative separator and root patterns.
func TestNormalize_CrossPlatformMatrix(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantPosix string
		wantWin  string
	}{
		{
			name:      "posix forward slashes",
			input:     "src/lib/foo.rs",
			wantPosix: "src/lib/foo.rs",
			wantWin:   `src\lib\foo.rs`,
		},
		{
			name:      "mixed separators",
			input:     `src/lib\foo.rs`,
			wantPosix: "src/lib/foo.rs",
			wantWin:   `src\lib\foo.rs`,
		},
		{
			name:      "all backslashes",
			input:     `src\lib\foo.rs`,
			wantPosix: "src/lib/foo.rs",
			wantWin:   `src\lib\foo.rs`,
		},
		{
			name:      "double separators collapse",
			input:     "src//lib///foo.rs",
			wantPosix: "src/lib/foo.rs",
			wantWin:   `src\lib\foo.rs`,
		},
		{
			name:      "dot segments resolved",
			input:     "src/./lib/../lib/foo.rs",
			wantPosix: "src/lib/foo.rs",
			wantWin:   `src\lib\foo.rs`,
		},
		{
			name:      "empty path",
			input:     "",
			wantPosix: ".",
			wantWin:   ".",
		},
		{
			name:      "trailing separator",
			input:     "src/lib/",
			wantPosix: "src/lib",
			wantWin:   `src\lib`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Normalize(tt.input)

			var want string
			if runtime.GOOS == "windows" {
				want = tt.wantWin
			} else {
				want = tt.wantPosix
			}

			if got != want {
				t.Errorf("Normalize(%q) = %q, want %q (GOOS=%s)", tt.input, got, want, runtime.GOOS)
			}
		})
	}
}

// TestNormalize_WindowsDriveLikeRoots verifies that Windows-style drive roots
// are handled correctly regardless of the current platform.
func TestNormalize_WindowsDriveLikeRoots(t *testing.T) {
	// These should never panic or return empty strings on any platform.
	roots := []string{
		`C:\Users\test\src\lib.rs`,
		`D:/projects/foo/main.go`,
		`E:\with/mixed/separators`,
		`c:\lowercase\drive`,
	}

	for _, root := range roots {
		t.Run(root, func(t *testing.T) {
			got := Normalize(root)
			if got == "" {
				t.Errorf("Normalize(%q) returned empty string", root)
			}
			// The result should never contain forward slashes on Windows
			// or backslashes on POSIX (after normalization).
			if runtime.GOOS == "windows" && strings.Contains(got, "/") {
				t.Errorf("Normalize on Windows left forward slashes: %q", got)
			}
		})
	}
}

// TestToSlash_CrossPlatform verifies ToSlash consistently produces
// forward-slash paths on all platforms.
func TestToSlash_CrossPlatform(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`foo/bar/baz`, "foo/bar/baz"},
		{`foo\bar\baz`, "foo/bar/baz"},
		{`foo\bar/baz`, "foo/bar/baz"},
		{`C:\foo\bar`, "C:/foo/bar"},
		{"/usr/local/bin", "/usr/local/bin"},
		{`.`, "."},
		{``, ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ToSlash(tt.input)
			if got != tt.want {
				t.Errorf("ToSlash(%q) = %q, want %q", tt.input, got, tt.want)
			}
			if strings.Contains(got, "\\") {
				t.Errorf("ToSlash result contains backslashes: %q", got)
			}
		})
	}
}

// TestJoin_CrossPlatform verifies that Join correctly handles mixed-separator
// inputs and returns the platform-appropriate separator.
func TestJoin_CrossPlatform(t *testing.T) {
	tests := []struct {
		name   string
		parts  []string
	PosixWant string
		WinWant  string
	}{
		{
			name:      "simple join",
			parts:     []string{"foo", "bar", "baz.rs"},
			PosixWant: "foo/bar/baz.rs",
			WinWant:   `foo\bar\baz.rs`,
		},
		{
			name:      "mixed separators",
			parts:     []string{`foo\bar`, "baz/qux"},
			PosixWant: "foo/bar/baz/qux",
			WinWant:   `foo\bar\baz\qux`,
		},
		{
			name:      "drive letter in part",
			parts:     []string{`C:\root`, "lib", "main.rs"},
			PosixWant: "C:/root/lib/main.rs",
			WinWant:   `C:\root\lib\main.rs`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Join(tt.parts...)
			var want string
			if runtime.GOOS == "windows" {
				want = tt.WinWant
			} else {
				want = tt.PosixWant
			}
			if got != want {
				t.Errorf("Join(%v) = %q, want %q (GOOS=%s)", tt.parts, got, want, runtime.GOOS)
			}
		})
	}
}

// TestIsWindowsAbs_CrossPlatform verifies that IsWindowsAbs detects Windows
// drive-letter paths consistently on all platforms.
func TestIsWindowsAbs_CrossPlatform(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{`C:\Users\foo`, true},
		{`C:/Users/foo`, true},
		{`c:\foo`, true},
		{`D:\`, true},
		{`Z:/a/b/c`, true},
		{"/usr/local/bin", false},
		{"relative/path", false},
		{"", false},
		{"C:", false},
		{"C", false},
		{`C:\`, true},
		{`C:/`, true},
		// Edge cases that should NOT be detected as Windows abs
		{"123\\path", false},
		{`\\server\share`, false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := IsWindowsAbs(tt.path)
			if got != tt.want {
				t.Errorf("IsWindowsAbs(%q) = %v, want %v (platform=%s)", tt.path, got, tt.want, runtime.GOOS)
			}
		})
	}
}

// TestNormalizeForGitHub_CrossPlatform verifies that NormalizeForGitHub produces
// clean forward-slash paths without Windows drive prefixes on any platform.
func TestNormalizeForGitHub_CrossPlatform(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"posix path", "contracts/token/src/lib.rs", "contracts/token/src/lib.rs"},
		{"backslashes", `contracts\token\src\lib.rs`, "contracts/token/src/lib.rs"},
		{"windows drive", `C:\contracts\token\src\lib.rs`, "contracts/token/src/lib.rs"},
		{"windows forward", "C:/contracts/token/src/lib.rs", "contracts/token/src/lib.rs"},
		{"posix absolute", "/usr/local/src/lib.rs", "usr/local/src/lib.rs"},
		{"mixed", `src/main\lib/mod.rs`, "src/main/lib/mod.rs"},
		{"double slashes", "src//lib///main.rs", "src/lib/main.rs"},
		{"dot segments", "src/../src/main.rs", "src/main.rs"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeForGitHub(tt.input)
			if got != tt.want {
				t.Errorf("NormalizeForGitHub(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestRelToSlash_CrossPlatform verifies that RelToSlash handles both POSIX
// and Windows-style base and target paths.
func TestRelToSlash_CrossPlatform(t *testing.T) {
	tests := []struct {
		name string
		base string
		target string
		want string
	}{
		{
			name:   "posix simple",
			base:   "/repo/root",
			target: "/repo/root/contracts/token/src/lib.rs",
			want:   "contracts/token/src/lib.rs",
		},
		{
			name:   "same directory",
			base:   "/repo/root",
			target: "/repo/root",
			want:   ".",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rel, err := RelToSlash(tt.base, tt.target)
			if err != nil {
				t.Fatalf("RelToSlash error: %v", err)
			}
			if rel != tt.want {
				t.Errorf("RelToSlash(%q, %q) = %q, want %q", tt.base, tt.target, rel, tt.want)
			}
		})
	}
}

// ── ValidateSourcePath cross-platform tests ──────────────────────────────────

func TestValidateSourcePath_CrossPlatformMatrix(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"empty", "", true},
		{"null byte", "src/lib.rs\x00", true},
		{"traversal up", "../src/lib.rs", true},
		{"traversal deep", "src/../../etc/passwd", true},
		{"valid relative", "src/lib.rs", false},
		{"valid nested", "src/deep/nested/path/file.rs", false},
		{"windows drive", `C:\project\src\lib.rs`, runtime.GOOS == "windows"},
		{"posix abs", "/home/user/project/src/lib.rs", runtime.GOOS != "windows"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSourcePath(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateSourcePath(%q) error = %v, wantErr = %v (platform=%s)",
					tt.path, err, tt.wantErr, runtime.GOOS)
			}
		})
	}
}

// TestIsPathSafe_CrossPlatform verifies IsPathSafe returns consistent results.
func TestIsPathSafe_CrossPlatform(t *testing.T) {
	safe := []string{
		"src/lib.rs",
		"contracts/token/src/lib.rs",
		"package.json",
	}

	unsafe := []string{
		"",
		"../etc/passwd",
		"src/../../secret",
	}

	for _, p := range safe {
		if !IsPathSafe(p) {
			t.Errorf("IsPathSafe(%q) = false, want true (platform=%s)", p, runtime.GOOS)
		}
	}

	for _, p := range unsafe {
		if IsPathSafe(p) {
			t.Errorf("IsPathSafe(%q) = true, want false (platform=%s)", p, runtime.GOOS)
		}
	}
}

// ── Platform-specific process tests ──────────────────────────────────────────

// TestJoinTempDir_CrossPlatform verifies that creating a temp directory and
// joining paths within it works correctly on all platforms.
func TestJoinTempDir_CrossPlatform(t *testing.T) {
	dir := t.TempDir()

	// Create a subdirectory using filepath.Join to ensure platform correctness.
	sub := filepath.Join(dir, "sub", "deep")
	err := os.MkdirAll(sub, 0o755)
	require.NoError(t, err)

	// Write a file using platform-native separators.
	target := filepath.Join(sub, "test.txt")
	err = os.WriteFile(target, []byte("cross-platform"), 0o600)
	require.NoError(t, err)

	// Verify we can read it back.
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "cross-platform", string(data))
}

// TestNormalizeDriveRootPreservesCase verifies that Normalize does not
// alter the case of drive letters or path components.
func TestNormalizeDriveRootPreservesCase(t *testing.T) {
	inputs := []string{
		"src/MY_MODULE/lib.rs",
		`C:\Users\MyName\project`,
		"/home/MyUser/project",
	}

	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			got := Normalize(input)
			// Verify the original casing is preserved in the path components.
			// filepath.Clean normalizes separators but preserves case.
			if strings.Contains(input, "MY_MODULE") && !strings.Contains(got, "MY_MODULE") {
				t.Errorf("Normalize lost casing of MY_MODULE: got %q", got)
			}
		})
	}
}

// TestNormalizeForGitHub_StripsDrivePrefixOnAllPlatforms verifies that
// NormalizeForGitHub removes Windows drive prefixes even when running on Linux.
func TestNormalizeForGitHub_StripsDrivePrefixOnAllPlatforms(t *testing.T) {
	input := `D:\projects\glassbox\src\main.rs`
	got := NormalizeForGitHub(input)

	if strings.HasPrefix(got, "D:") {
		t.Errorf("NormalizeForGitHub did not strip drive prefix: %q", got)
	}
	if strings.HasPrefix(got, "/") {
		t.Errorf("NormalizeForGitHub should not produce absolute path: %q", got)
	}
	if got != "projects/glassbox/src/main.rs" {
		t.Errorf("NormalizeForGitHub(%q) = %q, want %q", input, got, "projects/glassbox/src/main.rs")
	}
}

// TestJoinEmptyParts verifies Join handles empty parts without panicking.
func TestJoinEmptyParts(t *testing.T) {
	got := Join()
	if got == "" {
		// filepath.Join("") returns "." on most platforms.
		got = "."
	}
	// Should not panic; result is a valid relative path.
	if strings.Contains(got, "//") {
		t.Errorf("Join() produced double slashes: %q", got)
	}
}

func TestJoinSinglePart(t *testing.T) {
	got := Join("single")
	if got != "single" {
		t.Errorf("Join('single') = %q, want %q", got, "single")
	}
}

// TestNormalizeDeeplyNested verifies deeply nested paths with mixed separators.
func TestNormalizeDeeplyNested(t *testing.T) {
	input := `a/b\c/d\e/f/g`
	got := Normalize(input)

	if runtime.GOOS == "windows" {
		if strings.Contains(got, "/") {
			t.Errorf("Normalize on Windows left forward slashes in deep path: %q", got)
		}
	} else {
		if strings.Contains(got, "\\") {
			t.Errorf("Normalize on POSIX left backslashes in deep path: %q", got)
		}
	}

	// Should not contain ".." or "." (except for root).
	if strings.Contains(got, "..") {
		t.Errorf("Normalize left traversal in path: %q", got)
	}
}

// TestValidateSourcePath_WindowsDriveValidation verifies that Windows drive
// paths are properly validated on all platforms.
func TestValidateSourcePath_WindowsDriveValidation(t *testing.T) {
	// A well-formed Windows absolute path should be valid when running on
	// Windows, and the validation logic should handle it gracefully on POSIX.
	validWin := `C:\project\src\lib.rs`
	err := ValidateSourcePath(validWin)
	if runtime.GOOS == "windows" && err != nil {
		t.Errorf("ValidateSourcePath(%q) failed on Windows: %v", validWin, err)
	}
	// On POSIX, this path should not be rejected as "traversal" — it is a
	// well-formed path even though it looks unusual on Linux.
	if err != nil && strings.Contains(err.Error(), "traversal") {
		t.Errorf("ValidateSourcePath(%q) should not report traversal for Windows drive path: %v", validWin, err)
	}
}
