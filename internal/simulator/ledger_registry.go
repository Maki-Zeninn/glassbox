// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package simulator

import (
	"encoding/base64"
	"fmt"
	"sort"
	"sync"
)

// EntryKind classifies a ledger entry by its semantic role in a Soroban
// contract execution. The kind is stored alongside the XDR payload so tests
// can assert entry-type invariants without re-decoding the XDR.
type EntryKind uint8

const (
	EntryKindAccount       EntryKind = iota // Classic account balance and sequence
	EntryKindContract                        // ContractData with Persistent durability
	EntryKindCode                            // ContractCode (WASM bytecode)
	EntryKindTTL                             // Time-to-live record for a contract entry
	_                                        // reserved iota slot
	EntryKindTemporary                       // ContractData Temporary durability
	EntryKindConfigSetting                   // Network-wide configurable parameter

	EntryKindPersistent = EntryKindContract // Alias for ContractData Persistent (same wire type)
)

// String returns a human-readable name for the EntryKind.
func (k EntryKind) String() string {
	switch k {
	case EntryKindAccount:
		return "account"
	case EntryKindContract:
		return "contract_data_persistent"
	case EntryKindCode:
		return "contract_code"
	case EntryKindTTL:
		return "ttl"
	case EntryKindTemporary:
		return "contract_data_temporary"
	case EntryKindConfigSetting:
		return "config_setting"
	default:
		return "unknown"
	}
}

// Mutation records the before- and after-XDR images of a single written entry.
// Before is empty when the entry was absent before the write.
type Mutation struct {
	// Before is the base64-encoded XDR of the entry before the write.
	// Empty string means the entry was absent before the write (a create
	// operation). This is unambiguous because valid XDR-encoded LedgerEntries
	// always have a non-zero serialised length.
	Before string
	After  string // base64 XDR after the write
	Kind   EntryKind
}

// registryEntry is the internal storage record.
type registryEntry struct {
	entryXDR string
	kind     EntryKind
}

// LedgerEntryRegistry is a deterministic, thread-safe in-memory ledger store
// for simulator fixture tests. It:
//   - distinguishes a missing entry (present=false) from a zero-valued one
//   - tracks every key read during execution (read set)
//   - records the before/after XDR of every mutation (write set)
//   - returns a reproducibly sorted footprint for fixture comparison
type LedgerEntryRegistry struct {
	mu      sync.Mutex
	entries map[string]*registryEntry // keyed by base64 XDR LedgerKey
	reads   map[string]struct{}
	writes  map[string]Mutation
}

// NewLedgerEntryRegistry returns an empty LedgerEntryRegistry ready for use
// in a fixture transaction.
func NewLedgerEntryRegistry() *LedgerEntryRegistry {
	return &LedgerEntryRegistry{
		entries: make(map[string]*registryEntry),
		reads:   make(map[string]struct{}),
		writes:  make(map[string]Mutation),
	}
}

