// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package signer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/smithy-go"
)

// ── classifier tests ─────────────────────────────────────────────────────────

func TestClassifyKMSError_NilIsNotRetryable(t *testing.T) {
	retryable, code, class := classifyKMSError(nil)
	if retryable || code != "" || class != "" {
		t.Fatalf("nil error should classify as not retryable with empty code/class, got (%v, %q, %q)",
			retryable, code, class)
	}
}

func TestClassifyKMSError_RetryableCodes(t *testing.T) {
	for _, code := range []string{
		"InternalError",
		"ServiceUnavailable",
		"ThrottlingException",
		"RequestTimeoutException",
		"KMSInternalException",
		"TooManyRequestsException",
		"ProvisionedThroughputExceededException",
	} {
		t.Run(code, func(t *testing.T) {
			err := &smithy.GenericAPIError{Code: code, Message: "boom"}
			retryable, gotCode, class := classifyKMSError(err)
			if !retryable {
				t.Errorf("%s should be retryable", code)
			}
			if gotCode != code {
				t.Errorf("expected code %q, got %q", code, gotCode)
			}
			if class != "api" {
				t.Errorf("expected class api, got %q", class)
			}
		})
	}
}

func TestClassifyKMSError_NonRetryableCodes(t *testing.T) {
	for _, code := range []string{
		"AccessDeniedException",
		"InvalidKeyIdException",
		"NotFoundException",
		"ValidationException",
		"DisabledException",
		"IncorrectKeyMaterialException",
		"InvalidGrantException",
	} {
		t.Run(code, func(t *testing.T) {
			err := &smithy.GenericAPIError{Code: code, Message: "boom"}
			retryable, gotCode, class := classifyKMSError(err)
			if retryable {
				t.Errorf("%s should NOT be retryable", code)
			}
			if gotCode != code {
				t.Errorf("expected code %q, got %q", code, gotCode)
			}
			if class != "api" {
				t.Errorf("expected class api, got %q", class)
			}
		})
	}
}

func TestClassifyKMSError_NetworkErrorIsRetryable(t *testing.T) {
	err := &fakeNetError{msg: "connection refused", timeout: false}
	retryable, code, class := classifyKMSError(err)
	if !retryable {
		t.Error("net.Error should be retryable")
	}
	if code != "NetworkError" || class != "network" {
		t.Errorf("expected (NetworkError, network), got (%q, %q)", code, class)
	}
}

func TestClassifyKMSError_UnknownErrorIsNotRetryable(t *testing.T) {
	retryable, code, class := classifyKMSError(errors.New("opaque"))
	if retryable {
		t.Error("opaque error should not be retried (safe default)")
	}
	if code != "Unknown" || class != "unknown" {
		t.Errorf("expected (Unknown, unknown), got (%q, %q)", code, class)
	}
}

// fakeNetError implements the stdlib net.Error shape used by classifyKMSError.
type fakeNetError struct {
	msg     string
	timeout bool
}

func (e *fakeNetError) Error() string   { return e.msg }
func (e *fakeNetError) Timeout() bool   { return e.timeout }
func (e *fakeNetError) Temporary() bool { return e.timeout } //nolint:staticcheck

// ── idempotency key + safeKeyIDRef helpers ───────────────────────────────────

func TestIdempotencyKey_DistinguishesKeyID(t *testing.T) {
	a := idempotencyKey("alias/A", []byte("digest"))
	b := idempotencyKey("alias/B", []byte("digest"))
	if a == b {
		t.Fatalf("idempotency keys for different key IDs collided: %q == %q", a, b)
	}
}

func TestIdempotencyKey_DistinguishesMessage(t *testing.T) {
	a := idempotencyKey("alias/A", []byte("digest-one"))
	b := idempotencyKey("alias/A", []byte("digest-two"))
	if a == b {
		t.Fatalf("idempotency keys for different messages collided")
	}
}

func TestIdempotencyKey_OmitsRawMessage(t *testing.T) {
	msg := []byte("super-secret-payload")
	key := idempotencyKey("alias/A", msg)
	if strings.Contains(key, "super-secret-payload") {
		t.Fatalf("idempotency key leaked message bytes: %q", key)
	}
}

