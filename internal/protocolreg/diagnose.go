// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package protocolreg

import (
	"fmt"
	"os"
	"runtime"
	"strings"
)

// DiagnosticStatus represents the overall health of the protocol registration.
type DiagnosticStatus string

const (
	// StatusOK means the protocol handler is correctly registered and functional.
	StatusOK DiagnosticStatus = "ok"
	// StatusDegraded means registration exists but has issues (e.g. stale path).
	StatusDegraded DiagnosticStatus = "degraded"
	// StatusNotRegistered means no registration was found.
	StatusNotRegistered DiagnosticStatus = "not_registered"
	// StatusError means the diagnostic check itself encountered an error.
	StatusError DiagnosticStatus = "error"
)

// DiagnosticReport is the structured output of Diagnose. It contains the
// overall status, per-check results, root-cause descriptions, and ordered
// remediation steps.
type DiagnosticReport struct {
	// Platform is the runtime.GOOS value.
	Platform string `json:"platform"`
	// Scheme is the URL scheme being diagnosed (always "glassbox").
	Scheme string `json:"scheme"`
	// Status is the overall health assessment.
	Status DiagnosticStatus `json:"status"`
	// Checks lists individual checks that passed.
	Checks []string `json:"checks"`
	// Issues lists individual checks that failed, with root-cause descriptions.
	Issues []string `json:"issues"`
	// RemediationSteps contains ordered, actionable instructions to fix issues.
	RemediationSteps []string `json:"remediation_steps,omitempty"`
	// ExecutablePath is the path of the currently running binary.
	ExecutablePath string `json:"executable_path"`
	// RegisteredHandler is the handler path found in the OS registration, if any.
	RegisteredHandler string `json:"registered_handler,omitempty"`
	// HandlerMatchesSelf reports whether the registered handler points to the
	// current executable.
	HandlerMatchesSelf bool `json:"handler_matches_self"`
	// ConflictDetected is true when a registration exists for the glassbox://
	// scheme but it was written by a different application or a previous binary
	// path that no longer matches the current executable. This is a stronger
	// signal than a stale path because it indicates an external process owns
	// the registration.
	ConflictDetected bool `json:"conflict_detected,omitempty"`
	// ConflictingHandler is the handler path that currently owns the
	// registration when ConflictDetected is true.
	ConflictingHandler string `json:"conflicting_handler,omitempty"`
	// TraceContext carries the W3C trace identifiers for the active diagnostic
	// span, when the diagnostic was triggered by a deep-link invocation that
	// included trace-context parameters. Populated by the caller via
	// DiagnosticReport.WithTraceContext; nil when no context is available.
	TraceContext *URITraceContext `json:"trace_context,omitempty"`
}

// RepairResult describes the outcome of a Repair attempt.
type RepairResult struct {
	// Repaired is true when the repair completed without errors.
	Repaired bool
	// Actions lists the repair steps that were performed.
	Actions []string
	// Err holds the first error encountered during repair, if any.
	Err error
	// PermissionHint is set when the repair failed due to insufficient
	// privileges, with a platform-specific hint for the user.
	PermissionHint string
}

// WithTraceContext attaches W3C trace-context identifiers to the report so
// that downstream telemetry can correlate this diagnostic run with its
// originating distributed trace. Returns the report for chaining.
func (r *DiagnosticReport) WithTraceContext(tc *URITraceContext) *DiagnosticReport {
	r.TraceContext = tc
	return r
}

// Diagnose inspects the current protocol registration state and returns a
// structured DiagnosticReport. It never modifies system state.
func (r *Registrar) Diagnose() *DiagnosticReport {
	report := &DiagnosticReport{
		Platform:       runtime.GOOS,
		Scheme:         Scheme,
		ExecutablePath: r.executablePath,
	}

	switch runtime.GOOS {
	case "windows":
		r.diagnoseWindows(report)
	case "darwin":
		r.diagnoseDarwin(report)
	case "linux":
		r.diagnoseLinux(report)
	default:
		report.Status = StatusError
		report.Issues = append(report.Issues,
			fmt.Sprintf("protocol diagnostics are not supported on %s", runtime.GOOS))
		report.RemediationSteps = []string{
			"Protocol registration is only supported on Windows, macOS, and Linux.",
		}
		return report
	}

	// Determine overall status
	if len(report.Issues) == 0 {
		report.Status = StatusOK
	} else if report.RegisteredHandler != "" {
		// Some registration exists but it has problems
		report.Status = StatusDegraded
	} else {
		report.Status = StatusNotRegistered
	}

	// Append remediation steps if there are issues
	if len(report.Issues) > 0 && len(report.RemediationSteps) == 0 {
		report.RemediationSteps = r.defaultRemediationSteps()
	}

	return report
}

