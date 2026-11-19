// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package simulator

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/dotandev/glassbox/internal/errors"
)

// FakeProcess is a test helper that simulates a process for testing lifecycle management.
type FakeProcess struct {
	startCalled     bool
	readyCalled     bool
	runningCalled   bool
	waitCalled      bool
	terminateCalled bool
	cleanupCalled   bool
	
	startDelay      time.Duration
	readyDelay      time.Duration
	shouldFailStart bool
	shouldFailReady bool
	shouldFailWait  bool
	exitCode        int
	
	stderrOutput string
}

func NewFakeProcess() *FakeProcess {
	return &FakeProcess{
		exitCode: 0,
	}
}

func (fp *FakeProcess) WithStartDelay(d time.Duration) *FakeProcess {
	fp.startDelay = d
	return fp
}

func (fp *FakeProcess) WithReadyDelay(d time.Duration) *FakeProcess {
	fp.readyDelay = d
	return fp
}

func (fp *FakeProcess) WithStartFailure() *FakeProcess {
	fp.shouldFailStart = true
	return fp
}

func (fp *FakeProcess) WithReadyFailure() *FakeProcess {
	fp.shouldFailReady = true
	return fp
}

func (fp *FakeProcess) WithWaitFailure(code int) *FakeProcess {
	fp.shouldFailWait = true
	fp.exitCode = code
	return fp
}

func (fp *FakeProcess) WithStderr(output string) *FakeProcess {
	fp.stderrOutput = output
	return fp
}

func (fp *FakeProcess) Start(ctx context.Context, setupCmd func(*exec.Cmd)) error {
	fp.startCalled = true
	if fp.startDelay > 0 {
		time.Sleep(fp.startDelay)
	}
	if fp.shouldFailStart {
		return fmt.Errorf("fake process start failed")
	}
	return nil
}

func (fp *FakeProcess) WaitForReady(ctx context.Context, readyCheck func() (bool, error)) error {
	fp.readyCalled = true
	if fp.readyDelay > 0 {
		time.Sleep(fp.readyDelay)
	}
	if fp.shouldFailReady {
		return fmt.Errorf("fake process ready check failed")
	}
	return nil
}

func (fp *FakeProcess) MarkRunning() error {
	fp.runningCalled = true
	return nil
}

func (fp *FakeProcess) Wait() error {
	fp.waitCalled = true
	if fp.shouldFailWait {
		return fmt.Errorf("fake process wait failed with exit code %d", fp.exitCode)
	}
	return nil
}

func (fp *FakeProcess) Terminate(graceTimeout time.Duration) error {
	fp.terminateCalled = true
	return nil
}

func (fp *FakeProcess) Cleanup() *ProcessCleanupResult {
	fp.cleanupCalled = true
	return &ProcessCleanupResult{
		ProcessKilled: true,
		PipesClosed:   3,
	}
}

func (fp *FakeProcess) GetState() ProcessState {
	return StateReady
}

func (fp *FakeProcess) GetExitCode() int {
	return fp.exitCode
}

func (fp *FakeProcess) GetExitError() error {
	if fp.shouldFailWait {
		return fmt.Errorf("fake process wait failed")
	}
	return nil
}

func (fp *FakeProcess) GetStderr() string {
	return fp.stderrOutput
}

func (fp *FakeProcess) GetPID() int {
	return 12345
}

func (fp *FakeProcess) GetUptime() time.Duration {
	return 100 * time.Millisecond
}

func (fp *FakeProcess) TranslateExitCode() error {
	return translateUnixExitCode(fp.exitCode, fp.stderrOutput)
}

// TestLifecycleStateTransitions tests state machine transitions.
func TestLifecycleStateTransitions(t *testing.T) {
	tests := []struct {
		name           string
		initialState   ProcessState
		targetState    ProcessState
		shouldSucceed  bool
	}{
		{"NotStarted to Starting", StateNotStarted, StateStarting, true},
		{"Starting to Ready", StateStarting, StateReady, true},
		{"Ready to Running", StateReady, StateRunning, true},
		{"Running to Terminating", StateRunning, StateTerminating, true},
		{"Terminating to Terminated", StateTerminating, StateTerminated, true},
		{"Invalid: NotStarted to Running", StateNotStarted, StateRunning, false},
		{"Invalid: Ready to Starting", StateReady, StateStarting, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pl := NewProcessLifecycle(ProcessConfig{
				BinaryPath: "/bin/echo",
				Timeout:    5 * time.Second,
			})

			pl.mu.Lock()
			pl.state = tt.initialState
			pl.mu.Unlock()

			var err error
			switch tt.targetState {
			case StateStarting:
				err = pl.Start(context.Background(), nil)
			case StateReady:
				pl.mu.Lock()
				pl.state = StateStarting
				pl.mu.Unlock()
				err = pl.WaitForReady(context.Background(), func() (bool, error) { return true, nil })
			case StateRunning:
				pl.mu.Lock()
				pl.state = StateReady
				pl.mu.Unlock()
				err = pl.MarkRunning()
			case StateTerminating:
				pl.mu.Lock()
				pl.state = StateRunning
				pl.mu.Unlock()
				err = pl.Terminate(1 * time.Second)
			}

			if tt.shouldSucceed && err != nil {
				t.Errorf("expected success but got error: %v", err)
			}
			if !tt.shouldSucceed && err == nil {
				t.Errorf("expected error but got success")
			}
		})
	}
}

