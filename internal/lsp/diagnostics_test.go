// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package lsp

// diagnostics_test.go covers the translation from a simulation outcome to LSP
// diagnostics, and the no-client behaviour that keeps `glassbox debug
// --lsp-socket` safe when no editor is listening.

import (
	"context"
	"errors"
	"testing"

	"github.com/dotandev/glassbox/internal/security"
	"github.com/dotandev/glassbox/internal/simulator"
	"github.com/dotandev/glassbox/internal/sourcemap"
)

// mockResult is a fixture simulation response with a trap, an expensive budget
// and a deprecated host function call in one of its diagnostic events.
func mockResult() *simulator.SimulationResponse {
	cpu := uint64(5_000_000)
	return &simulator.SimulationResponse{
		Status: "error",
		Error:  "HostError: Error(Contract, #1)",
		SourceLocation: &simulator.SourceLocation{
			File: "/ws/contracts/src/lib.rs",
			Line: 42,
		},
		BudgetUsage: &simulator.BudgetUsage{
			CPUInstructions: cpu,
			CPULimit:        1_000_000,
			CPUUsagePercent: 500,
		},
		DiagnosticEvents: []simulator.DiagnosticEvent{
			{Data: "bytes_new_from_linear_memory called with len=4"},
		},
	}
}

func mapping() []sourcemap.FallbackResult {
	return []sourcemap.FallbackResult{
		{File: "/ws/contracts/src/lib.rs", Line: 42, Quality: sourcemap.MappingQualityFull},
	}
}

func onlyDiagnostics(t *testing.T, byURI DiagnosticsMap) map[string]map[string]ProtocolDiagnostic {
	t.Helper()

	out := make(map[string]map[string]ProtocolDiagnostic, len(byURI))
	for uri, diags := range byURI {
		byCode := make(map[string]ProtocolDiagnostic, len(diags))
		for _, diag := range diags {
			byCode[diag.Code] = diag
		}
		out[uri] = byCode
	}
	return out
}

func TestDiagnosticsFromSimulationReportsTrapsAsErrors(t *testing.T) {
	byURI := DiagnosticsFromSimulation(mockResult(), mapping(), nil, Options{})
	diags := onlyDiagnostics(t, byURI)

	const wantURI = "file:///ws/contracts/src/lib.rs"
	found, ok := diags[wantURI][CodeTrap]
	if !ok {
		t.Fatalf("expected a trap diagnostic on %s, got %v", wantURI, diags)
	}
	if found.Severity != SeverityError {
		t.Errorf("trap severity = %d, want %d", found.Severity, SeverityError)
	}
	if found.Source != diagnosticSource {
		t.Errorf("source = %q, want %q", found.Source, diagnosticSource)
	}
	if found.Message == "" {
		t.Error("expected a non-empty trap message")
	}
}

func TestDiagnosticsUsesZeroBasedLines(t *testing.T) {
	byURI := DiagnosticsFromSimulation(mockResult(), mapping(), nil, Options{})
	for _, diags := range byURI {
		for _, diag := range diags {
			if diag.Range.Start.Line != 41 {
				t.Errorf("source line 42 should map to LSP line 41, got %d", diag.Range.Start.Line)
			}
		}
	}
}

func TestDiagnosticsReportsHighResourceUsageAsWarnings(t *testing.T) {
	byURI := DiagnosticsFromSimulation(mockResult(), mapping(), nil, Options{})
	diags := onlyDiagnostics(t, byURI)

	found, ok := diags["file:///ws/contracts/src/lib.rs"][CodeResourceCost]
	if !ok {
		t.Fatalf("expected a resource-cost diagnostic, got %v", diags)
	}
	if found.Severity != SeverityWarning {
		t.Errorf("resource severity = %d, want %d", found.Severity, SeverityWarning)
	}
}

func TestDiagnosticsHonoursTheResourceCostThreshold(t *testing.T) {
	// A threshold above the observed usage must suppress the warning.
	byURI := DiagnosticsFromSimulation(
		mockResult(),
		mapping(),
		nil,
		Options{ResourceCostThreshold: 10_000_000},
	)
	for _, diags := range byURI {
		for _, diag := range diags {
			if diag.Code == CodeResourceCost {
				t.Fatal("expected no resource diagnostic above the threshold")
			}
		}
	}

	// A threshold below the observed usage must produce it.
	byURI = DiagnosticsFromSimulation(
		mockResult(),
		mapping(),
		nil,
		Options{ResourceCostThreshold: 1_000},
	)
	seen := false
	for _, diags := range byURI {
		for _, diag := range diags {
			if diag.Code == CodeResourceCost {
				seen = true
			}
		}
	}
	if !seen {
		t.Error("expected a resource diagnostic below the threshold")
	}
}

