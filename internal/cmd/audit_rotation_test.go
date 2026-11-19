// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotandev/glassbox/internal/audit"
)

func TestAuditVerifyDir_ValidChain(t *testing.T) {
	dir := t.TempDir()
	w, err := audit.OpenWriter(dir, audit.RotationConfig{})
	if err != nil {
		t.Fatal(err)
	}
	_ = w.WriteRecord([]byte(`{"n":1}`))
	_ = w.Rotate()
	_ = w.WriteRecord([]byte(`{"n":2}`))
	_ = w.Rotate()
	_ = w.Close()

	auditVerifyDirPath = dir
	auditVerifyDirJSON = true
	t.Cleanup(func() {
		auditVerifyDirPath = ""
		auditVerifyDirJSON = false
	})

	cmd := auditVerifyDirCmd
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(&bytes.Buffer{})
	if err := runAuditVerifyDir(cmd, nil); err != nil {
		t.Fatalf("verify-dir failed: %v\n%s", err, buf.String())
	}

	var result audit.DirVerifyResult
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Valid || result.SegmentsChecked != 2 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestAuditRetention_DryRunReportsRemovals(t *testing.T) {
	dir := t.TempDir()
	w, err := audit.OpenWriter(dir, audit.RotationConfig{})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		_ = w.WriteRecord([]byte(`{"n":1}`))
		_ = w.Rotate()
	}
	_ = w.Close()

	auditRetentionDir = dir
	auditRetentionMaxSegments = 1
	auditRetentionDryRun = true
	auditRetentionJSON = false
	t.Cleanup(func() {
		auditRetentionDir = ""
		auditRetentionMaxSegments = 0
		auditRetentionDryRun = false
		auditRetentionJSON = false
		auditRetentionMaxAge = 0
		auditRetentionMaxSize = 0
	})

	cmd := auditRetentionCmd
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	if err := runAuditRetention(cmd, nil); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "Would remove") {
		t.Fatalf("expected dry-run report, got:\n%s", out)
	}
	if !strings.Contains(out, "Re-run without --dry-run") {
		t.Fatalf("expected dry-run guidance, got:\n%s", out)
	}

	// Files must still exist.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var segs int
	for _, e := range entries {
		if audit.IsClosedSegmentName(e.Name()) {
			segs++
		}
	}
	if segs != 3 {
		t.Fatalf("dry-run removed files; remaining segments=%d", segs)
	}

	// Ensure manifests remain next to segments.
	for _, e := range entries {
		if audit.IsClosedSegmentName(e.Name()) {
			if _, err := os.Stat(filepath.Join(dir, strings.TrimSuffix(e.Name(), ".jsonl")+audit.ManifestSuffix)); err != nil {
				t.Fatal(err)
			}
		}
	}
}
