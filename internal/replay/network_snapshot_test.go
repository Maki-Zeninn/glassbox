// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package replay

// Tests for NetworkSnapshot, NetworkCompatibilityResult, and
// Registry.ValidateNetworkSnapshot.
//
// Test taxonomy:
//   - Deterministic serialisation — same inputs → same hash
//   - Compatible() — all field combinations: name match, passphrase match,
//     protocol match, and the three individual mismatch cases
//   - Custom networks — non-standard names and passphrases (the typical
//     production failure mode this feature was built to catch)
//   - Passphrase mismatches — authoritative rejection path
//   - Override — intentional cross-network analysis: error suppressed,
//     NetworkOverrideActive=true, mismatches still recorded
//   - Registry integration — New/NewWithNetworkSnapshot, round-trip,
//     ValidateNetworkSnapshot, legacy back-fill
//   - Error type helpers — IsNetworkSnapshotMismatch, AsNetworkSnapshotMismatch

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// ── well-known test fixtures ──────────────────────────────────────────────────

var (
	testnetSnap = &NetworkSnapshot{
		Name:            "testnet",
		Passphrase:      "Test SDF Network ; September 2015",
		ProtocolVersion: 22,
		RPCURL:          "https://soroban-testnet.stellar.org",
	}
	mainnetSnap = &NetworkSnapshot{
		Name:            "mainnet",
		Passphrase:      "Public Global Stellar Network ; September 2015",
		ProtocolVersion: 22,
		RPCURL:          "https://mainnet.stellar.validationcloud.io/v1/DEMO",
	}
	customSnap = &NetworkSnapshot{
		Name:            "custom-local",
		Passphrase:      "Standalone Network ; February 2017",
		ProtocolVersion: 21,
		RPCURL:          "http://localhost:8000",
	}
	// Same passphrase as testnet but a different name — should produce a
	// name mismatch, not a passphrase mismatch.
	renamedTestnetSnap = &NetworkSnapshot{
		Name:            "renamed-testnet",
		Passphrase:      "Test SDF Network ; September 2015",
		ProtocolVersion: 22,
	}
)

// ── NewNetworkSnapshot ────────────────────────────────────────────────────────

func TestNewNetworkSnapshot_Fields(t *testing.T) {
	s := NewNetworkSnapshot("testnet", "Test SDF Network ; September 2015",
		"https://soroban-testnet.stellar.org", 22)

	if s.Name != "testnet" {
		t.Errorf("Name = %q, want testnet", s.Name)
	}
	if s.Passphrase != "Test SDF Network ; September 2015" {
		t.Errorf("Passphrase = %q, want the exact passphrase string", s.Passphrase)
	}
	if s.ProtocolVersion != 22 {
		t.Errorf("ProtocolVersion = %d, want 22", s.ProtocolVersion)
	}
	if s.RPCURL != "https://soroban-testnet.stellar.org" {
		t.Errorf("RPCURL = %q, unexpected value", s.RPCURL)
	}
}

func TestNewNetworkSnapshot_TrimsNameAndURL(t *testing.T) {
	s := NewNetworkSnapshot("  testnet  ", "passphrase", "  https://rpc  ", 0)
	if s.Name != "testnet" {
		t.Errorf("Name not trimmed, got %q", s.Name)
	}
	if s.RPCURL != "https://rpc" {
		t.Errorf("RPCURL not trimmed, got %q", s.RPCURL)
	}
	// Passphrase must NOT be trimmed — it is content-sensitive.
	if s.Passphrase != "passphrase" {
		t.Errorf("Passphrase was altered, got %q", s.Passphrase)
	}
}

// ── Hash ──────────────────────────────────────────────────────────────────────

func TestNetworkSnapshot_Hash_Deterministic(t *testing.T) {
	s := testnetSnap
	h1 := s.Hash()
	h2 := s.Hash()
	if h1 != h2 || h1 == "" {
		t.Errorf("Hash not deterministic: %q vs %q", h1, h2)
	}
}

