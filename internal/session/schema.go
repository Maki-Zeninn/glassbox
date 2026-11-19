// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package session

import (
	"fmt"
	"strings"

	"github.com/dotandev/glassbox/internal/version"
)

// MinSupportedSchemaVersion is the oldest session schema version that can be
// loaded without manual regeneration. Rows older than this must be re-debugged.
const MinSupportedSchemaVersion = 1

// CurrentSchemaVersion is bumped each time a breaking change is made to the
// session file format. To add a migration, increment this constant and add a
// corresponding case to the migration switch in migration.go (migrationTable
// in this file). The version history is:
//
//   v1 — initial schema; backfills env_fingerprint on legacy rows.
//   v2 — normalises empty Status field to "active".
//   v3 — backfills audit-chain sentinel and revision baseline.
//
// NOTE: SchemaVersion is defined in store.go (currently 5). When a new migration
// step is appended to migrationTable below, increment that constant to match the
// highest toVersion in the table.

// SchemaUpgradeResult describes the outcome of a schema check or migration
// attempt so callers can decide whether to abort, warn, or proceed silently.
type SchemaUpgradeResult struct {
	// StoredVersion is the schema version found in the session row.
	StoredVersion int
	// CurrentVersion is the version this binary expects.
	CurrentVersion int
	// NeedsUpgrade is true when StoredVersion < CurrentVersion and the row
	// can be migrated automatically.
	NeedsUpgrade bool
	// Unsupported is true when StoredVersion is outside the supported range.
	Unsupported bool
	// FromFuture is true when StoredVersion > CurrentVersion (row was
	// produced by a newer Glassbox binary).
	FromFuture bool
	// Message is a human-readable summary of the situation.
	Message string
}

// classifySchemaVersion returns a SchemaUpgradeResult for the given stored
// version relative to the current SchemaVersion constant.
func classifySchemaVersion(stored int) *SchemaUpgradeResult {
	r := &SchemaUpgradeResult{
		StoredVersion:  stored,
		CurrentVersion: SchemaVersion,
	}

	switch {
	case stored == SchemaVersion:
		r.Message = fmt.Sprintf("session schema version %d is current — no upgrade needed", stored)

	case stored < MinSupportedSchemaVersion:
		r.Unsupported = true
		r.Message = fmt.Sprintf(
			"session schema version %d is too old to load (minimum supported: %d, current: %d); "+
				"re-run 'glassbox debug <tx-hash>' to recreate the session",
			stored, MinSupportedSchemaVersion, SchemaVersion,
		)

	case stored < SchemaVersion:
		r.NeedsUpgrade = true
		r.Message = fmt.Sprintf(
			"session schema version %d is outdated (current: %d); "+
				"Glassbox will upgrade the session automatically on load",
			stored, SchemaVersion,
		)

	case stored > SchemaVersion:
		r.FromFuture = true
		r.Unsupported = true
		r.Message = fmt.Sprintf(
			"session schema version %d was produced by a newer version of Glassbox (this binary supports up to %d); "+
				"upgrade Glassbox to resume this session, or re-run 'glassbox debug <tx-hash>' with the current binary",
			stored, SchemaVersion,
		)
	}

	return r
}

// SchemaError is returned when a session's schema version is incompatible.
// It carries structured information so callers can generate targeted
// remediation messages.
type SchemaError struct {
	Result    *SchemaUpgradeResult
	SessionID string
}

func (e *SchemaError) Error() string {
	var sb strings.Builder
	if e.SessionID != "" {
		fmt.Fprintf(&sb, "session %q: %s", e.SessionID, e.Result.Message)
	} else {
		sb.WriteString(e.Result.Message)
	}
	return sb.String()
}

// IsSchemaError reports whether err is a *SchemaError.
func IsSchemaError(err error) bool {
	_, ok := err.(*SchemaError)
	return ok
}

// AsSchemaError returns the *SchemaError if err is one, or nil.
func AsSchemaError(err error) *SchemaError {
	if se, ok := err.(*SchemaError); ok {
		return se
	}
	return nil
}

// ValidateSchemaVersion returns a *SchemaError when the stored version cannot
// be loaded or requires manual regeneration. It returns nil when the version
// is current or can be upgraded automatically on load.
func ValidateSchemaVersion(stored int, sessionID string) error {
	r := classifySchemaVersion(stored)
	if r.Unsupported {
		return &SchemaError{Result: r, SessionID: sessionID}
	}
	return nil
}

// SchemaVersionSummary returns a one-line human-readable description of the
// schema version situation, suitable for verbose output or diagnostic logs.
func SchemaVersionSummary(stored int) string {
	return classifySchemaVersion(stored).Message
}

