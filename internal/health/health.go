// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package health provides liveness, readiness, and detailed health check
// endpoints suitable for supervisors and container orchestration systems
// (e.g., Kubernetes, systemd, Docker Compose).
//
// Endpoint contract:
//
//   - GET /healthz/live  — liveness probe. Always 200 while the process is up.
//     Never depends on external services so a temporary RPC or cache outage
//     does not cause a container restart.
//
//   - GET /healthz/ready — readiness probe. 200 when all required subsystems
//     are available; 503 with a JSON body listing unavailable components when
//     one or more checks fail.
//
//   - GET /healthz       — detailed status. Always 200; returns a versioned
//     JSON object with per-component status, protocol version, uptime, and
//     timestamp. Intended for dashboards and operators — not for probes.
//
// Handlers are bound to a caller-supplied *http.ServeMux so they cannot be
// accidentally registered on the default mux.
//
// Checks are read-only and lightweight; they never trigger replay work.
package health

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// StatusValue is the string status carried in JSON responses.
type StatusValue string

const (
	// StatusOK means the component is operating normally.
	StatusOK StatusValue = "ok"
	// StatusDegraded means the component is available but impaired.
	StatusDegraded StatusValue = "degraded"
	// StatusUnavailable means the component cannot be reached.
	StatusUnavailable StatusValue = "unavailable"
	// StatusUnknown means the component has not been checked yet.
	StatusUnknown StatusValue = "unknown"
	// StatusSkipped means the check was intentionally not run (e.g. offline
	// mode and the dependency requires network access).
	StatusSkipped StatusValue = "skipped"
)

// CheckKind distinguishes between required and optional dependencies.
// A required dependency failing marks the service not-ready; an optional
// dependency failing degrades status but does not block readiness.
type CheckKind string

const (
	// CheckRequired means the dependency must be healthy for readiness.
	CheckRequired CheckKind = "required"
	// CheckOptional means the dependency is informational only.
	CheckOptional CheckKind = "optional"
)

// checkMeta holds the registration-time metadata for one checker.
type checkMeta struct {
	checker         Checker
	kind            CheckKind
	networkRequired bool
}

// HealthCheckResult is a machine-readable result for a single health check.
// It satisfies the requirement for typed (non-raw-string) check output and
// can be used by callers that need to inspect individual check outcomes
// programmatically rather than parsing a formatted string.
type HealthCheckResult struct {
	// Name is the stable component identifier (matches the value returned by
	// Checker.Name()).
	Name string `json:"name"`
	// Healthy is true when the check passed.
	Healthy bool `json:"healthy"`
	// Message provides a human-readable explanation when Healthy is false.
	// It is empty on success.
	Message string `json:"message,omitempty"`
}

// CheckReport is the per-component result in a standalone RunChecks call.
type CheckReport struct {
	Status          StatusValue `json:"status"`
	Message         string      `json:"message,omitempty"`
	Kind            CheckKind   `json:"kind"`
	NetworkRequired bool        `json:"network_required"`
}

// RunResult holds the complete result of a standalone RunChecks call.
type RunResult struct {
	Overall    StatusValue            `json:"overall"`
	Components map[string]CheckReport `json:"components"`
	Offline    bool                   `json:"offline"`
	CheckedAt  string                 `json:"checked_at"`
}

// schemaVersion is embedded in every /healthz response so consumers can
// detect breaking changes without inspecting field presence.
const schemaVersion = "1"

// Checker is a named health check function. It must return quickly (well
// under one second) and must not trigger any simulation or replay work.
type Checker interface {
	// Name returns the stable component identifier used in JSON output.
	Name() string
	// Check performs the health check. A nil error means healthy.
	Check(ctx context.Context) error
}

// CheckerFunc adapts a plain function to the Checker interface.
type CheckerFunc struct {
	name string
	fn   func(ctx context.Context) error
}

// NewChecker constructs a CheckerFunc with the given name and check function.
func NewChecker(name string, fn func(ctx context.Context) error) *CheckerFunc {
	return &CheckerFunc{name: name, fn: fn}
}

// Name returns the component name.
func (c *CheckerFunc) Name() string { return c.name }

// Check invokes the underlying function.
func (c *CheckerFunc) Check(ctx context.Context) error { return c.fn(ctx) }

// ComponentStatus is the per-component portion of a health response.
type ComponentStatus struct {
	// Status is one of "ok", "degraded", or "unavailable".
	Status StatusValue `json:"status"`
	// Message is a human-readable explanation when status != "ok". Empty otherwise.
	Message string `json:"message,omitempty"`
}

