// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package simulator

// fixture_generator.go — Sanitized simulator fixture generation from captured transactions.
// Issue #535: Add simulator fixture generation from captured transactions
//
// Converts a replay bundle into a versioned simulator fixture with expected
// result and trace fingerprints. Secrets are redacted; generation is deterministic.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// FixtureManifest contains metadata about a generated fixture.
type FixtureManifest struct {
	// SchemaVersion of the fixture format.
	SchemaVersion string `json:"schema_version"`
	// GeneratedAt timestamp (UTC).
	GeneratedAt string `json:"generated_at"`
	// SimulatorVersion that produced the fixture.
	SimulatorVersion string `json:"simulator_version"`
	// ProtocolVersion of the Stellar network the fixture targets.
	ProtocolVersion uint32 `json:"protocol_version"`
	// Network the fixture was captured from.
	Network string `json:"network"`
	// TxHash of the original transaction (fingerprinted for privacy).
	TxHashFingerprint string `json:"tx_hash_fingerprint"`
	// TraceFingerprint is the SHA-256 of the expected trace.
	TraceFingerprint string `json:"trace_fingerprint"`
	// ResultFingerprint is the SHA-256 of the expected result.
	ResultFingerprint string `json:"result_fingerprint"`
	// RedactedFields lists which fields were sanitized.
	RedactedFields []string `json:"redacted_fields"`
}

// GeneratedFixture is the complete fixture bundle written to disk.
type GeneratedFixture struct {
	Manifest    FixtureManifest    `json:"manifest"`
	Request     json.RawMessage    `json:"request"`
	ExpectedResult *SimulationResponse `json:"expected_result"`
	ExpectedTrace  json.RawMessage    `json:"expected_trace,omitempty"`
}

// FixtureGeneratorConfig controls fixture generation behavior.
type FixtureGeneratorConfig struct {
	// RedactPatterns are field names to redact (replaced with "REDACTED").
	RedactPatterns []string
	// OutputDir is where the fixture files are written.
	OutputDir string
	// IncludeTrace controls whether the full trace is embedded.
	IncludeTrace bool
}

// DefaultFixtureGeneratorConfig returns sensible defaults for fixture generation.
func DefaultFixtureGeneratorConfig(outputDir string) FixtureGeneratorConfig {
	return FixtureGeneratorConfig{
		RedactPatterns: []string{
			"secret", "private_key", "seed", "mnemonic", "password",
			"api_key", "token", "authorization",
		},
		OutputDir:    outputDir,
		IncludeTrace: true,
	}
}

// GenerateFixture creates a sanitized, deterministic simulator fixture from a
// captured transaction replay bundle.
//
// Parameters:
//   - txHash: Original transaction hash (will be fingerprinted)
//   - req: The simulation request (will be redacted)
//   - resp: The simulation response (used as expected result)
//   - traceBytes: The trace JSON (optional, for fingerprint)
//   - config: Generator configuration
//
// Returns the fixture and the path it was written to.
func GenerateFixture(
	txHash string,
	req json.RawMessage,
	resp *SimulationResponse,
	traceBytes []byte,
	protocolVersion uint32,
	network string,
	config FixtureGeneratorConfig,
) (*GeneratedFixture, string, error) {
	// 1. Redact sensitive fields from the request
	redactedReq, redactedFields := redactSensitive(req, config.RedactPatterns)

	// 2. Compute fingerprints
	// Sort the request keys for determinism
	var reqMap map[string]interface{}
	if err := json.Unmarshal(redactedReq, &reqMap); err == nil {
		redactedReq, _ = json.Marshal(sortKeys(reqMap))
	}

	reqFingerprint := sha256Hex(redactedReq)
	traceFingerprint := ""
	if len(traceBytes) > 0 {
		traceFingerprint = sha256Hex(traceBytes)
	}

	resultBytes, _ := json.Marshal(resp)
	resultFingerprint := sha256Hex(resultBytes)

	// 3. Build manifest
	manifest := FixtureManifest{
		SchemaVersion:     "1.0.0",
		GeneratedAt:       time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		SimulatorVersion:  "glassbox-1.0",
		ProtocolVersion:   protocolVersion,
		Network:           network,
		TxHashFingerprint: "sha256:" + sha256Hex([]byte(txHash)),
		TraceFingerprint:  "sha256:" + traceFingerprint,
		ResultFingerprint: "sha256:" + resultFingerprint,
		RedactedFields:    redactedFields,
	}

	// 4. Build fixture
	fixture := &GeneratedFixture{
		Manifest:        manifest,
		Request:         redactedReq,
		ExpectedResult:  resp,
	}
	if config.IncludeTrace && len(traceBytes) > 0 {
		fixture.ExpectedTrace = json.RawMessage(traceBytes)
	}

	// 5. Write to disk (deterministic file name based on fingerprint)
	if config.OutputDir != "" {
		if err := os.MkdirAll(config.OutputDir, 0755); err != nil {
			return nil, "", fmt.Errorf("create output dir: %w", err)
		}
		fileName := fmt.Sprintf("fixture-%s.json", reqFingerprint[:12])
		path := filepath.Join(config.OutputDir, fileName)

		data, err := json.MarshalIndent(fixture, "", "  ")
		if err != nil {
			return nil, "", fmt.Errorf("marshal fixture: %w", err)
		}
		if err := os.WriteFile(path, data, 0644); err != nil {
			return nil, "", fmt.Errorf("write fixture: %w", err)
		}
		return fixture, path, nil
	}

	return fixture, "", nil
}

// redactSensitive replaces values of fields matching the given patterns
// with "REDACTED". Returns the redacted JSON and a list of redacted field names.
func redactSensitive(data json.RawMessage, patterns []string) (json.RawMessage, []string) {
	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		return data, nil
	}

	var redacted []string
	redactedData := redactRecursive(v, patterns, &redacted)

	result, _ := json.Marshal(redactedData)
	return result, redacted
}

func redactRecursive(v interface{}, patterns []string, redacted *[]string) interface{} {
	switch val := v.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{})
		for key, value := range val {
			if isSensitive(key, patterns) {
				result[key] = "REDACTED"
				*redacted = appendIfMissing(*redacted, key)
			} else {
				result[key] = redactRecursive(value, patterns, redacted)
			}
		}
		return result
	case []interface{}:
		result := make([]interface{}, len(val))
		for i, item := range val {
			result[i] = redactRecursive(item, patterns, redacted)
		}
		return result
	default:
		return v
	}
}

func isSensitive(key string, patterns []string) bool {
	lowerKey := strings.ToLower(key)
	for _, p := range patterns {
		if strings.Contains(lowerKey, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

func appendIfMissing(s []string, v string) []string {
	for _, existing := range s {
		if existing == v {
			return s
		}
	}
	return append(s, v)
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func sortKeys(m map[string]interface{}) map[string]interface{} {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	result := make(map[string]interface{})
	for _, k := range keys {
		result[k] = m[k]
	}
	return result
}

// VerifyFixtureFingerprint checks that a fixture's expected result fingerprint
// matches the actual result, detecting regressions.
func VerifyFixtureFingerprint(fixture *GeneratedFixture, actualResult *SimulationResponse) bool {
	if fixture == nil {
		return false
	}
	actualBytes, _ := json.Marshal(actualResult)
	actualFp := sha256Hex(actualBytes)
	return fixture.Manifest.ResultFingerprint == "sha256:"+actualFp
}
