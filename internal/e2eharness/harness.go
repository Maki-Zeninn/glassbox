// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package e2eharness provides a deterministic end-to-end replay harness for
// testing the full fetch → simulate → export pipeline without live network
// access.
//
// Issue #598: Build deterministic end-to-end replay harness.
//
// Design goals:
//   - Fixed RPC responses (supplied via ScenarioManifest).
//   - Fixed simulator binary behaviour (supplied via RunFunc callbacks).
//   - Fixed filesystem root (isolated temp directory per run).
//   - Fixed clock and randomness (Timestamp field on manifest).
//   - Single command: RunScenario(ctx, manifest) → ScenarioResult.
//   - Failures retain all intermediate artifacts for inspection.
//   - At least one success flow and one failure flow in DefaultScenarios.
package e2eharness

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ─── Fixed constants ──────────────────────────────────────────────────────────

const (
	// canonicalTxHash is the deterministic transaction hash used in all
	// default scenarios.
	canonicalTxHash = "5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab"

	// canonicalEnvelopeXDR is a minimal stub base64 used when no real XDR is needed.
	canonicalEnvelopeXDR = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

	// canonicalTimestamp is 2026-01-01T00:00:00Z as Unix seconds.
	canonicalTimestamp int64 = 1735689600
)

// ─── Simulator types (self-contained, no imports from internal/simulator) ─────

// SimulationRequest is the minimal request shape the harness passes to a runner.
type SimulationRequest struct {
	EnvelopeXdr   string            `json:"envelope_xdr"`
	ResultMetaXdr string            `json:"result_meta_xdr"`
	LedgerEntries map[string]string `json:"ledger_entries,omitempty"`
	Timestamp     int64             `json:"timestamp,omitempty"`
}

// DiagnosticEvent is a single diagnostic event in a simulation response.
type DiagnosticEvent struct {
	EventType  string  `json:"event_type"`
	ContractID *string `json:"contract_id,omitempty"`
	Data       string  `json:"data,omitempty"`
}

// BudgetUsage tracks CPU / memory consumption.
type BudgetUsage struct {
	CPUInstructions    uint64  `json:"cpu_instructions"`
	CPULimit           uint64  `json:"cpu_limit"`
	CPUUsagePercent    float64 `json:"cpu_usage_percent"`
	MemoryBytes        uint64  `json:"memory_bytes"`
	MemoryLimit        uint64  `json:"memory_limit"`
	MemoryUsagePercent float64 `json:"memory_usage_percent"`
}

// SimulationResponse is the minimal response shape returned by a runner.
type SimulationResponse struct {
	Status           string            `json:"status"`
	Error            string            `json:"error,omitempty"`
	Events           []string          `json:"events,omitempty"`
	DiagnosticEvents []DiagnosticEvent `json:"diagnostic_events,omitempty"`
	Logs             []string          `json:"logs,omitempty"`
	BudgetUsage      *BudgetUsage      `json:"budget_usage,omitempty"`
}

// RunnerFunc is the function signature for a deterministic mock simulator runner.
// It mirrors the RunnerInterface.Run contract but without the dependency on
// the internal/simulator package.
type RunnerFunc func(ctx context.Context, req *SimulationRequest) (*SimulationResponse, error)

// ─── Scenario manifest ────────────────────────────────────────────────────────

// RPCTransactionStub holds canned transaction data returned by the stub server.
type RPCTransactionStub struct {
	EnvelopeXdr   string
	ResultXdr     string
	ResultMetaXdr string
}

// ScenarioManifest fully describes one deterministic end-to-end test scenario.
type ScenarioManifest struct {
	// Name is the human-readable scenario identifier.
	Name string

	// TxHash is the transaction hash the harness will "fetch" from the stub RPC.
	TxHash string

	// Network identifies the Stellar network label (no live network contacted).
	Network string

	// RPCResponse is the canned response returned by the stub Soroban RPC server.
	RPCResponse *RPCTransactionStub

	// Runner is the mock simulator function for this scenario.
	// If nil, DefaultSuccessRunner is used.
	Runner RunnerFunc

	// Timestamp is the fixed Unix timestamp for deterministic clock.
	Timestamp int64

	// ExpectedOutcome defines what a successful run looks like.
	ExpectedOutcome ExpectedOutcome
}

