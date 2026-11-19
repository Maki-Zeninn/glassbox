// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package protocolreg

import (
	"errors"
	"fmt"
	ersterrors "github.com/dotandev/glassbox/internal/errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	Scheme             = "glassbox"
	windowsRegistryKey = `HKEY_CURRENT_USER\Software\Classes\Glassbox`
	linuxDesktopFile   = "glassbox-protocol.desktop"
	linuxMimeType      = "x-scheme-handler/Glassbox"
	macOSAppName       = "GLASSBOX Protocol.app"
	macOSBundleID      = "dev.dotan.Glassbox.protocol"
	macOSExecutable    = "glassbox-protocol-handler"

	// maxPathLength is the maximum acceptable path length for registration artefacts.
	// Windows MAX_PATH is 260, but we use a more conservative limit for cross-platform safety.
	maxPathLength = 255
)

type Registrar struct {
	executablePath string
	homeDir        string
}

type VerificationReport struct {
	Platform string
	Scheme   string
	Checks   []string
	Issues   []string
	// ElapsedMs is how long the verification took in milliseconds.
	ElapsedMs int64
	// TraceContext carries the W3C trace identifiers for the active registration
	// verification span. When set, the Glassbox backend can correlate this
	// verification result with an originating distributed trace for accurate
	// session attribution.
	TraceContext *URITraceContext
}

// validatePathLength checks that a path is within acceptable length limits.
// Returns an error with actionable guidance if the path is too long.
func validatePathLength(path string, context string) error {
	if len(path) > maxPathLength {
		return fmt.Errorf(
			"%s path is too long (%d characters, maximum %d)\n"+
				"  Fix: move the binary to a shorter path (e.g., ~/.local/bin/glassbox) or use a symlink",
			context, len(path), maxPathLength,
		)
	}
	return nil
}

// normalizePath cleans and validates a filesystem path, rejecting dangerous patterns.
// It removes redundant separators, resolves . and .. components, and checks for
// suspicious patterns like consecutive dots or path traversal attempts.
func normalizePath(path string, context string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("%s path is empty", context)
	}

	// Check for null bytes before any processing.
	if strings.ContainsRune(path, 0) {
		return "", fmt.Errorf("%s contains null bytes and cannot be trusted", context)
	}

	// Clean the path to resolve . and .. components and remove redundant separators.
	cleaned := filepath.Clean(path)

	// Check for path traversal patterns (e.g., ".." sequences that weren't resolved).
	// This catches cases like "/usr/local/bin/../../etc/passwd" which Clean() normalizes
	// but we want to flag as suspicious.
	if strings.Contains(cleaned, "..") {
		return "", fmt.Errorf(
			"%s contains suspicious path traversal pattern\n"+
				"  Fix: use a direct path without '..' components",
			context,
		)
	}

	// Check for consecutive dots which may indicate an attempt to hide files.
	if strings.Contains(cleaned, "...") {
		return "", fmt.Errorf(
			"%s contains consecutive dots which is not allowed\n"+
				"  Fix: use a standard path without consecutive dots",
			context,
		)
	}

	// Validate the cleaned path length.
	if err := validatePathLength(cleaned, context); err != nil {
		return "", err
	}

	return cleaned, nil
}

