// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cache

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestArtifactCache_SetAndGet(t *testing.T) {
	cacheDir := t.TempDir()
	manager := NewManager(cacheDir, DefaultConfig())
	artifactCache := NewArtifactCache(manager, 24*time.Hour)

	artifact := &CachedArtifact{
		Key:     "test-contract-id",
		Type:    ArtifactContractCode,
		Network: "testnet",
		Data:    "base64-wasm-data",
	}

	err := artifactCache.Set(artifact)
	require.NoError(t, err)

	got, found, err := artifactCache.Get("test-contract-id", ArtifactContractCode, "testnet")
	require.NoError(t, err)
	assert.True(t, found)
	require.NotNil(t, got)
	assert.Equal(t, "test-contract-id", got.Key)
	assert.Equal(t, "base64-wasm-data", got.Data)
	assert.NotEmpty(t, got.Hash)
}

func TestArtifactCache_Get_NotFound(t *testing.T) {
	cacheDir := t.TempDir()
	manager := NewManager(cacheDir, DefaultConfig())
	artifactCache := NewArtifactCache(manager, 24*time.Hour)

	got, found, err := artifactCache.Get("nonexistent-key", ArtifactContractCode, "testnet")
	require.NoError(t, err)
	assert.False(t, found)
	assert.Nil(t, got)
}

func TestArtifactCache_Expiration(t *testing.T) {
	cacheDir := t.TempDir()
	manager := NewManager(cacheDir, DefaultConfig())
	artifactCache := NewArtifactCache(manager, 24*time.Hour)

	artifact := &CachedArtifact{
		Key:     "test-contract-id",
		Type:    ArtifactContractCode,
		Network: "testnet",
		Data:    "base64-wasm-data",
		// Set expired time explicitly
		ExpiresAt: time.Now().Add(-24 * time.Hour),
	}

	// Manually marshal and write to bypass Set's TTL calculation
	data, _ := json.Marshal(artifact)
	_ = os.WriteFile(artifactCache.artifactPath("test-contract-id", ArtifactContractCode, "testnet"), data, 0600)

	got, found, err := artifactCache.Get("test-contract-id", ArtifactContractCode, "testnet")
	require.NoError(t, err)
	assert.False(t, found)
	assert.Nil(t, got)
}

func TestArtifactCache_VerifyIntegrity(t *testing.T) {
	cacheDir := t.TempDir()
	manager := NewManager(cacheDir, DefaultConfig())
	artifactCache := NewArtifactCache(manager, 24*time.Hour)

	artifact := &CachedArtifact{
		Key:     "test-contract-id",
		Type:    ArtifactContractCode,
		Network: "testnet",
		Data:    "base64-wasm-data",
	}

	err := artifactCache.Set(artifact)
	require.NoError(t, err)

	assert.True(t, artifactCache.VerifyIntegrity(artifact))

	// Tamper with data
	artifact.Data = "tampered-data"
	assert.False(t, artifactCache.VerifyIntegrity(artifact))
}

func TestArtifactCache_Invalidate(t *testing.T) {
	cacheDir := t.TempDir()
	manager := NewManager(cacheDir, DefaultConfig())
	artifactCache := NewArtifactCache(manager, 24*time.Hour)

	artifact := &CachedArtifact{
		Key:     "test-contract-id",
		Type:    ArtifactContractCode,
		Network: "testnet",
		Data:    "base64-wasm-data",
	}

	err := artifactCache.Set(artifact)
	require.NoError(t, err)

	err = artifactCache.Invalidate("test-contract-id", ArtifactContractCode, "testnet")
	require.NoError(t, err)

	got, found, err := artifactCache.Get("test-contract-id", ArtifactContractCode, "testnet")
	require.NoError(t, err)
	assert.False(t, found)
	assert.Nil(t, got)
}

func TestArtifactCache_Clear(t *testing.T) {
	cacheDir := t.TempDir()
	manager := NewManager(cacheDir, DefaultConfig())
	artifactCache := NewArtifactCache(manager, 24*time.Hour)

	for i := 0; i < 3; i++ {
		artifact := &CachedArtifact{
			Key:     "test-contract-id-" + string(rune('0'+i)),
			Type:    ArtifactContractCode,
			Network: "testnet",
			Data:    "base64-wasm-data",
		}
		err := artifactCache.Set(artifact)
		require.NoError(t, err)
	}

	count, err := artifactCache.Clear(ArtifactContractCode, "testnet")
	require.NoError(t, err)
	assert.Equal(t, 3, count)

	got, found, err := artifactCache.Get("test-contract-id-0", ArtifactContractCode, "testnet")
	require.NoError(t, err)
	assert.False(t, found)
	assert.Nil(t, got)
}

