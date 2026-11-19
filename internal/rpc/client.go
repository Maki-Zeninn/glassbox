// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package rpc

import (
	"context"
	stdErrors "errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/dotandev/glassbox/internal/errors"
	"github.com/dotandev/glassbox/internal/logger"
	"github.com/dotandev/glassbox/internal/metrics"

	"github.com/dotandev/glassbox/internal/telemetry"
	"github.com/stellar/go-stellar-sdk/clients/horizonclient"
	"go.opentelemetry.io/otel/attribute"
)

var Version = "dev"

// HTTPClient is an interface that matches horizonclient.HTTP.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
	Get(url string) (*http.Response, error)
	Post(url, contentType string, body io.Reader) (*http.Response, error)
	PostForm(url string, data url.Values) (*http.Response, error)
}

// Client handles interactions with the Stellar Network
type Client struct {
	Horizon          horizonclient.ClientInterface
	HorizonURL       string
	Network          Network
	SorobanURL       string
	AltURLs          []string
	// SorobanAltURLs holds the full list of Soroban RPC endpoints available for
	// failover. When non-empty, the adaptive selector uses this list instead of
	// AltURLs for Soroban-specific calls (getLedgerEntries, simulateTransaction,
	// getHealth, etc.).
	SorobanAltURLs   []string
	currIndex        int
	mu               sync.RWMutex
	httpClient       HTTPClient
	token            string // stored for reference, not logged
	Config           NetworkConfig
	CacheEnabled     bool
	methodTelemetry  MethodTelemetry
	failures         map[string]int
	lastFailure      map[string]time.Time
	FailureThreshold int
	RetryTimeout     int
	middlewares      []Middleware
	// rotateCount tracks how many times rotateURL has successfully switched
	// the active provider.  This is useful for metrics/observability when the
	// client is operating in a multi‑URL failover configuration.
	rotateCount      int
	healthCollector  *HealthCollector
	// selector drives adaptive endpoint selection for Soroban RPC calls.
	selector         *EndpointSelector
	// failoverPolicy is the active policy used by the selector.
	failoverPolicy   FailoverPolicy
	// providerPool is the ordered-failover pool used for all Soroban RPC calls.
	// It supersedes the legacy selector/circuit-breaker for new call paths.
	providerPool     *ProviderPool
	// LastAttemptDiagnostics holds the diagnostic trace from the most recent
	// pool operation. Safe for read after a call returns; overwritten on the
	// next call.
	LastAttemptDiagnostics AttemptDiagnostics
	// ResponsePayloadLimit caps the number of bytes read from any Soroban RPC
	// response body. 0 means use DefaultResponsePayloadLimit.
	ResponsePayloadLimit int64
	// AggregateFetchLimit caps the total bytes fetched across all RPC responses
	// in a session. 0 means use DefaultAggregateFetchLimit.
	AggregateFetchLimit int64
	// aggregateTracker tracks total bytes fetched across all requests.
	aggregateTracker *AggregateTracker
}

func (c *Client) startMethodTimer(ctx context.Context, method string, attributes map[string]string) MethodTimer {
	if c == nil || c.methodTelemetry == nil {
		return noopMethodTimer{}
	}
	return c.methodTelemetry.StartMethodTimer(ctx, method, attributes)
}

// GetTransaction fetches the transaction details and full XDR data.
// The result is stored in the content-addressed cache after the first
// successful fetch so that identical subsequent calls can be served
// offline without any RPC round-trip.
func (c *Client) GetTransaction(ctx context.Context, hash string) (*TransactionResponse, error) {
	// Cache check — transactions are immutable once on-chain.
	if c.CacheEnabled {
		if cached, ok, err := GetTransaction(c.GetNetworkName(), hash); ok {
			logger.Logger.Debug("Transaction served from cache", "hash", hash, "network", c.GetNetworkName())
			return cached, nil
		} else if err != nil {
			logger.Logger.Warn("Cache read error for transaction", "hash", hash, "error", err)
		}
	}

	attempts := c.endpointAttempts()
	var failures []NodeFailure
	for attempt := 0; attempt < attempts; attempt++ {
		resp, err := c.getTransactionAttempt(ctx, hash)
		if err == nil {
			c.markSuccess(c.HorizonURL)
			// Store in cache so the next identical request is served offline.
			if c.CacheEnabled {
				if cacheErr := SetTransaction(c.GetNetworkName(), hash, resp); cacheErr != nil {
					logger.Logger.Warn("Failed to cache transaction", "hash", hash, "error", cacheErr)
				}
			}
			return resp, nil
		}

		c.markFailure(c.HorizonURL)

		failures = append(failures, NodeFailure{URL: c.HorizonURL, Reason: err})

		// Only rotate if this isn't the last possible URL
		if attempt < attempts-1 && len(c.AltURLs) > 1 {
			logger.Logger.Warn("Retrying with fallback RPC...", "error", err)
			if !c.rotateURL() {
				break
			}
			continue
		}

		if len(c.AltURLs) <= 1 {
			return nil, err
		}
	}
	return nil, &AllNodesFailedError{Failures: failures}
}