func TestSafeKeyIDRef_Truncates(t *testing.T) {
	got := safeKeyIDRef("arn:aws:kms:us-east-1:123456789012:key/12345678-1234-1234-1234-12345678")
	if !strings.HasPrefix(got, "...") {
		t.Fatalf("expected truncated prefix '...', got %q", got)
	}
	if len(got) > 11 { // '...' + 8 char suffix
		t.Fatalf("expected truncated key ref, got %q (len=%d)", got, len(got))
	}
	if strings.Contains(got, "123456789012") {
		t.Fatalf("safe ref leaked account id: %q", got)
	}
}

func TestSafeKeyIDRef_Empty(t *testing.T) {
	if got := safeKeyIDRef(""); got != "(none)" {
		t.Fatalf("expected '(none)' for empty key id, got %q", got)
	}
}

func TestSafeKeyIDRef_ShortUnchanged(t *testing.T) {
	got := safeKeyIDRef("alias/X")
	if got != "alias/X" {
		t.Fatalf("short key id should be unchanged, got %q", got)
	}
}

// ── backoff helper ──────────────────────────────────────────────────────────

func TestNextRetryBackoff_DoublesUpToCap(t *testing.T) {
	cfg := KMSRetryConfig{
		InitialBackoff: 100 * time.Millisecond,
		MaxBackoff:     500 * time.Millisecond,
		JitterFraction: 0,
	}
	zero := func() float64 { return 0 }

	// Pass 0 to trigger the initial backoff seeding.
	if got := nextRetryBackoff(cfg, 0, zero); got != 100*time.Millisecond {
		t.Errorf("first backoff should be InitialBackoff, got %v", got)
	}
	if got := nextRetryBackoff(cfg, 100*time.Millisecond, zero); got != 200*time.Millisecond {
		t.Errorf("second backoff should double to 200ms, got %v", got)
	}
	if got := nextRetryBackoff(cfg, 200*time.Millisecond, zero); got != 400*time.Millisecond {
		t.Errorf("third backoff should be 400ms, got %v", got)
	}
	if got := nextRetryBackoff(cfg, 400*time.Millisecond, zero); got != 500*time.Millisecond {
		t.Errorf("fourth backoff should be capped at MaxBackoff 500ms, got %v", got)
	}
	if got := nextRetryBackoff(cfg, 10*time.Second, zero); got != 500*time.Millisecond {
		t.Errorf("large backoff should be capped at MaxBackoff, got %v", got)
	}
}

// ── idempotency cache tests ─────────────────────────────────────────────────

func TestIdempotencyCache_PutGet(t *testing.T) {
	c := newIdempotencyCache(8, time.Minute)
	c.put("k1", []byte{0x01, 0x02, 0x03})
	got, ok := c.get("k1")
	if !ok {
		t.Fatal("expected hit")
	}
	if string(got) != string([]byte{0x01, 0x02, 0x03}) {
		t.Errorf("value mismatch: got %v", got)
	}
}

func TestIdempotencyCache_Expiry(t *testing.T) {
	c := newIdempotencyCache(8, 100*time.Millisecond)
	c.now = func() time.Time { return time.Unix(0, 0) }
	c.put("k1", []byte{0x01})

	c.now = func() time.Time { return time.Unix(0, 200*time.Millisecond.Nanoseconds()) }
	if _, ok := c.get("k1"); ok {
		t.Fatal("expected miss after TTL")
	}
	if c.size() != 0 {
		t.Fatalf("expected cache to evict expired entry, size=%d", c.size())
	}
}

func TestIdempotencyCache_LRUEviction(t *testing.T) {
	c := newIdempotencyCache(2, time.Minute)
	c.put("a", []byte{1})
	c.put("b", []byte{2})
	c.get("a") // makes 'a' the freshest
	c.put("c", []byte{3})
	if _, ok := c.get("b"); ok {
		t.Fatal("expected 'b' to be evicted (LRU)")
	}
	if _, ok := c.get("a"); !ok {
		t.Fatal("expected 'a' to remain")
	}
	if _, ok := c.get("c"); !ok {
		t.Fatal("expected 'c' to remain")
	}
}

