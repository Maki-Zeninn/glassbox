// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package security

import (
	"strings"
	"testing"
)

func TestSecretScanner_APIKeyDetection(t *testing.T) {
	scanner := NewSecretScanner(ModeOptIn)

	tests := []struct {
		name     string
		value    string
		location string
		wantType SecretType
	}{
		{
			name:     "API key with colon",
			value:    "api_key: sk-1234567890abcdefghijklmnopqrstuvwxyz",
			location: "config.api_key",
			wantType: SecretTypeAPIKey,
		},
		{
			name:     "API key with equals",
			value:    "apikey=sk-1234567890abcdefghijklmnopqrstuvwxyz",
			location: "env.APIKEY",
			wantType: SecretTypeAPIKey,
		},
		{
			name:     "Generic long key",
			value:    "key: abcdefghijklmnopqrstuvwxyz123456789012",
			location: "settings.key",
			wantType: SecretTypeAPIKey,
		},
		{
			name:     "Short key - not detected",
			value:    "key: abc123",
			location: "settings.key",
			wantType: "",
		},
		{
			name:     "No key pattern",
			value:    "regular value",
			location: "config.value",
			wantType: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			finding := scanner.ScanString(tt.value, tt.location)
			if finding.Type != tt.wantType {
				t.Errorf("ScanString() type = %v, want %v", finding.Type, tt.wantType)
			}
		})
	}
}

func TestSecretScanner_BearerTokenDetection(t *testing.T) {
	scanner := NewSecretScanner(ModeOptIn)

	tests := []struct {
		name     string
		value    string
		location string
		wantType SecretType
	}{
		{
			name:     "Bearer token prefix",
			value:    "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9",
			location: "authorization.header",
			wantType: SecretTypeBearerToken,
		},
		{
			name:     "bearer token lowercase",
			value:    "bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9",
			location: "auth.token",
			wantType: SecretTypeBearerToken,
		},
		{
			name:     "Bearer token with colon",
			value:    "bearer_token: eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.abc123",
			location: "config.token",
			wantType: SecretTypeBearerToken,
		},
		{
			name:     "Short bearer token",
			value:    "Bearer abc",
			location: "auth.header",
			wantType: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			finding := scanner.ScanString(tt.value, tt.location)
			if finding.Type != tt.wantType {
				t.Errorf("ScanString() type = %v, want %v", finding.Type, tt.wantType)
			}
		})
	}
}

func TestSecretScanner_PrivateKeyDetection(t *testing.T) {
	scanner := NewSecretScanner(ModeOptIn)

	tests := []struct {
		name     string
		value    string
		location string
		wantType SecretType
	}{
		{
			name:     "RSA private key header",
			value:    "-----BEGIN RSA PRIVATE KEY-----",
			location: "ssh.key",
			wantType: SecretTypePrivateKey,
		},
		{
			name:     "EC private key header",
			value:    "-----BEGIN EC PRIVATE KEY-----",
			location: "tls.key",
			wantType: SecretTypePrivateKey,
		},
		{
			name:     "OpenSSH private key",
			value:    "-----BEGIN OPENSSH PRIVATE KEY-----",
			location: "id_rsa",
			wantType: SecretTypePrivateKey,
		},
		{
			name:     "Hex private key in private field",
			value:    "a1b2c3d4e5f6789012345678901234567890abcdef1234567890abcdef1234567890",
			location: "wallet.private_key",
			wantType: SecretTypePrivateKey,
		},
		{
			name:     "Hex key not in private field",
			value:    "a1b2c3d4e5f6789012345678901234567890abcdef1234567890abcdef1234567890",
			location: "tx.hash",
			wantType: "",
		},
		{
			name:     "Regular text",
			value:    "some regular text",
			location: "config.value",
			wantType: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			finding := scanner.ScanString(tt.value, tt.location)
			if finding.Type != tt.wantType {
				t.Errorf("ScanString() type = %v, want %v", finding.Type, tt.wantType)
			}
		})
	}
}

