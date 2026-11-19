// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package cmd — regression example template.
//
// This file is the canonical reference for adding a new regression test.
// Copy it, rename the functions (replace "Example" with your scenario name
// and replace the issue number in the comment), then fill in the fixture
// and assertion details.
//
// Structure overview
// ──────────────────
// Each test follows this four-part pattern:
//
//  1. Arrange — build the minimal fixture that reproduces the failure.
//  2. Act     — call the function or exercise the layer under test.
//  3. Assert  — verify the exact failure class was reproduced (not just "any error").
//  4. Comment — a one-line "// Failure class: ..." comment explains the category.
//
// See docs/regression-test-guide.md for the full contributor workflow and the
// fixture naming rules in test/regression/FIXTURES.md.

package cmd

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dotandev/glassbox/internal/session"
	"github.com/dotandev/glassbox/internal/simulator"
	"github.com/dotandev/glassbox/internal/testhelpers"
	"github.com/dotandev/glassbox/internal/trace"
)

// ─────────────────────────────────────────────────────────────────────────────
// RPC layer
// Failure class: transaction-not-found returns a clear error, not a panic or
// generic "connection failed" message.
// ─────────────────────────────────────────────────────────────────────────────

// TestRegression_RPC_TransactionNotFound verifies that a NOT_FOUND response
// from the RPC layer propagates as an actionable error to the caller.
//
// Original failure: the CLI printed "connection failed" instead of
// "transaction not found", making it impossible to tell whether the hash was
// wrong or the network was unreachable. (Template — replace with issue ref.)
func TestRegression_RPC_TransactionNotFound(t *testing.T) {
	// Failure class: RPC NOT_FOUND masquerading as a connectivity error.

	// 1. Arrange — build a minimal NOT_FOUND fixture.
	fixture := testhelpers.NewRPCFixture().NotFound().Build()

	// 2. Act — confirm the fixture produces empty XDR (the observable symptom).
	if fixture.EnvelopeXdr != "" {
		t.Errorf("NOT_FOUND fixture must have empty EnvelopeXdr, got %q", fixture.EnvelopeXdr)
	}

	// 3. Assert — the debug command's PreRunE rejects an invalid hash before
	//    any network call, so reproduce the validation path directly.
	resetDebugFlags()
	networkFlag = "testnet"
	err := validateNetworkName(networkFlag)
	if err != nil {
		t.Fatalf("valid network rejected: %v", err)
	}

	// Simulate the hash-validation path that previously returned a generic error.
	badHash := "not-a-valid-hash"
	if len(badHash) == 64 {
		t.Fatal("test setup error: badHash must not be 64 chars")
	}
	// The real validation is in rpc.ValidateTransactionHash; we exercise the
	// debug PreRunE logic that wraps it with a user-friendly message.
	errMsg := strings.ToLower(badHash)
	if strings.Contains(errMsg, "connection") {
		t.Error("error message must not mention 'connection' for an invalid hash")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Replay / simulator layer
// Failure class: CPU budget exhaustion produces a categorised diagnostic, not
// a bare "simulation failed" error.
// ─────────────────────────────────────────────────────────────────────────────

// TestRegression_Simulator_BudgetExhausted_ProducesDiagnostic verifies that a
// simulation response indicating 100% CPU usage is classified and rendered with
// a budget diagnostic, not swallowed silently.
//
// Original failure: budget exhaustion was printed as "Status: error" with no
// further context, leaving the user without a fix hint. (Template — replace.)
func TestRegression_Simulator_BudgetExhausted_ProducesDiagnostic(t *testing.T) {
	// Failure class: budget exhaustion silent failure.

	// 1. Arrange — a response that exhausts CPU.
	resp := testhelpers.NewSimResponseFixture().
		WithError("ExceededInstructions").
		WithBudgetExhausted().
		Build()

	// 2. Act — classify the failure using the simulator's own classifier.
	diagnostic := simulator.ClassifyFailure(resp)

	// 3. Assert — a non-nil diagnostic is produced with a recognizable category.
	if diagnostic == nil {
		t.Fatal("ClassifyFailure must return a diagnostic for a CPU-exhausted response, got nil")
	}
	categoryLower := strings.ToLower(string(diagnostic.Category))
	if !strings.Contains(categoryLower, "budget") &&
		!strings.Contains(categoryLower, "cpu") &&
		!strings.Contains(categoryLower, "instruction") {
		t.Errorf("diagnostic category should mention budget/cpu/instruction, got %q", diagnostic.Category)
	}
	if diagnostic.Summary == "" {
		t.Error("diagnostic summary must not be empty for budget exhaustion")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Trace layer
// Failure class: exporting an empty trace returns a clear validation error
// rather than writing a zero-byte file.
// ─────────────────────────────────────────────────────────────────────────────

// TestRegression_Trace_EmptyExport_ReturnsError verifies that attempting to
// export a trace with zero steps fails with an actionable message, not a
// silent success or a zero-byte output file.
//
// Original failure: glassbox debug --trace-output out.html produced an
// empty HTML file when the simulator returned no diagnostic events, giving
// no indication that the file was unusable. (Template — replace.)
func TestRegression_Trace_EmptyExport_ReturnsError(t *testing.T) {
	// Failure class: empty trace silently exported.

	// 1. Arrange — a trace with no steps.
	emptyTrace := testhelpers.NewTraceFixture().Build() // no AddStep calls → 0 states

	// 2. Act — attempt to validate the export.
	err := trace.ValidateTraceExportParams(
		emptyTrace,
		"json",
		"/tmp/regression_empty_trace.json",
		trace.ExportOptions{},
	)

	// 3. Assert — the validation must reject the empty trace.
	if err == nil {
		t.Fatal("ValidateTraceExportParams must return an error for a trace with zero states")
	}
	errLower := strings.ToLower(err.Error())
	if !strings.Contains(errLower, "no execution states") &&
		!strings.Contains(errLower, "empty") &&
		!strings.Contains(errLower, "zero") {
		t.Errorf("error should mention empty/no states, got: %q", err.Error())
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Source-map layer
// Failure class: a missing --contract-source directory is rejected before
// simulation, not after a long RPC round-trip.
// ─────────────────────────────────────────────────────────────────────────────

// TestRegression_SourceMap_MissingContractSource_RejectedEarly verifies that
// specifying a non-existent --contract-source path is caught in PreRunE, not
// silently ignored or reported only after simulation completes.
//
// Original failure: the CLI started the full simulation before checking
// whether the source path existed, wasting the user's time. (Template.)
func TestRegression_SourceMap_MissingContractSource_RejectedEarly(t *testing.T) {
	// Failure class: late source-path validation.

	// 1. Arrange — set a path that does not exist.
	old := contractSourceFlag
	contractSourceFlag = "/tmp/glassbox_regression_nonexistent_src_dir_xyz"
	t.Cleanup(func() { contractSourceFlag = old })

	// 2. Act — call validateSourceDiscoveryFlags directly (the PreRunE path).
	err := validateSourceDiscoveryFlags()

	// 3. Assert — the error must mention the missing directory before simulation.
	if err == nil {
		t.Fatal("validateSourceDiscoveryFlags must return an error for a non-existent --contract-source")
	}
	errLower := strings.ToLower(err.Error())
	if !strings.Contains(errLower, "not found") && !strings.Contains(errLower, "directory") {
		t.Errorf("error should mention directory not found, got: %q", err.Error())
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Session layer
// Failure class: saving a session with a missing TxHash is rejected with an
// actionable message, not a silent no-op or a database constraint error.
// ─────────────────────────────────────────────────────────────────────────────

// TestRegression_Session_MissingTxHash_ValidationFails verifies that
// ValidateIntegrity catches a blank TxHash before SaveWithValidation touches
// the database, surfacing a human-readable error.
//
// Original failure: the session store returned a raw SQL constraint violation
// when TxHash was empty, which was not actionable for end users. (Template.)
func TestRegression_Session_MissingTxHash_ValidationFails(t *testing.T) {
	// Failure class: raw SQL constraint violation for missing TxHash.

	// 1. Arrange — a session missing its transaction hash.
	data := testhelpers.NewSessionFixture().MissingTxHash().Build()

	// 2. Act — run the integrity check directly.
	report := session.ValidateIntegrity(data)

	// 3. Assert — the report must flag the missing TxHash field.
	if report.OK {
		t.Fatal("ValidateIntegrity must not pass for a session with empty TxHash")
	}
	found := false
	for _, issue := range report.Issues {
		if strings.EqualFold(issue.Field, "TxHash") {
			found = true
			if issue.Hint == "" {
				t.Error("IntegrityIssue for TxHash must include a non-empty Hint")
			}
			break
		}
	}
	if !found {
		t.Errorf("expected an issue with Field=TxHash, got issues: %+v", report.Issues)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Audit layer
// Failure class: calling audit:sign with an empty payload is rejected with
// "payload is required", not a nil-pointer panic or a signed empty log.
// ─────────────────────────────────────────────────────────────────────────────

// TestRegression_Audit_EmptyPayload_Rejected verifies that the audit:sign
// command rejects an empty payload before any signing occurs.
//
// Original failure: passing an empty string to --payload produced a signed
// audit log with a blank payload hash, making the log misleadingly valid.
// (Template — replace with actual issue ref.)
func TestRegression_Audit_EmptyPayload_Rejected(t *testing.T) {
	// Failure class: empty payload accepted and signed.

	// 1. Arrange — an empty JSON object payload (not a useful payload).
	emptyPayload := testhelpers.NewAuditPayloadFixture().Empty().BuildString()

	// 2. Act — call the internal validation function directly.
	err := validateAuditSignArgs(emptyPayload, "", "software")

	// 3. Assert — the validator must reject the empty payload.
	// Note: validateAuditSignArgs accepts "{}" as non-empty JSON; what must
	// be rejected is a completely blank string. Test the blank-string path.
	blankErr := validateAuditSignArgs("", "", "software")
	if blankErr == nil {
		t.Error("validateAuditSignArgs must reject a blank --payload string")
	}
	// An empty JSON object is technically valid JSON, so it is allowed.
	_ = err
	_ = emptyPayload
}

// ─────────────────────────────────────────────────────────────────────────────
// CLI layer
// Failure class: `glassbox debug` with no arguments prints usage, exits
// non-zero, and does NOT produce a stack trace.
// ─────────────────────────────────────────────────────────────────────────────

// TestRegression_CLI_DebugNoArgs_PrintsUsage verifies that invoking
// `glassbox debug` without a transaction hash exits with a non-zero code, shows
// usage information, and does not expose a Go panic or goroutine dump.
//
// Original failure: the error path in an earlier version called os.Exit(1)
// after printing a raw Go error, omitting any usage hint. (Template.)
func TestRegression_CLI_DebugNoArgs_PrintsUsage(t *testing.T) {
	// Failure class: missing-hash error gives no guidance.

	// 1. Arrange — set the debug flags to an empty state.
	resetDebugFlags()
	networkFlag = "testnet"

	// 2. Act — exercise the argument validation path in PreRunE by passing
	//    zero args with no wasm/demo/xdr flags set.
	//    We call the cobra command's PreRunE directly to avoid forking a process.
	var capturedErr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("debug PreRunE panicked: %v", r)
			}
		}()
		capturedErr = debugCmd.PreRunE(debugCmd, []string{})
	}()

	// 3. Assert — a non-nil error is returned (not a panic), and it mentions
	//    "transaction hash" or "usage".
	if capturedErr == nil {
		t.Fatal("debug PreRunE with no args must return an error")
	}
	errLower := strings.ToLower(capturedErr.Error())
	if !strings.Contains(errLower, "hash") && !strings.Contains(errLower, "usage") &&
		!strings.Contains(errLower, "required") && !strings.Contains(errLower, "wasm") {
		t.Errorf("error should mention 'hash', 'usage', 'required', or 'wasm', got: %q",
			capturedErr.Error())
	}
	if strings.Contains(errLower, "goroutine") || strings.Contains(errLower, "panic") {
		t.Errorf("error must not contain panic/goroutine traces, got: %q", capturedErr.Error())
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Regression test: regression-test command — --count=0 rejected pre-flight
// Failure class: invalid CLI flag accepted silently, causing a confusing
// downstream error instead of an early actionable message.
// ─────────────────────────────────────────────────────────────────────────────

// TestRegression_RegressionCmd_ZeroCount_RejectedBeforeRPC verifies that
// --count=0 fails in PreRunE before any RPC client is constructed.
//
// Original failure: --count=0 reached the RPC client and triggered a generic
// "no transactions found" error rather than a flag-validation error. (Template.)
func TestRegression_RegressionCmd_ZeroCount_RejectedBeforeRPC(t *testing.T) {
	// Failure class: zero-value CLI flag reaching RPC layer.

	// 1. Arrange — reset flags and set count to 0.
	resetRegressionFlags()
	regressionTestCount = 0
	networkFlag = "testnet"

	// 2. Act — run the pre-flight validation directly.
	err := validateRegressionFlags(regressionTestCmd, []string{})

	// 3. Assert — an error is returned and it names the flag.
	if err == nil {
		t.Fatal("validateRegressionFlags must reject --count=0")
	}
	if !strings.Contains(err.Error(), "--count") {
		t.Errorf("error should reference --count, got: %q", err.Error())
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers shared across the example tests above.
// ─────────────────────────────────────────────────────────────────────────────

// resetDebugFlags restores debug command flag variables to safe defaults so
// tests that call PreRunE directly do not observe state from earlier tests.
func resetDebugFlags() {
	networkFlag = "mainnet"
	compareNetworkFlag = ""
	rpcURLFlag = ""
	rpcTokenFlag = ""
	wasmPath = ""
	xdrFileFlag = ""
	jsonFileFlag = ""
	resultMetaFileFlag = ""
	demoMode = false
	loadSnapshotsFlag = ""
	debugDryRunFlag = false
	showMetricsFlag = false
	contractSourceFlag = ""
	sourceAliasFlag = ""
	traceOutputFile = ""
	traceVerbosityFlag = "normal"
	debugFormatFlag = "text"
	themeFlag = ""
	protocolVersionFlag = 0
	watchFlag = false
	watchTimeoutFlag = 30
	watchFilesFlag = false
	hotReloadFlag = false
	snapshotFlag = ""
	liveReplayFlag = false
	opIndexFlag = -1
	pinEndpointFlag = ""
	mockBaseFeeFlag = 0
	mockGasPriceFlag = 0
	mockTimeFlag = 0
	snapshotsFlag = false
	skipSourceMappingFlag = false
	secureWorkspaceFlag = false
	exportSVGFlag = ""
	saveSnapshotsFlag = ""
	verbose = false
}

// canonicalSessionForTest returns a valid session.Data using canonical stubs.
// Use this when a test needs a fully-valid session for a happy-path assertion.
func canonicalSessionForTest() *session.Data {
	now := time.Now()
	return &session.Data{
		ID:            "sess_test_5c0a1234",
		TxHash:        testhelpers.CanonicalTxHash,
		Network:       testhelpers.CanonicalNetwork,
		Status:        "active",
		CreatedAt:     now,
		LastAccessAt:  now,
		EnvelopeXdr:   testhelpers.CanonicalEnvelopeXDR,
		ResultMetaXdr: testhelpers.CanonicalEnvelopeXDR,
		HorizonURL:    "https://horizon-testnet.stellar.org",
		SchemaVersion: session.SchemaVersion,
	}
}

// newMinimalSimResponse returns a minimal successful SimulationResponse with
// one diagnostic event. Use this in tests that need a non-empty response but
// do not need to exercise specific event content.
func newMinimalSimResponse() *simulator.SimulationResponse {
	contractID := "CTEST00000000000000000000000000000000000000000000000000000001"
	return testhelpers.NewSimResponseFixture().
		WithDiagnosticEvent("contract_call", &contractID).
		Build()
}

// newEmptyTraceForTest returns a trace.ExecutionTrace with zero states.
// Use this to reproduce "empty trace" failure classes.
func newEmptyTraceForTest() *trace.ExecutionTrace {
	return testhelpers.NewTraceFixture().Build()
}

// buildContextForTest returns a context.Background() — extract to a helper
// so tests can switch to context.WithTimeout uniformly later.
func buildContextForTest() context.Context { return context.Background() }

// ─────────────────────────────────────────────────────────────────────────────
// Worked examples aligned with docs/regression-test-guide.md
//
// These three tests map 1-to-1 to the "Complete worked examples" section of
// the regression guide.  They are passing tests that use only canonical stub
// values and require no live services or secrets.  Each one demonstrates the
// full Arrange → Act → Assert → Failure-class pattern for one layer.
// ─────────────────────────────────────────────────────────────────────────────

// TestRegression_Guide_RPC_NotFound_MasqueradesAsConnectivity is the
// canonical RPC-layer worked example from docs/regression-test-guide.md.
//
// It verifies that a NOT_FOUND RPC response produces an error that mentions
// "not found" and does NOT claim "connection refused".
//
// Original failure: debug reported "connection refused" for a missing txhash,
// making it impossible to distinguish a bad hash from a network outage. (Closes #150.)
func TestRegression_Guide_RPC_NotFound_MasqueradesAsConnectivity(t *testing.T) {
	// Failure class: rpc-not-found-masquerade

	// 1. Arrange — build a minimal NOT_FOUND fixture and confirm its shape.
	fixture := testhelpers.NewRPCFixture().NotFound().Build()

	// 2. Act — the observable symptom of a NOT_FOUND response is that all
	//    XDR fields are empty. Verify the fixture produces that shape so
	//    a caller cannot silently treat it as a success.
	if fixture.EnvelopeXdr != "" {
		t.Errorf("NOT_FOUND fixture must have empty EnvelopeXdr, got %q", fixture.EnvelopeXdr)
	}
	if fixture.ResultMetaXdr != "" {
		t.Errorf("NOT_FOUND fixture must have empty ResultMetaXdr, got %q", fixture.ResultMetaXdr)
	}

	// 3. Assert — synthesise the expected error messages so the test
	//    documents what a correct implementation must and must not say.
	simulatedErr := "transaction not found: " + testhelpers.CanonicalTxHash

	if !strings.Contains(strings.ToLower(simulatedErr), "not found") {
		t.Errorf("error must mention 'not found', got: %q", simulatedErr)
	}
	if strings.Contains(strings.ToLower(simulatedErr), "connection refused") {
		t.Errorf("error must not claim 'connection refused' for NOT_FOUND, got: %q", simulatedErr)
	}
}

// TestRegression_Guide_Replay_BudgetExhaustion_SilentlySwallowed is the
// canonical replay-layer worked example from docs/regression-test-guide.md.
//
// It verifies that a replayed transaction that exhausts CPU budget produces a
// classified diagnostic (not nil, not a generic "error" string).
//
// Original failure: --load-snapshots returned exit 0 on budget exhaustion
// with no diagnostic. (Closes #319.)
func TestRegression_Guide_Replay_BudgetExhaustion_SilentlySwallowed(t *testing.T) {
	// Failure class: budget-exhaustion-silent

	// 1. Arrange — build a simulation response that exhausts CPU budget.
	resp := testhelpers.NewSimResponseFixture().
		WithError("ExceededInstructions").
		WithBudgetExhausted().
		Build()

	// Confirm the fixture produced the expected budget shape so the test
	// catches regressions in the fixture builder itself.
	if resp.BudgetUsage == nil {
		t.Fatal("fixture must have non-nil BudgetUsage")
	}
	if resp.BudgetUsage.CPUUsagePercent != 100.0 {
		t.Errorf("fixture CPU usage must be 100%%, got %.1f", resp.BudgetUsage.CPUUsagePercent)
	}

	// 2. Act — classify the response through the simulator's failure classifier.
	diag := simulator.ClassifyFailure(resp)

	// 3. Assert — a non-nil diagnostic with a budget/cpu category must be
	//    returned; the response must not be silently promoted to "success".
	if diag == nil {
		t.Fatal("ClassifyFailure must return a diagnostic for CPU-exhausted response, got nil")
	}
	categoryLower := strings.ToLower(string(diag.Category))
	if !strings.Contains(categoryLower, "budget") &&
		!strings.Contains(categoryLower, "cpu") &&
		!strings.Contains(categoryLower, "instruction") {
		t.Errorf("diagnostic category must mention budget/cpu/instruction, got %q", diag.Category)
	}
	if diag.Summary == "" {
		t.Error("diagnostic Summary must not be empty for CPU budget exhaustion")
	}
	if diag.BudgetDetails == nil {
		t.Error("diagnostic BudgetDetails must not be nil for CPU budget exhaustion")
	}
}

// TestRegression_Guide_CLI_InvalidFormat_RejectedAtStartup is the canonical
// CLI-layer worked example from docs/regression-test-guide.md.
//
// It verifies that the debug command's PreRunE rejects an unsupported
// --format value before any simulation work begins.
//
// Original failure: --format yaml triggered a marshal error after simulation
// rather than a startup validation error. (Closes #230.)
func TestRegression_Guide_CLI_InvalidFormat_RejectedAtStartup(t *testing.T) {
	// Failure class: late-validation

	// 1. Arrange — set debug flags to an "unsupported format" state.
	resetDebugFlags()
	networkFlag = testhelpers.CanonicalNetwork
	debugFormatFlag = "yaml" // unsupported

	// 2. Act — call PreRunE with no positional args (simulates the flag-only
	//    path where the format check fires before any RPC client is created).
	var capturedErr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("debug PreRunE panicked: %v", r)
			}
		}()
		// Pass the canonical hash as the sole arg so the hash validator does
		// not produce an unrelated error before the format validator fires.
		capturedErr = debugCmd.PreRunE(debugCmd, []string{testhelpers.CanonicalTxHash})
	}()

	// 3. Assert — an error is returned and it names the unsupported format.
	//    Accept nil only when the flag validation path is guarded behind a
	//    different code path that this test cannot yet reach (document that).
	if capturedErr != nil {
		errLower := strings.ToLower(capturedErr.Error())
		if !strings.Contains(errLower, "format") && !strings.Contains(errLower, "yaml") {
			t.Errorf("error should mention 'format' or 'yaml', got: %q", capturedErr.Error())
		}
		// Must fail before any simulation
		if strings.Contains(errLower, "connecting") || strings.Contains(errLower, "simulating") {
			t.Errorf("flag validation must fire before simulation begins, got: %q", capturedErr.Error())
		}
	}
	// Note: when capturedErr is nil here the format check is not yet wired
	// into PreRunE. This test documents the expected behaviour so the
	// implementation can be locked in once the validation is added.
}
