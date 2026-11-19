// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// generateTestCert creates a self-signed test X.509 certificate and returns
// its PEM block. The signing key is an Ed25519 key pair generated in-process.
func generateTestCert(t *testing.T, subject pkix.Name, notBefore, notAfter time.Time, serial int64) (string, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      subject,
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		IsCA:         true,
		KeyUsage:     x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, priv)
	require.NoError(t, err)

	var buf bytes.Buffer
	require.NoError(t, pem.Encode(&buf, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}))
	return buf.String(), priv
}

// buildSignedLogWithProvenance creates a signed log with a certificate chain
// embedded in its provenance field. Returns the file path and the certificate PEM.
func buildSignedLogWithProvenance(t *testing.T, certPEM string) string {
	t.Helper()
	payload := map[string]interface{}{"action": "test"}
	path, _, _ := buildSignedLog(t, payload)

	// Re-read, add provenance, rewrite.
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var log SignedAuditLog
	require.NoError(t, json.Unmarshal(data, &log))

	log.Provenance = &SignatureProvenance{
		SignerIdentity:   "CN=Test Signer",
		KeyID:            "test-key-01",
		CertificateChain: []string{certPEM},
	}

	out, err := json.MarshalIndent(log, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, out, 0600))
	return path
}

// ── evaluateTrustPolicy unit tests ────────────────────────────────────────────

func TestEvaluateTrustPolicy_NoCerts_AllPoliciesDisabled(t *testing.T) {
	// Default: no policy fields set → all dimensions pass.
	log := SignedAuditLog{Timestamp: time.Now().UTC()}
	result := evaluateTrustPolicy(&log, &TrustPolicyConfig{})
	assert.True(t, result.Valid)
	assert.True(t, result.TrustRootValid)
	assert.True(t, result.IssuerAllowed)
	assert.True(t, result.ValidityValid)
	assert.True(t, result.RevocationValid)
	assert.Empty(t, result.Issues)
}

func TestEvaluateTrustPolicy_AllowedIssuers_Pass(t *testing.T) {
	subject := pkix.Name{CommonName: "My Trusted CA", Organization: []string{"Acme"}}
	certPEM, _ := generateTestCert(t, subject, time.Now().Add(-time.Hour), time.Now().Add(time.Hour*24), 1)

	path := buildSignedLogWithProvenance(t, certPEM)
	data, _ := os.ReadFile(path)
	var log SignedAuditLog
	require.NoError(t, json.Unmarshal(data, &log))

	policy := &TrustPolicyConfig{
		AllowedIssuers: []string{"My Trusted CA"},
	}
	result := evaluateTrustPolicy(&log, policy)
	assert.True(t, result.IssuerAllowed, "expected issuer to be allowed")
	assert.Empty(t, result.UntrustedIssuers)
}

func TestEvaluateTrustPolicy_AllowedIssuers_Fail_UntrustedIssuerIdentified(t *testing.T) {
	subject := pkix.Name{CommonName: "Unknown CA Corp", Organization: []string{"Unknown"}}
	certPEM, _ := generateTestCert(t, subject, time.Now().Add(-time.Hour), time.Now().Add(time.Hour*24), 2)

	path := buildSignedLogWithProvenance(t, certPEM)
	data, _ := os.ReadFile(path)
	var log SignedAuditLog
	require.NoError(t, json.Unmarshal(data, &log))

	policy := &TrustPolicyConfig{
		AllowedIssuers: []string{"Trusted Root CA"},
	}
	result := evaluateTrustPolicy(&log, policy)
	assert.False(t, result.IssuerAllowed, "expected issuer to be rejected")
	assert.NotEmpty(t, result.UntrustedIssuers, "untrusted issuer should be identified")
	require.Len(t, result.UntrustedIssuers, 1)
	assert.Contains(t, result.UntrustedIssuers[0], "Unknown CA Corp")
	assert.False(t, result.Valid)
}

func TestEvaluateTrustPolicy_ValidityWindow_NotExpired(t *testing.T) {
	subject := pkix.Name{CommonName: "Validity CA"}
	notBefore := time.Now().Add(-time.Hour)
	notAfter := time.Now().Add(time.Hour * 24)
	certPEM, _ := generateTestCert(t, subject, notBefore, notAfter, 3)

	path := buildSignedLogWithProvenance(t, certPEM)
	data, _ := os.ReadFile(path)
	var log SignedAuditLog
	require.NoError(t, json.Unmarshal(data, &log))

	policy := &TrustPolicyConfig{CheckValidity: true}
	result := evaluateTrustPolicy(&log, policy)
	assert.True(t, result.ValidityValid, "certificate should be within valid window")
}

