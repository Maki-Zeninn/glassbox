// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package session

import (
	"strings"
	"testing"
	"time"
)

// ── Append ordering & bounds ──────────────────────────────────────────────────

func TestProvenanceTimeline_Append_PreservesOrder(t *testing.T) {
	var tl ProvenanceTimeline
	tl.Append(ProvenanceEntry{Operation: ProvenanceFetched, Actor: ActorUser, Success: true})
	tl.Append(ProvenanceEntry{Operation: ProvenanceReplayed, Actor: ActorUser, Success: true})
	tl.Append(ProvenanceEntry{Operation: ProvenanceExported, Actor: ActorUser, Success: true})

	if len(tl.Entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(tl.Entries))
	}
	wantOrder := []ProvenanceOperation{ProvenanceFetched, ProvenanceReplayed, ProvenanceExported}
	for i, op := range wantOrder {
		if tl.Entries[i].Operation != op {
			t.Errorf("entry %d: got operation %q, want %q", i, tl.Entries[i].Operation, op)
		}
	}
}

func TestProvenanceTimeline_Append_BoundedAtMax(t *testing.T) {
	var tl ProvenanceTimeline
	for i := 0; i < MaxProvenanceEntries+50; i++ {
		tl.Append(ProvenanceEntry{Operation: ProvenanceReplayed, Actor: ActorSystem, Success: true})
	}
	if len(tl.Entries) != MaxProvenanceEntries {
		t.Fatalf("expected timeline to be capped at %d entries, got %d", MaxProvenanceEntries, len(tl.Entries))
	}
}

func TestProvenanceTimeline_Append_DropsOldestFirst(t *testing.T) {
	var tl ProvenanceTimeline
	for i := 0; i < MaxProvenanceEntries+1; i++ {
		tl.Append(ProvenanceEntry{Operation: ProvenanceFetched, Detail: fromInt(i)})
	}
	// The very first entry (Detail "0") should have been evicted; the most
	// recent (Detail for the last index) must survive.
	if tl.Entries[0].Detail == "0" {
		t.Error("oldest entry should have been dropped once the timeline exceeded its bound")
	}
	last := tl.Entries[len(tl.Entries)-1]
	if last.Detail != fromInt(MaxProvenanceEntries) {
		t.Errorf("expected the newest entry to survive, got Detail=%q", last.Detail)
	}
}

func fromInt(i int) string {
	digits := []byte{}
	if i == 0 {
		return "0"
	}
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}

// ── Redaction ─────────────────────────────────────────────────────────────────

func TestProvenanceTimeline_Append_RedactsPII(t *testing.T) {
	var tl ProvenanceTimeline
	tl.Append(ProvenanceEntry{
		Operation: ProvenanceExported,
		Detail:    "exported by /home/alice/.Glassbox/sessions.db",
	})
	if strings.Contains(tl.Entries[0].Detail, "alice") {
		t.Errorf("expected the username to be redacted from Detail, got: %q", tl.Entries[0].Detail)
	}
}

// ── Failed operations ─────────────────────────────────────────────────────────

func TestProvenanceTimeline_FailedEntry_NotRenderedAsSuccess(t *testing.T) {
	var tl ProvenanceTimeline
	tl.Append(ProvenanceEntry{Operation: ProvenanceExported, Success: false, Detail: "disk full"})

	if tl.Entries[0].Success {
		t.Fatal("a failed operation must be recorded with Success=false")
	}
	rendered := tl.RenderText()
	if !strings.Contains(rendered, "FAILED") {
		t.Errorf("rendered output must flag the failed entry, got: %q", rendered)
	}
	if strings.Contains(rendered, "status=ok") {
		t.Errorf("a failed entry must never be rendered with status=ok, got: %q", rendered)
	}
}

func TestProvenanceTimeline_RenderText_EmptyTimeline(t *testing.T) {
	var tl ProvenanceTimeline
	rendered := tl.RenderText()
	if !strings.Contains(rendered, "No provenance history") {
		t.Errorf("expected empty-timeline message, got: %q", rendered)
	}
}

// ── RecordProvenance / RoundTrip ─────────────────────────────────────────────

func TestRecordProvenance_AppendsAndSerializes(t *testing.T) {
	d := &Data{ID: "sess-1"}
	if err := RecordProvenance(d, ProvenanceFetched, ActorUser, "1.0.0", "", "initial fetch", true); err != nil {
		t.Fatalf("RecordProvenance: %v", err)
	}
	if d.ProvenanceJSON == "" {
		t.Fatal("expected ProvenanceJSON to be populated")
	}

	tl := ParseProvenanceTimeline(d.ProvenanceJSON)
	if len(tl.Entries) != 1 || tl.Entries[0].Operation != ProvenanceFetched {
		t.Fatalf("expected a single fetched entry, got: %+v", tl.Entries)
	}
}

func TestRecordProvenance_NilData_Errors(t *testing.T) {
	if err := RecordProvenance(nil, ProvenanceFetched, ActorUser, "1.0.0", "", "", true); err == nil {
		t.Fatal("expected error for nil session data")
	}
}

func TestParseProvenanceTimeline_EmptyString_ReturnsEmptyTimeline(t *testing.T) {
	tl := ParseProvenanceTimeline("")
	if tl == nil {
		t.Fatal("expected a non-nil empty timeline")
	}
	if len(tl.Entries) != 0 {
		t.Errorf("expected zero entries, got %d", len(tl.Entries))
	}
}

func TestParseProvenanceTimeline_MalformedJSON_ReturnsEmptyTimeline(t *testing.T) {
	tl := ParseProvenanceTimeline("{not json")
	if tl == nil || len(tl.Entries) != 0 {
		t.Fatalf("expected an empty timeline for malformed JSON, got: %+v", tl)
	}
}

// ── Migration visibility ──────────────────────────────────────────────────────

func TestUpgradeSessionData_RecordsMigrationInProvenance(t *testing.T) {
	if SchemaVersion <= MinSupportedSchemaVersion {
		t.Skip("no upgradable version below current")
	}
	d := &Data{
		ID:            "sess-migrate",
		SchemaVersion: SchemaVersion - 1,
		CreatedAt:     time.Now(),
		LastAccessAt:  time.Now(),
	}
	upgraded, err := UpgradeSessionData(d)
	if err != nil {
		t.Fatalf("UpgradeSessionData: %v", err)
	}
	if !upgraded {
		t.Fatal("expected the session to be upgraded")
	}

	tl := ParseProvenanceTimeline(d.ProvenanceJSON)
	found := false
	for _, e := range tl.Entries {
		if e.Operation == ProvenanceMigrated {
			found = true
			if !e.Success {
				t.Error("a successful migration must be recorded with Success=true")
			}
			if strings.TrimSpace(e.Detail) == "" {
				t.Error("expected the migration entry to carry a detail describing the version change")
			}
		}
	}
	if !found {
		t.Fatal("expected a 'migrated' provenance entry after schema upgrade")
	}
}
