// Copyright (c) 2026 dotandev
// SPDX-License-Identifier: MIT OR Apache-2.0

/**
 * Retry configuration, error classification, and idempotency cache for
 * the AWS KMS Signer (Issue 66).
 *
 * Design rationale lives alongside the Go implementation in
 * internal/signer/kms_retry.go; the two are kept structurally similar
 * so future audits can compare them line-for-line.
 */

// ---------------------------------------------------------------------------
// Retry configuration
// ---------------------------------------------------------------------------

export interface KmsRetryConfig {
  /** Max attempts beyond the initial call. 0 disables retries. Default 3. */
  maxRetries: number;
  /** Initial backoff in milliseconds before the first retry. Default 250. */
  initialBackoffMs: number;
  /** Upper bound for a single backoff interval in ms. Default 5000. */
  maxBackoffMs: number;
  /** Proportional jitter added to each backoff (0 disables). Default 0.2. */
  jitterFraction: number;
  /** Client-side signature cache TTL in ms. 0 disables caching. Default 60000. */
  idempotencyTtlMs: number;
  /** Maximum entries in the LRU cache. Default 1024. */
  idempotencyMaxEntries: number;
}

/**
 * Conservative production defaults for the KMS Signer: 3 retries,
 * exponential backoff 250ms → 5s with ±20% jitter, and a 60-second
 * client-side idempotency window sized for ~1k unique digests. These
 * values mirror internal/signer.DefaultKMSRetryConfig exactly so the
 * Go CLI and the Node SDK apply the same knobs.
 */
export function defaultKmsRetryConfig(): KmsRetryConfig {
  return {
    maxRetries: 3,
    initialBackoffMs: 250,
    maxBackoffMs: 5000,
    jitterFraction: 0.2,
    idempotencyTtlMs: 60_000,
    idempotencyMaxEntries: 1024,
  };
}

/**
 * Reads GLASSBOX_KMS_* overrides from the current process environment.
 * Unknown / malformed values are silently ignored so a typo in the
 * environment cannot cascade into a runtime error during signing.
 */
export function loadKmsRetryConfigFromEnv(
  base: KmsRetryConfig = defaultKmsRetryConfig(),
): KmsRetryConfig {
  const env = process.env;
  const cfg: KmsRetryConfig = { ...base };

  if (env.GLASSBOX_KMS_MAX_RETRIES != null) {
    const n = Number(env.GLASSBOX_KMS_MAX_RETRIES);
    if (Number.isFinite(n) && n >= 0) cfg.maxRetries = n;
  }
  if (env.GLASSBOX_KMS_INITIAL_BACKOFF_MS != null) {
    const n = Number(env.GLASSBOX_KMS_INITIAL_BACKOFF_MS);
    if (Number.isFinite(n) && n >= 0) cfg.initialBackoffMs = n;
  }
  if (env.GLASSBOX_KMS_MAX_BACKOFF_MS != null) {
    const n = Number(env.GLASSBOX_KMS_MAX_BACKOFF_MS);
    if (Number.isFinite(n) && n >= 0) cfg.maxBackoffMs = n;
  }
  if (env.GLASSBOX_KMS_JITTER_FRACTION != null) {
    const n = Number(env.GLASSBOX_KMS_JITTER_FRACTION);
    if (Number.isFinite(n) && n >= 0) cfg.jitterFraction = n;
  }
  if (env.GLASSBOX_KMS_IDEMPOTENCY_TTL_MS != null) {
    const n = Number(env.GLASSBOX_KMS_IDEMPOTENCY_TTL_MS);
    if (Number.isFinite(n) && n >= 0) cfg.idempotencyTtlMs = n;
  }
  if (env.GLASSBOX_KMS_IDEMPOTENCY_MAX != null) {
    const n = Number(env.GLASSBOX_KMS_IDEMPOTENCY_MAX);
    if (Number.isFinite(n) && n > 0) cfg.idempotencyMaxEntries = n;
  }

  return cfg;
}

// ---------------------------------------------------------------------------
// SignMetadata
// ---------------------------------------------------------------------------

export interface SignMetadata {
  /** Signature bytes on success. Undefined on error. */
  signature?: Buffer;
  /** Total number of KMS API calls; 0 when the idempotency cache hit. */
  attempts: number;
  /** Opaque caller-supplied id threaded through every attempt. */
  correlationId?: string;
  /** True when the signature was returned from the in-memory cache. */
  idempotencyHit: boolean;
  /** AWS error code (or "NetworkError", "ContextCancelled", "EmptyMessage"). Undefined on success. */
  errorCode?: string;
  /** Coarse category: "api" | "network" | "context" | "input" | "unknown". Undefined on success. */
  errorClass?: string;
  /** True if the final error was classified as retryable. */
  retryable: boolean;
  /** Wall-clock duration of the entire call in ms. */
  elapsedMs: number;
}

export class KmsEmptyMessageError extends Error {
  constructor() {
    super("kms signer: empty message");
    this.name = "KmsEmptyMessageError";
  }
}

