// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"strings"
	"testing"
	"time"
)

// renderAllFormats returns the HTML, Markdown, and plain-text renderings of a
// trace so each assertion can state what every shareable format must contain.
func renderAllFormats(t *testing.T, tr *ExecutionTrace) (html, md, text string) {
	t.Helper()
	var err error
	if html, err = GenerateTraceHTML(tr); err != nil {
		t.Fatalf("GenerateTraceHTML: %v", err)
	}
	if md, err = GenerateTraceMarkdown(tr); err != nil {
		t.Fatalf("GenerateTraceMarkdown: %v", err)
	}
	if text, err = GenerateTracePlainText(tr); err != nil {
		t.Fatalf("GenerateTracePlainText: %v", err)
	}
	return html, md, text
}

// The acceptance criterion for exports: a reader must be able to tell what
// each comment is about, in every format.
func TestExportAssociatesCommentWithItsStep(t *testing.T) {
	tr := newAnnotatedTrace(t)
	stepID := tr.StepID(1)
	c := comment("c1", "alice", "require_auth is called twice here", StepTarget(stepID))
	c.Severity = SeverityCritical
	if _, err := tr.AttachReviewerComments([]ReviewerComment{c}); err != nil {
		t.Fatalf("attach: %v", err)
	}

	html, md, text := renderAllFormats(t, tr)

	for name, out := range map[string]string{"html": html, "markdown": md, "text": text} {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(out, "require_auth is called twice here") {
				t.Error("comment body is missing from the export")
			}
			if !strings.Contains(out, "alice") {
				t.Error("comment author is missing from the export")
			}
			if !strings.Contains(out, "critical") {
				t.Error("comment severity is missing from the export")
			}
			if !strings.Contains(out, "open") {
				t.Error("comment resolution state is missing from the export")
			}
			if !strings.Contains(out, stepID) {
				t.Error("the step ID the comment targets is missing from the export")
			}
		})
	}

	// The comment must appear inside the section for the step it targets, not
	// merely somewhere in the document.
	stepHeading := strings.Index(md, "## Step 1:")
	nextHeading := strings.Index(md, "## Step 2:")
	body := strings.Index(md, "require_auth is called twice here")
	if stepHeading < 0 || nextHeading < 0 || body < 0 {
		t.Fatalf("markdown is missing expected sections (step1=%d step2=%d body=%d)",
			stepHeading, nextHeading, body)
	}
	if body < stepHeading || body > nextHeading {
		t.Error("markdown comment is not rendered within the section of the step it targets")
	}
}

func TestExportRendersSourceAnchoredComment(t *testing.T) {
	tr := newAnnotatedTrace(t)
	if _, err := tr.AttachReviewerComments([]ReviewerComment{
		comment("c1", "bob", "allocation on every call", SourceTarget("src/lib.rs", 42)),
	}); err != nil {
		t.Fatalf("attach: %v", err)
	}

	html, md, text := renderAllFormats(t, tr)
	for name, out := range map[string]string{"html": html, "markdown": md, "text": text} {
		if !strings.Contains(out, "src/lib.rs:42") {
			t.Errorf("%s: expected the source location to identify the comment target", name)
		}
		if !strings.Contains(out, "allocation on every call") {
			t.Errorf("%s: comment body is missing", name)
		}
	}
}

func TestExportRendersTraceLevelComment(t *testing.T) {
	tr := newAnnotatedTrace(t)
	if _, err := tr.AttachReviewerComments([]ReviewerComment{
		comment("c1", "carol", "overall this looks correct", TraceTarget()),
	}); err != nil {
		t.Fatalf("attach: %v", err)
	}

	html, md, text := renderAllFormats(t, tr)
	if !strings.Contains(md, "## Reviewer Comments (whole trace)") {
		t.Error("markdown is missing the trace-level comment section")
	}
	if !strings.Contains(html, "Reviewer comments on the whole trace") {
		t.Error("HTML is missing the trace-level comment section")
	}
	if !strings.Contains(text, "Reviewer comments (whole trace)") {
		t.Error("text is missing the trace-level comment section")
	}
	for name, out := range map[string]string{"html": html, "markdown": md, "text": text} {
		if !strings.Contains(out, "overall this looks correct") {
			t.Errorf("%s: trace-level comment body is missing", name)
		}
		if !strings.Contains(out, "whole trace") {
			t.Errorf("%s: trace-level target is not identified", name)
		}
	}
}

// A dangling comment must still be exported — with its broken anchor made
// visible — rather than dropped.
func TestExportRendersDanglingCommentsWithReason(t *testing.T) {
	tr := newAnnotatedTrace(t)
	if _, err := tr.AttachReviewerComments([]ReviewerComment{
		comment("good", "alice", "valid comment text", StepTarget(tr.StepID(0))),
		comment("bad", "bob", "orphaned comment text", StepTarget("step-99-deadbeef")),
	}); err != nil {
		t.Fatalf("attach: %v", err)
	}

	html, md, text := renderAllFormats(t, tr)
	for name, out := range map[string]string{"html": html, "markdown": md, "text": text} {
		if !strings.Contains(out, "orphaned comment text") {
			t.Errorf("%s: a dangling comment must still be exported", name)
		}
		if !strings.Contains(out, "valid comment text") {
			t.Errorf("%s: a dangling comment must not displace the valid ones", name)
		}
		if !strings.Contains(out, "step-99-deadbeef") {
			t.Errorf("%s: the unresolved target must be shown", name)
		}
		if !strings.Contains(strings.ToLower(out), "not found in this trace") {
			t.Errorf("%s: the export must explain why the target is unresolved", name)
		}
	}
}

