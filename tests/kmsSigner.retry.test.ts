// Copyright (c) 2026 dotandev
// SPDX-License-Identifier: MIT OR Apache-2.0

import { KmsSigner, KmsSignError } from '../src/audit/signing/kmsSigner';
import {
  classifyKmsError,
  defaultKmsRetryConfig,
  IdempotencyCache,
  idempotencyKey,
  KmsEmptyMessageError,
  KmsContextCancelledError,
  loadKmsRetryConfigFromEnv,
  nextRetryBackoffMs,
  safeKeyIdRef,
  sleepWithAbort,
} from '../src/audit/signing/kmsRetry';
import type { KmsLogFunction } from '../src/audit/signing/kmsRetry';

// ---------------------------------------------------------------------------
// Test fixtures: mock KMS module + logger
// ---------------------------------------------------------------------------

interface MockKmsOptions {
  signature?: Buffer;
  signErrorSequence?: Array<Error | null>; // null = success, Error = failure
  publicKey?: Buffer;
}

interface MockKmsHandle {
  module: {
    KMSClient: any;
    SignCommand: any;
    GetPublicKeyCommand: any;
  };
  callCount: { sign: number; publicKey: number };
  captured: Array<{ Message: Buffer; KeyId: string; SigningAlgorithm: string; MessageType: string }>;
}

function buildMockKms(opts: MockKmsOptions = {}): MockKmsHandle {
  const signatureBase = opts.signature ?? Buffer.from('kmssig');
  const publicKeyBase = opts.publicKey ?? Buffer.concat([Buffer.from([0x30, 0x2a]), Buffer.alloc(40)]);
  const sequence = opts.signErrorSequence ?? [];
  let cursor = 0;

  const captured: MockKmsHandle['captured'] = [];
  const callCount = { sign: 0, publicKey: 0 };

  class SignCommand {
    public readonly input: any;
    constructor(input: any) {
      this.input = input;
      captured.push({
        Message: Buffer.from(input.Message),
        KeyId: input.KeyId,
        SigningAlgorithm: input.SigningAlgorithm,
        MessageType: input.MessageType,
      });
    }
  }

  class GetPublicKeyCommand {
    constructor(public readonly input: any) {}
  }

  class KMSClient {
    async send(command: any): Promise<any> {
      if (command instanceof SignCommand) {
        callCount.sign += 1;
        const fail = sequence[cursor++];
        if (fail) throw fail;
        return { Signature: Buffer.from(signatureBase) };
      }
      if (command instanceof GetPublicKeyCommand) {
        callCount.publicKey += 1;
        return { PublicKey: Buffer.from(publicKeyBase) };
      }
      throw new Error('unexpected command: ' + command?.constructor?.name);
    }
  }

  return {
    module: { KMSClient, SignCommand, GetPublicKeyCommand },
    callCount,
    captured,
  };
}

interface CapturedLog {
  level: 'debug' | 'warn';
  msg: string;
  attrs: Record<string, unknown>;
}

function captureLogs(): { fn: KmsLogFunction; lines: CapturedLog[] } {
  const lines: CapturedLog[] = [];
  return {
    lines,
    fn: (level, msg, attrs) => {
      lines.push({ level, msg, attrs: { ...attrs } });
    },
  };
}

// Used by tests to make the retry loop synchronously deterministic.
const ZERO_JITTER: () => number = () => 0;

// ---------------------------------------------------------------------------
// classifyKmsError
// ---------------------------------------------------------------------------

