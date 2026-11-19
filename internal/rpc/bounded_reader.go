// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package rpc

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// DefaultResponsePayloadLimit is the default maximum number of bytes allowed
// for a single Soroban RPC response body (32 MiB).
const DefaultResponsePayloadLimit int64 = 32 * 1024 * 1024

// DefaultAggregateFetchLimit is the default total bytes allowed across all
// RPC responses in a single session (512 MiB).
const DefaultAggregateFetchLimit int64 = 512 * 1024 * 1024

// DefaultEnvelopeLimit is the default maximum size for transaction envelopes (1 MiB).
const DefaultEnvelopeLimit int64 = 1 * 1024 * 1024

// DefaultLedgerEntriesLimit is the default maximum size for ledger entries responses (1 MiB).
const DefaultLedgerEntriesLimit int64 = 1 * 1024 * 1024

// Error codes for resource limit violations
const (
	ErrCodePayloadTooLarge     = "ERR_PAYLOAD_TOO_LARGE"
	ErrCodeAggregateLimitExceeded = "ERR_AGGREGATE_LIMIT_EXCEEDED"
	ErrCodeEnvelopeTooLarge    = "ERR_ENVELOPE_TOO_LARGE"
	ErrCodeLedgerEntriesTooLarge = "ERR_LEDGER_ENTRIES_TOO_LARGE"
)

// PayloadTooLargeError is returned when a response body exceeds the configured
// byte limit. It names the operation and the effective limit so callers can
// surface an actionable remediation hint.
type PayloadTooLargeError struct {
	Code        string `json:"code"`
	Operation  string `json:"operation"`
	ReadBytes  int64  `json:"read_bytes"`
	Limit      int64  `json:"limit"`
	Configured int64  `json:"configured_limit"`
}

func (e *PayloadTooLargeError) Error() string {
	return fmt.Sprintf(
		"rpc: %s response exceeded size limit: read %s, limit %s — "+
			"reduce the request scope (fewer ledger keys, smaller footprint) "+
			"or raise the limit with --rpc-response-limit",
		e.Operation,
		formatBytes(e.ReadBytes),
		formatBytes(e.Limit),
	)
}

// ToJSON returns a compact JSON representation of the error.
func (e *PayloadTooLargeError) ToJSON() ([]byte, error) {
	return json.Marshal(e)
}

// AggregateLimitExceededError is returned when the total bytes fetched across
// all requests exceeds the configured aggregate limit.
type AggregateLimitExceededError struct {
	Code             string `json:"code"`
	TotalBytesRead  int64  `json:"total_bytes_read"`
	AggregateLimit  int64  `json:"aggregate_limit"`
	ConfiguredLimit int64  `json:"configured_limit"`
	RequestCount    int    `json:"request_count"`
}

func (e *AggregateLimitExceededError) Error() string {
	return fmt.Sprintf(
		"rpc: aggregate fetch limit exceeded: read %s across %d requests, limit %s — "+
			"reduce the total number of operations or raise the limit with --rpc-aggregate-limit",
		formatBytes(e.TotalBytesRead),
		e.RequestCount,
		formatBytes(e.AggregateLimit),
	)
}

// ToJSON returns a compact JSON representation of the error.
func (e *AggregateLimitExceededError) ToJSON() ([]byte, error) {
	return json.Marshal(e)
}

// AggregateTracker tracks total bytes fetched across all requests.
type AggregateTracker struct {
	mu               sync.Mutex
	totalBytesRead   int64
	aggregateLimit   int64
	configuredLimit  int64
	requestCount     int
}

// NewAggregateTracker creates a new aggregate tracker with the specified limit.
func NewAggregateTracker(limit int64) *AggregateTracker {
	return &AggregateTracker{
		aggregateLimit:  limit,
		configuredLimit: limit,
	}
}

// RecordRequest records a request of the specified size and checks against the aggregate limit.
func (t *AggregateTracker) RecordRequest(bytesRead int64) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.totalBytesRead += bytesRead
	t.requestCount++

	if t.aggregateLimit > 0 && t.totalBytesRead > t.aggregateLimit {
		return &AggregateLimitExceededError{
			Code:             ErrCodeAggregateLimitExceeded,
			TotalBytesRead:  t.totalBytesRead,
			AggregateLimit:  t.aggregateLimit,
			ConfiguredLimit: t.configuredLimit,
			RequestCount:    t.requestCount,
		}
	}

	return nil
}

// GetStats returns the current aggregate statistics.
func (t *AggregateTracker) GetStats() (totalBytes int64, requestCount int, limit int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.totalBytesRead, t.requestCount, t.aggregateLimit
}