func NewRegistrar() (*Registrar, error) {
	executablePath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve executable path: %w", err)
	}

	// Normalize and validate the executable path.
	executablePath, err = normalizePath(executablePath, "executable path")
	if err != nil {
		return nil, err
	}

	// Validate that the path is not a system-critical directory (e.g., root, /usr, /etc)
	// which would indicate a misconfigured installation.
	if executablePath == "/" || executablePath == "\\" {
		return nil, fmt.Errorf(
			"executable path %q resolves to a system root directory — this is not a valid binary location\n"+
				"  Fix: ensure glassbox is installed in a proper bin directory (e.g., ~/.local/bin or /usr/local/bin)",
			executablePath,
		)
	}

	// Validate that the path is not a system-critical directory (e.g., root, /usr, /etc)
	// which would indicate a misconfigured installation.
	cleanPath := filepath.Clean(executablePath)
	if cleanPath == "/" || cleanPath == "\\" {
		return nil, fmt.Errorf(
			"executable path %q resolves to a system root directory — this is not a valid binary location\n"+
				"  Fix: ensure glassbox is installed in a proper bin directory (e.g., ~/.local/bin or /usr/local/bin)",
			executablePath,
		)
	}

	if _, err := os.Stat(executablePath); err != nil {
		return nil, fmt.Errorf(
			"executable not found at %s: %w\n"+
				"  Fix: ensure the glassbox binary is installed correctly and the path is not a broken symlink",
			executablePath, err,
		)
	}

	// Verify the file is executable (important on Unix systems where a file can exist
	// but lack execute permissions).
	if runtime.GOOS != "windows" {
		if info, statErr := os.Stat(executablePath); statErr == nil {
			mode := info.Mode()
			if mode&0o111 == 0 {
				return nil, fmt.Errorf(
					"executable at %s is not executable (permissions: %04o)\n"+
						"  Fix: run 'chmod +x %s' to make the binary executable",
					executablePath, mode&0o777, executablePath,
				)
			}
		}
	}

	// Resolve symlinks so the registered handler always points to the real binary,
	// not a link that could be replaced silently.
	resolved, err := filepath.EvalSymlinks(executablePath)
	if err != nil {
		return nil, fmt.Errorf(
			"resolve symlink for executable %s: %w\n"+
				"  Fix: ensure the binary is not a dangling symlink",
			executablePath, err,
		)
	}

	// Normalize the resolved path to ensure it's still safe after symlink resolution.
	resolved, err = normalizePath(resolved, "resolved executable path")
	if err != nil {
		return nil, fmt.Errorf("resolved path validation failed: %w", err)
	}
	executablePath = resolved

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve user home directory: %w", err)
	}

	// Normalize and validate home directory.
	homeDir, err = normalizePath(homeDir, "home directory")
	if err != nil {
		return nil, err
	}

	// Validate home directory is actually a directory.
	homeInfo, homeErr := os.Stat(homeDir)
	if homeErr != nil {
		return nil, fmt.Errorf("home directory %s is not accessible: %w", homeDir, homeErr)
	}
	if !homeInfo.IsDir() {
		return nil, fmt.Errorf("home directory %s is not a directory", homeDir)
	}

	return &Registrar{
		executablePath: executablePath,
		homeDir:        homeDir,
	}, nil
}

func (r *Registrar) Register() error {
	// Pre-flight validation: ensure the registrar is in a valid state before
	// attempting any OS modifications.
	if err := r.validatePreRegistration(); err != nil {
		return fmt.Errorf("pre-registration validation failed: %w", err)
	}

	start := time.Now()
	var err error
	switch runtime.GOOS {
	case "windows":
		err = r.registerWindows()
	case "darwin":
		err = r.registerDarwin()
	case "linux":
		err = r.registerLinux()
	default:
		err = fmt.Errorf("protocol registration is not supported on %s", runtime.GOOS)
	}
	if err != nil {
		return fmt.Errorf("%w (elapsed: %dms)", err, time.Since(start).Milliseconds())
	}
	return nil
}

// validatePreRegistration performs sanity checks before any OS state is modified.
func (r *Registrar) validatePreRegistration() error {
	if r.executablePath == "" {
		return fmt.Errorf(
			"executable path is empty — cannot register a handler that points nowhere\n"+
				"  Fix: ensure glassbox is invoked from a valid binary, not via 'go run' or an empty path",
		)
	}

	// Re-validate that the executable still exists (it could have been deleted after
	// NewRegistrar was called).
	if _, err := os.Stat(r.executablePath); err != nil {
		return fmt.Errorf(
			"executable no longer exists at %s: %w\n"+
				"  Fix: the binary may have been moved or deleted — reinstall or restart from the correct location",
			r.executablePath, err,
		)
	}

	// Validate home directory is still usable.
	if r.homeDir == "" {
		return fmt.Errorf("home directory is empty — cannot write registration artefacts")
	}
	if _, err := os.Stat(r.homeDir); err != nil {
		return fmt.Errorf("home directory %s is no longer accessible: %w", r.homeDir, err)
	}

	return nil
}

