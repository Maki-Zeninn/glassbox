// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package logger

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// ── Operation ID ──────────────────────────────────────────────────────────────

func TestWithOperation_GeneratesUniqueIDs(t *testing.T) {
	ctx1, id1 := WithOperation(context.Background())
	ctx2, id2 := WithOperation(context.Background())

	if id1 == "" || id2 == "" {
		t.Fatal("expected non-empty operation IDs")
	}
	if id1 == id2 {
		t.Errorf("two separate WithOperation calls produced the same ID: %q", id1)
	}
	// IDs should be stored in the returned context.
	if got := OperationIDFromContext(ctx1); got != id1 {
		t.Errorf("ctx1 operation_id = %q, want %q", got, id1)
	}
	if got := OperationIDFromContext(ctx2); got != id2 {
		t.Errorf("ctx2 operation_id = %q, want %q", got, id2)
	}
}

func TestWithOperation_NilContext(t *testing.T) {
	ctx, id := WithOperation(nil) //nolint:staticcheck
	if id == "" {
		t.Fatal("expected non-empty ID from nil context")
	}
	if OperationIDFromContext(ctx) != id {
		t.Error("operation ID not stored in returned context")
	}
}

func TestWithOperationID_Deterministic(t *testing.T) {
	ctx := WithOperationID(context.Background(), "test-op-1234")
	if got := OperationIDFromContext(ctx); got != "test-op-1234" {
		t.Errorf("OperationIDFromContext = %q, want %q", got, "test-op-1234")
	}
}

func TestWithOperationID_EmptyGeneratesNew(t *testing.T) {
	ctx := WithOperationID(context.Background(), "")
	id := OperationIDFromContext(ctx)
	if id == "" {
		t.Error("expected a generated ID when empty string supplied")
	}
}

func TestOperationIDFromContext_Missing(t *testing.T) {
	if got := OperationIDFromContext(context.Background()); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestOperationIDFromContext_Nil(t *testing.T) {
	if got := OperationIDFromContext(nil); got != "" { //nolint:staticcheck
		t.Errorf("expected empty string for nil context, got %q", got)
	}
}

// ── Component ID ──────────────────────────────────────────────────────────────

func TestWithComponent_DifferentIDsPerOperation(t *testing.T) {
	ctx, _ := WithOperation(context.Background())

	ctx1, id1 := WithComponent(ctx, "rpc")
	ctx2, id2 := WithComponent(ctx, "rpc")

	if id1 == id2 {
		t.Errorf("two WithComponent calls produced the same ID: %q", id1)
	}
	if ComponentIDFromContext(ctx1) != id1 {
		t.Error("component ID not stored in ctx1")
	}
	if ComponentIDFromContext(ctx2) != id2 {
		t.Error("component ID not stored in ctx2")
	}
}

func TestWithComponent_NilContext(t *testing.T) {
	ctx, id := WithComponent(nil, "sim") //nolint:staticcheck
	if id == "" {
		t.Fatal("expected non-empty component ID from nil context")
	}
	if ComponentIDFromContext(ctx) != id {
		t.Error("component ID not stored")
	}
}

func TestWithComponent_EmptyName(t *testing.T) {
	_, id := WithComponent(context.Background(), "")
	if !strings.HasPrefix(id, "cmp-") {
		t.Errorf("empty component name should use 'cmp' prefix, got %q", id)
	}
}

func TestComponentIDFromContext_Missing(t *testing.T) {
	if got := ComponentIDFromContext(context.Background()); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

// ── Concurrent operations do not share IDs ────────────────────────────────────

func TestConcurrentOperations_DistinctIDs(t *testing.T) {
	const n = 20
	ids := make([]string, n)
	var wg sync.WaitGroup

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, id := WithOperation(context.Background())
			ids[idx] = id
		}(i)
	}
	wg.Wait()

	seen := make(map[string]struct{}, n)
	for _, id := range ids {
		if _, dup := seen[id]; dup {
			t.Errorf("duplicate operation ID detected: %q", id)
		}
		seen[id] = struct{}{}
	}
}

// ── OpLogger injects fields ───────────────────────────────────────────────────

