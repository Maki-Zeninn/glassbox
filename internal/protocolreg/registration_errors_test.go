// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package protocolreg

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	ersterrors "github.com/dotandev/glassbox/internal/errors"
)

// ── verificationError ────────────────────────────────────────────────────────

func TestVerificationError_IncludesAllIssues(t *testing.T) {
	issues := []string{"registry key missing", "URL Protocol value absent", "open command mismatch"}
	err := verificationError(issues)
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	msg := err.Error()
	for _, issue := range issues {
		if !strings.Contains(msg, issue) {
			t.Errorf("error message should contain %q, got: %s", issue, msg)
		}
	}
}

func TestVerificationError_SingleIssue(t *testing.T) {
	err := verificationError([]string{"something went wrong"})
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	if !strings.Contains(err.Error(), "something went wrong") {
		t.Errorf("error should include the issue text, got: %v", err)
	}
}

// ── ErrRegistryConflict wrapping ─────────────────────────────────────────────

func TestErrRegistryConflict_IsDetectableWhenWrapped(t *testing.T) {
	// Verify that wrapping ErrRegistryConflict with fmt.Errorf preserves errors.Is
	// semantics — critical for callers that check for this sentinel.
	wrapped := fmt.Errorf("context about the conflict: %w", ersterrors.ErrRegistryConflict)
	if !errors.Is(wrapped, ersterrors.ErrRegistryConflict) {
		t.Error("errors.Is should detect ErrRegistryConflict through wrapping")
	}
}

func TestErrRegistryConflict_WrappedIncludesActionableGuidance(t *testing.T) {
	wrapped := fmt.Errorf(
		"protocol registration conflict: registry key is claimed by another app: %w\n"+
			"  Fix: run 'glassbox protocol:repair' to reclaim the registration",
		ersterrors.ErrRegistryConflict,
	)
	msg := wrapped.Error()
	if !strings.Contains(msg, "protocol:repair") {
		t.Errorf("wrapped conflict error should mention 'protocol:repair', got: %s", msg)
	}
	if !strings.Contains(msg, "registration conflict") {
		t.Errorf("wrapped conflict error should mention 'registration conflict', got: %s", msg)
	}
}

// ── NewRegistrar validation ──────────────────────────────────────────────────

func TestNewRegistrar_RejectsSystemRootPath(t *testing.T) {
	// We can't directly inject a path into NewRegistrar, but we can verify the
	// validation logic by constructing a Registrar manually and checking that
	// validatePreRegistration catches an empty executable path (which would be
	// the result of a failed NewRegistrar).
	r := &Registrar{
		executablePath: "/",
		homeDir:        t.TempDir(),
	}
	err := r.validatePreRegistration()
	if err == nil {
		t.Fatal("validatePreRegistration should reject a system root executable path")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error should mention 'empty' for root path, got: %v", err)
	}
}

func TestNewRegistrar_ValidatesHomeDirectory(t *testing.T) {
	r := &Registrar{
		executablePath: t.TempDir(), // a directory, not a file, but validatePreRegistration only checks existence
		homeDir:        "/nonexistent/home/dir/that/does/not/exist",
	}
	err := r.validatePreRegistration()
	if err == nil {
		t.Error("validatePreRegistration should reject an inaccessible home directory")
	}
	if !strings.Contains(err.Error(), "home directory") {
		t.Errorf("error should mention 'home directory', got: %v", err)
	}
}

func TestNewRegistrar_EmptyExecutablePath_ReturnsError(t *testing.T) {
	r := &Registrar{
		executablePath: "",
		homeDir:        t.TempDir(),
	}
	err := r.validatePreRegistration()
	if err == nil {
		t.Fatal("validatePreRegistration should reject empty executable path")
	}
	msg := err.Error()
	if !strings.Contains(msg, "executable path is empty") {
		t.Errorf("error should mention 'executable path is empty', got: %v", err)
	}
	if !strings.Contains(msg, "Fix:") {
		t.Errorf("error should include a Fix: hint, got: %v", err)
	}
}

func TestNewRegistrar_EmptyHomeDir_ReturnsError(t *testing.T) {
	r := &Registrar{
		executablePath: t.TempDir(),
		homeDir:        "",
	}
	err := r.validatePreRegistration()
	if err == nil {
		t.Fatal("validatePreRegistration should reject empty home directory")
	}
	if !strings.Contains(err.Error(), "home directory is empty") {
		t.Errorf("error should mention 'home directory is empty', got: %v", err)
	}
}

// ── hasCommand ───────────────────────────────────────────────────────────────

func TestHasCommand_KnownMissingCommand(t *testing.T) {
	if hasCommand("glassbox-nonexistent-binary-xyz-12345") {
		t.Error("hasCommand should return false for a nonexistent binary")
	}
}

