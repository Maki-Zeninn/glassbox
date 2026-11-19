// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// secret_resolution.go — typed secret-source resolution for the signer package.
//
// SecretResolution records where a secret was found (flag, environment
// variable, file, or agent) without ever recording the secret value itself.
// This provides audit-quality diagnostics for secret-loading failures while
// ensuring that values never appear in process logs, JSON errors, or
// provenance records.
//
// Precedence for audit/signing keys (highest → lowest):
//  1. CLI flag      (--software-private-key, --pkcs11-pin, etc.)
//  2. File          (path supplied via flag or env var, read from disk)
//  3. Environment   (GLASSBOX_AUDIT_PRIVATE_KEY_PEM, GLASSBOX_PKCS11_*, etc.)
//  4. Default/Agent (provider-specific defaults or external key agents)
//
// This order is documented in help text for every signing command and enforced
// by ResolveSecretSource.
package signer

import (
	"fmt"
	"os"
	"strings"
)

// SourceKind identifies the origin of a resolved secret, without conveying
// any information about the value.
type SourceKind string

const (
	// SourceKindFlag means the value was supplied via a CLI flag.
	SourceKindFlag SourceKind = "flag"
	// SourceKindFile means the value was read from a file whose path was
	// provided via a flag or environment variable.
	SourceKindFile SourceKind = "file"
	// SourceKindEnv means the value was read from an environment variable.
	SourceKindEnv SourceKind = "env"
	// SourceKindAgent means the value was obtained from a key agent or
	// provider-specific default mechanism.
	SourceKindAgent SourceKind = "agent"
	// SourceKindNone means no value was found from any source.
	SourceKindNone SourceKind = "none"
)

// SecretResolution describes where a secret was resolved from without
// recording the secret value.  It is safe to include in log entries,
// JSON error responses, and provenance records.
type SecretResolution struct {
	// Name is the logical name of the secret (e.g. "signing_key", "pkcs11_pin").
	Name string `json:"name"`
	// Kind is the source category that provided the value.
	Kind SourceKind `json:"kind"`
	// Source is a non-sensitive description of the exact source — for flags
	// this is the flag name, for env vars the variable name, for files the
	// redacted path (filename only, no directory components that could leak
	// filesystem layout).
	Source string `json:"source"`
	// Resolved is true when a non-empty value was found.
	Resolved bool `json:"resolved"`
	// Conflict is true when the same secret was found in more than one source.
	// The winner is the source with the highest precedence (flag > file > env > agent).
	Conflict bool `json:"conflict,omitempty"`
	// ConflictSources lists every source that supplied a value (excluding the
	// winner) so the operator knows exactly which sources are in conflict.
	// No values are ever stored here.
	ConflictSources []string `json:"conflict_sources,omitempty"`
}

// String returns a redacted, human-readable summary of the resolution.
func (r SecretResolution) String() string {
	if !r.Resolved {
		return fmt.Sprintf("%s: not found (checked: flag, file, env)", r.Name)
	}
	s := fmt.Sprintf("%s: resolved from %s (%s)", r.Name, r.Kind, r.Source)
	if r.Conflict {
		s += fmt.Sprintf(" [conflict: also set in %s]", strings.Join(r.ConflictSources, ", "))
	}
	return s
}

// SecretResolutionSet is a collection of SecretResolution records for a
// complete provider configuration.  It is used by diagnostics commands
// and the --plan flag to describe what would be used without executing.
type SecretResolutionSet struct {
	Provider    string             `json:"provider"`
	Resolutions []SecretResolution `json:"resolutions"`
}

// AllResolved returns true when every resolution in the set is resolved.
func (s *SecretResolutionSet) AllResolved() bool {
	for _, r := range s.Resolutions {
		if !r.Resolved {
			return false
		}
	}
	return true
}

// AnyConflict returns true when at least one resolution has a conflict.
func (s *SecretResolutionSet) AnyConflict() bool {
	for _, r := range s.Resolutions {
		if r.Conflict {
			return true
		}
	}
	return false
}

