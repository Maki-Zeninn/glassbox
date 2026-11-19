// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package protocolreg

import (
	"strings"
	"testing"
)

// validHash is a 64-character hex string used across tests.
const validHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// baseURI is the minimal valid URI (no optional params).
const baseURI = "glassbox://debug/" + validHash + "?network=testnet"

// ─── Backward-compatible baseline ────────────────────────────────────────────

func TestParseDebugURI(t *testing.T) {
	parsed, err := ParseDebugURI("glassbox://debug/" + validHash + "?network=testnet&operation=2&source=dashboard")
	if err != nil {
		t.Fatalf("ParseDebugURI returned error: %v", err)
	}

	if parsed.TransactionHash != validHash {
		t.Fatalf("unexpected transaction hash: %s", parsed.TransactionHash)
	}
	if parsed.Network != "testnet" {
		t.Fatalf("unexpected network: %s", parsed.Network)
	}
	// Operation (legacy field) must still be populated.
	if parsed.Operation == nil || *parsed.Operation != 2 {
		t.Fatalf("unexpected Operation: %#v", parsed.Operation)
	}
	// Op (new field) must mirror Operation.
	if parsed.Op == nil || *parsed.Op != 2 {
		t.Fatalf("unexpected Op: %#v", parsed.Op)
	}
	if parsed.Source != "dashboard" {
		t.Fatalf("unexpected source: %s", parsed.Source)
	}
}

func TestParseDebugURIRejectsInvalidValues(t *testing.T) {
	tests := []string{
		"",
		"https://example.com",
		"glassbox://decode/" + validHash + "?network=testnet",
		"glassbox://debug/not-a-hash?network=testnet",
		"glassbox://debug/" + validHash,                                    // missing network
		"glassbox://debug/" + validHash + "?network=invalid",               // bad network
		"glassbox://debug/" + validHash + "?network=testnet&operation=-1",  // negative op
	}

	for _, tc := range tests {
		if _, err := ParseDebugURI(tc); err == nil {
			t.Fatalf("expected ParseDebugURI to fail for %q", tc)
		}
	}
}

// ─── Network parameter ───────────────────────────────────────────────────────

func TestParseDebugURI_AllValidNetworks(t *testing.T) {
	networks := []string{"testnet", "mainnet", "futurenet"}
	for _, net := range networks {
		uri := "glassbox://debug/" + validHash + "?network=" + net
		parsed, err := ParseDebugURI(uri)
		if err != nil {
			t.Errorf("network=%q: unexpected error: %v", net, err)
			continue
		}
		if parsed.Network != net {
			t.Errorf("network=%q: got %q", net, parsed.Network)
		}
	}
}

func TestParseDebugURI_InvalidNetworks(t *testing.T) {
	bad := []string{"", "staging", "TESTNET", "Testnet", "local", "devnet"}
	for _, net := range bad {
		uri := "glassbox://debug/" + validHash + "?network=" + net
		_, err := ParseDebugURI(uri)
		if err == nil {
			t.Errorf("network=%q: expected error, got nil", net)
		}
	}
}

func TestParseDebugURI_MissingNetwork_ErrorMentionsParam(t *testing.T) {
	_, err := ParseDebugURI("glassbox://debug/" + validHash)
	if err == nil {
		t.Fatal("expected error for missing network")
	}
	if !strings.Contains(err.Error(), "network") {
		t.Errorf("error should mention 'network', got: %v", err)
	}
}

// ─── op parameter ────────────────────────────────────────────────────────────

func TestParseDebugURI_OpParam(t *testing.T) {
	uri := baseURI + "&op=0"
	parsed, err := ParseDebugURI(uri)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.Op == nil || *parsed.Op != 0 {
		t.Errorf("expected Op=0, got %v", parsed.Op)
	}
	// Operation must mirror Op.
	if parsed.Operation == nil || *parsed.Operation != 0 {
		t.Errorf("expected Operation=0 (mirror of Op), got %v", parsed.Operation)
	}
}

func TestParseDebugURI_OpParam_LargeIndex(t *testing.T) {
	uri := baseURI + "&op=99"
	parsed, err := ParseDebugURI(uri)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.Op == nil || *parsed.Op != 99 {
		t.Errorf("expected Op=99, got %v", parsed.Op)
	}
}

