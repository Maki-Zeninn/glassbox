// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package session

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func encryptableData() *Data {
	return &Data{
		ID:              "enc-session-01",
		TxHash:          "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
		Network:         "testnet",
		Status:          "saved",
		EnvelopeXdr:     "AAAA-secret-envelope==",
		ResultXdr:       "BBBB-secret-result==",
		SimRequestJSON:  `{"contract_args":["super-secret"]}`,
		SimResponseJSON: `{"status":"SUCCESS"}`,
		SchemaVersion:   SchemaVersion,
	}
}

// ── Round trip ────────────────────────────────────────────────────────────

func TestEncryptDecryptSessionPayload_RoundTrip(t *testing.T) {
	data := encryptableData()
	provider := PassphraseKeyProvider{Passphrase: "correct horse battery staple"}

	require.NoError(t, EncryptSessionPayload(data, provider))
	require.NotNil(t, data.EncryptedPayload)
	assert.Empty(t, data.EnvelopeXdr, "plaintext must be cleared after encryption")
	assert.Empty(t, data.SimRequestJSON, "plaintext must be cleared after encryption")

	require.NoError(t, DecryptSessionPayload(data, provider))
	assert.Nil(t, data.EncryptedPayload)
	assert.Equal(t, "AAAA-secret-envelope==", data.EnvelopeXdr)
	assert.Equal(t, "BBBB-secret-result==", data.ResultXdr)
	assert.Equal(t, `{"contract_args":["super-secret"]}`, data.SimRequestJSON)
}

func TestEncryptSessionPayload_KeysNeverWrittenToSession(t *testing.T) {
	data := encryptableData()
	provider := PassphraseKeyProvider{Passphrase: "super-secret-passphrase-xyz"}
	require.NoError(t, EncryptSessionPayload(data, provider))

	out, err := json.Marshal(data)
	require.NoError(t, err)
	assert.NotContains(t, string(out), "super-secret-passphrase-xyz",
		"the passphrase must never appear in the serialized session")
}

// ── No silent fallback ───────────────────────────────────────────────────

func TestEncryptSessionPayload_NoProvider_FailsClosed(t *testing.T) {
	data := encryptableData()
	err := EncryptSessionPayload(data, nil)
	require.Error(t, err)
	assert.Nil(t, data.EncryptedPayload)
	assert.NotEmpty(t, data.EnvelopeXdr, "data must be untouched when encryption fails")
}

func TestEncryptSessionPayload_EmptyPassphrase_FailsClosed(t *testing.T) {
	data := encryptableData()
	err := EncryptSessionPayload(data, PassphraseKeyProvider{Passphrase: ""})
	require.Error(t, err)
	assert.Nil(t, data.EncryptedPayload)
	assert.NotEmpty(t, data.EnvelopeXdr, "data must be untouched when encryption fails")
}

// ── Wrong key / tampering ────────────────────────────────────────────────

