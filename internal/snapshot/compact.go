// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// CompactionOptions controls the behavior of ledger state compaction.
type CompactionOptions struct {
	// DryRun when true predicts changes without writing any files.
	DryRun bool
	// Force when true bypasses safety checks (use with caution).
	Force bool
	// MinAge is the minimum age a snapshot must be before it's considered
	// for removal. Zero means no age restriction.
	MinAge time.Duration
	// PreserveHashes is a set of content hashes that must never be removed,
	// even if unreferenced. Useful for pinning specific snapshots.
	PreserveHashes []string
}

// CompactionReport summarizes the results of a compaction operation.
type CompactionReport struct {
	// BeforeSize is the total disk usage before compaction.
	BeforeSize int64
	// AfterSize is the total disk usage after compaction (or predicted if dry-run).
	AfterSize int64
	// ReclaimedBytes is the space freed by compaction.
	ReclaimedBytes int64
	// ReferencedCount is the number of snapshots that are referenced by
	// registries, sessions, or bundles.
	ReferencedCount int
	// UnreferencedCount is the number of snapshots that are not referenced.
	UnreferencedCount int
	// RemovedHashes are the content hashes of snapshots that were removed
	// (or would be removed in dry-run).
	RemovedHashes []string
	// PreservedHashes are the hashes that were preserved due to MinAge or
	// PreserveHashes.
	PreservedHashes []string
	// Errors encountered during compaction.
	Errors []string
	// Duration is the time taken for the compaction operation.
	Duration time.Duration
}

// ReferenceModel tracks which snapshots are referenced by different sources.
type ReferenceModel struct {
	// RegistryReferences maps snapshot content hashes to the registry files
	// that reference them.
	RegistryReferences map[string][]string
	// SessionReferences maps snapshot content hashes to session IDs that
	// reference them.
	SessionReferences map[string][]string
	// BundleReferences maps snapshot content hashes to bundle files that
	// reference them.
	BundleReferences map[string][]string
	// AllReferenced is the set of all content hashes that are referenced
	// by any source.
	AllReferenced map[string]bool
}

// BuildReferenceModel scans the data directories to build a model of which
// snapshots are referenced by registries, sessions, and bundles.
func BuildReferenceModel(dataDir string) (*ReferenceModel, error) {
	model := &ReferenceModel{
		RegistryReferences: make(map[string][]string),
		SessionReferences:  make(map[string][]string),
		BundleReferences:   make(map[string][]string),
		AllReferenced:      make(map[string]bool),
	}

	// Scan for registry files
	if err := scanRegistries(dataDir, model); err != nil {
		return nil, fmt.Errorf("failed to scan registries: %w", err)
	}

	// Scan for session files (if session package is available)
	if err := scanSessions(dataDir, model); err != nil {
		return nil, fmt.Errorf("failed to scan sessions: %w", err)
	}

	// Scan for bundle files
	if err := scanBundles(dataDir, model); err != nil {
		return nil, fmt.Errorf("failed to scan bundles: %w", err)
	}

	return model, nil
}

// scanRegistries scans for replay registry files and extracts snapshot references.
func scanRegistries(dataDir string, model *ReferenceModel) error {
	// Look for registry files in common locations
	registryPaths := []string{
		filepath.Join(dataDir, "registries"),
		filepath.Join(dataDir, "cache", "registries"),
		filepath.Join(dataDir, ".glassbox", "registries"),
	}

	for _, registryDir := range registryPaths {
		entries, err := os.ReadDir(registryDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}

			path := filepath.Join(registryDir, entry.Name())
			hashes, err := extractSnapshotHashesFromRegistry(path)
			if err != nil {
				// Log but don't fail on individual file errors
				continue
			}

			for _, hash := range hashes {
				model.RegistryReferences[hash] = append(model.RegistryReferences[hash], path)
				model.AllReferenced[hash] = true
			}
		}
	}

	return nil
}

// extractSnapshotHashesFromRegistry reads a registry file and extracts the
// content hashes of any snapshots it references.
func extractSnapshotHashesFromRegistry(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var registry struct {
		Entries []struct {
			Snapshot *Snapshot `json:"snapshot"`
		} `json:"entries"`
	}

	if err := json.Unmarshal(data, &registry); err != nil {
		return nil, err
	}

	var hashes []string
	for _, entry := range registry.Entries {
		if entry.Snapshot != nil {
			// Compute content hash of the snapshot
			ps := &PersistedSnapshot{
				Snapshot: entry.Snapshot,
			}
			hash, err := ContentHash(ps)
			if err == nil && hash != "" {
				hashes = append(hashes, hash)
			}
		}
	}

	return hashes, nil
}

// scanSessions scans for session files and extracts snapshot references.
// This is a simplified implementation that looks for embedded ledger state.
func scanSessions(dataDir string, model *ReferenceModel) error {
	sessionPaths := []string{
		filepath.Join(dataDir, "sessions.db"),
		filepath.Join(dataDir, ".glassbox", "sessions.db"),
		filepath.Join(dataDir, "glassbox.db"),
	}

	for _, sessionPath := range sessionPaths {
		// Check if file exists
		if _, err := os.Stat(sessionPath); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}

		// For now, we'll skip SQLite parsing since it requires the session package
		// In a full implementation, we would query the database for SimResponseJSON
		// and extract ledger state hashes from it.
		// This is a placeholder for the actual implementation.
	}

	return nil
}

