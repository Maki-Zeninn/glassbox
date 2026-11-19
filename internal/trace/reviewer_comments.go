// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package trace — reviewer comments anchored to stable trace targets.
//
// The original annotation model carried only free-form strings
// (TraceAnnotations.Comments), which meant a reviewer's note could not say
// *which* part of the execution it was about. This file adds a durable,
// portable comment model for collaborative debugging:
//
//   - ReviewerComment  — author, body, severity, resolution state, timestamps,
//                        and a target that points at a stable step ID, a
//                        source location, or the trace as a whole.
//   - AnnotationFile   — the portable on-disk envelope used by
//                        `glassbox trace --annotations` (import) and
//                        `--export-annotations` (export). Round-tripping a
//                        file through import and export preserves every
//                        comment verbatim.
//   - ValidateAnnotationRefs — resolves every target against a trace and
//                        reports the ones that no longer point anywhere.
//                        Dangling targets are reported, never silently
//                        dropped, so a filtered or migrated trace cannot lose
//                        review history.
//
// Why targets are content-derived
//
// A trace step's position in the slice is not a durable anchor: verbosity
// filtering rewrites state fields and schema migration copies states between
// envelope versions. StepIDOf therefore derives an identifier from the fields
// that survive both operations (step number, operation, event type, contract
// ID, and function). A comment written against a step keeps resolving after
// the trace is filtered or migrated; if the step's identity genuinely changed,
// the reference is reported as dangling rather than silently rebound to the
// wrong step.

package trace

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

// Comment limits. These bound the size of the collaboration metadata that
// travels with an exported artifact so a trace export cannot be turned into an
// unbounded payload by a malformed or hostile annotation file.
const (
	// MaxTraceComments is the maximum number of comments of a single kind
	// (free-form or reviewer) attached to one trace.
	MaxTraceComments = 100
	// MaxCommentLength is the maximum length in bytes of one comment body.
	MaxCommentLength = 10000
	// MaxCommentAuthorLength is the maximum length in bytes of an author name.
	MaxCommentAuthorLength = 128
	// MaxCommentIDLength is the maximum length in bytes of a comment ID.
	MaxCommentIDLength = 128
	// MaxSourceFileLength is the maximum length in bytes of a target source path.
	MaxSourceFileLength = 4096
)

// AnnotationTargetKind identifies what a reviewer comment is attached to.
type AnnotationTargetKind string

const (
	// TargetStep anchors a comment to a stable step ID (see StepIDOf).
	TargetStep AnnotationTargetKind = "step"
	// TargetSource anchors a comment to a source file and line number.
	TargetSource AnnotationTargetKind = "source"
	// TargetTrace anchors a comment to the trace as a whole. Trace-level
	// targets always resolve and are never reported as dangling.
	TargetTrace AnnotationTargetKind = "trace"
)

// AnnotationSeverity grades how much attention a comment demands.
type AnnotationSeverity string

const (
	SeverityInfo     AnnotationSeverity = "info"
	SeverityWarning  AnnotationSeverity = "warning"
	SeverityCritical AnnotationSeverity = "critical"
)

// AnnotationResolution tracks whether a comment has been dealt with.
type AnnotationResolution string

const (
	ResolutionOpen     AnnotationResolution = "open"
	ResolutionResolved AnnotationResolution = "resolved"
	ResolutionWontFix  AnnotationResolution = "wontfix"
)

// DefaultAnnotationSeverity and DefaultAnnotationResolution are applied by
// Normalize when a comment omits the field, so hand-written annotation files
// can stay terse.
const (
	DefaultAnnotationSeverity   = SeverityInfo
	DefaultAnnotationResolution = ResolutionOpen
)

// AnnotationTarget identifies the part of a trace a comment refers to.
//
// Exactly one anchor is populated per Kind: StepID for TargetStep, SourceFile
// and SourceLine for TargetSource, and none for TargetTrace.
type AnnotationTarget struct {
	Kind       AnnotationTargetKind `json:"kind"`
	StepID     string               `json:"step_id,omitempty"`
	SourceFile string               `json:"source_file,omitempty"`
	SourceLine int                  `json:"source_line,omitempty"`
}

// StepTarget returns a target anchored to the given stable step ID.
func StepTarget(stepID string) AnnotationTarget {
	return AnnotationTarget{Kind: TargetStep, StepID: stepID}
}

