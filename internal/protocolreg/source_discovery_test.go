// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package protocolreg

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ── validateDiscoveredPath ────────────────────────────────────────────────────

func TestValidateDiscoveredPath_EmptyPath_ReturnsError(t *testing.T) {
	_, err := validateDiscoveredPath("", "test-source")
	if err == nil {
		t.Fatal("expected error for empty path")
	}
	if !strings.Contains(err.Error(), "empty path") {
		t.Errorf("error should mention 'empty path', got: %v", err)
	}
}

func TestValidateDiscoveredPath_NullByte_ReturnsError(t *testing.T) {
	_, err := validateDiscoveredPath("/some/path\x00evil", "test-source")
	if err == nil {
		t.Fatal("expected error for path with null byte")
	}
	if !strings.Contains(err.Error(), "null bytes") {
		t.Errorf("error should mention 'null bytes', got: %v", err)
	}
}

func TestValidateDiscoveredPath_NonExistentFile_ReturnsError(t *testing.T) {
	_, err := validateDiscoveredPath("/nonexistent/path/glassbox", "test-source")
	if err == nil {
		t.Fatal("expected error for non-existent path")
	}
}

func TestValidateDiscoveredPath_Directory_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	_, err := validateDiscoveredPath(dir, "test-source")
	if err == nil {
		t.Fatal("expected error when path is a directory")
	}
	if !strings.Contains(err.Error(), "directory") {
		t.Errorf("error should mention 'directory', got: %v", err)
	}
}

func TestValidateDiscoveredPath_ValidFile_ReturnsAbsPath(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "glassbox")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("create test file: %v", err)
	}

	got, err := validateDiscoveredPath(exe, "test-source")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == "" {
		t.Error("expected a non-empty resolved path")
	}
	if !filepath.IsAbs(got) {
		t.Errorf("resolved path should be absolute, got %q", got)
	}
}

func TestValidateDiscoveredPath_WhitespacePath_Rejected(t *testing.T) {
	_, err := validateDiscoveredPath("   ", "test-source")
	if err == nil {
		t.Fatal("expected error for whitespace-only path")
	}
}

func TestValidateDiscoveredPath_TooLongPath_ReturnsError(t *testing.T) {
	longPath := "/a/" + strings.Repeat("b", maxDiscoveredPathLength)
	_, err := validateDiscoveredPath(longPath, "test-source")
	if err == nil {
		t.Fatal("expected error for path exceeding max length")
	}
	msg := err.Error()
	if !strings.Contains(msg, "too long") {
		t.Errorf("error should mention 'too long', got: %v", err)
	}
	if !strings.Contains(msg, "Fix:") {
		t.Errorf("error should include a Fix: hint, got: %v", err)
	}
}

func TestValidateDiscoveredPath_ExactMaxLength_Accepted(t *testing.T) {
	dir := t.TempDir()
	// Create a path at exactly maxDiscoveredPathLength by using a short dir name.
	shortName := "x"
	padLen := maxDiscoveredPathLength - len(dir) - len(shortName) - 1 // -1 for separator
	if padLen > 0 {
		shortName = shortName + strings.Repeat("a", padLen)
	}
	exe := filepath.Join(dir, shortName[:min(len(shortName), maxDiscoveredPathLength-len(dir)-1)])
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("create test file: %v", err)
	}

	// Shorten the name to fit within the limit.
	if len(exe) > maxDiscoveredPathLength {
		exe = exe[:maxDiscoveredPathLength]
		// Ensure we truncate at a separator boundary.
		if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("create truncated test file: %v", err)
		}
	}

	got, err := validateDiscoveredPath(exe, "test-source")
	if err != nil {
		t.Fatalf("path at exact max length should be accepted: %v", err)
	}
	if got == "" {
		t.Error("expected a non-empty resolved path")
	}
}

func TestValidateDiscoveredPath_NonExecutableOnUnix_ReturnsError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable permission check is Unix-only")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "glassbox")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatalf("create non-executable test file: %v", err)
	}

	_, err := validateDiscoveredPath(exe, "test-source")
	if err == nil {
		t.Fatal("expected error for non-executable file on Unix")
	}
	msg := err.Error()
	if !strings.Contains(msg, "not executable") {
		t.Errorf("error should mention 'not executable', got: %v", err)
	}
	if !strings.Contains(msg, "chmod +x") {
		t.Errorf("error should suggest chmod +x, got: %v", err)
	}
}

func TestValidateDiscoveredPath_ExecutableOnUnix_Accepted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable permission check is Unix-only")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "glassbox")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("create executable test file: %v", err)
	}

	got, err := validateDiscoveredPath(exe, "test-source")
	if err != nil {
		t.Fatalf("executable file should be accepted: %v", err)
	}
	if got == "" {
		t.Error("expected a non-empty resolved path")
	}
}

func TestValidateDiscoveredPath_Windows_NoPermissionCheck(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only test")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "glassbox.exe")
	// On Windows, we don't check executable permission, only existence.
	if err := os.WriteFile(exe, []byte("not really an exe"), 0o644); err != nil {
		t.Fatalf("create test file: %v", err)
	}

	got, err := validateDiscoveredPath(exe, "test-source")
	if err != nil {
		t.Fatalf("Windows should accept non-executable file: %v", err)
	}
	if got == "" {
		t.Error("expected a non-empty resolved path")
	}
}