func TestDiagnosticsReportsDeprecatedHostFunctionsAsHints(t *testing.T) {
	byURI := DiagnosticsFromSimulation(mockResult(), mapping(), nil, Options{})
	diags := onlyDiagnostics(t, byURI)

	found, ok := diags["file:///ws/contracts/src/lib.rs"][CodeDeprecatedHostFunction]
	if !ok {
		t.Fatalf("expected a deprecated host function hint, got %v", diags)
	}
	if found.Severity != SeverityHint {
		t.Errorf("host function severity = %d, want %d", found.Severity, SeverityHint)
	}
}

func TestDiagnosticsEnrichHostFunctionHintsWithFindings(t *testing.T) {
	findings := []security.Finding{{
		Title:       "bytes_new_from_linear_memory",
		Description: "removed in protocol 23",
	}}

	byURI := DiagnosticsFromSimulation(mockResult(), mapping(), findings, Options{})
	diags := onlyDiagnostics(t, byURI)

	found := diags["file:///ws/contracts/src/lib.rs"][CodeDeprecatedHostFunction]
	if got := found.Message; !contains(got, "removed in protocol 23") {
		t.Errorf("expected the finding description in the hint, got %q", got)
	}
}

func TestDiagnosticsIgnoresSupportedHostFunctions(t *testing.T) {
	result := mockResult()
	result.DiagnosticEvents = []simulator.DiagnosticEvent{{Data: "storage_get returned a value"}}

	byURI := DiagnosticsFromSimulation(result, mapping(), nil, Options{})
	for _, diags := range byURI {
		for _, diag := range diags {
			if diag.Code == CodeDeprecatedHostFunction {
				t.Fatal("storage_get is not deprecated and must not be reported")
			}
		}
	}
}

func TestDiagnosticsFallBackToMappedLocationWithoutASourceLocation(t *testing.T) {
	result := mockResult()
	result.SourceLocation = nil

	byURI := DiagnosticsFromSimulation(result, mapping(), nil, Options{})
	if _, ok := byURI["file:///ws/contracts/src/lib.rs"]; !ok {
		t.Fatalf("expected diagnostics on the mapped file, got %v", byURI)
	}
}

func TestDiagnosticsReturnNothingForACleanRun(t *testing.T) {
	byURI := DiagnosticsFromSimulation(
		&simulator.SimulationResponse{Status: "success"},
		nil,
		nil,
		Options{},
	)
	if len(byURI) != 0 {
		t.Errorf("expected no diagnostics for a clean run, got %v", byURI)
	}
}

func TestDiagnosticsHandleNilInput(t *testing.T) {
	if got := DiagnosticsFromSimulation(nil, nil, nil, Options{}); len(got) != 0 {
		t.Errorf("expected no diagnostics for a nil result, got %v", got)
	}
}

func TestDiagnosticsMakePathsRelativeToTheWorkspaceRoot(t *testing.T) {
	result := &simulator.SimulationResponse{
		Status:         "error",
		SourceLocation: &simulator.SourceLocation{File: "/ws/contracts/src/lib.rs", Line: 7},
	}

	byURI := DiagnosticsFromSimulation(result, nil, nil, Options{RootDir: "/ws"})
	if _, ok := byURI["file:///contracts/src/lib.rs"]; !ok {
		t.Errorf("expected a root-relative URI, got %v", byURI)
	}
}