func TestSecretScanner_PEMDetection(t *testing.T) {
	scanner := NewSecretScanner(ModeOptIn)

	tests := []struct {
		name     string
		value    string
		location string
		wantType SecretType
	}{
		{
			name:     "Certificate PEM",
			value:    "-----BEGIN CERTIFICATE-----\nMIIC...",
			location: "tls.cert",
			wantType: SecretTypePEM,
		},
		{
			name:     "Base64 encoded PEM-like data",
			value:    "SGVsbG8gV29ybGQ=\nSW4gYmFzZTY0\nRGF0YSBzdHJpbmc=",
			location: "encoded.data",
			wantType: SecretTypePEM,
		},
		{
			name:     "Single base64 line",
			value:    "SGVsbG8gV29ybGQ=",
			location: "data.value",
			wantType: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			finding := scanner.ScanString(tt.value, tt.location)
			if finding.Type != tt.wantType {
				t.Errorf("ScanString() type = %v, want %v", finding.Type, tt.wantType)
			}
		})
	}
}

func TestSecretScanner_AWSKeyDetection(t *testing.T) {
	scanner := NewSecretScanner(ModeOptIn)

	tests := []struct {
		name     string
		value    string
		location string
		wantType SecretType
	}{
		{
			name:     "AWS access key ID",
			value:    "aws_access_key_id: AKIAIOSFODNN7EXAMPLE",
			location: "aws.credentials",
			wantType: SecretTypeAWSKey,
		},
		{
			name:     "AWS secret access key",
			value:    "aws_secret_access_key: wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			location: "aws.secret",
			wantType: SecretTypeAWSKey,
		},
		{
			name:     "40 char secret in secret field",
			value:    "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY1234",
			location: "credentials.secret",
			wantType: SecretTypeAWSKey,
		},
		{
			name:     "Regular 40 char string",
			value:    "abcdefghijklmnopqrstuvwxyz123456789012",
			location: "data.value",
			wantType: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			finding := scanner.ScanString(tt.value, tt.location)
			if finding.Type != tt.wantType {
				t.Errorf("ScanString() type = %v, want %v", finding.Type, tt.wantType)
			}
		})
	}
}

func TestSecretScanner_GitHubTokenDetection(t *testing.T) {
	scanner := NewSecretScanner(ModeOptIn)

	tests := []struct {
		name     string
		value    string
		location string
		wantType SecretType
	}{
		{
			name:     "GitHub PAT",
			value:    "ghp_1234567890abcdefghijklmnopqrstuvwxyz123456",
			location: "github.token",
			wantType: SecretTypeGitHubToken,
		},
		{
			name:     "GitHub app token",
			value:    "gho_1234567890abcdefghijklmnopqrstuvwxyz123456",
			location: "auth.token",
			wantType: SecretTypeGitHubToken,
		},
		{
			name:     "GitHub server token",
			value:    "ghs_1234567890abcdefghijklmnopqrstuvwxyz123456",
			location: "server.token",
			wantType: SecretTypeGitHubToken,
		},
		{
			name:     "Short GitHub-like string",
			value:    "ghp_abc",
			location: "token.value",
			wantType: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			finding := scanner.ScanString(tt.value, tt.location)
			if finding.Type != tt.wantType {
				t.Errorf("ScanString() type = %v, want %v", finding.Type, tt.wantType)
			}
		})
	}
}

func TestSecretScanner_JWTDetection(t *testing.T) {
	scanner := NewSecretScanner(ModeOptIn)

	tests := []struct {
		name     string
		value    string
		location string
		wantType SecretType
	}{
		{
			name:     "Valid JWT",
			value:    "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c",
			location: "auth.jwt",
			wantType: SecretTypeJWT,
		},
		{
			name:     "Short JWT-like string",
			value:    "abc.def.ghi",
			location: "token.value",
			wantType: "",
		},
		{
			name:     "Invalid base64 in JWT",
			value:    "not!base64.not!base64.not!base64",
			location: "auth.token",
			wantType: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			finding := scanner.ScanString(tt.value, tt.location)
			if finding.Type != tt.wantType {
				t.Errorf("ScanString() type = %v, want %v", finding.Type, tt.wantType)
			}
		})
	}
}

