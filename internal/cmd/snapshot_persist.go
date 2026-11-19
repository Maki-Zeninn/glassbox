// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/dotandev/glassbox/internal/config"
	"github.com/dotandev/glassbox/internal/errors"
	"github.com/dotandev/glassbox/internal/snapshot"
	"github.com/dotandev/glassbox/internal/version"
	"github.com/spf13/cobra"
)

var (
	snapSaveTxHashFlag   string
	snapSaveNetworkFlag  string
	snapSaveInputFlag    string
	snapSaveOutputFlag   string
	snapSaveEnvXdrFlag   string
	snapSaveMetaXdrFlag  string
	snapSaveWasmFlag     string

	snapLoadPathFlag    string
	snapLoadTxHashFlag  string
	snapLoadNetworkFlag string
	snapLoadVerifyFlag  bool
)

// snapshotCmd is the parent command for snapshot persistence operations.
var snapshotCmd = &cobra.Command{
	Use:     "snapshot",
	GroupID: "utility",
	Short:   "Save and load replay snapshots",
	Long: `Persist and restore Glassbox replay snapshots.

A snapshot bundles the ledger state, transaction envelope, and replay metadata
into a single JSON file. Saved snapshots can be reopened later without
re-fetching or recomputing state from the network.

Snapshot integrity:
  Each snapshot includes a content fingerprint and optional parameter
  fingerprint. On load, Glassbox verifies these to detect stale or corrupted
  snapshots before replay begins.

Examples:
  # Save the current ledger state to a snapshot file
  glassbox snapshot save --tx abc123 --network testnet --input state.json --output snap.json

  # Load a snapshot and verify it matches the expected transaction
  glassbox snapshot load --path snap.json --tx abc123 --network testnet

  # Inspect a snapshot without verifying identity
  glassbox snapshot load --path snap.json`,
}

// snapshotSaveCmd saves a ledger-state JSON file as a persisted snapshot.
var snapshotSaveCmd = &cobra.Command{
	Use:   "save",
	Short: "Save a ledger state as a persisted snapshot",
	Long: `Read a ledger-state JSON file (as produced by the debug command) and write
it as a persisted snapshot with full metadata and integrity fingerprints.

The output file can later be loaded with 'glassbox snapshot load' or passed
to 'glassbox debug --snapshot' to replay without network access.

Validation:
  The metadata (transaction hash, network) is validated before writing.
  The network must be one of: testnet, mainnet, futurenet.
  If validation fails an actionable error is printed with a remediation hint.`,
	RunE: runSnapshotSave,
}

// snapshotLoadCmd loads and inspects a persisted snapshot.
var snapshotLoadCmd = &cobra.Command{
	Use:   "load",
	Short: "Load and inspect a persisted snapshot",
	Long: `Read a persisted snapshot file, verify its integrity, and display its
metadata. Use --verify to also check that the snapshot matches the expected
transaction hash and network.`,
	RunE: runSnapshotLoad,
}

func init() {
	// save flags
	snapshotSaveCmd.Flags().StringVar(&snapSaveTxHashFlag, "tx", "", "Transaction hash this snapshot belongs to")
	snapshotSaveCmd.Flags().StringVar(&snapSaveNetworkFlag, "network", "testnet", "Stellar network (testnet, mainnet, futurenet)")
	snapshotSaveCmd.Flags().StringVar(&snapSaveInputFlag, "input", "", "Input snapshot JSON file (ledger state)")
	snapshotSaveCmd.Flags().StringVar(&snapSaveOutputFlag, "output", "", "Output path for the persisted snapshot")
	snapshotSaveCmd.Flags().StringVar(&snapSaveEnvXdrFlag, "envelope-xdr", "", "Base64 transaction envelope XDR (optional)")
	snapshotSaveCmd.Flags().StringVar(&snapSaveMetaXdrFlag, "result-meta-xdr", "", "Base64 result meta XDR (optional)")
	snapshotSaveCmd.Flags().StringVar(&snapSaveWasmFlag, "wasm", "", "Path to WASM file for source hash (local replay mode)")

	// load flags
	snapshotLoadCmd.Flags().StringVar(&snapLoadPathFlag, "path", "", "Path to the persisted snapshot file")
	snapshotLoadCmd.Flags().StringVar(&snapLoadTxHashFlag, "tx", "", "Expected transaction hash (enables identity check)")
	snapshotLoadCmd.Flags().StringVar(&snapLoadNetworkFlag, "network", "", "Expected network (enables identity check)")
	snapshotLoadCmd.Flags().BoolVar(&snapLoadVerifyFlag, "verify", false, "Verify snapshot integrity and identity")

	snapshotCmd.AddCommand(snapshotSaveCmd)
	snapshotCmd.AddCommand(snapshotLoadCmd)
	rootCmd.AddCommand(snapshotCmd)
}

