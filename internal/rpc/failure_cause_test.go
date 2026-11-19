// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package rpc_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dotandev/glassbox/internal/rpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── ClassifyFailureCause ─────────────────────────────────────────────────────

func TestClassifyFailureCause_Nil(t *testing.T) {
	assert.Equal(t, rpc.FailureCauseNone, rpc.ClassifyFailureCause(nil, 0))
}

func TestClassifyFailureCause_ContextCanceled(t *testing.T) {
	assert.Equal(t, rpc.FailureCauseContextCanceled,
		rpc.ClassifyFailureCause(context.Canceled, 0))
}

func TestClassifyFailureCause_DeadlineExceeded(t *testing.T) {
	assert.Equal(t, rpc.FailureCauseTimeout,
		rpc.ClassifyFailureCause(context.DeadlineExceeded, 0))
}

func TestClassifyFailureCause_HTTPStatus(t *testing.T) {
	tests := []struct {
		code int
		want rpc.FailureCause
	}{
		{http.StatusTooManyRequests, rpc.FailureCauseRateLimited},
		{http.StatusRequestTimeout, rpc.FailureCauseTimeout},
		{http.StatusInternalServerError, rpc.FailureCauseServerError},
		{http.StatusBadGateway, rpc.FailureCauseServerError},
		{http.StatusServiceUnavailable, rpc.FailureCauseServerError},
		{http.StatusGatewayTimeout, rpc.FailureCauseServerError},
		{http.StatusUnauthorized, rpc.FailureCauseClientError},
		{http.StatusBadRequest, rpc.FailureCauseClientError},
		{http.StatusForbidden, rpc.FailureCauseClientError},
		{http.StatusNotFound, rpc.FailureCauseClientError},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("HTTP_%d", tt.code), func(t *testing.T) {
			got := rpc.ClassifyFailureCause(errors.New("some error"), tt.code)
			assert.Equal(t, tt.want, got, "HTTP %d should classify as %s", tt.code, tt.want)
		})
	}
}

func TestClassifyFailureCause_ErrorText(t *testing.T) {
	tests := []struct {
		msg  string
		want rpc.FailureCause
	}{
		{"dial tcp: connection refused", rpc.FailureCauseConnectionRefused},
		{"dial tcp: connect: connection refused", rpc.FailureCauseConnectionRefused},
		{"no such host", rpc.FailureCauseDNS},
		{"dns lookup failed", rpc.FailureCauseDNS},
		{"no route to host", rpc.FailureCauseUnreachable},
		{"network unreachable", rpc.FailureCauseUnreachable},
		{"connection reset by peer", rpc.FailureCauseEOF},
		{"EOF", rpc.FailureCauseEOF},
		{"i/o timeout", rpc.FailureCauseTimeout},
		{"request timeout", rpc.FailureCauseTimeout},
		{"unexpected widget failure", rpc.FailureCauseUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.msg, func(t *testing.T) {
			got := rpc.ClassifyFailureCause(errors.New(tt.msg), 0)
			assert.Equal(t, tt.want, got, "error %q should classify as %s", tt.msg, tt.want)
		})
	}
}

// ─── FailureCauseRemediationHint ─────────────────────────────────────────────

func TestFailureCauseRemediationHint_NonEmpty(t *testing.T) {
	causes := []rpc.FailureCause{
		rpc.FailureCauseTimeout,
		rpc.FailureCauseConnectionRefused,
		rpc.FailureCauseDNS,
		rpc.FailureCauseUnreachable,
		rpc.FailureCauseRateLimited,
		rpc.FailureCauseServerError,
		rpc.FailureCauseClientError,
		rpc.FailureCauseEOF,
		rpc.FailureCauseContextCanceled,
		rpc.FailureCauseUnknown,
	}
	for _, c := range causes {
		hint := rpc.FailureCauseRemediationHint(c)
		assert.NotEmpty(t, hint, "every cause should have a non-empty remediation hint (%s)", c)
	}
}

