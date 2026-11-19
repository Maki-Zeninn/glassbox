// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package plan_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dotandev/glassbox/internal/plan"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─────────────────────────────────────────────────────────────────────────────
// Plan construction
// ─────────────────────────────────────────────────────────────────────────────

func TestPlan_NewSetsCommandAndBuildTime(t *testing.T) {
	before := time.Now().UTC().Add(-time.Second)
	p := plan.New("debug")
	after := time.Now().UTC().Add(time.Second)

	assert.Equal(t, "debug", p.Command)
	assert.True(t, p.BuildTime.After(before), "BuildTime should be after test start")
	assert.True(t, p.BuildTime.Before(after), "BuildTime should be before test end")
}

func TestPlan_AddNetworkRequest(t *testing.T) {
	p := plan.New("debug")
	p.AddNetworkRequest("getTransaction", "https://soroban-testnet.stellar.org", "fetch tx")

	require.Len(t, p.NetworkRequests, 1)
	assert.Equal(t, "getTransaction", p.NetworkRequests[0].Method)
	assert.Equal(t, "https://soroban-testnet.stellar.org", p.NetworkRequests[0].Endpoint)
	assert.Equal(t, "fetch tx", p.NetworkRequests[0].Purpose)
}

func TestPlan_AddFile(t *testing.T) {
	p := plan.New("export")
	p.AddFile("write", "./state.snap.json", "Ledger state snapshot")

	require.Len(t, p.Files, 1)
	assert.Equal(t, "write", p.Files[0].Op)
	assert.Equal(t, "./state.snap.json", p.Files[0].Path)
}

func TestPlan_SetSimulator(t *testing.T) {
	p := plan.New("debug")
	p.SetSimulator("/usr/local/bin/glassbox-sim", "network", nil)

	require.NotNil(t, p.Simulator)
	assert.Equal(t, "/usr/local/bin/glassbox-sim", p.Simulator.BinaryPath)
	assert.Equal(t, "network", p.Simulator.Mode)
}

func TestPlan_SetSigning(t *testing.T) {
	p := plan.New("audit:sign")
	p.SetSigning("software", "my-key", "ed25519", "sign payload")

	require.NotNil(t, p.Signing)
	assert.Equal(t, "software", p.Signing.Provider)
	assert.Equal(t, "my-key", p.Signing.KeyIdentifier)
	assert.Equal(t, "ed25519", p.Signing.Algorithm)
}

func TestPlan_AddOutput(t *testing.T) {
	p := plan.New("debug")
	p.AddOutput("stdout", "", "simulation results")
	p.AddOutput("file", "/tmp/trace.json", "execution trace")

	require.Len(t, p.Outputs, 2)
	assert.Equal(t, "stdout", p.Outputs[0].Kind)
	assert.Equal(t, "file", p.Outputs[1].Kind)
	assert.Equal(t, "/tmp/trace.json", p.Outputs[1].Path)
}

// ─────────────────────────────────────────────────────────────────────────────
// Determinism
// ─────────────────────────────────────────────────────────────────────────────

func TestPlan_RenderText_IsDeterministic(t *testing.T) {
	// Build the same plan twice with the same inputs and verify the outputs
	// match structurally (ignoring BuildTime which varies).
	opts := plan.DebugPlanOptions{
		TxHash:          "abc123",
		Network:         "testnet",
		RPCEndpoint:     "https://soroban-testnet.stellar.org",
		SimulatorBinary: "/usr/local/bin/sim",
		SimulatorMode:   "network",
		JSONOutput:      false,
	}

	p1 := plan.BuildDebugPlan(opts)
	p2 := plan.BuildDebugPlan(opts)

	// Network requests, files, outputs should be identical.
	assert.Equal(t, len(p1.NetworkRequests), len(p2.NetworkRequests))
	assert.Equal(t, len(p1.Files), len(p2.Files))
	assert.Equal(t, len(p1.Outputs), len(p2.Outputs))
	for i := range p1.NetworkRequests {
		assert.Equal(t, p1.NetworkRequests[i], p2.NetworkRequests[i])
	}
}

func TestPlan_RenderJSON_IsDeterministic(t *testing.T) {
	p := plan.New("debug")
	p.Network = "testnet"
	p.AddNetworkRequest("getTransaction", "https://soroban-testnet.stellar.org", "fetch tx")
	p.SetSimulator("/bin/sim", "network", nil)
	p.AddOutput("stdout", "", "results")

	// Force a fixed time so JSON is byte-identical across runs.
	fixed, _ := time.Parse(time.RFC3339, "2026-07-24T00:00:00Z")
	p.BuildTime = fixed

	out1, err1 := p.RenderJSON()
	out2, err2 := p.RenderJSON()

	require.NoError(t, err1)
	require.NoError(t, err2)
	assert.Equal(t, out1, out2, "JSON rendering should be deterministic")
}

