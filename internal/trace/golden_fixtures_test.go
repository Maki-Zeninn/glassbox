// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// golden_fixtures_test.go defines the canonical trace fixtures used by the
// printer golden tests (printer_golden_test.go).
//
// Each fixture is a fully deterministic ExecutionTrace: every field that the
// runtime normally stamps with wall-clock time (StartTime, EndTime, per-state
// Timestamp, snapshot Timestamp) is pinned to a fixed base time after the
// trace is built through the real AddState path. Pinning at the fixture level
// keeps output comparison byte-exact, so the golden tests only need to
// normalize genuinely environment-volatile bytes (line endings).
//
// The stub values mirror the canonical regression constants documented in
// docs/regression-test-guide.md and internal/testhelpers (which cannot be
// imported here without creating an import cycle).
package trace

import (
	"time"
)

// goldenTxHash mirrors testhelpers.CanonicalTxHash.
const goldenTxHash = "5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab"

// goldenBaseTime mirrors testhelpers.CanonicalTimestamp (2026-01-01T00:00:00Z).
var goldenBaseTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// Contract IDs are 56-character stubs shaped like Soroban contract addresses.
// They are deliberately fake (see the secret-avoidance rules in
// docs/regression-test-guide.md).
const (
	goldenContractA = "CTESTAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	goldenContractB = "CTESTBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
)

// goldenFixture couples a canonical trace with the semantic tokens each
// output mode must preserve.
type goldenFixture struct {
	name  string
	trace *ExecutionTrace
	opts  ExportOptions

	// commonTokens must appear verbatim (or HTML-escaped) in EVERY output
	// mode, including the interactive terminal printer.
	commonTokens []string

	// exportTokens must appear in the four export formats (text, markdown,
	// html, json) but are not rendered by the terminal printer — e.g. source
	// locations and annotations.
	exportTokens []string
}

// newGoldenTrace builds a trace through the real AddState path, then pins the
// volatile timestamps AddState stamps with time.Now(). Steps are numbered by
// AddState, satisfying the sequential-step requirement of the JSON format
// compatibility check.
func newGoldenTrace(states []ExecutionState) *ExecutionTrace {
	t := NewExecutionTrace(goldenTxHash, DefaultSnapshotInterval)
	t.StartTime = goldenBaseTime
	t.EndTime = goldenBaseTime.Add(5 * time.Second)
	for _, s := range states {
		t.AddState(s)
	}
	for i := range t.States {
		t.States[i].Timestamp = goldenBaseTime.Add(time.Duration(i+1) * time.Second)
	}
	for i := range t.Snapshots {
		t.Snapshots[i].Timestamp = goldenBaseTime.Add(time.Duration(t.Snapshots[i].Step+1) * time.Second)
	}
	return t
}

// goldenFixtures returns the canonical fixture set. One fixture per semantic
// category from issue #547: calls, errors, source locations, gas costs,
// comments/annotations, and the empty trace.
func goldenFixtures() []goldenFixture {
	return []goldenFixture{
		{
			name: "calls",
			trace: newGoldenTrace([]ExecutionState{
				{
					Operation:   "contract_call",
					EventType:   EventTypeContractCall,
					ContractID:  goldenContractA,
					Function:    "transfer",
					Arguments:   []interface{}{"GALICE", "GBOB", 100},
					ReturnValue: "ok",
				},
				{
					Operation:   "host_function",
					EventType:   EventTypeHostFunction,
					Function:    "put_ledger_entry",
					ReturnValue: true,
				},
				{
					Operation:  "auth",
					EventType:  EventTypeAuth,
					ContractID: goldenContractA,
					Function:   "require_auth",
				},
			}),
			commonTokens: []string{
				goldenTxHash,
				goldenContractA,
				"transfer",
				"put_ledger_entry",
				"require_auth",
				"ok",
			},
		},
		{
			name: "errors",
			trace: newGoldenTrace([]ExecutionState{
				{
					Operation:  "contract_call",
					EventType:  EventTypeContractCall,
					ContractID: goldenContractB,
					Function:   "withdraw",
					Error:      "insufficient balance: needed 500, have 120",
				},
				{
					Operation: "trap",
					EventType: EventTypeTrap,
					Error:     "wasm trap: unreachable executed",
					HostState: map[string]interface{}{"status": "failed"},
				},
			}),
			commonTokens: []string{
				goldenTxHash,
				goldenContractB,
				"withdraw",
				"insufficient balance: needed 500, have 120",
				"wasm trap: unreachable executed",
			},
		},
		{
			name: "source_locations",
			trace: newGoldenTrace([]ExecutionState{
				{
					Operation:  "contract_call",
					EventType:  EventTypeContractCall,
					ContractID: goldenContractA,
					Function:   "mint",
					SourceFile: "src/lib.rs",
					SourceLine: 42,
					GitHubLink: "https://github.com/example/token/blob/main/src/lib.rs#L42",
				},
				{
					// Contrast step: no source mapping available.
					Operation:  "contract_call",
					EventType:  EventTypeContractCall,
					ContractID: goldenContractA,
					Function:   "balance",
				},
			}),
			commonTokens: []string{
				goldenTxHash,
				goldenContractA,
				"mint",
				"balance",
			},
			exportTokens: []string{
				"src/lib.rs",
				"https://github.com/example/token/blob/main/src/lib.rs#L42",
			},
		},
		{
			name: "gas",
			trace: newGoldenTrace([]ExecutionState{
				{
					Operation:  "contract_call",
					EventType:  EventTypeContractCall,
					ContractID: goldenContractA,
					Function:   "swap",
					Cost: &CostAnnotation{
						Source:       "observed",
						CPU:          125000,
						MemoryBytes:  2048,
						Operations:   3,
						EstimatedFee: 750,
						Breakdown: []CostComponent{
							{Name: "wasm_insns", Category: "cpu", Units: 1200, UnitCost: 4, Total: 4800},
							{Name: "mem_alloc", Category: "mem", Units: 16, UnitCost: 128, Total: 2048},
						},
					},
				},
			}),
			// Raw values rather than formatted strings ("cpu=125000") so the
			// same tokens are checkable in structural JSON and in the
			// human-readable formats alike.
			commonTokens: []string{
				goldenTxHash,
				goldenContractA,
				"swap",
				"observed",
				"125000",
				"2048",
				"750",
				"wasm_insns",
				"mem_alloc",
			},
		},
		{
			name: "comments",
			trace: func() *ExecutionTrace {
				t := newGoldenTrace([]ExecutionState{
					{
						Operation:  "contract_call",
						EventType:  EventTypeContractCall,
						ContractID: goldenContractA,
						Function:   "transfer",
					},
				})
				t.Annotations = TraceAnnotations{
					Comments:        []string{"Reviewed by alice", "Budget spike investigated"},
					SessionMetadata: map[string]string{"network": "testnet", "session": "sess_test_5c0a1234"},
					GeneratedAt:     goldenBaseTime,
				}
				return t
			}(),
			commonTokens: []string{
				goldenTxHash,
				goldenContractA,
				"transfer",
			},
			exportTokens: []string{
				"Reviewed by alice",
				"Budget spike investigated",
				"sess_test_5c0a1234",
			},
		},
		{
			// Empty trace: the terminal printer must emit its diagnostic
			// message and the export generators must render a zero-step
			// document, never a misleading success report.
			name:         "empty",
			trace:        newGoldenTrace(nil),
			commonTokens: []string{goldenTxHash},
		},
	}
}