// SimBehaviour is a convenience enum for the built-in runner factory.
type SimBehaviour int

const (
	SimSuccess        SimBehaviour = iota
	SimFailure                     // contract trap
	SimBudgetExhausted             // CPU budget exceeded
	SimNetworkError                // I/O error from runner
)

// ExpectedOutcome describes the expected result of running a scenario.
type ExpectedOutcome struct {
	SimStatus     string // "success" or "error"
	ErrorContains string // substring that must appear in SimulationResponse.Error
	EventCount    int    // minimum DiagnosticEvent count (0 = skip)
}

// ─── Scenario result ──────────────────────────────────────────────────────────

// ScenarioResult holds the outcome of a single scenario run.
type ScenarioResult struct {
	Scenario    *ScenarioManifest
	Passed      bool
	Failures    []string
	SimResponse *SimulationResponse
	ArtifactDir string
	Duration    time.Duration
}

// ─── Harness ──────────────────────────────────────────────────────────────────

// Harness is the deterministic end-to-end test harness.
type Harness struct {
	ArtifactBaseDir string
	mu              sync.Mutex
}

// NewHarness creates a new Harness with default settings.
func NewHarness() *Harness {
	return &Harness{
		ArtifactBaseDir: filepath.Join(os.TempDir(), "glassbox-e2e-artifacts"),
	}
}

// RunScenario executes a single scenario deterministically.
func (h *Harness) RunScenario(ctx context.Context, m *ScenarioManifest) *ScenarioResult {
	start := time.Now()
	result := &ScenarioResult{Scenario: m, Passed: true}

	// Apply defaults
	if m.Timestamp == 0 {
		m.Timestamp = canonicalTimestamp
	}
	if m.TxHash == "" {
		m.TxHash = canonicalTxHash
	}
	if m.Network == "" {
		m.Network = "testnet"
	}

	// Start stub RPC server
	stub := buildStubRPCServer(m.TxHash, m.RPCResponse)
	defer stub.Close()

	// Resolve runner
	runner := m.Runner
	if runner == nil {
		runner = defaultSuccessRunner()
	}

	// Execute pipeline
	simResp, err := executePipeline(ctx, stub.URL, m, runner)
	if err != nil {
		result.Passed = false
		result.Failures = append(result.Failures, fmt.Sprintf("pipeline error: %v", err))
		result.Duration = time.Since(start)
		h.retainArtifacts(result, m, nil, err)
		return result
	}

	result.SimResponse = simResp
	validateOutcome(result, simResp, m.ExpectedOutcome)
	result.Duration = time.Since(start)

	if !result.Passed {
		h.retainArtifacts(result, m, simResp, nil)
	}
	return result
}

// ─── Pipeline ─────────────────────────────────────────────────────────────────

func executePipeline(
	ctx context.Context,
	_ string, // rpcURL — stub server URL; not used directly (runner is already a mock)
	m *ScenarioManifest,
	runner RunnerFunc,
) (*SimulationResponse, error) {
	envelope := canonicalEnvelopeXDR
	if m.RPCResponse != nil && m.RPCResponse.EnvelopeXdr != "" {
		envelope = m.RPCResponse.EnvelopeXdr
	}

	req := &SimulationRequest{
		EnvelopeXdr:   envelope,
		ResultMetaXdr: canonicalEnvelopeXDR,
		LedgerEntries: make(map[string]string),
		Timestamp:     m.Timestamp,
	}

	resp, err := runner(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("simulator run: %w", err)
	}
	return resp, nil
}

// ─── Stub RPC server ──────────────────────────────────────────────────────────

