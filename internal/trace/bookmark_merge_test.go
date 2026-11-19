// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── ParseBookmarkConflictPolicy ───────────────────────────────────────────

func TestParseBookmarkConflictPolicy(t *testing.T) {
	p, err := ParseBookmarkConflictPolicy("fail")
	require.NoError(t, err)
	assert.Equal(t, BookmarkImportFail, p)

	p, err = ParseBookmarkConflictPolicy("")
	require.NoError(t, err)
	assert.Equal(t, BookmarkImportFail, p)

	p, err = ParseBookmarkConflictPolicy("RENAME")
	require.NoError(t, err)
	assert.Equal(t, BookmarkImportRename, p)

	_, err = ParseBookmarkConflictPolicy("bogus")
	require.Error(t, err)
}

// ── Duplicate targets: safe, auto-deduped, no conflict ────────────────────

func TestDetectBookmarkConflicts_DuplicateTarget_NotAConflict(t *testing.T) {
	tr := bookmarkableTrace("tx1", 5)
	b, err := NewBookmark("bm1", "entry", tr, 2)
	require.NoError(t, err)

	// Re-import the exact same bookmark (e.g. re-exported after a
	// nonsemantic trace refresh): same ID, same resolved target.
	incoming := b
	incoming.TraceFingerprint = "different-fingerprint-but-same-step"

	conflicts := DetectBookmarkConflicts(tr, []Bookmark{b}, []Bookmark{incoming})
	assert.Empty(t, conflicts, "two bookmarks resolving to the same step must not be reported as a conflict")
}

func TestMergeBookmarks_DuplicateTarget_Deduplicated(t *testing.T) {
	tr := bookmarkableTrace("tx1", 5)
	b, err := NewBookmark("bm1", "entry", tr, 2)
	require.NoError(t, err)

	incoming := b
	incoming.TraceFingerprint = "stale-fingerprint"

	merged, result, err := MergeBookmarks(tr, []Bookmark{b}, []Bookmark{incoming}, BookmarkImportFail)
	require.NoError(t, err)
	assert.Empty(t, result.Conflicts)
	assert.Len(t, merged, 1, "duplicate bookmark must not be added a second time")
}

// ── Deleted target: flagged, never silently dropped ───────────────────────

func TestDetectBookmarkConflicts_DeletedTarget_Flagged(t *testing.T) {
	tr := bookmarkableTrace("tx1", 5)
	existing, err := NewBookmark("bm1", "entry", tr, 2)
	require.NoError(t, err)

	// Incoming bookmark shares the same ID but its target step no longer
	// exists in the current trace.
	incoming := existing
	incoming.StepID = "step-99-deadbeef"
	incoming.TraceFingerprint = "irrelevant"

	conflicts := DetectBookmarkConflicts(tr, []Bookmark{existing}, []Bookmark{incoming})
	require.Len(t, conflicts, 1)
	assert.Contains(t, conflicts[0].Reason, "does not exist")
}

func TestMergeBookmarks_FailPolicy_NeverOverwritesConflict(t *testing.T) {
	tr := bookmarkableTrace("tx1", 5)
	existing, err := NewBookmark("bm1", "entry", tr, 2)
	require.NoError(t, err)
	other, err := NewBookmark("bm1", "entry-renamed-elsewhere", tr, 4) // same ID, different step
	require.NoError(t, err)

	merged, result, err := MergeBookmarks(tr, []Bookmark{existing}, []Bookmark{other}, BookmarkImportFail)
	require.Error(t, err)
	require.Len(t, result.Conflicts, 1)
	require.Len(t, merged, 1)
	assert.Equal(t, existing, merged[0], "existing bookmark must be completely unchanged when fail policy rejects the merge")
}

func TestMergeBookmarks_RenamePolicy_KeepsBoth(t *testing.T) {
	tr := bookmarkableTrace("tx1", 5)
	existing, err := NewBookmark("bm1", "entry", tr, 2)
	require.NoError(t, err)
	incoming, err := NewBookmark("bm1", "entry-from-colleague", tr, 4)
	require.NoError(t, err)

	merged, result, err := MergeBookmarks(tr, []Bookmark{existing}, []Bookmark{incoming}, BookmarkImportRename)
	require.NoError(t, err)
	require.Len(t, merged, 2, "both bookmarks must be kept")
	require.Len(t, result.Renamed, 1)

	// The existing bookmark is untouched.
	assert.Contains(t, merged, existing)
	// The incoming one was kept under a new, non-colliding ID.
	assert.NotEqual(t, existing.ID, result.Renamed[0].ID)
	assert.Equal(t, "entry-from-colleague", result.Renamed[0].Name)
}

func TestMergeBookmarks_MergePolicy_KeepsBothNeverPicksAWinner(t *testing.T) {
	tr := bookmarkableTrace("tx1", 5)
	existing, err := NewBookmark("bm1", "entry", tr, 2)
	require.NoError(t, err)
	incoming, err := NewBookmark("bm1", "entry-from-colleague", tr, 4)
	require.NoError(t, err)

	merged, result, err := MergeBookmarks(tr, []Bookmark{existing}, []Bookmark{incoming}, BookmarkImportMerge)
	require.NoError(t, err)
	require.Len(t, merged, 2)
	require.Len(t, result.Renamed, 1)
	assert.Contains(t, merged, existing, "merge policy must never overwrite or drop the existing bookmark")
}

// ── No conflict: genuinely new bookmarks pass through untouched ──────────

func TestMergeBookmarks_NoConflict_NewBookmarksAdded(t *testing.T) {
	tr := bookmarkableTrace("tx1", 5)
	existing, err := NewBookmark("bm1", "entry", tr, 0)
	require.NoError(t, err)
	incoming, err := NewBookmark("bm2", "exit", tr, 4)
	require.NoError(t, err)

	merged, result, err := MergeBookmarks(tr, []Bookmark{existing}, []Bookmark{incoming}, BookmarkImportFail)
	require.NoError(t, err)
	assert.Empty(t, result.Conflicts)
	assert.Len(t, merged, 2)
}

// ── Import never silently overwrites: fail is the safe default ───────────

func TestParseBookmarkConflictPolicy_DefaultIsFail(t *testing.T) {
	p, err := ParseBookmarkConflictPolicy("")
	require.NoError(t, err)
	assert.Equal(t, BookmarkImportFail, p, "the default policy must be the non-destructive one")
}
