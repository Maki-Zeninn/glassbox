// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package session — migration diagnostics and MigrationStatus.
//
// MigrationStatus is the structured result returned by LoadWithMigration and
// exposed in the session JSON output so callers can distinguish "loaded and
// already current", "loaded and auto-upgraded", and "failed due to unsupported
// version" without parsing free-form error messages.
package session

import (
	"encoding/json"
	"fmt"
	"strings"
)

// MigrationStatus describes the migration outcome for a loaded session.
// It is embedded in session list/resume/load JSON output so tooling and CI
// can inspect the migration state without parsing human-readable messages.
type MigrationStatus struct {
	// Required indicates whether a migration was needed.
	Required bool `json:"required"`
	// Applied is true when at least one migration step ran successfully.
	Applied bool `json:"applied"`
	// FromVersion is the schema version the session was at before migration.
	FromVersion int `json:"from_version"`
	// ToVersion is the schema version after migration (equals SchemaVersion
	// when Applied is true; equals FromVersion when Required is false).
	ToVersion int `json:"to_version"`
	// Steps lists each migration step description that was applied, in order.
	Steps []string `json:"steps,omitempty"`
	// Unsupported is true when the stored version is outside the supported
	// range and migration cannot proceed.
	Unsupported bool `json:"unsupported,omitempty"`
	// RemediationHint is a short actionable message when Unsupported is true.
	RemediationHint string `json:"remediation_hint,omitempty"`
}

// Summary returns a concise one-line description of the migration outcome.
func (m *MigrationStatus) Summary() string {
	if m == nil {
		return "migration status unavailable"
	}
	if m.Unsupported {
		return fmt.Sprintf("session schema v%d is unsupported — %s", m.FromVersion, m.RemediationHint)
	}
	if !m.Required {
		return fmt.Sprintf("session schema v%d is current — no migration needed", m.FromVersion)
	}
	if m.Applied {
		return fmt.Sprintf("session migrated v%d → v%d (%d step(s))", m.FromVersion, m.ToVersion, len(m.Steps))
	}
	return fmt.Sprintf("session schema v%d requires migration to v%d", m.FromVersion, m.ToVersion)
}

// MarshalJSON produces a compact JSON representation, omitting the Steps
// slice when it is empty so "current" sessions have minimal output.
func (m *MigrationStatus) MarshalJSON() ([]byte, error) {
	type alias MigrationStatus // avoid infinite recursion
	a := (*alias)(m)
	return json.Marshal(a)
}

// computeMigrationStatus inspects a Data record and produces the MigrationStatus
// that UpgradeSessionData would report without mutating the session.
func computeMigrationStatus(data *Data) *MigrationStatus {
	if data == nil {
		return &MigrationStatus{Unsupported: true, RemediationHint: "session data is nil"}
	}
	r := classifySchemaVersion(data.SchemaVersion)
	ms := &MigrationStatus{
		FromVersion: data.SchemaVersion,
		ToVersion:   data.SchemaVersion,
	}
	if r.Unsupported {
		ms.Unsupported = true
		ms.RemediationHint = remediationHintForSchemaResult(r)
		return ms
	}
	if r.NeedsUpgrade {
		ms.Required = true
		// Collect the descriptions of steps that would be applied.
		for _, step := range migrationTable {
			if step.toVersion <= data.SchemaVersion {
				continue
			}
			if step.toVersion > SchemaVersion {
				break
			}
			ms.Steps = append(ms.Steps, step.description)
		}
		ms.ToVersion = SchemaVersion
	}
	return ms
}

// ApplyMigration runs UpgradeSessionData and returns the final MigrationStatus
// together with whether the session was modified. It is the canonical entry
// point for code that both wants to upgrade and needs the structured result.
func ApplyMigration(data *Data) (*MigrationStatus, bool, error) {
	if data == nil {
		return &MigrationStatus{Unsupported: true, RemediationHint: "session data is nil"}, false, fmt.Errorf("cannot migrate nil session data")
	}
	fromVersion := data.SchemaVersion
	ms := computeMigrationStatus(data)
	if ms.Unsupported {
		return ms, false, &SchemaError{Result: classifySchemaVersion(fromVersion), SessionID: data.ID}
	}
	if !ms.Required {
		return ms, false, nil
	}
	upgraded, err := UpgradeSessionData(data)
	if err != nil {
		return ms, false, err
	}
	ms.Applied = upgraded
	ms.ToVersion = data.SchemaVersion
	return ms, upgraded, nil
}

// remediationHintForSchemaResult returns a short actionable message from a
// SchemaUpgradeResult so MigrationStatus.RemediationHint is always populated.
func remediationHintForSchemaResult(r *SchemaUpgradeResult) string {
	if r.FromFuture {
		return fmt.Sprintf("upgrade Glassbox to open sessions from schema v%d (this binary supports up to v%d)",
			r.StoredVersion, r.CurrentVersion)
	}
	return fmt.Sprintf("re-run 'glassbox debug <tx-hash>' to recreate the session (minimum supported schema: v%d)",
		MinSupportedSchemaVersion)
}

// ComputeMigrationStatusForDisplay is the exported version of
// computeMigrationStatus, intended for CLI commands that need to display the
// migration status of an already-loaded session. Because Store.Load
// auto-migrates, this function always returns a "current" status for sessions
// that loaded successfully — it is primarily useful for surfacing that the
// migration ran (Applied=true) versus was never needed (Required=false).
func ComputeMigrationStatusForDisplay(data *Data) *MigrationStatus {
	return computeMigrationStatus(data)
}

// FormatMigrationStatus returns a multi-line human-readable migration report
// for CLI output (resume, doctor, session list --verbose). It is intentionally
// separate from Summary() so verbose and terse paths can diverge.
func FormatMigrationStatus(ms *MigrationStatus, sessionID string) string {
	if ms == nil {
		return "  migration status: unavailable"
	}
	var sb strings.Builder
	prefix := "  "
	if sessionID != "" {
		fmt.Fprintf(&sb, "%sSession %s migration:\n", prefix, sessionID)
		prefix = "    "
	}
	fmt.Fprintf(&sb, "%sFrom schema:  v%d\n", prefix, ms.FromVersion)
	fmt.Fprintf(&sb, "%sTo schema:    v%d\n", prefix, ms.ToVersion)
	if ms.Unsupported {
		fmt.Fprintf(&sb, "%sStatus:       UNSUPPORTED\n", prefix)
		fmt.Fprintf(&sb, "%sHint:         %s\n", prefix, ms.RemediationHint)
		return sb.String()
	}
	if !ms.Required {
		fmt.Fprintf(&sb, "%sStatus:       current (no migration needed)\n", prefix)
		return sb.String()
	}
	if ms.Applied {
		fmt.Fprintf(&sb, "%sStatus:       migrated (%d step(s))\n", prefix, len(ms.Steps))
	} else {
		fmt.Fprintf(&sb, "%sStatus:       pending\n", prefix)
	}
	for i, step := range ms.Steps {
		fmt.Fprintf(&sb, "%sStep %d:       %s\n", prefix, i+1, step)
	}
	return sb.String()
}
