// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cache

import (
	"testing"
)

var validWASMBytes = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}

func TestNewCacheKey_EmptyToolVersion(t *testing.T) {
	_, err := NewCacheKey(validWASMBytes, KindCompilation, "", nil, nil)
	if err == nil {
		t.Fatal("expected error for empty toolVersion, got nil")
	}
}

func TestNewCacheKey_EmptyWASMBytes(t *testing.T) {
	_, err := NewCacheKey(nil, KindCompilation, "v1.0.0", nil, nil)
	if err == nil {
		t.Fatal("expected error for empty wasmBytes, got nil")
	}
}

func TestNewCacheKey_EmptyKind(t *testing.T) {
	_, err := NewCacheKey(validWASMBytes, "", "v1.0.0", nil, nil)
	if err == nil {
		t.Fatal("expected error for empty kind, got nil")
	}
}

func TestNewCacheKey_Valid(t *testing.T) {
	key, err := NewCacheKey(validWASMBytes, KindCompilation, "v1.0.0", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key.ToolVersion != "v1.0.0" {
		t.Errorf("ToolVersion: want v1.0.0, got %s", key.ToolVersion)
	}
	if key.Kind != KindCompilation {
		t.Errorf("Kind: want %s, got %s", KindCompilation, key.Kind)
	}
	if key.ContentHash == "" {
		t.Error("ContentHash must not be empty")
	}
	if key.SchemaVersion != CacheKeyVersion {
		t.Errorf("SchemaVersion: want %d, got %d", CacheKeyVersion, key.SchemaVersion)
	}
}

func TestCacheKey_Digest_Deterministic(t *testing.T) {
	config := map[string]interface{}{
		"opt_level": 2,
		"features":  []string{"bulk-memory", "simd"},
	}

	k1, err := NewCacheKey(validWASMBytes, KindOptimization, "wasm-opt 1.2.3", config, []string{"build-id", "dwarf"})
	if err != nil {
		t.Fatalf("NewCacheKey k1: %v", err)
	}
	k2, err := NewCacheKey(validWASMBytes, KindOptimization, "wasm-opt 1.2.3", config, []string{"build-id", "dwarf"})
	if err != nil {
		t.Fatalf("NewCacheKey k2: %v", err)
	}

	d1, err := k1.Digest()
	if err != nil {
		t.Fatalf("Digest k1: %v", err)
	}
	d2, err := k2.Digest()
	if err != nil {
		t.Fatalf("Digest k2: %v", err)
	}

	if d1 != d2 {
		t.Fatalf("Digest mismatch for identical keys: %s != %s", d1, d2)
	}
}

func TestCacheKey_Digest_SourceMapInputsOrderIndependent(t *testing.T) {
	config := map[string]string{"mode": "debug"}

	k1, err := NewCacheKey(validWASMBytes, KindSourceMap, "sourcemapper 1.0.0", config, []string{"dwarf", "build-id", "names"})
	if err != nil {
		t.Fatalf("NewCacheKey k1: %v", err)
	}
	k2, err := NewCacheKey(validWASMBytes, KindSourceMap, "sourcemapper 1.0.0", config, []string{"names", "dwarf", "build-id"})
	if err != nil {
		t.Fatalf("NewCacheKey k2: %v", err)
	}

	d1, err := k1.Digest()
	if err != nil {
		t.Fatalf("Digest k1: %v", err)
	}
	d2, err := k2.Digest()
	if err != nil {
		t.Fatalf("Digest k2: %v", err)
	}

	if d1 != d2 {
		t.Fatalf("Digest mismatch for reordered source map inputs: %s != %s", d1, d2)
	}
}