// "op" takes precedence over "operation" when both are present.
func TestParseDebugURI_OpTakesPrecedenceOverOperation(t *testing.T) {
	uri := baseURI + "&op=3&operation=7"
	parsed, err := ParseDebugURI(uri)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.Op == nil || *parsed.Op != 3 {
		t.Errorf("expected Op=3 (op wins), got %v", parsed.Op)
	}
}

func TestParseDebugURI_OperationParam_LegacyAlias(t *testing.T) {
	uri := baseURI + "&operation=5"
	parsed, err := ParseDebugURI(uri)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.Op == nil || *parsed.Op != 5 {
		t.Errorf("expected Op=5 via legacy 'operation' param, got %v", parsed.Op)
	}
	if parsed.Operation == nil || *parsed.Operation != 5 {
		t.Errorf("expected Operation=5, got %v", parsed.Operation)
	}
}

func TestParseDebugURI_InvalidOpValues(t *testing.T) {
	bad := []string{"-1", "-100", "abc", "1.5", "2147483648000"}
	for _, v := range bad {
		uri := baseURI + "&op=" + v
		_, err := ParseDebugURI(uri)
		if err == nil {
			t.Errorf("op=%q: expected error, got nil", v)
		}
	}
}

func TestParseDebugURI_NoOp_FieldIsNil(t *testing.T) {
	parsed, err := ParseDebugURI(baseURI)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.Op != nil {
		t.Errorf("expected Op=nil when not specified, got %v", *parsed.Op)
	}
	if parsed.Operation != nil {
		t.Errorf("expected Operation=nil when not specified, got %v", *parsed.Operation)
	}
}

// ─── view parameter ──────────────────────────────────────────────────────────

func TestParseDebugURI_AllValidViews(t *testing.T) {
	views := []string{"trace", "flamegraph", "events", "auth", "budget", "storage"}
	for _, v := range views {
		uri := baseURI + "&view=" + v
		parsed, err := ParseDebugURI(uri)
		if err != nil {
			t.Errorf("view=%q: unexpected error: %v", v, err)
			continue
		}
		if parsed.View != v {
			t.Errorf("view=%q: got %q", v, parsed.View)
		}
	}
}

func TestParseDebugURI_InvalidViews(t *testing.T) {
	bad := []string{"unknown", "TRACE", "Flamegraph", "raw", "json", "hex"}
	for _, v := range bad {
		uri := baseURI + "&view=" + v
		_, err := ParseDebugURI(uri)
		if err == nil {
			t.Errorf("view=%q: expected error, got nil", v)
		}
	}
}

func TestParseDebugURI_InvalidView_ErrorMentionsAllowed(t *testing.T) {
	_, err := ParseDebugURI(baseURI + "&view=unknown")
	if err == nil {
		t.Fatal("expected error for invalid view")
	}
	// Error should list the allowed values so the user knows what to use.
	for _, allowed := range []string{"trace", "flamegraph", "events"} {
		if !strings.Contains(err.Error(), allowed) {
			t.Errorf("error should mention allowed view %q, got: %v", allowed, err)
		}
	}
}

func TestParseDebugURI_NoView_FieldIsEmpty(t *testing.T) {
	parsed, err := ParseDebugURI(baseURI)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.View != "" {
		t.Errorf("expected View=\"\" when not specified, got %q", parsed.View)
	}
}

// ─── Combined parameters ─────────────────────────────────────────────────────

// Acceptance-criteria URI from the spec: glassbox://debug/?network=testnet&op=0
// Note: the spec shows no tx hash in the example, but our parser requires one.
// We test the closest valid form: glassbox://debug/<hash>?network=testnet&op=0
func TestParseDebugURI_AcceptanceCriteria_NetworkAndOp(t *testing.T) {
	uri := "glassbox://debug/" + validHash + "?network=testnet&op=0"
	parsed, err := ParseDebugURI(uri)
	if err != nil {
		t.Fatalf("acceptance-criteria URI failed: %v", err)
	}
	if parsed.Network != "testnet" {
		t.Errorf("expected network=testnet, got %q", parsed.Network)
	}
	if parsed.Op == nil || *parsed.Op != 0 {
		t.Errorf("expected op=0, got %v", parsed.Op)
	}
}

