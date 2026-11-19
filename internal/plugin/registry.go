// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"time"
)

// Registry manages the plugin ecosystem with isolation and versioning.
// Every registered SandboxedPlugin gets its own PluginExecutor so that
// timeouts, panic recovery, and quarantine are enforced on every call.
type Registry struct {
	mu       sync.RWMutex
	loader   *Loader
	cache    map[string]json.RawMessage
	bus      *LifecycleBus
	// manifests holds the loaded manifests keyed by plugin name.
	manifests map[string]*Manifest
	// executors holds a PluginExecutor per sandboxed plugin.
	executors map[string]*PluginExecutor
	// policy is the active sandbox policy; nil means no restrictions.
	policy *Policy
}

// NewRegistry initialises a fresh registry with the default (permissive) policy.
func NewRegistry() *Registry {
	return &Registry{
		loader:    NewLoader(),
		cache:     make(map[string]json.RawMessage),
		bus:       NewLifecycleBus(),
		manifests: make(map[string]*Manifest),
		executors: make(map[string]*PluginExecutor),
		policy:    DefaultPolicy(),
	}
}

// SetPolicy replaces the active sandbox policy. Pass nil to remove all restrictions.
func (r *Registry) SetPolicy(p *Policy) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.policy = p
}

// Policy returns the active sandbox policy.
func (r *Registry) Policy() *Policy {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.policy
}

// Bus returns the lifecycle event bus so callers can subscribe to plugin events.
func (r *Registry) Bus() *LifecycleBus {
	return r.bus
}

// LoadFromDirectory scans and loads all plugins from a directory.
// It first attempts manifest-based discovery (subdirectories with plugin.json).
// If no manifests are found it falls back to scanning for *.so shared libraries.
func (r *Registry) LoadFromDirectory(dir string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Attempt manifest-based discovery first.
	manifests, manifestErrs := DiscoverManifests(dir)
	if len(manifests) > 0 {
		var loadErrors []error
		for _, m := range manifests {
			// Enforce sandbox policy before loading.
			if err := r.policy.CheckManifest(m); err != nil {
				loadErrors = append(loadErrors, fmt.Errorf("policy denied plugin %s: %w", m.Name, err))
				r.bus.Emit(LifecyclePayload{
					PluginName: m.Name,
					Event:      EventError,
					Err:        err,
				})
				continue
			}
			manifestDir := filepath.Join(dir, m.Name)
			sp, err := NewSandboxedPlugin(m, manifestDir)
			if err != nil {
				loadErrors = append(loadErrors, fmt.Errorf("plugin %s: %w", m.Name, err))
				continue
			}
			// Run Init lifecycle hook (best-effort) through the executor.
			exec := r.createExecutor(sp)
			initResult := exec.Call(context.Background(), PluginRequest{
				Version: EnvelopeVersion,
				ID:      newRequestID(),
				Method:  MethodInit,
				Limits:  DefaultResourceLimits(),
			})
			if initResult.Err != nil {
				r.bus.Emit(LifecyclePayload{
					PluginName: m.Name,
					Event:      EventError,
					Err:        initResult.Err,
				})
			}
			r.loader.plugins[m.Name] = sp
			r.manifests[m.Name] = m
			r.executors[m.Name] = exec
			r.bus.Emit(LifecyclePayload{
				PluginName: m.Name,
				Event:      EventRegistered,
			})
			r.bus.Emit(LifecyclePayload{
				PluginName: m.Name,
				Event:      EventInitialized,
			})
		}
		if len(loadErrors) > 0 {
			return fmt.Errorf("encountered %d plugin loading errors", len(loadErrors))
		}
		return nil
	}

	// Log manifest discovery errors as informational (directory may simply be empty).
	_ = manifestErrs

	// Fallback: scan for *.so shared libraries (original behaviour).
	pattern := filepath.Join(dir, "*.so")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Errorf("failed to scan plugin directory: %w", err)
	}

	var loadErrors []error
	for _, path := range matches {
		if err := r.loader.Load(path); err != nil {
			loadErrors = append(loadErrors, err)
		}
	}

	if len(loadErrors) > 0 {
		return fmt.Errorf("encountered %d plugin loading errors", len(loadErrors))
	}

	return nil
}

