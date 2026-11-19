// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ContractEvent holds a decoded Soroban contract event and its correlation to
// the call stack at the point it was emitted.
type ContractEvent struct {
	// ContractID is the emitting contract (empty for system events).
	ContractID string `json:"contract_id,omitempty"`
	// Topics contains the decoded event topics as human-readable strings.
	Topics []string `json:"topics"`
	// Data is the decoded event body payload.
	Data string `json:"data"`
	// Type distinguishes "contract", "system", and "diagnostic" events.
	Type string `json:"type"`
	// TraceStep is the index into ExecutionTrace.States where this event
	// was correlated (−1 when correlation is unavailable).
	TraceStep int `json:"trace_step"`
	// CallStack captures the function call path at the point of emission.
	CallStack []string `json:"call_stack,omitempty"`
	// Name is the schema-defined event name, when a supplied schema matches.
	Name string `json:"name,omitempty"`
	// Decoded contains schema-named event fields for audit and trace output.
	Decoded map[string]interface{} `json:"decoded,omitempty"`
	// SequenceID is a monotonically increasing identifier assigned at collection time
	// to preserve event order across serialization, filtering, and merging operations.
	SequenceID uint64 `json:"sequence_id,omitempty"`
	// ParentSequenceID tracks the parent event for nested call relationships.
	// Zero indicates no parent (top-level event).
	ParentSequenceID uint64 `json:"parent_sequence_id,omitempty"`
}

// rawEventEnvelope is used to attempt JSON parsing of an event string.
type rawEventEnvelope struct {
	ContractID string   `json:"contract_id"`
	Topics     []string `json:"topics"`
	Data       interface{} `json:"data"`
	Type       string   `json:"type"`
}

// EventFieldSchema describes a single named field in a contract event payload.
type EventFieldSchema struct {
	Name    string `json:"name"`
	Type    string `json:"type,omitempty"`
	Indexed bool   `json:"indexed,omitempty"`
}

// EventSchema describes one contract event. JSON schema files may provide this
// shape directly, or ABI-like entries with "type":"event" and "inputs".
type EventSchema struct {
	Name     string             `json:"name"`
	Topic    string             `json:"topic,omitempty"`
	Fields   []EventFieldSchema `json:"fields,omitempty"`
	Inputs   []EventFieldSchema `json:"inputs,omitempty"`
	Contract string             `json:"contract_id,omitempty"`
}

// EventSchemaSet holds event definitions keyed by name/topic.
type EventSchemaSet struct {
	Events []EventSchema `json:"events"`
}

type rawABISchema struct {
	Type   string             `json:"type"`
	Name   string             `json:"name"`
	Inputs []EventFieldSchema `json:"inputs"`
}

// LoadEventSchemas reads JSON event schema definitions from one or more files.
// Supported inputs:
//   - {"events":[{"name":"transfer","fields":[...]}]}
//   - [{"type":"event","name":"transfer","inputs":[...]}]
//   - [{"name":"transfer","fields":[...]}]
func LoadEventSchemas(paths ...string) (*EventSchemaSet, error) {
	set := &EventSchemaSet{}
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read event schema %s: %w", path, err)
		}
		parsed, err := ParseEventSchemas(data)
		if err != nil {
			return nil, fmt.Errorf("parse event schema %s: %w", path, err)
		}
		set.Events = append(set.Events, parsed.Events...)
	}
	return set, nil
}

// ParseEventSchemas parses JSON or ABI-like event definitions.
func ParseEventSchemas(data []byte) (*EventSchemaSet, error) {
	var wrapped EventSchemaSet
	if err := json.Unmarshal(data, &wrapped); err == nil && len(wrapped.Events) > 0 {
		normalizeSchemas(wrapped.Events)
		return &wrapped, nil
	}

	var abiEntries []rawABISchema
	if err := json.Unmarshal(data, &abiEntries); err == nil {
		set := &EventSchemaSet{}
		for _, entry := range abiEntries {
			if entry.Type != "" && entry.Type != "event" {
				continue
			}
			if entry.Name == "" {
				continue
			}
			set.Events = append(set.Events, EventSchema{
				Name:   entry.Name,
				Topic:  entry.Name,
				Fields: entry.Inputs,
			})
		}
		if len(set.Events) > 0 {
			return set, nil
		}
	}

	var direct []EventSchema
	if err := json.Unmarshal(data, &direct); err != nil {
		return nil, err
	}
	normalizeSchemas(direct)
	return &EventSchemaSet{Events: direct}, nil
}

func normalizeSchemas(events []EventSchema) {
	for i := range events {
		if len(events[i].Fields) == 0 && len(events[i].Inputs) > 0 {
			events[i].Fields = events[i].Inputs
		}
		if events[i].Topic == "" {
			events[i].Topic = events[i].Name
		}
	}
}

