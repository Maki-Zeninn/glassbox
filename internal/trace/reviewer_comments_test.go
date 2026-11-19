// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixedTime keeps every test deterministic; annotation round trips are only
// byte-stable if the timestamps going in are the timestamps coming out.
var fixedTime = time.Date(2026, 7, 25, 9, 30, 0, 0, time.UTC)

// newAnnotatedTrace builds a small trace with source locations on two of its
// three steps, which is enough to exercise step, source, and trace targets.
func newAnnotatedTrace(t *testing.T) *ExecutionTrace {
	t.Helper()
	tr := NewExecutionTrace("tx-abc", 10)
	tr.StartTime = fixedTime
	tr.EndTime = fixedTime.Add(time.Second)
	tr.AddState(ExecutionState{
		Step: 0, Operation: "contract_call", EventType: EventTypeContractCall,
		ContractID: "C1", Function: "transfer",
		SourceFile: "src/lib.rs", SourceLine: 42,
	})
	tr.AddState(ExecutionState{
		Step: 1, Operation: "host_function", EventType: EventTypeHostFunction,
		Function: "require_auth",
		SourceFile: "src/auth.rs", SourceLine: 17,
	})
	tr.AddState(ExecutionState{
		Step: 2, Operation: "trap", EventType: EventTypeTrap,
		Error: "unreachable",
	})
	return tr
}

func comment(id, author, body string, target AnnotationTarget) ReviewerComment {
	return ReviewerComment{
		ID: id, Author: author, Body: body, Target: target,
		Severity: SeverityWarning, Resolution: ResolutionOpen,
		CreatedAt: fixedTime,
	}
}

// --- stable step IDs -------------------------------------------------------

func TestStepIDOf_IsDeterministic(t *testing.T) {
	tr := newAnnotatedTrace(t)
	first := tr.StepID(0)
	if first == "" {
		t.Fatal("expected a non-empty step ID")
	}
	if second := tr.StepID(0); first != second {
		t.Fatalf("step ID is not deterministic: %q then %q", first, second)
	}
	if tr.StepID(0) == tr.StepID(1) {
		t.Fatal("expected distinct steps to have distinct IDs")
	}
	if got := tr.StepID(99); got != "" {
		t.Fatalf("expected empty ID for out-of-range index, got %q", got)
	}
}

// The whole point of a content-derived ID is that verbosity filtering, which
// rewrites state fields, must not invalidate it.
func TestStepIDOf_SurvivesVerbosityFiltering(t *testing.T) {
	tr := newAnnotatedTrace(t)
	before := []string{tr.StepID(0), tr.StepID(1), tr.StepID(2)}

	for _, v := range []Verbosity{VerbosityNormal, VerbositySummary} {
		filtered := FilterExecutionTrace(tr, v)
		for i, want := range before {
			if got := filtered.StepID(i); got != want {
				t.Errorf("verbosity %v: step %d ID changed from %q to %q", v, i, want, got)
			}
		}
	}
}

func TestStepIDOf_SurvivesMigration(t *testing.T) {
	tr := newAnnotatedTrace(t)
	before := tr.StepID(0)

	migrated, err := migrateTrace(tr, TraceFormatVersion{Major: 1, Minor: 0}, TraceFormatVersion{Major: 1, Minor: 1})
	if err != nil {
		t.Fatalf("migrateTrace: %v", err)
	}
	if got := migrated.StepID(0); got != before {
		t.Fatalf("step ID changed across migration: %q → %q", before, got)
	}
}

func TestStepIDOf_ChangesWhenStepIdentityChanges(t *testing.T) {
	tr := newAnnotatedTrace(t)
	before := tr.StepID(0)
	tr.States[0].Function = "burn"
	if after := tr.StepID(0); after == before {
		t.Fatal("expected the step ID to change when the function changes")
	}
}

// --- validation ------------------------------------------------------------

