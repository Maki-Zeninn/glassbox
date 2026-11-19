// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/dotandev/glassbox/internal/errors"
)

// Policy defines the sandbox policy for plugin loading.
// It controls which capabilities and permissions are denied globally,
// regardless of what individual plugin manifests declare.
type Policy struct {
	// DeniedCapabilities is the set of capability strings that are forbidden.
	// Any plugin declaring one of these capabilities will be refused at load time.
	DeniedCapabilities []string `json:"denied_capabilities,omitempty"`

	// DeniedPermissions is the set of permission strings that are forbidden.
	// Any plugin requesting one of these permissions will be refused at load time.
	DeniedPermissions []string `json:"denied_permissions,omitempty"`

	// DeniedPlugins is a list of plugin names that are explicitly blocked,
	// regardless of their capabilities or permissions.
	DeniedPlugins []string `json:"denied_plugins,omitempty"`

	// AllowUntrusted, when true, permits plugins with TrustLevel 'untrusted'
	// or 'community' to load. The zero value is false (deny untrusted); use DefaultPolicy()
	// to get a restrictive policy.
	AllowUntrusted bool `json:"allow_untrusted"`
}

// DefaultPolicy returns a secure-by-default policy that denies untrusted
// and community plugins. Operators must explicitly opt-in to allow them
// via config (plugin.allow_untrusted = true) or the CLI flag
// --allow-untrusted-plugins.
func DefaultPolicy() *Policy {
	return &Policy{AllowUntrusted: false}
}

// LoadPolicy reads and parses a JSON policy file from path.
func LoadPolicy(path string) (*Policy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read policy file %q: %w", path, err)
	}
	var p Policy
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("failed to parse policy file %q: %w", path, err)
	}
	return &p, nil
}

// LoadPolicyWithOverride reads and parses a JSON policy file from path,
// or uses DefaultPolicy() if path is empty, and overrides AllowUntrusted
// if allowUntrustedOverride is true.
func LoadPolicyWithOverride(path string, allowUntrustedOverride bool) (*Policy, error) {
	var p *Policy
	var err error
	if path != "" {
		p, err = LoadPolicy(path)
		if err != nil {
			return nil, err
		}
	} else {
		p = DefaultPolicy()
	}
	if allowUntrustedOverride {
		p.AllowUntrusted = true
	}
	return p, nil
}

// CheckManifest returns an error if the manifest violates the policy.
// It checks denied capabilities, denied permissions, denied plugin names,
// and the untrusted-plugin allowance.
func (p *Policy) CheckManifest(m *Manifest) error {
	if p == nil {
		return nil
	}

	// Check denied plugin names.
	for _, denied := range p.DeniedPlugins {
		if m.Name == denied {
			return fmt.Errorf("plugin %q is explicitly denied by policy", m.Name)
		}
	}

	deniedCaps := make(map[string]bool, len(p.DeniedCapabilities))
	for _, c := range p.DeniedCapabilities {
		deniedCaps[c] = true
	}
	deniedPerms := make(map[string]bool, len(p.DeniedPermissions))
	for _, pr := range p.DeniedPermissions {
		deniedPerms[pr] = true
	}

	// Check denied capabilities.
	for _, cap := range m.Capabilities {
		if deniedCaps[string(cap)] {
			return fmt.Errorf("plugin %q declares capability %q which is denied by policy", m.Name, string(cap))
		}
	}

	// Check denied permissions.
	for _, perm := range m.Permissions {
		if deniedPerms[string(perm)] {
			return fmt.Errorf("plugin %q requests permission %q which is denied by policy", m.Name, string(perm))
		}
	}

	// Check untrusted/community allowance.
	if !p.AllowUntrusted && (m.TrustLevel == TrustLevelUntrusted || m.TrustLevel == TrustLevelCommunity || m.TrustLevel == "") {
		source := m.Author
		if source == "" {
			source = m.Entrypoint
		}
		if source == "" {
			source = "unknown"
		}
		tLevel := m.TrustLevel
		if tLevel == "" {
			tLevel = TrustLevelUntrusted
		}
		return &errors.ErstError{
			Code:    errors.ErstValidationFailed,
			Message: fmt.Sprintf("plugin %q has trust level %q (source: %s) which is not allowed by policy", m.Name, tLevel, source),
			Hint:    "To allow community and untrusted plugins, pass --allow-untrusted-plugins on the CLI or set plugin.allow_untrusted = true in the config TOML.",
		}
	}

	return nil
}
