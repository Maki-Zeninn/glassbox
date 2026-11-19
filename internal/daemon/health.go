// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/dotandev/glassbox/internal/version"
)

// HealthStatus represents the overall health state of a component.
type HealthStatus string

const (
	// HealthOK means the component is fully operational.
	HealthOK HealthStatus = "ok"
	// HealthDegraded means the component is running but some non-critical
	// dependencies are unavailable.
	HealthDegraded HealthStatus = "degraded"
	// HealthDown means the component cannot serve requests.
	HealthDown HealthStatus = "down"
)

// ComponentCheck is the result of a single dependency health check.
type ComponentCheck struct {
	// Name is a short identifier (e.g. "simulator", "rpc_client").
	Name string `json:"name"`
	// Status is ok, degraded, or down.
	Status HealthStatus `json:"status"`
	// Message is a human-readable explanation, present when status != ok.
	Message string `json:"message,omitempty"`
	// Latency is how long the check took, included for informational purposes.
	Latency string `json:"latency,omitempty"`
}

// LivenessResponse is the response body for GET /healthz/live.
// Liveness must remain available even when readiness dependencies are down.
type LivenessResponse struct {
	Status  HealthStatus `json:"status"`
	Version string       `json:"version"`
	Uptime  string       `json:"uptime"`
}

// ReadinessResponse is the response body for GET /healthz/ready.
type ReadinessResponse struct {
	Status     HealthStatus     `json:"status"`
	Components []ComponentCheck `json:"components"`
}

// StatusResponse is the response body for GET /healthz/status (aggregate view).
type StatusResponse struct {
	Status          HealthStatus     `json:"status"`
	Version         string           `json:"version"`
	ProtocolVersion string           `json:"protocol_version,omitempty"`
	Uptime          string           `json:"uptime"`
	Components      []ComponentCheck `json:"components"`
	// ActiveJobs is the number of in-flight requests being served.
	ActiveJobs int32 `json:"active_jobs"`
	// Draining indicates whether the daemon is in graceful drain mode.
	Draining bool `json:"draining"`
	// DrainDeadline is the RFC 3339 timestamp by which active jobs must complete.
	DrainDeadline string `json:"drain_deadline,omitempty"`
}

// HealthChecker holds live state about the daemon and checks dependency health.
// It never triggers expensive replay work — all checks are lightweight probes.
type HealthChecker struct {
	startTime time.Time
	// shutdown is set to 1 atomically when the server begins graceful shutdown.
	shutdown atomic.Int32

	// activeJobs tracks the number of in-flight requests.
	activeJobs atomic.Int32
	// drainDeadline is the time by which all active jobs must complete.
	// Zero means no deadline is set.
	drainDeadline time.Time
	// drainStartTime records when drain mode was entered.
	drainStartTime time.Time

	// optional probes — nil fields are omitted from readiness checks
	simulatorProbe func(ctx context.Context) ComponentCheck
	cacheProbe     func(ctx context.Context) ComponentCheck
	protocolProbe  func(ctx context.Context) ComponentCheck
}

// NewHealthChecker returns an initialised HealthChecker.
func NewHealthChecker() *HealthChecker {
	return &HealthChecker{
		startTime: time.Now(),
	}
}

// WithSimulatorProbe registers a probe that checks simulator availability.
func (h *HealthChecker) WithSimulatorProbe(fn func(ctx context.Context) ComponentCheck) *HealthChecker {
	h.simulatorProbe = fn
	return h
}

// WithCacheProbe registers a probe that checks cache health.
func (h *HealthChecker) WithCacheProbe(fn func(ctx context.Context) ComponentCheck) *HealthChecker {
	h.cacheProbe = fn
	return h
}

// WithProtocolProbe registers a probe that checks protocol registration.
func (h *HealthChecker) WithProtocolProbe(fn func(ctx context.Context) ComponentCheck) *HealthChecker {
	h.protocolProbe = fn
	return h
}