// RedactedDiagnostics returns a human-readable multi-line report of all
// resolutions, suitable for diagnostic output.  Secret values are never
// included.
func (s *SecretResolutionSet) RedactedDiagnostics() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Secret resolution for provider %q:\n", s.Provider)
	for _, r := range s.Resolutions {
		fmt.Fprintf(&sb, "  %s\n", r.String())
	}
	if s.AnyConflict() {
		fmt.Fprintln(&sb, "\n  WARNING: conflicting sources detected. "+
			"The highest-precedence source wins (flag > file > env > agent). "+
			"Remove the lower-precedence sources to silence this warning.")
	}
	return sb.String()
}

// ── Source resolution helpers ─────────────────────────────────────────────────

// SecretSource describes a single candidate for a secret, in precedence order.
// Callers build a slice of these and pass it to resolveSecret.
type SecretSource struct {
	// Kind is the source category.
	Kind SourceKind
	// Label is the human-readable identifier (flag name or env var name).
	Label string
	// Value is the raw string from this source, or "" if not available.
	// For file sources, Value is the file path, not the file contents.
	Value string
	// IsFilePath, when true, indicates that Value is a file path whose
	// contents should be read.  The resolution records the filename only.
	IsFilePath bool
}

// resolveSecret walks sources in order (index 0 = highest precedence) and
// returns the first resolved SecretResolution.  It sets Conflict and
// ConflictSources when more than one source has a non-empty value.
//
// The resolved value is returned as the second return only to let providers
// act on it — it must never be stored in the SecretResolution itself.
func resolveSecret(name string, sources []SecretSource) (SecretResolution, string) {
	type candidate struct {
		source SecretSource
		value  string // actual resolved value — never stored in resolution
	}
	var found []candidate

	for _, src := range sources {
		v := src.Value
		if v == "" {
			continue
		}
		if src.IsFilePath {
			// Value is a file path: try to read it. If the file does not exist
			// or is unreadable we skip this source entirely — the user gets a
			// missing-secret error rather than a misleading empty-value error.
			data, err := os.ReadFile(v)
			if err != nil || len(strings.TrimSpace(string(data))) == 0 {
				continue
			}
			found = append(found, candidate{source: src, value: strings.TrimSpace(string(data))})
		} else {
			found = append(found, candidate{source: src, value: v})
		}
	}

	if len(found) == 0 {
		return SecretResolution{Name: name, Kind: SourceKindNone, Resolved: false}, ""
	}

	winner := found[0]
	res := SecretResolution{
		Name:     name,
		Kind:     winner.source.Kind,
		Source:   safeLabel(winner.source),
		Resolved: true,
	}

	if len(found) > 1 {
		res.Conflict = true
		for _, extra := range found[1:] {
			res.ConflictSources = append(res.ConflictSources, safeLabel(extra.source))
		}
	}

	return res, winner.value
}

// safeLabel returns a non-sensitive label for a source.  For file paths it
// returns only the base filename to avoid leaking directory structure.
func safeLabel(src SecretSource) string {
	if src.IsFilePath && src.Value != "" {
		return fmt.Sprintf("%s (file: %s)", src.Label, fileBaseName(src.Value))
	}
	return src.Label
}

func fileBaseName(path string) string {
	// Strip directory components manually to avoid importing filepath in a
	// security-sensitive utility — we only want the base name.
	path = strings.ReplaceAll(path, "\\", "/")
	parts := strings.Split(path, "/")
	return parts[len(parts)-1]
}

// ── Software provider key resolution ─────────────────────────────────────────

