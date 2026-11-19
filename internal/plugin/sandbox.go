// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// defaultPluginTimeout is the wall-clock budget for a single plugin call.
// It is exported via ResourceLimits so callers can tighten it per invocation.
const defaultPluginTimeout = 10 * time.Second

// quarantineThreshold is the number of consecutive failures after which the
// plugin is quarantined and no further calls are dispatched.
const quarantineThreshold = 3

// healthCheckInterval is how often a background health check is emitted while
// the plugin is healthy.
const healthCheckInterval = 30 * time.Second

// PluginHealth represents the liveness state of a sandboxed plugin.
type PluginHealth int32

const (
	// HealthOK means the plugin is accepting calls normally.
	HealthOK PluginHealth = iota
	// HealthDegraded means the plugin has produced recent failures but has not
	// yet reached the quarantine threshold.
	HealthDegraded
	// HealthQuarantined means the plugin has exceeded the failure threshold and
	// is no longer called. The host remains fully operational.
	HealthQuarantined
)

func (h PluginHealth) String() string {
	switch h {
	case HealthOK:
		return "ok"
	case HealthDegraded:
		return "degraded"
	case HealthQuarantined:
		return "quarantined"
	default:
		return "unknown"
	}
}

// QuarantineReason captures the last error that triggered quarantine.
type QuarantineReason struct {
	Err       error
	OccuredAt time.Time
}

// SandboxedPlugin wraps a plugin manifest and executes the plugin binary in a
// child process, communicating over stdin/stdout JSON. This provides runtime
// isolation: a crashing or misbehaving plugin cannot corrupt the host process.
//
// Health tracking: consecutive failures increment a counter. Once the counter
// reaches quarantineThreshold the plugin is quarantined; all subsequent calls
// return ErrPluginQuarantined immediately without spawning a process.
type SandboxedPlugin struct {
	mu       sync.Mutex
	manifest *Manifest
	// binaryPath is the resolved absolute path to the plugin binary.
	binaryPath string

	// health tracks the current liveness state (atomic for lock-free reads).
	health atomic.Int32

	// consecutiveFailures counts unbroken failure runs.
	consecutiveFailures int

	// quarantineReason holds details of the failure that triggered quarantine.
	quarantineReason *QuarantineReason

	// lastHealthCheck records when the last successful health check was sent.
	lastHealthCheck time.Time

	// limits are the per-call resource constraints applied to every invocation.
	limits ResourceLimits
}

// ErrPluginQuarantined is returned for every call after a plugin has been
// quarantined. It is a stable, isolated error; the host remains usable.
var ErrPluginQuarantined = fmt.Errorf("plugin quarantined due to repeated failures")

// NewSandboxedPlugin creates a SandboxedPlugin from a manifest.
// The manifest's Entrypoint is resolved relative to manifestDir.
func NewSandboxedPlugin(manifest *Manifest, manifestDir string) (*SandboxedPlugin, error) {
	binaryPath := manifest.Entrypoint
	if !isAbsPath(binaryPath) {
		binaryPath = joinPaths(manifestDir, binaryPath)
	}

	if _, err := os.Stat(binaryPath); err != nil {
		return nil, fmt.Errorf("plugin binary not found at %s: %w", binaryPath, err)
	}

	if manifest.Checksum != "" {
		if err := verifyChecksum(binaryPath, manifest.Checksum); err != nil {
			return nil, fmt.Errorf("plugin binary checksum mismatch: %w", err)
		}
	}

	sp := &SandboxedPlugin{
		manifest:   manifest,
		binaryPath: binaryPath,
		limits:     DefaultResourceLimits(),
	}
	sp.health.Store(int32(HealthOK))
	return sp, nil
}

// Name returns the plugin name from its manifest.
func (s *SandboxedPlugin) Name() string { return s.manifest.Name }

// Version returns the plugin version from its manifest.
func (s *SandboxedPlugin) Version() string { return s.manifest.Version }

