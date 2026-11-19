// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package simulator

import (
	"encoding/base64"
	"testing"
)

// xdr encodes s as base64 to simulate a valid XDR payload.
func xdrPayload(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

// ── Put / Get ─────────────────────────────────────────────────────────────────

func TestRegistry_PutAndGet_Present(t *testing.T) {
	r := NewLedgerEntryRegistry()
	key := xdrPayload("account-key")
	entry := xdrPayload("account-entry")
	if err := r.Put(key, entry, EntryKindAccount); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, present := r.Get(key)
	if !present {
		t.Fatal("expected entry to be present")
	}
	if got != entry {
		t.Errorf("got %q, want %q", got, entry)
	}
}

func TestRegistry_Get_Missing(t *testing.T) {
	r := NewLedgerEntryRegistry()
	_, present := r.Get(xdrPayload("nonexistent"))
	if present {
		t.Fatal("expected missing entry to return present=false")
	}
}

func TestRegistry_Put_EmptyKey(t *testing.T) {
	r := NewLedgerEntryRegistry()
	if err := r.Put("", xdrPayload("entry"), EntryKindAccount); err == nil {
		t.Fatal("expected error for empty key")
	}
}

func TestRegistry_Put_EmptyEntry(t *testing.T) {
	r := NewLedgerEntryRegistry()
	if err := r.Put(xdrPayload("key"), "", EntryKindAccount); err == nil {
		t.Fatal("expected error for empty entry")
	}
}

func TestRegistry_Put_InvalidBase64(t *testing.T) {
	r := NewLedgerEntryRegistry()
	if err := r.Put(xdrPayload("key"), "not!!base64", EntryKindAccount); err == nil {
		t.Fatal("expected error for invalid base64 entry")
	}
}

// ── Entry kinds ───────────────────────────────────────────────────────────────

func TestRegistry_AllEntryKinds(t *testing.T) {
	kinds := []struct {
		kind EntryKind
		name string
	}{
		{EntryKindAccount, "account"},
		{EntryKindContract, "contract_data_persistent"},
		{EntryKindCode, "contract_code"},
		{EntryKindTTL, "ttl"},
		{EntryKindPersistent, "contract_data_persistent"},
		{EntryKindTemporary, "contract_data_temporary"},
		{EntryKindConfigSetting, "config_setting"},
	}
	r := NewLedgerEntryRegistry()
	for _, tc := range kinds {
		key := xdrPayload(tc.name + "-key")
		entry := xdrPayload(tc.name + "-entry")
		if err := r.Put(key, entry, tc.kind); err != nil {
			t.Errorf("Put kind=%s: %v", tc.name, err)
			continue
		}
		_, present := r.Get(key)
		if !present {
			t.Errorf("Get kind=%s: expected present", tc.name)
		}
		k, ok := r.GetKind(key)
		if !ok || k != tc.kind {
			t.Errorf("GetKind kind=%s: got (%v, %v)", tc.name, k, ok)
		}
		if tc.kind.String() != tc.name {
			t.Errorf("String() = %q, want %q", tc.kind.String(), tc.name)
		}
	}
}

// ── Read set ──────────────────────────────────────────────────────────────────

func TestRegistry_ReadSet_TracksAccesses(t *testing.T) {
	r := NewLedgerEntryRegistry()
	keys := []string{xdrPayload("k1"), xdrPayload("k2"), xdrPayload("k3")}
	for _, k := range keys {
		_ = r.Put(k, xdrPayload("entry"), EntryKindContract)
	}
	r.Get(keys[0])
	r.Get(keys[2])
	rs := r.ReadSet()
	if len(rs) != 2 {
		t.Fatalf("read set len = %d, want 2", len(rs))
	}
	if rs[0] != keys[0] || rs[1] != keys[2] {
		t.Errorf("read set = %v, want [%s, %s]", rs, keys[0], keys[2])
	}
}

func TestRegistry_ReadSet_IsSorted(t *testing.T) {
	r := NewLedgerEntryRegistry()
	// Insert in reverse order.
	for i := 5; i >= 1; i-- {
		k := xdrPayload(string(rune('a' + i)))
		_ = r.Put(k, xdrPayload("e"), EntryKindAccount)
		r.Get(k)
	}
	rs := r.ReadSet()
	for i := 1; i < len(rs); i++ {
		if rs[i] < rs[i-1] {
			t.Errorf("ReadSet not sorted at index %d: %q < %q", i, rs[i], rs[i-1])
		}
	}
}

func TestRegistry_RecordRead_MissingKey(t *testing.T) {
	r := NewLedgerEntryRegistry()
	r.RecordRead(xdrPayload("phantom"))
	rs := r.ReadSet()
	if len(rs) != 1 {
		t.Errorf("expected phantom key in read set, got %v", rs)
	}
}

// ── Write set / mutations ─────────────────────────────────────────────────────

func TestRegistry_RecordWrite_TracksMutation(t *testing.T) {
	r := NewLedgerEntryRegistry()
	key := xdrPayload("contract-key")
	before := xdrPayload("before-entry")
	after := xdrPayload("after-entry")
	_ = r.Put(key, before, EntryKindContract)
	if err := r.RecordWrite(key, after); err != nil {
		t.Fatalf("RecordWrite: %v", err)
	}
	ws := r.WriteSet()
	m, ok := ws[key]
	if !ok {
		t.Fatal("expected key in write set")
	}
	if m.Before != before {
		t.Errorf("before = %q, want %q", m.Before, before)
	}
	if m.After != after {
		t.Errorf("after = %q, want %q", m.After, after)
	}
}

func TestRegistry_RecordWrite_MissingBeforeImage(t *testing.T) {
	r := NewLedgerEntryRegistry()
	key := xdrPayload("new-key")
	after := xdrPayload("new-entry")
	if err := r.RecordWrite(key, after); err != nil {
		t.Fatalf("RecordWrite on absent key: %v", err)
	}
	ws := r.WriteSet()
	if ws[key].Before != "" {
		t.Errorf("before image should be empty for absent key, got %q", ws[key].Before)
	}
}

func TestRegistry_RecordWrite_AlsoUpdatesEntry(t *testing.T) {
	r := NewLedgerEntryRegistry()
	key := xdrPayload("k")
	_ = r.Put(key, xdrPayload("old"), EntryKindCode)
	_ = r.RecordWrite(key, xdrPayload("new"))
	got, present := r.Get(key)
	if !present {
		t.Fatal("entry should still be present after RecordWrite")
	}
	if got != xdrPayload("new") {
		t.Errorf("entry not updated: got %q", got)
	}
}

func TestRegistry_RecordWrite_EmptyNewEntry(t *testing.T) {
	r := NewLedgerEntryRegistry()
	if err := r.RecordWrite(xdrPayload("k"), ""); err == nil {
		t.Fatal("expected error for empty newEntryXDR")
	}
}

// ── Footprint ─────────────────────────────────────────────────────────────────

func TestRegistry_Footprint_Sorted(t *testing.T) {
	r := NewLedgerEntryRegistry()
	keys := []string{xdrPayload("c"), xdrPayload("a"), xdrPayload("b")}
	for _, k := range keys {
		_ = r.Put(k, xdrPayload("e"), EntryKindTemporary)
		r.Get(k)
		_ = r.RecordWrite(k, xdrPayload("e2"))
	}
	rk, wk := r.Footprint()
	for i := 1; i < len(rk); i++ {
		if rk[i] < rk[i-1] {
			t.Errorf("readKeys not sorted at %d", i)
		}
	}
	for i := 1; i < len(wk); i++ {
		if wk[i] < wk[i-1] {
			t.Errorf("writeKeys not sorted at %d", i)
		}
	}
}

func TestRegistry_Footprint_ReadOnlyVsReadWrite(t *testing.T) {
	r := NewLedgerEntryRegistry()
	readOnlyKey := xdrPayload("ro")
	writeKey := xdrPayload("rw")
	_ = r.Put(readOnlyKey, xdrPayload("ro-entry"), EntryKindConfigSetting)
	_ = r.Put(writeKey, xdrPayload("rw-entry"), EntryKindPersistent)
	r.Get(readOnlyKey)
	r.Get(writeKey)
	_ = r.RecordWrite(writeKey, xdrPayload("rw-entry-new"))

	rk, wk := r.Footprint()
	if len(rk) != 2 {
		t.Errorf("readKeys len = %d, want 2", len(rk))
	}
	if len(wk) != 1 || wk[0] != writeKey {
		t.Errorf("writeKeys = %v, want [%s]", wk, writeKey)
	}
}

// ── ToSimulatorMap ────────────────────────────────────────────────────────────

func TestRegistry_ToSimulatorMap(t *testing.T) {
	r := NewLedgerEntryRegistry()
	k1 := xdrPayload("k1")
	e1 := xdrPayload("e1")
	_ = r.Put(k1, e1, EntryKindTTL)
	m := r.ToSimulatorMap()
	if m[k1] != e1 {
		t.Errorf("ToSimulatorMap: got %q, want %q", m[k1], e1)
	}
}

// ── Delete ────────────────────────────────────────────────────────────────────

func TestRegistry_Delete(t *testing.T) {
	r := NewLedgerEntryRegistry()
	k := xdrPayload("del-key")
	_ = r.Put(k, xdrPayload("entry"), EntryKindAccount)
	r.Delete(k)
	_, present := r.Get(k)
	if present {
		t.Fatal("entry should be absent after Delete")
	}
}

// ── Reset ─────────────────────────────────────────────────────────────────────

func TestRegistry_Reset(t *testing.T) {
	r := NewLedgerEntryRegistry()
	k := xdrPayload("k")
	_ = r.Put(k, xdrPayload("e"), EntryKindAccount)
	r.Get(k)
	_ = r.RecordWrite(k, xdrPayload("e2"))
	r.Reset()
	if r.Len() != 0 {
		t.Errorf("Len after Reset = %d, want 0", r.Len())
	}
	if len(r.ReadSet()) != 0 {
		t.Error("ReadSet should be empty after Reset")
	}
	if len(r.WriteSet()) != 0 {
		t.Error("WriteSet should be empty after Reset")
	}
}

// ── Missing entry host error ───────────────────────────────────────────────────

// TestRegistry_MissingEntryIsDistinctFromPresent verifies that a key that was
// never Put returns present=false, while a key that was Put returns the
// stored XDR. This is the core invariant that allows host-error simulation.
func TestRegistry_MissingEntryIsDistinctFromPresent(t *testing.T) {
	r := NewLedgerEntryRegistry()
	presentKey := xdrPayload("present")
	_ = r.Put(presentKey, xdrPayload("data"), EntryKindContract)

	_, present := r.Get(presentKey)
	if !present {
		t.Error("present key should return present=true")
	}

	_, missing := r.Get(xdrPayload("absent"))
	if missing {
		t.Error("absent key should return present=false — missing ≠ zero-valued")
	}
}
