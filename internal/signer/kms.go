// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package signer

import (
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	mathrand "math/rand"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmsTypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	glassboxerrors "github.com/dotandev/glassbox/internal/errors"
	"github.com/dotandev/glassbox/internal/logger"
)

// KMSClient is the minimal subset of *kms.Client used by KMSSigner.
// Defining an interface lets tests inject a deterministic fake without
// pulling the entire AWS SDK into mock code paths. The production
// type (*kms.Client) satisfies this interface trivially; we verify at
// compile time below.
type KMSClient interface {
	Sign(ctx context.Context, in *kms.SignInput, opts ...func(*kms.Options)) (*kms.SignOutput, error)
	GetPublicKey(ctx context.Context, in *kms.GetPublicKeyInput, opts ...func(*kms.Options)) (*kms.GetPublicKeyOutput, error)
}

// Compile-time assertion that *kms.Client implements KMSClient.
var _ KMSClient = (*kms.Client)(nil)

// KMSAuditMetadata carries non-sensitive, auditable information about the
// KMS signing key and request context. It is intended to be embedded in
// signed audit envelopes (via SignedAuditLog.KeyOrigin) so operators can
// trace which key and region produced a given signature without accessing
// secret material.
//
// All fields are safe to log and store on disk. No private key material,
// PINs, or AWS credentials appear here.
type KMSAuditMetadata struct {
	// KeyRef is a safe, short identifier for the key — the last 8 characters
	// of the key ARN or alias.  The full ARN is NOT stored here; callers
	// that need to record the full ARN should pass it via CorrelationID or
	// write it to a separate, access-controlled audit store.
	KeyRef string `json:"key_ref"`
	// KeyIDHex is the hex-encoding of the configured key id (alias or ARN).
	// It binds audit logs to a specific configured key identity without
	// leaking the raw string in contexts where it would be visible.
	KeyIDHex string `json:"key_id_hex"`
	// Region is the AWS region in which the KMS key resides.
	Region string `json:"region"`
	// Algorithm is the KMS signing algorithm (e.g. "ECDSA_SHA_512").
	Algorithm string `json:"algorithm"`
	// CorrelationID is the caller-supplied tracing id passed to
	// SignWithMetadata.  Empty when the caller did not supply one.
	CorrelationID string `json:"correlation_id,omitempty"`
}

// AuditMetadata returns non-sensitive metadata about this KMS signer
// suitable for inclusion in signed audit envelopes and log records.
// The region is populated from the AWS config that was used to create
// the underlying KMS client.
func (s *KMSSigner) AuditMetadata(correlationID string) KMSAuditMetadata {
	return KMSAuditMetadata{
		KeyRef:        safeKeyIDRef(s.keyID),
		KeyIDHex:      s.keyIDHex,
		Region:        s.region,
		Algorithm:     s.Algorithm(),
		CorrelationID: correlationID,
	}
}

// KMSProvider implements SignerProvider for AWS KMS-backed signing.
// Keys are stored in AWS KMS and never exported, providing hardware-level
// security for audit log signatures.
type KMSProvider struct{}

// Ensure KMSProvider implements SignerProvider at compile time.
var _ SignerProvider = (*KMSProvider)(nil)

// Name returns the provider identifier for AWS KMS.
func (p *KMSProvider) Name() string {
	return "aws-kms"
}

// Description returns a human-readable description.
func (p *KMSProvider) Description() string {
	return "AWS Key Management Service (KMS) for audit log signing"
}

// Validate checks that the KMS configuration is valid.
func (p *KMSProvider) Validate(cfg ProviderConfig) error {
	keyID := cfg.Extra["kms_key_id"]
	if keyID == "" {
		return errors.New("AWS KMS provider requires kms_key_id in Extra config")
	}
	return nil
}

