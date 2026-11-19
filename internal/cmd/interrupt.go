// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	stderrors "errors"
)

const InterruptExitCode = 130

var ErrInterrupted = stderrors.New("interrupt received")

// IsInterrupted reports whether err originated from an OS signal (SIGINT/SIGTERM)
// caught by the root command's signal handler.
func IsInterrupted(err error) bool {
	return stderrors.Is(err, ErrInterrupted)
}

// IsCancellation reports whether err represents a context cancellation, either
// from an OS signal or from explicit context cancellation (timeout, parent cancel).
// Context cancellation maps to the same exit code (130) as an interrupt.
func IsCancellation(err error) bool {
	return stderrors.Is(err, context.Canceled)
}

// WrapCancellation wraps a context cancellation error with a user-friendly
// message indicating the operation was interrupted. If the error is not a
// cancellation, it is returned unchanged.
func WrapCancellation(err error) error {
	if err == nil {
		return nil
	}
	if IsCancellation(err) || IsInterrupted(err) {
		return ErrInterrupted
	}
	return err
}
