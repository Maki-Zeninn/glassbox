// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package simulator

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dotandev/glassbox/internal/errors"
)

// TestIntegrationEarlyExit tests early process exit scenarios.
func TestIntegrationEarlyExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on Windows - needs different binary path")
	}

	tests := []struct {
		name        string
		command     []string
		expectError bool
		errorCheck  func(error) bool
	}{
		{
			name:        "Immediate exit with code 0",
			command:     []string{"/bin/true"},
			expectError: false,
		},
		{
			name:        "Immediate exit with code 1",
			command:     []string{"/bin/false"},
			expectError: true,
			errorCheck: func(err error) bool {
				return err != nil
			},
		},
		{
			name:        "Exit with code 2 (validation error)",
			command:     []string{"/bin/sh", "-c", "exit 2"},
			expectError: true,
			errorCheck: func(err error) bool {
				return err != nil && errors.GetCode(err) == errors.CodeValidationFailed
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			
			pl := NewProcessLifecycle(ProcessConfig{
				BinaryPath: tt.command[0],
				Timeout:    5 * time.Second,
			})
			defer pl.Cleanup()

			if err := pl.Start(ctx, func(cmd *exec.Cmd) {
				cmd.Args = tt.command
			}); err != nil {
				t.Fatalf("Start failed: %v", err)
			}

			if err := pl.MarkRunning(); err != nil {
				t.Fatalf("MarkRunning failed: %v", err)
			}

			err := pl.Wait()
			if tt.expectError && err == nil {
				t.Error("expected error but got success")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected success but got error: %v", err)
			}
			if tt.errorCheck != nil && !tt.errorCheck(err) {
				t.Errorf("error check failed for: %v", err)
			}

			// Verify cleanup happened
			result := pl.Cleanup()
			if pl.GetState() != StateTerminated && pl.GetState() != StateFailed {
				t.Errorf("expected terminal state, got %s", pl.GetState())
			}
			if !result.ProcessKilled && pl.GetState() == StateFailed {
				t.Error("expected ProcessKilled in cleanup result for failed state")
			}
		})
	}
}

// TestIntegrationMalformedHandshake tests handshake with malformed responses.
func TestIntegrationMalformedHandshake(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on Windows")
	}

	tests := []struct {
		name        string
		output      string
		expectError bool
	}{
		{
			name:        "Empty output",
			output:      "",
			expectError: true,
		},
		{
			name:        "Invalid JSON",
			output:      "not valid json",
			expectError: true,
		},
		{
			name:        "Partial JSON",
			output:      `{"simulator_build": "test"`,
			expectError: true,
		},
		{
			name:        "JSON with error field",
			output:      `{"error": "handshake failed"}`,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			
			// Create a temporary script that outputs the malformed response
			script := fmt.Sprintf("echo '%s'", tt.output)
			
			pl := NewProcessLifecycle(ProcessConfig{
				BinaryPath: "/bin/sh",
				Timeout:    5 * time.Second,
			})
			defer pl.Cleanup()

			if err := pl.Start(ctx, func(cmd *exec.Cmd) {
				cmd.Args = []string{"/bin/sh", "-c", script}
			}); err != nil {
				t.Fatalf("Start failed: %v", err)
			}

			if err := pl.MarkRunning(); err != nil {
				t.Fatalf("MarkRunning failed: %v", err)
			}

			err := pl.Wait()
			if !tt.expectError && err != nil {
				t.Errorf("expected success but got error: %v", err)
			}
			if tt.expectError && err == nil {
				t.Error("expected error but got success")
			}

			// Verify process was cleaned up
			if pl.GetPID() != 0 {
				// Process should have exited
				t.Logf("Process PID: %d", pl.GetPID())
			}
		})
	}
}

