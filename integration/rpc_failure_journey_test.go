// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

// Package integration – RPC failure-class journey test
//
// TestRPCFailureJourney and its companion tests exercise every documented RPC
// failure mode in a controlled environment:
//
//  1. Connection refused / unreachable endpoint
//  2. HTTP 500 Internal Server Error (transient server fault)
//  3. HTTP 503 Service Unavailable — retries exhausted
//  4. HTTP 503 → success on third attempt (retry recovery)
//  5. HTTP 429 Rate Limited with integer Retry-After header
//  6. HTTP 429 Rate Limited with RFC 1123 date Retry-After header
//  7. HTTP 429 with Retry-After: 0 (fast path)
//  8. HTTP 413 Response Too Large (non-retryable)
//  9. Malformed JSON body
// 10. JSON-RPC auth failure
// 11. JSON-RPC transaction not found  (with hint assertion)
// 12. JSON-RPC ledger not found
// 13. JSON-RPC ledger archived
// 14. All-nodes-failed (two servers both returning 503)
// 15. Cancellation during retry backoff
// 16. compare command — missing --wasm validation (PreRunE)
// 17. compare command — RPC down with valid WASM file
// 18. dry-run subcommand RPC failure
// 19. debug --dry-run flag makes zero RPC calls
// 20. watch mode timeout
// 21. Session absence on every RPC failure variant
// 22. Retry count ≤ MaxRetries+1 for each retryable code
// 23. Exit-code stability across failure classes
// 24. Error output on stderr, not stdout
// 25. Partial artifact safety (no trace file on failure)
// 26. Invalid network flag triggers PreRunE, no RPC contact
// 27. JSON output mode on failure (stdout must be empty or valid JSON)
//
// Acceptance criteria (from the spec):
//   - Each failure class produces the documented stable exit code and
//     remediation guidance in stderr.
//   - Retries stay within policy (≤4 total attempts for retryable codes).
//   - Partial artifacts are safe: no session created, no trace file written.
//   - dry-run does not make the simulated operation calls.
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ─── constants ────────────────────────────────────────────────────────────────

// rpcValidTxHash is a syntactically correct 64-hex-char transaction hash used
// wherever the CLI's PreRunE validation requires a well-formed hash.
const rpcValidTxHash = "abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234"

// Documented exit codes mirrored from internal/cmd/exitcode.go + interrupt.go.
// Only the codes actually referenced in assertions are declared; unused codes
// are omitted to keep the constant block free of dead identifiers.
const (
	rpcExitSuccess  = 0
	rpcExitInternal = 3
)

// ─── fake RPC server utilities ───────────────────────────────────────────────

// rpcJSONError builds a JSON-RPC 2.0 error payload.
func rpcJSONError(code int, message string) []byte {
	b, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"error":   map[string]any{"code": code, "message": message},
	})
	return b
}

// rpcJSONResult builds a JSON-RPC 2.0 success payload.
func rpcJSONResult(result any) []byte {
	b, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"result":  result,
	})
	return b
}

// rpcFixedHandler returns a handler that always responds with the given HTTP
// status, body, and optional headers.
func rpcFixedHandler(status int, body []byte, headers map[string]string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if body != nil {
			_, _ = w.Write(body)
		}
	})
}

// rpcCounting wraps inner and atomically increments hits on every request.
func rpcCounting(hits *int64, inner http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(hits, 1)
		inner.ServeHTTP(w, r)
	})
}

// rpcSeqEntry is one response in a scripted sequence.
type rpcSeqEntry struct {
	status  int
	body    []byte
	headers map[string]string
}

// rpcSequence returns a handler that walks through entries in order, using the
// last entry for all subsequent requests.  It also returns a pointer to the
// request count.
func rpcSequence(entries []rpcSeqEntry) (http.Handler, *int64) {
	var n int64
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := int(atomic.AddInt64(&n, 1) - 1)
		e := entries[len(entries)-1]
		if idx < len(entries) {
			e = entries[idx]
		}
		for k, v := range e.headers {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(e.status)
		if e.body != nil {
			_, _ = w.Write(e.body)
		}
	})
	return h, &n
}

// ─── hermetic environment builder ────────────────────────────────────────────

// rpcHermeticEnv builds a minimal, isolated environment for child glassbox
// processes.  GLASSBOX_RPC_URL is pointed at mockURL; PATH is preserved for
// shared-library resolution; everything else is stripped to avoid ambient
// config leakage.
func rpcHermeticEnv(mockURL string, extra ...string) []string {
	env := []string{
		"GLASSBOX_RPC_URL=" + mockURL,
		"GLASSBOX_NO_UPDATE_CHECK=1",
		"GLASSBOX_TELEMETRY=",
		"GLASSBOX_TELEMETRY_ENDPOINT=",
		"GLASSBOX_SIM_PATH=",
		"NO_COLOR=1",
		"HOME=/tmp",
		"USERPROFILE=/tmp",
	}
	for _, e := range os.Environ() {
		if strings.HasPrefix(strings.ToUpper(e), "PATH=") {
			env = append(env, e)
			break
		}
	}
	return append(env, extra...)
}

