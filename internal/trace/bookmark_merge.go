// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"fmt"
	"strings"
)

// BookmarkConflictPolicy selects how MergeBookmarks resolves an incoming
// bookmark whose identity collides with an existing one. Named after, and
// with the same values as, session.ImportConflictPolicy for CLI consistency
// — kept as a distinct type here so this package does not import internal/session.
type BookmarkConflictPolicy string

const (
	// BookmarkImportFail rejects the merge outright when any conflict is
	// found, listing every one. This is the default, non-destructive
	// policy: it never overwrites or deletes an existing bookmark.
	BookmarkImportFail BookmarkConflictPolicy = "fail"
	// BookmarkImportRename keeps both bookmarks by assigning the incoming
	// one a fresh, non-colliding ID.
	BookmarkImportRename BookmarkConflictPolicy = "rename"
	// BookmarkImportMerge also keeps both bookmarks under distinct
	// identities. There is no sensible way to merge two bookmarks that
	// point at genuinely different steps into one, so — like rename — it
	// never silently picks a winner; it exists as a distinct, explicit
	// choice for callers that want a semantically different verb for "keep
	// both" than "rename."
	BookmarkImportMerge BookmarkConflictPolicy = "merge"
)

// ParseBookmarkConflictPolicy validates a user-supplied policy string (e.g.
// from a --bookmarks-on-conflict CLI flag).
func ParseBookmarkConflictPolicy(s string) (BookmarkConflictPolicy, error) {
	switch BookmarkConflictPolicy(strings.ToLower(strings.TrimSpace(s))) {
	case BookmarkImportFail, "":
		return BookmarkImportFail, nil
	case BookmarkImportRename:
		return BookmarkImportRename, nil
	case BookmarkImportMerge:
		return BookmarkImportMerge, nil
	default:
		return "", fmt.Errorf(
			"unknown bookmark conflict policy %q — must be one of: fail, rename, merge", s,
		)
	}
}

// BookmarkConflict describes one incoming bookmark that collides with an
// existing one by identity (ID or, failing that, Name) but targets a
// different place in the trace.
type BookmarkConflict struct {
	Existing Bookmark
	Incoming Bookmark
	Reason   string
}

// bookmarkKey returns the identity a bookmark is matched by: its ID if set,
// otherwise its Name.
func bookmarkKey(b Bookmark) string {
	if b.ID != "" {
		return b.ID
	}
	return b.Name
}

// findByKey returns the existing bookmark sharing in's identity, or nil.
func findByKey(existing []Bookmark, in Bookmark) *Bookmark {
	key := bookmarkKey(in)
	if key == "" {
		return nil
	}
	for i := range existing {
		if bookmarkKey(existing[i]) == key {
			return &existing[i]
		}
	}
	return nil
}

// DetectBookmarkConflicts compares incoming bookmarks against existing ones.
// Two bookmarks sharing an identity that resolve to the *same* step in t are
// a safe duplicate (e.g. re-importing the same export after the trace was
// refreshed) and are not reported as conflicts — see MergeBookmarks, which
// deduplicates them automatically. A conflict is reported only when the
// identities collide but the targets genuinely differ, including the case
// where one side's target has been deleted from the trace and the other's
// has not.
func DetectBookmarkConflicts(t *ExecutionTrace, existing, incoming []Bookmark) []BookmarkConflict {
	var conflicts []BookmarkConflict
	for _, in := range incoming {
		ex := findByKey(existing, in)
		if ex == nil {
			continue
		}
		exRes := ResolveBookmark(t, *ex)
		inRes := ResolveBookmark(t, in)
		if exRes.Status != BookmarkDangling && inRes.Status != BookmarkDangling && exRes.StepIndex == inRes.StepIndex {
			continue
		}
		conflicts = append(conflicts, BookmarkConflict{
			Existing: *ex,
			Incoming: in,
			Reason:   describeBookmarkConflict(exRes, inRes),
		})
	}
	return conflicts
}

