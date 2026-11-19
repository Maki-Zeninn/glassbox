// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// registry_metadata.go — Signed registry metadata for protocol entries.
//
// Protocol definitions and upgrade metadata influence replay fidelity and
// must not be accepted solely because they are present locally. This file
// adds:
//
//   - RegistryMetadata: a versioned, signed envelope for protocol entries.
//   - Trusted key configuration with Ed25519 verification.
//   - Expiry, revocation, and tamper detection with stable diagnostic codes.
//   - Offline verification that reports key and protocol versions.
//
// Cryptographic validity is separated from whether a protocol is locally
// enabled: a signature can be valid but the protocol may still be disabled
// in the local configuration.

package protocolreg

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dotandev/glassbox/internal/signer"
)

// RegistryMetadataSchemaVersion is the current schema version for signed
// registry metadata. Version increases are additive-only; older versions
// are always readable.
const RegistryMetadataSchemaVersion = 1

// Diagnostic codes are stable strings for machine consumption and log
// correlation. They never change across releases.
const (
	DiagSignatureValid       = "REG_SIGNATURE_VALID"
	DiagSignatureInvalid     = "REG_SIGNATURE_INVALID"
	DiagKeyUnknown           = "REG_KEY_UNKNOWN"
	DiagKeyExpired           = "REG_KEY_EXPIRED"
	DiagKeyRevoked           = "REG_KEY_REVOKED"
	DiagSchemaUnsupported    = "REG_SCHEMA_UNSUPPORTED"
	DiagProtocolNotFound     = "REG_PROTOCOL_NOT_FOUND"
	DiagProtocolDisabled     = "REG_PROTOCOL_DISABLED"
	DiagOfflineVerification  = "REG_OFFLINE_VERIFICATION"
	DiagContentTampered      = "REG_CONTENT_TAMPERED"
)

// RegistryMetadata is a versioned, signed envelope for protocol registry
// entries. It carries enough information to verify the entry's integrity
// and authenticity without accessing the network.
type RegistryMetadata struct {
	SchemaVersion   int       `json:"schema_version"`
	ProtocolID      string    `json:"protocol_id"`
	ProtocolVersion string    `json:"protocol_version"`
	RegisteredAt    time.Time `json:"registered_at"`
	ExpiresAt       time.Time `json:"expires_at,omitempty"`
	ExecutablePath  string    `json:"executable_path"`
	ExecutableHash  string    `json:"executable_hash,omitempty"`
	Platform        string    `json:"platform"`
	Scheme          string    `json:"scheme"`
	Enabled         bool      `json:"enabled"`
	Revoked         bool      `json:"revoked,omitempty"`
	RevocationNote  string    `json:"revocation_note,omitempty"`

	// Signature fields.
	Signature      string `json:"signature,omitempty"`
	KeyFingerprint string `json:"key_fingerprint,omitempty"`
	Algorithm      string `json:"algorithm,omitempty"`
}

// TrustedKey represents a public key trusted to sign registry metadata.
type TrustedKey struct {
	KeyID       string          `json:"key_id"`
	PublicKey   ed25519.PublicKey `json:"-"`
	PublicHex   string          `json:"public_hex"`
	Label       string          `json:"label,omitempty"`
	ValidFrom   time.Time       `json:"valid_from"`
	ValidUntil  time.Time       `json:"valid_until,omitempty"`
	Revoked     bool            `json:"revoked,omitempty"`
	RevokedAt   time.Time       `json:"revoked_at,omitempty"`
}

// TrustedKeyStore holds the set of keys trusted to sign registry metadata.
type TrustedKeyStore struct {
	Keys []TrustedKey `json:"keys"`
}

// VerificationStatus classifies the outcome of a metadata signature check.
type VerificationStatus string

const (
	VerificationOK           VerificationStatus = "ok"
	VerificationTampered     VerificationStatus = "tampered"
	VerificationKeyUnknown   VerificationStatus = "key_unknown"
	VerificationKeyExpired   VerificationStatus = "key_expired"
	VerificationKeyRevoked   VerificationStatus = "key_revoked"
	VerificationSchemaOld    VerificationStatus = "schema_unsupported"
	VerificationProtocolGone VerificationStatus = "protocol_not_found"
	VerificationOffline      VerificationStatus = "offline"
)