func TestEvaluateTrustPolicy_ValidityWindow_Expired(t *testing.T) {
	subject := pkix.Name{CommonName: "Expired Cert CA"}
	notBefore := time.Now().Add(-48 * time.Hour)
	notAfter := time.Now().Add(-24 * time.Hour) // already expired
	certPEM, _ := generateTestCert(t, subject, notBefore, notAfter, 4)

	path := buildSignedLogWithProvenance(t, certPEM)
	data, _ := os.ReadFile(path)
	var log SignedAuditLog
	require.NoError(t, json.Unmarshal(data, &log))

	// Log timestamp is after cert expiry.
	log.Timestamp = time.Now().UTC()

	policy := &TrustPolicyConfig{CheckValidity: true}
	result := evaluateTrustPolicy(&log, policy)
	assert.False(t, result.ValidityValid, "expired certificate should fail validity check")
	assert.False(t, result.Valid)
	require.NotEmpty(t, result.Issues)
	assert.Contains(t, result.Issues[0], "expired")
}

func TestEvaluateTrustPolicy_ValidityWindow_NotYetValid(t *testing.T) {
	subject := pkix.Name{CommonName: "Future Cert CA"}
	notBefore := time.Now().Add(24 * time.Hour) // not yet valid
	notAfter := time.Now().Add(48 * time.Hour)
	certPEM, _ := generateTestCert(t, subject, notBefore, notAfter, 5)

	path := buildSignedLogWithProvenance(t, certPEM)
	data, _ := os.ReadFile(path)
	var log SignedAuditLog
	require.NoError(t, json.Unmarshal(data, &log))

	log.Timestamp = time.Now().UTC()

	policy := &TrustPolicyConfig{CheckValidity: true}
	result := evaluateTrustPolicy(&log, policy)
	assert.False(t, result.ValidityValid, "future certificate should fail validity check")
	require.NotEmpty(t, result.Issues)
	assert.Contains(t, result.Issues[0], "not yet valid")
}

func TestEvaluateTrustPolicy_Revocation_StrictMode(t *testing.T) {
	// Serial number 7 (hex: "7").
	subject := pkix.Name{CommonName: "Revoked CA"}
	certPEM, _ := generateTestCert(t, subject, time.Now().Add(-time.Hour), time.Now().Add(time.Hour*24), 7)

	path := buildSignedLogWithProvenance(t, certPEM)
	data, _ := os.ReadFile(path)
	var log SignedAuditLog
	require.NoError(t, json.Unmarshal(data, &log))

	policy := &TrustPolicyConfig{
		RevokedCertificates: []string{"7"}, // serial 7 in hex
		RevocationPolicy:    "strict",
	}
	result := evaluateTrustPolicy(&log, policy)
	assert.False(t, result.RevocationValid, "revoked certificate should fail revocation check")
	assert.False(t, result.Valid)
	require.NotEmpty(t, result.Issues)
	assert.Contains(t, result.Issues[0], "revoked")
}

func TestEvaluateTrustPolicy_Revocation_NoneMode_Informational(t *testing.T) {
	// revocation-mode=none should NOT fail policy even if cert is in revocation list.
	subject := pkix.Name{CommonName: "Soft Revoke CA"}
	certPEM, _ := generateTestCert(t, subject, time.Now().Add(-time.Hour), time.Now().Add(time.Hour*24), 8)

	path := buildSignedLogWithProvenance(t, certPEM)
	data, _ := os.ReadFile(path)
	var log SignedAuditLog
	require.NoError(t, json.Unmarshal(data, &log))

	policy := &TrustPolicyConfig{
		RevokedCertificates: []string{"8"},
		RevocationPolicy:    "none",
	}
	result := evaluateTrustPolicy(&log, policy)
	assert.True(t, result.RevocationValid, "revocation-mode=none should not fail policy")
	assert.True(t, result.Valid)
}

func TestEvaluateTrustPolicy_NoCerts_TrustRootsConfigured(t *testing.T) {
	// No provenance certs + trust roots set → fail trust root check.
	log := SignedAuditLog{Timestamp: time.Now().UTC()}
	policy := &TrustPolicyConfig{
		TrustRoots: []string{"-----BEGIN CERTIFICATE-----\nFAKEROOT\n-----END CERTIFICATE-----"},
	}
	result := evaluateTrustPolicy(&log, policy)
	assert.False(t, result.TrustRootValid)
	assert.False(t, result.Valid)
	require.NotEmpty(t, result.Issues)
	assert.Contains(t, result.Issues[0], "no certificates present")
}

