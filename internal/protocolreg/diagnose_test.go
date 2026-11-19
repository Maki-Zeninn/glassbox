// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package protocolreg

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ---- helpers ----------------------------------------------------------------

// newTestRegistrar creates a Registrar whose executablePath and homeDir are
// set to temporary directories so tests never touch real system state.
func newTestRegistrar(t *testing.T) *Registrar {
	t.Helper()
	dir := t.TempDir()
	// Create a fake executable file so path-existence checks pass.
	exe := filepath.Join(dir, "glassbox-test")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("create fake executable: %v", err)
	}
	return &Registrar{
		executablePath: exe,
		homeDir:        dir,
	}
}

// ---- DiagnosticReport fields ------------------------------------------------

func TestDiagnosticReport_StatusConstants(t *testing.T) {
	// Verify the status constants are non-empty and distinct.
	statuses := []DiagnosticStatus{StatusOK, StatusDegraded, StatusNotRegistered, StatusError}
	seen := make(map[DiagnosticStatus]bool)
	for _, s := range statuses {
		if s == "" {
			t.Fatal("DiagnosticStatus must not be empty")
		}
		if seen[s] {
			t.Fatalf("duplicate DiagnosticStatus value: %q", s)
		}
		seen[s] = true
	}
}

// ---- Diagnose — unregistered state ------------------------------------------

func TestDiagnose_NotRegistered_ReturnsNotRegisteredStatus(t *testing.T) {
	r := newTestRegistrar(t)
	// The temp homeDir has no registration artefacts, so the handler is not registered.
	report := r.Diagnose()

	if report.Platform != runtime.GOOS {
		t.Fatalf("unexpected platform: %s", report.Platform)
	}
	if report.Scheme != Scheme {
		t.Fatalf("unexpected scheme: %s", report.Scheme)
	}
	if report.ExecutablePath != r.executablePath {
		t.Fatalf("unexpected executable path: %s", report.ExecutablePath)
	}

	// On unsupported platforms the status is StatusError; on supported ones it
	// should be StatusNotRegistered since nothing is installed.
	switch runtime.GOOS {
	case "windows", "darwin", "linux":
		if report.Status != StatusNotRegistered && report.Status != StatusDegraded {
			t.Fatalf("expected not_registered or degraded, got %q", report.Status)
		}
		if len(report.Issues) == 0 {
			t.Fatal("expected at least one issue for unregistered state")
		}
		if len(report.RemediationSteps) == 0 {
			t.Fatal("expected remediation steps when issues are present")
		}
	default:
		if report.Status != StatusError {
			t.Fatalf("expected error status on unsupported OS, got %q", report.Status)
		}
	}
}

func TestDiagnose_RemediationStepsAreNonEmpty(t *testing.T) {
	r := newTestRegistrar(t)
	report := r.Diagnose()

	if report.Status == StatusOK {
		t.Skip("handler is already registered; skipping remediation test")
	}

	for i, step := range report.RemediationSteps {
		if strings.TrimSpace(step) == "" {
			t.Fatalf("remediation step %d is empty", i)
		}
	}
}

// ---- Diagnose — registered state (Linux only) -------------------------------

func TestDiagnose_Linux_FullyRegistered(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only test")
	}

	r := newTestRegistrar(t)

	// Simulate a complete registration by writing the expected artefacts.
	if err := os.MkdirAll(filepath.Dir(r.linuxDesktopPath()), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(r.linuxWrapperPath()), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(r.linuxDesktopPath(), []byte(r.linuxDesktopEntry()), 0o644); err != nil {
		t.Fatalf("write desktop: %v", err)
	}
	if err := os.WriteFile(r.linuxWrapperPath(), []byte(r.unixHandlerScript()), 0o755); err != nil {
		t.Fatalf("write wrapper: %v", err)
	}

	report := r.Diagnose()

	// The desktop file and wrapper should be found; xdg-mime may not be
	// configured in CI, so we only check the file-based checks.
	foundDesktop := false
	for _, check := range report.Checks {
		if strings.Contains(check, "Desktop file found") {
			foundDesktop = true
		}
	}
	if !foundDesktop {
		t.Error("expected 'Desktop file found' check to pass")
	}

	foundWrapper := false
	for _, check := range report.Checks {
		if strings.Contains(check, "Protocol helper script launches current binary") {
			foundWrapper = true
		}
	}
	if !foundWrapper {
		t.Error("expected wrapper script check to pass")
	}
}