func TestReviewerComment_Validate(t *testing.T) {
	valid := comment("c1", "alice", "looks wrong", StepTarget("step-0-abcdef12"))

	tests := []struct {
		name    string
		mutate  func(*ReviewerComment)
		wantErr string
	}{
		{"valid", func(*ReviewerComment) {}, ""},
		{"empty id", func(c *ReviewerComment) { c.ID = "" }, "comment id is empty"},
		{"long id", func(c *ReviewerComment) { c.ID = strings.Repeat("x", MaxCommentIDLength+1) }, "id exceeds"},
		{"empty author", func(c *ReviewerComment) { c.Author = "" }, "empty author"},
		{"long author", func(c *ReviewerComment) { c.Author = strings.Repeat("a", MaxCommentAuthorLength+1) }, "author exceeds"},
		{"empty body", func(c *ReviewerComment) { c.Body = "" }, "empty body"},
		{"long body", func(c *ReviewerComment) { c.Body = strings.Repeat("b", MaxCommentLength+1) }, "exceeds the maximum length"},
		{"bad severity", func(c *ReviewerComment) { c.Severity = "urgent" }, "unknown severity"},
		{"bad resolution", func(c *ReviewerComment) { c.Resolution = "maybe" }, "unknown resolution"},
		{"no created_at", func(c *ReviewerComment) { c.CreatedAt = time.Time{} }, "no created_at"},
		{"updated before created", func(c *ReviewerComment) { c.UpdatedAt = fixedTime.Add(-time.Hour) }, "updated_at before created_at"},
		{"step target without id", func(c *ReviewerComment) { c.Target = AnnotationTarget{Kind: TargetStep} }, "requires a non-empty step_id"},
		{"source target without file", func(c *ReviewerComment) { c.Target = AnnotationTarget{Kind: TargetSource, SourceLine: 1} }, "requires a non-empty source_file"},
		{"source target with zero line", func(c *ReviewerComment) { c.Target = AnnotationTarget{Kind: TargetSource, SourceFile: "a.rs"} }, "requires a positive source_line"},
		{"trace target with anchor", func(c *ReviewerComment) { c.Target = AnnotationTarget{Kind: TargetTrace, StepID: "x"} }, "must not set step_id"},
		{"unknown kind", func(c *ReviewerComment) { c.Target = AnnotationTarget{Kind: "line"} }, "unknown target kind"},
		{"mixed step and source", func(c *ReviewerComment) {
			c.Target = AnnotationTarget{Kind: TargetStep, StepID: "s", SourceFile: "a.rs"}
		}, "must not set source_file"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := valid
			tc.mutate(&c)
			err := c.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected valid, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}

func TestReviewerComment_NormalizeFillsDefaults(t *testing.T) {
	c := ReviewerComment{
		ID: "  c1  ", Author: " alice ", Body: "  needs a look  ",
		CreatedAt: fixedTime.In(time.FixedZone("CEST", 2*3600)),
	}
	c.Normalize()

	if c.ID != "c1" || c.Author != "alice" || c.Body != "needs a look" {
		t.Fatalf("expected surrounding whitespace to be trimmed, got %+v", c)
	}
	if c.Severity != DefaultAnnotationSeverity {
		t.Fatalf("expected default severity %q, got %q", DefaultAnnotationSeverity, c.Severity)
	}
	if c.Resolution != DefaultAnnotationResolution {
		t.Fatalf("expected default resolution %q, got %q", DefaultAnnotationResolution, c.Resolution)
	}
	if c.Target.Kind != TargetTrace {
		t.Fatalf("expected an unanchored comment to default to the trace target, got %q", c.Target.Kind)
	}
	if c.CreatedAt.Location() != time.UTC {
		t.Fatalf("expected created_at normalised to UTC, got %v", c.CreatedAt.Location())
	}
}

// --- reference resolution --------------------------------------------------

func TestValidateAnnotationRefs_ResolvesEveryTargetKind(t *testing.T) {
	tr := newAnnotatedTrace(t)
	comments := []ReviewerComment{
		comment("step", "alice", "on step 1", StepTarget(tr.StepID(1))),
		comment("source", "bob", "on source", SourceTarget("src/lib.rs", 42)),
		comment("trace", "carol", "on the trace", TraceTarget()),
	}

	report := ValidateAnnotationRefs(tr, comments)
	if report.HasDangling() {
		t.Fatalf("expected no dangling refs, got %v", report.Warnings())
	}
	if len(report.Resolved) != 3 {
		t.Fatalf("expected 3 resolved comments, got %d", len(report.Resolved))
	}

	byID := map[string]int{}
	for _, r := range report.Resolved {
		byID[r.Comment.ID] = r.StepIndex
	}
	if byID["step"] != 1 {
		t.Errorf("expected step target to resolve to index 1, got %d", byID["step"])
	}
	if byID["source"] != 0 {
		t.Errorf("expected source target to resolve to index 0, got %d", byID["source"])
	}
	if byID["trace"] != -1 {
		t.Errorf("expected trace target to resolve to index -1, got %d", byID["trace"])
	}
}

// The acceptance criterion: a dangling reference is reported, and the valid
// comments around it are still resolved rather than dropped.
func TestValidateAnnotationRefs_DanglingDoesNotDropValidComments(t *testing.T) {
	tr := newAnnotatedTrace(t)
	comments := []ReviewerComment{
		comment("good-1", "alice", "fine", StepTarget(tr.StepID(0))),
		comment("bad", "bob", "broken anchor", StepTarget("step-99-deadbeef")),
		comment("good-2", "carol", "also fine", SourceTarget("src/auth.rs", 17)),
	}

	report := ValidateAnnotationRefs(tr, comments)
	if len(report.Resolved) != 2 {
		t.Fatalf("expected the 2 valid comments to survive, got %d", len(report.Resolved))
	}
	if len(report.Dangling) != 1 {
		t.Fatalf("expected 1 dangling comment, got %d", len(report.Dangling))
	}
	if report.Dangling[0].Comment.ID != "bad" {
		t.Fatalf("expected the dangling comment to be %q, got %q", "bad", report.Dangling[0].Comment.ID)
	}

	warnings := report.Warnings()
	if len(warnings) != 1 {
		t.Fatalf("expected exactly 1 warning, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "bad") || !strings.Contains(warnings[0], "step-99-deadbeef") {
		t.Fatalf("warning should name the comment and its target, got %q", warnings[0])
	}
	for _, w := range warnings {
		if strings.Contains(w, "good-1") || strings.Contains(w, "good-2") {
			t.Fatalf("valid comments must not be reported as dangling: %q", w)
		}
	}
}

// Summary verbosity strips source locations, so a source-anchored comment
// dangles — and the reason must say so rather than leaving the user guessing.
func TestValidateAnnotationRefs_ExplainsFilteredSourceLocations(t *testing.T) {
	tr := newAnnotatedTrace(t)
	comments := []ReviewerComment{
		comment("src", "alice", "on source", SourceTarget("src/lib.rs", 42)),
		comment("step", "bob", "on step", StepTarget(tr.StepID(0))),
	}

	filtered := FilterExecutionTrace(tr, VerbositySummary)
	report := ValidateAnnotationRefs(filtered, comments)

	if len(report.Dangling) != 1 {
		t.Fatalf("expected the source-anchored comment to dangle, got %d dangling", len(report.Dangling))
	}
	if !strings.Contains(report.Dangling[0].Reason, "verbosity") {
		t.Fatalf("reason should explain that filtering stripped source locations, got %q", report.Dangling[0].Reason)
	}
	// The step-anchored comment must still resolve — filtering must not break
	// every reference indiscriminately.
	if len(report.Resolved) != 1 || report.Resolved[0].Comment.ID != "step" {
		t.Fatalf("expected the step-anchored comment to still resolve, got %+v", report.Resolved)
	}
}

func TestValidateAnnotationRefs_NilTraceReportsAllAsDangling(t *testing.T) {
	report := ValidateAnnotationRefs(nil, []ReviewerComment{
		comment("c1", "alice", "body", TraceTarget()),
	})
	if len(report.Dangling) != 1 || len(report.Resolved) != 0 {
		t.Fatalf("expected every comment to dangle against a nil trace, got %+v", report)
	}
}

// --- attaching -------------------------------------------------------------

func TestAttachReviewerComments_IsIdempotentByID(t *testing.T) {
	tr := newAnnotatedTrace(t)
	first := comment("c1", "alice", "original", StepTarget(tr.StepID(0)))

	if _, err := tr.AttachReviewerComments([]ReviewerComment{first}); err != nil {
		t.Fatalf("first attach: %v", err)
	}
	updated := first
	updated.Body = "revised"
	updated.Resolution = ResolutionResolved
	if _, err := tr.AttachReviewerComments([]ReviewerComment{updated}); err != nil {
		t.Fatalf("second attach: %v", err)
	}

	if len(tr.Annotations.ReviewerComments) != 1 {
		t.Fatalf("expected re-import to replace rather than duplicate, got %d comments",
			len(tr.Annotations.ReviewerComments))
	}
	if got := tr.Annotations.ReviewerComments[0]; got.Body != "revised" || got.Resolution != ResolutionResolved {
		t.Fatalf("expected the comment to be updated in place, got %+v", got)
	}
}

func TestAttachReviewerComments_KeepsDanglingCommentsAndReportsThem(t *testing.T) {
	tr := newAnnotatedTrace(t)
	report, err := tr.AttachReviewerComments([]ReviewerComment{
		comment("bad", "alice", "broken", StepTarget("step-99-deadbeef")),
	})
	if err != nil {
		t.Fatalf("a dangling target must not fail the attach: %v", err)
	}
	if !report.HasDangling() {
		t.Fatal("expected the dangling target to be reported")
	}
	if len(tr.Annotations.ReviewerComments) != 1 {
		t.Fatal("a dangling comment must still be attached, otherwise review history is destroyed")
	}
}

func TestAttachReviewerComments_RejectsDuplicateIDsInOneImport(t *testing.T) {
	tr := newAnnotatedTrace(t)
	_, err := tr.AttachReviewerComments([]ReviewerComment{
		comment("dup", "alice", "one", TraceTarget()),
		comment("dup", "bob", "two", TraceTarget()),
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate comment id") {
		t.Fatalf("expected a duplicate-id error, got %v", err)
	}
	if len(tr.Annotations.ReviewerComments) != 0 {
		t.Fatal("a rejected import must not partially apply")
	}
}

func TestAttachReviewerComments_EnforcesCommentLimit(t *testing.T) {
	tr := newAnnotatedTrace(t)
	tooMany := make([]ReviewerComment, MaxTraceComments+1)
	for i := range tooMany {
		tooMany[i] = comment(strings.Repeat("c", 1)+string(rune('a'+i%26))+string(rune('0'+i/26)),
			"alice", "body", TraceTarget())
	}

	_, err := tr.AttachReviewerComments(tooMany)
	if err == nil || !strings.Contains(err.Error(), "too many reviewer comments") {
		t.Fatalf("expected the comment limit to be enforced, got %v", err)
	}
	if len(tr.Annotations.ReviewerComments) != 0 {
		t.Fatal("a rejected import must not partially apply")
	}
}

func TestAttachReviewerComments_RejectsInvalidCommentWithoutMutating(t *testing.T) {
	tr := newAnnotatedTrace(t)
	if _, err := tr.AttachReviewerComments([]ReviewerComment{
		comment("ok", "alice", "fine", TraceTarget()),
		comment("bad", "bob", "", TraceTarget()),
	}); err == nil {
		t.Fatal("expected an invalid comment to fail the import")
	}
	if len(tr.Annotations.ReviewerComments) != 0 {
		t.Fatal("a rejected import must not partially apply")
	}
}

// --- round trips -----------------------------------------------------------

// The headline acceptance criterion: annotations survive an export/import
// round trip unchanged.
func TestAnnotationFile_RoundTripPreservesEveryComment(t *testing.T) {
	tr := newAnnotatedTrace(t)
	original := []ReviewerComment{
		{
			ID: "c1", Author: "alice", Body: "double-check the auth path",
			Target: StepTarget(tr.StepID(1)), Severity: SeverityCritical,
			Resolution: ResolutionOpen, CreatedAt: fixedTime,
			UpdatedAt: fixedTime.Add(time.Hour),
		},
		{
			ID: "c2", Author: "bob", Body: "line 42 allocates on every call",
			Target: SourceTarget("src/lib.rs", 42), Severity: SeverityWarning,
			Resolution: ResolutionResolved, CreatedAt: fixedTime.Add(time.Minute),
		},
		{
			ID: "c3", Author: "carol", Body: "overall: looks fine",
			Target: TraceTarget(), Severity: SeverityInfo,
			Resolution: ResolutionWontFix, CreatedAt: fixedTime.Add(2 * time.Minute),
		},
		{
			ID: "c4", Author: "dave", Body: "anchor no longer exists",
			Target: StepTarget("step-99-deadbeef"), Severity: SeverityInfo,
			Resolution: ResolutionOpen, CreatedAt: fixedTime.Add(3 * time.Minute),
		},
	}
	if _, err := tr.AttachReviewerComments(original); err != nil {
		t.Fatalf("attach: %v", err)
	}

	path := filepath.Join(t.TempDir(), "review.json")
	file, err := ExportAnnotationFile(tr, fixedTime)
	if err != nil {
		t.Fatalf("ExportAnnotationFile: %v", err)
	}
	if err := file.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := LoadAnnotationFile(path)
	if err != nil {
		t.Fatalf("LoadAnnotationFile: %v", err)
	}
	if loaded.SchemaVersion != AnnotationFileSchemaVersion {
		t.Fatalf("expected schema version %q, got %q", AnnotationFileSchemaVersion, loaded.SchemaVersion)
	}
	if len(loaded.Comments) != len(original) {
		t.Fatalf("expected %d comments to survive the round trip, got %d",
			len(original), len(loaded.Comments))
	}

	// Including the one whose target dangles — a broken anchor must not cost
	// the reviewer their comment.
	got := map[string]ReviewerComment{}
	for _, c := range loaded.Comments {
		got[c.ID] = c
	}
	for _, want := range original {
		have, ok := got[want.ID]
		if !ok {
			t.Fatalf("comment %q did not survive the round trip", want.ID)
		}
		if have.Author != want.Author || have.Body != want.Body ||
			have.Severity != want.Severity || have.Resolution != want.Resolution ||
			have.Target != want.Target ||
			!have.CreatedAt.Equal(want.CreatedAt) || !have.UpdatedAt.Equal(want.UpdatedAt) {
			t.Errorf("comment %q changed across the round trip:\n want %+v\n  got %+v", want.ID, want, have)
		}
	}
}

// A second export of the same comments must produce identical bytes, otherwise
// annotation files churn in version control.
func TestAnnotationFile_RoundTripIsByteStable(t *testing.T) {
	tr := newAnnotatedTrace(t)
	if _, err := tr.AttachReviewerComments([]ReviewerComment{
		comment("c2", "bob", "second", SourceTarget("src/lib.rs", 42)),
		comment("c1", "alice", "first", StepTarget(tr.StepID(1))),
		comment("c3", "carol", "third", TraceTarget()),
	}); err != nil {
		t.Fatalf("attach: %v", err)
	}

	dir := t.TempDir()
	first := filepath.Join(dir, "a.json")
	file, err := ExportAnnotationFile(tr, fixedTime)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if err := file.Save(first); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Re-import into a fresh trace, then export again.
	reloaded := newAnnotatedTrace(t)
	loaded, err := LoadAnnotationFile(first)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, err := reloaded.AttachReviewerComments(loaded.Comments); err != nil {
		t.Fatalf("re-attach: %v", err)
	}
	second := filepath.Join(dir, "b.json")
	file2, err := ExportAnnotationFile(reloaded, fixedTime)
	if err != nil {
		t.Fatalf("second export: %v", err)
	}
	if err := file2.Save(second); err != nil {
		t.Fatalf("second save: %v", err)
	}

	a, _ := os.ReadFile(first)
	b, _ := os.ReadFile(second)
	if string(a) != string(b) {
		t.Fatalf("round trip is not byte-stable:\n--- first ---\n%s\n--- second ---\n%s", a, b)
	}
}

// An unedited comment must not carry a zero updated_at, which reads as a real
// edit timestamp; an edited one must keep it.
func TestReviewerComment_MarshalJSONOmitsUnsetUpdatedAt(t *testing.T) {
	unedited := comment("c1", "alice", "body", TraceTarget())
	data, err := json.Marshal(unedited)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), "updated_at") {
		t.Fatalf("expected updated_at to be omitted when unset, got %s", data)
	}

	edited := unedited
	edited.UpdatedAt = fixedTime.Add(time.Hour)
	data, err = json.Marshal(edited)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), `"updated_at":"2026-07-25T10:30:00Z"`) {
		t.Fatalf("expected updated_at to be written when set, got %s", data)
	}

	// And it must survive a decode.
	var back ReviewerComment
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !back.UpdatedAt.Equal(edited.UpdatedAt) {
		t.Fatalf("expected updated_at %v, got %v", edited.UpdatedAt, back.UpdatedAt)
	}
	if back.ID != "c1" || back.Author != "alice" || back.Target.Kind != TargetTrace {
		t.Fatalf("custom marshalling lost fields: %+v", back)
	}
}

