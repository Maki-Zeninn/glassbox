// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package snapshot

import (
	"fmt"
	"sort"
	"strings"
)

// ChangeKind describes how a ledger entry changed between two snapshots.
type ChangeKind string

const (
	ChangeAdded    ChangeKind = "added"
	ChangeRemoved  ChangeKind = "removed"
	ChangeModified ChangeKind = "modified"
)

// EntryChange describes a single ledger entry that differs between two snapshots.
type EntryChange struct {
	Key      string
	OldValue string
	NewValue string
	Kind     ChangeKind
}

// SnapshotDiff holds the result of comparing two snapshots.
type SnapshotDiff struct {
	Added             []EntryChange
	Removed           []EntryChange
	Modified          []EntryChange
	BaseFingerprint   string
	TargetFingerprint string
	// Version information — populated by DiffPersistedSnapshots when both
	// PersistedSnapshots are available.
	BaseSchemaVersion   int
	TargetSchemaVersion int
	// MigrationNotes records any automatic migrations that ran when the
	// snapshots were loaded, so diff output can surface version history.
	MigrationNotes []string
}

// TotalChanges returns the total count of added, removed, and modified entries.
func (d *SnapshotDiff) TotalChanges() int {
	return len(d.Added) + len(d.Removed) + len(d.Modified)
}

// DiffSnapshots compares two snapshots and returns all ledger entry differences.
// Entries are sorted by key in the returned diff for deterministic output.
func DiffSnapshots(base, target *Snapshot) *SnapshotDiff {
	baseMap := base.ToMap()
	targetMap := target.ToMap()

	diff := &SnapshotDiff{
		BaseFingerprint:   base.Fingerprint,
		TargetFingerprint: target.Fingerprint,
	}

	for k, oldVal := range baseMap {
		if newVal, ok := targetMap[k]; ok {
			if oldVal != newVal {
				diff.Modified = append(diff.Modified, EntryChange{
					Key: k, OldValue: oldVal, NewValue: newVal, Kind: ChangeModified,
				})
			}
		} else {
			diff.Removed = append(diff.Removed, EntryChange{
				Key: k, OldValue: oldVal, Kind: ChangeRemoved,
			})
		}
	}

	for k, newVal := range targetMap {
		if _, ok := baseMap[k]; !ok {
			diff.Added = append(diff.Added, EntryChange{
				Key: k, NewValue: newVal, Kind: ChangeAdded,
			})
		}
	}

	sort.Slice(diff.Added, func(i, j int) bool { return diff.Added[i].Key < diff.Added[j].Key })
	sort.Slice(diff.Removed, func(i, j int) bool { return diff.Removed[i].Key < diff.Removed[j].Key })
	sort.Slice(diff.Modified, func(i, j int) bool { return diff.Modified[i].Key < diff.Modified[j].Key })

	return diff
}

// FormatDiff returns a human-readable textual diff of two snapshots suitable
// for terminal output. Added entries are prefixed with "+", removed with "-",
// and modified with "~".
func FormatDiff(diff *SnapshotDiff) string {
	var sb strings.Builder

	// Version header — shown when schema version info is available.
	if diff.BaseSchemaVersion > 0 || diff.TargetSchemaVersion > 0 {
		fmt.Fprintf(&sb, "Schema versions:    base=v%d  target=v%d\n",
			diff.BaseSchemaVersion, diff.TargetSchemaVersion)
	}
	for _, note := range diff.MigrationNotes {
		fmt.Fprintf(&sb, "  (migration) %s\n", note)
	}

	if diff.TotalChanges() == 0 {
		sb.WriteString("Snapshots are identical.\n")
		return sb.String()
	}

	fmt.Fprintf(&sb, "Snapshot diff: %d added, %d removed, %d modified\n",
		len(diff.Added), len(diff.Removed), len(diff.Modified))

	if diff.BaseFingerprint != "" || diff.TargetFingerprint != "" {
		fmt.Fprintf(&sb, "Base fingerprint:   %s\n", diff.BaseFingerprint)
		fmt.Fprintf(&sb, "Target fingerprint: %s\n", diff.TargetFingerprint)
	}

	sb.WriteString("\n")

	for _, ch := range diff.Added {
		fmt.Fprintf(&sb, "+ [%s]\n    value: %s\n", truncateKey(ch.Key), truncateValue(ch.NewValue))
	}
	for _, ch := range diff.Removed {
		fmt.Fprintf(&sb, "- [%s]\n    value: %s\n", truncateKey(ch.Key), truncateValue(ch.OldValue))
	}
	for _, ch := range diff.Modified {
		fmt.Fprintf(&sb, "~ [%s]\n    old: %s\n    new: %s\n",
			truncateKey(ch.Key), truncateValue(ch.OldValue), truncateValue(ch.NewValue))
	}

	return sb.String()
}

// DiffPersistedSnapshots compares two PersistedSnapshots, auto-migrating each
// to the current schema version if needed, and returns a SnapshotDiff that
// includes version metadata and migration notes.
//
// Neither snapshot is written to disk during this call — migration is applied
// in memory only.
func DiffPersistedSnapshots(base, target *PersistedSnapshot) (*SnapshotDiff, error) {
	if base == nil {
		return nil, fmt.Errorf("base snapshot is nil")
	}
	if target == nil {
		return nil, fmt.Errorf("target snapshot is nil")
	}

	var migrationNotes []string

	// Migrate base if needed.
	baseVersion := 0
	if base.Metadata != nil {
		baseVersion = base.Metadata.SchemaVersion
	}
	baseSteps, err := MigrateSnapshot(base)
	if err != nil {
		return nil, fmt.Errorf("base snapshot migration failed: %w", err)
	}
	for _, s := range baseSteps {
		migrationNotes = append(migrationNotes,
			fmt.Sprintf("base: %s (v%d → v%d)", s.Description, s.FromVersion, s.ToVersion))
	}

	// Migrate target if needed.
	targetVersion := 0
	if target.Metadata != nil {
		targetVersion = target.Metadata.SchemaVersion
	}
	targetSteps, err := MigrateSnapshot(target)
	if err != nil {
		return nil, fmt.Errorf("target snapshot migration failed: %w", err)
	}
	for _, s := range targetSteps {
		migrationNotes = append(migrationNotes,
			fmt.Sprintf("target: %s (v%d → v%d)", s.Description, s.FromVersion, s.ToVersion))
	}

	diff := DiffSnapshots(base.Snapshot, target.Snapshot)
	diff.BaseSchemaVersion = baseVersion
	diff.TargetSchemaVersion = targetVersion
	diff.MigrationNotes = migrationNotes
	return diff, nil
}

func truncateKey(s string) string {
	if len(s) > 64 {
		return s[:61] + "..."
	}
	return s
}

func truncateValue(s string) string {
	if len(s) > 80 {
		return s[:77] + "..."
	}
	return s
}