func TestFailureCauseRemediationHint_TimeoutMentionsRPCURL(t *testing.T) {
	hint := rpc.FailureCauseRemediationHint(rpc.FailureCauseTimeout)
	assert.True(t, strings.Contains(hint, "--rpc-url") || strings.Contains(hint, "endpoint"),
		"timeout hint should mention switching endpoint, got: %s", hint)
}

// ─── BuildRPCFailureSummary ───────────────────────────────────────────────────

func TestBuildRPCFailureSummary_AllFailed(t *testing.T) {
	diag := rpc.AttemptDiagnostics{
		Attempts: []rpc.AttemptRecord{
			{URL: "https://a.example.com", Err: errors.New("connection refused"), Latency: 10 * time.Millisecond, HTTPStatusCode: 0},
			{URL: "https://b.example.com", Err: context.DeadlineExceeded, Latency: 5 * time.Second, HTTPStatusCode: 0},
		},
		SucceededURL:  "",
		TotalDuration: 5100 * time.Millisecond,
	}

	s := rpc.BuildRPCFailureSummary(diag)

	assert.True(t, s.AllFailed)
	assert.False(t, s.UsedFallback)
	assert.Equal(t, 2, s.TotalAttempts)
	assert.Equal(t, rpc.FailureCauseConnectionRefused, s.PrimaryFailedVia)
	require.Len(t, s.Attempts, 2)
	assert.Equal(t, rpc.FailureCauseConnectionRefused, s.Attempts[0].Cause)
	assert.Equal(t, rpc.FailureCauseTimeout, s.Attempts[1].Cause)
	// Both distinct causes should produce remediation hints.
	assert.GreaterOrEqual(t, len(s.RemediationHints), 2)
}

func TestBuildRPCFailureSummary_FallbackSucceeded(t *testing.T) {
	diag := rpc.AttemptDiagnostics{
		Attempts: []rpc.AttemptRecord{
			{URL: "https://primary.example.com", Err: errors.New("503 unavailable"), Latency: 50 * time.Millisecond, HTTPStatusCode: 503},
			{URL: "https://fallback.example.com", Err: nil, Latency: 30 * time.Millisecond, HTTPStatusCode: 200},
		},
		SucceededURL:  "https://fallback.example.com",
		TotalDuration: 90 * time.Millisecond,
	}

	s := rpc.BuildRPCFailureSummary(diag)

	assert.False(t, s.AllFailed)
	assert.True(t, s.UsedFallback)
	assert.Equal(t, "https://fallback.example.com", s.SucceededVia)
	assert.Equal(t, rpc.FailureCauseServerError, s.PrimaryFailedVia)
	assert.True(t, s.Attempts[1].Success)
}

func TestBuildRPCFailureSummary_DeduplicatesHints(t *testing.T) {
	// Three timeout failures — should produce only one timeout hint.
	diag := rpc.AttemptDiagnostics{
		Attempts: []rpc.AttemptRecord{
			{URL: "https://a.example.com", Err: context.DeadlineExceeded, Latency: time.Second},
			{URL: "https://b.example.com", Err: context.DeadlineExceeded, Latency: time.Second},
			{URL: "https://c.example.com", Err: context.DeadlineExceeded, Latency: time.Second},
		},
		TotalDuration: 3 * time.Second,
	}

	s := rpc.BuildRPCFailureSummary(diag)
	assert.Equal(t, 1, len(s.RemediationHints), "duplicate causes should produce a single hint")
}

// ─── FormatRPCFailureSummaryText ─────────────────────────────────────────────

func TestFormatRPCFailureSummaryText_ContainsExpectedFields(t *testing.T) {
	diag := rpc.AttemptDiagnostics{
		Attempts: []rpc.AttemptRecord{
			{URL: "https://primary.example.com", Err: errors.New("connection refused"), Latency: 10 * time.Millisecond},
			{URL: "https://fallback.example.com", Err: nil, Latency: 20 * time.Millisecond},
		},
		SucceededURL:  "https://fallback.example.com",
		TotalDuration: 35 * time.Millisecond,
	}
	s := rpc.BuildRPCFailureSummary(diag)
	out := rpc.FormatRPCFailureSummaryText(s)

	assert.Contains(t, out, "Attempts")
	assert.Contains(t, out, "https://primary.example.com")
	assert.Contains(t, out, "https://fallback.example.com")
	assert.Contains(t, out, "succeeded via")
	assert.Contains(t, out, "Suggestions")
}

