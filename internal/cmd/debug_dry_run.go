// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/dotandev/glassbox/internal/clioutput"
	"github.com/dotandev/glassbox/internal/config"
	"github.com/dotandev/glassbox/internal/plan"
	"github.com/dotandev/glassbox/internal/rpc"
	"github.com/dotandev/glassbox/internal/sourcemap"
	"github.com/dotandev/glassbox/internal/trace"
	"github.com/spf13/cobra"
)

// runDebugDryRun validates debug command inputs and environment without running
// the simulator or fetching full transaction data for replay.
func runDebugDryRun(cmd *cobra.Command, txHash string) error {
	out := cmd.OutOrStdout()
	errOut := cmd.ErrOrStderr()
	var failures []string

	fmt.Fprintf(out, "Dry-run: validating debug parameters (no replay will be executed)\n\n")

	// Transaction hash format
	if err := rpc.ValidateTransactionHash(txHash); err != nil {
		failures = append(failures, fmt.Sprintf("transaction hash: %v", err))
		fmt.Fprintf(errOut, "[FAIL] Invalid transaction hash format: %v\n"+
			"       Fix: transaction hashes must be 64 lowercase hexadecimal characters\n"+
			"       Example: 5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab\n", err)
	} else {
		fmt.Fprintf(out, "[OK]   Transaction hash format is valid (%d hex chars)\n", len(txHash))
	}

	// Network selection — accept both built-in and custom-configured networks.
	if err := validateNetworkName(networkFlag); err != nil {
		failures = append(failures, fmt.Sprintf("network: %v", err))
		fmt.Fprintf(errOut, "[FAIL] Invalid network %q — must be testnet, mainnet, futurenet, or a custom network defined in config\n"+
			"       Fix: use --network testnet, --network mainnet, or --network futurenet\n"+
			"       Or define a custom network in glassbox.toml under [networks]\n", networkFlag)
	} else {
		fmt.Fprintf(out, "[OK]   Network selection: %s\n", networkFlag)
	}

	if compareNetworkFlag != "" {
		if err := validateNetworkName(compareNetworkFlag); err != nil {
			failures = append(failures, fmt.Sprintf("compare-network: %v", err))
			fmt.Fprintf(errOut, "[FAIL] Invalid compare network %q — must be testnet, mainnet, futurenet, or a custom network defined in config\n"+
				"       Fix: use --compare-network testnet, --compare-network mainnet, or --compare-network futurenet\n"+
				"       Or define a custom network in glassbox.toml under [networks]\n", compareNetworkFlag)
		} else {
			if strings.EqualFold(networkFlag, compareNetworkFlag) {
				failures = append(failures, fmt.Sprintf("compare-network: cannot be the same as primary network %q", networkFlag))
				fmt.Fprintf(errOut, "[FAIL] --compare-network must be different from --network; both are %q\n"+
					"       Fix: select a different network for --compare-network to enable cross-network comparison\n"+
					"       Example: --network testnet --compare-network mainnet\n", networkFlag)
			} else {
				fmt.Fprintf(out, "[OK]   Compare network: %s\n", compareNetworkFlag)
			}
		}
	}

	// RPC endpoint reachability
	token := rpcTokenFlag
	if token == "" {
		token = os.Getenv("GLASSBOX_RPC_TOKEN")
	}

	// Validate RPC URL format if provided
	if rpcURLFlag != "" {
		if err := validateRPCURL(rpcURLFlag); err != nil {
			failures = append(failures, fmt.Sprintf("rpc-url: %v", err))
			fmt.Fprintf(errOut, "[FAIL] Invalid RPC URL %q: %v\n"+
				"       Fix: provide a valid HTTP or HTTPS URL\n"+
				"       Example: --rpc-url https://soroban-testnet.stellar.org\n", rpcURLFlag, err)
		} else {
			fmt.Fprintf(out, "[OK]   Custom RPC URL format is valid: %s\n", rpcURLFlag)
		}
	}

	opts, err := networkClientOptions(networkFlag)
	if err != nil {
		failures = append(failures, fmt.Sprintf("rpc client: %v", err))
		fmt.Fprintf(errOut, "[FAIL] Failed to build RPC client options for network %q: %v\n"+
			"       Fix: ensure the network name is correct and configured properly\n"+
			"       Run 'glassbox config show' to view your current configuration\n", networkFlag, err)
	} else {
		if rpcTokenFlag != "" {
			opts = append(opts, rpc.WithToken(rpcTokenFlag))
		}
		if token != "" && rpcTokenFlag == "" {
			opts = append(opts, rpc.WithToken(token))
		}
		if rpcURLFlag != "" {
			opts = append(opts, rpc.WithHorizonURL(rpcURLFlag))
		}

		client, clientErr := rpc.NewClient(opts...)
		if clientErr != nil {
			failures = append(failures, fmt.Sprintf("rpc client: %v", clientErr))
			fmt.Fprintf(errOut, "[FAIL] Failed to create RPC client: %v\n"+
				"       Fix: verify your RPC URL and authentication token are correct\n"+
				"       Set token: export GLASSBOX_RPC_TOKEN=your_token\n", clientErr)
		} else {
			probeCtx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			health, healthErr := client.GetHealth(probeCtx)
			if healthErr != nil {
				failures = append(failures, fmt.Sprintf("endpoint reachability: %v", healthErr))
				fmt.Fprintf(errOut, "[FAIL] RPC endpoint unreachable: %v\n"+
					"       Fix: check your network connection and verify the RPC endpoint is online\n"+
					"       Run 'glassbox doctor' for a comprehensive diagnosis\n"+
					"       Try alternate endpoint: --rpc-url <alternate-url>\n",
					healthErr)
			} else {
				status := "unknown"
				if health != nil && health.Result.Status != "" {
					status = health.Result.Status
				}
				if status == "unknown" || status == "" {
					// RPC responded but returned no status — treat as a warning, not a pass.
					failures = append(failures, "endpoint reachability: RPC responded but returned no health status")
					fmt.Fprintf(errOut, "[FAIL] RPC endpoint returned an empty health status — the node may be starting up\n"+
						"       Fix: wait for the node to become ready (may take 1-2 minutes)\n"+
						"       Check node logs for startup progress\n"+
						"       Or try a different RPC endpoint with --rpc-url\n")
				} else {
					fmt.Fprintf(out, "[OK]   RPC endpoint reachable (status: %s)\n", status)
				}
			}
		}
	}

	// Simulator binary compatibility
	simDep := checkSimulator(verbose)
	if !simDep.Installed {
		failures = append(failures, "simulator: "+simDep.FixHint)
		fmt.Fprintf(errOut, "[FAIL] Simulator binary not found or incompatible\n")
		if simDep.FixHint != "" {
			fmt.Fprintf(errOut, "       Fix: %s\n", simDep.FixHint)
		}
		fmt.Fprintf(errOut, "       Detailed steps:\n")
		fmt.Fprintf(errOut, "       1. cd simulator && cargo build --release\n")
		fmt.Fprintf(errOut, "       2. Ensure the binary is in your PATH or in simulator/target/release/\n")
		fmt.Fprintf(errOut, "       3. Run 'glassbox doctor --fix' for automatic setup\n")
	} else {
		fmt.Fprintf(out, "[OK]   Simulator binary found: %s\n", simDep.Path)
		if simDep.Version != "" {
			fmt.Fprintf(out, "       Version: %s\n", simDep.Version)
		}

		// Validate simulator version compatibility
		if simDep.Version != "" {
			if err := validateSimulatorVersion(simDep.Version); err != nil {
				failures = append(failures, fmt.Sprintf("simulator version: %v", err))
				fmt.Fprintf(errOut, "[FAIL] Simulator version incompatibility: %v\n"+
					"       Fix: update the simulator to the recommended version\n"+
					"       Run: cd simulator && git pull && cargo build --release\n", err)
			} else {
				fmt.Fprintf(out, "       Version compatibility: OK\n")
			}
		}
	}

	// Additional environment checks
	fmt.Fprintln(out)
	fmt.Fprintf(out, "Additional environment checks:\n")

	// Source discovery pre-flight: validate --contract-source when set.
	if contractSourceFlag != "" {
		trimmed := strings.TrimSpace(contractSourceFlag)
		if trimmed == "" || strings.ContainsRune(trimmed, 0) {
			label := "empty or whitespace"
			if strings.ContainsRune(trimmed, 0) {
				label = "contains null bytes"
			}
			failures = append(failures, fmt.Sprintf("contract-source: path %s", label))
			fmt.Fprintf(errOut, "[FAIL] --contract-source: path %s and cannot be used\n"+
				"       Fix: provide a valid directory path without null or whitespace-only values\n", label)
		} else {
			info, statErr := os.Stat(trimmed)
			if statErr != nil {
				if os.IsNotExist(statErr) {
					failures = append(failures, fmt.Sprintf("contract-source: directory not found: %q", trimmed))
					fmt.Fprintf(errOut, "[FAIL] --contract-source directory not found: %q\n"+
						"       Fix: provide the path to your contract source directory (the one containing src/)\n"+
						"       Example: --contract-source ./contracts/my_contract/src\n", trimmed)
				} else {
					failures = append(failures, fmt.Sprintf("contract-source: cannot access %q: %v", trimmed, statErr))
					fmt.Fprintf(errOut, "[FAIL] --contract-source: cannot access %q: %v\n"+
						"       Fix: check file permissions\n", trimmed, statErr)
				}
			} else if !info.IsDir() {
				failures = append(failures, fmt.Sprintf("contract-source: %q is a file, not a directory", trimmed))
				fmt.Fprintf(errOut, "[FAIL] --contract-source: %q is a file, not a directory\n"+
					"       Fix: provide the path to a directory, not a file\n", trimmed)
			} else {
				fmt.Fprintf(out, "[OK]   Source directory: %s\n", trimmed)
			}
		}
	}

	// Source alias pre-flight: validate JSON and target directories when --source-alias is set.
	if sourceAliasFlag != "" {
		aliasBytes, readErr := os.ReadFile(sourceAliasFlag)
		if readErr != nil {
			failures = append(failures, fmt.Sprintf("source-alias: cannot read %q: %v", sourceAliasFlag, readErr))
			fmt.Fprintf(errOut, "[FAIL] --source-alias: cannot read %q: %v\n"+
				"       Fix: ensure the file exists and is readable\n", sourceAliasFlag, readErr)
		} else {
			var aliasMap map[string]string
			if jsonErr := json.Unmarshal(aliasBytes, &aliasMap); jsonErr != nil {
				failures = append(failures, fmt.Sprintf("source-alias: invalid JSON in %q: %v", sourceAliasFlag, jsonErr))
				fmt.Fprintf(errOut, "[FAIL] --source-alias: failed to parse %q as JSON: %v\n"+
					"       Fix: ensure the file is a flat JSON object\n"+
					"       Example: {\"my_crate\": \"/path/to/my_crate/src\"}\n", sourceAliasFlag, jsonErr)
			} else {
				if _, err := sourcemap.LoadAliasConfig(sourceAliasFlag); err != nil {
					failures = append(failures, fmt.Sprintf("source-alias: %v", err))
					fmt.Fprintf(errOut, "[FAIL] --source-alias validation failed: %v\n", err)
				} else {
					fmt.Fprintf(out, "[OK]   Source alias file: %s (%d mapping(s))\n", sourceAliasFlag, len(aliasMap))
				}
			}
		}
	}

	// Check for protocol version compatibility
	if protocolVersionFlag != 0 {
		if err := validateProtocolVersion(protocolVersionFlag); err != nil {
			failures = append(failures, fmt.Sprintf("protocol version: %v", err))
			fmt.Fprintf(errOut, "[FAIL] Invalid protocol version %d: %v\n"+
				"       Fix: use a supported protocol version (20-23)\n"+
				"       Omit --protocol-version to use auto-detection\n", protocolVersionFlag, err)
		} else {
			fmt.Fprintf(out, "[OK]   Protocol version %d is supported\n", protocolVersionFlag)
		}
	}

	// Validate trace output configuration
	if traceOutputFile != "" {
		if err := trace.ValidateTraceInputs(traceVerbosityFlag, debugFormatFlag, "", traceOutputFile); err != nil {
			failures = append(failures, fmt.Sprintf("trace output: %v", err))
			fmt.Fprintf(errOut, "[FAIL] Trace output validation failed: %v\n"+
				"       Fix: ensure trace output path is valid and format is correct\n", err)
		} else {
			fmt.Fprintf(out, "[OK]   Trace output configuration is valid: %s\n", traceOutputFile)
		}
	}

	fmt.Fprintln(out)
	if len(failures) > 0 {
		fmt.Fprintf(errOut, "\nDry-run FAILED: %d validation error(s)\n", len(failures))
		for i, f := range failures {
			fmt.Fprintf(errOut, "  %d. %s\n", i+1, f)
		}
		fmt.Fprintf(errOut, "\nRecommendation: Fix all errors listed above before executing the debug command.\n")
		fmt.Fprintf(errOut, "For comprehensive diagnostics, run: glassbox doctor\n")
		return fmt.Errorf("dry-run validation failed with %d error(s)", len(failures))
	}

	fmt.Fprintf(out, "Dry-run PASSED: all checks succeeded for transaction %s on %s\n", txHash, networkFlag)
	if compareNetworkFlag != "" {
		fmt.Fprintf(out, "               Compare network: %s\n", compareNetworkFlag)
	}
	fmt.Fprintf(out, "\nYou can now run the full debug command by removing the --dry-run flag.\n")
	return nil
}

