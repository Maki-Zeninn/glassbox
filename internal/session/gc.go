// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package session

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dotandev/glassbox/internal/cache"
)

// GCEntry describes one session considered during a garbage-collection pass.
type GCEntry struct {
	ID        string
	Name      string
	SizeBytes int64
	Age       time.Duration
	// Pinned is true for bookmarked sessions (a non-empty Name) — protected
	// from deletion regardless of age or count.
	Pinned bool
	// Active is true when Status == "active" — protected from deletion.
	Active bool
	// Corrupt is true when the session fails integrity validation. Garbage
	// collection still considers it for retention like any other
	// non-pinned, non-active session rather than treating it as fatal.
	Corrupt bool
	// Eligible is true when this entry would be removed by RunGC given the
	// GCOptions used to build the plan.
	Eligible bool
}

// GCOptions controls which sessions a garbage-collection pass considers
// eligible for deletion.
type GCOptions struct {
	// MaxAge: sessions whose LastAccessAt is older than this are eligible
	// for deletion. Zero disables age-based eligibility.
	MaxAge time.Duration
	// MaxCount caps the number of retained sessions; the oldest eligible
	// excess beyond this count is removed. Zero disables count-based
	// eligibility.
	MaxCount int
	// MaxTotalSize is the maximum total size (in bytes) of all retained
	// sessions. When the sum of all retainable sessions exceeds this budget,
	// the oldest eligible sessions are removed until it fits. Zero disables
	// size-based eligibility.
	MaxTotalSize int64
	// RequireStatus, when non-empty, restricts eligibility to sessions with
	// exactly this Status value. Only "saved", "expired", and "recovered"
	// are meaningful here — "active" sessions are always protected
	// regardless of this filter.
	RequireStatus string
	// ExcludeTags is a set of Name values (user-assigned bookmarks) that are
	// always excluded from deletion even when no other protection applies.
	// An entry here acts as an additional pin beyond the non-empty Name check.
	ExcludeTags []string
}

// DefaultGCOptions mirrors the store's existing default retention policy
// (see DefaultTTL, DefaultMaxSessions).
func DefaultGCOptions() GCOptions {
	return GCOptions{MaxAge: DefaultTTL, MaxCount: DefaultMaxSessions}
}

// GCPlan is the result of evaluating retention rules against the sessions
// currently in the store. It is safe to render for --dry-run output before
// any deletion occurs — Entries and their SizeBytes are computed identically
// whether or not the plan is later executed.
type GCPlan struct {
	// Entries lists every session considered, in store listing order.
	Entries []GCEntry
	// ToDelete is the subset of Entries eligible for deletion.
	ToDelete []GCEntry
	// TotalSize is the sum of SizeBytes across all Entries.
	TotalSize int64
}

// DeleteSize returns the sum of SizeBytes across ToDelete.
func (p *GCPlan) DeleteSize() int64 {
	var total int64
	for _, e := range p.ToDelete {
		total += e.SizeBytes
	}
	return total
}

// approxSize estimates a session's on-disk footprint by deterministically
// marshaling it to JSON. This reuses the same canonical marshaling as
// session archives so size estimates are stable across runs.
func approxSize(d *Data) int64 {
	b, err := DeterministicMarshal(d)
	if err != nil {
		return 0
	}
	return int64(len(b))
}

