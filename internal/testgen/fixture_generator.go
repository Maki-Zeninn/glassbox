// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package testgen provides helpers for generating regression test fixtures.
// The FixtureGenerator produces minimal, byte-stable, offline fixtures for
// each supported regression layer (rpc, trace, sourcemap, session, audit,
// replay, cli).
//
// Generated fixtures:
//   - Use canonical stub values from internal/testhelpers (no real identifiers).
//   - Contain no secrets, production keys, or live transaction data.
//   - Include issue linkage and a failure_class field where the layer spec
//     requires it.
//   - Are deterministic: running the generator twice with the same inputs
//     produces identical bytes.
package testgen

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Layer identifies the regression fixture layer.
type Layer string

const (
	LayerRPC       Layer = "rpc"
	LayerTrace     Layer = "trace"
	LayerSourcemap Layer = "sourcemap"
	LayerSession   Layer = "session"
	LayerAudit     Layer = "audit"
	LayerReplay    Layer = "replay"
	LayerCLI       Layer = "cli"
)

// AllLayers lists every supported layer in the canonical order used by the
// fixture directory layout.
var AllLayers = []Layer{
	LayerRPC, LayerTrace, LayerSourcemap, LayerSession,
	LayerAudit, LayerReplay, LayerCLI,
}

// ── Canonical stub constants ───────────────────────────────────────────────────
// These mirror the values in internal/testhelpers so that the generated JSON
// files are always consistent with Go test helpers.

const (
	canonicalTxHash        = "5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab"
	canonicalNetwork       = "testnet"
	canonicalEnvelopeXDR   = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	canonicalTimestamp     = "2026-01-01T00:00:00Z"
	canonicalSessionPrefix = "sess_test_"
	canonicalContractID    = "CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABSC4"
	canonicalSchemaVersion = 1
)

// FixtureRequest describes a single fixture to generate.
type FixtureRequest struct {
	// Layer is the regression layer (rpc, trace, …).
	Layer Layer
	// ScenarioSlug is a short description of the failure scenario,
	// used in the filename (e.g. "gettransaction_notfound").
	ScenarioSlug string
	// IssueRef is the associated issue or PR identifier, e.g. "issue860".
	IssueRef string
	// OutputDir is the directory to write the fixture into.  If empty, the
	// default test/regression/fixtures/<layer>/ layout is assumed relative
	// to the repository root.
	OutputDir string
	// FailureClass is a human-readable description embedded in the fixture,
	// e.g. "response not found silently ignored".  If empty it defaults to
	// ScenarioSlug with underscores replaced by spaces.
	FailureClass string
}

// FixtureResult records the outcome of a single generation.
type FixtureResult struct {
	Path  string
	Layer Layer
	Err   error
}

// FixtureGenerator produces minimal, offline regression fixtures for each
// supported layer.
type FixtureGenerator struct {
	// RepoRoot is the repository root directory.  Defaults to the current
	// working directory when not set explicitly.
	RepoRoot string
}

// NewFixtureGenerator creates a FixtureGenerator rooted at the given
// repository root directory.
func NewFixtureGenerator(repoRoot string) *FixtureGenerator {
	if repoRoot == "" {
		repoRoot = "."
	}
	return &FixtureGenerator{RepoRoot: repoRoot}
}

// Generate produces a fixture file for each request and returns the results.
func (g *FixtureGenerator) Generate(requests []FixtureRequest) []FixtureResult {
	results := make([]FixtureResult, 0, len(requests))
	for _, req := range requests {
		path, err := g.generateOne(req)
		results = append(results, FixtureResult{Path: path, Layer: req.Layer, Err: err})
	}
	return results
}

// GenerateOne produces a single fixture file.
func (g *FixtureGenerator) GenerateOne(req FixtureRequest) (string, error) {
	return g.generateOne(req)
}

func (g *FixtureGenerator) generateOne(req FixtureRequest) (string, error) {
	if err := validateRequest(req); err != nil {
		return "", err
	}

	outputDir := req.OutputDir
	if outputDir == "" {
		outputDir = filepath.Join(g.RepoRoot, "test", "regression", "fixtures", string(req.Layer))
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create output directory %q: %w", outputDir, err)
	}

	filename := buildFilename(req)
	path := filepath.Join(outputDir, filename)

	payload, err := buildPayload(req)
	if err != nil {
		return "", fmt.Errorf("failed to build payload for layer %s: %w", req.Layer, err)
	}

	data, err := marshalDeterministic(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal fixture: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", fmt.Errorf("failed to write fixture to %q: %w", path, err)
	}

	return path, nil
}

// ── Validation ─────────────────────────────────────────────────────────────────

func validateRequest(req FixtureRequest) error {
	if req.Layer == "" {
		return fmt.Errorf("Layer is required")
	}
	validLayer := false
	for _, l := range AllLayers {
		if req.Layer == l {
			validLayer = true
			break
		}
	}
	if !validLayer {
		return fmt.Errorf("unsupported layer %q; must be one of: %s", req.Layer, joinLayers(AllLayers))
	}
	if req.ScenarioSlug == "" {
		return fmt.Errorf("ScenarioSlug is required")
	}
	if strings.ContainsAny(req.ScenarioSlug, "/\\ ") {
		return fmt.Errorf("ScenarioSlug must not contain path separators or spaces")
	}
	return nil
}

// ── Filename construction ───────────────────────────────────────────────────────