describe('classifyKmsError', () => {
  it.each([
    'InternalError',
    'ServiceUnavailable',
    'ThrottlingException',
    'RequestTimeoutException',
    'KMSInternalException',
    'TooManyRequestsException',
  ])('marks %s as retryable api error', (code) => {
    const err = Object.assign(new Error('boom'), { name: code, code });
    const r = classifyKmsError(err);
    expect(r.retryable).toBe(true);
    expect(r.code).toBe(code);
    expect(r.class).toBe('api');
  });

  it.each([
    'AccessDeniedException',
    'InvalidKeyIdException',
    'ValidationException',
    'DisabledException',
    'NotFoundException',
  ])('marks %s as non-retryable api error', (code) => {
    const err = Object.assign(new Error('boom'), { name: code, code });
    const r = classifyKmsError(err);
    expect(r.retryable).toBe(false);
    expect(r.code).toBe(code);
    expect(r.class).toBe('api');
  });

  it.each(['TimeoutError', 'ECONNRESET', 'ECONNREFUSED', 'ENOTFOUND', 'NetworkingError'])(
    'marks transport error %s as retryable network error',
    (name) => {
      const err = new Error('boom');
      err.name = name;
      const r = classifyKmsError(err);
      expect(r.retryable).toBe(true);
      expect(r.code).toBe('NetworkError');
      expect(r.class).toBe('network');
    },
  );

  it('does not retry on opaque error', () => {
    const r = classifyKmsError(new Error('opaque'));
    expect(r.retryable).toBe(false);
    expect(r.code).toBe('Unknown');
    expect(r.class).toBe('unknown');
  });

  it('treats null error as not retryable', () => {
    expect(classifyKmsError(null)).toEqual({ retryable: false, code: '', class: 'unknown' });
  });
});

// ---------------------------------------------------------------------------
// nextRetryBackoffMs
// ---------------------------------------------------------------------------

describe('nextRetryBackoffMs', () => {
  const cfg = defaultKmsRetryConfig();

  it('starts at initialBackoffMs when current is 0', () => {
    expect(nextRetryBackoffMs(cfg, 0, ZERO_JITTER)).toBe(cfg.initialBackoffMs);
  });

  it('doubles each step up to maxBackoffMs (no jitter)', () => {
    const tight: typeof cfg = { ...cfg, initialBackoffMs: 100, maxBackoffMs: 800, jitterFraction: 0 };
    let last = 0;
    const sequence = [100, 200, 400, 800, 800];
    for (const expected of sequence) {
      const got = nextRetryBackoffMs(tight, last, ZERO_JITTER);
      expect(got).toBe(expected);
      last = got;
    }
  });

  it('applies jitter scaled by jitterFraction', () => {
    const j = () => 1; // +jitterFraction * cap
    const tight: typeof cfg = { ...cfg, initialBackoffMs: 100, maxBackoffMs: 200, jitterFraction: 0.5 };
    expect(nextRetryBackoffMs(tight, 0, j)).toBe(150); // 100 + 50% = 150
    expect(nextRetryBackoffMs(tight, 100, j)).toBe(300); // 200 cap + 50% = 300
  });

  it('never returns negative even with extreme negative jitter', () => {
    const j = () => -1;
    const tight: typeof cfg = { ...cfg, initialBackoffMs: 100, maxBackoffMs: 200, jitterFraction: 0.5 };
    expect(nextRetryBackoffMs(tight, 0, j)).toBe(50);
  });
});

// ---------------------------------------------------------------------------
// sleepWithAbort
// ---------------------------------------------------------------------------

describe('sleepWithAbort', () => {
  it('sleeps full duration when no signal is aborted', async () => {
    const start = Date.now();
    await sleepWithAbort(40);
    const elapsed = Date.now() - start;
    expect(elapsed).toBeGreaterThanOrEqual(35);
  });

  it('returns false immediately when signal is already aborted', async () => {
    const c = new AbortController();
    c.abort();
    const start = Date.now();
    const ok = await sleepWithAbort(1_000, c.signal);
    expect(ok).toBe(false);
    expect(Date.now() - start).toBeLessThan(50);
  });

  it('returns false when signal aborts mid-sleep', async () => {
    const c = new AbortController();
    const settled = sleepWithAbort(500, c.signal).then((v) => v);
    setTimeout(() => c.abort(), 20);
    expect(await settled).toBe(false);
  });
});

// ---------------------------------------------------------------------------
// safeKeyIdRef
// ---------------------------------------------------------------------------

