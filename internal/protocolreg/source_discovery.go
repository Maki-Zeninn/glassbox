// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package protocolreg

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// SourceDiscoveryResult is the output of DiscoverExecutableSource. It
// describes where the Glassbox binary was found (or why it could not be found)
// so callers can surface actionable diagnostics to the user.
type SourceDiscoveryResult struct {
	// Found is true when a usable executable path was located.
	Found bool
	// Path is the resolved, absolute executable path (empty when Found=false).
	Path string
	// Source describes how the path was found (e.g. "os.Executable", "PATH lookup", "fallback env").
	Source string
	// Fallback is true when the path was found via a fallback strategy rather
	// than the primary os.Executable() call.
	Fallback bool
	// Issues lists non-fatal warnings encountered during discovery.
	Issues []string
	// Hint is an actionable suggestion when Found=false.
	Hint string
}

// DiscoverExecutableSource attempts to locate the Glassbox binary using a
// multi-stage fallback strategy. This is used by protocol registration flows
// that need to embed the binary path in OS handler artefacts but cannot rely
// on os.Executable() alone (e.g. when invoked via 'go run', a wrapper script,
// or a broken symlink).
//
// Discovery stages (in order):
//  1. os.Executable() — the standard runtime path.
//  2. PATH lookup for "glassbox" — useful when invoked via a shell wrapper.
//  3. GLASSBOX_BIN environment variable — explicit user override.
//
// The first stage that produces a readable, non-empty path is used.
// Subsequent stages are attempted only when the previous one fails or produces
// an unusable path (empty, contains null bytes, file does not exist).
func DiscoverExecutableSource() *SourceDiscoveryResult {
	result := &SourceDiscoveryResult{}

	// Stage 1: os.Executable()
	if path, err := discoverViaOsExecutable(); err == nil && path != "" {
		result.Found = true
		result.Path = path
		result.Source = "os.Executable"
		return result
	} else if err != nil {
		result.Issues = append(result.Issues,
			fmt.Sprintf("os.Executable() failed: %v", err))
	}

	// Stage 2: PATH lookup
	if path, err := discoverViaPath(); err == nil && path != "" {
		result.Found = true
		result.Path = path
		result.Source = "PATH lookup"
		result.Fallback = true
		result.Issues = append(result.Issues,
			"primary os.Executable() source was unusable; resolved via PATH")
		return result
	} else if err != nil {
		result.Issues = append(result.Issues,
			fmt.Sprintf("PATH lookup for 'glassbox' failed: %v", err))
	}

	// Stage 3: GLASSBOX_BIN environment variable
	if path, err := discoverViaEnv(); err == nil && path != "" {
		result.Found = true
		result.Path = path
		result.Source = "GLASSBOX_BIN env"
		result.Fallback = true
		result.Issues = append(result.Issues,
			"primary sources were unusable; resolved via GLASSBOX_BIN environment variable")
		return result
	} else if err != nil {
		result.Issues = append(result.Issues,
			fmt.Sprintf("GLASSBOX_BIN env lookup failed: %v", err))
	}

	// All stages exhausted.
	result.Found = false
	result.Hint = "Set the GLASSBOX_BIN environment variable to the absolute path of the glassbox binary, " +
		"or reinstall Glassbox so it is available on PATH."
	return result
}

// discoverViaOsExecutable resolves the executable path using os.Executable and
// validates the result. It returns an error if the path is unusable.
func discoverViaOsExecutable() (string, error) {
	raw, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("os.Executable: %w", err)
	}
	return validateDiscoveredPath(raw, "os.Executable")
}

// discoverViaPath looks up "glassbox" on the system PATH.
func discoverViaPath() (string, error) {
	raw, err := exec.LookPath("glassbox")
	if err != nil {
		return "", fmt.Errorf("exec.LookPath: %w", err)
	}
	return validateDiscoveredPath(raw, "PATH")
}

// discoverViaEnv reads the GLASSBOX_BIN environment variable.
func discoverViaEnv() (string, error) {
	raw := os.Getenv("GLASSBOX_BIN")
	if raw == "" {
		return "", fmt.Errorf("GLASSBOX_BIN is not set")
	}
	return validateDiscoveredPath(raw, "GLASSBOX_BIN")
}

// maxDiscoveredPathLength is the maximum acceptable path length for discovered
// executables. This matches maxPathLength in registration.go for consistency.
const maxDiscoveredPathLength = 255

// validateDiscoveredPath checks that path is non-empty, free of null bytes,
// within length limits, and points to an existing regular file. On Unix it
// also checks that the file has execute permission. It resolves symlinks so
// the registered handler always points to the real binary.
func validateDiscoveredPath(raw, source string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("%s returned an empty path", source)
	}
	if strings.ContainsRune(raw, 0) {
		return "", fmt.Errorf("%s returned a path with null bytes: %q", source, raw)
	}

	// Reject overly long paths that would break registration artefacts.
	if len(raw) > maxDiscoveredPathLength {
		return "", fmt.Errorf(
			"%s returned a path that is too long (%d characters, maximum %d)\n"+
				"  Fix: move the glassbox binary to a shorter path (e.g., ~/.local/bin/glassbox) or create a symlink",
			source, len(raw), maxDiscoveredPathLength,
		)
	}

	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path for %q: %w", raw, err)
	}

	// Re-check length after abs resolution since the absolute form may be longer.
	if len(abs) > maxDiscoveredPathLength {
		return "", fmt.Errorf(
			"%s resolved to a path that is too long (%d characters, maximum %d)\n"+
				"  Fix: move the glassbox binary to a shorter path (e.g., ~/.local/bin/glassbox) or create a symlink",
			source, len(abs), maxDiscoveredPathLength,
		)
	}

	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("stat %q from %s: %w", abs, source, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%q from %s is a directory, not a binary", abs, source)
	}

	// On Unix, verify the file has execute permission so the registration
	// artefacts will actually be able to invoke the binary.
	if runtime.GOOS != "windows" {
		mode := info.Mode()
		if mode&0o111 == 0 {
			return "", fmt.Errorf(
				"%q from %s is not executable (permissions: %04o)\n"+
					"  Fix: run 'chmod +x %s' to make the binary executable",
				abs, source, mode&0o777, abs,
			)
		}
	}

	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		// Symlink evaluation failure is non-fatal — use the absolute path.
		return abs, nil
	}

	// Final path length check after symlink resolution.
	if len(resolved) > maxDiscoveredPathLength {
		return "", fmt.Errorf(
			"%s resolved symlink to a path that is too long (%d characters, maximum %d)\n"+
				"  Fix: move the glassbox binary to a shorter path or update the symlink",
			source, len(resolved), maxDiscoveredPathLength,
		)
	}

	return resolved, nil
}
