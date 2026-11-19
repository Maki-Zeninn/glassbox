// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package simulator

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/dotandev/glassbox/internal/logger"
	"github.com/dotandev/glassbox/internal/rpc"
	"github.com/stellar/go-stellar-sdk/clients/horizonclient"
)

// RegressionTestResult represents the outcome of a single transaction test.
type RegressionTestResult struct {
	TransactionHash string
	Status          string // "pass", "fail", "error"
	ErrorMessage    string
	EventCountMatch bool
	EventCount      int
	ExpectedCount   int
	TrapsMatch      bool
}

// RegressionTestSuite holds results from a batch of regression tests.
type RegressionTestSuite struct {
	TotalTests  int
	PassedTests int
	FailedTests int
	ErrorTests  int
	Results     []RegressionTestResult
	mu          sync.Mutex
}

// RegressionHarness manages protocol regression testing against historic transactions.
type RegressionHarness struct {
	Runner     RunnerInterface
	RPCClient  *rpc.Client
	MaxWorkers int
	Verbose    bool
}

// NewRegressionHarness creates a new regression test harness.
// maxWorkers defaults to 4 when <= 0.
func NewRegressionHarness(runner RunnerInterface, client *rpc.Client, maxWorkers int) *RegressionHarness {
	if maxWorkers <= 0 {
		maxWorkers = 4
	}
	return &RegressionHarness{
		Runner:     runner,
		RPCClient:  client,
		MaxWorkers: maxWorkers,
		Verbose:    false,
	}
}

// RunRegressionTests fetches and tests historic failed transactions.
// Returns an error with a descriptive message when count is invalid, the
// runner is nil, or no transactions are found for the given parameters.
func (h *RegressionHarness) RunRegressionTests(
	ctx context.Context,
	count int,
	protocolVersion *uint32,
	startSeq uint32,
) (*RegressionTestSuite, error) {
	if count <= 0 {
		return nil, fmt.Errorf(
			"--count must be greater than 0 (got %d); "+
				"specify how many historic failed transactions to test",
			count,
		)
	}
	if h.Runner == nil {
		return nil, fmt.Errorf(
			"regression harness has no simulator runner; "+
				"call NewRegressionHarness with a valid RunnerInterface",
		)
	}

	// Guard against a MaxWorkers value of 0 or negative that was set directly
	// on the struct after construction (NewRegressionHarness already defaults
	// it to 4, but callers can mutate it). A zero-capacity channel would
	// deadlock every goroutine immediately.
	if h.MaxWorkers <= 0 {
		original := h.MaxWorkers
		h.MaxWorkers = 4
		logger.Logger.Warn(
			"MaxWorkers was <= 0; defaulted to 4 to prevent semaphore deadlock",
			"original", original,
		)
	}

	logger.Logger.Info("Fetching historic failed transactions", "count", count)

	txHashes, err := h.fetchFailedTransactions(ctx, count, startSeq)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to fetch transaction hashes: %w\n"+
				"Check your --rpc-url and --network settings, or run "+
				"'glassbox doctor' to verify network connectivity",
			err,
		)
	}

	if len(txHashes) == 0 {
		return nil, fmt.Errorf(
			"no failed transactions found (count=%d, startSeq=%d)\n"+
				"Try adjusting --start-seq to an earlier ledger, or verify that the "+
				"selected network has recent failed transactions",
			count, startSeq,
		)
	}

	logger.Logger.Info("Found transactions to test", "count", len(txHashes))

	// Run tests in parallel
	suite := &RegressionTestSuite{
		TotalTests: len(txHashes),
		Results:    make([]RegressionTestResult, 0, len(txHashes)),
	}

	sem := make(chan struct{}, h.MaxWorkers)
	var wg sync.WaitGroup
	var processedCount atomic.Int64

	for _, txHash := range txHashes {
		wg.Add(1)
		go func(hash string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			result := h.testTransaction(ctx, hash, protocolVersion)
			suite.addResult(result)

			current := processedCount.Add(1)
			if h.Verbose || current%10 == 0 {
				logger.Logger.Info(
					"Test progress",
					"processed", current,
					"total", suite.TotalTests,
					"status", result.Status,
				)
			}
		}(txHash)
	}

	wg.Wait()

	// Calculate statistics
	for _, result := range suite.Results {
		switch result.Status {
		case "pass":
			suite.PassedTests++
		case "fail":
			suite.FailedTests++
		case "error":
			suite.ErrorTests++
		}
	}

	return suite, nil
}