// WatchTransaction streams transaction status updates until the caller
// cancels the context or a terminal status is reached.
func (c *Client) WatchTransaction(ctx context.Context, hash string) (<-chan TxStatus, error) {
	return NewTxStreamer(c).Stream(ctx, hash)
}

func (c *Client) getTransactionAttempt(ctx context.Context, hash string) (txResp *TransactionResponse, err error) {
	timer := c.startMethodTimer(ctx, "rpc.get_transaction", map[string]string{
		"network": c.GetNetworkName(),
		"rpc_url": c.HorizonURL,
	})
	defer func() {
		timer.Stop(err)
	}()

	tracer := telemetry.GetTracer()
	_, span := tracer.Start(ctx, "rpc_get_transaction")
	span.SetAttributes(
		telemetry.Attr("transaction.hash", hash),
		telemetry.Attr("network", string(c.Network)),
		telemetry.Attr("rpc.url", c.HorizonURL),
	)
	defer span.End()

	logger.Logger.Debug("Fetching transaction details", "hash", hash, "url", c.HorizonURL)

	startTime := time.Now()

	// Fail fast if circuit breaker is open for this Horizon endpoint.
	if !c.isHealthy(c.HorizonURL) {
		err := fmt.Errorf("circuit breaker open for %s", c.HorizonURL)
		span.RecordError(err)
		// Record failed remote node response
		metrics.RecordRemoteNodeResponse(c.HorizonURL, string(c.Network), false, time.Since(startTime))
		c.recordTelemetry(c.HorizonURL, time.Since(startTime), false)
		return nil, errors.WrapRPCConnectionFailed(err)
	}

	tx, err := c.Horizon.TransactionDetail(hash)
	duration := time.Since(startTime)

	if err != nil {
		span.RecordError(err)
		logger.Logger.Error("Failed to fetch transaction", "hash", hash, "error", err, "url", c.HorizonURL)
		// Record failed remote node response
		metrics.RecordRemoteNodeResponse(c.HorizonURL, string(c.Network), false, duration)
		c.recordTelemetry(c.HorizonURL, duration, false)

		// Check if it's a 404 (Transaction Not Found)
		if hErr, ok := err.(*horizonclient.Error); ok && hErr.Problem.Status == 404 {
			c.recordTelemetry(c.HorizonURL, duration, true)
			return nil, errors.WrapTransactionNotFound(err)
		}

		c.recordTelemetry(c.HorizonURL, duration, false)

		return nil, errors.WrapRPCConnectionFailed(err)
	}

	// Record successful remote node response
	metrics.RecordRemoteNodeResponse(c.HorizonURL, string(c.Network), true, duration)
	c.recordTelemetry(c.HorizonURL, duration, true)

	span.SetAttributes(
		attribute.Int("envelope.size_bytes", len(tx.EnvelopeXdr)),
		attribute.Int("result.size_bytes", len(tx.ResultXdr)),
		attribute.Int("result_meta.size_bytes", len(tx.ResultMetaXdr)),
	)

	logger.Logger.Debug("Transaction fetched", "hash", hash, "envelope_size", len(tx.EnvelopeXdr), "url", c.HorizonURL)

	parsed := ParseTransactionResponse(tx)
	if err := ValidateTransactionResponse(c.HorizonURL, hash, parsed); err != nil {
		span.RecordError(err)
		logger.Logger.Error("Transaction response validation failed", "hash", hash, "url", c.HorizonURL, "error", err)
		return nil, err
	}

	return parsed, nil
}

// GetNetworkPassphrase returns the network passphrase for this client
func (c *Client) GetNetworkPassphrase() string {
	return c.Config.NetworkPassphrase
}

// GetNetworkName returns the network name for this client
func (c *Client) GetNetworkName() string {
	if c.Config.Name != "" {
		return c.Config.Name
	}
	return "custom"
}

// GetLedgerHeader fetches ledger header details for a specific sequence with automatic fallback.
// Ledger headers are immutable after they close; a cached copy is served
// immediately without any RPC call.
func (c *Client) GetLedgerHeader(ctx context.Context, sequence uint32) (*LedgerHeaderResponse, error) {
	// Cache check — ledger headers are immutable once closed.
	if c.CacheEnabled {
		if cached, ok, err := GetLedgerHeader(c.GetNetworkName(), sequence); ok {
			logger.Logger.Debug("Ledger header served from cache", "sequence", sequence, "network", c.GetNetworkName())
			return cached, nil
		} else if err != nil {
			logger.Logger.Warn("Cache read error for ledger header", "sequence", sequence, "error", err)
		}
	}

	attempts := c.endpointAttempts()
	var failures []NodeFailure
	for attempt := 0; attempt < attempts; attempt++ {
		resp, err := c.getLedgerHeaderAttempt(ctx, sequence)
		if err == nil {
			c.markSuccess(c.HorizonURL)
			if c.CacheEnabled {
				if cacheErr := SetLedgerHeader(c.GetNetworkName(), sequence, resp); cacheErr != nil {
					logger.Logger.Warn("Failed to cache ledger header", "sequence", sequence, "error", cacheErr)
				}
			}
			return resp, nil
		}

		c.markFailure(c.HorizonURL)

		failures = append(failures, NodeFailure{URL: c.HorizonURL, Reason: err})

		if attempt < attempts-1 && len(c.AltURLs) > 1 {
			logger.Logger.Warn("Retrying ledger header fetch with fallback RPC...", "error", err)
			if !c.rotateURL() {
				break
			}
			continue
		}

		if len(c.AltURLs) <= 1 {
			return nil, err
		}
	}
	// Single-node path: return the typed error directly so callers can use Is/As.
	if len(failures) == 1 {
		return nil, failures[0].Reason
	}
	return nil, &AllNodesFailedError{Failures: failures}
}

