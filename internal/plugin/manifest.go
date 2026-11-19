// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ManifestVersion is the current schema version for plugin manifests.
const ManifestVersion = "1"

// TrustLevel expresses how much the host trusts a plugin's origin and content.
type TrustLevel string

const (
	// TrustLevelVerified means the plugin has been signed and its identity confirmed.
	TrustLevelVerified TrustLevel = "verified"
	// TrustLevelCommunity means the plugin is published by a third-party author.
	TrustLevelCommunity TrustLevel = "community"
	// TrustLevelUntrusted is the default for plugins with no explicit trust declaration.
	TrustLevelUntrusted TrustLevel = "untrusted"
)

// validTrustLevels is the set of accepted trust level values.
var validTrustLevels = map[TrustLevel]bool{
	TrustLevelVerified:  true,
	TrustLevelCommunity: true,
	TrustLevelUntrusted: true,
}

// ManifestFileName is the conventional name for a plugin manifest file.
const ManifestFileName = "plugin.json"

// Capability represents a named capability a plugin declares it provides.
type Capability string

const (
	// CapabilityDecoder allows the plugin to decode custom event types.
	CapabilityDecoder Capability = "decoder"
	// CapabilityAnalyzer allows the plugin to contribute analysis hooks.
	CapabilityAnalyzer Capability = "analyzer"
	// CapabilityTraceViewer allows the plugin to register a custom trace viewer.
	CapabilityTraceViewer Capability = "trace_viewer"
	// CapabilityArtifactLoader allows the plugin to load custom artifact formats.
	CapabilityArtifactLoader Capability = "artifact_loader"
	// CapabilitySigning allows the plugin to perform cryptographic signing operations.
	CapabilitySigning Capability = "signing"
	// CapabilityLogging allows the plugin to emit structured log entries.
	CapabilityLogging Capability = "logging"
)

// Permission represents a runtime permission a plugin requires.
type Permission string

const (
	// PermissionReadFS allows the plugin to read from the filesystem.
	PermissionReadFS Permission = "read_fs"
	// PermissionNetwork allows the plugin to make outbound network calls.
	PermissionNetwork Permission = "network"
	// PermissionWriteFS allows the plugin to write to the filesystem.
	PermissionWriteFS Permission = "write_fs"
	// PermissionExecuteSubprocess allows the plugin to spawn child processes.
	PermissionExecuteSubprocess Permission = "execute_subprocess"
	// PermissionAccessEnv allows the plugin to read environment variables.
	PermissionAccessEnv Permission = "access_env"
)

// validCapabilities is the set of recognised capability strings.
var validCapabilities = map[Capability]bool{
	CapabilityDecoder:        true,
	CapabilityAnalyzer:       true,
	CapabilityTraceViewer:    true,
	CapabilityArtifactLoader: true,
	CapabilitySigning:        true,
	CapabilityLogging:        true,
}

// validPermissions is the set of recognised permission strings.
var validPermissions = map[Permission]bool{
	PermissionReadFS:             true,
	PermissionNetwork:            true,
	PermissionWriteFS:            true,
	PermissionExecuteSubprocess:  true,
	PermissionAccessEnv:          true,
}

// requiredPermissions maps capabilities to the minimum permissions they require.
// A manifest declaring a capability must also request its required permissions;
// the loader enforces this mapping before activation.
var requiredPermissions = map[Capability][]Permission{
	CapabilitySigning:        {PermissionReadFS, PermissionAccessEnv},
	CapabilityLogging:        {PermissionWriteFS},
	CapabilityArtifactLoader: {PermissionReadFS},
}

// semverRE is a loose semver pattern: MAJOR.MINOR.PATCH with optional pre-release.
var semverRE = regexp.MustCompile(`^\d+\.\d+\.\d+(-[a-zA-Z0-9.]+)?$`)