describe('safeKeyIdRef', () => {
  it('truncates long key ids and never leaks the full value', () => {
    const got = safeKeyIdRef('arn:aws:kms:us-east-1:123456789012:key/abcdef01');
    expect(got.startsWith('...')).toBe(true);
    expect(got).toContain('abcdef01');
    expect(got).not.toContain('123456789012');
  });
  it('returns "(none)" for falsy', () => {
    expect(safeKeyIdRef(undefined)).toBe('(none)');
    expect(safeKeyIdRef('')).toBe('(none)');
    expect(safeKeyIdRef(null)).toBe('(none)');
  });
  it('returns short key id unchanged', () => {
    expect(safeKeyIdRef('alias/X')).toBe('alias/X');
  });
});

// ---------------------------------------------------------------------------
// IdempotencyCache
// ---------------------------------------------------------------------------

describe('IdempotencyCache', () => {
  it('returns undefined when disabled', () => {
    const c = new IdempotencyCache(8, 0);
    c.put('k', Buffer.from([1]));
    expect(c.get('k')).toBeUndefined();
  });

  it('stores and retrieves values', () => {
    let nowMs = 0;
    const c = new IdempotencyCache(8, 60_000, () => nowMs);
    c.put('k', Buffer.from([1, 2, 3]));
    nowMs += 1000;
    expect(c.get('k')).toEqual(Buffer.from([1, 2, 3]));
    expect(c.size()).toBe(1);
  });

  it('expires entries by TTL', () => {
    let nowMs = 0;
    const c = new IdempotencyCache(8, 1000, () => nowMs);
    c.put('k', Buffer.from([1]));
    nowMs += 1001;
    expect(c.get('k')).toBeUndefined();
    expect(c.size()).toBe(0);
  });

  it('evicts LRU entries when full', () => {
    let nowMs = 0;
    const c = new IdempotencyCache(2, 60_000, () => nowMs);
    c.put('a', Buffer.from([1]));
    c.put('b', Buffer.from([2]));
    nowMs += 10;
    c.get('a'); // touches a so b becomes oldest
    c.put('c', Buffer.from([3]));
    expect(c.get('b')).toBeUndefined();
    expect(c.get('a')).toEqual(Buffer.from([1]));
    expect(c.get('c')).toEqual(Buffer.from([3]));
  });

  it('clones caller-supplied buffer on put so caller mutation is invisible', () => {
    const c = new IdempotencyCache(8, 60_000);
    const buf = Buffer.from([0xff]);
    c.put('k', buf);
    buf[0] = 0x00;
    expect(c.get('k')?.[0]).toBe(0xff);
  });
});

describe('idempotencyKey', () => {
  it('omits raw payload bytes from the key string', () => {
    const k = idempotencyKey('alias/A', Buffer.from('super-secret-message'));
    expect(k.includes('super-secret-message')).toBe(false);
  });
  it('distinguishes key id and message', () => {
    const a = idempotencyKey('alias/A', Buffer.from('m'));
    const b = idempotencyKey('alias/B', Buffer.from('m'));
    const c = idempotencyKey('alias/A', Buffer.from('n'));
    expect(a).not.toBe(b);
    expect(a).not.toBe(c);
    expect(a).toMatch(/^v1:alias\/A:[a-f0-9]{64}$/);
  });
});

// ---------------------------------------------------------------------------
// loadKmsRetryConfigFromEnv
// ---------------------------------------------------------------------------

