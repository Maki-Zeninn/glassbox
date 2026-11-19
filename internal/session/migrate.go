// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package session

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/dotandev/glassbox/internal/version"
)

// MigrationResultApplied and friends are the allowed MigrationJournal.result values.
const (
	MigrationResultApplied    = "applied"
	MigrationResultSkipped    = "skipped"
	MigrationResultRolledBack = "rolled_back"
)

// MigrationStep describes a single pending schema upgrade step for dry-run
// planning [Issue #1115].
type MigrationStep struct {
	FromVersion int    `json:"from_version"`
	ToVersion   int    `json:"to_version"`
	Description string `json:"description"`
}

// MigrationJournalEntry is one row of the MigrationJournal table.
type MigrationJournalEntry struct {
	FromVersion   int
	ToVersion     int
	AppliedAt     string
	BinaryVersion string
	BinaryCommit  string
	Result        string
	SessionID     string
	Description   string
}

// migrationFaultHook, when non-nil, is invoked immediately after a migration
// step's migrate func runs and before the schema version is committed. Tests
// inject failures here to simulate process interruption mid-step.
var migrationFaultHook func(toVersion int) error

// SetMigrationFaultHook installs a test-only fault hook. Pass nil to clear.
// Prefer defer SetMigrationFaultHook(nil) in tests.
func SetMigrationFaultHook(hook func(toVersion int) error) {
	migrationFaultHook = hook
}

// ensureMigrationJournal creates the MigrationJournal table if missing.
func (s *Store) ensureMigrationJournal() error {
	_, err := s.db.Exec(`
	CREATE TABLE IF NOT EXISTS migration_journal (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT,
		from_version INTEGER NOT NULL,
		to_version INTEGER NOT NULL,
		applied_at TEXT NOT NULL,
		binary_version TEXT NOT NULL,
		binary_commit TEXT NOT NULL,
		result TEXT NOT NULL,
		description TEXT
	);
	CREATE INDEX IF NOT EXISTS idx_migration_journal_session
		ON migration_journal(session_id);
	`)
	return err
}

