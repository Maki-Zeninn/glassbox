// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

//go:build linux || darwin

package secutil

import "syscall"

func platformLock(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	return syscall.Mlock(b)
}

func platformUnlock(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	return syscall.Munlock(b)
}
