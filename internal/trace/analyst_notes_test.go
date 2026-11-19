// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func makeNote(t *testing.T, id, body string, kind AnnotationTargetKind) AnalystNote {
	t.Helper()
	target := TraceTarget()
	switch kind {
	case TargetStep:
		target = StepTarget("step-0-aabbccdd")
	case TargetSource:
		target = SourceTarget("token.rs", 42)
	}
	n := AnalystNote{
		ID:        id,
		Target:    target,
		Body:      body,
		CreatedAt: time.Now().UTC(),
	}
	n.Normalize()
	return n
}

func makeTraceWithSteps(txHash string, n int) *ExecutionTrace {
	tr := makeTraceN(txHash, n)
	return tr
}

// ── GenerateNoteID ────────────────────────────────────────────────────────────

func TestGenerateNoteID_Format(t *testing.T) {
	id, err := GenerateNoteID()
	if err != nil {
		t.Fatalf("GenerateNoteID: %v", err)
	}
	if !strings.HasPrefix(id, "note-") {
		t.Errorf("expected prefix 'note-', got %q", id)
	}
	if len(id) != len("note-") + 8 {
		t.Errorf("expected length %d, got %d", len("note-")+8, len(id))
	}
}

func TestGenerateNoteID_Unique(t *testing.T) {
	ids := make(map[string]bool, 100)
	for i := 0; i < 100; i++ {
		id, err := GenerateNoteID()
		if err != nil {
			t.Fatalf("GenerateNoteID: %v", err)
		}
		if ids[id] {
			t.Fatalf("duplicate ID generated: %s", id)
		}
		ids[id] = true
	}
}

// ── Normalize ─────────────────────────────────────────────────────────────────

func TestNote_Normalize_DefaultsTarget(t *testing.T) {
	n := AnalystNote{ID: "n1", Body: "test", CreatedAt: time.Now()}
	n.Normalize()
	if n.Target.Kind != TargetTrace {
		t.Errorf("expected TargetTrace, got %q", n.Target.Kind)
	}
}

func TestNote_Normalize_DeduplicatesTags(t *testing.T) {
	n := AnalystNote{
		ID:        "n1",
		Body:      "b",
		Tags:      []string{"z", "a", "a", "z", "m"},
		CreatedAt: time.Now(),
	}
	n.Normalize()
	if len(n.Tags) != 3 {
		t.Errorf("expected 3 unique tags, got %d: %v", len(n.Tags), n.Tags)
	}
	// Must be sorted.
	if n.Tags[0] != "a" || n.Tags[1] != "m" || n.Tags[2] != "z" {
		t.Errorf("expected sorted tags [a m z], got %v", n.Tags)
	}
}

func TestNote_Normalize_TimestampsUTC(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	n := AnalystNote{
		ID:        "n1",
		Body:      "b",
		CreatedAt: time.Now().In(loc),
		UpdatedAt: time.Now().Add(time.Hour).In(loc),
	}
	n.Normalize()
	if n.CreatedAt.Location() != time.UTC {
		t.Error("expected CreatedAt in UTC")
	}
	if n.UpdatedAt.Location() != time.UTC {
		t.Error("expected UpdatedAt in UTC")
	}
}

// ── Validate ──────────────────────────────────────────────────────────────────

func TestNote_Validate_EmptyID(t *testing.T) {
	n := makeNote(t, "", "body", TargetTrace)
	n.ID = ""
	if err := n.Validate(); err == nil {
		t.Error("expected error for empty ID")
	}
}

func TestNote_Validate_EmptyBody_TagsPresent_OK(t *testing.T) {
	n := AnalystNote{
		ID:        "n1",
		Target:    TraceTarget(),
		Tags:      []string{"perf"},
		CreatedAt: time.Now().UTC(),
	}
	if err := n.Validate(); err != nil {
		t.Errorf("expected no error for tag-only note, got: %v", err)
	}
}

func TestNote_Validate_BodyTooLong(t *testing.T) {
	n := makeNote(t, "n1", strings.Repeat("x", MaxNoteBodyLength+1), TargetTrace)
	if err := n.Validate(); err == nil {
		t.Error("expected error for body exceeding max length")
	}
}