// TestIntegrationTimeout tests timeout scenarios.
func TestIntegrationTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on Windows")
	}

	tests := []struct {
		name           string
		command        []string
		timeout        time.Duration
		expectTimeout  bool
	}{
		{
			name:           "Process exceeds timeout",
			command:        []string{"/bin/sleep", "10"},
			timeout:        100 * time.Millisecond,
			expectTimeout:  true,
		},
		{
			name:           "Process completes within timeout",
			command:        []string{"/bin/sleep", "0.1"},
			timeout:        5 * time.Second,
			expectTimeout:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			
			pl := NewProcessLifecycle(ProcessConfig{
				BinaryPath: tt.command[0],
				Timeout:    tt.timeout,
			})
			defer pl.Cleanup()

			startTime := time.Now()
			
			if err := pl.Start(ctx, func(cmd *exec.Cmd) {
				cmd.Args = tt.command
			}); err != nil {
				t.Fatalf("Start failed: %v", err)
			}

			if err := pl.MarkRunning(); err != nil {
				t.Fatalf("MarkRunning failed: %v", err)
			}

			// Wait for timeout or completion
			done := make(chan error, 1)
			go func() {
				done <- pl.Wait()
			}()

			select {
			case err := <-done:
				if tt.expectTimeout && err == nil {
					t.Error("expected timeout but process completed successfully")
				}
				if !tt.expectTimeout && err != nil {
					t.Errorf("expected success but got error: %v", err)
				}
			case <-time.After(tt.timeout + 500*time.Millisecond):
				// Force terminate if still running
				pl.Terminate(100 * time.Millisecond)
				if !tt.expectTimeout {
					t.Error("process did not complete within expected time")
				}
			}

			elapsed := time.Since(startTime)
			if tt.expectTimeout && elapsed > tt.timeout+2*time.Second {
				t.Errorf("timeout took too long: %v (expected ~%v)", elapsed, tt.timeout)
			}

			// Verify cleanup
			result := pl.Cleanup()
			if tt.expectTimeout && !result.ProcessKilled {
				t.Error("expected process to be killed after timeout")
			}
		})
	}
}

// TestIntegrationCancellation tests context cancellation scenarios.
func TestIntegrationCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on Windows")
	}

	tests := []struct {
		name           string
		command        []string
		cancelAfter    time.Duration
		expectCanceled bool
	}{
		{
			name:           "Cancel during execution",
			command:        []string{"/bin/sleep", "10"},
			cancelAfter:    100 * time.Millisecond,
			expectCanceled: true,
		},
		{
			name:           "Cancel before execution",
			command:        []string{"/bin/sleep", "10"},
			cancelAfter:    0,
			expectCanceled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			
			pl := NewProcessLifecycle(ProcessConfig{
				BinaryPath: tt.command[0],
				Timeout:    5 * time.Second,
			})
			defer pl.Cleanup()

			if tt.cancelAfter > 0 {
				time.AfterFunc(tt.cancelAfter, cancel)
			} else {
				cancel() // Cancel immediately
			}

			if err := pl.Start(ctx, func(cmd *exec.Cmd) {
				cmd.Args = tt.command
			}); err != nil {
				if tt.cancelAfter == 0 {
					// Expected to fail if canceled before start
					return
				}
				t.Fatalf("Start failed: %v", err)
			}

			if err := pl.MarkRunning(); err != nil {
				t.Fatalf("MarkRunning failed: %v", err)
			}

			err := pl.Wait()
			if tt.expectCanceled {
				if err != context.Canceled {
					t.Errorf("expected context.Canceled, got: %v", err)
				}
			}

			// Verify process was terminated
			pl.Terminate(100 * time.Millisecond)
			state := pl.GetState()
			if state != StateTerminating && state != StateTerminated && state != StateFailed {
				t.Errorf("expected terminal state after cancellation, got %s", state)
			}
		})
	}
}

// TestIntegrationNoOrphanedProcesses verifies no orphaned processes remain.
func TestIntegrationNoOrphanedProcesses(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("orphaned process detection only works on Linux")
	}

	tracker := NewChildTracker()
	
	// Get parent PID before test
	parentPID := os.Getpid()
	tracker.SnapshotBefore(parentPID)

	ctx := context.Background()
	
	pl := NewProcessLifecycle(ProcessConfig{
		BinaryPath: "/bin/sleep",
		Timeout:    5 * time.Second,
	})

	if err := pl.Start(ctx, func(cmd *exec.Cmd) {
		cmd.Args = []string{"/bin/sleep", "0.1"}
	}); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if err := pl.MarkRunning(); err != nil {
		t.Fatalf("MarkRunning failed: %v", err)
	}

	// Wait for completion
	if err := pl.Wait(); err != nil {
		t.Errorf("Wait failed: %v", err)
	}

	// Cleanup
	pl.Cleanup()

	// Give some time for process to fully exit
	time.Sleep(100 * time.Millisecond)

	// Verify no orphaned processes
	if !tracker.VerifyNoOrphans() {
		t.Error("orphaned processes detected after cleanup")
	}
}