export class KmsContextCancelledError extends Error {
  constructor(public readonly cause: unknown) {
    super("kms signer: context cancelled");
    this.name = "KmsContextCancelledError";
  }
}

// ---------------------------------------------------------------------------
// Error classification
// ---------------------------------------------------------------------------

/**
 * AWS KMS error codes classified as always safe to retry. Mirrors the
 * Go retryableKMSErrorCodes map in internal/signer/kms_retry.go.
 */
const RETRYABLE_KMS_ERROR_CODES: ReadonlySet<string> = new Set<string>([
  "InternalError",
  "ServiceUnavailable",
  "ThrottlingException",
  "TooManyRequests",
  "TooManyRequestsException",
  "RequestTimeout",
  "RequestTimeoutException",
  "KMSInternalException",
  "UnavailableException",
  "RequestLimitExceeded",
  "ProvisionedThroughputExceededException",
]);

/**
 * Exception names thrown by the AWS SDK v3 that count as retryable
 * transport failures (DNS, connection reset, socket hangup, etc.). The
 * SDK v3 uses a unified `error.name` convention.
 */
const RETRYABLE_TRANSPORT_NAMES: ReadonlySet<string> = new Set<string>([
  "TimeoutError",
  "ECONNRESET",
  "ECONNREFUSED",
  "ENOTFOUND",
  "EAI_AGAIN",
  "EPIPE",
  "ETIMEDOUT",
  "NetworkingError",
]);

function isRetryableKmsErrorCode(code: string): boolean {
  return RETRYABLE_KMS_ERROR_CODES.has(code);
}

/**
 * Inspects an SDK error and returns (retryable, code, class).
 * Never records nor logs payload bytes — only the SDK-reported code or
 * the originating Error's name.
 */
export function classifyKmsError(err: unknown): {
  retryable: boolean;
  code: string;
  class: "api" | "network" | "unknown";
} {
  if (err == null) return { retryable: false, code: "", class: "unknown" };

  if (err instanceof Error) {
    const name = err.name ?? "";
    if (RETRYABLE_TRANSPORT_NAMES.has(name)) {
      return { retryable: true, code: "NetworkError", class: "network" };
    }
    const anyErr = err as Error & { code?: string; $metadata?: unknown };
    // Prefer an explicit AWS SDK error code; only fall back to the
    // Error's `name` when it carries domain meaning (i.e. it is not
    // the generic "Error" name a plain `new Error(...)` produces).
    // This keeps opaque JS errors from being misclassified as API
    // errors just because Error.prototype.name === "Error".
    const explicitCode = anyErr.code;
    const code = explicitCode ?? (name && name !== "Error" ? name : "");
    if (code) {
      return {
        retryable: isRetryableKmsErrorCode(code),
        code,
        class: "api",
      };
    }
  }
  return { retryable: false, code: "Unknown", class: "unknown" };
}

// ---------------------------------------------------------------------------
// Backoff
// ---------------------------------------------------------------------------

/**
 * Returns the next backoff in ms using exponential growth capped at
 * maxBackoffMs, plus optional proportional jitter. `jitterUnit` is a
 * function that returns a uniform random number in [-1, 1]; tests
 * inject a deterministic source to keep assertions reproducible.
 */
export function nextRetryBackoffMs(
  cfg: KmsRetryConfig,
  currentMs: number,
  jitterUnit: () => number,
): number {
  const base = currentMs <= 0 ? cfg.initialBackoffMs : currentMs * 2;
  const capped = base > cfg.maxBackoffMs ? cfg.maxBackoffMs : base;
  if (cfg.jitterFraction <= 0 || jitterUnit == null) {
    return capped;
  }
  const j = jitterUnit();
  const delta = capped * cfg.jitterFraction * j;
  const adjusted = capped + delta;
  return adjusted < 0 ? 0 : adjusted;
}

/**
 * Sleeps for ms, or aborts when an AbortSignal fires. Returns true on
 * full sleep, false on abort (with reason available via the signal).
 */
export function sleepWithAbort(ms: number, signal?: AbortSignal): Promise<boolean> {
  if (ms <= 0) return Promise.resolve(signal?.aborted !== true);
  return new Promise((resolve) => {
    let done = false;
    const finish = () => {
      if (done) return;
      done = true;
      if (timer) clearTimeout(timer);
      resolve(!signal?.aborted);
    };
    const timer = setTimeout(finish, ms);
    if (signal) {
      if (signal.aborted) {
        finish();
        return;
      }
      signal.addEventListener("abort", finish, { once: true });
    }
  });
}

// ---------------------------------------------------------------------------
// safeKeyIdRef — log-safe identifier
// ---------------------------------------------------------------------------

/**
 * Returns a short suffix of a KMS key id suitable for log lines. Empty
 * values become "(none)" so log lines are unambiguous. Full key ids
 * (ARNs, aliases) are not AWS secrets but are still identifiers; we
 * truncate to a fixed-length suffix so log scraping is less valuable
 * to an attacker with logs but no production credentials.
 */
export function safeKeyIdRef(keyId: string | undefined | null): string {
  if (!keyId) return "(none)";
  const suffixLen = 8;
  if (keyId.length <= suffixLen) return keyId;
  return "..." + keyId.slice(-suffixLen);
}