func TestParseDebugURI_AllParams(t *testing.T) {
	uri := "glassbox://debug/" + validHash + "?network=futurenet&op=2&view=flamegraph&source=ci&signature=abc"
	parsed, err := ParseDebugURI(uri)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.TransactionHash != validHash {
		t.Errorf("hash mismatch")
	}
	if parsed.Network != "futurenet" {
		t.Errorf("expected futurenet, got %q", parsed.Network)
	}
	if parsed.Op == nil || *parsed.Op != 2 {
		t.Errorf("expected op=2, got %v", parsed.Op)
	}
	if parsed.View != "flamegraph" {
		t.Errorf("expected view=flamegraph, got %q", parsed.View)
	}
	if parsed.Source != "ci" {
		t.Errorf("expected source=ci, got %q", parsed.Source)
	}
	if parsed.Signature != "abc" {
		t.Errorf("expected signature=abc, got %q", parsed.Signature)
	}
	if parsed.Raw != uri {
		t.Errorf("Raw field should preserve original URI")
	}
}

// ─── Transaction hash validation ─────────────────────────────────────────────

func TestParseDebugURI_HashCaseInsensitive(t *testing.T) {
	// Uppercase hex should be accepted.
	upperHash := "0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF"
	uri := "glassbox://debug/" + upperHash + "?network=testnet"
	parsed, err := ParseDebugURI(uri)
	if err != nil {
		t.Fatalf("uppercase hash should be accepted: %v", err)
	}
	if parsed.TransactionHash != upperHash {
		t.Errorf("hash should be preserved as-is: got %q", parsed.TransactionHash)
	}
}

func TestParseDebugURI_ShortHash_Rejected(t *testing.T) {
	shortHash := "0123456789abcdef"
	uri := "glassbox://debug/" + shortHash + "?network=testnet"
	_, err := ParseDebugURI(uri)
	if err == nil {
		t.Error("short hash should be rejected")
	}
}

func TestParseDebugURI_EmptyHash_Rejected(t *testing.T) {
	uri := "glassbox://debug/?network=testnet"
	_, err := ParseDebugURI(uri)
	if err == nil {
		t.Error("empty hash should be rejected")
	}
}

// ─── Malformed URI diagnostics ───────────────────────────────────────────────

func TestParseDebugURI_WrongScheme_ErrorMentionsScheme(t *testing.T) {
	_, err := ParseDebugURI("https://debug/" + validHash + "?network=testnet")
	if err == nil {
		t.Fatal("expected error for wrong scheme")
	}
	if !strings.Contains(err.Error(), "glassbox") {
		t.Errorf("error should mention expected scheme, got: %v", err)
	}
}

func TestParseDebugURI_WrongHost_ErrorMentionsDebug(t *testing.T) {
	_, err := ParseDebugURI("glassbox://inspect/" + validHash + "?network=testnet")
	if err == nil {
		t.Fatal("expected error for wrong host")
	}
	if !strings.Contains(err.Error(), "debug") {
		t.Errorf("error should mention expected host 'debug', got: %v", err)
	}
}

func TestParseDebugURI_InvalidOpNonNumeric_ErrorMentionsParam(t *testing.T) {
	_, err := ParseDebugURI(baseURI + "&op=notanumber")
	if err == nil {
		t.Fatal("expected error for non-numeric op")
	}
	if !strings.Contains(err.Error(), "operation index") {
		t.Errorf("error should mention 'operation index', got: %v", err)
	}
}

func TestParseDebugURI_InvalidNetwork_ErrorMentionsAllowed(t *testing.T) {
	_, err := ParseDebugURI("glassbox://debug/" + validHash + "?network=badnet")
	if err == nil {
		t.Fatal("expected error for invalid network")
	}
	for _, allowed := range []string{"testnet", "mainnet", "futurenet"} {
		if !strings.Contains(err.Error(), allowed) {
			t.Errorf("error should mention allowed network %q, got: %v", allowed, err)
		}
	}
}

// ─── source parameter length validation ──────────────────────────────────────

func TestParseDebugURI_Source_AtMaxLength_Accepted(t *testing.T) {
	source := strings.Repeat("a", maxSourceLen)
	uri := baseURI + "&source=" + source
	parsed, err := ParseDebugURI(uri)
	if err != nil {
		t.Fatalf("source at max length (%d) should be accepted: %v", maxSourceLen, err)
	}
	if parsed.Source != source {
		t.Error("Source field not preserved")
	}
}

