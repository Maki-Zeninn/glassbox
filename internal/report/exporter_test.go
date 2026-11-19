// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package report

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dotandev/glassbox/internal/redaction"
)

func TestExporter_WithProfile_RedactsExecutionSteps(t *testing.T) {
	profile := redaction.FullProfile()
	dir := t.TempDir()

	exp, err := NewExporterWithProfile(dir, profile)
	if err != nil {
		t.Fatal(err)
	}

	r := NewReport("test")
	r.Execution = &ExecutionLog{
		TransactionHash: "abc123secrettoken",
		Steps: []ExecutionStep{
			{
				Index:      1,
				Operation:  "call",
				ContractID: "CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQAHHAGCN4B2",
				Function:   "transfer",
				Details:    "some details",
			},
		},
	}

	path, err := exp.Export(r, "json")
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	content := string(data)
	// Contract ID should be redacted
	if contains(content, "CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQAHHAGCN4B2") {
		t.Error("contract ID should be redacted in exported report")
	}
}

func TestExporter_WithoutProfile_NoRedaction(t *testing.T) {
	dir := t.TempDir()

	exp, err := NewExporter(dir)
	if err != nil {
		t.Fatal(err)
	}

	r := NewReport("test")
	r.Execution = &ExecutionLog{
		TransactionHash: "abc123",
		Steps: []ExecutionStep{
			{
				Index:      1,
				ContractID: "some-contract",
				Function:   "transfer",
			},
		},
	}

	path, err := exp.Export(r, "json")
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	content := string(data)
	if !contains(content, "some-contract") {
		t.Error("without profile, contract ID should be preserved")
	}
}

func TestExporter_WithProfile_RedactsMetadataTags(t *testing.T) {
	profile := redaction.FullProfile()
	dir := t.TempDir()

	exp, err := NewExporterWithProfile(dir, profile)
	if err != nil {
		t.Fatal(err)
	}

	r := NewReport("test")
	r.Metadata = &Metadata{
		GeneratorVersion: "1.0.0",
		Tags: map[string]string{
			"rpc_token": "super-secret-token-value",
			"network":   "testnet",
		},
	}

	path, err := exp.Export(r, "json")
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	content := string(data)
	if contains(content, "super-secret-token-value") {
		t.Error("rpc_token in metadata tags should be redacted")
	}
	if !contains(content, "testnet") {
		t.Error("network tag should be preserved")
	}
}

func TestExporter_WithProfile_RedactsKeyFindings(t *testing.T) {
	profile := redaction.SecretsOnlyProfile()
	dir := t.TempDir()

	exp, err := NewExporterWithProfile(dir, profile)
	if err != nil {
		t.Fatal(err)
	}

	r := NewReport("test")
	r.Summary = &Summary{
		KeyFindings: []string{
			"Transaction succeeded",
			"Token value: SDXL6BUWX7HQZQBZJN5HQUV3LQHZ7FJVHQXQ5Y3T6GFLQY2SDNM2Q",
		},
	}

	path, err := exp.Export(r, "json")
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	content := string(data)
	if !contains(content, "Transaction succeeded") {
		t.Error("normal finding should be preserved")
	}
	if contains(content, "SDXL6BUWX7HQZQBZJN5HQUV3LQHZ7FJVHQXQ5Y3T6GFLQY2SDNM2Q") {
		t.Error("stellar key in findings should be redacted")
	}
}

func TestExporter_ExportMultiple_WithProfile(t *testing.T) {
	profile := redaction.FullProfile()
	dir := t.TempDir()

	exp, err := NewExporterWithProfile(dir, profile)
	if err != nil {
		t.Fatal(err)
	}

	r := NewReport("multi-test")
	r.Summary = &Summary{
		KeyFindings: []string{"finding with SDXL6BUWX7HQZQBZJN5HQUV3LQHZ7FJVHQXQ5Y3T6GFLQY2SDNM2Q"},
	}

	results, err := exp.ExportMultiple(r, []string{"json", "html"})
	if err != nil {
		t.Fatal(err)
	}

	for format, path := range results {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s output: %v", format, err)
		}
		if contains(string(data), "SDXL6BUWX7HQZQBZJN5HQUV3LQHZ7FJVHQXQ5Y3T6GFLQY2SDNM2Q") {
			t.Errorf("%s output should have redacted the stellar key", format)
		}
	}
}

func TestExporter_HTML_Redaction(t *testing.T) {
	profile := redaction.FullProfile()
	dir := t.TempDir()

	exp, err := NewExporterWithProfile(dir, profile)
	if err != nil {
		t.Fatal(err)
	}

	r := NewReport("html-redact-test")
	r.Execution = &ExecutionLog{
		TransactionHash: "secret-hash-value",
		Steps: []ExecutionStep{
			{
				Index:      1,
				ContractID: "CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQAHHAGCN4B2",
				Function:   "transfer",
			},
		},
	}

	path, err := exp.Export(r, "html")
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	content := string(data)
	if contains(content, "CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQAHHAGCN4B2") {
		t.Error("HTML report should have redacted contract ID")
	}
}

func contains(s, sub string) bool {
	return len(s) > 0 && len(sub) > 0 && len(s) >= len(sub) && (s == sub || len(s) > 0 && containsSubstr(s, sub))
}

func containsSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// Ensure time import is used
var _ = time.Now
