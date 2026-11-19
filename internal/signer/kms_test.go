// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package signer

import (
	"crypto/ed25519"
	"testing"
)

func TestKMSProvider_Name(t *testing.T) {
	p := &KMSProvider{}
	if p.Name() != "aws-kms" {
		t.Errorf("Expected name 'aws-kms', got '%s'", p.Name())
	}
}

func TestKMSProvider_Description(t *testing.T) {
	p := &KMSProvider{}
	desc := p.Description()
	if desc == "" {
		t.Error("Expected non-empty description")
	}
	if desc != "AWS Key Management Service (KMS) for audit log signing" {
		t.Errorf("Unexpected description: %s", desc)
	}
}

func TestKMSProvider_Validate(t *testing.T) {
	p := &KMSProvider{}

	tests := []struct {
		name    string
		cfg     ProviderConfig
		wantErr bool
	}{
		{
			name:    "valid config with key ID",
			cfg:     ProviderConfig{Extra: map[string]string{"kms_key_id": "alias/test-key"}},
			wantErr: false,
		},
		{
			name:    "missing key ID",
			cfg:     ProviderConfig{Extra: map[string]string{}},
			wantErr: true,
		},
		{
			name:    "empty key ID",
			cfg:     ProviderConfig{Extra: map[string]string{"kms_key_id": ""}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := p.Validate(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestKMSProvider_EnvVars(t *testing.T) {
	p := &KMSProvider{}
	envVars := p.EnvVars()

	if len(envVars) == 0 {
		t.Error("Expected at least one environment variable")
	}

	// Verify expected env vars are present
	expectedVars := []string{
		"GLASSBOX_AWS_KMS_KEY_ID",
		"GLASSBOX_AWS_KMS_REGION",
	}

	found := make(map[string]bool)
	for _, ev := range envVars {
		found[ev.Name] = true
	}

	for _, expected := range expectedVars {
		if !found[expected] {
			t.Errorf("Expected env var %q not found", expected)
		}
	}
}

func TestValidateKeyID(t *testing.T) {
	tests := []struct {
		name    string
		keyID   string
		wantErr bool
	}{
		{
			name:    "valid alias",
			keyID:   "alias/GlassboxAuditKey",
			wantErr: false,
		},
		{
			name:    "valid ARN",
			keyID:   "arn:aws:kms:us-east-1:123456789012:key/12345678-1234-1234-1234-123456789012",
			wantErr: false,
		},
		{
			name:    "valid key ID",
			keyID:   "12345678-1234-1234-1234-123456789012",
			wantErr: false,
		},
		{
			name:    "empty key ID",
			keyID:   "",
			wantErr: true,
		},
		{
			name:    "invalid ARN format",
			keyID:   "arn:invalid",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateKeyID(tt.keyID)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateKeyID() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestGenerateTestKey(t *testing.T) {
	pub, priv, err := GenerateTestKey()
	if err != nil {
		t.Fatalf("GenerateTestKey failed: %v", err)
	}

	if len(pub) == 0 {
		t.Error("Expected non-empty public key")
	}
	if len(priv) == 0 {
		t.Error("Expected non-empty private key")
	}

	// Verify the key pair works
	message := []byte("test message")
	signature := ed25519.Sign(priv, message)
	if !ed25519.Verify(pub, message, signature) {
		t.Error("Generated key pair failed signature verification")
	}
}

func TestEd25519Verify(t *testing.T) {
	pub, priv, err := GenerateTestKey()
	if err != nil {
		t.Fatalf("GenerateTestKey failed: %v", err)
	}

	message := []byte("test message")
	signature := ed25519.Sign(priv, message)

	// Should pass with correct signature
	if !Ed25519Verify(pub, message, signature) {
		t.Error("Ed25519Verify should pass with correct signature")
	}

	// Should fail with wrong message
	if Ed25519Verify(pub, []byte("wrong message"), signature) {
		t.Error("Ed25519Verify should fail with wrong message")
	}

	// Should fail with wrong public key
	wrongPub, _, _ := GenerateTestKey()
	if Ed25519Verify(wrongPub, message, signature) {
		t.Error("Ed25519Verify should fail with wrong public key")
	}
}
