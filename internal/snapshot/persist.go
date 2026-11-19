// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// PersistSchemaVersion is the current on-disk format version for persisted
// replay snapshots. Increment when the layout changes in a breaking way.
// v2: adds metadata.ledger_format and metadata.migration_log fields.
const PersistSchemaVersion = 2

// ReplayMetadata captures the context in which a snapshot was produced.
// It is stored alongside the ledger state so a future load can verify that
// the snapshot still corresponds to the same transaction and environment.
type ReplayMetadata struct {
	// SchemaVersion is the persist format version.
	SchemaVersion int `json:"schema_version"`
	// GlassboxVersion is the CLI version that produced this snapshot.
	GlassboxVersion string `json:"glassbox_version"`
	// SavedAt is the wall-clock time the snapshot was written.
	SavedAt time.Time `json:"saved_at"`
	// TxHash is the Stellar transaction hash.
	TxHash string `json:"tx_hash"`
	// Network is the Stellar network (testnet, mainnet, futurenet).
	Network string `json:"network"`
	// EnvelopeXdr is the base64-encoded transaction envelope XDR.
	EnvelopeXdr string `json:"envelope_xdr"`
	// ResultMetaXdr is the base64-encoded transaction result meta XDR.
	ResultMetaXdr string `json:"result_meta_xdr"`
	// SourceHash is the SHA-256 of the WASM source bytes when the snapshot
	// was produced in local-replay mode. Empty for network transactions.
	SourceHash string `json:"source_hash,omitempty"`
	// ParamFingerprint is a hash of the CLI parameters (network, tx hash, etc.)
	// used to produce this snapshot. Used to detect stale snapshots.
	ParamFingerprint string `json:"param_fingerprint,omitempty"`
	// LedgerFormat describes the encoding of ledger entry values in the
	// snapshot. Introduced in schema v2. Always "base64-xdr" for snapshots
	// produced by current builds; present so future formats are
	// unambiguous. Legacy v1 snapshots are migrated to "base64-xdr" on load.
	LedgerFormat string `json:"ledger_format,omitempty"`
	// MigrationLog records each automatic migration applied to this snapshot
	// during a LoadPersisted call. It is append-only and bounded by the
	// number of migration steps in migrationTable.
	MigrationLog []MigrationLogEntry `json:"migration_log,omitempty"`
}

// PersistedSnapshot is the top-level structure written to disk.
// It combines the ledger state snapshot with replay metadata so the two
// are always stored and validated together.
type PersistedSnapshot struct {
	Metadata *ReplayMetadata `json:"metadata"`
	Snapshot *Snapshot       `json:"snapshot"`
}

// SavePersisted writes a PersistedSnapshot to path atomically.
// The file is written to a temp path first and then renamed so a partial
// write never leaves a corrupt file on disk.
//
// The metadata is validated before writing so that incomplete or inconsistent
// parameters are rejected early with a clear diagnostic.
func SavePersisted(path string, meta *ReplayMetadata, snap *Snapshot) error {
	if meta == nil {
		return fmt.Errorf("metadata must not be nil")
	}
	if meta.TxHash == "" {
		return fmt.Errorf(
			"transaction hash is required in replay metadata\n" +
				"  Fix: provide the transaction hash with --tx <hash>",
		)
	}
	if meta.Network == "" {
		return fmt.Errorf(
			"network is required in replay metadata\n" +
				"  Fix: provide the network with --network testnet (or mainnet, futurenet)",
		)
	}
	validNetworks := map[string]bool{"testnet": true, "mainnet": true, "futurenet": true}
	if !validNetworks[meta.Network] {
		return fmt.Errorf(
			"unsupported network %q in replay metadata — must be one of: testnet, mainnet, futurenet\n"+
				"  Fix: re-run with --network testnet (or mainnet, futurenet)",
			meta.Network,
		)
	}
	if snap == nil {
		snap = FromMap(nil)
	}

	meta.SchemaVersion = PersistSchemaVersion
	if meta.SavedAt.IsZero() {
		meta.SavedAt = time.Now().UTC()
	}
	// v2: always record the ledger encoding for new saves.
	if meta.LedgerFormat == "" {
		meta.LedgerFormat = "base64-xdr"
	}

	ps := &PersistedSnapshot{
		Metadata: meta,
		Snapshot: normalizedForSave(snap),
	}

	data, err := json.MarshalIndent(ps, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal persisted snapshot: %w", err)
	}

	// Ensure the parent directory exists.
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create snapshot directory: %w", err)
		}
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("failed to write snapshot temp file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("failed to rename snapshot file: %w", err)
	}
	return nil
}

