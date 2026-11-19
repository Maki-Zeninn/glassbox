// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/dotandev/glassbox/internal/daemon"
	"github.com/dotandev/glassbox/internal/errors"
	"github.com/dotandev/glassbox/internal/rpc"
	"github.com/dotandev/glassbox/internal/config"
	"github.com/dotandev/glassbox/internal/telemetry"
	"github.com/spf13/cobra"
	"strconv"
)

var (
	daemonPort      string
	daemonNetwork   string
	daemonRPCURL    string
	daemonAuthToken string
	daemonTracing   bool
	daemonOTLPURL   string
)

var daemonCmd = &cobra.Command{
	Use:     "daemon",
	GroupID: "development",
	Short:   "Start JSON-RPC server for remote debugging",
	Long: `Start a JSON-RPC 2.0 server that exposes Glassbox functionality for remote tools and IDEs.

Endpoints:
  - debug_transaction: Debug a failed transaction
  - get_trace: Get execution traces for a transaction

Example:
  Glassbox daemon --port 8080 --network testnet
  Glassbox daemon --port 8080 --auth-token secret123`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

			// Initialize OpenTelemetry if enabled via flag, env, or config (opt-in)
			telemetryEnabled := daemonTracing
			if !telemetryEnabled {
				if v := os.Getenv("GLASSBOX_TELEMETRY"); v != "" {
					if b, err := strconv.ParseBool(v); err == nil {
						telemetryEnabled = b
					}
				}
				if !telemetryEnabled {
					if cfg, err := config.Load(); err == nil && cfg.TelemetryEnabled {
						telemetryEnabled = true
						if daemonOTLPURL == "" && cfg.TelemetryEndpoint != "" {
							daemonOTLPURL = cfg.TelemetryEndpoint
						}
					}
				}
			}

			var cleanup func()
			if telemetryEnabled {
				var err error
				cleanup, err = telemetry.Init(ctx, telemetry.Config{
					Enabled:     true,
					ExporterURL: daemonOTLPURL,
					ServiceName: "Glassbox-daemon",
				})
				if err != nil {
					return errors.WrapValidationError(fmt.Sprintf("failed to initialize telemetry: %v", err))
				}
				defer cleanup()
			}

		// Validate network
		switch rpc.Network(daemonNetwork) {
		case rpc.Testnet, rpc.Mainnet, rpc.Futurenet:
		default:
			return errors.WrapInvalidNetwork(daemonNetwork)
		}

		// Create server
		server, err := daemon.NewServer(daemon.Config{
			Port:      daemonPort,
			Network:   daemonNetwork,
			RPCURL:    daemonRPCURL,
			AuthToken: daemonAuthToken,
			Auth: daemon.AuthConfig{
				Token:        daemonAuthToken,
				Role:         daemon.RoleDebug,
				BindScope:    daemon.BindLocalhost,
				AuditEnabled: true,
			},
		})
		if err != nil {
			return errors.WrapValidationError(fmt.Sprintf("failed to create server: %v", err))
		}

		// Setup graceful shutdown
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		// Handle interrupt signals
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sigChan
			fmt.Println("\nReceived interrupt signal, shutting down...")
			cancel()
		}()

		fmt.Printf("Starting Glassbox daemon on port %s\n", daemonPort)
		fmt.Printf("Network: %s\n", daemonNetwork)
		if daemonRPCURL != "" {
			fmt.Printf("RPC URL: %s\n", daemonRPCURL)
		}
		if daemonAuthToken != "" {
			fmt.Println("Authentication: enabled")
		}

		// Start server
		return server.Start(ctx, daemonPort)
	},
}

func init() {
	daemonCmd.Flags().StringVarP(&daemonPort, "port", "p", "8080", "Port to listen on")
	daemonCmd.Flags().StringVarP(&daemonNetwork, "network", "n", string(rpc.Mainnet), "Stellar network to use (testnet, mainnet, futurenet)")
	daemonCmd.Flags().StringVar(&daemonRPCURL, "rpc-url", "", "Custom Horizon RPC URL to use")
	daemonCmd.Flags().StringVar(&daemonAuthToken, "auth-token", "", "Authentication token for API access")
	daemonCmd.Flags().BoolVar(&daemonTracing, "tracing", false, "Enable OpenTelemetry tracing")
	daemonCmd.Flags().StringVar(&daemonOTLPURL, "otlp-url", "http://localhost:4318", "OTLP exporter URL")

	_ = daemonCmd.RegisterFlagCompletionFunc("network", completeNetworkFlag)

	rootCmd.AddCommand(daemonCmd)
}
