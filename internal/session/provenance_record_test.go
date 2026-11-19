// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Tests for ProvenanceRecord, ProvenanceChain, and session integration.
package session

import (
	"strings"
	"testing"
	"time"
)

// ── HashContent ───────────────────────────────────────────────────────────────

func TestHashContent_DeterministicAndNonEmpty(t *testing.T) {
	h1 := HashContent([]byte("hello"))
	h2 := HashContent([]byte("hello"))
	if h1 != h2 {
		t.Error("HashContent must be deterministic")
	}
	if len(h1) != 64 {
		t.Errorf("HashContent must return a 64-char hex SHA-256, got %d chars", len(h1))
	}
}

func TestHashContent_DifferentInputsDifferentHashes(t *testing.T) {
	h1 := HashContent([]byte("input-a"))
	h2 := HashContent([]byte("input-b"))
	if h1 == h2 {
		t.Error("different inputs must produce different hashes")
	}
}

// ── NewInputRef / NewOutputRef ────────────────────────────────────────────────

func TestNewInputRef_HashesContent(t *testing.T) {
	content := []byte("envelope-xdr-data")
	ref := NewInputRef("transaction_envelope", content)
	if ref.Role != "transaction_envelope" {
		t.Errorf("role = %q, want transaction_envelope", ref.Role)
	}
	if ref.SHA256 != HashContent(content) {
		t.Error("SHA256 must equal HashContent of the provided content")
	}
	if ref.Size != int64(len(content)) {
		t.Errorf("Size = %d, want %d", ref.Size, len(content))
	}
}

func TestNewOutputRef_HashesContent(t *testing.T) {
	content := []byte("{\"status\":\"success\"}")
	ref := NewOutputRef("sim_response", content)
	if ref.SHA256 != HashContent(content) {
		t.Error("SHA256 must equal HashContent of the provided content")
	}
}

// ── ProvenanceRecord.Finalize ─────────────────────────────────────────────────

