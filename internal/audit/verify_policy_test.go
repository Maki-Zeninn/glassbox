// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package audit_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dotandev/glassbox/internal/audit"
)

// ── helpers ──────────────────────────────────────────────────────────────────

// writeSegment writes a minimal valid closed segment + manifest to dir.
// Returns the segment body hash.
func writeSegment(t *testing.T, dir string, seq uint64, body string, prevHash string) string {
	t.Helper()
	closedAt := time.Date(2026, 1, int(seq), 12, 0, 0, 0, time.UTC)
	segName := audit.FormatSegmentName(seq, closedAt)
	segPath := filepath.Join(dir, segName)

	bodyBytes := []byte(body + "\n")
	if err := os.WriteFile(segPath, bodyBytes, 0o600); err != nil {
		t.Fatalf("writeSegment: %v", err)
	}
	hash := audit.HashBytes(bodyBytes)

	man := audit.SegmentManifest{
		SchemaVersion:       audit.SchemaVersion,
		Segment:             segName,
		Sequence:            seq,
		CreatedAt:           closedAt.Add(-time.Hour),
		ClosedAt:            closedAt,
		RecordCount:         1,
		SizeBytes:           int64(len(bodyBytes)),
		SHA256:              hash,
		PreviousSegmentHash: prevHash,
	}
	if err := audit.WriteManifestAtomic(audit.ManifestPathFor(segPath), man); err != nil {
		t.Fatalf("writeManifest: %v", err)
	}
	return hash
}

// ── VerifyDirectoryWithPolicy tests ──────────────────────────────────────────

func TestVerifyDirectoryWithPolicy_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	result, err := audit.VerifyDirectoryWithPolicy(dir, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Valid {
		t.Errorf("empty dir should be valid, got issues: %v", result.AggregateIssues)
	}
	if result.SegmentsChecked != 0 {
		t.Errorf("expected 0 segments checked, got %d", result.SegmentsChecked)
	}
	if result.Segments == nil {
		t.Error("Segments slice must be non-nil even for empty dir")
	}
}

