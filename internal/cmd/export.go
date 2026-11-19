// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dotandev/glassbox/internal/clioutput"
	"github.com/dotandev/glassbox/internal/errors"
	"github.com/dotandev/glassbox/internal/plan"
	"github.com/dotandev/glassbox/internal/simulator"
	"github.com/dotandev/glassbox/internal/snapshot"
	"github.com/spf13/cobra"
)

var exportSnapshotFlag string
var exportIncludeMemoryFlag bool
var exportFormatFlag string
var exportPlanFlag bool // --plan: show execution plan without side effects

var decodeSnapshotFlag string
var decodeOffsetFlag int
var decodeLengthFlag int

// validExportFormats lists accepted --format values for the export command.
var validExportFormats = map[string]bool{
	"json": true,
	"text": true,
}

var exportCmd = &cobra.Command{
	Use:     "export",
	GroupID: "utility",
	Short:   "Export data from the current session",
	Long: `Export debugging data, such as state snapshots, from the currently active session.

Supported formats:
  text  — human-readable summary (default)
  json  — machine-readable JSON envelope

Examples:
  # Export the current ledger state snapshot
  glassbox export --snapshot ./state.snap.json

  # Export with linear memory included
  glassbox export --snapshot ./state.snap.json --include-memory

  # Export as machine-readable JSON
  glassbox export --snapshot ./state.snap.json --format json`,
	PreRunE: func(cmd *cobra.Command, args []string) error {
		// --plan bypasses all I/O, so only format validation is needed.
		if exportPlanFlag {
			if exportFormatFlag != "" {
				if !validExportFormats[strings.ToLower(strings.TrimSpace(exportFormatFlag))] {
					return errors.WrapValidationError(fmt.Sprintf(
						"invalid --format %q — must be one of: text, json\n"+
							"  Fix: use --format text (human-readable, default) or --format json (machine-readable)",
						exportFormatFlag,
					))
				}
			}
			return nil
		}

		// Validate --format before any work.
		if exportFormatFlag != "" {
			if !validExportFormats[strings.ToLower(strings.TrimSpace(exportFormatFlag))] {
				return errors.WrapValidationError(fmt.Sprintf(
					"invalid --format %q — must be one of: text, json\n"+
						"  Fix: use --format text (human-readable, default) or --format json (machine-readable)\n"+
						"  Example: glassbox export --snapshot ./state.snap.json --format json",
					exportFormatFlag,
				))
			}
		}

		if exportSnapshotFlag == "" {
			return errors.WrapValidationError(
				"--snapshot is required: provide an output file path for the exported snapshot\n" +
					"  Example: glassbox export --snapshot ./state.snap.json",
			)
		}

		// Normalize and validate the output path before any session or I/O work.
		// This catches null bytes, symlink escapes, and existing-directory targets
		// early so the error message is clear and actionable.
		if _, err := ValidateOutputPath("snapshot", exportSnapshotFlag); err != nil {
			return err
		}

		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		// --plan: show what will happen without doing it.
		if exportPlanFlag {
			execPlan := plan.BuildExportPlan(plan.ExportPlanOptions{
				SnapshotOutputPath: exportSnapshotFlag,
				IncludeMemory:      exportIncludeMemoryFlag,
				JSONOutput:         clioutput.WantsJSON(false, exportFormatFlag),
			})
			if clioutput.WantsJSON(false, exportFormatFlag) {
				jsonOut, jsonErr := execPlan.RenderJSON()
				if jsonErr != nil {
					return fmt.Errorf("failed to render plan: %w", jsonErr)
				}
				fmt.Fprintln(cmd.OutOrStdout(), jsonOut)
			} else {
				fmt.Fprint(cmd.OutOrStdout(), execPlan.RenderText())
			}
			return nil
		}

		// Get current session
		data := GetCurrentSession()
		if data == nil {
			return errors.WrapSimulationLogicError(
				"no active session — run 'glassbox debug <tx-hash>' first to create a session",
			)
		}

		// Unwrap simulation request to get ledger entries
		var simReq simulator.SimulationRequest
		if err := json.Unmarshal([]byte(data.SimRequestJSON), &simReq); err != nil {
			return errors.WrapUnmarshalFailed(err,
				"failed to decode session data; the session may be corrupt — try re-running 'glassbox debug'",
			)
		}

		if len(simReq.LedgerEntries) == 0 {
			fmt.Fprintln(cmd.ErrOrStderr(), "Warning: no ledger entries found in the current session. The exported snapshot will be empty.")
		}

		var encodedMemory string
		if exportIncludeMemoryFlag {
			memoryBase64, err := extractLinearMemoryBase64(data.SimResponseJSON)
			if err != nil {
				return errors.WrapUnmarshalFailed(err,
					"failed to decode linear memory from simulation response; try re-running 'glassbox debug'",
				)
			}
			if memoryBase64 == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "Warning: simulator response does not include a linear memory dump. Export will proceed without memory.")
			} else {
				if _, err := base64.StdEncoding.DecodeString(memoryBase64); err != nil {
					return errors.WrapValidationError(fmt.Sprintf(
						"linear memory dump in simulation response is not valid base64: %v\n"+
							"  Try re-running 'glassbox debug' to regenerate a clean session.",
						err,
					))
				}
				encodedMemory = memoryBase64
			}
		}

		snap := snapshot.FromMap(simReq.LedgerEntries)
		if encodedMemory != "" {
			snap.LinearMemory = encodedMemory
		}

		// Save
		if err := snapshot.Save(exportSnapshotFlag, snap); err != nil {
			return errors.WrapValidationError(fmt.Sprintf(
				"failed to save snapshot to %q: %v\n"+
					"  Check that the directory exists and you have write permissions.",
				exportSnapshotFlag, err,
			))
		}

		// Output — text or JSON
		if clioutput.WantsJSON(false, exportFormatFlag) {
			return clioutput.WriteStdout("export", map[string]interface{}{
				"snapshot_path":   exportSnapshotFlag,
				"entry_count":     len(snap.LedgerEntries),
				"fingerprint":     snap.Fingerprint,
				"includes_memory": encodedMemory != "",
			})
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Snapshot exported to %s (%d entries)\n", exportSnapshotFlag, len(snap.LedgerEntries))
		if snap.LinearMemory != "" {
			decodedBytes := base64.StdEncoding.DecodedLen(len(snap.LinearMemory)) - strings.Count(snap.LinearMemory, "=")
			fmt.Fprintf(cmd.OutOrStdout(), "Included linear memory dump: %d bytes\n", decodedBytes)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Fingerprint: %s\n", snap.Fingerprint)
		fmt.Fprintf(cmd.OutOrStdout(), "\nLoad with: glassbox debug --snapshot %s\n", exportSnapshotFlag)
		return nil
	},
}