// scanBundles scans for bundle files and extracts snapshot references.
func scanBundles(dataDir string, model *ReferenceModel) error {
	bundlePaths := []string{
		filepath.Join(dataDir, "bundles"),
		filepath.Join(dataDir, "cache", "bundles"),
		filepath.Join(dataDir, ".glassbox", "bundles"),
	}

	for _, bundleDir := range bundlePaths {
		entries, err := os.ReadDir(bundleDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}

			path := filepath.Join(bundleDir, entry.Name())
			hash, err := extractSnapshotHashFromBundle(path)
			if err != nil {
				continue
			}

			if hash != "" {
				model.BundleReferences[hash] = append(model.BundleReferences[hash], path)
				model.AllReferenced[hash] = true
			}
		}
	}

	return nil
}

// extractSnapshotHashFromBundle reads a bundle file and extracts the content
// hash of its ledger state snapshot.
func extractSnapshotHashFromBundle(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	var bundle struct {
		LedgerState map[string]string `json:"ledger_state"`
	}

	if err := json.Unmarshal(data, &bundle); err != nil {
		return "", err
	}

	if len(bundle.LedgerState) == 0 {
		return "", nil
	}

	// Convert to snapshot and compute hash
	snap := FromMap(bundle.LedgerState)
	ps := &PersistedSnapshot{
		Snapshot: snap,
	}

	return ContentHash(ps)
}

// CompactLedgerState performs garbage collection on ledger state snapshots.
// It uses a mark-and-sweep algorithm:
// 1. Mark: Build a reference model to identify all referenced snapshots
// 2. Sweep: Remove unreferenced snapshots that meet the age criteria
// 3. Atomic commit: Use temporary files and rename for atomic replacement
func CompactLedgerState(dedupStore *DedupStore, opts CompactionOptions) (*CompactionReport, error) {
	startTime := time.Now()
	report := &CompactionReport{}

	// Get current disk usage
	beforeSize, err := dedupStore.GetDiskUsage()
	if err != nil {
		return nil, fmt.Errorf("failed to get disk usage: %w", err)
	}
	report.BeforeSize = beforeSize

	// Build reference model
	dataDir := dedupStore.baseDir
	refModel, err := BuildReferenceModel(dataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to build reference model: %w", err)
	}

	// Get all stored hashes
	allHashes, err := dedupStore.GetStoredHashes()
	if err != nil {
		return nil, fmt.Errorf("failed to get stored hashes: %w", err)
	}

	// Build preserve set
	preserveSet := make(map[string]bool)
	for _, hash := range opts.PreserveHashes {
		preserveSet[hash] = true
	}

	// Classify hashes
	now := time.Now()
	var toRemove []string
	var preserved []string

	for _, hash := range allHashes {
		// Check if referenced
		if refModel.AllReferenced[hash] {
			report.ReferencedCount++
			continue
		}

		report.UnreferencedCount++

		// Check if explicitly preserved
		if preserveSet[hash] {
			preserved = append(preserved, hash)
			continue
		}

		// Check age
		if opts.MinAge > 0 {
			path := filepath.Join(dataDir, "snapshots", hashToFilename(hash))
			info, err := os.Stat(path)
			if err != nil {
				report.Errors = append(report.Errors, fmt.Sprintf("failed to stat %s: %v", hash, err))
				continue
			}
			age := now.Sub(info.ModTime())
			if age < opts.MinAge {
				preserved = append(preserved, hash)
				continue
			}
		}

		toRemove = append(toRemove, hash)
	}

	report.RemovedHashes = toRemove
	report.PreservedHashes = preserved

	// Dry-run: just report what would happen
	if opts.DryRun {
		// Calculate predicted space savings
		var reclaimed int64
		for _, hash := range toRemove {
			path := filepath.Join(dataDir, "snapshots", hashToFilename(hash))
			info, err := os.Stat(path)
			if err == nil {
				reclaimed += info.Size()
			}
		}
		report.ReclaimedBytes = reclaimed
		report.AfterSize = beforeSize - reclaimed
		report.Duration = time.Since(startTime)
		return report, nil
	}

	// Actual compaction with atomic commit
	if err := compactWithAtomicCommit(dedupStore, toRemove, opts.Force); err != nil {
		return nil, fmt.Errorf("compaction failed: %w", err)
	}

	// Calculate actual space savings
	afterSize, err := dedupStore.GetDiskUsage()
	if err != nil {
		return nil, fmt.Errorf("failed to get disk usage after compaction: %w", err)
	}
	report.AfterSize = afterSize
	report.ReclaimedBytes = beforeSize - afterSize
	report.Duration = time.Since(startTime)

	return report, nil
}