// ─────────────────────────────────────────────────────────────────────────────
// Secret redaction
// ─────────────────────────────────────────────────────────────────────────────

func TestPlan_RedactURLToken(t *testing.T) {
	p := plan.New("debug")
	// URL with a token in the query string.
	p.AddNetworkRequest("getTransaction",
		"https://example.com/rpc?token=supersecret123&network=testnet",
		"fetch tx",
	)

	text := p.RenderText()
	assert.NotContains(t, text, "supersecret123", "token must be redacted from URL")
	assert.Contains(t, text, "example.com", "host must be preserved")
	assert.Contains(t, text, "REDACTED", "REDACTED marker should appear")
}

func TestPlan_RedactHexPrivateKey(t *testing.T) {
	p := plan.New("audit:sign")
	// Pass a 64-char hex string (Ed25519 seed) as the key identifier.
	hexKey := strings.Repeat("a1b2c3d4", 8) // 64 hex chars
	p.SetSigning("software", hexKey, "ed25519", "sign")

	text := p.RenderText()
	assert.NotContains(t, text, hexKey, "raw hex key must be redacted")
	assert.Contains(t, text, "REDACTED (hex key)")
}

func TestPlan_RedactPEMKey(t *testing.T) {
	p := plan.New("audit:sign")
	pemKey := "-----BEGIN PRIVATE KEY-----\nMC4CAQAwBQYDK2VwBCIEIFakeKeyMaterial\n-----END PRIVATE KEY-----\n"
	p.SetSigning("software", pemKey, "ed25519", "sign")

	text := p.RenderText()
	assert.NotContains(t, text, "FakeKeyMaterial", "PEM key material must not appear in plan text")
	assert.Contains(t, text, "REDACTED (PEM key)")
}

func TestPlan_SafeIdentifiersPassThrough(t *testing.T) {
	p := plan.New("audit:sign")
	// PKCS#11 key labels, KMS ARNs, and fingerprints should not be redacted.
	p.SetSigning("pkcs11", "my-signing-key", "ed25519", "sign HSM")

	text := p.RenderText()
	assert.Contains(t, text, "my-signing-key", "human-safe key labels must not be redacted")
}

func TestPlan_AuditKeyInDebugPlan_Redacted(t *testing.T) {
	hexKey := strings.Repeat("deadbeef", 8) // 64 hex chars
	opts := plan.DebugPlanOptions{
		TxHash:          "abc",
		Network:         "testnet",
		SimulatorMode:   "network",
		SimulatorBinary: "/bin/sim",
		AuditKey:        hexKey,
	}
	p := plan.BuildDebugPlan(opts)
	text := p.RenderText()

	assert.NotContains(t, text, hexKey, "audit key must be redacted in debug plan")
}

// ─────────────────────────────────────────────────────────────────────────────
// JSON schema
// ─────────────────────────────────────────────────────────────────────────────

func TestPlan_RenderJSON_ValidSchema(t *testing.T) {
	opts := plan.DebugPlanOptions{
		TxHash:          "abc123",
		Network:         "testnet",
		RPCEndpoint:     "https://soroban-testnet.stellar.org",
		SimulatorBinary: "/bin/sim",
		SimulatorMode:   "network",
		TraceOutputFile: "/tmp/trace.json",
		JSONOutput:      true,
	}
	p := plan.BuildDebugPlan(opts)

	out, err := p.RenderJSON()
	require.NoError(t, err)

	// Verify the JSON is parseable and has required top-level fields.
	var decoded map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(out), &decoded))

	assert.Equal(t, "debug", decoded["command"])
	assert.Equal(t, "testnet", decoded["network"])
	assert.Contains(t, decoded, "build_time")
	assert.Contains(t, decoded, "network_requests")
	assert.Contains(t, decoded, "simulator")
	assert.Contains(t, decoded, "outputs")
}

// ─────────────────────────────────────────────────────────────────────────────
// Replay pinning in plan
// ─────────────────────────────────────────────────────────────────────────────