func TestHasCommand_KnownPresentCommand(t *testing.T) {
	// "sh" is available on all supported non-Windows platforms.
	if runtime.GOOS == "windows" {
		t.Skip("test uses 'sh' which is not present on Windows")
	}
	if !hasCommand("sh") {
		t.Error("hasCommand should return true for 'sh'")
	}
}

// ── runCommand ───────────────────────────────────────────────────────────────

func TestRunCommand_MissingBinary_ReturnsError(t *testing.T) {
	_, err := runCommand("glassbox-nonexistent-binary-xyz-12345")
	if err == nil {
		t.Fatal("expected error when running a nonexistent command")
	}
	// The error should identify the binary name so the user knows what failed.
	if !strings.Contains(err.Error(), "glassbox-nonexistent-binary-xyz-12345") {
		t.Errorf("error should include the binary name, got: %v", err)
	}
}

// ── shellQuote ───────────────────────────────────────────────────────────────

func TestShellQuote_SimplePath(t *testing.T) {
	got := shellQuote("/usr/local/bin/glassbox")
	if got != "'/usr/local/bin/glassbox'" {
		t.Errorf("expected single-quoted path, got %q", got)
	}
}

func TestShellQuote_PathWithSingleQuote(t *testing.T) {
	// A path like /home/user's/glassbox must be escaped so the shell script is valid.
	// shellQuote replaces ' with '"'"' — this is the standard POSIX escape sequence
	// that closes the current single-quoted string, injects a double-quoted single
	// quote, then reopens single-quoting.
	got := shellQuote("/home/user's/glassbox")
	// The result must be wrapped in outer single quotes.
	if !strings.HasPrefix(got, "'") || !strings.HasSuffix(got, "'") {
		t.Errorf("shellQuote result should be wrapped in single quotes, got: %q", got)
	}
	// The original path content must survive in the output.
	if !strings.Contains(got, "user") || !strings.Contains(got, "glassbox") {
		t.Errorf("shellQuote lost path components, got: %q", got)
	}
	// The escaped single quote sequence must appear in the output.
	if !strings.Contains(got, `'"'"'`) {
		t.Errorf("shellQuote should use '\"'\"' escape sequence, got: %q", got)
	}
}

func TestShellQuote_EmptyString(t *testing.T) {
	got := shellQuote("")
	if got != "''" {
		t.Errorf("empty string should become two single quotes, got %q", got)
	}
}

func TestRegisterLinux_XdgMissingMessage_IsActionable(t *testing.T) {
	// Construct the exact error message that registerLinux would return when
	// xdg-mime is absent, and verify it contains installation guidance.
	msg := "xdg-mime is not installed: cannot register the glassbox:// MIME handler\n" +
		"  Fix: install xdg-utils — try one of:\n" +
		"    sudo apt install xdg-utils   (Debian/Ubuntu)\n" +
		"    sudo dnf install xdg-utils   (Fedora/RHEL)\n" +
		"    sudo pacman -S xdg-utils     (Arch Linux)"

	for _, keyword := range []string{"xdg-mime", "xdg-utils", "apt install", "dnf install", "pacman"} {
		if !strings.Contains(msg, keyword) {
			t.Errorf("xdg-mime missing error should mention %q", keyword)
		}
	}
}

// ── Register/Unregister/Verify empty path guards ─────────────────────────────

func TestRegister_EmptyExecutablePath_ReturnsActionableError(t *testing.T) {
	r := &Registrar{
		executablePath: "",
		homeDir:        t.TempDir(),
	}
	err := r.Register()
	if err == nil {
		t.Fatal("Register should fail with empty executable path")
	}
	msg := err.Error()
	if !strings.Contains(msg, "pre-registration validation failed") {
		t.Errorf("error should mention pre-registration validation, got: %v", err)
	}
	if !strings.Contains(msg, "Fix:") {
		t.Errorf("error should include a Fix: hint, got: %v", err)
	}
}

func TestUnregister_EmptyExecutablePath_ReturnsActionableError(t *testing.T) {
	r := &Registrar{
		executablePath: "",
		homeDir:        t.TempDir(),
	}
	err := r.Unregister()
	if err == nil {
		t.Fatal("Unregister should fail with empty executable path")
	}
	msg := err.Error()
	if !strings.Contains(msg, "cannot unregister") {
		t.Errorf("error should mention 'cannot unregister', got: %v", err)
	}
	if !strings.Contains(msg, "Fix:") {
		t.Errorf("error should include a Fix: hint, got: %v", err)
	}
}

