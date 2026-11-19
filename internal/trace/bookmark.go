// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package trace — stable, conflict-aware bookmarks anchored to trace steps.
//
// A Bookmark points at a specific step in a specific trace. Unlike a
// session-level "bookmark" (see internal/session, which is really just a
// human-readable name on a whole saved session), this Bookmark answers "which
// step" — and it has to keep answering that correctly after the trace is
// re-fetched, filtered, or migrated, none of which are guaranteed to leave a
// step at the same slice index.
//
// Two pieces of location evidence make that possible:
//
//   - TraceFingerprint — the whole-trace Fingerprint() at the moment the
//     bookmark was created. An exact match means nothing about the trace has
//     changed since.
//   - StepID — the StepIDOf() of the target step, which (like reviewer
//     comment targets, see reviewer_comments.go) survives verbosity
//     filtering and schema migration even when the trace fingerprint does
//     not. A bookmark whose fingerprint no longer matches but whose StepID
//     still resolves is reopening the same step after a nonsemantic trace
//     change — exactly the case this package exists to get right.
//
// If neither matches, the bookmark is reported dangling rather than silently
// resolved to a different step.
package trace

import (
	"fmt"
	"time"
)

// Bookmark points at a single step in a trace, with enough evidence to
// re-locate that step after the trace has been refreshed or filtered.
type Bookmark struct {
	// ID uniquely identifies this bookmark for merge/conflict purposes.
	ID string `json:"id"`
	// Name is the human-readable label shown to the user.
	Name string `json:"name"`
	// TraceFingerprint is ExecutionTrace.Fingerprint() at creation time.
	TraceFingerprint string `json:"trace_fingerprint"`
	// StepID is StepIDOf() for the target step — the durable anchor.
	StepID string `json:"step_id"`
	// TxHash is advisory provenance, like AnnotationFile.TransactionHash: it
	// helps an operator notice a bookmark being applied to an unrelated
	// trace, but it is never used to reject a resolve.
	TxHash string `json:"tx_hash,omitempty"`
	// CreatedAt records when the bookmark was made.
	CreatedAt time.Time `json:"created_at"`
}

// BookmarkStatus describes how a Bookmark resolved against a trace.
type BookmarkStatus string

const (
	// BookmarkResolvedExact means the trace's current Fingerprint matches
	// the bookmark's TraceFingerprint exactly: nothing has changed.
	BookmarkResolvedExact BookmarkStatus = "resolved_exact"
	// BookmarkResolvedByStepID means the trace fingerprint has changed
	// (e.g. a re-fetch or verbosity filter) but the target step's stable
	// StepID is still present, so the bookmark still points at the
	// semantically correct step.
	BookmarkResolvedByStepID BookmarkStatus = "resolved_by_step_id"
	// BookmarkDangling means neither the fingerprint nor the StepID
	// resolved: the target step is gone, or the bookmark belongs to a
	// different trace entirely.
	BookmarkDangling BookmarkStatus = "dangling"
)

// BookmarkResolution is the outcome of resolving one Bookmark against a
// trace.
type BookmarkResolution struct {
	Bookmark  Bookmark
	StepIndex int // index into ExecutionTrace.States, or -1 if dangling
	Status    BookmarkStatus
	Reason    string // set when Status == BookmarkDangling
}

// NewBookmark captures a bookmark at trace's current step stepIndex. It
// records both pieces of location evidence (fingerprint and step ID) needed
// to re-locate the step later.
func NewBookmark(id, name string, t *ExecutionTrace, stepIndex int) (Bookmark, error) {
	if t == nil {
		return Bookmark{}, fmt.Errorf("cannot bookmark a nil trace")
	}
	if stepIndex < 0 || stepIndex >= len(t.States) {
		return Bookmark{}, fmt.Errorf("step index %d is out of range (trace has %d step(s))", stepIndex, len(t.States))
	}
	return Bookmark{
		ID:               id,
		Name:             name,
		TraceFingerprint: t.Fingerprint(),
		StepID:           t.StepID(stepIndex),
		TxHash:           t.TransactionHash,
		CreatedAt:        time.Now().UTC(),
	}, nil
}

// buildBookmarkStepIndex indexes t's steps by stable StepID once, so
// resolving many bookmarks against the same trace is O(1) per bookmark
// rather than O(steps) — the same approach ValidateAnnotationRefs uses for
// reviewer comments.
func buildBookmarkStepIndex(t *ExecutionTrace) map[string]int {
	idx := make(map[string]int, len(t.States))
	for i := range t.States {
		id := StepIDOf(&t.States[i])
		if _, seen := idx[id]; !seen {
			idx[id] = i
		}
	}
	return idx
}

func resolveBookmarkWithIndex(t *ExecutionTrace, b Bookmark, stepIndex map[string]int) BookmarkResolution {
	idx, ok := stepIndex[b.StepID]
	if !ok {
		return BookmarkResolution{
			Bookmark: b, StepIndex: -1, Status: BookmarkDangling,
			Reason: "no step in the trace has this stable ID; the step may have been " +
				"removed, or the bookmark may belong to a different trace",
		}
	}
	if b.TraceFingerprint != "" && b.TraceFingerprint == t.Fingerprint() {
		return BookmarkResolution{Bookmark: b, StepIndex: idx, Status: BookmarkResolvedExact}
	}
	return BookmarkResolution{
		Bookmark: b, StepIndex: idx, Status: BookmarkResolvedByStepID,
		Reason: "trace fingerprint changed since the bookmark was created, but the target step's identity is still present",
	}
}

// ResolveBookmark locates b's target step in t. A nil trace always resolves
// dangling.
func ResolveBookmark(t *ExecutionTrace, b Bookmark) BookmarkResolution {
	if t == nil {
		return BookmarkResolution{Bookmark: b, StepIndex: -1, Status: BookmarkDangling, Reason: "trace is nil"}
	}
	return resolveBookmarkWithIndex(t, b, buildBookmarkStepIndex(t))
}

// ResolveBookmarks resolves every bookmark against t, sharing one step index
// across all of them.
func ResolveBookmarks(t *ExecutionTrace, bookmarks []Bookmark) []BookmarkResolution {
	out := make([]BookmarkResolution, 0, len(bookmarks))
	if t == nil {
		for _, b := range bookmarks {
			out = append(out, BookmarkResolution{Bookmark: b, StepIndex: -1, Status: BookmarkDangling, Reason: "trace is nil"})
		}
		return out
	}
	idx := buildBookmarkStepIndex(t)
	for _, b := range bookmarks {
		out = append(out, resolveBookmarkWithIndex(t, b, idx))
	}
	return out
}
