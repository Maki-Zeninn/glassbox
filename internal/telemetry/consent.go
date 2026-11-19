// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package telemetry provides opt-in telemetry consent management alongside the
// OpenTelemetry integration. The consent store is a single JSON file at
// ~/.Glassbox/telemetry_consent.json. An environment variable override
// (GLASSBOX_TELEMETRY) always takes precedence over the persisted state.
package telemetry

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// consentFileName is the filename within the Glassbox config directory that
// stores the user's durable telemetry consent choice.
const consentFileName = "telemetry_consent.json"

// consentFilePerms is the permission mask applied to the consent file.
// 0600 ensures the file is readable and writable only by the owning user.
const consentFilePerms os.FileMode = 0600

// consentDirPerms is the permission mask for the config directory when it must
// be created. 0700 is consistent with the rest of the Glassbox config tree.
const consentDirPerms os.FileMode = 0700

// ConsentState represents the persisted telemetry consent record.
type ConsentState struct {
	// Enabled is the user's explicit choice: true = opted in, false = opted out.
	Enabled bool `json:"enabled"`
	// UpdatedAt is the RFC3339 timestamp of the last change.
	UpdatedAt string `json:"updated_at"`
}

// ConsentSource describes how the effective telemetry decision was reached.
type ConsentSource int

const (
	// ConsentSourceEnv means GLASSBOX_TELEMETRY env var is set and takes precedence.
	ConsentSourceEnv ConsentSource = iota
	// ConsentSourceFile means the consent file was read successfully.
	ConsentSourceFile
	// ConsentSourceDefault means neither env nor file was set; telemetry is off.
	ConsentSourceDefault
)

// EffectiveConsent is the resolved telemetry on/off decision together with
// information about how it was decided.
type EffectiveConsent struct {
	// Enabled is the final resolved value.
	Enabled bool
	// Source indicates what determined Enabled.
	Source ConsentSource
	// EnvValue is the raw GLASSBOX_TELEMETRY value when Source == ConsentSourceEnv.
	EnvValue string
}

// consentFilePath returns the canonical path of the consent file:
// ~/.Glassbox/telemetry_consent.json
func consentFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".Glassbox", consentFileName), nil
}

// ReadConsent loads the consent file and returns its state. If the file does
// not exist, ReadConsent returns a zero ConsentState (disabled) and nil error.
// If the file exists but is malformed, an error is returned.
func ReadConsent() (ConsentState, error) {
	path, err := consentFilePath()
	if err != nil {
		return ConsentState{}, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// No consent file → default disabled, no error.
			return ConsentState{}, nil
		}
		return ConsentState{}, fmt.Errorf("failed to read consent file %s: %w", path, err)
	}

	var state ConsentState
	if err := json.Unmarshal(data, &state); err != nil {
		return ConsentState{}, fmt.Errorf("consent file %s is malformed: %w", path, err)
	}
	return state, nil
}

// WriteConsent persists enabled to the consent file with the current timestamp.
// The file and its parent directory are created if they do not exist.
// File permissions are set to 0600.
func WriteConsent(enabled bool) error {
	path, err := consentFilePath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), consentDirPerms); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	state := ConsentState{
		Enabled:   enabled,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal consent state: %w", err)
	}

	if err := os.WriteFile(path, data, consentFilePerms); err != nil {
		return fmt.Errorf("failed to write consent file %s: %w", path, err)
	}
	return nil
}

// ResolveConsent determines the effective telemetry enabled state using the
// following precedence (highest first):
//
//  1. GLASSBOX_TELEMETRY environment variable (if set to a parseable boolean)
//  2. Consent file (~/.Glassbox/telemetry_consent.json)
//  3. Default: disabled
//
// Errors reading or parsing the consent file are silently swallowed and treated
// as "default disabled" so that a corrupted file never blocks CLI startup.
func ResolveConsent() EffectiveConsent {
	// 1. Environment override.
	if envVal := os.Getenv("GLASSBOX_TELEMETRY"); envVal != "" {
		if b, err := parseBoolLoose(envVal); err == nil {
			return EffectiveConsent{
				Enabled:  b,
				Source:   ConsentSourceEnv,
				EnvValue: envVal,
			}
		}
	}

	// 2. Consent file.
	state, err := ReadConsent()
	if err == nil {
		// File was present and parsed successfully — but only treat it as
		// ConsentSourceFile when the file actually existed (empty UpdatedAt
		// signals a synthesised default).
		if state.UpdatedAt != "" {
			return EffectiveConsent{
				Enabled: state.Enabled,
				Source:  ConsentSourceFile,
			}
		}
	}
	// err != nil or file absent → fall through to default.

	// 3. Default: disabled.
	return EffectiveConsent{
		Enabled: false,
		Source:  ConsentSourceDefault,
	}
}

// ConsentFilePath returns the path of the consent file for display/diagnostic
// purposes. Returns an empty string if the home directory cannot be determined.
func ConsentFilePath() string {
	p, err := consentFilePath()
	if err != nil {
		return ""
	}
	return p
}

// parseBoolLoose accepts common boolean string representations.
func parseBoolLoose(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	}
	return false, fmt.Errorf("unrecognised boolean value %q", s)
}