func TestExportRendersStepIDSoCommentsCanBeAnchored(t *testing.T) {
	tr := newAnnotatedTrace(t)
	html, md, text := renderAllFormats(t, tr)
	want := tr.StepID(0)
	for name, out := range map[string]string{"html": html, "markdown": md, "text": text} {
		if !strings.Contains(out, want) {
			t.Errorf("%s: export must publish the stable step ID so reviewers can target it", name)
		}
	}
}

// Comment text must not be able to inject markup into the HTML export.
func TestExportEscapesCommentContentInHTML(t *testing.T) {
	tr := newAnnotatedTrace(t)
	c := comment("c1", "<script>alert(1)</script>", "<img src=x onerror=alert(2)>", TraceTarget())
	if _, err := tr.AttachReviewerComments([]ReviewerComment{c}); err != nil {
		t.Fatalf("attach: %v", err)
	}

	html, err := GenerateTraceHTML(tr)
	if err != nil {
		t.Fatalf("GenerateTraceHTML: %v", err)
	}
	if strings.Contains(html, "<script>alert(1)</script>") {
		t.Error("comment author was not HTML-escaped")
	}
	if strings.Contains(html, "<img src=x onerror=alert(2)>") {
		t.Error("comment body was not HTML-escaped")
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Error("expected the escaped author to still appear in the output")
	}
}

func TestExportWithNoCommentsOmitsCommentSections(t *testing.T) {
	tr := newAnnotatedTrace(t)
	html, md, text := renderAllFormats(t, tr)

	if strings.Contains(md, "## Reviewer Comments") {
		t.Error("markdown should not emit a comment section for a trace with no comments")
	}
	if strings.Contains(html, "Reviewer comments") {
		t.Error("HTML should not emit a comment section for a trace with no comments")
	}
	if strings.Contains(text, "Reviewer comments") {
		t.Error("text should not emit a comment section for a trace with no comments")
	}
}

// Export-time comments supplied via ExportOptions behave like --comment/--meta:
// merged for this export, matched by ID rather than duplicated.
func TestExportOptionsReviewerCommentsMergeByID(t *testing.T) {
	tr := newAnnotatedTrace(t)
	if _, err := tr.AttachReviewerComments([]ReviewerComment{
		comment("c1", "alice", "from the trace", TraceTarget()),
	}); err != nil {
		t.Fatalf("attach: %v", err)
	}

	md, err := GenerateTraceMarkdownWithOptions(tr, ExportOptions{
		ReviewerComments: []ReviewerComment{
			comment("c1", "alice", "overridden at export time", TraceTarget()),
			comment("c2", "bob", "added at export time", TraceTarget()),
		},
	})
	if err != nil {
		t.Fatalf("GenerateTraceMarkdownWithOptions: %v", err)
	}

	if strings.Contains(md, "from the trace") {
		t.Error("expected the export-time comment to replace the one with the same ID")
	}
	if !strings.Contains(md, "overridden at export time") {
		t.Error("expected the overriding comment to be rendered")
	}
	if !strings.Contains(md, "added at export time") {
		t.Error("expected the new export-time comment to be rendered")
	}
	// The trace itself must be unchanged — export options are per-export.
	if len(tr.Annotations.ReviewerComments) != 1 {
		t.Errorf("export options must not mutate the trace, got %d comments",
			len(tr.Annotations.ReviewerComments))
	}
}

// --- export-time validation ------------------------------------------------

func TestValidateTraceExportParams_EnforcesReviewerCommentLimit(t *testing.T) {
	tr := newAnnotatedTrace(t)
	tooMany := make([]ReviewerComment, MaxTraceComments+1)
	for i := range tooMany {
		tooMany[i] = comment(string(rune('a'+i%26))+string(rune('0'+i/26)), "alice", "body", TraceTarget())
	}

	err := ValidateTraceExportParams(tr, "markdown", "out.md", ExportOptions{ReviewerComments: tooMany})
	if err == nil || !strings.Contains(err.Error(), "too many reviewer comments") {
		t.Fatalf("expected the reviewer comment limit to be enforced, got %v", err)
	}
}

