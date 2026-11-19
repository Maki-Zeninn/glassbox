// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package simulator

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/dotandev/glassbox/internal/errors"
	"github.com/dotandev/glassbox/internal/logger"
)

// ProcessState represents the lifecycle state of a simulator process.
type ProcessState int32

const (
	// StateNotStarted indicates the process has not been started yet.
	StateNotStarted ProcessState = iota
	// StateStarting indicates the process is starting but not yet ready.
	StateStarting
	// StateReady indicates the process has completed initialization/handshake.
	StateReady
	// StateRunning indicates the process is actively executing a simulation.
	StateRunning
	// StateTerminating indicates the process is being shut down gracefully.
	StateTerminating
	// StateTerminated indicates the process has exited successfully.
	StateTerminated
	// StateFailed indicates the process exited with an error or crashed.
	StateFailed
)

func (s ProcessState) String() string {
	switch s {
	case StateNotStarted:
		return "NotStarted"
	case StateStarting:
		return "Starting"
	case StateReady:
		return "Ready"
	case StateRunning:
		return "Running"
	case StateTerminating:
		return "Terminating"
	case StateTerminated:
		return "Terminated"
	case StateFailed:
		return "Failed"
	default:
		return "Unknown"
	}
}

// ProcessLifecycle manages the complete lifecycle of a simulator process.
// It centralizes process ownership, readiness detection, timeout handling,
// signal forwarding, exit-code translation, and temporary-directory cleanup.
type ProcessLifecycle struct {
	// Immutable fields
	binaryPath string
	tempDir    string
	timeout    time.Duration

	// Mutable fields (protected by mu)
	mu           sync.Mutex
	state        ProcessState
	cmd          *exec.Cmd
	startTime    time.Time
	exitCode     int
	exitError    error
	stderrBuffer *redactingBuffer
	tempFiles    []string

	// Atomic fields for lock-free reads
	stateAtomic atomic.Int32
	closed      atomic.Bool
}

// ProcessConfig holds configuration for process lifecycle management.
type ProcessConfig struct {
	BinaryPath    string
	TempDir       string
	Timeout       time.Duration
	MaxStderrSize int
}

// NewProcessLifecycle creates a new process lifecycle manager.
func NewProcessLifecycle(config ProcessConfig) *ProcessLifecycle {
	if config.MaxStderrSize == 0 {
		config.MaxStderrSize = 1 * 1024 * 1024 // 1MB default
	}
	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second // 30s default
	}

	return &ProcessLifecycle{
		binaryPath:   config.BinaryPath,
		tempDir:      config.TempDir,
		timeout:      config.Timeout,
		state:        StateNotStarted,
		stderrBuffer: newRedactingBuffer(config.MaxStderrSize),
		tempFiles:    make([]string, 0),
	}
}

// Start begins the process execution with the given command setup.
// It transitions the state from NotStarted to Starting.
func (pl *ProcessLifecycle) Start(ctx context.Context, setupCmd func(*exec.Cmd)) error {
	pl.mu.Lock()
	defer pl.mu.Unlock()

	if pl.state != StateNotStarted {
		return fmt.Errorf("invalid state transition from %s to Starting", pl.state)
	}

	cmd := exec.CommandContext(ctx, pl.binaryPath)
	prepareCommand(cmd)
	if setupCmd != nil {
		setupCmd(cmd)
	}

	// Set up stderr capture
	cmd.Stderr = pl.stderrBuffer

	pl.cmd = cmd
	pl.startTime = time.Now()
	pl.setState(StateStarting)

	if err := cmd.Start(); err != nil {
		pl.setState(StateFailed)
		pl.exitError = errors.WrapSimCrash(err, "failed to start simulator")
		return pl.exitError
	}

	// Track temp directory for cleanup
	if pl.tempDir != "" {
		pl.tempFiles = append(pl.tempFiles, pl.tempDir)
	}

	logger.Logger.Debug("Simulator process started",
		"pid", cmd.Process.Pid,
		"binary", pl.binaryPath,
	)

	return nil
}

