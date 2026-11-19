// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package rpc

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dotandev/glassbox/internal/errors"
	"github.com/dotandev/glassbox/internal/logger"
	"github.com/stellar/go-stellar-sdk/xdr"
	_ "modernc.org/sqlite"
)

// HashLedgerKey generates a deterministic SHA-256 hash of a Stellar LedgerKey.
// This is used by verification scripts and potentially conflicting with internal hashing if different.
func HashLedgerKey(key xdr.LedgerKey) (string, error) {
	xdrBytes, err := key.MarshalBinary()
	if err != nil {
		return "", errors.WrapMarshalFailed(err)
	}
	hash := sha256.Sum256(xdrBytes)
	return hex.EncodeToString(hash[:]), nil
}

const (
	CacheDBName     = "cache.db"
	CacheDirName    = ".Glassbox"
	FilePerm        = 0600
	DirPerm         = 0700
	DefaultCacheTTL = 24 * time.Hour

	// Immutable-data TTL – transactions and ledger headers are final once
	// they appear on-chain; keep them for 30 days before eviction.
	ImmutableTTL = 30 * 24 * time.Hour

	// cache entry kind constants used to scope keys and schema entries.
	kindTransaction   = "tx"
	kindLedgerHeader  = "ledger_header"
	kindLedgerEntry   = "ledger_entry"
)

// CachedEntry represents a single cached value.
type CachedEntry struct {
	Key       string        `json:"key"`
	Value     string        `json:"value"`
	CreatedAt time.Time     `json:"created_at"`
	ExpiresAt time.Time     `json:"expires_at"`
	TTL       time.Duration `json:"ttl"`
}

// CacheStats holds observable hit/miss counters for the current process.
// Values are updated atomically; read with LoadHits / LoadMisses.
type CacheStats struct {
	hits        int64
	misses      int64
	corruptions int64
	evictions   int64
}

// Snapshot returns a point-in-time copy of the counters.
type CacheStatsSnapshot struct {
	Hits        int64
	Misses      int64
	Corruptions int64
	Evictions   int64
}

var globalStats CacheStats

// StatsSnapshot returns a snapshot of the global cache counters.
func StatsSnapshot() CacheStatsSnapshot {
	return CacheStatsSnapshot{
		Hits:        atomic.LoadInt64(&globalStats.hits),
		Misses:      atomic.LoadInt64(&globalStats.misses),
		Corruptions: atomic.LoadInt64(&globalStats.corruptions),
		Evictions:   atomic.LoadInt64(&globalStats.evictions),
	}
}

var (
	cacheDB *sql.DB
	cacheMu sync.Mutex
)

// cacheSchema creates the rpc_cache table and indexes.
// The checksum column holds a SHA-256 hex digest of value; a mismatch on
// read causes the row to be treated as corrupted and discarded.
const cacheSchema = `
CREATE TABLE IF NOT EXISTS rpc_cache (
	key_hash   TEXT PRIMARY KEY,
	cache_key  TEXT NOT NULL,
	value      TEXT NOT NULL,
	network    TEXT NOT NULL DEFAULT '',
	kind       TEXT NOT NULL DEFAULT '',
	checksum   TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	expires_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_rpc_cache_expires ON rpc_cache(expires_at);
CREATE INDEX IF NOT EXISTS idx_rpc_cache_network  ON rpc_cache(network);
CREATE INDEX IF NOT EXISTS idx_rpc_cache_kind     ON rpc_cache(kind);
`

// GetCachePath returns the path to the cache directory, creating it if necessary.
func GetCachePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.WrapValidationError(fmt.Sprintf("failed to get user home directory: %v", err))
	}

	path := filepath.Join(home, CacheDirName)
	if err := os.MkdirAll(path, DirPerm); err != nil {
		return "", errors.WrapValidationError(fmt.Sprintf("failed to create cache directory: %v", err))
	}

	return path, nil
}

// getCacheDBPath returns the full path to cache.db inside ~/.glassbox/
func getCacheDBPath() (string, error) {
	dir, err := GetCachePath()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, CacheDBName), nil
}

