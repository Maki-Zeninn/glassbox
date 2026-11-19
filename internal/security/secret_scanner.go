// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package security

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
)

// SecretType categorizes the type of secret detected
type SecretType string

const (
	SecretTypeAPIKey       SecretType = "API_KEY"
	SecretTypeBearerToken  SecretType = "BEARER_TOKEN"
	SecretTypePrivateKey    SecretType = "PRIVATE_KEY"
	SecretTypePEM           SecretType = "PEM_DATA"
	SecretTypeAWSKey        SecretType = "AWS_ACCESS_KEY"
	SecretTypeGitHubToken   SecretType = "GITHUB_TOKEN"
	SecretTypeJWT           SecretType = "JWT_TOKEN"
	SecretTypeGenericSecret SecretType = "GENERIC_SECRET"
)

// SecretFinding represents a detected secret with its location
type SecretFinding struct {
	Type        SecretType `json:"type"`
	Location    string     `json:"location"`    // Field/path where secret was found
	Context     string     `json:"context"`     // Surrounding context (truncated, no value)
	LineNumber  int        `json:"line_number,omitempty"`
}

// ScanResult contains the findings from a secret scan
type ScanResult struct {
	Findings []SecretFinding `json:"findings"`
	HasSecrets bool           `json:"has_secrets"`
}

// ScannerMode determines how the scanner behaves
type ScannerMode string

const (
	ModeOptIn  ScannerMode = "OPT_IN"  // Warn but allow export
	ModeStrict ScannerMode = "STRICT"  // Block export if secrets found
)

// SecretScanner detects secrets in export data
type SecretScanner struct {
	mode               ScannerMode
	overrides          map[string]bool // Paths that are allowed to contain secrets
	customPatterns     map[string]*regexp.Regexp
	suppressionRegistry *SuppressionRegistry
	scopeValue         string // Contract ID, path, or transaction hash for scoped suppressions
}

// NewSecretScanner creates a new secret scanner
func NewSecretScanner(mode ScannerMode) *SecretScanner {
	return &SecretScanner{
		mode:               mode,
		overrides:          make(map[string]bool),
		customPatterns:     make(map[string]*regexp.Regexp),
		suppressionRegistry: NewSuppressionRegistry(),
	}
}

// NewSecretScannerWithSuppression creates a new secret scanner with a suppression registry.
func NewSecretScannerWithSuppression(mode ScannerMode, registry *SuppressionRegistry, scopeValue string) *SecretScanner {
	return &SecretScanner{
		mode:               mode,
		overrides:          make(map[string]bool),
		customPatterns:     make(map[string]*regexp.Regexp),
		suppressionRegistry: registry,
		scopeValue:         scopeValue,
	}
}

// SetSuppressionRegistry sets the suppression registry for this scanner.
func (s *SecretScanner) SetSuppressionRegistry(registry *SuppressionRegistry, scopeValue string) {
	s.suppressionRegistry = registry
	s.scopeValue = scopeValue
}

// AddOverride adds a path that is allowed to contain secrets (for test fixtures)
func (s *SecretScanner) AddOverride(path string) {
	s.overrides[path] = true
}

// AddCustomPattern adds a custom regex pattern for secret detection
func (s *SecretScanner) AddCustomPattern(name string, pattern string) error {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return fmt.Errorf("invalid custom pattern %q: %w", name, err)
	}
	s.customPatterns[name] = re
	return nil
}

// ScanString scans a string value for secrets
func (s *SecretScanner) ScanString(value, location string) SecretFinding {
	if value == "" {
		return SecretFinding{}
	}

	// Check if this location is overridden
	if s.overrides[location] {
		return SecretFinding{}
	}

	// Try each secret pattern
	if finding := s.checkAPIKey(value, location); finding.Type != "" {
		return finding
	}
	if finding := s.checkBearerToken(value, location); finding.Type != "" {
		return finding
	}
	if finding := s.checkPrivateKey(value, location); finding.Type != "" {
		return finding
	}
	if finding := s.checkPEM(value, location); finding.Type != "" {
		return finding
	}
	if finding := s.checkAWSKey(value, location); finding.Type != "" {
		return finding
	}
	if finding := s.checkGitHubToken(value, location); finding.Type != "" {
		return finding
	}
	if finding := s.checkJWT(value, location); finding.Type != "" {
		return finding
	}

	return SecretFinding{}
}

// ScanMap scans a map for secrets (e.g., session metadata, trace annotations)
func (s *SecretScanner) ScanMap(data map[string]string, prefix string) ScanResult {
	result := ScanResult{
		Findings: make([]SecretFinding, 0),
	}

	for key, value := range data {
		location := prefix
		if prefix != "" {
			location += "."
		}
		location += key

		finding := s.ScanString(value, location)
		if finding.Type != "" {
			result.Findings = append(result.Findings, finding)
		}
	}

	result.HasSecrets = len(result.Findings) > 0
	return result
}