// SourceTarget returns a target anchored to a source file and line.
func SourceTarget(file string, line int) AnnotationTarget {
	return AnnotationTarget{Kind: TargetSource, SourceFile: file, SourceLine: line}
}

// TraceTarget returns a target anchored to the trace as a whole.
func TraceTarget() AnnotationTarget {
	return AnnotationTarget{Kind: TargetTrace}
}

// String renders the target in the form used by every exported artifact, so a
// reader can always tell what a comment is about.
func (t AnnotationTarget) String() string {
	switch t.Kind {
	case TargetStep:
		return "step " + t.StepID
	case TargetSource:
		return fmt.Sprintf("%s:%d", t.SourceFile, t.SourceLine)
	case TargetTrace:
		return "whole trace"
	default:
		return fmt.Sprintf("unknown target kind %q", string(t.Kind))
	}
}

// Validate reports whether the target is structurally well-formed. It does not
// check that the target resolves against any particular trace — that is
// ValidateAnnotationRefs' job.
func (t AnnotationTarget) Validate() error {
	switch t.Kind {
	case TargetStep:
		if strings.TrimSpace(t.StepID) == "" {
			return fmt.Errorf("target kind %q requires a non-empty step_id\n"+
				"  Fix: set target.step_id to a stable step ID from 'glassbox trace --export-annotations'", TargetStep)
		}
		if t.SourceFile != "" || t.SourceLine != 0 {
			return fmt.Errorf("target kind %q must not set source_file or source_line\n"+
				"  Fix: use kind %q to anchor a comment to a source location", TargetStep, TargetSource)
		}
	case TargetSource:
		if strings.TrimSpace(t.SourceFile) == "" {
			return fmt.Errorf("target kind %q requires a non-empty source_file\n"+
				"  Fix: set target.source_file to the path reported in the trace step", TargetSource)
		}
		if len(t.SourceFile) > MaxSourceFileLength {
			return fmt.Errorf("target source_file exceeds %d bytes (got %d)\n"+
				"  Fix: shorten the path or anchor the comment to a step ID instead",
				MaxSourceFileLength, len(t.SourceFile))
		}
		if t.SourceLine <= 0 {
			return fmt.Errorf("target kind %q requires a positive source_line (got %d)\n"+
				"  Fix: line numbers are 1-based; use kind %q for a whole-file note",
				TargetSource, t.SourceLine, TargetTrace)
		}
		if t.StepID != "" {
			return fmt.Errorf("target kind %q must not set step_id\n"+
				"  Fix: use kind %q to anchor a comment to a step", TargetSource, TargetStep)
		}
	case TargetTrace:
		if t.StepID != "" || t.SourceFile != "" || t.SourceLine != 0 {
			return fmt.Errorf("target kind %q must not set step_id, source_file, or source_line\n"+
				"  Fix: remove the anchor fields, or change kind to %q or %q",
				TargetTrace, TargetStep, TargetSource)
		}
	case "":
		return fmt.Errorf("target kind is empty\n"+
			"  Fix: set target.kind to one of %q, %q, or %q", TargetStep, TargetSource, TargetTrace)
	default:
		return fmt.Errorf("unknown target kind %q\n"+
			"  Fix: use one of %q, %q, or %q", string(t.Kind), TargetStep, TargetSource, TargetTrace)
	}
	return nil
}

// ReviewerComment is a durable review note attached to a trace.
//
// Every field is preserved verbatim across an import/export round trip so a
// comment thread can be handed between reviewers without losing history.
type ReviewerComment struct {
	// ID uniquely identifies the comment within a trace. Re-importing a
	// comment with an existing ID replaces it, which makes import idempotent.
	ID         string               `json:"id"`
	Target     AnnotationTarget     `json:"target"`
	Author     string               `json:"author"`
	Body       string               `json:"body"`
	Severity   AnnotationSeverity   `json:"severity"`
	Resolution AnnotationResolution `json:"resolution"`
	CreatedAt  time.Time            `json:"created_at"`
	UpdatedAt  time.Time            `json:"updated_at,omitempty"`
}

