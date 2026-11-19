// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package rpc

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dotandev/glassbox/internal/errors"
	"github.com/dotandev/glassbox/internal/logger"
	"github.com/dotandev/glassbox/internal/telemetry"
)

// RetryConfig defines the retry behavior
type RetryConfig struct {
	MaxRetries         int
	InitialBackoff     time.Duration
	MaxBackoff         time.Duration
	JitterFraction     float64
	StatusCodesToRetry []int
	// RetryableRPCErrors contains JSON-RPC error codes that should trigger a retry
	RetryableRPCErrors []int
}

// DefaultRetryConfig returns a sensible default retry configuration
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:         3,
		InitialBackoff:     1 * time.Second,
		MaxBackoff:         10 * time.Second,
		JitterFraction:     0.1, // 10% jitter to prevent thundering herd
		StatusCodesToRetry: []int{408, 429, 500, 502, 503, 504},
		// Common JSON-RPC error codes that may be transient
		// -32603: Internal error (server-side)
		// -32000 to -32099: Server error range
		RetryableRPCErrors: []int{-32603, -32000, -32001, -32002, -32003, -32004, -32005},
	}
}

// retryLogic holds the shared retry behavior used by both Retrier and RetryTransport.
// Embedding this struct in either type promotes its methods, eliminating duplicated code
// while keeping each type's transport/client wiring independent.
type retryLogic struct {
	config RetryConfig
}

// isTransientError reports whether the error is a transient network error that should be retried.
// Transient errors include: timeouts, connection resets, temporary DNS failures, and context deadlines.
// Permanent errors include: context cancellation, malformed URLs, and certificate errors.
func (rl retryLogic) isTransientError(err error) bool {
	if err == nil {
		return false
	}

	// Context cancellation is not transient - user explicitly cancelled
	if err == context.Canceled {
		return false
	}

	// Context deadline exceeded is transient - timeout that might succeed on retry
	if err == context.DeadlineExceeded {
		return true
	}

	// Network operation timeout
	if netErr, ok := err.(net.Error); ok {
		if netErr.Timeout() {
			return true
		}
		// Temporary network errors (e.g., connection reset, DNS failure)
		if netErr.Temporary() {
			return true
		}
	}

	// Check for specific error strings that indicate transient failures
	errMsg := strings.ToLower(err.Error())
	transientPatterns := []string{
		"connection reset",
		"connection refused",
		"broken pipe",
		"eof",
		"temporary failure",
		"try again",
	}

	for _, pattern := range transientPatterns {
		if strings.Contains(errMsg, pattern) {
			return true
		}
	}

	return false
}

// shouldRetry reports whether the given HTTP status code warrants a retry.
func (rl retryLogic) shouldRetry(statusCode int) bool {
	for _, code := range rl.config.StatusCodesToRetry {
		if statusCode == code {
			return true
		}
	}
	return false
}

// isRetryableRPCError checks if a JSON-RPC error code indicates a transient failure.
// This is used when parsing RPC error payloads from response bodies.
func (rl retryLogic) isRetryableRPCError(code int) bool {
	for _, retryableCode := range rl.config.RetryableRPCErrors {
		if code == retryableCode {
			return true
		}
	}
	return false
}

// getRetryAfter parses the Retry-After response header.
// Supports both integer-seconds and RFC 1123 HTTP-date formats (RFC 7231 §7.1.3).
func (rl retryLogic) getRetryAfter(resp *http.Response) time.Duration {
	retryAfter := resp.Header.Get("Retry-After")
	if retryAfter == "" {
		return 0
	}

	// Try parsing as seconds (integer)
	if seconds, err := strconv.Atoi(retryAfter); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}

	// Try parsing as HTTP-date
	if t, err := time.Parse(time.RFC1123, retryAfter); err == nil {
		dur := time.Until(t)
		if dur > 0 {
			return dur
		}
	}

	return 0
}