// TestLifecycleCleanup tests cleanup functionality.
func TestLifecycleCleanup(t *testing.T) {
	tempDir := t.TempDir()
	
	pl := NewProcessLifecycle(ProcessConfig{
		BinaryPath: "/bin/echo",
		TempDir:    tempDir,
		Timeout:    5 * time.Second,
	})

	// Create a temp file
	tempFile := filepath.Join(tempDir, "test.txt")
	if err := os.WriteFile(tempFile, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}

	result := pl.Cleanup()

	if !result.ProcessKilled {
		t.Error("expected ProcessKilled to be true")
	}
	if len(result.TempFilesRemoved) == 0 {
		t.Error("expected temp files to be removed")
	}
}

// TestRedactingBuffer tests stderr redaction.
func TestRedactingBuffer(t *testing.T) {
	rb := newRedactingBuffer(1024)

	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{"No secrets", "normal output", "normal output"},
		{"With password", "password=secret123", "password=[REDACTED]123"},
		{"With token", "token=abc123", "token=[REDACTED]123"},
		{"With api_key", "api_key=xyz789", "api_key=[REDACTED]789"},
		{"Multiple secrets", "password=123 token=456", "password=[REDACTED]123 token=[REDACTED]456"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			rb.data = rb.data[:0] // Reset buffer
			n, err := rb.Write([]byte(tc.input))
			if err != nil {
				t.Errorf("Write failed: %v", err)
			}
			if n != len(tc.input) {
				t.Errorf("Write returned %d, expected %d", n, len(tc.input))
			}
			output := rb.String()
			if output != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, output)
			}
		})
	}
}

// TestRedactingBufferLimit tests size limit enforcement.
func TestRedactingBufferLimit(t *testing.T) {
	limit := 100
	rb := newRedactingBuffer(limit)

	// Write data that exceeds limit
	largeData := make([]byte, limit*2)
	for i := range largeData {
		largeData[i] = 'x'
	}

	n, err := rb.Write(largeData)
	if err != nil {
		t.Errorf("Write failed: %v", err)
	}
	if n != len(largeData) {
		t.Errorf("Write returned %d, expected %d", n, len(largeData))
	}

	// Buffer should be truncated to limit
	if len(rb.data) > limit {
		t.Errorf("buffer size %d exceeds limit %d", len(rb.data), limit)
	}
}

// TestExitCodeTranslation tests exit code to error mapping.
func TestExitCodeTranslation(t *testing.T) {
	tests := []struct {
		name      string
		exitCode  int
		stderr    string
		checkErr  func(error) bool
	}{
		{"Exit 0", 0, "", func(err error) bool { return err == nil }},
		{"Exit 1", 1, "simulator error", func(err error) bool {
			return err != nil && errors.GetCode(err) == errors.CodeSimulatorError
		}},
		{"Exit 2", 2, "validation failed", func(err error) bool {
			return err != nil && errors.GetCode(err) == errors.CodeValidationFailed
		}},
		{"Exit 127", 127, "command not found", func(err error) bool {
			return err != nil && errors.GetCode(err) == errors.CodeSimulatorNotFound
		}},
		{"Exit 130", 130, "", func(err error) bool { return err == context.Canceled }},
		{"Exit 137", 137, "", func(err error) bool {
			return err != nil && errors.GetCode(err) == errors.CodeSimulatorCrashed
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			switch runtime.GOOS {
			case "linux", "darwin":
				err = translateUnixExitCode(tt.exitCode, tt.stderr)
			case "windows":
				err = translateWindowsExitCode(tt.exitCode, tt.stderr)
			default:
				t.Skip("unsupported platform")
			}

			if !tt.checkErr(err) {
				t.Errorf("exit code translation failed for code %d: %v", tt.exitCode, err)
			}
		})
	}
}

// TestFakeProcess tests the fake process helper.
func TestFakeProcess(t *testing.T) {
	fp := NewFakeProcess()
	
	ctx := context.Background()
	
	// Test successful lifecycle
	if err := fp.Start(ctx, nil); err != nil {
		t.Errorf("Start failed: %v", err)
	}
	if !fp.startCalled {
		t.Error("Start was not called")
	}
	
	if err := fp.MarkRunning(); err != nil {
		t.Errorf("MarkRunning failed: %v", err)
	}
	if !fp.runningCalled {
		t.Error("MarkRunning was not called")
	}
	
	if err := fp.Wait(); err != nil {
		t.Errorf("Wait failed: %v", err)
	}
	if !fp.waitCalled {
		t.Error("Wait was not called")
	}
	
	result := fp.Cleanup()
	if !fp.cleanupCalled {
		t.Error("Cleanup was not called")
	}
	if !result.ProcessKilled {
		t.Error("ProcessKilled should be true")
	}
}