func (r *Registrar) Unregister() error {
	// Validate state before attempting removal to avoid partial/unclean unregistration.
	if r.executablePath == "" {
		return fmt.Errorf(
			"cannot unregister: executable path is empty\n"+
				"  Fix: ensure glassbox is invoked from a valid binary path",
		)
	}

	switch runtime.GOOS {
	case "windows":
		return r.unregisterWindows()
	case "darwin":
		return r.unregisterDarwin()
	case "linux":
		return r.unregisterLinux()
	default:
		return fmt.Errorf("protocol registration is not supported on %s", runtime.GOOS)
	}
}

func (r *Registrar) IsRegistered() bool {
	_, err := r.Verify()
	return err == nil
}

func (r *Registrar) Verify() (*VerificationReport, error) {
	// Guard against verifying a misconfigured registrar.
	if r.executablePath == "" {
		return nil, fmt.Errorf(
			"cannot verify: executable path is empty\n"+
				"  Fix: ensure glassbox is invoked from a valid binary path",
		)
	}

	start := time.Now()
	report := &VerificationReport{
		Platform: runtime.GOOS,
		Scheme:   Scheme,
	}

	switch runtime.GOOS {
	case "windows":
		r.verifyWindows(report)
	case "darwin":
		r.verifyDarwin(report)
	case "linux":
		r.verifyLinux(report)
	default:
		report.Issues = append(report.Issues, fmt.Sprintf("protocol verification is not supported on %s", runtime.GOOS))
	}

	report.ElapsedMs = time.Since(start).Milliseconds()

	if len(report.Issues) > 0 {
		return report, verificationError(report.Issues)
	}

	return report, nil
}

// ValidateForSnapshotOperation checks that the protocol registration is in a
// healthy enough state to support snapshot-related workflows (loading persisted
// snapshots, saving snapshot registries, and deep-linking into snapshot data).
// It returns nil when the registration is ready, or a descriptive error with
// remediation guidance when something is wrong.
//
// This is a lighter-weight check than PreflightCheck (which is meant for
// pre-registration validation). It focuses on the subset of conditions that
// would cause snapshot workflows to fail:
//   - Executable path is non-empty and reachable
//   - Home directory is accessible
//   - Platform is supported
//   - (Optional) Health check passes — the handler is actually registered
func (r *Registrar) ValidateForSnapshotOperation() error {
	// Check executable path is non-empty.
	if r.executablePath == "" {
		return fmt.Errorf(
			"protocol registration not ready for snapshot operations: executable path is empty\n" +
				"  Snapshot workflows need a valid glassbox binary to handle glassbox:// deep links.\n" +
				"  Fix: ensure glassbox is invoked from a valid binary path, not via 'go run'.",
		)
	}

	// Check executable exists and is reachable.
	if _, err := os.Stat(r.executablePath); err != nil {
		return fmt.Errorf(
			"protocol registration not ready for snapshot operations:\n"+
				"  executable not found at %s: %v\n"+
				"  The glassbox binary is required for snapshot deep-linking and protocol handler invocations.\n"+
				"  Fix: reinstall Glassbox or verify the binary path is correct.",
			r.executablePath, err,
		)
	}

	// Check platform is supported.
	switch runtime.GOOS {
	case "windows", "darwin", "linux":
		// supported
	default:
		return fmt.Errorf(
			"protocol registration not ready for snapshot operations:\n"+
				"  protocol registration is not supported on %s\n"+
				"  Snapshot deep-linking via glassbox:// requires a supported platform.\n"+
				"  Fix: use Linux, macOS, or Windows for snapshot workflows that use protocol registration.",
			runtime.GOOS,
		)
	}

	// Check home directory is accessible.
	if r.homeDir == "" {
		return fmt.Errorf(
			"protocol registration not ready for snapshot operations: home directory is empty\n"+
				"  Snapshot artefacts may need to be written to the home directory.\n"+
				"  Fix: set the HOME environment variable or ensure os.UserHomeDir() returns a valid path.",
		)
	}
	if _, err := os.Stat(r.homeDir); err != nil {
		return fmt.Errorf(
			"protocol registration not ready for snapshot operations:\n"+
				"  home directory %s is not accessible: %v\n"+
				"  Fix: ensure the home directory exists and is readable.",
			r.homeDir, err,
		)
	}

	// Run a lightweight health check to verify the handler is actually registered.
	health := r.HealthCheck()
	if !health.Ready {
		var failures []string
		for _, f := range health.Failures {
			failures = append(failures, f)
		}
		hint := health.Hint
		if hint == "" {
			hint = "Run 'glassbox protocol:repair' to restore the handler registration."
		}
		return fmt.Errorf(
			"protocol registration is not fully ready for snapshot operations (%d issue(s)):\n"+
				"  %s\n"+
				"  Snapshot workflows benefit from an active glassbox:// protocol handler for deep-linking.\n"+
				"  Hint: %s",
			len(failures), strings.Join(failures, "; "), hint,
		)
	}

	return nil
}