// WaitForReady waits for the process to become ready (e.g., complete handshake).
// It transitions the state from Starting to Ready on success, or to Failed on timeout/error.
func (pl *ProcessLifecycle) WaitForReady(ctx context.Context, readyCheck func() (bool, error)) error {
	pl.mu.Lock()
	if pl.state != StateStarting {
		pl.mu.Unlock()
		return fmt.Errorf("invalid state transition from %s to Ready", pl.state)
	}
	pl.mu.Unlock()

	readyCtx, cancel := context.WithTimeout(ctx, pl.timeout)
	defer cancel()

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-readyCtx.Done():
			pl.mu.Lock()
			pl.setState(StateFailed)
			pl.exitError = fmt.Errorf("process readiness check timed out after %v", pl.timeout)
			pl.mu.Unlock()
			pl.Cleanup()
			return pl.exitError

		case <-ticker.C:
			ready, err := readyCheck()
			if err != nil {
				pl.mu.Lock()
				pl.setState(StateFailed)
				pl.exitError = fmt.Errorf("readiness check failed: %w", err)
				pl.mu.Unlock()
				pl.Cleanup()
				return pl.exitError
			}
			if ready {
				pl.mu.Lock()
				pl.setState(StateReady)
				pl.mu.Unlock()
				logger.Logger.Debug("Simulator process ready")
				return nil
			}

		case <-ctx.Done():
			pl.mu.Lock()
			pl.setState(StateFailed)
			pl.exitError = ctx.Err()
			pl.mu.Unlock()
			pl.Cleanup()
			return pl.exitError
		}
	}
}

// MarkRunning transitions the state from Ready to Running.
func (pl *ProcessLifecycle) MarkRunning() error {
	pl.mu.Lock()
	defer pl.mu.Unlock()

	if pl.state != StateReady {
		return fmt.Errorf("invalid state transition from %s to Running", pl.state)
	}

	pl.setState(StateRunning)
	return nil
}

// Wait waits for the process to exit and captures the exit code.
// It transitions the state to Terminated on success or Failed on error.
func (pl *ProcessLifecycle) Wait() error {
	pl.mu.Lock()
	if pl.state != StateRunning && pl.state != StateTerminating {
		pl.mu.Unlock()
		return fmt.Errorf("invalid state for Wait: %s", pl.state)
	}
	cmd := pl.cmd
	pl.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return fmt.Errorf("process not started")
	}

	err := cmd.Wait()

	pl.mu.Lock()
	defer pl.mu.Unlock()

	if err != nil {
		pl.setState(StateFailed)
		pl.exitError = err
		if exitErr, ok := err.(*exec.ExitError); ok {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				pl.exitCode = status.ExitStatus()
			}
		}
	} else {
		pl.setState(StateTerminated)
		pl.exitCode = 0
	}

	logger.Logger.Debug("Simulator process exited",
		"state", pl.state,
		"exit_code", pl.exitCode,
		"error", pl.exitError,
	)

	return pl.exitError
}

// Terminate gracefully terminates the process with a grace period before force-killing.
// It transitions the state to Terminating, then to Terminated or Failed.
func (pl *ProcessLifecycle) Terminate(graceTimeout time.Duration) error {
	pl.mu.Lock()
	if pl.state != StateRunning && pl.state != StateReady && pl.state != StateStarting {
		pl.mu.Unlock()
		return fmt.Errorf("invalid state for Terminate: %s", pl.state)
	}
	cmd := pl.cmd
	pl.setState(StateTerminating)
	pl.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return nil
	}

	// Try graceful termination first
	if err := terminateCommand(cmd, graceTimeout); err != nil {
		logger.Logger.Warn("Graceful termination failed, force-killing", "error", err)
		// Force kill if graceful termination fails
		if killErr := cmd.Process.Kill(); killErr != nil {
			logger.Logger.Error("Force kill failed", "error", killErr)
		}
	}

	// Wait for process to exit
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-done:
		pl.mu.Lock()
		pl.setState(StateTerminated)
		pl.mu.Unlock()
	case <-time.After(5 * time.Second):
		logger.Logger.Warn("Process did not exit within 5 seconds after termination")
		pl.mu.Lock()
		pl.setState(StateFailed)
		pl.mu.Unlock()
	}

	return nil
}