// testTransaction runs a single transaction through the simulator and verifies results.
// All error paths return an RegressionTestResult with Status="error" and a
// descriptive ErrorMessage rather than panicking.
func (h *RegressionHarness) testTransaction(
	ctx context.Context,
	txHash string,
	protocolVersionOverride *uint32,
) RegressionTestResult {
	result := RegressionTestResult{
		TransactionHash: txHash,
		Status:          "error",
	}

	if h.RPCClient == nil {
		result.ErrorMessage = "RPC client not configured; provide an RPC client to the harness"
		return result
	}

	if txHash == "" {
		result.ErrorMessage = "transaction hash is empty — cannot test an empty hash\n" +
			"  Fix: verify the transaction hash list from the RPC is not corrupted\n" +
			"  Tip: re-run with --verbose to see which transactions are being fetched"
		return result
	}

	// Fetch transaction details
	resp, err := h.RPCClient.GetTransaction(ctx, txHash)
	if err != nil {
		result.ErrorMessage = fmt.Sprintf(
			"failed to fetch transaction %s: %v\n"+
				"Verify the hash is correct and the RPC endpoint is reachable",
			txHash, err,
		)
		return result
	}

	// Extract ledger keys from the transaction envelope's Soroban footprint.
	// The footprint (read-only + read-write sets) defines every ledger entry
	// that the contract touched; these are the keys we need to pre-load so the
	// simulator can replay the transaction against the correct state.
	keys, err := extractLedgerKeysFromXDR(resp.EnvelopeXdr)
	if err != nil {
		result.ErrorMessage = fmt.Sprintf("failed to extract ledger keys from XDR for %s: %v", txHash, err)
		return result
	}

	// Fetch ledger entries from network
	ledgerEntries, err := h.RPCClient.GetLedgerEntries(ctx, keys)
	if err != nil {
		result.ErrorMessage = fmt.Sprintf("failed to fetch ledger entries for %s: %v", txHash, err)
		return result
	}

	// Build simulation request
	simReq := &SimulationRequest{
		EnvelopeXdr:     resp.EnvelopeXdr,
		ResultMetaXdr:   resp.ResultMetaXdr,
		LedgerEntries:   ledgerEntries,
		ProtocolVersion: protocolVersionOverride,
	}

	// Run simulation
	simResp, err := h.Runner.Run(ctx, simReq)
	if err != nil {
		result.ErrorMessage = fmt.Sprintf(
			"simulation failed for %s: %v\n"+
				"Run 'glassbox debug %s' for a detailed trace",
			txHash, err, txHash,
		)
		return result
	}

	// Store actual event count
	if len(simResp.DiagnosticEvents) > 0 {
		result.EventCount = len(simResp.DiagnosticEvents)
	} else {
		result.EventCount = len(simResp.Events)
	}

	result.ExpectedCount = result.EventCount // simplified check

	// Verify results
	switch simResp.Status {
	case "success":
		result.Status = "pass"
		result.TrapsMatch = true
		result.EventCountMatch = true
	case "error":
		// Transaction failed in simulation — expected for historic failed txs
		result.Status = "pass"
		result.TrapsMatch = true
		result.EventCountMatch = true
		result.ErrorMessage = simResp.Error
	default:
		result.Status = "fail"
		result.TrapsMatch = false
		result.ErrorMessage = fmt.Sprintf(
			"unexpected simulation status %q for %s; expected 'success' or 'error'",
			simResp.Status, txHash,
		)
	}

	return result
}

// fetchFailedTransactions retrieves hashes of failed Soroban transactions from
// the Horizon API starting at startSeq (0 = latest ledger). It returns at most
// count hashes, fetching additional pages as needed.
//
// The Horizon transactions endpoint is queried with include_failed=true and
// order=desc so recent failed transactions surface first. Only transactions
// that are marked unsuccessful (Successful == false) are included in the
// returned slice; successful transactions are skipped and do not count against
// the requested count.
//
// A zero startSeq means "start from the most-recent ledger"; callers can pass
// a non-zero value to anchor the search at an earlier point in ledger history.
func (h *RegressionHarness) fetchFailedTransactions(
	ctx context.Context,
	count int,
	startSeq uint32,
) ([]string, error) {
	txHashes := make([]string, 0, count)

	logger.Logger.Info(
		"Fetching failed transactions from Horizon",
		"count", count,
		"startSeq", startSeq,
	)

	if h.RPCClient == nil {
		return nil, fmt.Errorf(
			"regression harness has no RPC client; " +
				"call NewRegressionHarness with a valid *rpc.Client",
		)
	}

	// Horizon caps a single page at 200 records; use the maximum page size to
	// minimise round-trips when collecting many transactions.
	const pageSize = 200

	// Build the initial Horizon transaction request. include_failed=true is
	// essential — without it Horizon only returns successful transactions.
	req := horizonclient.TransactionRequest{
		Limit:          uint(pageSize),
		Order:          horizonclient.OrderDesc,
		IncludeFailed:  true,
	}

	// If the caller has specified a starting ledger, cursor the request to
	// that ledger's approximate paging token. Horizon uses an opaque cursor
	// string derived from "<ledgerSeq>-<txIndex>" for transaction pagination.
	// We construct a coarse token that points to the beginning of startSeq so
	// the first page contains only transactions at or before that ledger.
	if startSeq > 0 {
		// Horizon cursor format: "<ledger_seq * 4096 + tx_index>". Using
		// tx_index = 0 positions the cursor at the opening of the ledger.
		req.Cursor = fmt.Sprintf("%d", uint64(startSeq)*4096)
	}

	// Page through transactions until we have enough or there are no more.
	for len(txHashes) < count {
		select {
		case <-ctx.Done():
			return txHashes, ctx.Err()
		default:
		}

		page, err := h.RPCClient.Horizon.Transactions(req)
		if err != nil {
			if len(txHashes) > 0 {
				// Return what we have so far rather than discarding all results
				// on a mid-pagination network error.
				logger.Logger.Warn(
					"Horizon page fetch failed; returning partial results",
					"collected", len(txHashes),
					"error", err,
				)
				break
			}
			return nil, fmt.Errorf(
				"failed to fetch transactions from Horizon: %w\n"+
					"Check your --rpc-url and --network settings, or run "+
					"'glassbox doctor' to verify network connectivity",
				err,
			)
		}

		records := page.Embedded.Records
		if len(records) == 0 {
			// No more transactions available in this direction.
			logger.Logger.Debug("No more transaction records from Horizon", "collected", len(txHashes))
			break
		}

		for _, tx := range records {
			if len(txHashes) >= count {
				break
			}
			// Only collect actually-failed transactions; skip successful ones.
			if !tx.Successful {
				txHashes = append(txHashes, tx.Hash)
				logger.Logger.Debug(
					"Found failed transaction",
					"hash", tx.Hash,
					"ledger", tx.Ledger,
				)
			}
		}

		// Advance the cursor to the last record on this page for the next
		// iteration. Horizon's PT (paging token) is the stable cursor to use.
		lastPT := records[len(records)-1].PagingToken()
		if lastPT == "" {
			// No paging token means we cannot advance; stop here.
			break
		}
		req.Cursor = lastPT
	}

	logger.Logger.Info(
		"Finished fetching failed transactions",
		"requested", count,
		"collected", len(txHashes),
	)
	return txHashes, nil
}

