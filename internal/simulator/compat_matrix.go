// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package simulator

// compat_matrix.go — Protocol-version compatibility matrix.
// Issue #536: Add protocol-version compatibility matrix tests
//
// Defines supported protocol versions, capabilities, expected limitations,
// and fixtures for each version. Provides table-driven test infrastructure
// and capability metadata.

import (
	"encoding/json"
	"fmt"
	"sort"
)

// Capability represents a feature and its support level for a protocol version.
type Capability struct {
	Name           string `json:"name"`
	Supported      bool   `json:"supported"`
	Limitation     string `json:"limitation,omitempty"` // e.g. "max 65536 bytes"
	MinProtocol    uint32 `json:"min_protocol"`
	DeprecatedIn   uint32 `json:"deprecated_in,omitempty"` // 0 = not deprecated
}

// ProtocolCapabilities maps a protocol version to its set of capabilities.
type ProtocolCapabilities struct {
	Version       uint32       `json:"version"`
	Name          string       `json:"name"`
	Capabilities []Capability `json:"capabilities"`
}

// CompatibilityMatrix is the full matrix of protocol versions and their capabilities.
var CompatibilityMatrix = []ProtocolCapabilities{
	{
		Version: 20,
		Name:    "Soroban Protocol 20",
		Capabilities: []Capability{
			{Name: "invoke_contract", Supported: true, MinProtocol: 20},
			{Name: "create_contract", Supported: true, MinProtocol: 20},
			{Name: "extend_contract", Supported: false, MinProtocol: 21, Limitation: "Not available in protocol 20"},
			{Name: "max_contract_size", Supported: true, MinProtocol: 20, Limitation: "65536 bytes"},
			{Name: "max_contract_data_size", Supported: true, MinProtocol: 20, Limitation: "1024000 bytes"},
			{Name: "max_instruction_limit", Supported: true, MinProtocol: 20, Limitation: "100000000"},
			{Name: "enhanced_metering", Supported: false, MinProtocol: 21, Limitation: "Not available in protocol 20"},
			{Name: "host_function_capture", Supported: true, MinProtocol: 20},
			{Name: "resource_reporting", Supported: true, MinProtocol: 20},
			{Name: "deterministic_mode", Supported: true, MinProtocol: 20},
			{Name: "trace_streaming", Supported: true, MinProtocol: 20},
		},
	},
	{
		Version: 21,
		Name:    "Soroban Protocol 21",
		Capabilities: []Capability{
			{Name: "invoke_contract", Supported: true, MinProtocol: 20},
			{Name: "create_contract", Supported: true, MinProtocol: 20},
			{Name: "extend_contract", Supported: true, MinProtocol: 21},
			{Name: "max_contract_size", Supported: true, MinProtocol: 20, Limitation: "65536 bytes"},
			{Name: "max_contract_data_size", Supported: true, MinProtocol: 20, Limitation: "2048000 bytes"},
			{Name: "max_instruction_limit", Supported: true, MinProtocol: 20, Limitation: "150000000"},
			{Name: "enhanced_metering", Supported: true, MinProtocol: 21},
			{Name: "host_function_capture", Supported: true, MinProtocol: 20},
			{Name: "resource_reporting", Supported: true, MinProtocol: 20},
			{Name: "deterministic_mode", Supported: true, MinProtocol: 20},
			{Name: "trace_streaming", Supported: true, MinProtocol: 20},
		},
	},
}

// SupportedProtocolVersions returns all declared supported versions.
func SupportedProtocolVersions() []uint32 {
	versions := make([]uint32, 0, len(CompatibilityMatrix))
	for _, pc := range CompatibilityMatrix {
		versions = append(versions, pc.Version)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })
	return versions
}

// IsProtocolSupported returns true if the version is in the compatibility matrix.
func IsProtocolSupported(version uint32) bool {
	for _, pc := range CompatibilityMatrix {
		if pc.Version == version {
			return true
		}
	}
	return false
}

// GetCapabilities returns the capabilities for a specific protocol version.
func GetCapabilities(version uint32) (*ProtocolCapabilities, error) {
	for _, pc := range CompatibilityMatrix {
		if pc.Version == version {
			return &pc, nil
		}
	}
	return nil, fmt.Errorf("unsupported protocol version %d. Supported versions: %v",
		version, SupportedProtocolVersions())
}

// HasCapability checks if a specific capability is supported for a protocol version.
func HasCapability(version uint32, capabilityName string) (bool, error) {
	pc, err := GetCapabilities(version)
	if err != nil {
		return false, err
	}
	for _, cap := range pc.Capabilities {
		if cap.Name == capabilityName {
			return cap.Supported, nil
		}
	}
	return false, fmt.Errorf("capability %q not found in protocol %d", capabilityName, version)
}

// GetCapabilityLimitation returns the limitation string for a capability, if any.
func GetCapabilityLimitation(version uint32, capabilityName string) (string, error) {
	pc, err := GetCapabilities(version)
	if err != nil {
		return "", err
	}
	for _, cap := range pc.Capabilities {
		if cap.Name == capabilityName {
			if cap.Supported {
				return cap.Limitation, nil
			}
			return "", fmt.Errorf("capability %q is not supported in protocol %d: %s",
				capabilityName, version, cap.Limitation)
		}
	}
	return "", fmt.Errorf("capability %q not found in protocol %d", capabilityName, version)
}

// CompatMatrixToJSON exports the full compatibility matrix as JSON.
func CompatMatrixToJSON() ([]byte, error) {
	return json.MarshalIndent(CompatibilityMatrix, "", "  ")
}

// CompatMatrixToMarkdown exports the compatibility matrix as a Markdown table.
func CompatMatrixToMarkdown() string {
	// Collect all capability names
	capNames := make(map[string]bool)
	for _, pc := range CompatibilityMatrix {
		for _, cap := range pc.Capabilities {
			capNames[cap.Name] = true
		}
	}
	sortedNames := make([]string, 0, len(capNames))
	for name := range capNames {
		sortedNames = append(sortedNames, name)
	}
	sort.Strings(sortedNames)

	versions := SupportedProtocolVersions()

	// Build table header
	header := "| Capability |"
	separator := "|------------|"
	for _, v := range versions {
		header += fmt.Sprintf(" Protocol %d |", v)
		separator += "------------|"
	}

	var sb string
	sb = header + "\n" + separator + "\n"

	// Build rows
	for _, name := range sortedNames {
		row := fmt.Sprintf("| %s |", name)
		for _, v := range versions {
			pc, _ := GetCapabilities(v)
			if pc == nil {
				row += " N/A |"
				continue
			}
			for _, cap := range pc.Capabilities {
				if cap.Name == name {
					if cap.Supported {
						if cap.Limitation != "" {
							row += fmt.Sprintf(" \u2705 (%s) |", cap.Limitation)
						} else {
							row += " \u2705 |"
						}
					} else {
						if cap.Limitation != "" {
							row += fmt.Sprintf(" \u274c (%s) |", cap.Limitation)
						} else {
							row += " \u274c |"
						}
					}
					break
				}
			}
		}
		sb += row + "\n"
	}

	return sb
}

// UnavailableNetworkError distinguishes between "protocol doesn't support this"
// and "network data is unavailable for this version".
type UnavailableNetworkError struct {
	ProtocolVersion uint32
	Reason          string
}

func (e *UnavailableNetworkError) Error() string {
	return fmt.Sprintf("network data unavailable for protocol %d: %s", e.ProtocolVersion, e.Reason)
}
