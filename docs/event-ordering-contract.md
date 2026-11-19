# Event Ordering Contract

## Overview

This document defines the ordering contract for trace events, ensuring deterministic behavior across collection, serialization, filtering, and merging operations. The contract guarantees that the same replay produces identical event order across runs and platforms.

## Sequence IDs

### Definition

Every `ExecutionState`, `DiagnosticEvent`, and `ContractEvent` has a `SequenceID` field:
- Type: `uint64`
- Monotonically increasing from 1
- Assigned at collection time by `EventSequencer`
- Preserved through all transformations

### Assignment Rules

1. **Automatic Assignment**: When adding states via `AddState()`, sequence IDs are automatically assigned if zero
2. **Manual Assignment**: Pre-existing sequence IDs are preserved (for imported traces)
3. **Concurrent Safety**: `EventSequencer` uses mutex protection for thread-safe ID generation

### Properties

- **Monotonic**: Sequence IDs strictly increase within a trace
- **Unique**: No two events in the same trace share a sequence ID
- **Immutable**: Once assigned, sequence IDs never change during normal operations

## Parent Relationships

### Definition

Events have a `ParentSequenceID` field to track nested call relationships:
- Type: `uint64`
- Zero indicates no parent (top-level event)
- References the sequence ID of the parent event

### Assignment Rules

1. **Automatic Tracking**: `EventSequencer` maintains a parent stack
2. **Push/Pop**: Use `PushParent()` and `PopParent()` to enter/exit nested contexts
3. **Default**: If zero, the event is considered top-level

### Validation Rules

- Parent sequence ID must be less than child sequence ID
- Parent must exist in the trace
- No circular references allowed

## Deterministic Tie-Breakers

When sequence IDs are equal (should not happen in normal operation), tie-breaking uses:

### For ExecutionState
1. Primary: Sequence ID
2. Secondary: Step index

### For DiagnosticEvent
1. Primary: Sequence ID
2. Secondary: Deterministic string key (contract ID + event type + data + WASM instruction)

### For ContractEvent
1. Primary: Sequence ID
2. Secondary: Deterministic string key (contract ID + type + topics + data)

## Collection Boundaries

### At Collection Time

```go
trace := NewExecutionTrace(txHash, snapshotInterval)
trace.AddState(state) // Sequence ID assigned automatically
```

### For Nested Calls

```go
parentID := trace.States[0].SequenceID
trace.sequencer.PushParent(parentID)
trace.AddState(nestedState) // ParentSequenceID set automatically
trace.sequencer.PopParent()
```

### For Diagnostic Events

```go
trace.sequencer.AssignSequenceIDsWithParent(events)
```

## Transformation Guarantees

### Filtering

- **Preserves**: Sequence IDs and parent relationships
- **Maintains**: Relative order of matched events
- **No Reindexing**: Original sequence IDs remain unchanged

### Serialization

- **Preserves**: All sequence ID fields in JSON
- **Backward Compatible**: Traces without sequence IDs can be reindexed
- **Round-Trip**: Serialize → Deserialize preserves exact sequence IDs

### Merging

- **Reindexing**: Required after merging traces
- **Parent Mapping**: Old parent IDs mapped to new IDs via `PreserveParentRelationships()`
- **Validation**: Sequence order validated after merge

### Diff Operations

- **Alignment**: Uses sequence IDs for deterministic step alignment
- **Fallback**: Uses contract ID + function + operation for backward compatibility
- **Stability**: Same traces produce identical diffs across runs

## Validation

### Sequence Order Validation

```go
ordering := &EventOrdering{}
if idx := ordering.ValidateSequenceOrder(states); idx != -1 {
    // Violation at index idx
}
```

### Parent Relationship Validation

```go
if idx := ordering.ValidateParentRelationships(states); idx != -1 {
    // Invalid parent relationship at index idx
}
```

## Backward Compatibility

### Importing Old Traces

Traces without sequence IDs can be reindexed:

```go
ordering := &EventOrdering{}
ordering.ReindexSequenceIDs(states)
ordering.PreserveParentRelationships(states)
```

### Exporting to Old Consumers

Sequence ID fields are optional (`omitempty`), so old consumers can ignore them.

## Performance Considerations

### Overhead

- **Sequence ID Assignment**: O(1) per state (atomic increment)
- **Parent Stack**: O(1) push/pop operations
- **Sorting**: O(n log n) only when explicitly sorting
- **Validation**: O(n) for sequence order, O(n²) for parent relationships

### Memory

- **Per Event**: 16 bytes for SequenceID + 16 bytes for ParentSequenceID
- **Sequencer**: Minimal stack allocation (typically < 1KB)

## Testing

### Unit Tests

- `TestEventSequencer_MonotonicSequenceIDs`
- `TestEventSequencer_ParentStack`
- `TestEventSequencer_ConcurrentUse`
- `TestEventOrdering_SortStatesBySequenceID`
- `TestEventOrdering_ValidateSequenceOrder`
- `TestEventOrdering_ValidateParentRelationships`

### Integration Tests

- `TestFilteringPreservesSequenceOrder`
- `TestSerializationPreservesSequenceOrder`
- `TestDiagnosticEventOrderingPreserved`
- `TestContractEventOrderingPreserved`
- `TestMergingPreservesSequenceOrder`
- `TestDiffWithSequenceIDs`
- `TestBackwardCompatibility`
- `TestConcurrentEventCollection`

### Fixture Tests

- `TestNestedAndSimultaneousEvents` - Tests nested call relationships
- Validates parent-child relationships after export and filtering

## Acceptance Criteria

✅ **Same replay produces identical event order across runs and platforms**
- Sequence IDs assigned deterministically
- Tie-breakers use stable string comparisons
- No reliance on map iteration order

✅ **Parent-child relationships remain valid after export and filtering**
- Parent sequence IDs preserved through serialization
- Parent context maintained in filtered traces
- Validation catches invalid relationships

✅ **Comparison reports identify true changes rather than ordering noise**
- Diff uses sequence IDs for alignment
- Backward compatible with traces lacking sequence IDs
- Stable diff output across platforms

## Implementation Files

- `internal/trace/event_sequencer.go` - Sequence ID assignment
- `internal/trace/event_ordering.go` - Sorting and validation
- `internal/trace/navigation.go` - ExecutionState with sequence IDs
- `internal/trace/parser.go` - DiagnosticEvent with sequence IDs
- `internal/trace/event_decoder.go` - ContractEvent with sequence IDs
- `internal/trace/event_ordering_test.go` - Unit tests
- `internal/trace/event_ordering_integration_test.go` - Integration tests

## Migration Guide

### For Trace Producers

1. Initialize `EventSequencer` when creating traces
2. Use `PushParent()`/`PopParent()` for nested calls
3. Assign sequence IDs to diagnostic events

### For Trace Consumers

1. No changes required for basic consumption
2. Use sequence IDs for stable sorting if needed
3. Validate sequence order for integrity checks

### For Existing Traces

1. Reindex traces without sequence IDs
2. Validate parent relationships after reindexing
3. Export with sequence IDs for future compatibility
