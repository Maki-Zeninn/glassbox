// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// withTempHome points HOME / USERPROFILE to a temporary directory for the
// duration of the test, then restores it.
func withTempHome(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()

	origHome := os.Getenv("HOME")
	origUserProfile := os.Getenv("USERPROFILE")
	t.Setenv("HOME", tmp)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", tmp)
	}
	t.Cleanup(func() {
		os.Setenv("HOME", origHome)
		if runtime.GOOS == "windows" {
			os.Setenv("USERPROFILE", origUserProfile)
		}
	})
	return tmp
}

// clearTelemetryEnv unsets GLASSBOX_TELEMETRY for the duration of the test.
func clearTelemetryEnv(t *testing.T) {
	t.Helper()
	orig, set := os.LookupEnv("GLASSBOX_TELEMETRY")
	os.Unsetenv("GLASSBOX_TELEMETRY")
	t.Cleanup(func() {
		if set {
			os.Setenv("GLASSBOX_TELEMETRY", orig)
		} else {
			os.Unsetenv("GLASSBOX_TELEMETRY")
		}
	})
}

// ─── ReadConsent ─────────────────────────────────────────────────────────────

// TestReadConsent_MissingFile returns a zero ConsentState with nil error when
// the consent file does not exist.
func TestReadConsent_MissingFile(t *testing.T) {
	withTempHome(t)

	state, err := ReadConsent()
	if err != nil {
		t.Fatalf("ReadConsent with no file: want nil error, got %v", err)
	}
	if state.Enabled {
		t.Error("ReadConsent with no file: want Enabled=false (default disabled)")
	}
}

// TestReadConsent_MalformedFile returns an error when the file contains
// invalid JSON.
func TestReadConsent_MalformedFile(t *testing.T) {
	home := withTempHome(t)
	dir := filepath.Join(home, ".Glassbox")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, consentFileName)
	if err := os.WriteFile(path, []byte("{not valid json"), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := ReadConsent()
	if err == nil {
		t.Fatal("ReadConsent with malformed file: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "malformed") {
		t.Errorf("error should mention 'malformed', got: %v", err)
	}
}

// TestReadConsent_ValidFile round-trips a consent record.
func TestReadConsent_ValidFile(t *testing.T) {
	home := withTempHome(t)
	dir := filepath.Join(home, ".Glassbox")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, consentFileName)

	want := ConsentState{Enabled: true, UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
	data, _ := json.MarshalIndent(want, "", "  ")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	got, err := ReadConsent()
	if err != nil {
		t.Fatalf("ReadConsent valid file: unexpected error: %v", err)
	}
	if got.Enabled != want.Enabled {
		t.Errorf("Enabled: got %v, want %v", got.Enabled, want.Enabled)
	}
	if got.UpdatedAt != want.UpdatedAt {
		t.Errorf("UpdatedAt: got %q, want %q", got.UpdatedAt, want.UpdatedAt)
	}
}

// TestReadConsent_UnreadableFile returns an error (not a panic) when the file
// exists but cannot be read (permissions denied). Skipped on Windows where
// file permission enforcement differs.
func TestReadConsent_UnreadableFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permission test not applicable on Windows")
	}
	home := withTempHome(t)
	dir := filepath.Join(home, ".Glassbox")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, consentFileName)
	if err := os.WriteFile(path, []byte(`{"enabled":true,"updated_at":"2026-01-01T00:00:00Z"}`), 0600); err != nil {
		t.Fatal(err)
	}
	// Remove read permission.
	if err := os.Chmod(path, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(path, 0600) }) //nolint:errcheck

	_, err := ReadConsent()
	if err == nil {
		t.Fatal("ReadConsent unreadable file: expected error, got nil")
	}
}

// ─── WriteConsent ─────────────────────────────────────────────────────────────

// TestWriteConsent_CreatesFileWithCorrectPermissions verifies the file is
// written with 0600 permissions and valid content.
func TestWriteConsent_CreatesFileWithCorrectPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permission check not reliable on Windows")
	}
	home := withTempHome(t)

	if err := WriteConsent(true); err != nil {
		t.Fatalf("WriteConsent(true): unexpected error: %v", err)
	}

	path := filepath.Join(home, ".Glassbox", consentFileName)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("consent file not found after WriteConsent: %v", err)
	}
	if perm := info.Mode().Perm(); perm != consentFilePerms {
		t.Errorf("file permissions: got %04o, want %04o", perm, consentFilePerms)
	}
}

// TestWriteConsent_RoundTrip writes and reads back both states.
func TestWriteConsent_RoundTrip(t *testing.T) {
	withTempHome(t)

	for _, enabled := range []bool{true, false} {
		if err := WriteConsent(enabled); err != nil {
			t.Fatalf("WriteConsent(%v): %v", enabled, err)
		}
		state, err := ReadConsent()
		if err != nil {
			t.Fatalf("ReadConsent after WriteConsent(%v): %v", enabled, err)
		}
		if state.Enabled != enabled {
			t.Errorf("Enabled: got %v, want %v", state.Enabled, enabled)
		}
		if state.UpdatedAt == "" {
			t.Error("UpdatedAt should be set after WriteConsent")
		}
		// UpdatedAt must parse as RFC3339.
		if _, err := time.Parse(time.RFC3339, state.UpdatedAt); err != nil {
			t.Errorf("UpdatedAt %q is not RFC3339: %v", state.UpdatedAt, err)
		}
	}
}

