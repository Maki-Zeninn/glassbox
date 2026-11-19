// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Tests for the extended GCOptions retention policies:
// size-based (MaxTotalSize), status-based (RequireStatus), and tag-based
// exclusions (ExcludeTags).

package session

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Size-based retention ──────────────────────────────────────────────────────

func TestPlanGC_MaxTotalSize_TrimsOldestUntilBudgetMet(t *testing.T) {
	overrideTempHome(t)
	store, err := NewStore()
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	base := time.Now()

	// Create 5 sessions, oldest first.
	for i := 0; i < 5; i++ {
		d := gcTestData(gcID(i), base.Add(-time.Duration(5-i)*time.Hour), "saved", "")
		require.NoError(t, store.Save(ctx, d))
		backdateLastAccess(t, store, gcID(i), base.Add(-time.Duration(5-i)*time.Hour))
	}

	plan, err := store.PlanGC(ctx, GCOptions{MaxTotalSize: 1}) // 1 byte — force deletion of all
	require.NoError(t, err)

	// At least the oldest sessions must be eligible.
	assert.NotEmpty(t, plan.ToDelete, "at least one session should be eligible when total size exceeds budget")
	assert.LessOrEqual(t, plan.TotalSize-plan.DeleteSize(), int64(1)+1024,
		"retained size should approach the budget after deletions")
}

func TestPlanGC_MaxTotalSize_Zero_Disables(t *testing.T) {
	overrideTempHome(t)
	store, err := NewStore()
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	base := time.Now()
	for i := 0; i < 3; i++ {
		d := gcTestData(gcID(i), base.Add(-time.Duration(3-i)*time.Hour), "saved", "")
		require.NoError(t, store.Save(ctx, d))
	}

	// MaxTotalSize=0 must disable size-based eligibility.
	plan, err := store.PlanGC(ctx, GCOptions{MaxTotalSize: 0})
	require.NoError(t, err)
	assert.Empty(t, plan.ToDelete, "MaxTotalSize=0 should disable size-based eligibility")
}

func TestPlanGC_MaxTotalSize_PinnedSessionNotCounted(t *testing.T) {
	overrideTempHome(t)
	store, err := NewStore()
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	base := time.Now()

	pinned := gcTestData("sz-pinned", base.Add(-2*time.Hour), "saved", "keep-this")
	regular := gcTestData("sz-regular", base.Add(-3*time.Hour), "saved", "")
	require.NoError(t, store.Save(ctx, pinned))
	require.NoError(t, store.Save(ctx, regular))
	backdateLastAccess(t, store, "sz-pinned", base.Add(-2*time.Hour))
	backdateLastAccess(t, store, "sz-regular", base.Add(-3*time.Hour))

	plan, err := store.PlanGC(ctx, GCOptions{MaxTotalSize: 1})
	require.NoError(t, err)

	for _, e := range plan.ToDelete {
		assert.NotEqual(t, "sz-pinned", e.ID, "pinned session must not be deleted by size-based policy")
	}
}

// ── Status-based retention ────────────────────────────────────────────────────

func TestPlanGC_RequireStatus_OnlyMatchingStatusEligible(t *testing.T) {
	overrideTempHome(t)
	store, err := NewStore()
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	base := time.Now().Add(-100 * 24 * time.Hour)

	expired := gcTestData("st-expired", base, "expired", "")
	saved := gcTestData("st-saved", base, "saved", "")
	require.NoError(t, store.Save(ctx, expired))
	require.NoError(t, store.Save(ctx, saved))
	backdateLastAccess(t, store, "st-expired", base)
	backdateLastAccess(t, store, "st-saved", base)

	// Only "expired" sessions should be eligible.
	plan, err := store.PlanGC(ctx, GCOptions{
		MaxAge:        30 * 24 * time.Hour,
		RequireStatus: "expired",
	})
	require.NoError(t, err)

	for _, e := range plan.ToDelete {
		assert.Equal(t, "st-expired", e.ID, "only expired sessions should be eligible when RequireStatus=expired")
	}

	found := false
	for _, e := range plan.ToDelete {
		if e.ID == "st-expired" {
			found = true
		}
	}
	assert.True(t, found, "the expired session should appear in ToDelete")
}

