// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package diagnostics builds a redacted, portable diagnostics archive that
// can be shared for support or issue reproduction without containing any
// private key, token, or personally identifying material.
package diagnostics

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// RedactedPlaceholder replaces any value that matched a secret pattern.
const RedactedPlaceholder = "[REDACTED]"

// secretPatterns lists compiled regexps whose full match is replaced when
// scanning string-valued config fields or environment variables.
var secretPatterns = []*regexp.Regexp{
	// Ed25519 / raw 32-byte hex private key material (64 hex chars)
	regexp.MustCompile(`(?i)^[0-9a-f]{64}$`),
	// Stellar secret key (S…)
	regexp.MustCompile(`^S[A-Z2-7]{55}$`),
	// Bearer / API tokens
	regexp.MustCompile(`(?i)^(bearer\s+)?[A-Za-z0-9\-_\.]{20,}$`),
}

// secretEnvKeys is the allowlist of environment variable names whose values
// are always redacted, regardless of content.
var secretEnvKeys = map[string]bool{
	"GLASSBOX_RPC_TOKEN":         true,
	"GLASSBOX_SENTRY_DSN":        true,
	"GLASSBOX_CONFIG_PASSPHRASE": true,
	"GLASSBOX_CRASH_ENDPOINT":    true,
}

// RedactString returns RedactedPlaceholder when s matches any secret pattern,
// otherwise returns s unchanged.
func RedactString(s string) string {
	trimmed := strings.TrimSpace(s)
	for _, re := range secretPatterns {
		if re.MatchString(trimmed) {
			return RedactedPlaceholder
		}
	}
	return s
}

// RedactEnvKey returns true when the environment variable name should always
// have its value redacted.
func RedactEnvKey(key string) bool {
	return secretEnvKeys[strings.ToUpper(key)]
}

// RedactPath replaces the user's home directory prefix with "~" so paths do
// not reveal usernames or home-directory paths.
func RedactPath(p string) string {
	if p == "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	// Normalize separator
	home = strings.ReplaceAll(home, "\\", "/")
	normalized := strings.ReplaceAll(p, "\\", "/")
	if strings.HasPrefix(normalized, home) {
		return "~" + strings.TrimPrefix(normalized, home)
	}
	return p
}

// RedactConfigMap returns a copy of a map[string]string with values redacted
// for keys whose names suggest secret material.
func RedactConfigMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		if RedactEnvKey(k) || looksLikeSecretKey(k) {
			out[k] = RedactedPlaceholder
		} else {
			out[k] = RedactPath(v)
		}
	}
	return out
}

// looksLikeSecretKey returns true for field names that conventionally hold
// secret material: anything containing "token", "secret", "key", "pass",
// "password", "dsn", "credential", or "auth".
func looksLikeSecretKey(k string) bool {
	lower := strings.ToLower(k)
	for _, kw := range []string{"token", "secret", "key", "pass", "password", "dsn", "credential", "auth", "pin"} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// secretFileBasenames is the set of exact basenames that must never be scanned
// by the diagnostics collector.  These files commonly hold key material.
var secretFileBasenames = map[string]bool{
	".env":          true,
	"id_rsa":        true,
	"id_ed25519":    true,
	"id_dsa":        true,
	"id_ecdsa":      true,
	".netrc":        true,
	"credentials":   true,
	".npmrc":        true,
	"token":         true,
	"secret.json":   true,
	"secrets.json":  true,
	"keystore.json": true,
	".htpasswd":     true,
}

// secretFileExtensions is the set of file extensions whose files must never
// be scanned by the diagnostics collector.
var secretFileExtensions = map[string]bool{
	".pem":  true,
	".key":  true,
	".pfx":  true,
	".p12":  true,
	".jks":  true,
	".der":  true,
	".ovpn": true,
}

// IsSecretFile returns true when the file at path is a known credential file
// that must never be read or scanned by the diagnostics collector.
func IsSecretFile(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	ext := strings.ToLower(filepath.Ext(base))
	if secretFileExtensions[ext] {
		return true
	}
	return secretFileBasenames[base]
}

// PathAllowedForScan returns true when it is safe for the diagnostics
// collector to read the file at path.  Any path classified as a secret file
// is always excluded regardless of its directory.
func PathAllowedForScan(path string) bool {
	return !IsSecretFile(path)
}
