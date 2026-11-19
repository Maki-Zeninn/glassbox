// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package rpc provides the provider pool, which offers ordered provider
// selection with health tracking, per-request deadlines, a configurable retry
// policy, and explicit failover diagnostics. The pool supports "pinned" mode
// for replay reproducibility, where a single provider is locked and silent
// switching is disabled.
package rpc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/dotandev/glassbox/internal/logger"
)

// ProviderStatus describes the current health state of a single provider endpoint.
type ProviderStatus int

const (
	// ProviderStatusHealthy means the provider is accepting requests.
	ProviderStatusHealthy ProviderStatus = iota
	// ProviderStatusDegraded means the provider has seen recent failures but
	// has not yet been fully evicted; it will still receive traffic at reduced weight.
	ProviderStatusDegraded
	// ProviderStatusDown means the provider has exceeded the failure threshold
	// and the circuit breaker is open.
	ProviderStatusDown
)

// String returns a human-readable label for the status.
func (s ProviderStatus) String() string {
	switch s {
	case ProviderStatusHealthy:
		return "healthy"
	case ProviderStatusDegraded:
		return "degraded"
	case ProviderStatusDown:
		return "down"
	default:
		return "unknown"
	}
}

// ProviderState holds runtime health state for one endpoint in the pool.
type ProviderState struct {
	URL                 string
	Status              ProviderStatus
	ConsecutiveFailures int
	LastFailureAt       time.Time
	LastSuccessAt       time.Time
	TotalRequests       int64
	TotalFailures       int64
}

// AttemptRecord captures the result of a single provider attempt made during
// a failover sequence. It is surfaced to callers as part of AttemptDiagnostics
// so users can see exactly which endpoints were tried and why each failed.
type AttemptRecord struct {
	// URL is the endpoint that was attempted.
	URL string
	// Retryable reports whether the error was classified as transient
	// (i.e. timeout, rate-limit, 5xx) vs. permanent (4xx auth, bad request).
	Retryable bool
	// Err is the raw error from the attempt, or nil on success.
	Err error
	// Latency is the wall-clock time taken by the attempt.
	Latency time.Duration
	// HTTPStatusCode is the HTTP status code returned, or 0 if the request
	// did not reach the server (connection refused, timeout, etc.).
	HTTPStatusCode int
	// Cause is the machine-stable classification of why the attempt failed.
	// It is set by Do() after each failed attempt.
	Cause FailureCause
}

// AttemptDiagnostics holds the full trace of provider attempts made during
// a single pool request, including the winning provider when one succeeded.
type AttemptDiagnostics struct {
	// Attempts is the ordered list of attempts, from first to last.
	Attempts []AttemptRecord
	// SucceededURL is the provider URL that ultimately succeeded, or "" if
	// all providers failed.
	SucceededURL string
	// TotalDuration is the elapsed wall-clock time across all attempts.
	TotalDuration time.Duration
}

// PoolConfig controls the behaviour of the ProviderPool.
type PoolConfig struct {
	// MaxRetries is the total number of per-provider retry attempts before
	// the pool gives up and returns an AllNodesFailedError. Minimum 1.
	// Default: 3.
	MaxRetries int

	// RequestDeadline is the per-attempt context deadline. When set, each
	// individual provider attempt is subject to this timeout independently of
	// the parent context deadline. Zero means no additional deadline.
	RequestDeadline time.Duration

	// InitialBackoff is the starting backoff for exponential retry waits.
	// Default: 500 ms.
	InitialBackoff time.Duration

	// MaxBackoff caps the exponential backoff growth.
	// Default: 10 s.
	MaxBackoff time.Duration

	// DegradedThreshold is the consecutive failure count at which a provider
	// is moved to the Degraded state. Default: 2.
	DegradedThreshold int

	// DownThreshold is the consecutive failure count at which a provider is
	// moved to the Down state. Default: 5.
	DownThreshold int

	// RecoveryInterval is how long a Down provider remains evicted before it
	// is re-admitted for a probe attempt. Default: 30 s.
	RecoveryInterval time.Duration

	// PinnedURL, when non-empty, locks the pool to a single provider.
	// All requests are routed to this URL regardless of health state.
	// Failover / silent switching is disabled; if the pinned provider fails,
	// the error is returned immediately (no fallback).
	// Use this for replay reproducibility.
	PinnedURL string
}

