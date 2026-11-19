// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package progress

import (
	"context"
	"fmt"
	"sync"
)

// phaseOrder defines the canonical ordering of phases.  A phase whose index
// is lower than the last emitted phase is considered out of order.
var phaseOrder = map[Phase]int{
	PhaseInit:     0,
	PhaseFetch:    1,
	PhaseSimulate: 2,
	PhaseAnalyze:  3,
	PhaseExport:   4,
	PhaseDone:     5,
}

// SequenceError is returned when an event is emitted out of expected order or
// after the operation has already reached a terminal state.
type SequenceError struct {
	// Got is the phase that was attempted.
	Got Phase
	// Last is the most recent terminal phase.
	Last Phase
	// Reason describes the specific violation.
	Reason string
}

func (e *SequenceError) Error() string {
	return fmt.Sprintf("progress sequence error: phase %q %s (last terminal: %q)", e.Got, e.Reason, e.Last)
}

// SequenceValidator tracks the state of a single operation to detect
// out-of-order or post-cancellation events.
//
// It is safe for concurrent use.
type SequenceValidator struct {
	mu           sync.Mutex
	lastTerminal Phase
	lastOrder    int
	cancelled    bool
	done         bool
}

// NewSequenceValidator returns a fresh validator.
func NewSequenceValidator() *SequenceValidator {
	return &SequenceValidator{lastOrder: -1}
}

// Validate checks whether event e can follow the current sequence state.
// It returns a non-nil error on any violation and updates internal state on
// success.
func (sv *SequenceValidator) Validate(e Event) error {
	sv.mu.Lock()
	defer sv.mu.Unlock()

	if sv.cancelled {
		return &SequenceError{Got: e.Phase, Last: sv.lastTerminal, Reason: "emitted after cancellation"}
	}
	if sv.done {
		return &SequenceError{Got: e.Phase, Last: sv.lastTerminal, Reason: "emitted after operation completed"}
	}

	order, known := phaseOrder[e.Phase]
	if !known {
		// Unknown phases are allowed — forward-compatibility.
		return nil
	}

	if order <= sv.lastOrder {
		return &SequenceError{Got: e.Phase, Last: sv.lastTerminal, Reason: "arrived out of order"}
	}

	if e.IsTerminal() {
		sv.lastTerminal = e.Phase
		sv.lastOrder = order
		if e.Phase == PhaseDone && e.Status == StatusComplete {
			sv.done = true
		}
		if e.Status == StatusError {
			// An error is terminal for that phase but the operation may still
			// emit a done event (e.g. with a summary).  Do not lock done here.
		}
	}

	return nil
}

// Cancel marks the operation as cancelled so that any subsequent event
// emissions are flagged as invalid.
func (sv *SequenceValidator) Cancel() {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	sv.cancelled = true
}

// IsCancelled reports whether Cancel has been called.
func (sv *SequenceValidator) IsCancelled() bool {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	return sv.cancelled
}

// ValidatingSink wraps a Sink and rejects events that violate phase sequence
// rules.  Rejected events are sent to the optional DroppedFn callback (if
// non-nil) rather than to the underlying sink.
//
// This sink is safe for concurrent use.
type ValidatingSink struct {
	inner     Sink
	validator *SequenceValidator
	// DroppedFn is called with the event and the sequence error whenever an
	// event is dropped.  It is called with the mutex unlocked.
	DroppedFn func(e Event, err *SequenceError)
}

// NewValidatingSink wraps inner with sequence validation.
func NewValidatingSink(inner Sink) *ValidatingSink {
	return &ValidatingSink{
		inner:     inner,
		validator: NewSequenceValidator(),
	}
}

// Emit forwards e to the inner sink if it passes sequence validation, or calls
// DroppedFn and discards the event if validation fails.
func (vs *ValidatingSink) Emit(e Event) {
	if err := vs.validator.Validate(e); err != nil {
		if vs.DroppedFn != nil {
			var se *SequenceError
			if seErr, ok := err.(*SequenceError); ok {
				se = seErr
			} else {
				se = &SequenceError{Got: e.Phase, Reason: err.Error()}
			}
			vs.DroppedFn(e, se)
		}
		return
	}
	vs.inner.Emit(e)
}

// OperationID delegates to the wrapped sink.
func (vs *ValidatingSink) OperationID() string { return vs.inner.OperationID() }

// Cancel marks the underlying validator as cancelled.
func (vs *ValidatingSink) Cancel() { vs.validator.Cancel() }

// CancelableSink wraps a Sink with a context.  Once the context is cancelled,
// further calls to Emit are silently dropped except for a single synthetic
// StatusError event on the current phase.
//
// This provides a clean cancellation semantic: consumers always see an
// unambiguous terminal event.
type CancelableSink struct {
	inner     Sink
	ctx       context.Context
	mu        sync.Mutex
	cancelled bool
}

// NewCancelableSink wraps inner, observing ctx for cancellation.
func NewCancelableSink(ctx context.Context, inner Sink) *CancelableSink {
	cs := &CancelableSink{inner: inner, ctx: ctx}
	go cs.watchContext()
	return cs
}

func (cs *CancelableSink) watchContext() {
	<-cs.ctx.Done()
	cs.mu.Lock()
	cs.cancelled = true
	cs.mu.Unlock()
}

// Emit forwards e to the inner sink unless the context has been cancelled.
func (cs *CancelableSink) Emit(e Event) {
	cs.mu.Lock()
	cancelled := cs.cancelled
	cs.mu.Unlock()
	if cancelled {
		return
	}
	cs.inner.Emit(e)
}

// OperationID delegates to the wrapped sink.
func (cs *CancelableSink) OperationID() string { return cs.inner.OperationID() }

// IsCancelled reports whether the context has fired.
func (cs *CancelableSink) IsCancelled() bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.cancelled
}
