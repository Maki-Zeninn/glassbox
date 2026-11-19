// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// --- isAbsPath ---

func TestIsAbsPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/usr/bin/plugin", true},
		{"relative/path", false},
		{"./local", false},
		{"", false},
		{"C:\\Windows\\plugin.exe", true},
		{"\\\\server\\share", true},
	}
	for _, tc := range cases {
		got := isAbsPath(tc.path)
		if got != tc.want {
			t.Errorf("isAbsPath(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// --- joinPaths ---

func TestJoinPaths_EmptyBase(t *testing.T) {
	got := joinPaths("", "rel")
	if got != "rel" {
		t.Errorf("joinPaths(\"\", \"rel\") = %q, want %q", got, "rel")
	}
}

func TestJoinPaths_NonEmptyBase(t *testing.T) {
	base := filepath.Join("plugins", "dir") // use filepath.Join for OS-correct separator
	rel := "my-plugin"
	got := joinPaths(base, rel)
	want := base + string(os.PathSeparator) + rel
	if got != want {
		t.Errorf("joinPaths(%q, %q) = %q, want %q", base, rel, got, want)
	}
}

// --- verifyChecksum ---

func computeSHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestVerifyChecksum_Match(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "binary")
	content := []byte("hello plugin binary")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}

	expected := computeSHA256Hex(content)
	if err := verifyChecksum(path, expected); err != nil {
		t.Errorf("expected checksum to match, got: %v", err)
	}
}

func TestVerifyChecksum_Mismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "binary")
	if err := os.WriteFile(path, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := verifyChecksum(path, "deadbeefdeadbeef"); err == nil {
		t.Error("expected error for wrong checksum")
	}
}

