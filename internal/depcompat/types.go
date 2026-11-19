// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package depcompat implements scheduled dependency compatibility testing for
// Glassbox. It captures golden JSON outputs from the deterministic harness,
// compares them against stored baselines, and classifies diffs as expected
// (schema-only format changes) or unexpected (value changes, missing fields).
//
// Supported dependency groups:
//   - stellar-sdk  : github.com/stellar/go-stellar-sdk
//   - soroban-host : soroban-env-host (Rust, via simulator binary)
//   - crypto       : ed25519-dalek + sha2 (Rust, via simulator binary)
//   - rpc-client   : github.com/stellar/go-stellar-sdk RPC layer
//
// Golden files live in internal/depcompat/testdata/golden/*.json and are
// committed to the repository. Capture is performed by
// scripts/dep-compat-capture.sh; comparison by scripts/dep-compat-compare.sh.
// The workflow .github/workflows/dep-compat.yml drives scheduled runs.
package depcompat

import (
	"encoding/json"
	"fmt"
	"time"
)

// DepGroup names a logical set of dependencies under test.
type DepGroup string

const (
	// DepGroupStellarSDK covers the Go stellar SDK (go-stellar-sdk).
	DepGroupStellarSDK DepGroup = "stellar-sdk"

	// DepGroupSorobanHost covers the Rust soroban-env-host linked into the simulator.
	DepGroupSorobanHost DepGroup = "soroban-host"

	// DepGroupCrypto covers ed25519-dalek and sha2 used in the Rust simulator.
	DepGroupCrypto DepGroup = "crypto"

	// DepGroupRPCClient covers the Stellar RPC client layer (Go).
	DepGroupRPCClient DepGroup = "rpc-client"
)

// AllDepGroups lists all supported dependency groups in declaration order.
var AllDepGroups = []DepGroup{
	DepGroupStellarSDK,
	DepGroupSorobanHost,
	DepGroupCrypto,
	DepGroupRPCClient,
}

// OutputKind names one of the four deterministic output types captured from the harness.
type OutputKind string

const (
	OutputReplay  OutputKind = "replay"
	OutputTrace   OutputKind = "trace"
	OutputAudit   OutputKind = "audit"
	OutputBinding OutputKind = "binding"
)

// AllOutputKinds lists all output kinds in declaration order.
var AllOutputKinds = []OutputKind{
	OutputReplay,
	OutputTrace,
	OutputAudit,
	OutputBinding,
}

// DiffClass classifies whether a diff between a golden file and a captured
// output is an expected (schema-format) change or an unexpected (semantic) one.
type DiffClass string

const (
	// DiffClassExpected means the diff is a known-acceptable schema or formatting
	// change (e.g., key reordering, new optional field with null/zero value).
	DiffClassExpected DiffClass = "expected"

	// DiffClassUnexpected means the diff contains a value change, a missing
	// required field, or a structural incompatibility.
	DiffClassUnexpected DiffClass = "unexpected"

	// DiffClassNone means the output matches the golden file exactly.
	DiffClassNone DiffClass = "none"
)

// FieldDiff records one changed JSON path between golden and captured output.
type FieldDiff struct {
	// JSONPath is the dot-separated path to the differing value (e.g. "events[0].type").
	JSONPath string `json:"json_path"`
	// GoldenValue is the value in the stored golden file (JSON-encoded string).
	GoldenValue string `json:"golden_value,omitempty"`
	// ActualValue is the value produced by the current dependency set.
	ActualValue string `json:"actual_value,omitempty"`
	// Class is the auto-classification of this field diff.
	Class DiffClass `json:"class"`
	// Reason is a human-readable explanation of why the diff is classified as it is.
	Reason string `json:"reason,omitempty"`
}

// OutputResult holds the comparison result for one (DepGroup, OutputKind) pair.
type OutputResult struct {
	// DepGroup identifies the dependency group under test.
	DepGroup DepGroup `json:"dep_group"`
	// OutputKind identifies which harness output was compared.
	OutputKind OutputKind `json:"output_kind"`
	// GoldenFile is the path to the stored golden JSON file.
	GoldenFile string `json:"golden_file"`
	// CapturedFile is the path to the freshly captured JSON file.
	CapturedFile string `json:"captured_file,omitempty"`
	// Class is the aggregate classification for this output.
	// It is DiffClassNone when there are no diffs, DiffClassExpected when all
	// diffs are expected, and DiffClassUnexpected when any diff is unexpected.
	Class DiffClass `json:"class"`
	// Diffs lists all detected field-level differences.
	Diffs []FieldDiff `json:"diffs,omitempty"`
	// Error holds an error message if capture or comparison failed outright.
	Error string `json:"error,omitempty"`
}