// VerificationDiagnostic is a single finding from metadata verification.
type VerificationDiagnostic struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  VerificationStatus `json:"status"`
}

// VerificationResult is the complete output of VerifyRegistryMetadata.
type VerificationResult struct {
	OK          bool                    `json:"ok"`
	Diagnostics []VerificationDiagnostic `json:"diagnostics"`
	Metadata    *RegistryMetadata       `json:"metadata,omitempty"`
}

// ── Canonical JSON Hashing ──────────────────────────────────────────────────

// canonicalMetadataHash computes the SHA-256 of the metadata's unsigned
// fields in canonical JSON form (sorted keys, no whitespace). The signature
// and key fields are excluded from the hash.
func canonicalMetadataHash(m *RegistryMetadata) (string, error) {
	unsigned := struct {
		SchemaVersion   int       `json:"schema_version"`
		ProtocolID      string    `json:"protocol_id"`
		ProtocolVersion string    `json:"protocol_version"`
		RegisteredAt    time.Time `json:"registered_at"`
		ExpiresAt       time.Time `json:"expires_at,omitempty"`
		ExecutablePath  string    `json:"executable_path"`
		ExecutableHash  string    `json:"executable_hash,omitempty"`
		Platform        string    `json:"platform"`
		Scheme          string    `json:"scheme"`
		Enabled         bool      `json:"enabled"`
		Revoked         bool      `json:"revoked,omitempty"`
		RevocationNote  string    `json:"revocation_note,omitempty"`
	}{
		SchemaVersion:   m.SchemaVersion,
		ProtocolID:      m.ProtocolID,
		ProtocolVersion: m.ProtocolVersion,
		RegisteredAt:    m.RegisteredAt,
		ExpiresAt:       m.ExpiresAt,
		ExecutablePath:  m.ExecutablePath,
		ExecutableHash:  m.ExecutableHash,
		Platform:        m.Platform,
		Scheme:          m.Scheme,
		Enabled:         m.Enabled,
		Revoked:         m.Revoked,
		RevocationNote:  m.RevocationNote,
	}

	data, err := marshalCanonical(unsigned)
	if err != nil {
		return "", fmt.Errorf("canonical marshal: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// marshalCanonical produces deterministic JSON: sorted keys, no indentation.
func marshalCanonical(v interface{}) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var intermediate interface{}
	if err := json.Unmarshal(raw, &intermediate); err != nil {
		return nil, err
	}
	return encodeCanonicalJSON(intermediate)
}

func encodeCanonicalJSON(v interface{}) ([]byte, error) {
	switch val := v.(type) {
	case map[string]interface{}:
		var sb strings.Builder
		sb.WriteByte('{')
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for i, k := range keys {
			if i > 0 {
				sb.WriteByte(',')
			}
			kb, _ := json.Marshal(k)
			sb.Write(kb)
			sb.WriteByte(':')
			vb, err := encodeCanonicalJSON(val[k])
			if err != nil {
				return nil, err
			}
			sb.Write(vb)
		}
		sb.WriteByte('}')
		return []byte(sb.String()), nil
	case []interface{}:
		var sb strings.Builder
		sb.WriteByte('[')
		for i, item := range val {
			if i > 0 {
				sb.WriteByte(',')
			}
			ib, err := encodeCanonicalJSON(item)
			if err != nil {
				return nil, err
			}
			sb.Write(ib)
		}
		sb.WriteByte(']')
		return []byte(sb.String()), nil
	default:
		return json.Marshal(val)
	}
}

// ── Sign & Verify ──────────────────────────────────────────────────────────

// SignRegistryMetadata signs the metadata using the provided signer. The
// signature covers all content fields (everything except Signature,
// KeyFingerprint, and Algorithm). After signing, the envelope is ready
// for persistent storage.
func SignRegistryMetadata(m *RegistryMetadata, s signer.Signer) error {
	if m == nil {
		return fmt.Errorf("nil metadata")
	}
	if s == nil {
		return fmt.Errorf("nil signer")
	}

	hash, err := canonicalMetadataHash(m)
	if err != nil {
		return fmt.Errorf("compute content hash: %w", err)
	}

	sig, err := s.Sign([]byte(hash))
	if err != nil {
		return fmt.Errorf("sign: %w", err)
	}

	m.Signature = hex.EncodeToString(sig)
	origin := s.KeyOrigin()
	m.KeyFingerprint = origin.KeyFingerprint
	m.Algorithm = origin.Algorithm
	return nil
}

// VerifyRegistryMetadata checks the signature on metadata against a trusted
// key store. It reports all findings as structured diagnostics with stable
// codes. Cryptographic validity is assessed independently of whether the
// protocol is locally enabled.
//
// When the key store is nil (offline mode), verification still proceeds for
// structural checks (schema, expiry, revocation) but signature verification
// is skipped with an appropriate diagnostic.
func VerifyRegistryMetadata(m *RegistryMetadata, keys *TrustedKeyStore) *VerificationResult {
	result := &VerificationResult{OK: true}

	if m == nil {
		result.OK = false
		result.Diagnostics = append(result.Diagnostics, VerificationDiagnostic{
			Code:    "REG_METADATA_NIL",
			Message: "metadata is nil",
			Status:  VerificationTampered,
		})
		return result
	}

	result.Metadata = m

	// Schema version check.
	if m.SchemaVersion > RegistryMetadataSchemaVersion {
		result.OK = false
		result.Diagnostics = append(result.Diagnostics, VerificationDiagnostic{
			Code:    DiagSchemaUnsupported,
			Message: fmt.Sprintf("schema version %d is newer than supported %d", m.SchemaVersion, RegistryMetadataSchemaVersion),
			Status:  VerificationSchemaOld,
		})
	}

	// Revocation check.
	if m.Revoked {
		result.OK = false
		note := m.RevocationNote
		if note == "" {
			note = "no reason provided"
		}
		result.Diagnostics = append(result.Diagnostics, VerificationDiagnostic{
			Code:    DiagKeyRevoked,
			Message: fmt.Sprintf("registry entry is revoked: %s", note),
			Status:  VerificationKeyRevoked,
		})
	}

	// Expiry check.
	if !m.ExpiresAt.IsZero() && time.Now().After(m.ExpiresAt) {
		result.OK = false
		result.Diagnostics = append(result.Diagnostics, VerificationDiagnostic{
			Code:    DiagKeyExpired,
			Message: fmt.Sprintf("registry entry expired at %s", m.ExpiresAt.Format(time.RFC3339)),
			Status:  VerificationKeyExpired,
		})
	}

	// Signature verification.
	if m.Signature == "" {
		result.Diagnostics = append(result.Diagnostics, VerificationDiagnostic{
			Code:    DiagSignatureInvalid,
			Message: "no signature present",
			Status:  VerificationTampered,
		})
		result.OK = false
	} else if keys == nil {
		// Offline mode: structural checks done, but can't verify signature.
		result.Diagnostics = append(result.Diagnostics, VerificationDiagnostic{
			Code:    DiagOfflineVerification,
			Message: "signature present but no trusted key store available (offline verification)",
			Status:  VerificationOffline,
		})
	} else {
		// Find the signing key.
		var trustedKey *TrustedKey
		for i := range keys.Keys {
			if keys.Keys[i].KeyID == m.KeyFingerprint || keys.Keys[i].PublicHex == m.KeyFingerprint {
				trustedKey = &keys.Keys[i]
				break
			}
		}

		if trustedKey == nil {
			result.OK = false
			result.Diagnostics = append(result.Diagnostics, VerificationDiagnostic{
				Code:    DiagKeyUnknown,
				Message: fmt.Sprintf("signing key %q is not in the trusted key store", m.KeyFingerprint),
				Status:  VerificationKeyUnknown,
			})
		} else {
			// Check key validity window.
			if !trustedKey.ValidFrom.IsZero() && time.Now().Before(trustedKey.ValidFrom) {
				result.OK = false
				result.Diagnostics = append(result.Diagnostics, VerificationDiagnostic{
					Code:    DiagKeyExpired,
					Message: fmt.Sprintf("trusted key %q is not yet valid (valid from %s)", trustedKey.KeyID, trustedKey.ValidFrom.Format(time.RFC3339)),
					Status:  VerificationKeyExpired,
				})
			}
			if !trustedKey.ValidUntil.IsZero() && time.Now().After(trustedKey.ValidUntil) {
				result.OK = false
				result.Diagnostics = append(result.Diagnostics, VerificationDiagnostic{
					Code:    DiagKeyExpired,
					Message: fmt.Sprintf("trusted key %q expired at %s", trustedKey.KeyID, trustedKey.ValidUntil.Format(time.RFC3339)),
					Status:  VerificationKeyExpired,
				})
			}
			if trustedKey.Revoked {
				result.OK = false
				result.Diagnostics = append(result.Diagnostics, VerificationDiagnostic{
					Code:    DiagKeyRevoked,
					Message: fmt.Sprintf("trusted key %q has been revoked", trustedKey.KeyID),
					Status:  VerificationKeyRevoked,
				})
			}

			// Verify cryptographic signature.
			hash, err := canonicalMetadataHash(m)
			if err != nil {
				result.OK = false
				result.Diagnostics = append(result.Diagnostics, VerificationDiagnostic{
					Code:    DiagContentTampered,
					Message: fmt.Sprintf("failed to compute content hash: %v", err),
					Status:  VerificationTampered,
				})
			} else {
				sigBytes, decodeErr := hex.DecodeString(m.Signature)
				if decodeErr != nil {
					result.OK = false
					result.Diagnostics = append(result.Diagnostics, VerificationDiagnostic{
						Code:    DiagSignatureInvalid,
						Message: fmt.Sprintf("signature is not valid hex: %v", decodeErr),
						Status:  VerificationTampered,
					})
				} else if !ed25519.Verify(trustedKey.PublicKey, []byte(hash), sigBytes) {
					result.OK = false
					result.Diagnostics = append(result.Diagnostics, VerificationDiagnostic{
						Code:    DiagSignatureInvalid,
						Message: "Ed25519 signature does not match content hash",
						Status:  VerificationTampered,
					})
				} else {
					result.Diagnostics = append(result.Diagnostics, VerificationDiagnostic{
						Code:    DiagSignatureValid,
						Message: fmt.Sprintf("signature verified against key %s", trustedKey.KeyID),
						Status:  VerificationOK,
					})
				}
			}
		}
	}

	return result
}

// ── Trusted Key Store Helpers ───────────────────────────────────────────────

// NewTrustedKeyStore creates an empty key store.
func NewTrustedKeyStore() *TrustedKeyStore {
	return &TrustedKeyStore{Keys: []TrustedKey{}}
}

// AddKey adds a trusted key to the store.
func (ks *TrustedKeyStore) AddKey(k TrustedKey) {
	ks.Keys = append(ks.Keys, k)
}

// AddKeyFromSigner extracts the public key from a signer and adds it as a
// trusted key with the given ID and validity window.
func (ks *TrustedKeyStore) AddKeyFromSigner(keyID string, s signer.Signer, validFrom, validUntil time.Time) error {
	pub, err := s.PublicKey()
	if err != nil {
		return fmt.Errorf("extract public key: %w", err)
	}
	origin := s.KeyOrigin()
	ks.Keys = append(ks.Keys, TrustedKey{
		KeyID:      keyID,
		PublicKey:  ed25519.PublicKey(pub),
		PublicHex:  hex.EncodeToString(pub),
		Label:      origin.Provider,
		ValidFrom:  validFrom,
		ValidUntil: validUntil,
	})
	return nil
}

// FindKey locates a key by ID or hex-encoded public key bytes.
func (ks *TrustedKeyStore) FindKey(idOrHex string) *TrustedKey {
	for i := range ks.Keys {
		if ks.Keys[i].KeyID == idOrHex || ks.Keys[i].PublicHex == idOrHex {
			return &ks.Keys[i]
		}
	}
	return nil
}

// GenerateTestKey creates an Ed25519 key pair for testing. It returns an
// InMemorySigner and the corresponding TrustedKey. The key is valid from
// now until validUntil.
func GenerateTestKey(keyID string, validUntil time.Time) (*signer.InMemorySigner, TrustedKey, error) {
	pub, priv, err := generateEd25519Key()
	if err != nil {
		return nil, TrustedKey{}, fmt.Errorf("generate key: %w", err)
	}
	s := signer.NewInMemorySignerFromKey(priv)
	hash := sha256.Sum256(pub)
	tk := TrustedKey{
		KeyID:      keyID,
		PublicKey:  pub,
		PublicHex:  hex.EncodeToString(hash[:]),
		Label:      "test",
		ValidFrom:  time.Now(),
		ValidUntil: validUntil,
	}
	return s, tk, nil
}

func generateEd25519Key() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	pub, priv, err := ed25519.GenerateKey(nil)
	return pub, priv, err
}