func TestProvenanceRecord_Finalize_SetsRecordID(t *testing.T) {
	r := &ProvenanceRecord{
		Operation:   "debug",
		ToolName:    "glassbox",
		ToolVersion: "1.2.3",
		Timestamp:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	if err := r.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if r.RecordID == "" {
		t.Error("RecordID must be set after Finalize")
	}
	if len(r.RecordID) != 64 {
		t.Errorf("RecordID must be a 64-char hex string, got %d chars", len(r.RecordID))
	}
}

func TestProvenanceRecord_Finalize_Deterministic(t *testing.T) {
	ts := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	r1 := &ProvenanceRecord{Operation: "debug", ToolName: "glassbox", ToolVersion: "1.0.0", Timestamp: ts}
	r2 := &ProvenanceRecord{Operation: "debug", ToolName: "glassbox", ToolVersion: "1.0.0", Timestamp: ts}
	if err := r1.Finalize(); err != nil {
		t.Fatalf("r1.Finalize: %v", err)
	}
	if err := r2.Finalize(); err != nil {
		t.Fatalf("r2.Finalize: %v", err)
	}
	if r1.RecordID != r2.RecordID {
		t.Error("Finalize must be deterministic for identical records")
	}
}

func TestProvenanceRecord_Finalize_DifferentInputsChangesID(t *testing.T) {
	ts := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	r1 := &ProvenanceRecord{Operation: "debug", ToolName: "glassbox", ToolVersion: "1.0.0", Timestamp: ts}
	r2 := &ProvenanceRecord{
		Operation:   "debug",
		ToolName:    "glassbox",
		ToolVersion: "1.0.0",
		Timestamp:   ts,
		Inputs:      []InputRef{NewInputRef("tx", []byte("different-envelope"))},
	}
	if err := r1.Finalize(); err != nil {
		t.Fatalf("r1.Finalize: %v", err)
	}
	if err := r2.Finalize(); err != nil {
		t.Fatalf("r2.Finalize: %v", err)
	}
	if r1.RecordID == r2.RecordID {
		t.Error("different inputs must produce different RecordIDs")
	}
}

// ── VerifyRecord ──────────────────────────────────────────────────────────────

func TestVerifyRecord_AllMatch(t *testing.T) {
	envelope := []byte("envelope-content")
	simResp := []byte("{\"status\":\"ok\"}")
	r := &ProvenanceRecord{
		Operation: "debug",
		Inputs:    []InputRef{NewInputRef("transaction_envelope", envelope)},
		Outputs:   []OutputRef{NewOutputRef("sim_response", simResp)},
	}
	result := VerifyRecord(r,
		map[string][]byte{"transaction_envelope": envelope},
		map[string][]byte{"sim_response": simResp},
	)
	if !result.OK {
		t.Errorf("VerifyRecord should return OK=true when all hashes match, mismatches: %v", result.Mismatches)
	}
	if result.InputsChecked != 1 {
		t.Errorf("InputsChecked = %d, want 1", result.InputsChecked)
	}
	if result.OutputsChecked != 1 {
		t.Errorf("OutputsChecked = %d, want 1", result.OutputsChecked)
	}
}

func TestVerifyRecord_ModifiedInput_Detected(t *testing.T) {
	originalEnvelope := []byte("original-envelope")
	tamperedEnvelope := []byte("tampered-envelope")
	r := &ProvenanceRecord{
		Operation: "debug",
		Inputs:    []InputRef{NewInputRef("transaction_envelope", originalEnvelope)},
	}
	result := VerifyRecord(r,
		map[string][]byte{"transaction_envelope": tamperedEnvelope},
		nil,
	)
	if result.OK {
		t.Error("VerifyRecord should return OK=false when input was modified")
	}
	if len(result.Mismatches) == 0 {
		t.Fatal("expected at least one mismatch")
	}
	m := result.Mismatches[0]
	if !m.IsInput {
		t.Error("mismatch should be flagged as IsInput=true")
	}
	if m.Role != "transaction_envelope" {
		t.Errorf("mismatch role = %q, want transaction_envelope", m.Role)
	}
}

func TestVerifyRecord_ModifiedOutput_Detected(t *testing.T) {
	originalOutput := []byte("{\"status\":\"ok\"}")
	tamperedOutput := []byte("{\"status\":\"hacked\"}")
	r := &ProvenanceRecord{
		Operation: "debug",
		Outputs:   []OutputRef{NewOutputRef("sim_response", originalOutput)},
	}
	result := VerifyRecord(r,
		nil,
		map[string][]byte{"sim_response": tamperedOutput},
	)
	if result.OK {
		t.Error("VerifyRecord should return OK=false when output was modified")
	}
	if len(result.Mismatches) == 0 {
		t.Fatal("expected at least one mismatch")
	}
	m := result.Mismatches[0]
	if m.IsInput {
		t.Error("mismatch should be flagged as IsInput=false for output")
	}
}

func TestVerifyRecord_MissingArtifactSkipped(t *testing.T) {
	r := &ProvenanceRecord{
		Inputs:  []InputRef{NewInputRef("optional_input", []byte("content"))},
		Outputs: []OutputRef{NewOutputRef("optional_output", []byte("result"))},
	}
	// Provide empty maps — no artifacts available for re-hashing.
	result := VerifyRecord(r, nil, nil)
	if !result.OK {
		t.Error("VerifyRecord should not fail when no live artifacts are provided")
	}
	if result.InputsChecked != 0 || result.OutputsChecked != 0 {
		t.Error("no artifacts checked when maps are empty")
	}
}

// ── ChainMismatch.String ──────────────────────────────────────────────────────

func TestChainMismatch_String_ContainsRole(t *testing.T) {
	m := ChainMismatch{
		Role:         "transaction_envelope",
		RecordedHash: strings.Repeat("a", 64),
		ActualHash:   strings.Repeat("b", 64),
		IsInput:      true,
	}
	s := m.String()
	if !strings.Contains(s, "transaction_envelope") {
		t.Errorf("ChainMismatch.String should contain the role, got: %q", s)
	}
	if !strings.Contains(s, "input") {
		t.Errorf("ChainMismatch.String should say 'input', got: %q", s)
	}
}

// ── ProvenanceChain ───────────────────────────────────────────────────────────

func TestProvenanceChain_Append_LinksRecords(t *testing.T) {
	chain := &ProvenanceChain{}
	r1 := &ProvenanceRecord{Operation: "fetch", ToolName: "glassbox", ToolVersion: "1.0", Timestamp: time.Now().UTC()}
	if err := chain.Append(r1); err != nil {
		t.Fatalf("Append r1: %v", err)
	}
	if r1.ChainPredecessorID != "" {
		t.Error("first record should have no predecessor")
	}
	r2 := &ProvenanceRecord{Operation: "sign", ToolName: "glassbox", ToolVersion: "1.0", Timestamp: time.Now().UTC()}
	if err := chain.Append(r2); err != nil {
		t.Fatalf("Append r2: %v", err)
	}
	if r2.ChainPredecessorID != r1.RecordID {
		t.Errorf("r2.ChainPredecessorID = %q, want %q", r2.ChainPredecessorID, r1.RecordID)
	}
}

func TestProvenanceChain_VerifyChainIntegrity_Clean(t *testing.T) {
	chain := &ProvenanceChain{}
	r1 := &ProvenanceRecord{Operation: "a", ToolName: "t", ToolVersion: "1", Timestamp: time.Now().UTC()}
	r2 := &ProvenanceRecord{Operation: "b", ToolName: "t", ToolVersion: "1", Timestamp: time.Now().UTC()}
	_ = chain.Append(r1)
	_ = chain.Append(r2)
	issues := chain.VerifyChainIntegrity()
	if len(issues) != 0 {
		t.Errorf("expected no issues for valid chain, got: %v", issues)
	}
}

func TestProvenanceChain_VerifyChainIntegrity_BrokenLink(t *testing.T) {
	chain := &ProvenanceChain{}
	r1 := &ProvenanceRecord{Operation: "a", ToolName: "t", ToolVersion: "1", Timestamp: time.Now().UTC()}
	r2 := &ProvenanceRecord{Operation: "b", ToolName: "t", ToolVersion: "1", Timestamp: time.Now().UTC()}
	_ = chain.Append(r1)
	_ = chain.Append(r2)
	// Corrupt the link.
	chain.Records[1].ChainPredecessorID = strings.Repeat("0", 64)
	issues := chain.VerifyChainIntegrity()
	if len(issues) == 0 {
		t.Error("expected chain integrity issue after corrupting a link")
	}
	found := false
	for _, issue := range issues {
		if strings.Contains(issue, "does not match") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'does not match' in issues, got: %v", issues)
	}
}

func TestProvenanceChain_VerifyChainIntegrity_GenesisWithPredecessor(t *testing.T) {
	chain := &ProvenanceChain{}
	r := &ProvenanceRecord{
		Operation:          "a",
		ToolName:           "t",
		ToolVersion:        "1",
		Timestamp:          time.Now().UTC(),
		ChainPredecessorID: strings.Repeat("f", 64),
	}
	if err := r.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	chain.Records = []ProvenanceRecord{*r}
	issues := chain.VerifyChainIntegrity()
	if len(issues) == 0 {
		t.Error("genesis record with a ChainPredecessorID should be flagged")
	}
}

// ── Session integration ───────────────────────────────────────────────────────

func TestAttachProvenanceChain_RoundTrip(t *testing.T) {
	d := validData()
	chain := &ProvenanceChain{}
	r := &ProvenanceRecord{
		Operation:   "debug",
		ToolName:    "glassbox",
		ToolVersion: "1.0.0",
		Timestamp:   time.Now().UTC(),
		Inputs:      []InputRef{NewInputRef("tx", []byte("envelope"))},
	}
	_ = chain.Append(r)

	if err := AttachProvenanceChain(d, chain); err != nil {
		t.Fatalf("AttachProvenanceChain: %v", err)
	}

	recovered := SessionProvenanceChain(d)
	if len(recovered.Records) != 1 {
		t.Fatalf("expected 1 record after round-trip, got %d", len(recovered.Records))
	}
	if recovered.Records[0].RecordID != r.RecordID {
		t.Errorf("RecordID mismatch after round-trip: got %q, want %q",
			recovered.Records[0].RecordID, r.RecordID)
	}
}

func TestSessionProvenanceChain_NilData_ReturnsEmpty(t *testing.T) {
	chain := SessionProvenanceChain(nil)
	if chain == nil {
		t.Fatal("SessionProvenanceChain(nil) returned nil")
	}
	if len(chain.Records) != 0 {
		t.Error("expected empty chain for nil session")
	}
}

func TestBuildSessionProvenanceRecord_ExcludesSecrets(t *testing.T) {
	d := validData()
	d.EnvelopeXdr = "AAA=="
	d.SimResponseJSON = `{"status":"ok"}`

	r := BuildSessionProvenanceRecord(d, "1.2.3")
	if r == nil {
		t.Fatal("BuildSessionProvenanceRecord returned nil")
	}
	if r.Operation != "debug" {
		t.Errorf("Operation = %q, want debug", r.Operation)
	}
	// Verify that no sensitive fields appear directly in the record.
	for _, in := range r.Inputs {
		if strings.Contains(in.SHA256, "AAA") {
			t.Error("SHA256 should not contain raw content — it should be a hash")
		}
	}
	// Configuration must not contain credential-like keys.
	for k := range r.Configuration {
		lower := strings.ToLower(k)
		if strings.Contains(lower, "key") || strings.Contains(lower, "pin") ||
			strings.Contains(lower, "token") || strings.Contains(lower, "secret") {
			t.Errorf("Configuration must not contain potentially sensitive key %q", k)
		}
	}
}
