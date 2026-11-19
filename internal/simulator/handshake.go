// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package simulator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	"github.com/dotandev/glassbox/internal/errors"
	"github.com/dotandev/glassbox/internal/ipc"
	"github.com/dotandev/glassbox/internal/logger"
)

// HandshakeResult holds the capabilities negotiated with the simulator binary.
// It is returned by PerformHandshake and may be included in diagnostic and
// replay fingerprint data.
type HandshakeResult struct {
	SimulatorBuild    string
	ProtocolVersion   uint32
	SupportedFeatures []string
	MaxRequestBytes   int64
	// MinClientVersion is the oldest client version the simulator supports.
	MinClientVersion string
	// MaxClientVersion is the newest client version the simulator supports.
	MaxClientVersion string
}

// ErrHandshakeFailed is the sentinel returned when the simulator rejects the
// handshake or does not respond within the deadline.
var ErrHandshakeFailed = errors.New("simulator handshake failed")

// ErrIncompatibleVersion is returned when the simulator's protocol version
// does not satisfy the minimum required by the Go runner.
var ErrIncompatibleVersion = errors.New("simulator protocol version incompatible")

// ErrMissingCapability is returned when a required feature is absent from the
// simulator's capability set.
var ErrMissingCapability = errors.New("simulator missing required capability")

// ErrPayloadTooLarge is returned when the simulator rejects the handshake because
// the client's MaxRequestBytes exceeds the simulator's limit.
var ErrPayloadTooLarge = errors.New("simulator rejected payload size")

// PerformHandshake spawns the simulator binary, sends a HandshakeRequest, and
// validates the HandshakeResponse. It returns ErrIncompatibleVersion when the
// simulator's protocol version does not match minProtocol, and
// ErrMissingCapability when a required feature is absent. Any of these errors
// causes the session to abort before a transaction is submitted.
//
// binaryPath is the resolved path to the glassbox-sim binary.
// minProtocol is the minimum Stellar protocol version the Go runner requires.
// required is the list of capability identifiers that must appear in the
// simulator's SupportedFeatures response.
// clientVersion is the semantic version of the Go client (e.g. "1.2.3").
func PerformHandshake(ctx context.Context, binaryPath string, minProtocol uint32, required []string, clientVersion string) (*HandshakeResult, error) {
	req := ipc.HandshakeRequest{
		Type:             ipc.HandshakeRequestType,
		ProtocolVersion:  minProtocol,
		ClientVersion:    clientVersion,
		RequiredFeatures: required,
		MaxRequestBytes:  defaultMaxHandshakeRequestBytes,
	}
	reqBytes, err := req.Marshal()
	if err != nil {
		return nil, fmt.Errorf("%w: marshal request: %v", ErrHandshakeFailed, err)
	}

	// Create lifecycle manager for handshake
	lifecycle := NewProcessLifecycle(ProcessConfig{
		BinaryPath:    binaryPath,
		Timeout:       handshakeTimeout,
		MaxStderrSize: 64 * 1024,
	})
	defer lifecycle.Cleanup()

	// Setup command with handshake request
	outBuf := limitedBuffer{limit: 512 * 1024}
	
	if err := lifecycle.Start(ctx, func(cmd *exec.Cmd) {
		cmd.Stdin = bytes.NewReader(reqBytes)
		cmd.Stdout = &outBuf
	}); err != nil {
		return nil, fmt.Errorf("%w: failed to start simulator: %v", ErrHandshakeFailed, err)
	}

	// Wait for process completion
	if err := lifecycle.Wait(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w: handshake canceled: %v", ErrHandshakeFailed, ctx.Err())
		}
		// A non-zero exit during handshake is only treated as a hard failure
		// when we also have no stdout to parse (the sim may exit 0 after the
		// handshake-only invocation).
		if outBuf.Len() == 0 {
			stderr := lifecycle.GetStderr()
			logger.Logger.Warn("Simulator handshake subprocess failed",
				"error", err, "stderr", stderr)
			return nil, fmt.Errorf("%w: simulator exited with error: %v", ErrHandshakeFailed, err)
		}
	}

	if outBuf.Len() == 0 {
		// Simulator does not implement the handshake protocol — treat as a
		// deliberate degradation: return a synthetic response that signals
		// an older build without a hard failure, unless required features
		// were specified.
		if len(required) > 0 {
			return nil, fmt.Errorf(
				"%w: simulator returned no handshake response but required features %v were requested",
				ErrHandshakeFailed, required,
			)
		}
		logger.Logger.Debug("Simulator does not support handshake; proceeding without capability negotiation")
		return &HandshakeResult{SimulatorBuild: "unknown", ProtocolVersion: minProtocol}, nil
	}

	resp, err := ipc.UnmarshalHandshakeResponse(outBuf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("%w: unmarshal response: %v", ErrHandshakeFailed, err)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("%w: %s", ErrHandshakeFailed, resp.Error)
	}
	return validateHandshake(resp, minProtocol, required)
}