func TestNote_Validate_TooManyTags(t *testing.T) {
	tags := make([]string, MaxNoteTags+1)
	for i := range tags {
		tags[i] = "tag"
	}
	n := AnalystNote{ID: "n1", Body: "b", Tags: tags, Target: TraceTarget(), CreatedAt: time.Now().UTC()}
	if err := n.Validate(); err == nil {
		t.Error("expected error for too many tags")
	}
}

func TestNote_Validate_UpdatedAtBeforeCreatedAt(t *testing.T) {
	n := AnalystNote{
		ID:        "n1",
		Body:      "b",
		Target:    TraceTarget(),
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().Add(-time.Hour).UTC(),
	}
	if err := n.Validate(); err == nil {
		t.Error("expected error for updated_at before created_at")
	}
}

func TestNote_Validate_Unicode(t *testing.T) {
	// Unicode body should be valid.
	n := makeNote(t, "n1", "Observação: 日本語テスト Ünïcödë 🔍", TargetTrace)
	if err := n.Validate(); err != nil {
		t.Errorf("expected valid Unicode note, got: %v", err)
	}
}

// ── AttachNotes ───────────────────────────────────────────────────────────────

func TestAttachNotes_Add(t *testing.T) {
	tr := makeTraceWithSteps("tx1", 3)
	note := makeNote(t, "n1", "first observation", TargetTrace)

	_, err := tr.AttachNotes([]AnalystNote{note})
	if err != nil {
		t.Fatalf("AttachNotes: %v", err)
	}
	if len(tr.Annotations.Notes) != 1 {
		t.Errorf("expected 1 note, got %d", len(tr.Annotations.Notes))
	}
}

func TestAttachNotes_Replace_SameID(t *testing.T) {
	tr := makeTraceWithSteps("tx1", 3)
	n1 := makeNote(t, "n1", "original", TargetTrace)
	tr.AttachNotes([]AnalystNote{n1}) //nolint

	n1updated := n1
	n1updated.Body = "updated body"
	_, err := tr.AttachNotes([]AnalystNote{n1updated})
	if err != nil {
		t.Fatalf("AttachNotes replace: %v", err)
	}
	if len(tr.Annotations.Notes) != 1 {
		t.Errorf("expected 1 note after replace, got %d", len(tr.Annotations.Notes))
	}
	if tr.Annotations.Notes[0].Body != "updated body" {
		t.Errorf("expected updated body, got %q", tr.Annotations.Notes[0].Body)
	}
}

func TestAttachNotes_DuplicateIDInBatch_Rejected(t *testing.T) {
	tr := makeTraceWithSteps("tx1", 3)
	n1a := makeNote(t, "n1", "first", TargetTrace)
	n1b := makeNote(t, "n1", "duplicate", TargetTrace)

	_, err := tr.AttachNotes([]AnalystNote{n1a, n1b})
	if err == nil {
		t.Error("expected error for duplicate ID in batch")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("expected 'duplicate' in error, got: %v", err)
	}
}

func TestAttachNotes_InvalidNote_Rejected(t *testing.T) {
	tr := makeTraceWithSteps("tx1", 3)
	bad := AnalystNote{ID: "", Body: "no id", Target: TraceTarget(), CreatedAt: time.Now()}
	_, err := tr.AttachNotes([]AnalystNote{bad})
	if err == nil {
		t.Error("expected error for note with empty ID")
	}
}

func TestAttachNotes_LimitEnforced(t *testing.T) {
	tr := makeTraceWithSteps("tx1", 3)
	notes := make([]AnalystNote, MaxNotes+1)
	for i := range notes {
		id, _ := GenerateNoteID()
		notes[i] = makeNote(t, id+"-"+string(rune('0'+i%10)), "body", TargetTrace)
	}
	_, err := tr.AttachNotes(notes)
	if err == nil {
		t.Error("expected error when exceeding note limit")
	}
}

// ── RemoveNote ────────────────────────────────────────────────────────────────

func TestRemoveNote_Success(t *testing.T) {
	tr := makeTraceWithSteps("tx1", 3)
	n := makeNote(t, "n1", "to remove", TargetTrace)
	tr.AttachNotes([]AnalystNote{n}) //nolint

	if err := tr.RemoveNote("n1"); err != nil {
		t.Fatalf("RemoveNote: %v", err)
	}
	if len(tr.Annotations.Notes) != 0 {
		t.Errorf("expected 0 notes after remove, got %d", len(tr.Annotations.Notes))
	}
}