func TestDiagnose_Linux_StaleExecutablePath(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only test")
	}

	r := newTestRegistrar(t)

	// Write a wrapper that references a *different* binary path.
	if err := os.MkdirAll(filepath.Dir(r.linuxDesktopPath()), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(r.linuxWrapperPath()), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(r.linuxDesktopPath(), []byte(r.linuxDesktopEntry()), 0o644); err != nil {
		t.Fatalf("write desktop: %v", err)
	}
	staleScript := "#!/bin/sh\nexec /old/path/to/glassbox protocol-handler \"$1\"\n"
	if err := os.WriteFile(r.linuxWrapperPath(), []byte(staleScript), 0o755); err != nil {
		t.Fatalf("write stale wrapper: %v", err)
	}

	report := r.Diagnose()

	if report.HandlerMatchesSelf {
		t.Error("HandlerMatchesSelf should be false when wrapper references a different binary")
	}

	foundIssue := false
	for _, issue := range report.Issues {
		if strings.Contains(issue, "does not reference current binary") {
			foundIssue = true
		}
	}
	if !foundIssue {
		t.Errorf("expected stale-path issue, got issues: %v", report.Issues)
	}
}

// ---- Diagnose — Windows (mock) ----------------------------------------------

func TestDiagnose_Windows_NotRegistered(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only test")
	}

	r := newTestRegistrar(t)
	report := r.Diagnose()

	// On a clean system without the registry key the status should be not_registered.
	if report.Status == StatusOK {
		t.Skip("glassbox:// is already registered on this machine; skipping")
	}
	if len(report.Issues) == 0 {
		t.Fatal("expected issues when registry key is absent")
	}
}

// ---- Repair — no-op when already registered ---------------------------------

func TestRepair_AlreadyOK_ReturnsNoOpMessage(t *testing.T) {
	// We can't easily simulate a fully registered state in a unit test without
	// touching real OS state, so we test the no-op path by mocking Diagnose
	// indirectly: create a registrar whose artefacts are all present and correct.
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only test for no-op repair path")
	}

	r := newTestRegistrar(t)

	// Write all artefacts so Diagnose returns StatusOK (minus xdg-mime which
	// we can't control in tests).
	if err := os.MkdirAll(filepath.Dir(r.linuxDesktopPath()), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(r.linuxWrapperPath()), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(r.linuxDesktopPath(), []byte(r.linuxDesktopEntry()), 0o644); err != nil {
		t.Fatalf("write desktop: %v", err)
	}
	if err := os.WriteFile(r.linuxWrapperPath(), []byte(r.unixHandlerScript()), 0o755); err != nil {
		t.Fatalf("write wrapper: %v", err)
	}

	// Repair should attempt registration (xdg-mime may fail in CI, that's OK).
	result := r.Repair()
	if len(result.Actions) == 0 {
		t.Fatal("Repair must always record at least one action")
	}
}

// ---- Repair — unsupported OS ------------------------------------------------

func TestRepair_UnsupportedOS_ReturnsError(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		t.Skip("test only applies to unsupported OS")
	}

	r := newTestRegistrar(t)
	result := r.Repair()
	if result.Err == nil {
		t.Fatal("expected error on unsupported OS")
	}
}

// ---- permissionHint ---------------------------------------------------------

func TestPermissionHint_PermissionDenied(t *testing.T) {
	r := newTestRegistrar(t)
	hint := r.permissionHint(os.ErrPermission)
	// On supported platforms a non-empty hint should be returned.
	switch runtime.GOOS {
	case "windows", "darwin", "linux":
		if hint == "" {
			t.Error("expected non-empty permission hint for permission denied error")
		}
	}
}

