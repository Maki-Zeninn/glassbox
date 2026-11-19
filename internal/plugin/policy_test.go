// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func makeManifest(name string, caps []Capability, perms []Permission, trust TrustLevel) *Manifest {
	return &Manifest{
		SchemaVersion: ManifestVersion,
		Name:          name,
		Version:       "1.0.0",
		APIVersion:    Version,
		Entrypoint:    "plugin",
		Capabilities:  caps,
		Permissions:   perms,
		TrustLevel:    trust,
	}
}

func TestDefaultPolicy_AllowsVerifiedOnly(t *testing.T) {
	pol := DefaultPolicy()
	// Default policy should allow verified plugins
	mVerified := makeManifest("verified-plugin", []Capability{CapabilityDecoder}, []Permission{PermissionNetwork}, TrustLevelVerified)
	if err := pol.CheckManifest(mVerified); err != nil {
		t.Errorf("default policy should allow verified plugins, got: %v", err)
	}
	// Default policy should block untrusted plugins
	mUntrusted := makeManifest("untrusted-plugin", []Capability{CapabilityDecoder}, []Permission{PermissionNetwork}, TrustLevelUntrusted)
	if err := pol.CheckManifest(mUntrusted); err == nil {
		t.Error("default policy should block untrusted plugins")
	}
	// Default policy should block community plugins
	mCommunity := makeManifest("community-plugin", []Capability{CapabilityDecoder}, []Permission{PermissionNetwork}, TrustLevelCommunity)
	if err := pol.CheckManifest(mCommunity); err == nil {
		t.Error("default policy should block community plugins")
	}
}

func TestPolicy_DeniedCapability(t *testing.T) {
	pol := &Policy{
		DeniedCapabilities: []string{"write_fs"},
		AllowUntrusted:     true,
	}
	m := makeManifest("bad-plugin", []Capability{CapabilityArtifactLoader, "write_fs"}, nil, TrustLevelCommunity)
	if err := pol.CheckManifest(m); err == nil {
		t.Error("expected policy to deny plugin with write_fs capability")
	}
}

func TestPolicy_DeniedPermission(t *testing.T) {
	pol := &Policy{
		DeniedPermissions: []string{"network"},
		AllowUntrusted:    true,
	}
	m := makeManifest("net-plugin", []Capability{CapabilityDecoder}, []Permission{PermissionNetwork}, TrustLevelVerified)
	if err := pol.CheckManifest(m); err == nil {
		t.Error("expected policy to deny plugin requesting network permission")
	}
}

func TestPolicy_DeniedPluginName(t *testing.T) {
	pol := &Policy{
		DeniedPlugins:  []string{"evil-plugin"},
		AllowUntrusted: true,
	}
	m := makeManifest("evil-plugin", []Capability{CapabilityDecoder}, nil, TrustLevelVerified)
	if err := pol.CheckManifest(m); err == nil {
		t.Error("expected policy to deny explicitly blocked plugin name")
	}
}

func TestPolicy_AllowedPlugin(t *testing.T) {
	pol := &Policy{
		DeniedCapabilities: []string{"write_fs"},
		DeniedPermissions:  []string{"network"},
		DeniedPlugins:      []string{"evil-plugin"},
		AllowUntrusted:     true,
	}
	m := makeManifest("good-plugin", []Capability{CapabilityDecoder}, []Permission{PermissionReadFS}, TrustLevelVerified)
	if err := pol.CheckManifest(m); err != nil {
		t.Errorf("expected policy to allow good plugin, got: %v", err)
	}
}

func TestPolicy_DenyUntrusted(t *testing.T) {
	pol := &Policy{AllowUntrusted: false}
	m := makeManifest("untrusted-plugin", []Capability{CapabilityDecoder}, nil, TrustLevelUntrusted)
	if err := pol.CheckManifest(m); err == nil {
		t.Error("expected policy to deny untrusted plugin when AllowUntrusted=false")
	}
}

func TestPolicy_DenyEmptyTrustLevel(t *testing.T) {
	pol := &Policy{AllowUntrusted: false}
	m := makeManifest("no-trust-plugin", []Capability{CapabilityDecoder}, nil, "")
	if err := pol.CheckManifest(m); err == nil {
		t.Error("expected policy to deny plugin with empty trust level when AllowUntrusted=false")
	}
}

func TestPolicy_AllowVerifiedWhenUntrustedDenied(t *testing.T) {
	pol := &Policy{AllowUntrusted: false}
	m := makeManifest("verified-plugin", []Capability{CapabilityDecoder}, nil, TrustLevelVerified)
	if err := pol.CheckManifest(m); err != nil {
		t.Errorf("verified plugin should be allowed even when AllowUntrusted=false, got: %v", err)
	}
}

func TestPolicy_NilPolicyAllowsAll(t *testing.T) {
	var pol *Policy
	m := makeManifest("any-plugin", []Capability{CapabilityDecoder}, []Permission{PermissionNetwork}, TrustLevelUntrusted)
	if err := pol.CheckManifest(m); err != nil {
		t.Errorf("nil policy should allow everything, got: %v", err)
	}
}

func TestLoadPolicy_ValidFile(t *testing.T) {
	pol := &Policy{
		DeniedCapabilities: []string{"write_fs"},
		DeniedPermissions:  []string{"network"},
		DeniedPlugins:      []string{"bad-plugin"},
		AllowUntrusted:     false,
	}
	data, _ := json.Marshal(pol)

	dir := t.TempDir()
	path := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadPolicy(path)
	if err != nil {
		t.Fatalf("LoadPolicy failed: %v", err)
	}
	if len(loaded.DeniedCapabilities) != 1 || loaded.DeniedCapabilities[0] != "write_fs" {
		t.Errorf("DeniedCapabilities mismatch: %v", loaded.DeniedCapabilities)
	}
	if loaded.AllowUntrusted {
		t.Error("AllowUntrusted should be false")
	}
}

func TestLoadPolicy_MissingFile(t *testing.T) {
	_, err := LoadPolicy("/nonexistent/policy.json")
	if err == nil {
		t.Error("expected error for missing policy file")
	}
}

func TestLoadPolicy_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(path, []byte("not-json"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadPolicy(path)
	if err == nil {
		t.Error("expected error for invalid JSON policy file")
	}
}

func TestRegistry_PolicyEnforced(t *testing.T) {
	reg := NewRegistry()
	pol := &Policy{
		DeniedCapabilities: []string{"decoder"},
		AllowUntrusted:     true,
	}
	reg.SetPolicy(pol)

	if reg.Policy() != pol {
		t.Error("Policy() should return the set policy")
	}
}

func TestPolicy_ErrorHasHint(t *testing.T) {
	pol := DefaultPolicy()
	m := makeManifest("untrusted-plugin", []Capability{CapabilityDecoder}, nil, TrustLevelUntrusted)
	err := pol.CheckManifest(m)
	if err == nil {
		t.Fatal("expected error for untrusted plugin with default policy")
	}
	
	hint := errors.Hint(err)
	if hint == "" {
		t.Error("expected error to have a Hint for remediation")
	}
	if hint != "To allow community and untrusted plugins, pass --allow-untrusted-plugins on the CLI or set plugin.allow_untrusted = true in the config TOML." {
		t.Errorf("unexpected hint: %q", hint)
	}
	
	// Verify it's an ErstError with the right code
	var e *errors.ErstError
	if !errors.As(err, &e) {
		t.Error("expected error to be an ErstError")
	}
	if e.Code != errors.ErstValidationFailed {
		t.Errorf("expected error code %s, got %s", errors.ErstValidationFailed, e.Code)
	}
}
