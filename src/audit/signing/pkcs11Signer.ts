// Copyright (c) glassbox Authors.
// SPDX-License-Identifier: Apache-2.0

import type {
  AuditSigner,
  PublicKey,
  Signature,
  HardwareAttestation,
} from "./types";
import { HsmRateLimiter } from "./rateLimiter";
import * as crypto from "crypto";

// eslint-disable-next-line @typescript-eslint/no-var-requires
const lazyRequire = (name: string): any => {
  return eval("require")(name);
};

const TOKEN_LABEL_PADDING = /\0/g;
const PIV_SLOT_REGEX = /^(0x)?[0-9a-fA-F]{2}$/;
const CKU_USER = 1;

const CKR_DEVICE_REMOVED = 0x00000032;
const CKR_OBJECT_HANDLE_INVALID = 0x00000082;
const CKR_SESSION_CLOSED = 0x000000b0;
const CKR_SESSION_HANDLE_INVALID = 0x000000b3;
const CKR_USER_ALREADY_LOGGED_IN = 0x00000100;
const CKR_USER_NOT_LOGGED_IN = 0x00000101;
const CKR_CRYPTOKI_ALREADY_INITIALIZED = 0x00000191;
const CKR_PIN_INCORRECT = 0x000000a0;
const CKR_PIN_LOCKED = 0x000000a4;

/** Default maximum number of concurrent PKCS#11 sessions in the pool. */
const DEFAULT_MAX_SESSIONS = 4;

const VALID_PKCS11_ALGORITHMS = new Set(["ed25519", "secp256k1"]);

export const normalizeTokenLabel = (label: string): string =>
  label.replace(TOKEN_LABEL_PADDING, "").trim();

export const resolveYkcs11KeyIdHex = (pivSlot: string): string => {
  const trimmed = pivSlot.trim().toLowerCase();
  if (!PIV_SLOT_REGEX.test(trimmed)) {
    throw new Error(
      `Invalid PIV slot '${pivSlot}'. Expected a 2-digit hex value like 9a, 9c, or f9.`,
    );
  }

  const hex = trimmed.startsWith("0x") ? trimmed.slice(2) : trimmed;
  const slotValue = Number.parseInt(hex, 16);

  let keyId: number | undefined;
  if (slotValue === 0x9a) keyId = 1;
  if (slotValue === 0x9c) keyId = 2;
  if (slotValue === 0x9d) keyId = 3;
  if (slotValue === 0x9e) keyId = 4;
  if (slotValue >= 0x82 && slotValue <= 0x95) keyId = slotValue - 0x82 + 5;
  if (slotValue === 0xf9) keyId = 25;

  if (!keyId) {
    throw new Error(
      `Unsupported PIV slot '${pivSlot}'. Supported slots: 9a, 9c, 9d, 9e, 82-95, f9.`,
    );
  }

  return keyId.toString(16).padStart(2, "0");
};

export const resolvePkcs11KeyIdHex = (cfg: {
  keyIdHex?: string;
  pivSlot?: string;
}): string | undefined => {
  if (cfg.keyIdHex) {
    const normalized = cfg.keyIdHex.trim();
    if (!/^[0-9a-fA-F]+$/.test(normalized) || normalized.length % 2 !== 0) {
      throw new Error(
        `Invalid GLASSBOX_PKCS11_KEY_ID '${cfg.keyIdHex}'. Expected an even-length hex string (e.g., 01, 0a, 10).`,
      );
    }
    return normalized;
  }
  if (cfg.pivSlot) return resolveYkcs11KeyIdHex(cfg.pivSlot);
  return undefined;
};

