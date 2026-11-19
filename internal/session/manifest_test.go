// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package session

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ── BuildManifest / VerifyManifest ───────────────────────────────────────────

func TestBuildManifest_OnlyRecordsPresentMembers(t *testing.T) {
	m := BuildManifest(map[string][]byte{
		"metadata": []byte(`{"id":"x"}`),
		"trace":    []byte(`{"trace":true}`),
	}, SchemaVersion, time.Now())

	if len(m.Members) != 2 {
		t.Fatalf("expected 2 members, got %d: %v", len(m.Members), m.Members)
	}
	if _, ok := m.Members["metadata"]; !ok {
		t.Error("expected metadata member")
	}
	if _, ok := m.Members["bundle"]; ok {
		t.Error("bundle should not be recorded when absent")
	}
}

func TestBuildManifest_Deterministic(t *testing.T) {
	members := map[string][]byte{"metadata": []byte(`{"id":"x"}`)}
	ts := time.Now()
	a := BuildManifest(members, SchemaVersion, ts)
	b := BuildManifest(members, SchemaVersion, ts)
	if a.Members["metadata"] != b.Members["metadata"] {
		t.Error("hash of identical content must be identical across builds")
	}
}

func TestVerifyManifest_NilManifest_CompatibleNotOK_ButNoIssues(t *testing.T) {
	report := VerifyManifest(nil, map[string][]byte{"metadata": []byte(`{}`)})
	if !report.OK {
		t.Error("nil manifest (old session) must not fail verification")
	}
	if report.Compatible {
		t.Error("nil manifest should report Compatible=false — documented compatibility path")
	}
}

func TestVerifyManifest_MatchingMembers_OK(t *testing.T) {
	members := map[string][]byte{
		"metadata": []byte(`{"id":"x"}`),
		"trace":    []byte(`{"a":1}`),
	}
	m := BuildManifest(members, SchemaVersion, time.Now())
	report := VerifyManifest(m, members)
	if !report.OK {
		t.Fatalf("expected OK, got issues: %+v", report.Issues)
	}
	if !report.Compatible {
		t.Error("a present manifest should report Compatible=true")
	}
}

func TestVerifyManifest_AlteredMember_IdentifiedByName(t *testing.T) {
	original := map[string][]byte{
		"metadata": []byte(`{"id":"x"}`),
		"trace":    []byte(`{"a":1}`),
	}
	m := BuildManifest(original, SchemaVersion, time.Now())

	tampered := map[string][]byte{
		"metadata": []byte(`{"id":"x"}`),
		"trace":    []byte(`{"a":999}`), // altered after manifest was built
	}
	report := VerifyManifest(m, tampered)
	if report.OK {
		t.Fatal("expected verification failure for altered member")
	}
	found := false
	for _, issue := range report.Issues {
		if issue.Member == "trace" {
			found = true
			if strings.TrimSpace(issue.Hint) == "" {
				t.Error("altered-member issue should carry a hint")
			}
		}
	}
	if !found {
		t.Errorf("expected an issue naming the altered 'trace' member, got: %+v", report.Issues)
	}
}

func TestVerifyManifest_MissingMember_Reported(t *testing.T) {
	original := map[string][]byte{
		"metadata": []byte(`{"id":"x"}`),
		"bundle":   []byte(`{"b":1}`),
	}
	m := BuildManifest(original, SchemaVersion, time.Now())

	// "bundle" was recorded in the manifest but is absent from the archive.
	incomplete := map[string][]byte{"metadata": []byte(`{"id":"x"}`)}
	report := VerifyManifest(m, incomplete)
	if report.OK {
		t.Fatal("expected verification failure for a missing member")
	}
	found := false
	for _, issue := range report.Issues {
		if issue.Member == "bundle" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an issue naming the missing 'bundle' member, got: %+v", report.Issues)
	}
}

