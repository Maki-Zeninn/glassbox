// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package plugin – executor.go
// Issue #828: Plugin lifecycle isolation.
//
// PluginExecutor is the single choke-point through which every plugin
// invocation flows. It enforces:
//
//   - Per-call deadlines (hard timeout via context).
//   - Cancellation propagation from the caller's context.
//   - Panic recovery so a host-side shim panic never reaches the CLI.
//   - Concurrency limit: at most MaxConcurrentCalls goroutines per executor.
//   - Diagnostic context preservation: every error includes the plugin name,
//     method, and request ID without leaking raw input payloads.
//
// The executor is intentionally decoupled from SandboxedPlugin so that either
// component can be replaced or tested independently.

package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// MaxConcurrentCalls is the default ceiling on simultaneous in-flight calls to
// a single plugin. Callers that exceed this limit receive ErrExecutorBusy.
const MaxConcurrentCalls = 8

// ErrExecutorBusy is returned when the executor's concurrency limit is reached.
var ErrExecutorBusy = fmt.Errorf("plugin executor: too many concurrent calls")

// Callable is the narrow interface the executor requires from a plugin backend.
// SandboxedPlugin satisfies this interface.
type Callable interface {
	// Name returns the plugin identifier for error attribution.
	Name() string
	// callRaw dispatches a PluginRequest and returns the raw result bytes.
	// It is called under the executor's concurrency gate.
	callRaw(ctx context.Context, req PluginRequest) (json.RawMessage, error)
	// recordFailure updates the plugin's health/quarantine state.
	recordFailure(err error)
	// IsQuarantined reports whether the plugin has been quarantined.
	IsQuarantined() bool
}

// ExecutorConfig holds tunable parameters for a PluginExecutor.
type ExecutorConfig struct {
	// DefaultTimeout is the per-call deadline when no explicit timeout is
	// supplied in ResourceLimits.  Zero means use defaultPluginTimeout.
	DefaultTimeout time.Duration

	// MaxConcurrent is the maximum number of simultaneous calls.
	// Zero means MaxConcurrentCalls.
	MaxConcurrent int
}

// ExecutorResult carries the outcome of a single executor call.
type ExecutorResult struct {
	// PluginName is the plugin that processed (or failed to process) the call.
	PluginName string
	// Method is the PluginMethod that was requested.
	Method PluginMethod
	// RequestID is the envelope ID for correlation.
	RequestID string
	// Result is the decoded payload (nil on error or non-decode methods).
	Result json.RawMessage
	// Err is nil on success.
	Err error
	// Elapsed is the wall time spent inside the plugin call.
	Elapsed time.Duration
	// Quarantined is true when the call was rejected because the plugin had
	// already been quarantined before the call was dispatched.
	Quarantined bool
	// TimedOut is true when the call exceeded its deadline.
	TimedOut bool
}

// IsOK reports whether the executor call succeeded.
func (r *ExecutorResult) IsOK() bool { return r != nil && r.Err == nil }

// PluginExecutor is a concurrency-safe execution wrapper for plugin calls.
// Create one per plugin instance via NewPluginExecutor.
type PluginExecutor struct {
	cfg      ExecutorConfig
	plugin   Callable
	sem      chan struct{} // concurrency gate (buffered channel used as semaphore)
	mu       sync.Mutex
	closed   atomic.Bool
	inflight atomic.Int64 // current in-flight count (for metrics/testing)
}

// NewPluginExecutor creates an executor wrapping the given Callable.
func NewPluginExecutor(p Callable, cfg ExecutorConfig) *PluginExecutor {
	if cfg.DefaultTimeout <= 0 {
		cfg.DefaultTimeout = defaultPluginTimeout
	}
	mc := cfg.MaxConcurrent
	if mc <= 0 {
		mc = MaxConcurrentCalls
	}
	return &PluginExecutor{
		cfg:    cfg,
		plugin: p,
		sem:    make(chan struct{}, mc),
	}
}

