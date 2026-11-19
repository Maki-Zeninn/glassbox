// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package plan

// DebugPlanOptions holds the resolved configuration needed to build an
// execution plan for the `debug` command. All values come from resolved
// flags / config — never from live network calls.
type DebugPlanOptions struct {
	// TxHash is the transaction hash being debugged (empty in WASM mode).
	TxHash string
	// Network is the Stellar network name.
	Network string
	// RPCEndpoint is the primary Soroban RPC URL (token already resolved).
	RPCEndpoint string
	// AdditionalEndpoints holds any failover URLs configured.
	AdditionalEndpoints []string
	// PinnedEndpoint is set when --pin-endpoint was passed.
	PinnedEndpoint string
	// SimulatorBinary is the resolved path to the simulator binary.
	SimulatorBinary string
	// SimulatorMode is "local" (WASM) or "network" (tx simulation).
	SimulatorMode string
	// WasmPath is set in WASM replay mode.
	WasmPath string
	// SnapshotPath is set when --snapshot is provided.
	SnapshotPath string
	// SaveSnapshotsPath is set when --save-snapshots is provided.
	SaveSnapshotsPath string
	// LoadSnapshotsPath is set when --load-snapshots is provided.
	LoadSnapshotsPath string
	// TraceOutputFile is set when --trace-output is provided.
	TraceOutputFile string
	// AuditKey is set when --audit-key is passed (raw value; will be redacted).
	AuditKey string
	// PublishIPFS is true when --publish-ipfs is set.
	PublishIPFS bool
	// IPFSNode is the IPFS node URL.
	IPFSNode string
	// PublishArweave is true when --publish-arweave is set.
	PublishArweave bool
	// ArweaveGateway is the Arweave gateway URL.
	ArweaveGateway string
	// ExportSVG is the path for SVG call graph export.
	ExportSVG string
	// CacheDisabled is true when --no-cache was set.
	CacheDisabled bool
	// JSONOutput is true when --json or --format=json was set.
	JSONOutput bool
}

// BuildDebugPlan constructs an ExecutionPlan for the `debug` command.
func BuildDebugPlan(opts DebugPlanOptions) *ExecutionPlan {
	p := New("debug")
	p.Network = opts.Network

	if opts.PinnedEndpoint != "" {
		p.PinnedProvider = opts.PinnedEndpoint
	}

	// Network requests.
	if opts.SimulatorMode == "network" {
		endpoint := opts.RPCEndpoint
		if endpoint == "" {
			endpoint = "(default " + opts.Network + " endpoint)"
		}
		p.AddNetworkRequest("getTransaction", endpoint,
			"Fetch transaction envelope and result metadata")
		if opts.SnapshotPath == "" && opts.LoadSnapshotsPath == "" {
			p.AddNetworkRequest("getLedgerEntries", endpoint,
				"Fetch ledger state entries for simulation")
		}
	}

	// Additional failover endpoints.
	for _, ep := range opts.AdditionalEndpoints {
		p.AddNote("Failover endpoint configured: %s", ep)
	}

	// File operations.
	if opts.SnapshotPath != "" {
		p.AddFile("read", opts.SnapshotPath, "Ledger state snapshot for replay")
	}
	if opts.LoadSnapshotsPath != "" {
		p.AddFile("read", opts.LoadSnapshotsPath, "Snapshot registry for time-travel replay")
	}
	if opts.WasmPath != "" {
		p.AddFile("read", opts.WasmPath, "Wasm binary for local replay")
	}
	if opts.SaveSnapshotsPath != "" {
		p.AddFile("write", opts.SaveSnapshotsPath, "Output snapshot registry")
	}
	if opts.ExportSVG != "" {
		p.AddFile("write", opts.ExportSVG, "Call graph SVG export")
	}

	// Simulator.
	simMode := opts.SimulatorMode
	if simMode == "" {
		simMode = "network"
	}
	var simArgs []string
	if opts.WasmPath != "" {
		simArgs = append(simArgs, "--wasm", opts.WasmPath)
	}
	binary := opts.SimulatorBinary
	if binary == "" {
		binary = "(auto-detected)"
	}
	p.SetSimulator(binary, simMode, simArgs)

	// Signing (audit trail).
	if opts.AuditKey != "" {
		p.SetSigning("software", opts.AuditKey, "ed25519", "Sign audit trail before publishing")
	}

	// Outputs.
	if opts.JSONOutput {
		p.AddOutput("stdout", "", "JSON simulation results")
	} else {
		p.AddOutput("stdout", "", "Text simulation results")
	}
	if opts.TraceOutputFile != "" {
		p.AddFile("write", opts.TraceOutputFile, "Execution trace export")
		p.AddOutput("file", opts.TraceOutputFile, "Execution trace (JSON)")
	}
	if opts.PublishIPFS {
		node := opts.IPFSNode
		if node == "" {
			node = "(public gateway)"
		}
		p.AddOutput("ipfs", node, "Signed audit trail published to IPFS")
	}
	if opts.PublishArweave {
		gw := opts.ArweaveGateway
		if gw == "" {
			gw = "(default gateway)"
		}
		p.AddOutput("arweave", gw, "Signed audit trail published to Arweave")
	}

	if opts.CacheDisabled {
		p.AddNote("Ledger entry cache disabled (--no-cache)")
	}

	return p
}