// TestWriteConsent_CreatesParentDir verifies the parent directory is created
// when it does not exist.
func TestWriteConsent_CreatesParentDir(t *testing.T) {
	home := withTempHome(t)
	// Ensure the .Glassbox dir does not exist yet.
	if err := os.RemoveAll(filepath.Join(home, ".Glassbox")); err != nil {
		t.Fatal(err)
	}

	if err := WriteConsent(false); err != nil {
		t.Fatalf("WriteConsent with no dir: %v", err)
	}

	path := filepath.Join(home, ".Glassbox", consentFileName)
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file should exist after WriteConsent: %v", err)
	}
}

// ─── ResolveConsent ────────────────────────────────────────────────────────────

// TestResolveConsent_EnvOverride_True verifies that GLASSBOX_TELEMETRY=true
// forces enabled regardless of the consent file.
func TestResolveConsent_EnvOverride_True(t *testing.T) {
	withTempHome(t)
	// Write a disabled consent file.
	if err := WriteConsent(false); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GLASSBOX_TELEMETRY", "true")

	ec := ResolveConsent()
	if ec.Source != ConsentSourceEnv {
		t.Errorf("Source: got %v, want ConsentSourceEnv", ec.Source)
	}
	if !ec.Enabled {
		t.Error("Enabled: expected true when GLASSBOX_TELEMETRY=true")
	}
}

// TestResolveConsent_EnvOverride_False verifies that GLASSBOX_TELEMETRY=false
// forces disabled regardless of the consent file.
func TestResolveConsent_EnvOverride_False(t *testing.T) {
	withTempHome(t)
	// Write an enabled consent file.
	if err := WriteConsent(true); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GLASSBOX_TELEMETRY", "false")

	ec := ResolveConsent()
	if ec.Source != ConsentSourceEnv {
		t.Errorf("Source: got %v, want ConsentSourceEnv", ec.Source)
	}
	if ec.Enabled {
		t.Error("Enabled: expected false when GLASSBOX_TELEMETRY=false")
	}
}

// TestResolveConsent_EnvOverride_Variants tests multiple boolean representations
// accepted by the env var.
func TestResolveConsent_EnvOverride_Variants(t *testing.T) {
	cases := []struct {
		envVal  string
		enabled bool
	}{
		{"1", true},
		{"yes", true},
		{"on", true},
		{"TRUE", true},
		{"0", false},
		{"no", false},
		{"off", false},
		{"FALSE", false},
	}
	for _, tc := range cases {
		t.Run(tc.envVal, func(t *testing.T) {
			withTempHome(t)
			t.Setenv("GLASSBOX_TELEMETRY", tc.envVal)
			ec := ResolveConsent()
			if ec.Source != ConsentSourceEnv {
				t.Errorf("Source: got %v, want ConsentSourceEnv for env=%q", ec.Source, tc.envVal)
			}
			if ec.Enabled != tc.enabled {
				t.Errorf("Enabled: got %v, want %v for env=%q", ec.Enabled, tc.enabled, tc.envVal)
			}
		})
	}
}

// TestResolveConsent_FileEnabled verifies the consent file is used when there
// is no env override and the user has opted in.
func TestResolveConsent_FileEnabled(t *testing.T) {
	withTempHome(t)
	clearTelemetryEnv(t)

	if err := WriteConsent(true); err != nil {
		t.Fatal(err)
	}

	ec := ResolveConsent()
	if ec.Source != ConsentSourceFile {
		t.Errorf("Source: got %v, want ConsentSourceFile", ec.Source)
	}
	if !ec.Enabled {
		t.Error("Enabled: expected true when consent file has enabled=true")
	}
}

// TestResolveConsent_FileDisabled verifies the consent file opt-out is respected.
func TestResolveConsent_FileDisabled(t *testing.T) {
	withTempHome(t)
	clearTelemetryEnv(t)

	if err := WriteConsent(false); err != nil {
		t.Fatal(err)
	}

	ec := ResolveConsent()
	if ec.Source != ConsentSourceFile {
		t.Errorf("Source: got %v, want ConsentSourceFile", ec.Source)
	}
	if ec.Enabled {
		t.Error("Enabled: expected false when consent file has enabled=false")
	}
}

// TestResolveConsent_Default verifies that without env or file, telemetry is
// disabled (opt-in by default).
func TestResolveConsent_Default(t *testing.T) {
	withTempHome(t)
	clearTelemetryEnv(t)

	ec := ResolveConsent()
	if ec.Source != ConsentSourceDefault {
		t.Errorf("Source: got %v, want ConsentSourceDefault", ec.Source)
	}
	if ec.Enabled {
		t.Error("Enabled: expected false when no env or file (default disabled)")
	}
}