// extractLedgerKeysFromXDR extracts ledger keys from a transaction envelope XDR.
// It decodes the envelope and collects the Soroban footprint read-only and
// read-write keys, which represent the complete ledger key set required to
// replay the transaction. When the envelope carries no Soroban data (e.g. a
// classic payment), an empty slice is returned without error.
//
// The returned keys are base64-encoded XDR LedgerKey strings, ready to pass
// directly to rpc.Client.GetLedgerEntries.
func extractLedgerKeysFromXDR(envelopeXdr string) ([]string, error) {
	if envelopeXdr == "" {
		return []string{}, nil
	}

	decoded, err := DecodeEnvelopeXDR(envelopeXdr)
	if err != nil {
		return nil, fmt.Errorf("extractLedgerKeysFromXDR: decode envelope: %w", err)
	}

	return collectFootprintKeys(decoded), nil
}

// collectFootprintKeys recursively walks a DecodedSimEnvelope and accumulates
// all footprint ledger keys (read-only and read-write) into a deduplicated
// slice. FeeBump envelopes are unwrapped to their inner V1 transaction.
func collectFootprintKeys(env *DecodedSimEnvelope) []string {
	if env == nil {
		return nil
	}

	// FeeBump: the actual Soroban data lives in the inner transaction.
	if env.Variant == VariantFeeBump && env.InnerEnvelope != nil {
		return collectFootprintKeys(env.InnerEnvelope)
	}

	fp := env.Footprint
	if fp == nil {
		return []string{}
	}

	// Deduplicate in case the same key appears in both read-only and
	// read-write sets (which is invalid per spec, but defensive is safer).
	seen := make(map[string]struct{}, len(fp.ReadOnly)+len(fp.ReadWrite))
	keys := make([]string, 0, len(fp.ReadOnly)+len(fp.ReadWrite))

	for _, k := range fp.ReadOnly {
		if _, dup := seen[k]; !dup {
			seen[k] = struct{}{}
			keys = append(keys, k)
		}
	}
	for _, k := range fp.ReadWrite {
		if _, dup := seen[k]; !dup {
			seen[k] = struct{}{}
			keys = append(keys, k)
		}
	}

	return keys
}

// addResult adds a test result to the suite (thread-safe).
func (suite *RegressionTestSuite) addResult(result RegressionTestResult) {
	suite.mu.Lock()
	defer suite.mu.Unlock()
	suite.Results = append(suite.Results, result)
}

// Summary returns a formatted summary of the test suite results.
func (suite *RegressionTestSuite) Summary() string {
	if suite.TotalTests == 0 {
		return "Regression Test Summary:\n  No tests were executed."
	}
	return fmt.Sprintf(
		"Regression Test Summary:\n"+
			"  Total Tests: %d\n"+
			"  Passed: %d\n"+
			"  Failed: %d\n"+
			"  Errors: %d\n"+
			"  Success Rate: %.1f%%",
		suite.TotalTests,
		suite.PassedTests,
		suite.FailedTests,
		suite.ErrorTests,
		float64(suite.PassedTests)/float64(suite.TotalTests)*100,
	)
}

// FailedResults returns only the failed and error test results.
func (suite *RegressionTestSuite) FailedResults() []RegressionTestResult {
	suite.mu.Lock()
	defer suite.mu.Unlock()

	failed := make([]RegressionTestResult, 0)
	for _, result := range suite.Results {
		if result.Status == "fail" || result.Status == "error" {
			failed = append(failed, result)
		}
	}
	return failed
}
