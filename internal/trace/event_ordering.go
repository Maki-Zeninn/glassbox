// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"sort"
	"strings"
)

// EventOrdering provides deterministic sorting and tie-breaking for events
// to ensure stable ordering across runs and platforms.
type EventOrdering struct{}

// SortStatesBySequenceID sorts ExecutionState slice by SequenceID.
// Falls back to Step index if SequenceID is zero (backward compatibility).
func (o *EventOrdering) SortStatesBySequenceID(states []ExecutionState) {
	sort.Slice(states, func(i, j int) bool {
		// Primary sort by SequenceID
		if states[i].SequenceID != states[j].SequenceID {
			return states[i].SequenceID < states[j].SequenceID
		}
		// Secondary sort by Step (for events with same SequenceID)
		return states[i].Step < states[j].Step
	})
}

// SortDiagnosticEventsBySequenceID sorts DiagnosticEvent slice by SequenceID.
// Falls back to deterministic string comparison if SequenceID is zero.
func (o *EventOrdering) SortDiagnosticEventsBySequenceID(events []DiagnosticEvent) {
	sort.Slice(events, func(i, j int) bool {
		// Primary sort by SequenceID
		if events[i].SequenceID != events[j].SequenceID {
			return events[i].SequenceID < events[j].SequenceID
		}
		// Secondary sort by deterministic string key
		return o.eventKey(&events[i]) < o.eventKey(&events[j])
	})
}

// SortContractEventsBySequenceID sorts ContractEvent slice by SequenceID.
// Falls back to deterministic string comparison if SequenceID is zero.
func (o *EventOrdering) SortContractEventsBySequenceID(events []*ContractEvent) {
	sort.Slice(events, func(i, j int) bool {
		// Primary sort by SequenceID
		if events[i].SequenceID != events[j].SequenceID {
			return events[i].SequenceID < events[j].SequenceID
		}
		// Secondary sort by deterministic string key
		return o.contractEventKey(events[i]) < o.contractEventKey(events[j])
	})
}

// eventKey creates a deterministic string key for tie-breaking DiagnosticEvents.
// Uses contract ID, event type, and data to ensure stable ordering.
func (o *EventOrdering) eventKey(e *DiagnosticEvent) string {
	parts := []string{}
	if e.ContractID != nil {
		parts = append(parts, *e.ContractID)
	} else {
		parts = append(parts, "")
	}
	parts = append(parts, e.EventType)
	parts = append(parts, e.Data)
	if e.WasmInstruction != nil {
		parts = append(parts, *e.WasmInstruction)
	}
	// Join with null byte to avoid collisions
	return strings.Join(parts, "\x00")
}

// contractEventKey creates a deterministic string key for tie-breaking ContractEvents.
// Uses contract ID, type, topics, and data to ensure stable ordering.
func (o *EventOrdering) contractEventKey(e *ContractEvent) string {
	parts := []string{e.ContractID, e.Type, e.Data}
	for _, topic := range e.Topics {
		parts = append(parts, topic)
	}
	// Join with null byte to avoid collisions
	return strings.Join(parts, "\x00")
}

// ValidateSequenceOrder checks that sequence IDs are monotonically increasing.
// Returns the index of the first violation, or -1 if order is valid.
func (o *EventOrdering) ValidateSequenceOrder(states []ExecutionState) int {
	for i := 1; i < len(states); i++ {
		if states[i].SequenceID > 0 && states[i-1].SequenceID > 0 {
			if states[i].SequenceID <= states[i-1].SequenceID {
				return i
			}
		}
	}
	return -1
}

// ValidateParentRelationships checks that parent-child relationships are valid:
// - Parent sequence IDs must be less than child sequence IDs
// - No circular references
// Returns the index of the first violation, or -1 if relationships are valid.
func (o *EventOrdering) ValidateParentRelationships(states []ExecutionState) int {
	for i := range states {
		if states[i].ParentSequenceID > 0 {
			// Parent must have a smaller sequence ID
			if states[i].ParentSequenceID >= states[i].SequenceID {
				return i
			}
			// Parent must exist in the trace
			parentFound := false
			for j := range states {
				if states[j].SequenceID == states[i].ParentSequenceID {
					parentFound = true
					break
				}
			}
			if !parentFound {
				return i
			}
		}
	}
	return -1
}

// ReindexSequenceIDs reassigns sequence IDs based on current order.
// Used when importing traces without sequence IDs or after filtering.
func (o *EventOrdering) ReindexSequenceIDs(states []ExecutionState) {
	for i := range states {
		states[i].SequenceID = uint64(i + 1)
	}
}

// PreserveParentRelationships preserves parent-child relationships during reindexing.
// Maps old sequence IDs to new sequence IDs.
func (o *EventOrdering) PreserveParentRelationships(states []ExecutionState) {
	oldToNew := make(map[uint64]uint64, len(states))
	for i := range states {
		oldToNew[states[i].SequenceID] = uint64(i + 1)
	}
	for i := range states {
		if states[i].ParentSequenceID > 0 {
			if newID, ok := oldToNew[states[i].ParentSequenceID]; ok {
				states[i].ParentSequenceID = newID
			}
		}
	}
}
