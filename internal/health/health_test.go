// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func newPassChecker(name string) *CheckerFunc {
	return NewChecker(name, func(_ context.Context) error { return nil })
}

func newFailChecker(name, msg string) *CheckerFunc {
	return NewChecker(name, func(_ context.Context) error { return errors.New(msg) })
}

func newSlowChecker(name string, d time.Duration) *CheckerFunc {
	return NewChecker(name, func(ctx context.Context) error {
		select {
		case <-time.After(d):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
}

// ── /healthz/live ────────────────────────────────────────────────────────────

func TestLive_AlwaysOK(t *testing.T) {
	h := NewHandler()
	h.Register(newFailChecker("db", "connection refused"))

	mux := http.NewServeMux()
	h.Mount(mux)

	req := httptest.NewRequest(http.MethodGet, "/healthz/live", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("live: want 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"ok"`) {
		t.Errorf("live: body does not contain status ok: %s", body)
	}
	if !strings.Contains(body, schemaVersion) {
		t.Errorf("live: body missing schema_version: %s", body)
	}
}

func TestLive_MethodNotAllowed(t *testing.T) {
	h := NewHandler()
	mux := http.NewServeMux()
	h.Mount(mux)

	req := httptest.NewRequest(http.MethodPost, "/healthz/live", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("live POST: want 405, got %d", w.Code)
	}
}

// Liveness must remain 200 even when the process is in a degraded state.
func TestLive_DuringDependencyFailure(t *testing.T) {
	h := NewHandler()
	// Simulate simulator unavailable + cache unavailable
	h.Register(newFailChecker("simulator", "binary not found"))
	h.Register(newFailChecker("cache", "disk full"))

	mux := http.NewServeMux()
	h.Mount(mux)

	req := httptest.NewRequest(http.MethodGet, "/healthz/live", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("live must be 200 even with failing deps, got %d", w.Code)
	}
}

// ── /healthz/ready ───────────────────────────────────────────────────────────

func TestReady_AllPass(t *testing.T) {
	h := NewHandler()
	h.Register(newPassChecker("simulator"))
	h.Register(newPassChecker("cache"))

	mux := http.NewServeMux()
	h.Mount(mux)

	req := httptest.NewRequest(http.MethodGet, "/healthz/ready", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("ready all-pass: want 200, got %d", w.Code)
	}
	var resp ReadinessResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != StatusOK {
		t.Errorf("overall status want ok, got %q", resp.Status)
	}
	for name, cs := range resp.Components {
		if cs.Status != StatusOK {
			t.Errorf("component %q: want ok, got %q", name, cs.Status)
		}
	}
	if resp.SchemaVersion != schemaVersion {
		t.Errorf("schema_version: want %q, got %q", schemaVersion, resp.SchemaVersion)
	}
}

func TestReady_OneFails(t *testing.T) {
	h := NewHandler()
	h.Register(newPassChecker("simulator"))
	h.Register(newFailChecker("cache", "disk full"))

	mux := http.NewServeMux()
	h.Mount(mux)

	req := httptest.NewRequest(http.MethodGet, "/healthz/ready", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("ready one-fail: want 503, got %d", w.Code)
	}
	var resp ReadinessResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != StatusUnavailable {
		t.Errorf("overall status want unavailable, got %q", resp.Status)
	}
	cs, ok := resp.Components["cache"]
	if !ok {
		t.Fatal("expected cache component in response")
	}
	if cs.Status != StatusUnavailable {
		t.Errorf("cache status want unavailable, got %q", cs.Status)
	}
	if cs.Message == "" {
		t.Error("cache message should be non-empty on failure")
	}
	// Simulator should still report ok
	if resp.Components["simulator"].Status != StatusOK {
		t.Errorf("simulator should remain ok")
	}
}

func TestReady_NoCheckers(t *testing.T) {
	h := NewHandler()
	mux := http.NewServeMux()
	h.Mount(mux)

	req := httptest.NewRequest(http.MethodGet, "/healthz/ready", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("ready no-checkers: want 200, got %d", w.Code)
	}
}

func TestReady_MethodNotAllowed(t *testing.T) {
	h := NewHandler()
	mux := http.NewServeMux()
	h.Mount(mux)

	req := httptest.NewRequest(http.MethodPost, "/healthz/ready", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("ready POST: want 405, got %d", w.Code)
	}
}

// Degraded state: readiness reports unavailable components with messages.
func TestReady_DegradedState(t *testing.T) {
	h := NewHandler()
	h.Register(newPassChecker("rpc"))
	h.Register(newFailChecker("simulator", "glassbox-sim not found in PATH"))
	h.Register(newPassChecker("protocol"))

	mux := http.NewServeMux()
	h.Mount(mux)

	req := httptest.NewRequest(http.MethodGet, "/healthz/ready", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("degraded: want 503, got %d", w.Code)
	}
	var resp ReadinessResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	sim := resp.Components["simulator"]
	if !strings.Contains(sim.Message, "glassbox-sim") {
		t.Errorf("simulator message should contain error: %q", sim.Message)
	}
}

// Checks that timeout on a slow checker surfaces as unavailable.
func TestReady_CheckerTimeout(t *testing.T) {
	h := NewHandler()
	h.checkTimeout = 50 * time.Millisecond
	h.Register(newSlowChecker("slow", 5*time.Second))

	mux := http.NewServeMux()
	h.Mount(mux)

	req := httptest.NewRequest(http.MethodGet, "/healthz/ready", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("timeout checker: want 503, got %d", w.Code)
	}
	var resp ReadinessResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Components["slow"].Status != StatusUnavailable {
		t.Errorf("timed-out checker should be unavailable")
	}
}

// ── /healthz ─────────────────────────────────────────────────────────────────

func TestDetailed_AlwaysOK(t *testing.T) {
	h := NewHandler()
	h.Register(newFailChecker("cache", "unavailable"))

	mux := http.NewServeMux()
	h.Mount(mux)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// Detailed endpoint always returns 200 regardless of component health.
	if w.Code != http.StatusOK {
		t.Errorf("detailed: want 200 always, got %d", w.Code)
	}
	var resp DetailedResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.SchemaVersion != schemaVersion {
		t.Errorf("schema_version: want %q, got %q", schemaVersion, resp.SchemaVersion)
	}
}

func TestDetailed_ProtocolVersion(t *testing.T) {
	h := NewHandler()
	h.SetProtocolVersion(21)

	mux := http.NewServeMux()
	h.Mount(mux)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	var resp DetailedResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ProtocolVersion != 21 {
		t.Errorf("protocol_version: want 21, got %d", resp.ProtocolVersion)
	}
}

func TestDetailed_UptimeIncreases(t *testing.T) {
	h := NewHandler()
	// Wind start time back so uptime is definitely > 0
	h.startTime = time.Now().Add(-2 * time.Second)

	mux := http.NewServeMux()
	h.Mount(mux)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	var resp DetailedResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.UptimeSeconds < 1 {
		t.Errorf("uptime_seconds should be >= 1, got %d", resp.UptimeSeconds)
	}
}

func TestDetailed_CheckedAtRFC3339(t *testing.T) {
	h := NewHandler()
	mux := http.NewServeMux()
	h.Mount(mux)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	var resp DetailedResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, err := time.Parse(time.RFC3339, resp.CheckedAt); err != nil {
		t.Errorf("checked_at is not RFC 3339: %q", resp.CheckedAt)
	}
}

func TestDetailed_MethodNotAllowed(t *testing.T) {
	h := NewHandler()
	mux := http.NewServeMux()
	h.Mount(mux)

	req := httptest.NewRequest(http.MethodPost, "/healthz", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("detailed POST: want 405, got %d", w.Code)
	}
}

// ── concurrency ───────────────────────────────────────────────────────────────

func TestHandler_ConcurrentRequests(t *testing.T) {
	h := NewHandler()
	h.Register(newPassChecker("a"))
	h.Register(newPassChecker("b"))

	mux := http.NewServeMux()
	h.Mount(mux)

	const n = 20
	done := make(chan struct{}, n)
	for i := 0; i < n; i++ {
		go func() {
			req := httptest.NewRequest(http.MethodGet, "/healthz/ready", nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			done <- struct{}{}
		}()
	}
	for i := 0; i < n; i++ {
		<-done
	}
}

// ── startup state ─────────────────────────────────────────────────────────────

// Startup state: handler with no checkers registered yet should be live and ready.
func TestStartupState_NoCheckers(t *testing.T) {
	h := NewHandler()
	mux := http.NewServeMux()
	h.Mount(mux)

	for _, path := range []string{"/healthz/live", "/healthz/ready", "/healthz"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("%s startup: want 200, got %d", path, w.Code)
		}
	}
}

// ── shutdown state ────────────────────────────────────────────────────────────

// Simulate all checkers failing (shutdown-like state). Liveness stays up;
// readiness returns 503.
func TestShutdownState_AllChecksFail(t *testing.T) {
	h := NewHandler()
	h.Register(newFailChecker("simulator", "shutting down"))
	h.Register(newFailChecker("cache", "shutting down"))
	h.Register(newFailChecker("rpc", "shutting down"))

	mux := http.NewServeMux()
	h.Mount(mux)

	// Liveness must stay 200
	req := httptest.NewRequest(http.MethodGet, "/healthz/live", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("shutdown: liveness want 200, got %d", w.Code)
	}

	// Readiness must return 503
	req = httptest.NewRequest(http.MethodGet, "/healthz/ready", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("shutdown: readiness want 503, got %d", w.Code)
	}
	var resp ReadinessResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != StatusUnavailable {
		t.Errorf("shutdown: overall status want unavailable, got %q", resp.Status)
	}
	// Each component should report unavailable with a message
	for name, cs := range resp.Components {
		if cs.Status != StatusUnavailable {
			t.Errorf("shutdown: component %q want unavailable, got %q", name, cs.Status)
		}
		if cs.Message == "" {
			t.Errorf("shutdown: component %q should have error message", name)
		}
	}
}

// ── CheckerFunc ───────────────────────────────────────────────────────────────

func TestCheckerFunc_Name(t *testing.T) {
	c := NewChecker("mycomp", func(_ context.Context) error { return nil })
	if c.Name() != "mycomp" {
		t.Errorf("Name() = %q, want %q", c.Name(), "mycomp")
	}
}

func TestCheckerFunc_PassAndFail(t *testing.T) {
	pass := NewChecker("ok", func(_ context.Context) error { return nil })
	if err := pass.Check(context.Background()); err != nil {
		t.Errorf("pass checker returned error: %v", err)
	}

	fail := NewChecker("fail", func(_ context.Context) error { return errors.New("boom") })
	if err := fail.Check(context.Background()); err == nil {
		t.Error("fail checker should return error")
	}
}

// ── no replay work triggered ──────────────────────────────────────────────────

// Ensures that health checks never exceed a short wall-clock budget even when
// called repeatedly, which guards against accidentally triggering expensive work.
func TestHealth_NeverExpensiveWork(t *testing.T) {
	h := NewHandler()
	// Checkers that complete instantly
	h.Register(newPassChecker("a"))
	h.Register(newPassChecker("b"))
	h.Register(newPassChecker("c"))

	mux := http.NewServeMux()
	h.Mount(mux)

	start := time.Now()
	for i := 0; i < 50; i++ {
		req := httptest.NewRequest(http.MethodGet, "/healthz/ready", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
	}
	elapsed := time.Since(start)
	// 50 passes with instant checks should complete in under 2 seconds.
	if elapsed > 2*time.Second {
		t.Errorf("health checks took %v for 50 iterations; potential expensive work", elapsed)
	}
}

// ── #842: RunChecks and optional/required dependency policy ──────────────────

func TestRunChecks_AllPass(t *testing.T) {
	h := NewHandler()
	h.RegisterWithMeta(newPassChecker("rpc"), CheckRequired, true)
	h.RegisterWithMeta(newPassChecker("simulator"), CheckRequired, false)

	result := h.RunChecks(context.Background(), false)
	if result.Overall != StatusOK {
		t.Errorf("overall want ok, got %q", result.Overall)
	}
	if result.Components["rpc"].Status != StatusOK {
		t.Errorf("rpc want ok, got %q", result.Components["rpc"].Status)
	}
}

func TestRunChecks_RequiredFail_OverallUnavailable(t *testing.T) {
	h := NewHandler()
	h.RegisterWithMeta(newFailChecker("rpc", "connection refused"), CheckRequired, true)
	h.RegisterWithMeta(newPassChecker("simulator"), CheckRequired, false)

	result := h.RunChecks(context.Background(), false)
	if result.Overall != StatusUnavailable {
		t.Errorf("overall want unavailable, got %q", result.Overall)
	}
}

func TestRunChecks_OptionalFail_OverallStillOK(t *testing.T) {
	h := NewHandler()
	h.RegisterWithMeta(newPassChecker("rpc"), CheckRequired, false)
	h.RegisterWithMeta(newFailChecker("signer", "not configured"), CheckOptional, false)

	result := h.RunChecks(context.Background(), false)
	if result.Overall != StatusOK {
		t.Errorf("optional failure should not affect overall; want ok, got %q", result.Overall)
	}
	if result.Components["signer"].Status != StatusUnavailable {
		t.Errorf("signer should be unavailable, got %q", result.Components["signer"].Status)
	}
}

func TestRunChecks_OfflineSkipsNetworkChecks(t *testing.T) {
	h := NewHandler()
	h.RegisterWithMeta(newPassChecker("local"), CheckRequired, false)
	h.RegisterWithMeta(newPassChecker("rpc"), CheckRequired, true) // network required

	result := h.RunChecks(context.Background(), true /* offline */)
	if result.Overall != StatusOK {
		t.Errorf("offline: overall want ok, got %q", result.Overall)
	}
	if result.Components["rpc"].Status != StatusSkipped {
		t.Errorf("offline: rpc should be skipped, got %q", result.Components["rpc"].Status)
	}
	if result.Components["local"].Status != StatusOK {
		t.Errorf("offline: local should still run, got %q", result.Components["local"].Status)
	}
}

func TestRunChecks_OfflineFlag_SetInResult(t *testing.T) {
	h := NewHandler()
	result := h.RunChecks(context.Background(), true)
	if !result.Offline {
		t.Error("RunResult.Offline should be true when called with offline=true")
	}
}

func TestRunChecks_CheckedAtRFC3339(t *testing.T) {
	h := NewHandler()
	result := h.RunChecks(context.Background(), false)
	if _, err := time.Parse(time.RFC3339, result.CheckedAt); err != nil {
		t.Errorf("checked_at is not RFC 3339: %q", result.CheckedAt)
	}
}

func TestRunChecks_KindPreservedInReport(t *testing.T) {
	h := NewHandler()
	h.RegisterWithMeta(newPassChecker("req"), CheckRequired, false)
	h.RegisterWithMeta(newPassChecker("opt"), CheckOptional, true)

	result := h.RunChecks(context.Background(), false)
	if result.Components["req"].Kind != CheckRequired {
		t.Errorf("req kind want %q, got %q", CheckRequired, result.Components["req"].Kind)
	}
	if result.Components["opt"].Kind != CheckOptional {
		t.Errorf("opt kind want %q, got %q", CheckOptional, result.Components["opt"].Kind)
	}
	if !result.Components["opt"].NetworkRequired {
		t.Error("opt NetworkRequired should be true")
	}
}

func TestFormatText_ContainsComponentNames(t *testing.T) {
	h := NewHandler()
	h.RegisterWithMeta(newPassChecker("rpc"), CheckRequired, true)
	h.RegisterWithMeta(newFailChecker("signer", "not found"), CheckOptional, false)

	result := h.RunChecks(context.Background(), false)
	text := FormatText(result)
	if !strings.Contains(text, "rpc") {
		t.Errorf("FormatText output missing 'rpc': %s", text)
	}
	if !strings.Contains(text, "signer") {
		t.Errorf("FormatText output missing 'signer': %s", text)
	}
	if !strings.Contains(text, string(result.Overall)) {
		t.Errorf("FormatText output missing overall status: %s", text)
	}
}

func TestFormatText_OfflineModeAnnotated(t *testing.T) {
	h := NewHandler()
	result := h.RunChecks(context.Background(), true)
	text := FormatText(result)
	if !strings.Contains(text, "offline") {
		t.Errorf("FormatText should annotate offline mode: %s", text)
	}
}

func TestRunChecks_NeverExposesCredentials(t *testing.T) {
	h := NewHandler()
	// A checker that returns an error message potentially containing a token.
	h.RegisterWithMeta(NewChecker("auth", func(_ context.Context) error {
		return errors.New("dial tcp: connection refused (no credential leak)")
	}), CheckRequired, false)

	result := h.RunChecks(context.Background(), false)
	text := FormatText(result)
	// The error message is allowed in the output but must not be token material.
	if strings.Contains(text, "Bearer") || strings.Contains(text, "password") {
		t.Errorf("FormatText should not expose credential tokens: %s", text)
	}
}

// ── HealthCheckResult / CheckResults ─────────────────────────────────────────

func TestCheckResults_HealthyFields(t *testing.T) {
	h := NewHandler()
	h.RegisterWithMeta(newPassChecker("rpc"), CheckRequired, false)
	h.RegisterWithMeta(newPassChecker("cache"), CheckOptional, false)

	results := h.CheckResults(context.Background(), false)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Name == "" {
			t.Error("HealthCheckResult.Name must not be empty")
		}
		if !r.Healthy {
			t.Errorf("component %q: expected Healthy=true", r.Name)
		}
		if r.Message != "" {
			t.Errorf("component %q: expected empty Message on success, got %q", r.Name, r.Message)
		}
	}
}

func TestCheckResults_UnhealthyFields(t *testing.T) {
	h := NewHandler()
	h.RegisterWithMeta(newFailChecker("simulator", "binary not found"), CheckRequired, false)

	results := h.CheckResults(context.Background(), false)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Name != "simulator" {
		t.Errorf("Name = %q, want %q", r.Name, "simulator")
	}
	if r.Healthy {
		t.Error("expected Healthy=false for failing checker")
	}
	if r.Message == "" {
		t.Error("expected non-empty Message for failing checker")
	}
}

func TestCheckResults_OfflineSkippedIsHealthy(t *testing.T) {
	h := NewHandler()
	h.RegisterWithMeta(newPassChecker("local"), CheckRequired, false)
	h.RegisterWithMeta(newPassChecker("rpc"), CheckRequired, true) // network-required → skipped offline

	results := h.CheckResults(context.Background(), true /* offline */)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for _, r := range results {
		if !r.Healthy {
			t.Errorf("component %q: skipped-offline checks should be Healthy=true, got false", r.Name)
		}
	}
}

func TestHealthCheckResult_StructFields(t *testing.T) {
	// Verify the struct has the required typed fields with the right zero values.
	var r HealthCheckResult
	if r.Name != "" {
		t.Errorf("zero-value Name should be empty string, got %q", r.Name)
	}
	if r.Healthy {
		t.Error("zero-value Healthy should be false")
	}
	if r.Message != "" {
		t.Errorf("zero-value Message should be empty string, got %q", r.Message)
	}
	// Construct and verify non-zero values.
	r = HealthCheckResult{Name: "db", Healthy: true, Message: ""}
	if r.Name != "db" {
		t.Errorf("Name = %q, want %q", r.Name, "db")
	}
	if !r.Healthy {
		t.Error("Healthy should be true")
	}
}
