// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package signer

import (
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
)

// SoftwareProvider implements SignerProvider for in-process Ed25519 signing
// using a PEM-encoded PKCS#8 private key or a hex-encoded seed/full key.
//
// Configuration priority (highest to lowest):
//  1. ProviderConfig.SoftwareKeyPEM (literal PEM or file path)
//  2. ProviderConfig.SoftwareKeyHex
//  3. GLASSBOX_AUDIT_PRIVATE_KEY_PEM environment variable
//  4. GLASSBOX_SOFTWARE_PRIVATE_KEY_HEX environment variable
type SoftwareProvider struct{}

// Name returns "software".
func (p *SoftwareProvider) Name() string { return "software" }

// Description returns a short human-readable description.
func (p *SoftwareProvider) Description() string {
	return "Ed25519 software signing using a PEM or hex-encoded private key"
}

// EnvVars documents the environment variables recognised by this provider.
func (p *SoftwareProvider) EnvVars() []EnvVarDoc {
	return []EnvVarDoc{
		{
			Name:        "GLASSBOX_AUDIT_PRIVATE_KEY_PEM",
			Required:    false,
			Description: "PKCS#8 PEM-encoded Ed25519 private key (literal PEM text or file path)",
		},
		{
			Name:        "GLASSBOX_SOFTWARE_PRIVATE_KEY_HEX",
			Required:    false,
			Description: "Hex-encoded Ed25519 private key seed (32 bytes) or full key (64 bytes)",
		},
	}
}

// Validate checks that at least one key source is available and that its
// content is structurally valid. This surfaces PEM parse errors and hex
// format/length errors before any signing work begins, so users get
// actionable messages at configuration time rather than mid-signing.
func (p *SoftwareProvider) Validate(cfg ProviderConfig) error {
	// PEM source takes priority — validate content when present.
	if pem := p.resolveKeyPEM(cfg); pem != "" {
		return p.validatePEMContent(pem)
	}

	// Hex source — validate format and length when present.
	if hexKey := p.resolveKeyHex(cfg); hexKey != "" {
		return p.validateHexContent(hexKey)
	}

	return &Error{
		Op:  "software",
		Msg: "no private key provided; set --software-private-key, GLASSBOX_AUDIT_PRIVATE_KEY_PEM, or GLASSBOX_SOFTWARE_PRIVATE_KEY_HEX",
	}
}

// validatePEMContent attempts to parse the PEM text as a PKCS#8 Ed25519
// private key so structural errors are caught at Validate time, not at
// Create time. It also detects common wrong-format keys and suggests the
// conversion command.
func (p *SoftwareProvider) validatePEMContent(pemText string) error {
	// Detect other PEM types that are not PKCS#8 and give a targeted hint.
	if strings.Contains(pemText, "BEGIN OPENSSH PRIVATE KEY") {
		return &Error{
			Op: "software",
			Msg: "the key is in OpenSSH format; Glassbox requires PKCS#8 PEM format\n" +
				"  Fix: convert with: openssl pkey -in key.pem -out key_pkcs8.pem\n" +
				"  Or generate a new key: openssl genpkey -algorithm ed25519 -out key.pem",
		}
	}
	if strings.Contains(pemText, "BEGIN EC PRIVATE KEY") {
		return &Error{
			Op: "software",
			Msg: "the key is in SEC1 (EC PRIVATE KEY) format; Glassbox requires PKCS#8 PEM format\n" +
				"  Fix: convert with: openssl pkcs8 -topk8 -nocrypt -in key.pem -out key_pkcs8.pem",
		}
	}
	if strings.Contains(pemText, "BEGIN RSA PRIVATE KEY") {
		return &Error{
			Op: "software",
			Msg: "the key is an RSA key; Glassbox requires an Ed25519 PKCS#8 PEM private key\n" +
				"  Fix: generate a new Ed25519 key: openssl genpkey -algorithm ed25519 -out key.pem",
		}
	}

	// Attempt full parse to catch garbled PEM, wrong algorithm, etc.
	if _, err := NewInMemorySignerFromPEM(pemText); err != nil {
		return &Error{
			Op: "software",
			Msg: fmt.Sprintf("invalid Ed25519 private key: %v\n"+
				"  Expected: a PKCS#8 PEM file starting with '-----BEGIN PRIVATE KEY-----'\n"+
				"  Generate: openssl genpkey -algorithm ed25519 -out key.pem", err),
		}
	}
	return nil
}

// validateHexContent checks that the hex string is valid hex and represents
// a key of the correct length (32-byte seed or 64-byte full key).
func (p *SoftwareProvider) validateHexContent(hexKey string) error {
	raw, err := hex.DecodeString(hexKey)
	if err != nil {
		return &Error{
			Op: "software",
			Msg: fmt.Sprintf("GLASSBOX_SOFTWARE_PRIVATE_KEY_HEX is not valid hexadecimal: %v\n"+
				"  Expected: a 64-character hex string (32-byte seed) or 128-character hex string (64-byte full key)\n"+
				"  Fix: re-export the key or set GLASSBOX_AUDIT_PRIVATE_KEY_PEM with a PEM file instead", err),
		}
	}
	if len(raw) != ed25519.SeedSize && len(raw) != ed25519.PrivateKeySize {
		return &Error{
			Op: "software",
			Msg: fmt.Sprintf(
				"GLASSBOX_SOFTWARE_PRIVATE_KEY_HEX has wrong key length: got %d bytes, expected %d (seed) or %d (full key)\n"+
					"  A 32-byte seed = 64 hex characters; a 64-byte full key = 128 hex characters",
				len(raw), ed25519.SeedSize, ed25519.PrivateKeySize,
			),
		}
	}
	return nil
}

// Create returns an InMemorySigner loaded from the first available key source.
func (p *SoftwareProvider) Create(cfg ProviderConfig) (Signer, error) {
	if pem := p.resolveKeyPEM(cfg); pem != "" {
		return NewInMemorySignerFromPEM(pem)
	}
	if hexKey := p.resolveKeyHex(cfg); hexKey != "" {
		return NewInMemorySigner(hexKey)
	}
	return nil, &Error{Op: "software", Msg: "no private key available after validation"}
}

// resolveKeyPEM returns the PEM key text from cfg or environment, reading
// from disk if the value looks like a file path rather than PEM text.
// Returns an error description via the second return when the value looks
// like a file path but the file cannot be read — the caller should treat
// an empty string with a non-nil error as a configuration problem.
func (p *SoftwareProvider) resolveKeyPEM(cfg ProviderConfig) string {
	raw := cfg.SoftwareKeyPEM
	if raw == "" {
		raw = os.Getenv("GLASSBOX_AUDIT_PRIVATE_KEY_PEM")
	}
	if raw == "" {
		return ""
	}
	// If the value doesn't look like PEM, treat it as a file path.
	if !strings.Contains(raw, "-----BEGIN") {
		if data, err := os.ReadFile(raw); err == nil {
			return string(data)
		}
		// File path given but file not readable — return empty so Validate
		// surfaces "no private key provided" rather than silently falling
		// through to the hex source.
		return ""
	}
	return raw
}

// resolveKeyHex returns the hex key from cfg or environment.
func (p *SoftwareProvider) resolveKeyHex(cfg ProviderConfig) string {
	if cfg.SoftwareKeyHex != "" {
		return cfg.SoftwareKeyHex
	}
	return os.Getenv("GLASSBOX_SOFTWARE_PRIVATE_KEY_HEX")
}