// TestResolveConsent_MalformedFile_FallsBackToDefault verifies that a
// malformed consent file silently degrades to "disabled" instead of surfacing
// an error that would break the CLI.
func TestResolveConsent_MalformedFile_FallsBackToDefault(t *testing.T) {
	home := withTempHome(t)
	clearTelemetryEnv(t)

	dir := filepath.Join(home, ".Glassbox")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, consentFileName)
	if err := os.WriteFile(path, []byte("not valid json"), 0600); err != nil {
		t.Fatal(err)
	}

	ec := ResolveConsent()
	if ec.Enabled {
		t.Error("Enabled: expected false for malformed consent file (safe default)")
	}
}

// TestResolveConsent_EnvPrecedenceOverMalformedFile proves that even when the
// consent file is broken, the env var still sets the effective state.
func TestResolveConsent_EnvPrecedenceOverMalformedFile(t *testing.T) {
	home := withTempHome(t)
	dir := filepath.Join(home, ".Glassbox")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, consentFileName)
	if err := os.WriteFile(path, []byte("garbage"), 0600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GLASSBOX_TELEMETRY", "true")

	ec := ResolveConsent()
	if ec.Source != ConsentSourceEnv {
		t.Errorf("Source: got %v, want ConsentSourceEnv", ec.Source)
	}
	if !ec.Enabled {
		t.Error("Enabled: expected true from env even with broken consent file")
	}
}

// TestResolveConsent_UnreadableFile_FallsBackToDefault checks that an
// unreadable file (permissions denied) safely degrades. Skipped on Windows.
func TestResolveConsent_UnreadableFile_FallsBackToDefault(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permission test not applicable on Windows")
	}
	home := withTempHome(t)
	clearTelemetryEnv(t)

	dir := filepath.Join(home, ".Glassbox")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, consentFileName)
	if err := os.WriteFile(path, []byte(`{"enabled":true,"updated_at":"2026-01-01T00:00:00Z"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(path, 0600) }) //nolint:errcheck

	ec := ResolveConsent()
	if ec.Enabled {
		t.Error("Enabled: unreadable consent file should default to disabled")
	}
}

// ─── IsTelemetryEnabled ───────────────────────────────────────────────────────

// TestIsTelemetryEnabled_EnvFalse verifies the function respects env override.
func TestIsTelemetryEnabled_EnvFalse(t *testing.T) {
	withTempHome(t)
	if err := WriteConsent(true); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GLASSBOX_TELEMETRY", "false")

	if IsTelemetryEnabled() {
		t.Error("IsTelemetryEnabled: expected false when GLASSBOX_TELEMETRY=false")
	}
}

// TestIsTelemetryEnabled_Default verifies telemetry is off without any config.
func TestIsTelemetryEnabled_Default(t *testing.T) {
	withTempHome(t)
	clearTelemetryEnv(t)
	// Reset runtime flag.
	commandTelemetryEnabled = false

	if IsTelemetryEnabled() {
		t.Error("IsTelemetryEnabled: expected false by default")
	}
}

// TestIsTelemetryEnabled_ConsentFileEnabled verifies consent file opt-in works.
func TestIsTelemetryEnabled_ConsentFileEnabled(t *testing.T) {
	withTempHome(t)
	clearTelemetryEnv(t)

	if err := WriteConsent(true); err != nil {
		t.Fatal(err)
	}

	if !IsTelemetryEnabled() {
		t.Error("IsTelemetryEnabled: expected true when consent file has enabled=true")
	}
}

// ─── ConsentFilePath ─────────────────────────────────────────────────────────

// TestConsentFilePath_ContainsGlassboxDir verifies the path includes the
// Glassbox config directory and the expected filename.
func TestConsentFilePath_ContainsGlassboxDir(t *testing.T) {
	withTempHome(t)
	p := ConsentFilePath()
	if p == "" {
		t.Fatal("ConsentFilePath should not be empty")
	}
	if !strings.Contains(p, ".Glassbox") {
		t.Errorf("ConsentFilePath %q should contain '.Glassbox'", p)
	}
	if filepath.Base(p) != consentFileName {
		t.Errorf("ConsentFilePath base: got %q, want %q", filepath.Base(p), consentFileName)
	}
}

// ─── parseBoolLoose ───────────────────────────────────────────────────────────

func TestParseBoolLoose(t *testing.T) {
	trueInputs := []string{"1", "true", "yes", "on", "TRUE", "YES", "ON"}
	falseInputs := []string{"0", "false", "no", "off", "FALSE", "NO", "OFF"}

	for _, s := range trueInputs {
		got, err := parseBoolLoose(s)
		if err != nil || !got {
			t.Errorf("parseBoolLoose(%q) = %v, %v; want true, nil", s, got, err)
		}
	}
	for _, s := range falseInputs {
		got, err := parseBoolLoose(s)
		if err != nil || got {
			t.Errorf("parseBoolLoose(%q) = %v, %v; want false, nil", s, got, err)
		}
	}

	_, err := parseBoolLoose("maybe")
	if err == nil {
		t.Error("parseBoolLoose('maybe') should return error")
	}
}