func TestOpLogger_InjectsOperationID_AtDebugLevel(t *testing.T) {
	buf := &bytes.Buffer{}
	SetOutput(buf, true)
	SetLevel(slog.LevelDebug) // Enable debug so correlation fields appear.

	ctx := WithOperationID(context.Background(), "op-aabbccdd1122")
	l := OpLogger(ctx)
	l.DebugContext(ctx, "checking op logger")

	out := buf.String()
	if !strings.Contains(out, "op-aabbccdd1122") {
		t.Errorf("operation_id not found in log output: %s", out)
	}
}

func TestOpLogger_InjectsComponentID_AtDebugLevel(t *testing.T) {
	buf := &bytes.Buffer{}
	SetOutput(buf, true)
	SetLevel(slog.LevelDebug)

	ctx := WithOperationID(context.Background(), "op-test")
	ctx = WithComponentID(ctx, "rpc", "rpc-deadbeef")
	l := OpLogger(ctx)
	l.DebugContext(ctx, "component trace")

	out := buf.String()
	if !strings.Contains(out, "rpc-deadbeef") {
		t.Errorf("component_id not found in log output: %s", out)
	}
}

func TestOpLogger_NormalInfo_NoCorrelationFields(t *testing.T) {
	buf := &bytes.Buffer{}
	SetOutput(buf, true)
	SetLevel(slog.LevelInfo) // INFO: correlation fields should be suppressed.

	ctx := WithOperationID(context.Background(), "op-should-not-appear")
	l := OpLogger(ctx)
	l.InfoContext(ctx, "user visible message")

	out := buf.String()
	if strings.Contains(out, "op-should-not-appear") {
		t.Errorf("operation_id should NOT appear in INFO output: %s", out)
	}
}

func TestOpLogger_Verbose_InfoIncludesCorrelation(t *testing.T) {
	buf := &bytes.Buffer{}
	SetOutput(buf, true)
	SetLevel(slog.LevelInfo)

	ctx := WithOperationID(context.Background(), "op-verbose-test")
	ctx = WithVerboseCorrelation(ctx)
	l := OpLogger(ctx)
	l.InfoContext(ctx, "verbose diagnostic")

	out := buf.String()
	if !strings.Contains(out, "op-verbose-test") {
		t.Errorf("operation_id not found in verbose INFO output: %s", out)
	}
}

func TestOpLogger_NoIDs_ReturnsGlobalLogger(t *testing.T) {
	l := OpLogger(context.Background())
	// Should not panic and should equal the global Logger.
	if l == nil {
		t.Error("expected non-nil logger")
	}
}

// ── ComponentLogger ───────────────────────────────────────────────────────────

func TestComponentLogger_AttachesComponentID(t *testing.T) {
	buf := &bytes.Buffer{}
	SetOutput(buf, true)
	SetLevel(slog.LevelDebug)

	ctx := WithOperationID(context.Background(), "op-comp-test")
	ctx2, l := ComponentLogger(ctx, "simulator")
	l.DebugContext(ctx2, "simulator boundary")

	out := buf.String()
	if !strings.Contains(out, "simulator-") {
		t.Errorf("component_id with 'simulator-' prefix not found: %s", out)
	}
}

func TestComponentLogger_ExistingComponentNotReplaced(t *testing.T) {
	buf := &bytes.Buffer{}
	SetOutput(buf, true)
	SetLevel(slog.LevelDebug)

	ctx := WithOperationID(context.Background(), "op-existing")
	ctx = WithComponentID(ctx, "rpc", "rpc-fixed")

	ctx2, l := ComponentLogger(ctx, "rpc")
	_ = ctx2
	l.DebugContext(ctx, "existing component")

	out := buf.String()
	if !strings.Contains(out, "rpc-fixed") {
		t.Errorf("pre-existing component ID 'rpc-fixed' should be preserved: %s", out)
	}
}

// ── IPC header propagation ────────────────────────────────────────────────────

func TestWithIPCHeaders_PopulatesHeaders(t *testing.T) {
	ctx := WithOperationID(context.Background(), "op-ipc-1")
	ctx = WithComponentID(ctx, "rpc", "rpc-ipc-2")

	headers := WithIPCHeaders(ctx)

	if headers[IPCHeaderOperationID] != "op-ipc-1" {
		t.Errorf("IPCHeaderOperationID = %q, want %q", headers[IPCHeaderOperationID], "op-ipc-1")
	}
	if headers[IPCHeaderComponentID] != "rpc-ipc-2" {
		t.Errorf("IPCHeaderComponentID = %q, want %q", headers[IPCHeaderComponentID], "rpc-ipc-2")
	}
}