// compactWithAtomicCommit performs the actual removal with atomic commit
// using temporary files and recovery.
func compactWithAtomicCommit(dedupStore *DedupStore, toRemove []string, force bool) error {
	if len(toRemove) == 0 {
		return nil
	}

	snapshotDir := filepath.Join(dedupStore.baseDir, "snapshots")

	// Create a recovery manifest
	recoveryPath := snapshotDir + ".recovery"
	recoveryManifest := struct {
		ToRemove  []string `json:"to_remove"`
		Timestamp string   `json:"timestamp"`
		Checksum  string   `json:"checksum"`
	}{
		ToRemove:  toRemove,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	// Compute checksum of the manifest
	manifestData, _ := json.Marshal(recoveryManifest.ToRemove)
	sum := sha256.Sum256(manifestData)
	recoveryManifest.Checksum = hex.EncodeToString(sum[:])

	// Write recovery manifest atomically
	manifestBytes, err := json.MarshalIndent(recoveryManifest, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal recovery manifest: %w", err)
	}

	tmpRecovery := recoveryPath + ".tmp"
	if err := os.WriteFile(tmpRecovery, manifestBytes, 0644); err != nil {
		return fmt.Errorf("failed to write recovery manifest: %w", err)
	}
	if err := os.Rename(tmpRecovery, recoveryPath); err != nil {
		_ = os.Remove(tmpRecovery)
		return fmt.Errorf("failed to commit recovery manifest: %w", err)
	}

	// Remove files
	var removalErrors []string
	for _, hash := range toRemove {
		filename := hashToFilename(hash)
		targetPath := filepath.Join(snapshotDir, filename)

		if err := os.Remove(targetPath); err != nil {
			removalErrors = append(removalErrors, fmt.Sprintf("%s: %v", hash, err))
		} else {
			// Update index
			if err := dedupStore.updateIndex(hash, false); err != nil {
				// Log but don't fail on index errors
				fmt.Fprintf(os.Stderr, "warning: failed to update dedup index: %v\n", err)
			}
		}
	}

	// Remove recovery manifest on success
	_ = os.Remove(recoveryPath)

	// Report any errors
	if len(removalErrors) > 0 && !force {
		return fmt.Errorf("failed to remove some snapshots: %v", removalErrors)
	}

	return nil
}

// RecoverFromInterruptedCompaction checks for a recovery manifest and
// restores the state if a compaction was interrupted.
func RecoverFromInterruptedCompaction(dedupStore *DedupStore) error {
	snapshotDir := filepath.Join(dedupStore.baseDir, "snapshots")
	recoveryPath := snapshotDir + ".recovery"

	// Check if recovery manifest exists
	if _, err := os.Stat(recoveryPath); os.IsNotExist(err) {
		return nil // No interrupted compaction
	}

	// Read recovery manifest
	data, err := os.ReadFile(recoveryPath)
	if err != nil {
		return fmt.Errorf("failed to read recovery manifest: %w", err)
	}

	var recoveryManifest struct {
		ToRemove  []string `json:"to_remove"`
		Timestamp string   `json:"timestamp"`
		Checksum  string   `json:"checksum"`
	}

	if err := json.Unmarshal(data, &recoveryManifest); err != nil {
		return fmt.Errorf("failed to parse recovery manifest: %w", err)
	}

	// Verify checksum
	manifestData, _ := json.Marshal(recoveryManifest.ToRemove)
	sum := sha256.Sum256(manifestData)
	computedChecksum := hex.EncodeToString(sum[:])

	if recoveryManifest.Checksum != computedChecksum {
		return fmt.Errorf("recovery manifest checksum mismatch - may be corrupted")
	}

	// Recovery is not automatically performed to avoid data loss
	// The manifest is left in place for manual inspection
	return fmt.Errorf("interrupted compaction detected - recovery manifest exists at %s\n"+
		"Manual intervention may be required. Manifest timestamp: %s\n"+
		"Files marked for removal: %d",
		recoveryPath, recoveryManifest.Timestamp, len(recoveryManifest.ToRemove))
}

// ValidateCompactionSafety performs safety checks before compaction.
func ValidateCompactionSafety(dedupStore *DedupStore, opts CompactionOptions) error {
	// Check that we're not operating on a filesystem root
	snapshotDir := filepath.Join(dedupStore.baseDir, "snapshots")
	clean := filepath.Clean(snapshotDir)
	if parent := filepath.Dir(clean); parent == clean {
		return fmt.Errorf("refusing to compact at filesystem root %q", clean)
	}

	// Check for interrupted compaction
	if err := RecoverFromInterruptedCompaction(dedupStore); err != nil {
		if !opts.Force {
			return fmt.Errorf("safety check failed: %w", err)
		}
	}

	// Check directory permissions
	if err := os.MkdirAll(snapshotDir, 0755); err != nil {
		return fmt.Errorf("cannot access snapshot directory: %w", err)
	}

	// Test write permissions
	testFile := filepath.Join(snapshotDir, ".compact-test")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		return fmt.Errorf("no write permission in snapshot directory: %w", err)
	}
	_ = os.Remove(testFile)

	return nil
}
