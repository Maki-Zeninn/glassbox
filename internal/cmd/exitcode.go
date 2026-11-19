// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	stderrors "errors"

	"github.com/dotandev/glassbox/internal/errors"
)

// Exit code taxonomy for the Glassbox CLI.
//
//	0  – success
//	1  – user error (bad input, missing argument, validation failure)
//	2  – configuration or environment error (bad config file, missing simulator)
//	3  – internal failure (RPC error, simulator crash, unexpected panic)
//	130 – interrupted (SIGINT / Ctrl-C)
const (
	ExitSuccess       = 0
	ExitUserError     = 1
	ExitConfigError   = 2
	ExitInternalError = 3
	// InterruptExitCode is defined in interrupt.go as 130.
)

// userErrorCodes are ErstErrorCodes that map to ExitUserError.
var userErrorCodes = map[errors.ErstErrorCode]bool{
	errors.ErstValidationFailed:    true,
	errors.ErstSimulationLogicError: true,
	errors.ErstArgumentRequired:    true,
	errors.ErstTransactionNotFound: true,
	errors.ErstLedgerNotFound:      true,
	errors.ErstLedgerArchived:      true,
	errors.ErstRateLimitExceeded:   true,
	errors.ErstUnauthorized:        true,
	errors.ErstInvalidNetwork:      true,
	errors.ErstNetworkNotFound:     true,
	// Session concurrency errors are user-actionable (retry or use --force).
	errors.ErstSessionConflict: true,
	errors.ErstSessionLockHeld: true,
}

// configErrorCodes are ErstErrorCodes that map to ExitConfigError.
var configErrorCodes = map[errors.ErstErrorCode]bool{
	errors.ErstConfigFailed:       true,
	errors.ErstSimulatorNotFound:  true,
}

// ExitCodeFor maps an error to the appropriate exit code using the taxonomy
// defined above. It inspects ErstError codes when available, falling back to
// sentinel matching and finally to ExitInternalError.
//
// Context cancellation (e.g., SIGINT-triggered cancellation or explicit cancel)
// maps to InterruptExitCode (130), matching the POSIX SIGINT convention.
func ExitCodeFor(err error) int {
	if err == nil {
		return ExitSuccess
	}
	if IsInterrupted(err) || IsCancellation(err) {
		return InterruptExitCode
	}

	var erstErr *errors.ErstError
	if stderrors.As(err, &erstErr) {
		if userErrorCodes[erstErr.Code] {
			return ExitUserError
		}
		if configErrorCodes[erstErr.Code] {
			return ExitConfigError
		}
		return ExitInternalError
	}

	// Fallback: check well-known sentinel errors.
	switch {
	case stderrors.Is(err, errors.ErrValidationFailed),
		stderrors.Is(err, errors.ErrArgumentRequired),
		stderrors.Is(err, errors.ErrTransactionNotFound),
		stderrors.Is(err, errors.ErrInvalidNetwork),
		stderrors.Is(err, errors.ErrSimulationLogicError):
		return ExitUserError

	case stderrors.Is(err, errors.ErrConfigFailed),
		stderrors.Is(err, errors.ErrSimulatorNotFound):
		return ExitConfigError
	}

	return ExitInternalError
}