// ensureDB lazily opens the SQLite database and creates the schema.
func ensureDB() (*sql.DB, error) {
	cacheMu.Lock()
	defer cacheMu.Unlock()

	if cacheDB != nil {
		return cacheDB, nil
	}

	dbPath, err := getCacheDBPath()
	if err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open cache database: %w", err)
	}

	// WAL mode for better concurrent read performance
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to set WAL mode: %w", err)
	}

	if _, err := db.Exec(cacheSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to initialize cache schema: %w", err)
	}

	cacheDB = db
	return cacheDB, nil
}

// InitCacheWithDB injects an already-open *sql.DB (e.g. an in-memory database
// for testing). The caller is responsible for closing it.
func InitCacheWithDB(db *sql.DB) error {
	cacheMu.Lock()
	defer cacheMu.Unlock()

	if _, err := db.Exec(cacheSchema); err != nil {
		return fmt.Errorf("failed to initialize cache schema: %w", err)
	}
	cacheDB = db
	return nil
}

// CloseCache closes the underlying SQLite connection, if open.
func CloseCache() error {
	cacheMu.Lock()
	defer cacheMu.Unlock()

	if cacheDB != nil {
		err := cacheDB.Close()
		cacheDB = nil
		return err
	}
	return nil
}

// getCacheKey returns the SHA-256 hex digest used as the primary key.
func getCacheKey(key string) string {
	hash := sha256.Sum256([]byte(key))
	return hex.EncodeToString(hash[:])
}

// Get retrieves a value from the SQLite cache.
// If the stored checksum does not match the value the row is evicted and
// (false, nil) is returned so the caller falls through to a live fetch.
// Returns (value, found, error).
func Get(key string) (string, bool, error) {
	db, err := ensureDB()
	if err != nil {
		return "", false, err
	}

	keyHash := getCacheKey(key)
	now := time.Now().UnixNano()

	var value, checksum string
	err = db.QueryRow(
		"SELECT value, checksum FROM rpc_cache WHERE key_hash = ? AND expires_at > ?",
		keyHash, now,
	).Scan(&value, &checksum)

	if err == sql.ErrNoRows {
		atomic.AddInt64(&globalStats.misses, 1)
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("cache read failed: %w", err)
	}

	// Integrity check: only verify when a checksum was stored.
	if checksum != "" && checksum != valueChecksum(value) {
		atomic.AddInt64(&globalStats.corruptions, 1)
		logger.Logger.Warn("Cache corruption detected, evicting entry", "key_hash", keyHash)
		_, _ = db.Exec("DELETE FROM rpc_cache WHERE key_hash = ?", keyHash)
		atomic.AddInt64(&globalStats.evictions, 1)
		return "", false, nil
	}

	atomic.AddInt64(&globalStats.hits, 1)
	return value, true, nil
}

// SetWithTTL stores a value in the cache with a specific TTL.
func SetWithTTL(key string, value string, ttl time.Duration) error {
	return SetWithTTLAndNetwork(key, value, ttl, "")
}

// SetWithTTLAndNetwork stores a value in the cache with a specific TTL and
// network tag. A SHA-256 checksum of value is stored alongside the row so
// that corrupted entries can be detected and discarded on the next read.
func SetWithTTLAndNetwork(key, value string, ttl time.Duration, network string) error {
	return setEntry(key, value, ttl, network, "")
}

// setEntry is the single write path shared by all typed helpers.
func setEntry(key, value string, ttl time.Duration, network, kind string) error {
	if ttl <= 0 {
		ttl = DefaultCacheTTL
	}

	db, err := ensureDB()
	if err != nil {
		return err
	}

	keyHash := getCacheKey(key)
	checksum := valueChecksum(value)
	now := time.Now()

	_, err = db.Exec(
		`INSERT INTO rpc_cache (key_hash, cache_key, value, network, kind, checksum, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(key_hash) DO UPDATE SET
		   value      = excluded.value,
		   network    = excluded.network,
		   kind       = excluded.kind,
		   checksum   = excluded.checksum,
		   created_at = excluded.created_at,
		   expires_at = excluded.expires_at`,
		keyHash, key, value, network, kind, checksum, now.UnixNano(), now.Add(ttl).UnixNano(),
	)
	if err != nil {
		return fmt.Errorf("cache write failed: %w", err)
	}
	return nil
}

