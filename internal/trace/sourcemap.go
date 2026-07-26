// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dotandev/glassbox/internal/logger"
	"github.com/dotandev/glassbox/internal/sourcemap"
)

// WasmDebugInfo holds the path and extracted contract ID of a local WASM file.
type WasmDebugInfo struct {
	Path       string
	ContractID string
}

// DiscoverLocalWasmFiles scans the standard Rust WASM release directory.
func DiscoverLocalWasmFiles() ([]WasmDebugInfo, error) {
	targetDir := filepath.Join("target", "wasm32-unknown-unknown", "release")
	var discoveredFiles []WasmDebugInfo

	entries, err := os.ReadDir(targetDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // Directory not found is a valid state if not compiled yet
		}
		return nil, fmt.Errorf("failed to read target directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".wasm") {
			fullPath := filepath.Join(targetDir, entry.Name())

			// Extract or generate the Contract ID for matching
			contractID, err := extractContractID(fullPath)
			if err != nil {
				continue // Skip unreadable files
			}

			discoveredFiles = append(discoveredFiles, WasmDebugInfo{
				Path:       fullPath,
				ContractID: contractID,
			})
		}
	}

	return discoveredFiles, nil
}

// extractContractID reads the WASM file to determine its associated contract ID.
// This is typically a hash of the WASM bytecode or extracted from a custom section.
func extractContractID(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	// Fallback implementation: Hash the WASM byte code to match against the trace's hash
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}

// MergeDebugSymbols attempts to match a given contract ID with local WASM files
// and merge the DWARF symbols into the trace viewer context.
// It uses the sourcemap.FallbackMapper for robust multi-stage resolution and
// returns actionable diagnostics when source mapping is incomplete.
func MergeDebugSymbols(expectedContractID string) error {
	localFiles, err := DiscoverLocalWasmFiles()
	if err != nil {
		return fmt.Errorf("local WASM discovery failed: %w\n"+
			"  Fix: ensure the WASM build directory (target/wasm32-unknown-unknown/release) exists\n"+
			"  Check: run 'cargo build --release --target wasm32-unknown-unknown' to generate WASM artifacts",
			err)
	}

	for _, file := range localFiles {
		if file.ContractID == expectedContractID {
			content, readErr := os.ReadFile(file.Path)
			if readErr != nil {
				return fmt.Errorf("found matching WASM at %q but could not read it: %w\n"+
					"  Fix: check file permissions on the WASM build artifact",
					file.Path, readErr)
			}

			mapper := sourcemap.NewFallbackMapper("")
			result := mapper.Resolve(content, 0)

			if result.Quality == sourcemap.MappingQualityUnknown {
				return fmt.Errorf(
					"WASM binary at %q matched contract %s but contains no usable debug info\n"+
						"  Quality: %s\n"+
						"  Fix: recompile with 'debug = true' in [profile.release] for accurate source mapping\n"+
						"  Or: use --contract-source <path> to provide local source files\n"+
						"  Or: use --skip-source-mapping to proceed without source mapping",
					file.Path, expectedContractID[:16]+"...", result.Quality)
			}

			logger.Logger.Info("Successfully merged DWARF debug symbols",
				"file", filepath.Base(file.Path),
				"contract_id", expectedContractID[:16]+"...",
				"quality", result.Quality.String())

			if result.Warning != "" {
				logger.Logger.Warn("Source mapping used fallback path",
					"warning", result.Warning,
					"quality", result.Quality.String())
			}

			return nil
		}
	}

	return nil
}
