// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package protocolreg

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// PreflightCheck validates all environment conditions before attempting protocol
// registration. Unlike Register, it never modifies system state. Use this to
// detect and surface problems early, giving users actionable guidance before
// any OS artefacts are written.
//
// Checks performed:
//   - Platform is supported (linux, darwin, windows)
//   - Executable path is non-empty and reachable
//   - Executable has execute permission (Unix)
//   - Home directory exists and is writable
//   - No conflicting registration exists for the glassbox:// scheme
//   - Required system tools are available (xdg-mime on Linux, lsregister on macOS)
//   - Target directories for registration artefacts are reachable
func (r *Registrar) PreflightCheck() *PreflightReport {
	report := &PreflightReport{Platform: runtime.GOOS, Scheme: Scheme}

	// Step 1: Validate platform support.
	switch runtime.GOOS {
	case "linux", "darwin", "windows":
		report.Checks = append(report.Checks, fmt.Sprintf("platform %s is supported", runtime.GOOS))
	default:
		report.Issues = append(report.Issues, PreflightIssue{
			Check:       "platform",
			Severity:    "error",
			Description: fmt.Sprintf("protocol registration is not supported on %s", runtime.GOOS),
			Hint:        "Protocol registration is only supported on Linux, macOS, and Windows.",
		})
		report.OK = false
		return report
	}

	// Step 2: Validate executable path is non-empty.
	if r.executablePath == "" {
		report.Issues = append(report.Issues, PreflightIssue{
			Check:       "executable_path",
			Severity:    "error",
			Description: "executable path is empty — cannot register a handler that points nowhere",
			Hint:        "Ensure glassbox is invoked from a valid binary, not via 'go run' or an empty path.",
		})
		report.OK = false
		return report
	}
	report.Checks = append(report.Checks, fmt.Sprintf("executable path non-empty: %s", r.executablePath))

	// Step 3: Validate executable exists and is reachable.
	info, err := os.Stat(r.executablePath)
	if err != nil {
		report.Issues = append(report.Issues, PreflightIssue{
			Check:       "executable_path",
			Severity:    "error",
			Description: fmt.Sprintf("executable not found at %s: %v", r.executablePath, err),
			Hint:        "Reinstall Glassbox or verify the binary path is correct.",
		})
		report.OK = false
		return report
	}
	report.Checks = append(report.Checks, "executable exists and is reachable")

	// Step 4: Validate executable permissions (Unix).
	if runtime.GOOS != "windows" {
		if info.Mode()&0o111 == 0 {
			report.Issues = append(report.Issues, PreflightIssue{
				Check:       "executable_permission",
				Severity:    "error",
				Description: fmt.Sprintf("executable at %s is not executable (permissions: %04o)", r.executablePath, info.Mode()&0o777),
				Hint:        fmt.Sprintf("Run 'chmod +x %s' to make the binary executable.", r.executablePath),
			})
			report.OK = false
			return report
		}
		report.Checks = append(report.Checks, "executable has execute permission")
	}

	// Step 5: Validate home directory.
	if r.homeDir == "" {
		report.Issues = append(report.Issues, PreflightIssue{
			Check:       "home_directory",
			Severity:    "error",
			Description: "home directory is empty — cannot determine where to write registration artefacts",
			Hint:        "Set the HOME environment variable or ensure os.UserHomeDir() returns a valid path.",
		})
		report.OK = false
		return report
	}
	homeInfo, homeErr := os.Stat(r.homeDir)
	if homeErr != nil {
		report.Issues = append(report.Issues, PreflightIssue{
			Check:       "home_directory",
			Severity:    "error",
			Description: fmt.Sprintf("home directory %s is not accessible: %v", r.homeDir, homeErr),
			Hint:        "Ensure the home directory exists and is readable.",
		})
		report.OK = false
		return report
	}
	if !homeInfo.IsDir() {
		report.Issues = append(report.Issues, PreflightIssue{
			Check:       "home_directory",
			Severity:    "error",
			Description: fmt.Sprintf("home path %s is not a directory", r.homeDir),
			Hint:        "HOME should point to a directory, not a file.",
		})
		report.OK = false
		return report
	}
	report.Checks = append(report.Checks, fmt.Sprintf("home directory accessible: %s", r.homeDir))

	// Step 6: Validate artefact target directories are writable.
	r.preflightArtefactDirectories(report)
	if !report.OK {
		return report
	}

	// Step 7: Check for system tools required for registration.
	r.preflightSystemTools(report)
	if !report.OK {
		return report
	}

	// Step 8: Detect existing conflicting registrations.
	r.preflightConflicts(report)

	report.OK = !hasPreflightErrors(report.Issues)
	return report
}