func TestPlan_PinnedProvider_AppearsInOutput(t *testing.T) {
	opts := plan.DebugPlanOptions{
		TxHash:         "abc",
		Network:        "testnet",
		RPCEndpoint:    "https://soroban-testnet.stellar.org",
		PinnedEndpoint: "https://pinned.example.com",
		SimulatorMode:  "network",
	}
	p := plan.BuildDebugPlan(opts)

	text := p.RenderText()
	out, err := p.RenderJSON()
	require.NoError(t, err)

	assert.Contains(t, text, "pinned.example.com",
		"pinned provider must appear in text plan")
	assert.Contains(t, text, "failover disabled",
		"text plan must note that failover is disabled when pinned")
	assert.Contains(t, out, "pinned.example.com",
		"pinned provider must appear in JSON plan")
}

// ─────────────────────────────────────────────────────────────────────────────
// Builder helpers
// ─────────────────────────────────────────────────────────────────────────────

func TestBuildAuditPlan_IncludesSigningAndPayload(t *testing.T) {
	opts := plan.AuditPlanOptions{
		PayloadFile:   "./payload.json",
		ProviderName:  "pkcs11",
		KeyIdentifier: "hsm-signing-key",
		Algorithm:     "ed25519",
		CertChainFile: "./chain.pem",
	}
	p := plan.BuildAuditPlan(opts)

	assert.Equal(t, "audit:sign", p.Command)
	require.NotNil(t, p.Signing)
	assert.Equal(t, "pkcs11", p.Signing.Provider)
	assert.Equal(t, "hsm-signing-key", p.Signing.KeyIdentifier)

	// Payload file should be a read op.
	require.Len(t, p.Files, 2) // payload + cert chain
	assert.Equal(t, "read", p.Files[0].Op)
	assert.Equal(t, "./payload.json", p.Files[0].Path)
	assert.Equal(t, "./chain.pem", p.Files[1].Path)
}

func TestBuildExportPlan_WritesSnapshot(t *testing.T) {
	opts := plan.ExportPlanOptions{
		SnapshotOutputPath: "./state.snap.json",
		IncludeMemory:      true,
		JSONOutput:         true,
	}
	p := plan.BuildExportPlan(opts)

	assert.Equal(t, "export", p.Command)
	require.Len(t, p.Files, 1)
	assert.Equal(t, "write", p.Files[0].Op)
	assert.Equal(t, "./state.snap.json", p.Files[0].Path)

	// Memory note should be present.
	found := false
	for _, n := range p.Notes {
		if strings.Contains(n, "memory") {
			found = true
		}
	}
	assert.True(t, found, "expected a note about linear memory dump")
}

func TestBuildSessionSavePlan_IncludesDBPath(t *testing.T) {
	opts := plan.SessionPlanOptions{
		SessionID: "my-session-123",
		Name:      "bug-hunt",
		DBPath:    "~/.Glassbox/sessions.db",
	}
	p := plan.BuildSessionSavePlan(opts)

	assert.Equal(t, "session save", p.Command)
	require.Len(t, p.Files, 1)
	assert.Equal(t, "write", p.Files[0].Op)

	// Session ID and name should appear in notes.
	noteText := strings.Join(p.Notes, " ")
	assert.Contains(t, noteText, "my-session-123")
	assert.Contains(t, noteText, "bug-hunt")
}

// ─────────────────────────────────────────────────────────────────────────────
// RenderText content checks
// ─────────────────────────────────────────────────────────────────────────────

func TestPlan_RenderText_ContainsDryRunDisclaimer(t *testing.T) {
	p := plan.New("debug")
	text := p.RenderText()
	assert.Contains(t, text, "dry-run plan",
		"text plan must include a disclaimer about no side effects")
	assert.Contains(t, text, "No network calls",
		"text plan must explicitly mention that no network calls are made")
}

func TestPlan_RenderText_NetworkRequests_DisplaysSucceededProvider(t *testing.T) {
	// When there is only one endpoint it should appear clearly.
	p := plan.New("debug")
	p.AddNetworkRequest("getTransaction", "https://soroban-testnet.stellar.org", "fetch tx")

	text := p.RenderText()
	assert.Contains(t, text, "soroban-testnet.stellar.org")
	assert.Contains(t, text, "getTransaction")
}

// ─────────────────────────────────────────────────────────────────────────────
// Protocol register plan builder tests
// ─────────────────────────────────────────────────────────────────────────────

