// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package protocolreg

import (
	"strings"
	"testing"
)

// canonicalTraceHash is the 64-character stub used across trace-step URI tests.
const canonicalTraceHash = "5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab"

// validTraceBase is a minimal valid glassbox://trace/ URI used as a base for
// table-driven mutation tests.
const validTraceBase = "glassbox://trace/" + canonicalTraceHash + "/step/3?network=testnet"

func TestParseTraceStepURI_Valid_Minimal(t *testing.T) {
	got, err := ParseTraceStepURI(validTraceBase)
	if err != nil {
		t.Fatalf("unexpected error for valid trace URI: %v", err)
	}
	if got.TransactionHash != canonicalTraceHash {
		t.Errorf("TransactionHash: got %q, want %q", got.TransactionHash, canonicalTraceHash)
	}
	if got.StepIndex != 3 {
		t.Errorf("StepIndex: got %d, want 3", got.StepIndex)
	}
	if got.Network != "testnet" {
		t.Errorf("Network: got %q, want testnet", got.Network)
	}
	if got.File != "" || got.Line != 0 || got.Column != 0 || got.View != "" {
		t.Errorf("optional fields should be zero for minimal URI, got file=%q line=%d col=%d view=%q",
			got.File, got.Line, got.Column, got.View)
	}
}

func TestParseTraceStepURI_Valid_AllOptionals(t *testing.T) {
	raw := "glassbox://trace/" + canonicalTraceHash +
		"/step/7?network=mainnet&file=src%2Flib.rs&line=42&col=8&view=source"
	got, err := ParseTraceStepURI(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.StepIndex != 7 {
		t.Errorf("StepIndex: got %d, want 7", got.StepIndex)
	}
	if got.Network != "mainnet" {
		t.Errorf("Network: got %q, want mainnet", got.Network)
	}
	if got.File != "src/lib.rs" {
		t.Errorf("File: got %q, want src/lib.rs", got.File)
	}
	if got.Line != 42 {
		t.Errorf("Line: got %d, want 42", got.Line)
	}
	if got.Column != 8 {
		t.Errorf("Column: got %d, want 8", got.Column)
	}
	if got.View != "source" {
		t.Errorf("View: got %q, want source", got.View)
	}
}

func TestParseTraceStepURI_Valid_StepZero(t *testing.T) {
	raw := "glassbox://trace/" + canonicalTraceHash + "/step/0?network=testnet"
	got, err := ParseTraceStepURI(raw)
	if err != nil {
		t.Fatalf("step 0 must be accepted, got: %v", err)
	}
	if got.StepIndex != 0 {
		t.Errorf("StepIndex: got %d, want 0", got.StepIndex)
	}
}

func TestParseTraceStepURI_Invalid_EmptyURI(t *testing.T) {
	_, err := ParseTraceStepURI("")
	assertErrContains(t, err, "must not be empty")
}

func TestParseTraceStepURI_Invalid_WrongScheme(t *testing.T) {
	_, err := ParseTraceStepURI("https://trace/" + canonicalTraceHash + "/step/0?network=testnet")
	assertErrContains(t, err, "expected glassbox://")
}

func TestParseTraceStepURI_Invalid_WrongHost(t *testing.T) {
	_, err := ParseTraceStepURI("glassbox://debug/" + canonicalTraceHash + "/step/0?network=testnet")
	assertErrContains(t, err, "expected \"trace\"")
}

func TestParseTraceStepURI_Invalid_ShortHash(t *testing.T) {
	_, err := ParseTraceStepURI("glassbox://trace/abc/step/0?network=testnet")
	assertErrContains(t, err, "64-character hex")
}

func TestParseTraceStepURI_Invalid_MissingStepSegment(t *testing.T) {
	_, err := ParseTraceStepURI("glassbox://trace/" + canonicalTraceHash + "?network=testnet")
	assertErrContains(t, err, "wrong path structure")
}

func TestParseTraceStepURI_Invalid_WrongMiddleSegment(t *testing.T) {
	_, err := ParseTraceStepURI("glassbox://trace/" + canonicalTraceHash + "/jump/0?network=testnet")
	assertErrContains(t, err, "expected \"step\"")
}

func TestParseTraceStepURI_Invalid_NegativeStep(t *testing.T) {
	_, err := ParseTraceStepURI("glassbox://trace/" + canonicalTraceHash + "/step/-1?network=testnet")
	assertErrContains(t, err, "non-negative integer")
}

func TestParseTraceStepURI_Invalid_NonNumericStep(t *testing.T) {
	_, err := ParseTraceStepURI("glassbox://trace/" + canonicalTraceHash + "/step/abc?network=testnet")
	assertErrContains(t, err, "non-negative integer")
}

func TestParseTraceStepURI_Invalid_MissingNetwork(t *testing.T) {
	_, err := ParseTraceStepURI("glassbox://trace/" + canonicalTraceHash + "/step/0")
	assertErrContains(t, err, "missing required query parameter: network")
}

func TestParseTraceStepURI_Invalid_BadNetwork(t *testing.T) {
	_, err := ParseTraceStepURI("glassbox://trace/" + canonicalTraceHash + "/step/0?network=staging")
	assertErrContains(t, err, "invalid network")
}

func TestParseTraceStepURI_Invalid_BadView(t *testing.T) {
	_, err := ParseTraceStepURI("glassbox://trace/" + canonicalTraceHash + "/step/0?network=testnet&view=raw")
	assertErrContains(t, err, "invalid view")
}

func TestParseTraceStepURI_Invalid_UnknownParam(t *testing.T) {
	_, err := ParseTraceStepURI("glassbox://trace/" + canonicalTraceHash + "/step/0?network=testnet&secret=x")
	assertErrContains(t, err, "unknown query parameter")
}

func TestParseTraceStepURI_Invalid_NullByte(t *testing.T) {
	_, err := ParseTraceStepURI("glassbox://trace/\x00/step/0?network=testnet")
	assertErrContains(t, err, "null bytes")
}

func TestParseTraceStepURI_Invalid_PathTraversal(t *testing.T) {
	_, err := ParseTraceStepURI("glassbox://trace/../etc/passwd/step/0?network=testnet")
	assertErrContains(t, err, "path traversal")
}

func TestParseTraceStepURI_Invalid_LineZero(t *testing.T) {
	_, err := ParseTraceStepURI(validTraceBase + "&line=0")
	assertErrContains(t, err, "invalid line")
}

func TestParseTraceStepURI_Invalid_ColZero(t *testing.T) {
	_, err := ParseTraceStepURI(validTraceBase + "&col=0")
	assertErrContains(t, err, "invalid col")
}

func TestParseTraceStepURI_AllowedViews(t *testing.T) {
	views := []string{"trace", "source", "flamegraph", "events", "auth", "budget", "storage"}
	for _, v := range views {
		uri := validTraceBase + "&view=" + v
		got, err := ParseTraceStepURI(uri)
		if err != nil {
			t.Errorf("view=%q: unexpected error: %v", v, err)
			continue
		}
		if got.View != v {
			t.Errorf("view=%q: got %q", v, got.View)
		}
	}
}

// assertErrContains fails the test when err is nil or does not contain substr.
func assertErrContains(t *testing.T, err error, substr string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error containing %q, got nil", substr)
	}
	if !strings.Contains(err.Error(), substr) {
		t.Errorf("error should contain %q, got: %q", substr, err.Error())
	}
}
