// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// integrity.go — Canonical content hashing for snapshot registry entries.
//
// Design goals:
//   - Hashing is *integrity detection*, not authenticity. It detects accidental
//     truncation or on-disk modification. For authenticity use audit signing.
//   - Hashes are reproducible across platforms: canonical JSON (sorted keys,
//     no extra whitespace) over the Entry's snapshot + timestamp, not raw
//     json.Marshal output (which has non-deterministic key order for maps).
//   - Legacy entries with no ContentHash are silently back-filled on first
//     VerifyIntegrityFull call and reported as "legacy" in diagnostics.

package replay

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/dotandev/glassbox/internal/snapshot"
)

// IntegrityAlgorithm names the hash algorithm used for content hashes.
// Exposed so diagnostics and docs can reference it by name.
const IntegrityAlgorithm = "sha256-canonical-json"

// EntryContentHash computes the canonical SHA-256 content hash for a registry
// Entry.  The hash covers the entry's Timestamp and the full serialised
// Snapshot (ledger entries, linear memory, fingerprint) so any change to
// either field produces a different hash.
//
// Canonical encoding rules:
//   - Object keys are sorted lexicographically.
//   - No extra whitespace (no indentation, no trailing newlines).
//   - HTML-unsafe characters are NOT escaped (<, >, &) — we are hashing bytes,
//     not emitting HTML.
//
// A nil snapshot produces an empty string so callers can distinguish
// "entry has no snapshot" from a computed hash.
func EntryContentHash(e *Entry) (string, error) {
	if e == nil || e.Snapshot == nil {
		return "", nil
	}
	return canonicalSnapshotHash(e.Timestamp, e.Snapshot)
}