// Repair attempts to fix a broken or missing protocol registration. It calls
// Register() and records each action taken. Permission failures are caught and
// surfaced as PermissionHint rather than hard errors.
func (r *Registrar) Repair() *RepairResult {
	result := &RepairResult{}

	// Guard against a misconfigured Registrar — an empty executable path means
	// any registration we write will point nowhere and immediately conflict again.
	if r.executablePath == "" {
		result.Err = fmt.Errorf(
			"cannot repair: executable path is empty\n" +
				"  Fix: ensure glassbox is invoked from a valid binary path, not via 'go run'",
		)
		return result
	}

	// Run diagnostics first to understand what needs fixing.
	diag := r.Diagnose()
	if diag.Status == StatusOK {
		result.Repaired = true
		result.Actions = append(result.Actions, "No repair needed: protocol handler is correctly registered.")
		return result
	}

	if diag.Status == StatusError {
		result.Err = fmt.Errorf("cannot repair: %s", strings.Join(diag.Issues, "; "))
		return result
	}

	// When a conflicting handler was detected, record which binary is being displaced.
	if diag.ConflictDetected {
		result.Actions = append(result.Actions,
			fmt.Sprintf("Conflicting registration detected (handler: %s); overwriting with current executable.",
				diag.ConflictingHandler))
	}

	// Attempt re-registration.
	result.Actions = append(result.Actions,
		fmt.Sprintf("Attempting to register %s:// protocol handler on %s", Scheme, runtime.GOOS))

	if err := r.Register(); err != nil {
		result.Err = err
		result.PermissionHint = r.permissionHint(err)
		result.Actions = append(result.Actions,
			fmt.Sprintf("Registration failed: %v", err))
		return result
	}

	result.Actions = append(result.Actions,
		fmt.Sprintf("Registered %s:// protocol handler successfully.", Scheme))

	// Verify the repair worked.
	postDiag := r.Diagnose()
	if postDiag.Status == StatusOK {
		result.Repaired = true
		result.Actions = append(result.Actions, "Post-repair verification passed.")
	} else {
		result.Err = fmt.Errorf("registration succeeded but verification still reports issues: %s",
			strings.Join(postDiag.Issues, "; "))
		result.Actions = append(result.Actions,
			fmt.Sprintf("Post-repair verification failed: %s", strings.Join(postDiag.Issues, "; ")))
		result.PermissionHint = r.permissionHint(result.Err)
	}

	return result
}

// ---- platform-specific diagnostic implementations ---------------------------

func (r *Registrar) diagnoseWindows(report *DiagnosticReport) {
	// Check registry key existence
	regOutput, err := runCommand("reg", "query", windowsRegistryKey)
	if err != nil {
		report.Issues = append(report.Issues,
			fmt.Sprintf("Registry key %s not found: %v", windowsRegistryKey, err))
		report.RemediationSteps = append(report.RemediationSteps,
			fmt.Sprintf("Run 'glassbox protocol:repair' or manually add the registry key %s", windowsRegistryKey),
			"Note: registry writes may require Administrator privileges.",
		)
		return
	}
	report.Checks = append(report.Checks, fmt.Sprintf("Registry key %s exists", windowsRegistryKey))

	// Check URL Protocol value
	if !strings.Contains(regOutput, "URL Protocol") {
		report.Issues = append(report.Issues, "Registry key is missing the 'URL Protocol' value")
	} else {
		report.Checks = append(report.Checks, "Registry 'URL Protocol' value is present")
	}

	// Check shell open command
	cmdOutput, err := runCommand("reg", "query", windowsRegistryKey+`\shell\open\command`, "/ve")
	if err != nil {
		report.Issues = append(report.Issues,
			fmt.Sprintf("Missing shell\\open\\command registry key: %v", err))
		return
	}

	expectedCmd := r.windowsOpenCommand()
	if strings.Contains(cmdOutput, r.executablePath) {
		report.RegisteredHandler = r.executablePath
		report.HandlerMatchesSelf = true
		report.Checks = append(report.Checks,
			fmt.Sprintf("Shell open command points to current executable: %s", r.executablePath))
	} else {
		// Extract the registered path for the report
		registeredPath := strings.TrimSpace(cmdOutput)
		report.RegisteredHandler = registeredPath
		report.HandlerMatchesSelf = false

		// Determine whether this is a foreign-binary conflict or a stale self-reference.
		// A conflict is when a path is registered that does not reference this binary at all
		// (e.g. a different application claimed the scheme).
		isConflict := registeredPath != "" &&
			!strings.Contains(strings.ToLower(registeredPath), "glassbox") &&
			!strings.Contains(strings.ToLower(registeredPath), strings.ToLower(Scheme))

		if isConflict {
			report.ConflictDetected = true
			report.ConflictingHandler = registeredPath
			report.Issues = append(report.Issues,
				fmt.Sprintf(
					"Protocol conflict: the glassbox:// scheme is claimed by a different application.\n"+
						"  Conflicting handler: %s\n"+
						"  Expected handler:    %s\n"+
						"  Another program has registered itself as the glassbox:// handler. "+
						"Run 'glassbox protocol:repair' to reclaim the registration.",
					registeredPath, expectedCmd))
			report.RemediationSteps = append(report.RemediationSteps,
				"Run 'glassbox protocol:repair' to overwrite the conflicting registration.",
				"If the conflicting application is still needed, uninstall or reconfigure it first.",
				fmt.Sprintf("To manually fix: reg add %s\\shell\\open\\command /ve /d %q /f",
					windowsRegistryKey, expectedCmd),
			)
		} else {
			report.Issues = append(report.Issues,
				fmt.Sprintf("Shell open command does not match current executable.\n  Expected: %s\n  Found:    %s\n  This is a stale path — run 'glassbox protocol:repair' to update it.",
					expectedCmd, registeredPath))
			report.RemediationSteps = append(report.RemediationSteps,
				"Run 'glassbox protocol:repair' to update the registry to point to the current executable.",
			)
		}
	}

	// Check for registry conflicts (key exists but doesn't look like a glassbox registration)
	if !strings.Contains(strings.ToLower(regOutput), "url:glassbox") &&
		!strings.Contains(strings.ToLower(regOutput), "url protocol") {
		report.Issues = append(report.Issues,
			"Registry key exists but does not appear to be a valid glassbox:// handler")
	}
}

