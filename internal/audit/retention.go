// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"fmt"
	"os"
	"sort"
	"time"
)

// RetentionConfig selects which closed segments are eligible for removal.
// Zero-valued fields are unlimited (never force removal on that axis).
// The active segment is never eligible.
type RetentionConfig struct {
	// MaxAge removes closed segments whose ClosedAt (or ModTime fallback) is
	// older than this duration.
	MaxAge time.Duration
	// MaxSizeBytes keeps the total size of retained closed segments at or
	// below this budget, deleting oldest first.
	MaxSizeBytes int64
	// MaxSegments keeps at most this many closed segments, deleting oldest
	// first.
	MaxSegments int
}

// RetentionPlan reports which closed segments would be kept or removed.
type RetentionPlan struct {
	Keep   []SegmentInfo `json:"keep"`
	Remove []SegmentInfo `json:"remove"`
}

// TotalRemoveBytes returns the sum of sizes of segments marked for removal.
func (p RetentionPlan) TotalRemoveBytes() int64 {
	var n int64
	for _, s := range p.Remove {
		n += s.SizeBytes
	}
	return n
}

// PlanRetention evaluates retention against dir without deleting anything.
func PlanRetention(dir string, cfg RetentionConfig) (*RetentionPlan, error) {
	segments, err := ListSegments(dir)
	if err != nil {
		return nil, err
	}
	return planRetention(segments, cfg, time.Now().UTC()), nil
}

func planRetention(segments []SegmentInfo, cfg RetentionConfig, now time.Time) *RetentionPlan {
	if len(segments) == 0 {
		return &RetentionPlan{}
	}

	// Work newest→oldest for size/count budgets; age is absolute.
	ordered := append([]SegmentInfo(nil), segments...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Manifest.Sequence == ordered[j].Manifest.Sequence {
			return ordered[i].Path > ordered[j].Path
		}
		return ordered[i].Manifest.Sequence > ordered[j].Manifest.Sequence
	})

	removeSet := map[string]struct{}{}

	if cfg.MaxAge > 0 {
		cutoff := now.Add(-cfg.MaxAge)
		for _, s := range ordered {
			ts := s.Manifest.ClosedAt
			if ts.IsZero() {
				ts = s.ModTime
			}
			if ts.Before(cutoff) {
				removeSet[s.Path] = struct{}{}
			}
		}
	}

	if cfg.MaxSegments > 0 {
		kept := 0
		for _, s := range ordered {
			if _, drop := removeSet[s.Path]; drop {
				continue
			}
			kept++
			if kept > cfg.MaxSegments {
				removeSet[s.Path] = struct{}{}
			}
		}
	}

	if cfg.MaxSizeBytes > 0 {
		var used int64
		for _, s := range ordered {
			if _, drop := removeSet[s.Path]; drop {
				continue
			}
			if used+s.SizeBytes > cfg.MaxSizeBytes {
				removeSet[s.Path] = struct{}{}
				continue
			}
			used += s.SizeBytes
		}
	}

	plan := &RetentionPlan{}
	// Report in ascending sequence order for stable output.
	sort.SliceStable(segments, func(i, j int) bool {
		return segments[i].Manifest.Sequence < segments[j].Manifest.Sequence
	})
	for _, s := range segments {
		if _, drop := removeSet[s.Path]; drop {
			plan.Remove = append(plan.Remove, s)
		} else {
			plan.Keep = append(plan.Keep, s)
		}
	}
	return plan
}

// ApplyRetention deletes segments (and their manifests) listed in a plan.
// When dryRun is true, nothing is deleted and the plan is returned as-is.
func ApplyRetention(dir string, cfg RetentionConfig, dryRun bool) (*RetentionPlan, error) {
	plan, err := PlanRetention(dir, cfg)
	if err != nil {
		return nil, err
	}
	if dryRun {
		return plan, nil
	}
	for _, s := range plan.Remove {
		if err := os.Remove(s.Path); err != nil && !os.IsNotExist(err) {
			return plan, fmt.Errorf("audit: remove segment %q: %w", s.Path, err)
		}
		if err := os.Remove(s.ManifestPath); err != nil && !os.IsNotExist(err) {
			return plan, fmt.Errorf("audit: remove manifest %q: %w", s.ManifestPath, err)
		}
	}
	return plan, nil
}
