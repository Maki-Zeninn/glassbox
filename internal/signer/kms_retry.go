// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package signer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"time"

	"github.com/aws/smithy-go"
)

// KMSRetryConfig configures bounded retries and client-side idempotency
// for the AWS KMS-backed Signer.
//
// All fields are optional and have safe defaults; configure explicitly
// when running in environments known to be flaky or when stricter
// guarantees are required (e.g. shorter caps to bound worst-case latency).
type KMSRetryConfig struct {
	// MaxRetries bounds the total number of retry attempts on a
	// retryable error. 0 disables retries (single-shot, current
	// pre-Issue-66 behaviour).
	MaxRetries int

	// InitialBackoff is the wait before the first retry. Subsequent
	// waits double (exponential), capped at MaxBackoff, with optional
	// proportional jitter.
	InitialBackoff time.Duration

	// MaxBackoff is the upper bound for any single backoff interval.
	MaxBackoff time.Duration

	// JitterFraction is a multiplier (0 = none, 0.1 = ±10%) added to
	// each calculated backoff so concurrent retriers do not synchronise.
	JitterFraction float64

	// IdempotencyTTL caches a successful signature keyed by
	// (keyId, canonical digest) for this duration. 0 disables caching.
	IdempotencyTTL time.Duration

	// IdempotencyMaxEntries caps the in-memory cache size; entries are
	// evicted LRU once the cap is exceeded. 0 effectively disables
	// caching regardless of IdempotencyTTL.
	IdempotencyMaxEntries int
}

// DefaultKMSRetryConfig returns a conservative, production-friendly
// retry policy: at most 3 retry attempts with exponential backoff
// doubled from 250ms up to 5s and 20% jitter, plus a 60-second
// client-side idempotency window with a 1k-entry LRU cap.
//
// Tunables follow Issue 66 acceptance criteria: bounded retries on
// safe failures, identical digest across attempts, no payload logging
// (configurable only via env) and at most one KMS call per audit log
// even if callers race.
func DefaultKMSRetryConfig() KMSRetryConfig {
	return KMSRetryConfig{
		MaxRetries:            3,
		InitialBackoff:        250 * time.Millisecond,
		MaxBackoff:            5 * time.Second,
		JitterFraction:        0.2,
		IdempotencyTTL:        60 * time.Second,
		IdempotencyMaxEntries: 1024,
	}
}

// KMSSignMetadata captures the observable outcome of a single KMS Sign
// call. The classic Sign(data) returns only the bytes; SignWithMetadata
// exposes retry/idempotency details so callers (CLI, tests, telemetry)
// can introspect the path.
type KMSSignMetadata struct {
	// Signature is the produced bytes. Nil on error.
	Signature []byte

	// Attempts is the total number of KMS API calls (1 on success with
	// no retries, more after transient retries, 0 on idempotency hit).
	Attempts int

	// CorrelationID is the opaque id threaded through every attempt for
	// traceability. May be empty.
	CorrelationID string

	// IdempotencyHit is true when the signature was returned from the
	// in-memory cache without contacting KMS.
	IdempotencyHit bool

	// ErrorCode is the AWS API error code (or "NetworkError",
	// "ContextCancelled", "EmptyMessage") on failure. Empty on success.
	ErrorCode string

	// ErrorClass is a coarse category: "api", "network", "context",
	// "input", "unknown". Empty on success.
	ErrorClass string

	// Retryable reports whether the final error was classified as
	// retryable. Always false on success and on the very last attempt.
	Retryable bool

	// Elapsed is the wall-clock duration of the whole call (cache lookup
	// + retry loop). Useful for latency budgets and tests.
	Elapsed time.Duration

	// SigningAttempts holds per-attempt detail for every KMS API call
	// made during the retry loop.  It is included in the canonical
	// audit record JSON so reviewers can determine whether a signing
	// event was straightforward or required multiple attempts.
	// An idempotency cache hit produces no entries (no KMS calls were made).
	SigningAttempts []SigningAttempt
}

