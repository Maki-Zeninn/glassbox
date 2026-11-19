// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// migration_test.go covers the audit segment manifest's schema_version
// compatibility contract [Issue #872]: manifests written before the
// schema_version field existed must still verify (forward loading of
// legacy artifacts), and manifests declaring a schema_version this binary
// does not recognize must be rejected rather than silently accepted.

package audit

import (
	"os"
	"strings"
	"testing"
)

// TestVerifyDirectory_LegacyManifestNoSchemaVersion_StillValid simulates a
// segment manifest written by a pre-versioning build of Glassbox (no
// schema_version field, so it deserializes as ""). VerifyDirectory must still
// treat the segment as valid: SchemaVersion is empty-tolerant by design (see
// verify.go's `manifestSV != "" && manifestSV != SchemaVersion` check), which
// is the compatibility guarantee this test pins down.
func TestVerifyDirectory_LegacyManifestNoSchemaVersion_StillValid(t *testing.T) {
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
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	segments, err := ListSegments(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 1 {
		t.Fatalf("need 1 segment, got %d", len(segments))
	}

	// Rewrite the manifest with schema_version stripped, as a legacy
	// (pre-versioning) manifest would look on disk.
	m, err := ReadManifest(segments[0].ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	m.SchemaVersion = ""
	if err := WriteManifestAtomic(segments[0].ManifestPath, *m); err != nil {
		t.Fatal(err)
	}

	result, err := VerifyDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid || !result.ChainValid {
		t.Fatalf("expected legacy (schema_version-less) manifest to verify, got %+v", result)
	}
}

// TestVerifyDirectory_UnsupportedSchemaVersion_Rejected simulates a manifest
// declaring a schema_version this binary does not recognize (e.g. written by
// a future Glassbox release). VerifyDirectory must flag it as invalid rather
// than silently accepting an artifact it cannot fully interpret.
func TestVerifyDirectory_UnsupportedSchemaVersion_Rejected(t *testing.T) {
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
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	segments, err := ListSegments(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 1 {
		t.Fatalf("need 1 segment, got %d", len(segments))
	}

	m, err := ReadManifest(segments[0].ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	m.SchemaVersion = "99"
	if err := WriteManifestAtomic(segments[0].ManifestPath, *m); err != nil {
		t.Fatal(err)
	}

	result, err := VerifyDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if result.Valid {
		t.Fatal("expected verification failure for unsupported schema_version")
	}
	found := false
	for _, issue := range result.Issues {
		if strings.Contains(issue, "unsupported schema_version") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected unsupported schema_version issue, got %v", result.Issues)
	}
}

// TestReadManifest_Roundtrip_PreservesSchemaVersion is a narrow sanity check
// that ReadManifest/WriteManifestAtomic roundtrip schema_version faithfully,
// which the two tests above depend on to construct their fixtures.
func TestReadManifest_Roundtrip_PreservesSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	path := dir + string(os.PathSeparator) + "segment-000001-20260101T000000Z.manifest.json"
	m := SegmentManifest{
		SchemaVersion: SchemaVersion,
		Segment:       "segment-000001-20260101T000000Z.jsonl",
		Sequence:      1,
	}
	if err := WriteManifestAtomic(path, m); err != nil {
		t.Fatal(err)
	}
	got, err := ReadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != SchemaVersion {
		t.Fatalf("SchemaVersion roundtrip = %q, want %q", got.SchemaVersion, SchemaVersion)
	}
}