// LoadPersisted reads a PersistedSnapshot from path and validates its schema
// version. It does not verify the snapshot fingerprint; call Validate for that.
func LoadPersisted(path string) (*PersistedSnapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read snapshot file %s: %w", path, err)
	}

	var ps PersistedSnapshot
	if err := json.Unmarshal(data, &ps); err != nil {
		return nil, fmt.Errorf("failed to parse snapshot file %s: %w", path, err)
	}

	if ps.Metadata == nil {
		return nil, fmt.Errorf("snapshot file %s is missing metadata", path)
	}
	// Delegate version checking to the canonical schema validation helper so
	// that the error messages and upgrade guidance stay in one place (schema.go).
	// For versions that NeedsUpgrade (v1 → v2), we migrate automatically below
	// instead of returning an error; for Unsupported versions we still error.
	versionResult := classifySchemaVersion(ps.Metadata.SchemaVersion)
	if versionResult.Unsupported {
		return nil, &SchemaError{Result: versionResult, Path: path}
	}
	// Auto-migrate supported older versions.
	if versionResult.NeedsUpgrade {
		if _, err := MigrateSnapshot(&ps); err != nil {
			return nil, fmt.Errorf("snapshot file %s: migration failed: %w", path, err)
		}
	}
	if ps.Metadata.TxHash == "" {
		return nil, fmt.Errorf(
			"snapshot file %s is missing the transaction hash in metadata — the file may be truncated or corrupted; "+
				"re-run the debug command to regenerate the snapshot",
			path,
		)
	}
	if ps.Metadata.Network == "" {
		return nil, fmt.Errorf(
			"snapshot file %s is missing the network in metadata — the file may be truncated or corrupted; "+
				"re-run the debug command to regenerate the snapshot",
			path,
		)
	}
	validNetworks := map[string]bool{"testnet": true, "mainnet": true, "futurenet": true}
	if !validNetworks[ps.Metadata.Network] {
		return nil, fmt.Errorf(
			"snapshot file %s contains unknown network %q in metadata — "+
				"must be one of: testnet, mainnet, futurenet; "+
				"re-run the debug command with --network to regenerate the snapshot",
			path, ps.Metadata.Network,
		)
	}
	if ps.Snapshot == nil {
		return nil, fmt.Errorf("snapshot file %s contains no ledger state", path)
	}

	return &ps, nil
}

// Validate checks that the persisted snapshot is internally consistent.
// It verifies the ledger-state fingerprint and optionally checks that the
// snapshot corresponds to the expected transaction and network.
//
// Returns nil when the snapshot is valid.
func (ps *PersistedSnapshot) Validate(expectedTxHash, expectedNetwork string) error {
	if ps.Snapshot == nil {
		return fmt.Errorf("snapshot is nil")
	}

	// Verify ledger-state fingerprint.
	computed := ComputeFingerprint(ps.Snapshot)
	if ps.Snapshot.Fingerprint != "" && ps.Snapshot.Fingerprint != computed {
		return fmt.Errorf(
			"snapshot fingerprint mismatch: stored=%s computed=%s (ledger state may be corrupted)",
			ps.Snapshot.Fingerprint, computed,
		)
	}

	// Verify transaction identity when requested.
	if expectedTxHash != "" && ps.Metadata.TxHash != expectedTxHash {
		return fmt.Errorf(
			"snapshot tx hash mismatch: stored=%s expected=%s",
			ps.Metadata.TxHash, expectedTxHash,
		)
	}
	if expectedNetwork != "" && ps.Metadata.Network != expectedNetwork {
		return fmt.Errorf(
			"snapshot network mismatch: stored=%s expected=%s",
			ps.Metadata.Network, expectedNetwork,
		)
	}

	return nil
}

// IsStale reports whether the snapshot should be considered stale given the
// current CLI parameters. A snapshot is stale when:
//   - Its param fingerprint does not match the current params, OR
//   - Its source hash does not match the current WASM source hash.
//
// An empty stored fingerprint is treated as always-fresh (backwards compat).
func (ps *PersistedSnapshot) IsStale(currentParams map[string]string, currentSourceHash string) bool {
	if ps.Metadata.ParamFingerprint != "" {
		if ps.Metadata.ParamFingerprint != hashStringMap(currentParams) {
			return true
		}
	}
	if ps.Metadata.SourceHash != "" && currentSourceHash != "" {
		if ps.Metadata.SourceHash != currentSourceHash {
			return true
		}
	}
	return false
}

// DefaultSnapshotPath returns the conventional path for a snapshot given a
// transaction hash and network. The file is placed under the Glassbox cache
// directory so it is automatically subject to LRU eviction.
//
// Format: ~/.glassbox/cache/snapshots/<network>/<txhash[:16]>.snap.json
func DefaultSnapshotPath(cacheDir, network, txHash string) string {
	if cacheDir == "" {
		home, _ := os.UserHomeDir()
		cacheDir = filepath.Join(home, ".glassbox", "cache")
	}
	// Use only the first 16 hex chars of the tx hash to keep paths short.
	shortHash := txHash
	if len(shortHash) > 16 {
		shortHash = shortHash[:16]
	}
	return filepath.Join(cacheDir, "snapshots", network, shortHash+".snap.json")
}

// HashWasmSource returns the SHA-256 hex digest of the given WASM bytes.
// Used to detect when a local WASM file has changed since a snapshot was saved.
func HashWasmSource(wasmBytes []byte) string {
	if len(wasmBytes) == 0 {
		return ""
	}
	sum := sha256.Sum256(wasmBytes)
	return hex.EncodeToString(sum[:])
}

// BuildParamFingerprint returns a deterministic hash of the CLI parameters
// that are relevant to snapshot validity (network, tx hash, protocol version,
// etc.). The map keys and values are sorted before hashing.
func BuildParamFingerprint(params map[string]string) string {
	return hashStringMap(params)
}

// hashStringMap returns a deterministic SHA-256 hex digest of a string map.
func hashStringMap(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	// Sort keys for determinism.
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	for _, k := range keys {
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(m[k])
		sb.WriteByte('\n')
	}

	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}