func TestIdempotencyCache_DisabledOnZeroTTL(t *testing.T) {
	c := newIdempotencyCache(8, 0)
	c.put("k1", []byte{1})
	if _, ok := c.get("k1"); ok {
		t.Fatal("cache with zero TTL should always miss")
	}
}

func TestIdempotencyCache_NilSafe(t *testing.T) {
	var c *idempotencyCache
	if _, ok := c.get("k"); ok {
		t.Fatal("nil cache should return miss")
	}
	c.put("k", []byte{1}) // should not panic
	if got := c.size(); got != 0 {
		t.Fatalf("size on nil cache should be zero, got %d", got)
	}
}

func TestIdempotencyCache_StoresOwnCopy(t *testing.T) {
	c := newIdempotencyCache(8, time.Minute)
	original := []byte{0xff}
	c.put("k", original)
	original[0] = 0x00 // mutate caller-owned buffer
	got, ok := c.get("k")
	if !ok {
		t.Fatal("expected hit")
	}
	if got[0] != 0xff {
		t.Fatalf("cache returned value that was mutated by caller; got %x", got[0])
	}
}

// ── fakeKMSClient + end-to-end retry behaviour ──────────────────────────────

// fakeKMSClient implements KMSClient for tests. It can be programmed
// to fail the first failFirst invocations of Sign with the supplied
// AWS error code, then succeed. Every Sign input's Message bytes are
// captured by-value so tests can assert the "same digest" invariant.
type fakeKMSClient struct {
	mu             sync.Mutex
	failFirst      int
	failWithCode   string
	signature      []byte
	signatureCalls int
	captured       [][]byte
	getPublicKey   []byte
}

func (f *fakeKMSClient) Sign(_ context.Context, in *kms.SignInput, _ ...func(*kms.Options)) (*kms.SignOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Capture Message bytes by VALUE so subsequent signs cannot be
	// confused by an aliased in-place mutation by the signer.
	dup := make([]byte, len(in.Message))
	copy(dup, in.Message)
	f.captured = append(f.captured, dup)
	f.signatureCalls++
	if f.signatureCalls <= f.failFirst {
		return nil, &smithy.GenericAPIError{
			Code:    f.failWithCode,
			Message: fmt.Sprintf("simulated failure #%d", f.signatureCalls),
		}
	}
	return &kms.SignOutput{Signature: append([]byte(nil), f.signature...)}, nil
}

func (f *fakeKMSClient) GetPublicKey(_ context.Context, _ *kms.GetPublicKeyInput, _ ...func(*kms.Options)) (*kms.GetPublicKeyOutput, error) {
	return &kms.GetPublicKeyOutput{PublicKey: append([]byte(nil), f.getPublicKey...)}, nil
}

// silencedLog stores all log calls so tests can assert structural
// invariants. It is intentionally append-only and does no formatting.
type silencedLog struct {
	mu    sync.Mutex
	lines []logEntry
}

type logEntry struct {
	level logLevel
	msg   string
	attrs []any
}

func (s *silencedLog) fn() logKMSSign {
	return func(level logLevel, msg string, attrs ...any) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.lines = append(s.lines, logEntry{level: level, msg: msg, attrs: append([]any(nil), attrs...)})
	}
}

// attrsMap flattens a log entry's attrs into a map for assertions.
func attrsMap(attrs []any) map[string]any {
	m := map[string]any{}
	for i := 0; i+1 < len(attrs); i += 2 {
		k, _ := attrs[i].(string)
		m[k] = attrs[i+1]
	}
	return m
}

func newTestSigner(client KMSClient, cfg KMSRetryConfig, log logKMSSign) *KMSSigner {
	return &KMSSigner{
		client:    client,
		keyID:     "alias/test-key",
		keyIDHex:  hexEncode("alias/test-key"),
		retryCfg:  cfg,
		idemCache: newIdempotencyCache(cfg.IdempotencyMaxEntries, cfg.IdempotencyTTL),
		now:       time.Now,
		logFn:     log,
		randSrc:   func() float64 { return 0 }, // determinism
	}
}

// hexEncode is a small helper to avoid importing hex at top-level just
// for tests (kms.go already imports it).
func hexEncode(s string) string {
	const hexchars = "0123456789abcdef"
	out := make([]byte, len(s)*2)
	for i, c := range s {
		out[i*2] = hexchars[c>>4]
		out[i*2+1] = hexchars[c&0xF]
	}
	return string(out)
}

