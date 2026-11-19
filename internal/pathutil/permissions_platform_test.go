// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package pathutil_test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestTempDirPermissions verifies that temporary directories created by the
// test harness have the expected ownership on each platform.
//
// On POSIX the temp dir must not be group- or world-writable. Windows uses
// ACL-based access control, so the POSIX mode bits are not meaningful there;
// the test records the mode for diagnostic purposes only.
func TestTempDirPermissions(t *testing.T) {
	dir := t.TempDir()

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Stat temp dir: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected directory, got mode %v", info.Mode())
	}

	if runtime.GOOS == "windows" {
		t.Logf("Windows: temp dir mode = %v (POSIX modes not enforced)", info.Mode())
		return
	}

	mode := info.Mode().Perm()
	if mode&0o077 != 0 {
		t.Errorf("temp dir is group/world-accessible on %s: mode %04o", runtime.GOOS, mode)
	}
}

// TestFilePermissionsRoundTrip writes a file with mode 0600 and verifies that
// os.Stat reports the same mode back. On Windows the assertion is advisory.
func TestFilePermissionsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "permtest.txt")

	if err := os.WriteFile(path, []byte("content"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}

	if runtime.GOOS == "windows" {
		t.Logf("Windows: file mode = %v (POSIX modes not enforced)", info.Mode())
		return
	}

	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("expected mode 0600, got %04o", got)
	}
}

// TestFilePermissionsReadOnly verifies that a mode-0444 file cannot be
// overwritten on POSIX.
//
// Skipped on Windows (ACL model) and when running as root (bypasses mode bits).
func TestFilePermissionsReadOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows uses ACL-based permissions; POSIX read-only semantics do not apply")
	}
	if os.Getuid() == 0 {
		t.Skip("root bypasses POSIX permission checks")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "readonly.txt")

	if err := os.WriteFile(path, []byte("initial"), 0o444); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })

	if err := os.WriteFile(path, []byte("overwrite"), 0o444); err == nil {
		t.Errorf("platform=%s: expected write to read-only file to fail, but it succeeded", runtime.GOOS)
	}
}

// TestSymlinkPermissions verifies that os.Lstat on a symlink reports the link
// mode rather than the target mode.
//
// Skipped on Windows where symlink creation requires elevated privileges or
// Developer Mode and is rarely available in standard CI.
func TestSymlinkPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges or Developer Mode on Windows")
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	link := filepath.Join(dir, "link.txt")

	if err := os.WriteFile(target, []byte("data"), 0o644); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	li, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("Lstat: %v", err)
	}
	if li.Mode()&os.ModeSymlink == 0 {
		t.Errorf("Lstat on symlink should report ModeSymlink; got %v", li.Mode())
	}
}

// TestTempFileCleanup verifies that multiple files can be created in a temp
// dir and that os.ReadDir returns the correct count. The test harness removes
// the dir after the test; this test only checks behaviour while it is live.
func TestTempFileCleanup(t *testing.T) {
	dir := t.TempDir()

	const n = 5
	for i := range n {
		p := filepath.Join(dir, fmt.Sprintf("file%d.tmp", i))
		if err := os.WriteFile(p, []byte("data"), 0o600); err != nil {
			t.Fatalf("WriteFile %d: %v", i, err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != n {
		t.Errorf("expected %d files in temp dir, got %d", n, len(entries))
	}
}

// TestPathSeparatorsInTempDir writes a file inside a nested subdirectory
// created with filepath.Join and verifies it is readable on all platforms.
// This confirms that the OS path separator embedded in the filepath is correct.
func TestPathSeparatorsInTempDir(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "a", "b", "c")

	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	f := filepath.Join(sub, "test.txt")
	if err := os.WriteFile(f, []byte("hello"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	data, err := os.ReadFile(f)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("ReadFile: got %q, want %q", data, "hello")
	}
}

// TestFileExecutableBit verifies that the executable bit can be set on POSIX
// and that it is reflected by os.Stat. Skipped on Windows.
func TestFileExecutableBit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose a POSIX executable bit")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "script.sh")

	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("platform=%s: expected executable bit set; mode %04o", runtime.GOOS, info.Mode().Perm())
	}
}

// TestTempDirPlatformSeparator verifies that t.TempDir() returns a path that
// uses the native OS path separator and contains no mixed separators.
func TestTempDirPlatformSeparator(t *testing.T) {
	dir := t.TempDir()

	switch runtime.GOOS {
	case "windows":
		for _, c := range dir {
			if c == '/' {
				t.Errorf("TempDir path contains forward slash on Windows: %q", dir)
				break
			}
		}
	default:
		for _, c := range dir {
			if c == '\\' {
				t.Errorf("TempDir path contains backslash on %s: %q", runtime.GOOS, dir)
				break
			}
		}
	}
}