// validateRPCURL checks if the provided RPC URL is valid and properly formatted.
func validateRPCURL(rawURL string) error {
	if rawURL == "" {
		return fmt.Errorf("RPC URL cannot be empty")
	}

	// Handle comma-separated URLs (fallback support)
	urls := strings.Split(rawURL, ",")
	for _, u := range urls {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}

		parsed, err := url.Parse(u)
		if err != nil {
			return fmt.Errorf("invalid URL format: %w", err)
		}

		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return fmt.Errorf("URL must use http or https scheme, got: %s", parsed.Scheme)
		}

		if parsed.Host == "" {
			return fmt.Errorf("URL must have a valid host")
		}
	}

	return nil
}

// validateSimulatorVersion checks if the simulator version is compatible with this CLI version.
func validateSimulatorVersion(version string) error {
	if version == "" {
		return fmt.Errorf("unable to determine simulator version")
	}

	// Version compatibility check - this is a basic check
	// TODO: Add more sophisticated version comparison logic
	if strings.HasPrefix(version, "0.0.") || version == "unknown" {
		return fmt.Errorf("simulator version %q appears to be a development build or too old", version)
	}

	return nil
}

// validateProtocolVersion checks if the protocol version is within supported range.
func validateProtocolVersion(version uint32) error {
	const (
		minSupportedProtocol = 20
		maxSupportedProtocol = 23
	)

	if version < minSupportedProtocol || version > maxSupportedProtocol {
		return fmt.Errorf("protocol version %d is outside the supported range (%d-%d)",
			version, minSupportedProtocol, maxSupportedProtocol)
	}

	return nil
}

