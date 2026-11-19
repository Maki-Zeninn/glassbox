// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// migration.go — Snapshot schema version negotiation and migration hooks.
//
// Design:
//   - PersistSchemaVersion (in persist.go) is the version writers emit.
//     Currently 2 after this change.
//   - MinSupportedSchemaVersion (in schema.go) is the oldest version readers
//     accept without error. Currently 1.
//   - Versions between Min and Current are migrated automatically on load
//     using the migrationTable below.
//   - Versions > Current (from a newer binary) fail with a clear error and
//     remediation hint before any mutation occurs.
//
// Adding a migration:
//  1. Increment PersistSchemaVersion in persist.go.
//  2. Append a migrationStep to migrationTable below.
//  3. Write a test in migration_test.go that round-trips the v(N-1) fixture.
//
// Version history:
//   v1 — original format; ledger entries + linear memory + fingerprint.
//   v2 — adds metadata.ledger_format field ("base64-xdr", default "base64-xdr")
//          and metadata.migration_log array tracking automatic upgrades.

package snapshot

import (
	"fmt"
	"time"
)

// MigrationStep is a single schema version upgrade applied to a
// PersistedSnapshot. Each step moves the snapshot from (toVersion-1) to
// toVersion. Steps are idempotent: calling a step on data already at or past
// its target is a safe no-op.
type MigrationStep struct {
	// ToVersion is the schema version produced by this step.
	ToVersion int
	// Description is a short human-readable label recorded in MigrationLog.
	Description string
	// Migrate performs the in-place transformation.
	Migrate func(ps *PersistedSnapshot)
}

// MigrationLogEntry records one automatic migration applied on load.
// It is appended to ReplayMetadata.MigrationLog so callers can audit
// what was changed.
type MigrationLogEntry struct {
	// FromVersion is the schema version before this step ran.
	FromVersion int `json:"from_version"`
	// ToVersion is the schema version after this step ran.
	ToVersion int `json:"to_version"`
	// Description is the step's human-readable label.
	Description string `json:"description"`
	// AppliedAt is when the migration ran (UTC).
	AppliedAt time.Time `json:"applied_at"`
}

// migrationTable is the ordered list of schema upgrade steps. Steps are
// applied in ascending toVersion order so each step only has to reason about
// one version transition.
var migrationTable = []MigrationStep{
	{
		ToVersion:   2,
		Description: "add ledger_format field (default base64-xdr)",
		Migrate: func(ps *PersistedSnapshot) {
			if ps.Metadata == nil {
				return
			}
			// v1 snapshots have no LedgerFormat; default to "base64-xdr".
			if ps.Metadata.LedgerFormat == "" {
				ps.Metadata.LedgerFormat = "base64-xdr"
			}
		},
	},
}

// MigrateSnapshot upgrades ps from its current schema version to
// PersistSchemaVersion using migrationTable. It returns the list of steps
// that were applied (empty if ps was already current) and an error if the
// version is unsupported.
//
// The caller is responsible for saving the migrated snapshot if they want
// the upgrade to be durable. MigrateSnapshot never writes to disk.
func MigrateSnapshot(ps *PersistedSnapshot) ([]MigrationLogEntry, error) {
	if ps == nil || ps.Metadata == nil {
		return nil, fmt.Errorf("cannot migrate nil snapshot or snapshot without metadata")
	}

	r := classifySchemaVersion(ps.Metadata.SchemaVersion)
	if r.Unsupported {
		return nil, &SchemaError{Result: r, Path: ""}
	}
	if !r.NeedsUpgrade {
		return nil, nil // already current
	}

	var applied []MigrationLogEntry
	for _, step := range migrationTable {
		if step.ToVersion <= ps.Metadata.SchemaVersion {
			continue // already past this step
		}
		if step.ToVersion > PersistSchemaVersion {
			break // safety: never apply steps beyond the known current
		}
		from := ps.Metadata.SchemaVersion
		step.Migrate(ps)
		ps.Metadata.SchemaVersion = step.ToVersion
		entry := MigrationLogEntry{
			FromVersion: from,
			ToVersion:   step.ToVersion,
			Description: step.Description,
			AppliedAt:   time.Now().UTC(),
		}
		applied = append(applied, entry)
		ps.Metadata.MigrationLog = append(ps.Metadata.MigrationLog, entry)
	}
	// Ensure we land exactly on PersistSchemaVersion even if the table has gaps.
	ps.Metadata.SchemaVersion = PersistSchemaVersion
	return applied, nil
}

// MigrationResult is the outcome of a migration attempted during LoadPersisted.
type MigrationResult struct {
	// WasUpgraded is true when at least one migration step ran.
	WasUpgraded bool
	// Steps describes each step that was applied.
	Steps []MigrationLogEntry
	// FromVersion is the schema version the file was loaded with.
	FromVersion int
}

// Summary returns a one-line human-readable description suitable for logs.
func (r *MigrationResult) Summary() string {
	if r == nil || !r.WasUpgraded {
		return fmt.Sprintf("schema version %d — no migration needed", PersistSchemaVersion)
	}
	return fmt.Sprintf("migrated from v%d to v%d (%d step(s))",
		r.FromVersion, PersistSchemaVersion, len(r.Steps))
}