// pluginNameRE restricts plugin names to safe identifier characters.
var pluginNameRE = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]{0,63}$`)

// Manifest describes a plugin's identity, capabilities, and requirements.
// It is loaded from a plugin.json file alongside the plugin binary.
type Manifest struct {
	// SchemaVersion is the manifest format version (currently "1").
	SchemaVersion string `json:"schema_version"`

	// Name is the unique plugin identifier. Must match [a-zA-Z][a-zA-Z0-9_-]{0,63}.
	Name string `json:"name"`

	// Version is the plugin's own semver string (e.g. "1.2.3").
	Version string `json:"version"`

	// APIVersion is the Glassbox plugin API version this plugin targets.
	// Must match the current plugin.Version constant.
	APIVersion string `json:"api_version"`

	// Entrypoint is the path to the plugin binary or shared library,
	// relative to the manifest file's directory.
	Entrypoint string `json:"entrypoint"`

	// Description is a human-readable summary of what the plugin does.
	Description string `json:"description,omitempty"`

	// Author is the plugin author or organisation name.
	Author string `json:"author,omitempty"`

	// Capabilities lists the extension points this plugin provides.
	Capabilities []Capability `json:"capabilities"`

	// Permissions lists the runtime permissions the plugin requires.
	// The loader will refuse to start a plugin that requests unknown permissions.
	Permissions []Permission `json:"permissions,omitempty"`

	// EventTypes lists the event type strings this plugin can decode.
	// Only meaningful when CapabilityDecoder is declared.
	EventTypes []string `json:"event_types,omitempty"`

	// Checksum is the optional SHA-256 hex digest of the entrypoint binary.
	// When present the loader verifies the binary before execution.
	Checksum string `json:"checksum,omitempty"`

	// GlassboxVersionRange is a semver range string (e.g. ">=1.0.0 <2.0.0")
	// declaring the Glassbox host versions this plugin is compatible with.
	// An empty value means "all versions accepted". The loader uses this to warn
	// when the running Glassbox version falls outside the declared range.
	GlassboxVersionRange string `json:"glassbox_version_range,omitempty"`

	// TrustLevel communicates the plugin author's claimed trust boundary.
	// Accepted values: "verified", "community", "untrusted" (default when omitted).
	TrustLevel TrustLevel `json:"trust_level,omitempty"`
}

// Validate checks that all required fields are present and well-formed.
func (m *Manifest) Validate() error {
	if m.SchemaVersion != ManifestVersion {
		return fmt.Errorf("unsupported manifest schema_version %q (expected %q)", m.SchemaVersion, ManifestVersion)
	}
	if !pluginNameRE.MatchString(m.Name) {
		return fmt.Errorf("invalid plugin name %q: must match [a-zA-Z][a-zA-Z0-9_-]{0,63}", m.Name)
	}
	if !semverRE.MatchString(m.Version) {
		return fmt.Errorf("invalid plugin version %q: must be semver (MAJOR.MINOR.PATCH)", m.Version)
	}
	if m.APIVersion != Version {
		return fmt.Errorf("plugin API version %q does not match current %q", m.APIVersion, Version)
	}
	if strings.TrimSpace(m.Entrypoint) == "" {
		return fmt.Errorf("entrypoint must not be empty")
	}
	// Reject path traversal in entrypoint to prevent loading binaries outside
	// the plugin directory.
	if strings.Contains(m.Entrypoint, "..") {
		return fmt.Errorf("entrypoint must not contain path traversal sequences")
	}
	if len(m.Entrypoint) > 256 {
		return fmt.Errorf("entrypoint path is too long (%d characters, max 256)", len(m.Entrypoint))
	}
	if len(m.Capabilities) == 0 {
		return fmt.Errorf("plugin must declare at least one capability")
	}
	for _, cap := range m.Capabilities {
		if !validCapabilities[cap] {
			return fmt.Errorf("unknown capability %q", cap)
		}
	}
	for _, perm := range m.Permissions {
		if !validPermissions[perm] {
			return fmt.Errorf("unknown permission %q", perm)
		}
	}
	// Validate capability-permission mapping: ensure that each capability's
	// required permissions are declared by the plugin.
	for _, cap := range m.Capabilities {
		if required, ok := requiredPermissions[cap]; ok {
			for _, needed := range required {
				if !m.HasPermission(needed) {
					return fmt.Errorf(
						"capability %q requires permission %q but it is not declared; "+
							"add %q to the permissions array", cap, needed, needed,
					)
				}
			}
		}
	}
	if m.TrustLevel != "" && !validTrustLevels[m.TrustLevel] {
		return fmt.Errorf("unknown trust_level %q: must be one of verified, community, untrusted", m.TrustLevel)
	}
	if m.GlassboxVersionRange != "" {
		if err := validateVersionRange(m.GlassboxVersionRange); err != nil {
			return fmt.Errorf("invalid glassbox_version_range: %w", err)
		}
	}
	return nil
}

// validateVersionRange performs a basic sanity check on a semver range string.
// It rejects obviously malformed inputs without requiring a full semver parser.
func validateVersionRange(vr string) error {
	vr = strings.TrimSpace(vr)
	if vr == "" {
		return fmt.Errorf("version range must not be blank")
	}
	// A valid range must contain at least one digit.
	hasDigit := false
	for _, c := range vr {
		if c >= '0' && c <= '9' {
			hasDigit = true
			break
		}
	}
	if !hasDigit {
		return fmt.Errorf("version range %q contains no version numbers", vr)
	}
	return nil
}

// HasCapability reports whether the manifest declares the given capability.
func (m *Manifest) HasCapability(cap Capability) bool {
	for _, c := range m.Capabilities {
		if c == cap {
			return true
		}
	}
	return false
}

// HasPermission reports whether the manifest requests the given permission.
func (m *Manifest) HasPermission(perm Permission) bool {
	for _, p := range m.Permissions {
		if p == perm {
			return true
		}
	}
	return false
}

// LoadManifest reads and validates a manifest from the given file path.
func LoadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest %s: %w", path, err)
	}

	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("failed to parse manifest %s: %w", path, err)
	}

	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("manifest %s is invalid: %w", path, err)
	}

	return &m, nil
}

// DiscoverManifests scans dir for subdirectories each containing a plugin.json
// and returns all valid manifests found. Errors for individual manifests are
// collected and returned alongside the valid set so callers can log them
// without aborting the entire discovery pass.
func DiscoverManifests(dir string) ([]*Manifest, []error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, []error{fmt.Errorf("failed to read plugin directory %s: %w", dir, err)}
	}

	var manifests []*Manifest
	var errs []error

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		manifestPath := filepath.Join(dir, entry.Name(), ManifestFileName)
		m, err := LoadManifest(manifestPath)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		manifests = append(manifests, m)
	}

	return manifests, errs
}