// Put stores entryXDR under keyXDR with the given EntryKind. Both arguments
// must be non-empty, valid base64-encoded XDR strings (as produced by
// rpc.EncodeLedgerKey and rpc.EncodeLedgerEntry). An existing entry is
// overwritten without creating a mutation record; use RecordWrite to track
// mutations during a simulated execution.
func (r *LedgerEntryRegistry) Put(keyXDR, entryXDR string, kind EntryKind) error {
	if keyXDR == "" {
		return fmt.Errorf("ledger registry: keyXDR must not be empty")
	}
	if entryXDR == "" {
		return fmt.Errorf("ledger registry: entryXDR must not be empty for key %q — use Delete to remove an entry", keyXDR)
	}
	if _, err := base64.StdEncoding.DecodeString(entryXDR); err != nil {
		return fmt.Errorf("ledger registry: entryXDR for key %q is not valid base64: %w", keyXDR, err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[keyXDR] = &registryEntry{entryXDR: entryXDR, kind: kind}
	return nil
}

// Get returns the XDR-encoded entry for keyXDR. present is false when no
// entry is registered — explicitly distinct from a zero-valued entry. Calling
// Get on a present key records keyXDR in the read set.
func (r *LedgerEntryRegistry) Get(keyXDR string) (entryXDR string, present bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[keyXDR]
	if !ok {
		return "", false
	}
	r.reads[keyXDR] = struct{}{}
	return e.entryXDR, true
}

// GetKind returns the EntryKind for an existing key, or (0, false) if absent.
func (r *LedgerEntryRegistry) GetKind(keyXDR string) (EntryKind, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[keyXDR]
	if !ok {
		return 0, false
	}
	return e.kind, true
}

// RecordRead marks keyXDR as accessed without returning the stored value.
// Useful when the caller retrieved the value through a different path and
// needs to ensure the key appears in the read set.
func (r *LedgerEntryRegistry) RecordRead(keyXDR string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reads[keyXDR] = struct{}{}
}

// RecordWrite updates the stored entry and appends the mutation to the write
// set. The before-image is taken from the current registry state (empty string
// if the entry was absent). keyXDR is also added to the read set.
// newEntryXDR must be a non-empty, valid base64 string.
func (r *LedgerEntryRegistry) RecordWrite(keyXDR, newEntryXDR string) error {
	if newEntryXDR == "" {
		return fmt.Errorf("ledger registry: newEntryXDR must not be empty for key %q", keyXDR)
	}
	if _, err := base64.StdEncoding.DecodeString(newEntryXDR); err != nil {
		return fmt.Errorf("ledger registry: newEntryXDR for key %q is not valid base64: %w", keyXDR, err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	var before string
	kind := EntryKindContract
	if existing, ok := r.entries[keyXDR]; ok {
		before = existing.entryXDR
		kind = existing.kind
	}
	r.entries[keyXDR] = &registryEntry{entryXDR: newEntryXDR, kind: kind}
	r.reads[keyXDR] = struct{}{}
	r.writes[keyXDR] = Mutation{Before: before, After: newEntryXDR, Kind: kind}
	return nil
}

// Delete removes an entry from the registry. Subsequent Gets return
// (empty, false). The deletion is not recorded as a mutation.
func (r *LedgerEntryRegistry) Delete(keyXDR string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.entries, keyXDR)
}

// ReadSet returns a deterministically sorted snapshot of all keys read during
// the simulated transaction.
func (r *LedgerEntryRegistry) ReadSet() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	keys := make([]string, 0, len(r.reads))
	for k := range r.reads {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// WriteSet returns a shallow copy of the mutation map. Keys are the base64
// XDR LedgerKeys that were written during the transaction.
func (r *LedgerEntryRegistry) WriteSet() map[string]Mutation {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]Mutation, len(r.writes))
	for k, v := range r.writes {
		out[k] = v
	}
	return out
}

// Footprint returns the transaction footprint as two sorted slices. readKeys
// contains all keys that were read (including those also written). writeKeys
// contains all keys that were mutated. Both slices are deterministically sorted
// for stable fixture comparison.
func (r *LedgerEntryRegistry) Footprint() (readKeys, writeKeys []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for k := range r.reads {
		readKeys = append(readKeys, k)
	}
	sort.Strings(readKeys)
	for k := range r.writes {
		writeKeys = append(writeKeys, k)
	}
	sort.Strings(writeKeys)
	return readKeys, writeKeys
}

// Len returns the number of entries currently stored in the registry.
func (r *LedgerEntryRegistry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}

// ToSimulatorMap serialises the registry as a map[keyXDR]entryXDR suitable
// for direct assignment to SimulationRequest.LedgerEntries.
func (r *LedgerEntryRegistry) ToSimulatorMap() map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]string, len(r.entries))
	for k, e := range r.entries {
		out[k] = e.entryXDR
	}
	return out
}

// Reset clears all entries, the read set, and the write set.
func (r *LedgerEntryRegistry) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = make(map[string]*registryEntry)
	r.reads = make(map[string]struct{})
	r.writes = make(map[string]Mutation)
}