// CanDecode reports whether this plugin can handle the given event type.
func (s *SandboxedPlugin) CanDecode(eventType string) bool {
	for _, et := range s.manifest.EventTypes {
		if et == eventType {
			return true
		}
	}
	return false
}

// Health returns the current health state of the plugin (lock-free).
func (s *SandboxedPlugin) Health() PluginHealth {
	return PluginHealth(s.health.Load())
}

// IsQuarantined reports whether the plugin is quarantined (lock-free).
func (s *SandboxedPlugin) IsQuarantined() bool {
	return s.Health() == HealthQuarantined
}

// QuarantineInfo returns the quarantine reason, or nil if not quarantined.
func (s *SandboxedPlugin) QuarantineInfo() *QuarantineReason {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.quarantineReason
}

// SetLimits replaces the per-call resource constraints.
func (s *SandboxedPlugin) SetLimits(l ResourceLimits) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.limits = l
}

// Decode invokes the plugin binary in a sandboxed child process and returns
// the decoded result. A crashed, timed-out, or repeatedly-failing plugin is
// isolated: the error is returned without affecting the host process.
func (s *SandboxedPlugin) Decode(data []byte) (json.RawMessage, error) {
	if s.IsQuarantined() {
		return nil, fmt.Errorf("plugin %s: %w", s.manifest.Name, ErrPluginQuarantined)
	}

	s.mu.Lock()
	limits := s.limits
	s.mu.Unlock()

	req := PluginRequest{
		Version:   EnvelopeVersion,
		ID:        newRequestID(),
		Method:    MethodDecode,
		EventType: "",
		Data:      json.RawMessage(data),
		Limits:    limits,
	}

	result, err := s.callWithTracking(req)
	return result, err
}

// Metadata returns plugin capabilities derived from the manifest.
func (s *SandboxedPlugin) Metadata() Metadata {
	return Metadata{
		Name:        s.manifest.Name,
		Version:     s.manifest.Version,
		APIVersion:  s.manifest.APIVersion,
		EventTypes:  s.manifest.EventTypes,
		Description: s.manifest.Description,
	}
}

// Init sends an initialisation request to the plugin process.
// Errors are non-fatal: a plugin that fails to initialise is logged but
// does not prevent other plugins from loading.
func (s *SandboxedPlugin) Init() error {
	if s.IsQuarantined() {
		return fmt.Errorf("plugin %s: %w", s.manifest.Name, ErrPluginQuarantined)
	}
	req := PluginRequest{
		Version: EnvelopeVersion,
		ID:      newRequestID(),
		Method:  MethodInit,
		Limits:  s.limits,
	}
	_, err := s.callWithTracking(req)
	return err
}

// Cleanup sends a cleanup request to the plugin process.
func (s *SandboxedPlugin) Cleanup() error {
	// Always attempt cleanup even if quarantined — best effort.
	req := PluginRequest{
		Version: EnvelopeVersion,
		ID:      newRequestID(),
		Method:  MethodCleanup,
		Limits:  s.limits,
	}
	// Use a short, fixed timeout for cleanup; ignore quarantine state.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := s.callRaw(ctx, req)
	return err
}

// HealthCheck sends a health_check request to the plugin and returns nil if
// the plugin responds with StatusOK. It updates the lastHealthCheck timestamp
// on success, but does NOT count a health-check failure toward quarantine —
// health checks are advisory.
func (s *SandboxedPlugin) HealthCheck(ctx context.Context) error {
	if s.IsQuarantined() {
		return fmt.Errorf("plugin %s is quarantined", s.manifest.Name)
	}

	req := PluginRequest{
		Version: EnvelopeVersion,
		ID:      newRequestID(),
		Method:  MethodHealthCheck,
		Limits:  ResourceLimits{TimeoutMs: 2000, MaxOutputBytes: 64 * 1024},
	}

	_, err := s.callRaw(ctx, req)
	if err == nil {
		s.mu.Lock()
		s.lastHealthCheck = time.Now()
		s.mu.Unlock()
	}
	return err
}

