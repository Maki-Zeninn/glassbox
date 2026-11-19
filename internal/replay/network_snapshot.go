// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package replay

// This file implements the canonical network snapshot used to guard replays
// against network configuration drift between capture time and replay time.
//
// Problem: a Stellar transaction is specific to exactly one network. The
// network is identified by its passphrase, not by a human-readable name —
// two networks can share the name "testnet" but use different passphrases,
// making them entirely incompatible for replay. Without capturing the full
// network identity at capture time, a replay can silently produce wrong
// conclusions by simulating a mainnet transaction against a testnet state.
//
// Solution: at capture time the full NetworkSnapshot (name + passphrase +
// protocol version + RPC identity) is serialised into the Registry. On load
// the snapshot is compared against the caller-supplied runtime config; any
// mismatch aborts the replay with a structured error that names the field
// that changed. Intentional cross-network analysis is possible via an
// explicit override flag that is visible in reports.
//
// Serialization design: all fields are sorted deterministically (struct order
// is fixed by the JSON tags) so the SHA-256 of the marshalled snapshot is
// stable across Go versions and OS/arch combinations.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// NetworkSnapshot is the canonical, serialisable record of the Stellar network
// configuration that was active when the Registry was captured.
//
// All fields use their canonical string forms so the snapshot is meaningful
// to a human reading the registry JSON file and unambiguous to a tool parsing
// it.  The passphrase is the authoritative identity for Stellar networks; the
// name field is human-readable convenience only.
type NetworkSnapshot struct {
	// Name is the human-readable network identifier (e.g. "testnet", "mainnet",
	// "futurenet", or a custom name supplied by the operator).
	Name string `json:"name"`
	// Passphrase is the Stellar network passphrase. This is the authoritative
	// identity — two networks with the same Name but different Passphrases are
	// incompatible for replay.
	Passphrase string `json:"passphrase"`
	// ProtocolVersion is the Soroban protocol version active at capture time.
	// A replay against a different protocol version may produce different
	// resource usage and event shapes.
	ProtocolVersion uint32 `json:"protocol_version,omitempty"`
	// RPCURL is the Soroban RPC endpoint URL used for capture. Recorded for
	// informational purposes; it is NOT compared during compatibility checks
	// because the same logical network can have many valid RPC endpoints.
	RPCURL string `json:"rpc_url,omitempty"`
}

// NewNetworkSnapshot constructs a NetworkSnapshot from discrete fields.
// name and passphrase are required; the rest are informational.
func NewNetworkSnapshot(name, passphrase, rpcURL string, protocolVersion uint32) *NetworkSnapshot {
	return &NetworkSnapshot{
		Name:            strings.TrimSpace(name),
		Passphrase:      passphrase, // preserve exactly — passphrase is content-sensitive
		ProtocolVersion: protocolVersion,
		RPCURL:          strings.TrimSpace(rpcURL),
	}
}