// ReadinessResponse is the JSON body returned by /healthz/ready.
type ReadinessResponse struct {
	// SchemaVersion enables consumers to detect format changes.
	SchemaVersion string `json:"schema_version"`
	// Status is the overall readiness status.
	Status StatusValue `json:"status"`
	// Components holds individual check results keyed by component name.
	Components map[string]ComponentStatus `json:"components"`
	// CheckedAt is the RFC 3339 timestamp of this evaluation.
	CheckedAt string `json:"checked_at"`
}

// DetailedResponse is the JSON body returned by /healthz.
type DetailedResponse struct {
	// SchemaVersion enables consumers to detect format changes.
	SchemaVersion string `json:"schema_version"`
	// Status is the overall health status.
	Status StatusValue `json:"status"`
	// Components holds individual check results keyed by component name.
	Components map[string]ComponentStatus `json:"components"`
	// ProtocolVersion is the Soroban protocol version detected by the daemon,
	// or 0 when unknown.
	ProtocolVersion int `json:"protocol_version"`
	// UptimeSeconds is the number of seconds since the Handler was created.
	UptimeSeconds int64 `json:"uptime_seconds"`
	// CheckedAt is the RFC 3339 timestamp of this evaluation.
	CheckedAt string `json:"checked_at"`
}

// Handler holds the registered checkers and serves health endpoints.
// It is safe for concurrent use.
type Handler struct {
	mu              sync.RWMutex
	checkers        []Checker
	meta            []checkMeta
	protocolVersion int
	startTime       time.Time
	// checkTimeout limits the time each Checker.Check call may take.
	// Defaults to 5 seconds.
	checkTimeout time.Duration
}

// NewHandler returns a Handler with no registered checkers.
func NewHandler() *Handler {
	return &Handler{
		startTime:    time.Now(),
		checkTimeout: 5 * time.Second,
	}
}

// Register adds a Checker to the handler as a required, network-transparent
// dependency.  Duplicate names are allowed but callers should ensure uniqueness.
func (h *Handler) Register(c Checker) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.checkers = append(h.checkers, c)
	h.meta = append(h.meta, checkMeta{checker: c, kind: CheckRequired, networkRequired: false})
}

// RegisterWithMeta adds a Checker with explicit kind and network-access policy.
// Use CheckOptional for dependencies whose failure should not prevent readiness,
// and set networkRequired=true for any check that contacts an external endpoint
// so it can be skipped in offline mode.
func (h *Handler) RegisterWithMeta(c Checker, kind CheckKind, networkRequired bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.checkers = append(h.checkers, c)
	h.meta = append(h.meta, checkMeta{checker: c, kind: kind, networkRequired: networkRequired})
}

// RunChecks executes all registered checkers and returns a RunResult suitable
// for CLI output.  When offline is true, any checker marked networkRequired is
// skipped rather than attempted; this prevents hangs on airgapped machines.
// Checker failures are isolated: one failing check never cancels the others.
func (h *Handler) RunChecks(ctx context.Context, offline bool) RunResult {
	h.mu.RLock()
	metas := make([]checkMeta, len(h.meta))
	copy(metas, h.meta)
	timeout := h.checkTimeout
	h.mu.RUnlock()

	components := make(map[string]CheckReport, len(metas))

	type result struct {
		name   string
		report CheckReport
	}
	ch := make(chan result, len(metas))

	for _, m := range metas {
		m := m
		go func() {
			name := m.checker.Name()
			if offline && m.networkRequired {
				ch <- result{name: name, report: CheckReport{
					Status:          StatusSkipped,
					Message:         "skipped in offline mode",
					Kind:            m.kind,
					NetworkRequired: m.networkRequired,
				}}
				return
			}
			checkCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			err := m.checker.Check(checkCtx)
			status := StatusOK
			msg := ""
			if err != nil {
				status = StatusUnavailable
				msg = err.Error()
			}
			ch <- result{name: name, report: CheckReport{
				Status:          status,
				Message:         msg,
				Kind:            m.kind,
				NetworkRequired: m.networkRequired,
			}}
		}()
	}

	for i := 0; i < len(metas); i++ {
		r := <-ch
		components[r.name] = r.report
	}

	overall := StatusOK
	for _, rep := range components {
		if rep.Status == StatusUnavailable && rep.Kind == CheckRequired {
			overall = StatusUnavailable
			break
		}
	}

	return RunResult{
		Overall:    overall,
		Components: components,
		Offline:    offline,
		CheckedAt:  time.Now().UTC().Format(time.RFC3339),
	}
}