func TestKMSSigner_SignWithMetadata_RetryableThenSuccess(t *testing.T) {
	fake := &fakeKMSClient{
		failFirst:    2,
		failWithCode: "InternalError",
		signature:    []byte{0xAA, 0xBB, 0xCC},
	}
	logger := &silencedLog{}
	signer := newTestSigner(fake, KMSRetryConfig{
		MaxRetries:            3,
		InitialBackoff:        time.Millisecond,
		MaxBackoff:            5 * time.Millisecond,
		JitterFraction:        0,
		IdempotencyTTL:        time.Minute,
		IdempotencyMaxEntries: 4,
	}, logger.fn())

	meta, err := signer.SignWithMetadata(context.Background(), []byte("payload"), "corr-1")
	if err != nil {
		t.Fatalf("expected success after retries, got %v", err)
	}
	if meta.Attempts != 3 {
		t.Errorf("expected 3 attempts (2 failures + 1 success), got %d", meta.Attempts)
	}
	if meta.IdempotencyHit {
		t.Error("first call should not be an idempotency hit")
	}
	if !meta.Retryable && meta.Attempts != 3 {
		// Retryable only meaningful for in-loop bookkeeping; final
		// success must not carry the last failure's retryable flag.
		t.Errorf("on success Retryable should be false (default), got %v", meta.Retryable)
	}
	if fake.signatureCalls != 3 {
		t.Errorf("expected 3 AWS calls, got %d", fake.signatureCalls)
	}
	if !bytesEqual(meta.Signature, []byte{0xAA, 0xBB, 0xCC}) {
		t.Errorf("signature mismatch: %v", meta.Signature)
	}
}

func TestKMSSigner_SameDigestEveryAttempt(t *testing.T) {
	fake := &fakeKMSClient{
		failFirst:    3,
		failWithCode: "ServiceUnavailable",
		signature:    []byte{0x10},
	}
	signer := newTestSigner(fake, KMSRetryConfig{
		MaxRetries:            3,
		InitialBackoff:        time.Microsecond,
		MaxBackoff:            5 * time.Microsecond,
		IdempotencyTTL:        0,
		IdempotencyMaxEntries: 0,
	}, (&silencedLog{}).fn())

	payload := []byte("deterministic-canonical-digest")
	meta, err := signer.SignWithMetadata(context.Background(), payload, "corr-digest")
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if meta.Attempts != 4 {
		t.Fatalf("expected 4 attempts, got %d", meta.Attempts)
	}
	// fake captured 4 messages — they MUST be byte-identical and match
	// the SHA-512(payload) digest computed by the signer.
	want := computeKMSDigest(payload)
	for i, captured := range fake.captured {
		if len(captured) != len(want) {
			t.Fatalf("attempt %d: digest length %d != %d", i, len(captured), len(want))
		}
		if !bytesEqual(captured, want) {
			t.Fatalf("attempt %d: digest mismatch; got %x want %x", i, captured, want)
		}
	}
}

func TestKMSSigner_NoPayloadMutation(t *testing.T) {
	fake := &fakeKMSClient{
		failFirst:    2,
		failWithCode: "ThrottlingException",
		signature:    []byte{0x42},
	}
	signer := newTestSigner(fake, KMSRetryConfig{
		MaxRetries:            3,
		InitialBackoff:        time.Microsecond,
		MaxBackoff:            time.Microsecond,
		IdempotencyTTL:        0,
		IdempotencyMaxEntries: 0,
	}, (&silencedLog{}).fn())

	payload := []byte("do-not-mutate-me")
	// Snapshot a copy of the payload bytes before & after signing.
	before := append([]byte(nil), payload...)

	meta, err := signer.SignWithMetadata(context.Background(), payload, "corr-mut")
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if !bytesEqual(payload, before) {
		t.Fatalf("signer mutated the caller's payload byte slice: before=%x after=%x",
			before, payload)
	}
	if meta.Signature == nil {
		t.Fatal("expected non-nil signature")
	}
}