// Hash returns the hex-encoded SHA-256 of the deterministic JSON serialisation
// of the snapshot. Two snapshots with identical Name, Passphrase, and
// ProtocolVersion produce the same hash regardless of RPCURL, because RPCURL
// is excluded from the hash input (it is informational only).
//
// The hash is used by the Registry to detect configuration changes between
// capture and replay without requiring a field-by-field comparison.
func (s *NetworkSnapshot) Hash() string {
	if s == nil {
		return ""
	}
	// Hash only the identity fields; RPCURL is excluded deliberately.
	type hashInput struct {
		Name            string `json:"name"`
		Passphrase      string `json:"passphrase"`
		ProtocolVersion uint32 `json:"protocol_version,omitempty"`
	}
	h := hashInput{
		Name:            s.Name,
		Passphrase:      s.Passphrase,
		ProtocolVersion: s.ProtocolVersion,
	}
	data, err := json.Marshal(h)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Compatible returns a NetworkCompatibilityResult describing whether other
// matches this snapshot for replay purposes. The comparison checks:
//
//  1. Passphrase — must match exactly (authoritative network identity).
//  2. Name       — must match (human-readable identity guard against trivial
//     mistakes; can be overridden by the caller via Override).
//  3. ProtocolVersion — must match when both are non-zero (a zero value in
//     either snapshot means "not recorded" and is skipped).
//
// RPCURL is never compared because the same network is reachable from many
// endpoints.
func (s *NetworkSnapshot) Compatible(other *NetworkSnapshot) NetworkCompatibilityResult {
	result := NetworkCompatibilityResult{Snapshot: s, ReplayConfig: other}

	if s == nil || other == nil {
		result.Mismatches = append(result.Mismatches, NetworkFieldMismatch{
			Field:       "snapshot",
			Captured:    snapshotStr(s),
			ReplayCfg:   snapshotStr(other),
			Description: "one or both network snapshots are nil; cannot validate compatibility",
		})
		return result
	}

	// 1. Passphrase — authoritative identity.
	if s.Passphrase != other.Passphrase {
		result.Mismatches = append(result.Mismatches, NetworkFieldMismatch{
			Field:       "passphrase",
			Captured:    s.Passphrase,
			ReplayCfg:   other.Passphrase,
			Description: fmt.Sprintf("network passphrase mismatch: captured %q, replay config has %q", s.Passphrase, other.Passphrase),
		})
	}

	// 2. Name — human-readable guard.
	if s.Name != other.Name {
		result.Mismatches = append(result.Mismatches, NetworkFieldMismatch{
			Field:       "name",
			Captured:    s.Name,
			ReplayCfg:   other.Name,
			Description: fmt.Sprintf("network name mismatch: captured %q, replay config has %q", s.Name, other.Name),
		})
	}

	// 3. ProtocolVersion — only when both are non-zero.
	if s.ProtocolVersion != 0 && other.ProtocolVersion != 0 && s.ProtocolVersion != other.ProtocolVersion {
		result.Mismatches = append(result.Mismatches, NetworkFieldMismatch{
			Field:       "protocol_version",
			Captured:    fmt.Sprintf("%d", s.ProtocolVersion),
			ReplayCfg:   fmt.Sprintf("%d", other.ProtocolVersion),
			Description: fmt.Sprintf("protocol version mismatch: captured %d, replay config has %d", s.ProtocolVersion, other.ProtocolVersion),
		})
	}

	return result
}

// String returns a compact one-line description of the snapshot for logs.
func (s *NetworkSnapshot) String() string {
	if s == nil {
		return "<nil>"
	}
	v := ""
	if s.ProtocolVersion > 0 {
		v = fmt.Sprintf(" proto=%d", s.ProtocolVersion)
	}
	return fmt.Sprintf("%s(%s)%s", s.Name, shortPassphrase(s.Passphrase), v)
}

// ── Compatibility result ──────────────────────────────────────────────────────

// NetworkFieldMismatch describes one detected incompatibility between the
// captured snapshot and the replay configuration.
type NetworkFieldMismatch struct {
	// Field is the stable name of the mismatched field.
	Field string
	// Captured is the value recorded at capture time.
	Captured string
	// ReplayCfg is the value present in the current replay configuration.
	ReplayCfg string
	// Description is a one-sentence explanation suitable for user output.
	Description string
}

// NetworkCompatibilityResult is the outcome of NetworkSnapshot.Compatible.
type NetworkCompatibilityResult struct {
	// Snapshot is the captured network configuration (from the Registry).
	Snapshot *NetworkSnapshot
	// ReplayConfig is the runtime network configuration being checked.
	ReplayConfig *NetworkSnapshot
	// Mismatches lists every detected incompatibility.
	Mismatches []NetworkFieldMismatch
}

// Compatible returns true when there are no mismatches.
func (r NetworkCompatibilityResult) Compatible() bool {
	return len(r.Mismatches) == 0
}

// ── Mismatch error ────────────────────────────────────────────────────────────

// NetworkSnapshotMismatchError is returned by Registry.ValidateNetworkSnapshot
// when the stored snapshot is incompatible with the replay configuration and
// the caller has not set OverrideCrossNetwork.
type NetworkSnapshotMismatchError struct {
	Result NetworkCompatibilityResult
}

func (e *NetworkSnapshotMismatchError) Error() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(
		"network snapshot mismatch: registry was captured on %s but replay config is %s (%d field(s) differ):\n",
		snapshotStr(e.Result.Snapshot),
		snapshotStr(e.Result.ReplayConfig),
		len(e.Result.Mismatches),
	))
	for i, m := range e.Result.Mismatches {
		sb.WriteString(fmt.Sprintf("  %d. [%s] %s\n", i+1, m.Field, m.Description))
	}
	sb.WriteString("\nRemediation:\n")
	sb.WriteString("  • Re-run the debug command against the correct network to regenerate the registry.\n")
	sb.WriteString("  • To compare across networks intentionally, set OverrideCrossNetwork=true in\n")
	sb.WriteString("    ValidateNetworkSnapshotOptions — this override will be visible in the replay report.\n")
	return sb.String()
}