// ScanStruct scans a struct for secrets by converting to map-like representation
func (s *SecretScanner) ScanStruct(data interface{}, prefix string) ScanResult {
	result := ScanResult{
		Findings: make([]SecretFinding, 0),
	}

	// Convert to string representation for scanning
	strValue := fmt.Sprintf("%v", data)
	if strValue == "" || strValue == "<nil>" {
		return result
	}

	finding := s.ScanString(strValue, prefix)
	if finding.Type != "" {
		result.Findings = append(result.Findings, finding)
	}

	result.HasSecrets = len(result.Findings) > 0
	return result
}

// ShouldBlockExport returns true if the scanner is in strict mode and secrets were found
func (s *SecretScanner) ShouldBlockExport(result ScanResult) bool {
	return s.mode == ModeStrict && result.HasSecrets
}

// GetScanResultWithSuppression returns active and suppressed findings.
func (s *SecretScanner) GetScanResultWithSuppression(result ScanResult) ScanResultWithSuppression {
	if s.suppressionRegistry == nil {
		return ScanResultWithSuppression{
			ActiveFindings:     result.Findings,
			SuppressedFindings: []SuppressedSecretFinding{},
			HasSecrets:         result.HasSecrets,
		}
	}
	
	scopeValue := NormalizeScopeValue(s.scopeValue)
	active, suppressed := s.suppressionRegistry.ApplyToSecretFindings(result.Findings, scopeValue)
	
	return ScanResultWithSuppression{
		ActiveFindings:     active,
		SuppressedFindings: suppressed,
		HasSecrets:         len(active) > 0,
	}
}

// GetErrorMessage returns a formatted error message for blocking exports
func (s *SecretScanner) GetErrorMessage(result ScanResult) string {
	if !result.HasSecrets {
		return ""
	}

	var parts []string
	parts = append(parts, fmt.Sprintf("Secret scan detected %d potential secret(s) in export data", len(result.Findings)))
	parts = append(parts, "\nDetected secrets:")
	
	for i, finding := range result.Findings {
		parts = append(parts, fmt.Sprintf("  %d. [%s] at %s", i+1, finding.Type, finding.Location))
		if finding.Context != "" {
			// Truncate context to avoid leaking the secret
			truncated := finding.Context
			if len(truncated) > 50 {
				truncated = truncated[:50] + "..."
			}
			parts = append(parts, fmt.Sprintf("     Context: %s", truncated))
		}
	}

	if s.mode == ModeStrict {
		parts = append(parts, "\nExport blocked due to strict mode.")
		parts = append(parts, "To allow this export:")
		parts = append(parts, "  1. Remove the secret from the data, or")
		parts = append(parts, "  2. Use opt-in mode instead of strict mode, or")
		parts = append(parts, "  3. Add an explicit override for this location if it's a test fixture")
	} else {
		parts = append(parts, "\nWarning: secrets detected but export allowed in opt-in mode.")
	}

	return strings.Join(parts, "\n")
}

// checkAPIKey detects API key patterns
func (s *SecretScanner) checkAPIKey(value, location string) SecretFinding {
	// Common API key patterns
	patterns := []string{
		`(?i)api[_-]?key\s*[:=]\s*['"]?([a-zA-Z0-9]{20,})['"]?`,
		`(?i)apikey\s*[:=]\s*['"]?([a-zA-Z0-9]{20,})['"]?`,
		`(?i)key\s*[:=]\s*['"]?([a-zA-Z0-9]{32,})['"]?`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		if re.MatchString(value) {
			return SecretFinding{
				Type:     SecretTypeAPIKey,
				Location: location,
				Context:  extractContext(value, 30),
			}
		}
	}

	return SecretFinding{}
}

// checkBearerToken detects bearer token patterns
func (s *SecretScanner) checkBearerToken(value, location string) SecretFinding {
	if strings.HasPrefix(strings.ToLower(value), "bearer ") {
		token := strings.TrimSpace(value[7:])
		if len(token) >= 20 {
			return SecretFinding{
				Type:     SecretTypeBearerToken,
				Location: location,
				Context:  "bearer " + maskString(token),
			}
		}
	}

	re := regexp.MustCompile(`(?i)bearer[_-]?token\s*[:=]\s*['"]?([a-zA-Z0-9._-]{20,})['"]?`)
	if re.MatchString(value) {
		return SecretFinding{
			Type:     SecretTypeBearerToken,
			Location: location,
			Context:  extractContext(value, 30),
		}
	}

	return SecretFinding{}
}