// Create instantiates a KMS-backed signer from the configuration.
//
// The SDK's own retryer is disabled via aws.NopRetryer because the
// KMSSigner owns the retry loop entirely (so per-attempt logging,
// classification, and idempotency remain in one place). Configure
// retry behaviour via GLASSBOX_KMS_* environment variables or the
// matching keys in ProviderConfig.Extra (see kmsExtraConfigKeys).
func (p *KMSProvider) Create(cfg ProviderConfig) (Signer, error) {
	keyID := cfg.Extra["kms_key_id"]
	region := cfg.Extra["kms_region"]
	if region == "" {
		region = "us-east-1"
	}

	retryCfg := buildKMSRetryConfig(cfg)

	// Build AWS config.
	awsCfg, err := buildAWSConfig(cfg, region)
	if err != nil {
		return nil, fmt.Errorf("failed to create AWS config: %w", err)
	}

	// Disable SDK-level retries; the KMSSigner owns the retry loop.
	kmsClient := kms.NewFromConfig(awsCfg, func(o *kms.Options) {
		o.Retryer = aws.NopRetryer{}
	})

	return &KMSSigner{
		client:    kmsClient,
		keyID:     keyID,
		keyIDHex:  hex.EncodeToString([]byte(keyID)),
		region:    region,
		retryCfg:  retryCfg,
		idemCache: newIdempotencyCache(retryCfg.IdempotencyMaxEntries, retryCfg.IdempotencyTTL),
		now:       time.Now,
		logFn:     defaultKMSLog(),
		randSrc:   mathrand.Float64,
	}, nil
}

// EnvVars returns the environment variables for KMS configuration.
func (p *KMSProvider) EnvVars() []EnvVarDoc {
	vars := []EnvVarDoc{
		{Name: "GLASSBOX_AWS_KMS_KEY_ID", Required: true, Description: "AWS KMS key ID (alias or ARN)"},
		{Name: "GLASSBOX_AWS_KMS_REGION", Required: false, Description: "AWS region (default: us-east-1)"},
		{Name: "AWS_ACCESS_KEY_ID", Required: false, Description: "AWS access key for authentication"},
		{Name: "AWS_SECRET_ACCESS_KEY", Required: false, Description: "AWS secret key for authentication"},
		{Name: "AWS_PROFILE", Required: false, Description: "AWS credential profile name"},
	}
	vars = append(vars, kmsRetryEnvVars()...)
	return vars
}

// KMSSigner implements Signer using AWS KMS with bounded retries and
// client-side idempotency (Issue 66).
//
// The Sign method preserves the legacy Signer interface (bytes in,
// bytes out + error). New callers that need visibility into retries
// or idempotency hits use SignWithMetadata.
type KMSSigner struct {
	client    KMSClient
	keyID     string
	keyIDHex  string
	region    string // AWS region, stored for AuditMetadata
	retryCfg  KMSRetryConfig
	idemCache *idempotencyCache
	now       func() time.Time
	logFn     logKMSSign
	randSrc   func() float64 // for testable backoff jitter
}

// Sign signs a message using the KMS key, applying retries and
// client-side idempotency. Equivalent to SignWithMetadata(ctx,
// message, "") with metadata discarded.
func (s *KMSSigner) Sign(message []byte) ([]byte, error) {
	meta, err := s.SignWithMetadata(context.Background(), message, "")
	if err != nil {
		return nil, err
	}
	return meta.Signature, nil
}

