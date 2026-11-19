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

// ── PreflightCheck — platform validation ────────────────────────────────────

func TestPreflightCheck_UnsupportedPlatform(t *testing.T) {
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		t.Skip("test only applies to unsupported platforms")
	}
	r := newTestRegistrar(t)
	report := r.PreflightCheck()
	if report.OK {
		t.Error("expected OK=false on unsupported platform")
	}
	requirePreflightIssue(t, report, "platform")
}

// ── PreflightCheck — empty executable path ──────────────────────────────────

func TestPreflightCheck_EmptyExecutablePath(t *testing.T) {
	r := &Registrar{executablePath: "", homeDir: t.TempDir()}
	report := r.PreflightCheck()
	if report.OK {
		t.Error("expected OK=false when executablePath is empty")
	}
	requirePreflightIssue(t, report, "executable_path")
}

// ── PreflightCheck — missing executable ─────────────────────────────────────

func TestPreflightCheck_MissingExecutable(t *testing.T) {
	r := &Registrar{
		executablePath: "/nonexistent/path/glassbox",
		homeDir:        t.TempDir(),
	}
	report := r.PreflightCheck()
	if report.OK {
		t.Error("expected OK=false when executable does not exist")
	}
	requirePreflightIssue(t, report, "executable_path")
}

// ── PreflightCheck — non-executable binary (Unix) ──────────────────────────

func TestPreflightCheck_NonExecutableBinary_Unix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission check is Unix-only")
	}
	tmpDir := t.TempDir()
	binary := filepath.Join(tmpDir, "glassbox")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\necho test"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &Registrar{executablePath: binary, homeDir: tmpDir}
	report := r.PreflightCheck()
	if report.OK {
		t.Error("expected OK=false when binary is not executable")
	}
	requirePreflightIssue(t, report, "executable_permission")
	for _, issue := range report.Issues {
		if issue.Check == "executable_permission" {
			if !strings.Contains(issue.Hint, "chmod +x") {
				t.Errorf("hint should suggest chmod +x, got: %q", issue.Hint)
			}
		}
	}
}

// ── PreflightCheck — empty home directory ───────────────────────────────────

func TestPreflightCheck_EmptyHomeDir(t *testing.T) {
	r := &Registrar{
		executablePath: os.Args[0],
		homeDir:        "",
	}
	report := r.PreflightCheck()
	if report.OK {
		t.Error("expected OK=false when homeDir is empty")
	}
	requirePreflightIssue(t, report, "home_directory")
}

// ── PreflightCheck — non-directory home ─────────────────────────────────────

func TestPreflightCheck_HomeDirIsFile(t *testing.T) {
	tmpDir := t.TempDir()
	f := filepath.Join(tmpDir, "notadir")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &Registrar{
		executablePath: os.Args[0],
		homeDir:        f,
	}
	report := r.PreflightCheck()
	if report.OK {
		t.Error("expected OK=false when homeDir is a file")
	}
	requirePreflightIssue(t, report, "home_directory")
}

// ── PreflightCheck — Linux system tools ─────────────────────────────────────

func TestPreflightCheck_Linux_MissingXdgMime(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only test")
	}
	// We can't easily test missing xdg-mime since it's a system tool,
	// but we can verify the check runs.
	r := newTestRegistrar(t)
	report := r.PreflightCheck()
	// The xdg-mime check should either pass or fail based on system state.
	for _, issue := range report.Issues {
		if issue.Check == "xdg_mime" {
			if !strings.Contains(issue.Description, "xdg-mime") {
				t.Errorf("xdg_mime issue should mention xdg-mime, got: %q", issue.Description)
			}
		}
	}
}

// ── PreflightCheck — Linux conflict detection ──────────────────────────────