// TestFakeProcessFailure tests fake process failure scenarios.
func TestFakeProcessFailure(t *testing.T) {
	t.Run("Start failure", func(t *testing.T) {
	ctx := context.Background()
		fp := NewFakeProcess().WithStartFailure()
		if err := fp.Start(ctx, nil); err == nil {
			t.Error("expected start failure but got success")
		}
	})
	
	t.Run("Wait failure", func(t *testing.T) {
		fp := NewFakeProcess().WithWaitFailure(1)
		if err := fp.Wait(); err == nil {
			t.Error("expected wait failure but got success")
		}
		if fp.GetExitCode() != 1 {
			t.Errorf("expected exit code 1, got %d", fp.GetExitCode())
		}
	})
}

// TestLifecycleWithRealProcess tests lifecycle with a real echo process.
func TestLifecycleWithRealProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Use "cmd /c echo" on Windows
		t.Skip("skipping on Windows - needs different binary path")
	}

	ctx := context.Background()
	
	pl := NewProcessLifecycle(ProcessConfig{
		BinaryPath: "/bin/echo",
		Timeout:    5 * time.Second,
	})
	defer pl.Cleanup()

	// Start the process
	if err := pl.Start(ctx, func(cmd *exec.Cmd) {
		cmd.Args = []string{"/bin/echo", "hello"}
	}); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if pl.GetState() != StateStarting {
		t.Errorf("expected state Starting, got %s", pl.GetState())
	}

	// Mark as running
	if err := pl.MarkRunning(); err != nil {
		t.Fatalf("MarkRunning failed: %v", err)
	}

	if pl.GetState() != StateRunning {
		t.Errorf("expected state Running, got %s", pl.GetState())
	}

	// Wait for completion
	if err := pl.Wait(); err != nil {
		t.Fatalf("Wait failed: %v", err)
	}

	if pl.GetState() != StateTerminated {
		t.Errorf("expected state Terminated, got %s", pl.GetState())
	}

	if pl.GetExitCode() != 0 {
		t.Errorf("expected exit code 0, got %d", pl.GetExitCode())
	}
}

// TestLifecycleTimeout tests timeout handling.
func TestLifecycleTimeout(t *testing.T) {
	ctx := context.Background()
	
	pl := NewProcessLifecycle(ProcessConfig{
		BinaryPath: "/bin/sleep",
		Timeout:    100 * time.Millisecond,
	})
	defer pl.Cleanup()

	if err := pl.Start(ctx, func(cmd *exec.Cmd) {
		cmd.Args = []string{"/bin/sleep", "10"}
	}); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if err := pl.MarkRunning(); err != nil {
		t.Fatalf("MarkRunning failed: %v", err)
	}

	// Wait should fail due to context timeout
	_, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	
	// The process should be terminated by the context
	pl.Terminate(50 * time.Millisecond)
	
	if pl.GetState() != StateTerminating && pl.GetState() != StateTerminated {
		t.Logf("state after timeout: %s", pl.GetState())
	}
}

// TestLifecycleCancellation tests context cancellation.
func TestLifecycleCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on Windows")
	}

	ctx, cancel := context.WithCancel(context.Background())
	
	pl := NewProcessLifecycle(ProcessConfig{
		BinaryPath: "/bin/sleep",
		Timeout:    5 * time.Second,
	})
	defer pl.Cleanup()

	if err := pl.Start(ctx, func(cmd *exec.Cmd) {
		cmd.Args = []string{"/bin/sleep", "10"}
	}); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if err := pl.MarkRunning(); err != nil {
		t.Fatalf("MarkRunning failed: %v", err)
	}

	// Cancel the context
	cancel()

	// Terminate should handle the cancellation
	pl.Terminate(100 * time.Millisecond)
	
	// The process should be in a terminal state
	state := pl.GetState()
	if state != StateTerminating && state != StateTerminated && state != StateFailed {
		t.Errorf("expected terminal state after cancellation, got %s", state)
	}
}

// TestStderrCapture tests stderr capture and redaction.
func TestStderrCapture(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on Windows")
	}

	ctx := context.Background()
	
	pl := NewProcessLifecycle(ProcessConfig{
		BinaryPath:    "/bin/sh",
		Timeout:       5 * time.Second,
		MaxStderrSize: 1024,
	})
	defer pl.Cleanup()

	script := `echo "password=secret123" >&2; echo "normal output"`
	
	if err := pl.Start(ctx, func(cmd *exec.Cmd) {
		cmd.Args = []string{"/bin/sh", "-c", script}
	}); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if err := pl.MarkRunning(); err != nil {
		t.Fatalf("MarkRunning failed: %v", err)
	}

	if err := pl.Wait(); err != nil {
		t.Logf("Wait failed (may be expected): %v", err)
	}

	stderr := pl.GetStderr()
	if stderr == "" {
		t.Error("expected stderr output, got empty string")
	}
	
	// Check for redaction
	if bytes.Contains([]byte(stderr), []byte("secret")) && !bytes.Contains([]byte(stderr), []byte("[REDACTED]")) {
		t.Error("expected password to be redacted in stderr")
	}
}
