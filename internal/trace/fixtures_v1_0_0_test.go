// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

// Schema version 1.0.0 canonical fixtures for testing migration behavior.
// These fixtures represent traces exported with schema version 1.0.0
// and are used to verify that migration to 2.0.0 works correctly.

// canonicalV1Trace is a trace exported with schema version 1.0.0.
// This fixture represents the canonical shape of a 1.0.0 trace.
const canonicalV1Trace = `{
  "schema_version": "1.0.0",
  "transaction_hash": "sha256:5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab",
  "start_time": "2026-01-01T00:00:00Z",
  "end_time": "2026-01-01T00:00:05Z",
  "states": [
    {
      "step": 0,
      "operation": "contract_call",
      "event_type": "contract_call",
      "contract_id": "CTESTAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
      "function": "transfer",
      "arguments": ["GALICE", "GBOB", 100],
      "return_value": "ok",
      "timestamp": "2026-01-01T00:00:01Z"
    },
    {
      "step": 1,
      "operation": "host_function",
      "event_type": "host_function",
      "function": "put_ledger_entry",
      "return_value": true,
      "timestamp": "2026-01-01T00:00:02Z"
    }
  ],
  "snapshots": [],
  "diagnostic_events": null,
  "decoded_events": null,
  "annotations": {
    "comments": ["Reviewed by alice"],
    "session_metadata": {
      "network": "testnet"
    }
  },
  "current_step": 0,
  "snapshot_interval": 100
}`

// canonicalV1TraceWithoutVersion is a trace from before schema versioning was added.
// This represents the "legacy" format that should be handled gracefully.
const canonicalV1TraceWithoutVersion = `{
  "transaction_hash": "5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab",
  "start_time": "2026-01-01T00:00:00Z",
  "end_time": "2026-01-01T00:00:05Z",
  "states": [
    {
      "step": 0,
      "operation": "contract_call",
      "timestamp": "2026-01-01T00:00:01Z"
    }
  ]
}`

// TestLoadV1_0_0Trace tests loading a trace with schema version 1.0.0.
func TestLoadV1_0_0Trace(t *testing.T) {
	var trace ExecutionTrace
	if err := json.Unmarshal([]byte(canonicalV1Trace), &trace); err != nil {
		t.Fatalf("failed to unmarshal V1.0.0 trace: %v", err)
	}

	// Verify basic structure
	if trace.TransactionHash == "" {
		t.Error("transaction_hash should not be empty")
	}
	if len(trace.States) != 2 {
		t.Errorf("expected 2 states, got %d", len(trace.States))
	}
	if trace.SnapshotInterval != 100 {
		t.Errorf("expected snapshot_interval 100, got %d", trace.SnapshotInterval)
	}
}

// TestMigrateV1toV2 tests the migration from schema 1.0.0 to 2.0.0.
func TestMigrateV1toV2(t *testing.T) {
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(canonicalV1Trace), &data); err != nil {
		t.Fatalf("failed to unmarshal V1.0.0 trace: %v", err)
	}

	// Apply migration
	migrated, err := migrateV1toV2(data)
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	// Verify schema version updated
	version, ok := migrated["schema_version"].(string)
	if !ok {
		t.Error("schema_version should be a string")
	}
	if version != TraceSchemaVersion {
		t.Errorf("expected schema_version %s, got %s", TraceSchemaVersion, version)
	}

	// Verify trace object preserved
	traceObj, ok := migrated["trace"].(map[string]interface{})
	if !ok {
		t.Error("trace object should be preserved")
	}

	// Verify snapshot_interval is present (added during migration)
	snapshotInterval, ok := traceObj["snapshot_interval"]
	if !ok {
		t.Error("snapshot_interval should be added during migration")
	}
	if snapshotInterval != 100 {
		t.Errorf("expected snapshot_interval 100, got %v", snapshotInterval)
	}
}

// TestLoadLegacyFormat tests loading a trace without schema version.
func TestLoadLegacyFormat(t *testing.T) {
	tmpDir := t.TempDir()
	path := tmpDir + "/legacy.json"
	
	if err := os.WriteFile(path, []byte(canonicalV1TraceWithoutVersion), 0o644); err != nil {
		t.Fatalf("failed to write legacy trace: %v", err)
	}

	// Should load with a deprecation warning
	loaded, err := LoadVersionedTrace(path, DefaultCompatibilityOptions())
	if err != nil {
		t.Fatalf("failed to load legacy trace: %v", err)
	}

	if loaded == nil {
		t.Fatal("loaded trace should not be nil")
	}
	if len(loaded.States) != 1 {
		t.Errorf("expected 1 state, got %d", len(loaded.States))
	}
}

// TestV1_0_0RoundTrip tests that a V1.0.0 trace can be exported and reloaded.
func TestV1_0_0RoundTrip(t *testing.T) {
	var original ExecutionTrace
	if err := json.Unmarshal([]byte(canonicalV1Trace), &original); err != nil {
		t.Fatalf("failed to unmarshal V1.0.0 trace: %v", err)
	}

	// Export as current version
	data, err := original.ExportJSON(CurrentJSONSchemaVersion, time.Now())
	if err != nil {
		t.Fatalf("failed to export trace: %v", err)
	}

	// Write to temp file
	tmpDir := t.TempDir()
	path := tmpDir + "/roundtrip.json"
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("failed to write exported trace: %v", err)
	}

	// Reload the trace
	loaded, err := LoadVersionedTrace(path, DefaultCompatibilityOptions())
	if err != nil {
		t.Fatalf("failed to reload exported trace: %v", err)
	}

	// Verify structure preserved
	if loaded == nil {
		t.Fatal("reloaded trace should not be nil")
	}
	if len(loaded.States) != len(original.States) {
		t.Errorf("state count mismatch: got %d, want %d", len(loaded.States), len(original.States))
	}
	if loaded.SnapshotInterval != original.SnapshotInterval {
		t.Errorf("snapshot_interval mismatch: got %d, want %d", loaded.SnapshotInterval, original.SnapshotInterval)
	}
}