// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package manifest_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/dotandev/glassbox/internal/manifest"
	"github.com/dotandev/glassbox/internal/signer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// inMemorySigner is a test-only Ed25519 signer that never touches disk.
type inMemorySigner struct {
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
}

func newTestSigner(t *testing.T) *inMemorySigner {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return &inMemorySigner{pub: pub, priv: priv}
}

func (s *inMemorySigner) Sign(data []byte) ([]byte, error) {
	return ed25519.Sign(s.priv, data), nil
}
func (s *inMemorySigner) PublicKey() ([]byte, error) { return s.pub, nil }
func (s *inMemorySigner) Algorithm() string          { return "ed25519" }
func (s *inMemorySigner) KeyOrigin() signer.KeyOriginMetadata {
	return signer.KeyOriginMetadata{
		Provider:  "software",
		Algorithm: "ed25519",
	}
}

// writeTemp writes content to a temporary file and returns its path and the
// directory that contains it.
func writeTemp(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))
	return path
}

func TestHash_IsDeterministic(t *testing.T) {
	m := &manifest.ReleaseManifest{
		SchemaVersion: manifest.SchemaVersion,
		Version:       "v1.0.0",
		Commit:        "abc123",
		BuildDate:     "2026-01-01T00:00:00Z",
		Artifacts: []manifest.Artifact{
			{Name: "glassbox-linux-amd64.tar.gz", Platform: "linux/amd64", SHA256: "aabbcc", Size: 100, Kind: manifest.KindArchive},
		},
	}

	h1, _, err := manifest.Hash(m)
	require.NoError(t, err)
	h2, _, err := manifest.Hash(m)
	require.NoError(t, err)

	assert.Equal(t, h1, h2, "Hash must be deterministic")
}

func TestSign_And_Verify_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "glassbox-linux-amd64.tar.gz", "fake binary archive content")
	writeTemp(t, dir, "checksums.sha256", "aabbcc  glassbox-linux-amd64.tar.gz\n")

	entries := []manifest.ArtifactEntry{
		{Name: "glassbox-linux-amd64.tar.gz", Platform: "linux/amd64", Kind: manifest.KindArchive},
		{Name: "checksums.sha256", Kind: manifest.KindChecksum},
	}

	m, err := manifest.New("v1.0.0", "deadbeef", "2026-01-01T00:00:00Z", "", dir, entries, &manifest.ManifestProvenance{
		SignerIdentity: "test-pipeline",
		Algorithm:      "ed25519",
	})
	require.NoError(t, err)
	require.Len(t, m.Artifacts, 2, "both artifacts should be included")

	s := newTestSigner(t)
	sm, err := manifest.Sign(m, s)
	require.NoError(t, err)
	assert.NotEmpty(t, sm.ManifestHash)
	assert.NotEmpty(t, sm.Signature)
	assert.NotEmpty(t, sm.PublicKey)

	result := manifest.Verify(sm)
	assert.True(t, result.Valid, "freshly signed manifest must verify")
	assert.True(t, result.HashValid)
	assert.True(t, result.SignatureValid)
	assert.True(t, result.ArtifactsComplete)
}

func TestVerify_TamperedArtifact(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "glassbox-linux-amd64.tar.gz", "original content")

	entries := []manifest.ArtifactEntry{
		{Name: "glassbox-linux-amd64.tar.gz", Platform: "linux/amd64", Kind: manifest.KindArchive},
	}
	m, err := manifest.New("v1.0.0", "abc", "2026-01-01T00:00:00Z", "", dir, entries, nil)
	require.NoError(t, err)

	s := newTestSigner(t)
	sm, err := manifest.Sign(m, s)
	require.NoError(t, err)

	// Tamper: change the SHA256 of the first artifact.
	sm.Artifacts[0].SHA256 = "00000000000000000000000000000000000000000000000000000000000000ff"

	result := manifest.Verify(sm)
	assert.False(t, result.Valid, "tampered manifest must not verify")
	assert.False(t, result.HashValid, "hash check must fail after tampering")
}

func TestVerify_WrongSignature(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "glassbox-linux-amd64.tar.gz", "content")

	entries := []manifest.ArtifactEntry{
		{Name: "glassbox-linux-amd64.tar.gz", Platform: "linux/amd64", Kind: manifest.KindArchive},
	}
	m, err := manifest.New("v1.0.0", "abc", "2026-01-01T00:00:00Z", "", dir, entries, nil)
	require.NoError(t, err)

	s := newTestSigner(t)
	sm, err := manifest.Sign(m, s)
	require.NoError(t, err)

	// Replace signature with random bytes (correct length).
	fakeBytes := make([]byte, 64)
	_, _ = rand.Read(fakeBytes)
	sm.Signature = hex.EncodeToString(fakeBytes)

	result := manifest.Verify(sm)
	assert.False(t, result.SignatureValid, "forged signature must not verify")
	assert.False(t, result.Valid)
}

func TestVerify_DuplicateArtifact(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "glassbox-linux-amd64.tar.gz", "content")

	entries := []manifest.ArtifactEntry{
		{Name: "glassbox-linux-amd64.tar.gz", Platform: "linux/amd64", Kind: manifest.KindArchive},
	}
	m, err := manifest.New("v1.0.0", "abc", "2026-01-01T00:00:00Z", "", dir, entries, nil)
	require.NoError(t, err)

	// Manually insert a duplicate before signing so we can test the check.
	m.Artifacts = append(m.Artifacts, m.Artifacts[0])

	s := newTestSigner(t)
	sm, err := manifest.Sign(m, s)
	require.NoError(t, err)

	result := manifest.Verify(sm)
	assert.False(t, result.ArtifactsComplete, "duplicate artifact must be detected")
	assert.Contains(t, result.DuplicateNames, "glassbox-linux-amd64.tar.gz")
	assert.False(t, result.Valid)
}

func TestVerify_MissingArtifactFile_SignFails(t *testing.T) {
	dir := t.TempDir()
	// Do not create the file — New should fail.
	entries := []manifest.ArtifactEntry{
		{Name: "nonexistent.tar.gz", Platform: "linux/amd64", Kind: manifest.KindArchive},
	}
	_, err := manifest.New("v1.0.0", "abc", "2026-01-01T00:00:00Z", "", dir, entries, nil)
	assert.Error(t, err, "New must fail when artifact file is missing")
}

func TestArtifacts_SortedByName(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "z-file.tar.gz", "z")
	writeTemp(t, dir, "a-file.tar.gz", "a")
	writeTemp(t, dir, "m-file.tar.gz", "m")

	entries := []manifest.ArtifactEntry{
		{Name: "z-file.tar.gz", Kind: manifest.KindArchive},
		{Name: "a-file.tar.gz", Kind: manifest.KindArchive},
		{Name: "m-file.tar.gz", Kind: manifest.KindArchive},
	}
	m, err := manifest.New("v1.0.0", "abc", "2026-01-01T00:00:00Z", "", dir, entries, nil)
	require.NoError(t, err)

	names := make([]string, len(m.Artifacts))
	for i, a := range m.Artifacts {
		names[i] = a.Name
	}
	assert.Equal(t, []string{"a-file.tar.gz", "m-file.tar.gz", "z-file.tar.gz"}, names)
}
