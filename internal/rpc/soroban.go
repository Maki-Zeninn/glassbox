// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/dotandev/glassbox/internal/endpoints"
	"github.com/dotandev/glassbox/internal/errors"
	"github.com/dotandev/glassbox/internal/logger"
	"github.com/dotandev/glassbox/internal/metrics"
	"github.com/dotandev/glassbox/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
)

// selectSorobanURL picks the best Soroban RPC endpoint using the adaptive selector.
// Falls back to c.SorobanURL when no selector or alt URLs are configured.
func (c *Client) selectSorobanURL() string {
	if c.selector != nil && len(c.SorobanAltURLs) > 1 {
		return c.selector.Select(c.SorobanAltURLs)
	}
	if c.SorobanURL != "" {
		return c.SorobanURL
	}
	switch c.Network {
	case Testnet:
		return TestnetSorobanURL
	case Mainnet:
		return MainnetSorobanURL
	case Futurenet:
		return FuturenetSorobanURL
	}
	return c.SorobanURL
}

// IsPinned reports whether the client's provider pool is locked to a single
// endpoint (replay-pin mode).
func (c *Client) IsPinned() bool {
	if c.providerPool == nil {
		return false
	}
	return c.providerPool.IsPinned()
}

// PinnedProviderURL returns the URL of the pinned endpoint when the client is
// in replay-pin mode, or an empty string otherwise.
func (c *Client) PinnedProviderURL() string {
	if c.providerPool == nil {
		return ""
	}
	return c.providerPool.PinnedURL()
}

// rotateSorobanURL switches to the next Soroban endpoint after a failure.
// It records the failure on the selector and returns the next candidate URL.
func (c *Client) rotateSorobanURL(failedURL string) string {
	if c.selector != nil {
		c.selector.RecordFailure(failedURL)
	}
	c.markFailure(failedURL)

	if len(c.SorobanAltURLs) <= 1 {
		return failedURL
	}

	// Pick the next best URL excluding the failed one.
	candidates := make([]string, 0, len(c.SorobanAltURLs)-1)
	for _, u := range c.SorobanAltURLs {
		if u != failedURL {
			candidates = append(candidates, u)
		}
	}
	if len(candidates) == 0 {
		return failedURL
	}
	if c.selector != nil {
		return c.selector.Select(candidates)
	}
	return candidates[0]
}

// sorobanEndpointAttempts returns how many Soroban endpoint attempts to make.
func (c *Client) sorobanEndpointAttempts() int {
	if len(c.SorobanAltURLs) > 1 {
		return len(c.SorobanAltURLs)
	}
	return c.endpointAttempts()
}

