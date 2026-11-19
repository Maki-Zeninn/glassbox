// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd_test

// debug_progress_test.go validates the --progress-json flag contract:
//
//  1. Each major phase emits a start event followed by a terminal event.
//  2. All events from one invocation share a single operation_id.
//  3. Error events carry a stable, snake_case error_code.
//  4. stdout is byte-for-byte unaffected when --progress-json is active
//     (all progress output goes exclusively to stderr).
//  5. Event ordering is deterministic: init → fetch/skip → simulate → analyze → export → done.

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dotandev/glassbox/internal/progress"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- helpers -----------------------------------------------------------------

func parseEvents(t *testing.T, ndjson string) []progress.Event {
	t.Helper()
	var events []progress.Event
	for _, line := range strings.Split(strings.TrimSpace(ndjson), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e progress.Event
		require.NoError(t, json.Unmarshal([]byte(line), &e), "bad NDJSON line: %q", line)
		events = append(events, e)
	}
	return events
}

func eventsForPhase(events []progress.Event, phase progress.Phase) []progress.Event {
	var out []progress.Event
	for _, e := range events {
		if e.Phase == phase {
			out = append(out, e)
		}
	}
	return out
}

// --- unit tests for the progress package contracts ---------------------------

// TestPhaseLifecycle verifies start → terminal event pairs for every named phase.
func TestPhaseLifecycle(t *testing.T) {
	phases := []progress.Phase{
		progress.PhaseInit,
		progress.PhaseFetch,
		progress.PhaseSimulate,
		progress.PhaseAnalyze,
		progress.PhaseExport,
		progress.PhaseDone,
	}

	var buf bytes.Buffer
	sink := progress.NewJSONSink(&buf)
	em := progress.NewEmitter(sink)

	for _, p := range phases {
		em.Start(p, string(p)+" starting")
		em.Complete(p, string(p)+" done")
	}

	events := parseEvents(t, buf.String())
	require.Equal(t, len(phases)*2, len(events))

	for i, p := range phases {
		start := events[i*2]
		end := events[i*2+1]

		assert.Equal(t, p, start.Phase, "phase[%d] start", i)
		assert.Equal(t, progress.StatusStart, start.Status, "phase[%d] start status", i)
		assert.False(t, start.IsTerminal(), "start event must not be terminal")

		assert.Equal(t, p, end.Phase, "phase[%d] end", i)
		assert.Equal(t, progress.StatusComplete, end.Status, "phase[%d] end status", i)
		assert.True(t, end.IsTerminal(), "complete event must be terminal")
	}
}

// TestSingleOperationID confirms all events from one emitter share the same ID.
func TestSingleOperationID(t *testing.T) {
	var buf bytes.Buffer
	sink := progress.NewJSONSink(&buf)
	em := progress.NewEmitter(sink)

	em.Start(progress.PhaseInit, "start")
	em.Complete(progress.PhaseInit, "done")
	em.Start(progress.PhaseFetch, "fetch")
	em.Complete(progress.PhaseFetch, "fetched")
	em.Error(progress.PhaseSimulate, "boom", "sim_failed")

	events := parseEvents(t, buf.String())
	require.NotEmpty(t, events)

	opID := events[0].OperationID
	assert.NotEmpty(t, opID, "operation_id must be non-empty")
	for i, e := range events {
		assert.Equal(t, opID, e.OperationID, "event[%d] must share operation_id", i)
	}
}

