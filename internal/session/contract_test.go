// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// contract_test.go is the fixture-contract suite for Issue #567: it runs a
// representative set of session shapes (valid, legacy, corrupt, encrypted,
// redacted, partially written) through validation, migration, integrity,
// export, and import, pinning the exact outcome for each so regressions in
// any of these layers are caught immediately.
//
// "Encrypted" and "redacted" sessions have no dedicated feature in this
// codebase (sessions are plaintext SQLite; ExportArchive does no field
// redaction), so those two classes are tested against the closest real
// behavior instead of an invented one:
//   - encrypted  -> SimRequestJSON/SimResponseJSON containing an opaque,
//     non-JSON blob (stand-in for "this field is encrypted at rest"),
//     proving ToSimulationRequest/ToSimulationResponse fail cleanly.
//   - redacted   -> the existing sanitize.go helpers (RedactTxHash,
//     SanitizeErrorMessage) as actually used by the CLI/store today.
package session_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotandev/glassbox/internal/session"
	"github.com/dotandev/glassbox/internal/testhelpers"
)

func overrideTempHome(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	_ = os.MkdirAll(filepath.Join(dir, ".Glassbox"), 0755)
}

func newContractStore(t *testing.T) *session.Store {
	t.Helper()
	overrideTempHome(t)
	store, err := session.NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// hasIssue reports whether report contains an issue for the given field.
func hasIssue(report *session.IntegrityReport, field string) bool {
	for _, issue := range report.Issues {
		if strings.EqualFold(issue.Field, field) {
			return true
		}
	}
	return false
}

// ─────────────────────────────────────────────────────────────────────────────
// Fixture class: valid
// ─────────────────────────────────────────────────────────────────────────────

func TestContract_Valid_PassesIntegrityAndRoundTrips(t *testing.T) {
	// Failure class: a well-formed session must have zero integrity issues
	// and must round-trip through Save/Load unchanged.
	data := testhelpers.NewSessionFixture().Build()

	report := session.ValidateIntegrity(data)
	if !report.OK {
		t.Fatalf("expected a valid fixture to pass integrity, got issues: %+v", report.Issues)
	}

	store := newContractStore(t)
	ctx := context.Background()
	if err := store.Save(ctx, data); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := store.Load(ctx, data.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.TxHash != data.TxHash {
		t.Errorf("TxHash = %q, want %q", loaded.TxHash, data.TxHash)
	}
	if loaded.SchemaVersion != session.SchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", loaded.SchemaVersion, session.SchemaVersion)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Fixture class: legacy
// ─────────────────────────────────────────────────────────────────────────────

func TestContract_Legacy_UpgradesOnLoad(t *testing.T) {
	// Failure class: a session written by an older schema version must be
	// auto-migrated on load, not rejected and not silently left stale.
	if session.SchemaVersion <= session.MinSupportedSchemaVersion {
		t.Skip("no legacy schema version exists below the current one")
	}
	data := testhelpers.NewSessionFixture().Legacy().Build()

	upgraded, err := session.UpgradeSessionData(data)
	if err != nil {
		t.Fatalf("UpgradeSessionData: %v", err)
	}
	if !upgraded {
		t.Fatal("expected a legacy fixture to report upgraded=true")
	}
	if data.SchemaVersion != session.SchemaVersion {
		t.Errorf("SchemaVersion after upgrade = %d, want %d", data.SchemaVersion, session.SchemaVersion)
	}
}

func TestContract_TooNew_RejectedBeforeUpgrade(t *testing.T) {
	// Failure class: a session from a newer binary must be rejected with a
	// SchemaError, not silently "upgraded" backwards or corrupted.
	data := testhelpers.NewSessionFixture().TooNew().Build()

	_, err := session.UpgradeSessionData(data)
	if err == nil {
		t.Fatal("expected UpgradeSessionData to reject a from-the-future schema version")
	}
	if !session.IsSchemaError(err) {
		t.Errorf("expected a *SchemaError, got: %v", err)
	}

	report := session.ValidateIntegrity(testhelpers.NewSessionFixture().TooNew().Build())
	if report.OK {
		t.Fatal("expected ValidateIntegrity to flag a too-new schema version")
	}
	if !hasIssue(report, "SchemaVersion") {
		t.Errorf("expected a SchemaVersion issue, got: %+v", report.Issues)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Fixture class: corrupt
// ─────────────────────────────────────────────────────────────────────────────

func TestContract_Corrupt_AuditChainVariants(t *testing.T) {
	// Failure class: every audit-chain corruption variant must be caught by
	// ValidateIntegrity with a stable, specific Field name — not a generic
	// error and not a silent pass.
	cases := []struct {
		name      string
		build     func() *session.Data
		wantField string
	}{
		{
			name:      "corrupt audit hash",
			build:     func() *session.Data { return testhelpers.NewSessionFixture().CorruptAuditHash().Build() },
			wantField: "AuditHash",
		},
		{
			name:      "corrupt audit signature",
			build:     func() *session.Data { return testhelpers.NewSessionFixture().CorruptAuditSignature().Build() },
			wantField: "AuditSignature",
		},
		{
			name:      "self-referential chain",
			build:     func() *session.Data { return testhelpers.NewSessionFixture().SelfReferentialChain().Build() },
			wantField: "PreviousSessionHash",
		},
		{
			name:      "orphaned signature without hash",
			build:     func() *session.Data { return testhelpers.NewSessionFixture().OrphanedAuditSignature().Build() },
			wantField: "AuditHash",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report := session.ValidateIntegrity(tc.build())
			if report.OK {
				t.Fatalf("expected corrupt fixture %q to fail integrity", tc.name)
			}
			if !hasIssue(report, tc.wantField) {
				t.Errorf("expected an issue for field %q, got: %+v", tc.wantField, report.Issues)
			}
		})
	}
}

func TestContract_Corrupt_TimestampsOutOfOrder(t *testing.T) {
	// Failure class: LastAccessAt preceding CreatedAt is an internally
	// inconsistent record and must be flagged, not accepted.
	data := testhelpers.NewSessionFixture().TimestampsOutOfOrder().Build()
	report := session.ValidateIntegrity(data)
	if report.OK {
		t.Fatal("expected out-of-order timestamps to fail integrity")
	}
	if !hasIssue(report, "LastAccessAt") {
		t.Errorf("expected a LastAccessAt issue, got: %+v", report.Issues)
	}
}

func TestContract_Corrupt_RejectedByExportArchive(t *testing.T) {
	// Failure class: exporting a corrupt session must fail before any bytes
	// are written to disk (no partial/unusable archive left behind).
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt.gbx")
	data := testhelpers.NewSessionFixture().CorruptAuditHash().Build()

	err := session.ExportArchive(data, path)
	if err == nil {
		t.Fatal("expected ExportArchive to reject a corrupt session")
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Error("ExportArchive must not leave a partial archive file for a rejected session")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Fixture class: encrypted (opaque, non-JSON payload fields)
// ─────────────────────────────────────────────────────────────────────────────

func TestContract_Encrypted_SimIOFailsCleanly(t *testing.T) {
	// Failure class: an opaque (e.g. encrypted-at-rest) SimRequestJSON /
	// SimResponseJSON blob must produce a wrapped error, not a panic, when
	// the caller tries to decode it back into a typed request/response.
	data := testhelpers.NewSessionFixture().EncryptedBlob().Build()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ToSimulationRequest/ToSimulationResponse must not panic on an opaque blob, got: %v", r)
		}
	}()

	if _, err := data.ToSimulationRequest(); err == nil {
		t.Error("expected ToSimulationRequest to fail on a non-JSON blob")
	}
	if _, err := data.ToSimulationResponse(); err == nil {
		t.Error("expected ToSimulationResponse to fail on a non-JSON blob")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Fixture class: redacted
// ─────────────────────────────────────────────────────────────────────────────

func TestContract_Redacted_TxHashNeverLeaksInFull(t *testing.T) {
	// Failure class: redacted output (as used for diagnostics/sharing) must
	// never contain the full transaction hash.
	data := testhelpers.NewSessionFixture().Build()

	redacted := session.RedactTxHash(data.TxHash)
	if redacted == data.TxHash {
		t.Fatal("RedactTxHash must shorten a full-length transaction hash")
	}
	if strings.Contains(redacted, data.TxHash) {
		t.Error("redacted hash must not contain the full original hash as a substring")
	}
}

func TestContract_Redacted_ErrorMessagesStripHomePath(t *testing.T) {
	// Failure class: sanitized error output (used before logging/CLI display)
	// must not leak the user's home directory.
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no resolvable home directory in this environment")
	}
	msg := "failed to open " + home + "/.Glassbox/sessions.db"
	sanitized := session.SanitizeErrorMessage(msg)
	if strings.Contains(sanitized, home) {
		t.Errorf("sanitized message still contains home path: %q", sanitized)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Fixture class: partially written
// ─────────────────────────────────────────────────────────────────────────────

func TestContract_PartiallyWritten_FlaggedByIntegrity(t *testing.T) {
	// Failure class: a session interrupted mid-write (zero timestamps) must
	// be flagged by ValidateIntegrity so recovery/doctor tooling can surface
	// it, rather than silently treating it as a valid, current session.
	data := testhelpers.NewSessionFixture().PartiallyWritten().Build()
	report := session.ValidateIntegrity(data)
	if report.OK {
		t.Fatal("expected a partially-written session (zero timestamps) to fail integrity")
	}
	if !hasIssue(report, "CreatedAt") {
		t.Errorf("expected a CreatedAt issue, got: %+v", report.Issues)
	}
	if !hasIssue(report, "LastAccessAt") {
		t.Errorf("expected a LastAccessAt issue, got: %+v", report.Issues)
	}
}

func TestContract_PartiallyWritten_DetectedByStoreDiagnostics(t *testing.T) {
	// Failure class: a partially-written session already persisted to the
	// store must show up as degraded in RunStoreDiagnostics, mirroring what
	// 'glassbox session doctor' reports to the user.
	store := newContractStore(t)
	ctx := context.Background()

	valid := testhelpers.NewSessionFixture().WithID("contract-valid-1").Build()
	if err := store.Save(ctx, valid); err != nil {
		t.Fatalf("Save valid: %v", err)
	}

	// Save() always stamps fresh timestamps and the current SchemaVersion, so
	// a zero-timestamp row can't be persisted through the public Save() API
	// (that class is validated at the Data level in
	// TestContract_PartiallyWritten_FlaggedByIntegrity). SavePreservingSchemaVersion
	// is the one API that can seed a persisted row degraded in the way store
	// diagnostics actually detects: a stored schema_version ValidateIntegrity
	// won't silently accept.
	degraded := testhelpers.NewSessionFixture().WithID("contract-degraded-1").TooNew().Build()
	if err := store.SavePreservingSchemaVersion(ctx, degraded); err != nil {
		t.Fatalf("SavePreservingSchemaVersion degraded: %v", err)
	}

	result, err := store.RunStoreDiagnostics(ctx)
	if err != nil {
		t.Fatalf("RunStoreDiagnostics: %v", err)
	}
	if result.DegradedSessions < 1 {
		t.Errorf("expected at least 1 degraded session, got %d", result.DegradedSessions)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Cross-layer agreement: export/import must apply the same integrity rules
// as ValidateIntegrity itself.
// ─────────────────────────────────────────────────────────────────────────────

func TestContract_ExportImport_RoundTripPreservesFixtureClass(t *testing.T) {
	// Failure class: a valid session exported and re-imported must still be
	// reported valid by the same ValidateIntegrity check used at save time.
	dir := t.TempDir()
	path := filepath.Join(dir, "valid.gbx")
	data := testhelpers.NewSessionFixture().WithID("contract-export-1").Build()

	if err := session.ExportArchive(data, path); err != nil {
		t.Fatalf("ExportArchive: %v", err)
	}
	imported, err := session.ImportArchive(path)
	if err != nil {
		t.Fatalf("ImportArchive: %v", err)
	}
	report := session.ValidateIntegrity(imported)
	if !report.OK {
		t.Errorf("re-imported valid session should still pass integrity, got issues: %+v", report.Issues)
	}
}