describe('loadKmsRetryConfigFromEnv', () => {
  afterEach(() => {
    for (const k of [
      'GLASSBOX_KMS_MAX_RETRIES',
      'GLASSBOX_KMS_INITIAL_BACKOFF_MS',
      'GLASSBOX_KMS_MAX_BACKOFF_MS',
      'GLASSBOX_KMS_JITTER_FRACTION',
      'GLASSBOX_KMS_IDEMPOTENCY_TTL_MS',
      'GLASSBOX_KMS_IDEMPOTENCY_MAX',
    ]) {
      delete process.env[k];
    }
  });

  it('returns defaults when no env vars are set', () => {
    expect(loadKmsRetryConfigFromEnv()).toEqual(defaultKmsRetryConfig());
  });

  it('honours each env var', () => {
    process.env.GLASSBOX_KMS_MAX_RETRIES = '5';
    process.env.GLASSBOX_KMS_INITIAL_BACKOFF_MS = '100';
    process.env.GLASSBOX_KMS_MAX_BACKOFF_MS = '1500';
    process.env.GLASSBOX_KMS_JITTER_FRACTION = '0.3';
    process.env.GLASSBOX_KMS_IDEMPOTENCY_TTL_MS = '30000';
    process.env.GLASSBOX_KMS_IDEMPOTENCY_MAX = '128';
    const cfg = loadKmsRetryConfigFromEnv();
    expect(cfg.maxRetries).toBe(5);
    expect(cfg.initialBackoffMs).toBe(100);
    expect(cfg.maxBackoffMs).toBe(1500);
    expect(cfg.jitterFraction).toBe(0.3);
    expect(cfg.idempotencyTtlMs).toBe(30_000);
    expect(cfg.idempotencyMaxEntries).toBe(128);
  });

  it('ignores garbage values', () => {
    process.env.GLASSBOX_KMS_MAX_RETRIES = 'not-a-number';
    const cfg = loadKmsRetryConfigFromEnv();
    expect(cfg.maxRetries).toBe(defaultKmsRetryConfig().maxRetries);
  });
});

// ---------------------------------------------------------------------------
// KmsSigner.signWithMetadata
// ---------------------------------------------------------------------------