// SignWithMetadata signs message and returns both the signature and
// metadata describing what happened (attempts, idempotency hit, error
// class, elapsed time).
//
// Invariants — preserved by construction and asserted by tests:
//   - The Message bytes passed to the AWS SDK are computed once and
//     reused across every retry attempt; identical on every call.
//   - The AWS SDK's built-in retryer is disabled — KMSSigner owns the
//     retry loop entirely.
//   - On success the signature is stored in an in-memory
//     idempotency cache keyed by (key id, SHA-256(message)) with TTL;
//     a second call with the same message within the TTL hits the
//     cache and never reaches KMS.
//   - Logs only carry named scalar attributes (attempt count, error
//     code, error class, key id suffix, correlation id, elapsed time).
//     Message bytes, digest bytes, and signature bytes are never
//     included in any log call.
func (s *KMSSigner) SignWithMetadata(ctx context.Context, message []byte, correlationID string) (KMSSignMetadata, error) {
	if len(message) == 0 {
		return KMSSignMetadata{
			CorrelationID: correlationID,
			ErrorCode:     "EmptyMessage",
			ErrorClass:    "input",
		}, ErrEmptyMessage
	}

	start := s.now()
	meta := KMSSignMetadata{CorrelationID: correlationID}

	// Step 1 — idempotency lookup. The cache key binds key id to a
	// SHA-256 of the message so that switching key ids does not return
	// a cached signature for the wrong key, and so the lookup key
	// itself never contains the raw message bytes (helpful when the
	// key surfaces in error paths).
	cacheKey := idempotencyKey(s.keyID, message)
	if sig, hit := s.idemCache.get(cacheKey); hit {
		meta.Signature = sig
		meta.IdempotencyHit = true
		meta.Elapsed = s.now().Sub(start)
		s.logFn(logDebug, "kms sign idempotency cache hit",
			"correlation_id", correlationID,
			"key_ref", safeKeyIDRef(s.keyID),
		)
		return meta, nil
	}

	// Step 2 — compute the digest ONCE. Every retry sends the same
	// bytes to KMS; we never recompute or mutate the digest.
	digest := computeKMSDigest(message)

	backoff := s.retryCfg.InitialBackoff

	for attempt := 0; attempt <= s.retryCfg.MaxRetries; attempt++ {
		meta.Attempts = attempt + 1

		if attempt > 0 {
			s.logFn(logDebug, "kms sign retrying",
				"correlation_id", correlationID,
				"attempt", attempt+1,
				"max_attempts", s.retryCfg.MaxRetries+1,
				"backoff_ms", backoff.Milliseconds(),
				"key_ref", safeKeyIDRef(s.keyID),
			)
			if err := waitWithContext(ctx, backoff); err != nil {
				// Record the context-cancellation as a synthetic attempt entry.
				meta.SigningAttempts = append(meta.SigningAttempts, SigningAttempt{
					AttemptNumber: meta.Attempts,
					StartedAt:     s.now(),
					DurationMS:    0,
					ErrorCategory: "context",
					ErrorMessage:  err.Error(),
					IdempotencyKey: cacheKey,
				})
				meta.ErrorCode = "ContextCancelled"
				meta.ErrorClass = "context"
				meta.Elapsed = s.now().Sub(start)
				return meta, fmt.Errorf("%w: %v", ErrContextCancelled, err)
			}
		}

		// Record the start time of this specific KMS API call.
		attemptStart := s.now()
		sig, err := s.callSignOnce(ctx, digest)
		attemptDuration := s.now().Sub(attemptStart)

		if err == nil {
			// Successful attempt — record it then return.
			meta.SigningAttempts = append(meta.SigningAttempts, SigningAttempt{
				AttemptNumber:  meta.Attempts,
				StartedAt:      attemptStart,
				DurationMS:     attemptDuration.Milliseconds(),
				ErrorCategory:  "success",
				IdempotencyKey: cacheKey,
			})
			s.idemCache.put(cacheKey, sig)
			meta.Signature = sig
			meta.Elapsed = s.now().Sub(start)
			s.logFn(logDebug, "kms sign succeeded",
				"correlation_id", correlationID,
				"attempts", meta.Attempts,
				"key_ref", safeKeyIDRef(s.keyID),
				"elapsed_ms", meta.Elapsed.Milliseconds(),
			)
			return meta, nil
		}

		retryable, code, class := classifyKMSError(err)
		meta.Retryable = retryable
		meta.ErrorCode = code
		meta.ErrorClass = class

		// Record this failed attempt.
		meta.SigningAttempts = append(meta.SigningAttempts, SigningAttempt{
			AttemptNumber:  meta.Attempts,
			StartedAt:      attemptStart,
			DurationMS:     attemptDuration.Milliseconds(),
			ErrorCategory:  errorCategoryForAttempt(err, code, class),
			ErrorMessage:   err.Error(),
			IdempotencyKey: cacheKey,
		})

		s.logFn(logDebug, "kms sign attempt failed",
			"correlation_id", correlationID,
			"attempt", attempt+1,
			"error_code", code,
			"error_class", class,
			"retryable", retryable,
			"key_ref", safeKeyIDRef(s.keyID),
		)

		if !retryable {
			meta.Elapsed = s.now().Sub(start)
			// Map permanent authorization failures to a stable structured error
			// so callers can distinguish "permission denied" from "transient"
			// without parsing the error string [Issue #805].
			if isKMSUnauthorizedCode(code) {
				return meta, glassboxerrors.WrapKMSUnauthorized(code, safeKeyIDRef(s.keyID))
			}
			return meta, err
		}
		if attempt < s.retryCfg.MaxRetries {
			backoff = nextRetryBackoff(s.retryCfg, backoff, s.randSrc)
		}
	}

	meta.Elapsed = s.now().Sub(start)
	// Classify the exhaustion cause: throttling vs generic transient failure
	// so callers surface the right stable error code [Issue #805].
	var wrappedErr error
	if isKMSThrottlingCode(meta.ErrorCode) {
		wrappedErr = glassboxerrors.WrapKMSThrottled(meta.Attempts, correlationID)
	} else {
		wrappedErr = glassboxerrors.WrapKMSTransientFailure(meta.Attempts, meta.ErrorCode, correlationID)
	}
	return meta, wrappedErr
}

