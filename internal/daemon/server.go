// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/dotandev/glassbox/internal/errors"
	"github.com/dotandev/glassbox/internal/health"
	"github.com/dotandev/glassbox/internal/logger"
	stellarrpc "github.com/dotandev/glassbox/internal/rpc"
	"github.com/dotandev/glassbox/internal/simulator"
	"github.com/dotandev/glassbox/internal/telemetry"
	"github.com/gorilla/rpc/v2"
	"github.com/gorilla/rpc/v2/json2"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Server represents the JSON-RPC daemon server
type Server struct {
	rpcClient     *stellarrpc.Client
	simulator     *simulator.Runner
	authToken     string
	healthHandler *health.Handler
	auth          *AuthPolicy
}

// Config holds daemon configuration
type Config struct {
	Port      string
	Network   string
	RPCURL    string
	AuthToken string
	Auth      AuthConfig
}

// DebugTransactionRequest represents the debug_transaction RPC request
type DebugTransactionRequest struct {
	Hash string `json:"hash"`
}

// DebugTransactionResponse represents the debug_transaction RPC response
type DebugTransactionResponse struct {
	Hash         string `json:"hash"`
	Network      string `json:"network"`
	EnvelopeSize int    `json:"envelope_size"`
	Status       string `json:"status"`
}

// GetTraceRequest represents the get_trace RPC request
type GetTraceRequest struct {
	Hash string `json:"hash"`
}

// GetTraceResponse represents the get_trace RPC response
type GetTraceResponse struct {
	Hash   string                   `json:"hash"`
	Traces []map[string]interface{} `json:"traces"`
}

// GetContractCodeRequest represents the get_contract_code RPC request
type GetContractCodeRequest struct {
	ContractID string `json:"contract_id"`
	TxHash     string `json:"tx_hash"`
}

// GetContractCodeResponse represents the get_contract_code RPC response
type GetContractCodeResponse struct {
	ContractID string `json:"contract_id"`
	WasmHash   string `json:"wasm_hash"`
	Wasm       string `json:"wasm"`
}

// NewServer creates a new JSON-RPC server
func NewServer(config Config) (*Server, error) {
	opts := []stellarrpc.ClientOption{
		stellarrpc.WithNetwork(stellarrpc.Network(config.Network)),
	}

	if config.RPCURL != "" {
		opts = append(opts, stellarrpc.WithHorizonURL(config.RPCURL))
	}

	client, err := stellarrpc.NewClient(opts...)
	if err != nil {
		return nil, errors.WrapValidationError(fmt.Sprintf("failed to create RPC client: %v", err))
	}

	sim, err := simulator.NewRunner("", false)
	if err != nil {
		return nil, errors.WrapSimulatorNotFound(err.Error())
	}

	h := &Server{
		rpcClient: client,
		simulator: sim,
		authToken: config.AuthToken,
		auth:      NewAuthPolicy(config.Auth),
	}

	// Build health handler and register lightweight checks.
	// These checks are read-only and never trigger expensive replay work.
	hh := health.NewHandler()

	// Simulator availability check: verify the runner binary can be located.
	hh.Register(health.NewChecker("simulator", func(_ context.Context) error {
		_, checkErr := simulator.NewRunner("", false)
		return checkErr
	}))

	// RPC connectivity check: verify the configured RPC endpoint is reachable.
	hh.Register(health.NewChecker("rpc", func(ctx context.Context) error {
		_, checkErr := client.GetHealth(ctx)
		return checkErr
	}))

	h.healthHandler = hh
	return h, nil
}

// authenticate validates the authorization token
func (s *Server) authenticate(r *http.Request) bool {
	if s.authToken == "" {
		return true // No auth required
	}

	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return false
	}

	token := strings.TrimPrefix(auth, "Bearer ")
	return s.auth.IsAuthenticated(token)
}

// authorizeOperation checks whether the request is authorized for the given operation.
// Returns an error if access is denied.
func (s *Server) authorizeOperation(r *http.Request, op Operation) error {
	if s.auth == nil {
		return nil
	}

	token := ""
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		token = strings.TrimPrefix(auth, "Bearer ")
	}

	remoteAddr := r.RemoteAddr
	if remoteAddr == "" {
		remoteAddr = "unknown"
	}

	if allowed, reason := s.auth.CheckAccess(token, op, remoteAddr); !allowed {
		logger.Logger.Warn("Authorization denied",
			"operation", op,
			"remote_addr", remoteAddr,
			"reason", reason,
		)
		return errors.WrapUnauthorized(reason)
	}

	return nil
}