// AuditPlanOptions holds the resolved configuration for the `audit:sign` command.
type AuditPlanOptions struct {
	// PayloadFile is the path to the payload file, or "" if --payload was used.
	PayloadFile string
	// ProviderName is the resolved signing provider (e.g. "software", "pkcs11").
	ProviderName string
	// KeyIdentifier is a safe identifier for the key (label, ARN, fingerprint).
	KeyIdentifier string
	// Algorithm is the signing algorithm.
	Algorithm string
	// CertChainFile is the path to the certificate chain PEM, if set.
	CertChainFile string
}

// BuildAuditPlan constructs an ExecutionPlan for the `audit:sign` command.
func BuildAuditPlan(opts AuditPlanOptions) *ExecutionPlan {
	p := New("audit:sign")

	if opts.PayloadFile != "" {
		p.AddFile("read", opts.PayloadFile, "JSON payload to be signed")
	}

	p.SetSigning(opts.ProviderName, opts.KeyIdentifier, opts.Algorithm,
		"Sign payload and produce a signed audit log")

	if opts.CertChainFile != "" {
		p.AddFile("read", opts.CertChainFile, "PEM certificate chain for provenance")
	}

	p.AddOutput("stdout", "", "Signed audit log JSON")
	return p
}

// ExportPlanOptions holds the resolved configuration for the `export` command.
type ExportPlanOptions struct {
	// SnapshotOutputPath is the file that will be written.
	SnapshotOutputPath string
	// IncludeMemory is true when --include-memory was set.
	IncludeMemory bool
	// JSONOutput is true when --format=json was set.
	JSONOutput bool
}

// BuildExportPlan constructs an ExecutionPlan for the `export` command.
func BuildExportPlan(opts ExportPlanOptions) *ExecutionPlan {
	p := New("export")

	if opts.SnapshotOutputPath != "" {
		p.AddFile("write", opts.SnapshotOutputPath, "Ledger state snapshot JSON")
		p.AddOutput("file", opts.SnapshotOutputPath, "Snapshot (JSON)")
	}
	if opts.IncludeMemory {
		p.AddNote("Linear memory dump will be included in the snapshot")
	}
	if opts.JSONOutput {
		p.AddOutput("stdout", "", "Export summary (JSON)")
	} else {
		p.AddOutput("stdout", "", "Export summary (text)")
	}
	return p
}

// SessionPlanOptions holds the resolved configuration for the `session save` command.
type SessionPlanOptions struct {
	// SessionID is the session ID (or "(auto-generated)").
	SessionID string
	// Name is the optional session name.
	Name string
	// DBPath is the path to the SQLite session store.
	DBPath string
}

// BuildSessionSavePlan constructs an ExecutionPlan for the `session save` command.
func BuildSessionSavePlan(opts SessionPlanOptions) *ExecutionPlan {
	p := New("session save")

	if opts.DBPath != "" {
		p.AddFile("write", opts.DBPath, "Session store (SQLite)")
	}

	sessionID := opts.SessionID
	if sessionID == "" {
		sessionID = "(auto-generated)"
	}
	p.AddNote("Session ID: %s", sessionID)
	if opts.Name != "" {
		p.AddNote("Session name: %s", opts.Name)
	}
	p.AddOutput("stdout", "", "Confirmation with session ID")
	return p
}

// ProtocolRegisterPlanOptions holds the resolved configuration for the `protocol:register` command.
// All values come from resolved flags / diagnostic results — never from live OS writes.
type ProtocolRegisterPlanOptions struct {
	// Platform is the detected OS platform (e.g. "linux", "darwin", "windows").
	Platform string
	// ExecutablePath is the resolved path to the glassbox binary.
	ExecutablePath string
	// AlreadyRegistered is true if the handler is already registered.
	AlreadyRegistered bool
	// RegisteredHandler is the current handler path if already registered.
	RegisteredHandler string
}