func TestSecretScanner_ScanMap(t *testing.T) {
	scanner := NewSecretScanner(ModeOptIn)

	data := map[string]string{
		"api_key":      "sk-1234567890abcdefghijklmnopqrstuvwxyz",
		"regular_value": "normal text",
		"secret_key":   "-----BEGIN PRIVATE KEY-----",
	}

	result := scanner.ScanMap(data, "config")

	if !result.HasSecrets {
		t.Error("ScanMap() should detect secrets")
	}

	if len(result.Findings) != 2 {
		t.Errorf("ScanMap() found %d secrets, want 2", len(result.Findings))
	}
}

func TestSecretScanner_Override(t *testing.T) {
	scanner := NewSecretScanner(ModeOptIn)

	// Add an override for a test fixture path
	scanner.AddOverride("test.fixture.key")

	data := map[string]string{
		"test.fixture.key": "sk-1234567890abcdefghijklmnopqrstuvwxyz",
		"real.api_key":     "sk-1234567890abcdefghijklmnopqrstuvwxyz",
	}

	result := scanner.ScanMap(data, "")

	// Should only detect the real API key, not the overridden one
	if len(result.Findings) != 1 {
		t.Errorf("ScanMap() with override found %d secrets, want 1", len(result.Findings))
	}

	if result.Findings[0].Location != "real.api_key" {
		t.Errorf("ScanMap() override failed, got location %s", result.Findings[0].Location)
	}
}

func TestSecretScanner_ShouldBlockExport(t *testing.T) {
	t.Run("strict mode with secrets", func(t *testing.T) {
		scanner := NewSecretScanner(ModeStrict)
		result := ScanResult{HasSecrets: true}
		
		if !scanner.ShouldBlockExport(result) {
			t.Error("ShouldBlockExport() should return true in strict mode with secrets")
		}
	})

	t.Run("strict mode without secrets", func(t *testing.T) {
		scanner := NewSecretScanner(ModeStrict)
		result := ScanResult{HasSecrets: false}
		
		if scanner.ShouldBlockExport(result) {
			t.Error("ShouldBlockExport() should return false in strict mode without secrets")
		}
	})

	t.Run("opt-in mode with secrets", func(t *testing.T) {
		scanner := NewSecretScanner(ModeOptIn)
		result := ScanResult{HasSecrets: true}
		
		if scanner.ShouldBlockExport(result) {
			t.Error("ShouldBlockExport() should return false in opt-in mode")
		}
	})
}

func TestSecretScanner_CustomPattern(t *testing.T) {
	scanner := NewSecretScanner(ModeOptIn)

	// Add a custom pattern for detecting "CUSTOM_SECRET:"
	err := scanner.AddCustomPattern("custom", `CUSTOM_SECRET:\s*([A-Z0-9]{10})`)
	if err != nil {
		t.Fatalf("AddCustomPattern() error = %v", err)
	}

	// The custom pattern is stored but not used in basic ScanString
	// This test verifies the pattern compiles correctly
	if _, ok := scanner.customPatterns["custom"]; !ok {
		t.Error("AddCustomPattern() did not store the pattern")
	}
}

func TestSecretScanner_ContextMasking(t *testing.T) {
	scanner := NewSecretScanner(ModeOptIn)

	finding := scanner.ScanString("api_key: sk-1234567890abcdefghijklmnopqrstuvwxyz", "config")

	if finding.Context == "" {
		t.Error("ScanString() should provide context")
	}

	// Context should be masked (contain asterisks)
	if !strings.Contains(finding.Context, "****") {
		t.Error("Context should be masked with asterisks")
	}

	// Context should not contain the full secret
	if strings.Contains(finding.Context, "sk-1234567890abcdefghijklmnopqrstuvwxyz") {
		t.Error("Context should not contain the full secret value")
	}
}