// valueChecksum returns the SHA-256 hex digest of v.
func valueChecksum(v string) string {
	h := sha256.Sum256([]byte(v))
	return hex.EncodeToString(h[:])
}

// Set stores a value using the default TTL.
func Set(key string, value string) error {
	return SetWithTTL(key, value, DefaultCacheTTL)
}

// Invalidate removes a specific key from the cache.
func Invalidate(key string) error {
	db, err := ensureDB()
	if err != nil {
		return err
	}

	keyHash := getCacheKey(key)
	_, err = db.Exec("DELETE FROM rpc_cache WHERE key_hash = ?", keyHash)
	if err != nil {
		return fmt.Errorf("cache invalidate failed: %w", err)
	}
	return nil
}

// Cleanup removes expired cache entries older than maxAge.
// Returns the number of rows removed.
func Cleanup(maxAge time.Duration) (int, error) {
	db, err := ensureDB()
	if err != nil {
		return 0, err
	}

	cutoff := time.Now().Add(-maxAge).UnixNano()

	result, err := db.Exec("DELETE FROM rpc_cache WHERE expires_at < ?", cutoff)
	if err != nil {
		return 0, fmt.Errorf("cache cleanup failed: %w", err)
	}

	removed, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}

	if removed > 0 {
		logger.Logger.Info("Cache cleanup completed", "entries_removed", removed)
	}

	return int(removed), nil
}

// CleanFilter holds the criteria for selective cache pruning.
type CleanFilter struct {
	// OlderThan removes entries whose created_at is older than this duration.
	// Zero means no age filter.
	OlderThan time.Duration
	// Network removes only entries matching this network tag.
	// Empty string means no network filter.
	Network string
	// All removes every entry regardless of other filters.
	All bool
}

// CleanByFilter removes rpc_cache entries that match the given filter.
// Returns the number of rows deleted.
func CleanByFilter(f CleanFilter) (int, error) {
	if !f.All && f.OlderThan == 0 && f.Network == "" {
		return 0, fmt.Errorf("no filter specified: use --all, --older-than, or --network")
	}

	db, err := ensureDB()
	if err != nil {
		return 0, err
	}

	var (
		query string
		args  []any
	)

	switch {
	case f.All:
		query = "DELETE FROM rpc_cache"

	case f.OlderThan > 0 && f.Network != "":
		cutoff := time.Now().Add(-f.OlderThan).UnixNano()
		query = "DELETE FROM rpc_cache WHERE network = ? AND created_at < ?"
		args = []any{f.Network, cutoff}

	case f.OlderThan > 0:
		cutoff := time.Now().Add(-f.OlderThan).UnixNano()
		query = "DELETE FROM rpc_cache WHERE created_at < ?"
		args = []any{cutoff}

	case f.Network != "":
		query = "DELETE FROM rpc_cache WHERE network = ?"
		args = []any{f.Network}
	}

	result, err := db.Exec(query, args...)
	if err != nil {
		return 0, fmt.Errorf("cache clean failed: %w", err)
	}

	removed, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}

	if removed > 0 {
		logger.Logger.Info("RPC cache clean completed", "entries_removed", removed)
	}

	return int(removed), nil
}

// Flush finalizes pending cache writes.
// Current cache writes are synchronous file writes, so this is a no-op.
func Flush(ctx context.Context) error {
	_ = ctx
	return nil
}

// CountEntries returns the total number of entries currently stored in the
// RPC response cache database.
func CountEntries() (int, error) {
	db, err := ensureDB()
	if err != nil {
		return 0, fmt.Errorf("failed to open cache DB: %w", err)
	}
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM rpc_cache").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count cache entries: %w", err)
	}
	return count, nil
}

