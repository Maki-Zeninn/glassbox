// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func withFaultInject(t *testing.T, f func(stage string) error) {
	t.Helper()
	prev := faultInject
	faultInject = f
	t.Cleanup(func() { faultInject = prev })
}

func TestWriter_SizeRotationNeverTruncatesRecord(t *testing.T) {
	dir := t.TempDir()
	w, err := OpenWriter(dir, RotationConfig{MaxSizeBytes: 40})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	rec1 := []byte(`{"id":"one","payload":"aaaaaaaa"}`)
	rec2 := []byte(`{"id":"two","payload":"bbbbbbbb"}`)

	if err := w.WriteRecord(rec1); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteRecord(rec2); err != nil {
		t.Fatal(err)
	}

	segments, err := ListSegments(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 1 {
		t.Fatalf("expected 1 closed segment after size rotation, got %d", len(segments))
	}

	closed, err := os.ReadFile(segments[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(closed), `"id":"one"`) {
		t.Fatalf("closed segment missing complete first record: %s", closed)
	}
	if strings.Contains(string(closed), `"id":"two"`) {
		t.Fatalf("second record should be in active segment, not closed: %s", closed)
	}

	active, err := os.ReadFile(filepath.Join(dir, ActiveSegmentName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(active), string(rec2)) && !strings.Contains(string(active), `"id":"two"`) {
		t.Fatalf("active segment missing complete second record: %q", active)
	}
	// Ensure no partial JSON line was written.
	for _, line := range strings.Split(strings.TrimSpace(string(closed)), "\n") {
		if !strings.HasPrefix(line, "{") || !strings.HasSuffix(line, "}") {
			t.Fatalf("truncated or malformed record in closed segment: %q", line)
		}
	}
}

func TestWriter_TimeRotation(t *testing.T) {
	dir := t.TempDir()
	w, err := OpenWriter(dir, RotationConfig{MaxAge: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	base := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	w.now = func() time.Time { return base }
	w.openedAt = base

	if err := w.WriteRecord([]byte(`{"n":1}`)); err != nil {
		t.Fatal(err)
	}

	w.now = func() time.Time { return base.Add(2 * time.Minute) }
	if err := w.WriteRecord([]byte(`{"n":2}`)); err != nil {
		t.Fatal(err)
	}

	segments, err := ListSegments(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 1 {
		t.Fatalf("expected time-based rotation to close 1 segment, got %d", len(segments))
	}
}

func TestWriter_ChecksumChain(t *testing.T) {
	dir := t.TempDir()
	w, err := OpenWriter(dir, RotationConfig{})
	if err != nil {
		t.Fatal(err)
	}

	if err := w.WriteRecord([]byte(`{"n":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := w.Rotate(); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteRecord([]byte(`{"n":2}`)); err != nil {
		t.Fatal(err)
	}
	if err := w.Rotate(); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	result, err := VerifyDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid || !result.ChainValid {
		t.Fatalf("expected valid chain, got %+v", result)
	}
	if result.SegmentsChecked != 2 {
		t.Fatalf("expected 2 segments, got %d", result.SegmentsChecked)
	}

	segments, err := ListSegments(dir)
	if err != nil {
		t.Fatal(err)
	}
	if segments[0].Manifest.PreviousSegmentHash != "" {
		t.Fatal("genesis must not set previous_segment_hash")
	}
	if segments[1].Manifest.PreviousSegmentHash != segments[0].Manifest.SHA256 {
		t.Fatalf("segment 2 previous hash %s != segment 1 sha %s",
			segments[1].Manifest.PreviousSegmentHash, segments[0].Manifest.SHA256)
	}
}

func TestVerifyDirectory_MissingSegment(t *testing.T) {
	dir := t.TempDir()
	w, err := OpenWriter(dir, RotationConfig{})
	if err != nil {
		t.Fatal(err)
	}
	_ = w.WriteRecord([]byte(`{"n":1}`))
	_ = w.Rotate()
	_ = w.WriteRecord([]byte(`{"n":2}`))
	_ = w.Rotate()
	_ = w.Close()

	segments, err := ListSegments(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 2 {
		t.Fatalf("need 2 segments, got %d", len(segments))
	}
	if err := os.Remove(segments[0].Path); err != nil {
		t.Fatal(err)
	}

	result, err := VerifyDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if result.Valid {
		t.Fatal("expected verification failure for missing segment")
	}
	found := false
	for _, issue := range result.Issues {
		if strings.Contains(issue, "missing segment") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected missing-segment issue, got %v", result.Issues)
	}
}

func TestVerifyDirectory_MissingSegmentInSequence(t *testing.T) {
	dir := t.TempDir()
	w, err := OpenWriter(dir, RotationConfig{})
	if err != nil {
		t.Fatal(err)
	}
	_ = w.WriteRecord([]byte(`{"n":1}`))
	_ = w.Rotate()
	_ = w.WriteRecord([]byte(`{"n":2}`))
	_ = w.Rotate()
	_ = w.WriteRecord([]byte(`{"n":3}`))
	_ = w.Rotate()
	_ = w.Close()

	segments, err := ListSegments(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Remove middle segment body + manifest to simulate a gap.
	_ = os.Remove(segments[1].Path)
	_ = os.Remove(segments[1].ManifestPath)

	result, err := VerifyDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if result.Valid || result.ChainValid {
		t.Fatalf("expected chain failure for gap, got %+v", result)
	}
	found := false
	for _, issue := range result.Issues {
		if strings.Contains(issue, "missing segment in chain") || strings.Contains(issue, "chain link broken") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected sequence/chain gap issue, got %v", result.Issues)
	}
}

func TestRetention_ReportsWhatWillBeRemoved(t *testing.T) {
	dir := t.TempDir()
	w, err := OpenWriter(dir, RotationConfig{})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := w.WriteRecord([]byte(fmt.Sprintf(`{"n":%d}`, i+1))); err != nil {
			t.Fatal(err)
		}
		if err := w.Rotate(); err != nil {
			t.Fatal(err)
		}
	}
	_ = w.Close()

	plan, err := PlanRetention(dir, RetentionConfig{MaxSegments: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Keep) != 1 || len(plan.Remove) != 2 {
		t.Fatalf("expected keep=1 remove=2, got keep=%d remove=%d", len(plan.Keep), len(plan.Remove))
	}

	// Dry-run must not delete.
	if _, err := ApplyRetention(dir, RetentionConfig{MaxSegments: 1}, true); err != nil {
		t.Fatal(err)
	}
	segments, err := ListSegments(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 3 {
		t.Fatalf("dry-run deleted segments: still have %d", len(segments))
	}

	applied, err := ApplyRetention(dir, RetentionConfig{MaxSegments: 1}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied.Remove) != 2 {
		t.Fatalf("expected 2 removals, got %d", len(applied.Remove))
	}
	segments, err = ListSegments(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 1 {
		t.Fatalf("expected 1 segment after apply, got %d", len(segments))
	}
}

func TestRetention_MaxAge(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	segments := []SegmentInfo{
		{
			Path:      "old.jsonl",
			SizeBytes: 10,
			ModTime:   now.Add(-48 * time.Hour),
			Manifest: SegmentManifest{
				Segment:  "segment-000001-20260726T120000Z.jsonl",
				Sequence: 1,
				ClosedAt: now.Add(-48 * time.Hour),
				SHA256:   strings.Repeat("a", 64),
			},
		},
		{
			Path:      "new.jsonl",
			SizeBytes: 10,
			ModTime:   now,
			Manifest: SegmentManifest{
				Segment:  "segment-000002-20260728T120000Z.jsonl",
				Sequence: 2,
				ClosedAt: now,
				SHA256:   strings.Repeat("b", 64),
			},
		},
	}
	plan := planRetention(segments, RetentionConfig{MaxAge: 24 * time.Hour}, now)
	if len(plan.Remove) != 1 || plan.Remove[0].Path != "old.jsonl" {
		t.Fatalf("expected only old segment removed, got %+v", plan.Remove)
	}
	if len(plan.Keep) != 1 || plan.Keep[0].Path != "new.jsonl" {
		t.Fatalf("expected new segment kept, got %+v", plan.Keep)
	}
}

func TestRotate_InterruptedBeforeSegmentRename(t *testing.T) {
	dir := t.TempDir()
	w, err := OpenWriter(dir, RotationConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if err := w.WriteRecord([]byte(`{"intact":true}`)); err != nil {
		t.Fatal(err)
	}

	injected := errors.New("simulated crash before segment rename")
	withFaultInject(t, func(stage string) error {
		if stage == faultRotateBeforeSegmentRename {
			return injected
		}
		return nil
	})

	err = w.Rotate()
	if !errors.Is(err, injected) {
		t.Fatalf("expected injected fault, got %v", err)
	}

	// Active record must still be present and complete — never truncated.
	active, err := os.ReadFile(filepath.Join(dir, ActiveSegmentName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(active), `{"intact":true}`) {
		t.Fatalf("active record truncated or lost after interrupted rotation: %q", active)
	}

	result, err := VerifyDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Manifest may exist without a renamed segment body.
	if result.Valid {
		t.Fatal("expected verify to report interrupted rotation artifacts")
	}
	found := false
	for _, issue := range result.Issues {
		if strings.Contains(issue, "missing segment") || strings.Contains(issue, "interrupted") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected interrupted-rotation issue, got %v", result.Issues)
	}
}

func TestRotate_InterruptedAfterClosePreservesActive(t *testing.T) {
	dir := t.TempDir()
	w, err := OpenWriter(dir, RotationConfig{})
	if err != nil {
		t.Fatal(err)
	}

	if err := w.WriteRecord([]byte(`{"n":1}`)); err != nil {
		t.Fatal(err)
	}

	injected := errors.New("crash after close")
	withFaultInject(t, func(stage string) error {
		if stage == faultRotateAfterCloseActive {
			return injected
		}
		return nil
	})

	err = w.Rotate()
	if !errors.Is(err, injected) {
		t.Fatalf("expected injected fault, got %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ActiveSegmentName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `{"n":1}`) {
		t.Fatalf("active content lost: %q", data)
	}

	// No closed segments should exist yet.
	segments, listErr := ListSegments(dir)
	if listErr == nil && len(segments) != 0 {
		t.Fatalf("expected no closed segments after early interrupt, got %d", len(segments))
	}
}

func TestManifestIsImmutableAfterWrite(t *testing.T) {
	dir := t.TempDir()
	w, err := OpenWriter(dir, RotationConfig{})
	if err != nil {
		t.Fatal(err)
	}
	_ = w.WriteRecord([]byte(`{"n":1}`))
	_ = w.Rotate()
	_ = w.Close()

	segments, err := ListSegments(dir)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(segments[0].ManifestPath)
	if err != nil {
		t.Fatal(err)
	}

	// Tamper with the segment body; manifest must still describe the original hash.
	if err := os.WriteFile(segments[0].Path, []byte(`{"tampered":true}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(segments[0].ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(original) != string(after) {
		t.Fatal("manifest changed after segment body tamper — manifests must be immutable")
	}

	result, err := VerifyDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if result.Valid {
		t.Fatal("expected checksum mismatch after tamper")
	}
}