// DecodeContractEvent parses a single raw event string (JSON or structured
// text) into a ContractEvent. It never returns an error; unparseable input is
// preserved verbatim in the Data field.
func DecodeContractEvent(raw string) *ContractEvent {
	raw = strings.TrimSpace(raw)
	ev := &ContractEvent{TraceStep: -1, Type: "contract"}

	// Try JSON first (the canonical Soroban diagnostic event format).
	var envelope rawEventEnvelope
	if err := json.Unmarshal([]byte(raw), &envelope); err == nil {
		ev.ContractID = envelope.ContractID
		ev.Topics = envelope.Topics
		if envelope.Type != "" {
			ev.Type = envelope.Type
		}
		if envelope.Data != nil {
			switch v := envelope.Data.(type) {
			case string:
				ev.Data = v
			default:
				b, _ := json.Marshal(v)
				ev.Data = string(b)
			}
		}
		return ev
	}

	// Fallback: parse the key:value text format used by the Soroban simulator.
	ev.Data = raw
	for _, field := range strings.Split(raw, " ") {
		kv := strings.SplitN(field, ":", 2)
		if len(kv) != 2 {
			continue
		}
		key, val := strings.TrimSpace(kv[0]), strings.TrimSpace(kv[1])
		switch key {
		case "contract":
			ev.ContractID = val
		case "topic":
			ev.Topics = append(ev.Topics, val)
		case "type":
			ev.Type = val
		case "data":
			ev.Data = val
		}
	}
	return ev
}

// DecodeContractEventWithSchemas decodes an event and applies the first
// matching schema. A schema matches on contract_id when present and on either
// event name/topic in the event topics.
func DecodeContractEventWithSchemas(raw string, schemas *EventSchemaSet) *ContractEvent {
	ev := DecodeContractEvent(raw)
	ApplyEventSchemas(ev, schemas)
	return ev
}

// DecodeContractEvents decodes all raw event strings in the slice.
func DecodeContractEvents(raws []string) []*ContractEvent {
	out := make([]*ContractEvent, 0, len(raws))
	for _, r := range raws {
		out = append(out, DecodeContractEvent(r))
	}
	return out
}

// DecodeContractEventsWithSchemas decodes all raw events and applies schemas.
func DecodeContractEventsWithSchemas(raws []string, schemas *EventSchemaSet) []*ContractEvent {
	out := make([]*ContractEvent, 0, len(raws))
	for _, r := range raws {
		out = append(out, DecodeContractEventWithSchemas(r, schemas))
	}
	return out
}

// DecodeDiagnosticEventsWithSchemas converts structured diagnostic events to
// decoded contract events and applies schemas. Preserves sequence IDs and parent
// relationships from the source events for deterministic ordering.
func DecodeDiagnosticEventsWithSchemas(events []DiagnosticEvent, schemas *EventSchemaSet) []*ContractEvent {
	out := make([]*ContractEvent, 0, len(events))
	for _, de := range events {
		ev := &ContractEvent{
			Topics:           de.Topics,
			Data:             de.Data,
			Type:             de.EventType,
			TraceStep:        -1,
			SequenceID:       de.SequenceID,
			ParentSequenceID: de.ParentSequenceID,
		}
		if de.ContractID != nil {
			ev.ContractID = *de.ContractID
		}
		ApplyEventSchemas(ev, schemas)
		out = append(out, ev)
	}
	return out
}

// ApplyEventSchemas enriches a decoded event in place.
func ApplyEventSchemas(ev *ContractEvent, schemas *EventSchemaSet) {
	if ev == nil || schemas == nil {
		return
	}
	schema := schemas.match(ev)
	if schema == nil {
		return
	}
	ev.Name = schema.Name
	fields := schema.Fields
	if len(fields) == 0 {
		fields = schema.Inputs
	}
	if len(fields) == 0 {
		return
	}
	values := eventDataValues(ev.Data)
	decoded := make(map[string]interface{}, len(fields))
	for i, field := range fields {
		if field.Name == "" {
			continue
		}
		if i < len(values) {
			decoded[field.Name] = values[i]
		}
	}
	if len(decoded) > 0 {
		ev.Decoded = decoded
	}
}

func (s *EventSchemaSet) match(ev *ContractEvent) *EventSchema {
	if s == nil || ev == nil {
		return nil
	}
	for i := range s.Events {
		schema := &s.Events[i]
		if schema.Contract != "" && schema.Contract != ev.ContractID {
			continue
		}
		topic := schema.Topic
		if topic == "" {
			topic = schema.Name
		}
		for _, evTopic := range ev.Topics {
			if strings.EqualFold(evTopic, topic) || strings.EqualFold(evTopic, schema.Name) {
				return schema
			}
		}
	}
	return nil
}