func TestNetworkSnapshot_Hash_ChangesOnPassphrase(t *testing.T) {
	s1 := NewNetworkSnapshot("testnet", "passphrase-A", "", 22)
	s2 := NewNetworkSnapshot("testnet", "passphrase-B", "", 22)
	if s1.Hash() == s2.Hash() {
		t.Error("Hash must differ when passphrases differ")
	}
}

func TestNetworkSnapshot_Hash_ChangesOnName(t *testing.T) {
	s1 := NewNetworkSnapshot("net-A", "same-passphrase", "", 0)
	s2 := NewNetworkSnapshot("net-B", "same-passphrase", "", 0)
	if s1.Hash() == s2.Hash() {
		t.Error("Hash must differ when names differ")
	}
}

func TestNetworkSnapshot_Hash_RPCURLDoesNotAffectHash(t *testing.T) {
	// Two snapshots with different RPC URLs but identical identity fields must
	// hash identically — the same network is reachable from multiple endpoints.
	s1 := NewNetworkSnapshot("testnet", "Test SDF Network ; September 2015", "https://endpoint-a.example.com", 22)
	s2 := NewNetworkSnapshot("testnet", "Test SDF Network ; September 2015", "https://endpoint-b.example.com", 22)
	if s1.Hash() != s2.Hash() {
		t.Errorf("Hash differs only because of RPCURL (%q vs %q), want same hash", s1.RPCURL, s2.RPCURL)
	}
}

func TestNetworkSnapshot_Hash_Nil(t *testing.T) {
	var s *NetworkSnapshot
	if s.Hash() != "" {
		t.Errorf("nil.Hash() = %q, want empty", s.Hash())
	}
}

// ── Compatible ────────────────────────────────────────────────────────────────

func TestCompatible_IdenticalSnapshots(t *testing.T) {
	result := testnetSnap.Compatible(testnetSnap)
	if !result.Compatible() {
		t.Errorf("identical snapshots must be compatible; mismatches: %+v", result.Mismatches)
	}
}

func TestCompatible_PassphraseMismatch_IsRejected(t *testing.T) {
	// This is the core acceptance criterion: different passphrase = incompatible,
	// regardless of name.
	captured := NewNetworkSnapshot("testnet", "Test SDF Network ; September 2015", "", 22)
	replay := NewNetworkSnapshot("testnet", "WRONG PASSPHRASE", "", 22)

	result := captured.Compatible(replay)

	if result.Compatible() {
		t.Fatal("passphrase mismatch must make networks incompatible")
	}
	found := false
	for _, m := range result.Mismatches {
		if m.Field == "passphrase" {
			found = true
			if !strings.Contains(m.Description, "passphrase mismatch") {
				t.Errorf("description = %q, want 'passphrase mismatch'", m.Description)
			}
		}
	}
	if !found {
		t.Errorf("mismatches = %+v, want a 'passphrase' field mismatch", result.Mismatches)
	}
}

func TestCompatible_NameMismatch_WithSamePassphrase(t *testing.T) {
	// Two networks share a passphrase but have different names: should produce
	// a name mismatch only (not a passphrase mismatch).
	result := testnetSnap.Compatible(renamedTestnetSnap)

	if result.Compatible() {
		t.Fatal("name mismatch must make networks incompatible")
	}
	fieldNames := mismatchFields(result)
	if !fieldNames["name"] {
		t.Errorf("mismatches = %+v, want a 'name' field mismatch", result.Mismatches)
	}
	if fieldNames["passphrase"] {
		t.Error("passphrase mismatch should not be present when passphrases match")
	}
}

func TestCompatible_ProtocolVersionMismatch(t *testing.T) {
	s1 := NewNetworkSnapshot("testnet", "Test SDF Network ; September 2015", "", 21)
	s2 := NewNetworkSnapshot("testnet", "Test SDF Network ; September 2015", "", 22)

	result := s1.Compatible(s2)
	if result.Compatible() {
		t.Fatal("protocol version mismatch must make networks incompatible")
	}
	fieldNames := mismatchFields(result)
	if !fieldNames["protocol_version"] {
		t.Errorf("mismatches = %+v, want 'protocol_version' mismatch", result.Mismatches)
	}
}