// Cleanup releases all resources associated with the process.
// This includes killing the process, closing pipes, and removing temporary files.
func (pl *ProcessLifecycle) Cleanup() *ProcessCleanupResult {
	result := &ProcessCleanupResult{}

	pl.mu.Lock()
	defer pl.mu.Unlock()

	if pl.closed.Load() {
		return result
	}
	pl.closed.Store(true)

	// Kill process if still running
	if pl.cmd != nil && pl.cmd.Process != nil {
		if pl.state == StateRunning || pl.state == StateReady || pl.state == StateStarting {
			if err := pl.cmd.Process.Kill(); err == nil {
				result.ProcessKilled = true
			}
			// Wait for process to exit to prevent zombies
			done := make(chan error, 1)
			go func() {
				done <- pl.cmd.Wait()
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				result.Errors = append(result.Errors, "process did not exit within 5 seconds after kill")
			}
		}
	}

	// Close pipes
	if pl.cmd != nil {
		if pl.cmd.Stdin != nil {
			if closer, ok := pl.cmd.Stdin.(interface{ Close() error }); ok {
				closer.Close()
				result.PipesClosed++
			}
		}
		if pl.cmd.Stdout != nil {
			if closer, ok := pl.cmd.Stdout.(interface{ Close() error }); ok {
				closer.Close()
				result.PipesClosed++
			}
		}
		if pl.cmd.Stderr != nil {
			if closer, ok := pl.cmd.Stderr.(interface{ Close() error }); ok {
				closer.Close()
				result.PipesClosed++
			}
		}
	}

	// Remove temporary files
	for _, tempFile := range pl.tempFiles {
		if rmErr := os.RemoveAll(tempFile); rmErr == nil {
			result.TempFilesRemoved = append(result.TempFilesRemoved, tempFile)
		} else {
			result.Errors = append(result.Errors, fmt.Sprintf("failed to remove temp file %s: %v", tempFile, rmErr))
		}
	}

	return result
}

// GetState returns the current process state (lock-free).
func (pl *ProcessLifecycle) GetState() ProcessState {
	return ProcessState(pl.stateAtomic.Load())
}

// setState updates the state (must be called with mu held).
func (pl *ProcessLifecycle) setState(s ProcessState) {
	pl.state = s
	pl.stateAtomic.Store(int32(s))
}

// GetExitCode returns the process exit code.
func (pl *ProcessLifecycle) GetExitCode() int {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	return pl.exitCode
}

// GetExitError returns the process exit error.
func (pl *ProcessLifecycle) GetExitError() error {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	return pl.exitError
}

// GetStderr returns the captured stderr output (redacted).
func (pl *ProcessLifecycle) GetStderr() string {
	return pl.stderrBuffer.String()
}

// GetPID returns the process ID if the process is running.
func (pl *ProcessLifecycle) GetPID() int {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	if pl.cmd != nil && pl.cmd.Process != nil {
		return pl.cmd.Process.Pid
	}
	return 0
}

// GetUptime returns the duration since the process started.
func (pl *ProcessLifecycle) GetUptime() time.Duration {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	if pl.startTime.IsZero() {
		return 0
	}
	return time.Since(pl.startTime)
}

// TranslateExitCode converts the process exit code to a stable error with actionable hints.
func (pl *ProcessLifecycle) TranslateExitCode() error {
	exitCode := pl.GetExitCode()
	stderr := pl.GetStderr()

	// Platform-specific exit code mapping
	switch runtime.GOOS {
	case "linux", "darwin":
		return translateUnixExitCode(exitCode, stderr)
	case "windows":
		return translateWindowsExitCode(exitCode, stderr)
	default:
		return fmt.Errorf("simulator exited with code %d: %s", exitCode, stderr)
	}
}