func (c *Client) getLedgerHeaderAttempt(ctx context.Context, sequence uint32) (ledgerResp *LedgerHeaderResponse, err error) {
	timer := c.startMethodTimer(ctx, "rpc.get_ledger_header", map[string]string{
		"network": c.GetNetworkName(),
		"rpc_url": c.HorizonURL,
	})
	defer func() {
		timer.Stop(err)
	}()

	tracer := telemetry.GetTracer()
	_, span := tracer.Start(ctx, "rpc_get_ledger_header")
	span.SetAttributes(
		telemetry.Attr("network", string(c.Network)),
		attribute.Int("ledger.sequence", int(sequence)),
		telemetry.Attr("rpc.url", c.HorizonURL),
	)
	defer span.End()

	logger.Logger.Debug("Fetching ledger header", "sequence", sequence, "network", c.Network, "url", c.HorizonURL)

	// Fail fast if circuit breaker is open for this Horizon endpoint.
	if !c.isHealthy(c.HorizonURL) {
		err := fmt.Errorf("circuit breaker open for %s", c.HorizonURL)
		span.RecordError(err)
		return nil, errors.WrapRPCConnectionFailed(err)
	}

	// Fetch ledger from Horizon
	ledger, err := c.Horizon.LedgerDetail(sequence)
	if err != nil {
		span.RecordError(err)
		return nil, c.handleLedgerError(err, sequence)
	}

	response := FromHorizonLedger(ledger)

	span.SetAttributes(
		telemetry.Attr("ledger.hash", response.Hash),
		attribute.Int("ledger.protocol_version", int(response.ProtocolVersion)),
		attribute.Int("ledger.tx_count", int(response.SuccessfulTxCount+response.FailedTxCount)),
	)

	logger.Logger.Debug("Ledger header fetched successfully",
		"sequence", sequence,
		"hash", response.Hash,
		"url", c.HorizonURL,
	)

	return response, nil
}

// handleLedgerError provides detailed error messages for ledger fetch failures
func (c *Client) handleLedgerError(err error, sequence uint32) error {
	// Check if it's a Horizon error
	if hErr, ok := err.(*horizonclient.Error); ok {
		switch hErr.Problem.Status {
		case 404:
			logger.Logger.Warn("Ledger not found", "sequence", sequence, "status", 404)
			return errors.WrapLedgerNotFound(sequence)
		case 410:
			logger.Logger.Warn("Ledger archived", "sequence", sequence, "status", 410)
			return errors.WrapLedgerArchived(sequence)
		case 413:
			logger.Logger.Warn("Response too large", "sequence", sequence, "status", 413)
			return errors.WrapRPCResponseTooLarge(c.HorizonURL)
		case 429:
			logger.Logger.Warn("Rate limit exceeded", "sequence", sequence, "status", 429)
			return errors.WrapRateLimitExceeded()
		default:
			logger.Logger.Error("Horizon error", "sequence", sequence, "status", hErr.Problem.Status, "detail", hErr.Problem.Detail)
			return errors.WrapRPCError(c.HorizonURL, hErr.Problem.Detail, hErr.Problem.Status)
		}
	}

	// Generic error
	logger.Logger.Error("Failed to fetch ledger", "sequence", sequence, "error", err)
	return errors.WrapRPCConnectionFailed(err)
}

// IsLedgerNotFound checks if error is a "ledger not found" error
func IsLedgerNotFound(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, errors.ErrLedgerNotFound) {
		return true
	}
	return ledgerFailureContains(err, IsLedgerNotFound)
}

func ledgerFailureContains(err error, checker func(error) bool) bool {
	var allErr *AllNodesFailedError
	if !stdErrors.As(err, &allErr) {
		return false
	}
	for _, failure := range allErr.Failures {
		if checker(failure.Reason) {
			return true
		}
	}
	return false
}

// IsLedgerArchived checks if error is a "ledger archived" error
func IsLedgerArchived(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, errors.ErrLedgerArchived) {
		return true
	}
	return ledgerFailureContains(err, IsLedgerArchived)
}

// IsRateLimitError checks if error is a rate limit error
func IsRateLimitError(err error) bool {
	return errors.Is(err, errors.ErrRateLimitExceeded)
}

// IsResponseTooLarge checks if error indicates the RPC response exceeded size limits
func IsResponseTooLarge(err error) bool {
	return errors.Is(err, errors.ErrRPCResponseTooLarge)
}