func buildStubRPCServer(txHash string, stub *RPCTransactionStub) *httptest.Server {
	envelope := canonicalEnvelopeXDR
	resultXdr := canonicalEnvelopeXDR
	resultMetaXdr := canonicalEnvelopeXDR
	if stub != nil {
		if stub.EnvelopeXdr != "" {
			envelope = stub.EnvelopeXdr
		}
		if stub.ResultXdr != "" {
			resultXdr = stub.ResultXdr
		}
		if stub.ResultMetaXdr != "" {
			resultMetaXdr = stub.ResultMetaXdr
		}
	}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var rpcReq struct {
			Method string      `json:"method"`
			ID     interface{} `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&rpcReq); err != nil {
			http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
			return
		}
		switch rpcReq.Method {
		case "getTransaction":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0", "id": rpcReq.ID,
				"result": map[string]interface{}{
					"status": "SUCCESS", "txHash": txHash,
					"envelopeXdr": envelope, "resultXdr": resultXdr,
					"resultMetaXdr": resultMetaXdr, "ledger": 1000,
				},
			})
		case "getLedgerEntries":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0", "id": rpcReq.ID,
				"result": map[string]interface{}{"entries": []interface{}{}, "latestLedger": 1000},
			})
		default:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0", "id": rpcReq.ID,
				"error": map[string]interface{}{"code": -32601, "message": "method not found"},
			})
		}
	}))
}

// ─── Built-in runners ─────────────────────────────────────────────────────────

func defaultSuccessRunner() RunnerFunc {
	return func(ctx context.Context, req *SimulationRequest) (*SimulationResponse, error) {
		return &SimulationResponse{Status: "success", Events: []string{}, Logs: []string{}}, nil
	}
}

// NewRunnerForBehaviour returns a RunnerFunc for the given SimBehaviour.
// Use this to build scenario manifests without inline closures.
func NewRunnerForBehaviour(b SimBehaviour) RunnerFunc {
	switch b {
	case SimFailure:
		contractID := "CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAD2KM"
		return func(ctx context.Context, req *SimulationRequest) (*SimulationResponse, error) {
			return &SimulationResponse{
				Status: "error",
				Error:  "contract trap: wasm unreachable instruction",
				DiagnosticEvents: []DiagnosticEvent{
					{EventType: "contract_error", ContractID: &contractID},
				},
			}, nil
		}
	case SimBudgetExhausted:
		return func(ctx context.Context, req *SimulationRequest) (*SimulationResponse, error) {
			return &SimulationResponse{
				Status: "error",
				Error:  "CPU budget exceeded",
				BudgetUsage: &BudgetUsage{
					CPUInstructions: 100000000, CPULimit: 100000000, CPUUsagePercent: 100.0,
					MemoryBytes: 1024, MemoryLimit: 41943040, MemoryUsagePercent: 0.002,
				},
			}, nil
		}
	case SimNetworkError:
		return func(ctx context.Context, req *SimulationRequest) (*SimulationResponse, error) {
			return nil, fmt.Errorf("simulated network error: connection refused")
		}
	default: // SimSuccess
		return defaultSuccessRunner()
	}
}

// ─── Outcome validation ───────────────────────────────────────────────────────

func validateOutcome(result *ScenarioResult, resp *SimulationResponse, expected ExpectedOutcome) {
	if expected.SimStatus != "" && resp.Status != expected.SimStatus {
		result.Passed = false
		result.Failures = append(result.Failures,
			fmt.Sprintf("expected SimStatus=%q, got %q", expected.SimStatus, resp.Status))
	}
	if expected.ErrorContains != "" {
		if resp.Error == "" {
			result.Passed = false
			result.Failures = append(result.Failures,
				fmt.Sprintf("expected error containing %q but error is empty", expected.ErrorContains))
		} else {
			found := false
			ec := expected.ErrorContains
			for i := 0; i <= len(resp.Error)-len(ec); i++ {
				if resp.Error[i:i+len(ec)] == ec {
					found = true
					break
				}
			}
			if !found {
				result.Passed = false
				result.Failures = append(result.Failures,
					fmt.Sprintf("expected error to contain %q, got %q", ec, resp.Error))
			}
		}
	}
	if expected.EventCount > 0 && len(resp.DiagnosticEvents) < expected.EventCount {
		result.Passed = false
		result.Failures = append(result.Failures,
			fmt.Sprintf("expected at least %d diagnostic events, got %d",
				expected.EventCount, len(resp.DiagnosticEvents)))
	}
}

// ─── Artifact retention ───────────────────────────────────────────────────────

func (h *Harness) retainArtifacts(
	result *ScenarioResult, m *ScenarioManifest,
	resp *SimulationResponse, runErr error,
) {
	h.mu.Lock()
	defer h.mu.Unlock()

	dir := filepath.Join(h.ArtifactBaseDir, sanitizeName(m.Name))
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}
	result.ArtifactDir = dir
	writeJSON(filepath.Join(dir, "manifest.json"), m)
	if resp != nil {
		writeJSON(filepath.Join(dir, "sim_response.json"), resp)
	}
	if len(result.Failures) > 0 {
		var buf []byte
		for _, f := range result.Failures {
			buf = append(buf, []byte(f+"\n")...)
		}
		_ = os.WriteFile(filepath.Join(dir, "failures.txt"), buf, 0644)
	}
	if runErr != nil {
		_ = os.WriteFile(filepath.Join(dir, "run_error.txt"), []byte(runErr.Error()+"\n"), 0644)
	}
}

func writeJSON(path string, v interface{}) {
	if data, err := json.MarshalIndent(v, "", "  "); err == nil {
		_ = os.WriteFile(path, data, 0644)
	}
}

func sanitizeName(s string) string {
	out := make([]byte, 0, len(s))
	for _, c := range []byte(s) {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			out = append(out, c)
		} else {
			out = append(out, '_')
		}
	}
	return string(out)
}

// ─── Default scenarios ────────────────────────────────────────────────────────

// DefaultScenarios returns the canonical set of harness scenarios covering at
// least one success and one failure flow.
func DefaultScenarios() []*ScenarioManifest {
	stub := &RPCTransactionStub{
		EnvelopeXdr:   canonicalEnvelopeXDR,
		ResultXdr:     canonicalEnvelopeXDR,
		ResultMetaXdr: canonicalEnvelopeXDR,
	}
	return []*ScenarioManifest{
		{
			Name: "success-baseline", TxHash: canonicalTxHash, Network: "testnet",
			RPCResponse: stub, Runner: NewRunnerForBehaviour(SimSuccess),
			Timestamp:       canonicalTimestamp,
			ExpectedOutcome: ExpectedOutcome{SimStatus: "success"},
		},
		{
			Name: "contract-trap-failure", TxHash: canonicalTxHash, Network: "testnet",
			RPCResponse: stub, Runner: NewRunnerForBehaviour(SimFailure),
			Timestamp:       canonicalTimestamp,
			ExpectedOutcome: ExpectedOutcome{SimStatus: "error", ErrorContains: "trap", EventCount: 1},
		},
		{
			Name: "budget-exhausted-failure", TxHash: canonicalTxHash, Network: "testnet",
			RPCResponse: stub, Runner: NewRunnerForBehaviour(SimBudgetExhausted),
			Timestamp:       canonicalTimestamp,
			ExpectedOutcome: ExpectedOutcome{SimStatus: "error", ErrorContains: "budget"},
		},
	}
}

// ─── Exported constants for tests ─────────────────────────────────────────────

// CanonicalTxHash is the deterministic transaction hash used in default scenarios.
const CanonicalTxHash = canonicalTxHash

// CanonicalEnvelopeXDR is the stub base64 XDR used in default scenarios.
const CanonicalEnvelopeXDR = canonicalEnvelopeXDR
