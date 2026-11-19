// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package session

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// sessionEncryptionMagic identifies the envelope format. It is bound into
// the AES-GCM additional data (AAD) together with the session ID, so a
// tampered or transplanted envelope fails authentication instead of
// decrypting into the wrong session's data.
const sessionEncryptionMagic = "glassbox-session-enc-v1"

// EncryptedEnvelope carries the encrypted form of a session's sensitive
// payload (see SessionPayload). It is stored on Data.EncryptedPayload in
// place of the plaintext fields it replaces; nothing about its content
// reveals the plaintext, the passphrase, or the derived key.
type EncryptedEnvelope struct {
	// Version is the envelope format version, currently always 1.
	Version int `json:"version"`
	// Provider is the KeyProvider.Name() that produced the key used to
	// encrypt this envelope. Decryption must use a provider of the same
	// name, or the operator is told exactly why: keys are not
	// interchangeable across providers.
	Provider string `json:"provider"`
	// Salt is a random value generated fresh per envelope and folded into
	// key derivation by providers that derive rather than supply a fixed
	// key (e.g. PassphraseKeyProvider). Base64-encoded.
	Salt string `json:"salt"`
	// Nonce is the AES-GCM nonce used for this ciphertext. Base64-encoded.
	Nonce string `json:"nonce"`
	// Ciphertext is the AES-256-GCM sealed SessionPayload, including the
	// authentication tag. Base64-encoded.
	Ciphertext string `json:"ciphertext"`
}

// LogSafe returns a redacted summary of the envelope suitable for
// diagnostics and error messages: it never includes the salt, nonce, or
// ciphertext bytes, only metadata about them.
func (e *EncryptedEnvelope) LogSafe() string {
	if e == nil {
		return "<none>"
	}
	return fmt.Sprintf("{provider=%s version=%d ciphertext_len=%d}",
		e.Provider, e.Version, base64DecodedLen(e.Ciphertext))
}

func base64DecodedLen(s string) int {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return 0
	}
	return len(b)
}

// SessionPayload holds exactly the fields of Data considered sensitive
// replay data — transaction inputs, contract arguments, and captured
// execution state. When a session is encrypted, these fields are cleared on
// the Data record and travel only inside EncryptedEnvelope.Ciphertext.
// Everything else on Data (ID, Name, timestamps, Network, Status, TxHash,
// SchemaVersion, EnvFingerprint, provenance) stays in plaintext: the store
// needs it for indexing, listing, and garbage collection.
type SessionPayload struct {
	EnvelopeXdr     string `json:"envelope_xdr,omitempty"`
	ResultXdr       string `json:"result_xdr,omitempty"`
	ResultMetaXdr   string `json:"result_meta_xdr,omitempty"`
	SimRequestJSON  string `json:"sim_request_json,omitempty"`
	SimResponseJSON string `json:"sim_response_json,omitempty"`
	TraceJSON       string `json:"trace_json,omitempty"`
	BundleJSON      string `json:"bundle_json,omitempty"`
	SourceMapJSON   string `json:"source_map_json,omitempty"`
	AnnotationsJSON string `json:"annotations_json,omitempty"`
}

// extractSessionPayload copies the sensitive fields out of data.
func extractSessionPayload(data *Data) SessionPayload {
	return SessionPayload{
		EnvelopeXdr:     data.EnvelopeXdr,
		ResultXdr:       data.ResultXdr,
		ResultMetaXdr:   data.ResultMetaXdr,
		SimRequestJSON:  data.SimRequestJSON,
		SimResponseJSON: data.SimResponseJSON,
		TraceJSON:       data.TraceJSON,
		BundleJSON:      data.BundleJSON,
		SourceMapJSON:   data.SourceMapJSON,
		AnnotationsJSON: data.AnnotationsJSON,
	}
}

// clearSessionPayload zeroes the sensitive fields on data, leaving the
// non-sensitive metadata untouched. Call this after the payload has been
// sealed into an EncryptedEnvelope so the plaintext never coexists with the
// ciphertext on the same record.
func clearSessionPayload(data *Data) {
	data.EnvelopeXdr = ""
	data.ResultXdr = ""
	data.ResultMetaXdr = ""
	data.SimRequestJSON = ""
	data.SimResponseJSON = ""
	data.TraceJSON = ""
	data.BundleJSON = ""
	data.SourceMapJSON = ""
	data.AnnotationsJSON = ""
}

// applySessionPayload writes a decrypted payload's fields back onto data.
func applySessionPayload(data *Data, payload SessionPayload) {
	data.EnvelopeXdr = payload.EnvelopeXdr
	data.ResultXdr = payload.ResultXdr
	data.ResultMetaXdr = payload.ResultMetaXdr
	data.SimRequestJSON = payload.SimRequestJSON
	data.SimResponseJSON = payload.SimResponseJSON
	data.TraceJSON = payload.TraceJSON
	data.BundleJSON = payload.BundleJSON
	data.SourceMapJSON = payload.SourceMapJSON
	data.AnnotationsJSON = payload.AnnotationsJSON
}

