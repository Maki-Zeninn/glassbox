// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package rpc

import (
	"bytes"
	"compress/gzip"
	"io"
	"strings"
	"testing"
)

func TestAggregateTracker_Basic(t *testing.T) {
	tracker := NewAggregateTracker(1000)
	
	// Record some requests
	err := tracker.RecordRequest(100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	
	err = tracker.RecordRequest(200)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	
	total, count, limit := tracker.GetStats()
	if total != 300 {
		t.Errorf("expected total 300, got %d", total)
	}
	if count != 2 {
		t.Errorf("expected count 2, got %d", count)
	}
	if limit != 1000 {
		t.Errorf("expected limit 1000, got %d", limit)
	}
}

func TestAggregateTracker_ExceedsLimit(t *testing.T) {
	tracker := NewAggregateTracker(500)
	
	// Record requests up to limit
	err := tracker.RecordRequest(300)
	if err != nil {
		t.Fatalf("unexpected error at 300 bytes: %v", err)
	}
	
	err = tracker.RecordRequest(200)
	if err != nil {
		t.Fatalf("unexpected error at 500 bytes: %v", err)
	}
	
	// This should exceed the limit
	err = tracker.RecordRequest(100)
	if err == nil {
		t.Error("expected error when exceeding aggregate limit")
	}
	
	aggregateErr, ok := err.(*AggregateLimitExceededError)
	if !ok {
		t.Fatalf("expected *AggregateLimitExceededError, got %T", err)
	}
	
	if aggregateErr.Code != ErrCodeAggregateLimitExceeded {
		t.Errorf("expected error code '%s', got '%s'", ErrCodeAggregateLimitExceeded, aggregateErr.Code)
	}
	
	if aggregateErr.TotalBytesRead != 600 {
		t.Errorf("expected total bytes 600, got %d", aggregateErr.TotalBytesRead)
	}
	
	if aggregateErr.RequestCount != 3 {
		t.Errorf("expected request count 3, got %d", aggregateErr.RequestCount)
	}
}

func TestAggregateTracker_Unlimited(t *testing.T) {
	tracker := NewAggregateTracker(0) // 0 means unlimited
	
	// Record a large request
	err := tracker.RecordRequest(1000000)
	if err != nil {
		t.Fatalf("unexpected error with unlimited tracker: %v", err)
	}
	
	total, count, limit := tracker.GetStats()
	if total != 1000000 {
		t.Errorf("expected total 1000000, got %d", total)
	}
	if limit != 0 {
		t.Errorf("expected limit 0 (unlimited), got %d", limit)
	}
}

func TestReadBoundedWithTracker_Success(t *testing.T) {
	tracker := NewAggregateTracker(10000)
	payload := strings.Repeat("x", 1000)
	src := strings.NewReader(payload)
	
	data, err := ReadBoundedWithTracker(src, 5000, "test_op", tracker)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	
	if len(data) != 1000 {
		t.Errorf("expected 1000 bytes, got %d", len(data))
	}
	
	total, count, _ := tracker.GetStats()
	if total != 1000 {
		t.Errorf("expected total 1000, got %d", total)
	}
	if count != 1 {
		t.Errorf("expected count 1, got %d", count)
	}
}

func TestReadBoundedWithTracker_ExceedsLimit(t *testing.T) {
	tracker := NewAggregateTracker(100)
	payload := strings.Repeat("x", 200)
	src := strings.NewReader(payload)
	
	_, err := ReadBoundedWithTracker(src, 150, "test_op", tracker)
	if err == nil {
		t.Error("expected error when exceeding per-response limit")
	}
	
	// Check that aggregate tracker was still updated
	total, count, _ := tracker.GetStats()
	if total != 150 {
		t.Errorf("expected total 150 (bytes read before error), got %d", total)
	}
}

func TestReadBoundedWithTracker_AggregateExceeded(t *testing.T) {
	tracker := NewAggregateTracker(100)
	payload := strings.Repeat("x", 50)
	src := strings.NewReader(payload)
	
	// First request succeeds
	_, err := ReadBoundedWithTracker(src, 1000, "test_op", tracker)
	if err != nil {
		t.Fatalf("unexpected error on first request: %v", err)
	}
	
	// Second request should exceed aggregate limit
	_, err = ReadBoundedWithTracker(src, 1000, "test_op", tracker)
	if err == nil {
		t.Error("expected error when exceeding aggregate limit")
	}
	
	aggregateErr, ok := err.(*AggregateLimitExceededError)
	if !ok {
		t.Fatalf("expected *AggregateLimitExceededError, got %T", err)
	}
	
	if aggregateErr.Code != ErrCodeAggregateLimitExceeded {
		t.Errorf("expected error code '%s', got '%s'", ErrCodeAggregateLimitExceeded, aggregateErr.Code)
	}
}

func TestPayloadTooLargeError_ToJSON(t *testing.T) {
	err := &PayloadTooLargeError{
		Code:        ErrCodePayloadTooLarge,
		Operation:  "getLedgerEntries",
		ReadBytes:   40 * 1024 * 1024,
		Limit:      32 * 1024 * 1024,
		Configured: 32 * 1024 * 1024,
	}
	
	jsonBytes, jsonErr := err.ToJSON()
	if jsonErr != nil {
		t.Fatalf("failed to marshal error to JSON: %v", jsonErr)
	}
	
	// Verify the JSON is compact (no response content)
	if len(jsonBytes) > 500 {
		t.Errorf("JSON error output too large: %d bytes", len(jsonBytes))
	}
	
	// Verify key fields are present
	jsonStr := string(jsonBytes)
	if !strings.Contains(jsonStr, ErrCodePayloadTooLarge) {
		t.Error("JSON should contain error code")
	}
	if !strings.Contains(jsonStr, "getLedgerEntries") {
		t.Error("JSON should contain operation")
	}
	if !strings.Contains(jsonStr, "read_bytes") {
		t.Error("JSON should contain read_bytes")
	}
	if !strings.Contains(jsonStr, "limit") {
		t.Error("JSON should contain limit")
	}
}

func TestAggregateLimitExceededError_ToJSON(t *testing.T) {
	err := &AggregateLimitExceededError{
		Code:             ErrCodeAggregateLimitExceeded,
		TotalBytesRead:  600 * 1024 * 1024,
		AggregateLimit:  512 * 1024 * 1024,
		ConfiguredLimit: 512 * 1024 * 1024,
		RequestCount:    10,
	}
	
	jsonBytes, jsonErr := err.ToJSON()
	if jsonErr != nil {
		t.Fatalf("failed to marshal error to JSON: %v", jsonErr)
	}
	
	// Verify the JSON is compact
	if len(jsonBytes) > 500 {
		t.Errorf("JSON error output too large: %d bytes", len(jsonBytes))
	}
	
	// Verify key fields are present
	jsonStr := string(jsonBytes)
	if !strings.Contains(jsonStr, ErrCodeAggregateLimitExceeded) {
		t.Error("JSON should contain error code")
	}
	if !strings.Contains(jsonStr, "total_bytes_read") {
		t.Error("JSON should contain total_bytes_read")
	}
	if !strings.Contains(jsonStr, "aggregate_limit") {
		t.Error("JSON should contain aggregate_limit")
	}
	if !strings.Contains(jsonStr, "request_count") {
		t.Error("JSON should contain request_count")
	}
}

func TestCompressedResponse(t *testing.T) {
	// Create a compressed payload
	original := strings.Repeat("x", 10000)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	gz.Write([]byte(original))
	gz.Close()
	
	compressed := buf.Bytes()
	tracker := NewAggregateTracker(100000)
	
	// Read compressed data
	src := bytes.NewReader(compressed)
	data, err := ReadBoundedWithTracker(src, 20000, "getLedgerEntries", tracker)
	if err != nil {
		t.Fatalf("unexpected error reading compressed data: %v", err)
	}
	
	// The compressed data should be read successfully (smaller than limit)
	if len(data) != len(compressed) {
		t.Errorf("expected %d bytes (compressed), got %d", len(compressed), len(data))
	}
	
	// Verify tracker was updated
	total, count, _ := tracker.GetStats()
	if total != int64(len(compressed)) {
		t.Errorf("expected total %d, got %d", len(compressed), total)
	}
}

func TestExactBoundary_PerResponse(t *testing.T) {
	tracker := NewAggregateTracker(100000)
	payload := strings.Repeat("x", 1000)
	src := strings.NewReader(payload)
	
	// Read exactly at the limit
	data, err := ReadBoundedWithTracker(src, 1000, "test_op", tracker)
	if err != nil {
		t.Errorf("unexpected error at exact boundary: %v", err)
	}
	
	if len(data) != 1000 {
		t.Errorf("expected 1000 bytes at exact boundary, got %d", len(data))
	}
}

func TestExactBoundary_Aggregate(t *testing.T) {
	tracker := NewAggregateTracker(1000)
	
	// First request: 500 bytes
	payload1 := strings.Repeat("x", 500)
	src1 := strings.NewReader(payload1)
	_, err := ReadBoundedWithTracker(src1, 10000, "op1", tracker)
	if err != nil {
		t.Fatalf("unexpected error on first request: %v", err)
	}
	
	// Second request: 500 bytes (should hit exact aggregate limit)
	payload2 := strings.Repeat("x", 500)
	src2 := strings.NewReader(payload2)
	_, err = ReadBoundedWithTracker(src2, 10000, "op2", tracker)
	if err != nil {
		t.Errorf("unexpected error at exact aggregate boundary: %v", err)
	}
	
	total, count, _ := tracker.GetStats()
	if total != 1000 {
		t.Errorf("expected total 1000 at exact boundary, got %d", total)
	}
	if count != 2 {
		t.Errorf("expected count 2, got %d", count)
	}
}

func TestOneOver_PerResponse(t *testing.T) {
	tracker := NewAggregateTracker(100000)
	payload := strings.Repeat("x", 1001)
	src := strings.NewReader(payload)
	
	_, err := ReadBoundedWithTracker(src, 1000, "test_op", tracker)
	if err == nil {
		t.Error("expected error one byte over limit")
	}
	
	payloadErr, ok := err.(*PayloadTooLargeError)
	if !ok {
		t.Fatalf("expected *PayloadTooLargeError, got %T", err)
	}
	
	if payloadErr.ReadBytes != 1001 {
		t.Errorf("expected read bytes 1001, got %d", payloadErr.ReadBytes)
	}
	
	if payloadErr.Limit != 1000 {
		t.Errorf("expected limit 1000, got %d", payloadErr.Limit)
	}
}

func TestOneOver_Aggregate(t *testing.T) {
	tracker := NewAggregateTracker(1000)
	
	// First request: 500 bytes
	payload1 := strings.Repeat("x", 500)
	src1 := strings.NewReader(payload1)
	_, err := ReadBoundedWithTracker(src1, 10000, "op1", tracker)
	if err != nil {
		t.Fatalf("unexpected error on first request: %v", err)
	}
	
	// Second request: 501 bytes (should exceed aggregate limit by 1 byte)
	payload2 := strings.Repeat("x", 501)
	src2 := strings.NewReader(payload2)
	_, err = ReadBoundedWithTracker(src2, 10000, "op2", tracker)
	if err == nil {
		t.Error("expected error one byte over aggregate limit")
	}
	
	aggregateErr, ok := err.(*AggregateLimitExceededError)
	if !ok {
		t.Fatalf("expected *AggregateLimitExceededError, got %T", err)
	}
	
	if aggregateErr.TotalBytesRead != 1001 {
		t.Errorf("expected total bytes 1001, got %d", aggregateErr.TotalBytesRead)
	}
	
	if aggregateErr.AggregateLimit != 1000 {
		t.Errorf("expected aggregate limit 1000, got %d", aggregateErr.AggregateLimit)
	}
}

func TestMultipleRequests_AggregateTracking(t *testing.T) {
	tracker := NewAggregateTracker(10000)
	
	for i := 0; i < 10; i++ {
		payload := strings.Repeat("x", 500)
		src := strings.NewReader(payload)
		_, err := ReadBoundedWithTracker(src, 10000, "op", tracker)
		if err != nil {
			t.Fatalf("unexpected error on request %d: %v", i, err)
		}
	}
	
	total, count, _ := tracker.GetStats()
	if total != 5000 {
		t.Errorf("expected total 5000 bytes, got %d", total)
	}
	if count != 10 {
		t.Errorf("expected count 10, got %d", count)
	}
}

func TestConcurrentAggregateTracking(t *testing.T) {
	tracker := NewAggregateTracker(10000)
	
	done := make(chan bool)
	errors := make(chan error, 10)
	
	// Launch 10 concurrent requests
	for i := 0; i < 10; i++ {
		go func(idx int) {
			payload := strings.Repeat("x", 100)
			src := strings.NewReader(payload)
			_, err := ReadBoundedWithTracker(src, 10000, "op", tracker)
			errors <- err
			done <- true
		}(i)
	}
	
	// Wait for all to complete
	for i := 0; i < 10; i++ {
		<-done
		}
	
	// Check no errors occurred
	for i := 0; i < 10; i++ {
		if err := <-errors; err != nil {
			t.Errorf("concurrent request %d failed: %v", i, err)
		}
	}
	
	total, count, _ := tracker.GetStats()
	if total != 1000 {
		t.Errorf("expected total 1000 bytes, got %d", total)
	}
	if count != 10 {
		t.Errorf("expected count 10, got %d", count)
	}
}

func TestDiagnosticString_PayloadTooLarge(t *testing.T) {
	err := &PayloadTooLargeError{
		Code:        ErrCodePayloadTooLarge,
		Operation:  "simulateTransaction",
		ReadBytes:   64 * 1024 * 1024,
		Limit:      32 * 1024 * 1024,
		Configured: 32 * 1024 * 1024,
	}
	
	msg := err.Error()
	
	if !strings.Contains(msg, "simulateTransaction") {
		t.Error("error message should contain operation name")
	}
	if !strings.Contains(msg, "32.0 MiB") {
		t.Error("error message should contain human-readable limit")
	}
	if !strings.Contains(msg, "64.0 MiB") {
		t.Error("error message should contain human-readable read bytes")
	}
	if !strings.Contains(msg, "--rpc-response-limit") {
		t.Error("error message should contain remediation hint")
	}
}

func TestDiagnosticString_AggregateLimitExceeded(t *testing.T) {
	err := &AggregateLimitExceededError{
		Code:             ErrCodeAggregateLimitExceeded,
		TotalBytesRead:  600 * 1024 * 1024,
		AggregateLimit:  512 * 1024 * 1024,
		ConfiguredLimit: 512 * 1024 * 1024,
		RequestCount:    15,
	}
	
	msg := err.Error()
	
	if !strings.Contains(msg, "aggregate fetch limit exceeded") {
		t.Error("error message should describe aggregate limit exceeded")
	}
	if !strings.Contains(msg, "512.0 MiB") {
		t.Error("error message should contain human-readable limit")
	}
	if !strings.Contains(msg, "600.0 MiB") {
		t.Error("error message should contain human-readable total bytes")
	}
	if !strings.Contains(msg, "15 requests") {
		t.Error("error message should contain request count")
	}
	if !strings.Contains(msg, "--rpc-aggregate-limit") {
		t.Error("error message should contain remediation hint")
	}
}

func TestDefaultLimits(t *testing.T) {
	if DefaultResponsePayloadLimit != 32*1024*1024 {
		t.Errorf("DefaultResponsePayloadLimit should be 32 MiB, got %d", DefaultResponsePayloadLimit)
	}
	
	if DefaultAggregateFetchLimit != 512*1024*1024 {
		t.Errorf("DefaultAggregateFetchLimit should be 512 MiB, got %d", DefaultAggregateFetchLimit)
	}
	
	if DefaultEnvelopeLimit != 1*1024*1024 {
		t.Errorf("DefaultEnvelopeLimit should be 1 MiB, got %d", DefaultEnvelopeLimit)
	}
	
	if DefaultLedgerEntriesLimit != 1*1024*1024 {
		t.Errorf("DefaultLedgerEntriesLimit should be 1 MiB, got %d", DefaultLedgerEntriesLimit)
	}
}

func TestErrorCodes(t *testing.T) {
	tests := []struct {
		name     string
		expected string
	}{
		{"PayloadTooLarge", ErrCodePayloadTooLarge},
		{"AggregateLimitExceeded", ErrCodeAggregateLimitExceeded},
		{"EnvelopeTooLarge", ErrCodeEnvelopeTooLarge},
		{"LedgerEntriesTooLarge", ErrCodeLedgerEntriesTooLarge},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.expected == "" {
				t.Error("error code should not be empty")
			}
		})
	}
}