// recordMigrationJournal writes a journal row inside an optional savepoint.
func (s *Store) recordMigrationJournal(ctx context.Context, entry MigrationJournalEntry) error {
	if s == nil || s.db == nil {
		return nil
	}
	if entry.AppliedAt == "" {
		entry.AppliedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if entry.BinaryVersion == "" {
		entry.BinaryVersion = version.Version
	}
	if entry.BinaryCommit == "" {
		entry.BinaryCommit = version.CommitSHA
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO migration_journal
			(session_id, from_version, to_version, applied_at, binary_version, binary_commit, result, description)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.SessionID, entry.FromVersion, entry.ToVersion,
		entry.AppliedAt, entry.BinaryVersion, entry.BinaryCommit,
		entry.Result, entry.Description,
	)
	return err
}

// ListMigrationJournal returns journal rows for a session (newest first).
func (s *Store) ListMigrationJournal(ctx context.Context, sessionID string) ([]MigrationJournalEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT from_version, to_version, applied_at, binary_version, binary_commit, result, COALESCE(description,''), COALESCE(session_id,'')
		FROM migration_journal
		WHERE session_id = ? OR (? = '' AND session_id IS NULL)
		ORDER BY id DESC`, sessionID, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MigrationJournalEntry
	for rows.Next() {
		var e MigrationJournalEntry
		if scanErr := rows.Scan(&e.FromVersion, &e.ToVersion, &e.AppliedAt, &e.BinaryVersion, &e.BinaryCommit, &e.Result, &e.Description, &e.SessionID); scanErr != nil {
			return nil, scanErr
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// DryRunMigrationPlan opens the session database read-only (when possible),
// reads the schema version of the named session (or the global max when
// sessionID is empty), and returns the list of steps that would be applied
// without mutating anything [Issue #1115].
func DryRunMigrationPlan(path string) ([]MigrationStep, error) {
	return DryRunMigrationPlanForSession(path, "")
}

// DryRunMigrationPlanForSession returns pending steps for a specific session ID.
func DryRunMigrationPlanForSession(path string, sessionID string) ([]MigrationStep, error) {
	db, err := sql.Open("sqlite", path+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("open session db read-only: %w", err)
	}
	defer db.Close()

	var stored int
	if sessionID != "" {
		err = db.QueryRow(`SELECT schema_version FROM sessions WHERE id = ?`, sessionID).Scan(&stored)
	} else {
		err = db.QueryRow(`SELECT COALESCE(MIN(schema_version), 0) FROM sessions`).Scan(&stored)
		if err != nil {
			// Empty DB — nothing to migrate.
			stored = SchemaVersion
			err = nil
		}
	}
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("session %q not found", sessionID)
	}
	if err != nil {
		// Fallback: treat as current when the table is missing.
		stored = SchemaVersion
	}

	return PendingMigrationSteps(stored), nil
}

// PendingMigrationSteps returns the migration steps that would run to bring
// storedVersion up to SchemaVersion.
func PendingMigrationSteps(storedVersion int) []MigrationStep {
	var steps []MigrationStep
	cur := storedVersion
	for _, step := range migrationTable {
		if step.toVersion <= cur {
			continue
		}
		if step.toVersion > SchemaVersion {
			break
		}
		steps = append(steps, MigrationStep{
			FromVersion: cur,
			ToVersion:   step.toVersion,
			Description: step.description,
		})
		cur = step.toVersion
	}
	return steps
}

// UpgradeSessionDataWithJournal migrates data one step at a time, wrapping
// each step in savepoint semantics: on failure the in-memory state is restored
// to the pre-step snapshot and a rolled_back journal row is recorded.
func (s *Store) UpgradeSessionDataWithJournal(ctx context.Context, data *Data) (bool, error) {
	if data == nil {
		return false, fmt.Errorf("cannot upgrade nil session data")
	}
	r := classifySchemaVersion(data.SchemaVersion)
	if r.Unsupported {
		return false, &SchemaError{Result: r, SessionID: data.ID}
	}
	if !r.NeedsUpgrade {
		return false, nil
	}

	fromVersion := data.SchemaVersion
	upgraded := false

	for _, step := range migrationTable {
		if step.toVersion <= data.SchemaVersion {
			continue
		}
		if step.toVersion > SchemaVersion {
			break
		}

		savepoint := fmt.Sprintf("mig_v%d", step.toVersion)
		pre := cloneSessionData(data)
		stepFrom := data.SchemaVersion

		// SQL SAVEPOINT when a live DB connection is available.
		if s != nil && s.db != nil {
			if _, err := s.db.ExecContext(ctx, "SAVEPOINT "+savepoint); err != nil {
				return upgraded, fmt.Errorf("savepoint %s: %w", savepoint, err)
			}
		}

		step.migrate(data)
		data.SchemaVersion = step.toVersion

		if migrationFaultHook != nil {
			if ferr := migrationFaultHook(step.toVersion); ferr != nil {
				// Restore in-memory snapshot (savepoint semantics).
				*data = *pre
				if s != nil && s.db != nil {
					_, _ = s.db.ExecContext(ctx, "ROLLBACK TO "+savepoint)
					_, _ = s.db.ExecContext(ctx, "RELEASE "+savepoint)
					_ = s.recordMigrationJournal(ctx, MigrationJournalEntry{
						SessionID:   data.ID,
						FromVersion: stepFrom,
						ToVersion:   step.toVersion,
						Result:      MigrationResultRolledBack,
						Description: step.description + ": " + ferr.Error(),
					})
				}
				return upgraded, fmt.Errorf("migration to v%d rolled back: %w", step.toVersion, ferr)
			}
		}

		if s != nil && s.db != nil {
			if _, err := s.db.ExecContext(ctx, "RELEASE "+savepoint); err != nil {
				*data = *pre
				return upgraded, fmt.Errorf("release %s: %w", savepoint, err)
			}
			_ = s.recordMigrationJournal(ctx, MigrationJournalEntry{
				SessionID:   data.ID,
				FromVersion: stepFrom,
				ToVersion:   step.toVersion,
				Result:      MigrationResultApplied,
				Description: step.description,
			})
		}
		upgraded = true
	}

	data.SchemaVersion = SchemaVersion
	_ = RecordProvenance(data, ProvenanceMigrated, ActorSystem, version.Version, "",
		fmt.Sprintf("schema upgraded from v%d to v%d", fromVersion, SchemaVersion), true)
	return upgraded, nil
}

func cloneSessionData(d *Data) *Data {
	if d == nil {
		return nil
	}
	cp := *d
	return &cp
}

// LoadReadOnly loads a session without running schema migrations or updating
// last_access_at. If the stored schema is older than SchemaVersion a warning
// message is returned so CLI callers can print it [Issue #1115].
func (s *Store) LoadReadOnly(ctx context.Context, sessionID string) (*Data, string, error) {
	query := `
	SELECT id, name, created_at, last_access_at, status, network, horizon_url, tx_hash,
	       envelope_xdr, result_xdr, result_meta_xdr, pinned_endpoint,
	       audit_hash, audit_signature, previous_session_hash,
	       sim_request_json, sim_response_json, env_fingerprint, provenance_json, annotations_json,
	       COALESCE(tags_json, '[]'),
	       encrypted_payload, COALESCE(revision, 0), GLASSBOX_version, schema_version
	FROM sessions
	WHERE id = ?
	`

	var data Data
	var createdAt, lastAccessAt string
	var envFP, auditHash, auditSignature, prevSessionHash, provenanceJSON, annotationsJSON, tagsJSON, encryptedPayload sql.NullString
	err := s.db.QueryRowContext(ctx, query, sessionID).Scan(
		&data.ID, &data.Name, &createdAt, &lastAccessAt, &data.Status,
		&data.Network, &data.HorizonURL, &data.TxHash,
		&data.EnvelopeXdr, &data.ResultXdr, &data.ResultMetaXdr, &data.PinnedEndpoint,
		&auditHash, &auditSignature, &prevSessionHash,
		&data.SimRequestJSON, &data.SimResponseJSON, &envFP, &provenanceJSON, &annotationsJSON,
		&tagsJSON,
		&encryptedPayload, &data.Revision, &data.ErstVersion, &data.SchemaVersion,
	)
	if err == sql.ErrNoRows {
		return nil, "", fmt.Errorf("session not found: %s", sessionID)
	}
	if err != nil {
		return nil, "", err
	}
	if envFP.Valid {
		data.EnvFingerprint = envFP.String
	}
	if auditHash.Valid {
		data.AuditHash = auditHash.String
	}
	if auditSignature.Valid {
		data.AuditSignature = auditSignature.String
	}
	if prevSessionHash.Valid {
		data.PreviousSessionHash = prevSessionHash.String
	}
	if provenanceJSON.Valid {
		data.ProvenanceJSON = provenanceJSON.String
	}
	if annotationsJSON.Valid {
		data.AnnotationsJSON = annotationsJSON.String
	}
	if tagsJSON.Valid && tagsJSON.String != "" {
		data.TagsJSON = tagsJSON.String
	} else {
		data.TagsJSON = "[]"
	}
	if data.EncryptedPayload, err = unmarshalEncryptedPayload(encryptedPayload.String); err != nil {
		return nil, "", err
	}
	if data.CreatedAt, err = time.Parse(time.RFC3339, createdAt); err != nil {
		return nil, "", err
	}
	if data.LastAccessAt, err = time.Parse(time.RFC3339, lastAccessAt); err != nil {
		return nil, "", err
	}
	if decErr := DecryptSessionPayload(&data, s.keyProvider); decErr != nil {
		return nil, "", decErr
	}
	warn := ""
	if data.SchemaVersion < SchemaVersion {
		warn = fmt.Sprintf(
			"WARNING: session schema v%d is stale (current v%d); opened read-only without migration — use 'glassbox session migrate --dry-run' to inspect the plan",
			data.SchemaVersion, SchemaVersion,
		)
	}
	return &data, warn, nil
}
