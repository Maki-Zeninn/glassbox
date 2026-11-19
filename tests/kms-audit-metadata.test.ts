// Copyright (c) 2026 dotandev
// SPDX-License-Identifier: MIT OR Apache-2.0

/**
 * Tests for KMS signing audit metadata and structured error codes [Issue #805].
 *
 * Verifies:
 * 1. Correlation ID is threaded through every retry attempt without leakage.
 * 2. Key identity metadata (key_ref) uses the safe truncated suffix.
 * 3. Per-attempt sign calls carry the SAME digest bytes (determinism).
 * 4. Throttling errors after retry exhaustion carry stable errorCode.
 * 5. Authorization errors stop immediately without retrying.
 * 6. Error metadata carries the correlation ID for traceability.
 * 7. Successful metadata includes elapsedMs and attempts.
 * 8. buildMockKmsModule call log records key id and algorithm correctly.
 */

import { KmsSigner, KmsSignError } from '../src/audit/signing/kmsSigner';
import {
  buildMockKmsModule,
  kmsThrottlingError,
  kmsTransientError,
  kmsUnauthorizedError,
  kmsNetworkError,
} from '../src/audit/signing/mockKmsClient';
import type { KmsLogFunction } from '../src/audit/signing/kmsRetry';

// ---------------------------------------------------------------------------
// Log capture helper
// ---------------------------------------------------------------------------

interface CapturedLogLine {
  level: 'debug' | 'warn';
  msg: string;
  attrs: Record<string, unknown>;
}

function captureLog(): { fn: KmsLogFunction; lines: CapturedLogLine[] } {
  const lines: CapturedLogLine[] = [];
  return {
    lines,
    fn: (level, msg, attrs) => lines.push({ level, msg, attrs: { ...attrs } }),
  };
}

// Zero-jitter function for deterministic backoff in tests.
const ZERO_JITTER = () => 0;

// Minimal retry config for fast tests.
const FAST_RETRY = {
  maxRetries: 3,
  initialBackoffMs: 1,
  maxBackoffMs: 2,
  jitterFraction: 0,
  idempotencyTtlMs: 60_000,
  idempotencyMaxEntries: 8,
};

function makeSigner(
  mockModule: ReturnType<typeof buildMockKmsModule>['module'],
  overrides: {
    keyId?: string;
    logFn?: KmsLogFunction;
    retryConfig?: Partial<typeof FAST_RETRY>;
  } = {},
) {
  return new KmsSigner({
    keyId: overrides.keyId ?? 'alias/glassbox-audit-key',
    region: 'us-east-1',
    kmsModuleOverride: mockModule,
    logFn: overrides.logFn,
    retryConfig: { ...FAST_RETRY, ...(overrides.retryConfig ?? {}) },
    jitterUnit: ZERO_JITTER,
  });
}

// ---------------------------------------------------------------------------
// 1. Correlation ID threading
// ---------------------------------------------------------------------------

describe('KMS signing — correlation ID threading [Issue #805]', () => {
  it('logs correlation_id on every attempt and on success', async () => {
    const mock = buildMockKmsModule({
      signErrorSequence: [kmsTransientError(), null],
      signature: Buffer.from('ok'),
    });
    const cap = captureLog();
    const signer = makeSigner(mock.module, { logFn: cap.fn });

    const result = await signer.signWithMetadata(Buffer.from('payload'), {
      correlationId: 'trace-abc-123',
    });

    expect(result.meta.correlationId).toBe('trace-abc-123');

    const withCorr = cap.lines.filter(
      (l) => l.attrs.correlation_id === 'trace-abc-123',
    );
    expect(withCorr.length).toBeGreaterThanOrEqual(2); // retry + success
  });

  it('empty correlationId is echoed back as empty string', async () => {
    const mock = buildMockKmsModule({ signature: Buffer.from('sig') });
    const signer = makeSigner(mock.module);
    const result = await signer.signWithMetadata(Buffer.from('p'), {});
    expect(result.meta.correlationId).toBe('');
  });

  it('carries correlation_id in KmsSignError.meta on failure', async () => {
    const mock = buildMockKmsModule({
      signErrorSequence: [kmsUnauthorizedError()],
    });
    const signer = makeSigner(mock.module, {
      retryConfig: { maxRetries: 0 },
    });

    let caught: KmsSignError | undefined;
    try {
      await signer.signWithMetadata(Buffer.from('p'), { correlationId: 'fail-trace' });
    } catch (e) {
      caught = e as KmsSignError;
    }

    expect(caught).toBeInstanceOf(KmsSignError);
    expect(caught!.meta.correlationId).toBe('fail-trace');
  });
});