// MarshalJSON omits updated_at when the comment has never been edited.
//
// encoding/json's omitempty has no effect on time.Time — a struct is never
// considered empty — so without this an exported file would carry a misleading
// "updated_at": "0001-01-01T00:00:00Z" on every comment, which reads as a real
// edit timestamp to both humans and other tools.
func (c ReviewerComment) MarshalJSON() ([]byte, error) {
	// The alias sheds the method set, so this does not recurse.
	type alias ReviewerComment
	if c.UpdatedAt.IsZero() {
		// The outer field is shallower than the embedded one, so encoding/json
		// resolves the tag conflict in its favour and drops the nil pointer.
		return json.Marshal(struct {
			alias
			UpdatedAt *time.Time `json:"updated_at,omitempty"`
		}{alias: alias(c)})
	}
	return json.Marshal(alias(c))
}

// Normalize trims surrounding whitespace and fills in defaults for omitted
// severity and resolution, so hand-written annotation files stay terse and
// round trips are stable. Timestamps are converted to UTC so two exports of
// the same comment produce identical bytes regardless of the writer's zone.
func (c *ReviewerComment) Normalize() {
	c.ID = strings.TrimSpace(c.ID)
	c.Author = strings.TrimSpace(c.Author)
	c.Body = strings.TrimSpace(c.Body)
	c.Target.StepID = strings.TrimSpace(c.Target.StepID)
	c.Target.SourceFile = strings.TrimSpace(c.Target.SourceFile)

	if c.Severity == "" {
		c.Severity = DefaultAnnotationSeverity
	}
	if c.Resolution == "" {
		c.Resolution = DefaultAnnotationResolution
	}
	if c.Target.Kind == "" && c.Target.StepID == "" && c.Target.SourceFile == "" {
		c.Target.Kind = TargetTrace
	}
	if !c.CreatedAt.IsZero() {
		c.CreatedAt = c.CreatedAt.UTC()
	}
	if !c.UpdatedAt.IsZero() {
		c.UpdatedAt = c.UpdatedAt.UTC()
	}
}

// Validate reports whether the comment is well-formed and within limits.
// Structural validity is independent of any trace; see ValidateAnnotationRefs
// for target resolution.
func (c ReviewerComment) Validate() error {
	if strings.TrimSpace(c.ID) == "" {
		return fmt.Errorf("comment id is empty\n" +
			"  Fix: give every comment a unique non-empty \"id\" field")
	}
	if len(c.ID) > MaxCommentIDLength {
		return fmt.Errorf("comment id exceeds %d bytes (got %d)\n"+
			"  Fix: use a shorter identifier", MaxCommentIDLength, len(c.ID))
	}
	if strings.TrimSpace(c.Author) == "" {
		return fmt.Errorf("comment %q has an empty author\n"+
			"  Fix: set \"author\" so reviewers can be attributed", c.ID)
	}
	if len(c.Author) > MaxCommentAuthorLength {
		return fmt.Errorf("comment %q author exceeds %d bytes (got %d)\n"+
			"  Fix: use a shorter author name or handle", c.ID, MaxCommentAuthorLength, len(c.Author))
	}
	if strings.TrimSpace(c.Body) == "" {
		return fmt.Errorf("comment %q has an empty body\n"+
			"  Fix: provide comment text or remove the entry", c.ID)
	}
	if len(c.Body) > MaxCommentLength {
		return fmt.Errorf("comment %q exceeds the maximum length of %d characters (got %d)\n"+
			"  Fix: shorten the comment or split it across several entries",
			c.ID, MaxCommentLength, len(c.Body))
	}
	switch c.Severity {
	case SeverityInfo, SeverityWarning, SeverityCritical:
	default:
		return fmt.Errorf("comment %q has unknown severity %q\n"+
			"  Fix: use one of %q, %q, or %q",
			c.ID, string(c.Severity), SeverityInfo, SeverityWarning, SeverityCritical)
	}
	switch c.Resolution {
	case ResolutionOpen, ResolutionResolved, ResolutionWontFix:
	default:
		return fmt.Errorf("comment %q has unknown resolution %q\n"+
			"  Fix: use one of %q, %q, or %q",
			c.ID, string(c.Resolution), ResolutionOpen, ResolutionResolved, ResolutionWontFix)
	}
	if c.CreatedAt.IsZero() {
		return fmt.Errorf("comment %q has no created_at timestamp\n"+
			"  Fix: set \"created_at\" to an RFC 3339 timestamp, e.g. 2026-07-25T09:30:00Z", c.ID)
	}
	if !c.UpdatedAt.IsZero() && c.UpdatedAt.Before(c.CreatedAt) {
		return fmt.Errorf("comment %q has updated_at before created_at\n"+
			"  Fix: correct the timestamps; updated_at must not precede created_at", c.ID)
	}
	if err := c.Target.Validate(); err != nil {
		return fmt.Errorf("comment %q has an invalid target: %w", c.ID, err)
	}
	return nil
}