// validateRegistrationPreconditions rejects invalid inputs before any OS
// registration artefacts are written.
func (r *Registrar) validateRegistrationPreconditions() error {
	if r.executablePath == "" {
		return fmt.Errorf(
			"cannot register: executable path is empty\n" +
				"  Fix: ensure glassbox is invoked from a valid binary path, not via 'go run'",
		)
	}

	if strings.ContainsRune(r.executablePath, 0) {
		return fmt.Errorf(
			"cannot register: executable path contains a null byte\n" +
				"  Fix: reinstall Glassbox from a trusted source",
		)
	}

	if _, err := os.Stat(r.executablePath); err != nil {
		return fmt.Errorf(
			"cannot register: executable not found at %s: %w\n"+
				"  Fix: reinstall Glassbox or verify the binary path is correct",
			r.executablePath, err,
		)
	}

	switch runtime.GOOS {
	case "windows", "darwin", "linux":
		return nil
	default:
		return fmt.Errorf(
			"protocol registration is not supported on %s\n"+
				"  Fix: use Linux, macOS, or Windows to register the %s:// handler",
			runtime.GOOS, Scheme,
		)
	}
}

func (r *Registrar) registerWindows() error {
	// Detect Protocol Registry Conflicts (Issue #1198)
	registryOutput, err := runCommand("reg", "query", windowsRegistryKey, "/ve")
	if err == nil {
		// The key exists. Check whether it belongs to a different application by
		// verifying the shell\open\command value points to this binary.
		cmdOutput, cmdErr := runCommand("reg", "query", windowsRegistryKey+`\shell\open\command`, "/ve")
		if cmdErr == nil && !strings.Contains(cmdOutput, r.executablePath) {
			// The key exists and its open command references a different binary —
			// this is a genuine registry conflict, not just a stale self-reference.
			return fmt.Errorf(
				"protocol registration conflict: %s\\shell\\open\\command is claimed by a different application: %w\n"+
					"  Fix: run 'glassbox protocol:repair' to reclaim the registration",
				windowsRegistryKey, ersterrors.ErrRegistryConflict,
			)
		}
		if !strings.Contains(registryOutput, "glassbox") {
			// If the key exists (err == nil) but (Default) doesn't contain 'glassbox', it's a conflict
			return fmt.Errorf(
				"protocol registration conflict: registry key %s appears to belong to another application: %w\n"+
					"  Fix: run 'glassbox protocol:repair' to reclaim the registration",
				windowsRegistryKey, ersterrors.ErrRegistryConflict,
			)
		}
	}

	commands := [][]string{
		{"add", windowsRegistryKey, "/ve", "/d", "URL:GLASSBOX Protocol", "/f"},
		{"add", windowsRegistryKey, "/v", "URL Protocol", "/d", "", "/f"},
		{"add", windowsRegistryKey + `\shell\open\command`, "/ve", "/d", r.windowsOpenCommand(), "/f"},
	}

	for _, args := range commands {
		if _, err := runCommand("reg", args...); err != nil {
			return err
		}
	}

	return nil
}