func TestBuildProtocolRegisterPlan_AlreadyRegistered(t *testing.T) {
	opts := plan.ProtocolRegisterPlanOptions{
		Platform:          "linux",
		ExecutablePath:    "/usr/local/bin/glassbox",
		AlreadyRegistered: true,
		RegisteredHandler: "/usr/local/bin/glassbox",
	}
	p := plan.BuildProtocolRegisterPlan(opts)

	assert.Equal(t, "protocol:register", p.Command)
	assert.Contains(t, p.Notes, "Handler is already registered — no changes needed")
	assert.Contains(t, p.Notes, "Current handler: /usr/local/bin/glassbox")
	assert.Len(t, p.Files, 0, "No files should be written when already registered")
}

func TestBuildProtocolRegisterPlan_NewRegistration(t *testing.T) {
	opts := plan.ProtocolRegisterPlanOptions{
		Platform:          "linux",
		ExecutablePath:    "/usr/local/bin/glassbox",
		AlreadyRegistered: false,
	}
	p := plan.BuildProtocolRegisterPlan(opts)

	assert.Equal(t, "protocol:register", p.Command)
	assert.Contains(t, p.Notes, "Will register glassbox:// protocol handler")
	assert.Greater(t, len(p.Files), 0, "Files should be written for new registration")
}

func TestBuildProtocolRegisterPlan_PlatformSpecificFiles(t *testing.T) {
	// Test Linux
	pLinux := plan.BuildProtocolRegisterPlan(plan.ProtocolRegisterPlanOptions{
		Platform: "linux",
	})
	var hasDesktopFile, hasWrapperScript bool
	for _, f := range pLinux.Files {
		if strings.Contains(f.Path, "glassbox-protocol.desktop") {
			hasDesktopFile = true
		}
		if strings.Contains(f.Path, "glassbox-protocol-handler") {
			hasWrapperScript = true
		}
	}
	assert.True(t, hasDesktopFile, "Linux plan should include desktop file")
	assert.True(t, hasWrapperScript, "Linux plan should include wrapper script")

	// Test macOS
	pDarwin := plan.BuildProtocolRegisterPlan(plan.ProtocolRegisterPlanOptions{
		Platform: "darwin",
	})
	var hasPlist, hasAppExecutable bool
	for _, f := range pDarwin.Files {
		if strings.Contains(f.Path, "Info.plist") {
			hasPlist = true
		}
		if strings.Contains(f.Path, "glassbox-protocol-handler") {
			hasAppExecutable = true
		}
	}
	assert.True(t, hasPlist, "Darwin plan should include plist")
	assert.True(t, hasAppExecutable, "Darwin plan should include app executable")

	// Test Windows
	pWindows := plan.BuildProtocolRegisterPlan(plan.ProtocolRegisterPlanOptions{
		Platform: "windows",
	})
	var hasRegistryKey bool
	for _, f := range pWindows.Files {
		if strings.Contains(f.Path, "Glassbox") {
			hasRegistryKey = true
		}
	}
	assert.True(t, hasRegistryKey, "Windows plan should include registry key")
}

// ─────────────────────────────────────────────────────────────────────────────
// Snapshot plan builder tests
// ─────────────────────────────────────────────────────────────────────────────

func TestBuildSnapshotSavePlan_Basic(t *testing.T) {
	opts := plan.SnapshotSavePlanOptions{
		TxHash:     "abc123",
		Network:    "testnet",
		InputPath:  "./state.json",
		OutputPath: "./snapshot.snap.json",
	}
	p := plan.BuildSnapshotSavePlan(opts)

	assert.Equal(t, "snapshot save", p.Command)
	assert.Equal(t, "testnet", p.Network)
	assert.Contains(t, p.Notes, "Transaction: abc123")

	var hasInputRead, hasOutputWrite bool
	for _, f := range p.Files {
		if f.Op == "read" && f.Path == "./state.json" {
			hasInputRead = true
		}
		if f.Op == "write" && f.Path == "./snapshot.snap.json" {
			hasOutputWrite = true
		}
	}
	assert.True(t, hasInputRead, "Should read input file")
	assert.True(t, hasOutputWrite, "Should write output file")
}

func TestBuildSnapshotSavePlan_WithWasm(t *testing.T) {
	opts := plan.SnapshotSavePlanOptions{
		TxHash:    "abc123",
		Network:   "testnet",
		InputPath: "./state.json",
		WasmPath:  "./contract.wasm",
	}
	p := plan.BuildSnapshotSavePlan(opts)

	var hasWasmRead bool
	for _, f := range p.Files {
		if f.Op == "read" && f.Path == "./contract.wasm" {
			hasWasmRead = true
		}
	}
	assert.True(t, hasWasmRead, "Should read WASM file when provided")
}

