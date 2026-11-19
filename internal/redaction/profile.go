// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package redaction provides configurable redaction profiles that can be
// applied consistently across all report formats (HTML, JSON, PDF, text).
//
// A RedactionProfile defines rules for what to redact and how. Profiles
// can be built-in (e.g., "full", "secrets-only") or user-defined with
// custom field patterns and redaction markers.
package redaction

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Placeholder is the default redaction marker.
const Placeholder = "[REDACTED]"

// FieldType classifies the kind of data a redaction rule targets.
type FieldType string

const (
	FieldSecret       FieldType = "secret"
	FieldAccountID    FieldType = "account_id"
	FieldContractID   FieldType = "contract_id"
	FieldPath         FieldType = "path"
	FieldUserSelected FieldType = "user_selected"
	FieldPIN          FieldType = "pin"
	FieldToken        FieldType = "token"
)

// Rule defines a single redaction rule.
type Rule struct {
	// Field identifies the rule (e.g., "private_key", "rpc_token").
	Field string `json:"field"`

	// Type classifies the data being redacted.
	Type FieldType `json:"type"`

	// Pattern is an optional regex that matches values to redact.
	// If empty, the rule matches by key name only.
	Pattern string `json:"pattern,omitempty"`

	// compiled is the compiled regex (populated by Compile).
	compiled *regexp.Regexp
}

// Profile is a named, reusable set of redaction rules.
type Profile struct {
	// Name is a human-readable identifier (e.g., "full", "secrets-only").
	Name string `json:"name"`

	// Description explains what the profile redacts.
	Description string `json:"description,omitempty"`

	// Rules is the ordered list of redaction rules.
	Rules []Rule `json:"rules"`

	// RedactedPlaceholder overrides the default "[REDACTED]" marker.
	// If empty, Placeholder is used.
	RedactedPlaceholder string `json:"redacted_placeholder,omitempty"`

	// effectivePlaceholder returns the effective placeholder for this profile.
	// It returns p.RedactedPlaceholder if non-empty, otherwise Placeholder.
	func (p *Profile) effectivePlaceholder() string {
		if p.RedactedPlaceholder != "" {
			return p.RedactedPlaceholder
		}
		return Placeholder
	}

	// OptIn controls whether the profile is opt-in (false) or opt-out (true).
	// When OptIn is true, fields not explicitly listed are NOT redacted.
	// When OptIn is false (default), all matched fields are redacted.
	OptIn bool `json:"opt_in,omitempty"`
}

// RedactionSummary records what was redacted for inclusion in report metadata.
type RedactionSummary struct {
	ProfileName string `json:"profile_name"`
	// FieldsRedacted lists each field name that was redacted. Duplicates may
	// appear if multiple rules matched the same field.
	FieldsRedacted []string `json:"fields_redacted"`
	TotalRedacted  int      `json:"total_redacted"`
}

// Apply applies the profile's compiled redaction rules to value.
// It returns the redaction placeholder if any rule's pattern matches value,
// or value unchanged if no rule matches.
// Apply is safe for concurrent use; it does not modify the Profile.
func (p *Profile) Apply(value string) string {
	placeholder := p.effectivePlaceholder()

	for _, rule := range p.Rules {
		if rule.compiled != nil && rule.compiled.MatchString(value) {
			return placeholder
		}
	}
	return value
}

// ApplyToMap applies the profile's redaction rules to a map whose values may be
// of any type. String values are tested against both key-name rules and compiled
// pattern rules; non-string values are passed through unchanged.
// The original map is not modified; a new map is returned.
// ApplyToMap is safe for concurrent use; it does not modify the Profile.
func (p *Profile) ApplyToMap(m map[string]interface{}) map[string]interface{} {
	placeholder := p.effectivePlaceholder()

	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		strVal, ok := v.(string)
		if !ok {
			out[k] = v
			continue
		}

		redacted := false
		for _, rule := range p.Rules {
			if p.matchesKeyRule(k, rule) {
				out[k] = placeholder
				redacted = true
				break
			}
			if rule.compiled != nil && rule.compiled.MatchString(strVal) {
				out[k] = placeholder
				redacted = true
				break
			}
		}
		if !redacted {
			out[k] = v
		}
	}
	return out
}

// ApplyToStringMap applies the profile's redaction rules to a map[string]string.
// Each value is tested against key-name rules and compiled pattern rules.
// The original map is not modified; a new map is returned.
// ApplyToStringMap is safe for concurrent use; it does not modify the Profile.
func (p *Profile) ApplyToStringMap(m map[string]string) map[string]string {
	placeholder := p.effectivePlaceholder()

	out := make(map[string]string, len(m))
	for k, v := range m {
		redacted := false
		for _, rule := range p.Rules {
			if p.matchesKeyRule(k, rule) {
				out[k] = placeholder
				redacted = true
				break
			}
			if rule.compiled != nil && rule.compiled.MatchString(v) {
				out[k] = placeholder
				redacted = true
				break
			}
		}
		if !redacted {
			out[k] = v
		}
	}
	return out
}

