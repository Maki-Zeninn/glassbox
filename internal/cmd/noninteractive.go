// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"

	"github.com/dotandev/glassbox/internal/errors"
	"github.com/dotandev/glassbox/internal/termctx"
	"github.com/spf13/cobra"
)

// requireInteractiveTTY returns an error when the command is running in
// non-interactive mode (CI, piped stdin, --non-interactive flag). Commands
// that need user prompts MUST call this before reading from stdin.
func requireInteractiveTTY(cmd *cobra.Command, action string) error {
	if termctx.GlobalNonInteractive() {
		return errors.WrapValidationError(fmt.Sprintf(
			"non-interactive mode: %s requires an interactive terminal; use --yes or --force to skip prompting",
			action,
		))
	}
	return nil
}

// confirmWithForceOrNonInteractive handles the common pattern of prompting for
// confirmation with a force/skip flag. When forceFlag is true, returns defaultVal
// immediately. When non-interactive, returns an actionable error. Otherwise,
// prints the prompt and reads the user's answer.
func confirmWithForceOrNonInteractive(
	cmd *cobra.Command,
	prompt string,
	forceFlag bool,
	defaultVal bool,
) (bool, error) {
	if forceFlag {
		return defaultVal, nil
	}
	if termctx.GlobalNonInteractive() {
		return defaultVal, errors.WrapValidationError(fmt.Sprintf(
			"non-interactive mode: confirmation prompt requires an interactive terminal; "+
				"use --yes or --force to skip",
		))
	}
	return promptYesNo(cmd, prompt), nil
}

// requireInteractiveShell returns an error when the shell command is running
// in non-interactive mode. The shell command is inherently interactive and
// cannot function without a terminal.
func requireInteractiveShell(cmd *cobra.Command) error {
	if termctx.GlobalNonInteractive() {
		return errors.WrapValidationError(
			"non-interactive mode: the shell command requires an interactive terminal; " +
				"use 'glassbox debug' or individual commands instead",
		)
	}
	return nil
}