func TestArtifactCache_CleanupExpired(t *testing.T) {
	cacheDir := t.TempDir()
	manager := NewManager(cacheDir, DefaultConfig())
	artifactCache := NewArtifactCache(manager, 24*time.Hour)

	// Set an artifact that will not expire
	artifact1 := &CachedArtifact{
		Key:     "valid-contract-id",
		Type:    ArtifactContractCode,
		Network: "testnet",
		Data:    "base64-wasm-data",
	}
	err := artifactCache.Set(artifact1)
	require.NoError(t, err)

	// Set an expired artifact by setting cached time in the past
	artifact2 := &CachedArtifact{
		Key:      "expired-contract-id",
		Type:     ArtifactContractCode,
		Network:  "testnet",
		Data:     "base64-wasm-data",
		CachedAt: time.Now().Add(-48 * time.Hour),
	}
	data, _ := json.Marshal(artifact2)
	_ = os.WriteFile(artifactCache.artifactPath("expired-contract-id", ArtifactContractCode, "testnet"), data, 0600)

	count, err := artifactCache.CleanupExpired()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, count, 1)

	// Verify the valid one still exists
	got, found, err := artifactCache.Get("valid-contract-id", ArtifactContractCode, "testnet")
	require.NoError(t, err)
	assert.True(t, found)
	assert.NotNil(t, got)
}

func TestArtifactCache_MultipleTypesAndNetworks(t *testing.T) {
	cacheDir := t.TempDir()
	manager := NewManager(cacheDir, DefaultConfig())
	artifactCache := NewArtifactCache(manager, 24*time.Hour)

	// Set contract code for testnet
	artifact1 := &CachedArtifact{
		Key:     "contract-1",
		Type:    ArtifactContractCode,
		Network: "testnet",
		Data:    "testnet-wasm",
	}
	err := artifactCache.Set(artifact1)
	require.NoError(t, err)

	// Set ledger entry for mainnet
	artifact2 := &CachedArtifact{
		Key:     "ledger-1",
		Type:    ArtifactLedgerEntry,
		Network: "mainnet",
		Data:    "mainnet-ledger-entry",
	}
	err = artifactCache.Set(artifact2)
	require.NoError(t, err)

	// Verify they are stored separately
	got1, found1, err := artifactCache.Get("contract-1", ArtifactContractCode, "testnet")
	require.NoError(t, err)
	assert.True(t, found1)
	assert.Equal(t, "testnet-wasm", got1.Data)

	got2, found2, err := artifactCache.Get("ledger-1", ArtifactLedgerEntry, "mainnet")
	require.NoError(t, err)
	assert.True(t, found2)
	assert.Equal(t, "mainnet-ledger-entry", got2.Data)

	// Verify cross-lookup fails
	_, found3, _ := artifactCache.Get("contract-1", ArtifactContractCode, "mainnet")
	assert.False(t, found3)
}

func TestArtifactCache_Get_EmptyKey_ReturnsError(t *testing.T) {
	cacheDir := t.TempDir()
	manager := NewManager(cacheDir, DefaultConfig())
	artifactCache := NewArtifactCache(manager, 24*time.Hour)

	_, _, err := artifactCache.Get("", ArtifactContractCode, "testnet")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "key must not be empty")
}

func TestArtifactCache_Set_NilArtifact_ReturnsError(t *testing.T) {
	cacheDir := t.TempDir()
	manager := NewManager(cacheDir, DefaultConfig())
	artifactCache := NewArtifactCache(manager, 24*time.Hour)

	err := artifactCache.Set(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not be nil")
}

func TestArtifactCache_Set_EmptyKey_ReturnsError(t *testing.T) {
	cacheDir := t.TempDir()
	manager := NewManager(cacheDir, DefaultConfig())
	artifactCache := NewArtifactCache(manager, 24*time.Hour)

	err := artifactCache.Set(&CachedArtifact{
		Type:    ArtifactContractCode,
		Network: "testnet",
		Data:    "data",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "key must not be empty")
}

func TestArtifactCache_Invalidate_EmptyKey_ReturnsError(t *testing.T) {
	cacheDir := t.TempDir()
	manager := NewManager(cacheDir, DefaultConfig())
	artifactCache := NewArtifactCache(manager, 24*time.Hour)

	err := artifactCache.Invalidate("", ArtifactContractCode, "testnet")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "key must not be empty")
}