// preflightArtefactDirectories checks that the directories where registration
// artefacts will be written are reachable (or can be created).
func (r *Registrar) preflightArtefactDirectories(report *PreflightReport) {
	switch runtime.GOOS {
	case "linux":
		desktopDir := filepath.Dir(r.linuxDesktopPath())
		if err := ensureDirectory(desktopDir); err != nil {
			report.Issues = append(report.Issues, PreflightIssue{
				Check:       "desktop_directory",
				Severity:    "error",
				Description: fmt.Sprintf("cannot create or access desktop file directory %s: %v", desktopDir, err),
				Hint:        "Ensure your home directory is writable and ~/.local/share/applications can be created.",
			})
			report.OK = false
			return
		}
		report.Checks = append(report.Checks, fmt.Sprintf("desktop file directory reachable: %s", desktopDir))

		wrapperDir := filepath.Dir(r.linuxWrapperPath())
		if err := ensureDirectory(wrapperDir); err != nil {
			report.Issues = append(report.Issues, PreflightIssue{
				Check:       "wrapper_directory",
				Severity:    "error",
				Description: fmt.Sprintf("cannot create or access wrapper script directory %s: %v", wrapperDir, err),
				Hint:        "Ensure ~/.local/share/glassbox can be created.",
			})
			report.OK = false
			return
		}
		report.Checks = append(report.Checks, fmt.Sprintf("wrapper script directory reachable: %s", wrapperDir))

	case "darwin":
		bundleDir := filepath.Dir(r.macOSAppPath())
		if err := ensureDirectory(bundleDir); err != nil {
			report.Issues = append(report.Issues, PreflightIssue{
				Check:       "app_bundle_directory",
				Severity:    "error",
				Description: fmt.Sprintf("cannot create or access macOS app bundle directory %s: %v", bundleDir, err),
				Hint:        "Ensure ~/Applications exists and is writable.",
			})
			report.OK = false
			return
		}
		report.Checks = append(report.Checks, fmt.Sprintf("app bundle directory reachable: %s", bundleDir))

	case "windows":
		// Windows writes to the registry — no filesystem artefact directories to validate.
		report.Checks = append(report.Checks, "Windows registry target (no filesystem artefact directories)")
	}
}

// preflightSystemTools checks that external tools required for registration
// are available on the system.
func (r *Registrar) preflightSystemTools(report *PreflightReport) {
	switch runtime.GOOS {
	case "linux":
		if !hasCommand("xdg-mime") {
			report.Issues = append(report.Issues, PreflightIssue{
				Check:       "xdg_mime",
				Severity:    "error",
				Description: "xdg-mime is not installed — cannot register the glassbox:// MIME handler",
				Hint: "Install xdg-utils:\n" +
					"  sudo apt install xdg-utils   (Debian/Ubuntu)\n" +
					"  sudo dnf install xdg-utils   (Fedora/RHEL)\n" +
					"  sudo pacman -S xdg-utils     (Arch Linux)",
			})
			report.OK = false
			return
		}
		report.Checks = append(report.Checks, "xdg-mime is available")

	case "darwin":
		if !hasCommand(macOSLSRegisterPath()) {
			report.Issues = append(report.Issues, PreflightIssue{
				Check:       "lsregister",
				Severity:    "error",
				Description: fmt.Sprintf("LaunchServices registration tool not found at %s", macOSLSRegisterPath()),
				Hint:        "This tool is part of macOS and should always be present. Your macOS installation may be corrupted.",
			})
			report.OK = false
			return
		}
		report.Checks = append(report.Checks, "lsregister is available")

	case "windows":
		// Windows uses 'reg' which is always available.
		report.Checks = append(report.Checks, "reg command available (built-in)")
	}
}