// ResolveSoftwareSigningKey resolves the Ed25519 signing key for the software
// provider using the documented precedence:
//
//  1. cfg.SoftwareKeyPEM flag value (literal PEM or file path)
//  2. GLASSBOX_AUDIT_PRIVATE_KEY_PEM environment variable (literal PEM or path)
//  3. cfg.SoftwareKeyHex flag value
//  4. GLASSBOX_SOFTWARE_PRIVATE_KEY_HEX environment variable
//
// Returns the SecretResolution (for diagnostics) and the resolved raw value.
// The resolution never contains the key material.
func ResolveSoftwareSigningKey(cfg ProviderConfig) (SecretResolution, string) {
	pemSources := []SecretSource{
		{Kind: SourceKindFlag, Label: "--software-private-key", Value: cfg.SoftwareKeyPEM, IsFilePath: !looksLikePEM(cfg.SoftwareKeyPEM)},
		{Kind: SourceKindEnv, Label: "GLASSBOX_AUDIT_PRIVATE_KEY_PEM", Value: os.Getenv("GLASSBOX_AUDIT_PRIVATE_KEY_PEM"), IsFilePath: !looksLikePEM(os.Getenv("GLASSBOX_AUDIT_PRIVATE_KEY_PEM"))},
	}
	// PEM sources take priority over hex sources.
	res, val := resolveSecret("signing_key_pem", pemSources)
	if res.Resolved {
		return res, val
	}

	hexSources := []SecretSource{
		{Kind: SourceKindFlag, Label: "--software-private-key-hex", Value: cfg.SoftwareKeyHex},
		{Kind: SourceKindEnv, Label: "GLASSBOX_SOFTWARE_PRIVATE_KEY_HEX", Value: os.Getenv("GLASSBOX_SOFTWARE_PRIVATE_KEY_HEX")},
	}
	return resolveSecret("signing_key_hex", hexSources)
}

// ── PKCS#11 secret resolution ─────────────────────────────────────────────────

// ResolvePKCS11Secrets resolves all PKCS#11 secrets using documented precedence
// (flag > env) and returns a SecretResolutionSet for diagnostics.  The actual
// values are returned separately and must not be stored in the resolution set.
func ResolvePKCS11Secrets(cfg ProviderConfig) (*SecretResolutionSet, PKCS11ResolvedSecrets) {
	set := &SecretResolutionSet{Provider: "pkcs11"}

	moduleRes, modulePath := resolveSecret("pkcs11_module", []SecretSource{
		{Kind: SourceKindFlag, Label: "--pkcs11-module", Value: cfg.PKCS11ModulePath, IsFilePath: true},
		{Kind: SourceKindEnv, Label: "GLASSBOX_PKCS11_MODULE", Value: os.Getenv("GLASSBOX_PKCS11_MODULE"), IsFilePath: true},
	})
	set.Resolutions = append(set.Resolutions, moduleRes)

	pinRes, pinValue := resolveSecret("pkcs11_pin", []SecretSource{
		{Kind: SourceKindFlag, Label: "--pkcs11-pin", Value: cfg.PKCS11PIN},
		{Kind: SourceKindEnv, Label: "GLASSBOX_PKCS11_PIN", Value: os.Getenv("GLASSBOX_PKCS11_PIN")},
	})
	set.Resolutions = append(set.Resolutions, pinRes)

	tokenRes, tokenLabel := resolveSecret("pkcs11_token_label", []SecretSource{
		{Kind: SourceKindFlag, Label: "--pkcs11-token-label", Value: cfg.PKCS11TokenLabel},
		{Kind: SourceKindEnv, Label: "GLASSBOX_PKCS11_TOKEN_LABEL", Value: os.Getenv("GLASSBOX_PKCS11_TOKEN_LABEL")},
	})
	set.Resolutions = append(set.Resolutions, tokenRes)

	keyLabelRes, keyLabel := resolveSecret("pkcs11_key_label", []SecretSource{
		{Kind: SourceKindFlag, Label: "--pkcs11-key-label", Value: cfg.PKCS11KeyLabel},
		{Kind: SourceKindEnv, Label: "GLASSBOX_PKCS11_KEY_LABEL", Value: os.Getenv("GLASSBOX_PKCS11_KEY_LABEL")},
	})
	set.Resolutions = append(set.Resolutions, keyLabelRes)

	keyIDRes, keyID := resolveSecret("pkcs11_key_id", []SecretSource{
		{Kind: SourceKindFlag, Label: "--pkcs11-key-id", Value: cfg.PKCS11KeyIDHex},
		{Kind: SourceKindEnv, Label: "GLASSBOX_PKCS11_KEY_ID", Value: os.Getenv("GLASSBOX_PKCS11_KEY_ID")},
	})
	set.Resolutions = append(set.Resolutions, keyIDRes)

	secrets := PKCS11ResolvedSecrets{
		ModulePath: modulePath,
		PIN:        pinValue,
		TokenLabel: tokenLabel,
		KeyLabel:   keyLabel,
		KeyIDHex:   keyID,
	}
	return set, secrets
}