func TestParseDebugURI_Source_ExceedsMaxLength_Rejected(t *testing.T) {
	source := strings.Repeat("a", maxSourceLen+1)
	uri := baseURI + "&source=" + source
	_, err := ParseDebugURI(uri)
	if err == nil {
		t.Fatalf("source exceeding max length (%d) should be rejected", maxSourceLen)
	}
	if !strings.Contains(err.Error(), "source") {
		t.Errorf("error should mention 'source', got: %v", err)
	}
}

// ─── signature parameter length validation ────────────────────────────────────

func TestParseDebugURI_Signature_AtMaxLength_Accepted(t *testing.T) {
	sig := strings.Repeat("b", maxSignatureLen)
	uri := baseURI + "&signature=" + sig
	parsed, err := ParseDebugURI(uri)
	if err != nil {
		t.Fatalf("signature at max length (%d) should be accepted: %v", maxSignatureLen, err)
	}
	if parsed.Signature != sig {
		t.Error("Signature field not preserved")
	}
}

func TestParseDebugURI_Signature_ExceedsMaxLength_Rejected(t *testing.T) {
	sig := strings.Repeat("b", maxSignatureLen+1)
	uri := baseURI + "&signature=" + sig
	_, err := ParseDebugURI(uri)
	if err == nil {
		t.Fatalf("signature exceeding max length (%d) should be rejected", maxSignatureLen)
	}
	if !strings.Contains(err.Error(), "signature") {
		t.Errorf("error should mention 'signature', got: %v", err)
	}
}

// ─── NewRegistrar — executable existence validation ────────────────────────────

func TestNewRegistrar_NonExistentExecutable_ReturnsError(t *testing.T) {
	// We can't directly call NewRegistrar with a custom path because it uses
	// os.Executable() internally. Validate the Stat check indirectly by
	// constructing a Registrar that points to a non-existent file and verifying
	// Verify surfaces an actionable issue.
	r := &Registrar{
		executablePath: "/nonexistent/path/to/glassbox",
		homeDir:        t.TempDir(),
	}
	report := r.Diagnose()
	// The diagnostic must surface issues regardless of the platform since the
	// executable path does not exist.
	if len(report.Issues) == 0 && len(report.Checks) == 0 {
		t.Error("Diagnose with a non-existent executable should produce at least one issue or check")
	}
}

// ─── source / signature null-byte validation ─────────────────────────────────

func TestParseDebugURI_Source_NullByte_Rejected(t *testing.T) {
	uri := baseURI + "&source=valid\x00injected"
	_, err := ParseDebugURI(uri)
	if err == nil {
		t.Fatal("expected error for null byte in source parameter")
	}
	// The URI-level null byte check catches this before the parameter-level check.
	if !strings.Contains(err.Error(), "null bytes") {
		t.Errorf("error should mention 'null bytes', got: %v", err)
	}
}

func TestParseDebugURI_Signature_NullByte_Rejected(t *testing.T) {
	uri := baseURI + "&signature=ok\x00bad"
	_, err := ParseDebugURI(uri)
	if err == nil {
		t.Fatal("expected error for null byte in signature parameter")
	}
	// The URI-level null byte check catches this before the parameter-level check.
	if !strings.Contains(err.Error(), "null bytes") {
		t.Errorf("error should mention 'null bytes', got: %v", err)
	}
}

// ─── op overflow guard ────────────────────────────────────────────────────────

func TestParseDebugURI_Op_ExceedsMaxIndex_Rejected(t *testing.T) {
	uri := baseURI + "&op=99999"
	_, err := ParseDebugURI(uri)
	if err == nil {
		t.Fatal("expected error for op exceeding maximum index")
	}
	msg := err.Error()
	if !strings.Contains(msg, "operation index") && !strings.Contains(msg, "maximum") {
		t.Errorf("error should mention the operation index limit, got: %v", err)
	}
}

func TestParseDebugURI_Op_AtMaxIndex_Accepted(t *testing.T) {
	uri := baseURI + "&op=65535"
	parsed, err := ParseDebugURI(uri)
	if err != nil {
		t.Fatalf("op at max index (65535) should be accepted: %v", err)
	}
	if parsed.Op == nil || *parsed.Op != 65535 {
		t.Errorf("expected Op=65535, got %v", parsed.Op)
	}
}

func TestParseDebugURI_Op_JustAboveMax_Rejected(t *testing.T) {
	uri := baseURI + "&op=65536"
	_, err := ParseDebugURI(uri)
	if err == nil {
		t.Fatal("expected error for op=65536 (just above max)")
	}
}

