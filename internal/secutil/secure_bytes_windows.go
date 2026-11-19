// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

//go:build windows

package secutil

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

func platformLock(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	err := windows.VirtualLock(uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)))
	if err != nil {
		return fmt.Errorf("%w: VirtualLock: %v", ErrMlockUnavailable, err)
	}
	return nil
}

func platformUnlock(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	return windows.VirtualUnlock(uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)))
}