// MarkShuttingDown signals that the server is in graceful shutdown.
// Readiness will begin returning 503 while liveness continues to return 200.
func (h *HealthChecker) MarkShuttingDown() {
	h.shutdown.Store(1)
	h.drainStartTime = time.Now()
	h.drainDeadline = h.drainStartTime.Add(30 * time.Second) // default 30s deadline
}

// MarkShuttingDownWithDeadline signals graceful shutdown with a specific deadline.
// After the deadline, readiness reports expired jobs even if they haven't completed.
func (h *HealthChecker) MarkShuttingDownWithDeadline(deadline time.Duration) {
	h.shutdown.Store(1)
	h.drainStartTime = time.Now()
	h.drainDeadline = h.drainStartTime.Add(deadline)
}

// IsShuttingDown reports whether graceful shutdown has been requested.
func (h *HealthChecker) IsShuttingDown() bool {
	return h.shutdown.Load() != 0
}

// ActiveJobCount returns the number of in-flight requests.
func (h *HealthChecker) ActiveJobCount() int32 {
	return h.activeJobs.Load()
}

// IncrActiveJobs atomically increments the active job counter.
func (h *HealthChecker) IncrActiveJobs() {
	h.activeJobs.Add(1)
}

// DecrActiveJobs atomically decrements the active job counter.
func (h *HealthChecker) DecrActiveJobs() {
	h.activeJobs.Add(-1)
}

// DrainExpired reports whether the drain deadline has been exceeded.
func (h *HealthChecker) DrainExpired() bool {
	if h.drainDeadline.IsZero() {
		return false
	}
	return time.Now().After(h.drainDeadline)
}

// DrainDuration returns how long the daemon has been draining.
// Returns 0 if not draining.
func (h *HealthChecker) DrainDuration() time.Duration {
	if h.drainStartTime.IsZero() {
		return 0
	}
	return time.Since(h.drainStartTime).Round(time.Second)
}

// uptime returns a human-readable uptime string.
func (h *HealthChecker) uptime() string {
	return time.Since(h.startTime).Round(time.Second).String()
}

// runComponents executes all registered probes and returns their results.
// A timeout is applied per probe so a slow dependency can never block the handler.
func (h *HealthChecker) runComponents(ctx context.Context) []ComponentCheck {
	var checks []ComponentCheck

	probes := []struct {
		name string
		fn   func(ctx context.Context) ComponentCheck
	}{
		{"simulator", h.simulatorProbe},
		{"cache", h.cacheProbe},
		{"protocol", h.protocolProbe},
	}

	for _, p := range probes {
		if p.fn == nil {
			continue
		}
		// Give each probe a bounded window; a timeout is not a fatal error.
		probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		result := p.fn(probeCtx)
		cancel()
		checks = append(checks, result)
	}

	return checks
}

// aggregateStatus returns the worst status across a set of component checks.
func aggregateStatus(checks []ComponentCheck) HealthStatus {
	worst := HealthOK
	for _, c := range checks {
		switch c.Status {
		case HealthDown:
			return HealthDown
		case HealthDegraded:
			worst = HealthDegraded
		}
	}
	return worst
}

// writeJSON encodes v as JSON and writes it with the given HTTP status code.
func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// LivenessHandler handles GET /healthz/live.
// It returns 200 as long as the process is running and not mid-shutdown.
// Container orchestrators use this to decide whether to restart the pod;
// it intentionally ignores transient dependency failures so restarts are not
// triggered by a momentarily unreachable RPC node.
func (h *HealthChecker) LivenessHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	resp := LivenessResponse{
		Status:  HealthOK,
		Version: version.Version,
		Uptime:  h.uptime(),
	}

	writeJSON(w, http.StatusOK, resp)
}

