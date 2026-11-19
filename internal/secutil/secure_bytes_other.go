// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

//go:build !linux && !darwin && !windows

package secutil

import "fmt"

func platformLock(_ []byte) error {
	return fmt.Errorf("%w: no mlock/VirtualLock on this platform", ErrMlockUnavailable)
}

func platformUnlock(_ []byte) error {
	return nil
}