type GetLatestLedgerResponse struct {
	Jsonrpc string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Result  struct {
		ID          string `json:"id"`
		Sequence    int    `json:"sequence"`
		CloseTime   string `json:"closeTime"`
		HeaderXdr   string `json:"headerXdr"`
		MetadataXdr string `json:"metadataXdr"`
	} `json:"result"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type GetHealthRequest struct {
	Jsonrpc string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
}

type GetHealthResponse struct {
	Jsonrpc string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Result  struct {
		Status                string `json:"status"`
		LatestLedger          uint32 `json:"latestLedger"`
		OldestLedger          uint32 `json:"oldestLedger"`
		LedgerRetentionWindow uint32 `json:"ledgerRetentionWindow"`
	} `json:"result"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type GetLedgerEntriesRequest struct {
	Jsonrpc string        `json:"jsonrpc"`
	ID      int           `json:"id"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
}

type GetLedgerEntriesResponse struct {
	Jsonrpc string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Result  struct {
		Entries      []LedgerEntryResult `json:"entries"`
		LatestLedger int                 `json:"latestLedger"`
	} `json:"result"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// GetLedgerEntries fetches the current state of ledger entries from Soroban RPC
// keys should be a list of base64-encoded XDR LedgerKeys
// This method implements batching and concurrent requests for large key sets
func (c *Client) GetLedgerEntries(ctx context.Context, keys []string) (map[string]string, error) {
	if len(keys) == 0 {
		return map[string]string{}, nil
	}

	entries := make(map[string]string)
	var keysToFetch []string

	// Check cache if enabled. Keys are scoped by network so testnet and
	// mainnet entries never collide even if the raw XDR key bytes are equal.
	if c.CacheEnabled {
		for _, key := range keys {
			val, hit, err := GetLedgerEntry(c.GetNetworkName(), key)
			if err != nil {
				logger.Logger.Warn("Cache read failed", "error", err)
			}
			if hit {
				entries[key] = val
				logger.Logger.Debug("Cache hit", "key", key, "network", c.GetNetworkName())
			} else {
				keysToFetch = append(keysToFetch, key)
			}
		}
	} else {
		keysToFetch = keys
	}

	// If all keys found in cache, return immediately
	if len(keysToFetch) == 0 {
		logger.Logger.Info("All ledger entries found in cache", "count", len(keys))
		return entries, nil
	}

	if len(c.AltURLs) == 0 {
		return nil, &AllNodesFailedError{}
	}

	logger.Logger.Debug("Fetching ledger entries from RPC", "count", len(keysToFetch), "url", c.SorobanURL)

	// Batch keys into chunks for concurrent processing
	const batchSize = 50
	batches := chunkKeys(keysToFetch, batchSize)

	// Use concurrent requests for large batches
	if len(batches) > 1 {
		fetchedEntries, err := c.getLedgerEntriesConcurrent(ctx, batches)
		if err != nil {
			return nil, err
		}
		// Merge cached entries with fetched entries
		for k, v := range fetchedEntries {
			entries[k] = v
		}
		return entries, nil
	}

	// Single batch - use adaptive failover logic
	attempts := c.sorobanEndpointAttempts()
	activeURL := c.selectSorobanURL()
	for attempt := 0; attempt < attempts; attempt++ {

		fetchedEntries, err := c.getLedgerEntriesAttemptURL(ctx, keysToFetch, activeURL)
		if err == nil {
			if c.selector != nil {
				c.selector.RecordSuccess(activeURL)
			}
			c.markSuccess(activeURL)
			// Merge cached entries with fetched entries
			for k, v := range fetchedEntries {
				entries[k] = v
			}
			return entries, nil
		}

		logger.Logger.Warn("getLedgerEntries failed, rotating Soroban endpoint",
			"failed_url", activeURL, "attempt", attempt+1, "error", err)
		activeURL = c.rotateSorobanURL(activeURL)

		if attempt < attempts-1 {
			continue
		}
	}
	return nil, &AllNodesFailedError{Failures: []NodeFailure{}}
}

// getLedgerEntriesConcurrent fetches multiple batches concurrently with timeout handling
func (c *Client) getLedgerEntriesConcurrent(ctx context.Context, batches [][]string) (map[string]string, error) {
	tracer := telemetry.GetTracer()
	_, span := tracer.Start(ctx, "rpc_get_ledger_entries_concurrent")
	span.SetAttributes(
		attribute.Int("batch.count", len(batches)),
		telemetry.Attr("network", string(c.Network)),
	)
	defer span.End()

	type batchResult struct {
		entries map[string]string
		err     error
	}

	results := make(chan batchResult, len(batches))
	var wg sync.WaitGroup

	// Create a context with timeout for all concurrent requests
	batchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	logger.Logger.Debug("Fetching ledger entries concurrently",
		"batch_count", len(batches),
		"total_keys", sumBatchSizes(batches))

	for _, batch := range batches {
		wg.Add(1)
		go func(keys []string) {
			defer wg.Done()

			// Attempt with failover for each batch
			var entries map[string]string
			var err error
			for attempt := 0; attempt < len(c.AltURLs); attempt++ {
				entries, err = c.getLedgerEntriesAttempt(batchCtx, keys)
				if err == nil {
					break
				}
				if attempt < len(c.AltURLs)-1 {
					logger.Logger.Warn("Batch request failed, trying next URL", "error", err)
					c.rotateURL()
				}
			}

			results <- batchResult{entries: entries, err: err}
		}(batch)
	}

	// Wait for all goroutines to complete
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results
	allEntries := make(map[string]string)
	var errs []error

	for result := range results {
		if result.err != nil {
			errs = append(errs, result.err)
			span.RecordError(result.err)
		} else {
			for k, v := range result.entries {
				allEntries[k] = v
			}
		}
	}

	// If any batch failed, return error
	if len(errs) > 0 {
		return nil, fmt.Errorf("failed to fetch %d/%d batches: %v", len(errs), len(batches), errs[0])
	}

	logger.Logger.Debug("Concurrent ledger entry fetch completed",
		"total_entries", len(allEntries),
		"batches", len(batches))

	return allEntries, nil
}

// sumBatchSizes calculates total number of keys across all batches
func sumBatchSizes(batches [][]string) int {
	total := 0
	for _, batch := range batches {
		total += len(batch)
	}
	return total
}

func (c *Client) getLedgerEntriesAttempt(ctx context.Context, keysToFetch []string) (entries map[string]string, err error) {
	// Always use the dedicated Soroban RPC URL for getLedgerEntries; this is a
	// Soroban JSON-RPC method and is not served by the Horizon REST API.
	targetURL := c.SorobanURL
	if targetURL == "" {
		switch c.Network {
		case Testnet:
			targetURL = TestnetSorobanURL
		case Mainnet:
			targetURL = MainnetSorobanURL
		case Futurenet:
			targetURL = FuturenetSorobanURL
		}
	}
	return c.getLedgerEntriesAttemptURL(ctx, keysToFetch, targetURL)
}

// getLedgerEntriesAttemptURL performs a single getLedgerEntries call against the
// explicitly provided targetURL. This allows the adaptive failover loop to pass
// the selector-chosen URL without mutating c.SorobanURL.
func (c *Client) getLedgerEntriesAttemptURL(ctx context.Context, keysToFetch []string, targetURL string) (entries map[string]string, err error) {
	timer := c.startMethodTimer(ctx, "rpc.get_ledger_entries", map[string]string{
		"network": c.GetNetworkName(),
		"rpc_url": targetURL,
	})
	defer func() {
		timer.Stop(err)
	}()

	logger.Logger.Debug("Fetching ledger entries", "count", len(keysToFetch), "url", targetURL)

	startTime := time.Now()
	// Fail fast if circuit breaker is open for this Soroban endpoint.
	if !c.isHealthy(targetURL) {
		err := errors.WrapRPCConnectionFailed(
			fmt.Errorf("circuit breaker open for %s", targetURL),
		)
		// Record failed remote node response
		metrics.RecordRemoteNodeResponse(targetURL, string(c.Network), false, time.Since(startTime))
		c.recordTelemetry(targetURL, time.Since(startTime), false)

		return nil, err
	}

	reqBody := GetLedgerEntriesRequest{
		Jsonrpc: "2.0",
		ID:      1,
		Method:  "getLedgerEntries",
		Params:  []interface{}{keysToFetch},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, errors.WrapMarshalFailed(err)
	}

	// Validate payload size before attempting to send to network
	if err := ValidatePayloadSize(int64(len(bodyBytes))); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", targetURL, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return nil, errors.WrapRPCConnectionFailed(err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.getHTTPClient().Do(req)
	duration := time.Since(startTime)
	if err != nil {
		logger.Logger.Error("Soroban getLedgerEntries request failed", "url", targetURL, "error", err)
		// Record failed remote node response
		metrics.RecordRemoteNodeResponse(targetURL, string(c.Network), false, duration)
		c.recordTelemetry(targetURL, duration, false)
		return nil, errors.WrapRPCConnectionFailed(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusRequestEntityTooLarge {
		// Record failed remote node response
		metrics.RecordRemoteNodeResponse(targetURL, string(c.Network), false, duration)
		c.recordTelemetry(targetURL, duration, false)
		return nil, errors.WrapRPCResponseTooLarge(targetURL)
	}

	respBytes, err := c.readResponseBody(resp.Body, "getLedgerEntries")
	if err != nil {
		// Record failed remote node response
		metrics.RecordRemoteNodeResponse(targetURL, string(c.Network), false, duration)
		c.recordTelemetry(targetURL, duration, false)
		return nil, errors.WrapUnmarshalFailed(err, "body read error")
	}

	var rpcResp GetLedgerEntriesResponse
	if err := json.Unmarshal(respBytes, &rpcResp); err != nil {
		logger.Logger.Error("Soroban getLedgerEntries response unmarshal failed", "url", targetURL, "error", err)
		// Record failed remote node response
		metrics.RecordRemoteNodeResponse(targetURL, string(c.Network), false, duration)
		c.recordTelemetry(targetURL, duration, false)
		return nil, errors.WrapUnmarshalFailed(err, string(respBytes))
	}

	if err := ValidateGetLedgerEntriesResponse(targetURL, &rpcResp); err != nil {
		logger.Logger.Error("Soroban getLedgerEntries response validation failed", "url", targetURL, "error", err)
		metrics.RecordRemoteNodeResponse(targetURL, string(c.Network), false, duration)
		c.recordTelemetry(targetURL, duration, false)
		return nil, err
	}

	if rpcResp.Error != nil {
		logger.Logger.Error("Soroban getLedgerEntries RPC error", "url", targetURL, "code", rpcResp.Error.Code, "message", rpcResp.Error.Message)
		// Record failed remote node response
		metrics.RecordRemoteNodeResponse(targetURL, string(c.Network), false, duration)
		c.recordTelemetry(targetURL, duration, false)
		return nil, errors.WrapSorobanError(targetURL, rpcResp.Error.Message, rpcResp.Error.Code)
	}

	// Record successful remote node response
	metrics.RecordRemoteNodeResponse(targetURL, string(c.Network), true, duration)
	c.recordTelemetry(targetURL, duration, true)

	entries = make(map[string]string)
	fetchedCount := 0
	for _, entry := range rpcResp.Result.Entries {
		entries[entry.Key] = entry.Xdr
		fetchedCount++

		// Cache the new entry, scoped by network so the same XDR key on
		// testnet and mainnet is stored as a distinct row.
		if c.CacheEnabled {
			if err := SetLedgerEntry(c.GetNetworkName(), entry.Key, entry.Xdr); err != nil {
				logger.Logger.Warn("Failed to cache entry", "key", entry.Key, "error", err)
			}
		}
	}

	// Cryptographically verify all returned ledger entries
	if err := VerifyLedgerEntries(keysToFetch, rpcResp.Result.Entries); err != nil {
		return nil, fmt.Errorf("ledger entry verification failed: %w", err)
	}

	logger.Logger.Debug("Ledger entries fetched",
		"total_requested", len(keysToFetch),
		"from_cache", len(keysToFetch)-fetchedCount,
		"from_rpc", fetchedCount,
		"url", targetURL,
	)

	return entries, nil
}

type SimulateTransactionRequest struct {
	Jsonrpc string        `json:"jsonrpc"`
	ID      int           `json:"id"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
}

type SimulateTransactionResponse struct {
	Jsonrpc string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Result  struct {
		// Soroban RPC returns these in various versions. Keep fields optional.
		// We only need minimal pieces for fee/budget estimation.
		MinResourceFee  string `json:"minResourceFee,omitempty"`
		TransactionData string `json:"transactionData,omitempty"`
		Cost            struct {
			CpuInsns  int64 `json:"cpuInsns,omitempty"`
			MemBytes  int64 `json:"memBytes,omitempty"`
			CpuInsns_ int64 `json:"cpu_insns,omitempty"`
			MemBytes_ int64 `json:"mem_bytes,omitempty"`
		} `json:"cost,omitempty"`
	} `json:"result"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// SimulateTransaction calls Soroban RPC simulateTransaction using a base64 TransactionEnvelope XDR.
func (c *Client) SimulateTransaction(ctx context.Context, envelopeXdr string) (*SimulateTransactionResponse, error) {
	// Use the provider pool when available — it provides ordered failover,
	// health tracking, per-attempt deadlines, and replay pinning.
	if c.providerPool != nil {
		var result *SimulateTransactionResponse
		diag, err := c.providerPool.Do(ctx, func(attemptCtx context.Context, url string) (int, error) {
			resp, attemptErr := c.simulateTransactionAttemptURL(attemptCtx, envelopeXdr, url)
			if attemptErr != nil {
				// Extract HTTP status if available from the error.
				return 0, attemptErr
			}
			result = resp
			return 0, nil
		})
		c.LastAttemptDiagnostics = diag
		if err == nil {
			if diag.SucceededURL != "" {
				if c.selector != nil {
					c.selector.RecordSuccess(diag.SucceededURL)
				}
				c.markSuccess(diag.SucceededURL)
			}
			return result, nil
		}
		logger.Logger.Warn("SimulateTransaction failed after all pool attempts",
			"succeeded_url", diag.SucceededURL,
			"attempts", len(diag.Attempts),
		)
		return nil, err
	}

	// Legacy path (no pool configured).
	attempts := c.sorobanEndpointAttempts()
	activeURL := c.selectSorobanURL()
	var failures []NodeFailure
	for attempt := 0; attempt < attempts; attempt++ {
		resp, err := c.simulateTransactionAttemptURL(ctx, envelopeXdr, activeURL)
		if err == nil {
			if c.selector != nil {
				c.selector.RecordSuccess(activeURL)
			}
			c.markSuccess(activeURL)
			return resp, nil
		}

		failures = append(failures, NodeFailure{URL: activeURL, Reason: err})
		logger.Logger.Warn("simulateTransaction failed, rotating Soroban endpoint",
			"failed_url", activeURL, "attempt", attempt+1, "error", err)
		activeURL = c.rotateSorobanURL(activeURL)

		if attempt < attempts-1 {
			continue
		}
	}
	return nil, &AllNodesFailedError{Failures: failures}
}

func (c *Client) simulateTransactionAttempt(ctx context.Context, envelopeXdr string) (simResp *SimulateTransactionResponse, err error) {
	// Always use the dedicated Soroban RPC URL for simulateTransaction; this is a
	// Soroban JSON-RPC method and is not served by the Horizon REST API.
	targetURL := c.SorobanURL
	if targetURL == "" {
		switch c.Network {
		case Testnet:
			targetURL = TestnetSorobanURL
		case Mainnet:
			targetURL = MainnetSorobanURL
		case Futurenet:
			targetURL = FuturenetSorobanURL
		}
	}
	return c.simulateTransactionAttemptURL(ctx, envelopeXdr, targetURL)
}

// simulateTransactionAttemptURL performs a single simulateTransaction call against
// the explicitly provided targetURL.
func (c *Client) simulateTransactionAttemptURL(ctx context.Context, envelopeXdr string, targetURL string) (simResp *SimulateTransactionResponse, err error) {
	timer := c.startMethodTimer(ctx, "rpc.simulate_transaction", map[string]string{
		"network": c.GetNetworkName(),
		"rpc_url": targetURL,
	})
	defer func() {
		timer.Stop(err)
	}()

	logger.Logger.Debug("Simulating transaction (preflight)", "url", targetURL)
	startTime := time.Now()

	// Fail fast if circuit breaker is open for this Soroban endpoint.
	if !c.isHealthy(targetURL) {
		c.recordTelemetry(targetURL, time.Since(startTime), false)
		return nil, errors.WrapRPCConnectionFailed(
			fmt.Errorf("circuit breaker open for %s", targetURL),
		)
	}

	reqBody := SimulateTransactionRequest{
		Jsonrpc: "2.0",
		ID:      1,
		Method:  "simulateTransaction",
		Params:  []interface{}{envelopeXdr},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, errors.WrapMarshalFailed(err)
	}

	// Validate payload size before attempting to send to network
	if err := ValidatePayloadSize(int64(len(bodyBytes))); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", targetURL, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return nil, errors.WrapRPCConnectionFailed(err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.getHTTPClient().Do(req)
	duration := time.Since(startTime)
	if err != nil {
		logger.Logger.Error("Soroban simulateTransaction request failed", "url", targetURL, "error", err)
		c.recordTelemetry(targetURL, duration, false)
		return nil, errors.WrapRPCConnectionFailed(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusRequestEntityTooLarge {
		logger.Logger.Error("Soroban simulateTransaction response too large", "url", targetURL, "status", resp.StatusCode)
		c.recordTelemetry(targetURL, duration, false)
		return nil, errors.WrapRPCResponseTooLarge(targetURL)
	}

	respBytes, err := c.readResponseBody(resp.Body, "simulateTransaction")
	if err != nil {
		logger.Logger.Error("Soroban simulateTransaction response read failed", "url", targetURL, "error", err)
		c.recordTelemetry(targetURL, duration, false)
		return nil, errors.WrapUnmarshalFailed(err, "body read error")
	}

	var rpcResp SimulateTransactionResponse
	if err := json.Unmarshal(respBytes, &rpcResp); err != nil {
		logger.Logger.Error("Soroban simulateTransaction unmarshal failed", "url", targetURL, "error", err)
		c.recordTelemetry(targetURL, duration, false)
		return nil, errors.WrapUnmarshalFailed(err, string(respBytes))
	}

	if err := ValidateSimulateTransactionResponse(targetURL, &rpcResp); err != nil {
		logger.Logger.Error("Soroban simulateTransaction response validation failed", "url", targetURL, "error", err)
		c.recordTelemetry(targetURL, duration, false)
		return nil, err
	}

	if rpcResp.Error != nil {
		logger.Logger.Error("Soroban simulateTransaction RPC error", "url", targetURL, "code", rpcResp.Error.Code, "message", rpcResp.Error.Message)
		c.recordTelemetry(targetURL, duration, false)
		return nil, errors.WrapSorobanError(targetURL, rpcResp.Error.Message, rpcResp.Error.Code)
	}

	c.recordTelemetry(targetURL, duration, true)
	logger.Logger.Debug("Soroban simulateTransaction succeeded", "url", targetURL)
	return &rpcResp, nil
}

// GetHealth checks the health of the Soroban RPC endpoint.
func (c *Client) GetHealth(ctx context.Context) (*GetHealthResponse, error) {
	// Use the provider pool when available.
	if c.providerPool != nil {
		var result *GetHealthResponse
		diag, err := c.providerPool.Do(ctx, func(attemptCtx context.Context, url string) (int, error) {
			resp, attemptErr := c.getHealthAttemptURL(attemptCtx, url)
			if attemptErr != nil {
				return 0, attemptErr
			}
			result = resp
			return 0, nil
		})
		c.LastAttemptDiagnostics = diag
		if err == nil {
			if diag.SucceededURL != "" {
				if c.selector != nil {
					c.selector.RecordSuccess(diag.SucceededURL)
				}
				c.markSuccess(diag.SucceededURL)
			}
			return result, nil
		}
		return nil, err
	}

	// Legacy path.
	attempts := c.sorobanEndpointAttempts()
	activeURL := c.selectSorobanURL()
	var failures []NodeFailure
	for attempt := 0; attempt < attempts; attempt++ {
		resp, err := c.getHealthAttemptURL(ctx, activeURL)
		if err == nil {
			if c.selector != nil {
				c.selector.RecordSuccess(activeURL)
			}
			c.markSuccess(activeURL)
			return resp, nil
		}

		failures = append(failures, NodeFailure{URL: activeURL, Reason: err})
		logger.Logger.Warn("getHealth failed, rotating Soroban endpoint",
			"failed_url", activeURL, "attempt", attempt+1, "error", err)
		activeURL = c.rotateSorobanURL(activeURL)

		if attempt < attempts-1 {
			continue
		}
	}
	return nil, &AllNodesFailedError{Failures: failures}
}

func (c *Client) getHealthAttempt(ctx context.Context) (healthResp *GetHealthResponse, err error) {
	targetURL := c.SorobanURL
	if targetURL == "" {
		targetURL = c.HorizonURL
	}
	return c.getHealthAttemptURL(ctx, targetURL)
}

// getHealthAttemptURL performs a single getHealth call against the explicitly provided targetURL.
func (c *Client) getHealthAttemptURL(ctx context.Context, targetURL string) (healthResp *GetHealthResponse, err error) {
	timer := c.startMethodTimer(ctx, "rpc.get_health", map[string]string{
		"network": c.GetNetworkName(),
		"rpc_url": targetURL,
	})
	defer func() {
		timer.Stop(err)
	}()

	startTime := time.Now()
	logger.Logger.Debug("Checking Soroban RPC health", "url", targetURL)

	// Fail fast if circuit breaker is open for this Soroban endpoint.
	if !c.isHealthy(targetURL) {
		c.recordTelemetry(targetURL, time.Since(startTime), false)
		return nil, errors.NewRPCError(errors.CodeRPCConnectionFailed,
			fmt.Errorf("circuit breaker open for %s", targetURL),
		)
	}

	reqBody := GetHealthRequest{
		Jsonrpc: "2.0",
		ID:      1,
		Method:  "getHealth",
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, errors.NewRPCError(errors.CodeRPCMarshalFailed, err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", targetURL, bytes.NewBuffer(bodyBytes))
	if err != nil {
		c.recordTelemetry(targetURL, time.Since(startTime), false)
		return nil, errors.NewRPCError(errors.CodeRPCConnectionFailed, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.getHTTPClient().Do(req)
	duration := time.Since(startTime)
	if err != nil {
		logger.Logger.Error("Soroban getHealth request failed", "url", targetURL, "error", err)
		c.recordTelemetry(targetURL, duration, false)
		return nil, errors.NewRPCError(errors.CodeRPCConnectionFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBytes, err := c.readResponseBody(resp.Body, "getHealth")
	if err != nil {
		logger.Logger.Error("Soroban getHealth response read failed", "url", targetURL, "error", err)
		c.recordTelemetry(targetURL, duration, false)
		return nil, errors.NewRPCError(errors.CodeRPCUnmarshalFailed, err)
	}

	var rpcResp GetHealthResponse
	if err := json.Unmarshal(respBytes, &rpcResp); err != nil {
		logger.Logger.Error("Soroban getHealth unmarshal failed", "url", targetURL, "error", err)
		c.recordTelemetry(targetURL, duration, false)
		return nil, errors.NewRPCError(errors.CodeRPCUnmarshalFailed, err)
	}

	if err := ValidateGetHealthResponse(targetURL, &rpcResp); err != nil {
		logger.Logger.Error("Soroban getHealth response validation failed", "url", targetURL, "error", err)
		c.recordTelemetry(targetURL, duration, false)
		return nil, err
	}

	if rpcResp.Error != nil {
		logger.Logger.Error("Soroban getHealth RPC error", "url", targetURL, "code", rpcResp.Error.Code, "message", rpcResp.Error.Message)
		c.recordTelemetry(targetURL, duration, false)
		return nil, errors.NewRPCError(errors.CodeRPCError, fmt.Errorf("rpc error from %s: %s (code %d)", targetURL, rpcResp.Error.Message, rpcResp.Error.Code))
	}

	c.recordTelemetry(targetURL, duration, true)
	logger.Logger.Debug("Soroban RPC health check successful", "url", targetURL, "status", rpcResp.Result.Status)
	return &rpcResp, nil
}

// GetLatestLedgerSequence fetches the latest ledger from the node this client is configured for.
func (c *Client) GetLatestLedgerSequence(ctx context.Context) (int, error) {
	payload := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "getLatestLedger",
	}

	var resp GetLatestLedgerResponse
	err := c.postRequest(ctx, payload, &resp)
	if err != nil {
		return 0, err
	}

	if err := ValidateGetLatestLedgerResponse(c.selectSorobanURL(), &resp); err != nil {
		return 0, err
	}

	return resp.Result.Sequence, nil
}

func fetchLatestFromSDF(ctx context.Context, url string) (int, error) {
	// 1. Prepare the JSON-RPC payload
	payload := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "getLatestLedger",
	}
	body, _ := json.Marshal(payload)

	// 2. Create the request with a strict timeout
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	// 3. Decode using the struct you found earlier
	var rpcResp GetLatestLedgerResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return 0, err
	}

	if rpcResp.Error != nil {
		return 0, fmt.Errorf("RPC error: %s", rpcResp.Error.Message)
	}

	return rpcResp.Result.Sequence, nil
}

func (c *Client) CheckStaleness(ctx context.Context, network string) error {
	// 1. Get the ledger sequence from the user's configured RPC (the local node)
	localLedger, err := c.GetLatestLedgerSequence(ctx)
	if err != nil {
		return fmt.Errorf("failed to get local ledger: %w", err)
	}

	// 2. Determine the official reference URL based on the network
	var referenceURL string
	switch strings.ToLower(network) {
	case "testnet":
		referenceURL = endpoints.SorobanTestnet
	case "mainnet", "public":
		referenceURL = endpoints.SorobanMainnet
	default:
		// Skip check for 'standalone' or unknown networks
		return nil
	}

	// 3. Fetch the latest ledger from the official SDF reference node
	refLedger, err := fetchLatestFromSDF(ctx, referenceURL)
	if err != nil {
		// We don't want to block the tool if the internet is down,
		// just log it and move on.
		return nil
	}

	// 4. Compare
	const threshold = 15 // ~1.5 minutes of lag
	if refLedger > localLedger+threshold {
		fmt.Printf("\033[33m[WARN]\033[0m Local node is lagging! (Local: %d, Network: %d). \n", localLedger, refLedger)
		fmt.Println("       Traces and replays might use outdated contract state.")
	}

	return nil
}