func TestVerifyDirectoryWithPolicy_ValidChain(t *testing.T) {
	dir := t.TempDir()
	h1 := writeSegment(t, dir, 1, `{"event":"a"}`, "")
	h2 := writeSegment(t, dir, 2, `{"event":"b"}`, h1)
	_ = writeSegment(t, dir, 3, `{"event":"c"}`, h2)

	result, err := audit.VerifyDirectoryWithPolicy(dir, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Valid {
		t.Errorf("expected valid chain, issues: %v", result.AggregateIssues)
	}
	if result.SegmentsChecked != 3 {
		t.Errorf("expected 3 segments, got %d", result.SegmentsChecked)
	}
	if !result.ChainValid {
		t.Error("expected chain_valid = true")
	}
	if len(result.Segments) != 3 {
		t.Errorf("expected 3 per-segment results, got %d", len(result.Segments))
	}
	for _, sr := range result.Segments {
		if !sr.Valid {
			t.Errorf("segment %s should be valid, issues: %v", sr.Segment, sr.Issues)
		}
	}
}

func TestVerifyDirectoryWithPolicy_BrokenChain(t *testing.T) {
	dir := t.TempDir()
	h1 := writeSegment(t, dir, 1, `{"event":"a"}`, "")
	// Segment 2 links to a wrong previous hash.
	writeSegment(t, dir, 2, `{"event":"b"}`, strings.Repeat("0", 64))
	_ = h1

	result, err := audit.VerifyDirectoryWithPolicy(dir, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Valid {
		t.Error("expected invalid result for broken chain")
	}
	if result.ChainValid {
		t.Error("expected chain_valid = false")
	}
	if result.StructuralIssues == 0 {
		t.Error("expected at least 1 structural issue")
	}
}

func TestVerifyDirectoryWithPolicy_ChecksumTampering(t *testing.T) {
	dir := t.TempDir()
	h1 := writeSegment(t, dir, 1, `{"event":"original"}`, "")
	_ = h1

	// Tamper with the segment body after the manifest was written.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jsonl") && !strings.HasSuffix(e.Name(), ".manifest.json") {
			_ = os.WriteFile(filepath.Join(dir, e.Name()), []byte(`{"event":"tampered"}\n`), 0o600)
		}
	}

	result, err := audit.VerifyDirectoryWithPolicy(dir, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Valid {
		t.Error("expected invalid result after tampering")
	}
	if result.Segments[0].ChecksumValid {
		t.Error("expected checksum_valid = false for tampered segment")
	}
}

func TestVerifyDirectoryWithPolicy_MissingManifest(t *testing.T) {
	dir := t.TempDir()
	h1 := writeSegment(t, dir, 1, `{"event":"a"}`, "")
	_ = h1

	// Delete the manifest.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), audit.ManifestSuffix) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}

	result, err := audit.VerifyDirectoryWithPolicy(dir, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Valid {
		t.Error("expected invalid result for missing manifest")
	}
	if result.StructuralIssues == 0 {
		t.Error("expected structural issues for missing manifest")
	}
}

func TestVerifyDirectoryWithPolicy_DeterministicOrder(t *testing.T) {
	dir := t.TempDir()
	h1 := writeSegment(t, dir, 1, `{"seq":"1"}`, "")
	h2 := writeSegment(t, dir, 2, `{"seq":"2"}`, h1)
	_ = writeSegment(t, dir, 3, `{"seq":"3"}`, h2)

	// Run twice; results must be identical.
	r1, _ := audit.VerifyDirectoryWithPolicy(dir, nil)
	r2, _ := audit.VerifyDirectoryWithPolicy(dir, nil)

	b1, _ := json.Marshal(r1)
	b2, _ := json.Marshal(r2)
	if string(b1) != string(b2) {
		t.Error("VerifyDirectoryWithPolicy results are not deterministic across runs")
	}
}

func TestVerifyDirectoryWithPolicy_SequenceGapDetected(t *testing.T) {
	dir := t.TempDir()
	h1 := writeSegment(t, dir, 1, `{"a":"1"}`, "")
	// Skip seq 2 — write seq 3 directly.
	_ = writeSegment(t, dir, 3, `{"a":"3"}`, h1)

	result, err := audit.VerifyDirectoryWithPolicy(dir, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Valid {
		t.Error("expected invalid result for sequence gap")
	}
	if result.ChainValid {
		t.Error("expected chain_valid = false for gap")
	}
	foundGap := false
	for _, issue := range result.AggregateIssues {
		if strings.Contains(issue, "missing segment in chain") {
			foundGap = true
		}
	}
	if !foundGap {
		t.Errorf("expected 'missing segment in chain' in issues, got: %v", result.AggregateIssues)
	}
}

func TestVerifyDirectoryWithPolicy_PolicySchemaVersion(t *testing.T) {
	dir := t.TempDir()
	writeSegment(t, dir, 1, `{"e":"1"}`, "")

	policy := &audit.DirPolicy{ExpectedSchemaVersion: "999"}
	result, err := audit.VerifyDirectoryWithPolicy(dir, policy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Valid {
		t.Error("expected invalid result for schema version mismatch")
	}
	if result.PolicyViolations == 0 {
		t.Error("expected policy_violations > 0")
	}
	if result.Segments[0].SchemaVersionValid {
		t.Error("expected schema_version_valid = false")
	}
}

func TestVerifyDirectoryWithPolicy_PolicySchemaVersionMatch(t *testing.T) {
	dir := t.TempDir()
	writeSegment(t, dir, 1, `{"e":"1"}`, "")

	policy := &audit.DirPolicy{ExpectedSchemaVersion: audit.SchemaVersion}
	result, err := audit.VerifyDirectoryWithPolicy(dir, policy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Valid {
		t.Errorf("expected valid result for matching schema version, issues: %v", result.AggregateIssues)
	}
}

func TestVerifyDirectoryWithPolicy_RetentionPolicy(t *testing.T) {
	dir := t.TempDir()
	// Segment closed 10 days ago — should violate a 5-day retention policy.
	closedAt := time.Now().UTC().AddDate(0, 0, -10)
	segName := audit.FormatSegmentName(1, closedAt)
	segPath := filepath.Join(dir, segName)
	bodyBytes := []byte(`{"e":"old"}` + "\n")
	_ = os.WriteFile(segPath, bodyBytes, 0o600)
	hash := audit.HashBytes(bodyBytes)
	man := audit.SegmentManifest{
		SchemaVersion: audit.SchemaVersion,
		Segment:       segName,
		Sequence:      1,
		CreatedAt:     closedAt.Add(-time.Hour),
		ClosedAt:      closedAt,
		RecordCount:   1,
		SizeBytes:     int64(len(bodyBytes)),
		SHA256:        hash,
	}
	_ = audit.WriteManifestAtomic(audit.ManifestPathFor(segPath), man)

	policy := &audit.DirPolicy{MaxRetentionDays: 5}
	result, err := audit.VerifyDirectoryWithPolicy(dir, policy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Valid {
		t.Error("expected invalid result for retention violation")
	}
	if result.PolicyViolations == 0 {
		t.Error("expected policy_violations > 0 for old segment")
	}
	if result.Segments[0].RetentionValid {
		t.Error("expected retention_valid = false")
	}
}

func TestVerifyDirectoryWithPolicy_FailFastStopsEarly(t *testing.T) {
	dir := t.TempDir()
	h1 := writeSegment(t, dir, 1, `{"a":"1"}`, "")
	h2 := writeSegment(t, dir, 2, `{"a":"2"}`, h1)
	h3 := writeSegment(t, dir, 3, `{"a":"3"}`, h2)
	_ = h3

	// Tamper segment 1 so it fails checksum.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), "000001") && strings.HasSuffix(e.Name(), ".jsonl") && !strings.HasSuffix(e.Name(), ".manifest.json") {
			_ = os.WriteFile(filepath.Join(dir, e.Name()), []byte(`{"tampered":"yes"}`+"\n"), 0o600)
		}
	}

	policy := &audit.DirPolicy{FailFast: true}
	result, err := audit.VerifyDirectoryWithPolicy(dir, policy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Valid {
		t.Error("expected invalid result")
	}
	if !result.Truncated {
		t.Error("expected truncated = true when fail-fast triggered")
	}
	// With fail-fast, not all 3 segments should have been checked.
	if result.SegmentsChecked >= 3 {
		t.Errorf("fail-fast should stop early, but checked %d segments", result.SegmentsChecked)
	}
}

func TestVerifyDirectoryWithPolicy_JSONOutput(t *testing.T) {
	dir := t.TempDir()
	h1 := writeSegment(t, dir, 1, `{"e":"1"}`, "")
	_ = writeSegment(t, dir, 2, `{"e":"2"}`, h1)

	result, err := audit.VerifyDirectoryWithPolicy(dir, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	b, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	// Deserialize and verify the key fields round-trip correctly.
	var decoded audit.DirPolicyResult
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	if decoded.SegmentsChecked != result.SegmentsChecked {
		t.Errorf("round-trip mismatch: segments_checked got %d want %d", decoded.SegmentsChecked, result.SegmentsChecked)
	}
	if len(decoded.Segments) != len(result.Segments) {
		t.Errorf("round-trip mismatch: segments len got %d want %d", len(decoded.Segments), len(result.Segments))
	}
}

func TestVerifyDirectoryWithPolicy_LoadDirPolicy(t *testing.T) {
	f, err := os.CreateTemp("", "policy-*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())

	policyJSON := `{"expected_schema_version":"1","max_retention_days":30,"require_hash_chain":true}`
	if _, err := f.WriteString(policyJSON); err != nil {
		t.Fatal(err)
	}
	f.Close()

	p, err := audit.LoadDirPolicy(f.Name())
	if err != nil {
		t.Fatalf("LoadDirPolicy failed: %v", err)
	}
	if p.ExpectedSchemaVersion != "1" {
		t.Errorf("expected schema version '1', got %q", p.ExpectedSchemaVersion)
	}
	if p.MaxRetentionDays != 30 {
		t.Errorf("expected max_retention_days 30, got %d", p.MaxRetentionDays)
	}
	if !p.RequireHashChain {
		t.Error("expected require_hash_chain = true")
	}
}

func TestVerifyDirectoryWithPolicy_DuplicateSegmentBodyDetected(t *testing.T) {
	dir := t.TempDir()
	// Write two segments with identical bodies — will have same hash.
	body := `{"dup":"yes"}`
	h1 := writeSegment(t, dir, 1, body, "")
	// Seq 2 must link to seq 1 correctly to pass chain but will be flagged for dup hash.
	writeSegment(t, dir, 2, body, h1)

	result, err := audit.VerifyDirectoryWithPolicy(dir, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Valid {
		t.Error("expected invalid result for duplicate segment bodies")
	}
	foundDup := false
	for _, issue := range result.AggregateIssues {
		if strings.Contains(issue, "identical to previously seen segment") {
			foundDup = true
		}
	}
	if !foundDup {
		t.Errorf("expected duplicate detection message, got: %v", result.AggregateIssues)
	}
}

func TestVerifyDirectoryWithPolicy_ActiveSegmentPresence(t *testing.T) {
	dir := t.TempDir()
	// Write an active segment.
	_ = os.WriteFile(filepath.Join(dir, audit.ActiveSegmentName), []byte(`{"active":"1"}`+"\n"), 0o600)

	result, err := audit.VerifyDirectoryWithPolicy(dir, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.ActivePresent {
		t.Error("expected active_present = true")
	}
	// No closed segments — still valid.
	if !result.Valid {
		t.Errorf("expected valid result with only active segment, issues: %v", result.AggregateIssues)
	}
}

func TestVerifyDirectoryWithPolicy_PerSegmentResultAlwaysPresent(t *testing.T) {
	dir := t.TempDir()
	h1 := writeSegment(t, dir, 1, `{"a":"1"}`, "")
	_ = writeSegment(t, dir, 2, `{"a":"2"}`, h1)

	result, _ := audit.VerifyDirectoryWithPolicy(dir, nil)
	if len(result.Segments) != 2 {
		t.Errorf("expected 2 per-segment results, got %d", len(result.Segments))
	}
	// Verify they are in sequence order.
	if result.Segments[0].Sequence != 1 {
		t.Errorf("expected first segment seq=1, got %d", result.Segments[0].Sequence)
	}
	if result.Segments[1].Sequence != 2 {
		t.Errorf("expected second segment seq=2, got %d", result.Segments[1].Sequence)
	}
}