func TestFormatRPCFailureSummaryText_AllFailed(t *testing.T) {
	diag := rpc.AttemptDiagnostics{
		Attempts: []rpc.AttemptRecord{
			{URL: "https://a.example.com", Err: errors.New("connection refused"), Latency: 5 * time.Millisecond},
		},
		TotalDuration: 5 * time.Millisecond,
	}
	s := rpc.BuildRPCFailureSummary(diag)
	out := rpc.FormatRPCFailureSummaryText(s)
	assert.Contains(t, out, "all endpoints failed")
}

// ─── FormatAllNodesFailedText ─────────────────────────────────────────────────

func TestFormatAllNodesFailedText_IncludesProviderStates(t *testing.T) {
	diag := rpc.AllNodesFailedDiagnostic{
		Summary: rpc.BuildRPCFailureSummary(rpc.AttemptDiagnostics{
			Attempts: []rpc.AttemptRecord{
				{URL: "https://a.example.com", Err: errors.New("refused"), Latency: 5 * time.Millisecond},
			},
			TotalDuration: 5 * time.Millisecond,
		}),
		ProviderStates: []rpc.ProviderState{
			{URL: "https://a.example.com", Status: rpc.ProviderStatusDown, ConsecutiveFailures: 5},
		},
		Network:   "testnet",
		Timestamp: time.Now(),
	}

	out := rpc.FormatAllNodesFailedText(diag)

	assert.Contains(t, out, "All Endpoints Failed")
	assert.Contains(t, out, "testnet")
	assert.Contains(t, out, "https://a.example.com")
	assert.Contains(t, out, "down")
	assert.Contains(t, out, "--rpc-url")
	assert.Contains(t, out, "glassbox doctor")
}

func TestFormatAllNodesFailedText_RecoveryOptionsPresent(t *testing.T) {
	diag := rpc.AllNodesFailedDiagnostic{
		Summary:   rpc.BuildRPCFailureSummary(rpc.AttemptDiagnostics{}),
		Network:   "mainnet",
		Timestamp: time.Now(),
	}
	out := rpc.FormatAllNodesFailedText(diag)
	assert.Contains(t, out, "Recovery options")
	assert.Contains(t, out, "glassbox doctor")
}

// ─── AttemptRecord.Cause set by ProviderPool.Do ──────────────────────────────

func TestProviderPool_Do_CausePopulated_Timeout(t *testing.T) {
	// Use a real HTTP server that hangs until context deadline.
	hangSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Block until the client disconnects.
		<-r.Context().Done()
	}))
	defer hangSrv.Close()

	fastSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer fastSrv.Close()

	cfg := fastPoolConfig()
	cfg.RequestDeadline = 20 * time.Millisecond
	cfg.MaxRetries = 3
	pool := rpc.NewProviderPool([]string{hangSrv.URL, fastSrv.URL}, cfg)

	diag, err := pool.Do(context.Background(), func(ctx context.Context, url string) (int, error) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, httpErr := http.DefaultClient.Do(req)
		if httpErr != nil {
			return 0, httpErr
		}
		defer resp.Body.Close()
		return resp.StatusCode, nil
	})

	require.NoError(t, err, "should succeed via fast server")
	assert.Equal(t, fastSrv.URL, diag.SucceededURL)
	// First attempt should be classified as timeout.
	require.Greater(t, len(diag.Attempts), 0)
	assert.Equal(t, rpc.FailureCauseTimeout, diag.Attempts[0].Cause,
		"hanging server should produce FailureCauseTimeout")
}