// StepIDOf returns a stable identifier for a single execution state.
//
// The identifier combines the recorded step number with a digest of the
// step's identifying fields — operation, event type, contract ID, and
// function. Those fields survive both verbosity filtering
// (FilterExecutionTrace strips payloads and source locations but never these)
// and schema migration, so a comment anchored to a step keeps resolving. If a
// step's identity genuinely changes the digest changes with it, and the
// reference is reported as dangling instead of silently rebinding to a
// different step.
//
// Field values are length-prefixed before hashing so that no combination of
// values can be rearranged into the same digest.
func StepIDOf(s *ExecutionState) string {
	if s == nil {
		return ""
	}
	h := sha256.New()
	fmt.Fprintf(h, "%d|", s.Step)
	for _, field := range []string{s.Operation, s.EventType, s.ContractID, s.Function} {
		fmt.Fprintf(h, "%d:%s|", len(field), field)
	}
	return fmt.Sprintf("step-%d-%s", s.Step, hex.EncodeToString(h.Sum(nil))[:8])
}

// StepID returns the stable identifier of the state at slice index i, or an
// empty string when i is out of range.
func (t *ExecutionTrace) StepID(i int) string {
	if t == nil || i < 0 || i >= len(t.States) {
		return ""
	}
	return StepIDOf(&t.States[i])
}

// ResolvedAnnotation pairs a comment with the trace position its target
// resolved to. StepIndex is the index into ExecutionTrace.States, or -1 for a
// trace-level comment that is not anchored to any single step.
type ResolvedAnnotation struct {
	Comment   ReviewerComment
	StepIndex int
}

// DanglingAnnotation is a comment whose target no longer resolves against the
// trace, together with a human-readable explanation of why.
type DanglingAnnotation struct {
	Comment ReviewerComment
	Reason  string
}

// AnnotationRefReport is the outcome of resolving a set of comments against a
// trace. Resolved and Dangling together always account for every input comment
// — resolution never discards a comment.
type AnnotationRefReport struct {
	Resolved []ResolvedAnnotation
	Dangling []DanglingAnnotation
}

// HasDangling reports whether any target failed to resolve.
func (r AnnotationRefReport) HasDangling() bool { return len(r.Dangling) > 0 }

// Warnings renders each dangling reference as an operator-facing message.
// Valid comments are never mentioned, so an empty result means every target
// resolved.
func (r AnnotationRefReport) Warnings() []string {
	if len(r.Dangling) == 0 {
		return nil
	}
	out := make([]string, 0, len(r.Dangling))
	for _, d := range r.Dangling {
		out = append(out, fmt.Sprintf(
			"annotation %q by %s targets %s which is not present in this trace: %s",
			d.Comment.ID, d.Comment.Author, d.Comment.Target.String(), d.Reason))
	}
	return out
}

