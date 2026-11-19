// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func bookmarkableTrace(txHash string, n int) *ExecutionTrace {
	tr := NewExecutionTrace(txHash, 100)
	tr.StartTime = time.Now()
	tr.EndTime = tr.StartTime.Add(time.Second)
	for i := 0; i < n; i++ {
		tr.AddState(ExecutionState{
			Step:       i,
			Operation:  "invoke",
			EventType:  EventTypeContractCall,
			ContractID: "CONTRACT" + string(rune('A'+i)),
			Function:   "fn",
			SourceFile: "lib.rs",
			SourceLine: 10 + i,
		})
	}
	return tr
}

// ── NewBookmark ────────────────────────────────────────────────────────────

func TestNewBookmark_CapturesFingerprintAndStepID(t *testing.T) {
	tr := bookmarkableTrace("tx1", 5)
	b, err := NewBookmark("bm1", "entrypoint", tr, 2)
	require.NoError(t, err)
	assert.Equal(t, tr.Fingerprint(), b.TraceFingerprint)
	assert.Equal(t, tr.StepID(2), b.StepID)
	assert.Equal(t, "tx1", b.TxHash)
	assert.False(t, b.CreatedAt.IsZero())
}

func TestNewBookmark_NilTrace_Errors(t *testing.T) {
	_, err := NewBookmark("bm1", "x", nil, 0)
	require.Error(t, err)
}

func TestNewBookmark_OutOfRangeStep_Errors(t *testing.T) {
	tr := bookmarkableTrace("tx1", 3)
	_, err := NewBookmark("bm1", "x", tr, 99)
	require.Error(t, err)
}

// ── ResolveBookmark: exact match ─────────────────────────────────────────

func TestResolveBookmark_ExactMatch(t *testing.T) {
	tr := bookmarkableTrace("tx1", 5)
	b, err := NewBookmark("bm1", "x", tr, 3)
	require.NoError(t, err)

	res := ResolveBookmark(tr, b)
	assert.Equal(t, BookmarkResolvedExact, res.Status)
	assert.Equal(t, 3, res.StepIndex)
}

// ── ResolveBookmark: nonsemantic change (fingerprint changes, step ID survives) ──

func TestResolveBookmark_ReopensStepAfterNonsemanticChange(t *testing.T) {
	tr := bookmarkableTrace("tx1", 5)
	b, err := NewBookmark("bm1", "x", tr, 2)
	require.NoError(t, err)

	// Simulate a nonsemantic change: append a new step at the end. This
	// changes the trace's overall Fingerprint (which hashes step count and
	// every step) but does not change the StepID of the earlier step the
	// bookmark targets.
	tr.AddState(ExecutionState{Step: 5, Operation: "invoke", EventType: EventTypeContractCall, ContractID: "EXTRA", Function: "fn"})
	require.NotEqual(t, b.TraceFingerprint, tr.Fingerprint(), "fingerprint should differ after the trace changed")

	res := ResolveBookmark(tr, b)
	assert.Equal(t, BookmarkResolvedByStepID, res.Status)
	assert.Equal(t, 2, res.StepIndex, "bookmark must still resolve to the same step by content, not by position")
}

// ── ResolveBookmark: mismatched/deleted target is flagged, never silently rebound ──

func TestResolveBookmark_DeletedTarget_Dangling(t *testing.T) {
	tr := bookmarkableTrace("tx1", 5)
	b, err := NewBookmark("bm1", "x", tr, 2)
	require.NoError(t, err)

	// Rebuild the trace without step 2's identifying fields — the step is
	// effectively gone.
	tr2 := bookmarkableTrace("tx1", 5)
	tr2.States[2].ContractID = "SOMETHING-ELSE-ENTIRELY"

	res := ResolveBookmark(tr2, b)
	assert.Equal(t, BookmarkDangling, res.Status)
	assert.Equal(t, -1, res.StepIndex)
	assert.NotEmpty(t, res.Reason)
}

func TestResolveBookmark_MismatchedTrace_Dangling(t *testing.T) {
	tr1 := bookmarkableTrace("tx1", 5)
	b, err := NewBookmark("bm1", "x", tr1, 2)
	require.NoError(t, err)

	unrelated := bookmarkableTrace("tx-unrelated", 1)
	res := ResolveBookmark(unrelated, b)
	assert.Equal(t, BookmarkDangling, res.Status)
}

func TestResolveBookmark_NilTrace_Dangling(t *testing.T) {
	b := Bookmark{ID: "bm1", StepID: "step-0-deadbeef"}
	res := ResolveBookmark(nil, b)
	assert.Equal(t, BookmarkDangling, res.Status)
	assert.Equal(t, "trace is nil", res.Reason)
}

// ── ResolveBookmarks batch ────────────────────────────────────────────────

func TestResolveBookmarks_Batch(t *testing.T) {
	tr := bookmarkableTrace("tx1", 5)
	b1, _ := NewBookmark("bm1", "one", tr, 0)
	b2, _ := NewBookmark("bm2", "two", tr, 4)
	b3 := Bookmark{ID: "bm3", Name: "gone", StepID: "step-99-deadbeef"}

	results := ResolveBookmarks(tr, []Bookmark{b1, b2, b3})
	require.Len(t, results, 3)
	assert.Equal(t, BookmarkResolvedExact, results[0].Status)
	assert.Equal(t, BookmarkResolvedExact, results[1].Status)
	assert.Equal(t, BookmarkDangling, results[2].Status)
}
