// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"strings"
	"testing"

	"github.com/dotandev/glassbox/internal/signer"
	"github.com/spf13/cobra"
)

// ---- helpers ----------------------------------------------------------------

func generateTestPEM(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: raw}))
}

// resetAuditSignFlags resets all package-level flag variables to their zero
// values so tests are isolated from each other.
func resetAuditSignFlags() {
	auditSignPayload = ""
	auditSignPayloadFile = ""
	auditSignSoftwareKey = ""
	auditSignHSMProvider = ""
	auditSignProvider = ""
	auditSignPKCS11Module = ""
	auditSignPKCS11PIN = ""
	auditSignPKCS11TokenLabel = ""
	auditSignPKCS11KeyLabel = ""
	auditSignPKCS11KeyIDHex = ""
	auditSignSignerIdentity = ""
	auditSignKeyID = ""
	auditSignCertChainFile = ""
	auditSignPreviousSignatureHash = ""
}

// ---- readAuditPayload -------------------------------------------------------

func TestReadAuditPayload_File(t *testing.T) {
	content := `{"foo":"bar"}`
	file, err := os.CreateTemp("", "payload-*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(file.Name())
	if _, err := file.WriteString(content); err != nil {
		t.Fatal(err)
	}
	file.Close()

	got, err := readAuditPayload("", file.Name())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != content {
		t.Fatalf("expected payload %q, got %q", content, string(got))
	}
}

func TestReadAuditPayload_Inline(t *testing.T) {
	payload := `{"key":"value"}`
	got, err := readAuditPayload(payload, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != payload {
		t.Fatalf("expected %q, got %q", payload, string(got))
	}
}

// ---- resolveAuditSigner (backward-compat wrapper) ---------------------------

func TestResolveAuditSigner_SoftwarePEM_Flag(t *testing.T) {
	resetAuditSignFlags()
	auditSignSoftwareKey = generateTestPEM(t)

	s, err := resolveAuditSigner()
	if err != nil {
		t.Fatalf("resolveAuditSigner failed: %v", err)
	}
	if s.Algorithm() != "ed25519" {
		t.Fatalf("unexpected algorithm: %s", s.Algorithm())
	}
}

func TestResolveAuditSigner_SoftwarePEM_Env(t *testing.T) {
	resetAuditSignFlags()
	t.Setenv("GLASSBOX_AUDIT_PRIVATE_KEY_PEM", generateTestPEM(t))

	s, err := resolveAuditSigner()
	if err != nil {
		t.Fatalf("resolveAuditSigner failed: %v", err)
	}

	msg := []byte("test")
	sig, err := s.Sign(msg)
	if err != nil {
		t.Fatalf("sign failed: %v", err)
	}
	pub, err := s.PublicKey()
	if err != nil {
		t.Fatalf("public key failed: %v", err)
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), msg, sig) {
		t.Fatal("signature verification failed")
	}
}

func TestResolveAuditSigner_NoKey_ReturnsError(t *testing.T) {
	resetAuditSignFlags()
	t.Setenv("GLASSBOX_AUDIT_PRIVATE_KEY_PEM", "")
	t.Setenv("GLASSBOX_SOFTWARE_PRIVATE_KEY_HEX", "")
	t.Setenv("GLASSBOX_SIGNING_PROVIDER", "")
	t.Setenv("GLASSBOX_SIGNER_TYPE", "")

	_, err := resolveAuditSigner()
	if err == nil {
		t.Fatal("expected error when no key is configured")
	}
}

func TestResolveAuditSigner_UnknownProvider_ReturnsError(t *testing.T) {
	resetAuditSignFlags()
	auditSignProvider = "aws-kms"

	_, err := resolveAuditSigner()
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestResolveAuditSigner_LegacyHSMProvider_PKCS11_MissingModule(t *testing.T) {
	resetAuditSignFlags()
	auditSignHSMProvider = "pkcs11"
	t.Setenv("GLASSBOX_PKCS11_MODULE", "")
	t.Setenv("GLASSBOX_PKCS11_PIN", "")

	_, err := resolveAuditSigner()
	if err == nil {
		t.Fatal("expected error when PKCS#11 module is not configured")
	}
}

func TestResolveAuditSigner_SigningProviderEnv(t *testing.T) {
	resetAuditSignFlags()
	t.Setenv("GLASSBOX_SIGNING_PROVIDER", "software")
	t.Setenv("GLASSBOX_AUDIT_PRIVATE_KEY_PEM", generateTestPEM(t))

	s, err := resolveAuditSigner()
	if err != nil {
		t.Fatalf("resolveAuditSigner failed: %v", err)
	}
	if s.Algorithm() != "ed25519" {
		t.Fatalf("unexpected algorithm: %s", s.Algorithm())
	}
}

// ---- resolveProviderAndConfig -----------------------------------------------

func TestResolveProviderAndConfig_DefaultIsSoftware(t *testing.T) {
	resetAuditSignFlags()
	t.Setenv("GLASSBOX_SIGNING_PROVIDER", "")
	t.Setenv("GLASSBOX_SIGNER_TYPE", "")

	name, _ := resolveProviderAndConfig()
	if name != "software" {
		t.Fatalf("expected default provider 'software', got %q", name)
	}
}

func TestResolveProviderAndConfig_FlagTakesPrecedence(t *testing.T) {
	resetAuditSignFlags()
	auditSignProvider = "pkcs11"
	t.Setenv("GLASSBOX_SIGNING_PROVIDER", "software")

	name, _ := resolveProviderAndConfig()
	if name != "pkcs11" {
		t.Fatalf("expected 'pkcs11' from flag, got %q", name)
	}
}

func TestResolveProviderAndConfig_LegacyHSMProvider(t *testing.T) {
	resetAuditSignFlags()
	auditSignHSMProvider = "pkcs11"

	name, _ := resolveProviderAndConfig()
	if name != "pkcs11" {
		t.Fatalf("expected 'pkcs11' from legacy flag, got %q", name)
	}
}

func TestResolveProviderAndConfig_LegacySignerTypeEnv(t *testing.T) {
	resetAuditSignFlags()
	t.Setenv("GLASSBOX_SIGNING_PROVIDER", "")
	t.Setenv("GLASSBOX_SIGNER_TYPE", "pkcs11")

	name, _ := resolveProviderAndConfig()
	if name != "pkcs11" {
		t.Fatalf("expected 'pkcs11' from GLASSBOX_SIGNER_TYPE, got %q", name)
	}
}

func TestResolveProviderAndConfig_PKCS11FlagsPopulateConfig(t *testing.T) {
	resetAuditSignFlags()
	auditSignProvider = "pkcs11"
	auditSignPKCS11Module = "/usr/lib/softhsm/libsofthsm2.so"
	auditSignPKCS11PIN = "secret"
	auditSignPKCS11TokenLabel = "MyToken"
	auditSignPKCS11KeyLabel = "signing-key"
	auditSignPKCS11KeyIDHex = "aabb"

	_, cfg := resolveProviderAndConfig()
	if cfg.PKCS11ModulePath != "/usr/lib/softhsm/libsofthsm2.so" {
		t.Fatalf("unexpected module path: %s", cfg.PKCS11ModulePath)
	}
	if cfg.PKCS11PIN != "secret" {
		t.Fatalf("unexpected PIN")
	}
	if cfg.PKCS11TokenLabel != "MyToken" {
		t.Fatalf("unexpected token label: %s", cfg.PKCS11TokenLabel)
	}
	if cfg.PKCS11KeyLabel != "signing-key" {
		t.Fatalf("unexpected key label: %s", cfg.PKCS11KeyLabel)
	}
	if cfg.PKCS11KeyIDHex != "aabb" {
		t.Fatalf("unexpected key ID hex: %s", cfg.PKCS11KeyIDHex)
	}
}

// ---- runAuditSign -----------------------------------------------------------

func TestRunAuditSign_OutputsJSON(t *testing.T) {
	resetAuditSignFlags()
	auditSignPayload = `{"input":{},"state":{},"events":[],"timestamp":"2026-01-01T00:00:00.000Z"}`
	auditSignSoftwareKey = generateTestPEM(t)

	buf := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(buf)

	if err := runAuditSign(cmd, nil); err != nil {
		t.Fatalf("runAuditSign failed: %v", err)
	}

	var log SignedAuditLog
	if err := json.Unmarshal(buf.Bytes(), &log); err != nil {
		t.Fatalf("failed to parse output JSON: %v", err)
	}
	if log.TraceHash == "" || log.Signature == "" || log.PublicKey == "" {
		t.Fatalf("missing signed fields: %+v", log)
	}
	if !strings.Contains(string(log.Payload), `"input"`) {
		t.Fatalf("payload not preserved in output")
	}
	if log.Provider != "software" {
		t.Fatalf("expected provider 'software', got %q", log.Provider)
	}
	// Verify key-origin metadata is populated.
	if log.KeyOrigin == nil {
		t.Fatal("expected key_origin in output")
	}
	if log.KeyOrigin.Provider != "software" {
		t.Errorf("key_origin.provider = %q, want %q", log.KeyOrigin.Provider, "software")
	}
	if log.KeyOrigin.Algorithm != "ed25519" {
		t.Errorf("key_origin.algorithm = %q, want %q", log.KeyOrigin.Algorithm, "ed25519")
	}
	if log.KeyOrigin.KeyFingerprint == "" {
		t.Error("key_origin.key_fingerprint must not be empty")
	}
}

func TestRunAuditSign_BothPayloadFlags_ReturnsError(t *testing.T) {
	resetAuditSignFlags()
	auditSignPayload = `{"foo":"bar"}`
	auditSignPayloadFile = "/some/file.json"
	auditSignSoftwareKey = generateTestPEM(t)

	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})

	if err := runAuditSign(cmd, nil); err == nil {
		t.Fatal("expected error when both --payload and --payload-file are set")
	}
}

