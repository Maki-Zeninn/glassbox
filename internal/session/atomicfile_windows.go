// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

//go:build windows

package session

// syncDir is a no-op on Windows: directory handles cannot be fsync'd, and
// NTFS's own metadata journaling already makes a completed rename durable
// enough for our purposes.
func syncDir(dir string) error {
	return nil
}