// checkPrivateKey detects private key patterns
func (s *SecretScanner) checkPrivateKey(value, location string) SecretFinding {
	patterns := []string{
		`-----BEGIN\s+(RSA\s+)?PRIVATE\s+KEY-----`,
		`-----BEGIN\s+EC\s+PRIVATE\s+KEY-----`,
		`-----BEGIN\s+DSA\s+PRIVATE\s+KEY-----`,
		`-----BEGIN\s+OPENSSH\s+PRIVATE\s+KEY-----`,
		`-----BEGIN\s+PGP\s+PRIVATE\s+KEY-----`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		if re.MatchString(value) {
			return SecretFinding{
				Type:     SecretTypePrivateKey,
				Location: location,
				Context:  "-----BEGIN PRIVATE KEY-----",
			}
		}
	}

	// Check for hex-encoded private keys (64 hex chars for 32 bytes)
	re := regexp.MustCompile(`\b[0-9a-fA-F]{64}\b`)
	if re.MatchString(value) && strings.Contains(strings.ToLower(location), "private") {
		return SecretFinding{
			Type:     SecretTypePrivateKey,
			Location: location,
			Context:  maskString(value),
		}
	}

	return SecretFinding{}
}

// checkPEM detects PEM-encoded data
func (s *SecretScanner) checkPEM(value, location string) SecretFinding {
	re := regexp.MustCompile(`-----BEGIN\s+[A-Z\s]+-----`)
	if re.MatchString(value) {
		return SecretFinding{
			Type:     SecretTypePEM,
			Location: location,
			Context:  "-----BEGIN ... -----",
		}
	}

	// Check for base64-encoded PEM-like data (multiple lines of base64)
	lines := strings.Split(value, "\n")
	if len(lines) > 2 {
		base64Count := 0
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line != "" {
				if _, err := base64.StdEncoding.DecodeString(line); err == nil && len(line) > 40 {
					base64Count++
				}
			}
		}
		if base64Count >= 3 {
			return SecretFinding{
				Type:     SecretTypePEM,
				Location: location,
				Context:  "base64-encoded PEM-like data",
			}
		}
	}

	return SecretFinding{}
}

// checkAWSKey detects AWS access key patterns
func (s *SecretScanner) checkAWSKey(value, location string) SecretFinding {
	// AWS access key ID: 20 alphanumeric chars starting with A (for IAM) or specific patterns
	re := regexp.MustCompile(`(?i)(aws[_-]?(access[_-]?key[_-]?id|secret[_-]?access[_-]?key))\s*[:=]\s*['"]?([A-Z0-9]{20})['"]?`)
	if re.MatchString(value) {
		return SecretFinding{
			Type:     SecretTypeAWSKey,
			Location: location,
			Context:  extractContext(value, 30),
		}
	}

	// AWS secret access key: 40 alphanumeric chars
	re2 := regexp.MustCompile(`[A-Za-z0-9/+]{40}`)
	if re2.MatchString(value) && strings.Contains(strings.ToLower(location), "secret") {
		return SecretFinding{
			Type:     SecretTypeAWSKey,
			Location: location,
			Context:  maskString(value),
		}
	}

	return SecretFinding{}
}

// checkGitHubToken detects GitHub personal access tokens
func (s *SecretScanner) checkGitHubToken(value, location string) SecretFinding {
	// GitHub PAT: ghp_ followed by 36 alphanumeric chars
	re := regexp.MustCompile(`ghp_[a-zA-Z0-9]{36}`)
	if re.MatchString(value) {
		return SecretFinding{
			Type:     SecretTypeGitHubToken,
			Location: location,
			Context:  "ghp_" + maskString(value[4:]),
		}
	}

	// GitHub app tokens: gho_, ghu_, ghs_, ghr_
	re2 := regexp.MustCompile(`gh[ohus]_[a-zA-Z0-9]{36}`)
	if re2.MatchString(value) {
		return SecretFinding{
			Type:     SecretTypeGitHubToken,
			Location: location,
			Context:  maskString(value[:4]) + maskString(value[4:]),
		}
	}

	return SecretFinding{}
}

// checkJWT detects JSON Web Tokens
func (s *SecretScanner) checkJWT(value, location string) SecretFinding {
	// JWT: three base64-encoded parts separated by dots
	re := regexp.MustCompile(`^[a-zA-Z0-9_-]+\.[a-zA-Z0-9_-]+\.[a-zA-Z0-9_-]+$`)
	if re.MatchString(value) && len(value) > 30 {
		// Verify it's actually base64
		parts := strings.Split(value, ".")
		if len(parts) == 3 {
			if _, err := base64.RawURLEncoding.DecodeString(parts[0]); err == nil {
				return SecretFinding{
					Type:     SecretTypeJWT,
					Location: location,
					Context:  maskString(value[:20]) + "..." + maskString(value[len(value)-10:]),
				}
			}
		}
	}

	return SecretFinding{}
}

// extractContext extracts a safe context string without exposing the secret
func extractContext(value string, maxLen int) string {
	if len(value) <= maxLen {
		return maskString(value)
	}
	return maskString(value[:maxLen]) + "..."
}

// maskString replaces most characters with asterisks
func maskString(s string) string {
	if len(s) <= 4 {
		return "****"
	}
	return s[:2] + "****" + s[len(s)-2:]
}