func TestValidateTraceExportParams_ReportsInvalidReviewerComment(t *testing.T) {
	tr := newAnnotatedTrace(t)
	err := ValidateTraceExportParams(tr, "markdown", "out.md", ExportOptions{
		ReviewerComments: []ReviewerComment{
			{ID: "c1", Author: "alice", Body: strings.Repeat("x", MaxCommentLength+1),
				Target: TraceTarget(), CreatedAt: fixedTime},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "reviewer comment #1 is invalid") {
		t.Fatalf("expected the offending comment to be named, got %v", err)
	}
}

func TestValidateTraceExportParams_AcceptsValidReviewerComments(t *testing.T) {
	tr := newAnnotatedTrace(t)
	err := ValidateTraceExportParams(tr, "markdown", "out.md", ExportOptions{
		ReviewerComments: []ReviewerComment{
			comment("c1", "alice", "fine", StepTarget(tr.StepID(0))),
		},
	})
	if err != nil {
		t.Fatalf("expected valid reviewer comments to pass validation, got %v", err)
	}
}

// Dangling anchors surface as export warnings, without failing the export.
func TestValidateFormatCompatibility_WarnsAboutDanglingAnnotations(t *testing.T) {
	tr := newAnnotatedTrace(t)
	if _, err := tr.AttachReviewerComments([]ReviewerComment{
		comment("bad", "bob", "orphan", StepTarget("step-99-deadbeef")),
	}); err != nil {
		t.Fatalf("attach: %v", err)
	}

	warnings := ValidateFormatCompatibility(tr, "markdown", DefaultCompatibilityOptions())
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "Dangling annotation") && strings.Contains(w, "step-99-deadbeef") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a dangling-annotation warning, got %v", warnings)
	}
}

func TestValidateFormatCompatibility_NoWarningWhenTargetsResolve(t *testing.T) {
	tr := newAnnotatedTrace(t)
	if _, err := tr.AttachReviewerComments([]ReviewerComment{
		comment("good", "alice", "fine", StepTarget(tr.StepID(0))),
	}); err != nil {
		t.Fatalf("attach: %v", err)
	}

	for _, w := range ValidateFormatCompatibility(tr, "markdown", DefaultCompatibilityOptions()) {
		if strings.Contains(w, "Dangling annotation") {
			t.Fatalf("did not expect a dangling warning, got %q", w)
		}
	}
}

// Exporting a filtered trace keeps every comment but flags the anchors that
// filtering broke — the end-to-end shape of the acceptance criteria.
func TestFilteredExportKeepsCommentsAndFlagsBrokenAnchors(t *testing.T) {
	tr := newAnnotatedTrace(t)
	if _, err := tr.AttachReviewerComments([]ReviewerComment{
		comment("src", "alice", "source anchored note", SourceTarget("src/lib.rs", 42)),
		comment("step", "bob", "step anchored note", StepTarget(tr.StepID(0))),
	}); err != nil {
		t.Fatalf("attach: %v", err)
	}

	filtered := FilterExecutionTrace(tr, VerbositySummary)
	md, err := GenerateTraceMarkdown(filtered)
	if err != nil {
		t.Fatalf("GenerateTraceMarkdown: %v", err)
	}

	if !strings.Contains(md, "source anchored note") {
		t.Error("filtering must not drop a comment whose anchor it broke")
	}
	if !strings.Contains(md, "step anchored note") {
		t.Error("filtering must not drop comments whose anchors still resolve")
	}
	if !strings.Contains(md, "Unresolved Targets") {
		t.Error("the export must call out the comment whose anchor no longer resolves")
	}

	report := filtered.AnnotationRefReport()
	if len(report.Dangling) != 1 || report.Dangling[0].Comment.ID != "src" {
		t.Fatalf("expected exactly the source-anchored comment to dangle, got %+v", report.Dangling)
	}
}

func TestExportedAnnotationFileRecordsProvenance(t *testing.T) {
	tr := newAnnotatedTrace(t)
	if _, err := tr.AttachReviewerComments([]ReviewerComment{
		comment("c1", "alice", "body", TraceTarget()),
	}); err != nil {
		t.Fatalf("attach: %v", err)
	}

	file, err := ExportAnnotationFile(tr, fixedTime)
	if err != nil {
		t.Fatalf("ExportAnnotationFile: %v", err)
	}
	// The raw transaction hash must never appear in a shareable artifact.
	if strings.Contains(file.TransactionHash, tr.TransactionHash) {
		t.Error("expected the transaction hash to be fingerprinted, not embedded raw")
	}
	if !strings.HasPrefix(file.TransactionHash, "sha256:") {
		t.Errorf("expected a sha256 fingerprint, got %q", file.TransactionHash)
	}
	if !file.GeneratedAt.Equal(fixedTime.UTC().Truncate(time.Second)) {
		t.Errorf("expected generated_at %v, got %v", fixedTime, file.GeneratedAt)
	}
}

func TestExportAnnotationFile_NilTrace(t *testing.T) {
	if _, err := ExportAnnotationFile(nil, fixedTime); err == nil {
		t.Fatal("expected an error for a nil trace")
	}
}

func TestExportAnnotationFile_EmptyTraceProducesEmptyList(t *testing.T) {
	tr := newAnnotatedTrace(t)
	file, err := ExportAnnotationFile(tr, fixedTime)
	if err != nil {
		t.Fatalf("ExportAnnotationFile: %v", err)
	}
	if file.Comments == nil {
		t.Fatal("expected an empty slice rather than nil so the JSON has a comments array")
	}
	if len(file.Comments) != 0 {
		t.Fatalf("expected no comments, got %d", len(file.Comments))
	}
}