func (r *Registrar) diagnoseDarwin(report *DiagnosticReport) {
	// Check app bundle plist
	plistBytes, err := os.ReadFile(r.macOSPlistPath())
	if err != nil {
		report.Issues = append(report.Issues,
			fmt.Sprintf("App bundle plist not found at %s: %v", r.macOSPlistPath(), err))
		report.RemediationSteps = append(report.RemediationSteps,
			"Run 'glassbox protocol:repair' to create the app bundle and register the scheme.",
		)
		return
	}
	plistContent := string(plistBytes)
	report.Checks = append(report.Checks, fmt.Sprintf("App bundle plist found: %s", r.macOSPlistPath()))

	// Check URL scheme declaration in plist
	if !strings.Contains(plistContent, "<key>CFBundleURLSchemes</key>") ||
		!strings.Contains(plistContent, "<string>"+Scheme+"</string>") {
		report.Issues = append(report.Issues,
			"Info.plist does not declare the glassbox:// URL scheme")
	} else {
		report.Checks = append(report.Checks, "Info.plist declares the glassbox:// URL scheme")
	}

	// Check app bundle executable
	execBytes, err := os.ReadFile(r.macOSExecutablePath())
	if err != nil {
		report.Issues = append(report.Issues,
			fmt.Sprintf("App bundle executable not found at %s: %v", r.macOSExecutablePath(), err))
	} else {
		execContent := string(execBytes)
		if strings.Contains(execContent, r.executablePath) {
			report.RegisteredHandler = r.macOSExecutablePath()
			report.HandlerMatchesSelf = true
			report.Checks = append(report.Checks,
				fmt.Sprintf("App bundle executable launches current binary: %s", r.executablePath))
		} else {
			report.RegisteredHandler = r.macOSExecutablePath()
			report.HandlerMatchesSelf = false

			// Detect whether this is a foreign-binary conflict.
			conflictingPath := extractExecPath(execContent)
			if conflictingPath != "" && !strings.Contains(strings.ToLower(conflictingPath), "glassbox") {
				report.ConflictDetected = true
				report.ConflictingHandler = conflictingPath
				report.Issues = append(report.Issues,
					fmt.Sprintf(
						"Protocol conflict: app bundle references a foreign binary.\n"+
							"  Found:    %s\n"+
							"  Expected: %s\n"+
							"  Another application has registered the glassbox:// scheme.",
						conflictingPath, r.executablePath))
				report.RemediationSteps = append(report.RemediationSteps,
					"Run 'glassbox protocol:repair' to overwrite the conflicting registration.",
				)
			} else {
				report.Issues = append(report.Issues,
					fmt.Sprintf(
						"App bundle executable does not reference current binary.\n"+
							"  Found:    %s\n"+
							"  Expected: %s\n"+
							"  This is a stale path — run 'glassbox protocol:repair' to update it.",
						conflictingPath, r.executablePath))
				report.RemediationSteps = append(report.RemediationSteps,
					"Run 'glassbox protocol:repair' to update the app bundle to the current executable path.",
				)
			}
		}
	}

	// Check LaunchServices registration
	lsOutput, err := runCommand(macOSLSRegisterPath(), "-dump")
	if err != nil {
		report.Issues = append(report.Issues,
			fmt.Sprintf("Failed to query LaunchServices: %v", err))
		return
	}

	if strings.Contains(lsOutput, r.macOSAppPath()) {
		report.Checks = append(report.Checks,
			fmt.Sprintf("LaunchServices has registered: %s", r.macOSAppPath()))
	} else {
		report.Issues = append(report.Issues,
			fmt.Sprintf("LaunchServices does not contain the app bundle path %s", r.macOSAppPath()))
		report.RemediationSteps = append(report.RemediationSteps,
			"Run 'glassbox protocol:repair' to re-register with LaunchServices.",
			fmt.Sprintf("Or manually run: %s -f %s", macOSLSRegisterPath(), r.macOSAppPath()),
		)
	}

	if !strings.Contains(lsOutput, Scheme) {
		report.Issues = append(report.Issues,
			"LaunchServices does not list the glassbox:// scheme")
	} else {
		report.Checks = append(report.Checks, "LaunchServices lists the glassbox:// scheme")
	}
}

