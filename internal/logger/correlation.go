// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package logger — correlation.go adds structured correlation IDs for
// cross-component log tracing.
//
// Two ID scopes are defined:
//
//   - Operation ID  — identifies a single top-level CLI invocation (e.g. one
//     `glassbox debug` run).  It is generated once at the entry-point and
//     propagated via context.  Concurrent operations each receive a distinct ID.
//
//   - Component ID  — an optional sub-scope within an operation, identifying
//     a specific subsystem (e.g. "rpc", "simulator", "trace").  Component IDs
//     are nested under the operation and never shared between operations.
//
// Neither ID carries user data: both are opaque random hex strings.
//
// Propagation through IPC is supported via [WithIPCHeaders] / [FromIPCHeaders],
// which marshal both IDs into a map suitable for HTTP or JSON-RPC header blobs.
//
// The IDs are injected into every slog record produced by [OpLogger] and
// [ComponentLogger].  They are absent from log output unless verbose diagnostics
// are enabled ([VerboseCorrelation]), protecting normal user output.
package logger

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
)

// ── Context keys ──────────────────────────────────────────────────────────────

// operationKey is the context key for the operation-scoped correlation ID.
type operationKey struct{}

// componentKey is the context key for the component-scoped correlation ID.
type componentKey struct{}

// verboseCorrelationKey enables correlation fields in standard log output.
type verboseCorrelationKey struct{}

// ── ID generation ─────────────────────────────────────────────────────────────

// newOpID generates a new opaque 8-byte (16 hex char) operation correlation ID.
// IDs are prefixed with "op-" to be recognisable in log output.
func newOpID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("op-%x", b)
}

// newComponentID generates a new opaque 4-byte (8 hex char) component sub-ID.
// It is prefixed with the supplied component name so log records are self-describing.
func newComponentID(component string) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	if component == "" {
		component = "cmp"
	}
	return fmt.Sprintf("%s-%x", component, b)
}

// ── Context helpers ───────────────────────────────────────────────────────────

// WithOperation returns a new context carrying a freshly generated operation ID.
// The new ID is independent of any existing operation ID in ctx — each call
// creates a new top-level operation.  Use [WithOperationID] to supply a
// deterministic ID (e.g. in tests).
func WithOperation(ctx context.Context) (context.Context, string) {
	if ctx == nil {
		ctx = context.Background()
	}
	id := newOpID()
	return context.WithValue(ctx, operationKey{}, id), id
}

// WithOperationID stores id as the operation correlation ID in ctx.
// If id is empty a new random ID is generated.  This variant is primarily
// useful in tests that require deterministic IDs.
func WithOperationID(ctx context.Context, id string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if id == "" {
		id = newOpID()
	}
	return context.WithValue(ctx, operationKey{}, id)
}

// OperationIDFromContext extracts the operation ID stored in ctx.
// Returns "" when no ID has been set.
func OperationIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if id, ok := ctx.Value(operationKey{}).(string); ok {
		return id
	}
	return ""
}

// WithComponent attaches a component sub-ID to ctx.  A new random suffix is
// generated so that the same component name used in two concurrent operations
// produces distinct IDs.  The component ID is scoped beneath the current
// operation ID — it does not replace it.
func WithComponent(ctx context.Context, component string) (context.Context, string) {
	if ctx == nil {
		ctx = context.Background()
	}
	id := newComponentID(component)
	return context.WithValue(ctx, componentKey{}, id), id
}

// WithComponentID stores the given id as the component correlation ID.
// Useful in tests.  Empty id generates a new random one.
func WithComponentID(ctx context.Context, component, id string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if id == "" {
		id = newComponentID(component)
	}
	return context.WithValue(ctx, componentKey{}, id)
}

// ComponentIDFromContext extracts the component correlation ID stored in ctx.
// Returns "" when none has been set.
func ComponentIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if id, ok := ctx.Value(componentKey{}).(string); ok {
		return id
	}
	return ""
}

// ── Verbose diagnostics toggle ────────────────────────────────────────────────