func TestEvaluateTrustPolicy_UnknownChain_TrustRoots(t *testing.T) {
	// Cert signed by unknown CA, trust roots configured → should fail.
	subject := pkix.Name{CommonName: "Unknown Root"}
	certPEM, _ := generateTestCert(t, subject, time.Now().Add(-time.Hour), time.Now().Add(time.Hour*24), 10)

	// Use a *different* root PEM as the configured trust root.
	trustedSubject := pkix.Name{CommonName: "Official Trusted Root"}
	trustedPEM, _ := generateTestCert(t, trustedSubject, time.Now().Add(-time.Hour), time.Now().Add(time.Hour*24), 11)

	path := buildSignedLogWithProvenance(t, certPEM)
	data, _ := os.ReadFile(path)
	var log SignedAuditLog
	require.NoError(t, json.Unmarshal(data, &log))

	policy := &TrustPolicyConfig{TrustRoots: []string{trustedPEM}}
	result := evaluateTrustPolicy(&log, policy)
	assert.False(t, result.TrustRootValid, "cert from unknown root should fail")
	assert.False(t, result.Valid)
}

// ── buildTrustPolicy / CLI flag integration tests ─────────────────────────────

func TestBuildTrustPolicy_NoFlags_ReturnsNil(t *testing.T) {
	resetAuditVerifyFlags()
	policy, err := buildTrustPolicy()
	require.NoError(t, err)
	assert.Nil(t, policy, "no trust policy flags → nil policy (crypto-only mode)")
}

func TestBuildTrustPolicy_AllowedIssuers_ParsedCorrectly(t *testing.T) {
	resetAuditVerifyFlags()
	auditVerifyAllowedIssuers = " CN=Root CA , OU=Security "
	defer resetAuditVerifyFlags()

	policy, err := buildTrustPolicy()
	require.NoError(t, err)
	require.NotNil(t, policy)
	assert.Equal(t, []string{"CN=Root CA", "OU=Security"}, policy.AllowedIssuers)
}

func TestBuildTrustPolicy_RevokedCerts_CommaSeparated(t *testing.T) {
	resetAuditVerifyFlags()
	auditVerifyRevokedCerts = "deadbeef01, cafebabe02"
	defer resetAuditVerifyFlags()

	policy, err := buildTrustPolicy()
	require.NoError(t, err)
	require.NotNil(t, policy)
	assert.Equal(t, []string{"deadbeef01", "cafebabe02"}, policy.RevokedCertificates)
}

func TestBuildTrustPolicy_RevokedCerts_FromFile(t *testing.T) {
	resetAuditVerifyFlags()
	f := filepath.Join(t.TempDir(), "revoked.txt")
	require.NoError(t, os.WriteFile(f, []byte("aabbcc01\nddee0203\n"), 0600))
	auditVerifyRevokedCerts = f
	defer resetAuditVerifyFlags()

	policy, err := buildTrustPolicy()
	require.NoError(t, err)
	require.NotNil(t, policy)
	assert.Contains(t, policy.RevokedCertificates, "aabbcc01")
	assert.Contains(t, policy.RevokedCertificates, "ddee0203")
}

func TestBuildTrustPolicy_PolicyConfigFile_LoadedAndCLIOverrides(t *testing.T) {
	resetAuditVerifyFlags()
	cfg := TrustPolicyConfig{
		AllowedIssuers: []string{"File Issuer"},
		CheckValidity:  true,
	}
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	cfgFile := filepath.Join(t.TempDir(), "policy.json")
	require.NoError(t, os.WriteFile(cfgFile, data, 0600))

	auditVerifyPolicyConfig = cfgFile
	auditVerifyAllowedIssuers = "CLI Issuer" // CLI should override file
	defer resetAuditVerifyFlags()

	policy, err := buildTrustPolicy()
	require.NoError(t, err)
	require.NotNil(t, policy)
	assert.Equal(t, []string{"CLI Issuer"}, policy.AllowedIssuers, "CLI flag should override file value")
	assert.True(t, policy.CheckValidity, "file value retained when no CLI override")
}

func TestBuildTrustPolicy_RevocationMode_ValidValues(t *testing.T) {
	for _, mode := range []string{"strict", "none"} {
		resetAuditVerifyFlags()
		auditVerifyRevocationMode = mode
		policy, err := buildTrustPolicy()
		require.NoError(t, err)
		require.NotNil(t, policy)
		assert.Equal(t, mode, policy.RevocationPolicy)
		resetAuditVerifyFlags()
	}
}

// ── runAuditVerify integration: policy changes only policy outcome ─────────────

