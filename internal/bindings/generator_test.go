// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package bindings

import (
	"testing"
	"time"

	"github.com/dotandev/glassbox/internal/abi"
	"github.com/stellar/go-stellar-sdk/xdr"
)

func TestMapTypeDefToTS(t *testing.T) {
	g := &Generator{}

	tests := []struct {
		name     string
		typeDef  xdr.ScSpecTypeDef
		expected string
	}{
		{"Bool", xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeBool}, "boolean"},
		{"U32", xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeU32}, "number"},
		{"I32", xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeI32}, "number"},
		{"U64", xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeU64}, "bigint"},
		{"I64", xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeI64}, "bigint"},
		{"U128", xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeU128}, "bigint"},
		{"I128", xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeI128}, "bigint"},
		{"String", xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeString}, "string"},
		{"Symbol", xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeSymbol}, "SorobanSymbol"},
		{"Address", xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeAddress}, "Address"},
		{"MuxedAddress", xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeMuxedAddress}, "Address"},
		{"Bytes", xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeBytes}, "Bytes"},
		{"Void", xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeVoid}, "void"},
		{"Val", xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeVal}, "unknown"},
		{
			"Option<string>",
			xdr.ScSpecTypeDef{
				Type:   xdr.ScSpecTypeScSpecTypeOption,
				Option: &xdr.ScSpecTypeOption{ValueType: xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeString}},
			},
			"string | null",
		},
		{
			"Vec<Address>",
			xdr.ScSpecTypeDef{
				Type: xdr.ScSpecTypeScSpecTypeVec,
				Vec:  &xdr.ScSpecTypeVec{ElementType: xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeAddress}},
			},
			"Array<Address>",
		},
		{
			"Map<string,bigint>",
			xdr.ScSpecTypeDef{
				Type: xdr.ScSpecTypeScSpecTypeMap,
				Map: &xdr.ScSpecTypeMap{
					KeyType:   xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeString},
					ValueType: xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeU64},
				},
			},
			"Map<string, bigint>",
		},
		{
			"BytesN(32)",
			xdr.ScSpecTypeDef{
				Type:   xdr.ScSpecTypeScSpecTypeBytesN,
				BytesN: &xdr.ScSpecTypeBytesN{N: 32},
			},
			"Uint8Array /* length: 32 */",
		},
		{
			"UDT",
			xdr.ScSpecTypeDef{
				Type: xdr.ScSpecTypeScSpecTypeUdt,
				Udt:  &xdr.ScSpecTypeUdt{Name: "MyStruct"},
			},
			"MyStruct",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := g.mapTypeDefToTS(tt.typeDef)
			if result != tt.expected {
				t.Errorf("mapTypeDefToTS() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestToPascalCase(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello-world", "HelloWorld"},
		{"my_contract", "MyContract"},
		{"simple", "Simple"},
		{"multi-word-test", "MultiWordTest"},
		{"already", "Already"},
		{"UPPER", "Upper"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := toPascalCase(tt.input)
			if result != tt.expected {
				t.Errorf("toPascalCase(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestByteStableGeneration(t *testing.T) {
	// Create a simple contract spec for testing
	spec := &abi.ContractSpec{
		Functions: []xdr.ScSpecFunctionV0{
			{
				Name: xdr.Scsymbol("transfer"),
				Inputs: []xdr.ScSpecFunctionInputV0{
					{Name: xdr.Scsymbol("from"), Type: xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeAddress}},
					{Name: xdr.Scsymbol("to"), Type: xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeAddress}},
					{Name: xdr.Scsymbol("amount"), Type: xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeI128}},
				},
				Outputs: []xdr.ScSpecTypeDef{{Type: xdr.ScSpecTypeScSpecTypeVoid}},
			},
			{
				Name: xdr.Scsymbol("balance"),
				Inputs: []xdr.ScSpecFunctionInputV0{
					{Name: xdr.Scsymbol("account"), Type: xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeAddress}},
				},
				Outputs: []xdr.ScSpecTypeDef{{Type: xdr.ScSpecTypeScSpecTypeI128}},
			},
		},
		Structs: []xdr.ScSpecUdtStructV0{
			{
				Name: xdr.Scsymbol("Account"),
				Fields: []xdr.ScSpecUdtStructFieldV0{
					{Name: xdr.Scsymbol("address"), Type: xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeAddress}},
					{Name: xdr.Scsymbol("balance"), Type: xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeI128}},
				},
			},
		},
	}

	// Generate twice with fixed timestamp to ensure byte-stability
	fixedTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	var firstGeneration []GeneratedFile
	var secondGeneration []GeneratedFile

	for i := 0; i < 2; i++ {
		cfg := GeneratorConfig{
			SpecBytes:               []byte{}, // Will be set via spec directly
			OutputDir:               "",
			PackageName:             "test-contract",
			ContractID:              "TEST_CONTRACT_ID",
			Network:                 "testnet",
			RuntimeTarget:           RuntimeNode,
			IncludeDebugMeta:        false,
			NoEmbedArtifactMetadata: false,
			fixedGenerationTime:     fixedTime,
		}

		gen := &Generator{config: cfg, spec: spec}
		gen.sortSpec() // Ensure sorting is applied

		files := []GeneratedFile{
			{Path: "types.ts", Content: normalizeLineEndings(gen.generateTypes())},
			{Path: "metadata.ts", Content: normalizeLineEndings(gen.generateMetadata())},
			{Path: "client.ts", Content: normalizeLineEndings(gen.generateClient())},
			{Path: "Glassbox-integration.ts", Content: normalizeLineEndings(gen.generateErstIntegration())},
			{Path: "index.ts", Content: normalizeLineEndings(gen.generateIndex())},
			{Path: "package.json", Content: normalizeLineEndings(gen.generatePackageJSON())},
			{Path: "README.md", Content: normalizeLineEndings(gen.generateReadme())},
		}

		if i == 0 {
			firstGeneration = files
		} else {
			secondGeneration = files
		}
	}

	// Compare all files for byte-identity
	if len(firstGeneration) != len(secondGeneration) {
		t.Fatalf("generation count mismatch: first=%d, second=%d", len(firstGeneration), len(secondGeneration))
	}

	for i := range firstGeneration {
		if firstGeneration[i].Path != secondGeneration[i].Path {
			t.Errorf("file path mismatch at index %d: %s != %s", i, firstGeneration[i].Path, secondGeneration[i].Path)
		}
		if firstGeneration[i].Content != secondGeneration[i].Content {
			t.Errorf("file content mismatch for %s: generations are not byte-stable", firstGeneration[i].Path)
		}
	}
}

func TestNormalizeLineEndings(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "CRLF to LF",
			input:    "line1\r\nline2\r\nline3",
			expected: "line1\nline2\nline3",
		},
		{
			name:     "Already LF",
			input:    "line1\nline2\nline3",
			expected: "line1\nline2\nline3",
		},
		{
			name:     "Mixed",
			input:    "line1\r\nline2\nline3\r\n",
			expected: "line1\nline2\nline3\n",
		},
		{
			name:     "Empty",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeLineEndings(tt.input)
			if result != tt.expected {
				t.Errorf("normalizeLineEndings() = %q, want %q", result, tt.expected)
			}
		})
	}
}
