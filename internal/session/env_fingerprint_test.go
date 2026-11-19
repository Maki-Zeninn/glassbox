// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package session

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestEnvFingerprint_Deterministic verifies that BuildEnvFingerprint returns
// the same output for the same environment state.
func TestEnvFingerprint_Deterministic(t *testing.T) {
	// Call BuildEnvFingerprint twice with the same environment
	fp1 := BuildEnvFingerprint()
	fp2 := BuildEnvFingerprint()

	// Both calls should return the same fingerprint
	assert.NotEmpty(t, fp1, "first fingerprint should not be empty")
	assert.NotEmpty(t, fp2, "second fingerprint should not be empty")
	assert.Equal(t, fp1, fp2, "fingerprint should be deterministic")

	// Verify the fingerprint format: sha256: followed by 32 hex chars
	assert.Regexp(t, `^sha256:[a-f0-9]{32}$`, fp1, "fingerprint should match expected format")
}

// TestEnvFingerprint_HashPrefix verifies the fingerprint starts with the expected prefix.
func TestEnvFingerprint_HashPrefix(t *testing.T) {
	fp := BuildEnvFingerprint()
	assert.HasPrefix(t, fp, "sha256:", "fingerprint should start with sha256: prefix")
}

// TestEnvFingerprint_Length verifies the fingerprint has the expected length.
func TestEnvFingerprint_Length(t *testing.T) {
	fp := BuildEnvFingerprint()
	// "sha256:" (7 chars) + 32 hex chars = 39 chars total
	assert.Len(t, fp, 39, "fingerprint should be 39 characters long")
}