// DefaultPoolConfig returns production-ready defaults for the ProviderPool.
func DefaultPoolConfig() PoolConfig {
	return PoolConfig{
		MaxRetries:        3,
		RequestDeadline:   15 * time.Second,
		InitialBackoff:    500 * time.Millisecond,
		MaxBackoff:        10 * time.Second,
		DegradedThreshold: 2,
		DownThreshold:     5,
		RecoveryInterval:  30 * time.Second,
	}
}

// isRetryableStatusCode returns true for HTTP status codes that indicate a
// transient error and should trigger a retry or failover.
// Non-retryable codes (4xx except 429) are returned to the caller immediately
// without consuming additional provider attempts.
func isRetryableStatusCode(code int) bool {
	switch code {
	case http.StatusTooManyRequests, // 429
		http.StatusRequestTimeout,        // 408
		http.StatusInternalServerError,   // 500
		http.StatusBadGateway,            // 502
		http.StatusServiceUnavailable,    // 503
		http.StatusGatewayTimeout:        // 504
		return true
	}
	return false
}

// isRetryableError returns true for transport-layer errors (connection refused,
// context deadline exceeded, network unreachable, etc.) that should trigger
// failover. It does not treat context cancellation as retryable.
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		// Caller cancelled — respect their intent; do not retry.
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		// Per-attempt deadline or parent deadline exceeded — retryable at pool
		// level if there are remaining providers/attempts and the parent
		// context is still live.
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "i/o timeout") ||
		strings.Contains(msg, "eof") ||
		strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "temporary failure")
}

// ProviderPool maintains an ordered list of provider endpoints with health
// tracking, failover, retry logic, and replay-pinning support.
//
// It is safe for concurrent use.
type ProviderPool struct {
	mu     sync.Mutex
	config PoolConfig
	states []*ProviderState // ordered; index 0 is the primary provider
}

// NewProviderPool creates a pool for the given ordered URLs and config.
// urls must be non-empty; the first element is the primary provider.
func NewProviderPool(urls []string, config PoolConfig) *ProviderPool {
	if config.MaxRetries <= 0 {
		config.MaxRetries = 3
	}
	if config.DegradedThreshold <= 0 {
		config.DegradedThreshold = 2
	}
	if config.DownThreshold <= 0 {
		config.DownThreshold = 5
	}
	if config.RecoveryInterval <= 0 {
		config.RecoveryInterval = 30 * time.Second
	}
	if config.InitialBackoff <= 0 {
		config.InitialBackoff = 500 * time.Millisecond
	}
	if config.MaxBackoff <= 0 {
		config.MaxBackoff = 10 * time.Second
	}

	states := make([]*ProviderState, 0, len(urls))
	for _, u := range urls {
		states = append(states, &ProviderState{
			URL:    u,
			Status: ProviderStatusHealthy,
		})
	}

	return &ProviderPool{config: config, states: states}
}

// ProviderURLs returns a snapshot of all configured URLs in order.
func (p *ProviderPool) ProviderURLs() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.states))
	for i, s := range p.states {
		out[i] = s.URL
	}
	return out
}

// ProviderStates returns a snapshot of the current health state of every provider.
func (p *ProviderPool) ProviderStates() []ProviderState {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]ProviderState, len(p.states))
	for i, s := range p.states {
		out[i] = *s
	}
	return out
}

// PinnedURL returns the pinned URL if the pool is in pinned (replay) mode,
// or an empty string otherwise.
func (p *ProviderPool) PinnedURL() string {
	return p.config.PinnedURL
}

// IsPinned reports whether the pool is locked to a single provider.
func (p *ProviderPool) IsPinned() bool {
	return p.config.PinnedURL != ""
}

// RecordSuccess marks a successful request to url.
func (p *ProviderPool) RecordSuccess(url string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, s := range p.states {
		if s.URL == url {
			s.ConsecutiveFailures = 0
			s.LastSuccessAt = time.Now()
			s.TotalRequests++
			s.Status = ProviderStatusHealthy
			return
		}
	}
}