func TestPreflightCheck_Linux_ConflictDetected(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only test")
	}
	r := newTestRegistrar(t)

	// Write a desktop file pointing to a different wrapper path.
	if err := os.MkdirAll(filepath.Dir(r.linuxDesktopPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(r.linuxWrapperPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	// Write a wrapper script that references a different binary.
	staleWrapper := "#!/bin/sh\nexec '/other/path/glassbox' protocol-handler \"$1\"\n"
	if err := os.WriteFile(r.linuxWrapperPath(), []byte(staleWrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	// Write a desktop file whose Exec line points to a DIFFERENT wrapper path
	// than r.linuxWrapperPath() to trigger conflict detection.
	otherWrapper := "/other/path/glassbox-protocol-handler"
	staleDesktop := "[Desktop Entry]\nExec=" + otherWrapper + " %u\nMimeType=" + linuxMimeType + ";\n"
	if err := os.WriteFile(r.linuxDesktopPath(), []byte(staleDesktop), 0o644); err != nil {
		t.Fatal(err)
	}

	report := r.PreflightCheck()
	// Should detect conflict (warning, not error — registration can still proceed).
	found := false
	for _, issue := range report.Issues {
		if issue.Check == "conflict_linux" {
			found = true
			if issue.Severity != "warning" {
				t.Errorf("conflict should be a warning, got severity %q", issue.Severity)
			}
		}
	}
	if !found {
		t.Error("expected conflict_linux issue when wrapper points to different binary")
	}
}

func TestPreflightCheck_Linux_NoConflict(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only test")
	}
	r := newTestRegistrar(t)

	// Write artefacts pointing to the current binary.
	if err := os.MkdirAll(filepath.Dir(r.linuxDesktopPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(r.linuxWrapperPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.linuxWrapperPath(), []byte(r.unixHandlerScript()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.linuxDesktopPath(), []byte(r.linuxDesktopEntry()), 0o644); err != nil {
		t.Fatal(err)
	}

	report := r.PreflightCheck()
	for _, issue := range report.Issues {
		if issue.Check == "conflict_linux" {
			t.Errorf("should not detect conflict when wrapper points to current binary; got: %+v", issue)
		}
	}
}

// ── PreflightReport.Summary() ──────────────────────────────────────────────

func TestPreflightReport_Summary_NoIssues(t *testing.T) {
	report := &PreflightReport{OK: true}
	if s := report.Summary(); s != "" {
		t.Errorf("Summary() with no issues should be empty, got: %q", s)
	}
}

func TestPreflightReport_Summary_WithIssues(t *testing.T) {
	report := &PreflightReport{
		OK: false,
		Issues: []PreflightIssue{
			{Check: "platform", Severity: "error", Description: "not supported", Hint: "use Linux"},
		},
	}
	s := report.Summary()
	if s == "" {
		t.Fatal("Summary() should not be empty for a report with issues")
	}
	if !strings.Contains(s, "platform") {
		t.Errorf("Summary() should include check name, got: %q", s)
	}
	if !strings.Contains(s, "use Linux") {
		t.Errorf("Summary() should include hint, got: %q", s)
	}
}

func TestPreflightReport_Summary_MultipleIssues(t *testing.T) {
	report := &PreflightReport{
		OK: false,
		Issues: []PreflightIssue{
			{Check: "check1", Severity: "error", Description: "desc1", Hint: "hint1"},
			{Check: "check2", Severity: "warning", Description: "desc2", Hint: "hint2"},
		},
	}
	s := report.Summary()
	if !strings.Contains(s, "check1") || !strings.Contains(s, "check2") {
		t.Errorf("Summary() should include both checks, got: %q", s)
	}
}

func TestPreflightReport_Summary_IssueWithNoHint(t *testing.T) {
	report := &PreflightReport{
		OK: false,
		Issues: []PreflightIssue{
			{Check: "some_check", Severity: "warning", Description: "something", Hint: ""},
		},
	}
	s := report.Summary()
	if s == "" {
		t.Fatal("Summary() should not be empty")
	}
	if strings.Contains(s, "Hint:") {
		t.Errorf("Summary() should not emit 'Hint:' when hint is empty, got: %q", s)
	}
}

// ── PreflightCheck — all issues have actionable hints ──────────────────────

func TestPreflightCheck_AllIssuesHaveHints(t *testing.T) {
	r := &Registrar{executablePath: "", homeDir: t.TempDir()}
	report := r.PreflightCheck()
	for _, issue := range report.Issues {
		if strings.TrimSpace(issue.Hint) == "" {
			t.Errorf("issue %q has an empty Hint", issue.Check)
		}
		if strings.TrimSpace(issue.Description) == "" {
			t.Errorf("issue %q has an empty Description", issue.Check)
		}
	}
}

// ── PreflightCheck — checks populated on success ───────────────────────────

func TestPreflightCheck_Linux_ChecksPopulated(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only test")
	}
	r := newTestRegistrar(t)
	report := r.PreflightCheck()
	if len(report.Checks) == 0 {
		t.Error("Checks should be populated when preflight runs")
	}
	for i, c := range report.Checks {
		if strings.TrimSpace(c) == "" {
			t.Errorf("check entry %d is empty", i)
		}
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────

func requirePreflightIssue(t *testing.T, report *PreflightReport, check string) {
	t.Helper()
	for _, issue := range report.Issues {
		if issue.Check == check {
			return
		}
	}
	t.Errorf("expected an issue for check %q; got issues: %v", check, report.Issues)
}