func translateUnixExitCode(exitCode int, stderr string) error {
	switch exitCode {
	case 0:
		return nil
	case 1:
		return errors.NewSimErrorMsg(errors.CodeSimulatorError, "simulator execution failed: "+truncateStderr(stderr))
	case 2:
		return errors.NewSimErrorMsg(errors.CodeValidationFailed, "invalid input or configuration: "+truncateStderr(stderr))
	case 127:
		return errors.NewSimErrorMsg(errors.CodeSimulatorNotFound, "simulator binary not found or not executable")
	case 130: // SIGINT
		return context.Canceled
	case 137: // SIGKILL
		return errors.NewSimErrorMsg(errors.CodeSimulatorCrashed, "simulator was forcefully terminated (SIGKILL)")
	case 143: // SIGTERM
		return context.Canceled
	default:
		if exitCode > 128 {
			return errors.NewSimErrorMsg(errors.CodeSimulatorCrashed, fmt.Sprintf("simulator terminated by signal %d: %s", exitCode-128, truncateStderr(stderr)))
		}
		return errors.NewSimErrorMsg(errors.CodeSimulatorError, fmt.Sprintf("simulator exited with code %d: %s", exitCode, truncateStderr(stderr)))
	}
}

func translateWindowsExitCode(exitCode int, stderr string) error {
	switch exitCode {
	case 0:
		return nil
	case 1:
		return errors.NewSimErrorMsg(errors.CodeSimulatorError, "simulator execution failed: "+truncateStderr(stderr))
	case 2:
		return errors.NewSimErrorMsg(errors.CodeValidationFailed, "invalid input or configuration: "+truncateStderr(stderr))
	default:
		return errors.NewSimErrorMsg(errors.CodeSimulatorError, fmt.Sprintf("simulator exited with code %d: %s", exitCode, truncateStderr(stderr)))
	}
}

func truncateStderr(stderr string) string {
	const maxLen = 500
	if len(stderr) <= maxLen {
		return stderr
	}
	return stderr[:maxLen] + "..."
}

// redactingBuffer captures stderr while redacting sensitive information.
type redactingBuffer struct {
	data []byte
	limit int
}

func newRedactingBuffer(limit int) *redactingBuffer {
	return &redactingBuffer{
		data: make([]byte, 0, limit),
		limit: limit,
	}
}

func (rb *redactingBuffer) Write(p []byte) (n int, err error) {
	// Redact sensitive patterns
	redacted := rb.redact(p)

	// Enforce size limit
	if len(rb.data)+len(redacted) > rb.limit {
		remaining := rb.limit - len(rb.data)
		if remaining > 0 {
			rb.data = append(rb.data, redacted[:remaining]...)
		}
		return len(p), nil // Return original length to avoid signaling truncation
	}

	rb.data = append(rb.data, redacted...)
	return len(p), nil
}

func (rb *redactingBuffer) String() string {
	return string(rb.data)
}

func (rb *redactingBuffer) redact(data []byte) []byte {
	// Redact common sensitive patterns
	// This is a basic implementation; can be extended with more patterns
	patterns := [][]byte{
		[]byte("secret"),
		[]byte("password"),
		[]byte("token"),
		[]byte("api_key"),
		[]byte("private_key"),
	}

	result := make([]byte, len(data))
	copy(result, data)

	for _, pattern := range patterns {
		result = bytesReplaceAll(result, pattern, []byte("[REDACTED]"))
	}

	return result
}

func bytesReplaceAll(s, old, new []byte) []byte {
	if len(old) == 0 {
		return s
	}
	var result []byte
	for {
		i := bytesIndex(s, old)
		if i == -1 {
			break
		}
		result = append(result, s[:i]...)
		result = append(result, new...)
		s = s[i+len(old):]
	}
	result = append(result, s...)
	return result
}

func bytesIndex(s, sep []byte) int {
	for i := 0; i <= len(s)-len(sep); i++ {
		if bytesEqual(s[i:i+len(sep)], sep) {
			return i
		}
	}
	return -1
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
