// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func newTestChecker() *HealthChecker {
	return NewHealthChecker()
}

func decodeBody[T any](t *testing.T, rr *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.NewDecoder(rr.Body).Decode(&v); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}
	return v
}

// ── LivenessHandler ───────────────────────────────────────────────────────────

func TestLivenessHandler_AlwaysOK(t *testing.T) {
	h := newTestChecker()
	req := httptest.NewRequest(http.MethodGet, "/healthz/live", nil)
	rr := httptest.NewRecorder()

	h.LivenessHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	resp := decodeBody[LivenessResponse](t, rr)
	if resp.Status != HealthOK {
		t.Errorf("expected status %q, got %q", HealthOK, resp.Status)
	}
	if resp.Version == "" {
		t.Error("expected non-empty version")
	}
}

func TestLivenessHandler_RemainsAvailableDuringDependencyFailure(t *testing.T) {
	h := newTestChecker()
	// Register a failing simulator probe — liveness must ignore it.
	h.WithSimulatorProbe(func(_ context.Context) ComponentCheck {
		return ComponentCheck{Name: "simulator", Status: HealthDown, Message: "binary missing"}
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz/live", nil)
	rr := httptest.NewRecorder()
	h.LivenessHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("liveness must return 200 even when dependencies are down, got %d", rr.Code)
	}
}

func TestLivenessHandler_MethodNotAllowed(t *testing.T) {
	h := newTestChecker()
	req := httptest.NewRequest(http.MethodPost, "/healthz/live", nil)
	rr := httptest.NewRecorder()
	h.LivenessHandler(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 for POST, got %d", rr.Code)
	}
}

func TestLivenessHandler_HeadRequest(t *testing.T) {
	h := newTestChecker()
	req := httptest.NewRequest(http.MethodHead, "/healthz/live", nil)
	rr := httptest.NewRecorder()
	h.LivenessHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 for HEAD, got %d", rr.Code)
	}
}

// ── ReadinessHandler ──────────────────────────────────────────────────────────

