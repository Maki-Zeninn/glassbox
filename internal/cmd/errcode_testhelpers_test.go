// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// errcode_testhelpers_test.go provides a shared assertion for the stable
// error-code contract [Issue #762]: every error a command returns must be
// (or wrap) an *errors.ErstError, so that automation can rely on a stable
// Code and a consistent exit-code bucket instead of receiving an
// unclassified error that surfaces as ErstUnknown.

package cmd

import (
	stderrors "errors"
	"testing"

	glassboxerrors "github.com/dotandev/glassbox/internal/errors"
)

// requireErstError fails the test unless err is non-nil and is (or wraps) an
// *errors.ErstError. It returns the unwrapped *ErstError for further
// assertions (e.g. on Code or Hint) when the caller needs them.
//
// Use this at the boundary of any command's RunE (or the helper it delegates
// to) to guard against bare, unclassified errors leaking to the CLI's error
// output, which would otherwise report as ErstUnknown in both JSON and text
// modes.
func requireErstError(t *testing.T, err error) *glassboxerrors.ErstError {
	t.Helper()
	if err == nil {
		t.Fatal("requireErstError: expected a non-nil error")
	}
	var e *glassboxerrors.ErstError
	if !stderrors.As(err, &e) {
		t.Fatalf("requireErstError: error is not an *ErstError (unclassified command error): %T — %v", err, err)
	}
	if e.Code == "" {
		t.Fatalf("requireErstError: *ErstError has an empty Code: %v", err)
	}
	return e
}

// requireErstErrorCode is requireErstError plus an assertion that the code
// matches want.
func requireErstErrorCode(t *testing.T, err error, want glassboxerrors.ErstErrorCode) *glassboxerrors.ErstError {
	t.Helper()
	e := requireErstError(t, err)
	if e.Code != want {
		t.Fatalf("requireErstErrorCode: Code = %q, want %q", e.Code, want)
	}
	return e
}

// TestWrapInternalIsErstError guards the note-add ID-generation failure path
// (previously a bare fmt.Errorf) against regressing to an unclassified error.
func TestWrapInternalIsErstError(t *testing.T) {
	wrapped := glassboxerrors.WrapInternal("failed to generate note ID", stderrors.New("boom"))
	requireErstErrorCode(t, wrapped, glassboxerrors.ErstInternalError)
}

