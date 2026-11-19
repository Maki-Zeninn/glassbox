// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package ipc

import (
	"encoding/json"
	"testing"

	"github.com/dotandev/glassbox/internal/errors"
)

func TestToErstErrorMemoryLimitByCode(t *testing.T) {
	e := (&Error{
		Code:    "ERR_MEMORY_LIMIT_EXCEEDED",
		Message: "ERR_MEMORY_LIMIT_EXCEEDED: consumed 2048 bytes, limit 1024 bytes",
	}).ToErstError()

	if e.Code != errors.CodeSimMemoryLimitExceeded {
		t.Fatalf("expected %s, got %s", errors.CodeSimMemoryLimitExceeded, e.Code)
	}
}

func TestToErstErrorMemoryLimitByMessage(t *testing.T) {
	e := (&Error{
		Message: "memory limit exceeded while simulating contract",
	}).ToErstError()

	if e.Code != errors.CodeSimMemoryLimitExceeded {
		t.Fatalf("expected %s, got %s", errors.CodeSimMemoryLimitExceeded, e.Code)
	}
}

func TestUnmarshalSimulationResponseSchemaWithInlineSnapshots(t *testing.T) {
	payload := []byte(`{
		"request_id":"req-1",
		"success":true,
		"version":"1.0.0",
		"result":{"fee_charged":"100"},
		"snapshots":{
			"inline":{
				"post-run":{
					"ledger_entries":[["a2V5","dmFsdWU="]],
					"linear_memory":"AQID"
				}
			}
		}
	}`)

	resp, err := UnmarshalSimulationResponseSchema(payload)
	if err != nil {
		t.Fatalf("expected no error unmarshalling inline snapshots, got %v", err)
	}
	if resp.Snapshots == nil {
		t.Fatal("expected snapshots payload")
	}
	snapshot, ok := resp.Snapshots.Inline["post-run"]
	if !ok {
		t.Fatal("expected inline snapshot with key post-run")
	}
	if got := len(snapshot.LedgerEntries); got != 1 {
		t.Fatalf("expected 1 ledger entry, got %d", got)
	}
	if snapshot.LinearMemory != "AQID" {
		t.Fatalf("expected linear memory AQID, got %q", snapshot.LinearMemory)
	}
}

func TestUnmarshalSimulationResponseSchemaWithLazySnapshotIDs(t *testing.T) {
	payload := []byte(`{
		"request_id":"req-2",
		"success":true,
		"version":"1.0.0",
		"snapshots":{
			"ids":["snap-1","snap-2"]
		}
	}`)

	resp, err := UnmarshalSimulationResponseSchema(payload)
	if err != nil {
		t.Fatalf("expected no error unmarshalling snapshot ids, got %v", err)
	}
	if resp.Snapshots == nil {
		t.Fatal("expected snapshots payload")
	}
	if got := len(resp.Snapshots.IDs); got != 2 {
		t.Fatalf("expected 2 snapshot ids, got %d", got)
	}
}

func TestSimulationResponseSchemaMarshalRoundTripsSnapshots(t *testing.T) {
	original := SimulationResponseSchema{
		RequestID: "req-3",
		Success:   true,
		Version:   "1.0.0",
		Snapshots: &SnapshotsPayload{
			Inline: map[string]InlineSnapshot{
				"step-1": {
					LedgerEntries: [][]string{{"a", "b"}},
				},
			},
			IDs: []string{"step-2"},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded SimulationResponseSchema
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if decoded.Snapshots == nil {
		t.Fatal("expected snapshots after round trip")
	}
	if _, ok := decoded.Snapshots.Inline["step-1"]; !ok {
		t.Fatal("expected inline snapshot after round trip")
	}
	if got := len(decoded.Snapshots.IDs); got != 1 {
		t.Fatalf("expected 1 snapshot id, got %d", got)
	}
}

// ─── IPC Protocol Version Negotiation (Issue #824) ──────────────────────────

func TestHandshakeRequest_MarshalRoundTrip_ClientVersion(t *testing.T) {
	req := HandshakeRequest{
		Type:             HandshakeRequestType,
		ProtocolVersion:  21,
		ClientVersion:    "1.2.3",
		RequiredFeatures: []string{"snapshot_streaming"},
		MaxRequestBytes:  1024 * 1024,
	}
	data, err := req.Marshal()
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	decoded, err := UnmarshalHandshakeRequest(data)
	if err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if decoded.ClientVersion != "1.2.3" {
		t.Errorf("expected client_version '1.2.3', got %q", decoded.ClientVersion)
	}
	if decoded.ProtocolVersion != 21 {
		t.Errorf("expected protocol_version 21, got %d", decoded.ProtocolVersion)
	}
}

func TestHandshakeResponse_MarshalRoundTrip_VersionBounds(t *testing.T) {
	resp := HandshakeResponse{
		Type:              HandshakeResponseType,
		SimulatorBuild:    "abc123",
		ProtocolVersion:   21,
		MinClientVersion:  "1.0.0",
		MaxClientVersion:  "2.0.0",
		SupportedFeatures: []string{"snapshot_streaming"},
		MaxRequestBytes:   1024 * 1024,
	}
	data, err := resp.Marshal()
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	decoded, err := UnmarshalHandshakeResponse(data)
	if err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if decoded.MinClientVersion != "1.0.0" {
		t.Errorf("expected min_client_version '1.0.0', got %q", decoded.MinClientVersion)
	}
	if decoded.MaxClientVersion != "2.0.0" {
		t.Errorf("expected max_client_version '2.0.0', got %q", decoded.MaxClientVersion)
	}
}

func TestHandshakeResponse_ErrorCode(t *testing.T) {
	resp := HandshakeResponse{
		Type:      HandshakeResponseType,
		Error:     "simulator protocol version incompatible",
		ErrorCode: "incompatible_version",
	}
	data, err := resp.Marshal()
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	decoded, err := UnmarshalHandshakeResponse(data)
	if err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if decoded.ErrorCode != "incompatible_version" {
		t.Errorf("expected error_code 'incompatible_version', got %q", decoded.ErrorCode)
	}
}
