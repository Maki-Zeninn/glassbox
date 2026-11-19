// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// StaleTempFileAge is the minimum age a temp or recovery-journal file must
// reach before CleanStaleTempFiles considers it abandoned rather than
// belonging to a write currently in progress.
const StaleTempFileAge = time.Hour

// staleTempPatterns lists the temp/journal file name suffixes this package
// leaves behind mid-write. CleanStaleTempFiles only ever removes files
// matching one of these — never arbitrary files in a Glassbox directory.
var staleTempPatterns = []string{".tmp-", ".journal"}

// faultInject is a test-only hook invoked at each stage of writeFileAtomic.
// It is nil in production. Tests set it to simulate a crash at a specific
// stage and assert that the target file is never left partially written.
var faultInject func(stage string) error

const (
	faultAfterCreate  = "after_create"
	faultAfterWrite   = "after_write"
	faultAfterSync    = "after_sync"
	faultAfterClose   = "after_close"
	faultBeforeRename = "before_rename"
)

func injectFault(stage string) error {
	if faultInject == nil {
		return nil
	}
	return faultInject(stage)
}

// writeFileAtomic writes data to path so that a crash or power loss at any
// point during the write can never leave a truncated or partially written
// file at path: the data is written to a temporary file in the same
// directory, fsync'd, then renamed into place. The rename is atomic on every
// platform Glassbox supports, so readers of path always see either the
// previous complete contents or the new complete contents, never a mix.
//
// perm is applied to the temporary file before it is renamed into place, so
// the final file never has a permission window wider than requested.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create directory %q: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if err := injectFault(faultAfterCreate); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	if err := injectFault(faultAfterWrite); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}

	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("failed to sync temp file: %w", err)
	}
	if err := injectFault(faultAfterSync); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}

	// Best effort on platforms without POSIX permissions.
	_ = tmp.Chmod(perm)

	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("failed to close temp file: %w", err)
	}
	if err := injectFault(faultAfterClose); err != nil {
		cleanup()
		return err
	}

	if err := injectFault(faultBeforeRename); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("failed to rename temp file into place: %w", err)
	}

	// Best-effort directory fsync so the rename itself survives power loss
	// on platforms that support it (see atomicfile_unix.go /
	// atomicfile_windows.go). A failure here does not undo the rename: the
	// new file is already valid and visible, this only affects how quickly
	// the directory entry update is durable.
	_ = syncDir(dir)

	return nil
}

// isStaleTempName reports whether name looks like a leftover temp or
// recovery-journal file written by this package.
func isStaleTempName(name string) bool {
	for _, pattern := range staleTempPatterns {
		if strings.Contains(name, pattern) {
			return true
		}
	}
	return false
}

// CleanStaleTempFiles removes leftover temp/journal files in dir (created by
// writeFileAtomic or the export recovery journal, see archive.go) that are
// older than maxAge. It is not recursive and never touches files that don't
// match a known temp/journal pattern. Individual removal failures are
// ignored (best effort); the count of files actually removed is returned.
func CleanStaleTempFiles(dir string, maxAge time.Duration) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("failed to read directory %q: %w", dir, err)
	}

	now := time.Now()
	removed := 0
	for _, e := range entries {
		if e.IsDir() || !isStaleTempName(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) < maxAge {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err == nil {
			removed++
		}
	}
	return removed, nil
}