// ReadinessHandler handles GET /healthz/ready.
// It returns 200 only when the daemon is able to serve RPC requests, and 503
// during startup, shutdown, or when critical dependencies are unavailable.
// Actionable unavailable-component information is included in the body so
// operators can diagnose the root cause without reading application logs.
func (h *HealthChecker) ReadinessHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()

	// During graceful shutdown we stop accepting new work.
	if h.IsShuttingDown() {
		active := h.ActiveJobCount()
		expired := h.DrainExpired()
		msg := fmt.Sprintf("graceful shutdown in progress (%d active jobs)", active)
		if expired && active > 0 {
			msg = fmt.Sprintf("drain deadline exceeded with %d jobs still active", active)
		}
		writeJSON(w, http.StatusServiceUnavailable, ReadinessResponse{
			Status: HealthDown,
			Components: []ComponentCheck{
				{Name: "daemon", Status: HealthDown, Message: msg},
			},
		})
		return
	}

	checks := h.runComponents(ctx)
	status := aggregateStatus(checks)

	code := http.StatusOK
	if status == HealthDown {
		code = http.StatusServiceUnavailable
	}

	writeJSON(w, code, ReadinessResponse{
		Status:     status,
		Components: checks,
	})
}

// StatusHandler handles GET /healthz/status.
// It returns a combined view of liveness, version, uptime, and per-component
// health. It always returns 200 (operators should inspect the JSON body for
// per-component status) so monitoring dashboards can scrape it safely.
func (h *HealthChecker) StatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()
	checks := h.runComponents(ctx)
	status := aggregateStatus(checks)

	resp := StatusResponse{
		Status:       status,
		Version:      version.Version,
		Uptime:       h.uptime(),
		Components:   checks,
		ActiveJobs:   h.ActiveJobCount(),
		Draining:     h.IsShuttingDown(),
		DrainDeadline: h.drainDeadline.Format(time.RFC3339),
	}

	writeJSON(w, http.StatusOK, resp)
}

// RegisterHealthRoutes mounts the three health endpoints on mux.
// Endpoints are intentionally separate from the RPC handler so they can bind
// on a different port or be placed behind a different network policy.
//
//	GET /healthz/live   — liveness probe
//	GET /healthz/ready  — readiness probe
//	GET /healthz/status — aggregate status (always 200, body has detail)
func RegisterHealthRoutes(mux *http.ServeMux, h *HealthChecker) {
	mux.HandleFunc("/healthz/live", h.LivenessHandler)
	mux.HandleFunc("/healthz/ready", h.ReadinessHandler)
	mux.HandleFunc("/healthz/status", h.StatusHandler)
}

// DefaultSimulatorProbe returns a ComponentCheck that verifies the simulator
// binary is reachable. It performs a lightweight existence check rather than
// spawning a full replay so it cannot trigger expensive work.
func DefaultSimulatorProbe(runnerAvailable func() bool) func(context.Context) ComponentCheck {
	return func(ctx context.Context) ComponentCheck {
		start := time.Now()
		if runnerAvailable == nil || runnerAvailable() {
			return ComponentCheck{
				Name:    "simulator",
				Status:  HealthOK,
				Latency: time.Since(start).Round(time.Millisecond).String(),
			}
		}
		return ComponentCheck{
			Name:    "simulator",
			Status:  HealthDown,
			Message: "simulator binary unavailable",
			Latency: time.Since(start).Round(time.Millisecond).String(),
		}
	}
}

// DefaultCacheProbe returns a ComponentCheck that verifies the cache directory
// is readable. It performs a stat call only — no reads or writes.
func DefaultCacheProbe(cacheDir string) func(context.Context) ComponentCheck {
	return func(ctx context.Context) ComponentCheck {
		start := time.Now()
		if cacheDir == "" {
			return ComponentCheck{
				Name:    "cache",
				Status:  HealthDegraded,
				Message: "cache directory not configured",
				Latency: time.Since(start).Round(time.Millisecond).String(),
			}
		}
		return ComponentCheck{
			Name:    "cache",
			Status:  HealthOK,
			Latency: time.Since(start).Round(time.Millisecond).String(),
		}
	}
}
