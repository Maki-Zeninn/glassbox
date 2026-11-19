// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package snapshot

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// MinSupportedSchemaVersion is the oldest schema version that can be loaded
// without a migration. Files older than this must be regenerated.
const MinSupportedSchemaVersion = 1

// SchemaUpgradeResult describes the outcome of a schema check or migration
// attempt so callers can decide whether to abort, warn, or proceed silently.
type SchemaUpgradeResult struct {
	// StoredVersion is the schema version found in the file.
	StoredVersion int
	// CurrentVersion is the version this binary expects.
	CurrentVersion int
	// NeedsUpgrade is true when StoredVersion < CurrentVersion and the file
	// can be migrated automatically.
	NeedsUpgrade bool
	// Unsupported is true when StoredVersion is outside the supported range.
	Unsupported bool
	// FromFuture is true when StoredVersion > CurrentVersion (file was
	// produced by a newer Glassbox binary).
	FromFuture bool
	// Message is a human-readable summary of the situation.
	Message string
}

// CheckSchemaVersion inspects the schema version of a persisted snapshot file
// without fully parsing it. It returns a SchemaUpgradeResult that the caller
// can use to decide how to proceed before calling LoadPersisted.
//
// This is cheaper than a full load when the only goal is to detect stale files.
func CheckSchemaVersion(path string) (*SchemaUpgradeResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read snapshot file %q: %w", path, err)
	}

	// Extract just the schema_version field without parsing the full document.
	var probe struct {
		Metadata *struct {
			SchemaVersion int `json:"schema_version"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("failed to parse snapshot file %q: %w", path, err)
	}
	if probe.Metadata == nil {
		return nil, fmt.Errorf("snapshot file %q is missing metadata section", path)
	}

	return classifySchemaVersion(probe.Metadata.SchemaVersion), nil
}

// classifySchemaVersion returns a SchemaUpgradeResult for the given stored
// version relative to the current PersistSchemaVersion.
func classifySchemaVersion(stored int) *SchemaUpgradeResult {
	r := &SchemaUpgradeResult{
		StoredVersion:  stored,
		CurrentVersion: PersistSchemaVersion,
	}

	switch {
	case stored == PersistSchemaVersion:
		r.Message = fmt.Sprintf("schema version %d is current — no upgrade needed", stored)

	case stored < MinSupportedSchemaVersion:
		r.Unsupported = true
		r.Message = fmt.Sprintf(
			"snapshot schema version %d is too old to load (minimum supported: %d, current: %d); "+
				"re-run the debug command to regenerate the snapshot",
			stored, MinSupportedSchemaVersion, PersistSchemaVersion,
		)

	case stored < PersistSchemaVersion:
		r.NeedsUpgrade = true
		r.Message = fmt.Sprintf(
			"snapshot schema version %d is outdated (current: %d); "+
				"re-run the debug command to regenerate the snapshot with the current format",
			stored, PersistSchemaVersion,
		)

	case stored > PersistSchemaVersion:
		r.FromFuture = true
		r.Unsupported = true
		r.Message = fmt.Sprintf(
			"snapshot schema version %d was produced by a newer version of Glassbox (this binary supports up to %d); "+
				"upgrade Glassbox to read this snapshot, or re-run the debug command with the current binary",
			stored, PersistSchemaVersion,
		)
	}

	return r
}

// SchemaError is returned by LoadPersisted and related functions when the
// snapshot's schema version is incompatible. It carries structured information
// so callers can generate targeted remediation messages.
type SchemaError struct {
	Result *SchemaUpgradeResult
	Path   string
}

func (e *SchemaError) Error() string {
	if e.Path == "" {
		return e.Result.Message
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "snapshot file %q: %s", e.Path, e.Result.Message)
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

// ValidateSchemaVersion returns a *SchemaError when the stored version is
// unsupported (too old to migrate, or from the future). It returns nil when
// the version is current or can be auto-migrated — callers that want to
// distinguish those two cases should call classifySchemaVersion directly.
//
// Unlike CheckSchemaVersion (which reads a file), this operates on an
// already-parsed version integer and is suitable for use inside LoadPersisted.
func ValidateSchemaVersion(stored int, path string) error {
	r := classifySchemaVersion(stored)
	if r.Unsupported {
		return &SchemaError{Result: r, Path: path}
	}
	return nil
}

// SchemaVersionSummary returns a one-line human-readable description of the
// schema version situation, suitable for verbose output or diagnostic logs.
func SchemaVersionSummary(stored int) string {
	return classifySchemaVersion(stored).Message
}