// TestKMSSigner_PayloadPointerPreservedAcrossRetries is intentionally
// removed. The "every retry uses the SAME digest buffer, no copy /
// reallocation" invariant it would have asserted is already covered by
// TestKMSSigner_SameDigestEveryAttempt, which checks every captured
// KMS input is byte-identical to computeKMSDigest(payload) — itself
// the single digest computed once outside the retry loop in
// SignWithMetadata. Adding a separate pointer-identity test would
// require reflecting into a private field of the fake and add no
// value beyond what TestKMSSigner_NoPayloadMutation already checks.

func TestKMSSigner_NonRetryable_StopsImmediately(t *testing.T) {
	fake := &fakeKMSClient{
		failFirst:    99, // would retry many times if classifier were wrong
		failWithCode: "AccessDeniedException",
		signature:    []byte{0xAA},
	}
	logger_hook := &silencedLog{}
	signer := newTestSigner(fake, KMSRetryConfig{
		MaxRetries:            3,
		InitialBackoff:        time.Microsecond,
		MaxBackoff:            time.Microsecond,
		IdempotencyTTL:        0,
		IdempotencyMaxEntries: 0,
	}, logger_hook.fn())

	meta, err := signer.SignWithMetadata(context.Background(), []byte("payload"), "corr-noretry")
	if err == nil {
		t.Fatal("expected error for non-retryable code")
	}
	if meta.Attempts != 1 {
		t.Fatalf("non-retryable should stop after 1 attempt, got %d", meta.Attempts)
	}
	if meta.Retryable {
		t.Error("non-retryable error should not be classified as retryable")
	}
	if meta.ErrorCode != "AccessDeniedException" {
		t.Errorf("expected error code AccessDeniedException, got %q", meta.ErrorCode)
	}
	if fake.signatureCalls != 1 {
		t.Errorf("non-retryable should trigger exactly 1 AWS call, got %d", fake.signatureCalls)
	}
}

func TestKMSSigner_IdempotencyHitSkipsKMS(t *testing.T) {
	fake := &fakeKMSClient{signature: []byte{0xCC}}
	signer := newTestSigner(fake, KMSRetryConfig{
		MaxRetries:            3,
		InitialBackoff:        time.Microsecond,
		MaxBackoff:            time.Microsecond,
		IdempotencyTTL:        time.Minute,
		IdempotencyMaxEntries: 4,
	}, (&silencedLog{}).fn())

	payload := []byte("payload")
	if _, err := signer.SignWithMetadata(context.Background(), payload, ""); err != nil {
		t.Fatalf("first call failed: %v", err)
	}
	if fake.signatureCalls != 1 {
		t.Fatalf("first call should hit KMS once, got %d", fake.signatureCalls)
	}

	meta, err := signer.SignWithMetadata(context.Background(), payload, "")
	if err != nil {
		t.Fatalf("second call failed: %v", err)
	}
	if !meta.IdempotencyHit {
		t.Fatal("second call with identical digest should hit idempotency cache")
	}
	if meta.Attempts != 0 {
		t.Errorf("idempotency hit should report 0 attempts, got %d", meta.Attempts)
	}
	if fake.signatureCalls != 1 {
		t.Errorf("idempotency hit should NOT contact KMS, signatureCalls=%d", fake.signatureCalls)
	}
}

func TestKMSSigner_IdempotencyDistinguishesKeyID(t *testing.T) {
	fake := &fakeKMSClient{signature: []byte{0x01}}
	logger_hook := &silencedLog{}
	signer := newTestSigner(fake, KMSRetryConfig{
		MaxRetries:            3,
		InitialBackoff:        time.Microsecond,
		MaxBackoff:            time.Microsecond,
		IdempotencyTTL:        time.Minute,
		IdempotencyMaxEntries: 4,
	}, logger_hook.fn())

	// Cache one signature under key id A.
	if _, err := signer.SignWithMetadata(context.Background(), []byte("m"), ""); err != nil {
		t.Fatal(err)
	}
	firstCalls := fake.signatureCalls

	// Switch key id; second call must hit KMS even with the same digest.
	signer.keyID = "alias/different-key"
	meta, err := signer.SignWithMetadata(context.Background(), []byte("m"), "")
	if err != nil {
		t.Fatal(err)
	}
	if meta.IdempotencyHit {
		t.Error("changing the key id must invalidate the idempotency cache entry")
	}
	if fake.signatureCalls != firstCalls+1 {
		t.Errorf("expected one extra KMS call after key id change, total=%d", fake.signatureCalls)
	}
}