// IsNetworkSnapshotMismatch reports whether err is a *NetworkSnapshotMismatchError.
func IsNetworkSnapshotMismatch(err error) bool {
	_, ok := err.(*NetworkSnapshotMismatchError)
	return ok
}

// AsNetworkSnapshotMismatch returns the *NetworkSnapshotMismatchError if err is
// one, or nil.
func AsNetworkSnapshotMismatch(err error) *NetworkSnapshotMismatchError {
	if e, ok := err.(*NetworkSnapshotMismatchError); ok {
		return e
	}
	return nil
}

// ── Pre-replay validation ─────────────────────────────────────────────────────

// ValidateNetworkSnapshotOptions configures the pre-replay network validation.
type ValidateNetworkSnapshotOptions struct {
	// OverrideCrossNetwork disables the hard failure so mismatches are recorded
	// but do not prevent replay. Use only for intentional cross-network analysis.
	// When true the NetworkOverrideActive flag is set in the returned report so
	// the override is always visible to consumers and cannot be silently ignored.
	OverrideCrossNetwork bool
}

// NetworkValidationReport is the output of Registry.ValidateNetworkSnapshot.
type NetworkValidationReport struct {
	// Compatible is true when the registry snapshot matches the replay config.
	Compatible bool
	// Result holds the full per-field comparison.
	Result NetworkCompatibilityResult
	// NetworkOverrideActive is true when OverrideCrossNetwork was set and
	// mismatches exist. Callers must surface this prominently in reports.
	NetworkOverrideActive bool
}

// ValidateNetworkSnapshot compares the registry's captured NetworkSnapshot
// against runtimeCfg and returns a NetworkValidationReport.
//
// Behaviour:
//   - When the registry has no snapshot (legacy file pre-dating this feature),
//     validation passes with a note so old files stay usable.
//   - When runtimeCfg is nil, the registry snapshot is logged as informational
//     and validation passes (caller did not supply a config to compare).
//   - Mismatches produce a *NetworkSnapshotMismatchError unless
//     opts.OverrideCrossNetwork is true, in which case the error is nil but
//     NetworkValidationReport.NetworkOverrideActive is true.
func (r *Registry) ValidateNetworkSnapshot(
	runtimeCfg *NetworkSnapshot,
	opts *ValidateNetworkSnapshotOptions,
) (*NetworkValidationReport, error) {
	if opts == nil {
		opts = &ValidateNetworkSnapshotOptions{}
	}

	report := &NetworkValidationReport{}

	// No snapshot in registry → legacy file; pass with no error.
	if r.NetworkSnapshot == nil {
		report.Compatible = true
		return report, nil
	}

	// No runtime config supplied → informational pass.
	if runtimeCfg == nil {
		report.Compatible = true
		report.Result = NetworkCompatibilityResult{
			Snapshot:     r.NetworkSnapshot,
			ReplayConfig: nil,
		}
		return report, nil
	}

	result := r.NetworkSnapshot.Compatible(runtimeCfg)
	report.Result = result
	report.Compatible = result.Compatible()

	if !report.Compatible {
		report.NetworkOverrideActive = opts.OverrideCrossNetwork
		if !opts.OverrideCrossNetwork {
			return report, &NetworkSnapshotMismatchError{Result: result}
		}
		// Override active — return nil error but mark the override so callers
		// must log a prominent warning.
	}

	return report, nil
}

// ── Internal helpers ──────────────────────────────────────────────────────────

func snapshotStr(s *NetworkSnapshot) string {
	if s == nil {
		return "<nil>"
	}
	return s.String()
}

// shortPassphrase returns the first 20 characters of a passphrase followed by
// "…" so it is recognisable in logs without leaking the full value.
func shortPassphrase(p string) string {
	const maxLen = 20
	if len(p) <= maxLen {
		return p
	}
	return p[:maxLen] + "…"
}