func (r *Registrar) unregisterWindows() error {
	_, err := runCommand("reg", "delete", windowsRegistryKey, "/f")
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "unable to find") {
		return nil
	}
	return err
}

func (r *Registrar) verifyWindows(report *VerificationReport) {
	registryOutput, err := runCommand("reg", "query", windowsRegistryKey)
	if err != nil {
		report.Issues = append(report.Issues, fmt.Sprintf("missing registry key %s: %v", windowsRegistryKey, err))
		return
	}
	report.Checks = append(report.Checks, fmt.Sprintf("Found registry key %s", windowsRegistryKey))

	if !strings.Contains(registryOutput, "URL Protocol") {
		report.Issues = append(report.Issues, "missing URL Protocol registry value")
	} else {
		report.Checks = append(report.Checks, "Found URL Protocol registry value")
	}

	commandOutput, err := runCommand("reg", "query", windowsRegistryKey+`\shell\open\command`, "/ve")
	if err != nil {
		report.Issues = append(report.Issues, fmt.Sprintf("missing shell open command: %v", err))
		return
	}

	if !strings.Contains(commandOutput, r.windowsOpenCommand()) {
		report.Issues = append(report.Issues, fmt.Sprintf("unexpected shell open command, want %q", r.windowsOpenCommand()))
	} else {
		report.Checks = append(report.Checks, fmt.Sprintf("Shell open command points to %s", r.executablePath))
	}
}

func (r *Registrar) registerLinux() error {
	// Validate that required system tools are available before writing files.
	if !hasCommand("xdg-mime") {
		return fmt.Errorf(
			"xdg-mime is not installed: cannot register the glassbox:// MIME handler\n" +
				"  Fix: install xdg-utils — try one of:\n" +
				"    sudo apt install xdg-utils   (Debian/Ubuntu)\n" +
				"    sudo dnf install xdg-utils   (Fedora/RHEL)\n" +
				"    sudo pacman -S xdg-utils     (Arch Linux)",
		)
	}

	applicationsDir := filepath.Dir(r.linuxDesktopPath())
	if err := os.MkdirAll(applicationsDir, 0o755); err != nil {
		return fmt.Errorf("create applications directory %s: %w", applicationsDir, err)
	}

	helperDir := filepath.Dir(r.linuxWrapperPath())
	if err := os.MkdirAll(helperDir, 0o755); err != nil {
		return fmt.Errorf("create protocol helper directory %s: %w", helperDir, err)
	}

	// Write the wrapper script first so we can validate it before writing the desktop file.
	if err := os.WriteFile(r.linuxWrapperPath(), []byte(r.unixHandlerScript()), 0o755); err != nil {
		return fmt.Errorf("write protocol helper script to %s: %w", r.linuxWrapperPath(), err)
	}

	// Validate the wrapper script was written correctly by reading it back.
	wrapperContent, readErr := os.ReadFile(r.linuxWrapperPath())
	if readErr != nil {
		return fmt.Errorf("verify written wrapper script: %w", readErr)
	}
	if !strings.Contains(string(wrapperContent), r.executablePath) {
		return fmt.Errorf(
			"wrapper script does not reference the executable %s — written content may be corrupted",
			r.executablePath,
		)
	}

	if err := os.WriteFile(r.linuxDesktopPath(), []byte(r.linuxDesktopEntry()), 0o644); err != nil {
		// Clean up the wrapper script if desktop file write fails to avoid partial registration.
		_ = os.Remove(r.linuxWrapperPath())
		return fmt.Errorf("write desktop file to %s: %w", r.linuxDesktopPath(), err)
	}

	if _, err := runCommand("xdg-mime", "default", linuxDesktopFile, linuxMimeType); err != nil {
		return fmt.Errorf("configure xdg-mime default handler: %w", err)
	}

	if hasCommand("update-desktop-database") {
		if _, err := runCommand("update-desktop-database", filepath.Dir(r.linuxDesktopPath())); err != nil {
			// Non-fatal: update-desktop-database failure shouldn't block registration.
			// The MIME association is already set; the database update is an optimization.
		}
	}

	return nil
}

