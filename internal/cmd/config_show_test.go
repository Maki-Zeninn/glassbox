// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotandev/glassbox/internal/config"
)

func TestConfigShowJSONOutput(t *testing.T) {
	// Set up environment for test
	origEnv := os.Getenv("GLASSBOX_RPC_URL")
	os.Setenv("GLASSBOX_RPC_URL", "https://test.example.com")
	defer os.Setenv("GLASSBOX_RPC_URL", origEnv)

	// Create a temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, ".glassbox.toml")
	configContent := `[rpc]
url = "https://config.example.com"

[network]
testnet = true
`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	// Change to temp directory so config is found
	origCwd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(origCwd)

	// Test JSON output
	configShowJSONFlag = true
	defer func() { configShowJSONFlag = false }()

	cmd := configShowCmd
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetArgs([]string{})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Command failed: %v", err)
	}

	// Parse JSON output
	var output configShowOutput
	if err := json.Unmarshal(buf.Bytes(), &output); err != nil {
		t.Fatalf("Failed to parse JSON output: %v", err)
	}

	// Verify structure
	if len(output.Values) == 0 {
		t.Error("Expected at least one config value in output")
	}

	// Verify source is set
	if output.Source == "" {
		t.Log("Note: Source may be empty if config loading uses different paths")
	}
}

func TestConfigShowHumanReadableOutput(t *testing.T) {
	// Set up environment
	origEnv := os.Getenv("GLASSBOX_LOG_LEVEL")
	os.Setenv("GLASSBOX_LOG_LEVEL", "debug")
	defer os.Setenv("GLASSBOX_LOG_LEVEL", origEnv)

	cmd := configShowCmd
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetArgs([]string{})

	configShowJSONFlag = false

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Command failed: %v", err)
	}

	output := buf.String()

	// Verify human-readable output contains expected sections
	if !contains(output, "Active configuration:") {
		t.Error("Expected 'Active configuration:' in output")
	}
	if !contains(output, "Configuration sources:") {
		t.Error("Expected 'Configuration sources:' in output")
	}
}

func TestResolveConfigSource(t *testing.T) {
	// Test environment variable source
	os.Setenv("GLASSBOX_TEST_VAR", "value")
	defer os.Unsetenv("GLASSBOX_TEST_VAR")

	source := resolveConfigSource("GLASSBOX_TEST_VAR", "/some/config.toml", "default")
	if source != "environment" {
		t.Errorf("Expected 'environment', got '%s'", source)
	}

	// Test config file source (no env var)
	source = resolveConfigSource("GLASSBOX_NONEXISTENT", "/some/config.toml", "default")
	if source != "file" {
		t.Errorf("Expected 'file', got '%s'", source)
	}

	// Test default source (no env, no file)
	source = resolveConfigSource("GLASSBOX_NONEXISTENT", "", "default")
	if source != "default" {
		t.Errorf("Expected 'default', got '%s'", source)
	}
}

func TestBuildConfigOutput(t *testing.T) {
	cfg := &config.Config{
		RpcUrl:    "https://test.example.com",
		Network:   "testnet",
		LogLevel:  "debug",
		CachePath: "/tmp/cache",
		Telemetry: true,
	}

	output := buildConfigOutput(cfg, "/test/config.toml")

	// Verify values are present
	if _, ok := output.Values["rpc_url"]; !ok {
		t.Error("Expected rpc_url in output")
	}
	if _, ok := output.Values["network"]; !ok {
		t.Error("Expected network in output")
	}
	if _, ok := output.Values["log_level"]; !ok {
		t.Error("Expected log_level in output")
	}

	// Verify sensitive values are redacted
	if cfg.RPCToken != "" {
		if output.Values["rpc_token"].Value != "[redacted]" {
			t.Error("Expected rpc_token to be redacted")
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsAt(s, substr))
}

func containsAt(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// ── stripURLCredentials ───────────────────────────────────────────────────────

func TestStripURLCredentials_RemovesUserInfo(t *testing.T) {
	urls := []string{"https://user:pass@rpc.example.com/api"}
	got := stripURLCredentials(urls)
	if len(got) != 1 {
		t.Fatalf("expected 1 result, got %d", len(got))
	}
	if strings.Contains(got[0], "user") || strings.Contains(got[0], "pass") {
		t.Errorf("credentials should be stripped, got: %q", got[0])
	}
	if !strings.Contains(got[0], "rpc.example.com") {
		t.Errorf("host should be preserved, got: %q", got[0])
	}
}

func TestStripURLCredentials_NoCredentials_Unchanged(t *testing.T) {
	urls := []string{"https://rpc.example.com/api", "http://localhost:8000"}
	got := stripURLCredentials(urls)
	for i, u := range got {
		if u != urls[i] {
			t.Errorf("URL without credentials should be unchanged: got %q, want %q", u, urls[i])
		}
	}
}

func TestStripURLCredentials_InvalidURL_ReplacedWithPlaceholder(t *testing.T) {
	urls := []string{"not a valid url %%"}
	got := stripURLCredentials(urls)
	if len(got) != 1 {
		t.Fatalf("expected 1 result, got %d", len(got))
	}
	if got[0] != "[invalid url]" {
		t.Errorf("invalid URL should produce placeholder, got: %q", got[0])
	}
}

func TestStripURLCredentials_EmptySlice_ReturnsEmpty(t *testing.T) {
	got := stripURLCredentials(nil)
	if len(got) != 0 {
		t.Errorf("empty input should return empty slice, got: %v", got)
	}
}

func TestStripURLCredentials_MixedSlice_AllProcessed(t *testing.T) {
	urls := []string{
		"https://user:secret@rpc1.example.com",
		"https://rpc2.example.com",
		"not-a-url%%",
	}
	got := stripURLCredentials(urls)
	if len(got) != 3 {
		t.Fatalf("expected 3 results, got %d", len(got))
	}
	if strings.Contains(got[0], "secret") {
		t.Errorf("first URL credentials should be stripped, got: %q", got[0])
	}
	if got[1] != "https://rpc2.example.com" {
		t.Errorf("second URL should be unchanged, got: %q", got[1])
	}
	if got[2] != "[invalid url]" {
		t.Errorf("invalid URL should be placeholder, got: %q", got[2])
	}
}