// SigningAttempt records the observable outcome of a single KMS Sign
// API call within the retry loop.  All entries — including the final
// successful one — appear in the array, allowing a compliance reviewer
// to reconstruct the full signing history without access to runtime logs.
//
// The error_category field uses a closed vocabulary:
//
//	"throttled"    — AWS KMS responded with a throttling error code.
//	"auth_failure" — permanent authorization or key-state failure; no retry.
//	"transient"    — retryable infrastructure error (InternalError, etc.).
//	"network"      — TCP/TLS failure classified as net.Error.
//	"success"      — the KMS API returned a valid signature.
//	"context"      — the caller's context was cancelled during a backoff.
//
// The idempotency_key field is the SHA-256-derived cache key used for this
// signing operation (never the raw message bytes or digest).
type SigningAttempt struct {
	// AttemptNumber is 1-based (first attempt = 1).
	AttemptNumber int `json:"attempt_number"`
	// StartedAt is the wall-clock time at which this specific KMS API
	// call was initiated (after any preceding backoff sleep).
	StartedAt time.Time `json:"started_at"`
	// DurationMS is how long the KMS API call itself took, in milliseconds.
	// For a successful attempt this is the round-trip to KMS. For a failed
	// attempt it includes the time spent waiting for the error response.
	DurationMS int64 `json:"duration_ms"`
	// ErrorCategory classifies the outcome. "success" for the winning call.
	ErrorCategory string `json:"error_category"`
	// ErrorMessage is the human-readable error string. Empty on success.
	ErrorMessage string `json:"error_message,omitempty"`
	// IdempotencyKey is the opaque cache key for this signing operation.
	// It is derived as "v1:<key-id>:<hex(sha256(message))>" and is safe
	// to include in audit records (it contains neither message bytes nor
	// the digest passed to KMS).
	IdempotencyKey string `json:"idempotency_key"`
}

// ErrEmptyMessage is returned by SignWithMetadata when the caller passes
// an empty input. AWS KMS rejects empty messages with a confusing
// "must not be empty" error; surfacing a typed sentinel lets callers
// pre-flight cheaply and tests assert on identity.
var ErrEmptyMessage = errors.New("kms signer: empty message")

// ErrContextCancelled is returned when the supplied context is done
// before all retry attempts complete. The underlying ctx.Err() is wrapped
// so callers still see the precise cancel cause.
var ErrContextCancelled = errors.New("kms signer: context cancelled")

// retryableKMSErrorCodes is the allowlist of AWS KMS error codes that
// are always safe to retry. Mirrors the SDK's retryable classification
// but is owned here so it can be asserted against in tests and matched
// deterministically regardless of SDK defaults.
var retryableKMSErrorCodes = map[string]struct{}{
	"InternalError":                          {},
	"ServiceUnavailable":                     {},
	"ThrottlingException":                    {},
	"TooManyRequests":                        {},
	"TooManyRequestsException":               {},
	"RequestTimeout":                         {},
	"RequestTimeoutException":                {},
	"KMSInternalException":                   {},
	"UnavailableException":                   {},
	"RequestLimitExceeded":                   {},
	"ProvisionedThroughputExceededException": {},
}

// isRetryableKMSCode returns true when the AWS SDK-reported code is on
// the safe-to-retry allowlist.
func isRetryableKMSCode(code string) bool {
	if code == "" {
		return false
	}
	_, ok := retryableKMSErrorCodes[code]
	return ok
}

// classifyKMSError inspects an SDK error and reports whether it can be
// retried, the SDK error code (or "NetworkError"), and a coarse class.
//
// The function never returns, logs, or buffers payload bytes — it only
// extracts the SDK-reported error code via smithy.APIError.
func classifyKMSError(err error) (retryable bool, code string, class string) {
	if err == nil {
		return false, "", ""
	}

	// Network-layer failures (timeouts, connection refused) are
	// retryable. Smithy wraps these into smithy.OperationError; we
	// unwrap to find a stdlib net.Error.
	var nerr net.Error
	if errors.As(err, &nerr) {
		return true, "NetworkError", "network"
	}

	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		code = apiErr.ErrorCode()
		return isRetryableKMSCode(code), code, "api"
	}

	// Unknown / opaque error: do not retry (safer default). Tests
	// assert this behaviour so future changes cannot silently widen
	// the retry surface.
	return false, "Unknown", "unknown"
}

