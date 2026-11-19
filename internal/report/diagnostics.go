// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package report

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dotandev/glassbox/internal/telemetry"
	"github.com/dotandev/glassbox/internal/trace"
)

type DiagnosticSeverity string

const (
	SeverityCritical DiagnosticSeverity = "critical"
	SeverityHigh     DiagnosticSeverity = "high"
	SeverityMedium   DiagnosticSeverity = "medium"
	SeverityLow      DiagnosticSeverity = "low"
	SeverityInfo     DiagnosticSeverity = "info"
)

type DiagnosticSummary struct {
	Severity string `json:"severity"`
	Category string `json:"category"`
	Summary  string `json:"summary"`
	Details  string `json:"details,omitempty"`
	Action   string `json:"action,omitempty"`
	Step     int    `json:"step,omitempty"`
	Contract string `json:"contract_id,omitempty"`
}

type DiagnosticReport struct {
	CorrelationID   string              `json:"correlation_id,omitempty"`
	GeneratedAt     time.Time           `json:"generated_at"`
	TransactionHash string              `json:"transaction_hash"`
	TotalSteps      int                 `json:"total_steps"`
	Diagnostics     []DiagnosticSummary `json:"diagnostics"`
	Counts          map[string]int      `json:"counts"`
}

func NewDiagnosticReport(t *trace.ExecutionTrace) *DiagnosticReport {
	return NewDiagnosticReportWithContext(context.Background(), t)
}

func NewDiagnosticReportWithContext(ctx context.Context, t *trace.ExecutionTrace) *DiagnosticReport {
	r := &DiagnosticReport{
		GeneratedAt: time.Now().UTC(),
		Counts:      map[string]int{},
	}
	if ctx != nil {
		r.CorrelationID = telemetry.CorrelationIDFromContext(ctx)
	}
	if t == nil {
		return r
	}
	r.TransactionHash = t.TransactionHash
	r.TotalSteps = len(t.States)

	for _, state := range t.States {
		if state.Error != "" {
			severity := string(SeverityHigh)
			action := "Inspect the failing step, contract arguments, and preceding auth or storage changes."
			if strings.Contains(strings.ToLower(state.Error), "panic") || strings.Contains(strings.ToLower(state.Error), "trap") {
				severity = string(SeverityCritical)
				action = "Reproduce locally with source maps enabled and inspect the trap stack."
			}

			details := state.Error
			location := ""
			if state.SourceFile != "" {
				location = state.SourceFile
				if state.SourceLine > 0 {
					location = fmt.Sprintf("%s:%d", location, state.SourceLine)
				}
				action += fmt.Sprintf(" Source: %s", location)
			}

			r.add(DiagnosticSummary{
				Severity: severity,
				Category: "execution",
				Summary:  "Execution error",
				Details:  details,
				Action:   action,
				Step:     state.Step,
				Contract: state.ContractID,
			})
		}
		if state.WasmInstruction != "" && strings.Contains(strings.ToLower(state.WasmInstruction), "unreachable") {
			r.add(DiagnosticSummary{
				Severity: string(SeverityCritical),
				Category: "wasm",
				Summary:  "WASM trap instruction reached",
				Details:  state.WasmInstruction,
				Action:   "Review the mapped source location and guard panic paths before deployment.",
				Step:     state.Step,
				Contract: state.ContractID,
			})
		}
	}

	for _, event := range t.DiagnosticEvents {
		lower := strings.ToLower(event.Data + " " + strings.Join(event.Topics, " "))
		if strings.Contains(lower, "auth") && (strings.Contains(lower, "fail") || strings.Contains(lower, "invalid")) {
			contract := ""
			if event.ContractID != nil {
				contract = *event.ContractID
			}
			r.add(DiagnosticSummary{
				Severity: string(SeverityHigh),
				Category: "authorization",
				Summary:  "Authorization failure",
				Details:  event.Data,
				Action:   "Verify signer identity, require_auth usage, and custom account signature policy.",
				Contract: contract,
			})
		}
	}

	if len(r.Diagnostics) == 0 {
		r.add(DiagnosticSummary{
			Severity: string(SeverityInfo),
			Category: "execution",
			Summary:  "No diagnostics detected",
			Action:   "No action required.",
		})
	}

	return r
}

func (r *DiagnosticReport) add(d DiagnosticSummary) {
	r.Diagnostics = append(r.Diagnostics, d)
	r.Counts[d.Severity]++
}

func (r *DiagnosticReport) JSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

func (r *DiagnosticReport) Text() string {
	var buf bytes.Buffer
	_, _ = fmt.Fprintf(&buf, "Glassbox Diagnostic Report\n")
	if r.CorrelationID != "" {
		_, _ = fmt.Fprintf(&buf, "Correlation ID: %s\n", r.CorrelationID)
	}
	_, _ = fmt.Fprintf(&buf, "Transaction: %s\n", r.TransactionHash)
	_, _ = fmt.Fprintf(&buf, "Steps: %d\n\n", r.TotalSteps)
	for _, d := range r.Diagnostics {
		_, _ = fmt.Fprintf(&buf, "[%s] %s: %s\n", strings.ToUpper(d.Severity), d.Category, d.Summary)
		if d.Step > 0 || d.Contract != "" {
			_, _ = fmt.Fprintf(&buf, "  Location: step=%d contract=%s\n", d.Step, d.Contract)
		}
		if d.Details != "" {
			_, _ = fmt.Fprintf(&buf, "  Details: %s\n", d.Details)
		}
		if d.Action != "" {
			_, _ = fmt.Fprintf(&buf, "  Action: %s\n", d.Action)
		}
	}
	return buf.String()
}