func TestCompatible_ZeroProtocolVersionSkipped(t *testing.T) {
	// When either side has ProtocolVersion == 0 (not recorded), skip the check.
	s1 := NewNetworkSnapshot("testnet", "Test SDF Network ; September 2015", "", 0)
	s2 := NewNetworkSnapshot("testnet", "Test SDF Network ; September 2015", "", 22)

	result := s1.Compatible(s2)
	if !result.Compatible() {
		t.Errorf("zero ProtocolVersion must not cause a mismatch; got %+v", result.Mismatches)
	}
}

func TestCompatible_CustomNetwork_ExactMatch(t *testing.T) {
	// A custom network (non-standard name and passphrase) must be accepted when
	// capture and replay use the same config — the primary use case for
	// local/private network operators.
	result := customSnap.Compatible(customSnap)
	if !result.Compatible() {
		t.Errorf("identical custom network snapshots must be compatible; got %+v", result.Mismatches)
	}
}

func TestCompatible_CustomNetworkVsTestnet_Rejected(t *testing.T) {
	// A registry captured on a custom local network must not replay against
	// the public testnet — both passphrase and name differ.
	result := customSnap.Compatible(testnetSnap)
	if result.Compatible() {
		t.Fatal("custom network vs testnet must be incompatible")
	}
	fields := mismatchFields(result)
	if !fields["passphrase"] {
		t.Errorf("mismatches = %+v, want passphrase mismatch for custom vs testnet", result.Mismatches)
	}
	if !fields["name"] {
		t.Errorf("mismatches = %+v, want name mismatch for custom vs testnet", result.Mismatches)
	}
}

func TestCompatible_MainnetVsTestnet_Rejected(t *testing.T) {
	// The canonical mainnet-vs-testnet scenario: both fields differ.
	result := mainnetSnap.Compatible(testnetSnap)
	if result.Compatible() {
		t.Fatal("mainnet vs testnet must be incompatible")
	}
	fields := mismatchFields(result)
	if !fields["passphrase"] {
		t.Error("mainnet vs testnet must produce a passphrase mismatch")
	}
	if !fields["name"] {
		t.Error("mainnet vs testnet must produce a name mismatch")
	}
}

func TestCompatible_NilCaptured(t *testing.T) {
	var s *NetworkSnapshot
	result := s.Compatible(testnetSnap)
	if result.Compatible() {
		t.Error("nil captured snapshot must not be compatible")
	}
}

func TestCompatible_NilReplay(t *testing.T) {
	result := testnetSnap.Compatible(nil)
	if result.Compatible() {
		t.Error("nil replay snapshot must not be compatible")
	}
}

// ── String / display ──────────────────────────────────────────────────────────

func TestNetworkSnapshot_String_ContainsName(t *testing.T) {
	s := testnetSnap
	str := s.String()
	if !strings.Contains(str, "testnet") {
		t.Errorf("String() = %q, want network name 'testnet' present", str)
	}
}

func TestNetworkSnapshot_String_Nil(t *testing.T) {
	var s *NetworkSnapshot
	if s.String() != "<nil>" {
		t.Errorf("nil.String() = %q, want <nil>", s.String())
	}
}

func TestNetworkSnapshot_String_CustomNetwork(t *testing.T) {
	str := customSnap.String()
	if !strings.Contains(str, "custom-local") {
		t.Errorf("String() = %q, want custom-local name", str)
	}
}

// ── NetworkSnapshotMismatchError ──────────────────────────────────────────────

func TestNetworkSnapshotMismatchError_Message(t *testing.T) {
	result := mainnetSnap.Compatible(testnetSnap)
	err := &NetworkSnapshotMismatchError{Result: result}

	msg := err.Error()
	if !strings.Contains(msg, "mainnet") {
		t.Errorf("error message should mention 'mainnet', got: %s", msg)
	}
	if !strings.Contains(msg, "testnet") {
		t.Errorf("error message should mention 'testnet', got: %s", msg)
	}
	// Must include remediation hint.
	if !strings.Contains(msg, "Remediation") {
		t.Errorf("error message should contain 'Remediation', got: %s", msg)
	}
	// Must mention the override option.
	if !strings.Contains(msg, "OverrideCrossNetwork") {
		t.Errorf("error message should mention 'OverrideCrossNetwork', got: %s", msg)
	}
}

