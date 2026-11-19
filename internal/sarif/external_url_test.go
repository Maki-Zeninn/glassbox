// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package sarif

// external_url_test.go tests that ExternalURL on SecurityWarning propagates
// to artifactLocation.uri in the generated SARIF result, and that the fallback
// to the local file path still works when ExternalURL is empty.

import (
	"strings"
	"testing"
)

func TestResultForSecurityWarning_ExternalURL_UsedAsURI(t *testing.T) {
	w := SecurityWarning{
		RuleID:      RuleDeprecatedHostFunction,
		Title:       "deprecated_host_fn",
		Description: "calls a deprecated host function",
		File:        "/home/user/.cargo/registry/src/index/serde-1.0.0/src/lib.rs",
		Line:        42,
		WasmOffset:  0x1234,
		Function:    "serde::de::Deserialize",
		ExternalURL: "https://docs.rs/crate/serde/1.0.0/source/src/lib.rs",
	}

	result := ResultForSecurityWarning(w, "")

	if len(result.Locations) == 0 {
		t.Fatal("expected at least one location")
	}
	loc := result.Locations[0]
	if loc.PhysicalLocation == nil {
		t.Fatal("expected PhysicalLocation to be set")
	}
	uri := loc.PhysicalLocation.ArtifactLocation.URI
	if !strings.HasPrefix(uri, "https://docs.rs") {
		t.Errorf("URI = %q, want ExternalURL (https://docs.rs...)", uri)
	}
	if loc.PhysicalLocation.Region == nil {
		t.Error("expected Region to be set for line=42")
	} else if loc.PhysicalLocation.Region.StartLine != 42 {
		t.Errorf("StartLine = %d, want 42", loc.PhysicalLocation.Region.StartLine)
	}
}

func TestResultForSecurityWarning_NoExternalURL_UsesLocalFile(t *testing.T) {
	w := SecurityWarning{
		RuleID:      RuleDeprecatedHostFunction,
		Title:       "deprecated_host_fn",
		Description: "calls a deprecated host function",
		File:        "/workspace/my-contract/src/lib.rs",
		Line:        10,
		WasmOffset:  0xabcd,
		Function:    "my_contract::transfer",
		// ExternalURL intentionally empty — local file
	}

	result := ResultForSecurityWarning(w, "")

	if len(result.Locations) == 0 {
		t.Fatal("expected at least one location")
	}
	loc := result.Locations[0]
	if loc.PhysicalLocation == nil {
		t.Fatal("expected PhysicalLocation to be set")
	}
	uri := loc.PhysicalLocation.ArtifactLocation.URI
	if uri != w.File {
		t.Errorf("URI = %q, want local file %q", uri, w.File)
	}
}

func TestResultForSecurityWarning_NoFile_OffsetLocation(t *testing.T) {
	w := SecurityWarning{
		RuleID:     RuleDeprecatedHostFunction,
		Title:      "deprecated_host_fn",
		WasmOffset: 0xbeef,
	}

	result := ResultForSecurityWarning(w, "")

	if len(result.Locations) == 0 {
		t.Fatal("expected at least one location")
	}
	loc := result.Locations[0]
	if loc.LogicalLocation == nil {
		t.Fatal("expected LogicalLocation for no-file warning")
	}
	if loc.LogicalLocation.Kind != LogicalLocationKindWASMOffset {
		t.Errorf("Kind = %q, want %q", loc.LogicalLocation.Kind, LogicalLocationKindWASMOffset)
	}
}

func TestResultForSecurityWarning_GitExternalURL(t *testing.T) {
	w := SecurityWarning{
		RuleID:      RuleAllowlistedUnsafeHostFunction,
		Title:       "unsafe_host_fn",
		Description: "unsafe in this context",
		File:        "/home/.cargo/git/checkouts/soroban-sdk-abc/def/src/lib.rs",
		Line:        100,
		WasmOffset:  0xffff,
		Function:    "soroban_sdk::contract",
		ExternalURL: "https://github.com/stellar/rs-soroban-sdk/blob/a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2/src/lib.rs",
	}

	result := ResultForSecurityWarning(w, "")

	if len(result.Locations) == 0 {
		t.Fatal("expected at least one location")
	}
	uri := result.Locations[0].PhysicalLocation.ArtifactLocation.URI
	if !strings.HasPrefix(uri, "https://github.com/stellar/rs-soroban-sdk/blob/") {
		t.Errorf("URI = %q, want GitHub commit link", uri)
	}
	if !strings.Contains(uri, "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2") {
		t.Errorf("URI = %q, want commit hash in URL", uri)
	}
}

func TestResultForSecurityWarning_ExternalURL_WithArtifactURIFallback(t *testing.T) {
	// When File is empty, artifactURI is used as fallback.
	w := SecurityWarning{
		RuleID:     RuleDeprecatedHostFunction,
		Title:      "test",
		WasmOffset: 0x100,
	}
	artifactURI := "file:///workspace/my_contract.wasm"
	result := ResultForSecurityWarning(w, artifactURI)

	if len(result.Locations) == 0 {
		t.Fatal("expected at least one location")
	}
	// Should have a LogicalLocation (WASM offset) AND a PhysicalLocation (artifact).
	loc := result.Locations[0]
	if loc.LogicalLocation == nil {
		t.Error("expected LogicalLocation for WASM offset")
	}
	if loc.PhysicalLocation == nil {
		t.Error("expected PhysicalLocation from artifactURI fallback")
	} else if loc.PhysicalLocation.ArtifactLocation.URI != artifactURI {
		t.Errorf("PhysicalLocation URI = %q, want %q",
			loc.PhysicalLocation.ArtifactLocation.URI, artifactURI)
	}
}

func TestResultForSourceMapFailure_PhysicalLocation(t *testing.T) {
	f := SourceMapFailure{
		RuleID: RuleSourceMapOffsetNotFound,
		Offset: 0xdeadbeef,
		Reason: "no DWARF entry",
		Fix:    "build with debug=true",
	}
	artifactURI := "file:///workspace/contract.wasm"
	result := ResultForSourceMapFailure(f, artifactURI)

	if len(result.Locations) == 0 {
		t.Fatal("expected at least one location")
	}
	loc := result.Locations[0]
	// Source map failures always have a LogicalLocation (the WASM offset).
	if loc.LogicalLocation == nil {
		t.Error("expected LogicalLocation for source map failure")
	}
	// And a PhysicalLocation pointing at the artifact.
	if loc.PhysicalLocation == nil {
		t.Error("expected PhysicalLocation from artifactURI")
	} else if loc.PhysicalLocation.ArtifactLocation.URI != artifactURI {
		t.Errorf("URI = %q, want %q", loc.PhysicalLocation.ArtifactLocation.URI, artifactURI)
	}
}