// ─── Typed immutable-data cache helpers ─────────────────────────────────────
//
// Transactions and ledger headers are immutable once finalised on-chain.
// The helpers below scope every cache key by network so that the same tx
// hash on testnet and mainnet are stored as independent rows, and serialise
// the typed response as JSON so the value can be reconstructed without any
// additional RPC call.

// txCacheKey returns the network-scoped cache key for a transaction hash.
func txCacheKey(network, hash string) string {
	return kindTransaction + ":" + network + ":" + hash
}

// ledgerHeaderCacheKey returns the network-scoped cache key for a ledger header.
func ledgerHeaderCacheKey(network string, seq uint32) string {
	return fmt.Sprintf("%s:%s:%d", kindLedgerHeader, network, seq)
}

// ledgerEntryCacheKey returns the network-scoped cache key for a ledger entry XDR key.
func ledgerEntryCacheKey(network, xdrKey string) string {
	return kindLedgerEntry + ":" + network + ":" + xdrKey
}

// SetTransaction serialises resp as JSON and stores it under a network-scoped,
// content-addressed key. Transactions are immutable so ImmutableTTL is used.
func SetTransaction(network, hash string, resp *TransactionResponse) error {
	if resp == nil {
		return nil
	}
	payload, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("cache marshal tx: %w", err)
	}
	return setEntry(txCacheKey(network, hash), string(payload), ImmutableTTL, network, kindTransaction)
}

// GetTransaction retrieves a previously cached TransactionResponse.
// Returns (nil, false, nil) on a cache miss or evicted-corruption entry.
func GetTransaction(network, hash string) (*TransactionResponse, bool, error) {
	raw, found, err := Get(txCacheKey(network, hash))
	if !found || err != nil {
		return nil, false, err
	}
	var resp TransactionResponse
	if jsonErr := json.Unmarshal([]byte(raw), &resp); jsonErr != nil {
		// JSON decode failure treated as corruption – evict.
		atomic.AddInt64(&globalStats.corruptions, 1)
		_ = Invalidate(txCacheKey(network, hash))
		atomic.AddInt64(&globalStats.evictions, 1)
		return nil, false, nil
	}
	return &resp, true, nil
}

// SetLedgerHeader serialises resp as JSON and stores it under a network-scoped key.
func SetLedgerHeader(network string, seq uint32, resp *LedgerHeaderResponse) error {
	if resp == nil {
		return nil
	}
	payload, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("cache marshal ledger header: %w", err)
	}
	return setEntry(ledgerHeaderCacheKey(network, seq), string(payload), ImmutableTTL, network, kindLedgerHeader)
}

// GetLedgerHeader retrieves a previously cached LedgerHeaderResponse.
// Returns (nil, false, nil) on a cache miss.
func GetLedgerHeader(network string, seq uint32) (*LedgerHeaderResponse, bool, error) {
	raw, found, err := Get(ledgerHeaderCacheKey(network, seq))
	if !found || err != nil {
		return nil, false, err
	}
	var resp LedgerHeaderResponse
	if jsonErr := json.Unmarshal([]byte(raw), &resp); jsonErr != nil {
		atomic.AddInt64(&globalStats.corruptions, 1)
		_ = Invalidate(ledgerHeaderCacheKey(network, seq))
		atomic.AddInt64(&globalStats.evictions, 1)
		return nil, false, nil
	}
	return &resp, true, nil
}

// SetLedgerEntry stores a single base64-XDR ledger entry value, scoped by network.
func SetLedgerEntry(network, xdrKey, xdrValue string) error {
	return setEntry(ledgerEntryCacheKey(network, xdrKey), xdrValue, ImmutableTTL, network, kindLedgerEntry)
}

// GetLedgerEntry retrieves a single cached ledger entry XDR value scoped by network.
// Returns ("", false, nil) on cache miss.
func GetLedgerEntry(network, xdrKey string) (string, bool, error) {
	return Get(ledgerEntryCacheKey(network, xdrKey))
}
