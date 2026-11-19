// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package metrics

import (
	"fmt"
	"net/url"
	"strings"
)

// AllowedNetworkValues is the complete set of permitted values for the
// "network" label.  Any value outside this set indicates a cardinality or
// configuration bug and must be rejected before the metric is recorded.
var AllowedNetworkValues = map[string]bool{
	"testnet":    true,
	"mainnet":    true,
	"futurenet":  true,
	"standalone": true,
}

// AllowedStatusValues is the complete set of permitted values for the
// "status" label.
var AllowedStatusValues = map[string]bool{
	"success": true,
	"error":   true,
	"timeout": true,
	"skipped": true,
}

// MaxNodeAddressCardinality is the hard limit on unique node_address label
// values tracked by the metrics registry.  Raw token-per-request URLs, UUIDs,
// or transaction hashes as label values must be stripped before recording;
// exceeding this limit would cause unbounded memory growth.
const MaxNodeAddressCardinality = 50

// SanitizeNodeAddress strips query-string tokens, credentials, and fragment
// identifiers from a raw RPC URL before it is used as a Prometheus label
// value.  This prevents high-cardinality token-per-request labels from
// poisoning the metrics registry.
//
// Examples:
//
//	"https://rpc.example.com?token=abc123"      → "https://rpc.example.com"
//	"https://user:pass@rpc.example.com/v1"      → "https://rpc.example.com/v1"
//	"https://rpc.example.com"                   → "https://rpc.example.com"
func SanitizeNodeAddress(rawURL string) string {
	if rawURL == "" {
		return rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		// Unparseable: strip everything from ? onward as a best-effort.
		if idx := strings.IndexByte(rawURL, '?'); idx >= 0 {
			return rawURL[:idx]
		}
		return rawURL
	}
	u.RawQuery = ""
	u.Fragment = ""
	u.User = nil
	return u.String()
}

// ValidateNetworkLabel returns a non-nil error when network is not in
// AllowedNetworkValues.  Call this before recording any metric with a
// network label to enforce the cardinality policy.
func ValidateNetworkLabel(network string) error {
	if !AllowedNetworkValues[network] {
		return fmt.Errorf("metric label network=%q is not in the allowlist; allowed: testnet, mainnet, futurenet, standalone", network)
	}
	return nil
}

// ValidateStatusLabel returns a non-nil error when status is not in
// AllowedStatusValues.
func ValidateStatusLabel(status string) error {
	if !AllowedStatusValues[status] {
		return fmt.Errorf("metric label status=%q is not in the allowlist; allowed: success, error, timeout, skipped", status)
	}
	return nil
}

// looksHighCardinality returns true for label values that are likely to
// produce runaway cardinality: raw UUIDs, hex digests, or long path strings.
// Use this in tests to detect accidental high-cardinality label injection.
func looksHighCardinality(v string) bool {
	// UUID-like: 8-4-4-4-12 hex groups separated by hyphens
	if len(v) == 36 && v[8] == '-' && v[13] == '-' && v[18] == '-' && v[23] == '-' {
		return true
	}
	// Raw hex digest (SHA-256 = 64 chars, SHA-1 = 40 chars, etc.)
	if len(v) >= 40 && isAllHex(v) {
		return true
	}
	// Path-like: contains directory separators and is longer than a hostname
	if len(v) > 20 && strings.ContainsAny(v, "/\\") {
		if !strings.HasPrefix(v, "http://") && !strings.HasPrefix(v, "https://") {
			return true
		}
	}
	return false
}

func isAllHex(s string) bool {
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

// isValidMetricName returns true for names that follow the project convention:
// snake_case characters only and ending with a recognised units suffix.
func isValidMetricName(name string) bool {
	if name == "" {
		return false
	}
	for _, ch := range name {
		if !((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '_') {
			return false
		}
	}
	for _, suffix := range []string{
		"_total",
		"_seconds",
		"_bytes",
		"_info",
		"_ratio",
		"_timestamp_seconds",
	} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}