func TestWithIPCHeaders_EmptyWhenNoIDs(t *testing.T) {
	headers := WithIPCHeaders(context.Background())
	if len(headers) != 0 {
		t.Errorf("expected empty headers, got %v", headers)
	}
}

func TestFromIPCHeaders_RestoresIDs(t *testing.T) {
	headers := map[string]string{
		IPCHeaderOperationID: "op-restored",
		IPCHeaderComponentID: "sim-restored",
	}
	ctx := FromIPCHeaders(context.Background(), headers)

	if got := OperationIDFromContext(ctx); got != "op-restored" {
		t.Errorf("OperationIDFromContext = %q, want %q", got, "op-restored")
	}
	if got := ComponentIDFromContext(ctx); got != "sim-restored" {
		t.Errorf("ComponentIDFromContext = %q, want %q", got, "sim-restored")
	}
}

func TestFromIPCHeaders_CaseInsensitiveKeys(t *testing.T) {
	headers := map[string]string{
		"x-glassbox-operation-id": "op-ci",
		"x-glassbox-component-id": "cmp-ci",
	}
	ctx := FromIPCHeaders(context.Background(), headers)

	if got := OperationIDFromContext(ctx); got != "op-ci" {
		t.Errorf("OperationIDFromContext (lowercase key) = %q, want %q", got, "op-ci")
	}
}

func TestFromIPCHeaders_IgnoresUnknownKeys(t *testing.T) {
	headers := map[string]string{
		"x-other-header": "noise",
	}
	ctx := FromIPCHeaders(context.Background(), headers)
	if got := OperationIDFromContext(ctx); got != "" {
		t.Errorf("unexpected operation ID from unknown header: %q", got)
	}
}

func TestFromIPCHeaders_NilContext(t *testing.T) {
	headers := map[string]string{IPCHeaderOperationID: "op-nil-ctx"}
	ctx := FromIPCHeaders(nil, headers) //nolint:staticcheck
	if got := OperationIDFromContext(ctx); got != "op-nil-ctx" {
		t.Errorf("OperationIDFromContext = %q, want %q", got, "op-nil-ctx")
	}
}

// ── Round-trip: WithIPCHeaders ↔ FromIPCHeaders ───────────────────────────────

func TestIPCHeaders_RoundTrip(t *testing.T) {
	ctx := WithOperationID(context.Background(), "op-rt-abc")
	ctx = WithComponentID(ctx, "trace", "trace-rt-xyz")

	headers := WithIPCHeaders(ctx)

	ctx2 := FromIPCHeaders(context.Background(), headers)
	if got := OperationIDFromContext(ctx2); got != "op-rt-abc" {
		t.Errorf("round-trip operation_id = %q, want %q", got, "op-rt-abc")
	}
	if got := ComponentIDFromContext(ctx2); got != "trace-rt-xyz" {
		t.Errorf("round-trip component_id = %q, want %q", got, "trace-rt-xyz")
	}
}

// ── Cancellation: IDs survive context cancellation ────────────────────────────

func TestOperationID_SurvivesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ctx = WithOperationID(ctx, "op-cancel-test")

	cancel()

	// Even after cancellation the value is still readable.
	if got := OperationIDFromContext(ctx); got != "op-cancel-test" {
		t.Errorf("operation ID lost after cancel: %q", got)
	}
}

// ── Nested operations keep parent ID accessible ───────────────────────────────

func TestNestedOperations_ParentIDPreserved(t *testing.T) {
	parentCtx := WithOperationID(context.Background(), "op-parent")

	// A child operation gets its own ID but the parent ctx is unchanged.
	childCtx, childID := WithOperation(parentCtx)
	_ = childCtx

	if got := OperationIDFromContext(parentCtx); got != "op-parent" {
		t.Errorf("parent context modified by child operation: got %q, want op-parent", got)
	}
	if childID == "op-parent" {
		t.Error("child operation reused the parent ID")
	}
}
