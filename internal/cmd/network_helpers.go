// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"strings"

	"github.com/dotandev/glassbox/internal/config"
	"github.com/dotandev/glassbox/internal/errors"
	"github.com/dotandev/glassbox/internal/rpc"
)

func isBuiltinNetwork(name string) bool {
	switch rpc.Network(strings.ToLower(name)) {
	case rpc.Testnet, rpc.Mainnet, rpc.Futurenet:
		return true
	default:
		return false
	}
}

func networkClientOptions(name string) ([]rpc.ClientOption, error) {
	if isBuiltinNetwork(name) {
		return []rpc.ClientOption{rpc.WithNetwork(rpc.Network(strings.ToLower(name)))}, nil
	}

	cfg, err := config.GetCustomNetwork(name)
	if err != nil {
		// Preserve the underlying error so the user knows whether the network
		// is simply undefined or whether its config entry is malformed.
		return nil, errors.WrapValidationError(fmt.Sprintf(
			"invalid network %q: %v\n"+
				"  Valid built-in networks: testnet, mainnet, futurenet\n"+
				"  To use a custom network, add it under [networks.<name>] in your config file.",
			name, err,
		))
	}
	return []rpc.ClientOption{rpc.WithNetworkConfig(*cfg)}, nil
}

func newClientForNetwork(name string, extraOpts ...rpc.ClientOption) (*rpc.Client, error) {
	opts, err := networkClientOptions(name)
	if err != nil {
		return nil, err
	}
	
	// Apply resource limit flags from CLI
	if RPCResponseLimitFlag != 0 {
		opts = append(opts, rpc.WithResponsePayloadLimit(RPCResponseLimitFlag))
	}
	if RPCAggregateLimitFlag != 0 {
		opts = append(opts, rpc.WithAggregateFetchLimit(RPCAggregateLimitFlag))
	}
	
	return rpc.NewClient(append(opts, extraOpts...)...)
}

func validateNetworkName(name string) error {
	if isBuiltinNetwork(name) {
		return nil
	}
	if _, err := config.GetCustomNetwork(name); err != nil {
		return errors.WrapInvalidNetwork(name)
	}
	return nil
}