// canonicalSnapshotHash hashes a (timestamp, snapshot) pair in canonical form.
func canonicalSnapshotHash(timestamp int64, snap *snapshot.Snapshot) (string, error) {
	// Build a stable intermediate representation.
	type hashableEntry struct {
		Timestamp     int64                     `json:"timestamp"`
		LedgerEntries []snapshot.LedgerEntryTuple `json:"ledger_entries"`
		LinearMemory  string                    `json:"linear_memory,omitempty"`
		Fingerprint   string                    `json:"fingerprint,omitempty"`
	}

	// Sort ledger entries by key for determinism.
	entries := make([]snapshot.LedgerEntryTuple, len(snap.LedgerEntries))
	copy(entries, snap.LedgerEntries)
	sort.Slice(entries, func(i, j int) bool {
		if len(entries[i]) == 0 {
			return true
		}
		if len(entries[j]) == 0 {
			return false
		}
		return entries[i][0] < entries[j][0]
	})

	he := hashableEntry{
		Timestamp:     timestamp,
		LedgerEntries: entries,
		LinearMemory:  snap.LinearMemory,
		Fingerprint:   snap.Fingerprint,
	}

	data, err := marshalCanonicalJSON(he)
	if err != nil {
		return "", fmt.Errorf("canonical marshal failed: %w", err)
	}

	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// ── Canonical JSON ────────────────────────────────────────────────────────────

// marshalCanonicalJSON marshals v to deterministic JSON:
// sorted keys, no indentation, no HTML escaping.
func marshalCanonicalJSON(v interface{}) ([]byte, error) {
	// Round-trip through standard json.Marshal to get a generic interface{},
	// then re-encode with sorted keys.
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var intermediate interface{}
	if err := json.Unmarshal(raw, &intermediate); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := encodeCanonical(&buf, intermediate); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func encodeCanonical(buf *bytes.Buffer, v interface{}) error {
	switch val := v.(type) {
	case map[string]interface{}:
		buf.WriteByte('{')
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			kb, _ := json.Marshal(k)
			buf.Write(kb)
			buf.WriteByte(':')
			if err := encodeCanonical(buf, val[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	case []interface{}:
		buf.WriteByte('[')
		for i, item := range val {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := encodeCanonical(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	default:
		b, err := json.Marshal(val)
		if err != nil {
			return err
		}
		buf.Write(b)
	}
	return nil
}

// ── VerifyIntegrityFull ───────────────────────────────────────────────────────

// IntegrityStatus classifies the outcome of a single entry integrity check.
type IntegrityStatus string

const (
	// IntegrityOK means the stored ContentHash matches the computed hash.
	IntegrityOK IntegrityStatus = "ok"
	// IntegrityLegacy means no ContentHash was stored (pre-feature file).
	// The hash has been back-filled in memory; call SaveToFile to persist it.
	IntegrityLegacy IntegrityStatus = "legacy"
	// IntegrityTampered means the stored ContentHash does not match the
	// computed hash — the entry has been modified since it was saved.
	IntegrityTampered IntegrityStatus = "tampered"
	// IntegrityError means hash computation itself failed (e.g. marshal error).
	IntegrityError IntegrityStatus = "error"
)

// EntryIntegrityResult is the per-entry outcome of VerifyIntegrityFull.
type EntryIntegrityResult struct {
	// Index is the position of the entry in Registry.Entries.
	Index int
	// Timestamp is the entry's timestamp for log correlation.
	Timestamp int64
	// Status is the integrity classification.
	Status IntegrityStatus
	// StoredHash is the hash that was in the entry before verification.
	StoredHash string
	// ComputedHash is the hash we calculated during verification.
	ComputedHash string
	// Err is set when Status == IntegrityError.
	Err error
}

// String returns a one-line diagnostic suitable for log output.
func (r EntryIntegrityResult) String() string {
	switch r.Status {
	case IntegrityOK:
		return fmt.Sprintf("entry[%d] ts=%d OK hash=%s", r.Index, r.Timestamp, r.ComputedHash[:16])
	case IntegrityLegacy:
		return fmt.Sprintf("entry[%d] ts=%d LEGACY (back-filled hash=%s)", r.Index, r.Timestamp, r.ComputedHash[:16])
	case IntegrityTampered:
		return fmt.Sprintf("entry[%d] ts=%d TAMPERED stored=%s computed=%s",
			r.Index, r.Timestamp, r.StoredHash[:16], r.ComputedHash[:16])
	case IntegrityError:
		return fmt.Sprintf("entry[%d] ts=%d ERROR %v", r.Index, r.Timestamp, r.Err)
	default:
		return fmt.Sprintf("entry[%d] ts=%d UNKNOWN", r.Index, r.Timestamp)
	}
}

// RegistryIntegrityReport is the output of VerifyIntegrityFull.
type RegistryIntegrityReport struct {
	// Algorithm names the hash algorithm used.
	Algorithm string
	// Results holds one record per entry.
	Results []EntryIntegrityResult
	// LegacyCount is the number of entries that were back-filled.
	LegacyCount int
	// TamperedCount is the number of entries with hash mismatches.
	TamperedCount int
	// ErrorCount is the number of entries where hashing failed.
	ErrorCount int
	// OKCount is the number of entries that passed cleanly.
	OKCount int
	// BackfillApplied is true when at least one legacy entry was back-filled
	// and the caller should persist the registry to make it durable.
	BackfillApplied bool
}

// Passed returns true when no entries are tampered or errored.
// Legacy entries are acceptable (back-fillable from known content).
func (r *RegistryIntegrityReport) Passed() bool {
	return r.TamperedCount == 0 && r.ErrorCount == 0
}

// Errors returns one error per tampered entry, suitable for surfacing to users.
func (r *RegistryIntegrityReport) Errors() []error {
	var out []error
	for _, res := range r.Results {
		if res.Status == IntegrityTampered {
			out = append(out, fmt.Errorf(
				"entry[%d] timestamp=%d: content hash mismatch — stored=%s computed=%s (snapshot may have been modified)",
				res.Index, res.Timestamp, res.StoredHash, res.ComputedHash,
			))
		}
		if res.Status == IntegrityError {
			out = append(out, fmt.Errorf(
				"entry[%d] timestamp=%d: integrity check error: %w",
				res.Index, res.Timestamp, res.Err,
			))
		}
	}
	return out
}

// VerifyIntegrityFull performs a deep integrity check on every entry using
// EntryContentHash.  It replaces the lighter VerifyIntegrity (which uses raw
// json.Marshal and is therefore not canonical-JSON-stable across Go versions).
//
// Legacy behaviour (backward compatibility):
//   - An entry whose ContentHash is "" was saved before this feature existed.
//     Its hash is computed, stored back in memory, and reported as
//     IntegrityLegacy.  No file I/O is performed; call SaveToFile to persist.
//   - An entry whose ContentHash is non-empty is verified strictly.
//     A mismatch → IntegrityTampered (the entry is NOT back-filled).
func (r *Registry) VerifyIntegrityFull() *RegistryIntegrityReport {
	report := &RegistryIntegrityReport{Algorithm: IntegrityAlgorithm}

	for i := range r.Entries {
		e := &r.Entries[i]
		computed, err := EntryContentHash(e)
		if err != nil {
			report.Results = append(report.Results, EntryIntegrityResult{
				Index:     i,
				Timestamp: e.Timestamp,
				Status:    IntegrityError,
				Err:       err,
			})
			report.ErrorCount++
			continue
		}

		res := EntryIntegrityResult{
			Index:        i,
			Timestamp:    e.Timestamp,
			StoredHash:   e.ContentHash,
			ComputedHash: computed,
		}

		switch {
		case e.ContentHash == "":
			// Legacy entry — back-fill in memory.
			e.ContentHash = computed
			res.Status = IntegrityLegacy
			report.LegacyCount++
			report.BackfillApplied = true

		case e.ContentHash == computed:
			res.Status = IntegrityOK
			report.OKCount++

		default:
			res.Status = IntegrityTampered
			report.TamperedCount++
		}

		report.Results = append(report.Results, res)
	}

	return report
}