var exportDecodeMemoryCmd = &cobra.Command{
	Use:   "decode-memory",
	Short: "Decode and print a linear memory dump from a snapshot",
	Long: `Decode and hex-dump a slice of the Wasm linear memory stored in a snapshot.

Use --offset and --length to select the byte range to inspect.

Example:
  glassbox export decode-memory --snapshot ./state.snap.json --offset 0 --length 256`,
	PreRunE: func(cmd *cobra.Command, args []string) error {
		if decodeSnapshotFlag == "" {
			return errors.WrapValidationError(
				"--snapshot is required: provide the path to a snapshot file\n" +
					"  Example: glassbox export decode-memory --snapshot ./state.snap.json",
			)
		}
		if err := validateFilePath("snapshot", decodeSnapshotFlag); err != nil {
			return err
		}
		if decodeOffsetFlag < 0 {
			return errors.WrapValidationError(fmt.Sprintf(
				"--offset must be >= 0 (got %d)", decodeOffsetFlag,
			))
		}
		if decodeLengthFlag <= 0 {
			return errors.WrapValidationError(fmt.Sprintf(
				"--length must be > 0 (got %d)", decodeLengthFlag,
			))
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		snap, err := snapshot.Load(decodeSnapshotFlag)
		if err != nil {
			return errors.WrapValidationError(fmt.Sprintf(
				"failed to load snapshot %q: %v\n"+
					"  Verify the file exists and is a valid Glassbox snapshot (produced by 'glassbox export' or 'glassbox debug --save-snapshots').",
				decodeSnapshotFlag, err,
			))
		}

		memory, err := snap.DecodeLinearMemory()
		if err != nil {
			return errors.WrapValidationError(fmt.Sprintf(
				"failed to decode linear memory from snapshot: %v\n"+
					"  The snapshot may not include a memory dump. Re-run 'glassbox export --include-memory'.",
				err,
			))
		}
		if len(memory) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No linear memory dump found in snapshot.")
			fmt.Fprintln(cmd.OutOrStdout(), "  Tip: re-export with 'glassbox export --snapshot <file> --include-memory'.")
			return nil
		}

		if decodeOffsetFlag >= len(memory) {
			return errors.WrapValidationError(fmt.Sprintf(
				"--offset %d is out of bounds: snapshot memory is %d bytes total",
				decodeOffsetFlag, len(memory),
			))
		}

		end := decodeOffsetFlag + decodeLengthFlag
		if end > len(memory) {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"Warning: requested range [%d:%d] exceeds memory size (%d bytes); truncating to [%d:%d].\n",
				decodeOffsetFlag, end, len(memory), decodeOffsetFlag, len(memory),
			)
			end = len(memory)
		}

		segment := memory[decodeOffsetFlag:end]
		fmt.Fprintf(cmd.OutOrStdout(), "Linear memory segment [%d:%d] (%d bytes)\n", decodeOffsetFlag, end, len(segment))
		for i := 0; i < len(segment); i += 16 {
			lineEnd := i + 16
			if lineEnd > len(segment) {
				lineEnd = len(segment)
			}
			line := segment[i:lineEnd]
			fmt.Fprintf(cmd.OutOrStdout(), "0x%08x  ", decodeOffsetFlag+i)
			for _, b := range line {
				fmt.Fprintf(cmd.OutOrStdout(), "%02x ", b)
			}
			for j := len(line); j < 16; j++ {
				fmt.Fprint(cmd.OutOrStdout(), "   ")
			}
			fmt.Fprint(cmd.OutOrStdout(), " |")
			for _, b := range line {
				if b >= 32 && b <= 126 {
					fmt.Fprintf(cmd.OutOrStdout(), "%c", b)
				} else {
					fmt.Fprint(cmd.OutOrStdout(), ".")
				}
			}
			fmt.Fprintln(cmd.OutOrStdout(), "|")
		}

		return nil
	},
}