const resolvePkcs11Slot = (opts: {
  slots: Array<number | Buffer>;
  slotIndex?: string;
  tokenLabel?: string;
  getTokenInfo: (slotId: number | Buffer) => { label?: unknown };
}): number | Buffer => {
  if (opts.tokenLabel) {
    const desired = normalizeTokenLabel(opts.tokenLabel);
    const available: string[] = [];

    for (const slot of opts.slots) {
      const info = opts.getTokenInfo(slot);
      const rawLabel = info?.label;
      const label =
        typeof rawLabel === "string" ? normalizeTokenLabel(rawLabel) : "";
      if (label) available.push(label);
      if (label === desired) return slot;
    }

    const availableMessage =
      available.length > 0
        ? ` Available tokens: ${available.join(", ")}.`
        : " No token labels were reported by the module.";

    throw new Error(
      `GLASSBOX_PKCS11_TOKEN_LABEL (${opts.tokenLabel}) did not match any tokens.${availableMessage}`,
    );
  }

  const trimmedIndex = opts.slotIndex?.trim();
  if (trimmedIndex) {
    const index = Number(trimmedIndex);
    if (!Number.isInteger(index) || index < 0) {
      throw new Error(
        `Invalid GLASSBOX_PKCS11_SLOT '${opts.slotIndex}'. Expected a non-negative integer.`,
      );
    }
    if (index >= opts.slots.length) {
      throw new Error(
        `GLASSBOX_PKCS11_SLOT (${index}) is out of range. Available slot indexes: 0-${opts.slots.length - 1}.`,
      );
    }
    return opts.slots[index];
  }

  return opts.slots[0];
};

type Pkcs11ErrorLike = Error & {
  code?: number;
  method?: string;
};

/** A live, logged-in PKCS#11 session with a resolved key handle. */
interface PooledSession {
  lib: any;
  session: any;
  keyHandle: any;
  initializedBySigner: boolean;
}

/** Configuration that the pool needs to open sessions. */
interface PoolConfig {
  pkcs11: any;
  module: string | undefined;
  tokenLabel: string | undefined;
  slot: string | undefined;
  pin: string;
  keyLabel: string | undefined;
  resolvedKeyIdHex: string | undefined;
  maxSessions: number;
}

/**
 * Pkcs11SessionPool manages a bounded set of PKCS#11 sessions.
 *
 * Concurrency guarantees:
 * - At most `maxSessions` concurrent sessions are open at any time.
 * - C_Login is serialized through a single promise-chain mutex so that two
 *   concurrent callers never race on the login state of the same token slot.
 * - A session that fails a stale-session error is destroyed and removed from
 *   the pool rather than returned to the idle queue.
 * - Cancelled callers (via AbortSignal) never receive a session; any session
 *   that was already being opened for them is closed before the error is thrown.
 * - destroy() closes every open session and finalizes the module.
 */
export class Pkcs11SessionPool {
  private readonly cfg: PoolConfig;

  /** Sessions currently sitting idle, ready to be acquired. */
  private readonly idle: PooledSession[] = [];

  /** Total number of sessions that exist (idle + in-use). */
  private activeCount = 0;

  /**
   * Queue of resolve functions for callers waiting for a session to become
   * available. Each entry is fulfilled when a session is released.
   */
  private readonly waiters: Array<() => void> = [];

  /**
   * Login serialization mutex. All login operations are chained on this
   * promise so that only one C_Login runs at a time, preventing races on
   * CKR_USER_ALREADY_LOGGED_IN across concurrent session-open attempts.
   */
  private loginMutex: Promise<void> = Promise.resolve();

  /** Whether destroy() has been called. After this no new sessions open. */
  private destroyed = false;

  constructor(cfg: PoolConfig) {
    this.cfg = cfg;
  }

