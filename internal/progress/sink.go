// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package progress

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// Sink receives progress events and forwards them to one or more outputs.
type Sink interface {
	// Emit sends a single event to the sink.
	Emit(e Event)
	// OperationID returns the ID shared by all events produced by this sink.
	OperationID() string
}

// NopSink discards all events.  It is the default when --progress-json is not
// set, so instrumented code paths add zero overhead.
type NopSink struct {
	opID string
}

// NewNopSink returns a NopSink with a fresh operation ID.
func NewNopSink() *NopSink {
	return &NopSink{opID: newOperationID()}
}

func (n *NopSink) Emit(_ Event) {}

func (n *NopSink) OperationID() string { return n.opID }

// JSONSink writes events as newline-delimited JSON to w (typically os.Stderr).
type JSONSink struct {
	w    io.Writer
	opID string
}

// NewJSONSink creates a sink that writes NDJSON events to w.
func NewJSONSink(w io.Writer) *JSONSink {
	return &JSONSink{w: w, opID: newOperationID()}
}

// Emit serialises e as a single JSON line.  Errors are silently discarded
// because progress output must never fail a command.
func (s *JSONSink) Emit(e Event) {
	e.OperationID = s.opID
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	// Write as a single atomic line; io.Writer.Write is not required to be
	// goroutine-safe, but sinks are typically only used from one goroutine.
	_, _ = fmt.Fprintf(s.w, "%s\n", data)
}

func (s *JSONSink) OperationID() string { return s.opID }

// TextSink writes human-readable progress lines to w.  It is used when the
// caller wants visible progress but not machine-readable JSON.
type TextSink struct {
	w    io.Writer
	opID string
}

// NewTextSink creates a sink that writes human-readable progress to w.
func NewTextSink(w io.Writer) *TextSink {
	return &TextSink{w: w, opID: newOperationID()}
}

// Emit writes a brief, human-readable description of the event.
func (s *TextSink) Emit(e Event) {
	e.OperationID = s.opID
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	switch e.Status {
	case StatusStart:
		_, _ = fmt.Fprintf(s.w, "[%s] → %s\n", e.Phase, e.Message)
	case StatusComplete:
		_, _ = fmt.Fprintf(s.w, "[%s] ✓ %s\n", e.Phase, e.Message)
	case StatusSkipped:
		_, _ = fmt.Fprintf(s.w, "[%s] – skipped: %s\n", e.Phase, e.Message)
	case StatusError:
		if e.ErrorCode != "" {
			_, _ = fmt.Fprintf(s.w, "[%s] ✗ %s (error_code=%s)\n", e.Phase, e.Message, e.ErrorCode)
		} else {
			_, _ = fmt.Fprintf(s.w, "[%s] ✗ %s\n", e.Phase, e.Message)
		}
	}
}

func (s *TextSink) OperationID() string { return s.opID }

// MultiSink fans out events to several sinks in order.
type MultiSink struct {
	sinks []Sink
	opID  string
}

// NewMultiSink combines multiple sinks into one.
func NewMultiSink(sinks ...Sink) *MultiSink {
	return &MultiSink{sinks: sinks, opID: newOperationID()}
}

func (m *MultiSink) Emit(e Event) {
	for _, s := range m.sinks {
		s.Emit(e)
	}
}

func (m *MultiSink) OperationID() string { return m.opID }

// newOperationID generates a fresh random 16-byte hex string as an operation ID.
func newOperationID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