func describeBookmarkConflict(existing, incoming BookmarkResolution) string {
	switch {
	case existing.Status == BookmarkDangling && incoming.Status == BookmarkDangling:
		return "both the existing and incoming bookmark's targets are missing from this trace"
	case existing.Status == BookmarkDangling:
		return "the existing bookmark's target is missing from this trace, but the incoming one resolves — likely the step was deleted and re-added"
	case incoming.Status == BookmarkDangling:
		return "the incoming bookmark's target does not exist in this trace"
	default:
		return fmt.Sprintf("existing bookmark points at step %d, incoming bookmark points at step %d",
			existing.StepIndex, incoming.StepIndex)
	}
}

// BookmarkMergeResult summarizes the outcome of MergeBookmarks.
type BookmarkMergeResult struct {
	Policy BookmarkConflictPolicy
	// Conflicts lists every identity collision found, regardless of policy.
	Conflicts []BookmarkConflict
	// Merged is the final bookmark set. Empty when the fail policy rejected
	// the merge.
	Merged []Bookmark
	// Renamed lists incoming bookmarks that were kept under a new ID to
	// avoid a collision (rename and merge policies only).
	Renamed []Bookmark
}

// MergeBookmarks combines existing and incoming bookmarks under policy.
//
// A bookmark that is a safe duplicate of an existing one (same identity,
// same resolved target) is merged without comment. A genuine conflict
// (same identity, different or dangling target) is handled per policy:
// fail rejects the whole merge and changes nothing; rename and merge both
// keep every bookmark by giving the incoming one a fresh ID — the existing
// bookmark is never overwritten or deleted by either policy.
func MergeBookmarks(t *ExecutionTrace, existing, incoming []Bookmark, policy BookmarkConflictPolicy) ([]Bookmark, *BookmarkMergeResult, error) {
	conflicts := DetectBookmarkConflicts(t, existing, incoming)
	result := &BookmarkMergeResult{Policy: policy, Conflicts: conflicts}

	if len(conflicts) > 0 && (policy == BookmarkImportFail || policy == "") {
		return existing, result, formatBookmarkConflictError(conflicts)
	}

	conflictKeys := make(map[string]bool, len(conflicts))
	for _, c := range conflicts {
		conflictKeys[bookmarkKey(c.Incoming)] = true
	}

	takenKeys := make(map[string]bool, len(existing))
	for _, b := range existing {
		if k := bookmarkKey(b); k != "" {
			takenKeys[k] = true
		}
	}

	merged := append([]Bookmark(nil), existing...)
	for _, in := range incoming {
		key := bookmarkKey(in)

		if conflictKeys[key] {
			renamed := in
			renamed.ID = uniqueBookmarkID(key, takenKeys)
			takenKeys[renamed.ID] = true
			merged = append(merged, renamed)
			result.Renamed = append(result.Renamed, renamed)
			continue
		}

		// Not a conflict: either a genuinely new bookmark, or a safe
		// duplicate of one already present (same identity, same resolved
		// target) — either way, don't add a second copy of an identity
		// that's already in merged.
		if key != "" && takenKeys[key] {
			continue
		}
		merged = append(merged, in)
		if key != "" {
			takenKeys[key] = true
		}
	}

	result.Merged = merged
	return merged, result, nil
}

// uniqueBookmarkID derives a fresh identity for an incoming bookmark that
// collided with base, guaranteed not to be in taken.
func uniqueBookmarkID(base string, taken map[string]bool) string {
	if base == "" {
		base = "bookmark"
	}
	candidate := base + "-imported"
	for i := 2; taken[candidate]; i++ {
		candidate = fmt.Sprintf("%s-imported-%d", base, i)
	}
	return candidate
}

// formatBookmarkConflictError renders bookmark conflicts in the same
// numbered-list, actionable style used elsewhere for import conflicts (see
// internal/session/import_conflict.go).
func formatBookmarkConflictError(conflicts []BookmarkConflict) error {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("bookmark import conflict: %d bookmark(s) collide with existing ones:\n", len(conflicts)))
	for i, c := range conflicts {
		sb.WriteString(fmt.Sprintf("  %d. %q vs existing %q: %s\n", i+1, c.Incoming.Name, c.Existing.Name, c.Reason))
	}
	sb.WriteString("Re-run with --bookmarks-on-conflict rename or --bookmarks-on-conflict merge to keep both.")
	return fmt.Errorf("%s", sb.String())
}
