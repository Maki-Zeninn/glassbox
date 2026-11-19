// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package signer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── resolveSecret ─────────────────────────────────────────────────────────────

func TestResolveSecret_FlagWinsOverEnv(t *testing.T) {
	sources := []SecretSource{
		{Kind: SourceKindFlag, Label: "--my-flag", Value: "flag-value"},
		{Kind: SourceKindEnv, Label: "MY_ENV", Value: "env-value"},
	}
	res, val := resolveSecret("test_key", sources)
	if !res.Resolved {
		t.Fatal("expected resolved=true")
	}
	if val != "flag-value" {
		t.Errorf("expected flag-value to win, got %q", val)
	}
	if res.Kind != SourceKindFlag {
		t.Errorf("expected Kind=flag, got %q", res.Kind)
	}
	if !res.Conflict {
		t.Error("expected Conflict=true when both flag and env are set")
	}
	if len(res.ConflictSources) == 0 {
		t.Error("ConflictSources should be non-empty")
	}
	// Value must not appear in the resolution.
	if strings.Contains(res.Source, "flag-value") || strings.Contains(res.Source, "env-value") {
		t.Error("SecretResolution.Source must not contain secret values")
	}
	for _, cs := range res.ConflictSources {
		if strings.Contains(cs, "flag-value") || strings.Contains(cs, "env-value") {
			t.Error("ConflictSources must not contain secret values")
		}
	}
}

func TestResolveSecret_EnvUsedWhenFlagEmpty(t *testing.T) {
	sources := []SecretSource{
		{Kind: SourceKindFlag, Label: "--my-flag", Value: ""},
		{Kind: SourceKindEnv, Label: "MY_ENV", Value: "env-value"},
	}
	res, val := resolveSecret("test_key", sources)
	if !res.Resolved {
		t.Fatal("expected resolved=true from env source")
	}
	if val != "env-value" {
		t.Errorf("expected env-value, got %q", val)
	}
	if res.Kind != SourceKindEnv {
		t.Errorf("expected Kind=env, got %q", res.Kind)
	}
	if res.Conflict {
		t.Error("expected Conflict=false when only env is set")
	}
}

func TestResolveSecret_NoneWhenAllEmpty(t *testing.T) {
	sources := []SecretSource{
		{Kind: SourceKindFlag, Label: "--my-flag", Value: ""},
		{Kind: SourceKindEnv, Label: "MY_ENV", Value: ""},
	}
	res, val := resolveSecret("test_key", sources)
	if res.Resolved {
		t.Error("expected resolved=false when all sources are empty")
	}
	if val != "" {
		t.Errorf("expected empty value, got %q", val)
	}
	if res.Kind != SourceKindNone {
		t.Errorf("expected Kind=none, got %q", res.Kind)
	}
}

func TestResolveSecret_FileSource_ReadsFileContent(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(keyFile, []byte("-----BEGIN PRIVATE KEY-----\nMIIEv....\n-----END PRIVATE KEY-----\n"), 0600); err != nil {
		t.Fatalf("write key file: %v", err)
	}
	sources := []SecretSource{
		{Kind: SourceKindFile, Label: "--my-key-file", Value: keyFile, IsFilePath: true},
	}
	res, val := resolveSecret("signing_key", sources)
	if !res.Resolved {
		t.Fatal("expected resolved=true for file source")
	}
	// The resolution source should only contain the filename, not the full path
	// (to avoid leaking directory structure).
	if strings.Contains(res.Source, dir) {
		t.Errorf("SecretResolution.Source must not contain the full directory path, got: %q", res.Source)
	}
	if strings.Contains(res.Source, "BEGIN PRIVATE KEY") {
		t.Error("SecretResolution.Source must not contain key material")
	}
	if strings.Contains(val, "Source") {
		t.Error("resolved value should be the file content, not a metadata string")
	}
	_ = val // val is the file contents — intentionally not checked in detail
}

func TestResolveSecret_MissingFile_SkipsSource(t *testing.T) {
	sources := []SecretSource{
		{Kind: SourceKindFile, Label: "--my-key-file", Value: "/nonexistent/path/key.pem", IsFilePath: true},
		{Kind: SourceKindEnv, Label: "MY_ENV", Value: "env-fallback"},
	}
	res, val := resolveSecret("signing_key", sources)
	if !res.Resolved {
		t.Fatal("expected resolved=true from env fallback after missing file")
	}
	if res.Kind != SourceKindEnv {
		t.Errorf("expected Kind=env after missing file, got %q", res.Kind)
	}
	if val != "env-fallback" {
		t.Errorf("expected env-fallback, got %q", val)
	}
}