func TestVerifyManifest_UnexpectedMember_Reported(t *testing.T) {
	m := BuildManifest(map[string][]byte{"metadata": []byte(`{"id":"x"}`)}, SchemaVersion, time.Now())

	// "annotations" is present in the archive but was never recorded.
	extra := map[string][]byte{
		"metadata":    []byte(`{"id":"x"}`),
		"annotations": []byte(`{"note":"hi"}`),
	}
	report := VerifyManifest(m, extra)
	if report.OK {
		t.Fatal("expected verification failure for an unrecorded member")
	}
	found := false
	for _, issue := range report.Issues {
		if issue.Member == "annotations" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an issue naming the unrecorded 'annotations' member, got: %+v", report.Issues)
	}
}

// ── Archive round trip with manifest ────────────────────────────────────────

func TestExportArchive_WithArtifacts_VerifiesOnImport(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "session.gbx")

	original := sampleData()
	original.TraceJSON = `{"trace":"data"}`
	original.BundleJSON = `{"bundle":"data"}`
	original.SourceMapJSON = `{"sm":"data"}`
	original.AnnotationsJSON = `{"note":"hello"}`

	if err := ExportArchive(original, archivePath); err != nil {
		t.Fatalf("ExportArchive failed: %v", err)
	}

	restored, report, err := ImportArchiveWithManifest(archivePath)
	if err != nil {
		t.Fatalf("ImportArchiveWithManifest failed: %v", err)
	}
	if !report.OK || !report.Compatible {
		t.Fatalf("expected a valid, compatible manifest report, got: %+v", report)
	}
	if restored.TraceJSON != original.TraceJSON {
		t.Errorf("TraceJSON mismatch: got %q, want %q", restored.TraceJSON, original.TraceJSON)
	}
	if restored.BundleJSON != original.BundleJSON {
		t.Errorf("BundleJSON mismatch: got %q, want %q", restored.BundleJSON, original.BundleJSON)
	}
	if restored.SourceMapJSON != original.SourceMapJSON {
		t.Errorf("SourceMapJSON mismatch: got %q, want %q", restored.SourceMapJSON, original.SourceMapJSON)
	}
	if restored.AnnotationsJSON != original.AnnotationsJSON {
		t.Errorf("AnnotationsJSON mismatch: got %q, want %q", restored.AnnotationsJSON, original.AnnotationsJSON)
	}
}

func TestImportArchive_TamperedMember_Rejected(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "session.gbx")

	original := sampleData()
	original.TraceJSON = `{"trace":"original"}`
	if err := ExportArchive(original, archivePath); err != nil {
		t.Fatalf("ExportArchive failed: %v", err)
	}

	// Tamper with trace.json after export without updating the manifest.
	rewriteZipEntry(t, archivePath, "trace.json", []byte(`{"trace":"tampered"}`))

	_, report, err := ImportArchiveWithManifest(archivePath)
	if err == nil {
		t.Fatal("expected error importing an archive with a tampered member")
	}
	if report == nil || report.OK {
		t.Fatalf("expected a failing manifest report, got: %+v", report)
	}
	found := false
	for _, issue := range report.Issues {
		if issue.Member == "trace" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected the tampered 'trace' member to be named in the report, got: %+v", report.Issues)
	}
}

// ── Old sessions without a manifest ──────────────────────────────────────────

func TestImportArchive_NoManifest_CompatibilityPath(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "legacy.gbx")

	// Build an archive exactly as an older Glassbox version would: meta.json
	// and session.json only, no manifest.json.
	meta := archiveMeta{
		ArchiveVersion:  archiveVersion,
		GlassboxVersion: "0.0.0",
		CreatedAt:       "2026-01-01T00:00:00Z",
		SchemaVersion:   SchemaVersion,
	}
	metaBytes, _ := json.Marshal(meta)
	sessionBytes, _ := json.Marshal(sampleData())

	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("meta.json")
	_, _ = w.Write(metaBytes)
	w, _ = zw.Create("session.json")
	_, _ = w.Write(sessionBytes)
	_ = zw.Close()
	_ = f.Close()

	restored, report, err := ImportArchiveWithManifest(archivePath)
	if err != nil {
		t.Fatalf("legacy archive without a manifest must still load, got: %v", err)
	}
	if restored == nil {
		t.Fatal("expected a restored session for a legacy archive")
	}
	if !report.OK {
		t.Errorf("legacy archive should not fail verification, got issues: %+v", report.Issues)
	}
	if report.Compatible {
		t.Error("legacy archive without manifest.json should report Compatible=false")
	}
}