// RegisterManifest registers a plugin from an explicit manifest path.
// The plugin binary is resolved relative to the manifest's directory.
func (r *Registry) RegisterManifest(manifestPath string) error {
	m, err := LoadManifest(manifestPath)
	if err != nil {
		return err
	}

	// Enforce sandbox policy before creating the sandboxed plugin.
	r.mu.RLock()
	policy := r.policy
	r.mu.RUnlock()
	if err := policy.CheckManifest(m); err != nil {
		return fmt.Errorf("policy denied plugin %s: %w", m.Name, err)
	}

	manifestDir := filepath.Dir(manifestPath)
	sp, err := NewSandboxedPlugin(m, manifestDir)
	if err != nil {
		return fmt.Errorf("failed to create sandboxed plugin %s: %w", m.Name, err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.loader.plugins[m.Name]; exists {
		return fmt.Errorf("plugin %q is already registered", m.Name)
	}

	exec := r.createExecutor(sp)
	initResult := exec.Call(context.Background(), PluginRequest{
		Version: EnvelopeVersion,
		ID:      newRequestID(),
		Method:  MethodInit,
		Limits:  DefaultResourceLimits(),
	})
	if initResult.Err != nil {
		r.bus.Emit(LifecyclePayload{
			PluginName: m.Name,
			Event:      EventError,
			Err:        initResult.Err,
		})
	}

	r.loader.plugins[m.Name] = sp
	r.manifests[m.Name] = m
	r.executors[m.Name] = exec
	r.bus.Emit(LifecyclePayload{PluginName: m.Name, Event: EventRegistered})
	r.bus.Emit(LifecyclePayload{PluginName: m.Name, Event: EventInitialized})
	return nil
}

// GetManifest returns the manifest for a registered plugin, if available.
func (r *Registry) GetManifest(name string) (*Manifest, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.manifests[name]
	return m, ok
}

// GetExecutor returns the PluginExecutor for the named plugin, if present.
// This allows callers to perform direct executor operations (e.g. health checks).
func (r *Registry) GetExecutor(name string) (*PluginExecutor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.executors[name]
	return e, ok
}

// Decode uses a named plugin to decode an event, routing the call through the
// plugin's PluginExecutor so timeouts, panic recovery, and quarantine apply.
func (r *Registry) Decode(pluginName string, eventType string, data []byte) (json.RawMessage, error) {
	r.mu.RLock()
	p, ok := r.loader.Get(pluginName)
	exec, hasExec := r.executors[pluginName]
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("plugin %s not found", pluginName)
	}

	if !p.CanDecode(eventType) {
		return nil, fmt.Errorf("plugin %s cannot decode event type %s", pluginName, eventType)
	}

	// Route through executor if available (sandboxed plugin path).
	if hasExec {
		req := PluginRequest{
			Version:   EnvelopeVersion,
			ID:        newRequestID(),
			Method:    MethodDecode,
			EventType: eventType,
			Data:      json.RawMessage(data),
			Limits:    DefaultResourceLimits(),
		}
		res := exec.Call(context.Background(), req)
		if res.Err != nil {
			return nil, fmt.Errorf("plugin %s decode failed: %w", pluginName, res.Err)
		}
		return res.Result, nil
	}

	// Fallback for non-sandboxed plugins loaded via .so (legacy path).
	result, err := p.Decode(data)
	if err != nil {
		return nil, fmt.Errorf("plugin %s decode failed: %w", pluginName, err)
	}
	return result, nil
}

// DecodeWithContext is like Decode but accepts a caller-supplied context so
// cancellation and deadline propagate all the way into the plugin process.
func (r *Registry) DecodeWithContext(ctx context.Context, pluginName string, eventType string, data []byte) (json.RawMessage, error) {
	r.mu.RLock()
	p, ok := r.loader.Get(pluginName)
	exec, hasExec := r.executors[pluginName]
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("plugin %s not found", pluginName)
	}
	if !p.CanDecode(eventType) {
		return nil, fmt.Errorf("plugin %s cannot decode event type %s", pluginName, eventType)
	}
	if !hasExec {
		result, err := p.Decode(data)
		if err != nil {
			return nil, fmt.Errorf("plugin %s decode failed: %w", pluginName, err)
		}
		return result, nil
	}

	req := PluginRequest{
		Version:   EnvelopeVersion,
		ID:        newRequestID(),
		Method:    MethodDecode,
		EventType: eventType,
		Data:      json.RawMessage(data),
		Limits:    DefaultResourceLimits(),
	}
	res := exec.Call(ctx, req)
	if res.Err != nil {
		return nil, fmt.Errorf("plugin %s decode failed: %w", pluginName, res.Err)
	}
	return res.Result, nil
}

