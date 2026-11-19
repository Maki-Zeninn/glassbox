// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"fmt"
	"testing"

	"github.com/dotandev/glassbox/internal/errors"
)

func TestExitCodeFor(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil error is success", nil, ExitSuccess},
		{"interrupt is 130", ErrInterrupted, InterruptExitCode},
		{"validation error is user error", errors.WrapValidationError("bad input"), ExitUserError},
		{"simulation logic error is user error", errors.WrapSimulationLogicError("bad logic"), ExitUserError},
		{"config error is config error", errors.WrapConfigError("bad config", fmt.Errorf("missing")), ExitConfigError},
		{"unknown error is internal", fmt.Errorf("unknown failure"), ExitInternalError},
		{"sentinel ErrValidationFailed is user error", errors.ErrValidationFailed, ExitUserError},
		{"sentinel ErrConfigFailed is config error", errors.ErrConfigFailed, ExitConfigError},
		{"sentinel ErrSimulatorNotFound is config error", errors.ErrSimulatorNotFound, ExitConfigError},
		{"sentinel ErrTransactionNotFound is user error", errors.ErrTransactionNotFound, ExitUserError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExitCodeFor(tt.err)
			if got != tt.want {
				t.Errorf("ExitCodeFor(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}

// TestExitCodeFor_DeadlineExceeded documents the current exit-code mapping for
// context.DeadlineExceeded.
//
// Design intent: IsCancellation only matches context.Canceled, not
// context.DeadlineExceeded, so a deadline-exceeded error (e.g. from an RPC
// timeout) falls through to ExitInternalError (3) rather than InterruptExitCode
// (130).  This is the correct behaviour — ExitInternalError signals an
// infrastructure/timeout failure, whereas InterruptExitCode (130) implies the
// user explicitly interrupted the process (Ctrl-C / SIGINT). Separating the two
// codes lets callers distinguish "user cancelled" from "timeout".
//
// Known limitation: if callers need a dedicated timeout exit code they should
// add ExitTimeoutError and update ExitCodeFor to detect context.DeadlineExceeded
// before the IsCancellation check. That change should be made in a follow-up.
func TestExitCodeFor_DeadlineExceeded(t *testing.T) {
	got := ExitCodeFor(context.DeadlineExceeded)

	// ExitInternalError (3) is the current return value because IsCancellation
	// only matches context.Canceled; context.DeadlineExceeded is not treated as
	// an interrupt and no ErstError code is attached to it, so it falls through
	// to the internal-error default.
	//
	// Known limitation: a future improvement could map context.DeadlineExceeded
	// to a dedicated exit code (e.g. ExitTimeoutError) to distinguish RPC
	// timeouts from both user cancellation (130) and generic internal errors (3).
	const want = ExitInternalError
	if got != want {
		t.Errorf("ExitCodeFor(context.DeadlineExceeded) = %d, want %d (ExitInternalError)", got, want)
	}
}
