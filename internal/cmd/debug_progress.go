// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

// debug_progress.go instruments the debug command's major phases with
// structured progress events.  The events are opt-in via --progress-json and
// go exclusively to stderr so stdout payload output is unaffected.
//
// Import side-effects: registers the --progress-json flag on debugCmd during
// package init.

import (
	"fmt"
	"os"

	"github.com/dotandev/glassbox/internal/progress"
)

// progressJSONFlag is set by --progress-json on the debug command.
var progressJSONFlag bool

// buildDebugSink returns the appropriate Sink for the current invocation.
// When --progress-json is set it writes NDJSON to stderr; otherwise it returns
// a NopSink so instrumented paths add zero overhead.
func buildDebugSink() progress.Sink {
	if progressJSONFlag {
		return progress.NewJSONSink(os.Stderr)
	}
	return progress.NewNopSink()
}

// emitFetchStart emits the start event for the fetch phase, including safe
// metadata about which network and transaction are being fetched.
func emitFetchStart(em *progress.Emitter, txHash, network string) {
	em.Start(progress.PhaseFetch, fmt.Sprintf("fetching transaction %s from %s", txHash, network),
		map[string]interface{}{
			"tx_hash": txHash,
			"network": network,
		},
	)
}

// emitFetchComplete emits the completion event for the fetch phase.
func emitFetchComplete(em *progress.Emitter, txHash, network string, envelopeBytes int) {
	em.Complete(progress.PhaseFetch, fmt.Sprintf("transaction fetched (%d bytes)", envelopeBytes),
		map[string]interface{}{
			"tx_hash":        txHash,
			"network":        network,
			"envelope_bytes": envelopeBytes,
		},
	)
}

// emitFetchSkipped emits a skipped event when local envelope input mode is
// active and no network fetch is performed.
func emitFetchSkipped(em *progress.Emitter, reason string) {
	em.Skip(progress.PhaseFetch, reason)
}

// emitFetchError emits a stable error event for network fetch failures.
func emitFetchError(em *progress.Emitter, err error) {
	em.Error(progress.PhaseFetch,
		fmt.Sprintf("transaction fetch failed: %v", err),
		"rpc_fetch_failed",
	)
}

// emitSimulateStart emits the start of the simulation phase.
func emitSimulateStart(em *progress.Emitter, network string, ledgerEntries int) {
	em.Start(progress.PhaseSimulate, fmt.Sprintf("running simulation on %s", network),
		map[string]interface{}{
			"network":       network,
			"ledger_entries": ledgerEntries,
		},
	)
}

// emitSimulateComplete emits successful completion of the simulation phase.
func emitSimulateComplete(em *progress.Emitter, network, status string) {
	em.Complete(progress.PhaseSimulate, fmt.Sprintf("simulation complete: %s", status),
		map[string]interface{}{
			"network": network,
			"status":  status,
		},
	)
}

// emitSimulateError emits a stable error event when simulation fails.
func emitSimulateError(em *progress.Emitter, err error) {
	em.Error(progress.PhaseSimulate,
		fmt.Sprintf("simulation failed: %v", err),
		"simulation_failed",
	)
}

// emitAnalyzeStart emits the start of the post-simulation analysis phase.
func emitAnalyzeStart(em *progress.Emitter) {
	em.Start(progress.PhaseAnalyze, "running post-simulation analysis")
}

// emitAnalyzeComplete emits completion of the analysis phase.
func emitAnalyzeComplete(em *progress.Emitter) {
	em.Complete(progress.PhaseAnalyze, "analysis complete")
}

// emitExportStart emits the start of a trace or snapshot export.
func emitExportStart(em *progress.Emitter, path string) {
	em.Start(progress.PhaseExport, fmt.Sprintf("exporting trace to %s", path),
		map[string]interface{}{"path": path},
	)
}

// emitExportComplete emits successful completion of a trace export.
func emitExportComplete(em *progress.Emitter, path string) {
	em.Complete(progress.PhaseExport, fmt.Sprintf("trace exported to %s", path),
		map[string]interface{}{"path": path},
	)
}

// emitExportError emits a stable error event when export fails.
func emitExportError(em *progress.Emitter, path string, err error) {
	em.Error(progress.PhaseExport,
		fmt.Sprintf("export to %s failed: %v", path, err),
		"export_failed",
		map[string]interface{}{"path": path},
	)
}

// emitDone emits the terminal done event.
func emitDone(em *progress.Emitter) {
	em.Complete(progress.PhaseDone, "debug session complete")
}

func init() {
	debugCmd.Flags().BoolVar(&progressJSONFlag, "progress-json", false,
		"Emit structured progress events as newline-delimited JSON to stderr")
}
