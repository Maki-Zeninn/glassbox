// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package signer

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"sync"

	"github.com/dotandev/glassbox/internal/logger"
	"github.com/dotandev/glassbox/internal/secutil"
)

// InMemorySigner holds an Ed25519 private key seed in mlock'd SecureBytes and
// implements the Signer interface. This is the default signer for
// backward compatibility with existing callers that pass hex-encoded
// private keys directly.
type InMemorySigner struct {
	mu         sync.Mutex
	seed       *secutil.SecureBytes
	publicKey  ed25519.PublicKey
	closed     bool
	mlockWarn  string
}

// NewInMemorySigner creates an InMemorySigner from a hex-encoded Ed25519
// private key. The key may be either a 32-byte seed or a full 64-byte
// private key. The seed is stored in SecureBytes (mlock when available).
func NewInMemorySigner(privateKeyHex string) (*InMemorySigner, error) {
	raw, err := hex.DecodeString(privateKeyHex)
	if err != nil {
		return nil, &Error{Op: "inmemory", Msg: "invalid private key hex", Err: err}
	}

	if len(raw) != ed25519.PrivateKeySize && len(raw) != ed25519.SeedSize {
		return nil, &Error{
			Op:  "inmemory",
			Msg: fmt.Sprintf("invalid private key length: %d", len(raw)),
		}
	}

	seed := raw
	if len(raw) == ed25519.PrivateKeySize {
		seed = raw[:ed25519.SeedSize]
	}

	sb, err := secutil.NewSecureBytes(ed25519.SeedSize)
	if err != nil {
		return nil, &Error{Op: "inmemory", Msg: "allocate secure seed", Err: err}
	}
	sb.CopyFrom(seed)
	// Zero the temporary decoded buffer immediately.
	secutil.Memzero(raw)
	secutil.Memzero(seed)

	priv := ed25519.NewKeyFromSeed(sb.Bytes())
	pub := priv.Public().(ed25519.PublicKey)

	s := &InMemorySigner{
		seed:      sb,
		publicKey: append(ed25519.PublicKey(nil), pub...),
		mlockWarn: secutil.MlockStatus(),
	}
	if secutil.MlockUnavailable {
		if logger.Logger != nil {
			logger.Logger.Warn("InMemorySigner: mlock unavailable; key may be swapped",
				"status", s.mlockWarn)
		}
	}
	return s, nil
}

// NewInMemorySignerFromKey creates an InMemorySigner from an existing
// ed25519.PrivateKey value. The seed is copied into SecureBytes.
func NewInMemorySignerFromKey(key ed25519.PrivateKey) *InMemorySigner {
	if len(key) != ed25519.PrivateKeySize {
		// Fall back to empty signer that will fail on Sign — preserves
		// historical panic-free behaviour for malformed inputs in tests.
		sb, _ := secutil.NewSecureBytes(ed25519.SeedSize)
		return &InMemorySigner{seed: sb, mlockWarn: secutil.MlockStatus()}
	}
	seed := key.Seed()
	sb, err := secutil.NewSecureBytes(ed25519.SeedSize)
	if err != nil {
		// Soft path: allocate unlocked buffer so existing call sites that
		// ignore errors keep working.
		sb, _ = secutil.NewSecureBytes(ed25519.SeedSize)
	}
	sb.CopyFrom(seed)
	secutil.Memzero(seed)

	priv := ed25519.NewKeyFromSeed(sb.Bytes())
	pub := priv.Public().(ed25519.PublicKey)
	return &InMemorySigner{
		seed:      sb,
		publicKey: append(ed25519.PublicKey(nil), pub...),
		mlockWarn: secutil.MlockStatus(),
	}
}

// NewInMemorySignerFromPEM creates an InMemorySigner from a PEM-encoded
// Ed25519 private key. It supports PKCS#8 PEM private keys.
func NewInMemorySignerFromPEM(pemData string) (*InMemorySigner, error) {
	block, _ := pem.Decode([]byte(pemData))
	if block == nil {
		return nil, &Error{Op: "inmemory", Msg: "invalid PEM private key"}
	}

	privKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, &Error{Op: "inmemory", Msg: "invalid PKCS#8 private key", Err: err}
	}

	edPriv, ok := privKey.(ed25519.PrivateKey)
	if !ok {
		return nil, &Error{Op: "inmemory", Msg: "PEM does not contain an Ed25519 private key"}
	}

	return NewInMemorySignerFromKey(edPriv), nil
}

func (s *InMemorySigner) privateKey() (ed25519.PrivateKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.seed == nil {
		return nil, &Error{Op: "inmemory", Msg: "signer closed"}
	}
	seed := s.seed.Bytes()
	if seed == nil || len(seed) != ed25519.SeedSize {
		return nil, &Error{Op: "inmemory", Msg: "signer seed unavailable"}
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// Sign produces an Ed25519 signature over the provided data.
func (s *InMemorySigner) Sign(data []byte) ([]byte, error) {
	priv, err := s.privateKey()
	if err != nil {
		return nil, err
	}
	sig := ed25519.Sign(priv, data)
	secutil.Memzero(priv)
	return sig, nil
}

// PublicKey returns the raw Ed25519 public key bytes.
func (s *InMemorySigner) PublicKey() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, &Error{Op: "inmemory", Msg: "signer closed"}
	}
	if len(s.publicKey) == 0 {
		return nil, &Error{Op: "inmemory", Msg: "failed to derive public key"}
	}
	return append([]byte(nil), s.publicKey...), nil
}

// Algorithm returns "ed25519".
func (s *InMemorySigner) Algorithm() string {
	return "ed25519"
}

// KeyOrigin returns non-sensitive metadata about the in-memory signing key.
// The key fingerprint is the hex-encoded SHA-256 of the public key bytes.
// MemoryLock carries the mlock status so compliance reviewers can see when
// memory locking was unavailable for a signing session.
func (s *InMemorySigner) KeyOrigin() KeyOriginMetadata {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta := KeyOriginMetadata{
		Provider:   "software",
		Algorithm:  "ed25519",
		MemoryLock: s.mlockWarn,
	}
	if len(s.publicKey) == 0 {
		return meta
	}
	hash := sha256.Sum256(s.publicKey)
	meta.KeyFingerprint = hex.EncodeToString(hash[:])
	return meta
}

// Close zeros the secure seed and releases the memory lock. It is safe to
// call multiple times. Callers should `defer signer.Close()`.
func (s *InMemorySigner) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.seed != nil {
		_ = s.seed.Zero()
	}
	return nil
}

// SeedIsZeroed reports whether the backing seed bytes are all zero. Intended
// for tests after Close().
func (s *InMemorySigner) SeedIsZeroed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seed == nil {
		return true
	}
	b := s.seed.Bytes()
	if b == nil {
		return true
	}
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}