func init() {
	exportCmd.Flags().StringVar(&exportSnapshotFlag, "snapshot", "", "Output file path for the exported JSON snapshot")
	exportCmd.Flags().BoolVar(&exportIncludeMemoryFlag, "include-memory", false, "Include Wasm linear memory dump from simulation response when available")
	exportCmd.Flags().StringVar(&exportFormatFlag, "format", "text", "Output format: text or json")
	exportCmd.Flags().BoolVar(&exportPlanFlag, "plan", false, "Print the execution plan (files, outputs) without writing anything")

	_ = exportCmd.RegisterFlagCompletionFunc("format", completeExportFormatFlag)

	exportDecodeMemoryCmd.Flags().StringVar(&decodeSnapshotFlag, "snapshot", "", "Snapshot file that contains linear memory")
	exportDecodeMemoryCmd.Flags().IntVar(&decodeOffsetFlag, "offset", 0, "Start offset in bytes (must be >= 0)")
	exportDecodeMemoryCmd.Flags().IntVar(&decodeLengthFlag, "length", 256, "Number of bytes to print (must be > 0)")

	exportCmd.AddCommand(exportDecodeMemoryCmd)
	rootCmd.AddCommand(exportCmd)
}

func extractLinearMemoryBase64(simResponseJSON string) (string, error) {
	if simResponseJSON == "" {
		return "", nil
	}

	var payload struct {
		LinearMemoryDump   string `json:"linear_memory_dump"`
		LinearMemoryBase64 string `json:"linear_memory_base64"`
		LinearMemory       string `json:"linear_memory"`
	}

	if err := json.Unmarshal([]byte(simResponseJSON), &payload); err != nil {
		return "", err
	}

	if payload.LinearMemoryDump != "" {
		return payload.LinearMemoryDump, nil
	}

	if payload.LinearMemoryBase64 != "" {
		return payload.LinearMemoryBase64, nil
	}

	return payload.LinearMemory, nil
}