  /**
   * Acquire a session from the pool.
   *
   * If an idle session is available it is returned immediately. If the pool
   * has not yet reached `maxSessions` a new session is opened. Otherwise the
   * caller waits until another caller calls release().
   *
   * If the optional AbortSignal is aborted before a session becomes available
   * the method throws `DOMException { name: "AbortError" }` and does not leak
   * a session or increment activeCount.
   */
  async acquire(signal?: AbortSignal): Promise<PooledSession> {
    if (this.destroyed) {
      throw new Error("Pkcs11SessionPool has been destroyed");
    }

    // Fast path: idle session available.
    if (this.idle.length > 0) {
      return this.idle.pop()!;
    }

    // Slow path: wait if the pool is full.
    if (this.activeCount >= this.cfg.maxSessions) {
      await this.waitForCapacity(signal);
      // After being woken, either an idle session is available (normal release)
      // or the slot was freed by a stale-session destroy (idle queue is empty
      // but activeCount dropped). Handle both cases.
      if (this.idle.length > 0) {
        return this.idle.pop()!;
      }
      // No idle session — open a fresh one using the freed slot.
      this.activeCount++;
      try {
        return await this.openSession(signal);
      } catch (err) {
        this.activeCount--;
        this.notifyNextWaiter();
        throw err;
      }
    }

    // Room to open a new session.
    this.activeCount++;
    try {
      const entry = await this.openSession(signal);
      return entry;
    } catch (err) {
      this.activeCount--;
      this.notifyNextWaiter();
      throw err;
    }
  }

  /**
   * Return a session to the pool.
   *
   * If `error` is a stale-session error the session is destroyed instead of
   * being recycled, preventing unsafe reuse of an invalid session handle.
   * The activeCount slot is freed so waiting callers can open a fresh session.
   */
  release(entry: PooledSession, error?: unknown): void {
    if (this.destroyed) {
      this.closeEntry(entry);
      this.activeCount--;
      return;
    }

    if (error !== undefined && isStaleSessionError(error)) {
      this.closeEntry(entry);
      this.activeCount--;
      // Don't push to idle; the freed slot lets the next waiter open fresh.
      this.notifyNextWaiter();
      return;
    }

    this.idle.push(entry);
    this.notifyNextWaiter();
  }

  /**
   * Close all sessions and finalize the module. Safe to call multiple times.
   */
  destroy(): void {
    if (this.destroyed) return;
    this.destroyed = true;

    // Drain idle entries.
    while (this.idle.length > 0) {
      const entry = this.idle.pop()!;
      this.closeEntry(entry);
      this.activeCount--;
    }

    // Wake any waiters so they can throw "destroyed".
    while (this.waiters.length > 0) {
      const wake = this.waiters.shift()!;
      wake();
    }
  }

  // ── Private helpers ────────────────────────────────────────────────────────

  /** Wait until a session slot becomes free, respecting an optional AbortSignal. */
  private waitForCapacity(signal?: AbortSignal): Promise<void> {
    return new Promise<void>((resolve, reject) => {
      if (signal?.aborted) {
        reject(new AbortError("PKCS#11 session acquire aborted before queuing"));
        return;
      }

      const onAbort = (): void => {
        const idx = this.waiters.indexOf(wake);
        if (idx !== -1) this.waiters.splice(idx, 1);
        reject(new AbortError("PKCS#11 session acquire cancelled"));
      };

      const wake = (): void => {
        signal?.removeEventListener("abort", onAbort);
        if (this.destroyed) {
          reject(new Error("Pkcs11SessionPool has been destroyed"));
        } else {
          resolve();
        }
      };

      signal?.addEventListener("abort", onAbort, { once: true });
      this.waiters.push(wake);
    });
  }

  /** Wake the next queued waiter (if any). */
  private notifyNextWaiter(): void {
    if (this.waiters.length > 0) {
      const wake = this.waiters.shift()!;
      wake();
    }
  }