// ---------------------------------------------------------------------------
// 2. Key identity auditability — safeKeyIdRef in logs
// ---------------------------------------------------------------------------

describe('KMS signing — key identity auditability [Issue #805]', () => {
  it('logs key_ref as a truncated suffix, never the full ARN', async () => {
    const fullArn = 'arn:aws:kms:us-east-1:123456789012:key/abcdef01-1234-5678-abcd-efghij012345';
    const mock = buildMockKmsModule({ signature: Buffer.from('s') });
    const cap = captureLog();
    const signer = makeSigner(mock.module, { keyId: fullArn, logFn: cap.fn });

    await signer.signWithMetadata(Buffer.from('p'), {});

    const keyRefs = cap.lines
      .map((l) => l.attrs.key_ref)
      .filter((v): v is string => typeof v === 'string');

    expect(keyRefs.length).toBeGreaterThan(0);
    for (const ref of keyRefs) {
      expect(ref.includes('123456789012')).toBe(false); // account id must be stripped
      expect(ref.startsWith('...')).toBe(true);          // truncation prefix
    }
  });

  it('call log records key id verbatim for audit trail', async () => {
    const mock = buildMockKmsModule({ signature: Buffer.from('s') });
    const signer = makeSigner(mock.module, { keyId: 'alias/my-key' });

    await signer.signWithMetadata(Buffer.from('p'), {});

    const signCalls = mock.callLog().filter((c) => c.type === 'Sign');
    expect(signCalls.length).toBe(1);
    expect((signCalls[0] as any).keyId).toBe('alias/my-key');
  });

  it('call log records signing algorithm for audit trail', async () => {
    const mock = buildMockKmsModule({ signature: Buffer.from('s') });
    const signer = makeSigner(mock.module);

    await signer.signWithMetadata(Buffer.from('p'), {});

    const signCalls = mock.callLog().filter((c) => c.type === 'Sign');
    expect((signCalls[0] as any).signingAlgorithm).toBeTruthy();
  });
});

// ---------------------------------------------------------------------------
// 3. Deterministic digest across retry attempts
// ---------------------------------------------------------------------------

describe('KMS signing — deterministic digest [Issue #805]', () => {
  it('sends identical messageHex on every retry attempt', async () => {
    const mock = buildMockKmsModule({
      signErrorSequence: [kmsTransientError(), kmsTransientError(), null],
      signature: Buffer.from('ok'),
    });
    const signer = makeSigner(mock.module);

    await signer.signWithMetadata(Buffer.from('canonical-payload'), {});

    const signCalls = mock.callLog().filter((c) => c.type === 'Sign') as Array<{
      type: 'Sign';
      messageHex: string;
    }>;
    expect(signCalls.length).toBe(3);
    const first = signCalls[0].messageHex;
    for (const call of signCalls) {
      expect(call.messageHex).toBe(first);
    }
  });

  it('messageHex does not equal the plaintext payload', async () => {
    const mock = buildMockKmsModule({ signature: Buffer.from('ok') });
    const signer = makeSigner(mock.module);
    const payload = Buffer.from('plaintext-do-not-log');

    await signer.signWithMetadata(payload, {});

    const signCalls = mock.callLog().filter((c) => c.type === 'Sign') as Array<{
      type: 'Sign';
      messageHex: string;
    }>;
    // The signer hashes the payload before passing to KMS.
    expect(signCalls[0].messageHex).not.toBe(payload.toString('hex'));
    // The raw payload must not appear in the hex string.
    expect(signCalls[0].messageHex.includes('706c61696e74657874')).toBe(false); // "plaintext" hex
  });
});

// ---------------------------------------------------------------------------
// 4. Throttling exhaustion → stable error code
// ---------------------------------------------------------------------------

describe('KMS signing — throttling stable error code [Issue #805]', () => {
  it('carries errorCode=ThrottlingException on exhaustion', async () => {
    const mock = buildMockKmsModule({
      signErrorSequence: [
        kmsThrottlingError(),
        kmsThrottlingError(),
        kmsThrottlingError(),
        kmsThrottlingError(),
      ],
    });
    const signer = makeSigner(mock.module, {
      retryConfig: { maxRetries: 3 },
    });

    let caught: KmsSignError | undefined;
    try {
      await signer.signWithMetadata(Buffer.from('p'), { correlationId: 'throttle-id' });
    } catch (e) {
      caught = e as KmsSignError;
    }

    expect(caught).toBeInstanceOf(KmsSignError);
    expect(caught!.meta.errorCode).toBe('ThrottlingException');
    expect(caught!.meta.errorClass).toBe('api');
    expect(caught!.meta.attempts).toBe(4);
    expect(caught!.meta.correlationId).toBe('throttle-id');
  });
});

