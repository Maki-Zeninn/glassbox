// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package compare

import (
	"bytes"
	"strings"
	"testing"

	"github.com/dotandev/glassbox/internal/simulator"
	"github.com/stretchr/testify/assert"
)

// TestRender_BasicDiff verifies that Render produces output containing
// expected format elements for a basic diff between testnet and mainnet.
func TestRender_BasicDiff(t *testing.T) {
	// Construct two minimal SimulationResponse values with different CPU costs
	local := &simulator.SimulationResponse{
		Status: "success",
		Events: []string{"evt:mint", "evt:transfer"},
		BudgetUsage: &simulator.BudgetUsage{
			CPUInstructions: 2000,
			MemoryBytes:     512,
			OperationsCount: 3,
		},
	}

	onChain := &simulator.SimulationResponse{
		Status: "success",
		Events: []string{"evt:mint", "evt:burn"},
		BudgetUsage: &simulator.BudgetUsage{
			CPUInstructions: 1500,
			MemoryBytes:     256,
			OperationsCount: 2,
		},
	}

	// Create a diff result
	result := Diff(local, onChain)

	// Capture output to a buffer
	var buf bytes.Buffer
	RenderTo(result, &buf)
	output := buf.String()

	// Assert output is non-empty
	assert.NotEmpty(t, output, "Render output should not be empty")

	// Assert the output contains expected format elements
	assert.Contains(t, output, "COMPARE REPLAY", "output should contain header")
	assert.Contains(t, output, "Local WASM", "output should indicate local side")
	assert.Contains(t, output, "On-Chain WASM", "output should indicate on-chain side")
	assert.Contains(t, output, "────", "output should contain dividing line")
	assert.Contains(t, output, "Execution Status", "output should contain status section")
	assert.Contains(t, output, "Resource Usage", "output should contain budget section")

	// Verify budget diff is visible
	assert.Contains(t, output, "CPU Instructions", "output should show CPU metric")
	assert.Contains(t, output, "Memory Bytes", "output should show memory metric")
}

// TestRender_NetworkNamesInOutput verifies that the render output contains
// network identifiers for side-by-side comparison.
func TestRender_NetworkNamesInOutput(t *testing.T) {
	local := &simulator.SimulationResponse{
		Status:      "success",
		Events:      []string{"evt:transfer"},
		BudgetUsage: &simulator.BudgetUsage{CPUInstructions: 1000},
	}
	onChain := &simulator.SimulationResponse{
		Status:      "success",
		Events:      []string{"evt:transfer"},
		BudgetUsage: &simulator.BudgetUsage{CPUInstructions: 1100},
	}

	result := Diff(local, onChain)
	var buf bytes.Buffer
	RenderTo(result, &buf)
	output := buf.String()

	// The header should contain network labels
	assert.Contains(t, output, "Local WASM", "output should mention Local WASM")
	assert.Contains(t, output, "On-Chain WASM", "output should mention On-Chain WASM")

	// The header should contain visual separators
	assert.Contains(t, output, "─", "output should contain horizontal rule characters")
}

// TestRender_DivergentStatus verifies output for mismatched execution status.
func TestRender_DivergentStatus(t *testing.T) {
	local := &simulator.SimulationResponse{
		Status: "error",
		Error:  "out of CPU",
	}
	onChain := &simulator.SimulationResponse{
		Status: "success",
	}

	result := Diff(local, onChain)
	var buf bytes.Buffer
	RenderTo(result, &buf)
	output := buf.String()

	assert.Contains(t, output, "[DIFF]", "output should mark status difference")
	assert.Contains(t, output, "error", "output should show local status")
	assert.Contains(t, output, "success", "output should show on-chain status")
}

// TestRender_SummarySection verifies the summary section is rendered correctly.
func TestRender_SummarySection(t *testing.T) {
	local := &simulator.SimulationResponse{
		Status: "success",
		Events: []string{"e1", "e2", "e3"},
	}
	onChain := &simulator.SimulationResponse{
		Status: "success",
		Events: []string{"e1", "e2", "X3"},
	}

	result := Diff(local, onChain)
	var buf bytes.Buffer
	RenderTo(result, &buf)
	output := buf.String()

	// Summary section should be present
	assert.Contains(t, output, "Summary", "output should contain Summary section")
	assert.Contains(t, output, "Total events compared:", "output should show total events")
	assert.Contains(t, output, "Identical events:", "output should show identical count")
	assert.Contains(t, output, "Divergent events:", "output should show divergent count")

	// With 3 events and 1 divergent
	assert.True(t, strings.Contains(output, "3") && strings.Contains(output, "2") && strings.Contains(output, "1"),
		"output should show correct event counts")
}

// TestRender_NilResult_NoError verifies RenderTo does not panic with nil result.
func TestRender_NilResult_NoError(t *testing.T) {
	var buf bytes.Buffer
	assert.NotPanics(t, func() {
		RenderTo(nil, &buf)
	})
	assert.Empty(t, buf.String(), "output should be empty for nil result")
}

// TestRender_NilWriter_DoesNotPanic verifies RenderTo handles nil writer.
func TestRender_NilWriter_DoesNotPanic(t *testing.T) {
	local := &simulator.SimulationResponse{
		Status: "success",
	}
	onChain := &simulator.SimulationResponse{
		Status: "success",
	}

	result := Diff(local, onChain)
	assert.NotPanics(t, func() {
		RenderTo(result, nil)
	})
}
// TestRenderCrossCheck_Basic verifies RenderCrossCheckTo produces output.
func TestRenderCrossCheck_Basic(t *testing.T) {
	localDiag := &simulator.FailureDiagnostic{
		Category: simulator.FailureCPUBudget,
		Summary:  "CPU budget exceeded",
	}
	networkReason := "execution ran out of instructions"

	result := CrossCheckFailures(localDiag, networkReason)
	var buf bytes.Buffer
	RenderCrossCheckTo(result, &buf)
	output := buf.String()

	assert.NotEmpty(t, output, "RenderCrossCheck output should not be empty")
	assert.Contains(t, output, "Simulation vs Network Cross-Check", "output should contain header")
	assert.Contains(t, output, "CPU budget exceeded", "output should contain local summary")
	assert.Contains(t, output, "execution ran out of instructions", "output should contain network reason")
}

// TestRenderCrossCheck_Match verifies output when categories match.
func TestRenderCrossCheck_Match(t *testing.T) {
	localDiag := &simulator.FailureDiagnostic{
		Category: simulator.FailureCPUBudget,
		Summary:  "CPU limit reached",
	}
	networkReason := "CpuLimitExceeded: execution ran out of instructions"

	result := CrossCheckFailures(localDiag, networkReason)
	var buf bytes.Buffer
	RenderCrossCheckTo(result, &buf)
	output := buf.String()

	assert.Contains(t, output, "Categories agree", "output should indicate agreement")
	assert.Contains(t, output, "CPU", "output should mention the category")
}

// TestRenderCrossCheck_NilResult_NoError verifies RenderCrossCheckTo handles nil.
func TestRenderCrossCheck_NilResult_NoError(t *testing.T) {
	var buf bytes.Buffer
	assert.NotPanics(t, func() {
		RenderCrossCheckTo(nil, &buf)
	})
}

// TestRenderCrossCheck_NilWriter_DoesNotPanic verifies RenderCrossCheckTo handles nil writer.
func TestRenderCrossCheck_NilWriter_DoesNotPanic(t *testing.T) {
	result := &CrossCheckResult{}
	assert.NotPanics(t, func() {
		RenderCrossCheckTo(result, nil)
	})
}