// ValidateAnnotationRefs resolves every comment's target against the trace.
//
// Comments whose targets resolve are returned in Resolved with the matching
// step index; comments whose targets do not are returned in Dangling with a
// reason. No comment is dropped by this function, and a dangling reference
// never invalidates the comments around it — that is what lets a filtered or
// migrated trace report broken anchors while keeping the rest of the review
// history intact.
//
// A source-location target that matches several steps resolves to the first
// matching step, which is the earliest point in execution the reviewer could
// have been looking at.
func ValidateAnnotationRefs(t *ExecutionTrace, comments []ReviewerComment) AnnotationRefReport {
	report := AnnotationRefReport{}
	if len(comments) == 0 {
		return report
	}
	if t == nil {
		for _, c := range comments {
			report.Dangling = append(report.Dangling, DanglingAnnotation{
				Comment: c,
				Reason:  "trace is nil",
			})
		}
		return report
	}

	// Index the trace once; both lookups below are then O(1) per comment.
	stepIndex := make(map[string]int, len(t.States))
	sourceIndex := make(map[string]int, len(t.States))
	sourcesStripped := len(t.States) > 0
	for i := range t.States {
		if _, seen := stepIndex[StepIDOf(&t.States[i])]; !seen {
			stepIndex[StepIDOf(&t.States[i])] = i
		}
		if t.States[i].SourceFile != "" {
			sourcesStripped = false
			key := sourceKey(t.States[i].SourceFile, t.States[i].SourceLine)
			if _, seen := sourceIndex[key]; !seen {
				sourceIndex[key] = i
			}
		}
	}

	for _, c := range comments {
		switch c.Target.Kind {
		case TargetTrace:
			report.Resolved = append(report.Resolved, ResolvedAnnotation{Comment: c, StepIndex: -1})
		case TargetStep:
			if idx, ok := stepIndex[c.Target.StepID]; ok {
				report.Resolved = append(report.Resolved, ResolvedAnnotation{Comment: c, StepIndex: idx})
				continue
			}
			report.Dangling = append(report.Dangling, DanglingAnnotation{
				Comment: c,
				Reason: "no step in the trace has this stable ID; the step may have been " +
					"removed, or the comment may belong to a different trace",
			})
		case TargetSource:
			if idx, ok := sourceIndex[sourceKey(c.Target.SourceFile, c.Target.SourceLine)]; ok {
				report.Resolved = append(report.Resolved, ResolvedAnnotation{Comment: c, StepIndex: idx})
				continue
			}
			reason := "no step in the trace maps to this source location"
			if sourcesStripped {
				reason = "source locations were stripped from this trace (verbosity 'summary' " +
					"removes source_file and source_line); re-run with --trace-verbosity normal " +
					"to resolve source-anchored comments"
			}
			report.Dangling = append(report.Dangling, DanglingAnnotation{Comment: c, Reason: reason})
		default:
			report.Dangling = append(report.Dangling, DanglingAnnotation{
				Comment: c,
				Reason:  fmt.Sprintf("unknown target kind %q", string(c.Target.Kind)),
			})
		}
	}
	return report
}

func sourceKey(file string, line int) string {
	return fmt.Sprintf("%s:%d", file, line)
}

// AnnotationRefReport resolves the trace's own reviewer comments against its
// current states. Callers use it after filtering or migrating a trace to learn
// which anchors no longer point anywhere.
func (t *ExecutionTrace) AnnotationRefReport() AnnotationRefReport {
	if t == nil {
		return AnnotationRefReport{}
	}
	return ValidateAnnotationRefs(t, t.Annotations.ReviewerComments)
}

// AttachReviewerComments validates and merges comments into the trace.
//
// Comments are matched by ID: an incoming comment with an ID already present
// replaces the existing one, which makes repeated imports idempotent. The
// returned report describes which targets resolved and which are dangling.
//
// A dangling target is *not* an error and does not prevent the comment from
// being attached — dropping it would silently destroy review history that is
// still valid in the trace it was written against. Callers that want dangling
// references to be fatal check report.HasDangling themselves.
//
// An error is returned only for structurally invalid comments or when the
// merge would exceed MaxTraceComments; in that case the trace is left
// untouched so a bad import cannot partially apply.
func (t *ExecutionTrace) AttachReviewerComments(comments []ReviewerComment) (AnnotationRefReport, error) {
	if t == nil {
		return AnnotationRefReport{}, fmt.Errorf("cannot attach comments to a nil trace\n" +
			"  Fix: load the trace before importing annotations")
	}

	normalized := make([]ReviewerComment, len(comments))
	copy(normalized, comments)
	seen := make(map[string]int, len(normalized))
	for i := range normalized {
		normalized[i].Normalize()
		if err := normalized[i].Validate(); err != nil {
			return AnnotationRefReport{}, fmt.Errorf("annotation %d is invalid: %w", i, err)
		}
		if first, dup := seen[normalized[i].ID]; dup {
			return AnnotationRefReport{}, fmt.Errorf(
				"duplicate comment id %q at entries %d and %d\n"+
					"  Fix: give every comment in a single file a unique id",
				normalized[i].ID, first, i)
		}
		seen[normalized[i].ID] = i
	}

	// Merge into a copy so a limit failure cannot leave the trace half-updated.
	merged := append([]ReviewerComment(nil), t.Annotations.ReviewerComments...)
	position := make(map[string]int, len(merged))
	for i, existing := range merged {
		position[existing.ID] = i
	}
	for _, c := range normalized {
		if i, ok := position[c.ID]; ok {
			merged[i] = c
			continue
		}
		position[c.ID] = len(merged)
		merged = append(merged, c)
	}

	if len(merged) > MaxTraceComments {
		return AnnotationRefReport{}, fmt.Errorf(
			"too many reviewer comments (%d) — maximum is %d per trace\n"+
				"  The trace already carries %d comment(s) and the import adds %d new one(s)\n"+
				"  Fix: resolve and remove obsolete comments, or split the review across traces",
			len(merged), MaxTraceComments, len(t.Annotations.ReviewerComments),
			len(merged)-len(t.Annotations.ReviewerComments))
	}

	t.Annotations.ReviewerComments = merged
	if t.Annotations.GeneratedAt.IsZero() && len(merged) > 0 {
		t.Annotations.GeneratedAt = time.Now().UTC()
	}
	return ValidateAnnotationRefs(t, merged), nil
}

