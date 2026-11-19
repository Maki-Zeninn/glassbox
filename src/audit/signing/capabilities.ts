// Copyright (c) glassbox Authors.
// SPDX-License-Identifier: Apache-2.0

/**
 * Signing capability detection for the current runtime.
 *
 * Browser environments cannot load PKCS#11 or AWS KMS providers because
 * those depend on Node.js native modules. This module provides explicit
 * discovery so callers get a clear error rather than a module-load crash.
 */

import type { SigningProvider } from './factory';

// ─── Error type ───────────────────────────────────────────────────────────────

/**
 * Thrown when a signing provider is requested in an environment where it is
 * not available (e.g. PKCS#11 in a browser bundle).
 */
export class UnsupportedProviderError extends Error {
  readonly provider: SigningProvider | string;

  constructor(provider: SigningProvider | string, context?: string) {
    const ctx = context ? ` (${context})` : '';
    super(
      `Signing provider "${provider}" is not supported in this environment${ctx}. ` +
      'Browser environments support only Ed25519 verification via SubtleCrypto. ' +
      'Use the Node.js runtime for signing operations.',
    );
    this.name = 'UnsupportedProviderError';
    this.provider = provider;
  }
}

// ─── Runtime detection ────────────────────────────────────────────────────────

/**
 * Returns true when running in a Node.js process.
 * Conservatively false when running in a browser or Deno.
 */
export function isNodeEnvironment(): boolean {
  return (
    typeof process !== 'undefined' &&
    typeof process.versions !== 'undefined' &&
    typeof process.versions.node === 'string'
  );
}

// ─── Capability report ────────────────────────────────────────────────────────

export interface SigningCapabilityReport {
  /** Software Ed25519 signing (Node.js `crypto` module). */
  software: boolean;
  /** PKCS#11 HSM signing (native `pkcs11js` module). */
  pkcs11: boolean;
  /** AWS KMS signing (`@aws-sdk/client-kms`). */
  awsKms: boolean;
  /** Ed25519 verification via browser SubtleCrypto. */
  browserVerify: boolean;
  /** Whether we are running in a Node.js environment. */
  isNode: boolean;
}

/**
 * Discover which signing/verification providers are available in the current
 * runtime. This is synchronous and safe to call in any environment.
 *
 * @example
 * const caps = getSigningCapabilities();
 * if (!caps.pkcs11) {
 *   throw new UnsupportedProviderError('pkcs11');
 * }
 */
export function getSigningCapabilities(): SigningCapabilityReport {
  const node = isNodeEnvironment();

  // pkcs11js is a native addon — only discoverable at runtime in Node.
  let pkcs11 = false;
  if (node) {
    try {
      require('pkcs11js');
      pkcs11 = true;
    } catch {
      pkcs11 = false;
    }
  }

  // @aws-sdk/client-kms is Node-only as well.
  let awsKms = false;
  if (node) {
    try {
      require('@aws-sdk/client-kms');
      awsKms = true;
    } catch {
      awsKms = false;
    }
  }

  // Browser SubtleCrypto is available in modern browsers and Node ≥ 16.
  const browserVerify =
    typeof globalThis !== 'undefined' &&
    typeof (globalThis as any).crypto?.subtle?.verify === 'function';

  return {
    software: node,
    pkcs11,
    awsKms,
    browserVerify,
    isNode: node,
  };
}

/**
 * Assert that the requested provider is available, throwing
 * {@link UnsupportedProviderError} with a clear message if not.
 */
export function assertProviderSupported(
  provider: SigningProvider | string,
  caps: SigningCapabilityReport = getSigningCapabilities(),
): void {
  switch (provider) {
    case 'software':
      if (!caps.software) {
        throw new UnsupportedProviderError(provider, 'Node.js crypto module required');
      }
      break;
    case 'pkcs11':
      if (!caps.pkcs11) {
        throw new UnsupportedProviderError(
          provider,
          caps.isNode
            ? 'pkcs11js native module not installed'
            : 'browser environment',
        );
      }
      break;
    case 'kms':
      if (!caps.awsKms) {
        throw new UnsupportedProviderError(
          provider,
          caps.isNode
            ? '@aws-sdk/client-kms not installed'
            : 'browser environment',
        );
      }
      break;
    default:
      throw new UnsupportedProviderError(provider, 'unknown provider');
  }
}