describe('KmsSigner.signWithMetadata', () => {
  function makeSigner(
    module: MockKmsHandle['module'],
    overrides: Partial<{
      logFn: KmsLogFunction;
      retryConfig: Partial<typeof defaultKmsRetryConfig.prototype>;
    }> = {},
  ) {
    return new KmsSigner({
      keyId: 'alias/test-key',
      region: 'us-east-1',
      kmsModuleOverride: module,
      logFn: overrides.logFn,
      retryConfig: {
        maxRetries: 3,
        initialBackoffMs: 1,
        maxBackoffMs: 5,
        jitterFraction: 0,
        idempotencyTtlMs: 60_000,
        idempotencyMaxEntries: 4,
        ...overrides.retryConfig,
      },
      jitterUnit: ZERO_JITTER,
    });
  }

  it('returns a successful signature after a few retries', async () => {
    const internalErr = new Error('boom');
    internalErr.name = 'InternalError';
    (internalErr as any).code = 'InternalError';
    const mock = buildMockKms({
      signErrorSequence: [internalErr, internalErr, null],
      signature: Buffer.from('fake-sig'),
    });
    const cap = captureLogs();
    const signer = makeSigner(mock.module, { logFn: cap.fn });

    const result = await signer.signWithMetadata(Buffer.from('payload'), { correlationId: 'abc' });
    expect(result.meta.attempts).toBe(3);
    expect(result.meta.idempotencyHit).toBe(false);
    expect(result.signature?.toString()).toBe('fake-sig');
    expect(result.meta.retryable).toBe(false);
    expect(mock.callCount.sign).toBe(3);
  });

  it('preserves the SAME digest bytes across every retry', async () => {
    const internalErr = new Error('boom');
    internalErr.name = 'InternalError';
    (internalErr as any).code = 'InternalError';
    const mock = buildMockKms({
      signErrorSequence: [internalErr, internalErr, internalErr, null],
      signature: Buffer.from('ok'),
    });
    const signer = makeSigner(mock.module);

    const result = await signer.signWithMetadata(Buffer.from('canonical-digest'), {});
    expect(result.meta.attempts).toBe(4);

    const seen = mock.captured.map((c) => c.Message.toString('hex'));
    for (let i = 1; i < seen.length; i++) {
      expect(seen[i]).toBe(seen[0]);
    }
  });

  it('does not mutate the caller payload buffer', async () => {
    const internalErr = new Error('boom');
    internalErr.name = 'InternalError';
    (internalErr as any).code = 'InternalError';
    const mock = buildMockKms({
      signErrorSequence: [internalErr, internalErr, null],
    });
    const signer = makeSigner(mock.module);

    const payload = Buffer.from('do-not-mutate');
    const before = payload.toString('hex');
    await signer.signWithMetadata(payload, {});
    expect(payload.toString('hex')).toBe(before);
  });

  it('short-circuits with idempotencyHit on a repeat sign', async () => {
    const mock = buildMockKms({ signature: Buffer.from('s') });
    const signer = makeSigner(mock.module);

    const a = await signer.signWithMetadata(Buffer.from('x'), {});
    expect(a.meta.attempts).toBe(1);
    expect(a.meta.idempotencyHit).toBe(false);

    const b = await signer.signWithMetadata(Buffer.from('x'), {});
    expect(b.meta.idempotencyHit).toBe(true);
    expect(b.meta.attempts).toBe(0);
    expect(mock.callCount.sign).toBe(1);
    expect(signer.idempotencySize()).toBe(1);
  });

  it('cache key changes with key id so wrong-key cache is impossible', async () => {
    const mock = buildMockKms({ signature: Buffer.from('s') });
    const signer1 = makeSigner(mock.module);
    const signer2 = new KmsSigner({
      keyId: 'alias/different-key',
      region: 'us-east-1',
      kmsModuleOverride: mock.module,
      retryConfig: { idempotencyTtlMs: 60_000, idempotencyMaxEntries: 4 },
    });
    const payload = Buffer.from('m');

    await signer1.signWithMetadata(payload, {});
    await signer2.signWithMetadata(payload, {});
    expect(mock.callCount.sign).toBe(2);
  });

  it('throws KmsSignError with non-retryable metadata for AccessDenied', async () => {
    const accessDenied = new Error('forbidden');
    accessDenied.name = 'AccessDeniedException';
    (accessDenied as any).code = 'AccessDeniedException';
    const mock = buildMockKms({ signErrorSequence: [accessDenied] });
    const signer = makeSigner(mock.module);

    await expect(signer.signWithMetadata(Buffer.from('p'), {})).rejects.toMatchObject({
      meta: {
        errorCode: 'AccessDeniedException',
        errorClass: 'api',
        attempts: 1,
        retryable: false,
      },
    });
    expect(mock.callCount.sign).toBe(1); // stop immediately, no retries
  });

  it('throws KmsSignError(cause=KmsEmptyMessageError) on empty payload', async () => {
    const mock = buildMockKms();
    const signer = makeSigner(mock.module);
    let captured: KmsSignError | undefined;
    try {
      await signer.signWithMetadata(Buffer.alloc(0), {});
    } catch (e) {
      captured = e as KmsSignError;
    }
    expect(captured).toBeInstanceOf(KmsSignError);
    expect(captured!.meta.errorCode).toBe('EmptyMessage');
    expect(captured!.meta.errorClass).toBe('input');
    expect(captured!.cause).toBeInstanceOf(KmsEmptyMessageError);
    expect(mock.callCount.sign).toBe(0);
  });

  it('throws KmsSignError(cause=KmsContextCancelledError) when an already-aborted signal is used', async () => {
    const internalErr = new Error('throttle');
    internalErr.name = 'InternalError';
    (internalErr as any).code = 'InternalError';
    const mock = buildMockKms({
      signErrorSequence: [internalErr, internalErr, internalErr, internalErr],
    });
    const signer = makeSigner(mock.module, {
      retryConfig: { initialBackoffMs: 100, maxBackoffMs: 100, maxRetries: 5 },
    });
    const c = new AbortController();
    c.abort();
    let captured: KmsSignError | undefined;
    try {
      await signer.signWithMetadata(Buffer.from('p'), { signal: c.signal });
    } catch (e) {
      captured = e as KmsSignError;
    }
    expect(captured).toBeInstanceOf(KmsSignError);
    expect(captured!.meta.errorCode).toBe('ContextCancelled');
    expect(captured!.cause).toBeInstanceOf(KmsContextCancelledError);
    // The controller is pre-aborted before the call begins. The retry
    // loop only checks the signal inside `sleepWithAbort` (which runs
    // between attempts), so attempt 0 always reaches `Sign`. The
    // abort is observed on attempt 1's backoff sleep, so the exact
    // count is 1.
    expect(mock.callCount.sign).toBe(1);
  });

  it('carries metadata on retry exhaustion', async () => {
    const internalErr = new Error('throttle');
    internalErr.name = 'InternalError';
    (internalErr as any).code = 'InternalError';
    const mock = buildMockKms({
      signErrorSequence: [internalErr, internalErr, internalErr, internalErr],
    });
    const signer = makeSigner(mock.module, {
      retryConfig: { initialBackoffMs: 1, maxBackoffMs: 1, maxRetries: 3 },
    });
    let captured: KmsSignError | undefined;
    try {
      await signer.signWithMetadata(Buffer.from('p'), { correlationId: 'corr-exh' });
    } catch (e) {
      captured = e as KmsSignError;
    }
    expect(captured).toBeInstanceOf(KmsSignError);
    expect(captured!.meta.attempts).toBe(4);
    expect(captured!.meta.errorCode).toBe('InternalError');
    expect(captured!.meta.retryable).toBe(true);
    expect(captured!.meta.correlationId).toBe('corr-exh');
    expect(mock.callCount.sign).toBe(4);
  });

  it('backoff actually doubles (not flat) across iterations', async () => {
    const internalErr = new Error('throttle');
    internalErr.name = 'InternalError';
    (internalErr as any).code = 'InternalError';
    const mock = buildMockKms({
      signErrorSequence: [internalErr, internalErr, internalErr, null],
    });
    const delays: number[] = [];
    const signer = new KmsSigner({
      keyId: 'alias/X',
      region: 'us-east-1',
      kmsModuleOverride: mock.module,
      retryConfig: {
        maxRetries: 3,
        initialBackoffMs: 50,
        maxBackoffMs: 1000,
        jitterFraction: 0,
        idempotencyTtlMs: 60_000,
        idempotencyMaxEntries: 4,
      },
      jitterUnit: ZERO_JITTER,
      logFn: (_l, _m, attrs) => {
        if (typeof attrs.backoff_ms === 'number') delays.push(attrs.backoff_ms);
      },
    });
    await signer.signWithMetadata(Buffer.from('p'), {});
    // 3 retries means 3 backoff log lines. With jitter=0 the configured
    // initial=50 should produce 50, 100, 200 (not 50, 50, 50).
    expect(delays).toEqual([50, 100, 200]);
  });

  it('applies jitter on the first backoff so a fleet of retriers does not synchronise', async () => {
    // Verifies parity with the Go implementation: when jitterFraction > 0,
    // the first backoff (returned by nextRetryBackoffMs with currentMs=0)
    // is the configured initialBackoffMs plus optional proportional jitter,
    // not a flat InitialBackoff.
    const j = () => 1; // +jitterFraction * cap
    const cfg = {
      maxRetries: 0,
      initialBackoffMs: 100,
      maxBackoffMs: 200,
      jitterFraction: 0.5,
      idempotencyTtlMs: 60_000,
      idempotencyMaxEntries: 4,
    };
    expect(nextRetryBackoffMs(cfg, 0, j)).toBe(150); // 100 + 50%
  });

  it('public_keys() still works (no retry layer)', async () => {
    const mock = buildMockKms();
    const signer = makeSigner(mock.module);
    const pem = await signer.public_key();
    expect(pem.startsWith('-----BEGIN PUBLIC KEY-----')).toBe(true);
    expect(mock.callCount.publicKey).toBe(1);
  });

  it('sign() wrapper returns Buffer on success', async () => {
    const mock = buildMockKms({ signature: Buffer.from('ok') });
    const signer = makeSigner(mock.module);
    const sig = await signer.sign(Buffer.from('payload'));
    expect(Buffer.isBuffer(sig)).toBe(true);
    expect(sig.toString()).toBe('ok');
  });

  it('sign() wrapper rethrows the inner cause on failure', async () => {
    const accessDenied = new Error('forbidden');
    accessDenied.name = 'AccessDeniedException';
    (accessDenied as any).code = 'AccessDeniedException';
    const mock = buildMockKms({ signErrorSequence: [accessDenied] });
    const signer = makeSigner(mock.module);
    await expect(signer.sign(Buffer.from('p'))).rejects.toBe(accessDenied);
  });
});