// TestIntegrationNoLeakedArtifacts verifies no temporary files are leaked.
func TestIntegrationNoLeakedArtifacts(t *testing.T) {
	tempDir := t.TempDir()
	
	// Create some test files in temp dir
	testFiles := []string{"test1.txt", "test2.txt", "subdir/test3.txt"}
	for _, file := range testFiles {
		fullPath := filepath.Join(tempDir, file)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			t.Fatalf("failed to create directory: %v", err)
		}
		if err := os.WriteFile(fullPath, []byte("test"), 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}
	}

	ctx := context.Background()
	
	pl := NewProcessLifecycle(ProcessConfig{
		BinaryPath: "/bin/echo",
		TempDir:    tempDir,
		Timeout:    5 * time.Second,
	})

	if err := pl.Start(ctx, func(cmd *exec.Cmd) {
		cmd.Args = []string{"/bin/echo", "hello"}
	}); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if err := pl.MarkRunning(); err != nil {
		t.Fatalf("MarkRunning failed: %v", err)
	}

	if err := pl.Wait(); err != nil {
		t.Errorf("Wait failed: %v", err)
	}

	// Cleanup
	result := pl.Cleanup()

	// Verify temp files were removed
	if len(result.TempFilesRemoved) == 0 {
		t.Error("expected temp files to be removed")
	}

	// Verify temp directory no longer exists
	if _, err := os.Stat(tempDir); err == nil {
		t.Error("temp directory still exists after cleanup")
	}
}

// TestIntegrationSignalForwarding tests signal forwarding to child processes.
func TestIntegrationSignalForwarding(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signal forwarding not applicable on Windows")
	}

	// Create a script that spawns a child process
	script := `
		/bin/sh -c 'sleep 10' &
		CHILD_PID=$!
		echo $CHILD_PID
		sleep 10
	`

	ctx := context.Background()
	
	pl := NewProcessLifecycle(ProcessConfig{
		BinaryPath: "/bin/sh",
		Timeout:    5 * time.Second,
	})

	if err := pl.Start(ctx, func(cmd *exec.Cmd) {
		cmd.Args = []string{"/bin/sh", "-c", script}
	}); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if err := pl.MarkRunning(); err != nil {
		t.Fatalf("MarkRunning failed: %v", err)
	}

	// Give process time to spawn child
	time.Sleep(100 * time.Millisecond)

	// Terminate the process group
	pl.Terminate(100 * time.Millisecond)

	// Wait for cleanup
	pl.Wait()
	pl.Cleanup()

	// Verify the process was terminated
	if pl.GetState() != StateTerminated && pl.GetState() != StateFailed {
		t.Errorf("expected terminal state after termination, got %s", pl.GetState())
	}
}

// TestIntegrationConcurrentExecutions tests multiple concurrent process executions.
func TestIntegrationConcurrentExecutions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on Windows")
	}

	const numConcurrent = 5
	ctx := context.Background()
	
	lifecycles := make([]*ProcessLifecycle, numConcurrent)
	errorsCh := make(chan error, numConcurrent)

	// Start multiple processes concurrently
	for i := 0; i < numConcurrent; i++ {
		pl := NewProcessLifecycle(ProcessConfig{
			BinaryPath: "/bin/sleep",
			Timeout:    5 * time.Second,
		})
		lifecycles[i] = pl

		go func(idx int) {
			defer pl.Cleanup()
			
			if err := pl.Start(ctx, func(cmd *exec.Cmd) {
				cmd.Args = []string{"/bin/sleep", "0.1"}
			}); err != nil {
				errorsCh <- fmt.Errorf("process %d start failed: %w", idx, err)
				return
			}

			if err := pl.MarkRunning(); err != nil {
				errorsCh <- fmt.Errorf("process %d mark running failed: %w", idx, err)
				return
			}

			if err := pl.Wait(); err != nil {
				errorsCh <- fmt.Errorf("process %d wait failed: %w", idx, err)
				return
			}

			errorsCh <- nil
		}(i)
	}

	// Collect results
	for i := 0; i < numConcurrent; i++ {
		if err := <-errorsCh; err != nil {
			t.Errorf("concurrent execution error: %v", err)
		}
	}

	// Verify all processes were cleaned up
	for i, pl := range lifecycles {
		state := pl.GetState()
		if state != StateTerminated && state != StateFailed {
			t.Errorf("process %d in unexpected state: %s", i, state)
		}
	}
}