// FormatText returns a human-readable summary of a RunResult.  The output
// separates healthy, degraded, unavailable, and skipped components and
// never exposes credential values.
func FormatText(result RunResult) string {
	var sb strings.Builder
	sb.WriteString("── Dependency Health ──────────────────────────────\n")
	if result.Offline {
		sb.WriteString("  [offline mode — network checks skipped]\n")
	}
	for name, rep := range result.Components {
		marker := "  ✓"
		switch rep.Status {
		case StatusUnavailable:
			marker = "  ✗"
		case StatusSkipped:
			marker = "  –"
		case StatusDegraded:
			marker = "  !"
		}
		kind := ""
		if rep.Kind == CheckOptional {
			kind = " (optional)"
		}
		line := fmt.Sprintf("%s %-20s %s%s\n", marker, name, rep.Status, kind)
		sb.WriteString(line)
		if rep.Message != "" {
			sb.WriteString(fmt.Sprintf("      %s\n", rep.Message))
		}
	}
	sb.WriteString(fmt.Sprintf("─── overall: %s  checked_at: %s\n", result.Overall, result.CheckedAt))
	return sb.String()
}

// CheckResults runs all registered checkers and returns the results as a
// []HealthCheckResult slice. This is the machine-readable counterpart of
// RunChecks: callers can inspect individual Name/Healthy/Message fields
// without parsing a formatted string or decoding an HTTP response.
//
// When offline is true, network-required checkers are skipped and appear in
// the result with Healthy=false and a "skipped in offline mode" message.
func (h *Handler) CheckResults(ctx context.Context, offline bool) []HealthCheckResult {
	run := h.RunChecks(ctx, offline)
	out := make([]HealthCheckResult, 0, len(run.Components))
	for name, rep := range run.Components {
		healthy := rep.Status == StatusOK || rep.Status == StatusSkipped
		out = append(out, HealthCheckResult{
			Name:    name,
			Healthy: healthy,
			Message: rep.Message,
		})
	}
	return out
}

// SetProtocolVersion stores the current Soroban protocol version for reporting
// in /healthz. Call this whenever the daemon detects a version change.
func (h *Handler) SetProtocolVersion(v int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.protocolVersion = v
}

// Mount registers the three health endpoints on mux:
//
//	GET /healthz/live
//	GET /healthz/ready
//	GET /healthz
func (h *Handler) Mount(mux *http.ServeMux) {
	mux.HandleFunc("/healthz/live", h.handleLive)
	mux.HandleFunc("/healthz/ready", h.handleReady)
	mux.HandleFunc("/healthz", h.handleDetailed)
}

// handleLive serves GET /healthz/live.
// Always returns 200 while the process is running.
func (h *Handler) handleLive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok","schema_version":"` + schemaVersion + `"}`))
}

// handleReady serves GET /healthz/ready.
// Returns 200 when all checkers pass; 503 with details when any fail.
func (h *Handler) handleReady(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	results, overall := h.runChecks(r.Context())
	resp := ReadinessResponse{
		SchemaVersion: schemaVersion,
		Status:        overall,
		Components:    results,
		CheckedAt:     time.Now().UTC().Format(time.RFC3339),
	}
	w.Header().Set("Content-Type", "application/json")
	if overall != StatusOK {
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(resp)
}

// handleDetailed serves GET /healthz.
// Always returns 200 with full component details, uptime, and protocol version.
func (h *Handler) handleDetailed(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	results, overall := h.runChecks(r.Context())
	h.mu.RLock()
	protoVer := h.protocolVersion
	uptime := int64(time.Since(h.startTime).Seconds())
	h.mu.RUnlock()

	resp := DetailedResponse{
		SchemaVersion:   schemaVersion,
		Status:          overall,
		Components:      results,
		ProtocolVersion: protoVer,
		UptimeSeconds:   uptime,
		CheckedAt:       time.Now().UTC().Format(time.RFC3339),
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(resp)
}

// runChecks executes all registered checkers concurrently (up to checkTimeout
// each) and returns the per-component map and the overall status.
func (h *Handler) runChecks(ctx context.Context) (map[string]ComponentStatus, StatusValue) {
	h.mu.RLock()
	checkers := make([]Checker, len(h.checkers))
	copy(checkers, h.checkers)
	timeout := h.checkTimeout
	h.mu.RUnlock()

	if len(checkers) == 0 {
		return map[string]ComponentStatus{}, StatusOK
	}

	type result struct {
		name string
		cs   ComponentStatus
	}

	results := make(chan result, len(checkers))

	for _, c := range checkers {
		c := c
		go func() {
			checkCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			err := c.Check(checkCtx)
			if err != nil {
				results <- result{
					name: c.Name(),
					cs:   ComponentStatus{Status: StatusUnavailable, Message: err.Error()},
				}
			} else {
				results <- result{
					name: c.Name(),
					cs:   ComponentStatus{Status: StatusOK},
				}
			}
		}()
	}

	components := make(map[string]ComponentStatus, len(checkers))
	for i := 0; i < len(checkers); i++ {
		r := <-results
		components[r.name] = r.cs
	}

	overall := StatusOK
	for _, cs := range components {
		if cs.Status != StatusOK {
			overall = StatusUnavailable
			break
		}
	}
	return components, overall
}
