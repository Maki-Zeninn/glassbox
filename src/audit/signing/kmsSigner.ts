// Copyright (c) 2026 dotandev
// SPDX-License-Identifier: MIT OR Apache-2.0

import * as crypto from "crypto";
import type { AuditSigner, PublicKey, Signature } from "./types";
import {
  KmsContextCancelledError,
  KmsEmptyMessageError,
  classifyKmsError,
  defaultKmsLog,
  defaultKmsRetryConfig,
  idempotencyKey,
  loadKmsRetryConfigFromEnv,
  nextRetryBackoffMs,
  safeKeyIdRef,
  sleepWithAbort,
  IdempotencyCache,
  type KmsLogFunction,
  type KmsRetryConfig,
  type SignMetadata,
} from "./kmsRetry";

/**
 * Result wrapper so callers can introspect retry/idempotency metadata
 * even when the underlying call eventually throws. The legacy
 * AuditSigner.sign() interface returns just the signature; this richer
 * shape is exposed via signWithMetadata (returns) and signWithMetadata
 * (rejects) — when rejected, the rejection's `.meta` property carries
 * the same metadata a success path would have populated.
 */
export interface SignResult {
  signature?: Buffer;
  meta: SignMetadata;
}

export class KmsSignError extends Error {
  constructor(public readonly meta: SignMetadata, message: string, public readonly cause?: unknown) {
    super(message);
    this.name = "KmsSignError";
  }
}

/**
 * AWS KMS-backed signer for audit trail signing.
 *
 * Uses an asymmetric KMS key (ECC_NIST_P256 or RSA) to sign payloads natively
 * via the KMS Sign API, without routing through a PKCS#11 abstraction layer.
 *
 * Required environment variables:
 *   GLASSBOX_KMS_KEY_ID       - KMS key ID or ARN of the signing key
 *   AWS_REGION                - AWS region where the key resides
 *
 * Optional environment variables:
 *   GLASSBOX_KMS_SIGNING_ALGORITHM - KMS signing algorithm (default: ECDSA_SHA_256)
 *                                Supported values:
 *                                  RSASSA_PSS_SHA_256 | RSASSA_PSS_SHA_384 | RSASSA_PSS_SHA_512
 *                                  RSASSA_PKCS1_V1_5_SHA_256 | RSASSA_PKCS1_V1_5_SHA_384 | RSASSA_PKCS1_V1_5_SHA_512
 *                                  ECDSA_SHA_256 | ECDSA_SHA_384 | ECDSA_SHA_512
 *   GLASSBOX_KMS_MAX_RETRIES          - bounded retry attempts (default 3)
 *   GLASSBOX_KMS_INITIAL_BACKOFF_MS   - first backoff in ms (default 250)
 *   GLASSBOX_KMS_MAX_BACKOFF_MS       - max single backoff in ms (default 5000)
 *   GLASSBOX_KMS_JITTER_FRACTION      - proportional jitter (default 0.2)
 *   GLASSBOX_KMS_IDEMPOTENCY_TTL_MS   - signature cache TTL in ms (default 60000)
 *   GLASSBOX_KMS_IDEMPOTENCY_MAX      - idempotency LRU cap (default 1024)
 *
 * AWS credentials are resolved via the standard credential provider chain
 * (environment variables, shared credentials file, EC2/ECS instance metadata, etc.).
 *
 * Note: KMS does not support Ed25519 keys for asymmetric signing as of 2026.
 * If your policy requires Ed25519, use the PKCS#11 or software signer instead.
 */
export class KmsSigner implements AuditSigner {
  private readonly keyId: string;
  private readonly signingAlgorithm: string;
  private readonly region: string;
  private readonly retryConfig: KmsRetryConfig;
  private readonly idempotencyCache: IdempotencyCache;
  private readonly logFn: KmsLogFunction;
  private readonly jitterUnit: () => number;

