// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package rpc

// immutable_cache_test.go — tests for the content-addressed, network-scoped
// cache layer covering:
//   - cache hit: second identical request served from cache (no RPC needed)
//   - cache miss: unknown key returns (false, nil)
//   - corruption: tampered row is discarded and treated as a miss
//   - eviction: expired rows are not served
//   - network isolation: same key on different networks is independent
//   - typed helpers: SetTransaction/GetTransaction, SetLedgerHeader/GetLedgerHeader,
//     SetLedgerEntry/GetLedgerEntry
//   - diagnostics: StatsSnapshot hit/miss/corruption/eviction counters

import (
	"database/sql"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

// setupImmutableTestDB creates a fresh in-memory SQLite DB, resets global
// stats and registers cleanup. Call at the start of every sub-test that
// exercises the DB layer.
func setupImmutableTestDB(t *testing.T) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, InitCacheWithDB(db))

	// Reset global stats so tests are independent.
	atomic.StoreInt64(&globalStats.hits, 0)
	atomic.StoreInt64(&globalStats.misses, 0)
	atomic.StoreInt64(&globalStats.corruptions, 0)
	atomic.StoreInt64(&globalStats.evictions, 0)

	t.Cleanup(func() { _ = CloseCache() })
}

// ─── cache key helpers ────────────────────────────────────────────────────────

func TestTxCacheKey_Scoped(t *testing.T) {
	k1 := txCacheKey("mainnet", "abc123")
	k2 := txCacheKey("testnet", "abc123")
	assert.NotEqual(t, k1, k2, "same hash on different networks must produce different keys")
}

func TestLedgerHeaderCacheKey_Scoped(t *testing.T) {
	k1 := ledgerHeaderCacheKey("mainnet", 100)
	k2 := ledgerHeaderCacheKey("testnet", 100)
	assert.NotEqual(t, k1, k2)
}

func TestLedgerEntryCacheKey_Scoped(t *testing.T) {
	k1 := ledgerEntryCacheKey("mainnet", "xdr-key")
	k2 := ledgerEntryCacheKey("testnet", "xdr-key")
	assert.NotEqual(t, k1, k2)
}

// ─── transaction cache ───────────────────────────────────────────────────────

func TestSetGetTransaction_Hit(t *testing.T) {
	setupImmutableTestDB(t)

	resp := &TransactionResponse{
		EnvelopeXdr:   "envelope-xdr-data",
		ResultXdr:     "result-xdr-data",
		ResultMetaXdr: "meta-xdr-data",
	}

	require.NoError(t, SetTransaction("testnet", "txhash1", resp))

	got, found, err := GetTransaction("testnet", "txhash1")
	require.NoError(t, err)
	require.True(t, found)
	require.NotNil(t, got)
	assert.Equal(t, resp.EnvelopeXdr, got.EnvelopeXdr)
	assert.Equal(t, resp.ResultXdr, got.ResultXdr)
	assert.Equal(t, resp.ResultMetaXdr, got.ResultMetaXdr)
}

func TestSetGetTransaction_Miss(t *testing.T) {
	setupImmutableTestDB(t)

	got, found, err := GetTransaction("testnet", "nonexistent")
	require.NoError(t, err)
	assert.False(t, found)
	assert.Nil(t, got)
}

func TestSetGetTransaction_NetworkIsolation(t *testing.T) {
	setupImmutableTestDB(t)

	mainnetResp := &TransactionResponse{EnvelopeXdr: "mainnet-envelope"}
	testnetResp := &TransactionResponse{EnvelopeXdr: "testnet-envelope"}

	require.NoError(t, SetTransaction("mainnet", "txhash1", mainnetResp))
	require.NoError(t, SetTransaction("testnet", "txhash1", testnetResp))

	gotMain, ok, err := GetTransaction("mainnet", "txhash1")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "mainnet-envelope", gotMain.EnvelopeXdr)

	gotTest, ok, err := GetTransaction("testnet", "txhash1")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "testnet-envelope", gotTest.EnvelopeXdr)
}