// EncryptSessionPayload seals data's sensitive fields into an
// EncryptedEnvelope using a key from provider, then clears the plaintext
// fields on data and sets data.EncryptedPayload. If provider cannot produce
// a key, data is left completely unmodified and an error is returned — this
// is the enforcement point for "no silent fallback to plaintext."
func EncryptSessionPayload(data *Data, provider KeyProvider) error {
	if data == nil {
		return fmt.Errorf("cannot encrypt a nil session")
	}
	if provider == nil {
		return fmt.Errorf(
			"session encryption was requested but no key provider is configured\n" +
				"  Fix: pass --session-key-provider and the matching key/passphrase flag",
		)
	}

	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("failed to generate encryption salt: %w", err)
	}
	key, err := provider.Key(data.ID, salt)
	if err != nil {
		return fmt.Errorf("session encryption: %w", err)
	}
	if len(key) != SessionKeySize {
		return fmt.Errorf("session encryption: key provider %q returned a %d-byte key, want %d",
			provider.Name(), len(key), SessionKeySize)
	}

	payload := extractSessionPayload(data)
	payloadJSON, err := DeterministicMarshal(payload)
	if err != nil {
		return fmt.Errorf("session encryption: failed to marshal payload: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("session encryption: failed to create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("session encryption: failed to create GCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("failed to generate encryption nonce: %w", err)
	}

	ciphertext := gcm.Seal(nil, nonce, payloadJSON, sessionAAD(data.ID))

	data.EncryptedPayload = &EncryptedEnvelope{
		Version:    1,
		Provider:   provider.Name(),
		Salt:       base64.StdEncoding.EncodeToString(salt),
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
	}
	clearSessionPayload(data)
	return nil
}

// DecryptSessionPayload opens data.EncryptedPayload using a key from
// provider and writes the recovered fields back onto data, clearing
// EncryptedPayload. If data is not encrypted, it returns nil without
// requiring a provider. A wrong key, wrong provider, or corrupted/tampered
// ciphertext all produce a clear error and leave data unmodified — callers
// must never treat a decrypt failure as "session has no sensitive data."
func DecryptSessionPayload(data *Data, provider KeyProvider) error {
	if data == nil || data.EncryptedPayload == nil {
		return nil
	}
	env := data.EncryptedPayload
	if provider == nil {
		return fmt.Errorf(
			"session %q is encrypted and no key was provided\n"+
				"  Fix: pass --session-key-provider %s and the matching key/passphrase flag",
			data.ID, env.Provider,
		)
	}
	if provider.Name() != env.Provider {
		return fmt.Errorf(
			"session %q was encrypted with key provider %q, but %q was configured\n"+
				"  Fix: pass --session-key-provider %s",
			data.ID, env.Provider, provider.Name(), env.Provider,
		)
	}

	salt, err := base64.StdEncoding.DecodeString(env.Salt)
	if err != nil {
		return fmt.Errorf("session %q: encrypted envelope has a malformed salt: %w", data.ID, err)
	}
	nonce, err := base64.StdEncoding.DecodeString(env.Nonce)
	if err != nil {
		return fmt.Errorf("session %q: encrypted envelope has a malformed nonce: %w", data.ID, err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(env.Ciphertext)
	if err != nil {
		return fmt.Errorf("session %q: encrypted envelope has malformed ciphertext: %w", data.ID, err)
	}

	key, err := provider.Key(data.ID, salt)
	if err != nil {
		return fmt.Errorf("session %q: %w", data.ID, err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("session %q: failed to create cipher: %w", data.ID, err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("session %q: failed to create GCM: %w", data.ID, err)
	}
	if len(nonce) != gcm.NonceSize() {
		return fmt.Errorf("session %q: encrypted envelope has an invalid nonce length", data.ID)
	}

	plaintext, err := gcm.Open(nil, nonce, ciphertext, sessionAAD(data.ID))
	if err != nil {
		return fmt.Errorf(
			"session %q: decryption failed (wrong key, or the session was tampered with)\n"+
				"  Fix: verify the passphrase/key matches the one used to encrypt this session",
			data.ID,
		)
	}

	var payload SessionPayload
	if err := json.Unmarshal(plaintext, &payload); err != nil {
		return fmt.Errorf("session %q: decrypted payload is malformed: %w", data.ID, err)
	}

	applySessionPayload(data, payload)
	data.EncryptedPayload = nil
	return nil
}

func sessionAAD(sessionID string) []byte {
	return []byte(sessionEncryptionMagic + "\x00" + sessionID)
}

// marshalEncryptedPayload encodes env for the encrypted_payload TEXT column.
// A nil envelope encodes to a SQL NULL, matching every plaintext session in
// the store today (backward compatibility: no migration required).
func marshalEncryptedPayload(env *EncryptedEnvelope) (interface{}, error) {
	if env == nil {
		return nil, nil
	}
	b, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal encrypted payload: %w", err)
	}
	return string(b), nil
}

// unmarshalEncryptedPayload decodes the encrypted_payload column. An empty
// or NULL value (the common case: a plaintext session) decodes to nil.
func unmarshalEncryptedPayload(raw string) (*EncryptedEnvelope, error) {
	if raw == "" {
		return nil, nil
	}
	var env EncryptedEnvelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return nil, fmt.Errorf("encrypted_payload column is malformed: %w", err)
	}
	return &env, nil
}