func TestLoadAnnotationFile_AcceptsBareArray(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bare.json")
	body := `[{"id":"c1","author":"alice","body":"terse form","target":{"kind":"trace"},` +
		`"created_at":"2026-07-25T09:30:00Z"}]`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	file, err := LoadAnnotationFile(path)
	if err != nil {
		t.Fatalf("expected a bare array to load, got %v", err)
	}
	if len(file.Comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(file.Comments))
	}
	// Omitted severity and resolution must be defaulted, not left empty.
	if file.Comments[0].Severity != DefaultAnnotationSeverity {
		t.Errorf("expected default severity, got %q", file.Comments[0].Severity)
	}
	if file.Comments[0].Resolution != DefaultAnnotationResolution {
		t.Errorf("expected default resolution, got %q", file.Comments[0].Resolution)
	}
}

func TestLoadAnnotationFile_RejectsUnsupportedSchemaVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "future.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":"9.9","comments":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadAnnotationFile(path)
	if err == nil || !strings.Contains(err.Error(), "unsupported schema_version") {
		t.Fatalf("expected an unsupported-version error, got %v", err)
	}
}

func TestLoadAnnotationFile_EnforcesCommentLimit(t *testing.T) {
	comments := make([]map[string]interface{}, MaxTraceComments+1)
	for i := range comments {
		comments[i] = map[string]interface{}{
			"id": "c" + string(rune('a'+i%26)) + string(rune('0'+i/26)),
			"author": "alice", "body": "body",
			"target":     map[string]string{"kind": "trace"},
			"created_at": "2026-07-25T09:30:00Z",
		}
	}
	data, _ := json.Marshal(map[string]interface{}{
		"schema_version": AnnotationFileSchemaVersion, "comments": comments,
	})
	path := filepath.Join(t.TempDir(), "many.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadAnnotationFile(path)
	if err == nil || !strings.Contains(err.Error(), "maximum is") {
		t.Fatalf("expected the comment limit to be enforced at load time, got %v", err)
	}
}

func TestLoadAnnotationFile_ReportsInvalidEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.json")
	body := `{"schema_version":"1.0","comments":[{"id":"c1","author":"alice","body":"",` +
		`"target":{"kind":"trace"},"created_at":"2026-07-25T09:30:00Z"}]}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadAnnotationFile(path)
	if err == nil || !strings.Contains(err.Error(), "entry 0 is invalid") {
		t.Fatalf("expected the offending entry to be named, got %v", err)
	}
}

func TestLoadAnnotationFile_MissingFile(t *testing.T) {
	_, err := LoadAnnotationFile(filepath.Join(t.TempDir(), "nope.json"))
	if err == nil || !strings.Contains(err.Error(), "failed to read annotation file") {
		t.Fatalf("expected a read error naming the file, got %v", err)
	}
}

// --- annotations survive derived traces ------------------------------------

func TestAnnotationsSurviveFilteringAndAreDeepCopied(t *testing.T) {
	tr := newAnnotatedTrace(t)
	if _, err := tr.AttachReviewerComments([]ReviewerComment{
		comment("c1", "alice", "original body", StepTarget(tr.StepID(0))),
	}); err != nil {
		t.Fatalf("attach: %v", err)
	}

	filtered := FilterExecutionTrace(tr, VerbositySummary)
	if len(filtered.Annotations.ReviewerComments) != 1 {
		t.Fatal("expected reviewer comments to survive verbosity filtering")
	}

	// Mutating the filtered copy must not reach back into the original.
	filtered.Annotations.ReviewerComments[0].Body = "mutated"
	if tr.Annotations.ReviewerComments[0].Body != "original body" {
		t.Fatal("filtered trace shares annotation storage with the original")
	}
}

func TestAnnotationsSurviveMigrationAndAreDeepCopied(t *testing.T) {
	tr := newAnnotatedTrace(t)
	if _, err := tr.AttachReviewerComments([]ReviewerComment{
		comment("c1", "alice", "original body", TraceTarget()),
	}); err != nil {
		t.Fatalf("attach: %v", err)
	}

	migrated, err := migrateTrace(tr, TraceFormatVersion{Major: 1, Minor: 0}, TraceFormatVersion{Major: 1, Minor: 1})
	if err != nil {
		t.Fatalf("migrateTrace: %v", err)
	}
	if len(migrated.Annotations.ReviewerComments) != 1 {
		t.Fatal("expected reviewer comments to survive migration")
	}

	migrated.Annotations.ReviewerComments[0].Body = "mutated"
	if tr.Annotations.ReviewerComments[0].Body != "original body" {
		t.Fatal("migrated trace shares annotation storage with the original")
	}
}

func TestTraceAnnotations_CloneIsDeep(t *testing.T) {
	original := TraceAnnotations{
		Comments:         []string{"free form"},
		ReviewerComments: []ReviewerComment{comment("c1", "alice", "body", TraceTarget())},
		SessionMetadata:  map[string]string{"env": "testnet"},
	}
	clone := original.Clone()
	clone.Comments[0] = "changed"
	clone.ReviewerComments[0].Body = "changed"
	clone.SessionMetadata["env"] = "mainnet"

	if original.Comments[0] != "free form" ||
		original.ReviewerComments[0].Body != "body" ||
		original.SessionMetadata["env"] != "testnet" {
		t.Fatal("Clone must not share storage with the original")
	}
}

// A trace exported to JSON and read back must keep its reviewer comments.
func TestReviewerCommentsSurviveTraceJSONRoundTrip(t *testing.T) {
	tr := newAnnotatedTrace(t)
	if _, err := tr.AttachReviewerComments([]ReviewerComment{
		comment("c1", "alice", "keep me", StepTarget(tr.StepID(0))),
	}); err != nil {
		t.Fatalf("attach: %v", err)
	}

	path := filepath.Join(t.TempDir(), "trace.json")
	if err := tr.SaveToFile(path); err != nil {
		t.Fatalf("SaveToFile: %v", err)
	}
	loaded, err := LoadExecutionTrace(path)
	if err != nil {
		t.Fatalf("LoadExecutionTrace: %v", err)
	}

	if len(loaded.Annotations.ReviewerComments) != 1 {
		t.Fatalf("expected reviewer comments in the reloaded trace, got %d",
			len(loaded.Annotations.ReviewerComments))
	}
	got := loaded.Annotations.ReviewerComments[0]
	if got.ID != "c1" || got.Author != "alice" || got.Body != "keep me" {
		t.Fatalf("comment changed across the trace JSON round trip: %+v", got)
	}
	if loaded.AnnotationRefReport().HasDangling() {
		t.Fatal("expected the step target to still resolve after a trace round trip")
	}
}

// --- ordering --------------------------------------------------------------

func TestSortReviewerComments_OrdersByStepThenSeverity(t *testing.T) {
	tr := newAnnotatedTrace(t)
	step0, step1 := tr.StepID(0), tr.StepID(1)

	info := comment("info-step1", "alice", "b", StepTarget(step1))
	info.Severity = SeverityInfo
	critical := comment("critical-step1", "bob", "b", StepTarget(step1))
	critical.Severity = SeverityCritical
	first := comment("warn-step0", "carol", "b", StepTarget(step0))

	sorted := SortReviewerComments(tr, []ReviewerComment{info, critical, first})
	want := []string{"warn-step0", "critical-step1", "info-step1"}
	for i, id := range want {
		if sorted[i].ID != id {
			t.Fatalf("position %d: expected %q, got %q (full order: %v)",
				i, id, sorted[i].ID, idsOf(sorted))
		}
	}
}

func TestSortReviewerComments_NilTraceIsStable(t *testing.T) {
	comments := []ReviewerComment{
		comment("b", "alice", "x", TraceTarget()),
		comment("a", "bob", "x", TraceTarget()),
	}
	sorted := SortReviewerComments(nil, comments)
	if len(sorted) != 2 {
		t.Fatalf("expected both comments, got %d", len(sorted))
	}
	if sorted[0].ID != "a" || sorted[1].ID != "b" {
		t.Fatalf("expected a tie to break by ID, got %v", idsOf(sorted))
	}
}

func idsOf(comments []ReviewerComment) []string {
	out := make([]string, len(comments))
	for i, c := range comments {
		out[i] = c.ID
	}
	return out
}