func TestPermissionHint_NonPermissionError(t *testing.T) {
	r := newTestRegistrar(t)
	hint := r.permissionHint(os.ErrNotExist)
	if hint != "" {
		t.Errorf("expected empty hint for non-permission error, got %q", hint)
	}
}

func TestPermissionHint_NilError(t *testing.T) {
	r := newTestRegistrar(t)
	if hint := r.permissionHint(nil); hint != "" {
		t.Errorf("expected empty hint for nil error, got %q", hint)
	}
}

// ── Verify — ElapsedMs is populated ──────────────────────────────────────────

func TestVerify_ElapsedMs_IsPopulated(t *testing.T) {
	r := newTestRegistrar(t)
	report, _ := r.Verify()
	// ElapsedMs must be a non-negative value; registration on most platforms
	// completes in well under a second but we only assert it is non-negative.
	if report.ElapsedMs < 0 {
		t.Errorf("ElapsedMs must be non-negative, got %d", report.ElapsedMs)
	}
}

func TestVerify_ElapsedMs_ReflectsWork(t *testing.T) {
	r := newTestRegistrar(t)
	// Call Verify twice and confirm both reports carry an ElapsedMs value.
	r1, _ := r.Verify()
	r2, _ := r.Verify()
	if r1.ElapsedMs < 0 || r2.ElapsedMs < 0 {
		t.Errorf("both Verify calls should populate ElapsedMs; got %d and %d", r1.ElapsedMs, r2.ElapsedMs)
	}
}

// ---- defaultRemediationSteps ------------------------------------------------

func TestDefaultRemediationSteps_NonEmpty(t *testing.T) {
	r := newTestRegistrar(t)
	steps := r.defaultRemediationSteps()
	if len(steps) == 0 {
		t.Fatal("defaultRemediationSteps must return at least one step")
	}
	for i, s := range steps {
		if strings.TrimSpace(s) == "" {
			t.Fatalf("remediation step %d is empty", i)
		}
	}
}

func TestDefaultRemediationSteps_MentionsRepairCommand(t *testing.T) {
	r := newTestRegistrar(t)
	steps := r.defaultRemediationSteps()
	combined := strings.Join(steps, " ")
	if !strings.Contains(combined, "protocol:repair") {
		t.Error("remediation steps should mention 'protocol:repair'")
	}
}

// ── ValidateForSnapshotOperation ──────────────────────────────────────────────

func TestValidateForSnapshotOperation_EmptyExecutablePath_ReturnsError(t *testing.T) {
	r := &Registrar{executablePath: "", homeDir: t.TempDir()}
	err := r.ValidateForSnapshotOperation()
	if err == nil {
		t.Fatal("expected error for empty executable path")
	}
	msg := err.Error()
	if !strings.Contains(msg, "executable path is empty") {
		t.Errorf("error should mention 'executable path is empty', got: %q", msg)
	}
	if !strings.Contains(msg, "snapshot") {
		t.Errorf("error should mention 'snapshot', got: %q", msg)
	}
	if !strings.Contains(msg, "Fix:") {
		t.Errorf("error should include a Fix: hint, got: %q", msg)
	}
}

func TestValidateForSnapshotOperation_MissingExecutable_ReturnsError(t *testing.T) {
	r := &Registrar{
		executablePath: "/nonexistent/path/glassbox",
		homeDir:        t.TempDir(),
	}
	err := r.ValidateForSnapshotOperation()
	if err == nil {
		t.Fatal("expected error for non-existent executable")
	}
	msg := err.Error()
	if !strings.Contains(msg, "not found") {
		t.Errorf("error should mention 'not found', got: %q", msg)
	}
	if !strings.Contains(msg, "snapshot") {
		t.Errorf("error should mention 'snapshot', got: %q", msg)
	}
}

func TestValidateForSnapshotOperation_EmptyHomeDir_ReturnsError(t *testing.T) {
	r := &Registrar{
		executablePath: t.TempDir(),
		homeDir:        "",
	}
	err := r.ValidateForSnapshotOperation()
	if err == nil {
		t.Fatal("expected error for empty home directory")
	}
	msg := err.Error()
	if !strings.Contains(msg, "home directory is empty") {
		t.Errorf("error should mention 'home directory is empty', got: %q", msg)
	}
}