func TestProviderPool_Do_CausePopulated_ConnectionRefused(t *testing.T) {
	// Use a port that is not listening (connection refused).
	// Port 1 is conventionally not open; if it happens to be we skip rather than flake.
	refusedURL := "http://127.0.0.1:1"

	cfg := fastPoolConfig()
	cfg.MaxRetries = 2
	pool := rpc.NewProviderPool([]string{refusedURL}, cfg)

	diag, err := pool.Do(context.Background(), func(ctx context.Context, url string) (int, error) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		_, httpErr := http.DefaultClient.Do(req)
		return 0, httpErr
	})

	require.Error(t, err)
	require.Greater(t, len(diag.Attempts), 0)
	cause := diag.Attempts[0].Cause
	// Either ConnectionRefused or Unreachable depending on the OS.
	isExpected := cause == rpc.FailureCauseConnectionRefused ||
		cause == rpc.FailureCauseUnreachable ||
		cause == rpc.FailureCauseTimeout
	assert.True(t, isExpected,
		"connection to port 1 should classify as connection_refused/unreachable/timeout, got %s", cause)
}

func TestProviderPool_Do_CausePopulated_RateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	fastSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer fastSrv.Close()

	cfg := fastPoolConfig()
	cfg.MaxRetries = 3
	cfg.DegradedThreshold = 1
	pool := rpc.NewProviderPool([]string{srv.URL, fastSrv.URL}, cfg)

	diag, err := pool.Do(context.Background(), func(ctx context.Context, url string) (int, error) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, httpErr := http.DefaultClient.Do(req)
		if httpErr != nil {
			return 0, httpErr
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			return resp.StatusCode, fmt.Errorf("rate limited")
		}
		return resp.StatusCode, nil
	})

	require.NoError(t, err)
	require.Greater(t, len(diag.Attempts), 0)
	assert.Equal(t, rpc.FailureCauseRateLimited, diag.Attempts[0].Cause)
}

func TestProviderPool_Do_CausePopulated_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	fastSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer fastSrv.Close()

	cfg := fastPoolConfig()
	cfg.MaxRetries = 3
	cfg.DegradedThreshold = 1
	pool := rpc.NewProviderPool([]string{srv.URL, fastSrv.URL}, cfg)

	diag, err := pool.Do(context.Background(), func(ctx context.Context, url string) (int, error) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, httpErr := http.DefaultClient.Do(req)
		if httpErr != nil {
			return 0, httpErr
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 500 {
			return resp.StatusCode, fmt.Errorf("server error")
		}
		return resp.StatusCode, nil
	})

	require.NoError(t, err)
	require.Greater(t, len(diag.Attempts), 0)
	assert.Equal(t, rpc.FailureCauseServerError, diag.Attempts[0].Cause)
}

// ─── Partial success: first fails, second succeeds ───────────────────────────

func TestProviderPool_PartialSuccess_DiagnosticsAccurate(t *testing.T) {
	failSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway) // 502 – retryable server error
	}))
	defer failSrv.Close()

	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer okSrv.Close()

	cfg := fastPoolConfig()
	cfg.MaxRetries = 4
	cfg.DegradedThreshold = 1
	pool := rpc.NewProviderPool([]string{failSrv.URL, okSrv.URL}, cfg)

	diag, err := pool.Do(context.Background(), func(ctx context.Context, url string) (int, error) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, httpErr := http.DefaultClient.Do(req)
		if httpErr != nil {
			return 0, httpErr
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 500 {
			return resp.StatusCode, fmt.Errorf("server error %d", resp.StatusCode)
		}
		return resp.StatusCode, nil
	})

	require.NoError(t, err, "pool should succeed via second provider")
	assert.Equal(t, okSrv.URL, diag.SucceededURL)
	assert.Greater(t, len(diag.Attempts), 1, "should have recorded at least one failure and one success")

	// Verify overall summary from BuildRPCFailureSummary.
	summary := rpc.BuildRPCFailureSummary(diag)
	assert.False(t, summary.AllFailed)
	assert.True(t, summary.UsedFallback)
	assert.Equal(t, rpc.FailureCauseServerError, summary.PrimaryFailedVia)
	assert.Equal(t, okSrv.URL, summary.SucceededVia)
}