// LastHealthCheck returns the timestamp of the last successful health check.
func (s *SandboxedPlugin) LastHealthCheck() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastHealthCheck
}

// ResetQuarantine lifts the quarantine and resets the failure counter.
// This should only be called by operators / tests after the underlying issue
// has been resolved. It does NOT verify the plugin binary.
func (s *SandboxedPlugin) ResetQuarantine() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.consecutiveFailures = 0
	s.quarantineReason = nil
	s.health.Store(int32(HealthOK))
}

// ─── internal ────────────────────────────────────────────────────────────────

// callWithTracking dispatches a request and updates the health/quarantine state
// based on the outcome.
func (s *SandboxedPlugin) callWithTracking(req PluginRequest) (json.RawMessage, error) {
	timeout := defaultPluginTimeout
	if req.Limits.TimeoutMs > 0 {
		timeout = time.Duration(req.Limits.TimeoutMs) * time.Millisecond
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	result, err := s.callRaw(ctx, req)
	if err != nil {
		s.recordFailure(err)
		return nil, err
	}

	// Success — reset failure counter.
	s.mu.Lock()
	s.consecutiveFailures = 0
	if s.health.Load() == int32(HealthDegraded) {
		s.health.Store(int32(HealthOK))
	}
	s.mu.Unlock()

	return result, nil
}

// recordFailure increments the consecutive failure counter and quarantines the
// plugin if the threshold is reached.
func (s *SandboxedPlugin) recordFailure(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.consecutiveFailures++

	if s.consecutiveFailures >= quarantineThreshold {
		s.health.Store(int32(HealthQuarantined))
		s.quarantineReason = &QuarantineReason{
			Err:       err,
			OccuredAt: time.Now(),
		}
	} else {
		s.health.Store(int32(HealthDegraded))
	}
}

// callRaw spawns the plugin binary, writes req as JSON to its stdin, reads the
// JSON response from stdout, validates the envelope, and returns the result.
// The child process is killed if it does not respond within the deadline in ctx.
// Any panic inside this function is recovered and converted to an error so that
// a misbehaving plugin binary can never crash the host.
func (s *SandboxedPlugin) callRaw(ctx context.Context, req PluginRequest) (result json.RawMessage, retErr error) {
	// Recover from any unexpected panic so the host is never brought down.
	defer func() {
		if r := recover(); r != nil {
			retErr = fmt.Errorf("plugin %s: unexpected panic in host shim: %v", s.manifest.Name, r)
		}
	}()

	// Enforce max input size before even spawning a process.
	s.mu.Lock()
	limits := s.limits
	s.mu.Unlock()

	reqBytes, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("plugin %s: failed to marshal request: %w", s.manifest.Name, err)
	}
	if limits.MaxInputBytes > 0 && len(reqBytes) > limits.MaxInputBytes {
		return nil, fmt.Errorf("plugin %s: request payload (%d bytes) exceeds limit (%d bytes)",
			s.manifest.Name, len(reqBytes), limits.MaxInputBytes)
	}

	cmd := exec.CommandContext(ctx, s.binaryPath) //nolint:gosec
	cmd.Env = buildSandboxEnv(s.manifest)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("plugin %s: failed to create stdin pipe: %w", s.manifest.Name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("plugin %s: failed to create stdout pipe: %w", s.manifest.Name, err)
	}
	// Discard stderr to prevent plugin output from polluting the host terminal.
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("plugin %s: failed to start process: %w", s.manifest.Name, err)
	}

	// Write request envelope.
	enc := json.NewEncoder(stdin)
	if encErr := enc.Encode(req); encErr != nil {
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("plugin %s: failed to write request: %w", s.manifest.Name, encErr)
	}
	_ = stdin.Close()

	// Read response with an optional output size cap.
	var reader io.Reader = stdout
	maxOut := limits.MaxOutputBytes
	if maxOut <= 0 {
		maxOut = 4 * 1024 * 1024
	}
	reader = io.LimitReader(stdout, int64(maxOut)+1)

	rawBytes, readErr := io.ReadAll(reader)
	if readErr != nil {
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("plugin %s: failed to read response: %w", s.manifest.Name, readErr)
	}
	if len(rawBytes) > maxOut {
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("plugin %s: response exceeds max output size (%d bytes)", s.manifest.Name, maxOut)
	}

	// Wait for the child to exit — this is mandatory to reap the zombie.
	waitErr := cmd.Wait()

	// Detect context timeout/cancellation (the process was killed by exec.CommandContext).
	if ctx.Err() != nil {
		return nil, fmt.Errorf("plugin %s: call timed out or was cancelled: %w", s.manifest.Name, ctx.Err())
	}

	// A non-zero exit is a plugin-level error; decode the response anyway so we
	// can surface the plugin's own error message when available.
	if len(rawBytes) == 0 {
		if waitErr != nil {
			return nil, fmt.Errorf("plugin %s: process exited with no output: %w", s.manifest.Name, waitErr)
		}
		return nil, fmt.Errorf("plugin %s: process produced no output", s.manifest.Name)
	}

	var resp PluginResponse
	if jsonErr := json.Unmarshal(rawBytes, &resp); jsonErr != nil {
		return nil, &PluginProtocolError{
			Reason: "malformed response JSON",
			Detail: fmt.Sprintf("plugin=%s err=%v", s.manifest.Name, jsonErr),
		}
	}

	// Validate the envelope — version, ID, status field.
	if valErr := resp.Validate(req.ID); valErr != nil {
		return nil, valErr
	}

	if !resp.IsOK() {
		return nil, fmt.Errorf("plugin %s: %s", s.manifest.Name, resp.Error)
	}

	return resp.Result, nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// buildSandboxEnv constructs a minimal environment for the plugin process.
