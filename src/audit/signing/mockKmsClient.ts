// Copyright (c) 2026 dotandev
// SPDX-License-Identifier: MIT OR Apache-2.0

/**
 * mockKmsClient — deterministic mock for @aws-sdk/client-kms [Issue #805].
 *
 * Provides a drop-in replacement for the real KMS module so tests can
 * verify retry logic, error classification, correlation metadata, and
 * key-identity auditability without network access and without requiring
 * the optional @aws-sdk/client-kms package to be installed.
 *
 * Usage:
 *
 *   import { buildMockKmsModule, MockKmsClient } from './mockKmsClient';
 *
 *   const mock = buildMockKmsModule({
 *     signErrorSequence: [throttlingError, null],   // fail once then succeed
 *     signature: Buffer.from('expected-sig'),
 *   });
 *
 *   const signer = new KmsSigner({
 *     keyId: 'alias/test',
 *     region: 'us-east-1',
 *     kmsModuleOverride: mock.module,
 *   });
 *
 *   const result = await signer.signWithMetadata(digest, { correlationId: 'test-1' });
 *   // Inspect mock.callLog to verify correlation metadata and key identity
 *   // were passed correctly without leaking payload bytes.
 */

// ---------------------------------------------------------------------------
// Call log entry — records what was passed to Sign/GetPublicKey
// ---------------------------------------------------------------------------

export interface KmsSignCallRecord {
  readonly keyId: string;
  readonly signingAlgorithm: string;
  readonly messageType: string;
  /** The raw Message buffer as captured from the SDK input (digest bytes). */
  readonly messageHex: string;
}

export interface KmsGetPublicKeyCallRecord {
  readonly keyId: string;
}

export type KmsCallRecord =
  | ({ type: 'Sign' } & KmsSignCallRecord)
  | ({ type: 'GetPublicKey' } & KmsGetPublicKeyCallRecord);

// ---------------------------------------------------------------------------
// Mock builder options
// ---------------------------------------------------------------------------

export interface MockKmsOptions {
  /**
   * Sequence of errors to throw for successive Sign calls.
   * `null` means "succeed on this call".  When the sequence is exhausted
   * all subsequent Sign calls succeed.
   */
  signErrorSequence?: Array<Error | null>;
  /**
   * Fixed signature bytes returned on success.  Defaults to a stable
   * 6-byte sentinel so tests can assert the exact value.
   */
  signature?: Buffer;
  /** Fixed DER public key bytes returned by GetPublicKey. */
  publicKey?: Buffer;
  /** Optional error to throw on every GetPublicKey call. */
  publicKeyError?: Error;
}

// ---------------------------------------------------------------------------
// Mock implementation
// ---------------------------------------------------------------------------

export interface MockKmsModule {
  KMSClient: new (cfg?: { region?: string }) => MockKmsClientInstance;
  SignCommand: new (input: unknown) => { _commandType: 'Sign'; input: unknown };
  GetPublicKeyCommand: new (input: unknown) => { _commandType: 'GetPublicKey'; input: unknown };
}

export interface MockKmsClientInstance {
  send(command: unknown): Promise<unknown>;
}

export interface MockKmsHandle {
  /** The mock module to pass as `kmsModuleOverride` to KmsSigner. */
  readonly module: MockKmsModule;
  /** Total number of Sign calls made so far. */
  readonly signCallCount: () => number;
  /** Total number of GetPublicKey calls made so far. */
  readonly publicKeyCallCount: () => number;
  /** Ordered log of all Sign/GetPublicKey inputs (safe to assert on). */
  readonly callLog: () => readonly KmsCallRecord[];
}

/**
 * Builds a deterministic mock KMS module for use in KmsSigner tests.
 *
 * Design principles [Issue #805]:
 * - Capture every Sign input by value so tests can assert that the
 *   Message bytes are identical across all retry attempts (no mutation,
 *   no recomputation).
 * - Never leak plaintext payload — store Message as hex in the call log
 *   so the test can inspect without holding a live Buffer reference.
 * - Expose call counts and inputs separately from error configuration
 *   so tests that care only about error paths don't have to inspect logs.
 */