func TestGetTransaction_CorruptedJSON(t *testing.T) {
	setupImmutableTestDB(t)

	// Write a valid checksum but invalid JSON payload directly.
	key := txCacheKey("testnet", "corrupt-tx")
	badJSON := "THIS IS NOT JSON"
	require.NoError(t, setEntry(key, badJSON, ImmutableTTL, "testnet", kindTransaction))

	got, found, err := GetTransaction("testnet", "corrupt-tx")
	require.NoError(t, err)
	assert.False(t, found, "corrupted JSON must be treated as a miss")
	assert.Nil(t, got)

	snap := StatsSnapshot()
	assert.GreaterOrEqual(t, snap.Corruptions, int64(1))
	assert.GreaterOrEqual(t, snap.Evictions, int64(1))
}

func TestGetTransaction_ChecksumMismatch(t *testing.T) {
	setupImmutableTestDB(t)

	resp := &TransactionResponse{EnvelopeXdr: "original"}
	require.NoError(t, SetTransaction("testnet", "ck-tx", resp))

	// Directly overwrite the value with different content but leave the old checksum intact.
	db, err := ensureDB()
	require.NoError(t, err)
	keyHash := getCacheKey(txCacheKey("testnet", "ck-tx"))
	_, err = db.Exec("UPDATE rpc_cache SET value = ? WHERE key_hash = ?", `{"EnvelopeXdr":"tampered"}`, keyHash)
	require.NoError(t, err)

	got, found, err := GetTransaction("testnet", "ck-tx")
	require.NoError(t, err)
	assert.False(t, found, "checksum mismatch must be treated as a miss")
	assert.Nil(t, got)

	snap := StatsSnapshot()
	assert.GreaterOrEqual(t, snap.Corruptions, int64(1))
	assert.GreaterOrEqual(t, snap.Evictions, int64(1))
}

func TestSetTransaction_NilRespIsNoOp(t *testing.T) {
	setupImmutableTestDB(t)
	require.NoError(t, SetTransaction("testnet", "nil-tx", nil))
	_, found, err := GetTransaction("testnet", "nil-tx")
	require.NoError(t, err)
	assert.False(t, found)
}

// ─── ledger-header cache ─────────────────────────────────────────────────────

func TestSetGetLedgerHeader_Hit(t *testing.T) {
	setupImmutableTestDB(t)

	hdr := &LedgerHeaderResponse{
		Sequence:        42,
		Hash:            "ledger-hash-42",
		ProtocolVersion: 20,
	}

	require.NoError(t, SetLedgerHeader("mainnet", 42, hdr))

	got, found, err := GetLedgerHeader("mainnet", 42)
	require.NoError(t, err)
	require.True(t, found)
	require.NotNil(t, got)
	assert.Equal(t, uint32(42), got.Sequence)
	assert.Equal(t, "ledger-hash-42", got.Hash)
}

func TestSetGetLedgerHeader_Miss(t *testing.T) {
	setupImmutableTestDB(t)

	got, found, err := GetLedgerHeader("mainnet", 9999)
	require.NoError(t, err)
	assert.False(t, found)
	assert.Nil(t, got)
}

func TestSetGetLedgerHeader_NetworkIsolation(t *testing.T) {
	setupImmutableTestDB(t)

	mainHdr := &LedgerHeaderResponse{Sequence: 10, Hash: "main-hash"}
	testHdr := &LedgerHeaderResponse{Sequence: 10, Hash: "test-hash"}

	require.NoError(t, SetLedgerHeader("mainnet", 10, mainHdr))
	require.NoError(t, SetLedgerHeader("testnet", 10, testHdr))

	gotMain, ok, err := GetLedgerHeader("mainnet", 10)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "main-hash", gotMain.Hash)

	gotTest, ok, err := GetLedgerHeader("testnet", 10)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "test-hash", gotTest.Hash)
}

func TestGetLedgerHeader_CorruptedJSON(t *testing.T) {
	setupImmutableTestDB(t)

	key := ledgerHeaderCacheKey("mainnet", 77)
	require.NoError(t, setEntry(key, "{{not json}}", ImmutableTTL, "mainnet", kindLedgerHeader))

	got, found, err := GetLedgerHeader("mainnet", 77)
	require.NoError(t, err)
	assert.False(t, found)
	assert.Nil(t, got)
}