// SortReviewerComments orders comments deterministically for export: by step
// position where known, then by severity (most severe first), then by
// creation time, then by ID. Deterministic ordering is what makes an
// export/import/export cycle byte-stable.
func SortReviewerComments(t *ExecutionTrace, comments []ReviewerComment) []ReviewerComment {
	out := append([]ReviewerComment(nil), comments...)
	rank := map[string]int{}
	// Comments with no resolvable position sort after every positioned one.
	unresolvedRank := 1
	if t != nil {
		unresolvedRank = len(t.States) + 1
		report := ValidateAnnotationRefs(t, out)
		for _, r := range report.Resolved {
			rank[r.Comment.ID] = r.StepIndex
		}
	}
	severityRank := map[AnnotationSeverity]int{
		SeverityCritical: 0,
		SeverityWarning:  1,
		SeverityInfo:     2,
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, oki := rank[out[i].ID]
		rj, okj := rank[out[j].ID]
		if !oki {
			ri = unresolvedRank
		}
		if !okj {
			rj = unresolvedRank
		}
		if ri != rj {
			return ri < rj
		}
		si, sj := severityRank[out[i].Severity], severityRank[out[j].Severity]
		if si != sj {
			return si < sj
		}
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// AnnotationFileSchemaVersion is the schema_version written into exported
// annotation files. Importers reject versions they do not understand rather
// than guessing at the contents.
const AnnotationFileSchemaVersion = "1.0"

// SupportedAnnotationFileVersions lists every schema_version this build can
// import.
var SupportedAnnotationFileVersions = []string{"1.0"}

// AnnotationFile is the portable envelope exchanged by `glassbox trace
// --annotations` (import) and `--export-annotations` (export).
//
// TransactionHash is advisory provenance: it records which trace the comments
// were written against so an operator can tell when a file is being applied to
// an unrelated trace. It is never used to reject an import, because applying
// review notes to a re-run of the same transaction is a legitimate workflow.
type AnnotationFile struct {
	SchemaVersion   string            `json:"schema_version"`
	GeneratedAt     time.Time         `json:"generated_at"`
	TransactionHash string            `json:"transaction_hash,omitempty"`
	Comments        []ReviewerComment `json:"comments"`
	// Bookmarks are step-anchored bookmarks exchanged alongside comments
	// [Issue #562]. Omitted from the bare-array file shape; a file written
	// before bookmarks existed simply has none.
	Bookmarks []Bookmark `json:"bookmarks,omitempty"`
}

// LoadAnnotationFile reads a portable annotation file from path.
//
// Two shapes are accepted so that hand-written files stay convenient:
//
//	{"schema_version": "1.0", "comments": [ ... ]}   — the exported envelope
//	[ ... ]                                          — a bare comment array
//
// A bare array is treated as the current schema version. Every comment is
// normalized and validated; the first invalid entry fails the load with a
// message naming the offending comment, since a partially-applied annotation
// file is worse than none.
func LoadAnnotationFile(path string) (*AnnotationFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read annotation file %q: %w\n"+
			"  Fix: check the path and that the file is readable\n"+
			"  Tip: produce one with 'glassbox trace --export-annotations <file> <trace>'",
			path, err)
	}

	file := &AnnotationFile{}
	trimmed := strings.TrimLeft(string(data), " \t\r\n")
	if strings.HasPrefix(trimmed, "[") {
		var bare []ReviewerComment
		if err := json.Unmarshal(data, &bare); err != nil {
			return nil, fmt.Errorf("failed to parse annotation file %q as a comment array: %w\n"+
				"  Fix: check the JSON syntax, or use the envelope form "+
				"{\"schema_version\": %q, \"comments\": [...]}",
				path, err, AnnotationFileSchemaVersion)
		}
		file.SchemaVersion = AnnotationFileSchemaVersion
		file.Comments = bare
	} else {
		if err := json.Unmarshal(data, file); err != nil {
			return nil, fmt.Errorf("failed to parse annotation file %q: %w\n"+
				"  Fix: the file must be a JSON object with \"schema_version\" and \"comments\", "+
				"or a bare JSON array of comments",
				path, err)
		}
		if file.SchemaVersion == "" {
			file.SchemaVersion = AnnotationFileSchemaVersion
		}
		if !isAnnotationVersionSupported(file.SchemaVersion) {
			return nil, fmt.Errorf("annotation file %q has unsupported schema_version %q\n"+
				"  Supported versions: %s\n"+
				"  Fix: upgrade Glassbox, or re-export the annotations with this build",
				path, file.SchemaVersion, strings.Join(SupportedAnnotationFileVersions, ", "))
		}
	}

	if len(file.Comments) > MaxTraceComments {
		return nil, fmt.Errorf("annotation file %q contains %d comments — maximum is %d\n"+
			"  Fix: split the review across several annotation files",
			path, len(file.Comments), MaxTraceComments)
	}

	for i := range file.Comments {
		file.Comments[i].Normalize()
		if err := file.Comments[i].Validate(); err != nil {
			return nil, fmt.Errorf("annotation file %q entry %d is invalid: %w", path, i, err)
		}
	}
	return file, nil
}

