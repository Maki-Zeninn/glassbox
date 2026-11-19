// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/dotandev/glassbox/internal/errors"
	"github.com/dotandev/glassbox/internal/signer"
)

// validNetworkValues lists all accepted --network flag values across commands.
var validNetworkValues = map[string]bool{
	"testnet":    true,
	"mainnet":    true,
	"futurenet":  true,
	"standalone": true,
	"public":     true,
}

// validateNetwork returns a descriptive error when the network value is not
// one of the accepted values, including the invalid value and a suggestion.
func validateNetwork(network string) error {
	if network == "" {
		return nil // optional in some commands
	}
	if validNetworkValues[strings.ToLower(network)] {
		return nil
	}
	valid := []string{"testnet", "mainnet", "futurenet", "standalone", "public"}
	return errors.WrapValidationError(fmt.Sprintf(
		"invalid network %q — must be one of: %s",
		network, strings.Join(valid, ", "),
	))
}

// validateFilePath returns an error when path is non-empty but the file does
// not exist or is not readable.
func validateFilePath(flag, path string) error {
	if path == "" {
		return nil
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return errors.WrapValidationError(fmt.Sprintf(
				"--%s: file not found: %q", flag, path,
			))
		}
		return errors.WrapValidationError(fmt.Sprintf(
			"--%s: cannot access %q: %v", flag, path, err,
		))
	}
	return nil
}

// validatePositiveInt returns an error when val is set (non-zero) but not positive.
func validatePositiveInt(flag string, val int) error {
	if val < 0 {
		return errors.WrapValidationError(fmt.Sprintf(
			"--%s must be a positive integer, got %d", flag, val,
		))
	}
	return nil
}

// validateMutuallyExclusive returns an error when more than one of the named
// flags is set, listing all conflicting flags in the message with guidance.
func validateMutuallyExclusive(set map[string]bool, flags ...string) error {
	var active []string
	for _, f := range flags {
		if set[f] {
			active = append(active, "--"+f)
		}
	}
	if len(active) > 1 {
		all := make([]string, len(flags))
		for i, f := range flags {
			all[i] = "--" + f
		}
		return errors.WrapValidationError(fmt.Sprintf(
			"flags %s are mutually exclusive — provide only one\n"+
				"  Valid alternatives: %s",
			strings.Join(active, " and "),
			strings.Join(all, ", "),
		))
	}
	return nil
}

// validateExactlyOne returns an error unless exactly one of the named flags
// is set. It reports both the conflict and any missing flags.
func validateExactlyOne(set map[string]bool, flags ...string) error {
	var active []string
	for _, f := range flags {
		if set[f] {
			active = append(active, "--"+f)
		}
	}
	all := make([]string, len(flags))
	for i, f := range flags {
		all[i] = "--" + f
	}
	if len(active) > 1 {
		return errors.WrapValidationError(fmt.Sprintf(
			"flags %s are mutually exclusive — provide exactly one\n"+
				"  Valid alternatives: %s",
			strings.Join(active, " and "),
			strings.Join(all, ", "),
		))
	}
	if len(active) == 0 {
		return errors.WrapValidationError(fmt.Sprintf(
			"one of the following flags is required: %s",
			strings.Join(all, ", "),
		))
	}
	return nil
}

// validateAtLeastOne returns an error when none of the named flags are set.
func validateAtLeastOne(set map[string]bool, flags ...string) error {
	for _, f := range flags {
		if set[f] {
			return nil
		}
	}
	all := make([]string, len(flags))
	for i, f := range flags {
		all[i] = "--" + f
	}
	return errors.WrapValidationError(fmt.Sprintf(
		"at least one of the following flags is required: %s",
		strings.Join(all, ", "),
	))
}

// validateEnum returns an error when value is not one of the allowed values.
// The context parameter describes what is being validated (e.g., "--theme", "format").
func validateEnum(context, value string, allowed []string) error {
	if value == "" {
		return nil
	}
	for _, a := range allowed {
		if strings.EqualFold(value, a) {
			return nil
		}
	}
	return errors.WrapValidationError(fmt.Sprintf(
		"invalid %s %q — must be one of: %s",
		context, value, strings.Join(allowed, ", "),
	))
}

// validateRPCURL returns an error when rpcURL is set but is not a syntactically
// valid HTTP or HTTPS URL. It does not perform any network check.
func validateRPCURL(flag, rpcURL string) error {
	if rpcURL == "" {
		return nil
	}
	lower := strings.ToLower(strings.TrimSpace(rpcURL))
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return errors.WrapValidationError(fmt.Sprintf(
			"--%s %q must be a valid http:// or https:// URL\n"+
				"  Example: --%s https://soroban-testnet.stellar.org",
			flag, rpcURL, flag,
		))
	}
	return nil
}

// validateNonEmptyString returns an error when value is empty or whitespace-only.
// Use this for required free-form string flags.
func validateNonEmptyString(flag, value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.WrapValidationError(fmt.Sprintf(
			"--%s must not be empty", flag,
		))
	}
	return nil
}

