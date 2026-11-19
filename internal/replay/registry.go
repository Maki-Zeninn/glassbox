// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package replay provides the snapshot registry used by the debug command to
// persist and replay time-travel simulation sessions.
//
// A Registry bundles the transaction envelope, result metadata, and one or
// more per-timestamp ledger-state snapshots into a single JSON file. On load
// the registry verifies each snapshot's content hash so stale or corrupted
// entries are detected before replay begins.
package replay

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/dotandev/glassbox/internal/snapshot"
)

// SchemaVersion is the current registry file format version.
// Increment this when the on-disk layout changes in a breaking way.
const SchemaVersion = 1

// Entry holds a single simulation snapshot captured at a specific ledger
// timestamp.
type Entry struct {
	// Timestamp is the Unix epoch value used for the simulation.
	Timestamp int64 `json:"timestamp"`
	// Snapshot is the ledger state captured at this timestamp.
	Snapshot *snapshot.Snapshot `json:"snapshot"`
	// Checksum is the SHA-256 hex digest of the snapshot's canonical JSON.
	// It is computed on save and verified on load.
	// Deprecated: use ContentHash which is produced from canonical JSON and is
	// reproducible across Go versions and platforms.
	Checksum string `json:"checksum"`
	// ContentHash is the SHA-256 hex digest of the canonical JSON encoding of
	// the entry's (timestamp, snapshot) pair. Canonical JSON sorts object keys
	// and omits extra whitespace, making the hash reproducible across
	// platforms and Go versions. Empty on legacy entries saved before this
	// field was introduced; back-filled automatically by VerifyIntegrityFull.
	ContentHash string `json:"content_hash,omitempty"`
}

// Registry is the top-level container persisted to disk.
type Registry struct {
	// SchemaVersion identifies the file format.
	SchemaVersion int `json:"schema_version"`
	// GlassboxVersion is the CLI version that created this registry.
	GlassboxVersion string `json:"glassbox_version"`
	// CreatedAt is the wall-clock time the registry was first saved.
	CreatedAt time.Time `json:"created_at"`
	// TxHash is the Stellar transaction hash this registry belongs to.
	TxHash string `json:"tx_hash"`
	// Network is the Stellar network (testnet, mainnet, futurenet).
	// Deprecated: use NetworkSnapshot for the authoritative network identity.
	// This field is kept for backwards compatibility with registry files that
	// pre-date the NetworkSnapshot field.
	Network string `json:"network"`
	// NetworkSnapshot is the canonical, serialised network configuration
	// captured when this registry was created. It is used by
	// ValidateNetworkSnapshot to detect configuration drift between capture
	// and replay, and is nil for registry files created before this field was
	// introduced (legacy files pass validation unconditionally).
	NetworkSnapshot *NetworkSnapshot `json:"network_snapshot,omitempty"`
	// EnvelopeXdr is the base64-encoded transaction envelope XDR.
	EnvelopeXdr string `json:"envelope_xdr"`
	// ResultMetaXdr is the base64-encoded transaction result meta XDR.
	ResultMetaXdr string `json:"result_meta_xdr"`
	// CommandFingerprint is a hash of the CLI parameters used to produce this
	// registry. It is used to detect when a reload would be stale.
	CommandFingerprint string `json:"command_fingerprint,omitempty"`
	// Entries holds the per-timestamp snapshots.
	Entries []Entry `json:"entries"`
}

// New creates an empty Registry with the supplied metadata.
// snap is the canonical network configuration at capture time; it is stored
// in the NetworkSnapshot field for pre-replay compatibility checks. When snap
// is nil (e.g. for legacy callers that have not been updated) the legacy
// Network string is still populated from network for backwards compatibility.
func New(glassboxVersion, txHash, network, envelopeXdr, resultMetaXdr string) *Registry {
	return &Registry{
		SchemaVersion:   SchemaVersion,
		GlassboxVersion: glassboxVersion,
		CreatedAt:       time.Now().UTC(),
		TxHash:          txHash,
		Network:         network,
		EnvelopeXdr:     envelopeXdr,
		ResultMetaXdr:   resultMetaXdr,
		Entries:         make([]Entry, 0),
	}
}

// NewWithNetworkSnapshot creates a Registry and captures the full network
// configuration in NetworkSnapshot for pre-replay validation. Use this
// constructor for all new registries; New is kept for backwards compatibility.
func NewWithNetworkSnapshot(glassboxVersion, txHash, envelopeXdr, resultMetaXdr string, netSnap *NetworkSnapshot) *Registry {
	network := ""
	if netSnap != nil {
		network = netSnap.Name
	}
	r := New(glassboxVersion, txHash, network, envelopeXdr, resultMetaXdr)
	r.NetworkSnapshot = netSnap
	return r
}