func isAnnotationVersionSupported(v string) bool {
	for _, s := range SupportedAnnotationFileVersions {
		if s == v {
			return true
		}
	}
	return false
}

// Save writes the annotation file to path as indented JSON, creating parent
// directories as needed.
func (f *AnnotationFile) Save(path string) error {
	if f == nil {
		return fmt.Errorf("cannot save a nil annotation file")
	}
	if f.SchemaVersion == "" {
		f.SchemaVersion = AnnotationFileSchemaVersion
	}
	if f.Comments == nil {
		f.Comments = []ReviewerComment{}
	}

	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal annotation file: %w", err)
	}
	data = append(data, '\n')

	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
			return fmt.Errorf("failed to create parent directory for annotation file %q: %w\n"+
				"  Fix: ensure you have write permissions for the directory", path, mkErr)
		}
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("failed to write annotation file %q: %w\n"+
			"  Fix: check disk space and file permissions", path, err)
	}
	return nil
}

// ExportAnnotationFile builds a portable annotation file from a trace's
// reviewer comments.
//
// Comments are emitted in the deterministic order defined by
// SortReviewerComments, and the trace's transaction hash is recorded as
// provenance. Exporting and re-importing yields exactly the comments that went
// in, including ones whose targets are currently dangling — a broken anchor is
// reported, not destroyed.
func ExportAnnotationFile(t *ExecutionTrace, generatedAt time.Time) (*AnnotationFile, error) {
	if t == nil {
		return nil, fmt.Errorf("cannot export annotations from a nil trace\n" +
			"  Fix: load the trace before exporting annotations")
	}
	comments := SortReviewerComments(t, t.Annotations.ReviewerComments)
	if comments == nil {
		comments = []ReviewerComment{}
	}
	return &AnnotationFile{
		SchemaVersion:   AnnotationFileSchemaVersion,
		GeneratedAt:     generatedAt.UTC().Truncate(time.Second),
		TransactionHash: fingerprintTxHash(t.TransactionHash),
		Comments:        comments,
		Bookmarks:       append([]Bookmark(nil), t.Annotations.Bookmarks...),
	}, nil
}

// Clone returns a deep copy of the annotations.
//
// TraceAnnotations contains slices and a map, so copying the struct alone
// leaves the copy aliasing the original's backing storage. Operations that
// derive a new trace from an existing one — FilterExecutionTrace and schema
// migration — use this so that annotating the derived trace cannot mutate the
// trace it came from.
func (a TraceAnnotations) Clone() TraceAnnotations {
	out := a
	if a.Comments != nil {
		out.Comments = append([]string(nil), a.Comments...)
	}
	if a.ReviewerComments != nil {
		out.ReviewerComments = append([]ReviewerComment(nil), a.ReviewerComments...)
	}
	if a.SessionMetadata != nil {
		out.SessionMetadata = make(map[string]string, len(a.SessionMetadata))
		for k, v := range a.SessionMetadata {
			out.SessionMetadata[k] = v
		}
	}
	if a.Notes != nil {
		out.Notes = cloneNotes(a.Notes)
	}
	return out
}
