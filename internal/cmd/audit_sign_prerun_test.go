// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotandev/glassbox/internal/signer"
)

// ---- validateAuditSignProvenanceFlags ----------------------------------------

func TestValidateAuditSignProvenanceFlags_AllEmpty(t *testing.T) {
	if err := validateAuditSignProvenanceFlags("", ""); err != nil {
		t.Fatalf("expected nil for all-empty inputs, got: %v", err)
	}
}

func TestValidateAuditSignProvenanceFlags_ValidHash(t *testing.T) {
	validHash := strings.Repeat("a", 64)
	if err := validateAuditSignProvenanceFlags(validHash, ""); err != nil {
		t.Fatalf("expected nil for valid 64-char hash, got: %v", err)
	}
}

func TestValidateAuditSignProvenanceFlags_ShortHash(t *testing.T) {
	err := validateAuditSignProvenanceFlags("abc123", "")
	if err == nil {
		t.Fatal("expected error for short --previous-signature-hash")
	}
	msg := err.Error()
	if !strings.Contains(msg, "--previous-signature-hash") {
		t.Errorf("error should mention --previous-signature-hash, got: %q", msg)
	}
	if !strings.Contains(msg, "64") {
		t.Errorf("error should mention the required length, got: %q", msg)
	}
	if !strings.Contains(msg, "Fix:") {
		t.Errorf("error should include a Fix hint, got: %q", msg)
	}
	if !strings.Contains(msg, "Example") {
		t.Errorf("error should include an Example hint, got: %q", msg)
	}
}

func TestValidateAuditSignProvenanceFlags_NonHexHash(t *testing.T) {
	// 64 characters but not valid hex.
	nonHex := strings.Repeat("z", 64)
	err := validateAuditSignProvenanceFlags(nonHex, "")
	if err == nil {
		t.Fatal("expected error for non-hex --previous-signature-hash")
	}
	if !strings.Contains(err.Error(), "--previous-signature-hash") {
		t.Errorf("error should mention --previous-signature-hash, got: %q", err.Error())
	}
}

func TestValidateAuditSignProvenanceFlags_CertChainFileNotFound(t *testing.T) {
	err := validateAuditSignProvenanceFlags("", "/nonexistent/chain.pem")
	if err == nil {
		t.Fatal("expected error for nonexistent --cert-chain file")
	}
	msg := err.Error()
	if !strings.Contains(msg, "--cert-chain") {
		t.Errorf("error should mention --cert-chain, got: %q", msg)
	}
	if !strings.Contains(msg, "not found") || !strings.Contains(msg, "not readable") {
		// Either "not found" or "not readable" should be in the message.
		if !strings.Contains(msg, "not found") && !strings.Contains(msg, "not readable") {
			t.Errorf("error should say 'not found or not readable', got: %q", msg)
		}
	}
	if !strings.Contains(msg, "Fix:") {
		t.Errorf("error should include a Fix hint, got: %q", msg)
	}
	if !strings.Contains(msg, "Example") {
		t.Errorf("error should include an Example hint, got: %q", msg)
	}
}