func TestVerifyChecksum_FileNotFound(t *testing.T) {
	err := verifyChecksum("/nonexistent/binary", "abc123")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

// --- NewSandboxedPlugin ---

func TestNewSandboxedPlugin_BinaryNotFound(t *testing.T) {
	dir := t.TempDir()
	m := validManifest()
	m.Entrypoint = "nonexistent-binary"

	_, err := NewSandboxedPlugin(m, dir)
	if err == nil {
		t.Error("expected error when binary does not exist")
	}
}

func TestNewSandboxedPlugin_WithRealBinary(t *testing.T) {
	dir := t.TempDir()
	binaryPath := filepath.Join(dir, "plugin-binary")
	if err := os.WriteFile(binaryPath, []byte("#!/bin/sh\necho '{\"ok\":true}'"), 0755); err != nil {
		t.Fatal(err)
	}

	m := validManifest()
	m.Entrypoint = "plugin-binary"

	sp, err := NewSandboxedPlugin(m, dir)
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if sp.Name() != m.Name {
		t.Errorf("expected name %s, got %s", m.Name, sp.Name())
	}
	if sp.Version() != m.Version {
		t.Errorf("expected version %s, got %s", m.Version, sp.Version())
	}
}

func TestNewSandboxedPlugin_WithChecksumVerification(t *testing.T) {
	dir := t.TempDir()
	content := []byte("plugin binary content")
	binaryPath := filepath.Join(dir, "plugin-binary")
	if err := os.WriteFile(binaryPath, content, 0755); err != nil {
		t.Fatal(err)
	}

	m := validManifest()
	m.Entrypoint = "plugin-binary"
	m.Checksum = computeSHA256Hex(content)

	sp, err := NewSandboxedPlugin(m, dir)
	if err != nil {
		t.Fatalf("expected success with correct checksum, got: %v", err)
	}
	if sp == nil {
		t.Error("expected non-nil plugin")
	}
}

func TestNewSandboxedPlugin_ChecksumMismatch(t *testing.T) {
	dir := t.TempDir()
	binaryPath := filepath.Join(dir, "plugin-binary")
	if err := os.WriteFile(binaryPath, []byte("content"), 0755); err != nil {
		t.Fatal(err)
	}

	m := validManifest()
	m.Entrypoint = "plugin-binary"
	m.Checksum = "wrongchecksum"

	_, err := NewSandboxedPlugin(m, dir)
	if err == nil {
		t.Error("expected error for checksum mismatch")
	}
}

// --- SandboxedPlugin methods ---

func TestSandboxedPlugin_CanDecode(t *testing.T) {
	dir := t.TempDir()
	binaryPath := filepath.Join(dir, "plugin-binary")
	if err := os.WriteFile(binaryPath, []byte("binary"), 0755); err != nil {
		t.Fatal(err)
	}

	m := validManifest()
	m.Entrypoint = "plugin-binary"
	m.EventTypes = []string{"stellar.event", "soroban.call"}

	sp, err := NewSandboxedPlugin(m, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !sp.CanDecode("stellar.event") {
		t.Error("expected CanDecode(stellar.event) = true")
	}
	if !sp.CanDecode("soroban.call") {
		t.Error("expected CanDecode(soroban.call) = true")
	}
	if sp.CanDecode("unknown.event") {
		t.Error("expected CanDecode(unknown.event) = false")
	}
}

func TestSandboxedPlugin_Metadata(t *testing.T) {
	dir := t.TempDir()
	binaryPath := filepath.Join(dir, "plugin-binary")
	if err := os.WriteFile(binaryPath, []byte("binary"), 0755); err != nil {
		t.Fatal(err)
	}

	m := validManifest()
	m.Entrypoint = "plugin-binary"
	m.Description = "A test plugin"
	m.EventTypes = []string{"test.event"}

	sp, err := NewSandboxedPlugin(m, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	meta := sp.Metadata()
	if meta.Name != m.Name {
		t.Errorf("expected name %s, got %s", m.Name, meta.Name)
	}
	if meta.Description != m.Description {
		t.Errorf("expected description %s, got %s", m.Description, meta.Description)
	}
	if len(meta.EventTypes) != 1 || meta.EventTypes[0] != "test.event" {
		t.Errorf("unexpected event types: %v", meta.EventTypes)
	}
}

// --- Decode isolation: misbehaving plugin does not crash host ---

func TestSandboxedPlugin_DecodeWithCrashingBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script not supported on Windows")
	}

	dir := t.TempDir()
	script := "#!/bin/sh\nexit 1\n"
	binaryPath := filepath.Join(dir, "crash-plugin")
	if err := os.WriteFile(binaryPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	m := validManifest()
	m.Entrypoint = "crash-plugin"
	m.EventTypes = []string{"crash.event"}

	sp, err := NewSandboxedPlugin(m, dir)
	if err != nil {
		t.Fatalf("unexpected error creating plugin: %v", err)
	}

	// Decode must return an error, not crash the test process.
	data, _ := json.Marshal(map[string]string{"key": "value"})
	_, err = sp.Decode(data)
	if err == nil {
		t.Error("expected error from crashing plugin")
	}
}

func TestSandboxedPlugin_DecodeWithHangingBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script not supported on Windows")
	}

	dir := t.TempDir()
	script := "#!/bin/sh\nexec sleep 999\n"
	binaryPath := filepath.Join(dir, "hang-plugin")
	if err := os.WriteFile(binaryPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	m := validManifest()
	m.Entrypoint = "hang-plugin"
	m.EventTypes = []string{"hang.event"}

	sp, err := NewSandboxedPlugin(m, dir)
	if err != nil {
		t.Fatalf("unexpected error creating plugin: %v", err)
	}

	// The context timeout will kill the process; we just verify it returns an error.
	data, _ := json.Marshal(map[string]string{"key": "value"})
	_, err = sp.Decode(data)
	if err == nil {
		t.Error("expected error from hanging plugin")
	}
}

// --- buildSandboxEnv ---

func TestBuildSandboxEnv_ContainsRequiredVars(t *testing.T) {
	m := validManifest()
	env := buildSandboxEnv(m)

	hasPrefix := func(prefix string) bool {
		for _, e := range env {
			if strings.HasPrefix(e, prefix) {
				return true
			}
		}
		return false
	}

	if !hasPrefix("GLASSBOX_PLUGIN_NAME=") {
		t.Error("expected GLASSBOX_PLUGIN_NAME in env")
	}
	if !hasPrefix("GLASSBOX_PLUGIN_VERSION=") {
		t.Error("expected GLASSBOX_PLUGIN_VERSION in env")
	}
	if !hasPrefix("GLASSBOX_API_VERSION=") {
		t.Error("expected GLASSBOX_API_VERSION in env")
	}
}

func TestBuildSandboxEnv_ContainsPluginValues(t *testing.T) {
	m := validManifest()
	m.Name = "my-special-plugin"
	m.Version = "2.3.4"

	env := buildSandboxEnv(m)

	found := false
	for _, e := range env {
		if e == "GLASSBOX_PLUGIN_NAME=my-special-plugin" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected GLASSBOX_PLUGIN_NAME=my-special-plugin in env, got: %v", env)
	}
}