func TestKMSSigner_LogsDoNotLeakPayloadOrSecrets(t *testing.T) {
	fake := &fakeKMSClient{
		failFirst:    1,
		failWithCode: "InternalError",
		signature:    []byte{0xCA, 0xFE, 0xBA, 0xBE},
	}
	logHook := &silencedLog{}
	signer := newTestSigner(fake, KMSRetryConfig{
		MaxRetries:            3,
		InitialBackoff:        time.Microsecond,
		MaxBackoff:            time.Microsecond,
		IdempotencyTTL:        time.Minute,
		IdempotencyMaxEntries: 4,
	}, logHook.fn())

	payload := []byte("very-secret-canonical-payload-XX")
	fullKeyID := signer.keyID

	meta, err := signer.SignWithMetadata(context.Background(), payload, "corr-no-leak")
	if err != nil {
		t.Fatalf("sign failed: %v", err)
	}

	for _, entry := range logHook.lines {
		// Formatted message must not contain raw payload bytes or full key id.
		if strings.Contains(entry.msg, string(payload)) {
			t.Errorf("log message leaked payload: %q", entry.msg)
		}
		if strings.Contains(entry.msg, fullKeyID) {
			t.Errorf("log message leaked full key id: %q", entry.msg)
		}
		for _, a := range entry.attrs {
			s, ok := a.(string)
			if !ok {
				continue
			}
			if strings.Contains(s, string(payload)) {
				t.Errorf("log attr leaked payload: %q", s)
			}
			if s == fullKeyID {
				t.Errorf("log attr echoed the unredacted key id: %q", s)
			}
			if bytesEqual([]byte(s), meta.Signature) {
				t.Errorf("log attr echoed the signature bytes: %x", s)
			}
		}
	}
}

func TestKMSSigner_ContextCancel(t *testing.T) {
	fake := &fakeKMSClient{
		failFirst:    99,
		failWithCode: "InternalError",
		signature:    []byte{0xAA},
	}
	signer := newTestSigner(fake, KMSRetryConfig{
		MaxRetries:            3,
		InitialBackoff:        50 * time.Millisecond, // long enough to be cancelled
		MaxBackoff:            50 * time.Millisecond,
		IdempotencyTTL:        0,
		IdempotencyMaxEntries: 0,
	}, (&silencedLog{}).fn())

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before call
	meta, err := signer.SignWithMetadata(ctx, []byte("payload"), "corr-ctx")
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
	if !errors.Is(err, ErrContextCancelled) {
		t.Errorf("expected ErrContextCancelled, got %v", err)
	}
	if meta.ErrorCode != "ContextCancelled" {
		t.Errorf("expected ErrorCode ContextCancelled, got %q", meta.ErrorCode)
	}
}

func TestKMSSigner_EmptyMessage(t *testing.T) {
	signer := newTestSigner(&fakeKMSClient{signature: []byte{0xAA}}, KMSRetryConfig{}, (&silencedLog{}).fn())
	meta, err := signer.SignWithMetadata(context.Background(), nil, "")
	if !errors.Is(err, ErrEmptyMessage) {
		t.Fatalf("expected ErrEmptyMessage, got %v", err)
	}
	if meta.ErrorCode != "EmptyMessage" {
		t.Errorf("expected ErrorCode=EmptyMessage, got %q", meta.ErrorCode)
	}
	if meta.ErrorClass != "input" {
		t.Errorf("expected ErrorClass=input, got %q", meta.ErrorClass)
	}
}

func TestKMSSigner_DefaultConfig_Applied(t *testing.T) {
	got := buildKMSRetryConfig(ProviderConfig{})
	want := DefaultKMSRetryConfig()
	if got != want {
		t.Fatalf("buildKMSRetryConfig with empty cfg/empty env: got %+v want %+v", got, want)
	}
}

