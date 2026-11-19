// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ArtifactType defines the type of cached artifact
type ArtifactType string

const (
	// ArtifactContractCode caches contract WASM bytecode
	ArtifactContractCode ArtifactType = "contract_code"
	// ArtifactLedgerEntry caches ledger entries
	ArtifactLedgerEntry ArtifactType = "ledger_entry"
)

// CachedArtifact represents a cached contract artifact or ledger entry
type CachedArtifact struct {
	// Key is the lookup key (contract ID or ledger key hash)
	Key string `json:"key"`
	// Type is the artifact type
	Type ArtifactType `json:"type"`
	// Network identifies the Stellar network context
	Network string `json:"network"`
	// Data is the cached content (base64 XDR or raw bytes)
	Data string `json:"data"`
	// CachedAt is when the artifact was cached
	CachedAt time.Time `json:"cached_at"`
	// ExpiresAt is when the artifact should be considered stale
	ExpiresAt time.Time `json:"expires_at"`
	// Hash is the SHA-256 hash of the data for integrity verification
	Hash string `json:"hash"`
	// Size is the size of the cached data in bytes
	Size int64 `json:"size"`
}

// ArtifactCache provides disk-backed caching for contract code and ledger entries
type ArtifactCache struct {
	manager *Manager
	ttl     time.Duration
}

// NewArtifactCache creates a new artifact cache
func NewArtifactCache(manager *Manager, ttl time.Duration) *ArtifactCache {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &ArtifactCache{
		manager: manager,
		ttl:     ttl,
	}
}

// Get retrieves a cached artifact by key and type
func (c *ArtifactCache) Get(key string, artifactType ArtifactType, network string) (*CachedArtifact, bool, error) {
	if key == "" {
		return nil, false, fmt.Errorf("artifact key must not be empty")
	}
	_, err := c.manager.GetCacheDir()
	if err != nil {
		return nil, false, err
	}

	filePath := c.artifactPath(key, artifactType, network)
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("failed to read cached artifact: %w", err)
	}

	var artifact CachedArtifact
	if err := json.Unmarshal(data, &artifact); err != nil {
		return nil, false, fmt.Errorf("failed to parse cached artifact: %w", err)
	}

	// Check expiration
	if time.Now().After(artifact.ExpiresAt) {
		// Remove expired artifact
		_ = os.Remove(filePath)
		return nil, false, nil
	}

	return &artifact, true, nil
}

// Set stores an artifact in the cache
func (c *ArtifactCache) Set(artifact *CachedArtifact) error {
	if artifact == nil {
		return fmt.Errorf("artifact must not be nil")
	}
	if artifact.Key == "" {
		return fmt.Errorf("artifact key must not be empty")
	}
	if artifact.Type == "" {
		return fmt.Errorf("artifact type must not be empty")
	}
	_, err := c.manager.GetCacheDir()
	if err != nil {
		return err
	}

	// Calculate hash for integrity
	hash := sha256.Sum256([]byte(artifact.Data))
	artifact.Hash = hex.EncodeToString(hash[:])
	artifact.Size = int64(len(artifact.Data))
	artifact.CachedAt = time.Now()
	artifact.ExpiresAt = time.Now().Add(c.ttl)

	// Ensure directory exists
	artifactDir := c.artifactDir(artifact.Type, artifact.Network)
	if err := os.MkdirAll(artifactDir, 0755); err != nil {
		return fmt.Errorf("failed to create artifact directory: %w", err)
	}

	filePath := c.artifactPath(artifact.Key, artifact.Type, artifact.Network)
	data, err := json.Marshal(artifact)
	if err != nil {
		return fmt.Errorf("failed to marshal artifact: %w", err)
	}

	if err := os.WriteFile(filePath, data, 0600); err != nil {
		return fmt.Errorf("failed to write cached artifact: %w", err)
	}

	return nil
}