// nextBackoff calculates the next wait duration using exponential backoff with full jitter.
// Full jitter prevents thundering-herd problems when many clients retry simultaneously.
func (rl retryLogic) nextBackoff(current time.Duration) time.Duration {
	// Exponential backoff: double the current duration, capped at MaxBackoff
	next := time.Duration(float64(current) * 2)
	if next > rl.config.MaxBackoff {
		next = rl.config.MaxBackoff
	}

	// Full jitter: random value in [0, next*(1+JitterFraction))
	if rl.config.JitterFraction > 0 {
		maxJitter := float64(next) * (1.0 + rl.config.JitterFraction)
		jitter := time.Duration(rand.Float64() * maxJitter)
		next = jitter
		if next < 0 {
			next = 0
		}
	}

	return next
}

// waitWithContext sleeps for duration or returns early when ctx is cancelled.
func (rl retryLogic) waitWithContext(ctx context.Context, duration time.Duration) error {
	select {
	case <-time.After(duration):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// redactCredentials removes sensitive information from request headers for logging.
func (rl retryLogic) redactCredentials(req *http.Request) map[string]string {
	headers := make(map[string]string)
	for k, v := range req.Header {
		// Redact sensitive headers
		if strings.EqualFold(k, "authorization") ||
			strings.EqualFold(k, "proxy-authorization") ||
			strings.EqualFold(k, "cookie") ||
			strings.EqualFold(k, "set-cookie") ||
			strings.EqualFold(k, "x-api-key") ||
			strings.EqualFold(k, "x-auth-token") {
			headers[k] = "[REDACTED]"
		} else {
			headers[k] = strings.Join(v, ", ")
		}
	}
	return headers
}

// logRequestIdentity logs request identity information without exposing credentials.
func (rl retryLogic) logRequestIdentity(req *http.Request, attempt int, corrID string) {
	headers := rl.redactCredentials(req)
	logger.Logger.Debug("Request attempt",
		"attempt", attempt,
		"method", req.Method,
		"url", req.URL.Redacted(),
		"correlation_id", corrID,
		"headers", headers,
	)
}

// Retrier handles HTTP request retries with exponential backoff and jitter.
type Retrier struct {
	retryLogic
	client *http.Client
}

// NewRetrier creates a new Retrier with the given config and HTTP client.
func NewRetrier(config RetryConfig, client *http.Client) *Retrier {
	if client == nil {
		client = http.DefaultClient
	}
	return &Retrier{
		retryLogic: retryLogic{config: config},
		client:     client,
	}
}

// Do executes an HTTP request with retry logic.
func (r *Retrier) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	var lastErr error
	backoff := r.config.InitialBackoff

	for attempt := 0; attempt <= r.config.MaxRetries; attempt++ {
		if attempt > 0 {
			if err := r.waitWithContext(ctx, backoff); err != nil {
				return nil, errors.WrapRPCTimeout(err)
			}
		}

		resp, err := r.client.Do(req.Clone(ctx))
		corrID := telemetry.CorrelationIDFromContext(ctx)
		
		// Log request identity on each attempt for diagnostics
		if attempt > 0 || err != nil {
			r.logRequestIdentity(req, attempt+1, corrID)
		}
		
		if err != nil {
			lastErr = err
			// Check if error is transient before retrying
			if !r.isTransientError(err) {
				return nil, err
			}
			if attempt < r.config.MaxRetries {
				debugArgs := []interface{}{"attempt", attempt + 1, "error", err}
				if corrID != "" {
					debugArgs = append(debugArgs, "correlation_id", corrID)
				}
				logger.Logger.Debug("Request failed, will retry", debugArgs...)
			}
			backoff = r.nextBackoff(backoff)
			continue
		}

		// HTTP 413: response too large -- not retryable
		if resp.StatusCode == http.StatusRequestEntityTooLarge {
			_ = resp.Body.Close()
			return nil, errors.WrapRPCResponseTooLarge(req.URL.String())
		}

		// Check if response status is retryable
		if r.shouldRetry(resp.StatusCode) {
			lastErr = fmt.Errorf("status code %d", resp.StatusCode)
			retryAfter := r.getRetryAfter(resp)

			warnArgs := []interface{}{
				"attempt", attempt + 1,
				"status_code", resp.StatusCode,
				"retry_after", retryAfter,
			}
			if corrID != "" {
				warnArgs = append(warnArgs, "correlation_id", corrID)
			}
			logger.Logger.Warn("Rate limited or temporary failure, will retry", warnArgs...)

			_ = resp.Body.Close()

			if retryAfter > 0 {
				backoff = retryAfter
			} else {
				backoff = r.nextBackoff(backoff)
			}

			if attempt < r.config.MaxRetries {
				continue
			}
			// If we've exhausted retries on a retryable error, return error
			return nil, errors.WrapRPCConnectionFailed(lastErr)
		}

		// Success or non-retryable error
		return resp, nil
	}

	return nil, errors.WrapRPCConnectionFailed(lastErr)
}

// RetryTransport is an http.RoundTripper that adds retry logic to every request.
type RetryTransport struct {
	retryLogic
	transport http.RoundTripper
}

// NewRetryTransport creates a new RetryTransport with the given config.
func NewRetryTransport(config RetryConfig, transport http.RoundTripper) *RetryTransport {
	if transport == nil {
		transport = http.DefaultTransport
	}
	return &RetryTransport{
		retryLogic: retryLogic{config: config},
		transport:  transport,
	}
}

// RoundTrip implements http.RoundTripper with retry logic.
func (rt *RetryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var lastErr error
	backoff := rt.config.InitialBackoff

	for attempt := 0; attempt <= rt.config.MaxRetries; attempt++ {
		if attempt > 0 {
			if err := rt.waitWithContext(req.Context(), backoff); err != nil {
				return nil, errors.WrapRPCTimeout(err)
			}
		}

		resp, err := rt.transport.RoundTrip(req)
		corrID := telemetry.CorrelationIDFromContext(req.Context())
		
		// Log request identity on each attempt for diagnostics
		if attempt > 0 || err != nil {
			rt.logRequestIdentity(req, attempt+1, corrID)
		}
		
		if err != nil {
			lastErr = err
			// Check if error is transient before retrying
			if !rt.isTransientError(err) {
				return nil, err
			}
			if attempt < rt.config.MaxRetries {
				debugArgs := []interface{}{"attempt", attempt + 1, "error", err}
				if corrID != "" {
					debugArgs = append(debugArgs, "correlation_id", corrID)
				}
				logger.Logger.Debug("RoundTrip failed, will retry", debugArgs...)
			}
			backoff = rt.nextBackoff(backoff)
			continue
		}

		// HTTP 413: response too large -- not retryable
		if resp.StatusCode == http.StatusRequestEntityTooLarge {
			_ = resp.Body.Close()
			return nil, errors.WrapRPCResponseTooLarge(req.URL.String())
		}

		// Check if response status is retryable
		if rt.shouldRetry(resp.StatusCode) {
			lastErr = fmt.Errorf("status code %d", resp.StatusCode)
			retryAfter := rt.getRetryAfter(resp)

			warnArgs := []interface{}{
				"attempt", attempt + 1,
				"status_code", resp.StatusCode,
				"retry_after", retryAfter,
			}
			if corrID != "" {
				warnArgs = append(warnArgs, "correlation_id", corrID)
			}
			logger.Logger.Warn("Rate limited or temporary failure, will retry", warnArgs...)

			_ = resp.Body.Close()

			if retryAfter > 0 {
				backoff = retryAfter
			} else {
				backoff = rt.nextBackoff(backoff)
			}

			if attempt < rt.config.MaxRetries {
				continue
			}
			// If we've exhausted retries on a retryable error, return error
			return nil, errors.WrapRPCConnectionFailed(lastErr)
		}

		// Success or non-retryable error
		return resp, nil
	}

	return nil, errors.WrapRPCConnectionFailed(lastErr)
}