// DepVersions records the actual versions of all dependencies at capture time.
type DepVersions struct {
	// StellarSDKVersion is the Go module version of go-stellar-sdk.
	StellarSDKVersion string `json:"stellar_sdk_version,omitempty"`
	// SorobanHostVersion is the Cargo version of soroban-env-host.
	SorobanHostVersion string `json:"soroban_host_version,omitempty"`
	// Ed25519DalekVersion is the Cargo version of ed25519-dalek.
	Ed25519DalekVersion string `json:"ed25519_dalek_version,omitempty"`
	// Sha2Version is the Cargo version of sha2.
	Sha2Version string `json:"sha2_version,omitempty"`
	// GoVersion is the Go toolchain version.
	GoVersion string `json:"go_version,omitempty"`
	// RustVersion is the Rust toolchain version.
	RustVersion string `json:"rust_version,omitempty"`
}

// CompatReport is the top-level report produced by one compatibility run.
type CompatReport struct {
	// SchemaVersion identifies the report schema for future-proofing.
	SchemaVersion string `json:"schema_version"`
	// RunID is a unique identifier for this run (typically a GitHub run ID or timestamp).
	RunID string `json:"run_id"`
	// GeneratedAt is the UTC timestamp when the report was produced.
	GeneratedAt time.Time `json:"generated_at"`
	// DepGroup is set when the run targeted a single group; empty means all groups.
	DepGroup DepGroup `json:"dep_group,omitempty"`
	// Versions holds the resolved dependency versions used during this run.
	Versions DepVersions `json:"versions"`
	// Results holds per-output comparison results.
	Results []OutputResult `json:"results"`
	// Summary aggregates counts across all results.
	Summary ReportSummary `json:"summary"`
}

// ReportSummary provides quick-glance aggregate counts.
type ReportSummary struct {
	// TotalOutputs is the number of (DepGroup × OutputKind) pairs tested.
	TotalOutputs int `json:"total_outputs"`
	// OutputsMatched is the count with DiffClassNone.
	OutputsMatched int `json:"outputs_matched"`
	// OutputsExpected is the count with DiffClassExpected.
	OutputsExpected int `json:"outputs_expected"`
	// OutputsUnexpected is the count with DiffClassUnexpected.
	OutputsUnexpected int `json:"outputs_unexpected"`
	// OutputsErrored is the count where capture or comparison produced an error.
	OutputsErrored int `json:"outputs_errored"`
	// HasUnexpectedDiffs is true when OutputsUnexpected > 0.
	HasUnexpectedDiffs bool `json:"has_unexpected_diffs"`
	// HasErrors is true when OutputsErrored > 0.
	HasErrors bool `json:"has_errors"`
}

// SchemaVersion is the current report schema version string.
const SchemaVersion = "1.0"

// NewCompatReport initialises an empty CompatReport with metadata.
func NewCompatReport(runID string, depGroup DepGroup) *CompatReport {
	return &CompatReport{
		SchemaVersion: SchemaVersion,
		RunID:         runID,
		GeneratedAt:   time.Now().UTC(),
		DepGroup:      depGroup,
		Results:       []OutputResult{},
	}
}

// AddResult appends a result to the report and updates the summary.
func (r *CompatReport) AddResult(res OutputResult) {
	r.Results = append(r.Results, res)
}

// Finalize recomputes the summary from the current result set. Call this once
// all results have been added before marshalling the report.
func (r *CompatReport) Finalize() {
	s := ReportSummary{TotalOutputs: len(r.Results)}
	for _, res := range r.Results {
		switch {
		case res.Error != "":
			s.OutputsErrored++
		case res.Class == DiffClassNone:
			s.OutputsMatched++
		case res.Class == DiffClassExpected:
			s.OutputsExpected++
		default:
			s.OutputsUnexpected++
		}
	}
	s.HasUnexpectedDiffs = s.OutputsUnexpected > 0
	s.HasErrors = s.OutputsErrored > 0
	r.Summary = s
}

// ToJSON serialises the report as indented JSON.
func (r *CompatReport) ToJSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

// GoldenFileName returns the conventional filename for a golden baseline.
// Format: <dep_group>-<output_kind>.golden.json
func GoldenFileName(group DepGroup, kind OutputKind) string {
	return fmt.Sprintf("%s-%s.golden.json", group, kind)
}
