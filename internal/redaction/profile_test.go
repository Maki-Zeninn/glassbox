// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package redaction

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFullProfile_RedactsSecrets(t *testing.T) {
	p := FullProfile()

	cases := []struct {
		name     string
		input    map[string]interface{}
		keyCheck string
		wantRedact bool
	}{
		{
			name:     "private key value",
			input:    map[string]interface{}{"config": "aabbccdd112233440011223344556677aabbccdd1122334400112233445566788"},
			keyCheck: "config",
			wantRedact: true,
		},
		{
			name:     "stellar secret key",
			input:    map[string]interface{}{"key": "SDXL6BUWX7HQZQBZJN5HQUV3LQHZ7FJVHQXQ5Y3T6GFLQY2SDNM2Q"},
			keyCheck: "key",
			wantRedact: true,
		},
		{
			name:     "normal value preserved",
			input:    map[string]interface{}{"network": "testnet"},
			keyCheck: "network",
			wantRedact: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := p.ApplyToMap(tc.input)
			val, ok := result[tc.keyCheck].(string)
			if !ok {
				t.Fatalf("expected string value for key %q", tc.keyCheck)
			}
			if tc.wantRedact && val != Placeholder {
				t.Errorf("expected redacted value, got %q", val)
			}
			if !tc.wantRedact && val == Placeholder {
				t.Errorf("value should not have been redacted, got %q", val)
			}
		})
	}
}

func TestFullProfile_RedactsStringMap(t *testing.T) {
	p := FullProfile()

	m := map[string]string{
		"rpc_token":      "my-secret-token",
		"network":        "testnet",
		"GLASSBOX_DSN":   "https://abc@sentry.io/123",
		"cache_path":     "/home/user/.cache",
	}

	result := p.ApplyToStringMap(m)

	if result["rpc_token"] != Placeholder {
		t.Errorf("rpc_token should be redacted, got %q", result["rpc_token"])
	}
	if result["network"] != "testnet" {
		t.Errorf("network should be preserved, got %q", result["network"])
	}
	if result["GLASSBOX_DSN"] != Placeholder {
		t.Errorf("GLASSBOX_DSN should be redacted, got %q", result["GLASSBOX_DSN"])
	}
	// cache_path contains "path" keyword
	if result["cache_path"] != Placeholder {
		t.Errorf("cache_path should be redacted, got %q", result["cache_path"])
	}
}

func TestSecretsOnlyProfile_PreservesPaths(t *testing.T) {
	p := SecretsOnlyProfile()

	m := map[string]string{
		"rpc_token":  "my-secret-token",
		"cache_path": "/home/user/.cache",
		"network":    "testnet",
	}

	result := p.ApplyToStringMap(m)

	if result["rpc_token"] != Placeholder {
		t.Errorf("rpc_token should be redacted")
	}
	// SecretsOnly should NOT redact paths
	if result["cache_path"] != "/home/user/.cache" {
		t.Errorf("secrets-only should preserve paths, got %q", result["cache_path"])
	}
	if result["network"] != "testnet" {
		t.Errorf("network should be preserved")
	}
}

func TestProfilesByName(t *testing.T) {
	tests := []struct {
		name    string
		wantOk  bool
	}{
		{"full", true},
		{"secrets-only", true},
		{"secrets_only", true},
		{"secretsonly", true},
		{"unknown", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := ProfilesByName(tt.name)
			if ok != tt.wantOk {
				t.Errorf("ProfilesByName(%q) ok = %v, want %v", tt.name, ok, tt.wantOk)
			}
		})
	}
}

func TestProfile_Apply_StringValue(t *testing.T) {
	p := FullProfile()

	if p.Apply("testnet") != "testnet" {
		t.Error("normal string should pass through")
	}
	if p.Apply("SDXL6BUWX7HQZQBZJN5HQUV3LQHZ7FJVHQXQ5Y3T6GFLQY2SDNM2Q") != Placeholder {
		t.Error("stellar key should be redacted")
	}
}

func TestProfile_CustomPlaceholder(t *testing.T) {
	p := &Profile{
		Name:                "custom",
		RedactedPlaceholder: "***HIDDEN***",
		Rules: []Rule{
			{Field: "secret", Type: FieldSecret},
		},
	}
	_ = p.Compile()

	result := p.ApplyToStringMap(map[string]string{
		"my_secret": "value",
	})
	if result["my_secret"] != "***HIDDEN***" {
		t.Errorf("expected custom placeholder, got %q", result["my_secret"])
	}
}

func TestLoadProfileFromFile(t *testing.T) {
	dir := t.TempDir()
	profileJSON := `{
		"name": "test-profile",
		"description": "test",
		"rules": [
			{"field": "api_key", "type": "token"}
		]
	}`
	path := filepath.Join(dir, "profile.json")
	if err := os.WriteFile(path, []byte(profileJSON), 0644); err != nil {
		t.Fatal(err)
	}

	p, err := LoadProfileFromFile(path)
	if err != nil {
		t.Fatalf("LoadProfileFromFile failed: %v", err)
	}
	if p.Name != "test-profile" {
		t.Errorf("expected name 'test-profile', got %q", p.Name)
	}
	if len(p.Rules) != 1 {
		t.Errorf("expected 1 rule, got %d", len(p.Rules))
	}
}

func TestLoadProfileFromFile_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("not json"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadProfileFromFile(path)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestRedactionSummary(t *testing.T) {
	p := FullProfile()
	summary := p.Summary([]string{"rpc_token", "private_key"})

	if summary.ProfileName != "full" {
		t.Errorf("expected profile name 'full', got %q", summary.ProfileName)
	}
	if summary.TotalRedacted != 2 {
		t.Errorf("expected 2 fields redacted, got %d", summary.TotalRedacted)
	}
	if len(summary.FieldsRedacted) != 2 {
		t.Errorf("expected 2 field names, got %d", len(summary.FieldsRedacted))
	}
}

func TestProfile_AccountIDs(t *testing.T) {
	p := FullProfile()

	m := map[string]interface{}{
		"sender_account": "GABC1234567890DEF",
		"amount":         "100.50",
	}

	result := p.ApplyToMap(m)
	if result["sender_account"] != Placeholder {
		t.Error("account ID should be redacted with full profile")
	}
	if result["amount"] != "100.50" {
		t.Error("amount should be preserved")
	}
}

func TestProfile_ContractIDs(t *testing.T) {
	p := FullProfile()

	m := map[string]interface{}{
		"contract_id": "CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQAHHAGCN4B2",
		"name":        "token",
	}

	result := p.ApplyToMap(m)
	if result["contract_id"] != Placeholder {
		t.Error("contract ID should be redacted with full profile")
	}
	if result["name"] != "token" {
		t.Error("name should be preserved")
	}
}
