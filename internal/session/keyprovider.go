// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package session

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/dotandev/glassbox/internal/signer"
)

// SessionKeySize is the length in bytes of the AES-256 data-encryption key a
// KeyProvider must return.
const SessionKeySize = 32

// KeyProvider supplies the symmetric key used to encrypt and decrypt a
// session's sensitive payload (see encryption.go). Implementations must
// return an error rather than a zero-value key when a key cannot be
// produced — Store never falls back to writing plaintext when encryption
// was requested (see Store.SetKeyProvider).
type KeyProvider interface {
	// Name is the canonical provider identifier persisted in
	// EncryptedEnvelope.Provider and accepted by --session-key-provider /
	// GLASSBOX_SESSION_KEY_PROVIDER.
	Name() string

	// Key derives or retrieves the 32-byte AES-256 key for sessionID. salt
	// is a random value generated fresh for every encrypted envelope; a
	// provider that derives keys from a passphrase must fold it into the
	// derivation so envelopes never reuse a key+nonce pair.
	Key(sessionID string, salt []byte) ([]byte, error)

	// EnvVars documents the environment variables this provider reads, for
	// help text and diagnostics.
	EnvVars() []signer.EnvVarDoc
}

// PassphraseKeyProviderName is the registered name of PassphraseKeyProvider.
const PassphraseKeyProviderName = "passphrase"

// PassphraseKeyProvider derives a session key from a user-supplied
// passphrase using HKDF-SHA256, following the same extract/expand
// construction as internal/config's LoadEncryptedConfig — but salted with a
// random per-envelope salt (stored in the envelope) rather than a path
// hash, since session IDs are not a safe substitute for a random salt.
type PassphraseKeyProvider struct {
	Passphrase string
}

func (p PassphraseKeyProvider) Name() string { return PassphraseKeyProviderName }

func (p PassphraseKeyProvider) Key(sessionID string, salt []byte) ([]byte, error) {
	if strings.TrimSpace(p.Passphrase) == "" {
		return nil, fmt.Errorf(
			"session encryption requires a passphrase\n" +
				"  Fix: pass --session-key-passphrase or set GLASSBOX_SESSION_KEY_PASSPHRASE",
		)
	}
	if len(salt) == 0 {
		return nil, fmt.Errorf("session encryption: salt must not be empty")
	}

	info := []byte("glassbox-session:" + sessionID)

	// HKDF extract: prk = HMAC-SHA256(salt, passphrase)
	mac := hmac.New(sha256.New, salt)
	mac.Write([]byte(p.Passphrase))
	prk := mac.Sum(nil)

	// HKDF expand: okm = HMAC-SHA256(prk, info || 0x01)
	mac2 := hmac.New(sha256.New, prk)
	mac2.Write(info)
	mac2.Write([]byte{0x01})
	return mac2.Sum(nil)[:SessionKeySize], nil
}

func (p PassphraseKeyProvider) EnvVars() []signer.EnvVarDoc {
	return []signer.EnvVarDoc{
		{Name: "GLASSBOX_SESSION_KEY_PASSPHRASE", Required: true,
			Description: "Passphrase used to derive the session encryption key."},
	}
}

// EnvKeyProviderName is the registered name of EnvKeyProvider.
const EnvKeyProviderName = "env"

// SessionKeyEnvVar names the environment variable EnvKeyProvider reads a raw
// key from, hex- or base64-encoded.
const SessionKeyEnvVar = "GLASSBOX_SESSION_KEY"

// EnvKeyProvider reads a raw 32-byte key directly from an environment
// variable, hex- or base64-encoded. It ignores the per-envelope salt: the
// key itself is already the full key material, not something to derive
// from.
type EnvKeyProvider struct{}

func (p EnvKeyProvider) Name() string { return EnvKeyProviderName }

func (p EnvKeyProvider) Key(sessionID string, salt []byte) ([]byte, error) {
	raw := os.Getenv(SessionKeyEnvVar)
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf(
			"session encryption requires a key\n"+
				"  Fix: set %s to a %d-byte key, hex- or base64-encoded",
			SessionKeyEnvVar, SessionKeySize,
		)
	}
	key, err := decodeSessionKey(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", SessionKeyEnvVar, err)
	}
	return key, nil
}

func (p EnvKeyProvider) EnvVars() []signer.EnvVarDoc {
	return []signer.EnvVarDoc{
		{Name: SessionKeyEnvVar, Required: true,
			Description: "Raw 32-byte session encryption key, hex- or base64-encoded."},
	}
}

func decodeSessionKey(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if key, err := hex.DecodeString(raw); err == nil && len(key) == SessionKeySize {
		return key, nil
	}
	if key, err := base64.StdEncoding.DecodeString(raw); err == nil && len(key) == SessionKeySize {
		return key, nil
	}
	return nil, fmt.Errorf("expected a %d-byte key, hex- or base64-encoded", SessionKeySize)
}

// ResolveKeyProvider looks up a built-in KeyProvider by name, as configured
// via --session-key-provider / GLASSBOX_SESSION_KEY_PROVIDER.
func ResolveKeyProvider(name, passphrase string) (KeyProvider, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", PassphraseKeyProviderName:
		return PassphraseKeyProvider{Passphrase: passphrase}, nil
	case EnvKeyProviderName:
		return EnvKeyProvider{}, nil
	default:
		return nil, fmt.Errorf(
			"unknown session key provider %q — must be one of: %s, %s\n"+
				"  Fix: pass --session-key-provider passphrase (or env)",
			name, PassphraseKeyProviderName, EnvKeyProviderName,
		)
	}
}