// validateOutputDir returns an error when dir is non-empty but is not a
// writeable directory. If dir does not exist it is created.
func validateOutputDir(flag, dir string) error {
	if dir == "" {
		return nil
	}
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			// Attempt to create it so the user gets a clear error here rather
			// than a silent failure deep inside a long command.
			if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
				return errors.WrapValidationError(fmt.Sprintf(
					"--%s %q does not exist and could not be created: %v\n"+
						"  Fix: ensure the parent directory is writable",
					flag, dir, mkErr,
				))
			}
			return nil
		}
		return errors.WrapValidationError(fmt.Sprintf(
			"--%s: cannot access %q: %v", flag, dir, err,
		))
	}
	if !info.IsDir() {
		return errors.WrapValidationError(fmt.Sprintf(
			"--%s %q exists but is not a directory", flag, dir,
		))
	}
	return nil
}

// validateNoNullBytes returns an error when any path contains a null byte,
// which can be used for injection attacks on file-system calls.
func validateNoNullBytes(flag, value string) error {
	if strings.ContainsRune(value, 0) {
		return errors.WrapValidationError(fmt.Sprintf(
			"--%s: path contains null bytes and cannot be used\n"+
				"  Fix: remove null bytes from the value",
			flag,
		))
	}
	return nil
}

// validateGenerateBindingsArgs validates all flags for the generate-bindings
// command at parse time before any business logic runs.
// Deprecated: use validateGenerateBindingsFlags instead.
func validateGenerateBindingsArgs(network, wasmPath, outputDir string) error {
	args := []string{}
	if wasmPath != "" {
		args = []string{wasmPath}
	}
	return validateGenerateBindingsFlags(network, args, outputDir, "", "", "")
}

// validateGenerateBindingsFlags validates all flags for the generate-bindings
// command, including the new --runtime, --spec-file, and --spec-format flags.
func validateGenerateBindingsFlags(network string, args []string, outputDir, runtime, specFile, specFormat string) error {
	if err := validateNetwork(network); err != nil {
		return err
	}

	// Validate runtime target.
	if runtime != "" {
		validRuntimes := map[string]bool{"node": true, "browser": true, "universal": true}
		if !validRuntimes[strings.ToLower(runtime)] {
			return errors.WrapValidationError(fmt.Sprintf(
				"--runtime %q is not supported — must be one of: node, browser, universal", runtime,
			))
		}
	}

	// Validate spec-format.
	if specFormat != "" {
		validFormats := map[string]bool{"json": true, "xdr": true}
		if !validFormats[strings.ToLower(specFormat)] {
			return errors.WrapValidationError(fmt.Sprintf(
				"--spec-format %q is not supported — must be one of: json, xdr", specFormat,
			))
		}
	}

	// Validate spec-file and wasm-file are mutually exclusive.
	hasWasm := len(args) == 1 && args[0] != ""
	hasSpec := specFile != ""
	if hasWasm && hasSpec {
		return errors.WrapValidationError(
			"wasm-file argument and --spec-file are mutually exclusive — provide only one",
		)
	}

	if hasWasm {
		if err := validateFilePath("wasm", args[0]); err != nil {
			return err
		}
	}
	if hasSpec {
		if err := validateFilePath("spec-file", specFile); err != nil {
			return err
		}
	}

	if outputDir != "" {
		if info, err := os.Stat(outputDir); err == nil && !info.IsDir() {
			return errors.WrapValidationError(fmt.Sprintf(
				"--output %q exists but is not a directory", outputDir,
			))
		}
	}
	return nil
}

// validateCheckBindingsFlags validates all flags for the check-bindings command.
func validateCheckBindingsFlags(args []string, outputDir, specFile, specFormat string) error {
	hasWasm := len(args) == 1 && args[0] != ""
	hasSpec := specFile != ""

	if hasWasm && hasSpec {
		return errors.WrapValidationError(
			"wasm-file argument and --spec-file are mutually exclusive — provide only one",
		)
	}
	if !hasWasm && !hasSpec {
		return errors.WrapValidationError(
			"provide either a wasm-file argument or --spec-file",
		)
	}

	if hasWasm {
		if err := validateFilePath("wasm", args[0]); err != nil {
			return err
		}
	}
	if hasSpec {
		if err := validateFilePath("spec-file", specFile); err != nil {
			return err
		}
	}

	if specFormat != "" {
		validFormats := map[string]bool{"json": true, "xdr": true}
		if !validFormats[strings.ToLower(specFormat)] {
			return errors.WrapValidationError(fmt.Sprintf(
				"--spec-format %q is not supported — must be one of: json, xdr", specFormat,
			))
		}
	}

	if outputDir != "" {
		if info, err := os.Stat(outputDir); err == nil && !info.IsDir() {
			return errors.WrapValidationError(fmt.Sprintf(
				"--output %q exists but is not a directory", outputDir,
			))
		}
	}

	return nil
}

// validateAuditSignArgs validates all flags for the audit:sign command at
// parse time, catching mutually exclusive inputs and missing required options.
// The supported provider list is derived dynamically from the registry so
// newly registered providers are accepted without updating this function.
func validateAuditSignArgs(payload, payloadFile, provider string) error {
	if payload != "" && payloadFile != "" {
		return errors.WrapValidationError(
			"--payload and --payload-file are mutually exclusive — provide only one",
		)
	}
	if provider != "" {
		supported := signer.DefaultRegistry.Names()
		found := false
		for _, s := range supported {
			if strings.EqualFold(provider, s) {
				found = true
				break
			}
		}
		if !found {
			return errors.WrapValidationError(fmt.Sprintf(
				"--signing-provider %q is not supported — must be one of: %s",
				provider, strings.Join(supported, ", "),
			))
		}
	}
	return nil
}
