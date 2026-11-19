// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package pathutil

import (
	"errors"
	"runtime"
	"strings"
	"testing"
)

func TestIsWindowsAbs(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{`C:\Users\foo`, true},
		{`C:/Users/foo`, true},
		{`c:\foo`, true},
		{`/usr/local/bin`, false},
		{`relative/path`, false},
		{`D:\`, true},
		{``, false},
		{`C:`, false},
	}
	for _, tt := range tests {
		got := IsWindowsAbs(tt.path)
		if got != tt.want {
			t.Errorf("IsWindowsAbs(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestNormalizeForGitHub(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{`contracts/token/src/lib.rs`, `contracts/token/src/lib.rs`},
		{`contracts\token\src\lib.rs`, `contracts/token/src/lib.rs`},
		{`C:\contracts\token\src\lib.rs`, `contracts/token/src/lib.rs`},
		{`C:/contracts/token/src/lib.rs`, `contracts/token/src/lib.rs`},
		{`/usr/local/src/lib.rs`, `usr/local/src/lib.rs`},
	}
	for _, tt := range tests {
		got := NormalizeForGitHub(tt.path)
		if got != tt.want {
			t.Errorf("NormalizeForGitHub(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestToSlash(t *testing.T) {
	got := ToSlash(`foo/bar/baz`)
	if got != "foo/bar/baz" {
		t.Errorf("ToSlash = %q, want %q", got, "foo/bar/baz")
	}
	// Always forward slashes in output.
	if strings.Contains(got, "\\") {
		t.Errorf("ToSlash result contains backslashes: %q", got)
	}
}

func TestJoin(t *testing.T) {
	got := Join("foo", "bar", "baz.rs")
	if runtime.GOOS == "windows" {
		if !strings.Contains(got, "foo") || !strings.Contains(got, "baz.rs") {
			t.Errorf("Join produced unexpected result on Windows: %q", got)
		}
	} else {
		want := "foo/bar/baz.rs"
		if got != want {
			t.Errorf("Join = %q, want %q", got, want)
		}
	}
}

func TestNormalize(t *testing.T) {
	// Mixed separators collapse correctly.
	in := `contracts/token\src/lib.rs`
	got := Normalize(in)
	if strings.Contains(got, "/") && runtime.GOOS == "windows" {
		t.Errorf("Normalize on Windows left forward slashes: %q", got)
	}
	if strings.Contains(got, "\\") && runtime.GOOS != "windows" {
		t.Errorf("Normalize on POSIX left backslashes: %q", got)
	}
}

func TestRelToSlash(t *testing.T) {
	rel, err := RelToSlash("/repo/root", "/repo/root/contracts/token/src/lib.rs")
	if err != nil {
		t.Fatalf("RelToSlash error: %v", err)
	}
	if rel != "contracts/token/src/lib.rs" {
		t.Errorf("RelToSlash = %q, want %q", rel, "contracts/token/src/lib.rs")
	}
}

// ── ValidateSourcePath ─────────────────────────────────────────────────────

func TestValidateSourcePath_EmptyPath(t *testing.T) {
	err := ValidateSourcePath("")
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestValidateSourcePath_NullByte(t *testing.T) {
	err := ValidateSourcePath("src/lib.rs\x00")
	if err == nil {
		t.Fatal("expected error for path with null byte")
	}
}

func TestValidateSourcePath_Traversal(t *testing.T) {
	for _, p := range []string{"../src/lib.rs", "src/../../etc/passwd"} {
		err := ValidateSourcePath(p)
		if err == nil {
			t.Errorf("expected error for traversal path %q", p)
		}
	}
}

func TestValidateSourcePath_ValidRelative(t *testing.T) {
	if err := ValidateSourcePath("src/lib.rs"); err != nil {
		t.Errorf("expected no error for valid relative path, got: %v", err)
	}
}

func TestValidateSourcePath_ValidAbsolute(t *testing.T) {
	if runtime.GOOS == "windows" {
		if err := ValidateSourcePath(`C:\project\src\lib.rs`); err != nil {
			t.Errorf("expected no error for valid Windows path, got: %v", err)
		}
	} else {
		if err := ValidateSourcePath("/home/user/project/src/lib.rs"); err != nil {
			t.Errorf("expected no error for valid POSIX path, got: %v", err)
		}
	}
}

func TestIsPathSafe(t *testing.T) {
	if !IsPathSafe("src/lib.rs") {
		t.Error("expected src/lib.rs to be safe")
	}
	if IsPathSafe("") {
		t.Error("expected empty path to be unsafe")
	}
	if IsPathSafe("../etc/passwd") {
		t.Error("expected traversal path to be unsafe")
	}
}

func TestValidateSourcePath_ReturnsPathValidationError(t *testing.T) {
	err := ValidateSourcePath("")
	if err == nil {
		t.Fatal("expected error for empty path, got nil")
	}

	var pve *PathValidationError
	if !errors.As(err, &pve) {
		t.Fatalf("expected *PathValidationError via errors.As, got %T", err)
	}

	if pve.FixHint == "" {
		t.Error("expected FixHint to be non-empty on PathValidationError")
	}
}