func eventDataValues(data string) []interface{} {
	data = strings.TrimSpace(data)
	if data == "" {
		return nil
	}

	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(data), &obj); err == nil {
		if vals, ok := obj["values"].([]interface{}); ok {
			return vals
		}
		out := make([]interface{}, 0, len(obj))
		for _, v := range obj {
			out = append(out, v)
		}
		return out
	}

	var arr []interface{}
	if err := json.Unmarshal([]byte(data), &arr); err == nil {
		return arr
	}

	parts := strings.Split(data, ",")
	out := make([]interface{}, 0, len(parts))
	for _, part := range parts {
		out = append(out, strings.TrimSpace(part))
	}
	return out
}

// CorrelateEvents matches decoded events to trace steps by contract ID and
// populates each event's TraceStep and CallStack fields. It modifies events
// in place and returns the same slice for convenience.
//
// Matching strategy: iterate states in order; for each event, find the
// nearest state whose ContractID matches (or the first state if no match
// exists). This is a best-effort heuristic because Soroban event ordering
// follows ledger close, not per-call ordering.
func CorrelateEvents(events []*ContractEvent, trace *ExecutionTrace) []*ContractEvent {
	if trace == nil || len(events) == 0 {
		return events
	}

	// Build a quick index: contractID → list of step indices.
	byContract := make(map[string][]int, len(trace.States))
	for i, s := range trace.States {
		if s.ContractID != "" {
			byContract[s.ContractID] = append(byContract[s.ContractID], i)
		}
	}

	// Assign each event to its best matching step.
	// We advance a cursor per contract so each event gets a unique step.
	cursor := make(map[string]int)
	for _, ev := range events {
		steps, ok := byContract[ev.ContractID]
		if !ok || len(steps) == 0 {
			// No matching contract – correlate to the most recent state.
			if len(trace.States) > 0 {
				ev.TraceStep = len(trace.States) - 1
			}
			continue
		}
		idx := cursor[ev.ContractID]
		if idx >= len(steps) {
			idx = len(steps) - 1
		}
		ev.TraceStep = steps[idx]
		cursor[ev.ContractID] = idx + 1

		// Capture a synthetic call stack from ancestor nodes.
		ev.CallStack = buildCallStack(trace, ev.TraceStep)
	}
	return events
}

// buildCallStack reconstructs a call path up to the given step by collecting
// the function names from states at the same or lower depth.
func buildCallStack(trace *ExecutionTrace, step int) []string {
	if step < 0 || step >= len(trace.States) {
		return nil
	}
	var stack []string
	for i := 0; i <= step; i++ {
		s := trace.States[i]
		if s.Function != "" {
			entry := s.Function
			if s.ContractID != "" {
				entry = fmt.Sprintf("%s::%s", s.ContractID[:min(8, len(s.ContractID))], s.Function)
			}
			stack = append(stack, entry)
		}
	}
	// Keep only the last 8 frames to avoid noise.
	if len(stack) > 8 {
		stack = stack[len(stack)-8:]
	}
	return stack
}

// AttachEventsToNode walks the trace node tree and attaches decoded events to
// the TraceNode whose ContractID matches the event. This enriches the
// interactive viewer with inline event data.
func AttachEventsToNode(root *TraceNode, events []*ContractEvent) {
	if root == nil || len(events) == 0 {
		return
	}
	// Build a map: contractID → events to add.
	byContract := make(map[string][]*ContractEvent, len(events))
	for _, ev := range events {
		byContract[ev.ContractID] = append(byContract[ev.ContractID], ev)
	}
	attachRecursive(root, byContract)
}

func attachRecursive(node *TraceNode, byContract map[string][]*ContractEvent) {
	if node == nil {
		return
	}
	if evs, ok := byContract[node.ContractID]; ok {
		for _, ev := range evs {
			child := NewTraceNode(
				fmt.Sprintf("evt-%s-%d", node.ID, ev.TraceStep),
				"contract_event",
			)
			child.ContractID = ev.ContractID
			child.EventData = formatEventSummary(ev)
			node.AddChild(child)
		}
		// Prevent duplicate attachment on subsequent calls.
		delete(byContract, node.ContractID)
	}
	for _, c := range node.Children {
		attachRecursive(c, byContract)
	}
}

func formatEventSummary(ev *ContractEvent) string {
	if ev.Name != "" && len(ev.Decoded) > 0 {
		b, _ := json.Marshal(ev.Decoded)
		return fmt.Sprintf("[%s] %s %s", ev.Type, ev.Name, string(b))
	}
	if len(ev.Topics) > 0 {
		return fmt.Sprintf("[%s] topics=%s data=%s", ev.Type, strings.Join(ev.Topics, ","), ev.Data)
	}
	return fmt.Sprintf("[%s] %s", ev.Type, ev.Data)
}