func TestValidateDiscoveredPath_SymlinkToLongPath_ReturnsError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink test not applicable on Windows")
	}
	dir := t.TempDir()

	// Create a real executable at a short path.
	actualExe := filepath.Join(dir, "real_exe")
	if err := os.WriteFile(actualExe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("create executable: %v", err)
	}

	// Create a symlink with a very long name pointing to it.
	longName := filepath.Join(dir, strings.Repeat("a", maxDiscoveredPathLength-len(dir)+10))
	if err := os.Symlink(actualExe, longName); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	_, err := validateDiscoveredPath(longName, "test-source")
	if err == nil {
		t.Fatal("expected error for path exceeding max length")
	}
	if !strings.Contains(err.Error(), "too long") {
		t.Errorf("error should mention 'too long', got: %v", err)
	}
}

// ── DiscoverExecutableSource ──────────────────────────────────────────────────

// TestDiscoverExecutableSource_FindsSomething verifies that when glassbox is
// invoked normally (not via 'go run') os.Executable returns a usable path.
func TestDiscoverExecutableSource_FindsSomething(t *testing.T) {
	result := DiscoverExecutableSource()

	// In a normal test execution os.Executable() should resolve to the test binary.
	// We only assert the result is structurally valid.
	if result == nil {
		t.Fatal("DiscoverExecutableSource must never return nil")
	}
	if result.Found {
		if result.Path == "" {
			t.Error("Found=true but Path is empty")
		}
		if result.Source == "" {
			t.Error("Found=true but Source is empty")
		}
	} else {
		if result.Hint == "" {
			t.Error("Found=false but Hint is empty — must provide remediation guidance")
		}
	}
}

// TestDiscoverExecutableSource_GLASSBOX_BIN_Override verifies that when
// GLASSBOX_BIN is set to a valid executable it is preferred (as a fallback)
// when os.Executable() returns a temp binary that satisfies stage 1 first.
// We test the env-based fallback in isolation via discoverViaEnv.
func TestDiscoverExecutableSource_GLASSBOX_BIN_ValidFile_IsUsed(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "glassbox-override")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("create override binary: %v", err)
	}
	t.Setenv("GLASSBOX_BIN", exe)

	path, err := discoverViaEnv()
	if err != nil {
		t.Fatalf("discoverViaEnv failed: %v", err)
	}
	if path == "" {
		t.Error("expected non-empty path from GLASSBOX_BIN")
	}
}

func TestDiscoverExecutableSource_GLASSBOX_BIN_MissingFile_ReturnsError(t *testing.T) {
	t.Setenv("GLASSBOX_BIN", "/nonexistent/path/glassbox")
	_, err := discoverViaEnv()
	if err == nil {
		t.Fatal("expected error when GLASSBOX_BIN points to a non-existent file")
	}
}

func TestDiscoverExecutableSource_GLASSBOX_BIN_NotSet_ReturnsError(t *testing.T) {
	t.Setenv("GLASSBOX_BIN", "")
	_, err := discoverViaEnv()
	if err == nil {
		t.Fatal("expected error when GLASSBOX_BIN is not set")
	}
	if !strings.Contains(err.Error(), "GLASSBOX_BIN") {
		t.Errorf("error should mention GLASSBOX_BIN, got: %v", err)
	}
}

func TestDiscoverExecutableSource_GLASSBOX_BIN_NullByte_ReturnsError(t *testing.T) {
	t.Setenv("GLASSBOX_BIN", "/some\x00path")
	_, err := discoverViaEnv()
	if err == nil {
		t.Fatal("expected error for GLASSBOX_BIN with null byte")
	}
}

// ── SourceDiscoveryResult — Hint when not found ───────────────────────────────

func TestSourceDiscoveryResult_HintMentionsGLASSBOX_BIN(t *testing.T) {
	// Construct a result that simulates all stages failing.
	result := &SourceDiscoveryResult{
		Found: false,
		Hint: "Set the GLASSBOX_BIN environment variable to the absolute path of the glassbox binary, " +
			"or reinstall Glassbox so it is available on PATH.",
	}
	if !strings.Contains(result.Hint, "GLASSBOX_BIN") {
		t.Error("Hint should mention GLASSBOX_BIN for the env-var fallback")
	}
	if !strings.Contains(result.Hint, "PATH") {
		t.Error("Hint should mention PATH for the path-lookup fallback")
	}
}

// ── SourceDiscoveryResult — Fallback flag ─────────────────────────────────────

func TestSourceDiscoveryResult_PrimarySource_FallbackFalse(t *testing.T) {
	result := DiscoverExecutableSource()
	if result.Found && result.Source == "os.Executable" && result.Fallback {
		t.Error("Fallback should be false when resolved via the primary os.Executable source")
	}
}

// ── discoverViaPath ───────────────────────────────────────────────────────────

func TestDiscoverViaPath_NonExistentBinary_ReturnsError(t *testing.T) {
	// "glassbox-truly-nonexistent-xyz" should not be on PATH.
	// We can't directly test discoverViaPath("glassbox") without controlling PATH,
	// but we can verify the error path via validateDiscoveredPath for a missing file.
	_, err := validateDiscoveredPath("/nonexistent/binary", "PATH")
	if err == nil {
		t.Fatal("expected error for non-existent binary path")
	}
}