func runSnapshotSave(cmd *cobra.Command, args []string) error {
	if snapSaveTxHashFlag == "" {
		return errors.WrapCliArgumentRequired("tx")
	}
	if snapSaveInputFlag == "" {
		return errors.WrapCliArgumentRequired("input")
	}

	// Validate network before doing any I/O.
	validNetworks := map[string]bool{"testnet": true, "mainnet": true, "futurenet": true}
	if !validNetworks[snapSaveNetworkFlag] {
		return fmt.Errorf(
			"unsupported network %q — must be one of: testnet, mainnet, futurenet\n"+
				"  Fix: re-run with --network testnet (or mainnet, futurenet)",
			snapSaveNetworkFlag,
		)
	}

	// Validate that the input file exists before loading it.
	if info, err := os.Stat(snapSaveInputFlag); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf(
				"input file %q does not exist\n"+
					"  Fix: provide a valid ledger-state JSON file with --input",
				snapSaveInputFlag,
			)
		}
		return fmt.Errorf("failed to stat input file %q: %w", snapSaveInputFlag, err)
	} else if info.IsDir() {
		return fmt.Errorf(
			"--input %q is a directory, not a file\n"+
				"  Fix: provide a JSON file path, not a directory",
			snapSaveInputFlag,
		)
	}

	// Load the input ledger-state snapshot.
	snap, err := snapshot.Load(snapSaveInputFlag)
	if err != nil {
		return errors.WrapValidationError(fmt.Sprintf("failed to load input snapshot: %v", err))
	}

	// Determine output path.
	outputPath := snapSaveOutputFlag
	if outputPath == "" {
		cfg, _ := config.Load()
		cacheDir := ""
		if cfg != nil {
			cacheDir = cfg.CachePath
		}
		outputPath = snapshot.DefaultSnapshotPath(cacheDir, snapSaveNetworkFlag, snapSaveTxHashFlag)
	} else {
		// Validate the explicitly provided output path.
		if err := validateSnapshotOutputPath(outputPath); err != nil {
			return err
		}
	}

	// Build metadata.
	meta := &snapshot.ReplayMetadata{
		GlassboxVersion: version.Version,
		TxHash:          snapSaveTxHashFlag,
		Network:         snapSaveNetworkFlag,
		EnvelopeXdr:     snapSaveEnvXdrFlag,
		ResultMetaXdr:   snapSaveMetaXdrFlag,
	}

	// Compute WASM source hash if a WASM file is provided.
	if snapSaveWasmFlag != "" {
		// Validate WASM path exists and is a regular file before reading.
		if info, err := os.Stat(snapSaveWasmFlag); err != nil {
			if os.IsNotExist(err) {
				return errors.WrapValidationError(fmt.Sprintf(
					"WASM file %q does not exist — provide a valid path with --wasm",
					snapSaveWasmFlag,
				))
			}
			return errors.WrapValidationError(fmt.Sprintf("failed to stat WASM file: %v", err))
		} else if info.IsDir() {
			return errors.WrapValidationError(fmt.Sprintf(
				"--wasm %q is a directory, not a file — provide a .wasm file path",
				snapSaveWasmFlag,
			))
		}
		wasmBytes, err := os.ReadFile(snapSaveWasmFlag)
		if err != nil {
			return errors.WrapValidationError(fmt.Sprintf("failed to read WASM file: %v", err))
		}
		meta.SourceHash = snapshot.HashWasmSource(wasmBytes)
	}

	// Build param fingerprint from the key replay parameters.
	meta.ParamFingerprint = snapshot.BuildParamFingerprint(map[string]string{
		"tx":      snapSaveTxHashFlag,
		"network": snapSaveNetworkFlag,
	})

	if err := snapshot.SavePersisted(outputPath, meta, snap); err != nil {
		return errors.WrapValidationError(fmt.Sprintf("failed to save snapshot: %v", err))
	}

	fmt.Printf("Snapshot saved: %s\n", outputPath)
	fmt.Printf("  Transaction: %s\n", snapSaveTxHashFlag)
	fmt.Printf("  Network:     %s\n", snapSaveNetworkFlag)
	fmt.Printf("  Entries:     %d ledger entries\n", len(snap.LedgerEntries))
	fmt.Printf("  Fingerprint: %s\n", snap.Fingerprint)
	if meta.SourceHash != "" {
		fmt.Printf("  WASM hash:   %s\n", meta.SourceHash)
	}
	fmt.Printf("\nLoad with: glassbox snapshot load --path %s\n", outputPath)
	return nil
}

