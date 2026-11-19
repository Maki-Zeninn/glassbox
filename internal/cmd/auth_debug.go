// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/dotandev/glassbox/internal/authtrace"
	"github.com/dotandev/glassbox/internal/config"
	"github.com/dotandev/glassbox/internal/errors"
	"github.com/dotandev/glassbox/internal/logger"
	"github.com/dotandev/glassbox/internal/rpc"
	"github.com/spf13/cobra"
)

var (
	authNetworkFlag    string
	authRPCURLFlag     string
	authDetailedFlag   bool
	authJSONOutputFlag bool
)

var authDebugCmd = &cobra.Command{
	Use:     "auth-debug <transaction-hash>",
	GroupID: "core",
	Short:   "Debug multi-signature and threshold-based authorization failures",
	Long: `Analyze multi-signature authorization flows and identify which signatures or thresholds failed.

All inputs are validated before any network call is made:
  • The transaction hash must be exactly 64 hexadecimal characters.
  • --rpc-url, when provided, must use http:// or https:// and include a host.
  • --network, when provided, must be one of: testnet, mainnet, futurenet.

When --network is omitted, the network is auto-detected from the transaction and
the resolved value is printed so you can confirm it is correct.

Output modes:
  --detailed  Adds summary metrics and a list of missing signers to the text report.
  --json      Emits the full authorization trace as machine-readable JSON on stdout.
              Note: --detailed has no effect with --json because JSON output
              already includes all detail.

When no Soroban authorization entries are found in the transaction, a diagnostic
warning is written to stderr explaining that the report reflects
"no failures recorded" — not verified-successful authorization.`,
	Example: `  # Analyze authorization (network auto-detected)
  glassbox auth-debug 5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab

  # Force a specific network
  glassbox auth-debug --network testnet 5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab

  # Show detailed analysis with summary metrics and missing signatures
  glassbox auth-debug --detailed 5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab

  # Machine-readable JSON output (full detail, --detailed flag is a no-op here)
  glassbox auth-debug --json 5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab

  # Use a custom RPC endpoint
  glassbox auth-debug --rpc-url https://soroban-testnet.stellar.org --network testnet 5c0a1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab`,
	Args:    cobra.ExactArgs(1),
	PreRunE: authDebugPreRunE,
	RunE:    authDebugRunE,
}

// authDebugPreRunE validates all inputs before any network connection is made.
// Invalid conditions (malformed transaction hash, unsupported network, or a
// badly formed --rpc-url) are rejected here with explicit, actionable messages.
func authDebugPreRunE(cmd *cobra.Command, args []string) error {
	// Validate the transaction hash and --rpc-url up front, before touching the
	// network. validateNetworkName is re-checked below against the possibly
	// auto-resolved network value.
	if err := validateAuthDebugInputs(args[0], authNetworkFlag, authRPCURLFlag); err != nil {
		return err
	}

	// When --network was not explicitly provided, try to auto-detect it from the
	// transaction. This only runs after the hash is known to be well-formed.
	if !cmd.Flags().Changed("network") {
		token := authResolveRPCToken()
		baseCtx := cmd.Context()
		if baseCtx == nil {
			baseCtx = context.Background()
		}
		probeCtx, probeCancel := context.WithTimeout(baseCtx, 5*time.Second)
		defer probeCancel()
		if resolved, err := rpc.ResolveNetwork(probeCtx, args[0], token); err == nil {
			authNetworkFlag = string(resolved)
			fmt.Printf("Resolved network: %s\n", authNetworkFlag)
		}
	}

	return validateNetworkName(authNetworkFlag)
}

// validateAuthDebugInputs performs the input validation for the auth-debug
// command without any network access, so it can be exercised directly in tests.
// It delegates to the package-level ValidateAuthTraceInputs for the hash and
// RPC URL checks (which collect all failures in a single pass), then falls
// through to the network-name check which also accepts custom networks from
// config.
func validateAuthDebugInputs(txHash, network, rpcURL string) error {
	// Use the package-level validator for hash + RPC URL — it returns all
	// problems in a single, structured error so the user sees every issue at once.
	if err := authtrace.ValidateAuthTraceInputs(txHash, "", rpcURL); err != nil {
		return errors.WrapValidationError(err.Error())
	}

	// Network is validated separately because auth-debug also accepts custom
	// networks defined in config, which the package-level validator does not know
	// about. An empty network is allowed here — auto-detection runs in PreRunE.
	if err := validateNetworkName(network); err != nil {
		return err
	}

	return nil
}

// authResolveRPCToken resolves the RPC token from the environment first and then
// from config, matching the precedence used elsewhere in the CLI.
func authResolveRPCToken() string {
	if token := os.Getenv("GLASSBOX_RPC_TOKEN"); token != "" {
		return token
	}
	if cfg, err := config.LoadConfig(); err == nil && cfg.RPCToken != "" {
		return cfg.RPCToken
	}
	return ""
}

