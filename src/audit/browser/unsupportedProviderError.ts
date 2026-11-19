// Copyright (c) glassbox Authors.
// SPDX-License-Identifier: Apache-2.0

/**
 * Browser-safe re-export of UnsupportedProviderError.
 *
 * This file contains no Node.js imports, making it safe to bundle for
 * browser targets. It mirrors the error class from the signing package
 * so browser consumers can catch provider errors without pulling in
 * Node-only dependencies.
 */

export class UnsupportedProviderError extends Error {
  readonly provider: string;

  constructor(provider: string, context?: string) {
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