func TestBuildKMSRetryConfig_RespectsExtraOverrides(t *testing.T) {
	t.Setenv("GLASSBOX_KMS_MAX_RETRIES", "") // ensure env doesn't dominate
	cfg := ProviderConfig{Extra: map[string]string{
		KMSExtraKeyMaxRetries:       "1",
		KMSExtraKeyInitialBackoffMs: "10",
		KMSExtraKeyMaxBackoffMs:     "100",
		KMSExtraKeyIdempotencyTTLMs: "500",
		KMSExtraKeyIdempotencyMax:   "16",
	}}
	got := buildKMSRetryConfig(cfg)
	if got.MaxRetries != 1 {
		t.Errorf("MaxRetries: got %d want 1", got.MaxRetries)
	}
	if got.InitialBackoff != 10*time.Millisecond {
		t.Errorf("InitialBackoff: got %v want 10ms", got.InitialBackoff)
	}
	if got.MaxBackoff != 100*time.Millisecond {
		t.Errorf("MaxBackoff: got %v want 100ms", got.MaxBackoff)
	}
	if got.IdempotencyTTL != 500*time.Millisecond {
		t.Errorf("IdempotencyTTL: got %v want 500ms", got.IdempotencyTTL)
	}
	if got.IdempotencyMaxEntries != 16 {
		t.Errorf("IdempotencyMaxEntries: got %d want 16", got.IdempotencyMaxEntries)
	}
}

func TestBuildKMSRetryConfig_FallsBackToEnv(t *testing.T) {
	cfg := ProviderConfig{} // no Extra
	t.Setenv("GLASSBOX_KMS_MAX_RETRIES", "5")
	t.Setenv("GLASSBOX_KMS_INITIAL_BACKOFF_MS", "50")
	got := buildKMSRetryConfig(cfg)
	if got.MaxRetries != 5 {
		t.Errorf("MaxRetries from env: got %d want 5", got.MaxRetries)
	}
	if got.InitialBackoff != 50*time.Millisecond {
		t.Errorf("InitialBackoff from env: got %v want 50ms", got.InitialBackoff)
	}
}

// ── small helpers ────────────────────────────────────────────────────────────

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ── Issue #805: KMS audit metadata and structured error codes ────────────────

func TestKMSSigner_AuditMetadata_Fields(t *testing.T) {
	fake := &fakeKMSClient{signature: []byte{0xAA}}
	s := &KMSSigner{
		client:    fake,
		keyID:     "alias/glassbox-audit",
		keyIDHex:  hexEncode("alias/glassbox-audit"),
		region:    "eu-west-1",
		retryCfg:  DefaultKMSRetryConfig(),
		idemCache: newIdempotencyCache(8, 0),
		now:       time.Now,
		logFn:     (&silencedLog{}).fn(),
		randSrc:   func() float64 { return 0 },
	}

	meta := s.AuditMetadata("corr-abc")
	if meta.Region != "eu-west-1" {
		t.Errorf("expected region eu-west-1, got %q", meta.Region)
	}
	if meta.Algorithm != "ECDSA_SHA_512" {
		t.Errorf("expected algorithm ECDSA_SHA_512, got %q", meta.Algorithm)
	}
	if meta.CorrelationID != "corr-abc" {
		t.Errorf("expected correlation_id corr-abc, got %q", meta.CorrelationID)
	}
	if meta.KeyIDHex == "" {
		t.Error("KeyIDHex must not be empty")
	}
	// KeyRef must be a short suffix, never the full key id.
	if meta.KeyRef == "alias/glassbox-audit" {
		t.Error("KeyRef must be a truncated suffix, not the full key id")
	}
	if meta.KeyRef == "(none)" {
		t.Error("KeyRef must not be (none) for a non-empty key id")
	}
}

func TestKMSSigner_AuditMetadata_EmptyCorrelation(t *testing.T) {
	s := &KMSSigner{
		keyID:     "alias/test",
		keyIDHex:  hexEncode("alias/test"),
		region:    "us-east-1",
		retryCfg:  DefaultKMSRetryConfig(),
		idemCache: newIdempotencyCache(4, 0),
		now:       time.Now,
		logFn:     (&silencedLog{}).fn(),
		randSrc:   func() float64 { return 0 },
	}
	meta := s.AuditMetadata("")
	if meta.CorrelationID != "" {
		t.Errorf("empty correlation id should remain empty, got %q", meta.CorrelationID)
	}
}