// callSignOnce invokes KMS once with the precomputed digest. Centralised
// so retry tests can mock at exactly this boundary.
func (s *KMSSigner) callSignOnce(ctx context.Context, digest []byte) ([]byte, error) {
	input := &kms.SignInput{
		KeyId:            &s.keyID,
		Message:          digest,
		MessageType:      kmsTypes.MessageTypeDigest,
		SigningAlgorithm: kmsTypesSigningAlgorithm,
	}
	result, err := s.client.Sign(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("KMS sign request failed: %w", err)
	}
	if result == nil || len(result.Signature) == 0 {
		return nil, errors.New("KMS sign returned empty signature")
	}
	return result.Signature, nil
}

// computeKMSDigest returns the SHA-512 of message. The KMSSigner hashes
// locally because KMS supports SIGN_DIGEST for both SHA-256 and SHA-512;
// using SHA-512 pairs with the ECDSA_SHA_512 signing algorithm used by
// this provider. The function never mutates the input.
func computeKMSDigest(message []byte) []byte {
	d := crypto.SHA512.New()
	d.Write(message) // Sha512.Write never mutates message.
	return d.Sum(nil)
}

// PublicKey returns the public key associated with the KMS key.
// This queries KMS for the public key on each call to ensure freshness.
func (s *KMSSigner) PublicKey() ([]byte, error) {
	input := &kms.GetPublicKeyInput{
		KeyId: &s.keyID,
	}

	result, err := s.client.GetPublicKey(context.Background(), input)
	if err != nil {
		return nil, fmt.Errorf("failed to get public key from KMS: %w", err)
	}

	return result.PublicKey, nil
}

// KeyID returns the KMS key ID used for signing.
func (s *KMSSigner) KeyID() string {
	return s.keyID
}

// Algorithm returns the signing algorithm.
func (s *KMSSigner) Algorithm() string {
	return "ECDSA_SHA_512"
}

// KeyOrigin returns non-sensitive metadata about the KMS signing key.
// The key fingerprint is derived from the KMS key ID.
func (s *KMSSigner) KeyOrigin() KeyOriginMetadata {
	return KeyOriginMetadata{
		Provider:       "aws-kms",
		Algorithm:      "ECDSA_SHA_512",
		KeyFingerprint: s.keyIDHex,
	}
}

// IdempotencyStats returns the current size of the in-memory idempotency
// cache. Exposed for diagnostics and tests; safe for concurrent use.
func (s *KMSSigner) IdempotencyStats() int {
	return s.idemCache.size()
}

// Close implements io.Closer. KMS doesn't require cleanup, but we implement
// this interface for consistency with other providers.
func (s *KMSSigner) Close() error {
	return nil
}

// kmsTypesSigningAlgorithm is the signing algorithm for KMS.
// AWS KMS doesn't support Ed25519 directly; ECDSA_SHA_512 uses P-521 curve with SHA-512
// which is the closest available option for SHA-512 based signatures.
var kmsTypesSigningAlgorithm = kmsTypes.SigningAlgorithmSpecEcdsaSha512

// RegisterKMSProvider registers the AWS KMS provider with the default registry.
// This is called automatically during package initialization if the AWS SDK
// is available.
func init() {
	// Register the KMS provider - this will replace any existing provider with the same name
	DefaultRegistry.Register(&KMSProvider{})
}

// kmsExtraConfigKeys documents the ProviderConfig.Extra keys recognised
// by the KMS provider (in addition to the AWS *cred/profile keys).
const (
	KMSExtraKeyMaxRetries       = "kms_max_retries"
	KMSExtraKeyInitialBackoffMs = "kms_initial_backoff_ms"
	KMSExtraKeyMaxBackoffMs     = "kms_max_backoff_ms"
	KMSExtraKeyJitterFraction   = "kms_jitter_fraction"
	KMSExtraKeyIdempotencyTTLMs = "kms_idempotency_ttl_ms"
	KMSExtraKeyIdempotencyMax   = "kms_idempotency_max"
)

