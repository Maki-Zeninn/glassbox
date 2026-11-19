// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package sourcemap

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dotandev/glassbox/internal/wasmvalidate"
)

const wasmTargetPath = "target/wasm32-unknown-unknown/release"

// wasmMagic is the 4-byte magic number that every valid WASM binary starts with.
var wasmMagic = []byte{0x00, 0x61, 0x73, 0x6d}

// HashMismatchError is returned when the local WASM hash does not match
// the expected on-chain hash.
type HashMismatchError struct {
	Path    string
	Local   string
	OnChain string
}

func (e *HashMismatchError) Error() string {
	return fmt.Sprintf(
		"build mismatch: local WASM hash %q does not match on-chain hash %q (path: %s)\n"+
			"  The local binary differs from the deployed contract — it may be outdated,\n"+
			"  built with different flags, or be a completely different contract.\n"+
			"  Hint: rebuild with 'cargo build --release --target wasm32-unknown-unknown'\n"+
			"  and verify --opt-level matches the on-chain deployment.",
		e.Local, e.OnChain, e.Path)
}

// DiscoveryResult holds the outcome of a DiscoverLocalSymbols call.
type DiscoveryResult struct {
	// Found maps SHA-256 hex hashes to their absolute WASM file paths.
	Found map[string]string
	// SearchDir is the directory that was scanned.
	SearchDir string
	// Warnings holds non-fatal issues encountered during scanning
	// (e.g. a WASM file that could not be read or has bad magic bytes).
	Warnings []string
}

// CheckHashMismatch computes the SHA256 hash of the WASM at path and
// compares it to onChainHash. It returns a HashMismatchError when they
// differ, so callers can surface a warning to the user.
func CheckHashMismatch(path, onChainHash string) error {
	if path == "" {
		return fmt.Errorf("WASM path must not be empty")
	}
	if onChainHash == "" {
		return fmt.Errorf("on-chain hash must not be empty")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read WASM file %q: %w", path, err)
	}
	if len(content) < 8 || content[0] != 0x00 || content[1] != 0x61 || content[2] != 0x73 || content[3] != 0x6d {
		return fmt.Errorf("not a valid WASM binary: %q does not start with WASM magic bytes", path)
	}
	sum := sha256.Sum256(content)
	local := hex.EncodeToString(sum[:])
	if local != onChainHash {
		return &HashMismatchError{Path: path, Local: local, OnChain: onChainHash}
	}
	return nil
}

// hasWasmMagic reports whether data starts with the 4-byte WASM magic number.
func hasWasmMagic(data []byte) bool {
	if len(data) < 4 {
		return false
	}
	return data[0] == wasmMagic[0] &&
		data[1] == wasmMagic[1] &&
		data[2] == wasmMagic[2] &&
		data[3] == wasmMagic[3]
}

// DiscoverLocalSymbols scans for WASM files in the local target directory
// under projectRoot. It returns a DiscoveryResult with all discovered WASM
// hashes mapped to their absolute file paths.
//
// Validation:
//   - projectRoot must not be empty or whitespace-only; returns an error if it is.
//   - projectRoot must not contain null bytes; returns an error if it does.
//   - The target directory must exist; returns a descriptive error if not found.
//   - Individual unreadable files are collected as Warnings rather than
//     causing a hard failure, so partial results are always returned.
//   - Files with an invalid WASM magic number (not starting with \0asm) are
//     skipped with a warning instead of being indexed. This prevents hash
//     collisions from corrupt or misnamed files.
func DiscoverLocalSymbols(projectRoot string) (*DiscoveryResult, error) {
	// Reject empty or whitespace-only root early.
	if strings.TrimSpace(projectRoot) == "" {
		return nil, fmt.Errorf(
			"source discovery: projectRoot must not be empty\n" +
				"  Hint: provide the path to your contract workspace root, " +
				"or use --contract-source to specify the source directory directly.",
		)
	}

	// Reject null bytes — they are a shell-injection risk and will cause
	// obscure failures deep in the OS path layer.
	if strings.ContainsRune(projectRoot, 0) {
		return nil, fmt.Errorf(
			"source discovery: projectRoot contains null bytes and cannot be used\n" +
				"  Hint: remove any null bytes from the path.",
		)
	}

	searchDir := filepath.Join(projectRoot, wasmTargetPath)
	result := &DiscoveryResult{
		Found:     make(map[string]string),
		SearchDir: searchDir,
	}

	info, err := os.Stat(searchDir)
	if err != nil {
		if os.IsNotExist(err) {
			return result, fmt.Errorf(
				"source discovery: local build directory not found: %q\n"+
					"  Expected WASM artifacts at: %s\n"+
					"  Run 'cargo build --release --target wasm32-unknown-unknown' to generate them,\n"+
					"  or use --contract-source <path> to point to the source directory directly.",
				searchDir, searchDir,
			)
		}
		return result, fmt.Errorf("source discovery: cannot access %q: %w", searchDir, err)
	}
	if !info.IsDir() {
		return result, fmt.Errorf(
			"source discovery: expected a directory at %q but found a file\n"+
				"  The WASM build output path must be a directory containing .wasm files.",
			searchDir,
		)
	}

	files, err := os.ReadDir(searchDir)
	if err != nil {
		return result, fmt.Errorf("source discovery: failed to read directory %q: %w", searchDir, err)
	}

	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".wasm") {
			continue
		}

		fullPath := filepath.Join(searchDir, file.Name())
		content, readErr := os.ReadFile(fullPath)
		if readErr != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("could not read %q: %v (skipped)", fullPath, readErr))
			continue
		}

		// Skip files that don't start with the WASM magic number (\0asm).
		// A file named .wasm but containing ELF, JSON, or other data would
		// produce a valid-looking but useless hash entry and waste lookup time.
		if !hasWasmMagic(content) {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf(
					"%q does not have a valid WASM magic number (\\0asm) — skipped\n"+
						"  Rebuild with 'cargo build --release --target wasm32-unknown-unknown'\n"+
						"  to ensure the file is a proper WASM binary.",
					fullPath,
				))
			continue
		}

		// Structurally validate the module before indexing it. A file with a
		// valid magic number but a corrupt or hostile section table would
		// otherwise be hashed and offered as a resolution candidate, only to
		// fail later — deeper in DWARF parsing — with a far less specific
		// error.
		if report := wasmvalidate.Validate(content, wasmvalidate.DefaultLimits()); !report.OK {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("%q failed WASM validation and was skipped: %v", fullPath, report.Error()))
			continue
		}

		hash := sha256.Sum256(content)
		hashStr := hex.EncodeToString(hash[:])
		result.Found[hashStr] = fullPath
	}

	return result, nil
}

// DiscoverLocalSymbolsLegacy is the legacy API that returns a plain map for
// backwards compatibility with callers that have not yet migrated to
// DiscoverLocalSymbols. New callers should prefer DiscoverLocalSymbols.
//
// Deprecated: Use DiscoverLocalSymbols which returns richer diagnostics.
func DiscoverLocalSymbolsLegacy(projectRoot string) (map[string]string, error) {
	result, err := DiscoverLocalSymbols(projectRoot)
	if result != nil {
		return result.Found, err
	}
	return nil, err
}