func (r *Registrar) unregisterLinux() error {
	var joined error

	if err := os.Remove(r.linuxDesktopPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		joined = errors.Join(joined, fmt.Errorf("remove desktop file: %w", err))
	}
	if err := os.Remove(r.linuxWrapperPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		joined = errors.Join(joined, fmt.Errorf("remove protocol helper: %w", err))
	}

	return joined
}

func (r *Registrar) verifyLinux(report *VerificationReport) {
	desktopBytes, err := os.ReadFile(r.linuxDesktopPath())
	if err != nil {
		report.Issues = append(report.Issues, fmt.Sprintf("missing desktop file %s: %v", r.linuxDesktopPath(), err))
		return
	}

	desktopContent := string(desktopBytes)
	report.Checks = append(report.Checks, fmt.Sprintf("Found desktop file %s", r.linuxDesktopPath()))

	if !strings.Contains(desktopContent, "MimeType="+linuxMimeType+";") {
		report.Issues = append(report.Issues, "desktop file is missing the Glassbox MIME handler declaration")
	} else {
		report.Checks = append(report.Checks, "Desktop file declares x-scheme-handler/Glassbox")
	}

	if !strings.Contains(desktopContent, "Exec="+r.linuxWrapperPath()+" %u") {
		report.Issues = append(report.Issues, fmt.Sprintf("desktop file Exec entry does not point to %s", r.linuxWrapperPath()))
	} else {
		report.Checks = append(report.Checks, fmt.Sprintf("Desktop file Exec entry points to %s", r.linuxWrapperPath()))
	}

	wrapperBytes, err := os.ReadFile(r.linuxWrapperPath())
	if err != nil {
		report.Issues = append(report.Issues, fmt.Sprintf("missing protocol helper script %s: %v", r.linuxWrapperPath(), err))
	} else {
		wrapperContent := string(wrapperBytes)
		if !strings.Contains(wrapperContent, r.executablePath) {
			report.Issues = append(report.Issues, fmt.Sprintf("protocol helper script does not reference %s", r.executablePath))
		} else {
			report.Checks = append(report.Checks, fmt.Sprintf("Protocol helper script launches %s", r.executablePath))
		}
	}

	defaultDesktop, err := runCommand("xdg-mime", "query", "default", linuxMimeType)
	if err != nil {
		report.Issues = append(report.Issues, fmt.Sprintf("failed to query MIME handler: %v", err))
		return
	}

	if strings.TrimSpace(defaultDesktop) != linuxDesktopFile {
		report.Issues = append(report.Issues, fmt.Sprintf("xdg-mime reports %q instead of %q", strings.TrimSpace(defaultDesktop), linuxDesktopFile))
	} else {
		report.Checks = append(report.Checks, fmt.Sprintf("xdg-mime resolves %s to %s", linuxMimeType, linuxDesktopFile))
	}
}