  // Lazy-loaded KMS client. Loaded once on first use.
  private client: any | undefined;
  // Optional test override of the KMS module; see loadKmsModule.
  private readonly moduleOverride?: {
    KMSClient: any;
    SignCommand: any;
    GetPublicKeyCommand: any;
  };

  constructor(
    opts?: {
      keyId?: string;
      signingAlgorithm?: string;
      region?: string;
      retryConfig?: Partial<KmsRetryConfig>;
      logFn?: KmsLogFunction;
      jitterUnit?: () => number;
      now?: () => number;
      kmsModuleOverride?: {
        KMSClient: any;
        SignCommand: any;
        GetPublicKeyCommand: any;
      };
    },
  ) {
    const keyId = opts?.keyId ?? process.env.GLASSBOX_KMS_KEY_ID;
    if (!keyId) {
      throw new Error("KMS signer: GLASSBOX_KMS_KEY_ID is required");
    }

    const region = opts?.region ?? process.env.AWS_REGION;
    if (!region) {
      throw new Error("KMS signer: AWS_REGION is required");
    }

    this.keyId = keyId;
    this.region = region;
    this.signingAlgorithm =
      opts?.signingAlgorithm ??
      process.env.GLASSBOX_KMS_SIGNING_ALGORITHM ??
      "ECDSA_SHA_256";

    // Apply in-process overrides first, then env, then defaults.
    const base = defaultKmsRetryConfig();
    const fromEnv = loadKmsRetryConfigFromEnv(base);
    this.retryConfig = { ...fromEnv, ...(opts?.retryConfig ?? {}) };

    this.idempotencyCache = new IdempotencyCache(
      this.retryConfig.idempotencyMaxEntries,
      this.retryConfig.idempotencyTtlMs,
      opts?.now ?? Date.now,
    );
    this.logFn = opts?.logFn ?? defaultKmsLog();
    this.jitterUnit = opts?.jitterUnit ?? Math.random;
    this.moduleOverride = opts?.kmsModuleOverride;
  }

  /**
   * Signs the payload using AWS KMS Sign API with bounded retries and
   * a client-side idempotency cache. Behaviour:
   *
   *   - The Message byte buffer passed to the SDK is built ONCE and
   *     reused across every retry attempt — retries never recompute
   *     or mutate the digest.
   *   - On success the (key id, SHA-256(message)) pair is remembered
   *     for the configured TTL; a second call with the same message
   *     hits the cache and never reaches KMS.
   *   - Logs only carry scalar attributes (attempt count, error code,
   *     error class, key id suffix, correlation id, elapsed ms). The
   *     audit payload, digest bytes and signature bytes are NEVER
   *     logged.
   *
   * Effective configurable knobs live in GLASSBOX_KMS_* env vars
   * (see file header) and can be overridden per-instance via the
   * `retryConfig` constructor option.
   */
  async sign(payload: Uint8Array): Promise<Signature> {
    try {
      const result = await this.signWithMetadata(payload, {});
      if (!result.signature) {
        throw new Error(`KMS signing failed: ${result.meta.errorCode ?? "Unknown"}`);
      }
      return result.signature;
    } catch (e) {
      if (e instanceof KmsSignError) throw e.cause ?? e;
      throw e;
    }
  }

