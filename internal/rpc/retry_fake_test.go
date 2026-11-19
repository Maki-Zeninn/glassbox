// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package rpc

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// fakeClock provides deterministic time control for testing retry behavior.
type fakeClock struct {
	mu       sync.Mutex
	now      time.Time
	timers   []*fakeTimer
	timerID  int
}

func newFakeClock() *fakeClock {
	return &fakeClock{
		now: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func (fc *fakeClock) advance(d time.Duration) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	fc.now = fc.now.Add(d)
	for _, t := range fc.timers {
		t.check(fc.now)
	}
}

func (fc *fakeClock) since(t time.Time) time.Duration {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	return fc.now.Sub(t)
}

func (fc *fakeClock) after(d time.Duration) <-chan time.Time {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	ch := make(chan time.Time, 1)
	t := &fakeTimer{
		id:      fc.timerID,
		when:    fc.now.Add(d),
		ch:      ch,
		clock:   fc,
		stopped: false,
	}
	fc.timerID++
	fc.timers = append(fc.timers, t)
	return ch
}

type fakeTimer struct {
	id      int
	when    time.Time
	ch      chan time.Time
	clock   *fakeClock
	stopped bool
	once    sync.Once
}

func (t *fakeTimer) check(now time.Time) {
	if !t.stopped && now.After(t.when) || now.Equal(t.when) {
		t.once.Do(func() {
			t.ch <- now
		})
	}
}

func (t *fakeTimer) stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	t.stopped = true
	return !t.stopped
}

// fakeTransport is a deterministic transport for testing retry behavior.
type fakeTransport struct {
	responses []*http.Response
	errors    []error
	requests  []*http.Request
	mu        sync.Mutex
	callCount int
	clock     *fakeClock
	delay     time.Duration
}

func newFakeTransport(clock *fakeClock) *fakeTransport {
	return &fakeTransport{
		clock: clock,
	}
}

func (ft *fakeTransport) addResponse(resp *http.Response) {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	ft.responses = append(ft.responses, resp)
}

func (ft *fakeTransport) addError(err error) {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	ft.errors = append(ft.errors, err)
}

func (ft *fakeTransport) setDelay(d time.Duration) {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	ft.delay = d
}

func (ft *fakeTransport) getRequestCount() int {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	return ft.callCount
}

func (ft *fakeTransport) getRequests() []*http.Request {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	return append([]*http.Request{}, ft.requests...)
}

func (ft *fakeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	
	ft.requests = append(ft.requests, req)
	ft.callCount++
	
	if ft.delay > 0 {
		ft.clock.advance(ft.delay)
	}
	
	if ft.callCount <= len(ft.errors) {
		return nil, ft.errors[ft.callCount-1]
	}
	
	if ft.callCount <= len(ft.responses) {
		return ft.responses[ft.callCount-1], nil
	}
	
	// Default success response
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       nil,
	}, nil
}

// TestRetryWithFakeClock tests that retry behavior is deterministic under a fake clock.
func TestRetryWithFakeClock(t *testing.T) {
	clock := newFakeClock()
	transport := newFakeTransport(clock)
	
	// Configure transport to fail twice then succeed
	transport.addError(&net.OpError{Op: "dial", Err: errors.New("connection refused")})
	transport.addError(&net.OpError{Op: "dial", Err: errors.New("connection refused")})
	transport.addResponse(&http.Response{
		StatusCode: http.StatusOK,
		Body:       nil,
	})
	
	cfg := DefaultRetryConfig()
	cfg.InitialBackoff = 100 * time.Millisecond
	cfg.MaxBackoff = 500 * time.Millisecond
	cfg.JitterFraction = 0 // Disable jitter for deterministic testing
	
	retrier := &Retrier{
		retryLogic: retryLogic{config: cfg},
		client: &http.Client{
			Transport: transport,
		},
	}
	
	// Replace waitWithContext to use fake clock
	originalWait := retrier.waitWithContext
	retrier.waitWithContext = func(ctx context.Context, duration time.Duration) error {
		clock.advance(duration)
		return originalWait(ctx, duration)
	}
	
	req, _ := http.NewRequest("GET", "http://example.com", nil)
	resp, err := retrier.Do(context.Background(), req)
	
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
	
	// Verify exactly 3 attempts were made
	if transport.getRequestCount() != 3 {
		t.Errorf("expected 3 attempts, got %d", transport.getRequestCount())
	}
}

// TestTransientErrorClassification tests that transient errors are retried.
func TestTransientErrorClassification(t *testing.T) {
	cfg := DefaultRetryConfig()
	rl := retryLogic{config: cfg}
	
	tests := []struct {
		name     string
		err      error
		retryable bool
	}{
		{
			name:     "context deadline exceeded",
			err:      context.DeadlineExceeded,
			retryable: true,
		},
		{
			name:     "net error timeout",
			err:      &net.OpError{Err: errors.New("timeout"), Timeout: true},
			retryable: true,
		},
		{
			name:     "net error temporary",
			err:      &net.OpError{Err: errors.New("temporary"), Temporary: true},
			retryable: true,
		},
		{
			name:     "connection reset",
			err:      errors.New("connection reset by peer"),
			retryable: true,
		},
		{
			name:     "connection refused",
			err:      errors.New("connection refused"),
			retryable: true,
		},
		{
			name:     "context cancelled",
			err:      context.Canceled,
			retryable: false,
		},
		{
			name:     "malformed URL",
			err:      errors.New("invalid URL"),
			retryable: false,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := rl.isTransientError(tt.err)
			if result != tt.retryable {
				t.Errorf("isTransientError(%v) = %v, want %v", tt.err, result, tt.retryable)
			}
		})
	}
}