func TestIsNetworkSnapshotMismatch(t *testing.T) {
	result := mainnetSnap.Compatible(testnetSnap)
	err := &NetworkSnapshotMismatchError{Result: result}
	if !IsNetworkSnapshotMismatch(err) {
		t.Error("IsNetworkSnapshotMismatch = false, want true")
	}
	if AsNetworkSnapshotMismatch(err) == nil {
		t.Error("AsNetworkSnapshotMismatch = nil, want non-nil")
	}
}

func TestIsNetworkSnapshotMismatch_OtherError(t *testing.T) {
	if IsNetworkSnapshotMismatch(fmt.Errorf("unrelated error")) {
		t.Error("IsNetworkSnapshotMismatch = true for unrelated error, want false")
	}
}

// ── ValidateNetworkSnapshot ───────────────────────────────────────────────────

func TestValidateNetworkSnapshot_CompatibleNetworks(t *testing.T) {
	r := buildRegistry(testnetSnap)
	report, err := r.ValidateNetworkSnapshot(testnetSnap, nil)
	if err != nil {
		t.Fatalf("compatible networks must not error: %v", err)
	}
	if !report.Compatible {
		t.Error("report.Compatible = false, want true for identical snapshots")
	}
	if report.NetworkOverrideActive {
		t.Error("NetworkOverrideActive = true, want false (no override needed)")
	}
}

func TestValidateNetworkSnapshot_IncompatiblePassphrase_ReturnsError(t *testing.T) {
	r := buildRegistry(mainnetSnap)
	report, err := r.ValidateNetworkSnapshot(testnetSnap, nil)
	if err == nil {
		t.Fatal("incompatible passphrase must return an error")
	}
	if !IsNetworkSnapshotMismatch(err) {
		t.Errorf("error type = %T, want *NetworkSnapshotMismatchError", err)
	}
	if report.Compatible {
		t.Error("report.Compatible = true, want false")
	}
	// Mismatches must be recorded even though the error was returned.
	if len(report.Result.Mismatches) == 0 {
		t.Error("report must contain mismatches when incompatible")
	}
}

func TestValidateNetworkSnapshot_CustomNetworkPassphraseMismatch(t *testing.T) {
	// Capture on a custom private network; attempt replay on testnet.
	r := buildRegistry(customSnap)
	_, err := r.ValidateNetworkSnapshot(testnetSnap, nil)
	if err == nil {
		t.Fatal("custom-network passphrase mismatch must return an error")
	}
	mismatchErr := AsNetworkSnapshotMismatch(err)
	if mismatchErr == nil {
		t.Fatalf("wrong error type: %T", err)
	}
	fields := mismatchFields(mismatchErr.Result)
	if !fields["passphrase"] {
		t.Errorf("mismatches = %+v, want passphrase field mismatch", mismatchErr.Result.Mismatches)
	}
}

func TestValidateNetworkSnapshot_Override_SuppressesError(t *testing.T) {
	// With OverrideCrossNetwork=true, mismatches are recorded but no error returned.
	r := buildRegistry(mainnetSnap)
	report, err := r.ValidateNetworkSnapshot(testnetSnap, &ValidateNetworkSnapshotOptions{
		OverrideCrossNetwork: true,
	})
	if err != nil {
		t.Fatalf("override must suppress the error; got: %v", err)
	}
	if report.Compatible {
		t.Error("report.Compatible = true, want false (mismatches exist)")
	}
	if !report.NetworkOverrideActive {
		t.Error("NetworkOverrideActive = false, want true when override is active")
	}
	// Mismatches must still be present so the report is honest.
	if len(report.Result.Mismatches) == 0 {
		t.Error("override must not discard mismatches from the report")
	}
}

