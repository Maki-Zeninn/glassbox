// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package simulator

// sim_environment.go — Deterministic simulator time and randomness controls.
// Issue #534: Add deterministic simulator time and randomness controls
//
// Exposes explicit simulation timestamp and deterministic random seed settings,
// records them in session metadata, and keeps production-like defaults separate
// from reproducible mode.

import (
	"encoding/json"
	"fmt"
)

// SimEnvironment controls deterministic execution parameters.
// When set, replays with the same controls produce identical traces.
type SimEnvironment struct {
	// Timestamp is the fixed ledger timestamp (Unix seconds) for the simulation.
	// 0 = use live clock (production-like, non-deterministic).
	Timestamp int64 `json:"timestamp,omitempty"`

	// RandomSeed is the deterministic seed for any random operations.
	// 0 = use system entropy (non-deterministic).
	RandomSeed uint64 `json:"random_seed,omitempty"`

	// LedgerSequence is the fixed ledger sequence number.
	// 0 = use live sequence.
	LedgerSequence uint32 `json:"ledger_sequence,omitempty"`

	// DeterministicMode enables full reproducibility (timestamp + seed + ledger).
	DeterministicMode bool `json:"deterministic_mode"`

	// MinCloseTime is the minimum ledger close time for time validation.
	MinCloseTime int64 `json:"min_close_time,omitempty"`
	// MaxCloseTime is the maximum ledger close time.
	MaxCloseTime int64 `json:"max_close_time,omitempty"`
}

// DefaultSimEnvironment returns production-like defaults (non-deterministic).
func DefaultSimEnvironment() SimEnvironment {
	return SimEnvironment{
		DeterministicMode: false,
	}
}

// ReproducibleSimEnvironment returns a deterministic environment with fixed
// timestamp and seed for reproducible debugging.
func ReproducibleSimEnvironment(timestamp int64, seed uint64) SimEnvironment {
	return SimEnvironment{
		Timestamp:         timestamp,
		RandomSeed:        seed,
		LedgerSequence:    1,
		DeterministicMode:  true,
		MinCloseTime:       timestamp,
		MaxCloseTime:       timestamp + 5,
	}
}

// Validate checks that the environment configuration is well-formed.
func (env *SimEnvironment) Validate() error {
	if env == nil {
		return nil
	}

	if env.DeterministicMode {
		if env.Timestamp == 0 {
			return fmt.Errorf("deterministic mode requires a non-zero timestamp")
		}
		if env.RandomSeed == 0 {
			return fmt.Errorf("deterministic mode requires a non-zero random_seed")
		}
	}

	if env.MinCloseTime > 0 && env.MaxCloseTime > 0 && env.MinCloseTime > env.MaxCloseTime {
		return fmt.Errorf("min_close_time (%d) cannot exceed max_close_time (%d)",
			env.MinCloseTime, env.MaxCloseTime)
	}

	if env.Timestamp > 0 && env.MaxCloseTime > 0 && env.Timestamp > env.MaxCloseTime {
		return fmt.Errorf("timestamp (%d) exceeds max_close_time (%d)",
			env.Timestamp, env.MaxCloseTime)
	}

	return nil
}

// ApplyToRequest applies the simulation environment settings to a SimulationRequest.
// This threads the configuration from Go through IPC into the Rust host setup.
func (env *SimEnvironment) ApplyToRequest(req *SimulationRequest) {
	if env == nil || req == nil {
		return
	}

	if env.Timestamp > 0 {
		req.Timestamp = env.Timestamp
	}
	if env.LedgerSequence > 0 {
		req.LedgerSequence = env.LedgerSequence
	}
}

// ToSessionMetadata returns the environment as a metadata map for session persistence.
func (env *SimEnvironment) ToSessionMetadata() map[string]string {
	if env == nil {
		return nil
	}
	meta := map[string]string{
		"deterministic_mode": fmt.Sprintf("%v", env.DeterministicMode),
	}
	if env.Timestamp > 0 {
		meta["timestamp"] = fmt.Sprintf("%d", env.Timestamp)
	}
	if env.RandomSeed > 0 {
		meta["random_seed"] = fmt.Sprintf("%d", env.RandomSeed)
	}
	if env.LedgerSequence > 0 {
		meta["ledger_sequence"] = fmt.Sprintf("%d", env.LedgerSequence)
	}
	if env.MinCloseTime > 0 {
		meta["min_close_time"] = fmt.Sprintf("%d", env.MinCloseTime)
	}
	if env.MaxCloseTime > 0 {
		meta["max_close_time"] = fmt.Sprintf("%d", env.MaxCloseTime)
	}
	return meta
}

// FromSessionMetadata reconstructs a SimEnvironment from session metadata.
func FromSessionMetadata(meta map[string]string) SimEnvironment {
	env := SimEnvironment{}
	if v, ok := meta["deterministic_mode"]; ok {
		env.DeterministicMode = v == "true"
	}
	if v, ok := meta["timestamp"]; ok {
		fmt.Sscanf(v, "%d", &env.Timestamp)
	}
	if v, ok := meta["random_seed"]; ok {
		fmt.Sscanf(v, "%d", &env.RandomSeed)
	}
	if v, ok := meta["ledger_sequence"]; ok {
		fmt.Sscanf(v, "%d", &env.LedgerSequence)
	}
	if v, ok := meta["min_close_time"]; ok {
		fmt.Sscanf(v, "%d", &env.MinCloseTime)
	}
	if v, ok := meta["max_close_time"]; ok {
		fmt.Sscanf(v, "%d", &env.MaxCloseTime)
	}
	return env
}

// ToJSON serializes the environment configuration.
func (env *SimEnvironment) ToJSON() ([]byte, error) {
	return json.MarshalIndent(env, "", "  ")
}

// Fingerprint produces a short hash for comparing environments.
// Two environments with the same fingerprint produce identical traces.
func (env *SimEnvironment) Fingerprint() string {
	if env == nil {
		return "default"
	}
	return fmt.Sprintf("ts%d_rs%d_ls%d",
		env.Timestamp, env.RandomSeed, env.LedgerSequence)
}

// IsDeterministic reports whether the environment is fully deterministic.
func (env *SimEnvironment) IsDeterministic() bool {
	return env != nil && env.DeterministicMode && env.Timestamp > 0 && env.RandomSeed > 0
}
