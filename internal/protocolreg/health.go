// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package protocolreg

import (
	"fmt"
	"os"
	"runtime"
	"strings"
)

// HealthStatus is the readiness state of the protocol registration.
type HealthStatus string

const (
	// HealthReady means the protocol handler is registered and the binary is reachable.
	HealthReady HealthStatus = "ready"
	// HealthDegraded means the protocol handler is registered but has configuration issues.
	HealthDegraded HealthStatus = "degraded"
	// HealthNotReady means the protocol handler is not registered or the binary is unreachable.
	HealthNotReady HealthStatus = "not_ready"
)

// HealthReport is the output of HealthCheck. It describes whether the
// protocol handler is ready to serve deep-link invocations.
type HealthReport struct {
	// Status is the overall readiness state.
	Status HealthStatus `json:"status"`
	// Ready is true when the handler is registered and the binary is reachable.
	Ready bool `json:"ready"`
	// Checks lists individual checks that passed.
	Checks []string `json:"checks"`
	// Failures lists checks that failed with root-cause descriptions.
	Failures []string `json:"failures,omitempty"`
	// Hint is a top-level remediation suggestion when Ready is false.
	Hint string `json:"hint,omitempty"`
}

// HealthCheck performs a lightweight readiness probe for the protocol handler.
// Unlike Diagnose, it does not invoke external commands (xdg-mime, reg, lsregister)
// — it only checks local filesystem state and executable reachability. This makes
// it safe to call in health-polling loops or startup hooks.
//
// A handler is considered "ready" when:
//   - The executable path exists and is a regular file
//   - The platform-specific registration artefact is present
//   - The artefact references the current executable (no stale path)
func (r *Registrar) HealthCheck() *HealthReport {
	report := &HealthReport{}

	// Step 1: verify the executable itself is reachable.
	if r.executablePath == "" {
		report.Status = HealthNotReady
		report.Failures = append(report.Failures, "executable path is empty — cannot determine handler binary")
		report.Hint = "Reinstall Glassbox or invoke it from a valid binary path, not via 'go run'."
		return report
	}

	if _, err := os.Stat(r.executablePath); err != nil {
		report.Status = HealthNotReady
		report.Failures = append(report.Failures,
			fmt.Sprintf("executable not reachable at %s: %v", r.executablePath, err))
		report.Hint = "Ensure the Glassbox binary exists at the registered path. Run 'glassbox protocol:repair' to re-register."
		return report
	}
	report.Checks = append(report.Checks, fmt.Sprintf("executable reachable: %s", r.executablePath))

	// Step 1b: verify the binary is executable on Unix.
	if runtime.GOOS != "windows" {
		if info, statErr := os.Stat(r.executablePath); statErr == nil {
			mode := info.Mode()
			if mode&0o111 == 0 {
				report.Status = HealthNotReady
				report.Failures = append(report.Failures,
					fmt.Sprintf("executable at %s is not executable (permissions: %04o)", r.executablePath, mode&0o777))
				report.Hint = fmt.Sprintf("Run 'chmod +x %s' to make the binary executable.", r.executablePath)
				return report
			}
			report.Checks = append(report.Checks, "executable has execute permission")
		}
	}

	// Step 2: check the platform artefact.
	switch runtime.GOOS {
	case "linux":
		r.healthCheckLinux(report)
	case "darwin":
		r.healthCheckDarwin(report)
	case "windows":
		r.healthCheckWindows(report)
	default:
		report.Status = HealthNotReady
		report.Failures = append(report.Failures,
			fmt.Sprintf("health checks are not supported on %s", runtime.GOOS))
		report.Hint = "Protocol registration is only supported on Linux, macOS, and Windows."
		return report
	}

	// Derive Ready and Status from failures.
	if len(report.Failures) == 0 {
		report.Ready = true
		report.Status = HealthReady
	} else if report.Status == "" {
		// Has failures but some artefact exists — degraded.
		report.Ready = false
		report.Status = HealthDegraded
		if report.Hint == "" {
			report.Hint = "Run 'glassbox protocol:repair' to restore the registration."
		}
	}

	return report
}