// RecordFailure records a failure for url and updates its health state.
func (p *ProviderPool) RecordFailure(url string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, s := range p.states {
		if s.URL == url {
			s.ConsecutiveFailures++
			s.LastFailureAt = time.Now()
			s.TotalFailures++
			s.TotalRequests++
			// Transition health state based on consecutive failure thresholds.
			switch {
			case s.ConsecutiveFailures >= p.config.DownThreshold:
				if s.Status != ProviderStatusDown {
					logger.Logger.Warn("Provider marked DOWN",
						"url", url,
						"consecutive_failures", s.ConsecutiveFailures,
					)
				}
				s.Status = ProviderStatusDown
			case s.ConsecutiveFailures >= p.config.DegradedThreshold:
				if s.Status == ProviderStatusHealthy {
					logger.Logger.Warn("Provider marked DEGRADED",
						"url", url,
						"consecutive_failures", s.ConsecutiveFailures,
					)
				}
				s.Status = ProviderStatusDegraded
			}
			return
		}
	}
}

// orderedCandidates returns provider states in priority order for failover.
// Healthy providers come first (preserving their original ordering), then
// degraded providers (as fallbacks), then down providers whose recovery
// interval has elapsed (to allow probing).
// Caller must NOT hold p.mu.
func (p *ProviderPool) orderedCandidates() []*ProviderState {
	p.mu.Lock()
	defer p.mu.Unlock()

	var healthy, degraded, probe []*ProviderState
	for _, s := range p.states {
		switch s.Status {
		case ProviderStatusHealthy:
			healthy = append(healthy, s)
		case ProviderStatusDegraded:
			degraded = append(degraded, s)
		case ProviderStatusDown:
			// Allow probing after recovery interval.
			if !s.LastFailureAt.IsZero() &&
				time.Since(s.LastFailureAt) >= p.config.RecoveryInterval {
				probe = append(probe, s)
			}
		}
	}
	return append(append(healthy, degraded...), probe...)
}

// Do executes fn against the pool's providers using the configured retry and
// failover policy.
//
// fn receives the provider URL and a context that may be bounded by
// RequestDeadline. It must return (httpStatusCode int, err error) where
// httpStatusCode can be 0 if the request never reached the server.
//
// Do returns an AttemptDiagnostics alongside the final error so callers can
// surface which providers were tried and which one succeeded.
//
// Pinned mode: if PinnedURL is set, only that URL is used. On failure the
// error is returned immediately — no fallback is attempted.
func (p *ProviderPool) Do(
	ctx context.Context,
	fn func(ctx context.Context, url string) (httpStatusCode int, err error),
) (AttemptDiagnostics, error) {
	start := time.Now()
	diag := AttemptDiagnostics{}

	// Pinned mode: single provider, no failover.
	if p.IsPinned() {
		return p.doPinned(ctx, fn, start)
	}

	candidates := p.orderedCandidates()
	if len(candidates) == 0 {
		return diag, &AllNodesFailedError{}
	}

	backoff := p.config.InitialBackoff
	attemptsLeft := p.config.MaxRetries

	// Iterate candidates round-robin style until we succeed or exhaust retries.
	for i := 0; attemptsLeft > 0; i++ {
		// Rotate through candidates.
		candidate := candidates[i%len(candidates)]
		attemptsLeft--

		// Check parent context before attempting.
		if err := ctx.Err(); err != nil {
			// Parent cancelled or deadline exceeded — do not attempt more.
			diag.TotalDuration = time.Since(start)
			return diag, err
		}

		attemptCtx := ctx
		var cancelFn context.CancelFunc
		if p.config.RequestDeadline > 0 {
			attemptCtx, cancelFn = context.WithTimeout(ctx, p.config.RequestDeadline)
		}

		attemptStart := time.Now()
		code, err := fn(attemptCtx, candidate.URL)
		latency := time.Since(attemptStart)

		if cancelFn != nil {
			cancelFn()
		}

		rec := AttemptRecord{
			URL:            candidate.URL,
			Err:            err,
			Latency:        latency,
			HTTPStatusCode: code,
		}

		if err == nil {
			// Success.
			rec.Retryable = false
			diag.Attempts = append(diag.Attempts, rec)
			diag.SucceededURL = candidate.URL
			diag.TotalDuration = time.Since(start)
			p.RecordSuccess(candidate.URL)
			return diag, nil
		}

		// Classify the error.
		retryable := isRetryableError(err) || (code > 0 && isRetryableStatusCode(code))
		rec.Retryable = retryable
		rec.Cause = ClassifyFailureCause(err, code)
		diag.Attempts = append(diag.Attempts, rec)

		p.RecordFailure(candidate.URL)

		logger.Logger.Warn("Provider attempt failed",
			"url", candidate.URL,
			"attempt", len(diag.Attempts),
			"retryable", retryable,
			"status", code,
			"error", err,
		)

		if !retryable {
			// Non-retryable error (e.g. 400, 401, 403, invalid payload):
			// do NOT try another provider — return immediately to avoid
			// sending the same bad request to every endpoint.
			diag.TotalDuration = time.Since(start)
			return diag, fmt.Errorf("non-retryable error from %s (HTTP %d): %w", candidate.URL, code, err)
		}

		if attemptsLeft == 0 {
			break
		}

		// Apply backoff before next attempt (respects parent context).
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			diag.TotalDuration = time.Since(start)
			return diag, ctx.Err()
		}

		// Exponential backoff with cap.
		backoff *= 2
		if backoff > p.config.MaxBackoff {
			backoff = p.config.MaxBackoff
		}

		// Refresh candidate list in case health states changed mid-loop.
		candidates = p.orderedCandidates()
		if len(candidates) == 0 {
			break
		}
	}

	diag.TotalDuration = time.Since(start)

	// Build AllNodesFailedError from the attempt records.
	failures := make([]NodeFailure, 0, len(diag.Attempts))
	for _, a := range diag.Attempts {
		if a.Err != nil {
			failures = append(failures, NodeFailure{URL: a.URL, Reason: a.Err})
		}
	}
	return diag, &AllNodesFailedError{Failures: failures}
}

