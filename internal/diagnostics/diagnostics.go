// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package diagnostics provides opt-in phase timing for CLI commands.
//
// Usage pattern:
//
//	c := diagnostics.NewCollector()
//	done := c.Start(diagnostics.PhaseRPCFetch)
//	// … do work …
//	done(err) // nil err = success; non-nil err closes phase as failed
//
//	// At the end of the command:
//	c.PrintHuman(os.Stderr)          // for --timings human output
//	// or
//	c.AppendJSON(&envelope.Timings)  // for --format json output
//
// The Collector is safe for concurrent use. Phase names are defined as typed
// constants so callers compile-time-check against the shared vocabulary.
//
// Durations are stored and emitted in milliseconds (float64) for JSON, which
// is the documented unit for consumers of the machine-readable output.
package diagnostics

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

// Phase identifies a logical execution phase that can be timed.
type Phase string

// Canonical phase names used across all commands. Adding a new phase here is
// the only change needed to make it available to all collectors.
const (
	// PhaseRPCFetch covers fetching transaction data from the RPC endpoint.
	PhaseRPCFetch Phase = "rpc_fetch"

	// PhaseSimulator covers the full simulator / replay execution.
	PhaseSimulator Phase = "simulator"

	// PhaseDecode covers decoding diagnostic events into a call tree.
	PhaseDecode Phase = "decode"

	// PhaseSourceMap covers DWARF source-mapping of WASM offsets to file:line.
	PhaseSourceMap Phase = "source_map"

	// PhaseTraceExport covers serialising and writing trace output files.
	PhaseTraceExport Phase = "trace_export"

	// PhaseTraceLoad covers reading and parsing a trace file from disk.
	PhaseTraceLoad Phase = "trace_load"

	// PhaseTraceRender covers rendering a trace to the terminal or a string buffer.
	PhaseTraceRender Phase = "trace_render"

	// PhaseLedgerFetch covers fetching ledger entries from the network.
	PhaseLedgerFetch Phase = "ledger_fetch"
)

// record holds the immutable result of a completed phase span.
type record struct {
	Phase    Phase
	Duration time.Duration
	Failed   bool
}

// DoneFunc is returned by Collector.Start and must be called exactly once to
// close the phase. Pass the phase's final error (or nil on success).
type DoneFunc func(err error)

// Collector accumulates phase timing records during a single command run.
// It is injectable for tests — create one with NewCollector() in production
// and pass a no-op via Noop() in unit tests that don't care about timing.
type Collector struct {
	mu            sync.Mutex
	enabled       bool
	records       []record
	correlationID string
}

// NewCollector returns an enabled Collector ready to record phases.
func NewCollector() *Collector {
	return &Collector{enabled: true}
}

// Noop returns a disabled Collector whose Start calls are zero-cost no-ops.
// Use this in tests or when --timings is not set.
func Noop() *Collector {
	return &Collector{enabled: false}
}

// Enabled reports whether timing collection is active.
func (c *Collector) Enabled() bool {
	return c.enabled
}

// SetCorrelationID sets the per-operation correlation ID for diagnostics.
func (c *Collector) SetCorrelationID(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.correlationID = id
}

// CorrelationID returns the current per-operation correlation ID.
func (c *Collector) CorrelationID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.correlationID
}

// Start begins timing phase p and returns a DoneFunc. The DoneFunc records
// the elapsed time and whether the phase ended in error. Calling Start on a
// disabled Collector returns a no-op DoneFunc with negligible overhead.
func (c *Collector) Start(p Phase) DoneFunc {
	if !c.enabled {
		return func(_ error) {}
	}
	start := time.Now()
	return func(err error) {
		d := time.Since(start)
		c.mu.Lock()
		c.records = append(c.records, record{
			Phase:    p,
			Duration: d,
			Failed:   err != nil,
		})
		c.mu.Unlock()
	}
}

// Records returns a snapshot of all completed phase records in insertion order.
func (c *Collector) Records() []PhaseRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]PhaseRecord, len(c.records))
	for i, r := range c.records {
		out[i] = PhaseRecord{
			Phase:      r.Phase,
			DurationMs: msFloat(r.Duration),
			Failed:     r.Failed,
		}
	}
	return out
}

// PhaseRecord is the exported, JSON-serialisable representation of one phase.
// DurationMs is in milliseconds (float64) — this is the documented unit for
// consumers of machine-readable output.
type PhaseRecord struct {
	// Phase is the canonical phase identifier (e.g. "rpc_fetch").
	Phase Phase `json:"phase"`
	// DurationMs is the wall-clock duration in milliseconds.
	DurationMs float64 `json:"duration_ms"`
	// Failed indicates that the phase terminated with an error.
	Failed bool `json:"failed,omitempty"`
}

// TimingsBlock is the JSON-serialisable container embedded in structured
// command output envelopes when --timings is active.
type TimingsBlock struct {
	// CorrelationID is the per-operation correlation ID.
	CorrelationID string `json:"correlation_id,omitempty"`
	// Phases lists each completed phase in chronological order.
	Phases []PhaseRecord `json:"phases"`
	// TotalMs is the sum of all phase durations (not wall-clock total).
	TotalMs float64 `json:"total_ms"`
}

// BuildTimingsBlock converts the collector into a TimingsBlock for JSON embedding.
func (c *Collector) BuildTimingsBlock() TimingsBlock {
	recs := c.Records()
	var total float64
	for _, r := range recs {
		total += r.DurationMs
	}
	return TimingsBlock{
		CorrelationID: c.CorrelationID(),
		Phases:        recs,
		TotalMs:       total,
	}
}

// MarshalJSON implements json.Marshaler so a *Collector can be embedded
// directly in output structs without the caller calling BuildTimingsBlock.
func (c *Collector) MarshalJSON() ([]byte, error) {
	return json.Marshal(c.BuildTimingsBlock())
}

// PrintHuman writes a compact human-readable timing table to w.
// Output is written to stderr by convention (timing info should not
// contaminate stdout / JSON output).
//
// Example output:
//
//	── Phase Timings ──────────────────────────────────
//	  Correlation ID: corr-12345678
//	  rpc_fetch      142ms
//	  simulator      891ms
//	  decode           3ms
//	─────────────────────────────────────────────────
func (c *Collector) PrintHuman(w io.Writer) {
	if w == nil || !c.enabled {
		return
	}
	recs := c.Records()
	corrID := c.CorrelationID()
	if len(recs) == 0 && corrID == "" {
		return
	}

	fmt.Fprintln(w, "\n── Phase Timings ──────────────────────────────────")
	if corrID != "" {
		fmt.Fprintf(w, "  Correlation ID: %s\n", corrID)
	}
	for _, r := range recs {
		d := time.Duration(r.DurationMs * float64(time.Millisecond))
		status := ""
		if r.Failed {
			status = " [failed]"
		}
		fmt.Fprintf(w, "  %-18s %s%s\n",
			string(r.Phase),
			d.Round(time.Millisecond).String(),
			status,
		)
	}
	fmt.Fprintln(w, "─────────────────────────────────────────────────")
}

// msFloat converts a time.Duration to milliseconds as a float64.
func msFloat(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}