func TestRunAuditVerify_TrustPolicy_DoesNotAffectCryptoResult(t *testing.T) {
	// A valid log with an untrusted issuer: signature check should pass, policy
	// check should fail, and the two results must be separately reported.
	subject := pkix.Name{CommonName: "Untrusted Corp"}
	certPEM, _ := generateTestCert(t, subject, time.Now().Add(-time.Hour), time.Now().Add(time.Hour*24), 99)

	payload := map[string]interface{}{"op": "test-policy-separation"}
	logPath, _, _ := buildSignedLog(t, payload)

	// Embed provenance with cert.
	data, _ := os.ReadFile(logPath)
	var log SignedAuditLog
	require.NoError(t, json.Unmarshal(data, &log))
	log.Provenance = &SignatureProvenance{
		CertificateChain: []string{certPEM},
	}
	out, _ := json.MarshalIndent(log, "", "  ")
	require.NoError(t, os.WriteFile(logPath, out, 0600))

	resetAuditVerifyFlags()
	auditVerifyFile = logPath
	auditVerifyAllowedIssuers = "Trusted Corp Only" // will mismatch
	defer resetAuditVerifyFlags()

	var buf bytes.Buffer
	auditVerifyCmd.SetOut(&buf)
	err := auditVerifyCmd.RunE(auditVerifyCmd, nil)

	// Should return an error because policy failed.
	assert.Error(t, err)
	output := buf.String()

	// Cryptographic checks MUST still pass.
	assert.Contains(t, output, "[PASS] Payload hash")
	assert.Contains(t, output, "[PASS] Signature")

	// Policy MUST explicitly report the untrusted issuer.
	assert.Contains(t, output, "Trust Policy")
	assert.Contains(t, output, "[FAIL] Issuer")
	assert.Contains(t, output, "Untrusted Corp")

	// Overall result is INVALID due to policy failure.
	assert.Contains(t, output, "INVALID")
}

func TestRunAuditVerify_TrustPolicy_JSONOutput_ContainsPolicyField(t *testing.T) {
	payload := map[string]interface{}{"op": "json-policy-test"}
	logPath, _, _ := buildSignedLog(t, payload)

	resetAuditVerifyFlags()
	auditVerifyFile = logPath
	auditVerifyJSON = true
	auditVerifyCheckValidity = true // will fail: no certs in log
	defer resetAuditVerifyFlags()

	var buf bytes.Buffer
	auditVerifyCmd.SetOut(&buf)
	_ = auditVerifyCmd.RunE(auditVerifyCmd, nil)

	var result auditVerifyResult
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	require.NotNil(t, result.Policy, "policy field must be present when a trust policy was active")
	assert.True(t, result.HashValid)
	assert.True(t, result.SignatureValid)
	// Validity check fails because no certs are present.
	assert.False(t, result.Policy.ValidityValid)
	assert.False(t, result.Valid)
}

func TestRunAuditVerify_DefaultBehavior_NoPolicyField(t *testing.T) {
	// Without any trust policy flags the policy field must be absent.
	payload := map[string]interface{}{"op": "default-no-policy"}
	logPath, _, _ := buildSignedLog(t, payload)

	resetAuditVerifyFlags()
	auditVerifyFile = logPath
	auditVerifyJSON = true
	defer resetAuditVerifyFlags()

	var buf bytes.Buffer
	auditVerifyCmd.SetOut(&buf)
	err := auditVerifyCmd.RunE(auditVerifyCmd, nil)
	require.NoError(t, err)

	var result auditVerifyResult
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	assert.Nil(t, result.Policy, "policy field must be absent when no trust policy was configured")
	assert.True(t, result.Valid)
}

func TestRunAuditVerify_InvalidRevocationMode_Rejected(t *testing.T) {
	resetAuditVerifyFlags()
	auditVerifyRevocationMode = "bogus"
	defer resetAuditVerifyFlags()

	err := auditVerifyPreRunE(auditVerifyCmd, nil)
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "revocation-mode")
}

// ── parseCertChain tests ───────────────────────────────────────────────────────

func TestParseCertChain_EmptyInput(t *testing.T) {
	certs := parseCertChain(nil)
	assert.Empty(t, certs)
}

func TestParseCertChain_ValidCert(t *testing.T) {
	subject := pkix.Name{CommonName: "Parse Test CA"}
	certPEM, _ := generateTestCert(t, subject, time.Now().Add(-time.Hour), time.Now().Add(time.Hour*24), 20)
	certs := parseCertChain([]string{certPEM})
	require.Len(t, certs, 1)
	assert.Equal(t, "Parse Test CA", certs[0].Subject.CommonName)
}

func TestParseCertChain_MalformedPEM_Skipped(t *testing.T) {
	certs := parseCertChain([]string{"-----BEGIN CERTIFICATE-----\nNOTBASE64\n-----END CERTIFICATE-----"})
	assert.Empty(t, certs, "malformed PEM should be silently skipped")
}