  /**
   * Open a brand-new PKCS#11 session: load module, initialize, open session,
   * serialize login through the mutex, and find the key handle.
   *
   * If the AbortSignal fires after the session is physically opened but before
   * we return, the session is closed before throwing so there is no leak.
   */
  private async openSession(signal?: AbortSignal): Promise<PooledSession> {
    if (signal?.aborted) {
      throw new AbortError("PKCS#11 session open aborted");
    }

    const pkcs11 = this.cfg.pkcs11;
    const lib = new pkcs11.PKCS11();
    let initializedBySigner = false;
    let session: any | undefined;

    try {
      // Load module.
      try {
        lib.load(this.cfg.module);
      } catch (err) {
        throw new Error(
          `Failed to load PKCS#11 module at '${this.cfg.module}': ${
            err instanceof Error ? err.message : String(err)
          }. Check that the library exists and is accessible.`,
        );
      }

      // Initialize (idempotent — CKR_CRYPTOKI_ALREADY_INITIALIZED is not an error).
      try {
        lib.C_Initialize();
        initializedBySigner = true;
      } catch (err) {
        if (!isPkcs11Code(err, CKR_CRYPTOKI_ALREADY_INITIALIZED)) {
          throw formatPkcs11Error(
            "PKCS#11 initialization",
            err,
            "Check that the library is not locked by another process.",
          );
        }
      }

      // Enumerate slots.
      let slots: Array<number | Buffer>;
      try {
        slots = lib.C_GetSlotList(true) as Array<number | Buffer>;
      } catch (err) {
        throw formatPkcs11Error(
          "PKCS#11 slot enumeration",
          err,
          "Ensure the token is connected and the PKCS#11 module can enumerate slots.",
        );
      }

      if (!slots || slots.length === 0) {
        throw new Error(
          "No PKCS#11 slots with a present token were found. Ensure the HSM/token is connected.",
        );
      }

      const slot = resolvePkcs11Slot({
        slots,
        slotIndex: this.cfg.slot,
        tokenLabel: this.cfg.tokenLabel,
        getTokenInfo: (slotId) => lib.C_GetTokenInfo(slotId),
      });

      // Open the session.
      try {
        session = lib.C_OpenSession(
          slot,
          pkcs11.CKF_SERIAL_SESSION | pkcs11.CKF_RW_SESSION,
        );
      } catch (err) {
        throw formatPkcs11Error(
          "PKCS#11 session open",
          err,
          "Verify the selected slot/token is valid and available for a new session.",
        );
      }

      // Respect cancellation now that a physical resource has been allocated.
      if (signal?.aborted) {
        throw new AbortError("PKCS#11 session open aborted after C_OpenSession");
      }

      // Serialized login: chain on loginMutex so only one C_Login runs at a time.
      const pin = this.cfg.pin;
      let loginError: unknown;
      this.loginMutex = this.loginMutex.then(async () => {
        try {
          lib.C_Login(session, CKU_USER, pin);
        } catch (err) {
          if (!isPkcs11Code(err, CKR_USER_ALREADY_LOGGED_IN)) {
            // Redact the PIN from the error — never include it in the message.
            loginError = sanitizePinError(err);
          }
        }
      });
      await this.loginMutex;

      if (loginError !== undefined) {
        throw loginError;
      }

      // Key lookup.
      const keyHandle = findKey(lib, session, this.cfg.keyLabel, this.cfg.resolvedKeyIdHex, pkcs11);

      return { lib, session, keyHandle, initializedBySigner };
    } catch (err) {
      // Guaranteed cleanup: close session and finalize if we opened them.
      if (session !== undefined) {
        try { lib.C_CloseSession(session); } catch { /* best-effort */ }
      }
      if (initializedBySigner) {
        try { lib.C_Finalize(); } catch { /* best-effort */ }
      }
      throw err;
    }
  }

  /** Best-effort close of a pooled session. */
  private closeEntry(entry: PooledSession): void {
    try {
      entry.lib.C_CloseSession(entry.session);
    } catch { /* best-effort */ }

    if (entry.initializedBySigner) {
      try { entry.lib.C_Finalize(); } catch { /* best-effort */ }
    }
  }
}

// ── Module-level helpers (not on the class to keep it lean) ──────────────────

/** Returns true for error codes that indicate the session can't be reused. */
function isStaleSessionError(err: unknown): boolean {
  return isPkcs11Code(
    err,
    CKR_SESSION_HANDLE_INVALID,
    CKR_SESSION_CLOSED,
    CKR_USER_NOT_LOGGED_IN,
    CKR_OBJECT_HANDLE_INVALID,
    CKR_DEVICE_REMOVED,
  );
}