func TestDocumentURI(t *testing.T) {
	cases := map[string]string{
		"":                "",
		"/ws/a.rs":        "file:///ws/a.rs",
		"a.rs":            "file:///a.rs",
		"/ws/with space.rs": "file:///ws/with%20space.rs",
	}
	for input, want := range cases {
		if got := documentURI(input, ""); got != want {
			t.Errorf("documentURI(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestZeroBasedLine(t *testing.T) {
	cases := map[int]int{0: 0, 1: 0, 2: 1, 100: 99, -5: 0}
	for input, want := range cases {
		if got := zeroBasedLine(input); got != want {
			t.Errorf("zeroBasedLine(%d) = %d, want %d", input, got, want)
		}
	}
}

func TestPublishWithoutAClientIsANoOp(t *testing.T) {
	publisher := NewDiagnosticsPublisher(nil, Options{})
	err := publisher.Publish(context.Background(), mockResult(), mapping(), nil)
	if !errors.Is(err, ErrNoClient) {
		t.Fatalf("expected ErrNoClient, got %v", err)
	}
}

func TestPublishOnANilPublisherDoesNotPanic(t *testing.T) {
	var publisher *DiagnosticsPublisher
	if err := publisher.Publish(context.Background(), mockResult(), nil, nil); !errors.Is(err, ErrNoClient) {
		t.Fatalf("expected ErrNoClient from a nil publisher, got %v", err)
	}
}

func TestPublishToAnUnreachableServerReturnsAnErrorRatherThanBlocking(t *testing.T) {
	// 127.0.0.1:1 is not listening, so the dial must fail fast.
	client, err := Connect(context.Background(), "127.0.0.1:1", DefaultTimeout)
	if err == nil {
		_ = client.Close()
		t.Skip("something is listening on 127.0.0.1:1")
	}

	publisher := NewDiagnosticsPublisher(client, Options{})
	if err := publisher.Publish(context.Background(), mockResult(), mapping(), nil); err == nil {
		t.Error("expected publishing to an unconnected client to fail")
	}
}

func TestNetworkFor(t *testing.T) {
	if got := networkFor("127.0.0.1:9000"); got != "tcp" {
		t.Errorf("networkFor(\"127.0.0.1:9000\") = %q, want tcp", got)
	}
	if got := networkFor("/tmp/glassbox.sock"); got != "unix" {
		t.Errorf("networkFor(\"/tmp/glassbox.sock\") = %q, want unix", got)
	}
}

func TestConnectRejectsAnEmptyAddress(t *testing.T) {
	if _, err := Connect(context.Background(), "  ", DefaultTimeout); err == nil {
		t.Error("expected an error for an empty address")
	}
}

func TestClientConnectedAndCloseAreNilSafe(t *testing.T) {
	var client *Client
	if client.Connected() {
		t.Error("a nil client must not report connected")
	}
	if err := client.Close(); err != nil {
		t.Errorf("closing a nil client = %v, want nil", err)
	}
	if err := client.PublishDiagnostics(context.Background(), DiagnosticsMap{}); !errors.Is(err, ErrNotConnected) {
		t.Errorf("expected ErrNotConnected, got %v", err)
	}
}

func TestConnectAndPublishAgainstAMockServer(t *testing.T) {
	// The mock server captures the framed notifications so the test asserts on
	// what actually reached the wire, not just on internal state.
	server, addr, received := startMockLSPServer(t)

	client, err := Connect(context.Background(), addr, DefaultTimeout)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = client.Close() }()

	if !client.Connected() {
		t.Error("expected the client to report connected")
	}

	byURI := DiagnosticsFromSimulation(mockResult(), mapping(), nil, Options{})
	if err := client.PublishDiagnostics(context.Background(), byURI); err != nil {
		t.Fatalf("PublishDiagnostics: %v", err)
	}

	notifications := received()
	if len(notifications) != 1 {
		t.Fatalf("expected 1 publishDiagnostics notification, got %d", len(notifications))
	}
	if notifications[0].method != methodPublishDiagnostics {
		t.Errorf("method = %q, want %q", notifications[0].method, methodPublishDiagnostics)
	}

	uri, diags := notifications[0].publishParams(t)
	if uri != "file:///ws/contracts/src/lib.rs" {
		t.Errorf("published URI = %q", uri)
	}
	if len(diags) == 0 {
		t.Error("expected the trap diagnostic to reach the server")
	}
	foundTrap := false
	for _, diag := range diags {
		if diag.Code == CodeTrap && diag.Severity == SeverityError {
			foundTrap = true
		}
	}
	if !foundTrap {
		t.Error("expected an error-severity trap diagnostic on the wire")
	}

	_ = server
}

func TestClearDiagnosticsSendsEmptyLists(t *testing.T) {
	_, addr, received := startMockLSPServer(t)

	client, err := Connect(context.Background(), addr, DefaultTimeout)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = client.Close() }()

	if err := client.ClearDiagnostics(context.Background(), []string{"file:///a.rs"}); err != nil {
		t.Fatalf("ClearDiagnostics: %v", err)
	}

	uri, diags := received()[0].publishParams(t)
	if uri != "file:///a.rs" {
		t.Errorf("cleared URI = %q", uri)
	}
	if len(diags) != 0 {
		t.Errorf("expected an empty diagnostic list to clear annotations, got %d", len(diags))
	}
}

// contains is a tiny helper so the assertions above do not pull in strings just
// for a substring check.
func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
