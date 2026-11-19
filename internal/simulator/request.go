// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package simulator

type SimulationRequest struct {
	EnvelopeXdr               string                 `json:"envelope_xdr"`
	ResultMetaXdr             string                 `json:"result_meta_xdr"`
	LedgerEntries             map[string]string      `json:"ledger_entries,omitempty"`
	ControlCommand            string                 `json:"control_command,omitempty"`
	RewindStep                *int                   `json:"rewind_step,omitempty"`
	ForkParams                map[string]string      `json:"fork_params,omitempty"`
	HarnessReset              bool                   `json:"harness_reset,omitempty"`
	Timestamp                 int64                  `json:"timestamp,omitempty"`
	LedgerSequence            uint32                 `json:"ledger_sequence,omitempty"`
	WasmPath                  *string                `json:"wasm_path,omitempty"`
	NoCache                   bool                   `json:"no_cache,omitempty"`
	MockArgs                  *[]string              `json:"mock_args,omitempty"`
	Profile                   bool                   `json:"profile,omitempty"`
	ProtocolVersion           *uint32                `json:"protocol_version,omitempty"`
	MockBaseFee               *uint32                `json:"mock_base_fee,omitempty"`
	MockGasPrice              *uint64                `json:"mock_gas_price,omitempty"`
	MemoryLimit               *uint64                `json:"memory_limit,omitempty"`
	EnableCoverage            bool                   `json:"enable_coverage,omitempty"`
	CoverageLCOVPath          *string                `json:"coverage_lcov_path,omitempty"`
	EnableOptimizationAdvisor bool                   `json:"enable_optimization_advisor,omitempty"`
	EnableSnapshots           bool                   `json:"enable_snapshots"`
	RestorePreamble           map[string]interface{} `json:"restore_preamble,omitempty"`
	AuthTraceOpts             *AuthTraceOptions      `json:"auth_trace_opts,omitempty"`
	CustomAuthCfg             map[string]interface{} `json:"custom_auth_config,omitempty"`
	ResourceCalibration       *ResourceCalibration   `json:"resource_calibration,omitempty"`

	SandboxNativeTokenCapStroops *uint64 `json:"sandbox_native_token_cap_stroops,omitempty"`
	SandboxMode                  bool    `json:"sandbox_mode,omitempty"`
	AllowedHostFunctions         []string `json:"allowed_host_functions,omitempty"`
	ContractWasm                 *string `json:"contract_wasm,omitempty"`
	// ContractSourcePath is an explicit path to the contract source directory
	// for source mapping. Used when auto-discovery fails (Issue #117).
	ContractSourcePath *string `json:"contract_source_path,omitempty"`
	// SkipSourceMapping bypasses DWARF parsing and Git link generation for faster raw replay.
	SkipSourceMapping bool `json:"skip_source_mapping,omitempty"`
	// BuildManifestPath is the path to a glassbox-build-manifest.json that
	// enables cross-machine source mapping from reproducible builds (Issue #45).
	BuildManifestPath *string `json:"build_manifest_path,omitempty"`
}

type ResourceCalibration struct {
	SHA256Fixed      uint64 `json:"sha256_fixed"`
	SHA256PerByte    uint64 `json:"sha256_per_byte"`
	Keccak256Fixed   uint64 `json:"keccak256_fixed"`
	Keccak256PerByte uint64 `json:"keccak256_per_byte"`
	Ed25519Fixed     uint64 `json:"ed25519_fixed"`
}

type AuthTraceOptions struct {
	Enabled              bool `json:"enabled"`
	TraceCustomContracts bool `json:"trace_custom_contracts"`
	CaptureSigDetails    bool `json:"capture_sig_details"`
	MaxEventDepth        int  `json:"max_event_depth,omitempty"`
}