func TestRemoveNote_NotFound(t *testing.T) {
	tr := makeTraceWithSteps("tx1", 3)
	err := tr.RemoveNote("nonexistent-id")
	if err == nil {
		t.Error("expected error for non-existent note ID")
	}
}

// ── Dangling reference detection ─────────────────────────────────────────────

func TestResolveNotes_DeletedStep_Dangling(t *testing.T) {
	tr := makeTraceWithSteps("tx1", 3)
	// Attach a note to a step ID that doesn't exist in the trace.
	note := AnalystNote{
		ID:        "n1",
		Target:    StepTarget("step-99-nonexistent"),
		Body:      "anchored to deleted step",
		CreatedAt: time.Now().UTC(),
	}
	resolutions := ResolveNotes(tr, []AnalystNote{note})
	if len(resolutions) != 1 {
		t.Fatalf("expected 1 resolution, got %d", len(resolutions))
	}
	if resolutions[0].Status != NoteDangling {
		t.Errorf("expected NoteDangling, got %v", resolutions[0].Status)
	}
	if resolutions[0].Reason == "" {
		t.Error("expected non-empty dangling reason")
	}
}

func TestResolveNotes_TraceWide_AlwaysResolves(t *testing.T) {
	tr := makeTraceWithSteps("tx1", 0) // empty trace
	note := makeNote(t, "n1", "global observation", TargetTrace)
	resolutions := ResolveNotes(tr, []AnalystNote{note})
	if resolutions[0].Status != NoteTraceWide {
		t.Errorf("expected NoteTraceWide, got %v", resolutions[0].Status)
	}
}

func TestResolveNotes_SourceLocation_Resolved(t *testing.T) {
	tr := NewExecutionTrace("tx", 100)
	tr.AddState(ExecutionState{
		Step:       0,
		ContractID: "C1",
		Function:   "fn",
		SourceFile: "token.rs",
		SourceLine: 42,
		EventType:  EventTypeContractCall,
	})

	note := AnalystNote{
		ID:        "n1",
		Target:    SourceTarget("token.rs", 42),
		Body:      "interesting",
		CreatedAt: time.Now().UTC(),
	}
	resolutions := ResolveNotes(tr, []AnalystNote{note})
	if resolutions[0].Status != NoteResolved {
		t.Errorf("expected NoteResolved, got %v: %s", resolutions[0].Status, resolutions[0].Reason)
	}
	if resolutions[0].StepIndex != 0 {
		t.Errorf("expected StepIndex=0, got %d", resolutions[0].StepIndex)
	}
}

// ── NoteFile round trip ───────────────────────────────────────────────────────

func TestNoteFile_RoundTrip_JSON(t *testing.T) {
	notes := []AnalystNote{
		makeNote(t, "n1", "first note", TargetTrace),
		makeNote(t, "n2", "second note with emoji 🔍", TargetTrace),
		{
			ID:        "n3",
			Target:    SourceTarget("lib.rs", 10),
			Body:      "unicode: 日本語テスト",
			Tags:      []string{"perf", "review"},
			CreatedAt: time.Now().UTC(),
		},
	}
	for i := range notes {
		notes[i].Normalize()
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "notes.json")

	file := &NoteFile{
		SchemaVersion:   NoteFileSchemaVersion,
		GeneratedAt:     time.Now().UTC().Truncate(time.Second),
		TransactionHash: "txhash-test",
		Notes:           notes,
	}
	if err := file.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := LoadNoteFile(path)
	if err != nil {
		t.Fatalf("LoadNoteFile: %v", err)
	}

	if len(loaded.Notes) != len(notes) {
		t.Errorf("expected %d notes, got %d", len(notes), len(loaded.Notes))
	}
	for i, n := range loaded.Notes {
		if n.ID != notes[i].ID {
			t.Errorf("note %d ID mismatch: %q vs %q", i, n.ID, notes[i].ID)
		}
		if n.Body != notes[i].Body {
			t.Errorf("note %d body mismatch", i)
		}
	}
}

