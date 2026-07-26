// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validChainHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" // 64 hex

// testCertPEM and testIntermediatePEM are throwaway self-signed Ed25519
// certificates (TEST USE ONLY, no corresponding private key retained) used
// to exercise validateProvenance's PEM chain parsing.
const testCertPEM = `-----BEGIN CERTIFICATE-----
MIIBTzCCAQGgAwIBAgIUci48itMU8AupSBcnPTco+biRhEUwBQYDK2VwMB0xGzAZ
BgNVBAMMEmdsYXNzYm94LXRlc3QtbGVhZjAeFw0yNjA3MjYwMjI3MDdaFw0zNjA3
MjMwMjI3MDdaMB0xGzAZBgNVBAMMEmdsYXNzYm94LXRlc3QtbGVhZjAqMAUGAytl
cAMhAGURHrP+VkxpADyA6L5qw/MylsgWqZKQfNkzaO3/5UBgo1MwUTAdBgNVHQ4E
FgQUOwUViEqdIyLmQ+IWpLvfY6Xk4BgwHwYDVR0jBBgwFoAUOwUViEqdIyLmQ+IW
pLvfY6Xk4BgwDwYDVR0TAQH/BAUwAwEB/zAFBgMrZXADQQDrakkxeMgfCwdjXlTh
wlwPu/dB88SWNOLK93cbvoGBYpHqWZT+SBGCwe6msWSKlcEEWFpFRh+lujragBGG
jnYH
-----END CERTIFICATE-----`

const testIntermediatePEM = `-----BEGIN CERTIFICATE-----
MIIBXzCCARGgAwIBAgIUXtWBYYQ3FK9y8wom5Juq8ZCasv8wBQYDK2VwMCUxIzAh
BgNVBAMMGmdsYXNzYm94LXRlc3QtaW50ZXJtZWRpYXRlMB4XDTI2MDcyNjAyMjcw
N1oXDTM2MDcyMzAyMjcwN1owJTEjMCEGA1UEAwwaZ2xhc3Nib3gtdGVzdC1pbnRl
cm1lZGlhdGUwKjAFBgMrZXADIQCQnVQUgKpNq3XvxlvPiFHufLeycz9CE+GzDDhY
58xRQqNTMFEwHQYDVR0OBBYEFOg2YVLaoAd+0QoBDOlQZrtP4+PNMB8GA1UdIwQY
MBaAFOg2YVLaoAd+0QoBDOlQZrtP4+PNMA8GA1UdEwEB/wQFMAMBAf8wBQYDK2Vw
A0EAxkLGLhwlri5b6AKWnwOzYj4LUrmeJZgwwH23VxzekFC46AVFaN6tnU4w/wIh
dkcfwhzZ9KoLXW/5Xc/qmHeBDA==
-----END CERTIFICATE-----`

// writePrevHashIntoLog loads the signed log at path, sets its provenance
// previous_signature_hash, and rewrites it. The Ed25519 signature is computed
// over the payload hash, so injecting provenance does not invalidate it.
func writePrevHashIntoLog(t *testing.T, path, prevHash string) {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var log SignedAuditLog
	require.NoError(t, json.Unmarshal(data, &log))
	log.Provenance = &SignatureProvenance{
		SignerIdentity:        "ci@example.com",
		Algorithm:             "Ed25519",
		PreviousSignatureHash: prevHash,
	}
	out, err := json.MarshalIndent(log, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, out, 0600))
}

func resetAuditVerifyFlags() {
	auditVerifyFile = ""
	auditVerifyPublicKey = ""
	auditVerifySchema = ""
	auditVerifyJSON = false
	auditVerifyPreviousHash = ""
}