// VerifyIntegrity checks if the cached data matches its stored hash
func (c *ArtifactCache) VerifyIntegrity(artifact *CachedArtifact) bool {
	if artifact == nil {
		return false
	}
	hash := sha256.Sum256([]byte(artifact.Data))
	return hex.EncodeToString(hash[:]) == artifact.Hash
}

// Invalidate removes a specific cached artifact
func (c *ArtifactCache) Invalidate(key string, artifactType ArtifactType, network string) error {
	if key == "" {
		return fmt.Errorf("artifact key must not be empty")
	}
	filePath := c.artifactPath(key, artifactType, network)
	if err := os.Remove(filePath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to invalidate artifact: %w", err)
	}
	return nil
}

// Clear removes all cached artifacts of a specific type and network
func (c *ArtifactCache) Clear(artifactType ArtifactType, network string) (int, error) {
	artifactDir := c.artifactDir(artifactType, network)
	var count int

	err := filepath.Walk(artifactDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			if err := os.Remove(path); err == nil {
				count++
			}
		}
		return nil
	})

	return count, err
}

// CleanupExpired removes all expired artifacts from the cache
func (c *ArtifactCache) CleanupExpired() (int, error) {
	cacheDir := c.manager.cacheDir
	if cacheDir == "" {
		cacheDir = os.TempDir()
	}

	var count int
	now := time.Now()

	err := filepath.Walk(cacheDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		var artifact CachedArtifact
		if err := json.Unmarshal(data, &artifact); err != nil {
			return nil
		}

		if now.After(artifact.ExpiresAt) {
			if err := os.Remove(path); err == nil {
				count++
			}
		}

		return nil
	})

	return count, err
}

// List returns all cached artifacts of a specific type and network
func (c *ArtifactCache) List(artifactType ArtifactType, network string) ([]CachedArtifact, error) {
	artifactDir := c.artifactDir(artifactType, network)
	var artifacts []CachedArtifact

	err := filepath.Walk(artifactDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		var artifact CachedArtifact
		if err := json.Unmarshal(data, &artifact); err != nil {
			return nil
		}

		artifacts = append(artifacts, artifact)
		return nil
	})

	return artifacts, err
}

// artifactDir returns the directory path for a specific artifact type and network
func (c *ArtifactCache) artifactDir(artifactType ArtifactType, network string) string {
	cacheDir := c.manager.cacheDir
	if cacheDir == "" {
		cacheDir = os.TempDir()
	}
	return filepath.Join(cacheDir, string(artifactType), network)
}

// artifactPath returns the full file path for a cached artifact
func (c *ArtifactCache) artifactPath(key string, artifactType ArtifactType, network string) string {
	hash := sha256.Sum256([]byte(key))
	hashStr := hex.EncodeToString(hash[:])
	return filepath.Join(c.artifactDir(artifactType, network), hashStr+".json")
}

// ArtifactCacheProvider provides an interface for the artifact cache
type ArtifactCacheProvider interface {
	Get(key string, artifactType ArtifactType, network string) (*CachedArtifact, bool, error)
	Set(artifact *CachedArtifact) error
	VerifyIntegrity(artifact *CachedArtifact) bool
	Invalidate(key string, artifactType ArtifactType, network string) error
	Clear(artifactType ArtifactType, network string) (int, error)
	CleanupExpired() (int, error)
}

// contextKey is used for context values
type contextKey string

const artifactCacheKey contextKey = "artifact-cache"

// NewContextWithArtifactCache returns a context with the artifact cache attached
func NewContextWithArtifactCache(ctx context.Context, cache *ArtifactCache) context.Context {
	return context.WithValue(ctx, artifactCacheKey, cache)
}

// ArtifactCacheFromContext retrieves the artifact cache from context
func ArtifactCacheFromContext(ctx context.Context) *ArtifactCache {
	if v, ok := ctx.Value(artifactCacheKey).(*ArtifactCache); ok {
		return v
	}
	return nil
}