// PKCS11ResolvedSecrets holds the actual resolved values for PKCS#11
// configuration.  These values must never be logged or stored in any struct
// that ends up in logs, JSON errors, or provenance records.
type PKCS11ResolvedSecrets struct {
	ModulePath string
	PIN        string
	TokenLabel string
	KeyLabel   string
	KeyIDHex   string
}

// ── Session key resolution ────────────────────────────────────────────────────

// ResolveSessionEncryptionKey resolves the session encryption key using
// documented precedence:
//
//  1. CLI flag --session-key-passphrase
//  2. GLASSBOX_SESSION_KEY_PASSPHRASE environment variable
//  3. CLI flag --session-key (raw hex/base64)
//  4. GLASSBOX_SESSION_KEY environment variable
//
// Returns the SecretResolution for diagnostics and the resolved passphrase or
// raw key string.  The value must never be stored in a log or provenance record.
func ResolveSessionEncryptionKey(passphraseFlag, keyFlag string) (SecretResolution, string) {
	passphraseSources := []SecretSource{
		{Kind: SourceKindFlag, Label: "--session-key-passphrase", Value: passphraseFlag},
		{Kind: SourceKindEnv, Label: "GLASSBOX_SESSION_KEY_PASSPHRASE", Value: os.Getenv("GLASSBOX_SESSION_KEY_PASSPHRASE")},
	}
	res, val := resolveSecret("session_key_passphrase", passphraseSources)
	if res.Resolved {
		return res, val
	}
	rawKeySources := []SecretSource{
		{Kind: SourceKindFlag, Label: "--session-key", Value: keyFlag},
		{Kind: SourceKindEnv, Label: "GLASSBOX_SESSION_KEY", Value: os.Getenv("GLASSBOX_SESSION_KEY")},
	}
	return resolveSecret("session_key_raw", rawKeySources)
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func looksLikePEM(s string) bool {
	return strings.Contains(s, "-----BEGIN")
}

// ValidateNoConflicts checks a SecretResolutionSet for conflicts and returns a
// structured error with a redacted diagnostic when any are found.  The error
// message names the conflicting sources but never their values.
func ValidateNoConflicts(set *SecretResolutionSet) error {
	if set == nil || !set.AnyConflict() {
		return nil
	}
	var issues []string
	for _, r := range set.Resolutions {
		if !r.Conflict {
			continue
		}
		issues = append(issues, fmt.Sprintf(
			"  %s: resolved from %s but also set in %s",
			r.Name, r.Source, strings.Join(r.ConflictSources, ", "),
		))
	}
	return &Error{
		Op: "secret-resolution",
		Msg: fmt.Sprintf(
			"conflicting secret sources detected for provider %q — "+
				"the highest-precedence source wins, but you should remove the lower-precedence values "+
				"to ensure the intended key is always used:\n%s\n"+
				"  Fix: keep only one source per secret (flag takes precedence over env var).",
			set.Provider, strings.Join(issues, "\n"),
		),
	}
}

// ValidateRequiredSecrets returns an error listing every unresolved required
// secret.  Only source names (flag names, env var names) are included — values
// are never mentioned.
func ValidateRequiredSecrets(set *SecretResolutionSet, required []string) error {
	if set == nil {
		return nil
	}
	byName := make(map[string]SecretResolution, len(set.Resolutions))
	for _, r := range set.Resolutions {
		byName[r.Name] = r
	}
	var missing []string
	for _, name := range required {
		if r, ok := byName[name]; !ok || !r.Resolved {
			// Find the expected flag/env var names from the resolutions list.
			expected := expectedSourcesFor(set, name)
			if len(expected) > 0 {
				missing = append(missing, fmt.Sprintf("  %s (provide via: %s)", name, strings.Join(expected, " or ")))
			} else {
				missing = append(missing, "  "+name)
			}
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return &Error{
		Op:  "secret-resolution",
		Msg: fmt.Sprintf("required secret(s) not found for provider %q:\n%s", set.Provider, strings.Join(missing, "\n")),
	}
}

// expectedSourcesFor returns the label strings for the resolution with the
// given name — these are the flag/env names the user should set.
func expectedSourcesFor(set *SecretResolutionSet, name string) []string {
	for _, r := range set.Resolutions {
		if r.Name == name {
			return []string{r.Source}
		}
	}
	return nil
}