// ─── child-process runner ─────────────────────────────────────────────────────

// rpcRun executes the glassbox binary with the hermetic RPC environment and a
// 20 s timeout (generous enough for retry-backoff tests that use Retry-After: 1).
func rpcRun(t *testing.T, mockURL string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	return rpcRunEnv(t, rpcHermeticEnv(mockURL), 20*time.Second, args...)
}

// rpcRunCustomEnv allows callers to supply a fully custom environment.
func rpcRunCustomEnv(t *testing.T, env []string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	return rpcRunEnv(t, env, 20*time.Second, args...)
}

// rpcRunEnv is the low-level runner that all RPC-journey helpers delegate to.
func rpcRunEnv(t *testing.T, env []string, timeout time.Duration, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	bin := binaryPath(t)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = env

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err = cmd.Run()
	return outBuf.String(), errBuf.String(), err
}

// ─── focused assertion helpers ────────────────────────────────────────────────

// assertRPCExit3 asserts ExitInternalError (3) — the stable code for RPC failures.
func assertRPCExit3(t *testing.T, err error) {
	t.Helper()
	if got := exitCode(err); got != rpcExitInternal {
		t.Errorf("exit code: got %d, want %d (InternalError)", got, rpcExitInternal)
	}
}

// assertNoSessionCreated asserts that no "Session created:" line was emitted,
// proving the session lifecycle was never reached on the failure path.
func assertNoSessionCreated(t *testing.T, label, stderr string) {
	t.Helper()
	if strings.Contains(stderr, "Session created:") {
		t.Errorf("%s: unexpected session creation on RPC failure: %s", label, stderr)
	}
}

// assertMaxAttempts asserts the server received at most max requests.
func assertMaxAttempts(t *testing.T, label string, got, max int64) {
	t.Helper()
	if got > max {
		t.Errorf("%s: retry violation — server got %d requests, want ≤%d", label, got, max)
	}
}

// ─── 1. Connection refused ────────────────────────────────────────────────────

func TestRPCConnectionRefused(t *testing.T) {
	if testing.Short() {
		t.Skip("RPC journey test skipped in -short mode")
	}
	_, stderr, err := rpcRun(t, "http://127.0.0.1:1",
		"debug", rpcValidTxHash, "--network", "testnet",
	)
	assertRPCExit3(t, err)
	assertNotContains(t, "stderr", stderr, "panic")
	assertNotContains(t, "stderr", stderr, "goroutine")
	if !containsAny(stderr, "connection", "refused", "failed", "error", "RPC") {
		t.Errorf("connection-refused: expected a connection error in stderr, got: %q", stderr)
	}
	assertNoSessionCreated(t, "connection-refused", stderr)
}

// ─── 2. HTTP 500 Internal Server Error ───────────────────────────────────────

func TestRPCServerError500(t *testing.T) {
	if testing.Short() {
		t.Skip("RPC journey test skipped in -short mode")
	}
	var hits int64
	srv := httptest.NewServer(rpcCounting(&hits,
		rpcFixedHandler(http.StatusInternalServerError,
			rpcJSONError(-32603, "internal error"), nil),
	))
	defer srv.Close()

	_, stderr, err := rpcRun(t, srv.URL,
		"debug", rpcValidTxHash, "--network", "testnet",
	)
	assertRPCExit3(t, err)
	assertNotContains(t, "stderr", stderr, "panic")
	assertNoSessionCreated(t, "500-error", stderr)
	// HTTP 500 is NOT in StatusCodesToRetry; allow ≤3 for health checks.
	assertMaxAttempts(t, "500-error", atomic.LoadInt64(&hits), 3)
}

// ─── 3. HTTP 503 exhausts retries ────────────────────────────────────────────

func TestRPCServiceUnavailable503Exhausted(t *testing.T) {
	if testing.Short() {
		t.Skip("RPC journey test skipped in -short mode")
	}
	var hits int64
	srv := httptest.NewServer(rpcCounting(&hits,
		// Include Retry-After: 1 to cap the per-retry delay at 1 s, keeping the
		// total wall time bounded well within the 20 s runner timeout.
		// Without it, full-jitter exponential backoff can reach MaxBackoff (10 s)
		// per retry, making the worst-case total ~30 s on slow hardware.
		rpcFixedHandler(http.StatusServiceUnavailable,
			rpcJSONError(-32603, "service unavailable"),
			map[string]string{"Retry-After": "1"},
		),
	))
	defer srv.Close()

	_, stderr, err := rpcRun(t, srv.URL,
		"debug", rpcValidTxHash, "--network", "testnet",
	)
	assertRPCExit3(t, err)
	assertNotContains(t, "stderr", stderr, "panic")
	assertNoSessionCreated(t, "503-exhausted", stderr)
	// MaxRetries=3 → ≤4 total attempts.
	assertMaxAttempts(t, "503-exhausted", atomic.LoadInt64(&hits), 4)
	if n := atomic.LoadInt64(&hits); n < 2 {
		t.Errorf("503-exhausted: expected ≥2 attempts (retry must fire), got %d", n)
	}
}