func buildFilename(req FixtureRequest) string {
	parts := []string{req.ScenarioSlug}
	if req.IssueRef != "" {
		parts = append(parts, req.IssueRef)
	}
	base := strings.Join(parts, "_")

	switch req.Layer {
	case LayerRPC:
		return base + ".json"
	case LayerTrace:
		return base + ".trace.json"
	case LayerSourcemap:
		return base + ".json"
	case LayerSession:
		return base + ".session.json"
	case LayerAudit:
		return base + ".payload.json"
	case LayerReplay:
		return base + ".json"
	case LayerCLI:
		return base + ".env.json"
	default:
		return base + ".json"
	}
}

// ── Payload builders ───────────────────────────────────────────────────────────

func buildPayload(req FixtureRequest) (map[string]interface{}, error) {
	fc := req.FailureClass
	if fc == "" {
		fc = strings.ReplaceAll(req.ScenarioSlug, "_", " ")
	}

	meta := map[string]interface{}{
		"generated_by":  "internal/testgen.FixtureGenerator",
		"layer":         string(req.Layer),
		"failure_class": fc,
		"issue_ref":     req.IssueRef,
		"generated_at":  canonicalTimestamp,
	}

	switch req.Layer {
	case LayerRPC:
		return buildRPCPayload(meta), nil
	case LayerTrace:
		return buildTracePayload(meta), nil
	case LayerSourcemap:
		return buildSourcemapPayload(meta), nil
	case LayerSession:
		return buildSessionPayload(meta), nil
	case LayerAudit:
		return buildAuditPayload(meta), nil
	case LayerReplay:
		return buildReplayPayload(meta), nil
	case LayerCLI:
		return buildCLIPayload(meta), nil
	default:
		return nil, fmt.Errorf("unsupported layer: %s", req.Layer)
	}
}

func buildRPCPayload(meta map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"_meta":          meta,
		"id":             1,
		"jsonrpc":        "2.0",
		"result": map[string]interface{}{
			"status":        "SUCCESS",
			"txHash":        canonicalTxHash,
			"envelopeXdr":   canonicalEnvelopeXDR,
			"resultMetaXdr": canonicalEnvelopeXDR,
			"ledger":        1000,
			"createdAt":     1735689600,
		},
	}
}

func buildTracePayload(meta map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"_meta":          meta,
		"schema_version": canonicalSchemaVersion,
		"tx_hash":        canonicalTxHash,
		"network":        canonicalNetwork,
		"start_time":     canonicalTimestamp,
		"end_time":       canonicalTimestamp,
		"states": []interface{}{
			map[string]interface{}{
				"step":        0,
				"operation":   "contract_call",
				"event_type":  "contract_call",
				"contract_id": canonicalContractID,
				"function":    "invoke",
				"timestamp":   canonicalTimestamp,
				"arguments":   []interface{}{},
			},
		},
	}
}

func buildSourcemapPayload(meta map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"_meta":       meta,
		"contract_id": canonicalContractID,
		"wasm_hash":   strings.Repeat("0", 64),
		"repository":  "https://github.com/example/contract",
		"files": map[string]interface{}{
			"lib.rs": "// stub source — generated by FixtureGenerator",
		},
		"fetched_at": canonicalTimestamp,
	}
}

func buildSessionPayload(meta map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"_meta":          meta,
		"schema_version": canonicalSchemaVersion,
		"id":             canonicalSessionPrefix + canonicalTxHash[:8],
		"tx_hash":        canonicalTxHash,
		"network":        canonicalNetwork,
		"status":         "active",
		"created_at":     canonicalTimestamp,
		"last_access_at": canonicalTimestamp,
		"envelope_xdr":   canonicalEnvelopeXDR,
		"result_meta_xdr": canonicalEnvelopeXDR,
	}
}

func buildAuditPayload(meta map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"_meta":     meta,
		"input":     map[string]interface{}{},
		"state":     map[string]interface{}{},
		"events":    []interface{}{},
		"timestamp": canonicalTimestamp,
	}
}

func buildReplayPayload(meta map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"_meta":          meta,
		"schema_version": canonicalSchemaVersion,
		"tx_hash":        canonicalTxHash,
		"network":        canonicalNetwork,
		"envelope_xdr":   canonicalEnvelopeXDR,
		"result_meta_xdr": canonicalEnvelopeXDR,
		"ledger_entries": map[string]interface{}{},
		"snapshot_at":    canonicalTimestamp,
	}
}

func buildCLIPayload(meta map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"_meta": meta,
		"command": []string{"glassbox", "debug", "--network", canonicalNetwork, canonicalTxHash},
		"env": map[string]interface{}{
			"GLASSBOX_TELEMETRY": "false",
		},
		"expected_exit_code": 0,
		"expected_output_contains": []interface{}{},
	}
}

// ── Deterministic JSON serialisation ──────────────────────────────────────────

// marshalDeterministic produces JSON with sorted keys and a trailing newline so
// that the output is byte-stable across runs and Go versions.
func marshalDeterministic(v interface{}) ([]byte, error) {
	// encoding/json sorts map keys lexicographically, which is sufficient for
	// determinism when all keys are strings.
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// ── Helpers ─────────────────────────────────────────────────────────────────────

func joinLayers(ls []Layer) string {
	ss := make([]string, len(ls))
	for i, l := range ls {
		ss[i] = string(l)
	}
	return strings.Join(ss, ", ")
}


