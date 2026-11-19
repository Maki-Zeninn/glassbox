// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package progress_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dotandev/glassbox/internal/progress"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// collectJSON drains all NDJSON lines from buf into a slice of Events.
func collectJSON(t *testing.T, buf *bytes.Buffer) []progress.Event {
	t.Helper()
	var events []progress.Event
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var e progress.Event
		require.NoError(t, json.Unmarshal([]byte(line), &e), "bad event line: %s", line)
		events = append(events, e)
	}
	return events
}

func TestJSONSink_EmitsSingleLine(t *testing.T) {
	var buf bytes.Buffer
	sink := progress.NewJSONSink(&buf)
	em := progress.NewEmitter(sink)

	em.Start(progress.PhaseFetch, "fetching tx")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	assert.Len(t, lines, 1, "exactly one line per Emit")
	assert.Contains(t, lines[0], `"phase":"fetch"`)
	assert.Contains(t, lines[0], `"status":"start"`)
}

func TestJSONSink_AllEventsShareOperationID(t *testing.T) {
	var buf bytes.Buffer
	sink := progress.NewJSONSink(&buf)
	em := progress.NewEmitter(sink)

	em.Start(progress.PhaseInit, "starting")
	em.Start(progress.PhaseFetch, "fetching")
	em.Complete(progress.PhaseFetch, "fetched")
	em.Complete(progress.PhaseDone, "done")

	events := collectJSON(t, &buf)
	require.Len(t, events, 4)

	opID := events[0].OperationID
	assert.NotEmpty(t, opID)
	for _, ev := range events {
		assert.Equal(t, opID, ev.OperationID, "all events must share the operation ID")
	}
}

func TestJSONSink_PhaseOrdering(t *testing.T) {
	var buf bytes.Buffer
	sink := progress.NewJSONSink(&buf)
	em := progress.NewEmitter(sink)

	phases := []progress.Phase{
		progress.PhaseInit,
		progress.PhaseFetch,
		progress.PhaseSimulate,
		progress.PhaseAnalyze,
		progress.PhaseExport,
		progress.PhaseDone,
	}

	for _, p := range phases {
		em.Start(p, string(p)+" start")
		em.Complete(p, string(p)+" done")
	}

	events := collectJSON(t, &buf)
	require.Len(t, events, len(phases)*2)

	for i, p := range phases {
		start := events[i*2]
		end := events[i*2+1]
		assert.Equal(t, p, start.Phase)
		assert.Equal(t, progress.StatusStart, start.Status)
		assert.Equal(t, p, end.Phase)
		assert.Equal(t, progress.StatusComplete, end.Status)
	}
}

func TestJSONSink_ErrorEventHasCode(t *testing.T) {
	var buf bytes.Buffer
	sink := progress.NewJSONSink(&buf)
	em := progress.NewEmitter(sink)

	em.Error(progress.PhaseFetch, "connection refused", "rpc_connection_failed")

	events := collectJSON(t, &buf)
	require.Len(t, events, 1)
	assert.Equal(t, progress.StatusError, events[0].Status)
	assert.Equal(t, "rpc_connection_failed", events[0].ErrorCode)
}

func TestJSONSink_SkippedEvent(t *testing.T) {
	var buf bytes.Buffer
	sink := progress.NewJSONSink(&buf)
	em := progress.NewEmitter(sink)

	em.Skip(progress.PhaseFetch, "local envelope mode — no network fetch")

	events := collectJSON(t, &buf)
	require.Len(t, events, 1)
	assert.Equal(t, progress.StatusSkipped, events[0].Status)
	assert.True(t, events[0].IsTerminal())
}

func TestJSONSink_MetaIncluded(t *testing.T) {
	var buf bytes.Buffer
	sink := progress.NewJSONSink(&buf)
	em := progress.NewEmitter(sink)

	em.Complete(progress.PhaseSimulate, "simulation done", map[string]interface{}{
		"network":     "testnet",
		"entry_count": 42,
	})

	events := collectJSON(t, &buf)
	require.Len(t, events, 1)
	assert.Equal(t, "testnet", events[0].Meta["network"])
	assert.InDelta(t, float64(42), events[0].Meta["entry_count"], 0)
}

func TestJSONSink_RedactsNoSecretsInMeta(t *testing.T) {
	// Meta values should never contain raw tokens; callers are responsible,
	// but the test documents this contract.
	var buf bytes.Buffer
	sink := progress.NewJSONSink(&buf)
	em := progress.NewEmitter(sink)

	em.Complete(progress.PhaseFetch, "fetched", map[string]interface{}{
		"tx_hash": "abc123",
		"network": "mainnet",
	})

	events := collectJSON(t, &buf)
	require.Len(t, events, 1)
	assert.Equal(t, "abc123", events[0].Meta["tx_hash"])
	// No token key present.
	_, hasToken := events[0].Meta["token"]
	assert.False(t, hasToken)
}

func TestNopSink_ProducesNoOutput(t *testing.T) {
	// NopSink must not panic and must discard all events silently.
	sink := progress.NewNopSink()
	em := progress.NewEmitter(sink)
	assert.NotEmpty(t, em.OperationID())

	// These calls must not panic.
	em.Start(progress.PhaseInit, "ok")
	em.Complete(progress.PhaseInit, "done")
	em.Error(progress.PhaseSimulate, "boom", "sim_failed")
	em.Skip(progress.PhaseFetch, "skipped")
}

func TestTextSink_HumanReadable(t *testing.T) {
	var buf bytes.Buffer
	sink := progress.NewTextSink(&buf)
	em := progress.NewEmitter(sink)

	em.Start(progress.PhaseFetch, "fetching transaction")
	em.Complete(progress.PhaseFetch, "transaction fetched")
	em.Error(progress.PhaseSimulate, "out of budget", "simulation_budget_exceeded")

	output := buf.String()
	assert.Contains(t, output, "→ fetching transaction")
	assert.Contains(t, output, "✓ transaction fetched")
	assert.Contains(t, output, "✗ out of budget")
	assert.Contains(t, output, "simulation_budget_exceeded")
}

func TestIsTerminal(t *testing.T) {
	assert.False(t, (progress.Event{Status: progress.StatusStart}).IsTerminal())
	assert.True(t, (progress.Event{Status: progress.StatusComplete}).IsTerminal())
	assert.True(t, (progress.Event{Status: progress.StatusError}).IsTerminal())
	assert.True(t, (progress.Event{Status: progress.StatusSkipped}).IsTerminal())
}

func TestTimestampIsSet(t *testing.T) {
	var buf bytes.Buffer
	sink := progress.NewJSONSink(&buf)
	em := progress.NewEmitter(sink)
	em.Start(progress.PhaseInit, "test")

	events := collectJSON(t, &buf)
	require.Len(t, events, 1)
	assert.False(t, events[0].Timestamp.IsZero(), "timestamp must be non-zero")
}

func TestClock_Now_ReturnsCurrentTime(t *testing.T) {
	clock := progress.RealClock{}
	now := clock.Now()
	epsilon := 1 * time.Second
	assert.WithinDuration(t, time.Now().UTC(), now, epsilon, "RealClock.Now() should be within one second of time.Now()")
}
