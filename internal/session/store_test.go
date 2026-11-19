// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package session

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Store.Save validation ──────────────────────────────────────────────────────

func TestStore_Save_NilData_ReturnsError(t *testing.T) {
	store, err := NewStore()
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	err = store.Save(context.Background(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not be nil")
}

func TestStore_Save_EmptyID_ReturnsError(t *testing.T) {
	store, err := NewStore()
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	err = store.Save(context.Background(), &Data{
		TxHash:  "abc",
		Network: "testnet",
		Status:  "saved",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ID is required")
}

func TestStore_Save_EmptyTxHash_ReturnsError(t *testing.T) {
	store, err := NewStore()
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	err = store.Save(context.Background(), &Data{
		ID:      "session-1",
		Network: "testnet",
		Status:  "saved",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "transaction hash is required")
}

func TestStore_Save_EmptyNetwork_ReturnsError(t *testing.T) {
	store, err := NewStore()
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	err = store.Save(context.Background(), &Data{
		ID:      "session-1",
		TxHash:  "abc",
		Status:  "saved",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "network is required")
}

func TestStore_Save_InvalidNetwork_ReturnsError(t *testing.T) {
	store, err := NewStore()
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	err = store.Save(context.Background(), &Data{
		ID:      "session-1",
		TxHash:  "abc",
		Network: "invalidnet",
		Status:  "saved",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid")
	assert.Contains(t, err.Error(), "testnet")
	assert.Contains(t, err.Error(), "mainnet")
	assert.Contains(t, err.Error(), "futurenet")
}

// TestStore_Save_EmptyStatus_AutoPopulatesActive verifies that omitting Status
// causes Save to default it to "active" rather than returning an error.
func TestStore_Save_EmptyStatus_AutoPopulatesActive(t *testing.T) {
	store, err := NewStore()
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	err = store.Save(context.Background(), &Data{
		ID:      "session-1",
		TxHash:  "abc",
		Network: "testnet",
	})
	require.NoError(t, err, "empty status should be auto-populated to 'active', not rejected")
}

func TestStore_Save_InvalidStatus_ReturnsError(t *testing.T) {
	store, err := NewStore()
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	err = store.Save(context.Background(), &Data{
		ID:      "session-1",
		TxHash:  "abc",
		Network: "testnet",
		Status:  "borked",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid")
	assert.Contains(t, err.Error(), "active")
	assert.Contains(t, err.Error(), "saved")
	assert.Contains(t, err.Error(), "resumed")
	assert.Contains(t, err.Error(), "recovered")
	assert.Contains(t, err.Error(), "expired")
}

func TestStore_Save_ValidData_Succeeds(t *testing.T) {
	store, err := NewStore()
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	now := time.Now()
	data := &Data{
		ID:           "session-valid-" + now.Format("20060102150405"),
		TxHash:       "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Network:      "testnet",
		Status:       "saved",
		CreatedAt:    now,
		LastAccessAt: now,
		SchemaVersion: SchemaVersion,
	}

	err = store.Save(context.Background(), data)
	require.NoError(t, err)
	assert.NotZero(t, data.CreatedAt)
	assert.NotZero(t, data.LastAccessAt)
	assert.Equal(t, SchemaVersion, data.SchemaVersion)
}

func TestStore_Save_PinnedEndpointValidation(t *testing.T) {
	store, err := NewStore()
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	now := time.Now()
	data := &Data{
		ID:             "session-pinned-" + now.Format("20060102150405"),
		TxHash:         "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Network:        "testnet",
		Status:        "saved",
		CreatedAt:     now,
		LastAccessAt:  now,
		SchemaVersion: SchemaVersion,
		PinnedEndpoint: "http://127.0.0.1:9999",
	}

	err = store.Save(context.Background(), data)
	require.NoError(t, err)
}

func TestStore_SaveLoad_RoundTrip(t *testing.T) {
	overrideTempHome(t)
	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	original := &Data{
		ID:              "roundtrip-1",
		Name:            "payroll-bug",
		CreatedAt:       time.Now().Add(-time.Hour),
		LastAccessAt:    time.Now().Add(-time.Minute),
		Status:          "saved",
		Network:         "testnet",
		HorizonURL:      "https://horizon-testnet.stellar.org",
		TxHash:          "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
		EnvelopeXdr:     "envelope-xdr",
		ResultXdr:       "result-xdr",
		ResultMetaXdr:   "meta-xdr",
		PinnedEndpoint:  "https://rpc.testnet.example",
		SimRequestJSON:  `{"envelope_xdr":"abc"}`,
		SimResponseJSON: `{"status":"ok"}`,
		ErstVersion:     "test-version",
		SchemaVersion:   SchemaVersion,
	}

	ctx := context.Background()
	if err := store.Save(ctx, original); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load(ctx, original.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.Name != original.Name {
		t.Errorf("Name = %q, want %q", loaded.Name, original.Name)
	}
	if loaded.PinnedEndpoint != original.PinnedEndpoint {
		t.Errorf("PinnedEndpoint = %q, want %q", loaded.PinnedEndpoint, original.PinnedEndpoint)
	}
	if loaded.EnvFingerprint == "" {
		t.Error("EnvFingerprint should be populated on save")
	}
	if loaded.SchemaVersion != SchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", loaded.SchemaVersion, SchemaVersion)
	}
}

func TestStore_Load_UpgradesOlderSchemaVersion(t *testing.T) {
	if SchemaVersion <= MinSupportedSchemaVersion {
		t.Skip("no upgradable version below current")
	}

	overrideTempHome(t)
	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	d := makeValidSessionData(t, 0)
	d.ID = "upgrade-me"
	d.SchemaVersion = SchemaVersion - 1
	d.EnvFingerprint = ""

	if err := store.SavePreservingSchemaVersion(ctx, d); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load(ctx, d.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.SchemaVersion != SchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d after upgrade", loaded.SchemaVersion, SchemaVersion)
	}
	if loaded.EnvFingerprint == "" {
		t.Error("EnvFingerprint should be populated after upgrade")
	}
}

func TestStore_Load_UnsupportedSchemaVersion_ReturnsSchemaError(t *testing.T) {
	overrideTempHome(t)
	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	d := makeValidSessionData(t, 0)
	d.ID = "too-old"
	d.SchemaVersion = 0

	if err := store.SavePreservingSchemaVersion(ctx, d); err != nil {
		t.Fatalf("Save: %v", err)
	}

	_, err = store.Load(ctx, d.ID)
	if err == nil {
		t.Fatal("expected error loading unsupported schema version")
	}
	if !IsSchemaError(err) {
		t.Fatalf("expected *SchemaError, got: %T (%v)", err, err)
	}
	if !strings.Contains(err.Error(), "too old") {
		t.Errorf("error should mention too old schema, got: %v", err)
	}
}