// BoundedReader wraps an io.Reader and returns a PayloadTooLargeError once
// more than limit bytes have been consumed. A limit of 0 disables enforcement.
type BoundedReader struct {
	r     io.Reader
	limit int64
	n     int64 // total bytes consumed so far
	op    string
	tracker *AggregateTracker
}

// NewBoundedReader returns a BoundedReader that caps reads at limit bytes.
// op is embedded in the error message for diagnostics.
func NewBoundedReader(r io.Reader, limit int64, op string) *BoundedReader {
	return &BoundedReader{r: r, limit: limit, op: op}
}

// NewBoundedReaderWithTracker returns a BoundedReader with aggregate tracking.
func NewBoundedReaderWithTracker(r io.Reader, limit int64, op string, tracker *AggregateTracker) *BoundedReader {
	return &BoundedReader{r: r, limit: limit, op: op, tracker: tracker}
}

// Read implements io.Reader. It returns PayloadTooLargeError as soon as the
// cumulative byte count exceeds limit.
func (b *BoundedReader) Read(p []byte) (int, error) {
	if b.limit > 0 && b.n >= b.limit {
		return 0, &PayloadTooLargeError{
			Code:        ErrCodePayloadTooLarge,
			Operation:  b.op,
			ReadBytes:  b.n,
			Limit:      b.limit,
			Configured: b.limit,
		}
	}
	// Clamp the slice so a single Read can never exceed limit+1 bytes.
	if b.limit > 0 {
		if remaining := b.limit - b.n + 1; int64(len(p)) > remaining {
			p = p[:remaining]
		}
	}
	nn, err := b.r.Read(p)
	b.n += int64(nn)
	if b.limit > 0 && b.n > b.limit {
		return nn, &PayloadTooLargeError{
			Code:        ErrCodePayloadTooLarge,
			Operation:  b.op,
			ReadBytes:  b.n,
			Limit:      b.limit,
			Configured: b.limit,
		}
	}
	return nn, err
}

// BytesRead returns the total number of bytes consumed so far.
func (b *BoundedReader) BytesRead() int64 { return b.n }

// ReadBounded reads all bytes from r up to limit, returning PayloadTooLargeError
// when the body exceeds the limit. op is included in the error for diagnostics.
// A limit ≤ 0 is treated as unlimited (equivalent to io.ReadAll).
func ReadBounded(r io.Reader, limit int64, op string) ([]byte, error) {
	if limit <= 0 {
		return io.ReadAll(r)
	}
	return io.ReadAll(NewBoundedReader(r, limit, op))
}

// ReadBoundedWithTracker reads all bytes from r up to limit with aggregate tracking.
func ReadBoundedWithTracker(r io.Reader, limit int64, op string, tracker *AggregateTracker) ([]byte, error) {
	if limit <= 0 {
		data, err := io.ReadAll(r)
		if err == nil && tracker != nil {
			if trackerErr := tracker.RecordRequest(int64(len(data))); trackerErr != nil {
				return nil, trackerErr
			}
		}
		return data, err
	}
	
	br := NewBoundedReaderWithTracker(r, limit, op, tracker)
	data, err := io.ReadAll(br)
	
	if err == nil && tracker != nil {
		if trackerErr := tracker.RecordRequest(br.BytesRead()); trackerErr != nil {
			return nil, trackerErr
		}
	}
	
	return data, err
}

// formatBytes formats a byte count as a human-readable string.
func formatBytes(n int64) string {
	const kib = 1024
	const mib = 1024 * kib
	const gib = 1024 * mib
	switch {
	case n >= gib:
		return fmt.Sprintf("%.2f GiB (%d bytes)", float64(n)/float64(gib), n)
	case n >= mib:
		return fmt.Sprintf("%.2f MiB (%d bytes)", float64(n)/float64(mib), n)
	case n >= kib:
		return fmt.Sprintf("%.2f KiB (%d bytes)", float64(n)/float64(kib), n)
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}

// readResponseBody reads resp.Body up to the client's configured response
// payload limit. The operation name is included in any oversize error.
func (c *Client) readResponseBody(body io.Reader, op string) ([]byte, error) {
	limit := c.ResponsePayloadLimit
	if limit <= 0 {
		limit = DefaultResponsePayloadLimit
	}
	
	// Use aggregate tracker if available
	var tracker *AggregateTracker
	if c.aggregateTracker != nil {
		tracker = c.aggregateTracker
	}
	
	return ReadBoundedWithTracker(body, limit, op, tracker)
}