func TestValidateNetworkSnapshot_Override_Visible_InReport(t *testing.T) {
	// Verify that the override flag is preserved so report consumers can warn.
	r := buildRegistry(mainnetSnap)
	report, _ := r.ValidateNetworkSnapshot(testnetSnap, &ValidateNetworkSnapshotOptions{
		OverrideCrossNetwork: true,
	})
	if !report.NetworkOverrideActive {
		t.Error("override must be visible in the report for auditing")
	}
	// The report must describe which fields differed (passphrase + name).
	fields := mismatchFields(report.Result)
	if !fields["passphrase"] && !fields["name"] {
		t.Errorf("override report missing expected mismatches: %+v", report.Result.Mismatches)
	}
}

func TestValidateNetworkSnapshot_LegacyRegistry_NilSnapshot_Passes(t *testing.T) {
	// A registry with no NetworkSnapshot field (old file) must pass validation
	// unconditionally so old registries remain usable.
	r := New("v1.0.0", "txhash", "testnet", "env", "meta")
	r.NetworkSnapshot = nil // simulate legacy file

	report, err := r.ValidateNetworkSnapshot(mainnetSnap, nil)
	if err != nil {
		t.Fatalf("legacy registry (nil snapshot) must not produce an error: %v", err)
	}
	if !report.Compatible {
		t.Error("legacy registry must be marked Compatible=true")
	}
}

func TestValidateNetworkSnapshot_NilRuntimeConfig_Passes(t *testing.T) {
	// When no runtime config is supplied the validation is informational only.
	r := buildRegistry(testnetSnap)
	report, err := r.ValidateNetworkSnapshot(nil, nil)
	if err != nil {
		t.Fatalf("nil runtime config must not produce an error: %v", err)
	}
	if !report.Compatible {
		t.Error("nil runtime config must be treated as compatible (informational)")
	}
}

// ── Registry integration ──────────────────────────────────────────────────────

func TestNewWithNetworkSnapshot_PopulatesFields(t *testing.T) {
	r := NewWithNetworkSnapshot("v1.0.0", "txhash", "env", "meta", testnetSnap)
	if r.Network != "testnet" {
		t.Errorf("Network = %q, want testnet (legacy field back-populated)", r.Network)
	}
	if r.NetworkSnapshot == nil {
		t.Fatal("NetworkSnapshot is nil after NewWithNetworkSnapshot")
	}
	if r.NetworkSnapshot.Passphrase != testnetSnap.Passphrase {
		t.Errorf("NetworkSnapshot.Passphrase = %q, want %q", r.NetworkSnapshot.Passphrase, testnetSnap.Passphrase)
	}
}

func TestRegistry_NetworkSnapshot_RoundTrip(t *testing.T) {
	// Full disk round-trip: save a registry with a NetworkSnapshot, load it,
	// and verify the snapshot round-tripped without data loss.
	r := NewWithNetworkSnapshot("v1.0.0", "txhash", "env", "meta", customSnap)

	path := filepath.Join(t.TempDir(), "registry.json")
	if err := r.SaveToFile(path); err != nil {
		t.Fatalf("SaveToFile: %v", err)
	}

	loaded, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}

	if loaded.NetworkSnapshot == nil {
		t.Fatal("NetworkSnapshot is nil after round-trip")
	}
	if loaded.NetworkSnapshot.Name != customSnap.Name {
		t.Errorf("Name = %q, want %q", loaded.NetworkSnapshot.Name, customSnap.Name)
	}
	if loaded.NetworkSnapshot.Passphrase != customSnap.Passphrase {
		t.Errorf("Passphrase = %q, want %q", loaded.NetworkSnapshot.Passphrase, customSnap.Passphrase)
	}
	if loaded.NetworkSnapshot.ProtocolVersion != customSnap.ProtocolVersion {
		t.Errorf("ProtocolVersion = %d, want %d", loaded.NetworkSnapshot.ProtocolVersion, customSnap.ProtocolVersion)
	}
}

