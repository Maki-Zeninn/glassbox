// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package progress_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dotandev/glassbox/internal/progress"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── FakeClock tests ──────────────────────────────────────────────────────────

func TestFakeClock_MonotonicallyIncreasing(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := progress.NewFakeClock(base)

	t1 := clk.Now()
	t2 := clk.Now()
	t3 := clk.Now()

	assert.True(t, t2.After(t1), "second call must be after first")
	assert.True(t, t3.After(t2), "third call must be after second")
}

func TestFakeClock_DeterministicTimestamps(t *testing.T) {
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	clk := progress.NewFakeClock(base)
	clk.Step = 10 * time.Millisecond

	first := clk.Now()
	second := clk.Now()

	assert.Equal(t, base, first)
	assert.Equal(t, base.Add(10*time.Millisecond), second)
}

func TestEmitterWithFakeClock_TimestampsNonDecreasing(t *testing.T) {
	base := time.Date(2026, 3, 15, 8, 0, 0, 0, time.UTC)
	clk := progress.NewFakeClock(base)

	var buf strings.Builder
	sink := progress.NewJSONSink(&buf)
	em := progress.NewEmitterWithClock(sink, clk)

	em.Start(progress.PhaseInit, "starting")
	em.Complete(progress.PhaseInit, "done")
	em.Start(progress.PhaseFetch, "fetching")
	em.Complete(progress.PhaseFetch, "fetched")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, 4)

	var prev time.Time
	for _, line := range lines {
		var ev progress.Event
		require.NoError(t, json.Unmarshal([]byte(line), &ev))
		assert.False(t, ev.Timestamp.IsZero())
		if !prev.IsZero() {
			assert.True(t, !ev.Timestamp.Before(prev),
				"timestamps must be non-decreasing: %v is before %v", ev.Timestamp, prev)
		}
		prev = ev.Timestamp
	}
}

// ─── SequenceValidator tests ──────────────────────────────────────────────────

func TestSequenceValidator_HappyPath(t *testing.T) {
	sv := progress.NewSequenceValidator()

	phases := []struct {
		phase  progress.Phase
		status progress.Status
	}{
		{progress.PhaseInit, progress.StatusStart},
		{progress.PhaseInit, progress.StatusComplete},
		{progress.PhaseFetch, progress.StatusStart},
		{progress.PhaseFetch, progress.StatusComplete},
		{progress.PhaseSimulate, progress.StatusStart},
		{progress.PhaseSimulate, progress.StatusComplete},
		{progress.PhaseDone, progress.StatusComplete},
	}

	for _, tc := range phases {
		ev := progress.Event{Phase: tc.phase, Status: tc.status}
		assert.NoError(t, sv.Validate(ev), "phase=%s status=%s", tc.phase, tc.status)
	}
}

func TestSequenceValidator_OutOfOrder(t *testing.T) {
	sv := progress.NewSequenceValidator()

	// Advance to simulate phase.
	require.NoError(t, sv.Validate(progress.Event{Phase: progress.PhaseSimulate, Status: progress.StatusStart}))
	require.NoError(t, sv.Validate(progress.Event{Phase: progress.PhaseSimulate, Status: progress.StatusComplete}))

	// Attempting an earlier phase must fail.
	err := sv.Validate(progress.Event{Phase: progress.PhaseFetch, Status: progress.StatusStart})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "out of order")
}

func TestSequenceValidator_SkippedPhaseIsTerminal(t *testing.T) {
	sv := progress.NewSequenceValidator()

	// A skipped phase must be accepted and treated as terminal.
	require.NoError(t, sv.Validate(progress.Event{Phase: progress.PhaseFetch, Status: progress.StatusSkipped}))

	// Re-emitting a StatusStart for the same phase must now fail because
	// IsTerminal returned true for the skipped event and advanced lastOrder.
	err := sv.Validate(progress.Event{Phase: progress.PhaseFetch, Status: progress.StatusStart})
	require.Error(t, err, "StatusStart after StatusSkipped for the same phase must be rejected")
	assert.Contains(t, err.Error(), "out of order")
}

func TestSequenceValidator_AfterCancellation(t *testing.T) {
	sv := progress.NewSequenceValidator()
	sv.Cancel()

	err := sv.Validate(progress.Event{Phase: progress.PhaseInit, Status: progress.StatusStart})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "after cancellation")
}

func TestSequenceValidator_AfterDone(t *testing.T) {
	sv := progress.NewSequenceValidator()
	require.NoError(t, sv.Validate(progress.Event{Phase: progress.PhaseDone, Status: progress.StatusComplete}))

	err := sv.Validate(progress.Event{Phase: progress.PhaseInit, Status: progress.StatusStart})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "after operation completed")
}

// ─── ValidatingSink tests ─────────────────────────────────────────────────────

func TestValidatingSink_DropsOutOfOrderEvents(t *testing.T) {
	var buf strings.Builder
	inner := progress.NewJSONSink(&buf)
	vs := progress.NewValidatingSink(inner)

	var dropped []progress.Event
	vs.DroppedFn = func(e progress.Event, err *progress.SequenceError) {
		dropped = append(dropped, e)
	}

	// Forward to simulate.
	vs.Emit(progress.Event{Phase: progress.PhaseSimulate, Status: progress.StatusStart})
	vs.Emit(progress.Event{Phase: progress.PhaseSimulate, Status: progress.StatusComplete})

	// Try to go back to fetch — should be dropped.
	vs.Emit(progress.Event{Phase: progress.PhaseFetch, Status: progress.StatusStart})

	require.Len(t, dropped, 1)
	assert.Equal(t, progress.PhaseFetch, dropped[0].Phase)

	// Only 2 events should have reached the inner sink.
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	assert.Len(t, lines, 2)
}