func TestRunAuditSign_EmptyPayload_ReturnsError(t *testing.T) {
	resetAuditSignFlags()
	auditSignPayload = "   "
	auditSignSoftwareKey = generateTestPEM(t)

	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})

	if err := runAuditSign(cmd, nil); err == nil {
		t.Fatal("expected error for empty payload")
	}
}

func TestRunAuditSign_InvalidJSON_ReturnsError(t *testing.T) {
	resetAuditSignFlags()
	auditSignPayload = "not-json"
	auditSignSoftwareKey = generateTestPEM(t)

	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})

	if err := runAuditSign(cmd, nil); err == nil {
		t.Fatal("expected error for invalid JSON payload")
	}
}

// ---- custom provider registration -------------------------------------------

// mockProvider is a test-only SignerProvider that always returns a fixed signer.
type mockProvider struct {
	name string
	s    signer.Signer
}

func (m *mockProvider) Name() string        { return m.name }
func (m *mockProvider) Description() string { return "mock provider for testing" }
func (m *mockProvider) EnvVars() []signer.EnvVarDoc { return nil }
func (m *mockProvider) Validate(_ signer.ProviderConfig) error { return nil }
func (m *mockProvider) Create(_ signer.ProviderConfig) (signer.Signer, error) {
	return m.s, nil
}

func TestRunAuditSign_CustomProvider(t *testing.T) {
	resetAuditSignFlags()

	_, priv, _ := ed25519.GenerateKey(nil)
	s := signer.NewInMemorySignerFromKey(priv)

	// Register a custom provider in the default registry for this test.
	signer.DefaultRegistry.RegisterOrReplace(&mockProvider{name: "test-mock", s: s})
	t.Cleanup(func() {
		// Re-register the software provider to restore the default state.
		// (RegisterOrReplace is idempotent so this is safe.)
		signer.DefaultRegistry.RegisterOrReplace(&signer.SoftwareProvider{})
	})

	auditSignPayload = `{"input":{},"state":{},"events":[],"timestamp":"2026-01-01T00:00:00.000Z"}`
	auditSignProvider = "test-mock"

	buf := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(buf)

	if err := runAuditSign(cmd, nil); err != nil {
		t.Fatalf("runAuditSign with custom provider failed: %v", err)
	}

	var log SignedAuditLog
	if err := json.Unmarshal(buf.Bytes(), &log); err != nil {
		t.Fatalf("failed to parse output JSON: %v", err)
	}
	if log.Provider != "test-mock" {
		t.Fatalf("expected provider 'test-mock', got %q", log.Provider)
	}
}