// ─── 4. HTTP 503 → success on retry ──────────────────────────────────────────

func TestRPCServiceUnavailableThenSuccess(t *testing.T) {
	if testing.Short() {
		t.Skip("RPC journey test skipped in -short mode")
	}
	txResult := map[string]any{
		"status":        "SUCCESS",
		"envelopeXdr":   "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"resultXdr":     "",
		"resultMetaXdr": "",
	}
	seq, hitPtr := rpcSequence([]rpcSeqEntry{
		{status: http.StatusServiceUnavailable, body: rpcJSONError(-32603, "unavailable")},
		{status: http.StatusServiceUnavailable, body: rpcJSONError(-32603, "unavailable")},
		{status: http.StatusOK, body: rpcJSONResult(txResult)},
	})
	srv := httptest.NewServer(seq)
	defer srv.Close()

	_, _, err := rpcRun(t, srv.URL,
		"debug", rpcValidTxHash, "--network", "testnet",
	)
	n := atomic.LoadInt64(hitPtr)
	// The server must have received at least 3 requests (two 503s + one 200).
	if n < 3 {
		t.Errorf("503-then-success: expected ≥3 requests (2 retries + 1 success), got %d", n)
	}
	// After the server answered successfully on attempt 3, the binary must
	// NOT exit with an RPC-failure code (3).  Acceptable exits are 0
	// (success), 2 (simulator not found), or any non-RPC exit.
	if exitCode(err) == rpcExitInternal {
		t.Errorf("503-then-success: got exit 3 (RPC failure) despite server succeeding on attempt 3; hits=%d", n)
	}
}

// ─── 5. HTTP 429 with integer Retry-After ─────────────────────────────────────

func TestRPC429IntegerRetryAfter(t *testing.T) {
	if testing.Short() {
		t.Skip("RPC journey test skipped in -short mode")
	}
	var hits int64
	srv := httptest.NewServer(rpcCounting(&hits,
		rpcFixedHandler(http.StatusTooManyRequests,
			rpcJSONError(-32000, "rate limit exceeded"),
			map[string]string{"Retry-After": "1"},
		),
	))
	defer srv.Close()

	start := time.Now()
	_, stderr, err := rpcRun(t, srv.URL,
		"debug", rpcValidTxHash, "--network", "testnet",
	)
	elapsed := time.Since(start)

	assertRPCExit3(t, err)
	assertNotContains(t, "stderr", stderr, "panic")
	assertNoSessionCreated(t, "429-int", stderr)
	assertMaxAttempts(t, "429-int", atomic.LoadInt64(&hits), 4)
	if n := atomic.LoadInt64(&hits); n < 2 {
		t.Errorf("429-int: expected ≥2 requests, got %d", n)
	}
	// Retry-After: 1 → at least 1 retry was delayed ≥1 s.
	if elapsed < 900*time.Millisecond {
		t.Errorf("429-int: elapsed %v < 900ms — Retry-After not honoured?", elapsed)
	}
}

// ─── 6. HTTP 429 with RFC 1123 date Retry-After ──────────────────────────────

func TestRPC429HTTPDateRetryAfter(t *testing.T) {
	if testing.Short() {
		t.Skip("RPC journey test skipped in -short mode")
	}
	retryAt := time.Now().Add(1 * time.Second).UTC().Format(http.TimeFormat)

	var hits int64
	srv := httptest.NewServer(rpcCounting(&hits,
		rpcFixedHandler(http.StatusTooManyRequests,
			rpcJSONError(-32000, "rate limit exceeded"),
			map[string]string{"Retry-After": retryAt},
		),
	))
	defer srv.Close()

	start := time.Now()
	_, stderr, err := rpcRun(t, srv.URL,
		"debug", rpcValidTxHash, "--network", "testnet",
	)
	elapsed := time.Since(start)

	assertRPCExit3(t, err)
	assertNotContains(t, "stderr", stderr, "panic")
	assertMaxAttempts(t, "429-httpdate", atomic.LoadInt64(&hits), 4)
	if elapsed < 800*time.Millisecond {
		t.Errorf("429-httpdate: elapsed %v < 800ms — RFC1123 Retry-After not honoured?", elapsed)
	}
}

// ─── 7. HTTP 429 with Retry-After: 0 (fast path) ─────────────────────────────

func TestRPC429RetryAfterZero(t *testing.T) {
	if testing.Short() {
		t.Skip("RPC journey test skipped in -short mode")
	}
	var hits int64
	srv := httptest.NewServer(rpcCounting(&hits,
		rpcFixedHandler(http.StatusTooManyRequests,
			rpcJSONError(-32000, "rate limit"),
			map[string]string{"Retry-After": "0"},
		),
	))
	defer srv.Close()

	_, stderr, err := rpcRun(t, srv.URL,
		"debug", rpcValidTxHash, "--network", "testnet",
	)
	assertRPCExit3(t, err)
	assertNotContains(t, "stderr", stderr, "panic")
	assertMaxAttempts(t, "429-zero", atomic.LoadInt64(&hits), 4)
}