func TestDecryptSessionPayload_WrongPassphrase_ReturnsClearError(t *testing.T) {
	data := encryptableData()
	require.NoError(t, EncryptSessionPayload(data, PassphraseKeyProvider{Passphrase: "right-passphrase"}))

	err := DecryptSessionPayload(data, PassphraseKeyProvider{Passphrase: "wrong-passphrase"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decryption failed")
	assert.NotNil(t, data.EncryptedPayload, "envelope must remain intact after a failed decrypt")
	assert.Empty(t, data.EnvelopeXdr, "plaintext must not be reconstructed from a failed decrypt")
}

func TestDecryptSessionPayload_TamperedCiphertext_Detected(t *testing.T) {
	data := encryptableData()
	require.NoError(t, EncryptSessionPayload(data, PassphraseKeyProvider{Passphrase: "right-passphrase"}))

	// Flip a byte in the ciphertext to simulate corruption/tampering.
	tampered := []byte(data.EncryptedPayload.Ciphertext)
	require.NotEmpty(t, tampered)
	if tampered[0] == 'A' {
		tampered[0] = 'B'
	} else {
		tampered[0] = 'A'
	}
	data.EncryptedPayload.Ciphertext = string(tampered)

	err := DecryptSessionPayload(data, PassphraseKeyProvider{Passphrase: "right-passphrase"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decryption failed")
}

func TestDecryptSessionPayload_NoProvider_ReturnsClearError(t *testing.T) {
	data := encryptableData()
	require.NoError(t, EncryptSessionPayload(data, PassphraseKeyProvider{Passphrase: "right-passphrase"}))

	err := DecryptSessionPayload(data, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no key was provided")
}

func TestDecryptSessionPayload_WrongProviderName_ReturnsClearError(t *testing.T) {
	data := encryptableData()
	require.NoError(t, EncryptSessionPayload(data, PassphraseKeyProvider{Passphrase: "right-passphrase"}))

	err := DecryptSessionPayload(data, EnvKeyProvider{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "encrypted with key provider")
}

// ── Not encrypted is a no-op ─────────────────────────────────────────────

func TestDecryptSessionPayload_PlaintextSession_NoopWithoutProvider(t *testing.T) {
	data := encryptableData()
	require.NoError(t, DecryptSessionPayload(data, nil))
	assert.Equal(t, "AAAA-secret-envelope==", data.EnvelopeXdr, "plaintext session must be untouched")
}

// ── Store integration: backward compatibility, wrong key, fail-closed ────

func TestStore_Save_EncryptsWhenProviderConfigured(t *testing.T) {
	overrideTempHome(t)
	store, err := NewStore()
	require.NoError(t, err)
	defer store.Close()

	store.SetKeyProvider(PassphraseKeyProvider{Passphrase: "store-level-passphrase"})

	d := encryptableData()
	require.NoError(t, store.Save(t.Context(), d))

	// Load with the same provider decrypts transparently.
	loaded, err := store.Load(t.Context(), d.ID)
	require.NoError(t, err)
	assert.Equal(t, "AAAA-secret-envelope==", loaded.EnvelopeXdr)
}

func TestStore_Load_EncryptedSession_WrongPassphrase_Fails(t *testing.T) {
	overrideTempHome(t)
	store, err := NewStore()
	require.NoError(t, err)
	defer store.Close()

	store.SetKeyProvider(PassphraseKeyProvider{Passphrase: "correct-passphrase"})
	d := encryptableData()
	require.NoError(t, store.Save(t.Context(), d))

	store.SetKeyProvider(PassphraseKeyProvider{Passphrase: "incorrect-passphrase"})
	_, err = store.Load(t.Context(), d.ID)
	require.Error(t, err)
}

func TestStore_Load_EncryptedSession_NoProvider_Fails(t *testing.T) {
	overrideTempHome(t)
	store, err := NewStore()
	require.NoError(t, err)
	defer store.Close()

	store.SetKeyProvider(PassphraseKeyProvider{Passphrase: "correct-passphrase"})
	d := encryptableData()
	require.NoError(t, store.Save(t.Context(), d))

	store.SetKeyProvider(nil)
	_, err = store.Load(t.Context(), d.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "encrypted")
}

func TestStore_PlaintextSessionsRemainBackwardCompatible(t *testing.T) {
	overrideTempHome(t)
	store, err := NewStore()
	require.NoError(t, err)
	defer store.Close()

	// No key provider configured at all: sessions are saved and loaded
	// exactly as before encryption existed.
	d := encryptableData()
	require.NoError(t, store.Save(t.Context(), d))

	loaded, err := store.Load(t.Context(), d.ID)
	require.NoError(t, err)
	assert.Nil(t, loaded.EncryptedPayload)
	assert.Equal(t, "AAAA-secret-envelope==", loaded.EnvelopeXdr)
}

func TestStore_PlaintextSessionLoadsUnchangedWhenEncryptionLaterEnabled(t *testing.T) {
	overrideTempHome(t)
	store, err := NewStore()
	require.NoError(t, err)
	defer store.Close()

	// Saved without encryption...
	d := encryptableData()
	require.NoError(t, store.Save(t.Context(), d))

	// ...but loaded from a Store instance that now has encryption enabled
	// for *new* writes. An existing plaintext session must still load fine.
	store.SetKeyProvider(PassphraseKeyProvider{Passphrase: "some-passphrase"})
	loaded, err := store.Load(t.Context(), d.ID)
	require.NoError(t, err)
	assert.Equal(t, "AAAA-secret-envelope==", loaded.EnvelopeXdr)
}

func TestStore_Save_EncryptionRequestedButNoUsableKey_FailsClosedAndPersistsNothing(t *testing.T) {
	overrideTempHome(t)
	store, err := NewStore()
	require.NoError(t, err)
	defer store.Close()

	store.SetKeyProvider(PassphraseKeyProvider{Passphrase: ""}) // unusable: empty passphrase

	d := encryptableData()
	err = store.Save(t.Context(), d)
	require.Error(t, err)

	// Nothing should have been persisted under this ID.
	store.SetKeyProvider(nil)
	_, loadErr := store.Load(t.Context(), d.ID)
	require.Error(t, loadErr)
	assert.Contains(t, loadErr.Error(), "not found")
}

// ── Key providers ─────────────────────────────────────────────────────────

func TestResolveKeyProvider(t *testing.T) {
	p, err := ResolveKeyProvider("passphrase", "x")
	require.NoError(t, err)
	assert.Equal(t, PassphraseKeyProviderName, p.Name())

	p, err = ResolveKeyProvider("", "x")
	require.NoError(t, err)
	assert.Equal(t, PassphraseKeyProviderName, p.Name())

	p, err = ResolveKeyProvider("env", "")
	require.NoError(t, err)
	assert.Equal(t, EnvKeyProviderName, p.Name())

	_, err = ResolveKeyProvider("bogus", "")
	require.Error(t, err)
}

func TestEnvKeyProvider_ReadsFromEnvironment(t *testing.T) {
	key := strings.Repeat("ab", SessionKeySize) // 32 bytes hex-encoded
	t.Setenv(SessionKeyEnvVar, key)

	p := EnvKeyProvider{}
	got, err := p.Key("session-1", []byte("unused-salt"))
	require.NoError(t, err)
	assert.Len(t, got, SessionKeySize)
}

func TestEnvKeyProvider_MissingEnv_ReturnsError(t *testing.T) {
	t.Setenv(SessionKeyEnvVar, "")
	_, err := EnvKeyProvider{}.Key("session-1", []byte("salt"))
	require.Error(t, err)
}

// ── LogSafe never leaks ciphertext ───────────────────────────────────────

func TestEncryptedEnvelope_LogSafe_NeverLeaksCiphertext(t *testing.T) {
	data := encryptableData()
	require.NoError(t, EncryptSessionPayload(data, PassphraseKeyProvider{Passphrase: "secret-material-xyz"}))

	summary := data.EncryptedPayload.LogSafe()
	assert.NotContains(t, summary, data.EncryptedPayload.Ciphertext)
	assert.NotContains(t, summary, data.EncryptedPayload.Salt)
	assert.NotContains(t, summary, data.EncryptedPayload.Nonce)
	assert.Contains(t, summary, "provider=passphrase")
}