// Call dispatches a PluginRequest to the plugin, enforcing deadlines,
// concurrency limits, and panic recovery. It never panics; all failure
// paths return a populated ExecutorResult with Err set.
//
// The caller's ctx is respected: if it is cancelled before or during the call
// the plugin process is terminated and the cancellation error is returned.
func (e *PluginExecutor) Call(ctx context.Context, req PluginRequest) *ExecutorResult {
	res := &ExecutorResult{
		PluginName: e.plugin.Name(),
		Method:     req.Method,
		RequestID:  req.ID,
	}

	if e.closed.Load() {
		res.Err = fmt.Errorf("plugin executor for %s is closed", e.plugin.Name())
		return res
	}

	// Fast-path: quarantine check before touching the semaphore.
	if e.plugin.IsQuarantined() {
		res.Quarantined = true
		res.Err = fmt.Errorf("plugin %s: %w", e.plugin.Name(), ErrPluginQuarantined)
		return res
	}

	// Acquire concurrency slot.
	select {
	case e.sem <- struct{}{}:
		defer func() { <-e.sem }()
	default:
		res.Err = fmt.Errorf("plugin %s: %w", e.plugin.Name(), ErrExecutorBusy)
		return res
	}

	e.inflight.Add(1)
	defer e.inflight.Add(-1)

	// Build the effective call context with deadline.
	timeout := e.cfg.DefaultTimeout
	if req.Limits.TimeoutMs > 0 {
		timeout = time.Duration(req.Limits.TimeoutMs) * time.Millisecond
	}

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Dispatch with full panic + error isolation.
	start := time.Now()
	result, err := e.callSafe(callCtx, req)
	res.Elapsed = time.Since(start)

	if err != nil {
		// Classify timeout vs. ordinary failure.
		if callCtx.Err() == context.DeadlineExceeded {
			res.TimedOut = true
			err = fmt.Errorf("plugin %s: call timed out after %v: %w",
				e.plugin.Name(), timeout, context.DeadlineExceeded)
		} else if ctx.Err() != nil {
			// Parent context was cancelled — propagate as-is.
			err = fmt.Errorf("plugin %s: call cancelled: %w", e.plugin.Name(), ctx.Err())
		}

		e.plugin.recordFailure(err)
		res.Err = err
		return res
	}

	res.Result = result
	return res
}

// callSafe wraps callRaw with a recover so that any unexpected host-side panic
// is converted to a plain error rather than crashing the process.
func (e *PluginExecutor) callSafe(ctx context.Context, req PluginRequest) (result json.RawMessage, retErr error) {
	defer func() {
		if r := recover(); r != nil {
			retErr = fmt.Errorf("plugin %s: host shim panic: %v", e.plugin.Name(), r)
		}
	}()
	return e.plugin.callRaw(ctx, req)
}

// Inflight returns the number of currently executing calls (useful in tests).
func (e *PluginExecutor) Inflight() int64 { return e.inflight.Load() }

// Close marks the executor as closed. Calls issued after Close returns will
// receive an error; in-progress calls are not interrupted.
func (e *PluginExecutor) Close() {
	e.closed.Store(true)
}

// ─── HealthPoller ─────────────────────────────────────────────────────────────

// HealthCheckable is the interface required by HealthPoller.
type HealthCheckable interface {
	Name() string
	HealthCheck(ctx context.Context) error
	IsQuarantined() bool
}

// HealthPoller runs periodic health checks against a HealthCheckable plugin
// in a background goroutine. It stops automatically when ctx is cancelled or
// when the plugin becomes quarantined.
type HealthPoller struct {
	plugin   HealthCheckable
	interval time.Duration
	onError  func(pluginName string, err error)
}

// NewHealthPoller creates a HealthPoller for the given plugin.
// interval controls how frequently health checks are sent.
// onError is called (in the poller goroutine) each time a health check fails;
// it may be nil.
func NewHealthPoller(p HealthCheckable, interval time.Duration, onError func(string, error)) *HealthPoller {
	if interval <= 0 {
		interval = healthCheckInterval
	}
	return &HealthPoller{
		plugin:   p,
		interval: interval,
		onError:  onError,
	}
}

// Start launches the poller in a background goroutine. It returns immediately.
// The goroutine exits when ctx is cancelled or the plugin is quarantined.
func (hp *HealthPoller) Start(ctx context.Context) {
	go hp.run(ctx)
}

func (hp *HealthPoller) run(ctx context.Context) {
	ticker := time.NewTicker(hp.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if hp.plugin.IsQuarantined() {
				return
			}
			checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			err := hp.plugin.HealthCheck(checkCtx)
			cancel()
			if err != nil && hp.onError != nil {
				hp.onError(hp.plugin.Name(), err)
			}
		}
	}
}

// ─── BatchExecutor ────────────────────────────────────────────────────────────

// BatchExecutor fans out a slice of PluginRequests to the same executor
// concurrently and collects the results in input order.
type BatchExecutor struct {
	exec *PluginExecutor
}

// NewBatchExecutor wraps an existing PluginExecutor for batch use.
func NewBatchExecutor(e *PluginExecutor) *BatchExecutor {
	return &BatchExecutor{exec: e}
}

// Run dispatches all requests concurrently and returns results in the same
// order as the input slice. The parent ctx cancellation is propagated to all
// in-flight calls.
func (b *BatchExecutor) Run(ctx context.Context, reqs []PluginRequest) []*ExecutorResult {
	results := make([]*ExecutorResult, len(reqs))
	var wg sync.WaitGroup

	for i, req := range reqs {
		wg.Add(1)
		go func(idx int, r PluginRequest) {
			defer wg.Done()
			results[idx] = b.exec.Call(ctx, r)
		}(i, req)
	}

	wg.Wait()
	return results
}