// preflightConflicts checks for existing registrations that would conflict
// with the new registration. This is a read-only check — it does not modify
// any system state.
func (r *Registrar) preflightConflicts(report *PreflightReport) {
	switch runtime.GOOS {
	case "linux":
		desktopBytes, err := os.ReadFile(r.linuxDesktopPath())
		if err != nil {
			// No existing registration — no conflict.
			report.Checks = append(report.Checks, "no existing Linux registration (no conflict)")
			return
		}
		desktopContent := string(desktopBytes)
		if strings.Contains(desktopContent, "Exec="+r.linuxWrapperPath()) {
			report.Checks = append(report.Checks, "existing Linux registration points to current binary (no conflict)")
		} else {
			report.Issues = append(report.Issues, PreflightIssue{
				Check:       "conflict_linux",
				Severity:    "warning",
				Description: "an existing glassbox:// registration exists but points to a different binary",
				Hint:        "Run 'glassbox protocol:repair' to reclaim the registration. This will overwrite the existing handler.",
			})
		}

	case "darwin":
		execBytes, err := os.ReadFile(r.macOSExecutablePath())
		if err != nil {
			report.Checks = append(report.Checks, "no existing macOS registration (no conflict)")
			return
		}
		execContent := string(execBytes)
		if strings.Contains(execContent, r.executablePath) {
			report.Checks = append(report.Checks, "existing macOS registration points to current binary (no conflict)")
		} else {
			report.Issues = append(report.Issues, PreflightIssue{
				Check:       "conflict_darwin",
				Severity:    "warning",
				Description: "an existing glassbox:// app bundle references a different binary",
				Hint:        "Run 'glassbox protocol:repair' to overwrite the app bundle with the current executable.",
			})
		}

	case "windows":
		_, err := runCommand("reg", "query", windowsRegistryKey, "/ve")
		if err != nil {
			report.Checks = append(report.Checks, "no existing Windows registration (no conflict)")
			return
		}
		cmdOutput, cmdErr := runCommand("reg", "query", windowsRegistryKey+`\shell\open\command`, "/ve")
		if cmdErr == nil && strings.Contains(cmdOutput, r.executablePath) {
			report.Checks = append(report.Checks, "existing Windows registration points to current binary (no conflict)")
		} else {
			report.Issues = append(report.Issues, PreflightIssue{
				Check:       "conflict_windows",
				Severity:    "warning",
				Description: "an existing glassbox:// registration exists but may point to a different binary",
				Hint:        "Run 'glassbox protocol:repair' to reclaim the registration.",
			})
		}
	}
}

// PreflightIssue describes a single environment problem found during preflight.
type PreflightIssue struct {
	// Check is a short label for the check that failed.
	Check string
	// Severity is "error" (blocks registration) or "warning" (potential conflict).
	Severity string
	// Description explains what is wrong.
	Description string
	// Hint is an actionable remediation step.
	Hint string
}

// PreflightReport is the result of PreflightCheck.
type PreflightReport struct {
	// Platform is the runtime.GOOS value.
	Platform string
	// Scheme is the URL scheme being registered.
	Scheme string
	// OK is true when no error-severity issues were found.
	OK bool
	// Issues lists every problem found (errors and warnings).
	Issues []PreflightIssue
	// Checks lists individual checks that passed.
	Checks []string
}

// Summary returns a human-readable description of all issues in the report.
func (r *PreflightReport) Summary() string {
	if len(r.Issues) == 0 {
		return ""
	}
	var sb strings.Builder
	for i, issue := range r.Issues {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(fmt.Sprintf("[%s] %s: %s", issue.Severity, issue.Check, issue.Description))
		if issue.Hint != "" {
			sb.WriteString("\n  Hint: ")
			sb.WriteString(issue.Hint)
		}
	}
	return sb.String()
}

// ensureDirectory checks if a directory exists and creates it if needed.
func ensureDirectory(dir string) error {
	info, err := os.Stat(dir)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("%q exists but is not a directory", dir)
		}
		return nil
	}
	// Try to create the directory.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("cannot create directory %s: %w", dir, err)
	}
	return nil
}

// hasPreflightErrors returns true if any issue has severity "error".
func hasPreflightErrors(issues []PreflightIssue) bool {
	for _, i := range issues {
		if i.Severity == "error" {
			return true
		}
	}
	return false
}
