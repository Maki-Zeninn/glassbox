// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package progress

// ChannelSink publishes events to an unbuffered or buffered Go channel.
// It is the preferred consumer for daemon/IPC scenarios where the caller
// reads events from a goroutine.
//
// The channel is never closed by this sink; callers detect the end of the
// operation by observing a terminal event (IsTerminal == true) for
// PhaseDone.
//
// ChannelSink is safe for concurrent use.
type ChannelSink struct {
	ch   chan<- Event
	opID string
}

// NewChannelSink wraps ch (must be non-nil).  A buffer size of at least 16 is
// recommended to prevent emitters from blocking on slow consumers.
func NewChannelSink(ch chan<- Event) *ChannelSink {
	return &ChannelSink{ch: ch, opID: newOperationID()}
}

// Emit sends e to the channel.  If the channel is full and the send would
// block, the event is dropped silently so that emitters are never stalled.
func (cs *ChannelSink) Emit(e Event) {
	e.OperationID = cs.opID
	select {
	case cs.ch <- e:
	default:
		// Drop rather than block the emitting goroutine.
	}
}

// OperationID returns the shared operation ID for events from this sink.
func (cs *ChannelSink) OperationID() string { return cs.opID }