  /**
   * Same as sign() but exposes the retry/idempotency metadata
   * (attempts, errorCode, errorClass, elapsedMs, idempotencyHit).
   * On success returns { signature, meta }. On every error path —
   * including retry exhaustion and non-retryable short-circuits —
   * throws a KmsSignError whose `.meta` carries the same fields a
   * success would have populated. This mirrors the Go convention of
   * returning `(meta, wrapped)` so callers can introspect what
   * happened without parsing the error string.
   */
  async signWithMetadata(
    payload: Uint8Array,
    opts: { correlationId?: string; signal?: AbortSignal } = {},
  ): Promise<SignResult> {
    const start = Date.now();
    const correlationId = opts.correlationId ?? "";
    const signal = opts.signal;

    if (payload.byteLength === 0) {
      const meta: SignMetadata = {
        attempts: 0,
        idempotencyHit: false,
        retryable: false,
        elapsedMs: Date.now() - start,
        errorCode: "EmptyMessage",
        errorClass: "input",
        correlationId,
      };
      throw new KmsSignError(meta, "kms signer: empty message", new KmsEmptyMessageError());
    }

    // Step 1 — idempotency lookup. The key binds keyId to a SHA-256 of
    // the payload, so changing key ids does not return a stale
    // signature for the wrong key and the raw payload never appears
    // in any process-visible key string.
    const cacheKey = idempotencyKey(this.keyId, payload);
    const cached = this.idempotencyCache.get(cacheKey);
    if (cached) {
      const meta: SignMetadata = {
        signature: Buffer.from(cached),
        attempts: 0,
        idempotencyHit: true,
        retryable: false,
        elapsedMs: Date.now() - start,
        correlationId,
      };
      this.logFn("debug", "kms sign idempotency cache hit", {
        correlation_id: correlationId,
        key_ref: safeKeyIdRef(this.keyId),
      });
      return { signature: meta.signature, meta };
    }

    // Step 2 — pre-build the SDK input ONCE so every retry reuses the
    // same Message buffer reference. This is the "canonical digest
    // preserved across attempts" invariant.
    const { SignCommand } = this.loadKmsModule();
    const client = this.getClient();
    const messageBuffer = Buffer.from(crypto.createHash('sha512').update(payload).digest()); // hash once, reused across retries.
    const buildInput = () => ({
      KeyId: this.keyId,
      Message: messageBuffer,
      MessageType: "DIGEST",
      SigningAlgorithm: this.signingAlgorithm,
    });

    let attempts = 0;
    let lastError: unknown;
    let lastMeta: SignMetadata = {
      attempts: 0,
      idempotencyHit: false,
      retryable: false,
      elapsedMs: 0,
      correlationId,
    };
    let lastBackoffMs = 0;

    for (let attempt = 0; attempt <= this.retryConfig.maxRetries; attempt++) {
      attempts = attempt + 1;
      lastMeta = {
        attempts,
        idempotencyHit: false,
        retryable: false,
        elapsedMs: Date.now() - start,
        correlationId,
      };

      if (attempt > 0) {
        lastBackoffMs = nextRetryBackoffMs(
          this.retryConfig,
          lastBackoffMs,
          this.jitterUnit,
        );
        this.logFn("debug", "kms sign retrying", {
          correlation_id: correlationId,
          attempt: attempt + 1,
          max_attempts: this.retryConfig.maxRetries + 1,
          backoff_ms: Math.round(lastBackoffMs),
          key_ref: safeKeyIdRef(this.keyId),
        });
        const slept = await sleepWithAbort(lastBackoffMs, signal);
        if (!slept) {
          const meta: SignMetadata = {
            attempts,
            idempotencyHit: false,
            retryable: false,
            elapsedMs: Date.now() - start,
            errorCode: "ContextCancelled",
            errorClass: "context",
            correlationId,
          };
          throw new KmsSignError(
            meta,
            "kms signer: context cancelled",
            new KmsContextCancelledError(signal?.reason),
          );
        }
      }

      let response: any;
      try {
        response = await client.send(new SignCommand(buildInput()));
      } catch (e) {
        lastError = e;
        const { retryable, code, class: klass } = classifyKmsError(e);
        lastMeta = {
          attempts,
          idempotencyHit: false,
          retryable,
          errorCode: code,
          errorClass: klass,
          elapsedMs: Date.now() - start,
          correlationId,
        };
        this.logFn("debug", "kms sign attempt failed", {
          correlation_id: correlationId,
          attempt: attempt + 1,
          error_code: code,
          error_class: klass,
          retryable,
          key_ref: safeKeyIdRef(this.keyId),
        });
        if (!retryable) {
          throw new KmsSignError(lastMeta, `KMS signing failed (${code}): ${String(e)}`, e);
        }
        continue;
      }

      const signature = response?.Signature;
      if (!signature) {
        const meta: SignMetadata = {
          attempts,
          idempotencyHit: false,
          retryable: false,
          errorCode: "EmptyResponse",
          errorClass: "api",
          elapsedMs: Date.now() - start,
          correlationId,
        };
        throw new KmsSignError(meta, "KMS signing failed: response contained no Signature field");
      }

      const signatureBuffer = Buffer.from(signature);
      this.idempotencyCache.put(cacheKey, signatureBuffer);
      this.logFn("debug", "kms sign succeeded", {
        correlation_id: correlationId,
        attempts,
        key_ref: safeKeyIdRef(this.keyId),
        elapsed_ms: Date.now() - start,
      });
      const meta: SignMetadata = {
        signature: signatureBuffer,
        attempts,
        idempotencyHit: false,
        retryable: false,
        elapsedMs: Date.now() - start,
        correlationId,
      };
      return { signature: signatureBuffer, meta };
    }

    // Exhausted all attempts on retryable errors.
    const wrapped = new KmsSignError(
      lastMeta,
      `kms sign failed after ${attempts} attempts (last code: ${lastMeta.errorCode ?? "Unknown"}): ${String(lastError)}`,
      lastError,
    );
    throw wrapped;
  }

