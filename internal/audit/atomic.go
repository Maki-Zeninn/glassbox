// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"fmt"
	"os"
	"path/filepath"
)

// faultInject is a test-only hook invoked at each stage of writeFileAtomic and
// rotate. It is nil in production.
var faultInject func(stage string) error

const (
	faultAfterCreate  = "after_create"
	faultAfterWrite   = "after_write"
	faultAfterSync    = "after_sync"
	faultAfterClose   = "after_close"
	faultBeforeRename = "before_rename"

	faultRotateAfterCloseActive    = "rotate_after_close_active"
	faultRotateAfterHash           = "rotate_after_hash"
	faultRotateBeforeManifest      = "rotate_before_manifest"
	faultRotateAfterManifest       = "rotate_after_manifest"
	faultRotateBeforeSegmentRename = "rotate_before_segment_rename"
	faultRotateAfterSegmentRename  = "rotate_after_segment_rename"
	faultRotateBeforeOpenActive    = "rotate_before_open_active"
)

func injectFault(stage string) error {
	if faultInject == nil {
		return nil
	}
	return faultInject(stage)
}

// writeFileAtomic writes data to path so a crash never leaves a truncated
// target: data is written to a temp file, synced, then renamed into place.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("audit: create directory %q: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("audit: create temp file: %w", err)
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
		return fmt.Errorf("audit: write temp file: %w", err)
	}
	if err := injectFault(faultAfterWrite); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}

	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("audit: sync temp file: %w", err)
	}
	if err := injectFault(faultAfterSync); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}

	_ = tmp.Chmod(perm)

	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("audit: close temp file: %w", err)
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
		return fmt.Errorf("audit: rename temp file into place: %w", err)
	}
	return nil
}
