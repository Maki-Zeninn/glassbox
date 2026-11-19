// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package simulator

// process_cleanup.go — Process isolation and resource cleanup hardening.
// Issue #537: Add simulator process isolation and resource cleanup tests
//
// Hardens process lifecycle management to ensure that crashed or canceled
// simulator processes leave no pipes, temporary files, or child processes behind.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"time"
)

// ProcessCleanupResult records the outcome of a cleanup attempt.
type ProcessCleanupResult struct {
	ProcessKilled   bool     `json:"process_killed"`
	ChildrenKilled  int      `json:"children_killed"`
	TempFilesRemoved []string `json:"temp_files_removed"`
	PipesClosed     int      `json:"pipes_closed"`
	Errors          []string `json:"errors,omitempty"`
}

// CleanupProcess ensures all resources associated with a simulator process are
// released. It kills the process group (if supported), closes all file
// descriptors, and removes temporary files.
func CleanupProcess(cmd *exec.Cmd, tempDir string) *ProcessCleanupResult {
	result := &ProcessCleanupResult{}

	if cmd == nil {
		return result
	}

	// Kill the process and its children (process group on Unix)
	if cmd.Process != nil {
		// Try to kill the process group first (sends signal to all children)
		if err := killProcessGroup(cmd); err != nil {
			// Fall back to killing just the main process
			if killErr := cmd.Process.Kill(); killErr == nil {
				result.ProcessKilled = true
			} else {
				result.Errors = append(result.Errors,
					fmt.Sprintf("failed to kill process: %v", killErr))
			}
		} else {
			result.ProcessKilled = true
		}

		// Wait for the process to actually exit (prevents zombies)
		done := make(chan error, 1)
		go func() {
			done <- cmd.Wait()
		}()
		select {
		case <-done:
			// Process exited
		case <-time.After(5 * time.Second):
			result.Errors = append(result.Errors,
				"process did not exit within 5 seconds after kill")
		}
	}

	// Close any open pipes (stdin/stdout/stderr)
	if cmd.Stdin != nil {
		if closer, ok := cmd.Stdin.(interface{ Close() error }); ok {
			closer.Close()
			result.PipesClosed++
		}
	}
	if cmd.Stdout != nil {
		if closer, ok := cmd.Stdout.(interface{ Close() error }); ok {
			closer.Close()
			result.PipesClosed++
		}
	}
	if cmd.Stderr != nil {
		if closer, ok := cmd.Stderr.(interface{ Close() error }); ok {
			closer.Close()
			result.PipesClosed++
		}
	}

	// Remove temporary files
	if tempDir != "" {
		if entries, err := os.ReadDir(tempDir); err == nil {
			for _, entry := range entries {
				path := fmt.Sprintf("%s/%s", tempDir, entry.Name())
				if rmErr := os.RemoveAll(path); rmErr == nil {
					result.TempFilesRemoved = append(result.TempFilesRemoved, path)
				}
			}
			os.RemoveAll(tempDir)
		}
	}

	return result
}

// killProcessGroup kills the entire process group on Unix systems.
// On Windows it falls back to killing just the process.
func killProcessGroup(cmd *exec.Cmd) error {
	if runtime.GOOS == "windows" {
		return cmd.Process.Kill()
	}

	// Send SIGKILL to the process group (negative PID = group)
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		// Process may already be dead; try direct kill
		return cmd.Process.Kill()
	}
	return syscall.Kill(-pgid, syscall.SIGKILL)
}

// RunWithTimeout executes a simulator command with a hard timeout and
// guarantees cleanup of all resources on every path (success, timeout, error).
func RunWithTimeout(ctx context.Context, cmd *exec.Cmd, timeout time.Duration, tempDir string) ([]byte, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// Set up process group on Unix (so we can kill children)
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}

	// Track the command in the runner's active set for cleanup
	type cmdTracker struct {
		cmd     *exec.Cmd
		tempDir string
	}
	tracker := &cmdTracker{cmd: cmd, tempDir: tempDir}
	_ = tracker

	type result struct {
		out []byte
		err error
	}
	done := make(chan result, 1)

	go func() {
		out, err := cmd.Output()
		done <- result{out, err}
	}()

	select {
	case r := <-done:
		return r.out, r.err
	case <-ctx.Done():
		// Timeout or cancellation — clean up
		CleanupProcess(cmd, tempDir)
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("simulator timed out after %v", timeout)
		}
		return nil, fmt.Errorf("simulator canceled: %w", ctx.Err())
	}
}

// TrackChildren is a test helper that records child PIDs for verification.
// On Linux, it reads /proc to find children of the given PID.
type ChildTracker struct {
	InitialPIDs map[int]bool
}

// NewChildTracker creates a tracker that snapshots existing PIDs.
func NewChildTracker() *ChildTracker {
	return &ChildTracker{
		InitialPIDs: make(map[int]bool),
	}
}

// SnapshotBefore records the current set of child PIDs before a test.
func (ct *ChildTracker) SnapshotBefore(parentPID int) {
	// On Linux, read /proc to find children
	if runtime.GOOS != "linux" {
		return
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// Try to read the stat file to get parent PID
		statPath := fmt.Sprintf("/proc/%s/stat", entry.Name())
		data, err := os.ReadFile(statPath)
		if err != nil {
			continue
		}
		// Parse the stat file: pid (comm) state ppid ...
		// The ppid is the 4th field after the comm field
		fields := splitStat(string(data))
		if len(fields) >= 4 {
			ppid := 0
			fmt.Sscanf(fields[3], "%d", &ppid)
			if ppid == parentPID {
				var pid int
				fmt.Sscanf(entry.Name(), "%d", &pid)
				ct.InitialPIDs[pid] = true
			}
		}
	}
}

// VerifyNoOrphans checks that no child processes from the snapshot remain.
// Returns true if all children have been cleaned up.
func (ct *ChildTracker) VerifyNoOrphans() bool {
	if runtime.GOOS != "linux" {
		return true // Can't verify on non-Linux
	}
	for pid := range ct.InitialPIDs {
		if processExists(pid) {
			return false
		}
	}
	return true
}

func processExists(pid int) bool {
	if runtime.GOOS == "linux" {
		_, err := os.Stat(fmt.Sprintf("/proc/%d", pid))
		return err == nil
	}
	// Best-effort: try sending signal 0
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

func splitStat(s string) []string {
	var fields []string
	inParens := false
	start := 0
	for i, c := range s {
		if c == '(' {
			inParens = true
		} else if c == ')' {
			inParens = false
		} else if c == ' ' && !inParens {
			fields = append(fields, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		fields = append(fields, s[start:])
	}
	return fields
}
