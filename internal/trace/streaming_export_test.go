// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func makeTestTrace(stepCount int) *ExecutionTrace {
	states := make([]ExecutionState, stepCount)
	for i := 0; i < stepCount; i++ {
		states[i] = ExecutionState{
			Step:      i,
			Timestamp: time.Now().Add(time.Duration(i) * time.Millisecond),
			Operation: "op_" + strings.Repeat("x", i%10),
			EventType: "contract_call",
		}
	}
	return &ExecutionTrace{
		TransactionHash:  "test-tx-hash",
		StartTime:        time.Now(),
		EndTime:          time.Now().Add(time.Duration(stepCount) * time.Millisecond),
		States:           states,
		SnapshotInterval: 100,
	}
}

func TestStreamingExporterBasic(t *testing.T) {
	trace := makeTestTrace(10)
	var buf bytes.Buffer

	se := NewStreamingExporter(&buf)
	if err := se.WriteHeader(trace, StreamingSchemaVersion, time.Now().Truncate(time.Second)); err != nil {
		t.Fatal(err)
	}
	for i := range trace.States {
		if err := se.WriteState(&trace.States[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err := se.Close(); err != nil {
		t.Fatal(err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	schemaVersion, ok := result["schema_version"].(string)
	if !ok || schemaVersion != StreamingSchemaVersion {
		t.Errorf("schema_version: got %v, want %q", result["schema_version"], StreamingSchemaVersion)
	}

	traceObj, ok := result["trace"].(map[string]interface{})
	if !ok {
		t.Fatal("missing trace object")
	}

	states, ok := traceObj["states"].([]interface{})
	if !ok {
		t.Fatal("missing states array")
	}
	if len(states) != 10 {
		t.Errorf("states length: got %d, want 10", len(states))
	}
}

func TestStreamingExporterCommaSeparation(t *testing.T) {
	trace := makeTestTrace(3)
	var buf bytes.Buffer

	se := NewStreamingExporter(&buf)
	_ = se.WriteHeader(trace, StreamingSchemaVersion, time.Now().Truncate(time.Second))
	_ = se.WriteState(&trace.States[0])
	_ = se.WriteState(&trace.States[1])
	_ = se.WriteState(&trace.States[2])
	_ = se.Close()

	out := buf.String()
	// States should be comma-separated.
	if !strings.Contains(out, "},\n") {
		t.Error("states not comma-separated")
	}
}

func TestStreamingExporterEmptyTrace(t *testing.T) {
	trace := makeTestTrace(0)
	var buf bytes.Buffer

	se := NewStreamingExporter(&buf)
	if err := se.WriteHeader(trace, StreamingSchemaVersion, time.Now().Truncate(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := se.Close(); err != nil {
		t.Fatal(err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	traceObj := result["trace"].(map[string]interface{})
	states := traceObj["states"].([]interface{})
	if len(states) != 0 {
		t.Errorf("expected empty states, got %d", len(states))
	}
}

func TestStreamingExportToFile(t *testing.T) {
	trace := makeTestTrace(50)
	tmpDir := t.TempDir()
	destPath := tmpDir + "/trace.json"

	opts := &StreamingExportOptions{
		BufferSize: 10,
	}
	if err := ExportTraceStreaming(context.Background(), trace, destPath, opts); err != nil {
		t.Fatal(err)
	}

	b, err := readFileBytes(destPath)
	if err != nil {
		t.Fatal(err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	traceObj := result["trace"].(map[string]interface{})
	states := traceObj["states"].([]interface{})
	if len(states) != 50 {
		t.Errorf("states count: got %d, want 50", len(states))
	}
}

func TestStreamingExportProgress(t *testing.T) {
	trace := makeTestTrace(100)
	tmpDir := t.TempDir()
	destPath := tmpDir + "/trace.json"

	var progressCalls []StreamingExportProgress
	opts := &StreamingExportOptions{
		BufferSize: 25,
		Progress: func(ctx context.Context, p StreamingExportProgress) error {
			progressCalls = append(progressCalls, p)
			return nil
		},
	}
	if err := ExportTraceStreaming(context.Background(), trace, destPath, opts); err != nil {
		t.Fatal(err)
	}

	// 100 states / 25 buffer = 4 flushes + 1 final = 5 progress calls
	if len(progressCalls) < 4 {
		t.Errorf("expected at least 4 progress calls, got %d", len(progressCalls))
	}

	last := progressCalls[len(progressCalls)-1]
	if last.StatesWritten != 100 {
		t.Errorf("final StatesWritten: got %d, want 100", last.StatesWritten)
	}
}

func TestStreamingExportCancellation(t *testing.T) {
	trace := makeTestTrace(100)
	tmpDir := t.TempDir()
	destPath := tmpDir + "/trace.json"

	cancelled := false
	opts := &StreamingExportOptions{
		BufferSize: 10,
		Progress: func(ctx context.Context, p StreamingExportProgress) error {
			if p.StatesWritten >= 30 {
				cancelled = true
				return context.Canceled
			}
			return nil
		},
	}
	err := ExportTraceStreaming(context.Background(), trace, destPath, opts)
	if err == nil {
		t.Error("expected error from cancelled export")
	}
	if !cancelled {
		t.Error("expected cancellation flag to be set")
	}

	// Verify no partial file at destPath.
	if _, err := readFileBytes(destPath); err == nil {
		t.Error("partial file should not exist after cancellation")
	}
}

func TestStreamingExportNilTrace(t *testing.T) {
	err := ExportTraceStreaming(context.Background(), nil, "trace.json", nil)
	if err == nil {
		t.Error("expected error for nil trace")
	}
}

func TestStreamingExporterDoubleHeader(t *testing.T) {
	trace := makeTestTrace(1)
	var buf bytes.Buffer
	se := NewStreamingExporter(&buf)
	_ = se.WriteHeader(trace, StreamingSchemaVersion, time.Now().Truncate(time.Second))
	err := se.WriteHeader(trace, StreamingSchemaVersion, time.Now().Truncate(time.Second))
	if err == nil {
		t.Error("expected error for double header")
	}
}

func TestStreamingExportDeterminism(t *testing.T) {
	trace := makeTestTrace(20)
	fixedTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	var buf1, buf2 bytes.Buffer
	for _, buf := range []*bytes.Buffer{&buf1, &buf2} {
		se := NewStreamingExporter(buf)
		_ = se.WriteHeader(trace, StreamingSchemaVersion, fixedTime)
		for i := range trace.States {
			_ = se.WriteState(&trace.States[i])
		}
		_ = se.Close()
	}

	if buf1.String() != buf2.String() {
		t.Error("streaming export is not deterministic")
	}
}
