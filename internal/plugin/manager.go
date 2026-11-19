// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Manager coordinates plugin operations with the main decoder system.
// It owns a Registry (which owns one PluginExecutor per sandboxed plugin) and
// exposes a higher-level API for the CLI and analysis pipeline.
//
// All decode calls are automatically routed through the plugin's executor so
// timeouts, panic recovery, cancellation, and quarantine are always enforced.
type Manager struct {
	registry *Registry
	baseDir  string
}

// NewManager creates a plugin manager with an optional base directory.
func NewManager(baseDir string) (*Manager, error) {
	if baseDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to determine working directory: %w", err)
		}
		baseDir = cwd
	}

	return &Manager{
		registry: NewRegistry(),
		baseDir:  baseDir,
	}, nil
}

// Initialize loads plugins from the plugins directory under baseDir.
// It supports both manifest-based subdirectories and legacy *.so files.
func (m *Manager) Initialize() error {
	pluginDir := filepath.Join(m.baseDir, "plugins")

	if _, err := os.Stat(pluginDir); os.IsNotExist(err) {
		return fmt.Errorf("plugins directory not found at %s", pluginDir)
	}

	return m.registry.LoadFromDirectory(pluginDir)
}

// RegisterFromManifest registers a single plugin from an explicit manifest file path.
func (m *Manager) RegisterFromManifest(manifestPath string) error {
	return m.registry.RegisterManifest(manifestPath)
}

// Bus returns the lifecycle event bus so callers can subscribe to plugin events.
func (m *Manager) Bus() *LifecycleBus {
	return m.registry.Bus()
}

// DecodeEvent decodes using the most appropriate plugin.
// The call is routed through the plugin's PluginExecutor; the background
// context is used. Use DecodeEventWithContext to propagate cancellation.
func (m *Manager) DecodeEvent(eventType string, data []byte) (json.RawMessage, error) {
	result, _, err := m.registry.FindAndDecode(eventType, data)
	return result, err
}

// DecodeEventWithContext is like DecodeEvent but accepts a caller context so
// that cancellation and deadlines propagate into the plugin child process.
func (m *Manager) DecodeEventWithContext(ctx context.Context, eventType string, data []byte) (json.RawMessage, error) {
	r, _, err := m.registry.FindAndDecodeWithContext(ctx, eventType, data)
	return r, err
}

// DecodeEventWithPlugin uses a specific named plugin to decode an event.
// The call is routed through that plugin's PluginExecutor.
func (m *Manager) DecodeEventWithPlugin(pluginName string, eventType string, data []byte) (json.RawMessage, error) {
	return m.registry.Decode(pluginName, eventType, data)
}

// DecodeEventWithPluginContext is like DecodeEventWithPlugin but propagates ctx.
func (m *Manager) DecodeEventWithPluginContext(ctx context.Context, pluginName string, eventType string, data []byte) (json.RawMessage, error) {
	return m.registry.DecodeWithContext(ctx, pluginName, eventType, data)
}

// GetPlugins returns metadata for all available plugins.
func (m *Manager) GetPlugins() []Metadata {
	return m.registry.ListPlugins()
}

// GetPlugin retrieves a specific plugin by name.
func (m *Manager) GetPlugin(name string) (DecoderPlugin, bool) {
	m.registry.mu.RLock()
	defer m.registry.mu.RUnlock()
	return m.registry.loader.Get(name)
}

// GetManifest returns the manifest for a registered plugin.
func (m *Manager) GetManifest(name string) (*Manifest, bool) {
	return m.registry.GetManifest(name)
}

// PluginStatus returns a snapshot of health and quarantine state for all
// sandboxed plugins managed by this Manager.
func (m *Manager) PluginStatus() []PluginStatusSnapshot {
	return m.registry.PluginStatus()
}

// GetExecutor returns the PluginExecutor for the named sandboxed plugin so
// callers can perform direct executor operations such as health checks or
// configuring per-call resource limits.
func (m *Manager) GetExecutor(name string) (*PluginExecutor, bool) {
	return m.registry.GetExecutor(name)
}

// StartHealthPolling launches background health-check goroutines for every
// sandboxed plugin currently registered. The goroutines stop when ctx is
// cancelled or the plugin is quarantined. onError is called each time a
// health check fails; it may be nil.
func (m *Manager) StartHealthPolling(ctx context.Context, onError func(pluginName string, err error)) {
	m.registry.mu.RLock()
	defer m.registry.mu.RUnlock()

	for name := range m.registry.executors {
		sp, ok := m.registry.loader.plugins[name].(*SandboxedPlugin)
		if !ok {
			continue
		}
		poller := NewHealthPoller(sp, healthCheckInterval, onError)
		poller.Start(ctx)
	}
}

// Shutdown emits cleanup lifecycle events for all registered plugins and closes
// all executors. It should be called once when the CLI is exiting.
func (m *Manager) Shutdown() {
	m.registry.Clear()
}