// safeKeyIDRef returns a short, non-secret suffix of a KMS key id
// suitable for log lines. Full key ids are not secrets in AWS but are
// still identifiers; truncating makes log scraping less useful to an
// attacker who has access to logs but not the production credential
// store. Empty key ids are mapped to "(none)" so log lines are unambiguous.
func safeKeyIDRef(keyID string) string {
	if keyID == "" {
		return "(none)"
	}
	const suffixLen = 8
	if len(keyID) <= suffixLen {
		return keyID
	}
	return "..." + keyID[len(keyID)-suffixLen:]
}

// nextRetryBackoff returns the next wait duration using exponential
// backoff with optional proportional jitter, capped at cfg.MaxBackoff.
//
// When current is zero (the first call) the function returns
// cfg.InitialBackoff so the seeded value seeds the doubling curve
// instead of doubling on top of itself. Subsequent calls double the
// previous backoff, capped at cfg.MaxBackoff. Jitter = uniform *
// JitterFraction * next. Tests inject a deterministic jitter source to
// keep assertions stable.
func nextRetryBackoff(cfg KMSRetryConfig, current time.Duration, jitterSrc func() float64) time.Duration {
	if current <= 0 {
		return cfg.InitialBackoff
	}
	next := current * 2
	if next > cfg.MaxBackoff {
		next = cfg.MaxBackoff
	}
	if cfg.JitterFraction > 0 && jitterSrc != nil {
		j := jitterSrc()
		// Clamp jitter to [-1, 1] so callers can pass sources that
		// produce out-of-range values (e.g. [-1, 1] vs [0, 1]).
		if j < -1 {
			j = -1
		}
		if j > 1 {
			j = 1
		}
		delta := float64(next) * cfg.JitterFraction * j
		adj := float64(next) + delta
		if adj < 0 {
			adj = 0
		}
		return time.Duration(adj)
	}
	return next
}

// waitWithContext sleeps for d or returns ctx.Err() if the context is
// cancelled first.
func waitWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// idempotencyKey builds the cache key used by the in-memory idempotency
// cache. It mixes the key id (so changing key ids does not return a
// stale signature for the wrong key) and a SHA-256 of the message
// (so the full digest is never part of the key string, including in
// test logging). The "v1:" prefix lets us rev the key scheme later.
func idempotencyKey(keyID string, message []byte) string {
	sum := sha256.Sum256(message)
	return "v1:" + keyID + ":" + hex.EncodeToString(sum[:])
}

// logKMSSign is the structured-log hook used by KMSSigner. Tests inject
// a stub; production code uses logger.Logger.Debug by default. The
// signature intentionally takes a variadic any so we cannot
// accidentally accept the full message/digest — production callers only
// pass named attributes.
type logKMSSign func(level logLevel, msg string, attrs ...any)

type logLevel int

const (
	logDebug logLevel = iota
	logWarn
)

// defaultKMSLog returns the production log hook: debug-level events
// are routed to the package logger; warn-level events are routed too.
// The function intentionally does NOT take ctx — KMSRetryConfig is
// loggable without a parent context, and correlation ID flows via
// metadata attribute (already a string) rather than context value.
func defaultKMSLog() logKMSSign {
	// Lazy import to avoid a hard dependency on the logger package
	// for tests that wire a stub. internal/logger.Logger is initialised
	// at process start and is safe to use.
	return func(level logLevel, msg string, attrs ...any) {
		// Avoid pulling internal/logger into every test binary by
		// indirection. logger.Logger is *slog.Logger; we adapt here.
		logKMSSignToLogger(level, msg, attrs...)
	}
}

// errorCategoryForAttempt maps the outcome of a single retry attempt to the
// closed error_category vocabulary used in SigningAttempt.
// A nil error always maps to "success".
func errorCategoryForAttempt(err error, errorCode, errorClass string) string {
	if err == nil {
		return "success"
	}
	if errorClass == "context" {
		return "context"
	}
	if errorClass == "network" {
		return "network"
	}
	if isKMSUnauthorizedCode(errorCode) {
		return "auth_failure"
	}
	if isKMSThrottlingCode(errorCode) {
		return "throttled"
	}
	return "transient"
}