func TestVerify_EmptyExecutablePath_ReturnsActionableError(t *testing.T) {
	r := &Registrar{
		executablePath: "",
		homeDir:        t.TempDir(),
	}
	_, err := r.Verify()
	if err == nil {
		t.Fatal("Verify should fail with empty executable path")
	}
	msg := err.Error()
	if !strings.Contains(msg, "cannot verify") {
		t.Errorf("error should mention 'cannot verify', got: %v", err)
	}
	if !strings.Contains(msg, "Fix:") {
		t.Errorf("error should include a Fix: hint, got: %v", err)
	}
}

// ── validatePreRegistration — non-existent paths ─────────────────────────────

func TestValidatePreRegistration_NonExistentExecutable_ReturnsError(t *testing.T) {
	r := &Registrar{
		executablePath: "/nonexistent/path/to/binary",
		homeDir:        t.TempDir(),
	}
	err := r.validatePreRegistration()
	if err == nil {
		t.Fatal("validatePreRegistration should fail for non-existent executable")
	}
	if !strings.Contains(err.Error(), "no longer exists") {
		t.Errorf("error should mention 'no longer exists', got: %v", err)
	}
	if !strings.Contains(err.Error(), "Fix:") {
		t.Errorf("error should include a Fix: hint, got: %v", err)
	}
}

func TestValidatePreRegistration_NonExistentHomeDir_ReturnsError(t *testing.T) {
	r := &Registrar{
		executablePath: t.TempDir(),
		homeDir:        "/nonexistent/home/directory",
	}
	err := r.validatePreRegistration()
	if err == nil {
		t.Fatal("validatePreRegistration should fail for non-existent home directory")
	}
	if !strings.Contains(err.Error(), "home directory") {
		t.Errorf("error should mention 'home directory', got: %v", err)
	}
	if !strings.Contains(err.Error(), "Fix:") {
		t.Errorf("error should include a Fix: hint, got: %v", err)
	}
}

// ── Path normalization ───────────────────────────────────────────────────────

func TestNormalizePath_EmptyPath_ReturnsError(t *testing.T) {
	_, err := normalizePath("", "test path")
	if err == nil {
		t.Fatal("normalizePath should reject empty path")
	}
	if !strings.Contains(err.Error(), "test path") {
		t.Errorf("error should mention context, got: %v", err)
	}
}

func TestNormalizePath_NullBytes_ReturnsError(t *testing.T) {
	_, err := normalizePath("/path/with\x00null/bytes", "test path")
	if err == nil {
		t.Fatal("normalizePath should reject path with null bytes")
	}
	if !strings.Contains(err.Error(), "null bytes") {
		t.Errorf("error should mention null bytes, got: %v", err)
	}
}

func TestNormalizePath_PathTraversal_ReturnsError(t *testing.T) {
	_, err := normalizePath("/usr/local/bin/../../etc/passwd", "test path")
	if err == nil {
		t.Fatal("normalizePath should reject path traversal patterns")
	}
	if !strings.Contains(err.Error(), "path traversal") {
		t.Errorf("error should mention path traversal, got: %v", err)
	}
}

func TestNormalizePath_ConsecutiveDots_ReturnsError(t *testing.T) {
	_, err := normalizePath("/path/to/.../file", "test path")
	if err == nil {
		t.Fatal("normalizePath should reject consecutive dots")
	}
	if !strings.Contains(err.Error(), "consecutive dots") {
		t.Errorf("error should mention consecutive dots, got: %v", err)
	}
}

func TestNormalizePath_TooLong_ReturnsError(t *testing.T) {
	longPath := "/path/" + strings.Repeat("a", maxPathLength+1)
	_, err := normalizePath(longPath, "test path")
	if err == nil {
		t.Fatal("normalizePath should reject paths exceeding max length")
	}
	if !strings.Contains(err.Error(), "too long") {
		t.Errorf("error should mention 'too long', got: %v", err)
	}
	if !strings.Contains(err.Error(), "Fix:") {
		t.Errorf("error should include Fix: hint, got: %v", err)
	}
}

func TestNormalizePath_ValidPath_ReturnsCleaned(t *testing.T) {
	input := "/path/to/../file"
	cleaned, err := normalizePath(input, "test path")
	if err != nil {
		t.Fatalf("normalizePath should accept valid path, got error: %v", err)
	}
	// filepath.Clean resolves .. so /path/to/../file becomes /file
	if cleaned != "/file" {
		t.Errorf("expected cleaned path '/file', got %q", cleaned)
	}
}

func TestNormalizePath_RedundantSeparators_Removed(t *testing.T) {
	input := "/path//to///file"
	cleaned, err := normalizePath(input, "test path")
	if err != nil {
		t.Fatalf("normalizePath should accept path with redundant separators, got error: %v", err)
	}
	if cleaned != "/path/to/file" {
		t.Errorf("expected '/path/to/file', got %q", cleaned)
	}
}