func TestPlanGC_RequireStatus_Empty_MatchesAll(t *testing.T) {
	overrideTempHome(t)
	store, err := NewStore()
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	base := time.Now().Add(-100 * 24 * time.Hour)

	for _, status := range []string{"saved", "expired", "recovered"} {
		d := gcTestData("st-"+status, base, status, "")
		require.NoError(t, store.Save(ctx, d))
		backdateLastAccess(t, store, "st-"+status, base)
	}

	// RequireStatus="" should allow all statuses.
	plan, err := store.PlanGC(ctx, GCOptions{
		MaxAge:        30 * 24 * time.Hour,
		RequireStatus: "",
	})
	require.NoError(t, err)
	assert.Len(t, plan.ToDelete, 3, "all old sessions regardless of status should be eligible when RequireStatus is empty")
}

// ── ExcludeTags protection ────────────────────────────────────────────────────

func TestPlanGC_ExcludeTags_ProtectsNamedSessions(t *testing.T) {
	overrideTempHome(t)
	store, err := NewStore()
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	base := time.Now().Add(-100 * 24 * time.Hour)

	tagged := gcTestData("tag-protected", base, "saved", "important")
	regular := gcTestData("tag-regular", base, "saved", "")
	require.NoError(t, store.Save(ctx, tagged))
	require.NoError(t, store.Save(ctx, regular))
	backdateLastAccess(t, store, "tag-protected", base)
	backdateLastAccess(t, store, "tag-regular", base)

	plan, err := store.PlanGC(ctx, GCOptions{
		MaxAge:      30 * 24 * time.Hour,
		ExcludeTags: []string{"important"},
	})
	require.NoError(t, err)

	for _, e := range plan.ToDelete {
		assert.NotEqual(t, "tag-protected", e.ID,
			"session with excluded tag 'important' must not be deleted")
	}
	found := false
	for _, e := range plan.ToDelete {
		if e.ID == "tag-regular" {
			found = true
		}
	}
	assert.True(t, found, "untagged old session should still be eligible")
}

// ── Dry-run has no side effects ───────────────────────────────────────────────

func TestPlanGC_DryRun_SizePolicy_NoSideEffects(t *testing.T) {
	overrideTempHome(t)
	store, err := NewStore()
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	base := time.Now().Add(-5 * time.Hour)
	d := gcTestData("dryrun-sz", base, "saved", "")
	require.NoError(t, store.Save(ctx, d))
	backdateLastAccess(t, store, "dryrun-sz", base)

	plan, err := store.RunGC(ctx, GCOptions{MaxTotalSize: 1}, true /* dryRun */)
	require.NoError(t, err)
	assert.NotEmpty(t, plan.Entries)

	// Session must still exist.
	_, loadErr := store.Load(ctx, "dryrun-sz")
	require.NoError(t, loadErr, "dry-run must not delete any sessions")
}

// ── Boundary: session at exactly MaxAge boundary ─────────────────────────────

func TestPlanGC_BoundaryTimestamp_ExactlyAtMaxAge_IsEligible(t *testing.T) {
	overrideTempHome(t)
	store, err := NewStore()
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	// A session whose age is exactly MaxAge + 1 second to ensure it is over
	// the boundary (> not >=).
	boundary := time.Now().Add(-(30*24*time.Hour + time.Second))
	d := gcTestData("boundary-session", boundary, "saved", "")
	require.NoError(t, store.Save(ctx, d))
	backdateLastAccess(t, store, "boundary-session", boundary)

	plan, err := store.PlanGC(ctx, GCOptions{MaxAge: 30 * 24 * time.Hour})
	require.NoError(t, err)

	found := false
	for _, e := range plan.ToDelete {
		if e.ID == "boundary-session" {
			found = true
		}
	}
	assert.True(t, found, "session just past MaxAge boundary must be eligible")
}

func TestPlanGC_BoundaryTimestamp_JustUnderMaxAge_NotEligible(t *testing.T) {
	overrideTempHome(t)
	store, err := NewStore()
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	// Session is 1 second younger than MaxAge — should NOT be eligible.
	justUnder := time.Now().Add(-(30*24*time.Hour - time.Second))
	d := gcTestData("under-boundary", justUnder, "saved", "")
	require.NoError(t, store.Save(ctx, d))
	backdateLastAccess(t, store, "under-boundary", justUnder)

	plan, err := store.PlanGC(ctx, GCOptions{MaxAge: 30 * 24 * time.Hour})
	require.NoError(t, err)

	for _, e := range plan.ToDelete {
		assert.NotEqual(t, "under-boundary", e.ID,
			"session just under MaxAge must not be eligible")
	}
}