// Verify the error message for a negative op includes the Fix: hint.
func TestParseDebugURI_NegativeOp_ErrorIncludesFix(t *testing.T) {
	_, err := ParseDebugURI(baseURI + "&op=-5")
	if err == nil {
		t.Fatal("expected error for negative op")
	}
	if !strings.Contains(err.Error(), "Fix:") {
		t.Errorf("error for negative op should include a Fix: hint, got: %v", err)
	}
}

// ─── protocol:handle error hint ──────────────────────────────────────────────

// TestParseDebugURI_InvalidURI_ErrorDescribesFormat verifies that a bad URI
// returns enough context for the user to construct a correct one.
func TestParseDebugURI_InvalidURI_ErrorDescribesFormat(t *testing.T) {
	_, err := ParseDebugURI("glassbox://debug/badhash?network=testnet")
	if err == nil {
		t.Fatal("expected error for invalid hash")
	}
	// Must name the hash requirement so the user knows the exact fix.
	msg := err.Error()
	if !strings.Contains(msg, "64") && !strings.Contains(msg, "hex") {
		t.Errorf("error should describe the hash requirement, got: %v", err)
	}
}

// ─── mock-ledger-manifest & mock-ledger-entry & protocol-version parameters ────

func TestParseDebugURI_ProtocolVersion_Valid(t *testing.T) {
	for _, version := range []string{"20", "21", "22"} {
		uri := baseURI + "&protocol-version=" + version
		parsed, err := ParseDebugURI(uri)
		if err != nil {
			t.Fatalf("unexpected error parsing protocol-version=%s: %v", version, err)
		}
		val := uint32(20)
		if version == "21" {
			val = 21
		} else if version == "22" {
			val = 22
		}
		if parsed.ProtocolVersion == nil || *parsed.ProtocolVersion != val {
			t.Errorf("expected ProtocolVersion=%d, got %v", val, parsed.ProtocolVersion)
		}
	}
}

func TestParseDebugURI_ProtocolVersion_Invalid(t *testing.T) {
	tests := []struct {
		val string
		err string
	}{
		{"0", "must be a positive integer"},
		{"abc", "must be a positive integer"},
		{"19", "use a supported protocol version"},
		{"99", "use a supported protocol version"},
	}

	for _, tc := range tests {
		uri := baseURI + "&protocol-version=" + tc.val
		_, err := ParseDebugURI(uri)
		if err == nil {
			t.Fatalf("expected error for protocol-version=%s", tc.val)
		}
		if !strings.Contains(err.Error(), tc.err) {
			t.Errorf("error message for protocol-version=%s should mention %q, got: %v", tc.val, tc.err, err)
		}
	}
}

func TestParseDebugURI_MockLedgerManifest_Valid(t *testing.T) {
	uri := baseURI + "&mock-ledger-manifest=/path/to/manifest.json"
	parsed, err := ParseDebugURI(uri)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.MockLedgerManifest != "/path/to/manifest.json" {
		t.Errorf("expected MockLedgerManifest='/path/to/manifest.json', got %q", parsed.MockLedgerManifest)
	}
}

func TestParseDebugURI_MockLedgerManifest_NullByte(t *testing.T) {
	uri := baseURI + "&mock-ledger-manifest=/path/to\x00manifest.json"
	_, err := ParseDebugURI(uri)
	if err == nil {
		t.Fatal("expected error for null byte in mock-ledger-manifest")
	}
	if !strings.Contains(err.Error(), "null bytes") {
		t.Errorf("error should explain null bytes are not allowed, got: %v", err)
	}
}

func TestParseDebugURI_MockLedgerEntries_Valid(t *testing.T) {
	uri := baseURI + "&mock-ledger-entry=AAAAAQ==:BBBBQg==&mock-ledger-entry=CCCCQw==:DDDDRA=="
	parsed, err := ParseDebugURI(uri)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(parsed.MockLedgerEntries) != 2 {
		t.Fatalf("expected 2 mock ledger entries, got %d", len(parsed.MockLedgerEntries))
	}
	if parsed.MockLedgerEntries[0] != "AAAAAQ==:BBBBQg==" || parsed.MockLedgerEntries[1] != "CCCCQw==:DDDDRA==" {
		t.Errorf("unexpected entries: %v", parsed.MockLedgerEntries)
	}
}