func authDebugRunE(cmd *cobra.Command, args []string) error {
	txHash := args[0]

	// --detailed only affects the human-readable report; in JSON mode the full
	// detail is always emitted. Surface the no-op so the user is not surprised.
	if authJSONOutputFlag && authDetailedFlag {
		fmt.Fprintln(cmd.ErrOrStderr(), "note: --detailed has no effect together with --json; JSON output already includes full detail")
	}

	opts := []rpc.ClientOption{rpc.WithToken(authResolveRPCToken())}
	if authRPCURLFlag != "" {
		opts = append(opts, rpc.WithHorizonURL(authRPCURLFlag))
	}

	// networkClientOptions already returns an explicit, actionable error for an
	// unknown network, so it is propagated unchanged rather than re-wrapped.
	client, err := newClientForNetwork(authNetworkFlag, opts...)
	if err != nil {
		return err
	}

	logger.Logger.Info("Fetching transaction for auth analysis", "tx_hash", txHash)

	// GetTransaction already returns precise, hinted errors (transaction not
	// found vs. RPC connection failure). Propagate them verbatim instead of
	// flattening every failure into a generic "connection failed".
	resp, err := client.GetTransaction(cmd.Context(), txHash)
	if err != nil {
		return err
	}

	if len(resp.EnvelopeXdr) == 0 {
		return errors.WrapValidationError(fmt.Sprintf(
			"transaction %s was fetched but its envelope is empty; authorization cannot be analyzed\n"+
				"  Confirm the hash is correct and that --network matches where the transaction was submitted, then retry.",
			txHash))
	}

	fmt.Printf("Transaction Envelope: %d bytes\n", len(resp.EnvelopeXdr))

	traceConfig := authtrace.Config{
		TraceCustomContracts: true,
		CaptureSigDetails:    true,
		MaxEventDepth:        1000,
	}

	tracker := authtrace.NewTracker(traceConfig)
	trace := tracker.GenerateTrace()
	reporter := authtrace.NewDetailedReporter(trace)

// When no authorization events were extracted, the report's "SUCCEEDED"
// status only means "no failures were recorded" — not that authorization was
// verified. Make that explicit so the output is not misread as a pass.
// Also surface diagnostic hints from the trace, including source mapping guidance.
if !authTraceHasData(trace) {
	fmt.Fprintln(cmd.ErrOrStderr(), emptyAuthTraceNote(txHash))
	if trace.Diagnostics != nil && trace.Diagnostics.EmptyTraceReason != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "  Detail: %s\n", trace.Diagnostics.EmptyTraceReason)
	}
}

// When some events were recorded but source mapping is missing, provide
// a targeted hint so users can improve trace-to-source correlation.
if trace.Diagnostics != nil && !trace.Diagnostics.SourceMappingAvailable && len(trace.AuthEvents) > 0 {
	fmt.Fprintf(cmd.ErrOrStderr(), "  Hint: %s\n", trace.Diagnostics.SourceMappingHint)
}

	if authJSONOutputFlag {
		jsonStr, err := reporter.GenerateJSONString()
		if err != nil {
			return errors.WrapMarshalFailed(err)
		}
		fmt.Println(jsonStr)
		return nil
	}

	fmt.Println(reporter.GenerateReport())
	if authDetailedFlag {
		printDetailedAnalysis(reporter)
	}

	return nil
}

// authTraceHasData reports whether the trace carries any authorization signal
// (events or recorded failures). An empty trace produces a misleading
// "SUCCEEDED" report, so callers use this to attach an explanatory note.
func authTraceHasData(trace *authtrace.AuthTrace) bool {
	if trace == nil {
		return false
	}
	return len(trace.AuthEvents) > 0 || len(trace.Failures) > 0
}

// emptyAuthTraceNote returns the diagnostic shown when no authorization data was
// extracted from a transaction, so an empty "SUCCEEDED" report is not mistaken
// for a verified-successful authorization.
func emptyAuthTraceNote(txHash string) string {
	return fmt.Sprintf(
		"warning: no authorization events were extracted from transaction %s.\n"+
			"  The report below reflects \"no failures recorded\", not a verified-successful authorization.\n"+
			"  This is expected for transactions that contain no Soroban authorization entries.\n"+
			"  Verify the hash and --network, or run 'glassbox doctor' if you expected auth data.",
		txHash)
}

func printDetailedAnalysis(reporter *authtrace.DetailedReporter) {
	metrics := reporter.SummaryMetrics()
	fmt.Println("\n--- SUMMARY METRICS ---")
	for key, value := range metrics {
		fmt.Printf("%s: %v\n", key, value)
	}

	missingKeys := reporter.IdentifyMissingKeys()
	if len(missingKeys) > 0 {
		fmt.Println("\n--- MISSING SIGNATURES ---")
		for _, signer := range missingKeys {
			fmt.Printf("  - %s (required weight: %d)\n", signer.SignerKey, signer.Weight)
		}
	}
}

func init() {
	authDebugCmd.Flags().StringVarP(&authNetworkFlag, "network", "n", string(rpc.Mainnet), "Stellar network (testnet, mainnet, futurenet)")
	authDebugCmd.Flags().StringVar(&authRPCURLFlag, "rpc-url", "", "Custom Horizon RPC URL")
	authDebugCmd.Flags().BoolVar(&authDetailedFlag, "detailed", false, "Show detailed analysis and missing signatures")
	authDebugCmd.Flags().BoolVar(&authJSONOutputFlag, "json", false, "Output as JSON")

	_ = authDebugCmd.RegisterFlagCompletionFunc("network", completeNetworkFlag)

	rootCmd.AddCommand(authDebugCmd)
}