function isPkcs11Code(err: unknown, ...codes: number[]): boolean {
  const code = (err as Pkcs11ErrorLike | undefined)?.code;
  return typeof code === "number" && codes.includes(code);
}

function formatPkcs11Error(stage: string, err: unknown, remediation: string): Error {
  const code = (err as Pkcs11ErrorLike | undefined)?.code;
  const method = (err as Pkcs11ErrorLike | undefined)?.method;
  const rawMessage = (err as Pkcs11ErrorLike | undefined)?.message;
  const message = typeof rawMessage === "string" ? rawMessage : (err instanceof Error ? err.message : String(err));
  const codeSuffix =
    typeof code === "number"
      ? ` (0x${code.toString(16).padStart(8, "0")})`
      : "";
  const methodPrefix =
    typeof method === "string" && method.length > 0 ? `${method}: ` : "";

  return new Error(
    `${stage} failed: ${methodPrefix}${message}${codeSuffix}. ${remediation}`,
  );
}

/**
 * Sanitize a login error so that PIN values are never propagated in messages.
 * PIN-related errors get a fixed message; all other errors pass through
 * (their message does not contain the PIN).
 */
function sanitizePinError(err: unknown): Error {
  if (isPkcs11Code(err, CKR_PIN_INCORRECT)) {
    return formatPkcs11Error(
      "PKCS#11 login",
      { code: (err as any).code, message: "Wrong PIN", method: (err as any).method } as any,
      "Verify GLASSBOX_PKCS11_PIN and ensure the token is inserted and unlocked.",
    );
  }
  if (isPkcs11Code(err, CKR_PIN_LOCKED)) {
    return formatPkcs11Error(
      "PKCS#11 login",
      { code: CKR_PIN_LOCKED, message: "PIN locked" } as any,
      "The token PIN is locked. Use the SO PIN to unlock before retrying.",
    );
  }
  return formatPkcs11Error(
    "PKCS#11 login",
    err,
    "Verify GLASSBOX_PKCS11_PIN and ensure the token is inserted and unlocked.",
  );
}

/** Locate the private key on an already-logged-in session. */
function findKey(
  lib: any,
  session: any,
  keyLabel: string | undefined,
  resolvedKeyIdHex: string | undefined,
  pkcs11: any,
): any {
  const template: Array<{ type: number; value: Buffer | number | string }> = [
    { type: pkcs11.CKA_CLASS, value: pkcs11.CKO_PRIVATE_KEY },
  ];
  if (keyLabel) {
    template.push({ type: pkcs11.CKA_LABEL, value: keyLabel });
  }
  if (resolvedKeyIdHex) {
    template.push({
      type: pkcs11.CKA_ID,
      value: Buffer.from(resolvedKeyIdHex, "hex"),
    });
  }

  let keys: any[];
  try {
    lib.C_FindObjectsInit(session, template);
    try {
      keys = lib.C_FindObjects(session, 1) as any[];
    } finally {
      lib.C_FindObjectsFinal(session);
    }
  } catch (err) {
    throw formatPkcs11Error(
      "PKCS#11 key lookup",
      err,
      "Verify GLASSBOX_PKCS11_KEY_LABEL / GLASSBOX_PKCS11_KEY_ID / GLASSBOX_PKCS11_PIV_SLOT and confirm the key exists on the token.",
    );
  }

  const key = keys?.[0];
  if (!key) {
    const selector = keyLabel
      ? `label '${keyLabel}'`
      : resolvedKeyIdHex
        ? `CKA_ID '${resolvedKeyIdHex}'`
        : "(unknown selector)";
    throw new Error(
      `Private key not found for ${selector}. Check the configured key selector and confirm the key exists on the token.`,
    );
  }
  return key;
}