// doPinned executes fn against the pinned URL only. On failure it returns
// the error immediately — no fallback.
func (p *ProviderPool) doPinned(
	ctx context.Context,
	fn func(ctx context.Context, url string) (int, error),
	start time.Time,
) (AttemptDiagnostics, error) {
	diag := AttemptDiagnostics{}
	url := p.config.PinnedURL

	attemptCtx := ctx
	var cancelFn context.CancelFunc
	if p.config.RequestDeadline > 0 {
		attemptCtx, cancelFn = context.WithTimeout(ctx, p.config.RequestDeadline)
	}

	attemptStart := time.Now()
	code, err := fn(attemptCtx, url)
	latency := time.Since(attemptStart)
	if cancelFn != nil {
		cancelFn()
	}

	rec := AttemptRecord{
		URL:            url,
		Err:            err,
		Latency:        latency,
		HTTPStatusCode: code,
		Retryable:      false, // pinned mode: no retry
	}
	diag.Attempts = append(diag.Attempts, rec)
	diag.TotalDuration = time.Since(start)

	if err != nil {
		// Pinned mode: never fall back.
		logger.Logger.Error("Pinned provider failed — failover disabled (replay mode)",
			"url", url, "error", err,
		)
		p.RecordFailure(url)
		return diag, fmt.Errorf(
			"pinned provider %s failed (replay pinning disables silent switching): %w",
			url, err,
		)
	}

	diag.SucceededURL = url
	p.RecordSuccess(url)
	return diag, nil
}

// FormatDiagnostics returns a human-readable summary of the AttemptDiagnostics
// suitable for user-facing output.
func FormatDiagnostics(d AttemptDiagnostics) string {
	if len(d.Attempts) == 0 {
		return "No provider attempts recorded."
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Provider failover summary (%d attempt(s), %.0fms total):\n",
		len(d.Attempts), float64(d.TotalDuration.Milliseconds()))

	for i, a := range d.Attempts {
		status := "OK"
		if a.Err != nil {
			if a.Retryable {
				status = fmt.Sprintf("FAIL (retryable, %dms)", a.Latency.Milliseconds())
			} else {
				status = fmt.Sprintf("FAIL (non-retryable, %dms)", a.Latency.Milliseconds())
			}
			if a.HTTPStatusCode > 0 {
				status += fmt.Sprintf(", HTTP %d", a.HTTPStatusCode)
			}
		} else {
			status = fmt.Sprintf("OK (%dms)", a.Latency.Milliseconds())
		}
		fmt.Fprintf(&sb, "  [%d] %s — %s\n", i+1, a.URL, status)
	}

	if d.SucceededURL != "" {
		fmt.Fprintf(&sb, "  Succeeded via: %s\n", d.SucceededURL)
	} else {
		sb.WriteString("  All providers failed.\n")
	}
	return sb.String()
}