func TestValidatingSink_ForwardsValidEvents(t *testing.T) {
	var buf strings.Builder
	inner := progress.NewJSONSink(&buf)
	vs := progress.NewValidatingSink(inner)

	vs.Emit(progress.Event{Phase: progress.PhaseInit, Status: progress.StatusStart})
	vs.Emit(progress.Event{Phase: progress.PhaseInit, Status: progress.StatusComplete})
	vs.Emit(progress.Event{Phase: progress.PhaseDone, Status: progress.StatusComplete})

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	assert.Len(t, lines, 3)
}

// ─── ChannelSink tests ────────────────────────────────────────────────────────

func TestChannelSink_DeliversEvents(t *testing.T) {
	ch := make(chan progress.Event, 16)
	sink := progress.NewChannelSink(ch)
	em := progress.NewEmitter(sink)

	em.Start(progress.PhaseInit, "starting")
	em.Complete(progress.PhaseInit, "done")

	require.Len(t, ch, 2)

	ev1 := <-ch
	assert.Equal(t, progress.PhaseInit, ev1.Phase)
	assert.Equal(t, progress.StatusStart, ev1.Status)

	ev2 := <-ch
	assert.Equal(t, progress.StatusComplete, ev2.Status)
}

func TestChannelSink_DropWhenFull(t *testing.T) {
	// Buffer of 1 — second event must be dropped without blocking.
	ch := make(chan progress.Event, 1)
	sink := progress.NewChannelSink(ch)

	done := make(chan struct{})
	go func() {
		defer close(done)
		sink.Emit(progress.Event{Phase: progress.PhaseInit, Status: progress.StatusStart})
		// Buffer now full; this must not block.
		sink.Emit(progress.Event{Phase: progress.PhaseInit, Status: progress.StatusComplete})
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("ChannelSink blocked on full channel")
	}

	assert.Len(t, ch, 1)
}

func TestChannelSink_OperationIDPropagated(t *testing.T) {
	ch := make(chan progress.Event, 4)
	sink := progress.NewChannelSink(ch)

	sink.Emit(progress.Event{Phase: progress.PhaseInit, Status: progress.StatusStart})
	sink.Emit(progress.Event{Phase: progress.PhaseDone, Status: progress.StatusComplete})

	ev1 := <-ch
	ev2 := <-ch

	assert.NotEmpty(t, ev1.OperationID)
	assert.Equal(t, ev1.OperationID, ev2.OperationID, "all events must share operation ID")
}

// ─── CancelableSink tests ─────────────────────────────────────────────────────

func TestCancelableSink_DropsAfterCancel(t *testing.T) {
	var buf strings.Builder
	inner := progress.NewJSONSink(&buf)
	ctx, cancel := context.WithCancel(context.Background())
	cs := progress.NewCancelableSink(ctx, inner)

	cs.Emit(progress.Event{Phase: progress.PhaseInit, Status: progress.StatusStart})
	cancel()

	// Give the goroutine time to observe the cancellation.
	time.Sleep(10 * time.Millisecond)

	cs.Emit(progress.Event{Phase: progress.PhaseFetch, Status: progress.StatusStart})

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	// Only the first event should be present.
	assert.Len(t, lines, 1)
	assert.True(t, cs.IsCancelled())
}

// ─── Emitter.Cancel tests ─────────────────────────────────────────────────────

func TestEmitter_Cancel_EmitsErrorEvent(t *testing.T) {
	var buf strings.Builder
	sink := progress.NewJSONSink(&buf)
	em := progress.NewEmitter(sink)

	em.Start(progress.PhaseFetch, "fetching")
	em.Cancel(progress.PhaseFetch)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, 2)

	var last progress.Event
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &last))
	assert.Equal(t, progress.StatusError, last.Status)
	assert.Equal(t, "operation_cancelled", last.ErrorCode)
}

func TestEmitter_Cancel_IsUnambiguous(t *testing.T) {
	var buf strings.Builder
	sink := progress.NewJSONSink(&buf)
	em := progress.NewEmitter(sink)

	em.Start(progress.PhaseSimulate, "simulating")
	em.Cancel(progress.PhaseSimulate)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	var last progress.Event
	require.NoError(t, json.Unmarshal([]byte(lines[len(lines)-1]), &last))

	assert.True(t, last.IsTerminal(), "cancel event must be terminal")
	assert.Equal(t, "operation_cancelled", last.ErrorCode)
}

// ─── Completion semantics ─────────────────────────────────────────────────────

func TestCompletion_DonePhaseIsUnambiguous(t *testing.T) {
	var buf strings.Builder
	sink := progress.NewJSONSink(&buf)
	em := progress.NewEmitter(sink)

	em.Complete(progress.PhaseDone, "command finished")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, 1)

	var ev progress.Event
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &ev))

	assert.Equal(t, progress.PhaseDone, ev.Phase)
	assert.Equal(t, progress.StatusComplete, ev.Status)
	assert.True(t, ev.IsTerminal())
}