export function buildMockKmsModule(opts: MockKmsOptions = {}): MockKmsHandle {
  const signature = opts.signature ?? Buffer.from([0xDE, 0xAD, 0xBE, 0xEF, 0x00, 0x01]);
  const publicKey =
    opts.publicKey ??
    Buffer.concat([Buffer.from([0x30, 0x2a]), Buffer.alloc(40, 0xcc)]);
  const errorSequence: Array<Error | null> = opts.signErrorSequence ?? [];

  let signCalls = 0;
  let pkCalls = 0;
  const log: KmsCallRecord[] = [];

  class SignCommand {
    readonly _commandType = 'Sign' as const;
    constructor(public readonly input: any) {}
  }

  class GetPublicKeyCommand {
    readonly _commandType = 'GetPublicKey' as const;
    constructor(public readonly input: any) {}
  }

  class MockKmsClientImpl implements MockKmsClientInstance {
    async send(command: any): Promise<unknown> {
      if (command instanceof SignCommand) {
        const idx = signCalls++;
        // Capture Message as hex so the call log is safe to inspect and
        // print without risking payload leakage in test output.
        const msgHex = Buffer.isBuffer(command.input.Message)
          ? command.input.Message.toString('hex')
          : Buffer.from(command.input.Message as Uint8Array).toString('hex');

        log.push({
          type: 'Sign',
          keyId: String(command.input.KeyId ?? ''),
          signingAlgorithm: String(command.input.SigningAlgorithm ?? ''),
          messageType: String(command.input.MessageType ?? ''),
          messageHex: msgHex,
        });

        const scheduled = errorSequence[idx];
        if (scheduled != null) throw scheduled;
        return { Signature: Buffer.from(signature) };
      }

      if (command instanceof GetPublicKeyCommand) {
        pkCalls++;
        log.push({
          type: 'GetPublicKey',
          keyId: String(command.input.KeyId ?? ''),
        });
        if (opts.publicKeyError) throw opts.publicKeyError;
        return { PublicKey: Buffer.from(publicKey) };
      }

      throw new Error(
        `MockKmsClient: unexpected command type "${command?.constructor?.name ?? typeof command}"`,
      );
    }
  }

  const MockKMSClient = MockKmsClientImpl as unknown as new (
    cfg?: { region?: string },
  ) => MockKmsClientInstance;

  return {
    module: {
      KMSClient: MockKMSClient,
      SignCommand: SignCommand as unknown as MockKmsModule['SignCommand'],
      GetPublicKeyCommand:
        GetPublicKeyCommand as unknown as MockKmsModule['GetPublicKeyCommand'],
    },
    signCallCount: () => signCalls,
    publicKeyCallCount: () => pkCalls,
    callLog: () => log as readonly KmsCallRecord[],
  };
}

// ---------------------------------------------------------------------------
// Helpers for building standard AWS-style error objects
// ---------------------------------------------------------------------------

/**
 * Creates an AWS SDK-style throttling error.
 * Use in signErrorSequence to simulate KMS throttling retries.
 */
export function kmsThrottlingError(message = 'Rate exceeded'): Error {
  const err = new Error(message);
  err.name = 'ThrottlingException';
  (err as any).code = 'ThrottlingException';
  return err;
}

/**
 * Creates an AWS SDK-style transient error (InternalError / ServiceUnavailable).
 */
export function kmsTransientError(
  code: 'InternalError' | 'ServiceUnavailable' | 'KMSInternalException' = 'InternalError',
  message = 'Service unavailable',
): Error {
  const err = new Error(message);
  err.name = code;
  (err as any).code = code;
  return err;
}

/**
 * Creates an AWS SDK-style authorization error.
 * These must NOT be retried.
 */
export function kmsUnauthorizedError(
  code:
    | 'AccessDeniedException'
    | 'DisabledException'
    | 'InvalidKeyIdException'
    | 'NotFoundException'
    | 'InvalidGrantException' = 'AccessDeniedException',
  message = 'Access denied',
): Error {
  const err = new Error(message);
  err.name = code;
  (err as any).code = code;
  return err;
}

/**
 * Creates a network-level error (ECONNRESET, ECONNREFUSED, etc.).
 * These are retryable transport failures.
 */
export function kmsNetworkError(
  name: 'ECONNRESET' | 'ECONNREFUSED' | 'ENOTFOUND' | 'TimeoutError' | 'NetworkingError' =
    'ECONNRESET',
  message = 'socket hang up',
): Error {
  const err = new Error(message);
  err.name = name;
  return err;
}
