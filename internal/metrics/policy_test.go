// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package metrics

import (
	"strings"
	"testing"
)

// ── SanitizeNodeAddress ───────────────────────────────────────────────────────

func TestSanitizeNodeAddress_StripQueryToken(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"https://rpc.example.com?token=abc123", "https://rpc.example.com"},
		{"https://rpc.example.com/v1?key=secret&x=1", "https://rpc.example.com/v1"},
		{"https://rpc.example.com", "https://rpc.example.com"},
		{"", ""},
		{"https://user:pass@rpc.example.com/v1", "https://rpc.example.com/v1"},
		{"https://rpc.example.com#fragment", "https://rpc.example.com"},
	}
	for _, tc := range cases {
		got := SanitizeNodeAddress(tc.in)
		if got != tc.want {
			t.Errorf("SanitizeNodeAddress(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSanitizeNodeAddress_TokenInQuery_NotLeaked(t *testing.T) {
	rawToken := "supersecrettoken123"
	in := "https://rpc.example.com?auth=" + rawToken
	got := SanitizeNodeAddress(in)
	if strings.Contains(got, rawToken) {
		t.Errorf("sanitized label must not contain token; got %q", got)
	}
}

// ── ValidateNetworkLabel ──────────────────────────────────────────────────────

func TestValidateNetworkLabel_AllowedValues(t *testing.T) {
	for net := range AllowedNetworkValues {
		if err := ValidateNetworkLabel(net); err != nil {
			t.Errorf("allowed network %q was rejected: %v", net, err)
		}
	}
}

func TestValidateNetworkLabel_UnknownRejected(t *testing.T) {
	unknown := []string{"devnet", "localnet", "", "MAINNET", "Testnet"}
	for _, net := range unknown {
		if err := ValidateNetworkLabel(net); err == nil {
			t.Errorf("unknown network %q should be rejected", net)
		}
	}
}

// ── ValidateStatusLabel ───────────────────────────────────────────────────────

func TestValidateStatusLabel_AllowedValues(t *testing.T) {
	for s := range AllowedStatusValues {
		if err := ValidateStatusLabel(s); err != nil {
			t.Errorf("allowed status %q was rejected: %v", s, err)
		}
	}
}

func TestValidateStatusLabel_UnknownRejected(t *testing.T) {
	unknown := []string{"ok", "fail", "pending", "", "SUCCESS"}
	for _, s := range unknown {
		if err := ValidateStatusLabel(s); err == nil {
			t.Errorf("unknown status %q should be rejected", s)
		}
	}
}

// ── looksHighCardinality ──────────────────────────────────────────────────────

func TestLooksHighCardinality_UUID(t *testing.T) {
	if !looksHighCardinality("550e8400-e29b-41d4-a716-446655440000") {
		t.Error("UUID (8-4-4-4-12) should look high-cardinality")
	}
}

func TestLooksHighCardinality_SHA256(t *testing.T) {
	hash := strings.Repeat("a", 64)
	if !looksHighCardinality(hash) {
		t.Error("64-char hex string (SHA-256) should look high-cardinality")
	}
}

func TestLooksHighCardinality_SHA1(t *testing.T) {
	hash := strings.Repeat("b", 40)
	if !looksHighCardinality(hash) {
		t.Error("40-char hex string (SHA-1) should look high-cardinality")
	}
}

func TestLooksHighCardinality_LongPath(t *testing.T) {
	if !looksHighCardinality("/very/long/file/system/path/that/exceeds/twenty/chars") {
		t.Error("long path string should look high-cardinality")
	}
}

func TestLooksHighCardinality_SafeValues(t *testing.T) {
	safe := []string{"testnet", "mainnet", "success", "error", "https://rpc.example.com"}
	for _, v := range safe {
		if looksHighCardinality(v) {
			t.Errorf("%q should NOT look high-cardinality", v)
		}
	}
}

// ── isValidMetricName ─────────────────────────────────────────────────────────

func TestMetricNamingConvention_RegisteredMetrics(t *testing.T) {
	// All metric names exported by this package must follow the convention.
	names := []string{
		"remote_node_last_response_timestamp_seconds",
		"remote_node_response_total",
		"remote_node_response_duration_seconds",
		"simulation_execution_total",
	}
	for _, name := range names {
		if !isValidMetricName(name) {
			t.Errorf("metric %q violates the naming convention (must be snake_case with a units suffix)", name)
		}
	}
}

func TestMetricNamingConvention_InvalidNames(t *testing.T) {
	bad := []string{
		"camelCaseName",
		"metric-with-hyphens",
		"metric.with.dots",
		"no_units_suffix",
		"UPPERCASE_METRIC_TOTAL",
		"",
	}
	for _, name := range bad {
		if isValidMetricName(name) {
			t.Errorf("invalid metric name %q should fail the convention check", name)
		}
	}
}

// ── high-cardinality injection detection ──────────────────────────────────────

// TestHighCardinalityRejection verifies that a node_address containing a UUID
// token in its query string is cleaned before use as a label.
func TestHighCardinalityRejection_UUIDInQuery(t *testing.T) {
	rawUUID := "550e8400-e29b-41d4-a716-446655440000"
	withToken := "https://rpc.example.com?session=" + rawUUID

	sanitized := SanitizeNodeAddress(withToken)
	if strings.Contains(sanitized, rawUUID) {
		t.Errorf("UUID token leaked into sanitized label: %q", sanitized)
	}
	if looksHighCardinality(sanitized) {
		t.Errorf("sanitized label %q still looks high-cardinality", sanitized)
	}
}

// TestHighCardinalityRejection_SHA256InQuery verifies SHA-256 hashes are scrubbed.
func TestHighCardinalityRejection_SHA256InQuery(t *testing.T) {
	hash := strings.Repeat("a", 64)
	withHash := "https://rpc.example.com?tx=" + hash

	sanitized := SanitizeNodeAddress(withHash)
	if strings.Contains(sanitized, hash) {
		t.Errorf("SHA-256 hash leaked into sanitized label: %q", sanitized)
	}
}

// TestMaxNodeAddressCardinality_ConstantDefined ensures the constant is set
// to a reasonable value so callers can enforce the limit.
func TestMaxNodeAddressCardinality_ConstantDefined(t *testing.T) {
	if MaxNodeAddressCardinality <= 0 {
		t.Errorf("MaxNodeAddressCardinality must be positive, got %d", MaxNodeAddressCardinality)
	}
	// Sanity bound: should not be set unreasonably large.
	if MaxNodeAddressCardinality > 1000 {
		t.Errorf("MaxNodeAddressCardinality=%d seems unreasonably large; review the cardinality policy", MaxNodeAddressCardinality)
	}
}