// ── SecretResolution.String ───────────────────────────────────────────────────

func TestSecretResolution_String_NotResolved(t *testing.T) {
	r := SecretResolution{Name: "my_secret", Kind: SourceKindNone, Resolved: false}
	s := r.String()
	if !strings.Contains(s, "not found") {
		t.Errorf("String for unresolved secret should say 'not found', got: %q", s)
	}
	if strings.Contains(s, "secret-value") {
		t.Error("String must not contain secret values")
	}
}

func TestSecretResolution_String_Resolved_NoConflict(t *testing.T) {
	r := SecretResolution{
		Name:     "my_secret",
		Kind:     SourceKindFlag,
		Source:   "--my-flag",
		Resolved: true,
	}
	s := r.String()
	if !strings.Contains(s, "--my-flag") {
		t.Errorf("String should mention the source flag, got: %q", s)
	}
	if strings.Contains(s, "conflict") {
		t.Error("String should not mention conflict when Conflict=false")
	}
}

func TestSecretResolution_String_Resolved_WithConflict(t *testing.T) {
	r := SecretResolution{
		Name:            "my_secret",
		Kind:            SourceKindFlag,
		Source:          "--my-flag",
		Resolved:        true,
		Conflict:        true,
		ConflictSources: []string{"MY_ENV_VAR"},
	}
	s := r.String()
	if !strings.Contains(s, "conflict") {
		t.Errorf("String should mention 'conflict', got: %q", s)
	}
	if !strings.Contains(s, "MY_ENV_VAR") {
		t.Errorf("String should mention the conflicting source, got: %q", s)
	}
}

// ── SecretResolutionSet ───────────────────────────────────────────────────────

func TestSecretResolutionSet_AllResolved_True(t *testing.T) {
	set := &SecretResolutionSet{
		Provider: "software",
		Resolutions: []SecretResolution{
			{Name: "key1", Resolved: true},
			{Name: "key2", Resolved: true},
		},
	}
	if !set.AllResolved() {
		t.Error("AllResolved should return true when all secrets are resolved")
	}
}

func TestSecretResolutionSet_AllResolved_False(t *testing.T) {
	set := &SecretResolutionSet{
		Provider: "pkcs11",
		Resolutions: []SecretResolution{
			{Name: "key1", Resolved: true},
			{Name: "key2", Resolved: false},
		},
	}
	if set.AllResolved() {
		t.Error("AllResolved should return false when any secret is unresolved")
	}
}

func TestSecretResolutionSet_AnyConflict(t *testing.T) {
	set := &SecretResolutionSet{
		Provider: "pkcs11",
		Resolutions: []SecretResolution{
			{Name: "pin", Resolved: true, Conflict: true, ConflictSources: []string{"GLASSBOX_PKCS11_PIN"}},
		},
	}
	if !set.AnyConflict() {
		t.Error("AnyConflict should return true when at least one resolution has a conflict")
	}
}

func TestSecretResolutionSet_RedactedDiagnostics_NoSecretValues(t *testing.T) {
	set := &SecretResolutionSet{
		Provider: "software",
		Resolutions: []SecretResolution{
			{Name: "signing_key", Kind: SourceKindFlag, Source: "--software-private-key", Resolved: true},
		},
	}
	diag := set.RedactedDiagnostics()
	// Diagnostic output must mention the source name but never a hypothetical value.
	if !strings.Contains(diag, "software") {
		t.Errorf("diagnostics should mention the provider, got:\n%s", diag)
	}
	if !strings.Contains(diag, "--software-private-key") {
		t.Errorf("diagnostics should mention the flag name, got:\n%s", diag)
	}
}

// ── ValidateNoConflicts ───────────────────────────────────────────────────────

func TestValidateNoConflicts_NoConflict_ReturnsNil(t *testing.T) {
	set := &SecretResolutionSet{
		Provider: "software",
		Resolutions: []SecretResolution{
			{Name: "key", Resolved: true, Conflict: false},
		},
	}
	if err := ValidateNoConflicts(set); err != nil {
		t.Errorf("expected nil, got: %v", err)
	}
}