func TestBuildSnapshotLoadPlan_WithVerification(t *testing.T) {
	opts := plan.SnapshotLoadPlanOptions{
		Path:           "./snapshot.snap.json",
		Verify:         true,
		ExpectedTxHash: "abc123",
		ExpectedNetwork: "testnet",
	}
	p := plan.BuildSnapshotLoadPlan(opts)

	assert.Equal(t, "snapshot load", p.Command)
	assert.Contains(t, p.Notes, "Integrity verification will be performed")
	assert.Contains(t, p.Notes, "Expected transaction hash: abc123")
	assert.Contains(t, p.Notes, "Expected network: testnet")

	var hasFileRead bool
	for _, f := range p.Files {
		if f.Op == "read" && f.Path == "./snapshot.snap.json" {
			hasFileRead = true
		}
	}
	assert.True(t, hasFileRead, "Should read snapshot file")
}

// ─────────────────────────────────────────────────────────────────────────────
// Release manifest plan builder tests
// ─────────────────────────────────────────────────────────────────────────────

func TestBuildGenerateReleaseManifestPlan_Basic(t *testing.T) {
	opts := plan.GenerateReleaseManifestPlanOptions{
		DistDir:   "dist/release",
		Version:   "v1.2.3",
		Commit:    "deadbeef",
		BuildDate: "2026-01-01T00:00:00Z",
		Output:    "dist/release/manifest.json",
	}
	p := plan.BuildGenerateReleaseManifestPlan(opts)

	assert.Equal(t, "generate-release-manifest", p.Command)
	assert.Contains(t, p.Notes, "Version: v1.2.3")
	assert.Contains(t, p.Notes, "Commit: deadbeef")

	var hasDistRead, hasOutputWrite bool
	for _, f := range p.Files {
		if f.Op == "read" && f.Path == "dist/release" {
			hasDistRead = true
		}
		if f.Op == "write" && f.Path == "dist/release/manifest.json" {
			hasOutputWrite = true
		}
	}
	assert.True(t, hasDistRead, "Should read dist directory")
	assert.True(t, hasOutputWrite, "Should write manifest file")
}

func TestBuildGenerateReleaseManifestPlan_WithSigning(t *testing.T) {
	opts := plan.GenerateReleaseManifestPlanOptions{
		Version:    "v1.2.3",
		Commit:     "deadbeef",
		BuildDate:  "2026-01-01T00:00:00Z",
		SigningKey: "./release-key.pem",
		Verify:     true,
	}
	p := plan.BuildGenerateReleaseManifestPlan(opts)

	assert.NotNil(t, p.Signing, "Should have signing operation")
	assert.Equal(t, "software", p.Signing.Provider)
	assert.Contains(t, p.Notes, "Post-sign verification will be performed")
}

// ─────────────────────────────────────────────────────────────────────────────
// SBOM plan builder tests
// ─────────────────────────────────────────────────────────────────────────────

func TestBuildGenerateSBOMPlan_AllEcosystems(t *testing.T) {
	opts := plan.GenerateSBOMPlanOptions{
		Version:         "v1.2.3",
		Commit:          "deadbeef",
		ToolVersion:     "v1.2.3",
		GoModulesPath:   "./go-modules.json",
		CargoLockPath:   "./Cargo.lock",
		PackageLockPath: "./package-lock.json",
		Output:          "dist/release/sbom.spdx.json",
		Verify:          true,
	}
	p := plan.BuildGenerateSBOMPlan(opts)

	assert.Equal(t, "generate-sbom", p.Command)
	assert.Contains(t, p.Notes, "Version: v1.2.3")
	assert.Contains(t, p.Notes, "SBOM validation will be performed")

	var hasGoRead, hasCargoRead, hasNpmRead, hasOutputWrite bool
	for _, f := range p.Files {
		switch f.Path {
		case "./go-modules.json":
			hasGoRead = true
		case "./Cargo.lock":
			hasCargoRead = true
		case "./package-lock.json":
			hasNpmRead = true
		case "dist/release/sbom.spdx.json":
			hasOutputWrite = true
		}
	}
	assert.True(t, hasGoRead, "Should read Go modules")
	assert.True(t, hasCargoRead, "Should read Cargo.lock")
	assert.True(t, hasNpmRead, "Should read package-lock.json")
	assert.True(t, hasOutputWrite, "Should write SBOM file")
}