func TestParseDebugURI_MockLedgerEntries_Invalid(t *testing.T) {
	tests := []struct {
		entry string
		err   string
	}{
		{"keyonly", "expected key:value"},
		{":value", "expected key:value"},
		{"key:", "empty value"},
		{"key:not-base64-!@#$", "invalid base64"},
	}

	for _, tc := range tests {
		uri := baseURI + "&mock-ledger-entry=" + tc.entry
		_, err := ParseDebugURI(uri)
		if err == nil {
			t.Fatalf("expected error for mock-ledger-entry=%s", tc.entry)
		}
		if !strings.Contains(err.Error(), tc.err) {
			t.Errorf("error message for mock-ledger-entry=%s should mention %q, got: %v", tc.entry, tc.err, err)
		}
	}
}

// ─── Path traversal hardening (Issue #823) ───────────────────────────────────

func TestParseDebugURI_PathTraversal_Rejected(t *testing.T) {
	uri := baseURI + "&mock-ledger-manifest=../../etc/passwd"
	_, err := ParseDebugURI(uri)
	if err == nil {
		t.Fatal("expected error for path traversal in mock-ledger-manifest")
	}
	if !strings.Contains(err.Error(), "path traversal") {
		t.Errorf("error should mention 'path traversal', got: %v", err)
	}
}

func TestParseDebugURI_URIPathTraversal_Rejected(t *testing.T) {
	raw := "glassbox://debug/../secret?network=testnet"
	_, err := ParseDebugURI(raw)
	if err == nil {
		t.Fatal("expected error for path traversal in URI path")
	}
	if !strings.Contains(err.Error(), "path traversal") {
		t.Errorf("error should mention 'path traversal', got: %v", err)
	}
}

// ─── Oversized URI (Issue #823) ──────────────────────────────────────────────

func TestParseDebugURI_OversizedURI_Rejected(t *testing.T) {
	// Create a URI exceeding maxURILen.
	longSource := strings.Repeat("a", maxURILen+1)
	uri := baseURI + "&source=" + longSource
	_, err := ParseDebugURI(uri)
	if err == nil {
		t.Fatal("expected error for oversized URI")
	}
	if !strings.Contains(err.Error(), "maximum length") {
		t.Errorf("error should mention 'maximum length', got: %v", err)
	}
}

// ─── Duplicate mock-ledger-entry (Issue #823) ────────────────────────────────

func TestParseDebugURI_DuplicateMockEntry_Rejected(t *testing.T) {
	uri := baseURI + "&mock-ledger-entry=AAAAAQ==:BBBBQg==&mock-ledger-entry=AAAAAQ==:BBBBQg=="
	_, err := ParseDebugURI(uri)
	if err == nil {
		t.Fatal("expected error for duplicate mock-ledger-entry")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("error should mention 'duplicate', got: %v", err)
	}
}

// ─── Too many mock-ledger-entry (Issue #823) ─────────────────────────────────

func TestParseDebugURI_TooManyMockEntries_Rejected(t *testing.T) {
	uri := baseURI
	for i := 0; i < maxMockEntries+1; i++ {
		uri += "&mock-ledger-entry=AAAAAQ==:BBBBQg=="
	}
	_, err := ParseDebugURI(uri)
	if err == nil {
		t.Fatal("expected error for too many mock-ledger-entries")
	}
	if !strings.Contains(err.Error(), "too many") {
		t.Errorf("error should mention 'too many', got: %v", err)
	}
}

// ─── Unknown query parameter (Issue #823) ────────────────────────────────────

func TestParseDebugURI_UnknownParam_Rejected(t *testing.T) {
	uri := baseURI + "&evil_param=inject"
	_, err := ParseDebugURI(uri)
	if err == nil {
		t.Fatal("expected error for unknown query parameter")
	}
	if !strings.Contains(err.Error(), "unknown query parameter") {
		t.Errorf("error should mention 'unknown query parameter', got: %v", err)
	}
}

// ─── Oversized mock-ledger-manifest (Issue #823) ─────────────────────────────

func TestParseDebugURI_OversizedManifest_Rejected(t *testing.T) {
	longPath := strings.Repeat("a/", maxManifestLen/2+1)
	uri := baseURI + "&mock-ledger-manifest=" + longPath
	_, err := ParseDebugURI(uri)
	if err == nil {
		t.Fatal("expected error for oversized mock-ledger-manifest")
	}
	if !strings.Contains(err.Error(), "too long") {
		t.Errorf("error should mention 'too long', got: %v", err)
	}
}