func TestReadinessHandler_AllHealthy(t *testing.T) {
	h := newTestChecker()
	h.WithSimulatorProbe(func(_ context.Context) ComponentCheck {
		return ComponentCheck{Name: "simulator", Status: HealthOK}
	})
	h.WithCacheProbe(func(_ context.Context) ComponentCheck {
		return ComponentCheck{Name: "cache", Status: HealthOK}
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz/ready", nil)
	rr := httptest.NewRecorder()
	h.ReadinessHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	resp := decodeBody[ReadinessResponse](t, rr)
	if resp.Status != HealthOK {
		t.Errorf("expected %q, got %q", HealthOK, resp.Status)
	}
}

func TestReadinessHandler_DegradedWhenComponentDegraded(t *testing.T) {
	h := newTestChecker()
	h.WithSimulatorProbe(func(_ context.Context) ComponentCheck {
		return ComponentCheck{Name: "simulator", Status: HealthOK}
	})
	h.WithCacheProbe(func(_ context.Context) ComponentCheck {
		return ComponentCheck{Name: "cache", Status: HealthDegraded, Message: "slow disk"}
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz/ready", nil)
	rr := httptest.NewRecorder()
	h.ReadinessHandler(rr, req)

	// Degraded does not make readiness fail — still 200 but body says degraded.
	if rr.Code != http.StatusOK {
		t.Errorf("degraded should still be 200, got %d", rr.Code)
	}
	resp := decodeBody[ReadinessResponse](t, rr)
	if resp.Status != HealthDegraded {
		t.Errorf("expected %q, got %q", HealthDegraded, resp.Status)
	}
}

func TestReadinessHandler_503WhenComponentDown(t *testing.T) {
	h := newTestChecker()
	h.WithSimulatorProbe(func(_ context.Context) ComponentCheck {
		return ComponentCheck{Name: "simulator", Status: HealthDown, Message: "binary not found"}
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz/ready", nil)
	rr := httptest.NewRecorder()
	h.ReadinessHandler(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when a component is down, got %d", rr.Code)
	}
	resp := decodeBody[ReadinessResponse](t, rr)
	if resp.Status != HealthDown {
		t.Errorf("expected %q, got %q", HealthDown, resp.Status)
	}
	if len(resp.Components) == 0 {
		t.Error("expected component details in 503 body")
	}
}

func TestReadinessHandler_503DuringShutdown(t *testing.T) {
	h := newTestChecker()
	h.MarkShuttingDown()

	req := httptest.NewRequest(http.MethodGet, "/healthz/ready", nil)
	rr := httptest.NewRecorder()
	h.ReadinessHandler(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 during shutdown, got %d", rr.Code)
	}
	resp := decodeBody[ReadinessResponse](t, rr)
	if resp.Status != HealthDown {
		t.Errorf("expected down status during shutdown, got %q", resp.Status)
	}
}

func TestReadinessHandler_MethodNotAllowed(t *testing.T) {
	h := newTestChecker()
	req := httptest.NewRequest(http.MethodPut, "/healthz/ready", nil)
	rr := httptest.NewRecorder()
	h.ReadinessHandler(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 for PUT, got %d", rr.Code)
	}
}

// ── StatusHandler ─────────────────────────────────────────────────────────────

func TestStatusHandler_AlwaysHTTP200(t *testing.T) {
	h := newTestChecker()
	h.WithSimulatorProbe(func(_ context.Context) ComponentCheck {
		return ComponentCheck{Name: "simulator", Status: HealthDown, Message: "missing"}
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz/status", nil)
	rr := httptest.NewRecorder()
	h.StatusHandler(rr, req)

	// Status always returns 200 — operators inspect the body.
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 from /healthz/status regardless of component state, got %d", rr.Code)
	}
	resp := decodeBody[StatusResponse](t, rr)
	if resp.Status == "" {
		t.Error("expected non-empty status field")
	}
	if resp.Version == "" {
		t.Error("expected non-empty version field")
	}
	if resp.Uptime == "" {
		t.Error("expected non-empty uptime field")
	}
}

func TestStatusHandler_ReportsComponents(t *testing.T) {
	h := newTestChecker()
	h.WithSimulatorProbe(func(_ context.Context) ComponentCheck {
		return ComponentCheck{Name: "simulator", Status: HealthOK}
	})
	h.WithCacheProbe(func(_ context.Context) ComponentCheck {
		return ComponentCheck{Name: "cache", Status: HealthOK}
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz/status", nil)
	rr := httptest.NewRecorder()
	h.StatusHandler(rr, req)

	resp := decodeBody[StatusResponse](t, rr)
	if len(resp.Components) != 2 {
		t.Errorf("expected 2 components, got %d", len(resp.Components))
	}
}

func TestStatusHandler_MethodNotAllowed(t *testing.T) {
	h := newTestChecker()
	req := httptest.NewRequest(http.MethodDelete, "/healthz/status", nil)
	rr := httptest.NewRecorder()
	h.StatusHandler(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

// ── probe timeout ─────────────────────────────────────────────────────────────

func TestReadinessHandler_ProbeTimeoutDoesNotBlock(t *testing.T) {
	h := newTestChecker()
	// Simulate a slow probe — it must time out within the handler's 2s window,
	// not block the test indefinitely.
	h.WithSimulatorProbe(func(ctx context.Context) ComponentCheck {
		select {
		case <-ctx.Done():
			return ComponentCheck{Name: "simulator", Status: HealthDown, Message: "probe timeout"}
		case <-time.After(10 * time.Second):
			return ComponentCheck{Name: "simulator", Status: HealthOK}
		}
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz/ready", nil).
		WithContext(context.Background())
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		h.ReadinessHandler(rr, req)
		close(done)
	}()

	select {
	case <-done:
		// OK — probe timed out within the 2s budget
	case <-time.After(5 * time.Second):
		t.Fatal("readiness handler blocked beyond probe timeout")
	}
}

// ── aggregateStatus ───────────────────────────────────────────────────────────

func TestAggregateStatus_WorstWins(t *testing.T) {
	tests := []struct {
		name     string
		checks   []ComponentCheck
		expected HealthStatus
	}{
		{
			name:     "all ok",
			checks:   []ComponentCheck{{Status: HealthOK}, {Status: HealthOK}},
			expected: HealthOK,
		},
		{
			name:     "one degraded",
			checks:   []ComponentCheck{{Status: HealthOK}, {Status: HealthDegraded}},
			expected: HealthDegraded,
		},
		{
			name:     "one down overrides degraded",
			checks:   []ComponentCheck{{Status: HealthDegraded}, {Status: HealthDown}},
			expected: HealthDown,
		},
		{
			name:     "empty is ok",
			checks:   []ComponentCheck{},
			expected: HealthOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := aggregateStatus(tt.checks)
			if got != tt.expected {
				t.Errorf("aggregateStatus() = %q, want %q", got, tt.expected)
			}
		})
	}
}

// ── RegisterHealthRoutes ──────────────────────────────────────────────────────

func TestRegisterHealthRoutes_AllRoutesReachable(t *testing.T) {
	h := newTestChecker()
	mux := http.NewServeMux()
	RegisterHealthRoutes(mux, h)

	paths := []string{"/healthz/live", "/healthz/ready", "/healthz/status"}
	for _, path := range paths {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code == http.StatusNotFound {
			t.Errorf("route %s not registered (got 404)", path)
		}
	}
}

// ── DefaultProbes ─────────────────────────────────────────────────────────────

func TestDefaultSimulatorProbe_Available(t *testing.T) {
	probe := DefaultSimulatorProbe(func() bool { return true })
	c := probe(context.Background())
	if c.Status != HealthOK {
		t.Errorf("expected ok, got %q: %s", c.Status, c.Message)
	}
	if c.Name != "simulator" {
		t.Errorf("expected name 'simulator', got %q", c.Name)
	}
}

func TestDefaultSimulatorProbe_Unavailable(t *testing.T) {
	probe := DefaultSimulatorProbe(func() bool { return false })
	c := probe(context.Background())
	if c.Status != HealthDown {
		t.Errorf("expected down, got %q", c.Status)
	}
}

func TestDefaultSimulatorProbe_NilFunc(t *testing.T) {
	probe := DefaultSimulatorProbe(nil)
	c := probe(context.Background())
	// nil func means "assume available"
	if c.Status != HealthOK {
		t.Errorf("expected ok for nil availability func, got %q", c.Status)
	}
}

func TestDefaultCacheProbe_Configured(t *testing.T) {
	probe := DefaultCacheProbe("/tmp/glassbox-cache")
	c := probe(context.Background())
	if c.Status != HealthOK {
		t.Errorf("expected ok for configured cache, got %q", c.Status)
	}
}

func TestDefaultCacheProbe_NotConfigured(t *testing.T) {
	probe := DefaultCacheProbe("")
	c := probe(context.Background())
	if c.Status != HealthDegraded {
		t.Errorf("expected degraded for empty cache dir, got %q", c.Status)
	}
}

// ── JSON contract ─────────────────────────────────────────────────────────────

func TestHealthResponseJSON_FieldsPresent(t *testing.T) {
	h := newTestChecker()
	req := httptest.NewRequest(http.MethodGet, "/healthz/live", nil)
	rr := httptest.NewRecorder()
	h.LivenessHandler(rr, req)

	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type should be application/json, got %q", ct)
	}

	var raw map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&raw); err != nil {
		t.Fatalf("body is not valid JSON: %v", err)
	}
	for _, field := range []string{"status", "version", "uptime"} {
		if _, ok := raw[field]; !ok {
			t.Errorf("liveness response missing field %q", field)
		}
	}
}

func TestReadinessResponseJSON_ComponentsReported(t *testing.T) {
	h := newTestChecker()
	h.WithSimulatorProbe(func(_ context.Context) ComponentCheck {
		return ComponentCheck{Name: "simulator", Status: HealthDown, Message: "not found"}
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz/ready", nil)
	rr := httptest.NewRecorder()
	h.ReadinessHandler(rr, req)

	var raw map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&raw); err != nil {
		t.Fatalf("body is not valid JSON: %v", err)
	}

	components, ok := raw["components"].([]interface{})
	if !ok || len(components) == 0 {
		t.Error("readiness response must include components array")
	}

	// Verify first component has name and status
	comp, ok := components[0].(map[string]interface{})
	if !ok {
		t.Fatal("component entry is not an object")
	}
	if comp["name"] != "simulator" {
		t.Errorf("expected component name 'simulator', got %v", comp["name"])
	}
	if comp["status"] != string(HealthDown) {
		t.Errorf("expected component status 'down', got %v", comp["status"])
	}
	if comp["message"] != "not found" {
		t.Errorf("expected component message 'not found', got %v", comp["message"])
	}
}

// ── MarkShuttingDown is safe to call multiple times ───────────────────────────

func TestMarkShuttingDown_Idempotent(t *testing.T) {
	h := newTestChecker()
	h.MarkShuttingDown()
	h.MarkShuttingDown()
	if !h.IsShuttingDown() {
		t.Error("should be shutting down")
	}
}

// ── Active Job Tracking (Issue #826) ─────────────────────────────────────────

func TestActiveJobTracking_IncrDecr(t *testing.T) {
	h := newTestChecker()
	if h.ActiveJobCount() != 0 {
		t.Errorf("expected 0 active jobs initially, got %d", h.ActiveJobCount())
	}
	h.IncrActiveJobs()
	h.IncrActiveJobs()
	if h.ActiveJobCount() != 2 {
		t.Errorf("expected 2 active jobs, got %d", h.ActiveJobCount())
	}
	h.DecrActiveJobs()
	if h.ActiveJobCount() != 1 {
		t.Errorf("expected 1 active job, got %d", h.ActiveJobCount())
	}
}

// ── Drain Deadline (Issue #826) ──────────────────────────────────────────────

func TestDrainDeadline_NotExpiredInitially(t *testing.T) {
	h := newTestChecker()
	if h.DrainExpired() {
		t.Error("should not be expired before shutdown")
	}
}

func TestDrainDeadline_NotExpiredImmediately(t *testing.T) {
	h := newTestChecker()
	h.MarkShuttingDownWithDeadline(10 * time.Second)
	if h.DrainExpired() {
		t.Error("should not be expired immediately after shutdown with 10s deadline")
	}
}

func TestDrainDeadline_ExpiredAfterDeadline(t *testing.T) {
	h := newTestChecker()
	h.MarkShuttingDownWithDeadline(0) // immediate deadline
	time.Sleep(10 * time.Millisecond)
	if !h.DrainExpired() {
		t.Error("should be expired after deadline passes")
	}
}

// ── Readiness shows active jobs during drain ─────────────────────────────────

func TestReadinessHandler_ShutdownShowsActiveJobs(t *testing.T) {
	h := newTestChecker()
	h.MarkShuttingDown()
	h.IncrActiveJobs()
	h.IncrActiveJobs()

	req := httptest.NewRequest(http.MethodGet, "/healthz/ready", nil)
	rr := httptest.NewRecorder()
	h.ReadinessHandler(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 during shutdown, got %d", rr.Code)
	}
	resp := decodeBody[ReadinessResponse](t, rr)
	// The component message should mention active jobs.
	found := false
	for _, c := range resp.Components {
		if c.Name == "daemon" && strings.Contains(c.Message, "2 active jobs") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected component message to mention active jobs, got: %v", resp.Components)
	}
}

// ── Status shows active jobs and drain info ──────────────────────────────────

func TestStatusHandler_ReportsActiveJobsAndDrain(t *testing.T) {
	h := newTestChecker()
	h.MarkShuttingDown()
	h.IncrActiveJobs()

	req := httptest.NewRequest(http.MethodGet, "/healthz/status", nil)
	rr := httptest.NewRecorder()
	h.StatusHandler(rr, req)

	resp := decodeBody[StatusResponse](t, rr)
	if resp.ActiveJobs != 1 {
		t.Errorf("expected 1 active job, got %d", resp.ActiveJobs)
	}
	if !resp.Draining {
		t.Error("expected Draining=true")
	}
	if resp.DrainDeadline == "" {
		t.Error("expected non-empty drain_deadline")
	}
}
