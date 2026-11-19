// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"sync"
)

// EventSequencer assigns monotonically increasing sequence IDs to events
// at collection time to preserve deterministic ordering across serialization,
// filtering, and merging operations.
type EventSequencer struct {
	mu         sync.Mutex
	nextSeqID  uint64
	parentStack []uint64 // stack of parent sequence IDs for nested calls
}

// NewEventSequencer creates a new EventSequencer starting at sequence ID 1.
func NewEventSequencer() *EventSequencer {
	return &EventSequencer{
		nextSeqID: 1,
		parentStack: make([]uint64, 0, 16),
	}
}

// NextSequenceID returns the next sequence ID and increments the counter.
// This is safe for concurrent use.
func (s *EventSequencer) NextSequenceID() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextSeqID
	s.nextSeqID++
	return id
}

// CurrentParent returns the current parent sequence ID (0 if no parent).
func (s *EventSequencer) CurrentParent() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.parentStack) == 0 {
		return 0
	}
	return s.parentStack[len(s.parentStack)-1]
}

// PushParent sets a new parent sequence ID for nested events.
func (s *EventSequencer) PushParent(parentID uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.parentStack = append(s.parentStack, parentID)
}

// PopParent removes the current parent sequence ID.
func (s *EventSequencer) PopParent() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.parentStack) > 0 {
		s.parentStack = s.parentStack[:len(s.parentStack)-1]
	}
}

// AssignSequenceIDs assigns sequence IDs to diagnostic events in order.
// Parent relationships are preserved from existing ParentSequenceID values.
func (s *EventSequencer) AssignSequenceIDs(events []DiagnosticEvent) {
	for i := range events {
		if events[i].SequenceID == 0 {
			events[i].SequenceID = s.NextSequenceID()
		}
	}
}

// AssignSequenceIDsWithParent assigns sequence IDs to diagnostic events
// and sets parent relationships based on the current parent stack.
func (s *EventSequencer) AssignSequenceIDsWithParent(events []DiagnosticEvent) {
	for i := range events {
		if events[i].SequenceID == 0 {
			events[i].SequenceID = s.NextSequenceID()
		}
		if events[i].ParentSequenceID == 0 {
			events[i].ParentSequenceID = s.CurrentParent()
		}
	}
}

// Reset resets the sequencer to its initial state.
func (s *EventSequencer) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextSeqID = 1
	s.parentStack = s.parentStack[:0]
}