func (r *Registrar) diagnoseLinux(report *DiagnosticReport) {
	// Check .desktop file
	desktopBytes, err := os.ReadFile(r.linuxDesktopPath())
	if err != nil {
		report.Issues = append(report.Issues,
			fmt.Sprintf("Desktop file not found at %s: %v", r.linuxDesktopPath(), err))
		report.RemediationSteps = append(report.RemediationSteps,
			"Run 'glassbox protocol:repair' to create the .desktop file and register the MIME handler.",
			fmt.Sprintf("Or manually create %s with MimeType=x-scheme-handler/glassbox", r.linuxDesktopPath()),
			"Then run: xdg-mime default glassbox-protocol.desktop x-scheme-handler/glassbox",
		)
		return
	}
	desktopContent := string(desktopBytes)
	report.Checks = append(report.Checks, fmt.Sprintf("Desktop file found: %s", r.linuxDesktopPath()))

	// Check MIME type declaration
	if !strings.Contains(desktopContent, "MimeType="+linuxMimeType+";") {
		report.Issues = append(report.Issues,
			fmt.Sprintf("Desktop file is missing MimeType=%s", linuxMimeType))
	} else {
		report.Checks = append(report.Checks, "Desktop file declares the correct MIME type")
	}

	// Check Exec entry
	expectedExec := "Exec=" + r.linuxWrapperPath() + " %u"
	if strings.Contains(desktopContent, expectedExec) {
		report.RegisteredHandler = r.linuxWrapperPath()
		report.Checks = append(report.Checks,
			fmt.Sprintf("Desktop file Exec entry points to: %s", r.linuxWrapperPath()))
	} else {
		report.Issues = append(report.Issues,
			fmt.Sprintf("Desktop file Exec entry does not match expected path.\n  Expected: %s", expectedExec))
	}

	// Check wrapper script
	wrapperBytes, err := os.ReadFile(r.linuxWrapperPath())
	if err != nil {
		report.Issues = append(report.Issues,
			fmt.Sprintf("Protocol helper script not found at %s: %v", r.linuxWrapperPath(), err))
	} else {
		wrapperContent := string(wrapperBytes)
		if strings.Contains(wrapperContent, r.executablePath) {
			report.HandlerMatchesSelf = true
			report.Checks = append(report.Checks,
				fmt.Sprintf("Protocol helper script launches current binary: %s", r.executablePath))
		} else {
			report.HandlerMatchesSelf = false

			// Try to extract the conflicting path from the wrapper for diagnosis.
			conflictingPath := extractExecPath(wrapperContent)
			if conflictingPath != "" && !strings.Contains(strings.ToLower(conflictingPath), "glassbox") {
				report.ConflictDetected = true
				report.ConflictingHandler = conflictingPath
				report.Issues = append(report.Issues,
					fmt.Sprintf(
						"Protocol conflict: wrapper script references a foreign binary.\n"+
							"  Found:    %s\n"+
							"  Expected: %s\n"+
							"  A different application appears to have registered itself as the glassbox:// handler.",
						conflictingPath, r.executablePath))
				report.RemediationSteps = append(report.RemediationSteps,
					"Run 'glassbox protocol:repair' to overwrite the conflicting registration.",
				)
			} else {
				report.Issues = append(report.Issues,
					fmt.Sprintf(
						"Protocol helper script does not reference current binary.\n"+
							"  Found:    %s\n"+
							"  Expected: %s\n"+
							"  This is a stale path — run 'glassbox protocol:repair' to update it.",
						conflictingPath, r.executablePath))
				report.RemediationSteps = append(report.RemediationSteps,
					"Run 'glassbox protocol:repair' to update the helper script to the current executable path.",
				)
			}
		}
	}

	// Check xdg-mime registration
	defaultDesktop, err := runCommand("xdg-mime", "query", "default", linuxMimeType)
	if err != nil {
		report.Issues = append(report.Issues,
			fmt.Sprintf("xdg-mime query failed: %v", err))
		report.RemediationSteps = append(report.RemediationSteps,
			"Ensure xdg-utils is installed: sudo apt install xdg-utils (Debian/Ubuntu) or equivalent.",
		)
		return
	}

	trimmed := strings.TrimSpace(defaultDesktop)
	if trimmed == linuxDesktopFile {
		report.Checks = append(report.Checks,
			fmt.Sprintf("xdg-mime resolves %s to %s", linuxMimeType, linuxDesktopFile))
	} else if trimmed == "" {
		report.Issues = append(report.Issues,
			fmt.Sprintf("xdg-mime has no handler registered for %s", linuxMimeType))
		report.RemediationSteps = append(report.RemediationSteps,
			fmt.Sprintf("Run: xdg-mime default %s %s", linuxDesktopFile, linuxMimeType),
		)
	} else {
		report.Issues = append(report.Issues,
			fmt.Sprintf("xdg-mime reports %q as handler instead of %q", trimmed, linuxDesktopFile))
		report.RemediationSteps = append(report.RemediationSteps,
			fmt.Sprintf("Run: xdg-mime default %s %s", linuxDesktopFile, linuxMimeType),
		)
	}
}