// ---------------------------------------------------------------------------
// Idempotency cache (TTL + LRU)
// ---------------------------------------------------------------------------

/**
 * Tiny TTL+LRU table keyed by opaque strings. Used to short-circuit
 * identical re-signs within the configured TTL window without paying
 * another KMS bill and another client-side Encrypt call. All methods
 * are safe for concurrent use.
 *
 * Privacy: the cache stores signature bytes only — never messages,
 * digests, or key ids. The lookup key binds key id and a SHA-256 of
 * the message but never the plain message.
 */
export class IdempotencyCache {
  private readonly entries = new Map<string, { value: Uint8Array; expiresAt: number }>();
  private readonly maxEntries: number;
  private readonly ttlMs: number;
  private readonly now: () => number;

  constructor(maxEntries: number, ttlMs: number, now: () => number = Date.now) {
    this.maxEntries = maxEntries;
    this.ttlMs = ttlMs;
    this.now = now;
  }

  /** Returns true when caching is effectively disabled. */
  get disabled(): boolean {
    return this.ttlMs <= 0 || this.maxEntries <= 0;
  }

  get(key: string): Uint8Array | undefined {
    if (this.disabled) return undefined;
    const entry = this.entries.get(key);
    if (!entry) return undefined;
    if (this.now() > entry.expiresAt) {
      this.entries.delete(key);
      return undefined;
    }
    // LRU touch — re-insert to move to back of insertion order.
    this.entries.delete(key);
    this.entries.set(key, entry);
    return entry.value;
  }

  put(key: string, value: Uint8Array): void {
    if (this.disabled || !key) return;
    // Clone to insulate from caller-side mutation.
    const clone = new Uint8Array(value.byteLength);
    clone.set(value);
    if (this.entries.has(key)) this.entries.delete(key);
    else if (this.entries.size >= this.maxEntries) {
      // Evict oldest (first inserted).
      const oldestKey = this.entries.keys().next().value;
      if (oldestKey !== undefined) this.entries.delete(oldestKey);
    }
    this.entries.set(key, { value: clone, expiresAt: this.now() + this.ttlMs });
  }

  size(): number {
    return this.entries.size;
  }
}

/**
 * Cached reference to Node's `crypto` module so we don't pay the
 * eval-require cost on every idempotency-key derivation. The cache
 * is process-global: only one signer per process should ever import
 * Node crypto, so this is safe even across multiple KmsSigner instances.
 */
let cachedNodeCrypto: any | undefined;

function loadNodeCrypto(): any {
  if (!cachedNodeCrypto) {
    // eslint-disable-next-line @typescript-eslint/no-var-requires, no-eval
    cachedNodeCrypto = eval("require")("crypto");
  }
  return cachedNodeCrypto;
}

/**
 * Computes the idempotency cache key for a (key id, message) pair.
 * The message is hashed so the raw payload never appears in the cache
 * key string; excludes chance of the key surfacing in process listings
 * or breaker traces. The `v1:` prefix lets us rev the key scheme later
 * without colliding with cached entries.
 */
export function idempotencyKey(
  keyId: string,
  message: Uint8Array,
): string {
  const nodeCrypto = loadNodeCrypto();
  const hash = nodeCrypto.createHash("sha256");
  hash.update(Buffer.from(message));
  return `v1:${keyId}:${hash.digest("hex")}`;
}

/**
 * Reset the cached Node crypto reference. Test-only escape hatch so
 * tests can install a crypto polyfill before the first derivation.
 */
export function _resetIdempotencyCryptoForTesting(): void {
  cachedNodeCrypto = undefined;
}

// ---------------------------------------------------------------------------
// Logging hook
// ---------------------------------------------------------------------------

export interface KmsLogFunction {
  (level: "debug" | "warn", msg: string, attrs: Record<string, unknown>): void;
}

/**
 * Default log function: writes debug-and-above events through the
 * global console as a single readable line; production deployments
 * are expected to swap this for an OpenTelemetry / structured-log
 * adapter via the constructor option.
 */
export function defaultKmsLog(logger: KmsLogFunction = consoleLog): KmsLogFunction {
  return logger;
}

function consoleLog(
  level: "debug" | "warn",
  msg: string,
  attrs: Record<string, unknown>,
): void {
  // Never include payload bytes, full key ids, or signature bytes in
  // the formatted text — only known-safe, scalar attributes.
  const entries = Object.entries(attrs)
    .map(([k, v]) => `${k}=${safeFormat(v)}`)
    .join(" ");
  const line = `[kms ${level}] ${msg}${entries ? " " + entries : ""}`;
  if (level === "warn") {
    console.warn(line);
  } else {
    console.debug(line);
  }
}

function safeFormat(v: unknown): string {
  if (v == null) return "null";
  if (typeof v === "number" || typeof v === "boolean") return String(v);
  if (typeof v === "string") {
    // Bound log-line size & avoid leaking large strings.
    return v.length > 64 ? v.slice(0, 64) + "..." : v;
  }
  return "[complex]";
}