func TestValidateNoConflicts_WithConflict_ReturnsError(t *testing.T) {
	set := &SecretResolutionSet{
		Provider: "pkcs11",
		Resolutions: []SecretResolution{
			{Name: "pkcs11_pin", Resolved: true, Conflict: true,
				Source: "--pkcs11-pin", ConflictSources: []string{"GLASSBOX_PKCS11_PIN"}},
		},
	}
	err := ValidateNoConflicts(set)
	if err == nil {
		t.Fatal("expected error for conflicting sources")
	}
	// Error message must name the conflicting sources but not their values.
	if !strings.Contains(err.Error(), "pkcs11_pin") {
		t.Errorf("error should name the conflicting secret, got: %v", err)
	}
	if !strings.Contains(err.Error(), "GLASSBOX_PKCS11_PIN") {
		t.Errorf("error should name the env var source, got: %v", err)
	}
}

func TestValidateNoConflicts_Nil_ReturnsNil(t *testing.T) {
	if err := ValidateNoConflicts(nil); err != nil {
		t.Errorf("expected nil for nil set, got: %v", err)
	}
}

// ── ValidateRequiredSecrets ───────────────────────────────────────────────────

func TestValidateRequiredSecrets_AllPresent(t *testing.T) {
	set := &SecretResolutionSet{
		Provider: "pkcs11",
		Resolutions: []SecretResolution{
			{Name: "pkcs11_module", Resolved: true, Source: "--pkcs11-module"},
			{Name: "pkcs11_pin", Resolved: true, Source: "--pkcs11-pin"},
		},
	}
	err := ValidateRequiredSecrets(set, []string{"pkcs11_module", "pkcs11_pin"})
	if err != nil {
		t.Errorf("expected nil when all required secrets are resolved, got: %v", err)
	}
}

func TestValidateRequiredSecrets_MissingRequired(t *testing.T) {
	set := &SecretResolutionSet{
		Provider: "pkcs11",
		Resolutions: []SecretResolution{
			{Name: "pkcs11_module", Resolved: true, Source: "--pkcs11-module"},
			{Name: "pkcs11_pin", Resolved: false, Source: "--pkcs11-pin"},
		},
	}
	err := ValidateRequiredSecrets(set, []string{"pkcs11_module", "pkcs11_pin"})
	if err == nil {
		t.Fatal("expected error when required secret is missing")
	}
	if !strings.Contains(err.Error(), "pkcs11_pin") {
		t.Errorf("error should name the missing secret, got: %v", err)
	}
	// The error must not suggest that a value was found.
	if strings.Contains(err.Error(), "found") && !strings.Contains(err.Error(), "not found") {
		t.Errorf("error message is misleading, got: %v", err)
	}
}

// ── Integration: PKCS11 secrets never appear in resolution output ─────────────

func TestResolvePKCS11Secrets_PINNotInDiagnostics(t *testing.T) {
	// Simulate flag-based PIN: set cfg.PKCS11PIN to a sensitive value and verify
	// that it never appears in the SecretResolutionSet output.
	cfg := ProviderConfig{
		PKCS11ModulePath: "/usr/lib/softhsm/libsofthsm2.so",
		PKCS11PIN:        "super-secret-1234",
		PKCS11KeyLabel:   "my-signing-key",
	}
	set, _ := ResolvePKCS11Secrets(cfg)
	diag := set.RedactedDiagnostics()

	if strings.Contains(diag, "super-secret-1234") {
		t.Error("PIN value must never appear in diagnostic output")
	}
	// The diagnostic should mention the source (flag name) instead.
	if !strings.Contains(diag, "--pkcs11-pin") {
		t.Errorf("diagnostic should mention the --pkcs11-pin flag, got:\n%s", diag)
	}
}

// ── Integration: software key precedence via env var ─────────────────────────

func TestResolveSoftwareSigningKey_EnvVarFallback(t *testing.T) {
	const hexKey = "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899"
	t.Setenv("GLASSBOX_SOFTWARE_PRIVATE_KEY_HEX", hexKey)
	cfg := ProviderConfig{} // no flag values
	res, val := ResolveSoftwareSigningKey(cfg)
	if !res.Resolved {
		t.Fatal("expected resolved=true from env var")
	}
	if res.Kind != SourceKindEnv {
		t.Errorf("expected Kind=env, got %q", res.Kind)
	}
	// The resolved value should match the env var, but must not appear in the
	// resolution struct.
	if val != hexKey {
		t.Errorf("expected hex key value, got different value")
	}
	resJSON := res.String()
	if strings.Contains(resJSON, hexKey) {
		t.Error("resolved value must not appear in SecretResolution.String()")
	}
}
