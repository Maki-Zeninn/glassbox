// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

//go:build !windows

package session

import "os"

// syncDir fsyncs dir so that a rename into it is durable across power loss.
// Some filesystems (notably network/overlay filesystems) reject fsync on a
// directory handle; that is not a reason to fail the write the rename
// already completed, so the error is ignored by the caller.
func syncDir(dir string) error {
	f, err := os.Open(dir) //nolint:gosec
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}
