// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package rpc

import (
	"errors"
	"strings"
	"testing"
)

func TestBoundedReader_WithinLimit(t *testing.T) {
	src := strings.NewReader("hello world")
	data, err := ReadBounded(src, 100, "test_op")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != "hello world" {
		t.Errorf("got %q, want %q", string(data), "hello world")
	}
}

func TestBoundedReader_ExactLimit(t *testing.T) {
	payload := "exactly12"
	src := strings.NewReader(payload)
	data, err := ReadBounded(src, int64(len(payload)), "test_op")
	if err != nil {
		t.Fatalf("unexpected error at exact limit: %v", err)
	}
	if string(data) != payload {
		t.Errorf("got %q, want %q", string(data), payload)
	}
}

func TestBoundedReader_Oversize(t *testing.T) {
	payload := strings.Repeat("x", 200)
	src := strings.NewReader(payload)
	_, err := ReadBounded(src, 100, "getLedgerEntries")
	if err == nil {
		t.Fatal("expected PayloadTooLargeError, got nil")
	}
	var tooLarge *PayloadTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("expected *PayloadTooLargeError, got %T: %v", err, err)
	}
	if tooLarge.Limit != 100 {
		t.Errorf("Limit = %d, want 100", tooLarge.Limit)
	}
	if tooLarge.Operation != "getLedgerEntries" {
		t.Errorf("Operation = %q, want \"getLedgerEntries\"", tooLarge.Operation)
	}
	if !strings.Contains(err.Error(), "getLedgerEntries") {
		t.Errorf("error message should contain operation name, got: %v", err)
	}
	if !strings.Contains(err.Error(), "rpc-response-limit") {
		t.Errorf("error message should contain remediation hint, got: %v", err)
	}
}

func TestBoundedReader_ZeroLimitIsUnlimited(t *testing.T) {
	payload := strings.Repeat("z", 1_000_000)
	src := strings.NewReader(payload)
	data, err := ReadBounded(src, 0, "op")
	if err != nil {
		t.Fatalf("unexpected error with zero limit: %v", err)
	}
	if len(data) != 1_000_000 {
		t.Errorf("got %d bytes, want 1_000_000", len(data))
	}
}

func TestBoundedReader_BytesRead(t *testing.T) {
	br := NewBoundedReader(strings.NewReader("abcde"), 10, "op")
	buf := make([]byte, 3)
	n, err := br.Read(buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 3 || br.BytesRead() != 3 {
		t.Errorf("BytesRead = %d, want 3", br.BytesRead())
	}
}

func TestPayloadTooLargeError_Message(t *testing.T) {
	e := &PayloadTooLargeError{
		Operation: "simulateTransaction",
		ReadBytes: 40 * 1024 * 1024,
		Limit:     32 * 1024 * 1024,
	}
	msg := e.Error()
	if !strings.Contains(msg, "simulateTransaction") {
		t.Errorf("expected operation in message, got: %s", msg)
	}
	if !strings.Contains(msg, "32.0 MB") {
		t.Errorf("expected human-readable limit in message, got: %s", msg)
	}
}