func TestValidateAuditVerifyInputs(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	validPub := hex.EncodeToString(pub)

	tests := []struct {
		name       string
		pubKey     string
		schema     string
		prevHash   string
		wantErr    bool
		wantSubstr string
	}{
		{name: "all empty", wantErr: false},
		{name: "valid public key", pubKey: validPub, wantErr: false},
		{name: "non-hex public key", pubKey: "zz", wantErr: true, wantSubstr: "public-key"},
		{name: "wrong-length public key", pubKey: hex.EncodeToString([]byte("short")), wantErr: true, wantSubstr: "public-key"},
		{name: "missing schema file", schema: "C:/no/such/schema.json", wantErr: true, wantSubstr: "schema"},
		{name: "valid previous hash", prevHash: validChainHash, wantErr: false},
		{name: "short previous hash", prevHash: "abc123", wantErr: true, wantSubstr: "previous-signature-hash"},
		{name: "non-hex previous hash", prevHash: strings.Repeat("z", 64), wantErr: true, wantSubstr: "previous-signature-hash"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			err := validateAuditVerifyInputs(tt.pubKey, tt.schema, tt.prevHash)
			if tt.wantErr {
				require.Error(t, err)
				if tt.wantSubstr != "" {
					assert.Contains(t, err.Error(), tt.wantSubstr)
				}
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestValidateAuditLogFields(t *testing.T) {
	complete := &SignedAuditLog{
		Signature: strings.Repeat("c", 128),
		TraceHash: validChainHash,
		PublicKey: validChainHash,
		Payload:   json.RawMessage(`{}`),
	}

	t.Run("complete log passes", func(t *testing.T) {
		require.NoError(t, validateAuditLogFields(complete, false))
	})

	t.Run("missing signature", func(t *testing.T) {
		l := *complete
		l.Signature = ""
		err := validateAuditLogFields(&l, false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "signature")
	})

	t.Run("missing public_key without override", func(t *testing.T) {
		l := *complete
		l.PublicKey = ""
		err := validateAuditLogFields(&l, false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "public_key")
	})

	t.Run("missing public_key allowed with override", func(t *testing.T) {
		l := *complete
		l.PublicKey = ""
		require.NoError(t, validateAuditLogFields(&l, true))
	})

	t.Run("missing payload", func(t *testing.T) {
		l := *complete
		l.Payload = nil
		err := validateAuditLogFields(&l, false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "payload")
	})
}

func TestVerifyChainLink(t *testing.T) {
	t.Run("matching hash", func(t *testing.T) {
		log := &SignedAuditLog{Provenance: &SignatureProvenance{PreviousSignatureHash: validChainHash}}
		ok, err := verifyChainLink(log, validChainHash)
		require.NoError(t, err)
		assert.True(t, ok)
	})

	t.Run("case-insensitive match", func(t *testing.T) {
		log := &SignedAuditLog{Provenance: &SignatureProvenance{PreviousSignatureHash: strings.ToUpper(validChainHash)}}
		ok, err := verifyChainLink(log, validChainHash)
		require.NoError(t, err)
		assert.True(t, ok)
	})

	t.Run("mismatch", func(t *testing.T) {
		log := &SignedAuditLog{Provenance: &SignatureProvenance{PreviousSignatureHash: validChainHash}}
		ok, err := verifyChainLink(log, strings.Repeat("b", 64))
		require.Error(t, err)
		assert.False(t, ok)
		assert.Contains(t, err.Error(), "chain link broken")
	})

	t.Run("no provenance", func(t *testing.T) {
		ok, err := verifyChainLink(&SignedAuditLog{}, validChainHash)
		require.Error(t, err)
		assert.False(t, ok)
		assert.Contains(t, err.Error(), "no previous_signature_hash")
	})

	t.Run("empty previous hash", func(t *testing.T) {
		log := &SignedAuditLog{Provenance: &SignatureProvenance{PreviousSignatureHash: ""}}
		ok, err := verifyChainLink(log, validChainHash)
		require.Error(t, err)
		assert.False(t, ok)
	})
}

func TestAuditVerify_ChainLinkValid(t *testing.T) {
	defer resetAuditVerifyFlags()
	payload := map[string]interface{}{"input": "data"}
	path, _, _ := buildSignedLog(t, payload)
	writePrevHashIntoLog(t, path, validChainHash)

	resetAuditVerifyFlags()
	auditVerifyFile = path
	auditVerifyPreviousHash = validChainHash

	var buf bytes.Buffer
	auditVerifyCmd.SetOut(&buf)
	err := auditVerifyCmd.RunE(auditVerifyCmd, nil)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "[PASS] Chain link")
	assert.Contains(t, buf.String(), "VALID")
}

func TestAuditVerify_ChainLinkBroken(t *testing.T) {
	defer resetAuditVerifyFlags()
	payload := map[string]interface{}{"input": "data"}
	path, _, _ := buildSignedLog(t, payload)
	writePrevHashIntoLog(t, path, validChainHash)

	resetAuditVerifyFlags()
	auditVerifyFile = path
	auditVerifyPreviousHash = strings.Repeat("b", 64) // wrong predecessor

	var buf bytes.Buffer
	auditVerifyCmd.SetOut(&buf)
	err := auditVerifyCmd.RunE(auditVerifyCmd, nil)
	require.Error(t, err)
	assert.Contains(t, buf.String(), "[FAIL] Chain link")
	assert.Contains(t, buf.String(), "INVALID")
}

func TestAuditVerify_ChainHashPresentButUnverified(t *testing.T) {
	defer resetAuditVerifyFlags()
	payload := map[string]interface{}{"input": "data"}
	path, _, _ := buildSignedLog(t, payload)
	writePrevHashIntoLog(t, path, validChainHash)

	resetAuditVerifyFlags()
	auditVerifyFile = path
	// No --previous-signature-hash supplied: linkage must not be silently claimed.

	var buf bytes.Buffer
	auditVerifyCmd.SetOut(&buf)
	err := auditVerifyCmd.RunE(auditVerifyCmd, nil)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "not verified")
	assert.NotContains(t, buf.String(), "[PASS] Chain link")
}

func TestAuditVerify_PreRunRejectsBadPublicKey(t *testing.T) {
	defer resetAuditVerifyFlags()
	payload := map[string]interface{}{"input": "data"}
	path, _, _ := buildSignedLog(t, payload)

	resetAuditVerifyFlags()
	auditVerifyFile = path
	auditVerifyPublicKey = "not-hex"

	err := auditVerifyCmd.PreRunE(auditVerifyCmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "public-key")
}

func TestValidateProvenance_IdentityWithoutCertChain_Warns(t *testing.T) {
	prov := &SignatureProvenance{
		SignerIdentity:        "ci@example.com",
		Algorithm:             "Ed25519",
		PreviousSignatureHash: validChainHash,
	}
	valid, err := validateProvenance(prov)
	if valid {
		t.Error("expected provenance to be invalid when identity is set without cert chain")
	}
	if err == nil {
		t.Fatal("expected error for identity without cert chain")
	}
	if !strings.Contains(err.Error(), "certificate chain") {
		t.Errorf("error should mention certificate chain, got: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "chain-of-trust") {
		t.Errorf("error should mention chain-of-trust, got: %q", err.Error())
	}
}

func TestValidateProvenance_ValidChain_Passes(t *testing.T) {
	chain := []string{
		strings.TrimSpace(testCertPEM),
		strings.TrimSpace(testIntermediatePEM),
	}
	prov := &SignatureProvenance{
		CertificateChain:  chain,
		SignerIdentity:    "ci@example.com",
		PreviousSignatureHash: validChainHash,
	}
	valid, err := validateProvenance(prov)
	if !valid {
		t.Fatalf("expected valid provenance with correct cert chain, got error: %v", err)
	}
}

func TestValidateAuditLogFields_RequiresProvider(t *testing.T) {
	data := []byte(`{
		"version": "1.0.0",
		"timestamp": "2026-01-01T00:00:00Z",
		"trace_hash": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"signature": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"public_key": "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		"payload": {}
	}`)
	var log SignedAuditLog
	if err := json.Unmarshal(data, &log); err != nil {
		t.Fatal(err)
	}
	err := validateAuditLogFields(&log, false)
	if err == nil {
		t.Fatal("expected error when provider field is missing")
	}
	if !strings.Contains(err.Error(), "provider") {
		t.Errorf("error should mention provider, got: %q", err.Error())
	}
}

func TestValidateAuditLogFields_AllFieldsPresent_Passes(t *testing.T) {
	data := []byte(`{
		"version": "1.0.0",
		"timestamp": "2026-01-01T00:00:00Z",
		"trace_hash": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"signature": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"public_key": "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		"provider": "software",
		"payload": {}
	}`)
	var log SignedAuditLog
	if err := json.Unmarshal(data, &log); err != nil {
		t.Fatal(err)
	}
	if err := validateAuditLogFields(&log, false); err != nil {
		t.Fatalf("expected no error when all fields present, got: %v", err)
	}
}