  /**
   * Returns the public key corresponding to the KMS signing key as DER-encoded
   * bytes, base64-encoded, wrapped in a SPKI PEM envelope.
   *
   * KMS returns the raw DER-encoded SubjectPublicKeyInfo (SPKI) bytes, which
   * is exactly what SPKI PEM encapsulates.
   *
   * Note: GetPublicKey is NOT covered by idempotency — public keys are
   * rarely re-fetched and AWS KMS does not bill them the same way as
   * Sign, so adding a cache here would obscure staleness for little gain.
   */
  async public_key(): Promise<PublicKey> {
    const { KMSClient, GetPublicKeyCommand } = this.loadKmsModule();

    const client = this.getClient();

    const command = new GetPublicKeyCommand({ KeyId: this.keyId });

    let response: any;
    try {
      response = await client.send(command);
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      throw new Error(`KMS GetPublicKey failed: ${msg}`);
    }

    if (!response.PublicKey) {
      throw new Error(
        "KMS GetPublicKey: response contained no PublicKey field",
      );
    }

    const der = Buffer.from(response.PublicKey);
    const b64 = der
      .toString("base64")
      .replace(/(.{64})/g, "$1\n")
      .trimEnd();
    return `-----BEGIN PUBLIC KEY-----\n${b64}\n-----END PUBLIC KEY-----\n`;
  }

  /**
   * Returns the current size of the in-memory idempotency cache.
   * Exposed for diagnostics and tests; safe for concurrent use.
   */
  idempotencySize(): number {
    return this.idempotencyCache.size();
  }

  // ---- private helpers ----

  private getClient(): any {
    if (!this.client) {
      const { KMSClient } = this.loadKmsModule();
      this.client = new KMSClient({ region: this.region });
    }
    return this.client;
  }

  /**
   * Lazily requires @aws-sdk/client-kms so that users without the optional
   * dependency do not see errors unless they actually select the kms provider.
   * Tests can bypass this via the `kmsModuleOverride` constructor option.
   */
  private loadKmsModule(): {
    KMSClient: any;
    SignCommand: any;
    GetPublicKeyCommand: any;
  } {
    if (this.moduleOverride) return this.moduleOverride;
    try {
      // eslint-disable-next-line no-eval
      const mod = eval("require")("@aws-sdk/client-kms");
      return {
        KMSClient: mod.KMSClient,
        SignCommand: mod.SignCommand,
        GetPublicKeyCommand: mod.GetPublicKeyCommand,
      };
    } catch {
      throw new Error(
        "kms provider selected but optional dependency `@aws-sdk/client-kms` is not installed. " +
          "Add it to your dependencies: npm install @aws-sdk/client-kms",
      );
    }
  }
}