// FindAndDecode searches for a capable plugin and decodes the event.
func (r *Registry) FindAndDecode(eventType string, data []byte) (json.RawMessage, string, error) {
	r.mu.RLock()
	p, ok := r.loader.FindForEvent(eventType)
	var exec *PluginExecutor
	if ok {
		exec = r.executors[p.Name()]
	}
	r.mu.RUnlock()

	if !ok {
		return nil, "", fmt.Errorf("no plugin available for event type %s", eventType)
	}

	// Route through executor when available.
	if exec != nil {
		req := PluginRequest{
			Version:   EnvelopeVersion,
			ID:        newRequestID(),
			Method:    MethodDecode,
			EventType: eventType,
			Data:      json.RawMessage(data),
			Limits:    DefaultResourceLimits(),
		}
		res := exec.Call(context.Background(), req)
		if res.Err != nil {
			return nil, "", res.Err
		}
		return res.Result, p.Name(), nil
	}

	result, err := p.Decode(data)
	if err != nil {
		return nil, "", err
	}
	return result, p.Name(), nil
}

// FindAndDecodeWithContext is like FindAndDecode but propagates the caller's ctx
// into the plugin executor so cancellation and deadlines reach the child process.
func (r *Registry) FindAndDecodeWithContext(ctx context.Context, eventType string, data []byte) (json.RawMessage, string, error) {
	r.mu.RLock()
	p, ok := r.loader.FindForEvent(eventType)
	var exec *PluginExecutor
	if ok {
		exec = r.executors[p.Name()]
	}
	r.mu.RUnlock()

	if !ok {
		return nil, "", fmt.Errorf("no plugin available for event type %s", eventType)
	}

	if exec != nil {
		req := PluginRequest{
			Version:   EnvelopeVersion,
			ID:        newRequestID(),
			Method:    MethodDecode,
			EventType: eventType,
			Data:      json.RawMessage(data),
			Limits:    DefaultResourceLimits(),
		}
		res := exec.Call(ctx, req)
		if res.Err != nil {
			return nil, "", res.Err
		}
		return res.Result, p.Name(), nil
	}

	result, err := p.Decode(data)
	if err != nil {
		return nil, "", err
	}
	return result, p.Name(), nil
}

// PluginStatus returns a snapshot of the health and quarantine state for all
// registered sandboxed plugins. Non-sandboxed (.so) plugins are omitted.
func (r *Registry) PluginStatus() []PluginStatusSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var snaps []PluginStatusSnapshot
	for name, exec := range r.executors {
		sp, ok := r.loader.plugins[name].(*SandboxedPlugin)
		if !ok {
			continue
		}
		snap := PluginStatusSnapshot{
			Name:      name,
			Health:    sp.Health().String(),
			Inflight:  exec.Inflight(),
			Closed:    exec.closed.Load(),
		}
		if q := sp.QuarantineInfo(); q != nil {
			snap.QuarantineErr = q.Err.Error()
			snap.QuarantinedAt = q.OccuredAt
		}
		snaps = append(snaps, snap)
	}
	return snaps
}

// ListPlugins returns information about all loaded plugins.
func (r *Registry) ListPlugins() []Metadata {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := r.loader.List()
	metadata := make([]Metadata, 0, len(names))

	for _, name := range names {
		if p, ok := r.loader.Get(name); ok {
			metadata = append(metadata, p.Metadata())
		}
	}

	return metadata
}

// Clear removes all loaded plugins and emits cleanup lifecycle events.
// All plugin executors are closed before plugins are unregistered.
func (r *Registry) Clear() {
	r.mu.Lock()
	names := r.loader.List()

	// Close all executors.
	for _, exec := range r.executors {
		exec.Close()
	}

	r.loader = NewLoader()
	r.cache = make(map[string]json.RawMessage)
	r.manifests = make(map[string]*Manifest)
	r.executors = make(map[string]*PluginExecutor)
	r.mu.Unlock()

	// Emit cleanup events outside the lock.
	for _, name := range names {
		r.bus.Emit(LifecyclePayload{PluginName: name, Event: EventCleanup})
	}
}

// createExecutor builds a PluginExecutor for a SandboxedPlugin.
// Must be called while holding the registry write lock (or during init).
func (r *Registry) createExecutor(sp *SandboxedPlugin) *PluginExecutor {
	return NewPluginExecutor(sp, ExecutorConfig{
		DefaultTimeout: defaultPluginTimeout,
		MaxConcurrent:  MaxConcurrentCalls,
	})
}

// PluginStatusSnapshot is a point-in-time view of a single plugin's state.
type PluginStatusSnapshot struct {
	Name          string    `json:"name"`
	Health        string    `json:"health"`
	Inflight      int64     `json:"inflight"`
	Closed        bool      `json:"closed"`
	QuarantineErr string    `json:"quarantine_error,omitempty"`
	QuarantinedAt time.Time `json:"quarantined_at,omitempty"`
}