// matchesKeyRule checks whether a key matches a rule's field name or type.
func (p *Profile) matchesKeyRule(key string, rule Rule) bool {
	lower := strings.ToLower(key)

	// Direct field name match
	if rule.Field != "" && strings.Contains(lower, strings.ToLower(rule.Field)) {
		return true
	}

	// Type-based keyword matching
	switch rule.Type {
	case FieldSecret:
		return containsAny(lower, "secret", "private", "key", "pass", "password", "dsn", "credential", "auth", "pin")
	case FieldToken:
		return containsAny(lower, "token", "bearer", "api_key", "apikey")
	case FieldAccountID:
		return containsAny(lower, "account", "account_id", "address")
	case FieldContractID:
		return containsAny(lower, "contract", "contract_id", "wasm_hash")
	case FieldPath:
		return containsAny(lower, "path", "file", "dir", "directory", "cache")
	case FieldPIN:
		return containsAny(lower, "pin", "passphrase")
	case FieldUserSelected:
		return false // Must be matched by explicit field name
	default:
		return false
	}
}

func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// Compile compiles all regex patterns in the profile's rules.
// It must be called once before any Apply* method is used on a Profile that
// was constructed manually (built-in profiles returned by FullProfile and
// SecretsOnlyProfile are already compiled). Compile is not safe for concurrent
// use; call it before sharing the Profile across goroutines.
func (p *Profile) Compile() error {
	for i := range p.Rules {
		if p.Rules[i].Pattern != "" {
			re, err := regexp.Compile(p.Rules[i].Pattern)
			if err != nil {
				return fmt.Errorf("compile rule %q pattern: %w", p.Rules[i].Field, err)
			}
			p.Rules[i].compiled = re
		}
	}
	return nil
}

// Summary returns a RedactionSummary recording which fields were redacted and
// by which profile. The returned struct is suitable for inclusion in report
// metadata. fieldsRedacted should be the list of field names that were
// replaced with the redaction placeholder during a single Apply* call.
func (p *Profile) Summary(fieldsRedacted []string) RedactionSummary {
	return RedactionSummary{
		ProfileName:    p.Name,
		FieldsRedacted: fieldsRedacted,
		TotalRedacted:  len(fieldsRedacted),
	}
}

// MarshalJSON implements json.Marshaler to include the summary.
func (s RedactionSummary) MarshalJSON() ([]byte, error) {
	type Alias struct {
		ProfileName    string   `json:"profile_name"`
		FieldsRedacted []string `json:"fields_redacted"`
		TotalRedacted  int      `json:"total_redacted"`
	}
	return json.Marshal(Alias(s))
}

// ── Built-in profiles ────────────────────────────────────────────────────────

// FullProfile returns a profile that redacts all sensitive fields.
func FullProfile() *Profile {
	p := &Profile{
		Name:        "full",
		Description: "Redacts all secrets, tokens, account IDs, contract IDs, paths, and PINs",
		Rules: []Rule{
			{Field: "private_key", Type: FieldSecret},
			{Field: "secret_key", Type: FieldSecret},
			{Field: "rpc_token", Type: FieldToken},
			{Field: "sentry_dsn", Type: FieldSecret},
			{Field: "crash_endpoint", Type: FieldSecret},
			{Field: "passphrase", Type: FieldPIN},
			{Field: "account_id", Type: FieldAccountID},
			{Field: "contract_id", Type: FieldContractID},
			{Field: "path", Type: FieldPath},
			{Field: "pin", Type: FieldPIN},
			{Type: FieldToken, Pattern: `(?i)^(bearer\s+)?[A-Za-z0-9\-_\.]{20,}$`},
			{Type: FieldSecret, Pattern: `(?i)^[0-9a-f]{64}$`},
			{Type: FieldSecret, Pattern: `^S[A-Z2-7]{55}$`},
		},
	}
	_ = p.Compile()
	return p
}

// SecretsOnlyProfile returns a profile that only redacts secret material
// (keys, tokens, credentials) but preserves account/contract IDs and paths.
func SecretsOnlyProfile() *Profile {
	p := &Profile{
		Name:        "secrets-only",
		Description: "Redacts only secrets, tokens, and credentials",
		Rules: []Rule{
			{Field: "private_key", Type: FieldSecret},
			{Field: "secret_key", Type: FieldSecret},
			{Field: "rpc_token", Type: FieldToken},
			{Field: "sentry_dsn", Type: FieldSecret},
			{Field: "crash_endpoint", Type: FieldSecret},
			{Field: "passphrase", Type: FieldPIN},
			{Type: FieldToken, Pattern: `(?i)^(bearer\s+)?[A-Za-z0-9\-_\.]{20,}$`},
			{Type: FieldSecret, Pattern: `(?i)^[0-9a-f]{64}$`},
			{Type: FieldSecret, Pattern: `^S[A-Z2-7]{55}$`},
		},
	}
	_ = p.Compile()
	return p
}

// ProfilesByName returns the built-in profile for the given name.
func ProfilesByName(name string) (*Profile, bool) {
	switch strings.ToLower(name) {
	case "full":
		return FullProfile(), true
	case "secrets-only", "secrets_only", "secretsonly":
		return SecretsOnlyProfile(), true
	default:
		return nil, false
	}
}

// LoadProfileFromFile reads a JSON-encoded Profile from the given path.
func LoadProfileFromFile(path string) (*Profile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read profile file: %w", err)
	}

	var p Profile
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse profile JSON: %w", err)
	}

	if err := p.Compile(); err != nil {
		return nil, err
	}

	return &p, nil
}