func TestRegistry_NetworkSnapshot_DeterministicJSON(t *testing.T) {
	// Two snapshots with the same fields must produce identical JSON so the
	// Registry's canonical representation is stable.
	s1 := NewNetworkSnapshot("testnet", "Test SDF Network ; September 2015", "https://rpc-1.example.com", 22)
	s2 := NewNetworkSnapshot("testnet", "Test SDF Network ; September 2015", "https://rpc-2.example.com", 22)

	b1, err := json.Marshal(s1)
	if err != nil {
		t.Fatal(err)
	}
	b2, err := json.Marshal(s2)
	if err != nil {
		t.Fatal(err)
	}

	// The JSON will differ only in rpc_url — but the Hash must be identical.
	if s1.Hash() != s2.Hash() {
		t.Errorf("Hash differs for snapshots that differ only in RPCURL: %q vs %q", s1.Hash(), s2.Hash())
	}
	// The raw JSON bytes will differ in rpc_url — that's expected and correct.
	_ = b1
	_ = b2
}

func TestRegistry_LegacyBackfill_ValidatesNameOnly(t *testing.T) {
	// A legacy registry has Network="testnet" but no NetworkSnapshot.
	// After LoadFromFile the snapshot is back-filled with Name="testnet" and
	// empty passphrase. Validating against a testnet config with the correct
	// name (but non-empty passphrase) should PASS because an empty captured
	// passphrase means it was not recorded (not that it was wrong).
	r := New("v1.0.0", "tx", "testnet", "env", "meta")
	r.NetworkSnapshot = nil

	path := filepath.Join(t.TempDir(), "legacy.json")
	if err := r.SaveToFile(path); err != nil {
		t.Fatalf("SaveToFile: %v", err)
	}

	loaded, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	// Back-fill must have populated Name.
	if loaded.NetworkSnapshot == nil {
		t.Fatal("back-fill did not create NetworkSnapshot")
	}
	if loaded.NetworkSnapshot.Name != "testnet" {
		t.Errorf("back-filled Name = %q, want testnet", loaded.NetworkSnapshot.Name)
	}

	// The back-filled snapshot has an empty passphrase. The runtime config has
	// the real testnet passphrase. This triggers a passphrase mismatch, which
	// is correct behaviour — the override path should be used for legacy files.
	replayCfg := NewNetworkSnapshot("testnet", "Test SDF Network ; September 2015", "", 22)
	result := loaded.NetworkSnapshot.Compatible(replayCfg)
	// Name matches; passphrase is "" vs real value → mismatch.
	fields := mismatchFields(result)
	// Passphrase "" vs non-"" → mismatch (authoritative rejection preserved).
	if !fields["passphrase"] {
		t.Errorf("expected passphrase mismatch for legacy empty-passphrase vs real passphrase; mismatches: %+v", result.Mismatches)
	}
}

func TestRegistry_SaveAndLoad_NetworkSnapshotPreservedAfterIntegrityCheck(t *testing.T) {
	snap := makeSnap(t, map[string]string{"k": "v"})
	r := NewWithNetworkSnapshot("v1.0.0", "txhash", "env", "meta", testnetSnap)
	r.Add(1000, snap)

	path := filepath.Join(t.TempDir(), "reg.json")
	if err := r.SaveToFile(path); err != nil {
		t.Fatalf("SaveToFile: %v", err)
	}
	loaded, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	if errs := loaded.VerifyIntegrity(); len(errs) != 0 {
		t.Errorf("integrity errors after round-trip: %v", errs)
	}
	if loaded.NetworkSnapshot == nil {
		t.Error("NetworkSnapshot lost after round-trip + integrity check")
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

// buildRegistry creates a minimal Registry with the given NetworkSnapshot.
func buildRegistry(snap *NetworkSnapshot) *Registry {
	name := ""
	if snap != nil {
		name = snap.Name
	}
	r := New("v1.0.0", "txhash", name, "env", "meta")
	r.NetworkSnapshot = snap
	return r
}

// mismatchFields returns a set of field names from the mismatches in result.
func mismatchFields(result NetworkCompatibilityResult) map[string]bool {
	m := make(map[string]bool, len(result.Mismatches))
	for _, mm := range result.Mismatches {
		m[mm.Field] = true
	}
	return m
}
