// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package progress

import (
	"time"

	"github.com/dotandev/glassbox/internal/telemetry"
)

// Emitter is a convenience wrapper around a Sink that stamps events with the
// shared operation ID and current time, so call sites only supply phase,
// status, message, and optional metadata.
//
// Use NewEmitterWithClock to inject a FakeClock in tests for deterministic
// timestamp assertions.
type Emitter struct {
	sink  Sink
	clock Clock
}

// NewEmitter wraps sink with helper methods using the real wall clock.
func NewEmitter(sink Sink) *Emitter {
	return &Emitter{sink: sink, clock: RealClock{}}
}

// NewEmitterWithClock wraps sink and uses clock for event timestamps.
// Pass a *FakeClock in tests to produce deterministic, non-zero timestamps.
func NewEmitterWithClock(sink Sink, clock Clock) *Emitter {
	return &Emitter{sink: sink, clock: clock}
}

// Start emits a StatusStart event for the given phase.
func (e *Emitter) Start(phase Phase, message string, meta ...map[string]interface{}) {
	ev := Event{
		OperationID: e.sink.OperationID(),
		Phase:       phase,
		Status:      StatusStart,
		Timestamp:   e.clock.Now(),
		Message:     message,
	}
	if len(meta) > 0 {
		ev.Meta = meta[0]
	}

	// Validate event against telemetry registry before emitting.
	// Emit unconditionally to avoid breaking existing behaviour if validation
	// is not yet strict for this event type.
	if err := validateEvent(ev); err != nil {
		e.sink.Emit(ev)
		return
	}

	e.sink.Emit(ev)
}

// Complete emits a StatusComplete event for the given phase.
func (e *Emitter) Complete(phase Phase, message string, meta ...map[string]interface{}) {
	ev := Event{
		OperationID: e.sink.OperationID(),
		Phase:       phase,
		Status:      StatusComplete,
		Timestamp:   e.clock.Now(),
		Message:     message,
	}
	if len(meta) > 0 {
		ev.Meta = meta[0]
	}

	if err := validateEvent(ev); err != nil {
		e.sink.Emit(ev)
		return
	}

	e.sink.Emit(ev)
}

// Error emits a StatusError event for the given phase.
// errorCode should be a stable snake_case identifier (e.g. "rpc_connection_failed").
func (e *Emitter) Error(phase Phase, message, errorCode string, meta ...map[string]interface{}) {
	ev := Event{
		OperationID: e.sink.OperationID(),
		Phase:       phase,
		Status:      StatusError,
		Timestamp:   e.clock.Now(),
		Message:     message,
		ErrorCode:   errorCode,
	}
	if len(meta) > 0 {
		ev.Meta = meta[0]
	}

	if err := validateEvent(ev); err != nil {
		e.sink.Emit(ev)
		return
	}

	e.sink.Emit(ev)
}

// Skip emits a StatusSkipped event for the given phase.
func (e *Emitter) Skip(phase Phase, message string, meta ...map[string]interface{}) {
	ev := Event{
		OperationID: e.sink.OperationID(),
		Phase:       phase,
		Status:      StatusSkipped,
		Timestamp:   e.clock.Now(),
		Message:     message,
	}
	if len(meta) > 0 {
		ev.Meta = meta[0]
	}

	if err := validateEvent(ev); err != nil {
		e.sink.Emit(ev)
		return
	}

	e.sink.Emit(ev)
}

// Cancel emits a StatusError event for the given phase using a well-known
// "operation_cancelled" error code and then marks any ValidatingSink or
// CancelableSink in the chain.  Normal final output is unaffected because
// progress events always go to stderr.
func (e *Emitter) Cancel(phase Phase) {
	ev := Event{
		OperationID: e.sink.OperationID(),
		Phase:       phase,
		Status:      StatusError,
		Timestamp:   e.clock.Now(),
		Message:     "operation cancelled",
		ErrorCode:   "operation_cancelled",
	}
	e.sink.Emit(ev)

	// Propagate cancellation to sinks that support it.
	type canceller interface{ Cancel() }
	if c, ok := e.sink.(canceller); ok {
		c.Cancel()
	}
}

// OperationID returns the operation ID shared by all events from this emitter.
func (e *Emitter) OperationID() string {
	return e.sink.OperationID()
}

// validateEvent converts an Event to a map and validates it against the
// telemetry registry.
func validateEvent(ev Event) error {
	payload := map[string]interface{}{
		"operation_id": ev.OperationID,
		"phase":        string(ev.Phase),
		"status":       string(ev.Status),
		"timestamp":    ev.Timestamp.UTC().Format(time.RFC3339Nano),
	}
	if ev.Message != "" {
		payload["message"] = ev.Message
	}
	if ev.ErrorCode != "" {
		payload["error_code"] = ev.ErrorCode
	}
	if ev.Meta != nil {
		payload["meta"] = ev.Meta
	}

	return telemetry.Validate("debug.progress", payload)
}