/** Minimal AbortError compatible with environments that may not have DOMException. */
class AbortError extends Error {
  readonly name = "AbortError";
  constructor(message: string) {
    super(message);
  }
}

/**
 * PKCS#11-backed signer supporting Ed25519 and secp256k1.
 *
 * Concurrency model: `Pkcs11Signer` delegates session management to an
 * internal `Pkcs11SessionPool`. Concurrent sign() calls acquire independent
 * sessions (up to `GLASSBOX_PKCS11_MAX_SESSIONS`, default 4). Login is
 * serialized inside the pool so PIN errors never race. Callers that supply an
 * AbortSignal will have their pending acquire cancelled and no session leaked.
 */
export class Pkcs11Signer implements AuditSigner {
  private readonly cfg: {
    module: string | undefined;
    tokenLabel: string | undefined;
    slot: string | undefined;
    pin: string;
    keyLabel: string | undefined;
    keyIdHex: string | undefined;
    pivSlot: string | undefined;
    publicKeyPem: string | undefined;
    algorithm: string;
    maxSessions: number;
  };

  private readonly resolvedKeyIdHex: string | undefined;
  private readonly pool: Pkcs11SessionPool;
  private readonly pkcs11: any;

  constructor() {
    let pkcs11Mod: any;
    try {
      pkcs11Mod = lazyRequire("pkcs11js");
    } catch {
      throw new Error(
        "pkcs11 provider selected but optional dependency `pkcs11js` is not installed",
      );
    }
    this.pkcs11 = pkcs11Mod;

    const rawAlgorithm = (process.env.GLASSBOX_PKCS11_ALGORITHM || "ed25519").toLowerCase();
    const rawPin = process.env.GLASSBOX_PKCS11_PIN ?? "";
    const rawSlot = process.env.GLASSBOX_PKCS11_SLOT;
    const rawMaxSessions = process.env.GLASSBOX_PKCS11_MAX_SESSIONS;

    const maxSessions = (() => {
      if (rawMaxSessions) {
        const parsed = Number(rawMaxSessions);
        if (Number.isInteger(parsed) && parsed > 0) return parsed;
        throw new Error(
          `Invalid GLASSBOX_PKCS11_MAX_SESSIONS '${rawMaxSessions}'. Expected a positive integer.`,
        );
      }
      return DEFAULT_MAX_SESSIONS;
    })();

    this.cfg = {
      module: process.env.GLASSBOX_PKCS11_MODULE,
      tokenLabel: process.env.GLASSBOX_PKCS11_TOKEN_LABEL,
      slot: rawSlot,
      pin: rawPin,
      keyLabel: process.env.GLASSBOX_PKCS11_KEY_LABEL,
      keyIdHex: process.env.GLASSBOX_PKCS11_KEY_ID,
      pivSlot: process.env.GLASSBOX_PKCS11_PIV_SLOT,
      publicKeyPem: process.env.GLASSBOX_PKCS11_PUBLIC_KEY_PEM,
      algorithm: rawAlgorithm,
      maxSessions,
    };

    if (!this.cfg.module) {
      throw new Error(
        "pkcs11 provider selected but GLASSBOX_PKCS11_MODULE is not set",
      );
    }
    if (!this.cfg.pin) {
      throw new Error(
        "pkcs11 provider selected but GLASSBOX_PKCS11_PIN is not set",
      );
    }
    if (!this.cfg.keyLabel && !this.cfg.keyIdHex && !this.cfg.pivSlot) {
      throw new Error(
        "pkcs11 provider selected but neither GLASSBOX_PKCS11_KEY_LABEL, GLASSBOX_PKCS11_KEY_ID, nor GLASSBOX_PKCS11_PIV_SLOT is set",
      );
    }
    if (rawSlot && !/^\d+$/.test(rawSlot.trim())) {
      throw new Error(
        `Invalid GLASSBOX_PKCS11_SLOT '${rawSlot}'. Expected a non-negative integer.`,
      );
    }
    if (!VALID_PKCS11_ALGORITHMS.has(this.cfg.algorithm)) {
      throw new Error(
        `Unsupported GLASSBOX_PKCS11_ALGORITHM '${this.cfg.algorithm}'. Supported values: ed25519, secp256k1.`,
      );
    }

    this.resolvedKeyIdHex = resolvePkcs11KeyIdHex(this.cfg);

    if (this.cfg.publicKeyPem) {
      try {
        crypto.createPublicKey(this.cfg.publicKeyPem);
      } catch {
        throw new Error(
          "Invalid GLASSBOX_PKCS11_PUBLIC_KEY_PEM. Expected a SPKI PEM public key.",
        );
      }
    }

    this.pool = new Pkcs11SessionPool({
      pkcs11: this.pkcs11,
      module: this.cfg.module,
      tokenLabel: this.cfg.tokenLabel,
      slot: this.cfg.slot,
      pin: this.cfg.pin,
      keyLabel: this.cfg.keyLabel,
      resolvedKeyIdHex: this.resolvedKeyIdHex,
      maxSessions: this.cfg.maxSessions,
    });
  }