// ---------------------------------------------------------------------------
// Log invariants — never leak payload, full key id, or signature bytes
// ---------------------------------------------------------------------------

describe('KmsSigner log invariants', () => {
  it('never logs the raw payload, full key id, or signature bytes', async () => {
    const internalErr = new Error('boom');
    internalErr.name = 'InternalError';
    (internalErr as any).code = 'InternalError';
    const mock = buildMockKms({
      signErrorSequence: [internalErr, null],
      signature: Buffer.from([0xca, 0xfe, 0xba, 0xbe]),
    });
    const cap = captureLogs();
    const signer = new KmsSigner({
      keyId: 'arn:aws:kms:us-east-1:123456789012:key/abcdef',
      region: 'us-east-1',
      kmsModuleOverride: mock.module,
      retryConfig: {
        maxRetries: 3,
        initialBackoffMs: 1,
        maxBackoffMs: 5,
        jitterFraction: 0,
        idempotencyTtlMs: 60_000,
        idempotencyMaxEntries: 4,
      },
      logFn: cap.fn,
      jitterUnit: ZERO_JITTER,
    });

    const payload = Buffer.from('super-secret-canonical-payload');
    await signer.signWithMetadata(payload, { correlationId: 'corr-leak-test' });

    for (const line of cap.lines) {
      expect(line.msg.includes('super-secret-canonical-payload')).toBe(false);
      const attrsFlat = Object.entries(line.attrs)
        .map(([_, v]) => (typeof v === 'string' ? v : ''))
        .join('|');
      expect(attrsFlat.includes('super-secret-canonical-payload')).toBe(false);
      expect(line.msg.includes('arn:aws:kms:us-east-1:123456789012:key/abcdef')).toBe(false);
      for (const v of Object.values(line.attrs)) {
        if (typeof v === 'string') {
          expect(v).not.toBe('arn:aws:kms:us-east-1:123456789012:key/abcdef');
        }
      }
      const all = Object.values(line.attrs)
        .map((v) => (typeof v === 'string' ? v : ''))
        .join('|');
      expect(all.includes('cafebabe')).toBe(false);
      expect(all.includes('CAFEBABE')).toBe(false);
    }
  });

  it('uses safeKeyIdRef in log attrs (truncated, no full ARN)', async () => {
    const mock = buildMockKms({ signature: Buffer.from('ok') });
    const cap = captureLogs();
    const signer = new KmsSigner({
      keyId: 'arn:aws:kms:us-east-1:123456789012:key/abcdef01',
      region: 'us-east-1',
      kmsModuleOverride: mock.module,
      logFn: cap.fn,
      jitterUnit: ZERO_JITTER,
    });
    await signer.signWithMetadata(Buffer.from('p'), {});
    const keyRefs = cap.lines
      .map((l) => l.attrs.key_ref)
      .filter((v): v is string => typeof v === 'string');
    expect(keyRefs.length).toBeGreaterThan(0);
    for (const r of keyRefs) {
      expect(r.includes('123456789012')).toBe(false);
      expect(r.endsWith('abcdef01')).toBe(true);
    }
  });
});

// ---------------------------------------------------------------------------
// Idempotency cache TTL — short TTL evicts
// ---------------------------------------------------------------------------

describe('KmsSigner idempotency TTL', () => {
  it('forgets cached signature after TTL elapses', async () => {
    let nowMs = 0;
    const mock = buildMockKms({ signature: Buffer.from('s') });
    const signer = new KmsSigner({
      keyId: 'alias/X',
      region: 'us-east-1',
      kmsModuleOverride: mock.module,
      retryConfig: {
        maxRetries: 0,
        initialBackoffMs: 0,
        maxBackoffMs: 0,
        idempotencyTtlMs: 100,
        idempotencyMaxEntries: 4,
      },
      now: () => nowMs,
    });
    const a = await signer.signWithMetadata(Buffer.from('m'), {});
    expect(a.meta.idempotencyHit).toBe(false);
    nowMs += 50;
    const b = await signer.signWithMetadata(Buffer.from('m'), {});
    expect(b.meta.idempotencyHit).toBe(true);
    nowMs += 200;
    const c = await signer.signWithMetadata(Buffer.from('m'), {});
    expect(c.meta.idempotencyHit).toBe(false); // expired
    expect(mock.callCount.sign).toBe(2);
  });
});