// ValidateHandshakeResponse validates a HandshakeResponse that was obtained
// through a channel other than PerformHandshake (e.g. in tests that inject a
// mock response). It applies the same version and capability checks.
func ValidateHandshakeResponse(resp ipc.HandshakeResponse, minProtocol uint32, required []string) (*HandshakeResult, error) {
	return validateHandshake(resp, minProtocol, required)
}

func validateHandshake(resp ipc.HandshakeResponse, minProtocol uint32, required []string) (*HandshakeResult, error) {
	if resp.ProtocolVersion < minProtocol {
		return nil, fmt.Errorf(
			"%w: simulator reports protocol %d, minimum required is %d — "+
				"update the simulator binary to a version that supports protocol %d",
			ErrIncompatibleVersion, resp.ProtocolVersion, minProtocol, minProtocol,
		)
	}

	// Check client version range if the simulator advertised supported bounds.
	// These checks are best-effort: older simulators omit the fields entirely.
	if resp.MinClientVersion != "" || resp.MaxClientVersion != "" {
		// We cannot do a full semver comparison here, but we can log the bounds
		// for diagnostics and the caller can decide whether to proceed.
		logger.Logger.Debug("Simulator advertised client version bounds",
			"min", resp.MinClientVersion,
			"max", resp.MaxClientVersion,
		)
	}

	featureSet := make(map[string]struct{}, len(resp.SupportedFeatures))
	for _, f := range resp.SupportedFeatures {
		featureSet[f] = struct{}{}
	}
	for _, req := range required {
		if _, ok := featureSet[req]; !ok {
			return nil, fmt.Errorf(
				"%w: %q — simulator build %q does not advertise this capability; "+
					"update the simulator or remove the feature requirement",
				ErrMissingCapability, req, resp.SimulatorBuild,
			)
		}
	}

	result := &HandshakeResult{
		SimulatorBuild:    resp.SimulatorBuild,
		ProtocolVersion:   resp.ProtocolVersion,
		SupportedFeatures: resp.SupportedFeatures,
		MaxRequestBytes:   resp.MaxRequestBytes,
		MinClientVersion:  resp.MinClientVersion,
		MaxClientVersion:  resp.MaxClientVersion,
	}
	logger.Logger.Debug("Simulator handshake succeeded",
		"build", result.SimulatorBuild,
		"protocol", result.ProtocolVersion,
		"features", result.SupportedFeatures,
	)
	return result, nil
}

// HandshakeDiagnostics returns a JSON-serialisable map of the negotiated
// capabilities for inclusion in diagnostic and replay fingerprint output.
func HandshakeDiagnostics(r *HandshakeResult) map[string]interface{} {
	if r == nil {
		return map[string]interface{}{"handshake": "not_performed"}
	}
	m := map[string]interface{}{
		"simulator_build":     r.SimulatorBuild,
		"protocol_version":    r.ProtocolVersion,
		"supported_features":  r.SupportedFeatures,
		"max_request_bytes":   r.MaxRequestBytes,
	}
	if r.MinClientVersion != "" {
		m["min_client_version"] = r.MinClientVersion
	}
	if r.MaxClientVersion != "" {
		m["max_client_version"] = r.MaxClientVersion
	}
	return m
}

// HandshakeDiagnosticsJSON returns HandshakeDiagnostics serialised to compact
// JSON. Returns "{}" on marshal error.
func HandshakeDiagnosticsJSON(r *HandshakeResult) string {
	b, err := json.Marshal(HandshakeDiagnostics(r))
	if err != nil {
		return "{}"
	}
	return string(b)
}

const (
	handshakeTimeout              = 10 * time.Second
	defaultMaxHandshakeRequestBytes int64 = 10 * 1024 * 1024
)