// PlanGC evaluates every session in the store against opts without deleting
// anything. Active sessions and pinned (bookmarked) sessions are always
// excluded from ToDelete regardless of age, count, or size. Sessions whose
// Name appears in opts.ExcludeTags are also excluded.
//
// When opts.RequireStatus is non-empty, only sessions with that status are
// considered for deletion — all others are implicitly retained.
func (s *Store) PlanGC(ctx context.Context, opts GCOptions) (*GCPlan, error) {
	sessions, err := s.List(ctx, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to list sessions for garbage collection: %w", err)
	}

	// Build an exclusion set from ExcludeTags for O(1) lookup.
	excludedTags := make(map[string]bool, len(opts.ExcludeTags))
	for _, tag := range opts.ExcludeTags {
		if strings.TrimSpace(tag) != "" {
			excludedTags[strings.TrimSpace(tag)] = true
		}
	}

	now := time.Now()
	plan := &GCPlan{}

	// Oldest-first so MaxCount and MaxTotalSize trimming removes the oldest
	// excess first.
	sorted := make([]*Data, len(sessions))
	copy(sorted, sessions)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].LastAccessAt.Before(sorted[j].LastAccessAt)
	})

	entries := make(map[string]*GCEntry, len(sorted))
	order := make([]string, 0, len(sorted))
	for _, d := range sorted {
		// A session is pinned when it has a non-empty Name OR when its Name
		// appears in ExcludeTags.
		pinned := strings.TrimSpace(d.Name) != "" || excludedTags[strings.TrimSpace(d.Name)]

		e := &GCEntry{
			ID:        d.ID,
			Name:      d.Name,
			SizeBytes: approxSize(d),
			Age:       now.Sub(d.LastAccessAt),
			Pinned:    pinned,
			Active:    d.Status == "active",
			Corrupt:   !ValidateIntegrity(d).OK,
		}
		entries[d.ID] = e
		order = append(order, d.ID)
		plan.TotalSize += e.SizeBytes
	}

	// isCandidate returns true when the entry may be considered for
	// deletion by the policy rules below. Active and pinned sessions are
	// always excluded. When RequireStatus is set, only sessions matching
	// that status are candidates.
	isCandidate := func(d *Data, e *GCEntry) bool {
		if e.Pinned || e.Active {
			return false
		}
		if opts.RequireStatus != "" && d.Status != opts.RequireStatus {
			return false
		}
		return true
	}

	// Build a map of sessionID → *Data for candidate checks.
	dataByID := make(map[string]*Data, len(sorted))
	for _, d := range sorted {
		dataByID[d.ID] = d
	}

	// Age-based eligibility.
	if opts.MaxAge > 0 {
		for _, id := range order {
			e := entries[id]
			d := dataByID[id]
			if !isCandidate(d, e) {
				continue
			}
			if e.Age > opts.MaxAge {
				e.Eligible = true
			}
		}
	}

	// Count-based eligibility: trim the oldest retainable candidates beyond
	// MaxCount.
	if opts.MaxCount > 0 {
		retainable := 0
		for _, id := range order {
			e := entries[id]
			d := dataByID[id]
			if isCandidate(d, e) {
				retainable++
			}
		}
		excess := retainable - opts.MaxCount
		if excess > 0 {
			counted := 0
			for _, id := range order {
				e := entries[id]
				d := dataByID[id]
				if !isCandidate(d, e) {
					continue
				}
				if counted < excess {
					e.Eligible = true
					counted++
				}
			}
		}
	}

	// Size-based eligibility: after age and count, if the total retained
	// size still exceeds MaxTotalSize, trim the oldest candidates further
	// until the budget is met.
	if opts.MaxTotalSize > 0 {
		// Compute the retained total: plan.TotalSize minus what's already
		// marked eligible.
		var eligibleSize int64
		for _, id := range order {
			if entries[id].Eligible {
				eligibleSize += entries[id].SizeBytes
			}
		}
		retainedSize := plan.TotalSize - eligibleSize

		if retainedSize > opts.MaxTotalSize {
			overage := retainedSize - opts.MaxTotalSize
			for _, id := range order {
				if overage <= 0 {
					break
				}
				e := entries[id]
				d := dataByID[id]
				if e.Eligible || !isCandidate(d, e) {
					continue
				}
				e.Eligible = true
				overage -= e.SizeBytes
			}
		}
	}

	// Preserve original store-listing order (most-recently-accessed first)
	// in the returned plan, as callers expect from Store.List.
	for _, d := range sessions {
		e := *entries[d.ID]
		plan.Entries = append(plan.Entries, e)
		if e.Eligible {
			plan.ToDelete = append(plan.ToDelete, e)
		}
	}

	return plan, nil
}

// RunGC deletes every session PlanGC marks eligible. Active and pinned
// sessions are never part of the plan, so they are never deleted. Pass
// dryRun=true to compute the plan without mutating the store — the returned
// plan is identical either way, so CLI callers can render it for preview.
func (s *Store) RunGC(ctx context.Context, opts GCOptions, dryRun bool) (*GCPlan, error) {
	plan, err := s.PlanGC(ctx, opts)
	if err != nil {
		return nil, err
	}
	if dryRun {
		return plan, nil
	}
	for _, e := range plan.ToDelete {
		if err := s.Delete(ctx, e.ID); err != nil {
			return plan, fmt.Errorf("failed to delete session %q during garbage collection: %w", e.ID, err)
		}
	}
	return plan, nil
}

// ValidateGCRoot ensures root is safe to run destructive cleanup against. It
// rejects empty paths, filesystem roots (e.g. "/", "C:\"), and any directory
// that isn't named ".Glassbox", so a mistyped --root flag can never point
// cleanup at an unrelated or system directory.
func ValidateGCRoot(root string) error {
	trimmed := strings.TrimSpace(root)
	if trimmed == "" {
		return fmt.Errorf("cleanup root path is required")
	}
	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return fmt.Errorf("cannot resolve cleanup root %q: %w", trimmed, err)
	}
	clean := filepath.Clean(abs)
	if parent := filepath.Dir(clean); parent == clean {
		return fmt.Errorf(
			"refusing to run cleanup against filesystem root %q\n"+
				"  Fix: point --root at a Glassbox data directory (e.g. ~/.Glassbox)",
			clean,
		)
	}
	if filepath.Base(clean) != ".Glassbox" {
		return fmt.Errorf(
			"refusing to run cleanup against %q — expected a '.Glassbox' directory\n"+
				"  Fix: pass the path to your Glassbox data directory, or omit --root to use the default",
			clean,
		)
	}
	return nil
}

// CacheGCSummary lists the cache files a garbage-collection pass considers
// alongside sessions, reusing the existing cache.Manager rather than
// duplicating file-scanning logic.
type CacheGCSummary struct {
	Files      []cache.FileInfo
	TotalBytes int64
}

// PlanCacheGC lists cache files under cacheDir for the --dry-run listing
// shown alongside session entries. It never deletes anything; callers use
// cache.Manager.Clean/CleanLRU directly to perform cache eviction.
func PlanCacheGC(cacheDir string) (*CacheGCSummary, error) {
	mgr := cache.NewManager(cacheDir, cache.DefaultConfig())
	files, err := mgr.ListCachedFiles()
	if err != nil {
		return nil, fmt.Errorf("failed to list cache entries: %w", err)
	}
	summary := &CacheGCSummary{Files: files}
	for _, f := range files {
		summary.TotalBytes += f.Size
	}
	return summary, nil
}
