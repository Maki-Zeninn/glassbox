// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

//go:build go1.18

package manifest_test

import (
	"encoding/json"
	"testing"

	"github.com/dotandev/glassbox/internal/manifest"
)

// FuzzVerifyManifest fuzzes manifest signature verification with arbitrary JSON.
//
// Security boundary: Verify processes externally-supplied manifest files that
// assert they are cryptographically signed. Malformed input must not produce
// panics, hangs, or misclassified verification results. A panic here could
// allow a crafted manifest to crash the release-verification pipeline.
//
// Excluded from mutation testing: the random key generation path is
// non-deterministic and cannot be killed by a deterministic mutant.
func FuzzVerifyManifest(f *testing.F) {
	// Well-formed manifest with empty crypto fields (signature will be invalid).
	f.Add([]byte(`{"schema_version":"1","version":"v0.0.0","commit":"abc","build_date":"2026-01-01T00:00:00Z","artifacts":[],"manifest_hash":"","signature":"","public_key":""}`))
	// Minimal: only crypto fields.
	f.Add([]byte(`{"manifest_hash":"aabbccdd","signature":"eeff0011","public_key":"22334455"}`))
	// Not an object.
	f.Add([]byte(`[]`))
	f.Add([]byte(`null`))
	f.Add([]byte(nil))
	f.Add([]byte(`{}`))
	// Schema version mismatch.
	f.Add([]byte(`{"schema_version":"99","version":"v1.0.0","manifest_hash":"","signature":"","public_key":""}`))
	// Hex-like but wrong length crypto fields.
	f.Add([]byte(`{"manifest_hash":"aabb","signature":"ccdd","public_key":"eeff"}`))
	// Valid-length but non-hex crypto fields.
	f.Add([]byte(`{"manifest_hash":"ZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ","signature":"` + string(make([]byte, 128)) + `","public_key":"` + string(make([]byte, 64)) + `"}`))
	// Artifacts list with malformed entries.
	f.Add([]byte(`{"artifacts":[{"name":"","sha256":"","size":-1,"kind":""},{"size":9999999999}]}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var sm manifest.SignedManifest
		if err := json.Unmarshal(data, &sm); err != nil {
			return
		}
		// Verify must not panic on any successfully-parsed SignedManifest.
		result := manifest.Verify(&sm)
		_ = result
	})
}

// FuzzCanonicalJSON fuzzes canonical JSON serialization of arbitrary manifests.
//
// CanonicalJSON is the function whose output is hashed and signed. If it panics
// or returns inconsistent results for identical inputs it breaks the signing
// security invariant.
func FuzzCanonicalJSON(f *testing.F) {
	f.Add(`{"schema_version":"1","version":"v1.0.0","commit":"abc","build_date":"2026-01-01T00:00:00Z","artifacts":[]}`)
	f.Add(`{}`)
	f.Add(`{"artifacts":[{"name":"glassbox-linux-amd64.tar.gz","platform":"linux/amd64","sha256":"aabb","size":0,"kind":"archive"}]}`)
	f.Add(`{"artifacts":null}`)
	f.Add(`{"version":null,"commit":null}`)
	f.Add(`{"provenance":{"signer_identity":"ci","key_id":"k1","algorithm":"ed25519"}}`)

	f.Fuzz(func(t *testing.T, jsonStr string) {
		var m manifest.ReleaseManifest
		if err := json.Unmarshal([]byte(jsonStr), &m); err != nil {
			return
		}
		_, _ = manifest.CanonicalJSON(&m)
	})
}

// FuzzManifestHash fuzzes hash computation over arbitrary manifest structures.
//
// Hash is computed over CanonicalJSON output; its correctness is a
// pre-condition for valid signatures. Any panic is a bug.
func FuzzManifestHash(f *testing.F) {
	f.Add(`{"schema_version":"1","version":"v1.0.0","commit":"abc","build_date":"2026-01-01T00:00:00Z","artifacts":[]}`)
	f.Add(`{}`)
	f.Add(`{"artifacts":[{"name":"","sha256":"","size":0,"kind":""}]}`)

	f.Fuzz(func(t *testing.T, jsonStr string) {
		var m manifest.ReleaseManifest
		if err := json.Unmarshal([]byte(jsonStr), &m); err != nil {
			return
		}
		_, _, _ = manifest.Hash(&m)
	})
}
