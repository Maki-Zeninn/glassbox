// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package progress defines structured progress events for long-running
// commands.  Events are emitted to stderr as newline-delimited JSON when the
// caller opts in via an --progress-json flag, leaving stdout available for
// machine-readable payload output.
package progress

import "time"

// Phase identifies the named stage of an operation.
type Phase string

const (
	// PhaseInit is emitted once before any work begins.
	PhaseInit Phase = "init"
	// PhaseFetch covers fetching transaction data from the network.
	PhaseFetch Phase = "fetch"
	// PhaseSimulate covers the simulation / replay step.
	PhaseSimulate Phase = "simulate"
	// PhaseAnalyze covers post-simulation analysis (security, token flows, etc.).
	PhaseAnalyze Phase = "analyze"
	// PhaseExport covers writing trace or snapshot output files.
	PhaseExport Phase = "export"
	// PhaseDone is emitted once after all phases complete successfully.
	PhaseDone Phase = "done"
)

// Status is the terminal or intermediate state of a phase.
type Status string

const (
	// StatusStart indicates the phase has begun.
	StatusStart Status = "start"
	// StatusComplete indicates the phase completed without error.
	StatusComplete Status = "complete"
	// StatusError indicates the phase failed.
	StatusError Status = "error"
	// StatusSkipped indicates the phase was skipped (e.g. no ledger to fetch).
	StatusSkipped Status = "skipped"
)

// Event is a single structured progress event.  It is always emitted to
// stderr so that stdout remains byte-for-byte compatible with payload output.
type Event struct {
	// OperationID groups all events from a single command invocation.
	OperationID string `json:"operation_id"`
	// Phase is the named lifecycle stage.
	Phase Phase `json:"phase"`
	// Status is start, complete, error, or skipped.
	Status Status `json:"status"`
	// Timestamp is the UTC time the event was emitted.
	Timestamp time.Time `json:"timestamp"`
	// Message is a human-readable description (optional, never secret).
	Message string `json:"message,omitempty"`
	// ErrorCode is a stable machine-readable identifier for failure events.
	// It is non-empty only when Status == StatusError.
	ErrorCode string `json:"error_code,omitempty"`
	// Meta carries safe, non-sensitive metadata for the phase.
	// Examples: {"network":"testnet"}, {"entry_count":42}.
	Meta map[string]interface{} `json:"meta,omitempty"`
}

// IsTerminal returns true when the event's Status represents a final state
// for the phase (complete, error, or skipped).
func (e Event) IsTerminal() bool {
	return e.Status == StatusComplete || e.Status == StatusError || e.Status == StatusSkipped
}
