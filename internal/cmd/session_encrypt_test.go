// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Tests for Issue #560: session encryption CLI wiring.

package cmd

import (
	"testing"

	"github.com/dotandev/glassbox/internal/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resetSessionEncryptFlags(t *testing.T) {
	t.Helper()
	prevEncrypt, prevProvider, prevPassphrase := sessionEncryptFlag, sessionKeyProviderFlag, sessionKeyPassphraseFlag
	sessionEncryptFlag = false
	sessionKeyProviderFlag = ""
	sessionKeyPassphraseFlag = ""
	t.Cleanup(func() {
		sessionEncryptFlag, sessionKeyProviderFlag, sessionKeyPassphraseFlag = prevEncrypt, prevProvider, prevPassphrase
	})
}

func TestResolveSessionKeyProviderFromFlags_NotRequested_ReturnsNil(t *testing.T) {
	resetSessionEncryptFlags(t)

	kp, err := resolveSessionKeyProviderFromFlags()
	require.NoError(t, err)
	assert.Nil(t, kp, "no encryption flags set should mean encryption is not configured")
}

func TestResolveSessionKeyProviderFromFlags_FlagRequestsPassphraseProvider(t *testing.T) {
	resetSessionEncryptFlags(t)
	sessionEncryptFlag = true
	sessionKeyPassphraseFlag = "flag-passphrase"

	kp, err := resolveSessionKeyProviderFromFlags()
	require.NoError(t, err)
	require.NotNil(t, kp)
	assert.Equal(t, session.PassphraseKeyProviderName, kp.Name())
}

func TestResolveSessionKeyProviderFromFlags_EnvFallback(t *testing.T) {
	resetSessionEncryptFlags(t)
	t.Setenv("GLASSBOX_SESSION_ENCRYPTION", "1")
	t.Setenv("GLASSBOX_SESSION_KEY_PASSPHRASE", "env-passphrase")

	kp, err := resolveSessionKeyProviderFromFlags()
	require.NoError(t, err)
	require.NotNil(t, kp)
	key, keyErr := kp.Key("session-1", []byte("0123456789abcdef"))
	require.NoError(t, keyErr)
	assert.Len(t, key, session.SessionKeySize)
}

func TestResolveSessionKeyProviderFromFlags_UnknownProvider_ReturnsError(t *testing.T) {
	resetSessionEncryptFlags(t)
	sessionEncryptFlag = true
	sessionKeyProviderFlag = "bogus-provider"

	_, err := resolveSessionKeyProviderFromFlags()
	require.Error(t, err)
}

func TestOpenSessionStore_EncryptionRoundTripThroughCLIFlags(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	resetSessionEncryptFlags(t)
	sessionEncryptFlag = true
	sessionKeyPassphraseFlag = "cli-flag-passphrase"

	store, err := openSessionStore()
	require.NoError(t, err)
	defer store.Close()

	data := &session.Data{
		ID:          session.GenerateID("tx1"),
		TxHash:      "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
		Network:     "testnet",
		Status:      "saved",
		EnvelopeXdr: "top-secret-envelope",
	}
	require.NoError(t, store.Save(t.Context(), data))

	loaded, err := store.Load(t.Context(), data.ID)
	require.NoError(t, err)
	assert.Equal(t, "top-secret-envelope", loaded.EnvelopeXdr)
}