// kmsRetryEnvVars returns EnvVarDoc entries documenting the retry
// knobs. Exposed via KMSProvider.EnvVars so the CLI can list them
// alongside the existing AWS_* variables.
func kmsRetryEnvVars() []EnvVarDoc {
	return []EnvVarDoc{
		{Name: "GLASSBOX_KMS_MAX_RETRIES",
			Required:    false,
			Description: "Bounded retry attempts on safe KMS failures; default 3, set 0 to disable retries."},
		{Name: "GLASSBOX_KMS_INITIAL_BACKOFF_MS",
			Required:    false,
			Description: "Initial backoff in ms before the first retry; default 250. Subsequent waits double."},
		{Name: "GLASSBOX_KMS_MAX_BACKOFF_MS",
			Required:    false,
			Description: "Upper bound for any single backoff interval in ms; default 5000."},
		{Name: "GLASSBOX_KMS_JITTER_FRACTION",
			Required:    false,
			Description: "Proportional jitter added to backoff (0 = none); default 0.2."},
		{Name: "GLASSBOX_KMS_IDEMPOTENCY_TTL_MS",
			Required:    false,
			Description: "Client-side signature cache TTL in ms; default 60000 (0 disables)."},
		{Name: "GLASSBOX_KMS_IDEMPOTENCY_MAX",
			Required:    false,
			Description: "Maximum entries in the idempotency LRU cache; default 1024."},
	}
}

// buildKMSRetryConfig returns the retry configuration for the KMS
// provider.
//
// Precedence (highest first): GLASSBOX_KMS_* environment variables,
// then ProviderConfig.Extra, then DefaultKMSRetryConfig. CLI flags
// surface through ProviderConfig.Extra when the CLI layer funnels
// them in, so a flag value can be overridden by a process-level
// environment variable if both are set — matching the conventional
// "env > config > default" ladder used elsewhere in Glassbox.
func buildKMSRetryConfig(cfg ProviderConfig) KMSRetryConfig {
	rc := DefaultKMSRetryConfig()

	if extra := cfg.Extra; extra != nil {
		if v, ok := extra[KMSExtraKeyMaxRetries]; ok {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				rc.MaxRetries = n
			}
		}
		if v, ok := extra[KMSExtraKeyInitialBackoffMs]; ok {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				rc.InitialBackoff = time.Duration(n) * time.Millisecond
			}
		}
		if v, ok := extra[KMSExtraKeyMaxBackoffMs]; ok {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				rc.MaxBackoff = time.Duration(n) * time.Millisecond
			}
		}
		if v, ok := extra[KMSExtraKeyJitterFraction]; ok {
			if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
				rc.JitterFraction = f
			}
		}
		if v, ok := extra[KMSExtraKeyIdempotencyTTLMs]; ok {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				rc.IdempotencyTTL = time.Duration(n) * time.Millisecond
			}
		}
		if v, ok := extra[KMSExtraKeyIdempotencyMax]; ok {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				rc.IdempotencyMaxEntries = n
			}
		}
	}

	if v := os.Getenv("GLASSBOX_KMS_MAX_RETRIES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			rc.MaxRetries = n
		}
	}
	if v := os.Getenv("GLASSBOX_KMS_INITIAL_BACKOFF_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			rc.InitialBackoff = time.Duration(n) * time.Millisecond
		}
	}
	if v := os.Getenv("GLASSBOX_KMS_MAX_BACKOFF_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			rc.MaxBackoff = time.Duration(n) * time.Millisecond
		}
	}
	if v := os.Getenv("GLASSBOX_KMS_JITTER_FRACTION"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
			rc.JitterFraction = f
		}
	}
	if v := os.Getenv("GLASSBOX_KMS_IDEMPOTENCY_TTL_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			rc.IdempotencyTTL = time.Duration(n) * time.Millisecond
		}
	}
	if v := os.Getenv("GLASSBOX_KMS_IDEMPOTENCY_MAX"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			rc.IdempotencyMaxEntries = n
		}
	}

	return rc
}