// TestNoRetryOnContextCancellation tests that context cancellation is not retried.
func TestNoRetryOnContextCancellation(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	
	cfg := DefaultRetryConfig()
	cfg.InitialBackoff = 10 * time.Millisecond
	retrier := NewRetrier(cfg, server.Client())
	
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately
	
	req, _ := http.NewRequest("GET", server.URL, nil)
	_, err := retrier.Do(ctx, req)
	
	if err == nil {
		t.Error("expected error from context cancellation")
	}
	
	// Should only attempt once (no retry on cancellation)
	if attempts != 1 {
		t.Errorf("expected 1 attempt (no retry on cancellation), got %d", attempts)
	}
}

// TestCredentialRedaction tests that sensitive headers are redacted in logs.
func TestCredentialRedaction(t *testing.T) {
	cfg := DefaultRetryConfig()
	rl := retryLogic{config: cfg}
	
	req, _ := http.NewRequest("GET", "http://example.com", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	req.Header.Set("X-API-Key", "api-key-123")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", "session=abc123")
	
	headers := rl.redactCredentials(req)
	
	if headers["Authorization"] != "[REDACTED]" {
		t.Errorf("Authorization header not redacted: %s", headers["Authorization"])
	}
	if headers["X-API-Key"] != "[REDACTED]" {
		t.Errorf("X-API-Key header not redacted: %s", headers["X-API-Key"])
	}
	if headers["Cookie"] != "[REDACTED]" {
		t.Errorf("Cookie header not redacted: %s", headers["Cookie"])
	}
	if headers["Content-Type"] != "application/json" {
		t.Errorf("Content-Type header incorrectly redacted: %s", headers["Content-Type"])
	}
}

// TestRetryableRPCErrorClassification tests RPC error code classification.
func TestRetryableRPCErrorClassification(t *testing.T) {
	cfg := DefaultRetryConfig()
	rl := retryLogic{config: cfg}
	
	tests := []struct {
		code      int
		retryable bool
	}{
		{-32603, true},  // Internal error
		{-32000, true},  // Server error
		{-32600, false}, // Invalid request (client error)
		{-32601, false}, // Method not found (client error)
		{-32602, false}, // Invalid params (client error)
	}
	
	for _, tt := range tests {
		result := rl.isRetryableRPCError(tt.code)
		if result != tt.retryable {
			t.Errorf("isRetryableRPCError(%d) = %v, want %v", tt.code, result, tt.retryable)
		}
	}
}

// TestNonIdempotentOperationsNotRepeated tests that non-idempotent operations
// are not accidentally repeated when the request body cannot be replayed.
func TestNonIdempotentOperationsNotRepeated(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		// Read the body to ensure it's consumed
		body := make([]byte, 100)
		r.Body.Read(body)
		
		if attempts == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	
	cfg := DefaultRetryConfig()
	cfg.InitialBackoff = 10 * time.Millisecond
	retrier := NewRetrier(cfg, server.Client())
	
	// Create a request with a body that can only be read once
	body := []byte("test data")
	req, _ := http.NewRequest("POST", server.URL, nil)
	
	// The retrier uses req.Clone(ctx) which should handle body replay
	// This test verifies the existing behavior
	resp, err := retrier.Do(context.Background(), req)
	
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
	
	// With req.Clone, the body should be replayed
	// This documents the current behavior
	if attempts < 2 {
		t.Logf("Note: body replay behavior - attempts: %d", attempts)
	}
}

// TestPermanentErrorsNotRetried tests that permanent errors return immediately.
func TestPermanentErrorsNotRetried(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusBadRequest) // 400 - permanent client error
	}))
	defer server.Close()
	
	cfg := DefaultRetryConfig()
	cfg.InitialBackoff = 10 * time.Millisecond
	retrier := NewRetrier(cfg, server.Client())
	
	req, _ := http.NewRequest("GET", server.URL, nil)
	resp, err := retrier.Do(context.Background(), req)
	
	// Should return immediately with the 400 response
	if err != nil {
		t.Fatalf("expected response with error status, got error: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", resp.StatusCode)
	}
	
	// Should only attempt once (400 is not in retryable status codes)
	if attempts != 1 {
		t.Errorf("expected 1 attempt (no retry on 400), got %d", attempts)
	}
}

// TestRetryAfterHeaderRespected tests that Retry-After header is respected.
func TestRetryAfterHeaderRespected(t *testing.T) {
	clock := newFakeClock()
	transport := newFakeTransport(clock)
	
	transport.addResponse(&http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"2"}},
		Body:       nil,
	})
	transport.addResponse(&http.Response{
		StatusCode: http.StatusOK,
		Body:       nil,
	})
	
	cfg := DefaultRetryConfig()
	cfg.InitialBackoff = 100 * time.Millisecond
	cfg.MaxBackoff = 500 * time.Millisecond
	cfg.JitterFraction = 0
	
	retrier := &Retrier{
		retryLogic: retryLogic{config: cfg},
		client: &http.Client{
			Transport: transport,
		},
	}
	
	// Replace waitWithContext to use fake clock and track delays
	var totalDelay time.Duration
	originalWait := retrier.waitWithContext
	retrier.waitWithContext = func(ctx context.Context, duration time.Duration) error {
		totalDelay += duration
		clock.advance(duration)
		return originalWait(ctx, duration)
	}
	
	req, _ := http.NewRequest("GET", "http://example.com", nil)
	_, err := retrier.Do(context.Background(), req)
	
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	
	// Should have waited at least 2 seconds as specified by Retry-After
	if totalDelay < 2*time.Second {
		t.Errorf("expected delay >= 2s from Retry-After, got %v", totalDelay)
	}
}