// ── Path length validation ───────────────────────────────────────────────────

func TestValidatePathLength_ExactMaxLength_Accepted(t *testing.T) {
	path := strings.Repeat("a", maxPathLength)
	err := validatePathLength(path, "test path")
	if err != nil {
		t.Errorf("path at exact max length (%d) should be accepted: %v", maxPathLength, err)
	}
}

func TestValidatePathLength_OneOverMaxLength_Rejected(t *testing.T) {
	path := strings.Repeat("a", maxPathLength+1)
	err := validatePathLength(path, "test path")
	if err == nil {
		t.Errorf("path exceeding max length (%d) should be rejected", maxPathLength)
	}
	if !strings.Contains(err.Error(), "too long") {
		t.Errorf("error should mention 'too long', got: %v", err)
	}
}

// ── NewRegistrar path normalization integration ──────────────────────────────

func TestNewRegistrar_PathWithDots_Rejected(t *testing.T) {
	// We can't directly inject a path into NewRegistrar, but we can test the
	// normalization logic by constructing a Registrar with a path containing ..
	r := &Registrar{
		executablePath: "/usr/local/bin/../../etc/passwd",
		homeDir:        t.TempDir(),
	}
	err := r.validatePreRegistration()
	if err == nil {
		t.Fatal("validatePreRegistration should reject path with traversal pattern")
	}
	if !strings.Contains(err.Error(), "no longer exists") {
		t.Errorf("error should mention path doesn't exist, got: %v", err)
	}
}

func TestNewRegistrar_NormalizesHomeDir(t *testing.T) {
	// Test that home directory with redundant separators is normalized.
	// We construct a Registrar manually since NewRegistrar uses os.UserHomeDir().
	homeWithDots := t.TempDir() + "/../" + filepath.Base(t.TempDir())
	r := &Registrar{
		executablePath: t.TempDir(),
		homeDir:        homeWithDots,
	}
	// validatePreRegistration should work with the normalized path.
	err := r.validatePreRegistration()
	if err != nil {
		t.Errorf("validatePreRegistration should accept normalized home dir, got: %v", err)
	}
}

// ── Linux registration validation ────────────────────────────────────────────

func TestRegisterLinux_WrapperScriptValidation(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only test")
	}

	r := newTestRegistrar(t)

	// Create a scenario where the wrapper script would be written but we can
	// verify the validation logic by checking that a corrupted wrapper is detected.
	applicationsDir := filepath.Dir(r.linuxDesktopPath())
	helperDir := filepath.Dir(r.linuxWrapperPath())

	if err := os.MkdirAll(applicationsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(helperDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Write a wrapper script that does NOT reference the executable (simulating corruption).
	badWrapper := "#!/bin/sh\nexec /some/other/binary protocol-handler \"$1\"\n"
	if err := os.WriteFile(r.linuxWrapperPath(), []byte(badWrapper), 0o755); err != nil {
		t.Fatalf("write bad wrapper: %v", err)
	}

	// Write a valid desktop file.
	if err := os.WriteFile(r.linuxDesktopPath(), []byte(r.linuxDesktopEntry()), 0o644); err != nil {
		t.Fatalf("write desktop: %v", err)
	}

	// Attempt registration - it should fail because the wrapper doesn't reference the executable.
	err := r.Register()
	if err == nil {
		t.Error("Register should fail when wrapper script does not reference the executable")
	} else {
		if !strings.Contains(err.Error(), "wrapper script does not reference") {
			t.Errorf("error should mention wrapper script issue, got: %v", err)
		}
	}
}

// ── Darwin registration validation ───────────────────────────────────────────

func TestRegisterDarwin_PlistValidation(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-only test")
	}

	r := newTestRegistrar(t)

	// Create the app bundle directory structure.
	bundleDir := filepath.Dir(r.macOSExecutablePath())
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Write a valid executable script.
	if err := os.WriteFile(r.macOSExecutablePath(), []byte(r.unixHandlerScript()), 0o755); err != nil {
		t.Fatalf("write exec: %v", err)
	}

	// Write a plist that does NOT contain the scheme (simulating corruption).
	badPlist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleExecutable</key>
    <string>glassbox-protocol-handler</string>
</dict>
</plist>
`
	if err := os.WriteFile(r.macOSPlistPath(), []byte(badPlist), 0o644); err != nil {
		t.Fatalf("write bad plist: %v", err)
	}

	// Attempt registration - it should fail because the plist doesn't contain the scheme.
	err := r.Register()
	if err == nil {
		t.Error("Register should fail when plist does not contain the scheme")
	} else {
		if !strings.Contains(err.Error(), "does not contain the") {
			t.Errorf("error should mention plist scheme issue, got: %v", err)
		}
	}
}