// logKMSSignToLogger forwards to the package logger. It is a thin
// shim kept in this file so tests in the same package can replace
// logFn without depending on internal/logger directly.
func logKMSSignToLogger(level logLevel, msg string, attrs ...any) {
	if logger.Logger == nil {
		return
	}
	switch level {
	case logDebug:
		logger.Logger.Debug(msg, attrs...)
	case logWarn:
		logger.Logger.Warn(msg, attrs...)
	default:
		logger.Logger.Debug(msg, attrs...)
	}
}

// buildAWSConfig creates an AWS config from provider config.
func buildAWSConfig(cfg ProviderConfig, region string) (aws.Config, error) {
	opts := []func(*config.LoadOptions) error{
		config.WithRegion(region),
	}

	// Check for explicit credentials in Extra config
	if accessKey := cfg.Extra["aws_access_key_id"]; accessKey != "" {
		secretKey := cfg.Extra["aws_secret_access_key"]
		if secretKey == "" {
			return aws.Config{}, errors.New("AWS access key provided but secret key missing")
		}
		opts = append(opts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		))
	} else if profile := cfg.Extra["aws_profile"]; profile != "" {
		// Use profile from config (takes precedence over env)
		opts = append(opts, config.WithSharedConfigProfile(profile))
	}
	// Otherwise, use default credential chain (env, config file, EC2 role, etc.)

	return config.LoadDefaultConfig(context.Background(), opts...)
}

// Ed25519Verify is a helper that verifies an Ed25519 signature.
// This is used for testing and verification.
func Ed25519Verify(publicKey ed25519.PublicKey, message, signature []byte) bool {
	return ed25519.Verify(publicKey, message, signature)
}

// GenerateTestKey generates a test Ed25519 key pair for development.
// This should not be used in production.
func GenerateTestKey() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	return pub, priv, err
}

// ValidateKeyID validates that a KMS key ID is properly formatted.
func ValidateKeyID(keyID string) error {
	if keyID == "" {
		return errors.New("KMS key ID cannot be empty")
	}

	// Check for alias format
	if strings.HasPrefix(keyID, "alias/") {
		return nil
	}

	// Check for ARN format
	if strings.HasPrefix(keyID, "arn:aws:kms:") {
		// Basic ARN validation
		parts := strings.Split(keyID, ":")
		if len(parts) < 6 {
			return errors.New("invalid KMS ARN format")
		}
		return nil
	}

	// Check for UUID format (key ID)
	if len(keyID) == 36 { // Standard UUID length
		return nil
	}

	return errors.New("KMS key ID must be an alias (alias/name), ARN, or key ID")
}

// kmsUnauthorizedCodes lists AWS KMS error codes that indicate a permanent
// authorization or key-state failure — retrying will not help [Issue #805].
var kmsUnauthorizedCodes = map[string]struct{}{
	"AccessDeniedException":           {},
	"InvalidGrantException":           {},
	"InvalidGrantTokenException":      {},
	"InvalidKeyUsageException":        {},
	"DisabledException":               {},
	"NotFoundException":               {},
	"InvalidKeyIdException":           {},
	"IncorrectKeyMaterialException":   {},
	"KMSInvalidStateException":        {},
	"IncorrectKeyException":           {},
	"DryRunOperationException":        {},
}

// isKMSUnauthorizedCode reports whether code is a permanent authorization
// or key-state failure.
func isKMSUnauthorizedCode(code string) bool {
	_, ok := kmsUnauthorizedCodes[code]
	return ok
}

// kmsThrottlingCodes lists AWS KMS error codes that indicate throttling.
var kmsThrottlingCodes = map[string]struct{}{
	"ThrottlingException":                    {},
	"TooManyRequests":                        {},
	"TooManyRequestsException":               {},
	"RequestLimitExceeded":                   {},
	"ProvisionedThroughputExceededException": {},
}

// isKMSThrottlingCode reports whether code represents a throttling failure.
func isKMSThrottlingCode(code string) bool {
	_, ok := kmsThrottlingCodes[code]
	return ok
}

// IdempotencyKeyFor returns the idempotency cache key that would be used
// for the given pre-hashed message.  Exposed so the dead-letter writer in
// cmd/audit_sign.go can embed the key in dead-letter entries without
// duplicating the key derivation logic.
func (s *KMSSigner) IdempotencyKeyFor(hash []byte) string {
	return idempotencyKey(s.keyID, hash)
}