// validateSnapshotOutputPath checks that a user-supplied output path is safe
// to write to: it must not contain null bytes, and if it already exists it
// must not be a directory.
func validateSnapshotOutputPath(path string) error {
	if path == "" {
		return nil // caller handles the empty case separately
	}
	// Reject null bytes — they are never valid in a file path.
	if strings.ContainsRune(path, 0) {
		return fmt.Errorf(
			"--output path contains a null byte, which is not allowed in file paths",
		)
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // path doesn't exist yet — that's fine for a new snapshot file
		}
		return fmt.Errorf("failed to stat output path %q: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf(
			"--output %q is an existing directory, not a file path\n"+
				"  Fix: provide a file path such as --output /path/to/snap.json",
			path,
		)
	}
	return nil
}

func runSnapshotLoad(cmd *cobra.Command, args []string) error {
	if snapLoadPathFlag == "" {
		return errors.WrapCliArgumentRequired("path")
	}

	// Validate that the file exists and is not a directory before attempting to parse it.
	if info, err := os.Stat(snapLoadPathFlag); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf(
				"snapshot file %q not found\n"+
					"  Fix: verify the path or run 'glassbox snapshot save' to create a new snapshot",
				snapLoadPathFlag,
			)
		}
		return fmt.Errorf("failed to stat snapshot file %q: %w", snapLoadPathFlag, err)
	} else if info.IsDir() {
		return fmt.Errorf(
			"--path %q is a directory, not a snapshot file\n"+
				"  Fix: provide the full path to the .snap.json file",
			snapLoadPathFlag,
		)
	}

	ps, err := snapshot.LoadPersisted(snapLoadPathFlag)
	if err != nil {
		return errors.WrapValidationError(fmt.Sprintf("failed to load snapshot: %v", err))
	}

	// Print metadata.
	fmt.Printf("Snapshot: %s\n", snapLoadPathFlag)
	fmt.Printf("  Schema version:   %d\n", ps.Metadata.SchemaVersion)
	fmt.Printf("  Glassbox version: %s\n", ps.Metadata.GlassboxVersion)
	fmt.Printf("  Saved at:         %s\n", ps.Metadata.SavedAt.Format("2006-01-02 15:04:05 UTC"))
	fmt.Printf("  Transaction:      %s\n", ps.Metadata.TxHash)
	fmt.Printf("  Network:          %s\n", ps.Metadata.Network)
	fmt.Printf("  Ledger entries:   %d\n", len(ps.Snapshot.LedgerEntries))
	fmt.Printf("  Fingerprint:      %s\n", ps.Snapshot.Fingerprint)
	if ps.Metadata.SourceHash != "" {
		fmt.Printf("  WASM hash:        %s\n", ps.Metadata.SourceHash)
	}
	if ps.Metadata.ParamFingerprint != "" {
		fmt.Printf("  Param hash:       %s\n", ps.Metadata.ParamFingerprint)
	}

	// Validate if requested or if expected values are provided.
	if snapLoadVerifyFlag || snapLoadTxHashFlag != "" || snapLoadNetworkFlag != "" {
		if err := ps.Validate(snapLoadTxHashFlag, snapLoadNetworkFlag); err != nil {
			fmt.Printf("\nValidation FAILED: %v\n", err)
			return errors.WrapValidationError(fmt.Sprintf("snapshot validation failed: %v", err))
		}
		fmt.Printf("\nValidation OK\n")
	}

	// Check staleness against current params if tx/network are provided.
	if snapLoadTxHashFlag != "" || snapLoadNetworkFlag != "" {
		params := map[string]string{}
		if snapLoadTxHashFlag != "" {
			params["tx"] = snapLoadTxHashFlag
		}
		if snapLoadNetworkFlag != "" {
			params["network"] = snapLoadNetworkFlag
		}
		if ps.IsStale(params, "") {
			fmt.Printf("\nWarning: snapshot may be stale (parameters have changed since it was saved).\n")
			fmt.Printf("Re-run the debug command to regenerate the snapshot.\n")
		}
	}

	fmt.Printf("\nUse with: glassbox debug --snapshot %s\n", snapLoadPathFlag)
	return nil
}