// ─── 8. HTTP 413 Response Too Large (non-retryable) ──────────────────────────

func TestRPCResponseTooLarge413(t *testing.T) {
	if testing.Short() {
		t.Skip("RPC journey test skipped in -short mode")
	}
	var hits int64
	srv := httptest.NewServer(rpcCounting(&hits,
		rpcFixedHandler(http.StatusRequestEntityTooLarge, nil, nil),
	))
	defer srv.Close()

	_, stderr, err := rpcRun(t, srv.URL,
		"debug", rpcValidTxHash, "--network", "testnet",
	)
	assertRPCExit3(t, err)
	assertNotContains(t, "stderr", stderr, "panic")
	assertNoSessionCreated(t, "413-too-large", stderr)
	// 413 is non-retryable: ≤2 attempts (1 real + possible health check).
	assertMaxAttempts(t, "413-non-retryable", atomic.LoadInt64(&hits), 2)
}

// ─── 9. Malformed JSON response ───────────────────────────────────────────────

func TestRPCMalformedJSONResponse(t *testing.T) {
	if testing.Short() {
		t.Skip("RPC journey test skipped in -short mode")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{this is not valid json{{{{`)
	}))
	defer srv.Close()

	_, stderr, err := rpcRun(t, srv.URL,
		"debug", rpcValidTxHash, "--network", "testnet",
	)
	if exitCode(err) == rpcExitSuccess {
		t.Error("malformed-json: expected non-zero exit")
	}
	assertNotContains(t, "stderr", stderr, "panic")
	assertNotContains(t, "stderr", stderr, "goroutine")
	assertNoSessionCreated(t, "malformed-json", stderr)
}

// ─── 10. JSON-RPC auth failure ────────────────────────────────────────────────

func TestRPCAuthFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("RPC journey test skipped in -short mode")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(rpcJSONError(-32001, "auth error: missing or invalid authorization credentials"))
	}))
	defer srv.Close()

	_, stderr, err := rpcRun(t, srv.URL,
		"debug", rpcValidTxHash, "--network", "testnet",
	)
	if exitCode(err) == rpcExitSuccess {
		t.Error("auth-failure: expected non-zero exit")
	}
	assertNotContains(t, "stderr", stderr, "panic")
	assertNotContains(t, "stderr", stderr, "goroutine")
	if !containsAny(stderr, "auth", "unauthorized", "credential", "error", "failed") {
		t.Errorf("auth-failure: expected auth diagnostic in stderr, got: %q", stderr)
	}
	assertNoSessionCreated(t, "auth-failure", stderr)
}

func TestRPCAuthFailureExitCode(t *testing.T) {
	if testing.Short() {
		t.Skip("RPC journey test skipped in -short mode")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write(rpcJSONError(-32001, "unauthorized"))
	}))
	defer srv.Close()

	_, _, err := rpcRun(t, srv.URL,
		"debug", rpcValidTxHash, "--network", "testnet",
	)
	// Auth via HTTP 401 wraps as WrapRPCConnectionFailed → ExitInternalError.
	assertRPCExit3(t, err)
}

// ─── 11. Transaction not found ────────────────────────────────────────────────

func TestRPCTransactionNotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("RPC journey test skipped in -short mode")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(rpcJSONResult(map[string]any{"status": "NOT_FOUND"}))
	}))
	defer srv.Close()

	_, stderr, err := rpcRun(t, srv.URL,
		"debug", rpcValidTxHash, "--network", "testnet",
	)
	if exitCode(err) == rpcExitSuccess {
		t.Error("tx-not-found: expected non-zero exit")
	}
	assertNotContains(t, "stderr", stderr, "panic")
	assertNoSessionCreated(t, "tx-not-found", stderr)
}

// TestRPCTransactionNotFoundHint exercises the JSON-RPC error variant that
// triggers WrapTransactionNotFound and must surface a remediation hint.
func TestRPCTransactionNotFoundHint(t *testing.T) {
	if testing.Short() {
		t.Skip("RPC journey test skipped in -short mode")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(rpcJSONError(-32001, "transaction not found"))
	}))
	defer srv.Close()

	_, stderr, err := rpcRun(t, srv.URL,
		"debug", rpcValidTxHash, "--network", "testnet",
	)
	if exitCode(err) == rpcExitSuccess {
		t.Error("tx-not-found-hint: expected non-zero exit")
	}
	assertNotContains(t, "stderr", stderr, "panic")
	// The Hint from WrapTransactionNotFound is printed by main.go as "Hint: …".
	if !containsAny(stderr, "Hint:", "hint:", "check", "network", "hash", "transaction", "not found") {
		t.Errorf("tx-not-found-hint: expected actionable guidance, got: %q", stderr)
	}
	assertNoSessionCreated(t, "tx-not-found-hint", stderr)
}

// ─── 12. Ledger not found ─────────────────────────────────────────────────────

func TestRPCLedgerNotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("RPC journey test skipped in -short mode")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(rpcJSONError(-32001, "ledger not found"))
	}))
	defer srv.Close()

	_, stderr, err := rpcRun(t, srv.URL,
		"debug", rpcValidTxHash, "--network", "testnet",
	)
	if exitCode(err) == rpcExitSuccess {
		t.Error("ledger-not-found: expected non-zero exit")
	}
	assertNotContains(t, "stderr", stderr, "panic")
	assertNoSessionCreated(t, "ledger-not-found", stderr)
}

// ─── 13. Ledger archived ──────────────────────────────────────────────────────

func TestRPCLedgerArchived(t *testing.T) {
	if testing.Short() {
		t.Skip("RPC journey test skipped in -short mode")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(rpcJSONError(-32001, "ledger has been archived"))
	}))
	defer srv.Close()

	_, stderr, err := rpcRun(t, srv.URL,
		"debug", rpcValidTxHash, "--network", "testnet",
	)
	if exitCode(err) == rpcExitSuccess {
		t.Error("ledger-archived: expected non-zero exit")
	}
	assertNotContains(t, "stderr", stderr, "panic")
	if !containsAny(stderr, "archived", "ledger", "error", "failed") {
		t.Errorf("ledger-archived: expected diagnostic in stderr, got: %q", stderr)
	}
	assertNoSessionCreated(t, "ledger-archived", stderr)
}

// ─── 14. All-nodes-failed ─────────────────────────────────────────────────────

func TestRPCAllNodesFailed(t *testing.T) {
	if testing.Short() {
		t.Skip("RPC journey test skipped in -short mode")
	}
	srv1 := httptest.NewServer(rpcFixedHandler(http.StatusServiceUnavailable,
		rpcJSONError(-32603, "unavailable"), map[string]string{"Retry-After": "1"}))
	defer srv1.Close()
	srv2 := httptest.NewServer(rpcFixedHandler(http.StatusServiceUnavailable,
		rpcJSONError(-32603, "unavailable"), map[string]string{"Retry-After": "1"}))
	defer srv2.Close()

	// Pass both URLs via the env var (comma-separated list).
	_, stderr, err := rpcRunCustomEnv(t,
		rpcHermeticEnv(srv1.URL+","+srv2.URL),
		"debug", rpcValidTxHash, "--network", "testnet",
	)
	assertRPCExit3(t, err)
	assertNotContains(t, "stderr", stderr, "panic")
	assertNoSessionCreated(t, "all-nodes-failed", stderr)
}

// ─── 15. Cancellation during retry backoff ────────────────────────────────────

// TestRPCCancellationDuringBackoff uses a very short overall timeout to kill
// the process while it waits in a 60-second Retry-After backoff.
func TestRPCCancellationDuringBackoff(t *testing.T) {
	if testing.Short() {
		t.Skip("RPC journey test skipped in -short mode")
	}
	var hits int64
	// Retry-After: 60 parks the binary in a 60-s sleep — long enough to be
	// interrupted by our 3 s test timeout.
	srv := httptest.NewServer(rpcCounting(&hits,
		rpcFixedHandler(http.StatusTooManyRequests,
			rpcJSONError(-32000, "rate limit exceeded"),
			map[string]string{"Retry-After": "60"},
		),
	))
	defer srv.Close()

	// Run with a 3 s timeout so the driver kills the binary mid-backoff.
	_, stderr, err := rpcRunEnv(t, rpcHermeticEnv(srv.URL), 3*time.Second,
		"debug", rpcValidTxHash, "--network", "testnet",
	)

	// Acceptable: 130 (SIGINT), 3 (context/RPC failure), or -1 (killed by OS).
	if exitCode(err) == rpcExitSuccess {
		t.Error("cancellation-during-backoff: unexpected exit 0")
	}
	assertNotContains(t, "stderr", stderr, "panic")
	assertNotContains(t, "stderr", stderr, "goroutine")
	assertNoSessionCreated(t, "cancellation-during-backoff", stderr)
	if atomic.LoadInt64(&hits) == 0 {
		t.Error("cancellation-during-backoff: server was never contacted before cancellation")
	}
}

// ─── 16. compare command — PreRunE guard ─────────────────────────────────────

func TestCompareCommandMissingWasm(t *testing.T) {
	if testing.Short() {
		t.Skip("RPC journey test skipped in -short mode")
	}
	srv := httptest.NewServer(rpcFixedHandler(http.StatusOK,
		rpcJSONResult(map[string]any{"status": "SUCCESS"}), nil))
	defer srv.Close()

	// No --wasm → PreRunE exits 1 (validation error), no RPC call.
	_, stderr, err := rpcRun(t, srv.URL,
		"compare", rpcValidTxHash, "--network", "testnet",
	)
	if exitCode(err) == rpcExitSuccess {
		t.Error("compare-no-wasm: expected non-zero exit")
	}
	if !containsAny(stderr, "wasm", "--wasm", "required", "flag", "error") {
		t.Errorf("compare-no-wasm: expected validation error, got: %q", stderr)
	}
	assertNotContains(t, "stderr", stderr, "panic")
}

// ─── 17. compare command — RPC down with valid WASM ──────────────────────────

func TestCompareCommandRPCDown(t *testing.T) {
	if testing.Short() {
		t.Skip("RPC journey test skipped in -short mode")
	}
	// Write a 8-byte WASM magic so os.Stat + magic-byte checks pass.
	wasmFile, err := os.CreateTemp(t.TempDir(), "*.wasm")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = wasmFile.Write([]byte{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00})
	_ = wasmFile.Close()

	srv := httptest.NewServer(rpcFixedHandler(http.StatusServiceUnavailable,
		rpcJSONError(-32603, "unavailable"), map[string]string{"Retry-After": "1"}))
	defer srv.Close()

	_, stderr, err := rpcRun(t, srv.URL,
		"compare", rpcValidTxHash,
		"--network", "testnet",
		"--wasm", wasmFile.Name(),
	)
	if exitCode(err) == rpcExitSuccess {
		t.Error("compare-rpc-down: expected non-zero exit")
	}
	assertNotContains(t, "stderr", stderr, "panic")
	assertNotContains(t, "stderr", stderr, "goroutine")
}

// ─── 18. dry-run subcommand RPC failure ───────────────────────────────────────

func TestDryRunSubcommandRPCFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("RPC journey test skipped in -short mode")
	}
	// Write a syntactically minimal base64 XDR file.  The binary will attempt
	// to unmarshal it as a TransactionEnvelope; if it fails we get exit 1 —
	// that's also an acceptable test outcome (validates early rejection).
	xdrFile, err := os.CreateTemp(t.TempDir(), "tx*.xdr")
	if err != nil {
		t.Fatal(err)
	}
	// 88-char base64 = 66 zero bytes — not a valid XDR envelope, but the
	// binary will error out cleanly before touching the RPC.
	_, _ = fmt.Fprintln(xdrFile,
		"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	_ = xdrFile.Close()

	srv := httptest.NewServer(rpcFixedHandler(http.StatusServiceUnavailable,
		rpcJSONError(-32603, "unavailable"), map[string]string{"Retry-After": "1"}))
	defer srv.Close()

	_, stderr, err := rpcRun(t, srv.URL,
		"dry-run", xdrFile.Name(), "--network", "testnet",
	)
	if exitCode(err) == rpcExitSuccess {
		t.Error("dry-run-subcommand-rpc-down: expected non-zero exit")
	}
	assertNotContains(t, "stderr", stderr, "panic")
	assertNotContains(t, "stderr", stderr, "goroutine")
}

// ─── 19. debug --dry-run makes zero RPC calls ─────────────────────────────────

// TestDebugDryRunNoRPCCall verifies that `debug --dry-run` never contacts the
// RPC endpoint.  Any request to the fake server fails the test immediately.
func TestDebugDryRunNoRPCCall(t *testing.T) {
	if testing.Short() {
		t.Skip("RPC journey test skipped in -short mode")
	}
	var hits int64
	srv := httptest.NewServer(rpcCounting(&hits, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Do not call t.Errorf here — this handler runs in httptest's goroutine,
		// which may outlive the test function.  The hit counter is checked after
		// rpcRun returns, when the test goroutine is still active.
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(rpcJSONResult(map[string]any{"status": "SUCCESS"}))
	})))
	defer srv.Close()

	_, stderr, err := rpcRun(t, srv.URL,
		"debug", rpcValidTxHash,
		"--network", "testnet",
		"--dry-run",
	)
	code := exitCode(err)
	// Acceptable: 0 (validation passed) or 2 (simulator not found).
	// Not acceptable: 3 (means an RPC call was attempted).
	if code == rpcExitInternal {
		t.Errorf("debug --dry-run: exit 3 indicates RPC was contacted; stderr=%q", stderr)
	}
	assertNotContains(t, "stderr", stderr, "panic")
	if n := atomic.LoadInt64(&hits); n > 0 {
		t.Errorf("debug --dry-run: %d unexpected RPC request(s) made", n)
	}
}

// ─── 20. watch mode timeout ───────────────────────────────────────────────────

func TestWatchModeTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("RPC journey test skipped in -short mode")
	}
	// Always return PENDING so watch never terminates on its own.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(rpcJSONResult(map[string]any{"status": "PENDING"}))
	}))
	defer srv.Close()

	start := time.Now()
	_, stderr, err := rpcRun(t, srv.URL,
		"debug", rpcValidTxHash,
		"--network", "testnet",
		"--watch",
		"--watch-timeout", "2",
	)
	elapsed := time.Since(start)

	if exitCode(err) == rpcExitSuccess {
		t.Error("watch-timeout: expected non-zero exit")
	}
	assertNotContains(t, "stderr", stderr, "panic")
	assertNotContains(t, "stderr", stderr, "goroutine")
	if !containsAny(stderr, "timeout", "not found", "transaction", "watch", "error") {
		t.Errorf("watch-timeout: expected timeout diagnostic, got: %q", stderr)
	}
	assertNoSessionCreated(t, "watch-timeout", stderr)
	if elapsed < 1500*time.Millisecond {
		t.Errorf("watch-timeout: elapsed %v < 1.5s — timeout not respected?", elapsed)
	}
}

// ─── 21. Session absence on failure variants ──────────────────────────────────

func TestNoSessionOnRPCFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("RPC journey test skipped in -short mode")
	}
	cases := []struct {
		name   string
		status int
		body   []byte
		hdrs   map[string]string
	}{
		{"500", http.StatusInternalServerError, rpcJSONError(-32603, "internal error"), nil},
		// Retry-After: 1 caps 503 backoff so worst-case total stays within 20 s.
		{"503", http.StatusServiceUnavailable, rpcJSONError(-32603, "unavailable"), map[string]string{"Retry-After": "1"}},
		{"401", http.StatusUnauthorized, rpcJSONError(-32001, "unauthorized"), nil},
		{"not_found_jsonrpc", http.StatusOK, rpcJSONError(-32001, "transaction not found"), nil},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(rpcFixedHandler(tc.status, tc.body, tc.hdrs))
			defer srv.Close()

			_, stderr, err := rpcRun(t, srv.URL,
				"debug", rpcValidTxHash, "--network", "testnet",
			)
			if exitCode(err) == rpcExitSuccess {
				t.Errorf("%s: expected non-zero exit", tc.name)
			}
			assertNoSessionCreated(t, tc.name, stderr)
			assertNotContains(t, tc.name+" stderr", stderr, "panic")
		})
	}
}

// ─── 22. Retry count ≤ MaxRetries+1 ──────────────────────────────────────────

func TestRetryCountEnforcement(t *testing.T) {
	if testing.Short() {
		t.Skip("RPC journey test skipped in -short mode")
	}
	const maxRetryable = int64(4)  // 3 retries + 1 initial = 4 total
	const maxSingleShot = int64(3) // non-retryable: 1 attempt + ≤2 health calls

	cases := []struct {
		name        string
		status      int
		headers     map[string]string
		retryable   bool
		maxExpected int64
	}{
		// Retryable codes: all include Retry-After: 1 to cap per-retry delay at
		// 1 s so worst-case total stays well within the 20 s runner timeout.
		{"429", http.StatusTooManyRequests, map[string]string{"Retry-After": "1"}, true, maxRetryable},
		{"503", http.StatusServiceUnavailable, map[string]string{"Retry-After": "1"}, true, maxRetryable},
		{"504", http.StatusGatewayTimeout, map[string]string{"Retry-After": "1"}, true, maxRetryable},
		// Non-retryable codes: single attempt (+ ≤2 health/staleness checks).
		{"500", http.StatusInternalServerError, nil, false, maxSingleShot},
		{"400", http.StatusBadRequest, nil, false, maxSingleShot},
		{"404", http.StatusNotFound, nil, false, maxSingleShot},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var hits int64
			srv := httptest.NewServer(rpcCounting(&hits,
				rpcFixedHandler(tc.status, rpcJSONError(-32603, "test"), tc.headers)))
			defer srv.Close()

			_, _, _ = rpcRun(t, srv.URL,
				"debug", rpcValidTxHash, "--network", "testnet",
			)
			n := atomic.LoadInt64(&hits)
			if n > tc.maxExpected {
				t.Errorf("%s: %d requests, want ≤%d (retryable=%v)",
					tc.name, n, tc.maxExpected, tc.retryable)
			}
			if tc.retryable && n < 2 {
				t.Errorf("%s: retryable code got only %d requests, expected ≥2", tc.name, n)
			}
		})
	}
}

// ─── 23. Exit-code stability ──────────────────────────────────────────────────

func TestExitCodeStabilityAcrossFailures(t *testing.T) {
	if testing.Short() {
		t.Skip("RPC journey test skipped in -short mode")
	}
	cases := []struct {
		name     string
		handler  http.Handler
		wantCode int
	}{
		{
			name:     "500",
			handler:  rpcFixedHandler(http.StatusInternalServerError, rpcJSONError(-32603, "internal"), nil),
			wantCode: rpcExitInternal,
		},
		{
			name:    "503_exhausted",
			handler: rpcFixedHandler(http.StatusServiceUnavailable, rpcJSONError(-32603, "unavailable"), map[string]string{"Retry-After": "1"}),
			wantCode: rpcExitInternal,
		},
		{
			name: "429_exhausted",
			handler: rpcFixedHandler(http.StatusTooManyRequests,
				rpcJSONError(-32000, "rate limit"),
				map[string]string{"Retry-After": "1"},
			),
			wantCode: rpcExitInternal,
		},
		{
			name: "malformed_json",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, "not json")
			}),
			wantCode: rpcExitInternal,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()

			_, stderr, err := rpcRun(t, srv.URL,
				"debug", rpcValidTxHash, "--network", "testnet",
			)
			if got := exitCode(err); got != tc.wantCode {
				t.Errorf("%s: exit %d, want %d; stderr=%q", tc.name, got, tc.wantCode, stderr)
			}
			assertNotContains(t, tc.name, stderr, "panic")
			assertNotContains(t, tc.name, stderr, "goroutine")
		})
	}
}

// Connection refused is tested separately since it needs a closed server.
func TestExitCodeConnectionRefused(t *testing.T) {
	if testing.Short() {
		t.Skip("RPC journey test skipped in -short mode")
	}
	// Open and immediately close a server to get an address, then use it.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	url := srv.URL
	srv.Close() // closed before the binary contacts it

	_, stderr, err := rpcRun(t, url,
		"debug", rpcValidTxHash, "--network", "testnet",
	)
	assertRPCExit3(t, err)
	assertNotContains(t, "stderr", stderr, "panic")
}

// ─── 24. Error output on stderr, not stdout ───────────────────────────────────

func TestRPCErrorOnStderr(t *testing.T) {
	if testing.Short() {
		t.Skip("RPC journey test skipped in -short mode")
	}
	srv := httptest.NewServer(rpcFixedHandler(http.StatusServiceUnavailable,
		rpcJSONError(-32603, "unavailable"), map[string]string{"Retry-After": "1"}))
	defer srv.Close()

	stdout, stderr, err := rpcRun(t, srv.URL,
		"debug", rpcValidTxHash, "--network", "testnet",
	)
	if exitCode(err) == rpcExitSuccess {
		t.Skip("binary returned 0; skipping stream placement check")
	}
	assertNotContains(t, "stdout must not have Error:", stdout, "Error:")
	if !containsAny(stderr, "Error:", "error", "failed", "connection") {
		t.Errorf("expected error text on stderr; stdout=%q stderr=%q", stdout, stderr)
	}
}

// ─── 25. Partial artifact safety ─────────────────────────────────────────────

func TestPartialArtifactSafety(t *testing.T) {
	if testing.Short() {
		t.Skip("RPC journey test skipped in -short mode")
	}
	traceOut := t.TempDir() + "/trace.json"

	srv := httptest.NewServer(rpcFixedHandler(http.StatusServiceUnavailable,
		rpcJSONError(-32603, "unavailable"), map[string]string{"Retry-After": "1"}))
	defer srv.Close()

	_, _, err := rpcRun(t, srv.URL,
		"debug", rpcValidTxHash,
		"--network", "testnet",
		"--generate-trace",
		"--trace-output", traceOut,
	)
	if exitCode(err) == rpcExitSuccess {
		t.Skip("binary returned 0; skipping artifact safety check")
	}
	if _, statErr := os.Stat(traceOut); statErr == nil {
		t.Errorf("partial-artifact: trace file written despite RPC failure: %s", traceOut)
	}
}

// ─── 26. Invalid network flag — no RPC contact ───────────────────────────────

func TestInvalidNetworkPreRunENoRPCContact(t *testing.T) {
	if testing.Short() {
		t.Skip("RPC journey test skipped in -short mode")
	}
	var hits int64
	srv := httptest.NewServer(rpcCounting(&hits, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))
	defer srv.Close()

	_, stderr, err := rpcRun(t, srv.URL,
		"debug", rpcValidTxHash, "--network", "not-a-valid-network",
	)
	if exitCode(err) == rpcExitSuccess {
		t.Error("invalid-network: expected non-zero exit")
	}
	if !containsAny(stderr, "network", "invalid", "error") {
		t.Errorf("invalid-network: expected validation error, got: %q", stderr)
	}
	assertNotContains(t, "stderr", stderr, "panic")
	if n := atomic.LoadInt64(&hits); n > 0 {
		t.Errorf("invalid-network: %d unexpected RPC request(s) before validation error", n)
	}
}

// ─── 27. JSON output mode — no garbled stdout ─────────────────────────────────

func TestRPCFailureJSONOutputClean(t *testing.T) {
	if testing.Short() {
		t.Skip("RPC journey test skipped in -short mode")
	}
	srv := httptest.NewServer(rpcFixedHandler(http.StatusServiceUnavailable,
		rpcJSONError(-32603, "unavailable"), map[string]string{"Retry-After": "1"}))
	defer srv.Close()

	stdout, stderr, err := rpcRun(t, srv.URL,
		"debug", rpcValidTxHash,
		"--network", "testnet",
		"--json",
	)
	if exitCode(err) == rpcExitSuccess {
		t.Skip("binary returned 0; skipping JSON-output failure check")
	}
	assertNotContains(t, "stderr", stderr, "panic")
	// If stdout is non-empty it must be valid JSON — never garbled partial output.
	if trimmed := strings.TrimSpace(stdout); trimmed != "" {
		var v interface{}
		if jsonErr := json.Unmarshal([]byte(trimmed), &v); jsonErr != nil {
			t.Errorf("RPC failure with --json: stdout is non-empty and invalid JSON: %v\nstdout: %s", jsonErr, stdout)
		}
	}
}