// migrationStep is a single schema version upgrade function.
// It receives the session Data at the version just *below* its target and
// mutates it in-place to reach that target. Steps must be idempotent:
// calling a step on data that is already at or past its target must be a
// safe no-op.
type migrationStep struct {
	// toVersion is the schema version produced by this step.
	toVersion int
	// description is a short human-readable label used in provenance entries.
	description string
	// migrate performs the in-place data transformation.
	migrate func(data *Data)
}

// migrationTable is the ordered list of schema upgrade steps.
// To add a new migration: append a new migrationStep with toVersion set to
// the next integer and a migrate func that applies only the changes needed to
// move from (toVersion-1) → toVersion. The table is applied in order, so
// each step only needs to reason about the single version transition it owns.
var migrationTable = []migrationStep{
	{
		toVersion:   1,
		description: "backfill env_fingerprint",
		migrate: func(data *Data) {
			// v0 → v1: legacy rows may lack env_fingerprint and pinned_endpoint.
			if data.EnvFingerprint == "" {
				data.EnvFingerprint = BuildEnvFingerprint()
			}
		},
	},
	{
		toVersion:   2,
		description: "normalise status default",
		migrate: func(data *Data) {
			// v1 → v2: rows without a status value default to "active" so that
			// the integrity validator never sees an empty Status field.
			if data.Status == "" {
				data.Status = "active"
			}
		},
	},
	{
		toVersion:   3,
		description: "backfill audit-chain sentinel and revision baseline",
		migrate: func(data *Data) {
			// v2 → v3: sessions created before the audit-chain fields existed
			// (AuditHash, AuditSignature, PreviousSessionHash) have them as empty
			// strings, which is correct — no backfill needed for the hash fields.
			//
			// Revision was added in the same release cycle. A zero revision on an
			// existing row means "no concurrent-write protection ever applied"; we
			// leave it at zero so the first Save after migration establishes the
			// baseline cleanly rather than generating a false conflict.
			//
			// ErstVersion: rows written before version stamping existed have an
			// empty ErstVersion — backfill a sentinel so diagnostics can tell the
			// difference between "version unknown" and "version not recorded".
			if data.ErstVersion == "" {
				data.ErstVersion = "pre-v3"
			}
		},
	},
	{
		toVersion:   4,
		description: "backfill tags_json empty array for pre-tagging sessions",
		migrate: func(data *Data) {
			// v3 → v4: the tags_json column is new in schema v4. Rows that
			// pre-date it have a NULL / empty value. We backfill a canonical
			// empty JSON array so the Tags() accessor never needs to handle
			// NULL and all rows are structurally valid from this version onward.
			if data.TagsJSON == "" {
				data.TagsJSON = "[]"
			}
		},
	},
	{
		toVersion:   5,
		description: "ensure migration_journal table and record schema history",
		migrate: func(data *Data) {
			// v4 → v5: MigrationJournal is a store-level table (created in
			// initSchema). The per-row schema bump records that the session
			// was opened under a binary that supports journalled migrations.
			// No Data field changes are required.
			if data.TagsJSON == "" {
				data.TagsJSON = "[]"
			}
		},
	},
}

// UpgradeSessionData migrates an in-memory session record from an older schema
// version to the current SchemaVersion using the migrationTable dispatch table.
// It is safe to call on already-current sessions and never modifies sessions
// from a newer binary.
//
// Each migration step is applied in sequence so the data always advances one
// version at a time, making it straightforward to reason about each
// transition and to add future steps without touching existing ones.
func UpgradeSessionData(data *Data) (upgraded bool, err error) {
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

	// Walk the migration table and apply every step whose target version is
	// greater than the current schema version. Each step is wrapped in
	// in-memory savepoint semantics: on fault-injection failure the pre-step
	// snapshot is restored [Issue #1115].
	for _, step := range migrationTable {
		if step.toVersion <= data.SchemaVersion {
			continue
		}
		if step.toVersion > SchemaVersion {
			break
		}
		pre := cloneSessionData(data)
		step.migrate(data)
		data.SchemaVersion = step.toVersion
		if migrationFaultHook != nil {
			if ferr := migrationFaultHook(step.toVersion); ferr != nil {
				*data = *pre
				return false, fmt.Errorf("migration to v%d rolled back: %w", step.toVersion, ferr)
			}
		}
	}

	data.SchemaVersion = SchemaVersion

	_ = RecordProvenance(data, ProvenanceMigrated, ActorSystem, version.Version, "",
		fmt.Sprintf("schema upgraded from v%d to v%d", fromVersion, SchemaVersion), true)

	return true, nil
}