// ── Duplicate members ────────────────────────────────────────────────────────

func TestImportArchive_DuplicateSessionJSON_Rejected(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "dup.gbx")

	meta := archiveMeta{
		ArchiveVersion:  archiveVersion,
		GlassboxVersion: "0.0.0",
		CreatedAt:       "2026-01-01T00:00:00Z",
		SchemaVersion:   SchemaVersion,
	}
	metaBytes, _ := json.Marshal(meta)
	sessionBytes, _ := json.Marshal(sampleData())

	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("meta.json")
	_, _ = w.Write(metaBytes)
	w, _ = zw.Create("session.json")
	_, _ = w.Write(sessionBytes)
	// Duplicate entry — a corrupt or tampered archive.
	w, _ = zw.Create("session.json")
	_, _ = w.Write(sessionBytes)
	_ = zw.Close()
	_ = f.Close()

	_, err = ImportArchive(archivePath)
	if err == nil {
		t.Fatal("expected error for archive with duplicate session.json entries")
	}
	if !strings.Contains(err.Error(), "duplicate") && !strings.Contains(err.Error(), "copies") {
		t.Errorf("error should mention the duplicate entry, got: %v", err)
	}
}

func TestImportArchive_DuplicateManifestJSON_Rejected(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "dup_manifest.gbx")

	original := sampleData()
	exportPath := filepath.Join(dir, "clean.gbx")
	if err := ExportArchive(original, exportPath); err != nil {
		t.Fatalf("ExportArchive failed: %v", err)
	}

	// Copy the clean archive's entries but duplicate manifest.json.
	zr, err := zip.OpenReader(exportPath)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()

	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, entry := range zr.File {
		raw := readZipEntryBytes(t, entry)
		w, _ := zw.Create(entry.Name)
		_, _ = w.Write(raw)
		if entry.Name == "manifest.json" {
			// Write it a second time to create a duplicate.
			w, _ = zw.Create("manifest.json")
			_, _ = w.Write(raw)
		}
	}
	_ = zw.Close()
	_ = f.Close()

	_, err = ImportArchive(archivePath)
	if err == nil {
		t.Fatal("expected error for archive with duplicate manifest.json entries")
	}
	if !strings.Contains(err.Error(), "manifest.json") {
		t.Errorf("error should name manifest.json, got: %v", err)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func readZipEntryBytes(t *testing.T, f *zip.File) []byte {
	t.Helper()
	rc, err := f.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	buf := make([]byte, f.UncompressedSize64)
	n := 0
	for {
		read, readErr := rc.Read(buf[n:])
		n += read
		if readErr != nil {
			break
		}
	}
	return buf[:n]
}

// rewriteZipEntry rewrites a single named entry's content in place, leaving
// every other entry byte-identical, to simulate post-export tampering.
func rewriteZipEntry(t *testing.T, archivePath, name string, newContent []byte) {
	t.Helper()

	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()

	tmpPath := archivePath + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, entry := range zr.File {
		w, err := zw.Create(entry.Name)
		if err != nil {
			t.Fatal(err)
		}
		if entry.Name == name {
			if _, err := w.Write(newContent); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if _, err := w.Write(readZipEntryBytes(t, entry)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmpPath, archivePath); err != nil {
		t.Fatal(err)
	}
}
