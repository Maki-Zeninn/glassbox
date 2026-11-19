// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package signer

import "fmt"

// Signer is the generic interface for cryptographic signing operations.
// Implementations may hold keys in memory (InMemorySigner) or delegate
// to an external PKCS#11 hardware security module (Pkcs11Signer).
type Signer interface {
	// Sign produces a digital signature over the provided data.
	Sign(data []byte) ([]byte, error)

	// PublicKey returns the raw public key bytes associated with the
	// signing key.
	PublicKey() ([]byte, error)

	// Algorithm returns the signing algorithm name (e.g. "ed25519").
	Algorithm() string

	// KeyOrigin returns non-sensitive metadata describing the origin of the
	// signing key, including the provider name, algorithm, and a public key
	// identifier.  This metadata is intended to be included in signed audit
	// envelopes so verifiers can identify the key without accessing secrets.
	//
	// The returned KeyOriginMetadata must never contain PINs, private key
	// material, or other sensitive data.  Implementations that cannot supply
	// origin metadata return a zero value.
	KeyOrigin() KeyOriginMetadata
}

// KeyOriginMetadata carries non-sensitive metadata about the origin of a
// signing key.  It is included in signed audit envelopes so verifiers can
// identify which provider, algorithm, and public key identity was used
// without needing access to secrets.
type KeyOriginMetadata struct {
	// Provider is the canonical provider name (e.g. "software", "pkcs11", "aws-kms").
	Provider string `json:"provider"`

	// Algorithm is the signing algorithm (e.g. "ed25519", "ECDSA_SHA_512").
	Algorithm string `json:"algorithm"`

	// KeyFingerprint is a non-sensitive public key identifier.  For Ed25519
	// keys this is the hex-encoded SHA-256 of the raw public key bytes.
	// For HSM-backed keys this may be the CKA_LABEL or a truncated key
	// identifier.  It must never contain the private key material.
	KeyFingerprint string `json:"key_fingerprint"`

	// CreatedAt records when the key was created, if known.
	// Zero value means "not reported".
	CreatedAt string `json:"created_at,omitempty"`

	// MemoryLock describes whether the signing key seed was pinned with
	// mlock/VirtualLock. When locking is unavailable the value begins with
	// "WARNING:" so compliance reviewers can spot it in audit metadata.
	MemoryLock string `json:"memory_lock,omitempty"`
}

// Error represents an error originating from a signing operation.
type Error struct {
	Op  string
	Msg string
	Err error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Op, e.Msg, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Op, e.Msg)
}

func (e *Error) Unwrap() error {
	return e.Err
}