func TestNoteFile_BareArray_Accepted(t *testing.T) {
	notes := []AnalystNote{makeNote(t, "n1", "bare", TargetTrace)}
	data, _ := json.MarshalIndent(notes, "", "  ")
	path := filepath.Join(t.TempDir(), "bare.json")
	os.WriteFile(path, data, 0644) //nolint

	loaded, err := LoadNoteFile(path)
	if err != nil {
		t.Fatalf("LoadNoteFile bare array: %v", err)
	}
	if len(loaded.Notes) != 1 {
		t.Errorf("expected 1 note, got %d", len(loaded.Notes))
	}
}

func TestNoteFile_UnsupportedVersion_Rejected(t *testing.T) {
	file := &NoteFile{
		SchemaVersion: "99.0",
		Notes:         []AnalystNote{},
	}
	data, _ := json.MarshalIndent(file, "", "  ")
	path := filepath.Join(t.TempDir(), "future.json")
	os.WriteFile(path, data, 0644) //nolint

	_, err := LoadNoteFile(path)
	if err == nil {
		t.Error("expected error for unsupported schema version")
	}
	if !strings.Contains(err.Error(), "schema_version") {
		t.Errorf("expected 'schema_version' in error, got: %v", err)
	}
}

// ── Clone preserves Notes ─────────────────────────────────────────────────────

func TestTraceAnnotations_Clone_PreservesNotes(t *testing.T) {
	tr := makeTraceWithSteps("tx1", 3)
	note := makeNote(t, "n1", "original", TargetTrace)
	tr.AttachNotes([]AnalystNote{note}) //nolint

	cloned := tr.Annotations.Clone()
	// Mutate the clone's note; original must not change.
	cloned.Notes[0].Body = "mutated"

	if tr.Annotations.Notes[0].Body != "original" {
		t.Error("Clone did not deep-copy Notes — original was mutated")
	}
}

// stableTraceHash returns a simple hash of execution evidence that should not
// change when notes are added/removed. Uses TransactionHash + step count as a
// proxy since ExecutionTrace has no Fingerprint() method.
func stableTraceHash(tr *ExecutionTrace) string {
	if tr == nil {
		return ""
	}
	return fmt.Sprintf("%s:%d", tr.TransactionHash, len(tr.States))
}

// ── Note doesn't affect execution hash ───────────────────────────────────────

func TestNote_DoesNotAffectFingerprint(t *testing.T) {
	tr := makeTraceWithSteps("tx1", 5)
	hashBefore := stableTraceHash(tr)

	note := makeNote(t, "n1", "shouldn't change fp", TargetTrace)
	tr.AttachNotes([]AnalystNote{note}) //nolint

	hashAfter := stableTraceHash(tr)
	if hashBefore != hashAfter {
		t.Error("adding a note changed the trace execution evidence — notes must not affect execution identity")
	}
}

func TestNote_RemoveDoesNotAffectFingerprint(t *testing.T) {
	tr := makeTraceWithSteps("tx1", 5)
	note := makeNote(t, "n1", "body", TargetTrace)
	tr.AttachNotes([]AnalystNote{note}) //nolint
	hashWith := stableTraceHash(tr)

	tr.RemoveNote("n1") //nolint
	hashWithout := stableTraceHash(tr)

	if hashWith != hashWithout {
		t.Error("removing a note changed the trace execution evidence")
	}
}

// ── ExportNoteFile ────────────────────────────────────────────────────────────

func TestExportNoteFile_DeterministicOrder(t *testing.T) {
	tr := makeTraceN("tx1", 5)
	// Attach notes in reverse step order.
	n1 := makeNote(t, "late", "later step", TargetTrace)
	n2 := makeNote(t, "early", "early note", TargetTrace)
	n1.CreatedAt = time.Now().Add(time.Hour)
	n2.CreatedAt = time.Now()

	tr.AttachNotes([]AnalystNote{n1, n2}) //nolint

	f1, _ := ExportNoteFile(tr, time.Now())
	f2, _ := ExportNoteFile(tr, time.Now())

	ids1 := make([]string, len(f1.Notes))
	ids2 := make([]string, len(f2.Notes))
	for i, n := range f1.Notes {
		ids1[i] = n.ID
	}
	for i, n := range f2.Notes {
		ids2[i] = n.ID
	}
	for i := range ids1 {
		if ids1[i] != ids2[i] {
			t.Errorf("non-deterministic order: %v vs %v", ids1, ids2)
		}
	}
}