// BuildProtocolRegisterPlan constructs an ExecutionPlan for the `protocol:register` command.
// This function is side-effect free: it only builds a plan describing what would be written,
// without actually modifying any OS state or opening any devices.
func BuildProtocolRegisterPlan(opts ProtocolRegisterPlanOptions) *ExecutionPlan {
	p := New("protocol:register")

	p.AddNote("Platform: %s", opts.Platform)
	p.AddNote("Executable: %s", opts.ExecutablePath)

	if opts.AlreadyRegistered {
		p.AddNote("Handler is already registered — no changes needed")
		if opts.RegisteredHandler != "" {
			p.AddNote("Current handler: %s", opts.RegisteredHandler)
		}
	} else {
		p.AddNote("Will register glassbox:// protocol handler")
		// Platform-specific file operations would be added here based on platform
		switch opts.Platform {
		case "linux":
			p.AddFile("write", "~/.local/share/applications/glassbox-protocol.desktop", "Desktop entry file")
			p.AddFile("write", "~/.local/bin/glassbox-protocol-handler", "Protocol helper script")
		case "darwin":
			p.AddFile("write", "~/Applications/Glassbox.app/Contents/Info.plist", "App bundle plist")
			p.AddFile("write", "~/Applications/Glassbox.app/Contents/MacOS/glassbox-protocol-handler", "App bundle executable")
		case "windows":
			p.AddFile("write", "HKEY_CURRENT_USER\\Software\\Classes\\Glassbox", "Registry key for protocol handler")
		}
	}

	p.AddOutput("stdout", "", "Registration confirmation")
	return p
}

// SnapshotSavePlanOptions holds the resolved configuration for the `snapshot save` command.
// All values come from resolved flags — never from live file reads or writes.
type SnapshotSavePlanOptions struct {
	// TxHash is the transaction hash the snapshot belongs to.
	TxHash string
	// Network is the Stellar network name.
	Network string
	// InputPath is the path to the input ledger-state JSON file.
	InputPath string
	// OutputPath is the path where the persisted snapshot will be written.
	OutputPath string
	// EnvelopeXdr is the base64 transaction envelope XDR (optional).
	EnvelopeXdr string
	// ResultMetaXdr is the base64 result meta XDR (optional).
	ResultMetaXdr string
	// WasmPath is the path to the WASM file for source hash (optional).
	WasmPath string
}

// BuildSnapshotSavePlan constructs an ExecutionPlan for the `snapshot save` command.
// This function is side-effect free: it only builds a plan describing what would be read/written,
// without actually accessing the filesystem.
func BuildSnapshotSavePlan(opts SnapshotSavePlanOptions) *ExecutionPlan {
	p := New("snapshot save")
	p.Network = opts.Network

	p.AddNote("Transaction: %s", opts.TxHash)

	if opts.InputPath != "" {
		p.AddFile("read", opts.InputPath, "Input ledger-state JSON file")
	}

	if opts.WasmPath != "" {
		p.AddFile("read", opts.WasmPath, "WASM binary for source hash computation")
	}

	if opts.OutputPath != "" {
		p.AddFile("write", opts.OutputPath, "Persisted snapshot JSON")
		p.AddOutput("file", opts.OutputPath, "Snapshot (JSON)")
	} else {
		p.AddOutput("file", "(default cache path)", "Snapshot (JSON)")
	}

	if opts.EnvelopeXdr != "" {
		p.AddNote("Transaction envelope XDR will be included")
	}
	if opts.ResultMetaXdr != "" {
		p.AddNote("Result metadata XDR will be included")
	}

	p.AddOutput("stdout", "", "Save confirmation with fingerprint")
	return p
}

// SnapshotLoadPlanOptions holds the resolved configuration for the `snapshot load` command.
// All values come from resolved flags — never from live file reads.
type SnapshotLoadPlanOptions struct {
	// Path is the path to the persisted snapshot file.
	Path string
	// Verify is true if integrity verification should be performed.
	Verify bool
	// ExpectedTxHash is the expected transaction hash for identity check (optional).
	ExpectedTxHash string
	// ExpectedNetwork is the expected network for identity check (optional).
	ExpectedNetwork string
}

// BuildSnapshotLoadPlan constructs an ExecutionPlan for the `snapshot load` command.
// This function is side-effect free: it only builds a plan describing what would be read,
// without actually accessing the filesystem.
func BuildSnapshotLoadPlan(opts SnapshotLoadPlanOptions) *ExecutionPlan {
	p := New("snapshot load")

	if opts.Path != "" {
		p.AddFile("read", opts.Path, "Persisted snapshot file")
	}

	if opts.Verify {
		p.AddNote("Integrity verification will be performed")
	}

	if opts.ExpectedTxHash != "" {
		p.AddNote("Expected transaction hash: %s", opts.ExpectedTxHash)
	}

	if opts.ExpectedNetwork != "" {
		p.AddNote("Expected network: %s", opts.ExpectedNetwork)
	}

	p.AddOutput("stdout", "", "Snapshot metadata and verification results")
	return p
}