// Add appends a snapshot captured at the given timestamp.
// Both the legacy Checksum (raw-JSON SHA-256) and the new ContentHash
// (canonical-JSON SHA-256) are computed immediately so every entry is
// verifiable offline without signing keys.
func (r *Registry) Add(timestamp int64, snap *snapshot.Snapshot) {
	checksum := computeSnapshotChecksum(snap)
	e := Entry{
		Timestamp: timestamp,
		Snapshot:  snap,
		Checksum:  checksum,
	}
	// Compute canonical content hash; ignore error — an empty hash will be
	// back-filled on the next VerifyIntegrityFull call.
	if h, err := EntryContentHash(&e); err == nil {
		e.ContentHash = h
	}
	r.Entries = append(r.Entries, e)
}

// SaveToFile serialises the registry to a JSON file at path.
// The file is written atomically via a temp-file rename so a partial write
// never leaves a corrupt registry on disk.
func (r *Registry) SaveToFile(path string) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal registry: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("failed to write registry temp file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("failed to rename registry file: %w", err)
	}
	return nil
}

// LoadFromFile reads a registry from path and verifies its schema version.
// Individual entry checksums are not verified here; call VerifyIntegrity for
// that.
func LoadFromFile(path string) (*Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read registry file %s: %w", path, err)
	}

	var reg Registry
	if err := json.Unmarshal(data, &reg); err != nil {
		return nil, fmt.Errorf("failed to parse registry file %s: %w", path, err)
	}

	if reg.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf(
			"registry schema version %d is not supported (expected %d); "+
				"please re-run the debug command to regenerate the snapshot registry",
			reg.SchemaVersion, SchemaVersion,
		)
	}

	// Validate metadata fields so a truncated or corrupted registry file is
	// detected immediately with a clear diagnostic.
	if reg.TxHash == "" {
		return nil, fmt.Errorf(
			"registry file %s is missing the transaction hash — the file may be truncated or corrupted; "+
				"re-run the debug command to regenerate the registry",
			path,
		)
	}
	if reg.Network == "" {
		return nil, fmt.Errorf(
			"registry file %s is missing the network — the file may be truncated or corrupted; "+
				"re-run the debug command to regenerate the registry",
			path,
		)
	}
	if reg.GlassboxVersion == "" {
		// Back-fill for registries saved before this field was recorded.
		reg.GlassboxVersion = "unknown"
	}

	// Back-fill NetworkSnapshot from the legacy Network string for registries
	// that pre-date the NetworkSnapshot field. The back-filled snapshot has only
	// a Name (no passphrase/protocol), so ValidateNetworkSnapshot will accept
	// any passphrase for these legacy files — the name field alone guards against
	// the most common cross-network mistake (replaying mainnet on testnet).
	if reg.NetworkSnapshot == nil && reg.Network != "" {
		reg.NetworkSnapshot = &NetworkSnapshot{Name: reg.Network}
	}

	return &reg, nil
}

// VerifyIntegrity checks each entry's stored checksum against the current
// snapshot content. It returns one error per mismatched entry so callers can
// log warnings without aborting the replay.
func (r *Registry) VerifyIntegrity() []error {
	var errs []error
	for i, entry := range r.Entries {
		computed := computeSnapshotChecksum(entry.Snapshot)
		if entry.Checksum == "" {
			// Back-fill: registry was saved before checksums were introduced.
			r.Entries[i].Checksum = computed
			continue
		}
		if entry.Checksum != computed {
			errs = append(errs, fmt.Errorf(
				"entry[%d] timestamp=%d: checksum mismatch (stored=%s computed=%s)",
				i, entry.Timestamp, entry.Checksum, computed,
			))
		}
	}
	return errs
}

// SetCommandFingerprint stores a hash of the CLI parameters so a future load
// can detect whether the registry was produced with different settings.
func (r *Registry) SetCommandFingerprint(params map[string]string) {
	r.CommandFingerprint = hashParams(params)
}

// MatchesCommandFingerprint returns true when the registry's stored fingerprint
// matches the hash of the supplied params. Returns true when no fingerprint is
// stored (backwards compatibility).
func (r *Registry) MatchesCommandFingerprint(params map[string]string) bool {
	if r.CommandFingerprint == "" {
		return true
	}
	return r.CommandFingerprint == hashParams(params)
}

// computeSnapshotChecksum returns the SHA-256 hex digest of the canonical JSON
// encoding of snap. A nil snapshot returns an empty string.
func computeSnapshotChecksum(snap *snapshot.Snapshot) string {
	if snap == nil {
		return ""
	}
	data, err := json.Marshal(snap)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// hashParams returns a deterministic SHA-256 hex digest of a string map.
// Keys are sorted before hashing so insertion order does not matter.
func hashParams(params map[string]string) string {
	if len(params) == 0 {
		return ""
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	for _, k := range keys {
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(params[k])
		sb.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}
