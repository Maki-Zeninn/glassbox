// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package e2eharness_test

import (
	"context"
	"testing"
	"time"

	"github.com/dotandev/glassbox/internal/e2eharness"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func defaultStub() *e2eharness.RPCTransactionStub {
	return &e2eharness.RPCTransactionStub{
		EnvelopeXdr:   e2eharness.CanonicalEnvelopeXDR,
		ResultXdr:     e2eharness.CanonicalEnvelopeXDR,
		ResultMetaXdr: e2eharness.CanonicalEnvelopeXDR,
	}
}

func TestDefaultScenariosRunDeterministically(t *testing.T) {
	harness := e2eharness.NewHarness()
	scenarios := e2eharness.DefaultScenarios()
	require.NotEmpty(t, scenarios, "DefaultScenarios must not be empty")

	for _, m := range scenarios {
		m := m
		t.Run(m.Name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			r1 := harness.RunScenario(ctx, m)
			r2 := harness.RunScenario(ctx, m)
			require.Equal(t, r1.Passed, r2.Passed,
				"scenario %q: determinism violated — run 1 passed=%v, run 2 passed=%v",
				m.Name, r1.Passed, r2.Passed)
			if r1.SimResponse != nil && r2.SimResponse != nil {
				assert.Equal(t, r1.SimResponse.Status, r2.SimResponse.Status,
					"scenario %q: SimResponse.Status differs between runs", m.Name)
			}
		})
	}
}

func TestSuccessFlowPassesE2E(t *testing.T) {
	harness := e2eharness.NewHarness()
	ctx := context.Background()

	m := &e2eharness.ScenarioManifest{
		Name:            "success-e2e",
		TxHash:          e2eharness.CanonicalTxHash,
		Network:         "testnet",
		RPCResponse:     defaultStub(),
		Runner:          e2eharness.NewRunnerForBehaviour(e2eharness.SimSuccess),
		Timestamp:       1735689600,
		ExpectedOutcome: e2eharness.ExpectedOutcome{SimStatus: "success"},
	}

	result := harness.RunScenario(ctx, m)

	assert.True(t, result.Passed, "success scenario should pass; failures: %v", result.Failures)
	require.NotNil(t, result.SimResponse)
	assert.Equal(t, "success", result.SimResponse.Status)
	assert.Empty(t, result.ArtifactDir, "no artifacts retained on success")
}

func TestFailureFlowRetainsArtifacts(t *testing.T) {
	harness := e2eharness.NewHarness()
	ctx := context.Background()

	m := &e2eharness.ScenarioManifest{
		Name:        "artifact-retention-check",
		TxHash:      e2eharness.CanonicalTxHash,
		Network:     "testnet",
		RPCResponse: defaultStub(),
		Runner:      e2eharness.NewRunnerForBehaviour(e2eharness.SimFailure),
		Timestamp:   1735689600,
		// Wrong expectation — forces a failure so artifacts are retained.
		ExpectedOutcome: e2eharness.ExpectedOutcome{SimStatus: "success"},
	}

	result := harness.RunScenario(ctx, m)

	assert.False(t, result.Passed)
	assert.NotEmpty(t, result.ArtifactDir)
	assert.NotEmpty(t, result.Failures)
}

func TestContractTrapFailureFlow(t *testing.T) {
	harness := e2eharness.NewHarness()
	ctx := context.Background()

	m := &e2eharness.ScenarioManifest{
		Name:        "contract-trap-e2e",
		TxHash:      e2eharness.CanonicalTxHash,
		Network:     "testnet",
		RPCResponse: defaultStub(),
		Runner:      e2eharness.NewRunnerForBehaviour(e2eharness.SimFailure),
		Timestamp:   1735689600,
		ExpectedOutcome: e2eharness.ExpectedOutcome{
			SimStatus: "error", ErrorContains: "trap", EventCount: 1,
		},
	}

	result := harness.RunScenario(ctx, m)

	assert.True(t, result.Passed, "contract trap scenario; failures: %v", result.Failures)
	require.NotNil(t, result.SimResponse)
	assert.Equal(t, "error", result.SimResponse.Status)
	assert.Contains(t, result.SimResponse.Error, "trap")
	assert.GreaterOrEqual(t, len(result.SimResponse.DiagnosticEvents), 1)
}

func TestBudgetExhaustedFlow(t *testing.T) {
	harness := e2eharness.NewHarness()
	ctx := context.Background()

	m := &e2eharness.ScenarioManifest{
		Name:        "budget-exhausted-e2e",
		TxHash:      e2eharness.CanonicalTxHash,
		Network:     "testnet",
		RPCResponse: defaultStub(),
		Runner:      e2eharness.NewRunnerForBehaviour(e2eharness.SimBudgetExhausted),
		Timestamp:   1735689600,
		ExpectedOutcome: e2eharness.ExpectedOutcome{
			SimStatus: "error", ErrorContains: "budget",
		},
	}

	result := harness.RunScenario(ctx, m)

	assert.True(t, result.Passed, "budget exhausted; failures: %v", result.Failures)
	require.NotNil(t, result.SimResponse)
	assert.Equal(t, "error", result.SimResponse.Status)
	assert.NotNil(t, result.SimResponse.BudgetUsage)
	assert.Equal(t, uint64(100000000), result.SimResponse.BudgetUsage.CPULimit)
}

func TestNetworkErrorPropagates(t *testing.T) {
	harness := e2eharness.NewHarness()
	ctx := context.Background()

	m := &e2eharness.ScenarioManifest{
		Name:            "network-error-e2e",
		TxHash:          e2eharness.CanonicalTxHash,
		Network:         "testnet",
		RPCResponse:     defaultStub(),
		Runner:          e2eharness.NewRunnerForBehaviour(e2eharness.SimNetworkError),
		Timestamp:       1735689600,
		ExpectedOutcome: e2eharness.ExpectedOutcome{},
	}

	result := harness.RunScenario(ctx, m)

	assert.False(t, result.Passed)
	assert.NotEmpty(t, result.Failures)
	assert.Contains(t, result.Failures[0], "pipeline error")
	assert.NotEmpty(t, result.ArtifactDir)
}

func TestNoLiveNetworkRequired(t *testing.T) {
	harness := e2eharness.NewHarness()
	ctx := context.Background()

	for _, m := range e2eharness.DefaultScenarios() {
		m := m
		t.Run(m.Name, func(t *testing.T) {
			t.Parallel()
			result := harness.RunScenario(ctx, m)
			assert.NotNil(t, result)
			assert.Greater(t, result.Duration, 0*time.Nanosecond)
		})
	}
}

func TestScenarioManifestDocumentsExpectedOutcome(t *testing.T) {
	for _, m := range e2eharness.DefaultScenarios() {
		m := m
		t.Run(m.Name, func(t *testing.T) {
			assert.NotEmpty(t, m.ExpectedOutcome.SimStatus,
				"scenario %q must declare ExpectedOutcome.SimStatus", m.Name)
		})
	}
}
