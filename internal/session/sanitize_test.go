// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeErrorMessage_RemovesHomePath(t *testing.T) {
	msg := "failed to open /home/alice/.Glassbox/sessions.db: permission denied"
	got := SanitizeErrorMessage(msg)
	if strings.Contains(got, "alice") {
		t.Errorf("sanitized message must not contain username, got: %q", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Errorf("sanitized message should contain [REDACTED], got: %q", got)
	}
}

func TestSanitizeErrorMessage_RemovesMacOSPath(t *testing.T) {
	msg := "failed to open /Users/bob/.Glassbox/sessions.db"
	got := SanitizeErrorMessage(msg)
	if strings.Contains(got, "bob") {
		t.Errorf("sanitized message must not contain username, got: %q", got)
	}
}

func TestSanitizeErrorMessage_RemovesStellarSecretKey(t *testing.T) {
	secret := "SCZANGBA5AKIA4TDDKXGAI2NOOZVQZAHJPNZB3ZFEAKEOUYP4HFHGHNR"
	msg := "invalid key: " + secret
	got := SanitizeErrorMessage(msg)
	if strings.Contains(got, secret) {
		t.Errorf("sanitized message must not contain Stellar secret key, got: %q", got)
	}
}

func TestSanitizeErrorMessage_PreservesNonPII(t *testing.T) {
	msg := "session not found: abc123-session"
	got := SanitizeErrorMessage(msg)
	if got != msg {
		t.Errorf("non-PII message should be unchanged, got: %q", got)
	}
}

func TestSanitizeDBPath_ReplacesHomeDir(t *testing.T) {
	path := "/home/alice/.Glassbox/sessions.db"
	got := SanitizeDBPath(path)
	if strings.Contains(got, "alice") {
		t.Errorf("sanitized path must not contain username, got: %q", got)
	}
	if !strings.HasPrefix(got, "~") {
		t.Errorf("sanitized path should start with ~, got: %q", got)
	}
}

func TestSanitizeDBPath_ReplacesUsersDir(t *testing.T) {
	path := "/Users/bob/.Glassbox/sessions.db"
	got := SanitizeDBPath(path)
	if strings.Contains(got, "bob") {
		t.Errorf("sanitized path must not contain username, got: %q", got)
	}
}

func TestSanitizeDBPath_NoHomeDirUnchanged(t *testing.T) {
	path := "/tmp/sessions.db"
	got := SanitizeDBPath(path)
	if got != path {
		t.Errorf("path without home dir should be unchanged, got: %q", got)
	}
}

func TestRedactTxHash_ShortenLongHash(t *testing.T) {
	hash := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	got := RedactTxHash(hash)
	if len(got) > 12 {
		t.Errorf("redacted hash should be short, got: %q (len %d)", got, len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("redacted hash should end with ..., got: %q", got)
	}
}

func TestRedactTxHash_ShortHashUnchanged(t *testing.T) {
	hash := "abc123"
	got := RedactTxHash(hash)
	if got != hash {
		t.Errorf("short hash should be unchanged, got: %q", got)
	}
}

func TestRedactTxHash_NineCharHashUnchanged(t *testing.T) {
	hash := "abc123456"
	got := RedactTxHash(hash)
	if got != hash {
		t.Errorf("9-char hash should be unchanged, got: %q", got)
	}
}

func TestWrapStoreError_SanitizesPath(t *testing.T) {
	err := wrapTestErr("/home/alice/.Glassbox/sessions.db: disk full")
	wrapped := WrapStoreError("save", "/home/alice/.Glassbox/sessions.db", err)
	if wrapped == nil {
		t.Fatal("WrapStoreError should not return nil for non-nil error")
	}
	if strings.Contains(wrapped.Error(), "alice") {
		t.Errorf("wrapped error must not contain username, got: %q", wrapped.Error())
	}
}

func TestWrapStoreError_NilError_ReturnsNil(t *testing.T) {
	if WrapStoreError("load", "/some/path", nil) != nil {
		t.Error("WrapStoreError with nil error should return nil")
	}
}

// wrapTestErr creates a simple error for test use.
type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }

func wrapTestErr(msg string) error { return &testError{msg: msg} }

func TestSanitizeErrorMessage_RemovesNonCDriveWindowsPath(t *testing.T) {
	msg := `failed to open D:\Users\bob\file.txt: access denied`
	got := SanitizeErrorMessage(msg)
	if strings.Contains(got, "bob") {
		t.Errorf("sanitized message must not contain username, got: %q", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Errorf("sanitized message should contain [REDACTED], got: %q", got)
	}
}

func TestSanitizeErrorMessage_RemovesWindowsPathAnyDrive(t *testing.T) {
	msg := "failed to open D:\\Users\\carol\\.Glassbox\\sessions.db"
	got := SanitizeErrorMessage(msg)
	if strings.Contains(got, "carol") {
		t.Errorf("sanitized message must not contain username, got: %q", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Errorf("sanitized message should contain [REDACTED], got: %q", got)
	}
}

func TestValidateDBPermissions_NonExistent(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "nonexistent.db")
	if err := ValidateDBPermissions(dbPath); err != nil {
		t.Errorf("expected nil for non-existent db, got: %v", err)
	}
}

func TestValidateDBPermissions_ReadOnly(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "readonly.db")
	if err := os.WriteFile(dbPath, []byte("test-data"), 0400); err != nil {
		t.Fatal(err)
	}
	if err := ValidateDBPermissions(dbPath); err != nil {
		t.Errorf("expected read-only db to pass ValidateDBPermissions, got: %v", err)
	}
}

func TestValidateDBPermissions_Unreadable(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "noperms.db")
	if err := os.WriteFile(dbPath, []byte("test-data"), 0000); err != nil {
		t.Fatal(err)
	}
	err := ValidateDBPermissions(dbPath)
	if err != nil && !strings.Contains(err.Error(), "is not readable (permission denied)") {
		t.Errorf("expected error message to contain 'is not readable (permission denied)', got: %q", err.Error())
	}
}