// WithVerboseCorrelation returns a context that instructs [OpLogger] and
// [ComponentLogger] to include correlation fields in every log record,
// even when the log level is below DEBUG.  Use this for operator diagnostics
// sessions only — never in normal user-facing output.
func WithVerboseCorrelation(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, verboseCorrelationKey{}, true)
}

// isVerboseCorrelation reports whether verbose correlation fields are enabled.
func isVerboseCorrelation(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(verboseCorrelationKey{}).(bool)
	return v
}

// ── Loggers ───────────────────────────────────────────────────────────────────

// OpLogger returns a *slog.Logger pre-populated with the operation_id (and
// component_id, if set) from ctx.  Correlation fields are injected only when:
//
//   - verbose correlation is enabled in ctx, OR
//   - the log record is at DEBUG level or below.
//
// This prevents correlation IDs from appearing in normal INFO-level user output.
func OpLogger(ctx context.Context) *slog.Logger {
	l := Logger
	opID := OperationIDFromContext(ctx)
	compID := ComponentIDFromContext(ctx)

	if opID == "" && compID == "" {
		return l
	}

	if !isVerboseCorrelation(ctx) {
		// Only attach IDs when the caller explicitly enables verbose diagnostics
		// or when the underlying level is DEBUG or below — keeps INFO output clean.
		if level.Level() > slog.LevelDebug {
			return l
		}
	}

	if opID != "" {
		l = l.With("operation_id", opID)
	}
	if compID != "" {
		l = l.With("component_id", compID)
	}
	// Also propagate legacy correlation_id if set.
	if corrID := CorrelationFromContext(ctx); corrID != "" && corrID != opID {
		l = l.With("correlation_id", corrID)
	}
	return l
}

// ComponentLogger is a convenience wrapper around [OpLogger] that first attaches
// a component sub-ID (if one is not already present in ctx) and then returns
// the logger.  It returns the enriched context alongside the logger so callers
// can propagate the component ID downstream.
func ComponentLogger(ctx context.Context, component string) (context.Context, *slog.Logger) {
	if ComponentIDFromContext(ctx) == "" {
		var id string
		ctx, id = WithComponent(ctx, component)
		_ = id
	}
	return ctx, OpLogger(ctx)
}

// ── IPC propagation ───────────────────────────────────────────────────────────

const (
	// IPCHeaderOperationID is the header/key name for the operation correlation ID.
	IPCHeaderOperationID = "X-Glassbox-Operation-ID"
	// IPCHeaderComponentID is the header/key name for the component correlation ID.
	IPCHeaderComponentID = "X-Glassbox-Component-ID"
)

// WithIPCHeaders serialises the operation and component IDs from ctx into a
// map[string]string suitable for embedding into HTTP headers, JSON-RPC meta
// fields, or IPC envelope attributes.  Only populated fields are included.
func WithIPCHeaders(ctx context.Context) map[string]string {
	headers := make(map[string]string)
	if id := OperationIDFromContext(ctx); id != "" {
		headers[IPCHeaderOperationID] = id
	}
	if id := ComponentIDFromContext(ctx); id != "" {
		headers[IPCHeaderComponentID] = id
	}
	return headers
}

// FromIPCHeaders restores operation and component IDs from the supplied headers
// map into a new context derived from ctx.  Keys are matched case-insensitively.
// Unknown keys are silently ignored.
func FromIPCHeaders(ctx context.Context, headers map[string]string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	for k, v := range headers {
		switch canonicalHeader(k) {
		case IPCHeaderOperationID:
			if v != "" {
				ctx = context.WithValue(ctx, operationKey{}, v)
			}
		case IPCHeaderComponentID:
			if v != "" {
				ctx = context.WithValue(ctx, componentKey{}, v)
			}
		}
	}
	return ctx
}

// canonicalHeader normalises a header name to its canonical form for comparison.
func canonicalHeader(s string) string {
	// Simple exact-match normalization: we only have two well-known keys.
	switch {
	case eqFold(s, IPCHeaderOperationID):
		return IPCHeaderOperationID
	case eqFold(s, IPCHeaderComponentID):
		return IPCHeaderComponentID
	}
	return s
}

// eqFold is a fast ASCII case-insensitive equality check.
func eqFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