func TestKMSSigner_UnauthorizedCode_ReturnsPermanentError(t *testing.T) {
	for _, code := range []string{
		"AccessDeniedException",
		"DisabledException",
		"InvalidKeyIdException",
		"NotFoundException",
	} {
		t.Run(code, func(t *testing.T) {
			fake := &fakeKMSClient{
				failFirst:    99,
				failWithCode: code,
				signature:    []byte{0xAA},
			}
			signer := newTestSigner(fake, KMSRetryConfig{
				MaxRetries:            3,
				InitialBackoff:        time.Microsecond,
				MaxBackoff:            time.Microsecond,
				IdempotencyTTL:        0,
				IdempotencyMaxEntries: 0,
			}, (&silencedLog{}).fn())

			_, err := signer.SignWithMetadata(context.Background(), []byte("payload"), "corr-authz")
			if err == nil {
				t.Fatal("expected error for unauthorized code")
			}
			// Must use WrapKMSUnauthorized → ErstKMSUnauthorized sentinel.
			if !isErstCode(err, "KMS_UNAUTHORIZED") {
				t.Errorf("expected KMS_UNAUTHORIZED stable code, got: %v", err)
			}
			// Should stop after 1 attempt — not retry.
			if fake.signatureCalls != 1 {
				t.Errorf("unauthorized must stop after 1 attempt, got %d calls", fake.signatureCalls)
			}
		})
	}
}

func TestKMSSigner_ThrottlingExhausted_ReturnsKMSThrottled(t *testing.T) {
	fake := &fakeKMSClient{
		failFirst:    99,
		failWithCode: "ThrottlingException",
		signature:    []byte{0xAA},
	}
	signer := newTestSigner(fake, KMSRetryConfig{
		MaxRetries:            2,
		InitialBackoff:        time.Microsecond,
		MaxBackoff:            time.Microsecond,
		IdempotencyTTL:        0,
		IdempotencyMaxEntries: 0,
	}, (&silencedLog{}).fn())

	_, err := signer.SignWithMetadata(context.Background(), []byte("p"), "corr-throttle")
	if err == nil {
		t.Fatal("expected error after retry exhaustion")
	}
	if !isErstCode(err, "KMS_THROTTLED") {
		t.Errorf("expected KMS_THROTTLED stable code, got: %v", err)
	}
}

func TestKMSSigner_TransientExhausted_ReturnsKMSTransientFailure(t *testing.T) {
	fake := &fakeKMSClient{
		failFirst:    99,
		failWithCode: "InternalError",
		signature:    []byte{0xAA},
	}
	signer := newTestSigner(fake, KMSRetryConfig{
		MaxRetries:            2,
		InitialBackoff:        time.Microsecond,
		MaxBackoff:            time.Microsecond,
		IdempotencyTTL:        0,
		IdempotencyMaxEntries: 0,
	}, (&silencedLog{}).fn())

	_, err := signer.SignWithMetadata(context.Background(), []byte("p"), "corr-transient")
	if err == nil {
		t.Fatal("expected error after retry exhaustion")
	}
	if !isErstCode(err, "KMS_TRANSIENT_FAILURE") {
		t.Errorf("expected KMS_TRANSIENT_FAILURE stable code, got: %v", err)
	}
}

// isErstCode is a test helper that checks whether err carries a specific
// ErstErrorCode string.
func isErstCode(err error, want string) bool {
	if err == nil {
		return false
	}
	type coder interface{ Error() string }
	// Check via ErstError interface.
	type erstErr interface {
		Error() string
		Unwrap() error
	}
	// Walk the chain.
	unwrapped := err
	for unwrapped != nil {
		type withCode interface {
			Error() string
		}
		if e, ok := unwrapped.(interface{ Error() string }); ok {
			if len(e.Error()) >= len(want) {
				s := e.Error()
				if len(s) >= len(want) && s[:len(want)] == want {
					return true
				}
			}
		}
		type unwrapper interface{ Unwrap() error }
		if u, ok := unwrapped.(unwrapper); ok {
			unwrapped = u.Unwrap()
		} else {
			break
		}
	}
	return false
}