// Only variables explicitly required by the plugin's declared permissions are
// forwarded; all others are stripped to limit information leakage.
func buildSandboxEnv(m *Manifest) []string {
	env := []string{
		"GLASSBOX_PLUGIN_NAME=" + m.Name,
		"GLASSBOX_PLUGIN_VERSION=" + m.Version,
		"GLASSBOX_API_VERSION=" + m.APIVersion,
	}
	// Forward PATH so the binary can locate shared libraries.
	if p := os.Getenv("PATH"); p != "" {
		env = append(env, "PATH="+p)
	}
	return env
}

// verifyChecksum computes the SHA-256 digest of the file at path and compares
// it against the expected hex string.
func verifyChecksum(path, expected string) error {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return fmt.Errorf("cannot open binary for checksum: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("failed to hash binary: %w", err)
	}

	got := hex.EncodeToString(h.Sum(nil))
	if got != expected {
		return fmt.Errorf("expected %s, got %s", expected, got)
	}
	return nil
}

// isAbsPath reports whether p is an absolute filesystem path.
func isAbsPath(p string) bool {
	if len(p) == 0 {
		return false
	}
	if p[0] == '/' {
		return true
	}
	if len(p) >= 3 && p[1] == ':' {
		return true
	}
	if len(p) >= 2 && p[0] == '\\' && p[1] == '\\' {
		return true
	}
	return false
}

// joinPaths joins two path segments using the OS separator.
func joinPaths(base, rel string) string {
	if base == "" {
		return rel
	}
	sep := string(os.PathSeparator)
	if base[len(base)-1] == os.PathSeparator {
		return base + rel
	}
	return base + sep + rel
}

// newRequestID generates a compact monotonic request identifier using the
// current nanosecond timestamp. It does not need to be globally unique —
// only unique within a single plugin call sequence.
func newRequestID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