func TestValidateAuditSignProvenanceFlags_CertChainFileExists(t *testing.T) {
	dir := t.TempDir()
	chainPath := filepath.Join(dir, "chain.pem")
	if err := os.WriteFile(chainPath, []byte("dummy pem content"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateAuditSignProvenanceFlags("", chainPath); err != nil {
		t.Fatalf("expected nil for existing cert-chain file, got: %v", err)
	}
}

func TestValidateAuditSignProvenanceFlags_BothValid(t *testing.T) {
	dir := t.TempDir()
	chainPath := filepath.Join(dir, "chain.pem")
	if err := os.WriteFile(chainPath, []byte("pem content"), 0600); err != nil {
		t.Fatal(err)
	}
	validHash := strings.Repeat("b", 64)
	if err := validateAuditSignProvenanceFlags(validHash, chainPath); err != nil {
		t.Fatalf("expected nil when both inputs are valid, got: %v", err)
	}
}

// ---- auditSignPreRunE: provenance validation wired in ----------------------

func TestAuditSignPreRunE_RejectsBadPreviousHash(t *testing.T) {
	defer resetAuditSignFlags()
	auditSignPayload = `{"key":"val"}`
	auditSignSoftwareKey = generateTestPEM(t)
	auditSignPreviousSignatureHash = "tooshort" // invalid

	err := auditSignCmd.PreRunE(auditSignCmd, nil)
	if err == nil {
		t.Fatal("expected error for invalid --previous-signature-hash in PreRunE")
	}
	if !strings.Contains(err.Error(), "--previous-signature-hash") {
		t.Errorf("error should mention --previous-signature-hash, got: %q", err.Error())
	}
}

func TestAuditSignPreRunE_RejectsMissingCertChain(t *testing.T) {
	defer resetAuditSignFlags()
	auditSignPayload = `{"key":"val"}`
	auditSignSoftwareKey = generateTestPEM(t)
	auditSignCertChainFile = "/no/such/file.pem"

	err := auditSignCmd.PreRunE(auditSignCmd, nil)
	if err == nil {
		t.Fatal("expected error for nonexistent --cert-chain in PreRunE")
	}
	if !strings.Contains(err.Error(), "--cert-chain") {
		t.Errorf("error should mention --cert-chain, got: %q", err.Error())
	}
}

func TestAuditSignPreRunE_ValidProvenancePasses(t *testing.T) {
	defer resetAuditSignFlags()
	auditSignPayload = `{"key":"val"}`
	auditSignSoftwareKey = generateTestPEM(t)
	auditSignPreviousSignatureHash = strings.Repeat("a", 64)

	err := auditSignCmd.PreRunE(auditSignCmd, nil)
	if err != nil {
		t.Fatalf("expected no error for valid provenance flags, got: %v", err)
	}
}

// ── Enhanced PKCS#11 input validation ─────────────────────────────────────

func TestValidatePKCS11SignInputs_MutuallyExclusiveKeySelectors(t *testing.T) {
	cfg := signer.Pkcs11Config{
		ModulePath: "/usr/lib/softhsm/libsofthsm2.so",
		PIN:        "1234",
		KeyLabel:   "mykey",
		KeyIDHex:   "a1b2c3",
	}
	err := validatePKCS11SignInputs(cfg)
	if err == nil {
		t.Fatal("expected error when both key-label and key-id are provided")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error should mention mutual exclusivity, got: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "--pkcs11-key-label") {
		t.Errorf("error should mention --pkcs11-key-label, got: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "--pkcs11-key-id") {
		t.Errorf("error should mention --pkcs11-key-id, got: %q", err.Error())
	}
}

func TestValidatePKCS11SignInputs_MissingBothKeySelectors(t *testing.T) {
	cfg := signer.Pkcs11Config{
		ModulePath: "/usr/lib/softhsm/libsofthsm2.so",
		PIN:        "1234",
	}
	err := validatePKCS11SignInputs(cfg)
	if err == nil {
		t.Fatal("expected error when neither key-label nor key-id is provided")
	}
	if !strings.Contains(err.Error(), "--pkcs11-key-label") {
		t.Errorf("error should mention --pkcs11-key-label, got: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "--pkcs11-key-id") {
		t.Errorf("error should mention --pkcs11-key-id, got: %q", err.Error())
	}
}

func TestValidatePKCS11SignInputs_KeyLabelOnly_Accepts(t *testing.T) {
	cfg := signer.Pkcs11Config{
		ModulePath: "/usr/lib/softhsm/libsofthsm2.so",
		PIN:        "1234",
		KeyLabel:   "mykey",
	}
	if err := validatePKCS11SignInputs(cfg); err != nil {
		t.Fatalf("expected no error with key-label only, got: %v", err)
	}
}

func TestValidatePKCS11SignInputs_KeyIDOnly_Accepts(t *testing.T) {
	cfg := signer.Pkcs11Config{
		ModulePath: "/usr/lib/softhsm/libsofthsm2.so",
		PIN:        "1234",
		KeyIDHex:   "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6",
	}
	if err := validatePKCS11SignInputs(cfg); err != nil {
		t.Fatalf("expected no error with key-id only, got: %v", err)
	}
}

func TestValidatePKCS11SignInputs_InvalidKeyIDHex_Rejects(t *testing.T) {
	cfg := signer.Pkcs11Config{
		ModulePath: "/usr/lib/softhsm/libsofthsm2.so",
		PIN:        "1234",
		KeyIDHex:   "not-valid-hex!!!",
	}
	err := validatePKCS11SignInputs(cfg)
	if err == nil {
		t.Fatal("expected error for non-hex key-id")
	}
	if !strings.Contains(err.Error(), "--pkcs11-key-id") {
		t.Errorf("error should mention --pkcs11-key-id, got: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "hex-encoded") {
		t.Errorf("error should mention hex-encoded, got: %q", err.Error())
	}
}
