// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"strings"
	"testing"
)

// ── DetectSchemaVersion ───────────────────────────────────────────────────────

func TestDetectSchemaVersion_Missing_ReturnsZero(t *testing.T) {
	content := `rpc_url = "https://example.com"
network = "testnet"
`
	v, err := DetectSchemaVersion(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != 0 {
		t.Errorf("expected 0 for file without schema_version, got %d", v)
	}
}

func TestDetectSchemaVersion_Present_ReturnsValue(t *testing.T) {
	content := `schema_version = 1
rpc_url = "https://example.com"
`
	v, err := DetectSchemaVersion(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != 1 {
		t.Errorf("expected 1, got %d", v)
	}
}

func TestDetectSchemaVersion_FutureVersion(t *testing.T) {
	content := `schema_version = 99
rpc_url = "https://example.com"
`
	v, err := DetectSchemaVersion(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != 99 {
		t.Errorf("expected 99, got %d", v)
	}
}

func TestDetectSchemaVersion_InvalidValue_ReturnsError(t *testing.T) {
	content := `schema_version = not_a_number
rpc_url = "https://example.com"
`
	_, err := DetectSchemaVersion(content)
	if err == nil {
		t.Fatal("expected error for non-integer schema_version, got nil")
	}
}

func TestDetectSchemaVersion_CommentedOut_ReturnsZero(t *testing.T) {
	content := `# schema_version = 1
rpc_url = "https://example.com"
`
	v, err := DetectSchemaVersion(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != 0 {
		t.Errorf("commented-out key should not be detected, got %d", v)
	}
}

// ── TestMigration_Idempotent ─────────────────────────────────────────────────
// Issue 102: each migration function must be idempotent — applying it twice
// must produce the same result as applying it once.  This guards against
// accidental data corruption when a schema version bump is missed and the
// migration runs a second time on already-migrated content.

func TestMigration_Idempotent(t *testing.T) {
	// migrateV0ToV1 is the only migration step at present.  For each new
	// migration added to migrate.go, add a corresponding sub-test here.
	t.Run("migrateV0ToV1", func(t *testing.T) {
		inputs := []struct {
			name    string
			content string
		}{
			{
				name:    "plain config without schema_version",
				content: "rpc_url = \"https://rpc.example.com\"\nnetwork = \"testnet\"\n",
			},
			{
				name:    "config with comments and no schema_version",
				content: "# Glassbox config\nrpc_url = \"https://rpc.example.com\"\nnetwork = \"testnet\"\n",
			},
			{
				name:    "empty file",
				content: "",
			},
			{
				name:    "comments-only file",
				content: "# Just a comment\n",
			},
		}

		for _, tc := range inputs {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				// First application: should migrate.
				out1, changed1, diag1 := migrateV0ToV1(tc.content)
				_ = diag1

				// A non-empty input without schema_version should be changed on the first call.
				// (Empty content is a special case that may produce an empty result.)
				_ = changed1

				// Second application on the already-migrated content must be a no-op.
				out2, changed2, diag2 := migrateV0ToV1(out1)
				if changed2 {
					t.Errorf("second application of migrateV0ToV1 reported changed=true; migration is not idempotent")
				}
				if out2 != out1 {
					t.Errorf("second application changed content:\nbefore:\n%s\nafter:\n%s", out1, out2)
				}
				_ = diag2
			})
		}
	})

	// Idempotency via the public MigrateConfig API (end-to-end).
	t.Run("MigrateConfig_public_API", func(t *testing.T) {
		inputs := []string{
			"rpc_url = \"https://rpc.example.com\"\n",
			"# comment\nrpc_url = \"https://rpc.example.com\"\nnetwork = \"testnet\"\n",
			"",
		}
		for _, content := range inputs {
			out1, r1, err := MigrateConfig(content)
			if err != nil {
				t.Fatalf("first MigrateConfig call failed: %v", err)
			}
			_ = r1

			out2, r2, err := MigrateConfig(out1)
			if err != nil {
				t.Fatalf("second MigrateConfig call failed: %v", err)
			}
			if r2.Changed {
				t.Errorf("second MigrateConfig call reports Changed=true; not idempotent for input %q", content)
			}
			if out2 != out1 {
				t.Errorf("second MigrateConfig call modified content:\nbefore:\n%s\nafter:\n%s", out1, out2)
			}
		}
	})
}

// ── MigrateConfig — already current ─────────────────────────────────────────

func TestMigrateConfig_AlreadyCurrent_NoChange(t *testing.T) {
	content := `schema_version = 1
rpc_url = "https://example.com"
`
	out, result, err := MigrateConfig(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Changed {
		t.Error("Changed should be false for already-current file")
	}
	if result.FromVersion != 1 {
		t.Errorf("FromVersion = %d, want 1", result.FromVersion)
	}
	if result.ToVersion != 1 {
		t.Errorf("ToVersion = %d, want 1", result.ToVersion)
	}
	if out != content {
		t.Errorf("content must be unchanged, got:\n%s", out)
	}
}

// ── MigrateConfig — pre-versioning (v0 → v1) ─────────────────────────────────

func TestMigrateConfig_PreVersioning_AddsSchemaVersion(t *testing.T) {
	content := `rpc_url = "https://example.com"
network = "testnet"
`
	out, result, err := MigrateConfig(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Changed {
		t.Error("Changed should be true for pre-versioning file")
	}
	if result.FromVersion != 0 {
		t.Errorf("FromVersion = %d, want 0", result.FromVersion)
	}
	if result.ToVersion != 1 {
		t.Errorf("ToVersion = %d, want 1", result.ToVersion)
	}
	if !strings.Contains(out, "schema_version = 1") {
		t.Errorf("migrated output missing schema_version = 1:\n%s", out)
	}
	// Original keys must still be present.
	if !strings.Contains(out, `rpc_url = "https://example.com"`) {
		t.Errorf("migrated output lost rpc_url:\n%s", out)
	}
}

func TestMigrateConfig_PreVersioning_CommentsPreserved(t *testing.T) {
	content := `# Glassbox configuration
# Network settings
rpc_url = "https://example.com"
network = "testnet"
`
	out, result, err := MigrateConfig(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Changed {
		t.Error("expected Changed=true")
	}
	// Both comment lines must survive.
	if !strings.Contains(out, "# Glassbox configuration") {
		t.Errorf("comment lost after migration:\n%s", out)
	}
	if !strings.Contains(out, "# Network settings") {
		t.Errorf("comment lost after migration:\n%s", out)
	}
	// schema_version must appear after the comment block.
	schemaIdx := strings.Index(out, "schema_version = 1")
	commentIdx := strings.Index(out, "# Network settings")
	if schemaIdx < commentIdx {
		t.Errorf("schema_version should appear after the comment block")
	}
}

func TestMigrateConfig_PreVersioning_SchemaVersionBeforeFirstKey(t *testing.T) {
	// No leading comments — schema_version should be at the very top.
	content := `rpc_url = "https://example.com"
`
	out, _, err := MigrateConfig(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "schema_version = 1" {
		t.Errorf("first non-empty line should be schema_version = 1, got %q", lines[0])
	}
}

// ── MigrateConfig — idempotency ──────────────────────────────────────────────

func TestMigrateConfig_Idempotent_CalledTwice(t *testing.T) {
	content := `rpc_url = "https://example.com"
network = "testnet"
`
	// First call migrates.
	out1, r1, err := MigrateConfig(content)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if !r1.Changed {
		t.Error("first call: expected Changed=true")
	}

	// Second call on already-migrated content must be a no-op.
	out2, r2, err := MigrateConfig(out1)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if r2.Changed {
		t.Error("second call: Changed must be false (idempotent)")
	}
	if out2 != out1 {
		t.Errorf("second call changed content:\nbefore:\n%s\nafter:\n%s", out1, out2)
	}
}

func TestMigrateConfig_Idempotent_AlreadyHasSchemaVersion(t *testing.T) {
	// A file that already has schema_version = 0 explicitly must not get a
	// duplicate line (even though 0 is the "pre-versioning" sentinel).
	content := `schema_version = 0
rpc_url = "https://example.com"
`
	out, result, err := MigrateConfig(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// schema_version IS present, so migrateV0ToV1 must skip the insert step.
	count := strings.Count(out, "schema_version")
	if count != 1 {
		t.Errorf("expected exactly 1 schema_version line, got %d:\n%s", count, out)
	}
	_ = result
}

// ── MigrateConfig — future version rejection ─────────────────────────────────

func TestMigrateConfig_FutureVersion_ReturnsError(t *testing.T) {
	content := `schema_version = 99
rpc_url = "https://example.com"
`
	_, result, err := MigrateConfig(content)
	if err == nil {
		t.Fatal("expected error for future schema version, got nil")
	}
	if !strings.Contains(err.Error(), "99") {
		t.Errorf("error should mention the detected version 99, got: %v", err)
	}
	if result.Changed {
		t.Error("Changed must be false when migration is rejected")
	}
}

func TestMigrateConfig_FutureVersion_ContentUnchanged(t *testing.T) {
	content := `schema_version = 999
rpc_url = "https://example.com"
`
	out, _, err := MigrateConfig(content)
	if err == nil {
		t.Fatal("expected error")
	}
	// Even on error the original content must be returned unmodified.
	if out != content {
		t.Errorf("content must be unchanged on error, got:\n%s", out)
	}
}

// ── MigrateConfig — empty and whitespace-only files ──────────────────────────

func TestMigrateConfig_EmptyFile_InsertsSchemaVersion(t *testing.T) {
	out, result, err := MigrateConfig("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Changed {
		t.Error("expected Changed=true for empty file")
	}
	if !strings.Contains(out, "schema_version = 1") {
		t.Errorf("expected schema_version = 1 in output, got:\n%s", out)
	}
}

func TestMigrateConfig_CommentsOnly_InsertsAfterComments(t *testing.T) {
	content := "# Just a comment\n"
	out, result, err := MigrateConfig(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Changed {
		t.Error("expected Changed=true")
	}
	if !strings.Contains(out, "# Just a comment") {
		t.Errorf("comment was lost:\n%s", out)
	}
	if !strings.Contains(out, "schema_version = 1") {
		t.Errorf("schema_version not added:\n%s", out)
	}
}

// ── loadTOML — version guard integration ─────────────────────────────────────

func TestLoadTOML_FutureVersion_ReturnsError(t *testing.T) {
	// Write a temp file with future schema version and verify loadTOML rejects it.
	dir := t.TempDir()
	path := dir + "/future.toml"
	content := "schema_version = 999\nrpc_url = \"https://example.com\"\n"
	if err := writeTestFile(path, content); err != nil {
		t.Fatalf("writeTestFile: %v", err)
	}

	cfg := &Config{}
	err := cfg.loadTOML(path)
	if err == nil {
		t.Fatal("expected error for future schema version in loadTOML, got nil")
	}
	if !strings.Contains(err.Error(), "999") {
		t.Errorf("error should mention version 999, got: %v", err)
	}
}

func TestLoadTOML_V1_LoadsSuccessfully(t *testing.T) {
	path := t.TempDir() + "/v1.toml"
	content := "schema_version = 1\nrpc_url = \"https://example.com\"\nnetwork = \"testnet\"\n"
	if err := writeTestFile(path, content); err != nil {
		t.Fatalf("writeTestFile: %v", err)
	}

	cfg := &Config{}
	if err := cfg.loadTOML(path); err != nil {
		t.Fatalf("unexpected error for v1 config: %v", err)
	}
	if cfg.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1", cfg.SchemaVersion)
	}
	if cfg.RpcUrl != "https://example.com" {
		t.Errorf("RpcUrl = %q, want https://example.com", cfg.RpcUrl)
	}
}

func TestLoadTOML_NoVersion_LoadsAsV0(t *testing.T) {
	path := t.TempDir() + "/noversion.toml"
	content := "rpc_url = \"https://example.com\"\nnetwork = \"testnet\"\n"
	if err := writeTestFile(path, content); err != nil {
		t.Fatalf("writeTestFile: %v", err)
	}

	cfg := &Config{}
	if err := cfg.loadTOML(path); err != nil {
		t.Fatalf("unexpected error for pre-versioning config: %v", err)
	}
	if cfg.SchemaVersion != 0 {
		t.Errorf("SchemaVersion = %d, want 0 (absent key)", cfg.SchemaVersion)
	}
}

// ── MigrationResult diagnostics ──────────────────────────────────────────────

func TestMigrationResult_DiagnosticsPopulated(t *testing.T) {
	content := "rpc_url = \"https://example.com\"\n"
	_, result, err := MigrateConfig(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Diagnostics) == 0 {
		t.Error("expected at least one diagnostic message")
	}
}

func TestMigrationResult_NoDiagnosticsOnAlreadyCurrent(t *testing.T) {
	content := "schema_version = 1\nrpc_url = \"https://example.com\"\n"
	_, result, err := MigrateConfig(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// "already at version" is still a diagnostic, just informational.
	for _, d := range result.Diagnostics {
		if strings.Contains(d, "already at") {
			return // found — pass
		}
	}
	t.Error("expected 'already at schema_version' diagnostic message")
}

// ── helpers ───────────────────────────────────────────────────────────────────

func writeTestFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0600)
}
