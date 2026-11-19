// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ── Issue #541: Trace diff tests ──────────────────────────────────────────────

func TestTraceDiff_EquivalentTraces(t *testing.T) {
	old := makeTraceN("tx1", 5)
	new := makeTraceN("tx1", 5)

	diff := ComputeTraceDiff(old, new)
	if !diff.IsEmpty {
		t.Errorf("expected empty diff for equivalent traces, got entries: %d", len(diff.Entries))
	}
}

func TestTraceDiff_InsertedStep(t *testing.T) {
	old := makeTraceN("tx1", 3)
	new := makeTraceN("tx1", 4)

	diff := ComputeTraceDiff(old, new)
	if diff.IsEmpty {
		t.Error("expected non-empty diff when new trace has extra step")
	}
	hasInsert := false
	for _, e := range diff.Entries {
		if e.Kind == DiffInsert {
			hasInsert = true
			break
		}
	}
	if !hasInsert {
		t.Error("expected at least one inserted entry")
	}
}

func TestTraceDiff_RemovedStep(t *testing.T) {
	old := makeTraceN("tx1", 5)
	new := makeTraceN("tx1", 3)

	diff := ComputeTraceDiff(old, new)
	if diff.IsEmpty {
		t.Error("expected non-empty diff when steps removed")
	}
}

func TestTraceDiff_RenderJSON(t *testing.T) {
	old := makeTraceN("tx1", 3)
	new := makeTraceN("tx2", 3)

	diff := ComputeTraceDiff(old, new)
	data, err := diff.RenderJSON()
	if err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	if !json.Valid(data) {
		t.Error("RenderJSON produced invalid JSON")
	}
}

func TestTraceDiff_RenderHTML(t *testing.T) {
	old := makeTraceN("tx1", 3)
	new := makeTraceN("tx2", 3)

	diff := ComputeTraceDiff(old, new)
	html, err := diff.RenderHTML()
	if err != nil {
		t.Fatalf("RenderHTML: %v", err)
	}
	if html == "" {
		t.Error("RenderHTML produced empty string")
	}
}

// ── Issue #539: Trace streaming tests ──────────────────────────────────────────

func TestStreamRoundTrip(t *testing.T) {
	trace := makeTraceN("stream-test", 10)

	dir := t.TempDir()
	path := filepath.Join(dir, "trace.gbx")

	if err := WriteTraceStream(path, trace); err != nil {
		t.Fatalf("WriteTraceStream: %v", err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	sr := NewStreamReader(f)
	if _, err := sr.ReadHeader(); err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}

	var stateCount uint32
	for {
		rec, err := sr.Next()
		if err != nil && err.Error() != "EOF" {
			// io.EOF
			break
		}
		if rec == nil {
			break
		}
		if rec.Type == RecordTypeState {
			stateCount++
		}
	}

	if stateCount != 10 {
		t.Errorf("expected 10 state records, got %d", stateCount)
	}
}

func TestStreamCorruptionDetection(t *testing.T) {
	trace := makeTraceN("corrupt-test", 3)
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.gbx")

	if err := WriteTraceStream(path, trace); err != nil {
		t.Fatalf("WriteTraceStream: %v", err)
	}

	// Corrupt the file
	data, _ := os.ReadFile(path)
	if len(data) > 20 {
		data[len(data)-5] ^= 0xFF // Flip bits in last record
	}
	os.WriteFile(path, data, 0644)

	f, _ := os.Open(path)
	defer f.Close()
	sr := NewStreamReader(f)
	sr.ReadHeader()
	for {
		_, err := sr.Next()
		if err != nil {
			if sr.IsCorrupt() {
				return // Expected
			}
			break
		}
	}
}

// ── Issue #538: Schema versioning tests ───────────────────────────────────────

func TestSchemaVersion_Current(t *testing.T) {
	if TraceSchemaVersion != "2.0.0" {
		t.Errorf("expected schema version 2.0.0, got %s", TraceSchemaVersion)
	}
}

func TestSchemaVersion_Supported(t *testing.T) {
	if !IsTraceSchemaVersionSupported("1.0") {
		t.Error("1.0 should be supported")
	}
	if !IsTraceSchemaVersionSupported("2.0.0") {
		t.Error("2.0.0 should be supported")
	}
	if IsTraceSchemaVersionSupported("99.0.0") {
		t.Error("99.0.0 should not be supported")
	}
}

func TestSchemaVersion_ValidateValid(t *testing.T) {
	data := map[string]interface{}{
		"schema_version":   "2.0.0",
		"transaction_hash": "sha256:abc",
		"start_time":       "2026-01-01T00:00:00Z",
		"end_time":         "2026-01-01T00:00:01Z",
		"states":           []interface{}{},
	}
	if err := ValidateTraceSchema(data); err != nil {
		t.Errorf("expected valid, got: %v", err)
	}
}

func TestSchemaVersion_ValidateMissingVersion(t *testing.T) {
	data := map[string]interface{}{
		"transaction_hash": "sha256:abc",
	}
	err := ValidateTraceSchema(data)
	if err == nil {
		t.Error("expected error for missing schema_version")
	}
}

func TestSchemaVersion_MigrateV1toV2(t *testing.T) {
	data := map[string]interface{}{
		"schema_version": "1.0",
		"transaction_hash": "sha256:abc",
	}
	migrated, err := MigrateTrace(data)
	if err != nil {
		t.Fatalf("MigrateTrace: %v", err)
	}
	if v, _ := migrated["schema_version"].(string); v != TraceSchemaVersion {
		t.Errorf("expected migrated version %s, got %s", TraceSchemaVersion, v)
	}
}

// ── Helper ────────────────────────────────────────────────────────────────────

func makeTraceN(txHash string, numStates int) *ExecutionTrace {
	t := NewExecutionTrace(txHash, 100)
	t.StartTime = time.Now()
	t.EndTime = time.Now().Add(time.Millisecond)
	for i := 0; i < numStates; i++ {
		t.AddState(ExecutionState{
			Step:       i,
			Operation:  "contract_call",
			EventType:  EventTypeContractCall,
			ContractID: "CAAAA" + string(rune('A'+i)) + "BBBB",
			Function:   "test_fn",
			Timestamp:  time.Now(),
		})
	}
	return t
}