// runDebugDryRunPlan builds and renders an execution plan for the debug command
// using the shared plan model. This replaces the ad hoc validation in the old
// dry-run implementation with a structured, deterministic plan that shows exactly
// what the command will do without performing any side effects.
func runDebugDryRunPlan(cmd *cobra.Command, txHash string) error {
	out := cmd.OutOrStdout()
	errOut := cmd.ErrOrStderr()

	// Run the same validation checks as the old dry-run, but collect failures
	// for a structured report instead of printing immediately.
	var failures []string

	// Transaction hash format
	if err := rpc.ValidateTransactionHash(txHash); err != nil {
		failures = append(failures, fmt.Sprintf("transaction hash: %v", err))
	}

	// Network selection
	if err := validateNetworkName(networkFlag); err != nil {
		failures = append(failures, fmt.Sprintf("network: %v", err))
	}

	if compareNetworkFlag != "" {
		if err := validateNetworkName(compareNetworkFlag); err != nil {
			failures = append(failures, fmt.Sprintf("compare-network: %v", err))
		} else if strings.EqualFold(networkFlag, compareNetworkFlag) {
			failures = append(failures, fmt.Sprintf("compare-network: cannot be the same as primary network %q", networkFlag))
		}
	}

	// RPC endpoint configuration
	if rpcURLFlag != "" {
		if err := validateRPCURL(rpcURLFlag); err != nil {
			failures = append(failures, fmt.Sprintf("rpc-url: %v", err))
		}
	}

	// Simulator binary check
	simDep := checkSimulator(false)
	if !simDep.Installed {
		failures = append(failures, "simulator: binary not found or incompatible")
	} else if simDep.Version != "" {
		if err := validateSimulatorVersion(simDep.Version); err != nil {
			failures = append(failures, fmt.Sprintf("simulator version: %v", err))
		}
	}

	// If validation failed, print errors and exit
	if len(failures) > 0 {
		fmt.Fprintf(errOut, "Dry-run validation FAILED: %d error(s)\n", len(failures))
		for i, f := range failures {
			fmt.Fprintf(errOut, "  %d. %s\n", i+1, f)
		}
		return fmt.Errorf("dry-run validation failed with %d error(s)", len(failures))
	}

	// Validation passed - build and render the execution plan
	simBinary := ""
	if simDep.Installed {
		simBinary = simDep.Path
	}
	simMode := "network"
	rpcEndpoint := rpcURLFlag
	if rpcEndpoint == "" {
		cfg, cfgErr := config.Load()
		if cfgErr == nil {
			if len(cfg.SorobanRpcUrls) > 0 {
				rpcEndpoint = cfg.SorobanRpcUrls[0]
			} else if cfg.RpcUrl != "" {
				rpcEndpoint = cfg.RpcUrl
			}
		}
	}
	var extraEndpoints []string
	if cfg, cfgErr := config.Load(); cfgErr == nil && len(cfg.SorobanRpcUrls) > 1 {
		extraEndpoints = cfg.SorobanRpcUrls[1:]
	}

	execPlan := plan.BuildDebugPlan(plan.DebugPlanOptions{
		TxHash:              txHash,
		Network:             networkFlag,
		RPCEndpoint:         rpcEndpoint,
		AdditionalEndpoints: extraEndpoints,
		PinnedEndpoint:      pinEndpointFlag,
		SimulatorBinary:     simBinary,
		SimulatorMode:       simMode,
		WasmPath:            wasmPath,
		SnapshotPath:        snapshotFlag,
		SaveSnapshotsPath:   saveSnapshotsFlag,
		LoadSnapshotsPath:   loadSnapshotsFlag,
		TraceOutputFile:     traceOutputFile,
		AuditKey:            auditKeyFlag,
		PublishIPFS:         publishIPFSFlag,
		IPFSNode:            ipfsNodeFlag,
		PublishArweave:      publishArweaveFlag,
		ArweaveGateway:      arweaveGatewayFlag,
		ExportSVG:           exportSVGFlag,
		CacheDisabled:       noCacheFlag,
		JSONOutput:          clioutput.WantsJSON(debugJSONFlag, debugFormatFlag),
	})

	fmt.Fprint(out, execPlan.RenderText())
	return nil
}