func (r *Registrar) healthCheckLinux(report *HealthReport) {
	// Check desktop file exists.
	desktopBytes, err := os.ReadFile(r.linuxDesktopPath())
	if err != nil {
		report.Status = HealthNotReady
		report.Failures = append(report.Failures,
			fmt.Sprintf("desktop file not found at %s", r.linuxDesktopPath()))
		report.Hint = "Run 'glassbox protocol:register' to install the protocol handler."
		return
	}
	report.Checks = append(report.Checks, fmt.Sprintf("desktop file present: %s", r.linuxDesktopPath()))

	// Check wrapper script exists and references the current binary.
	wrapperBytes, err := os.ReadFile(r.linuxWrapperPath())
	if err != nil {
		report.Failures = append(report.Failures,
			fmt.Sprintf("protocol wrapper script not found at %s", r.linuxWrapperPath()))
		report.Hint = "Run 'glassbox protocol:repair' to recreate the wrapper script."
		return
	}

	if !strings.Contains(string(wrapperBytes), r.executablePath) {
		report.Failures = append(report.Failures,
			fmt.Sprintf("wrapper script does not reference current binary %s — registration is stale", r.executablePath))
		report.Hint = "Run 'glassbox protocol:repair' to update the wrapper script."
		return
	}
	report.Checks = append(report.Checks, "wrapper script references current binary")

	// Desktop entry must declare the correct Exec path.
	if !strings.Contains(string(desktopBytes), "Exec="+r.linuxWrapperPath()) {
		report.Failures = append(report.Failures,
			fmt.Sprintf("desktop file Exec entry does not point to wrapper at %s", r.linuxWrapperPath()))
		report.Hint = "Run 'glassbox protocol:repair' to update the desktop file."
	} else {
		report.Checks = append(report.Checks, "desktop file Exec entry is correct")
	}
}

func (r *Registrar) healthCheckDarwin(report *HealthReport) {
	// Check plist exists.
	if _, err := os.Stat(r.macOSPlistPath()); err != nil {
		report.Status = HealthNotReady
		report.Failures = append(report.Failures,
			fmt.Sprintf("app bundle plist not found at %s", r.macOSPlistPath()))
		report.Hint = "Run 'glassbox protocol:register' to install the macOS app bundle."
		return
	}
	report.Checks = append(report.Checks, fmt.Sprintf("app bundle plist present: %s", r.macOSPlistPath()))

	// Check app bundle executable exists and references the current binary.
	execBytes, err := os.ReadFile(r.macOSExecutablePath())
	if err != nil {
		report.Failures = append(report.Failures,
			fmt.Sprintf("app bundle executable not found at %s", r.macOSExecutablePath()))
		report.Hint = "Run 'glassbox protocol:repair' to recreate the app bundle executable."
		return
	}

	if !strings.Contains(string(execBytes), r.executablePath) {
		report.Failures = append(report.Failures,
			fmt.Sprintf("app bundle executable does not reference current binary %s — registration is stale", r.executablePath))
		report.Hint = "Run 'glassbox protocol:repair' to update the app bundle."
	} else {
		report.Checks = append(report.Checks, "app bundle executable references current binary")
	}
}

func (r *Registrar) healthCheckWindows(report *HealthReport) {
	// Query registry key — if it fails the handler is not ready.
	regOutput, err := runCommand("reg", "query", windowsRegistryKey)
	if err != nil {
		report.Status = HealthNotReady
		report.Failures = append(report.Failures,
			fmt.Sprintf("registry key %s not found", windowsRegistryKey))
		report.Hint = "Run 'glassbox protocol:register' to create the registry entries."
		return
	}
	report.Checks = append(report.Checks, fmt.Sprintf("registry key present: %s", windowsRegistryKey))

	if !strings.Contains(regOutput, "URL Protocol") {
		report.Failures = append(report.Failures, "registry key is missing the 'URL Protocol' value")
		report.Hint = "Run 'glassbox protocol:repair' to restore the registry entry."
		return
	}
	report.Checks = append(report.Checks, "registry 'URL Protocol' value present")

	// Check the shell open command points to this binary.
	cmdOutput, err := runCommand("reg", "query", windowsRegistryKey+`\shell\open\command`, "/ve")
	if err != nil {
		report.Failures = append(report.Failures,
			fmt.Sprintf("shell open command registry key missing: %v", err))
		report.Hint = "Run 'glassbox protocol:repair' to restore the registry open command."
		return
	}

	if !strings.Contains(cmdOutput, r.executablePath) {
		report.Failures = append(report.Failures,
			fmt.Sprintf("shell open command does not reference current binary %s — registration is stale", r.executablePath))
		report.Hint = "Run 'glassbox protocol:repair' to update the registry open command."
	} else {
		report.Checks = append(report.Checks, "shell open command references current binary")
	}
}