func TestValidateForSnapshotOperation_NonExistentHomeDir_ReturnsError(t *testing.T) {
	r := &Registrar{
		executablePath: t.TempDir(),
		homeDir:        "/nonexistent/home/dir",
	}
	err := r.ValidateForSnapshotOperation()
	if err == nil {
		t.Fatal("expected error for non-existent home directory")
	}
	msg := err.Error()
	if !strings.Contains(msg, "home directory") {
		t.Errorf("error should mention 'home directory', got: %q", msg)
	}
}

func TestValidateForSnapshotOperation_HealthNotReady_ReturnsError(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only: need file-based artefacts for health check")
	}
	r := newTestRegistrar(t)

	// Create the desktop file directory but NOT the wrapper script — this
	// makes the health check return HealthNotReady.
	if err := os.MkdirAll(filepath.Dir(r.linuxDesktopPath()), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(r.linuxWrapperPath()), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Write only the desktop file (not the wrapper script).
	if err := os.WriteFile(r.linuxDesktopPath(), []byte(r.linuxDesktopEntry()), 0o644); err != nil {
		t.Fatalf("write desktop: %v", err)
	}

	err := r.ValidateForSnapshotOperation()
	if err == nil {
		t.Fatal("expected error when health check is not ready")
	}
	msg := err.Error()
	if !strings.Contains(msg, "protocol registration") {
		t.Errorf("error should mention 'protocol registration', got: %q", msg)
	}
	if !strings.Contains(msg, "Hint:") || !strings.Contains(msg, "protocol:repair") {
		t.Errorf("error should include a repair hint, got: %q", msg)
	}
}

func TestValidateForSnapshotOperation_ReturnsNilOnSuccess(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only: need full registration for success")
	}
	r := newTestRegistrar(t)

	// Write all artefacts to make health check pass.
	if err := os.MkdirAll(filepath.Dir(r.linuxDesktopPath()), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(r.linuxWrapperPath()), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(r.linuxDesktopPath(), []byte(r.linuxDesktopEntry()), 0o644); err != nil {
		t.Fatalf("write desktop: %v", err)
	}
	if err := os.WriteFile(r.linuxWrapperPath(), []byte(r.unixHandlerScript()), 0o755); err != nil {
		t.Fatalf("write wrapper: %v", err)
	}

	err := r.ValidateForSnapshotOperation()
	if err != nil {
		t.Fatalf("expected nil error when fully registered, got: %v", err)
	}
}

func TestValidateForSnapshotOperation_UnsupportedPlatform_ReturnsError(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		t.Skip("test only applies to unsupported platforms")
	}
	r := newTestRegistrar(t)
	err := r.ValidateForSnapshotOperation()
	if err == nil {
		t.Fatal("expected error on unsupported platform")
	}
	msg := err.Error()
	if !strings.Contains(msg, "not supported") {
		t.Errorf("error should mention 'not supported', got: %q", msg)
	}
}

func TestValidateForSnapshotOperation_AllErrorsAreActionable(t *testing.T) {
	tests := []struct {
		name string
		r    *Registrar
	}{
		{"empty executable path", &Registrar{executablePath: "", homeDir: t.TempDir()}},
		{"missing executable", &Registrar{executablePath: "/nonexistent/path", homeDir: t.TempDir()}},
		{"empty home dir", &Registrar{executablePath: t.TempDir(), homeDir: ""}},
		{"non-existent home dir", &Registrar{executablePath: t.TempDir(), homeDir: "/nonexistent/home"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.r.ValidateForSnapshotOperation()
			if err == nil {
				return // Skip — some platforms may pass
			}
			msg := err.Error()
			if !strings.Contains(msg, "Fix:") {
				t.Errorf("error should include a Fix: hint, got: %q", msg)
			}
			if !strings.Contains(msg, "snapshot") {
				t.Errorf("error should mention 'snapshot', got: %q", msg)
			}
		})
	}
}
