// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// CacheKeyVersion is incremented whenever the key schema changes, forcing
// global invalidation of all previously written entries.
const CacheKeyVersion = 1

// WASMCacheKind identifies which analysis produced a cache entry.
type WASMCacheKind string

const (
	// KindCompilation caches the output of WASM compilation (e.g. optimized bytecode).
	KindCompilation WASMCacheKind = "compilation"
	// KindValidation caches the result of WASM binary validation.
	KindValidation WASMCacheKind = "validation"
	// KindSourceMap caches source-map data derived from DWARF debug info.
	KindSourceMap WASMCacheKind = "sourcemap"
	// KindOptimization caches the output of wasm-opt / DCE passes.
	KindOptimization WASMCacheKind = "optimization"
)

// CacheKey is the canonical, content-addressed identifier for a WASM cache
// entry. Every field that can change the output of an analysis pass must be
// represented here so that a change to any input causes a cache miss.
//
// Key derivation is deterministic: fields are sorted by name before hashing
// so that map iteration order never affects the result.
//
// Always use NewCacheKey to construct keys; direct struct literals produce
// unversioned entries that may fail validation.
type CacheKey struct {
	// ContentHash is the SHA-256 hex digest of the raw WASM bytes being
	// analysed. This is the primary invalidation signal: a file replaced
	// with different bytes (even at the same size) will produce a different
	// hash and therefore a cache miss.
	ContentHash string `json:"content_hash"`

	// Kind identifies the analysis pass this entry belongs to.
	Kind WASMCacheKind `json:"kind"`

	// ToolVersion is the version string of the tool (compiler, validator,
	// optimiser) that produced the entry. A toolchain upgrade automatically
	// invalidates all entries produced by older versions.
	ToolVersion string `json:"tool_version"`

	// ConfigHash is the SHA-256 hex digest of the serialised tool
	// configuration (flags, optimisation level, feature flags, etc.).
	// Any change to compiler or analyser options causes a cache miss.
	ConfigHash string `json:"config_hash"`

	// SourceMapInputs holds additional inputs that affect source-map
	// derivation (e.g. DWARF section hashes, build-id strings). It is
	// nil / empty for passes that do not use source maps.
	SourceMapInputs []string `json:"source_map_inputs,omitempty"`

	// SchemaVersion allows forward-compatible schema evolution. Entries
	// whose SchemaVersion differs from CacheKeyVersion are rejected.
	SchemaVersion int `json:"schema_version"`
}

// NewCacheKey constructs a CacheKey from raw WASM bytes, a tool version
// string, an arbitrary config object (marshalled to JSON for hashing), and
// optional source-map inputs.
//
// configObj may be nil (yields an empty-config hash). sourceMapInputs may be
// nil or empty for passes that have no source-map inputs.
func NewCacheKey(wasmBytes []byte, kind WASMCacheKind, toolVersion string, configObj interface{}, sourceMapInputs []string) (CacheKey, error) {
	if len(wasmBytes) == 0 {
		return CacheKey{}, fmt.Errorf("wasm bytes must not be empty")
	}
	if kind == "" {
		return CacheKey{}, fmt.Errorf("cache kind must not be empty")
	}
	if toolVersion == "" {
		return CacheKey{}, fmt.Errorf("tool version must not be empty")
	}

	// Hash raw WASM content.
	contentDigest := sha256.Sum256(wasmBytes)
	contentHash := hex.EncodeToString(contentDigest[:])

	// Hash config: marshal to canonical JSON (sorted keys) then SHA-256.
	configHash, err := hashConfig(configObj)
	if err != nil {
		return CacheKey{}, fmt.Errorf("failed to hash config: %w", err)
	}

	// Normalise source-map inputs: sort for determinism, deduplicate.
	var smInputs []string
	if len(sourceMapInputs) > 0 {
		seen := make(map[string]bool, len(sourceMapInputs))
		for _, s := range sourceMapInputs {
			if s != "" && !seen[s] {
				seen[s] = true
				smInputs = append(smInputs, s)
			}
		}
		sort.Strings(smInputs)
	}

	return CacheKey{
		ContentHash:     contentHash,
		Kind:            kind,
		ToolVersion:     toolVersion,
		ConfigHash:      configHash,
		SourceMapInputs: smInputs,
		SchemaVersion:   CacheKeyVersion,
	}, nil
}

// Digest returns the SHA-256 hex fingerprint of the entire key. This is what
// is used as the on-disk filename so that the full key does not need to be
// encoded in the path.
func (k CacheKey) Digest() (string, error) {
	if err := k.Validate(); err != nil {
		return "", err
	}

	// Serialise deterministically: sort struct fields via JSON tags.
	type stable struct {
		SchemaVersion   int           `json:"schema_version"`
		Kind            WASMCacheKind `json:"kind"`
		ToolVersion     string        `json:"tool_version"`
		ContentHash     string        `json:"content_hash"`
		ConfigHash      string        `json:"config_hash"`
		SourceMapInputs []string      `json:"source_map_inputs,omitempty"`
	}
	s := stable{
		SchemaVersion:   k.SchemaVersion,
		Kind:            k.Kind,
		ToolVersion:     k.ToolVersion,
		ContentHash:     k.ContentHash,
		ConfigHash:      k.ConfigHash,
		SourceMapInputs: k.SourceMapInputs,
	}
	b, err := json.Marshal(s)
	if err != nil {
		return "", fmt.Errorf("failed to serialise cache key: %w", err)
	}
	d := sha256.Sum256(b)
	return hex.EncodeToString(d[:]), nil
}

// Validate checks that all required fields are present and well-formed.
func (k CacheKey) Validate() error {
	if k.ContentHash == "" {
		return fmt.Errorf("cache key: content_hash is empty")
	}
	if len(k.ContentHash) != 64 {
		return fmt.Errorf("cache key: content_hash must be a 64-char hex string, got %d chars", len(k.ContentHash))
	}
	if k.Kind == "" {
		return fmt.Errorf("cache key: kind is empty")
	}
	if k.ToolVersion == "" {
		return fmt.Errorf("cache key: tool_version is empty")
	}
	if k.ConfigHash == "" {
		return fmt.Errorf("cache key: config_hash is empty")
	}
	if k.SchemaVersion != CacheKeyVersion {
		return fmt.Errorf("cache key: schema_version %d is not supported (want %d)", k.SchemaVersion, CacheKeyVersion)
	}
	return nil
}

// hashConfig marshals configObj to a canonical JSON byte slice and returns its
// SHA-256 hex digest. A nil configObj yields the hash of an empty JSON object.
func hashConfig(configObj interface{}) (string, error) {
	var raw []byte
	var err error

	if configObj == nil {
		raw = []byte("{}")
	} else {
		raw, err = json.Marshal(configObj)
		if err != nil {
			return "", fmt.Errorf("failed to marshal config object: %w", err)
		}
	}

	d := sha256.Sum256(raw)
	return hex.EncodeToString(d[:]), nil
}

// HashWASMContent is a convenience helper that returns the SHA-256 hex digest
// of raw WASM bytes. Callers that have already computed the digest elsewhere
// can pass it directly to CacheKey.ContentHash instead of calling this.
func HashWASMContent(wasmBytes []byte) string {
	d := sha256.Sum256(wasmBytes)
	return hex.EncodeToString(d[:])
}
