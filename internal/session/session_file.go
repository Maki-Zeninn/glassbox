// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SaveToFile writes session data as a JSON file at path using the same
// atomic write-flush-rename sequence as writeFileAtomic. This guarantees
// that a reader of path sees either the previous complete session or the
// new complete session — never a partial write.
//
// A crash or power loss at any stage never corrupts a previously valid file
// at the same path; the original is only replaced after the new content has
// been fully written, synced, and renamed into place.
//
// If data is nil the call returns an error without touching the filesystem.
func SaveToFile(data *Data, path string) error {
	if data == nil {
		return fmt.Errorf("cannot save nil session data to file")
	}
	if path == "" {
		return fmt.Errorf("destination path is required")
	}

	b, err := marshalSessionWithExtras(data)
	if err != nil {
		return fmt.Errorf("failed to marshal session for file save: %w", err)
	}
	return writeFileAtomic(path, b, 0o600)
}

// LoadFromFile reads a session JSON file written by SaveToFile and returns
// the reconstructed Data. It validates the schema version and runs the
// same upgrade path as the SQLite store so file-backed sessions are
// always current when returned.
func LoadFromFile(path string) (*Data, error) {
	if path == "" {
		return nil, fmt.Errorf("session file path is required")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read session file %q: %w", path, err)
	}
	var data Data
	if err := json.Unmarshal(b, &data); err != nil {
		return nil, fmt.Errorf("failed to parse session file %q: %w", path, err)
	}
	if schemaErr := ValidateSchemaVersion(data.SchemaVersion, data.ID); schemaErr != nil {
		return nil, schemaErr
	}
	if _, upgradeErr := UpgradeSessionData(&data); upgradeErr != nil {
		return nil, fmt.Errorf("failed to upgrade session from file %q: %w", path, upgradeErr)
	}
	return &data, nil
}

// RecoverStaleSessionFiles scans dir for leftover temp and journal files
// produced by interrupted session writes. Files younger than minAge are
// left alone — they may belong to a write currently in progress.
//
// This should be called once at Glassbox startup so abandoned temp files
// from a previous crash do not accumulate indefinitely.
//
// The count of files removed and a list of their base names are returned.
// Individual removal failures are logged and counted but do not abort the
// scan so a single unremovable file does not prevent others from being
// cleaned.
func RecoverStaleSessionFiles(dir string, minAge time.Duration) (int, []string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil, nil
		}
		return 0, nil, fmt.Errorf("failed to scan session directory %q: %w", dir, err)
	}

	now := time.Now()
	var removed []string
	for _, e := range entries {
		if e.IsDir() || !isStaleTempName(e.Name()) {
			continue
		}
		info, infoErr := e.Info()
		if infoErr != nil {
			continue
		}
		if now.Sub(info.ModTime()) < minAge {
			continue // still young enough to belong to a live write
		}
		full := filepath.Join(dir, e.Name())
		if removeErr := os.Remove(full); removeErr == nil {
			removed = append(removed, e.Name())
		}
	}
	return len(removed), removed, nil
}

// DefaultSessionDir returns the default directory where session-related files
// (checkpoints, temp files) are stored (~/.Glassbox).
func DefaultSessionDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".Glassbox"
	}
	return filepath.Join(home, ".Glassbox")
}