  async public_key(): Promise<PublicKey> {
    if (this.cfg.publicKeyPem) return this.cfg.publicKeyPem;
    throw new Error(
      "pkcs11 public key retrieval is not configured. Set GLASSBOX_PKCS11_PUBLIC_KEY_PEM to a SPKI PEM public key.",
    );
  }

  async sign(payload: Uint8Array, signal?: AbortSignal): Promise<Signature> {
    await HsmRateLimiter.checkAndRecordCall();

    const entry = await this.pool.acquire(signal);
    let lastError: unknown;

    try {
      const result = this.signWithSession(entry, payload);
      return result;
    } catch (err) {
      lastError = err;

      if (isStaleSessionError(err)) {
        // Release and destroy the stale session, then try once more with a fresh one.
        this.pool.release(entry, err);

        const freshEntry = await this.pool.acquire(signal);
        try {
          const result = this.signWithSession(freshEntry, payload);
          this.pool.release(freshEntry);
          return result;
        } catch (retryErr) {
          this.pool.release(freshEntry, retryErr);
          throw wrapSignError(
            "PKCS#11 signing after reconnect",
            retryErr,
            "The signer retried once after a stale session. Reinsert the token or check the HSM middleware logs.",
          );
        }
      }

      this.pool.release(entry, err);
      throw wrapSignError(
        "PKCS#11 signing",
        err,
        "Verify the token is still connected and the configured key supports signing.",
      );
    } finally {
      // If we already handled release in the stale-session branch, the
      // entry was released there. Only release here on the happy path.
      if (lastError === undefined) {
        this.pool.release(entry);
      }
    }
  }

  async close(): Promise<void> {
    this.pool.destroy();
  }

  async attestation_chain(): Promise<HardwareAttestation | undefined> {
    return undefined;
  }

  // ── Private ────────────────────────────────────────────────────────────────

  private signWithSession(entry: PooledSession, payload: Uint8Array): Buffer {
    const { lib, session, keyHandle } = entry;
    const pkcs11 = this.pkcs11;

    let mechanism: { mechanism: number };
    let dataToSign = Buffer.from(payload);

    if (this.cfg.algorithm === "secp256k1") {
      mechanism = { mechanism: pkcs11.CKM_ECDSA };
      dataToSign = crypto.createHash("sha256").update(payload).digest();
    } else {
      mechanism = { mechanism: pkcs11.CKM_EDDSA ?? 0x00001050 };
    }

    lib.C_SignInit(session, mechanism, keyHandle);
    return Buffer.from(lib.C_Sign(session, dataToSign));
  }
}

function wrapSignError(stage: string, err: unknown, remediation: string): Error {
  if (err instanceof Error && typeof (err as Pkcs11ErrorLike).code !== "number") {
    return err;
  }
  return formatPkcs11Error(stage, err, remediation);
}

export const Pkcs11Ed25519Signer = Pkcs11Signer;