// GenerateReleaseManifestPlanOptions holds the resolved configuration for the generate-release-manifest tool.
// All values come from resolved flags — never from live file reads, network calls, or signing operations.
type GenerateReleaseManifestPlanOptions struct {
	// DistDir is the directory containing release artifacts.
	DistDir string
	// Version is the release version string.
	Version string
	// Commit is the full git commit SHA.
	Commit string
	// BuildDate is the build timestamp in RFC3339 UTC.
	BuildDate string
	// SBOMRef is the filename of the SBOM artifact (optional).
	SBOMRef string
	// SigningKey is the path or literal PEM of the signing key.
	SigningKey string
	// Output is the path where the signed manifest will be written.
	Output string
	// Verify is true if post-sign verification should be performed.
	Verify bool
	// SignerIdentity is the human-readable signer identity (optional).
	SignerIdentity string
	// KeyID is the opaque key identifier (optional).
	KeyID string
}

// BuildGenerateReleaseManifestPlan constructs an ExecutionPlan for the generate-release-manifest tool.
// This function is side-effect free: it only builds a plan describing what would be done,
// without actually accessing files, making network calls, or performing signing operations.
func BuildGenerateReleaseManifestPlan(opts GenerateReleaseManifestPlanOptions) *ExecutionPlan {
	p := New("generate-release-manifest")

	p.AddNote("Version: %s", opts.Version)
	p.AddNote("Commit: %s", opts.Commit)
	p.AddNote("Build date: %s", opts.BuildDate)

	if opts.DistDir != "" {
		p.AddFile("read", opts.DistDir, "Scan release artifacts directory")
	}

	if opts.SBOMRef != "" {
		p.AddNote("SBOM reference: %s", opts.SBOMRef)
	}

	if opts.SigningKey != "" {
		p.SetSigning("software", "(ed25519 key)", "ed25519", "Sign release manifest")
	}

	if opts.Output != "" {
		p.AddFile("write", opts.Output, "Signed manifest JSON")
		p.AddOutput("file", opts.Output, "Signed manifest (JSON)")
	} else {
		p.AddOutput("stdout", "", "Signed manifest (JSON)")
	}

	if opts.Verify {
		p.AddNote("Post-sign verification will be performed")
	}

	if opts.SignerIdentity != "" {
		p.AddNote("Signer identity: %s", opts.SignerIdentity)
	}
	if opts.KeyID != "" {
		p.AddNote("Key ID: %s", opts.KeyID)
	}

	return p
}

// GenerateSBOMPlanOptions holds the resolved configuration for the generate-sbom tool.
// All values come from resolved flags — never from live file reads.
type GenerateSBOMPlanOptions struct {
	// Version is the release version string.
	Version string
	// Commit is the full git commit SHA.
	Commit string
	// ToolVersion is the Glassbox tool version.
	ToolVersion string
	// GoModulesPath is the path to go list -m -json all output file.
	GoModulesPath string
	// CargoLockPath is the path to Cargo.lock.
	CargoLockPath string
	// PackageLockPath is the path to package-lock.json.
	PackageLockPath string
	// Output is the path where the SBOM will be written.
	Output string
	// Verify is true if SBOM validation should be performed.
	Verify bool
}

// BuildGenerateSBOMPlan constructs an ExecutionPlan for the generate-sbom tool.
// This function is side-effect free: it only builds a plan describing what would be done,
// without actually accessing files or making network calls.
func BuildGenerateSBOMPlan(opts GenerateSBOMPlanOptions) *ExecutionPlan {
	p := New("generate-sbom")

	p.AddNote("Version: %s", opts.Version)
	p.AddNote("Commit: %s", opts.Commit)
	p.AddNote("Tool version: %s", opts.ToolVersion)

	if opts.GoModulesPath != "" {
		p.AddFile("read", opts.GoModulesPath, "Go modules JSON")
	}
	if opts.CargoLockPath != "" {
		p.AddFile("read", opts.CargoLockPath, "Cargo.lock")
	}
	if opts.PackageLockPath != "" {
		p.AddFile("read", opts.PackageLockPath, "package-lock.json")
	}

	if opts.Output != "" {
		p.AddFile("write", opts.Output, "SPDX 2.3 JSON SBOM")
		p.AddOutput("file", opts.Output, "SBOM (SPDX JSON)")
	} else {
		p.AddOutput("stdout", "", "SBOM (SPDX JSON)")
	}

	if opts.Verify {
		p.AddNote("SBOM validation will be performed")
	}

	return p
}