func (r *Registrar) registerDarwin() error {
	appPath := r.macOSAppPath()
	execPath := r.macOSExecutablePath()
	plistPath := r.macOSPlistPath()

	// Validate that the LaunchServices tool exists before writing files.
	if !hasCommand(macOSLSRegisterPath()) {
		return fmt.Errorf(
			"LaunchServices registration tool not found at %s\n"+
				"  Fix: this tool is part of macOS and should always be present — your macOS installation may be corrupted",
			macOSLSRegisterPath(),
		)
	}

	bundleDir := filepath.Dir(execPath)
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		return fmt.Errorf("create macOS app bundle directory %s: %w", bundleDir, err)
	}

	// Write the executable script.
	if err := os.WriteFile(execPath, []byte(r.unixHandlerScript()), 0o755); err != nil {
		return fmt.Errorf("write app bundle executable to %s: %w", execPath, err)
	}

	// Validate the executable script references the correct binary.
	execContent, readErr := os.ReadFile(execPath)
	if readErr != nil {
		return fmt.Errorf("verify written app bundle executable: %w", readErr)
	}
	if !strings.Contains(string(execContent), r.executablePath) {
		return fmt.Errorf(
			"app bundle executable does not reference %s — written content may be corrupted",
			r.executablePath,
		)
	}

	// Write the Info.plist.
	if err := os.WriteFile(plistPath, []byte(r.macOSInfoPlist()), 0o644); err != nil {
		// Clean up the executable if plist write fails.
		_ = os.RemoveAll(appPath)
		return fmt.Errorf("write app bundle plist to %s: %w", plistPath, err)
	}

	// Validate the plist contains the expected scheme.
	plistContent, plistReadErr := os.ReadFile(plistPath)
	if plistReadErr != nil {
		_ = os.RemoveAll(appPath)
		return fmt.Errorf("verify written plist: %w", plistReadErr)
	}
	if !strings.Contains(string(plistContent), Scheme) {
		_ = os.RemoveAll(appPath)
		return fmt.Errorf(
			"generated Info.plist does not contain the %q scheme — plist generation is broken",
			Scheme,
		)
	}

	// Register with LaunchServices.
	if _, err := runCommand(macOSLSRegisterPath(), "-f", appPath); err != nil {
		return fmt.Errorf("register with LaunchServices: %w", err)
	}

	return nil
}

func (r *Registrar) unregisterDarwin() error {
	var joined error

	if _, err := os.Stat(r.macOSAppPath()); errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if _, err := runCommand(macOSLSRegisterPath(), "-u", r.macOSAppPath()); err != nil {
		joined = errors.Join(joined, err)
	}

	if err := os.RemoveAll(r.macOSAppPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		joined = errors.Join(joined, fmt.Errorf("remove macOS app bundle: %w", err))
	}

	return joined
}

func (r *Registrar) verifyDarwin(report *VerificationReport) {
	plistBytes, err := os.ReadFile(r.macOSPlistPath())
	if err != nil {
		report.Issues = append(report.Issues, fmt.Sprintf("missing app bundle plist %s: %v", r.macOSPlistPath(), err))
		return
	}
	plistContent := string(plistBytes)
	report.Checks = append(report.Checks, fmt.Sprintf("Found app bundle plist %s", r.macOSPlistPath()))

	if _, err := os.Stat(r.macOSExecutablePath()); err != nil {
		report.Issues = append(report.Issues, fmt.Sprintf("missing app bundle executable %s: %v", r.macOSExecutablePath(), err))
	} else {
		report.Checks = append(report.Checks, fmt.Sprintf("Found app bundle executable %s", r.macOSExecutablePath()))
	}

	if !strings.Contains(plistContent, "<key>CFBundleURLSchemes</key>") || !strings.Contains(plistContent, "<string>"+Scheme+"</string>") {
		report.Issues = append(report.Issues, "Info.plist does not declare the Glassbox URL scheme")
	} else {
		report.Checks = append(report.Checks, "Info.plist declares the Glassbox URL scheme")
	}

	registrationDump, err := runCommand(macOSLSRegisterPath(), "-dump")
	if err != nil {
		report.Issues = append(report.Issues, fmt.Sprintf("failed to inspect LaunchServices registration: %v", err))
		return
	}

	if !strings.Contains(registrationDump, r.macOSAppPath()) {
		report.Issues = append(report.Issues, fmt.Sprintf("LaunchServices dump does not contain %s", r.macOSAppPath()))
	} else {
		report.Checks = append(report.Checks, fmt.Sprintf("LaunchServices contains %s", r.macOSAppPath()))
	}
	if !strings.Contains(registrationDump, Scheme) {
		report.Issues = append(report.Issues, "LaunchServices dump does not mention the Glassbox scheme")
	} else {
		report.Checks = append(report.Checks, "LaunchServices dump mentions the Glassbox scheme")
	}

	executableBytes, err := os.ReadFile(r.macOSExecutablePath())
	if err == nil {
		executableContent := string(executableBytes)
		if !strings.Contains(executableContent, r.executablePath) {
			report.Issues = append(report.Issues, fmt.Sprintf("app bundle executable does not launch %s", r.executablePath))
		} else {
			report.Checks = append(report.Checks, fmt.Sprintf("App bundle executable launches %s", r.executablePath))
		}
	}
}