// ---------------------------------------------------------------------------
// 5. Authorization errors do not retry
// ---------------------------------------------------------------------------

describe('KMS signing — authorization errors [Issue #805]', () => {
  it.each([
    'AccessDeniedException',
    'DisabledException',
    'InvalidKeyIdException',
    'NotFoundException',
    'InvalidGrantException',
  ] as const)('stops immediately for %s', async (code) => {
    const mock = buildMockKmsModule({
      signErrorSequence: [kmsUnauthorizedError(code)],
    });
    const signer = makeSigner(mock.module, { retryConfig: { maxRetries: 3 } });

    let caught: KmsSignError | undefined;
    try {
      await signer.signWithMetadata(Buffer.from('p'), { correlationId: 'authz' });
    } catch (e) {
      caught = e as KmsSignError;
    }

    expect(caught).toBeInstanceOf(KmsSignError);
    expect(caught!.meta.errorCode).toBe(code);
    expect(caught!.meta.retryable).toBe(false);
    // Must not retry: exactly 1 Sign call.
    expect(mock.signCallCount()).toBe(1);
  });
});

// ---------------------------------------------------------------------------
// 6. Network-level retryable errors
// ---------------------------------------------------------------------------

describe('KMS signing — network errors are retryable [Issue #805]', () => {
  it('retries ECONNRESET and succeeds', async () => {
    const mock = buildMockKmsModule({
      signErrorSequence: [kmsNetworkError('ECONNRESET'), null],
      signature: Buffer.from('network-ok'),
    });
    const signer = makeSigner(mock.module);

    const result = await signer.signWithMetadata(Buffer.from('p'), {});
    expect(result.meta.attempts).toBe(2);
    expect(result.signature?.toString()).toBe('network-ok');
    expect(mock.signCallCount()).toBe(2);
  });
});

// ---------------------------------------------------------------------------
// 7. Successful metadata completeness
// ---------------------------------------------------------------------------

describe('KMS signing — successful metadata [Issue #805]', () => {
  it('returns elapsed_ms >= 0, attempts = 1, and non-empty signature on first-try success', async () => {
    const mock = buildMockKmsModule({ signature: Buffer.from('fast') });
    const signer = makeSigner(mock.module);

    const result = await signer.signWithMetadata(Buffer.from('p'), { correlationId: 'c1' });

    expect(result.meta.attempts).toBe(1);
    expect(result.meta.idempotencyHit).toBe(false);
    expect(result.meta.elapsedMs).toBeGreaterThanOrEqual(0);
    expect(result.meta.retryable).toBe(false);
    expect(result.meta.errorCode).toBeUndefined();
    expect(Buffer.isBuffer(result.signature)).toBe(true);
  });

  it('idempotencyHit=true on second call with same digest', async () => {
    const mock = buildMockKmsModule({ signature: Buffer.from('cached') });
    const signer = makeSigner(mock.module);

    const first = await signer.signWithMetadata(Buffer.from('p'), {});
    const second = await signer.signWithMetadata(Buffer.from('p'), {});

    expect(first.meta.attempts).toBe(1);
    expect(second.meta.idempotencyHit).toBe(true);
    expect(second.meta.attempts).toBe(0);
    expect(mock.signCallCount()).toBe(1); // KMS called exactly once
  });
});

// ---------------------------------------------------------------------------
// 8. buildMockKmsModule call log fidelity
// ---------------------------------------------------------------------------

describe('buildMockKmsModule call log [Issue #805]', () => {
  it('records GetPublicKey calls with key id', async () => {
    const mock = buildMockKmsModule();
    const signer = makeSigner(mock.module, { keyId: 'alias/audit-key' });

    await signer.public_key();

    const pkCalls = mock.callLog().filter((c) => c.type === 'GetPublicKey');
    expect(pkCalls.length).toBe(1);
    expect((pkCalls[0] as any).keyId).toBe('alias/audit-key');
  });

  it('call log message hex is a valid sha512 hex string (64 bytes = 128 hex chars)', async () => {
    const mock = buildMockKmsModule({ signature: Buffer.from('s') });
    const signer = makeSigner(mock.module);

    await signer.signWithMetadata(Buffer.from('payload-for-digest'), {});

    const signCalls = mock.callLog().filter((c) => c.type === 'Sign') as Array<{
      messageHex: string;
    }>;
    // KmsSigner hashes with SHA-512 internally before calling Sign.
    expect(signCalls[0].messageHex).toHaveLength(128); // 64 bytes = 128 hex chars
    expect(/^[0-9a-f]+$/.test(signCalls[0].messageHex)).toBe(true);
  });
});