// ---- helpers ----------------------------------------------------------------

// defaultRemediationSteps returns generic repair instructions for the current OS.
func (r *Registrar) defaultRemediationSteps() []string {
	switch runtime.GOOS {
	case "windows":
		return []string{
			"Run 'glassbox protocol:repair' to re-register the protocol handler.",
			"If repair fails, run the command as Administrator.",
			fmt.Sprintf("Or manually add the registry key: %s", windowsRegistryKey),
		}
	case "darwin":
		return []string{
			"Run 'glassbox protocol:repair' to rebuild the app bundle and re-register.",
			fmt.Sprintf("Or manually run: %s -f ~/Applications/%s", macOSLSRegisterPath(), macOSAppName),
		}
	default: // linux
		return []string{
			"Run 'glassbox protocol:repair' to recreate the .desktop file and re-register.",
			fmt.Sprintf("Or manually create %s and run: xdg-mime default %s %s",
				r.linuxDesktopPath(), linuxDesktopFile, linuxMimeType),
		}
	}
}

// permissionHint returns a platform-specific hint when a repair fails due to
// permission errors.
func (r *Registrar) permissionHint(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	isPermission := strings.Contains(msg, "permission denied") ||
		strings.Contains(msg, "access is denied") ||
		strings.Contains(msg, "operation not permitted")

	if !isPermission {
		return ""
	}

	switch runtime.GOOS {
	case "windows":
		return "Registry writes require Administrator privileges. " +
			"Right-click the terminal and choose 'Run as administrator', then retry."
	case "darwin":
		return "Writing to ~/Applications requires write permission to your home directory. " +
			"Check that ~/Applications exists and is writable."
	default:
		return "Writing to ~/.local/share/applications requires write permission. " +
			"Ensure your home directory is writable and try again."
	}
}

// extractExecPath attempts to parse the executable path from a shell handler
// script content (e.g. "#!/bin/sh\nexec /path/to/binary protocol-handler \"$1\"\n").
// Returns an empty string when no path can be extracted.
func extractExecPath(scriptContent string) string {
	for _, line := range strings.Split(scriptContent, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "exec ") {
			continue
		}
		// Strip the "exec " prefix and take the first token (the binary path).
		rest := strings.TrimPrefix(line, "exec ")
		// Handle single-quoted paths produced by shellQuote().
		if strings.HasPrefix(rest, "'") {
			end := strings.Index(rest[1:], "'")
			if end >= 0 {
				return rest[1 : end+1]
			}
		}
		// Handle plain unquoted paths.
		fields := strings.Fields(rest)
		if len(fields) > 0 {
			return fields[0]
		}
	}
	return ""
}