func (r *Registrar) windowsOpenCommand() string {
	return fmt.Sprintf(`"%s" protocol-handler "%%1"`, r.executablePath)
}

func (r *Registrar) linuxDesktopPath() string {
	return filepath.Join(r.homeDir, ".local", "share", "applications", linuxDesktopFile)
}

func (r *Registrar) linuxWrapperPath() string {
	return filepath.Join(r.homeDir, ".local", "share", "glassbox", macOSExecutable)
}

func (r *Registrar) linuxDesktopEntry() string {
	return strings.Join([]string{
		"[Desktop Entry]",
		"Version=1.0",
		"Type=Application",
		"Name=GLASSBOX Protocol Handler",
		"Exec=" + r.linuxWrapperPath() + " %u",
		"MimeType=" + linuxMimeType + ";",
		"NoDisplay=true",
		"Terminal=false",
		"",
	}, "\n")
}

func (r *Registrar) macOSAppPath() string {
	return filepath.Join(r.homeDir, "Applications", macOSAppName)
}

func (r *Registrar) macOSExecutablePath() string {
	return filepath.Join(r.macOSAppPath(), "Contents", "MacOS", macOSExecutable)
}

func (r *Registrar) macOSPlistPath() string {
	return filepath.Join(r.macOSAppPath(), "Contents", "Info.plist")
}

func (r *Registrar) macOSInfoPlist() string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleDevelopmentRegion</key>
    <string>en</string>
    <key>CFBundleExecutable</key>
    <string>%s</string>
    <key>CFBundleIdentifier</key>
    <string>%s</string>
    <key>CFBundleInfoDictionaryVersion</key>
    <string>6.0</string>
    <key>CFBundleName</key>
    <string>GLASSBOX Protocol Handler</string>
    <key>CFBundlePackageType</key>
    <string>APPL</string>
    <key>CFBundleShortVersionString</key>
    <string>1.0</string>
    <key>CFBundleURLTypes</key>
    <array>
        <dict>
            <key>CFBundleURLName</key>
            <string>GLASSBOX Protocol</string>
            <key>CFBundleURLSchemes</key>
            <array>
                <string>%s</string>
            </array>
        </dict>
    </array>
</dict>
</plist>
`, macOSExecutable, macOSBundleID, Scheme)
}

func (r *Registrar) unixHandlerScript() string {
	return fmt.Sprintf("#!/bin/sh\nexec %s protocol-handler \"$1\"\n", shellQuote(r.executablePath))
}

func macOSLSRegisterPath() string {
	return "/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"
}

func runCommand(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		trimmed := strings.TrimSpace(string(output))
		if trimmed == "" {
			return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
		}
		return string(output), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, trimmed)
	}
	return string(output), nil
}

func hasCommand(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

func verificationError(issues []string) error {
	return fmt.Errorf("protocol registration verification failed: %s", strings.Join(issues, "; "))
}
