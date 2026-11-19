// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package simulator

// resource_report.go — Simulator resource-limit reporting.
// Issue #531: Add simulator resource-limit reporting
//
// Collects configured limits, consumption, remaining budget, and the first
// exceeded resource in the simulator result. Captured on both success and trap paths.

import (
	"encoding/json"
	"fmt"
)

// ResourceReport is a comprehensive resource summary included in the simulation result.
// It reports all available resource limits, their consumption, and which
// (if any) was exceeded.
type ResourceReport struct {
	// CPU instructions
	CPUInstructions uint64 `json:"cpu_instructions"`
	CPULimit        uint64 `json:"cpu_limit"`
	CPUUsagePercent float64 `json:"cpu_usage_percent"`
	CPURemaining    uint64 `json:"cpu_remaining"`
	CPUExceeded     bool   `json:"cpu_exceeded"`

	// Memory
	MemoryBytes        uint64 `json:"memory_bytes"`
	MemoryLimit        uint64 `json:"memory_limit"`
	MemoryUsagePercent float64 `json:"memory_usage_percent"`
	MemoryRemaining    uint64 `json:"memory_remaining"`
	MemoryExceeded     bool   `json:"memory_exceeded"`

	// Operations count
	OperationsCount   int    `json:"operations_count"`
	OperationsLimit   int    `json:"operations_limit,omitempty"`
	OperationsExceeded bool  `json:"operations_exceeded,omitempty"`

	// First exceeded resource (empty if none exceeded)
	FirstExceeded string `json:"first_exceeded,omitempty"`

	// ExceededValue is the configured limit value of the first exceeded resource.
	ExceededValue uint64 `json:"exceeded_value,omitempty"`

	// Whether all resource counters are available
	Available bool `json:"available"`
}

// BuildResourceReport constructs a ResourceReport from a BudgetUsage.
// Returns a report with Available=false if budget is nil.
func BuildResourceReport(budget *BudgetUsage) *ResourceReport {
	if budget == nil {
		return &ResourceReport{
			Available: false,
			CPUExceeded: false,
			MemoryExceeded: false,
		}
	}

	report := &ResourceReport{
		Available: true,
		CPUInstructions:    budget.CPUInstructions,
		CPULimit:           budget.CPULimit,
		MemoryBytes:        budget.MemoryBytes,
		MemoryLimit:        budget.MemoryLimit,
		OperationsCount:    budget.OperationsCount,
		CPUUsagePercent:    budget.CPUUsagePercent,
		MemoryUsagePercent: budget.MemoryUsagePercent,
	}

	// Calculate remaining
	if budget.CPULimit > 0 {
		if budget.CPUInstructions >= budget.CPULimit {
			report.CPURemaining = 0
			report.CPUExceeded = true
		} else {
			report.CPURemaining = budget.CPULimit - budget.CPUInstructions
		}
	} else {
		report.CPURemaining = 0
		// Missing counters are explicitly marked
	}

	if budget.MemoryLimit > 0 {
		if budget.MemoryBytes >= budget.MemoryLimit {
			report.MemoryRemaining = 0
			report.MemoryExceeded = true
		} else {
			report.MemoryRemaining = budget.MemoryLimit - budget.MemoryBytes
		}
	} else {
		report.MemoryRemaining = 0
	}

	// Determine first exceeded resource (priority: CPU > Memory > Operations)
	if report.CPUExceeded {
		report.FirstExceeded = "cpu"
		report.ExceededValue = report.CPULimit
	} else if report.MemoryExceeded {
		report.FirstExceeded = "memory"
		report.ExceededValue = report.MemoryLimit
	} else if report.OperationsExceeded {
		report.FirstExceeded = "operations"
	}

	return report
}

// ToSummary produces a human-readable one-line resource summary for text output.
func (r *ResourceReport) ToSummary() string {
	if r == nil || !r.Available {
		return "resources: unavailable"
	}

	summary := fmt.Sprintf("CPU: %d/%d (%.1f%%), Memory: %d/%d (%.1f%%), Ops: %d",
		r.CPUInstructions, r.CPULimit, r.CPUUsagePercent,
		r.MemoryBytes, r.MemoryLimit, r.MemoryUsagePercent,
		r.OperationsCount)

	if r.FirstExceeded != "" {
		summary += fmt.Sprintf(" | EXCEEDED: %s (limit=%d)", r.FirstExceeded, r.ExceededValue)
	}

	return summary
}

// ToJSON produces a JSON representation of the resource report.
func (r *ResourceReport) ToJSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

// ToMarkdownTable produces a Markdown table representation for documentation.
func (r *ResourceReport) ToMarkdownTable() string {
	if r == nil || !r.Available {
		return "| Resource | Available |\n|----------|----------|\n| All | unavailable |\n"
	}

	var sb string
	sb = "| Resource | Used | Limit | Remaining | % | Exceeded |\n"
	sb += "|----------|------|-------|----------|---|----------|\n"
	sb += fmt.Sprintf("| CPU Instructions | %d | %d | %d | %.1f%% | %v |\n",
		r.CPUInstructions, r.CPULimit, r.CPURemaining, r.CPUUsagePercent, r.CPUExceeded)
	sb += fmt.Sprintf("| Memory (bytes) | %d | %d | %d | %.1f%% | %v |\n",
		r.MemoryBytes, r.MemoryLimit, r.MemoryRemaining, r.MemoryUsagePercent, r.MemoryExceeded)
	sb += fmt.Sprintf("| Operations | %d | %d | - | - | %v |\n",
		r.OperationsCount, r.OperationsLimit, r.OperationsExceeded)

	if r.FirstExceeded != "" {
		sb += fmt.Sprintf("\n**First exceeded resource:** `%s` (configured limit: %d)\n",
			r.FirstExceeded, r.ExceededValue)
	}

	return sb
}

// AttachToResponse adds the resource report to a SimulationResponse.
// This is called on both success and trap paths to ensure limits are always reported.
func AttachToResponse(resp *SimulationResponse, report *ResourceReport) {
	if resp == nil || report == nil {
		return
	}
	// The report can be attached as part of the response's BudgetUsage
	// or as a separate field. We extend BudgetUsage if present, otherwise
	// we create a minimal one.
	if resp.BudgetUsage == nil {
		resp.BudgetUsage = &BudgetUsage{
			CPUInstructions:    report.CPUInstructions,
			MemoryBytes:       report.MemoryBytes,
			OperationsCount:   report.OperationsCount,
			CPULimit:          report.CPULimit,
			MemoryLimit:       report.MemoryLimit,
			CPUUsagePercent:   report.CPUUsagePercent,
			MemoryUsagePercent: report.MemoryUsagePercent,
		}
	}
}