func TestSetLedgerHeader_NilRespIsNoOp(t *testing.T) {
	setupImmutableTestDB(t)
	require.NoError(t, SetLedgerHeader("mainnet", 1, nil))
	_, found, _ := GetLedgerHeader("mainnet", 1)
	assert.False(t, found)
}

// ─── ledger-entry cache ──────────────────────────────────────────────────────

func TestSetGetLedgerEntry_Hit(t *testing.T) {
	setupImmutableTestDB(t)

	require.NoError(t, SetLedgerEntry("testnet", "xdr-key-abc", "xdr-value-abc"))

	val, found, err := GetLedgerEntry("testnet", "xdr-key-abc")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "xdr-value-abc", val)
}

func TestSetGetLedgerEntry_Miss(t *testing.T) {
	setupImmutableTestDB(t)

	_, found, err := GetLedgerEntry("testnet", "nonexistent-key")
	require.NoError(t, err)
	assert.False(t, found)
}

func TestSetGetLedgerEntry_NetworkIsolation(t *testing.T) {
	setupImmutableTestDB(t)

	require.NoError(t, SetLedgerEntry("mainnet", "same-key", "mainnet-value"))
	require.NoError(t, SetLedgerEntry("testnet", "same-key", "testnet-value"))

	mainVal, ok, err := GetLedgerEntry("mainnet", "same-key")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "mainnet-value", mainVal)

	testVal, ok, err := GetLedgerEntry("testnet", "same-key")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "testnet-value", testVal)
}

func TestSetGetLedgerEntry_ChecksumMismatch(t *testing.T) {
	setupImmutableTestDB(t)

	require.NoError(t, SetLedgerEntry("testnet", "ck-key", "original-value"))

	db, err := ensureDB()
	require.NoError(t, err)
	keyHash := getCacheKey(ledgerEntryCacheKey("testnet", "ck-key"))
	_, err = db.Exec("UPDATE rpc_cache SET value = 'tampered-value' WHERE key_hash = ?", keyHash)
	require.NoError(t, err)

	val, found, err := GetLedgerEntry("testnet", "ck-key")
	require.NoError(t, err)
	assert.False(t, found)
	assert.Empty(t, val)

	snap := StatsSnapshot()
	assert.GreaterOrEqual(t, snap.Corruptions, int64(1))
	assert.GreaterOrEqual(t, snap.Evictions, int64(1))
}

// ─── TTL / eviction ──────────────────────────────────────────────────────────

func TestTransaction_Expiration(t *testing.T) {
	setupImmutableTestDB(t)

	resp := &TransactionResponse{EnvelopeXdr: "expiring"}
	payload, err := json.Marshal(resp)
	require.NoError(t, err)

	// Write with a very short TTL directly via setEntry.
	require.NoError(t, setEntry(
		txCacheKey("testnet", "expiring-tx"),
		string(payload),
		50*time.Millisecond,
		"testnet",
		kindTransaction,
	))

	// Should be available immediately.
	_, found, err := GetTransaction("testnet", "expiring-tx")
	require.NoError(t, err)
	assert.True(t, found)

	time.Sleep(100 * time.Millisecond)

	// Should be gone after expiry.
	_, found, err = GetTransaction("testnet", "expiring-tx")
	require.NoError(t, err)
	assert.False(t, found)
}

// ─── diagnostics counters ────────────────────────────────────────────────────

func TestStatsSnapshot_HitAndMiss(t *testing.T) {
	setupImmutableTestDB(t)

	require.NoError(t, SetLedgerEntry("testnet", "stats-key", "stats-value"))

	// One hit.
	_, _, _ = GetLedgerEntry("testnet", "stats-key")
	// One miss.
	_, _, _ = GetLedgerEntry("testnet", "not-there")

	snap := StatsSnapshot()
	assert.Equal(t, int64(1), snap.Hits)
	assert.Equal(t, int64(1), snap.Misses)
}
