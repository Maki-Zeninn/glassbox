// Copyright (c) glassbox Authors.
// SPDX-License-Identifier: Apache-2.0

/**
 * Browser-safe audit verification bundle entry point.
 *
 * This entry point exports ONLY the pure canonicalization and Web-Crypto-based
 * verification APIs. It does NOT re-export:
 *   - AuditLogger (depends on Node.js `crypto` for signing)
 *   - SoftwareEd25519Signer (depends on Node.js `crypto`)
 *   - Pkcs11Signer (depends on native pkcs11js)
 *   - KmsSigner (depends on @aws-sdk/client-kms)
 *
 * Bundle size target: < 10 KiB minified+gzipped.
 * The only runtime dependency is the browser's built-in SubtleCrypto API.
 *
 * @module glassbox/audit/browser
 *
 * @example
 * import {
 *   verifyAuditLogBrowser,
 *   getProviderCapabilities,
 * } from 'glassbox/src/audit/browser';
 *
 * const caps = await getProviderCapabilities();
 * if (!caps.ed25519Verify) {
 *   console.warn('Ed25519 not supported in this browser');
 * }
 * const result = await verifyAuditLogBrowser(signedLog);
 * console.log(result.valid); // true / false
 */

export {
  verifyAuditLogBrowser,
  getProviderCapabilities,
} from './browserVerifier';

export type {
  BrowserVerificationResult,
  ProviderCapabilities,
} from './browserVerifier';

export {
  canonicalStringify,
  buildAuditHashInput,
} from './canonicalize';

// Re-export the UnsupportedProviderError so browser consumers can catch it
// without importing from the Node-only signing package.
export { UnsupportedProviderError } from './unsupportedProviderError';