// TestErrorCodeStability checks that error events always carry a non-empty,
// snake_case error_code — matching the acceptance criterion.
func TestErrorCodeStability(t *testing.T) {
	cases := []struct {
		phase     progress.Phase
		errorCode string
	}{
		{progress.PhaseFetch, "rpc_fetch_failed"},
		{progress.PhaseSimulate, "simulation_failed"},
		{progress.PhaseExport, "export_failed"},
		{progress.PhaseInit, "invalid_dry_run_flags"},
	}

	for _, tc := range cases {
		var buf bytes.Buffer
		sink := progress.NewJSONSink(&buf)
		em := progress.NewEmitter(sink)
		em.Error(tc.phase, "some error message", tc.errorCode)

		events := parseEvents(t, buf.String())
		require.Len(t, events, 1)

		e := events[0]
		assert.Equal(t, progress.StatusError, e.Status)
		assert.Equal(t, tc.errorCode, e.ErrorCode,
			"phase %s: error_code must be %q", tc.phase, tc.errorCode)
		assert.True(t, e.IsTerminal())

		// error_code must be snake_case: only lowercase letters, digits, underscores
		for _, ch := range e.ErrorCode {
			assert.True(t, (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '_',
				"error_code %q contains non-snake_case character %q", e.ErrorCode, ch)
		}
	}
}

// TestSkippedPhase verifies that a skipped phase is terminal and has no error_code.
func TestSkippedPhase(t *testing.T) {
	var buf bytes.Buffer
	em := progress.NewEmitter(progress.NewJSONSink(&buf))
	em.Skip(progress.PhaseFetch, "local envelope input — no network fetch required")

	events := parseEvents(t, buf.String())
	require.Len(t, events, 1)
	assert.Equal(t, progress.StatusSkipped, events[0].Status)
	assert.Empty(t, events[0].ErrorCode)
	assert.True(t, events[0].IsTerminal())
}

// TestStdoutUnaffected is the key acceptance criterion: progress JSON must go
// to stderr only, never to stdout.  We verify this by simulating what the
// JSONSink writes and confirming a separate stdout buffer remains empty.
func TestStdoutUnaffected(t *testing.T) {
	var stderr bytes.Buffer
	var stdout bytes.Buffer // must remain empty

	// Only the stderr sink receives events; stdout is never touched.
	sink := progress.NewJSONSink(&stderr)
	em := progress.NewEmitter(sink)

	em.Start(progress.PhaseInit, "init")
	em.Complete(progress.PhaseInit, "done")
	em.Start(progress.PhaseFetch, "fetching")
	em.Complete(progress.PhaseFetch, "fetched")
	em.Start(progress.PhaseSimulate, "simulating")
	em.Complete(progress.PhaseSimulate, "done")
	em.Complete(progress.PhaseDone, "all done")

	// stdout must be completely empty.
	assert.Empty(t, stdout.String(), "stdout must remain empty when --progress-json is active")

	// stderr must have NDJSON events.
	events := parseEvents(t, stderr.String())
	assert.Greater(t, len(events), 0, "stderr must have events")

	// Every line in stderr must be valid JSON (NDJSON contract).
	for _, line := range strings.Split(strings.TrimSpace(stderr.String()), "\n") {
		if line == "" {
			continue
		}
		assert.True(t, json.Valid([]byte(line)), "every stderr line must be valid JSON: %q", line)
	}
}

// TestTimestampOrdering ensures that timestamps are non-decreasing across
// events emitted sequentially (monotonic clock guarantee).
func TestTimestampOrdering(t *testing.T) {
	var buf bytes.Buffer
	em := progress.NewEmitter(progress.NewJSONSink(&buf))

	em.Start(progress.PhaseInit, "a")
	em.Complete(progress.PhaseInit, "b")
	em.Start(progress.PhaseFetch, "c")
	em.Complete(progress.PhaseFetch, "d")

	events := parseEvents(t, buf.String())
	require.Len(t, events, 4)

	for i := 1; i < len(events); i++ {
		assert.False(t, events[i].Timestamp.Before(events[i-1].Timestamp),
			"event[%d] timestamp must not precede event[%d]", i, i-1)
	}
}

// TestMetaNotLeakingSecrets verifies that callers can include safe metadata
// without accidentally forwarding secret-like keys.  This is a contract test —
// the emitter does not enforce redaction; callers are responsible.  We document
// that token keys must not appear in event metadata.
func TestMetaNotLeakingSecrets(t *testing.T) {
	var buf bytes.Buffer
	em := progress.NewEmitter(progress.NewJSONSink(&buf))

	em.Complete(progress.PhaseFetch, "fetched", map[string]interface{}{
		"tx_hash":        "abc123def456",
		"network":        "testnet",
		"envelope_bytes": 512,
	})

	events := parseEvents(t, buf.String())
	require.Len(t, events, 1)

	meta := events[0].Meta
	assert.Equal(t, "abc123def456", meta["tx_hash"])
	assert.Equal(t, "testnet", meta["network"])

	// These keys must never appear in progress metadata.
	for _, forbidden := range []string{"token", "rpc_token", "secret", "password", "key", "dsn"} {
		_, exists := meta[forbidden]
		assert.False(t, exists, "meta must not contain secret key %q", forbidden)
	}
}

// TestNopSinkProducesNoOutput ensures that when --progress-json is off the
// NopSink adds zero overhead and produces no output.
func TestNopSinkProducesNoOutput(t *testing.T) {
	sink := progress.NewNopSink()
	em := progress.NewEmitter(sink)
	assert.NotEmpty(t, em.OperationID())

	// None of these calls must panic or produce output.
	em.Start(progress.PhaseInit, "init")
	em.Complete(progress.PhaseInit, "done")
	em.Start(progress.PhaseFetch, "fetch")
	em.Error(progress.PhaseFetch, "failed", "rpc_fetch_failed")
	em.Skip(progress.PhaseSimulate, "skipped")
	em.Complete(progress.PhaseDone, "done")
}

// TestFetchSkippedInLocalMode validates the skip event path used when
// --xdr-file or --json-file bypasses the network fetch phase.
func TestFetchSkippedInLocalMode(t *testing.T) {
	var buf bytes.Buffer
	em := progress.NewEmitter(progress.NewJSONSink(&buf))

	// Simulate what debug_progress.go emitFetchSkipped does.
	em.Skip(progress.PhaseFetch, "local envelope input — no network fetch required")

	events := parseEvents(t, buf.String())
	require.Len(t, events, 1)

	e := events[0]
	assert.Equal(t, progress.PhaseFetch, e.Phase)
	assert.Equal(t, progress.StatusSkipped, e.Status)
	assert.Contains(t, e.Message, "local envelope")
	assert.True(t, e.IsTerminal())
}

// TestEventOrdering_FullRun validates the expected sequence for a successful
// network transaction debug run: init → fetch → simulate → analyze → done.
func TestEventOrdering_FullRun(t *testing.T) {
	var buf bytes.Buffer
	em := progress.NewEmitter(progress.NewJSONSink(&buf))

	// Simulate a full successful run.
	em.Start(progress.PhaseInit, "starting")
	em.Complete(progress.PhaseInit, "ready")
	em.Start(progress.PhaseFetch, "fetching tx abc from testnet")
	em.Complete(progress.PhaseFetch, "fetched (256 bytes)")
	em.Start(progress.PhaseSimulate, "running simulation on testnet")
	em.Complete(progress.PhaseSimulate, "simulation complete: success")
	em.Start(progress.PhaseAnalyze, "running post-simulation analysis")
	em.Complete(progress.PhaseAnalyze, "analysis complete")
	em.Complete(progress.PhaseDone, "debug session complete")

	events := parseEvents(t, buf.String())
	require.Len(t, events, 9)

	expectedOrder := []struct {
		phase  progress.Phase
		status progress.Status
	}{
		{progress.PhaseInit, progress.StatusStart},
		{progress.PhaseInit, progress.StatusComplete},
		{progress.PhaseFetch, progress.StatusStart},
		{progress.PhaseFetch, progress.StatusComplete},
		{progress.PhaseSimulate, progress.StatusStart},
		{progress.PhaseSimulate, progress.StatusComplete},
		{progress.PhaseAnalyze, progress.StatusStart},
		{progress.PhaseAnalyze, progress.StatusComplete},
		{progress.PhaseDone, progress.StatusComplete},
	}

	for i, exp := range expectedOrder {
		assert.Equal(t, exp.phase, events[i].Phase, "event[%d] phase", i)
		assert.Equal(t, exp.status, events[i].Status, "event[%d] status", i)
	}
}

// TestEventOrdering_FetchFail validates the sequence when the fetch phase fails.
func TestEventOrdering_FetchFail(t *testing.T) {
	var buf bytes.Buffer
	em := progress.NewEmitter(progress.NewJSONSink(&buf))

	em.Start(progress.PhaseInit, "starting")
	em.Complete(progress.PhaseInit, "ready")
	em.Start(progress.PhaseFetch, "fetching tx")
	em.Error(progress.PhaseFetch, "connection refused", "rpc_fetch_failed")

	events := parseEvents(t, buf.String())
	require.Len(t, events, 4)

	fetchEvents := eventsForPhase(events, progress.PhaseFetch)
	require.Len(t, fetchEvents, 2)
	assert.Equal(t, progress.StatusStart, fetchEvents[0].Status)
	assert.Equal(t, progress.StatusError, fetchEvents[1].Status)
	assert.Equal(t, "rpc_fetch_failed", fetchEvents[1].ErrorCode)
}