// DebugTransaction handles debug_transaction RPC calls
func (s *Server) DebugTransaction(r *http.Request, req *DebugTransactionRequest, resp *DebugTransactionResponse) error {
	if err := s.authorizeOperation(r, OpDebugTransaction); err != nil {
		return err
	}

	ctx := r.Context()
	tracer := telemetry.GetTracer()
	ctx, span := tracer.Start(ctx, "rpc_debug_transaction")
	span.SetAttributes(telemetry.Attr("transaction.hash", req.Hash))
	defer span.End()

	logger.Logger.Info("Processing debug_transaction RPC", "hash", req.Hash)

	// Fetch transaction details
	txResp, err := s.rpcClient.GetTransaction(ctx, req.Hash)
	if err != nil {
		span.RecordError(err)
		return errors.WrapRPCConnectionFailed(err)
	}

	*resp = DebugTransactionResponse{
		Hash:         req.Hash,
		Network:      string(s.rpcClient.Network),
		EnvelopeSize: len(txResp.EnvelopeXdr),
		Status:       "success",
	}

	return nil
}

// GetTrace handles get_trace RPC calls
func (s *Server) GetTrace(r *http.Request, req *GetTraceRequest, resp *GetTraceResponse) error {
	if err := s.authorizeOperation(r, OpGetTrace); err != nil {
		return err
	}

	ctx := r.Context()
	tracer := telemetry.GetTracer()
	_, span := tracer.Start(ctx, "rpc_get_trace")
	span.SetAttributes(telemetry.Attr("transaction.hash", req.Hash))
	defer span.End()

	logger.Logger.Info("Processing get_trace RPC", "hash", req.Hash)

	// For now, return mock trace data
	// In a full implementation, this would integrate with actual tracing
	*resp = GetTraceResponse{
		Hash: req.Hash,
		Traces: []map[string]interface{}{
			{
				"span_id":   "debug_transaction",
				"operation": "fetch_transaction",
				"duration":  "150ms",
				"status":    "success",
			},
		},
	}

	return nil
}

// GetContractCode handles get_contract_code RPC calls to fetch historical WASM bytecode
func (s *Server) GetContractCode(r *http.Request, req *GetContractCodeRequest, resp *GetContractCodeResponse) error {
	if err := s.authorizeOperation(r, OpGetContractCode); err != nil {
		return err
	}

	ctx := r.Context()
	tracer := telemetry.GetTracer()
	ctx, span := tracer.Start(ctx, "rpc_get_contract_code")
	span.SetAttributes(
		telemetry.Attr("contract.id", req.ContractID),
		telemetry.Attr("transaction.hash", req.TxHash),
	)
	defer span.End()

	logger.Logger.Info("Processing get_contract_code RPC", "contract_id", req.ContractID, "tx_hash", req.TxHash)

	wasmBytes, wasmHash, err := stellarrpc.FetchHistoricalContractBytecode(ctx, s.rpcClient, req.ContractID, req.TxHash)
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("fetch historical bytecode: %w", err)
	}

	*resp = GetContractCodeResponse{
		ContractID: req.ContractID,
		WasmHash:   wasmHash,
		Wasm:       base64.StdEncoding.EncodeToString(wasmBytes),
	}

	return nil
}

// Start starts the JSON-RPC server
func (s *Server) Start(ctx context.Context, port string) error {
	rpcServer := rpc.NewServer()
	rpcServer.RegisterCodec(json2.NewCodec(), "application/json")
	rpcServer.RegisterCodec(json2.NewCodec(), "application/json;charset=UTF-8")

	if err := rpcServer.RegisterService(s, ""); err != nil {
		return errors.WrapValidationError(fmt.Sprintf("failed to register service: %v", err))
	}

	// Use a dedicated mux so we don't pollute the global http.DefaultServeMux.
	mux := http.NewServeMux()

	// Auth middleware: enforce bind scope and audit denied requests
	authMiddleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			remote := r.RemoteAddr
			if remote != "" && s.auth != nil && !s.auth.IsAllowedBindAddr(remote) {
				logger.Logger.Warn("Connection rejected by bind scope",
					"remote_addr", remote,
					"bind_scope", s.auth.config.BindScope,
				)
				http.Error(w, `{"error":"connection refused: not allowed by bind scope"}`, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}

	mux.Handle("/rpc", authMiddleware(rpcServer))
	mux.Handle("/metrics", authMiddleware(promhttp.Handler()))

	// Legacy /health endpoint for backwards compatibility.
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	// Structured health endpoints (liveness, readiness, aggregate status).
	checker := NewHealthChecker()
	checker.WithSimulatorProbe(DefaultSimulatorProbe(func() bool {
		return s.simulator != nil
	}))
	RegisterHealthRoutes(mux, checker)

	// Audit endpoint: returns recent denied authorization attempts (admin only)
	if s.auth != nil {
		mux.HandleFunc("/audit", authMiddleware(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			denials := s.auth.GetDenials()
			if denials == nil {
				denials = []AuditEntry{}
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"denials": denials,
				"total":   len(denials),
			})
		}))
	}

	logger.Logger.Info("Starting JSON-RPC server", "port", port)

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}

	// Start server in goroutine
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Logger.Error("Server failed", "error", err)
		}
	}()

	// Wait for context cancellation; signal readiness probe before shutdown begins.
	<-ctx.Done()
	checker.MarkShuttingDown()
	logger.Logger.Info("Shutting down JSON-RPC server")
	return srv.Shutdown(context.Background())
}