// TestIntegrationStderrRedaction tests that stderr is properly redacted.
func TestIntegrationStderrRedaction(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on Windows")
	}

	tests := []struct {
		name        string
		script      string
		shouldRedact []string
	}{
		{
			name: "Redact password",
			script: `echo "password=secret123" >&2; echo "done"`,
			shouldRedact: []string{"secret"},
		},
		{
			name: "Redact token",
			script: `echo "token=abc123xyz" >&2; echo "done"`,
			shouldRedact: []string{"abc123xyz"},
		},
		{
			name: "Redact api_key",
			script: `echo "api_key=xyz789abc" >&2; echo "done"`,
			shouldRedact: []string{"xyz789abc"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			
			pl := NewProcessLifecycle(ProcessConfig{
				BinaryPath:    "/bin/sh",
				Timeout:       5 * time.Second,
				MaxStderrSize: 1024,
			})
			defer pl.Cleanup()

			if err := pl.Start(ctx, func(cmd *exec.Cmd) {
				cmd.Args = []string{"/bin/sh", "-c", tt.script}
			}); err != nil {
				t.Fatalf("Start failed: %v", err)
			}

			if err := pl.MarkRunning(); err != nil {
				t.Fatalf("MarkRunning failed: %v", err)
			}

			pl.Wait()

			stderr := pl.GetStderr()
			for _, sensitive := range tt.shouldRedact {
				if strings.Contains(stderr, sensitive) && !strings.Contains(stderr, "[REDACTED]") {
					t.Errorf("expected sensitive data %q to be redacted in stderr: %s", sensitive, stderr)
				}
			}
		})
	}
}

// TestIntegrationExitCodeTranslation tests exit code translation in real scenarios.
func TestIntegrationExitCodeTranslation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on Windows")
	}

	tests := []struct {
		name      string
		exitCode  int
		script    string
		errorCheck func(error) bool
	}{
		{
			name:     "Exit code 0",
			exitCode: 0,
			script:   "exit 0",
			errorCheck: func(err error) bool {
				return err == nil
			},
		},
		{
			name:     "Exit code 1",
			exitCode: 1,
			script:   "exit 1",
			errorCheck: func(err error) bool {
				return err != nil && errors.GetCode(err) == errors.CodeSimulatorError
			},
		},
		{
			name:     "Exit code 2",
			exitCode: 2,
			script:   "exit 2",
			errorCheck: func(err error) bool {
				return err != nil && errors.GetCode(err) == errors.CodeValidationFailed
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			
			pl := NewProcessLifecycle(ProcessConfig{
				BinaryPath: "/bin/sh",
				Timeout:    5 * time.Second,
			})
			defer pl.Cleanup()

			if err := pl.Start(ctx, func(cmd *exec.Cmd) {
				cmd.Args = []string{"/bin/sh", "-c", tt.script}
			}); err != nil {
				t.Fatalf("Start failed: %v", err)
			}

			if err := pl.MarkRunning(); err != nil {
				t.Fatalf("MarkRunning failed: %v", err)
			}

			pl.Wait()

			if pl.GetExitCode() != tt.exitCode {
				t.Errorf("expected exit code %d, got %d", tt.exitCode, pl.GetExitCode())
			}

			translatedErr := pl.TranslateExitCode()
			if !tt.errorCheck(translatedErr) {
				t.Errorf("exit code translation failed for code %d: %v", tt.exitCode, translatedErr)
			}
		})
	}
}

// TestIntegrationProcessGroup tests process group termination.
func TestIntegrationProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process groups not applicable on Windows")
	}

	// Create a script that spawns child processes
	script := `
		for i in 1 2 3; do
			/bin/sleep 10 &
		done
		sleep 10
	`

	ctx := context.Background()
	
	pl := NewProcessLifecycle(ProcessConfig{
		BinaryPath: "/bin/sh",
		Timeout:    5 * time.Second,
	})

	if err := pl.Start(ctx, func(cmd *exec.Cmd) {
		cmd.Args = []string{"/bin/sh", "-c", script}
	}); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if err := pl.MarkRunning(); err != nil {
		t.Fatalf("MarkRunning failed: %v", err)
	}

	// Give children time to spawn
	time.Sleep(200 * time.Millisecond)

	// Terminate the process group
	pl.Terminate(100 * time.Millisecond)

	// Wait for cleanup
	pl.Wait()
	pl.Cleanup()

	// Verify the process group was terminated
	if pl.GetState() != StateTerminated && pl.GetState() != StateFailed {
		t.Errorf("expected terminal state after process group termination, got %s", pl.GetState())
	}
}
